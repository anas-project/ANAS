#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/ci/module-revisions.sh [--base <git-ref>] [--write|--check|--print]

Calculate derived-image module revisions from changes to the build contexts
registered in .github/images.json.

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

catalog=.github/images.json
if [[ ! -f "$catalog" ]]; then
  echo "$catalog does not exist" >&2
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

replace_scalar() {
  local file="$1" key="$2" value="$3" tmp mode_bits
  tmp="${file}.revision-tmp.$$"
  mode_bits="$(stat -f '%Lp' "$file" 2>/dev/null || stat -c '%a' "$file")"
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
  mode_bits="$(stat -f '%Lp' "$file" 2>/dev/null || stat -c '%a' "$file")"
  awk -v image="$image" -v tag="$tag" '
    BEGIN { found = 0 }
    {
      needle = "/" image ":"
      if (index($0, needle)) {
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

context_changed() {
  local context
  while IFS= read -r context; do
    if ! git diff --quiet "$base" -- "$context"; then
      return 0
    fi
    if [[ -n "$(git ls-files --others --exclude-standard -- "$context")" ]]; then
      return 0
    fi
  done
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
    grep -Fq "/${image}:${version}-r${revision}" "$compose" || return 1
  done < <(jq -r --arg module "$module" '.[] | select(.module == $module) | .image' "$catalog")
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
    if ! grep -Fq "/${image}:${version}-r${revision}" "$compose"; then
      replace_compose_tag "$compose" "$image" "${version}-r${revision}"
    fi
  done < <(jq -r --arg module "$module" '.[] | select(.module == $module) | .image' "$catalog")
}

if ! jq -e '
  type == "array" and length > 0 and
  all(.[];
    (.module | type == "string" and length > 0) and
    (.image | type == "string" and test("^[a-z0-9-]+$")) and
    (.context | type == "string" and length > 0)
  )
' "$catalog" >/dev/null; then
  echo "$catalog is not a valid derived-image catalog" >&2
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
  elif jq -r --arg module "$module" '.[] | select(.module == $module) | .context' "$catalog" | context_changed; then
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
done < <(jq -r '.[].module' "$catalog" | LC_ALL=C sort -u)

if [[ "$mode" == write ]]; then
  echo "Revision metadata updated from base $base."
elif [[ "$mode" == check && "$drift" -ne 0 ]]; then
  exit 1
fi
