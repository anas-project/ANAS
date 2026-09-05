#!/usr/bin/env bash
# REQUIREMENTS: UPGRADE-R-028
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
runner=$script_dir/server-module-upgrade-e2e.sh

grep -Fq 'targets=$(realpath "${config%.*}.targets")' "$runner"
grep -Fq 'cp -a "$old_modules/." "$suite_modules/"' "$runner"
grep -Fq 'cp -a "$new_modules/$target" "$suite_modules/$target"' "$runner"
grep -Fq 'current_build_ghcr_registry=${ANAS_UPGRADE_CURRENT_BUILD_GHCR_REGISTRY:-}' "$runner"
grep -Fq 'ANAS_UPGRADE_CURRENT_BUILD_GHCR_REGISTRY must be a registry host with an optional port' "$runner"
grep -Fq 'env "GHCR_REGISTRY=$current_build_ghcr_registry"' "$runner"
grep -Fq 'scope=current-target-build' "$runner"
build_invocations=$(grep -Fc '"$new_anas" build "${target_modules[@]}"' "$runner")
[[ "$build_invocations" -eq 2 ]] || {
  echo "suite runner has $build_invocations current target build invocations, want mirrored and direct branches" >&2
  exit 1
}
grep -Fq '"$new_anas" apply --deployment "$new_deployment"' "$runner"
grep -Fq '"$new_anas" rollback "$new_deployment"' "$runner"
if grep -Fq '"$new_anas" apply --build' "$runner"; then
  echo "suite runner rebuilds unassigned dependency Modules" >&2
  exit 1
fi

printf '%s\n' 'upgrade_suite_targets_test=pass root=old-release overlays=catalog-targets builds=targets-only current-build-registry=scoped'
