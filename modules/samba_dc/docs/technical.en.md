# Samba domain controller and DNS technical implementation

This page records the current implementation, security boundaries, and verification entry points for `samba_dc`. User instructions are in the [English README](../README.en.md).

> Status: current implementation; based on `4.23.6-r6` / `anas.module/v1`.

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `lego` | Module | — |

## Compose topology

| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_samba_dc` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc:4.23.6-r6` | `` | 3 |
| `anas_samba_dc_anchor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc-anchor:4.23.6-r6` | `` | 3 |
| `anas_samba_dc_events_init` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-ubuntu:resolute-678c6550cc43` | `` | 1 |

## Configuration contract

| Path | Type | Default | Environment | Required | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `samba_dc.admin_complex_pass` | string | `true` | `SAMBA_DC_ADMIN_COMPLEX_PASS` | no | no | yes | `hot_reload` | Administrator password complexity policy |
| `samba_dc.admin_lockout_duration` | string | `30` | `SAMBA_DC_ADMIN_LOCKOUT_DURATION` | no | no | yes | `hot_reload` | Administrator lockout duration |
| `samba_dc.admin_lockout_reset_after` | string | `30` | `SAMBA_DC_ADMIN_LOCKOUT_RESET_AFTER` | no | no | yes | `hot_reload` | Administrator failed-attempt reset interval |
| `samba_dc.admin_lockout_threshold` | string | `10` | `SAMBA_DC_ADMIN_LOCKOUT_THRESHOLD` | no | no | yes | `hot_reload` | Administrator lockout threshold |
| `samba_dc.admin_max_pass_age` | string | `0` | `SAMBA_DC_ADMIN_MAX_PASS_AGE` | no | no | yes | `hot_reload` | Administrator maximum password age; 0 means never expires |
| `samba_dc.admin_min_pass_age` | string | `1` | `SAMBA_DC_ADMIN_MIN_PASS_AGE` | no | no | yes | `hot_reload` | Administrator minimum password age |
| `samba_dc.admin_min_pass_length` | string | `8` | `SAMBA_DC_ADMIN_MIN_PASS_LENGTH` | no | no | yes | `hot_reload` | Administrator minimum password length |
| `samba_dc.admin_password_history` | string | `2` | `SAMBA_DC_ADMIN_PASSWORD_HISTORY` | no | no | yes | `hot_reload` | Administrator password history length |
| `samba_dc.admin_name` | string | `admin` | `SAMBA_DC_ADMIN_NAME` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.admin_password` | string | `—` | `SAMBA_DC_ADMIN_PASSWORD` | no | yes | no: `rotate-samba-admin-password` | `credential_rotate` | The existing AD account must be updated explicitly. |
| `samba_dc.administrator_password` | string | `—` | `SAMBA_DC_ADMINISTRATOR_PASSWORD` | no | yes | no: `rotate-samba-administrator-password` | `credential_rotate` | The provision password is not reapplied after initialization. |
| `samba_dc.anchor_bind_name` | string | `svc_anchor` | `SAMBA_DC_ANCHOR_BIND_NAME` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.anchor_bind_password` | string | `—` | `SAMBA_DC_ANCHOR_BIND_PASSWORD` | no | yes | no: `rotate-anchor-bind-password` | `credential_rotate` | Update the AD service account and Anchor Worker as one transaction. |
| `samba_dc.anchor_scan_interval` | string | `300` | `SAMBA_DC_ANCHOR_SCAN_INTERVAL` | no | no | yes | `container_recreate` | The reconciliation interval is consumed by the Anchor Worker process. |
| `samba_dc.app_filter` | string | `true` | `SAMBA_DC_APP_FILTER` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.create_structure` | string | `true` | `SAMBA_DC_CREATE_STRUCTURE` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.dns_allowed_networks` | string | `—` | `SAMBA_DC_DNS_ALLOWED_NETWORKS` | no | no | yes | `container_recreate` | BIND recursion and cache access are restricted to these networks. |
| `samba_dc.dns_cache_size` | string | `128M` | `SAMBA_DC_DNS_CACHE_SIZE` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.dns_debug` | string | `false` | `SAMBA_DC_DNS_DEBUG` | no | no | yes | `container_recreate` | The named process debug level is selected when the container starts. |
| `samba_dc.dns_forwarders` | string | `—` | `SAMBA_DC_DNS_FORWARDERS` | no | no | yes | `container_recreate` | The generated BIND configuration is installed during container initialization. |
| `samba_dc.ldap_bind_name` | string | `svc_ldap` | `SAMBA_DC_LDAP_BIND_NAME` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.ldap_bind_password` | string | `—` | `SAMBA_DC_LDAP_BIND_PASSWORD` | no | yes | no: `rotate-ldap-bind-password` | `credential_rotate` | Update AD and all LDAP consumers as one transaction. |
| `samba_dc.log_level` | string | `1` | `SAMBA_DC_LOG_LEVEL` | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_dc.max_log_size` | string | `2048` | `SAMBA_DC_MAX_LOG_SIZE` | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_dc.netbios_name` | string | `—` | `SAMBA_DC_NETBIOS_NAME` | no | no | no: `replace-domain-controller` | `immutable` | Changing the DC machine identity requires a controlled replacement. |
| `samba_dc.password_bind_name` | string | `svc_password` | `SAMBA_DC_PASSWORD_BIND_NAME` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.password_bind_password` | string | `—` | `SAMBA_DC_PASSWORD_BIND_PASSWORD` | no | yes | no: `rotate-password-bind-password` | `credential_rotate` | Update AD and password-capable applications as one transaction. |
| `samba_dc.realm` | string | `—` | `SAMBA_DC_REALM` | no | no | no: `migrate-domain` | `immutable` | The realm is part of the provisioned AD identity. |
| `samba_dc.template_homedir` | string | `/home/%D/%U` | `SAMBA_DC_TEMPLATE_HOMEDIR` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.template_shell` | string | `/bin/false` | `SAMBA_DC_TEMPLATE_SHELL` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.user_complex_pass` | string | `false` | `SAMBA_DC_USER_COMPLEX_PASS` | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_lockout_duration` | string | `30` | `SAMBA_DC_USER_LOCKOUT_DURATION` | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_lockout_reset_after` | string | `30` | `SAMBA_DC_USER_LOCKOUT_RESET_AFTER` | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_lockout_threshold` | string | `10` | `SAMBA_DC_USER_LOCKOUT_THRESHOLD` | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_max_pass_age` | string | `90` | `SAMBA_DC_USER_MAX_PASS_AGE` | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_min_pass_age` | string | `1` | `SAMBA_DC_USER_MIN_PASS_AGE` | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_min_pass_length` | string | `8` | `SAMBA_DC_USER_MIN_PASS_LENGTH` | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_password_history` | string | `2` | `SAMBA_DC_USER_PASSWORD_HISTORY` | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

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
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

`realm` and `netbios_name` are immutable identities; password parameters require dedicated credential rotation and cannot be changed by recreating containers.
