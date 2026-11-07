package harness

import (
	"bufio"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"io"
	"net/http"
	"os"
	"strings"
)

// droidRPC consumes the official full-duplex protocol, including client requests.
// History notifications during initialization are never emitted into the new turn.
type droidRPC struct {
	ctx         context.Context
	control     *Control
	out         *bufio.Scanner
	emit        func(AgentEvent)
	next        int
	session     string
	streaming   bool
	done        bool
	progress    map[string]string
	compactions int
	compacting  bool
	summaries   map[string]bool
}

func (a *droidRPC) call(method string, params any, result any) error {
	a.next++
	id := fmt.Sprintf("request-%d", a.next)
	if err := a.control.send(map[string]any{"jsonrpc": "2.0", "id": id, "method": method, "params": params}); err != nil {
		return err
	}
	for a.out.Scan() {
		var e struct {
			ID     json.RawMessage
			Method string
			Result json.RawMessage
			Error  *struct{ Code int }
		}
		if err := json.Unmarshal(a.out.Bytes(), &e); err != nil {
			return err
		}
		if e.Method != "" {
			if err := a.consume(a.out.Bytes()); err != nil {
				return err
			}
			continue
		}
		var responseID string
		_ = json.Unmarshal(e.ID, &responseID)
		var failure error
		if e.Error != nil {
			failure = fmt.Errorf("Droid %s failed (code %d)", method, e.Error.Code)
		}
		if responseID != id {
			a.control.response(responseID, failure)
			continue
		}
		if failure != nil {
			return failure
		}
		if result != nil {
			return json.Unmarshal(e.Result, result)
		}
		return nil
	}
	return a.readError()
}
func (a *droidRPC) readError() error {
	if a.ctx.Err() != nil {
		return a.ctx.Err()
	}
	if a.out.Err() != nil {
		return a.out.Err()
	}
	return fmt.Errorf("Droid exited before completing the request")
}
func (a *droidRPC) consume(raw []byte) error {
	var e struct {
		ID     json.RawMessage
		Method string
		Params struct {
			SessionID    string
			Notification json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &e); err != nil {
		return err
	}
	if len(e.ID) > 0 {
		response := map[string]any{"jsonrpc": "2.0", "id": e.ID}
		result, err := a.interaction(raw)
		if err != nil {
			return err
		}
		if result == nil {
			response["error"] = map[string]any{"code": -32601, "message": "Unsupported client method"}
		} else {
			response["result"] = result
		}
		return a.control.send(response)
	}
	if e.Method == "droid.session_notification" && a.streaming && (e.Params.SessionID == "" || e.Params.SessionID == a.session) {
		return a.update(e.Params.Notification)
	}
	return nil
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
	control := o.Control
	if control == nil {
		control = &Control{}
	}
	control.mu.Lock()
	control.in = json.NewEncoder(in)
	control.driver = "droid"
	control.mu.Unlock()
	defer control.close()
	a := droidRPC{ctx: ctx, control: control, out: bufio.NewScanner(out), emit: emit}
	defer func() {
		status := "failed"
		if ctx.Err() != nil {
			status = "interrupted"
		}
		a.finishCompaction(status)
	}()
	a.out.Buffer(make([]byte, 4096), 8<<20)
	method := "droid.initialize_session"
	params := map[string]any{"machineId": "mujicagent", "cwd": o.Cwd, "autoRejectPermissionRequests": false, "skipPermissionsUnsafe": false}
	if o.Session != "" {
		method = "droid.load_session"
		params = map[string]any{"sessionId": o.Session, "autoRejectPermissionRequests": false, "skipPermissionsUnsafe": false}
	}
	var session struct{ SessionID string }
	if err = a.call(method, params, &session); err != nil {
		return err
	}
	a.session = session.SessionID
	if o.Session != "" {
		a.session = o.Session
	}
	if a.session == "" {
		return fmt.Errorf("Droid returned no session ID")
	}
	access := o.Access
	if access == "" {
		access = "off"
	}
	settings := map[string]any{"interactionMode": "auto", "autonomyLevel": access}
	if o.Model != "" {
		settings["modelId"] = o.Model
	}
	if o.Effort != "" {
		settings["reasoningEffort"] = o.Effort
	}
	if err = a.call("droid.update_session_settings", settings, nil); err != nil {
		return err
	}
	parts, err := droidPrompt(o)
	if err != nil {
		return err
	}
	prompt := map[string]any{"text": o.Prompt}
	if len(parts) > 1 {
		images := []any{}
		for _, part := range parts[1:] {
			v := part.(map[string]string)
			images = append(images, map[string]string{"type": "base64", "data": v["data"], "mediaType": v["mimeType"]})
		}
		prompt["images"] = images
	}
	emit(AgentEvent{Session: a.session})
	a.streaming = true
	if err = a.call("droid.add_user_message", prompt, nil); err != nil {
		return err
	}
	emit(AgentEvent{Accepted: true})
	for !a.done && a.out.Scan() {
		var e struct {
			ID     json.RawMessage
			Method string
			Error  *struct{ Code int }
		}
		if err = json.Unmarshal(a.out.Bytes(), &e); err != nil {
			return err
		}
		if e.Method != "" {
			if err = a.consume(a.out.Bytes()); err != nil {
				return err
			}
		} else {
			var id string
			_ = json.Unmarshal(e.ID, &id)
			var failure error
			if e.Error != nil {
				failure = fmt.Errorf("Droid message rejected (code %d)", e.Error.Code)
			}
			control.response(id, failure)
		}
	}
	if !a.done {
		return a.readError()
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
