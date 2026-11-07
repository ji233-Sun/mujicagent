package view

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/transfer"
	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
	"github.com/ji233-Sun/mujicagent/internal/markup"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type attachmentPreview struct {
	bitmap *ui.Bitmap
	size   int64
	format string
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
	p.bitmap, p.size, p.format, err = markup.Load(a.Path)
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
	if a.Kind != "image" {
		return
	}
	p := v.preview(a)
	if !p.ready || p.err != nil || p.bitmap == nil {
		return
	}
	v.attachment, v.attachmentThread = a, threadID
	v.annotation = newAnnotationEditor(a.Notes())
	v.attachmentQueue = v.editorQueue
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
	edit := v.C.QueueEdit(id)
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
			if edit != v.C.QueueEdit(id) {
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
				v.syncComposer(v.C.ComposerDraft(t))
				v.editorHandle.Focus()
			} else {
				// Do not overwrite a draft that was replaced while the dialog was open.
				*v.C.ComposerDraft(t) += strings.Join(links, " ")
			}
			v.C.Save()
		})
	}()
}
