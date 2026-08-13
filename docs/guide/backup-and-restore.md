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
