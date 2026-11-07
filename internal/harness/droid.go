package harness

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

// droidACP owns one process and serializes requests while consuming notifications.
type droidACP struct {
	in               *json.Encoder
	out              *bufio.Scanner
	next             int
	session          string
	permissionDenied bool
	emit             func(AgentEvent)
}

func (a *droidACP) call(method string, params any, result any) error {
	a.next++
	id := a.next
	if err := a.in.Encode(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for a.out.Scan() {
		var e struct {
			ID     json.RawMessage
			Method string
			Result json.RawMessage
			Error  *struct{ Code int }
			Params struct {
				SessionID string `json:"sessionId"`
				Update    struct {
					SessionUpdate string
					Content       json.RawMessage
				}
			}
		}
		if err := json.Unmarshal(a.out.Bytes(), &e); err != nil {
			return fmt.Errorf("invalid Droid ACP response: %w", err)
		}
		if e.Method != "" && len(e.ID) > 0 {
			// Requests outside the selected auto-approval policy never gain permission.
			response := map[string]any{"jsonrpc": "2.0", "id": e.ID}
			if e.Method == "session/request_permission" {
				a.permissionDenied = true
				response["result"] = map[string]any{"outcome": map[string]string{"outcome": "cancelled"}}
			} else {
				response["error"] = map[string]any{"code": -32601, "message": "Unsupported client method"}
			}
			if err := a.in.Encode(response); err != nil {
				return err
			}
			continue
		}
		if method == "session/prompt" && e.Method == "session/update" && e.Params.SessionID == a.session && e.Params.Update.SessionUpdate == "agent_message_chunk" {
			var content struct{ Type, Text string }
			if err := json.Unmarshal(e.Params.Update.Content, &content); err != nil {
				return fmt.Errorf("invalid Droid message: %w", err)
			}
			if content.Type == "text" {
				a.emit(AgentEvent{Text: content.Text, Delta: true})
			}
		}
		if string(e.ID) != fmt.Sprint(id) {
			continue
		}
		if e.Error != nil {
			return fmt.Errorf("Droid ACP %s failed (code %d); check CLI configuration", method, e.Error.Code)
		}
		if result != nil {
			return json.Unmarshal(e.Result, result)
		}
		return nil
	}
	if err := a.out.Err(); err != nil {
		return err
	}
	return fmt.Errorf("Droid exited before %s completed", method)
}

func runDroid(ctx context.Context, o RunOptions, args []string, emit func(AgentEvent)) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	cmd, err := codex.Command(ctx, o.Binary, args...)
	if err != nil {
		return err
	}
	cmd.Dir, cmd.Stderr = o.Cwd, io.Discard
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	prepareProcess(cmd)
	if err = cmd.Start(); err != nil {
		return err
	}
	defer func() { cancel(); _ = cmd.Wait() }()
	a := droidACP{in: json.NewEncoder(in), out: bufio.NewScanner(out), emit: emit}
	a.out.Buffer(make([]byte, 4096), 8<<20)
	var init struct {
		AgentCapabilities struct {
			LoadSession        bool
			PromptCapabilities struct{ Image bool }
		}
	}
	if err = a.call("initialize", map[string]any{"protocolVersion": 1, "clientCapabilities": map[string]any{}}, &init); err != nil {
		return err
	}
	if len(o.Images) > 0 && !init.AgentCapabilities.PromptCapabilities.Image {
		return fmt.Errorf("Droid ACP does not support images; update the CLI")
	}
	params := map[string]any{"cwd": o.Cwd, "mcpServers": []any{}}
	method := "session/new"
	if o.Session != "" {
		if !init.AgentCapabilities.LoadSession {
			return fmt.Errorf("Droid ACP cannot restore this session; update the CLI")
		}
		method = "session/load"
		params["sessionId"] = o.Session
	}
	var session struct {
		SessionID string `json:"sessionId"`
	}
	if err = a.call(method, params, &session); err != nil {
		return err
	}
	a.session = session.SessionID
	if o.Session != "" {
		a.session = o.Session
	}
	if a.session == "" {
		return fmt.Errorf("Droid ACP returned no session ID")
	}
	mode := "normal"
	if o.Access != "" {
		mode = "auto-" + o.Access
	}
	if err = a.call("session/set_mode", map[string]any{"sessionId": a.session, "modeId": mode}, nil); err != nil {
		return err
	}
	if o.Model != "" {
		if err = a.call("session/set_model", map[string]any{"sessionId": a.session, "modelId": o.Model}, nil); err != nil {
			return err
		}
	}
	if o.Effort != "" {
		if err = a.call("session/set_config_option", map[string]any{"sessionId": a.session, "configId": "reasoning_effort", "value": o.Effort}, nil); err != nil {
			return err
		}
	}
	prompt, err := droidPrompt(o)
	if err != nil {
		return err
	}
	// Save the session before streaming so interrupted turns can still resume.
	emit(AgentEvent{Session: a.session, Delta: true})
	var done struct{ StopReason string }
	if err = a.call("session/prompt", map[string]any{"sessionId": a.session, "prompt": prompt}, &done); err != nil {
		return err
	}
	if a.permissionDenied {
		return fmt.Errorf("Droid operation blocked by the selected permissions; change permissions and retry if needed")
	}
	if done.StopReason != "end_turn" {
		return fmt.Errorf("Droid stopped: %s", done.StopReason)
	}
	return nil
}

func droidPrompt(o RunOptions) ([]any, error) {
	parts := []any{map[string]string{"type": "text", "text": o.Prompt}}
	for _, path := range o.Images {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		mime := http.DetectContentType(data)
		if !strings.HasPrefix(mime, "image/") {
			return nil, fmt.Errorf("unsupported image: %s", path)
		}
		parts = append(parts, map[string]string{"type": "image", "mimeType": mime, "data": base64.StdEncoding.EncodeToString(data)})
	}
	return parts, nil
}
