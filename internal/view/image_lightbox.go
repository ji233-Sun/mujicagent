package view

import (
	"fmt"
	"math"
	"path/filepath"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type imageLightbox struct {
	open     bool
	path     string
	bitmap   *ui.Bitmap
	zoom     float32 // Zero fits the image to the available area.
	pan      ui.Point
	canvas   ui.Handle
	dragging bool
	last     ui.Point
}

func (v *View) openImageLightbox(a model.Attachment, bitmap *ui.Bitmap) {
	v.lightbox = imageLightbox{open: true, path: a.Path, bitmap: bitmap}
}

func (s *imageLightbox) fitScale(w, h float32) float32 {
	bw, bh := s.bitmap.Size()
	return min(max(w-32, 1)/float32(bw), max(h-32, 1)/float32(bh), 1)
}

func (s *imageLightbox) imageRect(w, h float32) ui.Rect {
	bw, bh := s.bitmap.Size()
	scale := s.zoom
	if scale == 0 {
		scale = s.fitScale(w, h)
	}
	iw, ih := float32(bw)*scale, float32(bh)*scale
	px, py := max((iw-w)/2, 0), max((ih-h)/2, 0)
	return ui.Rect{
		X: (w-iw)/2 + max(-px, min(s.pan.X, px)),
		Y: (h-ih)/2 + max(-py, min(s.pan.Y, py)),
		W: iw, H: ih,
	}
}

func (s *imageLightbox) clampPan(bounds ui.Rect) {
	r := s.imageRect(bounds.W, bounds.H)
	s.pan = ui.Point{X: r.X - (bounds.W-r.W)/2, Y: r.Y - (bounds.H-r.H)/2}
}

func (s *imageLightbox) zoomBy(factor float32, bounds ui.Rect, pivot ui.Point) {
	if bounds.W <= 0 || bounds.H <= 0 || factor <= 0 {
		return
	}
	old := s.imageRect(bounds.W, bounds.H)
	bw, bh := s.bitmap.Size()
	s.zoom = max(min(s.fitScale(bounds.W, bounds.H), .05), min(old.W/float32(bw)*factor, 8))
	iw, ih := float32(bw)*s.zoom, float32(bh)*s.zoom
	s.pan = ui.Point{
		X: pivot.X - (pivot.X-old.X)*iw/old.W - (bounds.W-iw)/2,
		Y: pivot.Y - (pivot.Y-old.Y)*ih/old.H - (bounds.H-ih)/2,
	}
	s.clampPan(bounds)
}

func (s *imageLightbox) input(ev ui.InputEvent, bounds ui.Rect) bool {
	p := ui.Point{X: ev.X, Y: ev.Y}
	switch ev.Kind {
	case ui.InputZoom:
		s.zoomBy(ev.Scale, bounds, p)
		return true
	case ui.InputScroll:
		if ev.Mods&ui.Ctrl != 0 {
			s.zoomBy(float32(math.Exp(float64(-ev.DY)*.01)), bounds, p)
		} else {
			s.clampPan(bounds)
			s.pan.X -= ev.DX
			s.pan.Y -= ev.DY
			s.clampPan(bounds)
		}
		return true
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		s.dragging, s.last = true, p
		return true
	case ui.InputPointerMove:
		if s.dragging {
			s.clampPan(bounds)
			s.pan.X += p.X - s.last.X
			s.pan.Y += p.Y - s.last.Y
			s.clampPan(bounds)
			s.last = p
			return true
		}
	case ui.InputPointerUp:
		if ev.Button == 0 {
			s.dragging = false
			return true
		}
	}
	return false
}

func (v *View) imageLightboxDialog(c *ui.Context) {
	s := &v.lightbox
	if !s.open {
		s.bitmap = nil
		return
	}
	tx := v.texts()
	cw, ch := c.Size()
	ui.DialogBase(c.Key("image-lightbox"), &s.open, func(backdrop, panel ui.Element) {
		backdrop.Background(ui.RGBA(12, 22, 18, .65))
		panel.Width(min(cw-40, 1240)).Height(min(ch-40, 860)).Radius(16).Clip().
			Background(c.Theme().Background).Border(1, c.Theme().Border).
			Shadow(0, 18, 60, 0, ui.RGBA(0, 0, 0, .25))
		ui.Row(c).Height(56).Shrink(0).Padding(12, 16).Gap(12).AlignItems(ui.Center).Children(func() {
			ui.Icon(c, imageMarkIcon).Size(20, 20).Shrink(0).TextColor(c.Theme().Accent)
			ui.Text(c, filepath.Base(s.path)).FontSize(14).Bold().Grow(1).MinWidth(0).SingleLine().Tooltip(s.path)
			markTool(c, tx.CloseAttachment(), closeIcon, false, func() { s.open = false })
		})
		ui.Divider(c)
		ui.Row(c).Height(44).Shrink(0).Padding(6, 16).Gap(8).AlignItems(ui.Center).Children(func() {
			ui.Text(c, tx.ImagePreview()).FontSize(11).TextColor(c.Theme().TextMuted).Grow(1).MinWidth(0).SingleLine()
			ui.Button(c, tx.FitImage()).Height(28).FontSize(11).OnClick(func() { s.zoom = 0; s.pan = ui.Point{} })
			ui.Button(c, "1:1").Height(28).FontSize(11).OnClick(func() { s.zoom = 1; s.pan = ui.Point{} })
			changeZoom := func(factor float32) {
				b := s.canvas.Bounds(c)
				s.zoomBy(factor, b, ui.Point{X: b.W / 2, Y: b.H / 2})
			}
			markTool(c, tx.ZoomOut(), minusMarkIcon, false, func() { changeZoom(.8) })
			b := s.canvas.Bounds(c)
			label := tx.FitImage()
			if b.W > 0 && b.H > 0 {
				bw, _ := s.bitmap.Size()
				label = fmt.Sprintf("%.0f%%", s.imageRect(b.W, b.H).W/float32(bw)*100)
			}
			ui.Text(c, label).FontSize(11).Width(44).TextAlign(ui.Center).TextColor(c.Theme().TextMuted)
			markTool(c, tx.ZoomIn(), plus, false, func() { changeZoom(1.25) })
		})
		canvas := ui.Box(c.Key("lightbox-canvas")).Bind(&s.canvas).Grow(1).MinHeight(0).FillWidth().
			Clip().Background(ui.Hex("#202b27")).Label(tx.PanImage()).Focusable().Cursor(ui.CursorGrab)
		if s.dragging {
			canvas.Cursor(ui.CursorGrabbing)
		}
		canvas.HandleInput(func(ev ui.InputEvent) bool { return s.input(ev, s.canvas.Bounds(c)) })
		canvas.Draw(func(p *ui.Painter, bounds ui.Rect) {
			r := s.imageRect(bounds.W, bounds.H)
			r.X += bounds.X
			r.Y += bounds.Y
			p.Clip(bounds, 0, func() {
				p.Fill(r, ui.Hex("#ffffff"), 0)
				p.Image(s.bitmap, r, ui.Contain)
			})
		})
	})
}
