package controller

import (
	"time"

	"github.com/ji233-Sun/mujicagent/internal/model"
)

const reconnectInterval = 15 * time.Second

func (c *Controller) closed() bool {
	select {
	case <-c.stop:
		return true
	default:
		return false
	}
}

// CanConnect scopes running work to the client being replaced.
func (c *Controller) CanConnect() bool {
	if c.closed() || c.Connection == "Connecting" || c.HarnessInstalling() {
		return false
	}
	if h := c.Harnesses["codex"]; h != nil && h.Preparing {
		return false
	}
	for _, t := range c.State.Threads {
		if t.Driver() == "codex" && (c.Running[t.ID] || c.turnRuns[t.ID] != nil) {
			return false
		}
	}
	return true
}

func (c *Controller) cancelReconnect() {
	c.reconnectGeneration++
	if c.reconnectTimer != nil {
		c.reconnectTimer.Stop()
		c.reconnectTimer = nil
	}
}

func (c *Controller) scheduleReconnect() {
	c.cancelReconnect()
	if c.closed() {
		return
	}
	generation := c.reconnectGeneration
	c.reconnectTimer = time.AfterFunc(reconnectInterval, func() {
		c.post(func() {
			if generation != c.reconnectGeneration || c.closed() {
				return
			}
			c.reconnectTimer = nil
			if c.Connection != "Disconnected" {
				return
			}
			if c.CanConnect() {
				c.Connect()
			} else {
				c.scheduleReconnect()
			}
		})
	})
}

// Only submissions explicitly waiting for a connection resume automatically.
// Interrupted turns and their pre-existing queues still require user action.
func (c *Controller) waitForConnection(t *model.Thread, text string, plan bool, queuedID string) {
	if queuedID == "" {
		queuedID = model.ID()
		t.Queue = append(t.Queue, model.QueuedMessage{ID: queuedID, Text: text})
	}
	if c.connectionQueue == nil {
		c.connectionQueue = make(map[string]bool)
	}
	c.connectionQueue[queuedID] = plan
	for i := range t.Queue {
		if t.Queue[i].ID == queuedID {
			t.Queue[i].Sending = false
		}
	}
	c.Save()
	c.Connect()
}

func (c *Controller) resumeConnectionQueue() {
	waiting := c.connectionQueue
	c.connectionQueue = nil
	for _, t := range c.State.Threads {
		if t.Driver() != "codex" || len(t.Queue) == 0 {
			continue
		}
		q := &t.Queue[0]
		plan, ok := waiting[q.ID]
		if !ok || c.QueueEdit(t.ID) != nil {
			continue
		}
		q.Sending = true
		if !c.sendThread(t, q.Text, plan, q.ID) {
			q.Sending = false
		}
	}
}
