# Real ANAS Immich workspace acceptance

[`server-immich-workspace-e2e.sh`](../../scripts/server-immich-workspace-e2e.sh)
deploys repository Modules through the real `anas init/apply/restart/backup` commands.
It uses Samba AD and native Authentik OIDC. Authentik is also a second consumer of shared PostgreSQL.
The existing direct-Docker `server-immich-e2e.sh` remains a separate upstream behavior test.

An operator must explicitly select the host and prepare its isolated Docker/containerd and ANAS test netns
using the existing test-environment workflow. This entry point performs no SSH, namespace provisioning,
host DNS or firewall mutation. It reuses the daemon, netns and proxy guards and rejects a default socket,
production data root, nonempty daemon, existing workspace, symlink path or non-Btrfs filesystem.
The fixed target Module tuple is PostgreSQL `18.4.0-r4`, Immich `3.2.4-r1`, Authentik `2026.5.6-r15`.
It requires `vector=0.8.2`, `cube=1.5`, `earthdistance=1.2` and empty preload.
Changing versions requires another upstream and harness review. ML is disabled; this does not validate
4GB hosts or mobile clients.

Required host tools are root, Btrfs tools, findmnt, ACL/xattr-capable rsync, Docker Compose, curl, Go,
Python 3.9+ and python3-pyyaml. Build `anas` and the adjacent `anas-helper` from the same source.
Python assertions must stay enabled; `python -O` and `PYTHONOPTIMIZE` cannot bypass acceptance checks.
`--modules` must point to the same-source checkout's modules with the existing go.mod/go.sum and contracts/ beside it.
The suite copies these into the private fixture root for current Contract validation and source Hook compilation without adding Go dependencies.
Repeated fixture creation checks the shared Contract contents and rejects catalog drift.
Allow sufficient storage for the cold build, complete shared database/media copies and full image archives for multiple recovery points.
Missing Btrfs, namespace, DNS or source-network access blocks execution instead of selecting a fake boundary.
The pinned Authentik shell still emits two banner lines. The suite uses verbosity 0, strictly checks and removes only those lines, and preserves the remaining stdout for JSON, integer and Task ID checks. A changed banner fails the suite and requires upstream review.
Before the first apply, the suite prepares Immich mirrors from the current `.github/mirrors.json` upstream digests and tags their target names only inside the isolated daemon. Unpublished Module mirrors need not exist in the registry; the suite never pushes them.
The suite explicitly caches inactive Compose service images because the recovery archive includes them.

```sh
go build -o /tmp/immich-workspace-bin/anas ./cmd/anas
go build -o /tmp/immich-workspace-bin/anas-helper ./cmd/anas-helper

# Allocate these addresses, port and namespace in the selected isolated lab.
# This example does not authorize any host.
sudo nsenter --net=/run/netns/anas-immich-test -- env \
  DOCKER_HOST=unix:///run/anas-immich-test.sock \
  ANAS_UPGRADE_NETNS_PATH=/run/netns/anas-immich-test \
  ./test-env/scripts/server-immich-workspace-e2e.sh \
  --anas /tmp/immich-workspace-bin/anas --modules "$PWD/modules" \
  --workspace /data/anas-immich-workspace-01234567abcdef89 \
  --host-ip 10.254.0.2 --host-lan-ip 10.254.0.250 \
  --host-lan-bridge-ip 10.254.0.249 --port 29443
```

`--preflight-only` performs read-only checks; `--keep-workspace` retains the fresh workspace.
Exit 0 means every runtime check and cleanup succeeded, 2 means preflight was blocked, and 1 means a runtime phase failed.
Evidence is saved at `<workspace>.reports/report.json`. CLI JSON, stderr and private credential files are mode 0600.
Nonzero exits and timeouts also retain available CLI output. Before failed cleanup, a bounded copy of active/deployment
state, including maintenance/restore guards, is saved in `failure-workspace-state.json` for later diagnosis.
`private-state.json` contains disposable user passwords: protect and delete it after review. Tokens/passwords are not printed.
Default cleanup stops and removes only this fresh workspace, its backups, containers and snapshots.
Images remain cached; no global prune is used. A failed ANAS stop or cleanup retains evidence and reports failure.
Remaining workspace containers after a successful stop also retain the workspace, preventing removal of live data.

Runtime assertions include:

- New ANAS apply, empty repeat apply, and repeat apply after media/account/album/Valkey state has been seeded.
- Real AD anchor and Authentik native authorization-code login; first ordinary visitor rejection and trusted-role administrator/user creation.
- Separate AD users for changed email, same-email/different-anchor conflict, concurrent first callbacks for the same sub, and native soft deletion followed by login.
  Use actual LDAP synchronization, distinct authorization codes and the administrator HTTP delete endpoint; SQL only asserts identity and never creates or rebinds accounts.
- Actual ordinary-role TCP connections for Immich and Authentik, wrong-password rejection, extension versions and PG 18.4.
  The Provider administrator separately reads restricted preload settings over private TCP; application roles receive no settings grant.
- Photo and real H.264 video uploads, original hashes, thumbnail jobs and albums, followed by real ANAS restart.
- Full workspace `backup create --mode copy` and `verify`, with captured service image IDs and archive checksum.
- Seed media and a genuine HNSW index using a private Module copy with PG 18.4, SCRAM and real pgvector 0.8.1,
  then apply the current 0.8.2 target through ANAS. The copy uses the audited source checksum and retains the current
  Provider and managed entrypoint. `18.4.0-r3` is an isolated-daemon test identity, **not a published or historical release**;
  metadata, localization and both Compose services agree. Validate every patch anchor before copying, leave source Modules intact,
  and check the fixed compiled Immich version range at runtime. Docker events must show every old runtime container stopping
  before target PG starts, alongside the complete ANAS recovery point, ordinary-role versions and HNSW/media/shared-consumer checks.
- Actual `anas plan` before and after upgrade records both database consumers and the complete stop/restart scope,
  while runtime, config, Secret and active-state hashes must remain unchanged.
- ANAS restore/start after deliberate changes to both database states, managed media, Valkey, config and Secret;
  recheck internal user IDs/anchors, hashes, album, other consumer marker, config/Secret and exact images.
- Restore matching old 0.8.1 data/images, then deliberately fail a temporary PG Hook only after native maintenance succeeds.
  Require the existing `postgres_maintenance_target` record, a stopped workspace, and rejected ordinary start/restart,
  different apply and rollback. Recover only through the matching ANAS snapshot to 0.8.1, then the matching full workspace
  backup to 0.8.2. Restore must return with all writers stopped; explicit start verifies the corresponding image IDs,
  actual versions, HNSW, media and other shared database. Never forge pg_extension catalogs or open new data with an old image.
  Docker events are auxiliary evidence; request ordering alone does not establish quiescence.
- A private Hook pauses only after successful native extension maintenance. Verify the exact Linux identities of this run's CLI/Hook before SIGKILL.
  Persistent guards must reject ordinary starts and other candidates; `apply --deployment` then retries the same frozen candidate.
  Its original recovery point must remain complete and retry must stop the failed candidate before consumers resume.
  Recheck media, HNSW, the shared database and guard removal. Only the test copy contains pause markers; production Hooks and the request ABI remain unchanged.
  This step becomes acceptance evidence only after execution on the selected host.
- Separate users for real AD disable, APP_immich removal, account deletion, native RP/IAM logout before removal, and Admins loss while retaining APP_immich admission.
  Wait for existing LDAP sync and directory delivery tasks to reach DONE, then require old Bearer/Cookie/API key/share HTTP access to fail.
  Managed originals/anchors stay intact and another user's session/API key/share must remain valid.
- After Admins loss, wait for a PG second later than the signed cutoff before native OIDC login: the same internal ID becomes an ordinary user, fresh credentials work, and old administrator credentials remain invalid.
- Re-admit the removed user and retain the internal ID on native login. Invoke the native signing sender again with the frozen Provider, issuer, anchor and original receiver-ledger cutoff;
  old credentials must stay invalid while new session/API key/share and unrelated users survive.
  Ordinary logout separately retains API keys/shares; directory delivery must revoke them even after the active Immich OAuth grant is gone.

The fixed Authentik worker clears task messages after DONE; the suite never decodes completed messages.
The report's `sender_tasks_done_window` contains only message_id, retries and mtime for tasks in the time window,
without assigning a recipient or event epoch. `receiver_signed_cutoff` reads the anchor/epoch written by Immich's
native signed-event receiver and pairs it with HTTP assertions as target evidence. A retry waits for its exact
returned message_id to reach DONE, then rechecks fresh and old credentials. No production task-retention protocol is added.

This covers the entry-point portions of `IMMI-R-001/002/003/005/007/008/010/011/012` and the existing RDBEXT/ALOG/backup requirements.
It does not complete rows that also require mobile clients, disk-full, every credential race or the full logout-security negative matrix.
Only an actual host-run report is acceptance evidence. Harness unit tests are separate:

```sh
PYTHONDONTWRITEBYTECODE=1 python3 test-env/scripts/test_immich_workspace_e2e.py
PYTHONDONTWRITEBYTECODE=1 python3 test-env/scripts/test_immich_workspace_plan.py
PYTHONDONTWRITEBYTECODE=1 python3 test-env/scripts/test_immich_workspace_crash.py
```

The suite uses only managed workspace data. It does not test external libraries or reconstruct external mount/nested-subvolume topology.
Fixed upstream APIs follow Immich `v3.2.4`
[`constants.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/constants.ts) (pgvector range `>=0.5 <1`),
[`server.controller.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/controllers/server.controller.ts) and
[`album.controller.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/controllers/album.controller.ts).
The native logout route follows the fixed
[`auth.controller.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/controllers/auth.controller.ts).
The [backup specification](../../../docs/en/reference/contracts/backup.md) and
[Immich requirement matrix](../../../modules/immich/dev-docs/requirements/immich-module.md) remain the acceptance sources.

Browser acceptance reuses `npm run e2e:immich-browser`, existing Playwright, and installed Chrome. It
accepts only this fixture's HTTPS `photos.iw<8 hex digits>.immich.test` origin. Forward the same port
through SSH to the dedicated namespace entry; Chromium's private resolver maps only that fixture
suffix to 127.0.0.1 without changing host DNS or browser profiles. After the server suite stops, explicitly
start the retained workspace and supply each administrator/ordinary test account through private inputs:
`ANAS_TEST_APP_URL`, `ANAS_TEST_IAM_URL`, `ANAS_TEST_USERNAME`, `ANAS_TEST_PASSWORD`,
`ANAS_TEST_ROLE=admin|user`, `ANAS_TEST_PHOTO`, `ANAS_TEST_VIDEO`, and a private `ANAS_TEST_REPORT_FILE`.
Use this test's PNG/H.264 originals and an account that has not uploaded them; duplicates fail explicitly.
Keep credentials out of command history and reports. The browser runs native OIDC/onboarding, UI photo
and video uploads, SHA256/thumbnail checks, UI album creation/rename, native RP and direct IAM logout, old Bearer/Cookie session HTTP401,
and a fresh OIDC attempt requiring IAM login. The existing sanitized reporter disables screenshots,
traces, and video. This entry does not replace acceptance on a physical mobile device.

Alternatively, run the same entry on the original test host inside its dedicated daemon/network namespace
with official `mcr.microsoft.com/playwright:v1.62.1-noble` and `ANAS_TEST_BROWSER_CHANNEL=chromium`.
Keep the server isolation guard and mount only public test runtime code, selected private fixture login
fields, synthetic media, and a separate output directory; mount neither Docker socket nor the full Secret
store. Chromium's 127.0.0.1 belongs to the dedicated namespace. This mode needs no private fixture export.
The native UI test selects en-US and retains the existing sanitized report format.

When the large official image cannot be downloaded promptly, reuse an existing Node image in the same
dedicated container with Playwright 1.62.1’s official Chromium headless shell 151.0.7922.34/linux64. Install
its system libraries and select its absolute path with `ANAS_TEST_BROWSER_EXECUTABLE`. Verify the public
runtime checksum first; credentials/media stay on the original host and the same mount boundaries apply.


Automatic directory-event acceptance uses `verify_directory_event_delivery`: fixture preparation may request a sync, but removing `APP_immich` membership must not manually trigger one. It checks the new `Modify/member` event, the managed subscriber cursor/trigger time, native LDAP/signed-delivery DONE tasks, the verified receiver cutoff and real old Bearer/Cookie/API-key/share denial, preserving media/bindings and unrelated credentials. It records elapsed time against a 300-second host acceptance bound for this healthy fixed combination. Real-host evidence is required; unit tests do not establish the bound, 4GB support or general capacity guarantees.

Extension upgrades use ordinary `apply` with prepared images checked against their fixed sources;
this data lifecycle step does not force rebuilding every module. Image builds are accepted separately.
Real application-role extension versions, preload and HNSW checks, actual running-image capture and
ANAS full-workspace recovery points remain mandatory. The Casdoor entry reuses the isolated driver;
independent grants meet before concurrent callbacks, which must retain one active binding. Fresh-email
registration after soft deletion must issue no credentials and preserve the full tombstone; upstream
rejection is not restricted to HTTP 400.

Set `ANAS_TEST_IAM_PROVIDER=casdoor` to select the fixed Casdoor native login form and `/api/logout`; the default remains `authentik`. Both providers share media, album and old-session rejection assertions. The new Casdoor branch requires real browser acceptance; syntax checks do not count as acceptance.

Browser tests map the isolated domain to `127.0.0.1` by default. When Docker host networking does not enter the test host's separate network namespace, set `ANAS_TEST_HOST_ADDRESS` to its isolated IPv4 address (loopback or `10.0.0.0/8` only). Check the entry response first to avoid connecting to a different proxy.

Official-source preflight uses HEAD to verify HTTPS/HTTP reachability without downloading the GitHub landing page. Actual build inputs retain independent fixed-digest verification. The 2026-10-08 user-authorized finance workload suspension, full suite and original-service restoration are recorded in modules/immich/dev-docs/plans/immich-module.md.
