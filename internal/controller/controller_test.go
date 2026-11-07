package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type updateTransport func(*http.Request) (*http.Response, error)

func (f updateTransport) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }

func TestDrainYieldsToRendering(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	defer c.Close()
	processed := 0
	var next func()
	next = func() {
		processed++
		if processed < 4096 {
			c.post(next)
		}
	}
	c.post(next)
	c.Drain()
	if processed > 128 || len(c.events) == 0 {
		t.Fatalf("continuous output monopolized a frame: processed=%d pending=%d", processed, len(c.events))
	}
}

func TestAgentStreamBurst(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake process")
	}
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			dir := t.TempDir()
			s := model.New()
			old := s.AddThread("", dir)
			old.Turns = []model.Turn{{Status: "completed", Items: []model.Item{{Kind: "agentMessage", Text: strings.Repeat("history ", 3<<20)}}}}
			thread := s.AddThread("", dir)
			thread.Harness = driver
			binary := filepath.Join(dir, "fake-agent")
			script := "#!/bin/sh\nread request\n"
			delta := `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"x"}}`
			finish := `{"type":"agent_settled"}`
			if driver == "droid" {
				script += `printf '%s\n' '{"id":"request-1","result":{"sessionId":"burst"}}'
read request
printf '%s\n' '{"id":"request-2","result":{}}'
read request
printf '%s\n' '{"id":"request-3","result":{}}'
`
				delta = `{"method":"droid.session_notification","params":{"sessionId":"burst","notification":{"type":"assistant_text_delta","messageId":"m","textDelta":"x"}}}`
				finish = `{"method":"droid.session_notification","params":{"sessionId":"burst","notification":{"type":"agent_turn_completed","reason":"completed"}}}`
			}
			script += "i=0\nwhile [ \"$i\" -lt 2048 ]; do\nprintf '%s\\n' '" + delta + "'\ni=$((i+1))\ndone\nprintf '%s\\n' '" + finish + "'\nsleep 20\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			s.Settings.PiPath, s.Settings.DroidPath = binary, binary
			c := New(dir, s, nil)
			defer c.Close()
			start := time.Now()
			if !c.Send("stream", false) {
				t.Fatal(c.Error)
			}
			sendDuration := time.Since(start)
			start = time.Now()
			var worst time.Duration
			frames := 0
			for c.Running[thread.ID] && time.Since(start) < 10*time.Second {
				frame := time.Now()
				c.Drain()
				worst = max(worst, time.Since(frame))
				frames++
				time.Sleep(time.Millisecond)
			}
			elapsed := time.Since(start)
			if c.Running[thread.ID] || thread.Turns[0].Status != "completed" || len(thread.Turns[0].Items) != 1 || thread.Turns[0].Items[0].Text != strings.Repeat("x", 2048) {
				t.Fatal("stream was lost or never completed", c.Error)
			}
			if worst > 100*time.Millisecond {
				t.Fatalf("UI blocked during streaming: %s", worst)
			}
			deadline := time.Now().Add(3 * time.Second)
			for {
				saved, err := model.Load(filepath.Join(dir, "state.json"))
				if err == nil && len(saved.Thread(thread.ID).Turns[0].Items) == 1 && saved.Thread(thread.ID).Turns[0].Status == "completed" {
					break
				}
				if time.Now().After(deadline) {
					t.Fatal("final background save never completed", err)
				}
				time.Sleep(20 * time.Millisecond)
			}
			// A newer settings save must supersede any queued stream snapshot.
			s.Settings.PiDefaultAccess = "native"
			c.Save()
			c.Close()
			restored, err := model.Load(filepath.Join(dir, "state.json"))
			if err != nil || restored.Settings.PiDefaultAccess != "native" || restored.Thread(thread.ID).Turns[0].Items[0].Text != strings.Repeat("x", 2048) {
				t.Fatal("stale stream snapshot overwrote final state", err)
			}
			t.Logf("24 MiB history, 2048 deltas: send=%s stream=%s frames=%d worst Drain=%s", sendDuration, elapsed, frames, worst)
		})
	}
}

func TestAgentDefaultPermissions(t *testing.T) {
	s := model.New()
	s.Settings.PiDefaultAccess, s.Settings.DroidDefaultAccess = "native", "medium"
	c := New(t.TempDir(), s, nil)
	defer c.Close()
	s.AddThread("", t.TempDir())
	for _, driver := range []string{"pi", "droid", "pi"} {
		if !c.Handoff(driver, "model") {
			t.Fatal("handoff failed")
		}
		thread := s.Thread(s.Selected)
		want := s.Settings.AgentDefaultAccess(driver)
		if thread.AgentAccess != want {
			t.Fatal("default permissions did not apply", driver, thread.AgentAccess)
		}
		thread.Turns = []model.Turn{{Status: "completed"}}
	}
	existing := s.Thread(s.Selected)
	s.Settings.PiDefaultAccess = ""
	if existing.AgentAccess != "native" {
		t.Fatal("settings rewrote an existing conversation")
	}
	c.Save()
	loaded, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil || loaded.Settings.AgentDefaultAccess("droid") != "medium" || loaded.Thread(existing.ID).AgentAccess != "native" {
		t.Fatal("permissions did not persist", err)
	}
	s.Settings.PiDefaultAccess, s.Settings.DroidDefaultAccess = "unknown", "native"
	if s.Settings.AgentDefaultAccess("pi") != "" || s.Settings.AgentDefaultAccess("droid") != "" {
		t.Fatal("unknown default increased access")
	}
}

func TestStreamSnapshotIsolation(t *testing.T) {
	s := model.New()
	s.Projects = []model.Project{{Name: "before"}}
	s.Settings.ModelFavorites = []model.ModelFavorite{{Harness: "pi", ModelID: "before"}}
	thread := s.AddThread("", "")
	thread.Queue = []model.QueuedMessage{{Text: "before"}}
	thread.Turns = []model.Turn{{Items: []model.Item{{Text: "before", MCP: &model.MCPCall{Result: "before"}}}}}
	copy := cloneState(s)
	s.Projects[0].Name = "after"
	s.Settings.ModelFavorites[0].ModelID = "after"
	thread.Queue[0].Text = "after"
	thread.Turns[0].Items[0].Text = "after"
	thread.Turns[0].Items[0].MCP.Result = "after"
	got := copy.Thread(thread.ID)
	if copy.Projects[0].Name != "before" || copy.Settings.ModelFavorites[0].ModelID != "before" || got.Queue[0].Text != "before" || got.Turns[0].Items[0].Text != "before" || got.Turns[0].Items[0].MCP.Result != "before" {
		t.Fatal("background snapshot shares mutable UI state")
	}
}

func TestNativeToolMergeDetails(t *testing.T) {
	turn := model.Turn{}
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "script", Tool: &harness.ToolEvent{Name: "Script", Kind: "execute", Arguments: json.RawMessage(`{"script":"text('ok')"}`)}})
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "child", Tool: &harness.ToolEvent{Name: "Read", Kind: "read", ParentID: "script", Arguments: json.RawMessage(`{"file_path":"a.go"}`)}})
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "child", Status: "completed", Tool: &harness.ToolEvent{Result: json.RawMessage(`"package main"`)}})
	// Message snapshots can follow results, and later heartbeats omit input.
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "child", Status: "inProgress", Tool: &harness.ToolEvent{Name: "Read", Kind: "read", ParentID: "script", Arguments: json.RawMessage(`{"file_path":"a.go"}`)}})
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "child", Status: "inProgress", Tool: &harness.ToolEvent{Name: "Read", Kind: "read"}})
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "todo", Tool: &harness.ToolEvent{Name: "TodoWrite", Kind: "plan", Arguments: json.RawMessage(`{"todos":"1. [in_progress] Verify"}`)}})
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "todo", Tool: &harness.ToolEvent{Arguments: json.RawMessage(`{"todos":"1. [completed] Verify"}`)}})
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "compact-1", Kind: "contextCompaction", Status: "completed"})
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "compact-2", Kind: "contextCompaction", Status: "inProgress"})
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "compact-2", Kind: "contextCompaction", Status: "interrupted"})
	if len(turn.Items) != 5 || turn.Items[1].ParentID != "script" || turn.Items[1].Kind != "toolCall" || turn.Items[2].Text != "1. [completed] Verify" || turn.Items[4].Status != "interrupted" {
		t.Fatal("native details lost", turn.Items)
	}
	if child := turn.Items[1]; child.Status != "completed" || !strings.Contains(child.MCP.Arguments, `"file_path": "a.go"`) || child.MCP.Result != `"package main"` {
		t.Fatal("late Script snapshot or heartbeat discarded completed details", child)
	}
	s := model.New()
	thread := s.AddThread("", "")
	thread.Turns = []model.Turn{turn}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := model.Save(path, s); err != nil {
		t.Fatal(err)
	}
	restored, err := model.Load(path)
	if err != nil || restored.Thread(thread.ID).Turns[0].Items[1].ParentID != "script" || len(restored.Thread(thread.ID).Turns[0].Items) != 5 {
		t.Fatal("restart lost Script children or repeated compactions", err)
	}
	if got := agentPlanText(json.RawMessage(`{"plan":[{"step":"Verify","status":"completed"}]}`)); got != "1. [completed] Verify" {
		t.Fatal("structured plan lost", got)
	}
}

func TestReleaseUpdateManifest(t *testing.T) {
	for _, tc := range []struct {
		name      string
		body      string
		status    int
		wantError bool
	}{
		{"matching release", `{"version":"0.1.5","signature":"signed","url":"https://example.com/update.tar.gz"}`, 200, false},
		{"old manifest", `{"version":"0.1.4"}`, 200, true},
		{"missing version", `{}`, 200, true},
		{"malformed manifest", `{`, 200, true},
		{"missing asset", `Not found`, 404, true},
		{"oversized manifest", strings.Repeat(" ", 1<<20+1), 200, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &http.Client{Transport: updateTransport(func(r *http.Request) (*http.Response, error) {
				want := "https://github.com/ji233-Sun/mujicagent/releases/download/v0.1.5/update-" + runtime.GOOS + "-" + runtime.GOARCH + ".json"
				if r.URL.String() != want {
					t.Fatalf("manifest URL = %s, want %s", r.URL, want)
				}
				return &http.Response{StatusCode: tc.status, Body: io.NopCloser(strings.NewReader(tc.body)), Header: make(http.Header)}, nil
			})}
			source := releaseUpdateSource{tag: "v0.1.5", client: client}
			body, err := source.Manifest(context.Background(), false)
			if (err != nil) != tc.wantError {
				t.Fatalf("Manifest error = %v", err)
			}
			if err == nil {
				defer body.Close()
				data, err := io.ReadAll(body)
				if err != nil || string(data) != tc.body {
					t.Fatalf("manifest changed before MyGo validation: %q, %v", data, err)
				}
			}
		})
	}
}

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

func TestFileAttachmentInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "未分类 file.xyz")
	if err := os.WriteFile(path, []byte("file contents must not be inlined"), 0600); err != nil {
		t.Fatal(err)
	}
	a := model.Attachment{Kind: "file", Path: path}
	input, err := messageInput("before "+a.Link()+" after", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 3 {
		t.Fatalf("input: %+v", input)
	}
	item := input[1].(map[string]any)
	if item["type"] != "text" || !strings.Contains(item["text"].(string), fmt.Sprintf("%q", path)) || strings.Contains(item["text"].(string), "file contents") {
		t.Fatalf("file input: %+v", item)
	}
	for _, path := range []string{path + "missing", filepath.Dir(path)} {
		if _, err := messageInput((model.Attachment{Kind: "file", Path: path}).Link(), t.TempDir()); err == nil {
			t.Fatalf("invalid attachment accepted: %s", path)
		}
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
			case "turn/steer":
				path := os.Getenv("MUJICAGENT_SPEED_REQUEST")
				os.WriteFile(path+"-steer", m.Params, 0600)
				if reject, _ := os.ReadFile(path + "-reject"); string(reject) == "yes" {
					enc.Encode(map[string]any{"id": m.ID, "error": map[string]any{"code": -32602, "message": "turn mismatch"}})
					continue
				}
			case "thread/settings/update":
				path := os.Getenv("MUJICAGENT_SPEED_REQUEST")
				if os.WriteFile(path+"-update", m.Params, 0600) != nil {
					os.Exit(2)
				}
				if reject, _ := os.ReadFile(path + "-reject"); string(reject) == "yes" {
					enc.Encode(map[string]any{"id": m.ID, "error": map[string]any{"code": -32601, "message": "unsupported settings update"}})
					continue
				}
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
	c.AddProject(t.TempDir())
	thread := c.State.Thread(c.State.Selected)
	imagePath := filepath.Join(dir, "图 片.png")
	if err := os.WriteFile(imagePath, []byte("test image"), 0600); err != nil {
		t.Fatal(err)
	}
	attachment := model.Attachment{Kind: "image", Path: imagePath, X: 25, Y: 75, Tip: "change this"}
	for caseIndex, tc := range []struct{ model, selected, sent string }{
		{"speed-model", "priority", "priority"},
		{"speed-model", "ultrafast", "ultrafast"},
		{"speed-model", "", "default"},
		{"speed-model", "invented", "default"},
		{"plain-model", "priority", "default"},
		{"unknown-model", "ultrafast", "default"},
	} {
		if caseIndex == 5 {
			t.Setenv("MUJICAGENT_DATA_DIR", dir)
			c.NewThread("")
			thread = c.State.Thread(c.State.Selected)
		}
		// The conversation owns its model independently of the default for new chats.
		thread.AgentModel = tc.model
		c.State.Settings.Model, c.State.Settings.ServiceTier = "plain-model", tc.selected
		if !c.Send("speed test "+attachment.Link()+" end", false) {
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
		var wire struct {
			Input []struct{ Type, Text, Path string }
		}
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		if len(wire.Input) != 4 || wire.Input[0].Text != "speed test " || wire.Input[2].Type != "localImage" || wire.Input[2].Path != imagePath || wire.Input[3].Text != " end" || !strings.Contains(wire.Input[1].Text, "25.00%, 75.00%") {
			t.Fatalf("incorrect attachment input: %s", data)
		}
		if caseIndex == 5 {
			data, err := os.ReadFile(request + "-thread")
			var start struct{ Cwd string }
			if err != nil || json.Unmarshal(data, &start) != nil || start.Cwd != thread.Cwd || !strings.HasPrefix(start.Cwd, filepath.Join(dir, "workspace")) {
				t.Fatalf("incorrect projectless cwd: %s, %v", data, err)
			}
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
			c.AddProject(t.TempDir())
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
	// Live selections are future-turn settings, never grants for pending approvals.
	c.Permissions = []codex.PermissionProfile{{ID: ":read-only", Allowed: true}, {ID: ":workspace", Allowed: true}, {ID: ":danger-full-access", Allowed: true}, {ID: ":approve-for-me", Allowed: true}, {ID: "restricted"}}
	c.permissionsTarget = thread.ID
	pending := &codex.Request{ID: json.RawMessage(`"pending"`), ThreadID: thread.ServerID, Method: "item/commandExecution/requestApproval"}
	c.Requests = []*codex.Request{pending}
	if !c.Send("keep this context", false) {
		t.Fatal(c.Error)
	}
	wait := func(done func() bool) {
		t.Helper()
		deadline := time.Now().Add(3 * time.Second)
		for !done() && time.Now().Before(deadline) {
			c.Drain()
			time.Sleep(time.Millisecond)
		}
		if !done() {
			t.Fatalf("timed out: %s", c.Error)
		}
	}
	wait(func() bool { return thread.Turns[len(thread.Turns)-1].ID != "" })
	current, defaults, turns := c.PermissionID(), c.State.Settings.DefaultPermissionProfile, len(thread.Turns)
	for _, id := range []string{":danger-full-access", ":read-only", ":approve-for-me"} {
		c.SetPermission(id)
		if !c.PermissionUpdating() || c.NextPermissionID() == id {
			t.Fatal("selection changed before server acknowledgement")
		}
		wait(func() bool { return !c.PermissionUpdating() })
		if c.NextPermissionID() != id || c.PermissionID() != current || len(thread.Turns) != turns || !c.Running[thread.ID] {
			t.Fatal("live change altered the active turn or lost future selection")
		}
		data, err := os.ReadFile(request + "-update")
		if err != nil {
			t.Fatal(err)
		}
		var wire struct{ ThreadID, Permissions, ApprovalPolicy, ApprovalsReviewer string }
		if err := json.Unmarshal(data, &wire); err != nil {
			t.Fatal(err)
		}
		profile := codex.PermissionProfile{ID: id}
		if wire.ThreadID != thread.ServerID || wire.Permissions != profile.WireID() || wire.ApprovalPolicy != profile.ApprovalPolicy() || wire.ApprovalsReviewer != profile.Reviewer() {
			t.Fatalf("wrong settings payload: %s", data)
		}
		if len(c.Requests) != 1 || c.Requests[0] != pending || pending.Responding || c.State.Settings.DefaultPermissionProfile != defaults {
			t.Fatal("selection affected pending approval or global defaults")
		}
	}
	c.SetPermission("restricted")
	if c.PermissionUpdating() || c.NextPermissionID() != ":approve-for-me" {
		t.Fatal("managed restriction bypassed")
	}
	if err := os.WriteFile(request+"-reject", []byte("yes"), 0600); err != nil {
		t.Fatal(err)
	}
	c.SetPermission(":read-only")
	wait(func() bool { return !c.PermissionUpdating() })
	if c.NextPermissionID() != ":approve-for-me" || c.PermissionID() != current || !strings.Contains(c.Error, "not confirmed") {
		t.Fatal("failed update changed selection or lacked visible error")
	}
	if err := os.WriteFile(request+"-reject", nil, 0600); err != nil {
		t.Fatal(err)
	}
	notify(c, "turn/completed", `{"threadId":"speed-thread","turn":{"id":"speed-turn","status":"completed"}}`)
	// The next turn explicitly carries the queued selection on the same thread.
	if !c.Send("continue context", false) {
		t.Fatal(c.Error)
	}
	wait(func() bool { return thread.Turns[len(thread.Turns)-1].ID != "" })
	data, err := os.ReadFile(request)
	if err != nil {
		t.Fatal(err)
	}
	var next struct{ ThreadID, Permissions, ApprovalsReviewer string }
	if err := json.Unmarshal(data, &next); err != nil || next.ThreadID != thread.ServerID || next.Permissions != ":workspace" || next.ApprovalsReviewer != "auto_review" {
		t.Fatalf("next turn did not carry selected permissions: %s (%v)", data, err)
	}
	notify(c, "turn/completed", `{"threadId":"speed-thread","turn":{"id":"speed-turn","status":"completed"}}`)
	c.SetPermission(":read-only")
	c.AddProject(t.TempDir())
	other := c.State.Thread(c.State.Selected)
	otherPermission := other.PermissionProfile
	wait(func() bool { return !c.permissionUpdates[thread.ID] })
	if thread.PermissionProfile != ":read-only" || other.PermissionProfile != otherPermission {
		t.Fatal("background update targeted the newly selected conversation")
	}
	c.State.Selected = thread.ID
	t.Run("queue and steer", func(t *testing.T) {
		if !c.Send("A", false) {
			t.Fatal(c.Error)
		}
		wait(func() bool { return thread.Turns[len(thread.Turns)-1].ID != "" })
		initial := len(thread.Turns)
		thread.Draft = "B"
		if !c.Send(thread.Draft, false) || !c.Send("C", false) || thread.Draft != "" {
			t.Fatal("enqueue failed")
		}
		b, cc := thread.Queue[0].ID, thread.Queue[1].ID
		c.MoveQueued(thread.ID, cc, b)
		if thread.Queue[0].Text != "C" {
			t.Fatal("reorder failed")
		}
		notify(c, "item/completed", `{"threadId":"speed-thread","turnId":"speed-turn","item":{"id":"tool","type":"commandExecution","status":"completed"}}`)
		if len(thread.Turns) != initial || len(thread.Queue) != 2 {
			t.Fatal("tool completion advanced queue")
		}
		os.WriteFile(request+"-reject", []byte("yes"), 0600)
		c.EditQueued(thread.ID, b)
		c.Steer(thread.ID, b)
		if thread.Queue[1].Sending {
			t.Fatal("steer sent an editing message")
		}
		c.CancelQueueEdit(thread.ID)
		c.Steer(thread.ID, b)
		wait(func() bool { return !thread.Queue[1].Sending })
		if len(thread.Queue) != 2 || !strings.Contains(c.Error, "turn mismatch") {
			t.Fatal("failed steer lost message")
		}
		os.WriteFile(request+"-reject", nil, 0600)
		c.Steer(thread.ID, b)
		wait(func() bool { return len(thread.Queue) == 1 })
		data, _ := os.ReadFile(request + "-steer")
		var wire struct {
			ThreadID, ExpectedTurnID string
			Input                    []struct{ Text string }
		}
		if json.Unmarshal(data, &wire) != nil || wire.ExpectedTurnID != "speed-turn" || wire.ThreadID != "speed-thread" || len(wire.Input) != 1 || wire.Input[0].Text != "B" {
			t.Fatalf("invalid steer: %s", data)
		}
		if len(thread.Turns) != initial || thread.Queue[0].Text != "C" {
			t.Fatal("steer started a new turn")
		}
		thread.Draft = "unsent draft"
		c.State.Selected = other.ID
		c.EditQueued(thread.ID, cc)
		notify(c, "turn/completed", `{"threadId":"speed-thread","turn":{"id":"speed-turn","status":"completed"}}`)
		if len(thread.Queue) != 1 || c.Running[thread.ID] {
			t.Fatal("completion sent an editing message")
		}
		c.CancelQueueEdit(thread.ID)
		c.ResumeQueue(thread.ID)
		wait(func() bool { return len(thread.Queue) == 0 })
		if len(thread.Turns) != initial+1 || thread.Turns[initial].Prompt != "C" || thread.Draft != "unsent draft" || len(other.Turns) != 0 {
			t.Fatal("queue targeted wrong thread or draft")
		}
		c.State.Selected = thread.ID
		c.Send("D", false)
		c.Send("E", false)
		notify(c, "turn/completed", `{"threadId":"speed-thread","turn":{"id":"speed-turn","status":"completed"}}`)
		wait(func() bool { return len(thread.Queue) == 1 })
		if thread.Turns[len(thread.Turns)-1].Prompt != "D" || thread.Queue[0].Text != "E" {
			t.Fatal("queue drained more than one message")
		}
		notify(c, "turn/completed", `{"threadId":"speed-thread","turn":{"id":"speed-turn","status":"interrupted"}}`)
		if len(thread.Queue) != 1 || c.Running[thread.ID] {
			t.Fatal("interrupted turn must pause queue")
		}
		c.ResumeQueue(thread.ID)
		wait(func() bool { return len(thread.Queue) == 0 })
		notify(c, "turn/completed", `{"threadId":"speed-thread","turn":{"id":"speed-turn","status":"completed"}}`)
	})

	t.Run("completion while steer is pending", func(t *testing.T) {
		c.Send("A2", false)
		wait(func() bool { return thread.Turns[len(thread.Turns)-1].ID != "" })
		c.Send("B2", false)
		c.Send("C2", false)
		initial := len(thread.Turns)
		c.Steer(thread.ID, thread.Queue[0].ID)
		notify(c, "turn/completed", `{"threadId":"speed-thread","turn":{"id":"speed-turn","status":"completed"}}`)
		if len(thread.Turns) != initial {
			t.Fatal("advanced before steer resolved")
		}
		wait(func() bool { return len(thread.Queue) == 0 })
		if len(thread.Turns) != initial+1 || thread.Turns[initial].Prompt != "C2" {
			t.Fatal("steered message sent twice")
		}
		notify(c, "turn/completed", `{"threadId":"speed-thread","turn":{"id":"speed-turn","status":"completed"}}`)
	})

	c.Requests = nil
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

	for _, id := range []string{`"mcp-1"`, `42`} {
		for _, action := range []string{"accept", "decline", "cancel"} {
			c.handle(codex.Message{ID: json.RawMessage(id), Method: "mcpServer/elicitation/request", Params: json.RawMessage(`{"threadId":"speed-thread","serverName":"github","mode":"form","message":"Create PR?","_meta":{"source":"server"},"requestedSchema":{"type":"object","properties":{"decision":{"type":"string","enum":["once","session"],"enumNames":["Allow once","This session"]}},"required":["decision"]}}`)})
			r := c.Requests[len(c.Requests)-1]
			c.RespondElicitation(r, "accept", map[string]string{"decision": "invented"})
			if r.Responding {
				t.Fatal("invalid selection accepted")
			}
			c.RespondElicitation(r, action, map[string]string{"decision": "once"})
			deadline := time.Now().Add(3 * time.Second)
			var reply codex.Message
			var result map[string]any
			var data []byte
			for time.Now().Before(deadline) {
				c.Drain()
				data, _ = os.ReadFile(request)
				reply = codex.Message{}
				result = nil
				if json.Unmarshal(data, &reply) == nil && json.Unmarshal(reply.Result, &result) == nil &&
					string(reply.ID) == id && result["action"] == action && len(c.Requests) == 0 {
					break
				}
				time.Sleep(time.Millisecond)
			}
			if string(reply.ID) != id || result["action"] != action || reply.Error != nil || len(c.Requests) != 0 {
				t.Fatalf("elicitation response: %s; pending=%d", data, len(c.Requests))
			}
			if _, exists := result["decision"]; exists {
				t.Fatal("command approval response reused")
			}
			if _, exists := result["_meta"]; exists {
				t.Fatal("server metadata echoed as client metadata")
			}
			if action == "accept" {
				if result["content"].(map[string]any)["decision"] != "once" {
					t.Fatal(result)
				}
			} else if result["content"] != nil {
				t.Fatal("decline/cancel must not include input")
			}
			// A resolved/stale card cannot send a second answer.
			c.RespondElicitation(r, "accept", map[string]string{"decision": "once"})
			if len(c.Requests) != 0 {
				t.Fatal("stale request requeued")
			}
		}
	}
}

func notify(c *Controller, method, params string) {
	c.handle(codex.Message{Method: method, Params: json.RawMessage(params)})
}

func TestMCPCallDetailsEvents(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, model.New(), nil)
	thread := c.State.AddThread("project", dir)
	thread.ServerID = "server"
	thread.Turns = []model.Turn{{ID: "turn", Status: "inProgress"}}
	c.Running[thread.ID] = true
	notify(c, "item/started", `{"threadId":"server","turnId":"turn","item":{"id":"search","type":"mcpToolCall","server":"github","tool":"search_issues","status":"inProgress","arguments":{"query":"layout bug","headers":{"Authorization":"Bearer fake-start-secret"},"limit":10}}}`)
	started := thread.Turns[0].Items[0]
	if started.Status != "inProgress" || started.MCP == nil || started.MCP.Tool != "search_issues" || !strings.Contains(started.MCP.Arguments, "layout bug") || strings.Contains(started.MCP.Arguments, "fake-start-secret") {
		t.Fatal("MCP start metadata or redaction is incorrect")
	}
	// Completion can omit the metadata already supplied when the call started.
	notify(c, "item/completed", `{"threadId":"server","turnId":"turn","item":{"id":"search","type":"mcpToolCall","status":"completed","result":{"content":[{"type":"text","text":"Found issue 16"}],"structuredContent":{"id":9007199254740993,"credentials":{"token":"fake-result-secret"}}}}}`)
	notify(c, "item/completed", `{"threadId":"server","turnId":"turn","item":{"id":"fetch","type":"mcpToolCall","server":"github","tool":"get_issue","status":"failed","arguments":{"number":16,"api_key":"fake-api-secret"},"error":{"message":"Access denied: token=fake-error-secret"}}}`)
	notify(c, "item/completed", `{"threadId":"server","turnId":"turn","item":{"id":"missing","type":"mcpToolCall"}}`)
	notify(c, "turn/completed", `{"threadId":"server","turn":{"id":"turn","status":"completed"}}`)
	loaded, err := model.Load(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	items := loaded.Thread(thread.ID).Turns[0].Items
	if len(items) != 3 || items[0].MCP == nil || items[1].MCP == nil || items[2].MCP == nil {
		t.Fatal("MCP records were not preserved across save/load")
	}
	first, failed := items[0], items[1]
	if first.Status != "completed" || first.MCP.Tool != "search_issues" || first.MCP.Server != "github" || first.MCP.Arguments != started.MCP.Arguments || !strings.Contains(first.MCP.Result, "Found issue 16") || !strings.Contains(first.MCP.Result, "9007199254740993") {
		t.Fatal("completion lost call metadata, result, or JSON number precision")
	}
	if failed.Status != "failed" || failed.MCP.Tool != "get_issue" || !strings.Contains(failed.MCP.Error, "Access denied") || !strings.Contains(failed.MCP.Error, mcpRedacted) {
		t.Fatal("failed call lost its readable error")
	}
	if items[2].Status != "completed" || *items[2].MCP != (model.MCPCall{}) {
		t.Fatal("missing metadata must not be inferred from another call")
	}
	data, err := os.ReadFile(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"fake-start-secret", "fake-result-secret", "fake-api-secret", "fake-error-secret"} {
		if strings.Contains(string(data), secret) {
			t.Fatal("credential was persisted in the conversation snapshot")
		}
	}
}

func TestMCPCallRedaction(t *testing.T) {
	for _, tc := range []struct {
		name, raw, secret string
	}{
		{"nested arguments", `{"arguments":{"rows":[{"client_secret":"nested-secret"}],"max_tokens":42}}`, "nested-secret"},
		{"JSON text result", `{"result":{"content":[{"type":"text","text":"{\"password\":\"embedded-secret\",\"count\":42}"}]}}`, "embedded-secret"},
		{"authorization error", `{"error":{"message":"Denied: Authorization: Bearer auth-secret"}}`, "auth-secret"},
		{"basic authorization", `{"result":{"content":[{"type":"text","text":"Basic dGVzdDpzZWNyZXQ="}]}}`, "dGVzdDpzZWNyZXQ="},
		{"URL query", `{"arguments":{"url":"https://example.com/?access_token=query-secret&limit=42"}}`, "query-secret"},
		{"URL password", `{"arguments":{"url":"https://user:url-secret@example.com/"}}`, "url-secret"},
		{"cookie header", `{"error":{"message":"Cookie: session=cookie-secret; other=second-secret"}}`, "cookie-secret"},
		{"private key", `{"result":{"content":[{"type":"text","text":"-----BEGIN PRIVATE KEY-----\nprivate-secret\n-----END PRIVATE KEY-----"}]}}`, "private-secret"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			call := decodeMCPCall(json.RawMessage(tc.raw))
			if call == nil {
				t.Fatal("valid MCP payload failed to decode")
			}
			data, err := json.Marshal(call)
			if err != nil || strings.Contains(string(data), tc.secret) || !strings.Contains(string(data), mcpRedacted) {
				t.Fatal("MCP credential redaction failed")
			}
			if strings.Contains(tc.raw, "42") && !strings.Contains(string(data), "42") {
				t.Fatal("redaction removed unrelated call data")
			}
		})
	}
}

func TestStreamingAndManualSettle(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, model.New(), nil)
	c.AddProject(t.TempDir())
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
	thread.Turns = []model.Turn{{Status: "inProgress"}}
	c.Save()
	s, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Thread(thread.ID).Turns[0].Status != "interrupted" {
		t.Fatal("stuck running after restart")
	}
	if s.Selected != thread.ID || !s.Projects[0].Expanded {
		t.Fatal("last conversation and its project were not restored")
	}
}

func TestSettleStartsNewConversationInCurrentProject(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	c.AddProject(t.TempDir())
	current := c.State.Thread(c.State.Selected)
	current.Draft = "old draft"
	current.Turns = []model.Turn{{Prompt: "old prompt", Status: "completed"}}
	c.AddProject(t.TempDir())
	other := c.State.Thread(c.State.Selected)
	c.Select(current.ID)
	c.Settle(current.ID, true)
	next := c.State.Thread(c.State.Selected)
	if next == nil || next.ID == current.ID || next.ID == other.ID || next.ProjectID != current.ProjectID || next.Cwd != current.Cwd {
		t.Fatalf("wrong new conversation: %+v", next)
	}
	if !current.Settled || next.Settled || next.Draft != "" || len(next.Turns) != 0 || next.ServerID != "" || len(next.Queue) != 0 {
		t.Fatal("settle did not create a blank conversation")
	}
	count := len(c.State.Threads)
	c.Settle(other.ID, true)
	c.Settle(current.ID, true)
	c.Settle(current.ID, false)
	if c.State.Selected != next.ID || len(c.State.Threads) != count {
		t.Fatal("settling a background conversation or restoring one changed the current conversation")
	}
	s, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if s.Selected != next.ID || !s.Projects[0].Expanded || s.Projects[1].Expanded || len(s.Threads) != count {
		t.Fatal("restart did not restore the current project and conversation")
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
	c.handle(codex.Message{ID: json.RawMessage(`"unknown"`), Method: "unknown/request", Params: json.RawMessage(`{"threadId":"t1"}`)})
	if len(c.Logs) != 1 || c.Logs[0].Level != "debug" || !strings.Contains(c.Logs[0].Message, "unknown/request") || c.Error != "" {
		t.Fatal("unsupported requests must be diagnostic logs, not error alerts")
	}
}

func TestLogRetentionAndErrors(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	for i := 0; i < maxLogEntries+1; i++ {
		c.LogNotice(fmt.Sprint(i))
	}
	if len(c.Logs) != maxLogEntries || c.Logs[0].Message != "1" || c.Logs[len(c.Logs)-1].Message != fmt.Sprint(maxLogEntries) {
		t.Fatal("logs must retain only the most recent entries")
	}
	c.ReportError("Codex missing")
	last := c.Logs[len(c.Logs)-1]
	if c.Error != last.Message || last.Level != "error" || last.Time.IsZero() {
		t.Fatal("important errors must be both logged and surfaced without developer mode")
	}
}

func TestMissingCodexIsAlwaysReported(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	defer c.Close()
	c.State.Settings.CodexPath = filepath.Join(t.TempDir(), "missing-codex")
	c.Connect()
	deadline := time.Now().Add(8 * time.Second)
	for c.Connection == "Connecting" && time.Now().Before(deadline) {
		c.Drain()
		time.Sleep(time.Millisecond)
	}
	if c.Connection != "Disconnected" || c.Error == "" || len(c.Logs) == 0 || c.Logs[len(c.Logs)-1].Level != "error" {
		t.Fatalf("missing Codex must show an error with developer mode off: %s / %s", c.Connection, c.Error)
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
	c.AddProject(t.TempDir())
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
	c.AddProject(t.TempDir())
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

func TestInvalidAndLegacySessions(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	for _, id := range []string{"missing"} {
		c.NewThread(id)
		if len(c.State.Threads) != 0 || c.State.Selected != "" {
			t.Fatal("invalid project created a conversation")
		}
	}
	c.Permissions = []codex.PermissionProfile{{ID: ":read-only", Allowed: true}}
	c.SetPermission(":read-only")
	if len(c.State.Threads) != 0 {
		t.Fatal("changing permissions created a projectless conversation")
	}
	// A connected client must not bypass the project requirement.
	c.client, c.Connection = &codex.Client{}, "Connected"
	if c.Send("hello", false) || len(c.State.Threads) != 0 {
		t.Fatal("sending without a selection created a conversation")
	}
	legacy := c.State.AddThread("", filepath.Join(c.Dir, "chats", "legacy"))
	legacy.Draft = "preserve draft"
	if c.Send(legacy.Draft, false) || len(legacy.Turns) != 0 || legacy.Draft != "preserve draft" || c.Running[legacy.ID] {
		t.Fatal("projectless history must remain unchanged and cannot start a turn")
	}
	if _, err := os.Stat(legacy.Cwd); !os.IsNotExist(err) {
		t.Fatal("projectless sending created a working directory")
	}
}

func TestMcpElicitationQueueAndCleanup(t *testing.T) {
	for _, id := range []string{`"mcp-approval-1"`, `42`} {
		for _, mode := range []string{"form", "url", "openai/userVerification"} {
			c := New(t.TempDir(), model.New(), nil)
			params := fmt.Sprintf(`{"threadId":"t1","serverName":"github","mode":%q,"message":"Create PR?","requestedSchema":{"type":"object","properties":{}}}`, mode)
			c.handle(codex.Message{ID: json.RawMessage(id), Method: "mcpServer/elicitation/request", Params: json.RawMessage(params)})
			if len(c.Requests) != 1 || string(c.Requests[0].ID) != id || len(c.Logs) != 0 {
				t.Fatalf("request not visible: %+v / %+v", c.Requests, c.Logs)
			}
			notify(c, "serverRequest/resolved", fmt.Sprintf(`{"requestId":%s}`, id))
			if len(c.Requests) != 0 {
				t.Fatal("resolved request retained")
			}
			c.handle(codex.Message{ID: json.RawMessage(id), Method: "mcpServer/elicitation/request", Params: json.RawMessage(params)})
			notify(c, "transport/closed", `{}`)
			if len(c.Requests) != 0 {
				t.Fatal("disconnected request retained")
			}
		}
	}
}

func TestProjectlessWorkspace(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUJICAGENT_DATA_DIR", dir)
	c := New(dir, model.New(), nil)
	c.NewThread("")
	thread := c.State.Thread(c.State.Selected)
	if thread == nil || thread.Settled || thread.Cwd != "" || !c.State.NoProjectExpanded {
		t.Fatal("invalid blank conversation")
	}
	if _, err := os.Stat(filepath.Join(dir, "workspace")); !os.IsNotExist(err) {
		t.Fatal("draft allocated a directory")
	}
	thread.Created = time.Date(2025, 12, 31, 23, 59, 0, 0, time.FixedZone("local", -8*3600))
	thread.Draft = "Organize 下载 / FILES"
	if err := c.prepareWorkspace(thread, "Organize 下载 / FILES"); err != nil {
		t.Fatal(err)
	}
	path := thread.Cwd
	if !strings.Contains(filepath.ToSlash(path), "/workspace/2025/12/organize-files-") || len(filepath.Base(path)) != len("organize-files-")+8 {
		t.Fatal(path)
	}
	if err := os.WriteFile(filepath.Join(path, "keep"), []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	c.NewThread("")
	second := c.State.Thread(c.State.Selected)
	second.Created = thread.Created
	if err := c.prepareWorkspace(second, "Organize 下载 / FILES"); err != nil {
		t.Fatal(err)
	}
	if second.Cwd == path {
		t.Fatal("shared directory")
	}
	if _, err := os.Stat(filepath.Join(second.Cwd, "keep")); !os.IsNotExist(err) {
		t.Fatal("shared files")
	}
	state, err := model.Load(filepath.Join(dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	c = New(dir, state, nil)
	saved := state.Thread(thread.ID)
	saved.Title = "Changed title"
	saved.Updated = time.Now()
	if err := c.prepareWorkspace(saved, "different"); err != nil || saved.Cwd != path {
		t.Fatalf("directory changed: %v", err)
	}
	if data, err := os.ReadFile(filepath.Join(path, "keep")); err != nil || string(data) != "keep" {
		t.Fatal("lost file")
	}
	c.Settle(saved.ID, true)
	c.Settle(saved.ID, false)
	if saved.Settled || saved.ProjectID != "" {
		t.Fatal("restore changed ownership")
	}
}

func TestWorkspaceSlugAndCollision(t *testing.T) {
	for input, want := range map[string]string{
		"HELLO World": "hello-world", "中文": "chat", "12345": "chat", "": "chat",
		"Hello中文World": "hello-world", " / Hello---there / ": "hello-there",
		"abcdefghijklmnopqrstuvw-XYZ": "abcdefghijklmnopqrstuvw",
		"123456789012345678901234abc": "chat",
	} {
		if got := workspaceSlug(input); got != want {
			t.Fatalf("%q: %q != %q", input, got, want)
		}
	}
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "chat-11111111"), 0700); err != nil {
		t.Fatal(err)
	}
	calls := 0
	path, err := createWorkspace(dir, "chat", func() string {
		calls++
		if calls == 1 {
			return "11111111"
		}
		return "22222222"
	})
	if err != nil || calls != 2 || filepath.Base(path) != "chat-22222222" {
		t.Fatalf("%s %v", path, err)
	}
}

func TestWorkspaceFailuresAndAttachmentNames(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("MUJICAGENT_DATA_DIR", dir)
	c := New(dir, model.New(), nil)
	c.NewThread("")
	thread := c.State.Thread(c.State.Selected)
	thread.Draft = (model.Attachment{Kind: "file", Path: filepath.Join(dir, "secret-filename.txt")}).Link()
	if err := c.prepareWorkspace(thread, thread.Draft); err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(filepath.Base(thread.Cwd), "chat-") {
		t.Fatal(thread.Cwd)
	}
	c.NewThread("")
	named := c.State.Thread(c.State.Selected)
	named.Title = "Custom TITLE"
	named.Draft = "ignored text"
	if err := c.prepareWorkspace(named, "ignored text"); err != nil || !strings.HasPrefix(filepath.Base(named.Cwd), "custom-title-") {
		t.Fatalf("%s %v", named.Cwd, err)
	}
	// A non-directory parent makes saving fail; no turn may start.
	blocker := filepath.Join(dir, "blocked")
	if err := os.WriteFile(blocker, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	c.Dir = blocker
	c.client, c.Connection = &codex.Client{}, "Connected"
	if c.Send("hello", false) || c.Running[named.ID] || len(named.Turns) != 0 {
		t.Fatal("started despite failed save")
	}
	c.Dir = dir
	t.Setenv("MUJICAGENT_DATA_DIR", blocker)
	c.NewThread("")
	failed := c.State.Thread(c.State.Selected)
	if c.Send("hello", false) || failed.Cwd != "" || c.Running[failed.ID] || len(failed.Turns) != 0 {
		t.Fatal("started despite failed allocation")
	}
}

func TestQueueEditingAndDeletion(t *testing.T) {
	s := model.New()
	c := New(t.TempDir(), s, nil)
	c.AddProject(t.TempDir())
	thread := s.Thread(s.Selected)
	thread.Draft = "ordinary draft"
	thread.Queue = []model.QueuedMessage{{ID: "a", Text: "first"}, {ID: "b", Text: "second"}, {ID: "c", Text: "sending", Sending: true}}
	if !c.EditQueued(thread.ID, "b") {
		t.Fatal("edit failed")
	}
	*c.ComposerDraft(thread) = "edited"
	if thread.Draft != "ordinary draft" || thread.Queue[1].Text != "second" {
		t.Fatal("edit changed original")
	}
	if c.EditQueued(thread.ID, "a") {
		t.Fatal("replaced unsaved edit")
	}
	c.DeleteQueued(thread.ID, "a")
	if !c.Send("edited", false) || thread.Queue[0].ID != "b" || thread.Queue[0].Text != "edited" {
		t.Fatal("save lost original slot")
	}
	if thread.Draft != "ordinary draft" || c.QueueEdit(thread.ID) != nil {
		t.Fatal("save lost draft or edit state")
	}
	if !c.EditQueued(thread.ID, "b") {
		t.Fatal("second edit failed")
	}
	c.CancelQueueEdit(thread.ID)
	if thread.Queue[0].Text != "edited" {
		t.Fatal("cancel changed queue")
	}
	c.DeleteQueued(thread.ID, "c")
	if c.EditQueued(thread.ID, "c") || len(thread.Queue) != 2 {
		t.Fatal("sending message modified")
	}
	c.EditQueued(thread.ID, "b")
	c.DeleteQueued(thread.ID, "b")
	if len(thread.Queue) != 1 || c.QueueEdit(thread.ID) != nil {
		t.Fatal("delete left editing state")
	}
	loaded, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil || len(loaded.Thread(thread.ID).Queue) != 1 {
		t.Fatal("queue deletion not persisted", err)
	}
}

func TestAnnotatedImageInput(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "original.png")
	var original bytes.Buffer
	if err := png.Encode(&original, image.NewRGBA(image.Rect(0, 0, 320, 180))); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, original.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	a := model.Attachment{Kind: "image", Path: path, Annotations: []model.Annotation{{ID: "a", Comment: "Move this button", Marks: []model.Mark{{Kind: "arrow", Color: "#e05263", Width: 3, Points: []model.ImagePoint{{X: 10, Y: 20}, {X: 80, Y: 60}}}}}}}
	input, err := messageInput(a.Link(), dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(input) != 3 {
		t.Fatalf("expected text, original, markup: %+v", input)
	}
	prompt := input[0].(map[string]any)["text"].(string)
	if !strings.Contains(prompt, "Annotation 1: Move this button") || !strings.Contains(prompt, "arrow at (10.00%, 20.00%)") {
		t.Fatalf("lost correspondence: %s", prompt)
	}
	if input[1].(map[string]any)["path"] != path {
		t.Fatal("original not included")
	}
	rendered := input[2].(map[string]any)["path"].(string)
	data, err := os.ReadFile(rendered)
	if err != nil {
		t.Fatal(err)
	}
	img, err := png.Decode(bytes.NewReader(data))
	if err != nil {
		t.Fatal(err)
	}
	// The arrow midpoint must be colored in the actual export.
	r, g, b, _ := img.At(144, 72).RGBA()
	if !(r > g && r > b) {
		t.Fatalf("arrow missing from export: %d %d %d", r, g, b)
	}
	after, err := os.ReadFile(path)
	if err != nil || !bytes.Equal(after, original.Bytes()) {
		t.Fatal("original was overwritten")
	}
	if !strings.HasPrefix(rendered, filepath.Join(dir, "attachments")) {
		t.Fatal("export outside application data")
	}
}

func TestNewThreadReusesOnlyEmptyConversationInProject(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	c.NewThread("")
	first := c.State.Thread(c.State.Selected)
	first.Draft = " \n "
	c.NewThread("")
	if c.State.Selected != first.ID || len(c.State.Threads) != 1 {
		t.Fatal("repeated creation duplicated an empty conversation")
	}
	first.Draft = "draft"
	c.NewThread("")
	blank := c.State.Thread(c.State.Selected)
	if blank == first || first.Draft != "draft" {
		t.Fatal("creation replaced a nonempty draft")
	}
	c.AddProject(t.TempDir())
	projectThread := c.State.Thread(c.State.Selected)
	if projectThread.ProjectID == "" || projectThread == blank {
		t.Fatal("empty conversation reused across projects")
	}
	c.Select(first.ID)
	c.Settle(first.ID, true)
	if c.State.Selected != blank.ID || len(c.State.Threads) != 3 {
		t.Fatal("settling did not reuse the existing blank conversation")
	}
	blank.Turns = []model.Turn{{Prompt: "sent", Status: "completed"}}
	c.NewThread("")
	if c.State.Selected == blank.ID {
		t.Fatal("sent conversation reused as empty")
	}
}

func TestSwitchProjectPreservesPromptAndHistory(t *testing.T) {
	for _, history := range []bool{false, true} {
		t.Run(fmt.Sprint(history), func(t *testing.T) {
			c := New(t.TempDir(), model.New(), nil)
			c.State.Projects = []model.Project{{ID: "target", Name: "Target", Path: t.TempDir()}}
			old := c.State.AddThread("", "")
			old.Draft = "prompt\n<mujica://file/?path=%2Ftmp%2Fsample.txt>"
			prompt := old.Draft
			if history {
				old.ServerID = "server"
				old.Turns = []model.Turn{{Prompt: "sent"}}
			}
			other := c.State.AddThread("target", c.State.Projects[0].Path)
			other.Draft = "existing target draft"
			c.State.Selected = old.ID
			if !c.SwitchProject("target") {
				t.Fatal("switch failed")
			}
			next := c.State.Thread(c.State.Selected)
			if next.Draft != prompt || next.ProjectID != "target" || next.Cwd != c.State.Projects[0].Path {
				t.Fatalf("wrong destination: %+v", next)
			}
			if other.Draft != "existing target draft" || c.State.Projects[0].Expanded {
				t.Fatal("changed existing draft or expansion")
			}
			if history {
				if old == next || old.ProjectID != "" || old.ServerID != "server" || len(old.Turns) != 1 || old.Draft != "" {
					t.Fatal("history changed")
				}
			} else if old != next {
				t.Fatal("unsent conversation duplicated")
			}
			if c.SwitchProject("missing") || c.SwitchProject("target") {
				t.Fatal("invalid/no-op switch accepted")
			}
			if !c.SwitchProject("") || c.State.Thread(c.State.Selected).Cwd != "" {
				t.Fatal("no-project workspace not reset")
			}
			saved, err := model.Load(filepath.Join(c.Dir, "state.json"))
			if err != nil || saved.Thread(saved.Selected).Draft != prompt {
				t.Fatal("draft not persisted", err)
			}
		})
	}
}

func TestHarnessInstallGuardAndFailure(t *testing.T) {
	for _, driver := range []string{"codex", "pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			c := New(t.TempDir(), model.New(), nil)
			original := c.State.Settings.AgentPath(driver)
			*c.HarnessFor(driver) = HarnessState{Binary: original, Release: harness.Release{Version: "1.2.3"}}
			c.InstallHarness(driver)
			if c.HarnessFor(driver).Installing {
				t.Fatal("installed without confirmation preview")
			}
			c.HarnessFor(driver).Plan = &harness.InstallPlan{Release: harness.Release{Version: "1.2.3"}}
			c.CheckHarness(driver)
			if c.HarnessFor(driver).Checking || c.HarnessFor(driver).Plan == nil {
				t.Fatal("background check replaced a pending confirmation")
			}
			c.Running["busy"] = true
			c.InstallHarness(driver)
			if c.HarnessFor(driver).Installing {
				t.Fatal("installed during a turn")
			}
			c.Running["busy"] = false
			c.HarnessFor(driver).Binary = "stale-path"
			c.InstallHarness(driver)
			if c.HarnessFor(driver).Installing {
				t.Fatal("installed stale selection")
			}
			c.HarnessFor(driver).Binary = original
			c.Connection = "Connecting"
			c.InstallHarness(driver)
			if c.HarnessFor(driver).Installing {
				t.Fatal("installed while connecting")
			}
			c.Connection = "Disconnected"
			other := "codex"
			if driver == "codex" {
				other = "pi"
			}
			c.HarnessFor(other).Installing = true
			c.InstallHarness(driver)
			if c.HarnessFor(driver).Installing {
				t.Fatal("concurrent harness installation")
			}
			c.HarnessFor(other).Installing = false
			c.HarnessFor(driver).Plan.Driver = other
			c.InstallHarness(driver)
			if c.HarnessFor(driver).Installing {
				t.Fatal("executed another harness plan")
			}
			c.HarnessFor(driver).Plan.Driver = driver
			if driver != "codex" {
				c.Connection = "Connected"
			}
			// Reinspection fails before execution because the installation is different
			// from the approved empty snapshot (or the explicit path is invalid).
			c.setHarnessPath(driver, filepath.Join(t.TempDir(), "missing-agent"))
			c.HarnessFor(driver).Binary = c.State.Settings.AgentPath(driver)
			original = c.State.Settings.AgentPath(driver)
			c.InstallHarness(driver)
			deadline := time.Now().Add(3 * time.Second)
			for c.HarnessFor(driver).Installing && time.Now().Before(deadline) {
				c.Drain()
				time.Sleep(time.Millisecond)
			}
			if c.HarnessFor(driver).Installing || c.HarnessFor(driver).Error == "" {
				t.Fatal("failure was not published")
			}
			if c.State.Settings.AgentPath(driver) != original {
				t.Fatal("failed install changed active path")
			}
			if driver != "codex" && c.Connection != "Connected" {
				t.Fatal("external installation disconnected Codex")
			}
			entries, err := os.ReadDir(filepath.Join(c.Dir, "tools", driver))
			if (err != nil && !os.IsNotExist(err)) || len(entries) != 0 {
				t.Fatalf("staging not cleaned: %v %v", entries, err)
			}
			if c.HarnessFor(driver).Plan != nil {
				t.Fatal("confirmation was reusable")
			}
			c.HarnessFor(driver).Plan = &harness.InstallPlan{}
			c.DismissHarnessPlan(driver)
			c.InstallHarness(driver)
			if c.HarnessFor(driver).Installing {
				t.Fatal("dismissed preview could install")
			}
		})
	}
}

func TestHarnessPreviewCancellationAndStaleResult(t *testing.T) {
	for _, driver := range []string{"codex", "pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			c := New(t.TempDir(), model.New(), nil)
			c.Connection = "Connected"
			u := c.HarnessFor(driver)
			c.setHarnessPath(driver, filepath.Join(t.TempDir(), "missing-old"))
			c.PrepareHarness(driver, "")
			old := u.planRequest
			c.DismissHarnessPlan(driver)
			if u.Preparing || u.Plan != nil || old == u.planRequest || c.Connection != "Connected" {
				t.Fatal("preview dismissal changed the connection or retained confirmation")
			}
			c.setHarnessPath(driver, filepath.Join(t.TempDir(), "missing-new"))
			c.PrepareHarness(driver, "cn")
			deadline := time.Now().Add(5 * time.Second)
			for u.Preparing && time.Now().Before(deadline) {
				c.Drain()
				time.Sleep(time.Millisecond)
			}
			c.Drain()
			if u.Preparing || u.Plan != nil || u.Error == "" || !strings.Contains(u.Error, "missing-new") || c.Connection != "Connected" {
				t.Fatalf("stale preview won or probing disconnected: %+v", u)
			}
			if entries, err := os.ReadDir(c.Dir); err != nil || len(entries) != 0 {
				t.Fatalf("preview wrote application data: %v %v", entries, err)
			}
		})
	}
}

func TestAgentHandoffOwnership(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	old := c.State.AddThread("project", "/workspace")
	old.ServerID = "codex-server"
	old.Draft = "continue"
	old.Queue = []model.QueuedMessage{{ID: "q", Text: "queued"}}
	old.Turns = []model.Turn{{Prompt: "task", Items: []model.Item{{Kind: "agentMessage", Text: "answer"}, {Kind: "reasoning", Text: "private reasoning"}}}}
	c.Running[old.ID] = true
	if c.Handoff("pi", "provider/model") {
		t.Fatal("handed off running task")
	}
	c.Running[old.ID] = false
	if !c.Handoff("pi", "provider/model") {
		t.Fatal("handoff rejected")
	}
	next := c.State.Thread(c.State.Selected)
	if next.ServerID != "" || next.Driver() != "pi" || next.AgentModel != "provider/model" || next.HandoffFrom != old.ID || next.Cwd != old.Cwd {
		t.Fatalf("wrong ownership: %+v", next)
	}
	if !strings.Contains(next.HandoffContext, "answer") || strings.Contains(next.HandoffContext, "private reasoning") {
		t.Fatal("wrong transcript")
	}
	if old.ServerID != "codex-server" || old.Draft != "continue" || len(old.Queue) != 1 || len(next.Queue) != 0 {
		t.Fatal("source modified or queue replayed")
	}
	restored, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if restored.Thread(next.ID).Driver() != "pi" || restored.Thread(old.ID).Driver() != "codex" {
		t.Fatal("ownership lost")
	}
	next.Turns = []model.Turn{{Prompt: "followup", Items: []model.Item{{Kind: "agentMessage", Text: "new answer"}}}}
	if !c.Handoff("droid", "another-model") {
		t.Fatal("second handoff failed")
	}
	last := c.State.Thread(c.State.Selected)
	if !strings.Contains(last.HandoffContext, "answer") || !strings.Contains(last.HandoffContext, "new answer") {
		t.Fatal("ancestry lost")
	}
	if c.HarnessFor("pi") == c.HarnessFor("codex") {
		t.Fatal("shared harness state")
	}
}

func TestAgentProcessLifecycle(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Unix fake CLI; Windows argument resolution tested separately")
	}
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			dir := t.TempDir()
			t.Setenv("MUJICAGENT_DATA_DIR", dir)
			binary := filepath.Join(dir, "fake agent")
			output := `{"type":"result","session_id":"owned-droid","result":"done"}`
			if driver == "pi" {
				output = "{\"type\":\"message_end\",\"message\":{\"role\":\"assistant\",\"content\":[{\"type\":\"text\",\"text\":\"done\"}]}}\n{\"type\":\"agent_settled\"}"
			}
			script := "#!/bin/sh\nread request\nprintf '%s\\n' '" + output + "'\n"
			if driver == "droid" {
				script = `#!/bin/sh
read request
printf '%s\n' '{"id":"request-1","result":{"sessionId":"owned-droid"}}'
read request
case "$request" in *'"autonomyLevel":"off"'*) ;; *) exit 2;; esac
printf '%s\n' '{"id":"request-2","result":{}}'
read request
printf '%s\n' '{"id":"request-3","result":{}}'
printf '%s\n' '{"method":"droid.session_notification","params":{"sessionId":"owned-droid","notification":{"type":"assistant_text_delta","messageId":"m","blockIndex":0,"textDelta":"done"}}}'
printf '%s\n' '{"method":"droid.session_notification","params":{"sessionId":"owned-droid","notification":{"type":"agent_turn_completed","reason":"completed"}}}'
sleep 20
`
			}
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			c := New(dir, model.New(), nil)
			defer c.Close()
			c.State.Settings.PiPath = binary
			c.State.Settings.DroidPath = binary
			thread := c.State.AddThread("project", dir)
			thread.Harness = driver
			thread.AgentModel = "custom"
			if !c.Send("first", false) {
				t.Fatal(c.Error)
			}
			deadline := time.Now().Add(5 * time.Second)
			for c.Running[thread.ID] && time.Now().Before(deadline) {
				c.Drain()
				time.Sleep(time.Millisecond)
			}
			c.Drain()
			if c.Running[thread.ID] || thread.Turns[0].Status != "completed" || thread.Turns[0].Items[0].Text != "done" || thread.ServerID == "" {
				t.Fatalf("%+v %s", thread, c.Error)
			}
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nexec sleep 20\n"), 0700); err != nil {
				t.Fatal(err)
			}
			if !c.Send("second", false) {
				t.Fatal(c.Error)
			}
			c.Interrupt()
			deadline = time.Now().Add(5 * time.Second)
			for c.Running[thread.ID] && time.Now().Before(deadline) {
				c.Drain()
				time.Sleep(time.Millisecond)
			}
			c.Drain()
			if thread.Turns[1].Status != "interrupted" {
				t.Fatalf("not cancelled: %+v", thread.Turns[1])
			}
		})
	}
}

func TestAgentEventMergeAndPersistence(t *testing.T) {
	turn := model.Turn{Status: "inProgress"}
	for _, e := range []harness.AgentEvent{
		{ID: "m1", Kind: "agentMessage", Text: "Before", Delta: true},
		{ID: "a", Tool: &harness.ToolEvent{Name: "Read", Kind: "read", Arguments: json.RawMessage(`{"path":"a","api_key":"secret"}`)}},
		{ID: "b", Tool: &harness.ToolEvent{Name: "Read", Kind: "read", Arguments: json.RawMessage(`{"path":"b"}`)}},
		{ID: "a", Tool: &harness.ToolEvent{Result: json.RawMessage(`"partial"`)}},
		{ID: "m2", Kind: "agentMessage", Text: "After", Delta: true},
		{ID: "a", Status: "completed", Tool: &harness.ToolEvent{Result: json.RawMessage(`["complete"]`)}},
		{ID: "b", Status: "failed", Tool: &harness.ToolEvent{Result: json.RawMessage(`{"error":"denied"}`)}},
		{ID: "m1", Kind: "agentMessage", Text: "Before", Status: "completed"},
	} {
		mergeAgentEvent(&turn, e)
	}
	if len(turn.Items) != 4 || turn.Items[0].Text != "Before" || turn.Items[3].Text != "After" {
		t.Fatalf("order/snapshot: %+v", turn.Items)
	}
	a, b := turn.Items[1], turn.Items[2]
	if a.Kind != "toolCall" || a.Status != "completed" || strings.Contains(a.MCP.Arguments, "secret") || strings.Contains(a.MCP.Result, "partial") || b.Status != "failed" || b.MCP.Error == "" {
		t.Fatalf("tool merge: %+v %+v", a, b)
	}
	s := model.New()
	thread := s.AddThread("", t.TempDir())
	thread.Harness = "pi"
	thread.Turns = []model.Turn{turn}
	path := filepath.Join(t.TempDir(), "state.json")
	if err := model.Save(path, s); err != nil {
		t.Fatal(err)
	}
	restored, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	items := restored.Thread(thread.ID).Turns[0].Items
	if len(items) != 4 || items[1].MCP.Result != a.MCP.Result || items[2].Status != "failed" {
		t.Fatal("tool details lost on restart")
	}
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "mcp", Tool: &harness.ToolEvent{Kind: "mcp", Name: "lookup", Server: "docs"}})
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "mcp", Status: "completed", Tool: &harness.ToolEvent{Kind: "other", Name: "opaque_wire_name", Result: json.RawMessage(`"done"`)}})
	call := turn.Items[len(turn.Items)-1]
	if call.Kind != "mcpToolCall" || call.MCP.Server != "docs" || call.MCP.Tool != "lookup" || call.MCP.Result != `"done"` {
		t.Fatalf("sparse update replaced MCP identity: %+v", call)
	}
}

func TestAgentPlanHandoffAndMultiAnswer(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	defer c.Close()
	thread := c.State.AddThread("", t.TempDir())
	thread.Harness = "droid"
	turn := model.Turn{Status: "completed"}
	mergeAgentEvent(&turn, harness.AgentEvent{ID: "todo", Tool: &harness.ToolEvent{Kind: "plan", Arguments: json.RawMessage(`{"todos":"- [x] Read\n- [ ] Verify"}`)}})
	thread.Turns = []model.Turn{turn}
	if !c.Handoff("pi", "model") || !strings.Contains(c.State.Thread(c.State.Selected).HandoffContext, "- [ ] Verify") {
		t.Fatal("readable plan lost during handoff")
	}
	r := &codex.Request{ID: json.RawMessage(`"multi"`), Method: "item/tool/requestUserInput", Cancellable: true, Questions: []codex.Question{{ID: "q", MultiSelect: true}}}
	ch := make(chan any, 1)
	c.agentReplies = map[*codex.Request]chan any{r: ch}
	c.Requests = []*codex.Request{r}
	c.AnswerQuestions(r, map[string]string{"q": `["A","B"]`, "q:other": "C"})
	select {
	case reply := <-ch:
		data, _ := json.Marshal(reply)
		if !strings.Contains(string(data), `["A","B","C"]`) {
			t.Fatal(string(data))
		}
	default:
		t.Fatal("multi answer not delivered")
	}
	if len(c.Requests) != 0 {
		t.Fatal("answered request remains visible")
	}
	r.Responding = false
	c.Requests = []*codex.Request{r}
	c.agentReplies[r] = ch
	c.CancelQuestions(r)
	select {
	case reply := <-ch:
		data, _ := json.Marshal(reply)
		if !strings.Contains(string(data), `"cancelled":true`) {
			t.Fatal(string(data))
		}
	default:
		t.Fatal("cancel not delivered")
	}
}

func TestAgentFailedQueueRemainsRetryable(t *testing.T) {
	dir := t.TempDir()
	c := New(dir, model.New(), nil)
	defer c.Close()
	c.State.Settings.DroidPath = filepath.Join(dir, "missing-droid")
	thread := c.State.AddThread("project", dir)
	thread.Harness = "droid"
	thread.Queue = []model.QueuedMessage{{ID: "q", Text: "retry me"}}
	c.ResumeQueue(thread.ID)
	deadline := time.Now().Add(5 * time.Second)
	for c.Running[thread.ID] && time.Now().Before(deadline) {
		c.Drain()
		time.Sleep(time.Millisecond)
	}
	c.Drain()
	if len(thread.Queue) != 1 || thread.Queue[0].Sending || thread.Turns[0].Status != "failed" {
		t.Fatalf("lost failed queue: %+v", thread)
	}
}

func TestModelFavoritesPersistExactPairs(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	a, b := model.ModelFavorite{Harness: "codex", ModelID: "same"}, model.ModelFavorite{Harness: "droid", ModelID: "same"}
	c.State.ToggleModelFavorite(a)
	if c.State.IsModelFavorite(b) {
		t.Fatal("favorite leaked across harnesses")
	}
	c.State.ToggleModelFavorite(b)
	c.Save()
	restored, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !restored.IsModelFavorite(a) || !restored.IsModelFavorite(b) {
		t.Fatal("favorites not persisted")
	}
	restored.ToggleModelFavorite(a)
	if restored.IsModelFavorite(a) || !restored.IsModelFavorite(b) {
		t.Fatal("unfavorite removed wrong tuple")
	}
	restored.ToggleModelFavorite(model.ModelFavorite{Harness: "droid"})
	if len(restored.Settings.ModelFavorites) != 1 {
		t.Fatal("default model is not a stable model ID")
	}
}

func TestModelDiscoveryReplacesStaleRequest(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	dir := t.TempDir()
	c := New(dir, model.New(), nil)
	defer c.Close()
	first, second := filepath.Join(dir, "first"), filepath.Join(dir, "second")
	for _, p := range []string{first, second} {
		script := "#!/bin/sh\nread request\n"
		if p == first {
			script += "sleep 1\n"
		}
		script += "printf '%s\\n' '{\"id\":\"models\",\"result\":{\"models\":[{\"id\":\"" + filepath.Base(p) + "\",\"supportedReasoningEfforts\":[\"high\"]}]}}'\nsleep 20\n"
		if err := os.WriteFile(p, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
	}
	c.State.Settings.DroidPath = first
	thread := c.State.AddThread("", dir)
	thread.Harness, thread.AgentModel, thread.AgentEffort = "droid", "second", "low"
	c.LoadAgentModels("droid", false)
	c.State.Settings.DroidPath = second
	c.LoadAgentModels("droid", false)
	deadline := time.Now().Add(4 * time.Second)
	h := c.HarnessFor("droid")
	for h.ModelsLoading && time.Now().Before(deadline) {
		c.Drain()
		time.Sleep(time.Millisecond)
	}
	c.Drain()
	if h.ModelsLoading || h.ModelsError != "" || len(h.Models) != 1 || h.Models[0].ID != "second" {
		t.Fatalf("stale catalog: %+v", h)
	}
	if thread.AgentEffort != "high" {
		t.Fatal("refresh retained unsupported effort", thread.AgentEffort)
	}
	saved, err := model.Load(filepath.Join(dir, "state.json"))
	if err != nil || saved.Thread(thread.ID).AgentEffort != "high" {
		t.Fatal("normalized effort was not saved", err)
	}
	request := h.modelsRequest
	c.LoadAgentModels("droid", false)
	if request != h.modelsRequest {
		t.Fatal("cache ignored")
	}
}
