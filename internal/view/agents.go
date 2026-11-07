package view

import (
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/harness"
)

func (v *View) agentsPane(c *ui.Context) {
	tx, u := v.texts(), v.C.HarnessFor("codex")
	if v.agentSection == "pi" || v.agentSection == "droid" {
		v.externalAgentPane(c, v.agentSection)
		return
	}
	for _, driver := range harness.Drivers {
		if driver.ID != "codex" {
			continue
		}
		ui.Column(c.Key("harness-"+driver.ID)).Radius(10).Border(1, c.Theme().Border).Background(c.Theme().Background).Children(func() {
			ui.Row(c).Padding(14).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(34, 34).Radius(8).Background(c.Theme().Surface).Center().Children(func() { ui.Icon(c, openAIIcon).Size(23, 23) })
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, driver.Name).Bold().FontSize(15)
						status := map[string]string{"Connected": tx.Connected(), "Disconnected": tx.Disconnected(), "Connecting": tx.Connecting()}[v.C.Connection]
						color := c.Theme().TextMuted
						if v.C.Connection == "Connected" {
							color = c.Theme().Accent
						}
						ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
							ui.Box(c).Size(5, 5).Radius(3).Background(color)
							ui.Text(c, status).FontSize(11).TextColor(color)
						})
					})
					version := u.Installation.Version
					if version == "" && v.C.Connection == "Connected" {
						version = v.C.Version
					}
					if version == "" {
						version = tx.HarnessUnchecked()
						if !u.CheckedAt.IsZero() && u.Error == "" {
							version = tx.HarnessMissing()
						}
					}
					method := map[string]string{"managed": tx.HarnessManaged(), "custom": tx.HarnessCustom(), "npm": "npm", "brew": "Homebrew", "vp": "VP"}[u.Installation.Method]
					if method != "" {
						version += " · " + method
					}
					ui.Text(c, version).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
				})
				label := tx.CheckForUpdates()
				if u.Checking {
					label = tx.CheckingForUpdates()
				}
				ui.Button(c, label).Height(28).FontSize(12).Radius(6).Padding(0, 10).Disabled(u.Checking || u.Installing || u.Preparing).OnClick(v.C.CheckHarness)
				label = tx.Connect()
				if v.C.Connection == "Connected" {
					label = tx.Reconnect()
				}
				ui.PrimaryButton(c, label).Height(28).FontSize(12).Radius(6).Padding(0, 12).Disabled(v.C.AnyRunning() || u.Installing || u.Preparing || v.C.Connection == "Connecting").OnClick(v.C.Connect)
			})
			if u.Release.Version != "" || u.Missing || u.Installing || u.Error != "" {
				ui.Column(c).Padding(0, 14, 10).Gap(6).Children(func() {
					if u.Release.Version != "" || u.Missing {
						ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
							available := u.Missing || harness.Newer(u.Release.Version, u.Installation.Version)
							label := tx.UpToDate()
							if u.Missing {
								label = tx.HarnessMissing()
							} else if available {
								label = tx.NewVersion(u.Release.Version)
							}
							ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted).Grow(1)
							if available {
								label = tx.HarnessUpdate()
								if u.Missing {
									label = tx.HarnessInstall()
								}
								if u.Installation.Method == "custom" {
									ui.Text(c, tx.HarnessUnknownManager()).FontSize(12).TextColor(c.Theme().TextMuted)
								} else {
									ui.Button(c, label).Height(28).FontSize(12).Radius(6).Disabled(u.Checking || u.Installing || u.Preparing || v.C.AnyRunning() || v.C.Connection == "Connecting" || u.Binary != v.C.State.Settings.CodexPath).OnClick(v.openHarnessInstall)
								}
							}
						})
					}
					if u.Installing {
						ui.Row(c).AlignItems(ui.Center).Children(func() {
							ui.Text(c, tx.HarnessWorking()).FontSize(12).Grow(1)
							ui.Button(c, tx.Cancel()).Height(26).FontSize(12).OnClick(v.C.CancelHarness)
						})
						ui.Progress(c, -1).Height(3)
					}
					if u.Error != "" && !v.harnessDialog {
						ui.Text(c, u.Error).FontSize(12).TextColor(c.Theme().Danger).Selectable()
					}
				})
			}
			ui.Divider(c)
			ui.Row(c).Padding(10, 14).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Grow(1)
				auto := !v.C.State.Settings.DisableHarnessAutoCheck
				ui.Checkbox(c, &auto, tx.HarnessAuto()).FontSize(12).OnClick(func() { v.C.State.Settings.DisableHarnessAutoCheck = !auto; v.C.Save() })
			})
			ui.Divider(c)
			ui.Column(c).Padding(0, 14).Children(func() {
				detail := ui.CollapsibleBase(c.Key("details"), v.openState("harness-"+driver.ID))
				detail.Trigger.Height(36).Gap(6).AlignItems(ui.Center).Label(tx.HarnessDetails()).Children(func() {
					ui.Icon(c, chevron).Size(11, 11).Rotate(90 * detail.Progress()).TextColor(c.Theme().TextMuted)
					ui.Text(c, tx.HarnessDetails()).FontSize(12).Bold()
				})
				detail.Panel(func() {
					ui.Column(c).Padding(0, 0, 12).Gap(8).Children(func() {
						ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
							ui.Text(c, tx.CodexPath()).Width(112).Shrink(0).FontSize(12).TextColor(c.Theme().TextMuted)
							ui.TextInput(c, &v.C.State.Settings.CodexPath).Height(28).Radius(6).FontSize(12).Grow(1).MinWidth(0).Label(tx.CodexPath()).Disabled(v.C.AnyRunning() || u.Installing || u.Checking || u.Preparing || v.C.Connection == "Connecting").OnChange(v.C.Save)
						})
						if u.Installation.Path != "" && u.Installation.Path != v.C.State.Settings.CodexPath {
							ui.Text(c, u.Installation.Path).Padding(0, 0, 0, 124).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(u.Installation.Path)
						}
						account := v.statusText(v.C.Account)
						if v.C.Account == "Configured provider (no OpenAI login required)" {
							account = tx.HarnessProviderAuth()
						}
						if account == "" {
							account = "—"
						}
						ui.Row(c).MinHeight(28).AlignItems(ui.Center).Gap(12).Children(func() {
							ui.Text(c, tx.Account()).Width(112).Shrink(0).FontSize(12).TextColor(c.Theme().TextMuted)
							ui.Text(c, account).FontSize(12).Grow(1).MinWidth(0).Selectable()
						})
						ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
							ui.Text(c, tx.CodexDefaultPermissions()).Width(112).Shrink(0).FontSize(12).TextColor(c.Theme().TextMuted)
							settingsSelect(c.Key("default-permissions"), &v.C.State.Settings.DefaultPermissionProfile, tx.CodexDefaultPermissions(), []settingOption{{":read-only", tx.ReadOnly()}, {":workspace", tx.DefaultPermissions()}, {":approve-for-me", tx.ApproveForMe()}, {":danger-full-access", tx.FullAccess()}}, v.C.Save)
						})
					})
				})
			})
		})
	}
}
