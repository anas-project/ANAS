# Repository layout

```text
cmd/anas/             CLI entry point
cmd/anasd/            Web API daemon entry point (M0 development skeleton)
internal/             internal Go implementation, platform adapters, and tests
internal/application/ typed use cases shared by CLI and HTTP
internal/deployment/  read-only deployment state model and store
internal/api/httpapi/ HTTP routes, DTOs, and error mapping
internal/runner/      CLI adapter, migration implementation, and tests
api/openapi.yaml      OpenAPI 3.1 contract for the HTTP API
modules/              discoverable module bundles
test-env/             integration and remote validation environments
docs/                 VitePress documentation sources
.github/workflows/    CI, images, and documentation deployment
```

A module normally owns `module.yml`, `docker-compose.yml`, an optional Go hook, build contexts, templates, and runtime assets. Cross-module semantics belong in contracts or resources rather than private file access. The CLI and HTTP adapters share application use cases while retaining separate `anas.dev/cli/v1` and `anas.dev/api/v1` external contracts.
