#!/usr/bin/env sh
# End-to-end proof that narrowing a module's environment changed nothing an
# application can see.
#
# A rendered .env stopped carrying everything the dependency closure happened to
# own and now carries only what the module declares. The key-set difference is
# large by design, so "the sets differ" proves nothing. What has to hold is that
# every value which reaches an application is still there, and there are exactly
# two ways one does:
#
# E1 Through Compose. Anything written as ${VAR} in docker-compose.yml -- image
#    tags, published ports, volume paths, router labels -- is substituted when
#    the stack is created. Two checks cover it: the resolved `docker compose
#    config` must be identical apart from the environment block, and every ${VAR}
#    the compose file names must still be present in the .env. The environment
#    block itself is excluded on purpose, because every module loads .env through
#    env_file, so Compose inlines the whole file there and the block differs by
#    exactly the reduction this change is for.
#
# E2 Through the container's own environment. Entrypoints, cont-init scripts and
#    .envsubst templates read variables at runtime out of env_file, so they
#    never appear in the Compose output at all. Every variable such a file
#    references must therefore still be present in the narrow .env.
#
# E1 and E2 are a pair: E1 alone would miss the whole envsubst layer, and E2
# alone would miss variables consumed by Compose itself, such as image tags and
# published ports.
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"

config=${ANAS_SCOPE_CONFIG:-$CONFIG_DIR/full.yml}
base=${ANAS_SCOPE_WORKSPACE_BASE:-$RUNTIME_DIR/env-scope}
log="$REPORT_DIR/env-scope.log"
failures=0

fail() {
  printf 'FAIL: %s\n' "$1"
  failures=$((failures + 1))
}

# `go run` is fine here: this suite asserts on rendered output rather than on
# exit codes, so the status collapsing that forces other suites to build a
# binary does not matter.
anas_bin="$ROOT_DIR/.anas-test/bin/anas"
mkdir -p "$(dirname -- "$anas_bin")" "$REPORT_DIR"
go build -o "$anas_bin" ./cmd/anas

: > "$log"

# render <workspace> <wide?> prints the rendered modules directory.
#
# The second workspace inherits the first one's secret store. Every `anas init`
# generates fresh secrets, so without this the two renderings differ in every
# derived credential -- client secrets, cookie secrets, database passwords --
# and those differences would swamp the one thing being measured.
render() {
  ws=$1
  wide=$2
  rm -rf "$ws"
  mkdir -p "$ws"
  "$anas_bin" init "$ws" -y >>"$log" 2>&1
  if [ -f "$base/wide/.anas/secrets.yml" ] && [ "$wide" != "1" ]; then
    cp "$base/wide/.anas/secrets.yml" "$ws/.anas/secrets.yml"
  fi
	"$anas_bin" config import "$config" -w "$ws" >>"$log" 2>&1
  "$anas_bin" lock -w "$ws" >>"$log" 2>&1
  if [ "$wide" = "1" ]; then
    ANAS_WIDE_ENV_SCOPE=1 "$anas_bin" render -w "$ws" >>"$log" 2>&1
  else
    "$anas_bin" render -w "$ws" >>"$log" 2>&1
  fi
  find "$ws/.anas/deployments" -mindepth 2 -maxdepth 2 -type d -name modules | head -1
}

echo "== rendering the same deployment under both rules =="
wide_dir=$(render "$base/wide" 1)
narrow_dir=$(render "$base/narrow" 0)
if [ -z "$wide_dir" ] || [ -z "$narrow_dir" ]; then
  echo "FAIL: rendering produced no deployment; see $log" >&2
  exit 1
fi

# ------------------------------------------------------------------ E1
#
# `docker compose config` resolves every ${VAR} against the module's .env, so two
# identical outputs mean no Compose-visible value changed. Without Docker the
# check cannot run, and saying so is better than reporting a pass that was never
# attempted.
echo "== E1: the resolved Compose configuration is unchanged =="
if ! docker compose version >/dev/null 2>&1; then
  echo "SKIP E1: docker compose is unavailable; the Compose-visible half is unchecked" >&2
else
  for narrow_module in "$narrow_dir"/*/; do
    module=$(basename "$narrow_module")
    compose="$narrow_module/docker-compose.yml"
    [ -f "$compose" ] || continue
    # The two renderings live in different directories and carry different
    # deployment ids, so every absolute path differs for reasons that have
    # nothing to do with scoping. Normalising them is what leaves an actual
    # value change as the only thing a diff can report.
    for side in wide narrow; do
      if [ "$side" = wide ]; then dir="$wide_dir/$module"; else dir="$narrow_module"; fi
      ( cd "$dir" && docker compose --env-file .env -f docker-compose.yml config 2>>"$log" ) \
        | sed -e "s|$base/wide|<WORKSPACE>|g" -e "s|$base/narrow|<WORKSPACE>|g" \
              -e 's|/deployments/[0-9TZ]*-[0-9a-f]*/|/deployments/<ID>/|g' \
        | python3 -c '
import sys
# Drop the environment: block, which is the env_file passthrough. Everything
# else in the resolved output came from a ${VAR} substitution and must match.
out, skip, indent = [], False, 0
for line in sys.stdin:
    stripped = line.strip()
    current = len(line) - len(line.lstrip())
    if skip and (current > indent or not stripped):
        continue
    skip = False
    if stripped == "environment:":
        skip, indent = True, current
        continue
    out.append(line)
sys.stdout.write("".join(out))
' > "$REPORT_DIR/compose-$side-$module.yml" || true
    done
    if ! diff -u "$REPORT_DIR/compose-wide-$module.yml" "$REPORT_DIR/compose-narrow-$module.yml" \
      > "$REPORT_DIR/compose-diff-$module.txt" 2>&1; then
      fail "$module: the resolved Compose configuration changed; see $REPORT_DIR/compose-diff-$module.txt"
    else
      rm -f "$REPORT_DIR/compose-diff-$module.txt"
    fi
    rm -f "$REPORT_DIR/compose-wide-$module.yml" "$REPORT_DIR/compose-narrow-$module.yml"
  done

  # Explicit ${VAR} references in the compose source. The diff above cannot see
  # these when they sit inside the environment block, and they are exactly the
  # values a module asked for by name.
  for narrow_module in "$narrow_dir"/*/; do
    module=$(basename "$narrow_module")
    [ -f "$narrow_module/docker-compose.yml" ] || continue
    for key in $(grep -ohE '\$\{[A-Z][A-Z0-9_]*' "$narrow_module/docker-compose.yml" | sed 's/^\${//' | sort -u); do
      # Against the wide baseline again. DOCKER_BUILD_NETWORK, DOCKER_SOCKET_PATH
      # and NPM_REGISTRY_URL are optional host overrides that nothing in the
      # runner or any manifest sets, so Compose substitutes them empty under
      # both rules; they were never delivered and cannot have been taken away.
      grep -qE "^$key=" "$wide_dir/$module/.env" 2>/dev/null || continue
      if ! grep -qE "^$key=" "$narrow_module/.env"; then
        fail "$module: docker-compose.yml substitutes \${$key}; it was delivered before and is not any more"
      fi
    done
  done
  echo "done"
fi

# ------------------------------------------------------------------ E2
#
# Everything that ships into the container and reads the environment at runtime.
# Only files that actually reach a container are scanned: the module's own hook is
# a build-time program on the host and its Go source would otherwise contribute
# every identifier it mentions.
echo "== E2: variables read inside containers are still delivered =="
python3 - "$narrow_dir" "$wide_dir" "$ROOT_DIR/modules" >"$REPORT_DIR/env-scope-missing.txt" 2>&1 <<'PY' || true
import os, re, sys

narrow, wide, mods = sys.argv[1], sys.argv[2], sys.argv[3]
# ${VAR} and $VAR as a shell or envsubst reference.
ref = re.compile(r'\$\{([A-Z][A-Z0-9_]*)\}|\$([A-Z][A-Z0-9_]{2,})\b')
# Shell and template constructs that look like references but are not the
# deployment's environment: loop variables, and anything the file assigns first.
assign = re.compile(r'^\s*(?:export\s+)?([A-Z][A-Z0-9_]*)=', re.M)

missing = {}
for module in sorted(os.listdir(narrow)):
    env_path = os.path.join(narrow, module, '.env')
    src = os.path.join(mods, module)
    if not os.path.isfile(env_path) or not os.path.isdir(src):
        continue
    have = set(re.findall(r'^([A-Z0-9_]+)=', open(env_path).read(), re.M))
    # The baseline is the wide rendering, not "present at all". A variable this
    # module never received cannot have been taken away by narrowing, and most of
    # what looks like a reference is not an environment value in the first
    # place: $TTL and $ORIGIN are BIND zone directives, APT_MIRROR_URL is a
    # Dockerfile build argument. Only a value that used to arrive and no longer
    # does is a regression.
    wide_path = os.path.join(wide, module, '.env')
    if not os.path.isfile(wide_path):
        continue
    had = set(re.findall(r'^([A-Z0-9_]+)=', open(wide_path).read(), re.M))
    for root, dirs, files in os.walk(src):
        # hook/ is host-side build tooling, not container content.
        dirs[:] = [d for d in dirs if d != 'hook']
        for name in files:
            path = os.path.join(root, name)
            rel = os.path.relpath(path, src)
            if name.endswith(('.md', '.yml', '.yaml')) and not name.endswith('.envsubst'):
                continue
            try:
                text = open(path, errors='ignore').read()
            except OSError:
                continue
            local = set(assign.findall(text))
            for a, b in ref.findall(text):
                key = a or b
                if key in local or key in have or key not in had:
                    continue
                missing.setdefault(module, {}).setdefault(key, set()).add(rel)

for module in sorted(missing):
    for key in sorted(missing[module]):
        print(f"{module}\t{key}\t{','.join(sorted(missing[module][key]))}")
PY

if [ -s "$REPORT_DIR/env-scope-missing.txt" ]; then
  while IFS="$(printf '\t')" read -r module key where; do
    [ -n "${module:-}" ] || continue
    fail "$module reads \$$key inside the container ($where); it was delivered before and is not any more"
  done < "$REPORT_DIR/env-scope-missing.txt"
else
  echo "done"
fi

# ------------------------------------------------------------------ report
echo "== scope narrowing summary =="
for narrow_module in "$narrow_dir"/*/; do
  module=$(basename "$narrow_module")
  [ -f "$narrow_module/.env" ] || continue
  w=$(grep -c . "$wide_dir/$module/.env" 2>/dev/null || echo 0)
  n=$(grep -c . "$narrow_module/.env")
  printf '  %-14s %4s -> %4s\n' "$module" "$w" "$n"
done

rm -rf "$base"
if [ "$failures" -gt 0 ]; then
  echo "$failures env-scope assertion(s) failed"
  exit 1
fi
echo "env scope checks complete"
