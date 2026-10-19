package ctl

import (
	"archive/tar"
	"bufio"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// DefaultReleaseBase is where releases are published.
const DefaultReleaseBase = "https://github.com/Lamiv/zaks-drive/releases"

// VerifyError is returned when a downloaded artifact fails verification. The command maps it to
// ExitVerify and nothing downloaded is used.
type VerifyError struct{ Msg string }

func (e *VerifyError) Error() string { return e.Msg }

const (
	maxManifestBytes = 1 << 20
	maxArchiveBytes  = 300 << 20
	maxFileBytes     = 150 << 20
)

// validReleaseBase accepts an https:// URL, or http:// only for the local machine (a mirror or a
// test server on loopback). Anything else would let a network attacker replace the manifest.
func validReleaseBase(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%q is not a valid URL", raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "localhost" {
			return nil
		}
		if ip := net.ParseIP(host); ip != nil && ip.IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("the release location must be an https:// URL (http:// is only accepted for localhost), got %q", raw)
}

func (a *App) releaseBase() string {
	if a.ReleaseBase != "" {
		return strings.TrimRight(a.ReleaseBase, "/")
	}
	if v := a.env("ZAKS_DRIVE_RELEASE_BASE_URL"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return DefaultReleaseBase
}

func (a *App) downloadClient() *http.Client {
	if a.DownloadHTTP != nil {
		return a.DownloadHTTP
	}
	return &http.Client{Timeout: 15 * time.Minute}
}

func (a *App) httpGetLimited(ctx context.Context, rawURL string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, err
	}
	resp, err := a.downloadClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("download of %s failed: %s", rawURL, urlErrSummary(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download of %s failed: HTTP %d", rawURL, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(b)) > limit {
		return nil, &VerifyError{Msg: "the download is larger than allowed: " + rawURL}
	}
	return b, nil
}

// urlErrSummary describes a network error without echoing the request URL.
func urlErrSummary(err error) string {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Op + " request failed (" + oneLine(ue.Err.Error()) + ")"
	}
	return oneLine(err.Error())
}

// latestTag resolves "latest" to a tag name by following the redirect of <base>/latest.
// GitHub's "latest" never points at a pre-release.
func (a *App) latestTag(ctx context.Context) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, a.releaseBase()+"/latest", nil)
	if err != nil {
		return "", err
	}
	resp, err := a.downloadClient().Do(req)
	if err != nil {
		return "", fmt.Errorf("could not reach the release location: %s", urlErrSummary(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("the release location answered HTTP %d for /latest. GitHub's 'latest' never points at a pre-release such as 0.1.0-beta.1: pin the version with --version", resp.StatusCode)
	}
	tag := strings.TrimPrefix(path.Base(resp.Request.URL.Path), "v")
	if !validVersion(tag) {
		return "", fmt.Errorf("the latest release tag is not a plain SemVer version: pin the version with --version")
	}
	return tag, nil
}

var sumsLineRe = regexp.MustCompile(`^([0-9a-fA-F]{64}) [ *](.+)$`)

// parseSums reads a SHA256SUMS manifest into name -> lower-case hex.
func parseSums(b []byte) map[string]string {
	m := map[string]string{}
	sc := bufio.NewScanner(strings.NewReader(string(b)))
	for sc.Scan() {
		if mm := sumsLineRe.FindStringSubmatch(strings.TrimSpace(sc.Text())); mm != nil {
			m[mm[2]] = strings.ToLower(mm[1])
		}
	}
	return m
}

// releaseArch maps GOARCH to the release naming.
func releaseArch(goarch string) (string, bool) {
	switch goarch {
	case "amd64", "arm64":
		return goarch, true
	}
	return "", false
}

// Fetched is a downloaded and VERIFIED release archive in a private temporary directory.
type Fetched struct {
	Dir     string // temporary directory; remove it with Cleanup
	Archive string
	SHA256  string
}

// Cleanup removes the temporary download directory.
func (f *Fetched) Cleanup() {
	if f != nil && f.Dir != "" {
		_ = os.RemoveAll(f.Dir)
	}
}

// fetchRelease downloads SHA256SUMS and the archive of version for this architecture, and
// verifies the archive's SHA-256 against the manifest. The archive is hashed while it is
// written and is never opened, extracted or run before the check passes.
func (a *App) fetchRelease(ctx context.Context, version string) (*Fetched, error) {
	arch, ok := releaseArch(a.GOARCH)
	if !ok {
		return nil, fmt.Errorf("unsupported architecture %s (releases exist for amd64 and arm64)", a.GOARCH)
	}
	base := a.releaseBase() + "/download/v" + version
	name := fmt.Sprintf("zaks-drive_%s_linux_%s.tar.gz", version, arch)

	sums, err := a.httpGetLimited(ctx, base+"/SHA256SUMS", maxManifestBytes)
	if err != nil {
		return nil, err
	}
	want, ok := parseSums(sums)[name]
	if !ok {
		return nil, &VerifyError{Msg: "SHA256SUMS has no valid entry for " + name + "; nothing was installed"}
	}

	tmp, err := os.MkdirTemp("", "zaks-drive-update-")
	if err != nil {
		return nil, err
	}
	// A constant file name: the download location never depends on text from the network.
	f := &Fetched{Dir: tmp, Archive: filepath.Join(tmp, "release.tar.gz")}
	fail := func(err error) (*Fetched, error) { f.Cleanup(); return nil, err }

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/"+name, nil)
	if err != nil {
		return fail(err)
	}
	resp, err := a.downloadClient().Do(req)
	if err != nil {
		return fail(fmt.Errorf("download of %s failed: %s", name, urlErrSummary(err)))
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fail(fmt.Errorf("download of %s failed: HTTP %d", name, resp.StatusCode))
	}
	out, err := os.OpenFile(f.Archive, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fail(err)
	}
	h := sha256.New()
	n, err := io.Copy(io.MultiWriter(out, h), io.LimitReader(resp.Body, maxArchiveBytes+1))
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fail(err)
	}
	if n > maxArchiveBytes {
		return fail(&VerifyError{Msg: name + " is larger than allowed; nothing was installed"})
	}
	got := hex.EncodeToString(h.Sum(nil))
	if got != want {
		return fail(&VerifyError{Msg: fmt.Sprintf("checksum mismatch for %s (expected %s, got %s): the download is corrupt or was tampered with; nothing was installed", name, want, got)})
	}
	f.SHA256 = got
	return f, nil
}

// topLevelFiles are the only files allowed next to the top-level directory's root.
var topLevelFiles = map[string]bool{"zaks-drive": true, "zaks-drive-server": true, "release.json": true, "VERSION": true, "LICENSE": true}

// extractRelease unpacks a VERIFIED archive into dest with strict rules: only regular files and
// directories; every name below <version dir>/; no absolute paths, no "..", no links; only
// known top-level files and the deploy/ and spool/ directories; size limits.
func extractRelease(archive, topDir, dest string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return &VerifyError{Msg: "the archive is not a valid gzip file"}
	}
	tr := tar.NewReader(gz)
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	// os.Root confines every file operation below to dest: a path that would leave it, by ".."
	// or through a link, fails in the runtime itself. This is in addition to the checks below.
	root, err := os.OpenRoot(dest)
	if err != nil {
		return err
	}
	defer root.Close()
	var total int64
	for {
		h, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return &VerifyError{Msg: "the archive is damaged: " + oneLine(err.Error())}
		}
		name := h.Name
		if strings.ContainsAny(name, "\x00\\") || strings.HasPrefix(name, "/") {
			return &VerifyError{Msg: fmt.Sprintf("the archive contains an unsafe path %q; nothing was installed", name)}
		}
		clean := path.Clean(name)
		if clean == ".." || strings.HasPrefix(clean, "../") || strings.Contains(clean, "/../") {
			return &VerifyError{Msg: fmt.Sprintf("the archive contains an unsafe path %q; nothing was installed", name)}
		}
		if clean == topDir {
			continue
		}
		if !strings.HasPrefix(clean, topDir+"/") {
			return &VerifyError{Msg: fmt.Sprintf("the archive contains %q outside %s/; nothing was installed", name, topDir)}
		}
		rel := strings.TrimPrefix(clean, topDir+"/")
		inSubdir := strings.HasPrefix(rel, "deploy/") || strings.HasPrefix(rel, "spool/") || rel == "deploy" || rel == "spool"
		switch h.Typeflag {
		case tar.TypeDir:
			if !inSubdir {
				return &VerifyError{Msg: fmt.Sprintf("the archive contains an unexpected directory %q; nothing was installed", rel)}
			}
			if err := root.MkdirAll(rel, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if !topLevelFiles[rel] && !(inSubdir && rel != "deploy" && rel != "spool") {
				return &VerifyError{Msg: fmt.Sprintf("the archive contains an unexpected file %q; nothing was installed", rel)}
			}
			if h.Size > maxFileBytes {
				return &VerifyError{Msg: fmt.Sprintf("%q is larger than allowed; nothing was installed", rel)}
			}
			total += h.Size
			if total > maxArchiveBytes {
				return &VerifyError{Msg: "the archive expands to more than allowed; nothing was installed"}
			}
			if dir := path.Dir(rel); dir != "." {
				if err := root.MkdirAll(dir, 0o755); err != nil {
					return err
				}
			}
			mode := os.FileMode(0o644)
			if rel == "zaks-drive" || rel == "zaks-drive-server" {
				mode = 0o755
			}
			out, err := root.OpenFile(rel, os.O_WRONLY|os.O_CREATE|os.O_EXCL, mode)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, io.LimitReader(tr, maxFileBytes+1)); err != nil {
				out.Close()
				return err
			}
			if err := out.Close(); err != nil {
				return err
			}
		default:
			return &VerifyError{Msg: fmt.Sprintf("the archive contains %q, which is a link or special file; a release never does; nothing was installed", name)}
		}
	}
}

// checkExtracted verifies that the unpacked release is the version that was asked for.
func checkExtracted(dir, version string) error {
	rb, err := os.ReadFile(filepath.Join(dir, "release.json"))
	if err != nil {
		return &VerifyError{Msg: "the release has no release.json; nothing was installed"}
	}
	var rj struct {
		Version string `json:"version"`
	}
	if json.Unmarshal(rb, &rj) != nil || rj.Version != version {
		return &VerifyError{Msg: "release.json does not describe version " + version + "; nothing was installed"}
	}
	vb, err := os.ReadFile(filepath.Join(dir, "VERSION"))
	if err != nil || strings.TrimSpace(string(vb)) != version {
		return &VerifyError{Msg: "the VERSION file does not match " + version + "; nothing was installed"}
	}
	if st, err := os.Stat(filepath.Join(dir, "zaks-drive-server")); err != nil || st.Size() == 0 {
		return &VerifyError{Msg: "the release has no server binary; nothing was installed"}
	}
	if _, err := os.Stat(filepath.Join(dir, "deploy", "Dockerfile.release")); err != nil {
		return &VerifyError{Msg: "the release has no Dockerfile.release; nothing was installed"}
	}
	return nil
}
