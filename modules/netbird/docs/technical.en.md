# NetBird technical implementation

This page records the current implementation, security boundaries, and verification entry points for `netbird`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `0.76.1-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `iam` | Capability | `oidc` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_dashboard` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-netbird-dashboard:2.90.9` | `traefik` | 0 |
| `anas_management` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-netbird-management:0.76.1-r5` | `traefik` | 2 |
| `anas_relay` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-netbird-relay:0.76.1` | `traefik` | 0 |
| `anas_signal` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-netbird-signal:0.76.1` | `traefik` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `netbird.domain_prefix` | string | — | `netbird` | `static` | `NETBIRD_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `netbird.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `NETBIRD_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | The OIDC issuer and client configuration change together. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

It declares an OIDC consumer and application group, but administrator-role mapping remains a release blocker.

### Logout boundary

Pinned Dashboard `2.90.9` drives RP logout from the discovery endpoint. The unified browser matrix must verify `state`, Dashboard-local session invalidation first, central IAM-session termination, and failure of silent recovery; until then its status is “upstream support, integration pending.” This version exposes no standard front/back-channel receiver, so IAM-to-NetBird logout is not declared.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | oidc |
| Group | `APP_netbird` / `APP_all` |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

### Directory attribute changes — implementation

One-to-one with the README's *Directory attribute changes*.

- **Which table and field persist identity**: NetBird management's own data store, where the user id
  is the ID Token's `sub`. The Module keeps no directory replica and no mapping table.
- **How the matching key is configured**: `hook/main.go` renders
  `NETBIRD_AUTH_USER_ID_CLAIM = "sub"` and `management.json.envsubst` substitutes it into
  `AuthUserIDClaim`. **That field is configurable** — upstream accepts any claim name, which is the
  key difference between NetBird and Forgejo or Vikunja: it has no "missing configurable identity
  field" gap in the sense of `DIRKEY-R-004`.
- **Refreshed at each login**: not re-checked. The Module registers the `name`, `cn`,
  `sAMAccountName`, and `email` claims, but whether upstream overwrites an existing user row with them
  has not been verified on the pinned version.
- **Which interface performs revocation**: only the Provider's admission decision. The pinned
  Dashboard `2.90.9` has no IAM→Module notification endpoint; peers and setup keys are managed by the
  NetBird management API and can only be revoked explicitly through the management interface or that
  API.
- **Reconciliation or event-subscription path**: none. The Module keeps no directory replica and falls
  outside the directory event subscription requirement.
- **Where there is no automatic path, the technical obstacle**: a **missing receiver**, not a missing
  immutable id. Upstream has no OIDC back-channel logout endpoint and no interface that disables peers
  in bulk from directory state. Peer credentials are long-lived credentials NetBird issues itself and
  by design never pass through an interactive login, so no "converges at the next sign-in" mechanism
  applies to them.

**`DIRKEY-R-013` projection verdict: ordinary-user URL projection (pinned source verified
2026-10-03).** Version `0.76.1` copies `AuthUserIDClaim` into `UserAuth.UserId` in
`shared/auth/jwt/extractor.go`. `management/server/http/handlers/users/pat_handler.go` registers
`/users/{userId}/tokens`, and `GetAllPATs` in `management/server/user.go` permits a user to read
their own personal access tokens. This cannot be exempted as an administrator-only view.
The Casdoor r10 hook rejects Netbird registration until the consumer projection is corrected.
Using a username, another stable claim or a derived id violates the unique-anchor binding rule.
This is source call-path evidence, not Netbird UI E2E acceptance.

## Management surfaces and secret lifecycle

There is no supported private recovery administrator or documented IAM-bypass entry.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET`
- `NETBIRD_DATASTORE_ENC_KEY`
- `NETBIRD_RELAY_AUTH_SECRET`
- `TURN_SECRET`

`credentials.consumes` explicitly binds `TURN_SECRET` to `eturnal.secret`.
The declaration is frozen into the deployment and creates an
Eturnal-to-NetBird activation edge. NetBird consumes separate candidate and
previous projections; it neither owns the credential nor implements its
reconcile handler.

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `ANAS_IAM_CLIENT__NETBIRD__*`
- `APPS_LIST*`

### Explicit consumes

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `NETBIRD_SIGNAL_PORT`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_APP_FILTER`
- `TRAEFIK_BASE_PORT`
- `TRAEFIK_HOSTNAME`
- `TURN_DOMAIN_PORT`
- `ANAS_IAM_BINDING__NETBIRD__*`
- `TURN_SECRET`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- Eturnal's credential ready barrier completes before this Module starts;
  NetBird is not started when owner verification fails.
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Real client IP

Management startup resolves Traefik and writes its exact `/32` plus explicit upstream proxies to `ReverseProxy.TrustedHTTPProxies`. Resolution failure prevents startup; the module neither relies on positional `TrustedHTTPProxiesCount` inference nor trusts the whole Docker private network. Dashboard, Signal, and Relay do not consume access IPs, so Traefik's JSON access log is the boundary record.

## Current limitations

Status is `developing` and it is excluded from recommended deployments.
