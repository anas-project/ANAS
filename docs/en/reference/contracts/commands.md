# Deployment and configuration command JSON contract

> Status: **implemented** (`init` / `plan` / `lock` / `render` / `build` /
> `apply` / `start` / `restart` / `stop` / `rollback` / `status` /
> `deployments` / `config` / `admin` / `credential` / `module`).
> The common conventions (stream separation, exit codes, enumerations, time and
> size, paths, versioning, the minimal envelope) are in the
> [common conventions](index.md) and are not repeated here.
> For `snapshot` see [snapshot.md](snapshot.md); for `backup` see
> [backup.md](backup.md).

This document covers the commands that **predate the contract**. They previously
all "returned an error and exited 1", which tells a caller that needs to branch
on a code exactly nothing.

## Contents

- [init](#init)
- [plan](#plan) / [lock](#lock)
- [render / build](#render--build)
- [apply](#apply)
- [start / restart / stop](#start--restart--stop)
- [rollback](#rollback)
- [status](#status)
- [deployments](#deployments)
- [config](#config)
- [credential](#credential)
- [admin local](#admin-local)
- [module](#module)
- [help](#help)

---

## init

```
anas init [PATH] [-c CONFIG] [--module-root DIR] [--shell-init write|remove] [-y] [--json]
```

Creates a workspace. It is the only command that creates one — every other
command refuses to conjure a workspace out of nothing, because one mistyped `cd`
should not quietly grow a second parallel deployment.

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/data/ws",
  "config_path": "/data/ws/config.yml",
  "config_source": "/data/anas.yml",
  "secrets_imported": 0,
  "data_path": "/data/ws/data",
  "snapshots_path": "/data/ws/snapshots",
  "state_path": "/data/ws/.anas",
  "btrfs": true,
  "data_is_subvolume": true,
  "snapshots_usable": true,
  "shell_init": { "action": "none", "profile": null, "changed": false }
}
```

`-c` / `--config` imports external YAML while creating the workspace. ANAS
validates and normalizes the source file first, and does not create the workspace
if validation fails; the source file itself is always left unchanged.
`module_source: cn` normalizes to `official-cn`, and unless `global.chinese_speedup`
is declared explicitly, the managed `config.yml` gains `true` and the rendered
environment gains `CHINESE_SPEEDUP=true` accordingly. An explicit `false` is left
alone. `--module-root` is used only to locate the module definitions the import
validation needs.

`data_is_subvolume` determines whether this workspace **has snapshot and data
rollback capability**, so it is part of the result rather than an implementation
detail. Off btrfs it is `false`, and from then on the whole `snapshot` family is
unavailable, `apply` takes no automatic pre-snapshot, and `backup` is left with
whole-directory copying as its only mode.

`shell_init.action` takes `none` / `write` / `remove`; `changed` distinguishes
"it was already like that" from "it was rewritten".

`--shell-init` accepts only `write` and `remove`. Previously any non-empty value
was treated as `write`, which made `--shell-init=yes` silently taking effect
indistinguishable from silently not taking effect.

| code | Exit code | When |
| --- | --- | --- |
| `workspace_exists` | 4 | The target is already a workspace |
| `config_import_failed` | 4 | The external configuration cannot be read, normalized, or validated against module parameters |
| `module_root_missing` / `module_root_invalid` | 4 | The module definitions cannot be found or loaded while a configuration is given |
| `data_is_symlink` | 4 | `data/` is a symlink; tar and rsync skip it and the backup would quietly be empty |
| `data_is_mount_point` | 4 | `data/` is a mount point; data restore renames this directory, which cannot cross a mount point |
| `shell_unrecognised` | 4 | `$SHELL` is unrecognized and no profile can be written |
| `confirmation_required` | 3 | Non-btrfs needs confirmation, or the profile already has an anas block pointing elsewhere |
| `subvolume_create_failed` / `mkdir_failed` / `write_failed` | 1 | Failed part way through creation |

## plan

```
anas plan [-w WORKSPACE] [-c config.yml] [--module-root DIR] [--json]
```

Computes without writing: the core creates and modifies no workspace or
deployment state. `-w` selects a managed workspace, and not merely for
command-line symmetry; plan validates configuration integrity and reads the
existing secret store in the private validation view to satisfy lifecycle inputs
and sensitivity taint. Secret plaintext enters neither the output nor a module
`validate` request.

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "config": "/data/ws/config.yml",
  "module_root": "/srv/anas/modules",
  "modules": ["samba_dc", "postgres", "authentik", "nextcloud"],
  "iam": {
    "provider": "authentik",
    "consumers": [{ "module": "nextcloud", "interface": "oidc" }]
  },
  "capability_bindings": { "nextcloud": { "relational_database": "postgres" } },
  "module_plans": {
    "samba_dc": {
      "requested_mode": "auto",
      "resolved_mode": "ad_zone",
      "zone": "example.net"
    }
  }
}
```

`modules` is the startup order, not a set; the order is meaningful.

`iam.provider` is `null` rather than absent when nobody consumes the capability —
a caller always gets the key and never has to tell "there is no IAM" apart from
"this version does not report IAM".

`module_plans` likewise always exists and is an object; it is `{}` when no module
returned plan metadata. Its first-level keys are module names, and the second
level is the read-only, non-sensitive `string -> string` metadata that module's
`validate` hook returned and the core accepted. It modifies no configuration and
does not indicate that a change was executed. Human output uses
`module plan: <module> key=value ...` lines sorted by module and key.

A non-zero exit from a module `validate` hook, an invalid or unknown JSON field,
a mutation response, or non-conforming plan metadata all mean the configuration
fails validation against the current desired state: `anas plan` returns
`config_invalid` (exit 4). `anas config plan` below uses the same boundary. The
CLI currently has **no** `validation_failed` error code, and callers must not
branch on codes that are not implemented.

| code | Exit code | When |
| --- | --- | --- |
| `config_missing` | 4 | The configuration file does not exist |
| `config_invalid` | 4 | The configuration cannot be parsed, violates the general schema, or a module `validate` hook rejected the desired state or returned an invalid response |
| `module_root_missing` / `module_root_invalid` | 4 | The module directory cannot be found, or cannot be read |
| `resolution_failed` | 4 | A dependency cycle, an unknown module, or a disabled module that is depended on |
| `version_conflict` | 4 | Version constraints contradict each other |

## lock

```
anas lock [-w WORKSPACE] [-c config.yml] [--json]
```

Pins the resolution result and writes `<config>.lock.yml`.

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "config": "/data/ws/config.yml",
  "lock_path": "/data/ws/config.lock.yml",
  "modules": [{ "name": "postgres", "version": "16.4.0", "revision": 1, "app_version": "16.4", "digest": "sha256:…" }],
  "iam": { "provider": null, "consumers": [] },
  "capability_bindings": {},
  "snapshot": { "backend": "btrfs", "keep_auto": 5 }
}
```

`modules` is the contract's view of the lock, not the lock file's on-disk format;
the on-disk format may change without this changing with it.

The error codes are the same as `plan`, plus `lock_invalid` (4, the lock file
cannot be read) and `write_failed` (1).

## render / build

```
anas render [-w WORKSPACE] [-c config.yml] [--update-lock] [--json]
anas build  [MODULE...] [-w WORKSPACE] [-c config.yml] [--update-lock] [--json]
```

Produces an immutable deployment artifact and seals it, but **does not activate
it**. `build` does one step more than `render`: it builds images.

`build` accepts module names and builds images for those modules only; rendering
is always for the whole deployment, because half a render is not a deployment.
`render` does not accept module names.

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "config": "/data/ws/config.yml",
  "deployment_id": "20260731T101500Z-a1b2c3d4",
  "deployment_path": "/data/ws/.anas/deployments/20260731T101500Z-a1b2c3d4",
  "built": false
}
```

Progress `phase`: `calculate` → `render` → `build-images` (`build` only) →
`seal`, with `unit` of `modules` (`deployments` for `seal`).

| code | Exit code | When |
| --- | --- | --- |
| `lock_missing` | 4 | There is no config lock and `--update-lock` was not given |
| `lock_stale` | 4 | The lock does not match the modules in the source; an explicit `anas lock` is needed |
| `secrets_unreadable` | 4 | The secret store cannot be read |
| `compose_missing` | 4 | A build was requested but there is no docker compose |
| `calculate_failed` / `render_failed` / `build_failed` / `seal_failed` | 1 | Failed after work had started |

## apply

```
anas apply [-w WORKSPACE] [-c config.yml [--build] | --deployment ID]
           [--update-lock] [--allow-risky] [--snapshot | --no-snapshot] [-y] [--json]
```

Renders (or takes a ready artifact) and activates it.

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws",
  "deployment_id": "20260731T101500Z-a1b2c3d4",
  "previous_deployment": "20260730T090000Z-9f8e7d6c",
  "activated_at": "2026-07-31T10:15:07Z",
  "deployment_path": "/data/ws/.anas/deployments/20260731T101500Z-a1b2c3d4"
}
```

`previous_deployment` is `null` when this is the first deployment.

**Guards.** Crossing an irreversible change (`credential_rotate`,
`data_migrate`, `immutable`) refuses to execute and exits **4**, with
`error.detail.blocked` listing the specific items:

```json
{
  "api_version": "anas.dev/cli/v1", "ok": false,
  "error": {
    "code": "guarded_changes",
    "message": "apply crosses guarded state changes:\n  …",
    "detail": { "blocked": ["samba_dc.admin_password (credential_rotate; rotate-samba-admin-password)"] }
  }
}
```

This is 4 rather than 1: the machine is in a state that cannot simply be carried
forward, and the caller either runs a migration or comes back with
`--allow-risky`.

**Automatic snapshots.** The trigger conditions are in
[snapshot.md](snapshot.md). Giving up a snapshot requires confirmation, so
`--no-snapshot` must be accompanied by `-y` on a non-tty, or the exit is **3**.
When a snapshot cannot be taken, a warning is recorded to stderr with `code`
either `no_snapshot_backend` or `data_not_subvolume`, and confirmation is still
required afterwards.

| code | Exit code | When |
| --- | --- | --- |
| `deployment_not_ready` | 4 | The artifact named by `--deployment` is not in the ready state |
| `guarded_changes` | 4 | See above |
| `credential_store_mismatch` | 4 | The target deployment's credential generation/authority disagrees with the store; `--allow-risky` cannot bypass it |
| `confirmation_required` | 3 | `--no-snapshot`, or continuing without a possible snapshot, with no way to confirm |
| `start_failed` | 1 | Startup failed; a best effort was made to bring the old deployment back up |

## start / restart / stop

```
anas start|restart|stop [MODULE...] [-w WORKSPACE] [--json]
```

Without module names this is the whole deployment. With names, the command must
expand the targets into a dependency-safe chain rather than acting only on the
modules named:

- `start MODULE...` expands forward through every direct and indirect dependency
  of the targets, then starts them in dependency order. That way the providers,
  databases, and so on that a target needs are already running before it starts.
- `stop MODULE...` expands backward through every module that directly or
  indirectly depends on the targets, then stops them in reverse dependency order.
  That way no application is left running and already broken after its
  dependencies stopped.
- `restart MODULE...` uses the same dependent chain as `stop`, stopping
  everything in reverse dependency order first and then starting everything in
  dependency order.

Multiple targets are expanded separately, then unioned and deduplicated. The
final order comes from the deployment's frozen dependency order, not the word
order on the command line. `dependencies.after` constrains order only when both
modules are already selected into the chain, and never widens it. A name not in
this deployment is a usage error, and the message lists which modules this
deployment actually has. The CLI offers no option to bypass the chain and act on
a single dependency node.

For example, with a dependency relation of
`postgres -> nextcloud -> collabora`, `anas restart postgres` stops `collabora`,
`nextcloud`, and `postgres` in that order and then starts `postgres`,
`nextcloud`, and `collabora` in that order. `anas restart nextcloud` does not
restart a PostgreSQL that is still running fine; it restarts only Nextcloud and
its dependent Collabora.

Stopping a named chain **does not tear down the macvlan bridge**: a full stop
does (nobody is using it any more), but stopping one chain does not mean modules
outside the chain are not using it.

**This is a command with no natural result**, so it follows the README's
"minimal envelope":

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "action": "stop",
  "deployment_id": "20260731T101500Z-a1b2c3d4",
  "modules": ["postgres", "authentik", "traefik"]
}
```

`modules` is the modules the expanded chain actually acted on, listed in
dependency order; it is not a count of "how many were stopped or started".
Callers **must not** expect result fields beyond this. Without `--json`, stdout
is empty.

Progress `phase`: `stop-containers` for stopping and `activate-modules` for
starting, with `unit` of `modules`. One `activate-modules` covers the current
module's container startup, owned-credential probe/reconcile/verify, and the
after-start/local-admin ready barrier; downstream modules are only processed once
that barrier succeeds, rather than splitting all container startup and all hooks
into two global phases.

The three lifecycle commands take the exclusive runtime lock. If an unfinished
credential transaction exists, they first restore previous or complete the
candidate promotion automatically according to the store's
`rotation_id/generation`, and only then carry out the user's request.

| code | Exit code | When |
| --- | --- | --- |
| `no_active_deployment` | 4 | Nothing has been applied yet |
| `compose_missing` | 4 | There is no docker compose |
| `deployment_unreadable` | 4 | The active deployment's artifact is broken or absent |
| `credential_store_mismatch` | 4 | The active deployment's credential generation/authority disagrees with the store; restore a matching snapshot first |
| `lock_failed` | 1 | The runtime lock could not be acquired, or automatic credential transaction recovery failed |
| `start_failed` / `stop_failed` | 1 | Failed after work had started |

## rollback

```
anas rollback [DEPLOYMENT_ID] -w WORKSPACE [--allow-risky] [--json]
```

Switches artifacts and **never touches data**. Rolling data back has exactly one
route: `anas snapshot restore`.

It accepts only `-w`, and infers nothing from `ANAS_WORKSPACE` or the current
directory — an environment variable written into a shell profile is the thing
most likely to be stale and pointing somewhere else.

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws",
  "deployment_id": "20260730T090000Z-9f8e7d6c",
  "previous_deployment": "20260731T101500Z-a1b2c3d4",
  "activated_at": "2026-07-31T11:02:31Z",
  "data_touched": false
}
```

`data_touched` is always `false`, and is **deliberately kept**. It is this
command's boundary against `snapshot restore` written into the document; omitting
it would leave callers inferring from the command name whether data was touched.

| code | Exit code | When |
| --- | --- | --- |
| `no_previous_deployment` | 4 | There is nothing to roll back to |
| `already_active` | 4 | The named deployment is already active |
| `rollback_data_breaking` | 4 | The target module declared that this downgrade rewrites on-disk data; `--allow-risky` **cannot** bypass it |
| `rollback_guarded_changes` | 4 | A guarded change would be crossed; `--allow-risky` can bypass it |
| `credential_store_mismatch` | 4 | The target deployment's credential generation/authority disagrees with the store; `--allow-risky` cannot bypass it and a matching snapshot must be restored |

`rollback_data_breaking` and `rollback_guarded_changes` are two codes on purpose:
the latter describes something the runtime does not know and the operator might,
while the former describes something the runtime **does** know.

## status

```
anas status [-w WORKSPACE] [--json]
```

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws",
  "active_deployment": "20260731T101500Z-a1b2c3d4",
  "activated_at": "2026-07-31T10:15:07Z",
  "verified_at": "2026-07-31T10:15:07Z",
  "previous_deployments": ["20260730T090000Z-9f8e7d6c"]
}
```

**No active deployment is a successful answer and exits 0**, with
`active_deployment` set to `null`. Reporting it as a failure would leave callers
unable to tell a brand-new workspace from a workspace whose state cannot be read.

## deployments

```
anas deployments list [-w WORKSPACE] [--json]
anas deployments inspect ID [-w WORKSPACE] [--json]
```

`list`:

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws",
  "deployments": [{
    "id": "20260731T101500Z-a1b2c3d4", "status": "active",
    "created_at": "2026-07-31T10:15:00Z", "activated_at": "2026-07-31T10:15:07Z",
    "deactivated_at": null, "verified_at": "2026-07-31T10:15:07Z",
    "predecessor": "20260730T090000Z-9f8e7d6c", "failure": null
  }]
}
```

`status` takes `ready` / `active` / `previous` / `failed`.

`inspect` outputs `deployment` (the artifact manifest) and `state`. **The
manifest is emitted as JSON, not the YAML that is on disk.** It previously
printed `deployment.yml` verbatim to stdout, identically with and without
`--json`, and YAML is not a JSON document, so no `JSON.parse` could handle it.
The manifest types therefore gained json tags, with key names matching the
on-disk snake_case — one manifest in two spellings is exactly the thing callers
hard-code their way around.

| code | Exit code | When |
| --- | --- | --- |
| `deployment_missing` | 4 | No such id |
| `state_unreadable` | 4 | The state directory cannot be read |

## config

```
anas config list    [global|<module>]     [-w WORKSPACE] [-c config.yml] [--json]
anas config import  SOURCE [-w WORKSPACE] [--json]
anas config migrate        [-w WORKSPACE] [--json]
anas config set     <module.parameter> <value> [-w WORKSPACE] [-c config.yml] [--json]
anas config explain <module.parameter> [--json]
anas config plan    [-w WORKSPACE] [-c config.yml] [--json]
anas config secret  list | get <KEY>   [-w WORKSPACE] [--json]
```

`config list` reports each parameter's `set` path, environment variable, type,
input and resolution requirements, default source, single-field constraints,
current-value state, and change effect. Human output for a sensitive parameter
reports only `<set>`/`<unset>`; JSON returns `set: true`/`false` and omits
`value`, and never prints plaintext. Secret store records of kind
`lifecycle_managed` report presence the same way. Reading a credential still goes
through `config secret get`. Parameter declarations belong to modules and need no
workspace; when called inside a workspace, current values are filled in as well.

`parameters[].type` takes `string`, `bool`, `int`, `enum`, or `unknown`. An
`enum` entry also carries a non-empty `allowed_values`; other types omit the
field. `unknown` is a compatibility value for reading legacy modules and
incomplete in-development declarations. The built-in module release gate forbids
it from shipping, and its count in the current built-in inventory must be 0.
Callers should still handle `unknown`, and must not treat it as an explicitly
declared free-form `string`.

Every item returns the following resolution-semantics fields:

| Field | Contract |
| --- | --- |
| `required` | A compatibility alias for `input_required`; the two must always be equal |
| `input_required` | Whether the operator must supply a non-empty value explicitly in every applicable case for this parameter; must be `false` when a default or another unconditional source exists |
| `must_resolve` | Whether the final value must be non-empty after canonicalization and all sources have been applied; it may be `true` while `input_required` is `false` |
| `has_default` | Whether a static default was declared; used to tell "no default" apart from "the default is explicitly the empty string" |
| `default_source` | The unconditional source used when input is omitted: `none`, `static`, `host`, `runtime`, `generated`, or `inherited` |

A parameter with `input_required: true` must also satisfy `has_default: false`
and `default_source: "none"`. `required: false` does not mean any value is valid,
nor that a missing value is allowed after resolution. The compatibility `default`
field is still a string and may display a host value computable on the current
machine; to decide whether a static default exists, read `has_default` rather
than guessing from whether `default` is empty.

`default_source: "none"` means only that there is no **unconditional** source; it
does not imply `input_required: true`. For example, `ddns_go.dns_provider` and
`ddns_updater.dns_provider` may be injected conditionally from the deployment's
`dynamic_dns.dns_provider`, so both are `input_required: false`,
`must_resolve: true`, and `default_source: "none"`; validation requires a
module-side value only when the resolver did not inject one.

When single-field restrictions are declared, the parameter also returns a
`constraints` object. The object contains only non-empty members: integers use
`minimum` and `maximum`, and strings use `min_length`, `max_length`, `pattern`,
and `format`; the current `format` values are `iana_timezone`, `language_tag`,
`locale`, and `ipv4`. `type`, enum membership, and these constraints can all be
validated against a single candidate value. Conditional requirements,
relationships between two or more fields, and rules that depend on workspace or
runtime state remain resolver, application-service, plan, or hook validation, and
none of them may broadly mark one field as `input_required`.

Import and `config plan` run the same general dependency/Capability/Contract
resolver. Only a binding the caller has not yet chosen, and whose `auto` cannot
yet resolve uniquely, may be deferred as a draft; an explicitly invalid, unknown,
disabled, or unsupported provider/interface must fail immediately rather than at
apply time. Structural selectors such as a provider, interface, backend, or DNS
platform cannot come from `secrets:` or the lifecycle secret store: their
canonical identifier must enter the plan and the resolution lock, which cannot
simultaneously promise plaintext secrecy. Callers must move the selector into
ordinary configuration and keep only actual credentials in the secret channel.
Rejection diagnostics show only `<redacted>`.

These fields are the CLI's projection of the ANAS configuration schema and are
**not a JSON Schema document**: JSON Schema's `required` is an array of object
property names, whereas this is the input requirement of the current parameter;
JSON Schema's `default` is only an annotation, whereas ANAS actually applies
defaults during resolution; and `constraints` supports only the stable fields
listed above, accepting no arbitrary JSON Schema keywords. A new parameter must
not have its type inferred from the YAML scalar type of its default, and must be
declared explicitly in the global schema or the module manifest.

Typed values are canonicalized before persistence and before delivery to the
runtime: `bool` becomes lowercase `true`/`false`, `enum` is written back in the
manifest's canonical spelling, `int` loses surrounding whitespace, and `string`
keeps its text. This keeps a case difference in an old configuration from
suddenly invalidating a selector, and prevents "validation passed but the hook
interprets the same value the opposite way".

`config import` is the entry point for importing external YAML into an existing
workspace; the same import can be done at first creation with
`anas init WORKSPACE --config SOURCE`. Neither modifies the source file. The
import first writes `env` and `secrets` keys in their uppercase runtime spelling,
and module names, global parameter names, and module parameter names in
lowercase; any address that duplicates after normalization fails the import
rather than being decided by YAML order. The collision check runs within
`secrets`, and between `secrets`, `env`, and a structured module parameter that
map to the same runtime key; errors never echo sensitive values. A structured
module parameter declared as a bare export migrates to the canonical
`env.<KEY>` — for example `modules.samba_fs.config.share_dir_name` persists as
`env.SHARE_DIR_NAME` — and supplying both spellings is rejected as a collision.
When `secrets.<KEY>` maps to a declared parameter, the same type, constraints,
sensitivity, and change effect still apply; it is not an untyped validation
escape hatch. After the normalized configuration is validated,
`credential_rotate` inputs and local administrator bootstrap passwords are moved
into `.anas/secrets.yml`, while ordinary deployment secrets such as DNS/API
tokens remain in the mode-0600 workspace `config.yml`. Configuration, the secret
store, and the integrity digest are all staged first and replaced together; a
failure at any step leaves the original state intact. `migrate` exists only to
bring a current workspace configuration that has no CLI integrity digest yet into
this model, and offers no legacy secret store compatibility. plan/lock/render/apply
reject an external `-c` and hand edits whose digest does not match.

An extracted `lifecycle_managed` input does not have to be supplied again in
plaintext when the managed `config.yml` is reimported: an existing non-empty
secret store value satisfies `input_required` in the private validation view only
and is revalidated against the current schema. Repeating the same value
explicitly is idempotent; reimporting with a different value is rejected before
any file is written, and directs the caller to the declared credential rotation
command or `anas admin local rotate`. Records of kind `generated` or
`local_admin` cannot satisfy an unrelated caller-input requirement; a missing
`lifecycle_managed` value, or one that no longer meets the current constraints,
likewise fails import/plan without leaking the value. `config plan` reports only
metadata such as changed paths, effect, and sensitive; ordinary deployment
secrets that came through `secrets:` retain the risk policy their owner declared,
and no secret store plaintext enters plan output.

Every secret store kind is a source-sensitivity taint: if an ordinary
configuration value equals any store plaintext, set/import/plan/lock/apply errors
and list/plan projections still treat it as sensitive; only `lifecycle_managed`
is actually merged into the caller-input view. set, import/reimport,
`config plan`, deployment lock/plan/materialize, and remote lock use one
registry-aware schema, and a failure must not first change the configuration, the
integrity digest, the secret store, or a lock.

A new workspace with an empty lock, and a workspace with an existing lock that
stages a newly added module during set/import, both run no hooks for modules that
are not yet pinned; already-pinned modules are still validated in effective
dependency order. Before the first lock, `config plan` / `anas plan` also perform
only a static schema and topology projection and produce no module validation
metadata. The subsequent explicit `anas lock` is the code-trust transition for a
new module: it computes a candidate lock in memory, runs `validate` over the
extended topology, and writes the lock only if validation succeeds. That neither
executes a tampered hook before digest validation, nor lets "change config first
or change lock first" deadlock the addition of a module.

`set` and `explain` reject a parameter no manifest declares, name the closest
declared item, and use the usage exit code. A raw `env.<KEY>` that maps to a
declared parameter receives the same type validation and canonicalization; only a
valid environment key the schema does not recognize at all remains a permissive
compatibility entry point. If a legacy module declares a bare environment key
only in the legacy `required`, that key remains addressable solely as
`env.<KEY>` and is not faked into an invalid `<module>.<parameter>`.

`set` and `explain` share one complete `setting` shape, including `type`,
`allowed_values` for enums, `env_key`, input and resolution requirements, default
source, and single-field constraints. The types and the meaning of the metadata
are the same as in `config list`:

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "config": "/data/ws/config.yml",
  "setting": {
    "path": "samba_dc.admin_password", "module": "samba_dc",
    "parameter": "admin_password", "type": "string",
    "env_key": "SAMBA_DC_ADMIN_PASSWORD",
    "required": false, "input_required": false, "must_resolve": true,
    "has_default": false, "default_source": "generated", "default": "",
    "effect": "credential_rotate", "apply": "rotate-samba-admin-password",
    "sensitive": true, "description": "…"
  }
}
```

`setting.path` is **a configuration item's dotted path**, not a filesystem path —
the README's "paths are always absolute" does not apply to it.

`explain` needs no workspace; it reads the module registry only.

`plan`:

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "config": "/data/ws/config.yml",
  "applied_at": "2026-07-30T09:00:00Z",
  "matches_last_start": false,
  "changes": [{
    "key": "global.timezone", "change": "change",
    "path": "global.timezone", "module": "global", "parameter": "timezone",
    "effect": "container_recreate", "apply": "render-and-recreate",
    "sensitive": false, "description": "…"
  }],
  "module_plans": {
    "samba_dc": {
      "requested_mode": "auto",
      "resolved_mode": "ad_zone",
      "zone": "example.net"
    }
  }
}
```

`change` takes `add` / `remove` / `change`, an enumeration rather than a verb
chosen to fit a sentence. `applied_at` is `null` when nothing has ever started
successfully. `module_plans` has the same field semantics as deployment `plan`
and is independent of `changes`; validation metadata for selected modules may
appear even with `matches_last_start: true`. Human output likewise appends sorted
`module plan: <module> key=value ...` lines. A module validation failure returns
`config_invalid` (exit 4) uniformly, not `validation_failed`.

`secret list` **returns key names only**, and `secret get` returns values. The
reasoning is in the README's "sensitive values".

`secret get KEY -w WORKSPACE` and `secret get -w WORKSPACE KEY` must be
equivalent. Previously the former used standard flag parsing, stopped at `KEY`,
and silently discarded `-w`, so the command went on to read the secret of the
workspace named by the current directory or `ANAS_WORKSPACE` — reading this
deployment's key while the operator believed they were asking about another one is
not something argument order gets to decide.

The M3 `anasd` configuration endpoints consume this same typed application
schema: they do not parse CLI JSON and do not maintain a per-module HTTP
adapter, and the CLI, the HTTP API, and the web form model must stay
projections of one schema. The current M1A daemon does not yet expose
configuration HTTP endpoints.

| code | Exit code | When |
| --- | --- | --- |
| `usage` | 2 | Bad path format, unknown module, or wrong number of arguments |
| `config_missing` | 4 | The configuration file does not exist |
| `config_invalid` | 4 | The configuration cannot be parsed, violates the general schema, or a module `validate` hook rejected the desired state or returned an invalid response |
| `lock_invalid` | 4 | An existing lock file cannot be parsed |
| `lock_stale` | 4 | A pinned module's version/revision/bundle digest disagrees with the current registry |
| `secret_missing` | 4 | No such generated secret |
| `secrets_unreadable` | 4 | The secret store cannot be read |
| `state_unreadable` | 4 | Workspace state such as applied-setting fingerprints cannot be read |
| `write_failed` | 1 | Writing the configuration failed |

## credential

```text
anas credential list [-w WORKSPACE] [--json]
anas credential rotate CREDENTIAL_ID [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]
anas credential rotate --module MODULE [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]
anas credential rotate --all [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]
```

`list` reads the machine-credential inventory frozen into the active deployment.
It returns logical identity and status only — no values, hashes, verifiers, or
other digests usable for offline guessing. The current scope is deployment
credentials that declare `credentials.provides/consumes`; resource credentials,
local administrators, and external API tokens are still governed by their own
inventory and configuration boundaries, and cannot be declared rotatable merely
because a value exists in the secret store.

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "deployment_id": "20260820T010203Z-deadbeef",
  "credentials": [{
    "id": "eturnal.secret", "owner": "eturnal",
    "consumers": ["netbird", "nextcloud"], "kind": "shared_secret",
    "authority": "anas", "generation": 2,
    "rotation_mode": "reconcile", "status": "rotatable"
  }]
}
```

`status` takes `rotatable` / `manual` / `unsupported` / `orphaned` /
`recovery_required`. With an unfinished transaction, `list` remains readable and
additionally returns `recovery: {transaction_id, phase, credentials[]}`,
containing no plaintext.

`--dry-run` shares the planner with actual execution and reads active state plus
secret store presence/generation; it generates no random values, creates no
candidate, writes no journal or store, and calls neither hooks nor Docker.
`executable` in the result indicates whether blockers is empty:

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "dry_run": true, "executable": true,
  "plan": {
    "previous_deployment": "20260820T010203Z-deadbeef", "scope": "single",
    "credential_order": ["eturnal.secret"],
    "affected_modules": ["eturnal", "netbird", "nextcloud"],
    "stop_order": ["nextcloud", "netbird", "eturnal"],
    "activation_order": ["eturnal", "netbird", "nextcloud"],
    "blockers": [], "manual": [], "force": false, "all": false
  }
}
```

Actual execution requires the active runtime to be `running`, and requires `-y`
in a non-interactive call. The runner first generates an independent candidate
projection, stops all of previous or the affected closure, and runs
probe/reconcile/verify in ready-barrier order; only after everything validates
does it commit values, generation, and `rotation_id` in a single atomic store
save, followed by promoting the candidate. `--module MODULE` selects the complete
unified lifecycle set that module owns and declares in the active deployment; a
manual or unsupported item in the set blocks the whole batch. `--all` selects
every executable `reconcile` target in the active deployment; manual targets are
never quietly rewritten, and there is no `--allow-partial`.

`--force` only takes over external-authority records that have an ANAS generator,
a complete probe/reconcile/verify, and a valid owner/consumer graph all at once.
It is not a general switch for skipping blockers.

A successful JSON returns both `plan` and `rotation`, with `rotation.status` of
`complete`. Failures use `error.detail.rotation.status` to distinguish
`not_started`, `candidate_failed`, `previous_restored`,
`previous_restore_failed`, and `recovery_required`. A failure before the store
commit stops the candidate and restores previous; after the store commit the old
store is not written back, and the next exclusive runtime operation completes the
candidate promotion automatically from the journal and the
`rotation_id/generation`. No journal, JSON, progress, or hook stderr may ever
contain a credential value.

| code | Exit code | When |
| --- | --- | --- |
| `usage` | 2 | An invalid subcommand, target count, or `ID`/`--module`/`--all` combination |
| `confirmation_required` | 3 | An actual rotation in a non-TTY without `-y`, or an interactive confirmation that was declined |
| `no_active_deployment` / `deployment_unreadable` | 4 | There is no readable active deployment |
| `credential_recovery_required` | 4 | The dry run found an unfinished transaction; read-only list can still report the recovery state |
| `credential_rotation_blocked` | 4 | The planner, the runtime state, or the store generation/presence preflight found a blocker |
| `runtime_lock_failed` | 4 | The workspace runtime lock could not be acquired, or a journal awaiting recovery could not be read |
| `compose_missing` | 4 | Actual execution needs Compose but the host does not have it |
| `credential_rotation_failed` | 1 | The candidate, the compensation, the store commit, or the promotion failed; the specific state is in detail |

## admin local

```text
anas admin local list [-w WORKSPACE] [--json]
anas admin local credential MODULE [ACCOUNT] [-w WORKSPACE] [--password-only | --json]
anas admin local rotate MODULE [ACCOUNT] [-w WORKSPACE] [--prompt] [--json]
```

`list` is a non-leaking security inventory, returning only the module, account
id, purpose, username, and current entry point; it returns an empty list when
there is no local account. `credential` is the explicit sensitive read, returning
the entry point, username, password, and purpose together. `--password-only`
exists for interactive pipelines and prints one bare password line; it cannot be
combined with `--json`.

`ACCOUNT` is always the module manifest's `management.local_accounts[].id` (for
example `primary` or `break_glass`), never the physical username. When omitted,
selection prefers the account with id `primary`, then the module's only account;
any other multi-account case is an ambiguity error. `rotate` generates a random
password by default; `--prompt` can only read without echo from a real TTY and
requires a second confirmation. The command accepts no password argument, no
environment variable, and no plaintext YAML input.

Rotation first hands the candidate secret to the module handler frozen into the
active deployment alone. The handler updates the application's internal state and
verifies the new credential; a failed verification must restore the old
application state. Only on success does the runner atomically commit
`secrets.yml` (bootstrap-only applications also update a restricted runtime
secret projection). A module that does not declare `lifecycle.rotate` reports the
capability as unsupported explicitly, and the CLI must not guess at an update
method.

The account name lock lives in `.anas/local-admins.yml`, and passwords are
persisted only by `secrets.yml`; neither enters `config.lock.yml`, a deployment
manifest, or the deployment `.env`. The runner injects plaintext only during
module hook execution; bootstrap-only applications read it through a mode-0600
temporary projection under `.anas/runtime-secrets/`, and artifacts of
applications that support hashes persist only the hash.

| code | Exit code | When |
| --- | --- | --- |
| `usage` | 2 | A wrong subcommand, argument count, or flag combination |
| `confirmation_required` | 3 | `--prompt` used in a non-TTY, two entries that disagree, or a password policy that is not met |
| `local_admin_missing` | 4 | The module has no local account, or it has several and no id was given |
| `local_admin_state_unreadable` | 4 | The local administrator inventory cannot be read |
| `secret_missing` | 4 | The inventory exists but the corresponding random password is missing |
| `secrets_unreadable` | 4 | The secret store cannot be read |
| `local_admin_rotate_failed` | 1 | The handler, the verification, the rollback, or the secret commit failed |

## module

```text
anas module list [--source NAME] [-w WORKSPACE] [--json]
anas module versions NAME [--source NAME] [-w WORKSPACE] [--json]
anas module install NAME@VERSION-rN [--source NAME] [--digest sha256:...] [--json]
anas module sync [-w WORKSPACE] [--source NAME] [--json]
anas module update [MODULE...] [-w WORKSPACE] [--source NAME] [--json]
anas module commands [MODULE] [-w WORKSPACE] [--json]
anas module invoke MODULE COMMAND [-w WORKSPACE] [--param NAME=VALUE]... [-y] [--json]
```

`list` reads `anas.module-catalog/v1`; `versions` reads the standard tag list of
the module's OCI repository, filters `<semver>-r<N>`, and sorts descending by
SemVer then revision. The catalog provides only the discovery entry point and the
current release; the sole source of truth for historical versions remains the
registry tag list.

`install` downloads one specific release. The optional `--digest` requires the tag
to currently resolve to the given OCI manifest digest. Installation validates, in
order, the artifact/layer media types, the manifest/layer digests, the
`package.yml` identity, and the unpacked `content_digest`; absolute paths, `..`,
duplicate paths, symlinks/hardlinks, and device nodes are all rejected. The
result goes into a user-level content-addressed cache and modifies neither the
workspace nor the lock directly.

`update` is the only ordinary entry point that changes module versions in the
lock. It resolves the full dependency closure from `modules.<name>.version` or the
catalog's current release, writes the OCI manifest, content, and installation-tree
digests, runs defaults and module `validate` against an in-memory candidate lock,
and only then generates the workspace module view. When module names are given,
modules that were not named and already have a remote lock keep their existing
release; when none are given, the modules the configuration selects directly are
updated. `sync` only restores missing cache entries and views from the existing
lock and never upgrades; a local `bundle:<name>` in the lock is never quietly
replaced with a registry package by it.

The lock and `module-view.json` are still two independent paths: a write error
returned by the command restores the previous contents of both, but a `SIGKILL`
or power loss between the two renames can still leave a cross-generation
combination, which the later digest/trust gate fails closed on. Eliminating the
window entirely requires moving to a single generation pointer and serializing
remote writers against readers; until that storage upgrade is done, do not run
`module sync/update` concurrently with other workspace-writing commands.

`--source` takes precedence over the workspace's `module_source`. With neither,
`official` is used. The cache defaults to `anas/modules/` under the user cache
directory, overridable with `ANAS_MODULE_CACHE`. `--module-root` and
`ANAS_MODULE_ROOT` still take precedence over the workspace's remote view, for
source development.

`commands` does not reach the module registry; it reads only the module command
descriptors and executor digests frozen into the active deployment. With the
module omitted it lists every command sorted by deployment module order then
command ID; with no active deployment or no commands it returns an empty array.
The output carries only the public command description, typed parameters,
digests, and availability — no internal handler, executor path, env/secret key, or
injected value. The health of a remote service must be checked through a query
command the module declares; discovery itself connects to no remote service. The
full fields and security boundaries are in
[Module-specific commands](/en/reference/module-commands).

`invoke` accepts only module and command IDs frozen into the active deployment,
plus repeated `--param NAME=VALUE`. The CLI interprets neither handler nor
executor: the shared application service performs type canonicalization, digest
rechecking, runtime state checks, the env/secret allowlist, locking, timeout, and
the executor ABI. A destructive command requires `-y` on a non-TTY; a TTY
confirmation shows the public description, the deployment/release, and the
canonicalized parameters. Raw executor stdout/stderr is not passed through; with
`--json`, stdout still carries only one final envelope, and validated
progress/warnings are written to stderr as JSONL.

The current read-only anasd M1A exposes separate GET list/detail DTOs for
command discovery only in full state over HTTPS with an owner session, and no
invoke endpoint.

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/srv/anas", "deployment_id": "20260822T120000Z-ab12cd34",
  "module": "forgejo", "command": "incus-doctor",
  "changed": false, "result": {"project_state": "ready"}
}
```

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "source": "official-cn",
  "module": "nextcloud",
  "release": "34.0.2-r4",
  "oci_digest": "sha256:…",
  "content_digest": "sha256:…",
  "path": "/home/user/.cache/anas/modules/unpacked/sha256/…"
}
```

| code | Exit code | When |
| --- | --- | --- |
| `usage` | 2 | A wrong subcommand, source, release, or argument format |
| `lock_missing` / `lock_invalid` | 4 | `sync` has no usable lock |
| `config_not_managed` / `config_invalid` | 4 | The workspace configuration for `update` is not CLI-managed, or its content is invalid |
| `module_lock_local` / `module_lock_mismatch` | 4 | The lock's source is not OCI, or the cached content disagrees with the lock |
| `module_not_found` | 4 | A module the configuration selects is not in the catalog |
| `module_root_invalid` / `contract_root_invalid` / `contract_invalid` | 4 | An installed package's module/contract definitions cannot be loaded or validated |
| `resolution_failed` / `version_conflict` / `snapshot_policy_invalid` | 4 | Dependencies, exact versions, capability bindings, or host policy cannot be resolved |
| `module_source_unavailable` / `module_versions_unavailable` | 1 | The catalog, the tag list, or every fallback source is unavailable |
| `module_install_failed` / `module_sync_failed` / `module_update_failed` | 1 | Download, authentication, or a multi-layer digest/package validation failed |
| `module_cache_unavailable` / `module_cache_corrupt` | 1 | The cache directory is unavailable, or a content-addressed record or its content is corrupt |
| `module_view_failed` / `write_failed` / `lock_update_failed` | 1 | The workspace view or the lock resolution/transactional write failed |
| `invalid_module` | 2 | An invalid module name for `commands` |
| `invalid_module_command` / `module_command_invalid_parameter` | 2 | An invalid invoke target format, or duplicate, unknown, missing, or invalid typed parameters |
| `confirmation_required` | 3 | A destructive invoke on a non-TTY without `-y`, or a declined TTY confirmation |
| `module_not_active` / `module_commands_unavailable` | 4 | The named module is not in the active deployment, or the active artifact cannot be read |
| `no_active_deployment` | 4 | The workspace has no active deployment during invoke; commands discovery still succeeds and returns an empty list |
| `module_command_not_found` / `module_command_unavailable` / `module_command_changed` / `module_command_busy` | 4 | The command does not exist, a precondition check failed, the descriptor changed after confirmation, or a lock wait was cancelled |
| `module_command_timeout` / `module_command_failed` / `module_command_protocol_error` | 1 | The executor timed out, failed, or violated the bounded JSONL ABI |

## help

```
anas help [--json]
```

Human-readable help is prose and **is not a contract**. Under `--json` it becomes
a list of invocable commands instead:

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "commands": ["init", "plan", "lock", "render", "…"],
  "module_abi": "anas.module-hook/v1",
  "module_command_abi": "anas.module-command/v1"
}
```

It exists for exactly one reason: `anas help --json` must not be the one place on
stdout that is not a JSON document.
