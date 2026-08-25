# snapshot command JSON contract

> Status: **implemented** (`list` / `show` / `create` / `restore` / `pin` /
> `unpin` / `delete` / `prune` / `verify` / `path`). The "automatic triggers"
> section (`data_breaking`, the automatic-snapshot trigger conditions, and the
> rollback decision) **has also landed**. `diff` is deferred to phase two.
> The common conventions are in the [contract index](index.md) and are not
> repeated here. The dividing line against backup is in [backup.md](backup.md).

## Storage layout

```text
<workspace>/
  config.yml
  config.lock.yml
  data/                   ← btrfs subvolume (a snapshot's source must be a subvolume)
  snapshots/              ← ordinary directory, 0700
    .tmp-<snapshot-id>/   # snapshot being created; renamed into place on completion
    <snapshot-id>/
      snapshot.yml        # metadata, 0600, the complete field written last
      meta/
        config.yml            # taken from the artifact's config.source.yml, not the current on-disk value, 0600
        config-managed.yml    # the CLI integrity digest matching that config.yml, 0600
        config.lock.yml
        secrets.yml # the secret store at that moment, 0600
        local-admins.yml       # local administrator locked names and secret logical keys, 0600
        deployment-state.yml  # the single matching .anas/state/deployments/<id>.yml
      deployment/         # a full copy of .anas/deployments/<id>/
      data/               # a read-only btrfs snapshot of data
  .anas/                  ← 0700
    state/
      snapshots.yml       # derived index; can be rebuilt by scanning
```

**A snapshot must be self-sufficient enough to restore the system on its own.**
Once it has been sent to an external disk there is no `.anas` over there, so
config, lock, secrets, runnable artifacts, and data are all real copies rather
than references. The one thing that cannot be covered is the upstream base
images (a registry is required); see "Restore semantics".

`config.yml` and `secrets.yml` contain plaintext secrets, and `local-admins.yml`,
while it holds no passwords, is security inventory. `snapshots/` is therefore
0700 as a whole and the files under `meta/` are 0600. All three must roll back
together on restore, or the username lock and the password logical key could
point at different accounts.

**`snapshots/` is an ordinary directory, not a subvolume.** Only a snapshot's
**source** must be a subvolume (`data/`); the target only requires that the
parent directory exists on the same btrfs, and `snapshots/<id>/data` is created
**as** a subvolume. Making the container directory a subvolume too buys nothing
(qgroups already account per snapshot, send only ships a single snapshot, and
`--one-file-system` would exclude `data/` along with everything else) while
making a directory containing nested subvolumes impossible to delete directly,
and cutting off the hard-link fallback below it.

### How `deployment/` is copied

Copy degrades through this order:

1. `cp --reflink=auto` — available on btrfs
2. Hard links — **with a precondition, see below**. Because `snapshots/` is an
   ordinary directory rather than a subvolume, this does not cross a boundary on
   btrfs either, so there is no `EXDEV` restriction
3. Full copy — the fallback, roughly 42M per snapshot

### The hard-link precondition: artifact sealing (**landed**)

Hard links share an inode with the source, so **any in-place write to a
deployment file simultaneously contaminates every snapshot referencing it**.
Design document §13 requires that "ordinary release assets become read-only after
sealing". That sealing is implemented by `sealDeployment`
([artifact.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/artifact.go)),
executed before staging is renamed into `deployments/<id>/`, so **all three
degradation steps are available**.

Sealing **clears write permission bitwise** rather than assigning a fixed mode,
preserving the two distinctions render already made: executables stay executable
(0755 → 0555), owner-only sensitive files stay owner-only (0600 → 0400), and the
rest land on 0444. Assigning a uniform 0444 would turn `.env` from 0600 into
world-readable — widening the access surface in the name of read-only.

**Directories stay 0700 and are not sealed.** A read-only directory would also
block unlink, and unlink-and-replace allocates a new inode, which is exactly the
kind of change hard links are already safe against; sealing directories buys no
extra guarantee while making deployments impossible to reclaim.

The `artifact_copy` field of `snapshot.yml` records which step was actually used
(`reflink` / `hardlink` / `copy`). Step 1 uses `cp -a --reflink=always` rather
than `=auto`: `auto` degrades silently to a full copy, which would make the
recorded step a lie.

Copying it buys the disappearance of a cross-subsystem invariant: deployment GC
**does not need** to read the snapshot index to avoid referenced artifacts, and
pinning a snapshot no longer has to pin a deployment along with it.

### `state/` copies only what cannot be rebuilt

| Contents | Copied | Reason |
| --- | --- | --- |
| `state/deployments/<id>.yml` | ✅ only the matching one | Cannot be rebuilt |
| `state/active.yml` | ❌ regenerated on restore | Its `previous_deployments` reference deployments absent from the snapshot, so copying it in leaves dangling references. This snapshot's active is implied by `deployment_id` |
| `state/index.yml` | ❌ | Rebuildable by definition |
| `state/transactions/` | ❌ | Diagnostic and transient |
| `state/lock` | ❌ | A runtime lock |

### Deleting a subvolume needs privilege (a measured conclusion, contrary to the common claim that "create/snapshot/delete all work without root")

`btrfs subvolume create` and `snapshot` work for an ordinary user; `delete` does
**not**: `BTRFS_IOC_SNAP_DESTROY` requires CAP_SYS_ADMIN unless the filesystem
was mounted with `user_subvol_rm_allowed`. The following is a historical
measurement from an independent non-production environment (kernel 5.15, `/data`
without that option):

| Operation | Ordinary user |
| --- | --- |
| `btrfs subvolume create` | ✅ |
| `btrfs subvolume snapshot -r` | ✅ |
| `btrfs subvolume delete` (non-empty subvolume) | ❌ EPERM |
| `btrfs property set -ts <p> ro false` | ✅ |
| `rmdir` an empty subvolume | ✅ (kernel ≥ 4.18) |
| `rm -rf` the contents of a read-only snapshot | ❌ Read-only file system |

**Every command that reclaims space** (`delete`, `prune`, the retention policy
after apply, cleaning up interrupted `.tmp-*`) therefore fails when that mount
option is absent. The implementation's handling is to **fail with a remedy**
(`subvolume_delete_denied`, exit code 4) rather than fall back to "clear the ro
flag and delete recursively":

- that path still hits EACCES part way through on **data directories written by
  containers as root** (the normal case on a NAS);
- failing part way leaves a half-deleted snapshot — exactly the kind of
  corruption `verify` exists to catch, and manufacturing it is worse than
  refusing.

The remedy: `mount -o remount,user_subvol_rm_allowed <fs>` plus an fstab entry,
or run the reclaiming commands as root.

### Atomicity of creation

Everything is written to `snapshots/.tmp-<id>/` first; after all contents are on
disk and `fsync`ed, the whole thing is `rename`d to `snapshots/<id>/`, and
`complete: true` in `snapshot.yml` is written last. A `.tmp-*` directory left by
a failure part way is cleaned up the next time any anas command starts.

### Restore semantics

**All or nothing.** The secret store is append-only by generation, so restoring
an older snapshot discards the generations created after it. That is
self-consistent when data rolls back with it, but "restore only meta and keep the
current data" would mismatch keys against data, and the CLI must refuse that
combination.

After a restore the upstream base images still have to be pulled from a registry.
A fully offline restore needs a `docker save`-level image archive and belongs to
the phase-two `--include-images` scope.

## Source of truth

**`snapshot.yml` is the sole authority.** `.anas/state/snapshots.yml`
(deployment → snapshot list) is a **derived index**, fully rebuildable by
scanning `snapshots/*/snapshot.yml`, and `anas snapshot verify --rebuild-index`
rebuilds it.

There is no second authoritative direction. `deploymentState.SnapshotID` should
be removed — it serves exactly one query, "which snapshot should a rollback from
Y to X use", and that query is answered by `from_deployment` / `to_deployment`;
keeping it only creates a double-write inconsistency window.

## Retention policy

- `snapshot.keep_auto` (default **5**): among snapshots with `kind: auto` and
  `pinned: false`, keep the N most recently created and reclaim the rest.
  **The implementation location does not match the naming used here**: the
  snapshot section currently hangs under `rollback.snapshot` in configuration
  (`backend` / `source` / `root` are already there), so it is actually written
  `rollback.snapshot.keep_auto`. The field name in JSON output is still
  `keep_auto`, matching this document. Promoting the whole section to a
  top-level `snapshot:` is a separate configuration rename, out of scope here.
  Use `*int` rather than `int`: an explicit `keep_auto: 0` (keep none) must be
  distinguishable from "not written", which takes the default of 5.
- `kind: manual` and `pinned: true` never take part in automatic reclamation,
  **and do not count toward N**.
- When reclamation happens: after each successful `apply` commits active, and on
  an explicit `anas snapshot prune`.
- Deployment GC is **uncoupled** from snapshots: a snapshot holds its own copy of
  the artifacts (see above), so reclaiming `.anas/deployments/` affects no
  snapshot.

## Automatic triggers

The `upgrade` node **already exists**
([manifest.go:104](https://github.com/anas-project/ANAS/blob/master/internal/runner/manifest.go)),
and this design adds `data_breaking` beneath it rather than creating another
top-level field:

```yaml
upgrade:
  from: ">=30.0.0"           # existing: allowed source versions; validateUpgrade blocks anything else outright
  data_breaking: ["31.0.0"]  # new: crossing it rewrites the data format
```

The two mean different things and neither can be derived from the other: `from`
says "can this be upgraded", `data_breaking` says "having upgraded, can it come
back". A module may allow upgrading from any version while one particular upgrade
still rewrote the data format.

Before this work landed **no module declared `upgrade:` at all**, so both fields
were purely additive; on landing, all 17 modules were given
`data_breaking: []`, and `from` is still unused by every one of them.

`data_breaking` lists **the versions at which the on-disk data format breaks**:
once a version at or above it has been deployed, the on-disk data can no longer be
read by any version below it.

It is a list rather than a boolean because breaking is **a property of the
transition, not of the version**: 30.0.1 → 30.0.2 is not breaking, 30.0.1 →
31.0.0 is, and 30.0.1 → 33.0.0 crosses two break points at once. Only listing the
break points makes it possible to judge a jump between any two versions.

### The decision

With `A` currently deployed and `B` as the target:

```
breaking  ⟺  ∃ V ∈ data_breaking,  A < V ≤ B
```

`≤ B` rather than `< B`: upgrading to the breaking version itself already
rewrites the format. When several modules upgrade at once, any one breaking makes
the whole thing breaking — a snapshot is workspace-wide, so one is enough.

The decision is symmetric in direction: the interval `(min, max]` does not care
which end is the origin, and upgrade and rollback use the same comparison. They
differ only in what the caller does with the answer.

### Which declaration to use (unspecified in this document's first draft; the implementation follows the rule below)

A transition involves two versions, and each one's module.yml carries a
`data_breaking`. **Take the one from the higher version.**

Only "the release that caused the break" can possibly know that it broke; the
lower version's module.yml was written before the break and cannot mention it.
Taking the lower version's declaration would return "compatible" precisely on the
genuinely dangerous transition.

So the upgrade direction takes the **target's** declaration and the rollback
direction takes the **currently deployed** one — both being "the one from the
higher version". In the implementation, `data_breaking` is frozen into
`modules.<name>.data_breaking` in `deployments/<id>/deployment.yml`, and the
decision reads the frozen value rather than the module package currently on disk,
so a deployed system's verdict does not change because somebody updated a bundle.

### Anything unparseable degrades to "unknown"

When a version number or a declared entry fails to parse, the verdict is
**unknown** (blocking), not "compatible". An error in a declaration is a bug, and
the safe reading of a bug in a safety declaration is "this gate may have been
meant to apply". `module.yml` validates that every entry is valid semver at load
time (`loadModuleManifest`), so this path is only reached when a frozen artifact
has been corrupted by hand.

### Undeclared ≠ declared empty (the distinction is mandatory, or the default inverts)

Treating "no `data_breaking` declared" as an empty list makes the formula above
always false → the verdict becomes "never breaking" → **every rollback is
allowed**. The previous behavior was **to block everything by default**, and at
the time not one of the 17 modules declared `upgrade:`. Treating it as an empty
list would flip the default from most conservative to most permissive — a
security regression that would take effect silently.

Therefore:

| Declaration | Meaning | Rollback verdict |
| --- | --- | --- |
| No `data_breaking` | **Unknown** | Status quo: any version difference blocks |
| `data_breaking: []` | An explicit declaration that no version transition rewrites the data format | Allow |
| `data_breaking: ["31.0.0"]` | Break points listed | Decided by the formula above |

The implementation must use `*[]string` to distinguish `nil` from an empty slice;
`[]string` will not do.

**Pre-release action**: give every module an explicit `data_breaking: []`. It
means "there is no break point up to the current version" — a verifiable
statement of fact, not a promise about the future. Shipping with them all left
blank would make rollback permanently blocked as "unknown".

The maintenance rules for the list (add only, and when it may be trimmed) are in
"Rules for module authors" below.

Release granularity uses a module's `(version, revision)`, not the
display-only `app_version`, consistent with `validateUpgrade`. Different
`version`s compare by SemVer; identical `version`s compare by integer `revision`.
A revision transition within one version is also a release change: without a
`data_breaking` declaration it is still treated as unknown, and only an explicit
`[]` means compatible.

### The upgrade direction

Breaking → a `kind: auto` snapshot is created automatically before `apply`.
`--no-snapshot` disables it explicitly, and because that gives up the only way
back, it requires `-y`.

`apply` gains three flags for this:

| Flag | Effect |
| --- | --- |
| `--snapshot` | Always create one whether or not it triggered, recorded as `reason: pre_apply` |
| `--no-snapshot` | Do not create one even when triggered; requires `-y`, and a non-tty without `-y` exits 3 |
| `-y` / `--yes` | Confirms the two scenarios above that require confirmation |

Giving `--snapshot` and `--no-snapshot` together is a usage error (exit code 2).

### The rollback direction

`deploymentRollbackVersionBlockers`
([deployment.go:766](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go))
currently judges **any** version difference as "data compatibility unknown" and
blocks it — not even a patch-level change may roll back. The comment above that
function states outright that this is a conservative placeholder awaiting this
contract. It becomes three tiers:

**`rollback` never touches data.** It only switches artifacts, and there is no
`--restore-data` switch. Rolling data back always goes through
`anas snapshot restore`.

| Case | Result |
| --- | --- |
| The target deployment's versions are **identical** (a pure configuration rollback) | ✅ Allowed, data untouched (already the status quo) |
| Versions changed, and the module declared no `data_breaking` | ❌ Blocked (`--allow-risky` bypasses) — status quo |
| Versions changed, declared, and the reverse crosses no break point | ✅ Allowed |
| Versions changed and the reverse **crosses a break point** | ❌ **Hard error with no bypass**, pointing at snapshot restore |

The third tier is the only relaxation in this work — narrowing "any version
difference needs `--allow-risky`" to "only crossing a break point does". This is
**an experience improvement, not a safety improvement**, and it depends on module
authors declaring correctly, so the default must stay conservative (see above).

The fourth tier is given no `--allow-risky` escape hatch: crossing a break point
means the old code **definitely** cannot read data in the new format, and
allowing it would only leave services unable to start. The error message must
give the next step:

```
cannot roll back nextcloud 31.0.0 -> 30.0.1: crosses data-breaking version 31.0.0
data written by 31.0.0 cannot be read by 30.0.1

to return to that state, restore a snapshot instead:
  anas snapshot list
  anas snapshot restore <id>
```

That error's `code` is `data_breaking_crossed` with exit code 4 (precondition),
which distinguishes it at the machine-readable level from the
`--allow-risky`-bypassable class (a general error, exit code 1). The decision
runs before `--allow-risky`, so adding the flag does not get around it.

**Module additions and removals keep blocking as before** (`--allow-risky`
bypasses): an added module has no corresponding version in the target, and a
removed module's data stays on disk with nobody to own it. Neither is a
version-interval question, and `data_breaking` has nothing to decide with.

### Three things this simplifies away (**all landed**)

1. **The `rollback --restore-data` flag is removed**, and `rollback` becomes
   purely an artifact switch.
2. **The transition-pairing check in `restoreDeploymentSnapshot` is removed**
   (along with `restoreDeploymentSnapshot`, `createDeploymentSnapshot`, and
   `dataSnapshot` themselves) — the only reason that check existed was that a
   snapshot was bound to a particular transition. Once a snapshot is
   self-sufficient it is simply **a point in time**, and restoring it needs
   nothing from `deployments/`. `from_deployment` / `to_deployment` are kept as
   fields but demoted to human-readable context, taking part in no decision.
3. **`deploymentState.SnapshotID` is removed** (it was already slated for
   removal; here it loses its last reason to exist).

### Why "rollback without data" is not dropped

An artifact rollback and a snapshot restore **do not solve the same problem**,
and the former is not a weaker version of the latter:

| Scenario | The right move | If a snapshot restore were forced |
| --- | --- | --- |
| Wrong domain/port/resource limit, services will not start, **the data is fine** | Artifact rollback | **Loses all data since the last apply** |
| 30.0.1 → 30.0.2 has a regression, the data format did not change | Artifact rollback | Same as above |
| An upgrade corrupted the database schema | Snapshot restore | Correct |

The first two are the most common rollback scenarios. In a NAS setting
(Nextcloud files, mail, database writes), "rolling back means losing everything
written since" is catastrophic — the user is rolling back to fix a configuration
mistake, not to give up a week of work. Dropping artifact rollback would leave
the most common case with no correct answer to choose.

### Configuration-change triggers

The code already has a set of effects that "cannot be reversed automatically".
**Reuse it directly rather than inventing a second standard**: the deployment-side
set is collected in `guardedSettingChanges`
([deployment.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go)),
so apply blocking and the automatic-snapshot trigger read the same function and
cannot drift apart. `ensureNoGuardedChanges`
([config_state.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/config_state.go))
is the same set used again on the start path.

| effect | Triggers a snapshot | Reason |
| --- | --- | --- |
| `data_migrate` (7 uses) | ✅ | Existing data must be migrated |
| `credential_rotate` (10 uses) | ✅ | It changes state **inside** the service (a password in LDAP/DB); changing config.yml back does not change it back |
| `immutable` | — | Cannot be changed at all, so it never happens |
| `reconcile` | ❌ | Hooks are required to be idempotent, and it changes rebuildable external state |
| `container_recreate` / `hot_reload` / `container_restart` / `process_restart` | ❌ | Does not touch data |

The division of labor: `data_migrate` and `credential_rotate` are triggered by
**configuration changes**, `data_breaking` by **version upgrades**.

**Do not snapshot on every apply**: routine applies would fill the `keep_auto`
slots and squeeze out the pre-breaking snapshot that actually matters. To force
one, use an explicit `--snapshot`.

### Boundaries

- **Downgrade**: `validateUpgrade` already forbids it entirely (`cmp > 0` errors
  outright), so `A > B` cannot occur
- **Module addition**: there is no `A`, so breaking is not judged (there is no
  old data)
- **Module removal**: data stays on disk, and the existing block is kept to be
  conservative
- `upgrade.from` and `data_breaking` are **independent**: the former decides
  "can this be upgraded", the latter "having upgraded, can it come back"

### Rules for module authors

**In one sentence: when in doubt, declare it.**

The consequences are asymmetric:

| | Consequence | Direction |
| --- | --- | --- |
| **Missing** (should have been declared, was not) | No snapshot before the upgrade; the rollback is allowed, the old code cannot read the new format, and **services will not start** | Dangerous |
| **Extra** (declared when it should not have been) | One extra snapshot; rollback is over-blocked, and `--allow-risky` bypasses it | Safe |

The cost of missing one is an unrecoverable data incident; the cost of an extra
one is a redundant confirmation. These are not the same order of magnitude, so
when the judgment is unclear, declare it.

Five operational rules:

1. **Give every module an explicit `data_breaking: []` before release.** Omitting
   it means "unknown" and makes any version change in that module impossible to
   roll back. `TestBundledModulesDeclareDataBreaking` guards this. As of now all
   17 modules declare `[]` — a verifiable statement of fact (nothing has been
   released yet, so no release has rewritten a data format), not a promise about
   the future.
2. **A break point must be written in the same commit as the version that causes
   it.** The declaration is frozen into `deployment.yml` at render time and the
   decision reads the frozen value, so **adding a break point to a version that
   has already been deployed does not retroactively protect that existing
   deployment** — it froze the empty list from before. A late addition only
   applies to deployments rendered afterwards.

   Historical measurement (independent non-production environment, 2026-07-31):
   9.0.0 was deployed with `data_breaking: []`, 9.0.0 was then added to the list
   afterwards, and rolling back from that deployment to 2.5.1 **was still
   allowed**. Changing it to "declare 9.0.1 breaking at the same time as
   upgrading to 9.0.1" made the rollback refuse immediately.

   This follows necessarily from freezing, and freezing is right: the alternative
   is to read the module package currently on disk at decision time, which would
   make a deployed system's verdict change because somebody updated a bundle. The
   price is that late additions do not work — hence the rule is **the same
   commit**, not "add it when you remember".

3. **Existing entries are added to, never modified.** Modifying a historical entry
   changes the verdict for deployments **rendered afterwards**: a transition that
   was rollback-eligible yesterday becomes blocked today, or worse, the reverse.
   Once an entry has shipped it is history.
4. **They need not accumulate forever: trim when you raise `upgrade.from`.** The
   lower bound of `A` is guaranteed by `upgrade.from` (`validateUpgrade` rejects
   any source version that fails the constraint), so **any entry with
   `V ≤ the lower bound of upgrade.from` can never satisfy `A < V` and is dead**:

   ```yaml
   upgrade:
     from: ">=30.0.0"             # anything below 30 cannot be upgraded from at all
     data_breaking: ["31.0.0"]    # the historical 21.0.0/25.0.0/28.0.0 can no longer match; delete them
   ```

   **Raising `upgrade.from` is the only safe time to delete**, because the
   constraint itself already keeps those source versions out. A deletion at any
   other time relaxes a safety declaration that has already shipped.
5. **`upgrade.from` must be written with an explicit lower bound** (the `>=X`
   family). Something like `"!=31.0.0"` has no lower bound and makes rule 4
   impossible to carry out.

(Once independent module distribution lands, this could become a single boolean
per version, with the runner walking every version between `A` and `B` to decide.
It is impossible today because the runner only holds the module.yml of the two
versions involved. See
[module-distribution-draft.md](/architecture/module-distribution-draft).)

### Off btrfs

Snapshots cannot be created, so a breaking upgrade must print an explicit warning
and require `-y`; it must not continue silently.

When `rollback.snapshot.backend` is unconfigured, `anas lock` probes the
workspace and freezes the result into `config.lock.yml`: when available it writes
`btrfs` with `keep_auto: 5`, and otherwise `none`. At runtime "cannot create a
snapshot" therefore has two forms: backend is `none` in the lock, or
`<workspace>/data` has since stopped being a btrfs subvolume. Both take the same
path: print one line to stderr for the trigger and one for why no snapshot can be
created, then require confirmation (`-y`, or an interactive confirmation on a
tty). A non-tty without `-y` exits 3. **No path silently skips the snapshot when
the trigger condition holds.**

---

## `snapshot.yml` fields

```yaml
api_version: anas.dev/snapshot/v1
id: 20260729T081504Z-4a1b2c3d
backend: btrfs
kind: auto              # auto | manual
pinned: false
created_at: 2026-07-29T08:15:04Z
reason: module_upgrade_breaking    # enumeration, see below
label: "before upgrade"           # free user text, may be empty
source: /srv/anas/data
path: /srv/anas/snapshots/20260729T081504Z-4a1b2c3d/data
from_deployment: 20260728T041632Z-a9f9519d   # optional, human-readable only, not consulted on restore
to_deployment: 20260728T131040Z-cd6fc061     # same
deployment_id: 20260728T131040Z-cd6fc061
config_digest: sha256:…
lock_digest: sha256:…
modules: { nextcloud: "30.0.1", authentik: "2024.10.5" }
artifact_copy: hardlink  # reflink | hardlink | copy, the degradation step actually used
complete: true           # written last; its absence marks an interrupted product that cannot be restored
```

Two deviations from the first draft, both corrected to match reality:

- **No single-valued `secret_generation`.** A `secrets.yml` record may now carry
  owner/kind/provenance/generation/rotation_id, but a generation belongs to each
  logical credential, and no scalar can represent the whole snapshot. The
  snapshot already copies the complete secret store at that moment; writing
  either `0` or the maximum generation would be a fake measurement, so snapshot
  metadata continues to omit the field. Should an index be needed later, add a
  generation map keyed by logical ID.
- **`recovery_path` is deleted.** It described "where the original data was moved
  aside to during a rollback". Once a `reason: pre_restore` snapshot is
  mandatory before a restore, that moved-aside data is already inside a named
  snapshot and is deleted in place once the restore succeeds; keeping another
  directory whose meaning nobody knows would only consume disk. Undoing a restore
  now goes through `snapshot restore <pre_restore-id>`, whose id is in the
  returned JSON.

### `reason` enumeration

| Value | Trigger | Status |
| --- | --- | --- |
| `manual` | The user ran `anas snapshot create` | Implemented |
| `pre_apply` | The automatic snapshot before `apply` switches deployment | Implemented |
| `pre_restore` | Before `snapshot restore` runs, to make the restore itself undoable | Implemented |
| `pre_backup` | The snapshot `anas backup create` takes internally | Reserved |
| `module_upgrade_breaking` | A `data_breaking` point was crossed | Implemented |
| `setting_data_migrate` | A setting with `effect: data_migrate` **or `credential_rotate`** changed | Implemented |

**The name `setting_data_migrate` is narrower than its scope.** It also covers
`credential_rotate`: changing the password back in config.yml does not change the
password back inside the service (LDAP/DB), which is just as irreversible as a
data migration and needs a way back just as much. The name is left alone because
enumerated values are an external contract, and renaming one for wording would
require raising `api_version` — a cost out of proportion to the benefit.

When both hold, `module_upgrade_breaking` is recorded: only one snapshot is
created, and the more serious reason is taken.

`pre_restore` and `pre_apply` were added here: the first draft's enumeration had
neither, yet the body already required a snapshot before a restore and `apply`
had long created one before switching, leaving both with no `reason` to write.

---

## `anas snapshot list`

```
anas snapshot list [--json]
```

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/srv/anas",
  "keep_auto": 5,
  "snapshots": [
    {
      "id": "20260729T081504Z-4a1b2c3d",
      "kind": "auto",
      "pinned": false,
      "created_at": "2026-07-29T08:15:04Z",
      "reason": "module_upgrade_breaking",
      "label": "",
      "deployment_id": "20260728T131040Z-cd6fc061",
      "complete": true,
      "config_matches_current": false,
      "size_bytes": null,
      "modules": { "nextcloud": "30.0.1" },
      "healthy": true
    }
  ]
}
```

- `size_bytes` being `null` means btrfs qgroups are not enabled and no accounting
  is possible — **do not emit `0`**. **It is always `null` in phase one**:
  reading a qgroup goes through `btrfs qgroup show`, which like
  `btrfs subvolume show` / `list` is a tree-search ioctl requiring
  CAP_SYS_ADMIN, while the whole snapshot subsystem deliberately works without
  root (see why `checkBtrfsSubvolume` identifies a subvolume by inode 256).
  Pushing every command behind root for one size number is not a fair trade.
- `config_matches_current: false` means the current `config.yml` differs from the
  one this snapshot recorded.
- `complete: false` marks an interrupted product that cannot be used for restore.

## `anas snapshot show <id>`

Outputs every field of `snapshot.yml` plus runtime validation results.

`"config_matches_current": false` means the `config.yml` currently on disk
differs from the one the snapshot recorded. This **is not an anomaly** — a
pre-upgrade automatic snapshot is almost always in this state: it captured the
old data and the old deployment, while the user's on-disk config is already the
new version they are about to apply. Report it faithfully; do not hide it.

### Precondition: artifacts must carry the original configuration

A snapshot's `meta/config.yml` must be the configuration **matching that
snapshot's `deployment_id`**, not "the config.yml on disk at snapshot time". For
a steady-state `kind: manual` snapshot the two are the same, but for a
`kind: auto` pre-upgrade snapshot they necessarily differ:

```
t0  on-disk config = 30.0.1, active = deployment-A, data in the 30 format
t1  the user edits config.yml → 31.0.0        ← the on-disk config is already the new one
t2  apply detects breaking
t3  the auto snapshot is created: data in the 30 format, deployment/ = deployment-A
t4  deployment-B is rendered and activated
```

If `t3` copied the on-disk config, the result would be a 31.0.0 configuration
paired with 30-format data and deployment-A — restoring it lands exactly in the
broken state the snapshot was supposed to rescue you from.

And nowhere in the system stores the original text of the configuration matching
deployment-A:

- `saveAppliedConfig`
  ([config_state.go:70](https://github.com/anas-project/ANAS/blob/master/internal/runner/config_state.go))
  writes `state/config-applied.yml`, which holds only a **sha256 hash** per
  setting;
- Design document §9.1 states explicitly that a release does not keep the
  original configuration, only the redacted `resolved.redacted.yml`;
- `Settings` in `deployment.yml` is likewise only a fingerprint.

**This design therefore requires `apply` to write the original configuration text
to `deployments/<id>/config.source.yml` (0600).** That does not conflict with
§9.1 — what §9.1 restricts is using it as a **startup input** (avoiding config.yml
holding the dual identity of desired state and startup input); this is a
read-only copy **for restore**, distinguished in both purpose and name.

Nor is it a new mechanism: the legacy `release/` path already did it
(`copyFile(cfgPath, work/config.yml)`,
[runner.go:369](https://github.com/anas-project/ANAS/blob/master/internal/runner/runner.go)),
and only the deployment path failed to carry it forward — a measurement on the
server showed `deployments/<id>/` containing only `modules`, `deployment.yml`,
and `lock.yml`.

The sha256 fingerprints in `state/config-applied.yml` can **detect** that the
on-disk config differs from what was applied, but detection is not the ability to
**produce** that old configuration.

The file contains plaintext secrets (a user secret may be written directly into
config.yml), hence 0600, consistent with §3's "the deployment directory is
protected as sensitive data throughout".

**Without it, "restore the system from the snapshot alone" does not hold.**

## `anas snapshot create`

```
anas snapshot create [--label "…"] [--reason manual] [--json]
```

Creates a `kind: manual` snapshot. Output is the same as `show`.

## `anas snapshot pin` / `unpin`

```
anas snapshot pin <id> [--label "…"] [--json]
anas snapshot unpin <id> [--json]
```

## `anas snapshot delete`

```
anas snapshot delete <id> [--force] [-y] [--json]
```

A snapshot with `pinned: true` requires `--force`.

## `anas snapshot prune`

```
anas snapshot prune [--dry-run] [--keep N] [-y] [--json]
```

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "dry_run": true,
  "keep_auto": 5,
  "would_delete": [
    { "id": "20260720T…", "kind": "auto", "created_at": "…", "size_bytes": null }
  ],
  "retained": 5,
  "pinned_excluded": 2
}
```

Without `--dry-run` the field is named `deleted`. **Before running a retention
policy for the first time, the user must be able to see what it would delete**,
so `--dry-run` is not an optional feature.

## `anas snapshot verify`

```
anas snapshot verify [<id>] [--rebuild-index] [--json]
```

Checks whether the metadata still lines up with the actual subvolumes. Designed
to be called from cron: metadata present while the subvolume was deleted by hand
with `btrfs subvolume delete` is a situation that otherwise only surfaces at the
moment of rollback.

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": false,
  "checked": 7,
  "index_rebuilt": false,
  "problems": [
    { "id": "20260720T…", "code": "subvolume_missing", "message": "…" },
    { "id": "20260722T…", "code": "deployment_missing", "message": "…" }
  ]
}
```

### `problems[].code` enumeration

| Value | Meaning |
| --- | --- |
| `subvolume_missing` | `snapshot.yml` is present but the data subvolume is gone |
| `metadata_unreadable` | `snapshot.yml` is corrupt or its version is unsupported |
| `meta_incomplete` | One of config / lock / secrets / deployment-state is missing under `meta/` |
| `deployment_incomplete` | The snapshot's `deployment/` copy is missing or incomplete |
| `snapshot_incomplete` | `complete: false`, an interrupted product |
| `index_stale` | The derived index disagrees with an actual scan (fixable with `--rebuild-index`) |

## `anas snapshot restore <id>`

```
anas snapshot restore <id> -w <workspace> [--dry-run] [-y] [--json]
```

Restores the workspace to the point in time the snapshot represents: data,
artifacts, config, lock, secrets, and the necessary state all together. This is
the **only** command that rolls data back.

- **`-w` is mandatory and explicit.** `ANAS_WORKSPACE` is not accepted, and there
  is no inference from cwd.
- `--dry-run` reports the list of paths that would be overwritten, without
  touching disk; a non-dry-run requires `-y`.
- **All or nothing**: "restore only meta and keep the current data" is not
  allowed, because it would mismatch keys against data.
- Refuses to run when the snapshot has `complete: false`.
- Before restoring, the current state is captured in another snapshot with
  `kind: auto` and `reason: pre_restore` — the restore itself must be undoable
  too.
- `state/active.yml` is regenerated from the snapshot's `deployment_id` rather
  than taken from the snapshot copy.
- A restore **does not start services automatically**; it reports `next_steps`.

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/srv/anas",
  "restored_from": "20260729T081504Z-4a1b2c3d",
  "pre_restore_snapshot": "20260730T093012Z-7f8e9a0b",
  "restored": ["config", "lock", "secrets", "state", "deployment", "data"],
  "deployment_id": "20260728T131040Z-cd6fc061",
  "next_steps": ["anas start -w /srv/anas"]
}
```

## `anas snapshot path <id>`

Prints the snapshot's read-only data path and exits. A btrfs snapshot is a
readable directory in its own right, and a user who wants to retrieve one
accidentally deleted file should not be forced into a full rollback.

```json
{ "api_version": "anas.dev/cli/v1", "ok": true, "id": "…", "path": "/srv/anas/snapshots/…/data" }
```

## Phase two

`anas snapshot diff <id>` — compare a snapshot's module versions and
configuration against the current ones, so a user knows what will be lost before
rolling back. Valuable, but not a blocker for phase one.

## Coverage

A workspace has two independent btrfs subvolumes, and snapshots treat them
differently:

| Tree | Contents | Snapshot default | Restore default |
|---|---|---|---|
| `<workspace>/data` | Application state (databases, the AD store, certificates) | **Always included** | **Always restored** |
| `<workspace>/userdata` | Files the user stores themselves | **Not included** | **Not restored** |

The reason for the split is correctness, not tidiness: restore replaces `data/`
wholesale, and if user files lived inside it, **every deployment rollback would
delete files saved after the snapshot** — files that have nothing to do with the
deployment being rolled back.

`snapshot.yml` records whether each tree was captured in `coverage`, with a reason
when it was not:

```yaml
coverage:
    - tree: data
      path: /data/ws/data
      captured: true
    - tree: userdata
      path: /data/ws/userdata
      captured: false
      reason: excluded
```

`reason` takes `excluded` (an automatic snapshot, or a manual one without
`--include-userdata`), `not_a_subvolume` (userdata is not on btrfs and cannot be
snapshotted), or `missing` (the workspace has no such tree).

Without this record a snapshot would still be marked `complete` and a restore
would still report success while the largest tree in the workspace was untouched,
with nothing on disk to say so.

At the command level:

- `anas snapshot create [--include-userdata]` — excluded by default
- `anas snapshot restore <id> [--restore-userdata]` — not restored by default; an
  interactive terminal asks once more, and `-y` takes the default (do not
  restore), because it means "do not ask me" rather than "do the more drastic
  one"
- Automatic pre-apply snapshots **never include** userdata
- `anas backup create [--skip-userdata]` — **included by default**, the opposite
  of snapshots: a backup exists so that a dead disk can be recovered from, and
  user files are the one part a redeploy cannot reproduce
