#!/bin/sh

echo "Applying for a certificate"
mkdir -p /certs/certs1000
$( /lego -a -m=$LEGO_EMAIL --domains $BASE_DOMAIN --domains *.$BASE_DOMAIN --path /certs --pem --dns $DNS_PROVIDER --dns.resolvers $LEGO_DNS_SERVER renew --days 30 )
if [ $? -ne 0 ]; then
  $( /lego -a -m=$LEGO_EMAIL --domains $BASE_DOMAIN --domains *.$BASE_DOMAIN --path /certs --pem --dns $DNS_PROVIDER --dns.resolvers $LEGO_DNS_SERVER run )
  if [ $? -eq 0 ]; then
    echo "Copy certificates(root) to certs1000(user 1000)"
    cp /certs/certificates/* /certs/certs1000/
    chown 1000:1000 -R /certs/certs1000
  fi
else
  echo "Copy certificates(root) to certs1000(user 1000)"
  cp /certs/certificates/* /certs/certs1000/
  chown 1000:1000 -R /certs/certs1000
fi

