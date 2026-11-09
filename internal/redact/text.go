// Package redact shares the text credential policy used for untrusted tool data.
package redact

import "regexp"

const Marker = "[REDACTED]"

var (
	assignment     = regexp.MustCompile(`(?i)((?:"|')?(?:[a-z0-9_.-]*(?:token|secret|password|passwd|passphrase|api[_-]?key|access[_-]?key|private[_-]?key)|authorization|proxy-authorization|credentials?)(?:"|')?\s*[:=]\s*)(?:(?:bearer|basic)\s+)?("[^"]*"|'[^']*'|[^\s,;&}\]]+)`)
	authorization  = regexp.MustCompile(`(?i)\b(bearer|basic)\s+[a-z0-9._~+/=-]+`)
	cookie         = regexp.MustCompile(`(?im)(\b(?:cookie|set-cookie)\s*:\s*)[^\r\n]+`)
	privateKey     = regexp.MustCompile(`(?s)-----BEGIN [A-Z ]*PRIVATE KEY-----.*?-----END [A-Z ]*PRIVATE KEY-----`)
	urlCredentials = regexp.MustCompile(`(?i)([a-z][a-z0-9+.-]*://)[^/@\s]+:[^/@\s]+@`)
)

// Text removes recognized credentials while preserving useful surrounding text.
func Text(text string) string {
	text = privateKey.ReplaceAllString(text, Marker)
	text = urlCredentials.ReplaceAllString(text, "${1}"+Marker+"@")
	text = cookie.ReplaceAllString(text, "${1}"+Marker)
	text = assignment.ReplaceAllString(text, "${1}"+Marker)
	return authorization.ReplaceAllString(text, "${1} "+Marker)
}
