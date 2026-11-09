package browser

import (
	"context"
	"errors"
	"math"
	"net/url"
	"strings"
	"sync"

	"github.com/ji233-Sun/mujicagent/internal/model"
)

// Runtime owns ephemeral pages. Engine objects never enter persistent state.
type Runtime struct {
	mu      sync.Mutex
	pages   map[string]*runtimePage
	factory func(context.Context, *runtimePage) (pageEngine, error)
	changed func(Status)
	closed  bool
}

type pageEngine interface {
	open(context.Context, string) error
	eval(context.Context, string, any) (pageData, error)
	capture(context.Context) (Screenshot, error)
	refresh()
	destroy()
}

type runtimePage struct {
	mu          sync.Mutex
	status      Status
	engine      pageEngine
	creating    bool
	cancel      context.CancelFunc
	done        chan struct{}
	observation string
	elements    map[string]Element
	url         string
	origin      string
	commits     uint64
}

type pageData struct {
	Code      string        `json:"code"`
	Text      string        `json:"text"`
	Title     string        `json:"title"`
	Revision  string        `json:"revision"`
	Elements  []pageElement `json:"elements"`
	Changed   bool          `json:"changed"`
	Truncated bool          `json:"truncated"`
}

type pageElement struct {
	ID, Role, Name string
	Click, Fill    bool
}

func newRuntime(changed func(Status)) *Runtime {
	return &Runtime{pages: map[string]*runtimePage{}, changed: changed}
}

func boundedContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(ctx, DefaultTimeout)
}

func (r *Runtime) publish(p *runtimePage) {
	if r.changed != nil {
		p.mu.Lock()
		s := p.status
		p.mu.Unlock()
		r.changed(s)
	}
}

func (r *Runtime) lookup(key PageKey) (*runtimePage, error) {
	r.mu.Lock()
	p := r.pages[key.ThreadID]
	r.mu.Unlock()
	if p == nil {
		return nil, ErrNotFound
	}
	p.mu.Lock()
	match := p.status.Page.PageKey == key
	p.mu.Unlock()
	if !match {
		return nil, ErrNotFound
	}
	return p, nil
}

func (r *Runtime) Create(ctx context.Context, thread string) (Status, error) {
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	if strings.TrimSpace(thread) == "" {
		return Status{}, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	r.mu.Lock()
	if r.closed {
		r.mu.Unlock()
		return Status{}, ErrClosed
	}
	if old := r.pages[thread]; old != nil {
		old.mu.Lock()
		s, creating := old.status, old.creating
		old.mu.Unlock()
		if s.State != StateClosed || s.Busy {
			r.mu.Unlock()
			if creating || s.State == StateClosed {
				return s, ErrBusy
			}
			return s, nil
		}
	}
	p := &runtimePage{creating: true, done: make(chan struct{}), elements: map[string]Element{}}
	p.status = Status{Page: PageRef{PageKey: PageKey{ThreadID: thread, PageID: model.ID()}, Navigation: 1}, State: StateHuman, Busy: true}
	r.pages[thread] = p
	r.mu.Unlock()
	// Creation can enter the native main thread. Never hold a runtime lock here.
	finished := make(chan error, 1)
	go func() {
		engine, err := r.factory(ctx, p)
		p.mu.Lock()
		p.engine, p.creating = engine, false
		if err != nil || ctx.Err() != nil || p.status.State == StateClosed {
			p.status.State = StateClosed
			if err == nil {
				err = ctx.Err()
			}
			if err == nil {
				err = ErrClosed
			}
		}
		closed := p.status.State == StateClosed
		p.mu.Unlock()
		if closed && engine != nil {
			engine.destroy()
		}
		p.mu.Lock()
		p.status.Busy = false
		close(p.done)
		p.mu.Unlock()
		r.publish(p)
		finished <- safeError(err)
	}()
	var err error
	select {
	case err = <-finished:
	case <-ctx.Done():
		p.mu.Lock()
		p.status.State, p.status.Permissions = StateClosed, Permissions{}
		p.invalidate()
		engine := p.engine
		p.mu.Unlock()
		if engine != nil {
			engine.destroy()
		}
		err = ctx.Err()
		r.publish(p)
	}
	p.mu.Lock()
	s := p.status
	p.mu.Unlock()
	return s, err
}

func (r *Runtime) Status(ctx context.Context, thread string) (Status, error) {
	if err := ctx.Err(); err != nil {
		return Status{}, err
	}
	r.mu.Lock()
	p := r.pages[thread]
	r.mu.Unlock()
	if p == nil {
		return Status{}, ErrNotFound
	}
	p.mu.Lock()
	engine, live := p.engine, p.status.State != StateClosed
	p.mu.Unlock()
	if engine != nil && live {
		engine.refresh()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.status, nil
}

func (p *runtimePage) invalidate() {
	p.observation = ""
	p.elements = map[string]Element{}
}

func (r *Runtime) Resume(ctx context.Context, key PageKey, permissions Permissions) (Status, error) {
	p, err := r.lookup(key)
	if err != nil {
		return Status{}, err
	}
	p.mu.Lock()
	s := p.status
	switch {
	case ctx.Err() != nil:
		err = ctx.Err()
	case s.State == StateClosed:
		err = ErrClosed
	case s.State != StateHuman:
		err = ErrPermissionDenied
	case s.Busy || s.Loading:
		err = ErrBusy
	case p.url != "" && p.url != "about:blank" && !p.authorizedOrigin(p.url):
		err = ErrPermissionDenied
	default:
		p.invalidate()
		p.status.State, p.status.Permissions, p.status.LastError = StateIdle, permissions, nil
		s = p.status
	}
	p.mu.Unlock()
	r.publish(p)
	return s, err
}

func (r *Runtime) TakeOver(ctx context.Context, key PageKey) (Status, error) {
	return r.revoke(ctx, key, false)
}

func (r *Runtime) Close(ctx context.Context, key PageKey) (Status, error) {
	return r.revoke(ctx, key, true)
}

func (r *Runtime) revoke(ctx context.Context, key PageKey, closing bool) (Status, error) {
	ctx, cancel := boundedContext(ctx)
	defer cancel()
	p, err := r.lookup(key)
	if err != nil {
		return Status{}, err
	}
	p.mu.Lock()
	if !closing && p.status.State == StateClosed {
		s := p.status
		p.mu.Unlock()
		return s, ErrClosed
	}
	p.status.State, p.status.Permissions = StateHuman, Permissions{}
	if closing {
		p.status.State = StateClosed
	}
	p.invalidate()
	if p.cancel != nil {
		p.cancel()
	}
	done, engine, creating := p.done, p.engine, p.creating
	if closing {
		p.status.Busy = true
	}
	p.mu.Unlock()
	r.publish(p)
	if closing && engine != nil {
		engine.destroy()
	}
	// A cancelled waiter does not release the next operator. The worker owns
	// Busy until the native operation has actually returned.
	if done != nil {
		select {
		case <-done:
		case <-ctx.Done():
			s, _ := r.Status(context.Background(), key.ThreadID)
			return s, ctx.Err()
		}
	}
	p.mu.Lock()
	if closing && !creating {
		p.status.Busy, p.status.Loading = false, false
	}
	s := p.status
	p.mu.Unlock()
	r.publish(p)
	return s, nil
}

// Shutdown revokes before returning; native destruction is safe during app quit.
func (r *Runtime) Shutdown() {
	r.mu.Lock()
	r.closed = true
	pages := make([]*runtimePage, 0, len(r.pages))
	for _, p := range r.pages {
		pages = append(pages, p)
	}
	r.mu.Unlock()
	for _, p := range pages {
		p.mu.Lock()
		p.status.State, p.status.Permissions = StateClosed, Permissions{}
		p.invalidate()
		if p.cancel != nil {
			p.cancel()
		}
		engine := p.engine
		p.mu.Unlock()
		if engine != nil {
			engine.destroy()
		}
	}
}

type operation struct {
	p      *runtimePage
	ctx    context.Context
	cancel context.CancelFunc
}

func (r *Runtime) begin(ctx context.Context, req Request, permission string) (*operation, error) {
	if req.Page.ThreadID == "" || req.Page.PageID == "" || req.Page.Navigation == 0 || req.Actor != ActorAgent && req.Actor != ActorHuman {
		return nil, ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	p, err := r.lookup(req.Page.PageKey)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	engine, live := p.engine, p.status.State != StateClosed
	p.mu.Unlock()
	if engine != nil && live {
		engine.refresh()
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status
	switch {
	case s.State == StateClosed:
		return nil, ErrClosed
	case s.Page != req.Page:
		return nil, ErrStale
	case s.Busy || s.Loading:
		return nil, ErrBusy
	case req.Actor == ActorAgent && s.State != StateIdle, req.Actor == ActorHuman && s.State != StateHuman:
		return nil, ErrPermissionDenied
	case req.Actor == ActorAgent && (permission == "navigate" && !s.Permissions.Navigate || permission == "interact" && !s.Permissions.Interact || permission == "capture" && !s.Permissions.Capture):
		return nil, ErrPermissionDenied
	}
	ctx, cancel := boundedContext(ctx)
	p.cancel, p.done, p.status.Busy, p.status.LastError = cancel, make(chan struct{}), true, nil
	if req.Actor == ActorAgent {
		p.status.State = StateAgent
	}
	return &operation{p: p, ctx: ctx, cancel: cancel}, nil
}

func (r *Runtime) run(ctx context.Context, req Request, permission string, work func(*operation) (any, error)) (any, error) {
	op, err := r.begin(ctx, req, permission)
	if err != nil {
		return nil, err
	}
	defer op.cancel()
	r.publish(op.p)
	type output struct {
		value any
		err   error
	}
	finished := make(chan output, 1)
	go func() {
		value, err := work(op)
		p := op.p
		p.mu.Lock()
		if op.ctx.Err() != nil {
			err = op.ctx.Err()
			if p.status.State != StateClosed {
				p.status.State = StateHuman
			}
			p.status.Permissions = Permissions{}
			p.invalidate()
		}
		if p.status.State == StateAgent {
			p.status.State = StateIdle
		}
		p.status.LastError, p.status.Busy, p.cancel = safeError(err), false, nil
		close(p.done)
		p.mu.Unlock()
		r.publish(p)
		finished <- output{value, safeError(err)}
	}()
	select {
	case out := <-finished:
		return out.value, out.err
	case <-op.ctx.Done():
		select {
		case out := <-finished:
			return out.value, out.err
		default:
		}
		op.p.mu.Lock()
		if op.p.status.State != StateClosed {
			op.p.status.State = StateHuman
		}
		op.p.status.Permissions = Permissions{}
		op.p.invalidate()
		op.p.mu.Unlock()
		r.publish(op.p)
		return nil, op.ctx.Err()
	}
}

func (op *operation) reference() PageRef {
	op.p.mu.Lock()
	defer op.p.mu.Unlock()
	return op.p.status.Page
}

func (r *Runtime) Open(ctx context.Context, req OpenRequest) (ActionResult, error) {
	u, err := parseURL(req.URL)
	result := ActionResult{Page: req.Page, Dispatch: DispatchNone, Completion: CompletionUnconfirmed}
	if err != nil {
		return result, err
	}
	value, err := r.run(ctx, req.Request, "navigate", func(op *operation) (any, error) {
		result := result
		p := op.p
		p.mu.Lock()
		if req.Actor == ActorAgent && p.origin != origin(u) {
			p.mu.Unlock()
			return result, ErrPermissionDenied
		}
		p.origin = origin(u) // Only a human address-bar request authorizes a new origin.
		p.invalidate()
		p.status.Page.Navigation++
		p.status.Loading = true
		p.mu.Unlock()
		result.Dispatch = DispatchSent
		err := p.engine.open(op.ctx, req.URL)
		p.mu.Lock()
		p.status.Loading = false
		result.Page = p.status.Page
		p.mu.Unlock()
		if err == nil {
			result.Completion, result.Evidence = CompletionObserved, "Document loaded."
		}
		return result, err
	})
	if value != nil {
		return value.(ActionResult), err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		result.Dispatch = DispatchUnknown
	}
	return result, err
}

func (r *Runtime) Observe(ctx context.Context, req Request) (Observation, error) {
	value, err := r.run(ctx, req, "", func(op *operation) (any, error) {
		p := op.p
		p.mu.Lock()
		p.invalidate()
		page := p.status.Page
		p.mu.Unlock()
		data, err := p.engine.eval(op.ctx, "observe", nil)
		if err != nil {
			return nil, err
		}
		if len(data.Elements) > 128 || len(data.Text) > 16384 || len(data.Title) > 512 || data.Revision == "" {
			return nil, ErrEngine
		}
		p.mu.Lock()
		defer p.mu.Unlock()
		if p.status.Page != page || op.ctx.Err() != nil {
			return nil, ErrStale
		}
		id := model.ID()
		observation := Observation{Page: page, ObservationID: id, URL: p.status.URL, Title: clean(data.Title, 256), Text: clean(data.Text, 12000), Truncated: data.Truncated}
		p.observation = data.Revision
		for _, item := range data.Elements {
			if item.ID == "" || len(item.ID) > 64 {
				return nil, ErrEngine
			}
			ref := ElementRef{PageRef: page, ObservationID: id, ID: model.ID()}
			element := Element{Ref: ref, Role: clean(item.Role, 32), Name: clean(item.Name, 256), CanClick: item.Click, CanFill: item.Fill}
			p.elements[ref.ID] = element
			// Engine IDs are internal and never accepted from model arguments.
			p.elements["engine:"+ref.ID] = Element{Ref: ElementRef{ID: item.ID}}
			observation.Elements = append(observation.Elements, element)
		}
		return observation, nil
	})
	if err != nil {
		return Observation{}, err
	}
	return value.(Observation), nil
}

func (r *Runtime) Click(ctx context.Context, req ClickRequest) (ActionResult, error) {
	return r.elementAction(ctx, req.Request, req.Element, "click", "")
}

func (r *Runtime) Fill(ctx context.Context, req FillRequest) (ActionResult, error) {
	if len(req.Text) > 16384 {
		return ActionResult{Page: req.Page, Dispatch: DispatchNone}, ErrInvalidArgument
	}
	return r.elementAction(ctx, req.Request, req.Element, "fill", req.Text)
}

func (r *Runtime) elementAction(ctx context.Context, req Request, ref ElementRef, action, text string) (ActionResult, error) {
	result := ActionResult{Page: req.Page, Dispatch: DispatchNone, Completion: CompletionUnconfirmed}
	value, err := r.run(ctx, req, "interact", func(op *operation) (any, error) {
		result := result
		p := op.p
		p.mu.Lock()
		element, exists := p.elements[ref.ID]
		engineID := p.elements["engine:"+ref.ID].Ref.ID
		revision := p.observation
		p.invalidate()
		p.mu.Unlock()
		if !exists || element.Ref != ref || ref.PageRef != req.Page {
			return result, ErrStale
		}
		if action == "fill" && !element.CanFill || action == "click" && !element.CanClick {
			return result, ErrUnsupported
		}
		result.Dispatch = DispatchUnknown
		data, err := p.engine.eval(op.ctx, action, map[string]any{"id": engineID, "revision": revision, "text": text})
		result.Page = op.reference()
		if data.Code == "stale" || data.Code == "sensitive" || data.Code == "unsupported" {
			result.Dispatch = DispatchNone
		}
		if err != nil {
			return result, err
		}
		if data.Code == "stale" || data.Code == "sensitive" || data.Code == "unsupported" {
			result.Dispatch = DispatchNone
		} else {
			result.Dispatch = DispatchSent
		}
		if err = dataError(data); err != nil {
			return result, err
		}
		if !data.Changed && result.Page == req.Page {
			return result, ErrUnconfirmed
		}
		result.Completion, result.Evidence = CompletionObserved, "Page state changed."
		return result, nil
	})
	if value != nil {
		return value.(ActionResult), err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		result.Dispatch = DispatchUnknown
	}
	return result, err
}

func (r *Runtime) Scroll(ctx context.Context, req ScrollRequest) (ActionResult, error) {
	result := ActionResult{Page: req.Page, Dispatch: DispatchNone, Completion: CompletionUnconfirmed}
	if math.IsNaN(req.DeltaX) || math.IsNaN(req.DeltaY) || math.IsInf(req.DeltaX, 0) || math.IsInf(req.DeltaY, 0) || req.DeltaX == 0 && req.DeltaY == 0 || math.Abs(req.DeltaX) > 1e6 || math.Abs(req.DeltaY) > 1e6 {
		return result, ErrInvalidArgument
	}
	value, err := r.run(ctx, req.Request, "interact", func(op *operation) (any, error) {
		result := result
		op.p.mu.Lock()
		op.p.invalidate()
		op.p.mu.Unlock()
		result.Dispatch = DispatchUnknown
		data, err := op.p.engine.eval(op.ctx, "scroll", map[string]any{"x": req.DeltaX, "y": req.DeltaY})
		result.Page, result.Dispatch = op.reference(), DispatchSent
		if err != nil {
			return result, err
		}
		if !data.Changed {
			return result, ErrUnconfirmed
		}
		result.Completion, result.Evidence = CompletionObserved, "Viewport offset changed."
		return result, nil
	})
	if value != nil {
		return value.(ActionResult), err
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		result.Dispatch = DispatchUnknown
	}
	return result, err
}

func (r *Runtime) Capture(ctx context.Context, req Request) (Screenshot, error) {
	value, err := r.run(ctx, req, "capture", func(op *operation) (any, error) {
		ref := op.reference()
		image, err := op.p.engine.capture(op.ctx)
		if err != nil {
			return nil, err
		}
		if ref != op.reference() || op.ctx.Err() != nil {
			return nil, ErrStale
		}
		image.Page = ref
		return image, nil
	})
	if err != nil {
		return Screenshot{}, err
	}
	return value.(Screenshot), nil
}

func parseURL(raw string) (*url.URL, error) {
	u, err := url.Parse(raw)
	if err != nil || len(raw) > 8192 || u.Hostname() == "" || u.User != nil || u.Opaque != "" || u.Scheme != "http" && u.Scheme != "https" || strings.ContainsAny(raw, "\r\n\t\\") {
		return nil, ErrInvalidArgument
	}
	return u, nil
}

// ValidateURL is shared by the human address bar and runtime entry point.
func ValidateURL(raw string) error {
	_, err := parseURL(raw)
	return err
}

func (p *runtimePage) authorizedOrigin(raw string) bool {
	u, err := parseURL(raw)
	return err == nil && origin(u) == p.origin
}

// Called under p.mu using a native URL, including history navigation paths that
// bypass OnWillNavigate. A commit does not grant access to the committed origin.
func (p *runtimePage) acceptLocation(raw string) {
	p.url, p.status.URL = raw, displayURL(raw)
	if raw != "about:blank" && !p.authorizedOrigin(raw) {
		p.status.State, p.status.Permissions, p.status.LastError = StateHuman, Permissions{}, ErrPermissionDenied
		p.invalidate()
		if p.cancel != nil {
			p.cancel()
		}
	}
}

func origin(u *url.URL) string {
	return strings.ToLower(u.Scheme + "://" + u.Host)
}

func dataError(data pageData) error {
	switch data.Code {
	case "ok":
		return nil
	case "stale":
		return ErrStale
	case "sensitive":
		return ErrSensitive
	case "unsupported":
		return ErrUnsupported
	case "unconfirmed":
		return ErrUnconfirmed
	default:
		return ErrEngine
	}
}

func safeError(err error) error {
	if err == nil {
		return nil
	}
	for _, sentinel := range []error{context.Canceled, context.DeadlineExceeded, ErrInvalidArgument, ErrNotFound, ErrClosed, ErrStale, ErrBusy, ErrPermissionDenied, ErrUnsupported, ErrSensitive, ErrUnconfirmed} {
		if errors.Is(err, sentinel) {
			return sentinel
		}
	}
	return ErrEngine
}
