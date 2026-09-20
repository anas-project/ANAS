# AI Agent orchestration technical notes

How the `ai_agent` control plane is put together and where its boundaries are. Configuration and
operation are in the [English README](../README.en.md); the acceptance criteria are the
[requirement matrix](../dev-docs/requirements/ai-agent.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `0.1.0-r1` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose topology

The orchestrator joins the provisioned external control bridge through its `work_instances` Resource
projection without gaining a Docker socket or host service authority. Database connectivity remains
separate, and `traefik` keeps the business default gateway with `gw_priority: 1` (Compose 2.33.1+).
Native control-bridge connectivity, certificate isolation and job lifecycle acceptance remain separate.

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_ai_agent` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-ai-agent:0.1.0-r1` | `db, traefik, compute-control` | 2 |
<!-- generated:compose-topology:end -->

One long-lived service. It is `read_only`, `cap_drop: ALL`, `no-new-privileges`, runs as `65532`, and
has no host Docker socket and no mount outside the ANAS data tree. Its only writable path is the
session volume, which is what lets a session continue after the container is rebuilt. Forgejo is
reached over its own published HTTPS URL, verified against the mounted internal CA -- there is no
private side channel between the two containers.

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ai_agent.agent_runtime_images` | string | `format: json_object` | `""` | `static` | `AI_AGENT_AGENT_RUNTIME_IMAGES` | no | no | no | yes | `container_recreate` | Runtime id → structured image reference; Core freezes the mapping |
| `ai_agent.agent_runtimes` | string | `pattern: ^(?:[a-z][a-z0-9_-]{0,31}(?:,[a-z][a-z0-9_-]{0,31})*)?$` | `""` | `static` | `AI_AGENT_AGENT_RUNTIMES` | no | no | no | yes | `container_recreate` | Which agent runtimes are enabled; labels, issue templates and capability groups are generated from the registry accordingly |
| `ai_agent.daily_budget_usd` | int | `0..100000` | `20` | `static` | `AI_AGENT_DAILY_BUDGET_USD` | no | no | no | yes | `reconcile` | Deployment-wide daily spend ceiling; a job that would exceed it is interrupted and the reason written back |
| `ai_agent.db_name` | string | — | `ai_agent` | `static` | `AI_AGENT_DB_NAME` | no | no | no | no: `migrate-ai-agent-database` | `data_migrate` | Name of the database holding the orchestration state |
| `ai_agent.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `AI_AGENT_DB_TYPE` | no | no | no | no: `migrate-ai-agent-database` | `data_migrate` | Database interface for the orchestration state; postgres only |
| `ai_agent.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `agent` | `static` | `AI_AGENT_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Hostname prefix of the webhook ingress; changing it re-registers the webhook |
| `ai_agent.egress_allowlist` | string | `pattern: ^(?:[a-z0-9.*-]{1,253}(?::[0-9]{1,5})?(?:,[a-z0-9.*-]{1,253}(?::[0-9]{1,5})?)*)?$` | `""` | `static` | `AI_AGENT_EGRESS_ALLOWLIST` | no | no | no | yes | `reconcile` | The only destinations a work instance may reach; everything else is denied |
| `ai_agent.enabled` | bool | — | `false` | `static` | `AI_AGENT_ENABLED` | no | no | no | yes | `container_recreate` | The single feature switch; turning it on requires the administrative credential, the repository allowlist, pinned images and a compute binding together |
| `ai_agent.execution_isolation` | enum (`auto`, `incus_container`, `incus_vm`) | — | `auto` | `static` | `AI_AGENT_EXECUTION_ISOLATION` | no | no | no | yes | `container_recreate` | Isolation tier requested from the compute provider: a system container shares the host kernel, a VM has its own |
| `ai_agent.job_wallclock_minutes` | int | `1..1440` | `60` | `static` | `AI_AGENT_JOB_WALLCLOCK_MINUTES` | no | no | no | yes | `reconcile` | Hard wall-clock ceiling for one job; exceeding it interrupts the job |
| `ai_agent.language` | string | — | — | `inherited` | `AI_AGENT_LANGUAGE` | no | yes | no | yes | `reconcile` | Language of the status comments and explanations the orchestrator writes into issues |
| `ai_agent.reconcile_interval_seconds` | int | `30..86400` | `300` | `static` | `AI_AGENT_RECONCILE_INTERVAL_SECONDS` | no | no | no | yes | `reconcile` | How often the reconciliation sweep runs to recover deliveries the webhook missed |
| `ai_agent.repository_allowlist` | string | `pattern: ^(?:[A-Za-z0-9._-]{1,64}/[A-Za-z0-9._-]{1,100}(?:,[A-Za-z0-9._-]{1,64}/[A-Za-z0-9._-]{1,100})*)?$` | `""` | `static` | `AI_AGENT_REPOSITORY_ALLOWLIST` | no | no | no | yes | `container_recreate` | Repositories that participate; deliveries for anything else are dropped at ingress |
| `ai_agent.workspace_scope` | enum (`repo`, `job`) | — | `repo` | `static` | `AI_AGENT_WORKSPACE_SCOPE` | no | no | no | yes | `container_recreate` | One long-lived work instance per repository, or one disposable instance per job |

All fourteen reach the control plane container through `.env`. `enabled` is the single feature switch
and the hook decides its four preconditions early in apply; the rest are either ingress and execution
boundaries (allowlists, image fingerprints, isolation tier) or hard ceilings (budget, wall clock).

## How the control plane is split

| File | Responsibility |
| --- | --- |
| `config.go` | Reads the environment, parses repositories and image fingerprints, fails closed; every `enabled` precondition is decided here |
| `registry.go` | The agent runtime registry: models, native effort levels, capabilities, image, capability group name |
| `forgejo.go` | The admin API: accounts, tokens, SSH keys, system webhook, reconciliation reads |
| `ingress.go` | Signature, allowlist, self-trigger filter, inbox write, `202` |
| `reconcile.go` | The periodic sweep and the outbox idempotency key |
| `bootstrap.go` | Identity issue and rotation, webhook registration |
| `postgres.go` / `store.go` | Authoritative state: inbox, outbox, identities, webhook, cursors, audit |
| `redact.go` | Outbound scrubbing |

## Why the control plane does not know any runtime's name

`registry.go` is the only file that contains `codex`, `claude_code` or `pi`; everything else reads
values off a `Runtime`. Adding a runtime takes three things: a registry entry, an adapter, and an
image with a pinned fingerprint. Labels, issue templates and capability group names
(`CAP_ai_agent_<id>`) are all generated from the registry.

Thinking effort deliberately has **no shared vocabulary**. Runtimes differ in both the names and the
number of levels, so a forced mapping invents levels that do not exist on some runtime and implies
that "high" means one thing everywhere. `EffortLevels` therefore holds each runtime's native values,
and a rejected value is reported together with the alternatives. A runtime that does not grade effort
(`pi`) leaves the field empty and the template omits it.

`TestControlPlaneDoesNotBranchOnRuntimeName` scans the package's own source: any runtime id appearing
outside `registry.go` fails the test.

## Five things the pinned version taught this code

None of these came from the design; they came from probing `forgejo 15.0.7`
([requirement §15](../dev-docs/requirements/ai-agent.md)). Each one overturned an assumption
that was already written:

| Upstream fact | Where it lands |
| --- | --- |
| There is no `/admin/users/{u}/tokens`; minting happens at `/users/{u}/tokens` and refuses token auth | `tokenPath` plus `sudo()`; the administrative credential must therefore be a password |
| `repositories` is a list of `{owner,name}` objects; a string fails to unmarshal | `ForgejoTarget` |
| A repository-limited token may only carry issue and repository scopes | the consistency test between `discussionScopes` and `repositoryScopedScopes` |
| Token names and key titles are unique per user | `tokenNameFor` / `keyTitleFor` carry the generation |
| `GET /admin/hooks` answers empty while the id lookup works, and the server expands event families | `SystemHook(id)` for existence, URL and fingerprint for drift |

`fakeAdmin` reproduces the last three (name uniqueness, empty listing, event expansion), so these are
not "only findable against a real server" any more -- they fail locally now.

## Idempotency: two keys for two different problems

One external side effect happening once rests on two different keys, because there are two different
things to prevent:

| Prevented | Key | Where |
| --- | --- | --- |
| The same delivery arriving more than once | `X-Forgejo-Delivery` -> `inbox_event` primary key | `Ingress.Accept` |
| The same change arriving by webhook and by reconciliation | `<repo>#<issue>:<kind>:<discriminator>` -> `outbox_write` primary key | `Outbox.Do` |

The first alone is not enough: a reconstructed event carries its own deterministic delivery id, which
differs from the webhook's, so it always reaches the inbox. What collapses the external write to one
is the outbox key. The second alone is not enough either -- every repeat delivery would re-run the
whole handler to discover there is nothing to do. Both are needed.

A reconciled delivery id is derived from repository, issue number and `updated_at`, so sweeping an
unchanged issue twice produces the same id and is deduplicated by the inbox; a random id would turn
every sweep into an event storm.

## Why rotation issues before it revokes

The order is: issue the replacement, confirm it is stored, revoke the predecessor. A failure at any
step revokes the credential just issued.

What triggers it is the age check inside `Bootstrapper.Reconcile`, not an external schedule:
AGENT-R-007 asks for unattended rotation, and "there is an entry point you could call" is not the
same as "it gets called".

The trade-off is explicit. A failure at the last step leaves the old credential live, which the next
reconciliation can see and converge. Revoking first would mean that any failure in between leaves an
account with **no usable credential at all** and needs a person. One stale credential awaiting
convergence beats one broken identity. What must never happen is two live credentials, which is why
the failure path always reclaims the new one.

## Scrubbing is by value, not by call site

`Redactor` holds the known secret values and every outbound string -- a log line, an error, an audit
reason -- passes through it. This rather than "being careful at each call site", because the latter is
not an enforcement: an HTTP transport error echoes the request URL and a database driver prints the
connection string, and neither is a call site anyone thinks of. Values shorter than 8 bytes are not
registered: a secret that short is guessable anyway, and redacting it would blank out unrelated text.

## Directory attribute changes — implementation

One-to-one with the README's *Directory attribute changes*. This is about **people** only; Agent
machine accounts do not come from the directory.

- **Which table and field persist identity**: four places in `orchestrator/postgres.go`, all keyed by
  the Forgejo username string: `agent_grant.username` (PRIMARY KEY), `agent_grant_deny.username`
  (unique together with `agent` as `agent_grant_deny_subject`), `policy_override.username`, and
  `audit_record.subject`.
- **Where the matching key comes from**: the webhook envelope in `orchestrator/ingress.go` decodes
  only `sender.login`. The orchestrator parses no ID Token and consumes no IAM binding — everything it
  knows about a person passes through Forgejo.
- **When it is re-derived**: `PolicyEngine.grant` keeps an in-memory snapshot and, once past its TTL,
  calls `Permissions.UserTeams(ctx, user)` to re-read Forgejo team names, hands them to
  `GrantFromTeams` to recompute the ceiling, and writes it back with `SaveGrant`. `agent_grant` is
  therefore a **derived snapshot, not a source of truth** — `Grant.Source` literally reads
  `"directory groups projected into Forgejo teams"`.
- **Which interface performs revocation**: `PolicyEngine.Veto` writes `agent_grant_deny` and drops any
  cached ceiling that would contradict it, backing the `agent-grant deny` command. No directory-side
  interface can trigger it.
- **Reconciliation or event-subscription path**: the orchestrator **needs** directory-event immediacy
  (group revocation within seconds, see `docs/architecture/orchestration-design.md` §6.2), but
  `forgejo` is OIDC-only and keeps no directory replica, so groups can only reach teams through a
  claim at login. The immediate veto table exists as the **manual fast path** that fills that hole.
- **Technical obstacle**: **no immutable person identifier reaches the orchestrator**. Forgejo's
  OAuth2 source stores the OIDC `sub` in `login_name`, but the webhook payload does not carry that
  field — only the admin API echoes it — so identifying people by anchor would need an extra admin
  user lookup after each webhook to turn `login` into `login_name`. That route only becomes meaningful
  once `DIRKEY-R-008` lands; before then `login_name` holds a Provider-internal id, which equally
  cannot be reconciled against the directory.

**`DIRKEY-R-002` compliance: not compliant; the gap is declared per `DIRKEY-R-004`.** The persistent
authorization key is the Forgejo username, a directory label. This Module does **not** pass off
"fall back to matching by username" as satisfying the requirement; it records the consequences and the
fallback explicitly (see the README). Remediation follows M2 of the
[directory identity key plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md):
only once `sub` is the anchor, and `login_name` therefore holds the anchor, can the orchestrator
change its authorization key. Per `DIRKEY-R-005`, re-check whether the webhook has begun carrying a
stable identifier whenever `forgejo`'s pinned version changes.

**Scope of the `DIRKEY-R-006` exception**: `agent_grant_deny` and `policy_override` only ever revoke
(the `Override` comment states that the "final action set is an intersection",
`AGENT-R-031`/`R-036`), so they are permitted to match by label; the cost of a misjudgment is an
erroneous revocation, and recovery is an administrator recreating the entry by hand — **there is no
automatic restoration path in the code**. `agent_grant` is the granting direction and does not fall
under the exception; it bounds the exposure window by re-deriving from the current teams once the TTL
expires.

**`DIRKEY-R-013` projection verdict: not applicable (this Module is not an OIDC/SAML Consumer).**
`module.yml` declares no `iam`, the orchestrator consumes no subject identifier, and the M2 switch
puts no UUID into any of its interfaces or paths. M2 affects it only indirectly: the switch changes
what `forgejo`'s `login_name` contains and thereby **opens** the "identify people by anchor" route
that is currently closed.

## Where each requirement lands

| Requirement | Implementation |
| --- | --- |
| `AGENT-R-001` | `module.yml` pins the `0.1.0-r1` image tag and keeps `status: developing` |
| `AGENT-R-002` | `docker-compose.yml`: no socket, no host mount, `cap_drop: ALL`, non-root |
| `AGENT-R-003` | The schema in `postgres.go` behind the `Store` interface; no authoritative state in process |
| `AGENT-R-004` | `validateEnabled` in the hook and `requireEnabledPreconditions` in `config.go` |
| `AGENT-R-005` | `bootstrap.go` `EnsureUser` |
| `AGENT-R-006` | `discussionScopes`, `CreateToken(repositories)` and `EnsureCollaborator`; `ScopingPerRepoAccount` kept but unused |
| `AGENT-R-007` | `bootstrap.go` `Rotate` and `abandon` |
| `AGENT-R-008` | The hook's `adminAccount`, used only inside the control plane container |
| `AGENT-R-009` | `EnsureWebhook`: existence by id, drift by URL and key fingerprint |
| `AGENT-R-010` | `redact.go`, plus the hook never echoing `local-admin` output |
| `AGENT-R-011` | `ingress.go` `serveWebhook` |
| `AGENT-R-012` | The `inbox_event` primary key behind `RecordDelivery` |
| `AGENT-R-013` | `reconcile.go` `Sweep` |
| `AGENT-R-014` | The `ByAccount` filter plus `RunIDFromPayload` as the second dedupe |

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
