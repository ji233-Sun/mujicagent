package view

import (
	_ "embed"
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
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

//go:embed assets/pi.svg
var piSVG []byte

//go:embed assets/factory.svg
var factorySVG []byte
var piLogo = ui.MustParseSVG(piSVG)
var factoryLogo = ui.MustParseSVG(factorySVG)

func agentGlyph(c *ui.Context, id string) {
	if id == "pi" {
		ui.Image(c, piLogo).Size(18, 18).Shrink(0)
		return
	}
	icon := openAIIcon
	if id == "droid" {
		icon = factoryLogo
	}
	ui.Icon(c, icon).Size(18, 18).Shrink(0)
}
func (v *View) agentSidebar(c *ui.Context) {
	for _, d := range harness.Drivers {
		b := ui.ButtonBase(c.Key(d.ID)).Height(30).Radius(6).Padding(0, 8).Gap(6).AlignItems(ui.Center).Justify(ui.Start).Label(d.Name)
		if v.agentSection == d.ID || v.agentSection == "" && d.ID == "codex" {
			b.Background(c.Theme().Surface).TextColor(c.Theme().Accent)
		}
		b.Children(func() { agentGlyph(c, d.ID); ui.Text(c, d.Name).FontSize(12).Grow(1).SingleLine() })
		b.OnClick(func() { v.agentSection = d.ID })
	}
}
func (v *View) externalAgentPane(c *ui.Context, id string) {
	tx, h := v.texts(), v.C.HarnessFor(id)
	path := &v.C.State.Settings.PiPath
	url := "https://pi.dev/"
	if id == "droid" {
		path = &v.C.State.Settings.DroidPath
		url = "https://docs.factory.com/droid-cli/quickstart"
	}
	services := c.Services()
	ui.Column(c.Key(id)).Radius(10).Border(1, c.Theme().Border).Background(c.Theme().Background).Children(func() {
		ui.Row(c).Padding(14).Gap(10).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(34, 34).Radius(8).Background(c.Theme().Surface).Center().Children(func() { agentGlyph(c, id) })
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, agentName(id)).Bold().FontSize(15)
				version := h.Installation.Version
				if version == "" {
					version = tx.HarnessUnchecked()
				}
				ui.Text(c, version).FontSize(11).TextColor(c.Theme().TextMuted).SingleLine()
			})
			ui.Button(c, tx.AgentProbe()).Height(28).FontSize(12).Disabled(h.Checking).OnClick(func() { v.C.CheckAgent(id) })
			ui.Button(c, tx.AgentSetup()).Height(28).FontSize(12).OnClick(func() { services.OpenURL(url) })
		})
		if h.Checking {
			ui.Progress(c, -1).Height(3)
		}
		if h.Error != "" {
			ui.Text(c, h.Error).FontSize(11).TextColor(c.Theme().Danger).Padding(0, 14, 10)
		}
		ui.Divider(c)
		ui.Column(c).Padding(0, 14).Children(func() {
			detail := ui.CollapsibleBase(c.Key("details"), v.openState("harness-"+id))
			detail.Trigger.Height(36).Gap(6).AlignItems(ui.Center).Label(tx.HarnessDetails()).Children(func() {
				ui.Icon(c, chevron).Size(11, 11).Rotate(90 * detail.Progress()).TextColor(c.Theme().TextMuted)
				ui.Text(c, tx.HarnessDetails()).FontSize(12).Bold()
			})
			detail.Panel(func() {
				ui.Row(c).Padding(0, 0, 12).AlignItems(ui.Center).Gap(12).Children(func() {
					ui.Text(c, tx.AgentPath()).Width(112).Shrink(0).FontSize(12).TextColor(c.Theme().TextMuted)
					ui.TextInput(c, path).Height(28).Radius(6).FontSize(12).Grow(1).MinWidth(0).Placeholder(id).Label(tx.AgentPath()).Disabled(h.Checking).OnChange(func() { v.C.Save(); h.ModelsChecked = false })
				})
			})
		})
	})
}
func (v *View) harnessRail(c *ui.Context) {
	favorite := ui.ButtonBase(c.Key("favorites")).Size(32, 32).Radius(7).Center().Label(v.texts().FavoriteModels()).Tooltip(v.texts().FavoriteModels())
	if v.pickerHarness == "favorites" {
		favorite.Background(c.Theme().Surface).TextColor(c.Theme().Accent)
	}
	favorite.Children(func() { ui.Icon(c, starIcon).Size(18, 18) })
	favorite.OnClick(func() {
		v.pickerHarness = "favorites"
		v.modelSearch = ""
		v.C.LoadAgentModels("pi", false)
		v.C.LoadAgentModels("droid", false)
	})
	ui.Divider(c).Margin(2, 4)

	for _, d := range harness.Drivers {
		b := ui.ButtonBase(c.Key(d.ID)).Size(32, 32).Radius(7).Center().Label(d.Name).Tooltip(d.Name)
		if v.pickerHarness == d.ID || v.pickerHarness == "" && d.ID == "codex" {
			b.Background(c.Theme().Surface).TextColor(c.Theme().Accent)
		}
		b.Children(func() { agentGlyph(c, d.ID) })
		b.OnClick(func() {
			v.pickerHarness = d.ID
			v.C.LoadAgentModels(d.ID, false)
			v.modelSearch = ""
		})
	}
}
func (v *View) agentModelPanel(c *ui.Context) {
	id := v.pickerHarness
	tx, h := v.texts(), v.C.HarnessFor(id)
	t := v.C.State.Thread(v.C.State.Selected)
	selected := ""
	if t != nil && t.Driver() == id {
		selected = t.AgentModel
	}
	v.modelCatalogPanel(c, id, v.externalModels(id), selected, func(m codex.Model) { v.chooseExternalModel(id, m) }, func(current codex.Model) {
		if t == nil || t.Driver() != id {
			return
		}
		ui.Text(c, tx.ReasoningEffort()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(6, 8).Margin(0, 0, 3)
		ui.Box(c).Disabled(v.C.Running[t.ID]).Children(func() { v.modelEffortSlider(c, current, &t.AgentEffort) })
	})
	ui.Row(c).Height(28).Shrink(0).Padding(0, 8).Gap(6).AlignItems(ui.Center).Children(func() {
		ui.Box(c).Grow(1)
		label := tx.AgentRefreshModels()
		if h.ModelsLoading {
			label = tx.AgentLoadingModels()
		}
		ui.Button(c, label).Height(24).FontSize(11).Disabled(h.ModelsLoading).OnClick(func() { v.C.LoadAgentModels(id, true) })
		if h.ModelsError != "" {
			ui.Text(c, tx.AgentModelsFailed()).FontSize(11).TextColor(c.Theme().Danger).Tooltip(h.ModelsError)
		}
	})
}
func (v *View) agentPermissions(c *ui.Context, t *model.Thread) {
	tx := v.texts()
	type profile struct{ id, name, icon, detail string }
	options := []profile{{"", tx.ReadOnly(), ":read-only", tx.AgentReadOnlyHint()}, {"native", tx.FullAccess(), ":danger-full-access", tx.AgentPiToolsHint()}}
	if t.Driver() == "droid" {
		options = []profile{{"", tx.ReadOnly(), ":read-only", tx.AgentReadOnlyHint()}, {"low", tx.AgentFileEdits(), ":workspace", tx.AgentFileEditsHint()}, {"medium", tx.AgentDevelopment(), ":workspace", tx.AgentDevelopmentHint()}, {"high", tx.FullAccess(), ":danger-full-access", tx.AgentFullAccessHint()}}
	}
	if v.permissionTarget != t.ID {
		v.permissionPicker = false
	}
	current := options[0]
	for _, p := range options {
		if p.id == t.AgentAccess {
			current = p
		}
	}
	button := pickerOption(c.Key("agent-permission-selector"), tx.Permissions(), v.permissionPicker).Height(30).MaxWidth(170).Shrink(1).Tooltip(current.detail)
	button.Children(func() {
		ui.Icon(c, permissionIcon(current.icon)).Size(15, 15).Shrink(0)
		name := current.name
		if v.C.Running[t.ID] {
			name = tx.PermissionNextTurn(name)
		}
		ui.Text(c, name).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
	})
	button.OnClick(func() { v.permissionPicker = !v.permissionPicker; v.modelPicker = false; v.permissionTarget = t.ID })
	pickerPopover(c.Key("agent-permission-picker"), button, &v.permissionPicker, tx.PermissionPicker(), 290, func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, tx.Permissions()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(6, 8)
			for _, p := range options {
				selected := p.id == t.AgentAccess
				b := pickerOption(c.Key(p.id), p.name, selected).Tooltip(p.detail)
				b.Children(func() {
					ui.Icon(c, permissionIcon(p.icon)).Size(17, 17).Shrink(0)
					ui.Text(c, p.name).FontSize(13).Grow(1).SingleLine()
					if selected {
						ui.Icon(c, checkIcon).Size(14, 14)
					}
				})
				b.OnClick(func() {
					v.permissionPicker = false
					if p.icon == ":danger-full-access" && !selected {
						v.confirmFullAccess = true
						return
					}
					t.AgentAccess = p.id
					v.C.Save()
				})
			}
		})
	})
}

func (v *View) externalModels(id string) []codex.Model {
	models := []codex.Model{}
	for _, m := range v.C.HarnessFor(id).Models {
		option := codex.Model{Model: m.ID, DisplayName: m.Name, DefaultReasoningEffort: m.DefaultEffort}
		for _, effort := range m.Efforts {
			option.SupportedReasoningEfforts = append(option.SupportedReasoningEfforts, struct {
				ReasoningEffort string `json:"reasoningEffort"`
			}{ReasoningEffort: effort})
		}
		models = append(models, option)
	}

	return models
}

func (v *View) favoriteModelPanel(c *ui.Context) {
	catalogs := map[string][]codex.Model{"codex": v.C.Models, "pi": v.externalModels("pi"), "droid": v.externalModels("droid")}
	var choices []modelChoice
	for _, key := range v.C.State.Settings.ModelFavorites {
		option := codex.Model{Model: key.ModelID}
		for _, m := range catalogs[key.Harness] {
			if m.Model == key.ModelID {
				option = m
				break
			}
		}
		choices = append(choices, modelChoice{key.Harness, option})
	}
	selected := model.ModelFavorite{}
	if t := v.C.State.Thread(v.C.State.Selected); t != nil {
		selected = model.ModelFavorite{Harness: t.Driver(), ModelID: t.AgentModel}
	}
	if selected.Harness == "codex" && selected.ModelID == "" {
		selected.ModelID = v.C.State.Settings.Model
	}
	v.modelChoicesPanel(c, choices, selected, func(m modelChoice) {
		if m.Driver == "codex" {
			v.chooseModel(m.Model)
		} else {
			v.chooseExternalModel(m.Driver, m.Model)
		}
	}, nil, true)
}
func (v *View) chooseExternalModel(id string, m codex.Model) {
	t := v.C.State.Thread(v.C.State.Selected)
	if t == nil || v.C.Running[t.ID] {
		return
	}
	if t.Driver() != id {
		if !v.C.Handoff(id, m.Model) {
			return
		}
		t = v.C.State.Thread(v.C.State.Selected)
	} else {
		t.AgentModel = m.Model
	}
	t.AgentEffort = m.DefaultReasoningEffort
	v.C.Save()
}
