#!/usr/bin/with-contenv bash

runas_user() {
  yasu nextcloud:nextcloud "$@"
}

if [ "$SIDECAR_CRON" = "1" ] || [ "$SIDECAR_PREVIEWGEN" = "1" ] || [ "$SIDECAR_NEWSUPDATER" = "1" ]; then
  exit 0
fi

if [ "$NEXTCLOUD_RM_SKELETON_FILES" == "true" ]; then 
  rm -rf /var/www/core/skeleton/*
  mkdir /var/www/core/skeleton/Documents
  mkdir /var/www/core/skeleton/Photos
fi

user_app_path='/var/www/userapps'
install_and_enable_app() { # $1 app name
  if ! [ -d "$user_app_path/$1" ]; then
      occ app:install $1
  fi
  if [ "$SKIP_UPDATE" != 1 ]; then
    occ app:update $1
  fi
  if [ "$(occ config:app:get $1 enabled)" != "yes" ]; then
    occ app:enable $1
    if [ "$(occ config:app:get $1 enabled)" != "yes" ]; then\
      echo -e "Error\n$1 app enable failed"
    fi
  fi
}

disable_app() { # $1 app name
  if [ -d "$user_app_path/$1" ]; then
    occ app:disable $1
  fi
}

import_occ() { # $1 json string
  echo "occ config:import $1"
  echo "$1" | occ config:import
}

echo "Config setting"

config_system='{}'
# default_phone_region
# occ config:system:set default_phone_region --value=$NEXTCLOUD_PHONE_REGION

# default_language
# echo "Set default_language => $DEFAULT_LANGUAGE"
# occ config:system:set default_language --value=$DEFAULT_LANGUAGE

# config domain
echo "Set https $NEXTCLOUD_DOMAIN_FULL"
config_system=$(cat <<EOF
{
  "system": {
    "default_phone_region": "$NEXTCLOUD_PHONE_REGION",
    "overwriteprotocol": "https",
    "trusted_domains": [
      "$NEXTCLOUD_DOMAIN_PORT"
    ],
    "overwrite.cli.url": "$NEXTCLOUD_DOMAIN_FULL",
    "overwritehost": "$NEXTCLOUD_DOMAIN_PORT",
    "allow_local_remote_servers": true,
    "log_rotate_size": 10485760
  }
}
EOF
)

# Set log level
occ log:manage --level $NEXTCLOUD_LOG_LEVEL

import_occ "$config_system"

# cron
echo "Set occ background:cron"
occ background:cron

# password policy
# if [ "$NEXTCLOUD_USER_COMPLEX_PASS" == 'true' ]; then
#   occ config:app:set password_policy enforceNumericCharacters --value=1
#   occ config:app:set password_policy enforceSpecialCharacters --value=0
#   occ config:app:set password_policy enforceUpperLowerCase --value=1
# else
#   occ config:app:set password_policy enforceNumericCharacters --value=0
#   occ config:app:set password_policy enforceSpecialCharacters --value=0
#   occ config:app:set password_policy enforceUpperLowerCase --value=0
# fi

# occ config:app:set password_policy expiration --value=$NEXTCLOUD_USER_MAX_PASS_AGE
# occ config:app:set password_policy minLength --value=$NEXTCLOUD_USER_MIN_PASS_LENGTH

# trusted_proxies
occ config:system:set trusted_proxies 0 --value=`ping $TRAEFIK_HOSTNAME -c 1 | sed '1{s/[^(]*(//;s/).*//;q}'`

# install apps
# user_app_path='/var/www/userapps'
# install_app() { # $1 filename, $2 app name
  # tar -xzf /root/$1 -C $user_app_path/$2
#   occ app:install $2
# }
# install_app "ldap_write_support-1.8.0.tar.gz" "ldap_write_support"

# collectives notes memories deck tasks ncdownloader news maps passwords forms groupfolders calendar impersonate polls tables bookmarks 
# files_markdown camerarawpreviews files_pdfviewer previewgenerator files_lock files_retention quota_warning files_texteditor 
# files_accesscontrol
# files_automatedtagging flow_notifications drawio workflow_script unsplash approval
# Mastodon Jira OpenProject Mattermost Jitsi 
echo "Install apps"

echo "Install collabora office"
if [ -n "$COLLABORA_DOMAIN_FULL" ]; then
  app_name='richdocuments'
  install_and_enable_app $app_name
  richdocuments_config='{}'
  richdocuments_config=`echo $richdocuments_config | jq ".apps.$app_name = { doc_format: \"ooxml\"}" `
  richdocuments_config=`echo $richdocuments_config | jq ".apps.$app_name = { public_wopi_url: \"$COLLABORA_DOMAIN_FULL\"}" `
  richdocuments_config=`echo $richdocuments_config | jq ".apps.$app_name = { wopi_url: \"$COLLABORA_DOMAIN_FULL\"}" `

  collabora_ipv4=`ping $COLLABORA_HOSTNAME -c 1 | sed '1{s/[^(]*(//;s/).*//;q}'`
  # TODO: ipv6
  richdocuments_config=`echo $richdocuments_config | jq ".apps.$app_name = { wopi_allowlist: \"$collabora_ipv4\"}" `
  import_occ "$richdocuments_config"
  occ richdocuments:activate-config
else
  disable_app 'richdocuments'
fi

echo "Install talk"
if [ "$NEXTCLOUD_TALK_ENABLED" == "true" ]; then
  app_name='spreed'
  install_and_enable_app $app_name
  talk_config='{}'
  occ config:app:set spreed stun_servers --value "[]"
  occ talk:stun:add "$NEXTCLOUD_TALK_TURN_DOMAIN_PORT"
  occ config:app:set spreed turn_servers --value "[]"
  occ talk:turn:add "turn" "$NEXTCLOUD_TALK_TURN_DOMAIN_PORT" "udp,tcp" --secret="$TALK_TURN_SECRET"
  occ config:app:set spreed signaling_servers --value "{}"
  occ talk:signaling:add "$NEXTCLOUD_TALK_SIGNALING_DOMAIN_FULL" "$TALK_SIGNALING_SECRET" --verify
else
  disable_app 'spreed'
fi

echo "Install Preview Generator"
install_and_enable_app "previewgenerator"

# Imaginary
echo "Install Imaginary & set preview & Setup redis"

config_imaginary=$(cat <<EOF
{
  "system": {
    "preview_max_x": 2048,
    "preview_max_y": 2048,
    "preview_imaginary_url": "http://$NEXTCLOUD_IMAGINARY_HOSTNAME:9000",
    "enable_previews": true,
    "filelocking.enabled": true,
    "redis": {
      "host": "$NEXTCLOUD_REDIS_HOSTNAME",
      "port": "$NEXTCLOUD_REDIS_PORT"
    }
  },
  "apps": {
    "preview": {
      "jpeg_quality": "60"
    }
  }
}
EOF
)

config_imaginary_provides=$(echo '
{
  "system": {
    "enabledPreviewProviders": [
      "OC\\Preview\\Imaginary",
      "OC\\Preview\\Image",
      "OC\\Preview\\MarkDown",
      "OC\\Preview\\MP3",
      "OC\\Preview\\TXT",
      "OC\\Preview\\OpenDocument",
      "OC\\Preview\\Movie",
      "OC\\Preview\\Krita",
      "OC\\Preview\\Epub",
      "OC\\Preview\\MKV",
      "OC\\Preview\\MP4",
      "OC\\Preview\\AVI"
    ],
    "memcache.locking": "\\OC\\Memcache\\Redis"
  }
}
'
)

config_imaginary=$(echo "$config_imaginary" "$config_imaginary_provides" | jq -s '.[0] * .[1]')
import_occ "$config_imaginary"

echo "Install notify_push"
app_name='notify_push'
install_and_enable_app $app_name
occ config:system:set trusted_proxies 1 --value="127.0.0.1"
occ config:system:set trusted_proxies 2 --value="::1"
occ config:app:set notify_push base_endpoint --value="$NEXTCLOUD_DOMAIN_FULL/push"

echo "Set LDAP"

occ app:enable user_ldap
LDAP_CONFIG_NAME="s01"
LDAP_CMD="occ ldap:set-config $LDAP_CONFIG_NAME"

IFS=',' read -ra attrs_array <<< "$SAMBA_DC_USER_LOGIN_ATTRS"
attrs=$(printf '%s\\n' "${attrs_array[@]}")
config_ldap=$(cat <<EOF
{
  "apps": {
    "user_ldap": {
      "types": "authentication",
      "s01ldap_configuration_active": "1",
      "s01ldap_port": $SAMBA_DC_LDAPS_PORT,
      "s01ldap_dn": "$SAMBA_DC_ADMINISTRATOR_DN",
      "s01ldap_base": "$SAMBA_DC_BASE_DN",
      "s01ldap_base_groups": "$SAMBA_DC_BASE_GROUPS_ROLE_DN",
      "s01ldap_base_users": "$SAMBA_DC_BASE_USERS_DN",
      "s01ldap_group_filter": "$SAMBA_DC_GROUP_CLASS_FILTER",
      "s01ldap_groupfilter_objectclass": "$SAMBA_DC_GROUP_CLASS_NAME",
      "s01ldap_group_display_name": "$SAMBA_DC_GROUP_DISPLAY_NAME",
      "s01ldap_group_member_assoc_attribute": "$SAMBA_DC_GROUP_MEMBER_ATTR",
      "s01ldap_host": "$SAMBA_DC_LDAPS_SERVER_URL",
      "s01ldap_login_filter": "$NEXTCLOUD_USER_LOGIN_FILTER",
      "s01ldap_userlist_filter": "$NEXTCLOUD_USER_FILTER",
      "s01ldap_expert_username_attr": "$SAMBA_DC_USER_NAME",
      "s01ldap_userfilter_objectclass": "$SAMBA_DC_USER_CLASS_NAME",
      "s01ldap_display_name": "$SAMBA_DC_USER_DISPLAY_NAME",
      "s01ldap_attributes_for_user_search": "$attrs",
      "s01ldap_email_attr": "$SAMBA_DC_USER_EMAIL",
      "s01ldap_nested_groups": 1,
      "s01ldap_user_filter_mode": 1,
      "s01ldap_group_filter_mode": 1,
      "s01ldap_login_filter_mode": 1,
      "s01ldap_nested_groups": 1,
      "s01ldap_expert_uuid_group_attr": "cn",
      "s01ldap_expert_uuid_user_attr": "sAMAccountName",
      "s01ldap_turn_on_pwd_change": 1
    }
  }
}
EOF
)

if [ -n "$NEXTCLOUD_DEFAULT_QUOTA" ]; then
  config_ldap=`echo $config_ldap | jq ".apps.user_ldap = { s01ldap_quota_def: \"$NEXTCLOUD_DEFAULT_QUOTA\"}" `
fi

import_occ "$config_ldap"
$LDAP_CMD ldapAgentPassword "$SAMBA_DC_ADMINISTRATOR_PASSWORD"

echo "occ ldap:test-config s01"
occ ldap:test-config s01

app_name='ldap_write_support'
install_and_enable_app $app_name
template=`echo -e "dn: CN={UID},{BASE}\nobjectClass: user\nsAMAccountName: {UID}\nuserPrincipalName: {UID}@$SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN\ncn: {UID}\nuserAccountControl: 512"`
occ config:app:set $app_name 'template.user' --value "$template"

# add ldap user admin to admin group
waiting_admin() {
  while :
  do
    echo "occ user:info $SAMBA_DC_ADMIN_NAME"
    occ user:info $SAMBA_DC_ADMIN_NAME
    if [[ $(echo $?) == 0 ]]; then
      occ group:adduser admin $SAMBA_DC_ADMIN_NAME
      return
    fi
    # force ldap update users
    occ ldap:search $SAMBA_DC_ADMIN_NAME
    echo "Waiting ldap admin user sync online..."
    sleep 5
  done
}

waiting_admin

echo "Install memories"
if [ "$NEXTCLOUD_MEMORIES_ENABLED" == "true" ]; then
  app_name='memories'
  install_and_enable_app $app_name
  config_memories=$(cat <<EOF
{
  "system": {
    "preview_max_memory": 4096,
    "preview_max_filesize_image": -1
  }
}
EOF
)
  import_occ "$config_memories"
  occ memories:places-setup
fi

echo "Install SAML authentication"
app_name='user_saml'
install_and_enable_app $app_name

# line_count=$(occ saml:config:get 1 | wc -l)
# if [ "$line_count" -eq 1 ]; then
#     occ saml:config:create
# fi
occ config:app:set user_saml type --value="saml"
occ saml:config:set 1 \
    --general-idp0_display_name="SSO Login" \
    --general-uid_mapping="sAMAccountName" \
    --idp-entityId="$LLNG_SAML_IDP_ENTITY_ID" \
    --idp-singleSignOnService.url="$LLNG_SAML_IDP_SSO" \
    --idp-singleLogoutService.url="$LLNG_SAML_IDP_SLO" \
    --idp-singleLogoutService.responseUrl="$LLNG_SAML_IDP_SLO_RESPONSE" \
    --idp-x509cert="$(echo -e $LLNG_SAML_SERVICE_PUBLIC_KEY | sed 's/"//g')" \
    --sp-x509cert="$(echo -e $LLNG_SAML_SERVICE_PUBLIC_KEY | sed 's/"//g')" \
    --sp-privateKey="$(echo -e $LLNG_SAML_SERVICE_PRIVATE_KEY | sed 's/"//g')" \
    --sp-name-id-format="urn:oasis:names:tc:SAML:1.1:nameid-format:WindowsDomainQualifiedName" \
    --security-nameIdEncrypted=0 \
    --security-authnRequestsSigned=1 \
    --security-logoutRequestSigned=1 \
    --security-logoutResponseSigned=1 \
    --security-signMetadata=0 \
    --security-wantMessagesSigned=1 \
    --security-wantAssertionsSigned=1 \
    --security-wantAssertionsEncrypted=0 \
    --security-wantXMLValidation=0 \
    --security-sloWebServerDecode=1 \
    --security-lowercaseUrlencoding=1 \
    --security-wantNameId=0 \
    --security-wantNameIdEncrypted=0 \
    --security-signatureAlgorithm="http://www.w3.org/2001/04/xmldsig-more#rsa-sha256"
occ config:app:set user_saml general-use_saml_auth_for_desktop --value=1
occ config:app:set user_saml general-require_provisioned_account --value=0

# config openid connect
# config_oidc=$(cat <<EOF
# {
#   "system": {
#     "oidc_login_proxy_ldap": true,
#     "oidc_login_hide_password_form": true,
#     "oidc_login_auto_redirect": true,
#     "oidc_login_redir_fallback": false,
#     "oidc_login_provider_url": "",
#     "oidc_login_tls_verify": true,
#     "oidc_login_client_id": "testtest",
#     "oidc_login_client_secret": "testtesttesttest",
#     "oidc_login_disable_registration": true,
#     "oidc_login_use_id_token": false,
#     "oidc_login_attributes": {
#       "sAMAccountName": "sAMAccountName"
#     },
#     "oidc_login_scope": "openid profile email",
#     "oidc_login_logout_url": ""
#   }
# }
# EOF
# )
# import_occ "$config_oidc"

echo "Nextcloud tasks execute completed"
