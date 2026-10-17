# ANAS User Portal

An independent application directory, personal account and delegated LDAP administration portal. This is a developing candidate; complete first-version acceptance has not passed.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `user_portal` |
| Version / revision | `0.1.0-r1` |
| Status | `developing` |
| Category | `identity` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Dependencies, capabilities and contracts

Requires Traefik, Samba AD and Casdoor. OIDC only; native self-account APIs currently restrict this version to Casdoor. No database.

## Minimal configuration

```yaml
modules:
  user_portal: {}
```

The default URL is `https://portal.<base_domain>`. Portal and IAM must share an HTTPS site. The registered ANAS administrator role is enabled by default; directory writes also require dedicated bindings and ACLs. Explicit delegation rules grant additional scoped roles.

## Identity and user management

Authorization Code + S256 PKCE. Signature, issuer, audience, nonce, request binding and expiry are verified on the server. Only a protected opaque cookie reaches the browser. The immutable anchor in sub binds identity; names, email and DNs do not.

The user's own token reads native UserInfo/get-account. No application API secret is delivered to the portal process. Cards, search and icons require current IAM/AD state and an explicit fixed positive-group policy. Old-password change uses LDAP delete/add and never falls back to reset. ANAS administrator writes require dedicated restricted binds and recent authentication. System operations also require personal domain rights and per-operation verification. Additional delegated roles still require a complete rule.

Sign-out clears the portal session before the browser calls the discovery logout endpoint without any token. Failures remain unconfirmed and can be retried. `/oidc/backchannel-logout` verifies native logout tokens. Bidirectional logout and administrator revocation are not declared accepted until the paired plan has real-session E2E evidence.

| Directory change | Implementation and evidence |
| --- | --- |
| sAMAccountName | Anchor retains identity; next account read uses the new name. |
| mail | Actual AD value, never synthesized or used as a binding key. |
| displayName/profile | Read on each account API call, no identity replica. |
| Direct/recursive groups | Current AD membership and current IAM policy. |
| Disable | Reject current disabled state and invalidate the portal session. |
| Delete | Missing anchor denies access; no portal user assets. |
| Identifier reuse | Names/DNs cannot replace an anchor; GUID locates writes. |

Fallback: repair directory/IAM and sign in again; dependency failures deny access. Other application tokens, SSH keys and assets are not implicitly revoked.


### Profiles and ANAS administration

Users may edit given name, surname and display name. Email, login name and anchors remain read-only; no alias writes exist before mail integration. Profiles recheck exact field values immediately before writing and serialize portal profile writes, followed by one old-value LDAP Modify. This does not provide a global transaction with external directory tools. The application category switch persists per browser, while form outcomes are visible next to the operation. Unsupported languages select English.

`administration_enabled` binds the directory provider's registered ANAS administrator role to its stable identity, not a Casdoor native administrator flag or a name prefix. Registered groups are categorized by purpose; system groups require the advanced risk view. Self password reset, self removal from administrator paths and removal of the last confirmed available administrator are rejected. Computers, service and trust accounts are excluded.

Before enabling writes, an operator runs `tools/provision-directory.py` inside the selected Samba DC, supplying private JSON on stdin with confirmation `provision-user-portal-scoped-writers`, the actual base_dn and separate random profile/reset/group passwords. It creates `svc_portal_profile`, `svc_portal_reset` and `svc_portal_group` and adds attribute/object ACLs, without domain roles or resetting existing service passwords. Configure the resulting DNs/passwords in the portal and redeploy. Keep input under a 0700 directory in a 0600 file, never in argv, Git or logs. This is an operator tool, not a portal endpoint.

The profile writer only writes the three name fields. Reset covers ordinary personnel's reset/pwdLastSet/lockoutTime; member writes cover registered, non-domain groups. Existing domain-level personnel receive explicit reset denials. Privilege changes or new privileged users made outside the portal require the operator to recheck ACLs. Object ACLs do not constrain member values and cannot replace current portal authorization. A non-domain service account credential still has sensitive delegated rights.

`domain_administration_enabled` defaults to false. Protected personnel/system groups require recent OIDC, an expiring one-use parameter confirmation and the signed-in person's own per-request LDAPS bind and native AD rights. That password is not stored across requests. This is not an MFA or process isolation claim, and the ordinary portal holds no permanent domain-wide writer credential. AD remains authoritative for custom ACL permissions.

## Administrator login and IAM recovery

No local administrator or IAM bypass exists. Use Casdoor's own recovery procedure; the portal permits logout retries and no degraded authorization.

## Management commands

Use `anas config list user_portal --json`, `anas config set`, `anas plan` and `anas apply`. No arbitrary LDAP, DN or command execution endpoint.

## Database support

No database consumer/provider. Samba AD owns users, groups and passwords.

## All configuration parameters

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `user_portal.administration_enabled` | bool | — | `true` | `static` | `USER_PORTAL_ADMINISTRATION_ENABLED` | no | no | no | yes | `container_recreate` | Enable the registered ANAS administrator role; writes still need scoped bindings and ACLs |
| `user_portal.audit_capacity_mb` | int | `1..1024` | `64` | `static` | `USER_PORTAL_AUDIT_CAPACITY_MB` | no | no | no | yes | `container_recreate` | Private audit capacity; full storage closes writes |
| `user_portal.delegation_rules` | string | — | `[]` | `static` | `USER_PORTAL_DELEGATION_RULES` | no | no | no | yes | `container_recreate` | Complete delegation rules as JSON; disabled by default |
| `user_portal.domain_administration_enabled` | bool | — | `false` | `static` | `USER_PORTAL_DOMAIN_ADMINISTRATION_ENABLED` | no | no | no | yes | `container_recreate` | Opt in to per-operation personal AD credentials and native domain rights; disabled by default |
| `user_portal.domain_prefix` | string | — | `portal` | `static` | `USER_PORTAL_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Portal hostname prefix |
| `user_portal.group_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_DN` | no | no | no | yes | `container_recreate` | Dedicated single-member write bind DN |
| `user_portal.group_bind_password` | string | — | `""` | `static` | `USER_PORTAL_GROUP_BIND_PASSWORD` | no | no | yes | yes | `container_recreate` | Dedicated member bind credential; backend only |
| `user_portal.iam_protocol` | enum (`oidc`) | — | `oidc` | `static` | `USER_PORTAL_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | Fixed OIDC login |
| `user_portal.language` | string | — | — | `inherited` | `USER_PORTAL_LANGUAGE` | no | yes | no | yes | `container_recreate` | Default language; unsupported selections fall back to English |
| `user_portal.password_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_DN` | no | no | no | yes | `container_recreate` | Dedicated password/unlock bind DN |
| `user_portal.password_bind_password` | string | — | `""` | `static` | `USER_PORTAL_PASSWORD_BIND_PASSWORD` | no | no | yes | yes | `container_recreate` | Dedicated password bind credential; backend only |
| `user_portal.profile_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_PROFILE_BIND_DN` | no | no | no | yes | `container_recreate` | Dedicated writer DN restricted to givenName, sn and displayName |
| `user_portal.profile_bind_password` | string | — | `""` | `static` | `USER_PORTAL_PROFILE_BIND_PASSWORD` | no | no | yes | yes | `container_recreate` | Private profile writer credential; never sent to the browser |
| `user_portal.protected_groups` | string | — | `[]` | `static` | `USER_PORTAL_PROTECTED_GROUPS` | no | no | no | yes | `container_recreate` | Additional protected group anchors as JSON |
| `user_portal.protected_users` | string | — | `[]` | `static` | `USER_PORTAL_PROTECTED_USERS` | no | no | no | yes | `container_recreate` | Additional protected user anchors as JSON |
| `user_portal.recent_window_seconds` | int | `5..900` | `300` | `static` | `USER_PORTAL_RECENT_WINDOW_SECONDS` | no | no | no | yes | `container_recreate` | Recent authentication window; default 300 seconds |

Delegation rules are a JSON array with id, operator_groups, actions, target_users, target_user_groups and editable_groups, using stable anchors. The fixed actions are users.read, password.reset, account.unlock, groups.member.add, groups.member.remove and audit.read. Empty targets deny access; rules cannot be combined. Operator/scope boundary groups and their protection relationships are immutable.

Password/member binds must be dedicated portal service accounts with only the required OU/object/attribute rights. Do not provide Samba Administrator or widen shared binds. Manual binds are not automatically provisioned or rotated.

## Storage, backup, verification and limits

Only `${DATA_PATH}/user_portal/audit` persists: private 0700/0600 JSONL intents/results with fsync. Workspace data backup covers it; restore clears memory sessions. Failed intent persistence prevents LDAP writes. Failed result persistence reports the true outcome and closes subsequent writes. Corrupt, incomplete or full journals are never automatically repaired, removed or replayed.

The default limit is 64 MiB, configurable via audit_capacity_mb. Stop the portal, back up and reconcile unfinished records before managed export; never delete pending records. There is no automatic rotation interface.

Single instance; restart requires sign-in. Fresh checks and GUID single-member writes do not provide multi-object transactions. Restart the portal after Traefik's address changes. Evidence is in the [paired plan](dev-docs/plans/user-portal.md).

## Timezone and language

<!-- generated:localization:start -->
<!-- generated:localization:end -->

Chinese/English selection is immediate; unsupported selected languages use English. See [technical documentation](docs/technical.en.md).
