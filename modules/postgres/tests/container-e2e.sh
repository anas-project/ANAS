#!/usr/bin/env bash
set -euo pipefail

# Isolated Docker integration checks. No existing workspace/deployment is used.
# Usage: bash modules/postgres/tests/container-e2e.sh --local-task-fixture NEW_IMAGE [OLD_VECTOR_IMAGE]
repo_root="$(cd "$(dirname "$0")/../../.." && pwd)"
case "${1:-}" in
    --local-task-fixture) shift ;;
    --isolated-server)
        . "$repo_root/test-env/scripts/server-require-isolated-docker.sh"
        shift
        ;;
    *) echo 'use --local-task-fixture explicitly or the isolated server entry point' >&2; exit 2 ;;
esac
image="${1:?new PostgreSQL module image required}"
old_image="${2:-}"
prefix="anas-pg-e2e-$(date +%s)-$$"
network="$prefix-network"
volume="$prefix-data"
container="$prefix-server"
cleanup() {
    docker rm -f "$container" >/dev/null 2>&1 || true
    docker volume rm "$volume" >/dev/null 2>&1 || true
    docker network rm "$network" >/dev/null 2>&1 || true
}
trap cleanup EXIT
docker network create "$network" >/dev/null
docker volume create "$volume" >/dev/null
start() {
    docker run -d --name "$container" --network "$network" \
        -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=ProviderTest1 \
        -v "$volume:/var/lib/postgresql" "$1" >/dev/null
    for ((attempt=0; attempt<120; attempt++)); do
        if docker exec -e PGPASSWORD=ProviderTest1 "$container" psql -X -h 127.0.0.1 -U postgres -d postgres -c 'SELECT 1' >/dev/null 2>&1; then return; fi
        sleep 1
    done
    docker logs "$container" >&2
    return 1
}
sql() {
    docker exec -e PGPASSWORD=ProviderTest1 "$container" psql -X -h 127.0.0.1 -U postgres -d "${2:-postgres}" -At -v ON_ERROR_STOP=1 -c "$1"
}
provision() {
    docker run --rm --network "$network" --entrypoint /bin/sh \
        -e "PGHOST=$container" -e PGPORT=5432 -e PGUSER=postgres -e PGPASSWORD=ProviderTest1 \
        -e "ANAS_RESOURCE_DATABASE=${3:-photos}" -e "ANAS_RESOURCE_USERNAME=${3:-photos}" \
        -e "ANAS_RESOURCE_PASSWORD=${4:-ApplicationTest1}" -e "ANAS_RESOURCE_POSTGRES_EXTENSIONS=$2" \
        -v "$repo_root/modules/postgres/providers/relational_database:/operations:ro" \
        "$image" /operations/provision.sh "$1"
}
reject() {
    if "$@" > /dev/null 2>&1; then echo 'expected operation to fail' >&2; return 1; fi
}
assert() {
    if [ "$1" != "$2" ]; then echo "fixture assertion failed near line ${BASH_LINENO[0]}" >&2; exit 1; fi
}
application_sql() {
    docker exec -e PGPASSWORD=ApplicationTest1 "$container" psql -X -h 127.0.0.1 -U photos -d photos -At -v ON_ERROR_STOP=1 -c "$1"
}

start "$image"
docker exec "$container" /usr/local/bin/anas-postgres-extensions inspect
provision ensure vector,earthdistance
provision ensure vector,earthdistance
assert "$(provision inspect vector,earthdistance)" '{"exists":true,"ready":true}'
assert "$(sql "SELECT extname || ':' || extversion FROM pg_extension WHERE extname IN ('vector','cube','earthdistance') ORDER BY extname" photos)" $'cube:1.5\nearthdistance:1.2\nvector:0.8.2'
assert "$(application_sql "SELECT '[1,2,3]'::vector <-> '[1,2,4]'::vector")" 1
application_sql 'CREATE TABLE proof (id integer primary key, photo_hash text); INSERT INTO proof VALUES (1, '\''sha256-media-test'\'')' >/dev/null
provision ensure '' otherapp
sql "CREATE TABLE proof (id integer); INSERT INTO proof VALUES (7)" otherapp >/dev/null
reject provision ensure vector,unknown rejected
assert "$(sql "SELECT count(*) FROM pg_database WHERE datname='rejected'")" 0
reject provision ensure vector,vector rejected
reject provision ensure vector,,earthdistance rejected
reject provision inspect vector,earthdistance photos IncorrectTest1
sql 'GRANT pg_read_all_data TO photos' >/dev/null
reject provision inspect vector,earthdistance
reject provision ensure vector,earthdistance
sql 'REVOKE pg_read_all_data FROM photos; GRANT pg_write_all_data TO photos' >/dev/null
reject provision inspect vector,earthdistance
sql 'REVOKE pg_write_all_data FROM photos' >/dev/null
reject docker exec -e PGPASSWORD=WrongTest1 "$container" psql -X -h 127.0.0.1 -U postgres -d postgres -c 'SELECT 1'
reject docker exec -e PGPASSWORD=ApplicationTest1 "$container" psql -X -h 127.0.0.1 -U postgres -d postgres -c 'SELECT 1'
reject docker exec -e PGPASSWORD= -e PGPASSFILE=/dev/null "$container" psql -X -w -h 127.0.0.1 -U photos -d photos -c 'SELECT 1'
reject docker exec -e PGPASSWORD=ApplicationTest1 "$container" psql -X -h 127.0.0.1 -U photos -d otherapp -c 'SELECT 1'
before="$(sql "SELECT oid || ':' || rolpassword FROM pg_authid WHERE rolname='photos'")"
provision inspect vector,earthdistance >/dev/null
assert "$(sql "SELECT oid || ':' || rolpassword FROM pg_authid WHERE rolname='photos'")" "$before"
sql 'DROP EXTENSION cube CASCADE' photos >/dev/null
reject provision inspect vector,earthdistance
provision ensure earthdistance,cube,vector
provision ensure '' photos
assert "$(sql "SELECT count(*) FROM pg_extension WHERE extname IN ('vector','cube','earthdistance')" photos)" 3
docker restart "$container" >/dev/null
docker exec "$container" /usr/local/bin/anas-postgres-extensions inspect
provision inspect vector,earthdistance >/dev/null
assert "$(application_sql 'SELECT photo_hash FROM proof WHERE id=1')" sha256-media-test
assert "$(sql 'SELECT id FROM proof' otherapp)" 7
docker rm -f "$container" >/dev/null
# Bypass the managed startup only inside this fixture to reproduce live preload
# drift against the same SCRAM data. Neither inspect nor ensure may report ready.
docker run -d --name "$container" --network "$network" --user postgres --entrypoint postgres \
    -e POSTGRES_USER=postgres -e POSTGRES_PASSWORD=ProviderTest1 -v "$volume:/var/lib/postgresql" \
    "$image" -c password_encryption=scram-sha-256 -c shared_preload_libraries=pg_stat_statements >/dev/null
for ((attempt=0; attempt<120; attempt++)); do
    if sql 'SELECT 1' >/dev/null 2>&1; then break; fi
    sleep 1
done
reject provision inspect vector,earthdistance
reject provision ensure vector,earthdistance
docker rm -f "$container" >/dev/null
start "$image"
provision inspect vector,earthdistance >/dev/null
echo 'PASS: fresh/repeat/restart, application SQL, extension dependencies, auth/isolation, readonly inspect, retain'

if [[ -n "$old_image" ]]; then
    docker rm -f "$container" >/dev/null
    docker volume rm "$volume" >/dev/null
    docker volume create "$volume" >/dev/null
    # Fixed fixture uses the real 0.8.1 binary/control/SQL and legacy trust HBA.
    start "$old_image"
    # Official initdb can keep its bootstrap administrator SCRAM even when
    # the old server defaults to MD5; deliberately reproduce the older stored
    # administrator credential too, before asserting the actual legacy state.
    sql "ALTER ROLE postgres PASSWORD 'ProviderTest1'; CREATE ROLE photos LOGIN PASSWORD 'ApplicationTest1'; CREATE ROLE otherapp LOGIN PASSWORD 'OtherTest1'" >/dev/null
    sql 'CREATE DATABASE photos OWNER photos' >/dev/null
    sql 'CREATE DATABASE otherapp OWNER otherapp' >/dev/null
    sql "COMMENT ON DATABASE photos IS 'ANAS relational_database resource'; COMMENT ON DATABASE otherapp IS 'ANAS relational_database resource'" >/dev/null
    # A managed SCRAM 0.8.1 image is not evidence for legacy authentication
    # repair. Require the deliberately isolated old fixture's actual state.
    assert "$(sql "SELECT bool_or(auth_method='trust') FROM pg_hba_file_rules")" t
    assert "$(sql "SELECT bool_and(rolpassword LIKE 'md5%') FROM pg_authid WHERE rolname IN ('postgres','photos','otherapp')")" t
    sql "CREATE EXTENSION vector; CREATE TABLE vectors (id integer, embedding vector(3)); INSERT INTO vectors VALUES (1, '[1,2,3]'); CREATE INDEX ON vectors USING hnsw (embedding vector_l2_ops); ALTER TABLE vectors OWNER TO photos;" photos >/dev/null
    sql 'CREATE EXTENSION vector; CREATE TABLE proof (id integer); INSERT INTO proof VALUES (9); ALTER TABLE proof OWNER TO otherapp' otherapp >/dev/null
    assert "$(sql "SELECT extversion FROM pg_extension WHERE extname='vector'" photos)" 0.8.1
    docker rm -f "$container" >/dev/null
    start "$image"
    reject docker exec "$container" /usr/local/bin/anas-postgres-extensions inspect
    reject provision ensure vector
    # Simulate unsupported catalog drift in the isolated fixture. Preflight
    # must fail before the first database can be upgraded.
    sql "UPDATE pg_extension SET extversion='0.8.0' WHERE extname='vector'" photos >/dev/null
    reject docker exec "$container" /usr/local/bin/anas-postgres-extensions upgrade
    assert "$(sql "SELECT extversion FROM pg_extension WHERE extname='vector'" otherapp)" 0.8.1
    sql "UPDATE pg_extension SET extversion='0.8.1' WHERE extname='vector'" photos >/dev/null
    # The same SQL operation as the first successful step of a cross-database
    # maintenance run; next invocation must retry from actual partial state.
    sql "ALTER EXTENSION vector UPDATE TO '0.8.2'" otherapp >/dev/null
    docker exec "$container" /usr/local/bin/anas-postgres-extensions upgrade
    docker exec "$container" /usr/local/bin/anas-postgres-extensions upgrade
    provision ensure vector
    provision ensure '' otherapp OtherTest1
    assert "$(application_sql "SELECT embedding <-> '[1,2,4]'::vector FROM vectors WHERE id=1")" 1
    index_plan="$(application_sql "SET enable_seqscan=off; EXPLAIN SELECT embedding <-> '[1,2,4]'::vector FROM vectors ORDER BY embedding <-> '[1,2,4]'::vector LIMIT 1")"
    case "$index_plan" in *'Index Scan using vectors_embedding_idx'*) ;; *) echo 'old HNSW index is not queryable after upgrade' >&2; exit 1 ;; esac
    assert "$(docker exec -e PGPASSWORD=ApplicationTest1 -e PGOPTIONS='-c enable_seqscan=off' "$container" psql -X -h 127.0.0.1 -U photos -d photos -At -v ON_ERROR_STOP=1 -c "SELECT embedding <-> '[1,2,4]'::vector FROM vectors ORDER BY embedding <-> '[1,2,4]'::vector LIMIT 1")" 1
    assert "$(sql 'SELECT id FROM proof' otherapp)" 9
    reject docker exec -e PGPASSWORD=WrongTest1 "$container" psql -X -h 127.0.0.1 -U postgres -d postgres -c 'SELECT 1'
    assert "$(sql "SELECT bool_and(auth_method='scram-sha-256') FROM pg_hba_file_rules")" t
    assert "$(sql "SELECT bool_and(rolpassword LIKE 'SCRAM-SHA-256%') FROM pg_authid WHERE rolname IN ('postgres','photos','otherapp')")" t
    echo 'PASS: existing trust migration, real pgvector 0.8.1→0.8.2, retry, ordinary role/index and shared consumer data'
fi
