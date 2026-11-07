package harness

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

func runPi(ctx context.Context, o RunOptions, args []string, emit func(AgentEvent)) error {
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
	control.driver = "pi"
	control.mu.Unlock()
	defer control.close()
	parts, err := droidPrompt(o)
	if err != nil {
		return err
	}
	prompt := map[string]any{"id": "prompt", "type": "prompt", "message": o.Prompt}
	if len(parts) > 1 {
		prompt["images"] = parts[1:]
	}
	if err = control.send(prompt); err != nil {
		return err
	}
	scanner := bufio.NewScanner(out)
	scanner.Buffer(make([]byte, 4096), 8<<20)
	p := agentParser{driver: "pi"}
	for scanner.Scan() {
		var e struct {
			Type, ID, Command string
			Success, Aborted  bool
			Data              struct{ Disposition string }
		}
		if err = json.Unmarshal(scanner.Bytes(), &e); err != nil {
			return err
		}
		if e.Type == "response" {
			var failure error
			if !e.Success {
				failure = fmt.Errorf("Pi %s failed; check CLI configuration", e.Command)
			}
			if e.ID == "prompt" && failure != nil {
				return failure
			}
			if e.ID == "prompt" && failure == nil {
				emit(AgentEvent{Accepted: true, Session: o.Session})
			}
			if e.ID == "prompt" && e.Data.Disposition == "handled" {
				return nil
			}
			control.response(e.ID, failure)
			continue
		}
		if e.Type == "extension_ui_request" {
			if err = piInteraction(ctx, scanner.Bytes(), control, emit); err != nil {
				return err
			}
			continue
		}
		if err = p.line(scanner.Bytes(), emit); err != nil {
			return err
		}
		if e.Type == "agent_settled" {
			if e.Aborted {
				return fmt.Errorf("Pi aborted")
			}
			return nil
		}
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if scanner.Err() != nil {
		return scanner.Err()
	}
	return fmt.Errorf("Pi exited without a settled response")
}

func piInteraction(ctx context.Context, raw []byte, control *Control, emit func(AgentEvent)) error {
	var v struct {
		ID, Method, Title, Message string
		Options                    []string
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	r := &codex.Request{ID: json.RawMessage(fmt.Sprintf("%q", v.ID)), Reason: v.Title + "\n" + v.Message}
	switch v.Method {
	case "confirm":
		r.Method = "item/commandExecution/requestApproval"
		r.AvailableDecisions = []json.RawMessage{json.RawMessage(`"accept"`), json.RawMessage(`"decline"`), json.RawMessage(`"cancel"`)}
	case "select", "input", "editor":
		r.Method = "item/tool/requestUserInput"
		q := codex.Question{ID: "answer", Header: v.Title, Question: v.Title + "\n" + v.Message, IsOther: v.Method != "select"}
		for _, label := range v.Options {
			q.Options = append(q.Options, struct {
				Label       string `json:"label"`
				Description string `json:"description"`
			}{Label: label})
		}
		r.Questions = []codex.Question{q}
	default:
		return nil // Status/widget notifications are not blocking dialogs.
	}
	answer, err := ask(ctx, emit, r)
	if err != nil {
		return err
	}
	var response struct {
		Decision string
		Answers  map[string]struct{ Answers []string }
	}
	if err = json.Unmarshal(answer, &response); err != nil {
		return err
	}
	result := map[string]any{"type": "extension_ui_response", "id": v.ID}
	if v.Method == "confirm" {
		result["confirmed"] = response.Decision == "accept"
	} else if a := response.Answers["answer"].Answers; len(a) > 0 {
		result["value"] = a[0]
	} else {
		result["cancelled"] = true
	}
	return control.send(result)
}
