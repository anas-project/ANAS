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

The runtime wiring preserves the same identity boundary while separating
identity from transport. A Samba FS macvlan child cannot directly reach the
host address on its parent interface, so the hook derives the private
`SAMBA_FS_DC_TRANSPORT_IP` from the host-side macvlan bridge's
`VLAN_BRIDGE_IP` (falling back to `SAMBA_DC_DNS_SERVER` when no bridge value is
available). Compose's initial resolver and the container's `/etc/resolv.conf`
continue to use the DC's original listener address, `SAMBA_DC_DNS_SERVER`,
with `SAMBA_DC_DNS_SEARCH`. Initialization installs a `/32` route to that DNS
address through the transport IP. Independently, `/etc/hosts` maps the
canonical `SAMBA_DC_DC_DOMAIN` to the transport IP. Kerberos and AD still identify the DC
only by its canonical FQDN: `krb5.conf` gets its realm, KDC FQDN, and domain
mapping from `SAMBA_DC_REALM`, `SAMBA_DC_DC_DOMAIN`, and `SAMBA_DC_DOMAIN`,
never treating the bridge IP as a Kerberos identity. `smb.conf` gets its
workgroup and realm from `SAMBA_DC_WORKGROUP` and `SAMBA_DC_REALM`.

Every start runs `net ads testjoin` first. A valid existing trust is reused
without a join, and there is no automatic leave path. Only an invalid trust
causes `net ads join` retries with Samba DC administrator credentials. A
successful join must still pass a subsequent `net ads testjoin`; otherwise the
helper retries instead of declaring an unverified machine account ready. It
then acquires a DC-administrator ticket in a short-lived Kerberos cache, uses
`samba-tool dns` to remove stale A records and install the current member
address idempotently, and verifies the result by querying
`SAMBA_DC_DNS_SERVER`; that packet crosses the macvlan boundary through the
explicit `/32` route.
The password enters `kinit` only on standard input and never appears in process
arguments or logs. Registration or verification failure blocks startup. The
`wbinfo -t` health check verifies the same generated `smb.conf` and member
trust without reading the application domain or TLS service alias. Even if an
application-domain change recreates the container, the existing AD trust is
therefore only checked and reused.

Both the DNS server and transport next hop are numeric, so Docker does not need
to resolve the DC name before installing the resolver and route. Samba DC is a one-way dependency and
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

### Directory attribute changes — implementation

One-to-one with the README's *Directory attribute changes*.

- **Which table and field persist identity**: there is no database. Persistent identity lives on the
  **filesystem**: the inode's UID/GID, and the NT ACL in the `security.NTACL` extended attribute
  (written by `vfs objects = acl_xattr`, inherited via `map acl inherit = Yes`). winbind's `idmap` tdb
  cache also exists, but it is a rebuildable mapping cache, not a source of truth.
- **How the matching key is configured**: `idmap config ${SAMBA_DC_WORKGROUP} : backend = rid` with
  `range = 10000-999999` in `smb.conf.envsubst`. The rid backend is a **deterministic algorithm**:
  UID = the range's base + the SID's RID. The same SID therefore maps to the same UID at any time and
  on any member server, with no persistent mapping table and no drift on rename. The default-domain
  `idmap config * : backend = tdb` with `range = 3000-7999` serves only local and trusted-domain
  fallbacks.
- **Refreshed at each login**: there is no replica to refresh. Users and groups are resolved live by
  winbind through `nsswitch.conf`; `winbind enum users/groups = No` disables enumeration and
  `winbind expand groups = 2` bounds nested expansion.
- **Which interface performs revocation**: the DC's Kerberos/NTLM authentication decision (for new
  sessions) and `valid users`/`write list`/the POSIX ACLs (for authorization). **No interface acts on
  an established SMB session** — `smbcontrol` is a manual operator command, not an automatic path.
- **Reconciliation or event-subscription path**: none, and no directory replica needs keeping, so this
  Module falls outside the
  [directory event subscription requirement](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-event-subscription.md).
  The convergence latency of winbind's cache is set by its own TTL.
- **Where there is no automatic path, the technical obstacle**: **the SMB protocol has no primitive
  for "disconnect sessions on a directory event"**. What Samba offers is `smbcontrol`, an
  administrator command, with no event interface ANAS could call. This is not a missing immutable id —
  the SID is right there — it is a missing revocation interface.

**`DIRKEY-R-002` compliance**: compliant. The persistent key is the SID/UID, a directory rename does
not change it, and file ownership and ACLs are therefore stable across a rename. This Module does not
consume `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE`, because both SMB and POSIX only know SIDs; using the
anchor would introduce a mapping layer that does not otherwise exist.

**`DIRKEY-R-010` observation point: `[Home]` projects the username into a file path.**
`path = /userdata/Home/%U` together with
`root preexec = /usr/local/bin/samba_create_user_dir.sh /userdata/Home %U`. What is projected is the
`sAMAccountName` **label**, not the anchor, so this does not breach `DIRKEY-R-010`, which forbids
projecting the *anchor* into a path. It does leave an orphaned directory after a rename, and the
consequence and fallback are recorded in the README. **Naming home directories by anchor or SID would
turn the path into a UUID or `S-1-5-…` form**, which is precisely the shape `DIRKEY-R-010` forbids —
so keeping the username here is the correct choice, with the cost carried by fallback action 3.

**`DIRKEY-R-013` projection verdict: not applicable.** This Module is not an OIDC/SAML Consumer
(`module.yml` declares no `iam` and consumes no IAM binding), consumes no subject identifier, and is
entirely unaffected by the M2 switch.

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
- [`register_ad_dns.sh`](../samba_fs/root/usr/local/bin/register_ad_dns.sh)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

Changing the hostname requires rejoining the current `SAMBA_DC_DOMAIN`.
Changing the share directory requires file migration, which ordinary apply
does not perform. An existing AD domain cannot be renamed in place, and the
application-domain/internal-zone migrators for existing workspaces have not
been delivered.
