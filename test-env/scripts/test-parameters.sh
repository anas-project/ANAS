#!/usr/bin/env sh
# End-to-end audit for the public configuration surface. This complements the
# source-level consumer test: here the real CLI must list the retained
# parameters, reject deleted paths, and render the observable certificate-mode
# result under the surviving global.virtual_domain setting.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

ws="$RUNTIME_DIR/parameters"
inventory="$RUNTIME_DIR/parameters.json"
invalid="$RUNTIME_DIR/parameters-invalid.yml"
log="$REPORT_DIR/parameters.log"

rm -rf "$ws"
run_anas init "$ws" -y >/dev/null
run_anas config list --json >"$inventory"

{
  echo "== every retained parameter reports its transport and change result =="
  python3 - "$inventory" "$ROOT_DIR" <<'PY'
import json, os, re, sys

doc = json.load(open(sys.argv[1]))
parameters = doc["parameters"]
assert len(parameters) == 164, len(parameters)
assert sum(item["module"] == "global" for item in parameters) == 17
assert sum(item["module"] != "global" for item in parameters) == 147

removed = {
    "global.basicauth_user",
    "global.image_prefix",
    "global.default_service_root_password",
    "collabora.interface",
    "lego.virtual_domain",
    "nextcloud.debug",
    "traefik.subnet",
    "traefik.gateway_ip",
}
paths = {item["path"] for item in parameters}
assert not (paths & removed), sorted(paths & removed)

for item in parameters:
    for field in ("path", "module", "parameter", "env_key", "type", "effect", "apply", "description"):
        assert item.get(field), (item.get("path"), field, item.get(field))
    for field in ("required", "input_required", "must_resolve", "has_default"):
        assert isinstance(item.get(field), bool), (item.get("path"), field, item.get(field))
    assert item["required"] == item["input_required"], (
        item["path"], item["required"], item["input_required"]
    )
    assert isinstance(item.get("default"), str), (item["path"], "default", item.get("default"))
    assert item.get("default_source") in {
        "none", "static", "host", "runtime", "generated", "inherited",
    }, (item["path"], "default_source", item.get("default_source"))

    # A literal default is represented only by has_default/static. Other
    # sources may compute the v1 `default` string for display, but they never
    # turn it into a literal declaration. Explicit input and an unconditional
    # source are mutually exclusive.
    assert item["has_default"] == (item["default_source"] == "static"), item
    if item["input_required"]:
        assert not item["has_default"], item
        assert item["default_source"] == "none", item

    constraints = item.get("constraints")
    if constraints is None:
        continue
    assert isinstance(constraints, dict) and constraints, (item["path"], constraints)
    assert set(constraints) <= {
        "minimum", "maximum", "min_length", "max_length", "pattern", "format",
    }, (item["path"], constraints)
    for field in ("minimum", "maximum"):
        if field in constraints:
            assert item["type"] == "int" and type(constraints[field]) is int, (
                item["path"], field, constraints[field]
            )
    for field in ("min_length", "max_length"):
        if field in constraints:
            assert item["type"] == "string" and type(constraints[field]) is int, (
                item["path"], field, constraints[field]
            )
            assert constraints[field] >= 0, (item["path"], field, constraints[field])
    if "pattern" in constraints:
        assert item["type"] == "string" and isinstance(constraints["pattern"], str), (
            item["path"], "pattern", constraints["pattern"]
        )
        assert constraints["pattern"], (item["path"], "pattern")
    if "format" in constraints:
        assert item["type"] == "string", (item["path"], "format", constraints["format"])
        assert constraints["format"] in {
            "iana_timezone", "language_tag", "locale", "ipv4", "dns_name",
        }, (item["path"], "format", constraints["format"])

# Cover every source spelling and prove that an explicit empty literal remains
# distinguishable from no literal at all. Host-derived display defaults are
# deliberately not pinned because they vary with the test machine.
metadata_cases = {
    "global.base_domain": (False, "none"),
    "global.container_prefix": (True, "static"),
    "global.timezone": (False, "host"),
    "global.host_ip": (False, "runtime"),
    "samba_dc.admin_password": (False, "generated"),
    "nextcloud.language": (False, "inherited"),
}
by_path = {item["path"]: item for item in parameters}
for path, (has_default, default_source) in metadata_cases.items():
    item = by_path[path]
    assert item["has_default"] == has_default, (path, item["has_default"])
    assert item["default_source"] == default_source, (path, item["default_source"])
assert by_path["ddns_go.ipv4_interface"]["default"] == ""
assert by_path["ddns_go.ipv4_interface"]["has_default"] is True
assert by_path["global.base_domain"]["default"] == ""
assert by_path["global.base_domain"]["has_default"] is False

default_source_counts = {}
for item in parameters:
    source = item["default_source"]
    default_source_counts[source] = default_source_counts.get(source, 0) + 1
assert default_source_counts == {
    "none": 8,
    "static": 117,
    "host": 3,
    "runtime": 4,
    "generated": 9,
    "inherited": 5,
}, default_source_counts

# Pin representative portable constraints without requiring every parameter to
# have one. Adding a new constraint remains compatible; weakening one of these
# published examples is an intentional contract change.
constraint_cases = {
    "global.timezone": {"format": "iana_timezone"},
    "global.default_language": {"format": "language_tag"},
    "global.default_locale": {"format": "locale"},
    "global.host_ip": {"format": "ipv4"},
    "global.base_domain": {"format": "dns_name"},
    "samba_dc.domain": {"format": "dns_name"},
    "eturnal.port": {"minimum": 1, "maximum": 65535},
    "samba_dc.max_log_size": {"minimum": 1},
    "casdoor.ldap_auto_sync_minutes": {"minimum": 1},
}
for path, constraints in constraint_cases.items():
    assert by_path[path].get("constraints") == constraints, (
        path, by_path[path].get("constraints"), constraints
    )

# `unknown` remains readable for legacy/external Modules, but every parameter in
# the built-in release inventory must have an explicit declaration. In
# particular, an undeclared type must never be silently presented as `string`.
type_counts = {}
for item in parameters:
    kind = item["type"]
    assert kind in {"string", "bool", "int", "enum", "unknown"}, (item["path"], kind)
    type_counts[kind] = type_counts.get(kind, 0) + 1
    if kind == "enum":
        assert item.get("allowed_values"), (item["path"], "enum without allowed_values")
    else:
        assert "allowed_values" not in item, (item["path"], kind, item.get("allowed_values"))
assert type_counts == {
    "string": 83,
    "bool": 22,
    "int": 25,
    "enum": 16,
}, type_counts

global_types = {
    item["parameter"]: item["type"]
    for item in parameters
    if item["module"] == "global"
}
assert global_types == {
    "base_domain": "string",
    "chinese_build_speedup": "bool",
    "chinese_speedup": "bool",
    "container_prefix": "string",
    "default_language": "string",
    "default_locale": "string",
    "dns_server": "string",
    "email": "string",
    "host_ip": "string",
    "host_lan_arp_check": "bool",
    "host_lan_bridge_ip": "string",
    "host_lan_ip": "string",
    "ipv4": "bool",
    "ipv6": "bool",
    "network_prefix": "string",
    "timezone": "string",
    "virtual_domain": "bool",
}, global_types

# Input-required is intentionally conservative and `required` is its v1 alias.
# must_resolve additionally covers values supplied by host/runtime/inherited/
# generated sources or by a conditional deployment resolver. Conditional
# requirements belong to resolver/application/plan/Hook validation rather than
# either input-required field.
input_required_paths = {
    "global.base_domain",
    "global.email",
}
must_resolve_paths = input_required_paths | {
    "global.default_language",
    "global.default_locale",
    "global.host_ip",
    "global.timezone",
    "collabora.admin_password",
    "ddns_go.dns_provider",
    "ddns_updater.dns_provider",
    "lam.admin_password",
    "lam.language",
    "mariadb.root_password",
    "nextcloud.language",
    "nextcloud.locale",
    "postgres.password",
    "samba_dc.admin_password",
    "samba_dc.administrator_password",
    "samba_dc.anchor_bind_password",
    "samba_dc.domain",
    "samba_dc.ldap_bind_password",
    "samba_dc.netbios_name",
    "samba_dc.password_bind_password",
    "samba_dc.realm",
}
assert {item["path"] for item in parameters if item["input_required"]} == input_required_paths
assert {item["path"] for item in parameters if item["required"]} == input_required_paths
assert len(must_resolve_paths) == 23
assert {item["path"] for item in parameters if item["must_resolve"]} == must_resolve_paths

# These have no unconditional default source, but the deployment-level
# dynamic_dns.dns_provider resolver can supply them. `none` must therefore not
# be interpreted as an inverse alias for input_required.
for path in ("ddns_go.dns_provider", "ddns_updater.dns_provider"):
    item = by_path[path]
    assert item["input_required"] is False, item
    assert item["required"] is False, item
    assert item["must_resolve"] is True, item
    assert item["has_default"] is False, item
    assert item["default_source"] == "none", item

effects = {}
for item in parameters:
    effects[item["effect"]] = effects.get(item["effect"], 0) + 1
assert effects == {
    "container_recreate": 94,
    "credential_rotate": 7,
    "data_migrate": 11,
    "hot_reload": 16,
    "image_rebuild": 1,
    "immutable": 4,
    "reconcile": 13,
}, effects

# Every hot_reload/reconcile declaration must be tied to an explicit runtime
# capability case. Counts alone allowed a new parameter to inherit a broad
# effect without anyone deciding whether the upstream can actually apply it
# online, conditionally, or only by recreating runtime resources.
effect_cases = {
    "global.default_language": ("reconcile", "in_place", "localization"),
    "global.default_locale": ("reconcile", "in_place", "localization"),
    "global.virtual_domain": ("reconcile", "requires_recreate", "certificate_mode"),
    "authentik.domain_prefix": ("reconcile", "requires_recreate", "docker_route"),
    "casdoor.domain_prefix": ("reconcile", "requires_recreate", "docker_route"),
    "llng.domain_prefix": ("reconcile", "requires_recreate", "docker_route"),
    "lego.dns_provider": ("reconcile", "requires_recreate", "long_lived_env"),
    "nextcloud.domain_prefix": ("reconcile", "requires_recreate", "docker_route"),
    "nextcloud.language": ("reconcile", "in_place", "nextcloud_localization"),
    "nextcloud.locale": ("reconcile", "in_place", "nextcloud_localization"),
    "nextcloud.memories_enabled": ("reconcile", "conditional", "nextcloud_apps"),
    # These two are addressed through their owner alias by operators, but the
    # inventory prints their canonical storage paths because they intentionally
    # export unprefixed environment keys.
    "env.SHARE_ACCESS_MODE": ("reconcile", "in_place", "samba_fs_reload_acl"),
    "env.SHARE_GUEST_READ_ONLY": ("reconcile", "in_place", "samba_fs_reload_acl"),
}
for name in (
    "user_complex_pass", "user_min_pass_length", "user_password_history",
    "user_max_pass_age", "user_min_pass_age", "user_lockout_threshold",
    "user_lockout_duration", "user_lockout_reset_after",
    "admin_complex_pass", "admin_min_pass_length", "admin_password_history",
    "admin_max_pass_age", "admin_min_pass_age", "admin_lockout_threshold",
    "admin_lockout_duration", "admin_lockout_reset_after",
):
    effect_cases[f"samba_dc.{name}"] = ("hot_reload", "in_place", "samba_password_policy")

declared_effect_cases = {
    item["path"]: item for item in parameters
    if item["effect"] in {"hot_reload", "reconcile"}
}
assert set(declared_effect_cases) == set(effect_cases), {
    "missing_cases": sorted(set(declared_effect_cases) - set(effect_cases)),
    "stale_cases": sorted(set(effect_cases) - set(declared_effect_cases)),
}
for path, (effect, capability, runtime_case) in effect_cases.items():
    item = declared_effect_cases[path]
    assert item["effect"] == effect, (path, item["effect"], effect)
    assert capability in {"in_place", "conditional", "requires_recreate"}, (path, capability)
    assert runtime_case, path

virtual = next(item for item in parameters if item["path"] == "global.virtual_domain")
assert virtual["env_key"] == "VIRTUAL_DOMAIN", virtual
assert virtual["effect"] == "reconcile", virtual
assert virtual["apply"] == "reissue-certificates", virtual

# Raw-only user overrides are outside `config list`, but they are still part of
# the documented input surface. Prove each one has a repository-side consumer;
# a future upstream-only key must instead be covered by a source-pinned exception
# in the Go consumer audit.
raw_keys = {
    "APT_MIRROR_URL", "APK_MIRROR_URL", "NPM_REGISTRY_URL", "GOPROXY_URL",
    "GITHUB_DOWNLOAD_PROXY_PREFIX", "BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX",
    "NEXTCLOUD_APPSTORE_URL", "DOCKER_HUB_REGISTRY", "LLNG_DOCKER_HUB_REGISTRY",
    "ANAS_IMAGE_REGISTRY", "GHCR_REGISTRY", "NEXTCLOUD_APT_MIRROR_URL",
    "LAM_APT_MIRROR_URL", "LAM_DOWNLOAD_URL", "DOCKER_BUILD_NETWORK",
    "DOCKER_SOCKET_PATH",
}
runtime_roots = [os.path.join(sys.argv[2], part) for part in ("modules", "internal")]
for key in sorted(raw_keys):
    pattern = re.compile(r"(?:^|[^A-Z0-9_])" + re.escape(key) + r"(?:[^A-Z0-9_]|$)")
    found = False
    for runtime_root in runtime_roots:
        for base, _, names in os.walk(runtime_root):
            for name in names:
                if name == "README.md" or name.endswith("_test.go"):
                    continue
                path = os.path.join(base, name)
                try:
                    content = open(path, "rb").read().decode(errors="ignore")
                except OSError:
                    continue
                if pattern.search(content):
                    found = True
                    break
            if found:
                break
        if found:
            break
    assert found, (key, "documented raw override has no runtime consumer")
PY

  echo "== deleted manifest/global paths are rejected =="
  for path in \
    global.basicauth_user \
    global.image_prefix \
    global.default_service_root_password \
    collabora.interface \
    lego.virtual_domain \
    nextcloud.debug \
    traefik.subnet \
    traefik.gateway_ip
  do
    if run_anas config set "$path" unused -w "$ws" --root "$ROOT_DIR" >/dev/null 2>&1; then
      echo "$path unexpectedly remains configurable" >&2
      exit 1
    fi
  done

  echo "== deleted native YAML placeholders are rejected =="
  printf '%s\n' \
    'modules:' \
    '  traefik: {}' \
    'administration:' \
    '  bootstrap:' \
    '    username: admin' \
    '    display_name: Administrator' >"$invalid"
  if run_anas config import "$invalid" -w "$ws" >/dev/null 2>&1; then
    echo "administration.bootstrap.display_name unexpectedly remains accepted" >&2
    exit 1
  fi

  printf '%s\n' \
    'modules:' \
    '  traefik: {}' \
    'administration:' \
    '  local_accounts:' \
    '    password_policy: generated_per_module' >"$invalid"
  if run_anas config import "$invalid" -w "$ws" >/dev/null 2>&1; then
    echo "administration.local_accounts.password_policy unexpectedly remains accepted" >&2
    exit 1
  fi

  echo "== hand-written YAML cannot bypass manifest declarations =="
  printf '%s\n' \
    'modules:' \
    '  traefik:' \
    '    config:' \
    '      subnet: 172.23.0.0/16' >"$invalid"
  if run_anas config import "$invalid" -w "$ws" >/dev/null 2>&1; then
    echo "undeclared traefik.subnet unexpectedly passed config import" >&2
    exit 1
  fi

  echo "== global.virtual_domain produces the certificate-mode result =="
  run_anas config import "$CONFIG_DIR/min.yml" -w "$ws" >/dev/null
  run_anas lock -w "$ws" >/dev/null
  run_anas render -w "$ws" >/dev/null
  lego_env=$(find "$ws/.anas/deployments" -path '*/modules/lego/.env' -type f | head -1)
  test -n "$lego_env"
  grep -q '^VIRTUAL_DOMAIN=true$' "$lego_env"
  if grep -q '^LEGO_VIRTUAL_DOMAIN=' "$lego_env"; then
    echo "deleted module-local virtual_domain still leaks into the Lego environment" >&2
    exit 1
  fi
} >"$log" 2>&1

cat "$log"
echo "PASS: parameter inventory, removals, effects and rendered result"
