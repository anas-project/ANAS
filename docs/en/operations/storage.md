# Storage

## The workspace is the management boundary

Configuration, data, snapshots, and required runtime state belong to one workspace. To use a larger disk, move or mount the whole workspace instead of giving one module an external data path.

```text
<workspace>/
  config.yml
  config.lock.yml
  data/
  userdata/
  snapshots/
  .anas/
```

Btrfs enables local copy-on-write snapshots. `data/` contains application state and `userdata/` contains user files. An ordinary data snapshot rewinds only the former; backups include both by default. Other filesystems can run ANAS, but snapshot support is reduced or disabled. The resolved policy is written to the lock file rather than changing on each ordinary apply.

Before deployment, upgrade, or restore, check workspace capacity, Docker image and build-cache usage, Btrfs data/metadata usage, and real free space at the backup destination.
