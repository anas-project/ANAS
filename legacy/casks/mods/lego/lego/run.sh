#!/bin/sh

echo "Run script"

# id lego > /dev/null 2>&1
# if [ $? -ne 0 ]; then
#   echo "Create user lego($PUID):lego($PGID)"
#   addgroup lego -g $PGID -S
#   adduser -G lego lego -u $PUID -D -H -g lego -h /
# fi

echo "Setting DNS server to $LEGO_DNS_SERVER"
echo "nameserver $LEGO_DNS_SERVER" > /etc/resolv.conf

/root/cert.sh

echo "Run cron"
exec crond -l 2 -f