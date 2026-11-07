package view

import (
	_ "embed"
	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"slices"
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
		b.OnClick(func() {
			if v.agentSection != d.ID {
				v.dismissHarnessInstall()
			}
			v.agentSection = d.ID
		})
	}
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
		ui.Box(c).Disabled(v.C.Running[t.ID]).Children(func() { v.agentThinkingOptions(c, current, t) })
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
	effort := t.AgentEffort
	if t.Driver() != id {
		effort = ""
		if !v.C.Handoff(id, m.Model) {
			return
		}
		t = v.C.State.Thread(v.C.State.Selected)
	} else {
		t.AgentModel = m.Model
	}
	t.AgentEffort = thinkingModel(m).ResolveEffort(effort)
	v.C.Save()
}

func thinkingModel(m codex.Model) harness.AgentModel {
	result := harness.AgentModel{ID: m.Model, DefaultEffort: m.DefaultReasoningEffort}
	for _, effort := range m.SupportedReasoningEfforts {
		result.Efforts = append(result.Efforts, effort.ReasoningEffort)
	}
	return result
}

func thinkingOff(value string) bool { return value == "off" || value == "none" }

func (v *View) agentThinkingOptions(c *ui.Context, current codex.Model, t *model.Thread) {
	tx := v.texts()
	ui.Row(c).Gap(5).AlignItems(ui.Center).Padding(6, 6).Children(func() {
		ui.Icon(c, thinkingIcon).Size(13, 13).TextColor(c.Theme().TextMuted)
		ui.Text(c, tx.ReasoningEffort()).FontSize(11).TextColor(c.Theme().TextMuted)
	})
	if current.Model == "" {
		ui.Text(c, tx.ChooseModel()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(8, 6)
		return
	}
	m := thinkingModel(current)
	value := m.ResolveEffort(t.AgentEffort)
	choose := func(level string) {
		if !v.C.Running[t.ID] && slices.Contains(m.Efforts, level) {
			t.AgentEffort = level
			v.C.Save()
		}
	}
	label := func(level string) string {
		if thinkingOff(level) && !(slices.Contains(m.Efforts, "off") && slices.Contains(m.Efforts, "none")) {
			return tx.ThinkingOff()
		}
		return level
	}
	ui.Column(c.Key(current.Model)).Margin(0, 2).Padding(8).Gap(7).Radius(9).
		Background(c.Theme().Surface).Label(tx.AdjustReasoningEffort()).Children(func() {
		enabledLevels := 0
		for _, effort := range m.Efforts {
			if !thinkingOff(effort) {
				enabledLevels++
			}
		}
		if enabledLevels == 0 {
			ui.Text(c, tx.ThinkingUnavailable()).FontSize(12).TextColor(c.Theme().TextMuted)
			return
		}
		if len(m.Efforts) == 1 {
			ui.Text(c, label(m.Efforts[0])).FontSize(14).Bold().TextColor(c.Theme().Accent)
			ui.Text(c, tx.ThinkingFixed()).FontSize(10).TextColor(c.Theme().TextMuted)
			return
		}
		if len(m.Efforts) == 2 && enabledLevels == 1 {
			off, on := m.Efforts[0], m.Efforts[1]
			if !thinkingOff(off) {
				off, on = on, off
			}
			enabled := value == on
			state := tx.ThinkingOff()
			if enabled {
				state = tx.ThinkingOn()
			}
			if value == "" {
				state = tx.AgentDefaultEffort()
			}
			ui.Text(c, state).FontSize(13).Bold()
			if value == "" {
				// An unreported CLI default is not an off switch. Let the user
				// explicitly choose either state without implying an active value.
				ui.Row(c).Gap(4).Label(tx.ThinkingToggle()).Children(func() {
					ui.Button(c, tx.ThinkingOff()).Grow(1).Height(25).Padding(0, 5).FontSize(11).OnClick(func() { choose(off) })
					ui.Button(c, tx.ThinkingOn()).Grow(1).Height(25).Padding(0, 5).FontSize(11).OnClick(func() { choose(on) })
				})
				ui.Text(c, tx.ThinkingCLIDefault()).FontSize(10).TextColor(c.Theme().TextMuted)
				return
			}
			ui.Switch(c, &enabled).Label(tx.ThinkingToggle()).OnClick(func() {
				if enabled {
					choose(on)
				} else {
					choose(off)
				}
			})
			return
		}
		if value == "" {
			ui.Text(c, tx.AgentDefaultEffort()).FontSize(11).TextColor(c.Theme().TextMuted)
		}
		ui.Scroll(c.Key("thinking-levels")).MaxHeight(236).Gap(4).Children(func() {
			for _, effort := range m.Efforts {
				selected := effort == value
				b := ui.ButtonBase(c.Key(effort)).Height(27).FillWidth().Radius(6).Padding(0, 7).Gap(4).AlignItems(ui.Center).Label(label(effort))
				if selected {
					b.Background(c.Theme().Background).TextColor(c.Theme().Accent)
				}
				b.Children(func() {
					ui.Text(c, label(effort)).FontSize(12).Grow(1).SingleLine()
					if selected {
						ui.Icon(c, checkIcon).Size(12, 12)
					}
				})
				b.OnClick(func() { choose(effort) })
			}
		})
	})
}
