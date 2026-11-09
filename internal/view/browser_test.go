package view

import (
	"context"
	"fmt"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/browser"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func TestBrowserRegistrationErrorsAreLocalized(t *testing.T) {
	v, _, _ := browserTestView(t)
	for _, message := range []string{codex.ErrBrowserResume.Error(), codex.ErrBrowserRegistration.Error()} {
		v.C.State.Settings.Language = "zh-Hans"
		if got := v.statusText(message); got == message || !strings.Contains(got, "浏览器工具") {
			t.Fatalf("unlocalized registration error: %q", got)
		}
		v.C.State.Settings.Language = "en"
		if got := v.statusText(message); got == message || strings.Contains(got, "浏览器") {
			t.Fatalf("English registration error not presented: %q", got)
		}
	}
}

func browserTestView(t *testing.T) (*View, string, browser.Status) {
	t.Helper()
	state := model.New()
	thread := state.AddThread("", "")
	thread.Draft = "Keep this chat draft"
	c := controller.New(t.TempDir(), state, nil)
	t.Cleanup(c.Close)
	v := New(c)
	status := browser.Status{
		Page:  browser.PageRef{PageKey: browser.PageKey{ThreadID: thread.ID, PageID: "page-a"}, Navigation: 1},
		State: browser.StateHuman, URL: "https://example.com/first", Title: "Example page",
	}
	v.Browser.SetStatus(status)
	v.Browser.thread(thread.ID).visible = true
	return v, thread.ID, status
}

func browserClick(t *testing.T, tt *ui.Tester, label string) {
	t.Helper()
	if err := tt.Click(label); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
}

func browserPageAcceptsInput(t *testing.T, tt *ui.Tester) bool {
	t.Helper()
	bounds, ok := tt.Find("当前网页可见区域")
	if !ok || bounds.W <= 0 || bounds.H <= 0 {
		t.Fatalf("missing page viewport: %+v", bounds)
	}
	return tt.WebViewAt(bounds.X+bounds.W/2, bounds.Y+bounds.H/2) != nil
}

func TestBrowserTakeOverWaitsForDrain(t *testing.T) {
	v, id, status := browserTestView(t)
	status.State, status.Busy = browser.StateAgent, true
	v.Browser.SetStatus(status)
	page := &mygo.WebView{}
	v.Browser.Page = func(key browser.PageKey) ui.NativeWebView {
		if key != status.Page.PageKey {
			t.Fatalf("wrong page: %+v", key)
		}
		return page
	}
	var done BrowserReply
	v.Browser.Actions.TakeOver = func(key browser.PageKey, reply BrowserReply) {
		if key != status.Page.PageKey {
			t.Fatalf("wrong takeover: %+v", key)
		}
		done = reply
	}
	v.Browser.Actions.Resume = func(browser.PageKey, browser.Permissions, BrowserReply) { t.Fatal("resumed before draining") }
	tt := ui.NewTester(v.Render, 1140, 780)
	if browserPageAcceptsInput(t, tt) {
		t.Fatal("Agent page takes native pointer input")
	}
	browserClick(t, tt, v.texts().BrowserTakeOver())
	if done == nil || v.Browser.thread(id).status.State != browser.StateAgent || !tt.HasText(v.texts().BrowserPausing()) {
		t.Fatal("takeover was not dispatched or was reported complete prematurely")
	}
	status.State = browser.StateHuman
	done(status, context.DeadlineExceeded)
	tt.Frame()
	if browserPageAcceptsInput(t, tt) || !tt.HasText(v.texts().BrowserDraining()) || !tt.HasText(v.texts().BrowserTimeout()) {
		t.Fatal("timed-out takeover unlocked the page or hid its error")
	}
	browserClick(t, tt, v.texts().BrowserResume())
	if v.Browser.thread(id).confirm {
		t.Fatal("resume dialog opened before draining")
	}
	status.Busy = false
	v.Browser.SetStatus(status)
	tt.Frame()
	if !browserPageAcceptsInput(t, tt) {
		t.Fatal("drained Human page remains blocked")
	}
	status.Loading = true
	v.Browser.SetStatus(status)
	tt.Frame()
	if browserPageAcceptsInput(t, tt) || !tt.HasText(v.texts().BrowserLoading()) {
		t.Fatal("loading page remains interactive")
	}
	if _, ok := tt.Find(v.texts().BrowserPage()); ok {
		t.Fatal("transition to blocked input did not hide the native page to release its keyboard")
	}
	tt.Advance(2 * time.Millisecond)
	if _, ok := tt.Find(v.texts().BrowserPage()); !ok || browserPageAcceptsInput(t, tt) {
		t.Fatal("blocked page was not restored for display without native input")
	}
}

func TestBrowserResumeRequiresExplicitPermissions(t *testing.T) {
	v, id, status := browserTestView(t)
	var resume BrowserReply
	var permissions browser.Permissions
	v.Browser.Actions.TakeOver = func(browser.PageKey, BrowserReply) {}
	v.Browser.Actions.Resume = func(key browser.PageKey, grants browser.Permissions, reply BrowserReply) {
		if key != status.Page.PageKey {
			t.Fatalf("wrong resume page: %+v", key)
		}
		permissions, resume = grants, reply
	}
	tt := ui.NewTester(v.Render, 1140, 780)
	browserClick(t, tt, v.texts().BrowserResume())
	if !tt.HasText(v.texts().BrowserCaptureHint()) || v.Browser.thread(id).permissions != (browser.Permissions{}) || resume != nil {
		t.Fatal("opening permissions granted control or omitted the capture scope")
	}
	browserClick(t, tt, v.texts().BrowserAllowNavigate())
	browserClick(t, tt, v.texts().BrowserAllowInteract())
	browserClick(t, tt, v.texts().BrowserConfirmResume())
	if resume == nil || permissions != (browser.Permissions{Navigate: true, Interact: true}) || v.Browser.thread(id).status.State != browser.StateHuman {
		t.Fatal("permissions were not explicit or resume was reported prematurely")
	}
	status.State = browser.StateIdle
	status.Permissions = permissions
	resume(status, nil)
	tt.Frame()
	if !tt.HasText(v.texts().BrowserIdle()) {
		t.Fatal("confirmed control state was not displayed")
	}
	status.State = browser.StateHuman
	status.Permissions = browser.Permissions{}
	v.Browser.SetStatus(status)
	tt.Frame()
	browserClick(t, tt, v.texts().BrowserResume())
	if v.Browser.thread(id).permissions != (browser.Permissions{}) {
		t.Fatal("a later resume inherited permission grants")
	}
	browserClick(t, tt, v.texts().Cancel())
	if v.Browser.thread(id).confirm {
		t.Fatal("cancel left the permission dialog open")
	}
}

func TestBrowserAddressAndComposerStayIndependent(t *testing.T) {
	v, id, status := browserTestView(t)
	var request browser.OpenRequest
	var openDone func(browser.ActionResult, browser.Status, error)
	v.Browser.Actions.Open = func(req browser.OpenRequest, reply func(browser.ActionResult, browser.Status, error)) {
		request, openDone = req, reply
	}
	tt := ui.NewTester(v.Render, 1140, 780)
	browserClick(t, tt, v.texts().BrowserAddress())
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("https://example.com/next?q=private")
	v.Browser.SetStatus(status)
	tt.Frame()
	if v.Browser.thread(id).address != "https://example.com/next?q=private" {
		t.Fatal("a status update overwrote the edited address")
	}
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if request.Actor != browser.ActorHuman || request.Page != status.Page || request.URL != "https://example.com/next?q=private" || openDone == nil {
		t.Fatalf("address Enter did not dispatch the exact Human request: %+v", request)
	}
	if v.C.State.Thread(id).Draft != "Keep this chat draft" || len(v.C.State.Thread(id).Turns) != 0 {
		t.Fatal("address Enter modified or sent the chat draft")
	}
	status.Page.Navigation++
	status.URL = "https://example.com/next"
	openDone(browser.ActionResult{Page: status.Page, Dispatch: browser.DispatchSent, Completion: browser.CompletionObserved, Evidence: "navigation observed"}, status, nil)
	tt.Frame()
	if v.Browser.thread(id).address != status.URL || v.Browser.thread(id).err != nil {
		t.Fatal("confirmed navigation did not publish the sanitized URL")
	}
	browserClick(t, tt, v.texts().Message())
	tt.Type(" 中文补充")
	if !strings.HasSuffix(v.C.State.Thread(id).Draft, " 中文补充") || v.Browser.thread(id).address != status.URL {
		t.Fatal("composer input interfered with the address")
	}
	for _, address := range []string{"javascript:alert(1)", "file:///etc/passwd", "https://user:password@example.com/", "example.com", "https://example.com/\nsecret", "https://example.com\\next"} {
		openDone = nil
		v.Browser.thread(id).address = address
		v.browserOpen(id)
		if openDone != nil || v.Browser.thread(id).err != browser.ErrInvalidArgument {
			t.Fatalf("invalid address dispatched: %q", address)
		}
	}
}

func TestBrowserOpenNeverInfersSuccess(t *testing.T) {
	for _, result := range []browser.ActionResult{
		{},
		{Dispatch: browser.DispatchSent},
		{Dispatch: browser.DispatchSent, Completion: browser.CompletionObserved},
		{Dispatch: browser.DispatchUnknown, Completion: browser.CompletionObserved, Evidence: "untrusted claim"},
	} {
		v, id, status := browserTestView(t)
		result.Page = status.Page
		v.Browser.Actions.Open = func(_ browser.OpenRequest, reply func(browser.ActionResult, browser.Status, error)) {
			reply(result, status, nil)
		}
		v.browserOpen(id)
		if v.Browser.thread(id).err != browser.ErrUnconfirmed {
			t.Fatalf("incomplete action result reported success: %+v", result)
		}
	}
	v, id, _ := browserTestView(t)
	v.Browser.Actions.Open = func(_ browser.OpenRequest, reply func(browser.ActionResult, browser.Status, error)) {
		reply(browser.ActionResult{}, browser.Status{}, context.DeadlineExceeded)
	}
	v.browserOpen(id)
	if v.Browser.thread(id).interactive() {
		t.Fatal("missing authoritative status after timeout unlocked the old Human page")
	}
}

func TestBrowserCollapseCloseAndReplacement(t *testing.T) {
	v, id, status := browserTestView(t)
	var closeDone, createDone BrowserReply
	v.Browser.Actions.Close = func(key browser.PageKey, done BrowserReply) {
		if key != status.Page.PageKey {
			t.Fatal("closed the wrong page")
		}
		closeDone = done
	}
	v.Browser.Actions.Create = func(threadID string, done BrowserReply) {
		if threadID != id {
			t.Fatal("created a page for another conversation")
		}
		createDone = done
	}
	v.Browser.Actions.TakeOver = func(browser.PageKey, BrowserReply) { t.Fatal("collapse took over the page") }
	tt := ui.NewTester(v.Render, 1140, 780)
	browserClick(t, tt, v.texts().BrowserHide())
	if _, ok := tt.Find(v.texts().BrowserPanel()); ok || closeDone != nil || createDone != nil {
		t.Fatal("collapse changed the page lifecycle")
	}
	browserClick(t, tt, v.texts().BrowserShow())
	if _, ok := tt.Find(v.texts().BrowserPanel()); !ok || createDone != nil {
		t.Fatal("showing an existing page recreated it")
	}
	browserClick(t, tt, v.texts().BrowserClose())
	if closeDone == nil || v.Browser.thread(id).status.State == browser.StateClosed {
		t.Fatal("close was not dispatched or was reported complete prematurely")
	}
	closed := status
	closed.State, closed.Busy = browser.StateClosed, true
	closeDone(closed, context.DeadlineExceeded)
	tt.Frame()
	browserClick(t, tt, v.texts().BrowserOpen())
	if createDone != nil {
		t.Fatal("recreated a page before old resources were destroyed")
	}
	closed.Busy = false
	v.Browser.SetStatus(closed)
	tt.Frame()
	browserClick(t, tt, v.texts().BrowserOpen())
	if createDone == nil {
		t.Fatal("could not reopen a destroyed page")
	}
	replacement := status
	replacement.Page.PageID = "replacement-page"
	replacement.URL = ""
	createDone(replacement, nil)
	v.Browser.SetStatus(status)
	if v.Browser.thread(id).status.Page.PageID != "replacement-page" || v.Browser.thread(id).address != "" {
		t.Fatal("late snapshot resurrected the old page or retained its address")
	}
}

func TestBrowserSwitchRejectsLateReplies(t *testing.T) {
	v, first, status := browserTestView(t)
	other := v.C.State.AddThread("", "")
	otherStatus := status
	otherStatus.Page.ThreadID, otherStatus.Page.PageID = other.ID, "page-b"
	otherStatus.URL = "https://other.example/second"
	v.Browser.SetStatus(otherStatus)
	v.Browser.thread(other.ID).visible = true
	firstPage, otherPage := &mygo.WebView{}, &mygo.WebView{}
	v.Browser.Page = func(key browser.PageKey) ui.NativeWebView {
		switch key {
		case status.Page.PageKey:
			return firstPage
		case otherStatus.Page.PageKey:
			return otherPage
		default:
			t.Fatalf("unexpected page: %+v", key)
			return nil
		}
	}
	var openDone func(browser.ActionResult, browser.Status, error)
	var takeOverDone BrowserReply
	var takenOver browser.PageKey
	v.Browser.Actions.Open = func(_ browser.OpenRequest, done func(browser.ActionResult, browser.Status, error)) { openDone = done }
	v.Browser.Actions.TakeOver = func(key browser.PageKey, done BrowserReply) { takenOver, takeOverDone = key, done }
	v.C.State.Selected = first
	tt := ui.NewTester(v.Render, 1140, 780)
	v.browserOpen(first)
	v.C.Select(other.ID)
	tt.Frame()
	if takenOver != status.Page.PageKey || takeOverDone == nil || v.Browser.thread(first).pending != "takeover" {
		t.Fatal("switching conversations did not revoke the old page's control")
	}
	bounds, ok := tt.Find(v.texts().BrowserViewport())
	if !ok || tt.WebViewAt(bounds.X+bounds.W/2, bounds.Y+bounds.H/2) != otherPage || v.Browser.thread(other.ID).address != otherStatus.URL {
		t.Fatal("the selected conversation displays another page or address")
	}
	openDone(browser.ActionResult{Page: status.Page, Dispatch: browser.DispatchSent, Completion: browser.CompletionObserved, Evidence: "late navigation"}, status, nil)
	if v.Browser.thread(first).pending != "takeover" || v.Browser.thread(other.ID).status != otherStatus {
		t.Fatal("a late open reply changed the current page or acknowledged takeover")
	}
	takeOverDone(status, nil)
	if v.Browser.thread(first).pending != "" || v.Browser.thread(other.ID).status != otherStatus {
		t.Fatal("takeover completion changed another conversation")
	}
	newer := status
	newer.Page.Navigation = 3
	v.Browser.SetStatus(newer)
	v.Browser.SetStatus(status)
	if v.Browser.thread(first).status.Page.Navigation != 3 {
		t.Fatal("a late snapshot reversed navigation")
	}
}

func TestBrowserLateCreateIsTakenOver(t *testing.T) {
	v, id, status := browserTestView(t)
	v.Browser.thread(id).status = browser.Status{}
	var createDone BrowserReply
	var takenOver browser.PageKey
	v.Browser.Actions.Create = func(_ string, done BrowserReply) { createDone = done }
	v.Browser.Actions.TakeOver = func(key browser.PageKey, _ BrowserReply) { takenOver = key }
	tt := ui.NewTester(v.Render, 1140, 780)
	browserClick(t, tt, v.texts().BrowserOpen())
	other := v.C.State.AddThread("", "")
	v.C.Select(other.ID)
	tt.Frame()
	createDone(status, nil)
	if takenOver != status.Page.PageKey || v.Browser.thread(id).pending != "takeover" {
		t.Fatal("late page creation left a background page unguarded")
	}
}

func TestBrowserResizeAndModalLayers(t *testing.T) {
	v, id, status := browserTestView(t)
	page := &mygo.WebView{}
	v.Browser.Page = func(browser.PageKey) ui.NativeWebView { return page }
	v.Browser.Actions.Open = func(browser.OpenRequest, func(browser.ActionResult, browser.Status, error)) {}
	tt := ui.NewTester(v.Render, 1440, 900)
	browserClick(t, tt, v.texts().BrowserAddress())
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("https://example.com/unsent")
	for _, size := range [][2]int{{840, 560}, {1140, 780}, {1440, 900}} {
		tt.SetSize(size[0], size[1])
		tt.Frame()
		if !tt.Focused(v.texts().BrowserAddress()) || v.Browser.thread(id).address != "https://example.com/unsent" || v.C.State.Thread(id).Draft != "Keep this chat draft" || !browserPageAcceptsInput(t, tt) {
			t.Fatal("resizing changed input focus, a draft, or page identity")
		}
	}
	v.Settings = true
	tt.Frame()
	if browserPageAcceptsInput(t, tt) {
		t.Fatal("native page takes input through settings")
	}
	v.Settings = false
	tt.Frame()
	tt.Advance(250 * time.Millisecond)
	if !browserPageAcceptsInput(t, tt) {
		t.Fatal("native page did not return after settings closed")
	}
	v.Browser.thread(id).confirm = true
	tt.Frame()
	if browserPageAcceptsInput(t, tt) {
		t.Fatal("native page takes input through the permissions dialog")
	}
	v.Browser.thread(id).confirm = false
	v.Browser.thread(id).visible = false
	tt.Frame()
	tt.Frame()
	v.Browser.thread(id).visible = true
	tt.Frame()
	if !browserPageAcceptsInput(t, tt) || v.Browser.thread(id).status != status {
		t.Fatal("page identity or input did not survive disappearing and returning")
	}
	v.C.State.Settings.Language = "en"
	tt.SetLocale("en")
	tt.Frame()
	if !tt.HasText(v.texts().BrowserHuman()) || v.Browser.thread(id).address != "https://example.com/unsent" {
		t.Fatal("live language change lost browser state or kept old labels")
	}
}

func TestBrowserLayoutScreenshots(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, size := range [][2]int{{840, 560}, {1440, 900}} {
			for _, scene := range []string{"human", "agent", "loading", "draining", "error", "collapsed", "permissions", "capture-unavailable", "unavailable", "tools"} {
				t.Run(fmt.Sprintf("%s-%s-%dx%d", lang, scene, size[0], size[1]), func(t *testing.T) {
					v, id, status := browserTestView(t)
					v.C.State.Settings.Language = lang
					status.CaptureSupported = scene != "capture-unavailable"
					v.C.State.Thread(id).Turns = []model.Turn{{Prompt: "Inspect the page layout", Status: "completed", Items: []model.Item{{Kind: "agentMessage", Text: "The browser and conversation share this workspace.", Status: "completed"}}}}
					if scene == "tools" {
						v.C.State.Thread(id).Turns[0].Items = []model.Item{
							{ID: "fill", Kind: "toolCall", Status: "completed", MCP: &model.MCPCall{Tool: "browser_fill", Result: `{"dispatch":"dispatched","completion":"observed"}`}},
							{ID: "capture", Kind: "toolCall", Status: "completed", MCP: &model.MCPCall{Tool: "browser_capture", Result: `{"width":776,"height":1412}`}},
							{ID: "click", Kind: "toolCall", Status: "interrupted", MCP: &model.MCPCall{Tool: "browser_click", Result: `{"dispatch":"unknown","error":{"code":"cancelled"}}`}},
							{ID: "opaque", Kind: "toolCall", Status: "failed", MCP: &model.MCPCall{Tool: "browser_capture", Result: `{"dispatch":"not_dispatched","error":{"code":"sensitive"}}`}},
						}
						*v.openState("activity-" + id + "-0-0") = true
						for i := 0; i < 4; i++ {
							*v.openState(fmt.Sprintf("%s-0-0-%d", id, i)) = true
						}
					}
					v.Browser.Actions.Resume = func(browser.PageKey, browser.Permissions, BrowserReply) {}
					v.Browser.Actions.TakeOver = func(browser.PageKey, BrowserReply) {}
					v.Browser.Actions.Open = func(browser.OpenRequest, func(browser.ActionResult, browser.Status, error)) {}
					switch scene {
					case "agent", "collapsed":
						status.State, status.Busy = browser.StateAgent, true
					case "loading":
						status.Loading = true
					case "draining":
						status.Busy = true
					case "error":
						status.LastError = fmt.Errorf("private-token-should-never-render: %w", browser.ErrEngine)
					}
					v.Browser.SetStatus(status)
					v.Browser.thread(id).visible = scene != "collapsed"
					v.Browser.thread(id).confirm = scene == "permissions" || scene == "capture-unavailable"
					if scene == "unavailable" {
						v.Browser.thread(id).status = browser.Status{}
					}
					tt := ui.NewTester(v.Render, size[0], size[1])
					tt.SetLocale(lang)
					tt.Frame()
					tt.Advance(300 * time.Millisecond)
					if tt.HasText("private-token-should-never-render") {
						t.Fatal("engine details leaked into the UI")
					}
					if scene == "tools" {
						detail, _ := v.browserActivityText(&v.C.State.Thread(id).Turns[0].Items[3], true)
						if !tt.HasText(v.texts().BrowserToolFill()) || tt.HasText("browser_fill") || !strings.Contains(detail, v.texts().BrowserSensitive()) {
							t.Fatal("browser activities did not use localized titles/errors")
						}
					}
					for _, label := range []string{v.texts().Message(), v.texts().BrowserShow()} {
						if label == v.texts().BrowserShow() && scene != "collapsed" {
							label = v.texts().BrowserHide()
						}
						bounds, ok := tt.Find(label)
						if !ok || bounds.W <= 0 || bounds.H <= 0 || bounds.X < 0 || bounds.Y < 0 || bounds.X+bounds.W > float32(size[0]) || bounds.Y+bounds.H > float32(size[1]) {
							t.Fatalf("%s is missing or outside the window: %+v", label, bounds)
						}
					}
					if scene != "collapsed" {
						panel, _ := tt.Find(v.texts().BrowserPanel())
						for _, label := range []string{v.texts().BrowserAddress(), v.texts().BrowserGo(), v.texts().BrowserViewport()} {
							bounds, ok := tt.Find(label)
							if !ok || bounds.W <= 0 || bounds.H <= 0 || bounds.X < panel.X || bounds.Y < panel.Y || bounds.X+bounds.W > panel.X+panel.W+1 || bounds.Y+bounds.H > panel.Y+panel.H+1 {
								t.Fatalf("%s is missing or outside the browser panel: %+v panel=%+v", label, bounds, panel)
							}
						}
					} else if !tt.HasText(v.texts().BrowserHiddenActive()) {
						t.Fatal("collapsed panel hid the active automation status")
					}
					if output := os.Getenv("MUJICAGENT_BROWSER_SCREENSHOTS"); output != "" {
						if err := os.MkdirAll(output, 0700); err != nil {
							t.Fatal(err)
						}
						path := filepath.Join(output, fmt.Sprintf("%s-%s-%dx%d.png", lang, scene, size[0], size[1]))
						f, err := os.Create(path)
						if err != nil {
							t.Fatal(err)
						}
						err = png.Encode(f, tt.Image())
						closeErr := f.Close()
						if err != nil || closeErr != nil {
							t.Fatalf("screenshot: %v; close: %v", err, closeErr)
						}
					}
				})
			}
		}
	}
}
