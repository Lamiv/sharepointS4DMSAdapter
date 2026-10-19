package ctl

import (
	"archive/tar"
	"compress/gzip"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

func readBundle(t *testing.T, path string) map[string]string {
	t.Helper()
	f, err := os.Open(path)
	must(t, err)
	defer f.Close()
	gz, err := gzip.NewReader(f)
	must(t, err)
	tr := tar.NewReader(gz)
	out := map[string]string{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		must(t, err)
		b, err := io.ReadAll(tr)
		must(t, err)
		out[h.Name] = string(b)
	}
	return out
}

// bundleEnv is a deployment that is full of things that must never reach a bundle.
func bundleEnv(t *testing.T) *env {
	e := newEnv(t)
	base := e.run.handler
	e.run.handler = func(c call) (Result, error) {
		if strings.Contains(c.String(), " logs ") {
			return Result{Stdout: sampleLogs()}, nil
		}
		return base(c)
	}
	// .env holds an extra value that is not a secret by name, but must not be copied either.
	envPath := filepath.Join(e.dir, ".env")
	must(t, os.WriteFile(envPath, append(mustRead(t, envPath), []byte("\nZZ_ENV_ONLY_MARKER=envmarker123\n")...), 0o600))
	// config.yaml holds a plaintext key (instead of ${VAR}): redaction by value cannot know it,
	// so only the allowlist keeps it out.
	cfgPath := filepath.Join(e.dir, "config.yaml")
	cfg := strings.Replace(string(mustRead(t, cfgPath)), "${ADAPTER_API_KEY_S4}", "PLAINTEXT-IN-CONFIG-77", 1)
	must(t, os.WriteFile(cfgPath, []byte(cfg), 0o644))
	// The server's readiness text itself contains a credential-shaped value.
	e.readyStatus, e.readyBody = 503, "not ready: client_secret="+secClient
	return e
}

func TestSupportBundleNeedsConfirmationAndShowsThePlan(t *testing.T) {
	e := bundleEnv(t)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if rc := e.main("support", "bundle", "--dir", e.dir, "--output", out); rc != ExitDeclined {
		t.Fatalf("exit %d\n%s", rc, e.both())
	}
	for _, w := range []string{"The support bundle will contain", "versions.json", "logs.txt", "will NOT contain .env", "operational identifiers", "Nothing was collected"} {
		if !strings.Contains(e.both(), w) {
			t.Errorf("lacks %q:\n%s", w, e.both())
		}
	}
	if _, err := os.Stat(out); err == nil {
		t.Fatal("no archive may be written without confirmation")
	}
	if e.run.has(" logs ") || e.run.has("inspect") {
		t.Errorf("nothing may be collected before the user confirms: %v", e.run.all())
	}
}

func TestSupportBundleNeverContainsSecretsOrUnlistedData(t *testing.T) {
	e := bundleEnv(t)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if rc := e.main("support", "bundle", "--dir", e.dir, "--output", out, "--yes"); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, e.both())
	}
	files := readBundle(t, out)
	var names []string
	for n := range files {
		names = append(names, n)
		if strings.ContainsAny(n, `/\`) || strings.Contains(n, "..") {
			t.Errorf("unexpected path in archive: %q", n)
		}
	}
	sort.Strings(names)
	want := "README.txt,config-summary.json,doctor.json,logs.txt,status.json,system.json,versions.json"
	if strings.Join(names, ",") != want {
		t.Fatalf("archive contents %v, want %s", names, want)
	}
	forbidden := []string{
		secClient, "SYNTH~client", secAPI, secAdmin, "PLAINTEXT-IN-CONFIG-77", "envmarker123", "ZZ_ENV_ONLY_MARKER",
		"SYNTH-SECKEY-123456", "SYNTH-TEMPAUTH-999",
		guidA, guidB, "contoso", "SAP_DOCS", // tenant and client IDs and SharePoint names are not in the allowlist
		e.dir, // the local path
	}
	// logs.txt is free text: credentials are masked, but operational identifiers (here a SharePoint
	// host inside a log line) stay, which is why the user is warned. The generated files are
	// allowlisted and must not contain them at all.
	logOnlyOK := map[string]bool{"contoso": true, "SAP_DOCS": true}
	for name, content := range files {
		for _, f := range forbidden {
			if name == "logs.txt" && logOnlyOK[f] {
				continue
			}
			if strings.Contains(content, f) {
				t.Errorf("%s contains %q", name, f)
			}
		}
	}
	// What is allowlisted is there and useful.
	var sum ConfigSummary
	must(t, json.Unmarshal([]byte(files["config-summary.json"]), &sum))
	if len(sum.Interfaces) != 2 || len(sum.ContentServer.Repositories) != 1 || sum.ContentServer.Repositories[0].Signature != "required" ||
		len(sum.Repositories) != 1 || sum.Repositories[0].LocationType != "siteUrl" || !sum.GraphConfigured || sum.Auth["apiKeyCount"] != float64(2) {
		t.Errorf("config summary: %+v", sum)
	}
	if !strings.Contains(files["logs.txt"], "r-cs-1") || !strings.Contains(files["logs.txt"], "CS_CERT_INACTIVE") {
		t.Errorf("logs keep correlation IDs and codes:\n%s", files["logs.txt"])
	}
	if !strings.Contains(files["versions.json"], `"cli": "0.1.0"`) || !strings.Contains(files["system.json"], `"docker": "27.1.0"`) {
		t.Errorf("versions: %s system: %s", files["versions.json"], files["system.json"])
	}
	if !strings.Contains(files["README.txt"], "does NOT contain") {
		t.Error("README must state the exclusions")
	}
	if runtime.GOOS != "windows" {
		if st, _ := os.Stat(out); st.Mode().Perm() != 0o600 {
			t.Errorf("archive mode %v", st.Mode().Perm())
		}
	}
	// Output on the terminal carries no secret either.
	for _, f := range []string{secClient, secAPI, secAdmin} {
		if strings.Contains(e.both(), f) {
			t.Errorf("terminal output contains %q", f)
		}
	}
}

func TestSupportBundleUsesOnlyHarmlessDockerQueries(t *testing.T) {
	e := bundleEnv(t)
	out := filepath.Join(t.TempDir(), "b.tar.gz")
	if rc := e.main("support", "bundle", "--dir", e.dir, "--output", out, "--yes"); rc != ExitOK {
		t.Fatalf("exit %d", rc)
	}
	for _, c := range e.run.all() {
		for _, bad := range []string{".Config.Env", "--format {{json", " exec ", " config ", "docker cp", " env", "docker logs"} {
			if strings.Contains(c, bad) {
				t.Errorf("the bundle must not run %q (it could expose environment variables): %s", bad, c)
			}
		}
	}
}

func TestSupportBundleOptions(t *testing.T) {
	e := bundleEnv(t)
	dir := t.TempDir()
	out := filepath.Join(dir, "b.tar.gz")
	if rc := e.main("support", "bundle", "--dir", e.dir, "--output", out, "--yes", "--no-logs"); rc != ExitOK {
		t.Fatalf("exit %d", rc)
	}
	if _, ok := readBundle(t, out)["logs.txt"]; ok {
		t.Error("--no-logs must leave the logs out")
	}
	if e.run.has(" logs ") {
		t.Error("--no-logs must not read logs at all")
	}
	// Never overwrite.
	if rc := e.main("support", "bundle", "--dir", e.dir, "--output", out, "--yes"); rc != ExitFail {
		t.Errorf("an existing archive must not be overwritten: exit %d", rc)
	}
	for _, args := range [][]string{{"support"}, {"support", "frobnicate"}, {"support", "bundle", "--logs-tail", "0", "--dir", e.dir}, {"support", "bundle", "--logs-tail", "999999", "--dir", e.dir}} {
		if rc := e.main(args...); rc != ExitUsage {
			t.Errorf("%v: exit %d, want %d", args, rc, ExitUsage)
		}
	}
	// A default file name is created in the working directory.
	wd, _ := os.Getwd()
	must(t, os.Chdir(dir))
	defer os.Chdir(wd)
	if rc := e.main("support", "bundle", "--dir", e.dir, "--yes", "--no-logs"); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, e.both())
	}
	m, _ := filepath.Glob(filepath.Join(dir, "zaks-drive-support-*.tar.gz"))
	if len(m) != 1 {
		t.Errorf("default archive name: %v", m)
	}
}

func TestFirstSecretInFailsClosed(t *testing.T) {
	secrets := NewSanitizer([]string{secClient, secAPI}).values
	if !firstSecretIn([]byte("x "+secClient+" y"), secrets) || !firstSecretIn([]byte(secAPI), secrets) {
		t.Error("a secret value must be detected")
	}
	if firstSecretIn([]byte("nothing here"), secrets) || firstSecretIn([]byte("anything"), nil) {
		t.Error("clean content must pass")
	}
}

func TestSummarizeConfigOmitsIdentifiers(t *testing.T) {
	e := newEnv(t)
	cfg, problems := e.app.validateDeployment(newDeployment(e.dir))
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	b, _ := json.Marshal(summarizeConfig(cfg, newDeployment(e.dir).ports()))
	for _, bad := range []string{guidA, guidB, secClient, secAPI, secAdmin, "contoso", "SAP_DOCS", "SAP_DMS", "Documents"} {
		if strings.Contains(string(b), bad) {
			t.Errorf("the summary must not contain %q:\n%s", bad, b)
		}
	}
}
