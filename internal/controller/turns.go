package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

// One record owns submission, acceptance and completion for every driver.
// Pointer identity rejects callbacks from an earlier run on the same thread.
type turnRun struct {
	session            *harness.Session
	cancel             context.CancelFunc
	index              int
	queuedID           string
	accepted, finished bool
}

func (r *turnRun) close() {
	if r.cancel != nil {
		r.cancel()
	}
	if r.session != nil {
		r.session.Close()
	}
}

func (c *Controller) sendThread(t *model.Thread, text string, plan bool, queuedID string) bool {
	if t == nil || c.Running[t.ID] || c.turnRuns[t.ID] != nil || c.HarnessInstalling() || c.permissionUpdates[t.ID] {
		return false
	}
	driver, ok := harness.Lookup(t.Driver())
	if !ok {
		c.ReportError("Unsupported agent: " + t.Driver())
		return false
	}
	if driver.Capabilities.PersistentConnection && (c.client == nil || c.Connection != "Connected") {
		c.ReportError("Connect Codex in Settings before sending.")
		return false
	}
	if err := c.prepareWorkspace(t, text); err != nil {
		c.ReportError(err.Error())
		return false
	}
	info, err := os.Stat(t.Cwd)
	if err == nil && !info.IsDir() {
		err = fmt.Errorf("project is not a directory: %s", t.Cwd)
	}
	if err != nil {
		c.ReportError(err.Error())
		return false
	}
	c.normalizeAgentEffort(t)
	prompt := text
	if !t.HandoffSent {
		prompt = t.HandoffContext + text
	}
	o := harness.RunOptions{
		Driver: driver.ID, Binary: c.State.Settings.AgentPath(driver.ID), Cwd: t.Cwd,
		Session: driver.SessionID(c.Dir, t.ID, t.ServerID), Model: t.AgentModel,
		Effort: t.AgentEffort, Access: t.AgentAccess, Plan: plan,
	}
	turnID := model.ID()
	if driver.Capabilities.PersistentConnection {
		o.Client, o.Loaded = c.client, c.loaded[t.ID]
		if o.Model == "" {
			o.Model = c.State.Settings.Model
		}
		o.Effort, o.ServiceTier = c.State.Settings.Effort, c.serviceTierFor(o.Model)
		o.Access = t.PermissionProfile
		if o.Access == "" {
			o.Access = c.State.Settings.DefaultPermissionProfile
		}
		turnID = "" // The native turn identity arrives with its acknowledgment.
	}
	title, settled, updated := t.Title, t.Settled, t.Updated
	if len(t.Turns) == 0 && t.HandoffFrom == "" {
		t.Title = model.MessageTitle(text)
	}
	t.Settled, t.Updated = false, time.Now()
	t.Turns = append(t.Turns, model.Turn{ID: turnID, Prompt: text, Status: "inProgress", Started: time.Now()})
	index := len(t.Turns) - 1
	c.Running[t.ID] = true
	c.Error = ""
	if err := c.saveState(); err != nil {
		t.Turns = t.Turns[:index]
		t.Title, t.Settled, t.Updated = title, settled, updated
		c.Running[t.ID] = false
		c.ReportError(err.Error())
		return false
	}
	c.turnPermissions[t.ID] = o.Access
	if c.turnRuns == nil {
		c.turnRuns = map[string]*turnRun{}
	}
	ctx, cancel := context.WithCancel(context.Background())
	run := &turnRun{cancel: cancel, index: index, queuedID: queuedID}
	c.turnRuns[t.ID] = run
	emit := func(e harness.AgentEvent) {
		c.post(func() {
			if c.turnRuns[t.ID] == run {
				c.applyTurnEvent(t, index, e)
			}
		})
	}
	dir := c.Dir
	go func() {
		// Annotation export and attachment I/O must not hold the UI thread.
		input, err := messageInput(prompt, dir)
		var session *harness.Session
		if err == nil {
			o.Input = input
			session, err = harness.NewSession(ctx, o)
		}
		if err != nil || ctx.Err() != nil {
			if session != nil {
				session.Close()
			}
			result := &harness.TurnResult{Status: "interrupted"}
			if ctx.Err() == nil {
				result.Status, result.Error = "failed", err.Error()
			}
			emit(harness.AgentEvent{End: result})
			return
		}
		c.post(func() {
			if c.turnRuns[t.ID] != run {
				session.Close()
				return
			}
			run.session = session
			go session.Run(emit)
		})
	}()
	return true
}

func (c *Controller) applyTurnEvent(t *model.Thread, index int, e harness.AgentEvent) {
	turn := &t.Turns[index]
	run := c.turnRuns[t.ID]
	if e.Resolved != nil {
		c.removeAgentRequest(e.Resolved)
		return
	}
	if e.Session != "" {
		t.ServerID = e.Session
	}
	if e.Loaded {
		c.loaded[t.ID] = true
	}
	if e.TurnID != "" && turn.ID == "" {
		turn.ID = e.TurnID
	}
	if e.Accepted && run != nil {
		run.accepted = true
		c.removeQueued(t, run.queuedID)
		t.HandoffSent = true
		c.streamFinal = true
	}
	if e.Interaction != nil {
		r := e.Interaction.Request
		r.ID = json.RawMessage(fmt.Sprintf("%q", model.ID()))
		r.Reason, r.Command = redactMCPText(r.Reason), redactMCPText(r.Command)
		r.ThreadID, r.TurnID = t.ID, turn.ID
		if c.agentReplies == nil {
			c.agentReplies = map[*codex.Request]chan any{}
		}
		c.agentReplies[r] = e.Interaction.Reply
		c.Requests = append(c.Requests, r)
	}
	if e.TurnError != "" {
		turn.Error = e.TurnError
	}
	mergeAgentEvent(turn, e)
	c.streamDirty = true
	if e.Kind == "contextCompaction" && e.Status != "" && e.Status != "inProgress" {
		c.streamFinal = true
	}
	if e.End != nil {
		c.finishTurn(t, index, *e.End)
	} else if e.Accepted && run != nil && run.finished {
		c.releaseTurn(t, run)
	}
}

func (c *Controller) finishTurn(t *model.Thread, index int, result harness.TurnResult) {
	turn := &t.Turns[index]
	run := c.turnRuns[t.ID]
	awaitingAcceptance := run != nil && run.finished && !run.accepted
	if run != nil && run.index != index {
		return
	}
	if !c.Running[t.ID] && !awaitingAcceptance {
		return
	}
	if run != nil && run.finished && (!awaitingAcceptance || result.Status == "completed") {
		return
	}
	turn.Status, turn.Error = result.Status, result.Error
	if turn.Status == "" {
		turn.Status = "completed"
	}
	c.Running[t.ID] = false
	if run != nil {
		run.finished = true
	}
	for i := range turn.Items {
		item := &turn.Items[i]
		if item.Status == "" || item.Status == "inProgress" {
			item.Status = turn.Status
			if item.Kind == "contextCompaction" && turn.Status != "interrupted" {
				item.Status = "unknown"
			}
		}
	}
	if turn.Status != "completed" {
		for i := range t.Queue {
			t.Queue[i].Sending = false
		}
	}
	for i := len(c.Requests) - 1; i >= 0; i-- {
		r := c.Requests[i]
		if r.ThreadID == t.ID || t.Driver() == "codex" && r.ThreadID == t.ServerID && c.agentReplies[r] == nil {
			c.removeAgentRequest(r)
		}
	}
	if result.Error != "" && turn.Status == "failed" {
		c.ReportError(result.Error)
	}
	c.streamDirty, c.streamFinal = true, true
	// A fast native completion can arrive before turn/start replies.
	// Keep its submission alive and advance only after positive acceptance.
	if run == nil {
		if turn.Status == "completed" {
			c.sendNext(t)
		}
	} else if run.accepted || turn.Status != "completed" {
		c.releaseTurn(t, run)
	}
}

func (c *Controller) releaseTurn(t *model.Thread, run *turnRun) {
	if c.turnRuns[t.ID] != run {
		return
	}
	run.close()
	delete(c.turnRuns, t.ID)
	if run.accepted && t.Turns[run.index].Status == "completed" {
		c.sendNext(t)
	}
}

func (c *Controller) removeAgentRequest(r *codex.Request) {
	delete(c.agentReplies, r)
	for i, pending := range c.Requests {
		if pending == r {
			c.Requests = append(c.Requests[:i], c.Requests[i+1:]...)
			return
		}
	}
}

func (c *Controller) Interrupt() {
	t := c.State.Thread(c.State.Selected)
	if t == nil || !c.Running[t.ID] {
		return
	}
	run := c.turnRuns[t.ID]
	if run == nil {
		return
	}
	if run.session == nil {
		run.close()
		return
	}
	target := harness.TurnTarget{Session: t.ServerID, Turn: t.Turns[run.index].ID}
	driver, _ := harness.Lookup(t.Driver())
	if driver.Capabilities.PersistentConnection && target.Turn == "" {
		c.LogNotice("The turn is starting. Try Stop again in a moment.")
		return
	}
	go func() {
		ctx, cancel := codex.Timeout()
		defer cancel()
		if err := run.session.Interrupt(ctx, target); err != nil {
			c.post(func() {
				if c.turnRuns[t.ID] == run {
					c.ReportError(err.Error())
				}
			})
		}
	}()
}
