# Module IAM and OIDC support

OIDC is the current ANAS default IAM integration protocol, but it applies only to modules that consume the `iam` capability and declare OIDC support. Modules without OIDC are never forced outside their manifests; they fall back to a supported protocol or retain their own authentication mechanism.

| Module | OIDC login | Current authentication path | Status |
| --- | --- | --- | --- |
| `netbird` | Yes | Direct IAM/OIDC consumer | Implemented |
| `oauth2_proxy` | Yes | Direct IAM/OIDC consumer and ForwardAuth provider | Implemented |
| `ddns_updater` | Indirect | `oauth2_proxy` through Traefik ForwardAuth | Implemented |
| `nextcloud` | Yes | Official `user_oidc` by default; LDAPS provisions users/groups; `user_saml` remains an explicit fallback | Implemented without creating a second user backend |
| `meshcentral` | Yes | IAM/OIDC authentication, LDAPS user/group synchronization, and OIDC group-to-access/site-admin mapping | Implemented |
| `lam` | No | LDAPS directory-management login | Not an IAM consumer |
| `authentik` | N/A | IAM provider with fixed `akadmin` break-glass account | Provides OIDC/SAML |
| `llng` | N/A | IAM provider | Provides OIDC/SAML |
| `ddns_go` | No | ANAS-managed local emergency account | No OIDC |
| `traefik` | No | ANAS-managed local BasicAuth emergency account | No OIDC |
| `collabora` | Indirect | Integrated through Nextcloud/WOPI without a standalone user login | Follows the Nextcloud session |
| `postgres`, `mariadb` | No | Database credentials/Adminer | Not an IAM login |
| `samba_dc`, `samba_fs` | No | AD/LDAP/Kerberos/SMB | Not an OIDC web consumer |
| `eturnal`, `freeradius`, `lego` | No | TURN/RADIUS/no interactive UI | Not applicable |

## Default resolution

The precedence is explicit module `iam_protocol`, then `identity.iam.default_protocol` when supported by the module, then the manifest preference. Nextcloud, MeshCentral, NetBird, and OAuth2 Proxy now choose OIDC by default. Nextcloud can be switched explicitly to SAML; MeshCentral declares OIDC only.

## Samba directory password integration

When an IAM provider writes user password changes back to Samba AD, the Samba
domain policy is authoritative. Provider-side validation and guidance are early
feedback, not a second, stricter password policy. Every provider integration must
document these capabilities separately instead of claiming only “password change
support”:

1. **Policy-value synchronization:** consume the expressible minimum length, complexity flag, history count, and minimum password age from `samba_dc`. Values the provider cannot enforce must be marked as guidance-only or unsupported.
2. **Pre-submit validation:** minimum length can be checked exactly. Complexity must use the AD three-of-five algorithm plus username and display-name checks; independent character-category counters are not an equivalent substitute. LDAP does not expose password history, so history and minimum age normally cannot be validated before submission.
3. **LDAP writeback:** a business user's new password must be written to Samba, not only to the provider's local credential. Samba remains the final authority for concurrent changes, configuration drift, and directory state unavailable to the provider.
4. **Error mapping:** show safe, actionable user guidance. LDAP `constraintViolation` (19) and `unwillingToPerform` (53) can only be classified reliably as a domain-policy rejection; they do not identify history, complexity, or name rules individually. Insufficient access and a missing user can be mapped separately. Raw LDAP diagnostics belong only in administrator logs or events.
5. **First-login password change:** state separately whether Samba's forced-change state is recognized and routed through the same writeback path. Ordinary password-change support does not imply this capability.

After changing the Samba password policy, run ANAS apply/reconcile so provider
guidance and locally checkable values are refreshed. A provider's own password
history must not be presented as Samba history: they maintain different state and
have different enforcement scope.

## Implementation gate

Marking a module as OIDC-implemented requires a manifest interface, provider client registration, redirect URI/scope/claim/group mapping, Secret delivery, application-state verification, and a real browser or HTTP login E2E. `server-authentik-oidc-login-e2e.sh` covers the complete authorization-code login, application sessions, directory identity, and administrator-group mapping for Nextcloud and MeshCentral. `server-authentik-password-policy-e2e.sh` and `server-llng-password-policy-e2e.sh` separately cover provider preflight, Samba's final decision, writeback, safe error mapping, and credential transition. Upstream OIDC or password-change support alone is not implementation evidence for ANAS.
