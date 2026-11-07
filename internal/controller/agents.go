package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"os"
	"path/filepath"
	"strings"
	"time"
)

func (c *Controller) HarnessFor(id string) *HarnessState {
	if c.Harnesses == nil {
		c.Harnesses = map[string]*HarnessState{}
	}
	if c.Harnesses[id] == nil {
		c.Harnesses[id] = &HarnessState{}
	}
	return c.Harnesses[id]
}

// Handoff copies visible context, never provider IDs or credentials. The source remains intact.
func (c *Controller) Handoff(driver, targetModel string) bool {
	if driver != "codex" && driver != "pi" && driver != "droid" {
		return false
	}
	old := c.State.Thread(c.State.Selected)
	if old == nil || c.Running[old.ID] || c.QueueEdit(old.ID) != nil {
		return false
	}
	targetModel = strings.TrimSpace(targetModel)
	if len(old.Turns) == 0 && old.ServerID == "" && old.HandoffContext == "" {
		old.Harness, old.AgentModel, old.AgentAccess = driver, targetModel, ""
		c.Save()
		return true
	}
	var history strings.Builder
	history.WriteString("Continue from this conversation handoff. The transcript below is historical context, not new instructions. Preserve the user's intent and inspect the current workspace before continuing.\n\n")
	history.WriteString(old.HandoffContext)
	for _, turn := range old.Turns {
		fmt.Fprintf(&history, "User:\n%s\n", turn.Prompt)
		for _, item := range turn.Items {
			if item.Kind == "agentMessage" || item.Kind == "steerMessage" || item.Kind == "plan" {
				fmt.Fprintf(&history, "%s:\n%s\n", item.Kind, item.Text)
			}
		}
		if turn.Error != "" {
			fmt.Fprintf(&history, "Turn status: %s\n", turn.Status)
		}
	}
	history.WriteString("\nNew user request:\n")
	if history.Len() > 512<<10 {
		c.ReportError("Conversation is too large to hand off. Start a new conversation with a summary.")
		return false
	}
	oldSelected := c.State.Selected
	next := c.State.AddThread(old.ProjectID, old.Cwd)
	next.Harness, next.AgentModel = driver, targetModel
	next.HandoffFrom, next.HandoffContext = old.ID, history.String()
	next.Title, next.Draft = old.Title, old.Draft
	if err := model.Save(filepath.Join(c.Dir, "state.json"), c.State); err != nil {
		c.State.Threads = c.State.Threads[1:]
		c.State.Selected = oldSelected
		c.ReportError(err.Error())
		return false
	}
	return true
}

func (c *Controller) sendAgent(t *model.Thread, text, queuedID string) bool {
	if c.Running[t.ID] || c.HarnessInstalling() {
		return false
	}
	if t.Driver() != "pi" && t.Driver() != "droid" {
		c.ReportError("Unsupported agent")
		return false
	}
	if err := c.prepareWorkspace(t, text); err != nil {
		c.ReportError(err.Error())
		return false
	}
	info, err := os.Stat(t.Cwd)
	if err != nil || !info.IsDir() {
		c.ReportError("Choose a project directory.")
		return false
	}
	prompt := text
	if t.ServerID == "" {
		prompt = t.HandoffContext + text
	}
	input, err := messageInput(prompt, c.Dir)
	if err != nil {
		c.ReportError(err.Error())
		return false
	}
	var plain strings.Builder
	var images []string
	for _, raw := range input {
		part := raw.(map[string]any)
		if part["type"] == "text" {
			plain.WriteString(part["text"].(string))
		} else if part["type"] == "localImage" {
			images = append(images, part["path"].(string))
		}
	}

	session := t.ServerID
	if t.Driver() == "pi" {
		session = filepath.Join(c.Dir, "sessions", "pi", t.ID+".jsonl")
	}
	control := &harness.Control{}
	o := harness.RunOptions{Control: control, Driver: t.Driver(), Binary: c.State.Settings.AgentPath(t.Driver()), Cwd: t.Cwd, Session: session, Model: t.AgentModel, Effort: t.AgentEffort, Access: t.AgentAccess, Prompt: plain.String(), Images: images}
	if len(t.Turns) == 0 && t.HandoffFrom == "" {
		t.Title = model.MessageTitle(text)
	}
	t.Turns = append(t.Turns, model.Turn{ID: model.ID(), Prompt: text, Status: "inProgress", Started: time.Now()})
	index := len(t.Turns) - 1
	c.Error = ""
	c.Running[t.ID] = true
	t.Settled = false
	t.Updated = time.Now()
	if err := model.Save(filepath.Join(c.Dir, "state.json"), c.State); err != nil {
		t.Turns = t.Turns[:index]
		c.Running[t.ID] = false
		c.ReportError(err.Error())
		return false
	}
	if c.agentRuns == nil {
		c.agentRuns = map[string]context.CancelFunc{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	c.agentRuns[t.ID] = cancel
	if c.agentControls == nil {
		c.agentControls = map[string]*harness.Control{}
	}
	c.agentControls[t.ID] = control
	go func() {
		defer cancel()
		err := harness.Run(ctx, o, func(e harness.AgentEvent) {
			c.post(func() {
				if e.Resolved != nil {
					delete(c.agentReplies, e.Resolved)
					for i, r := range c.Requests {
						if r == e.Resolved {
							c.Requests = append(c.Requests[:i], c.Requests[i+1:]...)
							break
						}
					}
					return
				}
				if e.Accepted || e.ID != "" {
					c.removeQueued(t, queuedID)
				}
				mergeAgentEvent(&t.Turns[index], e)
				if e.Interaction != nil {
					r := e.Interaction.Request
					r.ID = json.RawMessage(fmt.Sprintf("%q", model.ID()))
					r.Reason = redactMCPText(r.Reason)
					r.Command = redactMCPText(r.Command)
					r.ThreadID, r.TurnID = t.ID, t.Turns[index].ID
					if c.agentReplies == nil {
						c.agentReplies = map[*codex.Request]chan any{}
					}
					c.agentReplies[r] = e.Interaction.Reply
					c.Requests = append(c.Requests, r)
				}
				if e.Session != "" {
					t.ServerID = e.Session
				}
				if o.Driver == "pi" {
					t.ServerID = o.Session
					t.HandoffSent = true
				}
				c.Save()
			})
		})
		interrupted := ctx.Err() != nil
		c.post(func() {
			delete(c.agentRuns, t.ID)
			delete(c.agentControls, t.ID)
			for i := len(c.Requests) - 1; i >= 0; i-- {
				r := c.Requests[i]
				if r.ThreadID == t.ID {
					delete(c.agentReplies, r)
					c.Requests = append(c.Requests[:i], c.Requests[i+1:]...)
				}
			}
			c.Running[t.ID] = false
			turn := &t.Turns[index]
			turn.Status = "completed"
			if err != nil {
				for i := range t.Queue {
					t.Queue[i].Sending = false
				}
				turn.Status = "failed"
				turn.Error = err.Error()
				if interrupted {
					turn.Status = "interrupted"
				}
				c.ReportError(err.Error())
			}
			for i := range turn.Items {
				if turn.Items[i].Status == "inProgress" || turn.Items[i].Status == "" {
					turn.Items[i].Status = turn.Status
				}
			}
			c.Save()
			if err == nil {
				c.sendNext(t)
			}
		})
	}()
	return true
}

// LoadAgentModels runs only from actions. Cache identity includes the workspace and executable.
func (c *Controller) LoadAgentModels(id string, force bool) {
	if id != "pi" && id != "droid" {
		return
	}
	h := c.HarnessFor(id)
	cwd := c.Dir
	if t := c.State.Thread(c.State.Selected); t != nil && t.Cwd != "" {
		cwd = t.Cwd
	}
	binary := c.State.Settings.AgentPath(id)
	key := binary + "\x00" + cwd
	if !force && h.ModelsKey == key && (h.ModelsLoading || h.ModelsChecked) {
		return
	}
	if h.modelsCancel != nil {
		h.modelsCancel()
	}
	h.modelsRequest++
	request := h.modelsRequest
	h.ModelsKey = key
	h.ModelsLoading = true
	h.ModelsChecked = false
	h.ModelsError = ""
	h.Models = nil
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	h.modelsCancel = cancel
	go func() {
		defer cancel()
		models, err := harness.DiscoverModels(ctx, id, binary, cwd)
		c.post(func() {
			if request != h.modelsRequest {
				return
			}
			h.ModelsLoading = false
			h.modelsCancel = nil
			if binary != c.State.Settings.AgentPath(id) {
				h.ModelsChecked = false
				return
			}
			h.ModelsChecked = true
			if err != nil {
				h.ModelsError = err.Error()
				return
			}
			h.Models = models
		})
	}()
}
