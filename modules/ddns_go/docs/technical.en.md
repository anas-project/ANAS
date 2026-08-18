# DDNS-GO technical implementation

This page records the current implementation, security boundaries, and verification entry points for `ddns_go`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `6.17.4-r5` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_ddns-go` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-ddns-go:6.17.4-r5` | `` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ddns_go.dns_provider` | string | — | — | — | `DDNS_GO_DNS_PROVIDER` | no | yes | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_go.domain_prefix` | string | — | `ddns-go` | `static` | `DDNS_GO_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_go.interval` | int | — | `300` | `static` | `DDNS_GO_INTERVAL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_go.ipv4_gettype` | enum (`url`, `netInterface`) | — | `url` | `static` | `DDNS_GO_IPV4_GETTYPE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_go.ipv4_interface` | string | — | `""` | `static` | `DDNS_GO_IPV4_INTERFACE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_go.ipv4_urls` | string | — | `""` | `static` | `DDNS_GO_IPV4_URLS` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_go.ipv6_gettype` | enum (`url`, `netInterface`) | — | `url` | `static` | `DDNS_GO_IPV6_GETTYPE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_go.ipv6_interface` | string | — | `""` | `static` | `DDNS_GO_IPV6_INTERFACE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_go.ipv6_urls` | string | — | `""` | `static` | `DDNS_GO_IPV6_URLS` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_go.web_enabled` | bool | — | `true` | `static` | `DDNS_GO_WEB_ENABLED` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

It does not integrate with the directory or IAM and has no user or group synchronization.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

The Web UI uses the `primary` local administrator, `admin_ddns_go` by default. ANAS generates, retrieves, and transactionally rotates it.

| Surface ID | URL source | Primary authentication |
| --- | --- | --- |
| `web` | `DDNS_GO_DOMAIN_FULL` | `local` |

| ID | Purpose | Username | Container format | Rotatable |
| --- | --- | --- | --- | --- |
| `primary` | `primary` | `admin_ddns_go` | `bcrypt` | yes |

```bash
anas admin local list -w /srv/anas
anas admin local credential ddns_go primary -w /srv/anas
anas admin local rotate ddns_go primary -w /srv/anas
anas admin local rotate ddns_go primary --prompt -w /srv/anas
```

`credential` reveals plaintext and must stay out of logs. `rotate` generates a random password by default; `--prompt` reads securely from a terminal and never accepts the password through argv or an ordinary environment variable.

### Secret boundaries

- `ANAS_LOCAL_ADMIN__DDNS_GO__PRIMARY__PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `ANAS_TRAEFIK_ROUTE__*`

### Explicit consumes

—

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`local_admin_test.go`](../hook/local_admin_test.go)
- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

The local account is the security boundary for both the domain route and direct host-port access.
