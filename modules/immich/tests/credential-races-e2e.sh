#!/usr/bin/env bash
set -Eeuo pipefail
umask 077
script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
scope="anas-immich-credentials-$(date +%s)-${RANDOM}"
network="${scope}-network"
postgres="${scope}-postgres"
worker="${scope}-worker"
immich_image=${ANAS_CREDENTIAL_IMMICH_IMAGE:-anas-immich-e2e:3.2.4}
pg_image=${ANAS_CREDENTIAL_PG_IMAGE:-postgres:18.4-alpine3.23}
password="${scope}-${RANDOM}-disposable"
cleanup() {
  for owned in "$worker" "$postgres"; do
    if [[ "$(docker container inspect --format '{{index .Config.Labels "anas.test.immich-credentials"}}' "$owned" 2>/dev/null || true)" == "$scope" ]]; then
      docker rm -f -v "$owned" >/dev/null 2>&1 || true
    fi
  done
  if [[ "$(docker network inspect --format '{{index .Labels "anas.test.immich-credentials"}}' "$network" 2>/dev/null || true)" == "$scope" ]]; then
    docker network rm "$network" >/dev/null 2>&1 || true
  fi
}
docker image inspect "$immich_image" "$pg_image" >/dev/null
immich_image_id=$(docker image inspect --format '{{.Id}}' "$immich_image")
pg_image_id=$(docker image inspect --format '{{.Id}}' "$pg_image")
for candidate in "$worker" "$postgres"; do
  if docker container inspect "$candidate" >/dev/null 2>&1; then printf 'test container name already exists\n' >&2; exit 2; fi
done
if docker network inspect "$network" >/dev/null 2>&1; then printf 'test network name already exists\n' >&2; exit 2; fi
trap cleanup EXIT
docker network create --label "anas.test.immich-credentials=$scope" "$network" >/dev/null
docker run -d --name "$postgres" --label "anas.test.immich-credentials=$scope" --network "$network" \
  -e POSTGRES_PASSWORD="$password" -e POSTGRES_HOST_AUTH_METHOD=scram-sha-256 "$pg_image_id" >/dev/null
ready=false
for _ in $(seq 1 60); do
  if docker exec "$postgres" pg_isready -U postgres >/dev/null 2>&1; then ready=true; break; fi
  sleep 1
done
[[ "$ready" == true ]] || { printf 'isolated PostgreSQL did not become ready\n' >&2; exit 1; }
docker exec -i "$postgres" psql -X -U postgres -v ON_ERROR_STOP=1 >/dev/null <<SQL
CREATE ROLE immich_credential_test LOGIN PASSWORD '$password' NOSUPERUSER NOCREATEDB NOCREATEROLE;
CREATE DATABASE immich_credential_test OWNER immich_credential_test;
SQL
docker run --rm --name "$worker" --label "anas.test.immich-credentials=$scope" --network "$network" \
  -e PGHOST="$postgres" -e PGUSER=immich_credential_test -e PGPASSWORD="$password" -e PGDATABASE=immich_credential_test \
  -e ANAS_CREDENTIAL_TEST_DIST=/tmp/anas-credential-dist \
  -e NODE_PATH=/usr/src/app/server/node_modules \
  --volume "$script_dir:/anas-tests:ro" --volume "$script_dir/../immich:/anas-patch:ro" \
  --entrypoint sh "$immich_image_id" -c '
    set -eu
    cp -a /usr/src/app/server/dist /tmp/anas-credential-dist
    cp /usr/src/app/server/package.json /tmp/package.json
    ln -s /usr/src/app/server/node_modules /tmp/node_modules
    cp /anas-patch/oidc-credentials.cjs /tmp/anas-credential-dist/oidc-credentials.cjs
    node -e '\''const fs=require("node:fs"),path=require("node:path"),p=require("/anas-patch/guard-credential-races.cjs");
      const dir="/tmp/anas-credential-dist/services";
      // Only this disposable dist copy is normalized. Revert exact known
      // replacements to test the current working-tree patch even when the
      // read-only local image contains a previous revision of these guards.
      const originals=Object.keys(p.rules).map(name=>{
        let source=fs.readFileSync(path.join(dir,name),"utf8");
        for(const [needle,replacement] of p.rules[name]){
          const applied=p.marker+"\n"+replacement;
          if(source.split(applied).length===2) source=source.replace(applied,needle);
          else if(source.split(needle).length!==2) throw new Error(name+": unknown fixture credential revision");
        }
        if(source.includes(p.marker)) throw new Error(name+": unknown fixture credential marker");
        return [name,source];
      });
      const patched=originals.map(([name,source])=>[name,p.patchSource(name,source)]);
      for(const [name,source] of patched)fs.writeFileSync(path.join(dir,name),source);'\''
    node --test /anas-patch/guard-credential-races.test.cjs
    node /anas-tests/credential-races.cjs
  '
