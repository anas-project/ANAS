#!/bin/sh
set -eu

[ "$#" -eq 2 ] || { echo "usage: provision.sh RUNNER_BINARY RUNNER_SHA256" >&2; exit 64; }
runner_binary=$1
runner_sha256=$2
case "$runner_sha256" in *[!0-9a-f]*|'') exit 64 ;; esac
[ "${#runner_sha256}" -eq 64 ] || exit 64
printf '%s  %s\n' "$runner_sha256" "$runner_binary" | sha256sum -c -

if [ "${CHINESE_BUILD_SPEEDUP:-false}" = "true" ]; then
    sh "$(dirname "$0")/configure-build-mirrors"
fi
apt-get update
DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends \
    apparmor ca-certificates git podman slirp4netns uidmap fuse-overlayfs util-linux coreutils dbus-user-session
rm -rf /var/lib/apt/lists/*

groupadd --gid 1003 actions-engine
useradd --uid 1001 --create-home --shell /usr/sbin/nologin runner-agent
useradd --uid 1002 --gid actions-engine --no-user-group --create-home --shell /usr/sbin/nologin runner-engine
usermod --append --groups actions-engine runner-agent
grep -q '^runner-engine:' /etc/subuid || usermod --add-subuids 100000-165535 runner-engine
grep -q '^runner-engine:' /etc/subgid || usermod --add-subgids 100000-165535 runner-engine
loginctl enable-linger runner-engine

install -d -o root -g root -m 0755 /etc/forgejo-runner /usr/local/libexec
install -d -o root -g root -m 0755 /usr/lib/systemd/user /etc/systemd/system/user@1002.service.d
install -o root -g root -m 0755 "$runner_binary" /usr/local/bin/forgejo-runner
install -o root -g root -m 0755 anas-forgejo-runner-start /usr/local/libexec/anas-forgejo-runner-start
install -o root -g root -m 0755 anas-forgejo-runner-input /usr/local/libexec/anas-forgejo-runner-input
install -o root -g root -m 0755 anas-forgejo-one-job /usr/local/libexec/anas-forgejo-one-job
install -o root -g root -m 0644 config.yml /etc/forgejo-runner/config.yml
install -o root -g root -m 0644 anas-podman.service /usr/lib/systemd/user/anas-podman.service
install -o root -g root -m 0644 anas-podman.socket /usr/lib/systemd/user/anas-podman.socket
install -o root -g root -m 0644 anas-engine-user.conf /etc/systemd/system/user@1002.service.d/anas-engine.conf
install -d -o root -g root -m 0755 /usr/share/anas /usr/share/anas/forgejo-runner
install -o root -g root -m 0644 anas-forgejo-podman.apparmor /usr/share/anas/forgejo-runner/podman.apparmor
install -o root -g root -m 0644 anas-forgejo-podman-policy.service /etc/systemd/system/anas-forgejo-podman-policy.service
install -d -o root -g root -m 0755 /etc/systemd/system/apparmor.service.d
install -o root -g root -m 0644 anas-apparmor-loader.conf /etc/systemd/system/apparmor.service.d/anas-runner.conf
install -o root -g root -m 0644 anas-podman.conf /usr/lib/tmpfiles.d/anas-podman.conf
install -d -o runner-agent -g runner-agent -m 0700 /home/runner-agent/.cache/act
install -d -o runner-engine -g actions-engine -m 0700 /home/runner-engine/.config /home/runner-engine/.config/systemd /home/runner-engine/.config/systemd/user /home/runner-engine/.config/systemd/user/sockets.target.wants
ln -s /usr/lib/systemd/user/anas-podman.socket /home/runner-engine/.config/systemd/user/sockets.target.wants/anas-podman.socket
chown -h runner-engine:actions-engine /home/runner-engine/.config/systemd/user/sockets.target.wants/anas-podman.socket

# A Runner process is intentionally not enabled at boot. The controller starts
# one-job only after it has a concrete Forgejo job handle and stdin token.
