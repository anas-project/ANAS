#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

for config in "$CONFIG_DIR"/*.yml; do
  name=$(basename "$config" .yml)
  base="$RUNTIME_DIR/$name"
  log="$REPORT_DIR/render-$name.log"
  mkdir -p "$base"
  runtime_config="$base/config.yml"
  cp "$config" "$runtime_config"
  if {
    echo "== plan: $name =="
    run_anas plan -c "$runtime_config" -b "$base"
    echo "== render: $name =="
    run_anas render -c "$runtime_config" -b "$base" --update-lock
  } >"$log" 2>&1; then
    cat "$log"
  else
    status=$?
    cat "$log"
    exit "$status"
  fi

  if find "$base/deployments" -type f -name '*.erb' -print -quit | grep -q .; then
    echo "unrendered ERB files remain for $name" >&2
    exit 1
  fi
done
