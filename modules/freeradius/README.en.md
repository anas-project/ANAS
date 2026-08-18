# FreeRADIUS

FreeRADIUS service scaffold for development and integration testing only.

> [!WARNING]
> Lifecycle is `developing`; use it for development and validation only, not recommended production deployments.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `freeradius` |
| Version / revision | `3.2.10-r3` |
| Status | `developing` |
| Category | `network` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `lego` | Module | — |

## Minimal configuration

```yaml
modules:
  freeradius: {}
```

## Identity, users, and groups

The current manifest declares no directory, IAM, user-sync, or group-mapping integration.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

There is no supported administrator login or local recovery account.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

There are currently no public configurable parameters.

### Query and modify

```bash
anas config list freeradius -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: The RADIUS service receives TZ for process and log timestamps.
- Language status: `not_applicable`
- Fallback: No user-facing language exists.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list freeradius -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Status is `developing`; do not use it as a production authentication service.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
