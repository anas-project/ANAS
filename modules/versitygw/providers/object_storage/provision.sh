#!/bin/sh
set -eu

operation="${1:-}"
: "${ANAS_RESOURCE_BUCKET:?missing object storage bucket}"
: "${ANAS_RESOURCE_ACCESS_KEY_ID:?missing object storage access key id}"

case "$ANAS_RESOURCE_BUCKET" in
  [a-z0-9]*[a-z0-9]) ;;
  *) echo "anas: invalid object storage bucket" >&2; exit 2 ;;
esac
case "$ANAS_RESOURCE_BUCKET" in
  *[!a-z0-9.-]*|*..*|*.-*|*-.*)
    echo "anas: invalid object storage bucket" >&2
    exit 2
    ;;
esac
if [ "${#ANAS_RESOURCE_BUCKET}" -lt 3 ] || [ "${#ANAS_RESOURCE_BUCKET}" -gt 63 ]; then
  echo "anas: invalid object storage bucket length" >&2
  exit 2
fi
case "$ANAS_RESOURCE_ACCESS_KEY_ID" in
  [A-Za-z0-9]*) ;;
  *)
    echo "anas: invalid object storage access key id" >&2
    exit 2
    ;;
esac
case "$ANAS_RESOURCE_ACCESS_KEY_ID" in
  *[!A-Za-z0-9._-]*)
    echo "anas: invalid object storage access key id" >&2
    exit 2
    ;;
esac
if [ "${#ANAS_RESOURCE_ACCESS_KEY_ID}" -lt 3 ] || [ "${#ANAS_RESOURCE_ACCESS_KEY_ID}" -gt 64 ]; then
  echo "anas: invalid object storage access key id length" >&2
  exit 2
fi

list_users() {
  versitygw admin list-users
}

list_buckets() {
  versitygw admin list-buckets
}

user_role() {
  printf '%s\n' "$1" | awk -v access="$ANAS_RESOURCE_ACCESS_KEY_ID" '$1 == access { print $2; exit }'
}

bucket_owner() {
  printf '%s\n' "$1" | awk -v bucket="$ANAS_RESOURCE_BUCKET" '$1 == bucket { print $2; exit }'
}

# A provider starts before its consumers, but the container health transition
# and the next compose run can still race. Bound the wait and keep failed admin
# responses out of ordinary output because they may contain operational detail.
tries=0
until users="$(list_users 2>/dev/null)"; do
  tries=$((tries + 1))
  if [ "$tries" -ge 120 ]; then
    echo "anas: VersityGW admin API did not become ready within 120 seconds" >&2
    exit 1
  fi
  sleep 1
done

case "$operation" in
  ensure)
    : "${ANAS_RESOURCE_SECRET_ACCESS_KEY:?missing object storage secret access key}"
    role="$(user_role "$users")"
    buckets="$(list_buckets)"
    owner="$(bucket_owner "$buckets")"
    if [ -n "$role" ] && [ "$role" != "user" ]; then
      echo "anas: object storage access key already exists with role $role" >&2
      exit 1
    fi
    if [ -n "$owner" ] && [ "$owner" != "$ANAS_RESOURCE_ACCESS_KEY_ID" ]; then
      echo "anas: object storage bucket already belongs to another principal" >&2
      exit 1
    fi

    if [ -z "$role" ]; then
      versitygw admin create-user \
        --access "$ANAS_RESOURCE_ACCESS_KEY_ID" \
        --secret "$ANAS_RESOURCE_SECRET_ACCESS_KEY" \
        --role user >/dev/null
    else
      # ensure is also the reconciliation path for an interrupted credential
      # rotation: provider first, then the already-persisted desired Secret.
      versitygw admin update-user \
        --access "$ANAS_RESOURCE_ACCESS_KEY_ID" \
        --secret "$ANAS_RESOURCE_SECRET_ACCESS_KEY" >/dev/null
    fi

    if [ -z "$owner" ]; then
      versitygw admin create-bucket \
        --owner "$ANAS_RESOURCE_ACCESS_KEY_ID" \
        --bucket "$ANAS_RESOURCE_BUCKET" >/dev/null
    fi
    ;;
  inspect)
    role="$(user_role "$users")"
    buckets="$(list_buckets)"
    owner="$(bucket_owner "$buckets")"
    if [ "$role" = "user" ] && [ "$owner" = "$ANAS_RESOURCE_ACCESS_KEY_ID" ]; then
      echo ready
    else
      echo missing
      exit 1
    fi
    ;;
  rotate_credential)
    : "${ANAS_RESOURCE_SECRET_ACCESS_KEY:?missing object storage secret access key}"
    role="$(user_role "$users")"
    if [ "$role" != "user" ]; then
      echo "anas: object storage principal is missing or has the wrong role" >&2
      exit 1
    fi
    versitygw admin update-user \
      --access "$ANAS_RESOURCE_ACCESS_KEY_ID" \
      --secret "$ANAS_RESOURCE_SECRET_ACCESS_KEY" >/dev/null
    ;;
  *)
    echo "anas: unsupported object_storage operation: $operation" >&2
    exit 2
    ;;
esac
