#!/usr/bin/with-contenv bash

mkdir -p /etc/avahi/services/
envsubst < /root/samba.service.j2 > /etc/avahi/services/samba.service

