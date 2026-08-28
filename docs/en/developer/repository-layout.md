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
docs/                 VitePress documentation sources (user-facing, published)
dev-docs/             requirements, implementation plans, and reviews (development artefacts, not published)
.github/workflows/    CI, images, and documentation deployment
```

The line between `dev-docs/` and `docs/` is the audience: `docs/` is for people
deploying and using ANAS, `dev-docs/` is for contributors.

```text
dev-docs/
  requirements/       outcomes, scope, hard constraints, and acceptance criteria
  plans/              milestones, migration order, and remaining work
  reviews/            review snapshots tied to a date or commit baseline
```

Requirements and plans private to one Module live in `modules/<name>/dev-docs/`;
the test is whether the constraint still means anything once that Module is
removed. A topic must not exist in both locations. See the
[documentation standard](/en/developer/documentation-standard) §1.

A module normally owns `module.yml`, `docker-compose.yml`, an optional Go hook, build contexts, templates, and runtime assets. Cross-module semantics belong in contracts or resources rather than private file access. The CLI and HTTP adapters share application use cases while retaining separate `anas.dev/cli/v1` and `anas.dev/api/v1` external contracts.
