#!/usr/bin/env sh
set -eu

github_release_root_default="https://github.com/anas-project/ANAS/releases"
cnb_release_root_default="https://cnb.cool/anas.dev/ANAS/-/releases"

usage() {
  cat <<'EOF'
Install the latest ANAS Core release on Linux.

Usage:
  install.sh [--source github|cn] [--install-dir DIR]

Environment:
  ANAS_INSTALL_SOURCE       github (default) or cn
  ANAS_INSTALL_DIR          binary destination (default: /usr/local/bin)
  ANAS_SOURCE_CONFIG        source preference file (default: $XDG_CONFIG_HOME/anas/source)
EOF
}

fail() {
  printf 'anas installer: %s\n' "$*" >&2
  exit 1
}

source_name="${ANAS_INSTALL_SOURCE:-github}"
install_dir="${ANAS_INSTALL_DIR:-/usr/local/bin}"
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
    -h|--help)
      usage
      exit 0
      ;;
    *)
      fail "unknown option: $1"
      ;;
  esac
done

case "$source_name" in
  github)
    release_root="$github_release_root_default"
    runtime_source="official"
    ;;
  cn)
    release_root="$cnb_release_root_default"
    runtime_source="official-cn"
    ;;
  *)
    fail "unsupported source '$source_name' (expected github or cn)"
    ;;
esac

os_name="$(uname -s)"
case "$os_name" in
  Linux) ;;
  *) fail "only Linux is currently supported (detected $os_name)" ;;
esac

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
latest_effective_url="$(
  curl --proto '=https' --tlsv1.2 -fsSL --retry 3 \
    -o /dev/null -w '%{url_effective}' "$latest_url"
)"
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
  if [ -n "$candidate_sum" ]; then
    asset="$candidate"
    expected="$candidate_sum"
    break
  fi
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
binary="$work_dir/extract/$archive_dir/anas"
[ -f "$binary" ] || fail "release archive does not contain anas"

# Verify the downloaded payload before replacing an existing installation or
# writing source preferences. This remains compatible with legacy archives
# that predate release.json while preventing a tag/binary identity mismatch.
reported_version_output="$("$binary" version)" || fail "downloaded anas binary could not report its version"
reported_version="$(printf '%s\n' "$reported_version_output" | awk 'NR == 1 && $1 == "anas" { print $2 }')"
[ "$reported_version" = "$version" ] || fail "release tag $tag contains anas ${reported_version:-<unknown>}"

# The privileged helper is optional at install time: only a deployment with a
# module that attaches to the host LAN needs it, and extracting it is allowed to
# fail on a release that predates it.
tar -xzf "$archive" -C "$work_dir/extract" "$archive_dir/anas-helper" 2>/dev/null || true
helper="$work_dir/extract/$archive_dir/anas-helper"

install_target="$install_dir/anas"
if [ ! -d "$install_dir" ]; then
  mkdir -p "$install_dir" 2>/dev/null || true
fi
if [ -d "$install_dir" ] && [ -w "$install_dir" ]; then
  install -m 0755 "$binary" "$install_target"
elif [ "$(id -u)" -eq 0 ]; then
  install -d -m 0755 "$install_dir"
  install -m 0755 "$binary" "$install_target"
else
  command -v sudo >/dev/null 2>&1 || fail "$install_dir is not writable and sudo is unavailable; use --install-dir"
  sudo install -d -m 0755 "$install_dir"
  sudo install -m 0755 "$binary" "$install_target"
fi

# anas-helper performs the one thing anas cannot do as an ordinary user:
# create the macvlan bridge a host-LAN module needs. It is installed root-owned,
# outside the writable install dir, and granted exactly CAP_NET_ADMIN -- not
# setuid root, and not a sudoers rule pointing at a file anas itself writes.
#
# setcap has to be re-run on every upgrade: replacing the file drops the
# capability with it, and the failure that follows ("needs CAP_NET_ADMIN")
# arrives a long way from its cause.
if [ -f "$helper" ]; then
  helper_dir="${ANAS_HELPER_DIR:-/usr/local/lib/anas}"
  helper_target="$helper_dir/anas-helper"
  if [ "$(id -u)" -eq 0 ]; then
    as_root=""
  elif command -v sudo >/dev/null 2>&1; then
    as_root="sudo"
  else
    as_root="unavailable"
  fi
  if [ "$as_root" = unavailable ]; then
    printf 'Skipped anas-helper: %s needs root and sudo is unavailable.\n' "$helper_dir" >&2
    printf 'Deployments with a host-LAN module will not start until it is installed.\n' >&2
  else
    $as_root install -d -m 0755 "$helper_dir"
    $as_root install -m 0755 "$helper" "$helper_target"
    if command -v setcap >/dev/null 2>&1; then
      $as_root setcap cap_net_admin+ep "$helper_target"
      printf 'Installed %s with CAP_NET_ADMIN\n' "$helper_target"
    else
      printf 'Installed %s, but setcap is not available.\n' "$helper_target" >&2
      printf 'Install libcap2-bin (or libcap) and run: sudo setcap cap_net_admin+ep %s\n' "$helper_target" >&2
    fi
  fi
fi

if [ -n "${ANAS_SOURCE_CONFIG:-}" ]; then
  source_config="$ANAS_SOURCE_CONFIG"
else
  config_home="${XDG_CONFIG_HOME:-${HOME:?HOME is required}/.config}"
  source_config="$config_home/anas/source"
fi
source_config_dir=$(dirname "$source_config")
mkdir -p "$source_config_dir"
source_config_tmp="$source_config.tmp.$$"
printf '%s\n' "$runtime_source" >"$source_config_tmp"
chmod 0644 "$source_config_tmp"
mv "$source_config_tmp" "$source_config"

"$install_target" version
printf 'Installed %s\n' "$install_target"
printf 'Saved default source %s in %s\n' "$runtime_source" "$source_config"
if [ "$runtime_source" = "official-cn" ]; then
  printf 'New workspaces will use CNB Modules, CNB runtime images, and mainland-China download mirrors.\n'
fi
