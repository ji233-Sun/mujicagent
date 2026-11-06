package controller

import (
	"fmt"
	"os"

	"github.com/ji233-Sun/mujicagent/internal/model"
)

func messageInput(text string) ([]any, error) {
	var input []any
	addText := func(s string) {
		input = append(input, map[string]any{"type": "text", "text": s, "text_elements": []any{}})
	}
	for _, part := range model.MessageParts(text) {
		if part.Attachment == nil {
			addText(part.Text)
			continue
		}
		a := part.Attachment
		info, err := os.Stat(a.Path)
		if err != nil {
			return nil, fmt.Errorf("attachment %q: %w", a.Path, err)
		}
		if !info.Mode().IsRegular() {
			return nil, fmt.Errorf("attachment is not a regular file: %s", a.Path)
		}
		addText(a.Prompt())
		if a.Kind == "image" {
			input = append(input, map[string]any{"type": "localImage", "path": a.Path})
		}
	}
	return input, nil
}
