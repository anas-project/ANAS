#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 5 ]]; then
  echo "usage: $0 <version> <commit> <build-date> <amd64|arm64> <output-dir>" >&2
  exit 2
fi

version="$1"
commit="$2"
build_date="$3"
arch="$4"
output_dir="$5"

case "$arch" in
  amd64|arm64) ;;
  *)
    echo "unsupported ANAS release architecture: $arch" >&2
    exit 2
    ;;
esac

archive="anas_linux_${arch}"
mkdir -p "$output_dir"
output_dir="$(cd "$output_dir" && pwd)"
stage_dir="${output_dir}/${archive}"
mkdir -p "$stage_dir"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
  -trimpath \
  -ldflags "-s -w -X github.com/anas-project/ANAS/internal/buildinfo.Version=${version} -X github.com/anas-project/ANAS/internal/buildinfo.Commit=${commit} -X github.com/anas-project/ANAS/internal/buildinfo.Date=${build_date}" \
  -o "${stage_dir}/anas" \
  ./cmd/anas

# anas-helper ships beside anas because it is the one part that has to be
# installed root-owned and granted a capability. Building it here rather than
# separately keeps the two from drifting apart in a release.
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build \
  -trimpath \
  -ldflags "-s -w" \
  -o "${stage_dir}/anas-helper" \
  ./cmd/anas-helper

tar \
  --sort=name \
  --mtime='@0' \
  --owner=0 \
  --group=0 \
  --numeric-owner \
  -C "$output_dir" \
  -czf "${output_dir}/${archive}.tar.gz" \
  "$archive"
