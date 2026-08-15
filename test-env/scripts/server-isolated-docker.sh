#!/usr/bin/env sh
set -eu

NS=${ANAS_TEST_NETNS:-anas-test}
HOST_VETH=${ANAS_TEST_HOST_VETH:-anas-test-host}
PEER_VETH=${ANAS_TEST_PEER_VETH:-anas-test-peer}
MACVLAN_PARENT=${ANAS_TEST_MACVLAN_PARENT:-enp3s0}
HOST_ADDR=${ANAS_TEST_HOST_ADDR:-10.254.0.1/24}
NS_ADDR=${ANAS_TEST_NS_ADDR:-10.254.0.2/24}
NS_GATEWAY=${ANAS_TEST_NS_GATEWAY:-10.254.0.1}
NS_SUBNET=${ANAS_TEST_NS_SUBNET:-10.254.0.0/24}
SOCKET=${ANAS_TEST_DOCKER_SOCKET:-/run/anas-docker-test.sock}
DATA_ROOT=${ANAS_TEST_DOCKER_ROOT:-/data/anas-docker-test}
EXEC_ROOT=${ANAS_TEST_DOCKER_EXEC_ROOT:-/run/anas-docker-test}
CONFIG=${ANAS_TEST_DOCKER_CONFIG:-/home/whl/anas-refactor-test/test-env/server-docker-daemon.json}
UNIT=${ANAS_TEST_DOCKER_UNIT:-anas-test-docker-netns.service}
PID_FILE=${ANAS_TEST_DOCKER_PID_FILE:-/run/anas-docker-test.pid}
CONTAINERD_NAMESPACE=${ANAS_TEST_CONTAINERD_NAMESPACE:-anas-test}
CONTAINERD_PLUGINS_NAMESPACE=${ANAS_TEST_CONTAINERD_PLUGINS_NAMESPACE:-anas-test-plugins}
DOCKER_BIP=${ANAS_TEST_DOCKER_BIP:-172.30.0.1/24}
DOCKER_ADDRESS_POOL=${ANAS_TEST_DOCKER_ADDRESS_POOL:-172.31.0.0/16}

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "run as root" >&2
    exit 1
  fi
}

uplink_interface() {
  ip route show default | awk 'NR == 1 { print $5 }'
}

status() {
  systemctl is-active "$UNIT" 2>/dev/null || true
  ip netns list | grep -F "$NS" || true
  if [ -S "$SOCKET" ]; then
    docker -H "unix://$SOCKET" info --format 'root={{.DockerRootDir}} driver={{.Driver}} server={{.ServerVersion}}'
  fi
}

start() {
  require_root
  uplink=$(uplink_interface)
  if [ -z "$uplink" ]; then
    echo "could not detect uplink interface" >&2
    exit 1
  fi
  dns_upstream=${ANAS_TEST_DNS_UPSTREAM:-$(resolvectl dns "$uplink" 2>/dev/null | awk '{ for (i = 1; i <= NF; i++) if ($i ~ /^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$/) { print $i; exit } }')}
  dns_upstream=${dns_upstream:-1.1.1.1}

  systemctl stop anas-test-docker-v3.service 2>/dev/null || true
  ip link delete anas-docker0 2>/dev/null || true

  if ! ip netns list | grep -Eq "^${NS}( |$)"; then
    ip netns add "$NS"
  fi
  if ! ip link show "$HOST_VETH" >/dev/null 2>&1; then
    ip link add "$HOST_VETH" type veth peer name "$PEER_VETH"
    ip link set "$PEER_VETH" netns "$NS"
  fi
  ip addr replace "$HOST_ADDR" dev "$HOST_VETH"
  ip link set "$HOST_VETH" up
  ip netns exec "$NS" ip link set lo up
  ip netns exec "$NS" ip addr replace "$NS_ADDR" dev "$PEER_VETH"
  ip netns exec "$NS" ip link set "$PEER_VETH" up
  if ! ip netns exec "$NS" ip link show "$MACVLAN_PARENT" >/dev/null 2>&1; then
    ip netns exec "$NS" ip link add "$MACVLAN_PARENT" type dummy
  fi
  ip netns exec "$NS" ip link set "$MACVLAN_PARENT" up
  ip netns exec "$NS" ip route replace default via "$NS_GATEWAY"
  ip netns exec "$NS" sysctl -w net.ipv6.conf.all.disable_ipv6=1 >/dev/null
  ip netns exec "$NS" sysctl -w net.ipv6.conf.default.disable_ipv6=1 >/dev/null
  ip netns exec "$NS" sysctl -w net.ipv4.conf.all.route_localnet=1 >/dev/null
  ip netns exec "$NS" iptables -t nat -C OUTPUT -d 127.0.0.53 -p udp --dport 53 -j DNAT --to-destination "$dns_upstream:53" 2>/dev/null || \
    ip netns exec "$NS" iptables -t nat -A OUTPUT -d 127.0.0.53 -p udp --dport 53 -j DNAT --to-destination "$dns_upstream:53"
  ip netns exec "$NS" iptables -t nat -C OUTPUT -d 127.0.0.53 -p tcp --dport 53 -j DNAT --to-destination "$dns_upstream:53" 2>/dev/null || \
    ip netns exec "$NS" iptables -t nat -A OUTPUT -d 127.0.0.53 -p tcp --dport 53 -j DNAT --to-destination "$dns_upstream:53"
  ip netns exec "$NS" iptables -t nat -C POSTROUTING -d "$dns_upstream" -p udp --dport 53 -j SNAT --to-source "${NS_ADDR%/*}" 2>/dev/null || \
    ip netns exec "$NS" iptables -t nat -A POSTROUTING -d "$dns_upstream" -p udp --dport 53 -j SNAT --to-source "${NS_ADDR%/*}"
  ip netns exec "$NS" iptables -t nat -C POSTROUTING -d "$dns_upstream" -p tcp --dport 53 -j SNAT --to-source "${NS_ADDR%/*}" 2>/dev/null || \
    ip netns exec "$NS" iptables -t nat -A POSTROUTING -d "$dns_upstream" -p tcp --dport 53 -j SNAT --to-source "${NS_ADDR%/*}"

  mkdir -p "/etc/netns/$NS"
  printf 'nameserver 223.5.5.5\nnameserver 1.1.1.1\n' >"/etc/netns/$NS/resolv.conf"
  sysctl -w net.ipv4.ip_forward=1 >/dev/null
  iptables -t nat -C POSTROUTING -s "$NS_SUBNET" -o "$uplink" -j MASQUERADE 2>/dev/null || \
    iptables -t nat -A POSTROUTING -s "$NS_SUBNET" -o "$uplink" -j MASQUERADE
  iptables -C FORWARD -i "$HOST_VETH" -o "$uplink" -j ACCEPT 2>/dev/null || \
    iptables -A FORWARD -i "$HOST_VETH" -o "$uplink" -j ACCEPT
  iptables -C FORWARD -i "$uplink" -o "$HOST_VETH" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || \
    iptables -A FORWARD -i "$uplink" -o "$HOST_VETH" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT

  systemctl stop "$UNIT" 2>/dev/null || true
  systemctl reset-failed "$UNIT" 2>/dev/null || true
  systemd-run --unit="${UNIT%.service}" --collect --property=Restart=on-failure \
    /usr/bin/nsenter --net="/var/run/netns/$NS" /usr/bin/dockerd \
    --config-file="$CONFIG" \
    --data-root="$DATA_ROOT" \
    --exec-root="$EXEC_ROOT" \
    --containerd-namespace="$CONTAINERD_NAMESPACE" \
    --containerd-plugins-namespace="$CONTAINERD_PLUGINS_NAMESPACE" \
    --pidfile="$PID_FILE" \
    --host="unix://$SOCKET" \
    --bip="$DOCKER_BIP" \
    --default-address-pool="base=$DOCKER_ADDRESS_POOL,size=24"

  i=0
  until docker -H "unix://$SOCKET" info >/dev/null 2>&1; do
    i=$((i + 1))
    if [ "$i" -ge 30 ]; then
      journalctl -u "$UNIT" -n 100 --no-pager >&2
      exit 1
    fi
    sleep 2
  done
  chgrp docker "$SOCKET"
  chmod 660 "$SOCKET"
  status
}

stop() {
  require_root
  uplink=$(uplink_interface)
  systemctl stop "$UNIT" 2>/dev/null || true
  iptables -t nat -D POSTROUTING -s "$NS_SUBNET" -o "$uplink" -j MASQUERADE 2>/dev/null || true
  iptables -D FORWARD -i "$HOST_VETH" -o "$uplink" -j ACCEPT 2>/dev/null || true
  iptables -D FORWARD -i "$uplink" -o "$HOST_VETH" -m conntrack --ctstate RELATED,ESTABLISHED -j ACCEPT 2>/dev/null || true
  ip link delete "$HOST_VETH" 2>/dev/null || true
  ip netns delete "$NS" 2>/dev/null || true
  rm -rf "/etc/netns/$NS"
}

case "${1:-status}" in
  start) start ;;
  stop) stop ;;
  restart) stop; start ;;
  status) status ;;
  *) echo "usage: $0 {start|stop|restart|status}" >&2; exit 2 ;;
esac
