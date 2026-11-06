package view

import (
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func TestImageAnnotation(t *testing.T) {
	state := model.New()
	c := controller.New(t.TempDir(), state, nil)
	c.AddProject(t.TempDir())
	thread := state.Thread(state.Selected)
	a := model.Attachment{Kind: "image", Path: filepath.Join(t.TempDir(), "sample.png")}
	img := image.NewRGBA(image.Rect(0, 0, 800, 400))
	for y := 0; y < 400; y++ {
		for x := 0; x < 800; x++ {
			img.Set(x, y, color.RGBA{R: 210, G: 100, B: 60, A: 255})
		}
	}
	f, err := os.Create(a.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(f, img); err != nil {
		t.Fatal(err)
	}
	f.Close()
	thread.Turns = []model.Turn{{Prompt: "before " + a.Link() + " after", Status: "completed"}}
	thread.Draft = "keep this"
	v := New(c)
	v.attachments = map[string]*attachmentPreview{"image:" + a.Path: readPreview(a)}
	tt := ui.NewTester(v.Render, 1140, 780)
	tt.Frame()
	if err := tt.Click("sample.png"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	r, ok := tt.Find("标记图片")
	if !ok {
		t.Fatal("image preview not shown")
	}
	tt.ClickAt(r.X+r.W*0.25, r.Y+r.H*0.75)
	if !v.annotationPoint || math.Abs(v.annotationX-25) > 0.1 || math.Abs(v.annotationY-75) > 0.1 {
		t.Fatalf("point: %v,%v", v.annotationX, v.annotationY)
	}
	if err := tt.Click("标记说明"); err != nil {
		t.Fatal(err)
	}
	tt.Type("改成绿色")
	if err := tt.Click("插入标记"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	parts := model.MessageParts(thread.Draft)
	if len(parts) != 2 || parts[0].Text != "keep this\n" || parts[1].Attachment.Tip != "改成绿色" || parts[1].Attachment.X != 25 || parts[1].Attachment.Y != 75 {
		t.Fatalf("draft: %s", thread.Draft)
	}
	if strings.Contains(thread.Turns[0].Prompt, "tip=") {
		t.Fatal("sent message mutated")
	}
	if err := tt.Click("sample.png"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if output := os.Getenv("MUJICAGENT_ATTACHMENT_SCREENSHOT"); output != "" {
		f, err := os.Create(output)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
}

func TestAttachmentFallback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "unknown.weird")
	if err := os.WriteFile(path, []byte("abc"), 0600); err != nil {
		t.Fatal(err)
	}
	p := readPreview(model.Attachment{Kind: "file", Path: path})
	if p.err != nil || p.bitmap != nil || p.size != 3 {
		t.Fatalf("file preview: %+v", p)
	}
	if p := readPreview(model.Attachment{Kind: "image", Path: path}); p.err == nil || p.bitmap != nil {
		t.Fatal("invalid image accepted")
	}
	if p := readPreview(model.Attachment{Kind: "file", Path: path + "missing"}); p.err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestAttachmentComposer(t *testing.T) {
	state := model.New()
	c := controller.New(t.TempDir(), state, nil)
	c.AddProject(t.TempDir())
	thread := state.Thread(state.Selected)
	a := model.Attachment{Kind: "file", Path: filepath.Join(t.TempDir(), "data.xyz")}
	thread.Draft = "你好" + a.Link() + "后文"
	v := New(c)
	tt := ui.NewTester(v.Render, 1140, 780)
	tt.Frame()
	if tt.HasText(a.Link()) {
		t.Fatal("encoded attachment exposed in editor")
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("前文")
	if thread.Draft != "前文"+a.Link()+"后文" {
		t.Fatalf("edit lost attachments: %s", thread.Draft)
	}
	if v.draftStart != 2 || v.draftEnd != 2 {
		t.Fatalf("incorrect Unicode insertion offset: %d,%d", v.draftStart, v.draftEnd)
	}
	tt.Key(0, ui.KeyEnter)
	if thread.Draft != "前文"+a.Link()+"后文" {
		t.Fatal("failed send lost draft")
	}
	if err := tt.Click("移除附件"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	if thread.Draft != "前文后文" {
		t.Fatalf("remove lost text: %s", thread.Draft)
	}
	c.NewThread(thread.ProjectID)
	tt.Frame()
	if len(v.editorParts) != 1 || v.editorParts[0].Text != "" {
		t.Fatal("attachment leaked to new conversation")
	}
}
