// Package apperr defines the adapter's error codes. Every error a caller or
// operator can see carries a stable code, what happened (detail), and what
// to do about it (fix), so whoever runs the server can correct the problem
// without reading source code.
//
// Codes are declared once in catalogue.go. The same catalogue produces
// docs/error-codes.md (`adapter errors`), which a test keeps in sync.
package apperr

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
)

// DocsURL is the page that explains every code; the anchor is the lower-case
// code.
const DocsURL = "https://github.com/Lamiv/zaks-drive/blob/main/docs/error-codes.md"

// Entry describes one code in the catalogue.
type Entry struct {
	Area   string
	Code   string
	Status int // default HTTP status; 0 for errors that never travel over HTTP
	Title  string
	Cause  string
	Fix    string
}

var (
	entries []Entry
	byCode  = map[string]*Entry{}
)

// def registers a code and returns it, so codes can be package variables
// that the compiler checks (a misspelt code does not compile).
func def(area, code string, status int, title, cause, fix string) string {
	if _, dup := byCode[code]; dup {
		panic("apperr: duplicate code " + code)
	}
	entries = append(entries, Entry{Area: area, Code: code, Status: status, Title: title, Cause: cause, Fix: fix})
	byCode[code] = &entries[len(entries)-1]
	// Appending can move the slice; rebuild the index (cheap, startup only).
	for i := range entries {
		byCode[entries[i].Code] = &entries[i]
	}
	return code
}

// Catalogue returns all codes in declaration order.
func Catalogue() []Entry { return append([]Entry(nil), entries...) }

// Lookup returns the catalogue entry of a code.
func Lookup(code string) (Entry, bool) {
	e, ok := byCode[code]
	if !ok {
		return Entry{}, false
	}
	return *e, true
}

// Error is an error with a catalogue code.
type Error struct {
	Code       string
	Detail     string // what happened, with the concrete values
	Cause      error  // underlying error; keeps errors.Is working for sentinels
	fix        string // overrides the catalogue's fix
	status     int    // overrides the catalogue's status
	RetryAfter time.Duration
	Operator   bool // true when the fix is for whoever runs the server, not for the caller
}

// New creates an error. detail may use fmt verbs.
func New(code, format string, args ...any) *Error {
	return &Error{Code: code, Detail: fmt.Sprintf(format, args...)}
}

// Wrap attaches an underlying error (for errors.Is / errors.As).
func (e *Error) Wrap(cause error) *Error { e.Cause = cause; return e }

// WithFix replaces the catalogue's generic fix with a specific one.
func (e *Error) WithFix(format string, args ...any) *Error {
	e.fix = fmt.Sprintf(format, args...)
	return e
}

// WithStatus overrides the catalogue's HTTP status.
func (e *Error) WithStatus(s int) *Error { e.status = s; return e }

// WithRetryAfter sets the delay after which the caller may retry.
func (e *Error) WithRetryAfter(d time.Duration) *Error { e.RetryAfter = d; return e }

func (e *Error) entry() Entry {
	if en, ok := Lookup(e.Code); ok {
		return en
	}
	return Entry{Code: e.Code, Title: e.Code, Status: 500}
}

// Title is the catalogue's one-line headline.
func (e *Error) Title() string { return e.entry().Title }

// Fix says what to do; a specific fix wins over the catalogue's.
func (e *Error) Fix() string {
	if e.fix != "" {
		return e.fix
	}
	return e.entry().Fix
}

// Status is the HTTP status for this error.
func (e *Error) Status() int {
	if e.status != 0 {
		return e.status
	}
	if s := e.entry().Status; s != 0 {
		return s
	}
	return 500
}

// URL points to this code's explanation.
func (e *Error) URL() string { return DocsURL + "#" + strings.ToLower(e.Code) }

func (e *Error) Error() string {
	if e.Detail == "" {
		return e.Code + ": " + e.Title()
	}
	return e.Code + ": " + e.Detail
}

func (e *Error) Unwrap() error { return e.Cause }

// LogSummary is what may be written to a log: the code, HTTP status and the
// catalogue title. Never Detail, which can quote text from a caller or a remote system.
func (e *Error) LogSummary() string {
	return fmt.Sprintf("%s (HTTP %d): %s", e.Code, e.Status(), e.Title())
}

// Message is a single line for channels without structure (the SAP
// X-ErrorDescription header, log lines): code, what happened, and the fix.
func (e *Error) Message() string {
	detail := e.Detail
	if detail == "" {
		detail = e.Title()
	}
	return fmt.Sprintf("%s: %s. Fix: %s", e.Code, strings.TrimRight(detail, ". "), e.Fix())
}

// As finds an *Error anywhere in err's chain.
func As(err error) (*Error, bool) {
	var e *Error
	if errors.As(err, &e) {
		return e, true
	}
	return nil, false
}

// ASCII makes s safe for an HTTP header: printable ASCII only, one line.
func ASCII(s string) string {
	var b strings.Builder
	space := false
	for _, r := range s {
		switch {
		case r == '\n' || r == '\r' || r == '\t' || unicode.IsSpace(r):
			space = true
			continue
		case r < 0x20 || r > 0x7e:
			r = '?'
		}
		if space && b.Len() > 0 {
			b.WriteByte(' ')
		}
		space = false
		b.WriteRune(r)
	}
	return b.String()
}

// Format renders err (possibly several joined errors) for a terminal:
// one block per error with the code, what happened and the fix.
func Format(err error) string {
	var list []error
	var collect func(error)
	collect = func(e error) {
		if e == nil {
			return
		}
		if j, ok := e.(interface{ Unwrap() []error }); ok {
			for _, x := range j.Unwrap() {
				collect(x)
			}
			return
		}
		list = append(list, e)
	}
	collect(err)
	var b strings.Builder
	for i, e := range list {
		if i > 0 {
			b.WriteByte('\n')
		}
		if ae, ok := As(e); ok {
			detail := ae.Detail
			if detail == "" {
				detail = ae.Title()
			}
			fmt.Fprintf(&b, "  [%s] %s\n      fix: %s\n      more: %s\n", ae.Code, detail, ae.Fix(), ae.URL())
		} else {
			fmt.Fprintf(&b, "  %s\n", e)
		}
	}
	return strings.TrimRight(b.String(), "\n")
}

// Markdown renders the catalogue as docs/error-codes.md.
func Markdown() string {
	var b strings.Builder
	b.WriteString("<!-- Generated by `adapter errors`; do not edit by hand. Source: internal/apperr/catalogue.go -->\n\n")
	b.WriteString("# Error codes\n\n")
	b.WriteString("Every error the adapter returns or prints carries a stable **code**, what happened, and what to do about it.\n\n")
	b.WriteString("- **REST API:** `application/problem+json` with `code`, `title`, `detail`, `hint` (the fix), `type` (a link to the code below) and `requestId`.\n")
	b.WriteString("- **SAP Content Server interface:** the `X-ErrorDescription` header reads `CODE: what happened. Fix: what to do`, and the HTTP status follows SAP's specification (400, 401, 403, 404, 406, 409, 500, 501).\n")
	b.WriteString("- **Start-up and command line:** the code, the detail and a `fix:` line are printed.\n")
	b.WriteString("- **Logs:** refused requests are logged with `code`, `reason` and `request_id`. Quote the request ID when you ask for help, and never include secrets.\n\n")
	b.WriteString("HTTP status `—` means the error is not sent over HTTP (start-up and command-line problems).\n\n")

	var area string
	var rows []Entry
	flush := func() {
		if len(rows) == 0 {
			return
		}
		fmt.Fprintf(&b, "## %s\n\n| Code | HTTP | Meaning |\n|---|---|---|\n", area)
		for _, e := range rows {
			fmt.Fprintf(&b, "| [`%s`](#%s) | %s | %s |\n", e.Code, strings.ToLower(e.Code), statusText(e.Status), e.Title)
		}
		b.WriteString("\n")
		for _, e := range rows {
			fmt.Fprintf(&b, "### `%s`\n\n**%s** (HTTP %s)\n\n- **Cause:** %s\n- **Fix:** %s\n\n", e.Code, e.Title, statusText(e.Status), e.Cause, e.Fix)
		}
		rows = nil
	}
	for _, e := range entries {
		if e.Area != area {
			flush()
			area = e.Area
		}
		rows = append(rows, e)
	}
	flush()
	return b.String()
}

func statusText(s int) string {
	if s == 0 {
		return "—"
	}
	return fmt.Sprint(s)
}
