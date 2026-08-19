#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

check_fixture() (
  name=$1
  config=$2
  expected_plan=$3
  workspace=$(mktemp -d "$RUNTIME_DIR/server-domain-separation-$name-static.XXXXXX")
  trap 'rm -rf -- "$workspace"' EXIT HUP INT TERM

  make_workspace "$workspace" "$config"
  run_anas lock -w "$workspace" -c "$workspace/config.yml" >/dev/null
  plan=$(run_anas plan -w "$workspace" -c "$workspace/config.yml")
  printf '%s\n' "$plan" | grep -Fq "$expected_plan"
  printf 'server_domain_fixture=%s %s\n' "$name" "$expected_plan"
)

check_fixture \
  authentik-ad-zone \
  "$TEST_ENV_DIR/server-domain-separation-authentik-e2e.yml" \
  'module plan: samba_dc requested_mode=auto resolved_mode=ad_zone zone=test.example'

check_fixture \
  llng-separate-zone \
  "$TEST_ENV_DIR/server-domain-separation-llng-e2e.yml" \
  'module plan: samba_dc requested_mode=auto resolved_mode=separate_zone zone=apps.example.test'
