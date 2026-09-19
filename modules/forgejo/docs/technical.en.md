# Forgejo technical implementation

This document records the `forgejo` container adapter, hook, security boundaries, and validation entry points.

<!-- generated:module-identity:start -->
> Status: current implementation; based on `15.0.7-r1` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose topology

The Actions preflight and controller join `compute-control` through their compute Resource projection.
For local host provisioning it references the pre-created external control bridge; Compose does not
own its creation or removal. The Forgejo Web service does not gain this host-network attachment.
`actions-control` keeps the default business gateway with `gw_priority: 1` (Compose 2.33.1+).
This topology declaration is not native mTLS, isolation or one-job acceptance evidence.

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_forgejo` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-forgejo:15.0.7-r1` | `actions-control, db, traefik` | 2 |
| `anas_forgejo_actions_controller` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-forgejo-actions-controller:15.0.7-r1` | `actions-control, compute-control` | 1 |
| `anas_forgejo_actions_preflight` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-forgejo-actions-controller:15.0.7-r1` | `actions-control, compute-control` | 0 |
<!-- generated:compose-topology:end -->

Web/API port 3000 is reachable only through Traefik. Built-in SSH container port 2222 is published directly as
`FORGEJO_SSH_PORT`. The root filesystem is read-only; `/tmp` is tmpfs and `/var/lib/gitea` is the application data
mount. The image extends `codeberg.org/forgejo/forgejo:15.0.7-rootless`. Its static wrapper fixes an initially
root-owned mount without following symlinks, drops permanently to `1000:1000`, and executes the upstream entrypoint.

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `forgejo.actions_allowed_scopes` | string | — | `""` | `static` | `FORGEJO_ACTIONS_ALLOWED_SCOPES` | no | no | no | yes | `container_recreate` | Comma-separated organizations or repositories authorized to consume ANAS Runner compute |
| `forgejo.actions_enabled` | bool | — | `false` | `static` | `FORGEJO_ACTIONS_ENABLED` | no | no | no | yes | `container_recreate` | The only shared switch for the Actions server and one-job Runner controller |
| `forgejo.actions_isolation` | enum (`auto`, `incus_vm`, `incus_container`) | — | `auto` | `static` | `FORGEJO_ACTIONS_ISOLATION` | no | no | no | yes | `container_recreate` | Isolation tier requested from the compute provider |
| `forgejo.actions_runner_image` | string | `format: json_object` | `""` | `static` | `FORGEJO_ACTIONS_RUNNER_IMAGE` | no | no | no | yes | `container_recreate` | Structured image reference; Core freezes the fingerprint before rendering |
| `forgejo.custom_git_hooks_enabled` | bool | — | `false` | `static` | `FORGEJO_CUSTOM_GIT_HOOKS_ENABLED` | no | no | no | yes | `container_recreate` | Allow repository custom Git hooks to execute server-side code as the Forgejo user |
| `forgejo.db_name` | string | — | `forgejo` | `static` | `FORGEJO_DB_NAME` | no | no | no | no: `migrate-forgejo-database` | `data_migrate` | Application database name |
| `forgejo.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `FORGEJO_DB_TYPE` | no | no | no | no: `migrate-forgejo-database` | `data_migrate` | Relational database type or automatic selection |
| `forgejo.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `git` | `static` | `FORGEJO_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Service domain prefix |
| `forgejo.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `FORGEJO_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | IAM login protocol; OIDC only |
| `forgejo.language` | string | — | — | `inherited` | `FORGEJO_LANGUAGE` | no | yes | no | yes | `reconcile` | Default UI language; browser and saved preferences take precedence |
| `forgejo.local_path_import_enabled` | bool | — | `false` | `static` | `FORGEJO_LOCAL_PATH_IMPORT_ENABLED` | no | no | no | yes | `container_recreate` | Allow imports from paths already visible inside the Forgejo container without adding a host mount |
| `forgejo.ssh_port` | int | `1..65535` | `2222` | `static` | `FORGEJO_SSH_PORT` | no | no | no | yes | `container_recreate` | Public SSH Git port |

The hook matches `DEFAULT_LANGUAGE` against the pinned 31-locale inventory and moves the selected locale to the
front of the complete `[i18n] LANGS/NAMES` list. Unsupported values warn and fall back to `en-US`.
Every upstream `FORGEJO__[SECTION]__[KEY]` setting is emitted in the uppercase form accepted by the ANAS Hook ABI;
the double-underscore section/key mapping is unchanged, so no lowercase section key is rejected by the Runner.

## Database and persistent state

Forgejo consumes a retained `primary_database` Resource. PostgreSQL maps to `postgres`; MariaDB maps to `mysql`.
The database owns users, organizations, metadata, issues, permissions, and sessions. `/var/lib/gitea` owns Git
repositories, LFS, packages, attachments, SSH state, indexes, and application configuration. Database name/type
changes are explicit data-migration boundaries.

## OIDC and session flow

`calculate` publishes client `forgejo`, callback `<domain>/user/oauth2/anas/callback`, scopes
`openid,profile,email,groups`, and claim mappings. With application filtering enabled, IAM admits
`APP_forgejo`, `APP_all`, and administrators.

`after_start` sends the desired auth-source document through container stdin. The helper idempotently executes the
pinned `forgejo admin auth add-oauth/update-oauth` command for source `anas` and maps the administrator group to
site administrators. Upstream offers no secret-stdin option, so the secret is briefly present in a helper child
process argv inside the container, but never in host `docker exec` argv, hook output, or error text. The OIDC secret
therefore uses manual `migrate` rotation rather than the unified transaction.

External JIT registration is enabled, open registration and automatic account linking are disabled, and sessions
use the database provider. The pinned version clears only its local session on `/user/logout`; the Module declares
no RP-Initiated or IAM-initiated logout receiver.

The current Module consumes no directory capability, configures no LDAP source, publishes or consumes no
`anasIdentityAnchor`, and supports no SAML source. The pinned version exposes no interface that joins an OIDC
identity to a pre-provisioned LDAP user by an immutable id -- the OIDC source can only fall back to username or
email -- so LDAP plus OIDC dual provisioning is not implemented: OIDC JIT creates users and Forgejo continues to
own organizations and teams. The decision and the upgrade re-check trigger are in the
[Forgejo Module design](/architecture/forgejo-module-design) §2.2.

## Boundaries of a single identity path

With OIDC as the only path the Module keeps no directory copy, so it falls outside the scope of the
[directory event subscription requirement](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-event-subscription.md)
and runs no watcher. On the OIDC side, `LOGOUT-R-007`/`R-008` record that the pinned version can neither end the
IAM central session nor receive an IAM-initiated logout. Neither direction has an automatic path, so per
`DIRSYNC-R-014` this is the missing side and its fallback:

| Directory change | How Forgejo converges |
| --- | --- |
| Account disabled, deleted, or removed from `APP_forgejo` | **No automatic path**: an administrator must disable or delete the Forgejo account |
| Group membership change | Reaches teams through the groups claim at that person's next OIDC login |
| Rename or mail change | **The same account**: matched by `sub`; username and email are written only at creation, so the username stays frozen at the old value (`inferred`, probe pending) |

Access tokens and SSH keys never pass through a login, so "it converges at the next sign-in" does not hold for
them. Revocation has to act on the account itself.

**Not yet verified**: whether `prohibit_login` on the pinned version also closes access tokens and Git over SSH.
Until a probe settles it, revocation means disabling the account *and* explicitly revoking its tokens and SSH
keys. See [interoperability baseline](/developer/forgejo-interop) §4.

## Recovery and security boundaries

The `break_glass` account is generated per Module. On first apply, the helper asks the CLI for a random bootstrap
password, changes it through the loopback admin API, and verifies the managed password. The desired secret enters
neither CLI nor Docker argv. Forgejo stores local-account hashes with bcrypt, matching the manifest projection
format. Existing-account drift fails closed. Rotation is not declared because Forgejo's CLI cannot satisfy verified
rotate/rollback without placing the password in argv.

Actions defaults off and `forgejo.actions_enabled` is the sole switch for the server and controller. The controller
polls approved scopes and creates one ephemeral registration and Incus VM per waiting handle. The token travels
only through Incus exec stdin to guest tmpfs, and cleanup covers completion, timeout, cancellation, shutdown, and
persisted crash state. The provider-neutral compute boundary fixes the restricted Incus project/profile and rejects
raw configuration, host disks, physical NICs, cloud-init secrets, and arbitrary devices. The guest uses separate
Runner/engine users, rootless Podman, capacity one, no privileged mode, no valid volumes, and no `host` label.

Before Forgejo starts with Actions enabled, a one-shot process using the same controller image connects to Incus and
validates the restricted project, quotas, and profile. Forgejo and the long-running controller depend on this
preflight completing successfully. With Actions disabled the preflight performs no Incus access and exits; it has no
separate feature state and is not a second Runner switch.

Empty queues create no Runner or VM, and no ANAS host Docker socket is shared. Real independent Incus/KVM, egress
firewall, image build, and one-job E2E remain release gates. Git hooks and local-path import are independently
configurable and disabled by default. Hooks execute server-side code as the Forgejo user; local
imports can read only paths already visible inside the container, and Compose adds no host mount for the feature.
LFS and built-in SSH are enabled. The Web port is Compose-private and the v15 image wildcard
`REVERSE_PROXY_TRUSTED_PROXIES` default is replaced with loopback and RFC 1918 container sources. SMTP, object
storage, and external search remain out of scope.

Design decisions are recorded in the [Forgejo Module design](/architecture/forgejo-module-design). Remaining work
and explicit exclusions are tracked in the [Forgejo Module implementation plan](../dev-docs/plans/forgejo-module.md).

Unit tests cover database mapping, locale fallback, OIDC metadata, secret stability, stdin boundaries, symlink-safe
ownership, local-admin bootstrap, and auth-source reconciliation. Database/architecture matrices, browser OIDC,
HTTP/SSH Git, LFS/package, restore, and LTS upgrade/rollback E2E remain release gates.

## Frozen compute image configuration

Image settings now use structured objects (a single object for Forgejo, a runtime-keyed map for AI Agent).
Core projects these through explicit `spec_from` modes and resolves them before Hook calculation. Runtime
containers receive only frozen fingerprints: Forgejo reads the lease allowlist, AI Agent reads JSON image
bindings. The Agent hook reads `AI_AGENT_AGENT_RUNTIMES`, matching the manifest parameter; Compose passes
it to the orchestrator as `AI_AGENT_RUNTIMES`. No new dependency is introduced.

Incus requires explicit `image_architecture` for the daemon target. Its trusted bundle catalog is currently
empty. Ensure checks each existing image's fingerprint, architecture and type in the lease project before
registering trust; missing images fail instead of resolving aliases or rebuilding. Import/baking remains
pending. See the [compute contract](../../../contracts/compute/docs/technical.en.md) for snapshot and rollback semantics.

The HTTP network prototype only generates lab artifacts (`cmd/incus-network-prototype`): a guest /32 route
with explicit source, veth-bound ingress filtering, an expiring address/port set, and existing Traefik route
environment fields. It does not install rules or enable production ingress. Docker/Incus rule ordering,
source spoofing, address reuse and long-connection revocation still require real Linux evidence; TCP/UDP
publishing is not implemented.

## Lease naming key lifecycle

Core now generates and reuses an independent 32-byte compute `LEASE_SECRET`, separate from the client
certificate. Deployment/resource state store references; the consumer receives a sensitive base64 projection
and backup restores the same key. It is excluded from credential rotation. See the
[compute lifecycle contract](../../../contracts/compute/docs/technical.en.md#independent-lease-naming-key).
The dedicated rotation command and production HTTP publishing remain pending.
