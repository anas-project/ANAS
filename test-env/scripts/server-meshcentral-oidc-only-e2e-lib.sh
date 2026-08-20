#!/usr/bin/env bash

# Shared browser-facing assertions for every IAM provider's MeshCentral E2E.
# The caller supplies its curl wrapper so provider-specific DNS and cookie
# handling remain in the owning test.
verify_meshcentral_oidc_only() {
  local curl_command=$1 meshcentral_url=$2 cookie_jar=$3 headers=$4 body=$5
  local location status
  rm -f "$cookie_jar" "$headers" "$body"

  status=$("$curl_command" -D "$headers" -o "$body" -w '%{http_code}' "$meshcentral_url/")
  test "$status" = 302
  location=$(awk 'tolower($1) == "location:" { print $2 }' "$headers" | tail -n 1 | tr -d '\r')
  case "$location" in
    /auth-oidc|"$meshcentral_url/auth-oidc") ;;
    *) printf 'MeshCentral root did not redirect to OIDC: %s\n' "$location" >&2; return 1 ;;
  esac

  status=$("$curl_command" -o "$body" -w '%{http_code}' "$meshcentral_url/login")
  test "$status" = 200
  grep -Fq "var passlogin = 'false';" "$body"
  grep -Fq "var authStrategies = 'oidc'.split(',');" "$body"

  status=$("$curl_command" -o "$body" -w '%{http_code}' \
    -H 'Content-Type: application/x-www-form-urlencoded' \
    --data 'action=login&username=anas-oidc-only-probe&password=not-a-password' \
    "$meshcentral_url/login")
  test "$status" = 404
  printf 'meshcentral_authentication_mode=oidc-only password_login_status=%s\n' "$status"
}
