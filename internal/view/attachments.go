package view

import (
	"bytes"
	"crypto/sha256"
	"errors"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type attachmentPreview struct {
	bitmap *ui.Bitmap
	size   int64
	ready  bool
	err    error
}

// Bound decoding avoids allocating arbitrarily large bitmaps on file selection.
func readPreview(a model.Attachment) *attachmentPreview {
	p := &attachmentPreview{ready: true}
	info, err := os.Stat(a.Path)
	if err != nil {
		p.err = err
		return p
	}
	if !info.Mode().IsRegular() {
		p.err = fmt.Errorf("not a regular file: %s", a.Path)
		return p
	}
	p.size = info.Size()
	if a.Kind != "image" {
		return p
	}
	if info.Size() > 32<<20 {
		p.err = fmt.Errorf("image preview exceeds 32 MiB")
		return p
	}
	f, err := os.Open(a.Path)
	if err != nil {
		p.err = err
		return p
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, (32<<20)+1))
	if len(data) > 32<<20 {
		p.err = fmt.Errorf("image preview exceeds 32 MiB")
		return p
	}
	if err == nil {
		var config image.Config
		config, _, err = image.DecodeConfig(bytes.NewReader(data))
		if err == nil && (config.Width <= 0 || config.Height <= 0 || int64(config.Width)*int64(config.Height) > 24_000_000) {
			err = fmt.Errorf("image preview exceeds 24 megapixels")
		}
		if err == nil {
			p.bitmap, err = ui.DecodeBitmap(data)
		}
	}
	p.err = err
	return p
}

func (v *View) preview(a model.Attachment) *attachmentPreview {
	if v.attachments == nil {
		v.attachments = map[string]*attachmentPreview{}
	}
	key := a.Kind + ":" + a.Path
	if p := v.attachments[key]; p != nil {
		return p
	}
	p := &attachmentPreview{}
	v.attachments[key] = p
	if w := v.Window; w != nil {
		go func() {
			loaded := readPreview(a)
			w.Update(func() { *p = *loaded })
		}()
	}
	return p
}

func fileSize(size int64) string {
	if size < 1024 {
		return fmt.Sprintf("%d B", size)
	}
	if size < 1<<20 {
		return fmt.Sprintf("%.1f KiB", float64(size)/1024)
	}
	return fmt.Sprintf("%.1f MiB", float64(size)/(1<<20))
}

func (v *View) message(c *ui.Context, text, threadID string) {
	objects := map[int]*richtext.Attachment{}
	offset := 0
	ui.RichText(c).FontSize(14).Selectable().Children(func() {
		for i, part := range model.MessageParts(text) {
			if part.Attachment == nil {
				ui.Text(c.Key(i), part.Text)
				offset += utf8.RuneCountInString(part.Text)
				continue
			}
			a := *part.Attachment
			object := v.attachmentObject(a)
			objects[offset] = object
			tooltip := a.Path
			if a.Tip != "" {
				tooltip += fmt.Sprintf("\n(%.2f%%, %.2f%%) %s", a.X, a.Y, a.Tip)
			}
			ui.RichText(c.Key(i), ui.Span{Text: string(richtext.ObjectReplacement), ObjectWidth: object.Width, ObjectHeight: object.Height}).Label(object.Name).Role(ui.RoleLink).Focusable().Cursor(ui.CursorPointer).Tooltip(tooltip).OnClick(func() { v.openAttachment(a, threadID) })
			offset++
		}
	}).DrawObjects(func(p *ui.Painter, at int, r ui.Rect) {
		if object := objects[at]; object != nil {
			object.Draw(p, r)
		}
	})
}

func (v *View) openAttachment(a model.Attachment, threadID string) {
	v.attachment, v.attachmentThread = a, threadID
	v.annotationTip = a.Tip
	v.annotationX, v.annotationY = a.X, a.Y
	v.annotationPoint = a.Tip != ""
	v.attachmentOpen = true
}

func (v *View) pickAttachments() {
	w := v.Window
	if w == nil || v.editor == nil {
		return
	}
	title := v.texts().AttachFile()
	v.loadAttachments(func() ([]string, error) {
		return mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: w, Title: title, Multiple: true})
	})
}

func (v *View) pasteAttachments(data transfer.Data) bool {
	paths, err := data.Files()
	if err != nil && !errors.Is(err, transfer.ErrFormat) {
		v.C.ReportError(err.Error())
		return true
	}
	if len(paths) > 0 {
		v.loadAttachments(func() ([]string, error) { return paths, nil })
		return true
	}
	for _, item := range data.Items() {
		imageData, err := item.Read(transfer.PNG)
		if err != nil {
			continue
		}
		dir := filepath.Join(v.C.Dir, "attachments")
		v.loadAttachments(func() ([]string, error) {
			if len(imageData) > 32<<20 {
				return nil, fmt.Errorf("image preview exceeds 32 MiB")
			}
			path := filepath.Join(dir, fmt.Sprintf("%x", sha256.Sum256(imageData)), "image.png")
			if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
				return nil, err
			}
			if err := os.WriteFile(path, imageData, 0600); err != nil {
				return nil, err
			}
			return []string{path}, nil
		})
		return true
	}
	return false
}

// Capture the document and selection before a dialog or background file read.
func (v *View) loadAttachments(load func() ([]string, error)) {
	if v.editor == nil || v.Window == nil {
		return
	}
	doc, id, w := v.editor, v.editorThread, v.Window
	a, b := doc.Selection()
	start, end, version := min(a, b), max(a, b), doc.Version()
	go func() {
		paths, err := load()
		var links []string
		loaded := map[string]*attachmentPreview{}
		for _, path := range paths {
			a := model.Attachment{Kind: "image", Path: path}
			p := readPreview(a)
			if p.bitmap == nil {
				a.Kind = "file"
				p = readPreview(a)
			}
			if p.err != nil {
				err = p.err
				break
			}
			loaded[a.Kind+":"+a.Path] = p
			links = append(links, a.Link())
		}
		w.Update(func() {
			if err != nil {
				v.C.ReportError(err.Error())
				return
			}
			t := v.C.State.Thread(id)
			if t == nil || len(links) == 0 {
				return
			}
			if v.attachments == nil {
				v.attachments = map[string]*attachmentPreview{}
			}
			for k, p := range loaded {
				v.attachments[k] = p
			}
			// A changed draft keeps its edits; insert at its current caret instead.
			if doc.Version() != version {
				a, b = doc.Selection()
				start, end = min(a, b), max(a, b)
			}
			text := v.composerText(strings.Join(links, " "))
			doc.Edit(func(e *richtext.Edit) { e.Replace(start, end, text); e.Select(start+text.Len(), start+text.Len()) })
			if v.editor == doc && v.editorThread == id {
				v.syncComposer(&t.Draft)
				v.editorHandle.Focus()
			} else {
				// Do not overwrite a draft that was replaced while the dialog was open.
				t.Draft += strings.Join(links, " ")
			}
			v.C.Save()
		})
	}()
}

func (v *View) attachmentDialog(c *ui.Context) {
	tx := v.texts()
	ui.DialogBase(c.Key("attachment-dialog"), &v.attachmentOpen, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(0, 0, 0, 0.25))
		panel.Width(740).MaxWidthPercent(94).MaxHeightPercent(92).Padding(20).Gap(12).Radius(14).Background(c.Theme().Background)
		ui.Text(c, filepath.Base(v.attachment.Path)).Bold()
		p := v.preview(v.attachment)
		if p.bitmap != nil {
			ui.Text(c, tx.AnnotationHint()).FontSize(12)
			w, h := p.bitmap.Size()
			scale := min(float32(620)/float32(w), float32(340)/float32(h), 1)
			bw, bh := float32(w)*scale, float32(h)*scale
			ui.Box(c).Size(bw, bh).AlignSelf(ui.Center).Children(func() {
				area := ui.ButtonBase(c.Key("image-point")).Label(tx.MarkImage()).Size(bw, bh).Padding(0).AlignSelf(ui.Center)
				bitmap, marked, x, y := p.bitmap, v.annotationPoint, v.annotationX, v.annotationY
				area.Draw(func(painter *ui.Painter, r ui.Rect) {
					painter.Image(bitmap, r, ui.Contain)
					if marked {
						point := ui.Rect{X: r.X + float32(x/100)*r.W - 6, Y: r.Y + float32(y/100)*r.H - 6, W: 12, H: 12}
						painter.Stroke(point, ui.Hex("#ff3355"), 6, 3)
					}
				})
				if area.Clicked() {
					x, y, over := area.PointerPosition()
					r := area.Bounds()
					if over && r.W > 0 && r.H > 0 {
						v.annotationX = float64(max(float32(0), min(x/r.W, 1))) * 100
						v.annotationY = float64(max(float32(0), min(y/r.H, 1))) * 100
						v.annotationPoint = true
					}
				}
				if v.annotationPoint {
					px, py := float32(v.annotationX/100)*bw, float32(v.annotationY/100)*bh
					bubbleWidth := min(float32(260), bw)
					left := max(float32(0), min(px-30, bw-bubbleWidth))
					top := py - 112
					direction := float32(-1)
					if top < 0 {
						top = py + 25
						direction = 1
					}
					ui.Box(c).Absolute().Left(px-3).Top(py+direction*9-3).Size(7, 7).Radius(4).Background(c.Theme().Background)
					ui.Box(c).Absolute().Left(px-7).Top(py+direction*20-5).Size(11, 11).Radius(6).Background(c.Theme().Background)
					ui.Column(c.Key("annotation-bubble")).Absolute().Left(left).Top(top).Width(bubbleWidth).Padding(12).Gap(8).Radius(18).Background(c.Theme().Background).Border(1, c.Theme().Border).Children(func() {
						ui.TextInput(c, &v.annotationTip).Label(tx.AnnotationTip()).Placeholder(tx.AnnotationTip())
						ui.PrimaryButton(c, tx.InsertAnnotation()).Disabled(strings.TrimSpace(v.annotationTip) == "" || v.C.State.Selected != v.attachmentThread).OnClick(func() {
							if t := v.C.State.Thread(v.attachmentThread); t != nil {
								a := v.attachment
								a.X, a.Y, a.Tip = v.annotationX, v.annotationY, strings.TrimSpace(v.annotationTip)
								replaced := false
								for at, run := range v.editor.Text().Runs(0, v.editor.Text().Len()) {
									if run.Attributes.Attachment != nil && run.Attributes.Attachment.URL == v.attachment.Link() {
										text := v.composerText(a.Link())
										v.editor.Edit(func(e *richtext.Edit) { e.Replace(at, at+1, text) })
										v.syncComposer(&t.Draft)
										replaced = true
										break
									}
								}
								if !replaced {
									v.insertComposer(a.Link())
								}
								v.C.Save()
							}
							v.attachmentOpen = false
						})
					})
				}
			})
		} else if p.err != nil {
			ui.Text(c, tx.PreviewUnavailable()+": "+p.err.Error())
		} else if !p.ready {
			ui.Text(c, "…")
		} else {
			ui.Text(c, fileSize(p.size))
		}
		ui.Button(c, tx.CloseAttachment()).OnClick(func() { v.attachmentOpen = false })
	})
}
