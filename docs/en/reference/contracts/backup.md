# backup command JSON contract

> Status: **implemented** (`capabilities` / `plan` / `create` / `list` /
> `restore` / `verify`, plus the interactive form shown when no subcommand is
> given). The implementation is in
> [backup.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup.go),
> [backup_create.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup_create.go),
> [backup_transfer.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup_transfer.go),
> [backup_txn.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup_txn.go),
> [backup_restore.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup_restore.go),
> and [backup_cli.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup_cli.go).
> The common conventions (stream separation, exit codes, enumerations, time and
> size formats) are in the [common conventions](index.md) and are not repeated
> here.
>
> Implementation found four points where the first draft disagreed with reality;
> all four were corrected to match reality, and each is recorded in a
> "deviation from the first draft" section below. Two of them — "copy mode needs
> privilege too" and "f_fsid is not a filesystem identifier" — would have
> produced, respectively, a **silently incomplete backup** and a snapshot mode
> that **could never be selected**.

## Where the concepts divide

- **snapshot** = local, instantaneous, in service of rollback. See
  [snapshot.md](snapshot.md).
- **backup** = off-site, complete, in service of disaster recovery.

The `snapshots/` directory therefore **is not part of a backup by default** — it
is a CoW reference on the same disk, and copying it elsewhere is both enormous
and pointless. When a backup should carry a particular snapshot, it `send`s it
explicitly.

## The unit of backup is a snapshot

**backup does not define its own contents — it backs up a snapshot.** By the
definition in [snapshot.md](snapshot.md) a snapshot is already self-sufficient
(config, lock, secrets, the necessary state, runnable artifacts, data), so
backup's only remaining job is "get it somewhere else safely".

When `backup create` is run without `--snapshot`, it first creates a snapshot
with `reason: pre_backup` internally and then sends that. Backup contents
therefore always equal snapshot contents; there is no second set of
include/exclude rules.

Off btrfs there is no snapshot capability, and `copy` mode copies the workspace
directly, with the trade-offs in this table:

| Category | Contents | Included in copy mode |
| --- | --- | --- |
| Authoritative state | `config.yml`, `config.lock.yml`, `.anas/state/`, `.anas/secrets.yml` | ✅ |
| Application data | `data/` | ✅ |
| Active artifacts | `.anas/deployments/<active-id>/` | ✅ |
| Historical artifacts | the remaining directories under `.anas/deployments/` | ❌ |
| Caches | `.anas/go-build-cache/`, `.anas/hook-bin/`, `.anas/staging/` | ❌ never backed up |

The active artifacts must be included, for three reasons, each sufficient on its
own:

1. They are the **image build context** (compose says
   `build: context: ./${MODULE_NAME}`); without them not even
   `docker compose build` can run.
2. They are the **only runnable copy of the modules inside the workspace** — the
   module source tree lives outside it (`locateModuleRoot` looks for the module
   bundle via `ANAS_MODULE_ROOT`, cwd, and the executable's location).
3. They carry the **frozen hook binaries** (`<module>/.hook.bin`).
   `freezeHookBinary` deletes the `hook/` source directory as it writes them,
   precisely so the result runs without a Go toolchain. `.anas/hook-bin/` is only
   a build cache; the authoritative copy is here.

By volume, one measurement in a non-production environment put the active
artifacts at roughly 3% of data (42M against 1.3G), of which 41M was 13 frozen
hook binaries.

**Restore still pulls upstream base images from a registry**, in every mode. A
fully offline restore belongs to the phase-two `--include-images` scope.

---

## `anas backup capabilities`

Probes the source and destination and reports whether each backup mode is
available. **Interactive mode calls it internally and offers the user only the
options with `available: true`**; the web layer renders the same output itself.

```
anas backup capabilities [--to <dest>] [--json]
```

When `--to` is omitted only the source is probed, and every mode that depends on
a destination returns `available: false` with `reason: "dest_not_specified"`.

### Output

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/srv/anas",
  "source": {
    "fstype": "btrfs",
    "fsid": "3f2a1c8e-...",
    "data_is_subvolume": true,
    "data_is_mountpoint": false,
    "data_fully_readable": false
  },
  "dest": {
    "path": "/mnt/backup",
    "exists": true,
    "writable": true,
    "fstype": "ext4",
    "fsid": "9b41e0aa-...",
    "free_bytes": 900000000000
  },
  "tools": { "btrfs": true, "rsync": true },
  "privileged": true,
  "estimate": {
    "data_bytes": 1395864371,
    "state_bytes": 53248,
    "active_deployment_bytes": 44040192,
    "total_bytes": 1439957811
  },
  "modes": [
    { "id": "snapshot",  "available": false, "reason": "dest_not_same_filesystem" },
    { "id": "send",      "available": false, "reason": "dest_not_btrfs" },
    { "id": "send-file", "available": true,  "incremental": true,
      "parents": ["20260728T131040Z-cd6fc061"],
      "notes": ["restore_requires_btrfs_target"] },
    { "id": "copy",      "available": true,  "incremental": false,
      "notes": ["snapshots_excluded_by_default"] }
  ],
  "recommended": "send-file"
}
```

### How `recommended` is ordered (unspecified in the first draft)

`send` > `send-file` > `copy` > **`snapshot` (last)**.

Deliberately not the order of the mode table. The reason backups exist is **to
recover after the source disk fails as a whole**, and `snapshot` puts the second
copy on that same disk — recommending it means recommending something that is not
a backup. It remains available as an option (a same-disk copy taken in seconds is
still useful before an upgrade), but it is never recommended.

### Modes

| `id` | Conditions | Notes |
| --- | --- | --- |
| `snapshot` | source btrfs + `data/` is a subvolume + destination on the same fs as the source | Equivalent to `anas snapshot create`; fastest |
| `send` | source btrfs + destination btrfs + `btrfs` tool present + privilege | `btrfs send \| btrfs receive`; supports incremental |
| `send-file` | source btrfs + destination writable + `btrfs` tool present + privilege | `btrfs send > <file>`; can only be restored onto btrfs |
| `copy` | destination writable **+ all of data readable** | rsync; can be restored onto any fs. **The only mode available when the source is not btrfs** |

### Deviation 2 from the first draft: copy mode needs privilege too (the draft said "destination writable")

The first draft's mode table gave `copy` the condition "destination writable". A
historical measurement in a non-production environment (ordinary user, with the
workspace's modules actually having run) showed that this **does not hold**. lego
writes `data/lego/certs/accounts` (0700 root) plus `ca.key` and `*.key`
(0600 root) as root, an ordinary user cannot read them, and rsync exits 23.

`copy` is the only one of the four modes that **reads the data file by file**, and
therefore the only one that privilege can block:

| Mode | How it reads | Affected by directory permissions |
| --- | --- | --- |
| `snapshot` | btrfs metadata operation; reads no files | ❌ |
| `send` / `send-file` | reads the subvolume through the filesystem | ❌ |
| `copy` | reads file by file | ✅ |

This produces a counter-intuitive but real asymmetry: **the mode that needs the
most privilege to start is not the one privilege stops first.**

The handling matches the phase-two "reclaiming space needs privilege" decision:
**fail with a remedy, do not go half way**. Skipping unreadable files would
produce a backup "marked complete but missing every private key" — exactly the
kind of corruption `verify` exists to catch, and deliberately manufactured.

Detection lives in `capabilities`: estimating the size already walks `data/`, so
it records whether it hit `EPERM` along the way and reports
`source.data_fully_readable`. **It must be detected here**, or the refusal would
happen after the containers were already stopped.

The root cause is not btrfs but **container data being owned by root** — the same
point already argued in
[workspace-backup.md](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/archived/workspace-backup.md) §7; this is just its other
appearance, on the read side.

### The send family needs two transfer channels

**`btrfs send` can only send a subvolume.** In the snapshot directory
`snapshots/<id>/`, only `data/` is a subvolume; `snapshot.yml`, `meta/`, and
`deployment/` sit in ordinary directories and **send will not carry them**. The
`send` and `send-file` modes must therefore transfer in pairs:

1. `btrfs send` carries the `data` subvolume (optionally incremental with `-p`)
2. tar/rsync carries `snapshot.yml` + `meta/` + `deployment/`

The completion marker on the destination is written only after both channels
finish; if either fails, the backup is recorded as `complete: false`.

### Deviation 3 from the first draft: the destination layout is one directory per backup (the draft drew flat files)

The draft's action list wrote `/mnt/backup/<id>.data.stream` and
`/mnt/backup/<id>.meta.tar`, flat under the destination root. The implementation
uses **one directory per backup** instead:

```text
<dest>/
  .tmp-<backup-id>/     # in transfer; renamed as a whole on completion
  <backup-id>/
    backup.yml          # manifest, 0600, complete written last
    data.stream         # send-file
    meta.tar            # second channel for send / send-file
    data/               # snapshot (ro subvolume) / send (received subvolume) / copy (ordinary directory)
    meta/  deployment/  snapshot.yml    # second channel under snapshot / copy mode
```

Three reasons: `copy` mode has to write a directory tree anyway and a flat layout
cannot hold it; **renaming a directory** gives the same atomicity snapshot
creation has, and an interrupted product carries the `.tmp-` prefix so it appears
in no listing; and all four modes therefore have one shape, so **restore needs
only one code path**.

**There is no index file on the destination.** The destination is often a
removable disk or a shared directory written by several hosts, and an index would
become a second source of truth whose freshness nobody can guarantee. One
`backup.yml` per backup is the sole authority, matching how `snapshot.yml` is
handled on the source side. `chain_broken` is computed while listing, not stored —
whether an ancestor still exists is a property of the destination right now, not
of the moment of writing.

### Every mode produces the same "snapshot shape"

On a non-btrfs host `copy` mode has no snapshot to copy, so the implementation
assembles the same shape from the live workspace, naming each item from the table
above (`config.source.yml` → `meta/config.yml`, active artifacts →
`deployment/`, `data/` → `data/`). **Naming is the exclusion**: historical
artifacts and caches stay out of the backup because they were never named, and
there is no filter rule that could be written wrong — in particular none that
could miss `data/`.

`copy` mode is not subject to that restriction, but note that **rsync does not
preserve hard-link relationships by default** (`-H` is required). When a
snapshot's `deployment/` is implemented with hard links, omitting `-H` copies
every link as an independent full file — for a backup that is the right
trade-off, completeness before deduplication.

"Same filesystem" is decided by the **btrfs fsid**, not by `st_dev` — different
subvolumes on one btrfs have different `st_dev` values (measured: two sibling
subvolumes at 124 and 125, the parent directory at 43).

### Deviation 1 from the first draft: `statfs`'s `f_fsid` cannot be used either (unspecified in the draft)

The draft said "use the btrfs fsid" but not where to read it. The most natural
reading — `f_fsid` from `statfs(2)` — is **also wrong**, and wrong more
insidiously than `st_dev`, because its name looks exactly like "filesystem
identifier".

btrfs XORs the subvolume's root objectid into `f_fsid`:

```c
buf->f_fsid.val[0] ^= objectid >> 32;
buf->f_fsid.val[1] ^= objectid;
```

Historical measurement in a non-production environment (kernel 5.15):

| Path | `f_fsid` | `st_dev` |
| --- | --- | --- |
| `/data` (mount point) | `38df694b8bbdc98e` | 43 |
| `/data/…/ws/data` (subvolume) | `38df680d`​`8bbdc98e` | 129 |

The high 32 bits differ, the low 32 bits match. Comparing on it judges **a
destination on the same disk to be a different filesystem**, so snapshot mode
becomes permanently unavailable — a silent failure whose only symptom is one
missing mode.

The genuinely stable identifier is the **filesystem UUID**, and it can be read
without privilege: take the block device named by the mount table
(`/proc/self/mountinfo`) and match it against the device names under
`/sys/fs/btrfs/<uuid>/devices/`. `btrfs filesystem show` answers directly, but
like the other tree-search ioctls it needs root. When sysfs cannot be read, fall
back to probing with `cp --reflink=always` — asking the filesystem directly
whether it can perform the operation these modes actually depend on beats guessing
in either direction.

`data_is_mountpoint` likewise cannot be decided by "compare `st_dev` with the
parent directory": on btrfs that would judge every subvolume to be a mount point.
Decide it from the mount table, because only a real mount point makes the restore
path's `rename(2)` return EBUSY.

The same cause makes **`rsync --one-file-system` and `find -xdev` stop at
subvolume boundaries** (measured: `find -xdev` matched 0 files inside a
subvolume). `copy` mode therefore cannot rely on one-file-system to exclude
`snapshots/` — that would also drop the `data/` it must include — and needs an
explicit `--exclude`.

### `reason` enumeration (why a mode is unavailable)

| Value | Meaning |
| --- | --- |
| `dest_not_specified` | `--to` was not provided |
| `dest_not_exist` | The destination path does not exist |
| `dest_not_writable` | The destination is not writable |
| `dest_not_btrfs` | The destination is not btrfs |
| `dest_not_same_filesystem` | The destination is not on the same btrfs as the source |
| `source_not_btrfs` | The source is not btrfs |
| `data_not_subvolume` | `data/` is not a btrfs subvolume |
| `data_is_mountpoint` | `data/` is a mount point (`rename(2)` would return EBUSY and the restore flow cannot work) |
| `btrfs_tool_missing` | The `btrfs` command was not found |
| `insufficient_privilege` | send family: `CAP_SYS_ADMIN` missing; `copy`: `data/` not fully readable |
| `insufficient_space` | Free space at the destination is below the estimated size |

`insufficient_privilege` covers two different missing privileges — send's ioctl
needs `CAP_SYS_ADMIN`, `copy` needs to read data written by containers as root.
They share one code because the caller's response to both is the same (rerun as
root); `message` distinguishes them by mode, because what a person needs to know
is not the same thing.

### `notes` enumeration (available, with a caveat)

| Value | Meaning |
| --- | --- |
| `restore_requires_btrfs_target` | A backup produced by this mode can only be restored onto btrfs |
| `snapshots_excluded_by_default` | `snapshots/` is not included by default |
| `no_incremental_support` | This mode has no incremental support; every run is full |
| `crash_consistent_only` | With `--no-stop`, only crash consistency is provided |
| `plaintext_secrets_leaving_host` | The backup contains plaintext secrets (`config.yml`, `secrets.yml`) that will leave this host |

---

## `anas backup plan`

Full precondition validation plus the action list that would be executed —
**without executing it**. `backup create` runs it as its first step and fails
outright if plan does not pass. It is the data source for the web confirmation
page.

```
anas backup plan --to <dest> --mode <mode> [--snapshot <id>] [--parent <id>]
                 [--no-stop] [--json]
```

### Output

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/srv/anas",
  "mode": "send-file",
  "dest": "/mnt/backup",
  "incremental": true,
  "parent": "20260728T131040Z-cd6fc061",
  "estimate": { "transfer_bytes": 204010496, "dest_free_after_bytes": 899795989504 },
  "includes": ["config", "lock", "secrets", "state", "deployment", "data"],
  "excludes": ["history_deployments", "caches"],
  "stop_containers": true,
  "containers_to_stop": ["anas_traefik", "anas_authentik", "anas_nextcloud"],
  "estimated_downtime_seconds": 240,
  "warnings": [
    { "code": "plaintext_secrets_leaving_host", "message": "…" }
  ],
  "actions": [
    { "step": 1, "op": "acquire_lock",     "target": ".anas/state/lock" },
    { "step": 2, "op": "stop_containers",  "count": 13 },
    { "step": 3, "op": "snapshot_data",     "target": "snapshots/<new-id>/data" },
    { "step": 4, "op": "copy_state",        "target": "snapshots/<new-id>/meta" },
    { "step": 5, "op": "copy_deployment",   "target": "snapshots/<new-id>/deployment", "method": "reflink" },
    { "step": 6, "op": "seal_snapshot",     "target": "snapshots/<new-id>" },
    { "step": 7, "op": "start_containers",  "count": 13 },
    { "step": 8, "op": "send_stream",       "target": "/mnt/backup/<new-id>.data.stream" },
    { "step": 9, "op": "send_metadata",     "target": "/mnt/backup/<new-id>.meta.tar" }
  ]
}
```

`actions` is an execution preview for humans. **The order and the `op` names form
part of the contract**, but callers must not depend on `step` numbers being
consecutive. `method` on `copy_deployment` takes `reflink` / `hardlink` / `copy`,
matching the degradation order in [snapshot.md](snapshot.md).

When `--snapshot <id>` backs up an existing snapshot, the action list has no
`stop_containers` / `start_containers`; `use_snapshot` takes their place. That
snapshot already froze the data, and stopping again would buy nothing. In `copy`
mode the transfer action is `copy_files`; in `snapshot` mode it is
`snapshot_data` + `copy_state`.

Note that `start_containers` comes **before** `send_stream`: the containers only
need to be down while the snapshot is being taken, and send reads from a
read-only snapshot, so it can proceed after service is restored. This compresses
downtime from "determined by data volume" to "determined by how long the snapshot
takes" — a btrfs snapshot takes seconds, while sending 1.3G may take minutes.

---

## `anas backup create`

```
anas backup create --to <dest> --mode <mode> [--snapshot <id>] [--parent <id>]
                   [--no-stop] [-y] [--json]
```

`--snapshot <id>` backs up an existing snapshot; when omitted, a snapshot with
`reason: pre_backup` is created first and then sent. Backup contents always equal
snapshot contents.

Downtime behavior: by default all containers are stopped while the snapshot is
taken, and afterwards **the previous running state is restored** (only the ones
that were running are started). The whole process is recorded in
`.anas/state/transactions/`. **A failed backup must never leave services
stopped** — that is this command's one unacceptable failure mode, and after a
crash the next anas command to start must detect and compensate for it.

Two mechanisms, both required: in-process recovery via defer, so every error path
passes through it; and, because defer does not run when the process is
`SIGKILL`ed, **the transaction record is written to disk before the first
container is stopped**.

"The ones that were running" is read from compose (`ps -q`), not from the runtime
state file: the latter records anas's intent last time, not what Docker is
actually doing after a reboot or a manual `docker stop`. Use `stop`/`start`
rather than `down`/`up` — the containers only need pausing, and `up` would
rebuild them from the **current** compose files, which during a restore are not
necessarily what they originally were.

### Where compensation is triggered (the draft said "the next anas command")

The implementation hangs compensation on **acquiring the exclusive lock**
(`acquireRuntimeLock`) rather than on literally any command. The exclusive lock
is precisely what makes this safe: a backup holds the same lock throughout, so a
transaction record seen here cannot belong to a backup that is still running.
Read-only commands (`snapshot list`, `backup capabilities`) do not take the
exclusive lock, and should not — starting containers is a change, and having it
happen from `snapshot list` would be a surprise.

Compensation therefore happens on the **state-changing commands** — `apply`,
`rollback`, `snapshot create|delete|prune|pin`, `backup create` — which are also
exactly the ones an operator is about to run next.

`--no-stop` requires `-y` and carries the `crash_consistent_only` warning in the
output. Under `snapshot` mode the btrfs snapshot is atomic and the risk is
markedly lower than under `copy` mode, so the warning text must distinguish the
two.

### Progress `phase` enumeration

`acquire_lock` → `stop_containers` → `snapshot_data` → `copy_state` →
`copy_deployment` → `seal_snapshot` → `start_containers` →
`send_stream` + `send_metadata` / `copy_files` → `finalize`

### Output

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "backup_id": "20260729T081504Z-4a1b2c3d",
  "mode": "send-file",
  "dest": "/mnt/backup",
  "incremental": true,
  "parent": "20260728T131040Z-cd6fc061",
  "transferred_bytes": 204010496,
  "started_at": "2026-07-29T08:15:04Z",
  "finished_at": "2026-07-29T08:23:41Z",
  "downtime_seconds": 217,
  "snapshot_id": "20260729T081504Z-4a1b2c3d",
  "warnings": []
}
```

---

## `anas backup list`

```
anas backup list --to <dest> [--json]
```

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "dest": "/mnt/backup",
  "backups": [
    {
      "backup_id": "20260729T081504Z-4a1b2c3d",
      "mode": "send-file",
      "created_at": "2026-07-29T08:15:04Z",
      "incremental": true,
      "parent": "20260728T131040Z-cd6fc061",
      "size_bytes": 204010496,
      "deployment_id": "20260728T131040Z-cd6fc061",
      "config_digest": "sha256:…",
      "modules": { "nextcloud": "30.0.1", "authentik": "2024.10.5" },
      "complete": true
    }
  ]
}
```

`complete: false` marks a backup as the product of an interruption. When the
incremental chain is broken (the parent is missing), the entry carries
`"chain_broken": true`.

---

## `anas backup restore`

```
anas backup restore --from <src> -w <workspace> [--backup-id <id>] [--dry-run] [-y] [--json]
```

- **`-w` is mandatory and explicit.** `ANAS_WORKSPACE` is not accepted, and there
  is no inference from cwd.
- `--dry-run` reports the list of paths that would be written or overwritten,
  without touching disk.
- A non-empty target workspace requires `-y`.
- On completion a structural check and a `snapshot verify` run automatically, and
  their results are merged into the output.
- **Restore is all or nothing.** The secret store is append-only by generation,
  so restoring an older snapshot discards the generations after it. That is
  self-consistent when data rolls back with it, but "restore only meta and keep
  the current data" would mismatch keys against data and must be refused.
- `state/active.yml` does not come from the backup; it is regenerated from the
  snapshot's `deployment_id`.

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/srv/anas",
  "backup_id": "20260729T081504Z-4a1b2c3d",
  "restored": ["config", "state", "secrets", "data", "active_deployment"],
  "verify": { "ok": true, "checked": 6, "problems": [] },
  "next_steps": ["anas start -w /srv/anas"]
}
```

`next_steps` is an array of suggested command strings for the web layer to
present directly. Restore **does not start services automatically**.

### Deviation 4 from the first draft: restoring a `send` backup copies rather than sending again (unspecified in the draft)

A `send-file` backup must go through `btrfs receive`, so **restore also requires
`CAP_SYS_ADMIN`** and the target must be btrfs. An incremental backup must
further be received **in order along the parent chain, starting from the full
one**; the chain is resolved before anything is touched, because btrfs refuses an
out-of-order receive and by then the workspace would already have been modified.

The data of a `send` backup is already a real subvolume on the destination, so it
could be `btrfs send`ed back to preserve CoW sharing. The implementation
**copies directly instead**: sending again requires `CAP_SYS_ADMIN` on both ends,
which would mean "a mode that needs root to create needs root to restore" — and
by the time a restore is actually needed, that machine is quite likely freshly
installed. Whether a restore is possible should not be more demanding than whether
the backup was.

On the restore side all four modes are first normalized into the same
"snapshot-shaped" directory (`materializeBackup`), and **the workspace is only
touched after validation passes**. A backup missing its metadata channel must be
discovered before the data is replaced, not after.

---

## `anas backup verify`

```
anas backup verify --to <dest> [--backup-id <id>] [--json]
```

Checks whether backups are still usable: files and streams present, sizes
consistent with metadata, incremental chains intact. Designed to be called from
cron — the most common failure of a backup system is "believing there is one when
there is not".

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": false,
  "dest": "/mnt/backup",
  "checked": 4,
  "problems": [
    { "backup_id": "20260726T…", "code": "parent_missing", "message": "…" },
    { "backup_id": "20260727T…", "code": "size_mismatch",  "message": "…" }
  ]
}
```

### `problems[].code` enumeration

`stream_missing`, `metadata_stream_missing` (one of the two channels is missing),
`parent_missing`, `size_mismatch`, `metadata_unreadable`, `incomplete_backup`
