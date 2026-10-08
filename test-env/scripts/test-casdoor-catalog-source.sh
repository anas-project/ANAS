#!/usr/bin/env bash
# Exercise the pinned Casdoor implementation in an isolated source tree/SQLite DB.
set -euo pipefail
umask 077
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)
module_root=$repo_root/modules/casdoor/casdoor
source_url=$(sed -n 's/^ARG CASDOOR_SOURCE_URL=//p' "$module_root/Dockerfile")
source_sha=$(sed -n 's/^ARG CASDOOR_SOURCE_SHA256=//p' "$module_root/Dockerfile")
[[ $# -le 1 ]] || { echo 'usage: test-casdoor-catalog-source.sh [pinned-source.tar.gz]' >&2; exit 2; }
[[ "$source_url" == https://github.com/casdoor/casdoor/archive/*.tar.gz && "$source_sha" =~ ^[0-9a-f]{64}$ ]]
probe_root=$(mktemp -d "${TMPDIR:-/tmp}/anas-casdoor-catalog.XXXXXX")
trap 'printf "catalog_probe_artifacts=%s\n" "$probe_root"' EXIT
if [[ $# == 1 ]]; then cp -- "$1" "$probe_root/source.tar.gz"; else curl -fsSL --retry 3 "$source_url" -o "$probe_root/source.tar.gz"; fi
actual_sha=$(shasum -a 256 "$probe_root/source.tar.gz" | awk '{print $1}')
[[ "$actual_sha" == "$source_sha" ]] || { echo 'source checksum mismatch' >&2; exit 1; }
mkdir "$probe_root/source"
tar -xzf "$probe_root/source.tar.gz" --strip-components=1 -C "$probe_root/source"
cd "$probe_root/source"
for patch_file in "$module_root"/patches/000[1-6]-*.patch; do
  shasum -a 256 "$patch_file"
  patch --batch -p1 < "$patch_file"
done
cp "$repo_root/test-env/fixtures/casdoor-catalog-probe/catalog_test.go.in" object/anas_catalog_probe_test.go
printf 'source_sha256=%s counts_as_e2e=false\n' "$source_sha"
go test -count=1 -v -run '^TestANASCatalogProbe$' ./object
