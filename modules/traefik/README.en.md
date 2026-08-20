# Traefik reverse proxy

HTTPS reverse proxy, routing layer, and dashboard for Web services.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `traefik` |
| Version / revision | `3.7.10-r4` |
| Status | `release` |
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
  traefik: {}
```

## Identity, users, and groups

The dashboard does not use directory or IAM; downstream IAM/ForwardAuth belongs to each application route.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

The dashboard uses the `primary` local administrator, `admin_traefik` by default. Its password is projected as bcrypt and ANAS can retrieve and transactionally rotate it.

| Surface ID | URL source | Primary authentication |
| --- | --- | --- |
| `dashboard` | `TRAEFIK_DASHBOARD_URL` | `local` |

| ID | Purpose | Username | Container format | Rotatable |
| --- | --- | --- | --- | --- |
| `primary` | `primary` | `admin_traefik` | `bcrypt` | yes |

```bash
anas admin local list -w /srv/anas
anas admin local credential traefik primary -w /srv/anas
anas admin local rotate traefik primary -w /srv/anas
anas admin local rotate traefik primary --prompt -w /srv/anas
```

`credential` reveals plaintext and must stay out of logs. `rotate` generates a random password by default; `--prompt` reads securely from a terminal and never accepts the password through argv or an ordinary environment variable.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `traefik.base_port` | int | `1..65535` | `9000` | `static` | `TRAEFIK_BASE_PORT` | no | no | no | yes | `container_recreate` | Published ports and derived application URLs change. |
| `traefik.domain_prefix` | string | — | `traefik` | `static` | `TRAEFIK_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | The router rule is a Compose label. |
| `traefik.forwarded_headers_trusted_ips` | string | — | `""` | `static` | `TRAEFIK_FORWARDED_HEADERS_TRUSTED_IPS` | no | no | no | yes | `container_recreate` | Comma-separated upstream proxy IPs or CIDRs allowed to supply forwarded client headers. |

### Query and modify

```bash
anas config list traefik -w /srv/anas
anas config explain traefik.base_port
anas config set traefik.base_port 9000 -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Declarative routes

The Docker provider sees only containers on a shared network. Host-network containers, processes outside Docker, and address-only services register routes through this environment contract:

```text
ANAS_TRAEFIK_ROUTE__<NAME>__RULE          required
ANAS_TRAEFIK_ROUTE__<NAME>__URL           required
ANAS_TRAEFIK_ROUTE__<NAME>__MIDDLEWARES   optional, comma-separated
ANAS_TRAEFIK_ROUTE__<NAME>__ENTRYPOINTS   optional, default https
ANAS_TRAEFIK_ROUTE__<NAME>__TLS           optional, default true
```

The declaring module must publish `ANAS_TRAEFIK_ROUTE__*` in `config.exports`. `<NAME>` is normalized into a router name. Values render as quoted YAML scalars, escaping backslashes and quotes; newlines are rejected to prevent YAML structure injection.

```yaml
env:
  ANAS_TRAEFIK_ROUTE__DDNS_GO__RULE: "Host(`ddns-go.example.com`)"
  ANAS_TRAEFIK_ROUTE__DDNS_GO__URL: "http://172.18.0.1:9876"
```

The route-declaring module decides whether to attach ForwardAuth. The Traefik dashboard's local administrator protects only the dashboard, not these upstream routes.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: Traefik receives TZ for process and access-log timestamps.
- Language status: `fixed`
- Supported languages (1): `en`
- Fallback: The built-in Dashboard is English and exposes no supported language selector.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list traefik -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

The Traefik local account protects only the dashboard and grants no downstream application administration.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
