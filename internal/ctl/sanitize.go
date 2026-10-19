package ctl

import (
	"net/url"
	"regexp"
	"strings"

	"github.com/Lamiv/zaks-drive/internal/safelog"
)

// Sanitizer masks credentials in text that leaves the tool: logs shown on a terminal, written
// to a file or put into a support bundle. It works in two ways at once, because neither is
// enough alone:
//
//   - by value: every secret value that the deployment's .env holds is replaced wherever it
//     appears (plain, URL-encoded);
//   - by pattern: URL query strings, bearer tokens, JWTs, credential-shaped key=value pairs and
//     JSON fields, and Authorization-like headers are masked (safelog.Redact plus the JSON forms).
//
// The server already avoids logging secrets (docs/operations/security.md); this is a second
// line of defence, not a replacement for it. The support bundle additionally uses allowlists.
type Sanitizer struct {
	values []string
}

// NewSanitizer returns a Sanitizer that also masks the given secret values.
func NewSanitizer(secretValues []string) *Sanitizer {
	var vs []string
	for _, v := range secretValues {
		if len(v) < 6 {
			continue // too short to mask by value without mangling ordinary words
		}
		vs = append(vs, v)
		if q := url.QueryEscape(v); q != v {
			vs = append(vs, q)
		}
		if q := url.PathEscape(v); q != v {
			vs = append(vs, q)
		}
	}
	return &Sanitizer{values: vs}
}

var jsonSecretRe = regexp.MustCompile(`(?i)("(?:authorization|proxy-authorization|x-api-key|api[_-]?key|client[_-]?secret|secret|password|passwd|token|access[_-]?token|refresh[_-]?token|id[_-]?token|seckey|cookie|set-cookie)"\s*:\s*")[^"]*(")`)

// Line sanitizes one line (or any text; it does not rely on line structure).
func (s *Sanitizer) Line(l string) string {
	for _, v := range s.values {
		if strings.Contains(l, v) {
			l = strings.ReplaceAll(l, v, "<redacted>")
		}
	}
	l = safelog.Redact(l)
	l = jsonSecretRe.ReplaceAllString(l, "${1}<redacted>${2}")
	return l
}

// Text sanitizes a multi-line string.
func (s *Sanitizer) Text(t string) string {
	lines := strings.Split(t, "\n")
	for i := range lines {
		lines[i] = s.Line(lines[i])
	}
	return strings.Join(lines, "\n")
}

// sanitizerFor builds the Sanitizer for a deployment (masking its secret values by value).
func (a *App) sanitizerFor(d *Deployment) *Sanitizer {
	if d == nil {
		return NewSanitizer(nil)
	}
	return NewSanitizer(d.secretValues())
}
