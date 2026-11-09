package main

import (
	"context"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/browser"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"github.com/ji233-Sun/mujicagent/internal/view"
)

func main() {
	mygo.Theme.SetSource(mygo.ThemeLight)
	dir, err := os.UserConfigDir()
	if err != nil {
		log.Fatal(err)
	}
	dir = filepath.Join(dir, "MujicAgent")
	// A separate data directory is useful for development and isolated smoke tests.
	if override := os.Getenv("MUJICAGENT_DATA_DIR"); override != "" {
		dir = override
		mygo.App.SetPath(mygo.PathUserData, dir)
	}
	state, err := model.Load(filepath.Join(dir, "state.json"))
	if err != nil {
		log.Fatal(err)
	}
	mygo.App.WhenReady(func() {
		mygo.App.SetLocale(state.Settings.Language)
		var win *mygo.Window
		ctrl := controller.New(dir, state, func() {
			if win != nil {
				win.Invalidate()
			}
		})
		v := view.New(ctrl)
		win = mygo.NewWindow(mygo.WindowOptions{
			Title: "MujicAgent", Width: 1140, Height: 780, MinWidth: 840, MinHeight: 560,
			StateKey: "main", TitleBarStyle: mygo.TitleBarHiddenInset, TitleBarHeight: 54,
			Vibrancy: mygo.VibrancySidebar, Content: ui.View(v.Render),
		})
		v.Window = win
		var runtime *browser.Runtime
		runtime = browser.New(win, func(snapshot browser.Status) {
			win.Update(func() {
				if ctrl.State.Thread(snapshot.Page.ThreadID) == nil {
					return
				}
				// Fetch authoritative state here: queued worker notifications
				// can otherwise apply a snapshot after a newer lifecycle event.
				status, err := runtime.Status(context.Background(), snapshot.Page.ThreadID)
				if err == nil {
					v.Browser.SetStatus(status)
				}
			})
		})
		ctrl.ConfigureBrowser(runtime)
		v.AttachBrowser(runtime)
		mygo.App.OnQuit(ctrl.Close)
		ctrl.Connect()
		ctrl.StartAppUpdates()
		ctrl.StartHarnessUpdates()
	})
	if err := mygo.App.Run(); err != nil {
		log.Fatal(err)
	}
}
