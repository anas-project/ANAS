# Repository layout

```text
cmd/anas/             CLI entry point
internal/             runner implementation, configuration, state, tests
modules/              discoverable module bundles
test-env/             integration and remote validation environments
docs/                 VitePress documentation sources
.github/workflows/    CI, images, and documentation deployment
```

A module normally owns `module.yml`, `docker-compose.yml`, an optional Go hook, build contexts, templates, and runtime assets. Cross-module semantics belong in contracts or resources rather than private file access.
