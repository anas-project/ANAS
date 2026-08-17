# Lego ACME certificates

Certificate issuance and renewal through ACME DNS-01 or the internal CA.

## Quick facts

| Item | Value |
| --- | --- |
| Module | `lego` |
| Version / revision | `5.3.1-r2` |
| Status | `release` |
| Category | `certificate` |
| Runtime | `compose` |

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| — | — | — |

## Minimal configuration

```yaml
modules:
  lego: {}
```

## Identity, users, and groups

There are no human users, directory sync, or IAM login. DNS API credentials are machine secrets.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | unsupported/not applicable |
| IAM | unsupported/not applicable |
| Group | not declared |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

There is no Web management surface or private administrator.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

This module neither consumes nor provides a relational-database contract.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Default | Environment | Required | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `lego.dns_provider` | string | `—` | `LEGO_DNS_PROVIDER` | no | no | yes | `reconcile` | Changing the ACME DNS provider requires issuing a replacement certificate with that provider. |
| `lego.dns_server` | string | `223.5.5.5` | `LEGO_DNS_SERVER` | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

### Query and modify

```bash
anas config list lego -w /srv/anas
anas config explain lego.dns_provider
anas config set lego.dns_provider VALUE -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## DNS vendors and credential scope

The DNS vendor is selected per engine; certificate issuance and dynamic DNS may use different vendors. A real domain uses ACME DNS-01, while an internal CA for a virtual domain does not require `dns_provider`:

```yaml
modules:
  lego:
    config:
      dns_provider: tencentcloud

secrets:
  tencentcloud_secret_id: replace-me
  tencentcloud_secret_key: replace-me
```

`hook/dns_registry_gen.go` and `internal/dns/providers.yml` are authoritative for vendors and credential keys. Shared vendor credentials may serve multiple engines; a `lego_<vendor>_*` key grants access only to lego. `anas plan` reports engine selection and whether credentials can be shared. lego v5 removed the legacy `dnspod` provider; use `tencentcloud`, noting that old DNSPod tokens cannot be converted into Tencent Cloud API keys.

The runner stores prefixed credentials in lego's private scope. Only the certificate worker translates them into upstream variable names while running; dependency edges do not grant DNS API secrets to other modules.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: The certificate worker receives TZ for process and log timestamps.
- Language status: `not_applicable`
- Fallback: No user-facing language exists.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list lego -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

Do not copy DNS secrets through ad-hoc environment variables; use structured secrets and module scoping.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
