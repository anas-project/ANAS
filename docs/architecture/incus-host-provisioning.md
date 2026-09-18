# Incus 宿主供给与镜像烘焙（设计）

> 状态：**提案，部分基础已实现**。更新：2026-09-18。本文规定 ANAS 如何在自己所在的宿主上装好 Incus daemon、如何自动
> 产出 guest 镜像，以及入站流量怎么走。已实现的部分是 `compute` Contract、`incus` Provider
> Module 与共享客户端；它们当前仍要求运维手工准备 daemon 与镜像，本文正是要消除这一步。

需求来源是 [Incus 要求矩阵](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/incus-module.md)
R-047—R-055、R-057、R-062—R-067、R-070—R-072、R-083—R-088、R-092、R-094—R-096；
轮换边界以凭据轮换要求 CRED-R-007/CRED-R-015 为准。下文命令、字段与 Go API 示例均为
目标设计，**除 §6.2 的镜像对象与冻结、§5.1.3.1 的独立命名密钥、§5.1.1 的 ingress 授权声明外，不可直接用于现有 schema**；已实现部分以 compute Contract 源文件为准。

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
4. 初始化符合当前配额准入的受管存储池（当前 Provider 只接受已创建的 btrfs/zfs，见 §7.4）；
   无法确认能力时保持 compute 关闭，不以 dir 降级冒充配额可用，不自动格式化或接管已有设备
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

数据连接与此控制连接分开：Traefik 后端不得复用一个能任意转发的通用端口。发布路径按 §5.1.7 由受信组件核验 guest 地址并登记有限的网络许可；
不能把消费者传入的 IP/URL 作为转发目标。

验收应覆盖：合法证书成功、错误 pin 失败、跨 project 失败、非授权网络不可达、LAN 不可达、
服务重启后的连接失败可归因、半配置失败可重试、卸载清除全部监听。任一项不满足，不通过把 daemon
改绑公网或挂入 root Unix socket 来兜底。

### 3.7 bridge 连接的实施方案

推荐采用 §3.6 的固定目的地转发，按下面的网络分工实现原型；这是目标设计，真实 Linux 验收前不标为可用。

| 网络 | 接入方 | 允许路径 |
| --- | --- | --- |
| Docker 控制 bridge | Provider run-only 服务、需要 compute 的消费者 | 访问该 bridge 宿主网关上的固定 Incus 转发端口 |
| Incus 租约 bridge | 本租约的 VM/系统容器，Provider 在 default project 创建 | DHCP/DNS、按策略 NAT 出站；不允许访问控制入口或其他租约 |
| Docker 入站 bridge | Traefik | 经宿主路由访问 §5.1.7 核验的 guest 地址和端口 |

控制 bridge 作为部署级外部网络，由安装/配置动作创建并按所有权清理。IPAM 从已配置可用范围分配，
检查与 Docker、Incus、LAN、VPN 及宿主路由冲突；不在文档硬编码一个所有宿主共用的网段。
消费者保留原业务网络，控制 bridge 只增加控制连接，不改变默认出站路由。禁止 bridge 内容器互访，
不把 guest 接入 Docker 控制 bridge；网络隔离由规则强制，不能仅凭网络名字推定成立。

非 root 传输服务只绑定控制 bridge 的宿主网关地址和受管高位端口，固定连接宿主
`127.0.0.1:8443`，不监听 `0.0.0.0`/`[::]`。Runner 向消费者投影该网关 endpoint 和原 daemon
证书 pin；转发不终止 TLS，不使用自己的客户端证书。管理证书仍只给 Provider，消费者保持独立受限证书。
此内部 mTLS 入口是新增可达面，必须单独核验，不能因为 daemon 仍监听回环就宣称没有暴露面。

宿主规则分别覆盖 INPUT 与 FORWARD：仅控制 bridge 入接口可访问固定控制端口；LAN、其他 Docker
网络及全部 guest bridge 拒绝。禁止租约之间转发；guest 出站仅开放既定目的范围，不能借 NAT
绕回宿主控制面。IPv6 不启用时显式关闭对应路径，启用时应用相同的拒绝规则；保留必要的回包流量。
规则通过宿主动作通道管理，不在消费者中授予 NET_ADMIN。与 Docker/Incus 自动防火墙规则的优先级、
重启恢复以及地址变化必须实测，不能只测 TCP 正向连接。

创建顺序：网络与拒绝规则 → 启动固定转发 → 检验 mTLS/pin/权限 → 发布 endpoint。失败不宣告
compute 就绪。停止/卸载先停止新租约接入，再按运行实例策略排空，撤销 endpoint、转发服务、规则
和本部署拥有的网络；不删除外部 daemon 或其他部署网络。

### 3.8 固定控制转发的编码边界（2026-09-18，未运行）

`modules/incus/control-relay` 已编码为独立的 Linux 非 root 传输组件，尚未编译、运行测试、打包安装或
接入宿主动作。它不是 `anas-hostd`，不安装包、不创建网络或规则、不发布 endpoint，也不改变生产
ingress 的关闭状态。其存在不能作为 §3.7 默认连接路径已经验收的证据。

唯一上游在二进制中固定为 `127.0.0.1:8443`；没有 upstream 参数、DNS 解析、环境代理、CONNECT、
SOCKS 或 TLS 终止。管理/消费者证书均不进入该进程。监听只允许受管接口上的单个私有 IPv4 和非特权
端口，拒绝通配、回环、IPv6、网段地址与广播地址。IPv6 控制传输尚未实现，不能借此放宽宿主的 IPv6
拒绝规则；数据面的双栈要求仍按 §3.7 独立验收。

安装配置默认路径为 `/etc/anas/incus-control-relay.json`。配置及全部父目录必须 root 所有且不可被
组/其他用户写入；逐级使用目录描述符和 NOFOLLOW 打开，最终文件必须为单链接普通文件，读前后
元数据一致。配置限 8 KiB，拒绝重复/未知/大小写别名字段、null 和尾随 JSON；错误不回显配置内容。
配置是宿主安装输入，不是消费者可写的 Module 参数，也不包含秘密。

| 安装字段 | 当前校验与用途 |
| --- | --- |
| `schema_version` | 必须为 1 |
| `listen_address`、`control_subnet` | 规范化 IPv4 地址/端口与掩码化私有网段，必须相互匹配 |
| `interface_name`、`interface_index` | 冻结安装时的内核接口绑定；启动时及运行中约每秒检查地址、名称、index 和 up 状态 |
| `run_uid`、`run_gid` | 必须匹配专用非 root 运行身份；拒绝 real/effective 身份不一致、额外附加组以及 effective/permitted/inheritable capabilities |
| `max_connections` | 显式设置 1—256，超过容量立即关闭新连接 |
| `idle_timeout_seconds` | 显式设置 10—3600 秒；任一方向的成功传输刷新共同空闲期限 |

双向转发采用固定 32 KiB 缓冲，保留 TCP 半关闭；拨号最多 5 秒。停止时关闭全部连接并等待复制循环
退出；接口漂移则关闭监听和现存连接，而不是切到通配地址。网卡 index 变化后不能沿用旧配置自动
接管新接口：宿主安装/启动协调必须重新核对网络归属、生成配置，再启动服务，该协调尚未实现。

源地址 CIDR 检查只收窄可达面，**不能证明流量来自控制 bridge，更不能代替认证**。上线仍要求宿主
动作先落实 INPUT/FORWARD 的入接口限制及默认拒绝，再启动此服务，完成 mTLS、错误 pin、跨 project、
LAN/其他 Docker 网络/guest 不可达的独立验证，最后才发布 endpoint。二进制与服务单元仍须由受信
发布/安装流程管理，不能让 root 执行 Module 可写路径中的产物；停用/卸载必须撤销监听和所拥有资源。

`config_test.go` 与 `relay_test.go` 已补严格配置、固定目的地、源网段边界、原始字节、半关闭、取消与
空闲超时测试源码，均未执行。它们即使通过，也只是本地传输测试，不是宿主网络隔离或 Incus 实机验收。

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

## 5. 入站流量：Traefik 与受管路由

目标方案已经选定，尚未实现或通过真实宿主验收：公网 IPv4/IPv6 由 Traefik 终止，后端优先使用
受管 guest IPv4 地址。宿主提供受限路由与防火墙，不增加 proxy device 或 network forward。
实例无需公网 IPv6，也不依赖上游 DHCPv6-PD；控制连接仍走 §3.7 的固定目的地传输服务。

### 5.1 把实例内的 Web 服务经 Traefik 发布

链路：

```text
公网 https://<name>.<base_domain>
  → Traefik（终止 TLS，按 Host 路由）
  → Docker 入站 bridge → 宿主受管路由/防火墙
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
          # none：不加认证；域名不可预测不是访问控制，SNI/Referer/日志可能泄露 URL；
          # 不得用于敏感或可写服务，见 §5.1.3.1
          # forward_auth：挂 ForwardAuth 中间件，需要部署里有 forward_auth provider
          auth: none
          domain:
            # fixed：<prefix>.<base_domain>——单实例、地址需要可预测
            # named：<prefix>-<label>.<base_domain>——实例集合固定且需要可读地址
            # random：<prefix>-<派生>.<base_domain>——实例数量不定、按任务动态产生
            mode: random
            prefix: ci
```

**运行时**——消费者经共享客户端提交发布意图与撤销意图，与 start/stop 成对。
2026-09-18 已写入 `Client.OpenHTTPPublisher`、`HTTPPublisher.PublishPort/UnpublishPort`；
代码和新增用例未编译/执行，自动投影、挂载与生产服务尚未接线。以下是接口示意，不是生产启用步骤：

```go
// httpConfig 是本租约的最小配置投影，不是完整冻结授权，也不含 Store 引用或 middleware。
// 目录必须已经由安装方创建并私有挂载；此调用不会创建目录或启用 ingress。
publisher, err := client.OpenHTTPPublisher(httpConfig)
if err != nil {
    return err
}
defer publisher.Close() // 只释放本地句柄，不隐式撤销持久请求。

options := computeclient.PublishOptions{}
// 仅 mode: named 时设置 options.Label = "api"；fixed/random 不接受 label。
publication, err := publisher.PublishPort(ctx, instanceID, 7000, options)
if err != nil {
    return err
}
// publication.RequestedURL() 只是预计 URL，不是后端可达、路由加载或认证生效的证明。

// 工作负载结束时，用原回执撤销。成功仅表示请求已移除；受信中介另行收敛路由与宿主许可。
if err := publisher.UnpublishPort(ctx, publication); err != nil {
    return err
}
```

`PublishPort` 校验本地租约范围、`allowed_ports`、精确匹配的受管 Running 实例及其
`user.anas.workload`，不接受调用者另填 workload/IP/URL。`label` 必须是规范的小写 DNS label，
并与 `mode` 一致；最终名称由共享 `Policy.Host` 推导。中介继续调用验证完整冻结授权的
`Authorization.Host`，两者共用算法，但前者不提供授权。

`HTTPPublicationConfig` 只包含 interface/project/实例前缀、公开策略、基础域名、租约私有目录，
以及 random 模式独立交付的命名密钥；不向消费者交付完整授权、middleware、entrypoint 或全局
Store 引用。密钥不进入请求文件，默认格式化和 JSON 序列化也不输出它。

`computeingress.RequestWriter` 用 instance/port 的 SHA-256 槽位名和目录锁序列化合作写入，
完整临时文件 fsync 后 rename，文件及目录同步成功才给回执；相同意图可重试，冲突不覆盖。
回执持有文件描述符，撤销核对原 inode 与完整意图，旧回执不能撤销合作 writer 后建的新请求。
仅接受已有、当前用户拥有的 0700 本地 Linux amd64/arm64 目录；限制 4 KiB 单链普通文件和
256 项目录，拒绝符号链接、硬链接、FIFO、目录替换和未支持的平台/文件系统。
关闭 writer 不删除请求；移除原文件仅是撤销意图，不能替代宿主权限、连接与地址保留的撤销确认。

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
<prefix>-<hex(HMAC-SHA256(lease_secret, workload_id))[:32]>.<base_domain>
```

- 同一个 `workload_id` 永远得到同一个名字，无需存储；
- 截取 128 位（32 个十六进制字符），使大批任务的碰撞概率足够低；仍须检测冲突并失败；
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
保持一致。以上生命周期已于 2026-09-11 接入 Core，并通过实际文件复制/备份恢复回归；
损坏值、历史引用丢失、跨租约引用与凭据轮换冒用均拒绝。专属轮换命令与生产 HTTP 发布
仍待实现；域名派生及授权实验代码见 §5.1.7，文件回归不代表实际宿主或已发布 URL 的验收。

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
消费者写入：  <lease 专属目录>/<request>.json   ANAS 自己的小 schema
                 { action, instance_id, workload_id, guest_port, label? }
                          │  校验
                          ▼
渲染出：      /run/anas/<...>.yml               真正的 Traefik 动态配置
```

中介从受信租约声明读取授权，不信任请求自报的 consumer、project、auth 或目标地址。除域名外，
还要验证实例属于租约、guest 端口在 allowed_ports 内，以及目标是经 daemon 核验的受管 guest 地址与端口。租约身份由专属目录
及其挂载权限绑定；密钥用于派生，不用于认证或证明归属。apply 时需拒绝重叠的域名命名空间，运行时
碰撞失败，不能覆盖其他路由。middleware 与 entrypoint 由渲染方决定。

消费者只可写自己的请求目录，不可写授权文件、其他租约目录或 Traefik 动态配置。中介需限制文件
大小和 schema，拒绝符号链接、路径穿越与任意目标 URL，原子渲染，并在重启时对账撤销过期请求。
这些检查属于已有租约边界的实现细化，真实越权反例归 R-087/R-088 验收。

#### proxy 方案的淘汰依据

固定 v7.3.0 的正常实例与 profile 更新仍执行 project 的 proxy 禁令，管理证书并不豁免。
源码路径及核验边界见[核验记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-10-incus-network-proxy-validation.md)。
本方案保持 proxy block，不添加实例设备，不需要放宽消费者权限。network forward 不再作为必需
中间层；只有后续发现具体的映射需求并完成专项设计时才评估，不能自动回退到它。

### 5.1.6 LAN 预留与网络边界

发布请求、域名授权和 Traefik 路由与底层 NAT/LAN 分离。当前只实现受管 bridge NAT。
LAN 未来启用前必须重新验证后端可达性、双协议族边界及宿主路由；不承诺复用当前链路即可支持，
也不以 macvlan shim 作为绕开授权的捷径。

### 5.1.7 选定方案：Traefik 直达受管 guest

```text
公网 IPv4/IPv6 HTTPS
  → Traefik（TLS、Host、冻结的认证策略）
  → 专用 Docker 入站 bridge
  → 宿主路由与默认拒绝防火墙
  → 本租约 Incus bridge → guest NIC IPv4:allowed_port
```

**网络与身份。** Docker 与各 Incus bridge 使用不重叠网段；Traefik 经专用入站 bridge 的网关
访问 guest 网段，不加入 guest 网络。多网络 Traefik 必须配置明确的目的路由，不能依赖默认路由
恰好选中入站 bridge；路由配置由受管启动/宿主动作负责，不向消费者授予 NET_ADMIN。
防火墙只允许指定 Traefik 接入端、目标实例地址、TCP 端口的组合及其回包，拒绝其他容器、LAN、
其他租约及 guest 反向发起访问。IPv6 后端暂不开放，公网 IPv6 在 Traefik 转为新的 IPv4 后端连接。
服务必须监听 guest NIC 地址或通配 IPv4 地址，仅监听 guest 回环的服务不可发布。

**权限分工。** 消费者只写本租约请求目录。独立受信发布中介读取冻结授权，通过受限只读身份
查询 Incus 的 project、实例、受管 NIC、IP 分配和运行状态；不持有网络修改能力，不创建 forward。
宿主规则执行端仅接受受信中介提交的类型化租约/实例/端口操作，并独立按受信租约映射校验；
不接受消费者提供的 IP、命令或防火墙文本。它的动作与授权接入宿主通道计划，不能假定现有通道已支持。
Traefik watcher 读取中介生成的受信描述，复用 `ANAS_TRAEFIK_ROUTE__*` 渲染逻辑，不持有 Incus 管理证书。
Core 不参与每个 job 的发布路径。

**请求校验。** 请求仅包含 instance_id、workload_id、guest_port、可选 label 与发布/撤销意图。
租约身份来自专属挂载目录；中介校验实例身份、获批端口、命名空间、固定 auth 及当前受管 NIC 分配。
不能仅凭“IP 在租约子网内”认定归属。请求不能指定 URL、目标 IP、宿主端口、middleware 或 entrypoint。
拒绝符号链接、路径穿越、超大文件和未知字段；目录授权与请求版本绑定活动 deployment。

**事务与回收。** 按租约/实例/端口串行化，先预占域名，再登记窄范围网络许可并验证后端，最后
原子发布路由。失败撤销本次许可；撤销时先移除路由，再删除许可并终止既有后端连接，不能让
ESTABLISHED 规则维持已撤销会话。重复请求幂等，冲突失败，禁止覆盖其他租约记录。
停止、暂停、删除、租约撤销和 IP 变化触发撤销，周期对账补足事件丢失。IP 在许可清理前不可重新
分配；需用受管分配保留或短期授权到期机制防止旧规则误指向新实例。中介重启后先对账再开放，
只恢复授权、实际实例身份与活动部署的交集，旧磁盘请求不得自行恢复访问。

验收覆盖真实 Linux Docker/Incus 路由与防火墙顺序、两档 guest、公网双栈、跨租约、源地址冒用、
绕过 Traefik 直连后端、端口注入、auth 降级、IP 复用、长连接撤销、事件丢失和中介重启。
目标路径通过前不开放 ingress；fake 单测不代表网络与权限验收。

已新增 `cmd/incus-network-prototype`，仅从管理员采集的实验观测生成 guest /32 路由、
bridge veth 来源限制、30 秒后端 tuple 许可、撤销/conntrack 操作及既有 Traefik 环境字段。
它不执行宿主命令、不接受生产域名、不提供消费者发布接口；不是受信中介或宿主动作的替代。
实验步骤与限制见仓库 `test-env/fixtures/incus-network-prototype/README.md`。
2026-09-11 已在操作者指定的 Ubuntu 26.04 宿主通过独立 namespace 的真实 nft 加载、HTTP、
来源隔离、IP/MAC 冒用拒绝、已有流撤销和 30 秒过期检查；脚本为
`test-env/scripts/server-incus-http-netns.py`，未修改宿主规则。该实验以 HTTP 进程模拟两端，
没有 Docker/Incus base chain 或真实 guest；尤其不能把 nft 早期 accept 视为能覆盖后续
Docker/Incus drop。真实两档路由、IP 回收、conntrack 删除与完整长连接撤销仍是阻塞项。

2026-09-12 原型补充 `--capture`：通过显式实验 Incus/Docker Unix socket 的 GET 与 host/Traefik
namespace 内的只读 link 查询，交叉核对实例、NIC、MAC/IP 分配与 veth，比较两轮观测后输出。
`--previous`/`--withdraw` 生成有序发布/撤销计划，旧身份撤销后保留拒绝表；同拓扑以单次 nft 事务
替换，拓扑改变需结束旧实验。这些是实验工具代码，不是生产中介、地址预留、自动宿主动作或 watcher。
拟发布记录不是已应用证明，计划条件仍由操作者核验。原型命令尚未编译或测试，不能把此前基础
namespace 结果用于这些新路径；待测场景已记录到仓库的 E2E 清单。

2026-09-12 继续接入 `compute_ingress`：Core 在 calculate 后冻结租约身份、端口、auth、域名与
独立密钥引用，ForwardAuth 绑定与 middleware 必须来自声明的 Provider；准备期检查租约命名空间
及已知服务域名。`internal/computeingress` 提供严格请求读取与带会话序号的内存预占，实验命令
`--mediation` 已使用这些代码。random 派生固定为 HMAC-SHA256 前 128 位，prefix 最长 30 字符。

这些是授权准备和单路由实验代码，尚未测试。生产启动仍主动拒绝带 ingress 的消费者；没有自动
挂载或启用中介；活动授权读取和独立目录登记见下段，仍没有全局路由登记、受限只读观测身份、宿主动作、IP 保留、探测执行或
事件/周期对账。实验里的活动 deployment 由管理员断言，旧计划不是活动授权或已执行证明。
字段和精确限制以 [compute 技术说明](/reference/module-contracts/compute-technical) 为准。

后续续作补充 Core 活动授权读取及 `internal/computeingressruntime` 请求目录登记：既有共享锁
保护 active/state/manifest 的一致读取，代次绑定实际工作区、激活时间与清单摘要。实验工具可在
新目录登记每租约请求目录，workspace 模式不接受手填授权或密钥来源，采集后再次检查 Core。
注册文件不含密钥；random 命名经管理员进程内的 Core 适配器读取单条命名密钥，生产窄范围传递
仍未定为可用。这些代码尚未测试，也未执行登记；生产启动拦截、真实受限身份、IP 保留、执行与
事件/周期对账的阻塞不变。普通 active 部署不能据此启用生产 ingress，实验正例使用独立元数据夹具。

同日新增 `internal/computeingressruntime.Executor` 作为执行顺序内核。它只接受 Planner 生成的
publication 与 Core epoch，通过 `Observer`、类型化 `HostActions`、`BackendProbe` 和
`RouteRenderer` 接口依次执行地址保留、guest `/32` 路由、HTTP tuple 许可、后端身份探测和路由发布；
每个可见步骤之后写入原子持久化回执。撤销严格按路由、许可、存量连接、`/32`、地址释放的顺序，
任一步失败都保留回执和地址占用。重启对账先撤销不在当前 epoch/目标交集内的记录，再开放新路由。
文件 renderer 使用按 reservation 派生的独占文件名和 no-overwrite hard link 发布，只删除内容完全
匹配的自有文件，消费者不能提供 URL、entrypoint 或动态 YAML。

这仍不是生产执行服务：类型化宿主动作、只读 Incus observer、后端探测实现、
事件来源与消费者目录挂载尚未接入。状态文件是清理回执，不是授权；没有活动 Core 快照和新鲜实例
观测时不能据此恢复访问。生产启动拦截保持不变，本轮代码也尚未编译或测试。

2026-09-13 增加持锁周期循环与工作区请求源。所有作用于同一 ingress 的执行者必须使用同一私有
本地状态目录；完整会话持有非阻塞 flock，目录/锁 inode 或权限变化时拒绝继续外部动作。循环启动
先清理旧目标再读取新请求；授权读取失败触发清理，取消时使用独立的有界清理上下文且保持锁。
退役意图与已退役 reservation 持久化，旧 token 不因请求再次出现或下一轮轮询而自动复活；历史不按
TTL 清除，4 MiB 上限保守拒绝新写入。状态丢失/损坏后的外部孤立工件盘点尚未实现，不能按空状态
推断实际网络干净；部署服务接入前必须补齐。

工作区请求源重新核对活动 Core epoch、登记目录、请求与冻结 auth/Host，实例 UUID/IP/MAC 由
独立 FactReader 提供并在执行前再次校验。实际静态/外部路由库存是必需依赖，读取失败不能换成
空列表。仍需交付真正受限的 Incus 查询身份、库存适配器、宿主动作与探测实现及窄范围密钥交付。
每租约最多 256 个目录项，每轮最多 1024 个 JSON 请求；一个完整读取失败时本原型撤销全部记录，
后续需验证其可用性代价。事件只负责唤醒，丢失事件由周期读取补足，实际事件源尚未接入。

文件 renderer 已改为复用原 entrypoint 的 `ANAS_TRAEFIK_ROUTE__*` 模板，使用仅渲染模式在新建
私有目录中生成候选文件；子进程不收到真实动态目录或环境中的 Secret。HTTPS/TLS 及 middleware
来自受信描述，最终通过独占 hard link 发布。正常 entrypoint 默认路径不变；共享模板升级导致已有
内容不同会拒绝覆盖/删除并保留待清理记录。文件操作成功仍不代表 Traefik 已消费或撤销路由，实际
消费确认与完整宿主验收待实现。renderer 强制要求 `RouteConfirmation` 适配器，未提供即拒绝执行；
确认失败留在清理阶段，不继续释放地址。本轮仅编码，以上新增路径均未执行。

同日补充 `IncusFactReader` 的 HTTPS GET 实现，可注入 `WorkspaceSource.Facts`。管理员配置固定
endpoint、服务端证书、专用客户端证书及已安装租约映射；请求文件不能指定这些值。连接最低 TLS 1.3，
精确比较服务端证书并检查双方证书有效期（包括复用连接），拒绝重定向与环境代理。每次 GET 最多
8 秒、响应最多 2 MiB，一次双采样最多 30 秒；只接受成功的 JSON 同步响应，拒绝重复字段、深层嵌套
及不完整结果，不回显 endpoint、证书或原始响应。

每轮读取服务器身份与明确版本、选定 project、default project 中的独立 bridge、选定实例/状态和
该 bridge 的地址分配。核对 restricted 网络作用域、bridge 归属/NAT、唯一无 VLAN 的受管 NIC、
运行时 MAC 与唯一私有 IPv4 分配；只匹配子网不足以通过。两次选定事实不一致则拒绝。实例 UUID、
`volatile.uuid.generation` 与 `last_used_at` 生成 incarnation 摘要，加入执行目标及回执；每步再次
核验，停止后快速重启且 UUID/IP/MAC 相同也使旧 token 失效。这不是地址保留或跨 API 原子快照；
宿主动作仍须独立确认当前映射。缺少 generation/启动时间的 daemon 或尚未返回 NIC 状态的 guest
直接拒绝，不能回退到管理 socket 或较弱身份。未发布的执行回执格式新增必需字段；不自动接受缺失
incarnation 的旧实验回执，也不据此删除外部工件。

服务端只读身份仍未供给，不能把客户端只调用 GET 视为权限收敛。依据
[Incus v7.3.0 授权文档](https://github.com/lxc/incus/blob/v7.3.0/doc/authorization.md)，普通 restricted
TLS 证书仍可写本项目；改用 scriptlet/OpenFGA 路由时还必须复现原项目隔离，不能直接改全局配置。
API 字段依据该版本的 [实例状态定义](https://github.com/lxc/incus/blob/v7.3.0/shared/api/instance_state.go)
和 [网络租约定义](https://github.com/lxc/incus/blob/v7.3.0/shared/api/network.go)。版本精确匹配只检测漂移，
不代表兼容性已验收。当前未实例化读取器、未修改 daemon 授权，Traefik 库存/加载确认、宿主动作与
完整 E2E 仍待交付，生产 ingress 保持拦截。

随后补充 `TraefikReader` 的库存与消费确认代码。它读取既有 BasicAuth 保护的 `/api/version` 和
`/api/rawdata`，固定 endpoint/证书及 Traefik 3.7.10；不添加监听端口或写入 API。每个 GET 最多 8 秒，
rawdata 最多 4 MiB（HTTP router 4096、service/middleware 各 8192 条）；12 秒窗口内需要两次间隔
250 ms 的完整匹配快照，版本接口的启动时间也必须一致。要求已有 HTTPS API router 与 `auth@file`
加载正常，空对象不能当作空库存；响应、账号密码和 middleware 定义不写日志或磁盘。

库存只处理 HTTPS entrypoint 的 HTTP Host 范围，其他明确 entrypoint 的路由不参与；warning/disabled
仍占用其配置的 Host。支持 `Host`、`Path`、`PathPrefix`、括号、`&&` 与 `||`；最终须得到有限 Host
集合，拒绝 HostRegexp、否定、无 Host 的 catch-all、其他未知 matcher 及多层 parentRefs。HTTPS
entrypoint 存在 TCP router 时也拒绝 HTTP 发布，避免抢占；这不实现 TCP/UDP 发布能力。

Controller 显式传入当前持锁 Journal，`WorkspaceSource.Inventory` 每次从有效回执读取候选归属，
不重新获取 flock。`OwnsHTTP` 重新运行受信模板并比较完整文件和 owner 摘要，目录/文件须由 root 或
当前执行用户拥有且无共享写权限。只有回执候选、完整文件、加载的 router/service 和冻结认证全部
匹配才从库存排除。名字像 ANAS 或仅有 owner 注释均不足以排除；损坏/丢失回执的孤立工件不会
自动获信任，恢复处理仍待实现。

`ValidatePublication` 在任何动态文件可见前检查 Host、service 槽位与 ForwardAuth；
`ConfirmPublished` 再检查唯一 HTTPS/TLS router、精确 guest IPv4:port、默认单后端 service、加载状态
和 middleware，前后复核文件。仅支持直接 ForwardAuth，安装配置必须提供其完整动态定义的规范 JSON
SHA-256（含固定版本的默认值，排除 status/error/usedBy）；`ForwardAuthDigest` 供可信安装输入计算。
不得从运行 API 首次读到什么就信任什么，缺失/漂移/chain 均拒绝。`ConfirmWithdrawn` 要求文件消失，
连续快照中对应 router/service 与直接 router 引用也消失；失败保留清理状态，不释放地址。

API 语义依据 [3.7.10 运行配置端点](https://github.com/traefik/traefik/blob/v3.7.10/pkg/api/handler.go) 与
[运行态类型](https://github.com/traefik/traefik/blob/v3.7.10/pkg/config/runtime/runtime_http.go)。
[service 实现](https://github.com/traefik/traefik/blob/v3.7.10/pkg/server/service/service.go) 默认把已构建
server 标为 UP，因此该确认只证明配置构建/加载状态，不能替代独立 BackendProbe、真实 HTTPS/auth
与连接撤销 E2E。API 凭据、认证摘要的自动供给及运行服务安装尚未接入；全快照变化持续超过窗口
会保守失败，需在后续验收评估可用性。新增代码未实例化、编译或测试，生产保护仍保留。

#### 私有读取配置交付与独立 fixture 探测（2026-09-15，代码未验收）

Core 侧 `DeliverComputeHTTPReaders` 已提供窄范围文件交付原语。它重用现有 Secret Store 解析及
归属校验，只导出当前活动授权中 random 租约的命名密钥；fixed/named 不导出 key。受信安装方另行
提供 Incus 专用观测身份、双方证书及版本，Traefik API 凭据/证书和已冻结 middleware 的精确摘要。
多余或缺失的 key/pin 拒绝，不能从消费者请求或现场 API 建立可信配置。它不签发只读身份、不自动
从 Dashboard 抓取密码，也不向中介开放任意 Secret 查询或完整 Store 挂载。

交付文件限定规范 JSON、1 MiB，按完整 `HTTPAuthorizationSnapshot` 绑定 workspace、部署、激活
与 manifest/epoch。已有私有父目录及文件须属于执行用户；文件 0400、单硬链接，拒绝符号链接、共享
访问和非普通文件。通过私有临时文件同步后无覆盖发布，前后重查 Core 与命名密钥来源；失败只清理
本次拥有的文件，清理失败明确留待恢复。旧目的文件不覆盖。`OpenWorkspaceReaders` 已连接事实读取、
库存、命名密钥与 renderer 确认；所有命名模式每轮/每步都核对完整快照，每个 API GET 前后复核交付
文件的目录/文件身份、权限和内容。新 epoch 需要新交付；旧配置在旧路由撤销前须保留有效，不能先
删凭据再要求清理。损坏/替换配置会阻止确认并保留地址占用；它不是支持热轮换的服务安装流程。

`FixtureHTTPProbe` 是首期实验用的独立 BackendProbe，只接受 `.example.test`。受信 fixture 生产方
预登记完整 PublicationTarget（含 token/epoch/UUID/incarnation/MAC/IP/端口）、安全 GET 路径及每目标
独有响应的长度与 SHA-256。上限 1024 个目标、路径 256 字节、响应 16 字节至 64 KiB；不从第一次
响应学习摘要，不复用不同目标的摘要，不接受消费者提供的 URL、header 或认证参数。HTTP 200 或
TCP 连通均不足以成功；前后还必须通过当前授权、请求和独立 Incus 实例事实核验。相同响应可被
复制，所以它仍依赖宿主持续保留正确地址/映射，不是对恶意 guest 的密码学认证。

探测进程须由可信启动方预先放入 Traefik netns，并提供可见 PID、启动 tick、boot ID、netns
device/inode、从该已核验命名空间内 socket 取得的 cookie 及指定入站 IPv4。每次连接检查真实 procfs
中的进程与 nsfs handle；实际 socket 经 `SO_NETNS_COOKIE` 比较，避免只检查 Go 当前线程就误认
socket 所属命名空间。PID 复用、进程停止/重启、命名空间变化、cookie 不符或内核不支持均拒绝。
相关语义依据 [proc stat](https://man7.org/linux/man-pages/man5/proc_pid_stat.5.html) 与
[Linux socket 实现](https://github.com/torvalds/linux/blob/v6.12/net/core/sock.c)。代码复用已有
`golang.org/x/sys v0.47.0` 的 getsockopt 包装，仅改为直接依赖，未新增版本或库。

请求绑定入站源 IPv4，直连精确 guest IPv4:port，HTTP Host 来自冻结目标；禁用代理、重定向、cookie、
压缩和连接复用，不发送命名密钥或认证信息。单次连接/响应头各限 3 秒，HTTP 交换 5 秒，包含前后
验证的完整探测 25 秒；不延长宿主许可。代码不调用 setns、shell、Docker socket 或网络写 API。
这只验证指定源路径上的 fixture，不证明 Traefik 自身的路由选源、公网 TLS/ForwardAuth、应用权限或
长连接撤销。实验采集与响应登记见下段；生产启动身份供给、guest 内服务准备、服务安装与生产应用
探测契约仍待接入。实际宿主动作/IP 保留及完整 E2E 也未交付，生产 ingress 继续阻止。

#### 实验启动身份采集和 fixture 登记（同日续作，未运行）

`CaptureTraefikProbeIdentity` 现在提供无提权采集原语：调用方先从受信安装映射取得 PID/源 IPv4，
函数锁定当前 OS 线程，要求 self、thread-self 和目标进程的真实 netns handle 一致，在该线程创建
未绑定、未连接、未监听的 socket 读取 cookie，前后复核启动身份与 namespace。函数不自行决定
任意 PID 就是 Traefik，也不切换 namespace。现有实验命令新增 `--capture-probe`，只接受明确的
完整 Docker 容器/网络 ID，通过两次选定容器及 bridge 分配读取包围内核采集，输出带时间和来源 ID
的 `probe-identity.json`。沿用独立实验 socket 限制，默认系统 socket 拒绝；管理 socket 仅存在于
管理员实验采集边界，生产中介仍不能持有。采集进程预置 netns、PID 可见性和生产安装身份映射仍由
受信启动方负责，采集文件不等同于新的授权或可长期缓存的活性证明。

fixture 准备在发布前进行。`PrepareFixtureResponse` 用 32 字节随机数和稳定目标摘要生成独有
响应，并事先计算长度/SHA-256；不从 guest 获取期待值。`RegisterHTTPFixtures` 将与 Core 完整
快照绑定的期待值写入新 0400、单硬链接、1 MiB 内的规范 JSON，复用凭据交付的私有文件原语。
最多 1024 项；重复 Host、实例端口或响应摘要拒绝。登记前后重查原始完整目标的当前请求、授权和
独立 Incus 事实，整个登记最多 30 秒。登记只描述实验响应，不保留地址或产生路由授权。

静态 `NewFixtureHTTPProbe` 继续绑定完整 reservation，适合单会话；持久的
`NewRegisteredFixtureHTTPProbe` 从登记中保存除 reservation 以外的全部目标字段，探测前后同时
检查文件身份/内容和当前 Core。中介重启得到新 token 时，只有完整稳定身份及现时授权/观测都匹配
才套用该期待值；执行器的退役 token 禁令没有放宽。更换 guest incarnation、epoch、MAC/IP、
端口、workload/label、Host 或 auth 均需重新准备，不能用注册文件恢复过期网络许可。

实验命令新增 `--prepare-fixtures`，只接受活动 example.test 授权，通过真实 WorkspaceReaders
在同一 FileStateStore 锁下读取目标；有未撤销 publication 即拒绝。输出独有响应文件与
`fixture-plan.json`，最后发布 `fixture-registry.json`，不调用 Probe、HostActions 或动态路由写入。
登记失败时保留私有准备文件供检查，并明确报错；完整登记也只表示准备完成，还需管理员按计划将
响应装入对应 guest 并提供精确路径的 HTTP 服务。完整命令限 90 秒，超时不输出网络就绪结论。
命令示例、权限与待测项目位于仓库实验目录，当前命令和读取器均未运行。

需要特权的启动/路由动作仍受[宿主动作通道设计](/architecture/host-action-channel)约束；统一动作
ABI 和通道尚未实现，这些原语不构成第三个提权入口，也没有把实验只读采集升级为生产服务安装。

#### 外部工件盘点与有回执恢复（2026-09-15 续作，未运行）

执行器现在要求 renderer 和宿主动作都实现 `HTTPArtifactInventory`。候选只取同一持锁 Journal 的
未完成 publication，不能从消费者请求、文件名、owner 注释或已退役 tombstone 重建归属。
`Executor.Recover` 只撤销已登记目标，再确认完整外部作用域清空；Controller 启动、故障恢复及
退出共用此路径，盘点失败不重置 Source 为可重新发布。对账在开通/续租前后检查外部工件。

地址释放前也核对完整作用域。发现未知工件时仍可按原顺序关闭已知路由、许可、存量连接和 `/32`，
但不释放地址、不删除 retiring 回执。盘点不要求当前 Core 授权或 guest Running，撤销仍以独立
安装身份和旧回执为依据。已退役记录只防重放，不能再用来清理可能已分配给新实例的地址。

文件盘点要求 renderer 使用专属受信目录，不与 `auth.yml` 等其他 provider 文件混放；安装方须保证
Traefik 实际观察该目录。最多读取 4096 个目录项，包括隐藏文件和临时文件；一次检查最多 30 秒。
每个可见文件必须与当前回执的完整模板输出一致。API 读取前后重读文件、项集、inode、权限和内容，
目录替换、无法完整读取、未知项或临时文件都阻止开通。Linux/macOS 文件读取加 NOFOLLOW/NONBLOCK，
拒绝符号链接与特殊文件，不因 FIFO 替换而阻塞。

Traefik 盘点仍需两个一致的实际 API 快照。只有精确文件与完整 router/service/auth 都匹配的 HTTP
对象可以排除；没有文件的已加载对象也算孤立工件。其他 provider、协议、动态 section 以及服务间
引用均检查 `anas-compute-` 保留命名空间。已核验 ForwardAuth 的对应 `usedBy` 是唯一额外排除；
其他 middleware 元数据不能自证归属。扫描键和字符串值时保守拒绝该前缀，即使它恰好出现在无关
配置字符串中；报错不包含原始 API 配置或凭据。检查其他协议的残留不等于实现 TCP/UDP 发布。

`RemoveHTTP` 还会清理规范的 `.anas-compute-<id>.yml.<24hex>.tmp`：须匹配当前未完成回执的完整
渲染字节，并在删除前核对文件身份，删除后确认不再存在及同步目录。崩溃发生在 hard link 前后均
可走该路径；部分写入、未知格式、不同内容或模板升级造成不匹配时保留，不按前缀批量删除。
实际撤销确认同时检查服务间及其他动态 section 的旧 ID 引用，仍有引用就不能继续释放地址。

宿主盘点目前只有强制接口契约，真实适配器仍依赖宿主通道。它必须从独立宿主回执和实时网络对象
核验地址保留、guest `/32`、HTTP 许可、连接、namespace/接口身份及不可变拒绝基线；失败或未安装
不能返回空列表。以上观测不是跨文件/API/内核的原子快照，仍依赖单写者、宿主动作独立校验和地址
保留，真实竞态与超时边界待验收。

缺失状态而留有工件、损坏状态或只有 tombstone 的残留均不自动恢复归属；需维护方结合独立备份、
宿主回执、实际目标和对应版本 renderer 证据处理。不得先删除状态/锁、复制旧 token 或据 owner
注释重建许可。受限管理员恢复动作尚未提供，本文不提供可执行清理命令。代码未编译或测试，真实
宿主验收与生产入口拦截保持不变，待测项已加入实验目录清单。

2026-09-16 开始补宿主动作所依赖的[统一动作 ABI 原语](/architecture/action-abi)（§15）：
`internal/actionabi` 提供有界帧、调用身份、受信事件序列及进程结果校验。执行器不能自行指定日志
seq 或截断，强杀和不完整输出只能得到 unknown。2026-09-17 又在既有 `consolejobs` 增加独立动作
seq、原子事件/截断/终态、重放与重启 unknown 恢复；`jobexecutor.ActionRecorder` 强制公有投影，
等待实际 EOF/进程退出证据后才提交终态。仍无 dispatcher、进程、Module Command/CLI/HTTP 或宿主
入口接线；不能从存储适配推导宿主通道已经可用。新代码未编译/测试，实际盘点适配器仍待实现。

#### 动作执行失联后的持久化拦截（2026-09-18，未运行）

`consolejobs` 的启动检查已增加共享存储级拦截：动作终态为 unknown，且原因是执行器清理失联
（`execution_containment_lost`）或守护进程重启（`daemon_restarted`）时，该存储中的新任务均不能
开始，包括其他 workspace 的只读任务和旧执行入口。依据来自既有 journal 的终态回执，不增加
第二份 job 存储。读取、重放及取消尚未启动的任务仍可进行。

该拦截不会因 registry 重建、journal 压缩、store 重新打开或普通 compensation acknowledgement
消失。它不证明旧进程已经停止，也不执行清理；宿主/执行服务接线仍必须持有执行租约并停止接纳
任务，不能在旧 writer 尚未结束时释放所有权。独立核验残留进程、writer 和宿主工件后解除阻断的
受限恢复动作仍待实现，不提供删日志或改状态文件的绕过流程。新回归代码未执行，M6/M11 与生产
ingress 拦截不变。

2026-09-18 的动作前置继续补齐预检拒绝与持久取消：从未启动的调用只以固定预检失败终态收尾，
不占用业务补偿；一旦启动，不得借该入口抹去执行证据。取消操作者及时间保存于同一 job 的控制
状态，先审计/提交再通知执行器，不从 warning 推断，日志截断与压缩不删除这份记录。
这些接口服务于当前 Module 动作 worker；宿主特权动作仍须独立注册/授权，不把 Module
可执行文件提升为 root。实现和未运行回归见[动作 ABI 设计](/architecture/action-abi)。
它们尚不构成 Incus 宿主网络适配、生产中介安装或真实 guest 验收，生产 ingress 保护保持不变。

同日共用 ABI 补齐 Module 注册表/dispatcher 的动作级幂等及 `coalesce/reject/queue` 接线。
同一 store 以 `(action, key)` 识别重试，完整冻结请求或 workspace 改变则冲突；终态后保留
1 小时，合流的新键、加入者审计和归属时间通过同一 journal 原子保存，快照恢复拒绝重叠归属。
无键合流不产生随机别名，现有任务及冲突 ID 仍需当前读取权限。上述源码和回归用例未运行，
不等于已具备宿主 root 动作、两段确认、网络盘点或生产入口；详情归统一动作 ABI 文档。

#### 消费者请求文件与回执（2026-09-18，代码未验收）

`computeingress.RequestWriter` 补齐消费者侧文件提交，但不改变上面的权限边界。构造时只打开
安装提供的已有私有租约目录，不创建授权、注册表或挂载；仅接受当前 UID 拥有的 0700 目录，以及
已列入实现的 Linux amd64/arm64 本地文件系统。其他平台和未核验文件系统拒绝写入。中介仍独立
验证目录登记、活动部署、冻结策略、实例事实及网络工件；消费者本地检查不是安全边界。

提交固定的小型 JSON 请求，以完整 SHA-256 的小写 base32 编码确定实例/端口槽位。协作写入者
通过目录 flock 串行执行；私有临时文件完成写入及同步后再 rename，并同步目录。临时文件不以
`.json` 结尾；连同忽略项在内最多 256 个目录项，本地保留回执也有 256 项上限。相同请求重试不
重写文件，不同 workload/label 不覆盖已有槽位。只保留本次创建的临时文件清理权限；崩溃遗留或
未知文件不按名称批量删除。锁只协调遵守约定的消费者，不为恶意写入者增加权限证明。

文件回执持有已验证的 inode 描述符。撤回时同时核对文件身份和完整请求，旧回执不能删除复用
同一文件名的新请求；撤回通过删除精确匹配的意图文件表达，不累计每个结束任务的 revoke 文件。
`Resume` 只接管与调用方持久化预期匹配的已有请求，不重新创建缺失文件，可用于消费者重启后的
意图清理。它不恢复中介/宿主回执，不推导网络工件归属；应用侧恢复接线仍待完成。`Close` 只释放
本地句柄，不自动撤回。rename/unlink 后同步或身份核验失败返回结果不确定，不能据此宣告成功。

共享客户端提供显式 `OpenHTTPPublisher`、`PublishPort`、`UnpublishPort`，复用同一策略、命名
和请求读写实现。当前接线从受管运行实例读取 workload，不允许调用参数指定 IP、任意主机名、
宿主端口或认证覆盖。`HTTPPublication.RequestedURL()` 只给出预测地址；文件提交/删除成功均
不证明路由加载、TLS/认证生效、既有连接清理或地址释放。消费者生命周期、安装投影、UID/挂载、
可信中介服务与宿主通道尚未装配，生产 ingress 继续关闭，不能把 API 示例当作已验收发布能力。

`request_writer_integrity_test.go` 已补文件层幂等、恢复、旧回执、原位内容变更、符号链接/硬链接/
FIFO、目录替换、容量、取消及并发回归源；测试没有执行。三处共享客户端镜像构建及 CI 目录已
补 `internal/computeingress`，仍须验证源码 checkout 和 staging 的实际构建与真实宿主矩阵。

### 5.1.8 后续 TCP/UDP 发布边界（不属于首期）

Traefik 原生支持 [TCP router](https://doc.traefik.io/traefik/reference/routing-configuration/tcp/routing/router/)
与 [UDP router](https://doc.traefik.io/traefik/reference/routing-configuration/udp/routing/router/)，因此暂不
增加另一套通用转发服务。未来可共用受管后端路由，但授权与 HTTP 分开：

| 类型 | 入口与认证 | 后续必须确定 |
| --- | --- | --- |
| HTTP/HTTPS | 域名、HTTP auth；首期 | 现有 ingress 声明 |
| TLS TCP | 支持 SNI 的协议可按域名分流；应用认证或明确 TLS 策略 | TLS 终止/透传、共享入口冲突 |
| 普通 TCP | 通常独占监听端口；应用认证与来源限制 | 端口池、租约配额、冲突及回收 |
| UDP | 独立 UDP 入口；应用认证与来源限制 | 端口池、超时及会话撤销 |

ForwardAuth 是 HTTP 策略，不得套用为 TCP/UDP 的认证保证。消费者不得使用 catch-all TCP 规则
抢占现有 HTTPS 入口。TCP/UDP 默认关闭，后续声明应分别表达协议、guest 端口、外部入口和访问策略，
不能扩展 HTTP allowed_ports 就隐式获得原始端口发布权。

[EntryPoints](https://doc.traefik.io/traefik/reference/install-configuration/entrypoints/) 属于安装配置；
不能仅靠动态文件新增任意监听端口。后续选择有限预配置端口池，或明确通过部署变更调整入口，
并同步 Docker 端口发布与宿主防火墙。该取舍以及需求 ID/测试需在实际实现 TCP/UDP 前补齐。

### 5.2 预留：接入 LAN（暂不考虑）

> [!NOTE]
> **本节暂不纳入实施范围。** 当前选定的 NAT 出站与 Traefik 受管路由已覆盖目标出入站需求，LAN 模式解决的是
> 「实例要在局域网里有自己的身份」这个另外的问题，当前没有需求推着它走。本节保留为设计备忘，
> 不排期。


除受管 bridge NAT 之外，预留第二种网络模式：**把实例直接接到宿主所在的 LAN**。此时地址由
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
长驻档额外涉及持久卷和稳定地址，不是入站能力的前置条件。受管路由与防火墙是后端路径，端口许可
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

### 6.2 镜像引用：结构化声明，摘要冻结

目标语法采用对象，不再使用前缀字符串：

```yaml
image_allowlist:
  - catalog: anas
    name: forgejo-runner
    revision: r3
  - fingerprint: "<64位小写十六进制SHA-256>"
```

每个条目必须严格匹配一种形状：`catalog/name/revision` 三字段全部存在，或只有 `fingerprint`。
拒绝混用、缺失、空值、未知字段及字符串条目，不兼容裸摘要或旧 `anas:`/`fingerprint:` 字符串。
首期 catalog 只接受 `anas`；name/revision 是受限标识符，不是路径、URL 或可变 alias。
独立解析包 `internal/computeimage` 已将 name/revision 限为 1–63 个小写字母、数字、点、下划线或
连字符，首字符为字母或数字；Core/schema 接入时使用相同约束，不从任意远程输入建立目录信任。

Core 根据 `(catalog, name, revision, architecture, interface)` 精确查找受信目录，计划中展示
引用与结果，deployment 冻结目录摘要及最终 fingerprint。未知条目、架构/档位缺失、重复映射、
同一版本键内容变化或产物校验失败均拒绝。`fingerprint` 分支跳过名称查询，但仍验证目标镜像可用。
共享客户端只接收解析后的摘要；回滚使用冻结结果，不重新查询最新目录。

命名不会凭空产生摘要，Provider ensure 仍无结果通道。因此镜像烘焙与目录进入部署输入的时机
必须遵循 §6.2.1，不能运行时查询可变 Incus alias。结构化声明、Core apply 接入与 deployment 冻结已落地；受信目录当前为空，
产物烘焙、导入与发布分发仍是后续目标。

### 6.2.1 推荐候选：在 apply 之前冻结镜像目录

消除结果通道缺口的条件是：**首次执行 Provider ensure 之前，目标摘要已经是部署输入**。
仅有不可变名字还不够。推荐将 distrobuilder 烘焙与 apply 的导入/验证拆开：

1. 发布流程按配方 revision、架构、隔离档烘焙一次，产出不可变镜像及目录记录；
2. 目录随受信发布输入分发，记录 `reference`、`architecture`、`interface`、`fingerprint`、
   `recipe_digest` 和产物内容校验信息；目录不是从消费者容器或 Provider stdout 读取；
3. Core 在计划阶段按引用、架构和隔离档精确解析，冻结目录摘要与解析结果；
4. Provider ensure 只保证指定摘要已导入目标 project，并读回验证，不生成新的引用映射；
5. 消费者只收到冻结后的摘要；回滚使用原 deployment 的映射，不查询最新目录。

未知引用、缺少对应架构/档位、同一键重复映射或产物摘要不符都失败。`fingerprint` 对象可绕过命名
解析，但不能绕过镜像存在性、租约 allowlist 或导入完整性检查。产物来源的认证与校验沿用发布
信任边界；远程下载地址只是目录中指定的获取位置，不能成为消费者可传入的任意 URL。

这一方案意味着默认部署**导入已经烘焙的产物，不在首次 apply 现场烘焙**。目前 §6.1 的首次
构建表述仍允许现场烘焙，两者不能同时作为默认流程：采纳本候选前需同步 guest_image 契约与
R-055 的解释及其测试阶段。自定义本地烘焙可作为 apply 之前的显式产物准备，但不能借本地目录
暗中增加 Provider→Runner 结果通道，也不能覆盖已发布的目录版本键。

验收用例分两组：发布端验证一次烘焙、revision 冲突与产物留存；部署端验证无构建导入、重复 apply、
错误摘要拒绝、旧 deployment 回滚和原产物丢失时失败。尚未选定发布产物格式与分发位置之前，
该候选不标记为完整实现设计。

### 6.2.2 离线工件核验接线（2026-09-18，代码未运行）

共享 `ArtifactRelease` 描述将目录 Entry 与实际镜像工件绑定。`release_verify.go` 补
`DescribeArtifactRelease` 与 `VerifyArtifactRelease`：前者为已经烘焙的字节生成候选描述，已有
fingerprint 不同即冲突；后者对照独立冻结的引用/目标/配方摘要、各片段长度和摘要及最终 fingerprint
核验。调用方须提供可信冻结快照，工具本身不验证目录签名，也不能以描述中的 fingerprint 自证可信。

总 fingerprint 按 [Incus 镜像格式](https://linuxcontainers.org/incus/docs/main/reference/image_format/)
计算：split 是 metadata 文件字节后接 rootfs 文件字节，unified 是单文件字节。共享库支持两者，
`cmd/compute-image-artifact` 当前只接收显式本地 split 文件。CLI 的 `--verify` 必须另给
`--expected-fingerprint` 及目标架构/interface；它不运行 builder、导入、下载、发布或更新目录。
使用示例见 [Incus 技术文档](https://github.com/anas-project/ANAS/blob/master/modules/incus/docs/technical.md)。

规范记录最多 16 KiB、metadata 16 MiB、镜像片段 64 GiB；不接收 URL/alias/脚本/动态路径字段。
哈希是有界分块流式操作，取消在读取间检查；任意自定义 Reader 的阻塞仍由调用方负责。
连续无进展的 Reader 有显式失败边界，原始读取错误不输出。该 CLI 对普通文件在读前/读后核对身份、
大小、模式与 mtime，拒绝末端符号链接/特殊文件，不能替代对敌对文件系统的强隔离。

新增用例涵盖拼接顺序、片段损坏/尾随数据、目标/版本/配方漂移、规范记录拒绝、旧 fingerprint
不可替换、取消/无进展 Reader 和 CLI 自证信任拒绝。用例均未运行。哈希一致不证明归档有效、VM
可启动、目标 Incus 已导入或安全边界成立；M12/M13 和 §6.2.1 的部署供给退出条件仍未满足。

### 6.2.3 本地产物归档与目录候选（2026-09-18，代码未运行）

`ArtifactArchive` 在只读核验之外保存已构建的原始产物，`cmd/incus-image-artifacts` 提供
`init`、`record`、`inspect`、`catalog` 四个显式入口。`record` 复用 `DescribeArtifactRelease`，
先将流式测得的片段写入内容寻址对象，再无覆盖提交规范 revision 记录；不另定义镜像格式。
首次记录绑定完整 catalog/name/revision/architecture/interface、配方文件摘要和工件摘要，
重复记录相同内容不改变映射。现存 revision 的字节、格式或配方改变即冲突。

归档目录必须私有、归当前执行用户所有，初始化只接受不存在的目录；正常打开不创建、接管或
修复归档。会话持排他文件锁，检查根目录、子目录、锁与格式标记，拒绝末端符号链接及特殊文件。
对象与元数据通过同步和无覆盖 hard-link 发布，元数据最后提交；崩溃可能留下完整孤立对象或
临时文件，但没有有效 revision 的对象不自动进入候选目录，也不按名称前缀批量删除。

`inspect` 对原始对象重新哈希；缺失对象可由相同原始产物补回，已经存在但损坏的对象不能被
覆盖。`catalog` 重新核验全部对象，并以独立提供的上一份可信目录做 append-only 校验，拒绝旧
版本键丢失或改变；明确的首次发布才允许无历史。归档本身不防御恶意管理员或整个历史丢失，
可信目录与归档须独立备份，不能将重建空归档当作解除版本不可变约束。

该工具只向本地 stdout 写 JSON 元数据，不提供制品下载端点、不内联 base64、不执行 builder，
也不连接 Incus。配方摘要仅覆盖传入的自包含配方字节，外部输入须被配方固定；它不能证明真实
构建 provenance，多文件配方还需统一输入清单。它不修改 bundle 的空目录、不替代发布签名与
分发，也尚未接入 Provider 导入、回滚恢复或受限 prune；§6.2.1 的完整发布供给方案仍待交付。
用法同步于 Module 双语技术文档；归档/CLI 回归源已编写但未编译、执行或作实机验收。

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
| 宿主回环与 bridge 容器连通 | §3.7 已补控制 bridge 与固定非 root 转发方案；待真实网络验证与动作清单评审 | 宿主默认供给、入站 |
| 入站路由与规则实现 | §5.1.7 已选定 Traefik 直达 guest；待验证精确路由、规则执行接口、IP 复用与连接撤销 | 入站 |
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


### 7.4 磁盘配额的存储前提

Project 的 `limits.disk` 是声明总量上限，不能单凭该键存在断言 guest 根磁盘已经受限。
2026-09-11 在隔离的发行版 Incus 6.0.5 daemon 上，dir 驱动因底层缺少 project quota 警告并跳过
限额，旧 Provider 仍报告 quota_enforced=true。源码基线 v7.3.0 的 dir 路径也包含相同跳过行为。
[上游 dir 文档](https://linuxcontainers.org/incus/docs/main/reference/storage_dir/#quotas)要求底层
ext4/XFS 已开启 project quota；远程 pool 元数据本身不能证明该前提。

当前 Provider 增加保守准入：租约写入前读取指定 pool，仅接受 `Created` 的 btrfs/zfs，写后再次
复核；inspect 的 quota/ready 也包含该条件。不支持时失败，不转换存储池、不迁移旧实例或自动
撤销既有证书。其他驱动未准入不表示上游不支持它们；后续需补充能力核验再扩展。
这个检查不替代服务器上的实际写满、总量超配和管理员变更后的恢复验收，也不让 M6 提前完成。
待测场景已列入仓库 `test-env/fixtures/incus-network-prototype/e2e-plan.md`，先完成代码再执行。
