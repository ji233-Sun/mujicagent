//go:build darwin

package view

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/browser"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

// Native acceptance is opt-in. It uses real WebViews and explicit scene state,
// never desktop input automation. Live inference requires a separate opt-in.
func TestMain(m *testing.M) {
	if os.Getenv("MUJICAGENT_BROWSER_NATIVE_TEST") != "1" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "mujicagent-browser-native-*")
	if err != nil {
		panic(err)
	}
	mygo.App.SetPath(mygo.PathUserData, dir)
	mygo.Bind(BrowserNativeProbe{})
	mygo.App.OnWindowAllClosed(func() {})
	var code atomic.Int32
	code.Store(1)
	mygo.App.WhenReady(func() {
		go func() {
			code.Store(int32(m.Run()))
			mygo.App.Quit()
		}()
	})
	if err := mygo.App.Run(); err != nil {
		fmt.Fprintln(os.Stderr, err)
	} else {
		_ = os.RemoveAll(dir)
		os.Exit(int(code.Load()))
	}
	_ = os.RemoveAll(dir)
	os.Exit(1)
}

type nativeBrowserScene struct {
	v       *View
	win     *mygo.Window
	runtime *browser.Runtime
	thread  string
}

func (s *nativeBrowserScene) update(t *testing.T, fn func()) {
	t.Helper()
	done := make(chan struct{})
	s.win.Update(func() { defer close(done); fn() })
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("native UI update timed out")
	}
}

func newNativeBrowserScene(t *testing.T) *nativeBrowserScene {
	t.Helper()
	if os.Getenv("MUJICAGENT_BROWSER_NATIVE_TEST") != "1" {
		t.Skip("native WebView acceptance not requested")
	}
	state := model.New()
	thread := state.AddThread("", t.TempDir())
	dir := t.TempDir()
	s := &nativeBrowserScene{thread: thread.ID}
	// Native window creation marshals to the main thread; scene changes below
	// always use Window.Update, including teardown.
	var v *View
	c := controller.New(dir, state, func() {
		if s.win != nil {
			s.win.Invalidate()
		}
	})
	v = New(c)
	s.v = v
	s.win = mygo.NewWindow(mygo.WindowOptions{Title: "MujicAgent BrowserUse acceptance", Width: 1140, Height: 900,
		Content: ui.View(v.Render), TitleBarStyle: mygo.TitleBarHiddenInset, TitleBarHeight: 54})
	s.runtime = browser.New(s.win, func(snapshot browser.Status) {
		s.win.Update(func() {
			status, err := s.runtime.Status(context.Background(), snapshot.Page.ThreadID)
			if err == nil {
				v.Browser.SetStatus(status)
			}
		})
	})
	s.update(t, func() {
		v.Window = s.win
		c.ConfigureBrowser(s.runtime)
		v.AttachBrowser(s.runtime)
		v.Browser.thread(thread.ID).visible = true
	})
	t.Cleanup(func() {
		s.update(t, func() { c.Close(); s.win.Destroy() })
	})
	status, err := s.runtime.Create(context.Background(), thread.ID)
	if err != nil {
		t.Fatal(err)
	}
	s.update(t, func() { v.Browser.SetStatus(status) })
	return s
}

func TestBrowserNativeResumeThenSwitch(t *testing.T) {
	s := newNativeBrowserScene(t)
	status := s.status(t, s.thread)
	takenOver := make(chan struct{})
	s.update(t, func() {
		s.v.Browser.selectThread(s.thread)
		takeOver := s.v.Browser.Actions.TakeOver
		s.v.Browser.Actions.TakeOver = func(key browser.PageKey, reply BrowserReply) {
			takeOver(key, func(status browser.Status, err error) { reply(status, err); close(takenOver) })
		}
		replied := false
		s.v.Browser.Actions.Resume(status.Page.PageKey, browser.Permissions{Interact: true}, func(status browser.Status, err error) {
			replied = true
			s.v.Browser.SetStatus(status)
			if err != nil {
				t.Error(err)
			}
		})
		if !replied {
			t.Error("Resume left an authorization worker that can overtake switching")
		}
		other := s.v.C.State.AddThread("", "")
		s.v.Browser.selectThread(other.ID)
		current, err := s.runtime.Status(context.Background(), s.thread)
		if err != nil || current.State != browser.StateHuman || current.Permissions != (browser.Permissions{}) {
			t.Error("switch did not synchronously revoke resume")
		}
	})
	select {
	case <-takenOver:
	case <-time.After(3 * time.Second):
		t.Fatal("switch takeover did not finish")
	}
	status = s.status(t, s.thread)
	if status.State != browser.StateHuman || status.Permissions != (browser.Permissions{}) {
		t.Fatal("old Resume regained access after switching")
	}
}

func (s *nativeBrowserScene) status(t *testing.T, thread string) browser.Status {
	t.Helper()
	status, err := s.runtime.Status(context.Background(), thread)
	if err != nil {
		t.Fatal(err)
	}
	return status
}

func nativeFixtureServer(t *testing.T) *httptest.Server {
	t.Helper()
	path, err := filepath.Abs("../browser/testdata")
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.FileServer(http.Dir(path)))
	t.Cleanup(server.Close)
	return server
}

func nativeElement(t *testing.T, observation browser.Observation, name string) browser.ElementRef {
	t.Helper()
	for _, e := range observation.Elements {
		if e.Name == name {
			return e.Ref
		}
	}
	t.Fatalf("element %q missing in %d elements", name, len(observation.Elements))
	return browser.ElementRef{}
}

var nativePrivateCalls atomic.Int32

type BrowserNativeProbe struct{}

func (BrowserNativeProbe) Touch() bool {
	nativePrivateCalls.Add(1)
	return true
}

func TestBrowserNativeRuntime(t *testing.T) {
	s := newNativeBrowserScene(t)
	server := nativeFixtureServer(t)
	ctx := context.Background()
	initial := s.status(t, s.thread)
	result, err := s.runtime.Open(ctx, browser.OpenRequest{Request: browser.Request{Page: initial.Page, Actor: browser.ActorHuman},
		URL: server.URL + "/acceptance-simple.html"})
	if err != nil || result.Completion != browser.CompletionObserved {
		t.Fatalf("open: %+v / %v", result, err)
	}
	status := s.status(t, s.thread)
	if _, err := s.runtime.Resume(ctx, status.Page.PageKey, browser.Permissions{Navigate: true, Interact: true, Capture: true}); err != nil {
		t.Fatal(err)
	}
	request := browser.Request{Page: status.Page, Actor: browser.ActorAgent}
	observation, err := s.runtime.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.runtime.Fill(ctx, browser.FillRequest{Request: request, Element: nativeElement(t, observation, "Controlled text"), Text: "fixture-native"}); err != nil {
		t.Fatalf("controlled fill: %v", err)
	}
	observation, err = s.runtime.Observe(ctx, request)
	if err != nil || !strings.Contains(observation.Text, "Model: fixture-native") {
		t.Fatalf("controlled model not updated: %v", err)
	}
	if _, err := s.runtime.Click(ctx, browser.ClickRequest{Request: request, Element: nativeElement(t, observation, "Commit controlled text")}); err != nil {
		t.Fatalf("commit: %v", err)
	}
	observation, err = s.runtime.Observe(ctx, request)
	if err != nil || !strings.Contains(observation.Text, "Committed: fixture-native") {
		t.Fatalf("commit not observed: %v", err)
	}
	if capture, err := s.runtime.Capture(ctx, request); !errors.Is(err, browser.ErrUnsupported) || len(capture.PNG) != 0 {
		t.Fatalf("untrusted capture must fail closed: %v", err)
	}
	observation, err = s.runtime.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	oldRef := nativeElement(t, observation, "Controlled text")
	native := s.runtime.View(status.Page.PageKey).(*mygo.WebView)
	if _, err := native.Page().EvalContext(ctx, `document.getElementById("controlled").setAttribute("aria-label", "Changed identity"); return true`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runtime.Fill(ctx, browser.FillRequest{Request: request, Element: oldRef, Text: "must-not-write"}); !errors.Is(err, browser.ErrStale) {
		t.Fatalf("DOM mutation accepted old reference: %v", err)
	}
	if _, err := native.Page().EvalContext(ctx, `document.getElementById("controlled").removeAttribute("aria-label"); return true`); err != nil {
		t.Fatal(err)
	}
	observation, err = s.runtime.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.runtime.Fill(ctx, browser.FillRequest{Request: request, Element: nativeElement(t, observation, "Form query"), Text: "fixture-nav"}); err != nil {
		t.Fatalf("query fill: %v", err)
	}
	observation, err = s.runtime.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.runtime.Click(ctx, browser.ClickRequest{Request: request, Element: nativeElement(t, observation, "Navigate with form")}); err != nil {
		t.Fatalf("form navigation: %v", err)
	}
	status = s.status(t, s.thread)
	if status.Page.Navigation <= request.Page.Navigation {
		t.Fatal("form did not advance host navigation generation")
	}
	if _, err := s.runtime.Observe(ctx, request); !errors.Is(err, browser.ErrStale) {
		t.Fatalf("old document accepted: %v", err)
	}
	request.Page = status.Page
	observation, err = s.runtime.Observe(ctx, request)
	if err != nil || !strings.Contains(observation.Text, "Navigation complete: fixture-nav") {
		t.Fatalf("form did not actually navigate: %v / synthetic text=%q", err, observation.Text)
	}
	if _, err := s.runtime.Scroll(ctx, browser.ScrollRequest{Request: request, DeltaY: 1200}); err != nil {
		t.Fatalf("scroll: %v", err)
	}
	_, _ = s.runtime.TakeOver(ctx, status.Page.PageKey)
	// Test storage isolation with synthetic values; never log raw cookies.
	first := s.runtime.View(status.Page.PageKey).(*mygo.WebView)
	if _, err := first.Page().EvalContext(ctx, `localStorage.setItem("fixture", "A"); document.cookie="fixture=A; Path=/"; return true`); err != nil {
		t.Fatal("could not set synthetic marker")
	}
	second, err := s.runtime.Create(ctx, "native-second")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.runtime.Open(ctx, browser.OpenRequest{Request: browser.Request{Page: second.Page, Actor: browser.ActorHuman}, URL: server.URL + "/acceptance-simple.html"})
	if err != nil {
		t.Fatal(err)
	}
	other := s.runtime.View(second.Page.PageKey).(*mygo.WebView)
	isolated, err := other.Page().EvalContext(ctx, `localStorage.getItem("fixture") === null && !document.cookie.includes("fixture=")`)
	if err != nil || isolated != true {
		t.Fatal("native ephemeral sessions share storage")
	}
	if _, err := other.Page().EvalContext(ctx, `localStorage.setItem("fixture", "B"); document.cookie="fixture=B; Path=/"; return true`); err != nil {
		t.Fatal("could not set second synthetic marker")
	}
	_, _ = s.runtime.Close(ctx, second.Page.PageKey)
	replacement, err := s.runtime.Create(ctx, "native-second")
	if err != nil || replacement.Page.PageID == second.Page.PageID {
		t.Fatalf("native replacement: %v", err)
	}
	_, err = s.runtime.Open(ctx, browser.OpenRequest{Request: browser.Request{Page: replacement.Page, Actor: browser.ActorHuman}, URL: server.URL + "/acceptance-simple.html"})
	if err != nil {
		t.Fatal(err)
	}
	cleanPage := s.runtime.View(replacement.Page.PageKey).(*mygo.WebView)
	empty, err := cleanPage.Page().EvalContext(ctx, `localStorage.getItem("fixture") === null && !document.cookie.includes("fixture=")`)
	if err != nil || empty != true {
		t.Fatal("replacement native session retained storage")
	}
	// Native history navigation bypasses MyGo's OnWillNavigate. Returning to
	// another previously human-opened origin must revoke the current grant.
	peer := nativeFixtureServer(t)
	status = s.status(t, s.thread)
	if _, err := s.runtime.Open(ctx, browser.OpenRequest{Request: browser.Request{Page: status.Page, Actor: browser.ActorHuman}, URL: peer.URL + "/acceptance-simple.html"}); err != nil {
		t.Fatal(err)
	}
	status = s.status(t, s.thread)
	if _, err := s.runtime.Resume(ctx, status.Page.PageKey, browser.Permissions{Interact: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := first.Page().EvalContext(ctx, `history.back(); return true`); err != nil {
		t.Fatal(err)
	}
	deadline := time.NewTimer(3 * time.Second)
	ticker := time.NewTicker(10 * time.Millisecond)
	defer deadline.Stop()
	defer ticker.Stop()
	for {
		status = s.status(t, s.thread)
		if status.URL == server.URL && !status.Loading {
			break
		}
		select {
		case <-ticker.C:
		case <-deadline.C:
			t.Fatal("native history did not return to previous origin")
		}
	}
	request.Page = status.Page
	if status.State != browser.StateHuman || status.Permissions != (browser.Permissions{}) {
		t.Fatal("history navigation retained another origin's grant")
	}
	if _, err := s.runtime.Observe(ctx, request); !errors.Is(err, browser.ErrPermissionDenied) {
		t.Fatalf("cross-origin history observation: %v", err)
	}
	if _, err := s.runtime.Resume(ctx, status.Page.PageKey, browser.Permissions{Interact: true}); !errors.Is(err, browser.ErrPermissionDenied) {
		t.Fatalf("history origin reauthorized without address-bar action: %v", err)
	}
	if _, err := s.runtime.Open(ctx, browser.OpenRequest{Request: browser.Request{Page: status.Page, Actor: browser.ActorHuman}, URL: server.URL + "/acceptance-simple.html"}); err != nil {
		t.Fatal(err)
	}
	status = s.status(t, s.thread)
	request.Page = status.Page
	denied, err := first.Page().EvalContext(ctx, `return mygo.call("BrowserNativeProbe.Touch").then(() => false, () => true)`)
	if err != nil || denied != true || nativePrivateCalls.Load() != 0 {
		t.Fatal("untrusted native page reached private Go binding")
	}
	if _, err := first.Page().EvalContext(ctx, `document.body.insertAdjacentHTML("beforeend", '<input type="password" value="fixture-private">'); return true`); err != nil {
		t.Fatal(err)
	}
	_, _ = s.runtime.Resume(ctx, status.Page.PageKey, browser.Permissions{Capture: true})
	if _, err := s.runtime.Capture(ctx, request); !errors.Is(err, browser.ErrUnsupported) {
		t.Fatalf("sensitive capture accepted: %v", err)
	}
	observation, err = s.runtime.Observe(ctx, request)
	if err != nil || strings.Contains(observation.Text, "fixture-private") {
		t.Fatalf("password entered observation: %v", err)
	}
	if _, err := first.Page().EvalContext(ctx, `document.body.insertAdjacentHTML("beforeend", '<input type="password" value="q7"><div style="position:fixed;top:0;left:0">q7 Authorization: Bearer fixture-bearer-value</div><div style="position:fixed;top:24px;left:0">Authorization: Basic Zml4dHVyZTpwYXNzd29yZA==</div>'); document.title = "q7"; return true`); err != nil {
		t.Fatal(err)
	}
	observation, err = s.runtime.Observe(ctx, request)
	if err != nil || strings.Contains(observation.Text+observation.Title, "q7") || strings.Contains(observation.Text, "fixture-bearer-value") || strings.Contains(observation.Text, "Zml4dHVyZTpwYXNzd29yZA") {
		t.Fatalf("short password or bearer token entered observation: %v", err)
	}
	if _, err := first.Page().EvalContext(ctx, `document.body.insertAdjacentHTML("beforeend", '<textarea style="position:fixed;top:24px;left:0">fixture-private-draft</textarea><label>Message<textarea style="position:fixed;top:48px;left:0">fixture-private-draft</textarea></label>'); return true`); err != nil {
		t.Fatal(err)
	}
	observation, err = s.runtime.Observe(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	for _, element := range observation.Elements {
		if strings.Contains(element.Name, "fixture-private-draft") {
			t.Fatal("textarea initial value entered accessible name")
		}
	}
	if _, err := first.Page().EvalContext(ctx, `const original = JSON.stringify; JSON.stringify = () => original({ok:true,v:'{"code":"ok"}'}); return true`); err != nil {
		t.Fatal(err)
	}
	if capture, err := s.runtime.Capture(ctx, request); !errors.Is(err, browser.ErrUnsupported) || len(capture.PNG) != 0 {
		t.Fatalf("forged capture approval must not release a bitmap: %v", err)
	}
	if _, err := first.Page().EvalContext(ctx, `const saved = JSON.stringify; JSON.stringify = () => saved({ok:true,v:"fixture-tampered"}); return true`); err != nil {
		t.Fatal(err)
	}
	if _, err := s.runtime.Observe(ctx, request); !errors.Is(err, browser.ErrEngine) {
		t.Fatalf("tampered pageWorld result accepted: %v", err)
	}
	closed, err := s.runtime.Close(ctx, status.Page.PageKey)
	if err != nil || closed.State != browser.StateClosed || closed.Busy {
		t.Fatalf("native close: %v", err)
	}
	if _, err := s.runtime.Observe(ctx, request); !errors.Is(err, browser.ErrClosed) {
		t.Fatalf("closed native page accepted callback: %v", err)
	}
	t.Log("native controlled fill/click/observe/scroll passed; session isolation, textarea privacy and forged capture approval rejection passed; native capture unsupported")
}

func TestBrowserNativeCodex(t *testing.T) {
	if os.Getenv("MUJICAGENT_BROWSER_LIVE_TEST") != "1" {
		t.Skip("real Codex connection/model usage not requested")
	}
	s := newNativeBrowserScene(t)
	server := nativeFixtureServer(t)
	ctx := context.Background()
	status := s.status(t, s.thread)
	_, err := s.runtime.Open(ctx, browser.OpenRequest{Request: browser.Request{Page: status.Page, Actor: browser.ActorHuman},
		URL: server.URL + "/acceptance-simple.html"})
	if err != nil {
		t.Fatal(err)
	}
	status = s.status(t, s.thread)
	s.update(t, func() { s.v.C.Connect() })
	wait := func(label string, check func() bool) {
		t.Helper()
		timer := time.NewTicker(25 * time.Millisecond)
		defer timer.Stop()
		deadline := time.After(180 * time.Second)
		for {
			var done bool
			s.update(t, func() { s.v.C.Drain(); done = check() })
			if done {
				return
			}
			select {
			case <-timer.C:
			case <-deadline:
				t.Fatalf("%s timed out", label)
			}
		}
	}
	wait("Codex connection", func() bool { return s.v.C.Connection != "Connecting" })
	// Establishing/replacing the transport revokes previous permissions.
	// Grant only after connection, exactly as an explicit user resume does.
	_, err = s.runtime.Resume(ctx, status.Page.PageKey, browser.Permissions{Navigate: true, Interact: true, Capture: true})
	if err != nil {
		t.Fatal(err)
	}
	s.update(t, func() {
		c := s.v.C
		if c.Connection != "Connected" {
			t.Errorf("Codex connection failed: %s", c.Error)
			return
		}
		if selected := os.Getenv("MUJICAGENT_BROWSER_LIVE_MODEL"); selected != "" {
			c.State.Settings.Model = selected
		}
		t.Logf("Codex=%s model=%s", c.Version, c.State.Settings.Model)
		if !c.Send("Use only the six browser_* tools, without shell, files, external browsers or other tools. First use browser_open to open "+server.URL+"/acceptance-simple.html in the existing authorized page. Observe, fill Controlled text with fixture-live, observe again, click Commit controlled text, observe and confirm Committed: fixture-live. Scroll down 1000 pixels and observe. Finally call browser_capture once: this native engine must return unsupported; report that limitation explicitly and never claim an image was shared. Do not guess element IDs. Report BROWSER_LIVE_OK only after the other five tools succeed and capture is explicitly rejected. Stop on any other error.", false) {
			t.Errorf("Codex send failed: %s", c.Error)
		}
	})
	if t.Failed() {
		return
	}
	wait("Codex browser turn", func() bool { return !s.v.C.Running[s.thread] })
	var turn model.Turn
	s.update(t, func() {
		thread := s.v.C.State.Thread(s.thread)
		turn = thread.Turns[len(thread.Turns)-1]
	})
	text, tools := "", map[string]bool{}
	captureRejected := false
	for _, item := range turn.Items {
		if item.Kind == "agentMessage" {
			text += item.Text
		}
		if item.MCP != nil && strings.HasPrefix(item.MCP.Tool, "browser_") && item.Status == "completed" {
			tools[item.MCP.Tool] = true
		}
		if item.MCP != nil && item.MCP.Tool == "browser_capture" && item.Status == "failed" {
			var result struct{ Error *struct{ Code string } }
			if json.Unmarshal([]byte(item.MCP.Result), &result) == nil && result.Error != nil && result.Error.Code == "unsupported" {
				captureRejected = true
			}
		}
	}
	native := s.runtime.View(status.Page.PageKey).(*mygo.WebView)
	matches, err := native.Page().EvalContext(ctx, `document.getElementById("committed").textContent === "Committed: fixture-live" && scrollY > 0`)
	if turn.Status != "completed" || !strings.Contains(text, "BROWSER_LIVE_OK") || err != nil || matches != true || len(tools) < 5 || !captureRejected {
		t.Fatalf("live browser result: status=%s error=%s reply=%s pageMatches=%v captureRejected=%v", turn.Status, turn.Error, text, matches, captureRejected)
	}
	t.Logf("real Codex controlled the visible native PageKey; %d activity items; five tools and explicit capture refusal verified", len(turn.Items))
}
