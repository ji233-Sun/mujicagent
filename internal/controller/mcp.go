package controller

import (
	"bytes"
	"encoding/json"
	"regexp"
	"strings"

	"github.com/ji233-Sun/mujicagent/internal/model"
)

func decodeMCPCall(raw json.RawMessage) *model.MCPCall {
	var call struct {
		Server, Tool      string
		Arguments, Result json.RawMessage
		Error             *struct{ Message string }
	}
	if json.Unmarshal(raw, &call) != nil {
		return nil
	}
	detail := &model.MCPCall{
		Server:    redactMCPText(call.Server),
		Tool:      redactMCPText(call.Tool),
		Arguments: redactMCPJSON(call.Arguments),
		Result:    redactMCPJSON(call.Result),
	}
	if call.Error != nil {
		detail.Error = redactMCPText(call.Error.Message)
	}
	return detail
}

const mcpRedacted = "[REDACTED]"

var (
	mcpCredentialAssignment = regexp.MustCompile(`(?i)((?:"|')?(?:[a-z0-9_.-]*(?:token|secret|password|passwd|passphrase|api[_-]?key|access[_-]?key|private[_-]?key)|authorization|proxy-authorization|credentials?)(?:"|')?\s*[:=]\s*)(?:(?:bearer|basic)\s+)?("[^"]*"|'[^']*'|[^\s,;&}\]]+)`)
	mcpAuthorization        = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[a-z0-9._~+/=-]+`)
	mcpCookie               = regexp.MustCompile(`(?im)(\b(?:cookie|set-cookie)\s*:\s*)[^\r\n]+`)
	mcpPrivateKey           = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	mcpURLCredentials       = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+:[^/@\s]+@`)
)

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
	text = mcpPrivateKey.ReplaceAllString(text, mcpRedacted)
	text = mcpURLCredentials.ReplaceAllString(text, "${1}"+mcpRedacted+"@")
	text = mcpCookie.ReplaceAllString(text, "${1}"+mcpRedacted)
	text = mcpCredentialAssignment.ReplaceAllString(text, "${1}"+mcpRedacted)
	return mcpAuthorization.ReplaceAllString(text, "${1} "+mcpRedacted)
}
