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

//go:embed assets/claude.svg
var claudeSVG []byte

var claudeLogo = ui.MustParseSVG(claudeSVG)
var piLogo = ui.MustParseSVG(piSVG)
var factoryLogo = ui.MustParseSVG(factorySVG)

func agentGlyph(c *ui.Context, id string) {
	if id == "claude" {
		ui.Image(c, claudeLogo).Size(18, 18).Shrink(0)
		return
	}
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
		v.C.LoadAgentModels("claude", false)
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
		ui.Box(c).Disabled(v.C.Running[t.ID]).Children(func() { v.modelTuningOptions(c, id, current, t) })
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
	options := v.agentPermissionOptions(t.Driver())
	if v.permissionTarget != t.ID {
		v.permissionPicker = false
	}
	current := options[0]
	permissionName := func(id string) string {
		for _, p := range options {
			if p.id == id {
				return p.name
			}
		}
		return options[0].name
	}
	for _, p := range options {
		if p.id == v.C.PermissionID() {
			current = p
		}
	}
	label := current.name
	if v.C.NextPermissionID() != v.C.PermissionID() {
		label += " → " + permissionName(v.C.NextPermissionID())
	}
	button := pickerOption(c.Key("agent-permission-selector"), tx.Permissions(), v.permissionPicker).Height(30).MaxWidth(170).Shrink(1).Tooltip(label)
	button.Children(func() {
		ui.Icon(c, permissionIcon(current.icon)).Size(15, 15).Shrink(0)
		ui.Text(c, label).FontSize(12).SingleLine().Shrink(1).MinWidth(0)
	})
	button.OnClick(func() { v.permissionPicker = !v.permissionPicker; v.modelPicker = false; v.permissionTarget = t.ID })
	pickerPopover(c.Key("agent-permission-picker"), button, &v.permissionPicker, tx.PermissionPicker(), 290, func() {
		ui.Column(c).Gap(4).Children(func() {
			ui.Text(c, tx.Permissions()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(6, 8)
			if v.C.NextPermissionID() != v.C.PermissionID() {
				ui.Text(c, tx.PermissionTiming()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(6, 8)
				ui.Text(c, tx.PermissionNextTurn(permissionName(v.C.NextPermissionID()))).FontSize(12).Padding(6, 8)
			}
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
						v.defaultPermissionHarness = ""
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
	catalogs := map[string][]codex.Model{"codex": v.C.Models, "pi": v.externalModels("pi"), "droid": v.externalModels("droid"), "claude": v.externalModels("claude")}
	var choices []modelChoice
	for _, key := range v.C.State.Settings.ModelFavorites {
		choices = append(choices, modelChoice{key.Harness, catalogModel(catalogs, key)})
	}
	t := v.C.State.Thread(v.C.State.Selected)
	selected := model.ModelFavorite{}
	if t != nil {
		selected = model.ModelFavorite{Harness: t.Driver(), ModelID: t.AgentModel}
	}
	if selected.Harness == "codex" && selected.ModelID == "" {
		selected.ModelID = v.C.State.Settings.Model
	}
	// Favorites span harnesses, so tune the model the active thread uses; its
	// catalog entry supplies the real effort and speed options.
	var side func(codex.Model)
	if active := catalogModel(catalogs, selected); t != nil && active.Model != "" {
		side = func(codex.Model) {
			box := ui.Box(c)
			if t.Driver() != "codex" && v.C.Running[t.ID] {
				box = box.Disabled(true)
			}
			box.Children(func() { v.modelTuningOptions(c, t.Driver(), active, t) })
		}
	}
	v.modelChoicesPanel(c, choices, selected, func(m modelChoice) {
		if m.Driver == "codex" {
			v.chooseModel(m.Model)
		} else {
			v.chooseExternalModel(m.Driver, m.Model)
		}
	}, side, true)
}

// catalogModel resolves a favorite or active model against loaded catalogs and
// keeps its ID when the catalog has not arrived yet.
func catalogModel(catalogs map[string][]codex.Model, key model.ModelFavorite) codex.Model {
	for _, m := range catalogs[key.Harness] {
		if m.Model == key.ModelID {
			return m
		}
	}
	return codex.Model{Model: key.ModelID}
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
	v.C.RememberModelSelection()
}

func thinkingModel(m codex.Model) harness.AgentModel {
	result := harness.AgentModel{ID: m.Model, DefaultEffort: m.DefaultReasoningEffort}
	for _, effort := range m.SupportedReasoningEfforts {
		result.Efforts = append(result.Efforts, effort.ReasoningEffort)
	}
	return result
}

type agentPermission struct{ id, name, icon, detail string }

func (v *View) agentPermissionOptions(driver string) []agentPermission {
	tx := v.texts()
	if driver == "claude" {
		return []agentPermission{{"", tx.DefaultPermissions(), ":workspace", tx.ClaudeDefaultHint()}, {"acceptEdits", tx.AgentFileEdits(), ":workspace", tx.ClaudeEditsHint()}, {"bypassPermissions", tx.FullAccess(), ":danger-full-access", tx.ClaudeFullAccessHint()}}
	}
	if driver == "droid" {
		return []agentPermission{{"", tx.ReadOnly(), ":read-only", tx.AgentReadOnlyHint()}, {"low", tx.AgentFileEdits(), ":workspace", tx.AgentFileEditsHint()}, {"medium", tx.AgentDevelopment(), ":workspace", tx.AgentDevelopmentHint()}, {"high", tx.FullAccess(), ":danger-full-access", tx.AgentFullAccessHint()}}
	}
	return []agentPermission{{"", tx.ReadOnly(), ":read-only", tx.AgentReadOnlyHint()}, {"native", tx.FullAccess(), ":danger-full-access", tx.AgentPiToolsHint()}}
}

func agentFullAccess(driver string) string {
	if driver == "claude" {
		return "bypassPermissions"
	}
	if driver == "droid" {
		return "high"
	}
	return "native"
}

func (v *View) agentDefaultAccess(driver string) *string {
	if driver == "claude" {
		return &v.C.State.Settings.ClaudeDefaultAccess
	}
	if driver == "droid" {
		return &v.C.State.Settings.DroidDefaultAccess
	}
	return &v.C.State.Settings.PiDefaultAccess
}

func (v *View) agentDefaultPermissions(c *ui.Context, driver string) {
	tx := v.texts()
	label := agentName(driver) + " " + tx.DefaultPermissions()
	if v.defaultPermissionChoices == nil {
		v.defaultPermissionChoices = map[string]*string{}
	}
	choice := v.defaultPermissionChoices[driver]
	if choice == nil {
		value := *v.agentDefaultAccess(driver)
		choice = &value
		v.defaultPermissionChoices[driver] = choice
	}
	var options []settingOption
	for _, p := range v.agentPermissionOptions(driver) {
		options = append(options, settingOption{p.id, p.name})
	}
	ui.Row(c).AlignItems(ui.Center).Gap(12).Children(func() {
		ui.Text(c, label).Width(112).Shrink(0).FontSize(12).TextColor(c.Theme().TextMuted)
		settingsSelect(c.Key("default-permissions"), choice, label, options, func() {
			if *choice == agentFullAccess(driver) && *v.agentDefaultAccess(driver) != agentFullAccess(driver) {
				*choice = *v.agentDefaultAccess(driver)
				v.defaultPermissionHarness = driver
				v.confirmFullAccess = true
				return
			}
			*v.agentDefaultAccess(driver) = *choice
			v.C.Save()
		})
	})
}

func (v *View) modelTuningOptions(c *ui.Context, driver string, current codex.Model, t *model.Thread) {
	tx := v.texts()
	ui.Text(c, tx.ReasoningEffort()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(6, 8).Margin(0, 0, 3)
	if driver == "codex" {
		v.effortSlider(c, current)
		v.speedOptions(c, current)
		return
	}
	if t == nil || current.Model == "" {
		ui.Text(c, tx.ChooseModel()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(8, 6)
		return
	}
	if len(current.SupportedReasoningEfforts) == 0 {
		ui.Text(c, tx.ThinkingUnavailable()).FontSize(12).TextColor(c.Theme().TextMuted).Padding(0, 8, 6)
		return
	}
	v.modelEffortSlider(c, current, &t.AgentEffort)
}
