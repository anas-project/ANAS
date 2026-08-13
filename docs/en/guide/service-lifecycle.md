# Service lifecycle

## Deploy

`apply` is the normal deployment entry point. Every successful apply creates a new immutable deployment before switching the active state:

```bash
anas apply -w /srv/anas
anas status -w /srv/anas
```

Use `--build --update-lock` for the first deployment or when intentionally updating locked decisions.

## Normal operation

```bash
anas start -w /srv/anas
anas stop -w /srv/anas
anas restart -w /srv/anas
anas deployments list -w /srv/anas
anas deployments inspect <id> -w /srv/anas
```

For named modules, the runner expands prerequisites or dependants and processes the complete chain in dependency-safe order.

## Roll back

A deployment rollback fixes a bad artifact or configuration. A snapshot restore fixes persistent data that has already changed:

```bash
anas rollback <deployment-id> -w /srv/anas
anas snapshot restore <snapshot-id> -w /srv/anas
```

Replacement operations require an explicit `-w` to reduce the risk of targeting the wrong workspace. Read [backup and restore](backup-and-restore.md) first.
