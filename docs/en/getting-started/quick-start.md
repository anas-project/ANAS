# First deployment

## 1. Create a workspace

```bash
anas init /srv/anas
```

`init` is the only command that creates a workspace. It generates `config.yml`, `data/`, `userdata/`, `snapshots/`, and the protected `.anas/` runtime directory. On Btrfs, `data/` and `userdata/` are separate subvolumes: data recovery replaces `data/` by default, while backups protect user files separately.

## 2. Edit the minimum configuration

Edit `/srv/anas/config.yml`, select the required modules, and set at least the domain, administrator email, timezone, and required credentials. Use the repository's [`config.example.yml`](https://github.com/anas-project/ANAS/blob/master/config.example.yml) as the starting point:

```yaml
modules:
  traefik: {}
  lego:
    config:
      dns_provider: cloudflare

global:
  base_domain: nas.example.com
  email: admin@example.com
  timezone: Asia/Singapore
  default_service_root_password: replace-with-a-strong-password

secrets:
  cloudflare_dns_api_token: replace-me
```

For networks in mainland China, enable the unified mirror switch:

```yaml
global:
  chinese_speedup: true
```

Never commit real passwords or API tokens.

## 3. Plan and apply

For the first deployment:

```bash
anas plan -c /srv/anas/config.yml
anas apply --update-lock -w /srv/anas
```

Published deployments pull fixed images directly. `--update-lock` freezes module versions, capability bindings, and the snapshot policy. Only source builders enable `global.chinese_build_speedup` and add `--build`. A normal later configuration change usually needs only:

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
