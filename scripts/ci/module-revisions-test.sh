#!/usr/bin/env bash
set -euo pipefail

source_script="$(cd "$(dirname "$0")" && pwd)/module-revisions.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

cd "$fixture"
git init -q
git config user.email revision-test@example.invalid
git config user.name revision-test
mkdir -p .github modules/alpha/image modules/alpha/helper
cp "$source_script" module-revisions.sh
cat >.github/images.json <<'EOF'
[
  {"module":"alpha","image":"anas-alpha","context":"modules/alpha/image","dockerfile":"modules/alpha/image/Dockerfile"},
  {"module":"alpha","image":"anas-alpha-helper","context":"modules/alpha/helper","dockerfile":"modules/alpha/helper/Dockerfile"}
]
EOF
cat >modules/alpha/module.yml <<'EOF'
version: 1.2.3
revision: 1
EOF
cat >modules/alpha/localization.yml <<'EOF'
module_version: 1.2.3
module_revision: 1
EOF
cat >modules/alpha/docker-compose.yml <<'EOF'
services:
  alpha:
    image: ${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-alpha:1.2.3-r1
  helper:
    image: ${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-alpha-helper:1.2.3-r1
EOF
printf 'FROM scratch\n' >modules/alpha/image/Dockerfile
printf 'FROM scratch\n' >modules/alpha/helper/Dockerfile
git add .
git commit -qm baseline
base="$(git rev-parse HEAD)"

bash ./module-revisions.sh --base "$base" --check >/dev/null
printf 'changed\n' >modules/alpha/helper/entrypoint.sh
if bash ./module-revisions.sh --base "$base" --check >/dev/null 2>&1; then
  echo "check unexpectedly accepted stale metadata" >&2
  exit 1
fi
bash ./module-revisions.sh --base "$base" --write >/dev/null
grep -qx 'revision: 2' modules/alpha/module.yml
grep -qx 'module_revision: 2' modules/alpha/localization.yml
grep -Fq '/anas-alpha:1.2.3-r2' modules/alpha/docker-compose.yml
grep -Fq '/anas-alpha-helper:1.2.3-r2' modules/alpha/docker-compose.yml
bash ./module-revisions.sh --base "$base" --check >/dev/null

git add .
git commit -qm revision-two
base="$(git rev-parse HEAD)"
sed 's/version: 1.2.3/version: 2.0.0/' modules/alpha/module.yml >modules/alpha/module.yml.tmp
mv modules/alpha/module.yml.tmp modules/alpha/module.yml
printf 'changed again\n' >>modules/alpha/image/Dockerfile
bash ./module-revisions.sh --base "$base" --write >/dev/null
grep -qx 'revision: 1' modules/alpha/module.yml
grep -qx 'module_version: 2.0.0' modules/alpha/localization.yml
grep -qx 'module_revision: 1' modules/alpha/localization.yml
grep -Fq '/anas-alpha:2.0.0-r1' modules/alpha/docker-compose.yml
grep -Fq '/anas-alpha-helper:2.0.0-r1' modules/alpha/docker-compose.yml

echo "module-revisions tests passed"
