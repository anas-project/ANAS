# Incus 宿主供给与镜像烘焙（设计）

> 状态：**当前实现与目标设计并存，宿主与镜像路径已部分接线，实机验收未完成**。更新：2026-09-22。本文规定 ANAS 如何在自己所在的宿主上装好 Incus daemon、如何自动
> 产出 guest 镜像，以及入站流量怎么走。当前源码已连接 `compute`、宿主供给与镜像工件路径，
> 但默认镜像发布、生产入站与完整宿主验收仍未完成；当前边界见 §1。

需求来源是 [Incus 要求矩阵](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/incus-module.md)
R-047—R-055、R-057、R-062—R-067、R-070—R-072、R-083—R-088、R-092、R-094—R-096；
轮换边界以凭据轮换要求 CRED-R-007/CRED-R-015 为准。下文命令、字段与 Go API 示例均为
目标设计，**除 §6.2 的镜像对象/冻结与显式发布工具、§5.1.3.1 的独立命名密钥、§5.1.1 的 ingress 授权声明外，不可直接用于现有 schema**；已实现部分以 compute Contract 源文件为准。

依据是 [Core 实现标准](core-implementation-standard.md) §4「默认可用，高级可替换」：一个只想开
Actions 的用户，不该先去理解 Incus 是什么、更不该先手工装好它。

## 1. 现状与缺口

2026-09-21 观测与生命周期接续：读取器现在匹配完整安装授权，并从实例自身读取托管标记和
workload；HTTP 读取拒绝选定字段的 JSON 大小写别名。执行器在路由加载后再次检查授权与实例，
变更则撤销；内部宿主观测投影增加逐次调用绑定，旧响应不可作为新观测。固定证书的真实本地 mTLS、
Controller 和磁盘 journal 已有联合回归，但 daemon 数据和宿主网络仍是测试适配器。
服务端只读身份供给、真实宿主投影处理器及完整生产装配未完成，不能把这些回归当作该缺口已经关闭；见 §7.7。

2026-09-19 接续已连接共享 job、独立 systemd 退出观察、同版本安装器、CLI/Web，以及 Incus
install/configure/enroll/uninstall 各自的计划、五分钟一次性确认和执行路径。现有 root/root
anasd 保持不变；早期“必须先迁移非 root TLS/状态”的判断已撤回。固定安装策略还要求实际
进程由 PID 1 识别为配置单元，不能只凭 UID 0 执行动作。当前边界见[宿主通道架构](host-action-channel.md) §13。

供给后端按声明表生成隔离的官方 APT 配置，安装前写入并逐字节校验；包依赖、Ubuntu arm64
官方源及 Debian 安全源分开处理。每个变更沿用持久 intent/receipt 和读回；控制防火墙校验真实
表达式、顺序、归属与表状态，不用注释证明权限，也不拦截无关宿主 IPv6 转发。

登记生成的 root-only 连接 bundle 包含实际宿主架构与受管存储池。Incus Hook 在四项连接设置
全缺省时自动读取，投影既有 Secret Store；完整显式远端配置不读取宿主 bundle，部分输入拒绝
混用。历史自动值即使已恢复到 Env 也必须重新核对原 bundle，删除或变更不能回退到旧凭据。
Provider 与消费者经 `ANAS_COMPUTE_RESOURCE__…__CONTROL_NETWORK_*` 投影连接同一受管控制桥，
业务网络保持默认出口。该多网络声明需要 Docker Compose 2.33.1+。

**代码接线不代表供给验收已完成。** 真实包安装/撤销、systemd 身份与退出、控制桥的容器来源
连接、隔离配额、guest 启动/取消、双栈、KVM 和 one-job 矩阵尚未执行。生产 ingress 的实际地址
生命周期、health 身份和服务装配仍有实现缺口。§7.5 与 §7.6 提供 container veth 地址路由、回复来源
与双向连接清理候选，不代表 DHCP 保留、完整旧 TCP 会话、数值 ifindex 复用或 VM/TAP 已解决。打开的 netns FD 执行器、完整身份回执及原生门禁已编码，
但只有本机回归和 Linux 双架构编译证据。镜像 prune 的确认删除已有代码，真实删除和正式签名产物发布
仍未验收。保持 `developing` 与生产 ingress 关闭，不能用 fixture 宣称默认可用。

`incus` Module 的四项敏感连接参数（endpoint、pinned server 证书、供给用管理证书与
key）均为 `must_resolve`，另外还必须显式解析目标镜像架构，不能从 CLI 宿主猜测。
集成前，这四项连接参数需要人工准备并粘贴；当前已有上述固定私有 bundle 自动接线。
镜像已具备发布侧配方、冻结归档、导出与 Provider 导入代码，但尚无通过启动验收的正式签名
命名镜像发布，因此首次默认使用仍不是已验收的“开箱可用”。

按 §4 的判据——「一个只知道自己想要什么服务的用户能不能装上」——**答案是否**。缺的是自动化，
不是文档。

架构本身不必推翻。Provider/客户端分层不变，变的只是「endpoint 与证书从哪来」：默认由 ANAS 为
本机 daemon 自动生成，高级路径仍可覆盖指向远端自建 daemon。

## 2. 支持的发行版

`install.sh` 安装同版本宿主动作通道，Incus 依赖安装由显式宿主动作完成，不能混为安装器已经
执行了 Incus 实机供给。`incus-host-preflight` 和 `internal/incushost` 声明式表由这些动作复用；
配方显式包括 `nftables` 与 `dnsmasq-base`，不依赖可被 `--no-install-recommends` 排除的推荐依赖。
Docker Engine 与支持新控制网络声明的 Compose 仍是用户自备前置条件。

本机登记探测连接的是宿主自己的控制桥地址，该流量在 Linux 本地回环交付。因此规则允许
`lo` 上以受管网关为源和目的地的固定 `18443` 探测，客户端同时绑定这个本地源地址；LAN、guest
或其他入接口没有此例外。源网段过滤和 mTLS/pin 不变，这个宿主探测不证明消费者容器可达性。

下表是目标适配基线，不是实机支持清单。一级三行已于 2026-09-19 核对官方包目录和 amd64/arm64
条目，记录于 `internal/incushost/recipes.json`；服务单元、安装重试、卸载和运行兼容性尚未验收。
**上游打包情况会变，执行安装时仍须核验实际包来源与签名**；「待适配」行保留为未核验候选，
不能拿 `ID_LIKE` 或第三方源来自动补齐。

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

### 2.1 已编码的只读供给预检

只读预检现有可选的 HTTP 入队和 anasd 同进程共享队列接线，见[宿主通道 §12](host-action-channel.md)。
开关默认关闭；非 root TLS/状态/权限迁移与安装器尚未完成。预检 job 成功不代表 daemon 已安装、
compute ready 或入站已启用，也不替代下述独立诊断与实机验收。

独立诊断命令 `go run ./cmd/incus-host-preflight` 只读取固定系统位置，返回 JSON；
`--recipes` 只打印编译期表，`--skip` 不读取系统标识文件，直接返回 `skipped`。
没有 `--root`、包管理器、脚本、任意路径或 endpoint 参数；它不是生产 `anas host` 入口。
默认 interface 为 `incus_container`，VM 必须显式选 `--interface incus_vm`。
缺少 KVM 时不会把 VM 降级，有 KVM 也不会把默认容器升级；设备存在仅是观察，不是 ioctl 验收。

`ParseOSRelease` 按数据读取，不 source shell、不展开变量。只用精确 ID/VERSION_ID 匹配表，
存在的 VERSION_CODENAME 必须一致，衍生发行版不得借 `ID_LIKE` 自动通过。
重复键、拼接引号、命令替换、非法 UTF-8、超长字段或其他畸形内容使预检失败，不执行任何安装。
Linux 读取按目录描述符检查 root 所有权、不可被组/其他用户写入、单链接普通文件与前后身份。
优先 `/etc/os-release`，仅其不存在时使用 `/usr/lib/os-release`；允许前者指向后者的两种规范链接，
不接受任意链接目的地、FIFO 或可写父目录。该实现比通用 os-release 读取器更保守；不支持的变体
保持关闭，不通过 shell 或放宽文件边界兜底。

当前包目录观测如下。它们是带日期的元数据，**不是硬编码 apt 安装版本，也不是运行准入**：

| 目标 | 2026-09-19 观测版本 | 来源 |
| --- | --- | --- |
| Debian 13 | `6.0.4-2+deb13u10` | [官方包目录](https://packages.debian.org/trixie/incus) |
| Ubuntu 24.04 | `6.0.0-1ubuntu0.3` | [官方 noble-updates/universe](https://packages.ubuntu.com/noble-updates/incus) |
| Ubuntu 26.04 | `6.0.5-8` | [官方 resolute/universe](https://packages.ubuntu.com/resolute/incus) |

这三者不能继承按 7.3.0 编写的入站 observer 兼容性结论。即使发行版匹配，结果仍明确返回
`compute_ready: false`、`runtime_verified: false`，并区分宿主动作未安装、包来源未验证、
daemon 兼容性未验证、存储/网络未验证等阻塞。未适配 OS/版本/架构/init 保持 disabled，
输出固定手工指引而不是添加源或发起安装；该非失败退出语义仅属于诊断工具，尚未接入主部署降级。

`internal/hostaction` 已复用此预检作为只读 `incus.status` 子集，宿主动作输入与审计边界见
[宿主通道设计](host-action-channel.md) §7。安装、配置、卸载、受管资源归属和完整 daemon 状态
仍未实现；不能从这份报告推断现有 daemon 属于 ANAS，或据此删除资源。

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

宿主通道的固定安装策略、已接受 fd 验证、拒绝审计和共享 job 执行侧绑定已于 2026-09-19
补入源码，详见[宿主动作设计](host-action-channel.md) §8。`anas host actions` 只显示本机
编译清单，不连接 daemon，也不证明安装完成。跨进程 broker、实际退出监督与当前 root/root
`anasd.service` 的非 root 身份迁移仍未完成；不会因此自动安装 Incus 或解除本章的密码禁令。

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

#### 受管 nft 基线与独立归属（2026-09-20，原生验收待办）

当前 `internal/incusingresshost` 不再安装全局 default-drop 链。inet forward 使用 `policy accept`，
只对受管 ingress/guest bridge 路径执行精确拒绝；先跳入 `http_permits`，没有有效 IP/端口许可才拒绝。
反向流量同样受许可约束，不能被宽泛 ESTABLISHED 例外保留；guest 主动 NAT 出站的 reply 单独处理。
bridge prerouting 以 `ibrname`、接入 veth、源 MAC/IP 约束来源，并拒绝仅 IPv4 后端网络的 IPv6。
这些 scope 内规则不能覆盖其他 base chain 的后续 drop；真实 Docker/Incus 共存仍需验收。

读回按照 native nft JSON，校验表和链的身份、优先级、policy、完整有序 AST、顶层 comment、集合及
剩余期限。所有动态对象必须匹配独立 publication intent/receipt，不按命名或注释过滤掉外来对象。
空或过期集合可清理但不能报告 ready。允许的 CT state 表示归一只适用于互斥状态位，不推广到 TCP flags。
规则语义以 [nft 官方手册](https://www.netfilter.org/projects/nftables/manpage.html) 与
[JSON 手册](https://manpages.debian.org/testing/libnftables1/libnftables-json.5.en.html) 为依据；夹具不是内核证据。

基线独立保存 `.nft-baseline.json`，绑定 scope digest 和 table handles。安装先完整盘点确认目标表不存在，
持久化 intent，再检查/应用事务并读回；既存但无回执的表不接管。卸载先核对无 publication/route/permit/
connection，保存 removing 意图，再删除并从完整库存确认缺失。缺失归属、表替换、未完结意图或盘点失败
均保守拒绝；不能删除状态重试，也不能把 comment 当成所有权证明。

新增的原生 CI 用例只在新建独立 netns 中验证真实 nft 语法和 JSON/生命周期，不连接 Docker/Incus。
本轮本机 Go 回归与交叉编译通过，原生用例未执行。真实地址分配生命周期、应用健康身份和生产服务接线
仍未交付，production ingress 继续关闭，不能由安装配置字符串或这些测试自动放开。

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
- 从未产出该 revision 时，只在 apply 之前的显式发布准备阶段按配方构建，并记录 fingerprint；
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
发布构建/打包和逐摘要导入已有代码，真实烘焙、正式签名分发及 daemon 导入验收仍是后续目标。

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

当前实现选择默认部署**导入已经烘焙的产物，不在首次 apply 现场烘焙**；§6.1 的首次构建只指
apply 之前的发布准备阶段，与冻结引用和 R-055 构建一次的要求一致。自定义本地烘焙可作为
apply 之前的显式产物准备，但不能借本地目录
暗中增加 Provider→Runner 结果通道，也不能覆盖已发布的目录版本键。

验收用例分两组：发布端验证一次烘焙、revision 冲突与产物留存；部署端验证无构建导入、重复 apply、
错误摘要拒绝、旧 deployment 回滚和原产物丢失时失败。尚未选定发布产物格式与分发位置之前，
该候选不标记为完整实现设计。

### 6.2.2 离线工件核验接线（2026-09-18，本机回归通过）

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
不可替换、取消/无进展 Reader 和 CLI 自证信任拒绝。用例已在 macOS arm64 运行。哈希一致不证明归档有效、VM
可启动、目标 Incus 已导入或安全边界成立；M12/M13 和 §6.2.1 的部署供给退出条件仍未满足。

### 6.2.3 本地产物归档与目录候选（2026-09-18，本机回归通过）

`ArtifactArchive` 在只读核验之外保存已构建的原始产物，`cmd/incus-image-artifacts` 提供
`init`、`record`、`inspect`、`catalog`、`export`、`bundle` 六个不运行 builder 的显式入口，另有 §6.2.4 的 `build`。
`record` 复用 `DescribeArtifactRelease`，
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

上述六个归档命令只向本地 stdout 写 JSON 元数据，不提供制品下载端点、不内联 base64、不执行 builder，
也不连接 Incus。配方摘要仅覆盖传入的自包含配方字节，外部输入须被配方固定；它不能证明真实
构建 provenance，多文件配方还需统一输入清单。它不自动填充仓库的空生产目录，不替代发布签名与
分发。Provider 逐摘要导入、冻结回滚选择和受限 prune 已有代码；真实验收与完整发布信任链仍待交付。
用法同步于 Module 双语技术文档；归档/CLI 本机回归已通过，但没有作真实镜像或 Incus 宿主验收。

### 6.2.4 显式发布构建入口（2026-09-18，编排回归通过，真实烘焙待验证）

`ArtifactArchive.BuildOnce` 与 `incus-image-artifacts build` 将真实 distrobuilder 调用接到上述
不可变归档，但只面向独立发布构建机，不被 `apply`、Provider `ensure`、控制台或宿主动作调用。
这没有交付自动安装、默认 guest 配方、发布信任链或导入环节，不能据此移除 compute/ingress 门禁。

入口先校验原生 Linux、root、目标架构和固定摘要的原生 ELF；将测得的程序封入 memfd，固定环境
与命令参数，通过文件描述符运行。它不继承调用者环境、不允许额外 argv，也不输出 builder 原文。
使用 distrobuilder 的 split 输出；实际 guest 产物及其工具链需要在可销毁的隔离构建机验证，
因为经审阅的配方仍具有 root 执行能力，程序摘要校验不是沙箱或发布来源证明。

归档锁跨越版本准入、构建与提交；首次启动 builder 前同步冻结配方、builder 摘要和版本键。
同一 revision 已有完整工件时验证并复用；工件缺失、损坏或配方变化均失败，不按相同 revision
重新烘焙。未完成的 `build-<版本键摘要>` 尝试目录在跨进程/重开归档后仍阻止静默重试。
恢复只能显式 `record` 完整可信的原始输出，或在不能恢复时采用新 revision。

构建后重新核验冻结配方，镜像经同一套流式哈希/归档提交，JSON 控制输出仅含元数据。
取消尝试终止进程组，但不宣称挂载、子进程及外部状态已清理；尝试目录保留，不递归删除或隐式
卸载未知资源。一次失败不会自动放开同版本重建。

具体命令、前置条件与边界见 [Module 技术文档](https://github.com/anas-project/ANAS/blob/master/modules/incus/docs/technical.md)。
回归夹具只提供不透明字节，证明顺序、恢复和身份核验，不证明真实镜像有效。发布签名、分发、
可信目录填充和实机验收仍未完成。默认 Forgejo 配方、Provider 导入与受限 prune 已有代码，不代表真实发布或运行通过。

### 6.2.5 完整发布目录与可取消供给（2026-09-21）

`ArtifactArchive.ExportBundle` 及 `incus-image-artifacts bundle` 在同一归档锁内冻结并核验全部
revision、目标和原始字节，以新建私有目录生成 Provider 所需的 `catalog.json` 与
`artifacts/<catalog>/<name>/<revision>/<architecture>/<interface>/`。必须显式提供可信历史，
只有实际首次发布才使用 `--first-release`；旧键缺失或改变、空归档和非 split 工件均拒绝。
目录句柄固定目的地，复制时再次有界核对长度和哈希，catalog 在全部工件写完后才写入；失败候选
不会被自动接管。发布脚本已由逐目标 export 改用 bundle，连同历史目标一起输出，不再生成只有
当前工件却引用全部历史的候选目录。该步骤不构建、不签名、不连接 daemon，也不新增下载接口。

Core 供给先验证完整 frozen snapshot，再匹配 release 元数据；多个 runtime/named revision
指向相同字节时，每个绑定先通过校验，再只复制一次物理工件。共享 `ValidateArtifactResolution`
同时用于供给描述验证和字节核验入口，避免两处对版本/配方/目标的解释分叉。供给 descriptor
统一限制 1 MiB；原始 release record 的 16 KiB 界限不变。unified 描述在 split 供给入口直接
失败，不再越界读取第二片段。文件哈希和复制沿用 apply 的取消 context，部分复制在取消后清理。

目录打包到 Core staging 的集成用例使用合成字节，只证明版本保留、布局、完整性、取消和不重建。
正式签名分发、真实 distrobuilder/guest 启动、完整回滚及破坏性 prune 继续按 M6/M12 跟踪；
生产目录保持为空，Incus 状态与 ingress 门禁不因此解除。双语操作说明见
[镜像供给](/architecture/incus-image-supply)。

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

2026-09-21 进一步收紧读回：四项配额必须与申请总量精确相符，全部受管 project 围栏键也须一致；
profile 的配置与设备属性不能携带额外字段。`inspect.ready` 不再等同于 project 已存在且受限，
而是只读核对网络、profile、证书与冻结镜像的当前依赖链。撤销证书后 project 仍存在，配额和
restricted 仍分别报告，但 ready 为 false。`ensure` 仅在最后一次完整依赖读回通过后返回 ready，
`inspect` 不进行导入、修复或再授权。夹具中的缺失与漂移反例不替代真实 daemon 行为。


### 7.5 设备绑定的地址路由保护（候选实现，生产关闭）

2026-09-20 新增 `internal/incusingresshost/address_*`。独立宿主路由表位于 host namespace，不能与
Traefik namespace 中的 `/32` 表混用；安装配置只包含 table/priority，没有消费者提供的命令或地址。
源 Traefik IPv4、目的 guest 子网和入接口同时匹配时才 lookup 该表。先安装后一优先级的终止
unreachable policy，再安装表内 unreachable default，最后安装 lookup；禁止 throw/default-table
回落，已有更早的未知 policy 或 host-local 目标会阻塞安装/准入，不静默接管。

容器分配的永久邻居钉住 guest MAC，精确 `/32` 钉住经独立观察核验的 host veth 设备。
设备注销导致旧路由消失时，新的同名设备不会通过旧 reservation 获得重建路由；请求仍被终止拒绝。
此处利用设备路由生命周期防止前向误送，**不是阻止 Incus DHCP 重新分配 IP**。原生 FIB 用例与
真实双向 HTTP 流量必须分别验证，不能把“读回设备名相同”当作设备连续存在的证明。

`.address-routing.json` 记录完整安装摘要、原始 ifindex、holding/held/releasing 和 retired token。
所有操作复用同一私有 guard；外部效果前持久意图，之后核对精确路由和永久邻居。多个端口仅共享
同一完整实例分配，最后一个使用者退出后才能释放；失败只继续撤销，不重新授权。
容量为 32 分配、每分配 64 使用者、退休加活跃 token 共 256；准入保留未来退休空间，达到历史上限
拒绝新发布但不剥夺已准入项的清理能力。准入还限制完整编码文档为 60 KiB，给 64 KiB 文件边界内
的清理状态变化预留空间；历史不因重装或 TTL 消失，维护工具仍待实现。

发布回执增加 `address_intent`，正常与失败撤销都经既有 permit/connection/route/inventory/address
顺序。没有实际创建的步骤须读回不存在；不能为了清理而把未执行步骤标为已完成。创建地址前确认
nft 基线，具体许可出口绑定目标 veth；默认拒绝仍覆盖从入站 bridge 到整个受管 guest 子网的请求。
移除地址基线必须先清空 publication，再撤 lookup/表内 fallback/末尾 deny；nft 基线最后撤销。

**当前限制：** 只识别非特权容器的 veth，不支持 VM/TAP 或自动档位转换。更早 policy 的兼容性、
反向包与 conntrack 的候选补强见 §7.6，完整生命周期、Incus 持续独立观测、生产 health identity、
宿主动作/启动装配和真实 Docker/Incus 规则共存均未验收。生产构造器仍拒绝开启 publication；不能通过设置上述安装字段启用。
原生用例已加入必跑门禁，缺能力/skip 不算通过；本机与交叉编译不是原生验证。

语义参考为 [ip-rule](https://man7.org/linux/man-pages/man8/ip-rule.8.html)、
[ip-route](https://man7.org/linux/man-pages/man8/ip-route.8.html) 和
[ip-neighbour](https://man7.org/linux/man-pages/man8/ip-neighbour.8.html)。本轮证据与剩余边界见
[接续核对](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-20-incus-address-routing.md)。


### 7.6 回复物理来源与双向连接清理（候选实现，生产关闭）

在 §7.5 安装投影存在时，guest bridge 发往 Traefik 后端 IPv4 的流量先经过 bridge regular chain
`http_reply_origins`，不匹配就明确拒绝。每条许可绑定原始 host veth 数值 ifindex、名称、guest MAC、
IP 与获批服务端口；ifindex 来自独立持久化 hold，不从当前同名设备重新获取。此门禁早于 inet 层的
conntrack established 判定，后者还分别限定请求 original 与回复 reply 方向。

前向 inet 集合与回复 bridge 集合在单条 nft 事务中创建、续租或撤销。两侧精确读回且集合未过期才报
ready；失败保留 intent，不能把一侧成功投影成整体成功。全部动态对象都接受归属、范围、表达式和
有效期核验。单端口撤销只删除本 reservation 的 handles/set，不 flush 其他端口或整个表。

连接清理要求精确回执且两侧许可先读回缺失，随后逐条删除观测到的原/回复四元组并确认无残留。
初期只接受无后端 NAT 的直接 IPv4/TCP、默认 zone 0；翻译回复、额外元组、非零 zone、offload、未知
字段或失败库存均阻断。scope 查询限定 Traefik 源及 guest 子网，删除上限为一次 256 条。
策略摘要增加 `bidirectional-origin-v1`，旧回执不静默升级为新策略授权，也不因此自动删除遗留工件。

原生测试源分别覆盖独立 netns 中的真实包来源/同名设备替换/过期，以及真实 conntrack 记录精确清理。
两条入口均已纳入拒绝 skip 的门禁，**本轮未运行**。包测试不是完整 TCP 会话；记录测试不是排队包或
连接继续传输的证明。强制或回绕 ifindex 复用、停止/暂停且接口仍存活、VLAN/卸载/快速路径、真实
Incus 身份供给、VM/TAP、health 和生产装配仍待完成。生产开启仍被构造器拒绝，不改变首期支持状态。

接口依据为 [nft 手册](https://www.netfilter.org/projects/nftables/manpage.html) 与
[conntrack 手册](https://manpages.debian.org/testing/conntrack/conntrack.8.en.html)；
本轮代码及测试记录见[回复来源核对](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-20-incus-reply-origin.md)。

### 7.7 独立观测校验与生命周期回归（2026-09-21，生产仍关闭）

`IncusFactReader` 的每次观察必须匹配构造时复制的**完整**租约授权，包括 deployment、端口、
域名策略、auth 与 ForwardAuth，不能只比较 project/前缀。新 epoch 的配置交付仍由外层
`WorkspaceSource.Configuration` 检查；读取器自己不能证明 Core 当前活动状态。
实例还必须在自身 `config` 中携带 `user.anas.managed=true`，且 `user.anas.workload` 与请求相同；
不能用 profile 继承值或消费者请求自报的 workload 补齐。标签可被持有项目写权限的消费者修改，
因此它们只是归属一致性检查，不是跨项目隔离或密码学身份。

独立实例校验现在可直接通过 `IncusFactReader.ValidateTarget` 接入执行器的 `Observer`，
与 `WorkspaceSource` 共用 UUID/incarnation/IP/MAC/分配比对。每步重新观察，没有成功结果缓存。
JSON 解码保留上游额外字段和大小写敏感的资源 map 键，但拒绝选定 struct 字段的大小写别名、
重复成员、尾随输入和非法 UTF-8。每次 HTTP 响应读完后再次核对取消与证书有效期；错误不回显
endpoint 或响应正文。两次一致采样仍不是跨 API 原子快照，也不能证明采样间未发生短暂暂停。

上轮内部宿主观测投影采用 `anas.incus-http-host-projection/v2`；本轮 v3 与实际处理器见 §7.8。客户端为每次调用生成独立的
32 字节随机 `observation_id`，请求和响应必须精确绑定；拒绝调用方指定该值、v1/缺字段响应及
旧响应重放。调用使用至多 30 秒的上下文，取消后即使适配器返回成功也不接受。它不是可复用
idempotency key 或长期凭据。尚未接线的宿主处理器必须在收到本次调用后重新读取真实状态，
不能给旧数据重新贴上新标识；随机标识本身不证明数据新鲜或服务端只读权限。
这个预发布协议变化不迁移 executor journal 或宿主 publication receipt，也不登记新的 root 动作。

执行器在 renderer 确认路由消费**之后**再次核对授权和独立实例事实，之后才保存就绪回执；
失效走既有路由→许可→连接→guest 路由→地址释放顺序。加载后复查不能抹去路由曾短暂可见的
事实，更不能替代内核许可期限和持续分配保障。Controller 的本地联合回归验证暂停/停止后
完整撤销、相同实例身份恢复后使用新 reservation、取消时独立清理，以及清理失败保留
`retiring`/地址回执并在重开 journal 后继续撤销。

测试使用真实本地 HTTPS/mTLS、真实 Planner/Controller、文件锁和持久日志；Core 请求源、
Incus 元数据、宿主动作、Traefik 消费和探测由明确夹具提供。没有证明真实 Linux 数据包、
既有 TCP 会话、ifindex 复用、VM/TAP、ForwardAuth、只读证书供给或进程崩溃后的生产恢复。
验证入口与结果见[观测与生命周期记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-21-incus-observation-lifecycle.md)。

2026-09-21 对照 [Incus main 授权文档](https://linuxcontainers.org/incus/docs/main/authorization/)：
将 restricted TLS 类切换到其他授权后端后，证书自身的 project 列表不再由 TLS 后端执行，
替代策略必须重新保持原有项目约束。因此本轮没有为读观察而修改全局授权路由，亦未把普通
restricted 证书交付成“只读证书”。该核验针对 main 文档，不是 7.3.0 或一级发行版包的兼容性证据。

### 7.8 受限宿主观察接线（2026-09-21，未自动安装与启用）

本机观察选择既有 `anas-hostd` 的编译只读动作 `incus.ingress.observe_http`，不向中介交付管理
证书、不挂入 Incus Unix socket，也不为此切换 daemon 全局授权。`HostObservationInvoker` 每次
经原共享 Store/队列发起一个新 job；动作使用 reject 并发策略，没有可复用结果或独立 job 数据库。
原对端/systemd 身份、执行租约、开始/完成审计、EOF 与独立退出监督全部保留。

请求使用 v3 投影：scope、epoch、deployment、consumer/resource、instance、workload、guest port，
另带客户端每次新生成的 observation ID。root action 自己重读安装授权与真实状态，不给旧数据
换 nonce。响应只返回所选租约和实例事实，新增 workload 与 interface 绑定，拒绝 v1/v2；没有
完整 Core manifest、API 原文、路径、证书或私钥。schema 变化不迁移既有 publication receipt。

固定安装输入为 `/etc/anas/incus-ingress/observers/<workspace-id>.json`，schema 为
`anas.incus-http-observation-scope/v1`。它包含 `scope_id`、既有 `ownership_id`、完整连接 bundle
的 `bundle_digest`、显式 `server_version` 和精确活动 `snapshot`。文件必须是受保护的 root 所有
规范 JSON；工作区路径只取 `/etc/anas/anasd.yml` 的登记记录。请求不接受路径或 endpoint。
scope ID 必须等于 job 已获授权的 workspace ID，入队、队列恢复及执行绑定都重新核对。

后端持有**已经存在**的宿主状态锁和工作区运行锁，共享锁期间不允许供给或 apply writer 修改。
读取器另复核 root 所有祖先、文件类型、权限、描述符/路径身份和内容，拒绝状态未完成的宿主、
外部 daemon、连接 bundle 漂移、非活动部署、远端 Provider 以及没有端口授权的实例请求。
Incus 服务必须已运行，不能通过观察隐式 socket 激活；管理证书只在 root 进程内用于固定
`https://127.0.0.1:8443`，pin 与版本来自安装状态，不从第一次响应建立信任。

后端复用 `IncusFactReader.ObserveHostHTTP` 的双采样与全部实例/workload/NIC/分配检查，
并用已有固定 executable-FD 执行器运行只读 `ip -j -d link show dev <已观察名称>`。实际设备必须是
获授权 bridge 上处于 UP/LOWER_UP 的 veth；两组 API 与内核观测必须一致，数值 ifindex、peer index
或其他选定身份变化即失败。本实现仅接受 container；VM/TAP 不能通过改声明或自动降级混入。

投影中历史字段名 `server_uuid` 明确定义为 **ANAS 已存储的随机安装 ID 的 UUID 形表示**，不是
声称 Incus API 提供了这个 UUID；它必须同时匹配 root 所有 `ownership_id` 和固定 bundle 摘要。
中介 `HostProjectionReader` 在安装时固定此值，不能从首次响应学习。它不携带任何 Incus 凭据，
只接受与自身冻结授权精确一致的单租约响应，并作为 `FactReader` / `Observer` 使用。

这只是新鲜的点时观察，不是持续地址持有或暂停窗口的证明。该切片之后的 scope 配置交付见 §7.9；真实 root
运行验收、中介生命周期/UID/挂载、health、完整内核生命周期、VM/TAP 与生产装配仍未完成。
现有 publication 构造器继续拒绝开启；本动作不会改变该开关。实现与验证见
[本轮核对](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-21-incus-host-observation-wiring.md)。

### 7.9 观察范围的计划、交付和撤销（2026-09-21，未启用生产入站）

`incus.ingress.observer.plan` / `incus.ingress.observer` 复用现有宿主计划、五分钟一次性确认、
共享 job、执行租约、审计和退出监督。后者是配置动作，操作仅有 `refresh` / `disable`；它不
创建网络规则、启动中介或改变 production publication gate。自动生成的是配置内容，不是
在后台未经确认地更新授权。实际服务启动和旧路由排空仍是后续交付项。

`refresh` 仅接收工作区 ID。宿主从固定 anasd 配置解析路径，持有已经存在的宿主独占锁和工作区
共享锁，验证受管宿主/连接 bundle、活动 running deployment、全部 ingress Provider 的本机
绑定及容器隔离档。精确 daemon 版本通过已固定证书的 mTLS 双读进入计划；不是首次连接学习
证书，也不代表上游版本已经兼容验收。工作区、活动 epoch、原文件、宿主状态、版本或 bundle
改变，旧计划/批准不能使用。调用方不能提供 snapshot、证书、路径、endpoint 或版本。

计划只读，不创建目录/锁/文件。确认执行后，scope 自动生成到固定
`/etc/anas/incus-ingress/observers/<workspace-id>.json`，文件 `0600`。在既有宿主 `state.json`
新增 `observer_scopes` 归属记录，包含 generation、状态、已提交摘要和待提交摘要；没有第二份
job 或事件数据库。先持久化 `pending` 使旧观察授权失效，再通过目录描述符写临时文件、同步、
比较原字节、原子替换并读回，最后提交 `enabled`。只认文件存在或内容看似合法不再足够。

中断后不自动接管现存文件。新计划只有在文件仍为已登记的原内容或本次待提交内容时才能恢复；
未完成 refresh 的目标又发生变化时，必须先明确 disable。保存失败、取消、读回或目录身份
失败均不能返回可用成功结果。已提交完全相同的 scope 重复 refresh 不重写。

失败返回不等于没有副作用：如果 enabled 的持久提交已经完成，而最终同步/读回或确认返回失败，
磁盘上仍可能是已批准的新配置。客户端必须重新计划读取实际状态，不能重用旧批准，也不能声称
这类未知结果证明观察权限仍关闭。pending 未提交期间才由持久记录明确阻止观察。

`disable` 不依赖 Incus 正在运行、旧 deployment 或连接 bundle 仍可用，只撤销已登记的文件，
保留 disabled 墓碑；把旧 scope 文件拷回不能恢复授权。未登记文件、未知内容、符号链接/硬链接
保留并拒绝。最多保留 64 个工作区记录，无自动过期淘汰。宿主 uninstall 在任何 scope 仍为
enabled/pending 或记录无效时拒绝执行，先通过同一确认流程撤销 scope。

现有手写实验 scope 没有新的归属记录时直接拒绝，不静默采纳或删除。新增状态字段继续位于
未发布的宿主状态 schema；旧二进制的严格解码会拒绝它，不能剥除字段来强行降级。
配置撤销不是已撤销网络流量的证明，实际中介/Traefik/许可的对称排空仍需单独验收。

CLI 使用 `anas host incus-plan --phase observer` 和对应的 `incus-apply --phase observer`；
HTTP 使用原 `{phase}` 路径的 `observer` 分支，plan 的 request 只有 operation，工作区取已授权
URL，apply 必须匹配该工作区和批准内容。Token 仍只经受保护 stdin/HTTPS 正文传输。
双语用法在 Module 技术文档；验证范围见
[观察配置交付核对](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-21-incus-observer-configuration.md)。

### 7.10 中介所有者与无 Incus 凭据的读取器装配（2026-09-21，生产关闭）

`ReaderInstallation.Host` 与直接连接的 `Incus` 配置互斥。既有 Core 交付入口可生成
`anas.compute-http-host-reader-credentials/v1` 私有文件：只含活动快照、选定租约的命名密钥、
Traefik 只读 API 配置，以及 `host_observer.scope_id/server_uuid` 公有安装 pin；不含 Incus
endpoint、证书或私钥。原直接连接文件 schema 和字节布局保持不变。两种读取入口不互相回退，
Host 模式仍仅支持 container。宿主调用客户端由可信运行所有者提供，文件不能指定 invoker。

`OpenHostWorkspaceReaders` 连接 HostProjectionReader、当前 Core 配置校验、命名密钥和原有
Traefik 库存/消费确认。观察前后复核交付文件的内容及目录/文件身份，同内容替换也拒绝。
关闭读取器后，配置、密钥和观察调用全部拒绝，不因再次请求而重建连接。私有文件仍需安装方
预先提供正确的 UID、权限和挂载；此入口不创建服务账户或挂载目录。

`WorkspaceReaders.NewControllerService` 将这组读取器交给单个生命周期所有者；宿主动作、探测
和状态目录必须显式提供，没有默认允许的替身。构造不启动 goroutine。可信 launcher 调用 `Run`
后，复用原 Controller 与同一个独占 journal/flock，启动恢复和首次完整对账通过才关闭 `Ready`。
该信号仅记录首次启动成功，不是永久可用性证明；停机前未完成首次对账则不会发出它。

`Stop` 取消观察、关闭新工作入口，使用独立有界上下文依次撤销路由、许可、连接、guest 路由和
地址占用。`ErrControllerDrain` 不可当作普通 context cancellation 忽略：失败时 `Run` 保留原锁
和旧读取器，等待显式 `RetryDrain`，即使原 owner context 已取消也不退出或重开发布。
并发重试加入同一轮清理。调用方取消等待不取消已开始的排空；每次排空仍受原清理预算约束。
完整库存、持久回执和清理均确认后才关闭读取器、释放会话并报告 stopped。锁获取失败不等于
空安装；`Done` 也必须结合 Stop 的结果解读。进程被强行终止时仍依靠旧 journal 恢复，不能把
内核释放锁理解成网络清理成功。

这是已测试的内部生命周期与配置装配，不是已安装的生产 daemon。外层必须让宿主动作服务、
旧 Traefik 凭据、renderer 和清理所需挂载存活到排空成功；scope refresh/disable 与共享宿主
服务正常停机的后续协调见 §7.11，生产启动器仍未连接。生产 root 网络动作、health、UID/挂载、VM/TAP 和真实
Linux/Incus/Traefik 联合验收仍待完成，不解除 publication gate。详见
[本轮记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-21-incus-controller-owner.md)。

### 7.11 共享配置任务与中介排空协调（2026-09-21，生产关闭）

`ControllerCoordinator` 以已登记工作区维护本进程的中介所有者与变更屏障，不增加持久数据库、
监听器或 root 动作。可信 launcher 必须与 `HostActionService` 共享同一实例；注册集不同则拒绝
构造。启动先同步认领一个新的 ControllerService，再启动其执行；同一工作区不能绕过尚未完成
的配置屏障，也不能替代保留锁的失败所有者。空的进程内登记不证明磁盘或宿主网络为空，每个新
Controller 仍须读旧 journal 并执行完整库存恢复。

确认后的 `incus.ingress.observer` 在共享队列中先封闭目标 scope 的启动入口、停止旧发布并
等待完整排空。install/configure/enroll/uninstall 和 image-prune 涉及共用 daemon，范围是全部
登记工作区。所有受影响中介先收到停止信号，再等待各自清理，不让一个慢租约延迟其他租约退役。
跨工作区集合原子取得屏障；相交的配置任务等待原所有者释放，不能抢占。

排空不在队列 goroutine 中同步等待，任务也不提前进入 running；共享队列因而仍能处理旧清理
依赖的只读任务。宿主执行前再次检查 actor 权限，并由真实 broker 的授权回调复核 job ID、
invocation、action、workspace 和冻结请求摘要所绑定的成功屏障。原来的确认 token、root Claim、
计划/状态摘要、审计与独立退出监督不变；等待过期必须重新计划，不自动延期。

失败排空使配置 job 以未开始失败结束，而非修改 scope 后再补清理。失败中介仍持原锁与旧读取器；
轮询不会发起第二次清理。后续明确批准的新变更可以重试原所有者。成功屏障一直保留到该任务的
确认终态；未知执行、失去 containment 或请求身份改变都不得放开。释放屏障不会自动启动替代中介。

`HostActionService.Run` 的正常 owner 取消先拒绝新的配置/计划与替代启动。其 broker context
不直接继承取消信号，旧中介清理期间保留同一个队列与执行租约；已经开始的受监督动作先完成
原执行流程。停机只继续现有只读观察/预检动作，配置任务保持未开始。所有中介排空成功后，才
关闭宿主 runtime。失败会保留运行所有权并等待显式 `RetryIngressShutdown`；该接口只属于可信
生命周期所有者，不是新 HTTP 端点，不更新配置或恢复发布。取消重试等待不取消实际清理。

本节针对健康共享队列下的配置变更与正常停机。强杀、broker 自身丢失、队列损坏、跨进程重启
接管与持久安装恢复仍需单独监督/验收，不能把内存屏障当作这些场景的清理凭证。生产 launcher、
UID/挂载、root 网络动作和 health/VM/TAP 仍未装配，不解除 production gate。新增本机联合测试
使用真实文件锁、journal、确认账本与队列，但网络及 root 执行是夹具，详见
[配置与排空协调记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-21-incus-ingress-coordination.md)。

### 7.12 普通部署与维护任务的领取前排空（2026-09-21，生产关闭）

`anasd` 现在创建一份工作区协调器，同时注入普通 `jobexecutor.Executor` 与宿主队列。普通
worker 领取的写任务均先封闭本工作区启动入口，包括部署 apply/start/stop/restart/rollback、
本地管理员凭据轮换、Module 变更/命令和快照维护。此处采用保守范围，因为这些服务取得写锁时
也可能进行已有事务恢复；不以任务看似只是元数据修改推导它不会影响旧依赖。

`ClaimNextObserved` 的提交前观察器只发起非阻塞协调，不在 `jobs.lock` 内等待排空或执行外部
操作。等待期间任务保留 queued、没有 started_at、不占运行槽，也不取得工作区写锁；旧中介
仍能通过同一个共享宿主队列读取清理依赖。FIFO 及既有并发/补偿限制不变。新任务不能抢占一个
已取消但尚未完成排空的前序任务；同一进程中的宿主配置任务也不能越过这个工作区屏障。

开始排空前先记录授权尝试审计。排空成功后的实际领取，以及调用应用工厂之前，重新核对 actor
与原 job 的不可变身份、请求摘要和成功屏障。完整模式复用现有 `CheckJobOwner`，不续登录态；
bootstrap/enrollment 仅接受当前状态、当前事务与已冻结确认来源完全一致的 apply，不升级为
后来管理员的权限。原应用层 plan/config/deployment 漂移校验继续执行，不因等待自动延期或
重算批准。

排空失败或领取前权限撤销，通过现有 journal 原子写入 rejected 事件与 failed 终态，错误码分别
为 `ingress_drain_failed`、`job_authorization_revoked`；started_at 为空，不伪报用户取消或业务
已经执行。`RejectQueuedObserved` 不接受 action-ABI 任务、运行中的任务、执行结果或补偿标记。
回放新增严格的 queued → failed 情形，没有新状态字段；旧二进制可能拒绝该记录，不能据此声称
任意降级兼容，也不能删日志来强行重放。

屏障只在原任务确认终态且没有待确认补偿后释放。应用返回成功或已追加 success 事件都不足以
替代终态提交；运行状态、缺失/改变的 job 及补偿未完成会保留屏障。排空失败的 Controller 仍持
原锁和读取器，普通轮询不自动重试。释放不启动新中介，新中介仍须重新验证当前授权与完整库存。

本节只覆盖 daemon 的普通持久任务及其现有维护入口；独立本地 `anas credential rotate`、
直接调用应用服务、其他进程与尚未启用的 Module action worker 不会因为这份内存协调器自动
受到保护。生产启动器、运行 UID/挂载、宿主网络动作、health、VM/TAP 和跨进程恢复仍待交付。
本机测试不是实际 guest/凭据轮换或网络排空验收，production gate 不变。见
[本轮核对](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-21-incus-workspace-mutation-gates.md)。

### 7.13 工作区跨进程围栏与独立 CLI 写入（2026-09-21，生产关闭）

工作区读取器的 `NewControllerService` 现在把 FileStateStore 装配为 `WorkspaceStateStore`，
拒绝内存日志或另一个工作区的围栏。底层通用 Controller/FileStateStore 仍保留为实验原语；
它们不是隐式具备工作区保护的生产启动器。工作区、`.anas/state`、共享运行锁和独立 HTTP 日志
目录都必须已安装，不能由消费者输入创建或替换。当前要求同一受信文件所有者，未替代未来的
非特权运行 UID、挂载与受限宿主通道装配。

启动先取得现有 `.anas/state/lock` 的独占锁和 HTTP 日志独占锁，初始化缺省日志后，在原运行
锁文件写入 `anas.compute-http-workspace-fence/v1` 标记，绑定 HTTP 日志目录的路径摘要与
设备/inode。标记及已有工作区目录项同步后才降为共享锁；转换期间竞争到独占锁的 writer 仍须
看到非空标记并拒绝。共享锁保持到观察、发布、停止与失败重试结束，独立只读 Core 观察仍可使用
同一个共享锁。工作区、祖先、锁描述符及标记在每步复核；最终符号链接、硬链接和身份替换拒绝。

这不是第二份任务数据库，也不是“网络已关闭”的凭证。非空字节只表示写入必须停止，普通 writer
不解析成可执行路径、不学习凭据、不代替用户清理。正常关闭必须完成原有路由→许可→连接→guest
路由→地址释放与完整库存核对，并成功关闭原读取器，才清空及同步标记；不删除或替换锁文件。
周期性空库存或普通回调返回成功不会清空它，单次 Reconcile 结束也不会自动开放工作区写入。

进程强杀后 flock 会释放，标记仍在。新恢复只能使用原路径、原目录身份和仍存在的有效日志，
不能换一个空目录、同路径重建目录、丢失日志后默认为空，或接管未知/部分标记。恢复过程中日志
丢失也失败。显式 Executor.Recover 仍须完成实际库存与退役记录后才能清除标记；失败保留证据。
这种恢复依赖独立真实后端，不能通过删除锁、日志或强制清零绕过。

所有经过 Runner 工作区独占锁的写入口，在锁竞争期间和取得锁后检查同一个描述符。因此独立
`anas credential rotate`（包括 `--force`）、`admin local rotate`、部署写调用及其自动事务恢复
会在中介活跃或待恢复时明确拒绝，而不是等待无限时长或先修改旧凭据。原运行目录初始化可能
先发生，但不会进入 Hook、随机密码生成、凭据写入或业务恢复。纯共享读不读取/修改围栏内容；
需要升级成写锁才能迁移或恢复的读操作仍可能被拒绝。

没有增加独立 CLI 的自动排空请求。当前同进程 daemon 任务仍通过 §7.11–7.12 的已授权协调器
先排空；独立进程必须由可信所有者先停止/恢复原中介后重试。此为合作进程锁协议，不抵抗受信
管理员直接改文件，也不兼容忽略标记的旧 writer；未清理状态下不得降级、替换锁或移动日志目录。
本机新增强杀子进程用例覆盖真实跨进程 flock 与持久记录，网络动作仍为夹具，不是实机 TCP/
Incus/Traefik 验收。生产开关、health、VM/TAP、签名镜像及发布状态保持不变。见
[跨进程围栏记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-21-incus-workspace-process-fence.md)。

### 7.14 统一启动准入与目录身份绑定（2026-09-21，生产关闭）

受信 owner 可通过 `ControllerCoordinator.StartHostWorkspace` 一次连接私有 host-only 读取器、
工作区布局校验、原跨进程围栏和单所有者生命周期。宿主队列提供 `StartIngressWorkspace`，只在
实际服务已运行且 actor 仍获授权时接受；上下文归服务 owner，协调器归同一队列，观察调用器由
服务内部生成，配置不能注入独立 invoker。该方法不是 HTTP 接口，也没有从新配置自动启动服务。
私有交付的 scope ID 必须与协调器选中的工作区一致；错误、已占用或已关闭的 scope 不启动。

`NewControllerService` 的工作区装配现在要求真实、已安装的目录，不能用不存在的路径占位。
请求注册根、凭据父目录、HTTP 日志目录、Traefik 输出目录须分离，不能彼此包含，也不能选中
工作区、整个 `.anas` 或共享 `.anas/state`；专用子目录仍允许。用解析后的路径以及各级
device/inode 检查别名/祖先关系，而不是只比较字符串。凭据和注册文件要求当前 owner、0400、
单硬链接；各租约目录均核对当前 Core 注册与创建时的身份，不仅检查有请求的租约。

renderer 脚本不能放在消费者请求目录、HTTP 日志或动态输出目录下。启动记录脚本文件身份与
SHA-256；后续编译使用的实际字节必须仍与它一致，同内容换 inode 也拒绝。输出目录在后续操作
中继续核对原 inode。这里固定的是受信安装输入，不是独立验证脚本的发行签名。原 Traefik
`ANAS_TRAEFIK_ROUTE__*` 渲染机制不变，没有消费者可选的命令或 entrypoint。

构造检查之后、写入工作区未清理标记之前，再次核对捕获的目录、租约源和文件。复核只读本机
身份与字节，不在已持工作区独占锁时重入 Core 读锁。准入失败不创建日志或未清理标记；若未曾
取得执行会话，仅关闭该次工作区装配新开的读取器，不能关闭另一 owner 的依赖或假称完成清理。
已经进入会话后的失败仍遵循原保锁排空和持久恢复流程。

运行期的发布授权检查也复核这些安装输入；消费者注册根消失或被替换会拒绝续发。此校验不会
绑到旧 Traefik 凭据读取：停止不能因为消费者输入已经丢失就无法清理。路由目录、旧 renderer
或凭据自身失效则仍须失败并保留恢复证据，不学习替代配置。

Start 返回只表示已认领执行，必须等 `Ready` 才能认定完成首次恢复和完整对账；`Done` 和 Stop
结果仍需一起解读。新增本机测试连接实际私有文件、注册表、原日志/flock、Controller 与本地
固定证书 HTTPS API；API 数据、宿主与探测是明确夹具，完整启动/停止用例是空请求场景。
它不证明 UID 隔离、bind mount 标志、真实 Incus/Traefik 或活动 TCP 撤销。独立非特权 UID 与
受限挂载交付、生产配置源/安装器、root 网络动作及 health/VM/TAP 仍待实现，开关不解除。
详见[启动边界记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-21-incus-workspace-launch-boundary.md)。

### 7.15 原生 nft 读回与内核接口索引取证（2026-09-21，生产关闭）

指定 Ubuntu 26.04 主机的 nft 1.1.6 实测表明：全数值 JSON 对不同 EtherType 可能输出相同
数字；`meta iif` 在指定 `-n` 后仍可能显示设备名。不能把字节序转换或当前设备名反查当作原
规则中的索引证据。现有读回使用 `-j -a -y -T`，只接受明确的 `ip`/`ip6` 协议名；数字协议
值继续拒绝。真实替换规则的反例验证另一 EtherType 不会被误认成原 IPv6 拒绝规则。

完整表达式仍按顺序比较。只归一化与紧邻 IPv4 地址/TCP 端口集合匹配等价的显式协议依赖，
以及已知单个 ct-state 数字；不跨越 counter、verdict 或其他未知表达式，不忽略额外字段，
不接受混合状态位掩码、扩大端口集合、错误方向或附加放行。常量身份、域名/租约及规则归属
校验没有因为兼容读回而改变。

真实宿主后端对 bridge 回包规则使用只读 NETLINK_NETFILTER：只发送 GETGEN 与 GETRULE，
按已安装的表、固定 `http_reply_origins` 链和 rule handle 读取首个 `meta iif` → `cmp eq`
的原始四字节索引。随后与完整符号 JSON 合并，前后 generation 必须一致；禁止缺失/多余
handle、截断属性、错误寄存器/比较运算、dump 中断、非内核发送者和响应超限。读取全程固定
OS 线程与 namespace，受八秒上限约束。不会发送写入/reset 动作，不注册新的 root 通道或
Web API，也不通过证据读取取得新的发布授权。generation 变化在原生反例中明确失败。

内核索引不是从 JSON 中的设备名或新设备状态学到的。即使旧 veth 删除且同名替代设备出现，
原规则索引仍与原地址 hold 比较；原生报文用例继续证明冒用来源与替代设备不能使用旧许可。
策略路由读回另外支持规范 IPv4 CIDR 或地址加 `srclen`/`dstlen` 的分列表示；含糊或扩大前缀
仍拒绝。生命周期测试的 guest 对端改在独立 netns，namespace 创建线程在返回前恢复原身份，
不再依赖退出锁定 goroutine 来恢复进程 leader 的 namespace。

这些是已在指定 Linux 主机执行的 kernel/nft/FIB/报文回归，不是完整 Incus/Traefik/guest
联合验收；生产安装、health、VM/TAP、连续地址身份和 Docker/Incus 共存仍需独立完成。
见[原生修复记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-21-incus-native-readback-fixes.md)。

### 7.16 同步创建响应与独立 daemon 验证（2026-09-21，生产关闭）

宿主 Unix API 客户端和固定证书 HTTPS 客户端按请求方法验证同步响应。GET 仍只接受 HTTP
200；POST 另接受 HTTP 201，但 envelope 必须同时为 `type: sync`、`status_code: 200`、
无错误和无 operation。其他方法的 201、任意其他成功状态、`async` 或带未确认 operation
不能冒充创建完成。异步路径继续按原操作 ID 等待，不重发创建请求。后续资源读回与归属记录
仍独立执行；传输成功不替代这些检查。

原因来自指定 Ubuntu 26.04 主机的真实 Incus 6.0.5 响应：池已经创建，但旧客户端因 HTTP
201 返回未确认错误。新增原生回归用实际 `incusUnixClient` 创建/读回/删除独占测试池，
并在错误返回时仍读回、清理可能已创建的对象。旧二进制在该用例失败，修复后二进制通过。

`test-env/scripts/test-incus-daemon-native.sh` 是显式管理员实验入口，要求新报告目录、已解包
发行版依赖、预编译宿主测试/Provider/test2json。它建立独立 mount/network/PID namespace，
验证空网络，私有化挂载传播，以只读 overlay 提供依赖。状态、客户端配置及密钥仅在私有
`/run` tmpfs 下存在；固定测试 socket，不接管系统 Incus 或 Docker。严格要求具名测试和包
终态通过，不接受 skip、空匹配或测试进程失败。正常退出监督与外层时限分别负责清理/约束。

该实验只验证真实同步存储 API 和 Provider 的拒绝边界，不把 `dir` 池当受支持配额后端。
没有创建 btrfs/zfs 池、loop 设备或 guest，不修改宿主 idmap/cgroup。6.0.5 实测不能替代
7.3.0、其他发行版、实际磁盘配额、容器/VM、one-job 或生产自动安装的验收。见
[本轮记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-21-incus-daemon-storage-validation.md)。

### 7.17 共享客户端初始化与消费者取消恢复（2026-09-22）

共享客户端现在将固定 `protocol: incus` 和 remote/project/endpoint 的 `config.yml` 与证书一起提交和精确读回，
不再在每次启动时调用 `remote add`。直接租约和环境租约使用相同的字段校验，初始化前固定
镜像与入口 allowlist；连接检查只做受限 project 的实例列举，不冒充 Provider 的完整配额
和围栏检查。配置文件与 TLS 文件均为私有单链接普通文件，不覆盖另一身份或项目的配置。
真实 Incus 6.0.5 CLI 拒绝旧 `lxd` 协议配置；旧实验配置需使用新私有目录，不能为了迁移
而在初始化时覆盖不匹配的原文件。

并发初始化的实测曾在锁文件 create-or-open 阶段返回 ENOENT。锁入口改为独占创建；已存在
时去掉创建标志后打开，消失或替换仍拒绝，不重新创建一个锁 inode。后续 flock、文件身份、
权限和内容检查不变；固定阶段及系统错误类别用于诊断，不回显私有路径或证书。

Forgejo 在初始化前接入退出 context，创建请求之前保存实例身份与未确认创建标志。取消后的
补偿有独立的两分钟上限；临时缺失不能证明迟到的创建已结束。未完成或超时记录先于队列查询
退役，所有权不符也不能被孤立扫描绕过；终态保存失败继续保留恢复记录。真实 daemon 中的
迟到创建、guest 取消、孤立 registration 和 one-job 仍是独立验收项。

共享构建清单和三份 Dockerfile 已补齐 `internal/securefs`，静态门禁检查仓库内 Go 传递依赖，
另用精简目录离线编译检验声明的输入集合。该检查不是 Docker build，也不证明产品 guest 镜像
可启动。完整客户端语义见 [compute 技术说明](/reference/module-contracts/compute-technical)，
本轮服务器结果见[Linux 验证记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-22-incus-ln-linux-validation.md)。

### 7.18 网桥安装依赖与真实容器生命周期（2026-09-22）

在新的 Ubuntu 26.04 实验 VM 中，仅安装原声明表并使用 `--no-install-recommends` 后，真实
Provider 的网桥创建返回 HTTP 500。实际安装元数据表明 `incus-base` 把 `dnsmasq-base` 列为
推荐项，程序当时不存在；补装后相同 Provider 二进制的双租约 ensure/重复 ensure/inspect
和容器生命周期通过。三份一级发行版配方因此显式登记该依赖，不增加第三方源或独立 DNS
服务配置。既有逐包归属机制继续只卸载本次安装且有记录的包，新增回归验证预装 helper 不被接管或删除。

`test-env/scripts/server-incus-lifecycle-e2e.py` 是显式的可销毁 QEMU VM 测试入口，要求 root、
精确匹配的 cloud-init 实例 ID、QEMU 标识、无 Docker，以及空 Incus project/instance/image/pool
库存。它不在业务宿主安装或运行 daemon；测试使用 VM 自身的 Incus 服务和 cgroup/idmap，
不再用无法代表真实容器运行环境的嵌套 PID namespace 替代容器启动。

测试创建独立 12 GiB btrfs 池，给实际 Provider 预装按原始字节计算摘要的测试镜像，再由 Provider
创建两份独立证书、profile、bridge 和 restricted 租约。共享客户端运行同名容器、stdin 校验、
实际根盘写满、取消 exec 后独立删除、另一租约不受影响及重复删除。测试没有重写共享客户端，
也没有让 fake API 或 dir 池充当真实配额证明。全局存储池预先具有至少 8 GiB 空间，避免把
整个池用尽误算为单个 4 GiB 根磁盘限额；Go 测试和必需子用例必须全部通过，skip 不算通过。

该镜像由 `test-env/helpers/incus-guest-fixture` 构成，只提供初始化、输入摘要校验、有限等待
和有上界的磁盘写入，不是 distrobuilder 烘焙的正式 Runner。取消 CLI 不被解释为 guest 自动
销毁，测试单独用独立预算调用删除并读回。未知创建、真实 controller crash/registration、
Forgejo one-job、ZFS、两档产品镜像、双栈、轮换、生产安装和 ingress 仍分别验收；生产目录与
发布 gate 不因这些夹具结果而开放。

### 7.19 默认 Runner copy 输入与真实构建边界（2026-09-22）

默认配方现以 `sources/forgejo-runner` 指向 BuildOnce 冻结的 Runner 文件。构建进程的工作目录
是原配方目录，distrobuilder 的发行版下载 sources 选项不替代 copy generator 的相对路径。
原裸路径已在真实 distrobuilder 3.2 的 pack 控制中复现失败；修正后生成的 squashfs 文件内容
与冻结输入一致。配方字节变化必须使用新 revision，不覆盖既有 catalog 或失败尝试。

完整默认构建本轮实际进入 Debian 签名校验和基础包下载，但未产出可验收镜像；发现上述路径
问题后取消。真实重试同一 revision 被原不可变归档拒绝，attempt 身份及摘要没有变化。
生成器的局部正例、Runner 二进制的 one-job help 均不证明完整镜像、Podman 或真实作业可用。

新增的完整镜像 smoke 需要独立记录的 fingerprint、真实 export 和精确可销毁 VM 身份，
通过 Provider 固定供给入口导入，再由共享客户端启动，并核验 Runner 与 rootless engine。
该完整入口尚未在真实产物上运行；签名发布、正式 one-job、两档/两架构和 production ingress
保持独立 gate。详见仓库 `test-env/fixtures/incus-runner-image/README.md` 及
[本轮核对](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-22-incus-runner-bake-validation.md)。
