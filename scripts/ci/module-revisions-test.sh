#!/usr/bin/env bash
set -euo pipefail

source_script="$(cd "$(dirname "$0")" && pwd)/module-revisions.sh"
fixture="$(mktemp -d)"
trap 'rm -rf "$fixture"' EXIT

cd "$fixture"
git init -q
git config user.email revision-test@example.invalid
git config user.name revision-test
mkdir -p .github cmd/package-module contracts/example/docs internal/modulepackage modules/alpha/docs modules/alpha/image modules/alpha/helper modules/alpha/hook modules/alpha/runtime/docs modules/beta/image
cp "$source_script" module-revisions.sh
cat >.github/images.json <<'EOF'
[
  {"module":"alpha","image":"anas-alpha","context":"modules/alpha/image","dockerfile":"modules/alpha/image/Dockerfile"},
  {"module":"alpha","image":"anas-alpha-helper","context":"modules/alpha/helper","dockerfile":"modules/alpha/helper/Dockerfile"},
  {"module":"beta","image":"anas-beta","context":"modules/beta/image","dockerfile":"modules/beta/image/Dockerfile"}
]
EOF
cat >.github/modules.json <<'EOF'
[
  {"module":"alpha","repository":"anas-module-alpha","platforms":["linux/amd64","linux/arm64"],"shared_contexts":["contracts"]},
  {"module":"beta","repository":"anas-module-beta","platforms":["linux/amd64","linux/arm64"],"shared_contexts":["contracts"]}
]
EOF
cat >.github/mirrors.json <<'EOF'
[]
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
# A release-looking comment must never satisfy active service validation.
# /anas-alpha:1.2.3-r1
services:
  alpha:
    image: ${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-alpha:1.2.3-r1
  helper:
    image: ${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-alpha-helper:1.2.3-r1
EOF
cat >modules/beta/module.yml <<'EOF'
version: 4.5.6
revision: 7
EOF
cat >modules/beta/docker-compose.yml <<'EOF'
services:
  beta:
    image: ${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-beta:4.5.6-r7
EOF
printf 'FROM scratch\n' >modules/alpha/image/Dockerfile
printf 'FROM scratch\n' >modules/alpha/helper/Dockerfile
printf 'FROM scratch\n' >modules/beta/image/Dockerfile
printf 'package main\n' >modules/alpha/hook/main.go
printf 'runtime payload v1\n' >modules/alpha/runtime/docs/payload.txt
printf 'package main\n' >cmd/package-module/main.go
printf 'contract v1\n' >contracts/contract.yml
printf 'contract documentation\n' >contracts/example/documentation.yml
printf 'contract technical documentation\n' >contracts/example/docs/technical.md
printf 'package modulepackage\n' >internal/modulepackage/package.go
git add .
git commit -qm baseline
base="$(git rev-parse HEAD)"

bash ./module-revisions.sh --base "$base" --check >/dev/null
printf 'documentation only\n' >modules/alpha/README.md
printf 'technical documentation only\n' >modules/alpha/docs/technical.md
printf 'updated contract documentation only\n' >contracts/example/documentation.yml
printf 'updated contract technical documentation only\n' >contracts/example/docs/technical.md
bash ./module-revisions.sh --base "$base" --check >/dev/null

# Release-managed fields are consequences of the calculation, not reasons for
# a new release. Wrong prewritten values must be corrected back to the result
# calculated from the successful base.
sed 's/revision: 1/revision: 99/' modules/alpha/module.yml >modules/alpha/module.yml.tmp
mv modules/alpha/module.yml.tmp modules/alpha/module.yml
sed 's/module_revision: 1/module_revision: 99/' modules/alpha/localization.yml >modules/alpha/localization.yml.tmp
mv modules/alpha/localization.yml.tmp modules/alpha/localization.yml
awk '$1 == "image:" { gsub(/1.2.3-r1/, "1.2.3-r99") } { print }' modules/alpha/docker-compose.yml >modules/alpha/docker-compose.yml.tmp
mv modules/alpha/docker-compose.yml.tmp modules/alpha/docker-compose.yml
if bash ./module-revisions.sh --base "$base" --check >/dev/null 2>&1; then
  echo "check unexpectedly accepted wrong release-managed metadata" >&2
  exit 1
fi
bash ./module-revisions.sh --base "$base" --write >/dev/null
grep -qx 'revision: 1' modules/alpha/module.yml
grep -qx 'module_revision: 1' modules/alpha/localization.yml
grep -Fq '/anas-alpha:1.2.3-r1' modules/alpha/docker-compose.yml
grep -Fq '/anas-alpha-helper:1.2.3-r1' modules/alpha/docker-compose.yml

# A matching baseline comment must not conceal an incorrect service image tag.
awk '
  !changed && $1 == "image:" && index($0, "/anas-alpha:1.2.3-r1") {
    sub(/anas-alpha:1.2.3-r1/, "anas-alpha:1.2.3-r88")
    changed = 1
  }
  { print }
' modules/alpha/docker-compose.yml >modules/alpha/docker-compose.yml.tmp
mv modules/alpha/docker-compose.yml.tmp modules/alpha/docker-compose.yml
if bash ./module-revisions.sh --base "$base" --check >/dev/null 2>&1; then
  echo "check accepted a stale service image hidden by a matching comment" >&2
  exit 1
fi
bash ./module-revisions.sh --base "$base" --write >/dev/null
grep -Fq 'image: ${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-alpha:1.2.3-r1' modules/alpha/docker-compose.yml

printf 'runtime payload v2\n' >modules/alpha/runtime/docs/payload.txt
if bash ./module-revisions.sh --base "$base" --check >/dev/null 2>&1; then
  echo "check unexpectedly accepted stale metadata" >&2
  exit 1
fi
bash ./module-revisions.sh --base "$base" --write >/dev/null
grep -qx 'revision: 2' modules/alpha/module.yml
grep -qx 'module_revision: 2' modules/alpha/localization.yml
grep -Fq '/anas-alpha:1.2.3-r2' modules/alpha/docker-compose.yml
grep -Fq '/anas-alpha-helper:1.2.3-r2' modules/alpha/docker-compose.yml
grep -qx 'revision: 7' modules/beta/module.yml
grep -Fq '/anas-beta:4.5.6-r7' modules/beta/docker-compose.yml
bash ./module-revisions.sh --base "$base" --check >/dev/null

git add .
git commit -qm revision-two
base="$(git rev-parse HEAD)"
printf 'contract v2\n' >contracts/contract.yml
bash ./module-revisions.sh --base "$base" --write >/dev/null
grep -qx 'revision: 3' modules/alpha/module.yml
grep -qx 'revision: 8' modules/beta/module.yml

git add .
git commit -qm shared-context
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

git add .
git commit -qm version-two
base="$(git rev-parse HEAD)"
printf 'package modulepackage\n// packaging behavior changed\n' >internal/modulepackage/package.go
bash ./module-revisions.sh --base "$base" --write >/dev/null
grep -qx 'revision: 2' modules/alpha/module.yml
grep -qx 'revision: 9' modules/beta/module.yml
grep -Fq '/anas-alpha:2.0.0-r2' modules/alpha/docker-compose.yml
grep -Fq '/anas-beta:4.5.6-r9' modules/beta/docker-compose.yml

echo "module-revisions tests passed"
