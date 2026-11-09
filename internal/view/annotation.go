package view

import (
	"encoding/json"
	"math"
	"slices"

	"github.com/egoist/mygo/ui"
	"github.com/ji233-Sun/mujicagent/internal/markup"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

type annotationEditor struct {
	notes        []model.Annotation
	selected     string
	addingTo     string
	tool, color  string
	width        float32
	sidebar      bool
	zoom         float32
	pan          ui.Point
	canvas       ui.Handle
	undo, redo   [][]model.Annotation
	baseline     string
	layoutKey    string
	confirmClose bool
	scrollTo     string
	dragging     string
	pressed      string
	start        ui.Point
	before       []model.Annotation
	pending      model.Mark
}

func cloneNotes(notes []model.Annotation) []model.Annotation {
	out := slices.Clone(notes)
	for i := range out {
		out[i].Marks = slices.Clone(out[i].Marks)
		for j := range out[i].Marks {
			out[i].Marks[j].Points = slices.Clone(out[i].Marks[j].Points)
		}
	}
	return out
}
func notesKey(notes []model.Annotation) string { b, _ := json.Marshal(notes); return string(b) }
func newAnnotationEditor(notes []model.Annotation) *annotationEditor {
	return &annotationEditor{notes: cloneNotes(notes), tool: "pan", color: "#e05263", width: 4, sidebar: true, baseline: notesKey(notes)}
}
func (s *annotationEditor) current() *model.Annotation {
	for i := range s.notes {
		if s.notes[i].ID == s.selected {
			return &s.notes[i]
		}
	}
	return nil
}
func (s *annotationEditor) record(before []model.Annotation) {
	if notesKey(before) == notesKey(s.notes) {
		return
	}
	s.undo = append(s.undo, cloneNotes(before))
	if len(s.undo) > 80 {
		s.undo = s.undo[1:]
	}
	s.redo = nil
}
func (s *annotationEditor) history(redo bool) {
	s.cancelGesture()
	from, to := &s.undo, &s.redo
	if redo {
		from, to = &s.redo, &s.undo
	}
	if len(*from) == 0 {
		return
	}
	*to = append(*to, cloneNotes(s.notes))
	s.notes = cloneNotes((*from)[len(*from)-1])
	*from = (*from)[:len(*from)-1]
	if s.current() == nil {
		s.selected = ""
	}
	if s.addingTo != "" && s.selected != s.addingTo {
		s.finishAdding()
	}
}
func (s *annotationEditor) add(mark *model.Mark) {
	if mark == nil {
		s.finishAdding()
	}
	before := cloneNotes(s.notes)
	n := s.current()
	if s.addingTo != "" && (n == nil || n.ID != s.addingTo) {
		s.finishAdding()
		return
	}
	if s.addingTo == "" {
		s.notes = append(s.notes, model.Annotation{ID: model.ID()})
		n = &s.notes[len(s.notes)-1]
		s.selected = n.ID
	}
	if mark != nil {
		n.Marks = append(n.Marks, *mark)
	}
	s.sidebar = true
	s.scrollTo = s.selected
	s.record(before)
}
func (s *annotationEditor) cancelGesture() {
	if s.dragging == "move" {
		s.notes = cloneNotes(s.before)
	}
	s.dragging = ""
	s.pressed = ""
	s.pending = model.Mark{}
	s.before = nil
}
func (s *annotationEditor) finishAdding() {
	s.cancelGesture()
	s.addingTo = ""
	s.tool = "pan"
}
func (s *annotationEditor) beginAdding(id string, bitmap *ui.Bitmap, bounds ui.Rect) {
	s.selectNote(id, bitmap, bounds)
	if s.current() != nil {
		s.addingTo = id
		s.tool = "pen"
	}
}
func (s *annotationEditor) chooseTool(tool string) {
	s.cancelGesture()
	if tool == "pan" {
		s.finishAdding()
	} else {
		s.tool = tool
	}
}
func (s *annotationEditor) deleteNote(id string) {
	s.cancelGesture()
	before := cloneNotes(s.notes)
	for i := range s.notes {
		if s.notes[i].ID == id {
			s.notes = slices.Delete(s.notes, i, i+1)
			break
		}
	}
	if s.selected == id || s.addingTo == id {
		s.finishAdding()
		s.selected = ""
	}
	s.record(before)
}
func (s *annotationEditor) addingNumber() int {
	for i, n := range s.notes {
		if n.ID == s.addingTo && n.ID == s.selected {
			return i + 1
		}
	}
	return 0
}
func (s *annotationEditor) displayNotes() ([]model.Annotation, string) {
	if s.dragging != "draw" {
		return s.notes, s.selected
	}
	notes := slices.Clone(s.notes)
	// Preview in the target's position so an unfinished shape has its real number.
	if number := s.addingNumber(); number > 0 {
		n := &notes[number-1]
		n.Marks = append(slices.Clone(n.Marks), s.pending)
		return notes, s.selected
	}
	return append(notes, model.Annotation{Marks: []model.Mark{s.pending}}), ""
}
func (s *annotationEditor) imageRect(w, h float32, bitmap *ui.Bitmap) ui.Rect {
	bw, bh := bitmap.Size()
	scale := s.zoom
	if scale == 0 {
		scale = min(max(w-48, 1)/float32(bw), max(h-48, 1)/float32(bh), 1)
	}
	iw, ih := float32(bw)*scale, float32(bh)*scale
	// Keep at least part of the image accessible while panning.
	px := max(-iw/2-w/2+48, min(s.pan.X, iw/2+w/2-48))
	py := max(-ih/2-h/2+48, min(s.pan.Y, ih/2+h/2-48))
	return ui.Rect{X: (w-iw)/2 + px, Y: (h-ih)/2 + py, W: iw, H: ih}
}
func normalized(p ui.Point, r ui.Rect) model.ImagePoint {
	return model.ImagePoint{X: float64(max(float32(0), min((p.X-r.X)/r.W, 1)) * 100), Y: float64(max(float32(0), min((p.Y-r.Y)/r.H, 1)) * 100)}
}
func inside(p ui.Point, r ui.Rect) bool {
	return p.X >= r.X && p.X <= r.X+r.W && p.Y >= r.Y && p.Y <= r.Y+r.H
}
func segmentDistance(p ui.Point, a, b [2]float32) float32 {
	dx, dy := b[0]-a[0], b[1]-a[1]
	t := float32(0)
	if dx*dx+dy*dy > 0 {
		t = max(float32(0), min(((p.X-a[0])*dx+(p.Y-a[1])*dy)/(dx*dx+dy*dy), 1))
	}
	return float32(math.Hypot(float64(p.X-a[0]-t*dx), float64(p.Y-a[1]-t*dy)))
}
func (s *annotationEditor) hit(p ui.Point, r ui.Rect) string {
	for i := len(s.notes) - 1; i >= 0; i-- {
		n := s.notes[i]
		for _, m := range n.Marks {
			if len(m.Points) == 0 {
				continue
			}
			q := markup.BadgePosition(m.Points[0], r)
			if math.Hypot(float64(p.X-q.X), float64(p.Y-q.Y)) < 16 {
				return n.ID
			}
			for _, path := range markup.Paths(m, r) {
				for j := 1; j < len(path); j++ {
					if segmentDistance(p, path[j-1], path[j]) < 8 {
						return n.ID
					}
				}
			}
		}
	}
	return ""
}
func (s *annotationEditor) selectNote(id string, bitmap *ui.Bitmap, bounds ui.Rect) {
	if id != s.selected || s.addingTo == "" {
		s.finishAdding()
	} else {
		s.cancelGesture()
	}
	s.selected = id
	s.scrollTo = id
	s.sidebar = true
	n := s.current()
	if n == nil || len(n.Marks) == 0 || bitmap == nil {
		return
	}
	s.color, s.width = n.Marks[0].Color, n.Marks[0].Width
	r := s.imageRect(bounds.W, bounds.H, bitmap)
	q := markup.Position(n.Marks[0].Points[0], r)
	if !inside(q, ui.Rect{X: 24, Y: 24, W: bounds.W - 48, H: bounds.H - 48}) {
		s.pan.X += bounds.W/2 - q.X
		s.pan.Y += bounds.H/2 - q.Y
	}
}
func (s *annotationEditor) zoomBy(factor float32, bitmap *ui.Bitmap, bounds ui.Rect, pivot ui.Point) {
	old := s.imageRect(bounds.W, bounds.H, bitmap)
	bw, _ := bitmap.Size()
	next := max(float32(.05), min(old.W/float32(bw)*factor, 8))
	s.zoom = next
	r := s.imageRect(bounds.W, bounds.H, bitmap)
	s.pan.X += (pivot.X - old.X) * (1 - r.W/old.W)
	s.pan.Y += (pivot.Y - old.Y) * (1 - r.H/old.H)
	// Account for the center-based change in the unpanned image rectangle.
	s.pan.X += old.X - r.X
	s.pan.Y += old.Y - r.Y
}
func (s *annotationEditor) input(ev ui.InputEvent, bitmap *ui.Bitmap, bounds ui.Rect) bool {
	p := ui.Point{X: ev.X, Y: ev.Y}
	r := s.imageRect(bounds.W, bounds.H, bitmap)
	switch ev.Kind {
	case ui.InputZoom:
		s.zoomBy(ev.Scale, bitmap, bounds, p)
		return true
	case ui.InputScroll:
		if ev.Mods&ui.Ctrl != 0 {
			s.zoomBy(float32(math.Exp(float64(-ev.DY)*.01)), bitmap, bounds, p)
		} else {
			s.pan.X -= ev.DX
			s.pan.Y -= ev.DY
		}
		return true
	case ui.InputKeyDown:
		if ev.Key == ui.KeyEscape && ev.Mods == 0 && (s.addingTo != "" || s.dragging != "") {
			s.finishAdding()
			return true
		}
		if ev.Mods&ui.Cmd != 0 && ev.Key == ui.KeyZ {
			s.history(ev.Mods&ui.Shift != 0)
			return true
		}
	case ui.InputPointerDown:
		if ev.Button != 0 {
			return false
		}
		s.canvas.Focus()
		s.start = p
		s.before = cloneNotes(s.notes)
		if s.tool == "pan" {
			s.pressed = ""
			if inside(p, r) {
				s.pressed = s.hit(p, r)
			}
			s.dragging = "pan-pending"
			if s.pressed != "" && s.pressed == s.selected {
				s.dragging = "move-pending"
			}
			return true
		}
		if !inside(p, r) {
			return true
		}
		if s.tool == "point" {
			m := model.Mark{Kind: "point", Color: s.color, Width: s.width, Points: []model.ImagePoint{normalized(p, r)}}
			s.add(&m)
			return true
		}
		s.pending = model.Mark{Kind: s.tool, Color: s.color, Width: s.width, Points: []model.ImagePoint{normalized(p, r), normalized(p, r)}}
		s.dragging = "draw"
		return true
	case ui.InputPointerMove:
		return s.drag(p, r)
	case ui.InputPointerUp:
		if ev.Button != 0 {
			return false
		}
		s.drag(p, r)
		if s.dragging == "draw" {
			if math.Hypot(float64(p.X-s.start.X), float64(p.Y-s.start.Y)) >= 3 || len(s.pending.Points) > 3 {
				s.add(&s.pending)
			} else if id := s.hit(p, r); id != "" {
				s.selectNote(id, bitmap, bounds)
			}
			s.pending = model.Mark{}
		} else if s.dragging == "move" {
			s.record(s.before)
		} else if s.dragging == "pan-pending" || s.dragging == "move-pending" {
			// Select on release so revealing the sidebar cannot shift an active drag.
			if s.pressed == "" {
				s.selected = ""
			} else if inside(p, r) && s.hit(p, r) == s.pressed {
				s.selectNote(s.pressed, bitmap, bounds)
			}
		}
		s.dragging = ""
		s.pressed = ""
		s.before = nil
		return true
	}
	return false
}

func (s *annotationEditor) drag(p ui.Point, r ui.Rect) bool {
	if s.dragging == "pan-pending" || s.dragging == "move-pending" {
		if math.Hypot(float64(p.X-s.start.X), float64(p.Y-s.start.Y)) < 4 {
			return true
		}
		if s.dragging == "move-pending" {
			s.dragging = "move"
		} else {
			s.dragging = "pan"
		}
	}
	switch s.dragging {
	case "pan":
		s.pan.X += p.X - s.start.X
		s.pan.Y += p.Y - s.start.Y
		s.start = p
		return true
	case "move":
		s.notes = cloneNotes(s.before)
		n := s.current()
		if n == nil {
			return true
		}
		dx, dy := float64((p.X-s.start.X)/r.W*100), float64((p.Y-s.start.Y)/r.H*100)
		loX, loY, hiX, hiY := 100., 100., 0., 0.
		for _, m := range n.Marks {
			for _, q := range m.Points {
				loX = min(loX, q.X)
				loY = min(loY, q.Y)
				hiX = max(hiX, q.X)
				hiY = max(hiY, q.Y)
			}
		}
		dx = max(-loX, min(dx, 100-hiX))
		dy = max(-loY, min(dy, 100-hiY))
		for j := range n.Marks {
			for k := range n.Marks[j].Points {
				n.Marks[j].Points[k].X += dx
				n.Marks[j].Points[k].Y += dy
			}
		}
		return true
	case "draw":
		q := normalized(p, r)
		if s.pending.Kind == "pen" {
			last := markup.Position(s.pending.Points[len(s.pending.Points)-1], r)
			if len(s.pending.Points) < 2000 && math.Hypot(float64(p.X-last.X), float64(p.Y-last.Y)) >= 2 {
				s.pending.Points = append(s.pending.Points, q)
			}
		} else {
			s.pending.Points[1] = q
		}
		return true
	}
	return false
}

func annotationIcon(path string) *ui.SVG {
	return ui.MustParseSVG([]byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round">` + path + `</svg>`))
}

var (
	panMarkIcon     = annotationIcon(`<path d="M8 13V6a2 2 0 0 1 4 0v6-8a2 2 0 0 1 4 0v8-5a2 2 0 0 1 4 0v9c0 4-3 6-7 6-3 0-5-2-7-5l-3-4a2 2 0 0 1 3-2l2 2"/>`)
	penMarkIcon     = annotationIcon(`<path d="m15 4 5 5M4 20l5-1L20 8a3 3 0 0 0-4-4L5 15z"/>`)
	arrowMarkIcon   = annotationIcon(`<path d="M5 19 19 5M8 5h11v11"/>`)
	undoMarkIcon    = annotationIcon(`<path d="m8 4-5 5 5 5M3 9h10a7 7 0 0 1 0 14" transform="translate(0 -2)"/>`)
	redoMarkIcon    = annotationIcon(`<path d="m16 4 5 5-5 5M21 9H11a7 7 0 0 0 0 14" transform="translate(0 -2)"/>`)
	sidebarMarkIcon = annotationIcon(`<rect x="3" y="4" width="18" height="16" rx="3"/><path d="M15 4v16"/>`)
	imageMarkIcon   = annotationIcon(`<rect x="3" y="3" width="18" height="18" rx="4"/><circle cx="8" cy="8" r="1"/><path d="m3 17 5-5 4 4 4-6 5 7"/>`)
	minusMarkIcon   = annotationIcon(`<path d="M5 12h14"/>`)
)

func markTool(c *ui.Context, label string, icon *ui.SVG, selected bool, fn func()) ui.Element {
	b := ui.ButtonBase(c).Label(label).Tooltip(label).Size(34, 34).Radius(8).Center().TextColor(c.Theme().TextMuted)
	if selected {
		b.Background(c.Theme().Accent).TextColor(c.Theme().AccentText)
	} else if b.Hovered() {
		b.Background(c.Theme().Surface)
	}
	b.Children(func() { ui.Icon(c, icon).Size(17, 17) }).OnClick(fn)
	return b
}
