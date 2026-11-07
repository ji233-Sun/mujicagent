package controller

import (
	"context"
	"fmt"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
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
	if _, ok := harness.Lookup(driver); !ok {
		return false
	}
	old := c.State.Thread(c.State.Selected)
	if old == nil || c.Running[old.ID] || c.turnRuns[old.ID] != nil || c.QueueEdit(old.ID) != nil {
		return false
	}
	targetModel = strings.TrimSpace(targetModel)
	if len(old.Turns) == 0 && old.ServerID == "" && old.HandoffContext == "" {
		old.Harness, old.AgentModel, old.AgentAccess = driver, targetModel, c.State.Settings.AgentDefaultAccess(driver)
		old.AgentEffort = ""
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
	next.AgentAccess = c.State.Settings.AgentDefaultAccess(driver)
	next.HandoffFrom, next.HandoffContext = old.ID, history.String()
	next.Title, next.Draft = old.Title, old.Draft
	if err := c.saveState(); err != nil {
		c.State.Threads = c.State.Threads[1:]
		c.State.Selected = oldSelected
		c.ReportError(err.Error())
		return false
	}
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
			changed := false
			for _, t := range c.State.Threads {
				if t.Driver() == id && c.normalizeAgentEffort(t) {
					changed = true
				}
			}
			if changed {
				c.Save()
			}
		})
	}()
}

// A catalog applies only to its executable/workspace. Refresh also repairs old
// saved choices, so an unsupported level cannot survive into the next turn.
func (c *Controller) normalizeAgentEffort(t *model.Thread) bool {
	h := c.HarnessFor(t.Driver())
	cwd := t.Cwd
	if cwd == "" {
		cwd = c.Dir
	}
	if h.ModelsKey != c.State.Settings.AgentPath(t.Driver())+"\x00"+cwd {
		return false
	}
	for _, m := range h.Models {
		if m.ID == t.AgentModel {
			value := m.ResolveEffort(t.AgentEffort)
			if value != t.AgentEffort {
				t.AgentEffort = value
				if selection := &c.State.Settings.LastModelSelection; selection.Harness == t.Driver() && selection.Model == t.AgentModel {
					selection.Effort = m.ResolveEffort(selection.Effort)
				}
				return true
			}
			break
		}
	}
	return false
}
