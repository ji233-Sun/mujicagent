package view

import (
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func (v *View) mcpItem(c *ui.Context, item *model.Item, key string, running bool) {
	tx := v.texts()
	detail := item.MCP
	if detail == nil {
		detail = &model.MCPCall{}
	}
	title := detail.Tool
	if title == "" {
		title = tx.McpUnknownTool()
	}
	if detail.Server != "" {
		title += " · " + detail.Server
	}
	status := tx.McpUnknownStatus()
	switch item.Status {
	case "inProgress":
		status = tx.Stopped()
		if running {
			status = tx.McpRunning()
		}
	case "interrupted":
		status = tx.Stopped()
	case "completed":
		status = tx.McpCompleted()
	case "failed":
		status = tx.Failed()
	}
	summary := tx.McpNoArguments()
	if detail.Arguments != "" {
		summary = tx.McpArguments() + ": " + detail.Arguments
	}
	if item.Status == "failed" {
		summary = detail.Error
		if summary == "" {
			summary = tx.McpNoError()
		}
	}
	// Keep the preview short; the full sanitized content remains in the panel.
	summary = strings.Join(strings.Fields(summary), " ")
	if chars := []rune(summary); len(chars) > 100 {
		summary = string(chars[:100]) + "…"
	}
	ui.Column(c.Key(key)).Children(func() {
		p := ui.CollapsibleBase(c, v.openState(key))
		p.Trigger.Padding(8, 10).Gap(8).AlignItems(ui.Center).Radius(7).Label(title).Tooltip(title).Transition(hoverTransition)
		if p.Trigger.Hovered() {
			p.Trigger.Background(c.Theme().SurfaceHover)
		}
		p.Trigger.Children(func() {
			ui.Icon(c, chevron).Size(12, 12).Rotate(90 * p.Progress()).Shrink(0)
			_, icon := v.activityKind(item.Kind)
			ui.Icon(c, icon).Size(15, 15).Shrink(0)
			ui.Column(c).Grow(1).MinWidth(0).Gap(3).Children(func() {
				ui.Text(c, title).FontSize(12).SingleLine()
				ui.Text(c, summary).FontSize(11).SingleLine().TextColor(c.Theme().TextMuted)
			})
			color := c.Theme().TextMuted
			if item.Status == "failed" {
				color = c.Theme().Danger
				ui.Icon(c, failureIcon).Size(14, 14).TextColor(color)
			}
			if item.Status == "inProgress" && running {
				ui.Spinner(c).Size(13, 13)
			}
			ui.Text(c, status).FontSize(11).Shrink(0).TextColor(color)
		})
		p.Panel(func() {
			if item.Kind == "mcpToolCall" && (detail.Tool == "" || detail.Server == "") {
				ui.Text(c, tx.McpMissingMetadata()).FontSize(11).TextColor(c.Theme().TextMuted)
			}
			v.mcpDetail(c, tx.McpArguments(), detail.Arguments, tx.McpNoArguments())
			missingResult := tx.McpNoResult()
			if item.Status == "inProgress" && running {
				missingResult = tx.McpWaitingResult()
			}
			v.mcpDetail(c, tx.McpResult(), detail.Result, missingResult)
			if item.Status == "failed" || detail.Error != "" {
				v.mcpDetail(c, tx.McpError(), detail.Error, tx.McpNoError())
			}
		}).Gap(10).Padding(4, 12, 12)
	})
}

func (v *View) mcpDetail(c *ui.Context, title, text, missing string) {
	ui.Column(c.Key(title)).Gap(4).Children(func() {
		ui.Text(c, title).FontSize(11).Bold()
		if text == "" {
			ui.Text(c, missing).FontSize(12).TextColor(c.Theme().TextMuted)
			return
		}
		ui.Scroll(c.Key("content")).MaxHeight(220).Padding(10, 12).Radius(8).Background(c.Theme().Background).Children(func() {
			ui.Text(c, text).Font("monospace").FontSize(12).Selectable()
		})
	})
}
