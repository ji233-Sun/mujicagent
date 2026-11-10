package view

import (
	"encoding/json"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func TestFooterModelVersions(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		s := model.New()
		s.Settings.Language, s.Settings.Effort = language, ""
		thread := s.AddThread("", "")
		thread.Draft, thread.PermissionProfile = "Queue the next task.", ":approve-for-me"
		ctrl := controller.New(t.TempDir(), s, nil)
		ctrl.Connection, ctrl.Running[thread.ID] = "Connected", true
		v := New(ctrl)
		tt := ui.NewTester(v.Render, 840, 560)
		tt.SetLocale(language)
		for _, version := range []string{"2026-alpha", "2026-beta"} {
			s.Settings.Model = "provider-example/model-family-with-a-long-prefix-" + version
			tt.Frame()
			tt.Advance(time.Second)
			tt.Frame()
			if !tt.HasText(s.Settings.Model) || !strings.Contains(tailEllipsis(s.Settings.Model, 18), version) {
				t.Fatal("model identity or distinguishing version suffix lost")
			}
			saveDesignFrame(t, tt, "model-version-"+language+"-"+version)
		}
		ctrl.Close()
	}
}

func TestFooterPermissionLegibility(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		for _, profile := range []string{":read-only", ":workspace", ":approve-for-me", ":danger-full-access"} {
			t.Run(language+profile, func(t *testing.T) {
				s := model.New()
				s.Settings.Language = language
				s.Settings.Model = "a-very-long-provider-and-model-identifier-with-a-version-suffix"
				thread := s.AddThread("", "")
				thread.Title, thread.Draft = "Permission visibility", "Queue the next task."
				thread.PermissionProfile = profile
				ctrl := controller.New(t.TempDir(), s, nil)
				defer ctrl.Close()
				ctrl.Connection, ctrl.Running[thread.ID] = "Connected", true
				v := New(ctrl)
				tt := ui.NewTester(v.Render, 840, 560)
				tt.SetLocale(language)
				tt.Advance(time.Second)
				tt.Frame()
				label := v.permissionName(profile)
				bounds, ok := tt.Find(label)
				width, _ := ui.ShapeText(label, ui.Font{Size: 13}, 0).Size()
				if !ok || bounds.W+1 < width {
					t.Fatalf("permission label was squeezed by model/queue controls: %s %+v need %.2f", label, bounds, width)
				}
				for _, action := range []string{v.texts().ModelAndEffort(), v.texts().Stop(), v.texts().QueueAdd()} {
					r, found := tt.Find(action)
					if !found || r.W <= 0 || r.X < 246 || r.X+r.W > 832 {
						t.Fatalf("footer action clipped: %s %+v", action, r)
					}
				}
				saveDesignFrame(t, tt, "footer-"+language+"-"+profile[1:])
			})
		}
	}
}

// Visual acceptance uses direct state only, never input simulation or a live CLI.
func TestWorkspaceLayout(t *testing.T) {
	for _, language := range []string{"en", "zh-Hans"} {
		for _, size := range [][2]int{{1366, 858}, {1140, 780}, {840, 560}} {
			for _, scene := range []string{"conversation", "tools", "running", "failed", "reasoning", "approval", "empty", "appearance"} {
				t.Run(fmt.Sprintf("%s-%s-%dx%d", scene, language, size[0], size[1]), func(t *testing.T) {
					s := model.New()
					s.Settings.Language = language
					s.Settings.Model = "gpt-6.1-sol"
					s.Settings.Effort = "high"
					s.Projects = []model.Project{{ID: "workspace", Name: "MujicAgent", Expanded: true}, {ID: "docs", Name: "Documentation", Expanded: true}}
					thread := s.AddThread("workspace", "")
					thread.Title = "Workspace layout review"
					thread.Draft = "Keep the existing palette and interactions."
					prompt := "Review the workspace layout without adding features."
					reply := "## A calmer workspace\n\nThe layout should keep the conversation readable while leaving enough room for project navigation and existing controls.\n\n- **Reading width:** keep long paragraphs easy to scan.\n- **Consistent spacing:** align the conversation and composer.\n- **Clear feedback:** keep progress and approval states visible.\n\n### Verification\n\nUse isolated data and CPU-rendered screenshots. Native window behavior remains outside this check."
					if language == "zh-Hans" {
						thread.Title = "工作区布局审查"
						thread.Draft = "保留现有配色与交互，不增加功能。"
						prompt = "检查工作区布局，不增加新功能。"
						reply = "## 更从容的工作区\n\n布局应当让对话易于阅读，同时为项目导航和已有控件保留足够空间。\n\n- **阅读宽度：** 让长段落更容易浏览。\n- **一致间距：** 对齐对话与输入区域。\n- **明确反馈：** 保持进度和审批状态清晰可见。\n\n### 验证\n\n使用隔离数据与 CPU 离屏截图。系统原生窗口行为不在本次验证范围内。"
					}
					thread.Turns = []model.Turn{{ID: "review", Prompt: prompt, Status: "completed", Items: []model.Item{{ID: "reply", Kind: "agentMessage", Text: reply, Status: "completed"}}}}
					for _, title := range []string{"Polish the message composer", "Review component spacing", "A longer conversation title that must truncate safely"} {
						other := s.AddThread("workspace", "")
						other.Title = title
						other.Turns = []model.Turn{{Prompt: "Review", Status: "completed"}}
					}
					doc := s.AddThread("docs", "")
					doc.Title = "Usage guide"
					s.Selected = thread.ID
					ctrl := controller.New(t.TempDir(), s, nil)
					defer ctrl.Close()
					ctrl.Connection = "Connected"
					v := New(ctrl)
					if scene == "tools" || scene == "running" || scene == "failed" || scene == "reasoning" {
						thread.Turns[0].Items = []model.Item{
							{ID: "intro", Kind: "agentMessage", Text: "Checking the existing layout and verification evidence.", Status: "completed"},
							{ID: "read", Kind: "toolCall", Status: "completed", MCP: &model.MCPCall{Tool: "read", Arguments: `{"path":"README.md"}`, Result: "MujicAgent\nNative UI · Project conversations\n\nKeep the existing behavior and visual palette.\nUse isolated data for all verification."}},
							{ID: "test", Kind: "commandExecution", Text: "$ go test ./internal/view\nok   internal/view   0.42s", Status: "completed"},
							{ID: "reply", Kind: "agentMessage", Text: "The checks preserve existing interactions. No live messages were sent.", Status: "completed"},
						}
						*v.openState("activity-" + thread.ID + "-0-1") = true
						*v.openState(thread.ID + "-0-1-0") = true
						if scene == "running" || scene == "reasoning" {
							ctrl.Running[thread.ID] = true
							thread.Turns[0].Status = "inProgress"
							thread.Turns[0].Items[2].Status = "inProgress"
						}
						if scene == "failed" {
							thread.Turns[0].Items[2].Status = "failed"
						}
						if scene == "reasoning" {
							thread.Turns[0].Items[2] = model.Item{ID: "reasoning", Kind: "reasoning", Text: "COLLAPSED_REASONING_CONTENT", Status: "inProgress"}
						}
					}
					if scene == "approval" {
						ctrl.Running[thread.ID] = true
						ctrl.Requests = []*codex.Request{{ID: json.RawMessage(`"layout-approval"`), ThreadID: thread.ID, Method: "item/commandExecution/requestApproval", Command: "go test ./internal/view", Reason: "Verify the workspace layout with isolated test data.", AvailableDecisions: []json.RawMessage{json.RawMessage(`"accept"`), json.RawMessage(`"cancel"`)}}}
					}
					if scene == "empty" {
						thread.Title, thread.Draft, thread.Turns = "", "", nil
					}
					if scene == "appearance" {
						v.Settings, v.section = true, "Appearance"
					}
					tt := ui.NewTester(v.Render, size[0], size[1])
					tt.SetLocale(language)
					tt.SetDark(false)
					tt.Frame()
					tt.Advance(time.Second)
					tt.Frame()
					tx := v.texts()
					for _, label := range []string{tx.Message(), tx.Send(), tx.ModelAndEffort()} {
						if scene == "running" || scene == "reasoning" || scene == "approval" {
							if label == tx.Send() {
								label = tx.QueueAdd()
							}
						}
						bounds, ok := tt.Find(label)
						if !ok || bounds.X < 246 || bounds.Y < 0 || bounds.W <= 0 || bounds.H <= 0 || bounds.X+bounds.W > float32(size[0]) || bounds.Y+bounds.H > float32(size[1]) {
							t.Fatalf("%s missing or outside workspace: %+v", label, bounds)
						}
					}
					if scene == "conversation" {
						bodyText := "The layout should keep the conversation readable while leaving enough room for project navigation and existing controls."
						if language == "zh-Hans" {
							bodyText = "布局应当让对话易于阅读，同时为项目导航和已有控件保留足够空间。"
						}
						body, ok := tt.Find(bodyText)
						if !ok || body.W > conversationBodyWidth {
							t.Fatalf("assistant body exceeds reading width: %+v", body)
						}
					}
					composer, _ := tt.Find(tx.Message())
					if composer.H != composerEditorHeight || composer.W > conversationMaxWidth-78 {
						t.Fatalf("composer lost compact, bounded layout: %+v", composer)
					}
					if scene == "running" {
						queue, ok := tt.Find(tx.QueueAdd())
						if !ok || queue.W <= 30 {
							t.Fatalf("queue action needs a visible text label, not the idle send icon: %+v", queue)
						}
						stop, ok := tt.Find(tx.Stop())
						if !ok || stop.W != 30 || stop.H != 30 || !tt.HasText("· "+tx.McpRunning()) || !tt.HasText("$ go test ./internal/view") {
							t.Fatalf("running feedback or distinct stop action missing: %+v", stop)
						}
					}
					if scene == "tools" || scene == "running" || scene == "failed" {
						if _, ok := tt.Find(tx.McpCompleted()); !ok {
							t.Fatal("completed tool status missing")
						}
					}
					if scene == "failed" {
						if _, ok := tt.Find(tx.Failed()); !ok {
							t.Fatal("failed tool status missing")
						}
					}
					if scene == "reasoning" && tt.HasText("COLLAPSED_REASONING_CONTENT") {
						t.Fatal("status row must not reveal collapsed reasoning")
					}
					if scene == "approval" && (!tt.HasText(tx.ResponseRequired()) || tt.HasText("· "+tx.McpRunning())) {
						t.Fatal("pending response must not be labelled as running")
					}
					if scene == "appearance" && !tt.HasText(tx.Language()) {
						t.Fatal("appearance setting missing")
					}
					if scene == "approval" && !tt.HasText("go test ./internal/view") {
						t.Fatal("approval command missing")
					}
					if out := os.Getenv("MUJICAGENT_WORKSPACE_SCREENSHOTS"); out != "" {
						if err := os.MkdirAll(out, 0700); err != nil {
							t.Fatal(err)
						}
						file, err := os.Create(filepath.Join(out, fmt.Sprintf("%s-%s-%dx%d.png", scene, language, size[0], size[1])))
						if err != nil {
							t.Fatal(err)
						}
						err = png.Encode(file, tt.Image())
						closeErr := file.Close()
						if err != nil || closeErr != nil {
							t.Fatalf("write screenshot: %v, %v", err, closeErr)
						}
					}
				})
			}
		}
	}
}
