package ctl

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// A release payload is the unpacked content of a release bundle:
//
//	<share>/<version>/zaks-drive-server
//	<share>/<version>/deploy/Dockerfile.release
//	<share>/<version>/spool/.keep
//
// where <share> is $ZAKS_DRIVE_SHARE, or <prefix>/share/zaks-drive next to the directory of this
// executable. The installer (scripts/install.sh) and `zaks-drive update` put verified releases there.

var versionRe = regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z._+-]{0,63}$`)

// validVersion reports whether v is usable as a directory name and image tag.
func validVersion(v string) bool { return versionRe.MatchString(v) && !strings.Contains(v, "..") }

func (a *App) shareDir() string {
	if s := a.env("ZAKS_DRIVE_SHARE"); s != "" {
		return s
	}
	if a.ExePath == nil {
		return ""
	}
	exe, err := a.ExePath()
	if err != nil {
		return ""
	}
	if resolved, err := filepath.EvalSymlinks(exe); err == nil {
		exe = resolved
	}
	return filepath.Join(filepath.Dir(exe), "..", "share", "zaks-drive")
}

// payloadDir returns the directory of the unpacked release for version, if it exists.
func (a *App) payloadDir(version string) (string, bool) {
	if !validVersion(version) {
		return "", false
	}
	share := a.shareDir()
	if share == "" {
		return "", false
	}
	dir := filepath.Join(share, version)
	if st, err := os.Stat(filepath.Join(dir, "zaks-drive-server")); err == nil && !st.IsDir() {
		return dir, true
	}
	return "", false
}

// buildImage builds zaks-drive:<version> from the payload directory with an explicit
// argument list. ZAKS_DRIVE_BASE_IMAGE may pin the base image by digest.
func (a *App) buildImage(ctx context.Context, dir, version string) (Result, error) {
	args := []string{"build", "-f", filepath.Join(dir, "deploy", "Dockerfile.release"),
		"--build-arg", "VERSION=" + version, "-t", "zaks-drive:" + version}
	if base := a.env("ZAKS_DRIVE_BASE_IMAGE"); base != "" {
		args = append(args, "--build-arg", "BASE_IMAGE="+base)
	}
	args = append(args, dir)
	return a.Runner.Run(ctx, "docker", args, RunOpts{})
}

// buildFromPayload builds the image for version from the release payload on this machine.
func (a *App) buildFromPayload(ctx context.Context, version string) int {
	if version == "" {
		return a.fail(ExitFail, "no installed version is recorded for this deployment, so the image cannot be built",
			"run: zaks-drive setup --force, or zaks-drive update --version <version>")
	}
	dir, ok := a.payloadDir(version)
	if !ok {
		return a.fail(ExitFail, "the image zaks-drive:"+version+" does not exist and no release payload for it was found",
			"install the release with install.sh --version "+version+", or run: zaks-drive update --version "+version)
	}
	a.printf("Building the image zaks-drive:%s from the verified release payload...\n", version)
	res, err := a.buildImage(ctx, dir, version)
	if err != nil || res.ExitCode != 0 {
		return a.fail(ExitFail, "docker build failed: "+oneLine(trimLines(res.Stderr, 4)), "check your network access to gcr.io (the base image) and run again")
	}
	return ExitOK
}
