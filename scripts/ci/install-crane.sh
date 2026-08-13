#!/usr/bin/env bash
set -euo pipefail

version=v0.21.9
install_dir="${1:-${RUNNER_TEMP:-/tmp}/anas-bin}"
if [[ "$(uname -s)" != "Linux" ]]; then
  echo "the CI crane installer currently supports Linux only" >&2
  exit 1
fi
machine="$(uname -m)"
case "$machine" in
  x86_64)
    archive=go-containerregistry_Linux_x86_64.tar.gz
    expected_sha256=5c16d8ddb971cb1d5e6ed8b1e743da8224414eeba2c2762d8f1a61b2f095699e
    ;;
  aarch64|arm64)
    archive=go-containerregistry_Linux_arm64.tar.gz
    expected_sha256=1f4c647b7bb260ab5435661df5b526cf59950ebf95201790db7183ac189cbcbd
    ;;
  *)
    echo "unsupported crane architecture: $machine" >&2
    exit 1
    ;;
esac

mkdir -p "$install_dir"
if [[ -x "$install_dir/crane" ]] && "$install_dir/crane" version | grep -Fq "$version"; then
  echo "crane $version is already installed in $install_dir"
  exit 0
fi

download_dir="$(mktemp -d)"
trap 'rm -rf "$download_dir"' EXIT
url="https://github.com/google/go-containerregistry/releases/download/${version}/${archive}"
curl --fail --location --silent --show-error \
  --retry 5 --retry-delay 2 --retry-all-errors \
  --output "$download_dir/$archive" "$url"
printf '%s  %s\n' "$expected_sha256" "$download_dir/$archive" | sha256sum --check --status
tar -xzf "$download_dir/$archive" -C "$install_dir" crane
chmod 0755 "$install_dir/crane"
"$install_dir/crane" version
