package ctl

import (
	"bytes"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"
)

// logFilter selects log lines. Lines that are not structured logs (for example Docker's own
// messages) always pass the severity and component filters, so errors are never hidden.
type logFilter struct {
	minLevel  int    // 0 debug, 1 info, 2 warn, 3 error
	iface     string // "rest", "contentrepo" or ""
	requestID string
}

func levelRank(s string) int {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return 0
	case "info":
		return 1
	case "warn", "warning":
		return 2
	case "error", "err":
		return 3
	}
	return -1
}

var textLevelRe = regexp.MustCompile(`\blevel=([A-Za-z]+)`)
var textIfaceRe = regexp.MustCompile(`\biface=([A-Za-z]+)`)
var textReqRe = regexp.MustCompile(`\brequest_id=("?)([^\s"]+)`)

// splitTimestamp separates the timestamp that docker compose logs --timestamps prepends.
func splitTimestamp(line string) (ts, rest string) {
	if i := strings.IndexByte(line, ' '); i > 18 && i < 40 && line[4] == '-' && line[10] == 'T' {
		return line[:i], line[i+1:]
	}
	return "", line
}

// match reports whether the (already timestamp-stripped) line passes the filter.
func (f logFilter) match(body string) bool {
	if f.minLevel <= 0 && f.iface == "" && f.requestID == "" {
		return true
	}
	level, iface, rid := -1, "", ""
	trim := strings.TrimSpace(body)
	if strings.HasPrefix(trim, "{") {
		var m map[string]any
		if json.Unmarshal([]byte(trim), &m) == nil {
			if s, ok := m["level"].(string); ok {
				level = levelRank(s)
			}
			iface, _ = m["iface"].(string)
			rid, _ = m["request_id"].(string)
		}
	} else {
		if m := textLevelRe.FindStringSubmatch(body); m != nil {
			level = levelRank(m[1])
		}
		if m := textIfaceRe.FindStringSubmatch(body); m != nil {
			iface = m[1]
		}
		if m := textReqRe.FindStringSubmatch(body); m != nil {
			rid = m[2]
		}
	}
	if level >= 0 && level < f.minLevel {
		return false
	}
	if f.iface != "" && iface != "" && !strings.EqualFold(iface, f.iface) {
		return false
	}
	if f.requestID != "" && rid != f.requestID {
		return false
	}
	return true
}

// lineWriter turns a byte stream into lines and calls fn for each.
type lineWriter struct {
	buf bytes.Buffer
	fn  func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.buf.Write(p)
	for {
		b := w.buf.Bytes()
		i := bytes.IndexByte(b, '\n')
		if i < 0 {
			break
		}
		line := strings.TrimRight(string(b[:i]), "\r")
		w.buf.Next(i + 1)
		w.fn(line)
	}
	return len(p), nil
}

func (w *lineWriter) Flush() {
	if w.buf.Len() > 0 {
		w.fn(strings.TrimRight(w.buf.String(), "\r\n"))
		w.buf.Reset()
	}
}

const exportWarning = `Log export warning: the file will contain operational data: SharePoint file and folder names, SAP document
IDs, client addresses and timing. Credentials, tokens, signed URLs and secKey values are masked, but masking is
a safety net, not a guarantee. Review the file before you share it, and store it as confidential.`

func (a *App) cmdLogs(ctx context.Context, args []string) int {
	fset := flag.NewFlagSet("logs", flag.ContinueOnError)
	fset.SetOutput(a.Err)
	dir := fset.String("dir", "", "deployment directory")
	follow := fset.Bool("follow", false, "follow the log output")
	fset.BoolVar(follow, "f", false, "follow the log output")
	tail := fset.Int("tail", 100, "number of lines from the end (0 = all)")
	since := fset.String("since", "", "only logs newer than this, e.g. 30m or 1h")
	level := fset.String("level", "", "minimum severity: debug, info, warn, error")
	iface := fset.String("iface", "", "only one interface: rest or contentrepo")
	fset.StringVar(iface, "component", "", "same as --iface")
	rid := fset.String("request-id", "", "only lines of one request ID")
	output := fset.String("output", "", "write the sanitized logs to this file instead of the terminal")
	yes := fset.Bool("yes", false, "confirm exporting to a file without asking")
	if err := fset.Parse(args); err != nil {
		return ExitUsage
	}
	if *tail < 0 {
		return a.fail(ExitUsage, "--tail must not be negative", "")
	}
	flt := logFilter{iface: strings.ToLower(*iface), requestID: *rid}
	if *level != "" {
		if flt.minLevel = levelRank(*level); flt.minLevel < 0 {
			return a.fail(ExitUsage, fmt.Sprintf("unknown --level %q", *level), "use debug, info, warn or error")
		}
	}
	if flt.iface != "" && flt.iface != "rest" && flt.iface != "contentrepo" {
		return a.fail(ExitUsage, fmt.Sprintf("unknown --iface %q", *iface), "use rest or contentrepo")
	}
	if *since != "" && !validSince(*since) {
		return a.fail(ExitUsage, fmt.Sprintf("invalid --since %q", *since), "use a duration such as 30m, 2h or 1h30m")
	}
	d, code := a.prepare(ctx, *dir)
	if code != ExitOK {
		return code
	}

	var dst io.Writer = a.Out
	var f *os.File
	if *output != "" {
		fmt.Fprintln(a.Err, exportWarning)
		if !a.confirm(ctx, "Write the sanitized logs to "+*output+"?", *yes) {
			return ExitDeclined
		}
		var err error
		f, err = os.OpenFile(*output, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return a.fail(ExitFail, "cannot create "+*output+": "+err.Error(), "choose a path that does not exist yet (existing files are never overwritten)")
		}
		defer f.Close()
		dst = f
	}

	san := a.sanitizerFor(d)
	emit := &lineWriter{fn: func(line string) {
		ts, body := splitTimestamp(line)
		if !flt.match(body) {
			return
		}
		out := san.Line(body)
		if ts != "" {
			out = ts + " " + out
		}
		fmt.Fprintln(dst, out)
	}}
	errLines := &lineWriter{fn: func(line string) { fmt.Fprintln(a.Err, san.Line(line)) }}

	sub := []string{"logs", "--no-color", "--no-log-prefix", "--timestamps"}
	if *tail > 0 {
		sub = append(sub, "--tail", fmt.Sprint(*tail))
	}
	if *since != "" {
		sub = append(sub, "--since", *since)
	}
	if *follow {
		sub = append(sub, "--follow")
	}
	res, err := a.compose(ctx, d, RunOpts{Stdout: emit, Stderr: errLines}, sub...)
	emit.Flush()
	errLines.Flush()
	if err != nil {
		return a.fail(ExitFail, "could not read the logs: "+err.Error(), "run: zaks-drive doctor")
	}
	if res.ExitCode != 0 {
		return ExitFail
	}
	return ExitOK
}

var sinceRe = regexp.MustCompile(`^([0-9]+(\.[0-9]+)?(ns|us|ms|s|m|h))+$`)

func validSince(s string) bool { return sinceRe.MatchString(s) }
