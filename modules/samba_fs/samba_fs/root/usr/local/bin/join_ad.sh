#!/usr/bin/with-contenv bash

# Preserve an existing machine trust whenever it is still valid. Changing the
# application/Web namespace does not affect the directory inputs or cause a
# leave/join cycle.
join_domain() {
  if net ads testjoin >/dev/null 2>&1; then
    echo "Existing AD membership is valid"
    return 0
  fi

  while :; do
    sleep 5
    # A failed testjoin can mean either an invalid trust or a DC that was not
    # ready yet. Check again after the retry delay so transient DC startup
    # cannot turn a valid existing membership into an unnecessary rejoin.
    if net ads testjoin >/dev/null 2>&1; then
      echo "Existing AD membership is valid"
      return 0
    fi

    printf 'Joining AD %s ...\n' "$SAMBA_DC_DOMAIN"
    printf 'Requesting a Kerberos ticket for %s\n' "$SAMBA_DC_ADMIN_NAME"
    if printf '%s\n' "$SAMBA_DC_ADMIN_PASSWORD" | kinit "$SAMBA_DC_ADMIN_NAME"; then
      echo "kinit succeeded"
      printf 'Running net ads join for %s as %s\n' "$SAMBA_DC_DOMAIN" "$SAMBA_DC_ADMIN_NAME"
      if net ads join -d "$SAMBA_FS_LOG_LEVEL" -U "$SAMBA_DC_ADMIN_NAME%$SAMBA_DC_ADMIN_PASSWORD"; then
        # A zero exit from `net ads join` is not enough to declare the member
        # ready. Verify the machine-account trust against the realm/workgroup
        # rendered in smb.conf before allowing the service to start.
        if net ads testjoin >/dev/null 2>&1; then
          printf 'Joined AD %s and verified the machine trust\n' "$SAMBA_DC_DOMAIN"
          return 0
        fi
        printf 'Join AD %s returned success but trust verification failed, waiting retry...\n' "$SAMBA_DC_DOMAIN" >&2
        sleep 4
        continue
      fi
      printf 'Join AD %s failed, waiting retry...\n' "$SAMBA_DC_DOMAIN"
      sleep 4
    else
      echo "kinit failed, waiting retry..."
      sleep 4
    fi
  done
}
