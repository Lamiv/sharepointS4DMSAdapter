#!/bin/sh
# Builds the Linux release bundles of ZAKS Drive.
#
#   scripts/build-release.sh <version> <out-dir> [arch ...]      (default arches: amd64 arm64)
#
# For each architecture it writes <out-dir>/zaks-drive_<version>_linux_<arch>.tar.gz containing
#   zaks-drive          the management command (cmd/zaks-drive)
#   zaks-drive-server   the server (cmd/adapter)
#   deploy/             Dockerfile.release and the compose/config/.env templates
#   spool/.keep         the directory the image copies to /var/spool/adapter
#   release.json        version, source revision, Go version, architecture
#   LICENSE, VERSION
# and, once, <out-dir>/SHA256SUMS over every archive and over install.sh.
#
# The build is reproducible for a given source revision and Go toolchain: trimmed paths, no VCS
# stamping, no timestamps (file times come from SOURCE_DATE_EPOCH, default: the commit time),
# sorted tar entries with numeric owner 0, gzip without a name or time. CI checks this by
# building twice and comparing the checksums.
#
# Nothing is uploaded or published by this script.
set -eu

die() { echo "build-release: $*" >&2; exit 1; }

[ $# -ge 2 ] || die "usage: scripts/build-release.sh <version> <out-dir> [arch ...]"
VERSION=${1#v}
OUT=$2
shift 2
ARCHES=${*:-amd64 arm64}

echo "$VERSION" | grep -Eq '^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?$' ||
	die "the version must be SemVer (for example 0.1.0 or 0.1.0-beta.1), got: $VERSION"
for a in $ARCHES; do
	case $a in amd64|arm64) ;; *) die "unsupported architecture: $a (use amd64 or arm64)" ;; esac
done

ROOT=$(cd "$(dirname "$0")/.." && pwd)
cd "$ROOT"
COMMIT=$(git rev-parse HEAD 2>/dev/null || echo unknown)
SDE=${SOURCE_DATE_EPOCH:-$(git log -1 --format=%ct 2>/dev/null || echo 0)}
GOVER=$(go env GOVERSION)
mkdir -p "$OUT"
OUT=$(cd "$OUT" && pwd)
STAGE=$OUT/.stage
rm -rf "$STAGE"
mkdir -p "$STAGE"

for arch in $ARCHES; do
	name=zaks-drive_${VERSION}_linux_${arch}
	dir=$STAGE/$name
	mkdir -p "$dir/deploy" "$dir/spool"
	echo "build: $name"
	for target in "zaks-drive ./cmd/zaks-drive" "zaks-drive-server ./cmd/adapter"; do
		bin=${target% *}
		pkg=${target#* }
		GOOS=linux GOARCH=$arch CGO_ENABLED=0 go build -trimpath -buildvcs=false \
			-ldflags "-s -w -X main.version=$VERSION" -o "$dir/$bin" "$pkg"
	done
	cp deploy/Dockerfile.release deploy/docker-compose.release.yml deploy/config.example.yaml deploy/.env.example "$dir/deploy/"
	: > "$dir/spool/.keep"
	cp LICENSE "$dir/LICENSE"
	printf '%s\n' "$VERSION" > "$dir/VERSION"
	printf '{\n  "version": "%s",\n  "commit": "%s",\n  "go": "%s",\n  "os": "linux",\n  "arch": "%s"\n}\n' "$VERSION" "$COMMIT" "$GOVER" "$arch" > "$dir/release.json"
	chmod 0755 "$dir/zaks-drive" "$dir/zaks-drive-server"
	# Deterministic archive.
	(cd "$STAGE" && tar --sort=name --mtime="@$SDE" --owner=0 --group=0 --numeric-owner \
		--mode='u+rwX,go+rX,go-w' -cf - "$name" | gzip -n -9 > "$OUT/$name.tar.gz")
done
rm -rf "$STAGE"

# The checksum manifest covers the archives and the installer.
[ -f "$ROOT/scripts/install.sh" ] && cp "$ROOT/scripts/install.sh" "$OUT/install.sh"
(cd "$OUT" && sha256sum zaks-drive_*_linux_*.tar.gz install.sh > SHA256SUMS)
echo "wrote:"
ls -l "$OUT" | sed 's/^/  /'
echo "checksums:"
sed 's/^/  /' "$OUT/SHA256SUMS"
