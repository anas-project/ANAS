# Contract documentation generation standard

This standard defines Contract source files, machine contracts, reviewed semantics, generated output, VitePress mapping, bilingual content, and CI validation. Together with the [Module documentation generation standard](/en/developer/module-documentation), it is the maintenance baseline for Module and Contract documentation.

> [!NOTE]
> The current `cmd/gen-contract-docs` implements the README generated block and bilingual user/technical VitePress mirrors. Generated Contract catalogs, sidebar data, and CI `--check` enforcement are required by this standard but are not yet implemented.

## 1. Source layout

Every `contracts/<name>/` must contain:

```text
contracts/<name>/
├── contract.yml
├── documentation.yml
├── schemas/
│   └── *.yml
├── README.md
├── README.en.md
└── docs/
    ├── technical.md
    └── technical.en.md
```

| File | Responsibility |
| --- | --- |
| `contract.yml` | Name, version, interfaces, Resource identity, operations, and schema paths |
| `schemas/*.yml` | Resource, request, and result types, required fields, properties, and constraints |
| `documentation.yml` | Documentation API, implementation status, review date, and bilingual summary |
| `README.md` / `README.en.md` | Consumer-facing semantics, capability, examples, errors, and operating boundaries |
| `docs/technical.md` / `docs/technical.en.md` | Provider/Consumer implementation, idempotency, Secrets, lifecycle, compatibility, and tests |

`contracts/<name>/` is the sole source of truth. Files under `docs/reference/module-contracts/` are generated mirrors and must not be edited directly or contain independent facts.

## 2. Naming, identity, and versioning

The directory name, `contract.yml` name, `documentation.yml` contract, and Module-manifest Contract references must agree.

Contracts use semantic versioning. Compatible field additions may retain the major version. Removing or renaming fields, tightening constraints, changing Resource identity, or changing operation semantics requires a new major version. Document compatibility, deprecation, and migration.

Contracts do not have independent release tags or a separate documentation version axis. A published site reads Contract sources, schemas, and Module manifests from the selected ANAS `vMAJOR.MINOR.PATCH` tag, so Contract pages and Provider/Consumer matrices are released and archived with ANAS Core. A Module release must not advance Contract page content on its own.

Resource identity must be stable, serializable, and able to distinguish multiple Resources for one Consumer. Do not treat a display name or mutable address as stable identity.

## 3. `documentation.yml`

```yaml
api_version: anas.contract-documentation/v1
contract: example
status: implemented
reviewed_at: 2026-08-14
summary:
  zh: 中文摘要。
  en: English summary.
```

Allowed status values are:

- `implemented`: Runner dispatch, at least one Provider, and real E2E verification exist;
- `partial`: only some operations, interfaces, or Provider/Consumer paths work;
- `pending`: the definition exists but the Runner does not dispatch it;
- `proposal`: ADR, necessity, or interface review is still required;
- `deprecated`: compatibility only; no new Consumer may use it.

A manifest, schema, Provider name, or test stub does not prove `implemented`. `reviewed_at` is the semantic review date, not the generation date.

## 4. Required README content

Chinese and English READMEs must have equivalent structures covering at least:

1. business purpose, scope, and exclusions;
2. version, status, review date, and interfaces;
3. Resource identity, ownership, and lifecycle semantics;
4. purpose, required flag, input, output, idempotency, and error boundary for every operation;
5. current Providers, Consumers, version constraints, interfaces, and implementation status;
6. user-visible credential, Secret, permission, and deletion boundaries;
7. copyable examples without real Secrets;
8. verification, recovery, unimplemented behavior, and limitations;
9. a technical-document link.

Examples for `pending` or `proposal` Contracts must say they are not currently executable. An unavailable optional operation must report or document unsupported behavior, never pretend success.

## 5. Required technical content

Both `docs/technical*.md` files must cover at least:

1. full manifest and schema index;
2. Resource identity, ownership, idempotency key, and persistence;
3. operation requests/results, preconditions, transaction order, and state transitions;
4. Provider dispatch, Consumer binding, version selection, and lock/deployment state;
5. Secret generation, storage, projection, rotation, revocation, and logging boundaries;
6. permission, network, process, and trust boundaries;
7. delete/retain, compensation, retry, rollback, and disaster recovery;
8. compatibility, upgrade, deprecation, and migration strategy;
9. Provider/Consumer implementation, Runner dispatch, unit tests, and real-service E2E locations;
10. limitations, unimplemented operations, and verification gaps;
11. documentation-generation sources and review method.

Technical documentation explains why and how. Schemas own field contracts; technical prose owns cross-operation security and lifecycle semantics.

## 6. Generated versus reviewed content

The generator derives the content between `generated:contract-reference` markers from `contract.yml`, schemas, Module manifests, and `documentation.yml`:

- version, status, review date, and interfaces;
- Resource identity and schema;
- operation required flags and request/result schemas;
- schema types, required fields, and all properties;
- current Providers, Consumers, version constraints, interfaces, and implementation locations.

Reviewed content outside the block owns purpose, real identity semantics, idempotency, transactions, locks, retries, compensation, deletion, Secrets, permissions, logging, trust, compatibility, migration, recovery, limitations, and runtime/E2E conclusions.

Static analysis cannot prove state change, reliable rollback, or effective permissions. A generator must not promote declarations into unverified runtime conclusions.

## 7. Markers and editing

README generated blocks use:

```markdown
<!-- generated:contract-reference:start -->
...
<!-- generated:contract-reference:end -->
```

Only this block may be replaced. A missing block may be appended; duplicate, reversed, or unbalanced markers fail. README content outside the block and `docs/technical*.md` are reviewed sources.

Generated files under `docs/reference/module-contracts/` carry a do-not-edit warning. Fix the Contract source or generator and regenerate instead of patching a mirror.

## 8. VitePress output

Each Contract generates four pages:

| Contract source | Chinese site output | English site output |
| --- | --- | --- |
| `README.md` / `README.en.md` | `docs/reference/module-contracts/<name>.md` | `docs/en/reference/module-contracts/<name>.md` |
| `docs/technical.md` / `docs/technical.en.md` | `docs/reference/module-contracts/<name>-technical.md` | `docs/en/reference/module-contracts/<name>-technical.md` |

Rewrite `docs/technical*.md` links in README mirrors to the site technical page and rewrite `../README*.md` links in technical mirrors to the site user page. Both source and site layouts must be dead-link free.

Contract catalog and sidebar names, status, version, and links should come from one sorted Contract inventory. User pages must be discoverable from the sidebar or catalog; technical pages may be reached from user pages.

## 9. Generator behavior

`cmd/gen-contract-docs` must:

1. enumerate every directory containing `contract.yml`;
2. strictly validate required sources, API versions, names, status, version, interfaces, operations, and schema paths;
3. verify that operation schemas and references resolve;
4. scan all Module manifests for the Provider/Consumer matrix;
5. modify only README generated blocks;
6. atomically generate all bilingual user pages, technical pages, catalogs, and navigation data;
7. use deterministic ordering and Markdown formatting;
8. make `--check` read-only and fail for missing sources, missing mirrors, or stale output;
9. never infer implementation status from names or file presence.

```bash
go run ./cmd/gen-contract-docs
go run ./cmd/gen-contract-docs --check
npm run docs:build
```

`npm run docs:build` builds pages already under `docs/`; it does not run the Contract generator.

## 10. Bilingual output and technical identifiers

Both languages must have the same sections, operation set, support status, defaults, risks, and limitations. Do not translate Contract, Module, Provider, Consumer, or Resource names; interfaces; operations; fields; schema paths; commands; environment variables; status values; error codes; or version constraints.

Translate explanatory prose only. A change to one language updates the other language and all generated mirrors in the same change set.

## 11. CI and acceptance

CI must run before the VitePress build:

```bash
go run ./cmd/gen-contract-docs --check
test-env/scripts/test-contract.sh
npm run docs:build
```

When a Contract lacks one shared test entry point, run the relevant Provider/Consumer tests and real-service E2E. Promotion to `implemented` requires Provider/Consumer manifests, Runner dispatch, operation tests, and E2E evidence in the same change.

- [ ] Every Contract has a manifest, documentation metadata, schemas, and four bilingual documents.
- [ ] Names, versions, interfaces, identity, operations, and schema references agree.
- [ ] Both languages state identical semantics, support status, risks, and limitations.
- [ ] The Provider/Consumer matrix comes from current Module manifests.
- [ ] Secret, permission, idempotency, deletion, compensation, and recovery boundaries are explicit.
- [ ] Pending, proposed, or unimplemented behavior is not presented as available.
- [ ] Generated blocks, VitePress mirrors, links, catalogs, and navigation are current.
- [ ] Generator checks, Contract tests, and `npm run docs:build` pass.
