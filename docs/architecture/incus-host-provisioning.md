# Incus 宿主供给与镜像烘焙（设计）

> 状态：**提案，未实现**。更新：2026-09-10。本文规定 ANAS 如何在自己所在的宿主上装好 Incus daemon、如何自动
> 产出 guest 镜像，以及入站流量怎么走。已实现的部分是 `compute` Contract、`incus` Provider
> Module 与共享客户端；它们当前仍要求运维手工准备 daemon 与镜像，本文正是要消除这一步。

需求来源是 [Incus 要求矩阵](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/incus-module.md)
R-047—R-055、R-057、R-062—R-067、R-070—R-072、R-083—R-088、R-092、R-094—R-096；
轮换边界以凭据轮换要求 CRED-R-007/CRED-R-015 为准。下文命令、字段与 Go API 示例均为
目标设计，**当前不可直接执行或用于现有 schema**；已实现部分以 compute Contract 源文件为准。

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

第 3 步规定 daemon 的监听边界，但尚未解决客户端连接路径。当前 Provider、消费者和 Traefik
使用 Docker bridge 网络；容器里的 `127.0.0.1` 不指向宿主。`host.docker.internal` 指向宿主
网关，也不能直接到达仅绑定宿主回环的监听。不能以“同一宿主”推导可达，见 §3.5。

### 3.4 卸载

必须提供与安装对称的卸载：移除 ANAS 的信任条目、移除 ANAS 建的 project/network/profile，并说明
包本身是否移除由用户决定（可能有其他用途）。不得留下用户不知道存在的 root daemon。

### 3.5 宿主回环与容器连接路径（待定案）

需分别解决两条路径：消费者/Provider 到 daemon 的 mTLS 控制连接，以及 Traefik 到实例发布端口
的数据连接。现有 Compose 不提供这两条回环访问能力。

| 方案 | 影响 | 本轮判断 |
| --- | --- | --- |
| 相关容器使用宿主网络 | 可访问宿主回环，但扩大容器网络权限并影响现有服务发现、端口和隔离 | 不作为隐式修复，需专项评估 |
| 受管的受限转发路径 | 保持 daemon 回环监听，但增加连接入口及访问控制、生命周期和审计职责 | 候选，尚未定案 |
| daemon 改为对外监听 | 改变默认安全边界 | 不符合默认路径要求 |

定案前需用真实 Linux Docker 网络证明：目标容器可达，其他容器和 LAN 不获得未授权入口，
mTLS pin 保持有效，停止/卸载不留转发。远端自建 daemon 继续作为显式配置路径，不能掩盖本机默认
路径的缺口。安装状态需区分未安装、外部已有、ANAS 受管、部分失败、可用和已卸载；资源所有权清单
决定重试与卸载范围，不能接管或删除原有非 ANAS 资源。

### 3.6 推荐候选：固定目的地的宿主传输服务

此方案保留 bridge 网络与 daemon 回环监听，不更换消费者证书，也不终止 Incus TLS。
它是实现候选，须通过下面的验证后才能成为默认路径。

```text
消费者/Provider（独立客户端证书）
  → 仅供授权容器接入的宿主高位端口
  → 非 root 传输服务（固定转发到 127.0.0.1:8443）
  → Incus daemon（原有 mTLS/project 限制）
```

传输服务不读管理证书、不提供 HTTP CONNECT/SOCKS、不接受请求指定目的地址；TLS 字节原样转发，
由 daemon 检查客户端身份，共享客户端继续检查 pinned server certificate。独立网络用于收窄
可达面，不能取代 mTLS。服务使用单独非特权账号，不授予 Docker socket、Incus Unix socket 或
额外 capabilities；二进制与固定配置由安装流程管理，消费者不可写。

安装与撤销接入 `anas-hostd` 的 configure/uninstall 设计；非 root 转发进程不是第三个特权入口。
新增受管服务仍需按 HOSTACT-R-012 评审：运行不需 root，安装网络限制需既有宿主通道；留下服务
单元、专用网络连接与监听；卸载撤销这些资源；部分失败不宣告 compute 可用，重试按资源所有权收敛。
具体宿主动作的参数与清单变更需在宿主通道计划中另行登记，本文不假定当前已经存在可调用动作。

数据连接与此控制连接分开：Traefik 后端不得复用一个能任意转发的通用端口。发布路径需在 proxy
权限问题解决后，由受信组件生成有限的后端映射；不能把消费者传入的 IP/URL 作为转发目标。

验收应覆盖：合法证书成功、错误 pin 失败、跨 project 失败、非授权网络不可达、LAN 不可达、
服务重启后的连接失败可归因、半配置失败可重试、卸载清除全部监听。任一项不满足，不通过把 daemon
改绑公网或挂入 root Unix socket 来兜底。

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

实例**不需要自己的公网 IPv6 地址**。出站走受管 bridge NAT；入站目标仍是 proxy device。
但两档不能套用同一份设备配置：截至 2026-09-10，[Incus proxy 官方文档](https://linuxcontainers.org/incus/docs/main/reference/devices_proxy/)
说明容器支持 NAT 与非 NAT，VM 仅支持 NAT。下面的跨协议族连接是容器非 NAT 路径的示意，不能
作为 VM 验收依据，也不是直接占用公开 443 端口的部署配置：

```text
listen=tcp:[::]:443   connect=tcp:127.0.0.1:7000
```

对外双栈入口由 Traefik 承担；它到受管后端可以使用独立选定的协议族。VM 的 NAT proxy 目标
应是受管 NIC 地址，不能照抄 guest 回环地址。具体监听地址、端口分配和后端可达路径仍须与 §3.5
一起定案，并分别测试容器和 VM；不得依赖公网 guest IPv6 或上游 DHCPv6-PD。

### 5.1 把实例内的 Web 服务经 Traefik 发布

链路：

```text
公网 https://<name>.<base_domain>
  → Traefik（终止 TLS，按 Host 路由）
  → 受管后端连接路径（容器到宿主的连接方式待 §3.5 定案）
  → Incus proxy device
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
          # none：不加认证，域名不可预测是唯一屏障——它不是访问控制，见 §5.1.3.1
          # forward_auth：挂 ForwardAuth 中间件，需要部署里有 forward_auth provider
          auth: none
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

err = client.UnpublishPort(ctx, instanceID, 7000)        // 提交撤销，由受信方删路由与 proxy
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
| `random` | `<prefix>-<派生>.<base_domain>` | 实例按任务动态产生、数量不定，例如每个 CI 作业一个 | 支持，确定性派生；碰撞时失败 |

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

#### 域名难猜不是访问控制，认证由消费者声明

TLS 的 SNI 是明文，URL 会进 `Referer`、代理日志和浏览器历史。**任何在网络路径上的人、或者拿到
过一次链接的人，都知道这个域名。** 所以不可预测只挡得住扫描与枚举，挡不住上述任何一种。

但**默认强制 ForwardAuth 是错的**，理由不在安全，在依赖：ForwardAuth 需要部署里存在
`forward_auth` provider，也就是需要先装一个 IAM。把它设为默认，等于「预览一个 CI 作业的页面之前
先装身份系统」——与 [Core 实现标准](core-implementation-standard.md) §4「默认可用」直接冲突。

所以认证是**租约里的一项声明，默认 `none`**：

```yaml
        ingress:
          allowed_ports: [7000]
          auth: none            # none | forward_auth，默认 none
          domain:
            mode: random
            prefix: ci
```

它声明在 apply 时，不是运行时逐次发布时选择——否则被攻陷的消费者可以自己把认证关掉。一个 Module
若确实既要发布带认证的管理页、又要发布公开预览页，应当持有**两份租约**，而不是让 `PublishPort`
接受一个认证参数。

`auth: none` 的含义必须写在它旁边，不能只写在别处：

> **此时域名的不可预测性是唯一的屏障，而它不是访问控制。** 任何看到过这个 URL 的人、以及任何
> 在网络路径上观察 SNI 的人，都能访问该服务。不要用 `auth: none` 发布任何包含敏感数据、或带有
> 写入能力的服务。

在这个前提下，`lease_secret` 的作用是清楚的：**它降低被扫描到的概率，不构成访问控制**。泄漏它
不产生越权，只是让本来就不该公开的东西更容易被找到。

#### 存哪里：和数据库密码同一条路

这条路径复用既有存储，但需分别落实以下生命周期行为：

```text
apply 时  a.secrets.Ensure(<该资源的 secret key>, 生成 32 字节随机值)
             │  存进 Secret Store（.anas/secrets.yml）
             ▼
投影      ANAS_COMPUTE_RESOURCE__<MODULE>__<ID>__LEASE_SECRET   标记敏感
             │
             ▼
resource state 里只留 **Secret Store 的引用**，不留明文
```

1. **跨 apply 稳定**——`Ensure` 已有则不重铸。这条是功能性的：换了 key，所有已发布的域名会集体
   改变；
2. 进备份与恢复，恢复出来的部署域名不变；
3. 使用独立的专属轮换命令，排除凭据轮换和 `--all`；
4. 敏感标记带来的日志与 `config list` 脱敏。

第 1 条本身就足以让它进 Secret Store——它必须被稳定地记住，而 Secret Store 正是「稳定记住一个
不该出现在日志里的值」的那个地方。

**不能塞进客户端证书那个 bundle。** 那是 `base64(PEM 证书 + PEM 私钥)`；并进去之后，证书轮换会
连带改掉所有已发布的 URL——而证书轮换是一次安全操作，不该顺手把线上地址全换掉。分开存正是为了
让两种轮换互不牵连。

**也不能由部署级 secret 派生。** 消费者需要自己算域名（`workload_id` 是运行时才有的），密钥必须
交到消费者手里；一个部署级密钥交给每个消费者，等于每个消费者都能预测别人的域名。

升级旧部署时，只补建缺失的独立条目，不能重签已有客户端证书；已有值格式损坏应报错，不能静默
重铸。新部署 manifest 和 resource state 保存引用，消费者收到 base64 编码的 32 字节随机值并
标为敏感。旧冻结部署不因重放而生成新密钥，需通过新 apply 补齐。备份恢复需验证密钥与派生结果
保持一致。以上均为待实现行为，不能仅凭 Secret Store 的通用能力判定验收完成。

### 5.1.3.2 凭据轮换与域名密钥轮换分别接入

仓库已经有一套凭据轮换声明，用在 `credentials.provides` 上（`internal/runner/manifest.go`）：

| `rotation_mode` | 含义 |
| --- | --- |
| `reconcile` | 换掉并让活动系统跟着收敛（改数据库角色口令即属此类） |
| `overlap` | 新旧同时有效一段时间，再撤旧的 |
| `migrate` | 需要有人参与的迁移流程，不能就地换 |
| `external` | ANAS 不拥有它，只消费 |

配套还有 `type`（password / shared_secret / token / key / certificate）、`generation` 与
`lifecycle`（probe / reconcile / verify）。

**缺口是：`resources.requires` 生成的凭据完全没有这套声明。** 数据库口令、对象存储 secret key、
compute 的客户端证书，都是 Runner 用 `secrets.Ensure` 铸出来的，manifest 里没有
任何地方说它们该怎么轮换。今天的行为等于「一律不轮换」，而这不是设计出来的，是漏掉的。

补法是让 `spec.credential` 除 `policy` 之外也携带轮换声明：

```yaml
        credential:
          policy: generated
          rotation_mode: overlap        # 由 Module 声明，不是全局统一
```

compute 客户端证书按凭据要求采用 `overlap`，登记新证书并确认消费者可用后撤销旧证书。
`lease_secret` 不属于认证凭据，不声明为资源凭据的 `migrate`，也不进入通用凭据轮换或 `--all`。
它通过专属命令变更，明确告知全部派生 URL 会改变，并按 CRED-R-015 显式确认。
管理证书轮换与消费者证书轮换是不同用例；INCUS-R-029 针对前者，不用后者的测试替代。

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

中介从受信租约声明读取授权，不信任请求自报的 consumer、project、auth 或目标地址。除域名外，
还要验证实例属于租约、guest 端口在 allowed_ports 内，以及目标是受管 proxy。租约身份由专属目录
及其挂载权限绑定；密钥用于派生，不用于认证或证明归属。apply 时需拒绝重叠的域名命名空间，运行时
碰撞失败，不能覆盖其他路由。middleware 与 entrypoint 由渲染方决定。

消费者只可写自己的请求目录，不可写授权文件、其他租约目录或 Traefik 动态配置。中介需限制文件
大小和 schema，拒绝符号链接、路径穿越与任意目标 URL，原子渲染，并在重启时对账撤销过期请求。
这些检查属于已有租约边界的实现细化，真实越权反例归 R-087/R-088 验收。

**proxy 的执行权限尚未定案。** [Incus project 配置](https://linuxcontainers.org/incus/docs/main/reference/projects/)
中的 `restricted.devices.proxy` 只有 allow/block，不能表达租约端口白名单。不能为了让客户端
`PublishPort` 成功而直接开放任意 proxy。受信执行方须在消费者进程外，使用独立权限管理设备；
共享客户端只提交发布意图。还需验证 project 禁令对受信执行方的实际行为，再确定可实现的授权路径。
Traefik watcher 负责渲染不等于它应持有全局 Incus 管理证书；设备执行方的部署与最小权限仍待 §7 定案。

中介放在 **Traefik Module** 里最自然：它已经拥有那个目录，而且本来就是一个常驻服务，加一个小
watcher 不引入新的活动部件。把校验放进共享客户端库是不够的——被攻陷的消费者可以绕过库直接写文件。

#### 固定版本源码核验与当前结论

2026-09-10 检查 [Incus v7.3.0 project 权限代码](https://github.com/lxc/incus/blob/v7.3.0/internal/server/project/permissions.go)：
`checkRestrictions` 为实例和 profile 检查设备，proxy 默认 block；该校验函数不接收调用方
证书身份。`AllowInstanceCreation` 也调用 project 限制检查。继续追踪了实例 PUT/PATCH 与 profile 更新：它们分别经 `AllowInstanceUpdate` / `AllowProfileUpdate`
进入同一 project 内容校验。因此，在这些正常 API 路径上，换管理证书不会豁免 proxy 禁令。
这是固定版本源码结论；实机结果仍待记录。

当前不采用以下绕过：临时关闭 project 限制再恢复、给消费者所在 project 开放任意 proxy、
把消费者生命周期全部改经 Core。前两者破坏围栏，后者改变既定运行时边界。

| 后续方向 | 与现有要求的关系 | 当前处理 |
| --- | --- | --- |
| 保留 proxy 要求并寻找 daemon 可强制的细粒度权限 | 满足目标，但 v7.3.0 的 project 开关不足以证明可行 | 保持入站关闭，先做反例验证 |
| 使用受管网络转发等其他入站机制 | 可能保持消费者围栏，但改变 R-053/R-070 的指定机制 | 仅列候选，需先修订需求和验证，不自动替换 |
| 在消费者外增加强制 API 代理 | 改变直连与权限模型，成为新的可信组件 | 超出本次文档整理，不作为默认方案 |

最小验证矩阵：受限证书与管理证书分别创建/更新含 proxy 的实例和 profile；保持 block 时尝试
启动、停止与修改；记录 daemon 固定版本、错误类型和最终 project 配置。失败后不放宽禁令。
M11 的声明、密钥与路由请求格式可独立准备，完整发布功能须等待上述边界闭合。

### 5.1.6 LAN 模式若将来启用：仍然走 proxy device

macvlan 有一条对两档都成立的性质：**子接口与父接口不能互相通信**——宿主与挂在同一物理网卡上的
macvlan 实例互相到不了。而 Traefik 跑在宿主上。于是 LAN 模式下有两条候选路径：

| | 复用 macvlan shim | proxy device（选定） |
| --- | --- | --- |
| 机制 | 复用 `HOST_LAN_BRIDGE_IP` / `VLAN_BRIDGE_IP` 在宿主侧建的 macvlan shim，Traefik 经它访问实例的 LAN 地址 | 与 NAT 模式相同：实例端口经 proxy device 发布到宿主回环，Traefik 经 §3.5 的受管连接路径访问 |
| 已有实现 | 有（anas-helper） | 有（Incus 原生） |
| 路由目标 | 实例的 LAN 地址，**由路由器 DHCP 分配，会变** | 受管 proxy 后端（连接路径待定） |
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

当前代码尚未提供入站能力；目标方案允许一次性与长驻实例按同一租约授权发布，符合 R-063。
长驻档额外涉及持久卷和稳定地址，不是入站能力的前置条件。proxy 是实现手段，设备权限、端口分配
与发布授权由受信管理方控制，消费者不直接持有任意宿主端口。

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
- 从未产出该 revision 时才按配方构建，并记录 fingerprint；
- 已有摘要但本地镜像丢失时，恢复相同摘要的产物；无法恢复就失败，不能重建不同内容覆盖同名 revision；
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
运行时仍需归一成真实 fingerprint。**命名并未自动解决摘要如何到达共享客户端的问题**：当前 Core
和客户端只接受裸摘要，Provider ensure 没有结果通道。引用注册表由谁拥有、谁解析、解析结果在哪个
deployment 冻结，以及消费者如何读取，均需在 §7 定案；不能偷偷解析可变 Incus alias 代替。
兼容方案为读取旧裸 64 位摘要并归一为 `fingerprint:`，新声明采用上述语法。已有冻结部署继续使用
其原始摘要，不因解析器升级而重新解析成不同镜像。

### 6.2.1 推荐候选：在 apply 之前冻结镜像目录

消除结果通道缺口的条件是：**首次执行 Provider ensure 之前，目标摘要已经是部署输入**。
仅有不可变名字还不够。推荐将 distrobuilder 烘焙与 apply 的导入/验证拆开：

1. 发布流程按配方 revision、架构、隔离档烘焙一次，产出不可变镜像及目录记录；
2. 目录随受信发布输入分发，记录 `reference`、`architecture`、`interface`、`fingerprint`、
   `recipe_digest` 和产物内容校验信息；目录不是从消费者容器或 Provider stdout 读取；
3. Core 在计划阶段按引用、架构和隔离档精确解析，冻结目录摘要与解析结果；
4. Provider ensure 只保证指定摘要已导入目标 project，并读回验证，不生成新的引用映射；
5. 消费者只收到冻结后的摘要；回滚使用原 deployment 的映射，不查询最新目录。

未知引用、缺少对应架构/档位、同一键重复映射或产物摘要不符都失败。`fingerprint:` 可绕过命名
解析，但不能绕过镜像存在性、租约 allowlist 或导入完整性检查。产物来源的认证与校验沿用发布
信任边界；远程下载地址只是目录中指定的获取位置，不能成为消费者可传入的任意 URL。

这一方案意味着默认部署**导入已经烘焙的产物，不在首次 apply 现场烘焙**。目前 §6.1 的首次
构建表述仍允许现场烘焙，两者不能同时作为默认流程：采纳本候选前需同步 guest_image 契约与
R-055 的解释及其测试阶段。自定义本地烘焙可作为 apply 之前的显式产物准备，但不能借本地目录
暗中增加 Provider→Runner 结果通道，也不能覆盖已发布的 `anas:` 名字。

验收用例分两组：发布端验证一次烘焙、revision 冲突与产物留存；部署端验证无构建导入、重复 apply、
错误摘要拒绝、旧 deployment 回滚和原产物丢失时失败。尚未选定发布产物格式与分发位置之前，
该候选不标记为完整实现设计。

### 6.3 旧 revision 的清理：显式 prune，不自动删

每次配方变更都产出新 revision（`@r3` → `@r4`），旧镜像会占盘——一个 Incus 镜像是 GB 量级，累积
起来不小。但**不能在 apply 时自动删**，两个理由：

1. 可能还有实例正基于旧镜像在跑；
2. ANAS 把回滚当一等能力。上一个 deployment 引用的镜像被删掉，回滚就失败了——而回滚正是出问题
   时最需要它能用的时刻。

因此规则与 Resource 的 `deletion_policy: retain` 一致——**移除声明不隐式删除数据**：

- 当前 deployment 与上一个 deployment 引用到的镜像一律保留；
- 清理是**显式动作** `incus.image-prune`，先 dry-run 列出将删什么，确认后再删；
- 它是破坏性动作，遵循宿主特权动作通道的二段确认。

自动删和手动删之间的这条线，与仓库其他地方是同一条：ANAS 从不因为「你不再声明它」就删掉你的
数据，删除必须是有人明确说要删。

## 7. 待决

`lease_secret` 的轮换入口已定：**专属命令，不走凭据轮换通道**。理由是它不是凭据——轮换它改变的
是已发布的域名，不是认证材料；把它和数据库口令放进同一个 `--all` 批量，是含义不同的东西被归成
一类。要求见[凭据轮换要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/credential-rotation.md) `CRED-R-007`、
`CRED-R-015`。

### 7.1 实现前需要定案的事项

| 决策 | 所需产出 | 阻塞范围 |
| --- | --- | --- |
| 宿主回环与 bridge 容器连通 | §3.6 固定目的地非 root 传输候选；待真实网络验证与动作清单评审 | 宿主默认供给、入站 |
| proxy 权限与 VM NAT 拓扑 | v7.3.0 源码不支持“管理证书自然绕过禁令”的假设；按 §5.1.5 先验证，机制仍未定 | 入站 |
| 命名镜像解析 | §6.2.1 提供发布时烘焙、apply 前冻结目录的候选；需确定首次烘焙阶段与产物分发 | guest_image |
| 安装发行版能力 | 官方包及版本、架构、服务单元与幂等行为的核验表 | 自动安装 |

以上定案前不开放相应能力；不阻塞独立租约密钥、声明校验或测试脚本准备。

### 7.2 上游核验记录：镜像限制

2026-09-10 核验 [Incus main project 配置参考](https://linuxcontainers.org/incus/docs/main/reference/projects/)：
`restricted.images.servers` 限制镜像服务器域名，未设置时允许所有服务器；`features.images` 提供
project 镜像集合隔离。该参考未列出逐 fingerprint allowlist 开关，两者均不能直接等同于摘要白名单。
这不证明受限消费者无法导入其他镜像。R-085 后续需在目标 daemon 固定版本上测试远程拉取、本地
导入及启动路径；发现等价强制能力就下沉，否则明确消费者侧摘要校验的边界。此次仅完成文档核验，
不代表 project 的真实强制效果通过验收。

§2 标记为「待适配」的发行版**不是待决，是排期**：先把一级三个做完，其余作为后续计划，届时再
对照上游打包逐条核实。未适配时的行为已经定了——不自动安装、报错给手工指引、依赖 `compute` 的
功能保持关闭（`INCUS-R-049`）。

`incus_vm` 与 `incus_container` **都支持 macvlan**：Incus 对容器直接挂 macvlan 子接口，对 VM 用
tap/virtio-net 背靠 macvlan，两档的 NIC 声明形状一致。上面那条父子不可通信的限制对两档同样成立，
它是 macvlan 本身的性质，不是某一档的。

### 7.3 managed bridge 的 project 归属（代码已修复，真实验收待办）

修复前 Provider 设置 `features.networks=true` 并在租约 project 内创建 `type=bridge`，与 Incus
v7.3.0 不兼容。[networksPost](https://github.com/lxc/incus/blob/v7.3.0/cmd/incusd/networks.go#L385)
检查非 default project 的驱动能力；bridge 继承的 `Projects=false` 触发拒绝。这一结论已核对
有效 project 解析、网络入口与驱动能力，不再只作为文档疑点。

现有修复由 Provider 在 default project 创建每租约独立 bridge，租约关闭 network feature，
通过 `restricted.networks.access` 精确授权自己的网络；profile、证书、实例和配额继续位于
消费者 project。不能只关 feature 而不设置网络白名单。还需验证消费者无网络写权限、跨租约
接网被拒绝，以及真实流量隔离。已有网络不自动迁移，先检查所有权与运行实例。

源码证据及完整实机矩阵见
[2026-09-10 核验记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-10-incus-network-proxy-validation.md)。
Provider 已增加归属、精确作用域和写后读回校验，测试 fake 也拒绝旧请求路径；已有 network feature
开启的 project 不自动迁移。本轮环境无可用 Incus/Docker daemon，因此未执行实机用例；本地回归
通过不代表网络写权限和真实流量隔离已验收。
