#!/usr/bin/env sh
# TEST_CASES: TESTAUTO-T-012
set -eu

PATH=/usr/sbin:/usr/bin:/sbin:/bin
export PATH

helper_source=${1:-}
config_source=${2:-}
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
guard_source=$script_dir/server-require-isolated-docker.sh
helper_target=/usr/local/libexec/anas/anas-test-helper
guard_target=/usr/local/libexec/anas/server-require-isolated-docker.sh
config_target=/etc/anas-test-helper.yml
sudoers_target=/etc/sudoers.d/anas-test-helper

if [ "$(id -u)" -ne 0 ]; then
  echo "install-remote-test-helper must run as root" >&2
  exit 1
fi
if [ -z "$helper_source" ] || [ -z "$config_source" ]; then
  echo "usage: $0 <built-anas-test-helper> <reviewed-helper-config.yml>" >&2
  exit 2
fi
for source in "$helper_source" "$config_source" "$guard_source"; do
  if [ ! -f "$source" ] || [ -L "$source" ]; then
    echo "refusing non-regular or symlinked install source: $source" >&2
    exit 2
  fi
done

test_user=$(sed -n 's/^test_user:[[:space:]]*//p' "$config_source")
remote_work_root=$(sed -n 's/^remote_work_root:[[:space:]]*//p' "$config_source")
case "$test_user" in
  ""|*[!a-z0-9-]*) echo "helper config has an invalid test_user" >&2; exit 2 ;;
esac
case "$remote_work_root" in
  /*) ;;
  *) echo "helper config has an invalid remote_work_root" >&2; exit 2 ;;
esac
case "$remote_work_root" in
  *[!A-Za-z0-9_./-]*|*//*|*/../*|*/./*|*/..|*/.|/)
    echo "helper config has an unsafe remote_work_root" >&2
    exit 2
    ;;
esac
remote_root_scope=$(printf '%s' "$remote_work_root" | tr '[:upper:]' '[:lower:]')
case "$remote_root_scope" in
  *test*|*e2e*) ;;
  *) echo "remote_work_root must identify a test/e2e scope" >&2; exit 2 ;;
esac
case "${remote_work_root#/}" in
  */*) ;;
  *) echo "remote_work_root must be a dedicated nested directory" >&2; exit 2 ;;
esac
if ! id "$test_user" >/dev/null 2>&1; then
  echo "test user does not exist: $test_user" >&2
  exit 2
fi
if [ "$(id -u "$test_user")" -eq 0 ]; then
  echo "test user must not be root: $test_user" >&2
  exit 2
fi
for group in $(id -Gn "$test_user"); do
  case "$group" in
    root|docker|wheel|sudo|admin) echo "test user belongs to privileged group: $group" >&2; exit 2 ;;
  esac
done

if ! existing_sudo=$(/usr/bin/sudo -n -l -U "$test_user" 2>/dev/null); then
  echo "could not audit existing sudo policy for test user: $test_user" >&2
  exit 2
fi
other_sudo=$(printf '%s\n' "$existing_sudo" | grep -E '^[[:space:]]*\(' | grep -Fv "$helper_target" || true)
if [ -n "$other_sudo" ]; then
  echo "test user already has an unsafe sudo rule; remove it before installing the helper" >&2
  exit 2
fi

install -d -o root -g root -m 0755 /usr/local/libexec/anas
if [ -e "$remote_work_root" ] && { [ ! -d "$remote_work_root" ] || [ -L "$remote_work_root" ]; }; then
  echo "remote_work_root already exists but is not a regular directory" >&2
  exit 2
fi
install -d -o root -g root -m 0755 "$remote_work_root" "$remote_work_root/.leases"
install -o root -g root -m 0755 "$helper_source" "$helper_target"
install -o root -g root -m 0644 "$guard_source" "$guard_target"
install -o root -g root -m 0644 "$config_source" "$config_target"

sudoers_temp=$(mktemp /etc/sudoers.d/anas-test-helper.XXXXXX)
trap 'rm -f "$sudoers_temp"' EXIT HUP INT TERM
printf '%s ALL=(root) NOPASSWD: %s\n' "$test_user" "$helper_target" >"$sudoers_temp"
chmod 0440 "$sudoers_temp"
/usr/sbin/visudo -cf "$sudoers_temp" >/dev/null
install -o root -g root -m 0440 "$sudoers_temp" "$sudoers_target"

printf 'installed %s for user %s; only fixed helper verbs are authorized\n' "$helper_target" "$test_user"
