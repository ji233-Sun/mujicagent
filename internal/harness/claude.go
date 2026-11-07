package harness

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"slices"
	"sync"
)

// Claude Code's native SDK transport is NDJSON with a separate control channel.
// Keep the installed CLI's authentication, settings, tools and session storage.
func claudeArgs(o RunOptions) ([]string, error) {
	args := []string{"--print", "--verbose", "--input-format", "stream-json", "--output-format", "stream-json", "--include-partial-messages", "--replay-user-messages", "--permission-prompt-tool", "stdio"}
	switch o.Access {
	case "", "default":
		args = append(args, "--permission-mode", "default")
	case "acceptEdits":
		args = append(args, "--permission-mode", o.Access)
	case "bypassPermissions":
		args = append(args, "--permission-mode", o.Access, "--allow-dangerously-skip-permissions")
	default:
		return nil, fmt.Errorf("unsupported Claude Code permission: %s", o.Access)
	}
	if o.Session != "" {
		args = append(args, "--resume", o.Session)
	}
	if o.Model != "" {
		args = append(args, "--model", o.Model)
	}
	if o.Effort != "" {
		if !slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, o.Effort) {
			return nil, fmt.Errorf("unsupported Claude Code effort: %s", o.Effort)
		}
		args = append(args, "--effort", o.Effort)
	}
	return args, nil
}

func claudeUUID() string {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	b[6], b[8] = b[6]&0x0f|0x40, b[8]&0x3f|0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[:4], b[4:6], b[6:8], b[8:10], b[10:])
}

func claudeMessage(id string, content []any) map[string]any {
	return map[string]any{"type": "user", "uuid": id, "parent_tool_use_id": nil, "message": map[string]any{"role": "user", "content": content}}
}

func claudeParts(parts []any) []any {
	out := make([]any, 0, len(parts))
	for _, part := range parts {
		v := part.(map[string]string)
		if v["type"] == "image" {
			out = append(out, map[string]any{"type": "image", "source": map[string]string{"type": "base64", "media_type": v["mimeType"], "data": v["data"]}})
		} else {
			out = append(out, map[string]string{"type": "text", "text": v["text"]})
		}
	}
	return out
}

func claudeSteer(id, text string, parts []any) map[string]any {
	return claudeMessage(id, claudeParts(parts))
}

func claudeInitialize(hooks bool) map[string]any {
	request := map[string]any{"subtype": "initialize"}
	if hooks {
		request["hooks"] = map[string]any{
			"PreCompact":  []any{map[string]any{"hookCallbackIds": []string{"compact-start"}}},
			"PostCompact": []any{map[string]any{"hookCallbackIds": []string{"compact-end"}}},
			// AskUserQuestion must reach the user even in bypassPermissions mode.
			"PreToolUse": []any{map[string]any{"matcher": "AskUserQuestion", "hookCallbackIds": []string{"ask-user"}}},
		}
	}
	return map[string]any{"type": "control_request", "request_id": "initialize", "request": request}
}

type claudeEnvelope struct {
	Type, Subtype, UUID string
	SessionID           string `json:"session_id"`
	ParentID            string `json:"parent_tool_use_id"`
	RequestID           string `json:"request_id"`
	Request             json.RawMessage
	Response            struct {
		Subtype   string
		RequestID string `json:"request_id"`
		Response  json.RawMessage
	}
}

func startClaudeProcess(ctx context.Context, binary, cwd string, args []string) (*rpcProcess, error) {
	// Updates are explicitly previewed and confirmed through the shared installer.
	return startRPCProcessEnv(ctx, binary, cwd, args, map[string]string{"DISABLE_AUTOUPDATER": "1", "CLAUDE_CODE_SDK_READS_SESSION_STATE": "1"})
}

func discoverClaudeModels(ctx context.Context, binary, cwd string) ([]AgentModel, error) {
	args, _ := claudeArgs(RunOptions{})
	args = append(args, "--no-session-persistence")
	p, err := startClaudeProcess(ctx, binary, cwd, args)
	if err != nil {
		return nil, err
	}
	defer p.close()
	if err = p.input.Encode(claudeInitialize(false)); err != nil {
		return nil, err
	}
	for p.output.Scan() {
		var e claudeEnvelope
		if json.Unmarshal(p.output.Bytes(), &e) != nil {
			return nil, fmt.Errorf("invalid Claude Code model catalog response")
		}
		if e.Type != "control_response" || e.Response.RequestID != "initialize" {
			continue
		}
		if e.Response.Subtype != "success" {
			return nil, fmt.Errorf("Claude Code model discovery failed; check CLI configuration")
		}
		return parseClaudeModels(e.Response.Response)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, fmt.Errorf("Claude Code exited without a model catalog; update the CLI and retry")
}

func parseClaudeModels(raw json.RawMessage) ([]AgentModel, error) {
	var catalog struct {
		Models []struct {
			Value, ResolvedModel, DisplayName string
			SupportsEffort                    bool
			SupportedEffortLevels             []string
		}
	}
	if json.Unmarshal(raw, &catalog) != nil {
		return nil, fmt.Errorf("invalid Claude Code model catalog")
	}
	out := []AgentModel{}
	seen := map[string]bool{}
	for _, m := range catalog.Models {
		if m.Value == "" || m.Value == "default" {
			continue
		}
		id := m.ResolvedModel
		if id == "" {
			id = m.Value
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		value := AgentModel{ID: id, Name: m.DisplayName}
		if value.Name == "" {
			value.Name = id
		}
		if m.SupportsEffort {
			for _, level := range m.SupportedEffortLevels {
				if slices.Contains([]string{"low", "medium", "high", "xhigh", "max"}, level) && !slices.Contains(value.Efforts, level) {
					value.Efforts = append(value.Efforts, level)
				}
			}
		}
		out = append(out, value)
	}
	return out, nil
}

func runClaude(ctx context.Context, o RunOptions, args []string, emit func(AgentEvent)) error {
	parent := ctx
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p, err := startClaudeProcess(ctx, o.Binary, o.Cwd, args)
	if err != nil {
		return err
	}
	defer p.close()
	control := o.Control
	if control == nil {
		control = &Control{}
	}
	control.attach(p, "claude")
	defer control.close()
	// Requests can overlap while stdout must continue draining for cancellation.
	var emitMu sync.Mutex
	publish := func(e AgentEvent) { emitMu.Lock(); defer emitMu.Unlock(); emit(e) }
	parser := claudeParser{emit: publish}
	defer func() {
		status := "unknown"
		if parent.Err() != nil {
			status = "interrupted"
		}
		parser.finishCompaction(status, "")
	}()
	requests := map[string]context.CancelFunc{}
	var handlers sync.WaitGroup
	defer func() { cancel(); handlers.Wait() }()
	if err = control.send(claudeInitialize(true)); err != nil {
		return err
	}
	promptID, sent, accepted := claudeUUID(), false, false
	for p.output.Scan() {
		var e claudeEnvelope
		raw := slices.Clone(p.output.Bytes())
		if json.Unmarshal(raw, &e) != nil {
			return fmt.Errorf("invalid Claude Code protocol output")
		}
		switch e.Type {
		case "control_response":
			if e.Response.RequestID != "initialize" {
				continue
			}
			if sent || e.Response.Subtype != "success" {
				return fmt.Errorf("Claude Code initialization failed; check CLI configuration")
			}
			parts, err := imagePrompt(o)
			if err != nil {
				return err
			}
			if err = control.send(claudeMessage(promptID, claudeParts(parts))); err != nil {
				return err
			}
			sent = true
		case "control_cancel_request":
			if stop := requests[e.RequestID]; stop != nil {
				stop()
				delete(requests, e.RequestID)
			}
		case "control_request":
			var req claudeRequest
			if json.Unmarshal(e.Request, &req) != nil {
				return fmt.Errorf("invalid Claude Code control request")
			}
			if req.Subtype == "hook_callback" && (req.CallbackID == "compact-start" || req.CallbackID == "compact-end") {
				parser.compactHook(req)
				if err = control.send(claudeControlResponse(e.RequestID, map[string]any{}, "")); err != nil {
					return err
				}
				continue
			}
			child, stop := context.WithCancel(ctx)
			if previous := requests[e.RequestID]; previous != nil {
				previous()
			}
			requests[e.RequestID] = stop
			handlers.Add(1)
			go func(id string, req claudeRequest) {
				defer handlers.Done()
				defer stop()
				value, err := claudeInteraction(child, publish, req)
				if child.Err() != nil {
					return
				}
				message := ""
				if err != nil {
					message = "Unsupported or invalid client request"
				}
				_ = control.send(claudeControlResponse(id, value, message))
			}(e.RequestID, req)
		case "user":
			if e.UUID == promptID && sent && !accepted {
				accepted = true
				publish(AgentEvent{Accepted: true, Session: e.SessionID})
			} else {
				control.response(e.UUID, nil)
			}
			if accepted {
				if err = parser.line(raw); err != nil {
					return err
				}
			}
		default:
			if e.Type == "system" && e.Subtype == "init" && e.SessionID != "" {
				publish(AgentEvent{Session: e.SessionID})
			}
			if !accepted && e.Type != "result" {
				continue // Never append initialization/resume history to this turn.
			}
			if err = parser.line(raw); err != nil {
				return err
			}
			if parser.done() {
				if !accepted {
					return fmt.Errorf("Claude Code completed without acknowledging the message")
				}
				return nil
			}
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err = p.output.Err(); err != nil {
		return err
	}
	return fmt.Errorf("Claude Code exited before completing the request; check CLI login and configuration")
}

func claudeControlResponse(id string, value any, failure string) map[string]any {
	response := map[string]any{"subtype": "success", "request_id": id, "response": value}
	if failure != "" {
		response = map[string]any{"subtype": "error", "request_id": id, "error": failure}
	}
	return map[string]any{"type": "control_response", "response": response}
}

// Native task tools supplement TodoWrite in newer Claude Code versions.
type claudeTask struct{ Subject, Description, Status string }

type claudeParser struct {
	emit                         func(AgentEvent)
	message                      string
	messages                     map[string]string
	blocks                       map[string]*claudeBlock
	compactions                  int
	compacting, result           bool
	compactBoundary, compactPost bool
	sessionState                 string
	tasks                        map[string]bool
	todos                        map[string]claudeTask
	todoOrder                    []string
}

type claudeBlock struct {
	ID, Kind, Name, Parent, JSON string
}

func (p *claudeParser) done() bool {
	return p.result && len(p.tasks) == 0 && (p.sessionState == "" || p.sessionState == "idle")
}

func (p *claudeParser) finishCompaction(status, text string) {
	if !p.compacting {
		return
	}
	p.emit(AgentEvent{ID: fmt.Sprintf("claude-compact-%d", p.compactions), Kind: "contextCompaction", Status: status, Text: text, PreserveText: text == ""})
	p.compacting = false
}

func (p *claudeParser) startCompaction() {
	if p.compacting {
		return
	}
	p.compactions++
	p.compacting = true
	p.compactBoundary, p.compactPost = false, false
	p.emit(AgentEvent{ID: fmt.Sprintf("claude-compact-%d", p.compactions), Kind: "contextCompaction", Status: "inProgress"})
}

func (p *claudeParser) compactHook(req claudeRequest) {
	if req.CallbackID == "compact-start" {
		p.startCompaction()
	} else {
		var v struct {
			Summary string `json:"compact_summary"`
		}
		_ = json.Unmarshal(req.Input, &v)
		if !p.compacting && p.compactBoundary {
			p.emit(AgentEvent{ID: fmt.Sprintf("claude-compact-%d", p.compactions), Kind: "contextCompaction", Status: "completed", Text: v.Summary, PreserveText: v.Summary == ""})
			p.compactBoundary = false
			return
		}
		if !p.compacting {
			return
		}
		p.finishCompaction("completed", v.Summary)
		p.compactPost = true
	}
}

func (p *claudeParser) emitTool(id, name, parent string, args, result json.RawMessage, status string) {
	kind, server, tool := claudeTool(name)
	p.emit(AgentEvent{ID: id, Status: status, Tool: &ToolEvent{Name: tool, Kind: kind, Server: server, ParentID: parent, Arguments: args, Result: result}})
}

// Keep the shared plan component useful when the CLI uses task tools instead
// of TodoWrite. Snapshots are generated only from actual task inputs/results.
func (p *claudeParser) taskTool(name string, args, result json.RawMessage) {
	var v struct{ TaskID, Subject, Description, Status string }
	if json.Unmarshal(args, &v) != nil {
		return
	}
	if name == "TaskCreate" {
		var r struct{ Task struct{ ID string } }
		if json.Unmarshal(result, &r) == nil {
			v.TaskID = r.Task.ID
		}
	}
	if v.TaskID == "" {
		return
	}
	if p.todos == nil {
		p.todos = map[string]claudeTask{}
	}
	item, exists := p.todos[v.TaskID]
	if !exists {
		p.todoOrder = append(p.todoOrder, v.TaskID)
		item.Status = "pending"
	}
	if v.Subject != "" {
		item.Subject = v.Subject
	}
	if v.Description != "" {
		item.Description = v.Description
	}
	if v.Status != "" {
		item.Status = v.Status
	}
	p.todos[v.TaskID] = item
	var todos []map[string]string
	for _, id := range p.todoOrder {
		task := p.todos[id]
		if task.Status != "deleted" {
			todos = append(todos, map[string]string{"content": task.Subject, "status": task.Status})
		}
	}
	if len(todos) != 0 {
		raw, _ := json.Marshal(map[string]any{"todos": todos})
		p.emit(AgentEvent{ID: "claude-tasks", Status: "completed", Tool: &ToolEvent{Name: "TodoWrite", Kind: "plan", Arguments: raw}})
	}
}
