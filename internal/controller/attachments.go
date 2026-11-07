package controller

import (
	"fmt"
	"os"

	"github.com/ji233-Sun/mujicagent/internal/harness"
	"github.com/ji233-Sun/mujicagent/internal/markup"
	"github.com/ji233-Sun/mujicagent/internal/model"
)

func messageInput(text, dir string) (harness.Input, error) {
	var input harness.Input
	addText := func(s string) {
		input = append(input, harness.InputPart{Type: "text", Text: s})
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
			input = append(input, harness.InputPart{Type: "localImage", Path: a.Path})
			if len(a.Annotations) > 0 {
				path, err := markup.Export(*a, dir)
				if err != nil {
					return nil, fmt.Errorf("annotated image: %w", err)
				}
				input = append(input, harness.InputPart{Type: "localImage", Path: path})
			}
		}
	}
	return input, nil
}
