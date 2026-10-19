package ctl

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

// doctor runs `doctor --json` and returns the findings by ID plus the exit code.
func (e *env) doctor(t *testing.T, extra ...string) (map[string]Finding, int) {
	t.Helper()
	args := append([]string{"doctor", "--json", "--dir", e.dir}, extra...)
	rc := e.main(args...)
	var rep DoctorReport
	if err := json.Unmarshal(e.out.Bytes(), &rep); err != nil {
		t.Fatalf("doctor --json is not valid JSON: %v\n%s", err, e.out.String())
	}
	m := map[string]Finding{}
	for _, f := range rep.Findings {
		m[f.ID] = f
	}
	return m, rc
}

func wantStatus(t *testing.T, f map[string]Finding, id string, want Status) {
	t.Helper()
	got, ok := f[id]
	if !ok {
		t.Errorf("no finding %q", id)
		return
	}
	if got.Status != want {
		t.Errorf("%s: %s, want %s (%s)", id, got.Status, want, got.Detail)
	}
}

func TestDoctorHealthyInstallation(t *testing.T) {
	e := newEnv(t)
	f, rc := e.doctor(t)
	if rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, e.out.String())
	}
	for _, id := range []string{"os-platform", "docker-installed", "docker-daemon", "docker-compose", "deployment-files", "config-valid", "api-keys",
		"container-state", "container-resources", "ports", "health-endpoint", "readiness", "server-version", "sap-endpoint", "disk-space"} {
		wantStatus(t, f, id, StatusPass)
	}
	// Expected until an administrator acts: no allowedNetworks, no SAP certificate yet.
	wantStatus(t, f, "repository-mapping", StatusWarn)
	wantStatus(t, f, "sap-certificates", StatusWarn)
	wantStatus(t, f, "tls-certificate", StatusSkipped)
	wantStatus(t, f, "graph-connectivity", StatusSkipped)
	if !strings.Contains(f["graph-connectivity"].Detail, "contacts Microsoft") {
		t.Errorf("the skipped connectivity test must say why: %s", f["graph-connectivity"].Detail)
	}
	for _, s := range []string{secClient, secAPI, secAdmin} {
		if strings.Contains(e.out.String(), s) {
			t.Errorf("doctor output contains a secret: %s", s)
		}
	}
}

func TestDoctorTextOutputAndExitCodes(t *testing.T) {
	e := newEnv(t)
	if rc := e.main("doctor", "--dir", e.dir); rc != ExitOK {
		t.Fatalf("exit %d\n%s", rc, e.out.String())
	}
	for _, w := range []string{"[PASS", "[WARN", "[SKIPPED", "Summary:", "Not covered: the SAP side", "OAC0", "STRUST", "RSCMSTH0", "Microsoft access was not tested"} {
		if !strings.Contains(e.out.String(), w) {
			t.Errorf("output lacks %q:\n%s", w, e.out.String())
		}
	}
	if rc := e.main("doctor", "--dir", e.dir, "--strict"); rc != ExitFail {
		t.Errorf("--strict with warnings: exit %d, want %d", rc, ExitFail)
	}
}

func TestDoctorWithoutDockerExplainsAndSkips(t *testing.T) {
	e := newEnv(t)
	e.run.handler = func(c call) (Result, error) { return Result{}, ErrNotInstalled }
	f, rc := e.doctor(t)
	if rc != ExitFail {
		t.Fatalf("exit %d", rc)
	}
	wantStatus(t, f, "docker-installed", StatusFail)
	wantStatus(t, f, "docker-daemon", StatusSkipped)
	wantStatus(t, f, "docker-compose", StatusSkipped)
	wantStatus(t, f, "container-state", StatusSkipped)
	wantStatus(t, f, "readiness", StatusSkipped)
	if f["docker-installed"].Fix == "" {
		t.Error("a failure needs a fix")
	}
	// Configuration checks do not need Docker and still run.
	wantStatus(t, f, "config-valid", StatusPass)
}

func TestDoctorDockerPermissionAndDaemon(t *testing.T) {
	e := newEnv(t)
	e.run.handler = func(c call) (Result, error) {
		return Result{ExitCode: 1, Stderr: "permission denied while trying to connect to the Docker daemon socket"}, nil
	}
	f, _ := e.doctor(t)
	wantStatus(t, f, "docker-installed", StatusPass)
	wantStatus(t, f, "docker-daemon", StatusFail)
	if !strings.Contains(f["docker-daemon"].Fix, "docker\" group") {
		t.Errorf("fix: %s", f["docker-daemon"].Fix)
	}
}

func TestDoctorWithoutADeploymentStillReportsThePlatform(t *testing.T) {
	e := newEnv(t)
	empty := t.TempDir()
	rc := e.main("doctor", "--json", "--dir", empty)
	var rep DoctorReport
	must(t, json.Unmarshal(e.out.Bytes(), &rep))
	if rc != ExitFail {
		t.Errorf("exit %d", rc)
	}
	found := false
	for _, f := range rep.Findings {
		if f.ID == "deployment-files" && f.Status == StatusFail && strings.Contains(f.Fix, "zaks-drive setup") {
			found = true
		}
	}
	if !found {
		t.Errorf("deployment-files must FAIL with a pointer to setup: %+v", rep.Findings)
	}
}

func TestDoctorNotStarted(t *testing.T) {
	e := newEnv(t)
	e.useFake(func(f *dockerFake) { f.noContainer = true })
	f, rc := e.doctor(t)
	wantStatus(t, f, "container-state", StatusWarn)
	for _, id := range []string{"container-resources", "health-endpoint", "readiness", "server-version", "sap-endpoint", "sap-certificates"} {
		wantStatus(t, f, id, StatusSkipped)
	}
	if rc != ExitOK {
		t.Errorf("a stopped (not failed) deployment is a warning, not a failure: exit %d", rc)
	}
}

func TestDoctorContainerStates(t *testing.T) {
	cases := []struct {
		name string
		mod  func(f *dockerFake)
		want Status
	}{
		{"exited", func(f *dockerFake) { f.state = "exited" }, StatusFail},
		{"unhealthy", func(f *dockerFake) { f.health = "unhealthy" }, StatusFail},
		{"starting", func(f *dockerFake) { f.health = "starting" }, StatusWarn},
		{"one restart", func(f *dockerFake) { f.restarts = 1 }, StatusWarn},
		{"three restarts", func(f *dockerFake) { f.restarts = 3 }, StatusFail},
		{"healthy", func(f *dockerFake) {}, StatusPass},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			e.useFake(tc.mod)
			f, _ := e.doctor(t)
			wantStatus(t, f, "container-state", tc.want)
		})
	}
}

func TestDoctorResources(t *testing.T) {
	e := newEnv(t)
	e.useFake(func(f *dockerFake) { f.oom = true })
	f, _ := e.doctor(t)
	wantStatus(t, f, "container-resources", StatusFail)
	if !strings.Contains(f["container-resources"].Fix, "mem_limit") {
		t.Errorf("fix: %s", f["container-resources"].Fix)
	}
	e.useFake(func(f *dockerFake) { f.memory = 0 })
	f, _ = e.doctor(t)
	wantStatus(t, f, "container-resources", StatusWarn)
}

func TestDoctorReadinessClassification(t *testing.T) {
	cases := map[string]string{
		"not ready: TOKEN_FAILED: invalid_client AADSTS7000215 Invalid client secret":  "rejected the credentials",
		"not ready: AADSTS7000222 The provided client secret keys have expired":        "has expired",
		"not ready: SP_UNAUTHORIZED the access token has NO application permissions":   "no usable permissions",
		"not ready: SP_ACCESS_DENIED Microsoft Graph answered HTTP 403 (accessDenied)": "denied access",
		"not ready: SP_LIBRARY_NOT_FOUND no library called Docs":                       "SharePoint location",
		"not ready: SP_UNREACHABLE dial tcp: lookup graph.microsoft.com: no such host": "cannot reach Microsoft",
		"not ready: SP_THROTTLED Microsoft Graph answered HTTP 429":                    "throttling",
		"not ready: something unexpected":                                              "not ready",
	}
	for body, want := range cases {
		e := newEnv(t)
		e.readyStatus, e.readyBody = 503, body
		f, rc := e.doctor(t)
		wantStatus(t, f, "readiness", StatusFail)
		if !strings.Contains(f["readiness"].Detail, want) || f["readiness"].Fix == "" {
			t.Errorf("%q: detail %q fix %q (want %q)", body, f["readiness"].Detail, f["readiness"].Fix, want)
		}
		if rc != ExitFail {
			t.Errorf("exit %d", rc)
		}
	}
}

func TestDoctorPortConflictWhenNotRunning(t *testing.T) {
	e := newEnv(t)
	e.useFake(func(f *dockerFake) { f.noContainer = true })
	// Port 8080 (REST) is "taken" by another process.
	e.app.PortBusy = func(host string, port int) bool { return port == 8080 }
	f, rc := e.doctor(t)
	wantStatus(t, f, "ports", StatusFail)
	if !strings.Contains(f["ports"].Detail, "REST 0.0.0.0:8080") || !strings.Contains(f["ports"].Fix, "ss -ltnp") || !strings.Contains(f["ports"].Fix, "Cockpit") {
		t.Errorf("%+v", f["ports"])
	}
	if rc != ExitFail {
		t.Errorf("exit %d", rc)
	}
	// The command only suggests ss; it must not run arbitrary host commands.
	for _, c := range e.run.all() {
		if strings.HasPrefix(c, "ss ") || strings.HasPrefix(c, "lsof ") || strings.HasPrefix(c, "netstat ") {
			t.Errorf("doctor must not run %s", c)
		}
	}
	e.app.PortBusy = func(string, int) bool { return false }
	f, _ = e.doctor(t)
	wantStatus(t, f, "ports", StatusPass)
}

// The real probe: a port that something listens on is reported busy, a free one is not.
func TestPortInUseProbe(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	must(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	if !portInUse("127.0.0.1", port) {
		t.Error("a held port must be reported in use")
	}
	l.Close()
	if portInUse("127.0.0.1", port) {
		t.Error("a released port must be reported free")
	}
}

func mustRead(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(path)
	must(t, err)
	return b
}

func TestDoctorPortsWhenRunningButNotPublished(t *testing.T) {
	e := newEnv(t)
	e.useFake(func(f *dockerFake) { f.restPort = 9999 })
	f, _ := e.doctor(t)
	wantStatus(t, f, "ports", StatusWarn)
	if !strings.Contains(f["ports"].Detail, "REST 8080") {
		t.Errorf("%s", f["ports"].Detail)
	}
}

func TestDoctorFilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("file modes are not meaningful on Windows")
	}
	e := newEnv(t)
	envPath := filepath.Join(e.dir, ".env")
	must(t, os.Chmod(envPath, 0o644))
	f, rc := e.doctor(t)
	wantStatus(t, f, "file-permissions", StatusFail)
	if !strings.Contains(f["file-permissions"].Fix, "chmod 600") || rc != ExitFail {
		t.Errorf("%+v rc=%d", f["file-permissions"], rc)
	}
	must(t, os.Chmod(envPath, 0o640))
	f, _ = e.doctor(t)
	wantStatus(t, f, "file-permissions", StatusWarn)
	must(t, os.Chmod(envPath, 0o600))
	f, _ = e.doctor(t)
	wantStatus(t, f, "file-permissions", StatusPass)
}

func TestDoctorFilePermissionsSkippedOnWindowsHost(t *testing.T) {
	e := newEnv(t)
	e.app.GOOS = "windows"
	f, _ := e.doctor(t)
	wantStatus(t, f, "file-permissions", StatusSkipped)
	wantStatus(t, f, "os-platform", StatusWarn)
}

func TestDoctorKeysAndConfig(t *testing.T) {
	e := newEnv(t)
	envPath := filepath.Join(e.dir, ".env")
	b := mustRead(t, envPath)
	must(t, os.WriteFile(envPath, []byte(strings.Replace(string(b), "ADMIN_TOKEN="+secAdmin, "ADMIN_TOKEN=tinyvalue1", 1)), 0o600))
	f, _ := e.doctor(t)
	wantStatus(t, f, "api-keys", StatusWarn)
	if !strings.Contains(f["api-keys"].Detail, "ADMIN_TOKEN") || strings.Contains(f["api-keys"].Detail, "tinyvalue1") {
		t.Errorf("name the variable, not its value: %s", f["api-keys"].Detail)
	}
	// An invalid configuration is a FAIL with the server's error code.
	must(t, os.WriteFile(envPath, []byte(strings.Replace(string(b), "https://contoso.sharepoint.com/sites/SAP_DOCS", "https://contoso.sharepoint.com/sites/SAP_DOCS/Forms/AllItems.aspx", 1)), 0o600))
	f, rc := e.doctor(t)
	wantStatus(t, f, "config-valid", StatusFail)
	wantStatus(t, f, "repository-mapping", StatusSkipped)
	if !strings.Contains(f["config-valid"].Detail, "CFG_SITE_URL_INVALID") || rc != ExitFail {
		t.Errorf("%+v rc=%d", f["config-valid"], rc)
	}
}

func TestDoctorRepositoryMappingWarnings(t *testing.T) {
	e := newEnv(t)
	ans := validAnswers(e.dir)
	ans.AdminPort, ans.CSPort = e.adminPort, e.fake.csPort
	ans.ContReps = []ContRepAnswer{{"Z1", "none"}}
	ans.AllowedNetworks = []string{"10.0.0.0/8"}
	must(t, os.WriteFile(filepath.Join(e.dir, "config.yaml"), renderConfig(ans), 0o644))
	f, _ := e.doctor(t)
	wantStatus(t, f, "repository-mapping", StatusWarn)
	if !strings.Contains(f["repository-mapping"].Detail, "signature none") {
		t.Errorf("%s", f["repository-mapping"].Detail)
	}
	ans.ContReps = []ContRepAnswer{{"Z1", "required"}}
	must(t, os.WriteFile(filepath.Join(e.dir, "config.yaml"), renderConfig(ans), 0o644))
	f, _ = e.doctor(t)
	wantStatus(t, f, "repository-mapping", StatusPass)
}

func TestDoctorSAPEndpoint(t *testing.T) {
	e := newEnv(t)
	e.csBody = "serverStatus=\"running\";\ncontRep=\"Q9\";\n"
	f, _ := e.doctor(t)
	wantStatus(t, f, "sap-endpoint", StatusFail)
	if !strings.Contains(f["sap-endpoint"].Detail, "Z1") {
		t.Errorf("must name the missing repository: %s", f["sap-endpoint"].Detail)
	}
	e.csStatus, e.csHeader, e.csBody = 403, map[string]string{"X-ErrorCode": "CS_CLIENT_NOT_ALLOWED"}, "refused"
	f, rc := e.doctor(t)
	wantStatus(t, f, "sap-endpoint", StatusWarn)
	if !strings.Contains(f["sap-endpoint"].Fix, "RSCMSTH0") || rc != ExitOK {
		t.Errorf("%+v rc=%d", f["sap-endpoint"], rc)
	}
	e.csStatus, e.csHeader = 500, nil
	f, _ = e.doctor(t)
	wantStatus(t, f, "sap-endpoint", StatusFail)
	// With the SAP interface disabled the check is skipped, not failed.
	ans := validAnswers(e.dir)
	ans.AdminPort, ans.CSPort, ans.Interfaces = e.adminPort, e.fake.csPort, []string{"rest"}
	must(t, os.WriteFile(filepath.Join(e.dir, "config.yaml"), renderConfig(ans), 0o644))
	f, _ = e.doctor(t)
	wantStatus(t, f, "sap-endpoint", StatusSkipped)
	wantStatus(t, f, "sap-certificates", StatusSkipped)
}

func TestDoctorSAPCertificates(t *testing.T) {
	e := newEnv(t)
	future, past := time.Now().Add(400*24*time.Hour).UTC().Format(time.RFC3339), time.Now().Add(-24*time.Hour).UTC().Format(time.RFC3339)
	cert := func(active bool, notAfter string) string {
		return `{"value":[{"contRep":"Z1","authId":"CN=S4H","certificate":"c2VjcmV0LWNlcnQtYnl0ZXM=","active":` + strconv.FormatBool(active) + `,"notAfter":"` + notAfter + `","sha256Fingerprint":"AABBCCDDEEFF00112233"}]}`
	}
	cases := []struct {
		name   string
		body   string
		status int
		want   Status
		note   string
	}{
		{"none", `{"value":[]}`, 200, StatusWarn, "no certificate received yet"},
		{"inactive", cert(false, future), 200, StatusWarn, "NOT activated"},
		{"active", cert(true, future), 200, StatusPass, "1 active"},
		{"expired", cert(true, past), 200, StatusFail, "expired"},
		{"not ready", "", 404, StatusWarn, "not ready yet"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e.certs, e.certStatus = tc.body, tc.status
			f, _ := e.doctor(t)
			wantStatus(t, f, "sap-certificates", tc.want)
			if !strings.Contains(f["sap-certificates"].Detail, tc.note) {
				t.Errorf("%s", f["sap-certificates"].Detail)
			}
			if strings.Contains(e.out.String(), "c2VjcmV0LWNlcnQtYnl0ZXM") || strings.Contains(e.out.String(), secAdmin) {
				t.Error("certificate bytes and the admin token must never be printed")
			}
			if tc.want != StatusPass && tc.name != "not ready" && !strings.Contains(f["sap-certificates"].Fix, "STRUST") {
				t.Errorf("SAP-side guidance expected: %q", f["sap-certificates"].Fix)
			}
		})
	}
	// A token that the server refuses.
	envPath := filepath.Join(e.dir, ".env")
	b := mustRead(t, envPath)
	must(t, os.WriteFile(envPath, []byte(strings.Replace(string(b), "ADMIN_TOKEN="+secAdmin, "ADMIN_TOKEN="+secAdmin+"-old", 1)), 0o600))
	f, _ := e.doctor(t)
	wantStatus(t, f, "sap-certificates", StatusWarn)
	if !strings.Contains(f["sap-certificates"].Detail, "refused ADMIN_TOKEN") {
		t.Errorf("%s", f["sap-certificates"].Detail)
	}
}

func writeTLS(t *testing.T, dir string, notAfter time.Time, mismatch bool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	must(t, err)
	tmpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "zaks.test"},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	must(t, err)
	signer := key
	if mismatch {
		signer, err = ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		must(t, err)
	}
	kb, err := x509.MarshalECPrivateKey(signer)
	must(t, err)
	must(t, os.MkdirAll(filepath.Join(dir, "tls"), 0o750))
	must(t, os.WriteFile(filepath.Join(dir, "tls", "tls.crt"), pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "tls", "tls.key"), pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}), 0o600))
}

func TestDoctorTLSCertificate(t *testing.T) {
	cases := []struct {
		name     string
		notAfter time.Time
		mismatch bool
		want     Status
	}{
		{"valid", time.Now().Add(200 * 24 * time.Hour), false, StatusPass},
		{"expires soon", time.Now().Add(10 * 24 * time.Hour), false, StatusWarn},
		{"expired", time.Now().Add(-24 * time.Hour), false, StatusFail},
		{"key mismatch", time.Now().Add(200 * 24 * time.Hour), true, StatusFail},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			e := newEnv(t)
			ans := validAnswers(e.dir)
			ans.AdminPort, ans.CSPort, ans.TLS = e.adminPort, e.fake.csPort, true
			must(t, os.WriteFile(filepath.Join(e.dir, "config.yaml"), renderConfig(ans), 0o644))
			writeTLS(t, e.dir, tc.notAfter, tc.mismatch)
			f, _ := e.doctor(t)
			wantStatus(t, f, "tls-certificate", tc.want)
			if strings.Contains(e.out.String(), "PRIVATE KEY") {
				t.Error("key material must never be printed")
			}
		})
	}
	t.Run("missing files", func(t *testing.T) {
		e := newEnv(t)
		ans := validAnswers(e.dir)
		ans.AdminPort, ans.CSPort, ans.TLS = e.adminPort, e.fake.csPort, true
		must(t, os.WriteFile(filepath.Join(e.dir, "config.yaml"), renderConfig(ans), 0o644))
		f, _ := e.doctor(t)
		wantStatus(t, f, "tls-certificate", StatusFail)
	})
}

func TestDoctorDiskSpace(t *testing.T) {
	e := newEnv(t)
	for free, want := range map[int64]Status{50 << 30: StatusPass, 1 << 30: StatusWarn, 100 << 20: StatusFail} {
		free := free
		e.app.DiskFree = func(string) (int64, error) { return free, nil }
		f, _ := e.doctor(t)
		wantStatus(t, f, "disk-space", want)
	}
	e.app.DiskFree = func(string) (int64, error) { return 0, os.ErrInvalid }
	f, _ := e.doctor(t)
	wantStatus(t, f, "disk-space", StatusSkipped)
}

func TestDoctorConnectivityIsExplicitAndSanitized(t *testing.T) {
	e := newEnv(t)
	base := e.run.handler
	e.run.handler = func(c call) (Result, error) {
		if strings.Contains(c.String(), " exec ") {
			return Result{ExitCode: 1, Stdout: "  FAIL  [DMS] Write: upload a small file\n  token=" + secClient + "\n  passed 3, failed 1, skipped 0\n"}, nil
		}
		return base(c)
	}
	f, rc := e.doctor(t)
	wantStatus(t, f, "graph-connectivity", StatusSkipped)
	if e.run.has(" exec ") {
		t.Fatal("Microsoft must not be contacted unless --connectivity is given")
	}
	_ = rc
	f, rc = e.doctor(t, "--connectivity")
	wantStatus(t, f, "graph-connectivity", StatusFail)
	if rc != ExitFail {
		t.Errorf("exit %d", rc)
	}
	if strings.Contains(e.out.String(), secClient) {
		t.Errorf("the check output must be sanitized: %s", e.out.String())
	}
	if !e.run.has("exec -T adapter /zaks-drive-server check") {
		t.Errorf("wrong exec command: %v", e.run.all())
	}
	// Passing run.
	e.run.handler = func(c call) (Result, error) {
		if strings.Contains(c.String(), " exec ") {
			return Result{Stdout: "  passed 10, failed 0, skipped 0\n  All required access is in place.\n"}, nil
		}
		return base(c)
	}
	f, rc = e.doctor(t, "--connectivity")
	wantStatus(t, f, "graph-connectivity", StatusPass)
}

func TestExecServerCheckFallsBackToTheOldBinaryName(t *testing.T) {
	e := newEnv(t)
	base := e.run.handler
	e.run.handler = func(c call) (Result, error) {
		s := c.String()
		if strings.Contains(s, " exec ") && strings.Contains(s, "/zaks-drive-server") {
			return Result{ExitCode: 127, Stderr: "exec: \"/zaks-drive-server\": executable file not found in $PATH"}, nil
		}
		if strings.Contains(s, " exec ") {
			return Result{Stdout: "passed 10, failed 0\n"}, nil
		}
		return base(c)
	}
	res, err := e.app.execServerCheck(nil2(), newDeployment(e.dir), nil)
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	if !e.run.has("exec -T adapter /zaks-drive check") {
		t.Errorf("the compatibility name must be tried: %v", e.run.all())
	}
}

func TestCheckCommand(t *testing.T) {
	e := newEnv(t)
	base := e.run.handler
	e.run.handler = func(c call) (Result, error) {
		if strings.Contains(c.String(), " exec ") {
			return Result{Stdout: "  PASS  Credentials configured\n  FAIL  [DMS] Find the library " + secClient + "\n  passed 1, failed 1\n", ExitCode: 1}, nil
		}
		return base(c)
	}
	if rc := e.main("check", "--dir", e.dir); rc != ExitFail {
		t.Fatalf("exit %d\n%s", rc, e.both())
	}
	for _, w := range []string{"contacts Microsoft Entra ID", "PASS  Credentials configured", "FAIL  [DMS] Find the library"} {
		if !strings.Contains(e.out.String(), w) {
			t.Errorf("lacks %q:\n%s", w, e.out.String())
		}
	}
	if strings.Contains(e.both(), secClient) {
		t.Error("check output must be sanitized")
	}
	e.useFake(func(f *dockerFake) { f.noContainer = true })
	if rc := e.main("check", "--dir", e.dir); rc != ExitUnhealthy || !strings.Contains(e.err.String(), "zaks-drive start") {
		t.Errorf("not running: exit %d\n%s", rc, e.err.String())
	}
}

func TestDoctorList(t *testing.T) {
	e := newEnv(t)
	if rc := e.main("doctor", "--list"); rc != ExitOK {
		t.Fatalf("exit %d", rc)
	}
	for _, di := range Diagnostics {
		if !strings.Contains(e.out.String(), di.ID) {
			t.Errorf("--list lacks %s", di.ID)
		}
	}
	if len(e.run.calls) != 0 {
		t.Error("--list must not run anything")
	}
}

// Every finding doctor can emit is documented, and every documented diagnostic is emitted.
func TestDiagnosticsCatalogueIsComplete(t *testing.T) {
	emitted := map[string]bool{}
	run := func(e *env, args ...string) {
		f, _ := e.doctor(t, args...)
		for id := range f {
			emitted[id] = true
		}
	}
	e := newEnv(t)
	run(e)
	run(e, "--connectivity")
	e.useFake(func(f *dockerFake) { f.noContainer = true })
	run(e)
	known := map[string]bool{}
	for _, di := range Diagnostics {
		known[di.ID] = true
		if di.Title == "" || di.How == "" || (di.Fail == "" && di.Warn == "") {
			t.Errorf("diagnostic %s needs a title, an explanation of how it works, and when it fails or warns", di.ID)
		}
		if !emitted[di.ID] {
			t.Errorf("documented diagnostic %s is never emitted", di.ID)
		}
	}
	for id := range emitted {
		if !known[id] {
			t.Errorf("finding %s is emitted but not documented", id)
		}
	}
}
