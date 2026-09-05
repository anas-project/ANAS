#!/usr/bin/env bash
# REQUIREMENTS: UPGRADE-R-026
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
helper=$script_dir/server-upgrade-verify-services.sh
grep -Fq 'ANAS_UPGRADE_READY_TIMEOUT_SECONDS:-1200' "$helper"
work_dir=$(mktemp -d "${TMPDIR:-/tmp}/anas-upgrade-readiness-test.XXXXXX")

cleanup() {
  case "$work_dir" in
    "${TMPDIR:-/tmp}"/anas-upgrade-readiness-test.*|/tmp/anas-upgrade-readiness-test.*) rm -rf -- "$work_dir" ;;
    *) echo "refusing to remove unexpected path: $work_dir" >&2 ;;
  esac
}
trap cleanup EXIT HUP INT TERM

mkdir -p "$work_dir/bin"
cat >"$work_dir/bin/docker" <<'EOF'
#!/usr/bin/env bash
set -euo pipefail
scenario=${FAKE_DOCKER_SCENARIO:?}
if [[ "$1" == ps ]]; then
  case "$scenario" in
    healthy) printf '%s\n' anas_upgrade_app anas_upgrade_events_init unrelated ;;
    restarting|starting) printf '%s\n' anas_upgrade_app ;;
    bad-init) printf '%s\n' anas_upgrade_events_init ;;
    empty) : ;;
  esac
  exit 0
fi
[[ "$1" == inspect && "$2" == --format && $# -eq 4 ]]
format=$3
container=$4
case "$scenario:$container:$format" in
  healthy:anas_upgrade_app:'{{.State.Status}}') printf running ;;
  healthy:anas_upgrade_app:'{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}') printf healthy ;;
  healthy:anas_upgrade_app:'{{.State.ExitCode}}') printf 0 ;;
  healthy:anas_upgrade_app:'{{.HostConfig.RestartPolicy.Name}}') printf unless-stopped ;;
  healthy:anas_upgrade_events_init:'{{.State.Status}}') printf exited ;;
  healthy:anas_upgrade_events_init:'{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}') printf none ;;
  healthy:anas_upgrade_events_init:'{{.State.ExitCode}}') printf 0 ;;
  healthy:anas_upgrade_events_init:'{{.HostConfig.RestartPolicy.Name}}') printf no ;;
  restarting:anas_upgrade_app:'{{.State.Status}}') printf restarting ;;
  restarting:anas_upgrade_app:'{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}') printf none ;;
  restarting:anas_upgrade_app:'{{.State.ExitCode}}') printf 1 ;;
  restarting:anas_upgrade_app:'{{.HostConfig.RestartPolicy.Name}}') printf unless-stopped ;;
  starting:anas_upgrade_app:'{{.State.Status}}') printf running ;;
  starting:anas_upgrade_app:'{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}') printf starting ;;
  starting:anas_upgrade_app:'{{.State.ExitCode}}') printf 0 ;;
  starting:anas_upgrade_app:'{{.HostConfig.RestartPolicy.Name}}') printf unless-stopped ;;
  bad-init:anas_upgrade_events_init:'{{.State.Status}}') printf exited ;;
  bad-init:anas_upgrade_events_init:'{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}') printf none ;;
  bad-init:anas_upgrade_events_init:'{{.State.ExitCode}}') printf 1 ;;
  bad-init:anas_upgrade_events_init:'{{.HostConfig.RestartPolicy.Name}}') printf no ;;
  *) echo "unexpected fake docker invocation: $*" >&2; exit 2 ;;
esac
EOF
chmod 0755 "$work_dir/bin/docker"

run_helper() {
  FAKE_DOCKER_SCENARIO=$1 \
    PATH="$work_dir/bin:$PATH" \
    ANAS_TEST_CONTAINER_PREFIX=anas_upgrade_ \
    ANAS_UPGRADE_READY_TIMEOUT_SECONDS=0 \
    "$helper"
}

[[ "$(run_helper healthy)" == 2 ]]
for scenario in restarting starting bad-init empty; do
  if run_helper "$scenario" >"$work_dir/$scenario.out" 2>"$work_dir/$scenario.err"; then
    echo "readiness accepted invalid scenario: $scenario" >&2
    exit 1
  fi
done
grep -Fq 'state=restarting' "$work_dir/restarting.err"
grep -Fq 'health=starting' "$work_dir/starting.err"
grep -Fq 'exit=1' "$work_dir/bad-init.err"
grep -Fq 'no upgrade service containers' "$work_dir/empty.err"

printf '%s\n' 'upgrade_service_readiness_test=pass default_window=1200s running=accepted successful_init=accepted restarting/starting/failed_init/empty=rejected'
