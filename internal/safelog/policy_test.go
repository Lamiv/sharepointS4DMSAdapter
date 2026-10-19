package safelog

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// The logging policy, enforced: a log call must not take an error's text or an application error's
// Detail. Use safelog.Err(err) (or the code and title) instead. The patterns are deliberately simple;
// when one fires, fix the call rather than the test.
func TestLogCallsDoNotTakeRawErrorsOrDetail(t *testing.T) {
	logCall := regexp.MustCompile(`\b(log|Log|logger|s\.log|a\.log|c\.log|pr\.s\.log)\.(Debug|Info|Warn|Error|LogAttrs)\(|slog\.(Debug|Info|Warn|Error)\(`)
	bad := []*regexp.Regexp{
		regexp.MustCompile(`"(error|err|reason|detail|cause)",\s*(err|lastErr|derr|e\.Detail|ae\.Detail|[a-zA-Z.]*\.Detail)\b`),
		regexp.MustCompile(`slog\.String\("(error|err|reason|detail)",\s*[^)]*(\.Error\(\)|\.Detail)`),
		regexp.MustCompile(`slog\.Any\("(error|err)"`),
		regexp.MustCompile(`\.Error\(\)`),
	}
	// Startup messages shown to the operator who owns the configuration are allowed to carry a
	// redacted detail; they are listed here by the exact call so any new one is noticed.
	allowed := []string{`"detail", safelog.OneLine(safelog.Redact(ae.Detail))`}
	_ = filepath.WalkDir("../..", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}
		slash := filepath.ToSlash(path)
		if strings.Contains(slash, "/internal/graphmock/") || strings.Contains(slash, "/internal/safelog/") {
			return nil // the test emulator and this package
		}
		raw, _ := os.ReadFile(path)
		lines := strings.Split(string(raw), "\n")
		for i := 0; i < len(lines); i++ {
			if !logCall.MatchString(lines[i]) {
				continue
			}
			// Gather the whole call: continue until the parentheses opened by the call balance.
			call, depth := "", 0
			for j := i; j < len(lines) && j < i+12; j++ {
				call += lines[j] + "\n"
				depth += strings.Count(lines[j], "(") - strings.Count(lines[j], ")")
				if depth <= 0 {
					break
				}
			}
			for _, a := range allowed {
				call = strings.ReplaceAll(call, a, "")
			}
			for _, re := range bad {
				if re.MatchString(call) {
					t.Errorf("%s:%d logs raw error text or Detail; use safelog.Err / code and title:\n%s", slash, i+1, strings.TrimSpace(call))
					break
				}
			}
		}
		return nil
	})
}
