#!/usr/bin/env bash
# REQUIREMENTS: UPGRADE-R-030
set -euo pipefail

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
guard=$script_dir/server-require-upgrade-proxy-boundary.sh
test_dir=$(mktemp -d)
trap 'rm -rf "$test_dir"' EXIT HUP INT TERM

run_guard() {
  env -i PATH="$PATH" DOCKER_CONFIG="$1" DOCKER_HOST=unix:///run/anas-upgrade-e2e.sock \
    bash -c '. "$1"' bash "$guard"
}

mkdir "$test_dir/none"
run_guard "$test_dir/none"

mkdir "$test_dir/unsafe"
printf '%s\n' '{"proxies":{"default":{"httpProxy":"http://proxy.test:3128","noProxy":"localhost,127.0.0.1"}}}' \
  >"$test_dir/unsafe/config.json"
if run_guard "$test_dir/unsafe"; then
  echo "upgrade proxy guard accepted private Docker traffic through the outbound proxy" >&2
  exit 1
fi

mkdir "$test_dir/safe"
printf '%s\n' '{"proxies":{"default":{"httpProxy":"http://proxy.test:3128","noProxy":"localhost,127.0.0.1,10.0.0.0/8,172.16.0.0/12,192.168.0.0/16"}}}' \
  >"$test_dir/safe/config.json"
run_guard "$test_dir/safe"

mkdir "$test_dir/direct"
printf '%s\n' '{"proxies":{"default":{"httpsProxy":"http://proxy.test:3128","noProxy":"*"}}}' \
  >"$test_dir/direct/config.json"
run_guard "$test_dir/direct"

printf '%s\n' 'upgrade_proxy_boundary_test=pass build-proxy=allowed runtime-private-networks=direct'
