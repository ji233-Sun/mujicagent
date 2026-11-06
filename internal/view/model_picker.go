package view

import (
	_ "embed"
	"slices"
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
	label := settings.Model
	if label == "" {
		label = tx.ChooseModel()
	} else if settings.Effort != "" {
		label += " · " + settings.Effort
	}
	for _, model := range v.C.Models {
		if model.Model != settings.Model || !model.SupportsSpeed(settings.ServiceTier) {
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
		Tooltip(label).Disabled(len(v.C.Models) == 0).Transition(hoverTransition)
	if button.Hovered() || v.modelPicker {
		button.Background(c.Theme().SurfaceHover).TextColor(c.Theme().Accent)
	}
	button.Children(func() {
		modelIcon(c, settings.Model, 16)
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
	})
	pickerPopover(c.Key("model-picker"), button, &v.modelPicker, tx.ModelPicker(), 448, func() { v.modelPanel(c) })
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
	tx := v.texts()
	models := make([]codex.Model, 0, len(v.C.Models))
	var current codex.Model
	for _, m := range v.C.Models {
		if m.Model == v.C.State.Settings.Model {
			current = m
		}
		if strings.Contains(strings.ToLower(m.Model+" "+m.DisplayName), strings.ToLower(v.modelSearch)) {
			models = append(models, m)
		}
	}
	ui.Row(c).Gap(8).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(274).Gap(5).Children(func() {
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
			listHeight := float32(min(5, max(1, len(v.C.Models))) * 36)
			ui.List(c.Key("models"), &v.modelList, len(models)).Height(listHeight).Label(tx.ModelList()).
				ItemKey(func(i int) any { return models[i].Model }).Rows(func(row ui.ListRow) {
				m := models[row.Index]
				c := row.Context
				selected := m.Model == v.C.State.Settings.Model
				b := pickerOption(c, m.Model, selected)
				b.Children(func() {
					modelIcon(c, m.Model, 18)
					ui.Text(c, m.Model).FontSize(12).SingleLine().Grow(1).MinWidth(0)
					if selected {
						ui.Icon(c, checkIcon).Size(14, 14).TextColor(c.Theme().Accent)
					}
				})
				b.OnClick(func() { v.chooseModel(m) })
			}).Children(func() {
				if len(models) == 0 {
					ui.Text(c, tx.NoModels()).FontSize(12).TextColor(c.Theme().TextMuted).Padding(6, 8)
				}
			})
		})
		ui.Box(c).Width(1).Background(c.Theme().Border).Margin(5, 0)
		ui.Column(c).Grow(1).Gap(2).Children(func() {
			ui.Text(c, tx.ReasoningEffort()).FontSize(11).TextColor(c.Theme().TextMuted).Padding(6, 8).Margin(0, 0, 3)
			v.effortSlider(c, current)
			v.speedOptions(c, current)
		})
	})
}

func (v *View) effortSlider(c *ui.Context, m codex.Model) {
	efforts := m.SupportedReasoningEfforts
	if len(efforts) == 0 {
		return
	}
	index := float64(0)
	for i, effort := range efforts {
		if effort.ReasoningEffort == v.C.State.Settings.Effort {
			index = float64(i)
		}
	}
	ui.Column(c.Key(m.Model)).Padding(0, 8, 6).Gap(6).Children(func() {
		ui.Row(c).AlignItems(ui.Center).Gap(6).Children(func() {
			ui.Icon(c, thinkingIcon).Size(14, 14).TextColor(c.Theme().TextMuted)
			ui.Text(c, efforts[int(index)].ReasoningEffort).FontSize(13).Grow(1)
		})
		if len(efforts) == 1 {
			return
		}
		ui.StepSlider(c, &index, 0, float64(len(efforts)-1), 1).
			Label(v.texts().AdjustReasoningEffort()).Tooltip(efforts[int(index)].ReasoningEffort).
			OnChange(func() {
				v.C.State.Settings.Effort = efforts[int(index)].ReasoningEffort
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

func modelIcon(c *ui.Context, name string, size float32) {
	icon := agentIcon
	if strings.HasPrefix(name, "gpt-") || strings.HasPrefix(name, "chatgpt-") || strings.HasPrefix(name, "o1") || strings.HasPrefix(name, "o3") || strings.HasPrefix(name, "o4") {
		icon = openAIIcon
	}
	ui.Icon(c, icon).Size(size, size).Shrink(0).TextColor(ui.Hex("#202524"))
}
