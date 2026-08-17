# FreeRADIUS technical implementation

This page records the current implementation, security boundaries, and verification entry points for `freeradius`. User instructions are in the [English README](../README.en.md).

> Status: current implementation; based on `3.2.10-r1` / `anas.module/v1`.

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `lego` | Module | — |

## Compose topology

| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_freeradius` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-freeradius:3.2.10` | `` | 0 |

## Configuration contract

There are currently no public configurable parameters.

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

The current manifest declares no directory, IAM, user-sync, or group-mapping integration.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

There is no supported administrator login or local recovery account.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

The manifest declares no cross-module password/secret consumption or managed local administrator.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

—

### Explicit consumes

—

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: ``
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- There are currently no hook unit-test files.
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

Status is `developing`; do not use it as a production authentication service.
