#!/bin/sh
# Prints the ZAKS Drive version for this checkout: the git tag without its "v" when HEAD is tagged
# (0.1.0-beta.1), otherwise "<last tag>-<n>-g<commit>[-dirty]", or "dev" outside a git checkout.
# The same string is used for the binary, the image tag, the OCI version label and release artifacts.
#   ZAKS_DRIVE_VERSION=$(scripts/version.sh) docker compose -f deploy/docker-compose.yml up -d --build
d=$(git describe --tags --always --dirty 2>/dev/null) || { echo dev; exit 0; }
echo "${d#v}"
