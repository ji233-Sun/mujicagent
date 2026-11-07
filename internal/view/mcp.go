package view

import (
	"encoding/json"
	"strings"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func nativeTool(item *model.Item) bool {
	return item.MCP != nil && harness.NativeToolKind(item.MCP.Tool) != "other"
}

// Native tools use the same compact disclosure and output viewer as Codex.
// Keep protocol envelopes in persisted details, not in the timeline preview.
func toolActivityText(item *model.Item, expanded bool) string {
	d := item.MCP
	title := d.Tool
	body := ""
	var args map[string]json.RawMessage
	if json.Unmarshal([]byte(d.Arguments), &args) != nil {
		var patch string
		if item.Kind == "fileChange" && json.Unmarshal([]byte(d.Arguments), &patch) == nil {
			args = map[string]json.RawMessage{"input": json.RawMessage(d.Arguments)}
		}
	}
	get := func(keys ...string) string {
		for _, key := range keys {
			var value string
			if json.Unmarshal(args[key], &value) == nil && value != "" {
				return value
			}
		}
		return ""
	}
	switch item.Kind {
	case "commandExecution":
		if script := get("script", "code"); script != "" {
			title = "Script"
			if expanded {
				body = script
			}
		} else if command := get("command", "commandLine", "cmd"); command != "" {
			title = "$ " + command
		}
	case "fileChange":
		if path := get("file_path", "path", "file"); path != "" {
			title = path
		}
		if expanded {
			if patch := get("patch", "diff", "input"); patch != "" {
				body = patch
			} else {
				var diff strings.Builder
				add := func(prefix, text string) {
					if text == "" {
						return
					}
					for _, line := range strings.Split(strings.TrimSuffix(text, "\n"), "\n") {
						diff.WriteString(prefix)
						diff.WriteString(line)
						diff.WriteByte('\n')
					}
				}
				old, next := get("old_str", "oldText", "old_string"), get("new_str", "newText", "new_string")
				if old != "" || next != "" {
					add("-", old)
					add("+", next)
				} else {
					add("+", get("content", "file_text", "text"))
				}
				body = diff.String()
			}
		}
	default:
		if questions := get("questionnaire", "question"); questions != "" {
			title += " · " + strings.SplitN(questions, "\n", 2)[0]
		}
		if target := get("file_path", "path", "pattern", "query", "url", "command", "folder", "directory_path"); target != "" {
			title += " · " + target
		}
	}
	if !expanded {
		return strings.SplitN(title, "\n", 2)[0]
	}
	if body != "" {
		title += "\n" + body
	}
	output := toolResultText(d.Result)
	if item.Status == "failed" && d.Error != "" && d.Error != d.Result {
		output += "\n" + toolResultText(d.Error)
	}
	if output != "" {
		return title + "\n" + output
	}
	return title
}

func toolResultText(raw string) string {
	var value any
	if json.Unmarshal([]byte(raw), &value) != nil {
		return raw
	}
	var extract func(any) string
	extract = func(value any) string {
		switch v := value.(type) {
		case string:
			return v
		case []any:
			var parts []string
			for _, part := range v {
				if text := extract(part); text != "" {
					parts = append(parts, text)
				}
			}
			return strings.Join(parts, "\n")
		case map[string]any:
			// Images and private thinking are not textual tool output.
			if kind, _ := v["type"].(string); kind == "image" || kind == "thinking" {
				return ""
			}
			var parts []string
			for _, key := range []string{"text", "content", "output", "stdout", "stderr", "error", "message"} {
				if text := extract(v[key]); text != "" {
					parts = append(parts, text)
				}
			}
			if len(parts) == 0 {
				data, _ := json.MarshalIndent(v, "", "  ")
				return string(data)
			}
			return strings.Join(parts, "\n")
		}
		return ""
	}
	return extract(value)
}

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
