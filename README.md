# ANAS

ANAS is a Go-based NAS service launcher built around composable modules. Each
module owns its Docker Compose assets and declares its metadata in `module.yml`.

Current module hook ABI: `anas.module-hook/v1`.

Documentation: [website](https://anas-project.github.io/ANAS/) ·
[English](docs/en/getting-started/quick-start.md) ·
[简体中文](docs/getting-started/quick-start.md)

## Quick start

ANAS targets Linux hosts with Docker Engine and Docker Compose v2. A Btrfs
workspace is recommended when local snapshots are required.

```sh
# Create the self-contained workspace.
anas init /srv/anas

# Edit /srv/anas/config.yml, then validate its module selection.
anas plan -c /srv/anas/config.yml

# Build, lock, and activate the first immutable deployment.
anas apply --build --update-lock -w /srv/anas

# Inspect the active deployment.
anas status -w /srv/anas
```

When running from a source checkout, replace `anas` with
`go run ./cmd/anas`. See the [installation guide](docs/en/getting-started/installation.md)
and [complete task guide](docs/en/guide/usage.md) for prerequisites, normal
operation, snapshots, backups, and recovery.

## Configuration

Only structured YAML is supported. Module selection is a mapping, and each
module's parameters live beside its selection:

```yaml
modules:
  traefik: {}
  lego:
    config:
      dns_provider: cloudflare

global:
  base_domain: nas.example.com
  email: admin@example.com
  timezone: Asia/Singapore
  default_service_root_password: replace-with-a-strong-password

administration:
  bootstrap:
    username: admin
    display_name: Administrator
    email: admin@example.com
    roles: [platform_admin, directory_admin]
  local_accounts:
    username_template: "admin_{module}"
    password_policy: generated_per_module
    password_length: 24

secrets:
  cloudflare_dns_api_token: replace-me
```

Use `global`, `administration`, `identity`, `dynamic_dns`, `rollback`,
`secrets`, and `modules.<name>.config` for declared settings. Top-level `env`
is an escape hatch for raw environment keys without a structured field.

Inspect and change declared settings with:

```sh
anas config list
anas config explain samba_dc.user_min_pass_length
anas config set -w /srv/anas samba_dc.user_min_pass_length 10
anas config plan -w /srv/anas
anas apply -w /srv/anas
```

Do not commit real credentials or generated runtime state.

## Runtime model

One workspace contains everything owned by a deployment:

```text
<workspace>/
  config.yml          desired state
  config.lock.yml     resolved module releases and providers
  data/               application state restored by data recovery
  userdata/           user files, backed up but not changed by deployment rollback
  snapshots/          local point-in-time copies
  .anas/              protected runtime state and immutable deployments
```

`apply` is the normal deployment entry point. It materializes an immutable
artifact under `.anas/deployments/<id>/`, validates it, and only then activates
it. `start`, `stop`, and `restart` use the frozen active deployment and do not
need the source checkout or a Go toolchain.

Use deployment rollback for a bad artifact or configuration. Use snapshot
restore when persistent data itself must be rewound. Use `anas backup` instead
of copying the workspace directly.

## Development

```sh
go test ./...
npm ci
npm run docs:build
```

Developer references:

- [repository and module development](docs/developer/index.md)
- [module catalog](docs/reference/modules.md)
- [configuration reference](docs/reference/configuration.md)
- [CLI JSON contracts](docs/reference/contracts/index.md)
- [architecture and design](docs/architecture/index.md)
- [container image releases](docs/developer/release.md)

The documentation site is built with VitePress. Pull requests validate the
site; documentation changes on `master` are deployed to GitHub Pages.
