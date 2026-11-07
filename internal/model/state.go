package model

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Project struct {
	ID, Name, Path string
	Expanded       bool `json:",omitempty"`
}
type Item struct {
	ID, Kind, Text, Status string
	MCP                    *MCPCall `json:",omitempty"`
}

// MCPCall contains display data with credentials redacted before storage.
type MCPCall struct {
	Server, Tool, Arguments, Result, Error string
}
type Turn struct {
	ID, Prompt, Status, Error string
	Started                   time.Time
	Items                     []Item
}
type QueuedMessage struct {
	ID, Text string
	Sending  bool `json:"-"`
}

type Thread struct {
	HandoffSent                                   bool            `json:",omitempty"`
	Harness, AgentModel, AgentAccess, AgentEffort string          `json:",omitempty"`
	HandoffFrom, HandoffContext                   string          `json:",omitempty"`
	Queue                                         []QueuedMessage `json:",omitempty"`
	ID, ServerID, ProjectID, Title, Cwd           string
	Settled                                       bool
	Updated                                       time.Time
	Created                                       time.Time
	Turns                                         []Turn
	Draft                                         string
	PermissionProfile                             string
}
type ModelFavorite struct{ Harness, ModelID string }

type Settings struct {
	ModelFavorites                                                                        []ModelFavorite `json:",omitempty"`
	PiPath, DroidPath                                                                     string          `json:",omitempty"`
	HarnessSource                                                                         string          `json:",omitempty"`
	DisableHarnessAutoCheck                                                               bool            `json:",omitempty"`
	CodexPath, Model, Effort, ServiceTier, Appearance, Language, DefaultPermissionProfile string
	DeveloperMode                                                                         bool
}
type State struct {
	NoProjectExpanded bool
	Version           int
	Projects          []Project
	Threads           []*Thread
	Selected          string
	Settings          Settings
}

func New() *State {
	return &State{Version: 1, Settings: Settings{CodexPath: "codex", Effort: "medium", Appearance: "Light", Language: "zh-Hans", DefaultPermissionProfile: ":approve-for-me"}}
}
func ID() string {
	var b [12]byte
	if _, err := rand.Read(b[:]); err != nil {
		panic(err)
	}
	return hex.EncodeToString(b[:])
}
func (s *State) Thread(id string) *Thread {
	for _, t := range s.Threads {
		if t.ID == id {
			return t
		}
	}
	return nil
}
func (s *State) AddThread(projectID, cwd string) *Thread {
	t := &Thread{AgentModel: s.Settings.Model, ID: ID(), ProjectID: projectID, Cwd: cwd, Title: "New conversation", Updated: time.Now(), Created: time.Now(), PermissionProfile: s.Settings.DefaultPermissionProfile}
	s.Threads = append([]*Thread{t}, s.Threads...)
	s.Selected = t.ID
	return t
}

// MessageTitle shares the same compact title for drafts and their first send.
func MessageTitle(text string) string {
	var titleText strings.Builder
	for _, part := range MessageParts(strings.TrimSpace(text)) {
		if part.Attachment != nil {
			titleText.WriteString(filepath.Base(part.Attachment.Path))
		} else {
			titleText.WriteString(part.Text)
		}
	}
	title := []rune(strings.ReplaceAll(titleText.String(), "\n", " "))
	if len(title) > 48 {
		title = append(title[:48], '…')
	}
	return string(title)
}

func Load(path string) (*State, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return New(), nil
	}
	if err != nil {
		return nil, err
	}
	s := New()
	if err := json.Unmarshal(b, s); err != nil {
		return nil, fmt.Errorf("read conversation data: %w", err)
	}
	if s.Version != 1 {
		return nil, fmt.Errorf("unsupported data version %d", s.Version)
	}
	// Retain the field for older snapshots, but the app now uses light appearance only.
	s.Settings.Appearance = "Light"
	if s.Settings.Language != "en" {
		s.Settings.Language = "zh-Hans"
	}
	if s.Settings.DefaultPermissionProfile == "" {
		s.Settings.DefaultPermissionProfile = ":approve-for-me"
	}
	// Reveal the last selected conversation's project when reopening the app.
	if selected := s.Thread(s.Selected); selected != nil && selected.ProjectID != "" {
		for i := range s.Projects {
			if s.Projects[i].ID == selected.ProjectID {
				s.Projects[i].Expanded = true
				break
			}
		}
	}
	for _, t := range s.Threads {
		if t.PermissionProfile == "" {
			t.PermissionProfile = s.Settings.DefaultPermissionProfile
		}
		for i := range t.Turns {
			if t.Turns[i].Status == "inProgress" {
				t.Turns[i].Status = "interrupted"
				t.Turns[i].Error = "App closed before this turn finished."
			}
		}
	}
	return s, nil
}

// Save replaces the complete snapshot atomically; a failed write preserves the previous file.
func Save(path string, s *State) error {
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".state-*")
	if err != nil {
		return err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err = f.Write(b); err != nil {
		f.Close()
		return err
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}

// Empty IDs in existing snapshots retain Codex ownership.
func (t *Thread) Driver() string {
	if t == nil || t.Harness == "" {
		return "codex"
	}
	return t.Harness
}
func (s Settings) AgentPath(id string) string {
	switch id {
	case "pi":
		if s.PiPath != "" {
			return s.PiPath
		}
		return "pi"
	case "droid":
		if s.DroidPath != "" {
			return s.DroidPath
		}
		return "droid"
	}
	return s.CodexPath
}

func (s *State) IsModelFavorite(key ModelFavorite) bool {
	for _, favorite := range s.Settings.ModelFavorites {
		if favorite == key {
			return true
		}
	}
	return false
}
func (s *State) ToggleModelFavorite(key ModelFavorite) {
	if key.ModelID == "" || key.Harness != "codex" && key.Harness != "pi" && key.Harness != "droid" {
		return
	}
	for i, favorite := range s.Settings.ModelFavorites {
		if favorite == key {
			s.Settings.ModelFavorites = append(s.Settings.ModelFavorites[:i], s.Settings.ModelFavorites[i+1:]...)
			return
		}
	}
	s.Settings.ModelFavorites = append(s.Settings.ModelFavorites, key)
}
