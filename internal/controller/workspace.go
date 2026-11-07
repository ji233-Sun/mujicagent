package controller

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/model"
)

func workspaceSlug(text string) string {
	var slug strings.Builder
	for _, r := range strings.ToLower(text) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			slug.WriteRune(r)
		} else if slug.Len() > 0 && !strings.HasSuffix(slug.String(), "-") {
			slug.WriteByte('-')
		}
		if slug.Len() >= 24 {
			break
		}
	}
	result := strings.Trim(slug.String(), "-")
	if !strings.ContainsAny(result, "abcdefghijklmnopqrstuvwxyz") {
		return "chat"
	}
	return result
}

// Existing directories are never moved or recreated, including legacy chats.
func (c *Controller) prepareWorkspace(t *model.Thread, text string) error {
	if t.ProjectID != "" {
		return nil
	}
	if t.Cwd == "" {
		root := os.Getenv("MUJICAGENT_DATA_DIR")
		if root == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return err
			}
			root = filepath.Join(home, ".mujicagent")
		}
		created := t.Created
		if created.IsZero() {
			created = t.Updated
		}
		parent := filepath.Join(root, "workspace", created.Format("2006/01"))
		if err := os.MkdirAll(parent, 0700); err != nil {
			return err
		}
		name := t.Title
		if name == "" || name == "New conversation" || name == "新对话" {
			var content strings.Builder
			for _, part := range model.MessageParts(text) {
				if part.Attachment == nil {
					content.WriteString(part.Text)
				}
			}
			name = content.String()
		}
		path, err := createWorkspace(parent, workspaceSlug(name), func() string { return model.ID()[:8] })
		if err != nil {
			return err
		}
		t.Cwd = path
	}
	// Retry persistence after a failed save before allowing any agent operation.
	return c.saveState()
}

func createWorkspace(parent, slug string, id func() string) (string, error) {
	for range 100 {
		path := filepath.Join(parent, slug+"-"+id())
		if err := os.Mkdir(path, 0700); os.IsExist(err) {
			continue
		} else if err != nil {
			return "", err
		}
		return path, nil
	}
	return "", fmt.Errorf("could not allocate a unique conversation directory")
}
