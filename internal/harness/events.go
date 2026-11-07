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
	ParentID                  string
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
		if !p.compacting {
			p.compactions++
		}
		p.compacting = true
		e.ID, e.Kind = fmt.Sprintf("compaction-%d", p.compactions), "contextCompaction"
		if strings.HasSuffix(v.Type, "_end") {
			p.compacting = false
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

// NativeToolKind maps native aliases, never inferring an MCP server identity.
func NativeToolKind(name string) string {
	name = strings.ToLower(name)
	if i := strings.LastIndex(name, "."); i >= 0 {
		name = name[i+1:]
	}
	switch name {
	case "bash", "execute", "script", "waitforscript", "shell", "shell_command", "run_command":
		return "execute"
	case "edit", "multiedit", "write", "create", "applypatch", "apply_patch", "edit_file", "write_file", "create_file", "str_replace_editor":
		return "edit"
	case "read", "ls", "read_file", "list_directory":
		return "read"
	case "grep", "glob", "find", "websearch", "web_search":
		return "search"
	case "fetchurl", "webfetch":
		return "fetch"
	case "todowrite", "todo", "update_plan":
		return "plan"
	case "askuser", "userask", "ask_user", "request_user_input":
		return "ask"
	case "compact", "compactcontext", "compact_context":
		return "compaction"
	default:
		return "other"
	}
}
