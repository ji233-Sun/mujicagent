package view

import (
	"fmt"
	"image"
	"image/color"
	"image/draw"
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

func lightboxFixture(t *testing.T, lang string, portrait bool) (*View, *model.Thread, model.Attachment) {
	t.Helper()
	state := model.New()
	state.Settings.Language = lang
	thread := state.AddThread("", t.TempDir())
	thread.Title = "Image preview"
	thread.Draft = "Keep this draft"
	a := model.Attachment{Kind: "image", Path: filepath.Join(thread.Cwd, "preview.png")}
	thread.Turns = []model.Turn{{ID: "image-turn", Prompt: "Review this image", Status: "completed",
		Items: []model.Item{{ID: "image", Kind: "imageView", Text: a.Path, Status: "completed"}}}}
	for i := range 8 {
		thread.Turns = append(thread.Turns, model.Turn{ID: fmt.Sprint(i), Prompt: "Conversation context",
			Status: "completed", Items: []model.Item{{Kind: "agentMessage", Text: "The conversation stays in place while viewing the image."}}})
	}
	ctrl := controller.New(t.TempDir(), state, nil)
	t.Cleanup(ctrl.Close)
	v := New(ctrl)
	*v.openState("activity-" + thread.ID + "-0-0") = true
	*v.openState(thread.ID + "-0-0-0") = true
	w, h := 1200, 800
	if portrait {
		w, h = 600, 1200
	}
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	colors := []color.RGBA{{220, 231, 227, 255}, {7, 137, 109, 255}, {130, 171, 156, 255}, {232, 185, 124, 255}}
	for i, col := range colors {
		r := image.Rect(i%2*w/2, i/2*h/2, (i%2+1)*w/2, (i/2+1)*h/2)
		draw.Draw(img, r, image.NewUniform(col), image.Point{}, draw.Src)
		for y := r.Min.Y + 30; y < r.Max.Y; y += 60 {
			draw.Draw(img, image.Rect(r.Min.X+24, y, r.Max.X-24, y+3), image.NewUniform(color.White), image.Point{}, draw.Src)
		}
	}
	v.attachments = map[string]*attachmentPreview{"image:" + a.Path: {ready: true, bitmap: ui.NewBitmap(img)}}
	return v, thread, a
}

func TestImageLightboxInteractions(t *testing.T) {
	for _, dismiss := range []string{"button", "backdrop", "escape"} {
		t.Run(dismiss, func(t *testing.T) {
			v, thread, a := lightboxFixture(t, "en", false)
			tt := ui.NewTester(v.Render, 1100, 800)
			tt.Frame()
			v.scroll[thread.ID].Y = 40
			tt.Frame()
			scrollY := v.scroll[thread.ID].Y
			if scrollY != 40 {
				t.Fatalf("fixture did not establish a nonzero scroll offset: %g", scrollY)
			}
			thumb, ok := tt.Find(v.texts().ImagePreview() + ": " + a.Path)
			if !ok || thumb.Y < 0 || thumb.Y+thumb.H > 800 {
				t.Fatalf("thumbnail unavailable: %+v", thumb)
			}
			tt.ClickAt(thumb.X+thumb.W/2, thumb.Y+thumb.H/2)
			tt.Frame()
			s := &v.lightbox
			if !s.open || s.path != a.Path || s.bitmap != v.attachments["image:"+a.Path].bitmap {
				t.Fatalf("thumbnail did not open its image: rect=%+v, open=%t, path=%q, bitmap=%p", thumb, s.open, s.path, s.bitmap)
			}
			if v.attachmentOpen || v.annotation != nil {
				t.Fatal("lightbox opened the annotation editor")
			}
			canvas, ok := tt.Find(v.texts().PanImage())
			if !ok {
				t.Fatal("lightbox canvas missing")
			}
			fitted := s.imageRect(canvas.W, canvas.H)
			if fitted.X < 0 || fitted.Y < 0 || fitted.X+fitted.W > canvas.W || fitted.Y+fitted.H > canvas.H {
				t.Fatalf("initial image does not fit: %+v in %+v", fitted, canvas)
			}
			for _, label := range []string{v.texts().ZoomIn(), v.texts().ZoomOut(), "1:1"} {
				before := s.imageRect(canvas.W, canvas.H).W
				if err := tt.Click(label); err != nil {
					t.Fatal(err)
				}
				tt.Frame()
				after := s.imageRect(canvas.W, canvas.H).W
				if label == v.texts().ZoomIn() && after <= before || label == v.texts().ZoomOut() && after >= before || label == "1:1" && after != 1200 {
					t.Fatalf("%s did not change image scale: %g -> %g", label, before, after)
				}
			}
			x, y := canvas.X+canvas.W/2, canvas.Y+canvas.H/2
			tt.Pinch(x, y, 2)
			tt.Frame()
			if s.zoom != 2 {
				t.Fatalf("pinch did not zoom: %g", s.zoom)
			}
			tt.Press(x, y)
			tt.Move(x+100, y+80)
			tt.Release(x+100, y+80)
			tt.Frame()
			if s.pan != (ui.Point{X: 100, Y: 80}) || s.dragging {
				t.Fatalf("drag did not pan and release: %+v, dragging=%t", s.pan, s.dragging)
			}
			tt.Scroll(x, y, 20, 30)
			tt.Frame()
			if s.pan != (ui.Point{X: 80, Y: 50}) {
				t.Fatalf("scroll did not pan: %+v", s.pan)
			}
			if v.scroll[thread.ID].Y != scrollY {
				t.Fatal("lightbox input scrolled the conversation underneath")
			}
			if err := tt.Click(v.texts().FitImage()); err != nil {
				t.Fatal(err)
			}
			tt.Frame()
			if s.zoom != 0 || s.pan != (ui.Point{}) || s.imageRect(canvas.W, canvas.H) != fitted {
				t.Fatal("Fit did not reset zoom and pan")
			}
			switch dismiss {
			case "button":
				if err := tt.Click(v.texts().CloseAttachment()); err != nil {
					t.Fatal(err)
				}
			case "backdrop":
				tt.ClickAt(5, 400)
			case "escape":
				// Escape must also work with keyboard focus on the canvas.
				tt.ClickAt(x, y)
				tt.Key(0, ui.KeyEscape)
			}
			tt.Frame()
			tt.Frame()
			if s.open || s.bitmap != nil {
				t.Fatal("dismissal left the lightbox open or retained its bitmap")
			}
			if v.C.State.Selected != thread.ID || thread.Draft != "Keep this draft" || v.scroll[thread.ID].Y != scrollY ||
				!*v.openState("activity-" + thread.ID + "-0-0") || !*v.openState(thread.ID + "-0-0-0") {
				t.Fatal("dismissal changed the conversation state")
			}
			after, ok := tt.Find(v.texts().ImagePreview() + ": " + a.Path)
			if !ok || after != thumb {
				t.Fatalf("thumbnail moved after dismissal: %+v -> %+v", thumb, after)
			}
			tt.ClickAt(thumb.X+thumb.W/2, thumb.Y+thumb.H/2)
			tt.Frame()
			if !s.open || s.zoom != 0 || s.pan != (ui.Point{}) {
				t.Fatal("reopening did not start with a fitted image")
			}
		})
	}
}

func TestImageLightboxGeometry(t *testing.T) {
	s := imageLightbox{bitmap: ui.NewBitmap(image.NewRGBA(image.Rect(0, 0, 1200, 800)))}
	bounds := ui.Rect{W: 600, H: 400}
	if got, want := s.imageRect(600, 400), (ui.Rect{X: 24, Y: 16, W: 552, H: 368}); math.Abs(float64(got.X-want.X))+math.Abs(float64(got.Y-want.Y))+math.Abs(float64(got.W-want.W))+math.Abs(float64(got.H-want.H)) > .001 {
		t.Fatalf("fit: got %+v, want %+v", got, want)
	}
	s.zoom = 1
	pivot := ui.Point{X: 350, Y: 250}
	before := s.imageRect(600, 400)
	s.zoomBy(2, bounds, pivot)
	after := s.imageRect(600, 400)
	if (pivot.X-before.X)/before.W != (pivot.X-after.X)/after.W || (pivot.Y-before.Y)/before.H != (pivot.Y-after.Y)/after.H {
		t.Fatal("zoom did not preserve the image point under the pointer")
	}
	for _, direction := range []float32{-1, 1} {
		s.pan = ui.Point{X: direction * 1e6, Y: direction * 1e6}
		s.clampPan(bounds)
		r := s.imageRect(600, 400)
		if direction == 1 && (r.X != 0 || r.Y != 0) || direction == -1 && (r.X+r.W != 600 || r.Y+r.H != 400) {
			t.Fatalf("cannot reach image edges: %+v", r)
		}
		old := s.pan
		s.input(ui.InputEvent{Kind: ui.InputScroll, DX: direction * 10, DY: direction * 10}, bounds)
		if s.pan.X != old.X-direction*10 || s.pan.Y != old.Y-direction*10 {
			t.Fatal("panning back from an edge has a dead zone")
		}
	}
	s.input(ui.InputEvent{Kind: ui.InputScroll, Mods: ui.Ctrl, DY: -20, X: 300, Y: 200}, bounds)
	if s.zoom <= 2 {
		t.Fatal("Ctrl+wheel did not zoom")
	}
	for range 50 {
		s.zoomBy(.1, bounds, pivot)
	}
	r := s.imageRect(600, 400)
	if s.zoom != .05 || r.X < 0 || r.Y < 0 || s.pan != (ui.Point{}) {
		t.Fatalf("zoom out did not center at its limit: zoom=%g, rect=%+v, pan=%+v", s.zoom, r, s.pan)
	}
	for range 50 {
		s.zoomBy(10, bounds, pivot)
	}
	if s.zoom != 8 {
		t.Fatalf("zoom in limit: %g", s.zoom)
	}
	// Resizing must keep both aspect ratio and an accessible image.
	s.pan = ui.Point{X: 1e6, Y: -1e6}
	for _, size := range []ui.Point{{X: 300, Y: 200}, {X: 1400, Y: 900}} {
		r := s.imageRect(size.X, size.Y)
		if math.Abs(float64(r.W/r.H-1.5)) > .0001 || r.X > 0 || r.Y+r.H < size.Y {
			t.Fatalf("invalid resized geometry: %+v", r)
		}
	}
}

func TestImageLightboxScreenshots(t *testing.T) {
	for _, lang := range []string{"zh-Hans", "en"} {
		for _, scene := range []string{"fit", "zoomed", "portrait", "narrow", "closed"} {
			t.Run(lang+"/"+scene, func(t *testing.T) {
				v, thread, a := lightboxFixture(t, lang, scene == "portrait")
				width, height := 1100, 800
				if scene == "narrow" {
					width, height = 660, 700
				}
				tt := ui.NewTester(v.Render, width, height)
				tt.Frame()
				v.scroll[thread.ID].Y = 40
				// Screenshot scenarios are configured directly, without simulated input.
				if scene != "closed" {
					v.openImageLightbox(a, v.attachments["image:"+a.Path].bitmap)
					if scene == "zoomed" {
						v.lightbox.zoom = 2
						v.lightbox.pan = ui.Point{X: -240, Y: 180}
					}
				}
				tt.Frame()
				tt.Advance(time.Second)
				tt.Frame()
				if scene != "closed" {
					for _, label := range []string{v.texts().PanImage(), v.texts().CloseAttachment(), v.texts().FitImage(), "1:1", v.texts().ZoomIn(), v.texts().ZoomOut()} {
						r, ok := tt.Find(label)
						if !ok || r.W <= 0 || r.H <= 0 || r.X < 0 || r.Y < 0 || r.X+r.W > float32(width) || r.Y+r.H > float32(height) {
							t.Fatalf("%s missing or clipped: %+v", label, r)
						}
					}
					canvas, _ := tt.Find(v.texts().PanImage())
					r := v.lightbox.imageRect(canvas.W, canvas.H)
					bw, bh := v.lightbox.bitmap.Size()
					if math.Abs(float64(r.W/r.H-float32(bw)/float32(bh))) > .0001 {
						t.Fatal("image aspect ratio changed")
					}
				}
				if out := os.Getenv("MUJICAGENT_LIGHTBOX_SCREENSHOTS"); out != "" {
					if err := os.MkdirAll(out, 0700); err != nil {
						t.Fatal(err)
					}
					f, err := os.Create(filepath.Join(out, lang+"-"+scene+".png"))
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
