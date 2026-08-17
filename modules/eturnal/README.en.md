# Eturnal TURN

TURN relay for realtime communication modules.

## Quick facts

| Item | Value |
| --- | --- |
| Module | `eturnal` |
| Version / revision | `1.12.2-r3` |
| Status | `release` |
| Category | `communication` |
| Runtime | `compose` |

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |

## Minimal configuration

```yaml
modules:
  eturnal: {}
```

## Identity, users, and groups

This protocol service has no human users, directory sync, OIDC, SAML, or group administration. Consumers use a generated TURN shared secret.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

There is no Web administration surface or local administrator.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Default | Environment | Required | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `eturnal.domain_prefix` | string | `turn` | `TURN_DOMAIN_PREFIX` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `eturnal.port` | string | `3478` | `TURN_PORT` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

### Query and modify

```bash
anas config list eturnal -w /srv/anas
anas config explain eturnal.domain_prefix
anas config set eturnal.domain_prefix turn -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: The TURN service receives TZ for process and log timestamps.
- Language status: `not_applicable`
- Fallback: No user-facing language exists.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list eturnal -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

The TURN secret is a machine credential and must not be treated as a human password.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
