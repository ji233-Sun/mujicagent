package view

import (
	"context"

	"github.com/ji233-Sun/mujicagent/internal/browser"
)

// AttachBrowser binds both native rendering and UI commands to the executor
// used by the controller's Codex dispatcher. Call after assigning Window.
func (v *View) AttachBrowser(runtime *browser.Runtime) {
	publish := func(id string, reply BrowserReply, status browser.Status, err error) {
		v.Window.Update(func() {
			if v.C.State.Thread(id) == nil {
				if status.Page.PageID != "" {
					go func() { _, _ = runtime.Close(context.Background(), status.Page.PageKey) }()
				}
				return
			}
			if current, statusErr := runtime.Status(context.Background(), id); statusErr == nil {
				status = current
			}
			reply(status, err)
		})
	}
	v.Browser.Page = runtime.View
	v.Browser.Actions = BrowserActions{
		Create: func(id string, reply BrowserReply) {
			if v.C.State.Thread(id) == nil {
				reply(browser.Status{}, browser.ErrNotFound)
				return
			}
			go func() {
				status, err := runtime.Create(context.Background(), id)
				publish(id, reply, status, err)
			}()
		},
		Open: func(req browser.OpenRequest, reply func(browser.ActionResult, browser.Status, error)) {
			go func() {
				result, err := runtime.Open(context.Background(), req)
				status, _ := runtime.Status(context.Background(), req.Page.ThreadID)
				publish(req.Page.ThreadID, func(s browser.Status, e error) { reply(result, s, e) }, status, err)
			}()
		},
		TakeOver: func(key browser.PageKey, reply BrowserReply) {
			v.C.RevokeBrowser(key)
			go func() {
				status, err := runtime.TakeOver(context.Background(), key)
				publish(key.ThreadID, reply, status, err)
			}()
		},
		Resume: func(key browser.PageKey, permissions browser.Permissions, reply BrowserReply) {
			// Resume only changes locked host state. Grant and rearm in this
			// UI action so a later takeover cannot be overtaken by its worker.
			status, err := runtime.Resume(context.Background(), key, permissions)
			if err == nil {
				v.C.ResumeBrowser(key)
			}
			reply(status, err)
		},
		Close: func(key browser.PageKey, reply BrowserReply) {
			v.C.RevokeBrowser(key)
			go func() {
				status, err := runtime.Close(context.Background(), key)
				publish(key.ThreadID, reply, status, err)
			}()
		},
	}
}
