package controller

import (
	"context"
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
func (c *Controller) CheckAgent(id string) {
	if id == "codex" {
		c.CheckHarness()
		return
	}
	if id != "pi" && id != "droid" {
		return
	}
	h := c.HarnessFor(id)
	if h.Checking {
		return
	}
	binary := c.State.Settings.AgentPath(id)
	h.Checking, h.Error = true, ""
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	h.cancel = cancel
	go func() {
		defer cancel()
		cmd, err := codex.Command(ctx, binary, "--version")
		var version []byte
		if err == nil {
			version, err = cmd.Output()
		}
		c.post(func() {
			h.Checking, h.cancel = false, nil
			if binary != c.State.Settings.AgentPath(id) {
				return
			}
			h.CheckedAt, h.Binary = time.Now(), binary
			h.Installation = harness.Installation{}
			if err != nil {
				h.Error = err.Error()
				return
			}
			h.Installation = harness.Installation{Path: cmd.Path, Version: strings.TrimSpace(string(version)), Method: "custom"}
		})
	}()
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
	if c.Running[t.ID] {
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
	if t.Driver() == "droid" && len(images) > 0 {
		c.ReportError("Droid headless image input is not supported; use Pi or Codex")
		return false
	}
	session := t.ServerID
	if t.Driver() == "pi" {
		session = filepath.Join(c.Dir, "sessions", "pi", t.ID+".jsonl")
	}
	o := harness.RunOptions{Driver: t.Driver(), Binary: c.State.Settings.AgentPath(t.Driver()), Cwd: t.Cwd, Session: session, Model: t.AgentModel, Access: t.AgentAccess, Prompt: plain.String(), Images: images}
	if len(t.Turns) == 0 && t.HandoffFrom == "" {
		t.Title = model.MessageTitle(text)
	}
	t.Turns = append(t.Turns, model.Turn{ID: model.ID(), Prompt: text, Status: "inProgress", Started: time.Now(), Items: []model.Item{{Kind: "agentMessage", Status: "inProgress"}}})
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
	go func() {
		defer cancel()
		err := harness.Run(ctx, o, func(e harness.AgentEvent) {
			c.post(func() {
				c.removeQueued(t, queuedID)
				item := &t.Turns[index].Items[0]
				if e.Delta {
					item.Text += e.Text
				} else {
					item.Text = e.Text
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
			turn.Items[0].Status = turn.Status
			c.Save()
			if err == nil {
				c.sendNext(t)
			}
		})
	}()
	return true
}
