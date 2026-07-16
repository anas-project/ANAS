#!/usr/bin/with-contenv bash
set -euo pipefail

sleep 10

wait_for_group() {
  local group=$1
  for _ in $(seq 1 30); do
    if getent group "$group" >/dev/null; then
      return 0
    fi
    sleep 2
  done
  echo "AD group is not resolvable: $group" >&2
  return 1
}

wait_for_group "$SAMBA_DC_FS_ADMIN_GROUP_NAME"
wait_for_group "$SAMBA_DC_FS_SHARE_RW_GROUP_NAME"

echo "Fixing /$USERDATA_NAME/$SHARE_DIR_NAME..."
share_path="/$USERDATA_NAME/$SHARE_DIR_NAME"
home_path="/$USERDATA_NAME/Home"
guest_acl_state_file="/$USERDATA_NAME/.anas-share-guest-acl-state"

chown root:"$SAMBA_DC_FS_SHARE_RW_GROUP_NAME" "$share_path"
chmod 2770 "$share_path"
setfacl -m "g:Domain Users:$SAMBA_FS_SHARE_DOMAIN_USERS_ACL,g:$SAMBA_DC_FS_ADMIN_GROUP_NAME:rwx,g:$SAMBA_DC_FS_SHARE_RW_GROUP_NAME:rwx,m:rwx,o:---" "$share_path"
setfacl -d -m "g:Domain Users:$SAMBA_FS_SHARE_DOMAIN_USERS_ACL,g:$SAMBA_DC_FS_ADMIN_GROUP_NAME:rwx,g:$SAMBA_DC_FS_SHARE_RW_GROUP_NAME:rwx,m:rwx,o:---" "$share_path"

case "$SHARE_GUEST_READ_ONLY" in
  Yes|No) ;;
  *)
    echo "Invalid SHARE_GUEST_READ_ONLY value: $SHARE_GUEST_READ_ONLY (expected Yes or No)" >&2
    exit 1
    ;;
esac

previous_guest_acl_state="unknown"
if [ -f "$guest_acl_state_file" ]; then
  previous_guest_acl_state=$(tr -d '\r\n' < "$guest_acl_state_file")
fi

if [ "$SHARE_GUEST_READ_ONLY" == "Yes" ]; then
  # Keep the share root correct on every start without walking the tree.
  setfacl -m "u:nobody:r-x" "$share_path"
  setfacl -d -m "u:nobody:r-x" "$share_path"

  if [ "$previous_guest_acl_state" != "Yes" ]; then
    echo "Guest ACL state changed $previous_guest_acl_state -> Yes; applying read-only ACLs recursively"
    setfacl -R -m "u:nobody:r-X" "$share_path"
    find "$share_path" -type d -exec setfacl -d -m "u:nobody:r-x" {} +
  else
    echo "Guest read-only ACL state is unchanged; skip recursive scan"
  fi
else
  # Removing access at the root blocks guest immediately. A recursive cleanup
  # is only needed when guest access was enabled previously.
  setfacl -x "u:nobody" "$share_path" 2>/dev/null || true
  setfacl -d -x "u:nobody" "$share_path" 2>/dev/null || true

  if [ "$previous_guest_acl_state" == "Yes" ]; then
    echo "Guest ACL state changed Yes -> No; removing guest ACLs recursively"
    setfacl -R -x "u:nobody" "$share_path" 2>/dev/null || true
    find "$share_path" -type d -exec setfacl -d -x "u:nobody" {} + 2>/dev/null || true
  else
    echo "Guest read-only ACL state is disabled; skip recursive scan"
  fi
fi

guest_acl_state_tmp="${guest_acl_state_file}.tmp"
printf '%s\n' "$SHARE_GUEST_READ_ONLY" > "$guest_acl_state_tmp"
chmod 0600 "$guest_acl_state_tmp"
mv -f "$guest_acl_state_tmp" "$guest_acl_state_file"

chown root:"Domain Users" "$home_path"
chmod 0711 "$home_path"
