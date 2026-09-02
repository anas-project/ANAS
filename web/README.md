# ANAS Console Web

This is the independent Vue/TypeScript/Vite project for the embedded `anasd` console. It deliberately has its own `package.json`, lockfile, and dependency tree; it does not import the VitePress documentation build.

`npm run build` regenerates OpenAPI types, type-checks both packages, runs unit tests, and writes deterministic main/emergency bundles to `internal/webui/dist/`. The emergency package uses only browser APIs so a broken Vue bundle cannot prevent the recovery page from rendering.
