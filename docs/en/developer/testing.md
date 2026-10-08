# Testing

Run the Go suite for runner and module-hook changes:

```bash
go test ./...
```

Positive fixtures that require a private directory should explicitly set its mode to `0700` so they
meet production checks across host umasks. Permission-rejection fixtures should set the tested mode
explicitly and run with the usual `022` umask. Real process-group cancellation tests in a container
should use Docker `--init` to reap terminated orphan children before asserting that the group no longer exists.
Tests that check every directory ancestor also need a private temporary root: set container `TMPDIR`
to a `0700` directory on an independent private tmpfs, with private ancestors, outside the source Git
repository so the host's Btrfs filesystem or parent repository does not change ordinary fixture behavior.

Runtime endpoint regressions should verify all five Docker selectors, empty-versus-unset values and
restricted environment on every subprocess. Linux checks context when needed, daemon identity and
the complete container inventory before Compose; non-Linux storage inspection does not support that path.
Fixtures must return valid responses and assert the exact platform-specific sequence rather than assuming
two calls or accepting unknown commands.

Use the relevant scripts under `test-env/` for integration behavior. Tests that need Docker, real DNS, networking, or remote hosts require an explicit isolated environment.

### Workspace temporary storage editing

`TEMP-T-021` opens its ODT through `/f/<id>` using the WebDAV file ID. The
[Nextcloud 34.0.2 file route](https://github.com/nextcloud/server/blob/v34.0.2/apps/files/lib/Controller/ViewController.php)
places that ID in the Files path and sets the boolean switch `openfile=true`; `openfile` does not carry the ID.
Acceptance still requires a real Collabora editor, saved document content, and reopening after lifecycle
actions. See repository `test-env/cases/workspace-temp-storage/EDITING-STACK.md`.

Before typing, wait for the document canvas, initialized map, `_docLoaded`, edit permission,
and attached editable `div.clipboard#clipboard-area`, with the busy overlay absent.
Typing, save, and copy key events target that real input node. The pinned
[CODE upstream test helper](https://github.com/CollaboraOnline/online.mirror/blob/cp-26.04.2-4/cypress_test/integration_tests/common/helper.js)
also sends document input to `div.clipboard`; a visible frame or map alone does not establish input readiness.
The test still checks saved ODT XML and content in the reopened editor.

Close only a visible known welcome dialog through its inner iframe's normal controls:
click `#slide-3-indicator` if the third-page Close button is not yet visible, then click
the real `#slide-3-button`. If already on that page, click Close directly.
Cancel a settings dialog through `#iframe-settings-cancel` in the parent editor frame,
without saving settings. Each control click and removal wait is bounded by 30 seconds.
The fixed image listens for Escape in the parent window; pressing it on the inner body
does not establish a verified dismissal path.
Each normal canvas click has a five-second timeout, with at most three attempts. Only a real
`TimeoutError` with a visible known dialog allows dismissal and retry, covering a late dialog.
Unknown errors, timeouts without a known dialog, failed dismissal, and the attempt limit preserve
the original click error; the test never forces a click or modifies DOM/app state.
A successful click still requires the read-only `editorHasFocus()` check.
The editing browser has a 1 GiB memory limit;
dependency installation retains 768 MiB. Shared memory is 256 MiB and temporary tmpfs is 512 MiB,
with a 256-process limit, and finance gates run serially. A browser crash without OOM evidence retains an unknown cause.
One successful tmpfs comparison does not establish the cause of earlier crashes.

After reopening, real `Control+A` waits for both document selection handles to be attached
before `Control+C`, following the pinned upstream `selectAllText` helper.
The native clipboard and WebDAV ODT XML must still contain the saved text independently;
the test does not write clipboard/app state or call a copy protocol directly.

Clipboard read and write permissions are granted only to this isolated Playwright context,
without restricting them to one origin, so the cross-origin Collabora editor embedded by
Nextcloud can use the native clipboard. The pinned Chromium implementation applies a single-origin
grant to both requesting and embedding origins. Other browser contexts are unaffected;
native copied text and saved document content still require independent verification.

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

`implementation.files` accepts Go, JavaScript, TypeScript, Python, and Shell files,
plus `.in` configuration templates and `.txt` fixed input lists, each with a
`TEST_CASES:` comment. Templates and image lists contribute to the implementation
digest, so input changes require review and a digest update. Missing reverse
markers are rejected.

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

## Project review regression checks

The regular PR/master web CI job runs `npm ci`, `check:api`, `typecheck`, `test`, and builds both the main
and emergency interfaces. `check:api` generates OpenAPI types into a temporary directory and compares
only the target file; it neither rewrites tracked types nor requires a clean working tree.
`go run ./cmd/check-shared-build` checks shared image dependency declarations.

`go test ./internal/runner ./internal/jobexecutor ./internal/compose ./internal/deploymentaudit ./modules/lego/hook`
covers recovery failures blocking the queue, ownership/Compose endpoint consistency, durable primary
failures before recovery, diagnostic/quiet boundaries, and audit rejection. These fake-process and local
filesystem checks do not replace acceptance on real Docker/Incus/IAM hosts.
