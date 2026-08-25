# Module environment variable contract

This page records the environment variables each module hook currently produces
and the ownership of variables shared across modules. Values are ultimately
written into each module's own `.env`; a sensitive value may cross a module
boundary only after it is declared explicitly in `config.consumes` in
`module.yml`.

## Identity variables produced by the runner

These variables do not belong to any one application implementation. The runner
produces them uniformly from the enabled modules, `identity.interfaces`, and the
final IAM binding. They use an explicit consumption scope: only a module that
declares the corresponding variable in `config.consumes` receives it in its own
`.env`, and no module gets the whole identity topology by default.

| Variable | Meaning |
| --- | --- |
| `ANAS_IDENTITY_CLIENTS` | The union of modules using any identity protocol |
| `ANAS_IDENTITY_APP_CLIENTS` | Applications declaring `identity.application_group: true` that need an `APP_<module>` group |
| `ANAS_IDENTITY_LDAPS_CLIENTS` | Modules using LDAPS directly |
| `ANAS_IDENTITY_OIDC_CLIENTS` | IAM consumers finally bound to OIDC |
| `ANAS_IDENTITY_SAML_CLIENTS` | IAM consumers finally bound to SAML |
| `ANAS_IDENTITY_CLIENT__<MODULE>__INTERFACES` | The protocol list one module uses, for example `ldaps,saml` |
| `ANAS_IAM_PROVIDER` | The IAM provider this deployment selected |
| `ANAS_IAM_INTERFACES` | The IAM protocols the provider serves |
| `ANAS_IAM_BINDING__<MODULE>__INTERFACE` | The IAM protocol one consumer finally selected |

The old `USE_LDAP_MODS_NAME`, `ANAS_IAM_CLIENTS`, `ANAS_IAM_OIDC_CLIENTS`, and
`ANAS_IAM_SAML_CLIENTS` have been removed, with no compatibility aliases.

## Host and global variables produced by the runner

- Host and network: `HOST_IP`, `HOST_SUBNET_MASK`, `HOST_DNS_SERVER`,
  `DEFAULT_GATEWAY_IP`, `VLAN_GATEWAY_IP`, `INTERFACE`, `LOCAL_DNS_SERVER`.
- macvlan address planning: `HOST_SEGMENT`, `HOST_LAN_IP`, `VLAN_SEGMENT`,
  `VLAN_SUBNET_MASK`, `VLAN_BRIDGE_IP`, `VLAN_BRIDGE_INTERFACE`,
  `VLAN_INTERFACE`. Computed only when a module with
  `features.host_lan: required` is present.
  - `HOST_LAN_IP` is the container's address on the LAN, and `VLAN_BRIDGE_IP` is
    the address of the host-side bridge interface. Both can be set with
    `global.host_lan_ip` / `global.host_lan_bridge_ip`; when unset they are taken
    automatically from the top of the address pool (the bridge takes the first,
    the container the second).
  - `VLAN_SEGMENT` is published only when neither is set; it is
    `docker network create --ip-range`. Once the container address is set that
    range has nothing left to constrain, and continuing to pass it would make
    Docker reject a static address outside it.
  - The address pool is needed only for automatic allocation, so the restriction
    against a host prefix narrower than /28 also applies only to automatic
    allocation: with addresses set explicitly, /29 and /30 deploy just as well.
- Paths and names: `SERVER_NAME`.
- `DATA_PATH` (application state) and `USER_DATA_PATH` (user files) come from the
  workspace layout; `BASE_DOMAIN`, `EMAIL`, `TZ`, and the container/image/network
  prefixes come from the global configuration and are not any one application
  module's private output. `BASE_DOMAIN` denotes only the application/web
  namespace, and no longer defines the Samba AD realm, base DN, or machine trust.

## Application DNS topology produced by the runner

`DOMAINS` is an internal protocol from the runner to the Samba zone reconciler,
owned by `runner`, and only `samba_dc`, which consumes it explicitly through
`config.consumes`, receives it. The runner collects only modules declaring
`features.domain: true` and preserves the full FQDN:

```text
inner/cloud.nas.example.net/nextcloud,inner/auth.nas.example.net/authentik
```

The protocol does not include `SAMBA_DC_DOMAIN`, and does not use the old
"keep only the first label" meaning. The reconciler computes the relative owner
from `SAMBA_DC_APPLICATION_DNS_ZONE` and points these web A records at
`HOST_IP`. `DOMAINS` is derived and cannot be configured through the top-level
`env:`.

## lego

- Private certificate variables: `LEGO_DATA_PATH`, `LEGO_CERTS_PATH`,
  `LEGO_CERTS_USER1000_PATH`, `LEGO_CERT_NAME`, `LEGO_KEY_NAME`,
  `LEGO_CA_CERT_NAME`, `LEGO_EMAIL`.
- The outward certificate contract: `ANAS_TLS_CERTS_DIR`, `ANAS_TLS_CERT_NAME`,
  `ANAS_TLS_KEY_NAME`, `ANAS_TLS_ISSUER_NAME`, `ANAS_TLS_INTERNAL_CA_NAME`.

Other modules should read `ANAS_TLS_*`, and must not read `LEGO_*` to determine
the certificate implementation.

## samba_dc

- Directory domain and host: `SAMBA_DC_DOMAIN` comes from
  `modules.samba_dc.config.domain` and derives `SAMBA_DC_REALM`,
  `SAMBA_DC_WORKGROUP`, `SAMBA_DC_NETBIOS_NAME`, `SAMBA_DC_DC_NAME`,
  `SAMBA_DC_DC_DOMAIN`, and `SAMBA_DC_INTERFACES`. When an old configuration does
  not set `domain`, the effective value falls back to `BASE_DOMAIN`; changing the
  domain in place after provisioning is not supported.
- Application DNS plan: `SAMBA_DC_APPLICATION_DNS_MODE` holds the requested
  value, `SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED` holds the
  `ad_zone`/`separate_zone` resolution, and `SAMBA_DC_APPLICATION_DNS_ZONE` holds
  the final authoritative zone. All three also appear in `module_plans.samba_dc`
  and are frozen into the deployment's `validation_plan`.
- DNS/LDAPS: `SAMBA_DC_DNS_SERVER`, `SAMBA_DC_DNS_SEARCH`,
  `SAMBA_DC_DNS_FORWARDERS`, `SAMBA_DC_DNS_ALLOWED_NETWORKS`,
  `SAMBA_DC_DNS_CACHE_SIZE`, `SAMBA_DC_LDAPS_SERVER_URL`,
  `SAMBA_DC_LDAPS_SERVER_URL_PORT`, `SAMBA_DC_LDAPS_PORT`.
  `SAMBA_DC_DNS_SEARCH` always uses the AD domain.
- TLS service alias: `SAMBA_DC_HOST` stays at `BASE_DOMAIN` during the
  compatibility period, and its independently managed A record points at
  `SAMBA_DC_HOST_IP`; it is only for the LDAPS endpoint the certificate covers,
  and is neither the realm nor the canonical DC FQDN. Web `DOMAINS` records use
  `HOST_IP`, and the two kinds of target must not be mixed.
- Directory roots: `SAMBA_DC_BASE_DN`, `SAMBA_DC_BASE_USERS_DN_PREFIX`,
  `SAMBA_DC_BASE_USERS_DN`, `SAMBA_DC_BASE_GROUPS_DN_PREFIX`,
  `SAMBA_DC_BASE_GROUPS_DN`, `SAMBA_DC_BASE_GROUPS_ROLE_DN`,
  `SAMBA_DC_BASE_APP_DN`, `SAMBA_DC_BASE_ADMINS_DN`,
  `SAMBA_DC_BASE_SERVICE_ACCOUNTS_DN`, `SAMBA_DC_BASE_COMPUTERS_DN`.
- Administrators: `SAMBA_DC_ADMIN_NAME`, `SAMBA_DC_ADMIN_DN`,
  `SAMBA_DC_ADMIN_PASSWORD`, `SAMBA_DC_ADMIN_GROUP_NAME`,
  `SAMBA_DC_ADMIN_GROUP_DN`, `SAMBA_DC_ADMINISTRATOR_NAME`,
  `SAMBA_DC_ADMINISTRATOR_DN`, `SAMBA_DC_ADMINISTRATOR_PASSWORD`. The two
  passwords come from their own parameters, and when omitted two different
  secrets are generated.
- Service binds: `SAMBA_DC_LDAP_BIND_DN`, `SAMBA_DC_LDAP_BIND_PASSWORD`,
  `SAMBA_DC_PASSWORD_BIND_DN`, `SAMBA_DC_PASSWORD_BIND_PASSWORD`,
  `SAMBA_DC_ANCHOR_BIND_DN`, `SAMBA_DC_ANCHOR_BIND_PASSWORD`.
- Identity anchor: `SAMBA_DC_IDENTITY_ANCHOR_BINARY_ATTRIBUTE` is fixed to the
  binary `mS-DS-ConsistencyGuid`, and `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE` is
  fixed to the textual `anasIdentityAnchor` applications use;
  `SAMBA_DC_ANCHOR_USER_BASES` and `SAMBA_DC_ANCHOR_GROUP_BASES` control the
  forward scan scope, and `SAMBA_DC_ANCHOR_SCAN_INTERVAL` controls the catch-up
  interval.
- User/group attributes: `SAMBA_DC_USER_CLASS_NAME`,
  `SAMBA_DC_USER_CLASS_FILTER`, `SAMBA_DC_USER_ENABLED_FILTER`,
  `SAMBA_DC_USER_LOGIN_ATTRS`, `SAMBA_DC_USER_NAME`,
  `SAMBA_DC_USER_DISPLAY_NAME`, `SAMBA_DC_USER_EMAIL`,
  `SAMBA_DC_GROUP_CLASS_NAME`, `SAMBA_DC_GROUP_CLASS_FILTER`,
  `SAMBA_DC_GROUP_DISPLAY_NAME`, `SAMBA_DC_GROUP_MEMBER_ATTR`.
- Authorization groups: `SAMBA_DC_APP_ALL_NAME`, `SAMBA_DC_APP_ALL_DN`,
  `SAMBA_DC_FS_ADMIN_GROUP_NAME`, `SAMBA_DC_FS_SHARE_RW_GROUP_NAME`.
- Kerberos: `KRB5RCACHETYPE`.

## samba_fs

Produces `SAMBA_FS_NETBIOS_NAME`, `SAMBA_FS_ADMIN_USERS`,
`SAMBA_FS_SHARE_VALID_USERS`, `SAMBA_FS_SHARE_WRITE_LIST`,
`SAMBA_FS_SHARE_DOMAIN_USERS_ACL`, `SAMBA_FS_USE_DEFAULT_DOMAIN`, and
`SAMBA_FS_USERDATA_PATH`. It consumes the Samba DC's domain, DNS,
administrator, and group variables.

Member join uses `SAMBA_DC_DOMAIN`, `SAMBA_DC_REALM`, `SAMBA_DC_DNS_SEARCH`, and
`SAMBA_DC_DC_DOMAIN` explicitly, and derives nothing from `BASE_DOMAIN`. Changing
only the application domain must not make Samba FS leave and rejoin; changing an
already-provisioned AD domain cannot be done in place at all, and requires a new
directory and rejoining the members.

Both compose and the in-container resolver use `SAMBA_DC_DNS_SERVER` and
`SAMBA_DC_DNS_SEARCH` directly. Samba FS no longer produces or accepts a DNS
alias of its own, and does not fall back to the host/VLAN resolver. At startup, a
successful `net ads testjoin` reuses the existing trust; only an invalid trust
triggers a join, and there is no automatic leave path. When the DC is not ready
yet it waits and repeats `testjoin`, so a connectivity failure is not mistaken
for needing a rejoin. `SAMBA_DC_DNS_SERVER` is a numeric DC address, so
installing the resolver does not require resolving the DC name first and forms no
startup dependency cycle. After a successful join, `net ads testjoin` must pass
again and `net ads dns register -P` must succeed, or startup is blocked; the
`wbinfo -t` readiness check covers the same AD member-machine trust.

The file-sharing parameters `SHARE_DIR_NAME`, `SHARE_ACCESS_MODE`,
`SHARE_GUEST_READ_ONLY`, and `USE_DEFAULT_DOMAIN` belong to samba_fs but are
declared as bare names: they are what a user sees in a file manager, and they are
set in the configuration's top-level `env:` block. The share tree is mounted at a
fixed `/userdata` inside the container: that name is both the smb.conf share path
and the prefix of the guest ACL state file, and is not configurable. The host
path `SAMBA_FS_USERDATA_PATH` is derived from `${USER_DATA_PATH}/samba_fs` and is
**not** `DATA_PATH` — user files belong under `<workspace>/userdata`, while
`<workspace>/data` is replaced wholesale by a restore. To put these files on
another disk, mount that disk at `<workspace>/userdata`: one mount point covers
every module's user content. There is no per-module path override, because that
would let one module's files end up somewhere neither snapshots nor backups know
about while looking like an ordinary setting.

## postgres

Produces `POSTGRES_HOST`, `POSTGRES_PORT`, `POSTGRES_HOST_PORT`,
`POSTGRES_NETWORK_NAME`, `POSTGRES_USER`, `POSTGRES_USERNAME`,
`POSTGRES_PASSWORD`, `POSTGRES_ADMINER_DOMAIN_PREFIX`,
`POSTGRES_ADMINER_DOMAIN`.

`POSTGRES_PASSWORD` is a generated secret; a database consumer must declare it
explicitly in its own `config.consumes`.

## mariadb

Produces `MARIADB_HOST`, `MARIADB_PORT`, `MARIADB_HOST_PORT`,
`MARIADB_NETWORK_NAME`, `MARIADB_USERNAME`, `MARIADB_PASSWORD`,
`MARIADB_ROOT_PASSWORD`, `MARIADB_ADMINER_DOMAIN_PREFIX`,
`MARIADB_ADMINER_DOMAIN`, and publishes the compatibility runtime aliases
`MYSQL_HOST`, `MYSQL_PORT`, `MYSQL_USERNAME`, `MYSQL_PASSWORD`.

## traefik

Produces no new cross-module environment variables. It consumes the global
domain, ports, network prefix, BasicAuth, and the `ANAS_TLS_*` certificate
contract.

## authentik

- Service endpoints: `AUTHENTIK_DOMAIN`, `AUTHENTIK_DOMAIN_PORT`,
  `AUTHENTIK_DOMAIN_FULL`, `ANAS_IAM_PORTAL_URL`.
- PostgreSQL: `AUTHENTIK_NETWORK_DB`, `AUTHENTIK_POSTGRESQL__HOST`,
  `AUTHENTIK_POSTGRESQL__PORT`, `AUTHENTIK_POSTGRESQL__USER`,
  `AUTHENTIK_POSTGRESQL__PASSWORD`, `AUTHENTIK_POSTGRESQL__NAME`.
- Samba AD source: `AUTHENTIK_LDAP_SERVER_URI`, `AUTHENTIK_LDAP_BIND_DN`,
  `AUTHENTIK_LDAP_BIND_PASSWORD`, `AUTHENTIK_LDAP_BASE_DN`,
  `AUTHENTIK_LDAP_ADDITIONAL_USER_DN`, `AUTHENTIK_LDAP_ADDITIONAL_GROUP_DN`,
  `AUTHENTIK_LDAP_USER_OBJECT_FILTER`, `AUTHENTIK_LDAP_GROUP_OBJECT_FILTER`,
  `AUTHENTIK_LDAP_GROUP_MEMBERSHIP_FIELD`,
  `AUTHENTIK_LDAP_USER_MEMBERSHIP_ATTRIBUTE`.
- Local runtime secrets: `AUTHENTIK_SECRET_KEY`, `AUTHENTIK_SIGNING_KEY`,
  `AUTHENTIK_SIGNING_CERT`.
- The managed recovery account: account id `break_glass`, fixed username
  `akadmin`. The runner supplies
  `AUTHENTIK_LOCAL_ADMIN__BREAK_GLASS_PASSWORD` to the hook only temporarily, and
  the deployment environment keeps only the
  `AUTHENTIK_LOCAL_ADMIN__BREAK_GLASS_PASSWORD_FILE` path and the username. The
  container entrypoint temporarily exports the upstream
  `AUTHENTIK_BOOTSTRAP_PASSWORD` from that `0600` file, and an existing
  installation is updated by the `ak shell` handler.
- Non-sensitive bootstrap variables: `AUTHENTIK_BOOTSTRAP_EMAIL`.
- Per-application IAM endpoints: `ANAS_IAM_BINDING__<MODULE>__OIDC_*` or
  `ANAS_IAM_BINDING__<MODULE>__SAML_*`.
- Application session logout: consumes
  `ANAS_IAM_CLIENT__<MODULE>__OIDC_LOGOUT_*` and `SAML_SLS_*`, and generates
  Authentik `logout_uri/logout_method` or `sls_*` blueprint fields.

Every LDAP variable is computed from `SAMBA_DC_*`, and blueprints read
`AUTHENTIK_LDAP_*` only through `!Env`, storing no deployment DN or password.
The Authentik worker imports `anas-samba-ad-ca` from
`ANAS_TLS_CERTS_DIR/ANAS_TLS_INTERNAL_CA_NAME`, and the LDAP source validates the
Samba DC's LDAPS certificate through `peer_certificate` and SNI; an empty
certificate field must not be relied on, because Authentik skips validation in
that state.

## llng

- Database aliases: `DB_HOST`, `DB_POST`, `DB_USER`, `DB_PASSWORD`.
- Portal: `ANAS_IAM_PORTAL_URL`.
- Dynamic client inventory: `OIDC_RP_APPS`, `SAML_SP_APPS`.
- Per-application private configuration: `OIDC_RP__<MODULE>__*`,
  `SAML_SP__<MODULE>__*`.
- Per-application common endpoints: `ANAS_IAM_BINDING__<MODULE>__OIDC_*`,
  `ANAS_IAM_BINDING__<MODULE>__SAML_*`.
- OIDC logout private mapping: `OIDC_RP__<MODULE>__LOGOUT_URI`, `LOGOUT_TYPE`,
  `LOGOUT_SESSION_REQUIRED`; SAML SLS continues to be imported from
  `SAML_SP__<MODULE>__METADATA_URL`.

LLNG reads the final protocol inventory from `ANAS_IDENTITY_OIDC_CLIENTS` and
`ANAS_IDENTITY_SAML_CLIENTS`.

## nextcloud

- Addresses and containers: `NEXTCLOUD_DOMAIN`, `NEXTCLOUD_DOMAIN_PORT`,
  `NEXTCLOUD_DOMAIN_FULL`, `NEXTCLOUD_HOSTNAME`, `NEXTCLOUD_PUSH_HOSTNAME`,
  `NEXTCLOUD_BASE_PATH`, `NEXTCLOUD_DATA_DIR`, `NEXTCLOUD_NETWORK_DB`.
- The recovery administrator: `NEXTCLOUD_ADMIN_USERNAME`,
  `NEXTCLOUD_ADMIN_USER`, and the path-only
  `NEXTCLOUD_LOCAL_ADMIN__BREAK_GLASS_PASSWORD_FILE`. Plaintext passwords do not
  enter the deployment `.env`; a first installation reads it through the official
  entrypoint's `NEXTCLOUD_ADMIN_PASSWORD_FILE`, and an existing installation is
  updated by the occ handler.
- LDAP: `NEXTCLOUD_USER_FILTER`, `NEXTCLOUD_USER_LOGIN_FILTER`,
  `NEXTCLOUD_USER_COMPLEX_PASS`.
- SAML: `NEXTCLOUD_SAML_IDP_*`, `NEXTCLOUD_SAML_SP_PRIVATE_KEY`,
  `NEXTCLOUD_SAML_SP_CERT`, `NEXTCLOUD_IAM_HOST`.
- Redis/Imaginary/Talk: `NEXTCLOUD_REDIS_*`, `NEXTCLOUD_IMAGINARY_*`,
  `NEXTCLOUD_TALK_*`, `TALK_SIGNALING_SECRET`.
- Image runtime aliases: `MYSQL_*`, `POSTGRES_*`, `REDIS_HOST`, `PHP_*`,
  `APACHE_BODY_LIMIT`, `OVERWRITE*`.
- Application registration: `ANAS_IAM_CLIENT__NEXTCLOUD__*`,
  `APPS_LIST__NEXTCLOUD__*`; of these, OIDC publishes
  `OIDC_LOGOUT_URI/METHODS/SESSION_REQUIRED` and SAML publishes
  `SAML_SLS_URL/BINDINGS`.

## meshcentral

Produces `MESHCENTRAL_DOMAIN`, `MESHCENTRAL_TITLE`, `MESHCENTRAL_SUBTITLE`,
`MESHCENTRAL_USER_FILTER`, `MESHCENTRAL_USER_LOGIN_FILTER`. It consumes MariaDB
and the Samba DC's LDAPS address, `svc_ldap` credentials, and user/group DNs and
attributes.

## vikunja

- Addresses and localization: `VIKUNJA_DOMAIN`, `VIKUNJA_DOMAIN_PORT`,
  `VIKUNJA_DOMAIN_FULL`, `VIKUNJA_SERVICE_PUBLICURL`, `VIKUNJA_LANGUAGE`,
  `VIKUNJA_SERVICE_TIMEZONE`, `VIKUNJA_DEFAULTSETTINGS_TIMEZONE`,
  `VIKUNJA_DEFAULTSETTINGS_LANGUAGE`.
- The database resource: the runner produces `VIKUNJA_DB_TYPE`,
  `VIKUNJA_DB_HOST`, `VIKUNJA_DB_PORT`, `VIKUNJA_DB_NAME`,
  `VIKUNJA_DB_USERNAME`, `VIKUNJA_DB_PASSWORD`, `VIKUNJA_NETWORK_DB`; the hook
  maps PostgreSQL to the upstream `postgres` and MariaDB to `mysql`, and produces
  `VIKUNJA_DATABASE_TYPE/HOST/DATABASE/USER/PASSWORD/SSLMODE/TLS`.
- OIDC: `VIKUNJA_OIDC_CLIENT_ID`, `VIKUNJA_OIDC_CLIENT_SECRET`,
  `VIKUNJA_AUTH_OPENID_*`, plus the provider registration contract
  `ANAS_IAM_CLIENT__VIKUNJA__*`. The provider id is fixed to `anas` and the
  callback path to `/auth/openid/anas`.
- Runtime secrets and storage: `VIKUNJA_SERVICE_SECRET`,
  `VIKUNJA_FILES_BASEPATH`. Local registration and local authentication are
  disabled through `VIKUNJA_SERVICE_ENABLEREGISTRATION=false` and
  `VIKUNJA_AUTH_LOCAL_ENABLED=false` respectively.
- Launcher: `APPS_LIST__VIKUNJA__*`. This module publishes no `OIDC_LOGOUT_*`
  receiver; Vikunja `2.4.0` offers only application-initiated RP-Initiated
  Logout, with no standard IAM→module front-channel or back-channel notification
  entry point.

## netbird

- Addresses: `NETBIRD_DOMAIN`, `NETBIRD_DOMAIN_PORT`, `NETBIRD_DOMAIN_FULL`,
  `NETBIRD_DASHBOARD_ENDPOINT`, `NETBIRD_MGMT_API_ENDPOINT`,
  `NETBIRD_MGMT_GRPC_API_ENDPOINT`, `NETBIRD_SIGNAL_ENDPOINT`,
  `NETBIRD_RELAY_ENDPOINT`.
- OIDC: `AUTH_AUDIENCE`, `AUTH_AUTHORITY`, `AUTH_CLIENT_ID`,
  `AUTH_CLIENT_SECRET`, `AUTH_SUPPORTED_SCOPES`, `NETBIRD_AUTH_AUTHORITY`,
  `NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT`, `NETBIRD_AUTH_USER_ID_CLAIM`.
- Secrets: `NETBIRD_DATASTORE_ENC_KEY`, `NETBIRD_RELAY_AUTH_SECRET`.
- IAM registration: `ANAS_IAM_CLIENT__NETBIRD__*`.
- Launcher: `APPS_LIST__NETBIRD__*`.

## lam

Produces `LAM_DOMAIN`, `LAM_LANGUAGE`, `LAM_ADMIN_PASSWORD`. LAM's main login
searches for the user DN through the read-only
`SAMBA_DC_LDAP_BIND_DN`/`SAMBA_DC_LDAP_BIND_PASSWORD` and admits only enabled
members of `SAMBA_DC_ADMIN_GROUP_DN`; users authenticate with their own
`sAMAccountName` and Samba directory password. `lam` is only the default server
profile name. `LAM_ADMIN_PASSWORD` protects only the LAM configuration/profile
editor, comes from LAM's own `admin_password` or an independently generated
secret, and is not the main login password.

## collabora

Produces `COLLABORA_ADMIN_USERNAME`, `COLLABORA_ADMIN_PASSWORD`,
`COLLABORA_ALIAS_GROUP`, `COLLABORA_EXTRA_PARAMS`, and consumes Nextcloud's
public domain. The default username is `admin_collabora`; the password comes only
from Collabora's own parameter or an independently generated secret.

## eturnal

Produces `ETURNAL_DOMAIN`, `TURN_DOMAIN`, `TURN_DOMAIN_PORT`, `TURN_HOSTNAME`,
and the generated secret `TURN_SECRET`. TURN consumers such as Nextcloud and
NetBird must declare `TURN_SECRET` explicitly.

## ddns

Produces `DDNS_DOMAIN`, `DDNS_DNS_SERVER`, `DDNS_CONFIG`,
`DDNS_IPV6_AVAILABLE`, and the runtime `DNS_PROVIDER`, and consumes the DNS
provider's user secret.

## freeradius

This module currently produces no hook environment variables and has neither a
completed directory user source nor RADIUS client configuration; it is still
scaffolding.

## Maintenance rules

1. When a hook adds, removes, or renames an output variable, this page must be
   updated with it.
2. A sensitive cross-module variable must be declared item by item in the
   consumer's `config.consumes`.
3. Implementation-private variables use the module prefix; cross-implementation
   contracts use `ANAS_*`.
4. The identity protocol inventory is produced by the runner alone, and modules
   must not append to or override it.
5. This page records variable names and semantics only; writing an actual
   password, token, or private key into it is forbidden.

## Scope: what one module receives

The rendered `.env` contains only what that module **declared**, not everything
that happens to exist in its dependency closure:

```
.env = globally owned keys
     + its own prefixed keys (<MODULE>_*, or bare names declared in config.exports)
     + cross-module keys declared explicitly in config.consumes
     + what the user wrote explicitly under env: / modules.<module>.config
     + what the runner injects (MODULE_NAME and so on)
```

The dependency closure determines startup order only. Depending on postgres does
not mean being handed all of postgres's variables — the closure answers "who might
be relevant", not "who actually needs this". Before the change, collabora's
container received 264 variables and used 19 of them; it now receives 49. The
whole deployment went from 2524 to 1142.

Because every module's compose declares `env_file: .env`, whatever is in `.env`
enters the container's process environment verbatim — appearing in
`docker inspect`, `/proc/<pid>/environ`, and crash dumps. So this is not merely a
tidiness question.

The consequence of failing to declare something is that the container receives an
empty value and fails visibly and immediately, rather than "nobody can tell that
too much was handed over". `test-env/scripts/test-env-scope.sh` renders twice
(old rules / new rules) and proves that no value reaching an application changed.

## Naming rules

- **Global parameters**: the env key is the parameter name uppercased. The only
  exception is `timezone` → `TZ` (an established container image convention).
- **Module parameters**: the env key is `<MODULE>_` plus the parameter name
  uppercased, unless declared as a bare name in `config.exports`.
- **The `ANAS_` prefix**: marks a cross-module contract key the runner derived
  (`ANAS_IAM_*`, `ANAS_TLS_*`, `ANAS_FORWARD_AUTH_*`, and so on), not a user
  setting.

The mapping is defined in exactly one place (`globalBindings` in
`internal/config`), and tests guarantee it is a bijection — two parameters cannot
map to the same key.

`anas config list [global|<module>]` prints each parameter's path, env key,
default, current value, and change effect.
