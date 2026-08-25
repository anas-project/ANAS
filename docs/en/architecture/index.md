# Architecture

Chinese is the source language for the detailed design set. The pages below linked under `/en/` have English versions; the rest link to the Chinese originals, which remain normative. It covers:

- the normative [Core implementation standard](/en/architecture/core-implementation-standard);
- [modules, contracts, resources, and provider operations](/en/architecture/module-contract-resource-design);
- [administrator account lifecycle](/architecture/admin-account-system);
- [IAM capability, protocol selection, and bidirectional logout registration](/en/architecture/iam-capability-design);
- [application catalog visibility and authorization](/architecture/app-catalog-design);
- [dynamic DNS capability selection](/architecture/dynamic-dns-capability-design);
- [object-storage capability binding and normalized S3 outputs](/en/architecture/object-storage-capability-design);
- [Forgejo Module identity, Actions authorization, and Incus VM runner design](/architecture/forgejo-module-design);
- [AI agent orchestration (Forgejo baseline, proposal)](/architecture/ai-agent-orchestration-design) — agents as Forgejo accounts with repository-scoped tokens, issue/label/comment events as the control surface, a standalone orchestrator packaged as a module, and one-job isolated execution;
- [runtime artifacts, releases, and persistent state](/architecture/runtime-release-state-design);
- [configuration and state lifecycle](/architecture/config-state-lifecycle).

The Chinese source documents remain normative while further English translations are prepared. Stable machine-facing behavior is separately defined by the [CLI contracts](/en/reference/contracts/).
