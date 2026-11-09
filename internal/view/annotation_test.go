package view

import (
	"fmt"
	"image/png"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func annotationWorkspace(t *testing.T, notes []model.Annotation, width, height int) (*View, *ui.Tester) {
	t.Helper()
	state := model.New()
	ctrl := controller.New(t.TempDir(), state, nil)
	ctrl.NewThread("")
	a := model.Attachment{Kind: "image", Path: filepath.Join(t.TempDir(), "workspace.png"), Annotations: cloneNotes(notes)}
	state.Thread(state.Selected).Draft = a.Link()
	img := ui.Render(func(c *ui.Context) {
		ui.Column(c).Fill().Padding(36).Gap(24).Background(ui.Hex("#f5f8f7")).Children(func() {
			ui.Text(c, "WORKSPACE / DESIGN REVIEW").FontSize(13).TextColor(ui.Hex("#74877f"))
			ui.Text(c, "Project overview").FontSize(32).Bold().TextColor(ui.Hex("#28413a"))
			ui.Row(c).Gap(24).Grow(1).FillWidth().Children(func() {
				for _, title := range []string{"In progress", "Ready for review"} {
					ui.Column(c).Grow(1).Padding(24).Gap(16).Radius(12).Background(ui.Hex("#e1ebe6")).Children(func() {
						ui.Text(c, title).FontSize(20).Bold().TextColor(ui.Hex("#28413a"))
						for _, text := range []string{"Navigation and layout", "Comments and feedback", "Next iteration"} {
							ui.Text(c, text).FontSize(16).Padding(16).FillWidth().Radius(8).Background(ui.Hex("#ffffff"))
						}
					})
				}
			})
		})
	}, 1000, 600, 1)
	v := New(ctrl)
	v.attachments = map[string]*attachmentPreview{"image:" + a.Path: {ready: true, bitmap: ui.NewBitmap(img), format: "png", size: 4096}}
	v.openAttachment(a, state.Selected)
	tt := ui.NewTester(v.Render, width, height)
	tt.SetLocale("zh-Hans")
	tt.SetPreferences(ui.Preferences{ReduceMotion: true})
	tt.Frame()
	return v, tt
}

func annotationFixture() []model.Annotation {
	return []model.Annotation{
		{ID: "first", Comment: "Keep the title", Marks: []model.Mark{{Kind: "point", Color: "#4775d1", Width: 4, Points: []model.ImagePoint{{X: 12, Y: 21}}}}},
		{ID: "target", Comment: "Align these cards", Marks: []model.Mark{{Kind: "rectangle", Color: "#e05263", Width: 4, Points: []model.ImagePoint{{X: 8, Y: 35}, {X: 48, Y: 78}}}}},
		{ID: "empty", Comment: "Leave more space between sections"},
	}
}

func TestAnnotationAddingExit(t *testing.T) {
	for _, action := range []string{"done", "canvas-escape", "comment-escape", "tool-escape", "switch-comment", "switch-target", "delete", "hide-sidebar", "close", "apply", "new-comment", "hand"} {
		t.Run(action, func(t *testing.T) {
			v, tt := annotationWorkspace(t, annotationFixture(), 1140, 780)
			s := v.annotation
			click := func(label string) {
				t.Helper()
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
			}
			if s.tool != "pan" {
				t.Fatal("workspace did not default to the hand tool")
			}
			click("为评论 2 添加图形")
			if s.addingNumber() != 2 || s.selected != "target" || !tt.HasText("正在为评论 2 添加图形") {
				t.Fatal("card did not establish the explicit target")
			}
			switch action {
			case "done":
				click("完成")
			case "canvas-escape":
				b, _ := tt.Find("标记图片")
				r := s.imageRect(b.W, b.H, v.preview(v.attachment).bitmap)
				tt.Press(b.X+r.X+r.W*.65, b.Y+r.Y+r.H*.4)
				tt.Move(b.X+r.X+r.W*.8, b.Y+r.Y+r.H*.6)
				tt.Frame()
				if s.dragging != "draw" {
					t.Fatal("no unfinished stroke to cancel")
				}
				tt.Key(0, ui.KeyEscape)
				tt.Release(b.X+r.X+r.W*.8, b.Y+r.Y+r.H*.6)
			case "comment-escape":
				click("标记说明")
				tt.Key(0, ui.KeyEscape)
			case "tool-escape":
				click("矩形")
				tt.Key(0, ui.KeyEscape)
			case "switch-comment":
				click("选择标注 1")
			case "switch-target":
				click("为评论 1 添加图形")
				if s.addingTo != "first" || s.addingNumber() != 1 {
					t.Fatal("explicitly choosing another target failed")
				}
				click("完成")
			case "delete":
				click("删除此条标注")
				s.history(false)
				if len(s.notes) != 3 || s.addingTo != "" {
					t.Fatal("undoing deletion restored an implicit target")
				}
			case "hide-sidebar":
				click("显示或收起评论栏")
			case "close":
				click("关闭附件")
			case "apply":
				click("应用到当前草稿")
			case "new-comment":
				click("添加整体说明")
			case "hand":
				click("移动画布")
			}
			tt.Frame()
			if s.addingTo != "" || s.tool != "pan" || s.dragging != "" || len(s.pending.Points) != 0 {
				t.Fatalf("adding state remained after %s: %+v", action, s)
			}
			if len(s.notes[1].Marks) != 1 || s.notes[1].ID != "target" {
				t.Fatal("exit changed completed shapes")
			}
			if action != "close" && action != "apply" && !v.attachmentOpen {
				t.Fatal("Escape or another local action dismissed the workspace")
			}
		})
	}
}

func TestAnnotationAddingEmptyAndHistory(t *testing.T) {
	v, tt := annotationWorkspace(t, annotationFixture(), 1140, 780)
	s := v.annotation
	if err := tt.Click("为评论 3 添加图形"); err != nil {
		t.Fatal(err)
	}
	if s.addingNumber() != 3 || !tt.HasText("0 个图形") {
		t.Fatal("a comment without shapes could not become the target")
	}
	mark := model.Mark{Kind: "arrow", Color: s.color, Width: s.width, Points: []model.ImagePoint{{X: 55, Y: 45}, {X: 70, Y: 60}}}
	s.pending, s.dragging = mark, "draw"
	preview, selected := s.displayNotes()
	if len(preview) != 3 || selected != "empty" || len(preview[2].Marks) != 1 || len(s.notes[2].Marks) != 0 {
		t.Fatal("pending shape did not preview under comment 3 without committing")
	}
	s.cancelGesture()
	s.add(&mark)
	tt.Frame()
	if len(s.notes) != 3 || len(s.notes[2].Marks) != 1 || s.notes[2].Comment != "Leave more space between sections" || !tt.HasText("1 个图形") {
		t.Fatal("first shape lost its existing comment")
	}
	s.history(false)
	tt.Frame()
	if len(s.notes[2].Marks) != 0 || !tt.HasText("0 个图形") || s.addingNumber() != 3 {
		t.Fatal("undo did not restore the empty comment and active target")
	}
	s.history(true)
	if len(s.notes[2].Marks) != 1 || s.addingNumber() != 3 {
		t.Fatal("redo did not restore the first shape")
	}
	s.finishAdding()
	s.add(nil)
	id := s.selected
	s.beginAdding(id, nil, ui.Rect{})
	s.history(false)
	if len(s.notes) != 3 || s.addingTo != "" || s.tool != "pan" {
		t.Fatal("undoing the target's creation kept adding active")
	}
	s.history(true)
	if len(s.notes) != 4 || s.addingTo != "" {
		t.Fatal("redo silently resumed adding")
	}
	s.beginAdding(id, nil, ui.Rect{})
	s.selected = "target"
	s.add(&mark)
	if s.addingTo != "" || len(s.notes[1].Marks) != 1 || len(s.notes[3].Marks) != 0 {
		t.Fatal("a stale target silently attached the shape to another comment")
	}
}

func TestAnnotationHandSelection(t *testing.T) {
	notes := annotationFixture()
	notes[1].Marks = append(notes[1].Marks, model.Mark{Kind: "arrow", Color: "#e05263", Width: 4, Points: []model.ImagePoint{{X: 60, Y: 40}, {X: 80, Y: 65}}})
	v, tt := annotationWorkspace(t, notes, 1140, 780)
	s := v.annotation
	bitmap := v.preview(v.attachment).bitmap
	bounds, _ := tt.Find("标记图片")
	r := s.imageRect(bounds.W, bounds.H, bitmap)
	input := func(kind ui.InputKind, x, y float32) {
		t.Helper()
		s.input(ui.InputEvent{Kind: kind, X: r.X + r.W*x, Y: r.Y + r.H*y}, bitmap, bounds)
	}
	input(ui.InputPointerDown, .8, .65)
	if s.selected != "target" || s.dragging != "move" {
		t.Fatal("hand did not select the arrow's shared comment")
	}
	input(ui.InputPointerMove, .85, .70)
	input(ui.InputPointerUp, .85, .70)
	for i, mark := range s.notes[1].Marks {
		for j, point := range mark.Points {
			original := notes[1].Marks[i].Points[j]
			if math.Abs(point.X-original.X-5) > .01 || math.Abs(point.Y-original.Y-5) > .01 {
				t.Fatal("hand did not move all shapes of the shared comment")
			}
		}
	}
	s.history(false)
	if notesKey(s.notes) != notesKey(notes) {
		t.Fatal("undo lost the grouped geometry")
	}
	s.beginAdding("target", bitmap, bounds)
	input(ui.InputPointerDown, .12, .21)
	input(ui.InputPointerUp, .12, .21)
	if s.selected != "first" || s.addingTo != "" || s.tool != "pan" {
		t.Fatal("clicking another comment's number did not exit adding and locate it")
	}
	pan := s.pan
	input(ui.InputPointerDown, .9, .9)
	input(ui.InputPointerMove, .95, .95)
	input(ui.InputPointerUp, .95, .95)
	if s.pan == pan || notesKey(s.notes) != notesKey(notes) {
		t.Fatal("dragging empty canvas did not pan without changing shapes")
	}
}

func TestAnnotationAddingScreenshots(t *testing.T) {
	for _, language := range []string{"zh-Hans", "en"} {
		for _, size := range [][2]int{{1140, 780}, {840, 560}, {720, 780}} {
			for _, scene := range []string{"adding-existing", "added-multiple", "adding-empty", "finished"} {
				t.Run(fmt.Sprintf("%s-%dx%d-%s", language, size[0], size[1], scene), func(t *testing.T) {
					notes := annotationFixture()
					if scene == "added-multiple" || scene == "finished" {
						notes[1].Marks = append(notes[1].Marks,
							model.Mark{Kind: "arrow", Color: "#e05263", Width: 4, Points: []model.ImagePoint{{X: 74, Y: 65}, {X: 48, Y: 48}}},
							model.Mark{Kind: "pen", Color: "#e05263", Width: 4, Points: []model.ImagePoint{{X: 15, Y: 82}, {X: 23, Y: 84}, {X: 32, Y: 82}, {X: 43, Y: 83}}})
					}
					v, tt := annotationWorkspace(t, notes, size[0], size[1])
					v.C.State.Settings.Language = language
					tt.SetLocale(language)
					s := v.annotation
					target, number := "target", "2"
					if scene == "adding-empty" {
						target, number = "empty", "3"
					}
					bounds, _ := tt.Find(v.texts().MarkImage())
					s.beginAdding(target, v.preview(v.attachment).bitmap, bounds)
					if scene == "finished" {
						s.finishAdding()
					} else {
						s.chooseTool("arrow")
					}
					tt.Frame()
					tt.Advance(time.Second)
					tx := v.texts()
					if scene != "finished" {
						if !tt.HasText(tx.AddingAnnotationShapes(number)) {
							t.Fatal("target comment number is missing")
						}
						b, ok := tt.Find(tx.FinishAddingShapes())
						if !ok || b.X < 0 || b.Y < 0 || b.X+b.W > float32(size[0]) || b.Y+b.H > float32(size[1])-62 {
							t.Fatalf("Done is outside the comments viewport: %+v", b)
						}
					}
					if !tt.HasText(tx.AnnotationShapeCount(fmt.Sprint(len(s.current().Marks)))) {
						t.Fatal("shape count is missing")
					}
					canvas, ok := tt.Find(tx.MarkImage())
					if !ok || canvas.W < 200 || canvas.H < 50 {
						t.Fatalf("canvas is too small: %+v", canvas)
					}
					for _, removed := range []string{"新图形关联到此评论", "Link new marks to this comment", "画出重点，再在右侧补充说明", "选择标注"} {
						if _, ok := tt.Find(removed); ok {
							t.Fatalf("removed control or tutorial is still visible: %s", removed)
						}
					}
					if output := os.Getenv("MUJICAGENT_ANNOTATION_SCREENSHOTS"); output != "" {
						if err := os.MkdirAll(output, 0700); err != nil {
							t.Fatal(err)
						}
						f, err := os.Create(filepath.Join(output, fmt.Sprintf("%s-%dx%d-%s.png", language, size[0], size[1], scene)))
						if err != nil {
							t.Fatal(err)
						}
						defer f.Close()
						if err := png.Encode(f, tt.Image()); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		}
	}
}
