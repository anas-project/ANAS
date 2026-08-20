# MeshCentral

Remote device management with OIDC-only authentication and LDAPS directory provisioning.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `meshcentral` |
| Version / revision | `1.2.4-r7` |
| Status | `release` |
| Category | `app` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `iam` | Capability | `oidc` |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres, mariadb` |

## Minimal configuration

```yaml
modules:
  meshcentral: {}
```

This module also requires a deployment-level IAM provider, for example:

```yaml
identity:
  iam:
    provider: llng
```

## Identity, users, and groups

Browser authentication is OIDC-only: the anonymous root redirects to `/auth-oidc`, the login page hides password authentication, and the server rejects local or LDAP password login. LDAPS continues to provision directory users and groups. With application filtering, `APP_meshcentral`, `APP_all`, or the administrator group grants access, and the administrator group maps to site-admin.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | ldaps (`users, groups`) |
| IAM | oidc |
| Group | `APP_meshcentral` / `APP_all`; provisions groups |
| Directory password writeback | unsupported/not applicable |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Administrator login and IAM-outage recovery

There is no separate native recovery administrator or `management.local_accounts`; restore IAM and the directory path after an outage.

| Surface ID | URL source | Primary authentication |
| --- | --- | --- |
| `web` | `MESHCENTRAL_DOMAIN_FULL` | `iam` |

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

## Database support

| Item | Value |
| --- | --- |
| Role | Consumer |
| Interfaces | `postgres`, `mariadb` |
| Default | `postgres` |
| Resource | `primary_database` |
| Credential policy | `generated` |
| Deletion policy | `retain` |

The runner creates a dedicated database, principal, and stable generated credential. Changing `db_type` or `db_name` never migrates existing data.

## All configuration parameters

This inventory comes from the current `module.yml` and `anas config list`. The environment key is the rendered module-private key, not the preferred configuration interface.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `meshcentral.db_name` | string | — | `meshcentral` | `static` | `MESHCENTRAL_DB_NAME` | no | no | no | no: `migrate-meshcentral-database` | `data_migrate` | The database name is materialized when the database is initialized. |
| `meshcentral.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `MESHCENTRAL_DB_TYPE` | no | no | no | no: `migrate-meshcentral-database` | `data_migrate` | Changing the selected database does not migrate existing MeshCentral data. |
| `meshcentral.domain_prefix` | string | — | `meshcentral` | `static` | `MESHCENTRAL_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `meshcentral.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `MESHCENTRAL_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | The OIDC client registration and generated runtime configuration must be reconciled together. |
| `meshcentral.mps_port` | int | `1..65535` | `4433` | `static` | `MESHCENTRAL_MPS_PORT` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |

### Query and modify

```bash
anas config list meshcentral -w /srv/anas
anas config explain meshcentral.db_name
anas config set meshcentral.domain_prefix meshcentral -w /srv/anas
anas config plan -w /srv/anas
```

Parameters with `editable=false` cannot be completed by ordinary `config set`. A named workflow is a lifecycle declaration, not a guarantee that a generic command of that name exists. Raw `env.<KEY>` is only a compatibility escape hatch and cannot rotate an application-internal password.

## Timezone and language

- Timezone status: `container`
- Timezone mechanism: MeshCentral receives TZ through the module .env for process and log timestamps.
- Language status: `supported`
- Supported languages (30): `ar`, `bs`, `ca`, `cs`, `da`, `de`, `el`, `en`, `es`, `fi`, `fr`, `he`, `hi`, `hr`, `hu`, `it`, `ja`, `ko`, `nl`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `sr`, `sv`, `tr`, `uk`, `zh-Hans`, `zh-Hant`
- Fallback: User localization preference and browser language are used; unmatched languages fall back to English.

## Storage, backup, and verification

Protect persistent state with the workspace snapshot/backup. Database consumers must also back up their bound database resource; generated secrets and local-administrator state must share the same recovery point.

```bash
anas plan -c /srv/anas/config.yml
anas config list meshcentral -w /srv/anas
anas status -w /srv/anas
```

## Current limitations

LDAPS is backend provisioning only; it cannot be used for browser login or as an IAM-outage fallback. Upstream MeshCentral's `showPasswordLogin=false` only hides the form, so the ANAS image also rejects password-login POSTs server-side. If an upstream upgrade changes the login handler, the image build fails and requires the patch to be reviewed.

At container startup, the module validates the IAM OIDC Discovery metadata and
waits for the issuer, authorization, token, and JWKS endpoints. If the Provider
is not ready, MeshCentral is not allowed to start and silently disable OIDC. A
persistent failure exits the container so the Compose restart policy can retry.

## Technical documentation

See [technical documentation](docs/technical.en.md) for password storage, environment scope, hooks, networks, resources, and tests.
