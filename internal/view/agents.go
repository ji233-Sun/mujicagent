package view

import (
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/harness"
)

func (v *View) agentsPane(c *ui.Context) {
	tx, u := v.texts(), &v.C.Harness
	// Each driver owns a stable card; connection-specific fields stay in its detail section.
	for _, driver := range harness.Drivers {
		ui.Column(c.Key("harness-" + driver.ID)).Radius(10).Background(c.Theme().Surface).Padding(12).Gap(10).Children(func() {
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
				ui.Icon(c, openAIIcon).Size(24, 24)
				ui.Text(c, driver.Name).Bold().Grow(1)
				status := map[string]string{"Connected": tx.Connected(), "Disconnected": tx.Disconnected(), "Connecting": tx.Connecting()}[v.C.Connection]
				ui.Text(c, status).FontSize(12).TextColor(c.Theme().TextMuted)
				label := tx.Connect()
				if v.C.Connection == "Connected" {
					label = tx.Reconnect()
				}
				ui.Button(c, label).Height(28).Disabled(v.C.AnyRunning() || u.Installing || v.C.Connection == "Connecting").OnClick(v.C.Connect)
			})
			ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
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
				method := map[string]string{"managed": tx.HarnessManaged(), "custom": tx.HarnessCustom(), "npm": "npm", "brew": "Homebrew"}[u.Installation.Method]
				if method != "" {
					version += " · " + method
				}
				ui.Text(c, version).FontSize(12).Grow(1)
				label := tx.CheckForUpdates()
				if u.Checking {
					label = tx.CheckingForUpdates()
				}
				ui.Button(c, label).Height(28).Disabled(u.Checking || u.Installing).OnClick(v.C.CheckHarness)
				if u.Release.Version != "" && (u.Installation.Version == "" || harness.Newer(u.Release.Version, u.Installation.Version)) {
					label := tx.HarnessUpdate()
					if u.Installation.Path == "" {
						label = tx.HarnessInstall()
					} else if u.Installation.Method == "custom" {
						label = tx.HarnessManagedInstall()
					}
					ui.Button(c, label).Height(28).Disabled(u.Checking || u.Installing || v.C.AnyRunning() || v.C.Connection == "Connecting" || u.Binary != v.C.State.Settings.CodexPath || u.Source != v.C.State.Settings.HarnessSource).OnClick(v.C.InstallHarness)
				}
			})
			if u.Release.Version != "" {
				label := tx.UpToDate()
				if u.Installation.Version == "" || harness.Newer(u.Release.Version, u.Installation.Version) {
					label = tx.NewVersion(u.Release.Version)
				}
				ui.Text(c, label).FontSize(12).TextColor(c.Theme().TextMuted)
			}
			ui.Divider(c)
			ui.Row(c).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tx.HarnessSource()).FontSize(13).Grow(1)
				ui.Box(c).Disabled(u.Checking || u.Installing).Children(func() {
					settingsSelect(c.Key("harness-source"), &v.C.State.Settings.HarnessSource, tx.HarnessSource(), []settingOption{{"", tx.HarnessOfficial()}, {"cn", tx.HarnessCN()}, {"cn-native", tx.HarnessNativeMirror()}}, func() { v.C.Save(); v.C.CheckHarness() })
				})
			})
			auto := !v.C.State.Settings.DisableHarnessAutoCheck
			ui.Checkbox(c, &auto, tx.HarnessAuto()).Label(tx.HarnessAuto()).OnClick(func() { v.C.State.Settings.DisableHarnessAutoCheck = !auto; v.C.Save() })
			if u.Installing {
				ui.Row(c).AlignItems(ui.Center).Children(func() { ui.Text(c, tx.HarnessWorking()).Grow(1); ui.Button(c, tx.Cancel()).OnClick(v.C.CancelHarness) })
				ui.Progress(c, -1).Height(3)
			}
			if u.Error != "" {
				ui.Text(c, u.Error).FontSize(12).TextColor(c.Theme().Danger).Selectable()
			}
			if v.C.AnyRunning() {
				ui.Text(c, tx.UpdateAfterConversation()).FontSize(12).TextColor(c.Theme().TextMuted)
			}
			ui.Text(c, tx.HarnessInstallHint()).FontSize(11).TextColor(c.Theme().TextMuted)
			ui.Divider(c)
			detail := ui.CollapsibleBase(c.Key("details"), v.openState("harness-"+driver.ID))
			detail.Trigger.Height(28).Gap(6).AlignItems(ui.Center).Label(tx.HarnessDetails()).Children(func() {
				ui.Icon(c, chevron).Size(12, 12).Rotate(90 * detail.Progress())
				ui.Text(c, tx.HarnessDetails()).FontSize(13)
			})
			detail.Panel(func() {
				ui.Column(c).Gap(8).Padding(8, 0).Children(func() {
					ui.Row(c).AlignItems(ui.Center).Gap(8).Children(func() {
						ui.Text(c, tx.CodexPath()).Width(90).FontSize(12)
						ui.TextInput(c, &v.C.State.Settings.CodexPath).Grow(1).Label(tx.CodexPath()).Disabled(v.C.AnyRunning() || u.Installing || u.Checking || v.C.Connection == "Connecting").OnChange(v.C.Save)
					})
					if u.Installation.Path != "" {
						ui.Text(c, u.Installation.Path).FontSize(11).TextColor(c.Theme().TextMuted).Selectable()
					}
					account := v.statusText(v.C.Account)
					if v.C.Account == "Configured provider (no OpenAI login required)" {
						account = tx.HarnessProviderAuth()
					}
					if account == "" {
						account = "—"
					}
					ui.Text(c, tx.Account()+" · "+account).FontSize(12).Selectable()
					ui.Row(c).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tx.CodexDefaultPermissions()).FontSize(12).Grow(1)
						settingsSelect(c.Key("default-permissions"), &v.C.State.Settings.DefaultPermissionProfile, tx.CodexDefaultPermissions(), []settingOption{{":read-only", tx.ReadOnly()}, {":workspace", tx.DefaultPermissions()}, {":approve-for-me", tx.ApproveForMe()}, {":danger-full-access", tx.FullAccess()}}, v.C.Save)
					})
				})
			})
		})
	}
}
