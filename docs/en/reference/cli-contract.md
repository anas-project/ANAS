# CLI contract

The normative CLI JSON contract currently lives in the Chinese reference section:

- [Common rules and index](/reference/contracts/)
- [Deployment and configuration commands](/reference/contracts/commands)
- [Snapshot commands](/reference/contracts/snapshot)
- [Backup commands](/reference/contracts/backup)

Every JSON response includes `api_version`. Structured results use stdout; progress, warnings, and logs use stderr. Exit codes distinguish usage errors, confirmation requirements, precondition failures, and failures after work has started. Consumers must rely on documented enum values rather than parsing human-readable messages.

## Local administrator commands

```text
anas admin local list [-w WORKSPACE] [--json]
anas admin local credential MODULE [ACCOUNT] [-w WORKSPACE] [--password-only | --json]
anas admin local rotate MODULE [ACCOUNT] [-w WORKSPACE] [--prompt] [--json]
```

`ACCOUNT` is the Manifest `management.local_accounts[].id`, never the physical
username. When omitted, selection prefers the `primary` ID, then the module's
only account; any other multi-account case is ambiguous. `list` is safe
inventory. `credential` is the explicit sensitive read. `rotate` generates a
random password by default; `--prompt` reads it without echo from a real TTY
and confirms it. Managed passwords are never accepted in argv, environment
variables, or YAML.

Rotation sends a candidate Secret to the active artifact's declared module
handler, updates and verifies application state, and commits the Secret only
after success. A failed verification restores the old application password and
leaves the old Secret authoritative. Modules without a real
`lifecycle.rotate` handler report the capability as unsupported.
