# Module IAM and OIDC support

OIDC is the current ANAS default IAM integration protocol, but it applies only to modules that consume the `iam` capability and declare OIDC support. Modules without OIDC are never forced outside their manifests; they fall back to a supported protocol or retain their own authentication mechanism.

| Module | OIDC login | Current authentication path | Status |
| --- | --- | --- | --- |
| `netbird` | Yes | Direct IAM/OIDC consumer | Implemented |
| `oauth2_proxy` | Yes | Direct IAM/OIDC consumer and ForwardAuth provider | Implemented |
| `ddns_updater` | Indirect | `oauth2_proxy` through Traefik ForwardAuth | Implemented |
| `nextcloud` | Yes | Official `user_oidc` by default; LDAPS provisions users/groups; `user_saml` remains an explicit fallback | Implemented; exact logout capability is decided by the pinned provider/version matrix below |
| `meshcentral` | Yes | IAM/OIDC authentication, LDAPS user/group synchronization, and OIDC group-to-access/site-admin mapping | Implemented |
| `vikunja` | Yes | IAM/OIDC JIT account creation; `APP_vikunja`/`APP_all` access gate; local authentication and registration disabled | Developing: manifest, provider registration, secrets, hook, and application configuration are implemented; real browser/database E2E remains pending |
| `lam` | No | LDAPS directory-management login | Not an IAM consumer |
| `authentik` | N/A | IAM provider with fixed `akadmin` break-glass account | Provides OIDC/SAML |
| `casdoor` | N/A | Developing IAM provider with default-template `admin_casdoor` break-glass account | Provides OIDC/SAML and subscribes to Samba directory events; SAML SLO, permanent anchors, and `ALLOW_GROUPS` remain unaccepted |
| `llng` | N/A | IAM provider | Provides OIDC/SAML |
| `ddns_go` | No | ANAS-managed local emergency account | No OIDC |
| `traefik` | No | ANAS-managed local BasicAuth emergency account | No OIDC |
| `collabora` | Indirect | Integrated through Nextcloud/WOPI without a standalone user login | Follows the Nextcloud session |
| `postgres`, `mariadb` | No | Database credentials/Adminer | Not an IAM login |
| `samba_dc`, `samba_fs` | No | AD/LDAP/Kerberos/SMB | Not an OIDC web consumer |
| `eturnal`, `freeradius`, `lego` | No | TURN/RADIUS/no interactive UI | Not applicable |

## Pinned-version logout matrix

“Passed” applies only to the pinned provider/consumer and scenario shown; a restricted result is never rolled up into bidirectional logout. The unified browser entry point is `test-env/scripts/server-iam-logout-matrix-e2e.sh`, with sanitized JSON in `test-env/reports/iam-logout-*.json`.

| Pinned consumer | Endpoint / binding | Module→IAM | IAM→Module | Session granularity | Browser | Failure/degradation result | Acceptance |
| --- | --- | --- | --- | --- | --- | --- | --- |
| Nextcloud `user_oidc 8.10.1` | RP logout + `/index.php/apps/user_oidc/backchannel-logout/anas` | RP-Initiated Logout | `sid` back-channel | One OIDC session; multiple sessions/clients are mandatory security-matrix cases | RP logout yes; back-channel no | Local session must fail first while IAM is unavailable; missing provider notification is restricted | Existing Authentik browser/admin-delete and LLNG browser E2Es; remaining administrative cases, isolation security matrix, and Casdoor notification are restricted pending the unified matrix |
| Nextcloud `user_saml 8.2.0` | `/index.php/apps/user_saml/saml/sls`, Redirect | SP-Initiated SLO | IdP-Initiated SLO | NameID + SessionIndex | Required | Local logout only when no SLO is published | Existing Authentik Redirect E2E; LLNG Redirect entry implemented pending isolated fixture; Casdoor explicitly has no SLO |
| MeshCentral `1.2.4` | discovery/provider RP logout + post-logout URI | Upstream support | No standard receiver | Application cookie/session | Required | Local session first; IAM outage must not block local logout | Unified Playwright case implemented; `state` and central-session outcome are unaccepted on the current host, so status is “upstream support, integration pending” |
| Vikunja `2.4.0` | discovery `end_session_endpoint` + `id_token_hint` + post-logout URI | Upstream support | No standard receiver | Vikunja server-side session | Required | Upstream deletes the local session first; failure to build the provider logout URL does not block local logout | Hook/container-entrypoint unit tests and pinned upstream-source review complete; real browser E2E remains pending |
| NetBird Dashboard `2.90.9` | discovery `end_session_endpoint` + post-logout URI | Upstream support | No standard receiver | Dashboard local authentication state | Required | Local state first; no IAM→Module claim without notification | Unified Playwright case implemented; `state` and central-session outcome are unaccepted on the current host, so status is “upstream support, integration pending” |
| oauth2-proxy `7.15.3` | `/oauth2/sign_out` | Gateway cookie only | None | oauth2-proxy cookie, excluding IAM/backend sessions | No | IAM-down sign-out still clears the cookie and the protected service reauthenticates | Publishes no `OIDC_LOGOUT_*` and configures no `backend-logout-url`; IAM-down Playwright case implemented pending isolated fixture |

Providers are pinned to Authentik `2026.5.6`, LLNG `2.23.2`, and Casdoor `3.143.0`. Both SAML HTTP-POST and Redirect are browser bindings; the word `post` never implies browserless revocation. Casdoor registers only explicitly declared OIDC back-channel URIs, clears old values when declarations disappear or protocols switch, and publishes no unverified SAML SLO.

## Default resolution

The precedence is explicit module `iam_protocol`, then `identity.iam.default_protocol` when supported by the module, then the manifest preference. Nextcloud, MeshCentral, Vikunja, NetBird, and OAuth2 Proxy now choose OIDC by default. Nextcloud can be switched explicitly to SAML; MeshCentral and Vikunja declare OIDC only.

## Samba directory password integration

When an IAM provider writes user password changes back to Samba AD, the Samba
domain policy is authoritative. Provider-side validation and guidance are early
feedback, not a second, stricter password policy. Every provider integration must
document these capabilities separately instead of claiming only “password change
support”:

1. **Policy-value synchronization:** consume the expressible minimum length, complexity flag, history count, and minimum password age from `samba_dc`. Values the provider cannot enforce must be marked as guidance-only or unsupported.
2. **Pre-submit validation:** minimum length can be checked exactly. Complexity must use the AD three-of-five algorithm plus username and display-name checks; independent character-category counters are not an equivalent substitute. LDAP does not expose password history, so history and minimum age normally cannot be validated before submission.
3. **LDAP writeback:** a business user's new password must be written to Samba, not only to the provider's local credential. Distinguish a user change authenticated with the old password from a delegated service-account reset: the latter can bypass Samba history and minimum-age enforcement, so the provider must document that boundary and any compensating control instead of claiming that Samba decided every rule.
4. **Error mapping:** show safe, actionable user guidance. LDAP `constraintViolation` (19) and `unwillingToPerform` (53) can only be classified reliably as a domain-policy rejection; they do not identify history, complexity, or name rules individually. Insufficient access and a missing user can be mapped separately. Raw LDAP diagnostics belong only in administrator logs or events.
5. **First-login password change:** state separately whether Samba's forced-change state is recognized and routed through the same writeback path. Ordinary password-change support does not imply this capability.

After changing the Samba password policy, run ANAS apply/reconcile so provider
guidance and locally checkable values are refreshed. When delegated LDAP reset
semantics prevent Samba from enforcing history, a provider history synchronized
to the same depth may be used as a compensating control, but documentation and
tests must make clear that the two stores have different state and scope.

## Implementation gate

Local logout, Module-initiated logout, browser-mediated bidirectional logout,
and browserless bidirectional logout are evaluated separately under the
[OIDC/SAML Module bidirectional logout requirements](/requirements/module-iam-bidirectional-logout).
“OIDC implemented” in this table does not automatically mean that
bidirectional logout or administrative revocation is implemented.

Marking a module as OIDC-implemented requires a manifest interface, provider client registration, redirect URI/scope/claim/group mapping, Secret delivery, application-state verification, and a real browser or HTTP login E2E. `server-authentik-oidc-login-e2e.sh` covers the complete authorization-code login, application sessions, directory identity, and administrator-group mapping for Nextcloud and MeshCentral. `server-authentik-password-policy-e2e.sh` and `server-llng-password-policy-e2e.sh` separately cover provider preflight, Samba's final decision, writeback, safe error mapping, and credential transition. Upstream OIDC or password-change support alone is not implementation evidence for ANAS.

The two OIDC matrices retain the original Nextcloud cookie, verify IAM-initiated logout, and reject silent recovery: Authentik covers browser logout and administrative session deletion, while LLNG covers browser logout. The Authentik SAML fallback E2E covers Redirect SLO. SAML POST is also browser-mediated, so headless SAML revocation remains outside the support contract.
