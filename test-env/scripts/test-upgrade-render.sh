#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

for lock in "$TEST_ENV_DIR"/upgrades/supported/*.lock.yml; do
  name=$(basename "$lock" .lock.yml)
  base="$RUNTIME_DIR/upgrade-$name"
  log="$REPORT_DIR/upgrade-render-$name.log"
  make_workspace "$base" "$CONFIG_DIR/full.yml"
  cp "$lock" "$base/config.lock.yml"

  if {
    echo "== upgrade plan: $name ==" &&
    run_anas plan -w "$base" -c "$base/config.yml" &&
    echo "== upgrade render: $name ==" &&
    run_anas render -w "$base" --update-lock
  } >"$log" 2>&1; then
    cat "$log"
  else
    status=$?
    cat "$log"
    exit "$status"
  fi

  if find "$(ws_deployments "$base")" -type f \( -name '*.erb' -o -name '*.j2' -o -name '*.j3' -o -name '*.tmpl' \) -print -quit | grep -q .; then
    echo "legacy template files remain for upgrade fixture $name" >&2
    exit 1
  fi
done

for lock in "$TEST_ENV_DIR"/upgrades/rejected/*.lock.yml; do
  name=$(basename "$lock" .lock.yml)
  base="$RUNTIME_DIR/upgrade-rejected-$name"
  log="$REPORT_DIR/upgrade-rejected-$name.log"
  make_workspace "$base" "$CONFIG_DIR/full.yml"
  cp "$lock" "$base/config.lock.yml"

  if run_anas render -w "$base" --update-lock >"$log" 2>&1; then
    cat "$log"
    echo "upgrade fixture $name unexpectedly succeeded" >&2
    exit 1
  fi
  cat "$log"
done
