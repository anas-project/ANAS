---
doc_type: review
status: current
created: 2026-09-28
updated: 2026-09-28
---

# compute 租约入站简化草案

状态：已采纳，2026-09-29 定稿。操作者确认 §10 四点（中介放在 anasd 进程内），经三轮补充（§11—§13）后定稿：
`none`、`published` 两档，经 Traefik 的 HTTP 发布与 Docker 式端口绑定分开，多租约规则单列。写入
[Incus 要求](../requirements/incus-module.md) §7septies 与 `INCUS-R-130`—`R-163`，另新增 `HOSTACT-R-014`、`R-015` 与
[运行问题记录要求](../requirements/runtime-issues.md)；M11 重排，新增 M11b、M11c。本文保留为设计过程记录，以需求
矩阵为准。
原状态：设计与需求草案，未进矩阵，也没有改动实现。确认后，它取代
[宿主供给设计](../../docs/architecture/incus-host-provisioning.md) §5.1.7 的逐发布许可方案和 §7.5—§7.14 的候选实现；
§5.1.1—§5.1.3 的声明语法、域名三档和认证声明不变。基线：`7ad1876a` 加当前工作树。背景见
[设计简化评审](2026-09-28-incus-design-simplification-review.md)第 2、3 项，出站的对应方案见
[出站分级草案](2026-09-28-compute-egress-tiers-draft.md)。

## 1. 现有设计为什么要 30 秒许可

现有方案把每次发布当成一份独立授权。授权的对象是某个实例的某一次运行，而不是租约：

- 每次发布都在宿主上加一条 nft 许可，放行 Traefik 源地址到 guest IPv4:端口，期限 30 秒
  （`internal/incusingresshost/types.go` 的 `defaultPermitTTL`）。另外还有三样配套：钉住宿主 veth 的 `/32` 路由
  与永久邻居、回复方向的来源绑定（ifindex、MAC），以及撤销时的双向 conntrack 清理；
- 中介每次续期之前，都经 hostd 的只读观察动作重新读取实例事实（UUID、运行代际、IP、MAC），身份没变才续期。

期限防的是规则比实例活得久。一次性实例退出后，它的 IP 可能分给下一个实例，而下一个实例常常是同一租约里另一个
用户的作业。如果中介崩溃、漏掉停止事件，或者撤销失败，旧许可就会把旧域名指向新作业。有了 30 秒期限，没人续期的
许可会自己失效，安全不依赖撤销一定成功。

代价就是 M11 暂停的原因：

- 续期需要一个常驻、受信、能读实例事实的 owner；
- 为了让续期和撤销可靠，又加了执行回执、退役墓碑、排空屏障和跨进程工作区围栏；
- 部署热路径上的每个工作区写任务，都要先排空中介。

## 2. 前提：按租约信任

与出站相同：同一租约的实例属于同一个消费者，权限相同。放到入站上：

- 消费者持有租约证书，本来就能对租约里任何实例 exec、读写数据、删建实例。在租约内部防它「把 A 实例的域名指向
  B 实例」，挡不住任何它原本做不到的事。
- 需要守住的边界只剩四条：
  - 后端只能是本租约的实例，不能指向其他租约、宿主或局域网；
  - 域名只能落在本租约的命名空间内；
  - 认证方式与中间件由冻结授权决定；
  - 消费者不能写 Traefik 配置。
- 同一租约内，旧路由指向复用了该 IP 的新实例，从安全问题变成整洁问题，由消费者负责清理，见 §7。

## 3. 简化后的设计

### 3.1 数据面：租约 ACL 里的一条入站规则

- Provider 在租约网桥已有的那份 ACL 里加入站规则。这份 ACL 由来源围栏与出站分级共用，新增规则为：
  `allow tcp，source=$<Traefik 地址集>，destination_port=<allowed_ports>`。未声明 `ingress` 的租约没有这条规则。
- 网桥的 `security.acls.default.ingress.action` 从 `allow` 改为 `drop`。Traefik 之外的来源（其他 Docker 容器、
  局域网、其他租约）都不能再主动连进租约实例。
- Traefik 地址集就是出站 `module_access` 用的那一份（`INCUS-R-118`、`R-119`），不新增维护者。
- 不需要宿主规则。Traefik 是 Docker 容器，它发往租约网段的连接由 Docker 自己的容器出站规则放行并做
  masquerade，回包走 conntrack。入站也不依赖 `INCUS-R-126` 那条静态规则。
- 不再需要专用 Docker 入站网桥、Traefik 的多网络路由和宿主路由表：宿主本来就有到每个租约网段的直连路由。
- 后端仍然只用 IPv4，公网 IPv6 在 Traefik 终止。guest 看到的客户端地址是网桥网关（masquerade 之后），
  真实客户端在 `X-Forwarded-For`；服务要监听 guest 网卡地址或 `0.0.0.0`。

依据是 Incus 7.0.1 源码（`internal/server/firewall/drivers/drivers_nftables_templates.go`、`drivers_nftables.go`）
与 [ACL 文档](https://github.com/lxc/incus/blob/v7.0.1/doc/howto/network_acls.md)：

- 网桥 ACL 在 forward 钩子上对 `oifname <网桥>` 的报文执行入站规则。此时 Docker 的 SNAT 还没发生，看到的源地址
  就是 Traefik 容器的地址；
- ACL 链的第一条是 `ct state established,related accept`，所以默认入站拒绝不影响实例主动出站的回包；
- DNS、DHCP 与核心 ICMP 由 ACL 之前的基线规则放行，ACL 不影响它们；
- 网桥 ACL 只作用于网桥与宿主之间，同一网桥内的实例互访不经过它，这由出站的 `intra_lease` 另行处理；
- address set 只在 nftables 驱动的网桥上可用，入站规则的 `source` 可以引用它。

现状观察（按源码推断，未实测）：Provider 目前把默认入站设为 `allow`（`modules/incus/provisioner/acl.go`），
Docker 又放行容器出站，所以现在任何 Docker 容器都能连到租约实例的任意端口。改为默认拒绝，对没有声明入站的
租约同样有意义，建议随 M10a 一起做。

### 3.2 控制面：中介只把请求变成路由文件

- 消费者仍然只写本租约的请求目录（`INCUS-R-087`、`R-088` 不变）。请求改为 `{instance, address, port, label?}`：
  - `address` 由共享客户端从自己创建的实例读出；
  - random 模式的 label 由共享客户端按 `HMAC(lease_secret, workload_id)` 算出，算法沿用 `internal/computeingress`
    （`INCUS-R-065` 不变）。
- 中介逐条校验：
  - 端口在冻结的 `allowed_ports` 内；
  - `address` 在本租约的 IPv4 网段内，且不是网络地址、网关或广播地址。网关是宿主自己的地址，放行它会让 Traefik
    连到宿主上的服务；
  - 按冻结的域名模式拼出域名：fixed 不带 label，named 要求 DNS label，random 要求 32 位小写十六进制。结果必须
    落在本租约的命名空间内；
  - 同一域名有多个请求时只发布一个（已发布的优先），其余拒绝，不覆盖；
  - 中间件与 entrypoint 取自冻结授权，请求里不能出现。
- 输出：Traefik 动态目录下中介专属的子目录里，每个有效请求一个文件，用既有 `ANAS_TRAEFIK_ROUTE__*` 模板渲染
  （`INCUS-R-054`）。子目录里的其他文件一律删除。
- 整个过程是「冻结授权 + 请求文件 → 路由文件」的纯函数，任何时候都可以全量重算，不需要回执、墓碑或锁协议。
- 中介不持有 Incus 凭据、租约证书、`lease_secret` 或任何 Secret，不调用 hostd，也不改宿主网络。
- 租约的 IPv4 网段与网关由 Provider 在 `ensure` 结果里交回，Core 记进 resource state。`INCUS-R-128` 的租约网络
  视图也需要这两项。

### 3.3 生命周期

| 事件 | 由谁处理 | 做什么 |
| --- | --- | --- |
| 作业启动并发布 | 共享客户端 | 写请求文件，中介随后写出路由 |
| 实例停止或删除 | 共享客户端 | 删除该实例的全部请求文件，中介随后删除路由 |
| 消费者崩溃 | 消费者的 janitor（启动时与周期运行） | 删除实例已不存在的请求文件 |
| 部署改变授权（端口、认证、域名，或去掉 `ingress`） | Core，在 apply 激活时 | 删除受影响租约的全部路由文件，Provider 更新 ACL；中介之后按新授权重建。CLI 与控制台部署都覆盖，不依赖中介是否在运行 |
| 租约撤销（`INCUS-R-111`） | Core 与 Provider | ACL 全部拒绝（出站设计已有），删除该租约的路由文件 |
| 中介重启或恢复 | 中介 | 全量重算 |
| 中介停机 | — | 已发布的路由继续可用，新发布等待；收紧授权与撤销仍在 apply 时生效 |

中介由三件事触发：请求目录的文件事件、部署激活，以及低频的全量重算（例如 30 秒一次）。全量重算只是补漏，
不是许可期限。中介停机时什么都不会过期，也不需要过期。

### 3.4 中介由谁运行（待定）

中介没有特权、没有状态、不持有密钥，由谁运行只是装配上的选择：

- **建议：anasd 进程内。** anasd 已经是常驻进程，也已经负责纠正出站用的 Traefik 地址清单（`INCUS-R-118`）。
  放进去的只是文件读写，不是简化评审和 [owner 对比](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-27-incus-runtime-owner-candidates.md)担心的特权
  内核写入（候选 B2）。
- 备选：部署里的一个常驻 Compose 服务，例如 Traefik Module 的伴随服务。它随部署启停、不依赖 anasd；代价是多
  一个镜像，而且要把所有租约的请求目录都挂进去。

## 4. 删除的机制

| 机制 | 主要代码 | 为什么不再需要 |
| --- | --- | --- |
| 逐发布 nft 许可、30 秒期限与续期 | `internal/incusingresshost` 的许可部分 | 改为 ACL 静态规则 |
| `/32` 设备路由、永久邻居、地址保留、回复来源绑定、conntrack 清理 | `internal/incusingresshost` 的 `address_*`、`conntrack*` 等 | 同租约 IP 复用不再是安全问题 |
| 专用 Docker 入站网桥与 Traefik 多网络路由 | 设计 §3.7 表中的入站网桥一行 | Traefik 留在自己的 Docker 网络，宿主已有到租约网段的直连路由 |
| 受信实例观测：hostd `incus.ingress.observe_http`、观察范围的计划/确认/撤销、v3 投影、incarnation | `internal/computeingressruntime` 的 reader、`internal/incusprovision/ingress_observation*`、`observer_configuration*`、宿主动作登记、OpenAPI 与 Web 的 `observer` 阶段 | 中介不再需要实例事实 |
| Traefik API 库存与加载确认、后端探测 | `traefik_reader.go`、`traefik_inventory.go`、`fixture_probe*` | 路由文件是中介唯一的输出，库存就是它自己的子目录 |
| 执行回执、退役墓碑、`retiring` 意图与状态库 | `executor.go`、`state_store.go`、`recovery.go` | 由全量重算取代 |
| 中介协调器、领取前排空、跨进程工作区围栏、启动准入 | `controller*`、`workspace_fence*`、`workspace_launch.go`、`internal/jobexecutor` 的 ingress 相关文件、`cmd/anasd` 的装配 | 部署不再等待中介，同时解决简化评审第 3 项 |
| 给中介的命名密钥交付 | `internal/runner/compute_ingress_delivery.go`、`credentials*` | 中介不再派生域名 |
| HTTP 原型命令 | `cmd/incus-network-prototype` | 实验工具 |

规模：约 1.6 万行非测试源码，外加对应测试；不含出站方案已计划删除的 `forwarding_*`。`internal/hostaction` 与
M10 宿主动作引用了 `internal/incusingresshost` 的少量代码，删除前要先拆开。

保留：

- `internal/computeingress`：声明校验、域名派生、命名空间检查与请求读写，请求字段按 §3.2 调整；
- Core 的冻结与冲突检查（`internal/runner/compute_ingress.go`），实现完成后去掉激活拦截；
- `deployment.Reader.HTTPAuthorizations`；
- 共享客户端的发布 API，改为写入地址，并在停止或删除实例时自动撤销；
- 独立 `lease_secret`。

## 5. 对现有需求的影响

- 不变：`INCUS-R-054`、`R-062`、`R-064`、`R-065`、`R-070`、`R-071`、`R-086`、`R-087`、`R-088`、`R-095`、`R-096`。
- 改措辞，ID 不动：
  - `INCUS-R-053` 中的「经 Traefik 与受管路由/防火墙」改为「经 Traefik 与租约网桥 ACL」；
  - `INCUS-R-063`（绑定跟随实例生命周期）的执行者写明为共享客户端与 janitor，见草案 I9。
- 可以并入现有条目：草案 I3 并入 `INCUS-R-125`，草案 I10 并入 `INCUS-R-071`。
- 没有需要废弃的条目：逐发布许可、期限和实例身份核验只写在设计文档里，没有进矩阵。

## 6. 拟新增需求

| 拟编号 | 要求 | 验证 |
| --- | --- | --- |
| 草案 I1 | 租约网桥的默认入站动作必须是拒绝：Traefik 地址清单以外的来源（其他 Docker 容器、局域网、其他租约）不能主动连到租约实例；实例主动出站的回包，以及网关上的 DHCP 与 DNS 不受影响 | e2e |
| 草案 I2 | 声明了 `ingress` 的租约，其 ACL 必须只放行 Traefik 地址清单到 `allowed_ports` 的 TCP；未声明时没有这条放行 | 单元 + e2e |
| 草案 I3 | Provider 的 `inspect` 必须把入站规则或默认入站动作与冻结声明不一致的情况判为未就绪，`ensure` 必须把它修复到声明状态 | 单元 |
| 草案 I4 | 入站不得依赖宿主侧的逐发布或逐租约规则：宿主上不得为入站新增防火墙规则、路由或邻居项 | 审阅 + e2e |
| 草案 I5 | 中介只能接受目标地址在本租约 IPv4 网段内、且不是网络地址、网关或广播地址的请求；端口必须在 `allowed_ports` 内 | 单元 |
| 草案 I6 | 中介不得持有 Incus 凭据、租约证书、`lease_secret` 或其他 Secret，不得调用宿主特权动作；它唯一的输出是自己专属子目录里的 Traefik 路由文件 | 审阅 |
| 草案 I7 | 中介必须能从冻结授权与当前请求文件全量重算路由：重启或恢复后删除没有对应有效请求的路由文件并补齐缺失的；停机期间已发布的路由保持可用 | 单元 + e2e |
| 草案 I8 | 部署激活时，Core 必须删除授权被移除或改变（端口、认证、域名）的租约的全部路由文件；CLI 与控制台部署都必须覆盖，且不依赖中介是否在运行 | 单元 + e2e |
| 草案 I9 | 共享客户端停止或删除实例时，必须撤销该实例的全部发布请求；janitor 必须删除实例已不存在的发布请求 | 单元 |
| 草案 I10 | 同一租约内多个请求得到同一域名时，中介只能发布其中一个（已发布的优先），其余拒绝，不得覆盖 | 单元 |
| 草案 I11 | Provider 的 `ensure` 结果必须交回租约网桥的 IPv4 网段与网关，由 Core 记入 resource state | 契约 + 单元 |

## 7. 已知限制

- 消费者崩溃到 janitor 清理之间，旧域名可能指向同一租约里复用了该 IP 的新实例，例如另一个用户的 CI 作业。
  需要互相隔离的工作负载应该放在不同租约；`auth: none` 本来就不得用于敏感或可写服务（`INCUS-R-096`）。
- 撤销单个发布只让新请求返回 404。经 Traefik 已经建立的长连接（例如 WebSocket）会持续到任一方关闭或实例停止。
  租约撤销时实例会被停止，连接随之结束。
- 中介停机期间，新发布不生效。

## 8. 待实机验证

与 M10a 的实机验证合并执行：

1. 默认入站改为拒绝之后，实例主动出站的回包、DHCP 与 DNS 仍然正常。源码显示有 `ct state established,related accept`
   和基线规则，需要实测确认；
2. Traefik 容器能连到租约实例的获批端口：Docker 放行并做 masquerade，ACL 按 Traefik 容器地址（SNAT 之前）匹配；
   其他容器、其他端口被拒；
3. 7.0 LTS 的 nftables 网桥上，入站规则的 `source` 能引用 address set；
4. Traefik 容器重建或重启、地址变化后，address set 更新之前入站中断、更新之后恢复（与 `INCUS-R-118` 的验证合并）。

## 9. 否决的方案

- **保留逐发布许可、去掉期限**：失去失联兜底，却仍要常驻 owner 逐实例增删许可，与 owner 对比中 C1 的问题相同。
- **中介用租约证书向 Incus 核验实例和地址**：解绑可以不依赖消费者，但消费者本来就持有同一张证书、控制租约里的
  全部实例，这项核验挡不住任何它原本做不到的事，只多出凭据交付和网络 I/O。
- **宿主侧为每个租约加一条静态放行规则**（简化评审第 2 项的原建议）：Docker 已经放行容器出站，每个租约网桥上
  也已经有 ACL，宿主规则是多余的一层。
- **Traefik 按 Incus 网桥的 DNS 名访问实例**：Docker 内置 DNS 不能按域名后缀把查询转给各租约网桥的 dnsmasq。

## 10. 需要操作者确认

1. 入站按租约信任：中介接受共享客户端给出的地址，只校验网段，不再核验实例身份；
2. 所有租约网桥默认拒绝入站，随 M10a 一起落地；
3. 中介运行在 anasd 进程内，还是作为 Compose 服务运行；
4. 实施时机。建议先删除旧机制，并随 M10a 落地第 2 条与入站 ACL 规则；中介和共享客户端的改动等第一个声明
   `ingress` 的消费者出现时再做，M11 在此之前保持阻塞。

## 11. 操作者确认与补充（2026-09-28）

- 同意 §10 四点，中介放在 anasd 进程内。
- 入站也分档：
  - a「不接受入站」对应 `none`（默认）；
  - b「绑定特定端口转发到宿主 TCP/UDP」对应 TCP、UDP 发布，经 Traefik 独占宿主端口，档位为 `published`；
  - c「接受全部互联网访问」对应 `internet`；
  - 另补 `lan`：局域网来源可访问任意端口。
- Traefik 转发 HTTPS 与转发 TCP/UDP 端口是平行的功能：发布声明从 `ingress` 段改为 `publish.https`、`publish.tcp`、
  `publish.udp`，档位写在 `network.ingress`。
- 多租约规则与单租约分开定义：租约之间不直连；跨租约只经 Traefik 发布；宿主端口、域名命名空间与网段在租约
  之间唯一；撤销只影响本租约。
- 本轮的编号对照已被 §12 取代。

## 12. 第二轮调整（2026-09-28，同日）

操作者提出四点，结论如下。新编号区间尚未提交，因此直接改写，没有按废弃处理。

1. **以后要支持用户手动创建虚拟机，完全当成主机使用。** 记为预留方向（需求 §2），建立在长驻实例档（M8）之上：
   持久磁盘、固定地址、端口绑定与 HTTP 发布，也可以选 LAN 模式（`INCUS-R-062`）。归属与凭据实施前另定需求。
2. **TCP/UDP 改为实例直接绑定宿主端口，宿主端口可以指定，或 `auto` 从预设范围随机分配。** 采纳，用 Incus
   network forward 实现，Provider 在 apply 时建（Incus 7.0.1 源码 `driver_bridge.go`、network forward 文档）：
   - 转发只能指向固定地址，所以目标是 Provider 为目标实例名预留的固定地址；一次性实例用不了端口绑定；
   - 监听地址是宿主默认路由网卡的地址，每次 apply 重新计算；
   - 7.0.1 的 network forward 按网络占用监听地址，源码的冲突检查只覆盖部分网络，所以规定一个监听地址只属于
     一个租约，用户主机实例放在同一份租约里；
   - DNAT 在本机进程之前生效，要避开宿主上已有进程监听的端口。
   Traefik 的 TCP/UDP 入口不再采用：用户态代理丢失客户端地址，入口增删还要重启 Traefik。
3. **`lan` 档用处不大。** 采纳，删除：宿主在 NAT 之后时，路由器转发或回环进来的流量可能带局域网地址，来源地址
   区分不了局域网与互联网。需要像局域网主机那样访问时，用 LAN 模式。
4. **HTTPS 转 HTTP 是特殊处理，TCP/UDP 是原包转发，应该区分。** 采纳：`publish.http`（七层，Traefik 终止 HTTPS，
   guest 看到网桥网关，真实客户端在 `X-Forwarded-For`，可挂认证）与 `publish.ports`（四层，原样转发，guest 看到
   真实客户端，ANAS 不提供认证）分开声明，分别由 anasd 内的中介与 Provider 生效。

定稿编号对照：I1→`R-130`、I2→`R-133`、I3→`R-136`、I4→`R-137`、I5→`R-147`、I6→`R-146`、I7→`R-148`、
I8→`R-150`、I9→`R-149`、I10→`R-152`、I11→`R-159`。另新增 `R-131`、`R-132`、`R-134`、`R-135`、`R-138`—`R-145`、
`R-151`、`R-153`—`R-158`；废弃 `R-063`，由 `R-149`、`R-151` 取代。里程碑：`R-130` 归 M10a，`R-159` 归 M10b，
`R-131`—`R-138` 归 M11b，端口绑定 `R-140`、`R-143`、`R-153`—`R-158` 归 M11c，其余归 M11。

## 13. 第三轮定稿（2026-09-29）

§12 之后又经实测与讨论，操作者确认以下内容，并要求写进需求与计划：

1. **端口绑定用 ANAS 自写的 Docker 式规则**：固定规则链只接管目的地址是本机的流量（`fib daddr type local`），按端口表
   改写到槽位地址。Incus network forward（只能写具体地址、监听地址按网络占用）与 proxy NAT（要放开 proxy 设备；通配
   监听会劫持宿主外连与转发流量，见[实机探测](2026-09-29-incus-proxy-nat-wildcard-probe.md)）都不采用。
2. **端口绑定对 Module 与用户主机实例都开放**，目标是槽位：槽位在声明里写死实例名，Provider 预留固定 IPv4，租约启用
   IPv6 时另预留固定内网 IPv6；一个槽位可以承载多个端口绑定。一次性实例的动态占用以后再定。
3. **hostd 把关**：操作者在 `incus.configure` 时确认一次端口范围，之后范围内的同步不再逐次确认，hostd 自己重读冻结
   部署并逐条校验，范围外拒绝（`HOSTACT-R-014`、`R-015`）；每个生效的绑定用 systemd 套接字占住端口。
4. **报错**：显式端口被占用时 apply 失败；开机恢复失败与运行中的冲突由 hostd、anasd 记为运行问题，CLI 有查看命令，
   anasd 与控制台显示同一份记录（[运行问题记录要求](../requirements/runtime-issues.md)）。
5. **去掉 `internet` 档**：只剩 `none`、`published`；完全对外开放交给 LAN 模式。

§11、§12 的编号对照已作废：`INCUS-R-130`—`R-163` 尚未提交，按定稿直接改写，以需求矩阵为准。
