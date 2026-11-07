package view

import (
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func agentName(id string) string {
	for _, d := range harness.Drivers {
		if d.ID == id {
			return d.Name
		}
	}
	return "Codex"
}
func agentGlyph(c *ui.Context, id string) {
	icon := openAIIcon
	if id == "pi" {
		icon = spark
	}
	if id == "droid" {
		icon = agentIcon
	}
	ui.Icon(c, icon).Size(18, 18).Shrink(0)
}
func (v *View) agentSidebar(c *ui.Context) {
	for _, d := range harness.Drivers {
		b := ui.ButtonBase(c.Key(d.ID)).Height(30).Radius(6).Padding(0, 8).Gap(6).AlignItems(ui.Center).Label(d.Name)
		if v.agentSection == d.ID || v.agentSection == "" && d.ID == "codex" {
			b.Background(c.Theme().Surface).TextColor(c.Theme().Accent)
		}
		b.Children(func() { agentGlyph(c, d.ID); ui.Text(c, d.Name).FontSize(12) })
		b.OnClick(func() { v.agentSection = d.ID })
	}
}
func (v *View) externalAgentPane(c *ui.Context, id string) {
	tx := v.texts()
	h := v.C.HarnessFor(id)
	s := &v.C.State.Settings
	path, modelID := &s.PiPath, &s.PiModel
	url := "https://pi.dev/"
	if id == "droid" {
		path, modelID = &s.DroidPath, &s.DroidModel
		url = "https://docs.factory.com/droid-exec/overview"
	}
	services := c.Services()
	ui.Column(c.Key(id)).Gap(10).Children(func() {
		ui.Row(c).Height(32).Gap(8).AlignItems(ui.Center).Children(func() {
			agentGlyph(c, id)
			ui.Text(c, agentName(id)).Bold().FontSize(15).Grow(1)
			ui.Button(c, tx.AgentProbe()).Height(26).FontSize(12).Disabled(h.Checking).OnClick(func() { v.C.CheckAgent(id) })
			ui.Button(c, tx.AgentSetup()).Height(26).FontSize(12).OnClick(func() { services.OpenURL(url) })
		})
		if h.Installation.Version != "" {
			ui.Text(c, h.Installation.Version).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
		}
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tx.AgentPath()).Width(94).Shrink(0).FontSize(12)
			ui.TextInput(c, path).Height(28).Grow(1).MinWidth(0).FontSize(12).Placeholder(id).Label(tx.AgentPath()).Disabled(h.Checking).OnChange(v.C.Save)
		})
		ui.Row(c).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tx.AgentModel()).Width(94).Shrink(0).FontSize(12)
			ui.TextInput(c, modelID).Height(28).Grow(1).MinWidth(0).FontSize(12).Label(tx.AgentModel()).OnChange(v.C.Save)
		})
		ui.Text(c, tx.AgentModelHint()).FontSize(11).TextColor(c.Theme().TextMuted)
		ui.Divider(c)
		ui.Text(c, tx.AgentAuthHint()).FontSize(12).TextColor(c.Theme().TextMuted)
		ui.Text(c, tx.AgentExternalInstall()).FontSize(12).TextColor(c.Theme().TextMuted)
		if h.Checking {
			ui.Progress(c, -1).Height(3)
		}
		if h.Error != "" {
			ui.Text(c, h.Error).FontSize(12).TextColor(c.Theme().Danger).Selectable()
		}
	})
}
func (v *View) harnessRail(c *ui.Context) {
	for _, d := range harness.Drivers {
		b := ui.ButtonBase(c.Key(d.ID)).Size(32, 32).Radius(7).Center().Label(d.Name).Tooltip(d.Name)
		if v.pickerHarness == d.ID || v.pickerHarness == "" && d.ID == "codex" {
			b.Background(c.Theme().Surface).TextColor(c.Theme().Accent)
		}
		b.Children(func() { agentGlyph(c, d.ID) })
		b.OnClick(func() {
			v.pickerHarness = d.ID
			v.modelSearch = ""
			v.agentModelDraft = v.C.State.Settings.PiModel
			if d.ID == "droid" {
				v.agentModelDraft = v.C.State.Settings.DroidModel
			}
			if t := v.C.State.Thread(v.C.State.Selected); t != nil && t.Driver() == d.ID {
				v.agentModelDraft = t.AgentModel
			}
		})
	}
}
func (v *View) agentModelPanel(c *ui.Context) {
	tx := v.texts()
	t := v.C.State.Thread(v.C.State.Selected)
	ui.Column(c).Padding(6).Gap(8).Children(func() {
		ui.Text(c, agentName(v.pickerHarness)).Bold().FontSize(13)
		ui.TextInput(c.Key(v.pickerHarness), &v.agentModelDraft).Height(28).FontSize(12).Label(tx.AgentModel()).Placeholder(tx.AgentModel())
		ui.Text(c, tx.AgentModelHint()).FontSize(11).TextColor(c.Theme().TextMuted)
		label := tx.AgentSwitch()
		if t != nil && len(t.Turns) > 0 {
			label = tx.AgentHandoff()
			ui.Text(c, tx.AgentHandoffHint()).FontSize(11).TextColor(c.Theme().TextMuted)
		}
		if t != nil && v.C.Running[t.ID] {
			ui.Text(c, tx.AgentBusyHint()).FontSize(11).TextColor(c.Theme().TextMuted)
		}
		ui.PrimaryButton(c, label).Height(28).FontSize(12).Disabled(t == nil || v.C.Running[t.ID]).OnClick(func() {
			if v.C.Handoff(v.pickerHarness, v.agentModelDraft) {
				v.modelPicker = false
			}
		})
	})
}
func (v *View) agentPermissions(c *ui.Context, t *model.Thread) {
	tx := v.texts()
	options := []settingOption{{"", tx.AgentNoTools()}, {"native", tx.AgentNativeTools()}}
	label := tx.AgentPiToolsHint()
	if t.Driver() == "droid" {
		options = []settingOption{{"", tx.ReadOnly()}, {"low", "Auto · low"}, {"medium", "Auto · medium"}, {"high", "Auto · high"}}
		label = tx.AgentDroidAccess()
	}
	ui.Box(c).Disabled(v.C.Running[t.ID]).Tooltip(label).Children(func() { settingsSelect(c.Key("agent-permissions"), &t.AgentAccess, label, options, v.C.Save) })
}
