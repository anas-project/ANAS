# 故障排查

## 先确定故障层级

按下面顺序收集证据，避免一开始就修改持久状态：

```bash
anas status -w /srv/anas
anas deployments list -w /srv/anas
docker compose version
docker ps --format 'table {{.Names}}\t{{.Status}}'
```

然后查看失败命令的 stderr、对应容器的健康状态和日志。不要手工编辑 `.anas/state`、冻结 deployment 或生成的 Secret。

## 常见分界

| 现象 | 优先检查 |
| --- | --- |
| 找不到 Module bundle | `ANAS_MODULE_ROOT`、安装布局、`--module-root` |
| `apply` 前置条件失败 | 配置、锁文件、权限、磁盘与文件系统能力 |
| 容器启动失败 | deployment 中的 Compose 配置、镜像、容器日志 |
| HTTPS 无法访问 | DNS、Traefik router、证书、端口与防火墙 |
| 登录或目录服务异常 | 时间同步、Samba/IAM 健康状态、协议绑定 |
| 升级后应用数据异常 | 停止继续写入，判断使用 deployment rollback 还是 snapshot restore |

## 不要混淆两类回退

- 代码、镜像或配置制品有问题：使用 `anas rollback`。
- 数据库或用户数据已经发生错误迁移：使用 `anas snapshot restore`。

恢复前先阅读[备份与恢复](/guide/backup-and-restore)，并显式指定 `-w`。
