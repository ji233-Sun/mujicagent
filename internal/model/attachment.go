package model

import (
	"encoding/json"
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
)

// Attachment links live in the message itself, so copying or persisting a
// message preserves the position and meaning of each attachment.
type Attachment struct {
	Kind, Path  string
	Tip         string
	X, Y        float64
	Annotations []Annotation
}

// Annotation groups marks that share one comment. Coordinates are percentages
// of the upright original image, independent of zoom and window size.
type Annotation struct {
	ID      string `json:"id"`
	Comment string `json:"comment,omitempty"`
	Marks   []Mark `json:"marks,omitempty"`
}

type ImagePoint struct {
	X float64 `json:"x"`
	Y float64 `json:"y"`
}
type Mark struct {
	Kind   string       `json:"kind"`
	Points []ImagePoint `json:"points"`
	Color  string       `json:"color"`
	Width  float32      `json:"width"`
}

func ValidAnnotations(notes []Annotation) bool {
	if len(notes) > 100 {
		return false
	}
	ids := map[string]bool{}
	points, marks := 0, 0
	for _, note := range notes {
		if note.ID == "" || ids[note.ID] || len(note.Comment) > 16000 {
			return false
		}
		ids[note.ID] = true
		for _, m := range note.Marks {
			marks++
			points += len(m.Points)
			if marks > 500 || points > 20000 || !(m.Width >= 1 && m.Width <= 8) {
				return false
			}
			switch m.Color {
			case "#e05263", "#07896d", "#4775d1", "#d49726":
			default:
				return false
			}
			switch m.Kind {
			case "point":
				if len(m.Points) != 1 {
					return false
				}
			case "rectangle", "arrow":
				if len(m.Points) != 2 {
					return false
				}
			case "pen":
				if len(m.Points) < 2 {
					return false
				}
			default:
				return false
			}
			for _, p := range m.Points {
				if !(p.X >= 0 && p.X <= 100 && p.Y >= 0 && p.Y <= 100) {
					return false
				}
			}
		}
	}
	data, err := json.Marshal(notes)
	return err == nil && len(data) <= 1<<20
}

// Legacy point annotations remain readable without rewriting sent messages.
func (a Attachment) Notes() []Annotation {
	if len(a.Annotations) > 0 {
		return a.Annotations
	}
	if a.Tip != "" {
		return []Annotation{{ID: "legacy", Comment: a.Tip, Marks: []Mark{{Kind: "point", Points: []ImagePoint{{a.X, a.Y}}, Color: "#e05263", Width: 3}}}}
	}
	return nil
}

func (a Attachment) Link() string {
	u := url.URL{Scheme: "mujica", Host: a.Kind, Path: "/"}
	q := url.Values{"path": {a.Path}}
	if len(a.Annotations) > 0 {
		data, _ := json.Marshal(a.Annotations)
		q.Set("annotations", string(data))
		q.Set("v", "1")
	}
	if a.Tip != "" {
		q.Set("tip", a.Tip)
		q.Set("x", strconv.FormatFloat(a.X, 'f', 2, 64))
		q.Set("y", strconv.FormatFloat(a.Y, 'f', 2, 64))
	}
	u.RawQuery = q.Encode()
	return "<" + u.String() + ">"
}

type MessagePart struct {
	Text       string
	Attachment *Attachment
}

var attachmentLink = regexp.MustCompile(`<mujica://[^<>\s]+>`)

// Malformed and unknown links remain ordinary text, never local file access.
func MessageParts(text string) []MessagePart {
	var parts []MessagePart
	start := 0
	for _, match := range attachmentLink.FindAllStringIndex(text, -1) {
		u, err := url.Parse(text[match[0]+1 : match[1]-1])
		if err != nil || u.User != nil || u.Path != "/" || u.Fragment != "" || (u.Host != "image" && u.Host != "file") {
			continue
		}
		q, err := url.ParseQuery(u.RawQuery)
		if err != nil || !filepath.IsAbs(q.Get("path")) {
			continue
		}
		a := &Attachment{Kind: u.Host, Path: q.Get("path"), Tip: q.Get("tip")}
		if raw := q.Get("annotations"); raw != "" {
			if a.Kind != "image" || q.Get("v") != "1" || len(raw) > 1<<20 || json.Unmarshal([]byte(raw), &a.Annotations) != nil || !ValidAnnotations(a.Annotations) {
				continue
			}
		}
		if a.Tip != "" {
			x, ex := strconv.ParseFloat(q.Get("x"), 64)
			y, ey := strconv.ParseFloat(q.Get("y"), 64)
			if a.Kind != "image" || ex != nil || ey != nil || !(x >= 0 && x <= 100 && y >= 0 && y <= 100) {
				continue
			}
			a.X, a.Y = x, y
		}
		if match[0] > start {
			parts = append(parts, MessagePart{Text: text[start:match[0]]})
		}
		parts = append(parts, MessagePart{Attachment: a})
		start = match[1]
	}
	if start < len(text) {
		parts = append(parts, MessagePart{Text: text[start:]})
	}
	return parts
}

func (a Attachment) Prompt() string {
	if a.Kind == "file" {
		return fmt.Sprintf("Attached file: %q. Read this file using your tools as needed.", a.Path)
	}
	s := fmt.Sprintf("Attached image: %q.", a.Path)
	if len(a.Annotations) > 0 {
		s += "\nThe original image is followed by a numbered markup image. Each number identifies one annotation; multiple shapes with the same number share its comment. Coordinates are percentages of the upright original, from top-left (x right, y down)."
		for i, n := range a.Annotations {
			s += fmt.Sprintf("\nAnnotation %d: %s", i+1, n.Comment)
			if len(n.Marks) == 0 {
				s += " (whole image)"
			}
			for _, m := range n.Marks {
				s += "\n  " + m.Kind
				if len(m.Points) > 0 {
					s += fmt.Sprintf(" at (%.2f%%, %.2f%%)", m.Points[0].X, m.Points[0].Y)
				}
				if len(m.Points) == 2 {
					s += fmt.Sprintf(" to (%.2f%%, %.2f%%)", m.Points[1].X, m.Points[1].Y)
				}
			}
		}
	}
	if a.Tip != "" {
		s += fmt.Sprintf(" User annotation at (%.2f%%, %.2f%%), measured from the displayed image's top-left (x right, y down): %s", a.X, a.Y, a.Tip)
	}
	return s
}
