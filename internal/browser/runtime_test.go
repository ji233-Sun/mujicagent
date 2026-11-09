package browser

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type testPage struct {
	started chan struct{}
	release chan struct{}
	calls   atomic.Int32
	closed  atomic.Bool
	reject  bool
}

func (p *testPage) open(context.Context, string) error { return nil }
func (p *testPage) destroy()                           { p.closed.Store(true) }
func (p *testPage) refresh()                           {}
func (p *testPage) capture(context.Context) (Screenshot, error) {
	return Screenshot{}, ErrSensitive
}
func (p *testPage) eval(_ context.Context, action string, _ any) (pageData, error) {
	p.calls.Add(1)
	if p.started != nil {
		select {
		case p.started <- struct{}{}:
		default:
		}
		<-p.release
	}
	if action == "observe" {
		return pageData{Code: "ok", Revision: "1", Text: "fixture", Elements: []pageElement{{ID: "1", Role: "textbox", Name: "Input", Fill: true}}}, nil
	}
	if p.reject {
		return pageData{Code: "stale"}, ErrStale
	}
	return pageData{Code: "ok", Changed: true}, nil
}

func testRuntime(t *testing.T, engine *testPage) (*Runtime, Status) {
	t.Helper()
	r := newRuntime(nil)
	r.factory = func(context.Context, *runtimePage) (pageEngine, error) { return engine, nil }
	s, err := r.Create(context.Background(), "a")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(r.Shutdown)
	return r, s
}

func TestRuntimeAuthorizationAndReferences(t *testing.T) {
	engine := &testPage{}
	r, s := testRuntime(t, engine)
	ctx := context.Background()
	req := Request{Page: s.Page, Actor: ActorAgent}
	if _, err := r.Observe(ctx, req); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("initial observe: %v", err)
	}
	if _, err := r.Resume(ctx, s.Page.PageKey, Permissions{}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Scroll(ctx, ScrollRequest{Request: req, DeltaY: 10}); !errors.Is(err, ErrPermissionDenied) {
		t.Fatalf("ungranted scroll: %v", err)
	}
	_, _ = r.TakeOver(ctx, s.Page.PageKey)
	_, _ = r.Resume(ctx, s.Page.PageKey, Permissions{Interact: true})
	first, err := r.Observe(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	second, err := r.Observe(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Fill(ctx, FillRequest{Request: req, Element: first.Elements[0].Ref, Text: "x"}); !errors.Is(err, ErrStale) {
		t.Fatalf("old observation accepted: %v", err)
	}
	// Even a rejected write consumes the observation, so reacquire.
	second, err = r.Observe(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Fill(ctx, FillRequest{Request: req, Element: second.Elements[0].Ref, Text: "x"})
	if err != nil || result.Dispatch != DispatchSent || result.Completion != CompletionObserved {
		t.Fatalf("fill = %+v, %v", result, err)
	}
	before := engine.calls.Load()
	if _, err := r.Fill(ctx, FillRequest{Request: req, Element: second.Elements[0].Ref, Text: "x"}); !errors.Is(err, ErrStale) || engine.calls.Load() != before {
		t.Fatalf("reference reused: %v", err)
	}
}

func TestRuntimeCancellationDrainsBeforeResume(t *testing.T) {
	engine := &testPage{started: make(chan struct{}, 1), release: make(chan struct{})}
	r, s := testRuntime(t, engine)
	ctx := context.Background()
	_, _ = r.Resume(ctx, s.Page.PageKey, Permissions{Interact: true})
	requestCtx, cancel := context.WithCancel(ctx)
	finished := make(chan error, 1)
	go func() {
		_, err := r.Observe(requestCtx, Request{Page: s.Page, Actor: ActorAgent})
		finished <- err
	}()
	<-engine.started
	if _, err := r.Observe(ctx, Request{Page: s.Page, Actor: ActorAgent}); !errors.Is(err, ErrBusy) {
		t.Fatalf("parallel action = %v", err)
	}
	cancel()
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel = %v", err)
	}
	status, _ := r.Status(ctx, "a")
	if status.State != StateHuman || !status.Busy || status.Permissions != (Permissions{}) {
		t.Fatalf("undrained = %+v", status)
	}
	if _, err := r.Resume(ctx, s.Page.PageKey, Permissions{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("resume while draining = %v", err)
	}
	close(engine.release)
	status, err := r.TakeOver(ctx, s.Page.PageKey)
	if err != nil || status.Busy || status.State != StateHuman {
		t.Fatalf("drained = %+v, %v", status, err)
	}
}

func TestRuntimeReplacementAndIsolation(t *testing.T) {
	ctx := context.Background()
	r := newRuntime(nil)
	r.factory = func(context.Context, *runtimePage) (pageEngine, error) { return &testPage{}, nil }
	defer r.Shutdown()
	a, _ := r.Create(ctx, "a")
	b, _ := r.Create(ctx, "b")
	if a.Page.PageID == b.Page.PageID {
		t.Fatal("shared page identity")
	}
	_, _ = r.Resume(ctx, a.Page.PageKey, Permissions{Capture: true})
	bb, _ := r.Status(ctx, "b")
	if bb.State != StateHuman || bb.Permissions != (Permissions{}) {
		t.Fatalf("cross-session grant = %+v", bb)
	}
	_, _ = r.Close(ctx, a.Page.PageKey)
	next, err := r.Create(ctx, "a")
	if err != nil || next.Page.PageID == a.Page.PageID || next.State != StateHuman {
		t.Fatalf("replacement = %+v, %v", next, err)
	}
	if _, err := r.Close(ctx, a.Page.PageKey); !errors.Is(err, ErrNotFound) {
		t.Fatalf("old key close = %v", err)
	}
	current, _ := r.Status(ctx, "a")
	if current.Page != next.Page || current.State != StateHuman {
		t.Fatal("old close changed replacement")
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := r.Create(cancelled, "c"); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled creation = %v", err)
	}
	if _, err := r.Status(ctx, "c"); !errors.Is(err, ErrNotFound) {
		t.Fatal("cancelled creation left page")
	}
}

func TestRuntimeClosePrioritizesRevocation(t *testing.T) {
	engine := &testPage{started: make(chan struct{}, 1), release: make(chan struct{})}
	r, s := testRuntime(t, engine)
	ctx := context.Background()
	_, _ = r.Resume(ctx, s.Page.PageKey, Permissions{Capture: true})
	finished := make(chan struct{})
	go func() {
		_, _ = r.Observe(ctx, Request{Page: s.Page, Actor: ActorAgent})
		close(finished)
	}()
	<-engine.started
	deadline, cancel := context.WithTimeout(ctx, time.Millisecond)
	defer cancel()
	status, err := r.Close(deadline, s.Page.PageKey)
	if !errors.Is(err, context.DeadlineExceeded) || status.State != StateClosed || !status.Busy || !engine.closed.Load() {
		t.Fatalf("close before drain = %+v, %v", status, err)
	}
	if _, err := r.Create(ctx, "a"); !errors.Is(err, ErrBusy) {
		t.Fatalf("replacement before drain = %v", err)
	}
	close(engine.release)
	<-finished
	status, err = r.Close(ctx, s.Page.PageKey)
	if err != nil || status.Busy || status.State != StateClosed {
		t.Fatalf("close after drain = %+v, %v", status, err)
	}
}

func TestSanitization(t *testing.T) {
	for _, raw := range []string{"file:///tmp/x", "javascript:alert(1)", "https://u:p@example.com", "https://example.com\\x"} {
		if _, err := parseURL(raw); !errors.Is(err, ErrInvalidArgument) {
			t.Fatalf("accepted %q", raw)
		}
	}
	if got := displayURL("https://user:pass@example.com/token/path?q=secret#x"); got != "https://example.com" {
		t.Fatalf("display URL = %q", got)
	}
	if got := clean("token=supersecret\x1b password:private", 100); strings.Contains(got, "supersecret") || strings.Contains(got, "private") {
		t.Fatalf("redaction = %q", got)
	}
	for _, text := range []string{"Authorization: Basic Zml4dHVyZTpwYXNzd29yZA==", "Authorization: Bearer fixture-credential"} {
		if got := clean(text, 100); strings.Contains(got, "Zml4dHVyZTpwYXNzd29yZA") || strings.Contains(got, "fixture-credential") {
			t.Fatalf("authorization credential leaked: %q", got)
		}
	}
}

func TestRuntimeRejectedWriteIsNotDispatched(t *testing.T) {
	engine := &testPage{reject: true}
	r, s := testRuntime(t, engine)
	ctx := context.Background()
	_, _ = r.Resume(ctx, s.Page.PageKey, Permissions{Interact: true})
	req := Request{Page: s.Page, Actor: ActorAgent}
	observation, err := r.Observe(ctx, req)
	if err != nil {
		t.Fatal(err)
	}
	result, err := r.Fill(ctx, FillRequest{Request: req, Element: observation.Elements[0].Ref, Text: "must-not-write"})
	if !errors.Is(err, ErrStale) || result.Dispatch != DispatchNone {
		t.Fatalf("rejected write = %+v, %v", result, err)
	}
}

func TestRuntimeCreateCancellationKeepsReservationUntilCleanup(t *testing.T) {
	r := newRuntime(nil)
	engine := &testPage{}
	started, release := make(chan struct{}), make(chan struct{})
	r.factory = func(context.Context, *runtimePage) (pageEngine, error) { close(started); <-release; return engine, nil }
	t.Cleanup(r.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	finished := make(chan error, 1)
	go func() { _, err := r.Create(ctx, "a"); finished <- err }()
	<-started
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		close(release)
		t.Fatal("cancelled creation kept waiting for native factory")
	}
	status, err := r.Status(context.Background(), "a")
	if err != nil || status.State != StateClosed || !status.Busy {
		close(release)
		t.Fatalf("cancelled creation: %+v / %v", status, err)
	}
	if _, err := r.Create(context.Background(), "a"); !errors.Is(err, ErrBusy) {
		close(release)
		t.Fatal("replacement raced pending native creation")
	}
	p, _ := r.lookup(status.Page.PageKey)
	close(release)
	select {
	case <-p.done:
	case <-time.After(time.Second):
		t.Fatal("native creation did not clean up")
	}
	if !engine.closed.Load() {
		t.Fatal("late native page not destroyed")
	}
}
