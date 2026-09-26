#!/bin/bash
# Disposable QEMU-only preparation. This must never be run on the SSH server
# itself. Unlike the stop fixture, do NOT enable forwarding before Docker:
# the native experiment explicitly requires Docker's default DROP policy.
set -euo pipefail
identity=${1:?exact disposable VM cloud-init identity required}
[[ "$identity" =~ ^anas-incus-host-[a-f0-9]{6}$ ]]
test "$(id -u)" = 0
test "$(cat /var/lib/cloud/data/instance-id)" = "$identity"
test "$(cat /sys/class/dmi/id/sys_vendor)" = QEMU
test ! -e /run/docker.sock && test ! -d /var/lib/docker
test ! -e /var/lib/incus/unix.socket && test ! -d /var/lib/anas-forwarding-docker
test ! -e /usr/sbin/policy-rc.d
test "$(cat /proc/sys/net/ipv4/ip_forward)" = 0
umask 077
printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d
chmod 0755 /usr/sbin/policy-rc.d
owned_policy=$(sha256sum /usr/sbin/policy-rc.d | cut -d' ' -f1)
cleanup_policy() {
  if test "$(sha256sum /usr/sbin/policy-rc.d | cut -d' ' -f1)" = "$owned_policy"; then
    rm /usr/sbin/policy-rc.d
  else
    printf '%s\n' 'Temporary package-start policy identity changed; retained.' >&2
    return 1
  fi
}
trap cleanup_policy EXIT
export DEBIAN_FRONTEND=noninteractive
apt-get -o Acquire::Retries=2 -o Acquire::http::Timeout=30 update
apt-get -o Acquire::Retries=2 -o Acquire::http::Timeout=30 install -y --no-install-recommends \
  docker.io incus dnsmasq-base nftables iptables iproute2 python3
test "$(dpkg-query -W -f='${Status}' dnsmasq-base)" = 'install ok installed'
test -x /usr/sbin/dnsmasq
test ! -e /run/docker.sock
test "$(cat /proc/sys/net/ipv4/ip_forward)" = 0
install -d -m 0700 /var/lib/anas-forwarding-docker /run/anas-forwarding-docker
printf '{}\n' > /run/anas-forwarding-docker/daemon.json
systemd-run --unit=anas-forwarding-docker --collect \
  --property=RuntimeMaxSec=1800 --property=KillMode=control-group \
  /usr/bin/dockerd --config-file=/run/anas-forwarding-docker/daemon.json \
  --host=unix:///run/docker.sock --data-root=/var/lib/anas-forwarding-docker \
  --exec-root=/run/anas-forwarding-docker --pidfile=/run/anas-forwarding-docker/daemon.pid \
  --storage-driver=overlay2
for attempt in $(seq 1 60); do
  if test "$(docker info --format '{{.DockerRootDir}}' 2>/dev/null)" = /var/lib/anas-forwarding-docker; then break; fi
  test "$attempt" != 60
  sleep 1
done
test "$(cat /proc/sys/net/ipv4/ip_forward)" = 1
iptables -S FORWARD | grep -Fx -- '-P FORWARD DROP'
timeout 90 systemctl start incus.service
timeout 60 incus admin waitready
printf '%s\n' 'Fresh experimental Docker/Incus ready; default DROP was not changed.'
