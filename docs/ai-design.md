# ANAS AI Design Guide

This document is written for AI coding agents and maintainers working on ANAS.
It describes the current Go project structure, the cask structure, and the rules
for designing or modifying casks.

## Project Goal

ANAS is a Go launcher for building a NAS from open-source services. The launcher
does not replace Docker Compose. It prepares configuration, renders cask assets,
orders dependencies, manages generated secrets, and then calls Docker Compose.

The source of truth is:

- structured user config in YAML
- cask manifests in `casks/mods/*/cask.yml`
- cask hook logic for derived values and special integrations
- Docker Compose files and service assets inside each cask

Legacy Ruby configuration and `runner.rb` files are not supported in the Go
rules.

## Current Project Structure

```text
.
  bin/anas                         CLI wrapper around go run
  cmd/anas/main.go                 Process entry point
  internal/config                  Structured config loading and env flattening
  internal/compose                 Docker Compose detection and execution
  internal/runner                  Command lifecycle, cask manifest loading, render logic
  casks/mods/<name>                Cask manifests and service assets
  docs                             User and maintainer documentation
  config.example.yml               Minimal structured config
  config.full.example.yml          Broad integration example
```

Important files:

- `internal/config/config.go`: accepts only the new structured YAML keys.
- `internal/runner/runner.go`: implements `plan`, `render`, `build`, `start`,
  `restart`, and `stop`.
- `internal/runner/manifest.go`: loads cask manifests and validates the cask ABI.
- `internal/runner/hook.go`: invokes cask hook programs through the cask ABI.
- `internal/runner/modules.go`: contains core module metadata types.
- `internal/runner/secrets.go`: persists generated secrets under the runtime
  base path.
- `internal/runner/network.go`: creates host LAN/macvlan support when required.
- `internal/compose/compose.go`: calls `docker compose` using argument arrays.

## User Config Rule

Only this top-level shape is valid:

```yaml
modules:
  - nextcloud

global:
  domain: nas.example.com
  email: admin@example.com
  data_path: /srv/anas
  timezone: Asia/Shanghai
  dns_provider: manual
  default_service_root_password: ChangeMe1!

secrets:
  dnspod_api_key: token-value

services:
  nextcloud:
    env:
      domain_prefix: cloud
      upload_max_size: 32G

env:
  RAW_ENV_KEY: raw-value
```

Rules:

- Use `modules` for the requested casks.
- Use `global` for common NAS settings.
- Use `secrets` for user-provided secrets.
- Use `services.<name>.env` for service-specific overrides.
- Use `env` only for raw escape-hatch values.
- Do not use legacy `mods` or `envs`; the parser rejects unknown fields.

Service env keys are uppercased and prefixed automatically. For example:

```yaml
services:
  nextcloud:
    env:
      domain_prefix: cloud
```

becomes:

```text
NEXTCLOUD_DOMAIN_PREFIX=cloud
```

## Cask Directory Structure

Every cask must follow this layout:

```text
casks/mods/<cask_name>/
  cask.yml                         Required manifest
  hook/main.go                     Optional cask-owned Go hook
  docker-compose.yml               Required for compose casks
  <service>/Dockerfile             Optional build context
  <service>/root/...               Optional container root files
  assets/...                       Optional images and static assets
  *.envsubst                       Optional container-runtime text templates
```

Do not add `runner.rb`. Put cask-specific behavior in the cask hook declared by
`logic.hook.command`; runner internals should stay limited to lifecycle,
rendering, dependency ordering, and the hook protocol.

## Cask Manifest Rule

Every cask has a `cask.yml` manifest:

```yaml
api_version: anas.dev/v1
kind: Cask
name: example
version: 1.5.2
title: Example Service
description: Short service purpose.
category: app
upgrade:
  from: ">=1.0.1 <=1.5.1"
  # Versions at which the on-disk data format changed. Required on every cask:
  # an absent list means "unknown" and blocks every rollback across a version
  # change, while `[]` states that no release ever rewrote the format. When in
  # doubt, list the version — an extra snapshot costs far less than a rollback
  # into data the old code cannot read. See docs/contracts/snapshot.md.
  data_breaking: []
runtime:
  type: compose
  compose_file: docker-compose.yml
dependencies:
  requires:
    - name: traefik
      version: ">=0.1.0 <1.0.0"
  after:
    - samba_dc
config:
  required:
    - token
  defaults:
    domain_prefix: example
features:
  ldap_client: true
  domain: true
services:
  optional:
    - name: example_admin
      enabled_by: EXAMPLE_ADMIN_ENABLED
```

Manifest fields:

- `api_version`: currently `anas.dev/v1`.
- `kind`: always `Cask`.
- `name`: directory name and module id.
- `version`: the cask packaging version using semantic versioning
  (`MAJOR.MINOR.PATCH`). Dependency constraints, `upgrade.from`, and the
  downgrade check operate on this field only. Bump it when the cask's assets,
  templates, hook, or manifest change in a way consumers can observe.
- `app_version`: optional upstream application/image version, recorded in the
  lock file for humans and tooling. Track the primary service image version
  here (for build-based casks, the primary Dockerfile `FROM` version). An
  image bump changes `app_version` and needs only a minor/patch bump of
  `version` unless the packaging contract also changed. Existing casks carry
  their historical image-derived `version` as the packaging baseline.
- `abi.supports`: cask runtime ABI versions supported by this cask. The current
  runner ABI is `anas.cask/v1`.
- `upgrade.from`: optional semantic version constraint for supported source
  versions when upgrading to this cask version, for example
  `>=1.0.1 <=1.5.1` or `1.0.1 - 1.5.1`.
- `title`: human readable name.
- `description`: concise purpose.
- `category`: one of `system`, `network`, `identity`, `database`, `storage`,
  `communication`, `certificate`, or `app`.
- `runtime.type`: `compose` or `builtin`.
- `runtime.compose_file`: `docker-compose.yml` for compose casks.
- `dependencies.requires`: casks that must be present before this cask, with an
  optional semantic version constraint such as `^1.2.0` or `>=1.0.0 <2.0.0`.
  Required casks are selected automatically and are calculated, rendered, and
  started before the dependent cask.
- `dependencies.after`: soft ordering only. The named cask is not selected
  automatically; when both casks are selected, it is ordered before this cask.
- `dependencies.requires_one`: a provider choice for a capability. Each entry
  declares `capability`, the lower-snake-case `selected_by` parameter, the
  allowed `providers`, and a `default`. The parameter may be `auto` on first
  deployment; the runner resolves it to one provider and persists that binding
  in `cask.lock.yml`. A later provider change must use the parameter's declared
  `data_migrate` operation.

- `config.env_prefix`: optional environment prefix when it differs from the
  cask name, for example `eturnal` uses `TURN`.
- `config.required`: lower snake_case parameters that must exist after config
  flattening. Use `global.<name>` for global parameters.
- `config.defaults`: lower snake_case default parameters and values.
- `config.consumes`: env keys produced outside the cask's dependency closure
  that its rendering and hooks may read. Entries are exact keys or single
  leading/trailing-star globs, for example `APPS_LIST*` or `*_DB_NAME` for a
  capability provider that scans its consumers' declarations. User secrets
  must always be claimed here (or match a closure prefix) to be visible.
- `config.exports`: env keys outside the cask's own prefixes that its
  calculate hook publishes, for example `SMAL_SP_*` for SAML SP registration
  or `MYSQL_*` compatibility aliases. Undeclared cross-prefix writes fail.
- `features`: capabilities used by humans and future tooling.
- `services.optional`: compose services filtered by env flags.
- `logic.hook.command`: command executed from the cask directory for
  `calculate`, `render_env`, `services`, and `after_start` phases.
- `status`: optional; use `experimental` or `inactive` for unfinished casks.

Dependency order guarantees that the upstream Compose project is created first;
it does not by itself mean every upstream process is healthy. Runtime consumers
must retain bounded connection retries. Casks that need a strict readiness gate
should expose a healthcheck so a future readiness policy can wait explicitly
without overloading dependency selection semantics.

The built-in dependency graph is intentionally summarized as follows:

| Cask | Hard dependencies |
| --- | --- |
| `traefik`, `samba_dc`, `freeradius` | `lego` |
| `samba_fs` | `samba_dc` |
| `postgres`, `mariadb`, `eturnal`, `ddns` | `traefik` |
| `keycloak`, `llng` | `traefik`, `samba_dc`, one relational database |
| `nextcloud` | `traefik`, `eturnal`, `samba_dc`, one relational database |
| `collabora` | `nextcloud` |
| `meshcentral` | `traefik`, `mariadb`, `samba_dc` |
| `lam` | `traefik`, `samba_dc` |
| `netbird` | `traefik`, one SSO provider (`keycloak` or `llng`) |

Nextcloud's selected database is already a hard `requires_one` edge, so
duplicating both database names under `after` would incorrectly order it after
an unused provider. SSO integration is also not represented as a readiness
edge: the provider and service finish cross-configuration after their containers
are created, and forcing either side to become healthy first could deadlock.

The manifest is metadata, policy, and hook selection. Runner code should not
contain a hard-coded cask registry or cask-specific calculation functions.

## Runtime Cask Functionality

The Go runner treats casks as manifest-driven service packages. Its current
cask functionality is:

- Load all casks from `casks/mods/*/cask.yml` and reject casks without the
  current ABI support marker, invalid metadata, invalid semantic versions, or
  leftover `runner.rb` files.
- Build the execution order from cask required dependencies, optional
  dependency version constraints, order-only `after` rules, implicit `core`,
  and user-provided `services.<name>.depends_on`.
- Apply manifest defaults after user config is flattened. Defaults use
  lower snake_case names in manifests and become prefixed environment keys such
  as `NEXTCLOUD_DOMAIN_PREFIX`.
- Validate required config keys before running each cask's calculation hook.
- Run cask hooks through the `anas.cask/v1` JSON protocol. Supported phases are
  `calculate`, `render_env`, `services`, and `after_start`. Hooks declared as
  `go run <pkg>` are compiled once per run and frozen into the rendered cask
  as `.hook.bin`, so starting an existing release needs no Go toolchain.
- Accept hook responses for env patches, generated secrets, files written under
  the rendered cask directory, disabled Compose services, render-only
  `internal_env` keys, and after-start `docker cp` operations. Calculate
  patches are validated against the cask's prefixes and `config.exports`.
- Persist generated secrets in `secrets.generated.yml` and reuse them on later
  runs. Non-calculate hook phases receive only the cask-scoped secrets.
- Copy cask assets into the runtime work directory and write a scoped per-cask
  `.env`. The runner does not interpret application configuration templates;
  each container derives its runtime configuration from that environment.
- Detect Docker Compose and reconcile per cask with `build`, `up -d
  --remove-orphans`, and `down` using project names like `anas_nextcloud`.
  A config-driven start downs casks removed from the config instead of
  stopping the whole stack; `start`/`restart` without a config run the
  promoted release as an immutable artifact.
- Query Compose services and remove hook-disabled optional services before
  build/start.
- Maintain `cask.lock.yml` with installed cask versions (and upstream
  `app_version`), reject unsupported downgrades or upgrades outside a cask's
  `upgrade.from` constraint, keep the demoted release plus a lock snapshot as
  `release.previous`, and restore both with `anas rollback`.
- Create a host macvlan bridge and Docker macvlan network when an enabled cask
  declares required host LAN access.

## Current Casks

Current cask functionality is summarized below. Dependency names listed here are
the user-visible effect of manifest `requires`, `requires_one`, and `after`
rules; `core` is also added implicitly for non-core casks.

| Cask | Category | Current functionality | Dependencies and notes |
| --- | --- | --- | --- |
| `core` | system | Provides shared defaults, host and gateway discovery, basic auth hash generation, data path variables, DNS defaults, and VLAN/macvlan env values. | Required implicitly by every non-core cask. |
| `lego` | certificate | Prepares ACME certificate paths, email, certificate names, and certificate storage used by web and domain services. | Requires DNS provider config through `global.dns_provider`; currently ordered after `core`. |
| `traefik` | network | Builds and runs the HTTPS reverse proxy, dashboard, TLS routing, and basic-auth protected API. Generates service domain env from `domain_prefix`. | Requires `lego`; exposes the shared `traefik` Docker network. |
| `samba_dc` | identity | Runs an AD-compatible Samba domain controller and BIND9-DLZ DNS in one container; derives DNS forwarders, realm, base DN, LDAP filters, admin DN, app group DN, LDAPS endpoints, and Kerberos settings. | Requires `lego`. Acts as the DNS and LDAP provider for dependent casks. |
| `samba_fs` | storage | Runs a Samba file server joined to the domain and derives NetBIOS/default-domain settings. | Requires `samba_dc` and host LAN/macvlan. |
| `postgres` | database | Runs PostgreSQL, derives host/port/network/password env, and optionally exposes Adminer through Traefik. | Requires `traefik`. Optional Adminer is controlled by `adminer_enabled`. |
| `mariadb` | database | Runs MariaDB, derives root/user/password/mysql compatibility env, and optionally exposes Adminer through Traefik. | Requires `traefik`. Optional Adminer is controlled by `adminer_enabled`. |
| `eturnal` | communication | Runs TURN, derives TURN host/domain/port env, and persists the TURN shared secret. | Requires `traefik`; required by `nextcloud` for Talk. |
| `nextcloud` | app | Runs Nextcloud with Redis and Imaginary, optional Talk signaling, database auto-selection, LDAP filters, SAML SP registration, app launcher metadata, upload/memory defaults, and Talk secrets. | Requires `traefik`, `eturnal`, `samba_dc`, and one relational database. Optional Talk is controlled by `talk_enabled`. |
| `collabora` | app | Runs Collabora Online as an office editing backend and derives Traefik domain env. | Requires `nextcloud`, which provides its transitive Traefik and Samba dependencies. |
| `llng` | identity | Runs LemonLDAP::NG as the current SSO portal, SAML IdP, OIDC provider, and app launcher. Generates SAML/OIDC signing material and copies app logos after start. | Requires `traefik`, `samba_dc`, and one relational database. Optional Adminer is controlled by `adminer_enabled`. |
| `keycloak` | identity | Identity cask scaffold based on current LLNG integration assets. Derives Keycloak-prefixed SAML/OIDC fields and database env, but the service assets still mirror LLNG-style integration. | Requires `traefik`, `samba_dc`, and one relational database. Treat as scaffold until service-specific assets are completed. |
| `lam` | identity | Runs LDAP Account Manager, derives domain/language/admin password, and connects to Samba LDAP env. | Requires `traefik` and `samba_dc`. |
| `meshcentral` | app | Runs MeshCentral with Traefik routing, LDAP auth filters, app-filter aware user restrictions, and configurable MPS port. | Requires `traefik`, `mariadb`, and `samba_dc`. |
| `ddns` | network | Runs the upstream `ghcr.io/qdm12/ddns-updater` image and generates DNSPod settings for base-domain and wildcard IPv4/IPv6 records when `DNS_PROVIDER=dnspod`. | Requires `traefik` for dashboard routing and `global.dns_provider`. |
| `netbird` | network | Incomplete experimental dashboard, signal, and management scaffold; excluded from the full example. | Requires `traefik` and one SSO provider (`keycloak` or `llng`). Persistence and the complete management flow still need work. |
| `freeradius` | network | Runs the upstream FreeRADIUS 3.2 image with UDP 1812/1813 publication and configuration health checks; client and user policy remain deployment-owned. | Standalone base service; no default production clients or users are generated. |

Current limitations:

- Root-level Ruby casks still exist in the repository, but they are legacy and
  outside the Go cask rules.
- `keycloak` and `freeradius` are scaffolds and need service-specific cleanup
  before production use.
- The runner does not render application configuration. Container entrypoints
  own deterministic config generation from their scoped environment.

## How To Design A New Cask

When designing a cask, follow this order:

1. Define the user-facing service goal.
2. Decide if it is a `compose` cask or a `builtin` cask.
3. Create `casks/mods/<name>/cask.yml`.
4. Add `docker-compose.yml` and service assets if it is a compose cask.
5. Use lower snake_case parameter names, such as `domain_prefix`.
6. Add `abi.supports: [anas.cask/v1]`.
7. Add defaults to `cask.yml`.
8. Add or reuse a cask hook under `logic.hook.command`.
9. Add dependencies:
   - prefer `requires` for required casks, especially when a version constraint
     is needed
   - use `requires_one` for a capability with alternative providers
   - use `after` for order-only relationships
10. Add optional service filtering if a compose service is controlled by an env
   flag.
11. Run:

```sh
GOCACHE=$PWD/.gocache go test ./...
bin/anas plan -c config.example.yml
bin/anas render -c config.example.yml -b ./.runtime
```

Clean `.gocache` and `.runtime*` before committing.

## Derived Env Design

Prefer this priority:

1. Static defaults in `cask.yml`.
2. User overrides in `services.<name>.env`.
3. Shared defaults from `global`.
4. Cask hook output from the `calculate` and `render_env` phases.
5. Generated secrets persisted by `secretStore`.

Do not generate a new secret during every render. Use `secretStore.Ensure`.

Do not store internal helper values such as SSH private keys in module `.env`
files. A hook that returns render-only values must list those keys in the
`internal_env` field of its `render_env` response; the runner keeps them
available for template rendering but excludes them from the written `.env`.

Environment access is scoped. A cask's rendered `.env`, its template
rendering, and its `render_env`/`services`/`after_start` hook input contain
only: global and core-derived keys, keys owned by the cask itself or its
dependency closure, keys matching those casks' env prefixes, and keys claimed
in manifest `config.consumes`. User secrets from the config `secrets` section
are only distributed to casks that claim them. The `calculate` phase is the
privileged derivation stage: it sees the full accumulating environment, but
its env patch may only publish keys under the cask's own prefixes or patterns
declared in manifest `config.exports`; anything else fails the run.

## Compose Design Rules

Compose files should:

- use `.env` for runtime substitution
- use stable container, network, and image prefixes from env
- avoid hardcoded local paths
- declare external networks only when another cask owns the network
- keep optional services separable for filtering
- avoid host networking unless the cask explicitly declares host LAN needs

The launcher invokes Compose with:

```text
docker compose --project-name anas_<cask> --env-file .env ...
```

Do not rely on the caller's shell environment for required values.

## Container Configuration Rule

The runner never renders application configuration. It freezes each cask's
Compose definition, image inputs, startup scripts, and scoped `.env`; the
container entrypoint derives runtime configuration under `/run/anas` before it
execs the upstream process.

- JSON must be produced with an application-native structured writer (for
  example Node.js `JSON.stringify`), not `sed` or unrestricted `envsubst`.
- Small YAML files may be emitted by a container entrypoint only after values
  are type-checked and safely quoted.
- INI, XML, and other text formats may use `.envsubst`, with an explicit
  variable allowlist and a post-render unresolved-variable check.
- Generated files use a temporary sibling plus atomic rename, and sensitive
  files are owner-only.
- Runtime configuration is deterministic from the sealed `.env` and image. It
  must not generate secrets, contact discovery services, or mutate the secret
  store. Runtime-only discoveries such as a container IP are permitted when
  they are not deployment identity.

The suffixes `.erb`, `.j2`, `.j3`, and `.tmpl`, and ERB markers under `casks/`,
are rejected by the static test. Use `.envsubst` only for actual container-side
text substitution.

## Replacement Criteria

Do not replace the root project until all of these pass:

- `go test ./...`
- `plan` with minimal and full example configs
- `render` with the real NAS config
- `build` for all enabled casks
- `start` on a real NAS host
- service health checks or manual verification for each enabled cask

After replacement, remove old Ruby project files from the root and keep only
the Go structure documented here.
