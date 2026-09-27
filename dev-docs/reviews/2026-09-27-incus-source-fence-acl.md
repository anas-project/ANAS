# Incus 租约来源围栏 network ACL

状态：实现与实机验收记录。日期：2026-09-27。基线：`fe85e510` 加本轮工作树。关联需求 `INCUS-R-044`
（M9a），并影响 `INCUS-R-047`/`R-048` 的卸载盘点。

## 问题

2026-09-26 的两档生命周期发现 lease profile 不能要求 `security.ipv6_filtering`：Incus 只在宿主加载
`br_netfilter` 时启动带该项的实例，而 Docker 28 起默认不再加载该模块（见
[两档生命周期](2026-09-26-incus-vm-tier-lifecycle.md)）。移除后，IPv6 启用租约里的 guest 可以给自己配置
子网外的 IPv6 源地址：网桥的 masquerade 规则只改写本网桥子网，这类报文会以伪造源被宿主转发出去。
MAC 与 IPv4 过滤仍在 profile 中，IPv4 伪造源由网桥层过滤挡下。

操作者选择由 Provider 自有 network ACL 关闭该缺口，而不是让宿主供给启用 `br_netfilter`：后者改变
宿主上所有网桥（包括 Docker）的报文路径，超出 Incus Module 的归属。

## 实现

- `modules/incus/provisioner/acl.go`：每张租约网桥一份 default project 内、与网桥同名的 ACL，带
  `user.anas.consumer`、`user.anas.sandbox`、`user.anas.lease_credential` 归属标记；唯一规则为启用的
  egress `allow`，`source` 恰为网桥实际分配的 IPv4（IPv6 启用时加 IPv6）子网。网桥写
  `security.acls=<ACL>`、`security.acls.default.egress.action=drop`、
  `security.acls.default.ingress.action=allow`。
- `ensureNetwork` 在读回网桥具体子网后写 ACL（daemon 拒绝引用不存在的 ACL），写后读回，再挂到网桥并
  读回。同名 ACL 标记不符（包括无标记）即拒绝接管、不改写、不挂接、不登记证书：ACL 是新对象，没有
  需要兼容的无标记历史。
- `inspect.ready` 同样要求挂接键、恰好一条规则且来源集合等于当前子网；多余规则、入向规则、禁用或
  放宽都算漂移，`ensure` 以整体 PUT 修复。IPv6 由开转关时规则同步收窄为 IPv4 子网。
- `internal/incusprovision/uninstall.go`：删包前的共享 daemon 盘点加入 `network-acls`，存在任何 ACL 即
  `ErrBlocked`，与受管网络、镜像同理。
- 各实验清理路径在删网桥之后删除同名 ACL；Core 原生清理核对 ACL 的归属标记与 `used_by` 为空后删除。

对照 Incus 7.0.1 源码（`internal/server/firewall/drivers/drivers_nftables*.go`、
`internal/server/network/acl/acl_firewall.go`）确认：网桥 ACL 在 `input`/`forward`/`output` 钩子执行，
DNS、DHCP 与核心 ICMPv6（RS/NS/NA/MLD）由内建规则先放行，`ct state established,related` 放行回包；
一条混合 IPv4/IPv6 来源的规则会拆成 `ip saddr` 与 `ip6 saddr` 两条 nft 规则。围栏约束的是报文离开
网桥时的源子网，不区分同一租约子网内的不同 guest；同租约 guest 之间的 IPv6 冒用仍不受约束。

## 单元回归

`modules/incus/provisioner/acl_fence_test.go`：规则与默认动作（IPv6 开/关）、八种漂移使 `inspect`
未就绪且 `ensure` 修复、三种他人 ACL 拒绝接管、IPv6 由开转关收窄。对实现做三处变异（`inspect` 不校验
ACL 内容、`ensure` 不回写 ACL、不校验 ACL 归属）时相应用例失败。卸载盘点新增
`network ACL` 反例，对旧实现失败。

## 实机验收

环境同[两档生命周期](2026-09-26-incus-vm-tier-lifecycle.md)：`ln.hlong.wang` 只做只读盘点与私有 QEMU
管理，实验根 `/data/anas-incus-lifecycle-20260926.616ts5`，每轮一台一次性 Debian 13 VM，从 Zabbly
`lts-7.0` 安装 Incus 7.0.1，依次运行容器档、VM 档与双栈阶段。

`server-incus-network-e2e.py` 新增伪造源反例：合法出网通过后，guest 给 `eth0` 加子网外的 IPv4/IPv6
地址，以静态邻居把报文直接交给网桥（排除 ARP/NDP 的影响），实验 VM 与上游 netns 各以 nft 命名计数器
统计这些源地址。判据：上游计数恒为 0；IPv6 租约的伪造 IPv6 报文必须到达实验 VM 的路由层（计数 > 0，
证明是围栏挡下而非更早的过滤）；再做一次故意漂移（`network acl rule add … egress action=allow`），
要求泄漏可被上游计数看见、Provider `inspect` 报未就绪、`ensure` 修复且之后不再泄漏。

### 最终通过轮 r10（`g10`）

| 阶段 | 结果 | 关键指标 |
| --- | --- | --- |
| 容器档 | 22 项通过 | 两租约创建到就绪 4.9 / 4.3 s；根盘实写 4 291 723 264 B；典型作业墙钟 7.6 s |
| VM 档 | 21 项通过 | 两租约创建到 agent 就绪 58.6 / 43.6 s；根盘实写 3 220 914 176 B；典型作业墙钟 45.1 s；7.0 LTS 嵌套虚拟化仍可见（已知限制） |
| 双栈与来源围栏 | 2 份租约通过 | 见下表 |

| 租约 | 合法出网 | 伪造 IPv4（到达实验 VM / 上游） | 伪造 IPv6（到达实验 VM / 上游） | 故意漂移 |
| --- | --- | --- | --- | --- |
| IPv6 启用 | 两族 masquerade 为实验 VM 上游地址 | 0 / 0（网桥层 IPv4 过滤） | 4 / 0 | 泄漏 4 个报文；`inspect.ready=false`；`ensure` 退出 0 后 `ready=true`，再伪造上游增量 0 |
| IPv6 关闭 | IPv4 masquerade，guest 无全局 IPv6 | 0 / 0 | 0 / 0 | — |

- 归档 SHA-256：`c45232f8691d839e181b000e604149715a66fa1b80b40f47c869bc975d06f305`；输入 `provider`
  `5edd9372…64ea`、`server-incus-network-e2e.py` `dc14e224…0e7e`，其余摘要在轮次 `src/manifest.json`；
- 终态：正常关机、QEMU 退出 0，宿主 Docker/服务/nft/双栈路由十项对照相同，VM 文件按固定清单删除。

### 失败与诊断轮

- **r8**：容器档 22 项、VM 档 21 项通过；双栈阶段 IPv6 租约的合法 IPv6 探测一次失败即判负（当时不重试），
  IPv4 正常，伪造源检查未执行。归档 `b3eca2d4…c941`，VM 磁盘按失败轮保留。
- **r9（`hold-1`）**：保留已安装 VM，以加入有界重试与失败私有诊断的新 harness 手工连跑 4 次双栈阶段，
  4 次合法 IPv6 均第一次即成功，伪造源与漂移判据全部满足。诊断轮不计入验收。
- r10 中同一探测第 2 次成功（`probe_attempts` 记录为 2）。两次首发失败都出现在容器/VM 两阶段之后；
  被丢弃的首个 SYN 会在 15 秒内重传，不会使探测失败，失败应是连接立即出错（新 SLAAC 地址仍在 DAD、
  尚不可作源地址）。这一归因是推断，未取得失败时 guest 的错误输出；harness 现保留重试次数与失败诊断。

### Core 路径

真实 Core CLI 链路（host-v43，Ubuntu 26.04 审批安装的 Incus 7.0.1）使用同一 Provider：两个合成消费者的
租约 ensure、移除消费者后的撤销、清理时核对并删除同名 ACL、审批卸载删包前 `network-acls` 盘点为空均通过，
见 [prune 验收](2026-09-27-incus-image-prune.md)。

## 验证

- `go vet ./...`（及相关包 `GOOS=linux`）与 `go test ./...` 通过。首次全仓运行中 `internal/incusingresshost`
  两项在并行负载下因 5 秒宿主命令上限超时失败，单独重跑及最终全仓重跑均通过；该包本轮未改动。
- `test_incus_network_e2e` 等相关 Python 离线门禁通过。
