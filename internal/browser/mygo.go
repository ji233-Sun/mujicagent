package browser

import (
	"context"
	_ "embed"
	"encoding/json"
	"net/url"
	"regexp"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/redact"
)

//go:embed page.js
var pageScript string

type mygoPage struct {
	view *mygo.WebView
	p    *runtimePage
}

// New creates the runtime for a native UI window. changed can run on workers;
// callers must serialize its snapshots onto their UI event queue.
func New(window *mygo.Window, changed func(Status)) *Runtime {
	r := newRuntime(changed)
	r.factory = func(ctx context.Context, p *runtimePage) (pageEngine, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		v, err := window.NewWebView(mygo.WebViewOptions{URL: "about:blank", Page: mygo.PageOptions{
			Session: mygo.NewEphemeralSession(), Untrusted: true, PreloadScript: pageScript,
		}})
		if err != nil {
			return nil, ErrEngine
		}
		engine := &mygoPage{view: v, p: p}
		page := v.Page()
		page.OnWillNavigate(func(e *mygo.NavigateEvent) {
			target, err := parseURL(e.URL)
			p.mu.Lock()
			p.status.Page.Navigation++
			p.invalidate()
			allowed := err == nil && origin(target) == p.origin && p.status.State != StateClosed
			if allowed {
				p.status.Loading = true
			} else {
				p.status.LastError = ErrPermissionDenied
			}
			p.mu.Unlock()
			if !allowed {
				e.PreventDefault()
			}
			r.publish(p)
		})
		page.OnDidNavigate(func(raw string) {
			p.mu.Lock()
			p.commits++
			p.status.Page.Navigation++
			p.invalidate()
			p.acceptLocation(raw)
			p.mu.Unlock()
			r.publish(p)
		})
		page.OnDidFinishLoad(func() {
			p.mu.Lock()
			p.status.Loading = false
			p.mu.Unlock()
			r.publish(p)
		})
		page.OnDidFailLoad(func(_ *mygo.LoadError) {
			p.mu.Lock()
			p.status.Loading, p.status.LastError = false, ErrEngine
			p.mu.Unlock()
			r.publish(p)
		})
		page.OnPageTitleUpdated(func(e *mygo.TitleEvent) {
			// Titles can echo credentials. Only sanitized tool observations
			// supply a title; never publish raw engine title notifications.
			e.PreventDefault()
		})
		// NewWebView starts about:blank asynchronously. Do not publish a
		// supposedly interactive initial page while that load is still racing
		// the first address-bar request.
		readyCtx, cancel := context.WithTimeout(ctx, DefaultTimeout)
		defer cancel()
		ready, readyErr := page.EvalContext(readyCtx, "document.readyState")
		if readyErr != nil || ready != "complete" || page.IsLoading() {
			ticker := time.NewTicker(10 * time.Millisecond)
			defer ticker.Stop()
			for page.IsLoading() || ready != "complete" {
				select {
				case <-readyCtx.Done():
					v.Destroy()
					return nil, readyCtx.Err()
				case <-ticker.C:
					ready, readyErr = page.EvalContext(readyCtx, "document.readyState")
					if readyErr != nil {
						v.Destroy()
						return nil, ErrEngine
					}
				}
			}
		}
		p.mu.Lock()
		p.status.Loading, p.status.Page.Navigation = false, 1
		p.mu.Unlock()
		return engine, nil
	}
	return r
}

func (r *Runtime) View(key PageKey) ui.NativeWebView {
	p, err := r.lookup(key)
	if err != nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.status.State == StateClosed {
		return nil
	}
	if engine, ok := p.engine.(*mygoPage); ok {
		return engine.view
	}
	return nil
}

func (e *mygoPage) destroy() { e.view.Destroy() }

func (e *mygoPage) refresh() {
	raw := e.view.Page().URL()
	loading := e.view.Page().IsLoading()
	e.p.mu.Lock()
	if e.p.status.State != StateClosed {
		if raw != "" && raw != e.p.url {
			e.p.acceptLocation(raw)
			e.p.status.Page.Navigation++
			e.p.invalidate()
		}
		e.p.status.Loading = loading
	}
	e.p.mu.Unlock()
}

func (e *mygoPage) open(ctx context.Context, raw string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	e.p.mu.Lock()
	before := e.p.commits
	e.p.mu.Unlock()
	e.view.Page().LoadURL(raw)
	ticker := time.NewTicker(20 * time.Millisecond)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			e.view.Page().Stop()
			// A synchronous Eval is a main-thread/page barrier after Stop.
			// Keep Busy until the actual native callback returns.
			_, _ = e.eval(context.Background(), "observe", nil)
			return ctx.Err()
		case <-ticker.C:
			e.p.mu.Lock()
			loaded := e.p.commits > before && !e.p.status.Loading && e.p.url != "" && e.p.url != "about:blank"
			last := e.p.status.LastError
			e.p.mu.Unlock()
			if last != nil {
				return last
			}
			if loaded {
				_, err := e.eval(ctx, "observe", nil)
				return err
			}
		}
	}
}

func (e *mygoPage) eval(ctx context.Context, action string, args any) (pageData, error) {
	if err := ctx.Err(); err != nil {
		return pageData{}, err
	}
	wire, err := json.Marshal([]any{action, args})
	if err != nil {
		return pageData{}, ErrInvalidArgument
	}
	e.p.mu.Lock()
	before := e.p.commits
	e.p.mu.Unlock()
	// The fixed scripts are synchronous. Cancelling the caller must not make
	// Busy false while native JavaScript is still executing.
	engineCtx, cancel := context.WithTimeout(context.Background(), DefaultTimeout)
	defer cancel()
	value, err := e.view.Page().EvalContext(engineCtx, "window.__mujicBrowser(..."+string(wire)+")")
	if engineCtx.Err() != nil {
		// Destruction is the fallback barrier when MyGo cannot cancel JS.
		e.view.Destroy()
		e.p.mu.Lock()
		e.p.status.State, e.p.status.Loading = StateClosed, false
		e.p.mu.Unlock()
		return pageData{}, engineCtx.Err()
	}
	if err != nil {
		return pageData{}, ErrEngine
	}
	raw, ok := value.(string)
	if !ok || len(raw) > 65536 {
		return pageData{}, ErrEngine
	}
	var data pageData
	decoder := json.NewDecoder(strings.NewReader(raw))
	decoder.DisallowUnknownFields()
	if decoder.Decode(&data) != nil {
		return pageData{}, ErrEngine
	}
	if err := ctx.Err(); err != nil {
		return pageData{}, err
	}
	e.refresh()
	if action == "click" && data.Code == "ok" && !data.Changed {
		// A form/link navigation usually commits after the JS task returns.
		// Wait for native navigation evidence, rather than treating click()
		// returning as a successful submission.
		ticker := time.NewTicker(20 * time.Millisecond)
		defer ticker.Stop()
		deadline := time.NewTimer(time.Second)
		defer deadline.Stop()
		for {
			e.p.mu.Lock()
			changed, loading, last := e.p.commits != before, e.p.status.Loading, e.p.status.LastError
			e.p.mu.Unlock()
			if last != nil {
				return pageData{}, last
			}
			if changed && !loading {
				data.Changed = true
				break
			}
			select {
			case <-ctx.Done():
				return pageData{}, ctx.Err()
			case <-deadline.C:
				return data, nil
			case <-ticker.C:
				e.refresh()
			}
		}
	}
	return data, dataError(data)
}

func (e *mygoPage) capture(context.Context) (Screenshot, error) {
	// MyGo 0.7.4 serializes Eval through page-world JSON.stringify. A page can
	// forge any safety-check response, and capture cannot atomically redact
	// the native bitmap. Permission alone does not make that channel trusted.
	return Screenshot{}, ErrUnsupported
}

var tokenPattern = regexp.MustCompile(`\b(sk-[a-zA-Z0-9_-]{8,}|eyJ[a-zA-Z0-9_-]{12,}(?:\.[a-zA-Z0-9_-]+){1,2})\b`)

func clean(value string, limit int) string {
	value = redact.Text(value)
	value = tokenPattern.ReplaceAllString(value, "[redacted]")
	value = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) && r != '\n' && r != '\t' {
			return -1
		}
		return r
	}, value)
	if len(value) > limit {
		value = value[:limit]
		for !utf8.ValidString(value) {
			value = value[:len(value)-1]
		}
	}
	return value
}

func displayURL(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "http" && u.Scheme != "https" {
		return ""
	}
	u.User, u.RawQuery, u.Fragment, u.RawFragment = nil, "", "", ""
	u.Path, u.RawPath = "", ""
	// Paths can carry opaque login/reset tokens; show only the origin.
	return clean(u.String(), 512)
}
