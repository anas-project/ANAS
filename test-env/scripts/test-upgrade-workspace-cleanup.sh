#!/usr/bin/env bash
# REQUIREMENTS: UPGRADE-R-019
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
helper=$script_dir/server-upgrade-clean-workspace.sh
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/anas-upgrade-cleanup-test.XXXXXX")

cleanup() {
  case "$work_dir" in
    "${TMPDIR:-/tmp}"/anas-upgrade-cleanup-test.*|/tmp/anas-upgrade-cleanup-test.*) rm -rf -- "$work_dir" ;;
    *) echo "refusing to remove unexpected path: $work_dir" >&2 ;;
  esac
}
trap cleanup EXIT HUP INT TERM

fake_anas=$work_dir/anas
log=$work_dir/deleted.log
cat >"$fake_anas" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
[[ "$1" == snapshot && "$2" == delete && "$4" == -w && "$6" == --force && "$7" == -y && "$8" == --json ]]
printf '%s\n' "$3" >>"${FAKE_DELETE_LOG:?}"
rm -rf -- "$5/snapshots/$3"
EOF
chmod 0755 "$fake_anas"

workspace=/tmp/anas-upgrade-cleanup-fixture-$$
mkdir -p "$workspace/snapshots/snapshot-a" "$workspace/snapshots/snapshot-b" "$workspace/data"
FAKE_DELETE_LOG=$log "$helper" "$fake_anas" "$workspace"
[[ ! -e "$workspace" ]]
[[ "$(sort "$log" | tr '\n' ' ')" == 'snapshot-a snapshot-b ' ]]

unsafe=$work_dir/not-an-upgrade-workspace
mkdir -p "$unsafe"
if "$helper" "$fake_anas" "$unsafe" >/dev/null 2>&1; then
  echo "cleanup accepted an out-of-scope workspace" >&2
  exit 1
fi
[[ -d "$unsafe" ]]

printf '%s\n' 'upgrade_workspace_cleanup_test=pass snapshots=cli-first workspace=scoped unsafe=rejected'
