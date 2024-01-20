#!/bin/sh

echo "Applying for a certificate"
$( /lego -a -m=$LEGO_EMAIL --domains $BASE_DOMAIN --domains *.$BASE_DOMAIN --path /certs --pem --dns $DNS_PROVIDER --dns.resolvers $LEGO_DNS_SERVER renew --days 30 )
if [ $? -ne 0 ]; then
  $( /lego -a -m=$LEGO_EMAIL --domains $BASE_DOMAIN --domains *.$BASE_DOMAIN --path /certs --pem --dns $DNS_PROVIDER --dns.resolvers $LEGO_DNS_SERVER run )
  # echo "Fix certs permission..."
  # chown lego:lego -R /certs/
fi