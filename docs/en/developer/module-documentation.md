# Module documentation generation standard

This standard defines Module documentation sources, required content, generation boundaries, VitePress mapping, bilingual output, and CI validation. “Must”, “must not”, and “should” are normative for new Modules, Module upgrades, and generator changes.

> [!IMPORTANT]
> `cmd/gen-module-docs` maintains README quick facts and timezone/language blocks, technical-document identity and Compose-topology blocks, and localization summaries. `cmd/materialize-module-docs` creates per-Module pages, catalogs, navigation data, and bounded version history inside the disposable VitePress source tree. Generated pages are neither written back nor committed under the real `docs/` tree.

## 1. Source layout

Every `modules/<name>/` directory containing `module.yml` must contain:

```text
modules/<name>/
├── module.yml
├── localization.yml
├── README.md
├── README.en.md
└── docs/
    ├── technical.md
    └── technical.en.md
```

| File | Audience | Responsibility |
| --- | --- | --- |
| `module.yml` | Runner and generators | Version, status, dependencies, configuration, database, IAM, admin entries, and other machine contracts |
| `localization.yml` | Generator | Version-bound timezone, language, fallback, and evidence inventory |
| `README.md` | Chinese users and administrators | Dependencies, configuration, login, user management, recovery, and operating commands |
| `README.en.md` | English users and administrators | English equivalent of the Chinese README |
| `docs/technical.md` | Chinese maintainers | Implementation, security boundaries, Secrets, data flow, hooks, Compose, and tests |
| `docs/technical.en.md` | English maintainers | English equivalent of the Chinese technical document |

The Module directory is the sole documentation source of truth. Per-Module VitePress pages exist only in the disposable build tree and final static output and never become an independent implementation source.

## 2. Evidence precedence

Verify documentation facts in this order:

1. Runner, configuration parsing, CLI implementation, and tests;
2. `module.yml`, Compose, hooks, scripts, and Dockerfiles;
3. Contract manifests, schemas, provider/consumer implementations, and tests;
4. pinned upstream source, versioned official documentation, and real-container verification;
5. existing prose, which is only a lead and cannot override current implementation.

Never infer a capability from a name. An LDAP environment variable does not prove user sync, group sync, or password writeback. Unsupported CLI commands must be labeled unavailable instead of being presented as executable proposals.

## 3. Required README content

The Chinese and English READMEs must have equivalent sections covering at least:

1. Module name, version/revision, status, category, and runtime;
2. Module, Capability, and Contract dependencies, interfaces, and version constraints;
3. a minimal example valid under the current `config.yml` schema;
4. LDAPS, OIDC, SAML, Kerberos, user/group source of truth, synchronization direction, filters, password writeback, and both Module-initiated and IAM-initiated logout capability;
5. routine admin login, direct access, private/local admins, and IAM-outage recovery; when an emergency account exists, its login address, actual username, and password-retrieval command;
6. real commands for account inspection, credential retrieval, password rotation, configuration inspection, modification, and planning;
7. database provider/consumer/none, interfaces, defaults, Resource, credential, and deletion policy;
8. every available configuration parameter;
9. storage, backup, verification, limitations, and a technical-document link;
10. the versioned timezone and language generated block.

Keep an explicit “unsupported/not applicable” section when IAM, database, local-admin, or password-writeback support is absent.

### IAM logout documentation

The identity section of every OIDC or SAML Module must also state:

1. whether clicking logout in the Module performs local logout only or continues with OIDC RP-Initiated Logout / SAML SP-Initiated SLO;
2. whether IAM Portal logout reaches the Module through OIDC front-/back-channel or SAML IdP-Initiated SLO;
3. whether browserless administrative IAM-session revocation invalidates the application session;
4. the registered method/binding, endpoint type, and pinned plugin/application version, without tokens or Secrets;
5. the revocation scope based on `sid`, `sub`, or `NameID/SessionIndex`, plus browser, SameSite/CSP, TTL, and retry limitations;
6. the real-session E2E entry point and every uncovered case.

`post_logout_redirect_uri` is navigation only and must not be described as an
IAM-to-application notification endpoint. A README may claim “bidirectional
logout” or “administrative revocation” only after satisfying the
[bidirectional logout requirements](/requirements/module-iam-bidirectional-logout)
with real-session evidence. A ForwardAuth Module documents IAM, gateway, and
backend sessions separately and never projects gateway logout onto the backend.

### Configuration inventory

The README must cover every parameter returned by `anas config list <module> --json`:

| Field | Requirement |
| --- | --- |
| Path | Full path such as `nextcloud.db_type` |
| Type | Type explicitly declared in `config.types` and accepted by the CLI; never inferred from a default |
| Default | Current static default; use `—` for none and `""` for an explicit empty-string default |
| Default source | `default_source`: `none`, `static`, `host`, `runtime`, `generated`, or `inherited` |
| Environment key | Rendered Module-private environment name |
| Input required | `input_required`; `required` is an exactly equal compatibility alias |
| Must resolve | `must_resolve`; whether the final value must be non-empty after defaults and other sources |
| Single-field constraints | Applicable range, length, pattern, or format rules from `constraints`, or `—` |
| Sensitive | Whether it is a Secret |
| Editability | Ordinary `config set` support or the required lifecycle operation |
| Effect | Such as `hot_reload`, `container_recreate`, `credential_rotate`, `data_migrate`, or `immutable` |
| Purpose | Actual application, data, or network impact |

Every configurable parameter in a built-in Module must explicitly declare
`config.types`. `unknown` exists only to read legacy Modules or expose an
incomplete development declaration; the release gate rejects any unknown type
in the built-in inventory.

`input_required` answers only whether the operator must enter a value. It must
be false when a static default or an unconditional host, runtime, generated, or
inherited source exists; `has_default` distinguishes no default from an
explicit empty-string default. `must_resolve` says whether the resolved value
must be non-empty and may be true while `input_required` is false. When a
conditional resolver exists, `default_source: none` can accompany that
combination; it does not mean the operator must directly fill the Module field
in every scenario. Manifest `input_required` means the caller must explicitly
provide a value before resolution begins. Legacy manifest `required` keeps its
existing check after defaults/resolvers and before the calculate Hook, so those
sources may satisfy it; `must_resolve` is checked after the Hook patch. The
CLI/JSON `required` field is not a direct projection of legacy manifest
`required`: it always aliases `input_required`.

`constraints` covers only single-field rules: integer `minimum`/`maximum` and
string `min_length`/`max_length`/`pattern`/`format`. Document conditional
requirements, cross-field relationships, and runtime-dependent rules under
Purpose and enforce them through the resolver, shared application layer, plan,
or Hook.
Do not broadly mark a field required merely to drive a form. This metadata is
the ANAS schema, not JSON Schema; their `required` and `default` semantics
differ. Modules maintain declarations and generic validation rules. The M3
`anasd` configuration API reuses the shared schema and never adds per-Module
HTTP adapters.

Configuration must not exist only in technical documentation. Additions,
removals, renames, type/required changes, and default changes update both
READMEs and both technical documents together.

Configuration tables remain reviewed content. `gen-module-docs` validates the
machine-derived columns in all four tables against the shared inventory, but it
must not rewrite an unmarked table. Purpose, cross-field, migration, and security
semantics remain maintainer-reviewed.

### Administrator and password commands

Only a local administrator declared in `module.yml` with an implemented handler may document:

```bash
anas admin local list -w /srv/anas
anas admin local credential <module> <account-id> -w /srv/anas
anas admin local rotate <module> <account-id> -w /srv/anas
anas admin local rotate <module> <account-id> --prompt -w /srv/anas
```

Explain plaintext output, transaction behavior, prompt input, and failure behavior. A Module without a declared local account must explicitly say these commands are unavailable.

Emergency-account documentation must be sufficient to log in without reading
source code. It includes a complete login URL, or an explicit combination of
`<MODULE_DOMAIN_FULL>` and a fixed path; the Manifest account ID and purpose;
the physical username and whether it is upstream-fixed or derived from the
`admin_{module}` template; and the exact `anas admin local credential` command.
Document how to retrieve the password, never the generated password value. If
there is no emergency account, state that there is no in-application recovery
entry point and identify the real IAM, directory, or host recovery procedure.

Revisions shown in READMEs and technical documents are release state, not a
worktree change counter. Routine feature, fix, and documentation changes do not
increment them manually; the `image-release` workflow calculates and writes
back the authoritative revision. A developer changes every revision projection
temporarily only when a local E2E run must build a distinct test image. Tests
that need no new image do not change the revision.

Never put a password in argv, an ordinary environment variable, or shell history. Do not present `anas config set` as application-password rotation or directory-user password modification.

## 4. Required technical content

Both `docs/technical*.md` files must cover at least:

1. implementation version, status, and scope;
2. Module, Capability, and Contract dependencies;
3. Compose service, image/build, network, and volume topology;
4. the same complete configuration contract as the README;
5. user, group, LDAPS, OIDC/SAML, identity-anchor, password-writeback, and both RP/SP-initiated and IAM-initiated logout session flows;
6. management entries, local admins, and IAM-outage implementation;
7. Secret lifecycle, storage format, permissions, projection path, hash/plaintext boundary, and logging boundary;
8. database Contract, Resource identity, provider/consumer, credentials, and deletion policy;
9. exported and explicitly consumed environment variables;
10. hooks, change executors, transactions, rollback, and compensation;
11. implementation files, unit tests, integration/E2E entry points, and limitations.

The logout flow must identify the application session, central IAM session,
`sid`/`sub` or `NameID`/`SessionIndex`, notification endpoint, signing trust,
replay store, and failure/degradation boundary. OIDC documentation explains
Logout Token signature and claim validation. SAML documentation explains
LogoutRequest/LogoutResponse validation and binding limitations. A
front-channel- or Redirect-only path explicitly depends on the browser and
must not imply coverage of administrative back-office revocation.

`config.env_prefix`, `exports`, and `consumes` must use environment-safe
upper-snake names. A pattern may contain one leading or trailing `*`, never a
bare `*`. Default and custom prefixes owned by different Modules must not be
equal or nested, and must not overlap global, runner-owned, or reserved
`ANAS_*` namespaces. A calculate Hook's env/secret patch is applied only after
the whole patch passes ownership, key-canonicalization, and collision checks;
it cannot overwrite a key owned by another source. Declared parameters are
revalidated through the common type/constraint schema after both `calculate`
and `render_env`; private render-only keys remain supported. A Hook secret may
refresh only an existing `generated/module-hook` record, never
`lifecycle_managed`, `local_admin`, or another provenance, and an atomic
rejection does not echo values or free-form provenance.

### Static credential declarations and lifecycle ABI

Modules establish machine-credential relationships explicitly; Core never
guesses from names such as `PASSWORD`, `TOKEN`, or `KEY`:

```yaml
credentials:
  provides:
    - id: eturnal.secret
      secret_key: TURN_SECRET
      type: shared_secret
      rotation_mode: reconcile
      generation: {kind: hex, length: 16}
      lifecycle:
        probe: probe-eturnal-secret
        reconcile: reconcile-eturnal-secret
        verify: verify-eturnal-secret
  consumes:
    - credential: eturnal.secret
      projection: TURN_SECRET
```

A provider ID must use its Module's lower-case dotted namespace. Secret keys
and projections are upper-snake environment keys, and a consumer projection
must also appear in `config.consumes`. A `reconcile` provider declares a
`password` or `hex` generation policy of at least 16, all three handlers, and
explicit `credential_probe`, `credential_reconcile`, and `credential_verify`
Hook phases. Registry admission checks provider IDs, Secret keys, consumer
projections, and control-edge references for uniqueness and completeness.

After calculate, materialization freezes authority and generation from Secret
Store provenance. A matching-owner `generated/module-hook` record is `anas`;
all other sources are `external`. The deployment manifest stores only IDs,
owner/consumer relationships, authority, generation, projections, handlers,
and policy—never values, hashes, or verifiers. A credential consumer creates a
provider-to-consumer activation edge even without an ordinary dependency.

The credential Hook request carries handler, logical ID, Secret key, authority,
and generation under `credential`. The desired value exists only in stdin JSON
as `secrets.ANAS_CREDENTIAL_DESIRED`, never in metadata, host argv, errors, or
Hook stderr. The only response payload is:

```json
{"credential":{"credential_id":"eturnal.secret","status":"match","changed":false}}
```

Probe/verify statuses are `match`, `missing`, `mismatch`, `unavailable`, or
`unsupported`. A successful reconcile returns `reconciled` with a mutation or
`match` without one. External authority is probe/verify-only; mismatch requires
manual action. Ordinary mutation fields, unknown JSON fields, extra JSON
values, and invalid statuses all fail the ready barrier.

Documentation for an executable `reconcile` provider must show the actual
`anas credential list`, `anas credential rotate <id> --dry-run`, and rotation
commands, and state the affected consumers, downtime scope, and what verify
does and does not prove. Core uses a candidate deployment, value-free journal,
single Store commit, and automatic crash recovery. A stored value or a
probe-only record must never be described as rotatable. Resource credentials,
local administrators, overlap/migrate workflows, and external providers that
have not migrated to the unified executor must be labeled manual/unsupported
and point to their specialized workflow.

### Hook phases and the `validate` ABI

Technical documentation must enumerate the Hook phases a Module actually
implements and keep them aligned with `module.yml`:

```yaml
logic:
  hook:
    command: [go, run, ./hook]
    phases: [validate, calculate, render_env, services, after_start]
```

A non-empty `logic.hook.phases` list is an exact allowlist. Accepted values are
`validate`, `calculate`, `render_env`, `runtime_restore`, `services`,
`after_start`, `local_account_apply`, `local_account_rotate`, and
`local_account_rollback`, plus `credential_probe`, `credential_reconcile`, and
`credential_verify`. Omitting the field retains the legacy non-`validate`,
non-credential compatibility lifecycle; it never assumes that an old Hook
validates configuration or coordinates credentials. An explicit empty list is rejected as ambiguous manifest input.
A Module must explicitly include `validate` in a non-empty list to receive that
request.

Deployment activation processes Modules one at a time in dependency order. The
Runner does not start a downstream Module until the current Module's containers,
owned-credential probe/reconcile/verify, `after_start` Hook, and local-administrator reconciliation have all succeeded.
Technical documentation must not describe `after_start` as an asynchronous
notification sent after every container is already running.

`validate` runs in effective-topology dependency order and only inspects desired
state. Its request has `phase: "validate"`; `env` is the Module-scoped view with
sensitive keys and known equal-value aliases removed, `secrets` is always an
empty object, and `workdir` is empty. The Hook process also inherits only the
minimal environment needed to execute the command. This is an ABI data and
response boundary, not an operating-system sandbox; Module Hooks remain trusted
code that must be reviewed accordingly.

Only `plan` and `warnings` may be non-empty in a `validate` response. Any `env`,
`secrets`, `files`, `runtime_files`, `disable_services`, `docker_copies`, or
`internal_env` mutation rejects the entire validation. Unknown JSON fields,
multiple JSON values, and invalid JSON are also rejected. `warnings` report
recoverable issues and do not fail validation; the runner emits them as
`module_validation_warning`. Their content must remain non-sensitive.

`plan` is read-only, non-sensitive `string -> string` metadata; it never writes
back to configuration or environment. Each Module may return at most 64 entries.
Keys are not trimmed or case-normalized: they must directly match
`^[a-z][a-z0-9_]*$` and must not identify a sensitive parameter. Values are
trimmed, must be non-empty, must not exceed 1024 bytes or contain control
characters, and must not equal plaintext that Core knows is secret. Accepted
values appear under `module_plans.<module>` in deployment and configuration plan
output. Materialization freezes the same values into the Module's
`validation_plan` in the deployment manifest, so technical documentation must
not describe `plan` as transient logging or a mutation instruction.

Technical documentation is maintained inside the Module. The ANAS site publishes mirrors and does not own Module implementation semantics.

## 5. Generated versus reviewed content

Generators may derive or validate Module facts, dependencies, configuration metadata (including default presence/source, input and resolution requirements, and single-field constraints), database and admin declarations, localization inventory, Compose indexes, VitePress pages, catalog entries, and navigation links. The Module configuration audit proves that every built-in Module inventory entry has an explicit type; full release acceptance additionally requires `config list --json` to contain zero `type: "unknown"` entries.

Human or AI-assisted review is required for synchronization direction, group authorization and revocation, password-writeback ACLs, recovery safety, login availability, migration, rotation, rollback, backup semantics, actually enabled upstream behavior, limitations, and runtime conclusions.

Static analysis cannot prove a real login, synchronization, rotation, or recovery. Bind those claims to tests or runtime evidence.

## 6. Generated markers

A generator may modify only explicitly marked blocks. The current blocks are:

```markdown
<!-- generated:module-facts:start -->
...
<!-- generated:module-facts:end -->

<!-- generated:localization:start -->
...
<!-- generated:localization:end -->

<!-- generated:module-identity:start -->
...
<!-- generated:module-identity:end -->

<!-- generated:compose-topology:start -->
...
<!-- generated:compose-topology:end -->
```

`module-facts` and `module-identity` come from `module.yml`, `compose-topology` comes from the manifest-selected Compose file, and `localization` comes from `localization.yml`. Content outside markers is reviewed prose. New blocks use a unique `generated:<section>` name and must detect missing, duplicate, reversed, and unbalanced markers. Never replace an entire reviewed README.

## 7. VitePress output

The documentation build maps sources inside its disposable VitePress tree as follows:

| Module source | Chinese site output | English site output |
| --- | --- | --- |
| `README.md` / `README.en.md` | `/reference/modules/<name>/` | `/en/reference/modules/<name>/` |
| `docs/technical.md` / `docs/technical.en.md` | `/reference/modules/<name>/technical` | `/en/reference/modules/<name>/technical` |

It also generates the bilingual Module catalogs plus a temporary `.vitepress/generated/module-docs.json` used by the sidebar and version links. The existing localization summaries remain checked by `gen-module-docs`.

Every site mirror carries a generated-file warning. Rewrite README-to-technical links so both source and site layouts remain valid. Technical pages may be reached from the user page instead of being flattened into the sidebar.

Catalog and sidebar names, status, category, version, and links come from the same sorted manifest inventory. There is no hand-maintained README path map, and the published set must match `.github/modules.json`.

Historical pages come from immutable `module/<name>/<version>-r<revision>` tags. Selection keeps only the highest revision of each version and then the five newest semantic versions. The four bilingual user/technical bodies are normalized for newlines, trailing whitespace, and release-only labels and hashed together with SHA-256. Identical bodies retain only the newest page and record older releases as aliases. Legacy tags without all four source documents are never filled from newer files and do not produce historical bodies.

## 8. Generator behavior

The two Module documentation commands divide responsibility as follows:

1. enumerate every directory containing `module.yml`;
2. validate directory, manifest, localization, and document ownership;
3. make `materialize-module-docs` fail when a current bilingual source document or `localization.yml` is missing;
4. preflight every marker in all four source documents in memory, then update only allowed generated blocks without leaving partial writes after a missing, duplicate, reversed, or unbalanced marker;
5. let `materialize-module-docs` generate all bilingual pages, catalogs, and navigation data atomically in the disposable tree;
6. use deterministic ordering and formatting;
7. make `gen-module-docs --check` read-only and fail for stale source blocks, stale localization summaries, stale machine-derived columns in any of the four parameter tables, or any built-in Module parameter without an explicit type;
8. audit the union of Module `required`, `defaults`, `types`, and `changes` rather than checking only parameters with defaults; the runner's complete-inventory acceptance covers global parameters;
9. preserve all reviewed content outside markers.

```bash
go run ./cmd/gen-module-docs
go run ./cmd/gen-module-docs --check
npm run docs:build
```

`npm run docs:build` copies `docs/` to a disposable directory, runs `materialize-module-docs`, and then invokes VitePress. It leaves no per-Module mirror in the worktree. `npm run docs:dev` uses the same materialization path.

## 9. Localization inventory

`localization.yml` uses `anas.module-localization/v1`. `module_version` and `module_revision` exactly match `module.yml`; `reviewed_at` is the real review date.

`language.status` is `supported`, `fixed`, or `not_applicable`. `language.selection` is `browser`, `integration`, `application`, `deployment_default`, `fixed`, `client`, or `none`. `global_default` and `global_locale` are `applied`, `fallback`, `not_consumed`, or `not_applicable`.

Use canonical BCP 47 in `supported` and record upstream spellings such as `zh_CN`, `pt-br`, or POSIX locales in `upstream_format`. Prefer evidence from versioned source, then versioned official documentation, exact-image inspection, and finally official marketing material.

For `selection: browser`, upstream keeps the user or browser preference. Unknown values follow the declared fallback. Never cross script variants: `zh-Hant` must not silently match `zh-Hans`. See the [Module upstream upgrade SOP](/en/developer/module-upgrade-sop).

## 10. CI and acceptance

CI must run before the VitePress build:

```bash
go run ./cmd/gen-module-docs --check
npm run docs:build
```

As of 2026-08-22, the built-in release gate fixes a baseline of 22 Modules,
172 parameters, `unknown=0`, two `input_required` entries, 26 final
must-resolve entries, and the exact 23 declared constraints. Tests also prove
that generic set/import/plan/lock/apply paths and calculate/render Hooks use the
same schema, that no Secret Store kind leaks or masquerades as caller input,
and that Hook Secrets cannot be rewritten across Module ownership. Adding a
Module should change only its manifest, inventory, and generated tables—not
`anasd` or an HTTP handler.

Behavior changes also run the relevant Module unit and integration/E2E tests. Commit Module sources and the allowed localization summaries, not per-Module VitePress mirrors.

- [ ] Every `module.yml` directory has four bilingual documents and `localization.yml`.
- [ ] Both languages have the same structure, commands, defaults, support status, and risks.
- [ ] Configuration tables cover every `anas config list <module> --json` parameter, distinguish input-required, must-resolve, and default-source semantics, and the built-in inventory has no `type: "unknown"` entries.
- [ ] Single-field constraints match the shared schema; conditional and cross-field rules are documented and enforced by a resolver, the application layer, plan, or Hook without per-Module API adapters.
- [ ] IAM, LDAPS, groups, administrators, databases, and unsupported cases are explicit.
- [ ] No sensitive value appears in argv, logs, or ordinary environment-variable examples.
- [ ] Generated blocks, temporary pages, catalogs, links, and navigation are current.
- [ ] Generator checks, relevant tests, and `npm run docs:build` pass.
