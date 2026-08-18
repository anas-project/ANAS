#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/ci/module-revisions.sh [--base <git-ref>] [--write|--check|--print]

Calculate Module revisions from changes to their publishable runtime context.
Every Module is registered in .github/modules.json; .github/images.json remains
the catalog of Module-owned derived images whose tags follow the Module release.

  --write  update module.yml, localization.yml, and docker-compose.yml
  --check  fail when the checked-in values differ from the calculation
  --print  only print the calculation (default)

The base defaults to BASE_SHA, then the merge base with origin/master, then
HEAD. Pass the pull request base SHA or the previous push SHA in CI.
EOF
}

mode=print
base="${BASE_SHA:-}"
while [[ $# -gt 0 ]]; do
  case "$1" in
    --base)
      if [[ $# -lt 2 ]]; then
        echo "--base requires a git ref" >&2
        exit 2
      fi
      base="$2"
      shift 2
      ;;
    --write)
      mode=write
      shift
      ;;
    --check)
      mode=check
      shift
      ;;
    --print)
      mode=print
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    *)
      echo "unknown argument: $1" >&2
      usage >&2
      exit 2
      ;;
  esac
done

repo_root="$(git rev-parse --show-toplevel 2>/dev/null)" || {
  echo "module-revisions.sh must run inside a Git worktree" >&2
  exit 2
}
cd "$repo_root"

image_catalog=.github/images.json
module_catalog=.github/modules.json
if [[ ! -f "$image_catalog" || ! -f "$module_catalog" ]]; then
  echo "$image_catalog and $module_catalog must exist" >&2
  exit 2
fi
if ! command -v jq >/dev/null 2>&1; then
  echo "jq is required" >&2
  exit 2
fi

if [[ -z "$base" ]]; then
  if git show-ref --verify --quiet refs/remotes/origin/master; then
    base="$(git merge-base HEAD origin/master)"
  else
    base=HEAD
  fi
fi
base="$(git rev-parse --verify "${base}^{commit}" 2>/dev/null)" || {
  echo "base ref is not a commit: ${base:-<empty>}" >&2
  exit 2
}

read_scalar() {
  local file="$1" key="$2"
  awk -v key="$key" '$1 == key ":" { print $2; exit }' "$file"
}

read_base_scalar() {
  local file="$1" key="$2"
  git show "${base}:${file}" 2>/dev/null |
    awk -v key="$key" '$1 == key ":" { print $2; exit }'
}

file_mode() {
  local file="$1" mode

  if mode="$(stat -c '%a' "$file" 2>/dev/null)"; then
    printf '%s\n' "$mode"
  else
    stat -f '%Lp' "$file"
  fi
}

replace_scalar() {
  local file="$1" key="$2" value="$3" tmp mode_bits
  tmp="${file}.revision-tmp.$$"
  mode_bits="$(file_mode "$file")"
  awk -v key="$key" -v value="$value" '
    BEGIN { found = 0 }
    $1 == key ":" {
      if (found) {
        print "duplicate " key " in " FILENAME > "/dev/stderr"
        exit 3
      }
      print key ": " value
      found = 1
      next
    }
    { print }
    END {
      if (!found) {
        print "missing " key " in " FILENAME > "/dev/stderr"
        exit 4
      }
    }
  ' "$file" >"$tmp" || {
    rm -f "$tmp"
    return 1
  }
  chmod "$mode_bits" "$tmp"
  mv "$tmp" "$file"
}

replace_compose_tag() {
  local file="$1" image="$2" tag="$3" tmp mode_bits
  tmp="${file}.revision-tmp.$$"
  mode_bits="$(file_mode "$file")"
  awk -v image="$image" -v tag="$tag" '
    BEGIN { found = 0 }
    {
      needle = "/" image ":"
      if ($1 == "image:" && index($0, needle)) {
        prefix = substr($0, 1, index($0, needle) + length(needle) - 1)
        rest = substr($0, length(prefix) + 1)
        sub(/^[^[:space:]"\047]+/, tag, rest)
        $0 = prefix rest
        found = 1
      }
      print
    }
    END {
      if (!found) {
        print "missing image " image " in " FILENAME > "/dev/stderr"
        exit 4
      }
    }
  ' "$file" >"$tmp" || {
    rm -f "$tmp"
    return 1
  }
  chmod "$mode_bits" "$tmp"
  mv "$tmp" "$file"
}

compose_image_matches() {
  local file="$1" image="$2" tag="$3"
  awk -v image="$image" -v expected="$tag" '
    BEGIN { found = 0; valid = 1 }
    $1 == "image:" {
      needle = "/" image ":"
      position = index($0, needle)
      if (!position) next
      found++
      actual = substr($0, position + length(needle))
      sub(/[[:space:]"\047#].*$/, "", actual)
      if (actual != expected) valid = 0
    }
    END { exit !(found > 0 && valid) }
  ' "$file"
}

runtime_path() {
  local path="$1" basename relative
  basename="${path##*/}"
  case "$basename" in
    README*.md|localization.yml|.DS_Store|*_test.go|test_*.py) return 1 ;;
  esac
  if [[ "$path" == modules/*/* ]]; then
    relative="${path#modules/}"
    relative="${relative#*/}"
    [[ "$relative" == docs/* ]] && return 1
  elif [[ "$path" == contracts/*/* ]]; then
    relative="${path#contracts/}"
    relative="${relative#*/}"
    [[ "$relative" == docs/* || "$relative" == documentation.yml ]] && return 1
  fi
  [[ "$path" != */__pycache__/* && "$path" != modules/ddns_go/ddns-go/reconcile/reconcile ]]
}

normalize_module_manifest() {
  awk '
    /^revision:[[:space:]]/ { print "revision: <managed>"; next }
    { print }
  '
}

normalize_module_compose() {
  local module="$1" images
  images="$(jq -r --arg module "$module" '.[] | select(.module == $module) | .image' "$image_catalog" | paste -sd '|' -)"
  awk -v images="$images" '
    BEGIN { count = split(images, managed, "|") }
    {
      line = $0
      if ($1 != "image:") {
        print line
        next
      }
      for (i = 1; i <= count; i++) {
        if (managed[i] == "") continue
        needle = "/" managed[i] ":"
        position = index(line, needle)
        if (!position) continue
        prefix = substr(line, 1, position + length(needle) - 1)
        rest = substr(line, length(prefix) + 1)
        sub(/^[^[:space:]"\047#]+/, "<managed>", rest)
        line = prefix rest
      }
      print line
    }
  '
}

managed_file_differs() {
  local module="$1" path="$2"
  [[ -f "$path" ]] || return 0
  git cat-file -e "${base}:${path}" 2>/dev/null || return 0
  # File-mode changes affect the package even when normalized content matches.
  if [[ -n "$(git diff --summary "$base" -- "$path")" ]]; then
    return 0
  fi
  case "$path" in
    modules/*/module.yml)
      ! diff -q \
        <(git show "${base}:${path}" | normalize_module_manifest) \
        <(normalize_module_manifest <"$path") >/dev/null
      ;;
    modules/*/docker-compose.yml)
      ! diff -q \
        <(git show "${base}:${path}" | normalize_module_compose "$module") \
        <(normalize_module_compose "$module" <"$path") >/dev/null
      ;;
    *) return 0 ;;
  esac
}

module_path_context_changed() {
  local module="$1" path
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    runtime_path "$path" || continue
    case "$path" in
      "modules/${module}/module.yml"|"modules/${module}/docker-compose.yml")
        managed_file_differs "$module" "$path" || continue
        ;;
    esac
    return 0
  done < <(git diff --name-only "$base" -- "modules/${module}")
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    if runtime_path "$path"; then
      return 0
    fi
  done < <(git ls-files --others --exclude-standard -- "modules/${module}")
  return 1
}

path_context_changed() {
  local context="$1" path
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    if runtime_path "$path"; then
      return 0
    fi
  done < <(git diff --name-only "$base" -- "$context")
  while IFS= read -r path; do
    [[ -z "$path" ]] && continue
    if runtime_path "$path"; then
      return 0
    fi
  done < <(git ls-files --others --exclude-standard -- "$context")
  return 1
}

catalog_entry_changed() {
  local catalog="$1" filter="$2" before after
  # Introducing a catalog makes existing releases discoverable; it does not
  # change their runtime content and must not manufacture a revision bump for
  # every Module during the first independent-package release.
  if ! git cat-file -e "${base}:${catalog}" 2>/dev/null; then
    return 1
  fi
  before="$(git show "${base}:${catalog}" 2>/dev/null | jq -cS "$filter" 2>/dev/null || true)"
  if [[ -f "$catalog" ]]; then
    after="$(jq -cS "$filter" "$catalog")"
  else
    after='[]'
  fi
  [[ "$before" != "$after" ]]
}

module_context_changed() {
  local module="$1" context
  # The packager is an input to every bundle. Its initial introduction only
  # exposes existing releases, while later behavior changes produce new bytes
  # and therefore require a new revision for every Module.
  if git cat-file -e "${base}:${module_catalog}" 2>/dev/null; then
    for context in cmd/package-module internal/modulepackage; do
      if path_context_changed "$context"; then
        return 0
      fi
    done
  fi
  if module_path_context_changed "$module"; then
    return 0
  fi
  while IFS= read -r context; do
    [[ -z "$context" ]] && continue
    if path_context_changed "$context"; then
      return 0
    fi
  done < <(jq -r --arg module "$module" '.[] | select(.module == $module) | .shared_contexts[]?' "$module_catalog")
  if catalog_entry_changed "$module_catalog" "[.[] | select(.module == \"$module\")]"; then
    return 0
  fi
  if catalog_entry_changed "$image_catalog" "[.[] | select(.module == \"$module\")]"; then
    return 0
  fi
  if { [[ -f .github/mirrors.json ]] || git cat-file -e "${base}:.github/mirrors.json" 2>/dev/null; } &&
     catalog_entry_changed .github/mirrors.json "[.[] | select(.modules | index(\"$module\"))]"; then
    return 0
  fi
  return 1
}

metadata_matches() {
  local module="$1" version="$2" revision="$3" manifest localization compose image
  manifest="modules/${module}/module.yml"
  localization="modules/${module}/localization.yml"
  compose="modules/${module}/docker-compose.yml"

  [[ "$(read_scalar "$manifest" revision)" == "$revision" ]] || return 1
  if [[ -f "$localization" ]]; then
    [[ "$(read_scalar "$localization" module_version)" == "$version" ]] || return 1
    [[ "$(read_scalar "$localization" module_revision)" == "$revision" ]] || return 1
  fi
  while IFS= read -r image; do
    compose_image_matches "$compose" "$image" "${version}-r${revision}" || return 1
  done < <(jq -r --arg module "$module" '.[] | select(.module == $module) | .image' "$image_catalog")
}

write_metadata() {
  local module="$1" version="$2" revision="$3" manifest localization compose image
  manifest="modules/${module}/module.yml"
  localization="modules/${module}/localization.yml"
  compose="modules/${module}/docker-compose.yml"

  if [[ "$(read_scalar "$manifest" revision)" != "$revision" ]]; then
    replace_scalar "$manifest" revision "$revision"
  fi
  if [[ -f "$localization" ]]; then
    if [[ "$(read_scalar "$localization" module_version)" != "$version" ]]; then
      replace_scalar "$localization" module_version "$version"
    fi
    if [[ "$(read_scalar "$localization" module_revision)" != "$revision" ]]; then
      replace_scalar "$localization" module_revision "$revision"
    fi
  fi
  while IFS= read -r image; do
    if ! compose_image_matches "$compose" "$image" "${version}-r${revision}"; then
      replace_compose_tag "$compose" "$image" "${version}-r${revision}"
    fi
  done < <(jq -r --arg module "$module" '.[] | select(.module == $module) | .image' "$image_catalog")
}

if ! jq -e '
  type == "array" and length > 0 and
  all(.[];
    (.module | type == "string" and length > 0) and
    (.image | type == "string" and test("^[a-z0-9-]+$")) and
    (.context | type == "string" and length > 0)
  )
' "$image_catalog" >/dev/null; then
  echo "$image_catalog is not a valid derived-image catalog" >&2
  exit 2
fi

if ! jq -e '
  type == "array" and length > 0 and
  all(.[ ];
    (.module | type == "string" and test("^[a-z][a-z0-9_]*$")) and
    (.repository | type == "string" and test("^anas-module-[a-z0-9-]+$")) and
    (.platforms | type == "array" and length > 0 and all(.[]; . == "linux/amd64" or . == "linux/arm64")) and
    ((.shared_contexts // []) | type == "array")
  ) and
  ([.[].module] | length == (unique | length)) and
  ([.[].repository] | length == (unique | length))
' "$module_catalog" >/dev/null; then
  echo "$module_catalog is not a valid Module package catalog" >&2
  exit 2
fi

actual_modules="$(find modules -mindepth 2 -maxdepth 2 -type f -name module.yml | sed 's#^modules/##;s#/module.yml$##' | LC_ALL=C sort)"
registered_modules="$(jq -r '.[].module' "$module_catalog" | LC_ALL=C sort)"
if ! diff -u <(printf '%s\n' "$actual_modules") <(printf '%s\n' "$registered_modules"); then
  echo "Every Module must appear exactly once in $module_catalog" >&2
  exit 2
fi

drift=0
while IFS= read -r module; do
  manifest="modules/${module}/module.yml"
  if [[ ! -f "$manifest" ]]; then
    echo "missing manifest: $manifest" >&2
    exit 2
  fi

  version="$(read_scalar "$manifest" version)"
  current_revision="$(read_scalar "$manifest" revision)"
  if [[ -z "$version" || ! "$current_revision" =~ ^[1-9][0-9]*$ ]]; then
    echo "$manifest must contain a version and a positive integer revision" >&2
    exit 2
  fi

  old_version="$(read_base_scalar "$manifest" version || true)"
  old_revision="$(read_base_scalar "$manifest" revision || true)"
  reason=unchanged
  if [[ -z "$old_version" ]]; then
    expected_revision=1
    reason=new-module
  elif [[ ! "$old_revision" =~ ^[1-9][0-9]*$ ]]; then
    echo "${base}:${manifest} has an invalid revision: ${old_revision:-<missing>}" >&2
    exit 2
  elif [[ "$version" != "$old_version" ]]; then
    expected_revision=1
    reason=version-changed
  elif module_context_changed "$module"; then
    expected_revision=$((old_revision + 1))
    reason=context-changed
  else
    expected_revision="$old_revision"
  fi

  printf '%s\t%s-r%s\t%s\n' "$module" "$version" "$expected_revision" "$reason"

  if [[ "$mode" == write ]]; then
    write_metadata "$module" "$version" "$expected_revision"
  elif [[ "$mode" == check ]] && ! metadata_matches "$module" "$version" "$expected_revision"; then
    echo "revision metadata for $module is stale; run: scripts/ci/module-revisions.sh --base $base --write" >&2
    drift=1
  fi
done < <(jq -r '.[].module' "$module_catalog" | LC_ALL=C sort -u)

if [[ "$mode" == write ]]; then
  echo "Revision metadata updated from base $base."
elif [[ "$mode" == check && "$drift" -ne 0 ]]; then
  exit 1
fi
