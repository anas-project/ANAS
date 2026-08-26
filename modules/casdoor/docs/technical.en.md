# Casdoor technical implementation

This document records the protocol contract, security boundaries, and verification points for maintainers.

<!-- generated:module-identity:start -->
> Status: current implementation; based on `3.143.0-r4` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_casdoor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r4` | `traefik, db, casdoor` | 5 |
| `anas_casdoor_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r4` | `casdoor` | 3 |
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

## LDAP, directory events, and authority boundary

The LDAP connection uses trusted LDAPS and a filter that excludes disabled accounts and requires the Samba anchor attribute. `anas_casdoor_dirwatch` follows `ANAS_DIRECTORY_EVENTS_DIR` read-only with its own durable cursor, filtering and debouncing changes before it calls local Casdoor APIs with this module's managed Application credential. Each batch reads directory and shadow users, correlates renames by permanent anchor, runs the upstream LDAP import, and then reconciles `id/name/ldap/properties/groups/isForbidden/isDeleted`. Directory properties are merged without deleting manual properties; `displayName,email` remain limited to users named by the current event batch, and passwords or manual permissions are untouched.

Because upstream preserves existing groups, the subscriber queries the declared `ALLOW_GROUPS` with the same restricted bind over trusted LDAPS. It uses AD matching rule `1.2.840.113556.1.4.1941` for direct and recursive membership, then authoritatively replaces managed user groups. Missing groups, duplicate or missing anchors, or any failed Casdoor patch fail the batch and preserve the cursor for retry. Casdoor's default five-minute automatic sync remains enabled, so the subscriber is still a low-latency accelerator.

The integration imports users and verifies passwords remotely but does not enable password writeback. Delete events forbid and soft-delete the shadow record, deactivation events forbid it, and both clear its groups; re-enable or a rename with the same anchor reuses and restores the record. This reconciliation has unit evidence but still requires real synchronization E2E.

## IAM boundaries

Pinned `3.143.0` publishes OIDC issuer/discovery and registers per-consumer clients; removing the declaration or switching to SAML emits an empty back-channel URI to clear stale imported state, while actual notification remains restricted/pending acceptance. SAML publishes metadata, SSO, and the signing certificate without inventing SLO. Each `ALLOW_GROUPS` entry becomes a same-name Group/Role in the `anas` organization and an Approved Application Permission for the consumer, which Casdoor checks before issuing credentials. OIDC uses `JWT-Custom`/RS256 and places the permanent anchor in User ID (and therefore `sub`) while group claims use Role names. SAML maps the registered anchor to `$user.id` and groups to `$user.roles`; unknown sources are omitted. SAML NameID remains the username, so consumers must use the explicit anchor attribute for stable linking.

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
- [`server-casdoor-directory-authority-e2e.sh`](../../../test-env/scripts/server-casdoor-directory-authority-e2e.sh) (written; pending execution on a designated server)

## Current limitations

Lifecycle remains `developing`. The directory-subscription E2E now covers creates, existing-user profile refresh, burst debounce, and cursor recovery; deletion/deactivation propagation is still unaccepted. Real OIDC/SAML login, group authorization, permanent anchors, OIDC revocation, and recovery login also require E2E acceptance before production evaluation.
