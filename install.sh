#!/usr/bin/env sh
set -eu

github_release_root_default="https://github.com/anas-project/ANAS/releases"
cnb_release_root_default="https://cnb.cool/anas.dev/ANAS/-/releases"

usage() {
  cat <<'EOF'
Install, upgrade, or uninstall ANAS Core on Linux.

Usage:
  install.sh [--source github|cn] [--install-dir DIR] [--no-service]
  install.sh --uninstall [--install-dir DIR] [--purge] [--no-service]

Environment:
  ANAS_INSTALL_SOURCE       github (default) or cn
  ANAS_INSTALL_DIR          binary destination (default: /usr/local/bin)
  ANAS_INSTALL_SERVICE      auto (default), 1, or 0
  ANAS_SOURCE_CONFIG        source preference file (default: $XDG_CONFIG_HOME/anas/source)
  ANAS_HELPER_DIR           privileged helper directory (default: /usr/local/lib/anas)
  ANAS_SERVICE_CONFIG       daemon configuration (default: /etc/anas/anasd.yml)
  ANAS_SYSTEMD_UNIT         systemd unit (default: /etc/systemd/system/anasd.service)
  ANAS_SYSTEMCTL            systemctl executable (default: systemctl)
  ANAS_MANAGEMENT_PORT      initial management port (default: 8080)
  ANAS_CONSOLE_STORE        initial console state directory (default: /var/lib/anas/console)
EOF
}

fail() {
  printf 'anas installer: %s\n' "$*" >&2
  exit 1
}

run_as_root() {
  if [ "$(id -u)" -eq 0 ]; then
    "$@"
  else
    command -v sudo >/dev/null 2>&1 || fail "root access is required and sudo is unavailable"
    sudo "$@"
  fi
}

validate_service_path() {
  label="$1"
  value="$2"
  case "$value" in
    /*) ;;
    *) fail "$label must be an absolute path" ;;
  esac
  case "$value" in
    *[!A-Za-z0-9_./-]*) fail "$label contains characters unsupported by the systemd installer" ;;
  esac
}

# Everything that touches the system lives in main(), and main runs only from
# the last line. A truncated `curl ... | sh` download is therefore inert.
main() {
  source_name="${ANAS_INSTALL_SOURCE:-github}"
  install_dir="${ANAS_INSTALL_DIR:-/usr/local/bin}"
  install_service="${ANAS_INSTALL_SERVICE:-auto}"
  uninstall=false
  purge=false

  while [ "$#" -gt 0 ]; do
    case "$1" in
      --source)
        [ "$#" -ge 2 ] || fail "--source requires github or cn"
        source_name="$2"
        shift 2
        ;;
      --install-dir)
        [ "$#" -ge 2 ] || fail "--install-dir requires a directory"
        install_dir="$2"
        shift 2
        ;;
      --no-service)
        install_service=0
        shift
        ;;
      --uninstall)
        uninstall=true
        shift
        ;;
      --purge)
        purge=true
        shift
        ;;
      -h|--help)
        usage
        exit 0
        ;;
      *) fail "unknown option: $1" ;;
    esac
  done

  [ "$uninstall" = true ] || [ "$purge" = false ] || fail "--purge requires --uninstall"
  case "$install_service" in
    auto)
      if [ "$install_dir" = /usr/local/bin ]; then install_service=1; else install_service=0; fi
      ;;
    0|1) ;;
    *) fail "ANAS_INSTALL_SERVICE must be auto, 1, or 0" ;;
  esac

  service_config="${ANAS_SERVICE_CONFIG:-/etc/anas/anasd.yml}"
  systemd_unit="${ANAS_SYSTEMD_UNIT:-/etc/systemd/system/anasd.service}"
  systemctl_command="${ANAS_SYSTEMCTL:-systemctl}"
  management_port="${ANAS_MANAGEMENT_PORT:-8080}"
  console_store="${ANAS_CONSOLE_STORE:-/var/lib/anas/console}"
  helper_dir="${ANAS_HELPER_DIR:-/usr/local/lib/anas}"
  install_target="$install_dir/anas"
  daemon_target="$install_dir/anasd"
  helper_target="$helper_dir/anas-helper"
  service_name="$(basename "$systemd_unit")"

  if [ -n "${ANAS_SOURCE_CONFIG:-}" ]; then
    source_config="$ANAS_SOURCE_CONFIG"
  else
    config_home="${XDG_CONFIG_HOME:-${HOME:?HOME is required}/.config}"
    source_config="$config_home/anas/source"
  fi

  if [ "$uninstall" = true ]; then
    os_name="$(uname -s)"
    [ "$os_name" = Linux ] || fail "only Linux is currently supported (detected $os_name)"
    if [ "$install_service" -eq 1 ] && [ -e "$systemd_unit" ]; then
      command -v "$systemctl_command" >/dev/null 2>&1 || fail "$systemctl_command is required to remove the system service"
      run_as_root "$systemctl_command" disable --now "$service_name" >/dev/null 2>&1 || true
      run_as_root rm -f "$systemd_unit"
      run_as_root "$systemctl_command" daemon-reload
    fi
    if [ -e "$install_target" ] || [ -e "$daemon_target" ]; then
      run_as_root rm -f "$install_target" "$daemon_target"
    fi
    if [ -e "$helper_target" ]; then
      run_as_root rm -f "$helper_target"
    fi
    if [ "$purge" = true ]; then
      if [ -e "$service_config" ]; then run_as_root rm -f "$service_config"; fi
      rm -f "$source_config"
    fi
    printf 'Uninstalled ANAS Core. Workspace and console state data were preserved.\n'
    if [ "$purge" = false ] && [ -e "$service_config" ]; then
      printf 'Preserved service configuration %s (use --purge to remove it).\n' "$service_config"
    fi
    exit 0
  fi

  case "$source_name" in
    github) release_root="$github_release_root_default"; runtime_source="official" ;;
    cn) release_root="$cnb_release_root_default"; runtime_source="official-cn" ;;
    *) fail "unsupported source '$source_name' (expected github or cn)" ;;
  esac
  os_name="$(uname -s)"
  case "$os_name" in Linux) ;; *) fail "only Linux is currently supported (detected $os_name)" ;; esac
  machine="$(uname -m)"
  case "$machine" in
    x86_64|amd64) arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) fail "unsupported Linux architecture '$machine' (supported: amd64, arm64)" ;;
  esac

  command -v curl >/dev/null 2>&1 || fail "curl is required"
  command -v tar >/dev/null 2>&1 || fail "tar is required"
  command -v install >/dev/null 2>&1 || fail "install is required"
  work_dir="$(mktemp -d 2>/dev/null || mktemp -d -t anas-install)"
  trap 'rm -rf "$work_dir"' EXIT HUP INT TERM
  checksums="$work_dir/SHA256SUMS"
  download() {
    output="$1"
    url="$2"
    curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o "$output" "$url"
  }

  latest_url="${release_root%/}/latest"
  latest_effective_url="$(curl --proto '=https' --tlsv1.2 -fsSL --retry 3 -o /dev/null -w '%{url_effective}' "$latest_url")"
  tag="${latest_effective_url##*/}"
  case "$latest_effective_url:$tag" in
    */releases/tag/v*:v[0-9]*.[0-9]*.[0-9]*) ;;
    *) fail "could not resolve the latest $source_name release tag from $latest_effective_url" ;;
  esac
  version="${tag#v}"
  download_root="${release_root%/}/download/$tag"
  printf 'Downloading ANAS %s for linux/%s from %s...\n' "$version" "$arch" "$source_name"
  download "$checksums" "$download_root/SHA256SUMS"

  asset=""
  expected=""
  for candidate in "anas_linux_${arch}.tar.gz" "anas_${version}_linux_${arch}.tar.gz"; do
    candidate_sum="$(awk -v file="$candidate" '$2 == file || $2 == "*" file { print $1; exit }' "$checksums")"
    if [ -n "$candidate_sum" ]; then asset="$candidate"; expected="$candidate_sum"; break; fi
  done
  [ -n "$asset" ] || fail "no linux/$arch archive is listed in SHA256SUMS for $tag"
  archive="$work_dir/$asset"
  download "$archive" "$download_root/$asset"
  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$archive" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
  else
    fail "sha256sum or shasum is required to verify the release"
  fi
  [ "$actual" = "$expected" ] || fail "checksum mismatch for $asset"

  mkdir -p "$work_dir/extract"
  archive_dir="${asset%.tar.gz}"
  tar -xzf "$archive" -C "$work_dir/extract" "$archive_dir/anas"
  for optional in anasd anas-helper anasd.service anasd.yml; do
    tar -xzf "$archive" -C "$work_dir/extract" "$archive_dir/$optional" 2>/dev/null || true
  done
  binary="$work_dir/extract/$archive_dir/anas"
  daemon="$work_dir/extract/$archive_dir/anasd"
  helper="$work_dir/extract/$archive_dir/anas-helper"
  packaged_unit="$work_dir/extract/$archive_dir/anasd.service"
  packaged_config="$work_dir/extract/$archive_dir/anasd.yml"
  [ -f "$binary" ] || fail "release archive does not contain anas"
  reported_version_output="$("$binary" version)" || fail "downloaded anas binary could not report its version"
  reported_version="$(printf '%s\n' "$reported_version_output" | awk 'NR == 1 && $1 == "anas" { print $2 }')"
  [ "$reported_version" = "$version" ] || fail "release tag $tag contains anas ${reported_version:-<unknown>}"

  # Validate every service input before replacing any installed file. Legacy
  # CLI-only archives remain usable with --no-service, while a service install
  # cannot fail halfway through because one packaged member was absent.
  if [ "$install_service" -eq 1 ]; then
    [ -f "$daemon" ] || fail "release archive does not contain anasd"
    [ -f "$packaged_unit" ] || fail "release archive does not contain anasd.service"
    [ -f "$packaged_config" ] || fail "release archive does not contain anasd.yml"
    validate_service_path "ANAS_INSTALL_DIR" "$install_dir"
    validate_service_path "ANAS_SERVICE_CONFIG" "$service_config"
    validate_service_path "ANAS_SYSTEMD_UNIT" "$systemd_unit"
    validate_service_path "ANAS_CONSOLE_STORE" "$console_store"
    case "$management_port" in ''|*[!0-9]*) fail "ANAS_MANAGEMENT_PORT must be an integer between 1 and 65535" ;; esac
    [ "$management_port" -ge 1 ] && [ "$management_port" -le 65535 ] || fail "ANAS_MANAGEMENT_PORT must be between 1 and 65535"
    command -v "$systemctl_command" >/dev/null 2>&1 || fail "$systemctl_command is required unless --no-service is used"
  fi

  if [ ! -d "$install_dir" ]; then mkdir -p "$install_dir" 2>/dev/null || true; fi
  if [ -d "$install_dir" ] && [ -w "$install_dir" ]; then
    install -m 0755 "$binary" "$install_target"
    if [ -f "$daemon" ]; then install -m 0755 "$daemon" "$daemon_target"; fi
  else
    run_as_root install -d -m 0755 "$install_dir"
    run_as_root install -m 0755 "$binary" "$install_target"
    if [ -f "$daemon" ]; then run_as_root install -m 0755 "$daemon" "$daemon_target"; fi
  fi

  if [ -f "$helper" ]; then
    if [ "$(id -u)" -eq 0 ] || command -v sudo >/dev/null 2>&1; then
      run_as_root install -d -m 0755 "$helper_dir"
      run_as_root install -m 0755 "$helper" "$helper_target"
      if command -v setcap >/dev/null 2>&1; then
        run_as_root setcap cap_net_admin+ep "$helper_target"
        printf 'Installed %s with CAP_NET_ADMIN\n' "$helper_target"
      else
        printf 'Installed %s, but setcap is unavailable; host-LAN Modules need cap_net_admin+ep.\n' "$helper_target" >&2
      fi
    else
      printf 'Skipped anas-helper: root access is unavailable.\n' >&2
    fi
  fi

  if [ "$install_service" -eq 1 ]; then
    rendered_config="$work_dir/anasd.yml"
    awk -v port="$management_port" -v store="$console_store" '
      $1 == "port:" { print "port: " port; next }
      $1 == "console_store:" { print "console_store: " store; next }
      { print }
    ' "$packaged_config" >"$rendered_config"
    rendered_unit="$work_dir/anasd.service"
    awk -v binary="$daemon_target" -v config="$service_config" -v store="$console_store" '
      /^ExecStart=/ { print "ExecStart=" binary " --config " config; next }
      /^ReadWritePaths=/ { print "ReadWritePaths=-" store " -/srv/anas -/srv/anas-backups"; next }
      { print }
    ' "$packaged_unit" >"$rendered_unit"

    run_as_root install -d -m 0755 "$(dirname "$service_config")"
    run_as_root install -d -m 0755 "$(dirname "$systemd_unit")"
    run_as_root install -d -m 0700 "$console_store"
    if [ ! -e "$service_config" ]; then
      run_as_root install -m 0600 "$rendered_config" "$service_config"
    else
      run_as_root chown root:root "$service_config"
      run_as_root chmod 0600 "$service_config"
    fi
    run_as_root install -m 0644 "$rendered_unit" "$systemd_unit"
    run_as_root "$systemctl_command" daemon-reload
    run_as_root "$systemctl_command" enable "$service_name" >/dev/null
    run_as_root "$systemctl_command" restart "$service_name"
    printf 'Installed and started %s on management port %s.\n' "$service_name" "$management_port"
  elif [ -f "$daemon" ]; then
    printf 'Installed %s without a system service.\n' "$daemon_target"
  fi

  source_config_dir="$(dirname "$source_config")"
  mkdir -p "$source_config_dir"
  source_config_tmp="$source_config.tmp.$$"
  printf '%s\n' "$runtime_source" >"$source_config_tmp"
  chmod 0644 "$source_config_tmp"
  mv "$source_config_tmp" "$source_config"
  "$install_target" version
  printf 'Installed %s\n' "$install_target"
  printf 'Saved default source %s in %s\n' "$runtime_source" "$source_config"
  if [ "$runtime_source" = official-cn ]; then
    printf 'New workspaces will use CNB Modules, CNB runtime images, and mainland-China download mirrors.\n'
  fi
}

main "$@"
