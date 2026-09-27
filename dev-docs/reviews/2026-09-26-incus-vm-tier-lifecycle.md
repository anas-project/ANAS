# Incus 7.0.1 两档生命周期、双栈出网与三处回归缺陷

状态：实机验收记录与缺陷修复。日期：2026-09-26。基线：`fe85e510` 加本轮工作树。关联需求
`INCUS-R-007`、`R-008`、`R-011`、`R-025`、`R-029`—`R-034`、`R-043`、`R-044`、`R-052`。

## 环境与隔离

操作者指定的 `ln.hlong.wang`（Ubuntu 22.04 / 5.15，Intel，`kvm_intel nested=Y`）只做只读盘点和私有
QEMU 文件/进程管理。实验根 `/data/anas-incus-lifecycle-20260926.616ts5`（0700），宿主侧所有者脚本为
仓库内 `test-env/scripts/server-incus-lifecycle-lab.py`：每轮一台一次性 Debian 13 genericcloud VM
（3 vCPU / 3.5 GiB / user-mode NAT / 回环 SSH），安装脚本
`test-env/fixtures/incus-lifecycle-lab/install-debian13-zabbly-lts.sh` 从 Zabbly `lts-7.0` 安装
Incus 7.0.1（与产品宿主供给同一来源），依赖经保留 Debian 签名的阿里云镜像。同一台 VM 里依次运行
容器档、VM 档（嵌套 KVM）与双栈出网三个阶段，前后宿主 Docker/服务/nft/双栈路由十项对照。

VM 档夹具取 images.linuxcontainers.org 的 `debian:trixie:amd64:default` VM 镜像（`20260926_05:24`，
组合指纹 `4e38eb6d…9840a`，逐文件 SHA-256 按 simplestreams 索引核对），在默认 project 起一台构建 VM、
经 agent 推入同一个夹具程序后发布并导出，再导入各租约；它不是产品镜像。双栈阶段用同版本的
容器镜像（组合指纹 `18389062…0723`）。

## 最终通过轮 r7（`g7`）

| 阶段 | 必需通过 | 关键指标 |
| --- | --- | --- |
| 容器档 `TestNativeIncusContainerLeaseLifecycle` | 22 项 | 两租约创建到就绪 5.0 / 4.4 s；根盘实写 4 291 723 264 B（上限 4 GiB）；典型作业墙钟 8.0 s（就绪 5.5 s、执行 0.4 s、回收 2.1 s） |
| VM 档 `TestNativeIncusVMLeaseLifecycle` | 21 项 | 两租约创建到 agent 就绪 57.0 / 56.4 s；根盘实写 3 212 521 472 B（上限 4 GiB）；典型作业墙钟 47.2 s（就绪 43.4 s、执行 1.1 s、回收 2.7 s） |
| 双栈出网 `server-incus-network-e2e.py` | 2 份租约 | IPv6 启用租约的两族流量都被 masquerade 为实验 VM 自己的上游地址；IPv6 关闭租约的网桥 `ipv6.address=none`、guest 无全局 IPv6，IPv4 仍被 masquerade |

两档都验证了：同名实例在两个 project 互不可见、实例数配额、`limits.cpu/memory/disk` 直接超配被 daemon
拒绝、host disk / 其他租约网络 / proxy / gpu / usb 设备请求被拒（容器档另有 unix-char、unix-block、
privileged、`raw.lxc`，VM 档另有 pci、`raw.qemu`），stdin Secret 只经 exec 标准输入、管理证书重叠/撤销
后原 guest 身份不变、exec 取消后独立回收且另一租约不受影响、stop/delete 幂等。

- 归档 SHA-256：`9b99c5c98c728e34af2d64d0ac5cc8967d8b660d72992891c9bb1486f14b79ae`；
- 输入：`provider` `b637dd6c…cbe6`、`computeclient.test` `c475666a…82fe`、`anas-fixture` `8390a71a…c5a6`、
  两个 harness 与安装脚本的摘要记录在轮次 `src/manifest.json`；
- 终态：正常关机、QEMU 退出 0、清理与宿主十项对照全部通过，VM 文件已按固定清单删除。

时延来自嵌套虚拟化（实验 VM 内再起 Incus VM），且宿主磁盘是 HDD RAID10：它是可复现的相对基线，
不能直接当作目标 NAS 的绝对容量规划值。ARM64、ZFS 池和真实产品镜像仍不在本次覆盖内。

## 过程中发现并修复的缺陷

1. **lease profile 要求 `security.ipv6_filtering` 使实例无法启动。** 上一轮合入把三项来源过滤都写进
   profile；Incus 只在宿主加载 `br_netfilter` 且 `bridge-nf-call-ip6tables=1` 时启动带 IPv6 过滤的实例，
   Docker 28 起默认（`icc=true`、userland proxy 开）不再加载该模块。r2—r4 的容器全部启动失败，守护
   进程报错 `security.ipv6_filtering requires bridge netfilter: br_netfilter kernel module not loaded`。
   修复：profile 与 HTTP 观察器只要求 MAC、IPv4 过滤。**遗留缺口**：IPv6 启用租约里 guest 伪造子网外
   源地址的报文不会被 masquerade，R-044 因此当时不关闭；2026-09-27 已由 Provider 自有网桥 network ACL
   关闭，见[来源围栏](2026-09-27-incus-source-fence-acl.md)。
2. **共享客户端 `Inspect` 在任何 7.x CLI 上恒为 missing。** 它用 `incus list <remote>:<name>`；7.x 的
   `[<remote>:] [<filter>...]` 语法把它解析为 remote 加落空过滤，返回空列表（6.0 是前缀过滤）。于是
   `Delete` 先读后删时直接返回、从不删除，删除确认也会误判。Forgejo controller 镜像内置 7.3.0 CLI；
   此前 one-job 实机用的是宿主 6.0.5 CLI，未暴露该问题。修复：列出整个租约 project 后精确匹配名称，
   单元回归 `TestInspectListsTheProjectAndMatchesTheExactName`。
3. **VM 档从未能创建用满磁盘配额的实例。** Incus 统计 `limits.disk` 时给 VM 根盘加上状态卷
   `size.state`（默认 500 MiB，6.0.5 与 7.0.1 源码一致），Provider 只按 `max_instances × disk_gib` 预算，
   实测 `Reached maximum aggregate value "4GiB"`。修复：VM 档每实例预算 `disk_gib GiB + 500 MiB`，
   profile 根设备显式 `size.state=500MiB`，回归 `TestVMLeaseBudgetsTheStateVolume`。

## 已知限制

Incus 7.0 LTS 没有 `restricted.virtual-machines.nesting`：`security.nesting` 在该版本是容器专用键，VM
接受但不生效；嵌套 KVM 上 VM 租约 guest 实测可见 `vmx`（`guest_virtualization_flags=2`）。7.0 LTS 因此
无法从项目层阻止 VM 档嵌套虚拟化；后续 7.x 由 Provider 写 `restricted.virtual-machines.nesting=block`
（7.5.1 实测拒绝，见 [fence 实机核验](2026-09-26-incus-fence-native-validation.md)）。生命周期测试把它记为
版本相关的显式检查与指标，不计入 daemon 拒绝。

## 实验侧修正（不是产品问题）

- Debian 13 的 `/run` 为 noexec，管理证书轮换夹具不能从 `/run` 执行 Provider 副本，改放
  `/var/lib/anas-incus-lifecycle`；
- 构建 VM 需显式 4 GiB 根盘，否则发布出的夹具取 VM 默认 10 GiB，超出租约磁盘配额；
- 嵌套 guest 偶尔忽略 ACPI 关机，构建 VM 先 `sync` 再有界优雅停止，失败才强制停止；
- 运行中的 VM 会自行更新 `volatile.*`，拒绝类用例只比较声明配置与设备；
- 另加入失败时的私有诊断（daemon 日志、以管理员重放被拒请求），只写入 0700 报告。

r1—r6 为失败或诊断轮，原始记录保留在实验根；r6 是保持运行的诊断 VM，按设计在 2 小时上限处被
终止，它不作为验收证据。

## 验证

- 本机 `go vet` 与 `internal/computeclient`、`internal/computeingressruntime`、`internal/runner`、
  `internal/api/httpapi`、`modules/incus/...` 测试通过，另跑过一次全仓 `go vet ./... && go test ./...`；
- `test_incus_lifecycle_e2e`、`test_incus_lifecycle_lab`、`test_incus_network_e2e` 离线门禁通过并接入 CI。
