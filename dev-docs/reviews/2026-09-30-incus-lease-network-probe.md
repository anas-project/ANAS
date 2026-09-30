---
doc_type: review
status: current
created: 2026-09-30
updated: 2026-09-30
---

# 租约网络实机探测（M10a、M11b、M11c 与控制连接）

状态：实机核对记录。日期：2026-09-30。基线：`7ad1876a` 加累积工作树。覆盖
[Incus 计划](../plans/incus-module.md) M10a、M11b、M11c 的待验证项，以及
[设计简化评审](2026-09-28-incus-design-simplification-review.md)第 5 项（控制转发服务）的启动顺序问题。
按本记录的结果，第 5 项已实施，见 §4。

## 结论

- 设计里依赖的 Incus 与 Docker 行为都成立：ACL 的 drop 先于 allow；address set 能作出站目的地址（DNAT 之后）
  和入站来源地址（masquerade 之前）；默认拒绝入站不影响回包、DHCP、DNS；`security.port_isolation` 即时隔离
  同租约实例；受限证书能写槽位固定地址；Docker 式端口表对局域网、宿主本机与 Docker 容器都生效，保留真实客户端
  地址，不劫持旁路流量。
- 需要改设计的四处：
  1. Debian 的 docker.io 26 下，只把租约网桥交给 ACL 的宽规则会放行未发布的容器端口，要改用窄规则。
  2. UDP 端口占位会被报文触发、很快失效，要改为常驻进程持有。
  3. 回环地址不经过端口表。
  4. 同租约实例经宿主端口访问自己的槽位会被挡住。
- 控制连接：Incus 7.0.1 绑定失败只在 30 秒后重试一次；给 `incus.service` 加 `After=docker.service` 后，
  顺序启动和模拟开机都能监听，Docker 重启、网络重建后也不受影响。据此已让 Incus 直接监听控制网桥网关，
  删除转发服务。
- 实验轮正常关机，物理宿主 10 项基线前后一致。

## 1. 环境

- `ln.hlong.wang` 实验根 `/data/anas-incus-lifecycle-20260926.616ts5`，轮次 `r13-lease-network-probe`，`hold-leasenet`
  诊断轮：一次性 Debian 13 VM `anas-incus-lifecycle-fec4e6`（3 vCPU / 3.5 GiB，user-mode NAT）。所有者脚本
  `test-env/scripts/server-incus-lifecycle-lab.py` 与仓库一致（SHA-256 `36db9f23…6c00`）。
- VM 内：内核 6.12.107+deb13-cloud-amd64，Zabbly `lts-7.0` 的 Incus 7.0.1（`1:7.0.1-debian13-202609250206`），
  nftables 1.1.3，iptables 1.8.11（nf_tables）。Docker 先用 Debian 的 `docker.io 26.1.5+dfsg1-9+deb13u1`，再换成
  Docker CE 29.7.2（containerd.io 2.3.3）。
- 租约由真实 Provider（`dddae0c2` 的输入）建立：`probe-a` 开 IPv6（10.232.143.0/24、fd42:f024:e98f:c75d::/64），
  `probe-b` 只有 IPv4（10.120.220.0/24）。Docker 里的替身：Traefik（发布 18443→8443）、另一个 Module
  （发布 18444→9001，另有未发布的 9002）。独立 netns 代表局域网机器（198.51.100.0/24、2001:db8:51::/64）。
- 探测脚本 `test-env/fixtures/incus-lifecycle-lab/lease-network-probe.py`，最终版 SHA-256 `a7b7608b…f6b4`。前几版的
  问题都在脚本，不在被测对象：Provider 的内存与磁盘下限（512 MiB、4 GiB）、Debian 把 docker CLI 拆成
  `docker-cli`、`--device` 一次只能改一个键。
- 轮次结果：正常关机，QEMU 退出码 0，清理通过；物理宿主的容器、网络、卷、Docker 配置与单元、nft、双栈路由、
  命名 netns 共 10 项前后一致。VM 磁盘按诊断轮惯例保留（3.9 GiB）。

## 2. 结果

### 2.1 ACL 与地址清单（M10a、M11b）

| 检查 | 结果 |
| --- | --- |
| 规则顺序：先写的精确 allow 加后写的更宽 drop | 被挡住：drop 总是先于 allow，与书写顺序无关 |
| address set 作出站目的：guest 经网关或宿主局域网地址访问 Traefik 发布端口 | 放行；nft 里是 `ip daddr @set tcp dport 8443`，匹配的是 DNAT 之后的**容器端口** |
| 同一条规则下访问另一个 Module 的发布端口；ACL 为空时访问 Traefik | 都被挡住 |
| 默认拒绝入站：TCP/UDP 回包、IPv6 回包 | 正常 |
| 默认拒绝入站：新实例拿地址（DHCPv4、SLAAC）、本地与转发 DNS | 正常 |
| 默认拒绝入站：局域网、Docker 容器、其他租约访问 guest | 都被挡住；改回默认放行后可达（对照） |
| address set 作入站来源：Traefik → guest 的 HTTP 端口 | 放行，guest 看到的来源是租约网关（masquerade 之后），ACL 匹配的是之前的 Traefik 地址 |
| 同一规则下：其他容器、Traefik 访问其他端口、局域网、其他租约 | 都被挡住 |

Traefik 地址会变：`control` 阶段停启 Docker 后，两个替身容器重启，Traefik 从 172.30.0.2 变成 172.30.0.3。
这是 `INCUS-R-118` 需要同步地址清单的直接证据。

### 2.2 `internet_lan_host` 与 Docker 版本（M10a）

ACL 放行租约到 Docker 网段，比较宿主上两种静态规则：

| Docker | 规则 | 未发布端口直连 | 发布端口经宿主 | 发布端口直连容器地址 |
| --- | --- | --- | --- | --- |
| docker.io 26.1.5 | 宽：`-i anas+ -j ACCEPT` | **可达** | 可达 | 可达 |
| docker.io 26.1.5 | 窄：到 Docker 网桥只放行 `--ctstate DNAT`，其余丢弃 | 挡住 | 可达 | 可达（Docker 自己放行发布端口） |
| Docker CE 29.7.2 | 宽 | 挡住 | 可达 | 挡住 |
| Docker CE 29.7.2 | 窄 | 挡住 | 可达 | 挡住 |

Docker 28 起在 `DOCKER` 链末尾丢弃未发布端口，并在 raw 表丢弃从其他接口直连容器地址的包；26 没有。窄规则在两个
版本上都满足 `INCUS-R-115`，因此静态规则改用窄规则（§3）。

换装 Docker CE 后第一次测量里，发布端口经宿主、容器访问端口绑定都失败了。nft trace 显示回包被路由到卸掉的
docker.io 留下的旧网桥（与新网络同为 172.30.0.0/24）。这是实验里换包的副作用，删掉旧网桥后重测，结果见上表与 §2.4。

### 2.3 同租约隔离与槽位（M11b、M11c）

| 检查 | 结果 |
| --- | --- |
| lease profile 网卡设 `security.port_isolation=true` | 即时生效，重启后仍生效；同租约实例之间 IPv4、IPv6 都不通；出站与 DNS 不受影响 |
| 租约证书创建带固定 IPv4/IPv6 的实例（每个 `--device` 覆盖一个键） | 成功；展开后的网卡保留 profile 的 `ipv4_filtering`、`mac_filtering` |
| 有状态 DHCPv6 | 拿到固定 IPv6（`::20`） |
| 用固定地址访问局域网；用同网段其他地址伪造来源 | 前者正常，后者被 `ipv4_filtering` 挡住 |
| 第二台实例用同一地址 | 可以创建；启动时被拒（地址已被其他网卡定义） |
| 同名实例删除后重建 | 拿回原地址 |
| 租约证书创建时关掉 `security.ipv4_filtering` | **可以**；受限 project 不限制网卡键 |

最后一项与现有信任模型一致：同一租约属于同一个消费者，伪造只能发生在本租约网段内；跨租约或网段外的来源仍由
来源围栏 ACL 丢弃。IPv6 租约本来就没开 `ipv6_filtering`（依赖 `br_netfilter`，2026-09-26 已移除）。写入设计
§5.1.8 的信任边界，不改需求。

### 2.4 端口绑定（M11c）

用 Docker 式规则链：`fib daddr type local` 的流量按端口表 DNAT 到槽位（TCP 2222→7022、UDP 2253→7053，
IPv4 与 IPv6 各一张表）；租约 A 的入站 ACL 先丢弃全部租约网段（address set），再放行槽位的这两个端口。

| 来源 | docker.io 26 | Docker CE 29 |
| --- | --- | --- |
| 局域网 TCP/UDP，IPv4 | 通，guest 看到真实客户端地址 | 同左 |
| 局域网 TCP/UDP，IPv6 | 第一次超时，加占位后重测通过 | 通 |
| 宿主访问自己的局域网地址、上联地址 | 通 | 通 |
| 宿主访问另一台机器的同号端口 | 不被改写 | 同左 |
| Docker 容器访问自己的网关、宿主局域网地址 | 通，来源是租约网关（Docker 的 masquerade） | 同左 |
| 其他租约经宿主端口、直连槽位 | 挡住 | 挡住 |
| 同租约实例经宿主端口 | **挡住**：地址清单里也有自己的网段 | 同左 |
| 去掉租约 drop 规则后，出站不限的租约经宿主端口 | 通（说明 drop 规则必需） | 同左 |
| 同上，但对方租约出站只放行局域网 | 挡住（对方 ACL 看的是 DNAT 之后的地址） | 同左 |
| 宿主经 `127.0.0.1` | 不经过端口表，到达占位后 TCP 被复位 | 同左 |

IPv6 第一次超时只在 docker.io 那一轮出现，原因没有查清；同一轮加占位后以及 Docker CE 两轮都通过。M11c 的 e2e
仍按 `INCUS-R-157` 单独核验 IPv6。

### 2.5 端口占位

| 检查 | 结果 |
| --- | --- |
| systemd 套接字单元：TCP `Accept=yes` 加 `/bin/true` | 占住端口；经回环的连接被接受后立即关闭；单元保持 active |
| 同样方式的 UDP（报文触发一个空服务） | **失效**：服务不读报文，套接字一直可读，反复触发后达到启动次数上限，单元进入 failed，端口被释放 |
| 占位后宿主进程绑定同一端口（任意地址、具体地址、IPv6） | TCP 都是 `EADDRINUSE`；UDP 因上一项已失效而绑定成功 |
| 占位后 Docker 发布同一端口（开、关 userland proxy） | 两个 Docker 版本都直接失败（`address already in use`） |
| 改为 `Accept=no` 加常驻 `sleep infinity` 服务（`DynamicUser=yes`）持有套接字，各打 300 个回环 UDP 报文和 TCP 连接 | 套接字与服务都保持 active、无重启；端口仍被占住；每个持有进程约 1.8 MB；队列满后内核丢包，不再触发服务 |

结论：占位改为常驻持有进程（§3）。有占位时，关掉 userland proxy 的 Docker 发布也会失败，`INCUS-R-162`
运行时检查的主要作用变为发现占位丢失与规则漂移。

### 2.6 控制连接的启动顺序（简化评审第 5 项）

在 Docker 网络 `probe-control`（网关 172.29.0.1，网桥 `probectl0`）上测试 `core.https_address=172.29.0.1:8443`：

| 场景 | 结果 |
| --- | --- |
| 网桥存在时设置 | 监听；控制网络上的容器能建立 TCP 连接 |
| 停掉 Docker 并删网桥，只启动 Incus | 没有监听；日志 `Cannot currently listen on https socket, re-trying once in 30s` |
| 随后立即启动 Docker（30 秒内） | 重试成功，开始监听 |
| 再设置一次相同的值 | 仍在监听 |
| drop-in `After=docker.service`，停掉两者并删网桥后一起启动 | 监听，容器可连 |
| 同上，改为启动 `multi-user.target`（模拟开机） | 监听，容器可连 |
| 重启 Docker；删除网络后重建（同网段、同网关） | 监听一直在，重建后容器可连 |

`incus.service` 在 Zabbly 包里由 `incus.socket` 激活（`indirect`），开机时由 `incus-startup.service` 拉起。
Incus 只重试一次，Docker 晚于 30 秒启动就会永久没有监听，所以顺序依赖是必需的，不能只靠重试。实验 VM 以
`-no-reboot` 运行，没有做真正的重启；`multi-user.target` 是最接近的模拟。

## 3. 设计与需求的相应修改

- `INCUS-R-126`：宿主静态规则改为一组固定规则，从租约网桥到 Docker 网桥只放行 Docker 已 DNAT 的连接
  （发布端口），其余丢弃；其他方向仍交给 ACL。宿主供给设计 §5.4 同步。
- `INCUS-R-151`：写明回环地址（`127.0.0.0/8`、`::1`）不接管，与 Docker 关闭 userland proxy 时一致；打开
  `route_localnet` 的代价不值得。
- 端口占位（`INCUS-R-160`）：每个生效的绑定由一个套接字单元（`Accept=no`）加一个常驻、不读数据的持有服务
  （`DynamicUser=yes`）占住；设计 §5.1.8 同步。
- 同租约经宿主端口访问自己的槽位：按现有规则会被挡住。租约内本来可以直连（`intra_lease`），不为此给每个租约
  维护一份不含自己的清单；写进设计 §5.1.8。
- 槽位信任边界（§2.3 最后一项）：写进设计 §5.1.8。

## 4. 控制连接的实施

- Incus 的 `core.https_address` 由 `incus.configure` 设为控制网桥网关的 `8443`，这是它唯一的 HTTPS 监听；
  同时写入 `/etc/systemd/system/incus.service.d/anas-after-docker.conf`（`After=docker.service`）并
  `daemon-reload`。防火墙在监听移动之前安装：放行控制网桥网段与本机登记探测到网关 `8443`，丢弃其他来源。
  卸载时先撤监听与 drop-in，再删防火墙。
- 连接包升为 `anas.incus-connection-bundle/v2`，endpoint 为 `https://<网关>:8443`，去掉 `relay_service`；
  宿主状态以 `ownership.control_listener` 记录。旧版连接包与带 `relay_service` 的状态会被严格解码拒绝，只存在于
  做过实验的宿主上。
- 删除 `modules/incus/control-relay`（826 行，含测试）、`packaging/systemd/anas-incus-control-relay.service`、
  `anas-incus-relay` 账号的创建、`/etc/anas/incus-control-relay.json`、`useradd` 固定命令与 `18443` 规则；发行
  构建不再打包转发服务。安装器在升级与卸载时删除旧版留下的单元与二进制。
- 测试：`internal/incusprovision`（新增：监听只在防火墙之后移动、读回失败时保留归属以便卸载、卸载时监听先于
  防火墙删除）、`modules/incus/hook`、`cmd/anas-hostd` 打包测试、`scripts/ci/install-test.sh`（旧转发服务在升级时
  被删除）、`test-env/helpers/incus-control-probe`（要求 Incus 自己报告的监听地址就是被探测的网关地址）。
- 实机门禁：`server-incus-host-action-e2e.py` 的控制桥 mTLS、错误 pin、匿名与其他网络四项改为直接连 Incus，
  结果见[宿主通道简化记录](2026-09-30-incus-old-code-removal-and-hostd-simplification.md) §5。

## 5. 未覆盖

- 没有真正重启 VM 验证开机顺序；`multi-user.target` 是模拟。
- Docker 停着时重启 Incus，监听要等下一次 Incus 重启才恢复；运行时检查尚未实现。
- 同租约经宿主端口访问自己的槽位被挡住；如果以后需要，要改 Provider 的地址清单。
- 常驻持有方案只在实验单元里验证过，还没有进入实现（M11c）。
