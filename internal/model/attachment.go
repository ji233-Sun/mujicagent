package model

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strconv"
)

// Attachment links live in the message itself, so copying or persisting a
// message preserves the position and meaning of each attachment.
type Attachment struct {
	Kind, Path string
	Tip        string
	X, Y       float64
}

func (a Attachment) Link() string {
	u := url.URL{Scheme: "mujica", Host: a.Kind, Path: "/"}
	q := url.Values{"path": {a.Path}}
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
	if a.Tip != "" {
		s += fmt.Sprintf(" User annotation at (%.2f%%, %.2f%%), measured from the displayed image's top-left (x right, y down): %s", a.X, a.Y, a.Tip)
	}
	return s
}
