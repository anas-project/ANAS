#!/bin/sh
set -eu

operation="${1:-}"
: "${ANAS_RESOURCE_DATABASE:?missing database name}"
: "${ANAS_RESOURCE_USERNAME:?missing database username}"

for identifier in "$ANAS_RESOURCE_DATABASE" "$ANAS_RESOURCE_USERNAME"; do
  case "$identifier" in
    [a-z]*) ;;
    *) echo "anas: invalid relational database identifier" >&2; exit 2 ;;
  esac
  case "$identifier" in
    *[!a-z0-9_]*) echo "anas: invalid relational database identifier" >&2; exit 2 ;;
  esac
done

export PGCONNECT_TIMEOUT=5

case "$operation" in
  ensure)
    : "${ANAS_RESOURCE_PASSWORD:?missing database password}"
    tries=0
    until pg_isready --dbname postgres >/dev/null 2>&1; do
      tries=$((tries + 1))
      if [ "$tries" -ge 120 ]; then
        echo "anas: PostgreSQL did not become ready within 120 seconds" >&2
        exit 1
      fi
      sleep 1
    done
    psql --dbname postgres --set=ON_ERROR_STOP=1 \
      --set=resource_database="$ANAS_RESOURCE_DATABASE" \
      --set=resource_username="$ANAS_RESOURCE_USERNAME" \
      --set=resource_password="$ANAS_RESOURCE_PASSWORD" <<'SQL'
SELECT format('CREATE ROLE %I LOGIN PASSWORD %L', :'resource_username', :'resource_password')
WHERE NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'resource_username') \gexec
SELECT format('ALTER ROLE %I LOGIN PASSWORD %L', :'resource_username', :'resource_password') \gexec
SELECT format('CREATE DATABASE %I OWNER %I', :'resource_database', :'resource_username')
WHERE NOT EXISTS (SELECT 1 FROM pg_database WHERE datname = :'resource_database') \gexec
SELECT format('ALTER DATABASE %I OWNER TO %I', :'resource_database', :'resource_username') \gexec
SELECT format('REVOKE ALL ON DATABASE %I FROM PUBLIC', :'resource_database') \gexec
SELECT format('GRANT CONNECT, TEMPORARY ON DATABASE %I TO %I', :'resource_database', :'resource_username') \gexec
SQL
    psql --dbname "$ANAS_RESOURCE_DATABASE" --set=ON_ERROR_STOP=1 \
      --set=resource_username="$ANAS_RESOURCE_USERNAME" <<'SQL'
SELECT format('GRANT USAGE, CREATE ON SCHEMA public TO %I', :'resource_username') \gexec
SQL
    ;;
  inspect)
    psql --dbname postgres --set=ON_ERROR_STOP=1 --tuples-only --no-align \
      --set=resource_database="$ANAS_RESOURCE_DATABASE" \
      --set=resource_username="$ANAS_RESOURCE_USERNAME" <<'SQL'
SELECT CASE WHEN
  EXISTS (SELECT 1 FROM pg_database WHERE datname = :'resource_database') AND
  EXISTS (SELECT 1 FROM pg_roles WHERE rolname = :'resource_username')
THEN 'ready' ELSE 'missing' END;
SQL
    ;;
  *)
    echo "anas: unsupported relational_database operation: $operation" >&2
    exit 2
    ;;
esac
