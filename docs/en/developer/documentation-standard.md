# Documentation standard

This standard applies to every new or modified file under `docs/`. Identify the audience, document type, and source of truth before choosing a location or writing content.

## 1. Classify the document first

| Directory | Primary audience | Content |
| --- | --- | --- |
| `getting-started/` | First-time users | Installation, prerequisites, and the shortest working path |
| `guide/` | Users | Task-oriented configuration and routine operation |
| `operations/` | Administrators | Maintenance, troubleshooting, and service-specific operation |
| `operations/runbooks/` | On-call operators | Host procedures with checks, risk, validation, and recovery |
| `reference/` | Users and developers | Queryable facts such as settings, modules, and environment variables |
| `reference/contracts/` | Tool developers | Stable CLI, JSON, and file-format contracts |
| `developer/` | Contributors | Repository, module, test, release, and documentation workflows |
| `architecture/` | Maintainers | Current models, decisions, and explicitly marked proposals |
| `research/` | Decision makers | Dated research, assessments, and historical reviews |
| `governance/` | Community | Project policy, registrations, and governance material |

Do not duplicate an entire topic across categories. A guide explains how, a reference lists what, and architecture explains why; connect them with links.

## 2. Chinese and English

- Changes to user-facing `getting-started`, `guide`, `operations`, and core `reference` pages must update the matching English page under `/en/`.
- Mirrors use the same relative path and section structure.
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

Architecture documents must declare one of these states near the top:

- **Current model**: implemented and applicable;
- **Proposal**: not implemented and not an operating guide;
- **Historical record**: describes a named commit or dated baseline.

Mark proposed commands as unavailable. Explain the boundary when historical and current names appear together.

## 4. Files and structure

- Use lowercase kebab-case filenames such as `backup-and-restore.md`; use `index.md` for section landing pages.
- Use one level-one heading per page and do not skip heading levels.
- Start with the audience and outcome. Put prerequisites before state-changing steps.
- Keep one primary idea per paragraph and use descriptive level-two headings on longer pages.
- Tag code fences: `bash`, `yaml`, `json`, or `text`.
- Use placeholders such as `nas.example.com`, `admin@example.com`, `/srv/anas`, and `replace-me`, never real infrastructure or credentials.
- Use the project spellings `Module`, `Contract`, `Resource`, `Runner`, `workspace`, and `deployment`.

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
- [ ] User documentation has matching Chinese and English updates, or the translation boundary is documented.
- [ ] Commands, configuration, versions, and module status were checked against the implementation.
- [ ] Current behavior, proposals, and historical records are clearly separated.
- [ ] Examples contain no real secrets, host information, or personal data.
- [ ] Navigation and links are updated for new or moved pages.
- [ ] `npm run docs:build` passes, along with relevant code tests.

See [Documentation site](documentation.md) for preview, build, and publishing instructions.
