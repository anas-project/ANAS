#!/bin/bash
set -o pipefail

occ() {
  runuser -u www-data -- php /var/www/html/occ "$@"
}

rm -f /run/nextcloud-tasks.ready

if [ "$NEXTCLOUD_RM_SKELETON_FILES" == "true" ]; then 
  rm -rf /var/www/html/core/skeleton/*
  mkdir -p /var/www/html/core/skeleton/Documents
  mkdir -p /var/www/html/core/skeleton/Photos
fi

user_app_path='/var/www/html/custom_apps'
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

  proxy_prefix="${GITHUB_DOWNLOAD_PROXY_PREFIX:-}"
  if [ -z "$proxy_prefix" ]; then
    return "$status"
  fi
  archive=$(mktemp)
  echo "Direct GitHub download failed; retrying $app_name through the configured mirror"
  if ! curl -fsSL --retry 3 --connect-timeout 15 --max-time 300 \
    "${proxy_prefix%/}/${github_url#https://}" -o "$archive"; then
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
    richdocuments) printf '%s' '11.1.0' ;;
    spreed) printf '%s' '24.0.3' ;;
    previewgenerator) printf '%s' '5.14.0' ;;
    notify_push) printf '%s' '1.3.5' ;;
    memories) printf '%s' '8.1.0' ;;
    user_saml) printf '%s' '8.2.0' ;;
  esac
}

restore_memories_places_source() {
  local places_file backup proxy_prefix
  places_file="$user_app_path/memories/lib/Service/Places.php"
  backup="/var/www/html/.anas-backups/memories-Places.php"
  [ -f "$places_file" ] || return 0
  if [ -f "$backup" ]; then
    cp -p "$backup" "$places_file"
    return
  fi
  proxy_prefix="${GITHUB_DOWNLOAD_PROXY_PREFIX:-}"
  if [ -n "$proxy_prefix" ]; then
    sed -i \
      -e "s#${proxy_prefix%/}/github.com/pulsejet/memories-assets/#https://github.com/pulsejet/memories-assets/#" \
      -e "s#${proxy_prefix}https://github.com/pulsejet/memories-assets/#https://github.com/pulsejet/memories-assets/#" \
      "$places_file"
  fi
}

memories_places_archive='/var/www/html/.anas-cache/planet_coarse_boundaries.zip'
memories_places_sha256='b443fc32dfdd26dd27b3c2def96da865841b6210473e3360da191f725f14dc55'

validate_memories_places_archive() {
  printf '%s  %s\n' "$memories_places_sha256" "$memories_places_archive" | \
    sha256sum -c - >/dev/null || return 1
  php -r '
    $zip = new ZipArchive();
    $status = $zip->open($argv[1]);
    if ($status !== true) {
      exit(1);
    }
    $valid = $zip->locateName("planet_coarse_boundaries.txt") !== false;
    $zip->close();
    exit($valid ? 0 : 1);
  ' "$memories_places_archive"
}

prepare_memories_places_archive() {
  local attempt download_url proxy_prefix

  if [ -s "$memories_places_archive" ] && validate_memories_places_archive; then
    echo "Using cached Memories places archive"
    return 0
  fi

  mkdir -p "$(dirname "$memories_places_archive")"
  download_url='https://github.com/pulsejet/memories-assets/releases/download/geo-0.0.4/planet_coarse_boundaries.zip'
  if [ "$CHINESE_SPEEDUP" = "true" ] && [ -n "$GITHUB_DOWNLOAD_PROXY_PREFIX" ]; then
    proxy_prefix="$GITHUB_DOWNLOAD_PROXY_PREFIX"
    download_url="${proxy_prefix%/}/${download_url#https://}"
  fi

  for attempt in 1 2 3 4 5; do
    echo "Downloading Memories places archive (attempt $attempt/5)"
    if curl -fL --retry 5 --retry-delay 2 --retry-all-errors \
      --connect-timeout 20 --max-time 3600 -C - \
      "$download_url" -o "$memories_places_archive"; then
      if validate_memories_places_archive; then
        return 0
      fi
      echo "Downloaded Memories places archive is invalid"
      rm -f "$memories_places_archive"
    fi
    sleep 5
  done
  return 1
}

install_app_from_store_mirror() {
  local app_name="$1"
  local version version_pin appstore_cache appstore_url release release_version download_url proxy_prefix archive

  version=$(occ status --output=json | jq -r '.versionstring')
  version_pin=$(app_version_pin "$app_name")
  appstore_cache="/tmp/nextcloud-appstore-$version.json"
  if [ ! -s "$appstore_cache" ]; then
    appstore_url="${NEXTCLOUD_APPSTORE_URL:-https://apps.nextcloud.com/api/v1}"
    curl -fsSL --retry 3 --connect-timeout 15 --max-time 120 \
      "${appstore_url%/}/platform/$version/apps.json" \
      -o "$appstore_cache" || return 1
  fi
  release=$(jq -c --arg app "$app_name" --arg pin "$version_pin" '
    map(select(.id == $app))[0].releases
    | map(select(.version | test("-") | not))
    | if $pin == "" then
        sort_by(.version | split(".") | map(tonumber)) | last
      else
        map(select(.version == $pin)) | last
      end
  ' "$appstore_cache")
  release_version=$(printf '%s' "$release" | jq -r '.version // empty')
  download_url=$(printf '%s' "$release" | jq -r '.download // empty')
  if [ -z "$download_url" ]; then
    return 1
  fi

  case "$download_url" in
    https://github.com/*)
      proxy_prefix="${GITHUB_DOWNLOAD_PROXY_PREFIX:-}"
      if [ -n "$proxy_prefix" ]; then
        download_url="${proxy_prefix%/}/${download_url#https://}"
      fi
      ;;
  esac
  mkdir -p /var/www/html/.anas-cache/apps
  archive="/var/www/html/.anas-cache/apps/$app_name-$release_version.tar.gz"
  echo "Installing $app_name from the Nextcloud app store through the configured mirror"
  if ! tar -tzf "$archive" >/dev/null 2>&1; then
    if ! curl -fL --retry 5 --retry-delay 2 --retry-all-errors \
      --connect-timeout 20 --max-time 3600 -C - \
      "$download_url" -o "$archive"; then
      return 1
    fi
  fi
  tar -tzf "$archive" >/dev/null 2>&1 || return 1
  if ! tar -xzf "$archive" -C "$user_app_path"; then
    return 1
  fi
  [ -d "$user_app_path/$app_name" ]
}

retry_install_app() {
  local app_name="$1"
  local attempt
  for attempt in 1 2 3 4 5; do
    if install_app_from_store_mirror "$app_name"; then
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
  local places_file backup marker progress_marker log_file

  marker="/var/www/html/.anas-state/memories-places.ready"
  progress_marker="/var/www/html/.anas-state/memories-places.in-progress"
  log_file="/var/www/html/.anas-state/memories-places.log"
  if [ -f "$marker" ]; then
    echo "Memories places data is already initialized"
    return 0
  fi
  if [ -f "$progress_marker" ] && pgrep -f 'occ memories:places-setup' >/dev/null; then
    echo "Memories places data import is already running"
    return 0
  fi
  rm -f "$progress_marker"

  if ! prepare_memories_places_archive; then
    echo "Unable to download a valid Memories places archive"
    return 1
  fi

  places_file="$user_app_path/memories/lib/Service/Places.php"
  backup="/var/www/html/.anas-backups/memories-Places.php"
  mkdir -p "$(dirname "$backup")"
  restore_memories_places_source
  cp -p "$places_file" "$backup"
  sed -i \
    "s#^const PLANET_URL = .*#const PLANET_URL = 'file://${memories_places_archive}';#" \
    "$places_file"
  mkdir -p "$(dirname "$marker")"
  touch "$progress_marker"
  (
    status=1
    for attempt in 1 2 3 4 5; do
      occ memories:places-setup --force \
        --transaction-size "${NEXTCLOUD_MEMORIES_PLACES_TRANSACTION_SIZE:-10000}" && {
          status=0
          break
        }
      status=$?
      echo "Memories places setup failed (attempt $attempt/5)"
      sleep 5
    done
    cp -p "$backup" "$places_file"
    if ! occ integrity:check-app memories >/dev/null; then
      echo "Integrity check failed after Memories places setup"
      status=1
    fi
    if [ "$status" -eq 0 ]; then
      touch "$marker"
    fi
    rm -f "$progress_marker"
    exit "$status"
  ) >>"$log_file" 2>&1 &
  echo "Memories places data import started in background"
  return 0
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
  collabora_ipv4=$(getent ahostsv4 "$COLLABORA_HOSTNAME" | awk 'NR == 1 { print $1 }')
  # TODO: ipv6
  richdocuments_config=$(jq -n \
    --arg url "$COLLABORA_DOMAIN_FULL" \
    --arg allowlist "$collabora_ipv4" \
    '{apps:{richdocuments:{doc_format:"ooxml",public_wopi_url:$url,wopi_url:$url,wopi_allowlist:$allowlist}}}')
  import_occ "$richdocuments_config"
  occ richdocuments:activate-config
else
  disable_app 'richdocuments'
fi

echo "Install talk"
if [ "$NEXTCLOUD_TALK_ENABLED" == "true" ]; then
  app_name='spreed'
  install_and_enable_app $app_name
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
    "preview_imaginary_key": "$NEXTCLOUD_IMAGINARY_SECRET",
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
notify_push_ip=''
for attempt in $(seq 1 30); do
  notify_push_ip=$(getent ahostsv4 "$NEXTCLOUD_PUSH_HOSTNAME" | awk 'NR == 1 { print $1 }' || true)
  if [ -n "$notify_push_ip" ]; then
    break
  fi
  sleep 1
done
if [ -z "$notify_push_ip" ]; then
  echo "Unable to resolve notify_push container: $NEXTCLOUD_PUSH_HOSTNAME" >&2
  exit 1
fi
occ config:system:set trusted_proxies 1 --value="$notify_push_ip"
occ config:system:set trusted_proxies 2 --value="127.0.0.1"
occ config:system:set trusted_proxies 3 --value="::1"
occ config:app:set notify_push base_endpoint --value="$NEXTCLOUD_DOMAIN_FULL/push"

echo "Set LDAP"

occ app:enable user_ldap
LDAP_CONFIG_NAME="s01"

if ! occ ldap:show-config "$LDAP_CONFIG_NAME" >/dev/null 2>&1; then
  occ ldap:create-empty-config
fi

if ! occ ldap:show-config "$LDAP_CONFIG_NAME" >/dev/null 2>&1; then
  echo "Unable to create LDAP configuration $LDAP_CONFIG_NAME" >&2
  exit 1
fi

set_ldap_config() {
  occ ldap:set-config "$LDAP_CONFIG_NAME" "$1" "$2"
}

IFS=',' read -ra attrs_array <<< "$SAMBA_DC_USER_LOGIN_ATTRS"
attrs=""
for attr in "${attrs_array[@]}"; do
  [ -z "${attr//[[:space:]]/}" ] && continue
  [ -n "$attrs" ] && attrs="${attrs}"$'\n'
  attrs="${attrs}${attr}"
done

set_ldap_config ldapPort "$SAMBA_DC_LDAPS_PORT"
set_ldap_config ldapAgentName "$SAMBA_DC_PASSWORD_BIND_DN"
set_ldap_config ldapBase "$SAMBA_DC_BASE_DN"
set_ldap_config ldapBaseGroups "$SAMBA_DC_BASE_GROUPS_ROLE_DN"
set_ldap_config ldapBaseUsers "$SAMBA_DC_BASE_USERS_DN"
set_ldap_config ldapGroupFilter "$SAMBA_DC_GROUP_CLASS_FILTER"
set_ldap_config ldapGroupFilterObjectclass "$SAMBA_DC_GROUP_CLASS_NAME"
set_ldap_config ldapGroupDisplayName "$SAMBA_DC_GROUP_DISPLAY_NAME"
set_ldap_config ldapGroupMemberAssocAttr "$SAMBA_DC_GROUP_MEMBER_ATTR"
set_ldap_config ldapHost "$SAMBA_DC_LDAPS_SERVER_URL"
set_ldap_config ldapLoginFilter "$NEXTCLOUD_USER_LOGIN_FILTER"
set_ldap_config ldapUserFilter "$NEXTCLOUD_USER_FILTER"
set_ldap_config ldapExpertUsernameAttr "$SAMBA_DC_USER_NAME"
set_ldap_config ldapUserFilterObjectclass "$SAMBA_DC_USER_CLASS_NAME"
set_ldap_config ldapUserDisplayName "$SAMBA_DC_USER_DISPLAY_NAME"
set_ldap_config ldapAttributesForUserSearch "$attrs"
set_ldap_config ldapEmailAttribute "$SAMBA_DC_USER_EMAIL"
set_ldap_config ldapNestedGroups 1
set_ldap_config ldapUserFilterMode 1
set_ldap_config ldapGroupFilterMode 1
set_ldap_config ldapLoginFilterMode 1
set_ldap_config ldapExpertUUIDGroupAttr "$SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"
set_ldap_config ldapExpertUUIDUserAttr "$SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE"
set_ldap_config turnOnPasswordChange 1

if [ -n "$NEXTCLOUD_DEFAULT_QUOTA" ]; then
  set_ldap_config ldapQuotaDefault "$NEXTCLOUD_DEFAULT_QUOTA"
fi

set_ldap_config ldapAgentPassword "$SAMBA_DC_PASSWORD_BIND_PASSWORD"
set_ldap_config ldapConfigurationActive 1

echo "occ ldap:test-config s01"
occ ldap:test-config s01

# Password changes are handled by Nextcloud's LDAP backend through the
# narrowly delegated svc_password account. Broader LDAP writes and domain
# user creation remain disabled.
occ app:disable ldap_write_support >/dev/null 2>&1 || true

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
    --general-uid_mapping="$SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE" \
    --saml-attribute-mapping-displayName_mapping=cn \
    --saml-attribute-mapping-user_id_ldap_mapping="$SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE" \
    --idp-entityId="$NEXTCLOUD_SAML_IDP_ENTITY_ID" \
    --idp-singleSignOnService.url="$NEXTCLOUD_SAML_IDP_SSO" \
    --idp-singleLogoutService.url="$NEXTCLOUD_SAML_IDP_SLO" \
    --idp-singleLogoutService.responseUrl="$NEXTCLOUD_SAML_IDP_SLO_RESPONSE" \
    --idp-x509cert="$NEXTCLOUD_SAML_IDP_CERT" \
    --sp-x509cert="$NEXTCLOUD_SAML_SP_CERT" \
    --sp-privateKey="$NEXTCLOUD_SAML_SP_PRIVATE_KEY" \
    --sp-name-id-format="urn:oasis:names:tc:SAML:2.0:nameid-format:WindowsDomainQualifiedName" \
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
