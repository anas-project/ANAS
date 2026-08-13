#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
TEST_ENV_DIR="$ROOT_DIR/test-env"
CONFIG_DIR="$TEST_ENV_DIR/configs"
REPORT_DIR="$TEST_ENV_DIR/reports"
RUNTIME_DIR=${ANAS_TEST_RUNTIME_DIR:-"$ROOT_DIR/.anas-test/runtime"}
GOCACHE="$ROOT_DIR/.gocache"
export GOCACHE

mkdir -p "$REPORT_DIR" "$RUNTIME_DIR" "$GOCACHE"

run_anas() {
  go run ./cmd/anas "$@"
}

# make_workspace <dir> <config> creates a workspace and installs the config as
# its own. Tests are re-run against existing trees, so an already-initialised
# directory is not an error here.
make_workspace() {
  ws=$1
  cfg=$2
  mkdir -p "$(dirname -- "$ws")"
  if [ ! -d "$ws/.anas" ]; then
    run_anas init "$ws" -y >/dev/null
  fi
  run_anas config import "$cfg" -w "$ws" >/dev/null
}

# Deployment artifacts live under the workspace state directory.
ws_deployments() {
  printf '%s\n' "$1/.anas/deployments"
}

rendered_module_dirs() {
  find "$RUNTIME_DIR" -mindepth 4 -maxdepth 4 -type d -path '*/.anas/deployments/*' 2>/dev/null | sort
}
