#!/usr/bin/env bash
set -euo pipefail

docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-anas_anchor_}
provider=${ANAS_TEST_IAM_PROVIDER:?ANAS_TEST_IAM_PROVIDER must be authentik or llng}
apps=${ANAS_TEST_IAM_APPS:-nextcloud,meshcentral,netbird}

case "$provider" in
  authentik|llng) ;;
  *) printf 'unsupported IAM provider: %s\n' "$provider" >&2; exit 2 ;;
esac

iam_container="${prefix}${provider}"
dc="${prefix}samba_dc"

container_env() {
  local attempt value
  for attempt in $(seq 1 30); do
    if value=$("$docker_cmd" exec "$1" printenv "$2" 2>/dev/null); then
      printf '%s\n' "$value"
      return 0
    fi
    sleep 2
  done
  printf 'unable to read %s from running container %s\n' "$2" "$1" >&2
  return 1
}

csv_has() {
  [[ ",$1," == *",$2,"* ]]
}

identity_apps=$(container_env "$dc" ANAS_IDENTITY_APP_CLIENTS)
admin_group=$(container_env "$dc" SAMBA_DC_ADMIN_GROUP_NAME)
test "$admin_group" = Admins

IFS=',' read -r -a app_list <<<"$apps"
for app in "${app_list[@]}"; do
  upper=${app^^}
  expected="APP_${app},APP_all,${admin_group}"
  csv_has "$identity_apps" "$app"
  test "$(container_env "$iam_container" "ANAS_IAM_CLIENT__${upper}__ALLOW_GROUPS")" = "$expected"
  test "$(container_env "$iam_container" "APPS_LIST__${upper}__ALLOW_GROUPS")" = "$expected"
  test "$(container_env "$iam_container" "ANAS_IAM_BINDING__${upper}__INTERFACE")" = oidc
  test -n "$(container_env "$iam_container" "ANAS_IAM_CLIENT__${upper}__CLIENT_ID")"
  test -n "$(container_env "$iam_container" "ANAS_IAM_BINDING__${upper}__OIDC_DISCOVERY_URL")"
  if [ "$provider" = llng ]; then
    test "$(container_env "$iam_container" "OIDC_RP__${upper}__ALLOW_GROUPS")" = "$expected"
    test "$(container_env "$iam_container" "OIDC_RP__${upper}__CLIENT_ID")" = \
      "$(container_env "$iam_container" "ANAS_IAM_CLIENT__${upper}__CLIENT_ID")"
  fi
  printf 'iam_contract app=%s provider=%s allow_groups=%s interface=oidc\n' \
    "$app" "$provider" "$expected"
done

case "$provider" in
  authentik)
    "$docker_cmd" exec "$iam_container" ak shell -c \
      "from authentik.core.models import Application; from authentik.policies.models import PolicyBinding; apps='$apps'.split(','); assert all(Application.objects.filter(slug=a).exists() for a in apps); assert all(PolicyBinding.objects.filter(target=Application.objects.get(slug=a)).exists() for a in apps)"
    ;;
  llng)
    # The generated LLNG configuration, rather than only its input env, must
    # contain an RP rule for every application.
    for app in "${app_list[@]}"; do
      "$docker_cmd" exec "$iam_container" sh -lc \
        'file=$(find /var/lib/lemonldap-ng/conf -maxdepth 1 -name "lmConf-*.json" | sort -V | tail -n 1); jq -r --arg app "$1" ".oidcRPMetaDataOptions[\$app].oidcRPMetaDataOptionsRule // empty" "$file"' \
        iam-contract "$app" | grep -Fq "inGroup('APP_$app')"
    done
    ;;
esac

test "$(container_env "${prefix}nextcloud" NEXTCLOUD_IAM_PROTOCOL)" = oidc
test "$(container_env "${prefix}nextcloud" NEXTCLOUD_OIDC_DISCOVERY_URL)" = \
  "$(container_env "$iam_container" ANAS_IAM_BINDING__NEXTCLOUD__OIDC_DISCOVERY_URL)"
test "$(container_env "${prefix}meshcentral" MESHCENTRAL_IAM_PROTOCOL)" = oidc
test "$(container_env "${prefix}meshcentral" MESHCENTRAL_OIDC_DISCOVERY_URL)" = \
  "$(container_env "$iam_container" ANAS_IAM_BINDING__MESHCENTRAL__OIDC_DISCOVERY_URL)"
test "$(container_env "${prefix}netbird_management" AUTH_CLIENT_ID)" = \
  "$(container_env "$iam_container" ANAS_IAM_CLIENT__NETBIRD__CLIENT_ID)"

printf 'PASS: provider=%s generic registrations, provider translation, and app runtime bindings match\n' "$provider"
