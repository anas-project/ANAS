# Backup and restore

## Do not archive the workspace directly

Snapshots may share Btrfs extents, while `.anas/` contains rebuildable artifacts and caches. A direct `tar` or `cp` expands snapshots, increases backup size, and cannot ensure application consistency while services run.

Use the ANAS backup commands:

```bash
anas backup capabilities --to <destination> -w /srv/anas
anas backup plan --to <destination> -w /srv/anas
anas backup create --to <destination> -w /srv/anas
anas backup verify --to <destination>
```

Use current CLI help as the authority for exact arguments.

## Three recovery tools

| Failure | Tool |
| --- | --- |
| Active artifact or configuration failed | `anas rollback` |
| Local application data must return to a point in time | `anas snapshot restore`; only `--restore-userdata` also replaces user files |
| Workspace was lost or moves to another host | `anas backup restore` |

`rollback`, `snapshot restore`, and `backup restore` require explicit `-w`. Backups include `userdata/` by default; use `backup create --skip-userdata` only for an intentional deployment-only backup. Verify the backup and check the target path, free space, and filesystem capabilities first. See the [complete task guide](usage.md) for details.
