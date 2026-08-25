#!/usr/bin/env bash
set -euo pipefail

usage() {
  cat <<'EOF'
usage: server-domain-separation-e2e.sh <authentik-ad-zone|llng-separate-zone> [core|contracts|full]

  core       Read-only domain, DNS, Kerberos, LDAPS, LAM, IAM, and Nextcloud checks.
  contracts  Run core plus the existing IAM runtime-contract probe (default).
  full       Run core plus the existing IAM login matrix, LAM Admins login, and
             Nextcloud managed-local-admin probes. ANAS_TEST_WORKSPACE is required.

The deployment must already be running on its dedicated isolated Docker daemon.
This script never starts or removes containers.
EOF
}

profile=${1:-}
probe_level=${2:-${ANAS_TEST_DOMAIN_SEPARATION_PROBES:-contracts}}

case "$profile" in
  authentik-ad-zone)
    base_domain=nas.test.example
    samba_domain=test.example
    resolved_mode=ad_zone
    dns_zone=test.example
    iam_provider=authentik
    default_prefix=anas_ds_auth_
    default_socket=/run/anas-domain-auth-e2e-docker.sock
    default_entry_ip=10.252.10.2
    fixture=test-env/server-domain-separation-authentik-e2e.yml
    ;;
  llng-separate-zone)
    base_domain=apps.example.test
    samba_domain=ad.example.test
    resolved_mode=separate_zone
    dns_zone=apps.example.test
    iam_provider=llng
    default_prefix=anas_ds_llng_
    default_socket=/run/anas-domain-llng-e2e-docker.sock
    default_entry_ip=10.252.11.2
    fixture=test-env/server-domain-separation-llng-e2e.yml
    ;;
  -h|--help)
    usage
    exit 0
    ;;
  *)
    usage >&2
    exit 2
    ;;
esac

case "$probe_level" in
  core|contracts|full) ;;
  *)
    printf 'unsupported probe level: %s\n' "$probe_level" >&2
    usage >&2
    exit 2
    ;;
esac

script_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
repo_root=$(cd -- "$script_dir/../.." && pwd)
docker_cmd=${DOCKER_CMD:-docker}
prefix=${ANAS_TEST_CONTAINER_PREFIX:-$default_prefix}
entry_ip=${ANAS_TEST_ENTRY_IP:-$default_entry_ip}

if [[ -n ${ANAS_TEST_DOCKER_SOCKET:-} ]]; then
  docker_socket=$ANAS_TEST_DOCKER_SOCKET
elif [[ ${DOCKER_HOST:-} == unix:///* ]]; then
  docker_socket=${DOCKER_HOST#unix://}
elif [[ -n ${DOCKER_HOST:-} ]]; then
  printf 'only an isolated unix Docker socket is supported: %s\n' "$DOCKER_HOST" >&2
  exit 2
else
  docker_socket=$default_socket
fi
export ANAS_TEST_DOCKER_SOCKET=$docker_socket
export DOCKER_HOST="unix://$docker_socket"

# shellcheck source=server-require-isolated-docker.sh
source "$script_dir/server-require-isolated-docker.sh"

container() {
  printf '%s%s' "$prefix" "$1"
}

section() {
  printf '\n== %s ==\n' "$1"
}

fail() {
  printf 'domain-separation E2E failed: %s\n' "$1" >&2
  exit 1
}

assert_eq() {
  local label=$1 actual=$2 expected=$3
  if [[ "$actual" != "$expected" ]]; then
    printf '%s: got %q, want %q\n' "$label" "$actual" "$expected" >&2
    exit 1
  fi
  printf '%s=%s\n' "$label" "$actual"
}

container_env() {
  local target=$1 name=$2 value
  if ! value=$("$docker_cmd" exec "$target" printenv "$name" 2>/dev/null); then
    printf 'cannot read %s from %s\n' "$name" "$target" >&2
    return 1
  fi
  printf '%s\n' "$value"
}

domain_to_dn() {
  local domain=$1 label dn= first=true
  local -a labels
  IFS=. read -r -a labels <<<"$domain"
  for label in "${labels[@]}"; do
    if [[ $first == true ]]; then
      dn="DC=$label"
      first=false
    else
      dn="$dn,DC=$label"
    fi
  done
  printf '%s' "$dn"
}

dc=$(container samba_dc)
fs=$(container samba_fs)
lego=$(container lego)
lam=$(container lam)
iam=$(container "$iam_provider")
eturnal=
nextcloud=$(container nextcloud)
nextcloud_cron=$(container nextcloud_cron)
authentik_worker=
if [[ "$iam_provider" == authentik ]]; then
  authentik_worker=$(container authentik_worker)
else
  eturnal=$(container eturnal)
fi
base_dn=$(domain_to_dn "$samba_domain")

section "isolated fixture and required containers"
printf 'fixture=%s profile=%s probes=%s docker_socket=%s\n' \
  "$fixture" "$profile" "$probe_level" "$docker_socket"
required_containers=("$dc" "$fs" "$lego" "$lam" "$iam" "$nextcloud" "$nextcloud_cron")
if [[ -n "$authentik_worker" ]]; then
  required_containers+=("$authentik_worker")
fi
if [[ -n "$eturnal" ]]; then
  required_containers+=("$eturnal")
fi
for target in "${required_containers[@]}"; do
  state=$("$docker_cmd" inspect --format '{{.State.Status}}' "$target")
  health=$("$docker_cmd" inspect --format '{{if .State.Health}}{{.State.Health.Status}}{{else}}none{{end}}' "$target")
  restarts=$("$docker_cmd" inspect --format '{{.RestartCount}}' "$target")
  [[ "$state" == running ]] || fail "$target is $state"
  [[ "$health" != unhealthy && "$health" != starting ]] || fail "$target health is $health"
  printf '%s state=%s health=%s restarts=%s\n' "$target" "$state" "$health" "$restarts"
done

section "directory and Web-domain contract"
assert_eq BASE_DOMAIN "$(container_env "$dc" BASE_DOMAIN)" "$base_domain"
assert_eq SAMBA_DC_DOMAIN "$(container_env "$dc" SAMBA_DC_DOMAIN)" "$samba_domain"
assert_eq SAMBA_DC_REALM "$(container_env "$dc" SAMBA_DC_REALM)" "${samba_domain^^}"
assert_eq SAMBA_DC_BASE_DN "$(container_env "$dc" SAMBA_DC_BASE_DN)" "$base_dn"
assert_eq SAMBA_DC_DNS_SEARCH "$(container_env "$dc" SAMBA_DC_DNS_SEARCH)" "$samba_domain"
assert_eq SAMBA_DC_APPLICATION_DNS_MODE "$(container_env "$dc" SAMBA_DC_APPLICATION_DNS_MODE)" auto
assert_eq SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED \
  "$(container_env "$dc" SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED)" "$resolved_mode"
assert_eq SAMBA_DC_APPLICATION_DNS_ZONE \
  "$(container_env "$dc" SAMBA_DC_APPLICATION_DNS_ZONE)" "$dns_zone"
assert_eq SAMBA_DC_HOST "$(container_env "$dc" SAMBA_DC_HOST)" "$base_domain"
assert_eq SAMBA_DC_HOST_IP "$(container_env "$dc" SAMBA_DC_HOST_IP)" "$entry_ip"
assert_eq SAMBA_DC_LDAPS_SERVER_URL \
  "$(container_env "$dc" SAMBA_DC_LDAPS_SERVER_URL)" "ldaps://$base_domain"
dc_fqdn=$(container_env "$dc" SAMBA_DC_DC_DOMAIN)
case "$dc_fqdn" in
  *."$samba_domain") ;;
  *) fail "canonical DC FQDN $dc_fqdn is outside $samba_domain" ;;
esac
printf 'SAMBA_DC_DC_DOMAIN=%s\n' "$dc_fqdn"
fs_hostname=$(container_env "$fs" SAMBA_FS_HOSTNAME)
fs_host_ip=$(container_env "$fs" HOST_LAN_IP)
fs_fqdn="${fs_hostname,,}.$samba_domain"
printf 'SAMBA_FS_FQDN=%s SAMBA_FS_HOST_IP=%s\n' "$fs_fqdn" "$fs_host_ip"

section "authoritative DNS mode, zone ownership, and records"
"$docker_cmd" exec "$dc" bash -ceu '
  expected_mode=$1
  expected_zone=$2
  expected_base=$3
  expected_dc=$4
  expected_fs=$5
  expected_fs_ip=$6
  state=/var/lib/samba/.anas-application-zone-v1
  managed=/var/lib/samba/.anas-managed-dns-v1.tsv

  test -s "$state"
  IFS=$'\''\t'\'' read -r applied_mode applied_zone ownership < "$state"
  test "$applied_mode" = "$expected_mode"
  test "$applied_zone" = "$expected_zone"
  case "$expected_mode:$ownership" in
    ad_zone:directory|separate_zone:anas) ;;
    *) echo "unexpected application-zone ownership: $expected_mode/$ownership" >&2; exit 1 ;;
  esac

  zones=$(samba-tool dns zonelist 127.0.0.1 \
    -U "$SAMBA_DC_ADMINISTRATOR_NAME%$SAMBA_DC_ADMINISTRATOR_PASSWORD")
  printf "%s\n" "$zones" | awk -F: -v zone="$expected_zone" '\''
    /pszZoneName/ {
      value=$2
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", value)
      if (tolower(value) == tolower(zone)) found=1
    }
    END { exit !found }
  '\''

  expect_a() {
    local fqdn=$1 target=$2
    host "$fqdn" 127.0.0.1 | grep -Fq "has address $target"
  }
  expect_exact_a() {
    local fqdn=$1 target=$2 addresses
    addresses=$(host "$fqdn" 127.0.0.1 | awk '\''/ has address / { print $NF }'\'' | sort -u)
    test "$addresses" = "$target"
  }
  expect_managed() {
    local fqdn=$1 target=$2
    awk -F "\t" -v zone="$expected_zone" -v fqdn="$fqdn" -v target="$target" '\''
      $1 == zone && $2 == fqdn && $3 == "A" && $4 == target { found=1 }
      END { exit !found }
    '\'' "$managed"
  }

  expect_a "$expected_dc" "$SAMBA_DC_HOST_IP"
  host -t SRV "_ldap._tcp.$SAMBA_DC_DOMAIN" 127.0.0.1 \
    | tr "[:upper:]" "[:lower:]" | grep -Fq " ${expected_dc,,}."
  host -t SRV "_kerberos._tcp.$SAMBA_DC_DOMAIN" 127.0.0.1 \
    | tr "[:upper:]" "[:lower:]" | grep -Fq " ${expected_dc,,}."
  expect_a "$expected_base" "$SAMBA_DC_HOST_IP"
  expect_managed "$expected_base" "$SAMBA_DC_HOST_IP"
  expect_exact_a "$expected_fs" "$expected_fs_ip"

  IFS=, read -r -a entries <<< "${DOMAINS:-}"
  for entry in "${entries[@]}"; do
    test -n "$entry" || continue
    IFS=/ read -r kind fqdn owner <<< "$entry"
    test "$kind" = inner || continue
    expect_a "$fqdn" "$HOST_IP"
    expect_managed "$fqdn" "$HOST_IP"
  done
  printf "dns_mode=%s zone=%s ownership=%s records=ok\n" "$applied_mode" "$applied_zone" "$ownership"
' domain-dns-probe "$resolved_mode" "$dns_zone" "$base_domain" "$dc_fqdn" "$fs_fqdn" "$fs_host_ip"

section "Samba FS Kerberos and machine trust"
"$docker_cmd" exec "$fs" bash -ceu '
  expected_domain=$1
  expected_realm=$2
  expected_dc=$3
  test "$SAMBA_DC_DOMAIN" = "$expected_domain"
  test "$SAMBA_DC_REALM" = "$expected_realm"
  test "$SAMBA_DC_DC_DOMAIN" = "$expected_dc"
  grep -Fq "default_realm = $expected_realm" /etc/krb5.conf
  grep -Fq "default_domain = $expected_domain" /etc/krb5.conf
  grep -Fq "$expected_dc" /etc/krb5.conf
  net ads testjoin
  wbinfo -t
  trap "kdestroy >/dev/null 2>&1 || true" EXIT
  printf "%s\n" "$SAMBA_DC_ADMIN_PASSWORD" | kinit "$SAMBA_DC_ADMIN_NAME@$expected_realm"
  klist -s
  printf "kerberos_realm=%s machine_trust=ok kinit=ok\n" "$expected_realm"
' samba-fs-probe "$samba_domain" "${samba_domain^^}" "$dc_fqdn"

section "LDAPS certificate hostname, chain, bind, and RootDSE"
"$docker_cmd" exec "$lego" sh -ceu '
  endpoint_ip=$1
  service_name=$2
  ca_file="/certs/certificates/$ANAS_TLS_TRUST_BUNDLE_NAME"
  test -s "$ca_file"
  result=$(openssl s_client \
    -connect "$endpoint_ip:636" \
    -servername "$service_name" \
    -verify_hostname "$service_name" \
    -verify_return_error \
    -CAfile "$ca_file" </dev/null 2>&1)
  printf "%s\n" "$result" | grep -Eq "Verification: OK|Verify return code: 0 \(ok\)"
' ldaps-tls-probe "$entry_ip" "$base_domain"
printf 'ldaps_tls_hostname=%s chain=ok\n' "$base_domain"

"$docker_cmd" exec -i "$lam" php -- "$base_domain" "$samba_domain" "$base_dn" "$dc_fqdn" <<'PHP'
<?php
declare(strict_types=1);

[$script, $expectedAlias, $expectedDomain, $expectedBaseDn, $expectedDc] = $argv;
$url = (string) getenv('SAMBA_DC_LDAPS_SERVER_URL');
$bindDn = (string) getenv('SAMBA_DC_LDAP_BIND_DN');
$bindPassword = (string) getenv('SAMBA_DC_LDAP_BIND_PASSWORD');

if (getenv('SAMBA_DC_DOMAIN') !== $expectedDomain
    || getenv('SAMBA_DC_BASE_DN') !== $expectedBaseDn
    || parse_url($url, PHP_URL_SCHEME) !== 'ldaps'
    || parse_url($url, PHP_URL_HOST) !== $expectedAlias) {
    fwrite(STDERR, "LAM received a mixed Web/directory LDAP contract\n");
    exit(20);
}

ldap_set_option(null, LDAP_OPT_X_TLS_REQUIRE_CERT, LDAP_OPT_X_TLS_DEMAND);
$ldap = ldap_connect($url);
if ($ldap === false) {
    fwrite(STDERR, "cannot create the LDAPS connection\n");
    exit(21);
}
ldap_set_option($ldap, LDAP_OPT_PROTOCOL_VERSION, 3);
ldap_set_option($ldap, LDAP_OPT_REFERRALS, 0);
ldap_set_option($ldap, LDAP_OPT_X_TLS_REQUIRE_CERT, LDAP_OPT_X_TLS_DEMAND);
if (!@ldap_bind($ldap, $bindDn, $bindPassword)) {
    fwrite(STDERR, "LDAPS service-account bind failed\n");
    exit(22);
}

$search = @ldap_read(
    $ldap,
    '',
    '(objectClass=*)',
    ['defaultNamingContext', 'rootDomainNamingContext', 'dnsHostName']
);
if ($search === false) {
    fwrite(STDERR, "RootDSE query failed\n");
    exit(23);
}
$entries = ldap_get_entries($ldap, $search);
$entry = $entries[0] ?? [];
$value = static function (array $item, string $name): string {
    $key = strtolower($name);
    return (string) ($item[$key][0] ?? '');
};

$defaultDn = $value($entry, 'defaultNamingContext');
$rootDn = $value($entry, 'rootDomainNamingContext');
$dnsHostName = $value($entry, 'dnsHostName');
if (strcasecmp($defaultDn, $expectedBaseDn) !== 0
    || strcasecmp($rootDn, $expectedBaseDn) !== 0
    || strcasecmp($dnsHostName, $expectedDc) !== 0) {
    fwrite(STDERR, "RootDSE does not describe the configured Samba domain\n");
    exit(24);
}
ldap_unbind($ldap);
printf(
    "ldaps_bind=ok defaultNamingContext=%s rootDomainNamingContext=%s dnsHostName=%s\n",
    $defaultDn,
    $rootDn,
    $dnsHostName
);
PHP

section "LAM, Nextcloud, and IAM runtime consumers"
assert_eq LAM_DOMAIN "$(container_env "$lam" LAM_DOMAIN)" "lam.$base_domain"
assert_eq LAM_SAMBA_DC_DOMAIN "$(container_env "$lam" SAMBA_DC_DOMAIN)" "$samba_domain"
assert_eq LAM_LDAPS_URL \
  "$(container_env "$lam" SAMBA_DC_LDAPS_SERVER_URL)" "ldaps://$base_domain"
assert_eq NEXTCLOUD_DOMAIN \
  "$(container_env "$nextcloud" NEXTCLOUD_DOMAIN)" "nc.$base_domain"
assert_eq NEXTCLOUD_LDAPS_URL \
  "$(container_env "$nextcloud" SAMBA_DC_LDAPS_SERVER_URL)" "ldaps://$base_domain"
assert_eq NEXTCLOUD_BASE_DN \
  "$(container_env "$nextcloud" SAMBA_DC_BASE_DN)" "$base_dn"
"$docker_cmd" exec "$nextcloud" occ ldap:test-config s01
"$docker_cmd" exec "$nextcloud_cron" sh -ceu '
  source_ca="/certs/${ANAS_TLS_INTERNAL_CA_NAME:-anas-internal-ca.crt}"
  installed_ca=/usr/local/share/ca-certificates/anas-internal-ca.crt
  test -s "$source_ca"
  test -s "$installed_ca"
  cmp -s "$source_ca" "$installed_ca"
'
printf 'nextcloud_cron_internal_ca=installed\n'

case "$iam_provider" in
  authentik)
    assert_eq AUTHENTIK_DOMAIN \
      "$(container_env "$iam" AUTHENTIK_DOMAIN)" "auth.$base_domain"
    assert_eq AUTHENTIK_LDAP_SERVER_URI \
      "$(container_env "$iam" AUTHENTIK_LDAP_SERVER_URI)" "ldaps://$base_domain:636"
    assert_eq AUTHENTIK_LDAP_BASE_DN \
      "$(container_env "$iam" AUTHENTIK_LDAP_BASE_DN)" "$base_dn"
    assert_eq AUTHENTIK_WORKER_LDAP_SERVER_URI \
      "$(container_env "$authentik_worker" AUTHENTIK_LDAP_SERVER_URI)" "ldaps://$base_domain:636"
    assert_eq AUTHENTIK_WORKER_LDAP_BASE_DN \
      "$(container_env "$authentik_worker" AUTHENTIK_LDAP_BASE_DN)" "$base_dn"
    "$docker_cmd" exec "$authentik_worker" sh -ceu '
      test -s "/certs/$ANAS_TLS_TRUST_BUNDLE_NAME"
      ak healthcheck
    '
    "$docker_cmd" exec "$iam" ak shell -c \
      'import os; from authentik.sources.ldap.models import LDAPSource; source=LDAPSource.objects.get(slug="samba-ad"); assert source.server_uri == os.environ["AUTHENTIK_LDAP_SERVER_URI"]; assert source.base_dn == os.environ["AUTHENTIK_LDAP_BASE_DN"]; assert source.peer_certificate is not None'
    ;;
  llng)
    assert_eq LLNG_DOMAIN "$(container_env "$iam" LLNG_DOMAIN)" "auth.$base_domain"
    assert_eq LLNG_LDAPS_URL \
      "$(container_env "$iam" SAMBA_DC_LDAPS_SERVER_URL)" "ldaps://$base_domain"
    "$docker_cmd" exec "$iam" sh -ceu '
      test -n "$LLNG_SAML_SERVICE_PRIVATE_KEY"
      test -n "$LLNG_OIDC_SERVICE_PRIVATE_KEY"
      test "$LLNG_SAML_SERVICE_PRIVATE_KEY" = "$LLNG_OIDC_SERVICE_PRIVATE_KEY"
      file=$(find /var/lib/lemonldap-ng/conf -maxdepth 1 -name "lmConf-*.json" | sort -V | tail -n 1)
      test -n "$file"
      jq -e '\''
        .domain == env.BASE_DOMAIN and
        .ldapServer == env.SAMBA_DC_LDAPS_SERVER_URL and
        .ldapPort == env.SAMBA_DC_LDAPS_PORT and
        .ldapBase == env.SAMBA_DC_BASE_USERS_DN and
        .ldapGroupBase == env.SAMBA_DC_BASE_GROUPS_DN and
        .managerDn == env.SAMBA_DC_PASSWORD_BIND_DN and
        .ldapVerify == "require" and
        (.samlServicePrivateKeySig | length > 0) and
        (.oidcServicePrivateKeySig | length > 0)
      '\'' "$file" >/dev/null
    '
    printf 'llng_private_key_projection=ok\n'

    section "Eturnal first-activation credential readiness"
    "$docker_cmd" exec "$eturnal" sh -ceu '
      config_dir=${ANAS_CONFIG_DIR:-${ETURNAL_ETC_DIR:-}}
      test -n "$config_dir"
      config=$config_dir/eturnal.yml
      test -s "$config"
      test -n "$TURN_SECRET"
      escaped=$(printf "%s" "$TURN_SECRET" | sed "s/'\''/'\'''\''/g")
      grep -Fqx "  secret: '\''$escaped'\''" "$config"
    '
    printf 'eturnal_live_credential=match prewarm=not-used\n'
    ;;
esac

case "$probe_level" in
  core)
    printf '\nExisting state-changing probes skipped (probe level: core).\n'
    ;;
  contracts)
    section "existing IAM runtime-contract probe"
    ANAS_TEST_IAM_PROVIDER=$iam_provider \
    ANAS_TEST_IAM_APPS=nextcloud,meshcentral,netbird \
    ANAS_TEST_CONTAINER_PREFIX=$prefix \
      "$script_dir/server-iam-runtime-contract-e2e.sh"
    printf 'LAM temporary-user and Nextcloud credential-rotation probes skipped (probe level: contracts).\n'
    ;;
  full)
    : "${ANAS_TEST_WORKSPACE:?ANAS_TEST_WORKSPACE is required for the full probe level}"
    section "existing IAM login-matrix probe"
    ANAS_TEST_CONTAINER_PREFIX=$prefix \
    ANAS_TEST_DOMAIN=$base_domain \
    ANAS_TEST_ENTRY_IP=$entry_ip \
    ANAS_TEST_DOCKER_SOCKET=$docker_socket \
      "$script_dir/server-${iam_provider}-login-matrix-e2e.sh"

    section "existing LAM Admins-login probe"
    ANAS_TEST_CONTAINER_PREFIX=$prefix \
      "$script_dir/server-lam-admins-e2e.sh"

    section "existing Nextcloud managed-local-admin probe"
    ANAS_TEST_REPO_ROOT=$repo_root \
    ANAS_TEST_WORKSPACE=$ANAS_TEST_WORKSPACE \
    ANAS_TEST_CONTAINER_PREFIX=$prefix \
    ANAS_TEST_DOCKER_SOCKET=$docker_socket \
      "$script_dir/server-nextcloud-local-admin-e2e.sh"
    ;;
esac

printf '\nPASS: %s domain-separation runtime probes completed (%s)\n' "$profile" "$probe_level"
