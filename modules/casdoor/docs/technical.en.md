# Casdoor technical implementation

This document records the protocol contract, security boundaries, and verification points for maintainers.

<!-- generated:module-identity:start -->
> Status: current implementation; based on `3.143.0-r11` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_casdoor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r11` | `traefik, db, casdoor` | 5 |
| `anas_casdoor_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r11` | `casdoor` | 3 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `casdoor.db_name` | string | — | `casdoor` | `static` | `CASDOOR_DB_NAME` | no | no | no | yes | `container_recreate` | Casdoor database name |
| `casdoor.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `CASDOOR_DB_TYPE` | no | no | no | no: `migrate-casdoor-database` | `data_migrate` | Relational database interface or automatic selection |
| `casdoor.domain_prefix` | string | — | `auth` | `static` | `CASDOOR_DOMAIN_PREFIX` | no | no | no | yes | `reconcile` | Service domain prefix and all IAM endpoints |
| `casdoor.ldap_auto_sync_minutes` | int | `>= 1` | `5` | `static` | `CASDOOR_LDAP_AUTO_SYNC_MINUTES` | no | no | no | yes | `container_recreate` | LDAP automatic synchronization interval in minutes |

## Data and startup flow

Revision r9 mounts frozen `app.conf`, owned by root with mode `0400`, read-only at `/opt/anas/conf/app.conf`. The root entrypoint uses `umask 077` to copy it into the container's writable `/conf/app.conf`, sets ownership to `1000:1000` and mode `0600`, and only then starts bootstrap and the long-running process. Copy or permission failures abort startup. Source artifacts and historical deployments keep their original content and permissions. The runtime copy is regenerated on every container start.

The hook renders `app.conf` with an explicit PostgreSQL `dbname` and an init-data template. At startup the helper reads the projected recovery password and replaces it with bcrypt in `/tmp/init_data.json`. Because upstream initializes LDAP auto-synchronizers before importing init data, the entrypoint briefly starts Casdoor to commit tables and managed objects, then starts the long-running process as UID/GID 1000. The entrypoint removes the meaningless in-container `lsof` lookup and stale init file so the bootstrap cannot kill itself or fail on the prior UID-1000 file. After the long-running HTTP service is available, it projects and verifies the recovery password again and only then publishes the readiness marker; the normal health check requires that marker, making a direct Docker restart self-contained. Init data explicitly consents to the privileged built-in recovery administrator and creates a non-signup internal directory Application for the `anas` organization. PostgreSQL is the only supported database interface.

Revision r10 builds Casdoor from the `3.143.0` source commit `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e` after verifying archive SHA-256 `365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460`. Six controlled patches add the synchronized SAML `displayName/externalId` fields; issue independent OIDC `sid` values while recording the parent Beego session and notify on user/admin session deletion with a two-minute Logout Token; log credential-free delivery failures; and use XORM field predicates so PostgreSQL does not parse an unquoted `user` column as the current database user. The final runtime remains the pinned official `3.143.0`; proxies do not change its source identity. Go build stages run on `BUILDPLATFORM` and cross-compile with BuildKit `TARGETOS/TARGETARCH`, while the final target stage executes no `RUN`; historical r8 amd64 deployment and arm64 build/unprivileged probes passed; r10 deployment and complete dual-architecture builds require separate acceptance.

## Managed credential lifecycle

`CASDOOR_SIGNING_MATERIAL` is a secret JSON bundle containing the current RSA
private key, certificate, and time-bounded old certificates. The deployment
manifest freezes projection locations, never values, and advances the public
`CASDOOR_SIGNING_CERT` projection in the candidate. The helper names the active
Casdoor Cert by its SHA-256 fingerprint and atomically moves managed Application
references. Old JWKS keys remain for one hour. An r7 upgrade also retains the
legacy `anas-signing` alias until that certificate leaves the trust window.

`CASDOOR_PORTAL_CLIENT_SECRET` uses the same credential transaction to update
the built-in Portal Application. Probe, reconcile, and verify read the candidate
from stdin and do not return its value. The Secret Store is committed only after
candidate activation, database read-back, and health verification; failures
restore the previous deployment, database value, and Store generation.

## LDAP, directory events, and authority boundary

The LDAP connection uses trusted LDAPS and a filter that excludes disabled accounts and requires the Samba anchor attribute. `anas_casdoor_dirwatch` follows `ANAS_DIRECTORY_EVENTS_DIR` read-only with its own durable cursor, filtering and debouncing changes before it calls local Casdoor APIs with this module's managed Application credential. Each batch reads directory and shadow users, correlates renames by permanent anchor, runs the upstream LDAP import, and then reconciles `externalId/name/ldap/properties/groups/isForbidden/isDeleted`. `externalId` stores the permanent Samba anchor while Casdoor's immutable `id` is left untouched. Directory properties are merged without deleting manual properties; `displayName,email` refresh for every currently synchronized user, and passwords or manual permissions are untouched.

Because upstream preserves existing groups, the subscriber queries the declared `ALLOW_GROUPS` with the same restricted bind over trusted LDAPS. It uses AD matching rule `1.2.840.113556.1.4.1941` for direct and recursive membership, then authoritatively replaces managed user groups. Missing groups, duplicate or missing anchors, or any failed Casdoor patch fail the batch and preserve the cursor for retry. Casdoor's default five-minute automatic sync remains enabled, so the subscriber is still a low-latency accelerator.

The integration imports users and verifies passwords remotely but does not enable password writeback. Delete events forbid and soft-delete the shadow record, deactivation events forbid it, and both clear its groups; re-enable or a rename with the same anchor reuses and restores the record. Formal r10 builds for both architectures, OIDC subjects and revocation, real Nextcloud integration, backup restoration, credential rotation, and lifecycle acceptance pass; see the [2026-10-06 acceptance](../dev-docs/plans/archived/casdoor-iam.md). ARM64 validation runs the target helper under QEMU. SAML application sessions and SLO remain future work.

## IAM boundaries

Pinned `3.143.0` publishes OIDC issuer/discovery and registers per-consumer clients with a one-hour access-token and 30-day refresh-token lifetime. ID and Logout Tokens share the exact session `sid`; the RS256 Logout Token carries `iss/aud/sub/iat/exp/jti/events`, and removing the declaration or switching to SAML clears the old back-channel URI. SAML publishes metadata, SSO, and the signing certificate without inventing SLO. Each `ALLOW_GROUPS` entry becomes a same-name Group/Role in the `anas` organization and an Approved Application Permission for the consumer, which Casdoor checks before issuing credentials. OIDC uses `JWT-Custom`/RS256: the registered permanent-anchor claim comes from `ExternalId`, group claims use Role names, and OIDC sub, UserInfo and Logout Token use ExternalId. SAML maps the registered display name and anchor to `$user.displayName` and `$user.externalId`, and groups to `$user.roles`; unknown sources are omitted. SAML NameID uses ExternalId, so consumers must use the explicit anchor attribute for stable linking.

### Directory attribute changes — implementation

One-to-one with the README's *Directory attribute changes*. This Module is both a Consumer (of Samba
AD) and a Provider (to the applications).

**Consumer side (Casdoor ← Samba AD)**

- **Which table and field persist identity**: Casdoor's `user` table. The `externalId` column holds
  the Samba permanent anchor (the matching key) and dirwatch never modifies Casdoor's own immutable
  `id` column; `name` is the username, `displayName`/`email` are labels, and remaining directory
  attributes are merged into `properties`.
- **How the matching key is configured**: `hook/main.go` builds `CASDOOR_LDAP_FILTER` as
  `(&<user class><enabled>(anasIdentityAnchor=*))` and renders
  `CASDOOR_DIRWATCH_IDENTITY_ANCHOR_ATTRIBUTE`; `ldapCustomAttributes` in `hook/iam.go` registers the
  anchor attribute as an LDAP custom attribute. Each `anas_casdoor_dirwatch` batch first reads the
  directory and the Casdoor shadow users, **correlates renamed users by the permanent anchor**, then
  runs the upstream LDAP import, and finally converges
  `externalId/name/ldap/properties/groups/isForbidden/isDeleted`.
- **Refreshed at each sync batch**: `displayName` and `email` for every currently synchronized user; `properties` is merged without deleting manually set attributes; passwords and manually
  granted permissions are never overwritten. `id` remains unchanged; `externalId` is reconciled against the permanent anchor.
- **Which interface performs revocation**: dirwatch calls the **local Casdoor API** with the Module's
  own managed Application credential to set `isForbidden`/`isDeleted` and clear groups. It follows
  `ANAS_DIRECTORY_EVENTS_DIR` read-only, resuming, filtering, and debouncing on its own cursor; the
  default 5-minute periodic full LDAP sync is the fallback.
- **Technical obstacle**: none. The Consumer side satisfies `DIRKEY-R-002` and `DIRKEY-R-007`. This
  implementation does not enable Casdoor's LDAP/AD password writeback and never treats Casdoor's local
  user records as directory authority.

**Provider side (Casdoor → the applications)**

- **OIDC**: all four JWT formats, UserInfo and Logout Tokens take `sub` from `ExternalId`, exactly
  matching the directory anchor. Custom subject overrides cannot bypass this check. Recovery OIDC
  administrators retain their internal ID.
- **SAML**: NameID in both versions uses `ExternalId`; 2.0 uses persistent format. Explicit anchor
  attributes remain available.
- **Build**: the Dockerfile applies six pinned-source patches. The source probe first reproduces
  the historical configuration boundary, then checks the runtime subject and revocation patches.
  Deployment acceptance is recorded separately.

**Directory-event revocation (r10)** reuses the helper and state directory, with no new service,
database or message broker. The hook derives `CASDOOR_DIRWATCH_APPLICATIONS` from `ALLOW_GROUPS`.
Before shadow mutation, `prepare` on `/api/anas-directory-revocation` captures old user/token/session
identifiers. After mutation, a second `prepare` durably captures authorizations issued during the update;
`revoke` then deletes the captured authorizations. Only Basic Auth credentials
of `admin/app-built-in` may call it, and its scope is `anas`; callers cannot supply receiver URLs or
signing keys. Every authorization receives an independent OIDC sid. `Token.SessionId` stores its
parent Beego session; `Token.UserId` links grants to the immutable internal user ID across renames
and label reuse. User updates, code issuance/redemption, refresh and revocation share one process-local mutex.
The Module runs one Casdoor instance; this does not provide multi-instance consistency. Explicit
central user/admin logout expires the corresponding parent grants. Unredeemed codes and centrally retired grants are deleted without notifying an absent RP session.
Repeated captures of the same application/sid/subject retain one notification. Refresh preserves the OIDC sid. Application-only revocation preserves
shared central login; replay preserves later independent authorizations.

`pending-logouts.json` uses the existing state directory, mode 0600, atomic replacement and file plus
directory fsync. It holds identifiers, never bearer tokens or secrets. `deliver` uses the registered
receiver, a fresh signed two-minute token, a five-second timeout and no redirects. Only 2xx responses
acknowledge delivery; retries continue at 2/5/10/30/60 seconds independently of the directory cursor.
Health records pending count, oldest timestamp and diagnostics; unsupported or overdue work is
unhealthy after 60 seconds. SAML SLO remains unavailable and captured SAML sessions keep an explicit
incomplete diagnostic. Startup and 300-second reconciliation repair journal gaps. Reused labels with
different anchors are quarantined without restoring access by label.

Isolated real-host acceptance on 2026-10-04 passes the r10 standards-consumer OIDC fault matrix:
saved cookies and real refresh grants, direct/nested group revocation, disable/delete, rename, label
reuse quarantine, receiver 503, durable retry across watcher restart, and protection of later grants.
A separate two-client run preserves the same user's other client and other users. Real Nextcloud r11
also preserves uid, LDAP anchor mapping and the original DAV file, and revokes saved cookies after
rename/direct-group removal. Signed SAML 2.0 SP protocol acceptance passes without certifying
Nextcloud SAML sessions or SLO. Candidate hashes, scripts and release gaps are in the
[acceptance review](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-10-04-casdoor-directory-identity-acceptance.md).
Defaults are five-second debounce and a 60-second minimum sync interval. This run allows 420 seconds
for shadow-state convergence followed by 120 seconds for session revocation; these are test timeouts,
not a service guarantee during failure.


**`DIRKEY-R-013` projection verdict: not applicable (a Provider is not a Consumer).** This Module
consumes nobody else's subject identifier; its role under `R-013` is to be the side that is verified
against. Note that switching the SAML NameID to the anchor would put the anchor into the assertion's
NameID field, so a SAML Consumer that creates accounts from the NameID would end up with UUID-shaped
user ids. The only SAML Consumer today is `nextcloud`, whose `uid_mapping` takes the anchor attribute
explicitly and resolves back to the LDAP account through `user_id_ldap_mapping` without reading the
NameID, so it is unaffected (see the section of the same name in that Module's technical document).

## Local administrator lifecycle

`admin_casdoor` is managed by the local-account inventory through the default `admin_{module}` template; Casdoor does not need `fixed_username`. Apply/rotate handlers stream the candidate through stdin, update bcrypt in PostgreSQL, verify the stored hash, and restore the old password if rotation fails.

## Restore into an empty workspace

After restoring, verify the database, Secret Store, administrator inventory, directory cursor, and deployment metadata.
The imported deployment remains bound to its original workspace and cannot be started directly. Stop the original stack,
then run `anas apply -w <restored-workspace> --module-root <modules-directory> --no-snapshot -y` to create a deployment
bound to the restored workspace. Verify the original user's login, permanent anchor, and signature, and keep the original
backup metadata as restoration evidence.

## Environment ownership

The module exports `ANAS_IAM_BINDING_*` and `ANAS_IAM_PORTAL_URL`, and explicitly consumes TLS, Samba LDAPS, `ANAS_DIRECTORY_EVENTS_*`, IAM consumer registrations, and the application catalog. Image builds forward the global `GOPROXY_URL` to the directory-event helper's Go builder instead of hard-coding a dependency on `proxy.golang.org`. The sensitive bind password and the Casdoor Application secret used by the subscriber stay inside this module; the Samba producer holds no Casdoor credential.

## Tests and implementation

`test-env/upgrades/configs/modules-casdoor.yml` declares the r8-to-r9 upgrade suite. It reuses the isolated upgrade runner and persistent database/directory markers, and checks Casdoor readiness, UID 1000, configuration mode `0600`, and the actual OIDC issuer. Catalog registration does not claim successful upgrade acceptance for the new revision; the retained finance deployment and Workspace temporary-storage acceptance have separate records.

- [`iam_test.go`](../hook/iam_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`helper/main_test.go`](../casdoor/helper/main_test.go)
- [`helper/directory_watch_test.go`](../casdoor/helper/directory_watch_test.go)
- [`server-casdoor-directory-events-e2e.sh`](../../../test-env/scripts/server-casdoor-directory-events-e2e.sh) (passed 2026-08-26 on an isolated Docker daemon of the explicitly designated server)
- [`server-casdoor-directory-authority-e2e.sh`](../../../test-env/scripts/server-casdoor-directory-authority-e2e.sh) (passed 2026-08-26 in the same isolated environment)
- [`server-casdoor-oidc-e2e.sh`](../../../test-env/scripts/server-casdoor-oidc-e2e.sh) (passed 2026-08-27 in the same isolated environment)
- [`server-casdoor-saml-e2e.sh`](../../../test-env/scripts/server-casdoor-saml-e2e.sh) (passed 2026-08-27 in the same isolated environment)
- [`server-casdoor-oidc-logout-e2e.sh`](../../../test-env/scripts/server-casdoor-oidc-logout-e2e.sh) (passed 2026-08-27 in the same isolated environment with a real consumer, multiple sessions, administrative API, signature, replay, and configuration-restore matrix)
- [`server-casdoor-local-admin-e2e.sh`](../../../test-env/scripts/server-casdoor-local-admin-e2e.sh) (passed 2026-08-27 on the latest r8 with recovery login, successful rotation, and failure rollback)
- [`server-casdoor-restore-e2e.sh`](../../../test-env/scripts/server-casdoor-restore-e2e.sh) (passed 2026-08-27 for Btrfs snapshot restore into an empty workspace and original-identity login)
- [`server-casdoor-lifecycle-e2e.sh`](../../../test-env/scripts/server-casdoor-lifecycle-e2e.sh) (passed 2026-08-27 for amd64 cold start/restart/upgrade/rollback and arm64 build/execution)
- [`server-casdoor-key-rotation-e2e.sh`](../../../test-env/scripts/server-casdoor-key-rotation-e2e.sh) (passed 2026-08-27 for signing and Portal secret rotation, overlap trust, and failure recovery)

## Current limitations

**Future work: SAML SLO and directory-driven termination of SAML application sessions.** OIDC is
the primary supported protocol; this item does not block the current OIDC release. SAML retains
optional SSO and verified signed-protocol behavior, with application-local logout. Future support
requires signature, NameID/SessionIndex, saved-cookie revocation and isolation acceptance before
publishing any SLO declaration.

r10 completed release acceptance. The r11 candidate adds optional trusted roles and policy events; acceptance of that new scope remains in progress. The pinned version has no SAML LogoutRequest/LogoutResponse consumer, so no SLO endpoint or binding is published. Directory password writeback, silent database switching, and using the Casdoor local User ID as the Samba permanent anchor also remain unsupported. The requirement matrix and implementation plan define the accepted release scope.

## Trusted OIDC application role source

The optional `ATTRIBUTES=<claim>:anasRole:1` maps to the fixed server's
`ANASRole.<admin-group>` calculation over managed `anas/<group>` membership. Editable Properties
and custom LDAP attributes cannot supply administrator roles. Optional `OIDC_CAEP_EVENTS=session-revoked`
requires the anchor subject and subject-wide backchannel registration. Negotiation is stored in
managed Application `anasPolicyRevocation` boolean (without changing login tag restrictions) and existing directory policy projection. The existing r10 pending-logout
journal and native privileged API deliver admission/administrator-role loss even without active
IAM tokens. Retried policy notifications preserve the original cutoff; ordinary sid logout does not
revoke independent API keys or shares. Real Immich acceptance for r11 is in progress; r10 evidence
is not evidence that the new combination passed.

The directory watcher connects directly to its managed service API on the private Compose network. Its HTTP client bypasses build and outbound proxies so Basic credentials do not reach an unrelated proxy. The native Casdoor server still delivers signed logout notices to applications.

Trusted roles come from managed directory groups. The pinned patch rejects changes
of `groups`, `isAdmin` and `externalId` in ordinary users' updates to `anas` accounts even when organization
`accountItems` is absent. The recovery administrator and directory service retain
the existing privileged group management entry points.

The pinned upstream `JWT-Custom` emits an empty `nonce` even when authorization
did not request one, which Immich's strict OIDC validation rejects. The patch
matches the native standard token's `omitempty` behavior: omit an unrequested
nonce and preserve a requested value. Client validation stays enabled; no IAM
parameter is added.
