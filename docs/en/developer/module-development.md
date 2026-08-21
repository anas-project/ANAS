# Module development

A module is an independent release and deployment unit. It owns its manifest identity, version, ABI, dependencies, capabilities, configuration declarations, Compose definition, optional hook, templates, and assets.

The frozen deployment must carry everything needed to start. It must not depend on relative paths into a source checkout.

Module parameter semantics belong to the Module, not Core. Put cross-parameter
checks in the `validate` Hook, derived values in `calculate`, and persistent
state coordination in lifecycle operations or reconcilers. Never request a
Core branch for a Module name or direct mutation of its private parameters. See
the [Core implementation standard](/en/architecture/core-implementation-standard).

## Version and revision ownership

`version` follows the normalized upstream application version. `revision`
identifies a released ANAS image revision for that same upstream version.
Routine feature, fix, and documentation changes must not increment `revision`
merely because files changed. After changes merge into `image-release`, the
release workflow calculates the authoritative value and writes it back to
`module.yml`, `localization.yml`, the Compose image tag, and generated
documentation before the release result returns to Git.

The sole reason to change `revision` temporarily in a local branch is an E2E
run that must build an image distinct from the published image. Keep every
revision projection consistent during that test; ordinary feature branches
restore the published revision before commit and let `image-release` choose the
next formal number. Hook, configuration-rendering, and other tests that need no
new image do not change the revision.

Declare hard dependencies explicitly. Use capability providers for alternatives, ordering edges only for ordering, and resource/provider operations for persistent resources. Scope generated environments to the module, its dependency closure, and explicitly consumed keys. Never log secrets or inject unrelated credentials.

## Bidirectional logout for OIDC/SAML Modules

Every Module that consumes the `iam` capability and establishes an application
session through OIDC or SAML must design and verify three distinct cases:

1. logging out from the application invalidates both the application session
   and the central IAM session;
2. logging out from IAM notifies the application and invalidates its session;
3. when claimed as supported, administrative IAM-session revocation invalidates
   the application session without the user's browser.

The normative security and acceptance rules are in the
[bidirectional logout requirements for OIDC/SAML Modules](/requirements/module-iam-bidirectional-logout).

### Provider-neutral registration

A Module reads only its own `ANAS_IAM_BINDING__<APP>__*` values and publishes
its own `ANAS_IAM_CLIENT__<APP>__*` request from the `calculate` Hook. It must
not branch on a Provider name or generate LLNG-, Authentik-, Casdoor-, or other
Provider-private settings.

An OIDC Module that accepts IAM-initiated logout publishes:

```dotenv
ANAS_IAM_CLIENT__<APP>__POST_LOGOUT_REDIRECT_URIS=https://app.example/logged-out
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_URI=https://app.example/oidc/backchannel-logout
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_METHODS=backchannel,frontchannel
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_SESSION_REQUIRED=true
```

A SAML Module that supports SLO publishes:

```dotenv
ANAS_IAM_CLIENT__<APP>__SAML_SLS_URL=https://app.example/saml/sls
ANAS_IAM_CLIENT__<APP>__SAML_SLS_BINDINGS=redirect,post
```

URI and method/binding declarations are paired. `POST_LOGOUT_REDIRECT_URIS`
is only a navigation allowlist and never replaces an OIDC notification
endpoint. A normal `/logout` page is not a back-channel endpoint. Protocol
switches, domain changes, and repeated apply must remove stale fields from the
other protocol or former domain.

### OIDC boundary

- Module-initiated logout uses the discovery `end_session_endpoint`, standard
  `id_token_hint`/`client_id`, a registered `post_logout_redirect_uri`, and a
  random verified `state`. The local application session is invalid before
  leaving the application.
- IAM-initiated logout prefers back-channel. The endpoint verifies Logout Token
  signature and algorithm, `iss`, `aud`, `events`, time, replay-safe `jti`, and
  `sid`/`sub`, then revokes only the target application session.
- A front-channel-only implementation documents browser, iframe,
  SameSite/CSP limitations and does not claim browserless administrative
  revocation.
- If the pinned upstream version has no standard notification endpoint, omit
  `OIDC_LOGOUT_*` and explicitly state “local logout only” or
  “Module-initiated logout only”. Never invent a guessed endpoint.

### SAML boundary

- SP-initiated logout uses the SLO URL from its binding/metadata, retains the
  login `NameID`, format, and `SessionIndex`, and verifies signature,
  Destination, Issuer, `InResponseTo`, status, and time.
- IdP-initiated logout is declared only when the pinned application version
  exposes a real SLS. Redirect SLS guarantees browser-mediated bidirectional
  logout only. Browserless administration requires real POST/back-channel
  support and a separate real E2E.
- When the Provider omits optional `SAML_SLO_URL`, clear stale configuration
  and perform local logout instead of calling a historical endpoint.

### Release gate

Bidirectional logout is a `Provider × protocol × Module` capability. Unit tests
cover field publication, protocol switching, missing pairs, and invalid values.
Real-container E2E retains the pre-logout application Cookie and verifies
Module-initiated logout, IAM-initiated logout, and—when claimed—browserless
administrative revocation. A 302, logout page, or token TTL is not evidence of
session invalidation.

A Module behind `oauth2-proxy` or ForwardAuth documents IAM-session, gateway
Cookie, and backend-application-session invalidation separately. Gateway
logout alone does not prove backend bidirectional logout.

## Management surfaces and local administrators

Every module with a management surface must classify the application's real
login topology before declaring `management.local_accounts`:

| Application capability | Module requirement |
| --- | --- |
| IAM-integrated; a local administrator can be configured; a real entry point can bypass IAM and accept that account | Implement a managed local administrator, normally `break_glass` |
| IAM-integrated; no configurable local administrator, or no entry point can bypass IAM | Do not declare a local account. State the missing capability and the real IAM-outage recovery procedure—or the absence of one—in the module README |
| Not IAM-integrated; the management UI uses native username/password login | Implement a managed local administrator, normally `primary` |
| Not IAM-integrated; no user database or human management login | Do not declare a local account. Document how the surface is protected or that no management login exists |

An `admin`-named setting alone is not sufficient. Database root users, LDAP
binds, API tokens, `svc_*` principals, and internal service credentials are
service identities or resource secrets, not local administrators. Never claim
`break_glass`, `apply`, or `rotate` without a real direct-login route, an
application password update API/CLI, and a verification path.

```yaml
management:
  surfaces:
    - id: web
      uri_from: EXAMPLE_DOMAIN_FULL
      authentication: {primary: iam}
    - id: local_recovery
      uri_from: EXAMPLE_BREAK_GLASS_URL
      authentication: {primary: local}
  local_accounts:
    - id: break_glass
      purpose: break_glass
      credential:
        policy: generated_per_module
        container_format: plaintext_on_bootstrap
      lifecycle:
        apply: apply-example-break-glass
        rotate: rotate-example-break-glass
```

The account `id` is the CLI `ACCOUNT`: a stable logical ID, never a username.
Purposes are `primary`, `break_glass`, and `embedded_guard`. Omission resolves
`primary`, then a sole account, otherwise ambiguity. Use the global username
Users cannot configure the username. A module declares `fixed_username` only
when upstream fixes the physical name; otherwise the Runner uses the immutable
ANAS default template `admin_{module}`. Once materialized, the username is
locked, and no rename command is provided.

Passwords persist only in the versioned `.anas/secrets.yml` store (0600), using
stable keys and owner/kind/provenance metadata. External import YAML may supply
a one-time bootstrap value, which is removed from the normalized workspace
config after a successful import. A module must not accept managed clear text
through argv, workspace YAML, `.env`, or a long-lived container environment.
Bootstrap-only applications use a mode-0600 runtime Secret file; hash-capable
applications publish only the hash. Ordinary deployment secrets such as DNS API
tokens are not lifecycle-managed and remain in managed configuration.

`apply` must write or reconcile the current Secret into application state and
verify the account. Setting an environment variable alone is insufficient.
`rotate` receives a candidate Secret, updates and verifies the application, and
only then permits the Runner to commit it. Failure must restore the previous
application credential; do not declare rotation when rollback is unreliable.
An IAM break-glass account must expose a genuine non-IAM entry point.

Every module README needs an “Administrator access” section covering IAM
status, local-account support, account ID and purpose, physical username and
its source, direct or recovery entry point, apply/rotate implementation, and
limitations. Whenever an emergency account exists, document an actionable
login address (a full URL, or an explicit `<DOMAIN_FULL>` plus fixed path), the
actual username, and the exact
`anas admin local credential <module> <account-id> -w <workspace>` command used
to retrieve its password; never publish the password value. Modules without a
local account must explicitly explain why and give the real IAM-outage recovery
path. Tests must cover invalid
manifests, clear text exclusion from deployment env/lock/manifest, explicit
sensitive reads, real apply, successful rotation, verification rollback, and
the direct entry point. Stable support requires a real-container test proving
the old password fails and the new password succeeds.

Current status is explicit: Authentik declares the fixed `akadmin`
`break_glass` account, and Traefik declares an ANAS-default `primary` account;
both have application apply/rotate handlers. MeshCentral now enforces OIDC-only
and rejects local and LDAP passwords in its central authenticator, so it has no
same-domain `break_glass` bypass. LAM's main login accepts enabled Samba `Admins`
group members using their own directory credentials; its module password only
protects the username-less configuration/profile editor. Group membership grants
LAM entry, while Samba AD ACLs and privileged groups still control directory writes.
Collabora owns independent module parameters and a generated Secret but still
lacks a verified transactional rotation handler.
LLNG uses AD and the directory administrator group; its unused password variable
was removed rather than mislabeled as `break_glass`. The CLI reports these as
unsupported instead of inventing handlers.

The shared `global.default_service_root_password` has been removed. Every human
account and service identity must use its owning module's parameter or an
independently generated Secret. A future bulk operation must invoke each
account handler independently and report per-account results; it must not
reintroduce a shared password.

## Supporting PostgreSQL and MariaDB

A consumer supporting both engines must integrate through the `relational_database` contract. It must not hard-code a dependency on either provider module or read provider administrator credentials. Its manifest must declare a `db_type` selector, both verified interfaces, a default, and a managed database resource:

```yaml
dependencies:
  contracts:
    - name: relational_database
      version: ">=1.0.0 <2.0.0"
      selected_by: db_type
      interfaces: [postgres, mariadb]
      default: postgres
resources:
  requires:
    - id: primary_database
      contract: relational_database
      binding: db_type
      spec_from: {name: db_name}
      spec:
        principal: example_app
        credential: {policy: generated}
        deletion_policy: retain
config:
  defaults:
    db_type: auto
    db_name: example_app
  changes:
    db_type:
      effect: data_migrate
      apply: migrate-example-app-database
```

Use `postgres`, `mariadb`, and `auto` as the public selector values; `mysql` is not a contract interface. After resolution, consume only the runner-owned `<PREFIX>_DB_TYPE`, `_DB_HOST`, `_DB_PORT`, `_DB_NAME`, `_DB_USERNAME`, `_DB_PASSWORD`, and `_NETWORK_DB` values. Join the selected provider's external network through `_NETWORK_DB`; do not attach both database networks or use cross-project `depends_on`.

The image must contain both required clients and application drivers. Translate the generic binding to any upstream-specific `POSTGRES_*` or `MYSQL_*` settings inside the consumer module. Provider `ensure` owns the database and dedicated user; application startup owns only its idempotent schema initialization and must not grant administrative privileges.

Unit tests must exercise both engine branches, dedicated credentials, and network mapping. Render and Compose tests must cover both interfaces across the matrix, and stable support requires a real-container test of schema initialization, restart, and idempotent re-apply. If upstream supports only one engine, declare only that verified interface.

## Upgrades and compatibility

Design a new Module's initialization, migration, and reconciliation to be idempotent: fresh installation, repeated `apply`, restart, and interruption retry must converge on the same state. Do not add a custom upgrade script when the upstream entrypoint or declarative configuration can perform the adaptation. A necessary script must inspect actual state, be safe to repeat, and declare its applicable versions and removal condition.

A later release may stop accepting sources from before the adaptation and remove the old script, preferably with an upstream major-version upgrade. Raise the `upgrade.from` lower bound in the same release. If the new release also writes disk data that older releases cannot read, list the breaking version in `upgrade.data_breaking`. Follow the [Module upstream upgrade SOP](/en/developer/module-upgrade-sop) for the decision and tests.

## Documentation, timezone, and language

Every module must maintain `README.md` and `localization.yml` matching the current `module.yml` version. Derive supported languages from pinned source, official documentation, or the exact image, record canonical BCP 47 values, and distinguish browser negotiation, deployment defaults, fixed language, and services without a UI.

Follow the [module documentation standard](/en/developer/module-documentation) for fields, fallback policy, and generation, and run the [module upstream upgrade SOP](/en/developer/module-upgrade-sop) for every upstream version change.
