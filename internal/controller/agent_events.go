package controller

import (
	"encoding/json"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

// mergeAgentEvent is shared by all external adapters. Stable IDs preserve
// parallel calls and late updates without reordering the visible transcript.
func mergeAgentEvent(turn *model.Turn, e harness.AgentEvent) {
	if e.ID == "" {
		return
	}
	index := -1
	for i := range turn.Items {
		if turn.Items[i].ID == e.ID {
			index = i
			break
		}
	}
	if index < 0 {
		turn.Items = append(turn.Items, model.Item{ID: e.ID, Kind: e.Kind, Status: "inProgress"})
		index = len(turn.Items) - 1
	}
	item := &turn.Items[index]
	if e.Kind != "" {
		item.Kind = e.Kind
	}
	if e.Status != "" && !(e.Status == "inProgress" && (item.Status == "completed" || item.Status == "failed" || item.Status == "interrupted")) {
		item.Status = e.Status
	}
	if e.Tool == nil {
		if e.Delta {
			item.Text += e.Text
		} else {
			item.Text = e.Text
		}
		return
	}
	wasMCP := item.Kind == "mcpToolCall"
	t := e.Tool
	if item.Kind == "" {
		item.Kind = "toolCall"
	}
	if t.Kind != "" {
		switch t.Kind {
		case "execute":
			item.Kind = "commandExecution"
		case "edit", "delete", "move":
			item.Kind = "fileChange"
		case "mcp":
			item.Kind = "mcpToolCall"
		case "plan":
			item.Kind = "plan"
		case "search", "fetch":
			item.Kind = "webSearch"
		default:
			item.Kind = "toolCall"
		}
	}
	if t.Server != "" || wasMCP {
		item.Kind = "mcpToolCall"
	}
	if item.MCP == nil {
		item.MCP = &model.MCPCall{}
	}
	d := item.MCP
	if !wasMCP || t.Kind == "mcp" || t.Server != "" || d.Tool == "" {
		if t.Name != "" {
			d.Tool = redactMCPText(t.Name)
		} else if t.Title != "" {
			d.Tool = redactMCPText(t.Title)
		}
	}
	if t.Server != "" {
		d.Server = redactMCPText(t.Server)
	}
	if t.Arguments != nil {
		if item.Kind == "plan" {
			var input struct{ Todos string }
			if json.Unmarshal(t.Arguments, &input) == nil {
				item.Text = redactMCPText(input.Todos)
			}
		}
		d.Arguments = redactMCPJSON(t.Arguments)
	}
	if t.Result != nil {
		d.Result = redactMCPJSON(t.Result)
	}
	if e.Status == "failed" {
		d.Error = d.Result
	}
}
