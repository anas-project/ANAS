#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT HUP INT TERM

bash "$repo_root/scripts/ci/build-console-web.sh" "$work_dir/first.tar.gz"
bash "$repo_root/scripts/ci/build-console-web.sh" "$work_dir/second.tar.gz"
cmp "$work_dir/first.tar.gz" "$work_dir/second.tar.gz"
sha256sum "$work_dir/second.tar.gz"
