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

For a new target, run `anas init <workspace> -y` before `backup restore`. Empty initialization creates the workspace skeleton without starting containers. After restoring into another workspace or cloning, frozen artifacts still record source paths but grant no authority over source containers. Review the target configuration, use a different `global.container_prefix` when retaining the source on the same Docker daemon, then run `anas apply -w <workspace>` to create a local deployment. Direct `start`, `stop`, `restart`, `apply --deployment`, and rollback to imported artifacts are rejected with instructions to apply the target configuration first.

## Temporary directories

`snapshot`, `send`, `send-file`, `copy`, and workspace snapshots exclude managed temporary contents and source leases under `.anas/temp/`. Backups use an explicit list of persistent data and artifacts; external temporary roots are excluded. Restored or cloned workspaces allocate new directories and verify the target filesystem, without acquiring mount or deletion authority over the source workspace. Resuming the original containers with `compose start` after a backup pause revalidates their existing leases rather than allocating new directories.

The target's first configuration apply retains the imported active record for diagnosis and removes its runtime authority. Plans exclude source Modules from the old deployment's stop/start scope. A missing source lease registry does not bypass this check. A snapshot restored within the same workspace retains local active authority and valid leases when its frozen paths still bind to that workspace.
