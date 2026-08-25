# IAM multi-implementation and protocol capability design

## 1. Goals and hard constraints

ANAS should let application modules integrate with different IAM
implementations — LemonLDAP::NG (LLNG), Authentik, and Casdoor, for example —
without branching on the IAM's name inside an application hook.

> **Implementation status**: phases A–D have all landed. The Keycloak scaffold
> has been deleted from the repository, and switching `iam.provider` between
> `llng`, `authentik`, and `casdoor` requires no change to any consumer module.
> Deviations from the body of this document are collected in §12.

The design follows four hard constraints, which determine every trade-off below:

1. **Only an IAM providing both OIDC and SAML is accepted.** An implementation
   offering only one of the two is not eligible as an IAM provider.
2. **One deployment starts exactly one IAM.** Several simultaneously active IAMs
   are not supported.
3. **The provider can only be named explicitly by the user.** There is no
   default, no priority list, and no automatic inference.
4. **A module cannot choose the provider.** An application module can choose a
   protocol only, never which IAM serves it.

On top of that, the design must satisfy:

- IAM modules declare the protocols they provide in their manifests;
- application modules declare the protocols they can use and the preference
  order;
- the runner completes protocol resolution before startup, and a configuration
  error fails immediately during `plan`, `render`, `build`, or `start`;
- modules read the resolution result through unified environment variables, and
  depend on no implementation-private variables such as `LLNG_*` or
  `KEYCLOAK_*`;
- the selection enters the lock file, stays stable across restarts, and produces
  an explicit change prompt when the IAM is switched.

Constraints 1 and 2 together guarantee single sign-on semantics: every
application logs into the same session domain of the same IAM, and the user logs
in once. That is also the fundamental reason several IAMs are not allowed — two
IAMs maintain separate sessions and the user would be asked to log in twice,
unless IAM federation were introduced, and federation is out of scope here.

Constraint 1 also eliminates the entire class of failure where "the application
needs SAML but the provider only has OIDC": since any qualifying provider offers
both protocols, the protocol intersection is never empty.

### 1.1 Direct consequences of the eligibility condition

Authelia currently offers only OIDC and no SAML IdP, so under this design it is
**not eligible as an IAM provider** and is not on the migration path. Should it
offer a SAML IdP in future, it can be reassessed against §4.1's eligibility
condition, and no application module would need changing at that point.

The dual-protocol providers already implemented are LLNG, Authentik, and the
`developing` Casdoor. Keycloak also meets the eligibility condition, but the
scaffold that used to be in the repository has been deleted and it is out of
support.

These implementations cover two opposite endpoint models, which is not
coincidence but a selection criterion. Authentik binds an Application to a
Provider one to one, and its SAML endpoints hang under the application slug
(`/application/saml/<slug>/metadata/`, `/application/saml/<slug>/`, and so on),
with each provider getting its own EntityID; by default its OIDC also generates a
different issuer and discovery URL per application slug. LLNG's IdP endpoints are
a deployment-level singleton shared by every SP. Casdoor is a hybrid shape: a
singleton OIDC issuer and SAML SSO endpoint, with SAML metadata distinguished by
an Application query parameter. Only by supporting all of these shapes does the
contract become genuinely general.

It therefore **must not be assumed that IdP endpoints are a deployment-level
singleton**. The endpoint contract in §6.2 publishes per consumer to cover both
shapes; that is the second difference, beyond the eligibility condition, which
must be absorbed by the contract rather than by an implementation. The Authentik
path shapes above have not been verified against a running instance, and should
be rechecked against its current version's documentation at the first real
deployment.

## 2. Problems with the historical implementation

This section records the coupling that existed before the current IAM contract
was introduced. The names and lock paths are a historical baseline, not the
current configuration interface.

The `requires_one` of that time could only select one module from a static list
and wrote the binding into `module.lock.yml`. That suits a dependency such as
PostgreSQL/MariaDB where "the implementation name is the capability", but IAM
also had the following coupling:

1. A consumer had to list every implementation name, for example
   `providers: [authentik, llng]`; adding an IAM implementation meant editing
   every application manifest.
2. The `netbird` hook branched directly on the concrete provider name and read a
   provider-specific OIDC configuration endpoint.
3. `nextcloud` wrote the `SMAL_SP_*` variables LLNG uses by default, binding the
   protocol to the IAM implementation.
4. `features.sso_provider: true` could express only a boolean capability, not
   which of OIDC and SAML was actually supported.
5. Automatic selection with a default implementation would add and start an IAM
   with no explicit user intent, conflicting with constraint 3.

Continuing to extend IAM name branching is therefore not advisable. "Choosing an
implementation" and "choosing a protocol" have to be raised into a capability
binding the runner can validate, with implementation choice given entirely to the
user and protocol choice given to the application.

## 3. User configuration

A new top-level `iam` configuration:

```yaml
modules:
  nextcloud:
    identity:
      login_protocol: saml
  netbird:
    identity:
      login_protocol: auto

identity:
  iam:
    provider: llng
    default_protocol: oidc
```

The rules:

- `identity.iam.provider` is the deployment-level choice and takes an IAM module
  name; it is mandatory as soon as any IAM consumer exists, and has **no
  default**. The runner does not select the only candidate automatically.
- The selected IAM is added to the dependency closure by the runner, and the user
  does not have to list it in `modules` as well.
- `identity.iam.default_protocol` is optional, and is the deployment-level
  default for applications that do not name a protocol explicitly.
- `modules.<app>.config.iam_protocol` is an optional application-level override
  taking `oidc`, `saml`, or `auto`.
- **There is no application-level provider override.** Neither an application
  module nor the user configuration can make one application use an IAM other
  than `identity.iam.provider`.
- If `modules` also lists another IAM module explicitly, the runner errors.
- With no IAM consumer, `identity.iam.provider` is not started automatically. A
  user who really wants to start only the IAM can list that IAM in `modules` as
  well.

Overriding `identity.iam.provider` or `identity.iam.default_protocol` from the
host process environment is not allowed, or the same configuration file could
produce different deployments. An `anas plan --iam llng` could be added later for
a temporary trial calculation, but the persisted configuration remains the sole
source of truth.

### 3.1 Protocol resolution precedence

For each consumer, the final protocol is determined in the following order, and
the first rule that matches wins:

1. an explicit (non-`auto`) value of `modules.<app>.config.iam_protocol`;
2. `iam.default_protocol`, when it appears in that application manifest's
   `any_of`;
3. the first entry of that application manifest's `prefer` list.

Whichever rule applies, the result must fall inside that application's `any_of`,
or it is an error.

## 4. The module manifest capability model

This change introduces new manifest fields and raises the ABI to
`anas.module-hook/v1`. Because nothing has been formally released yet, there is no
v1/v2 dual reading: every module moved to v2 at once, and the runner recognizes
only v2.

### 4.1 IAM providers and the eligibility condition

LLNG, Authentik, and Casdoor all use the same capability declaration:

```yaml
abi:
  supports:
    - anas.module-hook/v1

capabilities:
  provides:
    - name: iam
      interfaces:
        - oidc
        - saml
```

The eligibility condition:

- `interfaces` must use lowercase protocol identifiers the runner knows; the
  first version recognizes only `oidc` and `saml`, and an unknown identifier
  fails at manifest load rather than being ignored silently.
- `interfaces` **must contain both `oidc` and `saml`**. A module declaring only
  one is rejected at manifest load, cannot register as an IAM provider, and
  cannot be selected by `iam.provider`.

The eligibility check sits at manifest load rather than at resolution so that
"this IAM is not eligible" is independent of the user's particular
configuration, and so the error message is more direct.

### 4.2 IAM consumers

A consumer declares only which protocols it can use, plus its preference order
for `auto`. **No manifest field can name a provider.**

NetBird accepts OIDC only:

```yaml
dependencies:
  requires_capabilities:
    - name: iam
      interface_selected_by: iam_protocol
      interfaces:
        any_of:
          - oidc
        prefer:
          - oidc
```

A consumer that accepts both protocols and prefers OIDC looks like this;
Nextcloud currently uses this declaration:

```yaml
dependencies:
  requires_capabilities:
    - name: iam
      interface_selected_by: iam_protocol
      interfaces:
        any_of:
          - oidc
          - saml
        prefer:
          - oidc
          - saml
```

The constraints:

- `any_of` is non-empty and means "match at least one", not "must use all of
  these protocols";
- `prefer` must be a subset of `any_of` and determines the `auto` selection
  order;
- `interface_selected_by` names an application parameter, mapping to for example
  `NEXTCLOUD_IAM_PROTOCOL`;
- the original design's `selected_by: global.iam.provider` field is deleted: the
  provider is always `iam.provider`, and an immutable fact does not need
  restating in the manifest;
- should a genuine "must support several interfaces at once" case appear later,
  `all_of` can be added then; the first version reserves no ambiguous semantics.
  Note that this would not be a local change: `all_of` would break both the
  single-value assumption of `ANAS_IAM_BINDING__<APP>__INTERFACE` and the
  list-partition invariant of §6.1.

IAM no longer uses `requires_one` with a static `providers` list. Existing
`requires_one` usages such as the database stay unchanged.

## 5. The runner's resolution algorithm

Resolution happens before hooks execute:

1. Read every module manifest and build a `capability -> provider -> interfaces`
   index, performing §4.1's dual-protocol eligibility check at this stage.
2. Collect the `requires_capabilities` of the enabled applications.
3. If an `iam` consumer exists, read `iam.provider`; if it is empty, error, and
   make no automatic selection.
4. Verify that the named module exists, is not disabled, and declares
   `provides: iam`.
5. Determine the protocol for each consumer by §3.1's precedence.
6. Verify the resulting protocol is in both `consumer.any_of` and
   `provider.interfaces`. Since the eligibility condition guarantees the provider
   has both protocols, this step can in practice only fail on an out-of-range
   explicit value on the application side, but it is kept as an invariant check.
7. On a validation failure, error immediately: run no hook, generate no secret,
   and write no runtime directory.
8. Add the IAM module to every consumer's dependency edges, so the IAM's
   `calculate` hook runs first.
9. **Before any hook runs**, inject the complete binding set:
   `ANAS_IAM_PROVIDER`, `ANAS_IDENTITY_CLIENTS`, the protocol-split
   `ANAS_IDENTITY_OIDC_CLIENTS` and `ANAS_IDENTITY_SAML_CLIENTS`, and each
   consumer's `ANAS_IAM_BINDING__<APP>__INTERFACE`.
10. Execute hooks in the existing order.
11. After a successful `start`, write the provider and each application's
    protocol binding into the lock file.

Step 9 is a precondition for the endpoint contract. For a provider with per-app
endpoints such as Authentik, its `calculate` must know the consumer list and each
one's protocol before it can derive each application's IdP endpoints (with the
slug taken from the module name). All of that information is resolved by steps
5–7, so the runner can publish it before the first hook, without changing the
`calculate` → `render_env` lifecycle order.

`plan` stays read-only but has to perform manifest-level capability resolution,
so it can report configuration errors early.

Error messages should name the consumer, the provider, both sides' protocols, and
the fix, for example:

```text
netbird requires IAM capability, but iam.provider is not set;
set iam.provider to one of: llng
```

```text
iam.provider "foo" does not provide capability "iam";
available providers: llng[oidc,saml]
```

```text
module "authelia" declares capability iam with interfaces [oidc];
an IAM provider must declare both oidc and saml
```

```text
netbird.iam_protocol is "saml", but netbird supports [oidc];
set netbird.iam_protocol to one of: oidc, auto
```

## 6. The unified environment variable contract

Before any hook, the runner publishes deployment-level read-only variables:

```dotenv
ANAS_IAM_PROVIDER=llng
ANAS_IAM_INTERFACES=oidc,saml

ANAS_IDENTITY_CLIENTS=nextcloud,netbird
ANAS_IDENTITY_LDAPS_CLIENTS=nextcloud
ANAS_IDENTITY_OIDC_CLIENTS=netbird
ANAS_IDENTITY_SAML_CLIENTS=nextcloud
ANAS_IDENTITY_CLIENT__NEXTCLOUD__INTERFACES=ldaps,saml
ANAS_IDENTITY_CLIENT__NETBIRD__INTERFACES=oidc

ANAS_IAM_BINDING__NETBIRD__INTERFACE=oidc
ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE=saml
```

This set contains only facts the runner resolved itself. Values derived from the
IAM's domain, such as `ANAS_IAM_PORTAL_URL`, are not among them; they are a
product of the provider's `calculate` per §6.2 — the runner does not know the
provider's domain before hooks run.

The binding contains no `__PROVIDER`: in a single-IAM deployment it is always
equal to `ANAS_IAM_PROVIDER`, and publishing it twice would only create two
sources of truth that can disagree.

### 6.1 The consumer list split by protocol

`ANAS_IDENTITY_<PROTOCOL>_CLIENTS` is the protocol projection of
`ANAS_IDENTITY_CLIENTS`. The runner writes all of these out in one pass from the
direct protocol declarations and §5's IAM resolution result, and modules only
read them, so they cannot drift apart.

The reason for splitting is not "otherwise the protocol is unavailable" — the
protocol can already be looked up one by one through
`ANAS_IAM_BINDING__<APP>__INTERFACE`. The real benefit is making **"this
deployment has no SAML consumer" a first-class, directly testable condition**
(`ANAS_IDENTITY_SAML_CLIENTS` is empty), which is exactly the condition §6.3's
endpoint validation relies on. Two uses of one fact deserve one expression of it,
or every provider module would have to scan the list itself to decide whether to
generate a SAML configuration section. For an implementation such as Authentik
that creates Application/Provider objects per protocol, the split lists map 1:1
directly.

The flat `ANAS_IDENTITY_CLIENTS` is kept because protocol-independent consumption
exists — the LLNG portal's application launcher needs to list every application,
for instance.

**Invariant: the OIDC and SAML lists form a partition of the IAM consumer set** —
each IAM consumer appears in exactly one list, because §4.2 states that one
application binds one IAM protocol. The full identity-protocol lists may overlap;
Nextcloud, for example, appears in both the LDAPS and the SAML list. Should
`all_of` later allow one application to use both protocols, the partition would
degrade into a cover and `__INTERFACE` would no longer be single-valued; this
section and §6.3 would both have to be redesigned at that point, and cannot be
papered over by "just adding one more list".

### 6.2 Endpoints are published per consumer

IdP endpoints are produced by the provider's `calculate` hook and **must be
published per consumer**, not as a deployment-level singleton:

```dotenv
ANAS_IAM_BINDING__NETBIRD__OIDC_ISSUER_URL=https://auth.nas.example.com/application/o/netbird/
ANAS_IAM_BINDING__NETBIRD__OIDC_DISCOVERY_URL=https://auth.nas.example.com/application/o/netbird/.well-known/openid-configuration

ANAS_IAM_BINDING__NEXTCLOUD__SAML_METADATA_URL=https://auth.nas.example.com/application/saml/nextcloud/metadata/
ANAS_IAM_BINDING__NEXTCLOUD__SAML_ENTITY_ID=https://auth.nas.example.com/application/saml/nextcloud/metadata/
ANAS_IAM_BINDING__NEXTCLOUD__SAML_SSO_URL=https://auth.nas.example.com/application/saml/nextcloud/
ANAS_IAM_BINDING__NEXTCLOUD__SAML_SLO_URL=https://auth.nas.example.com/application/saml/nextcloud/
```

This is the only contract covering both endpoint shapes from §1.1: for LLNG and
Keycloak, every consumer receives the same singleton value repeated; for
Authentik, the values genuinely differ per consumer. Defining the endpoints as a
deployment-level singleton instead would hard-code the LLNG/Keycloak shape into a
"general" contract, and the first Authentik module would force consumers to
change code, violating decision 6.

A provider that really does have deployment-level singleton endpoints **may
additionally** publish global variables such as `ANAS_IAM_OIDC_ISSUER_URL` as a
convenience, but consumer hooks must not read them. An application reads only its
own `ANAS_IAM_BINDING__<APP>__*`. NetBird, for example, no longer has a
`switch keycloak/llng`; it verifies that `ANAS_IAM_BINDING__NETBIRD__INTERFACE`
is `oidc` and then reads
`ANAS_IAM_BINDING__NETBIRD__OIDC_DISCOVERY_URL`.

### 6.3 Endpoint validation

After the provider hook returns, the runner validates the required variables
**per consumer according to the actual binding**, iterating exactly over §6.1's
two protocol lists:

- for each consumer in `ANAS_IDENTITY_OIDC_CLIENTS`: `..._OIDC_ISSUER_URL`,
  `..._OIDC_DISCOVERY_URL`;
- for each consumer in `ANAS_IDENTITY_SAML_CLIENTS`: `..._SAML_METADATA_URL`,
  `..._SAML_ENTITY_ID`, `..._SAML_SSO_URL`; `..._SAML_SLO_URL`,
  `..._SAML_SLO_RESPONSE_URL`, and `..._SAML_SIGNING_CERT` are optional, though
  an SP that wants to verify assertion signatures needs `SAML_SIGNING_CERT`.

`SAML_SIGNING_CERT` carries the public certificate only. **The signing private
key does not enter the contract** — a contract demanding that the provider hand
over its private key is unimplementable for an implementation such as Authentik
that manages its own keys, which is precisely the mistake of writing one
implementation's shape into a "general" contract. The SP's own key pair is
generated by the SP, and the provider retrieves its certificate from the SP
metadata.

When a list is empty, that protocol takes no part in validation. Only bound
protocols are validated and unused ones are not, because on a per-app-endpoint
provider there is simply no such thing as "a SAML endpoint independent of a
particular application" — a SAML endpoint does not exist until some application's
provider object has been created, so requiring unconditional publication would be
unsatisfiable.

The manifest-level dual-protocol eligibility check (§4.1) and the runtime
per-binding validation each do their own job: the former guarantees this IAM is
**capable** of serving an application of any protocol in future, the latter
guarantees this deployment is **actually** usable. A missing required variable
fails immediately after the provider hook.

### 6.4 Client registration requests

A consumer remains responsible for generating its own client secret and callback
addresses, but moves to the general namespace. The list variables
(`ANAS_IDENTITY_CLIENTS`, `ANAS_IDENTITY_OIDC_CLIENTS`,
`ANAS_IDENTITY_SAML_CLIENTS`) are already published by the runner per §6, and a
consumer only supplies its own fields; it must not append to or rewrite a list:

```dotenv
ANAS_IAM_CLIENT__NETBIRD__INTERFACE=oidc
ANAS_IAM_CLIENT__NETBIRD__CLIENT_ID=netbird
ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET=...
ANAS_IAM_CLIENT__NETBIRD__REDIRECT_URIS=https://netbird.example/auth,...
ANAS_IAM_CLIENT__NETBIRD__POST_LOGOUT_REDIRECT_URIS=https://netbird.example
ANAS_IAM_CLIENT__NETBIRD__OIDC_LOGOUT_URI=https://netbird.example/oidc/backchannel-logout
ANAS_IAM_CLIENT__NETBIRD__OIDC_LOGOUT_METHODS=backchannel,frontchannel
ANAS_IAM_CLIENT__NETBIRD__OIDC_LOGOUT_SESSION_REQUIRED=true
ANAS_IAM_CLIENT__NETBIRD__SCOPES=openid,profile,email,groups
ANAS_IAM_CLIENT__NETBIRD__ALLOW_GROUPS=APP_netbird,APP_all,Admins
```

A SAML client uses the same prefix and publishes `SP_METADATA_URL`,
`SP_ENTITY_ID`, `ACS_URL`, and `NAME_ID_FORMAT`; when Single Logout is supported
it additionally publishes `SAML_SLS_URL` and `SAML_SLS_BINDINGS=redirect,post`.
`POST_LOGOUT_REDIRECT_URIS` only names the addresses it is allowed to return to
after an IAM logout, and cannot substitute for an OIDC front/back-channel
endpoint. Both protocols share `ALLOW_GROUPS`, `DOMAIN`, and `ATTRIBUTES`;
`ATTRIBUTES` is a comma-separated list of `name:source:required`, which the
provider translates into its own attribute mapping (LLNG expands it into
`ATTR01`/`ATTR02`…).

The provider's `render_env` hook reads every general registration request and
translates it into LLNG's, Authentik's, or Casdoor's private configuration: LLNG
writes `OIDC_RP_*` / `SAML_SP_*` for its configuration scripts to consume,
Authentik generates a declarative blueprint creating Application and Provider
objects, and Casdoor generates managed init data. Private variables must no
longer be generated by an application module.

Because the current lifecycle completes every `calculate` first and then runs
every `render_env`, this direction is compatible with the existing hook order and
creates no circular dependency with §6.2's per-app endpoints:

1. the runner publishes the consumer list and protocol bindings first (§5 step 9);
2. the provider derives each consumer's IdP endpoints from that during
   `calculate`;
3. an application reads the endpoints during its own `calculate` and publishes a
   registration request;
4. the provider reads the complete registration requests during `render_env` and
   generates the private configuration.

Endpoints depend only on the consumer list and the protocols (both known to the
runner), and registration requests depend on endpoints, so the order is one-way
and forms no cycle.

After every `calculate` completes and before the provider's `render_env`, the
runner validates the logout declarations: an OIDC method and `OIDC_LOGOUT_URI`
must come in pairs, the method may only be `backchannel`/`frontchannel`, and
session-required may only be a boolean; a SAML SLS URL and its binding must come
in pairs, and the binding may only be `redirect`/`post`. An empty declaration
still means the application does not support IAM-initiated immediate logout, and
the provider must not guess an ordinary `/logout` page to be a standard
notification endpoint. Environment ownership continues to belong to the consumer,
and only the selected provider, which consumes `ANAS_IAM_CLIENT_*` explicitly,
receives these fields at render time.

## 7. The lock file and switching semantics

The provider is deployment-level and unique, so the lock file records it once and
records the protocol per application:

```yaml
iam:
  provider: llng
bindings:
  nextcloud:
    iam.interface: saml
  netbird:
    iam.interface: oidc
```

If the current `map[string]map[string]string` structure is kept, a composite key
can continue to be used during the transition, but a structured record is
recommended in the long run.

Switching the configuration from LLNG to Keycloak, or an application from SAML to
OIDC, should not be treated as an ordinary container restart. `iam.provider` and
the manifest parameter `iam_protocol` should be marked `reconcile`: generate the
new client configuration first, validate the callback addresses and the secret,
then switch the application, and stop the old IAM last. If the first version has
no automatic reconciliation yet, an ordinary `start` must follow the existing
configuration-change protection and prompt for an explicit reconcile rather than
switching silently.

## 8. Migration steps

### Phase A: the runner and the ABI

- add the v2 manifest structures, the capability index, the dual-protocol
  eligibility check, and protocol resolution;
- add the top-level `iam.provider` and `iam.default_protocol`;
- inject the binding environment variables and extend the lock file;
- have `plan` output `app -> interface` rather than only a startup order;
- keep the v1 database `requires_one` behavior.

### Phase B: LLNG and the existing applications

- upgrade LLNG to v2, declare `iam[oidc,saml]`, and publish endpoints per
  consumer (LLNG's endpoints are a deployment-level singleton, so every consumer
  receives the same value);
- change NetBird to consume only the general OIDC variables and delete the
  `NETBIRD_SSO_PROVIDER` branch;
- migrate Nextcloud's `SMAL_SP_*` (whose existing spelling should also be
  corrected into an internal SAML mapping) to general client registration
  requests, and stop reading `LLNG_SAML_*`;
- provide a one-time lock file migration for existing LLNG deployments, keeping
  the original protocol by default and changing no secret.

### Phase C: a second dual-protocol IAM

Authentik was chosen over Keycloak because it has the per-app endpoint shape and
genuinely exercises §6.2's per-consumer endpoint contract. Had the second IAM also
had the singleton endpoint shape (Keycloak being of the same kind as LLNG), the
"endpoints may vary per consumer" dimension of the contract would have been
covered by no test at all, and the defect would have surfaced only later.

- add the Authentik module, declaring `iam[oidc,saml]`;
- derive each application's SAML/OIDC endpoints in `calculate` from the consumer
  list the runner published;
- generate the SAML signing key pair itself, so `SAML_SIGNING_CERT` can be
  published during `calculate` instead of waiting for Authentik to generate one
  at first startup;
- read the general client registration requests and generate Authentik's
  Application and Provider blueprints.

### Phase D: clearing out private coupling

- forbid application hooks from reading IAM-private endpoints such as `LLNG_*`,
  `KEYCLOAK_*`, and `AUTHENTIK_*`;
- forbid application hooks from reading §6.2's deployment-level convenience
  endpoint variables. This check must enumerate variable names exactly
  (`ANAS_IAM_OIDC_ISSUER_URL`, `ANAS_IAM_OIDC_DISCOVERY_URL`,
  `ANAS_IAM_SAML_METADATA_URL`, `ANAS_IAM_SAML_ENTITY_ID`,
  `ANAS_IAM_SAML_SSO_URL`, `ANAS_IAM_SAML_SLO_URL`), and **must not match on the
  `ANAS_IAM_OIDC_*` / `ANAS_IAM_SAML_*` prefix**, which would also catch the
  `ANAS_IDENTITY_OIDC_CLIENTS` and `ANAS_IDENTITY_SAML_CLIENTS` that §6.1 allows
  to be read;
- add manifest/source static tests preventing implementation-name branches from
  being reintroduced;
- remove the `features.sso_provider` / `features.sso_client` boolean fields, with
  capability information expressed uniformly through `capabilities`.

## 9. Required tests

Runner unit tests:

- `llng + nextcloud(auto)` selects OIDC;
- `llng + nextcloud(saml)` selects SAML;
- `llng + netbird(auto)` selects OIDC;
- with `iam.default_protocol: saml`, `nextcloud(auto)` selects SAML while the
  OIDC-only `netbird(auto)` still selects OIDC;
- `netbird(saml)` fails during `plan` for falling outside its own `any_of`;
- failing when `iam.provider` is unset while consumers exist, without selecting
  automatically just because there is one candidate;
- failing when the named module does not exist, is disabled, or does not provide
  the IAM capability;
- an IAM module declaring only `oidc` is rejected at manifest load;
- failing when the provider hook did not publish
  `ANAS_IAM_BINDING__<APP>__SAML_METADATA_URL` for a SAML-bound consumer;
- the inverse case: with no SAML consumer in this deployment,
  `ANAS_IDENTITY_SAML_CLIENTS` is empty and a provider publishing no SAML
  endpoint at all must still succeed, because validation covers only bound
  protocols;
- the list-partition invariant: `ANAS_IDENTITY_OIDC_CLIENTS` and
  `ANAS_IDENTITY_SAML_CLIENTS` are disjoint and their union equals the IAM
  consumer set;
- the protocol lists follow the resolution: when `nextcloud` moves from `saml` to
  `auto` with `iam.default_protocol: oidc`, it moves from the SAML list to the
  OIDC list;
- the per-app endpoint case: fake a provider publishing different endpoints per
  consumer, and verify each application reads its own endpoints rather than
  another's;
- a consumer hook reading the deployment-level `ANAS_IAM_OIDC_*` instead of its
  own binding endpoints is caught by a static test;
- failing when two IAMs are listed explicitly among the startup modules;
- failing when a provider-selection field appears in a manifest, preventing
  constraint 4 from being bypassed;
- the lock file stably retains the provider and each application's interface;
- an explicit change to `iam.provider` or `iam_protocol` is caught by the
  configuration lifecycle protection.

The integration test matrix:

| IAM | Declared protocols | Endpoint shape | NetBird | Nextcloud OIDC | Nextcloud SAML |
| --- | --- | --- | --- | --- | --- |
| LLNG | OIDC, SAML | Deployment-level singleton | Passes | Not applicable | Passes |
| Authentik | OIDC, SAML | Per-app | Passes | Not applicable | Passes |
| Casdoor | OIDC, SAML | Hybrid: singleton issuer/SSO, per-app metadata | Unit/render integrated, E2E outstanding | Unit/render integrated, E2E outstanding | No SLO; login E2E outstanding |

The matrix must cover both endpoint shapes, or §6.2's per-consumer endpoint
contract is in effect unverified. Casdoor stays `developing` until the permanent
directory anchor, `ALLOW_GROUPS`, account-deactivation propagation, and a real
client login/revocation flow pass acceptance; dual-protocol manifest eligibility
by itself does not mean those directory and authorization semantics are
qualified. The "Nextcloud OIDC" column is not applicable for the reason in §12.

Tests should not merely check that the container is running; they should request
OIDC discovery / SAML metadata, complete one redirect flow, and confirm the
corresponding client/SP configuration was created in the IAM.

## 10. Key decisions

1. **The runner does the selecting.** Module environment variables are for
   consuming the resolution result, not for each module to scan `LLNG_*` and
   `KEYCLOAK_*` and guess at the provider.
2. **The provider is deployment-level and unique; the protocol is
   application-level.** One IAM serves both OIDC and SAML applications, and the
   user logs in once.
3. **The provider must be named explicitly.** No default is provided, and a sole
   candidate is not selected automatically.
4. **An IAM must be dual-protocol.** The eligibility condition runs at manifest
   load, eliminating protocol mismatch at the root.
5. **Endpoints are published per consumer, not as a deployment-level
   singleton.** Whether IdP endpoints vary per application is an implementation
   difference (LLNG/Keycloak singleton, Authentik per-app) and must be absorbed
   by the contract. The singleton shape is a special case of per-app and not the
   reverse, so the contract takes the more general one.
6. **Adding an IAM changes no consumer.** As long as a new module meets the
   eligibility condition and implements the unified environment contract,
   existing applications can select it.
7. **No silent switching.** The IAM and the protocol bindings are written into
   the lock file, and a change enters the reconcile flow.

## 11. Explicitly out of scope

The following capabilities are deliberately excluded, with the reasons recorded
so they are not reintroduced later as oversights:

- **Several simultaneously active IAMs.** There is no shared session across IAMs,
  the user would log in twice, and it defeats the purpose of SSO. Should a real
  need arise, the correct direction is IAM federation (one IAM upstream of
  another), not several active providers side by side.
- **Naming a provider at the module or application level.** Allowing it is
  equivalent to several IAMs, causes the same repeated logins, and makes
  `iam.provider` no longer a trustworthy deployment-level fact.
- **A provider priority list, or a default provider per protocol.** These
  mechanisms are meaningful only with several IAMs.
- **A single-protocol IAM.** Accepting one reintroduces the whole empty-protocol-
  intersection failure path and a user-visible combination matrix.
- **Overriding the IAM selection through host environment variables.** The same
  configuration file must produce the same deployment.

## 12. Deviations between the implementation and this document

Implementation found three places where the body disagreed with reality; the
conclusions and the reasons are recorded here.

### 12.1 Nextcloud gained OIDC, with SAML kept as a fallback

Nextcloud is now integrated with the official `user_oidc` and actually declares
`any_of: [oidc, saml]`, preferring OIDC. OIDC uses the authorization code flow,
and `preferred_username` aligns with LDAP's `sAMAccountName` internal username;
`auto_provision=false`, so the IAM only authenticates while users and groups are
still managed by the LDAPS backend. The existing `user_saml` path is retained and
is enabled only on an explicit `iam_protocol: saml`. The runner's
provider-neutral capability resolution needs no special case for this addition.

MeshCentral joined at the same time as an OIDC-only IAM capability: OIDC handles
authentication and the administrator group claim, while LDAPS continues to handle
directory synchronization. The complete authorization code path of both consumers
is verified by server end-to-end tests.

### 12.2 SP key ownership changed

Before the migration Nextcloud read `LLNG_SAML_SERVICE_PRIVATE_KEY` directly,
using the IdP's private key as its own SP private key. A general contract cannot
do that: it would require every IAM to hand over its signing private key, which
an implementation such as Authentik cannot satisfy.

Nextcloud now generates its own SP key pair and the IdP publishes only the public
certificate (`SAML_SIGNING_CERT`). LLNG already fetches SP metadata with `curl`
and picks up the new SP certificate automatically. **This is the only place in
this change that altered runtime key material, and one SAML login should be
verified on a real deployment.**

### 12.3 `ANAS_IAM_PORTAL_URL` is not published by the runner

§6 originally listed it among the runner's deployment-level variables, but the
runner does not know the provider's domain before hooks run. It is produced by the
provider's `calculate`, and the body has been corrected.

## 13. Deployment environment notes

The following two points come from a real server deployment. Neither concerns the
design, but both can fail a deployment or cause a misdiagnosis.

### 13.1 samba_dc's :53 does not conflict with systemd-resolved

`samba_dc` uses `network_mode: host`, which easily suggests it will occupy the
host's `0.0.0.0:53`. The actual configuration is:

```
listen-on port 53 { 127.0.0.1; ${SAMBA_DC_DNS_SERVER}; };
```

It binds only `127.0.0.1` and the address named by `SAMBA_DC_DNS_SERVER`, while
systemd-resolved's stub binds `127.0.0.53` and `127.0.0.54`. They do not conflict,
and **DNSStubListener does not need to be disabled**.

Conversely, disabling the stub listener severs outbound connectivity on a host
using a transparent proxy (something like v2raya): such proxies rely on
hijacking `127.0.0.53` to split traffic, and once bypassed, TLS handshakes to
blocked domains are reset. The symptom is every `curl` returning 000, which is
easily misdiagnosed as a network fault.

### 13.2 ghcr.io images need separate handling

`registry-mirrors` in `daemon.json` **applies only to docker.io**. A ghcr.io
manifest can be resolved through a mirror, but the blobs redirect to
`pkg-containers.githubusercontent.com`, a domain outside the mirror's proxy scope.

On a network that cannot reach ghcr.io directly, pull by the mirror's domain and
retag to the original name; no Dockerfile needs changing:

```sh
docker pull <mirror>/goauthentik/server:2024.10.5
docker tag  <mirror>/goauthentik/server:2024.10.5 ghcr.io/goauthentik/server:2024.10.5
```

The images that currently need this: `linuxserver/baseimage-ubuntu` (both noble
and jammy), `ylianst/meshcentral`, and `goauthentik/server`.
