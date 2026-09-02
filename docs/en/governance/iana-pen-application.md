# IANA Private Enterprise Number and OID management

> Status: **assigned**. IANA has approved PEN `66678`; the ANAS enterprise OID
> root is `1.3.6.1.4.1.66678`.

## IANA registration

| Item | Value |
| --- | --- |
| PEN (decimal) | `66678` |
| Enterprise OID root | `1.3.6.1.4.1.66678` |
| Registered assignee | Wang Hailong |
| Official registry | [IANA Private Enterprise Numbers: page containing 66678](https://www.iana.org/assignments/enterprise-numbers/?page=667) |

The repository records only the public facts needed to manage OIDs. It does
not copy the registration contact's email address, postal address, or phone
number. The assignee uses IANA's official modification process when the IANA
contact record changes; a repository commit does not modify that record.

## Internal allocation

ANAS administers the namespace below `1.3.6.1.4.1.66678`:

```text
1.3.6.1.4.1.66678.1     LDAP/Active Directory schema
1.3.6.1.4.1.66678.1.1   schema classes
1.3.6.1.4.1.66678.1.2   schema attributes
1.3.6.1.4.1.66678.2     management and telemetry
1.3.6.1.4.1.66678.3     protocol identifiers
```

The official `attributeID` for `anasIdentityAnchor` is
`1.3.6.1.4.1.66678.1.2.1`. Every concrete allocation and historical OID is
recorded in the [ANAS OID registry](oid-registry.md). An OID must be registered
before code uses it; an allocated or retired OID must never change meaning or
be reused.

## Whether another IANA registration is required

No separate request is needed for `1.3.6.1.4.1.66678.1.2.1` or any other
internal sub-assignment. [RFC 9371 Section 2.1](https://www.rfc-editor.org/rfc/rfc9371.html#section-2.1)
says that obtaining one PEN and making internal sub-assignments is normally
more appropriate, and that sub-assignments should not be reported to IANA.

Use the [IANA PEN modification form](https://www.iana.org/assignments/enterprise-numbers/assignment/modify/)
only when the registered assignee, public contact, or other PEN record data
must change. Adding, reserving, or retiring a child OID only changes the
repository registry.

## AD schema decision and the legacy deployment

Early ANAS deployments used this GUID-derived OID for `anasIdentityAnchor`:

```text
1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1
```

That OID is now **legacy / retired**. It exists only to identify and migrate
the old schema object and must never be reused. An AD schema object's
`attributeID` is not rewritten in place. Existing deployments use a controlled
**defunct replacement**: retain and deactivate the old object, move it to a
legacy name, create `anasIdentityAnchor` under the official OID, restore the
values exported beforehand, and verify every consumer. The complete constraints
are documented in the
[Samba AD identity-anchor architecture](/architecture/samba-identity-anchor#legacy-oid-migration).

The repository script is
`modules/samba_dc/samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh`
and is installed in the Samba DC image under `/usr/local/bin/`. It supports a
single-DC forest only. Run it as root after stopping Samba and every other
writer of the DC data volume. Start with the read-only check; only after
creating and verifying an ANAS snapshot should its real identifier be supplied
to execute mode:

```sh
/usr/local/bin/migrate-identity-anchor-oid.sh --check
/usr/local/bin/migrate-identity-anchor-oid.sh \
  --execute \
  --snapshot-id <verified-snapshot-id> \
  --backup-dir /mnt/anas-migration-evidence/<new-dir>
```

`--backup-dir` is mandatory. It must name a nonexistent absolute path on
protected persistent storage mounted outside the Samba data volume. The command
has no `/var/lib/samba` default and refuses an existing directory: restoring a
whole-volume snapshot would otherwise erase the migration evidence too.

After completion, run `--check` again while the data volume remains offline.
It verifies the final and retired legacy schema objects, both User and Group
class links, and that every textual anchor is unique and matches its binary
`mS-DS-ConsistencyGuid`. Then start the upgraded DC by itself so the structure
reconciler can replace the write ACE for the new schema GUID. Verify a real
`svc_anchor` write before starting the anchor worker and other consumers.
See the [PEN 66678 identity-anchor migration runbook](../guide/migrate-identity-anchor-oid.md)
for the complete outage, snapshot, external-evidence, staged-startup, and
full-volume recovery procedure.
