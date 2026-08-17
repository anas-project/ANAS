# Documentation site

The site uses VitePress. Chinese is served at the site root; the English mirror is under `/en/`.

Read the [documentation standard](documentation-standard.md) before adding or changing content. It defines classification, bilingual maintenance, sources of truth, design status, links, safety, and review checks.

```bash
npm ci
npm run docs:dev       # local development
npm run docs:build     # production build
```

The deployable static directory is `docs/.vitepress/dist/`. GitHub Actions builds every pull request, every push to `master`, and each completed Core or Module release. Successful `master` and post-release builds deploy to GitHub Pages; pull requests only validate the build. The workflow can also be started manually.

## Documentation version policy

The site root always publishes the continuously maintained latest documentation. The home page and top navigation on every page show the latest stable Core version. The version comes from a Git tag that exactly matches `vMAJOR.MINOR.PATCH`; prerelease and Module tags do not change it.

The site takes a snapshot only when the Core major version changes instead of copying the whole site for every minor or patch release. The build selects the final stable tag from each historical major. For example, after `v1.0.0` is released, the final `v0.x` documentation remains at `/versions/0.x/`. Archived pages carry a persistent version notice and a link to the latest documentation. Stable tags are immutable, so historical snapshots are not edited in place; corrections to the current documentation continue at the site root. Current documentation still receives strict dead-link checking; existing dead links in a historical tag remain as released but do not block publication of the new site.

The build also writes the selection to `versions.json` at the output root for redirects, site checks, and other publishing tools. Validate the tag-selection rules and build the complete versioned site with:

```bash
npm run docs:test-versions
npm run docs:build
```

The default public origin is `https://anas-project.github.io`. Set `DOCS_SITE_ORIGIN` when publishing the versioned output on another domain; `DOCS_BASE` continues to control the site root beneath that origin.

Everything under `docs/`, apart from build configuration and generated output, is public documentation. Navigation is not a publication boundary: do not store real secrets, test-host addresses, SSH commands, or internal incident records here. Keep one-off evidence in controlled issues, CI artifacts, or an external private system, and rewrite durable conclusions as sanitized guides, references, or design documents.
