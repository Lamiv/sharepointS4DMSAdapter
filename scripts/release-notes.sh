#!/bin/sh
# Writes the release notes of a version to stdout: the matching section of CHANGELOG.md (or its
# "Unreleased" section), followed by verification and upgrade instructions.
#
#   scripts/release-notes.sh <version> [changelog]
set -eu
[ $# -ge 1 ] || { echo "usage: scripts/release-notes.sh <version> [changelog]" >&2; exit 2; }
VERSION=${1#v}
CHANGELOG=${2:-CHANGELOG.md}
REPO=${ZAKS_DRIVE_REPO:-Lamiv/zaks-drive}

echo "# ZAKS Drive $VERSION"
echo
# Prefer a section named after the version; fall back to Unreleased.
section=$(awk -v v="$VERSION" '
	/^## \[/ { want = (index($0, "[" v "]") > 0); next }
	want { print }
' "$CHANGELOG")
if [ -z "$(printf '%s' "$section" | tr -d '[:space:]')" ]; then
	section=$(awk '/^## \[Unreleased\]/ { want = 1; next } /^## \[/ { want = 0 } want { print }' "$CHANGELOG")
fi
if [ -n "$(printf '%s' "$section" | tr -d '[:space:]')" ]; then
	printf '%s\n' "$section"
else
	echo "See CHANGELOG.md for the changes in this release."
fi

cat <<EOF

## Install

\`\`\`bash
curl -fsSLO https://github.com/$REPO/releases/download/v$VERSION/install.sh
less install.sh                       # read it first
sh install.sh --version $VERSION      # pinned; verifies the SHA-256 checksum before anything runs
zaks-drive setup
\`\`\`

Linux amd64 and arm64. The installer needs Docker and the Docker Compose plugin on the machine that runs ZAKS Drive;
it does not install them. See docs/cli.md.

## Verify the download yourself

\`\`\`bash
sha256sum -c SHA256SUMS --ignore-missing
gh attestation verify zaks-drive_${VERSION}_linux_amd64.tar.gz --repo $REPO   # build provenance, needs the GitHub CLI
\`\`\`

\`SHA256SUMS\` covers the archives and \`install.sh\`. The build is reproducible for a given source revision and Go
toolchain; \`release.json\` inside each archive records the revision.

## Upgrade

On an existing deployment: \`zaks-drive update --version $VERSION\` (add \`--check\` to only compare). It verifies the
release, builds the image, restarts, checks health and returns to the previous version if the new one does not become
ready. \`zaks-drive rollback\` goes back by hand. Configuration and data are not touched. A change of compatibility line
(\`0.y\` to another \`0.y\`, or a new major version) asks for confirmation; read the notes above first.

## Notes

- The management command (\`zaks-drive\`) and the server (\`zaks-drive-server\`) of one release carry the same version.
- No container image is published to a registry; \`zaks-drive\` builds \`zaks-drive:$VERSION\` locally from the verified
  server binary.
- This release has not been validated by SAP, and the arm64 build has not been tested on arm64 hardware unless the
  notes above say so.
EOF
