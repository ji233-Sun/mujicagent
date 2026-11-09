package controller

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"net/url"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/ji233-Sun/mujicagent/internal/browser"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/harness"
)

// BrowserToolActivity uses the existing activity merge path. Callbacks may run
// on workers; the host must post them onto its UI queue before applying events.
type BrowserToolActivity struct {
	ThreadID, TurnID string
	Event            harness.AgentEvent
}

// BrowserToolDispatcher belongs to one App Server connection. #31 supplies the
// trusted bindings, event wiring, browser implementation, and lifecycle hooks.
type BrowserToolDispatcher struct {
	mu           sync.Mutex
	tools        browser.Tools
	sessions     browser.Sessions
	reply        func(json.RawMessage, any) error
	emit         func(BrowserToolActivity)
	turns        map[string]*browserToolTurn
	seen         map[string]bool
	calls        map[browserCallKey]bool
	active       map[*browserToolPending]bool
	closed       bool
	disconnected bool
}

type browserToolTurn struct {
	id     string
	page   browser.PageKey
	ctx    context.Context
	cancel context.CancelFunc
}

type browserCallKey struct{ thread, turn, call string }

type browserToolPending struct {
	id      json.RawMessage
	call    codex.DynamicToolCall
	args    codex.BrowserToolArguments
	turn    *browserToolTurn
	ctx     context.Context
	cancel  context.CancelFunc
	invoked bool
	done    bool
}

func NewBrowserToolDispatcher(tools browser.Tools, sessions browser.Sessions, reply func(json.RawMessage, any) error, emit func(BrowserToolActivity)) *BrowserToolDispatcher {
	return &BrowserToolDispatcher{tools: tools, sessions: sessions, reply: reply, emit: emit,
		turns: map[string]*browserToolTurn{}, seen: map[string]bool{}, calls: map[browserCallKey]bool{}, active: map[*browserToolPending]bool{}}
}

// BindTurn takes identities from the host's model.Thread/accepted turn, never
// from the selected thread or tool arguments. Replacing a turn revokes the old one.
func (d *BrowserToolDispatcher) BindTurn(ctx context.Context, serverThreadID, turnID string, page browser.PageKey) error {
	return d.bindTurn(ctx, serverThreadID, turnID, page, false)
}

// ResumeTurn is host-only and follows a successful explicit Sessions.Resume.
// Pending calls retain their old cancelled context and cannot reply twice.
func (d *BrowserToolDispatcher) ResumeTurn(ctx context.Context, serverThreadID, turnID string, page browser.PageKey) error {
	return d.bindTurn(ctx, serverThreadID, turnID, page, true)
}

func (d *BrowserToolDispatcher) bindTurn(ctx context.Context, serverThreadID, turnID string, page browser.PageKey, resume bool) error {
	if !browserRefID(serverThreadID) || !browserRefID(turnID) || !browserRefID(page.ThreadID) || !browserRefID(page.PageID) {
		return browser.ErrInvalidArgument
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return browser.ErrClosed
	}
	for serverID, turn := range d.turns {
		if serverID != serverThreadID && turn.page.ThreadID == page.ThreadID && turn.ctx.Err() == nil {
			return browser.ErrPermissionDenied
		}
	}
	if old := d.turns[serverThreadID]; old != nil {
		if old.id == turnID && (!resume || old.page != page || old.ctx.Err() == nil) {
			return browser.ErrStale
		}
		old.cancel()
	}
	ctx, cancel := context.WithCancel(ctx)
	d.turns[serverThreadID] = &browserToolTurn{id: turnID, page: page, ctx: ctx, cancel: cancel}
	return nil
}

func (d *BrowserToolDispatcher) StopTurn(serverThreadID, turnID string) {
	d.mu.Lock()
	defer d.mu.Unlock()
	if turn := d.turns[serverThreadID]; turn != nil && turn.id == turnID {
		turn.cancel()
	}
}

// ClosePage also serves takeover/switch hooks. The host still calls Sessions
// TakeOver/Close to revoke permissions and drain the actual engine operation.
func (d *BrowserToolDispatcher) ClosePage(page browser.PageKey) {
	d.mu.Lock()
	defer d.mu.Unlock()
	for _, turn := range d.turns {
		if turn.page == page {
			turn.cancel()
		}
	}
}

// Close settles outstanding requests with cancellation while transport is live.
func (d *BrowserToolDispatcher) Close() { d.shutdown(false) }

// Disconnect cancels work without trying to write to the lost connection.
// A replacement connection must get its own dispatcher and registration registry.
func (d *BrowserToolDispatcher) Disconnect() { d.shutdown(true) }

func (d *BrowserToolDispatcher) shutdown(disconnected bool) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.closed = true
	d.disconnected = d.disconnected || disconnected
	for _, turn := range d.turns {
		turn.cancel()
	}
}

// Handle consumes item/tool/call, including explicit errors for unsupported
// tools. Request tombstones live for the connection; completed calls are never
// evicted and replayed. No arguments or result payloads enter the tombstones.
func (d *BrowserToolDispatcher) Handle(m codex.Message) bool {
	if m.Method != "item/tool/call" {
		return false
	}
	key, valid := codex.BrowserRequestKey(m.ID)
	if !valid {
		return true // Invalid JSON-RPC identities cannot be answered as tool calls.
	}
	p := &browserToolPending{id: append(json.RawMessage(nil), m.ID...)}
	d.mu.Lock()
	if d.disconnected || d.seen[key] {
		d.mu.Unlock()
		return true
	}
	d.seen[key] = true
	closed := d.closed
	d.mu.Unlock()
	if closed {
		d.finish(p, browserFailure(browser.ErrClosed, browser.DispatchNone))
		return true
	}
	call, err := codex.DecodeDynamicToolCall(m.Params)
	if err != nil {
		d.finish(p, browserFailure(browser.ErrInvalidArgument, browser.DispatchNone))
		return true
	}
	p.call = call
	d.mu.Lock()
	turn := d.turns[call.ThreadID]
	switch {
	case turn == nil:
		err = browser.ErrNotFound
	case turn.id != call.TurnID:
		err = browser.ErrStale
	default:
		p.turn = turn
		callKey := browserCallKey{call.ThreadID, call.TurnID, call.CallID}
		if d.calls[callKey] {
			err = browser.ErrStale
			p.turn = nil // Do not overwrite the original activity on a replay.
		} else {
			d.calls[callKey] = true
			err = turn.ctx.Err()
		}
	}
	d.mu.Unlock()
	if err == nil {
		p.args, err = codex.ParseBrowserToolArguments(call)
		if errors.Is(err, codex.ErrBrowserToolUnknown) {
			err = browser.ErrUnsupported
		} else if err != nil {
			err = browser.ErrInvalidArgument
		}
	}
	if err != nil {
		d.finish(p, browserFailure(err, browser.DispatchNone))
		return true
	}
	d.mu.Lock()
	p.ctx, p.cancel = context.WithTimeout(turn.ctx, browser.DefaultTimeout)
	d.active[p] = true
	d.mu.Unlock()
	d.activity(p, browserToolOutput{}, "inProgress")
	go func() {
		defer p.cancel()
		result := make(chan browserToolOutput, 1)
		go func() { result <- d.execute(p) }()
		var output browserToolOutput
		select {
		case output = <-result:
		case <-p.ctx.Done():
			// Prefer a returned ActionResult, including DispatchSent, if available.
			select {
			case output = <-result:
			default:
				output = browserFailure(p.ctx.Err(), d.uncertainDispatch(p))
			}
		}
		d.finish(p, output)
	}()
	return true
}

func browserWriteTool(tool string) bool {
	return tool == codex.BrowserOpen || tool == codex.BrowserClick || tool == codex.BrowserFill || tool == codex.BrowserScroll
}

func (d *BrowserToolDispatcher) uncertainDispatch(p *browserToolPending) browser.Dispatch {
	d.mu.Lock()
	defer d.mu.Unlock()
	if p.invoked && browserWriteTool(p.call.Tool) {
		return browser.DispatchUnknown
	}
	return browser.DispatchNone
}

func (d *BrowserToolDispatcher) execute(p *browserToolPending) browserToolOutput {
	a, page := p.args, p.turn.page
	if a.PageID != page.PageID {
		return browserFailure(browser.ErrNotFound, browser.DispatchNone)
	}
	if d.tools == nil || d.sessions == nil {
		return browserFailure(browser.ErrUnsupported, browser.DispatchNone)
	}
	status, err := d.sessions.Status(p.ctx, page.ThreadID)
	if err != nil {
		return browserFailure(err, browser.DispatchNone)
	}
	switch {
	case status.Page.PageKey != page:
		err = browser.ErrNotFound
	case status.State == browser.StateClosed:
		err = browser.ErrClosed
	case status.Page.Navigation != a.Navigation:
		err = browser.ErrStale
	case status.Busy || status.Loading:
		err = browser.ErrBusy
	case status.State != browser.StateIdle:
		err = browser.ErrPermissionDenied
	case p.call.Tool == codex.BrowserOpen && !status.Permissions.Navigate,
		browserWriteTool(p.call.Tool) && p.call.Tool != codex.BrowserOpen && !status.Permissions.Interact,
		p.call.Tool == codex.BrowserCapture && !status.Permissions.Capture:
		err = browser.ErrPermissionDenied
	}
	if err != nil {
		output := browserFailure(err, browser.DispatchNone)
		if status.Page.PageKey == page {
			output.PageID, output.Navigation = status.Page.PageID, status.Page.Navigation
		}
		return output
	}
	d.mu.Lock()
	err = p.ctx.Err()
	if p.done || d.closed || d.turns[p.call.ThreadID] != p.turn {
		err = context.Canceled
	}
	if err == nil {
		p.invoked = true
	}
	d.mu.Unlock()
	if err != nil {
		return browserFailure(err, browser.DispatchNone)
	}
	req := browser.Request{Page: browser.PageRef{PageKey: page, Navigation: a.Navigation}, Actor: browser.ActorAgent}
	element := browser.ElementRef{PageRef: req.Page, ObservationID: a.ObservationID, ID: a.ElementID}
	var action browser.ActionResult
	switch p.call.Tool {
	case codex.BrowserOpen:
		action, err = d.tools.Open(p.ctx, browser.OpenRequest{Request: req, URL: a.URL})
	case codex.BrowserClick:
		action, err = d.tools.Click(p.ctx, browser.ClickRequest{Request: req, Element: element})
	case codex.BrowserFill:
		action, err = d.tools.Fill(p.ctx, browser.FillRequest{Request: req, Element: element, Text: a.Text})
	case codex.BrowserScroll:
		action, err = d.tools.Scroll(p.ctx, browser.ScrollRequest{Request: req, DeltaX: a.DeltaX, DeltaY: a.DeltaY})
	case codex.BrowserObserve:
		observation, err := d.tools.Observe(p.ctx, req)
		return browserObservationOutput(req.Page, observation, err)
	case codex.BrowserCapture:
		capture, err := d.tools.Capture(p.ctx, req)
		return browserCaptureOutput(req.Page, capture, err)
	}
	return browserActionOutput(req.Page, action, err, a.Text)
}

type browserToolError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// Protocol DTOs intentionally omit local ThreadID, engine handles, and raw structs.
type browserToolOutput struct {
	PageID        string             `json:"pageId,omitempty"`
	Navigation    uint64             `json:"navigation,omitempty"`
	Dispatch      browser.Dispatch   `json:"dispatch,omitempty"`
	Completion    browser.Completion `json:"completion,omitempty"`
	Evidence      string             `json:"evidence,omitempty"`
	ObservationID string             `json:"observationId,omitempty"`
	URL           string             `json:"url,omitempty"`
	Title         string             `json:"title,omitempty"`
	Text          string             `json:"text,omitempty"`
	Elements      []browserElement   `json:"elements,omitempty"`
	Truncated     bool               `json:"truncated,omitempty"`
	Width         int                `json:"width,omitempty"`
	Height        int                `json:"height,omitempty"`
	Error         *browserToolError  `json:"error,omitempty"`
	imageURL      string
}

type browserElement struct {
	ID       string `json:"elementId"`
	Role     string `json:"role"`
	Name     string `json:"name"`
	CanClick bool   `json:"canClick"`
	CanFill  bool   `json:"canFill"`
}

func browserFailure(err error, dispatch browser.Dispatch) browserToolOutput {
	code, message := "engine", "The browser operation failed."
	for _, entry := range []struct {
		err           error
		code, message string
	}{
		{context.Canceled, "cancelled", "The browser operation was cancelled. Observe again after explicit user resume; do not retry an uncertain action automatically."},
		{context.DeadlineExceeded, "timeout", "The browser operation timed out. Observe again after explicit user resume; do not retry an uncertain action automatically."},
		{browser.ErrInvalidArgument, "invalid_argument", "Invalid browser tool arguments."},
		{browser.ErrNotFound, "not_found", "The page is not available in this thread."},
		{browser.ErrClosed, "closed", "The browser page or dispatcher is closed."},
		{browser.ErrStale, "stale", "The turn, page, observation, or element reference is stale. Request a fresh observation."},
		{browser.ErrBusy, "busy", "The browser page is busy or loading."},
		{browser.ErrPermissionDenied, "permission_denied", "The host has not granted this browser operation."},
		{browser.ErrUnsupported, "unsupported", "This browser capability is unavailable."},
		{browser.ErrSensitive, "sensitive", "The browser data cannot be safely shared. Human takeover is required."},
		{browser.ErrUnconfirmed, "unconfirmed", "The target change was not observed. Do not retry a dispatched or uncertain action automatically."},
	} {
		if errors.Is(err, entry.err) {
			code, message = entry.code, entry.message
			break
		}
	}
	return browserToolOutput{Error: &browserToolError{Code: code, Message: message}, Dispatch: dispatch, Completion: browser.CompletionUnconfirmed}
}

func browserActionOutput(page browser.PageRef, action browser.ActionResult, err error, fillText string) browserToolOutput {
	dispatch := action.Dispatch
	if dispatch != browser.DispatchNone && dispatch != browser.DispatchSent {
		dispatch = browser.DispatchUnknown
	}
	validPage := action.Page.PageKey == page.PageKey && action.Page.Navigation >= page.Navigation
	evidence := action.Evidence
	if fillText != "" {
		evidence = strings.ReplaceAll(evidence, fillText, mcpRedacted)
	}
	if err == nil && !browserShareableText(evidence) {
		err = browser.ErrSensitive
	}
	if err == nil && (!validPage || action.Completion != browser.CompletionObserved || dispatch == browser.DispatchUnknown || strings.TrimSpace(evidence) == "") {
		err = browser.ErrUnconfirmed
	}
	if err != nil {
		output := browserFailure(err, dispatch)
		if validPage {
			output.PageID, output.Navigation = action.Page.PageID, action.Page.Navigation
		}
		return output
	}
	evidence, _ = browserText(evidence, 4096)
	if strings.TrimSpace(evidence) == "" {
		return browserFailure(browser.ErrSensitive, dispatch)
	}
	return browserToolOutput{PageID: action.Page.PageID, Navigation: action.Page.Navigation, Dispatch: dispatch, Completion: browser.CompletionObserved, Evidence: evidence}
}

func browserObservationOutput(page browser.PageRef, observation browser.Observation, err error) browserToolOutput {
	if err != nil {
		return browserFailure(err, browser.DispatchNone)
	}
	if observation.Page != page || !browserRefID(observation.ObservationID) {
		return browserFailure(browser.ErrStale, browser.DispatchNone)
	}
	if !browserShareableText(observation.URL) || !browserShareableText(observation.Title) || !browserShareableText(observation.Text) {
		return browserFailure(browser.ErrSensitive, browser.DispatchNone)
	}
	output := browserToolOutput{PageID: page.PageID, Navigation: page.Navigation, ObservationID: observation.ObservationID, Truncated: observation.Truncated}
	u, err := url.Parse(observation.URL)
	if err != nil || observation.URL != "" && observation.URL != "about:blank" && (u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https") {
		return browserFailure(browser.ErrSensitive, browser.DispatchNone)
	}
	// The executor sanitizes page data; also strip URL credentials/query/fragment
	// here so an accidental display URL cannot carry them into the model response.
	u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
	output.URL, _ = browserText(u.String(), 8192)
	var truncated bool
	output.Title, truncated = browserText(observation.Title, 512)
	output.Truncated = output.Truncated || truncated
	output.Text, truncated = browserText(observation.Text, 32768)
	output.Truncated = output.Truncated || truncated
	ids := map[string]bool{}
	for i, element := range observation.Elements {
		if i >= 128 {
			output.Truncated = true
			break
		}
		if element.Ref.PageRef != page || element.Ref.ObservationID != observation.ObservationID || !browserRefID(element.Ref.ID) || ids[element.Ref.ID] {
			return browserFailure(browser.ErrStale, browser.DispatchNone)
		}
		ids[element.Ref.ID] = true
		if !browserShareableText(element.Role) || !browserShareableText(element.Name) {
			return browserFailure(browser.ErrSensitive, browser.DispatchNone)
		}
		role, roleCut := browserText(element.Role, 64)
		name, nameCut := browserText(element.Name, 512)
		output.Truncated = output.Truncated || roleCut || nameCut
		output.Elements = append(output.Elements, browserElement{ID: element.Ref.ID, Role: role, Name: name, CanClick: element.CanClick, CanFill: element.CanFill})
	}
	return output
}

func browserCaptureOutput(page browser.PageRef, capture browser.Screenshot, err error) browserToolOutput {
	if err != nil {
		return browserFailure(err, browser.DispatchNone)
	}
	if capture.Page != page {
		return browserFailure(browser.ErrStale, browser.DispatchNone)
	}
	if len(capture.PNG) == 0 || len(capture.PNG) > 4*1024*1024 || capture.Width <= 0 || capture.Height <= 0 || capture.Width > 8192 || capture.Height > 8192 || int64(capture.Width)*int64(capture.Height) > 16*1024*1024 {
		return browserFailure(browser.ErrUnsupported, browser.DispatchNone)
	}
	config, err := png.DecodeConfig(bytes.NewReader(capture.PNG))
	if err != nil || config.Width != capture.Width || config.Height != capture.Height {
		return browserFailure(browser.ErrEngine, browser.DispatchNone)
	}
	if _, err := png.Decode(bytes.NewReader(capture.PNG)); err != nil {
		return browserFailure(browser.ErrEngine, browser.DispatchNone)
	}
	return browserToolOutput{PageID: page.PageID, Navigation: page.Navigation, Width: capture.Width, Height: capture.Height,
		imageURL: "data:image/png;base64," + base64.StdEncoding.EncodeToString(capture.PNG)}
}

func browserRefID(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 256 && utf8.ValidString(value) && strings.IndexFunc(value, unicode.IsControl) < 0
}

func browserText(value string, limit int) (string, bool) {
	text := []rune(redactMCPText(value))
	if len(text) > limit {
		return string(text[:limit]), true
	}
	return string(text), false
}

func browserShareableText(value string) bool {
	return utf8.ValidString(value) && len(value) <= 1024*1024
}

func (d *BrowserToolDispatcher) finish(p *browserToolPending, output browserToolOutput) {
	d.mu.Lock()
	if p.done {
		d.mu.Unlock()
		return
	}
	if p.ctx != nil && p.ctx.Err() != nil {
		dispatch := output.Dispatch
		if dispatch == "" {
			dispatch = browser.DispatchNone
			if p.invoked && browserWriteTool(p.call.Tool) {
				dispatch = browser.DispatchUnknown
			}
		}
		output = browserFailure(p.ctx.Err(), dispatch)
	}
	p.done = true
	delete(d.active, p)
	send := !d.disconnected && d.reply != nil
	d.mu.Unlock()
	if send {
		text, _ := json.Marshal(output)
		response := codex.DynamicToolResponse{Success: output.Error == nil, ContentItems: []codex.DynamicToolContent{{Type: "inputText", Text: string(text)}}}
		if output.Error == nil && output.imageURL != "" {
			response.ContentItems = append(response.ContentItems, codex.DynamicToolContent{Type: "inputImage", ImageURL: output.imageURL})
		}
		if err := d.reply(p.id, response); err != nil {
			// Never retry a reply or expose the transport's raw error.
			output = browserFailure(browser.ErrEngine, output.Dispatch)
		}
	}
	status := "completed"
	if output.Error != nil {
		status = "failed"
		if output.Error.Code == "cancelled" {
			status = "interrupted"
		}
	}
	d.activity(p, output, status)
}

func (d *BrowserToolDispatcher) activity(p *browserToolPending, output browserToolOutput, status string) {
	if d.emit == nil || p.turn == nil || !codex.IsBrowserTool(p.call.Tool) || p.call.Namespace != "" {
		return
	}
	tool := &harness.ToolEvent{Kind: "browser", Name: p.call.Tool}
	if status != "inProgress" {
		// Activity is persisted: keep only classifications and counts. Never put
		// URL, fill values, page text/evidence, element names, or image data here.
		tool.Result, _ = json.Marshal(struct {
			Dispatch   browser.Dispatch   `json:"dispatch,omitempty"`
			Completion browser.Completion `json:"completion,omitempty"`
			Error      *browserToolError  `json:"error,omitempty"`
			Elements   int                `json:"elementCount,omitempty"`
			Width      int                `json:"width,omitempty"`
			Height     int                `json:"height,omitempty"`
		}{output.Dispatch, output.Completion, output.Error, len(output.Elements), output.Width, output.Height})
		if output.Error != nil {
			message := output.Error.Message
			tool.Error = &message
		}
	}
	d.emit(BrowserToolActivity{ThreadID: p.turn.page.ThreadID, TurnID: p.turn.id,
		Event: harness.AgentEvent{ID: p.call.CallID, Kind: "toolCall", Status: status, Tool: tool}})
}
