// Package controller owns application actions and serializes background results onto the UI thread.
package controller

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
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
	agentControls                       map[string]*harness.Control
	agentReplies                        map[*codex.Request]chan any
	AppUpdates                          AppUpdateState
	Harnesses                           map[string]*HarnessState
	agentRuns                           map[string]context.CancelFunc
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
		c.CancelHarness("codex")
		for _, cancel := range c.agentRuns {
			cancel()
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
	for _, b := range c.Running {
		if b {
			return true
		}
	}
	return false
}

func (c *Controller) Connect() {
	if c.Connection == "Connecting" || c.AnyRunning() || c.HarnessInstalling() {
		return
	}
	if c.client != nil {
		c.client.Close()
		c.client = nil
	}
	c.epoch++
	epoch := c.epoch
	c.Connection = "Connecting"
	c.Error = ""
	c.Requests = nil
	c.loaded = map[string]bool{}
	c.permissionUpdates = map[string]bool{}
	binary := c.State.Settings.CodexPath
	go func() {
		ctx, cancel := codex.Timeout()
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
		accountCtx, accountCancel := context.WithTimeout(context.Background(), 50*time.Second)
		account, accountErr := client.ReadAccount(accountCtx)
		accountCancel()
		version := client.Version()
		c.post(func() {
			if epoch != c.epoch {
				client.Close()
				return
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
		})
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
	if c.Running[id] {
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
	if t == nil || c.Running[id] {
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
	if c.State.Thread(c.State.Selected).Driver() == "codex" && (c.client == nil || c.Connection != "Connected") {
		c.ReportError("Connect Codex in Settings before sending.")
		return false
	}
	t := c.State.Thread(c.State.Selected)
	if t == nil {
		c.ReportError("Choose a project directory.")
		return false
	}
	if c.Running[t.ID] || len(t.Queue) > 0 {
		t.Queue = append(t.Queue, model.QueuedMessage{ID: model.ID(), Text: text})
		t.Draft = ""
		c.Save()
		return true
	}
	if !c.sendThread(t, text, plan, "") {
		return false
	}
	t.Draft = ""
	c.Save()
	return true
}

func (c *Controller) sendThread(t *model.Thread, text string, plan bool, queuedID string) bool {
	if t.Driver() != "codex" {
		return c.sendAgent(t, text, queuedID)
	}
	if c.HarnessInstalling() || c.client == nil || c.Connection != "Connected" || c.Running[t.ID] || c.permissionUpdates[t.ID] {
		return false
	}
	if err := c.prepareWorkspace(t, text); err != nil {
		c.ReportError(err.Error())
		return false
	}
	info, directoryErr := os.Stat(t.Cwd)
	if directoryErr == nil && !info.IsDir() {
		directoryErr = fmt.Errorf("project is not a directory: %s", t.Cwd)
	}
	if directoryErr != nil {
		c.ReportError(directoryErr.Error())
		return false
	}
	if len(t.Turns) == 0 && t.HandoffFrom == "" {
		t.Title = model.MessageTitle(text)
	}
	t.Settled = false
	t.Updated = time.Now()
	t.Turns = append(t.Turns, model.Turn{Prompt: text, Status: "inProgress", Started: time.Now()})
	c.Running[t.ID] = true
	c.Error = ""
	c.Save()
	id, serverID, cwd := t.ID, t.ServerID, t.Cwd
	loaded := c.loaded[id]
	handoff := t.HandoffContext
	fresh := !t.HandoffSent
	settings := c.State.Settings
	if t.AgentModel != "" {
		settings.Model = t.AgentModel
	}
	serviceTier := c.serviceTierFor(settings.Model)
	permissionID := t.PermissionProfile
	if permissionID == "" {
		permissionID = c.State.Settings.DefaultPermissionProfile
	}
	turnIndex := len(t.Turns) - 1
	c.turnPermissions[id] = permissionID
	client, epoch := c.client, c.epoch
	go func() {
		ctx, cancel := codex.Timeout()
		defer cancel()
		profiles, err := client.PermissionProfiles(ctx, cwd)
		if err != nil {
			c.fail(id, epoch, err)
			return
		}
		var permission *codex.PermissionProfile
		for i := range profiles {
			if profiles[i].ID == permissionID && profiles[i].Allowed {
				permission = &profiles[i]
				break
			}
		}
		if permission == nil {
			c.fail(id, epoch, fmt.Errorf("Permission profile is unavailable: %s", permissionID))
			return
		}
		if !loaded {
			params := map[string]any{"cwd": cwd, "approvalPolicy": permission.ApprovalPolicy(), "permissions": permission.WireID(), "approvalsReviewer": permission.Reviewer()}
			if settings.Model != "" {
				params["model"] = settings.Model
			}
			method := "thread/start"
			if serverID != "" {
				method = "thread/resume"
				params["threadId"] = serverID
			}
			var result struct {
				Thread struct {
					ID string `json:"id"`
				} `json:"thread"`
			}
			if err := client.Call(ctx, method, params, &result); err != nil {
				c.fail(id, epoch, err)
				return
			}
			serverID = result.Thread.ID
			c.post(func() {
				if epoch == c.epoch {
					t.ServerID = serverID
					c.loaded[id] = true
					c.Save()
				}
			})
		}
		prompt := text
		if fresh {
			prompt = handoff + text
		}
		input, err := messageInput(prompt, c.Dir)
		if err != nil {
			c.fail(id, epoch, err)
			return
		}
		params := map[string]any{"threadId": serverID, "serviceTier": serviceTier, "permissions": permission.WireID(), "approvalPolicy": permission.ApprovalPolicy(), "approvalsReviewer": permission.Reviewer(), "summary": "auto", "input": input}
		if settings.Model != "" {
			params["model"] = settings.Model
		}
		if settings.Effort != "" {
			params["effort"] = settings.Effort
		}
		if settings.Model != "" {
			mode := "default"
			if plan {
				mode = "plan"
			}
			params["collaborationMode"] = map[string]any{"mode": mode, "settings": map[string]any{"model": settings.Model, "reasoning_effort": settings.Effort, "developer_instructions": nil}}
		}
		var result struct {
			Turn struct {
				ID string `json:"id"`
			} `json:"turn"`
		}
		if err := client.Call(ctx, "turn/start", params, &result); err != nil {
			c.fail(id, epoch, err)
			return
		}
		c.post(func() {
			if epoch == c.epoch {
				turn := &t.Turns[turnIndex]
				c.removeQueued(t, queuedID)
				t.HandoffSent = true
				if turn.ID == "" {
					turn.ID = result.Turn.ID
				}
				c.Save()
				if turn.Status == "completed" {
					c.sendNext(t)
				}
			}
		})
	}()
	return true
}
func (c *Controller) fail(id string, epoch int, err error) {
	c.post(func() {
		if epoch != c.epoch {
			return
		}
		c.ReportError(err.Error())
		c.Running[id] = false
		if t := c.State.Thread(id); t != nil && len(t.Turns) > 0 {
			turn := &t.Turns[len(t.Turns)-1]
			turn.Status = "failed"
			turn.Error = err.Error()
			for i := range t.Queue {
				t.Queue[i].Sending = false
			}
		}
		c.Save()
	})
}
func (c *Controller) Interrupt() {
	if cancel := c.agentRuns[c.State.Selected]; cancel != nil {
		cancel()
		return
	}
	t := c.State.Thread(c.State.Selected)
	if t == nil || !c.Running[t.ID] || c.client == nil || len(t.Turns) == 0 {
		return
	}
	turnID := t.Turns[len(t.Turns)-1].ID
	if turnID == "" {
		c.LogNotice("The turn is starting. Try Stop again in a moment.")
		return
	}
	client := c.client
	serverID := t.ServerID
	go func() {
		ctx, cancel := codex.Timeout()
		defer cancel()
		if err := client.Call(ctx, "turn/interrupt", map[string]string{"threadId": serverID, "turnId": turnID}, nil); err != nil {
			c.post(func() { c.ReportError(err.Error()) })
		}
	}()
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
		c.Connection = "Disconnected"
		c.client = nil
		c.Requests = nil
		if m.Error != nil {
			c.ReportError(m.Error.Message)
		}
		for _, thread := range c.State.Threads {
			for i := range thread.Queue {
				thread.Queue[i].Sending = false
			}
		}
		for id, b := range c.Running {
			if b && c.State.Thread(id).Driver() == "codex" {
				t := c.State.Thread(id)
				if t != nil && len(t.Turns) > 0 {
					turn := &t.Turns[len(t.Turns)-1]
					turn.Status = "interrupted"
					turn.Error = "Connection lost. Reconnect to continue."
				}
				c.Running[id] = false
			}
		}
		c.Save()
		return
	}
	if len(m.ID) > 0 {
		switch m.Method {
		case "item/tool/requestUserInput", "item/commandExecution/requestApproval", "item/fileChange/requestApproval", "mcpServer/elicitation/request":
			var r codex.Request
			if err := json.Unmarshal(m.Params, &r); err != nil {
				c.ReportError(err.Error())
				return
			}
			r.ID = m.ID
			r.Method = m.Method
			c.Requests = append(c.Requests, &r)
		default:
			if c.client != nil {
				go c.client.Reject(m.ID, "Unsupported request: "+m.Method)
			}
			c.LogDiagnostic("Codex requested an unsupported action: " + m.Method)
		}
		return
	}
	var p struct {
		ThreadID  string          `json:"threadId"`
		TurnID    string          `json:"turnId"`
		ItemID    string          `json:"itemId"`
		Delta     string          `json:"delta"`
		RequestID json.RawMessage `json:"requestId"`
		Item      json.RawMessage `json:"item"`
		Turn      struct {
			ID, Status string
			Error      *struct{ Message string }
		} `json:"turn"`
		Error     *struct{ Message string }       `json:"error"`
		WillRetry bool                            `json:"willRetry"`
		Plan      []struct{ Step, Status string } `json:"plan"`
	}
	if err := json.Unmarshal(m.Params, &p); err != nil {
		return
	}
	if m.Method == "serverRequest/resolved" {
		c.removeRequest(p.RequestID)
		return
	}
	var t *model.Thread
	for _, candidate := range c.State.Threads {
		if candidate.ServerID == p.ThreadID && p.ThreadID != "" {
			t = candidate
			break
		}
	}
	if t == nil || len(t.Turns) == 0 {
		return
	}
	turn := &t.Turns[len(t.Turns)-1]
	if p.TurnID != "" && turn.ID != "" && p.TurnID != turn.ID {
		return
	}
	switch m.Method {
	case "turn/started":
		turn.ID = p.Turn.ID
	case "turn/completed":
		if !c.Running[t.ID] || turn.ID == "" || p.Turn.ID != turn.ID {
			return
		}
		turn.ID = p.Turn.ID
		turn.Status = p.Turn.Status
		if p.Turn.Error != nil {
			turn.Error = p.Turn.Error.Message
		}
		c.Running[t.ID] = false
		for i := len(c.Requests) - 1; i >= 0; i-- {
			if c.Requests[i].ThreadID == t.ServerID {
				c.Requests = append(c.Requests[:i], c.Requests[i+1:]...)
			}
		}
		c.Save()
		if turn.Status == "completed" {
			c.sendNext(t)
		}
	case "error":
		if p.Error != nil {
			turn.Error = p.Error.Message
			if !p.WillRetry {
				turn.Status = "failed"
				c.Running[t.ID] = false
				c.Save()
			}
		}
	case "item/started", "item/completed":
		item := decodeItem(p.Item)
		if item.Kind == "userMessage" || item.ID == "" {
			return
		}
		if m.Method == "item/completed" && item.Status == "" {
			item.Status = "completed"
		}
		found := false
		for i := range turn.Items {
			if turn.Items[i].ID == item.ID {
				if item.Text == "" && item.Kind != "mcpToolCall" {
					item.Text = turn.Items[i].Text
				}
				if item.MCP != nil && turn.Items[i].MCP != nil {
					previous := turn.Items[i].MCP
					if item.MCP.Server == "" {
						item.MCP.Server = previous.Server
					}
					if item.MCP.Tool == "" {
						item.MCP.Tool = previous.Tool
					}
					if item.MCP.Arguments == "" {
						item.MCP.Arguments = previous.Arguments
					}
				}
				turn.Items[i] = item
				found = true
				break
			}
		}
		if !found {
			turn.Items = append(turn.Items, item)
		}
	case "item/agentMessage/delta", "item/reasoning/summaryTextDelta", "item/reasoning/textDelta", "item/commandExecution/outputDelta", "item/plan/delta":
		kind := "agentMessage"
		if strings.Contains(m.Method, "reasoning") {
			kind = "reasoning"
		}
		if strings.Contains(m.Method, "commandExecution") {
			kind = "commandExecution"
		}
		if strings.Contains(m.Method, "/plan/") {
			kind = "plan"
		}
		idx := -1
		for i := range turn.Items {
			if turn.Items[i].ID == p.ItemID {
				idx = i
				break
			}
		}
		if idx < 0 {
			turn.Items = append(turn.Items, model.Item{ID: p.ItemID, Kind: kind, Status: "inProgress"})
			idx = len(turn.Items) - 1
		}
		turn.Items[idx].Text += p.Delta
	case "turn/plan/updated":
		text := ""
		for _, step := range p.Plan {
			text += fmt.Sprintf("- [%s] %s\n", step.Status, step.Step)
		}
		item := model.Item{ID: "plan", Kind: "plan", Text: text}
		replaced := false
		for i := range turn.Items {
			if turn.Items[i].ID == "plan" {
				turn.Items[i] = item
				replaced = true
			}
		}
		if !replaced {
			turn.Items = append(turn.Items, item)
		}
	}
}

func decodeItem(raw json.RawMessage) model.Item {
	var v struct {
		ID, Type, Text, Status, Command, AggregatedOutput string
		Summary, Content                                  []string
		Changes                                           []struct{ Path, Diff string }
		Query                                             string
	}
	if json.Unmarshal(raw, &v) != nil {
		return model.Item{}
	}
	text := v.Text
	switch v.Type {
	case "reasoning":
		text = strings.Join(v.Summary, "\n\n")
		if text == "" {
			text = strings.Join(v.Content, "\n\n")
		}
	case "commandExecution":
		text = "$ " + v.Command
		if v.AggregatedOutput != "" {
			text += "\n" + v.AggregatedOutput
		}
	case "fileChange":
		for _, change := range v.Changes {
			text += change.Path + "\n" + change.Diff + "\n"
		}
	case "webSearch":
		text = v.Query
	}
	item := model.Item{ID: v.ID, Kind: v.Type, Text: text, Status: v.Status}
	if v.Type == "mcpToolCall" {
		item.MCP = decodeMCPCall(raw)
		item.Text = ""
	}
	return item
}

func (c *Controller) CancelQuestions(r *codex.Request) {
	if r.Cancellable && c.agentReplies[r] != nil {
		c.replyRequest(r, map[string]any{"cancelled": true, "answers": map[string]any{}})
	}
}
