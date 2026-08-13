# Testing

Run the Go suite for runner and module-hook changes:

```bash
go test ./...
```

Use the relevant scripts under `test-env/` for integration behavior. Tests that need Docker, real DNS, networking, or remote hosts require an explicit isolated environment.

Dated reports are evidence, not permanent operating instructions. Promote durable conclusions into maintained documentation, and keep raw logs or host-specific records in controlled CI artifacts, issue attachments, or an external private system rather than under `docs/`.

Build documentation before committing a content or navigation change:

```bash
npm ci
npm run docs:build
```

The production build validates Markdown compilation and internal links.
