package ctl

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

type member struct {
	name     string
	data     string
	typeflag byte
	link     string
}

// buildArchive creates a release archive. top is the top-level directory name.
func buildArchive(t *testing.T, members []member) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	for _, m := range members {
		tf := m.typeflag
		if tf == 0 {
			tf = tar.TypeReg
		}
		h := &tar.Header{Name: m.name, Mode: 0o755, Typeflag: tf, Linkname: m.link}
		if tf == tar.TypeReg {
			h.Size = int64(len(m.data))
		}
		must(t, tw.WriteHeader(h))
		if tf == tar.TypeReg {
			_, err := tw.Write([]byte(m.data))
			must(t, err)
		}
	}
	must(t, tw.Close())
	must(t, gz.Close())
	return buf.Bytes()
}

func goodMembers(version string) []member {
	top := "zaks-drive_" + version + "_linux_amd64"
	return []member{
		{name: top + "/", typeflag: tar.TypeDir},
		{name: top + "/zaks-drive", data: "cli-bin-" + version},
		{name: top + "/zaks-drive-server", data: "server-bin-" + version},
		{name: top + "/deploy/", typeflag: tar.TypeDir},
		{name: top + "/deploy/Dockerfile.release", data: "FROM scratch\n"},
		{name: top + "/deploy/docker-compose.release.yml", data: "services: {}\n"},
		{name: top + "/spool/", typeflag: tar.TypeDir},
		{name: top + "/spool/.keep", data: ""},
		{name: top + "/release.json", data: fmt.Sprintf(`{"version":"%s","commit":"0123456789abcdef0123456789abcdef01234567"}`, version)},
		{name: top + "/VERSION", data: version + "\n"},
		{name: top + "/LICENSE", data: "MIT"},
	}
}

// releaseServer serves /download/v<ver>/SHA256SUMS and the archive for each version, and
// /latest as a redirect to the newest tag.
type releaseServer struct {
	srv     *httptest.Server
	files   map[string][]byte
	latest  string
	badSums map[string]string // version -> replacement checksum text
}

func newReleaseServer(t *testing.T) *releaseServer {
	rs := &releaseServer{files: map[string][]byte{}, badSums: map[string]string{}}
	rs.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/latest" {
			if rs.latest == "" {
				http.NotFound(w, r)
				return
			}
			http.Redirect(w, r, "/tag/v"+rs.latest, http.StatusFound)
			return
		}
		if strings.HasPrefix(r.URL.Path, "/tag/") {
			w.WriteHeader(200)
			return
		}
		b, ok := rs.files[r.URL.Path]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(b)
	}))
	t.Cleanup(rs.srv.Close)
	return rs
}

func (rs *releaseServer) publish(t *testing.T, version string, members []member) {
	t.Helper()
	arc := buildArchive(t, members)
	name := "zaks-drive_" + version + "_linux_amd64.tar.gz"
	sum := sha256.Sum256(arc)
	rs.files["/download/v"+version+"/"+name] = arc
	rs.files["/download/v"+version+"/SHA256SUMS"] = []byte(hex.EncodeToString(sum[:]) + "  " + name + "\n")
}

type updEnv struct {
	*env
	rel   *releaseServer
	share string
	// healthy maps a version to whether the fake server becomes ready when that version starts.
	healthy map[string]bool
	// images is the set of images that exist locally.
	images map[string]bool
	// started records the version of every `compose up`.
	started []string
}

func newUpdEnv(t *testing.T) *updEnv {
	t.Helper()
	e := newEnv(t)
	u := &updEnv{env: e, rel: newReleaseServer(t), share: t.TempDir(), healthy: map[string]bool{"0.1.0": true}, images: map[string]bool{"zaks-drive:0.1.0": true}}
	e.app.ReleaseBase = u.rel.srv.URL
	e.app.Getenv = func(k string) string {
		if k == "ZAKS_DRIVE_SHARE" {
			return u.share
		}
		return ""
	}
	base := e.fake.handler()
	e.run.handler = func(c call) (Result, error) {
		line := c.String()
		switch {
		case strings.HasPrefix(line, "docker build"):
			// The tag is the argument after -t.
			for i, a := range c.Args {
				if a == "-t" && i+1 < len(c.Args) {
					u.images[c.Args[i+1]] = true
				}
			}
			return Result{}, nil
		case strings.HasPrefix(line, "docker image inspect"):
			ref := c.Args[len(c.Args)-1]
			if u.images[ref] {
				return Result{Stdout: "sha256:abcdef\n"}, nil
			}
			return Result{ExitCode: 1, Stderr: "No such image"}, nil
		case strings.Contains(line, " up -d"):
			v := ""
			for _, kv := range c.Env {
				if strings.HasPrefix(kv, "ZAKS_DRIVE_VERSION=") {
					v = strings.TrimPrefix(kv, "ZAKS_DRIVE_VERSION=")
				}
			}
			u.started = append(u.started, v)
			if ok, known := u.healthy[v]; known && !ok {
				e.readyStatus, e.readyBody = 503, "not ready: SP_ACCESS_DENIED from version "+v
			} else {
				e.readyStatus, e.readyBody = 200, "ready"
			}
			return Result{}, nil
		}
		return base(c)
	}
	return u
}

func (u *updEnv) update(args ...string) int {
	return u.main(append([]string{"update", "--dir", u.dir, "--timeout", "2s"}, args...)...)
}

func (u *updEnv) state() State { return newDeployment(u.dir).loadState() }

func (u *updEnv) hash(name string) string {
	b, err := os.ReadFile(filepath.Join(u.dir, name))
	if err != nil {
		return "missing"
	}
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

func (u *updEnv) noDockerChanges(t *testing.T) {
	t.Helper()
	for _, c := range u.run.all() {
		if strings.HasPrefix(c, "docker build") || strings.Contains(c, " up -d") || strings.Contains(c, " restart") {
			t.Errorf("nothing may be built or restarted: %s", c)
		}
	}
}

func TestUpdateCheckReportsAndChangesNothing(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.latest = "0.1.1"
	if rc := u.main("update", "--dir", u.dir, "--check"); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	for _, w := range []string{"Installed version   0.1.0", "Target version      0.1.1", "Nothing was changed"} {
		if !strings.Contains(u.out.String(), w) {
			t.Errorf("lacks %q:\n%s", w, u.out.String())
		}
	}
	u.noDockerChanges(t)
	if len(u.run.calls) != 0 {
		t.Errorf("--check must not even call Docker: %v", u.run.all())
	}
	if len(u.rel.files) != 0 && u.state().Current != "0.1.0" {
		t.Error("state must not change")
	}
	// A breaking move is announced.
	if rc := u.main("update", "--dir", u.dir, "--check", "--version", "0.2.0"); rc != ExitOK || !strings.Contains(u.out.String(), "compatibility line") {
		t.Errorf("exit %d\n%s", rc, u.out.String())
	}
	// Up to date.
	if rc := u.main("update", "--dir", u.dir, "--check", "--version", "0.1.0"); rc != ExitOK || !strings.Contains(u.out.String(), "already on 0.1.0") {
		t.Errorf("exit %d\n%s", rc, u.out.String())
	}
}

func TestUpdateHappyPath(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.publish(t, "0.1.1", goodMembers("0.1.1"))
	u.healthy["0.1.1"] = true
	envBefore, cfgBefore := u.hash(".env"), u.hash("config.yaml")
	if rc := u.update("--version", "0.1.1", "--yes"); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	for _, w := range []string{"Checksum OK", "Building the image zaks-drive:0.1.1", "ZAKS Drive 0.1.1 is ready", "zaks-drive rollback"} {
		if !strings.Contains(u.out.String(), w) {
			t.Errorf("lacks %q:\n%s", w, u.out.String())
		}
	}
	// The release was unpacked and the image built from it, with the revision recorded.
	if _, err := os.Stat(filepath.Join(u.share, "0.1.1", "zaks-drive-server")); err != nil {
		t.Error("the release payload must be installed")
	}
	want := "docker build -f " + filepath.Join(u.share, "0.1.1", "deploy", "Dockerfile.release") + " --build-arg VERSION=0.1.1 -t zaks-drive:0.1.1 --build-arg REVISION=0123456789abcdef0123456789abcdef01234567 " + filepath.Join(u.share, "0.1.1")
	if !u.run.has(want) {
		t.Errorf("missing %q in %v", want, u.run.all())
	}
	if len(u.started) != 1 || u.started[0] != "0.1.1" {
		t.Errorf("the new image must be started: %v", u.started)
	}
	st := u.state()
	if st.Current != "0.1.1" || st.Previous != "0.1.0" {
		t.Errorf("state %+v", st)
	}
	if u.hash(".env") != envBefore || u.hash("config.yaml") != cfgBefore {
		t.Error("configuration must be untouched by an update")
	}
	for _, c := range u.run.all() {
		for _, bad := range []string{" down", " rm ", " -v", "--volumes", "prune", "volume ", "image rm", "rmi"} {
			if strings.Contains(c, bad) {
				t.Errorf("an update must not remove anything: %s", c)
			}
		}
	}
}

func TestUpdateToLatest(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.latest = "0.1.2"
	u.rel.publish(t, "0.1.2", goodMembers("0.1.2"))
	u.healthy["0.1.2"] = true
	if rc := u.update("--yes"); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	if u.state().Current != "0.1.2" {
		t.Errorf("%+v", u.state())
	}
	// No release at all: 'latest' explains the pre-release rule.
	u2 := newUpdEnv(t)
	if rc := u2.update("--yes"); rc != ExitFail || !strings.Contains(u2.err.String(), "pre-release") {
		t.Errorf("exit %d\n%s", rc, u2.err.String())
	}
}

func TestUpdateNeedsConfirmation(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.publish(t, "0.1.1", goodMembers("0.1.1"))
	if rc := u.update("--version", "0.1.1"); rc != ExitDeclined {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	if !strings.Contains(u.err.String(), "--yes") {
		t.Errorf("%s", u.err.String())
	}
	u.noDockerChanges(t)
	if len(u.rel.files) == 0 {
		t.Fatal("test setup")
	}
	if u.state().Current != "0.1.0" {
		t.Error("state must not change without confirmation")
	}
}

func TestUpdateRejectsChecksumFailuresBeforeUsingAnything(t *testing.T) {
	cases := map[string]func(u *updEnv){
		"tampered archive": func(u *updEnv) {
			k := "/download/v0.1.1/zaks-drive_0.1.1_linux_amd64.tar.gz"
			u.rel.files[k] = append(append([]byte{}, u.rel.files[k]...), 'x')
		},
		"missing entry": func(u *updEnv) {
			u.rel.files["/download/v0.1.1/SHA256SUMS"] = []byte(strings.Repeat("a", 64) + "  some-other-file.tar.gz\n")
		},
		"malformed entry": func(u *updEnv) {
			u.rel.files["/download/v0.1.1/SHA256SUMS"] = []byte("not-a-hash  zaks-drive_0.1.1_linux_amd64.tar.gz\n")
		},
		"wrong checksum": func(u *updEnv) {
			u.rel.files["/download/v0.1.1/SHA256SUMS"] = []byte(strings.Repeat("0", 64) + "  zaks-drive_0.1.1_linux_amd64.tar.gz\n")
		},
	}
	for name, tamper := range cases {
		t.Run(name, func(t *testing.T) {
			u := newUpdEnv(t)
			u.rel.publish(t, "0.1.1", goodMembers("0.1.1"))
			tamper(u)
			if rc := u.update("--version", "0.1.1", "--yes"); rc != ExitVerify {
				t.Fatalf("exit %d, want %d\n%s", rc, ExitVerify, u.both())
			}
			u.noDockerChanges(t)
			if _, err := os.Stat(filepath.Join(u.share, "0.1.1")); err == nil {
				t.Error("an unverified release must not be unpacked into the release directory")
			}
			if u.state().Current != "0.1.0" || u.state().Previous != "" {
				t.Errorf("state must not change: %+v", u.state())
			}
			if !strings.Contains(u.err.String(), "nothing was installed") && !strings.Contains(u.err.String(), "no change was made") {
				t.Errorf("the message must say nothing changed: %s", u.err.String())
			}
		})
	}
}

func TestUpdateRejectsHostileArchivesEvenWithAValidChecksum(t *testing.T) {
	top := "zaks-drive_0.1.1_linux_amd64"
	cases := map[string][]member{
		"parent traversal":  append(goodMembers("0.1.1"), member{name: top + "/../evil.txt", data: "x"}),
		"absolute path":     append(goodMembers("0.1.1"), member{name: "/etc/evil.txt", data: "x"}),
		"outside top dir":   append(goodMembers("0.1.1"), member{name: "other/evil.txt", data: "x"}),
		"symlink":           append(goodMembers("0.1.1"), member{name: top + "/deploy/link", typeflag: tar.TypeSymlink, link: "/etc/passwd"}),
		"hardlink":          append(goodMembers("0.1.1"), member{name: top + "/deploy/hl", typeflag: tar.TypeLink, link: top + "/zaks-drive"}),
		"unexpected file":   append(goodMembers("0.1.1"), member{name: top + "/surprise.sh", data: "#!/bin/sh"}),
		"backslash path":    append(goodMembers("0.1.1"), member{name: top + `\..\evil`, data: "x"}),
		"unexpected folder": append(goodMembers("0.1.1"), member{name: top + "/extra/", typeflag: tar.TypeDir}),
	}
	for name, members := range cases {
		t.Run(name, func(t *testing.T) {
			u := newUpdEnv(t)
			u.rel.publish(t, "0.1.1", members) // the checksum matches the hostile archive
			if rc := u.update("--version", "0.1.1", "--yes"); rc != ExitVerify {
				t.Fatalf("exit %d\n%s", rc, u.both())
			}
			u.noDockerChanges(t)
			if _, err := os.Stat(filepath.Join(u.share, "0.1.1")); err == nil {
				t.Error("nothing may be installed")
			}
			entries, _ := os.ReadDir(u.share)
			for _, en := range entries {
				t.Errorf("the release directory must be left clean, found %s", en.Name())
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(u.share), "evil.txt")); err == nil {
				t.Error("a file was written outside the release directory")
			}
		})
	}
}

func TestUpdateRejectsAnArchiveThatIsNotTheRequestedVersion(t *testing.T) {
	u := newUpdEnv(t)
	members := goodMembers("0.1.1")
	for i := range members {
		if strings.HasSuffix(members[i].name, "/release.json") {
			members[i].data = `{"version":"9.9.9"}`
		}
	}
	u.rel.publish(t, "0.1.1", members)
	if rc := u.update("--version", "0.1.1", "--yes"); rc != ExitVerify {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	u.noDockerChanges(t)
}

func TestFailedUpdateRollsBackAutomatically(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.publish(t, "0.1.1", goodMembers("0.1.1"))
	u.healthy["0.1.1"] = false // the new version never becomes ready
	envBefore := u.hash(".env")
	rc := u.update("--version", "0.1.1", "--yes")
	if rc != ExitFail {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	if strings.Join(u.started, ",") != "0.1.1,0.1.0" {
		t.Errorf("the new version, then the previous one, must be started: %v", u.started)
	}
	st := u.state()
	if st.Current != "0.1.0" {
		t.Errorf("the state must point at the working version again: %+v", st)
	}
	for _, w := range []string{"did not become ready", "SP_ACCESS_DENIED", "Going back to 0.1.0", "running 0.1.0 again", "configuration and data were not changed"} {
		if !strings.Contains(u.both(), w) {
			t.Errorf("lacks %q:\n%s", w, u.both())
		}
	}
	if u.hash(".env") != envBefore {
		t.Error(".env must be untouched")
	}
}

func TestFailedUpdateAndFailedRollbackIsReported(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.publish(t, "0.1.1", goodMembers("0.1.1"))
	u.healthy["0.1.1"] = false
	u.healthy["0.1.0"] = false
	if rc := u.update("--version", "0.1.1", "--yes"); rc != ExitUnhealthy {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	if !strings.Contains(u.err.String(), "not ready either") {
		t.Errorf("%s", u.err.String())
	}
}

func TestFailedUpdateWithoutThePreviousImageExplainsTheManualWay(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.publish(t, "0.1.1", goodMembers("0.1.1"))
	u.healthy["0.1.1"] = false
	delete(u.images, "zaks-drive:0.1.0")
	if rc := u.update("--version", "0.1.1", "--yes"); rc != ExitUnhealthy {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	if !strings.Contains(u.err.String(), "no longer exists locally") || !strings.Contains(u.err.String(), "update --version 0.1.0") {
		t.Errorf("%s", u.err.String())
	}
}

func TestBuildFailureChangesNothing(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.publish(t, "0.1.1", goodMembers("0.1.1"))
	inner := u.run.handler
	u.run.handler = func(c call) (Result, error) {
		if strings.HasPrefix(c.String(), "docker build") {
			return Result{ExitCode: 1, Stderr: "pull access denied for gcr.io/distroless/static-debian12"}, nil
		}
		return inner(c)
	}
	if rc := u.update("--version", "0.1.1", "--yes"); rc != ExitFail {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	if len(u.started) != 0 || u.state().Current != "0.1.0" {
		t.Errorf("started=%v state=%+v", u.started, u.state())
	}
	if !strings.Contains(u.err.String(), "untouched") {
		t.Errorf("%s", u.err.String())
	}
}

func TestBreakingChangesNeedExplicitApproval(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.publish(t, "0.2.0", goodMembers("0.2.0"))
	u.healthy["0.2.0"] = true
	// --yes alone is not enough for a move to another compatibility line.
	if rc := u.update("--version", "0.2.0", "--yes"); rc != ExitDeclined {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	if !strings.Contains(u.err.String(), "--allow-breaking") {
		t.Errorf("%s", u.err.String())
	}
	u.noDockerChanges(t)
	if rc := u.update("--version", "0.2.0", "--yes", "--allow-breaking"); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	if u.state().Current != "0.2.0" {
		t.Errorf("%+v", u.state())
	}
}

func TestBreakingChangeCanBeConfirmedInteractively(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.publish(t, "0.2.0", goodMembers("0.2.0"))
	u.healthy["0.2.0"] = true
	u.app.Interactive = func() bool { return true }
	u.app.In = strings.NewReader("y\ny\n") // breaking prompt, then the update prompt
	if rc := u.update("--version", "0.2.0"); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	u2 := newUpdEnv(t)
	u2.rel.publish(t, "0.2.0", goodMembers("0.2.0"))
	u2.app.Interactive = func() bool { return true }
	u2.app.In = strings.NewReader("n\n")
	if rc := u2.update("--version", "0.2.0"); rc != ExitDeclined {
		t.Fatalf("declining must stop: exit %d", rc)
	}
	u2.noDockerChanges(t)
}

func TestDowngradeNeedsApproval(t *testing.T) {
	u := newUpdEnv(t)
	must(t, newDeployment(u.dir).saveState(State{Current: "0.1.5"}))
	u.rel.publish(t, "0.1.4", goodMembers("0.1.4"))
	if rc := u.update("--version", "0.1.4", "--yes"); rc != ExitDeclined || !strings.Contains(u.out.String(), "DOWNGRADE") {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
}

func TestUpdateInputValidation(t *testing.T) {
	u := newUpdEnv(t)
	for _, v := range []string{"1.0;rm -rf /", "../../x", "latest", "1.2"} {
		if rc := u.update("--version", v, "--yes"); rc != ExitUsage {
			t.Errorf("--version %q: exit %d, want %d", v, rc, ExitUsage)
		}
	}
	for _, base := range []string{"http://example.com/releases", "ftp://x/y", "file:///tmp", "notaurl"} {
		if rc := u.update("--base-url", base, "--check"); rc != ExitUsage {
			t.Errorf("--base-url %q: exit %d, want %d", base, rc, ExitUsage)
		}
	}
	u.app.GOARCH = "386"
	u.rel.publish(t, "0.1.1", goodMembers("0.1.1"))
	if rc := u.update("--version", "0.1.1", "--yes"); rc == ExitOK {
		t.Error("an unsupported architecture must fail")
	}
}

func TestValidReleaseBase(t *testing.T) {
	for in, ok := range map[string]bool{
		"https://github.com/Lamiv/zaks-drive/releases": true, "https://mirror.example.org/x": true,
		"http://127.0.0.1:8080": true, "http://localhost:9000/r": true, "http://[::1]:80/x": true,
		"http://example.com": false, "http://10.0.0.5/x": false, "http://127.0.0.1.evil.com": false, "": false, "ftp://x": false,
	} {
		if err := validReleaseBase(in); (err == nil) != ok {
			t.Errorf("validReleaseBase(%q) = %v, want ok=%v", in, err, ok)
		}
	}
}

func TestUpdateRefusesSourceCheckouts(t *testing.T) {
	u := newUpdEnv(t)
	must(t, os.WriteFile(filepath.Join(u.dir, "docker-compose.yml"), []byte("services:\n  adapter:\n    build:\n      context: ..\n"), 0o644))
	if rc := u.update("--version", "0.1.1", "--yes"); rc != ExitFail || !strings.Contains(u.err.String(), "git") {
		t.Fatalf("exit %d\n%s", rc, u.err.String())
	}
}

// ---- rollback ----

func TestRollback(t *testing.T) {
	u := newUpdEnv(t)
	u.rel.publish(t, "0.1.1", goodMembers("0.1.1"))
	u.healthy["0.1.1"] = true
	if rc := u.update("--version", "0.1.1", "--yes"); rc != ExitOK {
		t.Fatalf("setup: %d\n%s", rc, u.both())
	}
	u.started = nil
	if rc := u.main("rollback", "--dir", u.dir, "--timeout", "2s"); rc != ExitDeclined {
		t.Fatalf("a rollback needs confirmation: exit %d", rc)
	}
	if rc := u.main("rollback", "--dir", u.dir, "--timeout", "2s", "--yes"); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	if strings.Join(u.started, ",") != "0.1.0" {
		t.Errorf("%v", u.started)
	}
	st := u.state()
	if st.Current != "0.1.0" || st.Previous != "0.1.1" {
		t.Errorf("rollback must be reversible: %+v", st)
	}
	if !strings.Contains(u.out.String(), "rolled back from 0.1.1") {
		t.Errorf("%s", u.out.String())
	}
}

func TestRollbackPreconditions(t *testing.T) {
	u := newUpdEnv(t)
	if rc := u.main("rollback", "--dir", u.dir, "--yes"); rc != ExitFail || !strings.Contains(u.err.String(), "no previous version") {
		t.Fatalf("exit %d\n%s", rc, u.err.String())
	}
	must(t, newDeployment(u.dir).saveState(State{Current: "0.1.1", Previous: "0.1.0"}))
	delete(u.images, "zaks-drive:0.1.0")
	if rc := u.main("rollback", "--dir", u.dir, "--yes"); rc != ExitFail || !strings.Contains(u.err.String(), "update --version 0.1.0") {
		t.Fatalf("exit %d\n%s", rc, u.err.String())
	}
	u.noDockerChanges(t)
	// A corrupted state file must not reach a docker argument.
	must(t, newDeployment(u.dir).saveState(State{Current: "0.1.1", Previous: "0.1.0 --privileged"}))
	if rc := u.main("rollback", "--dir", u.dir, "--yes"); rc != ExitFail {
		t.Fatalf("exit %d", rc)
	}
	if u.run.has("--privileged") {
		t.Error("an invalid version in the state file must not be passed to Docker")
	}
}

func TestRollbackThatFailsReturnsToTheVersionItLeft(t *testing.T) {
	u := newUpdEnv(t)
	must(t, newDeployment(u.dir).saveState(State{Current: "0.1.1", Previous: "0.1.0"}))
	u.images["zaks-drive:0.1.1"] = true
	u.healthy["0.1.0"] = false
	u.healthy["0.1.1"] = true
	if rc := u.main("rollback", "--dir", u.dir, "--yes", "--timeout", "2s"); rc != ExitFail {
		t.Fatalf("exit %d\n%s", rc, u.both())
	}
	if u.state().Current != "0.1.1" || !strings.Contains(u.err.String(), "running 0.1.1 again") {
		t.Errorf("state %+v\n%s", u.state(), u.err.String())
	}
}

// ---- helpers ----

func TestExtractReleaseUnpacksOnlyWhatItShould(t *testing.T) {
	arc := filepath.Join(t.TempDir(), "r.tar.gz")
	must(t, os.WriteFile(arc, buildArchive(t, goodMembers("0.1.1")), 0o600))
	dest := filepath.Join(t.TempDir(), "out")
	must(t, extractRelease(arc, "zaks-drive_0.1.1_linux_amd64", dest))
	for _, f := range []string{"zaks-drive", "zaks-drive-server", "release.json", "VERSION", "deploy/Dockerfile.release", "spool/.keep"} {
		if _, err := os.Stat(filepath.Join(dest, filepath.FromSlash(f))); err != nil {
			t.Errorf("missing %s", f)
		}
	}
	must(t, checkExtracted(dest, "0.1.1"))
	if err := checkExtracted(dest, "0.1.2"); err == nil {
		t.Error("a different version must not pass")
	}
}

func TestParseSums(t *testing.T) {
	h := strings.Repeat("ab", 32)
	m := parseSums([]byte(h + "  a.tar.gz\n" + strings.ToUpper(h) + " *b.tar.gz\nbad line\n" + strings.Repeat("z", 64) + "  c\n"))
	if m["a.tar.gz"] != h || m["b.tar.gz"] != h || len(m) != 2 {
		t.Errorf("%v", m)
	}
}
