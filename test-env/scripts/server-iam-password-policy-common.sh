#!/usr/bin/env bash

# Shared, destructive fixture helpers for the provider-specific password-policy
# E2E tests. Callers must run against an isolated deployment.

docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
dc=${ANAS_TEST_SAMBA_DC_CONTAINER:-${prefix}samba_dc}
policy_suffix=${ANAS_TEST_PASSWORD_POLICY_SUFFIX:-$(date +%H%M%S)}
policy_user=${ANAS_TEST_PASSWORD_POLICY_USER:-ipp${policy_suffix}}
policy_display_name="Password Policy E2E ${policy_suffix}"
policy_min_age=

dc_exec() {
  "$docker_cmd" exec "$dc" "$@"
}

strong_password() {
  local marker=$1 value
  # Deliberately omit the user suffix and display-name tokens so this fixture
  # exercises AD complexity without accidentally tripping the name rule.
  value="Qz7!Ocean-${marker}"
  while [ "${#value}" -lt "$policy_min_length" ]; do
    value="${value}X"
  done
  printf '%s' "$value"
}

short_password() {
  local target=$((policy_min_length - 1)) value='Aa1!'
  while [ "${#value}" -lt "$target" ]; do
    value="${value}x"
  done
  printf '%s' "${value:0:target}"
}

weak_password() {
  local value='onlylowercase'
  while [ "${#value}" -lt "$policy_min_length" ]; do
    value="${value}x"
  done
  printf '%s' "$value"
}

load_password_policy() {
  local settings expected_complexity
  policy_min_length=$(dc_exec printenv SAMBA_DC_USER_MIN_PASS_LENGTH)
  policy_history=$(dc_exec printenv SAMBA_DC_USER_PASSWORD_HISTORY)
  policy_min_age=$(dc_exec printenv SAMBA_DC_USER_MIN_PASS_AGE)
  policy_complex=$(dc_exec printenv SAMBA_DC_USER_COMPLEX_PASS)
  case "$policy_complex" in
    true) expected_complexity=on ;;
    false) expected_complexity=off ;;
    *) printf 'invalid SAMBA_DC_USER_COMPLEX_PASS=%s\n' "$policy_complex" >&2; return 1 ;;
  esac
  [ "$policy_complex" = true ]
  [ "$policy_min_length" -ge 8 ]
  [ "$policy_history" -gt 0 ]
  settings=$(dc_exec samba-tool domain passwordsettings show)
  printf '%s\n' "$settings" | grep -Fq "Password complexity: $expected_complexity"
  printf '%s\n' "$settings" | grep -Fq "Password history length: $policy_history"
  printf '%s\n' "$settings" | grep -Fq "Minimum password length: $policy_min_length"
  printf '%s\n' "$settings" | grep -Fq "Minimum password age (days): $policy_min_age"

  initial_password=$(strong_password initial)
  changed_password=$(strong_password changed)
  final_password=$(strong_password final)
  forced_password=$(strong_password forced)
  forced_final_password=$(strong_password forced-final)
  too_short_password=$(short_password)
  complexity_password=$(weak_password)
}

create_password_policy_user() {
  dc_exec samba-tool user delete "$policy_user" >/dev/null 2>&1 || true
  dc_exec samba-tool user add "$policy_user" "$initial_password" --userou='OU=People' >/dev/null
  dc_exec samba-tool user setexpiry "$policy_user" --noexpiry >/dev/null
  dc_exec samba-tool user rename "$policy_user" --display-name="$policy_display_name" >/dev/null
}

allow_rapid_password_changes() {
  # The normal test profile deliberately has a non-zero minimum age. Temporarily
  # turn it off so one disposable user can exercise success and history paths in
  # a single run; cleanup restores the exact deployed value.
  dc_exec samba-tool domain passwordsettings set --min-pwd-age=0 >/dev/null
}

cleanup_password_policy_fixture() {
  if [ -n "$policy_min_age" ]; then
    dc_exec samba-tool domain passwordsettings set --min-pwd-age="$policy_min_age" >/dev/null 2>&1 || true
  fi
  dc_exec samba-tool user delete "$policy_user" >/dev/null 2>&1 || true
}

prepare_password_policy_fixture() {
  "$docker_cmd" inspect "$dc" >/dev/null
  load_password_policy
  create_password_policy_user
}
