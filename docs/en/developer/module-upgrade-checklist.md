# Module upgrade checklist

> Status: **current baseline**. Updated: 2026-08-21.

This is the sign-off form for the [Module upstream upgrade SOP](/en/developer/module-upgrade-sop).
Use it for an upstream application, base image, bundled UI/helper, or other runtime-asset change. A
documentation-only correction may record `N/A`. The SOP remains normative.

## Review metadata

- [ ] Record Module, source and target version/revision, change type, every runtime image tag/digest, target platforms, reviewer, and evidence date.
- [ ] Identify the minimum-supported source, previous release, target, and permitted rollback target.

## 1. Upstream change review

- [ ] Review every intervening release note, migration guide, deprecation, and security advisory.
- [ ] Check data formats, database schema, configuration keys/defaults, ports, permissions, API, and authentication protocols.
- [ ] Check image user/entrypoint/healthcheck/VOLUME, base distribution, bundled components, and platform support.
- [ ] Check locale, language, timezone, date formatting, and translation-list changes.
- [ ] Record required intermediate versions and block an upgrade when the supported path is unknown.

## 2. Manifest, packaging, and contracts

- [ ] An upstream version change updates `version` and resets `revision` to `1`; packaging-only revisions are calculated by the release workflow.
- [ ] Manifest, Compose image tags, localization manifest, generated docs, and bundled-component versions agree.
- [ ] Recheck ABI, Module/Capability/Contract dependencies, Contract ranges, Resources, and provider interfaces.
- [ ] `upgrade.from` accepts only verified sources; `upgrade.data_breaking` records every on-disk break or explicitly remains `[]`.
- [ ] Synchronize `.github/modules.json`, build contexts, platforms, and global Module/Contract inventories.

## 3. Configuration, hooks, and runtime assets

- [ ] Update and test every added/removed/renamed key, type, default, constraint, sensitivity, and change effect.
- [ ] Make old-to-new conversion explicit; reject unsupported old values before startup instead of having Core silently repair them.
- [ ] Recheck the hook phase allowlist, ABI, derived values, secret ownership, and upstream environment mapping.
- [ ] Compare Dockerfile, Compose, entrypoint, templates, healthcheck, networks, volumes, and runtime user with the target image.
- [ ] Any custom conversion script has actual-state preflight, idempotence, interrupted retry, version scope, and a removal condition.

## 4. Data migration, snapshots, and rollback

- [ ] Identify the owner of database, file-format, index, cache, queue, and external-Resource migrations.
- [ ] The pre-upgrade snapshot/backup covers databases, user files, Secret Store, Resource state, and deployment metadata.
- [ ] Upgrade from the minimum-supported and previous versions using real persistent data.
- [ ] Verify repeated apply/restart/migration and interruption before, during, and after migration.
- [ ] Verify artifact rollback when no break exists; when data breaks, verify snapshot restore and do not claim impossible in-place rollback.

## 5. Password/key and rotation compatibility

- [ ] Compare the pre/post-upgrade inventory of every ANAS-managed password, shared/client secret, signing/encryption key, and Resource credential.
- [ ] Key-name, default, or provenance changes do not silently regenerate a stable secret.
- [ ] A required format/algorithm conversion uses candidate → application update → verify → Store commit, restoring the old value and application state on failure.
- [ ] Revalidate single-target, `anas credential rotate --module MODULE`, and deployment `--all` rotation after upgrade; mark unenrolled Resource/local-admin and other scopes manual/unsupported.
- [ ] An IAM client secret, database credential, or shared-secret change reconciles every provider and consumer in one transaction.

## 6. IAM, API, and management surfaces (conditional)

- [ ] OIDC/SAML/LDAP/Kerberos endpoints, redirects/SLS/logout URIs, scopes, claims, and group mappings match the target version.
- [ ] Local accounts, break-glass routes, and apply/rotate/rollback handlers remain usable.
- [ ] Reverify Module-initiated, IAM-initiated, and browserless administrative logout claims; remove stale endpoints.
- [ ] Recheck public API/webhook/CalDAV contracts, token permissions, rotation, and revocation behavior.

## 7. Localization and documentation

- [ ] Update localization version/revision/review date, language list, mappings, fallback, and pinned evidence.
- [ ] Verify one non-English and one unsupported language, a non-UTC and DST timezone, and user-preference precedence.
- [ ] Keep both README and technical-document languages aligned on versions, parameters, migration, recovery, rotation, limits, and evidence.
- [ ] Synchronize Module catalogs, Contract consumers, localization matrices, configuration counts, global references, and the upgrade review record.

## 8. Static and real-environment verification

- [ ] Run and record:

```bash
go run ./cmd/gen-module-docs
go run ./cmd/gen-module-docs --check
go run ./cmd/gen-contract-docs --check
go test ./...
npm run docs:build
git diff --check
```

- [ ] Compose parsing, Module/helper unit tests, and image builds pass on every declared platform.
- [ ] Fresh install, minimum and previous upgrades, repeated apply, restart, interrupted retry, and rollback/restore use real data.
- [ ] Health, main business flows, dependencies, IAM/credential rotation, and backup/restore pass in an isolated Docker environment on an authorized target.

## 9. Sign-off

| Area | Result | Evidence or blocker |
| --- | --- | --- |
| Upstream-difference review | Pass / Fail |  |
| Manifest/contracts/packaging | Pass / Fail |  |
| Configuration and data migration | Pass / Fail |  |
| Secret and credential rotation | Pass / Fail |  |
| Upgrade, interrupted retry, and rollback | Pass / Fail |  |
| Localization and documentation | Pass / Fail |  |
| Real-environment release gates | Pass / Fail |  |

Release the upgrade only when every applicable item passes. An unverified item blocks release or keeps
the Module `developing`; “upstream should support it” is not evidence.
