#!/command/with-contenv bash

sleep 5

source /assets/functions/00-container
source /assets/functions/20-llng

if [ $LLNG_DB_TYPE == "postgres" ]; then
  db_config="DBI:pg:database=${LLNG_DB_NAME};host=${DB_HOST};port=${DB_POST}"
  browseable_db_config="Apache::Session::Browseable::PgJSON"
elif [ $LLNG_DB_TYPE == "mariadb" ]; then
  db_config="DBI:mysql:database=${LLNG_DB_NAME};host=${DB_HOST};port=${DB_POST}"
  browseable_db_config="Apache::Session::Browseable::MySQL"
fi

configure_lmConf

lemonldap_ng_cli_set="sudo -u $NGINX_USER /usr/share/lemonldap-ng/bin/lemonldap-ng-cli -yes 1 -force 1 set"
lemonldap_ng_cli_addkey="sudo -u $NGINX_USER /usr/share/lemonldap-ng/bin/lemonldap-ng-cli -yes 1 -force 1 addKey"
lemonldap_ng_cli_delkey="sudo -u $NGINX_USER /usr/share/lemonldap-ng/bin/lemonldap-ng-cli -yes 1 -force 1 delKey"

# SAML & OIDC
saml_private_key=$(printf '%b\n' "${LLNG_SAML_SERVICE_PRIVATE_KEY//\"/}")
saml_public_key=$(printf '%b\n' "${LLNG_SAML_SERVICE_PUBLIC_KEY//\"/}")
oidc_private_key=$(printf '%b\n' "${LLNG_OIDC_SERVICE_PRIVATE_KEY//\"/}")
oidc_public_key=$(printf '%b\n' "${LLNG_OIDC_SERVICE_PUBLIC_KEY//\"/}")
$lemonldap_ng_cli_set \
        samlServicePrivateKeySig "$saml_private_key" \
        samlServicePublicKeySig "$saml_public_key" \ 
        oidcServicePrivateKeySig "$oidc_private_key" \
        oidcServicePublicKeySig "$oidc_public_key" \ 
        oidcServiceKeyIdSig "$LLNG_OIDC_SERVICE_KEY_ID"
        # samlNameIDFormatMapEmail mail \
        # samlNameIDFormatMapX509 mail \
        # samlNameIDFormatMapKerberos uid \
        # samlNameIDFormatMapWindows uid \

$lemonldap_ng_cli_delkey \
        globalStorageOptions Directory \
        globalStorageOptions LockDirectory

traefik_ip=$( ping $TRAEFIK_HOSTNAME -c 1 | sed '1{s/[^(]*(//;s/).*//;q}' )
for app in $APPS_LIST; do
  name="APPS_LIST__${app^^}__NAME"
  uri="APPS_LIST__${app^^}__URI"
  logo="APPS_LIST__${app^^}__LOGO_NAME"
  desc="APPS_LIST__${app^^}__DESC"
  domain="APPS_LIST__${app^^}__DOMAIN"
  allow_groups_name="APPS_LIST__${app^^}__ALLOW_GROUPS"
  allow_groups="${!allow_groups_name}"

  if [ -n "$allow_groups" ]; then
    groups=($(echo "$allow_groups" | tr ',' ' '))
    for group in "${groups[@]}"; do
        app_function_call="inGroup('$group')"
        app_function_calls+=("$app_function_call")
    done
    groups_filter=$(IFS='|'; echo "${app_function_calls[*]}")
    groups_filter="$groups_filter | inGroup('$SAMBA_DC_ADMIN_GROUP_NAME')"
  else
    groups_filter="on"
  fi

  $lemonldap_ng_cli_addkey \
        applicationList/1apps/$app type application \
        applicationList/1apps/$app/options name "${!name}" \
        applicationList/1apps/$app/options description "${!desc}" \
        applicationList/1apps/$app/options tooltip "${!desc}" \
        applicationList/1apps/$app/options display "$groups_filter" \
        applicationList/1apps/$app/options logo "${!logo}" \
        applicationList/1apps/$app/options uri "${!uri}"

  set_host ${!domain} $traefik_ip
done

for app in $SMAL_SP_APPS; do
  metadata_url="SMAL_SP__${app^^}__METADATA_URL"
  waiting_url ${!metadata_url}

  $lemonldap_ng_cli_addkey \
        samlSPMetaDataXML/$app samlSPMetaDataXML "`curl ${!metadata_url}`"

  # TODO: suit every app
  $lemonldap_ng_cli_addkey \
        samlSPMetaDataOptions/$app samlSPMetaDataOptionsSignSLOMessage 1

  allow_groups_name="SMAL_SP__${app^^}__ALLOW_GROUPS"
  allow_groups="${!allow_groups_name}"

  if [ -n "$allow_groups" ]; then
    groups=($(echo "$allow_groups" | tr ',' ' '))
    for group in "${groups[@]}"; do
        saml_function_call="inGroup('$group')"
        saml_function_calls+=("$saml_function_call")
    done
    groups_filter=$(IFS='|'; echo "${saml_function_calls[*]}")
    groups_filter="$groups_filter | inGroup('$SAMBA_DC_ADMIN_GROUP_NAME')"
    $lemonldap_ng_cli_addkey \
        samlSPMetaDataOptions/$app samlSPMetaDataOptionsRule "$groups_filter"
  fi
        

  index=1
  continue_loop=true
  while [ "$continue_loop" = true ]; do
    var="SMAL_SP__${app^^}__ATTR$(printf "%02d" "$index")"
    value="${!var}"

    if [ -z "$value" ]; then
      continue_loop=false
    else
      IFS=',' read -r var attr mandatory <<< "$value"

      $lemonldap_ng_cli_addkey \
            samlSPMetaDataExportedAttributes/$app $var "$mandatory;$attr;;"

      index=$((index + 1))
    fi
  done
done

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
        
else 
  $lemonldap_ng_cli_delkey \
        'locationRules' $LLNG_TEST_DOMAIN \
        'applicationList/98admin' 'test_auth'
fi

sudo -u $NGINX_USER /usr/share/lemonldap-ng/bin/lemonldap-ng-cli update-cache
