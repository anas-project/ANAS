#!/usr/bin/env bash
# Run a real source-to-source Module upgrade on an isolated Docker daemon.
set -euo pipefail
umask 077

if [[ $# -ne 8 ]]; then
  echo "usage: $0 <old-anas> <new-anas> <old-modules> <new-modules> <config> <seed> <verify> <workspace>" >&2
  exit 2
fi

. "$(dirname -- "$0")/server-require-upgrade-netns.sh"

old_anas=$(realpath "$1")
new_anas=$(realpath "$2")
old_modules=$(realpath "$3")
new_modules=$(realpath "$4")
config=$(realpath "$5")
targets=$(realpath "${config%.*}.targets")
seed=$(realpath "$6")
verify=$(realpath "$7")
workspace=$8
suite=${ANAS_UPGRADE_SUITE:-}
from_identity=${ANAS_UPGRADE_FROM:-}
to_identity=${ANAS_UPGRADE_TO:-}
current_build_ghcr_registry=${ANAS_UPGRADE_CURRENT_BUILD_GHCR_REGISTRY:-}
report_writer=$(realpath "$(dirname -- "$0")/server-upgrade-write-report.sh")
workspace_cleaner=$(realpath "$(dirname -- "$0")/server-upgrade-clean-workspace.sh")
old_compat=$(realpath "$(dirname -- "$0")/server-upgrade-old-compat.sh")

. "$(dirname -- "$0")/server-require-isolated-docker.sh"
. "$(dirname -- "$0")/server-require-upgrade-proxy-boundary.sh"

fail() { printf 'module upgrade E2E failed: %s\n' "$1" >&2; exit 1; }
old_helper=$(dirname -- "$old_anas")/anas-helper
new_helper=$(dirname -- "$new_anas")/anas-helper
for binary in "$old_anas" "$new_anas" "$old_helper" "$new_helper" "$seed" "$verify" "$report_writer" "$workspace_cleaner" "$old_compat"; do
  [[ -f "$binary" && ! -L "$binary" && -x "$binary" ]] || fail "$binary is not an executable regular file"
done
for directory in "$old_modules" "$new_modules"; do
  [[ -d "$directory" && ! -L "$directory" ]] || fail "$directory is not a Module directory"
done
[[ -f "$config" && ! -L "$config" ]] || fail "$config is not a regular config file"
[[ -f "$targets" && ! -L "$targets" ]] || fail "$targets is not a regular target inventory"
[[ "$workspace" = /* ]] || fail "workspace must be absolute"
case "$workspace" in
  /tmp/anas-upgrade-*|/srv/anas-upgrade-*|/data/anas-upgrade-*) ;;
  *) fail "workspace is not in an anas-upgrade test scope" ;;
esac
[[ ! -e "$workspace" ]] || fail "workspace already exists"
[[ "$suite" =~ ^modules-[a-z0-9-]+$ ]] || fail "ANAS_UPGRADE_SUITE must name a modules-* catalog suite"
[[ "$from_identity" =~ ^[A-Za-z0-9._/@+-]+$ ]] || fail "ANAS_UPGRADE_FROM must be a value-free source identity"
[[ "$to_identity" =~ ^[A-Za-z0-9._/@+-]+$ ]] || fail "ANAS_UPGRADE_TO must be a value-free source identity"
if [[ -n "$current_build_ghcr_registry" ]]; then
  [[ "$current_build_ghcr_registry" =~ ^[A-Za-z0-9]([A-Za-z0-9.-]*[A-Za-z0-9])?(:[1-9][0-9]{0,4})?$ ]] ||
    fail "ANAS_UPGRADE_CURRENT_BUILD_GHCR_REGISTRY must be a registry host with an optional port"
fi
report_dir=${workspace}.reports
[[ ! -e "$report_dir" ]] || fail "report directory already exists: $report_dir"
install -d -m 0700 "$report_dir"

phase=preflight
old_deployment=
new_deployment=
config_digest=
old_started=false
seeded=false
config_preserved=false
upgraded=false
rolled_back=false
reapplied=false
cleanup_completed=false
suite_modules=

finish() {
  exit_code=$?
  trap - EXIT HUP INT TERM
  set +e
  failed_phase=$phase
  runtime_cleanup=false
  old_compat_cleanup=false
  if [[ -d "$workspace" ]] && "$old_compat" "$old_modules" "$workspace" cleanup >/dev/null 2>&1; then
    old_compat_cleanup=true
  elif [[ ! -e "$workspace" ]]; then
    old_compat_cleanup=true
  fi
  if "$new_anas" stop -w "$workspace" >/dev/null 2>&1 ||
     "$old_anas" stop -w "$workspace" >/dev/null 2>&1 ||
     [[ "$old_started" == false ]]; then
    runtime_cleanup=true
  fi
  if [[ "${ANAS_UPGRADE_KEEP_WORKSPACE:-false}" != true ]]; then
    "$workspace_cleaner" "$new_anas" "$workspace"
  fi
  suite_modules_cleanup=false
  if [[ -z "$suite_modules" || ! -e "$suite_modules" ]]; then
    suite_modules_cleanup=true
  else
    new_source=$(dirname -- "$new_modules")
    case "$suite_modules" in
      "$new_source"/.anas-upgrade-suite-modules.*)
        rm -rf -- "$suite_modules"
        [[ ! -e "$suite_modules" ]] && suite_modules_cleanup=true
        ;;
    esac
  fi
  if [[ "$runtime_cleanup" == true && "$old_compat_cleanup" == true && "$suite_modules_cleanup" == true && ( "${ANAS_UPGRADE_KEEP_WORKSPACE:-false}" == true || ! -e "$workspace" ) ]]; then
    cleanup_completed=true
  fi
  if [[ "$cleanup_completed" != true && $exit_code -eq 0 ]]; then
    exit_code=1
    failed_phase=cleanup
  fi
  report_status=failed
  if [[ $exit_code -eq 0 ]]; then
    report_status=passed
  fi
  "$report_writer" "$report_dir" "$suite" "$from_identity" "$to_identity" \
    "$report_status" "$failed_phase" "$old_deployment" "$new_deployment" "$config_digest" \
    "$old_started" "$seeded" "$config_preserved" "$upgraded" "$rolled_back" "$reapplied" "$cleanup_completed"
  report_exit=$?
  if [[ $report_exit -ne 0 && $exit_code -eq 0 ]]; then
    exit_code=$report_exit
  fi
  exit "$exit_code"
}
trap finish EXIT
trap 'exit 130' HUP INT TERM

phase=prepare-suite-module-root
new_source=$(dirname -- "$new_modules")
[[ "$(basename -- "$new_modules")" == modules && -f "$new_source/go.mod" ]] ||
  fail "new Module root must be the modules directory of a Go source tree"
suite_modules=$(mktemp -d "$new_source/.anas-upgrade-suite-modules.XXXXXX")
cp -a "$old_modules/." "$suite_modules/"
target_count=0
target_names=
target_modules=()
while IFS= read -r target; do
  [[ -n "$target" && "$target" =~ ^[a-z][a-z0-9_]*$ ]] || fail "target inventory contains an invalid Module name"
  [[ -d "$old_modules/$target" && ! -L "$old_modules/$target" ]] || fail "old target Module is absent: $target"
  [[ -d "$new_modules/$target" && ! -L "$new_modules/$target" ]] || fail "new target Module is absent: $target"
  [[ " $target_names " != *" $target "* ]] || fail "target inventory repeats Module: $target"
  rm -rf -- "$suite_modules/$target"
  cp -a "$new_modules/$target" "$suite_modules/$target"
  target_names="${target_names:+$target_names,}$target"
  target_modules+=("$target")
  target_count=$((target_count + 1))
done <"$targets"
[[ $target_count -gt 0 ]] || fail "target inventory is empty"
install -m 0600 "$targets" "$report_dir/suite-targets.txt"
printf 'suite_module_targets=%s current_overlays=%s dependencies=old-release\n' "$target_count" "$target_names"

phase=old-init
"$old_anas" init "$workspace" -c "$config" --module-root "$old_modules" -y --json >"$report_dir/old-init.json"
phase=old-apply
# The old endpoint is an immutable release artifact. Rebuilding it here would
# resolve floating Dockerfile inputs again and could produce an image that was
# never released under the recorded version-rN identity.
old_apply_attempt=1
while true; do
  attempt_report="$report_dir/old-apply-attempt-${old_apply_attempt}.json"
  set +e
  "$old_anas" apply -w "$workspace" --module-root "$old_modules" --update-lock --json >"$attempt_report"
  old_apply_status=$?
  set -e
  install -m 0600 "$attempt_report" "$report_dir/old-apply.json"
  if [[ $old_apply_status -eq 0 ]]; then
    break
  fi
  old_apply_error=$(sed -n 's/.*"code": "\([^"]*\)".*/\1/p' "$attempt_report" | head -n 1)
  if [[ "$old_apply_error" != start_failed || $old_apply_attempt -ge 3 ]]; then
    exit "$old_apply_status"
  fi
  retry_report="$report_dir/old-start-retry-${old_apply_attempt}.txt"
  set +e
  "$old_compat" "$old_modules" "$workspace" prepare-old-start-retry >"$retry_report" 2>&1
  retry_status=$?
  set -e
  cat "$retry_report"
  if [[ $retry_status -ne 0 ]]; then
    exit "$old_apply_status"
  fi
  old_apply_attempt=$((old_apply_attempt + 1))
done
old_deployment=$(sed -n 's/^active_deployment: //p' "$workspace/.anas/state/active.yml")
[[ "$old_deployment" =~ ^[A-Za-z0-9._-]+$ ]] || fail "old version did not activate a valid deployment identity"
config_digest=$(sha256sum "$workspace/config.yml" | awk '{print $1}')
old_started=true

phase=prepare-old-compat
"$old_compat" "$old_modules" "$workspace" prepare-running-old | tee "$report_dir/old-compat-prepare.txt"
phase=seed
ANAS_UPGRADE_PHASE=seed ANAS_UPGRADE_WORKSPACE="$workspace" "$seed"
seeded=true

phase=retire-old-compat-before-upgrade
"$old_compat" "$old_modules" "$workspace" cleanup | tee "$report_dir/old-compat-retire-before-upgrade.txt"

phase=inspect-old-deployment
"$new_anas" deployments inspect "$old_deployment" -w "$workspace" --json >"$report_dir/inspect-old-deployment.json"
phase=new-build-targets
# `apply --build` builds the entire selected dependency closure. That would
# rebuild old, unassigned dependency artifacts from floating Dockerfile inputs
# and destroy the suite boundary. Build only the catalog-owned targets, then
# activate that sealed deployment without another build.
if [[ -n "$current_build_ghcr_registry" ]]; then
  printf 'current_build_registry_override=GHCR_REGISTRY scope=current-target-build registry=%s\n' \
    "$current_build_ghcr_registry" | tee "$report_dir/current-build-registry.txt"
  env "GHCR_REGISTRY=$current_build_ghcr_registry" \
    "$new_anas" build "${target_modules[@]}" -w "$workspace" --module-root "$suite_modules" --update-lock --json >"$report_dir/new-build-targets.json"
else
  printf '%s\n' 'current_build_registry_override=none' >"$report_dir/current-build-registry.txt"
  "$new_anas" build "${target_modules[@]}" -w "$workspace" --module-root "$suite_modules" --update-lock --json >"$report_dir/new-build-targets.json"
fi
new_deployment=$(sed -n 's/.*"deployment_id"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' "$report_dir/new-build-targets.json" | tail -n 1)
[[ "$new_deployment" =~ ^[A-Za-z0-9._-]+$ && "$new_deployment" != "$old_deployment" ]] || fail "target build did not create a valid new deployment identity"
phase=new-apply
"$new_anas" apply --deployment "$new_deployment" -w "$workspace" --allow-risky --json >"$report_dir/new-apply.json"
activated_deployment=$(sed -n 's/^active_deployment: //p' "$workspace/.anas/state/active.yml")
[[ "$activated_deployment" == "$new_deployment" ]] || fail "upgrade did not activate the target-built deployment"
[[ "$(sha256sum "$workspace/config.yml" | awk '{print $1}')" == "$config_digest" ]] || fail "upgrade changed managed config"
config_preserved=true
phase=verify-upgraded
ANAS_UPGRADE_PHASE=upgraded ANAS_UPGRADE_WORKSPACE="$workspace" "$verify"
upgraded=true

# Every current in-tree Module declares an explicit data_breaking boundary.
# A round trip proves that a supposedly compatible revision really remains
# readable by both artifacts and that the immutable old artifact stayed usable.
phase=prepare-old-compat-before-rollback
"$old_compat" "$old_modules" "$workspace" prepare-rollback | tee "$report_dir/old-compat-prepare-rollback.txt"
phase=rollback
"$new_anas" rollback "$old_deployment" -w "$workspace" --json >"$report_dir/rollback.json"
phase=verify-rolled-back
ANAS_UPGRADE_PHASE=rolled-back ANAS_UPGRADE_WORKSPACE="$workspace" "$verify"
rolled_back=true
phase=retire-old-compat-before-reapply
"$old_compat" "$old_modules" "$workspace" cleanup | tee "$report_dir/old-compat-retire-before-reapply.txt"
phase=reapply
# A successful rollback changes the formerly active deployment from `active`
# to `previous`. `apply --deployment` intentionally accepts only a sealed
# `ready` deployment, so use the deployment switch operation to reactivate the
# already-built current artifact. Although the CLI command is named rollback,
# the explicit target is the newer deployment and this is the forward half of
# the round trip.
"$new_anas" rollback "$new_deployment" -w "$workspace" --allow-risky --json >"$report_dir/reapply.json"
phase=verify-reapplied
ANAS_UPGRADE_PHASE=reapplied ANAS_UPGRADE_WORKSPACE="$workspace" "$verify"
reapplied=true
phase=complete

printf 'module_upgrade=pass old_deployment=%s new_deployment=%s config_preserved=true round_trip=true\n' \
  "$old_deployment" "$new_deployment"
