package harness

import (
	"encoding/json"
	"fmt"
	"strings"
)

func piArgs(o RunOptions) ([]string, error) {
	args := []string{"--mode", "rpc", "--session", o.Session}
	// Restricted runs expose only built-in read tools; extensions and MCP can
	// otherwise add execution paths outside this permission profile.
	switch o.Access {
	case "":
		args = append(args, "--no-extensions", "--no-mcp", "--tools", "read,grep,find,ls")
	case "native":
	default:
		return nil, fmt.Errorf("unsupported Pi permission: %s", o.Access)
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		args = append(args, "--thinking", o.Effort)
	}

	return args, nil
}

type piParser struct {
	driver      string
	message     int
	compactions int
	compacting  bool
}

func (p *piParser) line(line []byte, emit func(AgentEvent)) error {
	var e struct {
		Type    string
		Message struct {
			Role, StopReason, ErrorMessage string
			Content                        json.RawMessage
		}
		AssistantMessageEvent struct {
			Type, Delta, Content string
			ContentIndex         int
		}
	}
	if err := json.Unmarshal(line, &e); err != nil {
		return fmt.Errorf("invalid %s protocol output: %w", p.driver, err)
	}

	switch e.Type {
	case "message_start":
		if e.Message.Role == "assistant" {
			p.message++
		}
	case "message_update":
		if strings.HasPrefix(e.AssistantMessageEvent.Type, "thinking_") {
			return p.activity(line, emit)
		}
		if e.AssistantMessageEvent.Type == "text_delta" {
			emit(AgentEvent{ID: fmt.Sprintf("message-%d-%d", p.message, e.AssistantMessageEvent.ContentIndex), Kind: "agentMessage", Text: e.AssistantMessageEvent.Delta, Delta: true})
		}
	case "message_end":
		if e.Message.Role != "assistant" {
			return nil
		}
		if e.Message.StopReason == "aborted" {
			return fmt.Errorf("Pi: %w", ErrInterrupted)
		}
		if e.Message.StopReason == "error" {
			return fmt.Errorf("Pi: %s %s", e.Message.StopReason, e.Message.ErrorMessage)
		}
		var content []struct{ Type, Text, Thinking string }
		if err := json.Unmarshal(e.Message.Content, &content); err != nil {
			return err
		}
		for i, part := range content {
			switch part.Type {
			case "text":
				emit(AgentEvent{ID: fmt.Sprintf("message-%d-%d", p.message, i), Kind: "agentMessage", Text: part.Text, Status: "completed"})
			case "thinking":
				emit(AgentEvent{ID: fmt.Sprintf("thinking-%d-%d", p.message, i), Kind: "reasoning", Text: part.Thinking, Status: "completed"})
			}
		}

	case "tool_execution_start", "tool_execution_update", "tool_execution_end", "compaction_start", "compaction_end", "auto_compaction_start", "auto_compaction_end":
		return p.activity(line, emit)
	}
	return nil
}

func (p *piParser) activity(line []byte, emit func(AgentEvent)) error {
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
		if !p.compacting && (!strings.HasSuffix(v.Type, "_end") || p.compactions == 0) {
			p.compactions++
		}
		p.compacting = true
		e.ID, e.Kind = fmt.Sprintf("compaction-%d", p.compactions), "contextCompaction"
		if strings.HasSuffix(v.Type, "_end") {
			p.compacting = false
			var result struct{ TokensBefore *int64 }
			if json.Unmarshal(v.Result, &result) == nil && result.TokensBefore != nil && *result.TokensBefore >= 0 {
				e.Compaction.Before, e.Compaction.BeforeKnown = *result.TokensBefore, true
			}
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
		if v.ToolName != "" {
			e.Tool.Kind = NativeToolKind(v.ToolName)
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
		var result struct{ Details struct{ Server, Tool string } }
		if json.Unmarshal(e.Tool.Result, &result) == nil && result.Details.Server != "" && result.Details.Tool != "" {
			e.Tool.Server = result.Details.Server
			e.Tool.Name = result.Details.Tool
			e.Tool.Kind = "mcp"
		}
	}
	emit(e)
	return nil
}
