#!/usr/bin/with-contenv bash

mkdir -p /etc/avahi/services/
if [[ "${SAMBA_FS_HOSTNAME+x}" != x ]]; then
  echo "missing required environment variable: SAMBA_FS_HOSTNAME" >&2
  exit 1
fi
envsubst '${SAMBA_FS_HOSTNAME}' < /root/samba.service.envsubst > /etc/avahi/services/samba.service.tmp
if grep -n '\${[A-Z][A-Z0-9_]*}' /etc/avahi/services/samba.service.tmp; then
  echo "unresolved variables in samba.service" >&2
  exit 1
fi
mv /etc/avahi/services/samba.service.tmp /etc/avahi/services/samba.service
