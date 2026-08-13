# Using anas

*[中文版本](/guide/usage)*

This is the task-oriented guide: how to set a deployment up, run it, change it,
and get it back when something goes wrong. For the exact JSON and exit codes
each command produces, see the [CLI JSON contracts](/reference/contracts/).

---

## 1. The workspace

Everything one deployment owns lives in a single directory:

```text
<workspace>/
  config.yml          the desired state — the only file you edit
  config.lock.yml     resolved module versions, bindings, and snapshot policy
  data/               service data
  userdata/           user files; deployment rollback never changes them
  snapshots/          point-in-time copies
  .anas/              runtime state (0700; nothing in here is edited by hand)
```

**Nothing outside this directory is needed to restore the deployment.** That is
the reason for the layout, and it is why `data/` has no configurable location:
a configurable one would make self-containment conditional on nobody having
moved it. If the data belongs on a larger disk, put the whole workspace there.

Self-contained is not the same as "tar it". **Use `anas backup`** (§7) rather
than copying the directory:

- `snapshots/` duplicates `data/`. On Btrfs the copies are extent-shared and
  nearly free on disk, but `cp` and `tar` read files and write them out, so
  five snapshots of a 1.3 GB dataset materialise as roughly 6.5 GB of archive —
  for data that is already there once.
- `.anas/` is mostly rebuildable. On a live deployment measured at 402 MB, the
  part that cannot be reconstructed is **52 KB** (`state/` and
  `secrets.generated.yml`); the active artifact adds 42 MB, and the remaining
  ~360 MB is historical deployments and build caches.
- A copy taken while services are running is crash-consistent at best.
  `anas backup` stops them for the moment the snapshot is taken.

`anas backup` already knows which of those to include. The single-directory
property is what makes that possible — it is not an instruction to archive the
directory by hand.

### How commands find the workspace

In order: the `-w` flag, then `$ANAS_WORKSPACE`, then the current directory —
but only when that directory already contains `.anas/`.

No command creates a workspace implicitly. A mistyped `cd` must not silently
grow a second parallel deployment, so `anas init` is the only thing that makes
one.

**`rollback`, `snapshot restore` and `backup restore` accept only `-w`.** They
are the operations that replace things, and an environment variable left in a
shell profile is the easiest way to point one at the wrong deployment.

---

## 2. First run

```bash
anas init /srv/anas
```

Creates the layout above and writes a skeleton `config.yml`. On Btrfs it makes
`data/` and `userdata/` separate subvolumes. Ordinary snapshots include only
`data/`; backups include `userdata/` by default. Off Btrfs it says which
capabilities will be unavailable and asks before continuing.

`init` prints an `export ANAS_WORKSPACE=…` line but does **not** write it
anywhere. To have it written to your shell profile, ask:

```bash
anas init /srv/anas --shell-init write     # idempotent, marked block
anas init /srv/anas --shell-init remove    # take it back out
```

It is opt-in because `ANAS_WORKSPACE` is machine-global: once it is in a
profile it wins from every directory, which reintroduces the "right command,
wrong deployment" mistake that directory-based resolution avoids. It also has
no effect on cron or systemd units.

### Where the module definitions come from

The modules are **part of the program, not part of the deployment** — the same
category as the `anas` binary itself, and deliberately not copied into each
workspace. Install them beside it:

```text
/opt/anas/
  bin/anas
  modules/…
```

With that layout nothing needs configuring: `anas` finds the modules next to its
own executable, from any working directory. Running from a source checkout works
for the same reason.

If the binary lives somewhere else — `/usr/local/bin/anas` with the modules
elsewhere — point at them once:

```bash
export ANAS_MODULE_ROOT=/opt/anas/modules
```

The variable wants the bundle directory itself, not its parent. The `--root`
and `--module-root` flags accept either and append `modules` when it is there;
the environment variable does not, so a value that worked as a flag can fail as
an export.

Only some commands need them at all:

| Needs the modules | Does not |
| --- | --- |
| `plan` `lock` `render` `build` `apply` | `init` `status` `start` `stop` `restart` |
| `config set` `config explain` `config plan` | `deployments` `rollback` `snapshot *` `backup *` |

The split is **changing things versus running things**. A rendered deployment
carries everything it needs to start, which is why a workspace restored onto a
bare machine comes up with no modules anywhere in sight. Rendering a *new* one is
what needs the definitions.

Missing, you get `could not locate module bundle directory`. Set
`ANAS_MODULE_ROOT` persistently, or use `--root` or `--module-root` for one
invocation.

### Bring it up

Edit `<workspace>/config.yml`, then:

```bash
anas apply --build --update-lock -w /srv/anas
```

`--build` builds the images; `--update-lock` writes the resolved module versions,
capability bindings, and host snapshot policy into `config.lock.yml`. After the
first run neither is needed unless one of those decisions should change.

### Mainland China mirrors

One structured global switch enables the complete mirror set:

```yaml
global:
  chinese_speedup: true
```

It covers Docker Hub, GHCR, Quay, APT, Alpine APK, npm, Go modules, GitHub
release downloads, and the Nextcloud App Store. Every generated endpoint can
still be overridden under the same `env` map. See the
[mainland mirror and CNB distribution design](/research/china-mainland-mirrors-and-cnb-distribution-2026-08-11)
for the endpoint table and production publishing model.

When invoking `go run ./cmd/anas` for the first time instead of using a
prebuilt binary, Go has to compile ANAS before ANAS can read its config. Set
`go env -w GOPROXY=https://goproxy.cn,direct` once on that host. Subsequent
module-hook builds inherit the proxy generated by the switch.

---

## 3. Everyday operation

```bash
anas status                 # what is active, and whether it verified
anas start
anas stop
anas restart
anas deployments list
anas deployments inspect <id>
```

All of these take `-w`, or run from inside the workspace, and none of them needs
the module tree — they read the deployment that was already rendered.

---

## 4. Changing the configuration

Edit `config.yml` directly, or use the `config` commands — these read the module
definitions, so `ANAS_MODULE_ROOT` has to be set (see §2):

```bash
anas config set global.timezone Europe/Berlin -w /srv/anas
anas config explain nextcloud.domain_prefix        # what changing it costs
anas config plan -w /srv/anas                      # what the pending edits would do
```

Then apply:

```bash
anas apply -w /srv/anas
```

Each apply produces a **new immutable deployment**; the previous one stays on
disk, which is what makes rollback possible.

Some settings cannot be applied by an ordinary restart because they change
state *inside* a service — a password that lives in the LDAP directory, a
database that has to be migrated. `apply` refuses those with exit 4 and names
the setting. `--allow-risky` proceeds once you have arranged the migration.

---

## 5. When something breaks

This is the one distinction worth learning properly.

| Situation | Command | What happens to data |
| --- | --- | --- |
| A config change broke the service; the data is fine | `anas rollback` | **untouched** |
| The data itself is wrong — a bad upgrade, a bad migration | `anas snapshot restore <id>` | **rewound** |

```bash
anas rollback -w /srv/anas              # back to the previous deployment
anas rollback <deployment-id> -w /srv/anas
```

`rollback` switches the artifact and **never touches data**. That matters
because the common case is "I broke the config" — answering it by rewinding
data would throw away everything written since the last apply.

Rewinding data is only ever `snapshot restore`, which puts config, lock,
secrets, state, artifact and data back to one point in time, together.

### Rollbacks that are refused

A rollback across a module version that rewrote the on-disk format cannot work:
the older code cannot read the newer data. `anas` refuses it outright, with no
`--allow-risky` override, and points at the snapshot to restore instead.

A version change where the module says nothing about its data format is treated
as unknown and blocked, overridable with `--allow-risky`.

---

## 6. Snapshots

A snapshot is **local, instant, and exists so a change can be undone**.
Requires Btrfs.

```bash
anas snapshot create --label "before the upgrade"
anas snapshot list
anas snapshot show <id>
anas snapshot path <id>          # print the read-only data path, to fish one file out
anas snapshot restore <id> -w /srv/anas
anas snapshot verify             # do the recorded snapshots still exist?
```

`verify` is worth running from cron. The usual way a backup fails is that
somebody deleted the underlying subvolume by hand and nothing noticed until the
day it was needed.

### Automatic snapshots

If `rollback.snapshot.backend` is omitted, `anas lock` checks the workspace. A
Btrfs workspace whose `data/` is a subvolume locks `backend: btrfs` and
`keep_auto: 5`; any other workspace locks `backend: none`. That decision stays
fixed until the next explicit lock update.

`apply` takes one by itself before a change that cannot be undone:

- a module upgrade that crosses a version the module declared as rewriting data
- a setting whose effect is `data_migrate` or `credential_rotate`

Routine applies do **not** snapshot. Doing so would fill the retention slots
with ordinary config edits and evict the one that mattered.

```bash
anas apply --snapshot        # force one anyway
anas apply --no-snapshot -y  # skip it; -y required, this discards the only way back
```

### Retention

The locked `keep_auto` default is 5. An explicit
`rollback.snapshot.keep_auto` overrides it on the next lock update. Manual and
pinned snapshots are neither counted nor collected.

```bash
anas snapshot pin <id>
anas snapshot prune --dry-run    # always look first
anas snapshot prune --keep 3
```

---

## 7. Backup and disaster recovery

A backup is **a snapshot sent somewhere else**, so it survives the disk.

Ask what this host can actually do before planning around it:

```bash
anas backup capabilities --to /mnt/backup
```

It reports each of the four modes and, for the ones that cannot run, an
enumerated reason — the remedy for "wrong filesystem" is not the remedy for
"cannot read the data".

| Mode | Needs | Notes |
| --- | --- | --- |
| `snapshot` | destination on the same Btrfs filesystem | fastest, but the copy is on the disk you are protecting against — never recommended |
| `send` | both sides Btrfs, **root** | supports incremental with `--parent` |
| `send-file` | source Btrfs, **root** | a stream file; restores only onto Btrfs |
| `copy` | destination writable, **all data readable** | works anywhere; the only mode off Btrfs |

```bash
anas backup plan --to /mnt/backup          # what it would do, without doing it
anas backup create --to /mnt/backup
anas backup list   --to /mnt/backup
anas backup verify --to /mnt/backup        # cron this too
```

Containers stop only while the snapshot is taken, then start again before the
transfer runs. Downtime is therefore set by how long a snapshot takes — seconds
— not by how much data there is. If a backup fails part way, the services are
started again; that compensation also survives the process being killed.

### Restoring onto a fresh machine

```bash
anas init /srv/anas                 # the workspace must exist first
anas backup restore --from /mnt/backup -w /srv/anas
anas start -w /srv/anas
```

The restored workspace carries its own artifact, so it starts without needing
the module source tree. The target host must still be able to obtain the runtime
images referenced by the frozen Compose files unless they are already local.

---

## 8. What needs root, and why

`anas` runs as an ordinary user. Three operations do not, and it says so rather
than escalating by itself:

| Operation | Why |
| --- | --- |
| Reclaiming a snapshot (`delete`, `prune`) | `btrfs subvolume delete` needs `CAP_SYS_ADMIN` unless the filesystem was mounted `user_subvol_rm_allowed` |
| `send` / `send-file` backups | `btrfs send` needs root |
| `copy` backups of a deployment that has run | containers write their data as root; an unprivileged reader cannot read it |

The root cause of the first and third is the same: **container data is
root-owned**, so reclaiming or reading a copy of it needs privilege whether or
not snapshots are involved.

The practical answer is a systemd timer running the scheduled operations as
root — they were going to be scheduled anyway:

```ini
# anas-prune.service / anas-backup.service, driven by matching .timer units
ExecStart=/usr/local/bin/anas snapshot prune -w /srv/anas -y
ExecStart=/usr/local/bin/anas backup create -w /srv/anas --to /mnt/backup -y
```

Mounting with `user_subvol_rm_allowed` also works, but it applies to the whole
filesystem and cannot be narrowed to one subvolume, so it is documented as an
option rather than a requirement.

---

## 9. Without Btrfs

Everything works except the snapshot-dependent parts:

- no `snapshot` commands, and no automatic pre-upgrade snapshot
- no `rollback` protection from data restore — `rollback` itself still works
- backups run in `copy` mode only, which needs privilege once services have run

`anas init` says this at the point where you can still choose a different disk.

---

## 10. Scripting

Every command accepts `--json`:

- exactly one JSON document on stdout, parseable without filtering
- progress, warnings and logs on stderr as JSON Lines
- an exit code from a fixed table

| Code | Meaning |
| --- | --- |
| 0 | success |
| 1 | the work started and failed |
| 2 | the command line was wrong |
| 3 | a confirmation is required and cannot be obtained |
| 4 | the machine is not in a state where this can run |

`code` and `reason` fields are fixed enumerations. `message` is for humans —
do not parse it. Without `--json` the output is prose and is not a contract.

Non-interactive callers should pass `-y` where a confirmation would be
required; without it, and with no terminal, a command fails immediately at exit
3 rather than blocking on input nobody is there to give.

Full details are in the [CLI JSON contracts](/reference/contracts/).

---

## 11. Common messages

**`… is not a workspace: no .anas/ directory`** — you are outside a workspace.
Pass `-w`, or `cd` into one, or `anas init`.

**`could not locate module bundle directory`** — the command needs the module
definitions, and `ANAS_MODULE_ROOT` must name `modules` itself rather than the
directory above it. Set
`ANAS_MODULE_ROOT`; see §2.

**`anas rollback requires an explicit -w`** — by design; see §1.

**`no backup mode can run against …`** — run `anas backup capabilities --to …`,
which names the reason for each mode.

**`cannot delete Btrfs subvolume …: needs CAP_SYS_ADMIN`** — snapshot
reclamation; see §8.

**`crosses data-breaking version …`** — the rollback you asked for cannot work;
restore a snapshot instead. See §5.
