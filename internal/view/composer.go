package view

import (
	"path/filepath"
	"strings"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

// Each attachment occupies one character in the same editable document as text.
func (v *View) composerText(source string) richtext.Text {
	var text richtext.Text
	for _, part := range model.MessageParts(source) {
		if part.Attachment == nil {
			text = text.Append(part.Text, richtext.Attributes{})
			continue
		}
		a := *part.Attachment
		object := v.attachmentObject(a)
		text = text.Append(string(richtext.ObjectReplacement), richtext.Attributes{Attachment: object, Link: a.Link(), Tag: "attachment"})
	}
	return text
}

// Share the same compact inline preview in drafts and sent messages.
func (v *View) attachmentObject(a model.Attachment) *richtext.Attachment {
	name := filepath.Base(a.Path)
	layout := ui.ShapeText(name, ui.Font{Size: 14}, 0)
	width, _ := layout.Size()
	baseline := layout.Lines()[0].Baseline
	preview := v.preview(a)
	object := &richtext.Attachment{Name: name, URL: a.Link(), Width: width + 22, Height: max(baseline, float32(16))}
	object.Draw = func(p *ui.Painter, r ui.Rect) {
		thumbnail := ui.Rect{X: r.X, Y: r.Y + r.H - 16, W: 18, H: 16}
		if preview.bitmap != nil {
			p.Image(preview.bitmap, thumbnail, ui.Contain)
		} else {
			p.Icon(fileEditIcon, thumbnail, p.Theme().Accent)
		}
		p.Text(r.X+22, r.Y+r.H-baseline, name, 14, p.Theme().Accent)
	}
	return object
}

func composerSource(text richtext.Text) string {
	var source strings.Builder
	for _, run := range text.Runs(0, text.Len()) {
		for _, ch := range run.Text {
			if ch == richtext.ObjectReplacement && run.Attributes.Attachment != nil {
				source.WriteString(run.Attributes.Attachment.URL)
			} else {
				source.WriteRune(ch)
			}
		}
	}
	return source.String()
}

func (v *View) syncComposer(draft *string) {
	*draft = composerSource(v.editor.Text())
	v.editorSource = *draft
}

func (v *View) insertComposer(source string) {
	if v.editor == nil {
		return
	}
	a, b := v.editor.Selection()
	start, end := min(a, b), max(a, b)
	text := v.composerText(source)
	v.editor.Edit(func(e *richtext.Edit) { e.Replace(start, end, text); e.Select(start+text.Len(), start+text.Len()) })
	if t := v.C.State.Thread(v.editorThread); t != nil {
		v.syncComposer(v.C.ComposerDraft(t))
		v.C.Save()
	}
}

func (v *View) composerInput(c *ui.Context, draft *string) {
	id := v.C.State.Selected
	queueID := ""
	if edit := v.C.QueueEdit(id); edit != nil {
		queueID = edit.ID
	}
	if v.editor == nil || v.editorThread != id || v.editorQueue != queueID || v.editorSource != *draft {
		v.editor = richtext.NewDocument(v.composerText(*draft))
		v.editor.SetSelection(v.editor.Text().Len(), v.editor.Text().Len())
		v.editorThread, v.editorSource = id, *draft
		v.editorQueue = queueID
	}
	// Draft typing is plain text; deleting or replacing an attachment must not
	// carry its link and tag into the next characters.
	v.editor.SetTypingAttributes(richtext.Attributes{})
	services := c.Services()
	tx := v.texts()
	driver := "codex"
	if t := v.C.State.Thread(id); t != nil {
		driver = t.Driver()
	}
	input := richtext.EditorBase(c.Key("draft-"+id+"-"+queueID), v.editor, richtext.EditorOptions{
		TagStyle: func(tag string, span *ui.Span) {
			if tag == "attachment" {
				span.Underline = false
			}
		},
		Label: tx.Message(), Placeholder: tx.MessagePlaceholder(agentName(driver)), NoMarkdown: true, Font: ui.Font{Size: 14},
		OnLink: func(link string) {
			for _, part := range model.MessageParts(link) {
				if part.Attachment != nil {
					v.openAttachment(*part.Attachment, id)
				}
			}
		},
	}).Bind(&v.editorHandle).Height(composerEditorHeight)
	if input.Changed() {
		v.syncComposer(draft)
	}
	input.OnShortcut(0, ui.KeyEnter, func() {
		v.syncComposer(draft)
		if v.C.Send(*draft, false) {
			v.draft = ""
		}
	})
	if queueID != "" {
		input.OnShortcut(0, ui.KeyEscape, func() { v.C.CancelQueueEdit(id) })
	}
	copySelection := func(cut bool) {
		defer services.Invalidate()
		a, b := v.editor.Selection()
		if a == b {
			return
		}
		services.WriteClipboard(composerSource(v.editor.Text().Slice(min(a, b), max(a, b))))
		if cut {
			v.insertComposer("")
		}
	}
	paste := func() {
		defer services.Invalidate()
		data := services.ReadClipboardData(transfer.FileList, transfer.URIList, transfer.PNG, transfer.Text)
		if v.pasteAttachments(data) {
			return
		}
		v.insertComposer(services.ReadClipboard())
	}
	for _, mods := range []ui.Modifiers{ui.Cmd, ui.Ctrl} {
		input.OnShortcut(mods, ui.KeyV, paste)
		input.OnShortcut(mods, ui.KeyC, func() { copySelection(false) })
		input.OnShortcut(mods, ui.KeyX, func() { copySelection(true) })
	}
	v.updateComposerMenu(input.Focused() && !v.attachmentOpen && !v.Settings, paste, copySelection)
	input.ContextMenu(func(m *ui.Menu) {
		if m.Item(tx.Copy()).Chosen() {
			copySelection(false)
		}
		if m.Item(tx.Cut()).Chosen() {
			copySelection(true)
		}
		if m.Item(tx.Paste()).Chosen() {
			paste()
		}
	})
}

// macOS dispatches standard menu accelerators as edit commands, before UI key
// shortcuts. Give the focused composer its clipboard actions at that boundary.
func (v *View) updateComposerMenu(focused bool, paste func(), copySelection func(bool)) {
	if v.Window == nil {
		return
	}
	if !focused || (v.composerMenu != nil && v.composerMenuLanguage != v.C.State.Settings.Language) {
		if v.composerMenu != nil {
			mygo.App.SetMenu(v.composerMenu)
			v.composerMenu = nil
		}
	}
	if !focused || v.composerMenu != nil {
		return
	}
	original := mygo.App.Menu()
	if original == nil {
		return
	}
	var clone func([]*mygo.MenuItem) []*mygo.MenuItem
	clone = func(items []*mygo.MenuItem) []*mygo.MenuItem {
		result := make([]*mygo.MenuItem, 0, len(items))
		for _, item := range items {
			next := &mygo.MenuItem{ID: item.ID, Label: item.Label, Role: item.Role, Type: item.Type, Accelerator: item.Accelerator, Disabled: item.Disabled, Hidden: item.Hidden, Checked: item.Checked, ToolTip: item.ToolTip, Icon: item.Icon, Click: item.Click}
			if item.Submenu != nil {
				next.Submenu = clone(item.Submenu)
			}
			switch item.Role {
			case mygo.RoleCopy:
				next.Role = ""
				next.Click = func(*mygo.MenuItem, *mygo.Window) { copySelection(false) }
			case mygo.RoleCut:
				next.Role = ""
				next.Click = func(*mygo.MenuItem, *mygo.Window) { copySelection(true) }
			case mygo.RolePaste, mygo.RolePasteAndMatchStyle:
				next.Role = ""
				next.Click = func(*mygo.MenuItem, *mygo.Window) { paste() }
			}
			result = append(result, next)
		}
		return result
	}
	v.composerMenu = original
	v.composerMenuLanguage = v.C.State.Settings.Language
	mygo.App.SetMenu(mygo.NewMenu(clone(original.Items())))
}
