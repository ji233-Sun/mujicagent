package controller

import (
	"encoding/json"
	"fmt"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"strings"
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
	if e.Compaction.BeforeKnown {
		item.Compaction.Before, item.Compaction.BeforeKnown = e.Compaction.Before, true
	}
	if e.Compaction.AfterKnown {
		item.Compaction.After, item.Compaction.AfterKnown = e.Compaction.After, true
	}
	if e.Tool == nil {
		if e.Delta {
			item.Text += e.Text
		} else if e.Text != "" || item.Kind != "contextCompaction" {
			item.Text = e.Text
			if item.Kind == "contextCompaction" {
				item.Text = redactMCPText(item.Text)
			}
		}
		return
	}
	wasMCP := item.Kind == "mcpToolCall"
	t := e.Tool
	if t.ParentID != "" {
		item.ParentID = t.ParentID
	}
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
		case "compaction":
			item.Kind = "contextCompaction"
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
			if text := agentPlanText(t.Arguments); text != "" {
				item.Text = redactMCPText(text)
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

func agentPlanText(raw json.RawMessage) string {
	var input struct {
		Todos json.RawMessage
		Plan  json.RawMessage
	}
	if json.Unmarshal(raw, &input) != nil {
		return ""
	}
	var text string
	if json.Unmarshal(input.Todos, &text) == nil {
		return text
	}
	var entries []struct{ Content, Text, Step, Status string }
	if json.Unmarshal(input.Todos, &entries) != nil && json.Unmarshal(input.Plan, &entries) != nil {
		return ""
	}
	var lines []string
	for i, entry := range entries {
		text := entry.Content
		if text == "" {
			text = entry.Text
		}
		if text == "" {
			text = entry.Step
		}
		lines = append(lines, fmt.Sprintf("%d. [%s] %s", i+1, entry.Status, text))
	}
	return strings.Join(lines, "\n")
}

// A turn ending or transport disappearing does not prove compaction succeeded.
func markPendingCompactions(turn *model.Turn, status string) {
	for i := range turn.Items {
		item := &turn.Items[i]
		if item.Kind == "contextCompaction" && (item.Status == "" || item.Status == "inProgress") {
			item.Status = status
		}
	}
}
