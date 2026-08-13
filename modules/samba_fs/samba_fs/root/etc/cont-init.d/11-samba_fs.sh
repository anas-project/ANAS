#!/usr/bin/with-contenv bash

join_domain() {
  if net ads testjoin >/dev/null 2>&1; then
    echo "Existing AD membership is valid"
    return
  fi
  while :
  do
    echo "Joining AD $SAMBA_DC_DOMAIN ..."
    sleep 5
    echo "echo *** | kinit $SAMBA_DC_ADMIN_NAME"
    echo $SAMBA_DC_ADMIN_PASSWORD | kinit $SAMBA_DC_ADMIN_NAME
    result=$?
    if [ $result == 0 ]; then
      echo "kinit succeeded"
      echo net ads join -d $SAMBA_FS_LOG_LEVEL -U "$SAMBA_DC_ADMIN_NAME%*****"
      net ads join -d $SAMBA_FS_LOG_LEVEL -U "$SAMBA_DC_ADMIN_NAME%$SAMBA_DC_ADMIN_PASSWORD"

      # samba-tool domain join $SAMBA_DC_DOMAIN MEMBER -U $SAMBA_DC_ADMIN_NAME --password=$SAMBA_DC_ADMIN_PASSWORD
      result=$?
      if [ $result == 0 ]; then
        return
      fi
      echo "Join AD $SAMBA_DC_DOMAIN failed, waiting retry..."
      sleep 4
    else
      echo "kinit failed, waiting retry..."
      sleep 4
    fi
  done
}

# join_domain() {

#   while :
#   do
#     echo "Join AD $SAMBA_DC_DOMAIN"

#     echo "echo *** | kinit $SAMBA_DC_ADMIN_NAME"
#     echo $SAMBA_DC_ADMIN_PASSWORD | kinit $SAMBA_DC_ADMIN_NAME
#     result=$?
#     if [ $result == 0 ]; then
#       while :
#       do
#         echo "Join AD $SAMBA_DC_DOMAIN"
#         echo samba-tool domain join $SAMBA_DC_DOMAIN MEMBER -U "\"$SAMBA_DC_ADMIN_NAME%******\""

#         samba-tool domain join $SAMBA_DC_DOMAIN MEMBER -U $SAMBA_DC_ADMIN_NAME --password=$SAMBA_DC_ADMIN_PASSWORD
#         result=$?
#         if [ $result == 0 ]; then
#           return
#         fi
#         echo "Join AD $SAMBA_DC_DOMAIN failed, waiting retry..."
#         sleep 4
#       done
#     fi
#     echo "kinit failed, waiting retry..."
#     sleep 4
#   done
# }

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
for name in SAMBA_DC_DC_DOMAIN SAMBA_DC_DOMAIN SAMBA_DC_REALM; do
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
echo "nameserver ${SAMBA_FS_DNS_SERVER:-$SAMBA_DC_DNS_SERVER}" >> /etc/resolv.conf
for dns in $(echo $HOST_DNS_SERVER | tr " " "\n")
do
  echo "nameserver $dns" >> /etc/resolv.conf
done
echo "search $SAMBA_DC_DNS_SEARCH" >> /etc/resolv.conf

# /var/lib/samba is a bind mount that starts empty, so the directories the
# image ships are not there. net ads join needs private/ for its messaging
# socket and fails with "Unable to initialize messaging context!" without it.
mkdir -p /var/lib/samba/private /var/log/samba

join_domain

echo "Create share"
mkdir -p /userdata/$SHARE_DIR_NAME
mkdir -p /userdata/Home
