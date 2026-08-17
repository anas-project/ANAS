# Research

Research documents capture dated external investigation, product comparison, and implementation assessment. They are evidence and decision input, not current user instructions.

The current detailed research index is available in [Chinese](/research/). Stable conclusions are promoted into guides, references, or architecture documents.

Current additions:

- [Manage users, groups, and administrators with `samba-tool`, 2026-08-17](./samba-tool-user-group-admin-guide-2026-08-17.md) — a bilingual ANAS/Samba AD operations guide with detailed command-option, LDAP-attribute, directory-term, group-scope, and administrator-role explanations.
- [ANAS web API and admin console implementation plan, 2026-08-16 (Chinese)](/research/web-api-admin-console-plan-2026-08-16) — a **proposal**, not current behavior: nothing described in it is executable today. It argues for a separate host daemon `anasd` that serves `/api/v1` and an embedded Vue console, with the CLI and HTTP layer sharing one typed Go application service rather than the daemon shelling out to `anas --json`. It also settles three questions the existing modules already constrain: the console consumes lego's issuer-neutral `ANAS_TLS_*` certificates instead of minting its own, it reaches HTTPS through an SSH tunnel during the bootstrap window before any module has started, and it holds one fixed admin port permanently rather than handing a port over to Traefik.
- [Reusable application research module specification (Chinese)](/research/application-research-module-spec)
- [Open-source self-hosted S3-compatible file and object services, 2026-08-15 (Chinese)](/research/self-hosted-open-source-s3-compatible-storage-research-2026-08-15)
- [Open-source self-hosted Git services landscape, 2026-08-15 (Chinese)](/research/self-hosted-open-source-git-services-research-2026-08-15)
- [Super Productivity and open-source self-hosted alternatives, 2026-08-15 (Chinese)](/research/super-productivity-alternatives-research-2026-08-15)
- [Open-source self-hosted notes landscape, 2026-08-13 (Chinese)](/research/self-hosted-open-source-notes-research-2026-08-13)
