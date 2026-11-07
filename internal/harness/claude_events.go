package harness

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/model"
)

func (p *claudeParser) line(raw []byte) error {
	var e struct {
		Type, Subtype, UUID, Result, Status, State string
		IsError                                    bool            `json:"is_error"`
		ParentID                                   string          `json:"parent_tool_use_id"`
		TaskID                                     string          `json:"task_id"`
		TaskType                                   string          `json:"task_type"`
		ToolUseResult                              json.RawMessage `json:"tool_use_result"`
		Patch                                      struct{ Status string }
		Message                                    struct {
			ID, Role string
			Content  json.RawMessage
		}
		Event struct {
			Type, ID string
			Index    int
			Message  struct{ ID string }
			Block    struct {
				Type, ID, Name, Text, Thinking string
				Input                          json.RawMessage
			} `json:"content_block"`
			Delta struct {
				Type, Text, Thinking string
				PartialJSON          string `json:"partial_json"`
			}
		}
		Compact struct {
			PreTokens int64 `json:"pre_tokens"`
		} `json:"compact_metadata"`
	}
	if json.Unmarshal(raw, &e) != nil {
		return fmt.Errorf("invalid Claude Code event")
	}
	switch e.Type {
	case "system":
		switch e.Subtype {
		case "status":
			if e.Status == "compacting" {
				p.startCompaction()
			}
		case "compact_boundary":
			if !p.compactPost {
				p.startCompaction()
			}
			p.compactPost = false
			stats := model.CompactionStats{}
			if e.Compact.PreTokens > 0 {
				stats.Before, stats.BeforeKnown = e.Compact.PreTokens, true
			}
			p.emit(AgentEvent{ID: fmt.Sprintf("claude-compact-%d", p.compactions), Kind: "contextCompaction", Compaction: stats, Status: "completed", PreserveText: true})
			p.finishCompaction("completed", "")
			p.compactBoundary = true
		case "session_state_changed":
			p.sessionState = e.State
		case "task_started":
			if e.TaskType == "local_agent" || e.TaskType == "local_workflow" {
				if p.tasks == nil {
					p.tasks = map[string]bool{}
				}
				p.tasks[e.TaskID] = true
			}
		case "task_notification":
			delete(p.tasks, e.TaskID)
		case "task_updated":
			if e.Patch.Status == "completed" || e.Patch.Status == "failed" || e.Patch.Status == "stopped" {
				delete(p.tasks, e.TaskID)
			}
		}
	case "stream_event":
		if e.ParentID == "" {
			p.result = false
		}
		v := e.Event
		p.message = p.messages[e.ParentID]
		switch v.Type {
		case "message_start":
			p.message = v.Message.ID
			if p.messages == nil {
				p.messages = map[string]string{}
			}
			p.messages[e.ParentID] = p.message
		case "content_block_start":
			if p.blocks == nil {
				p.blocks = map[string]*claudeBlock{}
			}
			key := fmt.Sprintf("%s:%d", p.message, v.Index)
			b := &claudeBlock{ID: fmt.Sprintf("%s-%d", p.message, v.Index), Parent: e.ParentID}
			switch v.Block.Type {
			case "text":
				b.Kind = "agentMessage"
			case "thinking":
				b.Kind = "reasoning"
			case "tool_use", "server_tool_use":
				b.ID, b.Name = v.Block.ID, v.Block.Name
				b.Kind = "tool"
			default:
				return nil
			}
			p.blocks[key] = b
			if b.Kind == "tool" {
				p.emitTool(b.ID, b.Name, b.Parent, v.Block.Input, nil, "inProgress")
			} else {
				p.emit(AgentEvent{ID: b.ID, Kind: b.Kind, Text: v.Block.Text + v.Block.Thinking, Status: "inProgress"})
			}
		case "content_block_delta", "content_block_stop":
			b := p.blocks[fmt.Sprintf("%s:%d", p.message, v.Index)]
			if b == nil {
				return nil
			}
			if v.Type == "content_block_stop" {
				if b.Kind == "tool" {
					if b.JSON != "" && json.Valid([]byte(b.JSON)) {
						p.emitTool(b.ID, b.Name, b.Parent, json.RawMessage(b.JSON), nil, "inProgress")
					}
				} else {
					p.emit(AgentEvent{ID: b.ID, Kind: b.Kind, Status: "completed", PreserveText: true})
				}
			} else if v.Delta.Type == "input_json_delta" {
				b.JSON += v.Delta.PartialJSON
			} else if v.Delta.Type == "text_delta" || v.Delta.Type == "thinking_delta" {
				p.emit(AgentEvent{ID: b.ID, Kind: b.Kind, Text: v.Delta.Text + v.Delta.Thinking, Delta: true})
			}
		}
	case "assistant", "user":
		if e.Type == "assistant" && e.ParentID == "" {
			p.result = false
		}
		var blocks []struct {
			Type, ID, Name, Text, Thinking string
			ToolUseID                      string `json:"tool_use_id"`
			Input, Content                 json.RawMessage
			IsError                        bool `json:"is_error"`
		}
		if json.Unmarshal(e.Message.Content, &blocks) != nil {
			return nil // A replayed user prompt can be a plain string.
		}
		for i, b := range blocks {
			switch b.Type {
			case "text", "thinking":
				if e.Type != "assistant" {
					continue
				}
				kind, text := "agentMessage", b.Text
				if b.Type == "thinking" {
					kind, text = "reasoning", b.Thinking
				}
				p.emit(AgentEvent{ID: fmt.Sprintf("%s-%d", e.Message.ID, i), Kind: kind, Text: text, Status: "completed"})
			case "tool_use", "server_tool_use":
				key := fmt.Sprintf("%s:%d", e.Message.ID, i)
				if p.blocks == nil {
					p.blocks = map[string]*claudeBlock{}
				}
				p.blocks[key] = &claudeBlock{ID: b.ID, Name: b.Name, Kind: "tool", Parent: e.ParentID, JSON: string(b.Input)}
				p.emitTool(b.ID, b.Name, e.ParentID, b.Input, nil, "inProgress")
			case "tool_result":
				status := "completed"
				if b.IsError {
					status = "failed"
				}
				var call *claudeBlock
				for _, block := range p.blocks {
					if block.ID == b.ToolUseID {
						call = block
						break
					}
				}
				if call != nil {
					p.emitTool(b.ToolUseID, call.Name, call.Parent, nil, b.Content, status)
					if !b.IsError && (call.Name == "TaskCreate" || call.Name == "TaskUpdate") {
						p.taskTool(call.Name, json.RawMessage(call.JSON), e.ToolUseResult)
					}
				} else {
					p.emit(AgentEvent{ID: b.ToolUseID, Status: status, Tool: &ToolEvent{Result: b.Content}})
				}
			}
		}
	case "result":
		if e.IsError || e.Subtype != "success" {
			p.finishCompaction("failed", "")
			return fmt.Errorf("Claude Code request failed (%s); check CLI authentication, model access and configuration", e.Subtype)
		}
		p.result = true
	}
	return nil
}

// MCP tool names are namespaced by Claude Code itself, so these are protocol
// metadata rather than guesses about arbitrary native tool names.
func claudeTool(name string) (kind, server, tool string) {
	if strings.HasPrefix(name, "mcp__") {
		server, tool, ok := strings.Cut(strings.TrimPrefix(name, "mcp__"), "__")
		if ok && server != "" && tool != "" {
			return "mcp", server, tool
		}
	}
	return NativeToolKind(name), "", name
}
