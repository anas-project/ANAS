#!/usr/bin/with-contenv bash

. /usr/local/bin/join_ad.sh

if [ -z "$SAMBA_FS_INTERFACES" ]; then # bind interfaces empty, use default route
  export SAMBA_FS_INTERFACES=$(echo $(/sbin/ip route | awk '/default/ { print $5 }'))
fi

# Samba FS
if [ "$SAMBA_FS_RECYCLE_ENABLE" == "Yes" ]; then
  export GLOBAL_RECYCLE="recycle"
else
  export GLOBAL_RECYCLE=""
fi

smb_variables='${GLOBAL_RECYCLE} ${SAMBA_DC_REALM} ${SAMBA_DC_WORKGROUP} ${SAMBA_FS_ADMIN_USERS} ${SAMBA_FS_INTERFACES} ${SAMBA_FS_LOG_LEVEL} ${SAMBA_FS_NETBIOS_NAME} ${SAMBA_FS_SHARE_VALID_USERS} ${SAMBA_FS_SHARE_WRITE_LIST} ${SAMBA_FS_USE_DEFAULT_DOMAIN} ${SHARE_DIR_NAME} ${SHARE_GUEST_READ_ONLY}'
for name in $(printf '%s\n' "$smb_variables" | grep -o '[A-Z][A-Z0-9_]*'); do
  eval 'present=${'"$name"'+x}'
  if [[ "$present" != x ]]; then
    echo "missing required environment variable: $name" >&2
    exit 1
  fi
done
envsubst "$smb_variables" < /etc/samba/smb.conf.envsubst > /etc/samba/smb.conf.tmp
mv /etc/samba/smb.conf.tmp /etc/samba/smb.conf

smbusers_variables='${SAMBA_DC_ADMIN_NAME} ${SAMBA_DC_WORKGROUP}'
for name in SAMBA_DC_ADMIN_NAME SAMBA_DC_WORKGROUP; do
  eval 'present=${'"$name"'+x}'
  if [[ "$present" != x ]]; then
    echo "missing required environment variable: $name" >&2
    exit 1
  fi
done
envsubst "$smbusers_variables" < /etc/samba/smbusers.envsubst > /etc/samba/smbusers.tmp
mv /etc/samba/smbusers.tmp /etc/samba/smbusers

krb5_variables='${SAMBA_DC_DC_DOMAIN} ${SAMBA_DC_DOMAIN} ${SAMBA_DC_REALM}'
for name in SAMBA_DC_DC_DOMAIN SAMBA_DC_DNS_SEARCH SAMBA_DC_DNS_SERVER SAMBA_DC_DOMAIN SAMBA_DC_REALM; do
  eval 'present=${'"$name"'+x}'
  if [[ "$present" != x ]]; then
    echo "missing required environment variable: $name" >&2
    exit 1
  fi
done
envsubst "$krb5_variables" < /etc/krb5.conf.envsubst > /etc/krb5.conf.tmp
mv /etc/krb5.conf.tmp /etc/krb5.conf

if grep -n '\${[A-Z][A-Z0-9_]*}' /etc/samba/smb.conf /etc/samba/smbusers /etc/krb5.conf; then
  echo "unresolved variables in Samba client configuration" >&2
  exit 1
fi
testparm -s /etc/samba/smb.conf >/dev/null

# rm -f /var/cache/samba/*.tdb
# rm -f /var/cache/samba/*.ldb
# rm -f /var/lib/samba/*.tdb
# rm -f /var/lib/samba/*.ldb
# rm -f /var/cache/samba/*.tdb
# rm -f /var/cache/samba/*.ldb
# rm -f /var/lib/samba/private/*.tdb
# rm -f /var/lib/samba/private/*.ldb

chmod +x /usr/local/bin/samba_create_user_dir.sh
chmod +x /usr/local/bin/join_ad.sh
chmod +x /usr/local/bin/fix_perm.sh

echo "Set /etc/hosts"
sed "/$SAMBA_FS_HOSTNAME/d" /etc/hosts > ~/hosts
cp ~/hosts /etc/hosts 
export HOST_IP=$(ip addr show | grep -E '^\s*inet' | grep -m1 global | awk '{ print $2 }' | sed 's|/.*||')
fs_hostname=`echo $SAMBA_FS_HOSTNAME | tr '[:upper:]' '[:lower:]'`
echo "$HOST_IP  $fs_hostname.$SAMBA_DC_DOMAIN  $fs_hostname" >> /etc/hosts

# dns
echo "Setting DNS resolv.conf"
: > /etc/resolv.conf
echo "nameserver $SAMBA_DC_DNS_SERVER" >> /etc/resolv.conf
echo "search $SAMBA_DC_DNS_SEARCH" >> /etc/resolv.conf

# /var/lib/samba is a bind mount that starts empty, so the directories the
# image ships are not there. net ads join needs private/ for its messaging
# socket and fails with "Unable to initialize messaging context!" without it.
mkdir -p /var/lib/samba/private /var/log/samba

join_domain

# Unconditionally, not only after a join. join_domain returns early whenever
# `net ads testjoin` still passes, which it does after the container's address
# changes -- the machine account and its keytab are untouched by a new address.
# The AD DNS A record is not: nothing else updates it, so the directory would
# keep pointing clients at wherever this server used to be. Registering the
# current address every start is idempotent and costs one LDAP update.
echo "Registering $SAMBA_FS_HOSTNAME in AD DNS"
if ! net ads dns register -P; then
  echo "AD DNS registration failed; refusing to start with an unverified member address" >&2
  exit 1
fi

echo "Create share"
mkdir -p /userdata/$SHARE_DIR_NAME
mkdir -p /userdata/Home
