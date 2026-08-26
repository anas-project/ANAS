# 运维指南

本节面向维护运行中 ANAS 主机的管理员。

> **测试目标授权：** 默认使用独立的非生产环境。用户或操作者为本次测试明确指定准确服务器时，
> 可以在该服务器运行 E2E、回归或实验性部署，包括承载正式服务的主机。明确指定只授权目标，
> 不豁免测试专用 Docker daemon、workspace、网络、端口和定向清理等隔离要求。

- [存储](storage.md)：workspace、Btrfs、容量和挂载边界。
- [网络](networking.md)：域名、Traefik、macvlan 和防火墙。
- [故障排查](troubleshooting.md)：从 ANAS 状态到容器日志的排查顺序。
- [Samba](samba.md)和[Traefik](traefik.md)：服务专项说明。
- Runbook：需要谨慎执行的主机级操作步骤，包括
  [挂载与格式化](runbooks/mount.md)、[特权 helper](runbooks/privileged-helper.md)和
  [`samba-tool` 用户与组管理](runbooks/samba-tool-user-management.md)。

测试服务器地址、SSH 命令和阶段性回归报告不属于公开操作指南，应保存在受控的 Issue、CI artifact 或外部私有系统中。
