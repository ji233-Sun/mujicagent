package view

import (
	"math"
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
	text          string
	editor        editor.State
	viewport      ui.Handle
	viewportWidth float32
	lineSpan      float64

	measured                    bool
	measuredVersion             uint64
	measuredFirst, measuredLast int
	widest                      float32
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

// Inspect only the editor's visible lines, caching their shaped width until
// the buffer or visible range changes. Scrolling/revealing stays owned by the
// editor; the neutral hint does not imply that more text is always to the right.
func (s *outputState) horizontalOverflow(c *ui.Context) bool {
	bounds := s.viewport.Bounds(c)
	s.viewportWidth = bounds.W
	if bounds.W <= 0 {
		c.AnimationFrame() // commit the first viewport before measuring against it
		return false
	}
	first, last := s.editor.VisibleLines()
	lineHeight := s.editor.LineHeight
	if lineHeight <= 0 {
		lineHeight = 1.5
	}
	// The editor's existing wrap command is internal to State. Observe its
	// laid-out line heights rather than pretending the option flag is its
	// current wrapping state. Wrapped visible lines need no sideways hint.
	s.lineSpan = s.editor.LineTop(last+1) - s.editor.LineTop(first)
	// Allow the editor's device-pixel line-height rounding before inferring
	// multiple visual rows from a logical line's height.
	if s.lineSpan > float64(last-first+1)*math.Ceil(float64(s.editor.Font.Size*lineHeight))+0.5 {
		s.measured = false
		s.measuredFirst, s.measuredLast = first, last
		return false
	}
	snapshot := s.editor.Buffer.Snapshot()
	if !s.measured || s.measuredVersion != snapshot.Version() || s.measuredFirst != first || s.measuredLast != last {
		s.widest = 0
		tabSize := s.editor.TabSize
		if tabSize <= 0 {
			if language := s.editor.Buffer.Language(); language != nil {
				tabSize = language.TabSize
			}
		}
		if tabSize <= 0 {
			tabSize = 4
		}
		tabSize = min(16, tabSize)
		for line := max(0, first); line <= last && line < snapshot.LineCount(); line++ {
			text := snapshot.Line(line)
			// Match this pinned editor's tab stops, including its
			// expansion cap. Ordinary output has no inlays or styled font faces.
			if strings.ContainsRune(text, '\t') {
				var expanded strings.Builder
				column := 0
				for _, r := range text {
					if r == '\t' {
						n := tabSize - column%tabSize
						if column >= 256 {
							n = 1
						}
						expanded.WriteString(strings.Repeat(" ", n))
						column += n
					} else {
						expanded.WriteRune(r)
						column++
					}
				}
				text = expanded.String()
			}
			width, _ := ui.ShapeText(text, s.editor.Font, 0).Size()
			s.widest = max(s.widest, width)
		}
		s.measured, s.measuredVersion = true, snapshot.Version()
		s.measuredFirst, s.measuredLast = first, last
	}
	// With line numbers/folding off, this editor starts text eight DIPs in.
	// Use the actual clipping edge, not its hover-only scrollbar's track.
	return s.widest > bounds.W-8+0.5
}

func (v *View) outputEditor(c *ui.Context, s *outputState, height float32, clipped bool) {
	services := c.Services()
	ui.Box(c).FillWidth().Height(height).Padding(4, 0).Children(func() {
		editor.View(c, &s.editor).Fill().Bind(&s.viewport).DrawOver(func(p *ui.Painter, r ui.Rect) {
			// VisibleLines and committed bounds update during painting/layout.
			// Rebuild once after a scroll/resize, even when syntax is settled.
			first, last := s.editor.VisibleLines()
			span := s.editor.LineTop(last+1) - s.editor.LineTop(first)
			if first != s.measuredFirst || last != s.measuredLast || r.W != s.viewportWidth || span != s.lineSpan {
				services.Invalidate()
			}
		})
		if clipped {
			ui.Box(c).Absolute().Left(0).Right(0).Bottom(0).Height(36).
				Gradient(c.Theme().Background.Alpha(0), c.Theme().Background, 180)
		}
	})
	if s.horizontalOverflow(c) {
		ui.Row(c).Height(24).Shrink(0).Padding(0, 8, 4).Gap(6).AlignItems(ui.Center).TextColor(c.Theme().TextMuted).Children(func() {
			ui.Text(c, "↔").FontSize(13)
			ui.Text(c, v.texts().ScrollOutputHorizontally()).FontSize(12)
		})
	}
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
			Buffer: buf, ReadOnly: true, Font: ui.Font{Family: "monospace", Size: 13},
			LineNumbers: editor.LineNumbersOff, NoFolding: true, NoIndentGuides: true,
			NoActiveLine: true, NoScrollPastEnd: true,
		}}
	})
	s.update(text)
	s.editor.Label = v.texts().ActivityOutput()
	box.Children(func() {
		height := float32(min(s.editor.Buffer.LineCount(), outputPreviewLines))*20 + 28
		if *expanded {
			height = min(float32(s.editor.Buffer.LineCount())*20+28, 360)
		}
		v.outputEditor(c, s, height, truncated && !*expanded)
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
