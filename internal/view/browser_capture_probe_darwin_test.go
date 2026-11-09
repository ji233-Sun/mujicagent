//go:build darwin

package view

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/ebitengine/purego/objc"
	"github.com/egoist/mygo"
	"github.com/ji233-Sun/mujicagent/internal/browser"
)

// This opt-in native counterexample guards against a script-disable freeze:
// disabling JavaScript does not freeze already pending promise continuations.
// Its only input is a synthetic loopback page. It never captures a bitmap.
func TestBrowserNativeCaptureFreeze(t *testing.T) {
	s := newNativeBrowserScene(t)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	started, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	releaseResponse := func() { releaseOnce.Do(func() { close(release) }) }
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/delayed" {
			close(started)
			select {
			case <-release:
				fmt.Fprint(w, "fixture-response")
			case <-r.Context().Done():
			}
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<html><body><div id="result">pending</div><script>
fetch("/delayed").then(() => document.getElementById("result").textContent = "promise-ran");
</script></body></html>`)
	}))
	defer server.Close()
	defer releaseResponse()
	status := s.status(t, s.thread)
	_, err := s.runtime.Open(ctx, browser.OpenRequest{Request: browser.Request{Page: status.Page, Actor: browser.ActorHuman}, URL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal("synthetic fetch did not start")
	}
	var native objc.ID
	s.update(t, func() {
		views := captureProbeViews(captureProbeSend(objc.ID(s.win.NativeHandle()), "contentView"))
		if len(views) != 1 {
			t.Errorf("expected one native page, got %d", len(views))
			return
		}
		native = views[0]
		preferences := captureProbeSend(captureProbeSend(native, "configuration"), "preferences")
		captureProbeSend(preferences, "setJavaScriptEnabled:", false)
	})
	if t.Failed() {
		return
	}
	defer s.update(t, func() {
		preferences := captureProbeSend(captureProbeSend(native, "configuration"), "preferences")
		captureProbeSend(preferences, "setJavaScriptEnabled:", true)
	})
	code := `return document.getElementById("result").textContent;`
	if value, err := captureProbeEval(ctx, s.win, native, code); err != nil || value != "pending" {
		t.Fatalf("isolated read before releasing fetch: value=%q err=%v", value, err)
	}
	page := s.runtime.View(status.Page.PageKey).(*mygo.WebView)
	if _, err := page.Page().EvalContext(ctx, `document.getElementById("result").textContent`); err == nil {
		t.Fatal("page-world script still enabled; freeze probe did not exercise disabled JavaScript")
	}
	// Only release the server response after script execution is disabled.
	// The explicit barrier avoids sleeps and dependence on load timing.
	releaseResponse()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		value, err := captureProbeEval(ctx, s.win, native, code)
		if err != nil {
			t.Fatal("isolated read failed")
		}
		if value == "promise-ran" {
			break
		}
		select {
		case <-ticker.C:
		case <-ctx.Done():
			t.Fatal("pending promise did not complete; reassess the native freeze assumption")
		}
	}
	t.Log("page-world eval rejected, isolated eval succeeded, pending fetch promise still changed the DOM; capture requires an independent native transaction")
}

func captureProbeSend(object objc.ID, method string, args ...any) objc.ID {
	return object.Send(objc.RegisterName(method), args...)
}

func captureProbeViews(root objc.ID) []objc.ID {
	if captureProbeSend(root, "isKindOfClass:", objc.GetClass("WKWebView")) != 0 {
		return []objc.ID{root}
	}
	children := captureProbeSend(root, "subviews")
	var found []objc.ID
	for i, n := uintptr(0), uintptr(captureProbeSend(children, "count")); i < n; i++ {
		found = append(found, captureProbeViews(captureProbeSend(children, "objectAtIndex:", i))...)
	}
	return found
}

func captureProbeEval(ctx context.Context, window *mygo.Window, native objc.ID, code string) (string, error) {
	type result struct {
		value string
		err   error
	}
	done := make(chan result, 1)
	window.Update(func() {
		if ctx.Err() != nil {
			return
		}
		nsString := func(value string) objc.ID {
			return captureProbeSend(objc.ID(objc.GetClass("NSString")), "stringWithUTF8String:", value)
		}
		world := captureProbeSend(objc.ID(objc.GetClass("WKContentWorld")), "worldWithName:", nsString("mujicagent.capture.probe"))
		block := objc.NewBlock(func(_ objc.Block, value, err objc.ID) {
			if err != 0 || captureProbeSend(value, "isKindOfClass:", objc.GetClass("NSString")) == 0 {
				done <- result{err: browser.ErrEngine}
				return
			}
			done <- result{value: objc.Send[string](value, objc.RegisterName("UTF8String"))}
		})
		defer block.Release()
		captureProbeSend(native, "callAsyncJavaScript:arguments:inFrame:inContentWorld:completionHandler:",
			nsString(code), 0, 0, world, block)
	})
	select {
	case value := <-done:
		return value.value, value.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}
