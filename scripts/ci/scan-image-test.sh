#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
scanner="$repo_root/scripts/ci/scan-image.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

# A docker stub that records its argv and returns whatever the test asks for.
cat >"$fixture/docker" <<'EOF'
#!/usr/bin/env bash
printf '%s\n' "$@" >>"$ANAS_TEST_ARGV"
exit "${ANAS_TEST_DOCKER_EXIT:-0}"
EOF
chmod 0755 "$fixture/docker"

run_scan() {
  : >"$fixture/argv"
  ANAS_DOCKER="$fixture/docker" \
    ANAS_TEST_ARGV="$fixture/argv" \
    ANAS_TRIVY_CACHE="$fixture/cache" \
    "$@"
}

# Findings must not fail the run: reporting mode is the whole point of the first
# rollout, and a scanner that blocks a publish gets removed rather than triaged.
ANAS_TEST_DOCKER_EXIT=1 run_scan bash "$scanner" ghcr.io/example/image:1 >"$fixture/out" 2>"$fixture/err"
grep -q 'reporting only, not failing the run' "$fixture/err"
grep -qx -- '--exit-code' "$fixture/argv"
grep -qx -- '0' "$fixture/argv"

# The scanner version is pinned, not floating: an unpinned scanner changes what
# it reports with no commit in this repository.
grep -qx 'aquasecurity/trivy:0.58.1' "$fixture/argv"

# Only actionable findings, and only vulnerabilities.
grep -qx -- '--ignore-unfixed' "$fixture/argv"
grep -qx -- 'HIGH,CRITICAL' "$fixture/argv"
grep -qx 'ghcr.io/example/image:1' "$fixture/argv"

# Enforcement is one environment variable away, and then a finding does fail.
if ANAS_TEST_DOCKER_EXIT=1 ANAS_SCAN_ENFORCE=true run_scan bash "$scanner" ghcr.io/example/image:1 >/dev/null 2>"$fixture/err"; then
  echo "ANAS_SCAN_ENFORCE=true did not fail on a finding" >&2
  exit 1
fi
grep -q 'vulnerability scan failed' "$fixture/err"
grep -qx -- '1' "$fixture/argv"

# A clean scan succeeds in both modes.
run_scan bash "$scanner" ghcr.io/example/image:1 >/dev/null
ANAS_SCAN_ENFORCE=true run_scan bash "$scanner" ghcr.io/example/image:1 >/dev/null

# Usage errors are the one thing that must fail loudly: a missing reference means
# the workflow wiring is wrong, and silently scanning nothing would look green.
for args in "" "a b"; do
  # shellcheck disable=SC2086
  if run_scan bash "$scanner" $args >/dev/null 2>&1; then
    echo "scan-image.sh accepted a bad argument list: [$args]" >&2
    exit 1
  fi
done
if run_scan bash "$scanner" "" >/dev/null 2>&1; then
  echo "scan-image.sh accepted an empty image reference" >&2
  exit 1
fi

bash -n "$scanner"
echo "container image scan tests passed"
