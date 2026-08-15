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
  printf '#!/usr/bin/env sh\nprintf "anas fixture-%s\\n"\n' "$arch" >"$binary"
  chmod 0755 "$binary"
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
  mkdir -p "$target"
  PATH="$fixture/bin:$PATH" \
    ANAS_INSTALL_FIXTURE_ARCH="$machine" \
    ANAS_INSTALL_FIXTURE_RELEASE="$release" \
    ANAS_INSTALL_SOURCE="$source" \
    ANAS_INSTALL_DIR="$target" \
    ANAS_SOURCE_CONFIG="$preference" \
    sh "$repo_root/install.sh" >/dev/null
  [[ -x "$target/anas" ]]
  [[ "$(cat "$preference")" == "$expected_source" ]]
}

run_install github x86_64 official "$release_root" github
run_install cn aarch64 official-cn "$release_root" cn
run_install github x86_64 official "$legacy_release_root" github-legacy

if PATH="$fixture/bin:$PATH" \
  ANAS_INSTALL_FIXTURE_ARCH=riscv64 \
  ANAS_INSTALL_FIXTURE_RELEASE="$release_root" \
  ANAS_INSTALL_DIR="$fixture/unsupported" \
  ANAS_SOURCE_CONFIG="$fixture/unsupported-source" \
  sh "$repo_root/install.sh" >/dev/null 2>&1; then
  echo "installer accepted an unsupported architecture" >&2
  exit 1
fi

sh -n "$repo_root/install.sh"
echo "ANAS installer tests passed"
