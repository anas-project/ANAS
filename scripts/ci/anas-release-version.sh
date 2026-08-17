#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
Usage: scripts/ci/anas-release-version.sh [options]

Resolve the next ANAS Core release from immutable vMAJOR.MINOR.PATCH tags.

  --commit <git-ref>       release commit (default: HEAD)
  --initial <version>      first stable release (default: 0.1.0)
  --version <version>      exact manually requested SemVer
  --bump <kind>            patch, minor, or major (default: patch)
  --automatic              skip when Core inputs did not change

The command prints exactly one decision:

  release:<version>
  skip:<reason>
EOF
}

commit=HEAD
initial=0.1.0
requested=
bump=patch
automatic=false

while [[ $# -gt 0 ]]; do
  case "$1" in
    --commit)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      commit="$2"
      shift 2
      ;;
    --initial)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      initial="$2"
      shift 2
      ;;
    --version)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      requested="$2"
      shift 2
      ;;
    --bump)
      [[ $# -ge 2 ]] || { usage >&2; exit 2; }
      bump="$2"
      shift 2
      ;;
    --automatic)
      automatic=true
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

semver_pattern='^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?(\+[0-9A-Za-z.-]+)?$'
stable_pattern='^(0|[1-9][0-9]*)\.((0|[1-9][0-9]*))\.((0|[1-9][0-9]*))$'

if [[ ! "$initial" =~ $stable_pattern ]]; then
  echo "initial version must be stable SemVer: $initial" >&2
  exit 2
fi
case "$bump" in
  patch|minor|major) ;;
  *)
    echo "bump must be patch, minor, or major: $bump" >&2
    exit 2
    ;;
esac

commit="$(git rev-parse --verify "${commit}^{commit}" 2>/dev/null)" || {
  echo "release ref is not a commit: $commit" >&2
  exit 2
}

latest_tag=
latest_major=-1
latest_minor=-1
latest_patch=-1
while IFS= read -r tag; do
  [[ -z "$tag" ]] && continue
  version="${tag#v}"
  if [[ "$version" =~ $stable_pattern ]]; then
    major="${BASH_REMATCH[1]}"
    minor="${BASH_REMATCH[2]}"
    patch="${BASH_REMATCH[4]}"
    if (( major > latest_major ||
          (major == latest_major && minor > latest_minor) ||
          (major == latest_major && minor == latest_minor && patch > latest_patch) )); then
      latest_tag="$tag"
      latest_major="$major"
      latest_minor="$minor"
      latest_patch="$patch"
    fi
  fi
done < <(git tag --merged "$commit" --list 'v*')

tag_commit() {
  git rev-list -n1 "$1" 2>/dev/null
}

version_is_newer_than_latest() {
  local version="$1" major minor patch
  [[ "$version" =~ $stable_pattern ]] || return 0
  major="${BASH_REMATCH[1]}"
  minor="${BASH_REMATCH[2]}"
  patch="${BASH_REMATCH[4]}"
  (( latest_major < 0 ||
     major > latest_major ||
     (major == latest_major && minor > latest_minor) ||
     (major == latest_major && minor == latest_minor && patch > latest_patch) ))
}

if [[ -n "$requested" ]]; then
  if [[ ! "$requested" =~ $semver_pattern ]]; then
    echo "invalid requested SemVer: $requested" >&2
    exit 2
  fi
  requested_tag="v${requested}"
  if existing_commit="$(tag_commit "$requested_tag")"; then
    if [[ "$existing_commit" == "$commit" ]]; then
      echo "skip:${requested_tag}"
      exit 0
    fi
    echo "immutable release tag $requested_tag already points to $existing_commit" >&2
    exit 2
  fi
  if ! version_is_newer_than_latest "$requested"; then
    echo "requested version $requested must be newer than ${latest_tag#v}" >&2
    exit 2
  fi
  echo "release:${requested}"
  exit 0
fi

while IFS= read -r tag; do
  [[ -z "$tag" ]] && continue
  if [[ "${tag#v}" =~ $semver_pattern ]]; then
    echo "skip:${tag}"
    exit 0
  fi
done < <(git tag --points-at "$commit" --list 'v*')

if [[ "$automatic" == true && -n "$latest_tag" ]] &&
   git diff --quiet "$latest_tag" "$commit" -- \
     cmd/anas install.sh internal go.mod go.sum \
     scripts/ci/build-anas-release.sh scripts/ci/install-test.sh; then
  echo "skip:no-core-changes"
  exit 0
fi

if [[ -z "$latest_tag" ]]; then
  next="$initial"
else
  case "$bump" in
    patch) next="${latest_major}.${latest_minor}.$((latest_patch + 1))" ;;
    minor) next="${latest_major}.$((latest_minor + 1)).0" ;;
    major) next="$((latest_major + 1)).0.0" ;;
  esac
fi

next_tag="v${next}"
if existing_commit="$(tag_commit "$next_tag")"; then
  if [[ "$existing_commit" == "$commit" ]]; then
    echo "skip:${next_tag}"
    exit 0
  fi
  echo "calculated release tag $next_tag already points to $existing_commit" >&2
  exit 2
fi
echo "release:${next}"
