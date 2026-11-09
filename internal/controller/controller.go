// Package controller owns application actions and serializes background results onto the UI thread.
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/ji233-Sun/mujicagent/internal/codex"
	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type Controller struct {
	agentReplies                        map[*codex.Request]chan any
	AppUpdates                          AppUpdateState
	Harnesses                           map[string]*HarnessState
	turnRuns                            map[string]*turnRun
	State                               *model.State
	Dir                                 string
	Error, Connection, Version, Account string
	Logs                                []LogEntry
	logSequence                         uint64
	Models                              []codex.Model
	Permissions                         []codex.PermissionProfile
	PermissionsLoading                  bool
	PermissionsError                    string
	permissionsTarget                   string
	permissionsRequest                  uint64
	permissionUpdates                   map[string]bool
	turnPermissions                     map[string]string
	Requests                            []*codex.Request
	Running                             map[string]bool
	queueEdits                          map[string]*QueueEdit
	client                              *codex.Client
	loaded                              map[string]bool
	events                              chan func()
	stop                                chan struct{}
	once                                sync.Once
	wake                                func()
	streamDirty                         bool
	streamFinal                         bool
	writer                              *snapshotWriter
	epoch                               int
	connectCancel                       context.CancelFunc
	reconnectTimer                      *time.Timer
	reconnectGeneration                 uint64
	connectionQueue                     map[string]bool
}

func New(dir string, s *model.State, wake func()) *Controller {
	return &Controller{State: s, Dir: dir, Connection: "Disconnected", Running: map[string]bool{}, loaded: map[string]bool{}, permissionUpdates: map[string]bool{}, turnPermissions: map[string]string{}, events: make(chan func(), 1024), stop: make(chan struct{}), wake: wake}
}
func (c *Controller) post(fn func()) {
	select {
	case c.events <- fn:
		if c.wake != nil {
			c.wake()
		}
	case <-c.stop:
	}
}
func (c *Controller) Drain() {
	defer func() {
		if c.streamDirty {
			c.streamDirty = false
			c.saveStream()
		}
		if len(c.events) > 0 && c.wake != nil {
			c.wake()
		}
	}()
	// A producer can replenish the queue indefinitely. Always return to input
	// and painting, even while a CLI is emitting faster than we can consume.
	deadline := time.Now().Add(4 * time.Millisecond)
	for i := 0; i < 128; i++ {
		select {
		case fn := <-c.events:
			fn()
		default:
			return
		}
		if time.Now().After(deadline) {
			return
		}
	}
}
func (c *Controller) Save() {
	if err := c.saveState(); err != nil {
		c.ReportError(err.Error())
	}
}
func (c *Controller) Close() {
	c.once.Do(func() {
		close(c.stop)
		c.epoch++
		c.cancelReconnect()
		if c.connectCancel != nil {
			c.connectCancel()
		}
		c.CancelHarness("codex")
		for _, run := range c.turnRuns {
			run.close()
		}
		for _, h := range c.Harnesses {
			if h.modelsCancel != nil {
				h.modelsCancel()
			}
			if h.cancel != nil {
				h.cancel()
			}
		}
		if c.client != nil {
			c.client.Close()
		}
		c.Save()
		if c.writer != nil {
			close(c.writer.pending)
			<-c.writer.done
		}
	})
}
func (c *Controller) AnyRunning() bool {
	if len(c.turnRuns) > 0 {
		return true
	}
	for _, b := range c.Running {
		if b {
			return true
		}
	}
	return false
}

func (c *Controller) Connect() {
	if !c.CanConnect() {
		return
	}
	c.cancelReconnect()
	if c.connectCancel != nil {
		c.connectCancel()
	}
	connectionCtx, connectionCancel := context.WithCancel(context.Background())
	c.connectCancel = connectionCancel
	c.epoch++
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
	epoch := c.epoch
	c.Connection = "Connecting"
	c.Error = ""
	for i := len(c.Requests) - 1; i >= 0; i-- {
		if c.agentReplies[c.Requests[i]] == nil {
			c.Requests = append(c.Requests[:i], c.Requests[i+1:]...)
		}
	}
	c.loaded = map[string]bool{}
	c.permissionUpdates = map[string]bool{}
	binary := c.State.Settings.CodexPath
	go func() {
		defer connectionCancel()
		ctx, cancel := context.WithTimeout(connectionCtx, 45*time.Second)
		defer cancel()
		client, err := codex.Start(ctx, binary, func(m codex.Message) {
			c.post(func() {
				if epoch == c.epoch {
					c.handle(m)
				}
			})
		})
		if err != nil {
			c.post(func() {
				if epoch == c.epoch {
					c.Connection = "Disconnected"
					c.ReportError(err.Error())
					c.scheduleReconnect()
				}
			})
			return
		}
		select {
		case <-c.stop:
			client.Close()
			return
		default:
		}
		var models []codex.Model
		var cursor *string
		for {
			var page struct {
				Data       []codex.Model `json:"data"`
				NextCursor *string       `json:"nextCursor"`
			}
			if err = client.Call(ctx, "model/list", map[string]any{"cursor": cursor}, &page); err != nil {
				break
			}
			models = append(models, page.Data...)
			cursor = page.NextCursor
			if cursor == nil || *cursor == "" {
				break
			}
		}
		accountCtx, accountCancel := context.WithTimeout(connectionCtx, 50*time.Second)
		account, accountErr := client.ReadAccount(accountCtx)
		accountCancel()
		if connectionCtx.Err() != nil {
			client.Close()
			return
		}
		version := client.Version()
		applied := make(chan struct{})
		c.post(func() {
			defer close(applied)
			if epoch != c.epoch || c.closed() {
				client.Close()
				return
			}
			select {
			case <-client.Done():
				// The transport's closure event will schedule the retry.
				return
			default:
			}
			c.client = client
			c.Connection = "Connected"
			c.Models = models
			c.Version = version
			if err != nil {
				c.ReportError(err.Error())
			}
			c.Account = account.Label()
			if accountErr != nil {
				c.Account = accountErr.Error()
			}
			if c.State.Settings.Model == "" {
				for _, m := range models {
					if m.IsDefault {
						c.State.Settings.Model = m.Model
						c.State.Settings.Effort = m.DefaultReasoningEffort
						break
					}
				}
			}
			if c.serviceTier() == "default" {
				c.State.Settings.ServiceTier = ""
			}
			c.Save()
			c.resumeConnectionQueue()
		})
		select {
		case <-applied:
		case <-c.stop:
			client.Close()
		}
	}()
}

func (c *Controller) AddProject(path string) {
	abs, err := filepath.Abs(path)
	if err != nil {
		c.ReportError(err.Error())
		return
	}
	abs, err = filepath.EvalSymlinks(abs)
	if err != nil {
		c.ReportError(err.Error())
		return
	}
	info, err := os.Stat(abs)
	if err != nil {
		c.ReportError(err.Error())
		return
	}
	if !info.IsDir() {
		c.ReportError("Choose a project directory.")
		return
	}
	for _, p := range c.State.Projects {
		if p.Path == abs {
			c.NewThread(p.ID)
			return
		}
	}
	p := model.Project{ID: model.ID(), Name: filepath.Base(abs), Path: abs}
	c.State.Projects = append(c.State.Projects, p)
	c.NewThread(p.ID)
}
func (c *Controller) NewThread(projectID string) {
	cwd := ""
	for _, p := range c.State.Projects {
		if p.ID == projectID {
			cwd = p.Path
		}
	}
	if cwd == "" && projectID != "" {
		c.ReportError("Project not found.")
		return
	}
	selection := c.State.Settings.LastModelSelection
	if selection.Harness == "" {
		selection = c.selectedModelConfiguration()
	}
	// Reuse only untouched conversations in this project and Harness; preserve all drafts.
	for _, t := range c.State.Threads {
		if t.Driver() == selection.Harness && t.ServerID == "" && t.HandoffFrom == "" && t.HandoffContext == "" && t.ProjectID == projectID && !c.Running[t.ID] && !t.Settled && len(t.Turns) == 0 && len(t.Queue) == 0 && strings.TrimSpace(t.Draft) == "" {
			c.applyModelConfiguration(t, selection)
			c.Select(t.ID)
			c.SetProjectExpanded(projectID, true)
			return
		}
	}
	t := c.State.AddThread(projectID, cwd)
	t.AgentAccess = c.State.Settings.AgentDefaultAccess(selection.Harness)
	c.applyModelConfiguration(t, selection)
	if projectID == "" {
		c.State.NoProjectExpanded = true
	}
	c.Save()
}

func (c *Controller) selectedModelConfiguration() model.ModelSelection {
	settings := c.State.Settings
	selection := model.ModelSelection{Harness: "codex", Model: settings.Model, Effort: settings.Effort, ServiceTier: settings.ServiceTier}
	if t := c.State.Thread(c.State.Selected); t != nil {
		selection.Harness = t.Driver()
		if t.AgentModel != "" || t.Driver() != "codex" {
			selection.Model = t.AgentModel
		}
		if t.Driver() != "codex" {
			selection.Effort, selection.ServiceTier = t.AgentEffort, ""
		}
	}
	return selection
}

// RememberModelSelection records explicit picker changes, not conversation navigation.
func (c *Controller) RememberModelSelection() {
	c.State.Settings.LastModelSelection = c.selectedModelConfiguration()
	c.Save()
}

func (c *Controller) applyModelConfiguration(t *model.Thread, selection model.ModelSelection) {
	t.Harness, t.AgentModel = selection.Harness, selection.Model
	if t.Driver() == "codex" {
		c.State.Settings.Model, c.State.Settings.Effort, c.State.Settings.ServiceTier = selection.Model, selection.Effort, selection.ServiceTier
	} else {
		t.AgentEffort = selection.Effort
		c.normalizeAgentEffort(t)
	}
}

// SwitchProject transfers the unsent prompt without changing a server thread's workspace.
func (c *Controller) SwitchProject(projectID string) bool {
	t := c.State.Thread(c.State.Selected)
	if t != nil && (t.ProjectID == projectID || c.QueueEdit(t.ID) != nil) {
		return false
	}
	cwd := ""
	if projectID != "" {
		for _, p := range c.State.Projects {
			if p.ID == projectID {
				cwd = p.Path
				break
			}
		}
		if cwd == "" {
			return false
		}
	}
	draft := ""
	driver, agentModel, access, effort := "", "", "", ""
	if t != nil {
		driver, agentModel, access, effort = t.Harness, t.AgentModel, t.AgentAccess, t.AgentEffort
		draft = t.Draft
	}
	if t == nil || t.ServerID != "" || len(t.Turns) != 0 || len(t.Queue) != 0 || c.Running[t.ID] || t.Settled {
		if t != nil {
			t.Draft = ""
		}
		t = c.State.AddThread(projectID, cwd)
	} else {
		t.ProjectID, t.Cwd = projectID, cwd
		// Permissions are scoped to the target workspace.
		t.PermissionProfile = c.State.Settings.DefaultPermissionProfile
	}
	t.Harness, t.AgentModel, t.AgentAccess, t.AgentEffort = driver, agentModel, access, effort
	t.Draft = draft
	t.Updated = time.Now()
	c.Save()
	return true
}

func (c *Controller) Select(id string) { c.State.Selected = id; c.Save() }

func (c *Controller) SetProjectExpanded(id string, expanded bool) {
	if id == "" {
		c.State.NoProjectExpanded = expanded
		c.Save()
		return
	}
	for i := range c.State.Projects {
		p := &c.State.Projects[i]
		if p.ID == id && p.Expanded != expanded {
			p.Expanded = expanded
			c.Save()
			return
		}
	}
}

// DeleteThread removes only this app's record after the view confirms the action.
func (c *Controller) DeleteThread(id string) {
	if c.Running[id] || c.turnRuns[id] != nil {
		return
	}
	for i, t := range c.State.Threads {
		if t.ID != id {
			continue
		}
		old, selected := c.State.Threads, c.State.Selected
		remaining := append([]*model.Thread{}, old[:i]...)
		c.State.Threads = append(remaining, old[i+1:]...)
		if selected == id {
			c.State.Selected = ""
			for _, next := range c.State.Threads {
				if !next.Settled {
					c.State.Selected = next.ID
					break
				}
			}
		}
		if err := c.saveState(); err != nil {
			c.State.Threads = old
			c.State.Selected = selected
			c.ReportError(err.Error())
			return
		}
		delete(c.loaded, id)
		delete(c.Running, id)
		c.CancelQueueEdit(id)
		return
	}
}

func (c *Controller) Settle(id string, settled bool) {
	t := c.State.Thread(id)
	if t == nil || c.Running[id] || c.turnRuns[id] != nil {
		return
	}
	t.Settled = settled
	if settled && c.State.Selected == id {
		c.NewThread(t.ProjectID)
	}
	c.Save()
}

func (c *Controller) serviceTier() string { return c.serviceTierFor(c.State.Settings.Model) }
func (c *Controller) serviceTierFor(modelID string) string {
	for _, m := range c.Models {
		if m.Model == modelID && m.SupportsSpeed(c.State.Settings.ServiceTier) {
			return c.State.Settings.ServiceTier
		}
	}
	// Omission inherits the thread or catalog default, which may be accelerated.
	return "default"
}

func (c *Controller) Send(text string, plan bool) bool {
	text = strings.TrimSpace(text)
	if text == "" {
		return false
	}
	if edit := c.QueueEdit(c.State.Selected); edit != nil {
		t := c.State.Thread(c.State.Selected)
		for i := range t.Queue {
			if t.Queue[i].ID == edit.ID && !t.Queue[i].Sending {
				t.Queue[i].Text = text
				delete(c.queueEdits, t.ID)
				c.Save()
				return true
			}
		}
		return false
	}
	t := c.State.Thread(c.State.Selected)
	if t == nil {
		c.ReportError("Choose a project directory.")
		return false
	}
	if c.Running[t.ID] || c.turnRuns[t.ID] != nil || len(t.Queue) > 0 {
		t.Queue = append(t.Queue, model.QueuedMessage{ID: model.ID(), Text: text})
		t.Draft = ""
		c.Save()
		if t.Driver() == "codex" && c.Connection != "Connected" {
			c.Connect()
		}
		return true
	}
	if !c.sendThread(t, text, plan, "") {
		return false
	}
	t.Draft = ""
	c.Save()
	return true
}

func (c *Controller) AnswerQuestions(r *codex.Request, answers map[string]string) {
	if r.Method != "item/tool/requestUserInput" {
		return
	}
	values := map[string]any{}
	for _, q := range r.Questions {
		answer := strings.TrimSpace(answers[q.ID])
		if answer == "" && (!q.MultiSelect || strings.TrimSpace(answers[q.ID+":other"]) == "") {
			c.ReportError("Answer each question before continuing.")
			return
		}
		selected := []string{answer}
		if q.MultiSelect {
			selected = []string{}
			if answer != "" && json.Unmarshal([]byte(answer), &selected) != nil {
				c.ReportError("Invalid selection")
				return
			}
			if other := strings.TrimSpace(answers[q.ID+":other"]); other != "" {
				selected = append(selected, other)
			}
			if len(selected) == 0 {
				c.ReportError("Answer each question before continuing.")
				return
			}
		}
		values[q.ID] = map[string]any{"answers": selected}
	}
	c.replyRequest(r, map[string]any{"answers": values})
}

func (c *Controller) Approve(r *codex.Request, decision json.RawMessage) {
	for _, option := range r.ApprovalOptions() {
		if bytes.Equal(option.Decision, decision) {
			c.replyRequest(r, map[string]any{"decision": option.Decision})
			return
		}
	}
}

func (c *Controller) replyRequest(r *codex.Request, result any) {
	if reply := c.agentReplies[r]; reply != nil && !r.Responding {
		r.Responding = true
		reply <- result
		delete(c.agentReplies, r)
		for i, request := range c.Requests {
			if request == r {
				c.Requests = append(c.Requests[:i], c.Requests[i+1:]...)
				break
			}
		}
		return
	}
	if c.client == nil || r.Responding {
		return
	}
	pending := false
	for _, request := range c.Requests {
		if request == r {
			pending = true
			break
		}
	}
	if !pending {
		return
	}
	r.Responding = true
	client, epoch := c.client, c.epoch
	go func() {
		err := client.Reply(r.ID, result)
		c.post(func() {
			if epoch != c.epoch {
				return
			}
			if err != nil {
				r.Responding = false
				c.ReportError(err.Error())
				return
			}
			c.removeRequest(r.ID)
		})
	}()
}
func (c *Controller) removeRequest(id json.RawMessage) {
	for i, r := range c.Requests {
		if string(r.ID) == string(id) {
			c.Requests = append(c.Requests[:i], c.Requests[i+1:]...)
			return
		}
	}
}

func (c *Controller) handle(m codex.Message) {
	if m.Method == "transport/closed" || m.Method == "transport/error" {
		c.epoch++
		if c.connectCancel != nil {
			c.connectCancel()
		}
		c.Connection = "Disconnected"
		if c.client != nil {
			c.client.Close()
		}
		c.client = nil
		if m.Error != nil {
			c.ReportError(m.Error.Message)
		}
		for _, t := range c.State.Threads {
			if t.Driver() != "codex" {
				continue
			}
			for i := range t.Queue {
				t.Queue[i].Sending = false
			}
			if (c.Running[t.ID] || c.turnRuns[t.ID] != nil) && len(t.Turns) > 0 {
				markPendingCompactions(&t.Turns[len(t.Turns)-1], "unknown")
				c.finishTurn(t, len(t.Turns)-1, harness.TurnResult{Status: "interrupted", Error: "Connection lost. Reconnect to continue."})
			}
		}
		// External driver requests and queue acknowledgments are independent.
		for i := len(c.Requests) - 1; i >= 0; i-- {
			if c.agentReplies[c.Requests[i]] == nil {
				c.Requests = append(c.Requests[:i], c.Requests[i+1:]...)
			}
		}
		c.streamDirty, c.streamFinal = true, true
		c.scheduleReconnect()
		return
	}
	native, known, err := harness.DecodeCodexEvent(m)
	if err != nil {
		c.ReportError(err.Error())
		return
	}
	if !known {
		if len(m.ID) > 0 {
			if c.client != nil {
				go c.client.Reject(m.ID, "Unsupported request: "+m.Method)
			}
			c.LogDiagnostic("Codex requested an unsupported action: " + m.Method)
		}
		return
	}
	if native.Request != nil {
		c.Requests = append(c.Requests, native.Request)
		return
	}
	if len(native.ResolvedID) > 0 {
		c.removeRequest(native.ResolvedID)
		return
	}
	var t *model.Thread
	for _, candidate := range c.State.Threads {
		if candidate.Driver() == "codex" && candidate.ServerID == native.ThreadID && native.ThreadID != "" {
			t = candidate
			break
		}
	}
	if t == nil || len(t.Turns) == 0 {
		return
	}
	index := len(t.Turns) - 1
	turn := &t.Turns[index]
	e := native.Event
	if native.TurnID != "" && native.TurnID != turn.ID {
		historical := -1
		for i := 0; i < index; i++ {
			if t.Turns[i].ID == native.TurnID {
				historical = i
				break
			}
		}
		if historical >= 0 {
			if e.Kind != "contextCompaction" {
				return
			}
			index, turn = historical, &t.Turns[historical]
		} else if turn.ID != "" {
			return
		} else if e.End != nil {
			turn.ID = native.TurnID
		}
	}
	c.applyTurnEvent(t, index, e)
}
func (c *Controller) CancelQuestions(r *codex.Request) {
	if r.Cancellable && c.agentReplies[r] != nil {
		c.replyRequest(r, map[string]any{"cancelled": true, "answers": map[string]any{}})
	}
}
