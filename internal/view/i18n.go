package view

import (
	"strings"

	"github.com/egoist/mygo"
	"github.com/ji233-Sun/mujicagent/internal/model"
	"github.com/ji233-Sun/mujicagent/locales"
)

func (v *View) texts() locales.Messages { return locales.In(v.C.State.Settings.Language) }

func (v *View) languageChanged() {
	if v.Window != nil {
		mygo.App.SetLocale(v.C.State.Settings.Language)
	}
	v.C.Save()
}

// Localize only the placeholder; user titles and conversation contents stay verbatim.
func (v *View) threadTitle(t *model.Thread) string {
	if t != nil && len(t.Turns) == 0 && strings.TrimSpace(t.Draft) != "" {
		return model.MessageTitle(t.Draft)
	}
	if t == nil || len(t.Turns) == 0 && (t.Title == "" || t.Title == "New conversation" || t.Title == "新对话") {
		return v.texts().NewConversation()
	}
	return t.Title
}

// Existing snapshots and controller notices use these stable source messages.
// External errors and agent output pass through without translation.
func (v *View) statusText(message string) string {
	tx := v.texts()
	switch message {
	case "Not installed":
		return tx.NotInstalled()
	case "Sign in with codex login":
		return tx.SignIn()
	case "Choose a project directory.":
		return tx.ChooseDirectory()
	case "Project not found.":
		return tx.ProjectNotFound()
	case "Connect Codex in Settings before sending.":
		return tx.ConnectBeforeSending()
	case "The turn is starting. Try Stop again in a moment.":
		return tx.TurnStarting()
	case "The turn is starting. Try changing permissions again in a moment.":
		return tx.PermissionStarting()
	case "Connect Codex before changing permissions.":
		return tx.ConnectBeforeSending()
	case "Answer each question before continuing.":
		return tx.AnswerEachQuestion()
	case "Connection lost. Reconnect to continue.":
		return tx.ConnectionLost()
	case "App closed before this turn finished.":
		return tx.AppClosed()
	case "Codex connection closed":
		return tx.CodexConnectionClosed()
	case "Codex is disconnected":
		return tx.CodexDisconnected()
	}
	if method, ok := strings.CutPrefix(message, "Codex requested an unsupported action: "); ok {
		return tx.UnsupportedAction(method)
	}
	if profile, ok := strings.CutPrefix(message, "Permission profile is unavailable: "); ok {
		return tx.PermissionUnavailable(profile)
	}
	if detail, ok := strings.CutPrefix(message, "Permission update was not confirmed; the previous selection is retained: "); ok {
		return tx.PermissionUpdateFailed(detail)
	}
	return message
}
