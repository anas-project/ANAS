# Samba AD identity anchor

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

The schema OID root was generated once from UUID
`43555bcd-c93f-4508-acbf-12be8d710729` with Microsoft's documented AD OID
generator. It is globally unique and must never be regenerated.

```text
ANAS AD schema root
1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401

Reserved class arc
<root>.1

Reserved attribute arc
<root>.2

anasIdentityAnchor attributeID
<root>.2.1

anasIdentityAnchor schemaIDGUID
7108c5a7-2290-45e0-9eba-eef087be58e3
```

The deployed attribute OID is therefore:

```text
1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1
```

IANA does not let an applicant choose an unassigned Private Enterprise Number.
It assigns the number after an application is confirmed. The application draft
is in [iana-pen-application.md](../governance/iana-pen-application.md). A future PEN is useful
for new protocol and schema allocations, but it must not be used to renumber an
attribute already installed in a released AD forest.

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
