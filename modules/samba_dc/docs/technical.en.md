# Samba domain controller and DNS technical implementation

This page records the current implementation, security boundaries, and verification entry points for `samba_dc`. User instructions are in the [English README](../README.en.md).

<!-- generated:module-identity:start -->
> Status: current implementation; based on `4.23.6-r13` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## Required modules, capabilities, and contracts

The durable directory journal also publishes the attribute names `lockoutTime` and
`accountExpires`, without values, so IAM subscribers can recheck lock and expiry state.
Consumers must still periodically reconcile expiry caused by the passage of time.
Each successful append updates `last_published_seq` in the health file immediately,
instead of waiting for the next full scan and reporting a stale journal position.

The mail candidate adds attribute names for primaryGroupID, proxyAddresses, password state, public contact details and anasMail resource versions/operations. Hook upgrades preserve configured extras and add required names without values. With mail configured, objects under the fixed Mailboxes/Addresses OUs also enter the journal. This event scope is separate from personnel/group anchor stamping, so a non-login resource Add never starts personnel-stamping retries. Producer unit coverage exists; actual upgrade and mail-consumer convergence remain pending.

| Dependency | Type | Interface/version |
| --- | --- | --- |
| `lego` | Module | — |


When `/etc/localtime` already refers to the selected `TZ` file, startup preserves it and writes `/etc/timezone`. This prevents Ubuntu's default UTC symlink from causing a same-file `cp` error that aborts directory initialization.

When no enabled Module declares capability codes, the capability-group list may be absent. Structure initialization treats it as empty and continues creating the basic groups and service accounts.

## Compose topology

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_samba_dc` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc:4.23.6-r13` | `` | 5 |
| `anas_samba_dc_anchor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc-anchor:4.23.6-r13` | `` | 3 |
| `anas_samba_dc_events_init` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-ubuntu:resolute-678c6550cc43` | `` | 1 |
<!-- generated:compose-topology:end -->

## Configuration contract

| Path | Type | Constraints | Default | Default source | Environment | Input required | Must resolve | Sensitive | Editability | Effect | Purpose |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `samba_dc.admin_complex_pass` | bool | — | `true` | `static` | `SAMBA_DC_ADMIN_COMPLEX_PASS` | no | no | no | yes | `hot_reload` | Administrator password complexity policy |
| `samba_dc.admin_lockout_duration` | int | — | `30` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_DURATION` | no | no | no | yes | `hot_reload` | Administrator lockout duration |
| `samba_dc.admin_lockout_reset_after` | int | — | `30` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_RESET_AFTER` | no | no | no | yes | `hot_reload` | Administrator failed-attempt reset interval |
| `samba_dc.admin_lockout_threshold` | int | — | `10` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_THRESHOLD` | no | no | no | yes | `hot_reload` | Administrator lockout threshold |
| `samba_dc.admin_max_pass_age` | int | — | `0` | `static` | `SAMBA_DC_ADMIN_MAX_PASS_AGE` | no | no | no | yes | `hot_reload` | Administrator maximum password age; 0 means never expires |
| `samba_dc.admin_min_pass_age` | int | — | `1` | `static` | `SAMBA_DC_ADMIN_MIN_PASS_AGE` | no | no | no | yes | `hot_reload` | Administrator minimum password age |
| `samba_dc.admin_min_pass_length` | int | — | `8` | `static` | `SAMBA_DC_ADMIN_MIN_PASS_LENGTH` | no | no | no | yes | `hot_reload` | Administrator minimum password length |
| `samba_dc.admin_name` | string | — | `admin` | `static` | `SAMBA_DC_ADMIN_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.admin_password` | string | — | — | `generated` | `SAMBA_DC_ADMIN_PASSWORD` | no | yes | yes | no: `rotate-samba-admin-password` | `credential_rotate` | The existing AD account must be updated explicitly. |
| `samba_dc.admin_password_history` | int | — | `2` | `static` | `SAMBA_DC_ADMIN_PASSWORD_HISTORY` | no | no | no | yes | `hot_reload` | Administrator password history length |
| `samba_dc.administrator_password` | string | — | — | `generated` | `SAMBA_DC_ADMINISTRATOR_PASSWORD` | no | yes | yes | no: `rotate-samba-administrator-password` | `credential_rotate` | The provision password is not reapplied after initialization. |
| `samba_dc.anchor_bind_name` | string | — | `svc_anchor` | `static` | `SAMBA_DC_ANCHOR_BIND_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.anchor_bind_password` | string | — | — | `generated` | `SAMBA_DC_ANCHOR_BIND_PASSWORD` | no | yes | yes | no: `rotate-anchor-bind-password` | `credential_rotate` | Update the AD service account and Anchor Worker as one transaction. |
| `samba_dc.anchor_scan_interval` | int | — | `300` | `static` | `SAMBA_DC_ANCHOR_SCAN_INTERVAL` | no | no | no | yes | `container_recreate` | The reconciliation interval is consumed by the Anchor Worker process. |
| `samba_dc.app_filter` | bool | — | `true` | `static` | `SAMBA_DC_APP_FILTER` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.application_dns_mode` | enum (`auto`, `ad_zone`, `separate_zone`) | — | `auto` | `static` | `SAMBA_DC_APPLICATION_DNS_MODE` | no | no | no | no: `migrate-application-dns-zone` | `data_migrate` | Application-DNS authoritative-zone mode. |
| `samba_dc.create_structure` | bool | — | `true` | `static` | `SAMBA_DC_CREATE_STRUCTURE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.dns_allowed_networks` | string | — | `""` | `static` | `SAMBA_DC_DNS_ALLOWED_NETWORKS` | no | no | no | yes | `container_recreate` | BIND recursion and cache access are restricted to these networks. |
| `samba_dc.dns_cache_size` | string | — | `128M` | `static` | `SAMBA_DC_DNS_CACHE_SIZE` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.dns_debug` | bool | — | `false` | `static` | `SAMBA_DC_DNS_DEBUG` | no | no | no | yes | `container_recreate` | The named process debug level is selected when the container starts. |
| `samba_dc.dns_forwarders` | string | — | `""` | `static` | `SAMBA_DC_DNS_FORWARDERS` | no | no | no | yes | `container_recreate` | The generated BIND configuration is installed during container initialization. |
| `samba_dc.domain` | string | `format: dns_name` | — | `inherited` | `SAMBA_DC_DOMAIN` | no | yes | no | no: `replace-directory-domain` | `immutable` | AD DNS domain (directory identity). |
| `samba_dc.ldap_bind_name` | string | — | `svc_ldap` | `static` | `SAMBA_DC_LDAP_BIND_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.ldap_bind_password` | string | — | — | `generated` | `SAMBA_DC_LDAP_BIND_PASSWORD` | no | yes | yes | no: `rotate-ldap-bind-password` | `credential_rotate` | Update AD and all LDAP consumers as one transaction. |
| `samba_dc.log_level` | string | — | `1` | `static` | `SAMBA_DC_LOG_LEVEL` | no | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_dc.mail_address_bind_password` | string | — | — | `generated` | `SAMBA_DC_MAIL_ADDRESS_BIND_PASSWORD` | no | no | yes | no: `rotate-mail-address-bind-password` | `credential_rotate` | Independent generated restricted portal address-writer credential; never granted to read-only mail service |
| `samba_dc.mail_creator_bind_password` | string | — | — | `generated` | `SAMBA_DC_MAIL_CREATOR_BIND_PASSWORD` | no | no | yes | no: `rotate-mail-creator-bind-password` | `credential_rotate` | Independent generated credential for restricted portal account creation, separate from address and personnel credentials |
| `samba_dc.mail_directory_enabled` | bool | — | `false` | `static` | `SAMBA_DC_MAIL_DIRECTORY_ENABLED` | no | no | no | no: `bootstrap-mail-directory` | `data_migrate` | Explicitly enable non-login mail bootstrap after verifying a full snapshot and stopping external writers |
| `samba_dc.mail_domain` | string | `format: dns_name` | — | `inherited` | `SAMBA_DC_MAIL_DOMAIN` | no | yes | no | no: `migrate-default-mail-addresses` | `data_migrate` | Primary mail domain; inherits the AD domain when unset and reconciles personnel addresses without renaming directory identities |
| `samba_dc.mail_initial_snapshot_id` | string | — | `""` | `static` | `SAMBA_DC_MAIL_INITIAL_SNAPSHOT_ID` | no | no | no | no: `bootstrap-mail-directory` | `data_migrate` | Verified full snapshot reference for initial mail import; the container cannot prove host-wide quiescence |
| `samba_dc.mail_initial_source_domain` | string | — | `""` | `static` | `SAMBA_DC_MAIL_INITIAL_SOURCE_DOMAIN` | no | no | no | no: `bootstrap-mail-directory` | `data_migrate` | Converged old primary domain for initial import; pinned after completion, separate from later target domains |
| `samba_dc.mail_management_policy` | string | — | `""` | `static` | `SAMBA_DC_MAIL_MANAGEMENT_POLICY` | no | no | no | yes | `container_recreate` | Complete JSON policy for managed domains, protected addresses and explicit mailbox/alias/capacity/rate limits |
| `samba_dc.max_log_size` | int | `>= 1` | `2048` | `static` | `SAMBA_DC_MAX_LOG_SIZE` | no | no | no | yes | `container_recreate` | The generated smb.conf is installed during container initialization. |
| `samba_dc.netbios_name` | string | — | — | `runtime` | `SAMBA_DC_NETBIOS_NAME` | no | yes | no | no: `replace-domain-controller` | `immutable` | Changing the DC machine identity requires a controlled replacement. |
| `samba_dc.password_bind_name` | string | — | `svc_password` | `static` | `SAMBA_DC_PASSWORD_BIND_NAME` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.password_bind_password` | string | — | — | `generated` | `SAMBA_DC_PASSWORD_BIND_PASSWORD` | no | yes | yes | no: `rotate-password-bind-password` | `credential_rotate` | Update AD and password-capable applications as one transaction. |
| `samba_dc.realm` | string | — | — | `inherited` | `SAMBA_DC_REALM` | no | yes | no | no: `replace-directory-domain` | `immutable` | Realm derived from the AD DNS domain. |
| `samba_dc.template_homedir` | string | — | `/home/%D/%U` | `static` | `SAMBA_DC_TEMPLATE_HOMEDIR` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.template_shell` | string | — | `/bin/false` | `static` | `SAMBA_DC_TEMPLATE_SHELL` | no | no | no | yes | `container_recreate` | No specialized reconciler is declared; recreate the affected container to apply rendered configuration. |
| `samba_dc.user_complex_pass` | bool | — | `false` | `static` | `SAMBA_DC_USER_COMPLEX_PASS` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_lockout_duration` | int | — | `30` | `static` | `SAMBA_DC_USER_LOCKOUT_DURATION` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_lockout_reset_after` | int | — | `30` | `static` | `SAMBA_DC_USER_LOCKOUT_RESET_AFTER` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_lockout_threshold` | int | — | `10` | `static` | `SAMBA_DC_USER_LOCKOUT_THRESHOLD` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_max_pass_age` | int | — | `90` | `static` | `SAMBA_DC_USER_MAX_PASS_AGE` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_min_pass_age` | int | — | `1` | `static` | `SAMBA_DC_USER_MIN_PASS_AGE` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_min_pass_length` | int | — | `8` | `static` | `SAMBA_DC_USER_MIN_PASS_LENGTH` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |
| `samba_dc.user_password_history` | int | — | `2` | `static` | `SAMBA_DC_USER_PASSWORD_HISTORY` | no | no | no | yes | `hot_reload` | samba-tool can update the domain policy without restarting Samba. |

`module.yml` is authoritative for the parameter inventory. The CLI combines defaults, types, required flags, environment mapping, sensitivity, and change executors. Technical docs must not invent additional settable parameters.

## Dual domains and the application-DNS plan

`BASE_DOMAIN` belongs only to the application/Web namespace.
`SAMBA_DC_DOMAIN` belongs to the provisioned directory identity and derives
`SAMBA_DC_REALM`, `SAMBA_DC_BASE_DN`, `SAMBA_DC_DNS_SEARCH`,
`SAMBA_DC_DC_DOMAIN`, and the default UPN suffix. When
`modules.samba_dc.config.domain` is absent, `validateDomainDNSConfig` falls
back to `BASE_DOMAIN` for old configurations. That compatibility path does not
permit renaming an existing directory. An explicit `realm` must equal
`upper(SAMBA_DC_DOMAIN)` case-insensitively; calculate publishes the uppercase
realm.

The requested `application_dns_mode` and its resolved result are separate
state:

| Requested | Validation and resolution | Resolved zone |
| --- | --- | --- |
| `auto` | Resolve to `ad_zone` when `BASE_DOMAIN == SAMBA_DC_DOMAIN` or the former is a label-boundary subdomain of the latter; otherwise use `separate_zone` | AD or application domain |
| `ad_zone` | Accept only the equal/label-subdomain relationship | `SAMBA_DC_DOMAIN` |
| `separate_zone` | No relationship is required except that identical domains must use the existing AD zone; the application domain must be a dedicated internal namespace ANAS can maintain completely | `BASE_DOMAIN` |

The validate hook writes `requested_mode`, `resolved_mode`, and `zone` to the
non-sensitive module plan. Text `anas plan` output includes
`module plan: samba_dc ...`; JSON exposes `module_plans.samba_dc`. After a
deployment is materialized, the same data is persisted under
`modules.samba_dc.validation_plan`. Calculate also publishes
`SAMBA_DC_APPLICATION_DNS_MODE`, `SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED`, and
`SAMBA_DC_APPLICATION_DNS_ZONE`, so the DNS reconciler never re-infers the
domain relationship at runtime.

Runner-to-reconciler `DOMAINS` is an internal protocol. It collects only Web
modules declaring `features.domain: true` and uses
`inner/<complete FQDN>/<module>`, for example
`inner/cloud.nas.example.net/nextcloud`. It neither includes
`SAMBA_DC_DOMAIN` nor truncates an FQDN to its first label. `ad_zone` derives a
multi-label owner such as `cloud.nas`; `separate_zone` derives the short owner
`cloud` inside the application zone. These Web A records point to `HOST_IP`.

The LDAPS service alias uses a separate record. For compatibility it remains
`SAMBA_DC_HOST=BASE_DOMAIN` and resolves to `SAMBA_DC_HOST_IP`. The Web
certificate covers it for ANAS LDAP consumers, but it does not participate in
the realm, Base DN, SRV records, or canonical DC FQDN. `SAMBA_DC_HOST_IP` may
differ from the `HOST_IP` used by Web records, so this alias must not be put
back into `DOMAINS`.

The service alias overlaps a Samba-native A record in two cases: it is the AD
zone apex when both domains are equal, or it is the canonical DC name when
`SAMBA_DC_HOST == SAMBA_DC_DC_DOMAIN` (for example server name `nas`, AD domain
`test.example`, and application domain `nas.test.example`). ANAS only verifies
that either directory-native record contains the exact `SAMBA_DC_HOST_IP`; it
does not claim, add, replace, or delete it. During upgrade, ownership wrongly
recorded by an earlier reconciler is released without deleting the native
record. Other exact-target records whose creation provenance cannot be proven
remain non-deletable legacy observations.

BIND starts only after application-zone reconciliation. If Samba's first
automatic DNS update runs before BIND listens on port 53, its background retry
may occur after the Compose health window. Once BIND is ready, the reconciler
therefore runs `samba_dnsupdate --all-names --use-samba-tool` once over Samba RPC and verifies that
`SAMBA_DC_DC_DOMAIN` resolves exactly to `SAMBA_DC_HOST_IP` before publishing
its ready marker.

Initial provisioning explicitly passes `SAMBA_DC_DC_NAME` and `SAMBA_DC_HOST_IP`,
so the directory's DC identity and native DNS records match the Hook's values
instead of inheriting the container host's name. Startup does not rename an
existing directory. Preserve diagnostic data from a workspace provisioned with
another DC name, then create a new directory with the correct configuration.

Zone-inventory RPC or parse failures fail closed. Before any DNS mutation, the
reconciler checks every `SAMBA_DC_HOST` and `DOMAINS` FQDN for a closer child
zone that would intercept it. An A record may be replaced or deleted only when
its provenance is present in the committed manifest or durable pending journal;
an explicit write failure withdraws pending ownership instead of promoting a
concurrent administrator-created target to ANAS ownership. Samba RPC may return
duplicate A entries for one address with different internal flags; comparisons
deduplicate by address, while any different address still fails closed.

Samba is internally authoritative for a `separate_zone`; names absent from the
managed record inventory are not forwarded to public DNS. The selected zone
and managed records are persisted with Samba state so an unmigrated zone drift
is rejected. The existing-workspace `migrate-service-domain` and
`migrate-application-dns-zone` migrators have not been delivered; only a new
workspace may select separated domains before first provision.
`SAMBA_DC_DOMAIN` itself cannot be renamed in place. A change requires a new
directory plus identity and member migration.

## Password-policy implementation

`structure.sh` passes `SAMBA_DC_USER_*` to `samba-tool domain passwordsettings set` for the ordinary-user domain policy. It generates or updates the administrator `pso_privileged` from `SAMBA_DC_ADMIN_*` and applies that PSO to built-in `Administrator` and the `Admins` group. Complexity, length, history, minimum/maximum age, and all three lockout settings come from module parameters; the script contains no policy constants.

Before reconciling the structure, startup checks the local LDAP listener up to 60 times at one-second intervals instead of sleeping for a fixed 20 seconds. One native command applies all eight ordinary-user settings, reducing repeated Samba/Python startup during recovery. Listener or policy failures still stop initialization, and the per-boot readiness marker is written only after reconciliation completes. The full Compose AD/DNS health check is unchanged.

Compose allows a 180-second startup grace period for initial directory, ACL,
and password-policy creation. Each complete check has a 30-second timeout,
with the existing 30-second interval and six failed checks. A successful check becomes ready during the grace period;
there is no fixed delay. The previous 90-second grace period triggered recovery
while initialization was progressing on a constrained host. The revised window
requires verification with a newly provisioned directory. Full-stack startup
also repeatedly exceeded the old 10-second check timeout, while the complete
check succeeded when run separately. All AD/DNS checks remain enabled; the new
budget requires full-stack verification.

All 16 policy parameters declare `hot_reload`/`samba-password-policy`. The current executor still uses the deployment apply fallback; container initialization idempotently updates the domain policy and any existing PSO.

## Identity and authorization data flow

It is authoritative for people, groups, service identities, and identity anchors. Use LAM or Samba tools to create/disable users and manage groups. `svc_ldap` is read-only, `svc_password` may change ordinary-user passwords only in its delegated scope, and `svc_anchor` manages identity anchors and system primary addresses.

| Capability | Current declaration |
| --- | --- |
| Directory / LDAPS | provider: AD / ldaps / kerberos (`users, groups, service accounts`) |
| IAM | unsupported/not applicable |
| Group | authoritative group source |
| Directory password writeback | authoritative directory; controlled by ACLs and password policy |

There is currently no generic `anas user/group/password` command. Directory-backed modules synchronize through their own mechanisms. Manage users, groups, and directory passwords in Samba AD/LAM or an application with restricted LDAPS password writeback; neither `anas config set` nor `env.<KEY>` is a directory operation.

## Mail directory schema under development

The approved model uses primary/additional mailbox resources and address claims under one personnel login. `samba_dc/root/usr/local/share/anas/mail-schema.json` fixes two non-login structural classes, `anasMailbox` and `anasMailAddressClaim`, plus 13 attributes with registered OIDs/GUIDs. Each mailbox has its own anasMailboxID and references personnel through anasMailOwnerAnchor; resources carry no personnel anasIdentityAnchor, sAMAccountName, UPN or password. Personnel proxyAddresses retains effective aliases while claims identify their target mailbox.

The development `install-mail-schema.py` defaults to read-only checking, also available as `--check`; `--install` explicitly adds missing definitions. It preflights every name/OID/GUID, type and field, refusing collisions, defunct objects or drift without overwriting schema. Interrupted installation resumes only exact definitions; join mode only verifies existing schema. It creates no OUs/ACLs, changes no personnel or primary-address policy, and is not wired into startup. Released images are not assumed to contain it. Existing business domains remain unchanged; formal migration requires a verified full-volume snapshot and an explicit deployment scope.

Class definitions specify domain administrators as owner, grant domain administrators/SYSTEM access and also limit implicit WRITE_DAC through OWNER RIGHTS. Live ACL verification is required; SDDL text alone does not establish enforcement. The development `install-mail-directory.py` now prepares fixed OUs and separate roles. It is not deployed; isolated checks do not establish production bindings. The security-descriptor owner governs directory ACL administration; it neither changes personnel ownership through anasMailOwnerAnchor nor grants mail login. Schema alone does not enforce owner foreign keys, one primary box per person, address normalization or lifecycle rules; controlled writers, read-back checks and fail-closed consumption must enforce them.

Shared Go locators in `internal/directorymail/resources.go` fix the Mailboxes/Addresses OUs, derive primary-box locators from personnel anchors and additional-box locators from mailbox UUIDs, and derive one claim DN from the normalized complete address regardless of its type. The locator library performs no LDAP writes or authorization. Candidate portal creation uses these locators; a Python LDAP coordinator now covers system-primary writes; alias/additional-box writers and the mail projection remain unfinished.

Checks are `python3 -m unittest discover -s modules/samba_dc/tests -p 'test_mail_*.py'` and `test-env/scripts/server-mail-directory-schema-e2e.sh`, which requires a dedicated Docker daemon, a new private directory, a fresh test domain and explicit `ANAS_ANCHOR_IMAGE` selecting a cached fixed Anchor Worker image. The root mail plan records actual results; this subset does not accept existing-domain migration, all writer races, address transfers, events or DMS/Roundcube integration.

### Explicit mail OU and ACL preparation

`install-mail-directory.py` reuses Samba's bundled Python/ldb/SamDB without a new dependency or service. It first checks the complete mail schema without changes, then validates the selected domain, its single writable schema-master DC, personnel/service OUs and four existing service roles inside one local transaction. Roles must be distinct enabled ordinary users directly in the Service Accounts OU, with Domain Users as primary group, no additional groups, privileged primary-group ancestry, adminCount or SID history. The tool creates no accounts and changes no passwords, membership, personnel or schema.

Non-secret JSON on stdin (maximum 64 KiB) contains `base_dn`, `people_dn` and `roles` with `reader`, `creator`, `address_writer`, `primary_writer` DNs. For example, use the existing svc_ldap and svc_portal_create, plus a separately prepared svc_portal_mail and the existing svc_anchor, all under the provider's Service Accounts OU. `SAMBA_DC_BASE_DN` must match the current domain. Default/`--check` performs no writes and rejects missing preparation. Explicit `--prepare` creates protected Mail/Mailboxes/Addresses OUs owned by domain administrators; join mode rejects preparation. Existing OUs require exact ownership markers, owner/group and permissions. Drift stops execution without adoption or replacement. OU creation, scoped grants and read-back share one transaction; failures roll everything back and repeated complete preparation makes no changes.

The reader only reads resources. The creator can create the specified resource classes, advance state/version/operation fields and create users under People; it cannot change existing personnel mail/proxies or a mailbox's ID/personnel owner. The address writer can create resources and maintain default address/quota and claim mapping/history; its only personnel write is proxyAddresses under People. It cannot write system mail or login names, or transfer the mailbox itself. The primary writer reuses the Anchor Worker. It can create resources, advance operations/versions, update defaults/address kinds, retain existing personnel anchor/mail rights and write personnel proxyAddresses. It cannot rename people, change mailbox identity/owner, or delete resources. Each role receives current-domain mail/proxyAddresses read and directory listing for legacy collision checks. This does not establish administrator authorization or replace application validation. Unrelated domain/People ACEs are preserved; unexpected explicit grants to these services stop preparation. No delete, ACL-change, ownership or domain-role rights are delegated. AD permits the creator to supply initial Add fields, so subsequent attribute restrictions cannot replace controlled API ownership checks.

The tool is not wired into startup, Hook or existing portal provisioning. It does not create or distribute the example address-writer account. Formal migration still requires a verified full-volume snapshot, existing-address import, all controlled writers and effective namespace visibility for the actual binding. Protected-object ACLs, external grants and later changes need separate checks; inherited read ACEs alone do not prove complete visibility. A portal-only image update is not a completed upgrade.

### Existing-personnel primary import candidate

`migrate-mail-primary.py` reuses the anchor worker's `mail_policy.py`. It writes no personnel, aliases, passwords, groups, schema or existing resources. Compose and the CI image catalog now use the Module root as the DC build context with `samba_dc/Dockerfile`, allowing the same policy source to be copied. A `.dockerignore` allowlist includes only DC root, Dockerfile and that policy, excluding runtime `.env`, tests and other Module files. The anchor image keeps its existing context. An isolated scratch COPY probe checks the actual context; it does not establish a rebuilt Ubuntu/Samba release image.

Stdin uses the preceding preparation JSON plus `mail_domain`. Default/`--check` returns planned/existing primary counts and unmapped SMTP counts without changes; this is not mail readiness. Output `planned` records the preflight addition count for this run, `created` records confirmed additions, and `existing` records reused resources. Personnel must have paired stable anchors, native objectGUID/SID, a normal account type and a primary mail value already converged under current system policy. Missing mail, a pending name/suffix change or malformed identity stops import instead of replacing the primary-address worker. It checks domain-wide mail/SMTP proxies, personnel anchors and managed boxes/claims. Conflicts, missing completed claims, unknown/pending operations, wrong DNs, defaults referenced by another box and deleted/recreated people are rejected. Completed portal-created primaries are reused by anchor/objectGUID rather than profile labels.

Explicit `--execute --snapshot-id ID --evidence-dir PATH` requires root, a stopped DC, a private evidence parent outside Samba data and a new evidence subdirectory. The snapshot ID labels a full snapshot already verified by the caller; the script neither creates nor verifies an ANAS snapshot and cannot prove that all host/external LDAP writers stopped. Business migration still needs those maintenance steps; an arbitrary label is not snapshot acceptance.

Preflight, new primary/claim Adds and read-back share one local SamDB transaction, rolling back on failure. Each box receives an independent random ID and binds the original personnel objectGUID in operation data. Repetition validates rather than recreating or rewriting resources. Evidence intent precedes commit and result follows it. A failed result-file write after commit is unknown: inspect the directory and use a new evidence directory before retrying rather than inferring absence from a lost response.

Only converged personnel primaries are imported. Existing SMTP proxies and non-SMTP values remain intact. Aliases with no explicit box mapping are counted, never merged into the primary or assigned claims automatically. Existing ownership is never transferred. AD active/complete means directory configuration only; DMS, cloud routes, Webmail and local projection still need confirmation. Startup, Hook and ANAS upgrade do not invoke this candidate. The system-primary/suffix coordinator is described below; the business domain remains unchanged.

### System primary addresses and shared claims

The Anchor Worker reuses its verified LDAPS connection and existing python3-ldap dependency. Once the mail schema is present, it requires the marked Mail OU and uses `mail_resources.py` / `mail_resource_ldap.py`. Missing/invisible OUs, insufficient rights and unknown operations fail without falling back to mail-only writes. Domains without that schema retain the existing policy. Enabling this candidate requires schema, four-role ACL preparation and existing-primary import before starting writers; replacing only the Worker image is insufficient. Add `primary_writer` pointing to the existing `svc_anchor` service account in the bootstrap JSON.

The primary locator uses the personnel anchor and binds the original objectGUID. Durable box intent and the deterministic target claim precede the atomic personnel mail/proxy update, followed by claim/box confirmation. Pending work resumes the same operation after failures or lost responses without allocating a second mailbox or releasing claims. Missing completed claims, replaced people, duplicate anchors and foreign mail/proxy/claim ownership fail closed.

Suffix changes retain the mailbox UUID and old address as an active same-box alias, preserving every existing SMTP/non-SMTP proxy. Switching back can reuse one's active suffix alias; retained, disabled or foreign pending claims are not adopted. Username renaming remains disabled. A single strict Modify deletes the old resource version and adds its replacement. Personnel writes use a GUID locator and compare both anchors plus mail/proxy preimages through strict Delete/Add in one Modify. Concurrent unrelated proxy additions survive; inconsistent read-back remains pending. Username and scope are rechecked before/after writing, without an atomic username lock or a rename workflow. RootDSE does not advertise RFC4528, so this uses the same native atomic Modify primitive as the portal. Downstream projection, revocation, DNS and real delivery remain independent checks. The attempted RFC4528 control API is documented in [python-ldap](https://www.python-ldap.org/en/latest/reference/ldap-controls.html) and [RFC 4528](https://datatracker.ietf.org/doc/html/rfc4528); the live Samba test rejected it, so it is not a runtime dependency.

This is development behavior. The root mail plan §4.20 records actual fixed-image and fault-test results; the business service on port 10000 is not upgraded. Earlier three-role candidate OUs are not automatically granted new rights; in-place upgrade remains unfinished and drift still fails.

The portal address candidate stores `anas.mail-management/v1` in the primary mailbox's `anasMailQuotaState`, containing one pending operation and the last receipt. A system suffix change refuses to pass a pending personal operation, then competes on the same primary configuration version. An unchanged-address check may remain a no-op. When a former primary becomes an alias, `system.primary.previous` marks it so a former personal alias cannot accidentally remain removable after serving as a primary. This grants the portal no personnel mail write and does not let the Worker execute personal operations. Native results and limits are in root mail plan §4.21.

## PEN 66678 schema-migration contract

In r11, the current `anasIdentityAnchor` uses `attributeID=1.3.6.1.4.1.66678.1.2.1` and `schemaIDGUID=db3786ae-3261-4d44-a2a1-588bfe3e41c5`. The legacy OID `1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1` and GUID `7108c5a7-2290-45e0-9eba-eef087be58e3` remain only on the renamed defunct schema object and are never reused. The migrator is [`migrate-identity-anchor-oid.sh`](../samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh) and supports only one writable DC that owns the Schema Master FSMO role.

This is an offline maintenance transaction with full-volume rollback:

1. Render, but do not start, the exact `4.23.6-r11` candidate before the upgrade. Stop the whole workspace and exclude every host-level writer: the DC, worker, Consumers, backups, scheduled jobs, and interactive LDAP/LDB sessions. An in-container check cannot prove that the host is quiesced.
2. After quiescence, create a real cold snapshot, preserve the snapshot ID actually returned by the tool, and require a successful `anas snapshot verify`. `--snapshot-id` only records an audit label and does not prove that a snapshot exists or is restorable.
3. After read-only `--check` confirms the exact legacy state, `--execute` exports legacy values, removes class links, marks the old object defunct in a dedicated operation, changes `lDAPDisplayName` and the RDN separately, creates the new schema object, restores values, and rebuilds the class links.
4. `--execute` requires an explicit `--backup-dir /mnt/anas-migration-evidence/<new-dir>` outside the Samba data volume. It must be a new per-execution child directory; because it contains DNs and stable identifiers, protect it as sensitive directory data.
5. Final-state `--check` validates the final/legacy schema, User/Group class links, Windows `bytes_le` equality between every text anchor and `mS-DS-ConsistencyGuid`, and global uniqueness. After r11 starts, also prove that OU ACLs use the new GUID and omit the legacy GUID, perform a real `svc_anchor` write, check worker health, and validate every Consumer for duplicate accounts or authorization drift.

If any step fails after `--execute` starts, stop retrying and restore the validated **entire Samba data-volume snapshot**. Never delete the migration marker, restore only `sam.ldb`, or hand-edit forward from a checkpoint. External evidence is not rolled back with the data volume and must be retained for investigation. See the [migration runbook](../../../docs/en/guide/migrate-identity-anchor-oid.md) for the complete commands and acceptance order.

## Management surfaces and secret lifecycle

`admin_name` defines the routine directory administrator. Built-in RID 500 `Administrator` is reserved for provisioning and low-level recovery. Their independent passwords are not `anas admin local` accounts.

This module declares no account managed by `anas admin local`; `credential` and `rotate` are unavailable for it.

### Secret boundaries

- `SAMBA_DC_ADMINISTRATOR_PASSWORD`
- `SAMBA_DC_ADMIN_PASSWORD`
- `SAMBA_DC_ANCHOR_BIND_PASSWORD`
- `SAMBA_DC_LDAP_BIND_PASSWORD`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

Generated values and lifecycle-managed credentials use stable logical keys in workspace `.anas/secrets.yml` (`0600`). It is permission-protected plaintext, not an encrypted vault. Plaintext must not enter README files, locks, logs, or ordinary `config list`. Local-administrator names and secret references live in password-free `.anas/local-admins.yml`; hooks receive plaintext only for the required lifecycle phase. `bcrypt` accounts persist only a hash in runtime configuration, while `plaintext_on_bootstrap` accounts use a `0600` projection at `.anas/runtime-secrets/local-admins/<module>/<id>.password`. Snapshots/backups must keep the secret store, account inventory, and application data at one recovery point.

## Database support

This module neither consumes nor provides a relational-database contract.

## Environment ownership

### Exports

- `ANAS_DIRECTORY_EVENTS_DIR`
- `ANAS_DIRECTORY_EVENTS_FILE_NAME`

### Explicit consumes

- `DOMAINS`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `ANAS_TLS_TRUST_BUNDLE_NAME`
- `LEGO_CA_CERT_NAME`
- `LEGO_CERTS_PATH`
- `LEGO_CERT_NAME`
- `LEGO_KEY_NAME`
- `ANAS_IDENTITY_APP_CLIENTS`
- `ANAS_IDENTITY_CAPABILITY_GROUPS`

The dependency closure does not grant every environment value. Sensitive values enter this module's hook/container scope only through ownership or an explicit `config.consumes` claim.

`ANAS_IDENTITY_CAPABILITY_GROUPS` is an optional comma-separated list of capability codes published by enabled module hooks; the key may be absent when there is no publisher. The structure script treats an absent or empty list as empty, creates no capability OU/group, and continues directory and administrator initialization. A nonempty list creates the corresponding `CAP_*` groups only when `create_structure=true`; `set -u` still rejects missing required environment values.

## Hooks, changes, and rollback

- Hook command: `go run ./hook`
- `credential_rotate`, `data_migrate`, and `immutable` are blocked from ordinary edits; the declared lifecycle operation must update persistent application state.
- A local-administrator rotation commits the generated secret only after the module handler succeeds; failure keeps or restores the old application credential.

## Tests and implementation locations

- [`main_test.go`](../hook/main_test.go)
- [`structure_script_test.go`](../hook/structure_script_test.go): executes the production capability block in Bash with absent, empty, and nonempty lists, disabled structure creation, and missing required environment. Only directory writes are replaced; Samba data and `/run` are untouched.
- [`zone_script_test.go`](../hook/zone_script_test.go)
- [`domain_dns.go`](../hook/domain_dns.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## Current limitations

`domain`, `realm`, and `netbios_name` are immutable identities; password
parameters require dedicated credential rotation and cannot be changed by
recreating containers. The service-domain/application-zone migrators for
existing workspaces have not been delivered. Ordinary apply cannot switch the
zone, and an AD domain cannot be renamed in place.

## Application catalog

No personnel card is published: Directory and DNS protocols only; administration is published by LAM.

The explicit ANAS group registry also includes Unix Admins. Registration marks ownership, not a low-risk permission: this group inherits infrastructure administrator rights and the portal must handle it through the domain-level path.

### System primary addresses

`mail_domain` defaults to the AD DNS domain. Bootstrap supplies `--mail-address` when creating the routine admin. The existing directory worker owns primary `mail` values for human accounts in the provider user OU and its descendants, using lowercase `username@domain`. It preflights registered mail/proxyAddresses conflicts before reconciliation, verifies writes and reports failures through its readiness check. Existing manual primary values in this scope are replaced by the system rule. Outside entries, service OUs, built-ins, trusts and computers are excluded. The writer receives only attribute-scoped mail rights in the personnel OU; anchor rights on groups do not confer mail rights there. No mailbox, alias, new identity schema or additional synchronization service is created. A suffix change affects mail only; operators must check readiness and remaining policy errors rather than infer success from container startup.

A mail-suffix change currently uses the native config import → lock → render → apply workflow. Prepare a complete configuration with `modules.samba_dc.config.mail_domain`, review the affected modules and recovery point, then activate and verify worker readiness. The policy name `migrate-default-mail-addresses` is metadata, not a newly registered CLI command; ordinary `config set` is blocked for this data-migration setting. Existing mail and alias delivery migration is a later mail-backend task.


## Single source for group declarations

`identity.groups` in `module.yml` declares common group names, directory parents, categories, order, risk and bilingual descriptions, plus SID/RID-located descriptions of AD built-ins. Application groups reuse existing identity declarations; capability names come from their owning Module's declared list. Core compiles one deployment-scoped `shared/identity-groups/catalog.json`. Samba's `identity_groups.py` consumes the read-only catalog to create managed groups and maintain their English AD `description`. It does not modify built-ins, move existing same-name objects, or derive membership/ACL changes from metadata. Native bootstrap membership and rights policy remains explicit.

`SAMBA_DC_APPLICATION_CATALOG_GROUPS` is a CSV projection of managed catalog names rather than a second Hook list. Missing or invalid catalogs and conflicting locators fail initialization without reverting to hardcoded defaults. Primary mail and identity-anchor maintenance retain their existing privilege boundaries.

The audit follower emits only newline-terminated records. It retains the start of an unfinished
line for the next poll, including a UTF-8 character split across writes. An incomplete tail from
a rotated file is never concatenated with its replacement.

Candidate formal mail bootstrap is explicitly enabled by mail_directory_enabled and requires the complete mail_management_policy, converged old mail_initial_source_domain and verified full mail_initial_snapshot_id. Two independently generated restricted identities serve portal creation/address maintenance; the existing LDAP reader and Anchor Worker serve reader/primary_writer. Before the Samba daemon starts, the fixed script verifies a single writable schema-master, installs the confirmed two non-login classes, prepares/checks role and OU/ACL scopes, and calls the existing primary import tool. Failures leave no completion marker and retry remains idempotent. Completion pins the initial source domain, snapshot and role SIDs; subsequent boots only check, without reimporting the old domain or adopting replacement roles. ANAS must still stop external writers and verify the full snapshot before import; unit tests cannot prove these prerequisites. Private migration evidence lives under DATA_PATH/samba_dc/mail-bootstrap and belongs in the recovery set. Business bootstrap acceptance remains pending. The Anchor container receives only its required ANCHOR_* bindings and TZ instead of the entire .env with administrator and other writer secrets.

Formal bootstrap stops before primary-import writes when legacy SMTP aliases lack an explicit mapping; it never infers mailbox ownership from old proxy values. server-mail-bootstrap-runtime-e2e.py defines actual isolated bootstrap and two-phase company-directory checks. Until executed it remains pending and establishes neither business-domain nor complete lifecycle acceptance.
