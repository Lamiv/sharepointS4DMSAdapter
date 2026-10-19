package safelog

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"strings"
	"testing"
)

const secret = "SYNTHETIC-SECRET-9f2c"

type remoteErr struct{ msg string }

func (e *remoteErr) Error() string { return e.msg }

func TestSummaryNeverContainsRemoteText(t *testing.T) {
	cases := map[string]error{
		"url error with pre-authenticated URL": &url.Error{Op: "Get", URL: "https://x.sharepoint.com/dl?tempauth=" + secret, Err: errors.New("boom " + secret)},
		"wrapped unknown error":                fmt.Errorf("while reading: %w", &remoteErr{"Authorization: Bearer " + secret}),
		"plain unknown error":                  errors.New("token=" + secret),
		"path error":                           &os.PathError{Op: "open", Path: "/var/spool/" + secret, Err: errors.New("denied")},
	}
	for name, err := range cases {
		if got := Summary(err); strings.Contains(got, secret) || strings.Contains(got, "http") {
			t.Errorf("%s: summary %q leaks", name, got)
		}
	}
}

func TestSummaryKeepsUsefulStructure(t *testing.T) {
	sentinel := errors.New("a constant message of this program")
	Allow(sentinel)
	for err, want := range map[error]string{
		context.DeadlineExceeded:                                "deadline exceeded",
		fmt.Errorf("ctx: %w", context.Canceled):                 "context canceled",
		io.ErrUnexpectedEOF:                                     "unexpected EOF",
		fmt.Errorf("wrap: %w", sentinel):                        "a constant message of this program",
		&url.Error{Op: "Post", URL: "https://a/b", Err: io.EOF}: "Post request failed: EOF",
	} {
		if got := Summary(err); !strings.Contains(got, want) {
			t.Errorf("Summary(%v) = %q, want it to contain %q", err, got, want)
		}
	}
}

func TestRedact(t *testing.T) {
	for in, forbidden := range map[string][]string{
		"GET https://x.sharepoint.com/f/a.docx?tempauth=" + secret + "&e=1 failed": {secret, "tempauth"},
		"https://user:" + secret + "@host/path":                                    {secret},
		"header Authorization: Bearer " + secret + " rejected":                     {secret},
		"token eyJhbGciOiJSUzI1NiJ9.eyJzdWIiOiIxMjM0NTYifQ.c2lnbmF0dXJl bad":       {"eyJhbGci"},
		"secKey=" + secret + "&x=1":                                                {secret},
		"client_secret=" + secret:                                                  {secret},
		"bearer " + secret:                                                         {secret},
	} {
		if out := Redact(in); func() bool {
			for _, f := range forbidden {
				if strings.Contains(out, f) {
					return true
				}
			}
			return false
		}() {
			t.Errorf("Redact(%q) = %q still contains a forbidden value", in, out)
		}
	}
	if got := Redact("HTTP 404 itemNotFound: the item does not exist"); got != "HTTP 404 itemNotFound: the item does not exist" {
		t.Errorf("harmless text changed: %q", got)
	}
	if got := Redact("see https://learn.microsoft.com/graph/errors."); !strings.Contains(got, "https://learn.microsoft.com/graph/errors.") {
		t.Errorf("plain URL should survive: %q", got)
	}
}

func TestRedactValues(t *testing.T) {
	got := RedactValues("line 4: cannot unmarshal !!str `" + secret + "` into int")
	if strings.Contains(got, secret) {
		t.Fatalf("%q", got)
	}
}

func TestHeaderValuesHidesEverythingNotAllowListed(t *testing.T) {
	h := map[string][]string{
		"Content-Type":  {"text/plain"},
		"Authorization": {"Bearer " + secret},
		"X-Api-Key":     {secret},
	}
	out := HeaderValues(h, "Content-Type")
	if strings.Contains(out, secret) || !strings.Contains(out, "Content-Type: text/plain") || !strings.Contains(out, "Authorization: <hidden>") {
		t.Fatalf("%q", out)
	}
}
