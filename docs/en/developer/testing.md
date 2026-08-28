# Testing

Run the Go suite for runner and module-hook changes:

```bash
go test ./...
```

Use the relevant scripts under `test-env/` for integration behavior. Tests that need Docker, real DNS, networking, or remote hosts require an explicit isolated environment.

Machine-readable catalogs are now available under `test-env/cases/<topic>/cases.yml`;
their generated README files and bidirectional requirement/implementation links
are checked by `npm run docs:check-requirements`. Generate or check them with
`npm run test-cases:generate` and `npm run test-cases:check`.

Three hard rules govern this:

- **Traceability runs both ways.** A case lists the requirement IDs it covers and the
  implementation declares its case IDs in a `TEST_CASES:` comment, so the gate can walk from a
  requirement to the case, the code, the command, and the latest evidence — and back.
- **Changed requirement wording or verification method puts the case under review**, even when the
  requirement ID is unchanged. An agent proposes a patch from the diff; overwriting a reviewed
  assertion with no diff is not allowed.
- **A passing happy path does not prove a security, rollback, rejection, or degradation
  requirement.** Those need a negative case or fault injection as well.

The normative source is the Chinese
[document-driven test automation requirements](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/document-driven-test-automation.md),
with delivery order in the [implementation plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/document-driven-test-automation.md);
both live in the repository under `dev-docs/`. An agent may
generate complete tests from requirements and machine-readable cases; generated
tests still need the same traceability, negative/fault-path validation, real
execution, and review as human-written tests. The planned SSH runner will use
either a registered dedicated target or the exact server explicitly named by
the user for that run to transfer an identified source
bundle, deploy into a per-run isolated Docker environment, execute a selected
suite, collect sanitized reports, and clean up only that run's resources. This
single-command remote runner is not implemented yet.

A dedicated non-production target remains the default. Explicitly naming a
server authorizes that target even when it carries production services, but it
never bypasses the isolated Docker daemon, workspace, network, port-range, and
scoped-cleanup boundaries or authorizes mutation of existing resources.

Dated reports are evidence, not permanent operating instructions. Promote durable conclusions into maintained documentation, and keep raw logs or host-specific records in controlled CI artifacts, issue attachments, or an external private system rather than under `docs/`.

## Automated gates

| Workflow | Trigger | Coverage |
| --- | --- | --- |
| `ci.yml` | every pull request, and pushes to `master` | `go vet ./...`, `go test ./...`, documentation-source consistency, the four `scripts/ci/*-test.sh` shell tests, and `govulncheck` (reporting only) |
| `docs.yml` | every pull request, and the post-release deploy trigger | VitePress build (only a real build can check dead links) and the GitHub Pages deployment |
| `anas-release.yml` | pushes to `anas-release` | version decision, build, release |
| `container-images.yml` | pushes to `image-release` | Module and container artifacts |

No step in `ci.yml` may depend on a Docker daemon, a real host, or the network. The integration
scripts under `test-env/` belong to a machine that has those; putting them in a required gate keeps
it permanently red, and a permanently red gate gets ignored.

The `govulncheck` version is pinned in the workflow rather than tracking `@latest`: an unpinned
scanner changes what it reports with no commit in this repository, so a red run could not be traced
to a change. It runs with `continue-on-error` until the false-positive rate is known.

Container images are scanned in `container-images.yml`, because that is the only workflow that has
images. The `build` and `mirror` jobs each run `scripts/ci/scan-image.sh` *after* publishing, against
the reference that actually shipped. After rather than before, because a scanner failure -- a
vulnerability database that did not download, say -- must not be able to abort a release. It reports
only: `ANAS_SCAN_ENFORCE=true` is the single switch that turns it into a gate once the
false-positive rate is known. The Trivy version is pinned for the same reason as `govulncheck`.

## Documentation tests

Build documentation before committing a content or navigation change:

```bash
npm ci
npm run docs:build
```

The production build validates Markdown compilation and internal links.
