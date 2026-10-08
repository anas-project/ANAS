# Immich technical implementation

See the [README](../README.en.md) for operation. This page documents implemented code and keeps real-host
acceptance open where no evidence exists.

<!-- generated:module-identity:start -->
> Status: current implementation; based on `3.2.4-r1` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_immich` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-immich:3.2.4-r1` | `db, traefik, immich` | 3 |
| `anas_immich_machine_learning` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-immich-machine-learning:3.2.4` | `immich` | 1 |
| `anas_immich_valkey` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-immich-valkey:9.1.1` | `immich` | 1 |
<!-- generated:compose-topology:end -->

The server exposes 2283 only on the Traefik network and joins the Runner-resolved shared PG network.
Dedicated Valkey and ML services publish no host ports and use the module network. Valkey enables
AOF/everysec in its own data directory; network members and Docker administrators remain the queue trust
boundary. ML receives no database/OIDC secrets. The services Hook disables ML and sets application
ML enabled=false together.

The Valkey health command is `valkey-cli -e SET anas:healthcheck 1 EX 30`, testing a write and returning
nonzero for server errors. Real AOF ENOSPC on a bounded 4MiB tmpfs showed plain `valkey-cli ping`
printing MISCONF while exiting zero; the new probe exits one and Docker reports unhealthy. The probe
key expires quickly and does not inspect application queues.

## Pinned release and account guards

The server uses upstream `v3.2.4` index digest `sha256:d317916b28090c33eb36b308464ea391f8b7df1d850fcfea227a39ec879718c2`.
The ML mirror freezes `v3.2.4` digest `sha256:e16c2f166a8174901959fdf85e2e4c7bd1ebc4b37e0b6655de97c41408a260c4`.
The Valkey mirror freezes the upstream compose digest `sha256:70739f85ad2ee01a726a965584a0f94895f01b0c60b3cc8b0aeef11eaa6888cf`;
its inspected binary reports 9.1.1. Repository image/mirror inventories own publication; runtime installs
no dependencies.

Upstream passwordLogin.enabled protects only password login. The pinned build script
[`enforce-oidc-only.cjs`](../immich/enforce-oidc-only.cjs) adds exact guards to compiled services:
BaseService.createUser requires nonempty oauthId and no password; AuthService.changePassword/link/unlink
and AuthAdminService.unlinkAll reject; UserService.updateMe and UserAdminService.update reject password
fields. This also guards admin create and setup through the shared creation path. Update DTOs accept no
oauthId. Callback logic, internal ids, asset keys, and directory architecture are preserved. All anchors
are validated before writing; unknown, missing, duplicated, or already patched targets fail the build.
Sources: [AuthService](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/auth.service.ts),
[AuthAdminService](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/auth-admin.service.ts),
[BaseService](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/base.service.ts).

A real PG/signed OIDC fixture reproduced eight rows with the same oauthId from eight concurrent first
callbacks using one sub and different emails in the pinned image without a unique constraint. The
build patch now creates `user_oauthId_uq UNIQUE(oauthId)` inside the native migration lock before HTTP
startup and updates the pinned schema metadata. Soft-deleted rows retain their anchor: registration
with the same sub fails instead of creating a new binding. Repeated startup checks the constraint
definition; duplicates or a mismatching definition stop startup without merging or rebinding accounts.
Concurrent first-callback acceptance requires at least one success, exactly one active same-sub account,
the same internal id in every successful response, and no accessToken in rejected responses. Constraint
competition may return upstream 400/500; this does not establish success for every concurrent request.
The ordinary application database owner creates this constraint without superuser access.

Real queue ENOSPC also reproduced an upstream upload exception path that enqueues file cleanup before
rolling back its new asset. If cleanup enqueue fails too, HTTP returns 500 but retains the checksum
row, making healthy retry appear to be a duplicate upload. The pinned
[`fix-upload-queue-rollback.cjs`](../immich/fix-upload-queue-rollback.cjs) moves existing new-asset removal
before cleanup enqueue. Real HTTP checks confirm no failed asset remains and healthy retry completes
original/thumbnail processing. Existing duplicate assets are preserved. Unreferenced temporary files
may remain when cleanup cannot be queued; no additional file reclamation mechanism is claimed.

## Identity and configuration

calculate generates a stable IMMICH_OIDC_CLIENT_SECRET and publishes generic registration with explicit
sub:directory-anchor and anas_role:anasRole attributes. The IAM Provider maps trusted Admins membership;
upstream BaseService refuses the first ordinary visitor, and native OAuth roleClaim creates the first
administrator. Email/name are requested and the internal user.id remains upstream-owned.
Every directory user admitted to Immich, including the first administrator, needs a valid `mail`.
The native callback rejects a profile without email and leaves the database empty. `global.email`
is the service contact address and does not populate the directory administrator's `mail`.
Confirm the account address in the directory and deliver it through existing synchronization.
Local account creation must not bypass this prerequisite.

render reads only this consumer binding and writes private config.json containing its Secret. It never reads another
consumer binding or Provider administrator credentials. IMMICH_CONFIG_FILE makes upstream runtime
configuration-update API requests fail.

Native OAuthRepository validates the authorization grant, state, PKCE/issuer/signature/audience and
rejects empty sub. Existing sub wins; email is a later conflict check. Guards prevent normal fresh
installation from creating unbound accounts. The constraint patch retains unique bindings during
concurrent first login and after soft deletion, tested with real PG and the signed fixture. Real Linux/Btrfs AD/Authentik and AD/Casdoor tests verified first administrator and ordinary JIT, canonical anchors, email changes/conflicts, concurrent callbacks and soft-deleted binding protection. Provider combinations lacking the required trusted role or directory revocation capability fail configuration.

Redirects include /auth/login, native /user-settings, and app.immich:///oauth-callback, with no mobile
redirect override. Managed configuration sets passwordLogin=false, oauth.autoRegister/autoLaunch=true,
roleClaim=anas_role, storageLabelClaim="", backup.database.enabled=false, and queue concurrency. Changes
recreate containers; database name/type use data_migrate and never silently move data in ordinary apply.

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `immich.db_name` | string | — | `immich` | `static` | `IMMICH_DB_NAME` | no | no | no | no: `migrate-immich-database` | `data_migrate` | Application database name; changing it requires a matching media migration |
| `immich.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `IMMICH_DB_TYPE` | no | no | no | no: `migrate-immich-database` | `data_migrate` | Shared PostgreSQL only |
| `immich.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `photos` | `static` | `IMMICH_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Public photo service subdomain prefix |
| `immich.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `IMMICH_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | OIDC only; automatic selection must resolve to OIDC |
| `immich.job_concurrency` | int | `1..16` | `2` | `static` | `IMMICH_JOB_CONCURRENCY` | no | no | no | yes | `container_recreate` | Concurrency for non-video background queues |
| `immich.machine_learning` | bool | — | `true` | `static` | `IMMICH_MACHINE_LEARNING` | no | no | no | yes | `container_recreate` | Enable both application ML settings and the dedicated ML service |
| `immich.video_concurrency` | int | `1..8` | `1` | `static` | `IMMICH_VIDEO_CONCURRENCY` | no | no | no | yes | `container_recreate` | Video conversion queue concurrency |

Disabling ML retains photo/video upload and disables ML-dependent search/recognition. No whole-system
load acceptance has been performed; 4GB support is not claimed.

## Logout and directory events

The fixed RP logout patch collects actually deleted parent/descendant sessions in one transaction and uses the native SessionDelete path to disconnect each credential before constructing IAM
end_session navigation with the ID Token. Native /api/oauth/backchannel-logout checks signatures,
issuer/audience/algorithm/age/event/nonce and deletes OAuth sessions by sid/sub. Real HTTP/PG tests showed
native sid-less token replay revoking a newly created legitimate session. The pinned patch therefore
requires a nonempty jti after native verification and consumes verified issuer/client/jti atomically
with session deletion; duplicates return 400. The small `anas_immich_logout_token` table is created inside
the native migration lock and retained for five minutes from database receipt, covering the final valid
integer-second boundary. An in-transaction database clock check requires receipt age relative to iat in
`[-5,126)` seconds and rechecks optional exp with native JOSE tolerance, rejecting requests that expire while queued after verification. The patch also rejects
any nonce field, including empty values, and non-object backchannel events. Expired records are removed when
handling requests. No service or scheduler is added. Only one of
eight concurrent requests can revoke sessions, and replay across restart preserves the new session.
Generic OIDC_LOGOUT registration is reused. API keys and shares remain valid after these logout paths.

Directory admission loss negotiates `OIDC_CAEP_EVENTS=session-revoked` through existing registration.
Runner checks Provider support, backchannel, and subject-wide semantics. Pinned Authentik `2026.5.6-r15`
supports it; other combinations fail. The sender reuses LDAP sync, deletion transactions, and native
durable tasks with the directory anchor and shared-PG detection time, even without an AccessToken.
Trusted Admins-role loss persists notification in the native membership/group-update/delete transaction, even while admission remains allowed; the next native callback refreshes the role. Ordinary logout omits the policy member. The receiver validates signature, issuer/audience, sub_id,
policy and event time with the original OIDC backchannel member. Only the selected
[CAEP event](https://openid.net/specs/openid-caep-1_0.html) is implemented; complete SSF deployment is not claimed.

Deleting by credential creation time misses old-session keys created during notification delivery and
delayed old callbacks. The pinned [credential patch](../immich/guard-credential-races.cjs) retains verified
ID Token issue time in `anasOAuthIssuedAt`. Sessions, delegated sessions, API keys/rotation, and shares
inherit it from their actual source. Creation/revocation take the same anchor advisory lock and user-row
lock. `anas_immich_directory_revocation` retains only the maximum cutoff, including subjects without
their first user yet. The transaction consumes jti, advances the cutoff, and deletes old-source
credentials without deleting users/media or rebinding identities. Restored admission requires an ID
Token issued after the cutoff; second-resolution iat means same-second callbacks are conservatively
refused and may retry in the next second. Userinfo subject must match the verified ID Token; userinfo.iat
is never used as provenance. Native callback role updates also check cutoff under the same locks, preventing rejected old-admin callbacks from first changing isAdmin. API-key authentication retains its hash version in an internal nonenumerable symbol and rechecks it under lock, so an old request cannot borrow a newer same-ID rotation; this value is excluded from DTOs/JSON.

The [socket patch](../immich/fix-revoked-sockets.cjs) disconnects deleted session/API-key rooms on the
server, including native parentId cascade descendants. Connection setup joins a credential room before
revalidating. A successful same-ID API-key rotation disconnects its canonical UUID room before returning the new secret; the fresh secret can reconnect. It does not depend on client logout handling or disconnect a whole user room. Invalid
Logout Tokens log a fixed error without JOSE payload/cause. Real PG/HTTP/WebSocket run `f0961da23d`
passed receiver, provenance, and disconnection checks; native Authentik signing/sync method checks
passed. All five native directory functional cases passed across recorded runs. Automatic event propagation passed on the real host; physical mobile clients remain a release gate.

## Data and ANAS lifecycle

primary_database requests vector and earthdistance; the PG Provider owns dependencies such as cube and
privileged maintenance. The app uses an ordinary role. DB_VECTOR_EXTENSION=pgvector freezes vector
selection. The Provider must reconcile installed/default extension versions before app startup; do not
grant superuser to bypass upstream extension-update failures.

Managed media in data/immich/media and Valkey AOF are part of the database-coupled recovery set. The model
cache is reconstructable and included in the initial whole-workspace backup scope. Only ANAS performs
stop/snapshot/copy/restore; application database scheduling is disabled (this upstream version uses
pg_dump, not pg_dumpall). PG and media must match, and restoring affects other consumers and later
uploads. Single-app recovery, PG major migration, generic resource tiers, and 4GB support are not
implemented. External-library source files remain source-owner data with separately verified backup and
revocation boundaries; no automatic mount parameter is exposed.

## Verification and limits

- [main_test.go](../hook/main_test.go): config, resource projection, missing binding rejection, stable Secret,
  ML service and queue settings.
- [enforce-oidc-only.test.cjs](../immich/enforce-oidc-only.test.cjs): entry rejection, valid OIDC creation,
  and anchor counterexamples.
- [verify-image.cjs](../immich/verify-image.cjs): actual compiled pinned-image methods with repository doubles;
  this is not real IAM/PG E2E.
- [container-e2e.sh](../tests/container-e2e.sh): disposable networks/volumes with real PG, signed OIDC grants,
  HTTP login, forbidden endpoints, concurrent/soft-delete cases, and media; the server entry
  `test-env/scripts/server-immich-e2e.sh` additionally requires an isolated test daemon.
- [Private requirements](../dev-docs/requirements/immich-module.md) and [plan/evidence](../dev-docs/plans/immich-module.md).

`valkey-e2e.py` reads the actual Compose health command and exercises bounded ENOSPC. The guarded
`test-env/scripts/server-immich-workspace-e2e.sh` runs actual ANAS new/repeated apply, restart and full
workspace backup/restore with Samba AD, Authentik, and a second PG consumer. It requires a caller-selected
Linux/Btrfs host, isolated daemon, and test network namespace.

Local arm64 build, compiled-method regression, real-PG concurrency/soft-delete guards, photo/video
original hashes, and thumbnail jobs passed. Real paused metadataExtraction jobs survive a Valkey AOF
restart; native application bootstrap resumes processing to completion. Queue ENOSPC, failed-upload
rollback, and healthy retry also pass. Fixtures use a signed test IdP and do not establish native directory or client acceptance. Real AD/Authentik identity checks passed on the Linux/Btrfs host; physical mobile clients remain acceptance work. Real Linux/Btrfs ANAS whole-workspace restore, matching old/new images, maintenance failure and SIGKILL/exact frozen retry have passed; the current managed-HTTP-CA image combination has also passed matching whole-workspace recovery. Developing status
does not imply those checks passed.


On 2026-10-03 the isolated finance host passed administrator and ordinary-user browser acceptance: native OIDC, photo/video upload with original hashes/thumbnails, UI album creation/rename, RP and IAM-initiated logout, old Bearer/Cookie HTTP401 and fresh IAM authentication. Playwright1.62.1 with Chromium151.0.7922.34 runs in a temporary container on the same host; private synthetic accounts/media remain there. The internal-CA browser fixture does not establish ServiceWorker/PWA acceptance. Physical mobile uploads, background backup and sessions remain untested. Whole-workspace recovery with the current managed-HTTP-CA images has passed, including damaged media, both shared consumers, Valkey, original configuration/Secret bytes and exact images. All five native disable/delete/admission-loss/admin-role-loss/logout-first functional cases passed across recorded runs. Automatic event propagation passed without a manual sync: old credentials became HTTP401 in 71.206 seconds, within the 300-second host acceptance bound for this healthy fixed combination; existing matching old/new recovery evidence is retained.


Automatic-event evidence pairs new `Modify/member` sequence99 with persistent subscriber cursor99, native LDAP/signed-delivery DONE tasks, the verified receiver cutoff and old Bearer/Cookie/API-key/share HTTP401, preserving media/bindings and unrelated credentials. The 300-second bound is for this healthy fixed-combination test; it does not establish 4GB support, general capacity or fault-propagation guarantees. Exhausted native task retries still require the existing task-management path.
