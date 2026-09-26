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
  ANAS_HOSTD_CONFIG         host action installation policy (default: /etc/anas/hostd.json)
  ANAS_HOSTD_SOCKET_UNIT    host action socket unit (default: /etc/systemd/system/anas-hostd.socket)
  ANAS_HOSTD_SERVICE_UNIT   host action service template (default: /etc/systemd/system/anas-hostd@.service)
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
    *[!A-Za-z0-9_./@-]*) fail "$label contains characters unsupported by the systemd installer" ;;
  esac
}

# Stop admission before changing a single executable or installation policy.
# A running host operation must drain normally; an upgrade must not kill apt or
# overwrite a binary while its frozen invocation is still being supervised.
quiesce_host_actions() {
  [ "$install_service" -eq 1 ] || return 0
  if [ -e "$hostd_socket_unit" ] || [ -e "$hostd_service_unit" ]; then
    command -v "$systemctl_command" >/dev/null 2>&1 || fail "$systemctl_command is required to inspect host actions"
    active_host_units="$(run_as_root "$systemctl_command" list-units --no-legend --no-pager --state=activating,active,deactivating "$hostd_instance_pattern")" || fail "could not inspect active host actions; installation unchanged"
    [ -z "$active_host_units" ] || fail "a host action is still active; installation unchanged"
    socket_was_active=false
    if run_as_root "$systemctl_command" is-active --quiet "$hostd_socket_name"; then
      socket_was_active=true
    else
      socket_state=$?
      case "$socket_state" in 3|4) ;; *) fail "could not inspect the host action socket" ;; esac
    fi
    run_as_root "$systemctl_command" stop "$hostd_socket_name" >/dev/null
    active_host_units="$(run_as_root "$systemctl_command" list-units --no-legend --no-pager --state=activating,active,deactivating "$hostd_instance_pattern")" || fail "could not confirm host action drain; binaries unchanged"
    if [ -n "$active_host_units" ]; then
      if [ "$socket_was_active" = true ]; then run_as_root "$systemctl_command" start "$hostd_socket_name" >/dev/null; fi
      fail "a host action started during upgrade admission; binaries unchanged"
    fi
    if [ -e "$systemd_unit" ]; then run_as_root "$systemctl_command" stop "$service_name" >/dev/null; fi
    run_as_root "$systemctl_command" stop "$hostd_instance_pattern" >/dev/null
  elif [ -e "$systemd_unit" ]; then
    run_as_root "$systemctl_command" stop "$service_name" >/dev/null
  fi
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
  hostd_config="${ANAS_HOSTD_CONFIG:-/etc/anas/hostd.json}"
  hostd_socket_unit="${ANAS_HOSTD_SOCKET_UNIT:-/etc/systemd/system/anas-hostd.socket}"
  hostd_service_unit="${ANAS_HOSTD_SERVICE_UNIT:-/etc/systemd/system/anas-hostd@.service}"
  relay_service_unit="${ANAS_RELAY_SERVICE_UNIT:-/etc/systemd/system/anas-incus-control-relay.service}"
  systemctl_command="${ANAS_SYSTEMCTL:-systemctl}"
  management_port="${ANAS_MANAGEMENT_PORT:-8080}"
  console_store="${ANAS_CONSOLE_STORE:-/var/lib/anas/console}"
  helper_dir="${ANAS_HELPER_DIR:-/usr/local/lib/anas}"
  install_target="$install_dir/anas"
  daemon_target="$install_dir/anasd"
  helper_target="$helper_dir/anas-helper"
  hostd_target="$helper_dir/anas-hostd"
  relay_target="$helper_dir/anas-incus-control-relay"
  service_name="$(basename "$systemd_unit")"
  hostd_socket_name="$(basename "$hostd_socket_unit")"
  hostd_instance_pattern="${hostd_socket_name%.socket}@*.service"
  relay_service_name="$(basename "$relay_service_unit")"

  if [ -n "${ANAS_SOURCE_CONFIG:-}" ]; then
    source_config="$ANAS_SOURCE_CONFIG"
  else
    config_home="${XDG_CONFIG_HOME:-${HOME:?HOME is required}/.config}"
    source_config="$config_home/anas/source"
  fi

  if [ "$uninstall" = true ]; then
    os_name="$(uname -s)"
    [ "$os_name" = Linux ] || fail "only Linux is currently supported (detected $os_name)"
    quiesce_host_actions
    if [ "$install_service" -eq 1 ] && [ -e "$systemd_unit" ]; then
      command -v "$systemctl_command" >/dev/null 2>&1 || fail "$systemctl_command is required to remove the system service"
      run_as_root "$systemctl_command" disable --now "$service_name" >/dev/null
      run_as_root rm -f "$systemd_unit"
      run_as_root "$systemctl_command" daemon-reload
    fi
    if [ -e "$install_target" ] || [ -e "$daemon_target" ]; then
      run_as_root rm -f "$install_target" "$daemon_target"
    fi
    if [ "$install_service" -eq 1 ]; then
      if [ -e "$hostd_socket_unit" ] || [ -e "$hostd_service_unit" ]; then
        command -v "$systemctl_command" >/dev/null 2>&1 || fail "$systemctl_command is required to remove the host action service"
        run_as_root "$systemctl_command" disable --now "$hostd_socket_name" >/dev/null
        run_as_root "$systemctl_command" stop "$hostd_instance_pattern" >/dev/null
        run_as_root rm -f "$hostd_socket_unit" "$hostd_service_unit"
        if [ -e "$relay_service_unit" ]; then
          run_as_root "$systemctl_command" stop "$relay_service_name" >/dev/null
          run_as_root rm -f "$relay_service_unit"
        fi
        run_as_root "$systemctl_command" daemon-reload
      fi
      if [ -e "$hostd_config" ]; then run_as_root rm -f "$hostd_config"; fi
      if [ -e "$hostd_target" ]; then run_as_root rm -f "$hostd_target"; fi
      if [ -e "$relay_target" ]; then run_as_root rm -f "$relay_target"; fi
      if [ -d /run/anas-job-broker ]; then run_as_root rmdir /run/anas-job-broker 2>/dev/null || true; fi
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
  for optional in anasd anas-helper anasd.service anasd.yml anas-hostd anas-hostd.socket anas-hostd@.service anas-incus-control-relay anas-incus-control-relay.service release.json; do
    tar -xzf "$archive" -C "$work_dir/extract" "$archive_dir/$optional" 2>/dev/null || true
  done
  binary="$work_dir/extract/$archive_dir/anas"
  daemon="$work_dir/extract/$archive_dir/anasd"
  helper="$work_dir/extract/$archive_dir/anas-helper"
  packaged_unit="$work_dir/extract/$archive_dir/anasd.service"
  packaged_config="$work_dir/extract/$archive_dir/anasd.yml"
  hostd="$work_dir/extract/$archive_dir/anas-hostd"
  relay="$work_dir/extract/$archive_dir/anas-incus-control-relay"
  packaged_hostd_socket="$work_dir/extract/$archive_dir/anas-hostd.socket"
  packaged_hostd_service="$work_dir/extract/$archive_dir/anas-hostd@.service"
  packaged_relay_service="$work_dir/extract/$archive_dir/anas-incus-control-relay.service"
  release_json="$work_dir/extract/$archive_dir/release.json"
  [ -f "$binary" ] || fail "release archive does not contain anas"
  reported_version_output="$("$binary" version)" || fail "downloaded anas binary could not report its version"
  reported_version="$(printf '%s\n' "$reported_version_output" | awk 'NR == 1 && $1 == "anas" { print $2 }')"
  [ "$reported_version" = "$version" ] || fail "release tag $tag contains anas ${reported_version:-<unknown>}"
  release_commit=""
  if [ -f "$release_json" ]; then
    release_version="$(sed -n 's/^[[:space:]]*"version":[[:space:]]*"\([^"]*\)".*/\1/p' "$release_json" | head -n 1)"
    release_commit="$(sed -n 's/^[[:space:]]*"commit":[[:space:]]*"\([0-9a-f]\{40\}\)".*/\1/p' "$release_json" | head -n 1)"
    [ "$release_version" = "$version" ] || fail "release.json version does not match $tag"
    [ -n "$release_commit" ] || fail "release.json does not contain a full lowercase commit"
  fi

  # Validate every service input before replacing any installed file. Legacy
  # CLI-only archives remain usable with --no-service, while a service install
  # cannot fail halfway through because one packaged member was absent.
  if [ "$install_service" -eq 1 ]; then
    [ -f "$daemon" ] || fail "release archive does not contain anasd"
    [ -f "$hostd" ] || fail "release archive does not contain anas-hostd"
    [ -f "$relay" ] || fail "release archive does not contain anas-incus-control-relay"
    [ -f "$packaged_unit" ] || fail "release archive does not contain anasd.service"
    [ -f "$packaged_config" ] || fail "release archive does not contain anasd.yml"
    [ -f "$packaged_hostd_socket" ] || fail "release archive does not contain anas-hostd.socket"
    [ -f "$packaged_hostd_service" ] || fail "release archive does not contain anas-hostd@.service"
    [ -f "$packaged_relay_service" ] || fail "release archive does not contain anas-incus-control-relay.service"
    [ -f "$release_json" ] || fail "release archive does not contain release.json"
    validate_service_path "ANAS_INSTALL_DIR" "$install_dir"
    validate_service_path "ANAS_SERVICE_CONFIG" "$service_config"
    validate_service_path "ANAS_SYSTEMD_UNIT" "$systemd_unit"
    validate_service_path "ANAS_HOSTD_CONFIG" "$hostd_config"
    validate_service_path "ANAS_HOSTD_SOCKET_UNIT" "$hostd_socket_unit"
    validate_service_path "ANAS_HOSTD_SERVICE_UNIT" "$hostd_service_unit"
    validate_service_path "ANAS_RELAY_SERVICE_UNIT" "$relay_service_unit"
    validate_service_path "ANAS_CONSOLE_STORE" "$console_store"
    case "$management_port" in ''|*[!0-9]*) fail "ANAS_MANAGEMENT_PORT must be an integer between 1 and 65535" ;; esac
    [ "$management_port" -ge 1 ] && [ "$management_port" -le 65535 ] || fail "ANAS_MANAGEMENT_PORT must be between 1 and 65535"
    command -v "$systemctl_command" >/dev/null 2>&1 || fail "$systemctl_command is required unless --no-service is used"
    hostd_version_output="$("$hostd" --version)" || fail "downloaded anas-hostd could not report its version"
    hostd_version="$(printf '%s\n' "$hostd_version_output" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p')"
    hostd_commit="$(printf '%s\n' "$hostd_version_output" | sed -n 's/.*"commit":"\([0-9a-f]\{40\}\)".*/\1/p')"
    [ "$hostd_version" = "$version" ] || fail "release tag $tag contains anas-hostd ${hostd_version:-<unknown>}"
    [ "$hostd_commit" = "$release_commit" ] || fail "release archive contains mismatched anas-hostd commit"
  fi

  quiesce_host_actions
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
    rendered_hostd_socket="$work_dir/anas-hostd.socket"
    cp "$packaged_hostd_socket" "$rendered_hostd_socket"
    rendered_hostd_service="$work_dir/anas-hostd@.service"
    awk -v binary="$hostd_target" '
      /^ExecStart=/ { print "ExecStart=" binary " --serve"; next }
      { print }
    ' "$packaged_hostd_service" >"$rendered_hostd_service"
    rendered_relay_service="$work_dir/anas-incus-control-relay.service"
    awk -v binary="$relay_target" '
      /^ExecStart=/ { print "ExecStart=" binary " --config /run/anas-incus-control-relay.json"; next }
      { print }
    ' "$packaged_relay_service" >"$rendered_relay_service"
    rendered_hostd_config="$work_dir/hostd.json"
    printf '{"schema":"anas.host-action-installation/v2","release":{"version":"%s","commit":"%s"},"service_mode":"systemd-root-service","service_unit":"%s","socket_gid":0}' \
      "$version" "$release_commit" "$service_name" >"$rendered_hostd_config"

    run_as_root install -d -m 0755 "$(dirname "$service_config")"
    run_as_root install -d -m 0755 "$(dirname "$systemd_unit")"
    run_as_root install -d -m 0755 "$(dirname "$hostd_config")"
    run_as_root install -d -m 0755 "$(dirname "$hostd_socket_unit")"
    run_as_root install -d -m 0755 "$(dirname "$hostd_service_unit")"
    run_as_root install -d -m 0755 "$(dirname "$relay_service_unit")"
    run_as_root install -d -m 0755 "$helper_dir"
    run_as_root install -d -m 0700 "$console_store"
    if [ -w /run ]; then
      run_as_root install -d -m 0700 /run/anas-job-broker
    fi
    run_as_root install -m 0755 "$hostd" "$hostd_target"
    run_as_root install -m 0755 "$relay" "$relay_target"
    if [ ! -e "$service_config" ]; then
      run_as_root install -m 0600 "$rendered_config" "$service_config"
    else
      run_as_root chown root:root "$service_config"
      run_as_root chmod 0600 "$service_config"
    fi
    run_as_root install -m 0600 "$rendered_hostd_config" "$hostd_config"
    run_as_root install -m 0644 "$rendered_unit" "$systemd_unit"
    run_as_root install -m 0644 "$rendered_hostd_socket" "$hostd_socket_unit"
    run_as_root install -m 0644 "$rendered_hostd_service" "$hostd_service_unit"
    run_as_root install -m 0644 "$rendered_relay_service" "$relay_service_unit"
    run_as_root "$systemctl_command" daemon-reload
    run_as_root "$systemctl_command" enable "$hostd_socket_name" >/dev/null
    run_as_root "$systemctl_command" restart "$hostd_socket_name"
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
