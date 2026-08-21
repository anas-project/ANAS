#!/usr/bin/env sh
set -eu

. "$(dirname -- "$0")/common.sh"

cd "$ROOT_DIR"
log="$REPORT_DIR/static.log"

# Runtime E2E entry points are intentionally not executed by this host-only
# gate, but their control flow must remain parseable before they reach a test
# server.
bash -n ./test-env/scripts/server-domain-separation-e2e.sh
sh -n ./test-env/scripts/server-isolated-docker.sh
sh ./test-env/scripts/test-domain-separation-server-configs.sh

if grep -Eq 'anas-test-docker-v3|anas-docker0' ./test-env/scripts/server-isolated-docker.sh; then
  echo "isolated Docker helper contains a cross-scope legacy cleanup target" >&2
  exit 1
fi

if find modules -type f \( -name '*.erb' -o -name '*.j2' -o -name '*.j3' -o -name '*.tmpl' \) -print -quit | grep -q .; then
  echo "legacy template suffixes are forbidden under modules/" >&2
  exit 1
fi
if grep -R -n -E '<%=|<%[[:space:]]+if|#\{envs\[' modules; then
  echo "legacy ERB syntax is forbidden under modules/" >&2
  exit 1
fi

# Published runtime images have one source. Build-time FROM references retain
# registry-specific inputs because CHINESE_BUILD_SPEEDUP controls them.
direct_compose_images=$(
  grep -R -n -E --include='docker-compose.yml' '^[[:space:]]+image:' modules |
    grep -v -E 'image:[[:space:]]+\$\{ANAS_IMAGE_REGISTRY' || true
)
if [ -n "$direct_compose_images" ]; then
  printf '%s\n' "$direct_compose_images" >&2
  echo "Compose image bypasses the configurable registry variables" >&2
  exit 1
fi
direct_dockerfile_images=$(
  grep -R -n -E --include='Dockerfile' '^FROM[[:space:]]+' modules |
    grep -v -E 'FROM[[:space:]]+(--platform=[^[:space:]]+[[:space:]]+)?(scratch|\$\{(DOCKER_HUB_REGISTRY|LLNG_DOCKER_HUB_REGISTRY|GHCR_REGISTRY|GO_BUILDER_REGISTRY|ANAS_IMAGE_REGISTRY)\})' || true
)
if [ -n "$direct_dockerfile_images" ]; then
  printf '%s\n' "$direct_dockerfile_images" >&2
  echo "Dockerfile FROM bypasses the configurable registry variables" >&2
  exit 1
fi

bash ./scripts/ci/cnb-container-images.sh validate
sh ./test-env/scripts/test-nextcloud-download-proxy.sh
go run ./cmd/gen-module-docs --check
go run ./cmd/gen-contract-docs --check

status=0
go test ./... >"$log" 2>&1 || status=$?
# Nested modules are excluded from ./... by design: a module component that is
# built inside its own image keeps its own module so the image build context
# stays the bundle rather than the whole repository. They still have to be
# tested, so each is listed here.
if [ "$status" -eq 0 ]; then
  for module in modules/ddns_go/ddns-go/reconcile; do
    (cd "$module" && go test ./...) >>"$log" 2>&1 || status=$?
  done
fi
if [ "$status" -eq 0 ] && command -v python3 >/dev/null 2>&1; then
  for suite in modules/samba_dc/anchor_worker modules/authentik/authentik; do
    PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s "$suite" -p 'test_*.py' >>"$log" 2>&1 || status=$?
  done
fi
if [ "$status" -eq 0 ]; then
  sh ./test-env/scripts/test-container-config.sh >>"$log" 2>&1 || status=$?
fi
cat "$log"
exit "$status"
