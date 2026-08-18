# DDNS Updater

Dynamic DNS updater for the base domain and wildcard records.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `ddns_updater` |
| Version / revision | `2.10.0-r3` |
| Status | `release` |
| Category | `network` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `forward_auth` | Capability | `http` |

## Minimal configuration

```yaml
modules:
  ddns_updater: {}
```

## Identity, users, and groups

The application has no user database. Web requests use `forward_auth`, OAuth2 Proxy, and the selected IAM over OIDC; the default gate admits the administrator group.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | indirect: forward_auth/http → OIDC |
| Group | `Admins` (forward_auth) |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

There is no private administrator or local recovery account. Restore IAM/ForwardAuth before access can resume.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `ddns_updater.dns_provider` | string | — | — | — | `DDNS_UPDATER_DNS_PROVIDER` | no | yes | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_updater.domain_prefix` | string | — | `ddns` | `static` | `DDNS_UPDATER_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_updater.forward_auth_interface` | enum (`auto`, `http`) | — | `auto` | `static` | `DDNS_UPDATER_FORWARD_AUTH_INTERFACE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_updater.publicip_dns_providers` | string | — | `all` | `static` | `DDNS_UPDATER_PUBLICIP_DNS_PROVIDERS` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_updater.publicip_fetchers` | string | — | `http` | `static` | `DDNS_UPDATER_PUBLICIP_FETCHERS` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_updater.publicip_ipv4_providers` | string | — | `all` | `static` | `DDNS_UPDATER_PUBLICIP_IPV4_PROVIDERS` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_updater.publicip_ipv6_providers` | string | — | `all` | `static` | `DDNS_UPDATER_PUBLICIP_IPV6_PROVIDERS` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_updater.publicip_providers` | string | — | `all` | `static` | `DDNS_UPDATER_PUBLICIP_PROVIDERS` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_updater.ttl` | int | — | `300` | `static` | `DDNS_UPDATER_TTL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `ddns_updater.zone_identifier` | string | — | — | — | `DDNS_UPDATER_ZONE_IDENTIFIER` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

### Query and modify

```bash
anas config list ddns_updater -w /srv/anas
anas config explain ddns_updater.dns_provider
anas config set ddns_updater.dns_provider VALUE -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Address discovery and IPv6

This module cannot read a host interface. `http` asks external echo services, `dns` asks resolvers, and `all` rotates both. Provider lists default to `all`; narrow them only when an endpoint is unreachable or slow. A single provider removes the rate-limit protection gained from rotation.

```yaml
dynamic_dns:
  provider: ddns_updater
  dns_provider: cloudflare

modules:
  ddns_updater:
    config:
      publicip_fetchers: http
      publicip_ipv4_providers: "url:https://api.ipify.org,ipify"
```

It runs on a bridge network and therefore observes the container egress address. IPv4 normally matches the host; IPv6 works only when the Docker network has IPv6 egress. Select `ddns_go` when the host interface itself must be read. Without global host IPv6, it falls back to A-only updates and publishes `DDNS_UPDATER_IPV6_AVAILABLE`.

`hook/dns_registry_gen.go` is authoritative for supported DNS vendors. `dnspod` is intentionally absent because legacy DNSPod tokens are not Tencent Cloud API credentials. The Web route must always keep ForwardAuth; never bypass it by exposing the upstream port.

## Timezone and language

- Timezone status: `application`
- Timezone mechanism: Upstream officially accepts the IANA TZ environment variable for Web UI and log timestamps.
- Language status: `fixed`
- Supported languages (1): `en`
- Fallback: English is the only UI language in the fixed source version.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list ddns_updater -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Do not bypass ForwardAuth with a direct upstream port; doing so removes all access control.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
