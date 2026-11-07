package view

import (
	"encoding/json"
	"fmt"
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"image/png"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestNativeToolCards(t *testing.T) {
	cases := []struct{ scene, kind, pi, droid, args, output, title, body string }{
		{"bash", "commandExecution", "bash", "Execute", `{"command":"printf 'hello\\n'","timeout":30}`, `{"content":[{"type":"text","text":"hello\n"}]}`, "$ printf 'hello\\n'", "hello"},
		{"read", "toolCall", "read", "Read", `{"file_path":"/workspace/main.go"}`, `[{"type":"text","text":"package main\n\nfunc main() {}\n"}]`, " · /workspace/main.go", "package main"},
		{"edit", "fileChange", "edit", "Edit", `{"path":"/workspace/main.go","oldText":"old","newText":"new"}`, `"Successfully edited"`, "/workspace/main.go", "-old\n+new"},
		{"write", "fileChange", "write", "Create", `{"file_path":"/workspace/new.go","content":"package main\n"}`, `"Successfully created"`, "/workspace/new.go", "+package main"},
		{"script", "commandExecution", "Script", "Script", `{"script":"const result = await tools.Read({file_path: '/workspace/main.go'});\ntext(result);","code":"wrong"}`, `"package main"`, "Script", "const result = await tools.Read"},
		{"search", "webSearch", "grep", "Grep", `{"pattern":"TODO","path":"/workspace"}`, `"main.go:42: TODO verify"`, " · /workspace", "main.go:42"},
		{"todo", "plan", "todo", "TodoWrite", `{"todos":"1. [completed] Verify\n2. [in_progress] Review"}`, `"Plan updated"`, "", ""},
		{"patch", "fileChange", "apply_patch", "ApplyPatch", `"*** Begin Patch\n*** Update File: /workspace/main.go\n@@\n-old\n+new\n*** End Patch"`, `"Successfully applied"`, "", "*** Update File: /workspace/main.go"},
		{"skill", "toolCall", "Skill", "Skill", `{"skill":"review"}`, `"Skill loaded"`, "Skill", `"skill":`},
		{"tool-search", "toolCall", "ToolSearch", "ToolSearch", `{"query":"documentation"}`, `"Tools loaded"`, "ToolSearch", `"query":`},
		{"wait-script", "commandExecution", "WaitForScript", "WaitForScript", `{"toolCallId":"script","action":"status"}`, `{"status":"running"}`, "WaitForScript", "running"},
	}
	for _, driver := range []string{"codex", "pi", "droid"} {
		for _, lang := range []string{"zh-Hans", "en"} {
			for _, tc := range cases {
				if tc.scene == "script" && driver != "droid" {
					continue
				}
				if driver == "codex" && (tc.scene == "skill" || tc.scene == "tool-search") {
					continue
				}
				t.Run(lang+"-"+driver+"-"+tc.scene, func(t *testing.T) {
					s := model.New()
					s.Settings.Language = lang
					thread := s.AddThread("", "")
					thread.Harness = driver
					name := tc.pi
					if driver == "droid" {
						name = tc.droid
					}
					item := model.Item{ID: "tool", Kind: tc.kind, Status: "completed", MCP: &model.MCPCall{Tool: name, Arguments: tc.args, Result: tc.output}}
					text := toolActivityText(&item, true)
					if !strings.Contains(text, tc.title) || !strings.Contains(text, tc.body) {
						t.Fatal("native fields were not adapted", text)
					}
					if preview := toolActivityText(&item, false); strings.Contains(preview, "\n") || strings.Contains(preview, `"file_path"`) || strings.Contains(preview, `"command"`) {
						t.Fatal("raw arguments leaked into a tool row", preview)
					}
					if driver == "codex" {
						item.Text, item.MCP = text, nil
						if tc.scene == "read" {
							item.Kind = "commandExecution"
						}
					}
					if tc.scene == "todo" {
						item.Text = "1. [completed] Verify\n2. [in_progress] Review"
					}
					thread.Turns = []model.Turn{{ID: "turn", Prompt: "Verify the project", Status: "completed", Items: []model.Item{item}}}
					c := controller.New(t.TempDir(), s, nil)
					defer c.Close()
					v := New(c)
					*v.openState("activity-" + thread.ID + "-0-0") = true
					*v.openState(thread.ID + "-0-0-0") = true
					tt := ui.NewTester(v.Render, 1100, 800)
					tt.Frame()
					tt.Advance(time.Second)
					tt.Frame()
					if _, ok := tt.Find(v.texts().ActivityOutput()); !ok && tc.scene != "todo" && !(driver == "codex" && tc.scene == "search") {
						t.Fatal("native tool did not use the Codex output viewer")
					}
					if tc.scene == "todo" {
						if _, ok := tt.Find(v.texts().ActivityOutput()); ok || !tt.HasText("Verify") || !tt.HasText("Review") {
							t.Fatal("native Todo did not use the shared plan")
						}
					}
					if tt.HasText(v.texts().McpArguments()) {
						t.Fatal("native tool fell back to MCP")
					}
					if output := os.Getenv("MUJICAGENT_TOOLS_SCREENSHOTS"); output != "" {
						if err := os.MkdirAll(output, 0700); err != nil {
							t.Fatal(err)
						}
						f, err := os.Create(filepath.Join(output, lang+"-"+driver+"-"+tc.scene+".png"))
						if err != nil {
							t.Fatal(err)
						}
						defer f.Close()
						if err = png.Encode(f, tt.Image()); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func TestDroidScriptBatchScreenshots(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		t.Run(lang, func(t *testing.T) {
			s := model.New()
			s.Settings.Language = lang
			thread := s.AddThread("", "")
			thread.Harness = "droid"
			thread.Turns = []model.Turn{{ID: "turn", Prompt: "Inspect the project in parallel", Status: "completed", Items: []model.Item{
				{ID: "script", Kind: "commandExecution", Status: "completed", MCP: &model.MCPCall{Tool: "Script", Arguments: `{"script":"const results = await Promise.allSettled([\n  tools.Read({file_path: 'main.go'}),\n  tools.Read({file_path: 'go.mod'}),\n  tools.Skill({skill: 'review'})\n]); results.forEach(text);","unused":"hidden"}`, Result: `"3 calls completed"`}},
				{ID: "read-1", ParentID: "script", Kind: "toolCall", Status: "completed", MCP: &model.MCPCall{Tool: "Read", Arguments: `{"file_path":"main.go"}`, Result: `"package main"`}},
				{ID: "read-2", ParentID: "script", Kind: "toolCall", Status: "failed", MCP: &model.MCPCall{Tool: "Read", Arguments: `{"file_path":"go.mod"}`, Result: `"File not found"`}},
				{ID: "skill", ParentID: "script", Kind: "toolCall", Status: "completed", MCP: &model.MCPCall{Tool: "Skill", Arguments: `{"skill":"review"}`, Result: `"Skill loaded"`}},
				{ID: "todo", ParentID: "script", Kind: "plan", Status: "completed", Text: "1. [completed] Inspect files\n2. [pending] Review changes", MCP: &model.MCPCall{Tool: "TodoWrite"}},
				{ID: "compact-1", Kind: "contextCompaction", Status: "completed"},
				{ID: "compact-2", Kind: "contextCompaction", Status: "interrupted"},
			}}}
			c := controller.New(t.TempDir(), s, nil)
			defer c.Close()
			v := New(c)
			*v.openState("activity-" + thread.ID + "-0-0") = true
			*v.openState(thread.ID + "-0-0-0") = true
			tt := ui.NewTester(v.Render, 1100, 1000)
			tt.Frame()
			tt.Advance(time.Second)
			tt.Frame()
			if !tt.HasText("Read · main.go") || !tt.HasText("Read · go.mod") || !tt.HasText("Skill") || !tt.HasText(v.texts().CompactionCompleted()) || !tt.HasText(v.texts().CompactionCancelled()) || tt.HasText(v.texts().McpUnknownTool()) {
				t.Fatal("Script children or Compact missing, or native tools fell back to unknown MCP")
			}
			if dir := os.Getenv("MUJICAGENT_AGENTS_SCREENSHOTS"); dir != "" {
				if err := os.MkdirAll(dir, 0700); err != nil {
					t.Fatal(err)
				}
				f, err := os.Create(filepath.Join(dir, lang+"-droid-script-batch.png"))
				if err != nil {
					t.Fatal(err)
				}
				defer f.Close()
				if err := png.Encode(f, tt.Image()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestAgentRunningPermissionLabel(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, driver := range []string{"codex", "pi", "droid"} {
			t.Run(lang+"-"+driver, func(t *testing.T) {
				s := model.New()
				s.Settings.Language = lang
				s.NoProjectExpanded = true
				thread := s.AddThread("", "")
				thread.Harness, thread.AgentAccess = driver, agentFullAccess(driver)
				thread.PermissionProfile = ":danger-full-access"
				thread.AgentModel = "example-model"
				thread.Title = "Permission display"
				thread.Turns = []model.Turn{{ID: "running-turn", Prompt: "Check the project status.", Status: "inProgress", Started: time.Now(), Items: []model.Item{{ID: "reply", Kind: "agentMessage", Text: "I am checking the project.", Status: "inProgress"}}}}
				c := controller.New(t.TempDir(), s, nil)
				defer c.Close()
				c.Running[thread.ID] = true
				c.Connection = "Connected"
				v := New(c)
				tt := ui.NewTester(v.Render, 840, 560)
				tt.Frame()
				tt.Advance(time.Second)
				tt.Frame()
				if tt.HasText(v.texts().PermissionNextTurn(v.texts().FullAccess())) {
					t.Fatal("unchanged permissions are labeled as a next-turn change")
				}
				if !tt.HasText(v.texts().FullAccess()) {
					t.Fatal("current permissions are missing")
				}
				if output := os.Getenv("MUJICAGENT_PREFERENCES_SCREENSHOTS"); output != "" {
					if err := os.MkdirAll(output, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(output, lang+"-"+driver+"-running.png"))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tt.Image()); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestAgentDefaultPermissionConfirmation(t *testing.T) {
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			s := model.New()
			s.Settings.Language = "en"
			thread := s.AddThread("", "")
			thread.Harness = driver
			c := controller.New(t.TempDir(), s, nil)
			defer c.Close()
			v := New(c)
			v.Settings, v.agentSection = true, driver
			*v.openState("harness-" + driver) = true
			tt := ui.NewTester(v.Render, 1100, 800)
			tt.Frame()
			label := agentName(driver) + " " + v.texts().DefaultPermissions()
			chooseFull := func() {
				t.Helper()
				bounds, ok := tt.Find(label)
				if !ok {
					t.Fatal("default permission row missing")
				}
				tt.ClickAt(bounds.X+124+87, bounds.Y+bounds.H/2)
				tt.Frame()
				if err := tt.Click(v.texts().FullAccess()); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				tt.Advance(time.Second)
			}
			chooseFull()
			if *v.agentDefaultAccess(driver) != "" || !tt.HasText(v.texts().FullAccessDefaultWarning()) {
				t.Fatal("full access default changed before confirmation")
			}
			if err := tt.Click(v.texts().Cancel()); err != nil {
				t.Fatal(err)
			}
			if *v.agentDefaultAccess(driver) != "" {
				t.Fatal("cancel granted access")
			}
			tt.Advance(time.Second)
			chooseFull()
			if err := tt.Click(v.texts().EnableFullAccess()); err != nil {
				t.Fatal(err)
			}
			if *v.agentDefaultAccess(driver) != agentFullAccess(driver) || thread.AgentAccess != "" {
				t.Fatal("confirmation did not isolate default and existing permissions")
			}
		})
	}
}

func TestAgentPendingPermissionLabel(t *testing.T) {
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			dir := t.TempDir()
			s := model.New()
			thread := s.AddThread("", dir)
			thread.Harness = driver
			s.Settings.PiPath, s.Settings.DroidPath = filepath.Join(dir, "missing-agent"), filepath.Join(dir, "missing-agent")
			c := controller.New(dir, s, nil)
			defer c.Close()
			// Capture the active permissions through the real send entry point.
			// The focused renderer does not drain asynchronous process results.
			if !c.Send("permission snapshot", false) {
				t.Fatal(c.Error)
			}
			thread.AgentAccess = agentFullAccess(driver)
			v := New(c)
			v.permissionPicker, v.permissionTarget = true, thread.ID
			tt := ui.NewTester(func(ctx *ui.Context) {
				v.theme(ctx)
				v.agentPermissions(ctx, thread)
			}, 420, 420)
			tt.Frame()
			tt.Advance(time.Second)
			tt.Frame()
			if !tt.HasText(v.texts().ReadOnly()+" → "+v.texts().FullAccess()) || !tt.HasText(v.texts().PermissionTiming()) || !tt.HasText(v.texts().PermissionNextTurn(v.texts().FullAccess())) {
				t.Fatal("pending permissions do not match the Codex presentation")
			}
			thread.AgentAccess = ""
			tt.Frame()
			if tt.HasText(v.texts().PermissionTiming()) || tt.HasText(v.texts().PermissionNextTurn(v.texts().ReadOnly())) {
				t.Fatal("restoring permissions retained a pending change")
			}
			thread.AgentAccess = agentFullAccess(driver)
			c.Running[thread.ID] = false
			tt.Frame()
			if tt.HasText(v.texts().PermissionTiming()) || !tt.HasText(v.texts().FullAccess()) {
				t.Fatal("idle conversation still shows a pending change")
			}
		})
	}
}

func TestHarnessTimelineScreenshots(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, driver := range []string{"codex", "pi", "droid"} {
			for _, scene := range []string{"collapsed", "expanded", "long-output", "failed", "approval", "running", "stopped", "compact", "compact-running", "compact-failed", "compact-stopped", "compact-stats", "compact-narrow", "compact-unknown", "script-nested"} {
				t.Run(lang+"-"+driver+"-"+scene, func(t *testing.T) {
					s := model.New()
					s.Settings.Language = lang
					thread := s.AddThread("", "")
					thread.Harness = driver
					thread.ServerID = "session"
					status := "completed"
					if scene == "running" || scene == "approval" || scene == "compact-running" {
						status = "inProgress"
					}
					if scene == "stopped" || scene == "compact-stopped" {
						status = "interrupted"
					}
					output := "proof.txt: verified"
					if scene == "long-output" {
						output = strings.Repeat("proof.txt: verified output with a long line to check wrapping and scrolling\n", 40)
					}
					tool := model.Item{ID: "read-1", Kind: "toolCall", Status: status, MCP: &model.MCPCall{Tool: "Read", Arguments: `{"path":"proof.txt"}`, Result: output}}
					if scene == "failed" {
						tool.Status = "failed"
						tool.MCP.Error = "File not found: proof.txt"
					}
					thread.Turns = []model.Turn{{ID: "turn", Prompt: "Read proof.txt and report its contents", Status: status, Items: []model.Item{{ID: "m", Kind: "agentMessage", Text: "I will read the file.", Status: "completed"}, tool}}}
					if strings.HasPrefix(scene, "compact") {
						compact := model.Item{ID: "compact-1", Kind: "contextCompaction", Status: status}
						if scene == "compact-failed" {
							compact.Status, compact.Text = "failed", "Context compaction failed"
						}
						if scene == "compact-stats" || scene == "compact-narrow" {
							compact.Compaction = model.CompactionStats{Before: 120000, After: 24000, BeforeKnown: true, AfterKnown: true}
						}
						if scene == "compact-unknown" {
							compact.Status = "unknown"
						}
						thread.Turns[0].Items[1] = compact
						if scene != "compact-running" {
							thread.Turns[0].Items = append(thread.Turns[0].Items, tool, model.Item{ID: "after", Kind: "agentMessage", Status: "completed", Text: "Continuing after compaction."})
						}
					}
					if scene == "script-nested" {
						thread.Turns[0].Items[1].ParentID = "script"
						thread.Turns[0].Items = append(thread.Turns[0].Items[:1], model.Item{ID: "script", Kind: "commandExecution", Status: status, MCP: &model.MCPCall{Tool: "Script", Arguments: `{"script":"text(await tools.Read({file_path: 'proof.txt'}));"}`, Result: `"proof.txt: verified"`}}, thread.Turns[0].Items[1])
					}
					c := controller.New(t.TempDir(), s, nil)
					defer c.Close()
					c.Running[thread.ID] = status == "inProgress"
					v := New(c)
					if scene != "collapsed" {
						*v.openState("activity-" + thread.ID + "-0-1") = true
						*v.openState(thread.ID + "-0-1-0") = true
					}
					if scene == "approval" {
						c.Requests = []*codex.Request{{ID: json.RawMessage(`"approval"`), ThreadID: thread.ID, Method: "item/commandExecution/requestApproval", Reason: "Run pwd in this project", Command: "pwd", AvailableDecisions: []json.RawMessage{json.RawMessage(`"accept"`), json.RawMessage(`"cancel"`)}}}
					}
					width := 1100
					if scene == "compact-narrow" {
						width = 600
					}
					tt := ui.NewTester(v.Render, width, 800)
					tt.Frame()
					tt.Advance(time.Second)
					tt.Frame()
					if !strings.HasPrefix(scene, "compact") && !tt.HasText(v.texts().AgentToolCall()) {
						t.Fatal("tool category missing")
					}
					if strings.HasPrefix(scene, "compact") {
						label, _, _ := v.compactionLabel(&thread.Turns[0].Items[1], status == "inProgress")
						if !tt.HasText(label) {
							t.Fatal("compaction separator missing", label)
						}
						bounds, found := tt.Find(label)
						if !found || bounds.X < 246 || bounds.X+bounds.W > float32(width) || bounds.H < 12 {
							t.Fatalf("separator outside content: %+v", bounds)
						}
						if scene == "compact-running" && tt.HasText(agentName(driver)) {
							t.Fatal("duplicate running indicator")
						}
						if tt.HasText(v.texts().Compaction() + " × 1") {
							t.Fatal("compaction grouped as a tool")
						}
					}
					if scene == "running" && !tt.HasText(agentName(driver)) {
						t.Fatal("wrong running harness")
					}
					if dir := os.Getenv("MUJICAGENT_AGENTS_SCREENSHOTS"); dir != "" {
						if err := os.MkdirAll(dir, 0700); err != nil {
							t.Fatal(err)
						}
						f, err := os.Create(filepath.Join(dir, fmt.Sprintf("%s-%s-%s.png", lang, driver, scene)))
						if err != nil {
							t.Fatal(err)
						}
						defer f.Close()
						if err = png.Encode(f, tt.Image()); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func TestHarnessInteractionScreenshots(t *testing.T) {
	for _, lang := range []string{"en", "zh-Hans"} {
		for _, scene := range []string{"picker", "picker-empty", "multi-question", "pi-editor", "plan"} {
			t.Run(lang+"-"+scene, func(t *testing.T) {
				s := model.New()
				s.Settings.Language = lang
				thread := s.AddThread("", "")
				thread.Harness = "droid"
				c := controller.New(t.TempDir(), s, nil)
				defer c.Close()
				v := New(c)
				r := &codex.Request{ID: json.RawMessage(`"form"`), ThreadID: thread.ID, Method: "client/form", Mode: "form", Title: "Choose channels", Refreshable: true, FilterOptions: true, RequestedSchema: json.RawMessage(`{"type":"object","properties":{"selection":{"type":"array","title":"Channels","items":{"type":"string","enum":["alpha","beta"],"enumNames":["Alpha · public","Beta · private"]}}},"required":["selection"]}`), InitialValues: map[string]string{"selection": `["beta"]`}}
				switch scene {
				case "picker-empty":
					r.RequestedSchema = json.RawMessage(`{"type":"object","properties":{}}`)
					r.Message = "No channels. Refresh to try again."
				case "multi-question":
					r.Method = "item/tool/requestUserInput"
					r.Cancellable = true
					r.Questions = []codex.Question{{ID: "q", Question: "Which platforms?", MultiSelect: true, IsOther: true}}
					for _, label := range []string{"macOS", "Windows"} {
						r.Questions[0].Options = append(r.Questions[0].Options, struct {
							Label       string `json:"label"`
							Description string `json:"description"`
						}{Label: label})
					}
					r.InitialValues = map[string]string{"q": `["macOS","Windows"]`}
				case "pi-editor":
					thread.Harness = "pi"
					r.Title = "Edit notes"
					r.Refreshable = false
					r.FilterOptions = false
					r.Multiline = true
					r.RequestedSchema = json.RawMessage(`{"type":"object","properties":{"answer":{"type":"string","title":"Notes"}},"required":["answer"]}`)
					r.InitialValues = map[string]string{"answer": "First line\nSecond line"}
				case "plan":
					thread.Harness = "pi"
					thread.Turns = []model.Turn{{Status: "completed", Items: []model.Item{{ID: "plan", Kind: "plan", Text: "☑ Read the project\n☐ Verify the change", Status: "completed"}}}}
					*v.openState("activity-" + thread.ID + "-0-0") = true
					*v.openState(thread.ID + "-0-0-0") = true
				}
				if scene != "plan" {
					c.Requests = []*codex.Request{r}
				}
				tt := ui.NewTester(v.Render, 1100, 800)
				tt.Frame()
				tt.Advance(time.Second)
				tt.Frame()
				if scene == "picker" && (!tt.HasText("Alpha · public") || !tt.HasText(v.texts().AgentRefreshModels())) {
					t.Fatal("picker controls missing")
				}
				if scene == "multi-question" && !tt.HasText(v.texts().Cancel()) {
					t.Fatal("question cancellation missing")
				}
				if scene == "plan" && !tt.HasText("Verify the change") {
					t.Fatal("plan missing")
				}
				if dir := os.Getenv("MUJICAGENT_AGENTS_SCREENSHOTS"); dir != "" {
					if err := os.MkdirAll(dir, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(dir, "gap-"+lang+"-"+scene+".png"))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err = png.Encode(f, tt.Image()); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestHarnessInstallScreenshots(t *testing.T) {
	scenes := []string{
		"vp-current", "vp-update", "vp-confirm", "npm-confirm", "brew-confirm",
		"managed-official-confirm", "managed-npm-confirm", "managed-mirror-confirm",
		"unknown", "missing", "install-official", "install-npm", "install-mirror", "install-npm-fallback",
		"preparing", "preview-error", "installing", "install-error", "cancelled", "running",
		"bun-confirm", "pnpm-confirm", "unsupported-manager", "manager-unavailable", "manager-unverified", "manager-ambiguous",
	}
	for _, driver := range []string{"codex", "pi", "droid"} {
		for _, lang := range []string{"zh-Hans", "en"} {
			for _, scene := range append(slices.Clone(scenes), "native-confirm", "install-official-npm", "app-managed", "install-runtime-missing", "install-mirror-error") {
				if driver == "codex" && (scene == "native-confirm" || scene == "install-official-npm" || scene == "app-managed" || scene == "install-runtime-missing" || scene == "install-mirror-error") {
					continue
				}
				if driver != "codex" && scene == "install-npm-fallback" {
					continue
				}
				if driver == "pi" && (scene == "brew-confirm" || scene == "managed-mirror-confirm" || scene == "install-mirror" || scene == "app-managed") {
					continue
				}
				t.Run(lang+"/"+driver+"/"+scene, func(t *testing.T) {
					s := model.New()
					s.Settings.Language = lang
					s.Settings.HarnessSource = "cn-native" // Legacy global selection must not drive updates.
					thread := s.AddThread("", "")
					c := controller.New(t.TempDir(), s, nil)
					c.Connection = "Connected"
					pkg := "@openai/codex"
					if driver == "pi" {
						pkg = "@earendil-works/pi-coding-agent"
					}
					if driver == "droid" {
						pkg = "@factory/cli"
					}
					u := c.HarnessFor(driver)
					u.Binary = s.Settings.AgentPath(driver)
					u.CheckedAt = time.Now()
					u.Installation = harness.Installation{Driver: driver, Path: "/Users/demo/.vite-plus/bin/" + driver, Version: "0.167.0", Method: "vp", Prefix: "/Users/demo/.vite-plus/bin/vp"}
					u.Release = harness.Release{Version: "0.168.0", URL: "https://github.com/openai/codex/releases/download/rust-v0.168.0/codex-package-aarch64-apple-darwin.tar.gz", Digest: "sha256:" + strings.Repeat("a1", 32), Entry: "bin/codex"}
					v := New(c)
					v.agentSection, v.harnessTarget = driver, driver
					v.Settings = true
					*v.openState("harness-" + driver) = true
					fresh := strings.HasPrefix(scene, "install-") && scene != "install-error"
					if fresh || scene == "missing" {
						u.Installation = harness.Installation{}
						u.Missing = true
						c.Connection = "Disconnected"
					}
					switch scene {
					case "native-confirm":
						u.Installation.Method = "native"
						u.Installation.Path = "/Users/demo/.local/bin/" + driver
					case "app-managed":
						u.Installation.Method, u.Installation.UpdateIssue = "Factory.app", "unsupported"
						u.Installation.Path = "/Applications/Factory.app/Contents/Resources/bin/droid"
					case "vp-current":
						u.Release.Version = u.Installation.Version
					case "npm-confirm":
						u.Installation.Method = "npm"
						u.Installation.Path = "/usr/local/bin/" + driver
						u.Installation.Prefix = "/usr/local"
					case "bun-confirm", "pnpm-confirm":
						id := strings.TrimSuffix(scene, "-confirm")
						u.Installation.Method = id
						u.Installation.Path = "/Users/demo/." + id + "/bin/" + driver
						u.Installation.Manager = "/Users/demo/." + id + "/bin/" + id
						u.Installation.PackageRoot = "/Users/demo/." + id + "/global/node_modules"
					case "unsupported-manager":
						u.Installation.Method = "vlt"
						u.Installation.UpdateIssue = "unsupported"
						u.Installation.Path = "/Users/demo/.vlt/bin/" + driver
					case "manager-unavailable", "manager-unverified":
						u.Installation.Method = "pnpm"
						u.Installation.UpdateIssue = strings.TrimPrefix(scene, "manager-")
						u.Installation.Path = "/Users/demo/.local/share/pnpm/" + driver
					case "manager-ambiguous":
						u.Installation.Method = "ambiguous"
						u.Installation.UpdateIssue = "ambiguous"
						u.Release = harness.Release{} // Warning must survive a failed version lookup.
					case "brew-confirm":
						u.Installation.Method = "brew"
						u.Installation.Path = "/opt/homebrew/bin/" + driver
						u.Installation.Prefix = "/opt/homebrew"
					case "unknown":
						u.Installation.Method = "custom"
						u.Installation.Path = "/opt/custom/" + driver
					case "running":
						c.Running[thread.ID] = true
					case "installing":
						u.Installing = true
						c.Connection = "Disconnected"
					case "install-error":
						u.Error = "installation failed: exit status 1"
						c.Connection = "Disconnected"
					}
					stage := "/Users/demo/Library/Application Support/MujicAgent/tools/" + driver + "/release-0.168.0-preview"
					if strings.HasPrefix(scene, "managed-") {
						u.Installation.Method = "managed"
						u.Installation.Path = strings.Replace(stage, "0.168.0-preview", "0.167.0-old", 1) + "/bin/" + driver
					}
					if strings.HasSuffix(scene, "-confirm") || fresh || scene == "preparing" || scene == "preview-error" {
						v.harnessDialog = true
						p := &harness.InstallPlan{Driver: driver, Current: u.Installation, Release: u.Release, Path: u.Installation.Path}
						u.Plan = p
						switch u.Installation.Method {
						case "native":
							p.Command = u.Installation.Path
							p.Args = []string{"update", "--self", "--no-approve"}
							if driver == "droid" {
								p.Args = []string{"update", "--version", "0.168.0"}
							}
						case "vp":
							p.Command, p.Args = u.Installation.Prefix, []string{"update", "--global", pkg + "@0.168.0"}
						case "npm":
							p.Command = "/usr/local/bin/npm"
							p.Args = []string{"install", "--global", "--prefix", "/usr/local", pkg + "@0.168.0", "--ignore-scripts", "--no-audit", "--no-fund"}
						case "brew":
							p.Command, p.Args = "/opt/homebrew/bin/brew", []string{"upgrade", "--cask", driver}
						case "bun", "pnpm":
							p.Command, p.Args = u.Installation.Manager, []string{"add", "--global", pkg + "@0.168.0", "--ignore-scripts"}
						default:
							p.Stage, p.Path = stage, stage+"/bin/codex"
							if strings.Contains(scene, "mirror") || strings.Contains(scene, "npm") {
								p.Source = "native-mirror"
								p.Release.URL = "https://registry.npmmirror.com/@openai/codex/-/codex-0.168.0-darwin-arm64.tgz"
								p.Release.Digest = "sha512-" + strings.Repeat("aB12", 22)
								p.Path = stage + "/package/vendor/aarch64-apple-darwin/bin/codex"
								v.harnessSource = "native-mirror"
							}
							if strings.Contains(scene, "npm") {
								v.harnessSource = "cn"
								if scene != "install-npm-fallback" {
									p.Source = "cn"
									p.Command = "/usr/local/bin/npm"
									p.Args = []string{"install", "--prefix", stage, pkg + "@0.168.0", "--registry", "https://registry.npmmirror.com", "--ignore-scripts", "--no-audit", "--no-fund"}
									p.Path = stage + "/node_modules/.bin/" + driver
								}
							}
						}
						if scene == "install-official-npm" {
							v.harnessSource, p.Source = "npm", "npm"
							p.Command = "/usr/local/bin/npm"
							p.Args = []string{"install", "--prefix", stage, pkg + "@0.168.0", "--registry", "https://registry.npmjs.org", "--ignore-scripts"}
							p.Path = stage + "/node_modules/.bin/" + driver
						}
						if scene == "install-runtime-missing" {
							v.harnessSource, u.Plan = "npm", nil
							u.Error = driver + " npm installation requires Node.js; select the official native package for installation without Node.js"
						} else if scene == "install-mirror-error" {
							v.harnessSource, u.Plan = "cn", nil
							u.Error = "download HTTP 404"
						} else if scene == "preparing" {
							u.Plan, u.Preparing = nil, true
						} else if scene == "preview-error" {
							u.Plan = nil
							u.Error = "download HTTP 503"
						}
					}
					if p := u.Plan; p != nil && driver != "codex" && p.Command == "" {
						if driver == "pi" {
							p.Release.URL = "https://github.com/earendil-works/pi/releases/download/v0.168.0/pi-darwin-arm64.tar.gz"
							p.Path = stage + "/pi/pi"
						} else {
							p.Release.URL = "https://downloads.factory.ai/factory-cli/releases/0.168.0/darwin/arm64/droid"
							p.Path = stage + "/droid"
							if p.Source == "native-mirror" {
								p.Release.URL = "https://registry.npmmirror.com/@factory/cli-darwin-arm64/-/cli-darwin-arm64-0.168.0.tgz"
								p.Path = stage + "/package/bin/droid"
							}
						}
					}
					tt := ui.NewTester(v.Render, 1060, 720)
					tt.Frame()
					tt.Advance(time.Second)
					tt.Frame()
					tx := v.texts()
					if tt.HasText(tx.HarnessSource()) || tt.HasText(tx.HarnessManagedInstall()) {
						t.Fatal("legacy channel or managed migration control remains")
					}
					for _, option := range []settingOption{{"", tx.HarnessOfficial()}, {"npm", tx.HarnessNpmOfficial()}, {"cn", tx.HarnessCN()}, {"native-mirror", tx.HarnessNativeMirror()}} {
						want := fresh && slices.Contains(harness.InstallSources(driver), option.value)
						if tt.HasText(option.label) != want {
							t.Fatalf("install choice visibility: %s, want=%v", option.label, want)
						}
					}

					if v.harnessDialog {
						title := tx.HarnessUpdatePreview(agentName(driver))
						if fresh {
							title = tx.HarnessInstallPreview(agentName(driver))
						}
						if !tt.HasText(title) {
							t.Fatal("dialog does not identify the selected harness")
						}
						label := tx.HarnessConfirmUpdate()
						if fresh {
							label = tx.HarnessConfirmInstall()
						}
						b, ok := tt.Find(label)
						if !ok || b.X < 0 || b.Y < 0 || b.X+b.W > 1060 || b.Y+b.H > 720 {
							t.Fatalf("confirmation clipped: %+v", b)
						}
						if u.Plan != nil {
							expected := u.Plan.CommandPreview()
							if expected == "" {
								expected = tx.HarnessNativeAction()
							}
							if !tt.HasText(expected) || !tt.HasText(u.Plan.Path) {
								t.Fatal("preview omitted command/action or path")
							}
						}
					}
					if scene == "unknown" && !tt.HasText(tx.HarnessUnknownManager()) {
						t.Fatal("unknown manager missing guidance")
					}
					if u.Installation.Path != "" && !u.Installation.CanUpdate() {
						if slices.Contains(tt.Texts(), tx.HarnessUpdate()) {
							t.Fatal("unsupported installation offers an update")
						}
						message := tx.HarnessUnsupportedManager("vlt")
						switch scene {
						case "app-managed":
							message = tx.HarnessUnsupportedManager("Factory.app")
						case "unknown":
							message = tx.HarnessUnknownManager()
						case "manager-unavailable":
							message = tx.HarnessManagerUnavailable("pnpm")
						case "manager-unverified":
							message = tx.HarnessManagerUnverified("pnpm")
						case "manager-ambiguous":
							message = tx.HarnessManagerAmbiguous()
						}
						b, ok := tt.Find(message)
						if !ok || b.X+b.W > 1060 || b.Y+b.H > 720 {
							t.Fatalf("unsupported warning missing or clipped: %+v", b)
						}
					}
					if scene == "missing" && !tt.HasText(tx.HarnessInstall()) {
						t.Fatal("missing install entry")
					}
					if output := os.Getenv("MUJICAGENT_INSTALL_SCREENSHOTS"); output != "" {
						if err := os.MkdirAll(output, 0700); err != nil {
							t.Fatal(err)
						}
						f, err := os.Create(filepath.Join(output, lang+"-"+driver+"-"+scene+".png"))
						if err != nil {
							t.Fatal(err)
						}
						defer f.Close()
						if err := png.Encode(f, tt.Image()); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func TestAgentCompactScreenshots(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, scene := range []string{"settings-codex", "settings-codex-running", "settings-pi", "settings-droid", "settings-pi-confirm", "settings-droid-confirm", "picker-codex", "picker-pi", "picker-droid", "picker-favorites", "picker-loading", "picker-error", "picker-many", "picker-search-empty", "picker-favorites-empty", "permissions-pi", "permissions-droid"} {
			t.Run(lang+"-"+scene, func(t *testing.T) {
				s := model.New()
				s.Settings.Language = lang
				thread := s.AddThread("", "")
				thread.Turns = []model.Turn{{Prompt: "Implement the next step", Status: "completed", Items: []model.Item{{Kind: "agentMessage", Text: "The implementation is ready for review."}}}}
				c := controller.New(t.TempDir(), s, nil)
				c.Connection = "Connected"
				c.Models = []codex.Model{{Model: "gpt-6.1-sol"}, {Model: "gpt-6-astra"}, {Model: "gpt-6.1-terra"}}
				for _, id := range []string{"pi", "droid"} {
					h := c.HarnessFor(id)
					h.ModelsChecked = true
					h.Installation.Version = "1.5.0"
					h.Models = []harness.AgentModel{
						{ID: "gpt-6.1-sol", Name: "GPT-6.1 Sol", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "medium"},
						{ID: "claude-sonnet-4-6", Name: "Claude Sonnet 4.6", Efforts: []string{"low", "medium", "high"}, DefaultEffort: "high"},
						{ID: "custom:Local-0", Name: "Local model"},
					}
				}
				s.Settings.ModelFavorites = []model.ModelFavorite{{Harness: "codex", ModelID: "gpt-6-astra"}, {Harness: "pi", ModelID: "gpt-6.1-sol"}, {Harness: "droid", ModelID: "gpt-6.1-sol"}}
				v := New(c)
				if strings.HasPrefix(scene, "permissions-") {
					thread.Harness = strings.TrimPrefix(scene, "permissions-")
					v.permissionTarget = thread.ID
					v.permissionPicker = true
				} else if strings.HasPrefix(scene, "settings") {
					v.Settings = true
					v.agentSection = strings.TrimSuffix(strings.TrimSuffix(scene[9:], "-running"), "-confirm")
					if strings.HasSuffix(scene, "-confirm") {
						v.defaultPermissionHarness, v.confirmFullAccess = v.agentSection, true
					}
					if scene == "settings-codex-running" {
						c.Running[thread.ID] = true
						h := c.HarnessFor("codex")
						h.Installation.Version = "0.167.0"
						h.Installation.Method = "custom"
						h.Release.Version = "0.167.0"
					}
					if v.agentSection != "" {
						*v.openState("harness-" + v.agentSection) = true
					}
				} else {
					v.modelPicker = true
					v.pickerHarness = scene[7:]
					if v.pickerHarness == "pi" || v.pickerHarness == "droid" {
						thread.Harness = v.pickerHarness
						thread.AgentModel = "gpt-6.1-sol"
						thread.AgentEffort = "medium"
					}
					if v.pickerHarness == "loading" || v.pickerHarness == "error" {
						v.pickerHarness = "pi"
						h := c.HarnessFor("pi")
						h.Models = nil
						h.ModelsLoading = scene == "picker-loading"
						if !h.ModelsLoading {
							h.ModelsError = "CLI unavailable"
						}
					}
				}
				switch scene {
				case "picker-many":
					v.pickerHarness = "pi"
					for i := 0; i < 20; i++ {
						c.HarnessFor("pi").Models = append(c.HarnessFor("pi").Models, harness.AgentModel{ID: fmt.Sprintf("extra-%02d", i)})
					}
				case "picker-search-empty":
					v.pickerHarness = "codex"
					v.modelSearch = "no matching model"
				case "picker-favorites-empty":
					v.pickerHarness = "favorites"
					s.Settings.ModelFavorites = nil
				}
				tt := ui.NewTester(v.Render, 840, 560)
				tt.Frame()
				tt.Advance(time.Second)
				tt.Frame()
				if strings.HasPrefix(scene, "picker-") {
					panel, ok := tt.Find(v.texts().ModelPicker())
					if !ok || panel.H != 360 || panel.Y < 0 || panel.Y+panel.H > 560 {
						t.Fatalf("model picker must stay 360px tall within the viewport: %+v", panel)
					}
					list, ok := tt.Find(v.texts().ModelList())
					if !ok || list.H < 280 || list.Y+list.H > panel.Y+panel.H-8 {
						t.Fatalf("model list must fill the available panel space: %+v", list)
					}
					if scene == "picker-many" && v.modelList.AtEnd() {
						t.Fatal("long catalog must overflow inside the scrollable list")
					}
				}
				if scene == "settings-codex-running" {
					if !tt.HasText(v.texts().UpToDate()) || tt.HasText(v.texts().UpdateAfterConversation()) {
						t.Fatal("running Codex settings must show update status without the conversation hint")
					}
				}
				for _, removed := range []string{"Default model", "默认模型", "Model ID", "模型 ID", "Custom", "自定义"} {
					if slices.Contains(tt.Texts(), removed) {
						t.Fatalf("removed model control is visible: %s", removed)
					}
				}
				if scene == "settings-pi" || scene == "settings-droid" {
					if slices.Contains(tt.Texts(), v.texts().Models()) {
						t.Fatal("settings still exposes model selection")
					}
					if !tt.HasText(agentName(v.agentSection) + " " + v.texts().DefaultPermissions()) {
						t.Fatal("default permissions missing")
					}
				}
				if scene == "picker-pi" || scene == "picker-droid" {
					for _, label := range []string{"gpt-6.1-sol", "claude-sonnet-4-6", "custom:Local-0", v.texts().SearchModels(), v.texts().AdjustReasoningEffort()} {
						if _, ok := tt.Find(label); !ok {
							t.Fatalf("missing model control: %s", label)
						}
					}
				}
				names := []string{"Codex", "Pi", "Droid"}
				if strings.HasPrefix(scene, "permissions-") {
					names = []string{v.texts().ReadOnly(), v.texts().FullAccess()}
					if thread.Driver() == "droid" {
						names = append(names, v.texts().AgentFileEdits(), v.texts().AgentDevelopment())
					}
				}
				for _, name := range names {
					b, ok := tt.Find(name)
					if !ok || b.W < 20 || b.X < 0 || b.X+b.W > 840 {
						t.Fatalf("missing/clipped %s: %+v", name, b)
					}
				}
				if output := os.Getenv("MUJICAGENT_AGENTS_SCREENSHOTS"); output != "" {
					if err := os.MkdirAll(output, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(output, lang+"-"+scene+".png"))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tt.Image()); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestAgentThinkingScreenshots(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, driver := range []string{"pi", "droid"} {
			for _, scene := range []string{"levels", "extended", "single", "toggle-on", "toggle-off", "toggle-default", "none", "empty", "stale", "running"} {
				if scene == "extended" && driver == "pi" {
					continue
				}
				t.Run(lang+"-"+driver+"-"+scene, func(t *testing.T) {
					s := model.New()
					s.Settings.Language = lang
					thread := s.AddThread("", "")
					thread.Harness, thread.AgentModel, thread.AgentEffort = driver, "workbuddy/hy4std", "high"
					m := harness.AgentModel{ID: thread.AgentModel, Name: thread.AgentModel, Efforts: []string{"off", "minimal", "low", "medium", "high", "xhigh", "max"}}
					off := "off"
					if driver == "droid" {
						off = "none"
					}
					switch scene {
					case "extended":
						m.Efforts = []string{"none", "dynamic", "off", "minimal", "low", "medium", "high", "xhigh", "max"}
					case "single", "stale":
						m.Efforts = []string{"high"}
						thread.AgentEffort = ""
						if scene == "stale" {
							thread.AgentEffort = "low"
						}
					case "toggle-on", "toggle-off", "toggle-default":
						m.Efforts = []string{off, "high"}
						if scene == "toggle-off" {
							thread.AgentEffort = off
						}
						if scene == "toggle-default" {
							thread.AgentEffort = ""
						}
					case "none":
						m.Efforts = []string{off}
					case "empty":
						m.Efforts = nil
					}
					thread.AgentEffort = m.ResolveEffort(thread.AgentEffort)
					c := controller.New(t.TempDir(), s, nil)
					c.HarnessFor(driver).Models = []harness.AgentModel{m, {ID: "another-model", Name: "Another model"}}
					c.HarnessFor(driver).ModelsChecked = true
					c.Running[thread.ID] = scene == "running"
					v := New(c)
					v.modelPicker, v.pickerHarness = true, driver
					tt := ui.NewTester(v.Render, 840, 560)
					tt.Frame()
					tt.Advance(time.Second)
					tt.Frame()
					tx := v.texts()
					panel, ok := tt.Find(tx.ModelPicker())
					if !ok || panel.H != 360 || panel.Y < 0 || panel.Y+panel.H > 560 {
						t.Fatal("thinking changed picker bounds", panel)
					}
					wantSlider := len(m.Efforts) > 1
					if _, ok := tt.Find(tx.AdjustReasoningEffort()); ok != wantSlider {
						t.Fatal("all harnesses must use the Codex slider")
					}
					if _, ok := tt.Find(tx.ThinkingToggle()); ok {
						t.Fatal("harness-specific toggle returned")
					}
					if scene == "single" || scene == "stale" {
						if !tt.HasText("high") || tt.HasText(tx.AgentDefaultEffort()) {
							t.Fatal("single level invented choices or a default")
						}
					}
					if scene == "empty" {
						if !tt.HasText(tx.ThinkingUnavailable()) || tt.HasText("high") {
							t.Fatal("unsupported model exposes an effort")
						}
					}
					if scene == "none" && !tt.HasText(off) {
						t.Fatal("fixed off value missing")
					}
					for _, label := range []string{tx.AdjustReasoningEffort(), tx.ThinkingToggle(), tx.ThinkingFixed(), "max"} {
						if scene == "extended" && label == "max" {
							continue
						}
						if b, ok := tt.Find(label); ok && (b.X < panel.X || b.Y < panel.Y || b.X+b.W > panel.X+panel.W || b.Y+b.H > panel.Y+panel.H) {
							t.Fatalf("thinking control clipped: %s %+v", label, b)
						}
					}
					if output := os.Getenv("MUJICAGENT_THINKING_SCREENSHOTS"); output != "" {
						if err := os.MkdirAll(output, 0700); err != nil {
							t.Fatal(err)
						}
						f, err := os.Create(filepath.Join(output, lang+"-"+driver+"-"+scene+".png"))
						if err != nil {
							t.Fatal(err)
						}
						defer f.Close()
						if err := png.Encode(f, tt.Image()); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}

func TestAgentThinkingSelection(t *testing.T) {
	for _, driver := range []string{"pi", "droid"} {
		t.Run(driver, func(t *testing.T) {
			s := model.New()
			s.Settings.Language = "en"
			thread := s.AddThread("", "")
			thread.Harness = driver
			thread.AgentEffort = "high"
			c := controller.New(t.TempDir(), s, nil)
			v := New(c)
			c.HarnessFor(driver).Models = []harness.AgentModel{{ID: "compatible", Efforts: []string{"low", "high"}, DefaultEffort: "low"}, {ID: "single", Efforts: []string{"high"}}, {ID: "none"}, {ID: "toggle", Efforts: []string{"none", "high"}, DefaultEffort: "none"}}
			catalog := v.externalModels(driver)
			v.chooseExternalModel(driver, catalog[0])
			if thread.AgentEffort != "high" {
				t.Fatal("lost compatible effort")
			}
			if got := s.Settings.LastModelSelection; got != (model.ModelSelection{Harness: driver, Model: "compatible", Effort: "high"}) {
				t.Fatal("model picker did not remember its final configuration", got)
			}
			v.chooseExternalModel(driver, catalog[2])
			if thread.AgentEffort != "" {
				t.Fatal("retained unsupported effort")
			}
			v.chooseExternalModel(driver, catalog[1])
			if thread.AgentEffort != "high" {
				t.Fatal("did not select sole effort")
			}
			thread.AgentEffort = "none"
			v.chooseExternalModel(driver, catalog[3])
			tt := ui.NewTester(func(ctx *ui.Context) {
				ui.Box(ctx).Disabled(c.Running[thread.ID]).Children(func() { v.modelTuningOptions(ctx, driver, catalog[3], thread) })
			}, 160, 320)
			tt.Frame()
			if err := tt.Click(v.texts().AdjustReasoningEffort()); err != nil {
				t.Fatal(err)
			}
			tt.Key(0, ui.KeyEnd)
			if thread.AgentEffort != "high" {
				t.Fatal("slider did not send advertised enabled value", thread.AgentEffort)
			}
			if s.Settings.LastModelSelection.Effort != "high" {
				t.Fatal("slider did not remember the selected effort")
			}
			tt.Key(0, ui.KeyHome)
			if thread.AgentEffort != "none" {
				t.Fatal("slider changed advertised off value", thread.AgentEffort)
			}
			if s.Settings.LastModelSelection.Effort != "none" {
				t.Fatal("slider did not remember explicit off")
			}
			c.Running[thread.ID] = true
			tt.Frame()
			if err := tt.Click(v.texts().AdjustReasoningEffort()); err != nil {
				t.Fatal(err)
			}
			if thread.AgentEffort != "none" {
				t.Fatal("changed current turn settings")
			}
			c.Running[thread.ID] = false
			catalog[3].DefaultReasoningEffort = ""
			thread.AgentEffort = ""
			tt.Frame()
			if !tt.HasText(v.texts().AgentDefaultEffort()) {
				t.Fatal("unreported default looks like off")
			}
			if err := tt.Click(v.texts().AdjustReasoningEffort()); err != nil {
				t.Fatal(err)
			}
			tt.Key(0, ui.KeyHome)
			if thread.AgentEffort != "none" {
				t.Fatal("cannot explicitly disable from CLI default")
			}
			thread.Harness = "codex"
			thread.AgentEffort = "high"
			v.chooseExternalModel(driver, catalog[0])
			if thread.AgentEffort != "low" {
				t.Fatal("cross-harness effort leaked")
			}
		})
	}
}

func TestCodexPickerRemembersEffortAndSpeed(t *testing.T) {
	s := model.New()
	s.Settings.Language = "en"
	thread := s.AddThread("", "")
	c := controller.New(t.TempDir(), s, nil)
	defer c.Close()
	v := New(c)
	m := codex.Model{
		Model: "codex-model", DefaultReasoningEffort: "low",
		ServiceTiers: []codex.ServiceTier{{ID: "fast", Name: "Fast"}, {ID: "ultrafast", Name: "Ultrafast"}},
		SupportedReasoningEfforts: []struct {
			ReasoningEffort string `json:"reasoningEffort"`
		}{{ReasoningEffort: "low"}, {ReasoningEffort: "high"}},
	}
	c.Models = []codex.Model{m}
	v.chooseModel(m)
	tt := ui.NewTester(func(ctx *ui.Context) {
		ui.Column(ctx).Children(func() { v.effortSlider(ctx, m); v.speedOptions(ctx, m) })
	}, 200, 320)
	tt.Frame()
	if err := tt.Click(v.texts().AdjustReasoningEffort()); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeyEnd)
	for _, tier := range []string{"fast", "ultrafast", ""} {
		label := "Fast"
		if tier != "fast" {
			label = "Ultrafast"
		}
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		want := model.ModelSelection{Harness: "codex", Model: m.Model, Effort: "high", ServiceTier: tier}
		if s.Settings.LastModelSelection != want {
			t.Fatal("picker did not remember reasoning and speed", s.Settings.LastModelSelection)
		}
		thread.Draft = "preserve source"
		c.NewThread("")
		next := s.Thread(s.Selected)
		if next.AgentModel != m.Model || s.Settings.Effort != want.Effort || s.Settings.ServiceTier != tier {
			t.Fatal("new conversation did not inherit the picker configuration")
		}
		// Return to the source without changing the remembered configuration.
		c.Select(thread.ID)
	}
}

func TestModelFavoriteOrderingAndHarnessIdentity(t *testing.T) {
	s := model.New()
	s.Settings.Language = "en"
	s.AddThread("", "")
	c := controller.New(t.TempDir(), s, nil)
	v := New(c)
	models := []codex.Model{{Model: "first"}, {Model: "shared"}}
	s.ToggleModelFavorite(model.ModelFavorite{Harness: "pi", ModelID: "shared"})
	tt := ui.NewTester(func(ctx *ui.Context) { v.modelCatalogPanel(ctx, "droid", models, "", func(codex.Model) {}, nil) }, 500, 300)
	tt.Frame()
	first, _ := tt.Find("first")
	shared, _ := tt.Find("shared")
	if first.Y >= shared.Y {
		t.Fatal("Pi favorite reordered Droid")
	}
	star, ok := tt.Find("Favorite Droid · shared")
	if !ok {
		t.Fatal("favorite action missing")
	}
	tt.ClickAt(star.X+star.W/2, star.Y+star.H/2)
	tt.Frame()
	first, _ = tt.Find("first")
	shared, _ = tt.Find("shared")
	if first.Y <= shared.Y {
		t.Fatal("favorite was not pinned")
	}
	if c.State.Thread(c.State.Selected).Driver() != "codex" {
		t.Fatal("favorite changed active harness")
	}
	v.pickerHarness = "favorites"
	all := ui.NewTester(func(ctx *ui.Context) { v.favoriteModelPanel(ctx) }, 500, 300)
	all.Frame()
	for _, label := range []string{"Pi · shared", "Droid · shared"} {
		if _, ok := all.Find(label); !ok {
			t.Fatal("missing distinct tuple", label)
		}
	}
	s.ToggleModelFavorite(model.ModelFavorite{Harness: "pi", ModelID: "shared"})
	all.Frame()
	if _, ok := all.Find("Pi · shared"); ok {
		t.Fatal("unfavorite remains visible")
	}
	if _, ok := all.Find("Droid · shared"); !ok {
		t.Fatal("unfavorite removed other harness")
	}
	target, _ := all.Find("Droid · shared")
	all.ClickAt(target.X+target.W/2, target.Y+target.H/2)
	all.Frame()
	if thread := s.Thread(s.Selected); thread.Driver() != "droid" || thread.AgentModel != "shared" {
		t.Fatal("favorite selected the wrong harness/model")
	}

	s.Settings.Effort, s.Settings.ServiceTier = "ultra", "fast"
	v.chooseModel(codex.Model{Model: "codex-favorite", DefaultReasoningEffort: "medium", SupportedReasoningEfforts: []struct {
		ReasoningEffort string `json:"reasoningEffort"`
	}{{ReasoningEffort: "medium"}}})
	if s.Thread(s.Selected).Driver() != "codex" || s.Settings.Model != "codex-favorite" || s.Settings.Effort != "medium" || s.Settings.ServiceTier != "" {
		t.Fatal("Codex favorite did not apply compatible model options after handoff")
	}

}

func TestModelPickerWithoutCodexCatalog(t *testing.T) {
	s := model.New()
	s.Settings.Language = "en"
	s.AddThread("", "")
	c := controller.New(t.TempDir(), s, nil)
	v := New(c)
	tt := ui.NewTester(func(ctx *ui.Context) { v.modelSelector(ctx) }, 600, 400)
	tt.Frame()
	button, ok := tt.Find(v.texts().ModelAndEffort())
	if !ok {
		t.Fatal("missing model selector")
	}
	tt.ClickAt(button.X+button.W/2, button.Y+button.H/2)
	tt.Frame()
	if !v.modelPicker {
		t.Fatal("empty Codex catalog blocked the model picker")
	}
	for _, name := range []string{"Pi", "Droid"} {
		if _, ok := tt.Find(name); !ok {
			t.Fatalf("%s is unavailable without Codex models", name)
		}
	}

}

func codexEfforts(values ...string) []struct {
	ReasoningEffort string `json:"reasoningEffort"`
} {
	result := make([]struct {
		ReasoningEffort string `json:"reasoningEffort"`
	}, 0, len(values))
	for _, value := range values {
		result = append(result, struct {
			ReasoningEffort string `json:"reasoningEffort"`
		}{ReasoningEffort: value})
	}
	return result
}

func TestFavoritePickerTuning(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, driver := range []string{"codex", "pi", "droid"} {
			t.Run(lang+"-"+driver, func(t *testing.T) {
				s := model.New()
				s.Settings.Language = lang
				thread := s.AddThread("", "")
				c := controller.New(t.TempDir(), s, nil)
				c.Models = []codex.Model{{Model: "gpt-6.1-sol", DefaultReasoningEffort: "medium", SupportedReasoningEfforts: codexEfforts("low", "medium", "high"), ServiceTiers: []codex.ServiceTier{{ID: "fast", Name: "Fast", Description: "1.5x speed"}, {ID: "ultrafast", Name: "Ultra fast"}}}}
				for _, id := range []string{"pi", "droid"} {
					h := c.HarnessFor(id)
					h.ModelsChecked = true
					h.Models = []harness.AgentModel{{ID: "workbuddy/hy4std", Name: "WorkBuddy", Efforts: []string{"off", "low", "medium", "high"}, DefaultEffort: "medium"}}
				}
				s.Settings.ModelFavorites = []model.ModelFavorite{{Harness: "codex", ModelID: "gpt-6.1-sol"}, {Harness: "pi", ModelID: "workbuddy/hy4std"}, {Harness: "droid", ModelID: "workbuddy/hy4std"}}
				if driver == "codex" {
					s.Settings.Model, s.Settings.Effort = "gpt-6.1-sol", "medium"
				} else {
					thread.Harness, thread.AgentModel, thread.AgentEffort = driver, "workbuddy/hy4std", "medium"
				}
				v := New(c)
				v.modelPicker, v.pickerHarness = true, "favorites"
				tt := ui.NewTester(v.Render, 840, 560)
				tt.Frame()
				tt.Advance(time.Second)
				tt.Frame()
				tx := v.texts()
				panel, ok := tt.Find(tx.ModelPicker())
				if !ok || panel.H != 360 || panel.Y < 0 || panel.Y+panel.H > 560 {
					t.Fatalf("favorites tuning changed picker bounds: %+v", panel)
				}
				list, ok := tt.Find(tx.ModelList())
				if !ok || list.H < 280 || list.W > 280 {
					t.Fatalf("favorites list did not shrink for the tuning pane: %+v", list)
				}
				slider, ok := tt.Find(tx.AdjustReasoningEffort())
				if !ok || slider.X <= list.X+list.W || slider.X+slider.W > panel.X+panel.W || slider.Y+slider.H > panel.Y+panel.H {
					t.Fatalf("favorites tuning slider missing or clipped: %+v", slider)
				}
				if !tt.HasText("medium") {
					t.Fatal("favorites tuning lost the active effort")
				}
				_, fast := tt.Find("Fast")
				_, ultra := tt.Find("Ultra fast")
				if driver == "codex" && (!fast || !ultra) {
					t.Fatal("Codex favorites lost the speed options")
				}
				if driver != "codex" && (fast || ultra) {
					t.Fatal("harness favorites exposed Codex speed options")
				}
				if output := os.Getenv("MUJICAGENT_AGENTS_SCREENSHOTS"); output != "" {
					if err := os.MkdirAll(output, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(output, "favorites-tuning-"+lang+"-"+driver+".png"))
					if err != nil {
						t.Fatal(err)
					}
					defer f.Close()
					if err := png.Encode(f, tt.Image()); err != nil {
						t.Fatal(err)
					}
				}
				if err := tt.Click(tx.AdjustReasoningEffort()); err != nil {
					t.Fatal(err)
				}
				tt.Key(0, ui.KeyEnd)
				tt.Frame()
				if driver == "codex" {
					if s.Settings.Effort != "high" {
						t.Fatalf("favorites slider did not update Codex effort: %q", s.Settings.Effort)
					}
				} else if thread.AgentEffort != "high" {
					t.Fatalf("favorites slider did not update %s effort: %q", driver, thread.AgentEffort)
				}
			})
		}
	}
}

func TestCompactionStatistics(t *testing.T) {
	s := model.New()
	s.Settings.Language = "zh-Hans"
	c := controller.New(t.TempDir(), s, nil)
	defer c.Close()
	v := New(c)
	for _, tc := range []struct {
		before, after int64
		known         bool
		status, want  string
	}{
		{120000, 24000, true, "completed", "已压缩上下文：120k → 24k（减少 80%）"},
		{1234, 567, true, "completed", "已压缩上下文：1.2k → 567（减少 54.1%）"},
		{1000, 1000, true, "completed", "已压缩上下文：1k → 1k（减少 0%）"},
		{1000, 1200, true, "completed", "已压缩上下文：1k → 1.2k（减少 -20%）"},
		{0, 0, true, "completed", "已压缩上下文"},
		{1000, -1, true, "completed", "已压缩上下文"},
		{120000, 0, false, "completed", "已压缩上下文"},
		{120000, 24000, true, "failed", "上下文压缩失败"},
		{120000, 24000, true, "interrupted", "上下文压缩已取消"},
		{120000, 24000, true, "inProgress", "上下文压缩状态未知"},
	} {
		item := model.Item{Status: tc.status, Compaction: model.CompactionStats{Before: tc.before, After: tc.after, BeforeKnown: true, AfterKnown: tc.known}}
		label, _, active := v.compactionLabel(&item, false)
		if label != tc.want || active {
			t.Fatalf("%+v: %q", tc, label)
		}
	}
}

func TestCompactionPreservesHistoryScroll(t *testing.T) {
	s := model.New()
	thread := s.AddThread("", "")
	items := []model.Item{}
	for i := range 30 {
		items = append(items, model.Item{ID: fmt.Sprint(i), Kind: "agentMessage", Status: "completed", Text: strings.Repeat("History message. ", 8)})
	}
	items = append(items, model.Item{ID: "compact", Kind: "contextCompaction", Status: "inProgress"})
	thread.Turns = []model.Turn{{Status: "inProgress", Items: items}}
	c := controller.New(t.TempDir(), s, nil)
	defer c.Close()
	v := New(c)
	tt := ui.NewTester(func(ctx *ui.Context) { ui.Column(ctx).Fill().Children(func() { v.timeline(ctx, thread) }) }, 600, 400)
	tt.Frame()
	scroll := v.scroll[thread.ID]
	scroll.Y = 100
	tt.Frame()
	before := scroll.Y
	thread.Turns[0].Items[len(items)-1].Status = "completed"
	thread.Turns[0].Items = append(thread.Turns[0].Items, model.Item{ID: "next", Kind: "agentMessage", Text: "Continue"})
	tt.Frame()
	if scroll.Y != before {
		t.Fatalf("history reader moved: %v → %v", before, scroll.Y)
	}
}
