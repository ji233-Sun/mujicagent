package view

import (
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/editor"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

const outputPreviewLines = 8
const outputPreviewBytes = 4096

// Bound work before parsing or laying out even a single very long log line.
func outputPreview(text string) (string, bool) {
	end := min(len(text), outputPreviewBytes)
	for end > 0 && end < len(text) && !utf8.RuneStart(text[end]) {
		end--
	}
	lines := 1
	for i := 0; i < end; i++ {
		if text[i] == '\n' {
			if lines == outputPreviewLines {
				end = i
				break
			}
			lines++
		}
	}
	return text[:end], end < len(text)
}

// Command output is not shell source. Only the prompt uses shell syntax;
// ordinary output stays plain, while diff lines retain their semantic colors.
var commandOutputLanguage = &editor.Language{
	Name: "Command output",
	Tokenizer: editor.TokenizerFunc(func(line string, state uint64, tokens []editor.Token) ([]editor.Token, uint64) {
		if state == 0 && strings.HasPrefix(line, "$ ") {
			tokens, _ = editor.LanguageNamed("sh").Tokenizer.Tokenize(line, 0, tokens)
		} else {
			tokens, _ = editor.LanguageNamed("diff").Tokenizer.Tokenize(line, 0, tokens)
		}
		return tokens, 1
	}),
}

type outputState struct {
	text   string
	editor editor.State
}

func (s *outputState) update(text string) {
	if text == s.text {
		return
	}
	if strings.HasPrefix(text, s.text) {
		end := s.editor.Buffer.Len()
		start, delta := end, text[len(s.text):]
		// A CRLF can straddle two streaming chunks; normalize it together.
		if strings.HasSuffix(s.text, "\r") && strings.HasPrefix(delta, "\n") {
			start--
			delta = "\r" + delta
		}
		s.editor.Buffer.Edit(editor.Edit{Start: start, End: end, Text: delta})
	} else {
		s.editor.Buffer.Reset(text)
	}
	s.text = text
}

func (v *View) activityOutput(c *ui.Context, item *model.Item, key string) {
	preview, truncated := outputPreview(item.Text)
	expanded := v.openState("output-" + key)
	text := preview
	if *expanded {
		text = item.Text
	}
	box := ui.Column(c).FillWidth().Background(c.Theme().Background).Radius(8).Clip()
	s := ui.Local(box, "output", func() outputState {
		buf := editor.NewBuffer(text)
		language := commandOutputLanguage
		if item.Kind == "fileChange" {
			language = editor.LanguageNamed("diff")
		} else if item.MCP != nil && (item.MCP.Tool == "Script" || strings.HasSuffix(item.MCP.Tool, ".Script")) {
			language = editor.LanguageNamed("javascript")
		}
		buf.SetLanguage(language)
		return outputState{text: text, editor: editor.State{
			Buffer: buf, ReadOnly: true, Font: ui.Font{Family: "monospace", Size: 12},
			LineNumbers: editor.LineNumbersOff, NoFolding: true, NoIndentGuides: true,
			NoActiveLine: true, NoScrollPastEnd: true,
		}}
	})
	s.update(text)
	s.editor.Label = v.texts().ActivityOutput()
	box.Children(func() {
		height := float32(min(s.editor.Buffer.LineCount(), outputPreviewLines))*18 + 20
		if *expanded {
			height = min(float32(s.editor.Buffer.LineCount())*18+20, 360)
		}
		ui.Box(c).FillWidth().Height(height).Children(func() {
			editor.View(c, &s.editor).Fill()
			if truncated && !*expanded {
				ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(36).
					Gradient(c.Theme().Background.Alpha(0), c.Theme().Background, 180)
			}
		})
		if truncated {
			label, rotation := v.texts().ExpandOutput(), float32(90)
			if *expanded {
				label, rotation = v.texts().CollapseOutput(), 270
			}
			ui.ButtonBase(c).Label(label).Tooltip(label).FillWidth().Height(26).Center().Children(func() {
				ui.Icon(c, chevron).Size(14, 14).Rotate(rotation).TextColor(c.Theme().TextMuted)
			}).OnClick(func() { *expanded = !*expanded })
		}
	})
}
