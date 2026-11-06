package view

import (
	"time"

	"github.com/egoist/mygo/ui"
)

func (v *View) settingsDialog(c *ui.Context) {
	dialog := ui.DialogBase(c.Key("settings-dialog"), &v.Settings, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.18))
		panel.Width(840).Height(570).MaxWidthPercent(94).MaxHeightPercent(90).
			Radius(18).Border(1, c.Theme().Border).Background(c.Theme().Background).
			Shadow(0, 16, 50, 0, ui.RGBA(0, 0, 0, 0.22)).Clip().Label(v.texts().SettingsPanel()).
			Transition(ui.ElementTransition{Duration: 180 * time.Millisecond, Enter: &ui.Motion{Y: 8, Scale: 0.98}})
		ui.Row(c).Fill().AlignItems(ui.Stretch).Children(func() {
			ui.Column(c).Width(176).Shrink(0).Padding(16, 12).Background(c.Theme().Surface).Children(func() {
				v.settingsSidebar(c)
			})
			ui.Column(c).Grow(1).MinWidth(0).Children(func() {
				v.settingsPane(c)
			})
		})
	})
	if dialog.Dismissed() {
		v.C.Save()
	}
}

func (v *View) settingsSidebar(c *ui.Context) {
	tx := v.texts()
	ui.Text(c, tx.Settings()).FontSize(18).Bold().Padding(4, 10, 22)
	for _, name := range []string{"Appearance", "Agents", "Updates"} {
		label := tx.Appearance()
		if name == "Agents" {
			label = tx.Agents()
		} else if name == "Updates" {
			label = tx.AppUpdates()
		}
		b := ui.ButtonBase(c.Key(name)).Height(44).Radius(10).Padding(0, 12).Gap(10).AlignItems(ui.Center).Justify(ui.Start).Label(label)
		if v.section == name {
			b.Background(c.Theme().SurfaceHover)
		} else if b.Hovered() {
			b.Background(c.Theme().Surface)
		}
		b.Children(func() {
			icon := appearanceIcon
			if name == "Agents" {
				icon = agentIcon
			} else if name == "Updates" {
				icon = downloadIcon
			}
			ui.Icon(c, icon).Size(20, 20)
			ui.Text(c, label).FontSize(14).Grow(1)
			if name == "Updates" && v.C.AppUpdates.AvailableVersion != "" {
				ui.Box(c).Size(6, 6).Radius(3).Background(c.Theme().Accent)
			}
		})
		b.OnClick(func() { v.section = name })
	}
}

func (v *View) settingsPane(c *ui.Context) {
	tx := v.texts()
	title := tx.Agents()
	if v.section == "Appearance" {
		title = tx.Appearance()
	} else if v.section == "Updates" {
		title = tx.AppUpdates()
	}
	ui.Row(c).Height(62).Shrink(0).Padding(0, 20, 0, 24).AlignItems(ui.Center).Children(func() {
		ui.Text(c, title).Bold().FontSize(20).Grow(1)
		iconButton(c, tx.CloseSettings(), closeIcon, func() { v.Settings = false; v.C.Save() })
	})
	v.banner(c)
	ui.Scroll(c).Grow(1).MinHeight(0).Padding(4, 24, 24).Gap(16).Children(func() {
		if v.section == "Updates" {
			v.appUpdates(c)
			return
		}
		if v.section == "Appearance" {
			ui.Column(c).Radius(14).Background(c.Theme().Surface).Padding(18).Gap(18).Children(func() {
				ui.Row(c).AlignItems(ui.Center).Children(func() {
					ui.Text(c, tx.Language()).Grow(1)
					settingsSelect(c.Key("language"), &v.C.State.Settings.Language, tx.Language(), []settingOption{
						{"zh-Hans", "简体中文"}, {"en", "English"},
					}, v.languageChanged)
				})
			})
			return
		}
		ui.Column(c).Radius(14).Background(c.Theme().Surface).Padding(20).Gap(18).Children(func() {
			ui.Row(c).Gap(14).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(44, 44).Radius(12).Background(c.Theme().Background).Center().Children(func() { ui.Icon(c, openAIIcon).Size(26, 26) })
				ui.Column(c).Grow(1).Gap(5).Children(func() {
					ui.Text(c, "Codex").FontSize(16).Bold()
					label := map[string]string{"Connected": tx.Connected(), "Disconnected": tx.Disconnected(), "Connecting": tx.Connecting()}[v.C.Connection]
					ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
				})
				label := tx.Connect()
				if v.C.Connection == "Connected" {
					label = tx.Reconnect()
				}
				ui.Button(c, label).Radius(16).Disabled(v.C.Connection == "Connecting" || v.C.AnyRunning()).OnClick(v.C.Connect)
			})
			ui.Divider(c)
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Text(c, tx.Version()).Grow(1)
				label := v.statusText(v.C.Version)
				if label == "" {
					label = "—"
				}
				ui.Text(c, label).TextColor(c.Theme().TextMuted)
			})
			ui.Divider(c)
			ui.Row(c).AlignItems(ui.Center).Gap(16).Children(func() {
				ui.Text(c, tx.CodexDefaultPermissions()).Grow(1)
				settingsSelect(c.Key("default-permissions"), &v.C.State.Settings.DefaultPermissionProfile, tx.CodexDefaultPermissions(), []settingOption{
					{":read-only", tx.ReadOnly()},
					{":workspace", tx.DefaultPermissions()},
					{":approve-for-me", tx.ApproveForMe()},
					{":danger-full-access", tx.FullAccess()},
				}, v.C.Save)
			})
			ui.Text(c, tx.DefaultPermissionsHint()).FontSize(12).TextColor(c.Theme().TextMuted)
		})
		ui.Column(c).Radius(14).Background(c.Theme().Surface).Padding(18).Gap(14).Children(func() {
			ui.Text(c, tx.Connection()).Bold()
			ui.Row(c).Gap(16).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tx.CodexPath()).Width(92)
				ui.TextInput(c, &v.C.State.Settings.CodexPath).Grow(1).Label(tx.CodexPath()).Disabled(v.C.AnyRunning()).OnChange(v.C.Save)
			})
			ui.Divider(c)
			ui.Row(c).Gap(16).Children(func() {
				ui.Text(c, tx.Account()).Width(92)
				label := v.statusText(v.C.Account)
				if label == "" {
					label = "—"
				}
				ui.Text(c, label).TextColor(c.Theme().TextMuted).Selectable().Grow(1)
			})
		})
	})
}

type settingOption struct{ value, label string }

// Keep stored values language-independent while showing localized labels.
func settingsSelect(c *ui.Context, value *string, label string, options []settingOption, changed func()) {
	selected := *value
	for _, option := range options {
		if option.value == *value {
			selected = option.label
		}
	}
	selectBox := ui.SelectBase(c, value)
	selectBox.Trigger.Width(170).Height(30).Radius(8).Border(1, c.Theme().Border).
		Padding(0, 10).Gap(8).AlignItems(ui.Center).Label(label).OnChange(changed).Children(func() {
		ui.Text(c, selected).Grow(1)
		ui.Icon(c, chevron).Size(14, 14).Rotate(90).TextColor(c.Theme().TextMuted)
	})
	selectBox.Popup(func(panel ui.Element) {
		panel.Padding(4).Margin(4, 0, 0, 0).Radius(10).Background(c.Theme().Background).
			Border(1, c.Theme().Border).Shadow(0, 6, 20, 0, ui.RGBA(0, 0, 0, 0.15))
		for _, option := range options {
			item := selectBox.Item(option.value).Padding(7, 10).Radius(6).Label(option.label)
			if item.Highlighted() {
				item.Background(c.Theme().SurfaceHover)
			}
			item.Children(func() { ui.Text(c, option.label) })
		}
	})
}
