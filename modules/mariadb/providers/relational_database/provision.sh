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

client() {
  mariadb --protocol=TCP --host="$MARIADB_HOST" --port="$MARIADB_PORT" \
    --user="$MARIADB_USERNAME" --password="$MARIADB_PASSWORD" "$@"
}

case "$operation" in
  ensure)
    : "${ANAS_RESOURCE_PASSWORD:?missing database password}"
    case "$ANAS_RESOURCE_PASSWORD" in
      *[!A-Za-z0-9]*) echo "anas: generated database password has unsafe characters" >&2; exit 2 ;;
    esac
    tries=0
    until client --execute='SELECT 1' >/dev/null 2>&1; do
      tries=$((tries + 1))
      if [ "$tries" -ge 120 ]; then
        echo "anas: MariaDB did not become ready within 120 seconds" >&2
        exit 1
      fi
      sleep 1
    done
    client <<SQL
CREATE DATABASE IF NOT EXISTS \`$ANAS_RESOURCE_DATABASE\`
  CHARACTER SET utf8mb4 COLLATE utf8mb4_general_ci;
CREATE USER IF NOT EXISTS '$ANAS_RESOURCE_USERNAME'@'%' IDENTIFIED BY '$ANAS_RESOURCE_PASSWORD';
ALTER USER '$ANAS_RESOURCE_USERNAME'@'%' IDENTIFIED BY '$ANAS_RESOURCE_PASSWORD';
GRANT ALL PRIVILEGES ON \`$ANAS_RESOURCE_DATABASE\`.* TO '$ANAS_RESOURCE_USERNAME'@'%';
SQL
    ;;
  inspect)
    result="$(client --batch --skip-column-names --execute="SELECT CASE WHEN EXISTS (SELECT 1 FROM information_schema.schemata WHERE schema_name='$ANAS_RESOURCE_DATABASE') AND EXISTS (SELECT 1 FROM mysql.user WHERE User='$ANAS_RESOURCE_USERNAME' AND Host='%') THEN 1 ELSE 0 END;")"
    if [ "$result" = "1" ]; then
      echo ready
    else
      echo missing
    fi
    ;;
  *)
    echo "anas: unsupported relational_database operation: $operation" >&2
    exit 2
    ;;
esac
