package codex

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// The child process exercises real pipes, including a server-initiated Ask request.
func TestProtocolChild(t *testing.T) {
	if os.Getenv("MUJICAGENT_PROTOCOL_CHILD") != "1" {
		return
	}
	dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	initialized := false
	project := ""
	for {
		var m Message
		if dec.Decode(&m) != nil {
			os.Exit(0)
		}
		switch m.Method {
		case "initialize":
			var p struct {
				ClientInfo   struct{ Name string }
				Capabilities struct {
					ExperimentalAPI bool `json:"experimentalApi"`
				}
			}
			json.Unmarshal(m.Params, &p)
			if p.ClientInfo.Name != "mujicagent" || !p.Capabilities.ExperimentalAPI {
				os.Exit(2)
			}
			enc.Encode(map[string]any{"id": m.ID, "result": map[string]string{"userAgent": "test/1"}})
		case "initialized":
			initialized = true
		case "permissionProfile/list":
			var p struct {
				Cursor *string
				Cwd    string
			}
			json.Unmarshal(m.Params, &p)
			if !strings.HasPrefix(p.Cwd, "/project") {
				os.Exit(5)
			}
			project = p.Cwd
			result := map[string]any{"data": []PermissionProfile{{ID: ":read-only", Allowed: false}}}
			if p.Cursor == nil {
				result = map[string]any{"data": []PermissionProfile{{ID: ":workspace", Allowed: true}, {ID: ":danger-full-access", Allowed: true}}, "nextCursor": "page-2"}
			}
			enc.Encode(map[string]any{"id": m.ID, "result": result})
		case "configRequirements/read":
			requirements := map[string]any{"allowedApprovalPolicies": []string{"on-request"}}
			if project == "/project/restricted" {
				requirements["allowedApprovalsReviewers"] = []string{"user"}
			}
			enc.Encode(map[string]any{"id": m.ID, "result": map[string]any{"requirements": requirements}})
		case "experimentalFeature/list":
			features := []any{}
			if project != "/project/no-auto" {
				features = append(features, map[string]any{"name": "guardian_approval", "stage": "stable", "enabled": true})
			}
			enc.Encode(map[string]any{"id": m.ID, "result": map[string]any{"data": features}})
		case "config/read":
			features := map[string]bool{}
			if project == "/project/disabled" {
				features["guardian_approval"] = false
			}
			enc.Encode(map[string]any{"id": m.ID, "result": map[string]any{"config": map[string]any{"features": features}}})
		case "ask":
			if !initialized {
				os.Exit(3)
			}
			enc.Encode(map[string]any{"id": "ask-7", "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "t1", "turnId": "turn1", "questions": []any{map[string]string{"id": "choice", "question": "Which?", "header": "Choose"}}}})
			enc.Encode(map[string]any{"id": m.ID, "result": map[string]bool{"ok": true}})
		case "error":
			enc.Encode(map[string]any{"id": m.ID, "error": RPCError{Code: 42, Message: "intentional"}})
		case "hang":
		case "exit":
			os.Exit(0)
		case "":
			var result struct {
				Answers map[string]struct{ Answers []string }
			}
			json.Unmarshal(m.Result, &result)
			if string(m.ID) != `"ask-7"` || len(result.Answers["choice"].Answers) != 1 || result.Answers["choice"].Answers[0] != "原生 UI" {
				os.Exit(4)
			}
			enc.Encode(map[string]any{"method": "answer/accepted", "params": map[string]bool{"ok": true}})
		}
	}
}

func TestRoundTripAndAsk(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test launcher")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(t.TempDir(), "codex")
	if err := os.WriteFile(binary, []byte(fmt.Sprintf("#!/bin/sh\nexec '%s' -test.run=TestProtocolChild -- \"$@\"\n", strings.ReplaceAll(exe, "'", "'\\''"))), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUJICAGENT_PROTOCOL_CHILD", "1")
	events := make(chan Message, 16)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, err := Start(ctx, binary, func(m Message) { events <- m })
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	profiles, err := c.PermissionProfiles(ctx, "/project")
	if err != nil || len(profiles) != 4 || !profiles[0].Allowed || !profiles[1].Allowed || profiles[1].ID != ":approve-for-me" || profiles[2].Allowed || profiles[3].Allowed {
		t.Fatalf("profiles must be paginated and obey both profile and approval-policy restrictions: %+v, %v", profiles, err)
	}
	for _, project := range []string{"/project/no-auto", "/project/disabled", "/project/restricted"} {
		profiles, err := c.PermissionProfiles(ctx, project)
		if err != nil {
			t.Fatal(err)
		}
		autoFound := false
		for _, p := range profiles {
			if p.ID == ":approve-for-me" {
				autoFound = true
				if p.Allowed {
					t.Fatalf("auto review should not be allowed in %s", project)
				}
			}
		}
		if autoFound != (project == "/project/restricted") {
			t.Fatalf("incorrect capability filtering for %s", project)
		}
	}
	if err := c.Call(ctx, "ask", map[string]any{}, nil); err != nil {
		t.Fatal(err)
	}
	var ask Message
	select {
	case ask = <-events:
	case <-ctx.Done():
		t.Fatal("Ask not delivered")
	}
	if ask.Method != "item/tool/requestUserInput" {
		t.Fatal(ask.Method)
	}
	if err := c.Reply(ask.ID, map[string]any{"answers": map[string]any{"choice": map[string]any{"answers": []string{"原生 UI"}}}}); err != nil {
		t.Fatal(err)
	}
	select {
	case reply := <-events:
		if reply.Method != "answer/accepted" {
			t.Fatal(reply.Method)
		}
	case <-ctx.Done():
		t.Fatal("answer not received")
	}
	if err := c.Call(ctx, "error", nil, nil); err == nil || !strings.Contains(err.Error(), "intentional") {
		t.Fatalf("RPC error lost: %v", err)
	}
	short, stop := context.WithTimeout(ctx, 20*time.Millisecond)
	defer stop()
	if err := c.Call(short, "hang", nil, nil); err == nil {
		t.Fatal("missing timeout")
	}
	if err := c.Call(ctx, "exit", nil, nil); err == nil {
		t.Fatal("missing exit error")
	}
}

func TestDesktopLaunch(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("macOS desktop environment")
	}
	home := t.TempDir()
	bin := filepath.Join(home, "tools with spaces")
	if err := os.Mkdir(bin, 0700); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	files := map[string]string{
		filepath.Join(home, ".zshrc"):                 "printf 'profile startup output\\n'\nexport PATH=" + quote(bin) + ":$PATH\n",
		filepath.Join(bin, "codex"):                   "#!/usr/bin/env mujicagent-test-runtime\n",
		filepath.Join(bin, "mujicagent-test-runtime"): "#!/bin/sh\nshift\nif [ \"$1\" = --version ]; then printf 'codex-cli test\\n'; exit 0; fi\nexec " + quote(exe) + " -test.run=^TestProtocolChild$ -- \"$@\"\n",
	}
	for path, content := range files {
		if err := os.WriteFile(path, []byte(content), 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("HOME", home)
	t.Setenv("ZDOTDIR", home)
	t.Setenv("SHELL", "/bin/zsh")
	t.Setenv("PATH", "/usr/bin:/bin:/usr/sbin:/sbin")
	t.Setenv("MUJICAGENT_PROTOCOL_CHILD", "1")
	for name, binary := range map[string]string{"from PATH": "codex", "explicit path": filepath.Join(bin, "codex")} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			c, err := Start(ctx, binary, func(Message) {})
			if err != nil {
				t.Fatalf("desktop launch must find the CLI and its runtime from the login shell: %v", err)
			}
			defer c.Close()
			if version := c.Version(); version != "codex-cli test" {
				t.Fatalf("version lookup must share the server's executable and PATH: %q", version)
			}
		})
	}
	if os.Getenv("PATH") != "/usr/bin:/bin:/usr/sbin:/sbin" {
		t.Fatal("desktop discovery must not change the application environment")
	}
	if _, err := desktopCommand(context.Background(), filepath.Join(home, "missing", "codex")); err == nil {
		t.Fatal("an invalid explicit path must not fall back to another installation")
	}
	t.Run("profile unavailable", func(t *testing.T) {
		t.Setenv("SHELL", filepath.Join(home, "missing-shell"))
		t.Setenv("PATH", bin+":/usr/bin:/bin")
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		c, err := Start(ctx, "codex", func(Message) {})
		if err != nil {
			t.Fatalf("an unavailable shell must preserve the inherited PATH: %v", err)
		}
		c.Close()
	})
	t.Run("profile timeout", func(t *testing.T) {
		shell := filepath.Join(home, "slow-shell")
		if err := os.WriteFile(shell, []byte("#!/bin/sh\nexec /bin/sleep 30\n"), 0700); err != nil {
			t.Fatal(err)
		}
		t.Setenv("SHELL", shell)
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		started := time.Now()
		if _, err := desktopCommand(ctx, "codex"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("shell discovery must respect the connection deadline: %v", err)
		}
		if time.Since(started) > 2*time.Second {
			t.Fatal("shell discovery did not stop promptly")
		}
	})
}

func TestApprovalOptions(t *testing.T) {
	for _, tc := range []struct{ name, method, params, kinds string }{
		{"server list", "commandExecution", `{"availableDecisions":["accept","acceptForSession","decline","cancel"]}`, "accept,acceptForSession,decline,cancel"},
		{"server excludes rule", "commandExecution", `{"availableDecisions":["accept","cancel"],"proposedExecpolicyAmendment":["git"]}`, "accept,cancel"},
		{"empty list", "commandExecution", `{"availableDecisions":[]}`, ""},
		{"unknown option", "commandExecution", `{"availableDecisions":["unknown","cancel"]}`, "cancel"},
		{"human checkpoint", "commandExecution", `{"kind":"humanCommand","availableDecisions":["accept","acceptForSession","decline","cancel"]}`, "accept,decline,cancel"},
		{"command rule", "commandExecution", `{"proposedExecpolicyAmendment":["git","status"]}`, "accept,execRule,cancel"},
		{"network rule", "commandExecution", `{"networkApprovalContext":{"host":"example.com"},"proposedNetworkPolicyAmendments":[{"host":"example.com","action":"allow"}]}`, "accept,acceptForSession,network-allow,cancel"},
		{"extra permissions", "commandExecution", `{"additionalPermissions":{"network":{"enabled":true}},"proposedExecpolicyAmendment":["git"]}`, "accept,cancel"},
		{"file approval", "fileChange", `{}`, "accept,acceptForSession,decline,cancel"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var r Request
			if err := json.Unmarshal([]byte(tc.params), &r); err != nil {
				t.Fatal(err)
			}
			r.Method = "item/" + tc.method + "/requestApproval"
			var kinds []string
			for _, o := range r.ApprovalOptions() {
				kinds = append(kinds, o.Kind)
			}
			if strings.Join(kinds, ",") != tc.kinds {
				t.Fatalf("wrong options: %v", kinds)
			}
		})
	}
}
