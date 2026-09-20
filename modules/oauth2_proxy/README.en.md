# OAuth2 Proxy

OIDC ForwardAuth gate for services without their own login system.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `oauth2_proxy` |
| Version / revision | `7.15.3-r5` |
| Status | `release` |
| Category | `identity` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `iam` | Capability | `oidc` |
| `forward_auth` | Provides capability | `http` |

## Minimal configuration

```yaml
modules:
  oauth2_proxy: {}
```

This module also requires a deployment-level IAM provider, for example:

```yaml
identity:
  iam:
    provider: llng
```

## Identity, users, and groups

It stores no human users. It is an OIDC consumer of the selected IAM and admits only members of the
administrator group.

**Which groups pass is not a parameter.** Everything behind this gate is an administrative interface,
so the answer is fixed to the `platform_admin` role, which the hook resolves to the directory's real
administrator group name (`SAMBA_DC_ADMIN_GROUP_NAME`, falling back to the contract name `Admins`
when no directory module is deployed). Making it configurable would let one edit widen every service
behind the gate at once -- Adminer included -- with nothing to announce it. To open an entry to
non-administrators, extend the application catalogue's `audience` vocabulary instead of widening
this gate.

Pinned `7.15.3` guarantees only that `/oauth2/sign_out` clears the gateway cookie; the IAM cookie and any protected backend session are separate state. The module publishes no `OIDC_LOGOUT_*` fields and does not configure `backend-logout-url`, whose unbounded IAM request runs before cookie clearing. Local logout must therefore still succeed while IAM is down and the protected route must require authentication again, without claiming that the IAM or backend-business session ended.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | oidc |
| Group | `platform_admin` role (derived, not configurable) |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

### Directory attribute changes

**Matching key**: this Module **holds no persistent identity key of its own** — it is a stateless
authentication gateway with no human users, no accounts, and no database. It places the verified ID
Token `sub` verbatim into the `X-Anas-Identity-Subject` response header for the backend on every
request; whether and how that value is persisted is the backend's decision.

Every row below therefore has to be answered at three layers (`DIRKEY-R-011` forbids shrugging it off
as "the IAM handles it", and equally forbids projecting a gateway capability onto the backend): the
**IAM** (admission and sessions), the **gateway** (its cookie), and the **backend** (the application's
own state). The ANAS console is the only backend today, and its matching key is
`sha256(issuer ‖ sub)`, used solely as an internal principal id.

| Directory change | What the gateway and backend do | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | The gateway is stateless and unaffected. The console's principal id is derived from `issuer` and `sub`, so where the Provider's `sub` is stable a rename produces no new principal; with `llng`, whose `sub` is the login name, a rename turns the same person into a new principal and their job ownership lapses with it | how the principal id is composed: `verified`, entry `TestJobOwnerProxyIsLocallyBoundAndNeverRenews` in `internal/consoleauth/job_owner_test.go`; each Provider's `sub` shape: `inferred` |
| `mail` changes | Takes part in nothing. The gateway sets `--email-domain=*` and applies no domain restriction, and the console never reads email | `verified` (neither the Compose flags nor `internal/api/httpapi/proxy_authorizer.go` consumes email) |
| `displayName` and other profile attributes | Neither consumed nor stored | `verified` (same) |
| Direct or recursive group membership changes | **Re-decided on every ForwardAuth request**: the gateway checks that the ID Token's `groups`/`roles` contain the resolved administrator group, and the console then checks that `X-Anas-Identity-Group` equals the expected value. But the decision reads the **current ID Token**, whose refresh interval is set by the IAM's token TTL, not by directory events in real time | per-request group checking: `verified`, entries `modules/oauth2_proxy/oauth2_proxy/main_test.go` and `test-env/scripts/server-console-trusted-proxy-e2e.sh`; convergence latency following the token TTL: `inferred` |
| Account disabled | Once the ID Token expires no new one can be obtained and access is refused. **Within the token TTL the gateway cookie remains valid**; the console's proxy session has its own expiry, and neither is cut short by a directory disable | `inferred` |
| Account deleted | As above. Neither the gateway nor the console holds in-application assets — the console records job ownership by principal id, and once the original principal lapses those jobs can only be taken over by a new owner | `inferred` |
| Identifier recycled and reassigned | Depends on the Provider: where `sub` is an internal immutable id the newcomer gets a new principal (fail-closed); where `sub` is the recycled login name (`llng`) the newcomer inherits the old principal's job ownership (**fail-open**). In both cases the newcomer must still be a member of the administrator group to pass this gate | `inferred` |

**Fallback path** — what operations must do for every "no automatic path" row above:

1. To cut off an administrator immediately, **revoke their session on the IAM side and remove them
   from the administrator group**, then confirm their token TTL has elapsed; changing the directory
   alone does not take effect within the TTL;
2. After revoking, check the ANAS console for unfinished jobs owned by that principal and reassign
   them if needed;
3. This Module has no local recovery account, and protected services must never be exposed in order to
   revoke someone — restore the IAM instead.

## Administrator login and IAM-outage recovery

There is no local administrator or IAM-outage bypass account. Restore IAM rather than exposing protected services.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `oauth2_proxy.console_proxy_enabled` | bool | — | `false` | `static` | `OAUTH2_PROXY_CONSOLE_PROXY_ENABLED` | no | no | no | yes | `container_recreate` | Publish the OIDC- and mTLS-protected ANAS console route. |
| `oauth2_proxy.console_proxy_port` | int | `1..65535` | `8443` | `static` | `OAUTH2_PROXY_CONSOLE_PROXY_PORT` | no | no | no | yes | `container_recreate` | Trusted-proxy listener port exposed by anasd. |
| `oauth2_proxy.domain_prefix` | string | — | `auth-gate` | `static` | `OAUTH2_PROXY_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `oauth2_proxy.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `OAUTH2_PROXY_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

### Query and modify

```bash
anas config list oauth2_proxy -w /srv/anas
anas config explain oauth2_proxy.domain_prefix
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: oauth2-proxy receives TZ for process and log timestamps.
- Language status: `fixed`
- Supported languages (1): `en`
- Fallback: Built-in pages are English; protected applications manage their own language.

## ANAS console trusted proxy

With `oauth2_proxy.console_proxy_enabled: true`, the Hook publishes `https://anas.<base_domain>:<TRAEFIK_BASE_PORT>`, the existing `ANAS_FORWARD_AUTH_*` middleware, and the `ANAS_CONSOLE_MTLS` transport. Its backend is the host's `console_proxy_port`. The route is disabled by default so it cannot appear before `anasd` has a client CA, SPKI allowlist, and exact Traefik source IP.

oauth2-proxy itself listens only on container loopback port `4181`. The wrapper separately exposes browser/callback bridge `4180` and Traefik-ForwardAuth-only identity bridge `4182`. Both remove request-supplied ANAS identity headers. Only the identity bridge reads the fixed issuer, subject, `auth_time`, `exp`, and administrator group from oauth2-proxy's verified bearer ID-token response, then overwrites seven `X-Anas-Identity-*` headers. The raw assertion is never logged or written to the Secret Store. `anasd` is not an OIDC client and never receives an IdP password.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list oauth2_proxy -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

It controls the entry gate, not authorization inside the protected application.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
