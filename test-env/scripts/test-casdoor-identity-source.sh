#!/usr/bin/env bash
# Test the pinned server's configuration boundary, without a remote deployment.
set -euo pipefail
umask 077

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)
module_root=$repo_root/modules/casdoor/casdoor
source_url=$(sed -n 's/^ARG CASDOOR_SOURCE_URL=//p' "$module_root/Dockerfile")
source_sha=$(sed -n 's/^ARG CASDOOR_SOURCE_SHA256=//p' "$module_root/Dockerfile")
[[ "$source_url" == https://github.com/casdoor/casdoor/archive/*.tar.gz ]]
[[ "$source_sha" =~ ^[0-9a-f]{64}$ ]]
[[ $# -le 1 ]] || { printf 'usage: %s [pinned-source.tar.gz]\n' "$0" >&2; exit 2; }

probe_root=$(mktemp -d "${TMPDIR:-/tmp}/anas-casdoor-identity-probe.XXXXXX")
trap 'printf "Casdoor probe artifacts retained at %s\n" "$probe_root"' EXIT
archive=$probe_root/source.tar.gz
if [[ $# == 1 ]]; then
  cp -- "$1" "$archive"
else
  curl -fsSL --retry 3 "$source_url" -o "$archive"
fi
if command -v sha256sum >/dev/null 2>&1; then
  actual_sha=$(sha256sum "$archive" | awk '{print $1}')
else
  actual_sha=$(shasum -a 256 "$archive" | awk '{print $1}')
fi
[[ "$actual_sha" == "$source_sha" ]] || { echo 'Casdoor source archive checksum mismatch' >&2; exit 1; }
mkdir "$probe_root/source"
tar -xzf "$archive" --strip-components=1 -C "$probe_root/source"
cd "$probe_root/source"
for patch_file in "$module_root"/patches/000[1-4]-*.patch; do
  patch --batch -p1 <"$patch_file"
done
cp "$repo_root/test-env/fixtures/casdoor-identity-probe/identity_probe_test.go.in" object/anas_identity_configuration_probe_test.go
printf 'casdoor_identity_probe=source_functions counts_as_e2e=false source_sha256=%s\n' "$source_sha"
go test -count=1 -v -run '^TestANASIdentityConfigurationProbe$' ./object

# Apply the same subject/session changes as the runtime Dockerfile.
for patch_file in "$module_root"/patches/000[5-6]-*.patch; do
  patch --batch -p1 <"$patch_file"
done
cp "$repo_root/test-env/fixtures/casdoor-identity-probe/identity_candidate_test.go.in" object/anas_identity_configuration_probe_test.go
cp "$repo_root/test-env/fixtures/casdoor-identity-probe/revocation_test.go.in" object/anas_revocation_test.go
printf 'casdoor_identity_runtime=source_functions counts_as_e2e=false runtime_enabled=true\n'
go test -count=1 -v -run '^(TestANASDirectorySubjectCandidate|TestNewLogoutTokenClaims|TestCustomClaimsIncludeSessionID|TestANASDirectoryRevocationIsolation)$' ./object
