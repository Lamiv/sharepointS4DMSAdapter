package apperr

import (
	"errors"
	"os"
	"strings"
	"testing"
)

func TestEveryCodeIsComplete(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range Catalogue() {
		if seen[e.Code] {
			t.Errorf("duplicate code %s", e.Code)
		}
		seen[e.Code] = true
		if e.Code != strings.ToUpper(e.Code) || strings.ContainsAny(e.Code, " -") {
			t.Errorf("%s: codes are UPPER_SNAKE_CASE", e.Code)
		}
		for name, v := range map[string]string{"area": e.Area, "title": e.Title, "cause": e.Cause, "fix": e.Fix} {
			if strings.TrimSpace(v) == "" {
				t.Errorf("%s has no %s", e.Code, name)
			}
		}
		if e.Fix != "" && !strings.HasSuffix(strings.TrimSpace(e.Fix), ".") && !strings.HasSuffix(strings.TrimSpace(e.Fix), "?") {
			t.Errorf("%s: the fix should be a sentence ending with a full stop: %q", e.Code, e.Fix)
		}
		if e.Status != 0 && (e.Status < 400 || e.Status > 599) {
			t.Errorf("%s: status %d is not an HTTP error", e.Code, e.Status)
		}
	}
}

// docs/error-codes.md is generated from the catalogue. If this fails, run
//
//	go run ./cmd/adapter errors > docs/error-codes.md
func TestErrorCodesDocIsCurrent(t *testing.T) {
	got, err := os.ReadFile("../../docs/error-codes.md")
	if err != nil {
		t.Fatalf("docs/error-codes.md missing: %v (generate it with `go run ./cmd/adapter errors > docs/error-codes.md`)", err)
	}
	if strings.ReplaceAll(string(got), "\r\n", "\n") != Markdown() {
		t.Fatal("docs/error-codes.md is out of date; regenerate with: go run ./cmd/adapter errors > docs/error-codes.md")
	}
}

func TestErrorBehaviour(t *testing.T) {
	sentinel := errors.New("sentinel")
	e := New(NameInvalid, "the name %q is bad", "a:b").Wrap(sentinel)
	if !errors.Is(e, sentinel) {
		t.Error("Wrap must keep errors.Is working")
	}
	if e.Status() != 400 || e.Code != "NAME_INVALID" {
		t.Errorf("status/code: %d %s", e.Status(), e.Code)
	}
	if !strings.Contains(e.Message(), "NAME_INVALID: the name \"a:b\" is bad. Fix: ") {
		t.Errorf("message: %s", e.Message())
	}
	if e.WithStatus(422).Status() != 422 || e.WithFix("do %s", "this").Fix() != "do this" {
		t.Error("overrides")
	}
	if !strings.HasSuffix(e.URL(), "docs/error-codes.md#name_invalid") {
		t.Errorf("url %s", e.URL())
	}
	if got, ok := As(errors.Join(errors.New("x"), e)); !ok || got.Code != "NAME_INVALID" {
		t.Error("As must find the coded error in a joined error")
	}
}

func TestASCIIAndFormat(t *testing.T) {
	if got := ASCII("a\r\nb\tc é d"); got != "a b c ? d" {
		t.Errorf("ASCII: %q", got)
	}
	out := Format(errors.Join(New(CfgNoRepositories, "none"), errors.New("plain")))
	for _, want := range []string{"[CFG_NO_REPOSITORIES] none", "fix: ", "more: ", "plain"} {
		if !strings.Contains(out, want) {
			t.Errorf("Format lacks %q:\n%s", want, out)
		}
	}
}
