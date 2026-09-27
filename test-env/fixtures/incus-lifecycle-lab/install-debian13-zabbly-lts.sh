#!/bin/bash
# Lab VM only: Incus 7.0 LTS from Zabbly lts-7.0, the source the product's host
# provisioning pins (INCUS-R-108). Debian packages come through the project's
# fixed mainland mirror because the lab host reaches deb.debian.org slowly over
# IPv4; apt still verifies every Release file against Debian's own keyring.
source "$(dirname "$0")/install-common.sh" "$1"
mirror=https://mirrors.aliyun.com
install -d -m 0755 /root/apt-sources-original
find /etc/apt/sources.list.d -maxdepth 1 -name '*.sources' -exec mv {} /root/apt-sources-original/ \;
[ -f /etc/apt/sources.list ] && mv /etc/apt/sources.list /root/apt-sources-original/
cat > /etc/apt/sources.list.d/anas-debian-mirror.sources <<SOURCES
Types: deb
URIs: $mirror/debian
Suites: trixie trixie-updates
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg

Types: deb
URIs: $mirror/debian-security
Suites: trixie-security
Components: main
Signed-By: /usr/share/keyrings/debian-archive-keyring.gpg
SOURCES
"${APT[@]}" update
"${APT[@]}" install -y --no-install-recommends curl gpg ca-certificates btrfs-progs nftables dnsmasq-base openssl
curl -fsSL --max-time 60 https://pkgs.zabbly.com/key.asc -o /root/zabbly.asc
fingerprint=$(gpg --show-keys --with-colons /root/zabbly.asc | awk -F: '$1=="fpr"{print $10; exit}')
test "$fingerprint" = 4EFC590696CB15B87C73A3AD82CC8797C838DCFD
install -d -m 0755 /etc/apt/keyrings
install -m 0644 /root/zabbly.asc /etc/apt/keyrings/zabbly.asc
cat > /etc/apt/sources.list.d/zabbly-incus-lts-7.0.sources <<SOURCES
Enabled: yes
Types: deb
URIs: https://pkgs.zabbly.com/incus/lts-7.0
Suites: trixie
Components: main
Architectures: $(dpkg --print-architecture)
Signed-By: /etc/apt/keyrings/zabbly.asc
SOURCES
"${APT[@]}" update
"${APT[@]}" install -y --no-install-recommends incus incus-base incus-client
report
incus --version | grep -q '^7\.0\.'
test -c /dev/kvm
