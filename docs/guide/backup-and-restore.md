# 备份与恢复

## 不要直接打包 workspace

`snapshots/` 可能共享 Btrfs extent，`.anas/` 又包含大量可重建的制品和缓存。直接使用 `tar` 或 `cp` 会展开快照、放大备份，并且无法保证运行中服务的一致性。

使用 ANAS 提供的备份命令：

```bash
anas backup capabilities --to <destination> -w /srv/anas
anas backup plan --to <destination> -w /srv/anas
anas backup create --to <destination> -w /srv/anas
anas backup verify --to <destination>
```

以当前 CLI 帮助和[备份 JSON 契约](/reference/contracts/backup)中的精确参数为准。

## 三种恢复工具

| 问题 | 工具 |
| --- | --- |
| 当前发布制品或配置失败 | `anas rollback` |
| 本机应用数据需要回到时间点 | `anas snapshot restore`；只有显式 `--restore-userdata` 才替换用户文件 |
| workspace 丢失或迁移到另一台主机 | `anas backup restore` |

`rollback`、`snapshot restore` 和 `backup restore` 必须使用显式 `-w`。备份默认包含 `userdata/`；只有明确只备份部署状态时才使用 `backup create --skip-userdata`。恢复前先验证备份，再确认目标路径、空闲空间和文件系统能力。详细流程见[完整任务指南](usage.md)。

新目标先执行 `anas init <workspace> -y`，再执行 `backup restore`；空初始化只建立 workspace 骨架，不启动容器。恢复到另一 workspace 或克隆后，冻结制品仍记录源路径，但不授予源容器的运行权限。先检查目标配置，在同一 Docker 中保留源实例时使用不同的 `global.container_prefix`，然后执行 `anas apply -w <workspace>`，生成目标的新 deployment。直接 `start`、`stop`、`restart`、`apply --deployment` 或回滚到外来制品会被拒绝并提示先配置 apply。

## 临时目录

`snapshot`、`send`、`send-file`、`copy` 和 workspace 快照均排除受管临时内容以及 `.anas/temp/` 源租约。备份使用明确的持久数据与制品清单；外部临时根不加入备份。恢复或克隆后的启动重新分配目录并核验目标文件系统，不能取得源 workspace 的挂载或清理权限。备份暂停后用 `compose start` 复启原容器时，仅复核有效原租约，不分配新目录。

目标首次配置 apply 会将外来的 active 记录保留为诊断文件，并解除其运行授权；计划不把源 Module 纳入旧部署停启范围。缺少源租约也不会跳过这一检查。同 workspace 快照恢复且冻结路径仍绑定本 workspace 时，保留本地 active 授权和有效租约。
