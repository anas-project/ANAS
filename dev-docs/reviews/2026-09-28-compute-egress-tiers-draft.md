---
doc_type: review
status: current
created: 2026-09-28
updated: 2026-09-28
---

# compute 租约出站分级草案

状态：已采纳（2026-09-28），写入 [Incus 要求](../requirements/incus-module.md) §7sexies 与 `INCUS-R-112`—`R-129`，
M10a 已重排；本文保留为设计过程记录，以需求矩阵为准。原状态：设计与需求草案，未进矩阵，也没有改动实现。确认后，它会取代
[重新授权与增量续期草案](https://github.com/anas-project/ANAS/blob/9a6921a1/dev-docs/reviews/2026-09-28-incus-forwarding-regrant-renewal-draft.md)，以及运行 owner 对比中的逐实例
许可方向。基线：`dddae0c2` 加当前工作树。简化背景见[Incus 设计简化评审](2026-09-28-incus-design-simplification-review.md)。

## 1. 已定的前提（操作者，2026-09-28）

- 出站按租约放行，不按实例放行；
- 分四档，默认 a 档，由申请实例的 Module 在租约声明中选择；
- 申请实例的 Module 自己决定，它的实例能否访问 ANAS 的其他 Module；
- 分级面向所有 compute 消费者，而不只是 Forgejo。

同日追加的确认：

- d 档的理解正确：只能经 Traefik 访问 ANAS 的 Module；
- 租约网桥换一个专用前缀（§5 第 2 项）；
- 同一租约内实例之间的互访设开关（§5 第 5 项）；
- 局域网按「全部私有地址减去 Docker 与 Incus 网段」来界定，操作者对容器启动顺序的影响提出疑问，见 §5 第 3 项；
- 入站另开对话讨论。

## 2. 分级

| 档 | 拟用值 | 允许 | 仍然拒绝 |
| --- | --- | --- | --- |
| a（默认） | `internet` | 公网 | 局域网、宿主地址（含 Docker 发布到宿主的端口）、其他租约、Docker 网络、链路本地与元数据地址 |
| b | `internet_lan` | a + 局域网 | 宿主地址、其他租约、Docker 网络、链路本地与元数据地址 |
| c | `internet_lan_host` | b + 宿主地址上的端口 | 其他租约、Docker 网络（发布端口除外）、链路本地与元数据地址 |
| d | `modules_only` | 只能经 Traefik 访问 ANAS 的 Module | 其余全部出站 |

另设一个与档位独立的开关，拟名 `module_access`，默认关闭。打开后，a、b 两档的实例也可以经 Traefik 访问 ANAS 的
Module。c 档本来就能访问宿主端口；d 档可访问的就只有这一项。

各档都允许：本租约网桥网关上的 DNS 和 DHCP（由 Incus 内建规则放行）。

同一租约内实例之间的互访，由另一个开关控制，拟名 `intra_lease`，默认关闭，即实例之间互相隔离。原因是 one-job
这类场景下，同一租约里同时运行的作业不应互相访问。

## 3. Forgejo 的最佳实践

Forgejo 官方的 [Securing Forgejo Actions Deployments](https://forgejo.org/docs/latest/admin/actions/security/) 要点：

- runner 本质上就是远程代码执行，要按不可信代码来对待；
- 建议使用 ephemeral runner，每个 runner 最多执行一个作业（ANAS 已经是 one-job）；
- 不要让作业使用宿主网络，否则作业能绕过反向代理，直接访问宿主上只监听本机的服务；
- 「Local Network Resources」一节：只要能执行任意工作流，局域网资源就暴露给了它，建议隔离 runner 所在网段，
  并在防火墙上限制跨网段访问；
- 没有要求限制对公网的访问。

对应到本分级：Forgejo 推荐的姿态就是 **a 档 + `module_access`**（能访问 Forgejo 本身）。它和默认档一致，不需要
单独成为一档。ANAS 现有 [Forgejo 设计](../../docs/architecture/forgejo-module-design.md) §4.3 写的是「出站只允许
Forgejo HTTPS、DNS/NTP 和明确批准的镜像源」，比官方建议更严。采纳分级后，这句改为「a 档 + `module_access`」。

## 4. 声明与执行

- **声明**：compute 资源声明新增 `network.egress`（取值 a/b/c/d 对应的名字）和 `network.module_access`，随 apply
  冻结进部署。改档位就是一次普通的部署变更，走部署本身的计划与确认。
- **执行**：
  - Provider 把档位写进租约网桥的 Incus network ACL，与现有来源围栏共用同一份 ACL；由 `inspect` 检测漂移、
    `ensure` 修复。
  - 宿主侧只需要一条静态规则：`incus.configure` 时在 FORWARD 末尾追加「来自租约网桥的转发交给 Incus ACL 决定」，
    卸载时删除。它排在 Docker 自己的规则之后，Docker 的拒绝仍然先生效。之后不再有逐租约或逐实例的宿主动作。
- **撤销**：租约撤销（`INCUS-R-111`）时，Provider 把 ACL 改为全部拒绝，并停止该租约的实例，已建立的连接随实例
  停止而结束。这一步只调用 Incus API，CLI 与控制台两条部署路径都能覆盖。
- **结果**：
  - 不需要续期，出站方向也不需要常驻 owner；
  - 实例增减不用处理；
  - 部署之后不需要重新授权，因为档位本身就是部署声明的一部分。

## 5. 待定的实现细节

1. **DNAT 与 ACL 的时机**：guest 访问「宿主 IP:443」时，Docker 会在路由之前把目的地址改写成 Traefik 容器的地址，
   也就是 DNAT。Incus ACL 在转发阶段只看得到改写之后的地址。所以规则要按改写后的地址来写：
   - Docker 发布到宿主的端口会落在 Docker 网段，a、b 档因为拒绝 Docker 网段而自然拒绝它们；
   - `module_access` 放行 Traefik 的固定地址，因此 Traefik 要固定在一个专用网段的固定地址上；
   - c 档对「宿主发布端口」的放行要落到 Docker 网段，只放行已发布的端口，这一点依赖 Docker 自己的转发规则。
     不同 Docker 版本在这里的行为有差异，需要实机核对。
2. **租约网桥前缀（已定：换专用前缀）**：新前缀不能以 `anas` 开头，因为 anas-helper 可以操作所有 `anas*` 接口。
   拟用 `cmpt` 加 10 位十六进制，共 14 个字符，在接口名 15 个字符的上限之内。compute 还没有生产使用，旧名字的租约
   重建即可。
3. **局域网的定义与启动顺序**：
   - ACL 是按每个报文判断的，实例和容器谁先启动不影响结果；影响结果的是规则里的地址清单是否还是最新的。
   - 如果按「全部私有地址减去当前的 Docker 与 Incus 网段」在 apply 时算出一份清单，之后新建的 Docker 网络不在排除
     清单里，就会被当成局域网：b、c 档反而能访问到它。问题出在放得过宽，而不是访问不到。
   - 要让这个定义不受时间影响，有两种做法：一是固定 Docker 的地址池、按地址池排除，这要改 Docker 的全局配置并
     迁移现有网络，而且 Docker 默认地址池包含 192.168.0.0/16，和常见家用局域网重叠；二是正向定义局域网为「宿主
     默认路由所在网卡的直连网段，加上操作者配置的附加网段」，Docker 网络永远不会进入这份清单。建议用第二种。
   - 清单用 Incus 的 network address set 维护，ACL 引用它；Provider 在 apply 时刷新，`inspect` 检查漂移。
     address set 要求网桥使用 nftables 驱动，需要在 7.0 LTS 上核实。
4. **IPv6**：各档对 IPv6 同样适用；Docker 的 ip6tables 也有同样的默认 DROP。
5. **同一租约内的实例互访（已定：设开关）**：拟用 `intra_lease`，默认关闭，靠 lease profile 里网卡的
   `security.port_isolation` 实现，需要实机验证。

## 6. 对现有需求与实现的影响

- `INCUS-R-101`—`R-104`（逐实例许可、每次续期核验、逐实例撤销与退役）废弃。`INCUS-R-105`（保留管理员显式拒绝
  和 Docker 默认策略、不把 FORWARD 改成全局放行）保留，改为约束这条静态规则。
- 逐实例许可的实现停用后删除，包括：`internal/incusingresshost/forwarding_*`、`internal/incusprovision/forwarding_*`、
  转发相关的宿主动作，以及部署前的转发撤回。M10a 按新需求重排。
- 出站方向不再需要运行 owner；运行 owner 问题只剩 M11，见简化评审。

## 7. 拟新增需求

| 拟编号 | 要求 | 验证 |
| --- | --- | --- |
| 草案 E1 | compute 租约必须声明出站档位；未声明时取 a 档；档位随部署冻结，运行时消费者不得更改 | 契约 + 单元 |
| 草案 E2 | a 档实例必须能访问公网地址，且不能访问局域网、宿主地址（含发布到宿主的端口）、其他租约、Docker 网络与链路本地地址 | e2e |
| 草案 E3 | b 档实例在 a 档基础上必须能访问局域网，其余拒绝项不变 | e2e |
| 草案 E4 | c 档实例在 b 档基础上必须能访问宿主地址上的端口，其余拒绝项不变 | e2e |
| 草案 E5 | d 档实例只能经 Traefik 访问 ANAS 的 Module，其余出站全部拒绝 | e2e |
| 草案 E6 | `module_access` 打开时，a、b 档实例必须能经 Traefik 访问 ANAS 的 Module；关闭时必须不能 | e2e |
| 草案 E7 | 各档都必须允许访问本租约网桥网关上的 DNS 与 DHCP | e2e |
| 草案 E8 | 租约撤销后，该租约实例的新出站连接必须被拒绝，已建立的连接必须结束 | e2e |
| 草案 E9 | Provider 的 `inspect` 必须把 ACL 与所声明档位不一致判为未就绪，`ensure` 必须把它修复到声明档位 | 单元 |
| 草案 E10 | `intra_lease` 关闭时，同一租约内的实例之间必须不能互相访问；打开时必须可以 | e2e |
| 草案 E11 | 在实例启动之后才创建的 Docker 网络，不得因此变成 b、c 档可访问的局域网 | e2e |
