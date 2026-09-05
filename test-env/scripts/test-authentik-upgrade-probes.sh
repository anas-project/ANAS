#!/usr/bin/env bash
# REQUIREMENTS: UPGRADE-R-017
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
probe=$script_dir/server-authentik-upgrade-probes.sh

noproxy_calls=$(grep -Fc "curl -skS --noproxy '*'" "$probe")
[[ "$noproxy_calls" -eq 2 ]] || {
  echo "authentik upgrade probe has $noproxy_calls proxy-bypassed HTTPS helpers, want 2" >&2
  exit 1
}
grep -Fq '200|302) printf' "$probe"
if grep -Fq '200|302|403)' "$probe"; then
  echo "authentik upgrade probe accepts an unauthenticated 403 as backend reachability" >&2
  exit 1
fi
grep -Fq 'NetBird OIDC discovery endpoint returned status $discovery_code' "$probe"

printf '%s\n' 'authentik_upgrade_probe_test=pass internal-https=direct root=200-or-302 discovery=200-json'
