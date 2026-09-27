# Sourced by the install scripts; refuses anything but the owner's disposable VM.
set -euo pipefail
test "$(id -u)" = 0
test "$(cat /var/lib/cloud/data/instance-id)" = "$1"
test "$(cat /sys/class/dmi/id/sys_vendor)" = QEMU
test ! -e /var/run/docker.sock && test ! -d /var/lib/docker
export DEBIAN_FRONTEND=noninteractive
APT=(apt-get -o Acquire::Retries=3 -o Acquire::http::Timeout=60)
report() {
  incus --version
  dpkg-query -W -f='${Package} ${Version}\n' incus incus-base incus-client btrfs-progs nftables dnsmasq-base 2>/dev/null || true
  test -c /dev/kvm && echo "kvm: present" || echo "kvm: absent"
}
