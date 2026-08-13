#!/bin/sh

# Pure URL helpers shared by the runtime installer and host-side tests. The
# presence of explicit mirror variables is authoritative; CHINESE_SPEEDUP is
# responsible for supplying their published defaults.
nextcloud_appstore_platform_url() {
  version=$1
  appstore_url=${NEXTCLOUD_APPSTORE_URL:-https://apps.nextcloud.com/api/v1}
  printf '%s/platform/%s/apps.json\n' "${appstore_url%/}" "$version"
}

nextcloud_download_url() {
  source_url=$1
  case "$source_url" in
    https://github.com/*)
      if [ -n "${GITHUB_DOWNLOAD_PROXY_PREFIX:-}" ]; then
        printf '%s/%s\n' "${GITHUB_DOWNLOAD_PROXY_PREFIX%/}" "${source_url#https://}"
        return
      fi
      ;;
  esac
  printf '%s\n' "$source_url"
}
