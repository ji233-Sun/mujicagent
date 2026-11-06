package view

import (
	"bytes"
	"fmt"
	"image"
	"io"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/ui"
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
			w.Update(func() { v.attachments[key] = loaded })
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
	objects := map[int]*attachmentPreview{}
	offset := 0
	color := c.Theme().Accent
	ui.RichText(c).FontSize(14).Selectable().Children(func() {
		for i, part := range model.MessageParts(text) {
			if part.Attachment == nil {
				ui.Text(c.Key(i), part.Text)
				offset += utf8.RuneCountInString(part.Text)
				continue
			}
			a := *part.Attachment
			p := v.preview(a)
			objects[offset] = p
			status := "…"
			if p.ready {
				status = fileSize(p.size)
			}
			if p.err != nil {
				status = v.texts().PreviewUnavailable()
				if p.size > 0 {
					status = fileSize(p.size) + " · " + status
				}
			}
			label := " " + filepath.Base(a.Path) + " · " + status
			if a.Tip != "" {
				label += fmt.Sprintf(" (%.2f%%, %.2f%%) %s", a.X, a.Y, a.Tip)
			}
			ui.Link(c.Key(i), "", "").Label(filepath.Base(a.Path)).Tooltip(a.Path).Children(func() {
				ui.RichText(c, ui.Span{Text: "\ufffc", ObjectWidth: 18, ObjectHeight: 16}, ui.Span{Text: label, Color: color, Underline: true})
			}).OnClick(func() {
				v.attachment = a
				v.attachmentThread = threadID
				v.annotationTip = a.Tip
				v.annotationX, v.annotationY = a.X, a.Y
				v.annotationPoint = a.Tip != ""
				v.attachmentOpen = true
			})
			offset += 1 + utf8.RuneCountInString(label)
		}
	}).DrawObjects(func(painter *ui.Painter, at int, r ui.Rect) {
		if p := objects[at]; p != nil && p.bitmap != nil {
			painter.Image(p.bitmap, r, ui.Contain)
		} else {
			painter.Icon(fileEditIcon, r, color)
		}
	})
}

func (v *View) pickAttachments() {
	t := v.C.State.Thread(v.C.State.Selected)
	w := v.Window
	if t == nil || w == nil {
		return
	}
	id, draft, start, end := t.ID, t.Draft, v.draftStart, v.draftEnd
	title := v.texts().AttachFile()
	go func() {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: w, Title: title, Multiple: true})
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
			insert := strings.Join(links, " ")
			if t.Draft == draft {
				runes := []rune(draft)
				start, end = max(0, min(start, len(runes))), max(0, min(end, len(runes)))
				t.Draft = string(runes[:start]) + insert + string(runes[end:])
			} else {
				t.Draft += "\n" + insert
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
		ui.Text(c, v.attachment.Path).FontSize(11).Selectable()
		p := v.preview(v.attachment)
		if p.bitmap != nil {
			ui.Text(c, tx.AnnotationHint()).FontSize(12)
			w, h := p.bitmap.Size()
			scale := min(float32(620)/float32(w), float32(340)/float32(h), 1)
			bw, bh := float32(w)*scale, float32(h)*scale
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
				ui.Text(c, fmt.Sprintf("(%.2f%%, %.2f%%)", v.annotationX, v.annotationY))
			}
			ui.TextInput(c, &v.annotationTip).Label(tx.AnnotationTip()).Placeholder(tx.AnnotationTip())
			ui.PrimaryButton(c, tx.InsertAnnotation()).Disabled(!v.annotationPoint || strings.TrimSpace(v.annotationTip) == "" || v.C.State.Selected != v.attachmentThread).OnClick(func() {
				if t := v.C.State.Thread(v.attachmentThread); t != nil {
					a := v.attachment
					a.X, a.Y, a.Tip = v.annotationX, v.annotationY, strings.TrimSpace(v.annotationTip)
					if t.Draft != "" {
						t.Draft += "\n"
					}
					t.Draft += a.Link()
					v.C.Save()
				}
				v.attachmentOpen = false
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
