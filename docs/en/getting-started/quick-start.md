# First deployment

## 1. Prepare an external configuration

Create `anas.yml` outside the workspace, select the required modules, and set at least the domain,
administrator email, timezone, and credentials. Use the repository's
[`config.example.yml`](https://github.com/anas-project/ANAS/blob/master/config.example.yml) as a starting point:

```yaml
module_source: cn

modules:
  traefik: {}
  lego:
    config:
      dns_provider: cloudflare

global:
  base_domain: nas.example.com
  email: admin@example.com
  timezone: Asia/Singapore

secrets:
  cloudflare_dns_api_token: replace-me
```

Never commit real passwords or API tokens.

## 2. Initialize and import

```bash
anas init /srv/anas --config ./anas.yml
```

`init` creates the workspace and writes a normalized copy to the managed
`/srv/anas/config.yml`; it never modifies the external `anas.yml`. When `module_source: cn` is set
and `global.chinese_speedup` is absent, the managed configuration contains:

```yaml
module_source: official-cn
global:
  chinese_speedup: true
```

This renders as `CHINESE_SPEEDUP=true` in the container environment. An explicit
`global.chinese_speedup: false` is preserved. For an existing workspace, update it with
`anas config import ./anas.yml -w /srv/anas` instead of editing the managed file.

`init` also creates `data/`, `userdata/`, `snapshots/`, and the protected `.anas/` runtime directory.
On Btrfs, `data/` and `userdata/` are separate subvolumes.

## 3. Plan and apply

For the first deployment:

```bash
anas module update -w /srv/anas
anas plan -w /srv/anas
anas apply -w /srv/anas
```

`module update` resolves releases from the configured source, records OCI/content digests,
capability bindings, and snapshot policy in the lock, and creates the workspace Module view.
Published deployments pull fixed images directly. Only source builders enable
`global.chinese_build_speedup`, use a local `--module-root`, and add `--build`. A normal later
configuration change usually needs only:

```bash
anas apply -w /srv/anas
```

## 4. Verify

```bash
anas status -w /srv/anas
anas deployments list -w /srv/anas
```

Do not edit `.anas/` after a failure. Read the command error and container logs, then use the [troubleshooting guide](/en/operations/troubleshooting).

## Next steps

- [Configuration](/en/guide/configuration)
- [Service lifecycle](/en/guide/service-lifecycle)
- [Backup and restore](/en/guide/backup-and-restore)
- [Complete task guide](/en/guide/usage)
