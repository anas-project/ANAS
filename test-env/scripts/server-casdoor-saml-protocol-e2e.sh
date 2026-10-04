#!/usr/bin/env bash
# Live signed SAML assertion fixture; does not certify Nextcloud's SAML session.
set -Eeuo pipefail
umask 077
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
source "$script_dir/server-require-isolated-docker.sh"
prefix=${ANAS_TEST_CONTAINER_PREFIX:?}
casdoor="${prefix}casdoor"
nextcloud="${prefix}nextcloud"
fixture="${prefix}casdoor_saml_registration"
fixture_bin=${CASDOOR_LOGOUT_FIXTURE_BIN:?}
workdir=$(mktemp -d)
configured=false
cleanup() {
  if [ "$configured" = true ]; then
    docker exec "$casdoor" cat /run/secrets/casdoor-break-glass-password |
      docker exec -i "$fixture" /fixture restore --backup /state/application-original.json >/dev/null 2>&1 || true
  fi
  docker rm -f "$fixture" >/dev/null 2>&1 || true
  rm -rf "$workdir"
}
trap cleanup EXIT HUP INT TERM
issuer=$(docker exec "$casdoor" printenv CASDOOR_DOMAIN_FULL)
client_id=$(docker exec "$casdoor" printenv ANAS_IAM_CLIENT__NEXTCLOUD__CLIENT_ID)
redirects=$(docker exec "$casdoor" printenv ANAS_IAM_CLIENT__NEXTCLOUD__REDIRECT_URIS)
backchannel=$(docker exec "$casdoor" printenv ANAS_IAM_CLIENT__NEXTCLOUD__OIDC_LOGOUT_URI)
nextcloud_url=$(docker exec "$nextcloud" printenv NEXTCLOUD_DOMAIN_FULL)
image=$(docker inspect --format '{{.Config.Image}}' "$casdoor")
docker exec "$nextcloud" printenv NEXTCLOUD_OIDC_CLIENT_SECRET > "$workdir/client-secret"
docker run -d --name "$fixture" --network "container:$casdoor" --user "$(id -u):$(id -g)" --entrypoint /fixture \
  -v "$fixture_bin:/fixture:ro" -v "$workdir:/state" \
  -e "CASDOOR_FIXTURE_ISSUER=$issuer" -e CASDOOR_FIXTURE_INTERNAL_ORIGIN=http://127.0.0.1:8000 \
  -e "CASDOOR_FIXTURE_CLIENT_ID=$client_id" -e CASDOOR_FIXTURE_CLIENT_SECRET_FILE=/state/client-secret \
  -e CASDOOR_FIXTURE_REDIRECT_URI=http://127.0.0.1:18081/callback \
  -e CASDOOR_FIXTURE_BACKCHANNEL_URI=http://127.0.0.1:18081/backchannel \
  -e "CASDOOR_FIXTURE_MANAGED_REDIRECT_URIS=$redirects" -e "CASDOOR_FIXTURE_MANAGED_BACKCHANNEL_URI=$backchannel" \
  "$image" serve >/dev/null
sleep 2
configured=true
export CASDOOR_SAML_FIXTURE_ENTITY_ID=urn:anas:e2e:casdoor:saml
export CASDOOR_SAML_FIXTURE_ACS_URL="$nextcloud_url/anas-saml-e2e/acs"
docker exec "$casdoor" cat /run/secrets/casdoor-break-glass-password |
  docker exec -i "$fixture" /fixture configure --backup /state/application-original.json \
    --saml-entity-id "$CASDOOR_SAML_FIXTURE_ENTITY_ID" --saml-acs-url "$CASDOOR_SAML_FIXTURE_ACS_URL"
export CASDOOR_SAML_PROTOCOL_FIXTURE=1
bash "$script_dir/server-casdoor-saml-e2e.sh"
