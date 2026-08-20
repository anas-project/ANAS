# Casdoor technical implementation

This document records the protocol contract, security boundaries, and verification points for maintainers.

<!-- generated:module-identity:start -->
> Status: current implementation; based on `3.143.0-r1` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_casdoor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r1` | `traefik, db, casdoor` | 5 |
| `anas_casdoor_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-casdoor:3.143.0-r1` | `casdoor` | 2 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `casdoor.db_name` | string | — | `casdoor` | `static` | `CASDOOR_DB_NAME` | no | no | no | yes | `container_recreate` | Casdoor database name |
| `casdoor.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `CASDOOR_DB_TYPE` | no | no | no | no: `migrate-casdoor-database` | `data_migrate` | Relational database interface or automatic selection |
| `casdoor.domain_prefix` | string | — | `auth` | `static` | `CASDOOR_DOMAIN_PREFIX` | no | no | no | yes | `reconcile` | Service domain prefix and all IAM endpoints |
| `casdoor.ldap_auto_sync_minutes` | int | `>= 1` | `5` | `static` | `CASDOOR_LDAP_AUTO_SYNC_MINUTES` | no | no | no | yes | `container_recreate` | LDAP automatic synchronization interval in minutes |

## Data and startup flow

The hook renders `app.conf` and an init-data template. At startup the helper reads the projected recovery password and replaces it with bcrypt in `/tmp/init_data.json`. Because upstream initializes LDAP auto-synchronizers before importing init data, the entrypoint briefly starts Casdoor to commit tables and managed objects, then starts the long-running process as UID/GID 1000. This makes the committed LDAP record visible to the synchronizer on first deployment and after configuration changes. PostgreSQL is the only supported database interface.

## LDAP, directory events, and authority boundary

The LDAP connection uses trusted LDAPS and a filter that excludes disabled accounts and requires the Samba anchor attribute. `anas_casdoor_dirwatch` follows `ANAS_DIRECTORY_EVENTS_DIR` read-only with its own durable cursor, filtering and debouncing changes before it calls the local Casdoor `get-ldap-users` and `sync-ldap-users` APIs with this module's managed Application credential. The cursor is committed only after a successful sync; failures retry. Casdoor's default five-minute automatic sync remains enabled, so the subscriber is a low-latency accelerator.

The integration imports users and verifies passwords remotely but does not enable password writeback. Casdoor retains local shadow records, so deletion/deactivation propagation and anchor stability still require real synchronization E2E.

## IAM boundaries

OIDC publishes issuer/discovery and registers per-consumer clients. SAML publishes metadata, SSO, and the signing certificate without inventing SLO. `ALLOW_GROUPS` is not enforced, and unknown SAML attributes fall back to `$user.id`, which is not a verified Samba permanent anchor.

## Local administrator lifecycle

`admin_casdoor` is managed by the local-account inventory through the default `admin_{module}` template; Casdoor does not need `fixed_username`. Apply/rotate handlers stream the candidate through stdin, update bcrypt in PostgreSQL, verify the stored hash, and restore the old password if rotation fails.

## Environment ownership

The module exports `ANAS_IAM_BINDING_*` and `ANAS_IAM_PORTAL_URL`, and explicitly consumes TLS, Samba LDAPS, `ANAS_DIRECTORY_EVENTS_*`, IAM consumer registrations, and the application catalog. The sensitive bind password and the Casdoor Application secret used by the subscriber stay inside this module; the Samba producer holds no Casdoor credential.

## Tests and implementation

- [`iam_test.go`](../hook/iam_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`helper/main_test.go`](../casdoor/helper/main_test.go)
- [`helper/directory_watch_test.go`](../casdoor/helper/directory_watch_test.go)
- [`server-casdoor-directory-events-e2e.sh`](../../../test-env/scripts/server-casdoor-directory-events-e2e.sh) (written, not run)

## Current limitations

Lifecycle remains `developing`. The directory-subscription E2E has been written but intentionally not run yet. That script plus real OIDC/SAML login, directory synchronization and deactivation, group authorization, permanent anchors, OIDC revocation, and recovery login require E2E acceptance before production evaluation.
