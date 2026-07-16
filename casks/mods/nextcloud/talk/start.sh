#!/bin/bash

set_host() { # $1 domain, $2 ip
  echo "Set $2 $1"
  if grep -q $1 "/etc/hosts"; then
    hosts=$( sed "s/.*$1.*/$2\t$1/" "/etc/hosts" )
    echo "$hosts" > "/etc/hosts"
  else
    echo -e "$2\t$1" >> "/etc/hosts"
  fi
}

turn_ip=$( ping $TURN_HOSTNAME -c 1 | sed '1{s/[^(]*(//;s/).*//;q}' )
set_host $TURN_DOMAIN $turn_ip

# Signling
cat << SIGNALING_CONF > "/conf/signaling.conf"
[http]
listen = 0.0.0.0:8081

[app]
debug = false

[sessions]
hashkey = ${TALK_HASH_KEY}
blockkey = ${TALK_BLOCK_KEY}

[clients]
internalsecret = ${NEXTCLOUD_TALK_INTERNAL_SECRET}

[backend]
backends = backend-1
allowall = false
timeout = 10
connectionsperhost = 8

[backend-1]
url = ${NEXTCLOUD_DOMAIN_FULL}
secret = ${TALK_SIGNALING_SECRET}

[nats]
url = nats://127.0.0.1:4222

[mcu]
type = janus
url = ws://127.0.0.1:8188
SIGNALING_CONF

exec "$@"
