#!/usr/bin/env bash
set -euo pipefail

source_script="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/anas-release-version.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

cd "$fixture"
git init -q
git config user.email release-test@example.invalid
git config user.name release-test
mkdir -p cmd/anas internal/example docs
printf 'package main\n' >cmd/anas/main.go
printf 'package example\n' >internal/example/example.go
printf 'module example.invalid/anas\n\ngo 1.26\n' >go.mod
printf '#!/usr/bin/env sh\n' >install.sh
printf '# docs\n' >docs/index.md
git add .
git commit -qm initial
initial_commit="$(git rev-parse HEAD)"

decision="$(bash "$source_script" --commit HEAD --automatic)"
[[ "$decision" == release:0.1.0 ]]

git tag v0.1.0
decision="$(bash "$source_script" --commit HEAD --automatic)"
[[ "$decision" == skip:v0.1.0 ]]

printf '# documentation only\n' >>docs/index.md
git add docs/index.md
git commit -qm docs
decision="$(bash "$source_script" --commit HEAD --automatic)"
[[ "$decision" == skip:no-core-changes ]]

printf '# installer changed\n' >>install.sh
git add install.sh
git commit -qm installer
decision="$(bash "$source_script" --commit HEAD --automatic)"
[[ "$decision" == release:0.1.1 ]]

printf '// core changed\n' >>internal/example/example.go
git add internal/example/example.go
git commit -qm core
decision="$(bash "$source_script" --commit HEAD --automatic)"
[[ "$decision" == release:0.1.1 ]]

decision="$(bash "$source_script" --commit HEAD --bump minor)"
[[ "$decision" == release:0.2.0 ]]
decision="$(bash "$source_script" --commit HEAD --bump major)"
[[ "$decision" == release:1.0.0 ]]
decision="$(bash "$source_script" --commit HEAD --version 0.5.0)"
[[ "$decision" == release:0.5.0 ]]
decision="$(bash "$source_script" --commit HEAD --version 0.5.0-rc.1)"
[[ "$decision" == release:0.5.0-rc.1 ]]

if bash "$source_script" --commit HEAD --version invalid >/dev/null 2>&1; then
  echo "invalid SemVer unexpectedly accepted" >&2
  exit 1
fi
if bash "$source_script" --commit HEAD --version 0.0.9 >/dev/null 2>&1; then
  echo "version regression unexpectedly accepted" >&2
  exit 1
fi

git tag v0.1.1
printf '// another core change\n' >>cmd/anas/main.go
git add cmd/anas/main.go
git commit -qm second-core
decision="$(bash "$source_script" --commit HEAD --automatic)"
[[ "$decision" == release:0.1.2 ]]

if bash "$source_script" --commit "$initial_commit" --version 0.1.1 >/dev/null 2>&1; then
  echo "existing immutable tag unexpectedly reused" >&2
  exit 1
fi

echo "ANAS release version tests passed"
