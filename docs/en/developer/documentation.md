# Documentation site

The site uses VitePress. Chinese is served at the site root; the English mirror is under `/en/`.

```bash
npm ci
npm run docs:dev       # local development
npm run docs:build     # production build
```

The deployable static directory is `docs/.vitepress/dist/`. GitHub Actions builds every pull request and every push to `master`. A successful `master` build deploys to GitHub Pages; pull requests only validate the build. The workflow can also be started manually.

Everything under `docs/`, apart from build configuration and generated output, is public documentation. Navigation is not a publication boundary: do not store real secrets, test-host addresses, SSH commands, or internal incident records here. Keep one-off evidence in controlled issues, CI artifacts, or an external private system, and rewrite durable conclusions as sanitized guides, references, or design documents.
