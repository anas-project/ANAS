# User portal implementation

<!-- generated:module-identity:start -->
> Status: current implementation; based on `0.1.0-r1` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Dependencies, capabilities and contracts

Native OIDC binding supplies the issuer; Casdoor self APIs supply current IAM facts; Samba AD supplies current users, groups and passwords. Casdoor only in this version; no SAML or database.

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_user_portal` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-user-portal:0.1.0-r1` | `` | 3 |
<!-- generated:compose-topology:end -->

One Go process embeds UI/API. Traefik is the only ingress; no host ports or Docker socket. Root bootstrap initializes only the fixed audit mount through directory descriptors, then irreversibly switches to UID/GID 65532. Application rootfs is read-only.

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `user_portal.audit_capacity_mb` | int | `1..1024` | `64` | `static` | `USER_PORTAL_AUDIT_CAPACITY_MB` | no | no | no | yes | `container_recreate` | Private audit capacity; full storage closes writes |
| `user_portal.delegation_rules` | string | — | `[]` | `static` | `USER_PORTAL_DELEGATION_RULES` | no | no | no | yes | `container_recreate` | Complete delegation rules as JSON; disabled by default |
| `user_portal.domain_prefix` | string | — | `portal` | `static` | `USER_PORTAL_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Portal hostname prefix |
| `user_portal.group_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_DN` | no | no | no | yes | `container_recreate` | Dedicated single-member write bind DN |
| `user_portal.group_bind_password` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_PASSWORD` | no | no | yes | yes | `container_recreate` | Dedicated member bind credential; backend only |
| `user_portal.iam_protocol` | enum (`oidc`) | — | `oidc` | `static` | `USER_PORTAL_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | Fixed OIDC login |
| `user_portal.language` | string | — | — | `inherited` | `USER_PORTAL_LANGUAGE` | no | yes | no | yes | `container_recreate` | Default language; unsupported selections fall back to English |
| `user_portal.password_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_DN` | no | no | no | yes | `container_recreate` | Dedicated password/unlock bind DN |
| `user_portal.password_bind_password` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_PASSWORD` | no | no | yes | yes | `container_recreate` | Dedicated password bind credential; backend only |
| `user_portal.protected_groups` | string | — | `[]` | `static` | `USER_PORTAL_PROTECTED_GROUPS` | no | no | no | yes | `container_recreate` | Additional protected group anchors as JSON |
| `user_portal.protected_users` | string | — | `[]` | `static` | `USER_PORTAL_PROTECTED_USERS` | no | no | no | yes | `container_recreate` | Additional protected user anchors as JSON |
| `user_portal.recent_window_seconds` | int | `5..900` | `300` | `static` | `USER_PORTAL_RECENT_WINDOW_SECONDS` | no | no | no | yes | `container_recreate` | Recent authentication window; default 300 seconds |

module.yml is authoritative. Delegation defaults to disabled; directory privileges are never widened. Explicit runtime environment excludes application API secrets and IAM registration variables.

Source builds reuse the repository's `DOCKER_HUB_REGISTRY`, `GOPROXY_URL` and `CHINESE_BUILD_SPEEDUP`/`APK_MIRROR_URL`. `GO_MODULE_PROXY` can override the Go proxy separately; official endpoints remain the defaults. Download acceleration does not disable TLS verification.

## Hook and IAM

The hook publishes only its own USER_PORTAL client registration. PUBLIC_CLIENT registers PKCE without delivering an application API secret to the portal. Native import may generate a provider-side secret that the portal does not consume. REAUTHENTICATION_CLIENT_ID/REAUTHENTICATION_REDIRECT_URI request a separate native application with automatic/session sign-in disabled. Signed auth_time is still checked; max_age/prompt alone are insufficient. No new Casdoor upstream patch is added.

## Data flow and security

Memory sessions and one-use state/nonce/verifier have capacity limits and TTL. Cookies are Secure, HttpOnly, SameSite=Lax and host-only. Writes validate Origin/CSRF; only the exact Traefik peer reaches business APIs. LDAP verifies CA/server name and never falls back.

The directory reuses appcatalog language/BuildView. Policies must be enabled, Approved, Allow and match model/resource/Read/group sets, intersected with current AD groups. Native default allow/admin bypass is not used. Icon authorization precedes the no-link reader and digest check.

LDAP actions are fixed password change/reset, pwdLastSet, lockoutTime and one member add/delete. Anchor resolution yields a current GUID; no arbitrary attribute/DN, full-member replacement or command endpoint exists. Audit has no password/token field; intent fsync precedes LDAP, unknown network results never replay, and result-storage failure closes writes.

## Storage, backup and upgrades

The private audit.jsonl belongs to workspace data and requires data backup. Sessions are memory-only. Pending intents appear as unknown; corruption closes writes and preserves the original file. A single process holds the file lock, without database/cache/sync services.

## Verification

`go test ./modules/user_portal/portal ./modules/user_portal/hook` covers OIDC identity/time binding, policy/AD denial, CSRF, audit failures and no replay. Casdoor hooks/appcatalog are checked together. Server/browser entrypoints, fixed artifacts and per-ID outcomes belong in the paired plan. Unexecuted criteria remain unaccepted.

## Current limits

The formal Dockerfile build passed 95 runtime checks and all 41 first-version criteria on finance (Linux/amd64). Multi-object transactions are outside the supported capabilities; broader restore, multi-architecture publishing and other logout directions remain in their respective plans. Temporary reset passwords are returned only in the successful response. External application session status is not guessed.

Locked accounts or unconfirmed password state cannot use ordinary portal access. Missing current IAM state fields also close access. Audit scope is filtered from one current directory snapshot.

Pre-commit review fixed recovery identity-limit disclosure and missing actor attribution for invalid member requests. Local regressions pass; the historical 95/95 and 41/41 results apply to the earlier image. Rebuild and revalidate the current release image on finance before closing acceptance.

The binding declares `ANAS_IAM_BINDING__USER_PORTAL__OIDC_SELF_ACCOUNT_API_SCHEMA=casdoor-account/v1`. The portal checks this API contract rather than branching on IAM module names; it continues using existing native self-account APIs. The portal has a `no_prior_release` inventory baseline, with no earlier-version upgrade result claimed.

Go dependencies use coreos/go-oidc for OIDC and signed logout verification, go-ldap for bounded directory requests, and x/net/publicsuffix for same-site origin checks. The shared catalog uses x/image to generate seven PNG sizes. These libraries add no runtime service.
