package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/browser"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type browserTestCall struct {
	Tool           string
	Request        browser.Request
	Element        browser.ElementRef
	Text, URL      string
	DeltaX, DeltaY float64
	Deadline       time.Time
}

type browserTestBackend struct {
	mu       sync.Mutex
	statuses map[string]browser.Status
	calls    []browserTestCall
	entered  chan browserTestCall
	act      func(context.Context, browserTestCall) (browser.ActionResult, error)
	observe  func(context.Context, browser.Request) (browser.Observation, error)
	capture  func(context.Context, browser.Request) (browser.Screenshot, error)
	status   func(context.Context, string) (browser.Status, error)
	png      []byte
}

func newBrowserTestBackend(t *testing.T) *browserTestBackend {
	t.Helper()
	var data bytes.Buffer
	if err := png.Encode(&data, image.NewRGBA(image.Rect(0, 0, 2, 1))); err != nil {
		t.Fatal(err)
	}
	f := &browserTestBackend{statuses: map[string]browser.Status{}, entered: make(chan browserTestCall, 256), png: data.Bytes()}
	for _, suffix := range []string{"a", "b"} {
		page := browser.PageRef{PageKey: browser.PageKey{ThreadID: "local-" + suffix, PageID: "page-" + suffix}, Navigation: 1}
		f.statuses[page.ThreadID] = browser.Status{Page: page, State: browser.StateIdle, Permissions: browser.Permissions{Navigate: true, Interact: true, Capture: true}}
	}
	return f
}

func (f *browserTestBackend) record(ctx context.Context, call browserTestCall) (browser.ActionResult, error) {
	call.Deadline, _ = ctx.Deadline()
	f.mu.Lock()
	f.calls = append(f.calls, call)
	f.mu.Unlock()
	f.entered <- call
	if f.act != nil {
		return f.act(ctx, call)
	}
	page := call.Request.Page
	if call.Tool == codex.BrowserOpen {
		page.Navigation++
	}
	return browser.ActionResult{Page: page, Dispatch: browser.DispatchSent, Completion: browser.CompletionObserved, Evidence: "Observed viewport changed."}, nil
}

func (f *browserTestBackend) snapshot() []browserTestCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]browserTestCall(nil), f.calls...)
}

func (f *browserTestBackend) Open(ctx context.Context, req browser.OpenRequest) (browser.ActionResult, error) {
	return f.record(ctx, browserTestCall{Tool: codex.BrowserOpen, Request: req.Request, URL: req.URL})
}
func (f *browserTestBackend) Click(ctx context.Context, req browser.ClickRequest) (browser.ActionResult, error) {
	return f.record(ctx, browserTestCall{Tool: codex.BrowserClick, Request: req.Request, Element: req.Element})
}
func (f *browserTestBackend) Fill(ctx context.Context, req browser.FillRequest) (browser.ActionResult, error) {
	return f.record(ctx, browserTestCall{Tool: codex.BrowserFill, Request: req.Request, Element: req.Element, Text: req.Text})
}
func (f *browserTestBackend) Scroll(ctx context.Context, req browser.ScrollRequest) (browser.ActionResult, error) {
	return f.record(ctx, browserTestCall{Tool: codex.BrowserScroll, Request: req.Request, DeltaX: req.DeltaX, DeltaY: req.DeltaY})
}
func (f *browserTestBackend) Observe(ctx context.Context, req browser.Request) (browser.Observation, error) {
	f.record(ctx, browserTestCall{Tool: codex.BrowserObserve, Request: req})
	if f.observe != nil {
		return f.observe(ctx, req)
	}
	return browser.Observation{Page: req.Page, ObservationID: "observation-1", URL: "https://user:private@example.com/path?token=private#private", Title: "Page title", Text: "Visible page data password=private", Elements: []browser.Element{
		{Ref: browser.ElementRef{PageRef: req.Page, ObservationID: "observation-1", ID: "element-1"}, Role: "textbox", Name: "Display name", CanFill: true, CanClick: true},
	}}, nil
}
func (f *browserTestBackend) Capture(ctx context.Context, req browser.Request) (browser.Screenshot, error) {
	f.record(ctx, browserTestCall{Tool: codex.BrowserCapture, Request: req})
	if f.capture != nil {
		return f.capture(ctx, req)
	}
	return browser.Screenshot{Page: req.Page, PNG: f.png, Width: 2, Height: 1}, nil
}
func (f *browserTestBackend) Status(ctx context.Context, threadID string) (browser.Status, error) {
	if f.status != nil {
		return f.status(ctx, threadID)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	status, ok := f.statuses[threadID]
	if !ok {
		return browser.Status{}, browser.ErrNotFound
	}
	return status, nil
}
func (*browserTestBackend) Create(context.Context, string) (browser.Status, error) {
	return browser.Status{}, browser.ErrUnsupported
}
func (*browserTestBackend) TakeOver(context.Context, browser.PageKey) (browser.Status, error) {
	return browser.Status{}, browser.ErrUnsupported
}
func (*browserTestBackend) Resume(context.Context, browser.PageKey, browser.Permissions) (browser.Status, error) {
	return browser.Status{}, browser.ErrUnsupported
}
func (*browserTestBackend) Close(context.Context, browser.PageKey) (browser.Status, error) {
	return browser.Status{}, browser.ErrUnsupported
}

type browserTestReply struct {
	id       json.RawMessage
	response codex.DynamicToolResponse
}

type browserTestRuntime struct{ *browserTestBackend }

func (*browserTestRuntime) Shutdown() {}

func TestBrowserContextIsCodexOnly(t *testing.T) {
	c := New(t.TempDir(), model.New(), nil)
	t.Cleanup(c.Close)
	thread := &model.Thread{ID: "local-a"}
	c.State.Threads = []*model.Thread{thread}
	c.browser = &browserTestRuntime{newBrowserTestBackend(t)}
	c.browserDispatcher = NewBrowserToolDispatcher(c.browser, c.browser, nil, nil)
	for _, driver := range []string{"pi", "droid", "claude", "codex"} {
		thread.Harness = driver
		prompt, active := c.browserPrompt(thread.ID)
		if (driver == "codex") != active || (prompt != "") != active {
			t.Fatalf("%s received wrong browser context: active=%v", driver, active)
		}
	}
}

func newBrowserTestDispatcher(t *testing.T, f *browserTestBackend) (*BrowserToolDispatcher, chan browserTestReply, chan BrowserToolActivity) {
	t.Helper()
	replies := make(chan browserTestReply, 256)
	events := make(chan BrowserToolActivity, 256)
	d := NewBrowserToolDispatcher(f, f, func(id json.RawMessage, result any) error {
		replies <- browserTestReply{append(json.RawMessage(nil), id...), result.(codex.DynamicToolResponse)}
		return nil
	}, func(event BrowserToolActivity) { events <- event })
	t.Cleanup(d.Disconnect)
	bindBrowserTestTurn(t, d, context.Background(), "a", "turn-a")
	bindBrowserTestTurn(t, d, context.Background(), "b", "turn-b")
	return d, replies, events
}

func bindBrowserTestTurn(t *testing.T, d *BrowserToolDispatcher, ctx context.Context, suffix, turn string) {
	t.Helper()
	if err := d.BindTurn(ctx, "server-"+suffix, turn, browser.PageKey{ThreadID: "local-" + suffix, PageID: "page-" + suffix}); err != nil {
		t.Fatal(err)
	}
}

func browserTestMessage(id, suffix, turn, call, tool, args string) codex.Message {
	params, _ := json.Marshal(codex.DynamicToolCall{ThreadID: "server-" + suffix, TurnID: turn, CallID: call, Tool: tool, Arguments: json.RawMessage(args)})
	return codex.Message{ID: json.RawMessage(id), Method: "item/tool/call", Params: params}
}

func readBrowserTestReply(t *testing.T, replies <-chan browserTestReply) browserTestReply {
	t.Helper()
	select {
	case reply := <-replies:
		return reply
	case <-time.After(3 * time.Second):
		t.Fatal("browser request left pending")
		return browserTestReply{}
	}
}

func readBrowserTestFinal(t *testing.T, events <-chan BrowserToolActivity) BrowserToolActivity {
	t.Helper()
	deadline := time.After(3 * time.Second)
	for {
		select {
		case event := <-events:
			if event.Event.Status != "inProgress" {
				return event
			}
		case <-deadline:
			t.Fatal("browser activity left pending")
			return BrowserToolActivity{}
		}
	}
}

func browserTestOutput(t *testing.T, response codex.DynamicToolResponse) browserToolOutput {
	t.Helper()
	var output browserToolOutput
	if len(response.ContentItems) == 0 || response.ContentItems[0].Type != "inputText" || json.Unmarshal([]byte(response.ContentItems[0].Text), &output) != nil {
		t.Fatalf("invalid dynamic tool result: %+v", response)
	}
	return output
}

func TestBrowserExplicitResumeRearmsOnlyCancelledOwner(t *testing.T) {
	backend := newBrowserTestBackend(t)
	dispatcher, replies, _ := newBrowserTestDispatcher(t, backend)
	key := browser.PageKey{ThreadID: "local-a", PageID: "page-a"}
	dispatcher.ClosePage(key)
	if err := dispatcher.BindTurn(context.Background(), "server-a", "turn-a", key); !errors.Is(err, browser.ErrStale) {
		t.Fatalf("ordinary notification revived paused owner: %v", err)
	}
	if err := dispatcher.ResumeTurn(context.Background(), "server-a", "turn-a", browser.PageKey{ThreadID: "local-a", PageID: "other"}); !errors.Is(err, browser.ErrStale) {
		t.Fatalf("wrong page revived owner: %v", err)
	}
	if err := dispatcher.ResumeTurn(context.Background(), "server-a", "turn-a", key); err != nil {
		t.Fatal(err)
	}
	message := browserTestMessage(`"resumed"`, "a", "turn-a", "after-resume", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`)
	dispatcher.Handle(message)
	reply := readBrowserTestReply(t, replies)
	if !reply.response.Success {
		t.Fatalf("explicitly resumed call failed: %+v", browserTestOutput(t, reply.response))
	}
	dispatcher.Handle(message)
	select {
	case <-replies:
		t.Fatal("explicit resume lost request deduplication")
	default:
	}
}

func TestBrowserDispatchDoesNotBlockFullUIQueue(t *testing.T) {
	backend := newBrowserTestBackend(t)
	dispatcher, _, _ := newBrowserTestDispatcher(t, backend)
	c := New(t.TempDir(), model.New(), nil)
	t.Cleanup(c.Close)
	c.browserDispatcher = dispatcher
	for i := 0; i < cap(c.events); i++ {
		c.events <- func() {}
	}
	started, finished, returned := make(chan struct{}), make(chan struct{}), make(chan struct{})
	dispatcher.emit = func(BrowserToolActivity) { close(started); c.post(func() {}); close(finished) }
	message := browserTestMessage(`"full-queue"`, "a", "turn-a", "invalid-observe", codex.BrowserObserve, `{"pageId":"page-a"}`)
	go func() { c.handle(message); close(returned) }()
	<-started
	select {
	case <-returned:
	case <-time.After(time.Second):
		<-c.events
		<-finished
		t.Fatal("browser dispatch blocked the UI queue consumer")
	}
	<-c.events
	<-finished
}

func TestBrowserToolsAppServerRoundTrip(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX test launcher")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	binary := filepath.Join(dir, "codex")
	launcher := fmt.Sprintf("#!/bin/sh\nexec '%s' -test.run=^TestServiceTierRequests$ -- \"$@\"\n", strings.ReplaceAll(exe, "'", "'\\''"))
	if err := os.WriteFile(binary, []byte(launcher), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MUJICAGENT_SPEED_CHILD", "1")
	t.Setenv("MUJICAGENT_SPEED_REQUEST", filepath.Join(dir, "reply.json"))
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	messages := make(chan codex.Message, 32)
	client, err := codex.Start(ctx, binary, func(m codex.Message) { messages <- m })
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	f := newBrowserTestBackend(t)
	events := make(chan BrowserToolActivity, 32)
	d := NewBrowserToolDispatcher(f, f, client.Reply, func(e BrowserToolActivity) { events <- e })
	defer d.Close()
	bindBrowserTestTurn(t, d, ctx, "a", "turn-a")
	requests := []codex.Message{
		browserTestMessage(`"request-\u0031"`, "a", "turn-a", "observe", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`),
		browserTestMessage(`9007199254740993`, "a", "turn-a", "capture", codex.BrowserCapture, `{"pageId":"page-a","navigation":1}`),
		browserTestMessage(`"failure"`, "a", "turn-a", "failure", codex.BrowserFill, `{"pageId":"page-a","navigation":1,"text":null}`),
	}
	if err := client.Call(ctx, "browser/requests", requests, nil); err != nil {
		t.Fatal(err)
	}
	for range requests {
		select {
		case m := <-messages:
			if !d.Handle(m) {
				t.Fatal("dynamic tool request not recognized")
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	for range requests {
		readBrowserTestFinal(t, events)
	}
	var replies []codex.Message
	if err := client.Call(ctx, "browser/replies", nil, &replies); err != nil {
		t.Fatal(err)
	}
	if len(replies) != 3 {
		t.Fatalf("reply count: %d", len(replies))
	}
	byID := map[string]codex.DynamicToolResponse{}
	for _, reply := range replies {
		var response codex.DynamicToolResponse
		if reply.Error != nil || json.Unmarshal(reply.Result, &response) != nil {
			t.Fatalf("tool failure used the wrong protocol response: %+v", reply)
		}
		byID[string(reply.ID)] = response
	}
	observe, ok := byID[string(requests[0].ID)]
	if !ok || !observe.Success || strings.Contains(observe.ContentItems[0].Text, "private") || !strings.Contains(observe.ContentItems[0].Text, "Visible page data") {
		t.Fatalf("text result or original escaped ID lost: %+v", byID)
	}
	capture := byID[string(requests[1].ID)]
	if !capture.Success || len(capture.ContentItems) != 2 || capture.ContentItems[1].Type != "inputImage" {
		t.Fatalf("image result or integer ID lost: %+v", capture)
	}
	payload, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(capture.ContentItems[1].ImageURL, "data:image/png;base64,"))
	if err != nil || !bytes.Equal(payload, f.png) {
		t.Fatal("screenshot bytes changed", err)
	}
	failure := byID[`"failure"`]
	if failure.Success || browserTestOutput(t, failure).Error.Code != "invalid_argument" {
		t.Fatal("missing explicit failure", failure)
	}
}

func TestBrowserToolRoutingAndActivity(t *testing.T) {
	f := newBrowserTestBackend(t)
	d, replies, events := newBrowserTestDispatcher(t, f)
	cases := []struct{ tool, args string }{
		{codex.BrowserOpen, `{"pageId":"page-a","navigation":1,"url":"https://example.com/private?token=private"}`},
		{codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`},
		{codex.BrowserClick, `{"pageId":"page-a","navigation":1,"observationId":"observation-1","elementId":"element-1"}`},
		{codex.BrowserFill, `{"pageId":"page-a","navigation":1,"observationId":"observation-1","elementId":"element-1","text":"private-value"}`},
		{codex.BrowserScroll, `{"pageId":"page-a","navigation":1,"deltaX":-10.5,"deltaY":40}`},
		{codex.BrowserCapture, `{"pageId":"page-a","navigation":1}`},
	}
	var turn model.Turn
	for i, tc := range cases {
		id := fmt.Sprintf(`"request-%d"`, i)
		before := time.Now()
		d.Handle(browserTestMessage(id, "a", "turn-a", tc.tool, tc.tool, tc.args))
		reply := readBrowserTestReply(t, replies)
		output := browserTestOutput(t, reply.response)
		if !reply.response.Success || string(reply.id) != id || output.PageID != "page-a" {
			t.Fatalf("unexpected success response: %+v %+v", reply, output)
		}
		if tc.tool == codex.BrowserOpen && output.Navigation != 2 {
			t.Fatal("navigation result did not keep new generation")
		}
		event := readBrowserTestFinal(t, events)
		if event.ThreadID != "local-a" || event.TurnID != "turn-a" {
			t.Fatal("activity used server or selected thread identity", event)
		}
		mergeAgentEvent(&turn, event.Event)
		calls := f.snapshot()
		call := calls[len(calls)-1]
		if call.Request.Actor != browser.ActorAgent || call.Request.Page.ThreadID != "local-a" || call.Request.Page.PageID != "page-a" || call.Deadline.IsZero() || call.Deadline.After(before.Add(browser.DefaultTimeout+time.Second)) {
			t.Fatalf("untrusted routing, actor, or unbounded context: %+v", call)
		}
		if tc.tool == codex.BrowserFill && (call.Text != "private-value" || call.Element.ObservationID != "observation-1" || call.Element.PageRef != call.Request.Page) {
			t.Fatal("fill or reference mapping lost", call)
		}
		if tc.tool == codex.BrowserScroll && (call.DeltaX != -10.5 || call.DeltaY != 40) {
			t.Fatal("scroll units or signs changed", call)
		}
	}
	data, _ := json.Marshal(turn)
	for _, secret := range []string{"private", "Visible page data", "Page title", "Display name", "Observed viewport", "base64", "imageUrl"} {
		if strings.Contains(string(data), secret) {
			t.Fatalf("page/input data persisted in activity: %s", data)
		}
	}
	if len(turn.Items) != 6 || turn.Items[3].MCP == nil || turn.Items[3].MCP.Server != "" || turn.Items[3].Kind != "toolCall" {
		t.Fatalf("browser activity did not reuse generic tool records: %+v", turn.Items)
	}
	// A different accepted turn routes to its own page, independent of selection.
	d.Handle(browserTestMessage(`"b"`, "b", "turn-b", "b", codex.BrowserObserve, `{"pageId":"page-b","navigation":1}`))
	if !readBrowserTestReply(t, replies).response.Success || f.snapshot()[6].Request.Page.ThreadID != "local-b" {
		t.Fatal("second thread did not remain isolated")
	}
}

func TestBrowserToolFailuresBeforeDispatch(t *testing.T) {
	for _, tc := range []struct {
		name, suffix, turn, tool, args, code string
		change                               func(*browser.Status)
	}{
		{"foreign page", "a", "turn-a", codex.BrowserObserve, `{"pageId":"page-b","navigation":1}`, "not_found", nil},
		{"unknown thread", "missing", "turn-a", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`, "not_found", nil},
		{"old turn", "a", "old", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`, "stale", nil},
		{"bad args", "a", "turn-a", codex.BrowserObserve, `{"pageId":"page-a","navigation":1,"actor":"human"}`, "invalid_argument", nil},
		{"unknown tool", "a", "turn-a", "run_shell", `{}`, "unsupported", nil},
		{"closed page", "a", "turn-a", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`, "closed", func(s *browser.Status) { s.State = browser.StateClosed }},
		{"replaced page", "a", "turn-a", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`, "not_found", func(s *browser.Status) { s.Page.PageID = "new-page" }},
		{"old generation", "a", "turn-a", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`, "stale", func(s *browser.Status) { s.Page.Navigation = 2 }},
		{"busy", "a", "turn-a", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`, "busy", func(s *browser.Status) { s.Busy = true }},
		{"loading", "a", "turn-a", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`, "busy", func(s *browser.Status) { s.Loading = true }},
		{"human", "a", "turn-a", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`, "permission_denied", func(s *browser.Status) { s.State = browser.StateHuman }},
		{"capture permission", "a", "turn-a", codex.BrowserCapture, `{"pageId":"page-a","navigation":1}`, "permission_denied", func(s *browser.Status) { s.Permissions.Capture = false }},
		{"navigate permission", "a", "turn-a", codex.BrowserOpen, `{"pageId":"page-a","navigation":1,"url":"https://example.com"}`, "permission_denied", func(s *browser.Status) { s.Permissions.Navigate = false }},
		{"interact permission", "a", "turn-a", codex.BrowserScroll, `{"pageId":"page-a","navigation":1,"deltaX":0,"deltaY":1}`, "permission_denied", func(s *browser.Status) { s.Permissions.Interact = false }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBrowserTestBackend(t)
			if tc.change != nil {
				s := f.statuses["local-a"]
				tc.change(&s)
				f.statuses["local-a"] = s
			}
			d, replies, _ := newBrowserTestDispatcher(t, f)
			d.Handle(browserTestMessage(`"fail"`, tc.suffix, tc.turn, "fail", tc.tool, tc.args))
			reply := readBrowserTestReply(t, replies)
			out := browserTestOutput(t, reply.response)
			if reply.response.Success || out.Error == nil || out.Error.Code != tc.code || out.Dispatch != browser.DispatchNone || len(f.snapshot()) != 0 {
				t.Fatalf("failure dispatched or was misclassified: %+v calls=%+v", out, f.snapshot())
			}
		})
	}
}

func TestBrowserToolAtMostOnce(t *testing.T) {
	f := newBrowserTestBackend(t)
	release := make(chan struct{})
	f.act = func(ctx context.Context, call browserTestCall) (browser.ActionResult, error) {
		select {
		case <-release:
			return browser.ActionResult{Page: call.Request.Page, Dispatch: browser.DispatchSent, Completion: browser.CompletionObserved, Evidence: "Observed new field state."}, nil
		case <-ctx.Done():
			return browser.ActionResult{Dispatch: browser.DispatchSent}, ctx.Err()
		}
	}
	d, replies, events := newBrowserTestDispatcher(t, f)
	m := browserTestMessage(`"request-1"`, "a", "turn-a", "call-1", codex.BrowserFill, `{"pageId":"page-a","navigation":1,"observationId":"o","elementId":"e","text":"once"}`)
	d.Handle(m)
	select {
	case <-f.entered:
	case <-time.After(3 * time.Second):
		t.Fatal("no invocation")
	}
	var workers sync.WaitGroup
	for i := 0; i < 32; i++ {
		workers.Go(func() { d.Handle(m) })
	}
	workers.Wait()
	alias := m
	alias.ID = json.RawMessage(`"request-\u0031"`)
	d.Handle(alias)
	replay := m
	replay.ID = json.RawMessage(`"different-request"`)
	d.Handle(replay)
	if reply := readBrowserTestReply(t, replies); reply.response.Success || browserTestOutput(t, reply.response).Error.Code != "stale" {
		t.Fatal("semantic call replay accepted")
	}
	close(release)
	if !readBrowserTestReply(t, replies).response.Success {
		t.Fatal("original action failed")
	}
	readBrowserTestFinal(t, events)
	d.Handle(m)
	d.Handle(replay)
	if len(f.snapshot()) != 1 || len(replies) != 0 {
		t.Fatalf("duplicate side effect or reply: calls=%d replies=%d", len(f.snapshot()), len(replies))
	}
	bindBrowserTestTurn(t, d, context.Background(), "a", "next-turn")
	late := m
	late.ID = json.RawMessage(`"late"`)
	d.Handle(late)
	if out := browserTestOutput(t, readBrowserTestReply(t, replies).response); out.Error.Code != "stale" || len(f.snapshot()) != 1 {
		t.Fatal("old turn ran after replacement", out)
	}
}

func TestBrowserToolCancellation(t *testing.T) {
	for _, mode := range []string{"stop", "page-close", "close", "disconnect", "parent", "timeout", "replacement"} {
		t.Run(mode, func(t *testing.T) {
			f := newBrowserTestBackend(t)
			cancelled, drained, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
			var releaseOnce sync.Once
			releaseBackend := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(releaseBackend)
			f.act = func(ctx context.Context, call browserTestCall) (browser.ActionResult, error) {
				if call.Request.Page.ThreadID != "local-a" {
					return browser.ActionResult{}, nil
				}
				<-ctx.Done()
				close(cancelled)
				<-release // Simulate an engine still draining an already-sent action.
				close(drained)
				return browser.ActionResult{Page: call.Request.Page, Dispatch: browser.DispatchSent, Evidence: "password=late-secret"}, ctx.Err()
			}
			d, replies, events := newBrowserTestDispatcher(t, f)
			turn := "turn-a"
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var parentDeadline time.Time
			if mode == "timeout" {
				var stop context.CancelFunc
				ctx, stop = context.WithTimeout(ctx, 250*time.Millisecond)
				defer stop()
				parentDeadline, _ = ctx.Deadline()
			}
			if mode == "parent" || mode == "timeout" {
				turn = "bound-context"
				bindBrowserTestTurn(t, d, ctx, "a", turn)
			}
			m := browserTestMessage(`"cancel-me"`, "a", turn, "cancel-me", codex.BrowserFill, `{"pageId":"page-a","navigation":1,"observationId":"o","elementId":"e","text":"private-input"}`)
			d.Handle(m)
			select {
			case call := <-f.entered:
				if mode == "timeout" && !call.Deadline.Equal(parentDeadline) {
					t.Fatal("earlier parent deadline was extended", call.Deadline, parentDeadline)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("tool not invoked")
			}
			switch mode {
			case "stop":
				d.StopTurn("server-a", turn)
			case "page-close":
				d.ClosePage(browser.PageKey{ThreadID: "local-a", PageID: "page-a"})
			case "close":
				d.Close()
			case "disconnect":
				d.Disconnect()
			case "parent":
				cancel()
			case "replacement":
				bindBrowserTestTurn(t, d, context.Background(), "a", "replacement")
			}
			select {
			case <-cancelled:
			case <-time.After(3 * time.Second):
				t.Fatal("backend did not receive cancellation")
			}
			final := readBrowserTestFinal(t, events)
			wantCode, wantStatus := "cancelled", "interrupted"
			if mode == "timeout" {
				wantCode, wantStatus = "timeout", "failed"
			}
			if final.Event.Status != wantStatus || !strings.Contains(string(final.Event.Tool.Result), `"code":"`+wantCode+`"`) {
				t.Fatalf("wrong cancellation activity: %+v", final)
			}
			if mode == "disconnect" {
				if len(replies) != 0 {
					t.Fatal("wrote to disconnected transport")
				}
			} else {
				reply := readBrowserTestReply(t, replies)
				output := browserTestOutput(t, reply.response)
				if reply.response.Success || output.Error.Code != wantCode || output.Dispatch != browser.DispatchUnknown || len(reply.response.ContentItems) != 1 {
					t.Fatalf("cancelled action falsely claimed safe retry or success: %+v", output)
				}
			}
			d.mu.Lock()
			pending := len(d.active)
			d.mu.Unlock()
			if pending != 0 {
				t.Fatal("request left pending while the engine drained")
			}
			releaseBackend()
			select {
			case <-drained:
			case <-time.After(3 * time.Second):
				t.Fatal("backend did not drain")
			}
			d.Handle(m)
			if len(replies) != 0 || len(f.snapshot()) != 1 {
				t.Fatal("late completion or duplicate request was replayed")
			}
			if mode == "stop" || mode == "page-close" {
				d.Handle(browserTestMessage(`"other-thread"`, "b", "turn-b", "other-thread", codex.BrowserObserve, `{"pageId":"page-b","navigation":1}`))
				if !readBrowserTestReply(t, replies).response.Success {
					t.Fatal("cancellation leaked into another thread")
				}
			}
		})
	}
}

func TestBrowserActionResults(t *testing.T) {
	page := browser.PageRef{PageKey: browser.PageKey{ThreadID: "local-a", PageID: "page-a"}, Navigation: 1}
	for _, tc := range []struct {
		name     string
		action   browser.ActionResult
		err      error
		code     string
		dispatch browser.Dispatch
	}{
		{"zero", browser.ActionResult{}, nil, "unconfirmed", browser.DispatchUnknown},
		{"sent unconfirmed", browser.ActionResult{Page: page, Dispatch: browser.DispatchSent, Evidence: "private evidence"}, browser.ErrUnconfirmed, "unconfirmed", browser.DispatchSent},
		{"sensitive", browser.ActionResult{Dispatch: browser.DispatchNone}, fmt.Errorf("password=private: %w", browser.ErrSensitive), "sensitive", browser.DispatchNone},
		{"unknown error", browser.ActionResult{Dispatch: browser.DispatchUnknown}, errors.New("Cookie: private"), "engine", browser.DispatchUnknown},
		{"timeout after send", browser.ActionResult{Dispatch: browser.DispatchSent}, context.DeadlineExceeded, "timeout", browser.DispatchSent},
		{"cancel after send", browser.ActionResult{Dispatch: browser.DispatchSent}, context.Canceled, "cancelled", browser.DispatchSent},
		{"empty evidence", browser.ActionResult{Page: page, Dispatch: browser.DispatchSent, Completion: browser.CompletionObserved}, nil, "unconfirmed", browser.DispatchSent},
		{"foreign result page", browser.ActionResult{Page: browser.PageRef{Navigation: 1}, Dispatch: browser.DispatchSent, Completion: browser.CompletionObserved, Evidence: "private evidence"}, nil, "unconfirmed", browser.DispatchSent},
		{"confirmed no-op", browser.ActionResult{Page: page, Dispatch: browser.DispatchNone, Completion: browser.CompletionObserved, Evidence: "Observed field already at private-value."}, nil, "", browser.DispatchNone},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBrowserTestBackend(t)
			f.act = func(context.Context, browserTestCall) (browser.ActionResult, error) { return tc.action, tc.err }
			d, replies, events := newBrowserTestDispatcher(t, f)
			d.Handle(browserTestMessage(`"result"`, "a", "turn-a", "result", codex.BrowserFill, `{"pageId":"page-a","navigation":1,"observationId":"o","elementId":"e","text":"private-value"}`))
			reply := readBrowserTestReply(t, replies)
			output := browserTestOutput(t, reply.response)
			if reply.response.Success != (tc.code == "") || output.Dispatch != tc.dispatch || tc.code != "" && (output.Error == nil || output.Error.Code != tc.code || output.Evidence != "") || strings.Contains(reply.response.ContentItems[0].Text, "private") {
				t.Fatalf("unsafe action result: %+v", output)
			}
			final := readBrowserTestFinal(t, events)
			if strings.Contains(string(final.Event.Tool.Result), "private") || len(f.snapshot()) != 1 {
				t.Fatal("action data leaked or was retried")
			}
		})
	}
}

func TestBrowserReadFailuresDiscardPartialData(t *testing.T) {
	for _, tc := range []struct {
		name, tool, code string
		configure        func(*browserTestBackend)
	}{
		{"observation failure", codex.BrowserObserve, "sensitive", func(f *browserTestBackend) {
			f.observe = func(_ context.Context, req browser.Request) (browser.Observation, error) {
				return browser.Observation{Page: req.Page, Text: "private partial page"}, browser.ErrSensitive
			}
		}},
		{"foreign observation", codex.BrowserObserve, "stale", func(f *browserTestBackend) {
			f.observe = func(context.Context, browser.Request) (browser.Observation, error) {
				return browser.Observation{Page: browser.PageRef{}, ObservationID: "o", Text: "private partial page"}, nil
			}
		}},
		{"foreign element", codex.BrowserObserve, "stale", func(f *browserTestBackend) {
			f.observe = func(_ context.Context, req browser.Request) (browser.Observation, error) {
				return browser.Observation{Page: req.Page, ObservationID: "o", Text: "private partial page", Elements: []browser.Element{{Ref: browser.ElementRef{ID: "e", ObservationID: "o"}}}}, nil
			}
		}},
		{"invalid page text", codex.BrowserObserve, "sensitive", func(f *browserTestBackend) {
			f.observe = func(_ context.Context, req browser.Request) (browser.Observation, error) {
				return browser.Observation{Page: req.Page, ObservationID: "o", Text: string([]byte{0xff})}, nil
			}
		}},
		{"capture failure", codex.BrowserCapture, "sensitive", func(f *browserTestBackend) {
			f.capture = func(_ context.Context, req browser.Request) (browser.Screenshot, error) {
				return browser.Screenshot{Page: req.Page, PNG: f.png, Width: 2, Height: 1}, browser.ErrSensitive
			}
		}},
		{"invalid PNG", codex.BrowserCapture, "engine", func(f *browserTestBackend) { f.png = []byte("private raw image") }},
		{"empty PNG", codex.BrowserCapture, "unsupported", func(f *browserTestBackend) { f.png = nil }},
		{"PNG dimensions", codex.BrowserCapture, "engine", func(f *browserTestBackend) {
			f.capture = func(_ context.Context, req browser.Request) (browser.Screenshot, error) {
				return browser.Screenshot{Page: req.Page, PNG: f.png, Width: 3, Height: 1}, nil
			}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := newBrowserTestBackend(t)
			tc.configure(f)
			d, replies, _ := newBrowserTestDispatcher(t, f)
			d.Handle(browserTestMessage(`"read"`, "a", "turn-a", "read", tc.tool, `{"pageId":"page-a","navigation":1}`))
			reply := readBrowserTestReply(t, replies)
			output := browserTestOutput(t, reply.response)
			if reply.response.Success || output.Error == nil || output.Error.Code != tc.code || output.Text != "" || len(reply.response.ContentItems) != 1 || strings.Contains(reply.response.ContentItems[0].Text, "private") {
				t.Fatalf("partial data returned on failure: %+v", reply)
			}
		})
	}
}

func TestBrowserBindingAndStaleLifecycle(t *testing.T) {
	f := newBrowserTestBackend(t)
	d, replies, _ := newBrowserTestDispatcher(t, f)
	if err := d.BindTurn(context.Background(), "other-server", "turn-c", browser.PageKey{ThreadID: "local-a", PageID: "page-a"}); !errors.Is(err, browser.ErrPermissionDenied) {
		t.Fatal("ambiguous server to local thread binding", err)
	}
	if err := d.BindTurn(context.Background(), "server-a", "turn-a", browser.PageKey{ThreadID: "local-a", PageID: "page-a"}); !errors.Is(err, browser.ErrStale) {
		t.Fatal("existing turn can be rebound and reset", err)
	}
	d.StopTurn("server-a", "old-turn")
	d.ClosePage(browser.PageKey{ThreadID: "local-a", PageID: "old-page"})
	d.Handle(browserTestMessage(`"current"`, "a", "turn-a", "current", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`))
	if !readBrowserTestReply(t, replies).response.Success {
		t.Fatal("stale lifecycle event cancelled the current page/turn")
	}
	d.Close()
	d.Handle(browserTestMessage(`"closed"`, "a", "turn-a", "closed", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`))
	if out := browserTestOutput(t, readBrowserTestReply(t, replies).response); out.Error.Code != "closed" || len(f.snapshot()) != 1 {
		t.Fatal("closed dispatcher accepted more work", out)
	}
	if err := d.BindTurn(context.Background(), "server-a", "new", browser.PageKey{ThreadID: "local-a", PageID: "page-a"}); !errors.Is(err, browser.ErrClosed) {
		t.Fatal("closed dispatcher rebound", err)
	}
}

func TestBrowserNativeSnapshotsCannotPersistOrOverwrite(t *testing.T) {
	for _, method := range []string{"item/started", "item/completed"} {
		params := `{"threadId":"server-a","turnId":"turn-a","item":{"id":"fill","type":"dynamicToolCall","tool":"browser_fill","status":"completed","success":true,"arguments":{"text":"private-input"},"contentItems":[{"type":"inputImage","imageUrl":"private-image"}]}}`
		if _, known, err := harness.DecodeCodexEvent(codex.Message{Method: method, Params: json.RawMessage(params)}); known || err != nil {
			t.Fatal("native browser snapshot bypassed safe dispatcher activity", known, err)
		}
	}
}

func TestBrowserMissingCapabilityAndReplyFailure(t *testing.T) {
	for _, mode := range []string{"missing-capability", "reply-failure"} {
		t.Run(mode, func(t *testing.T) {
			f := newBrowserTestBackend(t)
			events := make(chan BrowserToolActivity, 16)
			replies := make(chan browserTestReply, 16)
			var attempts atomic.Int64
			var tools browser.Tools = f
			if mode == "missing-capability" {
				tools = nil
			}
			d := NewBrowserToolDispatcher(tools, f, func(id json.RawMessage, value any) error {
				attempts.Add(1)
				replies <- browserTestReply{id, value.(codex.DynamicToolResponse)}
				if mode == "reply-failure" {
					return errors.New("Cookie: private-transport-error")
				}
				return nil
			}, func(event BrowserToolActivity) { events <- event })
			defer d.Disconnect()
			bindBrowserTestTurn(t, d, context.Background(), "a", "turn-a")
			m := browserTestMessage(`"once"`, "a", "turn-a", "once", codex.BrowserObserve, `{"pageId":"page-a","navigation":1}`)
			if d.Handle(codex.Message{Method: "other/method"}) {
				t.Fatal("dispatcher claimed an unrelated method")
			}
			d.Handle(m)
			reply := readBrowserTestReply(t, replies)
			final := readBrowserTestFinal(t, events)
			d.Handle(m)
			if attempts.Load() != 1 || strings.Contains(string(final.Event.Tool.Result), "private") || final.Event.Status != "failed" {
				t.Fatal("reply failure leaked or was retried", final, attempts.Load())
			}
			if mode == "missing-capability" && (reply.response.Success || browserTestOutput(t, reply.response).Error.Code != "unsupported" || len(f.snapshot()) != 0) {
				t.Fatal("missing browser engine reported success")
			}
		})
	}
}

func TestBrowserCancellationBeforeDispatch(t *testing.T) {
	f := newBrowserTestBackend(t)
	statusEntered, release, statusDone := make(chan struct{}), make(chan struct{}), make(chan struct{})
	f.status = func(ctx context.Context, thread string) (browser.Status, error) {
		close(statusEntered)
		<-release
		defer close(statusDone)
		return f.statuses[thread], nil
	}
	d, replies, events := newBrowserTestDispatcher(t, f)
	d.Handle(browserTestMessage(`"before-dispatch"`, "a", "turn-a", "before-dispatch", codex.BrowserOpen, `{"pageId":"page-a","navigation":1,"url":"https://example.com"}`))
	select {
	case <-statusEntered:
	case <-time.After(3 * time.Second):
		t.Fatal("no status check")
	}
	d.StopTurn("server-a", "turn-a")
	reply := readBrowserTestReply(t, replies)
	if output := browserTestOutput(t, reply.response); reply.response.Success || output.Error.Code != "cancelled" || output.Dispatch != browser.DispatchNone {
		t.Fatal("undispatched cancellation was misclassified", output)
	}
	readBrowserTestFinal(t, events)
	close(release)
	<-statusDone
	if len(f.snapshot()) != 0 {
		t.Fatal("late status check invoked a cancelled operation")
	}
}
