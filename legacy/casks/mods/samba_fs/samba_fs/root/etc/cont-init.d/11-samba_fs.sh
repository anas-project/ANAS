#!/usr/bin/with-contenv bash

join_domain() {
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

envsubst < /etc/samba/smb.conf.j2 > /etc/samba/smb.conf 
envsubst < /etc/samba/smbusers.j2 > /etc/samba/smbusers
envsubst < /etc/krb5.conf.j2 > /etc/krb5.conf

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
echo "nameserver $BIND_HOST_IP" >> /etc/resolv.conf
for dns in $(echo $HOST_DNS_SERVER | tr " " "\n")
do
  echo "nameserver $dns" >> /etc/resolv.conf
done
echo "search $SAMBA_DC_DNS_SEARCH" >> /etc/resolv.conf

join_domain

mkdir -p /shares/homes/
mkdir -p /shares/alice/
mkdir -p /var/log/samba/

echo "Create share"
mkdir -p /$USERDATA_NAME/$SHARE_DIR_NAME
mkdir -p /$USERDATA_NAME/Home
