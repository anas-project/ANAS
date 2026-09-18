---
status: current
---

# Incus 发行版 daemon 供给探查与磁盘配额缺口

基线：2026-09-11，ANAS HEAD `f7642c5` 加指定工作树未提交实现。此记录只描述已观察结果；
后续代码先行、服务器 E2E 待办见[测试清单](../../test-env/fixtures/incus-network-prototype/e2e-plan.md)。

## 环境与范围

操作者本轮明确指定独立 SSH 宿主。Ubuntu 26.04 / Linux 7.0.0-30-generic / x86_64，未发现
`/dev/kvm`。从已配置的发行版源下载并解包 Incus 6.0.5-8、liblxc 6.0.6-1 等包至独立临时目录，
没有安装到系统或新增软件源。Daemon 使用独立 state、证书、私有 network/mount namespace 与
临时 systemd unit，不使用默认 Docker 或其他测试 daemon。这不是宿主安装或 M2 helper 验收。
源码兼容核验的 Incus 7.3.0 与此实机版本不同，结果不能直接外推。

## 已观察结果

| 检查 | 结果与边界 |
| --- | --- |
| 实际 Provider ensure，镜像未导入 | 创建 restricted project、default project 内每租约 bridge 和租约 profile；随后拒绝缺失镜像。未独立核验此时 trust 数量，不补写为已通过证书负例 |
| 导入固定 BusyBox 测试镜像后再次 ensure | 返回 exists/ready/restricted/quota_enforced=true；项目四项 limits 与精确 bridge 授权已读回。镜像是临时夹具，不是产品发布产物 |
| 非特权 guest init | 实例记录创建成功；dir 驱动明确警告底层不支持 quota 并跳过设置 |
| guest start | 未成功，返回 Error status；strace 定位 liblxc 创建 /run/lxc 锁目录遇到 EROFS，这是实验私有运行目录配置问题，尚未验证修复 |
| VM | 缺少 KVM，未执行 |
| Traefik、Docker/Incus 规则共存、实际磁盘写满 | 未执行 |
| 本轮资源清理与全局规则对比 | 已停止 daemon（MainPID=0），核验无测试 cgroup 进程、挂载或 loop；精确卸载遗留测试 dnsmasq AppArmor profile 后确认无残留，删除本轮临时目录并 reset 对应 transient unit。停止后的宿主 nft 无状态摘要与基线相同 |

临时测试镜像的 SHA-256 为
`3a9d1317c9870e85e7f5b90e8f1a775d3713bcc3f8fbf30d038bf3f358fd9f2a`，只标识此次夹具。
不保存连接地址、证书、私钥、含环境变量的 provider 输入或完整 daemon 日志。

## 已定位代码缺口与修复边界

原 `quotaEnforced` 仅检查四项 project limits 非空，因此在 dir 根磁盘没有实际 quota 时返回真。
[Incus dir 文档](https://linuxcontainers.org/incus/docs/main/reference/storage_dir/#quotas)明确其配额依赖
底层 ext4/XFS project quota。v7.3.0 的 `driver_dir_utils.go` 中不支持路径也会只警告并返回成功。

本轮修复在 ensure 副作用前读取池名称、状态和驱动，仅准入 Created 的 btrfs/zfs，并在读回时复核；
inspect 对不支持/不存在/未就绪池不再报告 quota/ready。btrfs/zfs 的原生卷限额路径具有明确错误
返回，但驱动准入仍不是宿主性能、磁盘写满或完整生命周期验收。其他驱动须另补能力证据。
本地回归覆盖拒绝前无写入、既有信任不变、池漂移、读取故障及两档正例；服务器回归按测试清单后置。
Module 保持 developing，M6/M11 未完成，不提交、不合并。

本轮代码变更的 `go vet ./...`、`go test ./...`、共享构建、Module/Contract 生成检查、需求/计划/
状态门禁与双语文档构建通过。升级目录中的既有 ai_agent 缺口未改变。本地结果不计入服务器验收。
