# ANAS Refactor Test Environment

This directory defines an isolated test environment for validating the Go
refactor before replacing the current launcher.

The test flow intentionally skips legacy Ruby comparison. It validates the
refactor on its own through static tests, rendering, Docker Compose validation,
image builds, and optional smoke checks.

## Layout

```text
test-env/
  configs/    Test matrix configs.
  scripts/    Repeatable test commands.
  upgrades/   Historical cask lock fixtures for upgrade validation.
  reports/    Generated logs and command output.
```

Runtime state is written under `.anas-test/` at the refactor root:

```text
.anas-test/
  runtime/
  data/
  logs/
```

Do not commit `.anas-test/` or generated secrets.

## Test Levels

1. Static tests

   Runs Go unit tests, manifest validation, the legacy-template ban, and
   container configuration generation tests for Traefik, Eturnal, and
   MeshCentral (the MeshCentral case runs when Node.js is available):

   ```sh
   ./test-env/scripts/test-static.sh
   ```

2. Render tests

   Renders every matrix config into an isolated runtime path:

   ```sh
   ./test-env/scripts/test-render.sh
   ```

   This validates module ordering, cask hooks, scoped env generation, generated
   files, persistent secrets, and the absence of legacy host-rendered templates.

3. Docker Compose config tests

   Runs `docker compose config` for every rendered module:

   ```sh
   ./test-env/scripts/test-compose-config.sh
   ```

   This catches missing env values, invalid Compose syntax, broken build
   contexts, and invalid network or volume definitions before containers start.

4. Image build tests

   Builds all images required by the full config:

   ```sh
   ./test-env/scripts/test-build.sh
   ```

5. Upgrade render tests

   Validates upgrade rules with multiple historical cask lock fixtures without
   starting Docker:

   ```sh
   ./test-env/scripts/test-upgrade-render.sh
   ```

   Supported fixtures must plan and render successfully. Rejected fixtures, such
   as future-version downgrade locks, must fail.

6. Smoke tests

   Starts the full config and checks container state:

   ```sh
   ./test-env/scripts/test-smoke.sh
   ```

   This is intentionally the last step because it touches Docker runtime state.

7. Runtime upgrade tests

   Starts a baseline release, writes migration probe data into persisted
   volumes, stops it, seeds an older `cask.lock.yml`, starts the current release
   with `--build`, checks required containers are running, checks host data
   markers still exist, runs service-level data probes, and verifies
   `cask.lock.yml` was updated to current cask versions:

   ```sh
   ./test-env/scripts/test-upgrade.sh previous-patch
   ```

   Available upgrade fixtures live under `test-env/upgrades/supported/`.

8. Samba identity-anchor runtime test

   Against a running server deployment, verifies audit-triggered user and
   group stamping, exclusion of computer accounts, and startup reconciliation
   after the worker misses a create event:

   ```sh
   DOCKER_HOST=unix:///run/anas-docker-test.sock \
   ANAS_TEST_CONTAINER_PREFIX=anas_anchor_ \
     ./test-env/scripts/server-anchor-e2e.sh
   ```

## Full Run

```sh
./test-env/scripts/test-all.sh
```

The full run performs static, render, upgrade-render, Compose config, build,
smoke, and runtime upgrade tests in that order.

## Test Server Cleanup

Images pulled or built on a test server do not need to be removed after a test
run. Keep them for reuse by later runs unless disk space must be reclaimed.
Cleanup should still remove test containers, networks, and other temporary
runtime resources. Do not run a global `docker image prune` as routine test
cleanup.

## Matrix

- `min.yml`: minimal reverse-proxy baseline.
- `full.yml`: broad coverage of current casks.
- `matrix-auth.yml`: identity and account management.
- `matrix-storage.yml`: Samba domain and file service.
- `matrix-apps.yml`: user-facing application stack.
- `matrix-network.yml`: network, certificate, DNS, TURN, VPN, and RADIUS casks.
- `matrix-db.yml`: database casks.

The removed `openldap` and `phpldapadmin` modules are not part of this matrix.

## Upgrade Validation

Upgrade tests are based on cask lock fixtures:

- `upgrades/supported/previous-patch.lock.yml`: one-step older versions.
- `upgrades/supported/mixed-old.lock.yml`: mixed older versions across modules.
- `upgrades/rejected/future-downgrade.lock.yml`: a future version that must be
  rejected as a downgrade.

`test-upgrade-render.sh` is safe for fast validation because it only runs
`plan` and `render`. `test-upgrade.sh` starts a baseline, seeds persisted probe
data, injects an older lock, starts the upgraded release, and verifies migration
signals:

- required containers are running;
- persisted host data markers remain present;
- PostgreSQL responds to `pg_isready`;
- MariaDB, Samba DC, and Nextcloud still see their mounted data directories;
- `cask.lock.yml` records current cask versions after the upgrade.

For module-specific migrations, add assertions to
`scripts/test-upgrade-probes.sh`. Prefer direct service probes, such as SQL,
LDAP, HTTP health endpoints, or file checks inside mounted volumes, over log
string matching.
