package view

import (
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/harness"
)

func (v *View) agentsPane(c *ui.Context) {
	id := v.agentSection
	if id == "" {
		id = "codex"
	}
	tx, u := v.texts(), v.C.HarnessFor(id)
	path := &v.C.State.Settings.CodexPath
	if id == "pi" {
		path = &v.C.State.Settings.PiPath
	}
	if id == "droid" {
		path = &v.C.State.Settings.DroidPath
	}
	if id == "claude" {
		path = &v.C.State.Settings.ClaudePath
	}
	services := c.Services()
	for _, driver := range harness.Drivers {
		if driver.ID != id {
			continue
		}
		ui.Column(c.Key("harness-"+driver.ID)).Radius(10).Border(1, c.Theme().Border).Background(c.Theme().Background).Children(func() {
			ui.Row(c).Padding(14).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(34, 34).Radius(8).Background(c.Theme().Surface).Center().Children(func() { agentGlyph(c, id) })
				ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, driver.Name).Bold().FontSize(15)
						status := map[string]string{"Connected": tx.Connected(), "Disconnected": tx.Disconnected(), "Connecting": tx.Connecting()}[v.C.Connection]
						if id != "codex" {
							status = tx.HarnessUnchecked()
							if u.Missing {
								status = tx.HarnessMissing()
							} else if u.Installation.Path != "" {
								status = tx.AgentCLIAvailable()
							}
						}
						color := c.Theme().TextMuted
						if id == "codex" && v.C.Connection == "Connected" || id != "codex" && u.Installation.Path != "" {
							color = c.Theme().Accent
						}
						ui.Row(c).AlignItems(ui.Center).Gap(4).Children(func() {
							ui.Box(c).Size(5, 5).Radius(3).Background(color)
							ui.Text(c, status).FontSize(11).TextColor(color)
						})
					})
					version := u.Installation.Version
					if version == "" && id == "codex" && v.C.Connection == "Connected" {
						version = v.C.Version
					}
					if version == "" {
						version = tx.HarnessUnchecked()
						if !u.CheckedAt.IsZero() && u.Error == "" {
							version = tx.HarnessMissing()
						}
					}
					method := harness.ManagerName(u.Installation.Method)
					if u.Installation.Method == "native" {
						method = tx.HarnessNative()
					}
					if u.Installation.Method == "managed" {
						method = tx.HarnessManaged()
					}
					if method == "" && u.Installation.Method != "" {
						method = tx.HarnessCustom()
					}
					if method != "" {
						version += " · " + method
					}
					ui.Text(c, version).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
				})
				label := tx.CheckForUpdates()
				if u.Checking {
					label = tx.CheckingForUpdates()
				}
				ui.Button(c, label).Height(28).FontSize(12).Radius(6).Padding(0, 10).Disabled(u.Checking || u.Installing || u.Preparing).OnClick(func() { v.C.CheckHarness(id) })
				label = tx.Connect()
				action := v.C.Connect
				if id == "codex" {
					if v.C.Connection == "Connected" {
						label = tx.Reconnect()
					}
				} else {
					label = tx.AgentLoginHelp()
					action = func() {
						endpoint := "https://pi.dev/docs/quickstart"
						if id == "droid" {
							endpoint = "https://docs.factory.com/droid-cli/quickstart"
						}
						if id == "claude" {
							endpoint = "https://code.claude.com/docs/en/authentication"
						}
						services.OpenURL(endpoint)
					}
				}
				ui.PrimaryButton(c, label).Height(28).FontSize(12).Radius(6).Padding(0, 12).Disabled(id == "codex" && !v.C.CanConnect()).OnClick(action)
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
								if u.Missing || u.Installation.CanUpdate() {
									ui.Button(c, label).Height(28).FontSize(12).Radius(6).Disabled(u.Checking || v.C.HarnessInstalling() || u.Preparing || v.C.AnyRunning() || v.C.Connection == "Connecting" || u.Binary != v.C.State.Settings.AgentPath(id)).OnClick(v.openHarnessInstall)
								}
							}
						})
					}
					if u.Installing {
						ui.Row(c).AlignItems(ui.Center).Children(func() {
							ui.Text(c, tx.HarnessWorking()).FontSize(12).Grow(1)
							ui.Button(c, tx.Cancel()).Height(26).FontSize(12).OnClick(func() { v.C.CancelHarness(id) })
						})
						ui.Progress(c, -1).Height(3)
					}
					if u.Error != "" && !v.harnessDialog {
						ui.Text(c, u.Error).FontSize(12).TextColor(c.Theme().Danger).Selectable()
					}
				})
			}
			if u.Installation.Path != "" && !u.Installation.CanUpdate() {
				message := tx.HarnessUnknownManager()
				name := harness.ManagerName(u.Installation.Method)
				if name != "" {
					message = tx.HarnessUnsupportedManager(name)
				}
				switch u.Installation.UpdateIssue {
				case "unavailable":
					message = tx.HarnessManagerUnavailable(name)
				case "unverified":
					message = tx.HarnessManagerUnverified(name)
				case "ambiguous":
					message = tx.HarnessManagerAmbiguous()
				}
				ui.Row(c.Key("unsupported-update")).Margin(0, 14, 12).Padding(10).Gap(8).Radius(8).Background(c.Theme().Surface).AlignItems(ui.Start).Children(func() {
					ui.Icon(c, failureIcon).Size(15, 15).Shrink(0).TextColor(ui.Hex("#a06b12"))
					ui.Text(c, message).FontSize(12).Grow(1).MinWidth(0).TextColor(ui.Hex("#a06b12"))
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
							ui.Text(c, tx.AgentPath()).Width(112).Shrink(0).FontSize(12).TextColor(c.Theme().TextMuted)
							ui.TextInput(c, path).Height(28).Radius(6).FontSize(12).Grow(1).MinWidth(0).Label(tx.AgentPath()).Disabled(v.C.AnyRunning() || u.Installing || u.Checking || u.Preparing || v.C.Connection == "Connecting").OnChange(func() { v.C.DismissHarnessPlan(id); u.ModelsChecked = false; v.C.Save() })
						})
						if u.Installation.Path != "" && u.Installation.Path != v.C.State.Settings.AgentPath(id) {
							ui.Text(c, u.Installation.Path).Padding(0, 0, 0, 124).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine().Tooltip(u.Installation.Path)
						}
						account := v.statusText(v.C.Account)
						if id != "codex" {
							account = tx.AgentCLIAuth()
						}
						if id == "codex" && v.C.Account == "Configured provider (no OpenAI login required)" {
							account = tx.HarnessProviderAuth()
						}
						if account == "" {
							account = "—"
						}
						ui.Row(c).MinHeight(28).AlignItems(ui.Center).Gap(12).Children(func() {
							ui.Text(c, tx.Account()).Width(112).Shrink(0).FontSize(12).TextColor(c.Theme().TextMuted)
							ui.Text(c, account).FontSize(12).Grow(1).MinWidth(0).Selectable()
						})
						if id == "codex" {
							ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
								ui.Text(c, tx.CodexDefaultPermissions()).Width(112).Shrink(0).FontSize(12).TextColor(c.Theme().TextMuted)
								settingsSelect(c.Key("default-permissions"), &v.C.State.Settings.DefaultPermissionProfile, tx.CodexDefaultPermissions(), []settingOption{{":read-only", tx.ReadOnly()}, {":workspace", tx.DefaultPermissions()}, {":approve-for-me", tx.ApproveForMe()}, {":danger-full-access", tx.FullAccess()}}, v.C.Save)
							})
						} else {
							v.agentDefaultPermissions(c, id)
						}
					})
				})
			})
		})
	}
}
