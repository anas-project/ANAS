# LemonLDAP::NG technical implementation

This page records the current implementation, security boundaries, and verification entry points for `llng`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `2.23.4-r13` / `anas.module/v1`.
<!-- generated:module-identity:end -->

The upstream version was updated to 2.23.4 on 2026-10-23. Host testing exposed a startup race:
closing the `ping` pipe after its first line could abort client registration under `pipefail`.
The Module now consumes the complete output and still fails when address lookup fails; no LLNG
upstream source is patched. New-version results and the historical baseline are recorded separately
in the [password-change plan](../dev-docs/plans/llng-password-change.md).

## Required modules, capabilities, and contracts

The directory subscriber reuses native Perl session and OIDC signing APIs in the LLNG image;
it adds no runtime dependency. `anas_llng_dirwatch` runs as UID 33 with read-only Samba events
and LLNG configuration, and separate durable cursor, pending notifications and health files.
It debounces for two seconds, calls at most once every five seconds and reconciles every
300 seconds. The healthy-dependency target is revocation within 60 seconds; host acceptance
is still pending. Notifications are durably saved before native grants are removed. Only
HTTP 200/204 acknowledges a native back-channel logout; failures retain the cursor and retry
while other directory changes continue to be evaluated. A corrupt cursor triggers full
reconciliation; corrupt pending notifications require recovery, not silent deletion.
Consumers without background logout remain visibly pending by default.
`llng.background_logout_exceptions` explicitly lists accepted OIDC client IDs, comma-separated
and empty by default (for example `vikunja`). Only clients without a background receiver may
record a durable limitation in `pending.json.limitations` and health `limited_clients` after
native grants are revoked, allowing the event cursor to advance. Their application sessions
can remain valid. Errors from a configured receiver always remain pending and retry. This
setting does not provide logout support or exempt other clients or SAML; an upgraded client
with a receiver automatically receives real notifications. See the
[implementation plan](../dev-docs/plans/llng-production-readiness.md) for actual results.

Public Nginx access logs include only address, time, method, path, status and byte count,
omitting query strings and Referer headers. Built-in Nginx request error logging embeds
unredacted URLs, so public virtual hosts disable it; safe access statuses and native LLNG
service errors remain available. Use the default `warn` level in production: upstream debug
logging can print protocol tokens.

The main container returns 503 during configuration; a shared read-only readiness marker
prevents the subscriber from using an incomplete client registry. Both containers verify
LDAPS certificates against the configured CA file mounted under `/certs/`.

Branding uses native `portalCustomCss`, `portalCustomJs`, `portalMainLogo` and `portalFavicon`
without copying authentication or password forms. The hook projects the catalog palette and
prefers its frozen hidden user_portal icon, then the LLNG administration icon, with the existing
ANAS portal mark as fallback. Palette values use the shared theme validator.
The portal's `theme=dark/default` navigation updates the authentication origin's local preference;
direct visits default to system appearance, with light, dark and system controls. Storage remains
origin-specific, so portal navigation carries the current appearance. LLNG continues to own form
tokens, password policy and protocol redirects. Browser language and narrow-screen results belong
to the readiness plan.

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |
| `iam` | Provides capability | `oidc, saml` |

## Compose topology

The image fixes public Perl modules, configuration templates and SQL templates to 0644, theme
directories to 0755 and assets to 0644. This keeps them readable by `www-data` when built from
a private source bundle. Private runtime configuration retains its existing permissions. The
directory watcher explicitly consumes Lego's `ANAS_TLS_TRUST_BUNDLE_NAME` for its public trust bundle.

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_llng` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-llng:2.23.4-r13` | `traefik, db` | 8 |
| `anas_llng_dirwatch` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-llng:2.23.4-r13` | `traefik, db` | 5 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `llng.background_logout_exceptions` | string | — | `""` | `static` | `LLNG_BACKGROUND_LOGOUT_EXCEPTIONS` | no | no | no | yes | `container_recreate` | Explicitly accepted OIDC client IDs without background logout, comma-separated; existing application sessions may remain valid. |
| `llng.db_name` | string | — | `lemonldap_ng` | `static` | `LLNG_DB_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `LLNG_DB_TYPE` | no | no | no | no: `migrate-llng-database` | `data_migrate` | Existing LLNG data must be migrated explicitly. |
| `llng.domain_prefix` | string | — | `auth` | `static` | `LLNG_DOMAIN_PREFIX` | no | no | no | yes | `reconcile` | SAML/OIDC metadata, clients, and proxy routes must change together. |
| `llng.enable_test` | bool | — | `true` | `static` | `LLNG_ENABLE_TEST` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.log_level` | string | — | `warn` | `static` | `LLNG_LOG_LEVEL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.manager_domain_prefix` | string | — | `auth-manager` | `static` | `LLNG_MANAGER_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.test_domain_prefix` | string | — | `auth-test` | `static` | `LLNG_TEST_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `llng.workers` | int | — | `2` | `static` | `LLNG_WORKERS` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

Samba AD supplies users and groups. The Portal authenticates against the directory, and IAM publishes OIDC/SAML endpoints and group attributes to consumers. `Admins` may enter the Manager.
The OIDC issuer preserves the trailing slash returned by pinned `2.23.4` discovery, which consumers must compare exactly; the discovery URL remains `/.well-known/openid-configuration` below the Portal root.

### Application-session logout

For pinned `2.23.4`, `render_env` maps the generic OIDC logout contract to `oidcRPMetaDataOptionsLogoutUrl`, `LogoutType=back`, and `LogoutSessionRequired=1`. `PostLogoutRedirectUris` and bypass-confirm serve only RP-initiated logout and do not replace back-channel notification. SAML imports the SLS and keeps `SignSLOMessage=1`; Redirect and POST guarantee only browser-mediated SLO. The startup script deletes the complete OIDC RP/exported-variable and SAML SP registries before rebuilding the current client contract, so old endpoints and opposite-protocol configuration cannot survive.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps authentication/search (`users, groups`) |
| IAM | provider: oidc, saml |
| Group | `Admins` + Consumer `APP_*` |
| Directory password writeback | restricted password-bind identity |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

### Native Passkeys

Native OIDC `oidcServiceMetaDataAmrRules` publishes signed `amr` from the actual first factor: `pwd` for AD and `webauthn` for WebAuthn. Authentication level alone no longer implies a password login. The user portal trusts this evidence only in a verified ID token.

`authentication.jq` enables native Choice on fresh and persisted configurations: AD passwords and
WebAuthn first factors. Debian's `libauthen-webauthn-perl` supplies the upstream verifier; no upstream
authentication code is patched. Credentials require discoverable keys and user verification (device
PIN or biometrics). Native device management supports multiple authenticators and revocation.

Persistent credentials use `anasIdentityAnchor` as `whatToTrace`. The Passkey branch looks up this
immutable AD identity, while password login uses the login name and pages display `displayName`.
The persistent table indexes WebAuthn user handles. Historical preferences keyed by usernames before
this feature are not migrated automatically; preserve the database and configuration in upgrade backups.

GrantSession rejects missing anchors, disabled/locked accounts and account expiry. Password expiry
and `pwdLastSet=0` do not block an existing Passkey. Registration does not require a second factor after
password login. If every authenticator is lost, a valid AD password can revoke old devices and enroll
replacements. Otherwise use existing AD password recovery; no independent recovery administrator is added.
See the [readiness plan](../dev-docs/plans/llng-production-readiness.md) for actual acceptance status.

Native device removal requires a nonempty activation rule; `0 == 1` keeps its controls available
without imposing a second factor. Per-process and local session caches are disabled so consecutive
registrations across workers cannot overwrite a new device using an old list, and deleted sessions
take effect immediately. Resource measurements include the additional database reads. The default
is two workers, adjustable with `workers`.

### Configuration-cache reload

After restoring Nginx runtime files, the entrypoint creates a `/reload` virtual host listening
only on `127.0.0.1:8089`, using the existing FastCGI server's `LLTYPE=reload`. No port is
published or externally routed. Both the fresh template and every persisted-config rebuild
point reload URLs there. Startup writes `/run/llng-configured` only after the final CLI cache
update and HTTP reload succeed, avoiding a healthy marker while workers serve an old RP registry.
Initialization also waits for workers through the separate loopback reload endpoint. Every public
Portal/Manager request returns 503 until ready, including requests through a loopback reverse proxy.
This follows the [upstream reload mechanism](https://www.lemonldap-ng.org/documentation/latest/configlocation.html)
and limits access with a loopback listener rather than exposing the Manager API.

### Directory attribute changes — implementation

LLNG authenticates Samba AD directly without a user replica. `sessions`, `psessions`,
`samlsessions`, `oidcsessions` and `cassessions` use session IDs as primary keys;
`_whatToTrace` and related fields are indexed search fields. `authentication.jq` sets the final
`whatToTrace=anasIdentityAnchor` for persistent credentials and Manager searches. Human labels still
use the directory display name. `ldapExportedVars` loads directory attributes on fresh authentication;
existing sessions may retain old values.

r12's `llng-config.sh` explicitly sets
`oidcRPMetaDataOptionsUserIDAttr=anasIdentityAnchor` while rebuilding every RP. In pinned
`2.23.4-1`, `Lib/OpenIDConnect.pm::getUserIDForRP` falls back to `whatToTrace` only if the
configuration option is empty. With the anchor option set it directly reads that session
variable. Authorization-code issuance, `_generateIDToken` and logout in
`Issuer/OpenIDConnect.pm`, plus `Lib/OpenIDConnect.pm::buildUserInfoResponse`, use this
method. Online refresh sessions save `_oidc_logout_sub` for subsequent logout notifications.
Refresh and access tokens may be opaque session IDs; they need not be JWTs containing `sub`.

Each RP Rule requires `defined($anasIdentityAnchor) and $anasIdentityAnchor ne ""` together
with its original application-group rule. An SSO session missing the anchor cannot obtain
an authorization code. `ldapExportedVars` always loads the anchor regardless of whether the
consumer requests an explicit anchor claim. Generic attributes still provide human login
names, display names, email and array-valued groups. The adapter maps the generic `email`
source to Samba AD's `mail` session variable, preserving the consumer's OIDC/SAML output
claim name. Reading an AD attribute named `email` loses the actual address and prevents
OIDC consumers that require an email from completing login. Other explicit sources remain unchanged.
`hook/iam.go` rejects Netbird's `sub`
URL projection at calculate and render_env boundaries; Nextcloud's anchor UID follows the
approved `DIRKEY-R-010` exception.

`test-env/scripts/server-llng-nextcloud-identity-e2e.py` uses the test host's existing
`cryptography` package to verify RS256 signatures, without a product runtime dependency.
It checks real authorization codes, UserInfo, refresh and signed Logout Tokens, plus original
Nextcloud cookies, LDAP mappings, rename/file ownership and recycled-label isolation.
Enabling refresh on the protocol fixture's RP does not imply that every production RP enables it.

SAML SP mappings now explicitly use the immutable anchor; real SAML application
acceptance remains pending in the readiness plan. This module still
has no directory-event watcher: disabling/removing groups does not delete established
sessions automatically. Manager removal and consumer revocation remain necessary. Ordinary
Portal logout is not evidence of live directory-event revocation.

### Public OIDC clients and SAML identity

Public OIDC clients explicitly declare `PUBLIC_CLIENT=true` without a secret, mapping to native
Public and RequirePKCE options. Reauthentication uses a separate public RP with the same claims and
admission groups. Both preserve RS256, exact redirect URIs and immutable subjects. The `AnasOIDC`
plugin uses upstream hooks to require S256 for public clients and a fresh password or Passkey interaction
for the reauthentication RP. Version 2.23.4 ignores `max_age=0` and normally tolerates a recent cookie
for `prompt=login`; those parameters alone do not prove a new authentication. Native browser validation
and the complete user_portal integration remain tracked in the readiness plan.

After PKCE validation, anonymous authorization requests return to native authentication instead of
refreshing a missing directory identity. The native issuer can return `login_required` for `prompt=none`;
requests with an existing session still require a fresh directory check.

`AnasDirectory` rereads AD account state and nested groups for OIDC authorization and SAML
AuthnRequest; directory errors deny new admission even with an existing SSO cookie. The
`llng-account/v1` self-account endpoint only accepts tokens issued to the bound portal RPs.
Native token/session validation precedes current identity, groups and admission-rule evaluation.
The read-only `account-policies.json` links catalog entries to native RP/menu rules; changed rules
omit the corresponding permission. No administrative token, other-user lookup, private device data
or directory write capability is exposed to the portal.

Each SAML SP explicitly sets its requested NameID format, ForceNameIDFormat and
`NameIDSessionKey=anasIdentityAnchor`, defaulting to persistent. Transient/entity formats are rejected
because they cannot maintain a stable user identity. GrantSession rejects new SSO sessions with missing
anchors. The real SP matrix remains pending.

### Native mandatory and expired-password changes

The initial `lmConf.json` enables `ldapChangePasswordAsUser` and
`ldapAllowResetExpiredPassword`. The startup CLI also writes both as `1` so persisted
configurations converge. This adds no Module parameter, service or upstream source patch.
Native `Auth/AD.pm` maps `pwdLastSet=0` to PE25 and genuine expiry to PE22, rejecting an
incorrect current password as PE5. `Password/AD.pm` uses `Lib/Net/LDAP.pm` to submit a
single `unicodePwd` delete-old/add-new modification; Samba validates the old password.
The search account needs no administrative password-reset privilege and the expired
user need not first complete an ordinary LDAP bind.

`Auth/LDAP.pm` returns PE35 and a login form after success, deliberately creating no
session. Users authenticate again with the new password. The [acceptance plan](../dev-docs/plans/llng-password-change.md)
records actual browser/OIDC results and artifact limits. See the [native-flow research](../../../docs/research/llng-samba-password-change.md)
and [Casdoor/UI/passwordless comparison](../../../docs/research/llng-casdoor-integration-comparison.md)
for the broader capability comparison.

## Management surfaces and secret lifecycle

There is no independent local `break_glass` account. Manager and Portal share directory authentication; recover IAM or the directory from the host instead of relying on a nonexistent local password.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Portal Documentation menu

`lmConf.json` assigns `inGroup("{{SAMBA_DC_ADMIN_GROUP_NAME}}")` to both
Documentation applications for new installations. On every startup,
`llng-config.sh` reapplies the same rule to the current persistent configuration,
replacing an older revision's `display=on`. When neither child application is
visible, ordinary users do not receive an empty Documentation category, while
administrators retain both the local-documentation and official-site entries.

### Secret boundaries

- `LLNG_OIDC_SERVICE_KEY_ID`
- `LLNG_SERVICE_PRIVATE_KEY`
- `LLNG_SERVICE_PUBLIC_KEY`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

### Signing-key exposure and rotation

Treat an OIDC/SAML signing private key as potentially exposed whenever it enters
terminal output, logs, test reports, task transcripts, or any other non-secret
boundary, even without evidence of external transmission. Diagnostics must read
an exact allowlist of configuration keys rather than recursively dumping LLNG
configuration. Reports may contain a key ID, fingerprint, and public key, never
the private-key body.

ANAS does not automatically rotate a running LLNG key. Rotation is a separately
approved operation: inventory whether every RP/SP consumes dynamic JWKS/metadata
or pins a certificate, back up workspace secrets and LLNG configuration, create
the replacement and publish its public material, refresh pinned trust, verify
OIDC/SAML login and logout, and retire the old key only after token/assertion
lifetime and rollback windows close. A deployment without overlapping-key
support needs a maintenance window. Until rotation is approved, record the
incident, restrict access to the affected transcript/log, and retain an explicit
rotation action; never silently overwrite the secret.

Native 2.23.4 supports multiple entries in `keys`. The first entry in the comma-separated
`oidcServiceSignatureKey` list signs tokens; all valid entries are published in JWKS.
Publish the new public key while the old key still signs, refresh consumer caches, promote
the new signer while retaining the old public key, and retire the old key after its lifetime
and rollback window. Fixture changes to native configuration do not update workspace secrets;
both must agree before recreation in an actual deployment.

Nextcloud `user_oidc 8.11.0` caches JWKS for 3600 seconds and throws for an unknown `kid`
before reaching its refresh retry. Do not promote immediately after publication. Wait for
and verify a cache refresh, or use native `occ config:app:delete user_oidc
provider-<actual numeric provider ID>-jwksCacheTimestamp`, then complete a real OIDC login
to fetch both public keys before promotion. Delete only that provider's timestamp, leaving
credentials, mappings, and other providers unchanged. Early retirement breaks validation of
unexpired old tokens against the current JWKS; the fixture tests this failure boundary,
not a recommended production sequence. Actual acceptance results belong to the paired plan.

For Nextcloud 8.11.0, pass client credentials using `occ user_oidc:provider --clientsecret-file`
with an application-readable temporary 0600 file and remove it afterward. Include the existing
`--clientid` in the same command: this version omits secret-file from update-option detection,
so that option alone merely queries the provider and returns zero. Verify a real login after
both endpoints change and confirm the old secret fails client authentication; a successful
command exit code does not establish successful rotation.

LDAP bind and database rotation must update both the service and LLNG credential projection,
with a usable rollback configuration retained. Changing a database password does not close
existing connections: verify retired and replacement passwords using fresh connections, then
check real application login and new session writes. PostgreSQL probes must match a password
authentication rule such as SCRAM; success under `trust` proves nothing about the password.
Isolated acceptance uses a dedicated bind account, cloned database, and dedicated role to
avoid changing shared accounts. This does not provide an ANAS unified service-credential
rotation command. Actual operations must also synchronize workspace secrets and runtime
configuration and verify that recreation continues to use the replacement credentials.

Samba AD defaults `old password allowed period` to 60 minutes, so a successful password
change does not imply immediate retirement. The isolated 4.23.6 fixture also accepted the
previous password through LDAPS simple bind during that window. Acceptance shortens only
the disposable domain's window to observe actual expiry. Reloading configuration did not
apply the shorter window on this baseline; restarting the test DC did. Production rotation must wait for
and verify expiry, or separately approve a directory-policy change; LLNG must not silently
change the domain policy. See the [Samba parameter documentation](https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html#OLDPASSWORDALLOWEDPERIOD).

## Database support

| Item | Value |
| --- | --- |
| Role | Consumer |
| Interfaces | `postgres`, `mariadb` |
| Default | `postgres` |
| Resource | `primary_database` |
| Credential policy | `generated` |
| Deletion policy | `retain` |

The runner creates a dedicated database, principal, and stable generated credential. Changing `db_type` or `db_name` never migrates existing data.

## Environment ownership

### Exports

- `ANAS_IAM_BINDING_*`
- `ANAS_IAM_PORTAL_URL`

### Explicit consumes

- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `SAMBA_DC_ADMIN_GROUP_NAME`
- `SAMBA_DC_BASE_GROUPS_DN`
- `SAMBA_DC_BASE_GROUPS_ROLE_DN`
- `SAMBA_DC_BASE_USERS_DN`
- `SAMBA_DC_LDAPS_PORT`
- `SAMBA_DC_LDAPS_SERVER_URL`
- `SAMBA_DC_PASSWORD_BIND_DN`
- `SAMBA_DC_USER_CLASS_FILTER`
- `SAMBA_DC_USER_EMAIL`
- `SAMBA_DC_USER_ENABLED_FILTER`
- `SAMBA_DC_USER_NAME`
- `TRAEFIK_DOMAIN_FULL`
- `TRAEFIK_HOSTNAME`
- `ANAS_IDENTITY_OIDC_CLIENTS`
- `ANAS_IDENTITY_SAML_CLIENTS`
- `ANAS_IAM_CLIENT_*`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`iam_test.go`](../hook/iam_test.go)
- [`lmConf.json`](../llng/root/root/lmConf.json)
- [`llng-config.sh`](../llng/root/root/llng-config.sh)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Real client IP

Container startup enables `real_ip_header X-Forwarded-For` and recursive parsing in LLNG's Nginx. It trusts only `TRAEFIK_HOSTNAME` plus explicit upstream proxy IPs or CIDRs already validated by Traefik. Login history, session auditing, and IP-based rules therefore receive the leftmost untrusted client address instead of a Docker bridge address; an invalid proxy value fails startup.

## Current limitations

Do not configure the removed `LLNG_PASSWORD`; it does not create an upstream administrator.

## Trusted OIDC application role source

The reserved generic OIDC ATTRIBUTES source `anasRole` uses an `inGroup` macro to derive admin/user
from the trusted administrator group. The same-named LDAP exported variable is removed so user
attributes cannot impersonate roles. Other ordinary directory claims retain their existing mapping.

## Application catalog

Personnel entry metadata is declared in module.yml applications, including zh-CN/en text, a stable URL
source, categories and permission facts. Runner generates seven PNG sizes and an immutable deployment
catalog; only explicitly declared IAM consumers receive it. Startup removes the upstream sample application,
administration and documentation groups so undeployed examples and old URLs do not appear in the menu.
ANAS management and documentation entries retain their administrator group rules. The default portal template renders one category
level, so native category labels combine “User applications / primary category” or “Administration / primary
category”, with applications directly beneath them. The full two-level layout belongs to the second-stage
portal. Local-auth entry visibility does not protect
the target URL. Native IAM display limitations and end-to-end acceptance are tracked in the
[catalog design](/architecture/app-catalog-design).

### PostgreSQL session driver

The PostgreSQL session backend explicitly depends on `libdbd-pg-perl`. Before FastCGI starts, the entrypoint preloads the driver through the standard `PERL5OPT=-MDBD::Pg`, preserving existing Perl startup options. The MariaDB branch does not add it. Driver loading therefore runs outside authentication request timeouts, avoiding a failed lazy require poisoning persistent workers. Missing drivers fail startup; there is no file-session fallback, relaxed authentication or longer request timeout. This change follows an observed cold-start failure during mail integration; the root mail plan records cold-start and browser verification.

### Application icon provenance

`applications.icons.source` declares the 512px PNG. `assets/icon-source.json` records the official URL, original digest, redraw status and generation inputs; original artwork is retained alongside it. `node scripts/application-themes.mjs --check` verifies provenance and the original/minimal packages. Use `--render` after changing vector artwork; runtime code never loads SVG or downloads remote images. The global theme uses the shared catalog; personal themes affect the portal only.
