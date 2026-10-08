# Immich

Photo and video upload, mobile backup, albums, sharing, and search. This module is developing: deployment
and pinned-version account guards exist; real IAM/directory sync, mobile clients, and full-workspace
recovery remain acceptance gates.

## Quick facts

<!-- generated:module-facts:start -->
| Item | Value |
| --- | --- |
| Module | `immich` |
| Version / revision | `3.2.4-r1` |
| Status | `developing` |
| Category | `app` |
| Runtime | `compose` |
<!-- generated:module-facts:end -->

## Dependencies

Traefik, the OIDC IAM Capability, and the shared PostgreSQL relational_database Contract. Fresh databases
only; existing local accounts are not imported. The module owns Valkey and an optional ML service.

## Minimal configuration

```yaml
identity:
  iam:
    provider: authentik
modules:
  immich:
    config:
      machine_learning: false
      job_concurrency: 2
      video_concurrency: 1
```

The default URL is `https://photos.<BASE_DOMAIN>:<TRAEFIK_BASE_PORT>`. This version selects Authentik
`2026.5.6-r15`, with trusted administrator roleClaim and directory revocation wiring; real login/sync
acceptance is pending. LLNG has the role mapping but lacks the required revocation capability. Casdoor
cannot express the role mapping; unsupported combinations fail during configuration. Password login and local
admin setup are disabled. The first account must receive `anas_role=admin` from the trusted administrator
group; the first ordinary visitor is refused. There is no emergency local account. Restore IAM, directory,
DNS, and CA availability when authentication fails.

## Identity and revocation

OIDC sub requests the Samba permanent anchor, stored as Immich oauthId. The upstream internal user.id
remains unchanged; the anchor is not a storageLabel or media directory. A returning sub identifies the
original account; email is profile data and a conflict check. The pinned server refuses unbound/local
creation, password writes, OAuth binding changes/unlink, and administrator unlink-all.

| Directory change | Current behavior and acceptance status |
| --- | --- |
| Login name, email, display name | sub should remain the anchor; the same sub retains the internal id. Profile refresh is not guaranteed; real IAM rename acceptance is pending |
| Group membership | IAM checks APP_immich/APP_all/administrator admission at new login; admin/user role updates at the OAuth callback |
| Disabled/deleted account or admission removed | Pinned Authentik sends negotiated signed policy events after successful LDAP sync or deletion; the receiver revokes old sessions/API keys/shares. The actual directory chain remains unverified |
| Anchor reassigned or replaced | Rebinding is unsupported; real PG fixtures verify concurrent uniqueness and refusal to create a new soft-deleted binding; actual IAM/host acceptance remains pending |

Upstream supports RP logout and `/api/oauth/backchannel-logout`; the module registers that receiver.
Ordinary logout deletes the current session. Backchannel logout deletes matching sid/sub sessions and
does not revoke API keys or shares. Browser behavior, valid/invalid tokens, and old cookies still require
acceptance. A pinned patch persists verified-token jtis so replay cannot revoke subsequently created
sessions. Signed-fixture valid/invalid tokens, old Cookies, concurrent replay, and replay across restart
passed; actual IAM/browser/mobile acceptance remains pending. Bidirectional logout is not declared complete.
Directory revocation uses a separately negotiated event member and retains users/media. Delayed old
callbacks and derived credentials are rejected by verified ID Token issue time; retries preserve the
original event time. Ordinary logout retains independent long-lived credentials.

## Data, queues, and recovery

Managed media uses `data/immich/media` → `/data`; queue AOF uses `data/immich/valkey`; reconstructable
models use `data/immich/model-cache`. The PostgreSQL Provider owns database storage; Immich receives only
its ordinary application role. Before application startup the Provider supplies fixed `vector`,
`earthdistance`, and dependencies; the application explicitly selects `pgvector`.

Immich scheduled database backup is disabled. Use ANAS full-workspace backup with matching database,
media, config/Secrets, and images; restoring also rolls back other shared PG consumers. Uploads after the
recovery point may be lost. Real consistent recovery has not passed. Read-only external libraries have no
automatic mount configuration yet: explicitly verify source-file backup scope, and do not assume AD/Samba
ACL inheritance or inclusion in managed-media backup. Do not edit generated deployments to add mounts
that cannot be frozen.

Local real AOF pending-job recovery, queue ENOSPC health detection, and upload failure tests passed.
Unwritable queues do not report successful upload. The new asset row is rolled back before cleanup
enqueue, allowing healthy retry; unqueued temporary files may require later inspection.

## All configuration parameters

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `immich.db_name` | string | — | `immich` | `static` | `IMMICH_DB_NAME` | no | no | no | no: `migrate-immich-database` | `data_migrate` | Application database name; changing it requires a matching media migration |
| `immich.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `IMMICH_DB_TYPE` | no | no | no | no: `migrate-immich-database` | `data_migrate` | Shared PostgreSQL only |
| `immich.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `photos` | `static` | `IMMICH_DOMAIN_PREFIX` | no | no | no | yes | `container_recreate` | Public photo service subdomain prefix |
| `immich.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `IMMICH_IAM_PROTOCOL` | no | no | no | yes | `container_recreate` | OIDC only; automatic selection must resolve to OIDC |
| `immich.job_concurrency` | int | `1..16` | `2` | `static` | `IMMICH_JOB_CONCURRENCY` | no | no | no | yes | `container_recreate` | Concurrency for non-video background queues |
| `immich.machine_learning` | bool | — | `true` | `static` | `IMMICH_MACHINE_LEARNING` | no | no | no | yes | `container_recreate` | Enable both application ML settings and the dedicated ML service |
| `immich.video_concurrency` | int | `1..8` | `1` | `static` | `IMMICH_VIDEO_CONCURRENCY` | no | no | no | yes | `container_recreate` | Video conversion queue concurrency |

Disabling ML retains photo/video upload and disables ML-dependent search/recognition. No whole-system
load acceptance has been performed; 4GB support is not claimed.

## Language and time zone

<!-- generated:localization:start -->
<!-- generated:localization:end -->

[Technical implementation](docs/technical.en.md) covers pinned images, account guards, and verification limits.
