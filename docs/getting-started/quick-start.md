# 首次部署

## 1. 创建 workspace

```bash
anas init /srv/anas
```

`init` 是唯一会创建 workspace 的命令。它会生成 `config.yml`、`data/`、`userdata/`、`snapshots/` 和受保护的 `.anas/` 运行目录。在 Btrfs 上，`data/` 与 `userdata/` 会创建为独立 subvolume：数据恢复默认只替换 `data/`，用户文件由备份单独保护。

## 2. 编辑最小配置

编辑 `/srv/anas/config.yml`，选择需要的 Module，并至少设置域名、管理员邮箱、时区和必要凭据。配置结构以仓库中的 [`config.example.yml`](https://github.com/anas-project/ANAS/blob/master/config.example.yml) 为起点：

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

中国大陆网络环境可以启用统一镜像开关：

```yaml
global:
  chinese_speedup: true
```

不要把真实密码或 API token 提交到 Git。

## 3. 规划并应用

首次部署执行：

```bash
anas plan -c /srv/anas/config.yml
anas apply --update-lock -w /srv/anas
```

正式发布用户直接拉取固定镜像；`--update-lock` 固化 Module 版本、能力绑定和快照策略。
只有源码构建者才启用 `global.chinese_build_speedup` 并添加 `--build`。后续普通配置修改通常只需要：

```bash
anas apply -w /srv/anas
```

## 4. 验证

```bash
anas status -w /srv/anas
anas deployments list -w /srv/anas
```

部署失败时不要直接修改 `.anas/`。先查看命令错误和容器日志，再参考[故障排查](/operations/troubleshooting)。

## 下一步

- [配置指南](/guide/configuration)
- [服务生命周期](/guide/service-lifecycle)
- [备份与恢复](/guide/backup-and-restore)
- [完整任务指南](/guide/usage)
