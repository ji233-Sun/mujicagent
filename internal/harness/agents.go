package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

type RunOptions struct {
	Driver, Binary, Cwd, Session, Model, Access, Prompt, Effort string
	Control                                                     *Control
	Images                                                      []string
}
type AgentEvent struct {
	Text, Session    string
	ID, Kind, Status string
	Interaction      *Interaction
	Tool             *ToolEvent
	Delta            bool
	Accepted         bool
}

// Each external driver registers its complete session transport here. New
// adapters must emit the shared event contract, not a final-text-only result.
type agentAdapter struct {
	run   func(context.Context, RunOptions, []string, func(AgentEvent)) error
	steer func(string, string, []any) map[string]any
}

var agentAdapters = map[string]agentAdapter{
	"pi":    {runPi, piSteer},
	"droid": {runDroid, droidSteer},
}

func CanSteer(driver string) bool { return agentAdapters[driver].steer != nil }

func agentArgs(o RunOptions) ([]string, error) {
	var args []string
	switch o.Driver {
	case "pi":
		args = []string{"--mode", "rpc", "--session", o.Session}
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

	case "droid":
		args = []string{"exec", "--input-format", "stream-jsonrpc", "--output-format", "stream-jsonrpc"}
		if o.Model != "" {
			args = append(args, "--model", o.Model)
		}
		if o.Effort != "" {
			args = append(args, "--reasoning-effort", o.Effort)
		}
		switch o.Access {
		case "", "low", "medium", "high":
		default:
			return nil, fmt.Errorf("unsupported Droid autonomy: %s", o.Access)
		}
	default:
		return nil, fmt.Errorf("unsupported agent: %s", o.Driver)
	}
	return args, nil
}

// Run continuously consumes protocol output; credentials remain owned by the CLI.
func Run(ctx context.Context, o RunOptions, emit func(AgentEvent)) error {
	args, err := agentArgs(o)
	if err != nil {
		return err
	}
	if o.Driver == "pi" {
		if err := os.MkdirAll(filepath.Dir(o.Session), 0700); err != nil {
			return err
		}
	}
	run := agentAdapters[o.Driver].run
	if run == nil {
		return fmt.Errorf("unsupported agent: %s", o.Driver)
	}
	return run(ctx, o, args, emit)
}

type agentParser struct {
	driver  string
	message int
}

func (p *agentParser) line(line []byte, emit func(AgentEvent)) error {
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
		if e.Message.StopReason == "error" || e.Message.StopReason == "aborted" {
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
