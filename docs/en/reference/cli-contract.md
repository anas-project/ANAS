# CLI contract

The normative CLI JSON contract currently lives in the Chinese reference section:

- [Common rules and index](/reference/contracts/)
- [Deployment and configuration commands](/reference/contracts/commands)
- [Snapshot commands](/reference/contracts/snapshot)
- [Backup commands](/reference/contracts/backup)

Every JSON response includes `api_version`. Structured results use stdout; progress, warnings, and logs use stderr. Exit codes distinguish usage errors, confirmation requirements, precondition failures, and failures after work has started. Consumers must rely on documented enum values rather than parsing human-readable messages.
