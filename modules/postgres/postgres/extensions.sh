#!/bin/sh
set -eu

# Shared fixed-version rules for provision and the provider after_start barrier.
# This file is not an extension catalog protocol and accepts no arbitrary SQL.
extension_version() {
    case "$1" in
        vector) echo 0.8.2 ;;
        cube) echo 1.5 ;;
        earthdistance) echo 1.2 ;;
        *) echo "anas: unsupported PostgreSQL extension: $1" >&2; return 2 ;;
    esac
}

extension_names() {
    requested="${ANAS_RESOURCE_POSTGRES_EXTENSIONS:-}"
    case "$requested" in ,*|*,|*,,*) echo 'anas: invalid extension list' >&2; return 2 ;; esac
    result=''
    old_ifs="$IFS"; IFS=,
    for name in $requested; do
        extension_version "$name" >/dev/null || return
        case " $result " in *" $name "*) echo 'anas: duplicate extension' >&2; return 2 ;; esac
        result="$result $name"
    done
    IFS="$old_ifs"
    case " $result " in
        *' earthdistance '*)
            ordered=cube
            for name in $result; do [ "$name" = cube ] || ordered="$ordered $name"; done
            result="$ordered"
            ;;
    esac
    printf '%s\n' "$result"
}

admin_sql() { psql -X --set=ON_ERROR_STOP=1 --tuples-only --no-align "$@"; }

check_instance() {
    state="$(admin_sql --dbname postgres <<'SQL'
SELECT current_setting('server_version_num')::integer = 180004
  AND current_setting('password_encryption') = 'scram-sha-256'
  AND current_setting('shared_preload_libraries') = ''
  AND (SELECT count(*) FROM pg_hba_file_rules) >= 3
  AND NOT EXISTS (SELECT 1 FROM pg_hba_file_rules WHERE error IS NOT NULL OR auth_method <> 'scram-sha-256');
SQL
)"
    [ "$state" = t ] || { echo 'anas: PostgreSQL engine, SCRAM HBA or preload does not match the fixed release' >&2; return 1; }
}

check_available() {
    for extension in $1; do
        expected="$(extension_version "$extension")"
        actual="$(admin_sql --dbname postgres --set=extension="$extension" <<'SQL'
SELECT default_version FROM pg_available_extensions WHERE name = :'extension';
SQL
)"
        [ "$actual" = "$expected" ] || { echo "anas: $extension binary/default version mismatch (expected $expected)" >&2; return 1; }
    done
}

check_installed() {
    database="$1"; names="$2"; allow_missing="$3"
    for extension in $names; do
        expected="$(extension_version "$extension")"
        actual="$(admin_sql --dbname "$database" --set=extension="$extension" <<'SQL'
SELECT extversion FROM pg_extension WHERE extname = :'extension';
SQL
)"
        if [ -z "$actual" ] && [ "$allow_missing" = true ]; then continue; fi
        [ "$actual" = "$expected" ] || { echo "anas: $database/$extension version mismatch; PostgreSQL Module maintenance is required" >&2; return 1; }
    done
}

provider_maintenance() {
    mode="$1"
    case "$mode" in inspect|upgrade) ;; *) echo 'anas: unsupported extension maintenance mode' >&2; return 2 ;; esac
    export PGHOST=127.0.0.1 PGPORT=5432 PGUSER="$POSTGRES_USER" PGPASSWORD="$POSTGRES_PASSWORD" PGCONNECT_TIMEOUT=5
    deadline=$(( $(date +%s) + 120 ))
    until admin_sql --dbname postgres -c 'SELECT 1' >/dev/null 2>&1; do
        [ "$(date +%s)" -lt "$deadline" ] || { echo 'anas: PostgreSQL lifecycle TCP authentication readiness timeout' >&2; return 1; }
        sleep 1
    done
    check_instance
    check_available 'vector cube earthdistance'
    databases="$(admin_sql --dbname postgres <<'SQL'
SELECT d.datname FROM pg_database d JOIN pg_roles r ON r.oid = d.datdba
WHERE NOT d.datistemplate AND NOT r.rolsuper
  AND shobj_description(d.oid, 'pg_database') = 'ANAS relational_database resource'
ORDER BY d.datname;
SQL
)"
    # Preflight every database before modifying any. Only one audited pgvector
    # path is supported; unrelated/unknown versions require an ANAS restore.
    for database in $databases; do
        installed="$(admin_sql --dbname "$database" -c "SELECT extname || ':' || extversion FROM pg_extension WHERE extname IN ('vector','cube','earthdistance') ORDER BY extname")"
        for pair in $installed; do
            case "$pair" in
                vector:0.8.2|cube:1.5|earthdistance:1.2) ;;
                vector:0.8.1) [ "$mode" = upgrade ] || { echo 'anas: vector 0.8.1 requires controlled provider maintenance' >&2; return 1; } ;;
                *) echo "anas: unsupported installed extension state in $database: $pair" >&2; return 1 ;;
            esac
        done
    done
    if [ "$mode" = upgrade ]; then
        for database in $databases; do
            admin_sql --dbname "$database" >/dev/null <<'SQL'
SELECT 'ALTER EXTENSION vector UPDATE TO ''0.8.2'''
WHERE EXISTS (SELECT 1 FROM pg_extension WHERE extname = 'vector' AND extversion = '0.8.1') \gexec
SQL
        done
    fi
    for database in $databases; do
        installed="$(admin_sql --dbname "$database" -c "SELECT extname FROM pg_extension WHERE extname IN ('vector','cube','earthdistance') ORDER BY extname")"
        check_installed "$database" "$installed" false
    done
}

# Sourced by provision; executed only by the PostgreSQL lifecycle barrier.
if [ "${0##*/}" = anas-postgres-extensions ]; then provider_maintenance "${1:-inspect}"; fi
