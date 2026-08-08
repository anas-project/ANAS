#!/usr/bin/with-contenv bash
set -euo pipefail

export SAMBA_DC_DOMAIN_ACTION="${SAMBA_DC_DOMAIN_ACTION:-provision}"
export SAMBA_DC_DOMAIN_MASTER="${SAMBA_DC_DOMAIN_MASTER:-auto}"
rm -f /var/lib/samba/.anas-zone-ready /run/anas-zone.ready /run/anas-identity-schema.ready
export SAMBA_DC_BIND_INTERFACES_ONLY="${SAMBA_DC_BIND_INTERFACES_ONLY:-No}"
export SAMBA_DC_SERVER_STRING="${SAMBA_DC_SERVER_STRING:-'Samba Domain Controller'}"

if [ ! -f /etc/timezone ] && [ ! -z "$TZ" ]; then
  echo 'Set timezone'
  cp /usr/share/zoneinfo/$TZ /etc/localtime
  echo $TZ >/etc/timezone
fi

# Cores must exist before samba runs, not after: provisioning already starts
# smbd/winbindd and they log a failure for every missing core directory.
mkdir -p /var/log/samba/cores /var/log/samba-audit
chmod 700 /var/log/samba/cores
chmod 755 /var/log/samba-audit

if [ ! -f /var/lib/samba/registry.tdb ]; then
  INTERFACE_OPTS="--option=\"bind interfaces only=$SAMBA_DC_BIND_INTERFACES_ONLY\" \
      --option=\"interfaces=$SAMBA_DC_INTERFACES\" \
      --option=\"posix:eadb=/var/lib/samba/eadb.tdb\""

  if [ $SAMBA_DC_DOMAIN_ACTION == provision ]; then
    PROVISION_OPTS="--server-role=dc --use-rfc2307 --domain=$SAMBA_DC_WORKGROUP \
    --realm=$SAMBA_DC_REALM --adminpass='$SAMBA_DC_ADMINISTRATOR_PASSWORD'"
    PROVISION_OPTS_ECHO="--server-role=dc --use-rfc2307 --domain=$SAMBA_DC_WORKGROUP \
    --realm=$SAMBA_DC_REALM --adminpass='*****'"
  elif [ $SAMBA_DC_DOMAIN_ACTION == join ]; then
    PROVISION_OPTS="$SAMBA_DC_REALM DC -U$SAMBA_DC_ADMINISTRATOR_NAME --password='$SAMBA_DC_ADMINISTRATOR_PASSWORD'"
    PROVISION_OPTS_ECHO="$SAMBA_DC_REALM DC -U$SAMBA_DC_ADMINISTRATOR_NAME --password='*****'"
  else
    echo 'Only provision and join actions are supported.'
    exit 1
  fi

  rm -f /etc/samba/smb.conf /etc/krb5.conf

  # This step is required for INTERFACE_OPTS to work as expected
  echo "Samba initializing...."
  echo "'samba-tool domain $SAMBA_DC_DOMAIN_ACTION $PROVISION_OPTS_ECHO $INTERFACE_OPTS \
     --dns-backend=BIND9_DLZ'"
  echo "samba-tool domain $SAMBA_DC_DOMAIN_ACTION $PROVISION_OPTS $INTERFACE_OPTS \
     --dns-backend=BIND9_DLZ" | sh

  mv /etc/samba/smb.conf /etc/samba/smb.conf.bak
  echo '!root = $SAMBA_DC_ADMIN_NAME' > /etc/samba/smbusers
fi

cp /var/lib/samba/private/krb5.conf /etc/krb5.conf

if [ -n "${SAMBA_DC_TLS_KEYFILE:-}" ] && [ -n "${SAMBA_DC_TLS_CERTFILE:-}" ] && [ -n "${SAMBA_DC_TLS_CAFILE:-}" ]; then
  echo "Using configured Samba TLS certificate"
else
  # The deployment always publishes a certificate here: an ACME one when the
  # domain can be validated, otherwise one signed by the internal CA. This
  # cask used to mint its own self-signed certificate when the directory was
  # empty, which is precisely why nothing trusted it — every cask that did the
  # same became its own issuer, and an LDAPS client had no way to verify any of
  # them. If the file is missing now, something upstream failed and saying so
  # is more useful than quietly serving a certificate no client can check.
  for f in "${LEGO_KEY_NAME:-}" "${LEGO_CERT_NAME:-}" "${LEGO_CA_CERT_NAME:-}"; do
    if [ -z "$f" ] || [ ! -s "/certs/$f" ]; then
      echo "Samba TLS material /certs/${f:-<unset>} is missing; the certificate provider has not published it" >&2
      exit 1
    fi
  done
  echo "Using the deployment certificate for Samba TLS"
  export SAMBA_DC_TLS_KEYFILE="/certs/$LEGO_KEY_NAME"
  export SAMBA_DC_TLS_CERTFILE="/certs/$LEGO_CERT_NAME"
  export SAMBA_DC_TLS_CAFILE="/certs/$LEGO_CA_CERT_NAME"
fi
export SAMBA_DC_TLS_ENABLED="${SAMBA_DC_TLS_ENABLED:-yes}"

echo "Creating /etc/samba/smb.conf ..."
smb_variables='${SAMBA_DC_BIND_INTERFACES_ONLY} ${SAMBA_DC_DOMAIN_MASTER} ${SAMBA_DC_INTERFACES} ${SAMBA_DC_LOG_LEVEL} ${SAMBA_DC_MAX_LOG_SIZE} ${SAMBA_DC_NETBIOS_NAME} ${SAMBA_DC_REALM} ${SAMBA_DC_TEMPLATE_HOMEDIR} ${SAMBA_DC_TEMPLATE_SHELL} ${SAMBA_DC_TLS_CAFILE} ${SAMBA_DC_TLS_CERTFILE} ${SAMBA_DC_TLS_ENABLED} ${SAMBA_DC_TLS_KEYFILE} ${SAMBA_DC_WORKGROUP}'
for name in $(printf '%s\n' "$smb_variables" | grep -o '[A-Z][A-Z0-9_]*'); do
  eval 'present=${'"$name"'+x}'
  if [[ "$present" != x ]]; then
    echo "missing required environment variable: $name" >&2
    exit 1
  fi
done
envsubst "$smb_variables" < /root/smb.conf.envsubst > /etc/samba/smb.conf.tmp
if grep -n '\${[A-Z][A-Z0-9_]*}' /etc/samba/smb.conf.tmp; then
  echo "unresolved variables in smb.conf" >&2
  exit 1
fi
mv /etc/samba/smb.conf.tmp /etc/samba/smb.conf
testparm -s /etc/samba/smb.conf >/dev/null

# Schema changes must be applied while the directory daemon is stopped. Doing
# this here also makes upgrades of an existing provisioned volume idempotent.
/usr/local/bin/install-identity-schema.sh

chmod 0755 /usr/local/bin/structure.sh
chmod +x /usr/local/bin/anas_zone.sh
