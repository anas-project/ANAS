# Architecture

Chinese is the source language for the detailed design set. The pages below linked under `/en/` have English versions; the rest link to the Chinese originals, which remain normative. It covers:

- the normative [Core implementation standard](/en/architecture/core-implementation-standard);
- [modules, contracts, resources, and provider operations](/en/architecture/module-contract-resource-design);
- [module-specific commands, typed parameters, and shared CLI/API execution](/architecture/module-command-capability-design);
- [administrator account lifecycle](/architecture/admin-account-system);
- [IAM capability, protocol selection, and bidirectional logout registration](/en/architecture/iam-capability-design);
- [application catalog visibility and authorization](/architecture/app-catalog-design);
- [dynamic DNS capability selection](/architecture/dynamic-dns-capability-design);
- [object-storage capability binding and normalized S3 outputs](/en/architecture/object-storage-capability-design);
- [Forgejo Module identity, Actions authorization, and Incus VM runner design](/architecture/forgejo-module-design);
- [AI agent orchestration (Forgejo baseline)](/architecture/ai-agent-orchestration-design) — agents as Forgejo accounts with repository-scoped tokens, issue/label/comment events as the control surface, a standalone orchestrator packaged as a module, and one-job isolated execution. The design itself now lives with the component under `modules/ai_agent/`, ready to be split into its own project;
- [Incus host provisioning, ingress, and guest image baking (proposal)](/architecture/incus-host-provisioning) — separates implemented leases from planned host setup and ingress; fixed-destination non-root transport and a pre-apply image catalog are concrete candidates. Proxy permissions and VM NAT topology remain blocked on validation; normal instance/profile updates enforce the proxy ban even for administrators. The bridge compatibility fix now uses provider-owned bridges in the default project with exact per-lease network access; real-host validation is still pending. Lease secrets use a dedicated rotation command, outside credential rotation. The Chinese source is normative;
- [runtime artifacts, releases, and persistent state](/architecture/runtime-release-state-design);
- [configuration and state lifecycle](/architecture/config-state-lifecycle).

The Chinese source documents remain normative while further English translations are prepared. Stable machine-facing behavior is separately defined by the [CLI contracts](/en/reference/contracts/).
