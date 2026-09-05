# Upgrade E2E catalog

`catalog.yml` is the release gate for Core, Web, and Module upgrades. It is not
an execution report: a transition says which real E2E suite must run for an
exact old-to-new boundary. Validate it with:

```bash
go run ./cmd/check-upgrade-tests
go run ./cmd/check-upgrade-tests --base-ref v0.1.1 --scope core,web
go run ./cmd/check-upgrade-tests --base-ref image-release/46-2 --scope modules
bash scripts/ci/module-upgrade-fixture-compatibility.sh image-release/46-2
```

## Core or Web change

1. Find the newest `v*` release tag reachable from the release branch.
2. Add a transition whose `from_ref` resolves to that exact commit and whose
   target is `worktree`. Keep the minimum supported Core transition as well.
3. Run `scripts/ci/core-upgrade-e2e.sh <tag>`. For Web, run the registered real
   browser suite using the old release's console store and browser fixture.
4. Record the successful, source-identified run in the paired implementation
   plan. The ANAS release workflow repeats the latest Core run before building.

A Web `baseline` is allowed only while the base Git tree has no complete Web
artifact. As soon as one release contains Web, `--base-ref` rejects the baseline
as a substitute for a transition.

## Module change

1. Use the latest successful `image-release/*` tag as the base and run
   `scripts/ci/module-revisions.sh --base <tag> --write` before review.
2. Update the Module's `current` release and add an exact `from` → `to`
   transition. Add the Module to the suite's `modules` list; merely naming a
   suite that does not deploy the Module is rejected.
3. Extract the base tree, build its Linux `anas`, and pass that binary and its
   `modules/` directory together with the worktree binary/root to:

   ```bash
   test-env/scripts/server-module-upgrade-e2e.sh \
     OLD_ANAS NEW_ANAS OLD_MODULES NEW_MODULES \
     CONFIG SEED VERIFY /srv/anas-upgrade-RUN_ID
   ```

4. Run on an isolated Docker socket/data-root. The runner starts the old stack,
   first requires every long-lived old service to be running and ready (while
   allowing only a successful `*_init` one-shot), then seeds persisted data,
   upgrades, verifies, rolls back to the immutable old deployment, verifies
   again, reapplies the new deployment, and verifies a third time. A restarting,
   unhealthy, still-starting, or failed-init baseline is rejected.

Each config has a sibling `.targets` file whose entries must exactly match the
suite's `modules` list. For the current endpoint, the runner copies the exact
old Module tree and overlays only those targets from the worktree. Dependencies
assigned to another suite therefore stay at their old release instead of being
silently upgraded and misattributed to this run; catalog validation rejects any
target-inventory drift.

An exact historical release with a known topology defect may use
`server-upgrade-old-compat.sh` only when that helper explicitly matches its
`version-rN`. The current `samba_fs 4.23.6-r5` case temporarily enables proxy
ARP inside the test namespace and seeds the member A record without rebuilding
or changing the old image. The runner retires this state before current code is
activated, enables it again only for the old-deployment rollback, then restores
the prior network state; each action is retained as value-free report evidence.

Module upgrade configs must leave `global.chinese_build_speedup` disabled and
must not define top-level `env` build inputs. The old endpoint reuses exact
`version-rN` images from the release registry; changing build inputs would make
the historical CLI require `--build` and destroy that artifact identity.

The runner must execute inside the same explicitly named test network namespace
as the isolated daemon. A complete invocation has this shape (use the exact
commit identities for the run):

```bash
sudo env \
  DOCKER_HOST=unix:///run/anas-docker-test.sock \
  ANAS_UPGRADE_NETNS_PATH=/run/netns/anas-test \
  ANAS_UPGRADE_SUITE=modules-base \
  ANAS_UPGRADE_FROM=image-release/46-2 \
  ANAS_UPGRADE_TO=worktree \
  nsenter --net=/run/netns/anas-test -- \
  test-env/scripts/server-module-upgrade-e2e.sh \
  OLD_ANAS NEW_ANAS OLD_MODULES NEW_MODULES CONFIG SEED VERIFY \
  /srv/anas-upgrade-RUN_ID
```

The workspace is removed after the run. Cleanup asks the current ANAS CLI to
delete this workspace's managed Btrfs snapshots before removing its ordinary
files, so a failed apply cannot strand read-only snapshot subvolumes. Value-free
JSON, JUnit, and Markdown evidence remains in
`/srv/anas-upgrade-RUN_ID.reports`, including a failure phase and cleanup result
when the suite fails.

`no_prior_release` is only for a Module absent from the release base. Once the
base contains it, any release identity change requires a transition. The Module
artifact workflow checks this after revision calculation and before any image
or package build.

The fixture compatibility command extracts and builds the exact historical
CLI, then proves every catalogued Module config can initialize with both the
historical and worktree Module roots. Catalog validation also requires every
Module named by a suite to be selected explicitly by that config. This fast
gate validates the test inputs; it does not replace the Docker E2E.

The files under `supported/` and `rejected/` are historical lock fixtures for
fast lock/render compatibility checks. They do not contain or execute an old
runtime and must not be cited as release-upgrade E2E evidence.
