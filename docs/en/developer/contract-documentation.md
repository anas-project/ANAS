# Contract documentation generation standard

This standard mirrors the [Module documentation standard](/en/developer/module-documentation) and defines how Contract root documentation, machine facts, reviewed semantics, and VitePress pages are maintained together.

## Required files and sources of truth

Every `contracts/<name>/` directory must contain `contract.yml`, `schemas/*.yml`, `documentation.yml`, `README.md`, and `README.en.md`. Root READMEs remain understandable outside the site. `docs/reference/contracts/<name>.md` and `docs/en/reference/contracts/<name>.md` are generated VitePress mirrors, not additional sources of truth.

```bash
go run ./cmd/gen-contract-docs
go run ./cmd/gen-contract-docs --check
```

The check form does not write files. The generator treats both root READMEs and both VitePress pages as one inseparable output set.

## Status inventory

`documentation.yml` uses `anas.contract-documentation/v1` and one status:

- `implemented`: the Runner dispatches it, a provider implements it, and real E2E verifies it;
- `partial`: only some operations, interfaces, or provider/consumer paths work;
- `pending`: the definition exists but the Runner does not dispatch it;
- `proposal`: an ADR or necessity review is still required;
- `deprecated`: compatibility remains, but no new consumer may use it.

The mere presence of `contract.yml`, schemas, or a Module name never proves `implemented`.

## Generated and reviewed content

Content between `generated:contract-reference` markers is generated from code: version, status, interfaces, resource identity, operation schemas, schema fields, and current Module provider/consumer declarations.

Content outside the block is reviewed prose: purpose, identity and idempotency semantics, lifecycle ordering, transactions, verification, compensation, Secret and trust boundaries, compatibility, deletion, examples, recovery, and known limitations.

Code and existing prose can seed this content, including through AI analysis, but static analysis cannot prove that application state changed, login works, rollback is reliable, or a security intent is correct. Review ties those claims to tests and explicit design decisions instead of manually copying fields.

## Bilingual output and CI

Chinese and English READMEs must have parallel structures. Technical IDs, paths, operations, commands, and statuses remain unchanged.

```bash
go run ./cmd/gen-contract-docs --check
test-env/scripts/test-contract.sh
npm run docs:build
```

Promoting a Contract to `implemented` requires provider and consumer manifests, Runner dispatch, operation tests, and evidence from a real-service E2E in the same change set.
