#!/bin/sh

set -eu

portal_languages=/usr/share/lemonldap-ng/portal/htdocs/static/languages
portal_common=/usr/share/lemonldap-ng/portal/htdocs/static/common
manager_languages=/usr/share/lemonldap-ng/manager/htdocs/static/languages
manager_common=/usr/share/lemonldap-ng/manager/htdocs/static/common
config=/etc/lemonldap-ng/lemonldap-ng.ini

# LLNG negotiates only languages listed in lemonldap-ng.ini.  Some upstream
# images ship the Simplified Chinese catalogue without enabling it.  If a
# future image drops that catalogue but keeps Traditional Chinese, expose the
# Traditional catalogue under the generic `zh` code so zh-CN/zh requests still
# get Chinese rather than falling through to English.
ensure_zh_catalogue() {
  language_dir=$1
  common_dir=$2

  if [ ! -f "$language_dir/zh.json" ] && [ -f "$language_dir/zh_TW.json" ]; then
    cp "$language_dir/zh_TW.json" "$language_dir/zh.json"
    if [ ! -f "$common_dir/zh.png" ] && [ -f "$common_dir/zh_TW.png" ]; then
      cp "$common_dir/zh_TW.png" "$common_dir/zh.png"
    fi
  fi
}

ensure_zh_catalogue "$portal_languages" "$portal_common"
ensure_zh_catalogue "$manager_languages" "$manager_common"

if [ -f "$portal_languages/zh.json" ]; then
  # There are two independent `languages` entries: Portal and Manager.
  # Enabling zh in both keeps the authentication and administration UIs
  # consistent. Avoid appending it more than once when the upstream default
  # eventually enables it itself.
  awk '
    /^[[:space:]]*languages[[:space:]]*=/ {
      list = $0
      normalized = list
      gsub(/[[:space:]]/, "", normalized)
      if (normalized !~ /(^|,)zh(,|$)/) {
        sub(/[[:space:]]*$/, ", zh", list)
      }
      print list
      next
    }
    { print }
  ' "$config" >"$config.tmp"
  cat "$config.tmp" >"$config"
  rm -f "$config.tmp"
fi
