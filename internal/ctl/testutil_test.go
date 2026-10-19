package ctl

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Lamiv/zaks-drive/deploy"
)

// Synthetic sensitive values used across the tests. None may ever appear in output.
const (
	secClient = "SYNTH~client.Secret-VALUE-81f3a9"
	secAPI    = "SYNTH-API-KEY-c0ffee1234567890abcdef"
	secAdmin  = "SYNTH-ADMIN-TOKEN-0987654321fedcba"
	guidA     = "11111111-2222-3333-4444-555555555555"
	guidB     = "66666666-7777-8888-9999-aaaaaaaaaaaa"
)

type call struct {
	Name string
	Args []string
	Dir  string
	Env  []string
}

func (c call) String() string { return c.Name + " " + strings.Join(c.Args, " ") }

// fakeRunner records every process start and answers from a handler. Nothing is executed.
type fakeRunner struct {
	mu      sync.Mutex
	calls   []call
	handler func(c call) (Result, error)
}

func (f *fakeRunner) Run(_ context.Context, name string, args []string, opts RunOpts) (Result, error) {
	c := call{Name: name, Args: args, Dir: opts.Dir, Env: opts.Env}
	f.mu.Lock()
	f.calls = append(f.calls, c)
	f.mu.Unlock()
	res := Result{}
	var err error
	if f.handler != nil {
		res, err = f.handler(c)
	}
	// Like ExecRunner: when a writer is supplied the output goes there and the Result is empty.
	if opts.Stdout != nil {
		_, _ = opts.Stdout.Write([]byte(res.Stdout))
		res.Stdout = ""
	}
	if opts.Stderr != nil {
		_, _ = opts.Stderr.Write([]byte(res.Stderr))
		res.Stderr = ""
	}
	return res, err
}

func (f *fakeRunner) all() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		out = append(out, c.String())
	}
	return out
}

func (f *fakeRunner) has(substr string) bool {
	for _, c := range f.all() {
		if strings.Contains(c, substr) {
			return true
		}
	}
	return false
}

type env struct {
	app *App
	out *bytes.Buffer
	err *bytes.Buffer
	run *fakeRunner
	dir string
	// admin is the fake admin endpoint (health/readiness) of the deployment.
	admin       *httptest.Server
	readyStatus int
	readyBody   string
}

// healthyDocker answers the way a working Docker with one running container does.
func healthyDocker(containerState string) func(c call) (Result, error) {
	return func(c call) (Result, error) {
		line := c.String()
		switch {
		case strings.HasPrefix(line, "docker version"):
			return Result{Stdout: "27.1.0\n"}, nil
		case strings.HasPrefix(line, "docker compose version"):
			return Result{Stdout: "2.29.0\n"}, nil
		case strings.Contains(line, " ps --all --format json"):
			return Result{Stdout: `{"ID":"abc123def456","Name":"zd-adapter-1","Service":"adapter","State":"` + containerState + `","Health":"healthy","ExitCode":0,"Image":"zaks-drive:0.1.0","Publishers":[{"URL":"127.0.0.1","TargetPort":9090,"PublishedPort":9090,"Protocol":"tcp"},{"URL":"0.0.0.0","TargetPort":8080,"PublishedPort":8080,"Protocol":"tcp"}]}` + "\n"}, nil
		case strings.HasPrefix(line, "docker inspect"):
			return Result{Stdout: "abc123def456000000000000000000000000000000000000000000000000abcd 0\n"}, nil
		case strings.Contains(line, "config --images"):
			return Result{Stdout: "zaks-drive:0.1.0\n"}, nil
		case strings.HasPrefix(line, "docker image inspect"):
			return Result{Stdout: "sha256:x\n"}, nil
		}
		return Result{}, nil
	}
}

// newEnv creates a test App, a valid release-mode deployment in a temp dir and a fake admin
// endpoint whose port is written into the deployment's .env.
func newEnv(t *testing.T) *env {
	t.Helper()
	e := &env{out: &bytes.Buffer{}, err: &bytes.Buffer{}, run: &fakeRunner{handler: healthyDocker("running")}, dir: t.TempDir(), readyStatus: 200, readyBody: "ready"}
	e.admin = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/readyz":
			w.WriteHeader(e.readyStatus)
			_, _ = w.Write([]byte(e.readyBody))
		case "/version":
			_, _ = w.Write([]byte("0.1.0"))
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(e.admin.Close)
	u, _ := url.Parse(e.admin.URL)
	port, _ := strconv.Atoi(u.Port())

	ans := validAnswers(e.dir)
	ans.AdminPort = port
	envText, err := renderEnv(ans)
	if err != nil {
		t.Fatal(err)
	}
	must(t, os.WriteFile(filepath.Join(e.dir, ".env"), envText, 0o600))
	must(t, os.WriteFile(filepath.Join(e.dir, "config.yaml"), renderConfig(ans), 0o644))
	must(t, os.WriteFile(filepath.Join(e.dir, "docker-compose.yml"), deploy.ComposeRelease, 0o644))
	d := newDeployment(e.dir)
	must(t, d.saveState(State{Current: "0.1.0"}))

	e.app = &App{
		Out: e.out, Err: e.err, In: strings.NewReader(""),
		Runner: e.run, HTTP: &http.Client{Timeout: 3 * time.Second},
		Getenv:       func(string) string { return "" },
		Now:          time.Now,
		Version:      "0.1.0",
		GOOS:         "linux",
		GOARCH:       "amd64",
		Interactive:  func() bool { return false },
		ExePath:      func() (string, error) { return filepath.Join(t.TempDir(), "bin", "zaks-drive"), nil },
		PollInterval: 5 * time.Millisecond,
	}
	return e
}

func validAnswers(dir string) Answers {
	return Answers{
		Dir: dir, Bind: "0.0.0.0", RESTPort: 8080, CSPort: 8090, AdminPort: 9090,
		Interfaces: []string{"rest", "contentrepo"},
		SiteURL:    "https://contoso.sharepoint.com/sites/SAP_DOCS", Library: "Documents", RootPath: "SAP_DMS",
		TenantID: guidA, ClientID: guidB, ClientSecret: secClient,
		ContReps: []ContRepAnswer{{"Z1", "required"}}, LogLevel: "info",
		APIKeyS4: secAPI, APIKeyFiori: secAPI + "-fiori", AdminToken: secAdmin,
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}

func (e *env) main(args ...string) int {
	e.out.Reset()
	e.err.Reset()
	return e.app.Main(args)
}

func (e *env) both() string { return e.out.String() + e.err.String() }
