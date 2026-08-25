#!/bin/bash

set -eo pipefail

var_true() {
  case "${1:-}" in
    1|true|TRUE|yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

waiting_url() {
  local url=$1
  local host=${2:-}
  local code
  while :; do
    if [ -n "$host" ]; then
      code=$(curl -ks -H "Host: $host" -o /dev/null -w '%{http_code}' "$url" || true)
    else
      code=$(curl -ks -o /dev/null -w '%{http_code}' "$url" || true)
    fi
    [ "$code" = 200 ] && return
    echo "Waiting for $url"
    sleep 3
  done
}

set_host() {
  printf '%s\t%s\n' "$2" "$1" >>/etc/hosts
}

waiting_url "http://127.0.0.1" "$LLNG_DOMAIN"
set_host "$LLNG_DOMAIN" 127.0.0.1
until [ -s /var/lib/lemonldap-ng/conf/lmConf-1.json ]; do sleep 2; done

lemonldap_ng_cli="/usr/share/lemonldap-ng/bin/lemonldap-ng-cli --user=www-data --group=www-data"
lemonldap_ng_cli_set="$lemonldap_ng_cli -yes 1 -force 1 set"
lemonldap_ng_cli_addkey="$lemonldap_ng_cli -yes 1 -force 1 addKey"
lemonldap_ng_cli_delkey="$lemonldap_ng_cli -yes 1 -force 1 delKey"

# Rebuild every ANAS RP/SP registry from the current runner contract. Clearing
# both protocols first is what removes stale LogoutUrl/SLS data after a
# declaration disappears, a domain changes, or a client switches protocol.
config_version=$( $lemonldap_ng_cli info | grep -oP 'Num\s+:\s+\K\d+' )

cat /var/lib/lemonldap-ng/conf/lmConf-$config_version.json \
  | jq 'del(.locationRules, .oidcRPMetaDataOptions, .oidcRPMetaDataExportedVars, .samlSPMetaDataXML, .samlSPMetaDataOptions, .samlSPMetaDataExportedAttributes, .applicationList."1apps")' \
  | jq --arg domain "$LLNG_MANAGER_DOMAIN" --arg group "$SAMBA_DC_ADMIN_GROUP_NAME" '. + {locationRules: {($domain): {default: "inGroup(\"\($group)\")"}}}' \
  > /tmp/config_new.json
mv /tmp/config_new.json /var/lib/lemonldap-ng/conf/lmConf-$config_version.json
chown -R www-data:www-data /var/lib/lemonldap-ng/conf

# manager & domain locationRules
$lemonldap_ng_cli_addkey \
      "locationRules/$LLNG_MANAGER_DOMAIN" 'default' "inGroup(\"$SAMBA_DC_ADMIN_GROUP_NAME\")" \
      "locationRules/$LLNG_DOMAIN" '(?#checkUser)^/checkuser' "inGroup(\"$SAMBA_DC_ADMIN_GROUP_NAME\")" \
      "locationRules/$LLNG_DOMAIN" 'default' 'accept'

# The upstream starter configuration publishes its documentation links to
# every authenticated user. Keep them available to administrators, but enforce
# the same Samba group boundary on both fresh installs and persisted configs
# upgraded from an earlier ANAS image.
documentation_rule="inGroup(\"$SAMBA_DC_ADMIN_GROUP_NAME\")"
$lemonldap_ng_cli_addkey \
      applicationList/99doc/localdoc/options display "$documentation_rule" \
      applicationList/99doc/officialwebsite/options display "$documentation_rule"

if var_true "${LLNG_ENABLE_TEST}" ; then
  $lemonldap_ng_cli_addkey \
        "locationRules/$LLNG_TEST_DOMAIN" 'default' 'accept' \
        "locationRules/$LLNG_TEST_DOMAIN" '^/logout' 'logout_sso' \
        "exportedHeaders/$LLNG_TEST_DOMAIN" 'Auth-User' '$uid' \
        "exportedHeaders/$LLNG_TEST_DOMAIN" 'Auth-Mail' '$mail' \
        "exportedHeaders/$LLNG_TEST_DOMAIN" 'Auth-Groups' '$groups'
  $lemonldap_ng_cli_addkey \
        applicationList/98admin/test_auth type application \
        applicationList/98admin/test_auth/options description "Test auth" \
        applicationList/98admin/test_auth/options display "auto" \
        applicationList/98admin/test_auth/options logo "network.png" \
        applicationList/98admin/test_auth/options name "Test auth server" \
        applicationList/98admin/test_auth/options uri "$LLNG_TEST_DOMAIN_FULL"

fi

# SAML & OIDC
saml_private_key=$(printf '%b\n' "${LLNG_SAML_SERVICE_PRIVATE_KEY//\"/}")
saml_public_key=$(printf '%b\n' "${LLNG_SAML_SERVICE_PUBLIC_KEY//\"/}")
oidc_private_key=$(printf '%b\n' "${LLNG_OIDC_SERVICE_PRIVATE_KEY//\"/}")
oidc_public_key=$(printf '%b\n' "${LLNG_OIDC_SERVICE_PUBLIC_KEY//\"/}")
$lemonldap_ng_cli_set \
        passwordPolicyActivation 1 \
        portalDisplayPasswordPolicy 1 \
        passwordPolicyMinSize "$SAMBA_DC_USER_MIN_PASS_LENGTH" \
        samlServicePrivateKeySig "$saml_private_key" \
        samlServicePublicKeySig "$saml_public_key" \
        oidcServicePrivateKeySig "$oidc_private_key" \
        oidcServicePublicKeySig "$oidc_public_key" \
        oidcServiceKeyIdSig "$LLNG_OIDC_SERVICE_KEY_ID" \
        issuerDBSAMLActivation 1 \
        issuerDBOpenIDConnectActivation 1

        # oidcServiceAllowImplicitFlow 1 \
        # oidcServiceAllowHybridFlow 1 \
        # samlNameIDFormatMapEmail mail \
        # samlNameIDFormatMapX509 mail \
        # samlNameIDFormatMapKerberos uid \
        # samlNameIDFormatMapWindows uid \

$lemonldap_ng_cli_delkey \
        globalStorageOptions Directory \
        globalStorageOptions LockDirectory

$lemonldap_ng_cli_addkey \
        applicationList/1apps catname "Applications" \
        applicationList/1apps type "category"

traefik_ip=$( ping -c 1 $TRAEFIK_HOSTNAME | sed '1{s/[^(]*(//;s/).*//;q}' )

IFS=','
for app in $APPS_LIST; do
  (
    IFS=' '
    echo "Configuring app_list $app"
    name="APPS_LIST__${app^^}__NAME"
    uri="APPS_LIST__${app^^}__URI"
    logo="APPS_LIST__${app^^}__LOGO_NAME"
    desc="APPS_LIST__${app^^}__DESC"
    allow_groups_name="APPS_LIST__${app^^}__ALLOW_GROUPS"
    allow_groups="${!allow_groups_name}"

    if [ -n "$allow_groups" ]; then
      groups=($(echo "$allow_groups" | tr ',' ' '))
      has_admin_group=false
      for group in "${groups[@]}"; do
          app_function_call="inGroup('$group')"
          app_function_calls+=("$app_function_call")
          if [ "$group" = "$SAMBA_DC_ADMIN_GROUP_NAME" ]; then
            has_admin_group=true
          fi
      done
      groups_filter=$(IFS='|'; echo "${app_function_calls[*]}")
      if [ "$has_admin_group" != true ]; then
        groups_filter="$groups_filter | inGroup('$SAMBA_DC_ADMIN_GROUP_NAME')"
      fi
    else
      groups_filter="on"
    fi

    $lemonldap_ng_cli_addkey \
          applicationList/1apps/$app type "application" \
          applicationList/1apps/$app/options name "${!name}" \
          applicationList/1apps/$app/options description "${!desc}" \
          applicationList/1apps/$app/options tooltip "${!desc}" \
          applicationList/1apps/$app/options display "$groups_filter" \
          applicationList/1apps/$app/options logo "${!logo}" \
          applicationList/1apps/$app/options uri "${!uri}"
  )
done

saml_get_var() { # $1 app name, $2 key name
  local app_name="$1"
  local key_name="$2"
  local name="SAML_SP__${app_name^^}__${key_name}"
  local var="${!name}"

  echo "$var"
}
IFS=','
for app in $SAML_SP_APPS; do
  (
    IFS=' '
    echo "Configuring saml sp $app"
    metadata_url=$(saml_get_var $app "METADATA_URL")
    domain=$(saml_get_var $app "DOMAIN")
    set_host "$domain" "$traefik_ip"
    waiting_url "$metadata_url"

    $lemonldap_ng_cli_addkey \
          samlSPMetaDataXML/$app samlSPMetaDataXML "$(curl "$metadata_url")"

    # TODO: suit every app
    $lemonldap_ng_cli_addkey \
          samlSPMetaDataOptions/$app samlSPMetaDataOptionsSignSLOMessage 1

    allow_groups=$(saml_get_var $app "ALLOW_GROUPS")

    if [ -n "$allow_groups" ]; then
      groups=($(echo "$allow_groups" | tr ',' ' '))
      has_admin_group=false
      for group in "${groups[@]}"; do
          saml_function_call="inGroup('$group')"
          saml_function_calls+=("$saml_function_call")
          if [ "$group" = "$SAMBA_DC_ADMIN_GROUP_NAME" ]; then
            has_admin_group=true
          fi
      done
      groups_filter=$(IFS='|'; echo "${saml_function_calls[*]}")
      if [ "$has_admin_group" != true ]; then
        groups_filter="$groups_filter | inGroup('$SAMBA_DC_ADMIN_GROUP_NAME')"
      fi
      $lemonldap_ng_cli_addkey \
          samlSPMetaDataOptions/$app samlSPMetaDataOptionsRule "$groups_filter"
    else 
      $lemonldap_ng_cli_addkey \
          samlSPMetaDataOptions/$app samlSPMetaDataOptionsRule ""
    fi

    index=1
    continue_loop=true
    while [ "$continue_loop" = true ]; do
      var="SAML_SP__${app^^}__ATTR$(printf "%02d" "$index")"
      value="${!var}"

      if [ -z "$value" ]; then
        continue_loop=false
      else
        IFS=',' read -r var attr mandatory <<< "$value"

        $lemonldap_ng_cli_addkey \
              samlSPMetaDataExportedAttributes/$app $var "$mandatory;$attr;;"

        if [ "$attr" != "groups" ]; then
          $lemonldap_ng_cli_addkey ldapExportedVars "$attr" "$attr"
        fi

        index=$((index + 1))
      fi
    done
  )
done

oidc_get_var() { # $1 app name, $2 key name
  local app_name="$1"
  local key_name="$2"
  local name="OIDC_RP__${app_name^^}__${key_name}"
  local var="${!name}"

  echo "$var"
}

comma_array_to_space() { # $1 comma array string
  IFS=',' read -ra the_array <<< "$1"
  space_string=$(IFS=' '; echo "${the_array[*]}")
  echo "$space_string"
}

IFS=','
for app in $OIDC_RP_APPS; do
  (
    IFS=' '
    echo "Configuring oidc rp $app"
    client_id=$(oidc_get_var $app "CLIENT_ID")
    client_secret=$(oidc_get_var $app "CLIENT_SECRET")

    redirect_uri=$(oidc_get_var $app "REDIRECT_URI")
    logout_redirect_uri=$(oidc_get_var $app "LOGOUT_REDIRECT_URI")
    logout_uri=$(oidc_get_var $app "LOGOUT_URI")
    logout_type=$(oidc_get_var $app "LOGOUT_TYPE")
    logout_session_required=$(oidc_get_var $app "LOGOUT_SESSION_REQUIRED")
    redirect_uri_space=$(comma_array_to_space "$redirect_uri")
    logout_redirect_uri_space=$(comma_array_to_space "$logout_redirect_uri")

    $lemonldap_ng_cli_addkey \
          oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsClientID "$client_id" \
          oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsClientSecret "$client_secret" \
          oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsIDTokenSignAlg RS256 \
          oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsIDTokenForceClaims 1 \
          oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsRedirectUris "$redirect_uri_space" \
          oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsPostLogoutRedirectUris "$logout_redirect_uri_space" \
          oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsBypassConsent 1 \
          oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsLogoutBypassConfirm 1

    if [ -n "$logout_uri" ]; then
      $lemonldap_ng_cli_addkey \
            oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsLogoutUrl "$logout_uri" \
            oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsLogoutType "$logout_type" \
            oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsLogoutSessionRequired "$logout_session_required"
    fi

    allow_groups=$(oidc_get_var $app "ALLOW_GROUPS")

    if [ -n "$allow_groups" ]; then
      groups=($(echo "$allow_groups" | tr ',' ' '))
      has_admin_group=false
      for group in "${groups[@]}"; do
          oidc_function_call="inGroup('$group')"
          oidc_function_calls+=("$oidc_function_call")
          if [ "$group" = "$SAMBA_DC_ADMIN_GROUP_NAME" ]; then
            has_admin_group=true
          fi
      done
      groups_filter=$(IFS='|'; echo "${oidc_function_calls[*]}")
      if [ "$has_admin_group" != true ]; then
        groups_filter="$groups_filter | inGroup('$SAMBA_DC_ADMIN_GROUP_NAME')"
      fi
      $lemonldap_ng_cli_addkey \
          oidcRPMetaDataOptions/$app oidcRPMetaDataOptionsRule "$groups_filter"
    fi
          

    index=1
    continue_loop=true
    while [ "$continue_loop" = true ]; do
      var="OIDC_RP__${app^^}__ATTR$(printf "%02d" "$index")"
      value="${!var}"

      if [ -z "$value" ]; then
        continue_loop=false
      else
        IFS=',' read -r var attr mandatory <<< "$value"

        oidc_attr="$attr"
        # OIDC consumers require a stable JSON type.  LLNG's default "auto"
        # mode emits a scalar when the user belongs to exactly one group and
        # an array for several groups.  Declare groups as an always-array
        # claim so every IAM consumer receives the same contract as it does
        # from Authentik.
        if [ "$attr" = "groups" ]; then
          oidc_attr="$attr;string;always"
        fi

        $lemonldap_ng_cli_addkey \
              oidcRPMetaDataExportedVars/$app $var "$oidc_attr"

        # RP output mappings reference LLNG session variables, not LDAP
        # attributes directly.  Make every directory-backed source attribute
        # declared by an application available in the session first.  `groups`
        # is computed by LLNG's group engine and must not be read as an LDAP
        # attribute.
        if [ "$attr" != "groups" ]; then
          $lemonldap_ng_cli_addkey ldapExportedVars "$attr" "$attr"
        fi

        index=$((index + 1))
      fi
    done

    domain=$(oidc_get_var $app "DOMAIN")
    set_host "$domain" "$traefik_ip"
  )
done

/usr/share/lemonldap-ng/bin/lemonldap-ng-cli --user=www-data --group=www-data update-cache
touch /run/llng-configured
