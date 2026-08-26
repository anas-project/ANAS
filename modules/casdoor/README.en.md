# Casdoor

IAM provider for OIDC and SAML with directory users imported from Samba AD over LDAPS.

> [!WARNING]
> Lifecycle is `developing` and intended only for development and validation. Permanent directory anchors, per-application group authorization, deactivation propagation, and OIDC session revocation have real E2E acceptance; recovery, backup, upgrade, key rotation, and release lifecycle acceptance remain incomplete.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `casdoor` |
| Version / revision | `3.143.0-r7` |
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

Pinned Casdoor `3.143.0` registers OIDC and SAML consumers through `ANAS_IAM_CLIENT__<APP>__*`. Revision r7 builds from upstream commit `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e` and source archive checksum `365d61c…f9460`. Four controlled patches under `casdoor/patches/` add the SAML `displayName/externalId` templates, exact-`sid` OIDC user/admin back-channel delivery, two-minute Logout Tokens, delivery diagnostics, and PostgreSQL reserved-column-safe queries. OIDC access tokens are managed at one hour and refresh tokens at 30 days. A back-channel URI is registered only when explicitly declared, while declaration removal or protocol switching clears the old URI. Real consumer E2E passed user logout, exact administrative session deletion, saved-cookie revocation, session isolation, signed claims, and replay rejection. The pinned version has no SAML LogoutRequest/LogoutResponse consumer path, so it publishes no SLO endpoint or binding and SAML consumers perform local logout.

The generic `ALLOW_GROUPS` contract is rendered as same-name Casdoor Groups/Roles plus a per-consumer Application Permission. The subscriber writes the Samba `anasIdentityAnchor` to Casdoor `ExternalId`; OIDC custom claims and the explicit SAML anchor attribute use that value while group claims come from same-name Roles. Casdoor's immutable User ID remains the stable OIDC `sub`, and a same-anchor rename reuses that record. Unknown SAML sources are omitted instead of being presented as a permanent anchor. Real consumer E2E covers signatures, attributes, group admission, rename reuse, application-account materialization, administrator mapping, and OIDC session revocation; this does not complete recovery, key rotation, or release acceptance.

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
