package view

import (
	"fmt"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/egoist/mygo/ui"
	"github.com/egoist/mygo/ui/richtext"
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
	if err := tt.Click("位置评论"); err != nil {
		t.Fatal(err)
	}
	r, ok := tt.Find("标记图片")
	if !ok {
		t.Fatal("image preview not shown")
	}
	ir := v.annotation.imageRect(r.W, r.H, v.preview(a).bitmap)
	tt.ClickAt(r.X+ir.X+ir.W*0.25, r.Y+ir.Y+ir.H*0.75)
	if len(v.annotation.notes) == 0 {
		t.Fatal("point was not added")
	}
	point := v.annotation.notes[0].Marks[0].Points[0]
	if math.Abs(point.X-25) > 0.1 || math.Abs(point.Y-75) > 0.1 {
		t.Fatalf("point: %+v", point)
	}
	if err := tt.Click("标记说明"); err != nil {
		t.Fatal(err)
	}
	tt.Type("改成绿色")
	if err := tt.Click("应用到当前草稿"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Frame()
	parts := model.MessageParts(thread.Draft)
	if len(parts) != 2 || parts[0].Text != "keep this" || parts[1].Attachment.Annotations[0].Comment != "改成绿色" || math.Abs(parts[1].Attachment.Annotations[0].Marks[0].Points[0].X-25) > 0.1 {
		t.Fatalf("draft: %s", thread.Draft)
	}
	// Editing a draft annotation replaces the same inline attachment, and undo
	// restores it without losing the text on either side.
	original := thread.Draft + " after"
	thread.Draft = original
	v.openAttachment(*parts[1].Attachment, thread.ID)
	tt.Frame()
	if err := tt.Click("选择标注 1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("标记说明"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("改成蓝色")
	if err := tt.Click("应用到当前草稿"); err != nil {
		t.Fatal(err)
	}
	updated := model.MessageParts(thread.Draft)
	if len(updated) != 3 || updated[0].Text != "keep this" || updated[1].Attachment.Annotations[0].Comment != "改成蓝色" || updated[2].Text != " after" {
		t.Fatalf("annotation split or duplicated attachment: %s", thread.Draft)
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if thread.Draft != original {
		t.Fatalf("annotation undo: %s", thread.Draft)
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

func TestAttachmentOpening(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"video.mp4", "broken.png", "large.png", "valid.png"} {
		f, err := os.Create(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		switch name {
		case "valid.png":
			err = png.Encode(f, image.NewRGBA(image.Rect(0, 0, 16, 16)))
		case "large.png":
			err = f.Truncate((32 << 20) + 1)
		default:
			_, err = f.WriteString("not an image")
		}
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name, kind string
		loading    bool
		wantOpen   bool
	}{
		{"video.mp4", "file", false, false},
		{"broken.png", "file", false, false},
		{"large.png", "file", false, false},
		{"broken.png", "image", false, false},
		{"large.png", "image", false, false},
		{"missing.png", "image", false, false},
		{"valid.png", "image", true, false},
		{"valid.png", "image", false, true},
	} {
		for _, entry := range []string{"message", "draft"} {
			t.Run(fmt.Sprintf("%s/%s/%s/loading=%v", entry, tc.kind, tc.name, tc.loading), func(t *testing.T) {
				state := model.New()
				ctrl := controller.New(t.TempDir(), state, nil)
				ctrl.NewThread("")
				thread := state.Thread(state.Selected)
				a := model.Attachment{Kind: tc.kind, Path: filepath.Join(dir, tc.name)}
				thread.Draft = a.Link()
				v := New(ctrl)
				p := readPreview(a)
				if tc.loading {
					p = &attachmentPreview{}
				}
				v.attachments = map[string]*attachmentPreview{a.Kind + ":" + a.Path: p}
				tt := ui.NewTester(func(c *ui.Context) {
					if entry == "message" {
						v.message(c, a.Link(), thread.ID)
					} else {
						v.composerInput(c, &thread.Draft)
					}
				}, 600, 200)
				tt.Frame()
				if entry == "message" {
					if err := tt.Click(tc.name); err != nil {
						t.Fatal(err)
					}
				} else {
					r, ok := tt.Find("消息")
					if !ok {
						t.Fatal("composer missing")
					}
					tt.ClickAt(r.X+8, r.Y+8)
				}
				for i := 0; i < 3; i++ {
					tt.Frame()
				}
				if v.attachmentOpen != tc.wantOpen || (v.annotation != nil) != tc.wantOpen {
					t.Fatalf("opened=%v, editor=%v, want %v", v.attachmentOpen, v.annotation != nil, tc.wantOpen)
				}
				if thread.Draft != a.Link() {
					t.Fatal("click changed draft")
				}
			})
		}
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
	// Replace only the text before the inline object.
	v.editor.SetSelection(0, 2)
	tt.Frame()
	tt.Type("前文")
	if thread.Draft != "前文"+a.Link()+"后文" {
		t.Fatalf("edit lost attachments: %s", thread.Draft)
	}
	// A single editor selects and copies text and attachments together.
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(ui.Cmd, ui.KeyC)
	if tt.Clipboard() != thread.Draft {
		t.Fatalf("copy: %s", tt.Clipboard())
	}
	tt.Key(0, ui.KeyRight)
	tt.Key(0, ui.KeyEnter)
	if thread.Draft != "前文"+a.Link()+"后文" {
		t.Fatal("failed send lost draft")
	}
	v.editor.SetSelection(3, 3)
	tt.Frame()
	tt.Key(0, ui.KeyBackspace)
	if thread.Draft != "前文后文" {
		t.Fatalf("delete split text: %s", thread.Draft)
	}
	tt.Key(ui.Cmd, ui.KeyZ)
	if thread.Draft != "前文"+a.Link()+"后文" {
		t.Fatalf("undo: %s", thread.Draft)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.SetClipboard("粘贴" + a.Link() + "末尾")
	tt.Key(ui.Cmd, ui.KeyV)
	if thread.Draft != "粘贴"+a.Link()+"末尾" || v.editor.Text().Len() != 5 {
		t.Fatalf("paste: %s", thread.Draft)
	}
	c.NewThread(thread.ProjectID)
	tt.Frame()
	if v.editor.Text().Len() != 0 {
		t.Fatal("attachment leaked to new conversation")
	}
}

func TestAttachmentDeletionTyping(t *testing.T) {
	for _, kind := range []string{"image", "file"} {
		for _, action := range []string{"backspace", "delete", "selection", "replace"} {
			t.Run(kind+"/"+action, func(t *testing.T) {
				state := model.New()
				c := controller.New(t.TempDir(), state, nil)
				c.NewThread("")
				thread := state.Thread(state.Selected)
				a := model.Attachment{Kind: kind, Path: filepath.Join(t.TempDir(), "sample.png")}
				thread.Draft = a.Link() + "后文"
				v := New(c)
				tt := ui.NewTester(v.Render, 1140, 780)
				tt.Frame()
				if err := tt.Click("消息"); err != nil {
					t.Fatal(err)
				}
				switch action {
				case "backspace":
					v.editor.SetSelection(1, 1)
				case "delete":
					v.editor.SetSelection(0, 0)
				default:
					v.editor.SetSelection(0, 1)
				}
				tt.Frame()
				if action == "delete" {
					tt.Key(0, ui.KeyDelete)
				} else if action != "replace" {
					tt.Key(0, ui.KeyBackspace)
				}
				tt.Type("目前")
				if thread.Draft != "目前后文" {
					t.Fatalf("draft = %q", thread.Draft)
				}
				for _, run := range v.editor.Text().Runs(0, v.editor.Text().Len()) {
					if run.Attributes != (richtext.Attributes{}) {
						t.Fatalf("ordinary text retained attachment formatting: %+v", run.Attributes)
					}
				}
				tt.Key(ui.Cmd, ui.KeyZ)
				if action != "replace" && thread.Draft != a.Link()+"后文" {
					tt.Key(ui.Cmd, ui.KeyZ)
				}
				if thread.Draft != a.Link()+"后文" || v.editor.Text().AttributesAt(0).Link != a.Link() {
					t.Fatalf("undo did not restore attachment: %q", thread.Draft)
				}
			})
		}
	}
}

func TestAttachmentDeletionPreview(t *testing.T) {
	state := model.New()
	c := controller.New(t.TempDir(), state, nil)
	c.NewThread("")
	thread := state.Thread(state.Selected)
	thread.Draft = (model.Attachment{Kind: "file", Path: "/tmp/example.txt"}).Link()
	v := New(c)
	tt := ui.NewTester(v.Render, 1140, 780)
	tt.Frame()
	// Set the state left by richtext deletion directly, without UI input.
	attrs := v.editor.Text().AttributesAt(0)
	v.editor.Edit(func(e *richtext.Edit) {
		e.Replace(0, 1, richtext.Text{})
		e.Select(0, 0)
	})
	v.editor.SetTypingAttributes(attrs)
	v.syncComposer(&thread.Draft)
	tt.Frame()
	attrs = v.editor.TypingAttributes()
	if attrs != (richtext.Attributes{}) {
		t.Fatalf("deleted attachment still supplies typing attributes: %+v", attrs)
	}
	v.editor.Edit(func(e *richtext.Edit) { e.ReplaceString(0, 0, "目前", attrs) })
	v.syncComposer(&thread.Draft)
	tt.Frame()
	if thread.Draft != "目前" {
		t.Fatalf("draft after attachment deletion = %q", thread.Draft)
	}
	// HasText inspects element labels, not the custom-painted richtext content;
	// a matching draft-derived conversation title can hide a broken assertion.
	if !v.editor.Text().Equal(richtext.NewText("目前")) {
		t.Fatalf("editor must contain unformatted text after attachment deletion: %+v", v.editor.Text().Runs(0, v.editor.Text().Len()))
	}
	if output := os.Getenv("MUJICAGENT_DELETION_SCREENSHOT"); output != "" {
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

func TestComposerClipsOverflow(t *testing.T) {
	for _, width := range []int{840, 1140} {
		state := model.New()
		c := controller.New(t.TempDir(), state, nil)
		c.NewThread("")
		thread := state.Thread(state.Selected)
		thread.Draft = "短文"
		v := New(c)
		tt := ui.NewTester(func(ctx *ui.Context) { v.composer(ctx, thread) }, width, 300)
		tt.Frame()
		bounds, ok := tt.Find("消息")
		if !ok {
			t.Fatal("composer missing")
		}
		baseline := tt.Image()
		attachment := model.Attachment{Kind: "file", Path: "/tmp/" + strings.Repeat("long-filename", 30) + ".txt"}
		for _, draft := range []string{strings.Repeat("中文长段落 mixed text ", 100), attachment.Link()} {
			thread.Draft = draft
			tt.Frame()
			for _, end := range []bool{false, true} {
				caret := 0
				if end {
					caret = v.editor.Text().Len()
				}
				v.editor.SetSelection(caret, caret)
				tt.Frame()
				got := tt.Image()
				for y := 0; y < got.Bounds().Dy(); y++ {
					for x := 0; x < got.Bounds().Dx(); x++ {
						if float32(x) >= bounds.X && float32(x) < bounds.X+bounds.W && float32(y) >= bounds.Y && float32(y) < bounds.Y+bounds.H {
							continue
						}
						if got.RGBAAt(x, y) != baseline.RGBAAt(x, y) {
							t.Fatalf("width %v, caret at end %v: editor painted outside bounds %+v at (%d, %d)", width, end, bounds, x, y)
						}
					}
				}
			}
		}
	}
}

func TestQueuedComposerAndDrag(t *testing.T) {
	state := model.New()
	ctrl := controller.New(t.TempDir(), state, nil)
	ctrl.AddProject(t.TempDir())
	thread := state.Thread(state.Selected)
	thread.Draft = "preserved draft"
	thread.Queue = []model.QueuedMessage{{ID: "a", Text: "first message"}, {ID: "b", Text: "second message"}}
	v := New(ctrl)
	tt := ui.NewTester(v.Render, 1140, 780)
	tt.Frame()
	if err := tt.Click("编辑待发送消息"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if ctrl.QueueEdit(thread.ID) == nil || v.editorSource != "first message" {
		t.Fatal("not loaded into composer")
	}
	v.insertComposer(" edited")
	tt.Frame()
	tt.Key(0, ui.KeyEnter)
	tt.Frame()
	if thread.Queue[0].Text != "first message edited" || thread.Draft != "preserved draft" {
		t.Fatal("save lost text or draft")
	}
	a, ok := tt.Find("first message edited")
	if !ok {
		t.Fatal("source missing")
	}
	b, ok := tt.Find("second message")
	if !ok {
		t.Fatal("target missing")
	}
	tt.Press(a.X+10, a.Y+5)
	tt.Move(b.X+10, b.Y+5)
	tt.Frame()
	tt.Release(b.X+10, b.Y+5)
	tt.Frame()
	if thread.Queue[1].ID != "a" {
		t.Fatal("whole card did not drag")
	}
	if err := tt.Click("编辑待发送消息"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	tt.Key(0, ui.KeyEscape)
	tt.Frame()
	if ctrl.QueueEdit(thread.ID) != nil || v.editorSource != thread.Draft {
		t.Fatal("cancel did not restore draft")
	}
	if err := tt.Click("删除"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if len(thread.Queue) != 1 {
		t.Fatal("delete failed")
	}
}

func TestAnnotationCommentLayout(t *testing.T) {
	state := model.New()
	v := New(controller.New(t.TempDir(), state, nil))
	long := strings.Repeat("长评论应自动换行并完整显示在卡片内。", 20)
	v.annotation = newAnnotationEditor([]model.Annotation{{ID: "first", Comment: long}, {ID: "second", Comment: "下一条评论"}})
	p := &attachmentPreview{ready: true, bitmap: ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 10, 10)))}
	tt := ui.NewTester(func(c *ui.Context) {
		ui.Column(c).Fill().Children(func() { v.annotationSidebar(c, p, false) })
	}, 304, 1000)
	bounds := func(label string) ui.Rect {
		t.Helper()
		r, ok := tt.Find(label)
		if !ok {
			t.Fatalf("missing %q", label)
		}
		return r
	}
	check := func() ui.Rect {
		t.Helper()
		body := bounds(v.annotation.notes[0].Comment)
		next := bounds("选择标注 2")
		// Two card paddings plus the list gap must separate the body and next header.
		if next.Y < body.Y+body.H+32 {
			t.Fatalf("overlapping cards: body=%+v next=%+v", body, next)
		}
		return body
	}
	narrow := check()
	if narrow.H <= 52 {
		t.Fatalf("long comment height capped: %+v", narrow)
	}
	tt.SetSize(500, 1000)
	wide := check()
	if wide.H >= narrow.H {
		t.Fatalf("comment did not reflow: narrow=%+v wide=%+v", narrow, wide)
	}
	// Exercise actual editor input, then return to the preview.
	if err := tt.Click("选择标注 1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("标记说明"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type("短评论")
	v.annotation.selected = ""
	tt.Frame()
	short := check()
	if short.H >= wide.H {
		t.Fatal("card did not shrink after editing")
	}
	if err := tt.Click("选择标注 1"); err != nil {
		t.Fatal(err)
	}
	if err := tt.Click("标记说明"); err != nil {
		t.Fatal(err)
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Type(long)
	v.annotation.selected = ""
	tt.Frame()
	if check().H <= short.H {
		t.Fatal("card did not grow after editing")
	}
	tt.SetSize(304, 300)
	tt.Scroll(150, 150, 0, 2000)
	tt.Frame()
	last := bounds("下一条评论")
	if last.Y < 52 || last.Y+last.H > 250 {
		t.Fatalf("last comment not reachable by scrolling: %+v", last)
	}
}

func TestMarkupCommentWorkflow(t *testing.T) {
	state := model.New()
	ctrl := controller.New(t.TempDir(), state, nil)
	ctrl.NewThread("")
	thread := state.Thread(state.Selected)
	a := model.Attachment{Kind: "image", Path: filepath.Join(t.TempDir(), "layout.png")}
	bitmap := ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1000, 600)))
	v := New(ctrl)
	v.attachments = map[string]*attachmentPreview{"image:" + a.Path: {bitmap: bitmap, ready: true, size: 4096, format: "png"}}
	thread.Draft = "Before " + a.Link() + " after"
	tt := ui.NewTester(v.Render, 1140, 780)
	tt.Frame()
	v.openAttachment(a, thread.ID)
	tt.Frame()
	click := func(label string) {
		t.Helper()
		if err := tt.Click(label); err != nil {
			t.Fatal(err)
		}
		tt.Frame()
	}
	draw := func(tool string, x1, y1, x2, y2 float32) {
		t.Helper()
		click(tool)
		b, _ := tt.Find("标记图片")
		r := v.annotation.imageRect(b.W, b.H, bitmap)
		tt.Press(b.X+r.X+r.W*x1, b.Y+r.Y+r.H*y1)
		tt.Move(b.X+r.X+r.W*(x1+x2)/2, b.Y+r.Y+r.H*(y1+y2)/2)
		tt.Frame()
		tt.Move(b.X+r.X+r.W*x2, b.Y+r.Y+r.H*y2)
		tt.Frame()
		tt.Release(b.X+r.X+r.W*x2, b.Y+r.Y+r.H*y2)
		tt.Frame()
	}
	draw("矩形", .15, .2, .6, .65)
	if len(v.annotation.notes) != 1 || v.annotation.notes[0].Marks[0].Kind != "rectangle" {
		t.Fatal("rectangle missing")
	}
	click("标记说明")
	tt.Type("Make these align")
	tt.Frame()
	click("撤销标注")
	if v.annotation.notes[0].Comment != "" {
		t.Fatal("comment undo failed")
	}
	click("重做标注")
	if v.annotation.notes[0].Comment != "Make these align" {
		t.Fatal("comment redo failed")
	}
	click("新图形关联到此评论")
	draw("箭头", .3, .4, .7, .6)
	if len(v.annotation.notes) != 1 || len(v.annotation.notes[0].Marks) != 2 || v.annotation.notes[0].Comment != "Make these align" {
		t.Fatalf("grouped marks: %+v", v.annotation.notes)
	}
	click("撤销标注")
	if len(v.annotation.notes[0].Marks) != 1 {
		t.Fatal("undo did not remove only arrow")
	}
	click("重做标注")
	if len(v.annotation.notes[0].Marks) != 2 {
		t.Fatal("redo lost arrow")
	}
	// Redo deliberately clears linking; a pen stroke starts a new annotation.
	draw("画笔", .1, .8, .4, .8)
	if len(v.annotation.notes) != 2 || v.annotation.notes[1].Marks[0].Kind != "pen" {
		t.Fatal("pen missing")
	}
	click("添加整体说明")
	click("标记说明")
	tt.Type("Keep the overall spacing")
	tt.Frame()
	if len(v.annotation.notes) != 3 || len(v.annotation.notes[2].Marks) != 0 {
		t.Fatal("whole-image note missing")
	}
	click("选择标注 1")
	if v.annotation.current() == nil || v.annotation.current().Comment != "Make these align" {
		t.Fatalf("selection did not restore comment: selected=%s notes=%+v texts=%v", v.annotation.selected, v.annotation.notes, tt.Texts())
	}
	// Zoomed image painting must stay inside the canvas, including alpha backgrounds.
	headerPixel := tt.Image().RGBAAt(700, 60)
	click("1:1")
	if tt.Image().RGBAAt(700, 60) != headerPixel {
		t.Fatal("zoomed image overpainted the header")
	}
	click("适应窗口")
	// Keep the same normalized point under the cursor while zooming.
	b, _ := tt.Find("标记图片")
	r := v.annotation.imageRect(b.W, b.H, bitmap)
	pivot := ui.Point{X: r.X + r.W*.35, Y: r.Y + r.H*.4}
	old := normalized(pivot, r)
	v.annotation.zoomBy(1.4, bitmap, b, pivot)
	next := normalized(pivot, v.annotation.imageRect(b.W, b.H, bitmap))
	if math.Abs(old.X-next.X) > .01 || math.Abs(old.Y-next.Y) > .01 {
		t.Fatalf("zoom moved pivot: %+v -> %+v", old, next)
	}
	click("适应窗口")
	// Hiding the sidebar preserves marks; clicking a mark reveals its comment.
	click("显示或收起评论栏")
	click("选择标注")
	b, _ = tt.Find("标记图片")
	r = v.annotation.imageRect(b.W, b.H, bitmap)
	tt.ClickAt(b.X+r.X+r.W*.15, b.Y+r.Y+r.H*.2)
	tt.Frame()
	if !v.annotation.sidebar || v.annotation.selected != v.annotation.notes[0].ID {
		t.Fatal("mark did not reveal sidebar")
	}
	// Dirty close offers a choice and Escape keeps edits intact.
	click("关闭附件")
	if !v.annotation.confirmClose || !v.attachmentOpen {
		t.Fatal("dirty editor closed without confirmation")
	}
	click("继续编辑")
	if len(v.annotation.notes) != 3 {
		t.Fatal("close confirmation lost notes")
	}
	if output := os.Getenv("MUJICAGENT_MARKUP_SCREENSHOT"); output != "" {
		f, err := os.Create(output)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	// The minimum native window keeps the canvas and apply action accessible.
	tt.SetSize(840, 560)
	tt.Frame()
	b, _ = tt.Find("标记图片")
	if b.W < 200 || b.H < 50 {
		t.Fatalf("small window canvas: %+v", b)
	}
	if output := os.Getenv("MUJICAGENT_MARKUP_SMALL_SCREENSHOT"); output != "" {
		f, err := os.Create(output)
		if err != nil {
			t.Fatal(err)
		}
		defer f.Close()
		if err := png.Encode(f, tt.Image()); err != nil {
			t.Fatal(err)
		}
	}
	click("应用到当前草稿")
	parts := model.MessageParts(thread.Draft)
	if len(parts) != 3 || parts[0].Text != "Before " || parts[2].Text != " after" || len(parts[1].Attachment.Annotations) != 3 {
		t.Fatalf("draft replacement: %s", thread.Draft)
	}
	if v.attachmentOpen {
		t.Fatal("editor still open")
	}
	click("消息")
	tt.Key(ui.Cmd, ui.KeyZ)
	if thread.Draft != "Before "+a.Link()+" after" {
		t.Fatal("composer undo did not restore attachment")
	}
}

func TestDraftTitleAndSidebarOrder(t *testing.T) {
	state := model.New()
	ctrl := controller.New(t.TempDir(), state, nil)
	ctrl.NewThread("")
	blank := state.Thread(state.Selected)
	draft := state.AddThread("", "")
	draft.Draft = "草稿"
	sent := state.AddThread("", "")
	sent.Title = "已发送"
	sent.Turns = []model.Turn{{Prompt: "已发送", Status: "completed"}}
	sent.Draft = "后续草稿"
	state.Selected = draft.ID
	v := New(ctrl)
	tt := ui.NewTester(v.Render, 1140, 780)
	tt.Frame()
	b, bok := tt.Find("新对话")
	d, dok := tt.Find("草稿")
	s, sok := tt.Find("已发送")
	if !bok || !dok || !sok || b.Y >= d.Y || d.Y >= s.Y {
		t.Fatalf("expected blank, draft, sent order: %v %v %v", b, d, s)
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("实时更新")
	tt.Frame()
	if v.threadTitle(draft) != "草稿实时更新" || !tt.HasText("草稿实时更新") {
		t.Fatal("draft title did not follow typing")
	}
	tt.Key(ui.Cmd, ui.KeyA)
	tt.Key(0, ui.KeyBackspace)
	tt.Frame()
	if v.threadTitle(draft) != "新对话" || v.threadTitle(blank) != "新对话" {
		t.Fatal("cleared draft did not restore placeholder")
	}
	draft.Draft = "前文" + (model.Attachment{Kind: "file", Path: filepath.Join(t.TempDir(), "example.txt")}).Link() + "后文"
	if v.threadTitle(draft) != "前文example.txt后文" {
		t.Fatal("attachment title exposed its internal link")
	}
	draft.Draft = strings.Repeat("中", 49)
	if v.threadTitle(draft) != strings.Repeat("中", 48)+"…" {
		t.Fatal("long draft title was not truncated safely")
	}
	if v.threadTitle(sent) != "已发送" {
		t.Fatal("follow-up draft changed a sent conversation title")
	}
}

func TestProjectPickerTransfersDraft(t *testing.T) {
	for _, expanded := range []bool{false, true} {
		name := "collapsed"
		if expanded {
			name = "expanded"
		}
		t.Run(name, func(t *testing.T) {
			state := model.New()
			ctrl := controller.New(t.TempDir(), state, nil)
			state.Projects = []model.Project{{ID: "target", Name: "Destination", Path: t.TempDir(), Expanded: expanded}}
			state.NoProjectExpanded = expanded
			thread := state.AddThread("", "")
			v := New(ctrl)
			tt := ui.NewTester(v.Render, 1140, 780)
			tt.Frame()
			if err := tt.Click("消息"); err != nil {
				t.Fatal(err)
			}
			tt.Type("carry this prompt")
			if err := tt.Click("切换项目"); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if err := tt.Click("搜索项目"); err != nil {
				t.Fatal(err)
			}
			tt.Type("dest")
			tt.Frame()
			if err := tt.Click("切换项目 · Destination"); err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 6; i++ {
				tt.Frame()
			}
			if thread.ProjectID != "target" || thread.Draft != "carry this prompt" {
				t.Fatalf("lost draft: %+v", thread)
			}
			if state.Projects[0].Expanded != expanded || state.NoProjectExpanded != expanded {
				t.Fatal("expansion changed")
			}
			if v.projectFlight == nil {
				t.Fatal("missing transfer animation")
			}
		})
	}
}

func TestProjectPickerFollowsSelectedSessionRunningState(t *testing.T) {
	state := model.New()
	ctrl := controller.New(t.TempDir(), state, nil)
	state.Projects = []model.Project{{ID: "target", Name: "Destination", Path: t.TempDir()}}
	thread := state.AddThread("", "")
	idle := state.AddThread("", "")
	state.Selected = thread.ID
	v := New(ctrl)
	tt := ui.NewTester(v.Render, 1140, 780)
	tt.Frame()
	_, ok := tt.Find("切换项目")
	if !ok {
		t.Fatal("idle session has no project selector")
	}
	idleMessage, _ := tt.Find("消息")
	if err := tt.Click("切换项目"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	ctrl.Running[thread.ID] = true
	for i := 0; i < 3; i++ {
		tt.Frame()
	}
	if _, ok := tt.Find("切换项目"); ok || v.projectPicker {
		t.Fatal("running session retained project selector or open picker")
	}
	if _, ok := tt.Find("搜索项目"); ok {
		t.Fatal("running session retained project popover")
	}
	runningMessage, ok := tt.Find("消息")
	if !ok || runningMessage.Y != idleMessage.Y || runningMessage.H != idleMessage.H {
		t.Fatalf("composer moved or resized: idle=%+v running=%+v", idleMessage, runningMessage)
	}
	// With a bottom-anchored composer, the removed row returns its space above it.
	if _, ok := tt.Find("切换项目 · Destination"); ok {
		t.Fatal("hidden picker retained clickable options")
	}
	if err := tt.Click("消息"); err != nil {
		t.Fatal(err)
	}
	tt.Type("queued while running")
	tt.Frame()
	if thread.Draft != "queued while running" {
		t.Fatalf("running composer lost input: %q", thread.Draft)
	}
	state.Selected = idle.ID
	tt.Frame()
	if _, ok := tt.Find("切换项目"); !ok {
		t.Fatal("idle selection inherited another session's running state")
	}
	state.Selected = thread.ID
	tt.Frame()
	if _, ok := tt.Find("切换项目"); ok {
		t.Fatal("running selection restored project selector")
	}
	ctrl.Running[thread.ID] = false
	tt.Frame()
	if _, ok := tt.Find("切换项目"); !ok || v.projectPicker {
		t.Fatal("stopped session did not restore closed selector")
	}
	if err := tt.Click("切换项目"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	if err := tt.Click("切换项目 · Destination"); err != nil {
		t.Fatal(err)
	}
	if state.Thread(state.Selected).ProjectID != "target" {
		t.Fatal("restored selector could not switch projects")
	}
}

func TestProjectPickerHeightAndSearch(t *testing.T) {
	state := model.New()
	ctrl := controller.New(t.TempDir(), state, nil)
	for i := 0; i < 20; i++ {
		name := strings.Repeat("x", i+1)
		state.Projects = append(state.Projects, model.Project{ID: name, Name: name, Path: "/tmp/" + name})
	}
	state.AddThread("", "")
	v := New(ctrl)
	tt := ui.NewTester(v.Render, 840, 560)
	tt.Frame()
	if err := tt.Click("切换项目"); err != nil {
		t.Fatal(err)
	}
	tt.Frame()
	list, ok := tt.Find("搜索项目 · 切换项目")
	if !ok || list.H != 192 {
		t.Fatalf("list height: %+v, found=%v", list, ok)
	}
	if err := tt.Click("搜索项目"); err != nil {
		t.Fatal(err)
	}
	tt.Type(strings.Repeat("x", 20))
	tt.Frame()
	filtered, ok := tt.Find("搜索项目 · 切换项目")
	if !ok || filtered.H != list.H || filtered.Y != list.Y {
		t.Fatalf("search moved panel: before=%+v after=%+v", list, filtered)
	}
	if err := tt.Click("切换项目 · " + strings.Repeat("x", 20)); err != nil {
		t.Fatal(err)
	}
	if state.Thread(state.Selected).ProjectID != strings.Repeat("x", 20) {
		t.Fatal("filtered destination not selected")
	}
}
