#!/bin/sh
# Tests the release build and the installer without any network access: it builds release
# bundles, serves them over file:// and runs scripts/install.sh against them.
#
#   sh scripts/test_install.sh                     (needs Go, tar, gzip, curl, sha256sum, install)
#   docker run --rm -v "$PWD:/src" -w /src golang:1.25 sh scripts/test_install.sh
#
# Nothing is installed outside a temporary directory and nothing is downloaded from the internet.
set -u

ROOT=$(cd "$(dirname "$0")/.." && pwd)
INSTALL=$ROOT/scripts/install.sh
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT
pass=0
fail=0

ok()  { pass=$((pass + 1)); printf 'ok   - %s\n' "$1"; }
bad() { fail=$((fail + 1)); printf 'FAIL - %s\n' "$1"; }
# expect "description" expected_exit command...  (the command's output goes to $WORK/out)
expect() {
	desc=$1; want=$2; shift 2
	"$@" > "$WORK/out" 2>&1
	got=$?
	if [ "$got" = "$want" ]; then ok "$desc"; else bad "$desc (exit $got, wanted $want)"; sed 's/^/       /' "$WORK/out" | head -8; fi
}
contains() { # description pattern
	if grep -q -- "$2" "$WORK/out"; then ok "$1"; else bad "$1 (output lacks: $2)"; sed 's/^/       /' "$WORK/out" | head -8; fi
}
lacks() { # description pattern file
	if grep -q -- "$2" "$3"; then bad "$1 (found: $2)"; else ok "$1"; fi
}

VER=0.0.1-test
cd "$ROOT" || exit 1

echo "== build the release bundles (amd64 and arm64)"
if ! SOURCE_DATE_EPOCH=1700000000 sh scripts/build-release.sh "$VER" "$WORK/rel" amd64 arm64 > "$WORK/build.log" 2>&1; then
	cat "$WORK/build.log"; echo "FAIL - build-release.sh"; exit 1
fi
ok "build-release.sh builds both architectures"
for a in amd64 arm64; do
	[ -f "$WORK/rel/zaks-drive_${VER}_linux_$a.tar.gz" ] && ok "archive for $a exists" || bad "archive for $a exists"
done
(cd "$WORK/rel" && sha256sum -c SHA256SUMS > "$WORK/out" 2>&1) && ok "SHA256SUMS covers the archives and install.sh" || { bad "SHA256SUMS verifies"; cat "$WORK/out"; }
grep -q ' install.sh$' "$WORK/rel/SHA256SUMS" && ok "install.sh is listed in SHA256SUMS" || bad "install.sh is listed in SHA256SUMS"

echo "== reproducible build"
SOURCE_DATE_EPOCH=1700000000 sh scripts/build-release.sh "$VER" "$WORK/rel2" amd64 arm64 > /dev/null 2>&1
if cmp -s "$WORK/rel/SHA256SUMS" "$WORK/rel2/SHA256SUMS"; then ok "two builds give identical checksums"; else bad "two builds give identical checksums"; diff "$WORK/rel/SHA256SUMS" "$WORK/rel2/SHA256SUMS"; fi

echo "== bundle contents"
mkdir "$WORK/peek" && tar -xzf "$WORK/rel/zaks-drive_${VER}_linux_amd64.tar.gz" -C "$WORK/peek"
B=$WORK/peek/zaks-drive_${VER}_linux_amd64
for f in zaks-drive zaks-drive-server deploy/Dockerfile.release deploy/docker-compose.release.yml deploy/config.example.yaml deploy/.env.example spool/.keep release.json VERSION LICENSE; do
	[ -e "$B/$f" ] && ok "bundle contains $f" || bad "bundle contains $f"
done
grep -q "\"version\": \"$VER\"" "$B/release.json" && ok "release.json records the version" || bad "release.json records the version"
if [ "$(uname -m)" = x86_64 ]; then
	"$B/zaks-drive" --version > "$WORK/out" 2>&1; contains "the command carries the release version" "$VER"
	"$B/zaks-drive-server" version > "$WORK/out" 2>&1; contains "the server carries the same release version" "$VER"
fi
find "$B" \( -name '.env' -o -name 'config.yaml' -o -name '*.pem' -o -name '*.key' \) | grep -q . && bad "no secret or tenant files in the bundle" || ok "no secret or tenant files in the bundle"
tar -tzf "$WORK/rel/zaks-drive_${VER}_linux_amd64.tar.gz" | grep -Eq '^/|\.\.' && bad "archive paths are relative and clean" || ok "archive paths are relative and clean"

# Where the installer looks for a release.
mkdir -p "$WORK/site/download/v$VER"
cp "$WORK/rel"/zaks-drive_*.tar.gz "$WORK/rel/SHA256SUMS" "$WORK/rel/install.sh" "$WORK/site/download/v$VER/"
BASE=file://$WORK/site
export ZAKS_DRIVE_INSTALL_ALLOW_FILE=1

echo "== platform detection"
for pair in "x86_64:amd64" "amd64:amd64" "aarch64:arm64" "arm64:arm64"; do
	m=${pair%%:*}; want=${pair##*:}
	got=$( (ZAKS_DRIVE_INSTALL_NO_MAIN=1; . "$INSTALL"; uname() { echo "$FAKE_M"; }; FAKE_M=$m; detect_arch) )
	[ "$got" = "$want" ] && ok "uname -m $m selects $want" || bad "uname -m $m selects $want (got $got)"
done
for m in armv7l i686 ppc64le riscv64; do
	( ZAKS_DRIVE_INSTALL_NO_MAIN=1; . "$INSTALL"; uname() { echo "$FAKE_M"; }; FAKE_M=$m; detect_arch ) > "$WORK/out" 2>&1; rc=$?
	[ "$rc" = 3 ] && grep -q 'unsupported architecture' "$WORK/out" && ok "$m is refused with exit 3" || bad "$m is refused with exit 3 (rc=$rc)"
done
( ZAKS_DRIVE_INSTALL_NO_MAIN=1; . "$INSTALL"; uname() { echo Darwin; }; detect_os ) > "$WORK/out" 2>&1; rc=$?
[ "$rc" = 3 ] && ok "a non-Linux OS is refused with exit 3" || bad "a non-Linux OS is refused with exit 3 (rc=$rc)"

echo "== successful install"
P=$WORK/prefix
mkdir -p "$WORK/fakebin"
printf '#!/bin/sh\necho "sudo was called: $*" >> "%s/sudo-called"\nexit 1\n' "$WORK" > "$WORK/fakebin/sudo"; chmod +x "$WORK/fakebin/sudo"
PATH="$WORK/fakebin:$PATH" sh "$INSTALL" --version "v$VER" --prefix "$P" --base-url "$BASE" > "$WORK/out" 2>&1; rc=$?
[ "$rc" = 0 ] && ok "install exits 0" || { bad "install exits 0 (rc=$rc)"; cat "$WORK/out"; }
contains "it says the checksum matched" "Checksum OK"
contains "it explains the integrity model" "integrity rests on HTTPS"
contains "it reports Docker without installing it" "Docker"
[ -x "$P/bin/zaks-drive" ] && ok "the command is installed" || bad "the command is installed"
[ -x "$P/share/zaks-drive/$VER/zaks-drive-server" ] && ok "the server payload is installed" || bad "the server payload is installed"
[ -f "$P/share/zaks-drive/$VER/deploy/Dockerfile.release" ] && ok "the deployment templates are installed" || bad "the deployment templates are installed"
[ ! -e "$WORK/sudo-called" ] && ok "sudo was never called" || bad "sudo was never called"
find "$P" \( -name '.env' -o -name 'config.yaml' \) | grep -q . && bad "no secrets or tenant settings are written" || ok "no secrets or tenant settings are written"
find "$P" -name '.*.new.*' -o -name '.*.old.*' | grep -q . && bad "no temporary files are left behind" || ok "no temporary files are left behind"
if [ "$(uname -m)" = x86_64 ]; then
	"$P/bin/zaks-drive" --version > "$WORK/out" 2>&1; contains "the installed command runs" "$VER"
fi
# Idempotent, and a second version sits next to the first.
sh "$INSTALL" --version "$VER" --prefix "$P" --base-url "$BASE" --no-docker-check > "$WORK/out" 2>&1 && ok "installing the same version again works" || bad "installing the same version again works"
mkdir -p "$WORK/site/download/v0.0.2-test"
for a in amd64 arm64; do cp "$WORK/site/download/v$VER/zaks-drive_${VER}_linux_$a.tar.gz" "$WORK/site/download/v0.0.2-test/zaks-drive_0.0.2-test_linux_$a.tar.gz"; done
echo "second version: payload dir name is derived from the version in the file name" > /dev/null

echo "== architecture selection end to end (arm64)"
P2=$WORK/prefix-arm
( uname() { case "$1" in -s) echo Linux ;; -m) echo aarch64 ;; esac; }; ZAKS_DRIVE_INSTALL_NO_MAIN=1; . "$INSTALL"; uname() { case "$1" in -s) echo Linux ;; -m) echo aarch64 ;; esac; }; main --version "$VER" --prefix "$P2" --base-url "$BASE" --no-docker-check ) > "$WORK/out" 2>&1; rc=$?
[ "$rc" = 0 ] && ok "arm64 install exits 0" || { bad "arm64 install exits 0 (rc=$rc)"; cat "$WORK/out"; }
mach=$(od -An -tx1 -j18 -N2 "$P2/bin/zaks-drive" 2>/dev/null | tr -d ' \n')
[ "$mach" = "b700" ] && ok "the installed binary is an aarch64 ELF" || bad "the installed binary is an aarch64 ELF (e_machine=$mach)"

echo "== verification failures install nothing"
fresh() { rm -rf "$WORK/bad" "$WORK/prefix-bad"; mkdir -p "$WORK/bad/download/v$VER"; cp "$WORK/site/download/v$VER"/* "$WORK/bad/download/v$VER/"; }
expect_nothing_installed() { [ ! -e "$WORK/prefix-bad/bin/zaks-drive" ] && [ ! -e "$WORK/prefix-bad/share/zaks-drive/$VER" ] && ok "$1: nothing was installed" || bad "$1: nothing was installed"; }
run_bad() { sh "$INSTALL" --version "$VER" --prefix "$WORK/prefix-bad" --base-url "file://$WORK/bad" --no-docker-check; }
HOSTARCH=amd64; [ "$(uname -m)" = x86_64 ] || HOSTARCH=arm64

fresh; printf 'x' >> "$WORK/bad/download/v$VER/zaks-drive_${VER}_linux_$HOSTARCH.tar.gz"
expect "a tampered archive is rejected (exit 6)" 6 run_bad
contains "the message says checksum mismatch" "checksum mismatch"
expect_nothing_installed "tampered archive"

fresh; grep -v "linux_$HOSTARCH.tar.gz" "$WORK/bad/download/v$VER/SHA256SUMS" > "$WORK/bad/s" && mv "$WORK/bad/s" "$WORK/bad/download/v$VER/SHA256SUMS"
expect "an archive missing from SHA256SUMS is rejected (exit 6)" 6 run_bad
contains "the message names the missing entry" "no entry"
expect_nothing_installed "missing manifest entry"

fresh; sed "s/^[0-9a-f]\{64\}  \(zaks-drive_.*linux_$HOSTARCH\)/not-a-hash  \1/" "$WORK/bad/download/v$VER/SHA256SUMS" > "$WORK/bad/s" && mv "$WORK/bad/s" "$WORK/bad/download/v$VER/SHA256SUMS"
expect "a malformed manifest entry is rejected (exit 6)" 6 run_bad
expect_nothing_installed "malformed manifest entry"

fresh; rm "$WORK/bad/download/v$VER/SHA256SUMS"
expect "a missing SHA256SUMS is a download failure (exit 5)" 5 run_bad
expect_nothing_installed "missing manifest"

# A hostile archive with a CORRECT checksum: the archive check must still refuse it.
evil() { # description member-setup-command
	fresh
	rm -rf "$WORK/evil" && mkdir -p "$WORK/evil/pkg/zaks-drive_${VER}_linux_$HOSTARCH" && (cd "$WORK/evil/pkg" && eval "$2")
	(cd "$WORK/evil/pkg" && eval "$3") || return 1
	(cd "$WORK/bad/download/v$VER" && sha256sum "zaks-drive_${VER}_linux_$HOSTARCH.tar.gz" > "$WORK/s1" && grep -v "linux_$HOSTARCH.tar.gz" SHA256SUMS > "$WORK/s2"; cat "$WORK/s2" "$WORK/s1" > SHA256SUMS)
}
evil "x" "echo data > evil.txt; echo ok > zaks-drive_${VER}_linux_$HOSTARCH/zaks-drive" "tar -czf $WORK/bad/download/v$VER/zaks-drive_${VER}_linux_$HOSTARCH.tar.gz --transform 's,^evil.txt,../evil.txt,' evil.txt zaks-drive_${VER}_linux_$HOSTARCH"
expect "an archive with a '..' member is refused (exit 6)" 6 run_bad
contains "the message says the paths are outside" "outside"
expect_nothing_installed "path traversal"
[ ! -e "$WORK/evil.txt" ] && ok "nothing was written outside the install directory" || bad "nothing was written outside the install directory"

evil "x" "ln -s /etc/passwd zaks-drive_${VER}_linux_$HOSTARCH/zaks-drive" "tar -czf $WORK/bad/download/v$VER/zaks-drive_${VER}_linux_$HOSTARCH.tar.gz zaks-drive_${VER}_linux_$HOSTARCH"
expect "an archive with a symlink is refused (exit 6)" 6 run_bad
contains "the message mentions links" "links"
expect_nothing_installed "symlink"

evil "x" "echo data > outside.txt" "tar -czf $WORK/bad/download/v$VER/zaks-drive_${VER}_linux_$HOSTARCH.tar.gz outside.txt"
expect "an archive with members outside its directory is refused (exit 6)" 6 run_bad
expect_nothing_installed "foreign member"

echo "== other guard rails"
expect "an http:// base URL is refused (exit 2)" 2 sh "$INSTALL" --version "$VER" --base-url http://example.com/releases
contains "the message asks for https" "https"
( unset ZAKS_DRIVE_INSTALL_ALLOW_FILE; expect "a file:// URL is refused in normal use (exit 2)" 2 sh "$INSTALL" --version "$VER" --base-url file:///tmp )
expect "a bad version is refused (exit 2)" 2 sh "$INSTALL" --version "../../etc" --base-url "$BASE"
expect "a version with a shell metacharacter is refused (exit 2)" 2 sh "$INSTALL" --version '1.0.0;id' --base-url "$BASE"
expect "an unknown option is refused (exit 2)" 2 sh "$INSTALL" --frobnicate
expect "--help works" 0 sh "$INSTALL" --help
contains "--help shows the usage" "Exit codes"
expect "an unwritable prefix is refused (exit 7)" 7 sh "$INSTALL" --version "$VER" --prefix /dev/null/nope --base-url "$BASE" --no-docker-check
contains "the message says it never uses sudo" "never uses sudo"
expect "--verify-only installs nothing" 0 sh "$INSTALL" --version "$VER" --prefix "$WORK/prefix-vo" --base-url "$BASE" --verify-only --no-docker-check
[ ! -e "$WORK/prefix-vo/bin/zaks-drive" ] && ok "--verify-only left the prefix empty" || bad "--verify-only left the prefix empty"
# --verify-attestation: no gh on PATH, a gh that rejects, and a gh that accepts.
mkdir -p "$WORK/nogh" "$WORK/failgh" "$WORK/okgh"
for tool in sh tar gzip install curl sha256sum awk grep sed mktemp uname basename dirname cp mv rm mkdir chmod cat tr head od find dd; do
	t=$(command -v "$tool" 2>/dev/null) && [ -n "$t" ] && ln -sf "$t" "$WORK/nogh/$tool"
done
expect "--verify-attestation without gh is refused (exit 4)" 4 env PATH="$WORK/nogh" sh "$INSTALL" --version "$VER" --prefix "$WORK/prefix-at" --base-url "$BASE" --verify-attestation --no-docker-check
contains "the message names gh" "gh attestation verify"
printf '#!/bin/sh\nexit 1\n' > "$WORK/failgh/gh"; chmod +x "$WORK/failgh/gh"
expect "a rejected build attestation aborts the install (exit 6)" 6 env PATH="$WORK/failgh:$PATH" sh "$INSTALL" --version "$VER" --prefix "$WORK/prefix-at" --base-url "$BASE" --verify-attestation --no-docker-check
contains "the message says the attestation failed" "attestation"
[ ! -e "$WORK/prefix-at/bin/zaks-drive" ] && ok "nothing was installed after a failed attestation" || bad "nothing was installed after a failed attestation"
printf '#!/bin/sh\necho "$@" >> "%s/gh-args"\nexit 0\n' "$WORK" > "$WORK/okgh/gh"; chmod +x "$WORK/okgh/gh"
expect "an accepted attestation lets the install proceed" 0 env PATH="$WORK/okgh:$PATH" sh "$INSTALL" --version "$VER" --prefix "$WORK/prefix-at" --base-url "$BASE" --verify-attestation --no-docker-check
contains "the success message mentions the attestation" "Build attestation OK"
grep -q "attestation verify .*zaks-drive_${VER}_linux_.*tar.gz --repo Lamiv/zaks-drive" "$WORK/gh-args" && ok "gh is asked to verify the archive against the project repository" || bad "gh is asked to verify the archive against the project repository"

echo "== latest resolution and Docker report"
got=$( (ZAKS_DRIVE_INSTALL_NO_MAIN=1; . "$INSTALL"; fetch_final_url() { echo "https://github.com/Lamiv/zaks-drive/releases/tag/v1.2.3"; }; resolve_version latest https://x) )
[ "$got" = "1.2.3" ] && ok "latest resolves to the tag of the redirect" || bad "latest resolves to the tag of the redirect (got $got)"
( ZAKS_DRIVE_INSTALL_NO_MAIN=1; . "$INSTALL"; fetch_final_url() { echo "https://github.com/x/releases/tag/nightly"; }; resolve_version latest https://x ) > "$WORK/out" 2>&1; rc=$?
[ "$rc" = 2 ] && ok "a non-SemVer latest tag is refused" || bad "a non-SemVer latest tag is refused (rc=$rc)"
( ZAKS_DRIVE_INSTALL_NO_MAIN=1; . "$INSTALL"; docker() { return 1; }; docker_report ) > "$WORK/out" 2>&1
contains "a docker that cannot be used is explained" "permission denied"
contains "the docker group is only suggested" "usermod"
( ZAKS_DRIVE_INSTALL_NO_MAIN=1; . "$INSTALL"; PATH=/nonexistent; docker_report ) > "$WORK/out" 2>&1
contains "missing Docker is explained and not installed" "does not install Docker"

echo
echo "installer tests: $pass passed, $fail failed"
[ "$fail" = 0 ]
