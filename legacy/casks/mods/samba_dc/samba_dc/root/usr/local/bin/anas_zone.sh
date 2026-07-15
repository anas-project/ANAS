#!/usr/bin/with-contenv bash

waiting_dns() {
  while :
  do
    port_open=$(nc -zv 127.0.0.1 53 2>&1)
    if [[ "$port_open" == *" succeeded"* ]]; then
      echo "DNS already online"
      return
    fi
    echo "Waiting dns online..."
    sleep 2
  done
}

sleep 5

echo "Add domain resolve"

if [ $BIND_DEBUG == "true" ]; then
  nsupdate_command="nsupdate -d -g"
else
  nsupdate_command="nsupdate -g"
fi

waiting_dns

# update *.$BASE_DOMAIN to host ip
hostname=$(hostname -s)
echo "exec \"kinit -k -t /var/lib/samba/private/dns.keytab dns-$hostname\""
kinit -k -t /var/lib/samba/private/dns.keytab dns-$hostname
update_sheet="
  server 127.0.0.1
"

for domain in $(echo $DOMAINS | tr "," "\n")
do
  domain_arr=( $(echo $domain | tr "/" " ") )
  if [ "${domain_arr[0]}" == 'inner' ]; then   
    echo "add ${domain_arr[1]}.$BASE_DOMAIN. 3600 IN A $HOST_IP"  
    update_sheet="
      $update_sheet
      update add ${domain_arr[1]}.$BASE_DOMAIN. 3600 IN A $HOST_IP
      "
  elif [ "${domain_arr[0]}" == 'dhcp' ]; then 
    echo "dhcp TODO"
    # TODO
  fi
done
echo "
  $update_sheet
  send
  quit
  " | $nsupdate_command
echo "Add domain completed"
