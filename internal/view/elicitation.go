package view

import (
	"encoding/json"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/codex"
)

func (v *View) elicitation(c *ui.Context, r *codex.Request, values map[string]string) {
	tx := v.texts()
	ui.Text(c, tx.McpRequest()+" · "+r.ServerName).Bold()
	message := r.Message
	if message == "" {
		message = r.Title + "\n" + r.Description
	}
	ui.Text(c, message).FontSize(12).Selectable()
	valid := false
	if r.Mode == "url" && r.ElicitationURL() {
		ui.Text(c, r.URL).Selectable()
		ui.Text(c, tx.McpURLHint()).FontSize(12).TextColor(c.Theme().TextMuted)
		valid = true
	} else if form, err := r.ElicitationForm(); err == nil {
		for _, key := range form.Keys() {
			field := form.Properties[key]
			ui.Column(c.Key(key)).Gap(3).Children(func() {
				title := field.Title
				if title == "" {
					title = key
				}
				if slices.Contains(form.Required, key) {
					title += " *"
				}
				ui.Text(c, title).FontSize(12).TextColor(c.Theme().TextMuted)
				if field.Description != "" {
					ui.Text(c, field.Description).FontSize(12)
				}
				options := field.Options()
				if field.Type == "boolean" {
					options = []codex.ElicitationOption{{Value: "true", Title: tx.McpYes()}, {Value: "false", Title: tx.McpNo()}}
				}
				if len(options) > 0 {
					for i, option := range options {
						selected := values[key] == option.Value
						var multi []string
						if field.Type == "array" {
							_ = json.Unmarshal([]byte(values[key]), &multi)
							selected = slices.Contains(multi, option.Value)
						}
						icon := shieldIcon
						if selected {
							icon = checkIcon
						}
						b := approvalRow(c.Key(i), option.Title, "", icon, selected).Disabled(r.Responding)
						b.OnClick(func() {
							if field.Type != "array" {
								values[key] = option.Value
								return
							}
							if selected {
								multi = slices.DeleteFunc(multi, func(s string) bool { return s == option.Value })
							} else {
								multi = append(multi, option.Value)
							}
							if multi == nil {
								multi = []string{}
							}
							data, _ := json.Marshal(multi)
							values[key] = string(data)
						})
					}
				} else {
					value := values[key]
					input := ui.TextInput(c, &value).Label(key).Disabled(r.Responding)
					if input.Changed() {
						values[key] = value
					}
				}
			})
		}
		_, err := form.Content(values)
		valid = err == nil
		if err != nil {
			ui.Text(c, tx.McpInputHint()).FontSize(12).TextColor(c.Theme().TextMuted)
		}
	} else {
		ui.Text(c, tx.McpUnsupported()+" ("+r.Mode+")").TextColor(c.Theme().Danger)
	}
	ui.Column(c).Gap(3).Children(func() {
		approvalRow(c.Key("accept"), tx.McpAccept(), "", checkIcon, true).Disabled(r.Responding || !valid).OnClick(func() { v.C.RespondElicitation(r, "accept", values) })
		approvalRow(c.Key("decline"), tx.Deny(), "", closeIcon, false).Disabled(r.Responding).OnClick(func() { v.C.RespondElicitation(r, "decline", nil) })
		approvalRow(c.Key("cancel"), tx.Cancel(), "", failureIcon, false).Disabled(r.Responding).OnClick(func() { v.C.RespondElicitation(r, "cancel", nil) })
	})
}
