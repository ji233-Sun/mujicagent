package view

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
	"github.com/ji233-Sun/mujicagent/internal/markup"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func (v *View) attachmentEditable() bool {
	return v.editor != nil && v.editorThread == v.attachmentThread && v.C.State.Selected == v.attachmentThread && v.editorQueue == v.attachmentQueue && v.C.State.Thread(v.attachmentThread) != nil
}
func (v *View) closeAttachment() {
	if v.annotation != nil {
		v.annotation.finishAdding()
	}
	if s := v.annotation; s != nil && notesKey(s.notes) != s.baseline {
		s.confirmClose = true
	} else {
		v.attachmentOpen = false
	}
}
func (v *View) applyAnnotations() {
	if !v.attachmentEditable() {
		return
	}
	s := v.annotation
	s.finishAdding()
	if !model.ValidAnnotations(s.notes) {
		return
	}
	a := v.attachment
	a.Tip = ""
	a.X = 0
	a.Y = 0
	a.Annotations = cloneNotes(s.notes)
	for i := range a.Annotations {
		a.Annotations[i].Comment = strings.TrimSpace(a.Annotations[i].Comment)
	}
	text := v.composerText(a.Link())
	replaced := false
	for at, run := range v.editor.Text().Runs(0, v.editor.Text().Len()) {
		if run.Attributes.Attachment != nil && run.Attributes.Attachment.URL == v.attachment.Link() {
			v.editor.Edit(func(e *richtext.Edit) { e.Replace(at, at+1, text) })
			replaced = true
			break
		}
	}
	if !replaced {
		v.insertComposer(a.Link())
	} else {
		v.syncComposer(v.C.ComposerDraft(v.C.State.Thread(v.attachmentThread)))
		v.C.Save()
	}
	v.attachmentOpen = false
}

func (v *View) attachmentDialog(c *ui.Context) {
	if !v.attachmentOpen {
		if v.annotation != nil {
			v.annotation.finishAdding()
		}
		return
	}
	tx := v.texts()
	s := v.annotation
	p := v.preview(v.attachment)
	cw, ch := c.Size()
	width, height := min(cw-40, 1240), min(ch-40, 860)
	compact := width < 760
	layoutKey := fmt.Sprintf("%g/%g/%t", cw, ch, s.sidebar)
	if layoutKey != s.layoutKey {
		s.layoutKey = layoutKey
		c.After(time.Millisecond)
	}
	panel := ui.DialogBase(c.Key("attachment-dialog"), &v.attachmentOpen, func(backdrop, panel ui.Element) {
		if s.addingTo != "" || s.dragging != "" {
			panel.OnShortcut(0, ui.KeyEscape, s.finishAdding)
		}
		backdrop.Background(ui.RGBA(20, 36, 30, .38))
		panel.Width(width).Height(height).MaxWidthPercent(96).MaxHeightPercent(96).Radius(18).Clip().Background(c.Theme().Background).Border(1, c.Theme().Border).Shadow(0, 18, 60, 0, ui.RGBA(0, 0, 0, .2))
		ui.Row(c).Height(76).Shrink(0).Padding(16, 22).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Box(c).Size(40, 40).Radius(11).Background(c.Theme().Surface).Center().Children(func() { ui.Icon(c, imageMarkIcon).Size(22, 22).TextColor(c.Theme().Accent) })
			ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
				ui.Text(c, filepath.Base(v.attachment.Path)).FontSize(16).Bold().SingleLine().Tooltip(v.attachment.Path)
				meta := tx.AttachmentLoading()
				if p.ready {
					meta = fileSize(p.size)
					if p.bitmap != nil {
						w, h := p.bitmap.Size()
						meta = fmt.Sprintf("%s  ·  %d × %d px  ·  %s", strings.ToUpper(p.format), w, h, fileSize(p.size))
					}
				}
				ui.Text(c, meta).FontSize(11).TextColor(c.Theme().TextMuted)
			})
			markTool(c.Key("sidebar-toggle"), tx.AnnotationSidebar(), sidebarMarkIcon, s.sidebar, func() {
				s.sidebar = !s.sidebar
				if !s.sidebar {
					s.finishAdding()
				}
			})
			markTool(c.Key("close-preview"), tx.CloseAttachment(), closeIcon, false, v.closeAttachment)
		})
		ui.Divider(c)
		body := ui.Row(c).Grow(1).MinHeight(0).AlignItems(ui.Stretch)
		if compact {
			body.Column()
		}
		body.Children(func() {
			v.annotationCanvas(c, p)
			if s.sidebar {
				side := ui.Column(c.Key("annotation-sidebar")).Shrink(0).Background(c.Theme().Background)
				if compact {
					side.FillWidth().Height(min(285, height*.42))
				} else {
					side.Width(304).FillHeight()
				}
				side.Children(func() { v.annotationSidebar(c, p, compact) })
			}
		})
		ui.Divider(c)
		ui.Row(c).Height(62).Shrink(0).Padding(12, 20).Gap(10).AlignItems(ui.Center).Children(func() {
			services := c.Services()
			path := v.attachment.Path
			markTool(c, tx.CopyAttachmentPath(), folder, false, func() { services.WriteClipboard(path) })
			ui.Box(c).Grow(1)
			if !model.ValidAnnotations(s.notes) {
				ui.Text(c, tx.AnnotationLimit()).FontSize(11).TextColor(c.Theme().Danger)
			} else if notesKey(s.notes) != s.baseline {
				ui.Text(c, tx.AnnotationUnapplied()).FontSize(11).TextColor(c.Theme().Accent)
			}
			ui.PrimaryButton(c, tx.ApplyAnnotations()).Height(36).Disabled(!v.attachmentEditable() || p.bitmap == nil || !model.ValidAnnotations(s.notes)).OnClick(v.applyAnnotations)
		})
	})
	if panel.Dismissed() {
		v.attachmentOpen = true
		v.closeAttachment()
	}
	if ui.AlertDialog(c.Key("discard-annotations"), &s.confirmClose, tx.DiscardAnnotationsTitle(), tx.DiscardAnnotationsMessage(), tx.KeepEditing(), tx.DiscardAnnotations()) == 1 {
		s.finishAdding()
		v.attachmentOpen = false
	}
}

func (v *View) annotationCanvas(c *ui.Context, p *attachmentPreview) {
	s := v.annotation
	tx := v.texts()
	ui.Column(c.Key("canvas-area")).Grow(1).MinWidth(0).MinHeight(100).Background(ui.Hex("#edf1ef")).Children(func() {
		ui.Row(c).Height(48).Shrink(0).Padding(8, 16).Gap(6).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tx.ImagePreview()).FontSize(11).TextColor(c.Theme().TextMuted).Grow(1)
			if p.bitmap != nil {
				ui.Button(c, tx.FitImage()).Height(28).FontSize(11).OnClick(func() { s.zoom = 0; s.pan = ui.Point{} })
				ui.Button(c, "1:1").Height(28).FontSize(11).OnClick(func() { s.zoom = 1; s.pan = ui.Point{} })
				markTool(c, tx.ZoomOut(), minusMarkIcon, false, func() { b := s.canvas.Bounds(c); s.zoomBy(.8, p.bitmap, b, ui.Point{X: b.W / 2, Y: b.H / 2}) })
				b := s.canvas.Bounds(c)
				r := s.imageRect(b.W, b.H, p.bitmap)
				w, _ := p.bitmap.Size()
				zoomLabel := tx.FitImage()
				if b.W > 0 && b.H > 0 {
					zoomLabel = fmt.Sprintf("%.0f%%", r.W/float32(w)*100)
				}
				ui.Text(c, zoomLabel).FontSize(11).Width(38).TextAlign(ui.Center).TextColor(c.Theme().TextMuted)
				markTool(c, tx.ZoomIn(), plus, false, func() { b := s.canvas.Bounds(c); s.zoomBy(1.25, p.bitmap, b, ui.Point{X: b.W / 2, Y: b.H / 2}) })
			}
		})
		canvas := ui.Box(c.Key("image-canvas")).Bind(&s.canvas).Grow(1).MinHeight(0).FillWidth().Clip().Label(tx.MarkImage()).Focusable()
		if p.bitmap != nil {
			cursor := ui.CursorCrosshair
			if s.tool == "pan" {
				cursor = ui.CursorGrab
				if s.dragging == "pan" || s.dragging == "move" {
					cursor = ui.CursorGrabbing
				}
			}
			canvas.Cursor(cursor).HandleInput(func(ev ui.InputEvent) bool { return s.input(ev, p.bitmap, s.canvas.Bounds(c)) })
			canvas.Draw(func(painter *ui.Painter, bounds ui.Rect) {
				painter.Clip(bounds, 0, func() {
					r := s.imageRect(bounds.W, bounds.H, p.bitmap)
					r.X += bounds.X
					r.Y += bounds.Y
					painter.Shadow(r, 3, 0, 4, 16, 0, ui.RGBA(0, 0, 0, .12))
					painter.Clip(r, 0, func() {
						painter.Fill(r, ui.Hex("#ffffff"), 0)
						// A subtle checkerboard reveals alpha without competing with the image.
						left, top := max(r.X, bounds.X), max(r.Y, bounds.Y)
						for y := top; y < min(r.Y+r.H, bounds.Y+bounds.H); y += 12 {
							for x := left; x < min(r.X+r.W, bounds.X+bounds.W); x += 12 {
								if (int((x-left)/12)+int((y-top)/12))%2 == 0 {
									painter.Fill(ui.Rect{X: x, Y: y, W: 12, H: 12}, ui.Hex("#f0f3f1"), 0)
								}
							}
						}
						painter.Image(p.bitmap, r, ui.Contain)
						notes, selected := s.displayNotes()
						markup.Draw(painter, notes, r, selected)
					})
				})
			})
		} else {
			canvas.Center().Children(func() {
				ui.Column(c).Gap(12).Padding(24).AlignItems(ui.Center).Children(func() {
					if !p.ready {
						ui.Spinner(c).Size(24, 24)
						ui.Text(c, tx.AttachmentLoading())
					} else if p.err != nil {
						ui.Icon(c, failureIcon).Size(32, 32).TextColor(c.Theme().Danger)
						ui.Text(c, tx.PreviewUnavailable())
						ui.Text(c, p.err.Error()).FontSize(12).TextColor(c.Theme().TextMuted).MaxWidth(430)
						ui.Button(c, tx.Retry()).OnClick(func() { delete(v.attachments, v.attachment.Kind+":"+v.attachment.Path) })
					} else {
						ui.Icon(c, fileEditIcon).Size(40, 40).TextColor(c.Theme().TextMuted)
						ui.Text(c, tx.NoImagePreview())
					}
				})
			})
		}
		if p.bitmap != nil {
			ui.Column(c).Shrink(0).AlignItems(ui.Center).Gap(6).Padding(10, 12).Children(func() {
				if number := s.addingNumber(); number > 0 {
					ui.Text(c, tx.AddingAnnotationShapes(fmt.Sprint(number))).FontSize(11).Bold().TextColor(c.Theme().Accent)
				}
				ui.Row(c).Radius(12).Padding(5, 7).Gap(3).Background(c.Theme().Background).Border(1, c.Theme().Border).Shadow(0, 4, 12, 0, ui.RGBA(0, 0, 0, .07)).Children(func() {
					for _, tool := range []struct {
						id, label string
						icon      *ui.SVG
					}{{"pan", tx.PanImage(), panMarkIcon}, {"pen", tx.DrawPen(), penMarkIcon}, {"rectangle", tx.DrawRectangle(), stopIcon}, {"arrow", tx.DrawArrow(), arrowMarkIcon}, {"point", tx.AddPointComment(), chat}} {
						markTool(c.Key(tool.id), tool.label, tool.icon, s.tool == tool.id, func() { s.chooseTool(tool.id) })
					}
					ui.Box(c).Width(1).Height(22).Margin(6, 5).Background(c.Theme().Border)
					markTool(c, tx.UndoAnnotation(), undoMarkIcon, false, func() { s.history(false) }).Disabled(len(s.undo) == 0)
					markTool(c, tx.RedoAnnotation(), redoMarkIcon, false, func() { s.history(true) }).Disabled(len(s.redo) == 0)
				})
			})
		}
	})
}

func (v *View) annotationSidebar(c *ui.Context, p *attachmentPreview, compact bool) {
	s := v.annotation
	tx := v.texts()
	ui.Row(c).Height(51).Shrink(0).Padding(14, 18).Gap(8).AlignItems(ui.Center).Children(func() {
		ui.Text(c, tx.AnnotationsAndComments()).Bold().FontSize(13).Grow(1)
		ui.Textf(c, "%02d", len(s.notes)).FontSize(11).TextColor(c.Theme().TextMuted)
		markTool(c, tx.AddImageComment(), plus, false, func() { s.add(nil) }).Disabled(p.bitmap == nil)
	})
	ui.Divider(c)
	ui.Scroll(c.Key("annotation-list")).Grow(1).MinHeight(0).Padding(12).Gap(8).Children(func() {
		if len(s.notes) == 0 {
			ui.Column(c).Padding(22, 12).Gap(10).AlignItems(ui.Center).Children(func() {
				ui.Box(c).Size(44, 44).Radius(14).Background(c.Theme().Surface).Center().Children(func() { ui.Icon(c, chat).Size(20, 20).TextColor(c.Theme().Accent) })
				ui.Text(c, tx.AnnotationEmptyTitle()).Bold().FontSize(13)
			})
		}
		for i := range s.notes {
			n := &s.notes[i]
			selected := n.ID == s.selected
			adding := n.ID == s.addingTo && selected
			card := ui.Column(c.Key(n.ID)).Padding(12).Gap(8).Radius(10).Border(1, c.Theme().Border).Background(c.Theme().Background)
			if selected {
				card.Border(2, c.Theme().Accent).Background(ui.Hex("#f1f8f5"))
			}
			if s.scrollTo == n.ID {
				card.ScrollIntoView()
				s.scrollTo = ""
			}
			card.Children(func() {
				ui.ButtonBase(c.Key("select")).Label(fmt.Sprintf("%s %d", tx.SelectAnnotation(), i+1)).FillWidth().OnClick(func() {
					s.selectNote(n.ID, p.bitmap, s.canvas.Bounds(c))
				}).Children(func() {
					ui.Row(c).FillWidth().Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Box(c).Size(22, 22).Radius(7).Background(annotationColor(n, c.Theme().Accent)).Center().Children(func() { ui.Textf(c, "%d", i+1).FontSize(11).Bold().TextColor(c.Theme().AccentText) })
						ui.Text(c, tx.AnnotationCommentNumber(fmt.Sprint(i+1))).FontSize(12).Bold().Grow(1)
						if selected {
							ui.Icon(c, checkIcon).Size(13, 13).TextColor(c.Theme().Accent)
						}
					})
				})
				ui.Row(c.Key("shape-count")).Gap(6).AlignItems(ui.Center).Children(func() {
					ui.Text(c, tx.AnnotationShapeCount(fmt.Sprint(len(n.Marks)))).FontSize(11).TextColor(c.Theme().TextMuted).Grow(1)
					for _, shape := range []struct {
						kind, label string
						icon        *ui.SVG
					}{{"pen", tx.DrawPen(), penMarkIcon}, {"rectangle", tx.DrawRectangle(), stopIcon}, {"arrow", tx.DrawArrow(), arrowMarkIcon}, {"point", tx.AddPointComment(), chat}} {
						for _, mark := range n.Marks {
							if mark.Kind == shape.kind {
								ui.Icon(c.Key(shape.kind), shape.icon).Size(14, 14).TextColor(c.Theme().TextMuted).Tooltip(shape.label)
								break
							}
						}
					}
				})
				if adding {
					ui.Row(c.Key("adding-status")).Gap(8).AlignItems(ui.Center).Children(func() {
						ui.Text(c, tx.AddingAnnotationShapes(fmt.Sprint(i+1))).FontSize(11).Bold().TextColor(c.Theme().Accent).Grow(1).MinWidth(0)
						ui.Button(c.Key("finish-adding"), tx.FinishAddingShapes()).Height(28).Shrink(0).FontSize(11).Tooltip(tx.FinishAddingShapes() + " · Esc").OnClick(s.finishAdding)
					})
				}
				if selected {
					before := cloneNotes(s.notes)
					ui.TextArea(c.Key("comment"), &n.Comment).Label(tx.AnnotationTip()).Placeholder(tx.AnnotationPlaceholder()).Lines(2, 5).FontSize(12).OnChange(func() { s.record(before) })
					ui.Row(c).Gap(6).AlignItems(ui.Center).Children(func() {
						if len(n.Marks) > 0 {
							q := n.Marks[0].Points[0]
							ui.Textf(c, "X %.1f%%  ·  Y %.1f%%", q.X, q.Y).FontSize(10).TextColor(c.Theme().TextMuted).Grow(1)
						} else {
							ui.Box(c).Grow(1)
						}
						markTool(c, tx.DeleteAnnotation(), closeIcon, false, func() { s.deleteNote(n.ID) })
					})
				} else {
					text := n.Comment
					if text == "" {
						text = tx.AnnotationNoComment()
					}
					ui.ButtonBase(c.Key("comment-preview")).Label(text).FillWidth().OnClick(func() {
						s.selectNote(n.ID, p.bitmap, s.canvas.Bounds(c))
					}).Children(func() {
						ui.Text(c, text).FillWidth().TextAlign(ui.Start).FontSize(12).TextColor(c.Theme().TextMuted)
					})
				}
				if !adding {
					ui.ButtonBase(c.Key("add-shape")).Label(tx.AddAnnotationShapeForNumber(fmt.Sprint(i + 1))).Tooltip(tx.AddAnnotationShapeForNumber(fmt.Sprint(i + 1))).FillWidth().Height(28).Radius(6).Gap(5).AlignItems(ui.Center).TextColor(c.Theme().Accent).Disabled(p.bitmap == nil).OnClick(func() {
						s.beginAdding(n.ID, p.bitmap, s.canvas.Bounds(c))
					}).Children(func() {
						ui.Icon(c, plus).Size(14, 14)
						ui.Text(c, tx.AddAnnotationShape()).FontSize(11)
					})
				}
			})
		}
	})
	ui.Divider(c)
	ui.Row(c).Padding(12, 16).Gap(8).AlignItems(ui.Center).Children(func() {
		for _, color := range []string{"#e05263", "#07896d", "#4775d1", "#d49726"} {
			b := ui.ButtonBase(c.Key(color)).Label(tx.MarkupColor()+" "+color).Tooltip(tx.MarkupColor()).Size(24, 24).Radius(12).Border(2, ui.Transparent).Padding(3)
			if s.color == color {
				b.Border(2, ui.Hex(color))
			}
			b.Children(func() { ui.Box(c).Fill().Radius(10).Background(ui.Hex(color)) }).OnClick(func() {
				before := cloneNotes(s.notes)
				s.color = color
				if n := s.current(); n != nil {
					for j := range n.Marks {
						n.Marks[j].Color = color
					}
				}
				s.record(before)
			})
		}
		ui.Box(c).Grow(1)
		for _, w := range []float32{2, 4, 6} {
			b := ui.ButtonBase(c.Key(w)).Label(fmt.Sprintf("%s %.0f", tx.MarkupWidth(), w)).Tooltip(tx.MarkupWidth()).Size(26, 26).Radius(6).Center()
			if s.width == w {
				b.Background(c.Theme().Surface)
			}
			b.Children(func() { ui.Box(c).Size(15, w).Radius(w / 2).Background(c.Theme().TextMuted) }).OnClick(func() {
				before := cloneNotes(s.notes)
				s.width = w
				if n := s.current(); n != nil {
					for j := range n.Marks {
						n.Marks[j].Width = w
					}
				}
				s.record(before)
			})
		}
	})
}

func annotationColor(n *model.Annotation, fallback ui.Color) ui.Color {
	if len(n.Marks) > 0 {
		return ui.Hex(n.Marks[0].Color)
	}
	return fallback
}
