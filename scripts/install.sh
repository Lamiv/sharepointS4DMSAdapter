#!/bin/sh
# ZAKS Drive installer for Linux (amd64 and arm64).
#
#   curl -fsSLO https://github.com/Lamiv/zaks-drive/releases/latest/download/install.sh
#   less install.sh                      # read it first
#   sh install.sh --version 0.1.0-beta.1 # pin a release (recommended)
#
# What it does:
#   1. detects the architecture and downloads the matching release archive and SHA256SUMS
#      over HTTPS;
#   2. verifies the archive against SHA256SUMS BEFORE it is unpacked or run (it aborts if the
#      checksum is missing or different); with --verify-attestation it also verifies the GitHub
#      build attestation (needs the gh command);
#   3. checks that the archive contains only plain files below its own directory;
#   4. installs the management command to <prefix>/bin and the release payload (server binary and
#      deployment templates) to <prefix>/share/zaks-drive/<version>;
#   5. reports whether Docker and Docker Compose are available.
#
# What it does NOT do: install Docker, use sudo, change permissions or users, start anything,
# write secrets or tenant settings, or send data anywhere except to download the release files.
#
# Options:
#   --version V        release to install (0.1.0-beta.1 or v0.1.0-beta.1); default "latest"
#   --prefix DIR       install below DIR (default /usr/local if you can write there, else ~/.local)
#   --base-url URL     release location (default https://github.com/Lamiv/zaks-drive/releases)
#   --verify-attestation   also verify the GitHub build attestation with `gh attestation verify`
#   --verify-only      download and verify, install nothing
#   --no-docker-check  skip the Docker report
#   -h, --help         show this help
#
# Exit codes: 0 ok, 1 failure, 2 usage, 3 unsupported platform, 4 missing tool, 5 download failed,
# 6 verification failed, 7 cannot write to the install location.
set -eu

REPO=${ZAKS_DRIVE_REPO:-Lamiv/zaks-drive}
BASE_URL_DEFAULT="https://github.com/$REPO/releases"

say()  { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die()  { code=$1; shift; printf 'install: error: %s\n' "$*" >&2; exit "$code"; }

usage() { sed -n '2,36p' "$0" | sed 's/^# \{0,1\}//'; }

# ---- platform ----

detect_os() {
	do_os=$(uname -s)
	[ "$do_os" = "Linux" ] || die 3 "this installer supports Linux only (found $do_os). Run the management command on a Linux host."
	printf 'linux'
}

detect_arch() {
	da_m=$(uname -m)
	case "$da_m" in
		x86_64|amd64)  printf 'amd64' ;;
		aarch64|arm64) printf 'arm64' ;;
		*) die 3 "unsupported architecture: $da_m (supported: amd64, arm64)" ;;
	esac
}

need() { command -v "$1" >/dev/null 2>&1 || die 4 "the '$1' command is required but was not found. $2"; }

# ---- downloading ----

# allowed_proto limits what curl may fetch. HTTPS only, unless a test explicitly allows file://.
allowed_proto() {
	if [ "${ZAKS_DRIVE_INSTALL_ALLOW_FILE:-}" = 1 ]; then printf '=https,file'; else printf '=https'; fi
}

http_get() { # url dest
	if command -v curl >/dev/null 2>&1; then
		curl --proto "$(allowed_proto)" --tlsv1.2 -fsSL --retry 2 -o "$2" "$1" || return 1
	elif command -v wget >/dev/null 2>&1; then
		case "$1" in https://*) ;; *) die 4 "wget can only download https:// URLs; install curl" ;; esac
		wget -q -O "$2" "$1" || return 1
	else
		die 4 "curl or wget is required to download the release. Install one of them."
	fi
}

# fetch_final_url prints the URL a request is redirected to (used to resolve "latest").
fetch_final_url() {
	need curl "Resolving 'latest' needs curl; or pin a version with --version."
	curl --proto "$(allowed_proto)" --tlsv1.2 -fsSLI -o /dev/null -w '%{url_effective}' "$1"
}

# SemVer without build metadata (a "+" is not valid in a Docker tag); pre-release identifiers are never empty.
semver_re='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$'

normalize_version() { # strips a leading v and validates SemVer
	nv_v=${1#v}
	printf '%s' "$nv_v" | grep -Eq "$semver_re" || die 2 "'$1' is not a version like 0.1.0 or 0.1.0-beta.1"
	printf '%s' "$nv_v"
}

resolve_version() { # prints the version to install
	if [ "$1" = latest ]; then
		rv_url=$(fetch_final_url "$2/latest") || die 5 "could not resolve the latest release from $2/latest. GitHub's 'latest' never points at a pre-release (such as 0.1.0-beta.1); pin the version with --version."
		rv_tag=${rv_url##*/}
		normalize_version "$rv_tag"
	else
		normalize_version "$1"
	fi
}

# ---- verification ----

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | awk '{print $1}'
	elif command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | awk '{print $1}'
	else
		die 4 "sha256sum or shasum is required to verify the download."
	fi
}

expected_hash() { # manifest filename
	awk -v f="$2" '($2 == f) || ($2 == "*" f) { print $1; exit }' "$1"
}

# verify_checksum aborts unless the file's SHA-256 matches its line in the manifest.
# NOTE: POSIX sh has no local variables, so every function uses its own variable names.
verify_checksum() { # file manifest
	vc_name=$(basename "$1")
	vc_want=$(expected_hash "$2" "$vc_name")
	[ -n "$vc_want" ] || die 6 "SHA256SUMS has no entry for $vc_name. Nothing was installed."
	printf '%s' "$vc_want" | grep -Eq '^[0-9a-f]{64}$' || die 6 "the SHA256SUMS entry for $vc_name is not a SHA-256 value. Nothing was installed."
	vc_got=$(sha256_of "$1")
	[ "$vc_got" = "$vc_want" ] || die 6 "checksum mismatch for $vc_name (expected $vc_want, got $vc_got). The download is corrupt or was tampered with. Nothing was installed."
}

# check_archive refuses archives with absolute paths, '..' segments, links, devices or members
# outside the archive's own top-level directory. It runs before anything is extracted.
check_archive() { # tarball topdir
	ca_listing=$(tar -tvzf "$1") || die 6 "$1 is not a readable tar.gz archive. Nothing was installed."
	[ -n "$ca_listing" ] || die 6 "the archive is empty"
	printf '%s\n' "$ca_listing" | grep -Eq '^[lhcbp]' && die 6 "the archive contains links or device files, which a release never does. Nothing was installed."
	ca_names=$(tar -tzf "$1") || die 6 "cannot list the archive"
	printf '%s\n' "$ca_names" | while IFS= read -r n; do
		case "$n" in
			/*|*..*|"") echo bad ;;
			"$2"/*|"$2"/) ;;
			*) echo bad ;;
		esac
	done | grep -q bad && die 6 "the archive contains paths outside $2/ . Nothing was installed."
	return 0
}

# ---- docker report (never installs anything) ----

docker_report() {
	if ! command -v docker >/dev/null 2>&1; then
		say "Docker: NOT FOUND. ZAKS Drive runs in Docker. Install Docker Engine and the Compose plugin:"
		say "         https://docs.docker.com/engine/install/   (this installer does not install Docker for you)"
		return 0
	fi
	if docker version >/dev/null 2>&1; then
		say "Docker: found, and your user can use it."
	else
		say "Docker: found, but 'docker version' failed for this user. If it says 'permission denied', add your user"
		say "         to the docker group (sudo usermod -aG docker \$USER) and log in again; if the daemon is stopped,"
		say "         start it (sudo systemctl start docker). This installer changes neither."
	fi
	if docker compose version >/dev/null 2>&1; then
		say "Docker Compose v2: found."
	else
		say "Docker Compose v2: NOT FOUND. Install the compose plugin: https://docs.docker.com/compose/install/linux/"
	fi
}

# ---- install ----

writable_dir() { # does the directory (or its nearest existing parent) accept writes?
	wd_path=$1
	while [ ! -e "$wd_path" ]; do wd_path=$(dirname "$wd_path"); done
	[ -d "$wd_path" ] && [ -w "$wd_path" ]
}

default_prefix() {
	if writable_dir /usr/local/bin && writable_dir /usr/local/share; then printf '/usr/local'; else printf '%s' "${HOME:-/tmp}/.local"; fi
}

install_release() { # extracted_dir prefix version
	ir_src=$1; ir_prefix=$2; ir_ver=$3
	ir_bin=$ir_prefix/bin
	ir_share=$ir_prefix/share/zaks-drive
	for ir_d in "$ir_bin" "$ir_share"; do
		writable_dir "$ir_d" || die 7 "cannot write to $ir_d. Run the installer with a prefix you own (--prefix \$HOME/.local), or re-run it as a user that may write there. It never uses sudo itself."
	done
	mkdir -p "$ir_bin" "$ir_share" || die 7 "cannot create $ir_bin and $ir_share"

	# Payload first (atomic move), then the command.
	ir_tmp=$ir_share/.$ir_ver.new.$$
	rm -rf "$ir_tmp"
	mkdir "$ir_tmp"
	cp -R "$ir_src/zaks-drive-server" "$ir_src/deploy" "$ir_src/spool" "$ir_src/release.json" "$ir_src/VERSION" "$ir_src/LICENSE" "$ir_tmp/"
	chmod 0755 "$ir_tmp/zaks-drive-server"
	if [ -d "$ir_share/$ir_ver" ]; then mv "$ir_share/$ir_ver" "$ir_share/.$ir_ver.old.$$"; fi
	mv "$ir_tmp" "$ir_share/$ir_ver"
	rm -rf "$ir_share/.$ir_ver.old.$$"

	install -m 0755 "$ir_src/zaks-drive" "$ir_bin/.zaks-drive.new.$$"
	mv -f "$ir_bin/.zaks-drive.new.$$" "$ir_bin/zaks-drive"
}

# ---- main ----

main() {
	version=latest
	prefix=
	base=$BASE_URL_DEFAULT
	attest=0
	verify_only=0
	docker_check=1
	while [ $# -gt 0 ]; do
		case "$1" in
			--version)  [ $# -ge 2 ] || die 2 "--version needs a value"; version=$2; shift 2 ;;
			--prefix)   [ $# -ge 2 ] || die 2 "--prefix needs a value"; prefix=$2; shift 2 ;;
			--base-url) [ $# -ge 2 ] || die 2 "--base-url needs a value"; base=$2; shift 2 ;;
			--verify-attestation) attest=1; shift ;;
			--verify-only) verify_only=1; shift ;;
			--no-docker-check) docker_check=0; shift ;;
			-h|--help) usage; exit 0 ;;
			*) die 2 "unknown option: $1 (see --help)" ;;
		esac
	done
	base=${base%/}
	case "$base" in
		https://*) ;;
		file://*) [ "${ZAKS_DRIVE_INSTALL_ALLOW_FILE:-}" = 1 ] || die 2 "--base-url must be an https:// URL" ;;
		*) die 2 "--base-url must be an https:// URL (got $base)" ;;
	esac

	detect_os >/dev/null
	arch=$(detect_arch)
	need tar "Install tar."
	need gzip "Install gzip."
	need install "Install coreutils (the install command)."
	ver=$(resolve_version "$version" "$base")
	[ "$version" = latest ] && say "Latest release is $ver. For repeatable installs pin it: --version $ver"
	[ -n "$prefix" ] || prefix=$(default_prefix)

	name=zaks-drive_${ver}_linux_${arch}
	tarball=$name.tar.gz
	say "Installing ZAKS Drive $ver for linux/$arch into $prefix"

	work=$(mktemp -d) || die 1 "cannot create a temporary directory"
	trap 'rm -rf "$work"' EXIT INT TERM
	rel=$base/download/v$ver
	say "Downloading $rel/SHA256SUMS"
	http_get "$rel/SHA256SUMS" "$work/SHA256SUMS" || die 5 "could not download $rel/SHA256SUMS"
	say "Downloading $rel/$tarball"
	http_get "$rel/$tarball" "$work/$tarball" || die 5 "could not download $rel/$tarball"

	verify_checksum "$work/$tarball" "$work/SHA256SUMS"
	say "Checksum OK (SHA-256 matches SHA256SUMS)."
	if [ "$attest" = 1 ]; then
		need gh "--verify-attestation uses 'gh attestation verify'. Install the GitHub CLI or omit the flag."
		gh attestation verify "$work/$tarball" --repo "$REPO" >/dev/null 2>&1 || die 6 "the GitHub build attestation for $tarball could not be verified. Nothing was installed."
		say "Build attestation OK (verified with gh)."
	elif command -v gh >/dev/null 2>&1; then
		say "Tip: add --verify-attestation to also verify the GitHub build attestation."
	else
		say "Note: integrity rests on HTTPS and the checksum manifest of the same release. For a cryptographic"
		say "      check install the GitHub CLI and use --verify-attestation."
	fi

	check_archive "$work/$tarball" "$name"
	mkdir "$work/x"
	tar -xzf "$work/$tarball" -C "$work/x"
	[ -x "$work/x/$name/zaks-drive" ] && [ -f "$work/x/$name/zaks-drive-server" ] || die 6 "the archive does not contain the expected files. Nothing was installed."

	if [ "$verify_only" = 1 ]; then
		say "Verified. --verify-only was given, so nothing was installed."
		return 0
	fi
	install_release "$work/x/$name" "$prefix" "$ver"
	say ""
	say "Installed:"
	say "  $prefix/bin/zaks-drive                       management command"
	say "  $prefix/share/zaks-drive/$ver/               server binary and deployment templates"
	case ":${PATH:-}:" in *":$prefix/bin:"*) ;; *) say "  Add $prefix/bin to your PATH to run 'zaks-drive'." ;; esac
	if [ "$docker_check" = 1 ]; then say ""; docker_report; fi
	say ""
	say "Next: zaks-drive setup     (see docs/cli.md)"
}

if [ "${ZAKS_DRIVE_INSTALL_NO_MAIN:-}" != 1 ]; then
	main "$@"
fi
