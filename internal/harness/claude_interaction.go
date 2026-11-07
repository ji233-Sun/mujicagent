package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

type claudeRequest struct {
	Subtype    string
	ToolName   string `json:"tool_name"`
	CallbackID string `json:"callback_id"`
	Input      json.RawMessage
}

func claudeInteraction(ctx context.Context, emit func(AgentEvent), req claudeRequest) (any, error) {
	hook := req.Subtype == "hook_callback" && req.CallbackID == "ask-user"
	if hook {
		var input struct {
			Name  string          `json:"tool_name"`
			Input json.RawMessage `json:"tool_input"`
		}
		if err := json.Unmarshal(req.Input, &input); err != nil {
			return nil, err
		}
		req.ToolName, req.Input = input.Name, input.Input
	}
	if req.Subtype != "can_use_tool" && !hook {
		return nil, fmt.Errorf("unsupported Claude Code client request")
	}
	var input map[string]any
	if err := json.Unmarshal(req.Input, &input); err != nil {
		return nil, err
	}
	deny := func() any {
		if hook {
			return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PreToolUse", "permissionDecision": "deny", "permissionDecisionReason": "User cancelled the question"}}
		}
		return map[string]any{"behavior": "deny", "message": "User declined the request"}
	}
	r := &codex.Request{Cancellable: true}
	if req.ToolName == "AskUserQuestion" {
		r.Method = "item/tool/requestUserInput"
		var v struct{ Questions []codex.Question }
		if err := json.Unmarshal(req.Input, &v); err != nil {
			return nil, err
		}
		if len(v.Questions) == 0 {
			return nil, fmt.Errorf("empty Claude Code question")
		}
		for i, q := range v.Questions {
			q.ID, q.IsOther = fmt.Sprint(i), true
			r.Questions = append(r.Questions, q)
		}
	} else {
		r.Method = "item/commandExecution/requestApproval"
		r.Reason = req.ToolName + "\n" + string(req.Input)
		if command, ok := input["command"].(string); ok {
			r.Command = command
		}
		r.AvailableDecisions = []json.RawMessage{json.RawMessage(`"accept"`), json.RawMessage(`"cancel"`)}
	}
	answer, err := ask(ctx, emit, r)
	if err != nil {
		return nil, err
	}
	var response struct {
		Decision string
		Answers  map[string]struct{ Answers []string }
	}
	if err := json.Unmarshal(answer, &response); err != nil {
		return nil, err
	}
	if req.ToolName == "AskUserQuestion" {
		answers := map[string]string{}
		for _, q := range r.Questions {
			values := response.Answers[q.ID].Answers
			if len(values) == 0 {
				return deny(), nil
			}
			answers[q.Question] = strings.Join(values, ", ")
		}
		input["answers"] = answers
	} else if response.Decision != "accept" {
		return deny(), nil
	}
	if hook {
		return map[string]any{"hookSpecificOutput": map[string]any{"hookEventName": "PreToolUse", "permissionDecision": "allow", "updatedInput": input}}, nil
	}
	return map[string]any{"behavior": "allow", "updatedInput": input}, nil
}
