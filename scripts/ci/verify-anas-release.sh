#!/usr/bin/env bash
set -euo pipefail

if [[ $# -lt 5 || $# -gt 6 ]]; then
  echo "usage: $0 <version> <commit> <release-date> <amd64|arm64> <archive> [--manifest-only]" >&2
  exit 2
fi

version="$1"
commit="$2"
release_date="$3"
arch="$4"
archive="$5"
mode="${6:-full}"
[[ "$mode" == full || "$mode" == --manifest-only ]] || {
  echo "unknown verification mode: $mode" >&2
  exit 2
}
[[ "$arch" == amd64 || "$arch" == arm64 ]] || {
  echo "unsupported ANAS release architecture: $arch" >&2
  exit 2
}
[[ -f "$archive" ]] || {
  echo "ANAS release archive does not exist: $archive" >&2
  exit 2
}
for command_name in tar jq; do
  command -v "$command_name" >/dev/null 2>&1 || {
    echo "$command_name is required" >&2
    exit 2
  }
done

work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
archive_dir="anas_linux_${arch}"
tar -xzf "$archive" -C "$work_dir" \
  "$archive_dir/release.json" \
  "$archive_dir/anas" \
  "$archive_dir/anasd" \
  "$archive_dir/anas-helper" \
  "$archive_dir/anasd.service" \
  "$archive_dir/anasd.yml"

manifest="$work_dir/$archive_dir/release.json"
jq -e \
  --arg version "$version" \
  --arg commit "$commit" \
  --arg release_date "$release_date" \
  --arg arch "$arch" '
    type == "object" and
    .api_version == "anas.release/v1" and
    .version == $version and
    .commit == $commit and
    .build_date == $release_date and
    .os == "linux" and
    .architecture == $arch and
    (keys | sort) == (["api_version", "architecture", "build_date", "commit", "os", "version"] | sort)
  ' "$manifest" >/dev/null

if [[ "$mode" == --manifest-only ]]; then
  exit 0
fi
command -v go >/dev/null 2>&1 || {
  echo "go is required for binary metadata verification" >&2
  exit 2
}

verify_go_metadata() {
  local binary="$1" package_path="$2" metadata
  metadata="$(go version -m "$binary")"
  grep -Fq $'\tpath\t'"$package_path" <<<"$metadata"
  grep -Fq $'\tbuild\t-trimpath=true' <<<"$metadata"
  grep -Fq $'\tbuild\tCGO_ENABLED=0' <<<"$metadata"
  grep -Fq $'\tbuild\tGOOS=linux' <<<"$metadata"
  grep -Fq $'\tbuild\tGOARCH='"$arch" <<<"$metadata"
  grep -Fq $'\tbuild\tvcs.revision='"$commit" <<<"$metadata"
  grep -Fq $'\tbuild\tvcs.modified=false' <<<"$metadata"
}

binary="$work_dir/$archive_dir/anas"
daemon="$work_dir/$archive_dir/anasd"
helper="$work_dir/$archive_dir/anas-helper"
verify_go_metadata "$binary" github.com/anas-project/ANAS/cmd/anas
verify_go_metadata "$daemon" github.com/anas-project/ANAS/cmd/anasd
verify_go_metadata "$helper" github.com/anas-project/ANAS/cmd/anas-helper

grep -Fq 'User=root' "$work_dir/$archive_dir/anasd.service"
grep -Fq 'Group=root' "$work_dir/$archive_dir/anasd.service"
grep -Fq 'ProtectSystem=strict' "$work_dir/$archive_dir/anasd.service"
grep -Fq 'ReadWritePaths=' "$work_dir/$archive_dir/anasd.service"
grep -Fq 'api_version: anas.console-config/v1' "$work_dir/$archive_dir/anasd.yml"

reported="$("$binary" version --json)"
jq -e \
  --arg version "$version" \
  --arg commit "$commit" \
  --arg release_date "$release_date" '
    .api_version == "anas.dev/cli/v1" and
    .ok == true and
    .version == $version and
    .commit == $commit and
    .date == $release_date
  ' <<<"$reported" >/dev/null
