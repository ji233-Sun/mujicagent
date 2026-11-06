package view

import (
	"fmt"

	"github.com/egoist/mygo/ui"
)

func (v *View) errorAlert(c *ui.Context) {
	if v.Settings || v.C.Error == "" {
		return
	}
	tx := v.texts()
	ui.Overlay(c, func() {
		ui.Column(c.Key("error-alert")).Absolute().Top(74).Right(24).Width(480).MaxWidthPercent(60).
			Padding(16).Gap(10).Radius(14).Background(ui.Hex("#fff2f2")).Border(1, ui.Hex("#e59898")).
			Shadow(0, 8, 28, 0, ui.RGBA(90, 13, 13, 0.16)).Role(ui.RoleStatus).Label(tx.ErrorAlert()).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(10).Children(func() {
				ui.Icon(c, failureIcon).Size(22, 22).TextColor(ui.Hex("#c52828"))
				ui.Text(c, tx.ErrorTitle()).FontSize(15).Bold().TextColor(ui.Hex("#a91d1d")).Grow(1)
				iconButton(c, tx.DismissNotice(), closeIcon, func() { v.C.Error = "" })
			})
			ui.Scroll(c).MaxHeight(160).Children(func() {
				ui.Text(c, v.statusText(v.C.Error)).FontSize(13).TextColor(ui.Hex("#8b2424")).Selectable()
			})
		})
	})
}

func (v *View) logsPane(c *ui.Context) {
	tx := v.texts()
	ui.Column(c).Padding(16).Gap(8).Radius(12).Background(c.Theme().Surface).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
			ui.Text(c, tx.DeveloperMode()).Bold().Grow(1)
			ui.Switch(c.Key("developer-mode"), &v.C.State.Settings.DeveloperMode).Label(tx.ToggleDeveloperMode()).OnChange(v.C.Save)
		})
		ui.Text(c, tx.DeveloperModeHint()).FontSize(12).TextColor(c.Theme().TextMuted)
	})
	ui.Text(c, tx.LogsHint()).FontSize(12).TextColor(c.Theme().TextMuted)
	visible := 0
	for i := len(v.C.Logs) - 1; i >= 0; i-- {
		entry := v.C.Logs[i]
		if entry.Level == "debug" && !v.C.State.Settings.DeveloperMode {
			continue
		}
		visible++
		color, label := c.Theme().TextMuted, tx.LogInfo()
		if entry.Level == "error" {
			color, label = c.Theme().Danger, tx.LogError()
		}
		if entry.Level == "debug" {
			label = tx.LogDebug()
		}
		ui.Column(c.Key(fmt.Sprintf("log-%d", entry.ID))).Padding(14).Gap(8).Radius(10).
			Background(c.Theme().Surface).Children(func() {
			ui.Row(c).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Text(c, label).FontSize(11).Bold().TextColor(color)
				ui.Text(c, entry.Time.Format("15:04:05")).FontSize(11).TextColor(c.Theme().TextMuted)
			})
			ui.Text(c, v.statusText(entry.Message)).FontSize(12).Selectable()
		})
	}
	if visible == 0 {
		ui.Text(c, tx.NoLogs()).Padding(24, 0).TextColor(c.Theme().TextMuted)
	}
}
