# DDNS Updater technical implementation

This page records the current implementation, security boundaries, and verification entry points for `ddns_updater`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `2.10.0-r3` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `forward_auth` | Capability | `http` |

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_ddns-updater` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-ddns-updater:2.10.0` | `` | 0 |
<!-- generated:compose-topology:end -->

## Configuration contract

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

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Identity and authorization data flow

The application has no user database. Web requests use `forward_auth`, OAuth2 Proxy, and the selected IAM over OIDC; the default gate admits the administrator group.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | indirect: forward_auth/http → OIDC |
| Group | `Admins` (forward_auth) |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Management surfaces and secret lifecycle

There is no private administrator or local recovery account. Restore IAM/ForwardAuth before access can resume.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

The manifest declares no cross-module password/secret consumption or managed local administrator.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

—

### Explicit consumes

- `ANAS_FORWARD_AUTH_MIDDLEWARE`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

Do not bypass ForwardAuth with a direct upstream port; doing so removes all access control.
