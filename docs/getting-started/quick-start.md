# 首次部署

## 1. 准备外部配置

在 workspace 外创建 `anas.yml`，选择需要的 Module，并至少设置域名、管理员邮箱、时区和必要凭据。配置结构以仓库中的 [`config.example.yml`](https://github.com/anas-project/ANAS/blob/master/config.example.yml) 为起点：

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

不要把真实密码或 API token 提交到 Git。

## 2. 初始化并导入

```bash
anas init /srv/anas --config ./anas.yml
```

`init` 创建 workspace，并把外部配置规范化写入受管的 `/srv/anas/config.yml`；外部
`anas.yml` 不会被修改。当 `module_source: cn` 且没有声明 `global.chinese_speedup` 时，
受管配置自动写入：

```yaml
module_source: official-cn
global:
  chinese_speedup: true
```

这会在容器环境中生成 `CHINESE_SPEEDUP=true`。显式写
`global.chinese_speedup: false` 会保留，不会被默认值覆盖。若 workspace 已经初始化，
使用 `anas config import ./anas.yml -w /srv/anas` 更新，不要直接编辑受管配置。

`init` 还会创建 `data/`、`userdata/`、`snapshots/` 和受保护的 `.anas/` 运行目录。在
Btrfs 上，`data/` 与 `userdata/` 会创建为独立 subvolume。

## 3. 规划并应用

首次部署执行：

```bash
anas module update -w /srv/anas
anas plan -w /srv/anas
anas apply -w /srv/anas
```

`module update` 从配置的 source 解析 Module release，把 OCI/content digest、能力绑定和快照
策略写入 lock，并建立 workspace Module 视图。正式发布用户直接拉取固定镜像；只有源码
构建者才启用 `global.chinese_build_speedup`、使用本地 `--module-root` 并添加 `--build`。
后续普通配置修改通常只需要：

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
