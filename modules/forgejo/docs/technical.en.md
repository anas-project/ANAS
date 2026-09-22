# Forgejo technical implementation

## Consistent cgroup ownership for OCI create and exec

`anas-podman.service` and its socket belong to the engine's systemd **user manager**; the socket is
enabled only for that account. A system service with the same UID is not the same cgroup authority.
Podman uses `--cgroup-manager=systemd` so create, exec and resource limits share one owning manager.
A fixed `user@1002.service` drop-in establishes PrivateTmp/ProtectSystem=strict in the system manager,
allowing writes only to the engine home, shared socket parent and private runtime, then passes that
mount isolation to the user units. Delegation, CPU/memory/PID limits, no-new-privileges, separate UIDs
and socket mode 0660 remain. Neither the engine nor its private bus is enabled for the agent.

The complete `.config/systemd/user/sockets.target.wants` directory chain is explicitly engine-owned,
group actions-engine, mode 0700. Assigning only the deepest directory can leave root-owned parents
that prevent first-time engine configuration. A previously initialized guest can hide this defect;
immutable admission therefore also checks private configuration ownership on cold boot.

The retained cgroupfs candidate `lab-r9` demonstrated why successful exec is insufficient: inspect
declared 128 MiB/32 PID, but actual tasks remained in the service cgroup with memory.max=max. That
candidate is rejected. Configuration values are not enforcement evidence, and cgroups are not disabled.

Immutable-image admission now requires `rootless-oci-exec-limits`: the actual agent drives create and
exec through the fixed socket using a digest-pinned OCI input, then computes effective cpu.max,
memory.max and pids.max across the task's visible cgroup ancestry and checks NoNewPrivs. It requires
0.5 CPU, 128 MiB, 32 PID and NNP=1, below the outer lease so the outer limits alone cannot pass.
A working info API or a disposable guest with a manually patched service cannot satisfy this gate.
The separate workflow matrix covers normal and
deliberate failure, controller SIGTERM cleanup and SIGKILL recovery with retained state, not Forgejo
web cancellation, state-volume loss, VM/ARM64 or production release acceptance.

## Runner startup permission corrections (2026-09-22)

Actual image testing found that the private build umask had left guest `/` at 0700, causing non-root
systemd services to fail at CHDIR. The recipe explicitly sets the guest root to 0755 while retaining
private archive and user-home modes. The engine account's passwd primary group must match the
service's actions-engine group; otherwise newuidmap rejects the process identity. The two UIDs,
private homes and token ownership remain separate.

`anas-podman.socket` creates the fixed API path with mode 0660, owned by runner-engine and the
actions-engine group. Podman inherits the systemd listening descriptor instead of creating a 0600
socket itself. Tmpfiles creates only the fixed shared 0770 parent and private 0700 engine runtime
directory. The service requests cgroup delegation; this does not enable privileged jobs, arbitrary
volumes. The later Provider namespace-policy adjustment needed for actual OCI execution is described below.

These are source corrections; actual candidate-image and one-job acceptance is recorded in
[the runtime review](../../../dev-docs/reviews/2026-09-22-incus-onejob-runtime-completion.md).

This document records the `forgejo` container adapter, hook, security boundaries, and validation entry points.

The default Runner recipe explicitly sets `/etc/forgejo-runner` and `/usr/local/libexec` to root:root,
mode 0755. These directories contain only public configuration and executables. The private builder
umask must not make their parent traversal root-only and prevent `runner-agent` from reading a 0644
configuration file. Home, token and archive permissions are not changed recursively. Recipe changes
require a new immutable revision. Native image admission now checks configuration readability as the
actual Runner account; a root-owned `--version` probe or working engine API is not sufficient.

The rootless engine also needs a real guest systemd user session for UID 1002. The recipe explicitly
installs `dbus-user-session`, enables engine-only lingering offline, and makes the engine service depend
on `user@1002.service`, using its private `/run/user/1002` runtime and bus. The shared API remains an
independent 0660 actions-engine group socket; the agent does not gain access to the private runtime.
An actual container start failed when aardvark-dns could not reach the user scope bus, which `podman
info` alone did not detect. No persistent Runner is enabled; user-session setup itself does not change
Incus policy or business-host systemd settings.

The Provider now fixes inner OCI namespace permission for the container tier; the client neither overrides
it nor accepts a caller nesting option. This is not nested virtualization, but it does expand the permitted
guest kernel-operation set. Project-level host-device/raw/privileged restrictions remain. Cross-trust
workloads still use VMs. See the [Chinese design](/architecture/forgejo-module-design), section 4.3.

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

### Directory attribute changes — implementation

One-to-one with the README's *Directory attribute changes*.

- **Where identity is persisted**: Forgejo's `user` table. The OIDC `sub` lands in the `login_name`
  column; `name` holds `preferred_username` and `email` holds `mail`, both written only when the
  account is created just in time. `external_login_user` additionally links the OAuth2 source to the
  same `sub`. The Module keeps no directory replica.
- **Matching key**: the `sub` in `login_name`. The Module does not choose this key — upstream's
  OAuth2 source fixes it, and the pinned version has no setting that binds an existing account by an
  anchor claim. That is exactly why the dual path fails in §2.2.
- **Refreshed at each login**: the groups claim, feeding team membership and the site-administrator
  mapping. Username, email, and display name are **not** refreshed.
- **Which interface performs revocation**: none. The Module registers no directory-event watcher and
  the pinned version exposes no IAM-initiated logout receiver, so there is no automatic revocation
  interface at all. Only an administrator, through Forgejo's admin UI or CLI, can revoke.
- **Reconciliation path**: none today. The entry point that would unlock one is below.
- **Technical obstacle**: upstream's OAuth2 source accepts no configurable identity field, and
  `ACCOUNT_LINKING` can only fall back to username or email. This is a gap in the sense of
  `DIRKEY-R-004`; per `DIRKEY-R-005` it is re-checked whenever the pinned version changes (the
  re-check trigger is `FORGEJO-R-066`).

**`DIRKEY-R-013` projection verdict: unaffected (`inferred`).** Forgejo stores `sub` in `login_name`,
an internal field visible only to site administrators; the in-application username comes from
`preferred_username`, and that username — not `sub` — is what URLs and repository paths use. So once
M2 switches the subject identifier to the anchor, no UUID surfaces in the interface and Forgejo needs
no prior change. **Not yet confirmed by a probe**: whether `login_name` really receives the verbatim
`sub`, and whether the admin user list echoes it. The check belongs in
`test-env/scripts/forgejo-agent-api-probe.sh` and must run before the M2 switch.

After the switch this gains a capability it does not have today: the value in `login_name` will be
the anchor itself, so listing Forgejo's external accounts, diffing them against the directory's
admitted set, and revoking the difference reconciles exactly, without catching renamed employees. That
path needs no LDAP source and does not change how accounts come into being.

## Recovery and security boundaries

The `break_glass` account is generated per Module. On first apply, the helper asks the CLI for a random bootstrap
password, changes it through the loopback admin API, and verifies the managed password. The desired secret enters
neither CLI nor Docker argv. Forgejo stores local-account hashes with bcrypt, matching the manifest projection
format. Existing-account drift fails closed. Rotation is not declared because Forgejo's CLI cannot satisfy verified
rotate/rollback without placing the password in argv.

Actions defaults off and `forgejo.actions_enabled` is the sole switch for the server and controller. When it is on,
`validateActionsConfig` checks the three inputs Forgejo itself owns -- the shape of `FORGEJO_ACTIONS_ALLOWED_SCOPES`
(`{owner}` or `{owner}/{repo}` only), `FORGEJO_ACTIONS_CONTROLLER_PASSWORD`, and the 64-hex image fingerprint. The
Incus endpoint and client certificate are **no longer Forgejo settings**: the compute contract supplies them and
projects them into the controller, so the hook cannot validate them; the preflight below validates
project/quota/profile at run time. The controller polls approved scopes and creates one ephemeral registration and
one Incus instance per waiting handle. The token travels only through Incus exec stdin to guest tmpfs, and cleanup
covers completion, timeout, cancellation, shutdown, and persisted crash state. The provider-neutral compute boundary
fixes the restricted Incus project/profile and rejects raw configuration, host disks, physical NICs, cloud-init
secrets, and arbitrary devices. The guest uses separate Runner/engine users, rootless Podman, capacity one, no
privileged mode, no valid volumes, and no `host` label.

### Isolation tiers

`forgejo.actions_isolation` picks the compute contract interface, and the contract default in `module.yml` resolves
`auto` to `incus_container`: an unprivileged Incus system container that **shares the host kernel**. `incus_vm` is a
QEMU/KVM machine with its own guest kernel but requires a KVM-capable host. The container tier is the default
because the target hardware does not reliably provide KVM (`INCUS-R-052`, `FORGEJO-R-024`); neither tier is selected
automatically from host capability.

Both tiers carry the same provider-side constraints -- restricted project, the four quotas, constrained egress, a
single managed NIC, no host disk, physical NIC, or arbitrary device -- and the container tier additionally has
`restricted.containers.privilege=unprivileged` enforced on the project. The only difference is the kernel boundary,
so scopes that span trust domains or execute untrusted input should set `incus_vm` explicitly (`FORGEJO-R-025`).

`TestModuleIsolationTierDefaultsToTheContainerTier` pins the manifest's two-tier set and its
`default: incus_container` (`R-024`). **Current gap**: nothing checks that the pinned fingerprint matches the
selected tier and target architecture (`R-026`) -- `runner-image/` expects one image per amd64/arm64 and per
container/VM tier, while `actions_runner_image` holds a single value validated only for hex shape.

### The control-plane account

`after_start` calls `reconcileActionsAccount` only while Actions is on, passing the fixed `anas_actions_controller`
account to the container helper's `local-admin` subcommand over stdin. The password comes from the Secret Store and
never reaches host `docker exec` argv. That subcommand always passes `--admin`, so the account is a **site
administrator**.

The controller's entire call set is three endpoints, all confined to approved scopes: `GET
.../actions/runners/jobs`, `POST .../actions/runners`, and `DELETE .../actions/runners/{id}` (under `orgs/{owner}`
or `repos/{owner}/{repo}`), using basic auth. Those endpoints need organization-owner or repository-admin rights,
not site-wide ones; site administrator is the consequence of having no per-scope granting path, and is a recorded
deviation (`FORGEJO-R-069`). The call set itself is pinned by
`TestForgejoClientNeverLeavesTheApprovedScopeRunnerAPI`: three requests per scope, for both org and repo scopes,
all inside that scope's `actions/runners` subtree and none touching `/admin/` (`FORGEJO-R-068`).

`reconcileActionsAccount` returns immediately when Actions is off, so **turning the switch off does not revoke the
account**: it and its valid Secret Store password both remain, and an administrator must remove them by hand.
`FORGEJO-R-070` tracks the fix.

Before Forgejo starts with Actions enabled, a one-shot process using the same controller image uses the shared
client to validate lease inputs, the pinned connection and read access to the restricted project's instance list.
Complete project, quota and profile readiness belongs to Provider ensure/inspect and cannot be inferred from
this read-only connection. Forgejo and the long-running controller depend on this
preflight completing successfully. With Actions disabled the preflight performs no Incus access and exits; it has no
separate feature state and is not a second Runner switch.

Empty queues create no Runner or instance, and no ANAS host Docker socket is shared. Controller state lives in the
named volume `forgejo_actions_state`, outside the `R-003` backup consistency point -- but **it is not freely
discardable**: `ListManaged` is called only from `CleanupAll` (switch-off), while the periodic `Reconcile` works
purely from state, and an orphaned Forgejo runner registration has no fallback at all because deregistration needs
the `RunnerID` held in state and `ForgejoAPI` has no method that lists runners. Losing state means orphaned
instances wait until the next switch-off, and a registration created just before a crash stays forever.
`FORGEJO-R-046` tracks the fix. A real independent Incus host, egress firewall, image build, and one-job E2E per tier
remain release gates. Git hooks and local-path import are independently
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

## Actions cancellation, uncertain creation and durable retirement

The controller installs its shutdown signal context before initializing compute. It persists the instance
name, workload and `create_pending` before Create; CLI timeout or cancellation does not establish absence
of daemon side effects. Temporary absence retains the pending record until the matching late instance can
be observed and deleted, preventing immediate reuse of the workload identity.

Provisioning compensation uses an independent context capped at two minutes while preserving the original
error identity. Retirement is persisted before cleanup; periodic reconciliation processes interrupted and
expired work before queue requests can exhaust the budget. Instance/workload mismatches also protect that
instance from the later orphan sweep. Failed terminal persistence restores the in-memory retirement record
so another successful save cannot forget it. A registration receipt still permits compensation after the
first state save fails, without requiring another successful write before deregistration.

State uses unique temporary files and file/directory synchronization rather than reusing an old `.tmp`.
Tokens remain absent from state. The optional new field does not establish safe downgrade to old binaries.
Lost-state orphan registrations, real daemon cancellation/late creation and one-job execution still require
separate acceptance; local adapter regressions do not establish those results.

## Frozen compute image configuration

Image settings now use structured objects (a single object for Forgejo, a runtime-keyed map for AI Agent).
Core projects these through explicit `spec_from` modes and resolves them before Hook calculation. Runtime
containers receive only frozen fingerprints: Forgejo reads the lease allowlist, AI Agent reads JSON image
bindings. The Agent hook reads `AI_AGENT_AGENT_RUNTIMES`, matching the manifest parameter; Compose passes
it to the orchestrator as `AI_AGENT_RUNTIMES`. No new dependency is introduced.

Incus requires explicit `image_architecture` for the daemon target. Ensure checks the fingerprint,
architecture and type in the lease project before registering trust. Missing images are imported only
from local supply matching the frozen reference, then read back; unavailable identical bytes fail instead
of resolving aliases or rebuilding during apply. Release-side baking and import have experimental
candidate evidence, but signed distribution and complete Runner engine/one-job acceptance remain pending.
See the [compute contract](../../../contracts/compute/docs/technical.en.md) for snapshot and rollback semantics.

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

## Failed compensation still consumes scope capacity (2026-09-22)

Within a reconcile pass, the controller counts the actual workload record retained after provisioning,
not just successful returns. Rejected engine admission, uncertain creation and incomplete retirement retain
their scope slot until compensation is confirmed and the record is removed. This matches the next pass's
state-based accounting and prevents two retained instances when the per-scope limit is one. Global limits,
ownership checks and retry backoff are unchanged. Regressions cover retained failure, later release and
successful compensation without a phantom slot; interface fixtures are not real one-job acceptance.

## Runner API transport and native compatibility gate (2026-09-22)

The controller rejects every HTTP redirect so Basic auth and registration/deletion requests cannot
leave the approved scope's `actions/runners` call set. Successful queue reads require exactly one nullable
JSON array within 4 MiB. Forgejo 15.0.7's actual empty-queue null is normalized to an empty array, but
truncated, trailing and oversized responses cannot trigger empty-queue cleanup. Transport/decoding
errors use fixed messages, preserve caller cancellation, and do
not echo endpoints or private diagnostics. Unknown JSON fields remain allowed for API compatibility.

`test-env/scripts/server-forgejo-runner-api-e2e.py` runs an independent, loopback-only Forgejo 15.0.7 /
SQLite as an ordinary user inside an explicitly identified disposable QEMU VM. The actual production
client exercises jobs/create/delete for repository and organization scopes, followed by independent
registration-empty readback. Random account passwords stay out of argv and test inputs are private.
This does not establish account-privilege convergence, the database matrix or real one-job execution.

## Engine admission before Runner token input (2026-09-22)

`WaitForGuest` proves only that the guest entrypoint is executable. The new starter checks the actual
fixed guest Podman API as `runner-agent`, with a clean environment, before creating the token directory
or reading stdin. The command must succeed and explicitly report rootless=true. A service active state,
rootful result, malformed output, timeout or error cannot substitute for that check. Each probe has a
two-second deadline and one-second termination grace, with at most eight probes and seven one-second
pauses: a nominal 31-second wait, excluding scheduling overhead. Failure exits 69 with a fixed message,
without consuming the token, starting one-job, restarting the engine or relaxing its restrictions.
An already-active one-job retains the existing no-second-token behavior.

Behavior tests execute the unchanged shell with test-only PATH commands that stop before the first file
effect. Real coreutils timeout rejects a hung probe even after it prints true. Separate controller tests
cover instance/registration compensation and durable retirement retry ahead of an unavailable queue.
These are not real Podman or Forgejo workflow tests. The native image gate now allows a bounded
35-second readiness observation and emits only enumerated/numeric service diagnostics, never raw
journal text. Observations cannot turn a failed gate into a pass.

New recipe bytes require a new revision; archived `lab-r4` is unchanged. SSH to the selected host did
not complete its handshake in this continuation. The enhanced native gate has not run, and the original
engine exit 125 remains unresolved rather than being declared fixed by these local tests.
