package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"io"
	"os"
	"path/filepath"
	"strings"
)

type RunOptions struct {
	Driver, Binary, Cwd, Session, Model, Access, Prompt, Effort string
	Images                                                      []string
}
type AgentEvent struct {
	Text, Session string
	Delta         bool
}

func agentArgs(o RunOptions) ([]string, error) {
	var args []string
	switch o.Driver {
	case "pi":
		args = []string{"--mode", "json", "--print", "--session", o.Session}
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
		for _, path := range o.Images {
			args = append(args, "@"+path)
		}
	case "droid":
		args = []string{"exec", "--output-format", "acp"}
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
	if o.Driver == "droid" {
		return runDroid(ctx, o, args, emit)
	}

	cmd, err := codex.Command(ctx, o.Binary, args...)
	if err != nil {
		return err
	}
	cmd.Dir = o.Cwd
	if o.Driver == "pi" {
		cmd.Stdin = strings.NewReader(o.Prompt)
	}
	// Arbitrary diagnostics can contain credentials; do not persist stderr.
	cmd.Stderr = io.Discard
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	prepareProcess(cmd)
	if err = cmd.Start(); err != nil {
		return err
	}
	parser := agentParser{driver: o.Driver}
	scanner := bufio.NewScanner(stdout)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	for scanner.Scan() {
		if err = parser.line(scanner.Bytes(), emit); err != nil {
			break
		}
	}
	if err == nil {
		err = scanner.Err()
	}
	if err != nil {
		_ = cmd.Cancel()
	}
	waitErr := cmd.Wait()
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if err != nil {
		return err
	}
	if waitErr != nil {
		return fmt.Errorf("%s failed: %w; check CLI login and configuration", o.Driver, waitErr)
	}
	if !parser.complete {
		return fmt.Errorf("%s exited without a completed response", o.Driver)
	}
	return nil
}

type agentParser struct {
	driver   string
	complete bool
	text     string
}

func (p *agentParser) line(line []byte, emit func(AgentEvent)) error {
	var e struct {
		Type    string
		Message struct {
			Role, StopReason, ErrorMessage string
			Content                        json.RawMessage
		}
		AssistantMessageEvent struct{ Type, Delta string }
	}
	if err := json.Unmarshal(line, &e); err != nil {
		return fmt.Errorf("invalid %s protocol output: %w", p.driver, err)
	}

	switch e.Type {
	case "message_update":
		if e.AssistantMessageEvent.Type == "text_delta" {
			emit(AgentEvent{Text: e.AssistantMessageEvent.Delta, Delta: true})
		}
	case "message_end":
		if e.Message.Role != "assistant" {
			return nil
		}
		if e.Message.StopReason == "error" || e.Message.StopReason == "aborted" {
			return fmt.Errorf("Pi: %s %s", e.Message.StopReason, e.Message.ErrorMessage)
		}
		var content []struct{ Type, Text string }
		if err := json.Unmarshal(e.Message.Content, &content); err != nil {
			return err
		}
		var text strings.Builder
		for _, part := range content {
			if part.Type == "text" {
				text.WriteString(part.Text)
			}
		}
		p.text += text.String() + "\n"
		emit(AgentEvent{Text: strings.TrimSpace(p.text)})
	case "agent_end", "agent_settled":
		p.complete = true
	}
	return nil
}
