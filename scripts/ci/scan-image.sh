#!/usr/bin/env bash
# Reports known vulnerabilities in a published container image.
#
# Reporting only: a finding never fails the run. A scanner that blocks merges
# from its first day gets switched off rather than triaged, and nobody yet knows
# the false-positive rate on these images -- most of them are upstream bases this
# repository pins but does not build. Turn ANAS_SCAN_ENFORCE=true on once that
# rate is known, which is the whole change needed to make it a gate.
#
# Trivy runs from a pinned image rather than a floating tag: an unpinned scanner
# changes what it reports with no commit here, so a newly red run could not be
# traced to a change in this repository.
set -euo pipefail

trivy_image="${ANAS_TRIVY_IMAGE:-aquasecurity/trivy:0.58.1}"
severity="${ANAS_SCAN_SEVERITY:-HIGH,CRITICAL}"
enforce="${ANAS_SCAN_ENFORCE:-false}"

if [[ "$#" != 1 ]]; then
  echo "usage: $0 <image-reference>" >&2
  exit 2
fi
reference="$1"
if [[ -z "$reference" ]]; then
  echo "an image reference is required" >&2
  exit 2
fi

docker_bin="${ANAS_DOCKER:-docker}"

# --ignore-unfixed keeps the report to findings someone can act on. An advisory
# with no fixed version upstream is not a decision this pipeline can make, and
# burying the actionable ones under it is how a report stops being read.
args=(
  image
  --severity "$severity"
  --ignore-unfixed
  --scanners vuln
  --format table
  --no-progress
)
if [[ "$enforce" == "true" ]]; then
  args+=(--exit-code 1)
else
  args+=(--exit-code 0)
fi
args+=("$reference")

printf '::group::vulnerability scan %s\n' "$reference"
status=0
"$docker_bin" run --rm \
  -v /var/run/docker.sock:/var/run/docker.sock \
  -v "${ANAS_TRIVY_CACHE:-$HOME/.cache/trivy}:/root/.cache/trivy" \
  "$trivy_image" "${args[@]}" || status=$?
printf '::endgroup::\n'

if [[ "$status" != 0 ]]; then
  if [[ "$enforce" == "true" ]]; then
    echo "vulnerability scan failed for $reference" >&2
    exit "$status"
  fi
  # Reporting mode swallows scanner failures too, including a database download
  # that did not complete. Those are real, they are just not this pipeline's
  # problem while the scan is advisory -- and letting them fail the publish
  # would make the release depend on an external service.
  echo "vulnerability scan for $reference exited $status; reporting only, not failing the run" >&2
fi
exit 0
