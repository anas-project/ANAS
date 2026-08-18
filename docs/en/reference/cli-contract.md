# CLI contract

The normative CLI JSON contract currently lives in the Chinese reference section:

- [Common rules and index](/reference/contracts/)
- [Deployment and configuration commands](/reference/contracts/commands)
- [Snapshot commands](/reference/contracts/snapshot)
- [Backup commands](/reference/contracts/backup)

Every JSON response includes `api_version`. Structured results use stdout; progress, warnings, and logs use stderr. Exit codes distinguish usage errors, confirmation requirements, precondition failures, and failures after work has started. Consumers must rely on documented enum values rather than parsing human-readable messages.

These documents are the compatibility contract for external non-interactive
CLI consumers. ANAS's own Web API does not launch `anas --json` as a subprocess;
the CLI and HTTP adapters share the typed Go application layer. The CLI contract
therefore remains both an external compatibility boundary and a black-box
regression baseline for keeping the two adapters aligned.

## Configuration metadata

`anas config list --json` is the external machine-readable configuration
inventory. Each parameter reports its accepted path, environment key, explicit
type, input and resolution requirements, default source, single-field
constraints, current-value state, sensitivity, editability, and change effect.
The normative field definitions are in the
[deployment and configuration command contract](/reference/contracts/commands#config).

`type` is `string`, `bool`, `int`, `enum`, or `unknown`; enum entries also carry
non-empty `allowed_values`. `unknown` exists for legacy Modules and incomplete
development declarations, but the built-in Module release gate rejects it, so
the released built-in inventory must contain zero unknown types. Consumers must
not interpret `unknown` as an explicitly declared free-form string.

Every item reports `required`, `input_required`, `must_resolve`, `has_default`,
and `default_source`. `required` is a compatibility alias and must equal
`input_required`. The latter is true only when every applicable case requires
the operator to enter a non-empty value and no default or other unconditional
source exists. `must_resolve` instead says that the final value must be
non-empty after canonicalization and all sources have been applied; it can be
true while `input_required` is false. `has_default` distinguishes an absent
static default from an explicitly empty-string default. `default_source` is
`none`, `static`, `host`, `runtime`, `generated`, or `inherited`. An
input-required item must have `has_default: false` and `default_source: "none"`.
The compatibility `default` field remains a string and may display a host value
that can be calculated on the current machine. Use `has_default`, not whether
that string is empty, to determine whether a static default was declared.

`default_source: "none"` means only that there is no **unconditional** source;
it does not imply `input_required: true`. For example,
`ddns_go.dns_provider` and `ddns_updater.dns_provider` may be injected
conditionally from deployment `dynamic_dns.dns_provider`. They therefore
report `input_required: false`, `must_resolve: true`, and
`default_source: "none"`; validation requires Module-local input only when the
resolver cannot supply the value.

When declared, `constraints` is an object containing only non-empty members.
Integers use `minimum` and `maximum`; strings use `min_length`, `max_length`,
`pattern`, and `format`. Current format identifiers are `iana_timezone`,
`language_tag`, `locale`, and `ipv4`. Type, enum membership, and these rules are
single-field validation. Conditional requirements, relationships between two
or more fields, and rules that depend on workspace or runtime state remain
resolver, application-service, plan, or Hook validation; they do not make one
whole field unconditionally input-required.

This projection is **not a JSON Schema document**. JSON Schema `required` is an
array of object property names, whereas this field describes explicit input for
one parameter. JSON Schema `default` is an annotation, whereas ANAS actually
applies defaults during resolution. `constraints` supports only the stable
fields above, not arbitrary JSON Schema keywords.

Typed values are canonicalized before persistence and runtime delivery: bools
become lowercase `true`/`false`, enums use the spelling declared by the
manifest, integers lose surrounding whitespace, and strings remain unchanged.
A raw `env.<KEY>` that resolves to a declared parameter follows the same rule;
only an undeclared raw key remains a permissive compatibility escape hatch.

`config import` also canonicalizes YAML addresses before validation and managed
persistence. Environment keys use their uppercase runtime spelling; Module
names, global parameter names, and Module parameter names become lowercase.
Two source keys that collapse to one address are rejected rather than resolved
by YAML order. A structured parameter declared as a bare export is moved to its
canonical `env.<KEY>` address—for example,
`modules.samba_fs.config.share_dir_name` becomes `env.SHARE_DIR_NAME`—and
supplying both spellings is a collision. The external source file is unchanged.

The M3 `anasd` configuration endpoints will consume this same typed application
schema. They do not parse CLI JSON and do not maintain per-Module HTTP adapters;
the CLI, HTTP API, and Web form model must stay projections of one schema. The
current M0 daemon does not yet expose configuration HTTP endpoints.

## Local administrator commands

```text
anas admin local list [-w WORKSPACE] [--json]
anas admin local credential MODULE [ACCOUNT] [-w WORKSPACE] [--password-only | --json]
anas admin local rotate MODULE [ACCOUNT] [-w WORKSPACE] [--prompt] [--json]
```

`ACCOUNT` is the Manifest `management.local_accounts[].id`, never the physical
username. When omitted, selection prefers the `primary` ID, then the module's
only account; any other multi-account case is ambiguous. `list` is safe
inventory. `credential` is the explicit sensitive read. `rotate` generates a
random password by default; `--prompt` reads it without echo from a real TTY
and confirms it. Managed passwords are never accepted in argv, environment
variables, or YAML.

Rotation sends a candidate Secret to the active artifact's declared module
handler, updates and verifies application state, and commits the Secret only
after success. A failed verification restores the old application password and
leaves the old Secret authoritative. Modules without a real
`lifecycle.rotate` handler report the capability as unsupported.
