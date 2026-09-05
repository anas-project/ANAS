#!/usr/bin/env bash
# REQUIREMENTS: UPGRADE-R-027
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
helper=$script_dir/server-upgrade-old-compat.sh
runner=$script_dir/server-module-upgrade-e2e.sh

grep -Fq '[[ "$version" != 4.23.6 || "$revision" != 5 ]]' "$helper"
grep -Fq 'prepare-old-start-retry' "$helper"
grep -Fq 'release=authentik-2026.5.6-r8' "$helper"
grep -Fq 'old_apply_attempt -ge 3' "$runner"
grep -Fq '"$old_apply_error" != start_failed' "$runner"
grep -Fq 'reason=samba-fs-not-selected' "$helper"
grep -Fq 'ip neigh replace proxy "$transport" dev "$bridge"' "$helper"
grep -Fq 'ip neigh del proxy "$transport" dev "$bridge"' "$helper"
grep -Fq 'previous_proxy_arp' "$helper"
grep -Fq '127.0.0.1 "$zone" "$record_name" A -P' "$helper"
grep -Fq 'old_artifact_unchanged=true' "$helper"
grep -Fq 'prepare-running-old' "$runner"
grep -Fq 'prepare-rollback' "$runner"
grep -Fq 'retire-old-compat-before-upgrade' "$runner"
grep -Fq 'retire-old-compat-before-reapply' "$runner"

if grep -Eq 'docker[[:space:]]+(system[[:space:]]+)?prune|docker[[:space:]]+rm[[:space:]]+-f[[:space:]]+\$|rm[[:space:]]+-rf[[:space:]]+/(srv|data)(/|[[:space:]])' "$helper"; then
  echo "historical compatibility helper contains an unscoped destructive action" >&2
  exit 1
fi

printf '%s\n' 'upgrade_old_compat_test=pass release=exact lifecycle=retired current=unassisted'
