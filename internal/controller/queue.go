package controller

import (
	"fmt"

	"github.com/ji233-Sun/mujicagent/internal/codex"
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
	return t != nil && c.client != nil && c.Connection == "Connected" && c.Running[t.ID] && len(t.Turns) > 0 && t.Turns[len(t.Turns)-1].ID != ""
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
		turnID, serverID := t.Turns[turnIndex].ID, t.ServerID
		client, epoch := c.client, c.epoch
		go func() {
			input, err := messageInput(text, c.Dir)
			if err == nil {
				ctx, cancel := codex.Timeout()
				defer cancel()
				err = client.Call(ctx, "turn/steer", map[string]any{"threadId": serverID, "expectedTurnId": turnID, "input": input}, nil)
			}
			c.post(func() {
				if epoch != c.epoch {
					return
				}
				for j := range t.Queue {
					if t.Queue[j].ID == id {
						t.Queue[j].Sending = false
					}
				}
				if err != nil {
					c.ReportError(fmt.Sprintf("Steer: %v", err))
					c.Save()
					return
				}
				c.removeQueued(t, id)
				t.Turns[turnIndex].Items = append(t.Turns[turnIndex].Items, model.Item{ID: id, Kind: "steerMessage", Text: text, Status: "completed"})
				c.Save()
				if t.Turns[turnIndex].Status == "completed" {
					c.sendNext(t)
				}
			})
		}()
		return
	}
}
