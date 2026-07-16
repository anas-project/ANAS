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
  *.erb                            Optional templates rendered before Compose
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
runtime:
  type: compose
  compose_file: docker-compose.yml
dependencies:
  requires:
    - name: traefik
      version: ">=0.1.0 <1.0.0"
  before:
    - traefik
  after:
    - samba_dc
config:
  required:
    - EXAMPLE_TOKEN
  defaults:
    EXAMPLE_DOMAIN_PREFIX: example
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
- `version`: cask package version using semantic versioning
  (`MAJOR.MINOR.PATCH`). By default it follows the cask's primary service image
  version. For build-based casks, use the primary Dockerfile `FROM` image
  version. Use `0.0.0` only for builtin casks or unpinned `latest` images whose
  upstream version cannot be tracked from the manifest.
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
- `dependencies.before`: required casks that must be present before this cask.
  Current migrated casks still use this field. Prefer `dependencies.requires`
  for new casks when version constraints matter.
- `dependencies.after`: ordering-only dependencies when both casks are present.
- `config.env_prefix`: optional environment prefix when it differs from the
  cask name, for example `eturnal` uses `TURN`.
- `config.required`: lower snake_case parameters that must exist after config
  flattening. Use `global.<name>` for global parameters.
- `config.defaults`: lower snake_case default parameters and values.
- `features`: capabilities used by humans and future tooling.
- `services.optional`: compose services filtered by env flags.
- `logic.hook.command`: command executed from the cask directory for
  `calculate`, `render_env`, `services`, and `after_start` phases.
- `status`: optional; use `experimental` or `inactive` for unfinished casks.

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
  `calculate`, `render_env`, `services`, and `after_start`.
- Accept hook responses for env patches, generated secrets, files written under
  the rendered cask directory, disabled Compose services, and after-start
  `docker cp` operations.
- Persist generated secrets in `secrets.generated.yml` and reuse them on later
  runs.
- Render cask assets into the runtime work directory, process the supported
  ERB-like template syntax, and write a per-cask `.env`.
- Detect Docker Compose and run per-cask `build`, `up -d`, and `down` with
  project names like `anas_nextcloud`.
- Query Compose services and remove hook-disabled optional services before
  build/start.
- Maintain `cask.lock.yml` with installed cask versions and reject unsupported
  downgrades or upgrades outside a cask's `upgrade.from` constraint.
- Create a host macvlan bridge and Docker macvlan network when an enabled cask
  declares required host LAN access.

## Current Casks

Current cask functionality is summarized below. Dependency names listed here are
the user-visible effect of manifest `before` and `after` rules; `core` is also
added implicitly for non-core casks.

| Cask | Category | Current functionality | Dependencies and notes |
| --- | --- | --- | --- |
| `core` | system | Provides shared defaults, host and gateway discovery, basic auth hash generation, SSH key generation, data path variables, DNS defaults, and VLAN/macvlan env values. | Required implicitly by every non-core cask. |
| `lego` | certificate | Prepares ACME certificate paths, email, certificate names, and certificate storage used by web and domain services. | Requires DNS provider config through `global.dns_provider`; currently ordered after `core`. |
| `traefik` | network | Builds and runs the HTTPS reverse proxy, dashboard, TLS routing, and basic-auth protected API. Generates service domain env from `domain_prefix`. | Requires `lego`; exposes the shared `traefik` Docker network. |
| `bind` | network | Runs local DNS, derives forwarders from host DNS, exports host IP, and sets Kerberos cache env for Samba integrations. | Requires `core`; used with `samba_dc`. |
| `samba_dc` | identity | Runs an AD-compatible Samba domain controller, derives realm, base DN, LDAP filters, admin DN, app group DN, LDAPS endpoints, and Kerberos settings. | Requires `lego` and `bind`. Acts as the LDAP provider for LDAP-aware casks. |
| `samba_fs` | storage | Runs a Samba file server joined to the domain and derives NetBIOS/default-domain settings. | Starts after `samba_dc` when both are enabled. Requires host LAN/macvlan. |
| `postgres` | database | Runs PostgreSQL, derives host/port/network/password env, and optionally exposes Adminer through Traefik. | Starts after `traefik` when Adminer is used. Optional Adminer is controlled by `adminer_enabled`. |
| `mariadb` | database | Runs MariaDB, derives root/user/password/mysql compatibility env, and optionally exposes Adminer through Traefik. | Starts after `traefik` when Adminer is used. Optional Adminer is controlled by `adminer_enabled`. |
| `eturnal` | communication | Runs TURN, derives TURN host/domain/port env, and persists the TURN shared secret. | Required by `nextcloud` for Talk. |
| `nextcloud` | app | Runs Nextcloud with Redis and Imaginary, optional Talk signaling, database auto-selection, LDAP filters, SAML SP registration, app launcher metadata, upload/memory defaults, and Talk secrets. | Requires `traefik` and `eturnal`; starts after Samba and database casks when present. Optional Talk is controlled by `talk_enabled`. |
| `collabora` | app | Runs Collabora Online as an office editing backend and derives Traefik domain env. | Requires `traefik`. |
| `llng` | identity | Runs LemonLDAP::NG as the current SSO portal, SAML IdP, OIDC provider, and app launcher. Generates SAML/OIDC signing material and copies app logos after start. | Starts after `traefik`. Can use PostgreSQL or MariaDB env when present. Optional Adminer is controlled by `adminer_enabled`. |
| `keycloak` | identity | Identity cask scaffold based on current LLNG integration assets. Derives Keycloak-prefixed SAML/OIDC fields and database env, but the service assets still mirror LLNG-style integration. | Starts after `traefik`. Treat as scaffold until service-specific assets are completed. |
| `lam` | identity | Runs LDAP Account Manager, derives domain/language/admin password, and connects to Samba LDAP env. | Requires `traefik`; starts after `samba_dc` when both are enabled. |
| `meshcentral` | app | Runs MeshCentral with Traefik routing, LDAP auth filters, app-filter aware user restrictions, and configurable MPS port. | Requires `traefik` and `mariadb`; starts after `samba_dc` when present. |
| `ddns` | network | Runs qmcgaw/ddns-updater and generates DNSPod settings for base-domain and wildcard IPv4/IPv6 records when `DNS_PROVIDER=dnspod`. | Requires `traefik` for dashboard routing and `global.dns_provider`. |
| `netbird` | network | Incomplete experimental dashboard, signal, and management scaffold; excluded from the full example. | Persistence and the complete login/management flow must be designed before it is restored to recommended deployments. |
| `freeradius` | network | Experimental FreeRADIUS manifest scaffold only. The current compose file still mirrors the Lego scaffold and is not a complete RADIUS deployment. | Status `experimental`; do not treat as production-ready. |

Current limitations:

- Root-level Ruby casks still exist in the repository, but they are legacy and
  outside the Go cask rules.
- Current migrated casks mostly use `dependencies.before`; new casks should use
  `dependencies.requires` when version constraints are needed.
- `keycloak` and `freeradius` are scaffolds and need service-specific cleanup
  before production use.
- The runner does not execute arbitrary Ruby template code. Complex rendering
  must be moved into a hook and exposed as simple env values.

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
   - use `before` only when maintaining the current migrated manifest style
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
files. Use `internalEnv` filtering in `runner.go` for render-only values.

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

## Template Rule

Current templates are ERB-like files ending in `.erb`. The Go renderer supports
the existing simple patterns used by the casks:

- `<%= envs["KEY"] %>`
- `<%= "#{envs["KEY"]}" %>`
- simple equality blocks like `<% if envs['KEY'] == 'value' %> ... <% end %>`

Do not add arbitrary Ruby expressions. If a template needs complex logic, move
the logic into Go and render a simple env value.

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
