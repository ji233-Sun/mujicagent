package view

import (
	_ "embed"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
)

//go:embed assets/openai.svg
var openAISVG []byte

var openAIIcon = ui.MustParseSVG(openAISVG)

func (v *View) modelSelector(c *ui.Context) {
	tx := v.texts()
	settings := &v.C.State.Settings
	t := v.C.State.Thread(v.C.State.Selected)
	label := settings.Model
	if t != nil && t.AgentModel != "" {
		label = t.AgentModel
	}
	if t.Driver() != "codex" {
		label = agentName(t.Driver())
		if t.AgentModel != "" {
			label += " · " + t.AgentModel
		}
	}
	if label == "" {
		label = tx.ChooseModel()
	} else if t.Driver() == "codex" && settings.Effort != "" {
		label += " · " + settings.Effort
	}
	for _, model := range v.C.Models {
		if t.Driver() != "codex" || model.Model != settings.Model || !model.SupportsSpeed(settings.ServiceTier) {
			continue
		}
		for _, tier := range model.ServiceTiers {
			if tier.ID == settings.ServiceTier && tier.Name != "" {
				label += " · " + tier.Name
				break
			}
		}
		break
	}
	button := ui.ButtonBase(c.Key("model-selector")).Height(30).MaxWidth(380).Shrink(1).
		Padding(0, 8).Gap(7).Radius(8).AlignItems(ui.Center).Label(tx.ModelAndEffort()).
		Tooltip(label).Transition(hoverTransition)
	if button.Hovered() || v.modelPicker {
		button.Background(c.Theme().SurfaceHover).TextColor(c.Theme().Accent)
	}
	button.Children(func() {
		agentGlyph(c, t.Driver())
		ui.Text(c, label).FontSize(12).SingleLine().MinWidth(0).Shrink(1)
		angle := float32(90)
		if v.modelPicker {
			angle = -90
		}
		icon := ui.Icon(c, chevron).Size(14, 14).Shrink(0)
		icon.Rotate(icon.Animate("rotation", angle, 160*time.Millisecond))
	})
	button.OnClick(func() {
		v.modelPicker = !v.modelPicker
		v.permissionPicker = false
		v.modelSearch = ""
		v.pickerHarness = t.Driver()
		v.C.LoadAgentModels(v.pickerHarness, false)
		v.agentModelDraft = ""
		if t != nil {
			v.agentModelDraft = t.AgentModel
		}
	})
	pickerPopover(c.Key("model-picker"), button, &v.modelPicker, tx.ModelPicker(), 492, func() { v.modelPanel(c) })
}

func pickerPopover(c *ui.Context, anchor ui.Element, open *bool, label string, width float32, content func()) {
	// Keep the overlay parent alive while MyGo draws the inert exit snapshot.
	ui.Overlay(c, func() {})
	ui.PopoverBase(c, anchor, open, func(panel ui.Element) {
		panel.Width(width).Padding(8).Margin(8, 0, 0, 0).Radius(14).
			Background(c.Theme().Background).Border(1, c.Theme().Border).
			Shadow(0, 8, 28, 0, ui.RGBA(0, 0, 0, 0.16)).Label(label)
		panel.Transition(ui.ElementTransition{Duration: 160 * time.Millisecond, Colors: true, Exit: &ui.Motion{Y: 8}})
		// A visual transform survives PopoverBase reattaching to its anchor.
		shown := panel.Keyframes("open", []ui.Keyframe{
			{Value: 0},
			{At: 220 * time.Millisecond, Value: 1, Ease: ui.EaseOut},
		})
		panel.Opacity(shown).Translate(0, 10*(1-shown))
		content()
	})
}

func (v *View) modelPanel(c *ui.Context) {
	ui.Row(c).Gap(8).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(32).Shrink(0).Gap(4).Children(func() { v.harnessRail(c) })
		ui.Box(c).Width(1).Background(c.Theme().Border)
		ui.Column(c).Grow(1).MinWidth(0).Children(func() {
			if v.pickerHarness == "favorites" {
				v.favoriteModelPanel(c)
				return
			}
			if v.pickerHarness == "pi" || v.pickerHarness == "droid" {
				v.agentModelPanel(c)
				return
			}
			v.codexModelPanel(c)
		})
	})
}
func (v *View) codexModelPanel(c *ui.Context) {
	selectedModel := v.C.State.Settings.Model
	if t := v.C.State.Thread(v.C.State.Selected); t != nil && t.AgentModel != "" && t.Driver() == "codex" {
		selectedModel = t.AgentModel
	}
	v.modelCatalogPanel(c, "codex", v.C.Models, selectedModel, v.chooseModel, func(current codex.Model) {
		ui.Text(c, v.texts().ReasoningEffort()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(6, 8).Margin(0, 0, 3)
		v.effortSlider(c, current)
		v.speedOptions(c, current)
	})
}

type modelChoice struct {
	Driver string
	Model  codex.Model
}

func (m modelChoice) key() model.ModelFavorite {
	return model.ModelFavorite{Harness: m.Driver, ModelID: m.Model.Model}
}
func (v *View) modelCatalogPanel(c *ui.Context, driver string, catalog []codex.Model, selectedModel string, choose func(codex.Model), side func(codex.Model)) {
	choices := make([]modelChoice, 0, len(catalog))
	for _, m := range catalog {
		choices = append(choices, modelChoice{driver, m})
	}
	v.modelChoicesPanel(c, choices, model.ModelFavorite{Harness: driver, ModelID: selectedModel}, func(m modelChoice) { choose(m.Model) }, side, false)
}
func (v *View) modelChoicesPanel(c *ui.Context, catalog []modelChoice, selectedModel model.ModelFavorite, choose func(modelChoice), side func(codex.Model), showHarness bool) {
	tx := v.texts()
	models := make([]modelChoice, 0, len(catalog))
	var current codex.Model
	for _, m := range catalog {
		if m.key() == selectedModel {
			current = m.Model
		}
		if strings.Contains(strings.ToLower(m.Model.Model+" "+m.Model.DisplayName+" "+m.Driver), strings.ToLower(v.modelSearch)) {
			models = append(models, m)
		}
	}
	sort.SliceStable(models, func(i, j int) bool {
		return v.C.State.IsModelFavorite(models[i].key()) && !v.C.State.IsModelFavorite(models[j].key())
	})
	ui.Row(c).Gap(8).AlignItems(ui.Stretch).Children(func() {
		list := ui.Column(c).Gap(5)
		if side != nil {
			list.Width(274)
		} else {
			list.Grow(1).MinWidth(0)
		}
		list.Children(func() {
			ui.Row(c).Height(28).Padding(0, 8).Gap(14).AlignItems(ui.Center).Children(func() {
				ui.Text(c, tx.Models()).FontSize(11).TextColor(c.Theme().TextMuted).Grow(1)
				search := ui.Row(c.Key("model-search")).Width(156).Height(26).Gap(6).AlignItems(ui.Center)
				active := search.Hovered() || search.FocusWithin()
				search.Children(func() {
					color := c.Theme().TextMuted
					if active {
						color = c.Theme().Accent
					}
					ui.Icon(c, searchIcon).Size(13, 13).Shrink(0).TextColor(color).Transition(hoverTransition)
					ui.TextInputBase(c, &v.modelSearch).Height(26).Grow(1).MinWidth(0).FontSize(11).
						Placeholder(tx.SearchModels()).Label(tx.SearchModels())
				})
			})
			listHeight := float32(min(5, max(1, len(catalog))) * 36)
			ui.List(c.Key("models"), &v.modelList, len(models)).Height(listHeight).Label(tx.ModelList()).
				ItemKey(func(i int) any { return models[i].key() }).Rows(func(row ui.ListRow) {
				m := models[row.Index]
				c := row.Context
				selected := m.key() == selectedModel
				label := m.Model.DisplayName
				if label == "" {
					label = m.Model.Model
				}
				ui.Row(c).Height(36).Gap(2).AlignItems(ui.Center).Children(func() {
					b := pickerOption(c, m.Model.Model, selected).Grow(1).MinWidth(0).Tooltip(m.Driver + " · " + m.Model.Model)
					if showHarness {
						b.Label(agentName(m.Driver) + " · " + m.Model.Model)
					}
					b.Children(func() {
						if showHarness {
							agentGlyph(c, m.Driver)
						}
						ui.Text(c, label).FontSize(12).SingleLine().Grow(1).MinWidth(0)
						if selected {
							ui.Icon(c, checkIcon).Size(14, 14).TextColor(c.Theme().Accent)
						}
					})
					b.Disabled(v.C.Running[v.C.State.Selected]).OnClick(func() { choose(m) })
					if m.Model.Model != "" {
						favorite := v.C.State.IsModelFavorite(m.key())
						hint := tx.FavoriteModel()
						if favorite {
							hint = tx.UnfavoriteModel()
						}
						star := iconButton(c, hint+" "+agentName(m.Driver)+" · "+m.Model.Model, starIcon, func() { v.C.State.ToggleModelFavorite(m.key()); v.C.Save() }).Size(24, 24)
						if favorite {
							star.TextColor(c.Theme().Accent).Background(c.Theme().Surface)
						} else {
							star.TextColor(c.Theme().TextMuted)
						}
					}
				})
			}).Children(func() {
				if len(models) == 0 {
					ui.Text(c, tx.NoModels()).FontSize(12).TextColor(c.Theme().TextMuted).Padding(6, 8)
				}
			})
		})
		if side != nil {
			ui.Box(c).Width(1).Background(c.Theme().Border).Margin(5, 0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(2).Children(func() { side(current) })
		}
	})
}

func (v *View) effortSlider(c *ui.Context, m codex.Model) {
	v.modelEffortSlider(c, m, &v.C.State.Settings.Effort)
}
func (v *View) modelEffortSlider(c *ui.Context, m codex.Model, value *string) {
	efforts := m.SupportedReasoningEfforts
	if len(efforts) == 0 {
		return
	}
	index := float64(0)
	for i, effort := range efforts {
		if effort.ReasoningEffort == *value || *value == "" && effort.ReasoningEffort == m.DefaultReasoningEffort {
			index = float64(i)
		}
	}
	ui.Column(c.Key(m.Model)).Padding(0, 8, 6).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, thinkingIcon).Size(14, 14).TextColor(c.Theme().TextMuted)
			label := efforts[int(index)].ReasoningEffort
			if *value == "" && m.DefaultReasoningEffort == "" {
				label = v.texts().AgentDefaultEffort()
			}
			ui.Text(c, label).FontSize(13).Grow(1)
		})
		if len(efforts) == 1 {
			return
		}
		ui.StepSlider(c, &index, 0, float64(len(efforts)-1), 1).
			Label(v.texts().AdjustReasoningEffort()).Tooltip(efforts[int(index)].ReasoningEffort).
			OnChange(func() {
				*value = efforts[int(index)].ReasoningEffort
				v.C.Save()
			})
		ui.Row(c).Justify(ui.SpaceBetween).TextColor(c.Theme().TextMuted).Children(func() {
			ui.Text(c, efforts[0].ReasoningEffort).FontSize(10)
			ui.Text(c, efforts[len(efforts)-1].ReasoningEffort).FontSize(10)
		})
	})
}

func (v *View) speedOptions(c *ui.Context, m codex.Model) {
	var tiers []codex.ServiceTier
	for _, tier := range m.ServiceTiers {
		if m.SupportsSpeed(tier.ID) {
			tiers = append(tiers, tier)
		}
	}
	if len(tiers) == 0 {
		return
	}
	ui.Divider(c).Margin(10, 6, 4)
	ui.Text(c, v.texts().Speed()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(3, 8)
	ui.Column(c).Gap(3).Padding(0, 0, 3).Children(func() {
		for _, tier := range tiers {
			selected := v.C.State.Settings.ServiceTier == tier.ID
			b := pickerOption(c.Key(tier.ID), tier.Name, selected).Tooltip(tier.Description)
			b.Children(func() {
				icon := fastIcon
				if tier.ID == "ultrafast" {
					icon = ultrafastIcon
				}
				ui.Icon(c, icon).Size(16, 16).Shrink(0)
				ui.Text(c, tier.Name).FontSize(12).Grow(1)
				if selected {
					ui.Icon(c, checkIcon).Size(14, 14).Shrink(0)
				}
			})
			b.OnClick(func() {
				v.C.State.Settings.ServiceTier = tier.ID
				if selected {
					v.C.State.Settings.ServiceTier = ""
				}
				v.C.Save()
			})
		}
	})
}

func (v *View) chooseModel(m codex.Model) {
	if v.C.Running[v.C.State.Selected] {
		return
	}
	t := v.C.State.Thread(v.C.State.Selected)
	if t != nil && t.Driver() != "codex" {
		if !v.C.Handoff("codex", m.Model) {
			return
		}
		v.modelPicker = false
		t = v.C.State.Thread(v.C.State.Selected)
	}
	if t != nil {
		t.AgentModel = m.Model
	}

	settings := &v.C.State.Settings
	settings.Model = m.Model
	if !m.SupportsSpeed(settings.ServiceTier) {
		settings.ServiceTier = ""
	}
	efforts := make([]string, 0, len(m.SupportedReasoningEfforts))
	for _, e := range m.SupportedReasoningEfforts {
		efforts = append(efforts, e.ReasoningEffort)
	}
	if !slices.Contains(efforts, settings.Effort) {
		settings.Effort = ""
		if slices.Contains(efforts, m.DefaultReasoningEffort) {
			settings.Effort = m.DefaultReasoningEffort
		} else if len(efforts) > 0 {
			settings.Effort = efforts[0]
		}
	}
	v.C.Save()
}

func pickerOption(c *ui.Context, label string, selected bool) ui.Element {
	b := ui.ButtonBase(c).Height(36).Shrink(0).Padding(0, 9).Gap(9).AlignItems(ui.Center).
		Radius(8).Label(label).Tooltip(label).Transition(hoverTransition)
	if selected {
		b.Background(c.Theme().Surface).TextColor(c.Theme().Accent)
	}
	if b.Hovered() {
		b.Background(c.Theme().SurfaceHover)
	}
	return b
}
