# Module upstream upgrade SOP

Use this SOP when upgrading a Module's upstream version or runtime assets. The upgrade must be repeatable, declare its compatibility boundary, and retain the required recovery path.

## 1. Pin the scope and inspect upstream

Record the application version, image tag/digest, Module `version`/`revision`, bundled Web UIs, and other runtime components. Review bundled components such as Adminer or NetBird Dashboard separately. Read the release notes, migration notes, and configuration documentation between the source and target versions, searching for `locale`, `language`, `translation`, `i18n`, `timezone`, `TZ`, `date`, and `format`. Check for changes to:

- data formats, database schemas, configuration keys, defaults, ports, and permissions;
- image entrypoints, health checks, dependencies, APIs, hooks, and Compose behavior;
- security fixes, deprecations, and required intermediate versions;
- languages, locales, timezones, and date formats.

Every runtime upstream or bundled-component change must increase the Module `version` or `revision`. An upstream version change updates `version` and resets `revision` to `1`; an ANAS-only packaging change increments `revision`. Keep `app_version` in sync when the upstream version cannot be normalized as SemVer.

## 2. Prefer idempotent upgrades

Prefer upstream initialization, migration, or reconciliation so a fresh install, repeated `apply`, and restart converge on the same state. Do not add an ANAS upgrade script when declarative configuration or the upstream entrypoint can perform the adaptation.

Add adaptation to an entrypoint, hook, or lifecycle operation only when upstream cannot perform a required conversion. It must:

- inspect actual state instead of relying only on a version marker;
- be safe to run repeatedly without further changes after success;
- be retryable after interruption and avoid an unrecognizable partial state on failure;
- constrain accepted input states and versions, with tests for each path.

Do not retain permanent branches for speculative historical versions. When a later release no longer supports sources from before an adaptation, raise the `upgrade.from` lower bound and remove the unreachable script in the same Module release. Prefer doing this with an upstream major-version upgrade to reduce extra compatibility breaks, but base the boundary on verified support.

Before removing a script, `upgrade.from` must exclude every source that still needs it, and tests must cover upgrade from the lowest supported version, repeated execution, and interruption recovery.

## 3. Declare compatibility and data boundaries

An incompatibility with older Module versions belongs in `module.yml`, not only in review notes:

```yaml
upgrade:
  from: ">=34.0.0"
  data_breaking: ["35.0.0"]
```

- `upgrade.from` defines source versions that may upgrade to the current release. When removing an old adaptation path, raise the lower bound to the first Module version whose state already has the required form.
- `upgrade.data_breaking` lists versions that rewrite the on-disk data format. List `35.0.0` when data written by 35.0.0 cannot be read by earlier releases; explicitly use `[]` when no break exists.

The fields are independent: `from` controls forward upgrade eligibility, while `data_breaking` controls pre-upgrade snapshots and artifact-only rollback. Commit a breaking point with the release that causes it, and only add to published history. Old breaking points may be removed only after raising the `upgrade.from` lower bound past them. See the [snapshot contract](/reference/contracts/snapshot#给-module-作者的规则).

## 4. Modify the Module

Review and update as needed:

- versions, dependency constraints, ABI, configuration, `upgrade.from`, and `data_breaking` in `module.yml`;
- Dockerfile, Compose, entrypoint, hooks, templates, health checks, and runtime assets;
- upstream configuration conversion, persistent paths, permissions, Secrets, networks, and Provider/Consumer contracts;
- the Module README, technical documentation, tests, and upgrade review.

Use the upstream-supported path for database and other persistent-state migrations. When custom adaptation is necessary, the review must state why upstream support is insufficient, why the script is idempotent, its supported source versions, and its removal condition.

## 5. Review timezone and language

Inspect translation manifests, locale directories, application APIs, `locale -a`, `/usr/share/zoneinfo`, and environment variables at the target tag, version branch, or exact image digest. Marketing pages can locate features but are not version evidence. Check whether:

- languages were added, removed, or renamed;
- browser, user, and deployment-default precedence changed;
- language or locale keys, encodings, or formats changed;
- the image still contains IANA zoneinfo and required POSIX locales;
- every long-running service receives `TZ`.

Update `modules/<name>/localization.yml`, including `module_version`, `module_revision`, `reviewed_at`, supported values, fallback, notes, and evidence. Update the version, revision, and review date even when the inventory is unchanged.

Handle differences as follows:

- use canonical BCP 47 for new languages and verify that the image ships them;
- warn and use the declared fallback for a removed configured language; do not block deployment;
- keep BCP 47 at the ANAS boundary and add tests when updating the upstream conversion layer for renamed values;
- test script variants such as `zh-Hans` and `zh-Hant` separately;
- test browser negotiation in user, browser, then deployment-fallback order without forcing a locale;
- keep IANA timezone names at the ANAS boundary and use a library with DST tests for upstream-specific formats.

## 6. Verify

At minimum, test:

1. a fresh installation;
2. upgrades from the lowest supported version and the previous version;
3. repeated `apply`, restart, and repeated execution of any adaptation;
4. retry after failure or interruption;
5. data, configuration, permissions, health checks, and dependent Modules;
6. rejection below `upgrade.from` and the expected snapshot and rollback guard at `data_breaking` boundaries.

Where container testing is available, upgrade and recover real persistent data; proving only that the image starts is insufficient. For localization, test UI, logs, and scheduled work with a non-English language, an unsupported language, a non-UTC timezone, and a DST timezone. Browser-aware applications must use `Accept-Language` to prove user preference outranks browser preference and browser preference outranks the deployment fallback.

Run:

```bash
go run ./cmd/gen-module-docs
go run ./cmd/gen-module-docs --check
go test ./...
npm run docs:build
```

`--check` validates Module inventories, version consistency, BCP 47 values, READMEs, and reference pages. Generated output must include both Chinese and English pages; inspect both diffs and navigation before completion.

The upgrade review should briefly record upstream changes, the upgrade path, compatibility lower bound, data-breaking points, reasons to retain or remove scripts, test results, and either the localization inventory change or “reviewed, no change.”
