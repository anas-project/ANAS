#!/usr/bin/env bash
set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

release_root="$fixture/releases"
legacy_release_root="$fixture/releases-legacy"
mkdir -p "$release_root" "$legacy_release_root" "$fixture/package/anas_linux_amd64" "$fixture/package/anas_linux_arm64" "$fixture/bin"
for arch in amd64 arm64; do
  binary="$fixture/package/anas_linux_${arch}/anas"
  printf '#!/usr/bin/env sh\nprintf "anas 0.1.0 (commit fixture-%s, built fixture)\\n"\n' "$arch" >"$binary"
  chmod 0755 "$binary"
  printf '#!/usr/bin/env sh\nexit 0\n' >"$fixture/package/anas_linux_${arch}/anasd"
  chmod 0755 "$fixture/package/anas_linux_${arch}/anasd"
  cp "$repo_root/packaging/systemd/anasd.service" "$fixture/package/anas_linux_${arch}/anasd.service"
  cp "$repo_root/packaging/anasd/anasd.yml" "$fixture/package/anas_linux_${arch}/anasd.yml"
  tar -C "$fixture/package" -czf "$release_root/anas_linux_${arch}.tar.gz" "anas_linux_${arch}"
done
(
  cd "$release_root"
  sha256sum anas_linux_*.tar.gz >SHA256SUMS
)

mkdir -p "$fixture/package/anas_0.1.0_linux_amd64"
cp "$fixture/package/anas_linux_amd64/anas" "$fixture/package/anas_0.1.0_linux_amd64/anas"
tar -C "$fixture/package" -czf "$legacy_release_root/anas_0.1.0_linux_amd64.tar.gz" "anas_0.1.0_linux_amd64"
(
  cd "$legacy_release_root"
  sha256sum anas_0.1.0_linux_amd64.tar.gz >SHA256SUMS
)

cat >"$fixture/bin/uname" <<'EOF'
#!/usr/bin/env sh
case "$1" in
  -s) printf 'Linux\n' ;;
  -m) printf '%s\n' "$ANAS_INSTALL_FIXTURE_ARCH" ;;
  *) exit 2 ;;
esac
EOF
cat >"$fixture/bin/curl" <<'EOF'
#!/usr/bin/env sh
set -eu
output=
url=
write_format=
while [ "$#" -gt 0 ]; do
  case "$1" in
    -o)
      output="$2"
      shift 2
      ;;
    -w)
      write_format="$2"
      shift 2
      ;;
    --proto|--retry)
      shift 2
      ;;
    --tlsv1.2|-fsSL)
      shift
      ;;
    *)
      url="$1"
      shift
      ;;
  esac
done
[ -n "$write_format" ] && {
  case "$ANAS_INSTALL_SOURCE:$url" in
    github:https://github.com/anas-project/ANAS/releases/latest) ;;
    cn:https://cnb.cool/anas.dev/ANAS/-/releases/latest) ;;
    *) echo "unexpected latest release URL: $url" >&2; exit 1 ;;
  esac
  printf 'https://fixture.invalid/releases/tag/v0.1.0'
  exit 0
}
[ -n "$output" ] && [ -n "$url" ]
cp "$ANAS_INSTALL_FIXTURE_RELEASE/${url##*/}" "$output"
EOF
chmod 0755 "$fixture/bin/uname" "$fixture/bin/curl"

run_install() {
  local source="$1" machine="$2" expected_source="$3" release="$4" label="$5"
  local target="$fixture/install-${label}-${machine}"
  local preference="$fixture/config-${label}-${machine}/source"
  local temp_root="$fixture/tmp ${label} ${machine}"
  mkdir -p "$target" "$temp_root"
  PATH="$fixture/bin:$PATH" \
    TMPDIR="$temp_root" \
    ANAS_INSTALL_FIXTURE_ARCH="$machine" \
    ANAS_INSTALL_FIXTURE_RELEASE="$release" \
    ANAS_INSTALL_SOURCE="$source" \
    ANAS_INSTALL_DIR="$target" \
    ANAS_INSTALL_SERVICE=0 \
    ANAS_SOURCE_CONFIG="$preference" \
    sh "$repo_root/install.sh" >/dev/null
  [[ -x "$target/anas" ]]
  if [[ "$label" != github-legacy ]]; then
    [[ -x "$target/anasd" ]]
  fi
  [[ "$(cat "$preference")" == "$expected_source" ]]
}

run_install github x86_64 official "$release_root" github
run_install cn aarch64 official-cn "$release_root" cn
run_install github x86_64 official "$legacy_release_root" github-legacy

bad_release_root="$fixture/releases-bad-version"
mkdir -p "$bad_release_root" "$fixture/package-bad/anas_linux_amd64"
printf '#!/usr/bin/env sh\nprintf "anas 9.9.9 (commit wrong, built wrong)\\n"\n' >"$fixture/package-bad/anas_linux_amd64/anas"
chmod 0755 "$fixture/package-bad/anas_linux_amd64/anas"
tar -C "$fixture/package-bad" -czf "$bad_release_root/anas_linux_amd64.tar.gz" anas_linux_amd64
(
  cd "$bad_release_root"
  sha256sum anas_linux_amd64.tar.gz >SHA256SUMS
)
bad_target="$fixture/install-bad-version"
bad_preference="$fixture/config-bad-version/source"
mkdir -p "$bad_target" "$(dirname "$bad_preference")"
printf 'existing installation\n' >"$bad_target/anas"
printf 'existing-source\n' >"$bad_preference"
if PATH="$fixture/bin:$PATH" \
  ANAS_INSTALL_FIXTURE_ARCH=x86_64 \
  ANAS_INSTALL_FIXTURE_RELEASE="$bad_release_root" \
  ANAS_INSTALL_SOURCE=github \
  ANAS_INSTALL_DIR="$bad_target" \
  ANAS_SOURCE_CONFIG="$bad_preference" \
  sh "$repo_root/install.sh" >/dev/null 2>&1; then
  echo "installer accepted a binary whose version disagrees with the release tag" >&2
  exit 1
fi

# A current archive installs, upgrades, and uninstalls the daemon, service
# configuration, systemd unit, and selected management port. A fake systemctl
# makes this contract test deterministic and unprivileged; the server e2e runs
# the same flow against real systemd as root.
cat >"$fixture/bin/id" <<'EOF'
#!/usr/bin/env sh
[ "${1:-}" = -u ] || exit 2
printf '0\n'
EOF
cat >"$fixture/bin/chown" <<'EOF'
#!/usr/bin/env sh
exit 0
EOF
cat >"$fixture/bin/systemctl" <<'EOF'
#!/usr/bin/env sh
printf '%s\n' "$*" >>"$ANAS_INSTALL_SYSTEMCTL_LOG"
EOF
chmod 0755 "$fixture/bin/id" "$fixture/bin/chown" "$fixture/bin/systemctl"

incomplete_target="$fixture/incomplete/bin"
mkdir -p "$incomplete_target"
printf 'keep-existing\n' >"$incomplete_target/anas"
if PATH="$fixture/bin:$PATH" \
  ANAS_INSTALL_FIXTURE_ARCH=x86_64 \
  ANAS_INSTALL_FIXTURE_RELEASE="$legacy_release_root" \
  ANAS_INSTALL_SOURCE=github \
  ANAS_INSTALL_DIR="$incomplete_target" \
  ANAS_INSTALL_SERVICE=1 \
  ANAS_SERVICE_CONFIG="$fixture/incomplete/anasd.yml" \
  ANAS_SYSTEMD_UNIT="$fixture/incomplete/anasd.service" \
  ANAS_SYSTEMCTL=systemctl \
  ANAS_INSTALL_SYSTEMCTL_LOG="$fixture/incomplete/systemctl.log" \
  ANAS_CONSOLE_STORE="$fixture/incomplete/console" \
  ANAS_SOURCE_CONFIG="$fixture/incomplete/source" \
  sh "$repo_root/install.sh" >/dev/null 2>&1; then
  echo "installer accepted an incomplete service archive" >&2
  exit 1
fi
grep -qx 'keep-existing' "$incomplete_target/anas"
[[ ! -e "$incomplete_target/anasd" && ! -e "$fixture/incomplete/anasd.yml" ]]

service_target="$fixture/service/bin"
service_config="$fixture/service/etc/anasd.yml"
service_unit="$fixture/service/systemd/anasd-fixture.service"
console_store="$fixture/service/state/console"
service_preference="$fixture/service/source"
systemctl_log="$fixture/service/systemctl.log"
mkdir -p "$fixture/service"
service_install() {
  PATH="$fixture/bin:$PATH" \
    ANAS_INSTALL_FIXTURE_ARCH=x86_64 \
    ANAS_INSTALL_FIXTURE_RELEASE="$release_root" \
    ANAS_INSTALL_SOURCE=github \
    ANAS_INSTALL_DIR="$service_target" \
    ANAS_INSTALL_SERVICE=1 \
    ANAS_SERVICE_CONFIG="$service_config" \
    ANAS_SYSTEMD_UNIT="$service_unit" \
    ANAS_SYSTEMCTL=systemctl \
    ANAS_INSTALL_SYSTEMCTL_LOG="$systemctl_log" \
    ANAS_MANAGEMENT_PORT=7788 \
    ANAS_CONSOLE_STORE="$console_store" \
    ANAS_HELPER_DIR="$fixture/service/helper" \
    ANAS_SOURCE_CONFIG="$service_preference" \
    sh "$repo_root/install.sh" "$@" >/dev/null
}
service_install
[[ -x "$service_target/anas" && -x "$service_target/anasd" ]]
[[ "$(stat -c '%a' "$service_config" 2>/dev/null || stat -f '%Lp' "$service_config")" == 600 ]]
grep -qx 'port: 7788' "$service_config"
grep -Fqx "console_store: $console_store" "$service_config"
grep -Fqx "ExecStart=$service_target/anasd --config $service_config" "$service_unit"
grep -Fqx "ReadWritePaths=-$console_store -/srv/anas -/srv/anas-backups" "$service_unit"
grep -qx 'User=root' "$service_unit"
grep -qx 'ProtectSystem=strict' "$service_unit"
touch "$console_store/preserve-me"

# Upgrade must replace binaries/unit but preserve the administrator's config.
sed -i.bak 's/^port: 7788$/port: 7789/' "$service_config"
rm -f "$service_config.bak"
service_install
grep -qx 'port: 7789' "$service_config"
grep -qx 'daemon-reload' "$systemctl_log"
grep -qx 'enable anasd-fixture.service' "$systemctl_log"
grep -qx 'restart anasd-fixture.service' "$systemctl_log"

service_install --uninstall
[[ ! -e "$service_target/anas" && ! -e "$service_target/anasd" && ! -e "$service_unit" ]]
[[ -e "$service_config" && -e "$console_store/preserve-me" ]]
grep -qx 'disable --now anasd-fixture.service' "$systemctl_log"
service_install --uninstall --purge
[[ ! -e "$service_config" && -e "$console_store/preserve-me" ]]
grep -qx 'existing installation' "$bad_target/anas"
grep -qx 'existing-source' "$bad_preference"

if PATH="$fixture/bin:$PATH" \
  ANAS_INSTALL_FIXTURE_ARCH=riscv64 \
  ANAS_INSTALL_FIXTURE_RELEASE="$release_root" \
  ANAS_INSTALL_DIR="$fixture/unsupported" \
  ANAS_SOURCE_CONFIG="$fixture/unsupported-source" \
  sh "$repo_root/install.sh" >/dev/null 2>&1; then
  echo "installer accepted an unsupported architecture" >&2
  exit 1
fi

# README.md documents `curl ... | sh`, so a dropped connection hands sh a prefix of
# this file. Every prefix must be inert: main() is invoked only from the last
# line, so no truncation may install a binary, write a source preference, or call
# setcap. Checking every cut point is what keeps a future top-level statement from
# quietly reintroducing the half-installed system.
truncation_root="$fixture/truncation"
mkdir -p "$truncation_root"
total_lines="$(wc -l <"$repo_root/install.sh")"
for (( cut = 1; cut < total_lines; cut++ )); do
  prefix="$truncation_root/install-$cut.sh"
  head -n "$cut" "$repo_root/install.sh" >"$prefix"
  target="$truncation_root/dir-$cut"
  preference="$truncation_root/config-$cut/source"
  mkdir -p "$target"
  PATH="$fixture/bin:$PATH" \
    ANAS_INSTALL_FIXTURE_ARCH=x86_64 \
    ANAS_INSTALL_FIXTURE_RELEASE="$release_root" \
    ANAS_INSTALL_SOURCE=github \
    ANAS_INSTALL_DIR="$target" \
    ANAS_SOURCE_CONFIG="$preference" \
    sh "$prefix" >/dev/null 2>&1 || true
  if [[ -n "$(ls -A "$target")" ]]; then
    echo "install.sh truncated after line $cut installed something into $target" >&2
    exit 1
  fi
  if [[ -e "$preference" ]]; then
    echo "install.sh truncated after line $cut wrote the source preference" >&2
    exit 1
  fi
done

# The guarantee above rests entirely on main being invoked from the final line.
if [[ "$(tail -n 1 "$repo_root/install.sh")" != 'main "$@"' ]]; then
  echo 'install.sh must end with: main "$@"' >&2
  exit 1
fi

sh -n "$repo_root/install.sh"
echo "ANAS installer tests passed"
