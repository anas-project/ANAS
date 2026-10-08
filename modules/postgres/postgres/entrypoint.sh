#!/usr/bin/env bash
set -Eeo pipefail

# Reuse the fixed upstream image's functions, including PG18's directory rules.
source /usr/local/bin/docker-entrypoint.sh
if [[ "${1:-}" == -* ]]; then set -- postgres "$@"; fi
if [[ "${1:-}" != postgres ]] || _pg_want_help "$@"; then exec "$@"; fi

docker_setup_env
docker_create_db_directories
if [[ "$(id -u)" == 0 ]]; then exec gosu postgres "$0" "$@"; fi
: "${POSTGRES_PASSWORD:?ANAS PostgreSQL requires a managed administrator password}"
export POSTGRES_HOST_AUTH_METHOD=scram-sha-256
export POSTGRES_INITDB_ARGS='--auth-local=scram-sha-256 --auth-host=scram-sha-256'
if [[ -z "$DATABASE_ALREADY_EXISTS" ]]; then
    docker_verify_minimum_env
    docker_error_old_databases
    docker_init_database_dir
    export PGPASSWORD="$POSTGRES_PASSWORD"
    docker_temp_server_start "$@" -c password_encryption=scram-sha-256
    docker_setup_db
    docker_process_init_files /docker-entrypoint-initdb.d/*
    docker_temp_server_stop
fi

# Existing trust data directories are hardened before any TCP listener exists.
# The temporary server has a private Unix socket and accepts no TCP traffic.
private_dir="$(mktemp -d /tmp/anas-postgres-auth.XXXXXX)"
chmod 700 "$private_dir"
printf 'local all all trust\n' > "$private_dir/pg_hba.conf"
cleanup() {
    docker_temp_server_stop >/dev/null 2>&1 || true
    rm -rf "$private_dir"
}
trap cleanup EXIT
docker_temp_server_start "$@" -c "hba_file=$private_dir/pg_hba.conf" -c "unix_socket_directories=$private_dir" -c password_encryption=scram-sha-256
PGHOST="$private_dir" PGUSER="$POSTGRES_USER" psql -X --dbname postgres --set=ON_ERROR_STOP=1 \
    --set=admin_user="$POSTGRES_USER" --set=admin_password="$POSTGRES_PASSWORD" >/dev/null <<'SQL'
SET log_statement = 'none';
SET log_min_error_statement = 'panic';
SELECT format('ALTER ROLE %I PASSWORD %L', :'admin_user', :'admin_password') \gexec
SQL
cat > "$PGDATA/pg_hba.conf.anas" <<'HBA'
# Managed by ANAS PostgreSQL. Password authentication also applies to sockets.
local all all scram-sha-256
host all all 0.0.0.0/0 scram-sha-256
host all all ::/0 scram-sha-256
HBA
chmod 600 "$PGDATA/pg_hba.conf.anas"
mv "$PGDATA/pg_hba.conf.anas" "$PGDATA/pg_hba.conf"
docker_temp_server_stop
trap - EXIT
rm -rf "$private_dir"
unset PGPASSWORD
unset "${!POSTGRES_@}"
exec "$@" -c "hba_file=$PGDATA/pg_hba.conf" -c password_encryption=scram-sha-256 -c shared_preload_libraries=
