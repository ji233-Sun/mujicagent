package harness

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"
)

func imagePrompt(o RunOptions) ([]any, error) {
	parts := []any{map[string]string{"type": "text", "text": o.Prompt}}
	for _, path := range o.Images {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		mime := http.DetectContentType(data)
		if !strings.HasPrefix(mime, "image/") {
			return nil, fmt.Errorf("unsupported image: %s", path)
		}
		parts = append(parts, map[string]string{"type": "image", "mimeType": mime, "data": base64.StdEncoding.EncodeToString(data)})
	}
	return parts, nil
}
