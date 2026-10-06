# Casdoor

IAM provider for OIDC and SAML with directory users imported from Samba AD over LDAPS.

> [!NOTE]
> The lifecycle is `release`. Formal builds for both architectures, directory-driven OIDC
> revocation, real Nextcloud anchor UID/rename/file ownership, backup restoration, credential
> rotation, and lifecycle acceptance pass. See the
> [2026-10-06 release acceptance](../../dev-docs/reviews/2026-10-06-casdoor-release-acceptance.md).
> OIDC is the primary supported protocol. SAML application-session logout is future work and does
> not block the current OIDC release. Netbird registration is rejected because
> it projects `sub` into ordinary users' API URLs.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `casdoor` |
| Version / revision | `3.143.0-r10` |
| Status | `release` |
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

Pinned Casdoor `3.143.0` registers OIDC and SAML consumers through `ANAS_IAM_CLIENT__<APP>__*`. Revision r10 builds from upstream commit `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e` and source archive checksum `365d61c…f9460`. Six controlled patches under `casdoor/patches/` add the SAML `displayName/externalId` templates, exact-`sid` OIDC user/admin back-channel delivery, two-minute Logout Tokens, delivery diagnostics, and PostgreSQL reserved-column-safe queries. OIDC access tokens are managed at one hour and refresh tokens at 30 days. A back-channel URI is registered only when explicitly declared, while declaration removal or protocol switching clears the old URI. Historical consumer E2E passed user logout, exact administrative session deletion, saved-cookie revocation, session isolation, signed claims, and replay rejection. The pinned version has no SAML LogoutRequest/LogoutResponse consumer path, so it publishes no SLO endpoint or binding and SAML consumers perform local logout.

The generic `ALLOW_GROUPS` contract is rendered as same-name Casdoor Groups/Roles plus a per-consumer Application Permission. The subscriber writes the Samba `anasIdentityAnchor` to Casdoor `ExternalId`; OIDC custom claims and the explicit SAML anchor attribute use that value while group claims come from same-name Roles. OIDC token, UserInfo and Logout Token `sub`, and SAML `NameID`, all use `ExternalId`, and a same-anchor rename reuses that record. Unknown SAML sources are omitted instead of being presented as a permanent anchor. Real consumer E2E covers signatures, attributes, group admission, rename reuse, application-account materialization, administrator mapping, and OIDC session revocation; the implementation plan records restore, upgrade/rollback, and credential-rotation evidence.

### Directory attribute changes

**Matching key**: `anasIdentityAnchor`, stored in the Casdoor user's `externalId` field. Every
`casdoor_dirwatch` batch first correlates directory objects with Casdoor shadow users by anchor and
then runs the LDAP import, so renames, disables, deletions, and group revocations all land
deterministically on **the same record**, and Casdoor's own immutable `id` is never modified. The LDAP
filter `(anasIdentityAnchor=*)` ensures objects without an anchor are never imported.

**Revision r10 issues the anchor itself as the subject.** `0005-directory-subject.patch` unifies
the four JWT formats, UserInfo, Logout Tokens and SAML 1.1/2.0; SAML 2.0 uses persistent NameID format.
Issuance rejects missing or padded anchors for `anas` users. Recovery OIDC users in `built-in` retain
their internal ID. `0006-directory-revocation.patch` rechecks identity and application admission on
refresh and preserves the issued `sid`. Initial release needs no historical account migration.
Source tests do not replace protocol or consumer-account ownership acceptance.

| Directory change | What Casdoor does | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | Reuse internal id by anchor, refresh labels, preserve anchor sub/NameID and revoke captured old sessions | r10 OIDC and signed SAML protocol pass; real Nextcloud retains uid/files and revokes old cookies |
| `mail` changes | Refresh email without identity matching or logout | Historical profile E2E and current helper tests |
| Profile attributes change | Refresh profiles and merge properties without deleting manual attributes | Helper tests pass |
| Direct/recursive group changes | Recalculate membership; lost admission revokes only the affected application and refresh rechecks admission | r10 cookie/refresh and two-client isolation pass; real Nextcloud direct-group revocation passes |
| Account disabled | Forbid, clear groups and revoke captured sessions; enable reuses identity | r10 standards-consumer saved-cookie/refresh revocation passes |
| Account deleted | Soft delete, forbid, clear groups and revoke captured sessions; consumer accounts/assets remain | r10 standards-consumer saved-cookie revocation passes; this does not certify application asset transfer |
| Identifier recycled | Quarantine a different anchor using the same name; never overwrite the old binding or restore access by label | r10 rejects the new anchor's login, preserves the old identity and reports a health conflict |

**Revocation and fallback**: disable, delete, rename and anchor conflicts revoke captured user
sessions; group admission loss revokes only the affected application. Standard OIDC back-channel
logout terminates consumer sessions. Delivery retries persist independently of the directory cursor;
missing endpoints, overdue delivery and unsupported SAML logout appear in health state.
Startup and 300-second full reconciliation repair journal gaps and remove admission for users absent
from enabled LDAP results. A reused username with a different anchor is quarantined without importing
it into the old identity; other users continue synchronizing. Recovery requires an operator.
Consumers must separately revoke offline access tokens. SAML currently requires consumer-side logout.

## Administrator recovery

The `break_glass` account follows ANAS's immutable `admin_{module}` template, producing `admin_casdoor`. Casdoor has no upstream-mandated built-in name, so the manifest does not declare `fixed_username`. Its password is generated independently and can be rotated transactionally.

```bash
anas admin local credential casdoor break_glass -w /srv/anas
anas admin local rotate casdoor break_glass -w /srv/anas
```

## Credential rotation

Casdoor declares two ANAS-managed credentials. `casdoor.signing_key` has a
one-hour X.509 trust overlap; `casdoor.portal_client_secret` invalidates its old
value as soon as the candidate passes verification. Inspect a dry-run first:

```bash
anas credential rotate casdoor.signing_key -w /srv/anas --dry-run --json
anas credential rotate casdoor.signing_key -w /srv/anas -y --json
anas credential rotate casdoor.portal_client_secret -w /srv/anas -y --json
```

See the [Casdoor IAM operations runbook](../../docs/en/operations/casdoor-iam.md)
for post-rotation checks, failure recovery, and backup procedures.

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

Revision r9 mounts the root-only deployment configuration read-only at `/opt/anas/conf/app.conf`. Before starting Casdoor, the entrypoint creates `/conf/app.conf` inside the container with UID/GID `1000` and mode `0600`. Immutable artifact permissions remain intact. Each start refreshes the copy, and initialization failures prevent readiness.

Casdoor state lives in PostgreSQL, while the directory-subscriber cursor lives under `${DATA_PATH}/casdoor/dirwatch`. Back up the database, cursor, workspace secret store, and local-administrator inventory at the same recovery point.

## Technical documentation

See [technical documentation](docs/technical.en.md) for implementation boundaries and tests.
