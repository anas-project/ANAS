#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

rendered_deployments="$RUNTIME_DIR/rendered-parameter-deployments.txt"
parameter_inventory="$RUNTIME_DIR/rendered-parameter-inventory.json"
render_root="$RUNTIME_DIR/render-matrix"
rm -rf "$render_root"
mkdir -p "$render_root"
: >"$rendered_deployments"

for config in "$CONFIG_DIR"/*.yml; do
  name=$(basename "$config" .yml)
  # Lock fixtures live beside the config they belong to and are not configs
  # themselves; feeding one to `plan` fails on the very first field.
  case "$name" in *.lock) continue ;; esac
  ws="$render_root/$name"
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

  case "$name" in
    iam-saml-authentik)
      grep -Fq 'ANAS_IAM_CLIENT__NEXTCLOUD__SAML_SLS_BINDINGS=redirect' \
        "$latest/modules/nextcloud/.env"
      grep -Fq 'ANAS_IAM_CLIENT__NEXTCLOUD__SAML_SLS_URL=https://nc.saml-authentik.example.test:9000/index.php/apps/user_saml/saml/sls' \
        "$latest/modules/authentik/.env"
      grep -Fq 'sls_binding: redirect' "$latest/modules/authentik/blueprints/anas-clients.yaml"
      grep -Fq 'logout_method: frontchannel_native' "$latest/modules/authentik/blueprints/anas-clients.yaml"
      grep -Fq 'sign_logout_request: true' "$latest/modules/authentik/blueprints/anas-clients.yaml"
      grep -Fq 'sign_logout_response: true' "$latest/modules/authentik/blueprints/anas-clients.yaml"
      ;;
    iam-saml-llng)
      grep -Fq 'ANAS_IAM_CLIENT__NEXTCLOUD__SAML_SLS_BINDINGS=redirect' \
        "$latest/modules/nextcloud/.env"
      grep -Fq 'ANAS_IAM_CLIENT__NEXTCLOUD__SAML_SLS_URL=https://nc.saml-llng.example.test:9000/index.php/apps/user_saml/saml/sls' \
        "$latest/modules/llng/.env"
      grep -Fq 'SAML_SP__NEXTCLOUD__METADATA_URL=' \
        "$latest/modules/llng/.env"
      grep -Fq 'https://nc.saml-llng.example.test:9000/apps/user_saml/saml/metadata?idp=1' \
        "$latest/modules/llng/.env"
      ;;
    domain-separation-ad-zone)
      python3 - "$latest" ad_zone nas.test.example test.example <<'PY'
import pathlib, sys

deployment = pathlib.Path(sys.argv[1])
mode, base_domain, ad_domain = sys.argv[2:]

def env(module):
    values = {}
    for line in (deployment / "modules" / module / ".env").read_text().splitlines():
        if line and not line.startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            values[key] = value
    return values

dc = env("samba_dc")
assert dc["BASE_DOMAIN"] == base_domain
assert dc["SAMBA_DC_DOMAIN"] == ad_domain
assert dc["SAMBA_DC_REALM"] == ad_domain.upper()
assert dc["SAMBA_DC_BASE_DN"] == "DC=test,DC=example"
assert dc["SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED"] == mode
assert dc["SAMBA_DC_APPLICATION_DNS_ZONE"] == ad_domain
assert dc["SAMBA_DC_HOST"] == base_domain
assert dc["SAMBA_DC_LDAPS_SERVER_URL_PORT"] == f"ldaps://{base_domain}:636"

fs = env("samba_fs")
assert fs["SAMBA_DC_DOMAIN"] == ad_domain
assert fs["SAMBA_DC_REALM"] == ad_domain.upper()
assert fs["SAMBA_DC_DC_DOMAIN"].endswith("." + ad_domain)
assert fs["SAMBA_DC_DNS_SEARCH"] == ad_domain

for module, web_key, web_host in (
    ("lam", "LAM_DOMAIN", "lam." + base_domain),
    ("nextcloud", "NEXTCLOUD_DOMAIN", "nc." + base_domain),
    ("authentik", "AUTHENTIK_DOMAIN", "auth." + base_domain),
):
    values = env(module)
    assert values[web_key] == web_host
    assert values["SAMBA_DC_BASE_DN"] == "DC=test,DC=example"
PY
      ;;
    domain-separation-separate-zone)
      python3 - "$latest" separate_zone apps.example.test ad.example.test <<'PY'
import pathlib, sys

deployment = pathlib.Path(sys.argv[1])
mode, base_domain, ad_domain = sys.argv[2:]

def env(module):
    values = {}
    for line in (deployment / "modules" / module / ".env").read_text().splitlines():
        if line and not line.startswith("#") and "=" in line:
            key, value = line.split("=", 1)
            values[key] = value
    return values

dc = env("samba_dc")
assert dc["BASE_DOMAIN"] == base_domain
assert dc["SAMBA_DC_DOMAIN"] == ad_domain
assert dc["SAMBA_DC_REALM"] == ad_domain.upper()
assert dc["SAMBA_DC_BASE_DN"] == "DC=ad,DC=example,DC=test"
assert dc["SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED"] == mode
assert dc["SAMBA_DC_APPLICATION_DNS_ZONE"] == base_domain
assert dc["SAMBA_DC_HOST"] == base_domain
assert dc["SAMBA_DC_LDAPS_SERVER_URL"] == f"ldaps://{base_domain}"

fs = env("samba_fs")
assert fs["SAMBA_DC_DOMAIN"] == ad_domain
assert fs["SAMBA_DC_REALM"] == ad_domain.upper()
assert fs["SAMBA_DC_DC_DOMAIN"].endswith("." + ad_domain)
assert fs["SAMBA_DC_DNS_SEARCH"] == ad_domain

for module, web_key, web_host in (
    ("lam", "LAM_DOMAIN", "lam." + base_domain),
    ("nextcloud", "NEXTCLOUD_DOMAIN", "nc." + base_domain),
    ("llng", "LLNG_DOMAIN", "auth." + base_domain),
):
    values = env(module)
    assert values[web_key] == web_host
    assert values["SAMBA_DC_BASE_USERS_DN"].endswith("DC=ad,DC=example,DC=test")
PY
      ;;
  esac
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
assert len(inventory) == 141, len(inventory)
print(f"observed all {len(inventory)} parameter transports in fresh deployment artifacts")
PY
