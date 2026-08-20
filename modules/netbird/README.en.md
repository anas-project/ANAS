# NetBird

Incomplete WireGuard overlay network module.

> [!WARNING]
> Lifecycle is `developing`; use it for development and validation only, not recommended production deployments.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `netbird` |
| Version / revision | `0.76.1-r4` |
| Status | `developing` |
| Category | `network` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `iam` | Capability | `oidc` |

## Minimal configuration

```yaml
modules:
  netbird: {}
```

This module also requires a deployment-level IAM provider, for example:

```yaml
identity:
  iam:
    provider: llng
```

## Identity, users, and groups

It declares an OIDC consumer and application group, but administrator-role mapping remains a release blocker.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | oidc |
| Group | `APP_netbird` / `APP_all` |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

There is no supported private recovery administrator or documented IAM-bypass entry.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `netbird.adminer_enabled` | bool | — | `false` | `static` | `NETBIRD_ADMINER_ENABLED` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `netbird.domain_prefix` | string | — | `netbird` | `static` | `NETBIRD_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `netbird.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `NETBIRD_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | The OIDC issuer and client configuration change together. |

### Query and modify

```bash
anas config list netbird -w /srv/anas
anas config explain netbird.adminer_enabled
anas config set netbird.adminer_enabled false -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Timezone and language

- Timezone status: `partial`
- Timezone mechanism: Dashboard, signal, and management receive the module environment; the relay service does not currently receive TZ.
- Language status: `fixed`
- Supported languages (1): `en`
- Fallback: English is the only Dashboard language in the fixed source version.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

`TURN_SECRET` is explicitly bound to `eturnal.secret` through
`credentials.consumes`. The Runner starts NetBird only after Eturnal's
credential ready barrier verifies successfully; this Module neither owns nor
rotates that value.

```bash
anas plan -c /srv/anas/config.yml
anas config list netbird -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Status is `developing` and it is excluded from recommended deployments.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
