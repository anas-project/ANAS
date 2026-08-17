#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

rendered_deployments="$RUNTIME_DIR/rendered-parameter-deployments.txt"
parameter_inventory="$RUNTIME_DIR/rendered-parameter-inventory.json"
: >"$rendered_deployments"

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

  # Record only the artifact produced by this run. Scanning every historical
  # deployment would let a stale render hide a newly disconnected parameter.
  latest=$(ls -1dt "$(ws_deployments "$ws")"/* | head -1)
  printf '%s\n' "$latest" >>"$rendered_deployments"
done

echo "== every declared parameter reaches a rendered module environment =="
run_anas config list --json >"$parameter_inventory"
python3 - "$parameter_inventory" "$rendered_deployments" <<'PY'
import json, pathlib, sys

inventory = json.load(open(sys.argv[1]))["parameters"]
deployments = [pathlib.Path(line.strip()) for line in open(sys.argv[2]) if line.strip()]
by_module = {}
for deployment in deployments:
    for env_file in deployment.glob("modules/*/.env"):
        keys = by_module.setdefault(env_file.parent.name, set())
        for line in env_file.read_text(errors="ignore").splitlines():
            if line and not line.startswith("#") and "=" in line:
                keys.add(line.split("=", 1)[0])

all_keys = set().union(*by_module.values()) if by_module else set()

# Parameters whose reader is the runner rather than a container. They govern how
# the deployment is attached to the host LAN and are consumed before any
# container exists, so they reach no rendered environment by design. Naming them
# here keeps the check exact: anything else missing is still a transport that
# was declared and never wired up. TestGlobalParametersHaveRuntimeConsumers is
# the matching guard on the other side -- it proves the runner does read them.
runner_consumed = {"global.host_lan_bridge_ip", "global.host_lan_arp_check"}

missing = []
for parameter in inventory:
    if parameter["path"] in runner_consumed:
        continue
    visible = all_keys if parameter["module"] == "global" else by_module.get(parameter["module"], set())
    if parameter["env_key"] not in visible:
        missing.append((parameter["path"], parameter["env_key"]))

assert not missing, "declared parameters absent from fresh renders: " + repr(missing)
assert len(inventory) == 131, len(inventory)
print(f"observed all {len(inventory)} parameter transports in fresh deployment artifacts")
PY
