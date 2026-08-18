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
  upgrades/   Historical module lock fixtures for upgrade validation.
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

   This validates module ordering, module hooks, scoped env generation, generated
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

   Validates upgrade rules with multiple historical module lock fixtures without
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
   volumes, stops it, seeds an older `module.lock.yml`, starts the current release
   with `--build`, checks required containers are running, checks host data
   markers still exist, runs service-level data probes, and verifies
   `module.lock.yml` was updated to current module versions:

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

9. Directory event journal runtime test

   Against a running deployment carrying both samba_dc and authentik, this
   reproduces the stale-sync failure on purpose before proving the fix: with
   the watcher stopped, a group membership written to AD stays invisible to
   authentik and no sync runs; with the watcher running, the same change
   converges in seconds without waiting for the schedule. It also covers noise
   rejection, burst debouncing into a single sync, and cursor durability across
   a watcher restart.

   ```sh
   DOCKER_HOST=unix:///run/anas-anchor-docker.sock \
   ANAS_TEST_CONTAINER_PREFIX=anas_anchor_ \
     ./test-env/scripts/server-directory-events-e2e.sh
   ```

10. Database resource hot-add test

    Applies a PostgreSQL/authentik baseline, then adds Nextcloud. It proves the
    runner creates a dedicated `nextcloud` role and database through the
    one-shot provider operation without recreating PostgreSQL, and that an
    idempotent re-apply recreates neither PostgreSQL nor Nextcloud.

    ```sh
    DOCKER_HOST=unix:///run/anas-anchor-docker.sock \
      ./test-env/scripts/server-database-hot-add-e2e.sh <workspace>
    ```

11. Environment scope

    Proves that restricting each module's rendered `.env` to what its manifest
    declares changed nothing an application can see. It renders the same
    deployment under both the old and new rules and compares the results: the
    resolved `docker compose config` must match apart from the env_file
    passthrough, every `${VAR}` a compose file substitutes must still be
    delivered, and every variable an entrypoint or `.envsubst` template reads
    inside the container must still be present.

    ```sh
    ./test-env/scripts/test-env-scope.sh
    ```

    The comparison is only meaningful against the wide baseline: a variable the
    module never received cannot have been taken away, and most environment-looking
    references are not environment values at all (`$TTL` is a BIND zone
    directive, `APT_MIRROR_URL` a Dockerfile build argument).

12. Deploy preflight

    Checks a rendered deployment for the two collisions that stay invisible on
    a redeploy and abort the first cold create: a pinned subnet the host
    already routes, and a published port a previous failed create never
    released. Run it before applying on a host that runs containers of its own.

    ```sh
    DOCKER_HOST=unix:///run/anas-anchor-docker.sock \
      ./test-env/scripts/server-deploy-preflight.sh \
      <workspace>/.anas/deployments/<deployment-id>
    ```

13. MariaDB consumer runtime test

    `server-mariadb-modules-e2e.yml` binds every stable dual-database consumer
    (`llng`, `nextcloud`, and `meshcentral`) explicitly to MariaDB. Run it only
    against the isolated Docker daemon because it starts a complete directory,
    IAM, proxy, database, and application stack:

    ```sh
    export DOCKER_HOST=unix:///run/anas-docker-test.sock
    go run ./cmd/anas init /data/anas-mariadb-modules-test -y
    cp test-env/server-mariadb-modules-e2e.yml \
      /data/anas-mariadb-modules-test/config.yml
    go run ./cmd/anas apply -w /data/anas-mariadb-modules-test \
      --update-lock --no-snapshot -y
    ```

    Verify all three resource state files are `ready`, all four core containers
    (`mariadb`, `llng`, `nextcloud`, and `meshcentral`) are healthy, and the
    MariaDB schemas contain application tables. Then run `anas restart mariadb`
    and an idempotent `anas apply` to cover dependency restart ordering and
    persistence.

14. LAM `Admins` group login runtime test

    Against a running deployment carrying `samba_dc` and `lam`, this creates a
    temporary directory user and executes LAM's deployed search-and-bind policy.
    It proves a non-member is rejected, an enabled `Admins` member can bind with
    their own password, a wrong password fails, disabling the member revokes
    access, re-enabling restores it, and removing group membership revokes it
    again. The temporary user is removed on exit.

    ```sh
    DOCKER_HOST=unix:///run/anas-anchor-docker.sock \
    ANAS_TEST_CONTAINER_PREFIX=anas_anchor_ \
      ./test-env/scripts/server-lam-admins-e2e.sh
    ```

15. Authentik OIDC application login runtime test

    Against `server-identity-app-e2e.yml`, this drives the real Authentik
    identification, password, application authorization, authorization-code
    callback, and application session flows for both Nextcloud and
    MeshCentral. It also verifies that Nextcloud reused its LDAP user and that
    MeshCentral received the directory identity anchor and site-admin group.

    ```sh
    ANAS_TEST_PASSWORD='<directory-admin-password>' \
    ANAS_TEST_DOCKER_SOCKET=/run/anas-anchor-docker.sock \
    ANAS_TEST_CONTAINER_PREFIX=anas_anchor_ \
      ./test-env/scripts/server-authentik-oidc-login-e2e.sh
    ```

    `server-authentik-nextcloud-login-e2e.sh` remains the explicit Nextcloud
    SAML-fallback test and must be run against a deployment whose
    `nextcloud.iam_protocol` is `saml`.

16. Samba AD application-authorization matrices (run separately per IAM)

    These are the release-gating login tests. Neither test creates an IAM-local
    business user. Each creates temporary Samba AD accounts for direct
    `APP_nextcloud`, `APP_all`, `Admins`, no application group, disabled while
    authorized, and a recursive `ROLE_* -> APP_nextcloud` membership. The tests
    distinguish directory authentication failure from application-policy
    denial, complete the real OIDC callbacks for Nextcloud and MeshCentral,
    compare the resulting user IDs/display names/identity anchors, and validate
    every registered application's generic env, provider translation, and app
    runtime binding. NetBird is included in the registration/runtime contract;
    its browser-only dashboard callback remains outside this curl-based session
    probe while the module is marked experimental.

    Authentik deployment (`server-identity-app-e2e.yml`):

    ```sh
    ANAS_TEST_DOCKER_SOCKET=/run/anas-anchor-docker.sock \
    ANAS_TEST_CONTAINER_PREFIX=anas_anchor_ \
      ./test-env/scripts/server-authentik-login-matrix-e2e.sh
    ```

    LLNG must use its own deployment and is tested independently with
    `server-identity-app-llng-e2e.yml`:

    ```sh
    DOCKER_HOST=unix:///run/anas-llng-docker.sock \
    ANAS_TEST_CONTAINER_PREFIX=anas_llng_ \
    ANAS_TEST_DOMAIN=llng.nas.test \
    ANAS_TEST_ENTRY_IP=10.253.0.2 \
      ./test-env/scripts/server-llng-login-matrix-e2e.sh
    ```

    Both matrices assert that the bootstrap `admin` is absent from `APP_all` and
    each `APP_*`. Its `Admins` membership alone grants IAM access, keeping
    application-access groups free of redundant administrator membership.

17. Parameter in-place capability E2E

    Against the prepared isolated Samba DC, Lego, and Nextcloud deployment,
    this changes and restores all eight Samba password-policy settings plus
    Nextcloud language/locale while requiring stable container IDs. It also
    starts disposable LAM and Samba FS containers to prove profile rewriting,
    `smbcontrol all reload-config`, and ACL reconciliation work online. Finally,
    it verifies that Docker route labels and Lego's PID 1 environment cannot be
    changed in place.

    ```sh
    ANAS_TEST_DOCKER_HOST=unix:///run/anas-anchor-docker.sock \
    ANAS_TEST_CONTAINER_PREFIX=anas_anchor_ \
      ./test-env/scripts/server-parameter-inplace-e2e.sh
    ```

    Run this only against the isolated test daemon. The script restores live
    values and removes its disposable containers on both success and failure.

18. Samba-backed IAM password-policy E2E (run separately per IAM)

    These tests use temporary directory users and the real browser-facing
    password-change endpoints. They verify provider policy/guidance state,
    minimum-length and confirmation preflight, AD complexity handling, a
    successful Samba writeback (`pwdLastSet` changes and the new credential
    authenticates), safe user error mapping, and cleanup. The Authentik case also
    requires the synchronized user's local password to remain unusable and
    probes every safe LDAP error category without exposing its diagnostic and
    verifies that history/minimum-age values remain explicitly guidance-only.
    For LLNG, history and minimum age are guidance-only because its delegated
    LDAP reset does not reliably enforce user-change semantics. LLNG does not
    currently route Samba's must-change-at-next-login state into a forced
    password-change flow, so the test and module do not claim that capability.

    The scripts do not require the preceding credential to fail immediately:
    Samba can intentionally accept it during its configured old-password grace
    period. `pwdLastSet` plus authentication with the new credential is the
    stable writeback assertion.

    The scripts temporarily set the isolated domain's minimum password age to
    zero so one disposable account can cover multiple successful writes in one
    run.
    A trap restores the deployed value and removes the account. Never run them
    against a production domain.

    Authentik deployment (`server-identity-app-e2e.yml`):

    ```sh
    ANAS_TEST_DOCKER_SOCKET=/run/anas-anchor-docker.sock \
    ANAS_TEST_CONTAINER_PREFIX=anas_anchor_ \
      ./test-env/scripts/server-authentik-password-policy-e2e.sh
    ```

    LLNG deployment (`server-llng-password-policy-e2e.yml`):

    ```sh
    DOCKER_HOST=unix:///run/anas-llng-docker.sock \
    ANAS_TEST_CONTAINER_PREFIX=anas_llng_ \
    ANAS_TEST_DOMAIN=llng.nas.test \
    ANAS_TEST_ENTRY_IP=10.253.0.2 \
      ./test-env/scripts/server-llng-password-policy-e2e.sh
    ```

## Full Run

```sh
./test-env/scripts/test-all.sh
```

The full run performs static, render, upgrade-render, Compose config, build,
smoke, and runtime upgrade tests in that order.

`test-parameters.sh` requires the inventory to contain the exact 8
`hot_reload` and 12 `reconcile` parameters with an effect, an in-place
capability classification, and a runtime case. `test-parameter-effects.sh`
then runs every one of those 20 parameters through the real CLI, importer,
hooks, renderer, deployment diff, and activation flow with a deterministic
Docker command boundary. It checks the rendered runtime value, affected-module
Compose `up`, absence of `build`, same-value container idempotency, and
configuration/deployment recovery after an injected activation failure. The
server E2E above complements that deterministic matrix by testing the actual
upstream in-place mechanisms and stable container IDs on ln.

## Test Server Cleanup

Images pulled or built on a test server do not need to be removed after a test
run. Keep them for reuse by later runs unless disk space must be reclaimed.
Cleanup should still remove test containers, networks, and other temporary
runtime resources. Do not run a global `docker image prune` as routine test
cleanup.

## Matrix

- `min.yml`: minimal reverse-proxy baseline.
- `full.yml`: broad coverage of current modules.
- `matrix-auth.yml`: identity and account management, with LLNG explicitly
  bound to MariaDB; the wider fixtures keep PostgreSQL consumer coverage. Run
  it as a container smoke test with
  `ANAS_TEST_CONFIG=test-env/configs/matrix-auth.yml ANAS_TEST_WORKSPACE=.anas-test/runtime/matrix-auth ./test-env/scripts/test-smoke.sh`.
- `matrix-storage.yml`: Samba domain and file service.
- `matrix-apps.yml`: user-facing application stack.
- `matrix-network.yml`: network, certificate, DNS, TURN, VPN, and RADIUS modules.
- `matrix-db.yml`: database modules.

The removed `openldap` and `phpldapadmin` modules are not part of this matrix.

## Upgrade Validation

Upgrade tests are based on module lock fixtures:

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
- `module.lock.yml` records current module versions after the upgrade.

For module-specific migrations, add assertions to
`scripts/test-upgrade-probes.sh`. Prefer direct service probes, such as SQL,
LDAP, HTTP health endpoints, or file checks inside mounted volumes, over log
string matching.
