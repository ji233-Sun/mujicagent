//go:build darwin

package browser

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
)

func TestMain(m *testing.M) {
	if os.Getenv("MUJICAGENT_BROWSER_BRIDGE_TEST") != "1" {
		os.Exit(m.Run())
	}
	dir, err := os.MkdirTemp("", "mujicagent-capture-native-*")
	if err != nil {
		panic(err)
	}
	resourceRoot := os.Getenv("MUJICAGENT_BROWSER_RESOURCES")
	if resourceRoot == "" {
		resourceRoot = "../../resources"
	}
	resources, err := filepath.Abs(resourceRoot)
	if err != nil {
		panic(err)
	}
	mygo.App.SetPath(mygo.PathUserData, dir)
	mygo.App.SetPath(mygo.PathResources, resources)
	mygo.App.OnWindowAllClosed(func() {})
	var code atomic.Int32
	code.Store(1)
	mygo.App.WhenReady(func() { go func() { code.Store(int32(m.Run())); mygo.App.Quit() }() })
	err = mygo.App.Run()
	_ = os.RemoveAll(dir)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	os.Exit(int(code.Load()))
}

func TestNativeCaptureDrain(t *testing.T) {
	if os.Getenv("MUJICAGENT_BROWSER_BRIDGE_TEST") != "1" {
		t.Skip("native bridge acceptance not requested")
	}
	for _, mode := range []string{"cancel", "close", "navigate"} {
		t.Run(mode, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			var view ui.NativeWebView
			win := mygo.NewWindow(mygo.WindowOptions{Title: "Synthetic native capture drain", Width: 600, Height: 400,
				Content: ui.View(func(c *ui.Context) {
					if view != nil {
						ui.WebView(c, view).Fill()
					}
				})})
			r := New(win, nil)
			defer win.Destroy()
			defer r.Shutdown()
			status, err := r.Create(ctx, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			if !status.CaptureSupported {
				t.Fatal("native helper unavailable")
			}
			placed := make(chan struct{})
			win.Update(func() { view = r.View(status.Page.PageKey); close(placed) })
			select {
			case <-placed:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			held, release := make(chan struct{}), make(chan struct{})
			start, ended := make(chan struct{}), make(chan struct{})
			var startOnce sync.Once
			startRequest := func() { startOnce.Do(func() { close(start) }) }
			var once sync.Once
			releaseResponse := func() { once.Do(func() { close(release) }) }
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
				if request.URL.Path == "/start" {
					select {
					case <-start:
						fmt.Fprint(w, "synthetic")
					case <-request.Context().Done():
					}
					return
				}
				if request.URL.Path == "/hold" {
					defer close(ended)
					close(held)
					select {
					case <-release:
						fmt.Fprint(w, "synthetic")
					case <-request.Context().Done():
					}
					return
				}
				w.Header().Set("Content-Type", "text/html")
				fmt.Fprint(w, `<!doctype html><html><body><p>Local capture drain fixture</p></body></html>`)
			}))
			defer server.Close()
			defer releaseResponse()
			defer startRequest()
			_, err = r.Open(ctx, OpenRequest{Request: Request{Page: status.Page, Actor: ActorHuman}, URL: server.URL})
			if err != nil {
				t.Fatal(err)
			}
			status, err = r.Status(ctx, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			native := r.View(status.Page.PageKey).(*mygo.WebView)
			// Start the blocking request only after Eval's response has reached
			// Go; otherwise WebKit may defer that response behind the timer.
			_, err = native.Page().EvalContext(ctx, `fetch("/start").then(()=>{const request=new XMLHttpRequest();request.open("GET","/hold",false);request.send();});true`)
			if err != nil {
				t.Fatal(err)
			}
			startRequest()
			select {
			case <-held:
			case <-ctx.Done():
				t.Fatal("synchronous fixture request did not start")
			}
			_, err = r.Resume(ctx, status.Page.PageKey, Permissions{Capture: true})
			if err != nil {
				t.Fatal(err)
			}
			// Observe the real private IPC dispatch without replacing it or
			// delaying a native callback. The blocked WebProcess owns the work.
			dispatched := make(chan struct{})
			a := loadCaptureAPI()
			post := a.post
			var sent sync.Once
			a.post = func(page, name, body uintptr) { post(page, name, body); sent.Do(func() { close(dispatched) }) }
			defer func() { a.post = post }()
			callCtx, stop := context.WithCancel(ctx)
			defer stop()
			type output struct {
				image Screenshot
				err   error
			}
			finished := make(chan output, 1)
			go func() {
				image, err := r.Capture(callCtx, Request{Page: status.Page, Actor: ActorAgent})
				finished <- output{image, err}
			}()
			select {
			case <-dispatched:
			case <-ctx.Done():
				t.Fatal("capture was not dispatched")
			}
			select {
			case <-ended:
				t.Fatal("fixture request ended before the native capture was interrupted")
			default:
			}
			if mode == "close" {
				closed, err := r.Close(ctx, status.Page.PageKey)
				if err != nil || closed.State != StateClosed || closed.Busy {
					t.Fatalf("close did not drain through destruction: %+v %v", closed, err)
				}
			} else if mode == "navigate" {
				if err := native.Page().LoadURL(server.URL + "/next"); err != nil {
					t.Fatal(err)
				}
			} else {
				stop()
				select {
				case result := <-finished:
					if !errors.Is(result.err, context.Canceled) || len(result.image.PNG) != 0 {
						t.Fatalf("cancelled request returned pixels: %v", result.err)
					}
				case <-ctx.Done():
					t.Fatal("caller did not receive cancellation")
				}
				current, err := r.Status(ctx, "fixture")
				if err != nil || !current.Busy || current.State != StateHuman || current.Permissions != (Permissions{}) {
					t.Fatalf("native work was unlocked before drain: %+v %v", current, err)
				}
				if _, err := r.Resume(ctx, status.Page.PageKey, Permissions{Capture: true}); !errors.Is(err, ErrBusy) {
					t.Fatal("resume overtook native capture")
				}
			}
			releaseResponse()
			if mode != "cancel" {
				select {
				case result := <-finished:
					if result.err == nil || len(result.image.PNG) != 0 {
						t.Fatal("closed or navigated request returned pixels")
					}
					if mode == "navigate" && !errors.Is(result.err, ErrStale) {
						t.Fatalf("navigation did not invalidate capture: %v", result.err)
					}
				case <-ctx.Done():
					t.Fatal("closed capture did not finish")
				}
			}
			ticker := time.NewTicker(time.Millisecond)
			defer ticker.Stop()
			for {
				current, err := r.Status(ctx, "fixture")
				if err == nil && !current.Busy {
					break
				}
				select {
				case <-ticker.C:
				case <-ctx.Done():
					t.Fatal("native transaction did not drain")
				}
			}
		})
	}
}
