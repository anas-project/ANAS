# Research

This section contains external investigation, candidate comparison, and technical selection only. Requirements, architecture, implementation plans, dated reviews, operating procedures, and development standards live in their own sections.

- [Casdoor application catalog and filtering](/research/casdoor-app-catalog) — Chinese original. Twelve isolated tests execute the pinned source's permission/list functions: user/group/role filtering, OR groups, policy defaults, admin bypass and membership removal. This is source evidence, not HTTP/browser or deployment acceptance. The proposed full catalog needs API/UI work beyond native Application fields.

- [Docker and Podman compatibility](/research/docker-podman-compatibility) — Chinese original. Proposes retaining Docker CLI and Compose against a local Linux rootful Podman API. Engine identity, macvlan, Incus control networking, SELinux and reboot behavior require validation. No host compatibility or E2E acceptance is claimed; rootless full-stack operation and in-place engine migration are outside the initial scope.

- [Casdoor directory subject feasibility](/research/casdoor-directory-subject) — Chinese original. Pinned-source tests show configuration cannot unify token, UserInfo, Logout Token and SAML subjects. Revision r10 integrates the common directory-anchor subject and rejects missing anchors. Consumer projection and deployment E2E remain required; no account migration is needed before initial release.

Research pages use stable topic-based filenames. Their frontmatter records `created`, `updated`, and, when external facts are volatile, `evidence_as_of`; updating a report does not rename it.

- [Immich integration feasibility](/research/immich-module-integration) — Chinese original. Consolidates the prior upstream evidence (as of 2026-10-01, not reverified in this documentation update) and the local reuse assessment. The confirmed design uses dedicated Redis/Valkey, OIDC-only sign-in, shared PostgreSQL and ANAS backup; resource-based operating modes are a separate next-release proposal for all Modules. No deployment acceptance is claimed.

- [Incus network visualization](/research/incus-network-visualization-research) — Chinese original. No existing open-source project gives Incus an AWS VPC-style topology view (the Incus UI and LXConsole are form-based, Skydive is unmaintained, Horizon is tied to Neutron); the recommendation is a read-only per-lease network view in the ANAS console.
- [Mastodon and ActivityPub self-hosted services](/research/mastodon-related-self-hosted-services-research)
- [LLNG Passkey/WebAuthn and Samba sharing boundary](/research/llng-passkey-webauthn-samba-sharing)
- [IAM logout and application-session synchronization](/research/iam-logout-application-session-sync)
- [Self-hosted IAM and ANAS integration](/research/self-hosted-open-source-iam-research)
- [Super Productivity and zero-configuration Nextcloud synchronization](/research/super-productivity-nextcloud-sso-sync-research)
- [Nextcloud search solutions](/research/nextcloud-search-solution-research)
- [BIND 9 web-management tools](/research/bind9-open-source-web-management-research)
- [Self-hosted mail services](/research/self-hosted-open-source-mail-services-research)
- [Self-hosted email forwarding](/research/self-hosted-open-source-email-forwarding-research)
- [Kanban integration with coding agents](/research/kanban-ai-agent-integration-research) — full text now under `modules/ai_agent/`
- [Self-hosted S3-compatible storage](/research/self-hosted-open-source-s3-compatible-storage-research)
- [Self-hosted Git services](/research/self-hosted-open-source-git-services-research)
- [Super Productivity alternatives](/research/super-productivity-alternatives-research)
- [Self-hosted notes applications](/research/self-hosted-open-source-notes-research)
- [Self-hosted Kanban applications](/research/self-hosted-open-source-kanban-research)
