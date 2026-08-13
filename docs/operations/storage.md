# 存储

## Workspace 是管理边界

配置、数据、快照和必要运行状态都属于同一个 workspace。要把数据放到大容量磁盘，应移动或挂载整个 workspace，而不是为单个 Module 设置站外数据路径。

```text
<workspace>/
  config.yml
  config.lock.yml
  data/
  userdata/
  snapshots/
  .anas/
```

## 文件系统

Btrfs 提供本地写时复制快照。`data/` 保存应用状态，`userdata/` 保存用户文件；普通数据快照只回退前者，备份默认保护两者。其他文件系统可以运行 ANAS，但快照能力会降级或关闭；最终选择会写入锁文件，普通 `apply` 不会反复探测并改变策略。

格式化磁盘或修改挂载表属于高风险主机操作。执行前确认设备名、备份和恢复路径；参考[挂载与格式化 Runbook](runbooks/mount.md)，不要直接复制未经核对的命令。

## 容量检查

部署、升级和恢复前同时检查：

- workspace 所在文件系统的可用空间；
- Docker 镜像与构建缓存占用；
- Btrfs data/metadata 使用量；
- 备份目标的真实可用空间。

ANAS 备份会选择必要状态，不要用全目录复制代替。[备份与恢复](/guide/backup-and-restore)解释了原因。
