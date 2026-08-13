# IANA Private Enterprise Number application draft

Use the official application form at
<https://www.iana.org/assignments/enterprise-numbers/assignment/apply/>. The
registration is free. IANA sends a confirmation email and normally assigns the
number within seven days after confirmation when no further information is
needed.

## Information the owner must supply

The repository must not guess or publish personal contact data. Before filing,
the project owner fills in:

```text
Assignee name / organization: <legal organization or responsible person>
Assignee postal address:      <required>
Assignee country:             <required>
Assignee phone:               <optional>

Contact name:                 <responsible maintainer>
Contact postal address:       <required>
Contact country:              <required>
Contact email:                <required and monitored>
Contact phone/fax:            <optional>
```

Suggested purpose text if IANA asks for clarification:

```text
ANAS requires a Private Enterprise Number to allocate globally unique object
identifiers for LDAP/Active Directory schema extensions and related identity,
management and telemetry protocol objects maintained by the project.
```

## Allocation plan after assignment

If IANA assigns PEN `<PEN>`, record the assignee and registry URL in this file
and reserve:

```text
1.3.6.1.4.1.<PEN>.1     LDAP/Active Directory schema
1.3.6.1.4.1.<PEN>.1.1   schema classes
1.3.6.1.4.1.<PEN>.1.2   schema attributes
1.3.6.1.4.1.<PEN>.2     management and telemetry
1.3.6.1.4.1.<PEN>.3     protocol identifiers
```

Each allocation must be added to a repository OID table before code uses it.
Never reuse a retired OID for a new meaning.

## Relationship to the current AD schema OID

`anasIdentityAnchor` currently uses Microsoft's GUID-derived schema OID root,
documented in [samba-identity-anchor.md](../architecture/samba-identity-anchor.md). That root is
already globally unique and valid for private AD forests. Obtaining a PEN does
not authorize changing an OID already installed in a released forest.

Because ANAS has not yet released this schema, the project owner may choose one
of two policies before the first production release:

1. keep the GUID-derived OID permanently and use the PEN only for future
   allocations; or
2. wait for the PEN, replace the constants and wipe every test forest before
   release.

After the first production schema installation, policy 1 becomes mandatory.
