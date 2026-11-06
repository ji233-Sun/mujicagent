package view

import (
	"fmt"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/markdown"
)

func (v *View) appUpdates(c *ui.Context) {
	tx, u := v.texts(), &v.C.AppUpdates
	ui.Column(c).Radius(14).Background(c.Theme().Surface).Padding(20).Gap(16).Children(func() {
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(42, 42).Radius(12).Background(c.Theme().Background).Center().Children(func() {
				ui.Icon(c, downloadIcon).Size(22, 22).TextColor(c.Theme().Accent)
			})
			ui.Column(c).Grow(1).Gap(4).Children(func() {
				ui.Text(c, "MujicAgent").FontSize(16).Bold()
				version := u.Version
				if version == "" {
					version = tx.DevelopmentBuild()
				}
				ui.Text(c, version).FontSize(12).TextColor(c.Theme().TextMuted)
			})
			label := tx.CheckForUpdates()
			if u.Checking {
				label = tx.CheckingForUpdates()
			}
			ui.Button(c, label).Radius(16).Disabled(u.Checking || u.Installing || u.Ready).OnClick(v.C.CheckAppUpdates)
		})
		ui.Divider(c)
		ui.Row(c).Gap(12).AlignItems(ui.Center).Children(func() {
			status := tx.AutoCheckUpdates()
			switch {
			case u.Checking:
				status = tx.CheckingForUpdates()
			case u.Installing:
				status = tx.DownloadingUpdate()
			case u.Ready:
				status = tx.UpdateReady()
			case u.Error != "":
				status = tx.UpdateFailed()
			case u.AvailableVersion != "":
				status = tx.NewVersion(u.AvailableVersion)
			case !u.Enabled:
				status = tx.UpdatesUnavailable()
			case !u.CheckedAt.IsZero() && len(u.Releases) == 0:
				status = tx.NoReleases()
			case !u.CheckedAt.IsZero():
				status = tx.UpToDate()
			}
			ui.Text(c, status).Grow(1).FontSize(13)
			if u.Ready {
				ui.Button(c, tx.RestartToUpdate()).Radius(16).Disabled(v.C.AnyRunning()).OnClick(v.C.RestartForUpdate)
			} else if u.AvailableVersion != "" && !u.Installing {
				ui.Button(c, tx.DownloadUpdate()).Radius(16).Disabled(!u.Enabled || u.Checking || v.C.AnyRunning()).OnClick(v.C.InstallAppUpdate)
			}
		})
		if u.Installing {
			progress := float64(-1)
			if u.Total > 0 {
				progress = min(1, float64(u.Downloaded)/float64(u.Total))
			}
			ui.Progress(c, progress).Height(4)
			if u.Total > 0 {
				ui.Text(c, fmt.Sprintf("%.1f / %.1f MB", float64(u.Downloaded)/1e6, float64(u.Total)/1e6)).FontSize(11).TextColor(c.Theme().TextMuted)
			}
		}
		if u.AvailableVersion != "" && v.C.AnyRunning() {
			ui.Text(c, tx.UpdateAfterConversation()).FontSize(12).TextColor(c.Theme().TextMuted)
		}
		if u.Error != "" {
			ui.Text(c, u.Error).FontSize(12).TextColor(c.Theme().Danger).Selectable()
		}
	})
	ui.Text(c, tx.ReleaseNotes()).FontSize(13).Bold().Padding(6, 4, 0)
	if len(u.Releases) == 0 {
		ui.Text(c, tx.NoReleases()).FontSize(12).TextColor(c.Theme().TextMuted).Padding(4)
	}
	for i, release := range u.Releases {
		key := "release-" + release.Tag
		if _, exists := v.expanded[key]; !exists {
			open := i == 0
			v.expanded[key] = &open
		}
		ui.Column(c.Key(key)).Radius(12).Border(1, c.Theme().Border).Children(func() {
			p := ui.CollapsibleBase(c, v.openState(key))
			p.Trigger.Height(42).Padding(0, 14).Gap(8).AlignItems(ui.Center).Label(release.Tag)
			p.Trigger.Children(func() {
				ui.Icon(c, chevron).Size(13, 13).Rotate(90 * p.Progress()).TextColor(c.Theme().TextMuted)
				ui.Text(c, release.Tag).FontSize(13).Bold().Grow(1)
				if !release.Published.IsZero() {
					ui.Text(c, release.Published.Local().Format("2006-01-02")).FontSize(11).TextColor(c.Theme().TextMuted)
				}
			})
			p.Panel(func() {
				if release.Notes == "" {
					ui.Text(c, tx.NoReleaseNotes()).FontSize(12).TextColor(c.Theme().TextMuted)
				} else {
					markdown.View(c, release.Notes, &v.md)
				}
			}).Padding(0, 16, 16)
		})
	}
}
