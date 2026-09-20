# Casdoor

IAM provider for OIDC and SAML with directory users imported from Samba AD over LDAPS.

> [!NOTE]
> Lifecycle is `release`. Real E2E covers permanent directory anchors,
> per-application group authorization, deactivation propagation, OIDC session
> revocation, recovery administration, empty-workspace restore, multi-platform
> lifecycle, and managed credential rotation. The pinned version does not
> support SAML SLO, so no SLO endpoint or binding is published.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `casdoor` |
| Version / revision | `3.143.0-r8` |
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

Pinned Casdoor `3.143.0` registers OIDC and SAML consumers through `ANAS_IAM_CLIENT__<APP>__*`. Revision r8 builds from upstream commit `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e` and source archive checksum `365d61c…f9460`. Four controlled patches under `casdoor/patches/` add the SAML `displayName/externalId` templates, exact-`sid` OIDC user/admin back-channel delivery, two-minute Logout Tokens, delivery diagnostics, and PostgreSQL reserved-column-safe queries. OIDC access tokens are managed at one hour and refresh tokens at 30 days. A back-channel URI is registered only when explicitly declared, while declaration removal or protocol switching clears the old URI. Real consumer E2E passed user logout, exact administrative session deletion, saved-cookie revocation, session isolation, signed claims, and replay rejection. The pinned version has no SAML LogoutRequest/LogoutResponse consumer path, so it publishes no SLO endpoint or binding and SAML consumers perform local logout.

The generic `ALLOW_GROUPS` contract is rendered as same-name Casdoor Groups/Roles plus a per-consumer Application Permission. The subscriber writes the Samba `anasIdentityAnchor` to Casdoor `ExternalId`; OIDC custom claims and the explicit SAML anchor attribute use that value while group claims come from same-name Roles. Casdoor's immutable User ID remains the stable OIDC `sub`, and a same-anchor rename reuses that record. Unknown SAML sources are omitted instead of being presented as a permanent anchor. Real consumer E2E covers signatures, attributes, group admission, rename reuse, application-account materialization, administrator mapping, and OIDC session revocation; the implementation plan records restore, upgrade/rollback, and credential-rotation evidence.

### Directory attribute changes

**Matching key**: `anasIdentityAnchor`, stored in the Casdoor user's `externalId` field. Every
`casdoor_dirwatch` batch first correlates directory objects with Casdoor shadow users by anchor and
then runs the LDAP import, so renames, disables, deletions, and group revocations all land
deterministically on **the same record**, and Casdoor's own immutable `id` is never modified. The LDAP
filter `(anasIdentityAnchor=*)` ensures objects without an anchor are never imported.

**The subject identifier it issues to Consumers is not yet the anchor, and the two protocols behave
differently (the current gap):**

- **OIDC `sub` = the immutable Casdoor User ID.** Stable across a rename, but not a value that can be
  reconciled against the directory directly;
- **SAML `NameID` = the username.** **It changes when the person is renamed** — a label used as a
  subject identifier, in direct breach of `DIRKEY-R-001`. A SAML Consumer's stable correlation **must**
  use the explicit anchor attribute (mapped to `$user.externalId`) and never the NameID. That is
  exactly how `nextcloud`'s SAML mode is configured.

Both belong to the part of `DIRKEY-R-008` not yet satisfied; remediation belongs to M2 of the
[directory identity key plan](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md),
and whether the subject identifier is configurable must be settled by a probe against the real pinned
version.

| Directory change | What Casdoor does | Evidence |
| --- | --- | --- |
| `sAMAccountName` changes | The same record: correlation runs on the anchor, `id` and `externalId` both stay put, `name` (the username) is refreshed to the new value, and the old name no longer resolves. **OIDC `sub` is unchanged; SAML `NameID` becomes the new username** | rename reusing the immutable `id` and anchor while not retaining the old name: `verified`, entry `test-env/scripts/server-casdoor-directory-authority-e2e.sh` ("rename reuses the permanent identity"); one anchor resolving to exactly one immutable `sub`: `verified`, entry `server-casdoor-oidc-e2e.sh`; the NameID changing with the rename: `verified`, entry `server-casdoor-saml-e2e.sh` |
| `mail` changes | `email` is refreshed only for the users involved in the batch; it takes no part in identity matching | `verified` (email following the directory through the rename/disable/delete matrix), entries `server-casdoor-saml-e2e.sh` and `server-casdoor-oidc-e2e.sh` attribute assertions |
| `displayName` and other profile attributes | `displayName` is likewise refreshed only for the users involved; other directory attributes are merged into `properties` without deleting manually set ones | `verified`, same entries (assert `name`/`displayName` match the directory) |
| Direct or recursive group membership changes | `casdoor_dirwatch` subscribes to the persistent directory event journal, triggers a sync immediately after debouncing, and computes recursive membership directly over trusted LDAPS; the default 5-minute periodic sync remains the fallback. Both direct and recursive revocation are authoritative | `verified`, entries `server-casdoor-directory-authority-e2e.sh` ("group removals are authoritative", once each for direct and recursive) and `server-casdoor-directory-events-e2e.sh` |
| Account disabled | The shadow user is set to `isForbidden = true` and **all its groups are cleared**, so it is refused before issuance; re-enabling restores the same `id`, the same anchor, and the original groups. **Already-issued access tokens (1 hour) and refresh tokens (30 days) do not expire because of this** | disable clearing groups and re-enable reusing the same identity: `verified`, same entry ("disable and re-enable converge without replacing identity"); survival of already-issued tokens: `inferred` |
| Account deleted | Set to `isForbidden = true` and `isDeleted = true` with groups cleared, leaving the shadow identity unusable. This is a soft delete and the record remains. **Application accounts and assets on the Consumer side are unaffected** | `verified`, same entry ("delete forbids, soft-deletes, and clears access") |
| Identifier recycled and reassigned | The newcomer's anchor differs, dirwatch does not correlate them to the old shadow user, and they receive a new Casdoor `id` (fail-closed). But the old record is soft-deleted and still holds the username, so under the username uniqueness constraint the new record's `name` collides — surfacing as a sync failure rather than as the wrong person | correlation by anchor rather than by name: `verified`, same entry; how a recycled username surfaces: `inferred` |

**Fallback path** — what operations must do for every "no automatic path" row above:

1. After disabling or deleting a directory account, **the Consumers do not converge with it**. Carry
   out each Consumer's own *Directory attribute changes* revocation actions; all Casdoor guarantees is
   that the user obtains no new tokens and that existing OIDC sessions can be revoked precisely;
2. To end someone's session immediately, delete it in the Casdoor management interface — this
   propagates by exact `sid` to Consumers that declare a back-channel URI; **SAML Consumers have no
   SLO consumption path and can only log out locally**;
3. Already-issued access and refresh tokens stay valid for their TTL, so emergency revocation must act
   on the Consumer side as well;
4. **Consumer-side constraint**: a SAML Consumer attached to Casdoor **must not** use `NameID` as a
   persistent identity key and has to request and use the anchor attribute instead.

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

Casdoor state lives in PostgreSQL, while the directory-subscriber cursor lives under `${DATA_PATH}/casdoor/dirwatch`. Back up the database, cursor, workspace secret store, and local-administrator inventory at the same recovery point.

## Technical documentation

See [technical documentation](docs/technical.en.md) for implementation boundaries and tests.
