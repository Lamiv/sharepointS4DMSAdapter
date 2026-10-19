// Package safelog is the logging policy of ZAKS Drive for anything that came
// from outside the process: error values, HTTP responses and request headers.
//
// The rule: a log line carries only explicitly approved fields (request ID,
// HTTP status, application error code and title, operation, repository,
// document identifiers, error category). It never carries free text taken from
// a remote system or a caller, because that text can contain credentials:
//
//   - Go's *url.Error includes the full request URL, and Microsoft Graph download
//     and upload-session URLs are pre-authenticated (the query string is a bearer
//     credential);
//   - Microsoft Graph and Entra ID error bodies are written by a third party;
//   - request headers can carry API keys, bearer tokens or SAP secKeys, and a
//     malformed credential can end up quoted in a parser error.
//
// Summary builds a description of an error from its structure (type, code,
// status) instead of its message. Redact scrubs text that must be shown to an
// operator or caller (for example Microsoft's explanation of a failure) of URL
// query strings and credential-shaped values. Neither is a substitute for the
// other: log lines use Summary, user-facing explanations use Redact.
package safelog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"syscall"
)

// Summarizer is implemented by errors that can describe themselves without
// free text from a remote system (application errors, Graph errors).
type Summarizer interface{ LogSummary() string }

var allowSet = map[error]bool{}

// Allow registers package-level sentinel errors whose message is a constant
// written by this program, so it may be logged verbatim.
func Allow(errs ...error) {
	for _, e := range errs {
		if e != nil {
			allowSet[e] = true
		}
	}
}

// Err is the log attribute for an error: slog.Any("error", err) must not be used
// for errors that can carry remote text.
func Err(err error) slog.Attr { return slog.String("error", Summary(err)) }

// Summary describes err by its structure only. It never contains a URL, a
// header value, a response body or any other text supplied by a remote party.
func Summary(err error) string {
	if err == nil {
		return ""
	}
	var parts []string
	add := func(s string) {
		if s != "" && (len(parts) == 0 || parts[len(parts)-1] != s) {
			parts = append(parts, s)
		}
	}
	for e := err; e != nil; e = errors.Unwrap(e) {
		if s, ok := e.(Summarizer); ok {
			add(s.LogSummary())
			continue
		}
		if allowed(e) {
			add(e.Error())
			return strings.Join(parts, ": ")
		}
		switch x := e.(type) {
		case *url.Error:
			add(x.Op + " request failed") // never x.URL
			continue
		case *net.OpError:
			add(x.Op + " " + x.Net)
			continue
		case *fs.PathError:
			add(x.Op) // never the path
			continue
		case syscall.Errno:
			add(x.Error())
			return strings.Join(parts, ": ")
		}
		switch {
		case e == context.Canceled:
			add("context canceled")
		case e == context.DeadlineExceeded:
			add("deadline exceeded")
		case e == io.EOF:
			add("EOF")
		case e == io.ErrUnexpectedEOF:
			add("unexpected EOF")
		default:
			if errors.Unwrap(e) == nil {
				var ne net.Error
				if errors.As(e, &ne) && ne.Timeout() {
					add("timeout")
				} else {
					add(typeName(e)) // leaf of unknown origin: its type, not its message
				}
			}
		}
	}
	return strings.Join(parts, ": ")
}

func allowed(e error) bool { return allowSet[e] }

func typeName(e error) string { return fmt.Sprintf("%T", e) }

// ---- Redact ----

var (
	urlRe       = regexp.MustCompile(`https?://[^\s"'<>)\]]+`)
	jwtRe       = regexp.MustCompile(`eyJ[A-Za-z0-9_-]{8,}\.[A-Za-z0-9_-]{8,}\.?[A-Za-z0-9_-]*`)
	bearerRe    = regexp.MustCompile(`(?i)\b(bearer|apikey|basic)\s+[A-Za-z0-9._~+/=-]{6,}`)
	secretKVRe  = regexp.MustCompile(`(?i)\b(seckey|client_secret|access_token|refresh_token|id_token|api[_-]?key|x-api-key|authorization|password|passwd|secret|token|sig|tempauth)=[^&\s"']+`)
	backtickRe  = regexp.MustCompile("`[^`]*`")
	hdrKVRe     = regexp.MustCompile(`(?i)\b(authorization|x-api-key|cookie|set-cookie|proxy-authorization):\s*[^\r\n]+`)
	whitespaces = regexp.MustCompile(`\s+`)
)

// Redact removes what must never leave the process from text that is shown to
// an operator or caller: URL query strings, fragments and credentials, bearer
// tokens, JWTs, and values of credential-like parameters and headers.
func Redact(s string) string {
	if s == "" {
		return s
	}
	s = urlRe.ReplaceAllStringFunc(s, func(m string) string {
		trail := ""
		for len(m) > 0 && strings.ContainsRune(".,;:", rune(m[len(m)-1])) {
			trail = m[len(m)-1:] + trail
			m = m[:len(m)-1]
		}
		u, err := url.Parse(m)
		if err != nil {
			return "<url>" + trail
		}
		u.User, u.RawQuery, u.Fragment, u.ForceQuery = nil, "", "", false
		out := u.String()
		if strings.Contains(m, "?") {
			out += "?<redacted>"
		}
		return out + trail
	})
	s = hdrKVRe.ReplaceAllString(s, "$1: <redacted>")
	s = jwtRe.ReplaceAllString(s, "<redacted-token>")
	s = bearerRe.ReplaceAllString(s, "$1 <redacted>")
	s = secretKVRe.ReplaceAllString(s, "$1=<redacted>")
	return s
}

// RedactValues hides the quoted values inside parser errors, such as YAML's
// "cannot unmarshal !!str `value` into int", which would otherwise echo the
// offending configuration value (possibly a secret).
func RedactValues(s string) string { return backtickRe.ReplaceAllString(s, "`<value>`") }

// OneLine collapses whitespace so a message cannot forge extra log lines.
func OneLine(s string) string { return strings.TrimSpace(whitespaces.ReplaceAllString(s, " ")) }

// HeaderNames lists the names of the headers (never their values), for debug
// logs that need to show which headers were present.
func HeaderNames(h map[string][]string) string {
	names := make([]string, 0, len(h))
	for k := range h {
		names = append(names, k)
	}
	sortStrings(names)
	return strings.Join(names, ",")
}

// HeaderValues returns "Name: value" for the allow-listed headers only. Every
// other header is shown by name with its value replaced.
func HeaderValues(h map[string][]string, safe ...string) string {
	ok := map[string]bool{}
	for _, n := range safe {
		ok[strings.ToLower(n)] = true
	}
	var parts []string
	for k, v := range h {
		val := "<hidden>"
		if ok[strings.ToLower(k)] {
			val = OneLine(strings.Join(v, ","))
			if len(val) > 80 {
				val = val[:80] + "..."
			}
		}
		parts = append(parts, k+": "+val)
	}
	sortStrings(parts)
	if len(parts) == 0 {
		return "(none)"
	}
	return strings.Join(parts, " | ")
}

func sortStrings(a []string) { sort.Strings(a) }
