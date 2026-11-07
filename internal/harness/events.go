package harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

// ToolEvent is a partial update. Nil payloads preserve earlier fields; a JSON
// null is an explicit empty value. Result snapshots replace, never append.
// Protocol adapters own classification; consumers never inspect wire records.
type ToolEvent struct {
	Name, Kind, Title, Server string
	Arguments, Result         json.RawMessage
}

func (p *agentParser) activity(line []byte, emit func(AgentEvent)) error {
	var v struct {
		Type, ToolCallID, ToolName, ErrorMessage string
		Args, PartialResult, Result              json.RawMessage
		IsError, Aborted                         bool
		AssistantMessageEvent                    struct {
			Type, Delta, Content string
			ContentIndex         int
		}
	}
	if err := json.Unmarshal(line, &v); err != nil {
		return err
	}
	e := AgentEvent{ID: v.ToolCallID, Status: "inProgress"}
	switch v.Type {
	case "message_update":
		a := v.AssistantMessageEvent
		e.ID, e.Kind = fmt.Sprintf("thinking-%d-%d", p.message, a.ContentIndex), "reasoning"
		e.Text, e.Delta = a.Delta, true
		if a.Type == "thinking_end" {
			e.Text, e.Delta, e.Status = a.Content, false, "completed"
		}
	case "compaction_start", "auto_compaction_start", "compaction_end", "auto_compaction_end":
		e.ID, e.Kind = fmt.Sprintf("compaction-%d", p.message), "contextCompaction"
		if strings.HasSuffix(v.Type, "_end") {
			e.Status = "completed"
			if v.Aborted {
				e.Status = "interrupted"
			}
			if v.ErrorMessage != "" {
				e.Status, e.Text = "failed", v.ErrorMessage
			}
		}
	default:
		if e.ID == "" {
			return nil
		}
		e.Tool = &ToolEvent{Name: v.ToolName, Arguments: v.Args}
		switch v.ToolName {
		case "bash":
			e.Tool.Kind = "execute"
		case "edit", "write":
			e.Tool.Kind = "edit"
		case "grep", "find":
			e.Tool.Kind = "search"
		case "read", "ls":
			e.Tool.Kind = "read"
		default:
			e.Tool.Kind = "other"
		}
		if v.Type == "tool_execution_update" {
			e.Tool.Result = v.PartialResult
		}
		if v.Type == "tool_execution_end" {
			e.Tool.Result, e.Status = v.Result, "completed"
			if v.IsError {
				e.Status = "failed"
			}
		}
	}
	emit(e)
	return nil
}
