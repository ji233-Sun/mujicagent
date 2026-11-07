package harness

import (
	"encoding/json"
	"fmt"
	"slices"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/codex"
)

func (a *droidRPC) interaction(raw []byte) (any, error) {
	var v struct {
		ID     json.RawMessage
		Method string
		Params struct {
			Title, Hint, EmptyText, InitialSearch string
			Multi                                 bool
			PreselectedIDs                        []string
			Rows                                  []struct{ ID, Label, Detail string }
			Options                               []struct{ Label, Value string }
			ToolUses                              []struct {
				Details struct{ Type, ServerName, ActualToolName, ToolName string }
				ToolUse struct {
					ID, Name string
					Input    json.RawMessage
				}
			}
			Questions []struct {
				Index           int
				Topic, Question string
				Options         []string
				MultiSelect     bool
			}
		}
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return nil, err
	}
	r := &codex.Request{ID: v.ID, Cancellable: true}
	switch v.Method {
	case "droid.request_permission":
		r.Method = "item/commandExecution/requestApproval"
		r.AvailableDecisions = []json.RawMessage{json.RawMessage(`"cancel"`)}
		for _, option := range v.Params.Options {
			if option.Value == "proceed_once" {
				r.AvailableDecisions = append([]json.RawMessage{json.RawMessage(`"accept"`)}, r.AvailableDecisions...)
				break
			}
		}
		for _, t := range v.Params.ToolUses {
			if t.Details.Type == "mcp_tool" && t.ToolUse.ID != "" {
				name := t.Details.ActualToolName
				if name == "" {
					name = t.Details.ToolName
				}
				a.emit(AgentEvent{ID: t.ToolUse.ID, Tool: &ToolEvent{Kind: "mcp", Name: name, Server: t.Details.ServerName, Arguments: t.ToolUse.Input}})
			}
			r.Reason += t.ToolUse.Name + "\n" + string(t.ToolUse.Input) + "\n"
		}
	case "droid.ask_user":
		r.Method = "item/tool/requestUserInput"
		for _, q := range v.Params.Questions {
			question := codex.Question{ID: fmt.Sprint(q.Index), Header: q.Topic, Question: q.Question, IsOther: true, MultiSelect: q.MultiSelect}
			for _, label := range q.Options {
				question.Options = append(question.Options, struct {
					Label       string `json:"label"`
					Description string `json:"description"`
				}{Label: label})
			}
			r.Questions = append(r.Questions, question)
		}
	case "droid.pick_options":
		r.Method, r.Mode, r.Title, r.Message = "client/form", "form", v.Params.Title, v.Params.Hint
		r.Refreshable = true
		r.FilterOptions, r.Search = true, v.Params.InitialSearch
		r.InitialValues = map[string]string{}
		options := []codex.ElicitationOption{}
		for _, row := range v.Params.Rows {
			label := row.Label
			if row.Detail != "" {
				label += " · " + row.Detail
			}
			options = append(options, codex.ElicitationOption{Value: row.ID, Title: label})
		}
		field := codex.ElicitationField{Type: "string", Title: v.Params.Title, OneOf: options}
		valid := []string{}
		for _, id := range v.Params.PreselectedIDs {
			if slices.ContainsFunc(options, func(o codex.ElicitationOption) bool { return o.Value == id }) && !slices.Contains(valid, id) {
				valid = append(valid, id)
			}
		}
		if v.Params.Multi {
			itemField := field
			field = codex.ElicitationField{Type: "array", Title: v.Params.Title, Items: &itemField}
			data, _ := json.Marshal(valid)
			r.InitialValues["selection"] = string(data)
		} else if len(valid) > 0 {
			r.InitialValues["selection"] = valid[0]
		}
		form := codex.ElicitationForm{Type: "object", Properties: map[string]codex.ElicitationField{}, Required: []string{}}
		if len(options) > 0 {
			form.Properties["selection"] = field
			form.Required = []string{"selection"}
		} else {
			r.Message += "\n" + v.Params.EmptyText
		}
		schema, err := json.Marshal(form)
		if err != nil {
			return nil, err
		}
		r.RequestedSchema = schema
	default:
		return nil, nil
	}
	answer, err := ask(a.ctx, a.emit, r)
	if err != nil {
		return nil, err
	}
	if v.Method == "droid.pick_options" {
		var response struct {
			Action  string
			Content map[string]json.RawMessage
		}
		if err = json.Unmarshal(answer, &response); err != nil {
			return nil, err
		}
		result := map[string]any{"selectedIds": []string{}}
		if response.Action == "refresh" {
			result["refresh"] = true
			return result, nil
		}
		if response.Action != "accept" {
			result["cancelled"] = true
			return result, nil
		}
		selected := []string{}
		if raw := response.Content["selection"]; len(raw) > 0 {
			if v.Params.Multi {
				err = json.Unmarshal(raw, &selected)
			} else {
				var id string
				err = json.Unmarshal(raw, &id)
				selected = append(selected, id)
			}
			if err != nil {
				return nil, err
			}
		}
		for _, id := range selected {
			if !slices.ContainsFunc(v.Params.Rows, func(row struct{ ID, Label, Detail string }) bool { return row.ID == id }) {
				return nil, fmt.Errorf("invalid picker selection")
			}
		}
		result["selectedIds"] = selected
		return result, nil
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
		if response.Decision == "accept" && slices.ContainsFunc(v.Params.Options, func(o struct{ Label, Value string }) bool { return o.Value == "proceed_once" }) {
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
		answers = append(answers, map[string]any{"index": q.Index, "question": q.Question, "answer": strings.Join(value, ", ")})
	}
	return map[string]any{"answers": answers}, nil
}

func (a *droidRPC) update(raw json.RawMessage) error {
	var v struct {
		Type, MessageID, TextDelta, ToolUseID, ToolName, NewState, Reason string
		BlockIndex                                                        int
		Content, Update, Message                                          json.RawMessage
		IsError                                                           bool
		ToolUse                                                           struct {
			ID, Name, Namespace string
			Input               json.RawMessage
			ScriptExecution     struct{ RunID, OuterToolUseID string }
		}
	}
	if err := json.Unmarshal(raw, &v); err != nil {
		return err
	}
	e := AgentEvent{Status: "inProgress"}
	switch v.Type {
	case "create_message":
		// Script children are published as real assistant message blocks, without
		// tool_call notifications. Text/thinking already have their own deltas.
		var message struct {
			Role, Visibility    string
			HiddenFromUserViews bool
			Content             []json.RawMessage
		}
		if err := json.Unmarshal(v.Message, &message); err != nil {
			return err
		}
		if message.HiddenFromUserViews || message.Visibility == "llm_only" {
			return nil
		}
		for _, block := range message.Content {
			var content struct{ Type string }
			if err := json.Unmarshal(block, &content); err != nil {
				return err
			}
			var notification any
			switch {
			case message.Role == "assistant" && content.Type == "tool_use":
				notification = map[string]any{"type": "tool_call", "toolUse": block}
			case message.Role == "tool" && content.Type == "tool_result":
				notification = block
			default:
				continue
			}
			data, err := json.Marshal(notification)
			if err != nil {
				return err
			}
			if err := a.update(data); err != nil {
				return err
			}
		}
		return nil
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
		e.Tool = &ToolEvent{Name: v.ToolUse.Name, Arguments: v.ToolUse.Input, ParentID: v.ToolUse.ScriptExecution.OuterToolUseID}
		e.Tool.Kind = NativeToolKind(v.ToolUse.Name)
	case "tool_result":
		e.ID = v.ToolUseID
		e.Status = "completed"
		if v.IsError {
			e.Status = "failed"
		}
		e.Tool = &ToolEvent{Result: v.Content}
	case "tool_progress_update":
		e.ID = v.ToolUseID
		var progress struct {
			Text, FullOutput *string
			Type, Error      string
		}
		if err := json.Unmarshal(v.Update, &progress); err != nil {
			return err
		}
		if a.progress == nil {
			a.progress = map[string]string{}
		}
		var parameters map[string]json.RawMessage
		_ = json.Unmarshal(v.Update, &parameters)
		if progress.FullOutput != nil {
			a.progress[v.ToolUseID] = *progress.FullOutput
		} else if progress.Text != nil {
			a.progress[v.ToolUseID] += *progress.Text
		}
		if progress.Type == "error" {
			e.Status = "failed"
			if progress.Error != "" {
				a.progress[v.ToolUseID] = progress.Error
			}
		}
		result := v.Update
		if progress.FullOutput != nil || progress.Text != nil || progress.Error != "" {
			result, _ = json.Marshal(a.progress[v.ToolUseID])
		}
		e.Tool = &ToolEvent{Name: v.ToolName, Arguments: parameters["parameters"], Result: result}
		if v.ToolName != "" {
			e.Tool.Kind = NativeToolKind(v.ToolName)
		}
	case "tool_execution_phase_changed", "tool_execution_heartbeat":
		e.ID = v.ToolUseID
		e.Tool = &ToolEvent{Name: v.ToolName}
		if v.ToolName != "" {
			e.Tool.Kind = NativeToolKind(v.ToolName)
		}
	case "session_compacted":
		var summary struct{ SummaryID string }
		_ = json.Unmarshal(raw, &summary)
		if summary.SummaryID != "" {
			if a.summaries == nil {
				a.summaries = map[string]bool{}
			}
			if a.summaries[summary.SummaryID] {
				return nil
			}
			a.summaries[summary.SummaryID] = true
		}
		if !a.compacting && (a.compactions == 0 || a.compactionSummary != "") {
			a.compactions++
		}
		a.compacting = false
		a.compactionSummary = summary.SummaryID
		e.ID = fmt.Sprintf("compaction-%d", a.compactions)
		e.Kind = "contextCompaction"
		e.Status = "completed"
	case "droid_working_state_changed":
		if v.NewState != "compacting_conversation" {
			// The CLI returns to streaming on successful compaction even when
			// no summary was saved. Idle alone does not prove success.
			if a.compacting && v.NewState == "streaming_assistant_message" {
				a.finishCompaction("completed")
			}
			return nil
		}
		if !a.compacting {
			a.compactions++
		}
		a.compacting = true
		a.compactionSummary = ""
		e.ID = fmt.Sprintf("compaction-%d", a.compactions)
		e.Kind = "contextCompaction"
	case "agent_turn_completed":
		if v.Reason != "completed" {
			status := "failed"
			if v.Reason == "cancelled" {
				status = "interrupted"
			}
			a.finishCompaction(status)
			if v.Reason == "cancelled" {
				return fmt.Errorf("Droid: %w", ErrInterrupted)
			}
			return fmt.Errorf("Droid stopped: %s", v.Reason)
		}
		a.finishCompaction("unknown")
		a.done = true
		return nil
	case "error":
		if a.compacting {
			var reason string
			_ = json.Unmarshal(v.Message, &reason)
			a.emit(AgentEvent{ID: fmt.Sprintf("compaction-%d", a.compactions), Kind: "contextCompaction", Status: "failed", Text: reason})
			a.compacting = false
		}
		return fmt.Errorf("Droid reported a session error; check CLI configuration")
	default:
		return nil
	}
	a.emit(e)
	return nil
}

func (a *droidRPC) finishCompaction(status string) {
	if !a.compacting {
		return
	}
	a.compacting = false
	a.emit(AgentEvent{ID: fmt.Sprintf("compaction-%d", a.compactions), Kind: "contextCompaction", Status: status})
}
