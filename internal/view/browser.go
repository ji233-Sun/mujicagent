package view

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/browser"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

// BrowserReply must run on the UI thread, normally through Window.Update.
// The host calls it only after the corresponding Sessions operation returns.
type BrowserReply func(browser.Status, error)

// BrowserActions are nonblocking host callbacks backed by browser.Sessions and
// browser.Tools. Open also returns a fresh status, including on failure; its
// ActionResult must retain dispatch and completion information from Tools.Open.
// The view neither creates an engine nor performs work in a rendering callback.
// The host applies DefaultTimeout, serializes snapshot delivery, and owns
// shutdown/deletion cleanup. Replies and SetStatus run through Window.Update.
type BrowserActions struct {
	Create   func(string, BrowserReply)
	Open     func(browser.OpenRequest, func(browser.ActionResult, browser.Status, error))
	TakeOver func(browser.PageKey, BrowserReply)
	Resume   func(browser.PageKey, browser.Permissions, BrowserReply)
	Close    func(browser.PageKey, BrowserReply)
}

// BrowserPanel is the view boundary for #31's runtime assembly. Page resolves
// the exact PageKey to a real native WebView; nil means it is not attached yet.
// Configure callbacks and publish snapshots on the UI thread. A zero panel is
// usable and honestly reports that the browser is not connected.
type BrowserPanel struct {
	Actions  BrowserActions
	Page     func(browser.PageKey) ui.NativeWebView
	threads  map[string]*browserThread
	selected string
}

type browserThread struct {
	status      browser.Status
	visible     bool
	address     string
	addressURL  string
	addressEdit bool
	addressBox  ui.Handle
	permissions browser.Permissions
	confirm     bool
	pending     string
	sequence    uint64
	err         error
	blocked     bool
	nativeInput bool
	resetInput  time.Time
}

func (p *BrowserPanel) thread(id string) *browserThread {
	if p.threads == nil {
		p.threads = map[string]*browserThread{}
	}
	if p.threads[id] == nil {
		p.threads[id] = &browserThread{}
	}
	return p.threads[id]
}

// SetStatus publishes a detached Sessions.Status snapshot. A replacement page
// is accepted only by its Create reply; late snapshots cannot resurrect an old
// page, reverse a navigation, or reopen a closed page. It does not acknowledge
// an outstanding UI action: the host must still invoke that action's reply.
func (p *BrowserPanel) SetStatus(status browser.Status) {
	if status.Page.ThreadID == "" || status.Page.PageID == "" || status.Page.Navigation == 0 {
		return
	}
	s := p.thread(status.Page.ThreadID)
	old := s.status
	if old.Page.PageID != "" && (old.Page.PageKey != status.Page.PageKey || status.Page.Navigation < old.Page.Navigation || old.State == browser.StateClosed && status.State != browser.StateClosed) {
		return
	}
	s.status = status
	s.blocked = false
	if !s.addressEdit && status.URL != s.addressURL {
		s.address, s.addressURL = status.URL, status.URL
	}
}

func (s *browserThread) live() bool {
	return s.status.Page.PageID != "" && s.status.State != browser.StateClosed
}

func (s *browserThread) interactive() bool {
	return s.live() && s.status.State == browser.StateHuman && !s.status.Busy && !s.status.Loading && s.pending == "" && !s.blocked
}

// begin correlates replies with the page and UI action that issued them.
// TakeOver and Close can supersede pending work; its late reply is then inert.
func (p *BrowserPanel) begin(id, action string) BrowserReply {
	s := p.thread(id)
	s.sequence++
	sequence, page := s.sequence, s.status.Page.PageKey
	s.pending, s.err, s.confirm = action, nil, false
	return func(status browser.Status, err error) {
		if s.sequence != sequence {
			return
		}
		s.sequence++
		s.pending = ""
		valid := status.Page.ThreadID == id && status.Page.PageID != "" && status.Page.Navigation > 0
		if action != "create" {
			valid = valid && status.Page.PageKey == page && status.Page.Navigation >= s.status.Page.Navigation && (s.status.State != browser.StateClosed || status.State == browser.StateClosed)
		}
		if valid {
			if action == "create" && s.status.Page.PageKey != status.Page.PageKey {
				s.status = browser.Status{}
				s.permissions = browser.Permissions{}
				s.address, s.addressURL, s.addressEdit = "", "", false
			}
			p.SetStatus(status)
		} else {
			s.blocked = true
			if err == nil {
				err = browser.ErrStale
			}
		}
		s.err = err
		if action == "create" && valid && p.selected != id && s.live() {
			p.takeOver(id)
		}
	}
}

func (p *BrowserPanel) takeOver(id string) {
	s := p.thread(id)
	if !s.live() || s.pending == "takeover" || s.pending == "close" {
		return
	}
	if p.Actions.TakeOver == nil {
		// Retain a blocking failure rather than allow input or resume after an
		// unacknowledged operation when the host cannot revoke control.
		s.err = browser.ErrUnsupported
		s.blocked = true
		return
	}
	p.Actions.TakeOver(s.status.Page.PageKey, p.begin(id, "takeover"))
}

func (p *BrowserPanel) selectThread(id string) {
	if p.selected == id {
		return
	}
	old := p.selected
	p.selected = id
	if old != "" {
		p.thread(old).confirm = false
		p.takeOver(old)
	}
}

func (v *View) browserToggle(c *ui.Context, t *model.Thread) {
	if t == nil {
		return
	}
	tx, p := v.texts(), &v.Browser
	s := p.thread(t.ID)
	label := tx.BrowserShow()
	if s.visible {
		label = tx.BrowserHide()
	}
	ui.Button(c.Key("browser-toggle"), tx.Browser()).Height(28).Label(label).Tooltip(label).OnClick(func() {
		s.visible = !s.visible
		if s.visible && !s.live() && s.pending == "" && !s.status.Busy && p.Actions.Create != nil {
			p.Actions.Create(t.ID, p.begin(t.ID, "create"))
		}
	})
	if !s.visible && (s.status.State == browser.StateAgent || s.status.State == browser.StateIdle || s.pending != "") {
		label := tx.BrowserHiddenActive()
		if s.pending != "" {
			label = v.browserState(s)
		}
		ui.Text(c, label).FontSize(11).TextColor(c.Theme().Accent).SingleLine().MaxWidth(200).Tooltip(v.browserState(s))
		ui.Button(c, tx.BrowserTakeOver()).Height(28).Disabled(p.Actions.TakeOver == nil || s.pending == "takeover" || s.pending == "close").OnClick(func() { p.takeOver(t.ID) })
	}
}

func (v *View) browserPane(c *ui.Context, id string) {
	tx, p := v.texts(), &v.Browser
	s := p.thread(id)
	ui.Column(c.Key("browser-" + id)).Fill().MinHeight(0).Label(tx.BrowserPanel()).Children(func() {
		ui.Row(c).Height(38).Shrink(0).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tx.Browser()).Bold().Grow(1)
			iconButton(c, tx.BrowserHide(), chevron, func() { s.visible = false })
			iconButton(c, tx.BrowserClose(), closeIcon, func() {
				p.Actions.Close(s.status.Page.PageKey, p.begin(id, "close"))
			}).Disabled(p.Actions.Close == nil || s.status.Page.PageID == "" || s.pending == "close" || s.status.State == browser.StateClosed && !s.status.Busy)
		})
		ui.Row(c).Shrink(0).Padding(0, 10, 6).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.TextInput(c.Key("browser-address"), &s.address).Bind(&s.addressBox).Grow(1).MinWidth(0).Height(30).
				Label(tx.BrowserAddress()).Placeholder("https://example.com").Disabled(!s.interactive() || p.Actions.Open == nil).
				OnChange(func() { s.addressEdit = true }).OnSubmit(func() { v.browserOpen(id) })
			ui.Button(c, tx.BrowserGo()).Height(30).Disabled(!s.interactive() || p.Actions.Open == nil).OnClick(func() { v.browserOpen(id) })
		})
		ui.Row(c).Height(34).Shrink(0).Padding(0, 10).Gap(8).AlignItems(ui.Center).Children(func() {
			if s.status.Loading || s.status.Busy || s.pending != "" {
				ui.Spinner(c).Size(12, 12).Shrink(0)
			}
			ui.Text(c, v.browserState(s)).FontSize(11).SingleLine().Grow(1).MinWidth(0).Tooltip(v.browserState(s))
			if s.live() {
				if s.status.State == browser.StateHuman && s.pending != "takeover" {
					ui.Button(c, tx.BrowserResume()).Height(26).Disabled(!s.interactive() || p.Actions.Resume == nil || p.Actions.TakeOver == nil).OnClick(func() {
						s.permissions = browser.Permissions{}
						s.confirm = true
					})
				} else {
					ui.Button(c, tx.BrowserTakeOver()).Height(26).Disabled(p.Actions.TakeOver == nil || s.pending == "takeover" || s.pending == "close").OnClick(func() { p.takeOver(id) })
				}
			}
		})
		if s.status.Loading {
			ui.Text(c, tx.BrowserLoading()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(0, 10, 4)
		}
		err := s.err
		if err == nil {
			err = s.status.LastError
		}
		if err != nil {
			ui.Text(c, v.browserError(err)).FontSize(11).TextColor(c.Theme().Danger).Padding(2, 10, 6)
		}
		ui.Box(c.Key("browser-viewport")).Grow(1).MinHeight(0).Clip().Background(c.Theme().Surface).Label(tx.BrowserViewport()).Children(func() {
			var page ui.NativeWebView
			if s.live() && p.Page != nil {
				page = p.Page(s.status.Page.PageKey)
			}
			if page != nil {
				interactive := s.interactive()
				if s.nativeInput && !interactive {
					// Hiding for one committed frame returns the native keyboard to
					// the window. Disabled alone does not blur a focused WebView.
					s.resetInput = c.Now().Add(time.Millisecond)
				}
				s.nativeInput = interactive
				if c.Now().Before(s.resetInput) {
					c.After(s.resetInput.Sub(c.Now()))
					return
				}
				ui.WebView(c.Key(s.status.Page.PageKey), page).Fill().Disabled(!interactive).Label(tx.BrowserPage())
				if !interactive {
					// Disabled removes Tab focus; a covering hit also blocks native
					// pointer routing, which WebView does not gate with Disabled.
					ui.Box(c.Key("browser-input-guard")).Absolute().Fill().Label(tx.BrowserInputBlocked())
				}
				return
			}
			ui.Column(c).Fill().Center().Padding(12).Gap(8).Children(func() {
				label := tx.BrowserUnavailable()
				if s.live() {
					label = tx.BrowserWaitingPage()
				} else if s.status.State == browser.StateClosed {
					label = tx.BrowserClosed()
				}
				ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
				if !s.live() && p.Actions.Create != nil {
					ui.Button(c, tx.BrowserOpen()).Disabled(s.pending != "" || s.status.Busy).OnClick(func() { p.Actions.Create(id, p.begin(id, "create")) })
				}
			})
		})
	})
}

func (v *View) browserOpen(id string) {
	p := &v.Browser
	s := p.thread(id)
	if !s.interactive() || p.Actions.Open == nil {
		return
	}
	address := strings.TrimSpace(s.address)
	if browser.ValidateURL(address) != nil {
		s.err = browser.ErrInvalidArgument
		return
	}
	request := browser.OpenRequest{Request: browser.Request{Page: s.status.Page, Actor: browser.ActorHuman}, URL: address}
	reply := p.begin(id, "open")
	sequence := s.sequence
	p.Actions.Open(request, func(result browser.ActionResult, status browser.Status, err error) {
		if s.sequence != sequence {
			return
		}
		if err == nil && (result.Page.PageKey != request.Page.PageKey || result.Page.Navigation < request.Page.Navigation || status.Page.Navigation < result.Page.Navigation || result.Completion != browser.CompletionObserved || strings.TrimSpace(result.Evidence) == "" || result.Dispatch != browser.DispatchNone && result.Dispatch != browser.DispatchSent) {
			err = browser.ErrUnconfirmed
		}
		reply(status, err)
		if s.sequence == sequence+1 && s.pending == "" && !s.blocked && s.status.Page.PageKey == request.Page.PageKey && s.status.Page.Navigation >= result.Page.Navigation {
			s.addressEdit = false
			s.address, s.addressURL = s.status.URL, s.status.URL
		}
	})
}

func (v *View) browserResumeDialog(c *ui.Context) {
	p, tx := &v.Browser, v.texts()
	id := v.C.State.Selected
	if id == "" {
		return
	}
	s := p.thread(id)
	ui.DialogBase(c.Key("browser-resume-"+id), &s.confirm, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.18))
		panel.Width(420).MaxWidthPercent(90).Padding(20).Gap(12).Radius(14).Border(1, c.Theme().Border).Background(c.Theme().Background).Label(tx.BrowserPermissions())
		ui.Text(c, tx.BrowserResume()).Bold().FontSize(16)
		ui.Text(c, tx.BrowserPermissionsHint()).FontSize(12)
		ui.Checkbox(c, &s.permissions.Navigate, tx.BrowserAllowNavigate())
		ui.Checkbox(c, &s.permissions.Interact, tx.BrowserAllowInteract())
		ui.Checkbox(c, &s.permissions.Capture, tx.BrowserAllowCapture()).Disabled(!s.status.CaptureSupported)
		ui.Text(c, tx.BrowserCaptureHint()).FontSize(11).TextColor(c.Theme().TextMuted)
		if !s.status.CaptureSupported {
			ui.Text(c, tx.BrowserCaptureUnavailable()).FontSize(11).TextColor(c.Theme().TextMuted)
		}
		ui.Text(c, s.status.URL).FontSize(11).SingleLine().TextColor(c.Theme().TextMuted)
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			ui.Button(c, tx.Cancel()).OnClick(func() { s.confirm = false })
			ui.PrimaryButton(c, tx.BrowserConfirmResume()).Disabled(!s.interactive() || p.Actions.Resume == nil || p.Actions.TakeOver == nil).OnClick(func() {
				p.Actions.Resume(s.status.Page.PageKey, s.permissions, p.begin(id, "resume"))
			})
		})
	})
}

func (v *View) browserState(s *browserThread) string {
	tx := v.texts()
	switch s.pending {
	case "create":
		return tx.BrowserOpening()
	case "open":
		return tx.BrowserNavigating()
	case "takeover":
		return tx.BrowserPausing()
	case "resume":
		return tx.BrowserResuming()
	case "close":
		return tx.BrowserClosing()
	}
	switch s.status.State {
	case browser.StateAgent:
		return tx.BrowserAgent()
	case browser.StateIdle:
		return tx.BrowserIdle()
	case browser.StateHuman:
		if s.status.Busy {
			return tx.BrowserDraining()
		}
		return tx.BrowserHuman()
	case browser.StateClosed:
		if s.status.Busy {
			return tx.BrowserClosing()
		}
		return tx.BrowserClosed()
	default:
		return tx.BrowserUnavailable()
	}
}

// Never render wrapped engine/page error text, which can contain credentials.
func (v *View) browserError(err error) string {
	tx := v.texts()
	for _, item := range []struct {
		err  error
		text string
	}{
		{browser.ErrInvalidArgument, tx.BrowserInvalidAddress()},
		{browser.ErrNotFound, tx.BrowserNotFound()},
		{browser.ErrClosed, tx.BrowserClosed()},
		{browser.ErrStale, tx.BrowserStale()},
		{browser.ErrBusy, tx.BrowserBusy()},
		{browser.ErrPermissionDenied, tx.BrowserDenied()},
		{browser.ErrUnsupported, tx.BrowserUnsupported()},
		{browser.ErrSensitive, tx.BrowserSensitive()},
		{browser.ErrUnconfirmed, tx.BrowserUnconfirmed()},
		{context.Canceled, tx.BrowserCancelled()},
		{context.DeadlineExceeded, tx.BrowserTimeout()},
	} {
		if errors.Is(err, item.err) {
			return item.text
		}
	}
	return tx.BrowserEngineError()
}

func (v *View) browserActivityText(item *model.Item, expanded bool) (string, bool) {
	if item.MCP == nil {
		return "", false
	}
	tx := v.texts()
	var title string
	switch item.MCP.Tool {
	case "browser_open":
		title = tx.BrowserToolOpen()
	case "browser_observe":
		title = tx.BrowserToolObserve()
	case "browser_click":
		title = tx.BrowserToolClick()
	case "browser_fill":
		title = tx.BrowserToolFill()
	case "browser_scroll":
		title = tx.BrowserToolScroll()
	case "browser_capture":
		title = tx.BrowserToolCapture()
	default:
		return "", false
	}
	if !expanded {
		return title, true
	}
	var result struct {
		Dispatch   browser.Dispatch
		Completion browser.Completion
		Error      *struct{ Code string }
	}
	_ = json.Unmarshal([]byte(item.MCP.Result), &result)
	if result.Error != nil {
		classification := map[string]error{
			"cancelled": context.Canceled, "timeout": context.DeadlineExceeded,
			"invalid_argument": browser.ErrInvalidArgument, "not_found": browser.ErrNotFound,
			"closed": browser.ErrClosed, "stale": browser.ErrStale, "busy": browser.ErrBusy,
			"permission_denied": browser.ErrPermissionDenied, "unsupported": browser.ErrUnsupported,
			"sensitive": browser.ErrSensitive, "unconfirmed": browser.ErrUnconfirmed,
		}[result.Error.Code]
		if classification == nil {
			classification = browser.ErrEngine
		}
		title += "\n" + v.browserError(classification)
	} else if result.Completion == browser.CompletionObserved {
		title += "\n" + tx.BrowserObserved()
	} else if item.Status == "completed" {
		title += "\n" + tx.McpCompleted()
	}
	switch result.Dispatch {
	case browser.DispatchNone:
		title += "\n" + tx.BrowserNotDispatched()
	case browser.DispatchSent:
		if result.Completion != browser.CompletionObserved {
			title += "\n" + tx.BrowserDispatched()
		}
	case browser.DispatchUnknown:
		title += "\n" + tx.BrowserDispatchUnknown()
	}
	return title, true
}
