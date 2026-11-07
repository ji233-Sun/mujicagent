package view

import "github.com/egoist/mygo/ui"

func (v *View) openHarnessInstall() {
	v.harnessDialog = true
	v.harnessSource = ""
	v.C.PrepareHarness(v.harnessSource)
}

func (v *View) harnessInstallDialog(c *ui.Context) {
	if !v.harnessDialog {
		return
	}
	tx, u := v.texts(), v.C.HarnessFor("codex")
	title, confirm := tx.HarnessUpdatePreview(), tx.HarnessConfirmUpdate()
	fresh := u.Missing
	if u.Plan != nil {
		fresh = u.Plan.Current.Path == ""
	}
	if fresh {
		title, confirm = tx.HarnessInstallPreview(), tx.HarnessConfirmInstall()
	}
	dialog := ui.DialogBase(c.Key("harness-install-dialog"), &v.harnessDialog, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, .2))
		panel.Width(640).MaxWidthPercent(94).MaxHeightPercent(88).Padding(20).Gap(14).
			Radius(14).Border(1, c.Theme().Border).Background(c.Theme().Background).
			Shadow(0, 16, 50, 0, ui.RGBA(0, 0, 0, .2))
		ui.Text(c, title).FontSize(17).Bold()
		if fresh {
			ui.Column(c).Gap(6).Children(func() {
				for _, option := range []settingOption{{"", tx.HarnessOfficial()}, {"cn", tx.HarnessCN()}, {"native-mirror", tx.HarnessNativeMirror()}} {
					button := ui.Button(c.Key(option.value), option.label).FillWidth().Height(32).Justify(ui.Start)
					if v.harnessSource == option.value {
						button.Border(1, c.Theme().Accent).Background(c.Theme().Surface)
					}
					button.OnClick(func() {
						v.harnessSource = option.value
						v.C.PrepareHarness(option.value)
					})
				}
			})
		}
		ui.Scroll(c.Key("install-preview")).MaxHeight(300).MinHeight(44).Gap(8).Children(func() {
			if u.Preparing {
				ui.Text(c, tx.HarnessPreparing()).FontSize(12).TextColor(c.Theme().TextMuted)
			} else if p := u.Plan; p != nil {
				version := p.Release.Version
				if p.Current.Version != "" {
					version = p.Current.Version + " → " + version
				}
				ui.Text(c, "Codex · "+version).FontSize(12).Bold()
				if command := p.CommandPreview(); command != "" {
					ui.Text(c, tx.HarnessCommandPreview()).FontSize(11).TextColor(c.Theme().TextMuted)
					ui.Text(c, command).Font("monospace").FontSize(12).FillWidth().Selectable().Padding(10).Radius(8).Background(c.Theme().Surface)
				} else {
					ui.Text(c, tx.HarnessNativeAction()).FontSize(12)
					ui.Text(c, p.Release.URL).Font("monospace").FontSize(11).FillWidth().Selectable()
					ui.Text(c, p.Release.Digest).Font("monospace").FontSize(11).FillWidth().Selectable().TextColor(c.Theme().TextMuted)
				}
				ui.Text(c, tx.HarnessDestination()).FontSize(11).TextColor(c.Theme().TextMuted)
				ui.Text(c, p.Path).Font("monospace").FontSize(11).FillWidth().Selectable()
			}
			if u.Error != "" {
				ui.Text(c, u.Error).FontSize(12).FillWidth().TextColor(c.Theme().Danger).Selectable()
				ui.Button(c, tx.Retry()).OnClick(func() { v.C.PrepareHarness(v.harnessSource) })
			}
		})
		ui.Row(c).Gap(8).Justify(ui.End).Children(func() {
			ui.Button(c, tx.Cancel()).Height(32).OnClick(func() {
				v.harnessDialog = false
				v.C.DismissHarnessPlan()
			})
			ui.PrimaryButton(c, confirm).Height(32).Disabled(u.Preparing || u.Plan == nil || u.Installing || v.C.AnyRunning() || v.C.Connection == "Connecting" || u.Binary != v.C.State.Settings.CodexPath).OnClick(func() {
				v.C.InstallHarness()
				if u.Installing {
					v.harnessDialog = false
				}
			})
		})
	})
	if dialog.Dismissed() {
		v.C.DismissHarnessPlan()
	}
}
