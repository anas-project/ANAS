#!/bin/sh
set -eu

[ "$#" -eq 2 ] || { echo "usage: provision.sh RUNNER_BINARY RUNNER_SHA256" >&2; exit 64; }
runner_binary=$1
runner_sha256=$2
case "$runner_sha256" in *[!0-9a-f]*|'') exit 64 ;; esac
[ "${#runner_sha256}" -eq 64 ] || exit 64
printf '%s  %s\n' "$runner_sha256" "$runner_binary" | sha256sum -c -

apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    ca-certificates git podman slirp4netns uidmap fuse-overlayfs util-linux coreutils
rm -rf /var/lib/apt/lists/*

groupadd --gid 1003 actions-engine
useradd --uid 1001 --create-home --shell /usr/sbin/nologin runner-agent
useradd --uid 1002 --create-home --shell /usr/sbin/nologin --groups actions-engine runner-engine
usermod --append --groups actions-engine runner-agent
grep -q '^runner-engine:' /etc/subuid || usermod --add-subuids 100000-165535 runner-engine
grep -q '^runner-engine:' /etc/subgid || usermod --add-subgids 100000-165535 runner-engine
loginctl enable-linger runner-engine

install -o root -g root -m 0755 "$runner_binary" /usr/local/bin/forgejo-runner
install -o root -g root -m 0755 anas-forgejo-runner-start /usr/local/libexec/anas-forgejo-runner-start
install -o root -g root -m 0755 anas-forgejo-one-job /usr/local/libexec/anas-forgejo-one-job
install -o root -g root -m 0644 config.yml /etc/forgejo-runner/config.yml
install -o root -g root -m 0644 anas-podman.service /etc/systemd/system/anas-podman.service
install -d -o runner-agent -g runner-agent -m 0700 /home/runner-agent/.cache/act
systemctl enable anas-podman.service

# A Runner process is intentionally not enabled at boot. The controller starts
# one-job only after it has a concrete Forgejo job handle and stdin token.
