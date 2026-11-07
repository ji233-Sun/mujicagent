package harness

import (
	"encoding/json"
	"fmt"
	"github.com/ji233-Sun/mujicagent/internal/codex"
)

func (a *droidRPC) interaction(raw []byte) (any, error) {
	var v struct {
		ID     json.RawMessage
		Method string
		Params struct {
			ToolUses []struct {
				ToolUse struct {
					Name  string
					Input json.RawMessage
				}
			}
			Questions []struct {
				Index           int
				Topic, Question string
				Options         []string
			}
		}
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	r := &codex.Request{ID: v.ID}
	switch v.Method {
	case "droid.request_permission":
		r.Method = "item/commandExecution/requestApproval"
		r.AvailableDecisions = []json.RawMessage{json.RawMessage(`"accept"`), json.RawMessage(`"cancel"`)}
		for _, t := range v.Params.ToolUses {
			r.Reason += t.ToolUse.Name + "\n" + string(t.ToolUse.Input) + "\n"
		}
	case "droid.ask_user":
		r.Method = "item/tool/requestUserInput"
		for _, q := range v.Params.Questions {
			question := codex.Question{ID: fmt.Sprint(q.Index), Header: q.Topic, Question: q.Question, IsOther: true}
			for _, label := range q.Options {
				question.Options = append(question.Options, struct {
					Label       string `json:"label"`
					Description string `json:"description"`
				}{Label: label})
			}
			r.Questions = append(r.Questions, question)
		}
	default:
		return nil, nil
	}
	answer, err := ask(a.ctx, a.emit, r)
	if err != nil {
		return nil, err
	}
	var response struct {
		Decision string
		Answers  map[string]struct{ Answers []string }
	}
	if err = json.Unmarshal(answer, &response); err != nil {
		return nil, err
	}
	if v.Method == "droid.request_permission" {
		option := "cancel"
		if response.Decision == "accept" {
			option = "proceed_once"
		}
		return map[string]string{"selectedOption": option}, nil
	}
	answers := []any{}
	for _, q := range v.Params.Questions {
		value := response.Answers[fmt.Sprint(q.Index)].Answers
		if len(value) == 0 {
			return map[string]any{"cancelled": true, "answers": []any{}}, nil
		}
		answers = append(answers, map[string]any{"index": q.Index, "question": q.Question, "answer": value[0]})
	}
	return map[string]any{"answers": answers}, nil
}

func (a *droidRPC) update(raw json.RawMessage) error {
	var v struct {
		Type, MessageID, TextDelta, ToolUseID, ToolName, NewState, Reason string
		BlockIndex                                                        int
		Content, Update                                                   json.RawMessage
		IsError                                                           bool
		ToolUse                                                           struct {
			ID, Name, Namespace string
			Input               json.RawMessage
		}
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	e := AgentEvent{Status: "inProgress"}
	switch v.Type {
	case "assistant_text_delta", "thinking_text_delta", "assistant_text_complete", "thinking_text_complete":
		e.ID = fmt.Sprintf("%s-%d", v.MessageID, v.BlockIndex)
		e.Kind = "agentMessage"
		if v.Type == "thinking_text_delta" || v.Type == "thinking_text_complete" {
			e.Kind = "reasoning"
		}
		e.Text, e.Delta = v.TextDelta, true
		if v.Type == "assistant_text_complete" || v.Type == "thinking_text_complete" {
			e.Status = "completed"
		}
	case "tool_call":
		e.ID = v.ToolUse.ID
		e.Tool = &ToolEvent{Name: v.ToolUse.Name, Arguments: v.ToolUse.Input}
		switch v.ToolUse.Name {
		case "TodoWrite":
			e.Tool.Kind = "plan"
		case "Execute":
			e.Tool.Kind = "execute"
		case "Edit", "Create", "ApplyPatch":
			e.Tool.Kind = "edit"
		case "Grep", "Glob", "WebSearch":
			e.Tool.Kind = "search"
		default:
			e.Tool.Kind = "other"
		}
	case "tool_result":
		e.ID = v.ToolUseID
		e.Status = "completed"
		if v.IsError {
			e.Status = "failed"
		}
		e.Tool = &ToolEvent{Result: v.Content}
	case "tool_progress_update":
		e.ID = v.ToolUseID
		var progress struct{ Text, FullOutput *string }
		if err := json.Unmarshal(v.Update, &progress); err != nil {
			return err
		}
		if a.progress == nil {
			a.progress = map[string]string{}
		}
		if progress.FullOutput != nil {
			a.progress[v.ToolUseID] = *progress.FullOutput
		} else if progress.Text != nil {
			a.progress[v.ToolUseID] += *progress.Text
		}
		result := v.Update
		if progress.FullOutput != nil || progress.Text != nil {
			result, _ = json.Marshal(a.progress[v.ToolUseID])
		}
		e.Tool = &ToolEvent{Name: v.ToolName, Result: result}
	case "session_compacted":
		e.ID = "compaction"
		e.Kind = "contextCompaction"
		e.Status = "completed"
	case "droid_working_state_changed":
		if v.NewState != "compacting_conversation" {
			return nil
		}
		e.ID = "compaction"
		e.Kind = "contextCompaction"
	case "agent_turn_completed":
		if v.Reason != "completed" {
			return fmt.Errorf("Droid stopped: %s", v.Reason)
		}
		a.done = true
		return nil
	case "error":
		return fmt.Errorf("Droid reported a session error; check CLI configuration")
	default:
		return nil
	}
	a.emit(e)
	return nil
}
