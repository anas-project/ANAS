# Samba domain controller and DNS

Active Directory, LDAPS, Kerberos, and BIND9-DLZ DNS provider.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `samba_dc` |
| Version / revision | `4.23.6-r7` |
| Status | `release` |
| Category | `identity` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `lego` | Module | — |

## Minimal configuration

```yaml
modules:
  samba_dc: {}
```

## Identity, users, and groups

It is authoritative for people, groups, service identities, and identity anchors. Use LAM or Samba tools to create/disable users and manage groups. `svc_ldap` is read-only, `svc_password` may change ordinary-user passwords only in its delegated scope, and `svc_anchor` manages identity anchors.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | provider: AD / ldaps / kerberos (`users, groups, service accounts`) |
| IAM | unsupported/not applicable |
| Group | authoritative group source |
| Directory password writeback | authoritative directory; controlled by ACLs and password policy |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

`admin_name` defines the routine directory administrator. Built-in RID 500 `Administrator` is reserved for provisioning and low-level recovery. Their independent passwords are not `anas admin local` accounts.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## User and administrator password policies

The domain password policy is generated from the `user_*` parameters. A separate `pso_privileged`, generated from the `admin_*` parameters, applies to built-in `Administrator` and members of `Admins`. The two parameter sets do not override each other.

| Policy | Ordinary-user default | Administrator default |
| --- | --- | --- |
| Complexity | off | on |
| Minimum length | 8 characters | 8 characters |
| Password history | 2 passwords | 2 passwords |
| Minimum age | 1 day | 1 day |
| Maximum age | 90 days | 0 (never expires) |
| Lockout threshold | 10 attempts | 10 attempts |
| Lockout duration | 30 minutes | 30 minutes |
| Failed-attempt reset | 30 minutes | 30 minutes |

With complexity disabled, Samba does not inspect character categories; it does not require a mixture of letters and digits. A one-day minimum age means users cannot change their own password again until one day after a successful change.

```yaml
modules:
  samba_dc:
    config:
      user_complex_pass: false
      user_min_pass_length: 8
      user_password_history: 2
      admin_complex_pass: true
      admin_min_pass_length: 8
      admin_password_history: 2
      admin_max_pass_age: 0
```

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

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
| `samba_dc.create_structure` | bool | — | `true` | `static` | `SAMBA_DC_CREATE_STRUCTURE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.dns_allowed_networks` | string | — | `""` | `static` | `SAMBA_DC_DNS_ALLOWED_NETWORKS` | no | no | no | yes | `container_recreate` | BIND recursion and cache access are restricted to these networks. |
| `samba_dc.dns_cache_size` | string | — | `128M` | `static` | `SAMBA_DC_DNS_CACHE_SIZE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.dns_debug` | bool | — | `false` | `static` | `SAMBA_DC_DNS_DEBUG` | no | no | no | yes | `container_recreate` | The named process debug level is selected when the container starts. |
| `samba_dc.dns_forwarders` | string | — | `""` | `static` | `SAMBA_DC_DNS_FORWARDERS` | no | no | no | yes | `container_recreate` | The generated BIND configuration is installed during container initialization. |
| `samba_dc.ldap_bind_name` | string | — | `svc_ldap` | `static` | `SAMBA_DC_LDAP_BIND_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.ldap_bind_password` | string | — | — | `generated` | `SAMBA_DC_LDAP_BIND_PASSWORD` | no | yes | yes | no: `rotate-ldap-bind-password` | `credential_rotate` | Update AD and all LDAP consumers as one transaction. |
| `samba_dc.log_level` | string | — | `1` | `static` | `SAMBA_DC_LOG_LEVEL` | no | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_dc.max_log_size` | int | `>= 1` | `2048` | `static` | `SAMBA_DC_MAX_LOG_SIZE` | no | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_dc.netbios_name` | string | — | — | `runtime` | `SAMBA_DC_NETBIOS_NAME` | no | yes | no | no: `replace-domain-controller` | `immutable` | Changing the DC machine identity requires a controlled replacement. |
| `samba_dc.password_bind_name` | string | — | `svc_password` | `static` | `SAMBA_DC_PASSWORD_BIND_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.password_bind_password` | string | — | — | `generated` | `SAMBA_DC_PASSWORD_BIND_PASSWORD` | no | yes | yes | no: `rotate-password-bind-password` | `credential_rotate` | Update AD and password-capable applications as one transaction. |
| `samba_dc.realm` | string | — | — | `inherited` | `SAMBA_DC_REALM` | no | yes | no | no: `migrate-domain` | `immutable` | The realm is part of the provisioned AD identity. |
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

### Query and modify

```bash
anas config list samba_dc -w /srv/anas
anas config explain samba_dc.admin_complex_pass
anas config set samba_dc.admin_min_pass_length 12 -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

### Sensitive parameters and generated secrets

- `samba_dc.admin_password` → `SAMBA_DC_ADMIN_PASSWORD`
- `samba_dc.administrator_password` → `SAMBA_DC_ADMINISTRATOR_PASSWORD`
- `samba_dc.anchor_bind_password` → `SAMBA_DC_ANCHOR_BIND_PASSWORD`
- `samba_dc.ldap_bind_password` → `SAMBA_DC_LDAP_BIND_PASSWORD`
- `samba_dc.password_bind_password` → `SAMBA_DC_PASSWORD_BIND_PASSWORD`

```bash
anas config secret list -w /srv/anas
anas config secret get SAMBA_DC_ADMIN_PASSWORD -w /srv/anas
anas config secret get SAMBA_DC_ADMINISTRATOR_PASSWORD -w /srv/anas
anas config secret get SAMBA_DC_ANCHOR_BIND_PASSWORD -w /srv/anas
anas config secret get SAMBA_DC_LDAP_BIND_PASSWORD -w /srv/anas
anas config secret get SAMBA_DC_PASSWORD_BIND_PASSWORD -w /srv/anas
```

`secret get` works only when the module generated and stored the value. A user-supplied configuration value is not echoed by the safe inventory command. For `credential_rotate`, neither `config set` nor `env.<KEY>` replaces application-internal rotation. For sensitive parameters still modeled as an ordinary recreate, the CLI accepts `config set`, but the value would enter argv/shell history; prefer the generated secret or a protected configuration-editing workflow.

## Timezone and language

- Timezone status: `system`
- Timezone mechanism: Startup validates TZ against /usr/share/zoneinfo and installs /etc/localtime and /etc/timezone.
- Language status: `not_applicable`
- Fallback: No user-facing Web UI exists; automation should keep LC_ALL=C where stable machine-readable output is required.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list samba_dc -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

`realm` and `netbios_name` are immutable identities; password parameters require dedicated credential rotation and cannot be changed by recreating containers.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
