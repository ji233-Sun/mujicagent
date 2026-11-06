package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"github.com/ji233-Sun/mujicagent/internal/view"
)

func TestNativeView(t *testing.T) {
	state := model.New()
	ctrl := controller.New(t.TempDir(), state, nil)
	v := view.New(ctrl)
	tt := ui.NewTester(func(ctx *ui.Context) {
		v.Render(ctx)
		if ctx.Theme().Dark {
			t.Error("app must remain light regardless of the system or saved appearance")
		}
	}, 1140, 780)
	for _, saved := range []string{"Dark", "System", "Light"} {
		state.Settings.Appearance = saved
		tt.SetDark(true)
		tt.Frame()
	}
	tt.SetDark(false)
	if tt.HasText("搜索会话") {
		t.Fatal("conversation search should not be displayed")
	}
	// Clicking the remaining title text must not create a conversation.
	_ = tt.Click("新对话")
	tt.Key(ui.Cmd, ui.KeyN)
	tt.Key(ui.Ctrl, ui.KeyN)
	if len(state.Threads) != 0 {
		t.Fatal("shortcuts must not create projectless conversations")
	}
	ctrl.AddProject(t.TempDir())
	tt.Frame()
	if len(state.Threads) != 1 {
		t.Fatal("new thread missing")
	}
	if tt.HasText("New conversation") || !tt.HasText("新对话") {
		t.Fatal("new conversation placeholder should use Chinese by default")
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("你好，Codex")
	if state.Threads[0].Draft != "你好，Codex" {
		t.Fatal("draft was not bound")
	}
	ctrl.Connection = "Connected"
	ctrl.Version = "codex-cli 0.167.0"
	tt.Frame()
	send, _ := tt.Find("发送")
	minX, minY, maxX, maxY := int(send.X+send.W), int(send.Y+send.H), -1, -1
	for y := int(send.Y); y < int(send.Y+send.H); y++ {
		for x := int(send.X); x < int(send.X+send.W); x++ {
			p := tt.Image().RGBAAt(x, y)
			dx, dy := float64(x)+0.5-float64(send.X+send.W/2), float64(y)+0.5-float64(send.Y+send.H/2)
			// Include antialiased white strokes, excluding the background outside the circle.
			if dx*dx+dy*dy < 121 && p.R > 100 && p.G > 170 && p.B > 150 {
				minX, minY, maxX, maxY = min(minX, x), min(minY, y), max(maxX, x), max(maxY, y)
			}
		}
	}
	if maxX < 0 || math.Abs(float64(minX+maxX+1)/2-float64(send.X+send.W/2)) > 1 || math.Abs(float64(minY+maxY+1)/2-float64(send.Y+send.H/2)) > 1 {
		t.Fatalf("send icon is not centered: button=%+v icon=(%d,%d)-(%d,%d)", send, minX, minY, maxX, maxY)
	}
	composer, _ := tt.Find("消息")
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("Codex 路径") {
		t.Fatal("agent settings missing")
	}
	if tt.HasText("升级 Codex") || tt.HasText("检查更新") || !tt.HasText(ctrl.Version) {
		t.Fatal("settings should display the current version without update controls")
	}
	tt.Advance(220 * time.Millisecond)
	panel, ok := tt.Find("设置面板")
	close, hasClose := tt.Find("关闭设置")
	behind, hasComposer := tt.Find("消息")
	if !ok || panel.X <= 0 || panel.Y <= 0 || panel.W >= 1140 || panel.H >= 780 || !hasComposer || behind != composer {
		t.Fatalf("settings must float over the unchanged conversation: panel=%+v composer=%+v", panel, behind)
	}
	if !hasClose || close.X < panel.X+panel.W-60 || close.Y > panel.Y+50 || tt.HasText("返回对话") {
		t.Fatalf("settings must have only a top-right close control: %+v", close)
	}
	tt.Key(ui.Cmd, ui.KeyN)
	if len(state.Threads) != 1 || !v.Settings {
		t.Fatal("background shortcuts should not run through settings")
	}
	if err := tt.Click("关闭设置"); err != nil {
		t.Fatal(err)
	}
	if v.Settings || tt.HasText("Codex 路径") || state.Threads[0].Draft != "你好，Codex" {
		t.Fatal("settings did not close or conversation draft was lost")
	}
	loaded, err := model.Load(filepath.Join(ctrl.Dir, "state.json"))
	if err != nil || loaded.Threads[0].Draft != "你好，Codex" {
		t.Fatalf("closing settings did not preserve saved state: %v", err)
	}
	if err := tt.Click("设置"); err != nil {
		t.Fatal(err)
	}
	tt.SetSize(840, 560)
	tt.Advance(220 * time.Millisecond)
	panel, _ = tt.Find("设置面板")
	if panel.X < 0 || panel.Y < 0 || panel.X+panel.W > 840 || panel.Y+panel.H > 560 {
		t.Fatalf("settings overflowed the smallest window: %+v", panel)
	}
	tt.Key(0, ui.KeyEscape)
	if v.Settings || tt.HasText("Codex 路径") {
		t.Fatal("Escape did not close settings")
	}
}

func TestDiagnosticLogsAndErrorAlert(t *testing.T) {
	s := model.New()
	c := controller.New(t.TempDir(), s, nil)
	c.AddProject(t.TempDir())
	v := view.New(c)
	tt := ui.NewTester(v.Render, 840, 560)
	composer, _ := tt.Find("消息")
	diagnostic := "Codex requested an unsupported action: mcpServer/elicitation/request"
	c.LogDiagnostic(diagnostic)
	c.LogNotice("The turn is starting. Try Stop again in a moment.")
	tt.Frame()
	if tt.HasText("mcpServer/elicitation/request") || tt.HasText("正在开始对话") {
		t.Fatal("general and diagnostic logs must not appear on the conversation")
	}
	c.ReportError("Codex executable not found")
	tt.Frame()
	alert, ok := tt.Find("错误通知")
	close, closeOK := tt.Find("关闭提示")
	after, _ := tt.Find("消息")
	if !ok || !closeOK || composer != after || alert.X < 246 || alert.X+alert.W > 840 || alert.Y+alert.H > 560 || close.X < alert.X+alert.W-60 {
		t.Fatalf("error must float over the main pane with a top-right close: alert=%+v close=%+v", alert, close)
	}
	if !tt.HasText("发生错误") || !tt.HasText("Codex executable not found") {
		t.Fatal("important errors must be visible without developer mode")
	}
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Advance(250 * time.Millisecond)
	}
	click("设置")
	if _, ok := tt.Find("错误通知"); ok {
		t.Fatal("error alert must not cover settings")
	}
	click("日志")
	if tt.HasText("mcpServer/elicitation/request") || !tt.HasText("Codex executable not found") {
		t.Fatal("logs must hide diagnostics by default while preserving important errors")
	}
	click("切换开发者模式")
	if !s.Settings.DeveloperMode || !tt.HasText("mcpServer/elicitation/request") {
		t.Fatal("developer mode must reveal diagnostic logs")
	}
	loaded, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil || !loaded.Settings.DeveloperMode {
		t.Fatalf("developer mode not saved: %v", err)
	}
	data, err := os.ReadFile(filepath.Join(c.Dir, "state.json"))
	if err != nil || bytes.Contains(data, []byte("mcpServer/elicitation/request")) {
		t.Fatal("logs must not be persisted with conversations")
	}
	click("切换开发者模式")
	if tt.HasText("mcpServer/elicitation/request") {
		t.Fatal("turning developer mode off must hide existing diagnostic logs")
	}
	click("关闭设置")
	click("关闭提示")
	if _, ok := tt.Find("错误通知"); ok || c.Error != "" || len(c.Logs) != 3 {
		t.Fatal("dismissal must clear only the floating error")
	}
	click("设置")
	s.Settings.Language = "en"
	tt.SetLocale("en")
	tt.Frame()
	if !tt.HasText("Developer mode") || !tt.HasText("Codex executable not found") {
		t.Fatal("logs must remain available and localized after dismissal")
	}
}

func TestAppUpdatesView(t *testing.T) {
	s := model.New()
	c := controller.New(t.TempDir(), s, nil)
	c.AppUpdates = controller.AppUpdateState{Version: "0.1.0", Enabled: true, AvailableVersion: "0.1.1", Releases: []model.Release{
		{Tag: "v0.1.1", Notes: "NEW_RELEASE_NOTES", Published: time.Now()},
		{Tag: "v0.1.0", Notes: "OLDER_RELEASE_NOTES"},
	}}
	tt := ui.NewTester(view.New(c).Render, 840, 560)
	if err := tt.Click("发现新版本"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	if !tt.HasText("新版本 0.1.1") || !tt.HasText("下载更新") || !tt.HasText("NEW_RELEASE_NOTES") || tt.HasText("OLDER_RELEASE_NOTES") {
		t.Fatal("update entry or default release-note disclosure is incorrect")
	}
	if err := tt.Click("v0.1.0"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	if !tt.HasText("OLDER_RELEASE_NOTES") {
		t.Fatal("cannot view historical release notes")
	}
	c.AppUpdates.Installing, c.AppUpdates.Downloaded, c.AppUpdates.Total = true, 1e6, 2e6
	tt.Frame()
	if !tt.HasText("正在下载…") || !tt.HasText("1.0 / 2.0 MB") || tt.HasText("下载更新") {
		t.Fatal("download state is missing")
	}
	c.AppUpdates.Installing, c.AppUpdates.Ready = false, true
	c.Running["test"] = true
	tt.Frame()
	if !tt.HasText("重启并更新") || !tt.HasText("对话结束后可安装并重启") {
		t.Fatal("restart state must explain active conversations")
	}
	s.Settings.Language = "en"
	tt.SetLocale("en")
	tt.Frame()
	if !tt.HasText("Restart to update") || !tt.HasText("Release notes") {
		t.Fatal("updates are not localized")
	}
	panel, _ := tt.Find("Settings panel")
	restart, ok := tt.Find("Restart to update")
	if !ok || restart.X < panel.X || restart.X+restart.W > panel.X+panel.W {
		t.Fatal("update action overflows compact settings")
	}
	c.AppUpdates = controller.AppUpdateState{Version: "0.1.0", Enabled: true, CheckedAt: time.Now()}
	tt.Frame()
	if !tt.HasText("No releases yet") || tt.HasText("Download update") {
		t.Fatal("empty repository is not handled")
	}
	c.AppUpdates.Releases = []model.Release{{Tag: "v0.1.5"}}
	c.AppUpdates.Checking = true
	tt.Frame()
	if !tt.HasText("Checking…") || tt.HasText("You’re up to date") {
		t.Fatal("checking must replace the previous result")
	}
	c.AppUpdates.Checking = false
	c.AppUpdates.Error = "release v0.1.5 has update manifest version 0.1.4"
	tt.Frame()
	if !tt.HasText("Update failed. Please try again.") || tt.HasText("You’re up to date") || tt.HasText("Download update") {
		t.Fatal("failed check must not claim the app is current or offer a stale update")
	}
	s.Settings.Language = "zh-Hans"
	tt.SetLocale("zh-Hans")
	tt.Frame()
	if !tt.HasText("更新失败，请重试") || tt.HasText("已是最新版本") {
		t.Fatal("failed update status must be localized")
	}
}

func TestManualSettleFromHover(t *testing.T) {
	s := model.New()
	c := controller.New(t.TempDir(), s, nil)
	c.AddProject(t.TempDir())
	thread := s.Thread(s.Selected)
	thread.Title = "设计一个原生界面"
	v := view.New(c)
	tt := ui.NewTester(v.Render, 1140, 780)
	if err := tt.Click(s.Projects[0].Name); err != nil {
		t.Fatal(err)
	}
	box, ok := tt.Find(thread.Title)
	if !ok {
		t.Fatal("thread not visible")
	}
	if box.H > 36 {
		t.Fatalf("thread should occupy one compact row: %+v", box)
	}
	tt.Move(box.X+10, box.Y+box.H/2)
	if err := tt.Click("收纳"); err != nil {
		t.Fatal(err)
	}
	if !thread.Settled {
		t.Fatal("manual settle not applied")
	}
	next := s.Thread(s.Selected)
	if next == nil || next.ID == thread.ID || next.ProjectID != thread.ProjectID || next.Draft != "" || len(next.Turns) != 0 {
		t.Fatal("settle did not open a blank conversation in the same project")
	}
	// Move off the new conversation's hover action before opening the shelf.
	tt.Move(500, 100)
	if err := tt.Click("收纳"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	if !tt.HasText(thread.Title) {
		t.Fatal("settled thread not recoverable")
	}
	if err := tt.RightClick(thread.Title); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("恢复会话"); err != nil {
		t.Fatal(err)
	}
	if thread.Settled {
		t.Fatal("context-menu restore failed")
	}
	tt.Advance(300 * time.Millisecond)
	if err := tt.RightClick(thread.Title); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("收纳"); err != nil {
		t.Fatal(err)
	}
	if !thread.Settled {
		t.Fatal("context-menu settle failed")
	}
}

func TestDefaultPermissionsSettings(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Older snapshots have neither an app default nor a per-thread selection.
	if err := os.WriteFile(path, []byte(`{"Version":1,"Settings":{},"Threads":[{"ID":"old"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c := controller.New(dir, s, nil)
	if c.PermissionID() != ":approve-for-me" || s.Thread("old").PermissionProfile != ":approve-for-me" {
		t.Fatal("unconfigured permissions must default to auto-review")
	}
	s.Selected = "old"
	s.Thread("old").PermissionProfile = ":read-only"
	v := view.New(c)
	tt := ui.NewTester(v.Render, 1140, 780)
	for _, label := range []string{"设置", "自动审批", "完全访问"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Advance(250 * time.Millisecond)
	}
	if s.Settings.DefaultPermissionProfile != ":danger-full-access" {
		t.Fatal("settings selection did not update default permissions")
	}
	loaded, err := model.Load(path)
	if err != nil || loaded.Settings.DefaultPermissionProfile != ":danger-full-access" {
		t.Fatalf("default permissions were not persisted: %v", err)
	}
	if loaded.Thread("old").PermissionProfile != ":read-only" {
		t.Fatal("changing defaults changed an existing conversation")
	}
	c.AddProject(t.TempDir())
	if c.PermissionID() != ":danger-full-access" {
		t.Fatal("new conversations must use the configured default")
	}
	s.Settings.DefaultPermissionProfile = ":read-only"
	if c.PermissionID() != ":danger-full-access" {
		t.Fatal("existing conversation must retain its permissions")
	}
	s.Settings.Language = "en"
	tt.SetLocale("en")
	tt.Frame()
	if !tt.HasText("Codex default permissions") {
		t.Fatal("default permissions setting is not localized")
	}
}

func TestNativePermissions(t *testing.T) {
	s := model.New()
	thread := s.AddThread("", t.TempDir())
	thread.ServerID = "permissions-thread"
	thread.PermissionProfile = ":workspace"
	c := controller.New(t.TempDir(), s, nil)
	c.Connection = "Connected"
	c.Permissions = []codex.PermissionProfile{{ID: ":read-only", Allowed: true}, {ID: ":workspace", Allowed: true}, {ID: ":approve-for-me", Allowed: true}, {ID: ":danger-full-access", Allowed: true}, {ID: "managed-profile", Allowed: false}}
	tt := ui.NewTester(view.New(c).Render, 1140, 780)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Advance(250 * time.Millisecond)
	}
	click("权限")
	if tt.HasText("调整从下一轮生效") {
		t.Fatal("unchanged permissions must not show a pending-change hint")
	}
	click("自动审批")
	storedAuto, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil || storedAuto.Thread(thread.ID).PermissionProfile != ":approve-for-me" {
		t.Fatalf("auto-review choice was not persisted: %v", err)
	}
	click("权限")
	click("默认权限")
	click("权限")
	click("managed-profile")
	if c.PermissionID() != ":workspace" {
		t.Fatal("managed policy restriction was ignored")
	}
	click("只读")
	if thread.PermissionProfile != ":read-only" {
		t.Fatal("read-only selection was not applied")
	}
	click("权限")
	click("完全访问")
	if thread.PermissionProfile != ":read-only" || !tt.HasText("启用完全访问？") {
		t.Fatal("full access must wait for explicit confirmation")
	}
	click("取消")
	click("权限")
	click("完全访问")
	click("启用完全访问")
	if thread.PermissionProfile != ":danger-full-access" {
		t.Fatal("full access confirmation was not applied")
	}
	stored, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil || stored.Thread(thread.ID).PermissionProfile != ":danger-full-access" {
		t.Fatalf("permissions not persisted: %v", err)
	}
	other := s.AddThread("", t.TempDir())
	tt.Frame()
	if c.PermissionID() != ":approve-for-me" {
		t.Fatal("permissions leaked into another conversation")
	}
	c.Select(thread.ID)
	tt.Frame()
	if c.PermissionID() != ":danger-full-access" {
		t.Fatal("conversation permissions were lost")
	}
	c.Running[thread.ID] = true
	tt.Frame()
	click("权限")
	if !tt.HasText("managed-profile") || tt.HasText("调整从下一轮生效") {
		t.Fatal("running turn without a permission change must not show a pending-change hint")
	}
	click("只读")
	if thread.PermissionProfile != ":danger-full-access" || !tt.HasText("当前轮次正在启动，请稍后再调整权限。") {
		t.Fatal("startup update must retain permissions and show an error")
	}
	c.Running[thread.ID] = false
	var r codex.Request
	if err := json.Unmarshal([]byte(`{"threadId":"permissions-thread","command":"git status","availableDecisions":["accept",{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["git","status"]}},"cancel"]}`), &r); err != nil {
		t.Fatal(err)
	}
	r.ID, r.Method = json.RawMessage(`"approval-ui"`), "item/commandExecution/requestApproval"
	c.Requests = []*codex.Request{&r}
	tt.Frame()
	if !tt.HasText("允许本次") || !tt.HasText("允许并记住命令规则") || tt.HasText("本会话内允许") {
		t.Fatal("approval UI did not follow the advertised decisions")
	}
	r.AvailableDecisions = []json.RawMessage{json.RawMessage(`"acceptForSession"`), json.RawMessage(`"decline"`)}
	tt.Frame()
	if !tt.HasText("本会话内允许") || tt.HasText("允许并记住命令规则") || tt.HasText("允许本次") {
		t.Fatal("approval UI invented unavailable decisions")
	}
	s.Settings.Language = "en"
	tt.SetLocale("en")
	tt.Frame()
	if !tt.HasText("Allow for this session") || !tt.HasText("Deny") {
		t.Fatal("approval decisions did not follow the selected language")
	}
	c.Select(other.ID)
}

func TestGroupedActivity(t *testing.T) {
	s := model.New()
	thread := s.AddThread("", "")
	thread.Turns = []model.Turn{{Prompt: "检查项目", Status: "inProgress", Items: []model.Item{
		{ID: "answer-1", Kind: "agentMessage", Text: "先检查代码。", Status: "completed"},
		{ID: "cmd-1", Kind: "commandExecution", Text: "$ go test\nPRIVATE_OUTPUT", Status: "completed"},
		{ID: "think", Kind: "reasoning", Text: "PRIVATE_REASONING", Status: "completed"},
		{ID: "cmd-2", Kind: "commandExecution", Text: "$ go vet\nPRIVATE_ERROR", Status: "failed"},
		{ID: "file-1", Kind: "fileChange", Text: "main.go\nPRIVATE_DIFF", Status: "completed"},
		{ID: "file-2", Kind: "fileChange", Text: "view.go\nPRIVATE_DIFF", Status: "completed"},
		{ID: "cmd-3", Kind: "commandExecution", Text: "$ go build", Status: "inProgress"},
	}}}
	c := controller.New(t.TempDir(), s, nil)
	tt := ui.NewTester(view.New(c).Render, 1140, 900)
	summary := "命令执行 × 3 · 思考 × 1 · 文件更改 × 2"
	if _, ok := tt.Find(summary); !ok || tt.HasText("PRIVATE_") || tt.HasText("$ go test") {
		t.Fatal("activity should start as one collapsed summary")
	}
	// The timeline must use the available pane width, including after resizing.
	for _, width := range []int{1920, 840, 1140} {
		tt.SetSize(width, 900)
		tt.Frame()
		bounds, ok := tt.Find(summary)
		if !ok || math.Abs(float64(bounds.X-275)) > 1 || math.Abs(float64(bounds.X+bounds.W-float32(width-37))) > 1 {
			t.Fatalf("timeline at width %d does not fill its padded pane: %+v", width, bounds)
		}
	}
	if err := tt.Click(summary); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	if !tt.HasText("$ go test") || tt.HasText("PRIVATE_") {
		t.Fatal("individual activity details should also start collapsed")
	}
	if err := tt.Click("$ go test"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	// The virtualized output is a read-only editor, not a Text element.
	if _, ok := tt.Find("执行输出"); !ok || tt.HasText("PRIVATE_REASONING") {
		t.Fatal("only the chosen activity details should expand")
	}
	checkOutput := func() {
		t.Helper()
		if err := tt.Click("执行输出"); err != nil {
			t.Fatal(err)
		}
		tt.Key(ui.Cmd, ui.KeyA)
		tt.Key(ui.Cmd, ui.KeyC)
		if tt.Clipboard() != "$ go test\nPRIVATE_OUTPUT" {
			t.Fatalf("unexpected visible output: %q", tt.Clipboard())
		}
	}
	checkOutput()
	if err := tt.Click("思考摘要"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	if !tt.HasText("PRIVATE_REASONING") {
		t.Fatal("expanded reasoning summary is missing")
	}
	thread.Turns[0].Items[2].Text = ""
	tt.Frame()
	if !tt.HasText("未返回思考摘要") {
		t.Fatal("empty reasoning needs an explicit state")
	}
	thread.Turns[0].Items[2].Status = "inProgress"
	tt.Frame()
	if !tt.HasText("等待思考摘要…") {
		t.Fatal("pending reasoning was shown as missing")
	}
	thread.Turns[0].Items[2].Text = "STREAMED_REASONING"
	tt.Frame()
	if !tt.HasText("STREAMED_REASONING") || tt.HasText("等待思考摘要…") {
		t.Fatal("streaming did not replace the empty state")
	}
	if err := tt.Click("思考摘要"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	thread.Turns[0].Items[6].Text += "\nBUILD_OUTPUT"
	tt.Frame()
	if _, ok := tt.Find("执行输出"); !ok || tt.HasText("BUILD_OUTPUT") {
		t.Fatal("streaming changed disclosure state")
	}
	checkOutput()
	if err := tt.Click(summary); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	thread.Turns[0].Items = append(thread.Turns[0].Items,
		model.Item{ID: "answer-2", Kind: "agentMessage", Text: "修改已完成。", Status: "completed"},
		model.Item{ID: "cmd-4", Kind: "commandExecution", Text: "$ final check\nPRIVATE_FINAL", Status: "completed"},
		model.Item{ID: "answer-3", Kind: "agentMessage", Text: "验证通过。", Status: "completed"})
	tt.Frame()
	first, _ := tt.Find(summary)
	answer, _ := tt.Find("修改已完成。")
	last, ok := tt.Find("命令执行 × 1")
	if !ok || first.Y >= answer.Y || answer.Y >= last.Y || tt.HasText("PRIVATE_") {
		t.Fatal("formal replies must separate collapsed process groups in chronological order")
	}
}

func TestMCPCallDetailsView(t *testing.T) {
	s := model.New()
	thread := s.AddThread("", "")
	thread.Turns = []model.Turn{{Status: "inProgress", Items: []model.Item{
		{ID: "search", Kind: "mcpToolCall", Status: "completed", MCP: &model.MCPCall{Server: "github", Tool: "search_issues", Arguments: `{"query":"layout bug","token":"[REDACTED]"}`, Result: `{"content":[{"type":"text","text":"Issue 16 found"}]}`}},
		{ID: "fetch", Kind: "mcpToolCall", Status: "failed", MCP: &model.MCPCall{Server: "github", Tool: "get_issue", Arguments: `{"number":16}`, Error: "Access denied to issue 16"}},
		{ID: "old", Kind: "mcpToolCall", Status: "failed"},
	}}}
	tt := ui.NewTester(view.New(controller.New(t.TempDir(), s, nil)).Render, 1140, 900)
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Advance(250 * time.Millisecond)
	}
	if tt.HasText("mcpToolCall") || !tt.HasText("MCP 工具 × 3") || tt.HasText("search_issues") {
		t.Fatal("MCP calls should use a localized, initially collapsed group")
	}
	click("MCP 工具 × 3")
	for _, text := range []string{"search_issues · github", "get_issue · github", "未知 MCP 工具", "layout bug", "Access denied to issue 16", "已完成", "失败", "调用失败，但 Codex 未提供错误详情。"} {
		if !tt.HasText(text) {
			t.Fatalf("collapsed call is missing %q", text)
		}
	}
	if tt.HasText("Issue 16 found") {
		t.Fatal("collapsed call should not show full results")
	}
	click("search_issues · github")
	for _, text := range []string{"调用参数", "返回结果", "Issue 16 found", "[REDACTED]"} {
		if !tt.HasText(text) {
			t.Fatalf("expanded call is missing %q", text)
		}
	}
	longResult := strings.Repeat("Result line\n", 100) + "Last result line"
	thread.Turns[0].Items[0].MCP.Result = longResult
	tt.Frame()
	resultHeading, _ := tt.Find("返回结果")
	beforeScroll, _ := tt.Find(longResult)
	tt.Scroll(resultHeading.X+30, resultHeading.Y+resultHeading.H+30, 0, 10000)
	afterScroll, _ := tt.Find(longResult)
	afterHeading, _ := tt.Find("返回结果")
	if afterScroll.Y >= beforeScroll.Y || afterHeading != resultHeading {
		t.Fatalf("long results must scroll within their own viewport: before=%+v after=%+v heading=%+v afterHeading=%+v", beforeScroll, afterScroll, resultHeading, afterHeading)
	}
	click("search_issues · github")
	click("get_issue · github")
	if !tt.HasText("错误详情") || !tt.HasText("Access denied to issue 16") || !tt.HasText("Codex 未提供返回结果。") {
		t.Fatal("failed call should show its error and missing-result explanation")
	}
	click("get_issue · github")
	click("未知 MCP 工具")
	for _, text := range []string{"Codex 未提供工具名称或 MCP 服务名称。", "Codex 未提供调用参数。", "Codex 未提供返回结果。", "调用失败，但 Codex 未提供错误详情。"} {
		if !tt.HasText(text) {
			t.Fatalf("legacy call is missing fallback %q", text)
		}
	}
	thread.Turns[0].Items[2].Status = "inProgress"
	tt.Frame()
	if !tt.HasText("执行中") || !tt.HasText("等待工具返回结果…") {
		t.Fatal("running calls should show a pending result")
	}
	thread.Turns[0].Status = "interrupted"
	tt.Frame()
	if tt.HasText("执行中") || tt.HasText("等待工具返回结果…") || !tt.HasText("已停止") {
		t.Fatal("interrupted calls must not continue showing a running state")
	}
	s.Settings.Language = "en"
	tt.SetLocale("en")
	tt.Frame()
	for _, text := range []string{"MCP tools × 3", "Unknown MCP tool", "Arguments", "Result", "Codex did not provide the tool name or MCP server name.", "Access denied to issue 16"} {
		if !tt.HasText(text) {
			t.Fatalf("English MCP detail is missing %q", text)
		}
	}
	click("Unknown MCP tool")
	click("MCP tools × 3")
	tt.Frame()
	click("MCP tools × 3")
	if tt.HasText("Issue 16 found") {
		t.Fatal("rebuilding the group reset individual disclosure state")
	}
}

func pickerModels(t *testing.T) []codex.Model {
	t.Helper()
	var models []codex.Model
	err := json.Unmarshal([]byte(`[
		{"id":"sol","model":"gpt-6.1-sol","serviceTiers":[{"id":"priority","name":"Fast"},{"id":"ultrafast","name":"Ultrafast"}],"defaultReasoningEffort":"medium","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"medium"},{"reasoningEffort":"high"},{"reasoningEffort":"xhigh"},{"reasoningEffort":"max"},{"reasoningEffort":"ultra"}]},
		{"id":"terra","model":"gpt-6.1-terra","serviceTiers":[{"id":"priority","name":"Fast"}],"defaultReasoningEffort":"high","supportedReasoningEfforts":[{"reasoningEffort":"medium"},{"reasoningEffort":"high"}]},
		{"id":"mini","model":"gpt-5-mini","defaultReasoningEffort":"low","supportedReasoningEfforts":[{"reasoningEffort":"low"},{"reasoningEffort":"high"}]}
	]`), &models)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"gpt-6-sol", "gpt-6-luna", "gpt-5.6-sol", "gpt-5.6-terra"} {
		m := models[0]
		m.ID, m.Model = name, name
		models = append(models, m)
	}
	return models
}

func TestLanguageSwitch(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Snapshots from before language settings still open in Chinese.
	if err := os.WriteFile(path, []byte(`{"Version":1,"Settings":{"Appearance":"Dark"}}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := model.Load(path)
	if err != nil || s.Settings.Language != "zh-Hans" || s.Settings.Appearance != "Light" {
		t.Fatalf("old snapshot did not default to Chinese and light appearance: %v", err)
	}
	c := controller.New(dir, s, nil)
	stored := s.AddThread("", "")
	stored.Title, stored.Settled = "保留我的标题", true
	active := s.AddThread("", "")
	active.Draft = "未发送的草稿"
	active.ServerID = "language-test"
	var ask codex.Request
	if err := json.Unmarshal([]byte(`{"threadId":"language-test","questions":[{"id":"color","question":"保留问题原文","options":[{"label":"Blue"}]}]}`), &ask); err != nil {
		t.Fatal(err)
	}
	ask.ID, ask.Method = json.RawMessage(`1`), "item/tool/requestUserInput"
	c.Requests = []*codex.Request{&ask}
	c.Error = "Connect Codex in Settings before sending."
	tt := ui.NewTester(view.New(c).Render, 1140, 780)
	tt.SetLocale("zh-Hans")
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
	}
	if !tt.HasText("请先在设置中连接 Codex。") {
		t.Fatal("controller notice was not localized")
	}
	if !tt.HasText("继续") || !tt.HasText("保留问题原文") {
		t.Fatal("Chinese Ask controls are missing")
	}
	click("设置")
	click("外观")
	if tt.HasText("主题") || tt.HasText("跟随系统") || tt.HasText("深色") {
		t.Fatal("theme selector should be removed")
	}
	click("简体中文")
	click("English")
	tt.SetLocale(s.Settings.Language) // The real window receives App.SetLocale's locale event.
	if s.Settings.Language != "en" || !tt.HasText("Appearance") || !tt.HasText("Language") || tt.HasText("外观") {
		t.Fatal("language selection did not immediately update settings")
	}
	click("Close settings")
	if !tt.HasText("New conversation") || !tt.HasText("Settled") || tt.HasText("收纳") || !tt.HasText("Connect Codex in Settings before sending.") {
		t.Fatal("conversation UI did not switch to English")
	}
	if !tt.HasText("Continue") || tt.HasText("继续") || !tt.HasText("保留问题原文") || !tt.HasText("Blue") {
		t.Fatal("Ask should translate controls while preserving the server's question and options")
	}
	if active.Draft != "未发送的草稿" || stored.Title != "保留我的标题" {
		t.Fatal("language change rewrote user content")
	}
	loaded, err := model.Load(path)
	if err != nil || loaded.Settings.Language != "en" || loaded.Thread(active.ID).Draft != active.Draft {
		t.Fatalf("language preference was not persisted: %v", err)
	}
	click("Settled")
	tt.Advance(300 * time.Millisecond)
	if err := tt.RightClick(stored.Title); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("Restore conversation"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	if stored.Settled {
		t.Fatal("English context-menu action did not restore the same thread")
	}
	click("Settings")
	click("English")
	click("简体中文")
	tt.SetLocale(s.Settings.Language)
	click("关闭设置")
	if !tt.HasText("新对话") || !tt.HasText("收纳") || tt.HasText("New conversation") {
		t.Fatal("switching back to Chinese did not update the empty conversation title")
	}
}

func TestModelEffortPopover(t *testing.T) {
	s := model.New()
	s.Settings.Model, s.Settings.Effort = "gpt-6.1-sol", "low"
	c := controller.New(t.TempDir(), s, nil)
	c.Models = pickerModels(t)
	tt := ui.NewTester(view.New(c).Render, 1140, 780)
	if tt.HasText("计划") || !tt.HasText("gpt-6.1-sol · low") {
		t.Fatal("composer must show one combined selector without plan mode")
	}
	if _, exists := tt.Find("对话"); exists {
		t.Fatal("sidebar retained the redundant conversations heading")
	}
	box, ok := tt.Find("模型与思考强度")
	if !ok {
		t.Fatal("combined selector missing")
	}
	before := bytes.Clone(tt.Image().Pix)
	tt.Move(box.X+box.W/2, box.Y+box.H/2)
	tt.Advance(150 * time.Millisecond)
	if bytes.Equal(before, tt.Image().Pix) {
		t.Fatal("selector has no visible hover feedback")
	}
	if err := tt.Click("模型与思考强度"); err != nil {
		t.Fatal(err)
	}
	start := bytes.Clone(tt.Image().Pix)
	tt.Advance(70 * time.Millisecond)
	middle, visible := tt.Find("搜索模型")
	midPixels := bytes.Clone(tt.Image().Pix)
	if !visible || bytes.Equal(start, tt.Image().Pix) {
		t.Fatal("model popover should become visible during its opening animation")
	}
	tt.Advance(180 * time.Millisecond)
	end, _ := tt.Find("搜索模型")
	title, ok := tt.Find("模型")
	if !ok || end.X <= title.X+title.W || math.Abs(float64(title.Y+title.H/2-end.Y-end.H/2)) > 2 {
		t.Fatalf("model search should share the title row: title=%+v search=%+v", title, end)
	}
	if middle.Y <= end.Y {
		t.Fatalf("model popover must animate upwards on entry: middle=%+v end=%+v", middle, end)
	}
	panel, _ := tt.Find("模型选择面板")
	// Sample empty panel padding over the darker composer to check the fade itself.
	pixel := tt.Image().PixOffset(int(panel.X+panel.W-20), int(panel.Y+panel.H-12))
	if !(start[pixel] < midPixels[pixel] && midPixels[pixel] < tt.Image().Pix[pixel]) {
		t.Fatal("model popover should fade from transparent through an intermediate opacity")
	}
	left, _ := tt.Find("gpt-6.1-terra")
	right, ok := tt.Find("调整思考强度")
	if !ok || right.X <= left.X+left.W || right.Y >= box.Y {
		t.Fatalf("expected a two-column popover above the composer: left=%+v right=%+v", left, right)
	}
	tt.ClickAt(right.X+right.W-10, right.Y+10)
	if s.Settings.Effort != "ultra" {
		t.Fatal("last advertised effort is not reachable")
	}
	tt.Key(0, ui.KeyLeft)
	if s.Settings.Effort != "max" {
		t.Fatal("slider skipped an advertised effort")
	}
	tt.Key(0, ui.KeyHome)
	if s.Settings.Effort != "low" {
		t.Fatal("Home did not select the first effort")
	}
	fast, fastVisible := tt.Find("Fast")
	ultrafast, ultraVisible := tt.Find("Ultrafast")
	if !fastVisible || !ultraVisible || fast.X <= left.X+left.W || fast.Y <= right.Y || ultrafast.X != fast.X || ultrafast.Y < fast.Y+fast.H || fast.H < 34 {
		t.Fatal("server-advertised speeds must appear below the effort slider")
	}
	list, ok := tt.Find("模型列表")
	if !ok || list.H > 180 || fast.Y-(right.Y+right.H) > 65 {
		t.Fatalf("model picker left excessive empty space: list=%+v, slider=%+v, speed=%+v", list, right, fast)
	}
	for _, choice := range []struct{ label, tier string }{{"Fast", "priority"}, {"Ultrafast", "ultrafast"}, {"Ultrafast", ""}, {"Ultrafast", "ultrafast"}} {
		if err := tt.Click(choice.label); err != nil {
			t.Fatal(err)
		}
		if s.Settings.ServiceTier != choice.tier || !tt.HasText("搜索模型") {
			t.Fatal("speed selection must be exclusive, toggle off to standard, and keep the panel open")
		}
		label := "gpt-6.1-sol · low"
		if choice.tier != "" {
			label += " · " + choice.label
		}
		if _, ok := tt.Find(label); !ok {
			t.Fatalf("composer did not reflect the selected speed: %s", label)
		}
		stored, err := model.Load(filepath.Join(c.Dir, "state.json"))
		if err != nil || stored.Settings.ServiceTier != choice.tier {
			t.Fatalf("speed selection was not saved: %v", err)
		}
	}
	if err := tt.Click("gpt-6.1-terra"); err != nil {
		t.Fatal(err)
	}
	if s.Settings.Model != "gpt-6.1-terra" || s.Settings.Effort != "high" {
		t.Fatal("unsupported effort must fall back to the model's default")
	}
	if _, ok := tt.Find("low"); ok {
		t.Fatal("popover retained an unsupported effort")
	}
	if tt.HasText("Ultrafast") || s.Settings.ServiceTier != "" {
		t.Fatal("switching model must remove and clear unsupported speeds")
	}
	if _, ok := tt.Find("gpt-6.1-terra · high"); !ok {
		t.Fatal("composer retained an unsupported speed after changing models")
	}
	if err := tt.Click("Fast"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("gpt-6.1-sol"); err != nil {
		t.Fatal(err)
	}
	if s.Settings.ServiceTier != "priority" {
		t.Fatal("supported speeds should survive a model change")
	}
	if _, ok := tt.Find("gpt-6.1-sol · high · Fast"); !ok {
		t.Fatal("composer lost the compatible speed after changing models")
	}
	if err := tt.Click("gpt-5-mini"); err != nil {
		t.Fatal(err)
	}
	if s.Settings.Effort != "high" {
		t.Fatal("supported effort should survive a model change")
	}
	if tt.HasText("速度") || s.Settings.ServiceTier != "" {
		t.Fatal("models without speed metadata must use standard speed without invented options")
	}
	tt.Advance(250 * time.Millisecond)
	panel, _ = tt.Find("模型选择面板")
	pixel = tt.Image().PixOffset(int(panel.X+panel.W-20), int(panel.Y+panel.H-12))
	openPixel := tt.Image().Pix[pixel]
	slider, ok := tt.Find("调整思考强度")
	if !ok {
		t.Fatal("discrete effort slider is missing")
	}
	tt.ClickAt(slider.X+10, slider.Y+10)
	if !tt.HasText("gpt-5-mini · low") || !tt.HasText("搜索模型") {
		t.Fatal("effort slider should update the trigger and stay open while adjusting")
	}
	tt.Key(0, ui.KeyRight)
	if s.Settings.Effort != "high" {
		t.Fatal("arrow keys must follow the model's advertised steps")
	}
	tt.Key(0, ui.KeyLeft)
	if s.Settings.Effort != "low" {
		t.Fatal("arrow keys failed to return to the first step")
	}
	tt.Key(0, ui.KeyEscape)
	tt.Advance(65 * time.Millisecond)
	closingPixel := tt.Image().Pix[pixel]
	tt.Advance(150 * time.Millisecond)
	closedPixel := tt.Image().Pix[pixel]
	if !(openPixel > closingPixel && closingPixel > closedPixel) {
		t.Fatalf("popover must visibly fade out after selecting effort: %d -> %d -> %d", openPixel, closingPixel, closedPixel)
	}
	loaded, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil || loaded.Settings.Model != "gpt-5-mini" || loaded.Settings.Effort != "low" {
		t.Fatalf("selection not saved: %v", err)
	}
	if err := tt.Click("模型与思考强度"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(70 * time.Millisecond)
	reopened, _ := tt.Find("搜索模型")
	tt.Advance(180 * time.Millisecond)
	reopenedEnd, _ := tt.Find("搜索模型")
	if reopened.Y <= reopenedEnd.Y {
		t.Fatal("opening animation did not replay after closing and reopening")
	}
	if err := tt.Click("搜索模型"); err != nil {
		t.Fatal(err)
	}
	tt.Type("terra")
	if _, ok := tt.Find("gpt-6.1-sol"); ok || !tt.HasText("gpt-6.1-terra") {
		t.Fatal("model search did not filter its rows")
	}
	tt.Key(0, ui.KeyEscape)
	if tt.HasText("搜索模型") {
		t.Fatal("Escape did not dismiss the popover")
	}
	tt.Advance(200 * time.Millisecond)
	for _, dismissal := range []string{"escape", "outside", "toggle"} {
		if err := tt.Click("模型与思考强度"); err != nil {
			t.Fatal(err)
		}
		tt.Advance(250 * time.Millisecond)
		panel, _ := tt.Find("模型选择面板")
		pixel := tt.Image().PixOffset(int(panel.X+panel.W-20), int(panel.Y+panel.H-12))
		shown := tt.Image().Pix[pixel]
		switch dismissal {
		case "escape":
			tt.Key(0, ui.KeyEscape)
		case "outside":
			tt.ClickAt(700, 150)
		case "toggle":
			if err := tt.Click("模型与思考强度"); err != nil {
				t.Fatal(err)
			}
		}
		if _, active := tt.Find("搜索模型"); active {
			t.Fatalf("%s left an interactive popup during exit", dismissal)
		}
		tt.Advance(65 * time.Millisecond)
		fading := tt.Image().Pix[pixel]
		tt.Advance(150 * time.Millisecond)
		gone := tt.Image().Pix[pixel]
		if !(shown > fading && fading > gone) {
			t.Fatalf("%s must fade out: %d -> %d -> %d", dismissal, shown, fading, gone)
		}
	}
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if err := tt.Click("模型与思考强度"); err != nil {
		t.Fatal(err)
	}
	reduced, visible := tt.Find("搜索模型")
	if !visible || reduced != reopenedEnd {
		t.Fatal("reduced motion should show the popover immediately at its final position")
	}
}

func TestProjectFoldersPersistExpandedState(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "state.json")
	// Existing projects without an expansion preference start collapsed.
	if err := os.WriteFile(path, []byte(`{"Version":1,"Projects":[{"ID":"one","Name":"First project","Path":"/first"},{"ID":"two","Name":"Second project","Path":"/second"}],"Threads":[{"ID":"thread","ProjectID":"one","Title":"Saved conversation","Draft":"Keep this draft"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	s, err := model.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	c := controller.New(dir, s, nil)
	tt := ui.NewTester(view.New(c).Render, 1140, 780)
	if tt.HasText("Saved conversation") || tt.HasText("新建 · ") {
		t.Fatal("projects must start collapsed with their new-conversation buttons hidden")
	}
	header, _ := tt.Find("First project")
	tt.Move(header.X+10, header.Y+header.H/2)
	button, ok := tt.Find("新建 · First project")
	if !ok || button.X < header.X+header.W-65 || tt.HasText("新建 · Second project") {
		t.Fatal("only the hovered project's plus should appear beside its disclosure arrow")
	}
	if err := tt.Click("First project"); err != nil {
		t.Fatal(err)
	}
	if !s.Projects[0].Expanded || s.Projects[1].Expanded || !tt.HasText("Saved conversation") {
		t.Fatal("expanding a project must reveal only its conversations")
	}
	row, _ := tt.Find("Saved conversation")
	tt.Move(row.X+10, row.Y+row.H/2)
	if tt.HasText("新建 · ") {
		t.Fatal("project plus must hide when the pointer leaves its header")
	}
	for _, expanded := range []bool{true, false} {
		if !expanded {
			if err := tt.Click("First project"); err != nil {
				t.Fatal(err)
			}
		}
		s, err = model.Load(path)
		if err != nil {
			t.Fatal(err)
		}
		c = controller.New(dir, s, nil)
		tt = ui.NewTester(view.New(c).Render, 1140, 780)
		tt.Frame()
		if s.Projects[0].Expanded != expanded || s.Projects[1].Expanded || tt.HasText("Saved conversation") != expanded || s.Thread("thread").Draft != "Keep this draft" {
			t.Fatalf("restarting did not preserve expansion=%v and conversation data", expanded)
		}
	}
	header, _ = tt.Find("First project")
	tt.Move(header.X+10, header.Y+header.H/2)
	if err := tt.Click("新建 · First project"); err != nil {
		t.Fatal(err)
	}
	if len(s.Threads) != 2 || s.Thread(s.Selected).ProjectID != "one" || !s.Projects[0].Expanded || s.Projects[1].Expanded {
		t.Fatal("plus must create in and expand the hovered project without toggling another project")
	}
	if err := tt.Click("First project"); err != nil {
		t.Fatal(err)
	}
	tt.Key(0, ui.KeySpace)
	tt.Frame()
	if !s.Projects[0].Expanded {
		t.Fatal("project disclosure must support keyboard activation")
	}
	if err := tt.Click("Second project"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if !s.Projects[1].Expanded || len(s.Threads) != 2 {
		t.Fatal("expanding an empty folder must not create a conversation")
	}
}

func TestSidebarVirtualProjectAndSettledLists(t *testing.T) {
	s := model.New()
	for i := 999; i >= 0; i-- {
		thread := s.AddThread("", "")
		thread.Title, thread.Settled = fmt.Sprintf("Settled row %04d", i), true
	}
	s.Projects = []model.Project{{ID: "project", Name: "Project", Path: t.TempDir()}, {ID: "other", Name: "Other project"}}
	for i := 999; i >= 0; i-- {
		s.AddThread("project", s.Projects[0].Path).Title = fmt.Sprintf("Active row %04d", i)
	}
	s.Selected = ""
	c := controller.New(t.TempDir(), s, nil)
	tt := ui.NewTester(view.New(c).Render, 1140, 780)
	if tt.HasText("Active row ") {
		t.Fatal("collapsed folders must not build conversation rows")
	}
	if err := tt.Click("Project"); err != nil {
		t.Fatal(err)
	}
	header, _ := tt.Find("Project")
	next, _ := tt.Find("Other project")
	if math.Abs(float64(next.Y-header.Y-header.H-10*34-14)) > 1 {
		t.Fatalf("project viewport must be capped at ten rows: header=%+v next=%+v", header, next)
	}
	visibleActive := map[string]bool{}
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "Active row ") {
			visibleActive[text] = true
		}
	}
	if len(visibleActive) < 10 || len(visibleActive) > 14 {
		t.Fatalf("project must build ten visible rows and limited overscan, got %d", len(visibleActive))
	}
	first, _ := tt.Find("Active row 0000")
	if first.X <= header.X {
		t.Fatal("conversations should be indented beneath their folder")
	}
	tt.Wheel(first.X+10, first.Y+10, 0, 340)
	tt.Advance(400 * time.Millisecond)
	if tt.HasText("Active row 0000") || !tt.HasText("Active row 0010") {
		t.Fatal("wheel must load further project conversations")
	}
	tt.Scroll(first.X+10, first.Y+10, 0, 100000)
	if err := tt.Click("Active row 0999"); err != nil {
		t.Fatal(err)
	}
	if s.Thread(s.Selected).Title != "Active row 0999" {
		t.Fatal("virtual project row selected the wrong conversation")
	}
	tt.Move(header.X+10, header.Y+header.H/2)
	if err := tt.Click("新建 · Project"); err != nil {
		t.Fatal(err)
	}
	s.Thread(s.Selected).Title = "Newest conversation"
	tt.Frame()
	newest, ok := tt.Find("Newest conversation")
	if !ok || newest.X >= 246 || !tt.HasText("Active row 0000") {
		t.Fatal("creating a conversation must scroll the project back to its new first row")
	}
	if err := tt.Click("Project"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if tt.HasText("Active row ") {
		t.Fatal("collapsing a project must release its virtual rows")
	}
	closed, _ := tt.Find("收纳")
	footer, _ := tt.Find("设置")
	if closed.Y < 680 || footer.Y <= closed.Y {
		t.Fatal("Settled must be pinned above the bottom toolbar")
	}
	if err := tt.Click("收纳"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(100 * time.Millisecond)
	moving, _ := tt.Find("收纳")
	tt.Advance(200 * time.Millisecond)
	opened, _ := tt.Find("收纳")
	list, ok := tt.Find("已收纳的会话")
	if !ok || math.Abs(float64(list.H-5*34)) > 1 || math.Abs(float64(closed.Y-opened.Y-5*34)) > 1 || !(opened.Y < moving.Y && moving.Y < closed.Y) {
		t.Fatalf("shelf should animate upwards by five rows: closed=%+v moving=%+v opened=%+v list=%+v", closed, moving, opened, list)
	}
	count, ok := tt.Find("1000")
	if !ok || count.X < 195 {
		t.Fatal("settled count should appear at the right edge")
	}
	visible := map[string]bool{}
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "Settled row ") {
			visible[text] = true
		}
	}
	if len(visible) == 0 || len(visible) > 12 {
		t.Fatalf("offscreen titles should be virtualized, rendered %d rows", len(visible))
	}
	tt.Wheel(list.X+list.W/2, list.Y+list.H/2, 0, 340)
	tt.Advance(400 * time.Millisecond)
	scrolled := false
	for _, text := range tt.Texts() {
		if strings.HasPrefix(text, "Settled row ") && !visible[text] {
			scrolled = true
		}
	}
	if !scrolled {
		t.Fatal("wheel did not reveal additional settled conversations")
	}
	tt.Scroll(list.X+list.W/2, list.Y+list.H/2, 0, 100000)
	if err := tt.RightClick("Settled row 0999"); err != nil {
		t.Fatal(err)
	}
	if err := tt.ChooseMenuItem("删除"); err != nil {
		t.Fatal(err)
	}
	if !tt.HasText("将删除「Settled row 0999」") {
		t.Fatal("virtual row menu targeted the wrong conversation")
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	for _, thread := range s.Threads {
		if thread.Title == "Settled row 0999" {
			t.Fatal("confirmed virtual row deletion was not applied")
		}
	}
	tt.Advance(300 * time.Millisecond)
	if _, ok := tt.Find("999"); !ok {
		t.Fatal("settled count did not update after deletion")
	}
	if err := tt.Click("收纳"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(300 * time.Millisecond)
	reclosed, _ := tt.Find("收纳")
	if reclosed.Y != closed.Y || tt.HasText("Settled row ") {
		t.Fatal("collapsed shelf must return to its bottom position and release its rows")
	}

	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	if err := tt.Click("收纳"); err != nil {
		t.Fatal(err)
	}
	reduced, _ := tt.Find("收纳")
	if reduced.Y != opened.Y {
		t.Fatal("reduce-motion preference should skip drawer animation")
	}
}

// Optional artifacts aid visual review without introducing screenshot dependencies.
func TestNativePreview(t *testing.T) {
	out := os.Getenv("MUJICAGENT_PREVIEW_DIR")
	if out == "" {
		t.Skip("preview not requested")
	}
	s := model.New()
	c := controller.New(t.TempDir(), s, nil)
	s.Projects = []model.Project{{ID: "project", Name: "mujicagent", Path: "/projects/mujicagent"}}
	thread := s.AddThread("project", "/projects/mujicagent")
	thread.Title = "让原生界面更轻盈"
	thread.Turns = []model.Turn{{Prompt: "把会话列表设计得更紧凑，悬停时显示 Settle。", Status: "completed", Started: time.Now(), Items: []model.Item{{ID: "1", Kind: "agentMessage", Text: "已调整会话列表。\n\n每个会话保持 **一行**，悬停时显示收起按钮；已收起的会话保留在 **Settled** 中，随时可以继续。\n\n```go\nui.Row(c).Height(34).Radius(9)\n```", Status: "completed"}}}}
	thread.Turns[0].Items = append([]model.Item{
		{ID: "thought", Kind: "reasoning", Text: "检查侧栏结构和现有布局。", Status: "completed"},
		{ID: "command-1", Kind: "commandExecution", Text: "$ go test ./...\nok  mujicagent", Status: "completed"},
		{ID: "command-2", Kind: "commandExecution", Text: "$ go vet ./...", Status: "completed"},
		{ID: "command-3", Kind: "commandExecution", Text: "$ go tool mygo build", Status: "completed"},
		{ID: "file-1", Kind: "fileChange", Text: "internal/view/view.go\n+ compact native rows", Status: "completed"},
		{ID: "file-2", Kind: "fileChange", Text: "internal/view/activity.go\n+ grouped activity", Status: "completed"},
	}, thread.Turns[0].Items...)
	for _, title := range []string{"整理 Codex 连接状态", "为 Ask 添加选项回答", "检查项目目录"} {
		x := s.AddThread("project", "")
		x.Title = title
	}
	x := s.AddThread("", "")
	x.Title = "一个新的想法"
	s.Selected = thread.ID
	c.Connection = "Connected"
	c.Account = "ChatGPT"
	c.Version = "codex-cli 0.167.0"
	c.Models = pickerModels(t)
	c.Permissions = []codex.PermissionProfile{{ID: ":read-only", Allowed: true}, {ID: ":workspace", Allowed: true}, {ID: ":approve-for-me", Allowed: true}, {ID: ":danger-full-access", Allowed: true}}
	s.Settings.Model, s.Settings.Effort = "gpt-6.1-sol", "low"
	for _, title := range []string{"优化 Markdown 排版", "简化连接逻辑", "检查 Ask 回答", "调整深色主题", "整理模型列表", "连接本地项目", "打包 macOS 应用", "会话存储"} {
		x := s.AddThread("", "")
		x.Title, x.Settled = title, true
	}
	s.Selected = thread.ID
	v := view.New(c)
	tt := ui.NewTester(v.Render, 1140, 780)
	tt.SetLocale(s.Settings.Language)
	save := func(name string) {
		t.Helper()
		f, err := os.Create(filepath.Join(out, name))
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	save("conversation.png")
	c.LogNotice("The turn is starting. Try Stop again in a moment.")
	c.LogDiagnostic("Codex requested an unsupported action: mcpServer/elicitation/request")
	c.ReportError("找不到 Codex 可执行文件。请在设置 → 智能体中检查 Codex 路径，然后重新连接。")
	tt.Frame()
	save("error-alert.png")
	for _, label := range []string{"设置", "日志"} {
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Advance(250 * time.Millisecond)
	}
	save("logs.png")
	if err := tt.Click("切换开发者模式"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	save("developer-logs.png")
	tt.SetSize(840, 560)
	tt.Advance(250 * time.Millisecond)
	save("developer-logs-compact.png")
	if err := tt.Click("关闭设置"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	save("error-alert-compact.png")
	if err := tt.Click("关闭提示"); err != nil {
		t.Fatal(err)
	}
	s.Settings.DeveloperMode = false
	tt.SetSize(1140, 780)
	tt.Frame()
	if err := tt.Click("思考 × 1 · 命令执行 × 3 · 文件更改 × 2"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	save("activity-expanded.png")
	if err := tt.Click("思考摘要"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	save("reasoning.png")
	thread.Turns[0].Items[0].Text = ""
	tt.Frame()
	save("reasoning-empty.png")
	thread.Turns[0].Items[0].Text = "检查侧栏结构和现有布局。"
	if err := tt.Click("思考 × 1 · 命令执行 × 3 · 文件更改 × 2"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	if err := tt.Click("收纳"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(90 * time.Millisecond)
	save("settled-opening.png")
	tt.Advance(210 * time.Millisecond)
	save("settled.png")
	if err := tt.Click("模型与思考强度"); err != nil {
		t.Fatal(err)
	}
	save("model-picker-start.png")
	tt.Advance(70 * time.Millisecond)
	save("model-picker-opening.png")
	tt.Advance(180 * time.Millisecond)
	save("model-picker.png")
	if err := tt.Click("Ultrafast"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(160 * time.Millisecond)
	save("model-picker-speed.png")
	tt.Key(0, ui.KeyEscape)
	tt.Advance(65 * time.Millisecond)
	save("model-picker-closing.png")
	tt.Advance(150 * time.Millisecond)
	save("model-picker-closed.png")
	if err := tt.Click("模型与思考强度"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	tt.SetDark(true)
	tt.Advance(300 * time.Millisecond)
	save("model-picker-system-dark.png")
	tt.SetDark(false)
	tt.SetSize(840, 560)
	tt.Advance(300 * time.Millisecond)
	save("model-picker-compact.png")
	tt.Key(0, ui.KeyEscape)
	tt.SetSize(1140, 780)
	v.Settings = true
	tt.Frame()
	if err := tt.Click("智能体"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(220 * time.Millisecond)
	save("settings.png")
	c.AppUpdates = controller.AppUpdateState{Version: "0.1.0", Enabled: true, AvailableVersion: "0.1.1", Releases: []model.Release{
		{Tag: "v0.1.1", Published: time.Now(), Notes: "### 更新\n\n- 自动检查 GitHub Release，校验签名后安装。\n- 在设置中查看历史更新日志。"},
		{Tag: "v0.1.0", Notes: "首个原生版本。"},
	}}
	if err := tt.Click("软件更新"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	save("updates.png")
	tt.SetDark(true)
	tt.Advance(300 * time.Millisecond)
	save("updates-system-dark.png")
	tt.SetDark(false)
	if err := tt.Click("智能体"); err != nil {
		t.Fatal(err)
	}
	tt.SetSize(840, 560)
	tt.Advance(300 * time.Millisecond)
	save("settings-compact.png")
	s.Settings.Language = "en"
	tt.SetLocale("en")
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	save("settings-english.png")
	if err := tt.Click("Appearance"); err != nil {
		t.Fatal(err)
	}
	save("language-settings.png")
	tt.Key(0, ui.KeyEscape)
	if err := tt.Click("Model and reasoning effort"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	save("model-picker-english.png")
	tt.Key(0, ui.KeyEscape)
	tt.Advance(200 * time.Millisecond)
	s.Settings.Language = "zh-Hans"
	tt.SetLocale("zh-Hans")
	tt.SetSize(1140, 780)
	if err := tt.Click("权限"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	save("permissions.png")
	if err := tt.Click("自动审批"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(200 * time.Millisecond)
	if err := tt.Click("权限"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	save("permissions-auto.png")
	if err := tt.Click("完全访问"); err != nil {
		t.Fatal(err)
	}
	tt.Advance(250 * time.Millisecond)
	save("full-access-confirm.png")
	tt.Key(0, ui.KeyEscape)
	thread.ServerID = "preview-server"
	var approval codex.Request
	if err := json.Unmarshal([]byte(`{"threadId":"preview-server","command":"go test ./...","reason":"运行项目测试。","availableDecisions":["accept","acceptForSession",{"acceptWithExecpolicyAmendment":{"execpolicy_amendment":["go","test"]}},"cancel"]}`), &approval); err != nil {
		t.Fatal(err)
	}
	approval.ID, approval.Method = json.RawMessage(`"preview-approval"`), "item/commandExecution/requestApproval"
	c.Requests = []*codex.Request{&approval}
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	save("approval.png")
	tt.SetSize(840, 560)
	tt.Advance(250 * time.Millisecond)
	save("approval-compact.png")
}

func TestConversationContextMenuAndDeleteConfirmation(t *testing.T) {
	s := model.New()
	c := controller.New(t.TempDir(), s, nil)
	c.AddProject(t.TempDir())
	thread := s.Thread(s.Selected)
	thread.Title = "等待确认的会话"
	v := view.New(c)
	tt := ui.NewTester(v.Render, 1140, 780)
	if err := tt.Click(s.Projects[0].Name); err != nil {
		t.Fatal(err)
	}
	openDelete := func() {
		t.Helper()
		if err := tt.RightClick(thread.Title); err != nil {
			t.Fatal(err)
		}
		if err := tt.ChooseMenuItem("删除"); err != nil {
			t.Fatal(err)
		}
	}
	openDelete()
	if s.Thread(thread.ID) == nil || !tt.HasText("删除对话？") {
		t.Fatal("deletion must wait for confirmation")
	}
	if err := tt.Click("取消"); err != nil {
		t.Fatal(err)
	}
	if s.Thread(thread.ID) == nil {
		t.Fatal("cancel removed the conversation")
	}
	openDelete()
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	if s.Thread(thread.ID) != nil {
		t.Fatal("confirmed conversation still exists")
	}
	loaded, err := model.Load(filepath.Join(c.Dir, "state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Thread(thread.ID) != nil {
		t.Fatal("deleted conversation returned after reload")
	}
}

func TestMessageQueueLayoutAndDrag(t *testing.T) {
	for _, size := range [][2]int{{1140, 780}, {840, 560}} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			state := model.New()
			ctrl := controller.New(t.TempDir(), state, nil)
			ctrl.AddProject(t.TempDir())
			thread := state.Thread(state.Selected)
			thread.Turns = []model.Turn{{ID: "turn", Prompt: "Active task", Status: "inProgress", Items: []model.Item{{ID: "steer", Kind: "steerMessage", Text: "Steered message"}}}}
			thread.Queue = []model.QueuedMessage{{ID: "b", Text: "First queued message"}, {ID: "c", Text: "Second queued message"}}
			ctrl.Running[thread.ID] = true
			v := view.New(ctrl)
			tt := ui.NewTester(v.Render, size[0], size[1])
			tt.Frame()
			if tt.HasText("接下来") || tt.HasText("本轮结束后，按顺序逐条发送") || tt.HasText("已插入当前对话") || !tt.HasText("Steered message") {
				t.Fatal("explanatory labels remain or message content is missing")
			}
			editor, ok := tt.Find("消息")
			send, sendOK := tt.Find("加入队列")
			if !ok || !sendOK || editor.Y+editor.H > float32(size[1]) || send.Y+send.H > float32(size[1]) {
				t.Fatal("composer clipped by queue")
			}
			handle, ok := tt.Find("⠿")
			target, ok2 := tt.Find("Second queued message")
			if !ok || !ok2 {
				t.Fatal("queue rows missing")
			}
			x, y := handle.X+handle.W/2, handle.Y+handle.H/2
			tt.Press(x, y)
			tt.Move(x, y+8)
			tt.Move(target.X+8, target.Y+target.H/2)
			tt.Release(target.X+8, target.Y+target.H/2)
			tt.Frame()
			if thread.Queue[0].ID != "c" || thread.Queue[1].ID != "b" {
				t.Fatalf("drag failed: %+v", thread.Queue)
			}
			thread.Queue[0].Sending = true
			ctrl.Save()
			restored, err := model.Load(filepath.Join(ctrl.Dir, "state.json"))
			if err != nil || len(restored.Thread(thread.ID).Queue) != 2 || restored.Thread(thread.ID).Queue[0].ID != "c" || restored.Thread(thread.ID).Queue[0].Sending {
				t.Fatal("queue order or transient state not restored correctly")
			}
			thread.Queue = nil
			tt.Frame()
			thread.Queue = []model.QueuedMessage{{ID: "b", Text: "Restored message"}}
			tt.Frame()
			state.Settings.Language = "en"
			tt.Frame()
			if tt.HasText("Up next") || tt.HasText("Inserted into this turn") || !tt.HasText("Steer") {
				t.Fatal("queue localization missing")
			}
		})
	}
}

func TestNoProjectGroup(t *testing.T) {
	state := model.New()
	ctrl := controller.New(t.TempDir(), state, nil)
	tt := ui.NewTester(view.New(ctrl).Render, 1140, 780)
	header, ok := tt.Find("无项目")
	if !ok {
		t.Fatal("missing empty No Project group")
	}
	tt.Move(header.X+10, header.Y+header.H/2)
	if err := tt.Click("新建 · 无项目"); err != nil {
		t.Fatal(err)
	}
	thread := state.Thread(state.Selected)
	if thread == nil || thread.ProjectID != "" || thread.Settled || !state.NoProjectExpanded {
		t.Fatal("incorrect projectless creation")
	}
	thread.Title = "Projectless test"
	tt.Frame()
	if !tt.HasText(thread.Title) {
		t.Fatal("missing projectless row")
	}
	ctrl.Settle(thread.ID, true)
	tt.Frame()
	if tt.HasText(thread.Title) {
		t.Fatal("settled thread remains in project")
	}
	ctrl.Settle(thread.ID, false)
	tt.Frame()
	if !tt.HasText(thread.Title) {
		t.Fatal("restored thread missing")
	}
	if err := tt.Click("无项目"); err != nil {
		t.Fatal(err)
	}
	if tt.HasText(thread.Title) || state.NoProjectExpanded {
		t.Fatal("collapse failed")
	}
	saved, err := model.Load(filepath.Join(ctrl.Dir, "state.json"))
	if err != nil || saved.NoProjectExpanded {
		t.Fatal("expansion state not saved")
	}
	state.Settings.Language = "en"
	tt.Frame()
	if _, ok := tt.Find("No Project"); !ok {
		t.Fatal("missing English group")
	}
}
