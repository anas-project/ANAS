# Module documentation generation standard

This standard defines Module documentation sources, required content, generation boundaries, VitePress mapping, bilingual output, and CI validation. “Must”, “must not”, and “should” are normative for new Modules, Module upgrades, and generator changes.

> [!IMPORTANT]
> `cmd/gen-module-docs` maintains the timezone/language block and localization summaries. `cmd/materialize-module-docs` creates per-Module pages, catalogs, navigation data, and bounded version history inside the disposable VitePress source tree. Generated pages are neither written back nor committed under the real `docs/` tree.

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
4. LDAPS, OIDC, SAML, Kerberos, user/group source of truth, synchronization direction, filters, and password writeback;
5. routine admin login, direct access, private/local admins, and IAM-outage recovery;
6. real commands for account inspection, credential retrieval, password rotation, configuration inspection, modification, and planning;
7. database provider/consumer/none, interfaces, defaults, Resource, credential, and deletion policy;
8. every available configuration parameter;
9. storage, backup, verification, limitations, and a technical-document link;
10. the versioned timezone and language generated block.

Keep an explicit “unsupported/not applicable” section when IAM, database, local-admin, or password-writeback support is absent.

### Configuration inventory

The README must cover every parameter returned by `anas config list <module> --json`:

| Field | Requirement |
| --- | --- |
| Path | Full path such as `nextcloud.db_type` |
| Type | Type accepted by the current CLI |
| Default | Current default or `—` |
| Environment key | Rendered Module-private environment name |
| Required | Whether the value is required |
| Sensitive | Whether it is a Secret |
| Editability | Ordinary `config set` support or the required lifecycle operation |
| Effect | Such as `hot_reload`, `container_recreate`, `credential_rotate`, `data_migrate`, or `immutable` |
| Purpose | Actual application, data, or network impact |

Configuration must not exist only in technical documentation. Additions, removals, renames, and default changes update both READMEs and both technical documents together.

### Administrator and password commands

Only a local administrator declared in `module.yml` with an implemented handler may document:

```bash
anas admin local list -w /srv/anas
anas admin local credential <module> <account-id> -w /srv/anas
anas admin local rotate <module> <account-id> -w /srv/anas
anas admin local rotate <module> <account-id> --prompt -w /srv/anas
```

Explain plaintext output, transaction behavior, prompt input, and failure behavior. A Module without a declared local account must explicitly say these commands are unavailable.

Never put a password in argv, an ordinary environment variable, or shell history. Do not present `anas config set` as application-password rotation or directory-user password modification.

## 4. Required technical content

Both `docs/technical*.md` files must cover at least:

1. implementation version, status, and scope;
2. Module, Capability, and Contract dependencies;
3. Compose service, image/build, network, and volume topology;
4. the same complete configuration contract as the README;
5. user, group, LDAPS, OIDC/SAML, identity-anchor, and password-writeback data flow;
6. management entries, local admins, and IAM-outage implementation;
7. Secret lifecycle, storage format, permissions, projection path, hash/plaintext boundary, and logging boundary;
8. database Contract, Resource identity, provider/consumer, credentials, and deletion policy;
9. exported and explicitly consumed environment variables;
10. hooks, change executors, transactions, rollback, and compensation;
11. implementation files, unit tests, integration/E2E entry points, and limitations.

Technical documentation is maintained inside the Module. The ANAS site publishes mirrors and does not own Module implementation semantics.

## 5. Generated versus reviewed content

Generators may derive or validate Module facts, dependencies, configuration metadata, database and admin declarations, localization inventory, Compose indexes, VitePress pages, catalog entries, and navigation links.

Human or AI-assisted review is required for synchronization direction, group authorization and revocation, password-writeback ACLs, recovery safety, login availability, migration, rotation, rollback, backup semantics, actually enabled upstream behavior, limitations, and runtime conclusions.

Static analysis cannot prove a real login, synchronization, rotation, or recovery. Bind those claims to tests or runtime evidence.

## 6. Generated markers

A generator may modify only explicitly marked blocks. The existing localization block is:

```markdown
<!-- generated:localization:start -->
...
<!-- generated:localization:end -->
```

Content outside markers is reviewed prose. New blocks use a unique `generated:<section>` name and must detect missing, duplicate, reversed, and unbalanced markers. Never replace an entire reviewed README.

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
4. update only allowed generated blocks;
5. let `materialize-module-docs` generate all bilingual pages, catalogs, and navigation data atomically in the disposable tree;
6. use deterministic ordering and formatting;
7. make `gen-module-docs --check` read-only and fail for stale source blocks or localization summaries;
8. preserve all reviewed content outside markers.

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

Behavior changes also run the relevant Module unit and integration/E2E tests. Commit Module sources and the allowed localization summaries, not per-Module VitePress mirrors.

- [ ] Every `module.yml` directory has four bilingual documents and `localization.yml`.
- [ ] Both languages have the same structure, commands, defaults, support status, and risks.
- [ ] Configuration tables cover every `anas config list <module> --json` parameter.
- [ ] IAM, LDAPS, groups, administrators, databases, and unsupported cases are explicit.
- [ ] No sensitive value appears in argv, logs, or ordinary environment-variable examples.
- [ ] Generated blocks, temporary pages, catalogs, links, and navigation are current.
- [ ] Generator checks, relevant tests, and `npm run docs:build` pass.
