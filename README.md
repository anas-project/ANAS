# ANAS

ANAS is a Go-based NAS service launcher built around composable casks. Each
cask owns its Docker Compose assets and declares its launcher metadata in
`cask.yml`.

Current cask runtime ABI: `anas.cask/v2`.

**Usage guide: [docs/usage.md](docs/usage.md) · [中文](docs/usage.zh.md)** —
how to initialise a deployment, run it, change it, and recover it.

## Commands

```sh
go run ./cmd/anas plan   -c config.example.yml
go run ./cmd/anas lock   -c config.example.yml
go run ./cmd/anas render -c config.example.yml -b ./.runtime
go run ./cmd/anas build  -c config.example.yml -b ~/.anas
go run ./cmd/anas apply  -c config.example.yml --build -b ~/.anas
go run ./cmd/anas start  -b ~/.anas
go run ./cmd/anas stop   -b ~/.anas
go run ./cmd/anas rollback -b ~/.anas
go run ./cmd/anas config explain samba_dc.user_min_pass_length
go run ./cmd/anas config set -c config.yml samba_dc.user_min_pass_length 10
go run ./cmd/anas config plan -c config.yml -b ~/.anas
go run ./cmd/anas config secret list -b ~/.anas
```

`render` and `build` create an immutable ready deployment but never change the
active deployment. `apply` is the only normal deployment entry point. It
materializes under `staging/`, atomically finalizes the artifact under
`deployments/<id>/`, and only then starts Compose from that final path. This
keeps Docker Compose working-directory metadata valid. `start`, `stop`, and
`restart` operate only on the active frozen deployment and do not need the
original cask source tree or a Go toolchain.

The launcher locates independent cask bundles from `--cask-root`,
`ANAS_CASK_ROOT`, the current directory, or the installation directory. The
config-side `<name>.lock.yml` records both cask versions and bundle content
digests. A distributable bundle can carry a prebuilt hook at
`hook/bin/<os>-<arch>/anas-hook`; render/apply then need no Go toolchain.
`plan` is read-only: it does not inspect host networking, run cask
hooks, or create runtime state.

## Configuration

Only the structured YAML format is supported:

```yaml
modules:
  - traefik

global:
  domain: nas.example.com
  email: admin@example.com
  data_path: ./data
  timezone: Asia/Shanghai
  dns_provider: manual
  # Optional shared administrator password. When omitted, every cask gets its
  # own generated root password; read them with `anas config secret get`.
  default_service_root_password: ChangeMe1!

env:
  BASICAUTH_USER: admin
```

Persistent data can optionally use guarded Btrfs snapshots:

```yaml
rollback:
  snapshot:
    backend: btrfs
    source: /srv/anas/data
    root: /srv/anas/.snapshots
```

The source must be a Btrfs subvolume and the snapshot root must be on the same
Btrfs filesystem. A data restore requires `rollback --restore-data --yes` and
keeps the replaced data as a recovery subvolume.

Legacy Ruby keys such as `mods` and `envs` are intentionally rejected.
Per-service overrides live under `services`:

```yaml
services:
  nextcloud:
    enabled: true
    env:
      domain_prefix: cloud
      upload_max_size: 32G
      # Advanced override: auto, postgres, or mariadb.
      db_type: auto
```

Service env keys are automatically converted to the service prefix. The example
above becomes `NEXTCLOUD_DOMAIN_PREFIX` and `NEXTCLOUD_UPLOAD_MAX_SIZE`.

Use top-level `env` only for explicit raw environment variables that do not fit
the structured `global`, `secrets`, or `services` sections.

Set `services.<name>.enabled: false` to exclude a module listed under
`modules`. Disabling a module required by another enabled module is an error.

Cask dependency semantics are explicit: `requires` selects and orders a hard
dependency, `requires_one` selects exactly one capability provider, and `after`
only orders two casks when both were selected. Unknown or obsolete manifest
fields are rejected instead of being ignored.

## Generated State

Runtime files are written below the selected base path:

- `deployments/<id>/`: immutable, self-describing deployment artifacts.
- `staging/<id>/`: temporary materialization only; containers never start here.
- `state/active.yml`: active deployment and ordered rollback history.
- `state/deployments/*.yml` and `state/index.yml`: per-deployment lifecycle and
  a rebuildable summary index.
- `snapshots/<id>/`: optional data snapshot metadata and Btrfs subvolume.
- `secrets.generated.yml`: persistent generated secrets such as SSH keys,
  TURN secrets, OIDC client secrets, and SAML/OIDC signing keys.
- `<config-name>.lock.yml`: project-side cask versions, bundle digests, source
  identities, and resolved capability-provider bindings.
- `hook-bin/`: hook binaries compiled once per run; each rendered cask also
  carries its frozen copy as `.hook.bin`, so artifact starts need no Go
  toolchain.
- `go-build-cache/`: Go build cache used when compiling cask hooks.

Each rendered cask's `.env` is scoped: it contains only global values, the
cask's own and its dependency closure's keys, and keys the cask claims via
manifest `config.consumes`. One cask (or its containers) never receives the
credentials of unrelated casks.

Do not commit runtime directories or generated secrets. The runtime base and
generated `.env` files are owner-only because they contain service credentials.

## Current Scope

The launcher covers:

- manifest-based cask discovery from `casks/mods/*/cask.yml`
- cask ABI validation with `anas.cask/v1`
- semantic cask versions, dependency version constraints, upgrade checks, and a
  persisted cask lock file
- alternative capability providers with stable lock-file bindings (for example,
  PostgreSQL or MariaDB for application casks)
- module dependency ordering
- structured YAML config loading
- default env generation and cask hook based derived env generation
- cask hook phases for calculation, render-time env/files, optional service
  filtering, and after-start copy operations
- container-owned configuration generation from scoped environment files
- per-module `.env` generation
- Docker Compose detection and execution
- build/start/restart/stop/render/plan commands
- persistent generated secrets
- LLNG/Keycloak SAML/OIDC material generation
- Nextcloud, Netbird, Meshcentral, LDAP app integration variables
- macvlan setup for modules that require host LAN
- current cask manifests under `casks/mods/*/cask.yml`

Current casks provide:

- base runtime defaults and host network discovery through `core`
- ACME certificate files through `lego`
- HTTPS routing through `traefik`
- local BIND9-DLZ DNS and Samba AD support through `samba_dc`
- domain-joined file sharing through `samba_fs`
- PostgreSQL and MariaDB data stores with optional Adminer UIs
- TURN support through `eturnal`
- Nextcloud with Redis, Imaginary, optional Talk signaling, LDAP, SAML, and app
  launcher integration
- Collabora as the Nextcloud office backend
- LemonLDAP::NG as the current SSO portal and SAML/OIDC provider
- Keycloak as an identity cask scaffold using the current LLNG integration
  assets
- LDAP Account Manager and MeshCentral with Samba/LDAP integration
- DDNS config generation for DNSPod when selected
- NetBird as an incomplete experimental scaffold excluded from the full example
- FreeRADIUS as an experimental scaffold, not a complete RADIUS deployment yet

## Cask Layout

Each cask directory follows this rule:

```text
casks/mods/<name>/
  cask.yml
  hook/
    main.go
  docker-compose.yml
  <service build contexts, templates, assets>
```

Each cask declares the runner ABI it supports:

```yaml
abi:
  supports:
    - anas.cask/v1
```

Use lower snake_case for cask parameters in `cask.yml`. The runner maps them to
the environment variables consumed by existing templates, using the cask
`config.env_prefix` or the cask name as the prefix. For example,
`domain_prefix` in the `nextcloud` cask becomes `NEXTCLOUD_DOMAIN_PREFIX`.

Per-cask logic is executed through `logic.hook.command`. The runner sends a
JSON request using the cask ABI and applies the returned env patch, generated
secrets, files, service filters, and after-start copy operations. Runner code
does not contain cask-specific calculation functions.

`runner.rb` files are not part of the Go rules and should not be added. The
previous Ruby implementation is retained under `legacy/` for reference.

Before releasing changes, run render/build/start against real NAS configuration
and verify each enabled module.

## Developer Docs

See [docs/ai-design.md](docs/ai-design.md) for the project structure, cask
structure, and rules for designing new casks.

See [docs/iam-capability-design.md](docs/iam-capability-design.md) for the
proposed provider-neutral IAM capability model, OIDC/SAML negotiation,
environment contract, provider selection, and migration plan.

See [docs/config-state-lifecycle.md](docs/config-state-lifecycle.md) for the
current persistent-state audit, first-start-only settings, and the proposed
configuration lifecycle and reconciliation model.

See [docs/config-cli-lifecycle.md](docs/config-cli-lifecycle.md) for CLI config
editing, change-effect classification, start guards, and the apply/rotation/
migration command design.

See [docs/test-server-deployment-2026-06-30.md](docs/test-server-deployment-2026-06-30.md)
for the test-server deployment procedure, verification results, fixes, and
remaining full-stack test constraints.

See [docs/test-server-deployment-2026-07-03.md](docs/test-server-deployment-2026-07-03.md)
for the isolated Docker findings, PostgreSQL persistence verification, and the
remaining blockers for a full-stack availability claim.

See [docs/test-server-deployment-2026-07-04.md](docs/test-server-deployment-2026-07-04.md)
for the server-buildable image builds and the initial smoke-start of the full
stack.

See [docs/test-server-deployment-2026-07-05.md](docs/test-server-deployment-2026-07-05.md)
for the current record: per-container runtime verification that exposed three
crash-loop defects (postgres per-service database provisioning, the NetBird
dashboard `set -e` bootstrap, and the Nextcloud DB-wait host/port split), the
fixes and their re-verification, and why the earlier smoke pass was a false
green.

See [docs/test-server-deployment-2026-07-13.md](docs/test-server-deployment-2026-07-13.md)
for the latest deployment attempt, the fixed smoke-test false-green check, the
local regression results, and the current SSH entry-point blocker.

See [docs/test-server-deployment-2026-07-13-finance.md](docs/test-server-deployment-2026-07-13-finance.md)
for the completed isolated deployment on `finance.hlong.wang`, the runtime bugs
fixed there, service and persistence probes, and the remaining product-scope
limitations.
