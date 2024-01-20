#!/bin/sh

envsubst < /var/lib/ldap-account-manager/config/lam.conf.j2 > /var/lib/ldap-account-manager/config/lam.conf

exec /usr/local/bin/start.sh
