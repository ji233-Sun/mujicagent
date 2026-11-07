package view

import (
	"image"
	"image/color"
	"image/draw"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/controller"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func TestImageView(t *testing.T) {
	dir := t.TempDir()
	name := "界面预览 sample.png"
	path := filepath.Join(dir, name)
	img := image.NewRGBA(image.Rect(0, 0, 720, 400))
	draw.Draw(img, img.Bounds(), image.NewUniform(color.RGBA{245, 248, 247, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(0, 0, 180, 400), image.NewUniform(color.RGBA{220, 231, 227, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(210, 40, 680, 100), image.NewUniform(color.RGBA{7, 137, 109, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(210, 130, 430, 360), image.NewUniform(color.RGBA{130, 171, 156, 255}), image.Point{}, draw.Src)
	draw.Draw(img, image.Rect(460, 130, 680, 360), image.NewUniform(color.RGBA{232, 185, 124, 255}), image.Point{}, draw.Src)
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	err = png.Encode(f, img)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	broken := filepath.Join(dir, "broken.png")
	if err := os.WriteFile(broken, []byte("not an image"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, scene := range []string{"collapsed", "ready", "relative", "loading", "missing", "broken", "empty", "narrow"} {
			t.Run(lang+"/"+scene, func(t *testing.T) {
				state := model.New()
				state.Settings.Language = lang
				thread := state.AddThread("", dir)
				thread.Title = "Codex ImageView"
				item := model.Item{ID: "image", Kind: "imageView", Text: path, Status: "completed"}
				switch scene {
				case "relative":
					item.Text = name
				case "missing":
					item.Text = filepath.Join(dir, "removed.png")
				case "broken":
					item.Text = broken
				case "empty":
					item.Text = ""
				}
				thread.Turns = []model.Turn{{ID: "turn", Prompt: "查看图片，确认界面布局。", Status: "completed", Items: []model.Item{item}}}
				ctrl := controller.New(t.TempDir(), state, nil)
				defer ctrl.Close()
				v := New(ctrl)
				*v.openState("activity-" + thread.ID + "-0-0") = true
				*v.openState(thread.ID + "-0-0-0") = scene != "collapsed"
				a := v.viewedImage(&item)
				if scene == "relative" && a.Path != path {
					t.Fatalf("relative path not resolved against conversation cwd: %q", a.Path)
				}
				p := readPreview(a)
				if scene == "loading" {
					p = &attachmentPreview{}
				}
				v.attachments = map[string]*attachmentPreview{"image:" + a.Path: p}
				width := 1100
				if scene == "narrow" {
					width = 660
				}
				tt := ui.NewTester(v.Render, width, 800)
				tt.Frame()
				tt.Advance(time.Second)
				tt.Frame()
				label := v.texts().ImagePreview()
				if item.Text != "" {
					label += " · " + filepath.Base(item.Text)
				}
				if !tt.HasText(label) || tt.HasText("imageView") {
					t.Fatal("image tool row missing its localized title or filename")
				}
				if scene == "collapsed" {
					if tt.HasText(path) {
						t.Fatal("collapsed item exposes expanded contents")
					}
				} else if scene != "empty" && !tt.HasText(a.Path) {
					t.Fatal("expanded image path missing")
				}
				switch scene {
				case "missing", "broken", "empty":
					if !tt.HasText(v.texts().NoImagePreview()) {
						t.Fatal("unavailable image has no fallback")
					}
				case "loading":
					if !tt.HasText(v.texts().AttachmentLoading()) {
						t.Fatal("loading image has no status")
					}
				case "ready", "relative", "narrow":
					r, ok := tt.Find(v.texts().ImagePreview())
					if !ok || r.W <= 0 || r.H <= 0 || r.X+r.W > float32(width) {
						t.Fatalf("thumbnail missing or outside viewport: %+v", r)
					}
				}
				if out := os.Getenv("MUJICAGENT_IMAGE_VIEW_SCREENSHOTS"); out != "" {
					if err := os.MkdirAll(out, 0700); err != nil {
						t.Fatal(err)
					}
					file, err := os.Create(filepath.Join(out, strings.Join([]string{lang, scene}, "-")+".png"))
					if err != nil {
						t.Fatal(err)
					}
					defer file.Close()
					if err := png.Encode(file, tt.Image()); err != nil {
						t.Fatal(err)
					}
				}
			})
		}
	}
}

func TestImageViewThumbnailDoesNotOpenAnnotations(t *testing.T) {
	state := model.New()
	thread := state.AddThread("", t.TempDir())
	ctrl := controller.New(t.TempDir(), state, nil)
	defer ctrl.Close()
	v := New(ctrl)
	item := model.Item{Kind: "imageView", Text: filepath.Join(thread.Cwd, "sample.png")}
	v.attachments = map[string]*attachmentPreview{
		"image:" + item.Text: {ready: true, bitmap: ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 20, 20)))},
	}
	tt := ui.NewTester(func(c *ui.Context) {
		v.imageView(c, &item)
		v.attachmentDialog(c)
	}, 400, 300)
	tt.Frame()
	r, ok := tt.Find(v.texts().ImagePreview())
	if !ok {
		t.Fatal("thumbnail missing")
	}
	// Interaction regression is separate from the direct-state screenshot scenes.
	tt.ClickAt(r.X+r.W/2, r.Y+r.H/2)
	tt.Frame()
	if v.attachmentOpen || v.annotation != nil {
		t.Fatal("clicking a tool thumbnail must not open image annotations")
	}
}
