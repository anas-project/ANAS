#!/usr/bin/env bash
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
writer=$script_dir/server-upgrade-write-report.sh
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/anas-upgrade-report-test.XXXXXX")

cleanup() {
  case "$work_dir" in
    "${TMPDIR:-/tmp}"/anas-upgrade-report-test.*|/tmp/anas-upgrade-report-test.*) rm -rf -- "$work_dir" ;;
    *) echo "refusing to remove unexpected path: $work_dir" >&2 ;;
  esac
}
trap cleanup EXIT HUP INT TERM

pass_dir=$work_dir/pass
"$writer" "$pass_dir" modules-base image-release/46-2 worktree passed complete \
  old-deployment new-deployment aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  true true true true true true true >/dev/null
jq -e '
  .schema_version == "anas.upgrade-e2e/v1" and
  .suite == "modules-base" and
  .status == "passed" and
  .failed_phase == null and
  .assertions == {
    old_stack_started: true,
    persisted_state_seeded: true,
    config_preserved: true,
    upgrade_verified: true,
    rollback_verified: true,
    reapply_verified: true,
    cleanup_completed: true
  }
' "$pass_dir/modules-base.json" >/dev/null
grep -Fq 'failures="0"' "$pass_dir/modules-base.junit.xml"
grep -Fq -- '- Cleanup completed: true' "$pass_dir/modules-base.md"

fail_dir=$work_dir/fail
"$writer" "$fail_dir" modules-app image-release/46-2 worktree failed verify-upgraded \
  old-deployment new-deployment aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa \
  true true true false false false true >/dev/null
jq -e '
  .status == "failed" and
  .failed_phase == "verify-upgraded" and
  .assertions.persisted_state_seeded == true and
  .assertions.upgrade_verified == false and
  .assertions.cleanup_completed == true
' "$fail_dir/modules-app.json" >/dev/null
grep -Fq 'failures="1"' "$fail_dir/modules-app.junit.xml"

for report in "$pass_dir"/* "$fail_dir"/*; do
  [[ "$(stat -f '%Lp' "$report" 2>/dev/null || stat -c '%a' "$report")" == 600 ]]
done

printf 'upgrade_report_test=pass formats=json,junit,markdown success_and_failure=true\n'
