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
assert len(parameters) == 131, len(parameters)

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
    for field in ("path", "module", "parameter", "env_key", "effect", "apply", "description"):
        assert item.get(field), (item.get("path"), field, item.get(field))

effects = {}
for item in parameters:
    effects[item["effect"]] = effects.get(item["effect"], 0) + 1
assert effects == {
    "container_recreate": 91,
    "credential_rotate": 7,
    "data_migrate": 9,
    "hot_reload": 8,
    "image_rebuild": 1,
    "immutable": 3,
    "reconcile": 12,
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
