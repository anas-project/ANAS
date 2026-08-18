# DDNS-GO

Dynamic DNS updater with a Web UI, host IPv6 discovery, and strong Chinese DNS-vendor coverage.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `ddns_go` |
| Version / revision | `6.17.4-r5` |
| Status | `release` |
| Category | `network` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |

## Minimal configuration

```yaml
modules:
  ddns_go: {}
```

## Identity, users, and groups

It does not integrate with the directory or IAM and has no user or group synchronization.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

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

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

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

### Query and modify

```bash
anas config list ddns_go -w /srv/anas
anas config explain ddns_go.dns_provider
anas config set ddns_go.dns_provider VALUE -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Address discovery, IPv6, and configuration merge

`ipv4_gettype` and `ipv6_gettype` default to `url`, asking external services for the actually reachable address. To read a host interface, use case-sensitive `netInterface` and name the interface explicitly:

```yaml
dynamic_dns:
  provider: ddns_go
  dns_provider: tencentcloud

modules:
  ddns_go:
    config:
      ipv6_gettype: netInterface
      ipv6_interface: enp1s0
```

Empty URL lists use module defaults. `plan` rejects an unknown gettype or an unresolvable `netInterface` configuration. The container uses host networking to see host IPv6; without a global host IPv6 address it falls back to A-only updates and publishes `DDNS_GO_IPV6_AVAILABLE` for diagnostics.

Both ANAS and the Web UI author `.ddns_go_config.yaml`. `anas-ddns-go-reconcile` replaces matching `anas-managed:<id>` entries, adopts manually created entries with identical targets, rejects partial overlap, and preserves unrelated entries, webhooks, and unknown fields. `hook/dns_registry_gen.go` is authoritative for DNS vendors and credential keys.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: The service receives TZ through the module .env; upstream does not expose a separate timezone setting.
- Language status: `supported`
- Supported languages (2): `en`, `zh-CN`
- Fallback: The persisted application setting defaults to English; users can switch language in the Web UI.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list ddns_go -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

The local account is the security boundary for both the domain route and direct host-port access.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
