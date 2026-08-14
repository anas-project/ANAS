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

## Implementation gate

Marking a module as OIDC-implemented requires a manifest interface, provider client registration, redirect URI/scope/claim/group mapping, Secret delivery, application-state verification, and a real browser or HTTP login E2E. `server-authentik-oidc-login-e2e.sh` covers the complete authorization-code login, application sessions, directory identity, and administrator-group mapping for Nextcloud and MeshCentral. Upstream support alone is not implementation evidence for ANAS.
