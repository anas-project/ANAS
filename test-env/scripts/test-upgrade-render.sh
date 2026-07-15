#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

for lock in "$TEST_ENV_DIR"/upgrades/supported/*.lock.yml; do
  name=$(basename "$lock" .lock.yml)
  base="$RUNTIME_DIR/upgrade-$name"
  log="$REPORT_DIR/upgrade-render-$name.log"
  mkdir -p "$base"
  cp "$lock" "$base/cask.lock.yml"

  if {
    echo "== upgrade plan: $name =="
    run_anas plan -c "$CONFIG_DIR/full.yml" -b "$base"
    echo "== upgrade render: $name =="
    run_anas render -c "$CONFIG_DIR/full.yml" -b "$base"
  } >"$log" 2>&1; then
    cat "$log"
  else
    status=$?
    cat "$log"
    exit "$status"
  fi

  if find "$base/release" -type f -name '*.erb' -print -quit | grep -q .; then
    echo "unrendered ERB files remain for upgrade fixture $name" >&2
    exit 1
  fi
done

for lock in "$TEST_ENV_DIR"/upgrades/rejected/*.lock.yml; do
  name=$(basename "$lock" .lock.yml)
  base="$RUNTIME_DIR/upgrade-rejected-$name"
  log="$REPORT_DIR/upgrade-rejected-$name.log"
  mkdir -p "$base"
  cp "$lock" "$base/cask.lock.yml"

  if run_anas plan -c "$CONFIG_DIR/full.yml" -b "$base" >"$log" 2>&1; then
    cat "$log"
    echo "upgrade fixture $name unexpectedly succeeded" >&2
    exit 1
  fi
  cat "$log"
done
