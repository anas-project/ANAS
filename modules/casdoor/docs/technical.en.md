# Casdoor technical implementation

This document records the protocol contract, security boundaries, and verification points for maintainers.

<!-- generated:module-identity:start -->
> Status: current implementation; based on `3.143.0-r6` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_casdoor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r6` | `traefik, db, casdoor` | 5 |
| `anas_casdoor_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r6` | `casdoor` | 3 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `casdoor.db_name` | string | — | `casdoor` | `static` | `CASDOOR_DB_NAME` | no | no | no | yes | `container_recreate` | Casdoor database name |
| `casdoor.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `CASDOOR_DB_TYPE` | no | no | no | no: `migrate-casdoor-database` | `data_migrate` | Relational database interface or automatic selection |
| `casdoor.domain_prefix` | string | — | `auth` | `static` | `CASDOOR_DOMAIN_PREFIX` | no | no | no | yes | `reconcile` | Service domain prefix and all IAM endpoints |
| `casdoor.ldap_auto_sync_minutes` | int | `>= 1` | `5` | `static` | `CASDOOR_LDAP_AUTO_SYNC_MINUTES` | no | no | no | yes | `container_recreate` | LDAP automatic synchronization interval in minutes |

## Data and startup flow

The hook renders `app.conf` with an explicit PostgreSQL `dbname` and an init-data template. At startup the helper reads the projected recovery password and replaces it with bcrypt in `/tmp/init_data.json`. Because upstream initializes LDAP auto-synchronizers before importing init data, the entrypoint briefly starts Casdoor to commit tables and managed objects, then starts the long-running process as UID/GID 1000. The image removes the meaningless in-container `lsof` old-instance lookup so the bootstrap child cannot kill itself. Init data explicitly consents to the privileged built-in recovery administrator and creates a non-signup internal directory Application for the `anas` organization. PostgreSQL is the only supported database interface.

Revision r6 builds Casdoor from the `3.143.0` source commit `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e`, verifies archive SHA-256 `365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460`, and applies `casdoor/patches/0001-saml-directory-attributes.patch`. The patch adds only `displayName` and `externalId` to upstream SAML template evaluation so assertions can use already-synchronized directory fields; the final image remains based on the pinned official `3.143.0` runtime. Download and Go proxies may change with global build settings, but the source commit and checksum do not.

## LDAP, directory events, and authority boundary

The LDAP connection uses trusted LDAPS and a filter that excludes disabled accounts and requires the Samba anchor attribute. `anas_casdoor_dirwatch` follows `ANAS_DIRECTORY_EVENTS_DIR` read-only with its own durable cursor, filtering and debouncing changes before it calls local Casdoor APIs with this module's managed Application credential. Each batch reads directory and shadow users, correlates renames by permanent anchor, runs the upstream LDAP import, and then reconciles `externalId/name/ldap/properties/groups/isForbidden/isDeleted`. `externalId` stores the permanent Samba anchor while Casdoor's immutable `id` is left untouched. Directory properties are merged without deleting manual properties; `displayName,email` remain limited to users named by the current event batch, and passwords or manual permissions are untouched.

Because upstream preserves existing groups, the subscriber queries the declared `ALLOW_GROUPS` with the same restricted bind over trusted LDAPS. It uses AD matching rule `1.2.840.113556.1.4.1941` for direct and recursive membership, then authoritatively replaces managed user groups. Missing groups, duplicate or missing anchors, or any failed Casdoor patch fail the batch and preserve the cursor for retry. Casdoor's default five-minute automatic sync remains enabled, so the subscriber is still a low-latency accelerator.

The integration imports users and verifies passwords remotely but does not enable password writeback. Delete events forbid and soft-delete the shadow record, deactivation events forbid it, and both clear its groups; re-enable or a rename with the same anchor reuses and restores the record. This reconciliation has unit evidence but still requires real synchronization E2E.

## IAM boundaries

Pinned `3.143.0` publishes OIDC issuer/discovery and registers per-consumer clients with a one-hour access-token and 30-day refresh-token lifetime; removing the declaration or switching to SAML emits an empty back-channel URI to clear stale imported state, while actual notification remains restricted/pending acceptance. SAML publishes metadata, SSO, and the signing certificate without inventing SLO. Each `ALLOW_GROUPS` entry becomes a same-name Group/Role in the `anas` organization and an Approved Application Permission for the consumer, which Casdoor checks before issuing credentials. OIDC uses `JWT-Custom`/RS256: the registered permanent-anchor claim comes from `ExternalId`, group claims use Role names, and the immutable Casdoor User ID remains the stable `sub`. SAML maps the registered display name and anchor to `$user.displayName` and `$user.externalId`, and groups to `$user.roles`; unknown sources are omitted. SAML NameID remains the username, so consumers must use the explicit anchor attribute for stable linking.

## Local administrator lifecycle

`admin_casdoor` is managed by the local-account inventory through the default `admin_{module}` template; Casdoor does not need `fixed_username`. Apply/rotate handlers stream the candidate through stdin, update bcrypt in PostgreSQL, verify the stored hash, and restore the old password if rotation fails.

## Environment ownership

The module exports `ANAS_IAM_BINDING_*` and `ANAS_IAM_PORTAL_URL`, and explicitly consumes TLS, Samba LDAPS, `ANAS_DIRECTORY_EVENTS_*`, IAM consumer registrations, and the application catalog. Image builds forward the global `GOPROXY_URL` to the directory-event helper's Go builder instead of hard-coding a dependency on `proxy.golang.org`. The sensitive bind password and the Casdoor Application secret used by the subscriber stay inside this module; the Samba producer holds no Casdoor credential.

## Tests and implementation

- [`iam_test.go`](../hook/iam_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`helper/main_test.go`](../casdoor/helper/main_test.go)
- [`helper/directory_watch_test.go`](../casdoor/helper/directory_watch_test.go)
- [`server-casdoor-directory-events-e2e.sh`](../../../test-env/scripts/server-casdoor-directory-events-e2e.sh) (passed 2026-08-26 on an isolated Docker daemon of the explicitly designated server)
- [`server-casdoor-directory-authority-e2e.sh`](../../../test-env/scripts/server-casdoor-directory-authority-e2e.sh) (passed 2026-08-26 in the same isolated environment)
- [`server-casdoor-oidc-e2e.sh`](../../../test-env/scripts/server-casdoor-oidc-e2e.sh) (passed 2026-08-27 in the same isolated environment)
- [`server-casdoor-saml-e2e.sh`](../../../test-env/scripts/server-casdoor-saml-e2e.sh) (passed 2026-08-27 in the same isolated environment)

## Current limitations

Lifecycle remains `developing`. Directory-subscription and real OIDC/SAML E2Es cover creates, profile refresh, debounce, cursor recovery, deletion/deactivation, group admission, permanent anchors, rename reuse, and application permissions. OIDC session revocation, the SAML SLO scope decision, recovery login, backup restore, multi-architecture lifecycle, and key rotation still require acceptance before production evaluation.
