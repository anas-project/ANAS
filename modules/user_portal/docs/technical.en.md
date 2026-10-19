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
| `anas_user_portal` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-user-portal:0.1.0-r1` | `` | 4 |
<!-- generated:compose-topology:end -->

One Go process embeds UI/API. Traefik is the only ingress; no host ports or Docker socket. Root bootstrap initializes only the fixed audit mount through directory descriptors, then irreversibly switches to UID/GID 65532. Application rootfs is read-only.

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `user_portal.administration_enabled` | bool | — | `true` | `static` | `USER_PORTAL_ADMINISTRATION_ENABLED` | no | no | no | yes | `container_recreate` | Enable the registered ANAS administrator role; writes still need scoped bindings and ACLs |
| `user_portal.audit_capacity_mb` | int | `1..1024` | `64` | `static` | `USER_PORTAL_AUDIT_CAPACITY_MB` | no | no | no | yes | `container_recreate` | Private audit capacity; full storage closes writes |
| `user_portal.create_bind_dn` | string | — | `""` | `static` | `USER_PORTAL_CREATE_BIND_DN` | no | no | no | yes | `container_recreate` | Designated authorized bind DN for ordinary user creation in the provider user OU |
| `user_portal.create_bind_password` | string | — | `""` | `static` | `USER_PORTAL_CREATE_BIND_PASSWORD` | no | no | yes | yes | `container_recreate` | Creation bind password; never sent to the browser |
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
| `user_portal.recent_window_seconds` | int | `5..300` | `300` | `static` | `USER_PORTAL_RECENT_WINDOW_SECONDS` | no | no | no | yes | `container_recreate` | Recent authentication window; default 300 seconds |

module.yml is authoritative. Additional delegation rules default to empty. ANAS administration is a separate registered role; dedicated directory ACLs are an explicit operator step. Explicit runtime environment excludes application API secrets and IAM registration variables.

Source builds reuse the repository's `DOCKER_HUB_REGISTRY`, `GOPROXY_URL` and `CHINESE_BUILD_SPEEDUP`/`APK_MIRROR_URL`. `GO_MODULE_PROXY` can override the Go proxy separately; official endpoints remain the defaults. Download acceleration does not disable TLS verification.

## Hook and IAM

The hook publishes only its own USER_PORTAL client registration. PUBLIC_CLIENT registers PKCE without delivering an application API secret to the portal. Native import may generate a provider-side secret that the portal does not consume. REAUTHENTICATION_CLIENT_ID/REAUTHENTICATION_REDIRECT_URI request a separate native application with automatic/session sign-in disabled. Signed auth_time is still checked; max_age/prompt alone are insufficient. No new Casdoor upstream patch is added.

## Data flow and security

Memory sessions and one-use state/nonce/verifier have capacity limits and TTL. Cookies are Secure, HttpOnly, SameSite=Lax and host-only. Writes validate Origin/CSRF; only the exact Traefik peer reaches business APIs. LDAP verifies CA/server name and never falls back.

The directory reuses appcatalog language/BuildView. Policies must be enabled, Approved, Allow and match model/resource/Read/group sets, intersected with current AD groups. Native default allow/admin bypass is not used. Icon authorization precedes the no-link reader and digest check.

LDAP actions are limited to the three name fields, password change/reset, pwdLastSet, lockoutTime and one member add/delete. Anchor resolution yields a current GUID; no arbitrary attribute/DN, full-member replacement or command endpoint exists. Audit has no password/token field; intent fsync precedes LDAP, unknown network results never replay, and result-storage failure closes writes.


### Profiles and ANAS administration

Users may edit given name, surname and display name. Email, login name and anchors remain read-only; no alias writes exist before mail integration. Profiles recheck exact field values immediately before writing and serialize portal profile writes, followed by one old-value LDAP Modify. AD name matching is case-insensitive; external directory tools do not share a global transaction. The category switch controls both user and administration subcategories, preserves the two main groups, and persists per browser, while form outcomes are visible next to the operation and saved names immediately refresh the visible account summary. Unsupported languages select English.

`administration_enabled` binds the directory provider's registered ANAS administrator role to its stable identity, not a Casdoor native administrator flag or a name prefix. Registered groups are categorized by purpose; system groups require the advanced risk view. Self password reset, self removal from administrator paths and removal of the last confirmed available administrator are rejected. Computers, service and trust accounts are excluded.

Before enabling writes, an operator runs `tools/provision-directory.py` inside the selected Samba DC, supplying private JSON on stdin with confirmation `provision-user-portal-scoped-writers`, the actual base_dn and separate random profile/reset/group/create passwords. It creates `svc_portal_profile`, `svc_portal_reset`, `svc_portal_group` and `svc_portal_create` and adds attribute/object ACLs, without domain roles or resetting existing service passwords. Configure the resulting DNs/passwords in the portal and redeploy. Keep input under a 0700 directory in a 0600 file, never in argv, Git or logs. This is an operator tool, not a portal endpoint.

The profile writer only writes the three name fields. Reset covers ordinary personnel's reset/pwdLastSet/lockoutTime; member writes cover registered, non-domain groups. Provisioning requires ordinary user service accounts with no extra groups and Domain Users as their primary group, and checks that primary group's effective domain privileges before granting ACLs. Existing domain-level personnel receive explicit reset denials, including direct, nested and primary-group privileges; an unresolved primary group stops provisioning. Privilege changes or new privileged users made outside the portal require the operator to recheck ACLs. Object ACLs do not constrain member values and cannot replace current portal authorization. A non-domain service account credential still has sensitive delegated rights.

If a profile write succeeds but the following read fails, the form retains the saved result and request ID and asks for a reload. A late profile response after leaving the form cannot restore a signed-out browser identity.

`domain_administration_enabled` defaults to false. Protected personnel/system groups require recent OIDC, an expiring one-use parameter confirmation and the signed-in person's own per-request LDAPS bind and native AD rights. That password is not stored across requests. This is not an MFA or process isolation claim, and the recommended dedicated-bind configuration holds no permanent domain-wide writer credential. AD remains authoritative for custom ACL permissions.

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

### System primary addresses and user creation

`samba_dc.mail_domain` inherits the AD DNS domain when unset. The finance.hlong.wang admin receives `admin@finance.hlong.wang`; configuring hlong.wang reconciles personnel addresses to `username@hlong.wang`. AD domain, UPN, login names and identity anchors remain unchanged. The system owns `mail`; the UI accepts no manual primary address. An assigned address does not mean mail service is active.

Enumeration, details, profile, password, group and domain operations are restricted to the provider user OU and its descendants. ANAS administrators can create ordinary people with “New user”. The backend uses the designated `create_bind_dn/password`, independently of the frontend operator's domain credentials. Prefer `svc_portal_create` with user-object creation rights only in that OU. An already authorized configured account is also supported; portal roles and directory permissions are separate checks. Configuring a domain administrator as the creation bind would store that account’s existing privileges in the portal process. Per-request domain verification does not isolate that credential; the dedicated scoped bind avoids it.

`POST /api/users` accepts only `username`, the three `profile` fields and `force_change`. Login names allow up to 20 ASCII letters, digits, underscores, dots and hyphens and must form a valid system address. The server determines mail, UPN suffix, DN, random initial password and anchors and writes them in one native LDAP Add without administrator membership. Recent authentication, CSRF, current administrator authority and durable audit are required. Conflicts and unknown results must not be retried automatically. The temporary password appears only in the successful response; a change at next sign-in is required by default.

Creation authorization limit: on finance Samba 4.23.6 the dedicated creator also owns its new user object and can edit that object’s DACL (access-control list). A temporary native Owner Rights rule did not remove this ability. Child-creation ACLs alone therefore do not prove strict isolation from later privileged accounts. The portal performs and verifies the ownership transfer below before domain-risk membership. Do not grant additional domain privileges to the default creation bind. No global Owner Rights policy or Casdoor patch has been applied.


### Ownership before domain-risk membership

Before adding a person to a domain-risk group, the personal domain bind transfers the user object owner to Domain Admins in the same domain, using a critical native SD Flags control that writes only the owner. Membership is added only after readback. Uncertain ownership stops the request; membership failure retains the new owner. API and audit expose ownership and membership separately, and the confirmation explains partial results. There is no cross-object transaction; external concurrent changes require verification. Ownership transfer cannot revoke other ACLs or group privileges held by a custom broadly privileged creation bind.


## Shared group catalog and in-page confirmation

Each Module declares group information in `identity.groups`. After all calculate hooks finish, Core generates one deployment-scoped `shared/identity-groups/catalog.json`. Samba and the portal declare `identity.group_catalog: true` and mount it read-only at `/run/anas/identity-groups`. Application access groups reuse `identity.application_group` and existing application metadata. Capability families select the owning module's prefix from its published list. Samba owns common ANAS groups and descriptions of known AD built-ins. Legacy CSV outputs are catalog projections; the portal does not infer categories from APP_/CAP_ prefixes.

The default view shows registered groups bound to actual directory objects, with names and descriptions grouped by application, capability, file resource and role. System and other groups are inside a collapsed Advanced section at the bottom. External groups use their AD description or an explicit missing-description message. English and Simplified Chinese follow the page language; unsupported languages use English. Metadata does not replace native SID, nesting or AD permission checks.

At widths up to 600 pixels, each group shows its name and description above a row containing the membership status and actions. Status and button labels stay on one line; long group names wrap within the available width.

Adding an ordinary managed group (other than the ANAS administrator role group) executes directly. Removals, account creation, administrator profile changes, reset and unlock use an in-page operation summary. System-group additions require confirmation; managed groups with domain risk retain personal domain credentials and one-use confirmation. Pages and write requests provide no operation reason or note. Write APIs reject the removed `reason` field. Audit still records the action, target, authority and result; historical records are retained. Cancel and close perform no write; native browser alert/confirm/prompt calls are not used.

Adding a person to, or removing one from, the ANAS administrator role group (including groups that inherit the role through nesting) requires real authentication within five minutes, an in-page confirmation and a time-limited one-use parameter confirmation, without another password prompt. The role manages every person and application and, when anasd enables `trusted_proxy`, maps to the console owner. After confirmation the portal's scoped group writer still performs the membership change; domain administration need not be enabled. The audit rule is `platform_admin_confirmed`.

Domain confirmation prepares once and requires a one-use `proof` bound to the session, complete parameters and directory GUIDs. The server rejects replaced objects, changed parameters, expiry and replay.

Language and category controls retain native select semantics with consistent text padding and arrow placement, including keyboard and narrow-screen use.

Ordinary user operations and ANAS administrator role changes reuse real authentication within five minutes. Role changes retain an in-page, one-use confirmation without another password prompt. System/OS privilege groups and protected-account operations still require the operator’s own domain password and native AD rights for each request. Personal password changes still need the old password. Sign-out offers a homepage link, retaining retries and an unconfirmed status if IAM logout fails. Cross-application observations and limits are recorded in plan §19.


### Current-browser logout cleanup

After removing its local session, the portal signs out of native IAM and cleans up older Nextcloud and Immich browser sessions through the fixed `/.well-known/anas/browser-logout` path at each app origin. The existing portal process serves this path. Targets are trusted native modules on the same HTTPS site and port; short-lived signed requests and exact origin/window/nonce message checks protect the bridge. External catalog entries cannot supply arbitrary executable logout requests.

Business catalog filtering remains strict. The approved independent protocol exception contains only origins, fixed paths, and logout-only random capabilities, including hidden or no-longer-authorized applications. Native cookies and API responses stay in the app frame; only completion status is messaged to the portal. Unavailable APIs, timeouts, or valid identities remain unconfirmed. Partial results offer a CSRF-protected retry and a homepage link. There is no username-wide device revocation. See the paired plan for native-host acceptance and app-initiated integration progress.


The fixed native return `/signed-out?native=1` may anonymously read the same minimal logout protocol metadata, as explicitly approved. Business catalog and asset authorization are unchanged. Ordinary anonymous homepage responses do not publish app origins; the fixed logout page allows CSRF-protected retries after reload. Cross-site navigations and clients without Fetch Metadata require an in-page continue action; same-site native returns continue automatically using anonymous CSRF sessions.

Reloading the exit page discards cached results and returns to applications when bootstrap confirms a new portal login. Anonymous sessions retain partial results and retry controls. Cached results cannot override current authentication.

When a native IAM backchannel revokes the portal session between bootstrap and completion, the UI refreshes CSRF and retries once only after an explicit 403 `csrf_rejected`, before any logout effect. Repeated rejection, 5xx and network-unknown outcomes are not replayed. This recovery never applies to profile, password, account creation or group writes.
