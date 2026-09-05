#!/usr/bin/env sh
# Require the upgrade runner and its isolated Docker daemon to share the
# explicitly named test network namespace. Otherwise host-LAN discovery can
# inspect the production namespace while Compose runs elsewhere.

anas_upgrade_netns_fail() {
  printf 'refusing Module upgrade E2E outside its test network namespace: %s\n' "$1" >&2
  exit 2
}

[ "$(uname -s)" = Linux ] || anas_upgrade_netns_fail "Linux is required"
anas_upgrade_netns_path=${ANAS_UPGRADE_NETNS_PATH:-}
case "$anas_upgrade_netns_path" in
  /run/netns/anas-*|/var/run/netns/anas-*) ;;
  "") anas_upgrade_netns_fail "ANAS_UPGRADE_NETNS_PATH is required" ;;
  *) anas_upgrade_netns_fail "unsafe namespace path: $anas_upgrade_netns_path" ;;
esac
[ -e "$anas_upgrade_netns_path" ] || anas_upgrade_netns_fail "namespace does not exist: $anas_upgrade_netns_path"

anas_upgrade_current_netns=$(stat -Lc '%d:%i' /proc/self/ns/net 2>/dev/null) ||
  anas_upgrade_netns_fail "cannot inspect current namespace"
anas_upgrade_expected_netns=$(stat -Lc '%d:%i' "$anas_upgrade_netns_path" 2>/dev/null) ||
  anas_upgrade_netns_fail "cannot inspect namespace: $anas_upgrade_netns_path"
[ "$anas_upgrade_current_netns" = "$anas_upgrade_expected_netns" ] ||
  anas_upgrade_netns_fail "run with sudo nsenter --net=$anas_upgrade_netns_path --"

unset anas_upgrade_current_netns anas_upgrade_expected_netns
