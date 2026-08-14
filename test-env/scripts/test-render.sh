#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

for config in "$CONFIG_DIR"/*.yml; do
  name=$(basename "$config" .yml)
  # Lock fixtures live beside the config they belong to and are not configs
  # themselves; feeding one to `plan` fails on the very first field.
  case "$name" in *.lock) continue ;; esac
  ws="$RUNTIME_DIR/$name"
  log="$REPORT_DIR/render-$name.log"
  make_workspace "$ws" "$config"
  if {
    echo "== plan: $name ==" &&
    run_anas plan -w "$ws" -c "$ws/config.yml" &&
    echo "== render: $name ==" &&
    run_anas render -w "$ws" --update-lock
  } >"$log" 2>&1; then
    cat "$log"
  else
    status=$?
    cat "$log"
    exit "$status"
  fi

  if find "$(ws_deployments "$ws")" -type f \( -name '*.erb' -o -name '*.j2' -o -name '*.j3' -o -name '*.tmpl' \) -print -quit | grep -q .; then
    echo "legacy template files remain for $name" >&2
    exit 1
  fi
done
