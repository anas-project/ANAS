# ANAS AI Design Guide

This document is written for AI coding agents and maintainers working on ANAS.
It describes the current Go project structure, the module structure, and the rules
for designing or modifying modules.

## Project Goal

ANAS is a Go launcher for building a NAS from open-source services. The launcher
does not replace Docker Compose. It prepares configuration, renders module assets,
orders dependencies, manages generated secrets, and then calls Docker Compose.

The sources of truth are:

- structured user config in YAML
- module manifests in `modules/*/module.yml`
- module hook logic for derived values and special integrations
- Docker Compose files and service assets inside each module

Legacy Ruby configuration and `runner.rb` files are not supported in the Go
rules.

## Current Project Structure

```text
.
  bin/anas                         CLI wrapper around go run
  cmd/anas/main.go                 Process entry point
  internal/config                  Structured config loading and env flattening
  internal/compose                 Docker Compose detection and execution
  internal/runner                  Command lifecycle, module manifest loading, render logic
  modules/<name>                Module manifests and service assets
  docs                             User and maintainer documentation
  config.example.yml               Minimal structured config
  config.full.example.yml          Broad integration example
```

Important files:

- `internal/config/config.go`: accepts only the new structured YAML keys.
- `internal/runner/runner.go`: implements `plan`, `render`, `build`, `start`,
  `restart`, and `stop`.
- `internal/runner/manifest.go`: loads module manifests and validates the module ABI.
- `internal/runner/hook.go`: invokes module hook programs through the module ABI.
- `internal/runner/modules.go`: contains the module metadata types.
- `internal/runner/globals.go` and `globals.yml`: the deployment's own
  parameter schema, compiled into the runner.
- `internal/runner/hostnet.go`: host network discovery and macvlan planning.
- `internal/runner/secrets.go`: persists generated secrets under the runtime
  base path.
- `internal/runner/network.go`: creates host LAN/macvlan support when required.
- `internal/compose/compose.go`: calls `docker compose` using argument arrays.

## User Config Rule

The configuration is structured YAML. The example below shows the common core;
the complete schema also includes `administration`, `identity`, `dynamic_dns`,
and `rollback` sections. See the [configuration reference](/reference/configuration).

```yaml
modules:
  nextcloud:
    config:
      domain_prefix: cloud
      upload_max_size: 32G

global:
  base_domain: nas.example.com
  email: admin@example.com
  timezone: Asia/Shanghai
  default_service_root_password: ChangeMe1!

secrets:
  dnspod_api_key: token-value

env:
  RAW_ENV_KEY: raw-value
```

Rules:

- Use `modules` for the requested modules.
- Use `global` for common NAS settings.
- Use `secrets` for user-provided secrets.
- Use `modules.<name>.config` for service-specific overrides.
- Use `env` only for raw escape-hatch values.
- Do not use legacy `mods` or `envs`; the parser rejects unknown fields.

Module config keys are uppercased and prefixed automatically. For example:

```yaml
modules:
  nextcloud:
    config:
      domain_prefix: cloud
```

becomes:

```text
NEXTCLOUD_DOMAIN_PREFIX=cloud
```

## Module Directory Structure

Every module must follow this layout:

```text
modules/<module_name>/
  module.yml                         Required manifest
  hook/main.go                     Optional module-owned Go hook
  docker-compose.yml               Required for compose modules
  <service>/Dockerfile             Optional build context
  <service>/root/...               Optional container root files
  assets/...                       Optional images and static assets
  *.envsubst                       Optional container-runtime text templates
```

Do not add `runner.rb`. Put module-specific behavior in the module hook declared by
`logic.hook.command`; runner internals should stay limited to lifecycle,
rendering, dependency ordering, and the hook protocol.

## Module Manifest Rule

Every module has a `module.yml` manifest:

```yaml
api_version: anas.module/v1
kind: Module
name: example
version: 1.5.2
revision: 1
title: Example Service
description: Short service purpose.
category: app
upgrade:
  from: ">=1.0.1 <=1.5.1"
  # Versions at which the on-disk data format changed. Required on every module:
  # an absent list means "unknown" and blocks every rollback across a version
  # change, while `[]` states that no release ever rewrote the format. When in
  # doubt, list the version — an extra snapshot costs far less than a rollback
  # into data the old code cannot read. See
  # docs/reference/contracts/snapshot.md.
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
identity:
  interfaces: [ldaps]
  application_group: true
features:
  base_domain: true
services:
  optional:
    - name: example_admin
      enabled_by: EXAMPLE_ADMIN_ENABLED
```

Manifest fields:

- `api_version`: currently `anas.module/v1`.
- `kind`: always `Module`.
- `name`: directory name and module id.
- `version`: the normalized upstream version using semantic versioning
  (`MAJOR.MINOR.PATCH`). Dependency constraints and `upgrade.from` operate on
  this field. When the upstream version changes, reset `revision` to `1`.
- `revision`: the positive ANAS packaging revision for `version`. It starts at
  `1` and increments when ANAS changes a Dockerfile or another file copied into
  an image without changing the upstream version. Release identity and image
  tags use `<version>-r<revision>`; the runner compares `version` first and the
  numeric revision second instead of parsing the tag as SemVer.
- `app_version`: optional upstream application/image version, recorded in the
  lock file for humans and tooling. It preserves the upstream spelling when it
  cannot be used as normalized SemVer, such as Collabora's `26.04.2.4.1`.
- `abi.supports`: module runtime ABI versions supported by this module. The current
  runner ABI is `anas.module-hook/v1`.
- `upgrade.from`: optional semantic version constraint for supported source
  versions when upgrading to this module version, for example
  `>=1.0.1 <=1.5.1` or `1.0.1 - 1.5.1`.
- `title`: human readable name.
- `description`: concise purpose.
- `category`: one of `system`, `network`, `identity`, `database`, `storage`,
  `communication`, `certificate`, or `app`.
- `runtime.type`: `compose` or `builtin`.
- `runtime.compose_file`: `docker-compose.yml` for compose modules.
- `dependencies.requires`: modules that must be present before this module, with an
  optional semantic version constraint such as `^1.2.0` or `>=1.0.0 <2.0.0`.
  Required modules are selected automatically and are calculated, rendered, and
  started before the dependent module.
- `dependencies.after`: soft ordering only. The named module is not selected
  automatically; when both modules are selected, it is ordered before this module.
- `dependencies.requires_one`: a provider choice for a capability. Each entry
  declares `capability`, the lower-snake-case `selected_by` parameter, the
  allowed `providers`, and a `default`. The parameter may be `auto` on first
  deployment; the runner resolves it to one provider and persists that binding
  in `config.lock.yml`. A later provider change must use the parameter's declared
  `data_migrate` operation.

- `config.env_prefix`: optional environment prefix when it differs from the
  module name, for example `eturnal` uses `TURN`.
- `config.required`: lower snake_case parameters that must exist after config
  flattening. Use `global.<name>` for global parameters.
- `config.defaults`: lower snake_case default parameters and values.
- `config.consumes`: env keys produced outside the module's dependency closure
  that its rendering and hooks may read. Entries are exact keys or single
  leading/trailing-star globs, for example `APPS_LIST*` or `*_DB_NAME` for a
  capability provider that scans its consumers' declarations. User secrets
  must always be claimed here (or match a closure prefix) to be visible.
- `config.exports`: env keys outside the module's own prefixes that its
  calculate hook publishes, for example `SMAL_SP_*` for SAML SP registration
  or `MYSQL_*` compatibility aliases. Undeclared cross-prefix writes fail.
- `features`: capabilities used by humans and future tooling.
- `services.optional`: compose services filtered by env flags.
- `logic.hook.command`: command executed from the module directory for
  `calculate`, `render_env`, `services`, and `after_start` phases.
- `status`: optional; use `experimental` or `inactive` for unfinished modules.

Dependency order guarantees that the upstream Compose project is created first;
it does not by itself mean every upstream process is healthy. Runtime consumers
must retain bounded connection retries. Modules that need a strict readiness gate
should expose a healthcheck so a future readiness policy can wait explicitly
without overloading dependency selection semantics.

Do not duplicate the dependency graph in design prose. The manifests are
authoritative, and the maintained human-readable list is the
[Module catalog](/reference/modules). In particular, the repository contains
separate `ddns_go` and `ddns_updater` modules; it does not contain a `keycloak`
module. `authentik`, `netbird`, and `freeradius` are explicitly experimental.

The manifest is metadata, policy, and hook selection. Runner code should not
contain a hard-coded module registry or module-specific calculation functions.

## Runtime Module Functionality

The Go runner treats modules as manifest-driven service packages. Its current
module functionality is:

- Load all modules from `modules/*/module.yml` and reject modules without the
  current ABI support marker, invalid metadata, invalid semantic versions, or
  leftover `runner.rb` files.
- Build the execution order from module required dependencies, optional
  dependency version constraints, order-only `after` rules, and
  user-provided `modules.<name>.depends_on`.
- Apply manifest defaults after user config is flattened. Defaults use
  lower snake_case names in manifests and become prefixed environment keys such
  as `NEXTCLOUD_DOMAIN_PREFIX`.
- Validate required config keys before running each module's calculation hook.
- Run module hooks through the `anas.module-hook/v1` JSON protocol. Supported phases are
  `calculate`, `render_env`, `services`, and `after_start`. Hooks declared as
  `go run <pkg>` are compiled once per run and frozen into the rendered module
  as `.hook.bin`, so starting an existing release needs no Go toolchain.
- Accept hook responses for env patches, generated secrets, files written under
  the rendered module directory, disabled Compose services, render-only
  `internal_env` keys, and after-start `docker cp` operations. Calculate
  patches are validated against the module's prefixes and `config.exports`.
- Persist generated secrets in `secrets.generated.yml` and reuse them on later
  runs. Non-calculate hook phases receive only the module-scoped secrets.
- Copy module assets into the runtime work directory and write a scoped per-module
  `.env`. The runner does not interpret application configuration templates;
  each container derives its runtime configuration from that environment.
- Detect Docker Compose and reconcile per module with `build`, `up -d
  --remove-orphans`, and `down` using project names like `anas_nextcloud`.
  A config-driven start downs modules removed from the config instead of
  stopping the whole stack; `start`/`restart` without a config run the
  promoted release as an immutable artifact.
- Query Compose services and remove hook-disabled optional services before
  build/start.
- Persist resolution decisions in `config.lock.yml`, freeze module versions,
  settings and artifacts into immutable deployment directories, reject
  unsupported version transitions, and activate or roll back by deployment ID.
- Create a host macvlan bridge and Docker macvlan network when an enabled module
  declares required host LAN access.

## Current Modules

Use the [Module catalog](/reference/modules) for the maintained stable and
experimental inventory. Use each `modules/<name>/module.yml` for exact version,
dependency, capability, configuration and lifecycle metadata. The Runner does
not render application-specific configuration itself; container entrypoints own
deterministic generation from scoped environment values.

## How To Design A New Module

When designing a module, follow this order:

1. Define the user-facing service goal.
2. Decide if it is a `compose` module or a `builtin` module.
3. Create `modules/<name>/module.yml`.
4. Add `docker-compose.yml` and service assets if it is a compose module.
5. Use lower snake_case parameter names, such as `domain_prefix`.
6. Add `abi.supports: [anas.module-hook/v1]`.
7. Add defaults to `module.yml`.
8. Add or reuse a module hook under `logic.hook.command`.
9. Add dependencies:
   - prefer `requires` for required modules, especially when a version constraint
     is needed
   - use `requires_one` for a capability with alternative providers
   - use `after` for order-only relationships
10. Add optional service filtering if a compose service is controlled by an env
   flag.
11. Run:

```sh
GOCACHE=$PWD/.gocache go test ./...
bin/anas plan -c config.example.yml
bin/anas render -c config.example.yml -w ./.runtime
```

Clean `.gocache` and `.runtime*` before committing.

## Derived Env Design

Prefer this priority:

1. Static defaults in `module.yml`.
2. User overrides in `modules.<name>.config`.
3. Shared defaults from `global`.
4. Module hook output from the `calculate` and `render_env` phases.
5. Generated secrets persisted by `secretStore`.

Do not generate a new secret during every render. Use `secretStore.Ensure`.

Do not store internal helper values such as SSH private keys in module `.env`
files. A hook that returns render-only values must list those keys in the
`internal_env` field of its `render_env` response; the runner keeps them
available for template rendering but excludes them from the written `.env`.

Environment access is scoped. A module's rendered `.env`, its template
rendering, and its `render_env`/`services`/`after_start` hook input contain
only: globally owned keys, keys owned by the module itself or its
dependency closure, keys matching those modules' env prefixes, and keys claimed
in manifest `config.consumes`. User secrets from the config `secrets` section
are only distributed to modules that claim them. The `calculate` phase is the
privileged derivation stage: it sees the full accumulating environment, but
its env patch may only publish keys under the module's own prefixes or patterns
declared in manifest `config.exports`; anything else fails the run.

## Compose Design Rules

Compose files should:

- use `.env` for runtime substitution
- use stable container, network, and image prefixes from env
- avoid hardcoded local paths
- declare external networks only when another module owns the network
- keep optional services separable for filtering
- avoid host networking unless the module explicitly declares host LAN needs

The launcher invokes Compose with:

```text
docker compose --project-name anas_<module> --env-file .env ...
```

Do not rely on the caller's shell environment for required values.

## Container Configuration Rule

The runner never renders application configuration. It freezes each module's
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

The suffixes `.erb`, `.j2`, `.j3`, and `.tmpl`, and ERB markers under `modules/`,
are rejected by the static test. Use `.envsubst` only for actual container-side
text substitution.

## Replacement Criteria

Do not replace the root project until all of these pass:

- `go test ./...`
- `plan` with minimal and full example configs
- `render` with the real NAS config
- `build` for all enabled modules
- `start` on a real NAS host
- service health checks or manual verification for each enabled module

After replacement, remove old Ruby project files from the root and keep only
the Go structure documented here.
