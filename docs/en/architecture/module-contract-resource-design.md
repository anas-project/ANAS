# Module, Contract, and Resource architecture

Status: implementation baseline
Date: 2026-08-22

## 1. Goals

ANAS separates the deployment unit, the cross-module protocol, and the external
persistent resource into three distinct concepts:

- A **Module** is the deployment unit a user selects, installs, upgrades,
  starts, stops, and releases.
- A **Contract** is an independently versionable interoperation protocol between
  modules. It is not a container or a daemon.
- A **Resource** is a persistent object a consumer module requests under a
  contract and a provider module manages.

The runner is responsible for resolving bindings, persisting secrets and
resource state, invoking provider operations, and running the module lifecycle.
The runner does not implement the specifics of PostgreSQL, MariaDB, ACME, or IAM.

This design lands `relational_database` and `object_storage` first, and reserves
the same extension model for later contracts such as `certificate` and
`identity`.

## 2. Invariants

1. Adding a database consumer restarts neither the database provider nor any
   existing consumer.
2. An application's long-lived containers receive only their own
   least-privilege credentials.
3. Database administrator credentials enter only a one-shot provider operation.
4. A provider operation must be idempotent; a repeated `ensure` must not damage
   existing data.
5. Removing a module retains its persistent resources by default; destroying a
   resource must be explicit.
6. A contract contains no provider dialect; the concrete implementation ships
   with the provider module.
7. A consumer depends on no specific provider's files, images, SQL, or
   environment variables.
8. A module can be released independently, and its provider implementation must
   be contained in the same module package.
9. Contract versions and digests, provider bindings, and resource identities are
   all written into the lock/deployment state.
10. An unchanged module runs no `compose up` during an apply, and does not have
    its runtime artifacts replaced.

## 3. Source and release layout

The ANAS core source:

```text
modules/
  <module>/
    module.yml
    compose.yml
    hook/
    providers/
      <contract>/
        provider.yml
        <operation assets>

contracts/
  <contract>/
    contract.yml
    schemas/

internal/runner/
  module loading and lifecycle
  contract loading and validation
  binding resolution
  resource lifecycle and state
  operation execution
```

An independently released module is a self-describing directory or archive:

```text
postgres-18.4.0-r1/
  module.yml
  compose.yml
  providers/
    relational_database/
      provider.yml
      ensure.sh
      inspect.sh
      rotate.sh
      delete.sh
```

A module package does not copy the official contract. It declares only the
contract name, the compatible versions, and the implementation entry point. The
provider implementation ships with the module, which keeps a PostgreSQL module
version from drifting against an external SQL script.

The official contracts ship with the ANAS core. Independent contract packages and
a registry may be added later, but the initial implementation requires no network
registry.

## 4. Module

### 4.1 Definition

A module is the only thing that enters the ordinary deployment dependency graph
and the `start/stop/restart` lifecycle. For example:

- `postgres`
- `mariadb`
- `nextcloud`
- `authentik`
- `llng`
- `lego`
- `traefik`

A one-shot database provisioner is not a module, because it has no independent
user-facing desired state and should not become a shared singleton in the
start/stop dependency graph.

### 4.2 Module manifest

```yaml
api_version: anas.module/v1
kind: Module
name: nextcloud
version: 34.0.2
revision: 1

runtime:
  type: compose
  compose_file: compose.yml

dependencies:
  requires:
    - traefik
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
      spec_from:
        name: db_name
      spec:
        principal: nextcloud
        credential:
          policy: generated
        deletion_policy: retain
```

`resources.requires[].id` is unique within one consumer module. The
deployment-level resource identity is:

```text
<consumer-module>.<resource-id>
```

For example, `nextcloud.primary_database`.

`spec_from` resolves a module parameter into the final resource request. The
example above lets `modules.nextcloud.config.db_name` change the database name;
the provider receives only the resolved `spec.name` and never reads Nextcloud's
private configuration.

### 4.3 Provider module

```yaml
api_version: anas.module/v1
kind: Module
name: postgres
version: 18.4.0
revision: 1

contracts:
  provides:
    - name: relational_database
      version: 1.0.0
      interface: postgres
      implementation: providers/relational_database/provider.yml
```

A provider's ordinary compose services still belong to the module lifecycle;
provider operations run on demand and do not join the ordinary service list.

### 4.4 User configuration

User configuration uses `modules` only, and no longer `services.<module>.env`:

```yaml
modules:
  postgres:
    enabled: true
    config:
      adminer_enabled: false

  nextcloud:
    enabled: true
    config:
      db_type: postgres
      domain_prefix: nc
```

`services.nextcloud` was in fact configuring a module. Removing that name avoids
confusion with compose `services` and with the notion of a capability service.

### 4.5 Built-in module inventory

Every directory is a module that can be packaged on its own. The "protocol role"
describes only the current implementation, and claims no false compatibility for
a placeholder contract that has not been migrated.

<!-- generated:builtin-module-inventory:start -->
| Name | Category | Status | Description |
| --- | --- | --- | --- |
| [`authentik`](/en/reference/modules/authentik/) | `identity` | `developing` | Identity provider serving OIDC and SAML with per-application endpoints. |
| [`casdoor`](/en/reference/modules/casdoor/) | `identity` | `release` | Release IAM provider serving OIDC and SAML with Samba AD-backed sign-in. |
| [`collabora`](/en/reference/modules/collabora/) | `app` | `release` | Online document editing backend for Nextcloud. |
| [`ddns_go`](/en/reference/modules/ddns_go/) | `network` | `release` | Dynamic DNS updater with first-class IPv6 and Chinese DNS vendor coverage. |
| [`ddns_updater`](/en/reference/modules/ddns_updater/) | `network` | `release` | Dynamic DNS updater for the base domain and wildcard host. |
| [`eturnal`](/en/reference/modules/eturnal/) | `communication` | `release` | TURN service used by realtime communication modules. |
| [`forgejo`](/en/reference/modules/forgejo/) | `app` | `developing` | Self-hosted Git collaboration with HTTP/SSH access, Git LFS, packages, and OIDC authentication. |
| [`freeradius`](/en/reference/modules/freeradius/) | `network` | `developing` | RADIUS server module scaffold. |
| [`lam`](/en/reference/modules/lam/) | `identity` | `release` | Web UI for LDAP account administration. |
| [`lego`](/en/reference/modules/lego/) | `certificate` | `release` | Issues and stores wildcard certificates used by Traefik and domain services. |
| [`llng`](/en/reference/modules/llng/) | `identity` | `release` | SSO portal, SAML/OIDC identity provider, and app launcher. |
| [`mariadb`](/en/reference/modules/mariadb/) | `database` | `release` | MariaDB database service with optional Adminer UI. |
| [`meshcentral`](/en/reference/modules/meshcentral/) | `app` | `release` | Remote device management with OIDC-only authentication and LDAP directory synchronization. |
| [`netbird`](/en/reference/modules/netbird/) | `network` | `developing` | Incomplete WireGuard overlay network scaffold; excluded from recommended deployments. |
| [`nextcloud`](/en/reference/modules/nextcloud/) | `app` | `release` | File sync, sharing, office integration, memories, and Talk. |
| [`oauth2_proxy`](/en/reference/modules/oauth2_proxy/) | `identity` | `release` | Authenticated gate in front of services that have no login of their own. |
| [`postgres`](/en/reference/modules/postgres/) | `database` | `release` | PostgreSQL database service with optional Adminer UI. |
| [`samba_dc`](/en/reference/modules/samba_dc/) | `identity` | `release` | Active Directory compatible domain controller, LDAP source, and BIND9-DLZ DNS server. |
| [`samba_fs`](/en/reference/modules/samba_fs/) | `storage` | `release` | File sharing service joined to the Samba domain. |
| [`traefik`](/en/reference/modules/traefik/) | `network` | `release` | HTTPS reverse proxy and dashboard for all web-facing services. |
| [`versitygw`](/en/reference/modules/versitygw/) | `storage` | `developing` | S3-compatible API backed by a dedicated POSIX directory. |
| [`vikunja`](/en/reference/modules/vikunja/) | `app` | `developing` | Task and project management with OIDC-only authentication, multiple views, API, webhooks, and CalDAV. |
<!-- generated:builtin-module-inventory:end -->

### 4.6 The logout boundary for IAM consumers

For an OIDC/SAML consumer, both login and logout belong to the interoperation
boundary between the module and the IAM capability. The core only resolves the
effective interface, validates the common registration fields, and hands the
declaration to the selected provider; how an application session is created,
correlated, and revoked remains owned by the consumer module.

A module must express the following through the provider-neutral
`ANAS_IAM_BINDING__<APP>__*` and `ANAS_IAM_CLIENT__<APP>__*`:

- module → IAM: OIDC RP-Initiated Logout or SAML SP-Initiated SLO;
- IAM → module: OIDC front-/back-channel logout or SAML IdP-Initiated SLO;
- navigation callbacks and application session notification endpoints are two
  different kinds of field;
- an empty supported-method set means the pinned upstream version does not
  support IAM-initiated immediate logout, and neither the provider nor the core
  may guess at a path;
- whether bidirectional logout holds must be verified per
  `provider × protocol × module` with a real application session.

The core does not know Nextcloud's, MeshCentral's, or some plugin's logout URL,
and does not validate a logout token or a SAML message; those are the
module's/upstream application's protocol implementation. The core can
mechanically check that a URI and its method/binding come in pairs, that
enumerations are valid, that the fields belong to the current binding, and that
only the selected provider receives the registration request. The detailed
fields, security, and release gates are in
[bidirectional logout requirements for modules using OIDC/SAML](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/module-iam-bidirectional-logout.md)
and the [module development standard](/en/developer/module-development).

## 5. Contract

### 5.1 Why it is necessary

What a contract solves is interoperation between independently released modules,
not code reuse. Without contracts:

- a consumer must know every concrete provider;
- the runner accumulates PostgreSQL, MariaDB, ACME, and IAM dialect branches;
- a third-party provider cannot declare compatibility;
- requests, results, secret boundaries, and lifecycles cannot be validated
  uniformly.

A contract is created only for a capability that satisfies all of the following:

1. independent providers and consumers exist;
2. there may be several providers;
3. consumers should not depend on a concrete implementation;
4. the runner must orchestrate a resource or protocol lifecycle.

Internal helper scripts, log directories, and single-module implementation
details get no contract.

### 5.2 Contract manifest

```yaml
api_version: anas.contract/v1
kind: Contract
name: relational_database
version: 1.0.0

interfaces: [postgres, mariadb]

resource:
  schema: schemas/resource.yml
  identity: [consumer, resource_id]

operations:
  ensure:
    request_schema: schemas/ensure-request.yml
    result_schema: schemas/connection-result.yml
    required: true
  inspect:
    request_schema: schemas/inspect-request.yml
    result_schema: schemas/inspect-result.yml
    required: true
  rotate_credential:
    request_schema: schemas/rotate-request.yml
    result_schema: schemas/connection-result.yml
    required: false
  delete:
    request_schema: schemas/delete-request.yml
    result_schema: schemas/delete-result.yml
    required: false
```

### 5.3 Contract versions

- Patch: documentation and diagnostic corrections that do not change the machine
  protocol.
- Minor: adding an optional field, an optional result, or an optional operation.
- Major: removing or renaming a field, changing semantics, adding a required
  field, or changing an idempotency guarantee.

A consumer declares an accepted range, a provider declares one concrete
implementation version, and the lock pins the version and the content digest. A
contract schema version change decides protocol compatibility only; real resource
migration is still performed by provider operations.

### 5.4 The contract set

- `relational_database`: implemented.
- `object_storage`: per-resource bucket, principal, credential,
  ensure/inspect/rotate, and retain are implemented.
- `certificate`: only a placeholder schema for now; certificate request,
  inspection, renewal, and revocation will be defined later, implemented by
  `lego` and others.
- `identity`: only a placeholder schema for now; the authentication interface,
  client registration, and metadata will be defined later, converging the
  existing IAM capability.

`directory` (directory query, synchronization, and identity anchor) and
`dns_record` (record ensure/inspect/delete) are later candidates; no empty
contract manifest is published before their request, result, and lifecycle
semantics are settled.

This round must not fake a runtime implementation for an unmigrated capability
merely to make the catalog look complete. An unmigrated contract may have a
stable schema first, but the existing capability continues to run under its
native lifecycle until migration is complete.

## 6. Provider

A provider manifest describes how a contract operation executes inside a
provider module:

```yaml
api_version: anas.provider/v1
kind: ContractProvider
contract: relational_database
contract_version: 1.0.0
interface: postgres

operations:
  ensure:
    runtime: compose_run
    service: anas_postgres_provision
    command: [ensure]
  inspect:
    runtime: compose_run
    service: anas_postgres_provision
    command: [inspect]
```

The semantics of `compose_run` are:

```text
docker compose run --rm --no-deps <service> <operation>
```

The runner waits synchronously for the exit code. An operation service must not
be started by an ordinary `compose up`; a service referenced in a provider
manifest is automatically excluded from the module's ordinary service set.

A provider operation receives, through a controlled environment:

- the contract request;
- the resource-specific secret;
- the provider's management channel;
- the provider's network.

Administrator credentials must not enter a consumer's rendered environment.

## 7. The relational_database resource

### 7.1 Request

```yaml
consumer: nextcloud
resource_id: primary_database
provider: postgres
interface: postgres
spec:
  name: nextcloud
  principal: nextcloud
  credential:
    policy: generated
  deletion_policy: retain
```

Database names and principals use a conservative identifier:

```text
^[a-z][a-z0-9_]{0,62}$
```

### 7.2 Result

```yaml
host: anas_postgres
port: 5432
database: nextcloud
username: nextcloud
password_secret: RESOURCE_NEXTCLOUD_PRIMARY_DATABASE_PASSWORD
network: anas_postgres
```

The result persists no plaintext password, only a secret reference.

### 7.3 Ensure

The PostgreSQL provider must idempotently:

1. create or update the LOGIN role;
2. create the database if it does not exist;
3. reconcile owner, connect, and schema create privileges;
4. drop no tables and rebuild no database.

The MariaDB provider must idempotently:

1. create the database if it does not exist;
2. create or update the user;
3. reconcile `<database>.*` privileges;
4. drop no tables and rebuild no database.

### 7.4 Deletion

When a consumer module is removed:

- the consumer is stopped;
- the resource state moves to `retained`;
- the database and the account stay unchanged.

Only an explicit resource deletion invokes the provider's `delete`. Before
deleting, the target database, provider, consumer, and the unrecoverable impact
must be displayed and confirmation required.

### 7.5 The object_storage resource

An object storage request contains at least the bucket, a generated credential
policy, and a deletion policy; when `access_key_id` is omitted, the runner
derives it deterministically from `<consumer>.<resource_id>`. The result contains
the endpoint, region, bucket, access key ID, secret reference, and path-style
flag. The VersityGW provider creates or corrects the `user` principal, the
secret, and the bucket owner through a private admin API; when an existing
bucket's owner does not match it must fail, and must not transfer ownership
automatically. Removing the declaration follows `retained`, and no bucket or
object is deleted implicitly today.

## 8. Runner responsibilities

The runner holds the general types and the state machine, and no dialect:

```go
type ResourceRequest struct {
    Consumer  string
    ID        string
    Contract  string
    Provider  string
    Interface string
    Spec      map[string]any
}

type ProviderOperation struct {
    Runtime string
    Service string
    Command []string
}
```

The runner's resource phases:

```text
resolve modules
resolve contract/provider bindings
validate contract versions and schemas
materialize resource secrets
render immutable module artifacts
diff active and target modules/resources
ensure provider module is healthy
run provider ensure operation
persist resource state
start added/changed consumer modules
verify
commit active state
```

The runner must not select SQL through `if provider == postgres`. Field
validation and connection-environment publication for `relational_database` are
done by a built-in contract adapter; PostgreSQL's and MariaDB's SQL exists only
in their respective provider packages. The general executor dispatches solely on
the operation runtime (for example `compose_run`).

## 9. Resource state and secrets

Where the state lives:

```text
<workspace>/.anas/state/resources/
  <consumer>.<resource-id>.yml
```

For example:

```yaml
api_version: anas.resource-state/v1
consumer: nextcloud
resource_id: primary_database
contract: relational_database
contract_version: 1.0.0
provider: postgres
interface: postgres
spec_fingerprint: sha256:...
actual:
  host: anas_postgres
  port: 5432
  database: nextcloud
  username: nextcloud
  password_secret: RESOURCE_NEXTCLOUD_PRIMARY_DATABASE_PASSWORD
  network: anas_postgres
status: ready
deletion_policy: retain
provisioned_at: 2026-08-12T00:00:00Z
last_reconciled_at: 2026-08-12T00:00:00Z
```

The secret store uses a stable key:

```text
RESOURCE_NEXTCLOUD_PRIMARY_DATABASE_PASSWORD
```

No plaintext may be written into resource state, a deployment manifest, or logs.

## 10. Module-level activation diff

One-shot provider operations alone are not enough to guarantee hot addition. At
present every apply runs `compose up` for all modules from the new deployment
directory, and a path change may rebuild unchanged containers.

Activation must classify first:

- `unchanged`: the module artifact fingerprint, runtime configuration, and
  bindings are all identical; compose is not invoked.
- `added`: provision the resource first, then start the module.
- `changed`: reload/recreate/migrate according to the change policy.
- `removed`: stop the module in reverse dependency order.

The plan for adding Nextcloud must be:

```text
postgres                              unchanged
existing postgres consumers          unchanged
nextcloud.primary_database           ensure
nextcloud                             added
```

The runtime result:

```text
PostgreSQL and the existing consumers keep running
→ a short-lived provision operation
→ only Nextcloud is started
```

The active state must record which artifact deployment each module is currently
using. A new deployment may reuse the frozen directories of unchanged modules
from an older deployment, and reclaim them only once no active state references
them.

`state/active.yml` additionally records `runtime_status: running|stopped`. An
apply in the running state uses the activation diff above; an apply after a full
`anas stop` must restore every module of the target deployment, and must not skip
containers and networks that were deleted merely because the artifact did not
change.

## 11. Release and resolution

A module package must validate independently:

1. the `module.yml` schema;
2. file digests;
3. the runner ABI;
4. the declared provider manifest and operation assets;
5. contract names and version ranges;
6. the compose configuration.

Contracts install with the runner:

```text
<anas-install>/contracts/<name>/<version>/...
```

The module resolver reads independent module packages from the installation
directory or an explicit module root. The lock must pin:

```yaml
modules:
  postgres:
    version: 18.4.0
    revision: 1
    digest: sha256:...

contracts:
  relational_database:
    version: 1.0.0
    digest: sha256:...

bindings:
  nextcloud:
    relational_database: postgres
    relational_database.interface: postgres
```

## 12. Scope of the first migration

This repository has not been released, so no legacy compatibility layer is kept.
This round completes directly:

1. `casks/mods` becomes the top-level `modules`;
2. `module.yml` becomes `module.yml`, with the kind changed to `Module`;
3. user configuration converges on `modules.<name>.config`;
4. `contracts/relational_database` is added;
5. PostgreSQL and MariaDB implement provider operations;
6. every relational database consumer declares a resource and moves to a
   dedicated account;
7. PostgreSQL's global `*_DB_NAME` scan and reconcile compose service are
   removed;
8. resource state, secrets, and a synchronous operation executor are added;
9. a module-level activation diff is added;
10. static, render, compose, lifecycle, upgrade, and server end-to-end tests are
    updated.

The acceptance condition is: with PostgreSQL and at least one existing consumer
already running, adding Nextcloud leaves the PostgreSQL container ID, its
started-at, and the existing consumers' container IDs all unchanged, while
Nextcloud completes installation with a dedicated database account and passes its
functional probe.
