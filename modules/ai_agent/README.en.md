# AI Agent orchestration

Lets a team discuss requirements with AI agents inside Forgejo issues, land the conclusions in the
repository, and -- only after an explicit approval -- have an agent change code, run tests and open a
pull request from an isolated instance. Every step is authorized, audited, interruptible and costed.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `ai_agent` |
| Version / revision | `0.1.0-r1` |
| Status | `developing` |
| Category | `app` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## How far the implementation has got

The module lands milestone by milestone against its [plan](dev-docs/plans/ai-agent.md).
**M1, M2, M3 and M5 are complete**:

- M1: the module skeleton, Compose topology, hook and configuration contract; unattended issue and
  rotation of each agent's Forgejo account, token and SSH key; system webhook registration,
  signature-checked ingress, an inbox, a fast `202`, periodic reconciliation and self-trigger filtering;
- M2: issue form template generation and answer parsing, status comments and commands, reaction
  acknowledgements, serialized turns, documents committed through the contents API, frozen execution
  inputs and the onboarding issue;
- M3: repository-permission derivation, `CAP_ai_agent_*` directory-group projection, the immediate
  veto table, decision auditing and the re-check before a job starts;
- M5: pre-execution estimates and `due_date` checks, queue ordering, the `now`/`at`/`on`/`hold`
  timings, tracked-time write-back and the pinned queue issue.

**Not yet implemented**: M4 (execution instances, branches and pull requests, execution issues,
cancellation and idempotency), M6 (records and session views), M7 (real deployment acceptance) and
M8 (the added interaction and safety constraints). M4 is blocked on real-host acceptance of the
`compute` provider, so today the module discusses, produces documents, decides permissions and
queues work, but **never runs a code job**. Its status therefore stays `developing`.

## Boundaries

It does **not**:

- take the host Docker socket, a host directory mount, or any privilege;
- run model-generated code on the ANAS core host -- that only happens inside a disposable instance
  leased through the `compute` contract;
- write the administrative credential, an agent token or an SSH private key into an issue, a comment,
  a log, an image or a deployment manifest;
- merge pull requests or approve high-risk changes on its own.

## Modules, capabilities and contracts it depends on

| Dependency | Kind | Interface / version |
| --- | --- | --- |
| `forgejo` | Module | The collaboration surface: issues, labels, comments, repositories, identities |
| `traefik` | Module | Publishes the webhook ingress address |
| `relational_database` | Consumed contract | `>=1.0.0 <2.0.0` / `postgres` |
| `compute` | Consumed contract (only while `enabled`) | `>=1.0.0 <2.0.0` / `incus_container`, `incus_vm` |

PostgreSQL only: the orchestration state uses array and JSONB columns and job leases, and carrying a
second dialect for a control plane with no MariaDB deployment to serve would not pay for itself.

## What has to be in place before it can be turned on

`enabled` is the single feature switch, but turning it on needs four things at once. The hook refuses
to apply if any of them is missing:

1. **a Forgejo administrative credential** -- generated and held by ANAS; the hook creates the
   `anas_ai_agent` administrator through Forgejo's own managed-account entrypoint at `after_start`,
   with no operator step;
2. **a repository allowlist** (`repository_allowlist`) -- a repository that is not listed has its
   deliveries dropped at ingress;
3. **a pinned image fingerprint per enabled runtime** (`agent_runtime_images`) -- SHA-256 only, never
   a tag;
4. **a `compute` binding** -- an approved job needs somewhere to run.

```yaml
modules:
  ai_agent:
    enabled: true
    repository_allowlist: "anas-project/ANAS,anas-project/anas-agent"
    agent_runtimes: "codex,claude_code"
    agent_runtime_images: "codex=<64-hex fingerprint>,claude_code=<64-hex fingerprint>"
    execution_isolation: incus_container
```

## Identity and credentials

Every agent gets its own Forgejo account (`agent-<id>`), created unattended through the admin API and
never used to sign in. Its token is limited by scope and by repository: during discussion it holds
only `read:repository`, `write:issue` and `read:user`, and write access to a repository is added for
the duration of an execution job.

The pinned `forgejo 15.0.7` was probed and **does support repository limiting**, so the intended model
applies and the degradation is not in use. The same probe pinned down three boundaries:

- tokens are minted at `POST /users/{u}/tokens` -- `/admin/users/{u}/tokens` is a 404 -- and only with
  the administrator's **basic auth** plus a `Sudo: <account>` header;
- a repository-limited token may carry only `read:issue`, `write:issue`, `read:repository` and
  `write:repository`; one more scope makes it a 400, so the discussion scope set cannot grow;
- the agent has to be a `read` collaborator before the token can name the repository, or upstream
  reports the repository as non-existent.

The limit applies to repository **content and writes**: an out-of-scope repository answers 403 on
contents and 404 on opening an issue, but its **metadata still reads** (`GET /repos/{o}/{r}` is 200).
It is a least-privilege mechanism, not a way to hide a repository.

The degradation (one account per agent per repository, `agent-<id>-<repo digest>`) stays behind
`TokenScoping` for a future version that withdraws the capability.

Rotation issues the replacement, confirms it is stored, and only then revokes the predecessor. A
failure at any step revokes the credential just issued, so a failed rotation leaves exactly one live
credential and never two. It is driven by **credential age**, not by anyone remembering: the periodic
reconciliation replaces any token or SSH key older than 30 days. Names carry the generation
(`anas-ai-agent-g<N>`): upstream requires a token name and a key title to be unique per user, and
issuing before revoking necessarily makes the two coexist, so reusing the name fails at the first step.

## Event ingress

One Forgejo system webhook covers the whole instance. Ingress applies these in order, and no step
past a failure runs any business logic:

1. verify `X-Forgejo-Signature` (HMAC-SHA256 over the body, compared in constant time; an empty
   secret is refused outright);
2. repository not on the allowlist -> drop;
3. sender is one of this deployment's agent accounts -> drop;
4. payload carries a run marker this orchestrator wrote -> drop (the second self-trigger guard);
5. insert into `inbox_event`, keyed by the delivery id, so a repeat cannot write a second row;
6. answer `202`.

No business logic runs on the request path. **A repeated delivery producing one external side effect**
rests on two things: the delivery-id primary key covers "the same delivery arrived twice", and the
outbox idempotency key covers "the same change arrived once by webhook and once by reconciliation".

Reconciliation sweeps `updated_at` per repository behind a cursor to recover deliveries the webhook
never made, through the same filtering and dedupe path.

## Configuration

See the `ai_agent.*` section of the [configuration reference](../../docs/en/reference/configuration.md)
and the [technical notes](docs/technical.en.md).

## All configuration parameters

The list below comes from the current `module.yml` and `anas config list`. `Environment` is the
rendered module-private key; it is not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ai_agent.agent_runtime_images` | string | `pattern: ^(?:[a-z][a-z0-9_-]{0,31}=[0-9a-f]{64}(?:,[a-z][a-z0-9_-]{0,31}=[0-9a-f]{64})*)?$` | `""` | `static` | `AI_AGENT_AGENT_RUNTIME_IMAGES` | no | no | no | yes | `container_recreate` | Pinned SHA-256 image fingerprint per enabled runtime; a tag is refused |
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

### Inspecting and changing

```bash
anas config list ai_agent -w /srv/anas
```

```bash
anas config set ai_agent.repository_allowlist "anas-project/ANAS" -w /srv/anas
```

## Language and timezone

<!-- generated:localization:start -->
<!-- generated:localization:end -->
