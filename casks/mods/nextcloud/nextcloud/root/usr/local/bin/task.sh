#!/usr/bin/with-contenv bash

runas_user() {
  yasu nextcloud:nextcloud "$@"
}

if [ "$SIDECAR_CRON" = "1" ] || [ "$SIDECAR_PREVIEWGEN" = "1" ] || [ "$SIDECAR_NEWSUPDATER" = "1" ]; then
  exit 0
fi

rm -f /run/nextcloud-tasks.ready

if [ "$NEXTCLOUD_RM_SKELETON_FILES" == "true" ]; then 
  rm -rf /var/www/core/skeleton/*
  mkdir /var/www/core/skeleton/Documents
  mkdir /var/www/core/skeleton/Photos
fi

user_app_path='/var/www/userapps'
retry_occ() {
  local attempt
  for attempt in 1 2 3 4 5; do
    if "$@"; then
      return 0
    fi
    echo "Command failed (attempt $attempt/5): $*"
    sleep 5
  done
  return 1
}

install_app_once() {
  local app_name="$1"
  local output status github_url archive proxy_prefix

  output=$(occ app:install "$app_name" 2>&1)
  status=$?
  printf '%s\n' "$output"
  if [ "$status" -eq 0 ]; then
    return 0
  fi

  if [ "$CHINESE_SPEEDUP" != "true" ]; then
    return "$status"
  fi
  github_url=$(printf '%s\n' "$output" | \
    sed -n 's#.*\(https://github\.com/[^ ]*\.tar\.gz\).*#\1#p' | head -n 1)
  if [ -z "$github_url" ]; then
    return "$status"
  fi

  proxy_prefix="${GITHUB_DOWNLOAD_PROXY_PREFIX:-https://gh-proxy.com/}"
  archive=$(mktemp)
  echo "Direct GitHub download failed; retrying $app_name through the configured mirror"
  if ! curl -fsSL --retry 3 --connect-timeout 15 --max-time 300 \
    "$proxy_prefix$github_url" -o "$archive"; then
    rm -f "$archive"
    return "$status"
  fi
  if ! tar -xzf "$archive" -C "$user_app_path"; then
    rm -f "$archive"
    return "$status"
  fi
  rm -f "$archive"
  [ -d "$user_app_path/$app_name" ]
}

app_version_pin() {
  case "$1" in
    previewgenerator) printf '%s' "${NEXTCLOUD_PREVIEWGENERATOR_VERSION:-}" ;;
  esac
}

restore_memories_places_source() {
  local places_file backup proxy_prefix
  places_file="$user_app_path/memories/lib/Service/Places.php"
  backup="/data/.anas-backups/memories-Places.php"
  [ -f "$places_file" ] || return 0
  if [ -f "$backup" ]; then
    cp -p "$backup" "$places_file"
    return
  fi
  proxy_prefix="${GITHUB_DOWNLOAD_PROXY_PREFIX:-https://gh-proxy.com/}"
  sed -i \
    "s#${proxy_prefix}https://github.com/pulsejet/memories-assets/#https://github.com/pulsejet/memories-assets/#" \
    "$places_file"
}

install_app_from_store_mirror() {
  local app_name="$1"
  local version version_pin appstore_cache download_url proxy_prefix archive

  version=$(occ status --output=json | jq -r '.versionstring')
  version_pin=$(app_version_pin "$app_name")
  appstore_cache="/tmp/nextcloud-appstore-$version.json"
  if [ ! -s "$appstore_cache" ]; then
    curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 \
      "https://apps.nextcloud.com/api/v1/platform/$version/apps.json" \
      -o "$appstore_cache" || return 1
  fi
  download_url=$(jq -r --arg app "$app_name" --arg pin "$version_pin" '
    map(select(.id == $app))[0].releases
    | map(select(.version | test("-") | not))
    | if $pin == "" then
        sort_by(.version | split(".") | map(tonumber)) | last
      else
        map(select(.version == $pin)) | last
      end
    | .download // empty
  ' "$appstore_cache")
  if [ -z "$download_url" ]; then
    return 1
  fi

  case "$download_url" in
    https://github.com/*)
      proxy_prefix="${GITHUB_DOWNLOAD_PROXY_PREFIX:-https://gh-proxy.com/}"
      download_url="$proxy_prefix$download_url"
      ;;
  esac
  archive=$(mktemp)
  echo "Installing $app_name from the Nextcloud app store through the configured mirror"
  if ! curl -fsSL --retry 3 --connect-timeout 15 --max-time 300 \
    "$download_url" -o "$archive"; then
    rm -f "$archive"
    return 1
  fi
  if ! tar -xzf "$archive" -C "$user_app_path"; then
    rm -f "$archive"
    return 1
  fi
  rm -f "$archive"
  [ -d "$user_app_path/$app_name" ]
}

retry_install_app() {
  local app_name="$1"
  local attempt
  for attempt in 1 2 3 4 5; do
    if [ "$CHINESE_SPEEDUP" = "true" ] && \
      install_app_from_store_mirror "$app_name"; then
      return 0
    fi
    if install_app_once "$app_name"; then
      return 0
    fi
    echo "App install failed (attempt $attempt/5): $app_name"
    sleep 5
  done
  return 1
}

install_and_enable_app() { # $1 app name
  local installed_now=0 version_pin installed_version
  version_pin=$(app_version_pin "$1")
  if [ -d "$user_app_path/$1" ] && [ -n "$version_pin" ]; then
    installed_version=$(awk -F'[<>]' '/<version>/{print $3; exit}' "$user_app_path/$1/appinfo/info.xml")
    if [ "$installed_version" != "$version_pin" ]; then
      echo "Replacing $1 $installed_version with pinned compatible version $version_pin"
      occ app:disable "$1" || true
      rm -rf "$user_app_path/$1"
    fi
  fi
  if ! [ -d "$user_app_path/$1" ]; then
    retry_install_app "$1" || {
      echo "Unable to install required Nextcloud app: $1"
      exit 1
    }
    installed_now=1
  fi
  if [ "$SKIP_UPDATE" != 1 ] && [ "$installed_now" -eq 0 ] && [ -z "$version_pin" ]; then
    retry_occ occ app:update "$1" || {
      echo "Unable to update required Nextcloud app: $1"
      exit 1
    }
  fi
  if [ "$(occ config:app:get $1 enabled)" != "yes" ]; then
    retry_occ occ app:enable "$1" || {
      echo "Unable to enable required Nextcloud app: $1"
      exit 1
    }
    if [ "$(occ config:app:get $1 enabled)" != "yes" ]; then\
      echo -e "Error\n$1 app enable failed"
      exit 1
    fi
  fi
  if [ "$1" = "memories" ]; then
    restore_memories_places_source
  fi
  if ! occ integrity:check-app "$1" >/dev/null; then
    echo "Integrity check failed for required Nextcloud app: $1"
    exit 1
  fi
}

setup_memories_places() {
  local places_file backup marker proxy_prefix status

  marker="/data/.anas-state/memories-places.ready"
  if [ -f "$marker" ]; then
    echo "Memories places data is already initialized"
    return 0
  fi

  if [ "$CHINESE_SPEEDUP" != "true" ]; then
    occ memories:places-setup --force \
      --transaction-size "${NEXTCLOUD_MEMORIES_PLACES_TRANSACTION_SIZE:-10000}"
    status=$?
    if [ "$status" -eq 0 ]; then
      mkdir -p "$(dirname "$marker")"
      touch "$marker"
    fi
    return "$status"
  fi

  places_file="$user_app_path/memories/lib/Service/Places.php"
  backup="/data/.anas-backups/memories-Places.php"
  mkdir -p "$(dirname "$backup")"
  restore_memories_places_source
  cp -p "$places_file" "$backup"
  proxy_prefix="${GITHUB_DOWNLOAD_PROXY_PREFIX:-https://gh-proxy.com/}"
  sed -i \
    "s#https://github.com/pulsejet/memories-assets/#${proxy_prefix}https://github.com/pulsejet/memories-assets/#" \
    "$places_file"
  occ memories:places-setup --force \
    --transaction-size "${NEXTCLOUD_MEMORIES_PLACES_TRANSACTION_SIZE:-10000}"
  status=$?
  cp -p "$backup" "$places_file"
  if ! occ integrity:check-app memories >/dev/null; then
    echo "Integrity check failed after Memories places setup"
    return 1
  fi
  if [ "$status" -eq 0 ]; then
    mkdir -p "$(dirname "$marker")"
    touch "$marker"
  fi
  return "$status"
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
  occ talk:stun:add "$TURN_DOMAIN_PORT"
  occ config:app:set spreed turn_servers --value "[]"
  occ talk:turn:add "turn" "$TURN_DOMAIN_PORT" "udp,tcp" --secret="$TURN_SECRET"
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
attrs=""
for attr in "${attrs_array[@]}"; do
  [ -z "${attr//[[:space:]]/}" ] && continue
  [ -n "$attrs" ] && attrs="${attrs}\\n"
  attrs="${attrs}${attr}"
done
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
  if [ "${NEXTCLOUD_MEMORIES_PLACES_ENABLED:-true}" = "true" ]; then
    if ! setup_memories_places; then
      echo "Unable to initialize required Memories places data"
      exit 1
    fi
  fi
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
touch /run/nextcloud-tasks.ready
