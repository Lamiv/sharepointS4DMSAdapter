package ctl

import (
	"context"
	"flag"
	"fmt"
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"time"
)

// freeBytes returns the free space for path (replaceable in tests through DiskFree).
func (a *App) freeBytes(path string) (int64, error) {
	if a.DiskFree != nil {
		return a.DiskFree(path)
	}
	return diskFree(path)
}

// httpDo performs a GET against a local endpoint and returns status, a bounded body and headers.
func (a *App) httpDo(ctx context.Context, url string, headers map[string]string) (int, string, http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return 0, "", nil, err
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := a.HTTP.Do(req)
	if err != nil {
		return 0, "", nil, err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	return resp.StatusCode, strings.TrimSpace(string(b)), resp.Header, nil
}

// execServerCheck runs the server's own `check` inside the container. The server binary is named
// zaks-drive-server; older images only have /zaks-drive, so that is tried when the first is absent.
// The output goes to out when given (streaming), otherwise it is captured.
func (a *App) execServerCheck(ctx context.Context, d *Deployment, out io.Writer) (Result, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	var res Result
	var err error
	for _, bin := range []string{"/zaks-drive-server", "/zaks-drive"} {
		opts := RunOpts{}
		if out != nil {
			opts.Stdout, opts.Stderr = out, out
		}
		res, err = a.compose(ctx, d, opts, "exec", "-T", "adapter", bin, "check")
		if err != nil {
			return res, err
		}
		missing := res.ExitCode == 126 || res.ExitCode == 127 ||
			strings.Contains(strings.ToLower(res.Stderr), "executable file not found") ||
			strings.Contains(strings.ToLower(res.Stderr), "no such file or directory")
		if !missing {
			return res, nil
		}
	}
	return res, fmt.Errorf("the check could not be started inside the container: %s", oneLine(trimLines(res.Stderr, 2)))
}

func (a *App) cmdDoctor(ctx context.Context, args []string) int {
	fset := flag.NewFlagSet("doctor", flag.ContinueOnError)
	fset.SetOutput(a.Err)
	dir := fset.String("dir", "", "deployment directory")
	asJSON := fset.Bool("json", false, "machine-readable output")
	conn := fset.Bool("connectivity", false, "also test Microsoft access (contacts Microsoft, writes small test files)")
	strict := fset.Bool("strict", false, "exit 1 on warnings too")
	list := fset.Bool("list", false, "list every diagnostic and how it works")
	if err := fset.Parse(args); err != nil {
		return ExitUsage
	}
	if *list {
		for _, di := range Diagnostics {
			a.printf("%-20s %s\n    how:  %s\n", di.ID, di.Title, di.How)
			if di.Fail != "" {
				a.printf("    FAIL: %s\n", di.Fail)
			}
			if di.Warn != "" {
				a.printf("    WARN: %s\n", di.Warn)
			}
			if di.Remote {
				a.printf("    contacts Microsoft: yes (only with --connectivity)\n")
			}
		}
		return ExitOK
	}
	d, err := a.locate(*dir)
	if err != nil {
		// doctor still reports platform and Docker problems when there is no deployment yet.
		target := *dir
		if target == "" {
			target = defaultDir
		}
		abs, _ := filepath.Abs(target)
		d = newDeployment(abs)
	}
	rep := a.runDoctor(ctx, d, DoctorOpts{Connectivity: *conn})
	if *asJSON {
		b, _ := marshalJSON(rep)
		a.printf("%s\n", b)
	} else {
		a.printf("ZAKS Drive doctor (command %s), deployment %s\n\n", rep.CLIVersion, rep.Deployment)
		for _, f := range rep.Findings {
			a.printf("[%-7s] %-19s %s\n", f.Status, f.ID, f.Detail)
			if f.Fix != "" && f.Status != StatusPass {
				a.printf("%31s fix: %s\n", "", f.Fix)
			}
		}
		a.printf("\nSummary: %d passed, %d warnings, %d failed, %d skipped\n", rep.Summary[StatusPass], rep.Summary[StatusWarn], rep.Summary[StatusFail], rep.Summary[StatusSkipped])
		a.printf("Not covered: the SAP side. Local checks cannot see inside SAP. In SAP use OAC0 (connection test),\nSTRUST (compare the certificate fingerprint) and the reports RSCMSTH0 to RSCMSTH3.\n")
		if !*conn {
			a.printf("Microsoft access was not tested (add --connectivity; it contacts Microsoft and writes small test files).\n")
		}
	}
	if rep.Summary[StatusFail] > 0 || (*strict && rep.Summary[StatusWarn] > 0) {
		return ExitFail
	}
	return ExitOK
}

func (a *App) cmdCheck(ctx context.Context, args []string) int {
	fset := flag.NewFlagSet("check", flag.ContinueOnError)
	fset.SetOutput(a.Err)
	dir := fset.String("dir", "", "deployment directory")
	if err := fset.Parse(args); err != nil {
		return ExitUsage
	}
	d, code := a.prepare(ctx, *dir)
	if code != ExitOK {
		return code
	}
	cs, err := a.managed(ctx, d)
	if err != nil {
		return a.fail(ExitFail, err.Error(), "run: zaks-drive doctor")
	}
	running := false
	for _, c := range cs {
		running = running || c.State == "running"
	}
	if !running {
		return a.fail(ExitUnhealthy, "ZAKS Drive is not running, so its SharePoint access cannot be tested", "run: zaks-drive start")
	}
	a.printf("This contacts Microsoft Entra ID and Microsoft Graph with the application's credentials and creates and deletes\nsmall test files in the configured SharePoint folder. Credentials are never printed.\n\n")
	san := a.sanitizerFor(d)
	w := &lineWriter{fn: func(l string) { fmt.Fprintln(a.Out, san.Line(l)) }}
	res, err := a.execServerCheck(ctx, d, w)
	w.Flush()
	if err != nil {
		return a.fail(ExitFail, err.Error(), "run: zaks-drive doctor")
	}
	if res.ExitCode != 0 {
		return ExitFail
	}
	return ExitOK
}
