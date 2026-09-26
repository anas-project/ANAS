# Service lifecycle

## Deploy

`apply` is the normal deployment entry point. Every successful apply creates a new immutable deployment before switching the active state:

```bash
anas module update -w /srv/anas  # first deployment or an intentional Module release update
anas apply -w /srv/anas
anas status -w /srv/anas
```

`module update` resolves remote Modules, capability bindings, and host policy into the lock. Ordinary
`apply` uses that lock and never upgrades a Module. Only source development with a local Module
override normally adds `--build --update-lock`.

## Normal operation

```bash
anas start -w /srv/anas
anas stop -w /srv/anas
anas restart -w /srv/anas
anas deployments list -w /srv/anas
anas deployments inspect <id> -w /srv/anas
```

For named modules, the runner expands prerequisites or dependants and processes the complete chain in dependency-safe order.

A Module declaring `before_stop` runs its original deployment's cleanup Hook before containers or networks
are removed. Forgejo uses it to stop the original Actions controller and wait for workload and registration
cleanup while the API and compute connection remain available. The cleanup Hook receives only the old private
deployment projection, not the candidate deployment's newer Secret Store credentials. Unconfirmed cleanup blocks dependency removal
and cannot be bypassed by an automatic apply-recovery restart. Backups retain a pending-cleanup transaction
and suspend automatic recovery in this case; inspect the failed module and residual resources rather than
deleting the marker or rewriting state to turn a failed operation into a successful one.
Backup cleanup begins only after both the journal data and its published directory entry have been synced.
A storage sync failure blocks further execution rather than treating an undurable record as recovery authority.

The full administration console can also run `start`, `stop`, and `restart`. After a named Module is selected,
the browser requests a server preview and displays the complete ordered chain expanded by the Runner from the
active deployment. A durable job is created only after that exact chain is confirmed. An empty selection means
the whole active deployment. If the deployment, digest, or dependency graph changes before confirmation, the
server rejects the stale preview. Runtime and health shown by the console come from a live Compose probe rather
than deployment state files.

A running lifecycle job accepts cancellation only during a server-declared safe stage. Cancellation terminates
the whole external process group and runs a compensation check after the terminal job state. A job that has
already entered an unsafe stage rejects cancellation instead of pretending that execution stopped.

## Module management

The full administration console combines four kinds of Module state: selection in desired configuration, the
installed release in the immutable Module view, the release and entry points frozen in the active deployment,
and live Compose runtime, health, and container counts. Entry points are public HTTP(S) addresses frozen while
the deployment is materialized; the page does not re-derive them from current configuration or expose host paths.

Enable and disable use the strong configuration ETag and update desired configuration only; they never apply
implicitly. A stale action is rejected if configuration changes before its durable job executes. Catalog update
and lock-based sync are also durable, idempotent, per-workspace serialized jobs. Generate a plan and explicitly
confirm apply after an update to change the runtime. The corresponding CLI workflow remains available:

```bash
anas module list -w /srv/anas
anas module update -w /srv/anas
anas module sync -w /srv/anas
```

## Roll back

A deployment rollback fixes a bad artifact or configuration. A snapshot restore fixes persistent data that has already changed:

```bash
anas rollback <deployment-id> -w /srv/anas
anas snapshot restore <snapshot-id> -w /srv/anas
```

Replacement operations require an explicit `-w` to reduce the risk of targeting the wrong workspace. Read [backup and restore](backup-and-restore.md) first.
