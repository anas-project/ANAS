# Testing

Run the Go suite for runner and module-hook changes:

```bash
go test ./...
```

Use the relevant scripts under `test-env/` for integration behavior. Tests that need Docker, real DNS, networking, or remote hosts require an explicit isolated environment.

The target workflow is documented in the normative Chinese
[document-driven test automation requirements](/requirements/document-driven-test-automation)
and [implementation plan](/plans/document-driven-test-automation). An agent may
generate complete tests from requirements and machine-readable cases; generated
tests still need the same traceability, negative/fault-path validation, real
execution, and review as human-written tests. The planned SSH runner will use a
registered dedicated non-production target to transfer an identified source
bundle, deploy into a per-run isolated Docker environment, execute a selected
suite, collect sanitized reports, and clean up only that run's resources. This
single-command remote runner is not implemented yet.

Dated reports are evidence, not permanent operating instructions. Promote durable conclusions into maintained documentation, and keep raw logs or host-specific records in controlled CI artifacts, issue attachments, or an external private system rather than under `docs/`.

Build documentation before committing a content or navigation change:

```bash
npm ci
npm run docs:build
```

The production build validates Markdown compilation and internal links.
