#!/usr/bin/with-contenv bash

set_host() { # $1 domain, $2 ip
  echo "Set $2 $1"
  if grep -q $1 "/etc/hosts"; then
    hosts=$( sed "s/.*$1.*/$2\t$1/" "/etc/hosts" )
    echo "$hosts" > "/etc/hosts"
  else
    echo -e "$2\t$1" >> "/etc/hosts"
  fi
}

if [ "$SIDECAR_CRON" = "1" ] || [ "$SIDECAR_PREVIEWGEN" = "1" ] || [ "$SIDECAR_NEWSUPDATER" = "1" ]; then
  exit 0
fi

if [ -f /var/www/config/autoconfig.php ] ; then
  rm /var/www/config/autoconfig.php
fi

echo "Set hosts"
traefik_ip=$( ping $TRAEFIK_HOSTNAME -c 1 | sed '1{s/[^(]*(//;s/).*//;q}' )
if [ -n "$COLLABORA_DOMAIN" ]; then
  set_host $COLLABORA_DOMAIN $traefik_ip
fi
if [ -n "$NEXTCLOUD_DOMAIN" ]; then
  set_host $NEXTCLOUD_DOMAIN $traefik_ip
fi
if [ "$NEXTCLOUD_TALK_ENABLED" == "true" ]; then
  turn_ip=$( ping $TURN_HOSTNAME -c 1 | sed '1{s/[^(]*(//;s/).*//;q}' )
  set_host $TURN_DOMAIN $turn_ip
fi
# sed -i 's/^pm.max_spare_servers =.*/pm.max_spare_servers = 3/' /etc/php/php-fpm.d/www.conf; \
# php speed up
sed -i 's/^pm.max_children =.*/pm.max_children = 100/' /etc/php/php-fpm.d/www.conf; 
# set_host $TALK_SIGNALING_DOMAIN $traefik_ip
# talk_turn_ip=$( ping $TALK_HOSTNAME -c 1 | sed '1{s/[^(]*(//;s/).*//;q}' )
# set_host $TALK_TURN_DOMAIN $talk_turn_ip

# domain_write_to_hosts() { # $1 domain
#   ip=$( ping $1 -c 1 | sed '1{s/[^(]*(//;s/).*//;q}')
#   if grep -q $1 "/etc/hosts"; then
#     hosts=$( sed "s/.*$1.*/$ip\t$1/" "/etc/hosts" )
#     echo "$hosts" > "/etc/hosts"
#   else
#     echo -e "$ip\t$1" >> "/etc/hosts"
#   fi
# }

# domain_write_to_hosts $MYSQL_HOST

# config_in_file() { # $1 filename, $2 search string, $3 config string
#   if grep -q $2 "$1"; then
#     hosts=$( sed "s/.*$2.*/$3/" "$1" )
#     echo "$hosts" > "$1"
#   else
#     echo -e "$3" >> "$1"
#   fi
# }

# echo "Setting DNS server to $LOCAL_DNS_SERVER"
# config_in_file /etc/resolv.conf 'nameserver' "nameserver $LOCAL_DNS_SERVER"
