package controller

import "time"

// Logs belong to this app run, not the saved conversation snapshot.
type LogEntry struct {
	ID      uint64
	Time    time.Time
	Level   string
	Message string
}

const maxLogEntries = 200

// Like other controller state, these methods run on the UI event queue.
func (c *Controller) ReportError(message string) {
	c.Error = message
	c.addLog("error", message)
}

func (c *Controller) LogNotice(message string)     { c.addLog("info", message) }
func (c *Controller) LogDiagnostic(message string) { c.addLog("debug", message) }

func (c *Controller) addLog(level, message string) {
	if message == "" {
		return
	}
	c.logSequence++
	entry := LogEntry{ID: c.logSequence, Time: time.Now(), Level: level, Message: message}
	if len(c.Logs) == maxLogEntries {
		copy(c.Logs, c.Logs[1:])
		c.Logs[len(c.Logs)-1] = entry
		return
	}
	c.Logs = append(c.Logs, entry)
}
