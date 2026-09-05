#!/usr/bin/env bash
# Write value-free JSON, JUnit, and Markdown evidence for one upgrade suite.
set -euo pipefail

if [[ $# -ne 16 ]]; then
  echo "usage: $0 <report-dir> <suite> <from> <to> <status> <phase> <old-deployment> <new-deployment> <config-digest> <old-started> <seeded> <config-preserved> <upgraded> <rolled-back> <reapplied> <cleanup>" >&2
  exit 2
fi

report_dir=$1
suite=$2
from_identity=$3
to_identity=$4
status=$5
phase=$6
old_deployment=$7
new_deployment=$8
config_digest=$9
old_started=${10}
seeded=${11}
config_preserved=${12}
upgraded=${13}
rolled_back=${14}
reapplied=${15}
cleanup=${16}

case "$suite" in
  modules-[a-z0-9-]*) ;;
  *) echo "invalid upgrade suite: $suite" >&2; exit 2 ;;
esac
[[ "$from_identity" =~ ^[A-Za-z0-9._/@+-]+$ ]] || { echo "invalid from identity" >&2; exit 2; }
[[ "$to_identity" =~ ^[A-Za-z0-9._/@+-]+$ ]] || { echo "invalid to identity" >&2; exit 2; }
case "$status" in passed|failed) ;; *) echo "invalid report status: $status" >&2; exit 2 ;; esac
case "$phase" in *[!a-z0-9-]*|"") echo "invalid report phase: $phase" >&2; exit 2 ;; esac
case "$old_deployment" in *[!A-Za-z0-9._-]*) echo "invalid old deployment identity" >&2; exit 2 ;; esac
case "$new_deployment" in *[!A-Za-z0-9._-]*) echo "invalid new deployment identity" >&2; exit 2 ;; esac
if [[ -n "$config_digest" && ! "$config_digest" =~ ^[a-f0-9]{64}$ ]]; then
  echo "invalid config digest" >&2
  exit 2
fi
for value in "$old_started" "$seeded" "$config_preserved" "$upgraded" "$rolled_back" "$reapplied" "$cleanup"; do
  case "$value" in true|false) ;; *) echo "invalid assertion value: $value" >&2; exit 2 ;; esac
done
command -v jq >/dev/null || { echo "jq is required to write upgrade reports" >&2; exit 2; }

[ ! -L "$report_dir" ] || { echo "report directory must not be a symlink" >&2; exit 2; }
install -d -m 0700 "$report_dir"
json_report=$report_dir/$suite.json
junit_report=$report_dir/$suite.junit.xml
markdown_report=$report_dir/$suite.md
timestamp=$(date -u '+%Y-%m-%dT%H:%M:%SZ')
docker_version=$(${DOCKER_CMD:-docker} info --format '{{.ServerVersion}}' 2>/dev/null || true)
docker_root=$(${DOCKER_CMD:-docker} info --format '{{.DockerRootDir}}' 2>/dev/null || true)

jq -n \
  --arg schema_version 'anas.upgrade-e2e/v1' \
  --arg suite "$suite" \
  --arg from "$from_identity" \
  --arg to "$to_identity" \
  --arg status "$status" \
  --arg failed_phase "$phase" \
  --arg old_deployment "$old_deployment" \
  --arg new_deployment "$new_deployment" \
  --arg config_sha256 "$config_digest" \
  --arg timestamp "$timestamp" \
  --arg architecture "$(uname -m)" \
  --arg kernel "$(uname -r)" \
  --arg docker_version "$docker_version" \
  --arg docker_root "$docker_root" \
  --arg network_namespace "${ANAS_UPGRADE_NETNS_PATH:-}" \
  --argjson old_stack_started "$old_started" \
  --argjson persisted_state_seeded "$seeded" \
  --argjson config_preserved "$config_preserved" \
  --argjson upgrade_verified "$upgraded" \
  --argjson rollback_verified "$rolled_back" \
  --argjson reapply_verified "$reapplied" \
  --argjson cleanup_completed "$cleanup" \
  '{
    schema_version: $schema_version,
    suite: $suite,
    from: $from,
    to: $to,
    status: $status,
    failed_phase: (if $status == "failed" then $failed_phase else null end),
    deployments: {old: $old_deployment, new: $new_deployment},
    config_sha256: $config_sha256,
    assertions: {
      old_stack_started: $old_stack_started,
      persisted_state_seeded: $persisted_state_seeded,
      config_preserved: $config_preserved,
      upgrade_verified: $upgrade_verified,
      rollback_verified: $rollback_verified,
      reapply_verified: $reapply_verified,
      cleanup_completed: $cleanup_completed
    },
    environment: {
      timestamp: $timestamp,
      architecture: $architecture,
      kernel: $kernel,
      docker_version: $docker_version,
      docker_root: $docker_root,
      network_namespace: $network_namespace
    }
  }' >"$json_report"

if [[ "$status" == passed ]]; then
  failure_element=
else
  failure_element="<failure message=\"upgrade failed during $phase\"/>"
fi
printf '%s\n' \
  '<?xml version="1.0" encoding="UTF-8"?>' \
  "<testsuite name=\"$suite\" tests=\"1\" failures=\"$([[ $status == passed ]] && echo 0 || echo 1)\">" \
  "  <testcase classname=\"anas.upgrade\" name=\"$suite\">$failure_element</testcase>" \
  '</testsuite>' >"$junit_report"

printf '%s\n' \
  "# Upgrade E2E: $suite" \
  '' \
  "- Status: $status" \
  "- From: $from_identity" \
  "- To: $to_identity" \
  "- Old deployment: ${old_deployment:-not-created}" \
  "- New deployment: ${new_deployment:-not-created}" \
  "- Config SHA-256: ${config_digest:-not-created}" \
  "- Old stack started: $old_started" \
  "- Persisted state seeded: $seeded" \
  "- Config preserved: $config_preserved" \
  "- Upgrade verified: $upgraded" \
  "- Rollback verified: $rolled_back" \
  "- Reapply verified: $reapplied" \
  "- Cleanup completed: $cleanup" \
  "- Failed phase: $([[ $status == failed ]] && echo "$phase" || echo none)" \
  "- Environment: $(uname -m), kernel $(uname -r), Docker ${docker_version:-unknown}" \
  "- Network namespace: ${ANAS_UPGRADE_NETNS_PATH:-unknown}" >"$markdown_report"

chmod 0600 "$json_report" "$junit_report" "$markdown_report"
printf 'upgrade_report_json=%s upgrade_report_junit=%s upgrade_report_markdown=%s\n' \
  "$json_report" "$junit_report" "$markdown_report"
