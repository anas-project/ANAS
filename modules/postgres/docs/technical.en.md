# PostgreSQL technical implementation

This page records the current implementation, security boundaries, and verification entry points for `postgres`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `18.4.0-r4` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `relational_database` | Provides contract | `1.0.0` / `postgres` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_postgres` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-postgres:18.4.0-r4` | `postgres` | 1 |
| `anas_postgres_adminer` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-adminer:5.5.0` | `postgres, traefik` | 0 |
| `anas_postgres_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-postgres:18.4.0-r4` | `postgres` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `postgres.adminer_enabled` | bool | — | `false` | `static` | `POSTGRES_ADMINER_ENABLED` | no | no | no | yes | `container_recreate` | The optional Compose service set changes. |
| `postgres.forward_auth_interface` | enum (`auto`, `http`) | — | `auto` | `static` | `POSTGRES_FORWARD_AUTH_INTERFACE` | no | no | no | yes | `container_recreate` | The gateway binding changes with the selected interface. |
| `postgres.password` | string | — | — | `generated` | `POSTGRES_PASSWORD` | no | yes | yes | no: `rotate-postgres-password` | `credential_rotate` | Change the database role and consumers before recreating containers. |
| `postgres.username` | string | — | `postgres` | `static` | `POSTGRES_USERNAME` | no | no | no | no: `migrate-postgres-owner` | `data_migrate` | POSTGRES_USER only initializes an empty data directory. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

The database service does not use directory or IAM. Every consumer gets an isolated database, role, and generated credential.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

The superuser password is a provider credential, not a local administrator. When enabled, Adminer uses database credentials.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `POSTGRES_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module provides `relational_database/postgres` contract, version `1.0.0`。

The fixed combination is PG `18.4` / Alpine with `vector 0.8.2`, `cube 1.5`, and `earthdistance 1.2`.
Required `shared_preload_libraries` is exactly empty. `postgres/Dockerfile` fixes the pgvector source
archive and SHA-256 and uses portable CPU flags. Runtime startup/provision never download or compile.
The service and provision use the same `anas-postgres` image.

Runner projects the validated comma list as `ANAS_RESOURCE_POSTGRES_EXTENSIONS`. Before writing,
the provider checks the whole allowlist, image default versions, instance HBA/preload, and existing
extension versions. It implicitly adds `cube` for `earthdistance` and always creates cube first.
Unknown, duplicate, and invalid inputs fail. Ordinary ensure creates missing extensions without ALTER.

Readiness checks fixed PG/SCRAM HBA/preload with provider privileges, then connects over TCP using
the actual application password. It verifies an ordinary role without superuser/createdb/createrole/
replication/bypassrls or granted role memberships, exact extension versions, and working vector-distance/earthdistance functions.
PostgreSQL restricts preload visibility, so the provider performs that instance check without granting
applications `pg_read_all_settings`. Inspect is read-only and returns Contract `exists/ready`; any
actual check failure returns a nonzero exit status.

`postgres/entrypoint.sh` reuses the fixed upstream entrypoint functions. New initdb uses SCRAM.
For existing directories, a TCP-disabled temporary server on a private Unix socket resets the managed
administrator password to SCRAM and atomically replaces HBA before the production TCP listener starts.
Subsequent resource ensure reprojects ordinary role passwords as SCRAM. Consumers never receive the
provider administrator credential.

## Environment ownership

### Exports

—

### Explicit consumes

—

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

PG `after_start` is the provider barrier before downstream consumers. It waits for a bounded actual TCP
administrator login and runs `anas-postgres-extensions inspect` by default. Only a Runner request after
workspace quiescence and an ANAS recovery point may carry `ANAS_POSTGRES_EXTENSION_MAINTENANCE=true`
to perform the fixed upgrade. That flag must never become persistent ordinary deployment configuration.
Backup-pause compensation still uses `compose start` for existing containers. After starting a PostgreSQL
provider, it runs the same read-only `after_start` qualification before starting downstream consumers.
A provider start or qualification failure stops compensation and retains its transaction for retry, even
when the provider has no current resource requests. Ordinary compensation cannot authorize extension upgrades.
Maintenance enumerates only databases with comment `ANAS relational_database resource` and ordinary
role owners. It preflights every source before writing, accepts only `vector 0.8.1 → 0.8.2` or target
versions, and rejects unknown sources before any upgrade writes.
The upstream 0.8.2 upgrade SQL changes no function definitions; this path needs no consumer-specific
index rebuild. Ordinary-role distance queries through an existing old HNSW index passed container tests.
Future releases/routes must reassess index maintenance instead of broadening this script implicitly.

The script checks actual versions after completion; partial cross-database runs can retry remaining known
steps. On failure, the Runner retains stopped services and the ANAS recovery point and uses existing job
failure records to block unsafe rollback. Retry with `anas apply --deployment <original-candidate-ID> -w <workspace> -y`;
the original complete recovery point and matching images remain required. Other failed candidates cannot
cross that guard. Removing a request never drops extensions; uninstall retains data. A retained PostgreSQL
provider artifact change still requires maintenance protection after every consumer is removed.
Ordinary `start`/`restart` checks persistent guards before Compose detection or restart stopping. It
preserves the `postgres_recovery_required` or `data_restore_incomplete` precondition instead of wrapping
it as a generic startup failure. PG major-version migration is unsupported.

When the provider itself was removed from the active deployment, adding a PG provider again must not
treat retained data as a fresh database. Before any container start or resource ensure, the Runner checks
existing resource records and `PreviousDeployments` artifacts. Prior PG, recorded PG resources, or missing
history that cannot be qualified require an ANAS restore containing the provider, matching images, and
complete data first. `--allow-risky`, `--no-snapshot`, and a different provider name cannot bypass this check.
A first installation without PG history or resource records remains allowed. This is conservative startup
admission protection; it does not establish acceptance for manually replaced data or other recovery scopes.

Upstream evidence: [pgvector 0.8.2 changes](https://github.com/pgvector/pgvector/blob/v0.8.2/CHANGELOG.md),
[fixed upgrade SQL](https://github.com/pgvector/pgvector/blob/v0.8.2/sql/vector--0.8.1--0.8.2.sql).

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)
- [`container-e2e.sh`](../tests/container-e2e.sh): real PG in isolated networks/volumes; fresh/repeat/restart,
  dependencies, wrong/empty passwords, administrator impersonation, cross-database isolation, read-only inspect,
  missing extensions, retain, existing trust, real 0.8.1 upgrade, and partial-state retry.
- [`pgvector-0.8.1.fixture`](../tests/pgvector-0.8.1.fixture): fixed-source old binary image for tests only.

```bash
docker build -t anas-postgres:test modules/postgres/postgres
docker build -t anas-postgres:old-vector -f modules/postgres/tests/pgvector-0.8.1.fixture modules/postgres/tests
bash modules/postgres/tests/container-e2e.sh --local-task-fixture anas-postgres:test anas-postgres:old-vector
```

## Current limitations

Ordinary `config set` cannot safely rotate the database superuser password.

The isolated Docker checks on 2026-10-03 cover native Linux arm64. An amd64 image using the same base
image digest built successfully under local emulation and passed fresh/restart/authentication/extensions/preload-drift checks. Native amd64 and real-server apply/consistent recovery
still require acceptance. This evidence does not establish real-host business acceptance for Immich, IAM,
or arbitrary other consumers.

Before a target build, Runner retains the current PostgreSQL workspace’s actual images under local references scoped to the workspace and service. Docker’s containerd image store may stop resolving an old image ID after a build replaces its tag. These fixed service references are updated before the next build; they are never pushed and do not alter other deployment tags. Recovery points still export an untagged archive by immutable ID and capture data only after writers stop.
