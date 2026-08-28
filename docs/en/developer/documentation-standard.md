# Documentation standard

This standard applies to every new or modified file under `docs/` and `dev-docs/`. Identify the audience, document type, and source of truth before choosing a location or writing content.

## 1. Classify the document first

| Directory | Primary audience | Content |
| --- | --- | --- |
| `getting-started/` | First-time users | Installation, prerequisites, and the shortest working path |
| `guide/` | Users | Task-oriented configuration and routine operation |
| `operations/` | Administrators | Maintenance, troubleshooting, and service-specific operation |
| `operations/runbooks/` | On-call operators | Host procedures with checks, risk, validation, and recovery |
| `reference/` | Users and developers | Queryable facts such as settings, modules, and environment variables |
| `reference/contracts/` | Tool developers | Stable CLI, JSON, and file-format contracts |
| `reference/module-contracts/` | Tool developers | Module Contract mirrors generated from `contracts/<name>/` |
| `developer/` | Contributors | Repository, module, test, release, and documentation workflows |
| `architecture/` | Maintainers | Current models, decisions, and explicitly marked proposals |
| `research/` | Decision makers | External investigation, candidate comparison, and technical selection |
| `governance/` | Community | Project policy, registrations, and governance material |

Do not duplicate an entire topic across categories. A guide explains how, a reference lists what, and architecture explains why; connect them with links.

### Development artefacts live outside `docs/`

Requirements, implementation plans, and point-in-time reviews are development
artefacts rather than material for people deploying ANAS, so they are not part of
the documentation site. They live in `dev-docs/` at the repository root, beside
`contracts/` and `modules/`:

| Directory | Primary audience | Content |
| --- | --- | --- |
| `dev-docs/requirements/` | Product and architecture maintainers | Required outcomes, scope, hard constraints, and acceptance criteria |
| `dev-docs/plans/` | Implementation owners | Milestones, migration order, and remaining work for an agreed objective |
| `dev-docs/reviews/` | Maintainers and reviewers | Reviews and assessments tied to a date, version, or commit baseline |

Requirements and plans private to one Module live in `modules/<name>/dev-docs/`;
only ANAS-wide or cross-Module topics belong in the repository-level `dev-docs/`.
The test is whether the constraint the document states still means anything once
that Module is removed. A topic must not exist in both locations;
`npm run docs:check-requirements` gates that.

These three categories are **not mirrored in English**. They are development
artefacts read by contributors, and they are maintained in Chinese only.
Plans declare their state in frontmatter, with `status` limited to `proposed`,
`implementing`, `partial`, or `done`. The body may still carry a quote block with
detail for the reader, but frontmatter is the only machine-readable source, and
`npm run docs:check-plan-status` gates the index column against it. A plan whose
milestones are all done moves to `dev-docs/plans/archived/`; its requirement
document stays where it is, because an implemented requirement is a regression
baseline rather than history.


`dev-docs/` is read on GitHub and is never rendered by the site. Links inside it
use repository-relative paths; links from `docs/` into it use the full repository
URL (`https://github.com/anas-project/ANAS/blob/master/dev-docs/...`), because a
relative path there fails the site build as a dead link.

**When a site page cites a conclusion from `dev-docs/`, state the conclusion on the
page and keep the link only as the source.** A site page has to stand on its own: a
reader should never have to leave the site and open an unpublished development
artefact to understand a rule. State the conclusion, then close with one sentence
naming the normative source and saying it wins on conflict — do not let "see X"
stand in for the conclusion itself. Because the source wins, the summary should carry
the criteria and boundaries rather than copy whole paragraphs; copying creates a
second body of text to keep in sync.

## 2. Chinese and English

- Changes to user-facing `getting-started`, `guide`, `operations`, and core `reference` pages must update the matching English page under `/en/`.
- Mirrors use the same relative path and section structure.
- Requirements, plans, and reviews under `dev-docs/` are development artefacts kept in Chinese only; the bilingual rule does not apply to them.
- AI agents, generators, and other automation follow the same rule: create or update the Chinese page and its `docs/en/` mirror in one task. Never leave translation to a later task after generating a single-language page.
- A generator must treat both language files as one atomic output set. Its `--check` mode fails when either file is missing or stale, and every new generated page needs tests and sidebar entries for both languages.
- Chinese is currently the source language for detailed architecture. When a full translation is not practical, update the English architecture or research index with an accurate summary and link to the Chinese source.
- Do not translate commands, configuration keys, module names, paths, or error codes.
- Both languages must state the same support status, defaults, and risks.

## 3. Accuracy and status

Verify facts in this order:

1. runner code, configuration schema, and tests;
2. `modules/*/module.yml`, Compose files, and hooks;
3. stable contracts under `docs/reference/contracts/`;
4. maintained guides and design documents.

Do not copy commands or defaults from an old page without verification. Check CLI help, parsing, or the relevant tests. For versions, module status, counts, and other volatile facts, name the source or review date; link to an authoritative inventory instead of duplicating a list when possible.

The [Module documentation generation standard](/en/developer/module-documentation) and [Contract documentation generation standard](/en/developer/contract-documentation) define source files, required sections, generated markers, VitePress mirrors, bilingual output, and CI rules. Never edit a generated mirror directly; versioned Module timezone and language facts live in `modules/*/localization.yml`.

Requirements, architecture, plans, and reviews must declare their applicable status near the top. Architecture documents use at least one of these states:

- **Current model**: implemented and applicable;
- **Proposal**: not implemented and not an operating guide;
- **Historical record**: describes a named commit or dated baseline.

Mark proposed commands as unavailable. Explain the boundary when historical and current names appear together.

## 4. Files and structure

- Use lowercase kebab-case filenames such as `backup-and-restore.md`; use `index.md` for section landing pages.
- Use stable topic-based filenames under `research/`, such as `self-hosted-git-services-research.md`. Never put the creation date, update date, or evidence cutoff in a research filename; update the existing page in place.
- Only incident records and historical snapshots under `dev-docs/reviews/` use `YYYY-MM-DD-topic.md`. That prefix is the event or review baseline, not the page creation or last-edit date.
- Requirements, architecture, plans, guides, operations, development, and reference pages use stable filenames. Record plan status and target dates inside the page or its frontmatter.
- Use one level-one heading per page and do not skip heading levels.
- Start with the audience and outcome. Put prerequisites before state-changing steps.
- Keep one primary idea per paragraph and use descriptive level-two headings on longer pages.
- Tag code fences: `bash`, `yaml`, `json`, or `text`.
- Use placeholders such as `nas.example.com`, `admin@example.com`, `/srv/anas`, and `replace-me`, never real infrastructure or credentials.
- Use the project spellings `Module`, `Contract`, `Resource`, `Runner`, `workspace`, and `deployment`.

Every research page starts with this frontmatter:

```yaml
---
doc_type: research
created: 2026-08-15
updated: 2026-08-20
evidence_as_of: 2026-08-19
---
```

- `created` is the first creation date and never changes during later updates.
- `updated` is the last substantive change to conclusions, scope, or important facts; formatting-only and link-only edits need not change it.
- `evidence_as_of` is the last verification date for volatile external facts. Omit it when the research has no dynamic evidence, but do not use `updated` as a substitute.
- Dates use ISO `YYYY-MM-DD`. A title may mention an evidence cutoff when the prose requires it, but the date is not the page identity.

## 5. Links and navigation

- Prefer VitePress root paths for site links, for example `[Backup](/en/guide/backup-and-restore)`; English pages link to `/en/...`.
- Never link generated output, local absolute paths, or deleted legacy directories.
- Add discoverable pages to `docs/.vitepress/config/sidebar.ts`. Change the top navigation only for a new primary section.
- Fix all site, README, and navigation links in the same change when moving a page.
- Cite official documentation, specifications, or upstream repositories for external facts, using descriptive link text rather than bare URLs.

## 6. Commands, configuration, and safety

- Copyable commands must match the current CLI, include required `-w` or `-c` arguments, and state whether they mutate state.
- Dangerous procedures need preflight checks, scope, backup requirements, validation, and recovery before they qualify as runbooks.
- YAML examples must follow the current structured schema; module selection is a mapping, not the legacy list form.
- Everything under `docs/` is public. Do not store real secrets, host details, SSH instructions, incident records, or test-server logs there.
- Keep one-off evidence in controlled issues, CI artifacts, or private systems, then promote durable sanitized conclusions into maintained documentation.

## 7. Page template

```markdown
# Page title

State the audience and outcome in one sentence.

> [!NOTE]
> Declare a proposal or historical baseline here when applicable.

## Prerequisites

List conditions that must be true before starting.

## Procedure or explanation

Provide the shortest verifiable core content.

## Verification

Explain how to confirm success.

## Failure and recovery

Include this only for tasks that can fail or change state.

## Related documentation

Link to authoritative guides, references, or architecture.
```

Reference pages can instead use scope, field or command tables, errors and boundaries, and maintenance sources. Do not retain empty sections mechanically.

## 8. Pre-commit checklist

- [ ] The category and audience are correct, with no unnecessary duplicate page.
- [ ] Research uses a stable topic filename and maintains `created`, `updated`, and `evidence_as_of` where needed.
- [ ] User documentation has matching Chinese and English updates, or the translation boundary is documented.
- [ ] Generated pages were updated in both languages in the same run and are reachable from both sidebars.
- [ ] Commands, configuration, versions, and module status were checked against the implementation.
- [ ] Current behavior, proposals, and historical records are clearly separated.
- [ ] Examples contain no real secrets, host information, or personal data.
- [ ] Navigation and links are updated for new or moved pages.
- [ ] `npm run docs:build` passes, along with relevant code tests.

See [Documentation site](documentation.md) for preview, build, and publishing instructions.
