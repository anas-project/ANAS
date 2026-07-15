#!/usr/bin/env sh
set -eu

ROOT_DIR=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)
TEST_ENV_DIR="$ROOT_DIR/test-env"
CONFIG_DIR="$TEST_ENV_DIR/configs"
REPORT_DIR="$TEST_ENV_DIR/reports"
RUNTIME_DIR="$ROOT_DIR/.anas-test/runtime"
GOCACHE="$ROOT_DIR/.gocache"
export GOCACHE

mkdir -p "$REPORT_DIR" "$RUNTIME_DIR" "$GOCACHE"

run_anas() {
  go run ./cmd/anas "$@"
}

rendered_module_dirs() {
  find "$RUNTIME_DIR/release" -mindepth 1 -maxdepth 1 -type d 2>/dev/null | sort
}
