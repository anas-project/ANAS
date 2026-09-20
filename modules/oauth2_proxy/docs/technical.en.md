# OAuth2 Proxy technical implementation

This page records the current implementation, security boundaries, and verification entry points for `oauth2_proxy`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `7.15.3-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `iam` | Capability | `oidc` |
| `forward_auth` | Provides capability | `http` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_oauth2-proxy` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-oauth2-proxy:7.15.3-r5` | `` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `oauth2_proxy.console_proxy_enabled` | bool | — | `false` | `static` | `OAUTH2_PROXY_CONSOLE_PROXY_ENABLED` | no | no | no | yes | `container_recreate` | Publish the OIDC- and mTLS-protected ANAS console route. |
| `oauth2_proxy.console_proxy_port` | int | `1..65535` | `8443` | `static` | `OAUTH2_PROXY_CONSOLE_PROXY_PORT` | no | no | no | yes | `container_recreate` | Trusted-proxy listener port exposed by anasd. |
| `oauth2_proxy.domain_prefix` | string | — | `auth-gate` | `static` | `OAUTH2_PROXY_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `oauth2_proxy.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `OAUTH2_PROXY_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

It stores no human users. It is an OIDC consumer of the selected IAM and admits only members of the
administrator group.

**Which groups pass is not a parameter.** Everything behind this gate is an administrative interface,
so the answer is fixed to the `platform_admin` role, which the hook resolves to the directory's real
administrator group name (`SAMBA_DC_ADMIN_GROUP_NAME`, falling back to the contract name `Admins`
when no directory module is deployed). Making it configurable would let one edit widen every service
behind the gate at once -- Adminer included -- with nothing to announce it. To open an entry to
non-administrators, extend the application catalogue's `audience` vocabulary instead of widening
this gate.

### Logout boundary

Pinned `7.15.3` limits `/oauth2/sign_out` to clearing the oauth2-proxy gateway cookie. The IAM cookie and protected backend business session are outside its revocation scope. The hook publishes no `OIDC_LOGOUT_*` fields and does not set `backend-logout-url`, whose unbounded IAM call happens before cookie clearing. The unified browser E2E pauses IAM and verifies that the local cookie is invalidated first and the protected service requires authentication again.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | oidc |
| Group | `platform_admin` role (derived, not configurable) |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

### Directory attribute changes — implementation

One-to-one with the README's *Directory attribute changes*.

- **Which table and field persist identity**: none on the gateway side. The bootstrap
  (`oauth2_proxy/main.go`) decodes `iss`, `sub`, `auth_time`, `exp`, and `groups`/`roles` from the ID
  Token oauth2-proxy returns, verifies them, and writes the fixed `X-Anas-Identity-*` response
  headers — **nothing is written to disk, cached, or mapped**. Persistence on the backend side lives
  in `internal/consoleauth`: the proxy session record stores `Issuer` and `Subject`
  (`internal/consoleauth/state.go`) and audit events store `IdentityIssuer`/`IdentitySubject`.
- **Matching key**: `sha256(issuer ‖ "\0" ‖ subject)` prefixed with `oidc:`, in `proxyPrincipal`
  (`internal/api/httpapi/proxy_authorizer.go`) and `internal/consoleauth/job_owner.go`. Taking a
  digest rather than the raw value keeps the principal id and the job-ownership key free of any
  recognizable directory label.
- **Re-decided on every request**: `iss` must equal the expected issuer; `sub` must be non-empty and
  free of separator characters; the semantic role must be `platform_admin`; the directory group must
  equal the expected group; and `exp` must be later than both `now` and `auth_time`. Any failure
  yields `ErrUnauthenticated`. This is not a decision taken once at login — every ForwardAuth request
  is judged.
- **Which interface performs revocation**: none on the gateway side. Revocation happens only on the
  IAM (end the session, remove from the administrator group), and it takes effect after the ID
  Token's remaining TTL. `/oauth2/sign_out` clears the gateway cookie only and ends neither the IAM
  nor the backend session.
- **Reconciliation or event-subscription path**: none, and none is needed — the gateway is stateless
  and the console's proxy session has its own expiry.
- **Where there is no automatic path, the technical obstacle**: `backend-logout-url` is **deliberately
  left unconfigured**. In the pinned `7.15.3` that option sends an untimed request to the IAM before
  clearing the local cookie, so local logout hangs when the IAM is down; "the cookie still clears when
  the IAM is stopped" was traded for "logout also notifies the backend". That is a trade-off, not a
  missing capability.

**`DIRKEY-R-013` projection verdict: unaffected (`verified`).** The subject identifier appears in
exactly two places along this path: the `X-Anas-Identity-Subject` HTTP response header (passed between
processes, not a user-visible URL), and the console's proxy session record and audit events (an
internal binding field and a management view, which `DIRKEY-R-010` explicitly permits). **The
principal id and the job-ownership key take a digest rather than the raw value**, so even once the
subject identifier becomes the anchor, no UUID appears in any id, username, or URL path. Entry points:
`TestJobOwnerProxyIsLocallyBoundAndNeverRenews` in `internal/consoleauth/job_owner_test.go` builds the
expected actor as `"oidc:" + hex(sha256(issuer ‖ subject))` and asserts the match;
`modules/oauth2_proxy/oauth2_proxy/main_test.go` asserts that a forged `X-Anas-Identity-Subject`
request header is stripped and cannot be spoofed.

This Module is therefore **not a blocker for M2**: the projection `DIRKEY-R-013` asks each Consumer to
verify is already excluded here by the digest design.

## Management surfaces and secret lifecycle

There is no local administrator or IAM-outage bypass account. Restore IAM rather than exposing protected services.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `OAUTH2_PROXY_CLIENT_SECRET`
- `OAUTH2_PROXY_COOKIE_SECRET`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `ANAS_FORWARD_AUTH_*`
- `ANAS_PROXY_PLATFORM_ADMIN_GROUP`
- `ANAS_TRAEFIK_ROUTE__ANAS_CONSOLE__*`
- `ANAS_TRAEFIK_SERVERS_TRANSPORT__ANAS_CONSOLE_MTLS__*`
- `ANAS_CONSOLE_PROXY_PUBLIC_URL`
- `ANAS_IAM_CLIENT__OAUTH2_PROXY__*`

### Explicit consumes

- `ANAS_TLS_TRUST_BUNDLE_NAME`
- `TRAEFIK_BASE_PORT`
- `ANAS_IAM_BINDING_*`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- [`main_test.go`](../oauth2_proxy/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Console identity bridge and real client IP

oauth2-proxy binds loopback port `4181`; the wrapper uses `4180` for ordinary browser/callback traffic and `4182` for Traefik ForwardAuth. Both bridges first delete caller-supplied fixed identity headers and response Authorization. Only `4182`, after a 2xx upstream result, parses oauth2-proxy's verified ID token and requires the issuer, stable subject, numeric `auth_time`/`exp`, and resolved `platform_admin` directory group before overwriting seven fixed headers. The raw bearer assertion exists only in the request chain to bind sessions and step-up; it is neither persisted nor logged.

The ANAS wrapper image resolves Traefik at startup, appends its exact `/32` as `--trusted-proxy-ip`, and validates optional upstream proxy IPs or CIDRs. It no longer trusts all three RFC1918 ranges. Resolution or validation failure keeps the gate closed, preventing forged forwarded headers from changing redirects or authentication context.

## Current limitations

It controls the entry gate, not authorization inside the protected application.
