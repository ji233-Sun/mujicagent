package view

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func draftText(parts []model.MessagePart) string {
	var text strings.Builder
	for _, p := range parts {
		if p.Attachment != nil {
			text.WriteString(p.Attachment.Link())
		} else {
			text.WriteString(p.Text)
		}
	}
	return text.String()
}

// Keep text fields on both sides of every chip. The serialized draft is still
// one message; the editor never needs a separate attachment collection.
func (v *View) composerInput(c *ui.Context, draft *string) {
	id := v.C.State.Selected
	if v.editorThread != id {
		v.draftStart, v.draftEnd = utf8.RuneCountInString(*draft), utf8.RuneCountInString(*draft)
	}
	if v.editorParts == nil || v.editorThread != id || v.editorSource != *draft {
		v.editorParts = nil
		for _, p := range model.MessageParts(*draft) {
			if p.Attachment != nil && (len(v.editorParts) == 0 || v.editorParts[len(v.editorParts)-1].Attachment != nil) {
				v.editorParts = append(v.editorParts, model.MessagePart{})
			}
			v.editorParts = append(v.editorParts, p)
		}
		if len(v.editorParts) == 0 || v.editorParts[len(v.editorParts)-1].Attachment != nil {
			v.editorParts = append(v.editorParts, model.MessagePart{})
		}
		*draft = draftText(v.editorParts)
		v.editorThread, v.editorSource = id, *draft
	}
	tx := v.texts()
	ui.Scroll(c.Key("draft-" + id)).MaxHeight(200).Gap(3).Children(func() {
		for i := range v.editorParts {
			p := &v.editorParts[i]
			if p.Attachment != nil {
				ui.Row(c.Key(i)).AlignItems(ui.Center).Gap(6).Children(func() {
					ui.Column(c).Grow(1).MinWidth(0).Children(func() { v.message(c, p.Attachment.Link(), id) })
					ui.Button(c, "×").Label(tx.RemoveAttachment()).OnClick(func() {
						parts := append([]model.MessagePart(nil), v.editorParts[:i]...)
						parts = append(parts, v.editorParts[i+1:]...)
						*draft = draftText(parts)
						v.editorSource = "\x00"
						v.C.Save()
					})
				})
				continue
			}
			minLines := 1
			if len(v.editorParts) == 1 {
				minLines = 2
			}
			input := ui.TextAreaBase(c.Key(i), &p.Text).Lines(minLines, 7).Label(tx.Message()).Placeholder(tx.MessagePlaceholder()).FontSize(14).OnChange(func() {
				*draft = draftText(v.editorParts)
				v.editorSource = *draft
				// Pasted links become chips on the next build.
				if len(model.MessageParts(p.Text)) > 1 || strings.HasPrefix(p.Text, "<mujica://") {
					v.editorSource = "\x00"
				}
			}).OnTextKey(func(k ui.TextKey) bool {
				if k.Key != ui.KeyEnter || k.Mods != 0 {
					return false
				}
				p.Text = k.Text()
				*draft = draftText(v.editorParts)
				if v.C.Send(*draft, false) {
					k.SetText("")
					v.draft = ""
				}
				return true
			})
			if input.Focused() {
				start, end := input.TextSelection()
				offset := utf8.RuneCountInString(draftText(v.editorParts[:i]))
				v.draftStart, v.draftEnd = offset+start, offset+end
			}
		}
	})
}
