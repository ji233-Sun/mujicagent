package controller

import (
	"bytes"
	"encoding/json"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/redact"
)

const mcpRedacted = redact.Marker

func sensitiveMCPKey(key string) bool {
	key = strings.ToLower(strings.Map(func(r rune) rune {
		if r == '_' || r == '-' || r == '.' || r == ' ' {
			return -1
		}
		return r
	}, key))
	for _, suffix := range []string{"token", "secret", "password", "passwd", "passphrase", "apikey", "accesskey", "privatekey"} {
		if strings.HasSuffix(key, suffix) {
			return true
		}
	}
	switch key {
	case "authorization", "proxyauthorization", "cookie", "setcookie", "credential", "credentials":
		return true
	}
	return false
}

func redactMCPJSON(raw json.RawMessage) string {
	if len(raw) == 0 || string(bytes.TrimSpace(raw)) == "null" {
		return ""
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return ""
	}
	data, _ := json.MarshalIndent(redactMCPValue(value), "", "  ")
	return string(data)
}

func redactMCPValue(value any) any {
	switch value := value.(type) {
	case map[string]any:
		for key, field := range value {
			if sensitiveMCPKey(key) {
				value[key] = mcpRedacted
			} else {
				value[key] = redactMCPValue(field)
			}
		}
		return value
	case []any:
		for i, field := range value {
			value[i] = redactMCPValue(field)
		}
		return value
	case string:
		return redactMCPText(value)
	default:
		return value
	}
}

// Text content and errors can contain embedded JSON or credential headers too.
func redactMCPText(text string) string {
	trimmed := strings.TrimSpace(text)
	if (strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[")) && json.Valid([]byte(trimmed)) {
		return redactMCPJSON(json.RawMessage(trimmed))
	}
	return redact.Text(text)
}
