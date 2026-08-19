# Samba domain controller and DNS technical implementation

This page records the current implementation, security boundaries, and verification entry points for `samba_dc`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `4.23.6-r8` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `lego` | Module | — |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_samba_dc` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc:4.23.6-r8` | `` | 3 |
| `anas_samba_dc_anchor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc-anchor:4.23.6-r8` | `` | 3 |
| `anas_samba_dc_events_init` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-ubuntu:resolute-678c6550cc43` | `` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `samba_dc.admin_complex_pass` | bool | — | `true` | `static` | `SAMBA_DC_ADMIN_COMPLEX_PASS` | no | no | no | yes | `hot_reload` | Administrator password complexity policy |
| `samba_dc.admin_lockout_duration` | int | — | `30` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_DURATION` | no | no | no | yes | `hot_reload` | Administrator lockout duration |
| `samba_dc.admin_lockout_reset_after` | int | — | `30` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_RESET_AFTER` | no | no | no | yes | `hot_reload` | Administrator failed-attempt reset interval |
| `samba_dc.admin_lockout_threshold` | int | — | `10` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_THRESHOLD` | no | no | no | yes | `hot_reload` | Administrator lockout threshold |
| `samba_dc.admin_max_pass_age` | int | — | `0` | `static` | `SAMBA_DC_ADMIN_MAX_PASS_AGE` | no | no | no | yes | `hot_reload` | Administrator maximum password age; 0 means never expires |
| `samba_dc.admin_min_pass_age` | int | — | `1` | `static` | `SAMBA_DC_ADMIN_MIN_PASS_AGE` | no | no | no | yes | `hot_reload` | Administrator minimum password age |
| `samba_dc.admin_min_pass_length` | int | — | `8` | `static` | `SAMBA_DC_ADMIN_MIN_PASS_LENGTH` | no | no | no | yes | `hot_reload` | Administrator minimum password length |
| `samba_dc.admin_name` | string | — | `admin` | `static` | `SAMBA_DC_ADMIN_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.admin_password` | string | — | — | `generated` | `SAMBA_DC_ADMIN_PASSWORD` | no | yes | yes | no: `rotate-samba-admin-password` | `credential_rotate` | The existing AD account must be updated explicitly. |
| `samba_dc.admin_password_history` | int | — | `2` | `static` | `SAMBA_DC_ADMIN_PASSWORD_HISTORY` | no | no | no | yes | `hot_reload` | Administrator password history length |
| `samba_dc.administrator_password` | string | — | — | `generated` | `SAMBA_DC_ADMINISTRATOR_PASSWORD` | no | yes | yes | no: `rotate-samba-administrator-password` | `credential_rotate` | The provision password is not reapplied after initialization. |
| `samba_dc.anchor_bind_name` | string | — | `svc_anchor` | `static` | `SAMBA_DC_ANCHOR_BIND_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.anchor_bind_password` | string | — | — | `generated` | `SAMBA_DC_ANCHOR_BIND_PASSWORD` | no | yes | yes | no: `rotate-anchor-bind-password` | `credential_rotate` | Update the AD service account and Anchor Worker as one transaction. |
| `samba_dc.anchor_scan_interval` | int | — | `300` | `static` | `SAMBA_DC_ANCHOR_SCAN_INTERVAL` | no | no | no | yes | `container_recreate` | The reconciliation interval is consumed by the Anchor Worker process. |
| `samba_dc.app_filter` | bool | — | `true` | `static` | `SAMBA_DC_APP_FILTER` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.application_dns_mode` | enum (`auto`, `ad_zone`, `separate_zone`) | — | `auto` | `static` | `SAMBA_DC_APPLICATION_DNS_MODE` | no | no | no | no: `migrate-application-dns-zone` | `data_migrate` | Application-DNS authoritative-zone mode. |
| `samba_dc.create_structure` | bool | — | `true` | `static` | `SAMBA_DC_CREATE_STRUCTURE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.dns_allowed_networks` | string | — | `""` | `static` | `SAMBA_DC_DNS_ALLOWED_NETWORKS` | no | no | no | yes | `container_recreate` | BIND recursion and cache access are restricted to these networks. |
| `samba_dc.dns_cache_size` | string | — | `128M` | `static` | `SAMBA_DC_DNS_CACHE_SIZE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.dns_debug` | bool | — | `false` | `static` | `SAMBA_DC_DNS_DEBUG` | no | no | no | yes | `container_recreate` | The named process debug level is selected when the container starts. |
| `samba_dc.dns_forwarders` | string | — | `""` | `static` | `SAMBA_DC_DNS_FORWARDERS` | no | no | no | yes | `container_recreate` | The generated BIND configuration is installed during container initialization. |
| `samba_dc.domain` | string | `format: dns_name` | — | `inherited` | `SAMBA_DC_DOMAIN` | no | yes | no | no: `replace-directory-domain` | `immutable` | AD DNS domain (directory identity). |
| `samba_dc.ldap_bind_name` | string | — | `svc_ldap` | `static` | `SAMBA_DC_LDAP_BIND_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.ldap_bind_password` | string | — | — | `generated` | `SAMBA_DC_LDAP_BIND_PASSWORD` | no | yes | yes | no: `rotate-ldap-bind-password` | `credential_rotate` | Update AD and all LDAP consumers as one transaction. |
| `samba_dc.log_level` | string | — | `1` | `static` | `SAMBA_DC_LOG_LEVEL` | no | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_dc.max_log_size` | int | `>= 1` | `2048` | `static` | `SAMBA_DC_MAX_LOG_SIZE` | no | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_dc.netbios_name` | string | — | — | `runtime` | `SAMBA_DC_NETBIOS_NAME` | no | yes | no | no: `replace-domain-controller` | `immutable` | Changing the DC machine identity requires a controlled replacement. |
| `samba_dc.password_bind_name` | string | — | `svc_password` | `static` | `SAMBA_DC_PASSWORD_BIND_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.password_bind_password` | string | — | — | `generated` | `SAMBA_DC_PASSWORD_BIND_PASSWORD` | no | yes | yes | no: `rotate-password-bind-password` | `credential_rotate` | Update AD and password-capable applications as one transaction. |
| `samba_dc.realm` | string | — | — | `inherited` | `SAMBA_DC_REALM` | no | yes | no | no: `replace-directory-domain` | `immutable` | Realm derived from the AD DNS domain. |
| `samba_dc.template_homedir` | string | — | `/home/%D/%U` | `static` | `SAMBA_DC_TEMPLATE_HOMEDIR` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.template_shell` | string | — | `/bin/false` | `static` | `SAMBA_DC_TEMPLATE_SHELL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.user_complex_pass` | bool | — | `false` | `static` | `SAMBA_DC_USER_COMPLEX_PASS` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_lockout_duration` | int | — | `30` | `static` | `SAMBA_DC_USER_LOCKOUT_DURATION` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_lockout_reset_after` | int | — | `30` | `static` | `SAMBA_DC_USER_LOCKOUT_RESET_AFTER` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_lockout_threshold` | int | — | `10` | `static` | `SAMBA_DC_USER_LOCKOUT_THRESHOLD` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_max_pass_age` | int | — | `90` | `static` | `SAMBA_DC_USER_MAX_PASS_AGE` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_min_pass_age` | int | — | `1` | `static` | `SAMBA_DC_USER_MIN_PASS_AGE` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_min_pass_length` | int | — | `8` | `static` | `SAMBA_DC_USER_MIN_PASS_LENGTH` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_password_history` | int | — | `2` | `static` | `SAMBA_DC_USER_PASSWORD_HISTORY` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Dual domains and the application-DNS plan

`BASE_DOMAIN` belongs only to the application/Web namespace.
`SAMBA_DC_DOMAIN` belongs to the provisioned directory identity and derives
`SAMBA_DC_REALM`, `SAMBA_DC_BASE_DN`, `SAMBA_DC_DNS_SEARCH`,
`SAMBA_DC_DC_DOMAIN`, and the default UPN suffix. When
`modules.samba_dc.config.domain` is absent, `validateDomainDNSConfig` falls
back to `BASE_DOMAIN` for old configurations. That compatibility path does not
permit renaming an existing directory. An explicit `realm` must equal
`upper(SAMBA_DC_DOMAIN)` case-insensitively; calculate publishes the uppercase
realm.

The requested `application_dns_mode` and its resolved result are separate
state:

| Requested | Validation and resolution | Resolved zone |
| --- | --- | --- |
| `auto` | Resolve to `ad_zone` when `BASE_DOMAIN == SAMBA_DC_DOMAIN` or the former is a label-boundary subdomain of the latter; otherwise use `separate_zone` | AD or application domain |
| `ad_zone` | Accept only the equal/label-subdomain relationship | `SAMBA_DC_DOMAIN` |
| `separate_zone` | No relationship is required except that identical domains must use the existing AD zone; the application domain must be a dedicated internal namespace ANAS can maintain completely | `BASE_DOMAIN` |

The validate hook writes `requested_mode`, `resolved_mode`, and `zone` to the
non-sensitive module plan. Text `anas plan` output includes
`module plan: samba_dc ...`; JSON exposes `module_plans.samba_dc`. After a
deployment is materialized, the same data is persisted under
`modules.samba_dc.validation_plan`. Calculate also publishes
`SAMBA_DC_APPLICATION_DNS_MODE`, `SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED`, and
`SAMBA_DC_APPLICATION_DNS_ZONE`, so the DNS reconciler never re-infers the
domain relationship at runtime.

Runner-to-reconciler `DOMAINS` is an internal protocol. It collects only Web
modules declaring `features.domain: true` and uses
`inner/<complete FQDN>/<module>`, for example
`inner/cloud.nas.example.net/nextcloud`. It neither includes
`SAMBA_DC_DOMAIN` nor truncates an FQDN to its first label. `ad_zone` derives a
multi-label owner such as `cloud.nas`; `separate_zone` derives the short owner
`cloud` inside the application zone. These Web A records point to `HOST_IP`.

The LDAPS service alias uses a separate record. For compatibility it remains
`SAMBA_DC_HOST=BASE_DOMAIN` and resolves to `SAMBA_DC_HOST_IP`. The Web
certificate covers it for ANAS LDAP consumers, but it does not participate in
the realm, Base DN, SRV records, or canonical DC FQDN. `SAMBA_DC_HOST_IP` may
differ from the `HOST_IP` used by Web records, so this alias must not be put
back into `DOMAINS`.

The service alias overlaps a Samba-native A record in two cases: it is the AD
zone apex when both domains are equal, or it is the canonical DC name when
`SAMBA_DC_HOST == SAMBA_DC_DC_DOMAIN` (for example server name `nas`, AD domain
`test.example`, and application domain `nas.test.example`). ANAS only verifies
that either directory-native record contains the exact `SAMBA_DC_HOST_IP`; it
does not claim, add, replace, or delete it. During upgrade, ownership wrongly
recorded by an earlier reconciler is released without deleting the native
record. Other exact-target records whose creation provenance cannot be proven
remain non-deletable legacy observations.

Zone-inventory RPC or parse failures fail closed. Before any DNS mutation, the
reconciler checks every `SAMBA_DC_HOST` and `DOMAINS` FQDN for a closer child
zone that would intercept it. An A record may be replaced or deleted only when
its provenance is present in the committed manifest or durable pending journal;
an explicit write failure withdraws pending ownership instead of promoting a
concurrent administrator-created target to ANAS ownership.

Samba is internally authoritative for a `separate_zone`; names absent from the
managed record inventory are not forwarded to public DNS. The selected zone
and managed records are persisted with Samba state so an unmigrated zone drift
is rejected. The existing-workspace `migrate-service-domain` and
`migrate-application-dns-zone` migrators have not been delivered; only a new
workspace may select separated domains before first provision.
`SAMBA_DC_DOMAIN` itself cannot be renamed in place. A change requires a new
directory plus identity and member migration.

## Password-policy implementation

`structure.sh` passes `SAMBA_DC_USER_*` to `samba-tool domain passwordsettings set` for the ordinary-user domain policy. It generates or updates the administrator `pso_privileged` from `SAMBA_DC_ADMIN_*` and applies that PSO to built-in `Administrator` and the `Admins` group. Complexity, length, history, minimum/maximum age, and all three lockout settings come from module parameters; the script contains no policy constants.

All 16 policy parameters declare `hot_reload`/`samba-password-policy`. The current executor still uses the deployment apply fallback; container initialization idempotently updates the domain policy and any existing PSO.

## Identity and authorization data flow

It is authoritative for people, groups, service identities, and identity anchors. Use LAM or Samba tools to create/disable users and manage groups. `svc_ldap` is read-only, `svc_password` may change ordinary-user passwords only in its delegated scope, and `svc_anchor` manages identity anchors.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | provider: AD / ldaps / kerberos (`users, groups, service accounts`) |
| IAM | unsupported/not applicable |
| Group | authoritative group source |
| Directory password writeback | authoritative directory; controlled by ACLs and password policy |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

`admin_name` defines the routine directory administrator. Built-in RID 500 `Administrator` is reserved for provisioning and low-level recovery. Their independent passwords are not `anas admin local` accounts.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `SAMBA_DC_ADMINISTRATOR_PASSWORD`
- `SAMBA_DC_ADMIN_PASSWORD`
- `SAMBA_DC_ANCHOR_BIND_PASSWORD`
- `SAMBA_DC_LDAP_BIND_PASSWORD`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `ANAS_DIRECTORY_EVENTS_DIR`
- `ANAS_DIRECTORY_EVENTS_FILE_NAME`

### Explicit consumes

- `DOMAINS`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `ANAS_TLS_TRUST_BUNDLE_NAME`
- `LEGO_CA_CERT_NAME`
- `LEGO_CERTS_PATH`
- `LEGO_CERT_NAME`
- `LEGO_KEY_NAME`
- `ANAS_IDENTITY_APP_CLIENTS`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- [`zone_script_test.go`](../hook/zone_script_test.go)
- [`domain_dns.go`](../hook/domain_dns.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

`domain`, `realm`, and `netbios_name` are immutable identities; password
parameters require dedicated credential rotation and cannot be changed by
recreating containers. The service-domain/application-zone migrators for
existing workspaces have not been delivered. Ordinary apply cannot switch the
zone, and an AD domain cannot be renamed in place.
