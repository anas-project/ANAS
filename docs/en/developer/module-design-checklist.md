# Module design and release checklist

> Status: **current baseline**. Updated: 2026-08-21.

Use this checklist when creating, upgrading, or reviewing an ANAS Module. It consolidates the
[Module development standard](/en/developer/module-development),
[Module, Contract, and Resource design](/architecture/module-contract-resource-design),
[Core implementation standard](/en/architecture/core-implementation-standard),
[Module documentation standard](/en/developer/module-documentation), and
[Module upgrade SOP](/en/developer/module-upgrade-sop). The normative source wins if this checklist
ever conflicts with one of those documents.

## How to use it

- `[A]` means an existing test or generator can verify the item, `[M]` requires manual review, and `[E]` requires real-environment evidence.
- Keep non-applicable items and record `N/A` with a reason.
- A `developing` Module may retain explicitly listed `[E]` gaps. Every applicable release gate needs reproducible evidence before promotion to `release`.
- Record at least the Module, upstream version, revision, target status, platforms, database/IAM combinations, review baseline, reviewer, and date.

## 1. Boundaries and ownership

- [ ] `[M]` The functionality is an independent release/deployment unit, not a one-shot operation or internal-only helper.
- [ ] `[M]` Module parameter semantics, derived values, cross-parameter invariants, and upstream adaptation live in the Module; Core contains no product-name or private-env branches.
- [ ] `[M]` Cross-Module behavior uses an explicit Module dependency, Capability, Contract, Resource, or another provider-neutral ABI.
- [ ] `[A/M]` A frozen deployment/Module package contains Compose, hooks, and runtime assets and does not depend on a repository checkout path at runtime.

## 2. Manifest, version, and packaging

- [ ] `[A]` Directory name, manifest `name`, `api_version`, `kind`, runtime, Compose path, and hook ABI are valid and consistent.
- [ ] `[A/M]` `version` is the normalized upstream SemVer; `revision`, image tags, `localization.yml`, and generated projections agree; no runtime image uses `latest`.
- [ ] `[M]` `status` matches the evidence. A Module without real install, upgrade, restore, and security E2E is not presented as stable.
- [ ] `[A]` `.github/modules.json` contains the Module with accurate platforms and `shared_contexts`, and the formal catalog matches the machine catalog.
- [ ] `[M]` `upgrade.from` and `upgrade.data_breaking` separately express source compatibility and on-disk breaks; an audited absence of breaks is written as `data_breaking: []`.

## 3. Dependencies, Contracts, and Resources

- [ ] `[M]` Hard dependencies name only truly required concrete Modules; replaceable providers are resolved through a Capability or Contract.
- [ ] `[A/M]` Contract name, version range, interfaces, `selected_by`, and default provider are accurate; the consumer reads no provider-private file or administrator credential.
- [ ] `[A/M]` Every persistent object has a stable Resource ID, least-privilege principal, separate credential, and explicit deletion policy; consumer removal retains data by default.
- [ ] `[E]` Provider `ensure` is idempotent, and adding a consumer does not restart the provider or existing consumers.

## 4. Configuration schema and change semantics

- [ ] `[A]` Every built-in parameter in the union of `defaults`, `types`, required fields, `must_resolve`, and `changes` has an explicit non-`unknown` type.
- [ ] `[M]` Defaults, `default_source`, input-required, and final-must-resolve semantics match the actual resolution path.
- [ ] `[A/M]` Single-field lengths, ranges, patterns, and formats use `constraints`; conditional or cross-field rules are rejected by a resolver, plan, or `validate` hook rather than repaired by Core.
- [ ] `[M]` Each parameter's effect, editability, explicit operation, and data/credential/container impact are accurate; an unimplemented operation is not documented as an executable command.
- [ ] `[A/M]` `config.exports` and `config.consumes` describe only real boundaries, use safe upper-snake patterns, never use bare `*`, and do not rely on bulk env injection.

## 5. Hooks and lifecycle

- [ ] `[A/M]` A new Module declares an exact `logic.hook.phases` allowlist, and technical documentation matches it instead of relying on the legacy implicit phase set.
- [ ] `[A]` The hook rejects unsupported ABIs, emits one valid JSON value, and never echoes secrets in errors.
- [ ] `[A/M]` `validate` is side-effect free and receives no secrets; `calculate` produces only owned derived values/secrets; `render_env` does not mutate external persistent state.
- [ ] `[A/E]` Initialization, reconciliation, repeated apply, restart, and interrupted retries converge; mutating lifecycle operations document verification, compensation, and rollback.

## 6. Secrets, credentials, and management surfaces

- [ ] `[A/M]` Secrets use stable keys in the Secret/Resource store and do not enter workspace YAML, argv, logs, deployment manifests, or unrelated containers.
- [ ] `[M]` Service identities, Resource credentials, API tokens, and human local accounts are classified correctly; application secrets are not presented as `management.local_accounts`.
- [ ] `[M/E]` Every management surface declares its real authentication topology. Local accounts and apply/rotate are declared only when a direct login, update mechanism, and verification path exist.
- [ ] `[E]` Credential rotation proves the new value works, the old value fails, rollback preserves the old state on failure, and plaintext is not leaked.

### Rotation of ANAS-managed passwords and keys

- [ ] `[A/M]` Inventory every password, shared secret, client secret, and signing/encryption key that ANAS generates, stores, or has authority to update; record owner, consumers, authority, rotation mode, and any non-rotatable reason.
- [ ] `[A/E]` Every rotatable value has a single-target command covering candidate generation, application-state mutation, probe/verify, Secret Store commit, and rollback; merely generating a value or recreating a container is not rotation.
- [ ] `[M/E]` When one Module owns multiple unified-lifecycle credentials, verify the complete set, order, shared consumers/frozen alias projections, downtime, and atomicity of `anas credential rotate --module MODULE`. Mark Resource/local-admin and other unenrolled classes unimplemented rather than presenting several manual commands as one cross-class transaction.
- [ ] `[M/E]` Deployment-wide rotation names every included and excluded credential class and uses one planner/ready barrier. Manual/unsupported entries must become explicit blockers or `manual` exclusions; execution neither rewrites excluded entries nor permits silent partial failure.
- [ ] `[M]` Documentation does not call `anas credential rotate --all` “all ANAS secrets.” It currently covers executable `credentials.provides` reconcile records in the active deployment; Resource credentials, local administrators, and external API tokens retain separate boundaries.

## 7. Compose, image, and runtime security

- [ ] `[A]` Compose parses, and services, images/builds, networks, volumes, and healthchecks match the manifest and documentation.
- [ ] `[M]` Only required ports are exposed. Web traffic uses the managed Traefik/TLS route with no redundant unauthenticated host port.
- [ ] `[M/E]` The business process runs unprivileged. Any root startup work is minimal and followed by an irreversible drop before `exec`; use a read-only rootfs, tmpfs, and minimal writable volumes when upstream permits.
- [ ] `[M]` Persistent paths bind explicitly into managed workspace storage, never anonymous volumes; entrypoints do not follow untrusted symlinks.
- [ ] `[M/E]` Healthchecks cover the real application and required dependencies rather than only a PID, with failure semantics aligned to restart policy.
- [ ] `[M/E]` Internal DNS/CA and upstream TLS verification work without skip-verify as a production path.
- [ ] `[E]` Every declared platform, such as `linux/amd64` and `linux/arm64`, builds, starts, and becomes healthy.

## 8. Data, backup, and upgrades

An upstream-version or runtime-asset change must also complete the
[Module upgrade checklist](/en/developer/module-upgrade-checklist).

- [ ] `[M]` Ownership and host paths are explicit for databases, user files, configuration, secrets, and deployment metadata.
- [ ] `[E]` Backups keep all persistent planes at one recovery point; restore verifies business data, attachments, identity links, and application secrets/tokens.
- [ ] `[E]` Fresh install, minimum-supported and previous-version upgrades, repeated apply, restart, interrupted retry, and required rollback are tested.
- [ ] `[M/E]` Data-migration parameters are not presented as automatic migration; custom upgrade scripts have an applicability range, idempotence evidence, and removal condition.

## 9. IAM and administrator recovery (conditional)

- [ ] `[A/M]` An IAM consumer reads only its `ANAS_IAM_BINDING__<APP>__*` and publishes only its `ANAS_IAM_CLIENT__<APP>__*`, with no provider-name branch.
- [ ] `[M/E]` Client type, redirect URI, scopes, claims, group gate, JIT/sync direction, and local-auth state match the pinned upstream version.
- [ ] `[M]` Module-to-IAM, IAM-to-Module, and browserless administrator revocation are documented separately; `post_logout_redirect_uri` is not called a notification endpoint.
- [ ] `[A/M]` When the pinned version lacks a standard logout receiver, `OIDC_LOGOUT_*`/SAML SLS is omitted and a generic `/logout` path is not guessed.
- [ ] `[E]` Real browser tests retain the original application cookie and verify the application session, central IAM session, group allow/deny, IAM-down behavior, and every claimed logout direction.
- [ ] `[M/E]` When no local route can bypass IAM, the manifest does not invent a local account and the README gives the real IAM-outage recovery path.

## 10. Relational-database consumers (conditional)

- [ ] `[A/M]` A dual PostgreSQL/MariaDB consumer uses `relational_database`; public interfaces are `postgres`/`mariadb`/`auto`, and `mysql` appears only as an internal application mapping.
- [ ] `[A/M]` The hook consumes only `<PREFIX>_DB_*` and `<PREFIX>_NETWORK_DB`; Compose joins only the selected provider network and receives no provider root/admin credential.
- [ ] `[A/E]` Unit tests cover both interfaces, dedicated credentials, and network mapping; release evidence covers empty install, restart, and repeated apply on both engines.

## 11. Timezone and localization

- [ ] `[A]` `localization.yml` Module name, version, revision, statuses, and BCP 47 tags pass generator checks.
- [ ] `[M]` The language list comes from pinned source, versioned official documentation, or the exact image, never only a rolling marketing page.
- [ ] `[A/E]` Unsupported-language warning/fallback, script boundaries, one non-English language, a non-UTC timezone, and a DST case are verified.

## 12. Documentation and global inventories

- [ ] `[A]` `README.md`, `README.en.md`, both `docs/technical*.md` files, and `localization.yml` exist, with equivalent support status in both languages.
- [ ] `[A/M]` User and technical docs cover every parameter, dependency, identity path, local-account result, database, secret, storage, backup, hook, test, and current limitation.
- [ ] `[A]` Module catalog, localization matrix, Contract consumer list, configuration counts, IAM/env references, and the built-in Module architecture inventory are synchronized.
- [ ] `[A]` Generators do not overwrite reviewed semantics outside markers, and internal links plus both language navigations build.

## 13. Suggested static validation

Run from the repository root:

```bash
go test ./internal/modulepackage ./internal/runner
go test ./modules/<name>/hook
go run ./cmd/gen-module-docs --check
go run ./cmd/gen-contract-docs --check
docker compose -f modules/<name>/docker-compose.yml config --no-interpolate --no-path-resolution -q
go test ./...
npm run docs:build
git diff --check
```

Also test standalone helper/entrypoint packages and Linux amd64/arm64 compilation when present. Passing
commands prove mechanical contracts only; they do not replace real login, restore, upgrade, or data-safety
evidence. Docker/E2E must use an isolated non-production daemon, workspace, Compose project prefix, and
port range.

## 14. Review result template

| Category | Result | Evidence or remaining work |
| --- | --- | --- |
| Manifest and packaging | Pass / Fail / N/A |  |
| Ownership and dependencies | Pass / Fail / N/A |  |
| Configuration and hooks | Pass / Fail / N/A |  |
| Secrets and runtime security | Pass / Fail / N/A |  |
| Data, upgrade, and restore | Pass / Fail / N/A |  |
| IAM/database/localization | Pass / Fail / N/A |  |
| Documentation and automated gates | Pass / Fail / N/A |  |
| Real-environment release gates | Pass / Fail / N/A |  |

Use only **pass**, **conditional pass (list every condition)**, or **fail (list every blocker)** as the final result.
