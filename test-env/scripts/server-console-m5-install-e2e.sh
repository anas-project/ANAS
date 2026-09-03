#!/usr/bin/env bash
set -euo pipefail

if [[ $# -ne 4 ]]; then
  echo "usage: $0 <linux-amd64-archive> <install.sh> <7700-7799-port> <run-id>" >&2
  exit 2
fi

archive="$(realpath "$1")"
installer="$(realpath "$2")"
port="$3"
run_id="$4"
[[ -f "$archive" && -f "$installer" ]] || { echo "archive and installer must exist" >&2; exit 2; }
[[ "$port" =~ ^77[0-9][0-9]$ ]] || { echo "port must be in 7700-7799" >&2; exit 2; }
[[ "$run_id" =~ ^[a-z0-9-]{1,32}$ ]] || { echo "run-id must be lowercase ASCII" >&2; exit 2; }

work_dir="$(mktemp -d "/tmp/anas-m5-${run_id}.XXXXXX")"
release_dir="$work_dir/release"
fake_bin="$work_dir/bin"
install_dir="/usr/local/lib/anas-m5-${run_id}"
service_config="/etc/anas/anasd-m5-${run_id}.yml"
systemd_unit="/etc/systemd/system/anasd-m5-${run_id}.service"
service_name="anasd-m5-${run_id}.service"
console_root="/var/lib/anas-m5-${run_id}"
console_store="$console_root/console"
source_config="$console_root/source"
helper_dir="$install_dir/helper"

cleanup() {
  sudo systemctl disable --now "$service_name" >/dev/null 2>&1 || true
  sudo rm -f "$systemd_unit" "$service_config"
  sudo rm -rf "$install_dir" "$console_root"
  sudo systemctl daemon-reload >/dev/null 2>&1 || true
  rm -rf "$work_dir"
}
trap cleanup EXIT HUP INT TERM

if ss -ltn "sport = :$port" | tail -n +2 | grep -q .; then
  echo "port $port is already in use" >&2
  exit 1
fi

mkdir -p "$release_dir" "$fake_bin"
cp "$archive" "$release_dir/anas_linux_amd64.tar.gz"
(
  cd "$release_dir"
  sha256sum anas_linux_amd64.tar.gz >SHA256SUMS
)

cat >"$fake_bin/curl" <<'EOF'
#!/usr/bin/env sh
set -eu
output=
url=
write_format=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o) output="$2"; shift 2 ;;
    -w) write_format="$2"; shift 2 ;;
    --proto|--retry) shift 2 ;;
    --tlsv1.2|-fsSL) shift ;;
    *) url="$1"; shift ;;
  esac
done
if [ -n "$write_format" ]; then
  printf 'https://fixture.invalid/releases/tag/v0.8.0'
  exit 0
fi
cp "$ANAS_M5_RELEASE_DIR/${url##*/}" "$output"
EOF
chmod 0755 "$fake_bin/curl"

install_env=(
  "PATH=$fake_bin:/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin"
  "ANAS_M5_RELEASE_DIR=$release_dir"
  "ANAS_INSTALL_SOURCE=github"
  "ANAS_INSTALL_DIR=$install_dir"
  "ANAS_INSTALL_SERVICE=1"
  "ANAS_SERVICE_CONFIG=$service_config"
  "ANAS_SYSTEMD_UNIT=$systemd_unit"
  "ANAS_MANAGEMENT_PORT=$port"
  "ANAS_CONSOLE_STORE=$console_store"
  "ANAS_HELPER_DIR=$helper_dir"
  "ANAS_SOURCE_CONFIG=$source_config"
)

sudo env "${install_env[@]}" sh "$installer"
sudo systemctl is-active --quiet "$service_name"
for _ in {1..50}; do
  if curl -fsS "http://127.0.0.1:$port/healthz" | grep -q '"status":"ok"'; then break; fi
  sleep 0.1
done
curl -fsS "http://127.0.0.1:$port/healthz" | grep -q '"status":"ok"'
[[ "$(stat -c '%U:%G:%a' "$service_config")" == root:root:600 ]]
[[ "$(stat -c '%U:%G:%a' "$systemd_unit")" == root:root:644 ]]
file "$install_dir/anasd" | grep -q 'statically linked'
sudo grep -qx "port: $port" "$service_config"
grep -Fqx "ExecStart=$install_dir/anasd --config $service_config" "$systemd_unit"
grep -Fqx "ReadWritePaths=-$console_store -/srv/anas -/srv/anas-backups" "$systemd_unit"
grep -qx 'User=root' "$systemd_unit"
grep -qx 'Group=root' "$systemd_unit"
grep -qx 'ProtectSystem=strict' "$systemd_unit"
sudo touch "$console_store/preserve-me"

config_before="$(sudo sha256sum "$service_config" | awk '{print $1}')"
sudo env "${install_env[@]}" ANAS_MANAGEMENT_PORT=7799 sh "$installer"
config_after="$(sudo sha256sum "$service_config" | awk '{print $1}')"
[[ "$config_before" == "$config_after" ]]
sudo systemctl is-active --quiet "$service_name"
curl -fsS "http://127.0.0.1:$port/healthz" | grep -q '"status":"ok"'

sudo env "${install_env[@]}" sh "$installer" --uninstall
[[ ! -e "$install_dir/anas" && ! -e "$install_dir/anasd" && ! -e "$systemd_unit" ]]
sudo test -e "$service_config"
sudo test -e "$console_store/preserve-me"
sudo env "${install_env[@]}" sh "$installer" --uninstall --purge
sudo test ! -e "$service_config"
sudo test -e "$console_store/preserve-me"

echo "PASS: R-162 install/upgrade/uninstall, root config, systemd unit, and management port"
