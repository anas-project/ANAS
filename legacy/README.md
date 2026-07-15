# ANAS

ANAS is a NAS service launcher for composing open-source services with Docker
Compose.

The repository currently has two implementation lines:

- The legacy Ruby runner lives at the repository root and uses
  `casks/mods/*/runner.rb`.
- The current Go cask runtime lives in `refactor/` and uses manifest-based
  casks under `refactor/casks/mods/*/cask.yml`.

New cask design and documentation should target the Go runtime in `refactor/`.
The current cask runtime ABI is `anas.cask/v1`.

## Go Runtime

Run commands from the `refactor/` directory:

```sh
go run ./cmd/anas plan   -c config.example.yml
go run ./cmd/anas render -c config.example.yml -b ./.runtime
go run ./cmd/anas build  -c config.example.yml -b ~/.anas
go run ./cmd/anas start  -c config.example.yml -b ~/.anas
go run ./cmd/anas stop   -b ~/.anas
```

`start --build` runs `build` first, then starts the rendered release.

## Cask Functionality

In the Go runtime, a cask is a service package under `refactor/casks/mods`.
Each cask owns its Compose file, build contexts, templates, assets, and a
`cask.yml` manifest.

The runner currently supports:

- manifest loading from `refactor/casks/mods/*/cask.yml`
- ABI validation with `anas.cask/v1`
- semantic cask versions, dependency constraints, upgrade checks, and
  `cask.lock.yml`
- dependency ordering through manifest dependencies and user `depends_on`
- structured YAML config with `global`, `secrets`, `services`, and raw `env`
- default env generation from cask manifests
- cask hook phases: `calculate`, `render_env`, `services`, and `after_start`
- hook outputs for env patches, generated secrets, rendered files, optional
  service filtering, and post-start `docker cp` operations
- ERB-compatible template rendering for existing cask assets
- per-cask `.env` generation
- Docker Compose `build`, `up`, `down`, and service selection
- persistent generated secrets in `secrets.generated.yml`
- host LAN/macvlan setup for casks that require LAN exposure

Current migrated casks cover the base runtime, certificates, reverse proxy, DNS,
Samba domain and file services, PostgreSQL, MariaDB, TURN, Nextcloud,
Collabora, LemonLDAP::NG, Keycloak scaffold, LDAP Account Manager, MeshCentral,
DDNS, NetBird, and an experimental FreeRADIUS scaffold.

See [`refactor/README.md`](refactor/README.md) for runtime usage and
[`refactor/docs/ai-design.md`](refactor/docs/ai-design.md) for the detailed
cask design guide and current cask feature table.

## Runtime State

The Go runner writes runtime state under the selected base path:

- `release/`: active rendered Compose projects
- `tmp/`: temporary render/build directory before promotion
- `secrets.generated.yml`: persistent generated secrets
- `cask.lock.yml`: installed cask versions and sources
- `go-build-cache/`: cask hook build cache

Do not commit generated runtime state or generated secrets.
