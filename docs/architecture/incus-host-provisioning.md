# Incus 宿主供给与镜像烘焙（设计）

> 状态：**设计，未实现**。本文规定 ANAS 如何在自己所在的宿主上装好 Incus daemon、如何自动
> 产出 guest 镜像，以及入站流量怎么走。已实现的部分是 `compute` Contract、`incus` Provider
> Module 与共享客户端；它们当前仍要求运维手工准备 daemon 与镜像，本文正是要消除这一步。

依据是 [Core 实现标准](core-implementation-standard.md) §4「默认可用，高级可替换」：一个只想开
Actions 的用户，不该先去理解 Incus 是什么、更不该先手工装好它。

## 1. 现状与缺口

`incus` Module 有四个 `must_resolve` 参数（endpoint、pinned server 证书、供给用管理证书与
key），全部要运维在别处准备好再粘贴。镜像同理：`modules/forgejo/runner-image/README.md` 要求
在宿主上手工构建并抄下 fingerprint。

按 §4 的判据——「一个只知道自己想要什么服务的用户能不能装上」——**答案是否**。缺的是自动化，
不是文档。

架构本身不必推翻。Provider/客户端分层不变，变的只是「endpoint 与证书从哪来」：默认由 ANAS 为
本机 daemon 自动生成，高级路径仍可覆盖指向远端自建 daemon。

## 2. 支持的发行版

ANAS 目前**完全不做发行版探测**：`install.sh` 只校验 `uname -s = Linux` 与 amd64/arm64，Docker
Engine 与 Compose v2 是用户自备的前置条件。装 Incus 会是 ANAS 第一件需要认识发行版的事。

下表是脚本兼容性的基线。**上游打包情况会变，本表在实现前必须逐条复核**，尤其是「待适配」那几行
——它们的来源是凭记忆写下的，没有对照上游打包核实过。

| 发行版 | 来源 | 分级 |
| --- | --- | --- |
| Debian 13 (trixie) | 官方仓库 | 一级 |
| Ubuntu 24.04 LTS | 官方 `universe` | 一级 |
| Ubuntu 26.04 LTS | 官方 `universe` | 一级，**主要测试环境** |
| Debian 12 (bookworm) | `bookworm-backports` | 待适配 |
| Alpine | `community` | 待适配 |
| Fedora / RHEL 系 | COPR | 待适配 |
| Arch | `extra` | 待适配 |

Ubuntu 22.04 LTS 不在支持范围内：它的官方源没有 incus，只能加第三方源，而为一个即将退出主流
支持的版本引入外部源换不来对应的收益。

分级的含义：

- **一级**：`anas` 自动安装，不添加任何第三方软件源。CI 与发布验收在这三个上跑，
  Ubuntu 26.04 LTS 是主要测试环境；
- **待适配**：尚未实现自动安装。报明确错误、给手工步骤，并让依赖 `compute` 的功能保持关闭——
  不是失败，是保持关闭。适配一个发行版的工作量应当是往映射表里加一行。

发行版到安装步骤的映射必须是**声明式的表**，不是代码里的 `switch`。这与 Core 实现标准 §2
「不得按产品名写业务分支」是同一条规则：新增一个发行版应当是加一行数据。

## 3. 安装机制：一次性、可跳过、留痕

### 3.1 为什么不复用 anas-helper

`anas-helper` 是一个只持有 `CAP_NET_ADMIN`、只调用 `ip(8)` 的窄二进制。装包需要完整 root 与包
管理器，是另一个风险等级。

更要紧的是[特权操作与 helper](privilege-helper-draft.md) 自己的判据——**这次提权是否留下用户
事后要面对的特权产物**。macvlan 桥什么都不留，删掉重建没有区别；而装好的 Incus 会留下一个
常驻的 root daemon 和一份磁盘上的存储池。按那份文档自己的标准，它落在线的另一边，因此是**新
机制，不是 helper 的扩展**。

### 3.2 复用的是同意点，不是代码

`install.sh` 已经在安装期调用 `sudo`：把 `anas-helper` 装进 `/usr/local/lib/anas` 并 `setcap`。
因此「安装时要一次 root」这条路已经存在，加入 Incus 安装**不引入新的提权类别，只是扩大那一次
提权做的事**。

宿主供给步骤必须：

1. **幂等**：重复执行收敛，不重复添加软件源或信任条目；
2. **可跳过**：装不上时不让整个部署失败。`install.sh` 对 helper 已是这个语义（装不上只是让
   host-LAN 模块起不来），Incus 沿用：没有 daemon 就没有 `compute` provider，声明了
   `enabled_by` 的消费者（如 Forgejo Actions）保持关闭；
3. **留痕**：明确记录装了什么、动了哪些系统状态、以及如何卸载。

### 3.3 步骤

```text
1. 读 /etc/os-release，在 §2 的表里查到安装配方；表里没有 → 报错并给手工指引，退出码可区分
2. 安装 incus 包（一级发行版不添加第三方源；待适配的发行版不自动安装，报错并给手工指引）
3. 启用服务，设 core.https_address=127.0.0.1:8443（只监听回环）
4. 初始化存储池（默认 dir 后端，btrfs 可用时优先 btrfs）
5. 生成 ANAS 供给用管理证书，加入信任库
6. 把 endpoint、pinned server 证书、管理证书与 key 写入 anas 能读到的位置
7. 重新执行时校验 3–6 仍然成立，不成立就纠正
```

第 3 步只监听回环是有意的：Provider 与消费者都在同一宿主上，daemon 不需要对外可达。远程管理
是一次独立的加固决策，不是本流程的副产物。

### 3.4 卸载

必须提供与安装对称的卸载：移除 ANAS 的信任条目、移除 ANAS 建的 project/network/profile，并说明
包本身是否移除由用户决定（可能有其他用途）。不得留下用户不知道存在的 root daemon。

## 4. Web 管理端与 root 密码

**不得在 Web 管理端输入 root 或 sudo 密码。** 这不是可用性权衡，是安全边界：

- 密码会经网络到达一个服务、驻留在进程内存里，并可能进日志；
- 它授予的是整台宿主的完全控制权，远超管理控制台被授权的范围；
- 一个向用户索要系统密码的网页表单，与钓鱼在形态上无法区分——即使这一个是真的，它也在训练
  用户对下一个假的降低警惕。

这条约束不意味着「让用户回终端」。正确做法是**安装期一次授权，之后经受限通道执行具名动作**，
设计见[宿主特权动作通道](host-action-channel.md)：用户在命令行安装时给出的那一次 `sudo` 顺带
装好 `anas-hostd` 与它的 socket；此后 Web 控制台与 CLI 用同一条通道触发**编译进二进制的具名
动作**，全程不接触密码，也不接受调用方提供的命令或脚本。

本文 §3.3 的安装步骤就是该通道的首批动作（`incus.install` / `configure` / `enroll` / `status` /
`uninstall`）。在通道实现之前，宿主供给只能由管理员在终端执行，控制台显示待执行命令并轮询状态。

## 5. 入站流量：proxy device，不是公网 IPv6

实例**不需要自己的公网 IPv6 地址**。出站走受管 bridge 的 NAT（IPv4 恒定，IPv6 跟随宿主是否有
全局地址）；入站统一走 Incus **proxy device**，它明确支持 listen 与 connect 使用不同协议族：

```text
listen=tcp:[::]:443   connect=tcp:127.0.0.1:7000
```

因此外部 IPv4 与 IPv6 入站都能到达实例内部的服务，而实例本身只有 NAT 地址。这一条同时解决了
「宿主有 v6 但实例没有」的转发问题，也避免了对上游 DHCPv6-PD 的依赖——家用路由器通常不做前缀
委派，依赖它会让功能在多数目标环境里不可用。

### 5.1 把实例内的 Web 服务经 Traefik 发布

链路：

```text
公网 https://<name>.<base_domain>
  → Traefik（终止 TLS，按 Host 路由）
  → 宿主回环端口（Incus proxy device 监听）
  → 实例内 :7000 的 HTTP 服务
```

**绑定与解绑跟随实例生命周期**，与实例是不是长驻无关：实例启动就绑，停止或暂停就解绑。一个
只跑十分钟的 CI 作业照样可以在这十分钟里有一个可访问的 Web 界面。

机制已经存在，不需要 Traefik 侧新增能力。Traefik 的文件 provider 指向一个**目录**
（`--providers.file.directory=/run/anas`，由 `${ANAS_MODULE_RUNTIME_STATE_PATH}/dynamic` 挂入），
而文件 provider 是**监听目录变化**的：放进一个路由文件就多一条路由，删掉就少一条。因此运行时
绑定/解绑就是写文件和删文件，ANAS Core 不在这条路径上——与整个 compute 设计的其余部分一致。

### 5.1.1 声明语法

授权在 apply 时固定，动作在运行时发生。两层分开：

**apply 时**——租约里声明允许发布什么，这是授权：

```yaml
resources:
  requires:
    - id: runners
      contract: compute
      spec:
        ingress:
          # 允许发布的 guest 端口。固定在 apply 时，被攻陷的消费者无法把任意监听端口
          # 暴露出去。省略 ingress 段即完全不允许发布。
          allowed_ports: [7000]
          domain:
            # fixed：<prefix>.<base_domain>——单实例、地址需要可预测
            # named：<prefix>-<label>.<base_domain>——实例集合固定且需要可读地址
            # random：<prefix>-<派生>.<base_domain>——实例数量不定、按任务动态产生
            mode: random
            prefix: ci
```

**运行时**——消费者经共享客户端发布和撤销，与 start/stop 成对：

```go
// mode: random 时不给 label，域名由 workload_id 派生（§5.1.3）
addr, err := client.PublishPort(ctx, instanceID, 7000, computeclient.PublishOptions{})

// mode: named 时给一个 label，域名是 <prefix>-<label>.<base_domain>
addr, err := client.PublishPort(ctx, instanceID, 7000,
    computeclient.PublishOptions{Label: "api"})

err = client.UnpublishPort(ctx, instanceID, 7000)        // 删路由请求 + 删 proxy device
```

`PublishPort` 在动手前校验：端口在 `allowed_ports` 内、实例属于本租约、`label` 只含
`[a-z0-9-]` 且与租约的 `mode` 一致（`random` 下给 label、`named` 下不给 label 都是错误）。

**这些校验是给正确的消费者用的，不是安全边界。** 被攻陷的消费者可以绕过库直接写请求文件，
真正的强制点在租约之外的中介，见 §5.1.5。

### 5.1.2 一个 Module 起多个实例时怎么分域名

三种模式覆盖三类形态，选哪一种取决于**实例集合是不是事先已知**：

| 模式 | 域名 | 适用 | 多实例 |
| --- | --- | --- | --- |
| `fixed` | `<prefix>.<base_domain>` | 单实例、地址要可预测（写进书签、配到别的系统里） | **不支持**——同一租约内只能有一个发布实例 |
| `named` | `<prefix>-<label>.<base_domain>` | 实例集合固定且需要可读地址，例如一个 Module 同时起 `web` 与 `api` | 支持，`label` 由消费者在发布时给出 |
| `random` | `<prefix>-<派生>.<base_domain>` | 实例按任务动态产生、数量不定，例如每个 CI 作业一个 | 支持，天然不重复 |

`named` 的 `label` 由消费者提供，因此它必须被校验：**只允许 `[a-z0-9-]`，且最终域名必须落在该
租约的 `prefix` 命名空间内**。否则一个消费者可以用 `label` 拼出别的服务的域名——见 §5.1.5。

`random` 是多实例的默认答案，因为它不需要消费者维护「哪个实例叫什么」这张表。

### 5.1.3 随机域名必须是派生的，不是抽出来的

要求是：不同实例不重复，**同一个任务的实例保持不变**。这两条合起来排除了「生成随机数再存一张
表」——存表要处理并发、清理和丢失，而且重启后要能对上。

正确做法是**从任务标识确定性派生**：

```text
<prefix>-<hex(hmac(lease_secret, workload_id))[:10]>.<base_domain>
```

- 同一个 `workload_id` 永远得到同一个名字，无需存储；
- 不同任务得到不同名字，碰撞概率由摘要长度决定；
- 因为掺了租约自己的 secret，外部无法从任务 id 预测出域名，也无法枚举。

仍然把结果记进 resource state，但那是**为了可观测**，正确性不依赖这条记录。

`lease_secret` 是一把 HMAC 派生密钥，不是凭据；它进 Secret Store 的理由是「必须被稳定记住且不该
进日志」，不是「它是密码」。见 §5.1.3.1。

规则写死如下：

1. 名字**必须**由 `workload_id` 与租约 secret 派生，不得抽取随机数；
2. 派生**必须**是确定性的：同一 `workload_id` 在任何时候、任何进程里得到同一个名字；
3. 摘要**必须**掺入租约自己的 secret，使外部无法从任务 id 反推或枚举域名；
4. 截断长度需保证同一租约内的碰撞概率可忽略；发生碰撞时必须失败，**不得**静默复用他人域名；
5. `mode: fixed` 时不派生，直接用 `prefix`——它适用于单实例、地址需要可预测的场景，代价是同一
   租约内不能同时存在两个发布实例。

这一条能成立还有个前提：ANAS 的证书是**通配符**证书（`lego` Module 签发 `*.<base_domain>`），
所以任意子域都不需要为每个实例单独签证书。

### 5.1.3.1 `lease_secret` 不是密码，但仍然进 Secret Store

**它不是凭据。** 它不认证任何东西，也不授予任何权限——它是一把 HMAC 密钥，唯一的作用是让派生出
的域名从外部**不可预测、不可枚举**。域名本身完全公开：出现在地址栏、TLS SNI、Traefik 配置和访问
日志里。

那么泄漏它的代价到底是什么？准确地说只有一条：**攻击者可以在没见过域名的情况下算出域名。** 这
只有在「服务本身没有认证、靠地址猜不到来挡人」时才构成风险。

#### 因此更要紧的是：发布出去的服务不能靠域名难猜来保护

TLS 的 SNI 是明文，URL 会进 `Referer`、代理日志和浏览器历史。**任何在网络路径上的人、或者拿到
过一次链接的人，都知道这个域名。** 所以不可预测只挡得住扫描与枚举，挡不住上述任何一种。

结论是硬的：**经 §5.1 发布的服务默认必须挂 ForwardAuth**（仓库已有 `forward_auth` capability 与
`ANAS_FORWARD_AUTH_MIDDLEWARE`，`adminer` 一类内部页面就是这么挡的）。要发布一个无认证的服务
必须是显式的、写在租约里的选择，而不是默认行为。

在这个前提下，`lease_secret` 的作用回到它应有的位置：**纵深防御的一层**，泄漏它不构成越权。

#### 存哪里：和数据库密码同一条路

理由不是「它是密码」，而是这条路免费提供了四件本来要各自实现的事：

```text
apply 时  a.secrets.Ensure(<该资源的 secret key>, 生成 32 字节随机值)
             │  存进 Secret Store（.anas/secrets.yml）
             ▼
投影      ANAS_COMPUTE_RESOURCE__<MODULE>__<ID>__DOMAIN_SECRET   标记敏感
             │
             ▼
resource state 里只留 **Secret Store 的引用**，不留明文
```

1. **跨 apply 稳定**——`Ensure` 已有则不重铸。这条是功能性的：换了 key，所有已发布的域名会集体
   改变；
2. 进备份与恢复，恢复出来的部署域名不变；
3. 纳入既有的轮换工具；
4. 敏感标记带来的日志与 `config list` 脱敏。

第 1 条本身就足以让它进 Secret Store——它必须被稳定地记住，而 Secret Store 正是「稳定记住一个
不该出现在日志里的值」的那个地方。

**不能塞进客户端证书那个 bundle。** 那是 `base64(PEM 证书 + PEM 私钥)`；并进去之后，证书轮换会
连带改掉所有已发布的 URL——而证书轮换是一次安全操作，不该顺手把线上地址全换掉。分开存正是为了
让两种轮换互不牵连。

**也不能由部署级 secret 派生。** 消费者需要自己算域名（`workload_id` 是运行时才有的），密钥必须
交到消费者手里；一个部署级密钥交给每个消费者，等于每个消费者都能预测别人的域名。

### 5.1.4 与 Incus API 的区别

本节发布的是实例内的普通 HTTP 服务，Traefik 终止 TLS 完全正常。**Incus API 是另一回事**：它用
mTLS 客户端证书认证，Traefik 终止 TLS 会打断它，且暴露它等于暴露整个控制面，不在本设计范围内。

### 5.1.5 注册路由：Traefik 没有写入 API，文件 provider 就是那条动态通道

**Traefik 的 HTTP API 是只读的**，没有「POST 一条 router 进去」这种端点。它的配置只能来自
**provider**：Docker label、文件 provider、KV store（Consul/etcd/Redis）、Kubernetes CRD。

对这里可用的只有文件 provider——而 ANAS 已经在用它，且**它本身就是动态的**：

```text
--providers.file.directory=/run/anas    # 一个目录，Traefik 监听其变化
```

放进一个文件 = 多一条路由，删掉 = 少一条。所以「消费者自助注册域名」不需要 Traefik 侧任何新东西，
写文件和删文件就是注册与注销。Docker provider 用不上（Incus 实例不是 Docker 容器），KV store 要
多跑一个存储，不值得。

#### 但「谁能往那个目录里写」是一个真实的洞

今天只有 Traefik Module 自己的 entrypoint 写那个目录。如果让消费者直接写**原始 Traefik 动态
配置**，一个被攻陷的消费者可以：

- 定义一条 `Host(nextcloud.example.com)` 的 router，**劫持另一个服务的域名**；
- 定义一条不带 ForwardAuth middleware 的 router，**绕开某个服务的认证**。

而消费者容器正是我们从一开始就假设可能被攻陷的那一方——整个租约围栏就是为此存在的。所以这里
必须有中介，不能让消费者写原始配置。

#### 形态：受约束的请求文件 + 校验后渲染

```text
消费者写入：  <lease 专属目录>/<label>.yml      ANAS 自己的小 schema
                 { host_label, target_port, instance_id }
                          │  校验
                          ▼
渲染出：      /run/anas/<...>.yml               真正的 Traefik 动态配置
```

校验的核心只有一条：**请求的域名必须落在该租约被允许的命名空间内**——由 `prefix` 与租约 secret
决定（§5.1.3）。消费者拿不到别的租约的 secret，也就拼不出别人的域名。middleware 与 entrypoint
由渲染方决定，消费者无权指定。

中介放在 **Traefik Module** 里最自然：它已经拥有那个目录，而且本来就是一个常驻服务，加一个小
watcher 不引入新的活动部件。把校验放进共享客户端库是不够的——被攻陷的消费者可以绕过库直接写文件。

### 5.1.6 LAN 模式若将来启用：仍然走 proxy device

macvlan 有一条对两档都成立的性质：**子接口与父接口不能互相通信**——宿主与挂在同一物理网卡上的
macvlan 实例互相到不了。而 Traefik 跑在宿主上。于是 LAN 模式下有两条候选路径：

| | 复用 macvlan shim | proxy device（选定） |
| --- | --- | --- |
| 机制 | 复用 `HOST_LAN_BRIDGE_IP` / `VLAN_BRIDGE_IP` 在宿主侧建的 macvlan shim，Traefik 经它访问实例的 LAN 地址 | 与 NAT 模式相同：实例端口经 proxy device 发布到宿主回环，Traefik 指向 127.0.0.1 |
| 已有实现 | 有（anas-helper） | 有（Incus 原生） |
| 路由目标 | 实例的 LAN 地址，**由路由器 DHCP 分配，会变** | 固定的宿主回环端口 |
| 与网络模式的耦合 | 只在 LAN 模式可用，NAT 模式要另一条路径 | 两种模式完全一致 |
| 协议范围 | 任意协议 | 只覆盖显式发布的端口 |
| 额外一跳 | 无 | 有 |

**选 proxy device。** 决定性的理由是最后两行之外的那一行：**入站路径与网络模式解耦**。
`PublishPort` 只有一种实现、一处要测；把租约从 `nat` 切到 `lan` 不改变 Web 服务怎么发布。

复用 shim 看似省事，但它把 Traefik 的路由目标绑在一个 DHCP 分配、随时可能变的地址上，需要额外的
地址发现与重新绑定逻辑——而这套逻辑只在 LAN 模式下存在，是纯增量。

macvlan shim 继续解决它本来解决的问题（宿主与 LAN 模式实例的一般可达性），只是不再承担 Traefik
入站这一条。多出来的那一跳在回环上，代价可以忽略。

### 5.2 预留：接入 LAN（暂不考虑）

> [!NOTE]
> **本节暂不纳入实施范围。** NAT + proxy device 已经覆盖出站与入站两个方向，LAN 模式解决的是
> 「实例要在局域网里有自己的身份」这个另外的问题，当前没有需求推着它走。本节保留为设计备忘，
> 不排期。


除 NAT + proxy device 之外，预留第二种网络模式：**把实例直接接到宿主所在的 LAN**。此时地址由
LAN 里的路由器统一管理，IPv4 与 IPv6 都由它下发，ANAS 既不需要 DHCPv6-PD，也不需要自己做前缀
规划。

它可以用受管 macvlan 网络实现（`incus network create <n> --type=macvlan parent=<iface>`），因此
不必放宽 project 上的 `restricted.devices.nic: managed`。

**但它改变的是两个协议族，不是 v6 一个**：实例上了 LAN 之后，v4 与 v6 都直接暴露给局域网上所有
设备，围栏对两边同时消失。所以它是一个明确的隔离模式选择（`network_mode: nat | lan`，默认
`nat`），不是一个「IPv6 开关」。

现有的 macvlan 可行性检测可以复用——`detectHostNetwork` 取网关/接口/网段、
`checkLANAddressConflicts` 做 ARP 探测、`validateMacvlanPlan` 校验地址在网段内且不撞宿主与网关
——但这三处目前**全是 IPv4**（`validateMacvlanPlan` 直接 `.To4()`，网段算术是 uint32，探测用
ARP）。v6 需要 NDP，是新代码。

### 5.3 与租约边界的关系

发布端口是**入站**能力，当前一次性沙箱档不提供。它属于[已预留的长驻实例档](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/incus-module.md)：
稳定入站必须由受管 network 显式声明，消费者不得直接持有宿主端口。proxy device 是实现手段，
声明与授权仍归 Provider。

## 6. 镜像烘焙：distrobuilder，构建一次记摘要

guest 镜像不能要求用户手工构建。通用形态与 `compute` 同构：一个 Contract 的 Provider 在 apply
时保证「这个摘要的镜像存在」，并把 fingerprint 交给消费者。

**配方用 [distrobuilder](https://github.com/lxc/distrobuilder)**，不自造 Dockerfile 方言。它是
Incus 生态的原生工具，配方本身就是 YAML 的「装包 / 拷文件 / 执行动作」，正是这里需要的三样；
自造一层只会多一次翻译。

### 6.1 可复现性是硬约束

`apt install foo` 今天和明天装出来的不是同一份字节，镜像摘要会跟着变——而整个
`image_allowlist` 的意义就在于摘要钉死。因此语义必须是**构建一次、记录摘要、之后不再重建**：

- apply 时先查该摘要的镜像在不在，在就什么都不做；
- 不在才按配方构建，构建完记录产出的 fingerprint；
- 配方变更是一次显式的版本变更，产出新摘要，而不是就地覆盖旧摘要。

「每次 apply 都重新烘焙」是错的：它让钉死失去意义，也让每次部署时间不可预测。

### 6.2 镜像引用语法：命名，而不是回传摘要

早先的设想是 Provider 构建完把 fingerprint 交回 Runner，这需要一条现在不存在的通道
（`ensureResourcesFor` 只记录 Runner 自己传进去的值，Provider 的输出除退出码外不被读取）。

**更简单的做法是给镜像命名**，让引用本身就是标识：

```yaml
image_allowlist:
  - anas:ubuntu26.04-forgejo-runner@r3     # ANAS 发布的镜像，按名字引用
  - fingerprint:a1b2c3...（64 位十六进制）  # 显式钉死某个摘要
```

两种前缀，语义不同但都不可变：

| 前缀 | 含义 | 谁保证不可变 |
| --- | --- | --- |
| `anas:` | ANAS 自己发布的镜像，名字里带 revision | ANAS 的发布纪律 |
| `fingerprint:` | 内容摘要 | 摘要本身 |

这样就**不需要 Provider→Runner 的结果通道**：名字在 apply 时就是确定的，Provider 只负责保证
「这个名字对应的镜像存在」。

代价是 `anas:` 名字的不可变性由纪律而不是密码学保证，因此规则必须是硬的：

> **`anas:` 名字一旦发布就不得指向不同内容。配方变更产出新 revision（`@r4`），不覆盖旧名字。**

这与仓库现有的容器镜像纪律是同一条——`anas-forgejo:15.0.7-r1` 也从不被重新指向别的构建。
Provider 在解析 `anas:` 名字后，仍然把它归一成真实 fingerprint 再传给 daemon，因此运行时钉死的
仍是摘要。

### 6.3 旧 revision 的清理：显式 prune，不自动删

每次配方变更都产出新 revision（`@r3` → `@r4`），旧镜像会占盘——一个 Incus 镜像是 GB 量级，累积
起来不小。但**不能在 apply 时自动删**，两个理由：

1. 可能还有实例正基于旧镜像在跑；
2. ANAS 把回滚当一等能力。上一个 deployment 引用的镜像被删掉，回滚就失败了——而回滚正是出问题
   时最需要它能用的时刻。

因此规则与 Resource 的 `deletion_policy: retain` 一致——**移除声明不隐式删除数据**：

- 当前 deployment 与上一个 deployment 引用到的镜像一律保留；
- 清理是**显式动作** `incus.image-prune`，先 dry-run 列出将删什么，确认后再删；
- 它是破坏性动作，走 §3.4 的二段确认。

自动删和手动删之间的这条线，与仓库其他地方是同一条：ANAS 从不因为「你不再声明它」就删掉你的
数据，删除必须是有人明确说要删。

## 7. 待决

- 随机域名派生用的 `lease_secret` 的轮换入口（与客户端证书分开轮换意味着多一条轮换路径）。

§2 标记为「待适配」的发行版**不是待决，是排期**：先把一级三个做完，其余作为后续计划，届时再
对照上游打包逐条核实。未适配时的行为已经定了——不自动安装、报错给手工指引、依赖 `compute` 的
功能保持关闭（`INCUS-R-049`）。

`incus_vm` 与 `incus_container` **都支持 macvlan**：Incus 对容器直接挂 macvlan 子接口，对 VM 用
tap/virtio-net 背靠 macvlan，两档的 NIC 声明形状一致。上面那条父子不可通信的限制对两档同样成立，
它是 macvlan 本身的性质，不是某一档的。
