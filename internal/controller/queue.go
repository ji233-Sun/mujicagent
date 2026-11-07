package controller

import (
	"fmt"

	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

// QueueEdit keeps the original queue slot and ordinary draft intact until saved.
type QueueEdit struct{ ID, Text string }

func (c *Controller) QueueEdit(threadID string) *QueueEdit { return c.queueEdits[threadID] }

func (c *Controller) ComposerDraft(t *model.Thread) *string {
	if edit := c.QueueEdit(t.ID); edit != nil {
		return &edit.Text
	}
	return &t.Draft
}

func (c *Controller) EditQueued(threadID, id string) bool {
	t := c.State.Thread(threadID)
	if t == nil || c.QueueEdit(threadID) != nil {
		return false
	}
	for _, q := range t.Queue {
		if q.ID == id && !q.Sending {
			if c.queueEdits == nil {
				c.queueEdits = make(map[string]*QueueEdit)
			}
			c.queueEdits[threadID] = &QueueEdit{ID: id, Text: q.Text}
			return true
		}
	}
	return false
}

func (c *Controller) CancelQueueEdit(threadID string) { delete(c.queueEdits, threadID) }

func (c *Controller) DeleteQueued(threadID, id string) {
	t := c.State.Thread(threadID)
	if t == nil {
		return
	}
	for _, q := range t.Queue {
		if q.ID == id && !q.Sending {
			if edit := c.QueueEdit(threadID); edit != nil && edit.ID == id {
				c.CancelQueueEdit(threadID)
			}
			c.removeQueued(t, id)
			c.Save()
			return
		}
	}
}

func (c *Controller) removeQueued(t *model.Thread, id string) {
	for i, q := range t.Queue {
		if q.ID == id {
			t.Queue = append(t.Queue[:i], t.Queue[i+1:]...)
			return
		}
	}
}

func (c *Controller) MoveQueued(threadID, id, target string) {
	t := c.State.Thread(threadID)
	if t == nil {
		return
	}
	from, to := -1, -1
	for i, q := range t.Queue {
		if q.ID == id {
			from = i
		}
		if q.ID == target {
			to = i
		}
	}
	if from < 0 || to < 0 || from == to {
		return
	}
	if t.Queue[from].Sending {
		return
	}
	q := t.Queue[from]
	t.Queue = append(t.Queue[:from], t.Queue[from+1:]...)
	t.Queue = append(t.Queue, model.QueuedMessage{})
	copy(t.Queue[to+1:], t.Queue[to:])
	t.Queue[to] = q
	c.Save()
}

func (c *Controller) sendNext(t *model.Thread) {
	if c.Running[t.ID] || len(t.Queue) == 0 {
		return
	}
	// A pending Steer must resolve before a completed turn advances the queue.
	for _, q := range t.Queue {
		if q.Sending {
			return
		}
	}
	q := &t.Queue[0]
	if edit := c.QueueEdit(t.ID); edit != nil && edit.ID == q.ID {
		return
	}
	q.Sending = true
	if !c.sendThread(t, q.Text, false, q.ID) {
		q.Sending = false
	}
}

func (c *Controller) ResumeQueue(threadID string) {
	if t := c.State.Thread(threadID); t != nil {
		c.sendNext(t)
	}
}

func (c *Controller) CanSteer(t *model.Thread) bool {
	if t == nil || !harness.CanSteer(t.Driver()) || !c.Running[t.ID] {
		return false
	}
	run := c.turnRuns[t.ID]
	return run != nil && run.session != nil && run.accepted && !run.finished
}

func (c *Controller) Steer(threadID, id string) {
	t := c.State.Thread(threadID)
	if !c.CanSteer(t) {
		return
	}
	for i := range t.Queue {
		q := &t.Queue[i]
		if q.ID != id || q.Sending {
			continue
		}
		if edit := c.QueueEdit(t.ID); edit != nil && edit.ID == id {
			return
		}
		text := q.Text
		q.Sending = true
		turnIndex := len(t.Turns) - 1
		target := harness.TurnTarget{Session: t.ServerID, Turn: t.Turns[turnIndex].ID}
		run, epoch := c.turnRuns[t.ID], c.epoch
		driver, _ := harness.Lookup(t.Driver())
		go func() {
			input, err := messageInput(text, c.Dir)
			if err == nil {
				ctx, cancel := codex.Timeout()
				defer cancel()
				err = run.session.Steer(ctx, target, input)
			}
			c.post(func() {
				if driver.Capabilities.PersistentConnection && epoch != c.epoch {
					return
				}
				c.applySteerResult(t, run, turnIndex, id, text, err)
			})
		}()
		return
	}
}

func (c *Controller) applySteerResult(t *model.Thread, run *turnRun, index int, id, text string, err error) {
	if c.State.Thread(t.ID) != t {
		return
	}
	if current := c.turnRuns[t.ID]; current != nil && current != run {
		return
	}
	turn := &t.Turns[index]
	if turn.Status != "inProgress" && turn.Status != "completed" {
		return
	}
	pending := false
	for i := range t.Queue {
		if t.Queue[i].ID == id && t.Queue[i].Sending {
			t.Queue[i].Sending = false
			pending = true
			break
		}
	}
	if !pending {
		return
	}
	if err != nil {
		c.ReportError(fmt.Sprintf("Steer: %v", err))
		c.Save()
		return
	}
	c.removeQueued(t, id)
	turn.Items = append(turn.Items, model.Item{ID: id, Kind: "steerMessage", Text: text, Status: "completed"})
	c.Save()
	if turn.Status == "completed" {
		c.sendNext(t)
	}
}
