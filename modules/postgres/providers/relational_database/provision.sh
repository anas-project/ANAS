#!/bin/sh
set -eu

operation="${1:-}"
: "${ANAS_RESOURCE_DATABASE:?missing database name}"
: "${ANAS_RESOURCE_USERNAME:?missing database username}"
: "${ANAS_RESOURCE_PASSWORD:?missing database password}"
for identifier in "$ANAS_RESOURCE_DATABASE" "$ANAS_RESOURCE_USERNAME"; do
    case "$identifier" in [a-z]*) ;; *) echo 'anas: invalid relational database identifier' >&2; exit 2 ;; esac
    case "$identifier" in *[!a-z0-9_]*|'') echo 'anas: invalid relational database identifier' >&2; exit 2 ;; esac
    [ "${#identifier}" -le 63 ] || { echo 'anas: database identifier is too long' >&2; exit 2; }
done
case "$operation" in ensure|inspect) ;; *) echo 'anas: unsupported relational_database operation' >&2; exit 2 ;; esac
export PGCONNECT_TIMEOUT=5 PGPASSFILE=/dev/null
. /usr/local/bin/anas-postgres-extensions
# Reject the entire request before any connection or write.
names="$(extension_names)"
deadline=$(( $(date +%s) + 120 ))
until admin_sql --dbname postgres -c 'SELECT 1' >/dev/null 2>&1; do
    [ "$operation" = ensure ] || { echo 'anas: PostgreSQL inspect connection failed' >&2; exit 1; }
    [ "$(date +%s)" -lt "$deadline" ] || { echo 'anas: PostgreSQL did not become ready within 120 seconds' >&2; exit 1; }
    sleep 1
done
check_instance
check_available "$names"
role_state="$(admin_sql --dbname postgres --set=resource_username="$ANAS_RESOURCE_USERNAME" <<'SQL'
SELECT CASE WHEN rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls
  OR EXISTS (SELECT 1 FROM pg_auth_members WHERE member = pg_roles.oid)
THEN 'privileged' ELSE 'ordinary' END
FROM pg_roles WHERE rolname = :'resource_username';
SQL
)"
[ "$role_state" != privileged ] || { echo 'anas: refusing to use a privileged application role' >&2; exit 1; }
database_exists="$(admin_sql --dbname postgres --set=resource_database="$ANAS_RESOURCE_DATABASE" <<'SQL'
SELECT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'resource_database');
SQL
)"
if [ "$database_exists" = t ]; then
    allow_missing=false
    [ "$operation" != ensure ] || allow_missing=true
    check_installed "$ANAS_RESOURCE_DATABASE" "$names" "$allow_missing"
elif [ "$operation" = inspect ]; then
    printf '{"exists":false,"ready":false}\n'
    exit 0
fi

if [ "$operation" = ensure ]; then
    admin_sql --dbname postgres --set=resource_database="$ANAS_RESOURCE_DATABASE" \
        --set=resource_username="$ANAS_RESOURCE_USERNAME" --set=resource_password="$ANAS_RESOURCE_PASSWORD" >/dev/null <<'SQL'
SET log_statement = 'none';
SET log_min_error_statement = 'panic';
SET password_encryption = 'scram-sha-256';
SELECT format('CREATE ROLE %I LOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD %L', :'resource_username', :'resource_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'resource_username') \gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L', :'resource_username', :'resource_password') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'resource_database', :'resource_username')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'resource_database') \gexec
SELECT format('ALTER DATABASE %I OWNER TO %I', :'resource_database', :'resource_username') \gexec
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'resource_database') \gexec
SELECT format('GRANT CONNECT, TEMPORARY ON DATABASE %I TO %I', :'resource_database', :'resource_username') \gexec
SELECT format('COMMENT ON DATABASE %I IS %L', :'resource_database', 'ANAS relational_database resource') \gexec
SQL
    admin_sql --dbname "$ANAS_RESOURCE_DATABASE" --set=resource_username="$ANAS_RESOURCE_USERNAME" >/dev/null <<'SQL'
SELECT format('GRANT USAGE, CREATE ON SCHEMA public TO %I', :'resource_username') \gexec
SQL
    # earthdistance is installed only after its provider-managed cube dependency.
    for extension in $names; do
        expected="$(extension_version "$extension")"
        admin_sql --dbname "$ANAS_RESOURCE_DATABASE" --set=extension="$extension" --set=version="$expected" >/dev/null <<'SQL'
SELECT format('CREATE EXTENSION IF NOT EXISTS %I WITH SCHEMA public VERSION %L', :'extension', :'version') \gexec
SQL
    done
fi

check_installed "$ANAS_RESOURCE_DATABASE" "$names" false
# Readiness uses the actual consumer credentials and the same TCP endpoint.
application_state="$(PGUSER="$ANAS_RESOURCE_USERNAME" PGPASSWORD="$ANAS_RESOURCE_PASSWORD" psql -X --set=ON_ERROR_STOP=1 \
    --dbname "$ANAS_RESOURCE_DATABASE" --tuples-only --no-align --set=resource_username="$ANAS_RESOURCE_USERNAME" <<'SQL'
SELECT current_user = :'resource_username' AND NOT (rolsuper OR rolcreaterole OR rolcreatedb OR rolreplication OR rolbypassrls)
  AND NOT EXISTS (SELECT 1 FROM pg_auth_members WHERE member = pg_roles.oid)
FROM pg_roles WHERE rolname = current_user;
SQL
)"
[ "$application_state" = t ] || { echo 'anas: actual application connection or role verification failed' >&2; exit 1; }
for extension in $names; do
    expected="$(extension_version "$extension")"
    actual="$(PGUSER="$ANAS_RESOURCE_USERNAME" PGPASSWORD="$ANAS_RESOURCE_PASSWORD" psql -X --set=ON_ERROR_STOP=1 \
        --dbname "$ANAS_RESOURCE_DATABASE" --tuples-only --no-align --set=extension="$extension" <<'SQL'
SELECT extversion FROM pg_extension WHERE extname = :'extension';
SQL
)"
    [ "$actual" = "$expected" ] || { echo 'anas: extension not ready through application connection' >&2; exit 1; }
    case "$extension" in
        vector) PGUSER="$ANAS_RESOURCE_USERNAME" PGPASSWORD="$ANAS_RESOURCE_PASSWORD" psql -X --set=ON_ERROR_STOP=1 --dbname "$ANAS_RESOURCE_DATABASE" -c "SELECT '[1,2,3]'::vector <-> '[1,2,4]'::vector" >/dev/null ;;
        earthdistance) PGUSER="$ANAS_RESOURCE_USERNAME" PGPASSWORD="$ANAS_RESOURCE_PASSWORD" psql -X --set=ON_ERROR_STOP=1 --dbname "$ANAS_RESOURCE_DATABASE" -c 'SELECT earth_distance(ll_to_earth(0, 0), ll_to_earth(1, 1))' >/dev/null ;;
    esac
done
if [ "$operation" = inspect ]; then printf '{"exists":true,"ready":true}\n'; fi
