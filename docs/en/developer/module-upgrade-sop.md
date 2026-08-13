# Module upstream upgrade SOP

Use this SOP whenever changing an upstream version in `module.yml`, so timezone, language, and regional formatting do not drift silently.

## 1. Pin the upgrade scope

Record the application version, image tag/digest, module version/revision, and every bundled Web UI version. Bundled components such as Adminer or the NetBird Dashboard require their own review. Every runtime upstream or bundled-component change must increase the module version or revision so the documentation gate runs.

## 2. Inspect upstream changes

Review release notes, migration notes, configuration documentation, and source for `locale`, `language`, `translation`, `i18n`, `timezone`, `TZ`, date, and format changes. Check added, removed, or renamed languages; browser/user/default precedence; configuration formats; packaged zoneinfo/POSIX locales; and whether every long-running service receives the setting.

## 3. Collect versioned evidence

Prefer translation manifests, locale directories, and resource keys from the pinned tag. For binary-only releases, inspect the exact image digest, including `locale -a`, `/usr/share/zoneinfo`, resources, environment, and application APIs. Marketing pages can help locate features but are not sufficient version evidence.

Update `modules/<name>/localization.yml`, including `module_version`, `module_revision`, `reviewed_at`, supported values, fallback, notes, and evidence. Update the version/revision and review date even when the inventory is unchanged.

## 4. Handle differences

- Verify new languages are present in the shipped image before listing them.
- Warn for a configured language removed upstream and continue with the declared fallback; do not block deployment.
- Keep BCP 47 at the ANAS boundary and update only the upstream conversion mapping when names change.
- Test script variants such as `zh-Hans` and `zh-Hant` separately.
- For browser negotiation changes, test user preference, browser preference, then deployment fallback; do not force a locale.
- Keep IANA timezone names at the ANAS boundary and use a library plus DST tests for any upstream-specific conversion.

## 5. Generate and verify

```bash
go run ./cmd/gen-module-docs
go run ./cmd/gen-module-docs --check
go test ./...
npm run docs:build
```

Where container testing is available, verify a non-English language, an unsupported language, a non-UTC timezone, and a DST timezone. Browser-aware applications must also prove user preference outranks browser preference and browser preference outranks the deployment fallback. State either the inventory change or “reviewed, no change” in the upgrade review.

Generated output must include both Chinese and English reference pages. Before completing an upgrade, a maintainer or AI agent checks both diffs and both sidebar entries; either language missing means the upgrade is incomplete.
