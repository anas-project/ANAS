# Casdoor

IAM provider for OIDC and SAML with directory users imported from Samba AD over LDAPS.

> [!WARNING]
> Lifecycle is `developing` and intended only for development and validation. Permanent directory anchors, per-application group authorization, and deactivation propagation are implemented but still lack real E2E acceptance; client-session revocation is also unaccepted.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `casdoor` |
| Version / revision | `3.143.0-r4` |
| Status | `developing` |
| Category | `identity` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Required modules, capabilities, and contracts

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres` |
| `iam` | Provides capability | `oidc, saml` |

## Minimal configuration

```yaml
identity:
  iam:
    provider: casdoor
modules:
  nextcloud: {}
```

## Identity and protocol behavior

Samba AD remains authoritative. Casdoor imports users and verifies passwords over LDAPS with a restricted read-only bind. A dedicated `casdoor_dirwatch` subscriber tails Samba's durable directory-event journal and triggers an LDAP import after debounce. It correlates shadow users by `anasIdentityAnchor`, deterministically reconciles renames, deactivation, deletion, and group revocation, and refreshes `displayName` and email only for users named by that event batch. It also resolves declared recursive group membership directly over trusted LDAPS so upstream group merging cannot retain stale access. The default five-minute schedule remains as fallback. This integration does not enable Casdoor LDAP/AD password writeback.

Pinned Casdoor `3.143.0` registers OIDC and SAML consumers through `ANAS_IAM_CLIENT__<APP>__*`. It publishes no unverified SAML SLO endpoint, so consumers perform local logout. A back-channel URI is registered only when explicitly declared; declaration removal or protocol switching writes an explicit empty value to clear the old URI, while actual notification and session revocation still require E2E acceptance.

The generic `ALLOW_GROUPS` contract is rendered as same-name Casdoor Groups/Roles plus a per-consumer Application Permission. The subscriber sets the Casdoor User ID to the Samba `anasIdentityAnchor`; OIDC `sub` and custom claims, and the explicit SAML anchor attribute, use that value while group claims come from same-name Roles. Unknown SAML sources are omitted instead of being presented as a permanent anchor. Real consumer E2E is still required before these paths count as production support.

## Administrator recovery

The `break_glass` account follows ANAS's immutable `admin_{module}` template, producing `admin_casdoor`. Casdoor has no upstream-mandated built-in name, so the manifest does not declare `fixed_username`. Its password is generated independently and can be rotated transactionally.

```bash
anas admin local credential casdoor break_glass -w /srv/anas
anas admin local rotate casdoor break_glass -w /srv/anas
```

## Database support

| Item | Value |
| --- | --- |
| Role | Consumer |
| Interfaces | `postgres` |
| Default | `postgres` |
| Resource | `primary_database` |
| Credential policy | `generated` |
| Deletion policy | `retain` |

## All configuration parameters

This inventory comes from `module.yml` and `anas config list`.

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `casdoor.db_name` | string | — | `casdoor` | `static` | `CASDOOR_DB_NAME` | no | no | no | yes | `container_recreate` | Casdoor database name |
| `casdoor.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `CASDOOR_DB_TYPE` | no | no | no | no: `migrate-casdoor-database` | `data_migrate` | Relational database interface or automatic selection |
| `casdoor.domain_prefix` | string | — | `auth` | `static` | `CASDOOR_DOMAIN_PREFIX` | no | no | no | yes | `reconcile` | Service domain prefix and all IAM endpoints |
| `casdoor.ldap_auto_sync_minutes` | int | `>= 1` | `5` | `static` | `CASDOOR_LDAP_AUTO_SYNC_MINUTES` | no | no | no | yes | `container_recreate` | LDAP automatic synchronization interval in minutes |

### Query and modify

```bash
anas config list casdoor -w /srv/anas
anas config explain casdoor.ldap_auto_sync_minutes
anas config plan -w /srv/anas
```

## Timezone and language

- Casdoor receives the container timezone.
- ANAS maps a `zh` global language to `zh` and otherwise selects `en`; users may change the UI language.

## Storage and verification

Casdoor state lives in PostgreSQL, while the directory-subscriber cursor lives under `${DATA_PATH}/casdoor/dirwatch`. Back up the database, cursor, workspace secret store, and local-administrator inventory at the same recovery point.

## Technical documentation

See [technical documentation](docs/technical.en.md) for implementation boundaries and tests.
