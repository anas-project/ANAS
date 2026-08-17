# Lego ACME certificates technical implementation

This page records the current implementation, security boundaries, and verification entry points for `lego`. User instructions are in the [English README](../README.en.md).

> Status: current implementation; based on `5.3.1-r2` / `anas.module/v1`.

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| — | — | — |

## Compose topology

| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_lego` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-lego:5.3.1-r2` | `` | 1 |

## Configuration contract

| Path | Type | Default | Environment | Required | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `lego.dns_provider` | string | `—` | `LEGO_DNS_PROVIDER` | no | no | yes | `reconcile` | Changing the ACME DNS provider requires issuing a replacement certificate with that provider. |
| `lego.dns_server` | string | `223.5.5.5` | `LEGO_DNS_SERVER` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

There are no human users, directory sync, or IAM login. DNS API credentials are machine secrets.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

There is no Web management surface or private administrator.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

The manifest declares no cross-module password/secret consumption or managed local administrator.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `ANAS_TLS_*`

### Explicit consumes

—

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- There are currently no hook unit-test files.
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

Do not copy DNS secrets through ad-hoc environment variables; use structured secrets and module scoping.
