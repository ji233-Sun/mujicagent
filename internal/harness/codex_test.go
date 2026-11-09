package harness

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestLocalManagerPreviewAndExecution(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixtures; native Windows routing is checked separately")
	}
	for _, method := range []string{"vp", "npm", "brew"} {
		t.Run(method, func(t *testing.T) {
			root := filepath.Join(t.TempDir(), "工具 with spaces")
			bin := filepath.Join(root, "bin")
			if err := os.MkdirAll(bin, 0700); err != nil {
				t.Fatal(err)
			}
			log := filepath.Join(root, "args")
			version := filepath.Join(root, "version")
			t.Setenv("HARNESS_TEST_ARGS", log)
			t.Setenv("HARNESS_TEST_VERSION", version)
			if err := os.WriteFile(version, []byte("0.167.0"), 0600); err != nil {
				t.Fatal(err)
			}
			program := "#!/bin/sh\nif [ \"${0##*/}\" = codex ]; then\n if [ \"$1\" = --version ]; then printf 'codex-cli '; cat \"$HARNESS_TEST_VERSION\"; printf '\\n'; fi\n exit 0\nfi\nprintf '%s\\n' \"$@\" > \"$HARNESS_TEST_ARGS\"\nprintf '0.168.0' > \"$HARNESS_TEST_VERSION\"\n"
			manager := method
			managerPath := filepath.Join(bin, manager)
			if err := os.WriteFile(managerPath, []byte(program), 0700); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(bin, "codex")
			if err := os.Symlink(managerPath, path); err != nil {
				t.Fatal(err)
			}
			current := Installation{Driver: "codex", Path: path, Version: "0.167.0", Method: method, Prefix: root}
			if method == "vp" {
				current.Prefix = managerPath
				inspected, err := Inspect(context.Background(), "codex", path, filepath.Join(root, "managed"))
				if err != nil || inspected != current {
					t.Fatalf("VP launcher: %+v %v", inspected, err)
				}
			}
			plan, err := PrepareInstall(context.Background(), "codex", filepath.Join(root, "managed"), "", Release{Version: "0.168.0"}, current)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(log); !os.IsNotExist(err) {
				t.Fatal("preview ran updater")
			}
			if plan.Stage != "" || plan.Path != path || !strings.Contains(plan.CommandPreview(), "'"+managerPath+"'") {
				t.Fatalf("wrong preview: %+v", plan)
			}
			want := []string{"update", "--global", "@openai/codex@0.168.0"}
			if method == "npm" {
				want = []string{"install", "--global", "--prefix", root, "@openai/codex@0.168.0", "--ignore-scripts", "--no-audit", "--no-fund"}
			} else if method == "brew" {
				want = []string{"upgrade", "--cask", "codex"}
			}
			if !slices.Equal(plan.Args, want) {
				t.Fatal(plan.Args)
			}
			result, err := plan.Execute(context.Background())
			if err != nil || result != path {
				t.Fatal(result, err)
			}
			args, err := os.ReadFile(log)
			if err != nil || string(args) != strings.Join(want, "\n")+"\n" {
				t.Fatalf("executed another command: %q %v", args, err)
			}
		})
	}
}

// Exercises actual pipes and process cleanup without any account or model use.
func TestClaudeProtocolChild(t *testing.T) {
	mode := os.Getenv("MUJICAGENT_CLAUDE_CHILD")
	if mode == "" {
		return
	}
	dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	var init map[string]any
	if dec.Decode(&init) != nil || init["type"] != "control_request" {
		os.Exit(3)
	}
	enc.Encode(map[string]any{"type": "assistant", "message": map[string]any{"id": "history", "content": []any{map[string]string{"type": "text", "text": "Do not replay history"}}}})
	enc.Encode(claudeControlResponse("initialize", map[string]any{"models": []any{
		map[string]any{"value": "default", "resolvedModel": "resolved-model"},
		map[string]any{"value": "opus", "resolvedModel": "resolved-model", "displayName": "Opus", "supportsEffort": true, "supportedEffortLevels": []string{"low", "high", "high"}},
		map[string]any{"value": "custom-model", "displayName": "Custom"},
	}}, ""))
	if mode == "catalog" {
		var next map[string]any
		if dec.Decode(&next) == nil {
			os.Exit(4) // Discovery must never submit a user message.
		}
		os.Exit(0)
	}
	var prompt struct {
		Type, UUID string
		Message    struct {
			Content []struct {
				Type   string
				Source struct {
					Type, Data string
					MediaType  string `json:"media_type"`
				}
			}
		}
	}
	if dec.Decode(&prompt) != nil || prompt.Type != "user" || prompt.UUID == "" {
		os.Exit(5)
	}
	if mode == "reject" {
		enc.Encode(map[string]any{"type": "result", "subtype": "error_during_execution", "is_error": true})
		os.Exit(0)
	}
	if mode == "image" && (len(prompt.Message.Content) != 2 || prompt.Message.Content[1].Source.Type != "base64" || prompt.Message.Content[1].Source.MediaType != "image/png" || prompt.Message.Content[1].Source.Data == "") {
		os.Exit(6)
	}
	enc.Encode(map[string]any{"type": "user", "uuid": prompt.UUID, "session_id": "claude-session", "message": map[string]string{"role": "user", "content": "accepted"}})
	if mode == "steer" {
		var next map[string]any
		if dec.Decode(&next) != nil || next["type"] != "user" || next["uuid"] == nil {
			os.Exit(7)
		}
		enc.Encode(map[string]any{"type": "user", "uuid": next["uuid"], "message": map[string]string{"role": "user", "content": "steer accepted"}})
	}
	if mode == "cancel" || mode == "stop" {
		enc.Encode(map[string]any{"type": "control_request", "request_id": "blocked", "request": map[string]any{"subtype": "can_use_tool", "tool_name": "Bash", "input": map[string]string{"command": "pwd"}}})
		if mode == "stop" {
			var next any
			dec.Decode(&next)
			os.Exit(0)
		}
		enc.Encode(map[string]string{"type": "control_cancel_request", "request_id": "blocked"})
	}
	enc.Encode(map[string]any{"type": "system", "subtype": "session_state_changed", "state": "running"})
	enc.Encode(map[string]any{"type": "system", "subtype": "task_started", "task_id": "background", "task_type": "local_agent"})
	enc.Encode(map[string]any{"type": "result", "subtype": "success"})
	enc.Encode(map[string]any{"type": "system", "subtype": "task_notification", "task_id": "background"})
	enc.Encode(map[string]any{"type": "assistant", "message": map[string]any{"id": "reply", "content": []any{map[string]string{"type": "text", "text": "Finished after background task"}}}})
	enc.Encode(map[string]any{"type": "result", "subtype": "success"})
	enc.Encode(map[string]any{"type": "system", "subtype": "session_state_changed", "state": "idle"})
	var next any
	dec.Decode(&next)
	os.Exit(0)
}

func TestClaudeSessionProtocol(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX launcher; Windows entry layouts are tested separately")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []string{"catalog", "turn", "reject", "steer", "cancel", "stop", "image"} {
		t.Run(mode, func(t *testing.T) {
			t.Setenv("MUJICAGENT_CLAUDE_CHILD", mode)
			dir := t.TempDir()
			binary := filepath.Join(dir, "claude")
			script := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=^TestClaudeProtocolChild$ -- \"$@\"\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if mode == "catalog" {
				models, err := DiscoverModels(ctx, "claude", binary, dir)
				if err != nil || len(models) != 2 || models[0].ID != "resolved-model" || len(models[0].Efforts) != 2 || len(models[1].Efforts) != 0 {
					t.Fatalf("catalog: %+v %v", models, err)
				}
				return
			}
			o := RunOptions{Driver: "claude", Binary: binary, Cwd: dir, Session: "claude-session", Model: "custom-model", Access: "acceptEdits", Effort: "high", Prompt: "Run", Control: &Control{}}
			if mode == "image" {
				path := filepath.Join(dir, "图像.png")
				var data bytes.Buffer
				if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, data.Bytes(), 0600); err != nil {
					t.Fatal(err)
				}
				o.Images = []string{path}
			}
			session, err := NewSession(ctx, o)
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			var events []AgentEvent
			steered := make(chan error, 1)
			session.Run(func(e AgentEvent) {
				events = append(events, e)
				if e.Accepted && mode == "steer" {
					go func() { steered <- session.Steer(ctx, TurnTarget{}, Input{{Type: "text", Text: "follow-up"}}) }()
				}
				if e.Interaction != nil && mode == "stop" {
					cancel()
				}
			})
			accepted, resolved, final := 0, 0, ""
			var end *TurnResult
			for _, e := range events {
				if e.Accepted {
					accepted++
				}
				if e.Resolved != nil {
					resolved++
				}
				if e.ID == "history-0" {
					t.Fatal("resume history replayed into turn")
				}
				if e.Kind == "agentMessage" {
					final = e.Text
				}
				if e.End != nil {
					end = e.End
				}
			}
			if end == nil {
				t.Fatal("no terminal result")
			}
			if mode == "reject" {
				if accepted != 0 || end.Status != "failed" {
					t.Fatal(events)
				}
				return
			}
			if accepted != 1 {
				t.Fatalf("acceptances=%d", accepted)
			}
			if mode == "stop" {
				if end.Status != "interrupted" || resolved != 1 {
					t.Fatal(events)
				}
			} else if end.Status != "completed" || final != "Finished after background task" {
				t.Fatal("settled before background work or failed", events)
			}
			if mode == "cancel" && resolved != 1 {
				t.Fatal("cancelled interaction not resolved")
			}
			if mode == "steer" {
				select {
				case err := <-steered:
					if err != nil {
						t.Fatal(err)
					}
				case <-ctx.Done():
					t.Fatal("steer acknowledgment missing")
				}
			}
		})
	}
}

func TestClaudeToolsAndCompaction(t *testing.T) {
	var events []AgentEvent
	p := claudeParser{emit: func(e AgentEvent) { events = append(events, e) }}
	for _, raw := range []string{
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"msg"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"partial"}}}`,
		`{"type":"assistant","message":{"id":"msg","content":[{"type":"text","text":"final"},{"type":"tool_use","id":"bash-1","name":"Bash","input":{"command":"pwd"}},{"type":"tool_use","id":"bash-2","name":"Bash","input":{"command":"ls"}},{"type":"tool_use","id":"edit","name":"Edit","input":{"file_path":"a.go","old_string":"old","new_string":"new"}},{"type":"tool_use","id":"write","name":"Write","input":{"file_path":"b.go","content":"new"}},{"type":"tool_use","id":"read","name":"Read","input":{"file_path":"b.go"}},{"type":"tool_use","id":"todo","name":"TodoWrite","input":{"todos":[{"content":"Verify","status":"in_progress"}]}},{"type":"tool_use","id":"mcp","name":"mcp__docs__lookup","input":{"query":"Go"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"bash-1","content":[{"type":"text","text":"workspace"}]},{"type":"tool_result","tool_use_id":"bash-2","is_error":true,"content":{"error":"Denied"}},{"type":"tool_result","tool_use_id":"read","content":"package main"}]}}`,
		`{"type":"system","subtype":"status","status":"compacting"}`,
		`{"type":"system","subtype":"compact_boundary","compact_metadata":{"pre_tokens":1000}}`,
		`{"type":"system","subtype":"status","status":"compacting"}`,
	} {
		if err := p.line([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	p.finishCompaction("interrupted", "")
	tools := map[string]AgentEvent{}
	compacts := map[string]AgentEvent{}
	for _, e := range events {
		if e.Tool != nil {
			if old, ok := tools[e.ID]; ok && e.Tool.Arguments == nil {
				e.Tool.Arguments = old.Tool.Arguments
			}
			tools[e.ID] = e
		}
		if e.Kind == "contextCompaction" {
			compacts[e.ID] = e
		}
	}
	for id, kind := range map[string]string{"bash-1": "execute", "bash-2": "execute", "edit": "edit", "write": "edit", "read": "read", "todo": "plan", "mcp": "mcp"} {
		if tools[id].Tool == nil || tools[id].Tool.Kind != kind {
			t.Fatal(id, tools[id])
		}
	}
	if tools["bash-1"].Status != "completed" || tools["bash-2"].Status != "failed" || tools["mcp"].Tool.Server != "docs" || tools["mcp"].Tool.Name != "lookup" || len(compacts) != 2 || compacts["claude-compact-2"].Status != "interrupted" {
		t.Fatal(tools, compacts)
	}
}

func TestClaudeQuestionAndApproval(t *testing.T) {
	for _, mode := range []string{"ask", "hook", "cancel", "allow", "deny", "unknown"} {
		t.Run(mode, func(t *testing.T) {
			req := claudeRequest{Subtype: "can_use_tool", ToolName: "Bash", Input: json.RawMessage(`{"command":"pwd"}`)}
			if mode == "ask" || mode == "hook" || mode == "cancel" {
				req.ToolName = "AskUserQuestion"
				req.Input = json.RawMessage(`{"questions":[{"header":"Platform","question":"Choose platforms","multiSelect":true,"options":[{"label":"macOS"},{"label":"Windows"}]}]}`)
			}
			if mode == "hook" {
				input, _ := json.Marshal(map[string]any{"tool_name": req.ToolName, "tool_input": req.Input})
				req.Subtype, req.CallbackID, req.Input = "hook_callback", "ask-user", input
			}
			if mode == "unknown" {
				req.Subtype = "unknown"
			}
			resolved := 0
			value, err := claudeInteraction(context.Background(), func(e AgentEvent) {
				if e.Resolved != nil {
					resolved++
				}
				if e.Interaction == nil {
					return
				}
				if req.ToolName == "AskUserQuestion" && (!e.Interaction.Request.Questions[0].MultiSelect || !e.Interaction.Request.Questions[0].IsOther) {
					t.Fatal("lost multi/custom question")
				}
				answer := map[string]any{"decision": "cancel"}
				if mode == "allow" {
					answer["decision"] = "accept"
				}
				if mode == "ask" || mode == "hook" {
					answer["answers"] = map[string]any{"0": map[string]any{"answers": []string{"macOS", "Windows", "Other"}}}
				}
				e.Interaction.Reply <- answer
			}, req)
			if mode == "unknown" {
				if err == nil {
					t.Fatal("unknown request accepted")
				}
				return
			}
			if err != nil || resolved != 1 {
				t.Fatal(value, err, resolved)
			}
			raw, _ := json.Marshal(value)
			if (mode == "ask" || mode == "hook") && !strings.Contains(string(raw), "macOS, Windows, Other") {
				t.Fatal(string(raw))
			}
			if (mode == "deny" || mode == "cancel") && !strings.Contains(string(raw), `"deny"`) {
				t.Fatal(string(raw))
			}
		})
	}
}

func TestClaudeParallelBlocksAndTasks(t *testing.T) {
	var events []AgentEvent
	p := claudeParser{emit: func(e AgentEvent) { events = append(events, e) }}
	for _, raw := range []string{
		`{"type":"stream_event","event":{"type":"message_start","message":{"id":"main"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"read-main","name":"Read","input":{}}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":"}}}`,
		`{"type":"stream_event","parent_tool_use_id":"agent","event":{"type":"message_start","message":{"id":"child"}}}`,
		`{"type":"stream_event","parent_tool_use_id":"agent","event":{"type":"content_block_start","index":0,"content_block":{"type":"tool_use","id":"read-child","name":"Read","input":{}}}}`,
		`{"type":"stream_event","parent_tool_use_id":"agent","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"{\"file_path\":\"child.go\"}"}}}`,
		`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"input_json_delta","partial_json":"\"main.go\"}"}}}`,
		`{"type":"stream_event","parent_tool_use_id":"agent","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"stream_event","event":{"type":"content_block_stop","index":0}}`,
		`{"type":"assistant","message":{"id":"task","content":[{"type":"tool_use","id":"create","name":"TaskCreate","input":{"subject":"Verify","description":"Run checks"}}]}}`,
		`{"type":"user","tool_use_result":{"task":{"id":"1"}},"message":{"content":[{"type":"tool_result","tool_use_id":"create","content":"Task #1 created"}]}}`,
		`{"type":"assistant","message":{"id":"update","content":[{"type":"tool_use","id":"update","name":"TaskUpdate","input":{"taskId":"1","status":"in_progress"}}]}}`,
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"update","content":"Updated task"}]}}`,
	} {
		if err := p.line([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	found := map[string]AgentEvent{}
	for _, e := range events {
		found[e.ID] = e
	}
	if string(found["read-main"].Tool.Arguments) != `{"file_path":"main.go"}` || string(found["read-child"].Tool.Arguments) != `{"file_path":"child.go"}` || found["read-child"].Tool.ParentID != "agent" {
		t.Fatal("parallel input blocks mixed", found)
	}
	if plan := string(found["claude-tasks"].Tool.Arguments); !strings.Contains(plan, `"content":"Verify"`) || !strings.Contains(plan, `"status":"in_progress"`) {
		t.Fatal("native task plan lost", plan)
	}
}

func TestClaudeCompactionHookOrdering(t *testing.T) {
	for _, postFirst := range []bool{false, true} {
		t.Run(fmt.Sprint(postFirst), func(t *testing.T) {
			var events []AgentEvent
			p := claudeParser{emit: func(e AgentEvent) { events = append(events, e) }}
			for n := 0; n < 2; n++ {
				p.compactHook(claudeRequest{CallbackID: "compact-start"})
				boundary := func() {
					if err := p.line([]byte(`{"type":"system","subtype":"compact_boundary","compact_metadata":{"pre_tokens":1000}}`)); err != nil {
						t.Fatal(err)
					}
				}
				post := func() {
					p.compactHook(claudeRequest{CallbackID: "compact-end", Input: json.RawMessage(`{"compact_summary":"Keep the task"}`)})
				}
				if postFirst {
					post()
					boundary()
				} else {
					boundary()
					post()
				}
			}
			starts, summaries := 0, 0
			for _, e := range events {
				if e.Status == "inProgress" {
					starts++
				}
				if e.Text == "Keep the task" {
					summaries++
				}
			}
			if p.compactions != 2 || p.compacting || starts != 2 || summaries != 2 {
				t.Fatal("compaction duplicated or summary lost", events)
			}
		})
	}
}

func TestClaudeBackgroundSettlement(t *testing.T) {
	p := claudeParser{emit: func(AgentEvent) {}}
	for _, raw := range []string{
		`{"type":"system","subtype":"session_state_changed","state":"running"}`,
		`{"type":"system","subtype":"task_started","task_id":"child","task_type":"local_agent"}`,
		`{"type":"result","subtype":"success"}`,
		`{"type":"stream_event","parent_tool_use_id":"agent","event":{"type":"message_start","message":{"id":"child"}}}`,
		`{"type":"assistant","parent_tool_use_id":"agent","message":{"id":"child","content":[{"type":"text","text":"Subagent complete"}]}}`,
		`{"type":"system","subtype":"task_notification","task_id":"child"}`,
	} {
		if err := p.line([]byte(raw)); err != nil {
			t.Fatal(err)
		}
		if p.done() {
			t.Fatal("settled before session idle")
		}
	}
	if err := p.line([]byte(`{"type":"system","subtype":"session_state_changed","state":"idle"}`)); err != nil || !p.done() {
		t.Fatal("subagent output discarded parent completion", err)
	}
	if err := p.line([]byte(`{"type":"assistant","message":{"id":"follow-up","content":[{"type":"text","text":"Following up"}]}}`)); err != nil || p.done() {
		t.Fatal("new parent turn reused an old result", err)
	}
}

func TestClaudeUnsupportedImage(t *testing.T) {
	path := filepath.Join(t.TempDir(), "image.bmp")
	if err := os.WriteFile(path, append([]byte("BM"), make([]byte, 64)...), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := imagePrompt(RunOptions{Driver: "claude", Images: []string{path}}); err == nil || !strings.Contains(err.Error(), "image/bmp") {
		t.Fatal("unsupported image accepted", err)
	}
	c := Control{driver: "claude"}
	if err := c.Steer(context.Background(), "image", []string{path}); err == nil || !strings.Contains(err.Error(), "image/bmp") {
		t.Fatal("steer did not validate image type", err)
	}
}

func TestClaudeInstallAndArguments(t *testing.T) {
	for _, access := range []string{"", "acceptEdits", "bypassPermissions"} {
		args, err := agentArgs(RunOptions{Driver: "claude", Access: access, Session: "native-session", Model: "exact-model", Effort: "high"})
		if err != nil || !strings.Contains(strings.Join(args, " "), "--resume native-session --model exact-model --effort high") {
			t.Fatal(args, err)
		}
		if slices.Contains(args, "--allow-dangerously-skip-permissions") != (access == "bypassPermissions") {
			t.Fatal("permissions amplified", args)
		}
	}
	if _, err := agentArgs(RunOptions{Driver: "claude", Access: "high"}); err == nil {
		t.Fatal("unknown access accepted")
	}
	if _, err := agentArgs(RunOptions{Driver: "claude", Effort: "off"}); err == nil {
		t.Fatal("invented effort accepted")
	}
	if version, err := installationVersion("claude", []byte("2.1.317 (Claude Code)\n")); err != nil || version != "2.1.317" {
		t.Fatal(version, err)
	}
	for _, platform := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			target, exe, err := claudePlatform(platform, arch)
			if err != nil || target == "" || platform == "windows" && exe != "claude.exe" {
				t.Fatal(target, exe, err)
			}
		}
	}
	i := Installation{Driver: "claude", Path: "/local/claude", Method: "native"}
	cmd, args, err := managerAdapters["native"].update(i, "2.1.319")
	if err != nil || cmd != i.Path || strings.Join(args, " ") != "install 2.1.319" {
		t.Fatal(cmd, args, err)
	}
	for _, entry := range []string{"cli.js", "bin/claude.exe"} {
		method, _ := classifyDriver("claude", filepath.FromSlash("/usr/local/lib/node_modules/@anthropic-ai/claude-code/"+entry), t.TempDir())
		if method != "npm" {
			t.Fatal("npm source lost", entry, method)
		}
	}
	if slices.Contains(installerScriptArgs("claude", []string{"install", "--ignore-scripts"}), "--ignore-scripts") {
		t.Fatal("native postinstall blocked")
	}
	brewPath := filepath.FromSlash("/opt/homebrew/Caskroom/claude-code@latest/2.1.319/claude")
	if method, _ := classifyDriver("claude", brewPath, t.TempDir()); method != "brew" {
		t.Fatal("latest cask ownership lost", method)
	}
	_, args, err = managerAdapters["brew"].update(Installation{Driver: "claude", Method: "brew", Path: brewPath, Prefix: "/opt/homebrew"}, "2.1.319")
	if err != nil || !slices.Contains(args, "claude-code@latest") {
		t.Fatal("latest cask replaced", args, err)
	}
	for _, method := range []string{"bun", "pnpm"} {
		if _, _, err := globalPackageUpdate(Installation{Driver: "claude", Method: method, Manager: method, PackageRoot: "/packages"}, "2.1.319"); err == nil {
			t.Fatal("build trust policy silently changed", method)
		}
	}
}

func TestInstalledClaudeCatalog(t *testing.T) {
	if os.Getenv("MUJICAGENT_CLAUDE_PROBE_TEST") != "1" {
		t.Skip("explicit read-only installed Claude Code probe")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	models, err := DiscoverModels(ctx, "claude", "claude", t.TempDir())
	if err != nil || len(models) == 0 {
		t.Fatal(models, err)
	}
	t.Logf("Claude Code: %d models; no inference requested", len(models))
	for _, m := range models {
		if m.ID == "default" {
			t.Fatal("default-model placeholder leaked")
		}
	}
}

func TestNativeInstallerOwnership(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "bin", "pi")
	if err := os.MkdirAll(filepath.Join(root, "install"), 0700); err != nil {
		t.Fatal(err)
	}
	marker, _ := json.Marshal(map[string]any{"kind": "pi-managed-install", "layout": "releases-v1", "schemaVersion": 1, "entrypoint": map[string]string{"path": path}})
	if err := os.WriteFile(filepath.Join(root, "install", "managed-install.json"), marker, 0600); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ driver, path, method string }{
		{"pi", path, "native"},
		{"pi", filepath.Join(root, "bin", "other-pi"), "custom"},
		{"droid", filepath.FromSlash("/Applications/Factory.app/Contents/Resources/bin/droid"), "Factory.app"},
		{"pi", filepath.FromSlash("/nix/store/hash-pi/bin/pi"), "nix"},
	} {
		i := inspectNative(context.Background(), Installation{Driver: tc.driver, Path: tc.path, Method: "custom"}, tc.path)
		if i.Method != tc.method || i.CanUpdate() != (tc.method == "native") {
			t.Fatalf("ownership: %+v", i)
		}
	}
	for _, driver := range []string{"pi", "droid"} {
		command, args, err := managerAdapters["native"].update(Installation{Driver: driver, Path: path}, "1.6.0")
		want := []string{"update", "--self", "--no-approve"}
		if driver == "droid" {
			want = []string{"update", "--version", "1.6.0"}
		}
		if err != nil || command != path || !slices.Equal(args, want) {
			t.Fatalf("native updater %s: %s %v %v", driver, command, args, err)
		}
	}
}

func TestPiZipExtraction(t *testing.T) {
	for _, name := range []string{"pi.exe", "../escape", "/absolute", `pi\..\escape`, "C:/escape", "link"} {
		t.Run(name, func(t *testing.T) {
			var archive bytes.Buffer
			z := zip.NewWriter(&archive)
			header := &zip.FileHeader{Name: name, Method: zip.Deflate}
			header.SetMode(0700)
			if name == "link" {
				header.SetMode(os.ModeSymlink | 0700)
			}
			w, err := z.CreateHeader(header)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := w.Write([]byte("fixture")); err != nil {
				t.Fatal(err)
			}
			if err := z.Close(); err != nil {
				t.Fatal(err)
			}
			root := t.TempDir()
			err = extractZip(bytes.NewReader(archive.Bytes()), int64(archive.Len()), root)
			if (err == nil) != (name == "pi.exe") {
				t.Fatalf("unsafe ZIP acceptance: %v", err)
			}
			if err == nil {
				data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
				if err != nil || string(data) != "fixture" {
					t.Fatal("ZIP executable missing", err)
				}
			}
		})
	}
}

func TestInstallSourcePolicy(t *testing.T) {
	for _, d := range Drivers {
		a, ok := installerAdapters[d.ID]
		if !ok || a.latest == nil || a.packageName == "" || len(a.helpArgs) == 0 || len(a.sources) == 0 {
			t.Fatalf("%s is missing an installation contract", d.ID)
		}
	}
	root := t.TempDir()
	release := Release{Version: "0.168.0", URL: "https://example.test/codex.tar.gz", Digest: "sha256:" + strings.Repeat("0", 64), Entry: "bin/codex"}
	for _, source := range []string{"", "native-mirror"} {
		current := Installation{Driver: "codex", Path: filepath.Join(root, "old", "bin", "codex"), Method: "managed", Source: source}
		p, err := PrepareInstall(context.Background(), "codex", root, "cn", release, current)
		if err != nil || p.Source != source || p.Command != "" || p.Stage == "" {
			t.Fatalf("changed managed source: %+v %v", p, err)
		}
		if _, err := os.Stat(p.Stage); !os.IsNotExist(err) {
			t.Fatal("preview created installation")
		}
		// A failed download must leave the old path and remove only its new stage.
		p.Release.Digest = "missing"
		if _, err := p.Execute(context.Background()); err == nil {
			t.Fatal("accepted missing checksum")
		}
		if _, err := os.Stat(p.Stage); !os.IsNotExist(err) {
			t.Fatal("failed stage retained")
		}
	}
	if _, err := PrepareInstall(context.Background(), "codex", root, "", release, Installation{Path: "/custom/codex", Method: "custom"}); err == nil {
		t.Fatal("unknown install silently migrated")
	}
	if _, err := PrepareInstall(context.Background(), "codex", root, "bad", release, Installation{}); err == nil {
		t.Fatal("accepted unknown source")
	}
}

func TestManagedSourceInspection(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX executable fixture")
	}
	root := t.TempDir()
	stage := filepath.Join(root, "release-old")
	bin := filepath.Join(stage, "bin")
	if err := os.MkdirAll(bin, 0700); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(bin, "codex")
	if err := os.WriteFile(path, []byte("#!/bin/sh\nprintf 'codex-cli 0.167.0\\n'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	for _, source := range []string{"", "cn", "native-mirror"} {
		data, _ := json.Marshal(source)
		if err := os.WriteFile(filepath.Join(stage, "source.json"), data, 0600); err != nil {
			t.Fatal(err)
		}
		current, err := Inspect(context.Background(), "codex", path, root)
		if err != nil || current.Method != "managed" || current.Source != source {
			t.Fatal(current, err)
		}
	}
}

func TestManagedNpmExecutionPreservesSource(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX npm fixture")
	}
	root := t.TempDir()
	manager := filepath.Join(root, "npm")
	stage := filepath.Join(root, "managed", "release-next")
	script := "#!/bin/sh\nmkdir -p \"$2/node_modules/.bin\"\nprintf '#!/bin/sh\\nif [ \"$1\" = --version ]; then echo codex-cli 0.168.0; fi\\n' > \"$2/node_modules/.bin/codex\"\nchmod 700 \"$2/node_modules/.bin/codex\"\n"
	if err := os.WriteFile(manager, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	p := &InstallPlan{Release: Release{Version: "0.168.0"}, Source: "cn", Stage: stage,
		Path: filepath.Join(stage, "node_modules", ".bin", "codex"), Command: manager, Args: []string{"--prefix", stage}}
	path, err := p.Execute(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	current, err := Inspect(context.Background(), "codex", path, filepath.Dir(stage))
	if err != nil || current.Method != "managed" || current.Source != "cn" {
		t.Fatal(current, err)
	}
	if _, err := p.Execute(context.Background()); err == nil {
		t.Fatal("reused an existing stage")
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal("removed the existing installation", err)
	}
}

func TestWindowsNpmRuntimeOutsideGlobalPrefix(t *testing.T) {
	root := filepath.Join(t.TempDir(), "工具 Program Files")
	prefix := filepath.Join(t.TempDir(), "AppData", "npm")
	cli := filepath.Join(root, "node_modules", "npm", "bin", "npm-cli.js")
	if err := os.MkdirAll(filepath.Dir(cli), 0700); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{cli, filepath.Join(root, "npm.cmd"), filepath.Join(root, "node.exe")} {
		if err := os.WriteFile(path, nil, 0700); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", root)
	node, script, err := windowsNpmRuntime(prefix)
	if err != nil || node != filepath.Join(root, "node.exe") || script != cli {
		t.Fatal(node, script, err)
	}
	if err := os.Remove(cli); err != nil {
		t.Fatal(err)
	}
	if _, _, err := windowsNpmRuntime(prefix); err == nil {
		t.Fatal("accepted missing npm runtime")
	}
}

func TestInstalledHarnessSource(t *testing.T) {
	driver := os.Getenv("MUJICAGENT_HARNESS_INSPECT_TEST")
	if driver == "" {
		t.Skip("explicit read-only local inspection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	current, err := Inspect(ctx, driver, driver, t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("%s %s: source=%s, update=%v", driver, current.Version, current.Method, current.CanUpdate())
	if current.CanUpdate() {
		p, err := PrepareInstall(ctx, driver, t.TempDir(), UpdateSource(current), Release{Version: current.Version}, current)
		if err != nil {
			t.Fatal(err)
		}
		t.Log(p.CommandPreview())
	}
}

func TestInstalledCodexManager(t *testing.T) {
	if os.Getenv("MUJICAGENT_CODEX_INSPECT_TEST") != "1" {
		t.Skip("explicit read-only local inspection")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	current, err := Inspect(ctx, "codex", "codex", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if current.Method != "vp" {
		t.Fatalf("expected local VP installation: %+v", current)
	}
	plan, err := PrepareInstall(ctx, "codex", t.TempDir(), "", Release{Version: current.Version}, current)
	if err != nil {
		t.Fatal(err)
	}
	t.Log(plan.CommandPreview())
}

func TestReleaseAndInstallationRouting(t *testing.T) {
	for _, platform := range []string{"darwin", "linux", "windows"} {
		for _, arch := range []string{"amd64", "arm64"} {
			target, exe, err := Target(platform, arch)
			if err != nil || target == "" || (platform == "windows" && exe != "codex.exe") {
				t.Fatal(target, exe, err)
			}
		}
	}
	if _, _, err := Target("windows", "386"); err == nil {
		t.Fatal("accepted unsupported platform")
	}
	for _, tc := range []struct {
		a, b string
		want bool
	}{{"0.167.0", "0.99.0", true}, {"1.0.0", "1.0.0", false}, {"0.99.0", "0.167.0", false}, {"broken", "1.0.0", false}} {
		if Newer(tc.a, tc.b) != tc.want {
			t.Fatal(tc)
		}
	}
	root := filepath.Join(t.TempDir(), "tools")
	for _, tc := range []struct{ path, want string }{{filepath.Join(root, "release-1", "bin", "codex"), "managed"}, {filepath.Join(root+"-other", "codex"), "custom"}, {"/opt/homebrew/Caskroom/codex/1/codex", "brew"}, {"/usr/local/lib/node_modules/@openai/codex/bin/codex.js", "npm"}} {
		method, _ := classify(tc.path, root)
		if method != tc.want {
			t.Fatalf("%s: %s", tc.path, method)
		}
	}
}

func TestExtractPreservesCompanionsAndRejectsUnsafePaths(t *testing.T) {
	for _, name := range []string{"bin/codex", "bin/codex-code-mode-host", "../escape", "/absolute", `C:\escape`, `..\escape`} {
		var buf bytes.Buffer
		gz := gzip.NewWriter(&buf)
		tw := tar.NewWriter(gz)
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0755, Size: 4})
		tw.Write([]byte("data"))
		tw.Close()
		gz.Close()
		root := t.TempDir()
		err := extract(&buf, root)
		safe := name == "bin/codex" || name == "bin/codex-code-mode-host"
		if safe {
			if err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(root, filepath.FromSlash(name))); err != nil {
				t.Fatal(err)
			}
		} else if err == nil {
			t.Fatal("accepted", name)
		}
	}
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	tw.WriteHeader(&tar.Header{Name: "link", Typeflag: tar.TypeSymlink, Linkname: "/tmp"})
	tw.Close()
	gz.Close()
	if err := extract(&buf, t.TempDir()); err == nil {
		t.Fatal("accepted symlink")
	}
}

// Downloads only to a temporary directory; never changes the existing installation.
func TestDownloadNativePackage(t *testing.T) {
	driver := os.Getenv("MUJICAGENT_INSTALL_DRIVER")
	if driver == "" {
		driver = "codex"
	}
	source := os.Getenv("MUJICAGENT_INSTALL_TEST")
	if source == "" {
		t.Skip("explicit download test only")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	lookupSource := source
	if source == "cn" && (driver == "droid" || driver == "claude") {
		lookupSource = "native-mirror"
	}
	if source == "cn-npm" {
		lookupSource = "cn"
	}
	release, err := Latest(ctx, driver, lookupSource)
	if err != nil {
		t.Fatal(err)
	}
	installSource := source
	if source == "cn-npm" {
		installSource = "cn"
	}
	if source == "cn" {
		installSource = "native-mirror"
	}
	path, err := Install(ctx, driver, t.TempDir(), installSource, release, Installation{})
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("verified %s at %s", release.Version, path)
}

func TestAgentProtocol(t *testing.T) {
	for _, tc := range []struct {
		driver, line, want string
		fail               bool
	}{
		{"pi", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"你好"}}`, "你好", false},
		{"pi", `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"done"}]}}`, "done", false},
		{"pi", `{"type":"message_end","message":{"role":"assistant","stopReason":"error","errorMessage":"bad model"}}`, "", true},
		{"pi", `broken`, "", true},
		{"pi", `{"type":"tool_execution_end","result":{"content":[{"type":"text","text":"file"}]}}`, "", false},
	} {
		p := piParser{driver: tc.driver}
		text := ""
		err := p.line([]byte(tc.line), func(e AgentEvent) { text = e.Text })
		if (err != nil) != tc.fail || text != tc.want {
			t.Fatalf("%s: %q %v", tc.line, text, err)
		}
	}
	for _, driver := range []string{"pi", "droid"} {
		args, err := agentArgs(RunOptions{Driver: driver, Session: "owned-session", Model: "custom-model"})
		if err != nil {
			t.Fatal(err)
		}
		found := false
		for _, arg := range args {
			if arg == "custom-model" {
				found = true
			}
			if arg == "--skip-permissions-unsafe" {
				t.Fatal("unsafe default")
			}
		}
		if !found {
			t.Fatal("model omitted")
		}
	}
}

func TestNativeToolContracts(t *testing.T) {
	cases := []struct{ pi, droid, kind, input string }{
		{"bash", "Execute", "execute", `{"command":"pwd"}`},
		{"edit", "Edit", "edit", `{"path":"a.go","oldText":"old","newText":"new"}`},
		{"write", "Create", "edit", `{"file_path":"a.go","content":"new"}`},
		{"read", "Read", "read", `{"file_path":"a.go"}`},
		{"todo", "TodoWrite", "plan", `{"todos":"1. [in_progress] Verify"}`},
		{"ask_user", "AskUser", "ask", `{"questionnaire":"Choose a platform"}`},
		{"grep", "Grep", "search", `{"pattern":"TODO"}`},
		{"find", "Glob", "search", `{"patterns":["**/*.go"]}`},
		{"fetchurl", "FetchUrl", "fetch", `{"url":"https://example.com"}`},
		{"compact", "Compact", "compaction", `{"summary":"Retain the task"}`},
	}
	for _, tc := range cases {
		for _, driver := range []string{"pi", "droid"} {
			t.Run(driver+"/"+tc.kind+"/"+tc.pi, func(t *testing.T) {
				var event AgentEvent
				emit := func(e AgentEvent) { event = e }
				var err error
				if driver == "pi" {
					raw, _ := json.Marshal(map[string]any{"type": "tool_execution_start", "toolCallId": "call", "toolName": tc.pi, "args": json.RawMessage(tc.input)})
					p := piParser{driver: driver}
					err = p.line(raw, emit)
				} else {
					raw, _ := json.Marshal(map[string]any{"type": "tool_call", "toolUse": map[string]any{"id": "call", "name": tc.droid, "input": json.RawMessage(tc.input)}})
					a := droidRPC{emit: emit}
					err = a.update(raw)
				}
				if err != nil || event.ID != "call" || event.Tool == nil || event.Tool.Kind != tc.kind || string(event.Tool.Arguments) != tc.input {
					t.Fatalf("native tool contract: %+v %v", event, err)
				}
			})
		}
	}
	a := droidRPC{}
	var events []AgentEvent
	a.emit = func(e AgentEvent) { events = append(events, e) }
	for _, raw := range []string{
		`{"type":"tool_call","toolUse":{"id":"script","name":"Script","input":{"script":"text(await tools.Read({file_path:'/workspace/a.go'}));"}}}`,
		`{"type":"tool_call","toolUse":{"id":"read","name":"Read","input":{"file_path":"/workspace/a.go"},"scriptExecution":{"runId":"run","outerToolUseId":"script"}}}`,
		`{"type":"tool_result","toolUseId":"read","content":[{"type":"text","text":"package main"}]}`,
		`{"type":"tool_progress_update","toolUseId":"script","toolName":"Script","update":{"type":"message","text":"first"}}`,
		`{"type":"tool_progress_update","toolUseId":"script","toolName":"Script","update":{"type":"message","text":"second"}}`,
		`{"type":"tool_progress_update","toolUseId":"script","toolName":"Script","update":{"type":"message","fullOutput":"replacement"}}`,
		`{"type":"tool_progress_update","toolUseId":"script","toolName":"Script","update":{"type":"error","error":"Execution failed"}}`,
		`{"type":"tool_result","toolUseId":"script","content":"Execution failed","isError":true}`,
	} {
		if err := a.update([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 8 || events[0].Tool.Kind != "execute" || events[1].Tool.ParentID != "script" || string(events[4].Tool.Result) != `"firstsecond"` || string(events[5].Tool.Result) != `"replacement"` || events[6].Status != "failed" || events[7].Status != "failed" {
		t.Fatalf("Script children/progress/error contract: %+v", events)
	}
}

func TestCompactionLifecycle(t *testing.T) {
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			var events []AgentEvent
			emit := func(e AgentEvent) { events = append(events, e) }
			p := piParser{driver: driver}
			a := droidRPC{emit: emit}
			for range 2 {
				for _, end := range []bool{false, true} {
					var err error
					if driver == "pi" {
						kind := "auto_compaction_start"
						if end {
							kind = "auto_compaction_end"
						}
						err = p.line([]byte(`{"type":"`+kind+`"}`), emit)
					} else if !end {
						err = a.update([]byte(`{"type":"droid_working_state_changed","newState":"compacting_conversation"}`))
					} else {
						err = a.update([]byte(`{"type":"session_compacted"}`))
					}
					if err != nil {
						t.Fatal(err)
					}
				}
			}
			if len(events) != 4 || events[0].ID != events[1].ID || events[2].ID != events[3].ID || events[0].ID == events[2].ID || events[0].Kind != "contextCompaction" || events[0].Status != "inProgress" || events[1].Status != "completed" {
				t.Fatalf("repeated compaction overwrote history: %+v", events)
			}
			if driver == "pi" {
				for _, tc := range []struct{ raw, want string }{
					{`{"type":"compaction_end","aborted":true}`, "interrupted"},
					{`{"type":"auto_compaction_end","errorMessage":"failed"}`, "failed"},
				} {
					if err := p.line([]byte(tc.raw), emit); err != nil || events[len(events)-1].Status != tc.want {
						t.Fatal("compaction failure/cancel lost", err)
					}
				}
			}
		})
	}
}

func TestDroidScriptMessageTools(t *testing.T) {
	var events []AgentEvent
	a := droidRPC{streaming: true, session: "s", emit: func(e AgentEvent) { events = append(events, e) }}
	consume := func(notification string) {
		t.Helper()
		if err := a.consume([]byte(`{"method":"droid.session_notification","params":{"sessionId":"s","notification":` + notification + `}}`)); err != nil {
			t.Fatal(err)
		}
	}
	// The actual 1.6.0 Script path publishes children in create_message.
	// Include text and thinking to ensure their deltas are not duplicated.
	consume(`{"type":"tool_call","toolUse":{"id":"script","name":"Script","input":{"script":"const r = await Promise.allSettled([tools.Read({file_path:'a.go'}), tools.Read({file_path:'b.go'})]); text(r);"}}}`)
	cases := []struct{ name, kind, input string }{
		{"Read", "read", `{"file_path":"a.go"}`},
		{"Read", "read", `{"file_path":"b.go"}`},
		{"Execute", "execute", `{"command":"pwd"}`},
		{"Edit", "edit", `{"file_path":"a.go","old_str":"old","new_str":"new"}`},
		{"Create", "edit", `{"file_path":"new.go","content":"package main"}`},
		{"ApplyPatch", "edit", `{"input":"*** Begin Patch\n*** End Patch"}`},
		{"TodoWrite", "plan", `{"todos":"1. [in_progress] Verify"}`},
		{"AskUser", "ask", `{"questionnaire":"Choose a platform"}`},
		{"Compact", "compaction", `{}`},
		{"Skill", "other", `{"skill":"review"}`},
		{"ToolSearch", "other", `{"query":"docs"}`},
		{"Task", "other", `{"description":"Inspect","prompt":"Read code"}`},
		{"WaitForScript", "execute", `{"toolCallId":"script","action":"status"}`},
		{"custom.namespace.lookup", "other", `{"query":"proof"}`},
	}
	for i, tc := range cases {
		id := fmt.Sprintf("child-%d", i)
		consume(fmt.Sprintf(`{"type":"create_message","message":{"role":"assistant","visibility":"user_only","content":[{"type":"text","text":"already streamed"},{"type":"thinking","thinking":"private"},{"type":"tool_use","id":%q,"name":%q,"input":%s,"scriptExecution":{"runId":"run","outerToolUseId":"script","seen":%d}}]}}`, id, tc.name, tc.input, i))
		start := events[len(events)-1]
		if start.ID != id || start.Tool == nil || start.Tool.ParentID != "script" || start.Tool.Name != tc.name || start.Tool.Kind != tc.kind || string(start.Tool.Arguments) != tc.input {
			t.Fatalf("missing actual Script child: %+v", start)
		}
		consume(fmt.Sprintf(`{"type":"tool_result","toolUseId":%q,"content":[{"type":"text","text":"done"}],"isError":%t}`, id, i == 1))
		if events[len(events)-1].Status != map[bool]string{true: "failed", false: "completed"}[i == 1] {
			t.Fatal("child failure lost")
		}
	}
	if len(events) != 1+len(cases)*2 {
		t.Fatalf("message text duplicated or children invented: %d", len(events))
	}
	before := len(events)
	for _, metadata := range []string{`"visibility":"llm_only"`, `"hiddenFromUserViews":true`} {
		consume(`{"type":"create_message","message":{"role":"assistant",` + metadata + `,"content":[{"type":"tool_use","id":"hidden","name":"Read","input":{}}]}}`)
	}
	a.streaming = false
	consume(`{"type":"create_message","message":{"role":"assistant","content":[{"type":"tool_use","id":"history","name":"Read","input":{}}]}}`)
	a.streaming = true
	if len(events) != before {
		t.Fatal("private or restored history leaked into active turn")
	}
	consume(`{"type":"tool_execution_phase_changed","toolUseId":"late","toolName":"Execute","phase":"executing"}`)
	consume(`{"type":"tool_progress_update","toolUseId":"late","toolName":"Execute","update":{"type":"message","parameters":{"command":"pwd"},"text":"output"}}`)
	if e := events[len(events)-1]; e.Tool.Kind != "execute" || string(e.Tool.Arguments) != `{"command":"pwd"}` {
		t.Fatal("sparse tool metadata lost", e)
	}
	consume(`{"type":"create_message","message":{"role":"tool","content":[{"type":"tool_result","toolUseId":"late","isError":true,"content":{"error":"failed"}}]}}`)
	if e := events[len(events)-1]; e.ID != "late" || e.Status != "failed" || string(e.Tool.Result) != `{"error":"failed"}` {
		t.Fatal("message result lost", e)
	}
}

func TestDroidCompactionTerminalEvents(t *testing.T) {
	for _, tc := range []struct {
		name, end, status string
		wantError         bool
	}{
		{"summary", `{"type":"session_compacted","summaryId":"summary-1","removedCount":5,"visibleBoundaryMessageId":null}`, "completed", false},
		{"no-summary", `{"type":"droid_working_state_changed","newState":"streaming_assistant_message"}`, "completed", false},
		{"error", `{"type":"error","message":"Compaction failed"}`, "failed", true},
		{"cancelled", `{"type":"agent_turn_completed","reason":"cancelled"}`, "interrupted", true},
		{"failed-turn", `{"type":"agent_turn_completed","reason":"error"}`, "failed", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var events []AgentEvent
			a := droidRPC{emit: func(e AgentEvent) { events = append(events, e) }}
			for range 2 {
				if err := a.update([]byte(`{"type":"droid_working_state_changed","newState":"compacting_conversation"}`)); err != nil {
					t.Fatal(err)
				}
			}
			err := a.update([]byte(tc.end))
			if (err != nil) != tc.wantError || len(events) != 3 || events[0].ID != events[2].ID || events[2].Status != tc.status || a.compacting {
				t.Fatalf("Compact lifecycle: %+v, %v", events, err)
			}
			a.finishCompaction("failed")
			if len(events) != 3 {
				t.Fatal("terminal compaction emitted twice")
			}
			if tc.name == "summary" {
				_ = a.update([]byte(tc.end))
				if len(events) != 3 {
					t.Fatal("duplicate summary created a new compaction")
				}
			}
			_ = a.update([]byte(`{"type":"droid_working_state_changed","newState":"compacting_conversation"}`))
			a.finishCompaction("interrupted")
			if len(events) != 5 || events[3].ID == events[0].ID || events[4].Status != "interrupted" {
				t.Fatal("later compaction overwrote history", events)
			}
		})
	}
}

func TestPiToolAndMessageBoundaries(t *testing.T) {
	p := piParser{driver: "pi"}
	var events []AgentEvent
	for _, line := range []string{
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_update","assistantMessageEvent":{"type":"text_delta","contentIndex":0,"delta":"Before"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"Before"}]}}`,
		`{"type":"tool_execution_start","toolCallId":"read-1","toolName":"read","args":{"path":"proof.txt"}}`,
		`{"type":"tool_execution_update","toolCallId":"read-1","toolName":"read","partialResult":{"content":[{"type":"text","text":"partial"}]}}`,
		`{"type":"tool_execution_end","toolCallId":"read-1","toolName":"read","result":{"content":[{"type":"text","text":"proof"}]},"isError":false}`,
		`{"type":"message_start","message":{"role":"assistant"}}`,
		`{"type":"message_end","message":{"role":"assistant","content":[{"type":"thinking","thinking":"summary"},{"type":"text","text":"After"}]}}`,
	} {
		if err := p.line([]byte(line), func(e AgentEvent) { events = append(events, e) }); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 7 || events[0].ID != events[1].ID || events[6].ID == events[0].ID || events[5].Kind != "reasoning" {
		t.Fatalf("message boundaries: %+v", events)
	}
	for _, e := range events[2:5] {
		if e.ID != "read-1" || e.Tool == nil {
			t.Fatal("tool event dropped")
		}
	}
	if events[4].Status != "completed" || !strings.Contains(string(events[4].Tool.Result), "proof") {
		t.Fatal("result lost")
	}
}

func TestRegisteredAgentContracts(t *testing.T) {
	for _, driver := range Drivers {
		if driver.run == nil || driver.args == nil || !CanSteer(driver.ID) {
			t.Fatalf("%s lacks a complete session adapter", driver.ID)
		}
		args, err := agentArgs(RunOptions{Driver: driver.ID, Session: "session"})
		if err != nil || !driver.Capabilities.PersistentConnection && len(args) == 0 {
			t.Fatalf("%s cannot launch: %v", driver.ID, err)
		}
	}
	a := droidRPC{emit: func(AgentEvent) {}}
	if err := a.update(json.RawMessage(`{"type":"agent_turn_completed","reason":"permission_rejected"}`)); err == nil {
		t.Fatal("rejected turn treated as success")
	}
	if err := a.update(json.RawMessage(`{"type":"agent_turn_completed","reason":"completed"}`)); err != nil || !a.done {
		t.Fatal("completion missing", err)
	}
}

func TestSteerWaitsForAcknowledgment(t *testing.T) {
	for _, driver := range []string{"pi", "droid"} {
		reader, writer := io.Pipe()
		c := &Control{in: json.NewEncoder(writer), driver: driver}
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		done := make(chan error, 1)
		go func() { done <- c.Steer(ctx, "next", nil) }()
		var request map[string]any
		if err := json.NewDecoder(reader).Decode(&request); err != nil {
			t.Fatal(err)
		}
		select {
		case <-done:
			t.Fatal("write treated as acceptance")
		default:
		}
		c.response(request["id"].(string), nil)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		c.close()
		if err := c.Steer(ctx, "late", nil); err == nil {
			t.Fatal("stopped run accepted steer")
		}
		cancel()
		reader.Close()
		writer.Close()
	}
}

func TestDroidRPCNotifications(t *testing.T) {
	for _, streaming := range []bool{false, true} {
		var events []AgentEvent
		a := droidRPC{ctx: context.Background(), session: "s", streaming: streaming, emit: func(e AgentEvent) { events = append(events, e) }}
		for _, line := range []string{
			`{"method":"droid.session_notification","params":{"sessionId":"other","notification":{"type":"assistant_text_delta","messageId":"x","textDelta":"wrong"}}}`,
			`{"method":"droid.session_notification","params":{"sessionId":"s","notification":{"type":"tool_call","toolUse":{"id":"read-1","name":"Read","input":{"path":"proof.txt"}}}}}`,
			`{"method":"droid.session_notification","params":{"sessionId":"s","notification":{"type":"tool_result","toolUseId":"read-1","content":[{"type":"text","text":"proof"}]}}}`,
			`{"method":"droid.session_notification","params":{"sessionId":"s","notification":{"type":"assistant_text_delta","messageId":"m","blockIndex":0,"textDelta":"answer"}}}`,
		} {
			if err := a.consume([]byte(line)); err != nil {
				t.Fatal(err)
			}
		}
		if !streaming && len(events) != 0 {
			t.Fatal("history replayed")
		}
		if streaming && (len(events) != 3 || events[0].ID != "read-1" || events[1].Status != "completed" || events[2].Text != "answer") {
			t.Fatalf("events: %+v", events)
		}
	}
}

func TestAgentInteractionReplies(t *testing.T) {
	for _, decision := range []string{"accept", "cancel"} {
		a := droidRPC{ctx: context.Background(), emit: func(e AgentEvent) {
			if e.Interaction != nil {
				e.Interaction.Reply <- map[string]any{"decision": decision}
			}
		}}
		result, err := a.interaction(a.ctx, []byte(`{"id":"p","method":"droid.request_permission","params":{"options":[{"value":"proceed_once"},{"value":"cancel"}],"toolUses":[{"toolUse":{"name":"Execute","input":{"command":"pwd"}}}]}}`))
		if err != nil {
			t.Fatal(err)
		}
		want := "cancel"
		if decision == "accept" {
			want = "proceed_once"
		}
		if result.(map[string]string)["selectedOption"] != want {
			t.Fatal(result)
		}
	}
	var output bytes.Buffer
	c := &Control{in: json.NewEncoder(&output)}
	err := piInteraction(context.Background(), []byte(`{"type":"extension_ui_request","id":"q","method":"input","title":"Name?"}`), c, func(e AgentEvent) {
		if e.Interaction != nil {
			e.Interaction.Reply <- map[string]any{"action": "accept", "content": map[string]string{"answer": "Ada"}}
		}
	})
	if err != nil || !strings.Contains(output.String(), `"value":"Ada"`) {
		t.Fatal(output.String(), err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ask(ctx, func(AgentEvent) {}, nil); err == nil {
		t.Fatal("cancelled interaction did not stop")
	}
}

func TestDroidPickerAndMultiSelect(t *testing.T) {
	for _, action := range []string{"accept", "cancel", "refresh"} {
		resolved := false
		a := droidRPC{ctx: context.Background(), emit: func(e AgentEvent) {
			if e.Resolved != nil {
				resolved = true
				return
			}
			if e.Interaction == nil {
				return
			}
			r := e.Interaction.Request
			if r.Method != "client/form" || !r.Refreshable || !r.FilterOptions || r.Search != "beta" || r.InitialValues["selection"] != `["b"]` {
				t.Fatalf("picker defaults: %+v", r)
			}
			form, err := r.ElicitationForm()
			if err != nil {
				t.Fatal(err)
			}
			content, err := form.Content(map[string]string{"selection": `["a","b"]`})
			if err != nil {
				t.Fatal(err)
			}
			e.Interaction.Reply <- map[string]any{"action": action, "content": content}
		}}
		result, err := a.interaction(a.ctx, []byte(`{"id":"picker","method":"droid.pick_options","params":{"title":"Choose channels","multi":true,"initialSearch":"beta","preselectedIds":["b","missing"],"rows":[{"id":"a","label":"Alpha"},{"id":"b","label":"Beta"}]}}`))
		if err != nil || !resolved {
			t.Fatal(result, err, resolved)
		}
		value := result.(map[string]any)
		if action == "accept" && len(value["selectedIds"].([]string)) != 2 {
			t.Fatal(result)
		}
		if action == "cancel" && value["cancelled"] != true {
			t.Fatal(result)
		}
		if action == "refresh" && value["refresh"] != true {
			t.Fatal(result)
		}
	}
	a := droidRPC{ctx: context.Background(), emit: func(e AgentEvent) {
		if e.Interaction == nil {
			return
		}
		if !e.Interaction.Request.Questions[0].MultiSelect {
			t.Fatal("multiSelect lost")
		}
		e.Interaction.Reply <- map[string]any{"answers": map[string]any{"2": map[string]any{"answers": []string{"A", "B"}}}}
	}}
	result, err := a.interaction(a.ctx, []byte(`{"id":"q","method":"droid.ask_user","params":{"questions":[{"index":2,"question":"Choose","options":["A","B"],"multiSelect":true}]}}`))
	data, _ := json.Marshal(result)
	if err != nil || !strings.Contains(string(data), `"answer":"A, B"`) {
		t.Fatal(string(data), err)
	}
}

func TestPiEditorTimeoutAndPlan(t *testing.T) {
	var output bytes.Buffer
	control := &Control{in: json.NewEncoder(&output)}
	resolved := false
	err := piInteraction(context.Background(), []byte(`{"id":"editor","method":"editor","title":"Edit","prefill":"first\nsecond"}`), control, func(e AgentEvent) {
		if e.Resolved != nil {
			resolved = true
		}
		if e.Interaction == nil {
			return
		}
		r := e.Interaction.Request
		if !r.Multiline || r.InitialValues["answer"] != "first\nsecond" {
			t.Fatal(r)
		}
		e.Interaction.Reply <- map[string]any{"action": "accept", "content": map[string]string{"answer": "first\nchanged"}}
	})
	if err != nil || !resolved || !strings.Contains(output.String(), `first\nchanged`) {
		t.Fatal(output.String(), err)
	}
	output.Reset()
	resolved = false
	err = piInteraction(context.Background(), []byte(`{"id":"timeout","method":"select","title":"Choose","options":["A"],"timeout":1}`), control, func(e AgentEvent) {
		if e.Resolved != nil {
			resolved = true
		}
	})
	if err != nil || !resolved || !strings.Contains(output.String(), `"cancelled":true`) {
		t.Fatal(output.String(), err)
	}
	var events []AgentEvent
	for _, raw := range []string{`{"method":"setWidget","widgetKey":"plan-todos","widgetLines":["\u001b[32m☑ Read\u001b[0m","☐ Verify"]}`, `{"method":"setWidget","widgetKey":"plan-todos"}`} {
		if err := piInteraction(context.Background(), []byte(raw), control, func(e AgentEvent) { events = append(events, e) }); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 1 || events[0].Kind != "plan" || events[0].Text != "☑ Read\n☐ Verify" {
		t.Fatal(events)
	}
}

func TestMCPMetadataAndOfferedPermissions(t *testing.T) {
	p := piParser{driver: "pi"}
	var event AgentEvent
	err := p.line([]byte(`{"type":"tool_execution_end","toolCallId":"m","toolName":"opaque","result":{"details":{"server":"docs","tool":"lookup"},"content":[]}}`), func(e AgentEvent) { event = e })
	if err != nil || event.Tool.Server != "docs" || event.Tool.Name != "lookup" || event.Tool.Kind != "mcp" {
		t.Fatal(event, err)
	}
	var metadata *ToolEvent
	a := droidRPC{ctx: context.Background(), emit: func(e AgentEvent) {
		if e.Tool != nil {
			metadata = e.Tool
		}
		if e.Interaction == nil {
			return
		}
		for _, o := range e.Interaction.Request.ApprovalOptions() {
			if o.Kind == "accept" {
				t.Fatal("unoffered permission exposed")
			}
		}
		e.Interaction.Reply <- map[string]string{"decision": "accept"}
	}}
	result, err := a.interaction(a.ctx, []byte(`{"id":"p","method":"droid.request_permission","params":{"options":[{"value":"cancel"}],"toolUses":[{"toolUse":{"id":"m","name":"opaque","input":{}},"details":{"type":"mcp_tool","serverName":"docs","actualToolName":"lookup"}}]}}`))
	if err != nil || result.(map[string]string)["selectedOption"] != "cancel" || metadata == nil || metadata.Server != "docs" {
		t.Fatal(result, metadata, err)
	}
}

func TestModelCatalogParsing(t *testing.T) {
	pi, err := parseModelCatalog("pi", []byte(`{"models":[{"provider":"a","id":"shared","name":"A","reasoning":true,"thinkingLevelMap":{"low":"x","high":"y"},"headers":{"Authorization":"secret"}},{"provider":"b","id":"shared","name":"B"}]}`))
	if err != nil || len(pi) != 2 || pi[0].ID != "a/shared" || pi[1].ID != "b/shared" || !slices.Equal(pi[0].Efforts, []string{"off", "minimal", "low", "medium", "high"}) {
		t.Fatalf("Pi catalog: %+v %v", pi, err)
	}
	droid, err := parseModelCatalog("droid", []byte(`{"models":[{"id":"custom:Mine-0","displayName":"Mine","supportedReasoningEfforts":["low","high"],"defaultReasoningEffort":"high"},{"id":"blocked","disabled":true}]}`))
	if err != nil || len(droid) != 1 || droid[0].ID != "custom:Mine-0" || droid[0].DefaultEffort != "high" {
		t.Fatalf("Droid catalog: %+v %v", droid, err)
	}
	for _, tc := range []struct {
		name, driver, metadata string
		want                   []string
		previous, selected     string
	}{
		{"single", "pi", `"reasoning":true,"thinkingLevelMap":{"off":null,"minimal":null,"low":null,"medium":null,"high":"high","xhigh":null,"max":null}`, []string{"high"}, "low", "high"},
		{"toggle", "pi", `"reasoning":true,"thinkingLevelMap":{"minimal":null,"low":null,"medium":null,"high":"enabled"}`, []string{"off", "high"}, "high", "high"},
		{"extended", "pi", `"reasoning":true,"thinkingLevelMap":{"off":null,"xhigh":"high","max":null}`, []string{"minimal", "low", "medium", "high", "xhigh"}, "max", ""},
		{"none", "pi", `"reasoning":false`, []string{"off"}, "high", "off"},
		{"empty", "pi", `"reasoning":true,"thinkingLevelMap":{"off":null,"minimal":null,"low":null,"medium":null,"high":null}`, []string{}, "high", ""},
		{"single", "droid", `"supportedReasoningEfforts":["high"],"defaultReasoningEffort":"low"`, []string{"high"}, "", "high"},
		{"toggle", "droid", `"supportedReasoningEfforts":["none","high"],"defaultReasoningEffort":"none"`, []string{"none", "high"}, "low", "none"},
		{"ordered", "droid", `"supportedReasoningEfforts":["dynamic","low","high","high",""],"defaultReasoningEffort":"low"`, []string{"dynamic", "low", "high"}, "high", "high"},
		{"none", "droid", `"supportedReasoningEfforts":[],"defaultReasoningEffort":"high"`, []string{}, "high", ""},
	} {
		t.Run(tc.driver+"-"+tc.name, func(t *testing.T) {
			models, err := parseModelCatalog(tc.driver, []byte(`{"models":[{"id":"model","provider":"provider",`+tc.metadata+`}]}`))
			if err != nil || len(models) != 1 {
				t.Fatal(models, err)
			}
			m := models[0]
			if !slices.Equal(m.Efforts, tc.want) || m.ResolveEffort(tc.previous) != tc.selected {
				t.Fatalf("%+v, resolved=%q", m, m.ResolveEffort(tc.previous))
			}
		})
	}
	for _, driver := range []string{"pi", "droid"} {
		args, err := agentArgs(RunOptions{Driver: driver, Effort: "high"})
		if err != nil {
			t.Fatal(err)
		}
		flag := "--thinking"
		if driver == "droid" {
			flag = "--reasoning-effort"
		}
		found := false
		for i, arg := range args {
			if arg == flag && i+1 < len(args) && args[i+1] == "high" {
				found = true
			}
		}
		if !found {
			t.Fatalf("missing effort: %v", args)
		}
	}
}

func TestModelDiscoveryProcess(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX process fixture")
	}
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "agent")
			request := "get_available_models"
			response := `{"id":"models","success":true,"data":{"models":[{"provider":"test","id":"model"}]}}`
			if driver == "droid" {
				request = "droid.list_models"
				response = `{"id":"models","result":{"models":[{"id":"model"}]}}`
			}
			script := "#!/bin/sh\nread request\ncase \"$request\" in\n*" + request + "*) printf '%s\\n' '" + response + "';;\n*) exit 2;;\nesac\nsleep 20\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			models, err := DiscoverModels(ctx, driver, binary, dir)
			if err != nil || len(models) != 1 {
				t.Fatalf("discovery: %+v %v", models, err)
			}
			if err := os.WriteFile(binary, []byte("#!/bin/sh\nsleep 20\n"), 0700); err != nil {
				t.Fatal(err)
			}
			timeout, stop := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer stop()
			if _, err := DiscoverModels(timeout, driver, binary, dir); err == nil {
				t.Fatal("timeout ignored")
			}
		})
	}
}

func TestInstalledModelCatalog(t *testing.T) {
	if os.Getenv("MUJICAGENT_MODEL_DISCOVERY_TEST") != "1" {
		t.Skip("explicit read-only local CLI discovery")
	}
	for _, driver := range []string{"pi", "droid"} {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		models, err := DiscoverModels(ctx, driver, driver, t.TempDir())
		cancel()
		if err != nil {
			t.Fatal(driver, err)
		}
		t.Logf("%s: %d models", driver, len(models))
		for _, m := range models {
			if strings.Contains(m.ID, "hy4std") || len(m.Efforts) == 1 {
				t.Logf("%s %s: thinking=%v default=%q", driver, m.ID, m.Efforts, m.DefaultEffort)
			}
		}
	}
}

func TestAgentPermissionsAndImages(t *testing.T) {
	args, err := agentArgs(RunOptions{Driver: "pi"})
	if err != nil || !strings.Contains(strings.Join(args, " "), "--no-extensions --no-mcp --tools read,grep,find,ls") {
		t.Fatal(args, err)
	}
	if _, err := agentArgs(RunOptions{Driver: "pi", Access: "invalid"}); err == nil {
		t.Fatal("unknown access accepted")
	}
	path := filepath.Join(t.TempDir(), "red.png")
	img := image.NewRGBA(image.Rect(0, 0, 64, 64))
	for y := 0; y < 64; y++ {
		for x := 0; x < 64; x++ {
			img.Set(x, y, color.RGBA{R: 255, A: 255})
		}
	}
	f, _ := os.Create(path)
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	parts, err := imagePrompt(RunOptions{Prompt: "color?", Images: []string{path}})
	if err != nil || len(parts) != 2 {
		t.Fatal(parts, err)
	}
	part := parts[1].(map[string]string)
	data, _ := os.ReadFile(path)
	if part["mimeType"] != "image/png" || part["data"] != base64.StdEncoding.EncodeToString(data) {
		t.Fatal("image lost")
	}
}

func TestInstalledAgentTurns(t *testing.T) {
	if os.Getenv("MUJICAGENT_AGENT_LIVE_TEST") != "1" {
		t.Skip("explicit paid Pi/Droid test")
	}
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			dir := t.TempDir()
			o := RunOptions{Driver: driver, Binary: driver, Cwd: dir, Prompt: "Read proof.txt and report its exact contents. Do not modify any files.", Effort: "low"}
			if err := os.WriteFile(filepath.Join(dir, "proof.txt"), []byte("MujicProof719"), 0600); err != nil {
				t.Fatal(err)
			}
			o.Model = "magpie/workbuddy/deepseek-v4.1-flash"
			if driver == "pi" {
				o.Session = filepath.Join(dir, "session.jsonl")
			} else {
				o.Model = "custom:magpie/codex-fast/gpt-6.1-sol"
				img := image.NewRGBA(image.Rect(0, 0, 64, 64))
				for y := 0; y < 64; y++ {
					for x := 0; x < 64; x++ {
						img.Set(x, y, color.RGBA{R: 255, A: 255})
					}
				}
				path := filepath.Join(dir, "image.png")
				f, _ := os.Create(path)
				png.Encode(f, img)
				f.Close()
				o.Images = []string{path}
				o.Prompt += " Also identify the dominant color of the attached image."
			}
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			var answer string
			emit := func(e AgentEvent) {
				if e.Interaction != nil {
					e.Interaction.Reply <- map[string]any{"decision": "cancel"}
					return
				}
				if e.Session != "" {
					o.Session = e.Session
				}
				if e.Kind != "agentMessage" {
					return
				}
				if e.Delta {
					answer += e.Text
				} else {
					answer = e.Text
				}
				if e.Session != "" {
					o.Session = e.Session
				}
			}
			if err := Run(ctx, o, emit); err != nil {
				t.Fatal(err)
			}
			t.Log(driver, answer)
			if !strings.Contains(answer, "MujicProof719") {
				t.Fatal("file not read")
			}
			if driver == "droid" && !strings.Contains(strings.ToLower(answer), "red") {
				t.Fatal("image not recognized")
			}
			o.Images = nil
			o.Prompt = "Without reading files again, repeat the proof token from our previous turn."
			answer = ""
			if err := Run(ctx, o, emit); err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(answer, "MujicProof719") {
				t.Fatal("session not restored", answer)
			}
			o.Prompt = "Create a file named forbidden.txt containing hello in the current directory. Use a tool to do it."
			answer = ""
			if err := Run(ctx, o, emit); err != nil && !strings.Contains(err.Error(), "permission_rejected") {
				t.Fatal(err)
			}
			if _, err := os.Stat(filepath.Join(dir, "forbidden.txt")); !os.IsNotExist(err) {
				t.Fatal("read-only permission allowed writing", err)
			}
			t.Log(driver, "read-only write blocked", answer)

		})
	}
}

func TestCompactionDuplicateCompletions(t *testing.T) {
	var events []AgentEvent
	p := piParser{driver: "pi"}
	emit := func(e AgentEvent) { events = append(events, e) }
	for _, raw := range []string{
		`{"type":"compaction_start"}`,
		`{"type":"compaction_end","result":{"tokensBefore":120000,"estimatedTokensAfter":24000,"usage":{"input":999}}}`,
		`{"type":"compaction_end","result":{"tokensBefore":120000}}`,
		`{"type":"compaction_start"}`,
		`{"type":"compaction_end","aborted":true}`,
	} {
		if err := p.line([]byte(raw), emit); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 5 || events[0].ID != events[2].ID || events[3].ID == events[0].ID || events[1].Compaction.Before != 120000 || events[1].Compaction.AfterKnown {
		t.Fatal(events)
	}
	events = nil
	a := droidRPC{emit: emit}
	for _, raw := range []string{
		`{"type":"droid_working_state_changed","newState":"compacting_conversation"}`,
		`{"type":"droid_working_state_changed","newState":"streaming_assistant_message"}`,
		`{"type":"session_compacted","summaryId":"one","removedCount":4}`,
		`{"type":"session_compacted","summaryId":"one","removedCount":4}`,
		`{"type":"session_compacted","summaryId":"two","removedCount":5}`,
	} {
		if err := a.update([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
	if len(events) != 4 || events[0].ID != events[2].ID || events[3].ID == events[0].ID {
		t.Fatal(events)
	}
}

func TestSessionCapabilitiesAndInput(t *testing.T) {
	seen := map[string]bool{}
	for _, driver := range Drivers {
		if seen[driver.ID] || driver.ID == "" {
			t.Fatalf("invalid driver identity: %q", driver.ID)
		}
		seen[driver.ID] = true
		if found, ok := Lookup(driver.ID); !ok || found.Name != driver.Name {
			t.Fatalf("driver %s is not executable through the catalog", driver.ID)
		}
		for _, input := range []Input{
			{{Type: "unimplementedAttachment", Path: "file"}},
			{{Type: "localImage"}},
		} {
			if _, err := NewSession(context.Background(), RunOptions{Driver: driver.ID, Input: input}); err == nil {
				t.Fatalf("%s silently accepted an unsupported input", driver.ID)
			}
		}
		if !driver.Capabilities.PlanMode {
			if _, err := agentArgs(RunOptions{Driver: driver.ID, Plan: true}); err == nil {
				t.Fatalf("%s silently ignored plan mode", driver.ID)
			}
		}
		if !driver.Capabilities.ServiceTier {
			if _, err := agentArgs(RunOptions{Driver: driver.ID, ServiceTier: "priority"}); err == nil {
				t.Fatalf("%s silently ignored service tiers", driver.ID)
			}
		}
		saved := driver.SessionID(t.TempDir(), "local", "native")
		if driver.ID == "pi" {
			if !strings.HasSuffix(saved, filepath.Join("sessions", "pi", "local.jsonl")) {
				t.Fatal("Pi session path is not scoped to the local conversation")
			}
		} else if saved != "native" {
			t.Fatal("driver replaced a native session identity")
		}
	}
	if _, ok := Lookup("unknown"); ok || CanSteer("unknown") {
		t.Fatal("unknown driver was exposed as executable")
	}
	if _, err := NewSession(context.Background(), RunOptions{Driver: "codex"}); err == nil {
		t.Fatal("disconnected native session accepted")
	}
	input := Input{{Type: "text", Text: "before "}, {Type: "localImage", Path: "/image.png"}, {Type: "text", Text: " after"}}
	text, images := input.TextAndImages()
	if text != "before  after" || len(images) != 1 || images[0] != "/image.png" {
		t.Fatal("shared attachment input lost content")
	}
	wire, _ := json.Marshal(input.codex())
	if string(wire) != `[{"text":"before ","text_elements":[],"type":"text"},{"path":"/image.png","type":"localImage"},{"text":" after","text_elements":[],"type":"text"}]` {
		t.Fatalf("native ordered input changed: %s", wire)
	}
}

func TestSessionSettledAcceptanceAndCancellation(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX fake RPC executable")
	}
	for _, tc := range []struct {
		name, output, status string
		cancel               bool
	}{
		{"accepted", "{\"type\":\"response\",\"id\":\"prompt\",\"success\":true}\n{\"type\":\"agent_settled\"}", "completed", false},
		{"missing-ack", "{\"type\":\"agent_settled\"}", "failed", false},
		{"native-abort", "{\"type\":\"response\",\"id\":\"prompt\",\"success\":true}\n{\"type\":\"agent_settled\",\"aborted\":true}", "interrupted", false},
		{"local-stop", "{\"type\":\"response\",\"id\":\"prompt\",\"success\":true}", "interrupted", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			binary := filepath.Join(dir, "pi")
			script := "#!/bin/sh\nread request\nprintf '%s\\n' '" + tc.output + "'\nexec sleep 20\n"
			if err := os.WriteFile(binary, []byte(script), 0700); err != nil {
				t.Fatal(err)
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			session, err := NewSession(ctx, RunOptions{Driver: "pi", Binary: binary, Cwd: dir, Session: filepath.Join(dir, "session.jsonl"), Input: Input{{Type: "text", Text: "test"}}})
			if err != nil {
				t.Fatal(err)
			}
			defer session.Close()
			var ends []*TurnResult
			session.Run(func(e AgentEvent) {
				if e.Accepted && tc.cancel {
					session.Close()
				}
				if e.End != nil {
					ends = append(ends, e.End)
				}
			})
			if len(ends) != 1 || ends[0].Status != tc.status || (tc.status == "interrupted" && ends[0].Error != "") {
				t.Fatalf("terminal contract: %+v", ends)
			}
		})
	}
}
