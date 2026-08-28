#!/usr/bin/env bash
set -euo pipefail

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)
# shellcheck source=server-require-isolated-docker.sh
source "$script_dir/server-require-isolated-docker.sh"

: "${ANAS_TEST_IAM_PROVIDER:?ANAS_TEST_IAM_PROVIDER is required}"
: "${ANAS_TEST_IAM_PROTOCOL:?ANAS_TEST_IAM_PROTOCOL is required}"
: "${ANAS_TEST_APP:?ANAS_TEST_APP is required}"
: "${ANAS_TEST_USERNAME:?ANAS_TEST_USERNAME is required}"
: "${ANAS_TEST_PASSWORD:?ANAS_TEST_PASSWORD is required}"

report_dir=${ANAS_TEST_REPORT_DIR:-$repo_root/test-env/reports}
mkdir -p "$report_dir"
umask 077
export ANAS_TEST_REPORT_FILE=${ANAS_TEST_REPORT_FILE:-$report_dir/iam-logout-${ANAS_TEST_IAM_PROVIDER}-${ANAS_TEST_IAM_PROTOCOL}-${ANAS_TEST_APP}.json}

cd "$repo_root"
npm run e2e:iam-logout
node test-env/playwright/validate-sanitized-report.mjs "$ANAS_TEST_REPORT_FILE"
printf 'IAM logout browser report: %s\n' "$ANAS_TEST_REPORT_FILE"
