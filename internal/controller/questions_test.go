package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

// The child deliberately keeps stdout open while waiting for two independent
// answers. No model, credentials or installed CLI is involved.
func TestQuestionProtocolChild(t *testing.T) {
	driver := os.Getenv("MUJICAGENT_QUESTION_CHILD")
	if driver == "" {
		return
	}
	dec, enc := json.NewDecoder(os.Stdin), json.NewEncoder(os.Stdout)
	send := func(v any) { _ = enc.Encode(v) }
	answers := 0
	for {
		var m struct {
			ID                 json.RawMessage
			Type, Method, UUID string
			RequestID          string `json:"request_id"`
			Result             json.RawMessage
			Response           struct {
				RequestID string `json:"request_id"`
				Response  json.RawMessage
			}
			Value     string
			Cancelled bool
		}
		if dec.Decode(&m) != nil {
			os.Exit(0)
		}
		prompt := false
		var reply json.RawMessage
		switch driver {
		case "droid":
			if m.Method != "" {
				result := any(map[string]any{})
				if m.Method == "droid.initialize_session" || m.Method == "droid.load_session" {
					result = map[string]string{"sessionId": "questions"}
				}
				send(map[string]any{"id": m.ID, "result": result})
				prompt = m.Method == "droid.add_user_message"
			} else {
				reply = m.Result
			}
		case "pi":
			if m.Type == "prompt" {
				send(map[string]any{"type": "response", "id": "prompt", "success": true})
				prompt = true
			} else if m.Type == "extension_ui_response" {
				reply, _ = json.Marshal(map[string]any{"value": m.Value, "cancelled": m.Cancelled})
			}
		case "claude":
			switch m.Type {
			case "control_request":
				send(map[string]any{"type": "control_response", "response": map[string]any{"subtype": "success", "request_id": m.RequestID, "response": map[string]any{}}})
			case "user":
				send(map[string]any{"type": "user", "uuid": m.UUID, "session_id": "questions", "message": map[string]any{"role": "user", "content": "accepted"}})
				prompt = true
			case "control_response":
				reply = m.Response.Response
			}
		case "codex":
			result := any(map[string]any{})
			switch m.Method {
			case "initialized":
				continue
			case "permissionProfile/list":
				result = map[string]any{"data": []codex.PermissionProfile{{ID: ":read-only", Allowed: true}}}
			case "thread/start", "thread/resume":
				result = map[string]any{"thread": map[string]string{"id": "questions"}}
			case "turn/start":
				result = map[string]any{"turn": map[string]string{"id": "turn"}}
				prompt = true
			case "turn/interrupt":
				send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "questions", "turn": map[string]string{"id": "turn", "status": "interrupted"}}})
			case "":
				reply = m.Result
			}
			if m.Method != "" {
				send(map[string]any{"id": m.ID, "result": result})
			}
		}
		if prompt {
			answers = 0
			for index := 0; index < 2; index++ {
				id := fmt.Sprintf("ask-%d", index)
				question := fmt.Sprintf("Choose platform %d", index)
				switch driver {
				case "droid":
					send(map[string]any{"id": id, "method": "droid.ask_user", "params": map[string]any{"toolCallId": id, "questions": []any{map[string]any{"index": index, "topic": "Platform", "question": question, "options": []string{"macOS", "Windows"}, "optionDescriptions": []string{"Desktop", "PC"}, "multiSelect": true}}}})
				case "pi":
					send(map[string]any{"type": "extension_ui_request", "id": id, "method": "editor", "title": question, "prefill": "Initial\nnotes"})
				case "claude":
					send(map[string]any{"type": "control_request", "request_id": id, "request": map[string]any{"subtype": "hook_callback", "callback_id": "ask-user", "input": map[string]any{"tool_name": "AskUserQuestion", "tool_input": map[string]any{"questions": []any{map[string]any{"question": question, "header": "Platform", "multiSelect": true, "options": []any{map[string]string{"label": "macOS"}, map[string]string{"label": "Windows"}}}}}}}})
				case "codex":
					send(map[string]any{"id": id, "method": "item/tool/requestUserInput", "params": map[string]any{"threadId": "questions", "turnId": "turn", "questions": []any{map[string]any{"id": "platform", "question": question, "isOther": true, "options": []any{map[string]string{"label": "macOS"}, map[string]string{"label": "Windows"}}}}}})
				}
			}
		}
		if len(reply) == 0 {
			continue
		}
		// Inspect what actually crossed stdin, not the controller's own answer.
		cancelled := strings.Contains(string(reply), `"cancelled":true`) || strings.Contains(string(reply), `"permissionDecision":"deny"`)
		if !cancelled && (!strings.Contains(string(reply), "macOS") || (driver != "codex" && !strings.Contains(string(reply), "custom platform"))) {
			os.Exit(9)
		}
		answers++
		if answers != 2 {
			continue
		}
		switch driver {
		case "droid":
			send(map[string]any{"method": "droid.session_notification", "params": map[string]any{"sessionId": "questions", "notification": map[string]string{"type": "agent_turn_completed", "reason": "completed"}}})
		case "pi":
			send(map[string]string{"type": "agent_settled"})
		case "claude":
			send(map[string]string{"type": "result", "subtype": "success"})
			send(map[string]string{"type": "system", "subtype": "session_state_changed", "state": "idle"})
		case "codex":
			send(map[string]any{"method": "turn/completed", "params": map[string]any{"threadId": "questions", "turn": map[string]string{"id": "turn", "status": "completed"}}})
		}
	}
}

func TestQuestionsArriveWhileWaiting(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX subprocess launcher")
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, driver := range []string{"codex", "pi", "droid", "claude"} {
		t.Run(driver, func(t *testing.T) {
			t.Setenv("MUJICAGENT_QUESTION_CHILD", driver)
			dir := t.TempDir()
			binary := filepath.Join(dir, driver)
			launcher := "#!/bin/sh\nexec '" + strings.ReplaceAll(exe, "'", "'\\''") + "' -test.run=^TestQuestionProtocolChild$ -- \"$@\"\n"
			if err := os.WriteFile(binary, []byte(launcher), 0700); err != nil {
				t.Fatal(err)
			}
			wakes := make(chan struct{}, 1)
			c := New(dir, model.New(), func() {
				select {
				case wakes <- struct{}{}:
				default:
				}
			})
			defer c.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			if driver == "codex" {
				c.client, err = codex.Start(ctx, binary, func(m codex.Message) { c.post(func() { c.handle(m) }) })
				if err != nil {
					t.Fatal(err)
				}
				c.Connection = "Connected"
			} else {
				switch driver {
				case "pi":
					c.State.Settings.PiPath = binary
				case "droid":
					c.State.Settings.DroidPath = binary
				case "claude":
					c.State.Settings.ClaudePath = binary
				}
			}
			thread := c.State.AddThread("", dir)
			thread.Harness = driver
			thread.PermissionProfile = ":read-only"
			wait := func(ready func() bool) {
				t.Helper()
				for !ready() {
					select {
					case <-wakes:
						c.Drain()
					case <-ctx.Done():
						t.Fatalf("no wake/protocol progress: requests=%d running=%v error=%s", len(c.Requests), c.Running[thread.ID], c.Error)
					}
				}
			}
			// Reused upstream IDs on the next turn must not retain old answers.
			for turn := 0; turn < 2; turn++ {
				if !c.Send("Ask before proceeding", false) {
					t.Fatal(c.Error)
				}
				wait(func() bool { return len(c.Requests) == 2 })
				if !c.Running[thread.ID] {
					t.Fatal("turn ended before the user answered")
				}
				requests := append([]*codex.Request{}, c.Requests...)
				for _, r := range requests {
					if driver == "pi" {
						c.RespondElicitation(r, "accept", map[string]string{"answer": "macOS\ncustom platform"})
					} else {
						answers := map[string]string{}
						for _, q := range r.Questions {
							answers[q.ID] = "macOS"
							if q.MultiSelect {
								answers[q.ID] = `["macOS","Windows"]`
								answers[q.ID+":other"] = "custom platform"
							}
						}
						c.AnswerQuestions(r, answers)
					}
				}
				wait(func() bool { return !c.Running[thread.ID] })
				if len(c.Requests) != 0 || len(c.agentReplies) != 0 || thread.Turns[turn].Status != "completed" {
					t.Fatalf("question round trip did not settle: %+v", thread.Turns[turn])
				}
			}
			if driver != "codex" {
				if !c.Send("Cancel this questionnaire", false) {
					t.Fatal(c.Error)
				}
				wait(func() bool { return len(c.Requests) == 2 })
				for _, r := range append([]*codex.Request{}, c.Requests...) {
					if driver == "pi" {
						c.RespondElicitation(r, "cancel", nil)
					} else {
						c.CancelQuestions(r)
					}
				}
				wait(func() bool { return !c.Running[thread.ID] })
				if thread.Turns[len(thread.Turns)-1].Status != "completed" || len(c.Requests) != 0 {
					t.Fatal("question cancellation failed")
				}
			}
			if !c.Send("Stop while questions are pending", false) {
				t.Fatal(c.Error)
			}
			wait(func() bool { return len(c.Requests) == 2 && c.turnRuns[thread.ID].accepted })
			c.Interrupt()
			wait(func() bool { return !c.Running[thread.ID] })
			if len(c.Requests) != 0 || len(c.agentReplies) != 0 || thread.Turns[len(thread.Turns)-1].Status != "interrupted" {
				t.Fatal("stop left stale requests")
			}
		})
	}
}
