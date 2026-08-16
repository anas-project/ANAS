#!/usr/bin/env sh
# End-to-end checks for the host-LAN addressing plan: where the container's
# address comes from, what reaches docker because of it, and what happens when
# something on the segment already answers for it.
#
# The CLI, config importer, hooks, renderer and activation are real. Docker,
# sudo and the two probe commands are logged boundaries, which is what makes the
# suite deterministic and keeps it from putting ICMP on a developer's network.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

anas_bin="$ROOT_DIR/.anas-test/bin/anas"
fake_bin="$TEST_ENV_DIR/fakes"
ws="$RUNTIME_DIR/host-lan"
pooled_source="$RUNTIME_DIR/host-lan-pooled.yml"
pinned_source="$RUNTIME_DIR/host-lan-pinned.yml"
unchecked_source="$RUNTIME_DIR/host-lan-unchecked.yml"
log="$REPORT_DIR/host-lan.log"
command_log="$REPORT_DIR/host-lan-docker.log"

mkdir -p "$(dirname -- "$anas_bin")"
go build -o "$anas_bin" ./cmd/anas

export PATH="$fake_bin:$PATH"
export ANAS_FAKE_DOCKER_LOG="$command_log"

anas() { "$anas_bin" "$@"; }
fail() { echo "FAIL: $*" >&2; exit 1; }
reset_commands() { : >"$command_log"; }
active_deployment() { sed -n 's/^active_deployment: //p' "$ws/.anas/state/active.yml"; }

module_env() {
  deployment=$(active_deployment)
  [ -n "$deployment" ] || fail "no active deployment"
  printf '%s\n' "$ws/.anas/deployments/$deployment/modules/$1/.env"
}

assert_env() {
  module=$1
  key=$2
  value=$3
  file=$(module_env "$module")
  [ -f "$file" ] || fail "$module has no rendered environment"
  if [ "$value" = __ABSENT__ ]; then
    if grep -q "^$key=" "$file"; then
      fail "$key should not be rendered for $module: $(grep "^$key=" "$file")"
    fi
    return
  fi
  grep -Fqx "$key=$value" "$file" || fail "$key did not render as $value for $module: $(grep "^$key=" "$file" || echo absent)"
}

assert_command() {
  grep -Eq -- "$1" "$command_log" || fail "expected a command matching: $1"
}

refute_command() {
  if grep -Eq -- "$1" "$command_log"; then
    fail "did not expect a command matching: $1"
  fi
}

write_config() {
  target=$1
  extra=$2
  {
    cat <<'YAML'
modules:
  samba_dc:
    config:
      ldap_bind_password: Initial-LDAP-Password-1!
  samba_fs: {}
global:
  base_domain: hostlan.test
  email: admin@hostlan.test
  timezone: Asia/Shanghai
  virtual_domain: true
YAML
    printf '%s' "$extra"
    cat <<'YAML'
env:
  CONTAINER_PREFIX: anashl_
  NETWORK_PREFIX: anashl_
  HOST_IP: 192.0.2.10
  INTERFACE: anas-test0
  HOST_SUBNET_MASK: "24"
  DEFAULT_GATEWAY_IP: 192.0.2.1
  HOST_DNS_SERVER: 192.0.2.1
YAML
  } >"$target"
}

write_config "$pooled_source" ""
write_config "$pinned_source" "  host_lan_ip: 192.0.2.51
  host_lan_bridge_ip: 192.0.2.50
"
write_config "$unchecked_source" "  host_lan_ip: 192.0.2.51
  host_lan_bridge_ip: 192.0.2.50
  host_lan_arp_check: false
"

rm -rf "$ws"
: >"$log"
anas init "$ws" -y >>"$log" 2>&1

{
  echo "== the default plan still allocates from the pool =="
  anas config import "$pooled_source" -w "$ws" >>"$log" 2>&1
  reset_commands
  anas apply -w "$ws" --root "$ROOT_DIR" --update-lock >>"$log" 2>&1

  # The bridge takes the pool's first address and the container the second, so
  # an existing deployment's address does not move when it upgrades onto a
  # plan that states the address instead of letting IPAM pick it.
  assert_env samba_fs HOST_LAN_IP 192.0.2.242
  assert_env samba_fs VLAN_BRIDGE_IP 192.0.2.241
  assert_env samba_fs VLAN_SEGMENT 192.0.2.240/28
  assert_command 'docker network create .*--ip-range 192\.0\.2\.240/28'
  assert_command 'docker network create .*--aux-address bridge=192\.0\.2\.241'

  # Nothing answered, so nothing blocked the create.
  assert_command 'ip -4 neigh show 192\.0\.2\.242'
  echo "OK"
}

{
  echo "== a pinned address replaces the pool rather than living inside it =="
  anas config import "$pinned_source" -w "$ws" >>"$log" 2>&1
  reset_commands
  anas apply -w "$ws" --root "$ROOT_DIR" --update-lock >>"$log" 2>&1

  assert_env samba_fs HOST_LAN_IP 192.0.2.51
  assert_env samba_fs VLAN_BRIDGE_IP 192.0.2.50
  assert_env samba_fs VLAN_SEGMENT __ABSENT__
  assert_env samba_fs VLAN_SUBNET_MASK 32
  # docker rejects a static address outside --ip-range, so the range has to go.
  refute_command 'docker network create .*--ip-range'
  assert_command 'docker network create .*--subnet 192\.0\.2\.0/24'
  echo "OK"
}

{
  echo "== the bridge script routes to an address outside its own prefix =="
  script=$(find "$ws" -name anas_service.sh | head -1)
  [ -n "$script" ] || fail "the bridge script was never generated"
  grep -q 'ADDR="192.0.2.50/32"' "$script" || fail "bridge address is not a /32: $(grep '^ADDR=' "$script")"
  grep -q 'ROUTES="192.0.2.51/32"' "$script" || fail "container address has no route: $(grep '^ROUTES=' "$script")"
  echo "OK"
}

{
  echo "== an occupied address stops the deployment instead of colliding =="
  reset_commands
  if ANAS_FAKE_LAN_OCCUPIED="192.0.2.51" anas apply -w "$ws" --root "$ROOT_DIR" >"$REPORT_DIR/host-lan-conflict.log" 2>&1; then
    fail "apply succeeded onto an address something else already answers for"
  fi
  conflict=$(cat "$REPORT_DIR/host-lan-conflict.log")
  case $conflict in
    *192.0.2.51*) : ;;
    *) fail "the error does not name the contested address: $conflict" ;;
  esac
  case $conflict in
    *02:42:ac:11:00:99*) : ;;
    *) fail "the error does not name the occupant: $conflict" ;;
  esac
  case $conflict in
    *host_lan_ip*) : ;;
    *) fail "the error does not say which setting to change: $conflict" ;;
  esac
  # The refusal has to come before anything is created, or the check would only
  # be reporting a collision it had already caused.
  refute_command 'docker network create'
  echo "OK"
}

{
  echo "== the check is a safety net, not a dependency =="
  anas config import "$unchecked_source" -w "$ws" >>"$log" 2>&1
  reset_commands
  ANAS_FAKE_LAN_OCCUPIED="192.0.2.51" anas apply -w "$ws" --root "$ROOT_DIR" --update-lock >>"$log" 2>&1 ||
    fail "an opted-out deployment must still apply"
  assert_command 'docker network create'
  refute_command 'ip -4 neigh show'
  echo "OK"
}

echo "host-lan E2E passed"
