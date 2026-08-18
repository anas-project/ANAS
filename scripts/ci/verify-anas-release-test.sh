#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
verifier="$repo_root/scripts/ci/verify-anas-release.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT HUP INT TERM

version=1.2.3
commit=0123456789abcdef0123456789abcdef01234567
release_date=2026-08-18T12:34:56+00:00
archive_dir=anas_linux_amd64
stage="$fixture/stage"
verification_tmp="$fixture/tmp with spaces"
mkdir -p "$stage/$archive_dir" "$fixture/bin" "$verification_tmp"

write_anas() {
  local binary_version="$1" binary_commit="$2" binary_date="$3"
  cat >"$stage/$archive_dir/anas" <<EOF
#!/usr/bin/env sh
if [ "\${1:-}" = version ] && [ "\${2:-}" = --json ]; then
  printf '%s\\n' '{"api_version":"anas.dev/cli/v1","ok":true,"version":"$binary_version","commit":"$binary_commit","date":"$binary_date"}'
  exit 0
fi
exit 2
EOF
  chmod 0755 "$stage/$archive_dir/anas"
}

printf '#!/usr/bin/env sh\nexit 0\n' >"$stage/$archive_dir/anas-helper"
chmod 0755 "$stage/$archive_dir/anas-helper"
cat >"$fixture/bin/go" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == version && "$2" == -m && -n "${3:-}" ]]
case "${3##*/}" in
  anas) package=github.com/anas-project/ANAS/cmd/anas ;;
  anas-helper) package=github.com/anas-project/ANAS/cmd/anas-helper ;;
  *) exit 2 ;;
esac
cat <<METADATA
$3: go1.test
	path	$package
	build	-trimpath=true
	build	CGO_ENABLED=0
	build	GOOS=linux
	build	GOARCH=amd64
	build	vcs.revision=${ANAS_TEST_VCS_COMMIT}
	build	vcs.modified=${ANAS_TEST_VCS_MODIFIED:-false}
METADATA
EOF
chmod 0755 "$fixture/bin/go"

write_manifest() {
  local manifest_version="$1" architecture="$2"
  printf '{"api_version":"anas.release/v1","version":"%s","commit":"%s","build_date":"%s","os":"linux","architecture":"%s"}\n' \
    "$manifest_version" "$commit" "$release_date" "$architecture" >"$stage/$archive_dir/release.json"
}

build_archive() {
  tar -C "$stage" -czf "$fixture/release.tar.gz" "$archive_dir"
}

write_manifest "$version" amd64
write_anas "$version" "$commit" "$release_date"
build_archive
bash "$verifier" "$version" "$commit" "$release_date" amd64 "$fixture/release.tar.gz" --manifest-only
PATH="$fixture/bin:$PATH" TMPDIR="$verification_tmp" ANAS_TEST_VCS_COMMIT="$commit" \
  bash "$verifier" "$version" "$commit" "$release_date" amd64 "$fixture/release.tar.gz"

write_manifest 9.9.9 amd64
build_archive
if bash "$verifier" "$version" "$commit" "$release_date" amd64 "$fixture/release.tar.gz" --manifest-only >/dev/null 2>&1; then
  echo "verifier accepted a manifest with the wrong version" >&2
  exit 1
fi

write_manifest "$version" arm64
build_archive
if bash "$verifier" "$version" "$commit" "$release_date" amd64 "$fixture/release.tar.gz" --manifest-only >/dev/null 2>&1; then
  echo "verifier accepted a manifest with the wrong architecture" >&2
  exit 1
fi

write_manifest "$version" amd64
for field in version commit date; do
  binary_version="$version"
  binary_commit="$commit"
  binary_date="$release_date"
  case "$field" in
    version) binary_version=9.9.9 ;;
    commit) binary_commit=ffffffffffffffffffffffffffffffffffffffff ;;
    date) binary_date=2026-08-19T00:00:00Z ;;
  esac
  write_anas "$binary_version" "$binary_commit" "$binary_date"
  build_archive
  if PATH="$fixture/bin:$PATH" ANAS_TEST_VCS_COMMIT="$commit" \
    bash "$verifier" "$version" "$commit" "$release_date" amd64 "$fixture/release.tar.gz" >/dev/null 2>&1; then
    echo "verifier accepted binary with wrong self-reported $field" >&2
    exit 1
  fi
done

write_anas "$version" "$commit" "$release_date"
build_archive
if PATH="$fixture/bin:$PATH" ANAS_TEST_VCS_COMMIT=ffffffffffffffffffffffffffffffffffffffff \
  bash "$verifier" "$version" "$commit" "$release_date" amd64 "$fixture/release.tar.gz" >/dev/null 2>&1; then
  echo "verifier accepted wrong Go VCS revision" >&2
  exit 1
fi
if PATH="$fixture/bin:$PATH" ANAS_TEST_VCS_COMMIT="$commit" ANAS_TEST_VCS_MODIFIED=true \
  bash "$verifier" "$version" "$commit" "$release_date" amd64 "$fixture/release.tar.gz" >/dev/null 2>&1; then
  echo "verifier accepted a modified Go build" >&2
  exit 1
fi

echo "ANAS release artifact tests passed"
