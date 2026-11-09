package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

func codexArgs(o RunOptions) ([]string, error) { return nil, nil }

func codexSteerTurn(ctx context.Context, o RunOptions, target TurnTarget, input Input) error {
	return o.Client.Call(ctx, "turn/steer", map[string]any{
		"threadId": target.Session, "expectedTurnId": target.Turn, "input": input.codex(),
	}, nil)
}

func codexInterrupt(ctx context.Context, o RunOptions, target TurnTarget) error {
	return o.Client.Call(ctx, "turn/interrupt", map[string]string{
		"threadId": target.Session, "turnId": target.Turn,
	}, nil)
}

func runCodex(ctx context.Context, o RunOptions, _ []string, emit func(AgentEvent)) error {
	ctx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	profiles, err := o.Client.PermissionProfiles(ctx, o.Cwd)
	if err != nil {
		return err
	}
	var permission *codex.PermissionProfile
	for i := range profiles {
		if profiles[i].ID == o.Access && profiles[i].Allowed {
			permission = &profiles[i]
			break
		}
	}
	if permission == nil {
		return fmt.Errorf("Permission profile is unavailable: %s", o.Access)
	}
	params := map[string]any{
		"cwd": o.Cwd, "permissions": permission.WireID(),
		"approvalPolicy": permission.ApprovalPolicy(), "approvalsReviewer": permission.Reviewer(),
	}
	if o.Model != "" {
		params["model"] = o.Model
	}
	session := o.Session
	if !o.Loaded {
		method := "thread/start"
		if session != "" {
			method = "thread/resume"
			params["threadId"] = session
		}
		var result struct{ Thread struct{ ID string } }
		if err := o.Client.Call(ctx, method, params, &result); err != nil {
			return err
		}
		session = result.Thread.ID
		if session == "" {
			return fmt.Errorf("Codex returned no thread ID")
		}
		emit(AgentEvent{Session: session, Loaded: true})
	}
	input := o.Input
	if input == nil {
		input = Input{{Type: "text", Text: o.Prompt}}
	}
	params = map[string]any{
		"threadId": session, "serviceTier": o.ServiceTier, "permissions": permission.WireID(),
		"approvalPolicy": permission.ApprovalPolicy(), "approvalsReviewer": permission.Reviewer(),
		"summary": "auto", "input": input.codex(),
	}
	if o.Model != "" {
		params["model"] = o.Model
		mode := "default"
		if o.Plan {
			mode = "plan"
		}
		params["collaborationMode"] = map[string]any{"mode": mode, "settings": map[string]any{
			"model": o.Model, "reasoning_effort": o.Effort, "developer_instructions": nil,
		}}
	}
	if o.Effort != "" {
		params["effort"] = o.Effort
	}
	var result struct{ Turn struct{ ID string } }
	if err := o.Client.Call(ctx, "turn/start", params, &result); err != nil {
		return err
	}
	if result.Turn.ID == "" {
		return fmt.Errorf("Codex returned no turn ID")
	}
	emit(AgentEvent{Session: session, TurnID: result.Turn.ID, Accepted: true})
	return nil
}

// CodexEvent carries routing separately from the shared turn event. Restored
// history and notifications for another turn cannot be appended to this turn.
type CodexEvent struct {
	ThreadID, TurnID string
	Event            AgentEvent
	Request          *codex.Request
	ResolvedID       json.RawMessage
}

func DecodeCodexEvent(m codex.Message) (CodexEvent, bool, error) {
	var event CodexEvent
	if len(m.ID) > 0 {
		switch m.Method {
		case "item/tool/requestUserInput", "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "mcpServer/elicitation/request":
			r := &codex.Request{ID: m.ID, Method: m.Method}
			if err := json.Unmarshal(m.Params, r); err != nil {
				return event, false, err
			}
			event.ThreadID, event.TurnID, event.Request = r.ThreadID, r.TurnID, r
			return event, true, nil
		default:
			return event, false, nil
		}
	}
	var p struct {
		ThreadID, TurnID, ItemID, Delta string
		RequestID                       json.RawMessage
		Item                            json.RawMessage
		Turn                            struct {
			ID, Status string
			Error      *struct{ Message string }
		}
		Error     *struct{ Message string }
		WillRetry bool
		Plan      []struct{ Step, Status string }
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return event, false, err
	}
	event.ThreadID, event.TurnID = p.ThreadID, p.TurnID
	e := &event.Event
	switch m.Method {
	case "serverRequest/resolved":
		event.ResolvedID = p.RequestID
	case "turn/started":
		event.TurnID, e.TurnID = p.Turn.ID, p.Turn.ID
	case "turn/completed":
		if p.Turn.ID == "" {
			return event, false, nil
		}
		event.TurnID = p.Turn.ID
		e.End = &TurnResult{Status: p.Turn.Status}
		if p.Turn.Error != nil {
			e.End.Error = p.Turn.Error.Message
		}
	case "error":
		if p.Error != nil {
			e.TurnError = p.Error.Message
			if !p.WillRetry {
				e.End = &TurnResult{Status: "failed", Error: p.Error.Message}
			}
		}
	case "item/started", "item/completed":
		var v struct {
			ID, Type, Text, Status, Command, AggregatedOutput string
			Summary, Content                                  []string
			Changes                                           []struct{ Path, Diff string }
			Query, Path, Server, Tool, Namespace              string
			Arguments, Result                                 json.RawMessage
			Error                                             *struct{ Message string }
		}
		if err := json.Unmarshal(p.Item, &v); err != nil {
			return event, false, err
		}
		if v.ID == "" || v.Type == "userMessage" {
			return event, false, nil
		}
		if v.Type == "dynamicToolCall" && v.Namespace == "" && codex.IsBrowserTool(v.Tool) {
			// The browser dispatcher publishes safe activity. Native snapshots can
			// contain fill values and screenshots, and cannot overwrite its result.
			return event, false, nil
		}
		e.ID, e.Kind, e.Status, e.Text = v.ID, v.Type, v.Status, v.Text
		if e.Status == "" {
			e.Status = "inProgress"
			if m.Method == "item/completed" {
				e.Status = "completed"
			}
		}
		switch v.Type {
		case "reasoning":
			e.Text = strings.Join(v.Summary, "\n\n")
			if e.Text == "" {
				e.Text = strings.Join(v.Content, "\n\n")
			}
		case "commandExecution":
			e.Text = "$ " + v.Command
			if v.AggregatedOutput != "" {
				e.Text += "\n" + v.AggregatedOutput
			}
			if v.Command == "" && v.AggregatedOutput == "" {
				e.Text = ""
			}
		case "fileChange":
			for _, change := range v.Changes {
				e.Text += change.Path + "\n" + change.Diff + "\n"
			}
		case "webSearch":
			e.Text = v.Query
		case "imageView":
			e.Text = v.Path
		case "mcpToolCall":
			e.Tool = &ToolEvent{Kind: "mcp", Name: v.Tool, Server: v.Server, Arguments: v.Arguments, Result: v.Result}
			if v.Error != nil {
				e.Tool.Error = &v.Error.Message
			}
		}
		e.PreserveText = e.Text == ""
	case "item/agentMessage/delta", "item/reasoning/summaryTextDelta", "item/reasoning/textDelta", "item/commandExecution/outputDelta", "item/plan/delta":
		e.ID, e.Kind, e.Text, e.Delta = p.ItemID, "agentMessage", p.Delta, true
		if strings.Contains(m.Method, "reasoning") {
			e.Kind = "reasoning"
		} else if strings.Contains(m.Method, "commandExecution") {
			e.Kind = "commandExecution"
		} else if strings.Contains(m.Method, "/plan/") {
			e.Kind = "plan"
		}
	case "turn/plan/updated":
		e.ID, e.Kind = "plan", "plan"
		for _, step := range p.Plan {
			e.Text += fmt.Sprintf("- [%s] %s\n", step.Status, step.Step)
		}
	default:
		return event, false, nil
	}
	return event, true, nil
}
