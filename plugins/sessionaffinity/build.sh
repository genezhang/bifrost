#!/usr/bin/env bash
#
# build.sh — build sessionaffinity.so matched to a specific prebuilt bifrost image.
#
# Go native plugins must match the target binary's Go toolchain, core version, libc
# (Alpine/musl) and arch, or plugin.Open rejects the .so. This script reads those
# versions straight out of the image, then builds the .so with Dockerfile.plugin — so
# a tag bump is a single command.
#
# Usage:
#   ./build.sh <image>:<tag>
#   ./build.sh maximhq/bifrost:1.5.15
#
# Overrides (skip auto-detection for any of these):
#   GO_VERSION=1.26.3 CORE_VERSION=v1.5.15 ALPINE_VERSION=3.23 BIN_PATH=/app/main \
#     ./build.sh <image>:<tag>
#
# Output: ./out/sessionaffinity.so
set -euo pipefail

IMAGE="${1:-}"
if [[ -z "$IMAGE" ]]; then
  echo "usage: $0 <image>:<tag>   (e.g. maximhq/bifrost:1.5.15)" >&2
  exit 2
fi

BIN_PATH="${BIN_PATH:-/app/main}"
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "$SCRIPT_DIR"

command -v docker >/dev/null || { echo "error: docker not found" >&2; exit 1; }

# --- pull the image's build identity out of the binary ------------------------------
tmp="$(mktemp -d)"
trap 'rm -rf "$tmp"' EXIT

echo ">> inspecting $IMAGE ..."
cid="$(docker create "$IMAGE")"
docker cp "$cid:$BIN_PATH" "$tmp/main" >/dev/null
docker cp "$cid:/etc/alpine-release" "$tmp/alpine-release" >/dev/null 2>&1 || true
docker rm "$cid" >/dev/null

# Go toolchain + core version come from the binary's embedded build info.
GO_VERSION="${GO_VERSION:-}"
CORE_VERSION="${CORE_VERSION:-}"
if [[ -z "$GO_VERSION" || -z "$CORE_VERSION" ]]; then
  command -v go >/dev/null || {
    echo "error: 'go' is required to auto-detect versions; install Go or pass" >&2
    echo "       GO_VERSION= and CORE_VERSION= explicitly." >&2
    exit 1
  }
  buildinfo="$(go version -m "$tmp/main")"
  [[ -n "$GO_VERSION" ]]   || GO_VERSION="$(printf '%s\n' "$buildinfo" | grep -oE 'go[0-9]+\.[0-9]+(\.[0-9]+)?' | head -1 | sed 's/^go//')"
  [[ -n "$CORE_VERSION" ]] || CORE_VERSION="$(printf '%s\n' "$buildinfo" | awk '$1=="dep" && $2=="github.com/maximhq/bifrost/core"{print $3; exit}')"
fi

# Alpine major.minor from /etc/alpine-release (e.g. 3.23.4 -> 3.23); default if absent.
ALPINE_VERSION="${ALPINE_VERSION:-}"
if [[ -z "$ALPINE_VERSION" ]]; then
  if [[ -s "$tmp/alpine-release" ]]; then
    ALPINE_VERSION="$(cut -d. -f1,2 < "$tmp/alpine-release")"
  else
    ALPINE_VERSION="3.23"
    echo ">> /etc/alpine-release not found; defaulting ALPINE_VERSION=$ALPINE_VERSION" >&2
  fi
fi

if [[ -z "$GO_VERSION" || -z "$CORE_VERSION" ]]; then
  echo "error: could not determine GO_VERSION ('$GO_VERSION') or CORE_VERSION ('$CORE_VERSION')." >&2
  echo "       Pass them explicitly, e.g. GO_VERSION=1.26.3 CORE_VERSION=v1.5.15 $0 $IMAGE" >&2
  exit 1
fi

echo ">> matched versions:"
echo "     GO_VERSION=$GO_VERSION"
echo "     ALPINE_VERSION=$ALPINE_VERSION"
echo "     CORE_VERSION=$CORE_VERSION"

# --- build the .so ------------------------------------------------------------------
echo ">> building sessionaffinity.so ..."
DOCKER_BUILDKIT=1 docker build -f Dockerfile.plugin \
  --build-arg "GO_VERSION=$GO_VERSION" \
  --build-arg "ALPINE_VERSION=$ALPINE_VERSION" \
  --build-arg "CORE_VERSION=$CORE_VERSION" \
  --target export --output "type=local,dest=./out" .

echo ">> done: ./out/sessionaffinity.so"
echo "   mount it with:  -v \"\$PWD/out/sessionaffinity.so\":/app/plugins/sessionaffinity.so:ro"
echo "   and reference path /app/plugins/sessionaffinity.so in config.json"
