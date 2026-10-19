package ctl

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// switchVersion makes version the one that runs: it records it, recreates the containers from
// that image (volumes are untouched: there is no `down`), and waits for readiness. It returns
// "" on success or the reason it failed.
func (a *App) switchVersion(ctx context.Context, d *Deployment, version string, timeout time.Duration) string {
	st := d.loadState()
	st.Current = version
	if err := d.saveState(st); err != nil {
		return "cannot record the version: " + err.Error()
	}
	res, err := a.compose(ctx, d, RunOpts{}, "up", "-d")
	if err != nil || res.ExitCode != 0 {
		return "docker compose up failed: " + oneLine(trimLines(a.sanitizerFor(d).Text(res.Stderr), 3))
	}
	return a.waitReady(ctx, d, timeout)
}

func (a *App) cmdUpdate(ctx context.Context, args []string) int {
	fset := flag.NewFlagSet("update", flag.ContinueOnError)
	fset.SetOutput(a.Err)
	dir := fset.String("dir", "", "deployment directory")
	version := fset.String("version", "", "target version (default: the latest release; pre-releases must be named)")
	check := fset.Bool("check", false, "only compare versions; change nothing")
	yes := fset.Bool("yes", false, "confirm the update without asking")
	allowBreaking := fset.Bool("allow-breaking", false, "also accept a move to another compatibility line, or a downgrade")
	baseURL := fset.String("base-url", "", "release location (https:// only; default "+DefaultReleaseBase+")")
	timeout := fset.Duration("timeout", 120*time.Second, "how long to wait until the new version is ready")
	if err := fset.Parse(args); err != nil {
		return ExitUsage
	}
	if *baseURL != "" {
		if err := validReleaseBase(*baseURL); err != nil {
			return a.fail(ExitUsage, err.Error(), "")
		}
		a.ReleaseBase = *baseURL
	} else if env := a.env("ZAKS_DRIVE_RELEASE_BASE_URL"); env != "" {
		if err := validReleaseBase(env); err != nil {
			return a.fail(ExitUsage, "ZAKS_DRIVE_RELEASE_BASE_URL: "+err.Error(), "")
		}
	} else if a.ReleaseBase != "" {
		if err := validReleaseBase(a.ReleaseBase); err != nil {
			return a.fail(ExitUsage, err.Error(), "")
		}
	}
	d, err := a.locate(*dir)
	if err != nil {
		return a.fail(ExitNoDeployment, err.Error(), "create one with: zaks-drive setup")
	}
	if d.SourceMode() {
		return a.fail(ExitFail, "this deployment builds the image from a source checkout, so it is updated with git", "git pull, then: zaks-drive restart (or start) — see docs/development/local-development.md")
	}
	st := d.loadState()
	current := st.Current

	// Which version?
	var target string
	if *version != "" {
		target = strings.TrimPrefix(*version, "v")
		if !validVersion(target) {
			return a.fail(ExitUsage, fmt.Sprintf("%q is not a version like 0.1.0 or 0.1.0-beta.1 (SemVer without build metadata)", *version), "")
		}
	} else {
		t, err := a.latestTag(ctx)
		if err != nil {
			return a.fail(ExitFail, err.Error(), "")
		}
		target = t
	}
	tgt, _ := ParseVersion(target)
	cur, curOK := ParseVersion(current)
	cli, cliOK := ParseVersion(a.Version)

	a.printf("Installed version   %s\n", orUnknown(current))
	a.printf("Target version      %s\n", target)
	a.printf("Management command  %s\n", a.Version)
	downgrade := curOK && tgt.Compare(cur) < 0
	breaking := curOK && !SameLine(cur, tgt)
	cliMismatch := cliOK && !SameLine(cli, tgt)
	if level, note := CompatNote(a.Version, target); level != "ok" {
		a.printf("Compatibility       %s\n", note)
	}
	switch {
	case curOK && tgt.Compare(cur) == 0:
		a.printf("\nZAKS Drive is already on %s.\n", target)
		return ExitOK
	case downgrade:
		a.printf("This is a DOWNGRADE.\n")
	}
	if breaking {
		a.printf("This moves the server to another compatibility line (%s to %s): interfaces or configuration may change. Read the release notes first.\n", current, target)
	}
	if *check {
		a.printf("\nNothing was changed (--check).\n")
		return ExitOK
	}

	if _, err := a.checkDocker(ctx); err != nil {
		var pe *PrereqError
		if asPrereq(err, &pe) {
			return a.fail(ExitPrereq, pe.Msg, pe.Fix)
		}
		return a.fail(ExitPrereq, err.Error(), "")
	}
	if breaking || downgrade || cliMismatch {
		if !*allowBreaking {
			if a.Interactive == nil || !a.Interactive() {
				return a.fail(ExitDeclined, "this update changes the compatibility line or goes backwards; it needs your explicit approval", "re-run with --allow-breaking after reading the release notes")
			}
			if !a.confirm(ctx, "This is a breaking or backwards change. Continue?", false) {
				return ExitDeclined
			}
		}
	}
	if !a.confirm(ctx, fmt.Sprintf("Update ZAKS Drive to %s? The service restarts (a brief interruption); configuration and data are kept.", target), *yes) {
		return ExitDeclined
	}

	// 1. Download and verify. Nothing is opened before the checksum matches.
	a.printf("Downloading release %s and verifying its checksum...\n", target)
	fetched, err := a.fetchRelease(ctx, target)
	if err != nil {
		var ve *VerifyError
		if errors.As(err, &ve) {
			return a.fail(ExitVerify, ve.Msg, "no change was made; do not use this download")
		}
		return a.fail(ExitFail, err.Error(), "check your network access, or use --base-url for a mirror")
	}
	defer fetched.Cleanup()
	a.printf("Checksum OK (SHA-256 %s).\n", fetched.SHA256)

	// 2. Unpack with strict rules, next to the final location, then move into place.
	arch, _ := releaseArch(a.GOARCH)
	share := a.shareDir()
	if share == "" {
		return a.fail(ExitFail, "cannot determine where releases are stored", "set ZAKS_DRIVE_SHARE to a directory you own")
	}
	if err := os.MkdirAll(share, 0o755); err != nil {
		return a.fail(ExitFail, "cannot create "+share+": "+err.Error(), "set ZAKS_DRIVE_SHARE to a directory you own (the command never uses sudo)")
	}
	newDir := filepath.Join(share, fmt.Sprintf(".%s.new-%d", target, os.Getpid()))
	_ = os.RemoveAll(newDir)
	if err := extractRelease(fetched.Archive, fmt.Sprintf("zaks-drive_%s_linux_%s", target, arch), newDir); err == nil {
		err = checkExtracted(newDir, target)
		if err != nil {
			_ = os.RemoveAll(newDir)
			return a.verifyOrFail(err)
		}
	} else {
		_ = os.RemoveAll(newDir)
		return a.verifyOrFail(err)
	}
	final := filepath.Join(share, target)
	if _, err := os.Stat(final); err == nil {
		old := newDir + ".old"
		if err := os.Rename(final, old); err != nil {
			_ = os.RemoveAll(newDir)
			return a.fail(ExitFail, "cannot replace "+final+": "+err.Error(), "")
		}
		defer os.RemoveAll(old)
	}
	if err := os.Rename(newDir, final); err != nil {
		_ = os.RemoveAll(newDir)
		return a.fail(ExitFail, "cannot install the release into "+final+": "+err.Error(), "")
	}

	// 3. Build the image from the verified server binary. Failure here changes nothing.
	a.printf("Building the image zaks-drive:%s...\n", target)
	res, err := a.buildImage(ctx, final, target)
	if err != nil || res.ExitCode != 0 {
		return a.fail(ExitFail, "docker build failed: "+oneLine(trimLines(res.Stderr, 4)), "nothing was changed; the running version is untouched")
	}
	if ir, err := a.Runner.Run(ctx, "docker", []string{"image", "inspect", "--format", "{{.Id}}", "zaks-drive:" + target}, RunOpts{}); err == nil && ir.ExitCode == 0 {
		a.printf("Image zaks-drive:%s is %s\n", target, strings.TrimSpace(ir.Stdout))
	}

	// 4. Switch, check health, and go back if the new version does not become ready.
	prev := current
	st.Previous, st.Current = prev, target
	st.History = append(st.History, StateEvent{At: a.Now(), Action: "update", Version: target})
	if err := d.saveState(st); err != nil {
		return a.fail(ExitFail, "cannot record the update: "+err.Error(), "")
	}
	a.printf("Restarting on %s...\n", target)
	if reason := a.switchVersion(ctx, d, target, *timeout); reason != "" {
		a.errorf("zaks-drive: the new version did not become ready: %s\n", reason)
		return a.recoverFromFailedUpdate(ctx, d, prev, target, *timeout)
	}
	a.printf("ZAKS Drive %s is ready.\n", target)
	if prev != "" {
		a.printf("The previous version %s is kept: return to it with: zaks-drive rollback\n", prev)
	}
	a.printf("Old images are never removed automatically (docker image rm zaks-drive:<version> when you no longer need one).\n")
	if cliOK && !SameLine(cli, tgt) || a.Version != target {
		a.printf("The management command is %s; to update it too run: install.sh --version %s\n", a.Version, target)
	}
	return ExitOK
}

func orUnknown(s string) string {
	if s == "" {
		return "(not recorded)"
	}
	return s
}

func (a *App) verifyOrFail(err error) int {
	var ve *VerifyError
	if errors.As(err, &ve) {
		return a.fail(ExitVerify, ve.Msg, "no change was made")
	}
	return a.fail(ExitFail, err.Error(), "no change was made")
}

// recoverFromFailedUpdate puts the previous version back after a failed update.
func (a *App) recoverFromFailedUpdate(ctx context.Context, d *Deployment, prev, failed string, timeout time.Duration) int {
	a.printRecentLogs(ctx, d, 15)
	if prev == "" {
		return a.fail(ExitUnhealthy, "no previous version is recorded, so it cannot be restored automatically",
			"fix the problem (zaks-drive doctor), or reinstall a known version: zaks-drive update --version <version>")
	}
	if !a.imageExists(ctx, "zaks-drive:"+prev) {
		return a.fail(ExitUnhealthy, "the previous image zaks-drive:"+prev+" no longer exists locally, so it cannot be restored automatically",
			"run: zaks-drive update --version "+prev+"   (it rebuilds the image from the verified release)")
	}
	a.errorf("Going back to %s...\n", prev)
	st := d.loadState()
	st.Previous = ""
	st.History = append(st.History, StateEvent{At: a.Now(), Action: "rollback", Version: prev})
	_ = d.saveState(st)
	if reason := a.switchVersion(ctx, d, prev, timeout); reason != "" {
		return a.fail(ExitUnhealthy, "the previous version "+prev+" is not ready either: "+reason, "run: zaks-drive doctor")
	}
	return a.fail(ExitFail, fmt.Sprintf("the update to %s failed and ZAKS Drive is running %s again; configuration and data were not changed", failed, prev), "run: zaks-drive doctor, and look at the logs above")
}

func (a *App) cmdRollback(ctx context.Context, args []string) int {
	fset := flag.NewFlagSet("rollback", flag.ContinueOnError)
	fset.SetOutput(a.Err)
	dir := fset.String("dir", "", "deployment directory")
	yes := fset.Bool("yes", false, "confirm without asking")
	timeout := fset.Duration("timeout", 120*time.Second, "how long to wait until the version is ready")
	if err := fset.Parse(args); err != nil {
		return ExitUsage
	}
	d, code := a.prepare(ctx, *dir)
	if code != ExitOK {
		return code
	}
	if d.SourceMode() {
		return a.fail(ExitFail, "this deployment builds from source; use git to go back", "git checkout the previous revision, then: zaks-drive restart")
	}
	st := d.loadState()
	if st.Previous == "" {
		return a.fail(ExitFail, "there is no previous version to return to", "see what is installed: zaks-drive status; install one: zaks-drive update --version <version>")
	}
	if !validVersion(st.Previous) || !a.imageExists(ctx, "zaks-drive:"+st.Previous) {
		return a.fail(ExitFail, "the image zaks-drive:"+st.Previous+" does not exist locally", "run: zaks-drive update --version "+st.Previous+"   (it rebuilds the image from the verified release)")
	}
	a.printf("Installed %s, rolling back to %s.\n", orUnknown(st.Current), st.Previous)
	if !a.confirm(ctx, fmt.Sprintf("Roll back to %s? The service restarts; configuration and data are kept.", st.Previous), *yes) {
		return ExitDeclined
	}
	from, to := st.Current, st.Previous
	st.Previous = from
	st.History = append(st.History, StateEvent{At: a.Now(), Action: "rollback", Version: to})
	if err := d.saveState(st); err != nil {
		return a.fail(ExitFail, err.Error(), "")
	}
	if reason := a.switchVersion(ctx, d, to, *timeout); reason != "" {
		a.errorf("zaks-drive: %s did not become ready: %s\n", to, reason)
		if from != "" && a.imageExists(ctx, "zaks-drive:"+from) {
			a.errorf("Returning to %s...\n", from)
			st2 := d.loadState()
			st2.Previous = to
			_ = d.saveState(st2)
			if r2 := a.switchVersion(ctx, d, from, *timeout); r2 == "" {
				return a.fail(ExitFail, "the rollback failed; ZAKS Drive is running "+from+" again", "run: zaks-drive doctor")
			}
		}
		return a.fail(ExitUnhealthy, "the rollback did not produce a ready server", "run: zaks-drive doctor")
	}
	a.printf("ZAKS Drive %s is ready (rolled back from %s). To return: zaks-drive rollback\n", to, orUnknown(from))
	return ExitOK
}
