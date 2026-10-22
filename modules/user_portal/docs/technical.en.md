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
| `anas_user_portal` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-user-portal:0.1.0-r1` | `` | 5 |
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
| `user_portal.service_api_credentials_dir` | string | — | `""` | `static` | `USER_PORTAL_SERVICE_API_CREDENTIALS_DIR` | no | no | no | yes | `container_recreate` | Dedicated host token directory prepared by anasd for this portal client; mounted read-only and configured together with the service origin and workspace |
| `user_portal.service_api_url` | string | — | `""` | `static` | `USER_PORTAL_SERVICE_API_URL` | no | no | no | yes | `container_recreate` | anasd HTTPS origin called by the portal server; configure together with the workspace and token directory, with credential retrieval disabled by default |
| `user_portal.service_api_workspace` | string | — | `""` | `static` | `USER_PORTAL_SERVICE_API_WORKSPACE` | no | no | no | yes | `container_recreate` | anasd workspace ID authorized for the service token; configure together with the service origin and token directory |

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

`domain_administration_enabled` defaults to false. Protected personnel/system groups require recent authentication, an expiring one-use parameter confirmation and the signed-in person's own per-request LDAPS bind and native AD rights. That password is not stored across requests. This is not an MFA or process isolation claim, and the recommended dedicated-bind configuration holds no permanent domain-wide writer credential. AD remains authoritative for custom ACL permissions.

## Interface, languages and account preferences

The front end is plain JavaScript without a build step. `theme.js` runs synchronously before the stylesheet and applies the theme last used in this browser; `i18n.js` holds the interface text in zh-CN, zh-TW, en, ja, ko, fr, de and es; `app.js` renders `#/apps`, `#/account/<profile|security|groups|mail|log>`, `#/admin/users[/<anchor>|/new]` and `#/admin/audit`, and unknown addresses return to `#/apps`. All text is assigned with textContent; the CSP still allows only same-origin scripts and styles. Language matching and catalog text fallback live in internal/appcatalog: Traditional Chinese regions map to zh-TW and fall back to Simplified Chinese first, other added languages fall back to English; catalog content still needs only Chinese and English.

Themes are follow system, light and dark. Colors are CSS variables; in follow-system mode `prefers-color-scheme` switches them live. Workspace and Manage are separate modes; only users with the personnel query action see Manage, whose sidebar has only people and security records and lists no applications (administration applications are on the workspace). The whole application card opens the entry; the ⓘ button in its corner opens in-page details (description, categories, documentation, pin). ⌘K / Ctrl+K opens quick search: applications come from the current authorized catalog, pages follow permissions, and people results only call the existing `/api/users`. Write results show only confirmed outcomes. Password changes prompt sign-in with the new password; untracked IAM and application-session states are omitted. Internal API/audit states remain, including explicit failures and partial ownership-transfer outcomes. Password reset and unlock sit in a danger zone with unchanged confirmation and personal verification.

Recent authentication has two sources: a verified OIDC `auth_time` from signing in again, and the in-page password check `POST /api/reauth/password`. The latter accepts only `{"password"}`, requires the current session and CSRF, and is rate limited by account, IP and identity. The portal rereads the person's directory object, binds over LDAPS as that person and checks GUID, SID and identity anchor; success sets the session's `VerifiedAt` to now, valid for `recent_window_seconds`. Wrong passwords and disabled, locked or must-change accounts all return the same `authentication_rejected`. The password is never logged or cached, and both success and failure are written to the security audit as `session.reauth`. When an operation returns `recent_authentication_required`, the page opens a password dialog and retries the operation once after success; cancelling runs nothing. This path bypasses IAM, so if IAM later enables multi-factor authentication it will not ask for a second factor and must be reviewed again.

Account preferences (language, theme, category display and up to 24 pinned applications) are saved with `POST /api/me/preferences` and returned in the `preferences` field of `GET /api/me`. Writes require the current session and CSRF and accept only whitelisted fields and values; they need no recent authentication and are not security-audited. Pinned applications are filtered by current authorization on read, and unauthorized names are rejected on save. Anonymous pages use this browser's cache only; after sign-in the account preference wins and updates the cache.

## Storage, backup and upgrades

The private audit.jsonl and `preferences/preferences.json` belong to workspace data and require data backup. Startup creates the preferences directory as 0700 owned by uid 65532; the file is written through a temporary file and an atomic rename, and an unreadable or corrupt file yields default preferences, rejects writes and is never overwritten. Sessions are memory-only. Pending intents appear as unknown; corruption closes writes and preserves the original file. A single process holds the file lock, without database/cache/sync services.

## Verification

`go test ./modules/user_portal/portal ./modules/user_portal/hook` covers OIDC identity/time binding, policy/AD denial, CSRF, audit failures and no replay. Casdoor hooks/appcatalog are checked together. The UI suites `node test-env/playwright/user-portal-ui.test.mjs` and `node test-env/playwright/user-portal-experience.test.mjs` use the production static assets with mocked APIs. Server/browser entrypoints, fixed artifacts and per-ID outcomes belong in the paired plan. Unexecuted criteria remain unaccepted.

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

### Application icon themes

`theme` still selects system/light/dark; the independent account preference `icon_theme` selects artwork, with an empty value following the site default. Built-in `original` preserves source icons; `soft` provides minimal geometric artwork. Personal choices never change Casdoor. `application_catalog.icon_theme` freezes the global default into the existing shared catalog, and Casdoor continues using native Application.logo.

Current ANAS administrators can import and export data themes at `#/admin/themes`. Import requires CSRF and recent authentication; it makes a theme globally selectable without changing preferences or running apply. `anas.application-theme/v1` accepts names, allowed hexadecimal color tokens, PNG bytes and attribution only. Arbitrary CSS/scripts/SVG/remote images and unknown fields are rejected. Each source is a square 512–2048px PNG of at most 2MiB; a package allows 128 images and 32MiB, while the private library allows eight imported themes and 32MiB in total. Text/background pairs require at least 4.5:1 contrast.

`GET /api/themes` returns presentation information without an application inventory. `GET /api/assets/{name}/{size}?theme=ID` checks current application access before serving themed artwork. Missing images retain originals; unavailable choices fall back to the site default with a settings notice. `GET /api/themes/{id}/export` restricts full packages to administrators; a configured global theme absent from the writable library is not downloadable. Private `themes/*.json` uses 0700 directories, 0600 files, atomic writes and restart loading. Conflicting IDs are never overwritten; corrupt files remain preserved and unselectable.

See the [theme guide](/en/guide/application-themes) for provenance and configuration. Added validation commands are `node scripts/application-themes.mjs --check`, `go test ./internal/apptheme ./internal/appcatalog ./modules/user_portal/portal ./modules/casdoor/hook` and `node --test test-env/playwright/application-themes.test.mjs`. Cross-module plan records remain authoritative for real-host acceptance.

### Native IAM appearance and password entry

Portal sign-in passes the resolved current color mode as Casdoor's native `theme=dark/default`; system mode is resolved on click, without live synchronization of already-open IAM tabs. A `hidden: true` portal branding entry supplies a 512px rounded logo for sign-in and reauthentication without adding a visible self-link or access groups.
ANAS directory sign-in pages expose “首次／到期改密 · Set new password” through native `forgetUrl`, targeting the portal `/#/recover` page. That page verifies the old/temporary password before writing AD. The local recovery administrator keeps Casdoor's native recovery flow. Since Samba rejects LDAP binds before login when a password change is mandatory, this version provides an entry link, not an automatic IAM password-change screen, with no additional Casdoor patch or directory grant.

### Login details for backends without IAM

Module administration entries with an explicit local credential declaration can show “Get login details”. The portal filters `credentials_available` using current IAM application policy, directory admission groups and the ANAS administrator role. Ordinary users, IAM entries, external entries and entries without a credential declaration do not receive the action. `POST /api/apps/{name}/credentials` checks these conditions again, along with same-origin CSRF and real authentication within the existing five-minute window. Expired proof uses the existing in-page own-password verification without an additional domain-operation confirmation.

The feature is disabled by default. Enable it by supplying all three settings:

| Setting | Content |
| --- | --- |
| `service_api_url` | The anasd HTTPS origin, such as `https://anasd.example.net:8443`, without a path, user information or query |
| `service_api_workspace` | The workspace ID authorized by anasd |
| `service_api_credentials_dir` | This portal client's dedicated token directory on the host, matching the delivery directory registered in anasd; never mount the whole `/run` tree |

Before initial enablement, anasd must create the dedicated client directory with its permissions. Compose is forbidden from creating a configured directory, avoiding a Docker-created path that conflicts with anasd security checks. Compose mounts the dedicated directory read-only at `/run/anas/service-client`. The `token` file contains the complete bearer credential and is reread on every service call. When disabled, the `render_env` hook prepares only the empty `${DATA_PATH}/user_portal/service-client` directory for the read-only mount. The directory and file must be readable by portal UID/GID 65532 and inaccessible to other users. anasd maintains the token; the portal never writes it. An existing directory with no token does not prevent portal startup: credential requests fail temporarily and automatically recover once the file is published. Service tokens never enter the browser, application catalog, preferences, audit records or module environment values.

The portal verifies anasd TLS with its existing CA bundle and calls `POST /api/v1/service/workspaces/{workspace}/applications/{application}/credentials` with the bearer token, current actor anchor and a random request ID. Redirects are forbidden. A 401 permits exactly one retry only if rereading the file yields a different token. Network, TLS, service rejection and unreadable-file failures return a generic unavailable response without upstream error bodies; backend passwords are not cached. anasd controls the client's API and resource scope, while the portal controls user authorization.

A durable intent precedes retrieval. Before returning credentials, the portal rechecks the session, directory role and IAM permissions and durably records the result. Failure at either step prevents disclosure. The `application.credentials.read` audit action records the actor, application, request ID and outcome only. Separate buttons copy username and password; passwords are masked by default. Close, in-page navigation and logout clear the inputs, and delayed responses cannot recreate the dialog. Copied clipboard values remain under the user's control.

Local validation uses `go test ./modules/user_portal/portal ./modules/user_portal/hook` and `node --test test-env/playwright/user-portal-credentials.test.mjs`. The latter runs the production UI against mock APIs; it does not establish real-host, anasd or backend acceptance.
