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

Import and `config plan` run the same generic dependency, Capability, and
Contract resolver. Only an omitted/`auto` binding that cannot yet be selected
uniquely may remain deferred in a draft. An explicitly invalid, unknown,
disabled, or unsupported provider/interface fails immediately rather than at
apply time. Structural selectors such as a provider, interface, backend, or DNS
platform cannot come from `secrets:` or the lifecycle Secret Store: their
canonical identifier must be persisted in plan and the resolution lock, which
cannot also preserve plaintext secrecy. Move the selector to ordinary config
and keep only actual credentials in secret storage. Rejection diagnostics show
only `<redacted>`.

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
persistence. `env` and `secrets` keys use their uppercase runtime spelling;
Module names, global parameter names, and Module parameter names become
lowercase. Two source keys that collapse to one address are rejected rather
than resolved by YAML order. This includes collisions within `secrets` and
between `secrets`, `env`, and a structured Module parameter that resolve to the
same runtime key; diagnostics never echo sensitive candidate values. A
structured parameter declared as a bare export is moved to its canonical
`env.<KEY>` address—for example,
`modules.samba_fs.config.share_dir_name` becomes `env.SHARE_DIR_NAME`—and
supplying both spellings is a collision. The external source file is unchanged.

Top-level `secrets.<KEY>` is a sensitive spelling of a runtime input, not an
untyped validation escape hatch. When it maps to a declared parameter, the same
type, constraints, sensitivity, and change effect apply. Ordinary deployment
secrets remain in the mode-0600 managed `config.yml`; `credential_rotate`
inputs and managed local-administrator bootstrap passwords are extracted into
`.anas/secrets.yml`. Configuration, Secret Store, and integrity state are
staged and replaced together, so a failed import leaves the old state intact.

A normalized managed configuration can be reimported without supplying an
extracted secret again: only an existing non-empty Secret Store record of kind
`lifecycle_managed` may satisfy caller-input requirements in the private
validation view. Supplying the same value is idempotent; importing a different
replacement is rejected before writes in favor of the declared credential
rotation command or `anas admin local rotate`. Records of kind `generated` or
`local_admin` cannot satisfy an unrelated caller-input requirement. Missing or
currently invalid lifecycle-managed input makes import or plan fail without
revealing the value.

`config list` exposes lifecycle-managed presence as `<set>`/`<unset>` in human
output and as `set: true`/`false` with no `value` field in JSON. `config plan`
revalidates the private view against the current schema and reports only change
metadata. Ordinary values supplied through `secrets:` retain their owner's
declared effect and sensitivity, and no Secret Store plaintext is projected by
either command.

Every Secret Store kind is a source-sensitivity taint. If an ordinary config
value equals any stored plaintext, set/import/plan/lock/apply diagnostics and
list/plan projections still treat the alias as sensitive; only
`lifecycle_managed` is actually merged into the caller-input view. Set,
import/reimport, `config plan`, deployment lock/plan/materialize, and remote
lock use the same registry-aware schema, and a failure must not first change
configuration, integrity state, the Secret Store, or a lock.

`set` and `explain` reject a parameter no manifest declares, name the closest
declared parameter, and use the usage exit code. A raw `env.<KEY>` that maps to
a declared parameter receives the same type validation and normalization; only
a valid environment key unknown to every schema remains a permissive escape
hatch. If a legacy Module names a bare environment key only in legacy
`required`, it remains addressable solely as `env.<KEY>` and is not projected
as a nonfunctional `<module>.<parameter>`.

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
