# Samba file server technical implementation

This page records the current implementation, security boundaries, and verification entry points for `samba_fs`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `4.23.6-r6` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `samba_dc` | Module | — |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_samba_fs` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-fs:4.23.6-r6` | `default` | 2 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | enum (`all_rw`, `all_read_group_write`) | — | `all_read_group_write` | `static` | `SHARE_ACCESS_MODE` | no | no | no | yes | `reconcile` | Samba configuration and root/default ACLs must be reconciled. |
| `env.SHARE_DIR_NAME` | string | — | `Share` | `static` | `SHARE_DIR_NAME` | no | no | no | no: `migrate-share-directory` | `data_migrate` | The share directory holds the files; a new name is a new empty directory unless the contents are moved with it. |
| `env.SHARE_GUEST_READ_ONLY` | enum (`Yes`, `No`) | — | `No` | `static` | `SHARE_GUEST_READ_ONLY` | no | no | no | yes | `reconcile` | A state marker prevents recursive ACL work when the value is unchanged. |
| `env.USE_DEFAULT_DOMAIN` | enum (`yes`, `no`, `true`, `false`) | — | `yes` | `static` | `USE_DEFAULT_DOMAIN` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_fs.hostname` | string | — | `SambaFS` | `static` | `SAMBA_FS_HOSTNAME` | no | no | no | no: `rejoin-samba-member` | `data_migrate` | The AD machine account and member join must be changed together. |
| `samba_fs.log_level` | int | — | `1` | `static` | `SAMBA_FS_LOG_LEVEL` | no | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_fs.wsdd_log_level` | int | — | `0` | `static` | `SAMBA_FS_WSDD_LOG_LEVEL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## AD-domain boundary and member trust

The Samba FS member identity consumes only directory values exported by Samba
DC: `SAMBA_DC_DOMAIN`, `SAMBA_DC_REALM`, `SAMBA_DC_DNS_SEARCH`,
`SAMBA_DC_DC_DOMAIN`, the workgroup, and the DNS server. It never derives join
settings from `BASE_DOMAIN`. `global.base_domain` controls only the
application/Web namespace; `modules.samba_dc.config.domain` controls the AD
DNS domain, Kerberos realm, and machine trusts. When old configuration omits
`samba_dc.config.domain`, Samba DC falls back to `BASE_DOMAIN` for
compatibility.

Samba DC's `application_dns_mode` determines only whether complete application
FQDNs live in the AD zone (`ad_zone`) or a separate application zone
(`separate_zone`). It does not change the AD domain or canonical DC FQDN used
by Samba FS. `SAMBA_DC_HOST=BASE_DOMAIN`, used by ANAS LDAP consumers, is a
TLS service alias that points to `SAMBA_DC_HOST_IP`; Samba FS does not treat it
as the Kerberos/member canonical name.

Changing only the application domain must therefore never trigger Samba FS
leave/join or rewrite an existing machine account. The service-domain and
application-DNS-zone migrators for existing workspaces have not been
delivered; do not bypass their guards to test this path. A provisioned
`SAMBA_DC_DOMAIN` cannot be renamed in place. A new AD domain requires a new
directory and a fresh Samba FS join.

The runtime wiring enforces the same boundary. Both Compose's initial resolver
and the container's `/etc/resolv.conf` use only `SAMBA_DC_DNS_SERVER` as their
nameserver and `SAMBA_DC_DNS_SEARCH` as the search domain. They do not fall
back to `LOCAL_DNS_SERVER`, a host resolver, or the VLAN gateway. `krb5.conf`
gets its realm, canonical KDC FQDN, and domain mapping from
`SAMBA_DC_REALM`, `SAMBA_DC_DC_DOMAIN`, and `SAMBA_DC_DOMAIN`; `smb.conf` gets
its workgroup and realm from `SAMBA_DC_WORKGROUP` and `SAMBA_DC_REALM`.

Every start runs `net ads testjoin` first. A valid existing trust is reused
without a join, and there is no automatic leave path. Only an invalid trust
causes `net ads join` retries with Samba DC administrator credentials. A
successful join must still pass a subsequent `net ads testjoin`; otherwise the
helper retries instead of declaring an unverified machine account ready. It
then registers the current member address with `net ads dns register -P` and
blocks startup if registration fails, preventing the FS FQDN from retaining a
stale address. The `wbinfo -t` health check verifies the same generated `smb.conf` and member
trust without reading the application domain or TLS service alias. Even if an
application-domain change recreates the container, the existing AD trust is
therefore only checked and reused.

`SAMBA_DC_DNS_SERVER` is a numeric address exported by the Samba DC hook from
`SAMBA_DC_HOST_IP`, so Docker does not need to resolve the DC name before it
can install the resolver. Samba DC is a one-way dependency of this module and
does not depend on Samba FS, so there is no DNS startup cycle. If the DC is not
ready yet, the join helper waits and runs `testjoin` again. It returns as soon
as the existing trust becomes reachable and joins only when the reachable
trust is still invalid.

## Identity and authorization data flow

SMB clients authenticate with directory identities. Groups such as `FS Share RW` and `FS Admins` control access; users and groups are managed in Samba AD/LAM rather than copied into this module.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | AD domain / SMB authentication (`users, groups`) |
| IAM | unsupported/not applicable |
| Group | `FS Share RW`, `FS Admins` |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

There is no Web administrator or local recovery account. Restore Samba AD/domain-join connectivity after an outage.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `SAMBA_DC_ADMIN_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `SHARE_DIR_NAME`
- `SHARE_ACCESS_MODE`
- `SHARE_GUEST_READ_ONLY`
- `USE_DEFAULT_DOMAIN`

### Explicit consumes

- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_NAME`
- `SAMBA_DC_DC_DOMAIN`
- `SAMBA_DC_DNS_SEARCH`
- `SAMBA_DC_DNS_SERVER`
- `SAMBA_DC_DOMAIN`
- `SAMBA_DC_FS_ADMIN_GROUP_NAME`
- `SAMBA_DC_FS_SHARE_RW_GROUP_NAME`
- `SAMBA_DC_REALM`
- `SAMBA_DC_WORKGROUP`
- `SAMBA_DC_ADMIN_PASSWORD`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- [`domain_wiring_test.go`](../hook/domain_wiring_test.go)
- [`join_ad.sh`](../samba_fs/root/usr/local/bin/join_ad.sh)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

Changing the hostname requires rejoining the current `SAMBA_DC_DOMAIN`.
Changing the share directory requires file migration, which ordinary apply
does not perform. An existing AD domain cannot be renamed in place, and the
application-domain/internal-zone migrators for existing workspaces have not
been delivered.
