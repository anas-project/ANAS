# Samba AD identity anchor

> Status: **current model**. `mS-DS-ConsistencyGuid` and `anasIdentityAnchor` are both in use;
> consuming Modules reference the anchor through their `module.yml` contracts.

ANAS maintains two representations of one immutable identity:

| Purpose | LDAP attribute | Format |
| --- | --- | --- |
| AD and Microsoft Entra source anchor | `mS-DS-ConsistencyGuid` | 16-byte AD GUID |
| Application identity key | `anasIdentityAnchor` | lowercase canonical UUID text |

The binary value remains authoritative. The printable value is always derived
with the Active Directory GUID byte order, equivalent to Python
`str(uuid.UUID(bytes_le=value))`. Applications use the printable projection
because Authentik 2026.5 and Nextcloud 34 cannot store an arbitrary binary LDAP
uniqueness value safely.

## OID registry

IANA assigned ANAS PEN `66678`. The project owns the enterprise root
`1.3.6.1.4.1.66678` and administers its child OIDs in the repository. The
directory-schema layout is:

```text
ANAS enterprise root
1.3.6.1.4.1.66678

Reserved class arc
1.3.6.1.4.1.66678.1.1

Reserved attribute arc
1.3.6.1.4.1.66678.1.2

anasIdentityAnchor attributeID
1.3.6.1.4.1.66678.1.2.1

anasIdentityAnchor schemaIDGUID
db3786ae-3261-4d44-a2a1-588bfe3e41c5
```

The canonical registry is
[ANAS OID registry](../governance/oid-registry.md). Allocate a concrete OID
there before code uses it. Never change the meaning of an allocated OID or
reuse a retired one.

An early deployment used a root generated from UUID
`43555bcd-c93f-4508-acbf-12be8d710729` with Microsoft's AD OID generator. Its
`anasIdentityAnchor` schema object has `schemaIDGUID`
`7108c5a7-2290-45e0-9eba-eef087be58e3` and this legacy `attributeID`:

```text
1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1
```

The GUID-derived root and all its descendants are retired; no new OID is ever
allocated below it. The old attribute OID and GUID identify only the retained
legacy schema object and must never be assigned to another object. The active
replacement uses the PEN-based `attributeID` and distinct `schemaIDGUID`
`db3786ae-3261-4d44-a2a1-588bfe3e41c5`; schema OIDs and schema GUIDs are
independently unique identifiers.

Internal child OIDs do not need another IANA registration. RFC 9371 recommends
internal sub-assignment and says not to report those child allocations to IANA.
See [IANA Private Enterprise Number and OID management](../governance/iana-pen-application.md).

## Schema installation

`install-identity-schema.sh` runs inside the primary Samba DC container after
provisioning and before the Samba daemon starts. It:

1. rejects an OID already owned by another schema name;
2. rejects the same name with a different OID or incompatible syntax;
3. creates the single-valued, indexed, 36-character Unicode attribute;
4. adds the attribute to the `mayContain` set of the AD `user` and `group`
   classes;
5. verifies the installed schema on every container start.

The operation is idempotent for a locally provisioned forest. A DC joining an
existing forest is not allowed to invent a local schema: the script requires
the attribute and both class updates to have replicated from that forest's
schema master, otherwise startup fails with an actionable error.

Schema updates are permanent forest data. Before a production upgrade, restore
a snapshot into a lab forest and run both the schema and anchor E2E tests. A
normal deployment rollback cannot remove a replicated schema element.

## Legacy OID migration

Active Directory schema additions are permanent and an installed object's
`attributeID` is not rewritten in place. An existing forest that contains the
GUID-derived OID therefore uses a **defunct replacement**, not an LDIF
modification of that identifier. The order is significant because an attribute
cannot be made defunct while an active class still references it, and a change
to `isDefunct` must be a separate LDAP Modify operation:

1. confirm that the current `anasIdentityAnchor` has exactly the registered
   legacy OID and schema GUID; abort on any unexpected schema state;
2. stop the anchor writer and downstream directory synchronization, take a
   restorable DC snapshot, and export every anchor-bearing object's value;
3. while the legacy attribute is still active, delete its values from all
   objects and verify that none remain;
4. remove the legacy attribute from the `user` and `group` optional-attribute
   sets, mark its schema object defunct in a dedicated modify, then rename both
   its `lDAPDisplayName` and RDN to unambiguous legacy names in two further,
   separate operations;
5. create a new schema object named `anasIdentityAnchor` with
   `1.3.6.1.4.1.66678.1.2.1`, schema GUID
   `db3786ae-3261-4d44-a2a1-588bfe3e41c5`, and the same syntax, cardinality,
   range, indexing, and class applicability;
6. add the replacement to the `user` and `group` optional-attribute sets and
   restore each exported value without transforming it;
7. replace the old schema-GUID write ACE with the replacement GUID, then verify
   counts, per-object equality, UUID syntax, uniqueness, and an actual
   `svc_anchor` write;
8. restart the writer and consumers only after the schema, values, and
   least-privilege write path pass verification.

The migration is allowed only for the explicitly detected legacy state. Its
read-only completed-state check validates the final and retired legacy schema
objects, both User and Group class links, and every textual anchor's uniqueness
and equality to the UUID derived from `mS-DS-ConsistencyGuid`. ACLs and a real
service-account write still require the post-start verification below. The
migration never deletes the old schema object or its registry entry. A failure
at an intermediate state leaves services stopped; restore the required
pre-migration snapshot before retrying rather than trying to improvise a
partial repair. Once the replacement is active, all new installations and
runtime checks must reject the legacy OID for the live `anasIdentityAnchor`
name.

Microsoft's [guidance for disabling schema extensions](https://learn.microsoft.com/en-us/windows/win32/ad/disabling-existing-classes-and-attributes)
documents the permanent-object and defunct-replacement model. The migration
script additionally treats Samba-specific name/GUID ambiguity as fail-closed.

### Migration command

The repository command
`modules/samba_dc/samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh`
is installed in the upgraded Samba DC image as
`/usr/local/bin/migrate-identity-anchor-oid.sh`. It supports only a forest with
exactly one DC. Run its read-only preflight first, as root in a maintenance
container that mounts the DC's existing `/var/lib/samba` while Samba and every
other writer of that volume are stopped:

```sh
/usr/local/bin/migrate-identity-anchor-oid.sh --check
```

After creating and verifying an ANAS snapshot, execute the migration with that
snapshot's real identifier:

```sh
/usr/local/bin/migrate-identity-anchor-oid.sh \
  --execute \
  --snapshot-id <verified-snapshot-id> \
  --backup-dir /mnt/anas-migration-evidence/<new-dir>
```

`--execute` refuses an absent or unsafe snapshot identifier; it does not create
or validate the snapshot itself. `--backup-dir` is also mandatory and has no
default. Mount protected persistent storage outside the Samba data volume at
`/mnt/anas-migration-evidence`, then name a new absolute child directory there.
The script rejects a path within `/var/lib/samba`, an existing path, a symlinked
parent, or a missing parent. This separation keeps the evidence available if
the Samba data-volume snapshot must be restored.

Immediately after `--execute`, while the data volume is still offline, a second
`--check` must verify the final and defunct legacy schema records, both class
links, and every textual anchor's equality to `mS-DS-ConsistencyGuid` and
uniqueness. Then start only the upgraded DC. Its normal structure reconciler
replaces the attribute write ACE with the new schema GUID. Confirm a real
least-privilege `svc_anchor` write before starting the anchor worker and other
directory consumers.

The end-to-end outage, snapshot, evidence, staged startup, Consumer validation,
and full-volume recovery procedure is the
[PEN 66678 identity-anchor migration runbook](../guide/migrate-identity-anchor-oid.md).

## Runtime reconciliation

Samba writes successful DSDB Add events to the `samba-dc-audit` volume. The
`samba_dc_anchor` sidecar consumes the file read-only, confirms the object over
LDAPS, and writes both attributes with the least-privilege `svc_anchor`
account. It never mounts or opens `sam.ldb`.

At startup and every `SAMBA_DC_ANCHOR_SCAN_INTERVAL` seconds, the worker also
scans for an object missing either representation. Its rules are:

- if the binary anchor is absent, copy the raw 16 bytes of `objectGUID`;
- if the binary anchor exists, preserve it even when it differs from the new
  forest's `objectGUID`;
- derive `anasIdentityAnchor` only from the binary anchor;
- never overwrite a conflicting printable value; mark the worker unhealthy and
  require operator repair instead;
- detect malformed and duplicate anchors during integrity scans.

The positive scope contains normal users below `OU=People` and non-critical
groups below `OU=Groups`. Computers, DCs, trusts, managed service accounts,
`krbtgt`, contacts and critical system objects remain excluded. Creation time
is deliberately not part of the filter because it would miss restored and
historical entries.

## Consumer contract

- Authentik uses `anasIdentityAnchor` as `object_uniqueness_field`.
- Nextcloud uses it for user/group LDAP UUIDs, its SAML UID claim, and an OIDC
  identity-verification claim. OIDC login itself maps `preferred_username` to
  the LDAP `sAMAccountName` internal username, as required by `user_oidc` when
  LDAP owns provisioning.
- MeshCentral uses it through the textual `ldapUserKey` option and as the
  application-specific OIDC account key claim.
- OIDC providers use Authentik's stable `user_uuid` as `sub`; usernames are
  login names, not identities.

There is no fallback to `objectGUID`, `objectSid`, `sAMAccountName`, UPN, mail
or `cn`. An object remains invisible to consumers until both anchor
representations pass validation.

## Cross-forest restore

Create replacement objects outside the configured worker bases, restore the
old 16-byte `mS-DS-ConsistencyGuid`, and either restore or let the worker derive
the matching `anasIdentityAnchor`. Only then move the objects into the business
OUs. Creating an account with the same name does not restore its identity.

Useful checks:

```sh
docker exec "${CONTAINER_PREFIX}samba_dc" test -f /run/anas-identity-schema.ready
docker inspect --format '{{json .State.Health}}' "${CONTAINER_PREFIX}samba_dc_anchor"
docker logs "${CONTAINER_PREFIX}samba_dc_anchor"
```

## Directory event journal

The worker follows Samba's dsdb audit log to find new objects, and republishes
the interesting records from that same pass as a normalized stream for other
modules to subscribe to. See
[directory-event-journal.md](directory-event-journal.md).
