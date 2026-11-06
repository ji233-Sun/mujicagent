package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func TestReleaseHistory(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Accept") != "application/vnd.github+json" || r.Header.Get("User-Agent") != "MujicAgent" {
			t.Error("missing GitHub request headers")
		}
		fmt.Fprint(w, `[
			{"tag_name":"v0.3.0","draft":true},
			{"tag_name":"v0.2.0-beta","prerelease":true},
			{"tag_name":"v0.1.1","body":"## 更新\n\n- 签名更新","published_at":"2026-11-06T00:00:00Z"},
			{"tag_name":"v0.1.0","body":"First release"},
			{"body":"No tag"}
		]`)
	}))
	defer server.Close()
	releases, err := fetchReleases(context.Background(), server.Client(), server.URL)
	if err != nil {
		t.Fatal(err)
	}
	if len(releases) != 2 || releases[0].Tag != "v0.1.1" || releases[1].Tag != "v0.1.0" || releases[0].Published.IsZero() || releases[0].Notes != "## 更新\n\n- 签名更新" {
		t.Fatalf("incorrect stable release history: %+v", releases)
	}
}

func TestReleaseHistoryFailures(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    int
		body      string
		wantError bool
	}{
		{"empty repository", 200, "[]", false},
		{"rate limit", 403, "{}", true},
		{"server failure", 502, "Bad gateway", true},
		{"malformed response", 200, "{", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(tc.status); fmt.Fprint(w, tc.body) }))
			defer server.Close()
			releases, err := fetchReleases(context.Background(), server.Client(), server.URL)
			if (err != nil) != tc.wantError {
				t.Fatalf("releases=%+v, error=%v", releases, err)
			}
		})
	}
}

func TestServiceTierRequests(t *testing.T) {
	if os.Getenv("MUJICAGENT_SPEED_CHILD") == "1" {
		dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
		for {
			var m codex.Message
			if dec.Decode(&m) != nil {
				os.Exit(0)
			}
			var result any = map[string]any{}
			switch m.Method {
			case "initialized":
				continue
			case "permissionProfile/list":
				result = map[string]any{"data": []codex.PermissionProfile{{ID: ":read-only", Allowed: true}, {ID: ":workspace", Allowed: true}, {ID: ":danger-full-access", Allowed: true}}}
			case "experimentalFeature/list":
				result = map[string]any{"data": []any{map[string]any{"name": "guardian_approval", "enabled": true, "stage": "stable"}}}
			case "thread/start", "thread/resume":
				if os.WriteFile(os.Getenv("MUJICAGENT_SPEED_REQUEST")+"-thread", m.Params, 0600) != nil {
					os.Exit(2)
				}
				result = map[string]any{"thread": map[string]string{"id": "speed-thread"}}
			case "turn/start":
				if os.WriteFile(os.Getenv("MUJICAGENT_SPEED_REQUEST"), m.Params, 0600) != nil {
					os.Exit(2)
				}
				result = map[string]any{"turn": map[string]string{"id": "speed-turn"}}
			case "":
				data, _ := json.Marshal(m)
				if os.WriteFile(os.Getenv("MUJICAGENT_SPEED_REQUEST"), data, 0600) != nil {
					os.Exit(3)
				}
				continue
			}
			enc.Encode(map[string]any{"id": m.ID, "result": result})
		}
	}
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test launcher")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary, request := filepath.Join(dir, "codex"), filepath.Join(dir, "request.json")
	launcher := fmt.Sprintf("#!/bin/sh\nexec '%s' -test.run=^TestServiceTierRequests$ -- \"$@\"\n", strings.ReplaceAll(exe, "'", "'\\''"))
	if err := os.WriteFile(binary, []byte(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUJICAGENT_SPEED_CHILD", "1")
	t.Setenv("MUJICAGENT_SPEED_REQUEST", request)
	c := New(dir, model.New(), nil)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c.client, err = codex.Start(ctx, binary, func(codex.Message) {})
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Connection = "Connected"
	c.Models = []codex.Model{{Model: "speed-model", ServiceTiers: []codex.ServiceTier{{ID: "priority"}, {ID: "ultrafast"}}}, {Model: "plain-model"}}
	c.NewThread("")
	thread := c.State.Thread(c.State.Selected)
	for _, tc := range []struct{ model, selected, sent string }{
		{"speed-model", "priority", "priority"},
		{"speed-model", "ultrafast", "ultrafast"},
		{"speed-model", "", "default"},
		{"speed-model", "invented", "default"},
		{"plain-model", "priority", "default"},
		{"unknown-model", "ultrafast", "default"},
	} {
		c.State.Settings.Model, c.State.Settings.ServiceTier = tc.model, tc.selected
		if !c.Send("speed test", false) {
			t.Fatal(c.Error)
		}
		deadline := time.Now().Add(3 * time.Second)
		for thread.Turns[len(thread.Turns)-1].ID == "" && time.Now().Before(deadline) {
			c.Drain()
			time.Sleep(time.Millisecond)
		}
		if thread.Turns[len(thread.Turns)-1].ID == "" {
			t.Fatalf("turn did not start: %s", c.Error)
		}
		data, err := os.ReadFile(request)
		if err != nil {
			t.Fatal(err)
		}
		var params struct{ ThreadID, Model, ServiceTier, Summary string }
		if err := json.Unmarshal(data, &params); err != nil {
			t.Fatal(err)
		}
		if params.ServiceTier != tc.sent || params.Model != tc.model || params.ThreadID != "speed-thread" || params.Summary != "auto" {
			t.Fatalf("selection %+v sent incorrect turn/start: %s", tc, data)
		}
		var permissions struct {
			Permissions, ApprovalPolicy, ApprovalsReviewer string
			SandboxPolicy                                  any
		}
		if err := json.Unmarshal(data, &permissions); err != nil {
			t.Fatal(err)
		}
		if permissions.Permissions != ":workspace" || permissions.ApprovalPolicy != "on-request" || permissions.ApprovalsReviewer != "auto_review" || permissions.SandboxPolicy != nil {
			t.Fatalf("invalid default permissions: %s", data)
		}
		notify(c, "turn/completed", `{"threadId":"speed-thread","turn":{"id":"speed-turn","status":"completed"}}`)
	}
	for _, tc := range []struct{ profile, wireID, policy, reviewer string }{
		{":read-only", ":read-only", "on-request", "user"},
		{":approve-for-me", ":workspace", "on-request", "auto_review"},
		{":danger-full-access", ":danger-full-access", "never", "user"},
		{":workspace", ":workspace", "on-request", "user"},
	} {
		thread.PermissionProfile = tc.profile
		if tc.profile == ":danger-full-access" {
			c.State.Settings.DefaultPermissionProfile = tc.profile
			c.NewThread("")
			thread = c.State.Thread(c.State.Selected)
		}
		// Exercise resume as well as updates to an already loaded conversation.
		if tc.profile == ":danger-full-access" || tc.profile == ":approve-for-me" {
			c.loaded[thread.ID] = false
		}
		if !c.Send("permission test", false) {
			t.Fatal(c.Error)
		}
		deadline := time.Now().Add(3 * time.Second)
		for thread.Turns[len(thread.Turns)-1].ID == "" && time.Now().Before(deadline) {
			c.Drain()
			time.Sleep(time.Millisecond)
		}
		data, err := os.ReadFile(request)
		if err != nil {
			t.Fatal(err)
		}
		var p struct {
			Permissions, ApprovalPolicy, ApprovalsReviewer string
			SandboxPolicy                                  any
		}
		if err := json.Unmarshal(data, &p); err != nil {
			t.Fatal(err)
		}
		if p.Permissions != tc.wireID || p.ApprovalPolicy != tc.policy || p.ApprovalsReviewer != tc.reviewer || p.SandboxPolicy != nil {
			t.Fatalf("permission change not sent: %s", data)
		}
		if tc.profile == ":danger-full-access" || tc.profile == ":approve-for-me" {
			data, err = os.ReadFile(request + "-thread")
			if err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(data, &p); err != nil {
				t.Fatal(err)
			}
			if p.Permissions != tc.wireID || p.ApprovalPolicy != tc.policy || p.ApprovalsReviewer != tc.reviewer {
				t.Fatalf("resume did not restore the selected reviewer: %s", data)
			}
		}
		notify(c, "turn/completed", `{"threadId":"speed-thread","turn":{"id":"speed-turn","status":"completed"}}`)
	}
	for i, decision := range []string{`"acceptForSession"`, `{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git","status"]}}`, `{"applyNetworkPolicyAmendment":{"network_policy_amendment":{"host":"example.com","action":"allow"}}}`, `"cancel"`} {
		r := &codex.Request{ID: json.RawMessage(fmt.Sprintf(`"approval-%d"`, i)), Method: "item/commandExecution/requestApproval", AvailableDecisions: []json.RawMessage{json.RawMessage(decision)}}
		c.Requests = append(c.Requests, r)
		c.Approve(r, json.RawMessage(`"accept"`))
		if r.Responding {
			t.Fatal("accepted a decision absent from the server's list")
		}
		c.Approve(r, json.RawMessage(decision))
		deadline := time.Now().Add(3 * time.Second)
		var reply codex.Message
		for time.Now().Before(deadline) {
			c.Drain()
			data, _ := os.ReadFile(request)
			json.Unmarshal(data, &reply)
			if string(reply.ID) == string(r.ID) {
				break
			}
			time.Sleep(time.Millisecond)
		}
		var result struct{ Decision json.RawMessage }
		if err := json.Unmarshal(reply.Result, &result); err != nil {
			t.Fatal(err)
		}
		if string(reply.ID) != string(r.ID) || string(result.Decision) != decision {
			t.Fatalf("approval payload changed: %s, %s", reply.ID, reply.Result)
		}
	}
}

func notify(c *Controller, method, params string) {
	c.handle(codex.Message{Method: method, Params: json.RawMessage(params)})
}

func TestStreamingAndManualSettle(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, model.New(), nil)
	c.NewThread("")
	thread := c.State.Thread(c.State.Selected)
	thread.ServerID = "server1"
	thread.Turns = []model.Turn{{ID: "turn1", Prompt: "hello", Status: "inProgress"}}
	c.Running[thread.ID] = true
	c.Settle(thread.ID, true)
	if thread.Settled {
		t.Fatal("settled while running")
	}
	notify(c, "item/started", `{"threadId":"server1","turnId":"turn1","item":{"id":"a","type":"agentMessage","text":""}}`)
	notify(c, "item/agentMessage/delta", `{"threadId":"server1","turnId":"turn1","itemId":"a","delta":"你好"}`)
	notify(c, "item/agentMessage/delta", `{"threadId":"server1","turnId":"other","itemId":"a","delta":"wrong turn"}`)
	notify(c, "item/completed", `{"threadId":"server1","turnId":"turn1","item":{"id":"a","type":"agentMessage","text":"你好世界"}}`)
	notify(c, "item/started", `{"threadId":"server1","turnId":"turn1","item":{"id":"r","type":"reasoning","summary":[],"content":[]}}`)
	notify(c, "item/reasoning/summaryTextDelta", `{"threadId":"server1","turnId":"turn1","itemId":"r","summaryIndex":0,"delta":"检查布局。"}`)
	if thread.Turns[0].Items[1].Text != "检查布局。" {
		t.Fatal("streamed reasoning summary was lost")
	}
	notify(c, "item/completed", `{"threadId":"server1","turnId":"turn1","item":{"id":"r","type":"reasoning","summary":["检查布局。","确认对齐。"],"content":[]}}`)
	notify(c, "turn/completed", `{"threadId":"server1","turn":{"id":"turn1","status":"completed"}}`)
	if c.Running[thread.ID] || thread.Settled || thread.Turns[0].Items[0].Text != "你好世界" {
		t.Fatalf("incorrect reduction: %+v", thread)
	}
	c.Settle(thread.ID, true)
	loaded, err := model.Load(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.Thread(thread.ID).Settled || loaded.Thread(thread.ID).ServerID != "server1" {
		t.Fatal("settle/history not persisted")
	}
	if loaded.Thread(thread.ID).Turns[0].Items[1].Text != "检查布局。\n\n确认对齐。" {
		t.Fatal("completed reasoning summary was not persisted")
	}
	c.Settle(thread.ID, false)
	if thread.Settled {
		t.Fatal("restore failed")
	}
}

func TestProjectsAndInterruptedRecovery(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	path := t.TempDir()
	c.AddProject(path)
	c.AddProject(path)
	if len(c.State.Projects) != 1 {
		t.Fatal("duplicate project")
	}
	thread := c.State.Thread(c.State.Selected)
	canonical, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	if thread.Cwd != canonical {
		t.Fatal("wrong project cwd")
	}
	c.NewThread("")
	thread = c.State.Thread(c.State.Selected)
	if !strings.HasPrefix(thread.Cwd, filepath.Join(c.Dir, "chats")) {
		t.Fatal("unscoped thread not isolated")
	}
	thread.Turns = []model.Turn{{Status: "inProgress"}}
	c.Save()
	s, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Thread(thread.ID).Turns[0].Status != "interrupted" {
		t.Fatal("stuck running after restart")
	}
}

func TestApprovalAndRequestResolution(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	c.handle(codex.Message{ID: json.RawMessage(`"ask"`), Method: "item/tool/requestUserInput", Params: json.RawMessage(`{"threadId":"t1","questions":[{"id":"q1","question":"Choose"}]}`)})
	if len(c.Requests) != 1 || c.Requests[0].Questions[0].ID != "q1" {
		t.Fatal("ask not shown")
	}
	notify(c, "serverRequest/resolved", `{"requestId":"ask"}`)
	if len(c.Requests) != 0 {
		t.Fatal("resolved ask retained")
	}
}

// This test uses the installed CLI only when explicitly requested. Its prompt cannot modify files.
func TestLiveCodex(t *testing.T) {
	if os.Getenv("MUJICAGENT_LIVE_TEST") != "1" {
		t.Skip("live Codex test not requested")
	}
	c := New(t.TempDir(), model.New(), nil)
	defer c.Close()
	c.Connect()
	wait := func(done func() bool) {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for !done() {
			c.Drain()
			if time.Now().After(deadline) {
				t.Fatalf("timeout: %s / %s", c.Connection, c.Error)
			}
			time.Sleep(20 * time.Millisecond)
		}
		c.Drain()
	}
	wait(func() bool { return c.Connection != "Connecting" })
	if c.Connection != "Connected" {
		t.Fatal(c.Error)
	}
	if len(c.Models) == 0 {
		t.Fatal("no models")
	}
	c.NewThread("")
	if !c.Send("Reply with exactly MUJIC_OK. Do not call tools or read or modify any files.", false) {
		t.Fatal(c.Error)
	}
	thread := c.State.Thread(c.State.Selected)
	wait(func() bool { return !c.Running[thread.ID] })
	turn := thread.Turns[len(thread.Turns)-1]
	if turn.Status != "completed" {
		t.Fatalf("turn: %s, %s, %s", turn.Status, turn.Error, c.Error)
	}
	text := ""
	for _, item := range turn.Items {
		if item.Kind == "agentMessage" {
			text += item.Text
		}
	}
	if !strings.Contains(text, "MUJIC_OK") {
		t.Fatalf("unexpected reply: %q", text)
	}
	c.Connect()
	wait(func() bool { return c.Connection != "Connecting" })
	if !c.Send("Reply with exactly RESUMED_OK. Do not call tools or read or modify any files.", false) {
		t.Fatal(c.Error)
	}
	wait(func() bool { return !c.Running[thread.ID] })
	if len(thread.Turns) != 2 || thread.Turns[1].Status != "completed" {
		t.Fatalf("resume failed: %+v", thread.Turns[1])
	}
}

func TestLiveAsk(t *testing.T) {
	if os.Getenv("MUJICAGENT_LIVE_TEST") != "1" {
		t.Skip("live Ask test not requested")
	}
	c := New(t.TempDir(), model.New(), nil)
	defer c.Close()
	c.Connect()
	wait := func(done func() bool) {
		t.Helper()
		deadline := time.Now().Add(90 * time.Second)
		for !done() {
			c.Drain()
			if time.Now().After(deadline) {
				t.Fatalf("timeout: %s / %s", c.Connection, c.Error)
			}
			time.Sleep(20 * time.Millisecond)
		}
		c.Drain()
	}
	wait(func() bool { return c.Connection != "Connecting" })
	if c.Connection != "Connected" {
		t.Fatal(c.Error)
	}
	c.NewThread("")
	if !c.Send("Use request_user_input to ask me exactly one question: Native or Web UI? Offer Native (recommended) and Web. Do not call any other tool. After I answer, reply with ASK_OK and my choice. This is a UI protocol test, so ask the question even though Native is recommended.", true) {
		t.Fatal(c.Error)
	}
	thread := c.State.Thread(c.State.Selected)
	wait(func() bool { return len(c.Requests) > 0 || !c.Running[thread.ID] })
	if len(c.Requests) == 0 {
		t.Fatalf("no Ask event; turn: %+v", thread.Turns[len(thread.Turns)-1])
	}
	r := c.Requests[0]
	answers := map[string]string{}
	for _, q := range r.Questions {
		answers[q.ID] = "Native"
	}
	c.AnswerQuestions(r, answers)
	wait(func() bool { return !c.Running[thread.ID] })
	turn := thread.Turns[len(thread.Turns)-1]
	text := ""
	for _, item := range turn.Items {
		if item.Kind == "agentMessage" {
			text += item.Text
		}
	}
	if turn.Status != "completed" || !strings.Contains(text, "ASK_OK") {
		t.Fatalf("Ask did not resume: %s, %s, %s", turn.Status, text, turn.Error)
	}
}
