# ANAS OID registry

This file is the project registry for ANAS OID allocations. Once allocated, an
OID's number, name, and meaning are permanent records. Retirement stops new use;
it never makes the number available for reassignment.

## Namespaces

| OID | Name | Purpose | Status |
| --- | --- | --- | --- |
| `1.3.6.1.4.1.66678` | ANAS PEN | Enterprise root assigned by IANA | active |
| `1.3.6.1.4.1.66678.1` | `directorySchema` | LDAP / Active Directory schema branch | namespace |
| `1.3.6.1.4.1.66678.1.1` | `schemaClasses` | Schema-class allocation branch | namespace |
| `1.3.6.1.4.1.66678.1.2` | `schemaAttributes` | Schema-attribute allocation branch | namespace |
| `1.3.6.1.4.1.66678.1.2.1` | `anasIdentityAnchor` | Text identity-anchor AD schema attribute | active |
| `1.3.6.1.4.1.66678.2` | `managementAndTelemetry` | Management and telemetry allocation branch | reserved namespace |
| `1.3.6.1.4.1.66678.3` | `protocolIdentifiers` | Protocol identifier allocation branch | reserved namespace |

## Historical allocations

| OID | Name | Purpose | Status |
| --- | --- | --- | --- |
| `1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401` | `legacyDirectorySchemaRoot` | Pre-PEN AD schema root | retired namespace; never allocate descendants |
| `1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1` | `anasIdentityAnchor` legacy schema object | Text identity-anchor attribute deployed before PEN approval | retired; never reuse |

The historical OIDs are outside the ANAS PEN, but remain in this table to
prevent omission, mistaken ownership, or reuse.
The legacy root and all its descendants are retired; no new OID is allocated
below that root.

## Allocation rules

1. Before implementing or releasing a new object, allocate its concrete OID in
   this table and record a stable name, one purpose, and its status.
2. Allocate sequentially under the correct namespace. Never derive OIDs from a
   code version, host, or environment.
3. A published allocation must not be renumbered or repurposed. Give a
   replacement object a new OID and retain the old allocation as `retired`.
4. Do not report internal sub-assignments to IANA. Use IANA's modification
   process only when the PEN registration data changes.

See [IANA Private Enterprise Number and OID management](iana-pen-application.md)
for the registration facts and governing rule.
