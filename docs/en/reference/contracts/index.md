# CLI contracts

This directory defines the **machine-readable output contract** of the `anas`
command line. These contracts serve external non-interactive consumers,
scheduled jobs, and the CLI's own interactive mode (interactive mode does not
implement a second code path; it only "detects, asks, and calls the
non-interactive path"). ANAS's own Web API does not consume these JSON
documents through a subprocess; it shares the typed Go application layer with
the CLI. The CLI contract remains an external compatibility boundary that must
be preserved, and a black-box regression baseline for verifying that both
adapters behave alike.

> **Status: every command is implemented.** The common conventions (stream
> separation, exit codes, enumerations, time and size, paths, versioning) were
> established by `snapshot`; the implementation is in
> [cli.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/cli.go),
> and dispatch plus uniform error wrapping is in `Main` in
> [runner.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/runner.go).
> Where an implementation disagrees with this directory, change the code to
> match, or change this directory first.

## Index

| Document | Commands covered |
| --- | --- |
| [backup.md](backup.md) | `anas backup capabilities` / `plan` / `create` / `list` / `restore` / `verify` |
| [snapshot.md](snapshot.md) | `anas snapshot list` / `show` / `create` / `restore` / `pin` / `unpin` / `delete` / `prune` / `verify` / `path` |
| [commands.md](commands.md) | `anas init` / `plan` / `lock` / `render` / `build` / `apply` / `start` / `restart` / `stop` / `rollback` / `status` / `deployments` / `config` |

## Common conventions

The following rules apply to every command that supports `--json`. The
individual documents do not repeat them.

### Stream separation

- **Structured results go to stdout**, and stdout carries **exactly one** JSON
  document. A caller can run `JSON.parse(stdout)` directly, with no line
  splitting and no filtering.
- **Progress, logs, and warnings go to stderr.** This holds for **subprocesses**
  too: `docker compose` output is a log, not a result, and is always attached to
  stderr. It was once attached to this process's stdout, and every command that
  started a container then piled a stack of image-pull lines in front of the
  JSON document, which broke "stdout holds exactly one document" on the spot.
- Without `--json`, stdout is human-readable text or empty (see "Commands with
  no result"). That format is not a contract; do not parse it.

### Progress output

Under `--json`, long-running commands write JSON objects to **stderr** one per
line (JSON Lines). Each line is a complete JSON value on its own, so a caller
reads them line by line **while the operation is running** rather than waiting
for a closing bracket that only arrives at the end:

```json
{"type":"progress","phase":"send-data","current":734003200,"total":1395864371,"unit":"bytes"}
{"type":"progress","phase":"stop-containers","current":8,"total":13,"unit":"modules"}
{"type":"warning","code":"plaintext_secrets_leaving_host","message":"..."}
```

The values of `phase` are defined by each command's document, as is which
commands emit progress at all; no list is maintained here that would go stale.
When `total` is unknown, **omit the field** rather than writing `0` or `-1` —
a caller would have to special-case either one as a real count.

### Confirmation

- For an operation that requires confirmation, `-y` / `--yes` is the only bypass.
- **When confirmation is required, `-y` was not given, and stdin is not a tty,
  fail immediately with exit code 3.** Never block waiting for input. This is
  the guarantee non-interactive callers need most: the command either finishes
  or returns at once.
- **stdin not being a tty is not itself an error.** Exit code 3 appears only
  when a confirmation is genuinely required and cannot be obtained.
  `anas status --json > out.json` also has a non-tty stdin, and it must exit 0
  normally. (An earlier wording said "`-y` not given and stdin not a tty", which
  taken literally would make every non-interactive call unusable — the vast
  majority of commands need no confirmation at all.)

### Exit codes

| Code | Meaning |
| --- | --- |
| 0 | Success |
| 1 | General error (failed **after work had started**) |
| 2 | Usage error (missing, mutually exclusive, unrecognized, or invalid argument) |
| 3 | A confirmation is required, `-y` was not given, and stdin is not a tty |
| 4 | Precondition not met (**work had not started**: not btrfs, snapshot missing, destination not writable, insufficient privilege) |

**The line between 1 and 4 is whether work had started**, not how severe the
error is. A missing configuration file, no active deployment, an expired lock
file, and a deployment that is not ready are all 4 — the environment does not
meet the conditions and the caller can fix it and retry. A failed render, a
failed hook, and a failed compose start are 1 — work was already underway when
it went wrong.

**The line between 2 and 4 is whether the mistake is in the command line or on
the machine.** `-w /nowhere` is 2: the argument itself is wrong. No active
deployment during `start` is 4: the arguments are fine, the machine is not ready.

On a non-zero exit, stdout still carries one JSON document, and it **also
carries `api_version`**:

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": false,
  "error": { "code": "dest_not_writable", "message": "…", "detail": {"path": "/mnt/backup"} }
}
```

On success the top level is `{"api_version": …, "ok": true, ...}`.

`detail` is optional and its values are defined by each command's document. When
there is no structured supplementary information, omit the whole field; do not
write `{}` or `null`.

### `ok` answers the question, not "did the command finish"

In most commands `ok: true` means "it worked". Verification commands
(`snapshot verify`, `backup verify`) differ: they **successfully** found a
problem, and in that case `ok` is `false` and the exit code is 1. This is
deliberate — both commands are meant for cron, where reading only the exit code
is normal, and "report success while the body says there is a problem" is
exactly how a missing subvolume goes unnoticed until the day it is needed.

Conversely, `snapshot show` on a corrupted snapshot is `ok: true`: the command
answered the question "what is this snapshot", health lives in `problems`, and
judging health is `verify`'s job. Merging the two would make "show a corrupted
snapshot" and "show a snapshot that does not exist" indistinguishable forever.

### Error codes and reason codes are enumerations, not prose

Every `code` and `reason` field takes a **fixed machine-readable enumerated
value** (`snake_case`, matching `[a-z][a-z0-9_]*`), not free text. `message` is
an English explanation for humans, and **callers must not parse it**. The CLI
holds its own enum-to-Chinese mapping table for human-readable output; the web
layer holds its own mapping table. Adding a language requires no change to any
output logic.

Enumerated values are only added, never changed. To retire a value, keep it and
mark it deprecated in the documentation.

The following codes are produced uniformly by the dispatch layer, may be
returned by any command, and are not repeated in the individual documents:

| code | Exit code | When |
| --- | --- | --- |
| `usage` | 2 | Missing, mutually exclusive, unrecognized, or invalid argument; unknown command or subcommand |
| `confirmation_required` | 3 | A confirmation is required and cannot be obtained, or the user declined in interactive mode |
| `internal` | 1 | An unclassified error escaped to the top level. **Its appearance is a defect**; a specific code should be added |

### Commands with no result

Some commands have no natural result: `stop` either stopped the services or it
did not. They emit a **minimal envelope** — `api_version`, `ok`, and enough
identification to say what was operated on (workspace, deployment id) — and
nothing more.

Do not invent a payload to make a document "look substantial". Invented fields
will be depended on by callers, and after that they can never be removed.
Equally, callers **must not** wait for result fields from these commands.

Without `--json` these commands produce empty stdout. That is correct behavior,
not a defect.

### Time and size

- Times are always RFC 3339 UTC strings (`2026-07-29T08:15:04Z`). When the
  moment does not exist, write `null`, not an empty string — a caller cannot
  distinguish an empty string from "never happened".
- Sizes are always **integer bytes**, and the field name ends in `_bytes`. No
  unit conversion, and never `"1.3G"`.
- **When it cannot be measured, write `null`, not `0`.** Without a btrfs qgroup
  there is no measurement, and `0` is a fake one. The field still appears
  (`"size_bytes": null`), so a caller always gets the key.

### Paths

**Filesystem paths** are always absolute; a relative path never appears in any
JSON output.

This rule covers filesystem paths only. The `path` field of `config explain` is
a dotted configuration path (`global.domain`), not a filesystem path, and is not
subject to it — applying the rule by field name alone would turn it into a
reported bug.

### Sensitive values

`config secret list` returns key names only. Its purpose is "see which secrets
were generated", and returning the values with them would let a routine
inventory command leak every key into anywhere that captured its output.

`config secret get` is the only command documented to return a plaintext secret;
that is its entire purpose. Callers should treat it accordingly: its stdout has
a different sensitivity level from any other command's stdout.

### Versioning

Every JSON document carries a top-level `"api_version"` in the form
`anas.dev/cli/v1` — **on both success and failure documents**. Adding a field
does not raise the version; removing a field or changing its meaning does.

### Tests keep this consistent

- [internal/runner/contract_test.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/contract_test.go)
  walks table-driven through every command that does not need a real deployment:
  exactly one document on stdout, a complete envelope, and exit codes asserted
  one by one against specific numbers.
- [test-env/scripts/test-contract.sh](https://github.com/anas-project/ANAS/blob/master/test-env/scripts/test-contract.sh)
  reruns the exit-code portion with the **compiled binary**. `go run` collapses
  every non-zero status into 1, and a Go test only ever sees the returned error —
  the number the process actually exits with is visible only here.
