package ctl

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// sampleLogs mixes structured lines, a plain line and lines full of synthetic secrets, the way a
// misbehaving upstream or a future bug could produce them.
func sampleLogs() string {
	return strings.Join([]string{
		`2026-10-19T10:00:00.000000001Z {"time":"2026-10-19T10:00:00Z","level":"INFO","msg":"request","iface":"rest","request_id":"r-rest-1","status":200}`,
		`2026-10-19T10:00:01.000000001Z {"time":"2026-10-19T10:00:01Z","level":"WARN","msg":"content server request refused","iface":"contentrepo","request_id":"r-cs-1","code":"CS_CERT_INACTIVE"}`,
		`2026-10-19T10:00:02.000000001Z {"time":"2026-10-19T10:00:02Z","level":"ERROR","msg":"graph retry","iface":"rest","request_id":"r-rest-2","error":"HTTP 503 with Authorization: Bearer ` + secAPI + `"}`,
		`2026-10-19T10:00:03.000000001Z {"level":"DEBUG","msg":"content server request","iface":"contentrepo","query":"get&secKey=SYNTH-SECKEY-123456&docId=D1","token":"` + secAdmin + `"}`,
		`2026-10-19T10:00:04.000000001Z time=2026-10-19T10:00:04Z level=INFO msg=hello iface=rest request_id=r-text-1 client_secret=` + secClient,
		`2026-10-19T10:00:05.000000001Z download https://contoso.sharepoint.com/f/a.docx?tempauth=SYNTH-TEMPAUTH-999&e=1 failed`,
		`2026-10-19T10:00:06.000000001Z plain docker message without structure ` + secClient,
	}, "\n") + "\n"
}

func logEnv(t *testing.T) *env {
	e := newEnv(t)
	base := healthyDocker("running")
	e.run.handler = func(c call) (Result, error) {
		if strings.Contains(c.String(), " logs ") {
			return Result{Stdout: sampleLogs()}, nil
		}
		return base(c)
	}
	return e
}

func TestLogsAreSanitizedEvenWhenTheBackendLeaks(t *testing.T) {
	e := logEnv(t)
	if rc := e.main("logs", "--dir", e.dir); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, e.both())
	}
	out := e.out.String()
	for _, secret := range []string{secClient, secAPI, secAdmin, "SYNTH-SECKEY-123456", "SYNTH-TEMPAUTH-999"} {
		if strings.Contains(out, secret) || strings.Contains(e.err.String(), secret) {
			t.Errorf("synthetic secret %q leaked into the log output:\n%s", secret, out)
		}
	}
	for _, want := range []string{"2026-10-19T10:00:00.000000001Z", "r-rest-1", "CS_CERT_INACTIVE", "r-text-1", "plain docker message"} {
		if !strings.Contains(out, want) {
			t.Errorf("timestamps, correlation IDs and codes must be preserved: lacks %q\n%s", want, out)
		}
	}
}

func TestLogsCommandConstruction(t *testing.T) {
	e := logEnv(t)
	if rc := e.main("logs", "--dir", e.dir, "--follow", "--tail", "200", "--since", "30m"); rc != ExitOK {
		t.Fatalf("exit %d", rc)
	}
	var got string
	for _, c := range e.run.all() {
		if strings.Contains(c, " logs ") {
			got = c
		}
	}
	for _, want := range []string{"compose -f " + filepath.Join(e.dir, "docker-compose.yml") + " logs", "--timestamps", "--no-log-prefix", "--tail 200", "--since 30m", "--follow"} {
		if !strings.Contains(got, want) {
			t.Errorf("%q lacks %q", got, want)
		}
	}
	// Default: a bounded tail.
	e.run.calls = nil
	e.main("logs", "--dir", e.dir)
	if !e.run.has("--tail 100") {
		t.Errorf("the default must be a bounded tail: %v", e.run.all())
	}
	if e.run.has("--follow") {
		t.Error("--follow must be opt-in")
	}
}

func TestLogFilters(t *testing.T) {
	e := logEnv(t)
	run := func(args ...string) string {
		e.main(append([]string{"logs", "--dir", e.dir}, args...)...)
		return e.out.String()
	}
	if out := run("--level", "warn"); strings.Contains(out, "r-rest-1") || strings.Contains(out, "r-text-1") ||
		!strings.Contains(out, "r-cs-1") || !strings.Contains(out, "r-rest-2") || !strings.Contains(out, "plain docker message") {
		t.Errorf("--level warn:\n%s", out)
	}
	if out := run("--level", "error"); strings.Contains(out, "r-cs-1") || !strings.Contains(out, "r-rest-2") {
		t.Errorf("--level error:\n%s", out)
	}
	if out := run("--iface", "contentrepo"); strings.Contains(out, "r-rest-1") || !strings.Contains(out, "r-cs-1") {
		t.Errorf("--iface contentrepo:\n%s", out)
	}
	if out := run("--component", "rest"); strings.Contains(out, "r-cs-1") || !strings.Contains(out, "r-rest-1") || !strings.Contains(out, "r-text-1") {
		t.Errorf("--component rest (text format too):\n%s", out)
	}
	out := run("--request-id", "r-cs-1")
	if !strings.Contains(out, "r-cs-1") || strings.Contains(out, "r-rest-1") || strings.Contains(out, "r-rest-2") {
		t.Errorf("--request-id:\n%s", out)
	}
}

func TestExportingLogsNeedsConfirmationAndNeverOverwrites(t *testing.T) {
	e := logEnv(t)
	target := filepath.Join(t.TempDir(), "logs.txt")
	// Not a terminal and no --yes: refused, nothing written, warning shown.
	if rc := e.main("logs", "--dir", e.dir, "--output", target); rc != ExitDeclined {
		t.Fatalf("exit %d\n%s", rc, e.both())
	}
	if !strings.Contains(e.err.String(), "Log export warning") {
		t.Errorf("the warning must be shown first: %s", e.err.String())
	}
	if _, err := os.Stat(target); err == nil {
		t.Fatal("no file may be written without confirmation")
	}
	if rc := e.main("logs", "--dir", e.dir, "--output", target, "--yes"); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, e.both())
	}
	b, err := os.ReadFile(target)
	must(t, err)
	for _, secret := range []string{secClient, secAPI, secAdmin, "SYNTH-SECKEY-123456", "SYNTH-TEMPAUTH-999"} {
		if strings.Contains(string(b), secret) {
			t.Errorf("secret %q in the exported file", secret)
		}
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(target); st.Mode().Perm() != 0o600 {
			t.Errorf("exported logs must be mode 600, got %v", st.Mode().Perm())
		}
	}
	if rc := e.main("logs", "--dir", e.dir, "--output", target, "--yes"); rc != ExitFail {
		t.Errorf("an existing file must not be overwritten: exit %d", rc)
	}
}

func TestLineWriterHandlesPartialLines(t *testing.T) {
	var got []string
	w := &lineWriter{fn: func(l string) { got = append(got, l) }}
	_, _ = w.Write([]byte("a\r\nb"))
	_, _ = w.Write([]byte("c\nd"))
	w.Flush()
	if strings.Join(got, "|") != "a|bc|d" {
		t.Errorf("%v", got)
	}
}

func TestSplitTimestamp(t *testing.T) {
	ts, rest := splitTimestamp("2026-10-19T10:00:00.123456789Z hello world")
	if ts != "2026-10-19T10:00:00.123456789Z" || rest != "hello world" {
		t.Errorf("%q %q", ts, rest)
	}
	if ts, rest := splitTimestamp("no timestamp here"); ts != "" || rest != "no timestamp here" {
		t.Errorf("%q %q", ts, rest)
	}
}
