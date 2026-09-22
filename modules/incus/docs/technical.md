# Incus compute provider 技术实现

container 租约的固定 profile 允许内部 OCI namespace 嵌套（`security.nesting=true`），
project 的相应 nesting 键为 allow，但 privileged 仍由 project 强制 unprivileged。
lowlevel、宿主路径 disk、PCI/USB/字符设备等禁令及受管 NIC 围栏不变；VM 档仍禁止该
容器 nesting 能力。客户端不再把 provider profile 覆盖成 false，也不暴露调用方开关。
这是为兑现系统容器内的真实 OCI 执行而调整允许范围，不是关闭 AppArmor 或提供宿主权限。
原生门禁必须同时通过实际工作负载与 privileged/raw/host-device 越权拒绝控制。

本文记录 `incus` Module 的 Provider 实现与安全边界。配置与操作见[中文 README](../README.md)。

2026-09-22 共享消费者客户端接续：凭据初始化先完整校验证书/私钥和服务端 pin，再对私有目录
持锁、不跟随链接且不覆盖已有文件；只复用匹配字节，身份变更需要独立交付目录。CLI 子进程不
继承其他租约 Secret、默认连接或代理，输出有界且取消保持可归因。它不修改 Provider API、
profile、配额或生产 ingress 开关，也不证明真实 btrfs/guest 验收；具体接口与恢复边界见
[compute 技术说明](../../../contracts/compute/docs/technical.md#共享客户端的凭据准备与子进程边界)。

2026-09-18 复核：Provider、Hook、镜像归档/构建编排、HTTP 策略与执行事务的本机 Go 回归已通过。
文中较早的“未运行”段落保留当时的实现记录，不代表新增了生产授权。当前测试宿主是 macOS arm64；
Linux 专属身份校验、真实 distrobuilder 烘焙、Docker/Incus、配额与入站均未完成实机验收。
Module 仍为 `developing`，生产 ingress 继续关闭。完整验证范围与剩余工作见
[本轮核对记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-18-incus-implementation-verification.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `7.3.0-r2` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `compute` | 提供的 Contract | `1.0.0` / `incus_vm` |
| `compute` | 提供的 Contract | `1.0.0` / `incus_container` |

两个 interface 共用同一个 executor 与同一条校验路径，只在 project 配置上分叉一项容器特权限制。

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_incus_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-incus-provisioner:7.3.0-r2` | `incus` | 0 |
<!-- generated:compose-topology:end -->

只有一个 run-only 服务。它没有 `ports`、没有 Traefik label、没有卷、也不挂载宿主 socket——本
Module 是远端 daemon 的客户端控制面，容器内不该有任何可被连上的东西。Runner 用
`docker compose run --rm --no-deps --no-TTY` 一次性拉起它，进程退出即结束。

## 本地构建与 staging 的共享源码

Compose 的 `build.additional_contexts.shared` 使用
`${ANAS_SHARED_BUILD_CONTEXT:-../..}`。默认 `../..` 只适用于本仓库中
`modules/incus/docker-compose.yml` 的目录布局；它不是自动寻找源码根的机制。
复制到 deployment staging 后，不得继续假定该相对路径指向 ANAS 源码。

从 staging 本地构建时，应把 `ANAS_SHARED_BUILD_CONTEXT` 显式设为与该 Module
构建输入版本一致的、受信 ANAS 源码根的绝对路径。此目录至少须包含 `go.mod`、
`go.sum` 与 `internal/computeclient`；不能指向 `modules/incus`、`provisioner`
子目录或只含运行数据的工作区。不要用另一版本的共享库与已冻结的 Module 源码混建。

以下为路径示例，须先进入所选 Module 的 Compose 文件所在目录，确保 `provisioner/`
构建目录及 Compose 引用的 `.env` 已准备好。示例没有在本轮执行，不代表 staging 构建验收通过：

```sh
ANAS_SHARED_BUILD_CONTEXT=/srv/src/ANAS \
  docker compose build anas_incus_provision
```

变量只选择构建上下文，不改变 daemon endpoint、消费者凭据或运行时权限。Dockerfile
通过命名 `shared` context 复制共享包，Go 构建仍保留 `GOPROXY=off`；这不等于整个
Docker 构建离线，基础镜像与发行版包仍需已经可用或能从配置来源取得。
只有运行产物、没有对应源码时，不能靠修改路径完成本地构建，应使用匹配版本的预构建镜像，
或先准备完整受信源码。源码 checkout 与 staging 两种实际构建仍需分别验收。

## 固定控制转发组件（已打包、实机待验收）

`modules/incus/control-relay` 是为宿主回环连接候选方案编写的 Linux 非 root 传输组件，
**不是 Compose 服务，已进入同版本发行归档和安装器，只有 configure 才启用**。它只把安装配置指定的控制 bridge
IPv4/高位端口原样转发到编译期固定的 `127.0.0.1:8443`，不接受 upstream、HTTP CONNECT、
SOCKS、TLS 密钥或调用方命令；Incus mTLS 与原服务端 pin 仍由原两端核验。

组件要求 root 所有且不可被组/其他用户写入的配置及父目录、专用非 root UID/GID、无额外附加组
和无 capabilities。配置绑定接口名称、index、网段与网关；接口漂移时停止服务，不改绑通配地址。
连接数、拨号期限和双向空闲期限受限，保留半关闭，停止时关闭现存连接。配置字段属于宿主供给
流程，不是本节下面的 `incus.*` Module 参数；完整显式远端配置仍走独立接入路径。

宿主安装动作、单元、限定受管 bridge 的 INPUT/FORWARD 规则与 endpoint 私有投影已编码。
官方发布、接口重建协调及 mTLS/pin/project/跨网络和卸载实机验收仍未完成。源 CIDR 检查不能代替防火墙或认证。
本机可运行的配置和传输测试已通过；Linux 身份检查仅完成交叉编译，生产 ingress 保持关闭。
详细边界见宿主供给架构 §3.8。

## 配置契约

### 宿主供给预检（2026-09-19，独立诊断入口）

在源码仓库运行 `go run ./cmd/incus-host-preflight` 可只读检查本机；`--recipes` 打印编译期
发行版映射，`--skip` 不读取系统标识文件。默认 `incus_container`，显式 VM 使用
`--interface incus_vm`。没有安装、脚本、任意路径、包来源或 endpoint 参数，也不会连接
可能 socket 激活 daemon 的 Incus 接口；它不是正式 `anas host` 或 Web API。

`internal/incushost` 精确匹配 Debian 13、Ubuntu 24.04/26.04 的 ID/VERSION_ID 与目标架构；
`ID_LIKE` 不带来衍生发行版准入。Linux 固定文件读取检查 root 所有祖先、模式和读前后身份，
只允许已知 os-release 回退/链接形式，不 source shell、不执行命令。一级表的包信息已查官方
目录，但安装重试、包签名/来源、服务单元和运行兼容性尚未验收，不能直接作为安装步骤执行。

预检始终明确返回 `compute_ready: false`、`runtime_verified: false`，区分未适配、跳过和
未实现门禁；不会因缺 KVM 把 VM 自动变容器。低权限 `incus.status` 内部处理器复用这条只读
路径，并要求动作输入和执行审计；完整 daemon 状态与真实宿主能力仍另行验收。root socket、
共享 job/CLI/Web 和计划/确认/执行已接线，但不能以本机用例代替 Linux 原生身份或真实安装验收。
设计与上游版本差异见 [宿主供给架构](../../../docs/architecture/incus-host-provisioning.md) §2.1。

### 宿主 job 绑定（内部实现）

以下 Module 配置不包含宿主 job broker 的私有参数。新增的 `Activation.ServeBrokered` 与
`HostJobBinding.ServeBroker` 连接编译动作：固定私有 Unix endpoint、双向内核身份核对、
冻结 job/release 及执行前后的权限复核。握手或 socket 关闭均不能释放仍有存活进程的执行租约。
该传输不创建第二份 job 存储、不向 root 提供用户目录里的脚本或数据库，不从 manifest 注册宿主处理器。

私有 listener 与 `HostJobBroker` 分派已编码：只在已安装的私有目录创建固定 socket，
不接管旧节点；最多 32 个已运行任务绑定、8 条并发连接，输入不能自行注册 job。退休要求实际
终态与远端清理，监听停机不解除任务的执行租约。最新接续增加了 systemd 独立退出观察和共享
recorder 的终态接线：先固定真实单元/invocation，再核对退出码及空进程集，未确认则保留租约
并写已有的持久阻断。`anas-hostd` 与候选单元已进入同版本打包。
HTTP 入队、daemon 同进程队列、计划/一次性确认/执行、安装器和 CLI 已接线。现有 root/root
anasd 与 TLS 权限不变；不再要求非 root 迁移。真实 systemd/退出状态验收未运行。
服务配置 `host_actions` 默认关闭，不是 compute ready。
Linux 原生测试入口为
`bash test-env/scripts/test-host-job-broker-native.sh`，只运行隔离 socket/子进程 fixture；要求关键
用例实际执行，不把缺内核能力或跳过当成验收。该脚本不覆盖新 D-Bus/systemd 原生链路，
详细当前边界见[宿主通道架构](../../../docs/architecture/host-action-channel.md) §13。

### 自动宿主连接与控制网桥

四项敏感连接设置全缺省时，Hook 只读取固定 root-owned `0600` 文件
`/var/lib/anas/incus-host/connection.json`，校验固定 schema、实际架构、受管池、网段/网关、证书
与私钥匹配及管理证书摘要，然后写入既有 Secret Store。禁止路径覆盖、链接、重复 JSON、
过宽权限和半套显式连接；完整显式远端配置不读取该文件。自动来源和绑定摘要随 Secret 保存，
重复 apply 即使已经恢复四项 Env，也必须重新核对原文件；撤销或漂移不回退到历史凭据。

自动目标的池为 `anas-btrfs`，架构来自宿主供给观察；远端架构仍由管理员明确提供，远端池
缺省为 `default`。改变既有自动绑定需显式恢复/协调，不能通过普通 calculate 暗中轮换凭据。

自动模式将 `INCUS_NETWORK_NAME` 设置为受管 `anas-incus-control`，并标记 external，Compose
不会接管它的生命周期。Core 以每资源的 `CONTROL_NETWORK_NAME` / `CONTROL_NETWORK_EXTERNAL`
投影给消费者。Forgejo 的两个 compute 服务及 AI Agent 编排服务才连接控制桥，业务网络以
`gw_priority: 1` 保持默认出口，要求 Compose 2.33.1+。远端模式清除自动网络标记并保留原接入。
该连接仍须通过真实容器来源、默认路由、mTLS、隔离与 IPv6 验收，不能把静态 Compose 解析当作连通证据。

### Module 参数

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `incus.admin_certificate_b64` | string | — | — | `host` | `INCUS_ADMIN_CERTIFICATE_B64` | 否 | 是 | 是 | 否：`rotate-incus-admin-credential` | `credential_rotate` | 供给专用的管理客户端证书，不交给任何消费者 |
| `incus.admin_key_b64` | string | — | — | `host` | `INCUS_ADMIN_KEY_B64` | 否 | 是 | 是 | 否：`rotate-incus-admin-credential` | `credential_rotate` | 管理证书的私钥 |
| `incus.endpoint` | string | `pattern: ^https://[A-Za-z0-9.:_-]+$` | — | `host` | `INCUS_ENDPOINT` | 否 | 是 | 是 | 是 | `reconcile` | 远端 Incus daemon 的 HTTPS 地址 |
| `incus.image_architecture` | enum (`amd64`, `arm64`) | — | — | `host` | `INCUS_IMAGE_ARCHITECTURE` | 否 | 是 | 否 | 是 | `container_recreate` | 目标 daemon 的 guest 镜像架构；必须显式提供，不从 CLI 宿主推断 |
| `incus.server_certificate_b64` | string | — | — | `host` | `INCUS_SERVER_CERTIFICATE_B64` | 否 | 是 | 是 | 是 | `reconcile` | 被固定的 daemon 服务端证书；失配时直接失败，不回退 |
| `incus.storage_pool` | string | `pattern: ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$` | — | `runtime` | `INCUS_STORAGE_POOL` | 否 | 是 | 否 | 是 | `reconcile` | 每个租约根磁盘所在的存储池；显式远端未设置时 Hook 沿用 `default`，宿主自动 bundle 使用 `anas-btrfs` |

四项全部经 `.env` 进入 run-only 容器，三项凭据以 base64 PEM 传递，Hook 在 apply 早期校验类型。

四项连接配置均标为敏感。配置键 `server_certificate_b64`、`admin_certificate_b64` 先导出为
`INCUS_SERVER_CERTIFICATE_B64`、`INCUS_ADMIN_CERTIFICATE_B64`；Hook 校验后才派生 Provider
使用的 `INCUS_SERVER_CERT_B64`、`INCUS_ADMIN_CERT_B64`。缺少规范输入时不接受旧 raw-env 别名
替代，敏感值传播同时覆盖派生别名。消费者的 `ENDPOINT`、`SERVER_CERT`、`CLIENT_CERT`、
`CLIENT_KEY` 投影也均标为敏感，而不是仅保护私钥。

当 `endpoint`、`server_certificate_b64`、`admin_certificate_b64`、`admin_key_b64` 四项全部显式
提供时，Hook 走高级远端路径，不读取宿主文件；四项只提供一部分时 fail closed，不把显式值和自动值混用。
显式远端仍要求 `image_architecture` 明确给出，`storage_pool` 为空时按既有语义使用 `default`。

四项连接配置全部为空时，Hook 只在已安装 Linux 路径读取固定文件
`/var/lib/anas/incus-host/connection.json`，不接受配置、环境变量或用户参数覆盖路径。该文件必须是
安全祖先目录下 root 所有、`0600`、单硬链接的普通文件，读前读后身份一致；symlink、FIFO、可写祖先、
超限、未知字段、重复 JSON 字段和旧 schema 均拒绝。bundle schema 仍为
`anas.incus-connection-bundle/v1`，但自动接入要求新增 `architecture`（`amd64`/`arm64`）和
`storage_pool`（`anas-btrfs`）字段；缺少这些字段的旧 bundle 不会被猜测补齐。

自动 bundle 必须固定 `endpoint=https://<control_gateway>:18443`、`control_network=anas-incus-control`、
`relay_service=anas-incus-control-relay.service`，并验证管理证书、私钥 pair 和
`management_fingerprint`。bundle 值同时投影到 Hook Env 与 module Secret Store；Secret 中还保存
模块私有来源与绑定摘要。已有自动绑定的重复 apply 会重新读取固定文件并要求摘要一致；bundle 被删除、
替换或漂移时拒绝继续使用历史 Secret，也不会自动轮换到新 bundle。显式高级远端输入不会被自动值覆盖。

Hook 与 Provider 都拒绝带用户信息、非根路径、查询或 fragment 的 endpoint。Provider 禁止跟随
HTTP 重定向，限制响应为 4 MiB，并要求同步成功响应；异步确认不能被当成已完成的供给操作。
错误仅输出受信操作/状态类别，不回显 endpoint、服务端错误原文或解析失败的元数据。
X.509 解析错误也转为固定类别，避免非法 SAN URI 经标准库错误泄漏证书内容。

## Contract Resource 生命周期

`ensure` 的顺序是刻意的：

1. 先读取 default project 的 `GET /1.0/storage-pools/{pool}`，要求名称匹配、状态为 `Created`，
   且驱动为本版准入的 `btrfs` 或 `zfs`；不支持时在任何租约写入前失败。随后
   `GET /1.0/projects/{sandbox}`。存在则把期望配置合并进现有配置后 `PUT`，不存在则 `POST` 新建。
   合并而不是覆盖，是因为 project 里可能有运行中的实例和运维手工加的 `user.*` 键；已有
   `features.networks=true` 的 project 直接拒绝，要求显式迁移，不自动切换网络归属；
2. **读回**并断言全部受管 project 配置与申请值一致，包括 `restricted=true`、四项 limits 的精确总量、再次读取存储池准入条件，且 network feature 关闭、NIC 为 managed、
   `restricted.networks.access` 恰好等于本租约 bridge。任一不满足立刻返回错误，且**不继续**
   登记证书——这一步是整个契约唯一的信任来源，写入成功不算数，daemon 自己的副本才算；
3. 在 default project 建立受管 bridge 并读回校验，再在租约 project 建立 profile 并读回校验。这一步在证书之前：一个还没有根磁盘和
   网卡的租约，把证书发出去也没用；
4. 读取目标 project 的每个冻结镜像，校验 fingerprint、架构与类型；缺失时只允许从匹配的冻结 supply 导入原始字节并读回，无法供给或失配即失败，不查询 alias；
5. 登记消费者证书。若该 fingerprint 已在信任库中，校验它是 restricted 且 `projects` 恰好只有本
   sandbox；发现它无限制或绑着别的 project 就报错退出，不做任何修改。最后只读核对完整依赖链，通过后才返回 ready。

## network 与 profile

network 名由 sandbox 名 SHA-256 前 10 位派生（`anas` + 10 位十六进制 = 14 字符）。不能直接用
sandbox 名：Linux bridge 接口名上限 15 字符，而 `anas-forgejo-runners` 有 20。派生保证短、稳定、
发生截断碰撞时通过归属校验拒绝复用。

bridge 的所有者是 Provider，API 请求显式使用 `project=default`；租约设置
`features.networks=false`、`restricted.devices.nic=managed` 和精确的 `restricted.networks.access`。
这避免 Incus 7.3 不支持非 default project 内 bridge 的问题，同时限制消费者可引用的网络。
实例、profile、证书作用域和配额仍属于独立租约 project。

网络记录 `user.anas.consumer` 与 `user.anas.sandbox`。已有同名网络若类型或归属不符、缺少归属
标记或挂接外部接口，ensure 拒绝接管且不登记证书。不得仅凭派生名字推断所有权。
重复 apply 保留 daemon 已分配的子网及无关配置；关闭 IPv6 时写 `none` 并移除旧 NAT 开关。
网络创建/更新后读回类型、归属、地址及 NAT，再建立 profile。不同 bridge 本身不证明流量隔离，
跨租约接网、网络写权限和真实出网仍须实机验收。

profile 固定名为 `anas-lease`，只有两个设备：

| 设备 | 内容 |
| --- | --- |
| `root` | `type=disk`、`path=/`、`pool=<storage_pool>`，无 `source` |
| `eth0` | `type=nic`、`network=<派生 bridge 名>`，无 `parent`/`nictype` |

network 的 IPv6 跟随宿主：Hook 只在 IPv6 开关未关闭**且** `HOST_HAS_IPV6=true` 时置
`INCUS_NETWORK_IPV6=true`，此时 bridge 得到 `ipv6.address=auto` + `ipv6.nat=true`；否则显式写
`ipv6.address=none`。写 `none` 而不是留空是有意的——留空会让 daemon 按自己的默认发地址，而给
guest 一个宿主路由不到的 v6 地址，表现是每次出网先等一次超时再回落 v4，看起来像作业卡住而不像
配置错误。两个协议族都经同一张受管 bridge 做 NAT，开启 v6 只扩大 guest 能到达的范围，不改变它
如何出去。

`ensureProfile` 走的是 **PUT 整体替换**而不是合并，`verifyProfile` 随后读回并要求设备数恰好为 2，
配置只有 `user.anas.managed=true`，每个设备的完整属性也必须与受管模板一致。附加 raw config 或设备属性均拒绝。
两者合起来是这条约束的唯一执行点——daemon 不会阻止别人往 profile 上挂东西，所以「profile 上没有
多余设备」只能由这里保证。

第 5 步的两条拒绝是 Provider 侧的越权防线：一张已被以全局权限信任的证书，如果这里默默接受，
消费者拿到的就是整台 daemon。

`inspect` 的 `ready` 要求全部 project 围栏、存储池准入、网络归属/NAT、profile、受限证书和冻结镜像
当前仍然有效；不是只看 project 存在或网络作用域。撤销证书后 project 保留，但不再 ready。
检查不读取供给文件、不导入镜像、不修复配置或重新授权，仍分别保留 restricted 与 quota 标志。
`inspect` 只读，分别报告 `exists`、`ready`、`restricted`、`quota_enforced`。project 不存在时返回
零值而不是错误，因为「不存在」是一个正常的可观测状态。

`revoke` 删除消费者证书，保留 project。删 project 会连带销毁里面的实例，而那些实例从来不属于
本 Contract。对不存在的 fingerprint 删除是幂等成功。

## 配额映射

Contract 说的是每实例上限，Incus project 说的是项目总量，`projectConfig` 用 `max_instances` 相乘
把两者对上：

| Contract | Incus project 键 | 值 |
| --- | --- | --- |
| `quota.max_instances` | `limits.instances` | 原值 |
| `quota.cpu` | `limits.cpu` | `max_instances × cpu` |
| `quota.memory_mib` | `limits.memory` | `max_instances × memory_mib` MiB |
| `quota.disk_gib` | `limits.disk` | `max_instances × disk_gib` GiB |

项目 `limits.disk` 限制的是声明总量，不能证明根磁盘真的限额。实机探查发现 `dir` 在底层未启用
project quota 时可仅警告并继续创建卷，因此本版仅准入状态 `Created` 的 `btrfs`/`zfs` 池；
`dir`（包括可能已配置底层 quota 的 dir）及其他驱动均拒绝。Provider 不探测宿主文件系统、不自动
转换池、不迁移既有实例。其他驱动须补充能力证明后再准入，不表示上游不支持它们。
`inspect` 重新读取池状态；缺失、未就绪或不支持时 `quota_enforced=false`、`ready=false`，保留
project 的 `exists`/`restricted`。读取故障返回错误，不伪装成缺失。该检查不替代 guest 写满验收，
也不持续监控管理员在租约发出后的存储变更。既有证书不会因失败而自动撤销。
[Incus dir 配额前提](https://linuxcontainers.org/incus/docs/main/reference/storage_dir/#quotas)说明底层条件；
完整待测清单在仓库 `test-env/fixtures/incus-network-prototype/e2e-plan.md`。

同时写入的固定限制：`restricted.devices.disk|gpu|pci|usb|unix-block|unix-char=block`、
`restricted.containers.nesting=block`、`restricted.{containers,virtual-machines}.lowlevel=block`。
`incus_container` 额外写入 `restricted.containers.privilege=unprivileged`——系统容器档只是比 VM
更弱的**隔离**边界，绝不是更弱的**特权**边界。

## 证书固定

`newClient` 用 `InsecureSkipVerify: true` 搭配 `VerifyPeerCertificate` 做**精确 DER 比对**。这看
起来危险，实际比链校验更严：Incus daemon 使用自签证书，其 SAN 通常与运维填写的地址对不上，链
校验要么在正确部署上失败，要么只能放宽成接受任意证书。DER 相等意味着只接受 apply 时固定的那一
张，代码里没有任何在不匹配时继续的分支。

同一个 `tls.Config` 携带供给用的管理证书，因此每次请求的身份都在 TLS 层，不在 header 里；错误
路径无从回显凭据。

## 敏感值边界

四项凭据以 base64 PEM 经 `.env` 进入容器环境，Hook 在 `calculate` 阶段就校验它们能解出正确类型
的 PEM 块。所有失败信息只说哪个参数不对，不回显值；`decodeBase64Env` 与 `validatePEM` 都有对应
的回显测试守住这条线。

消费者的**私钥从不经过本 Module**：Runner 只把证书传进来用于登记，私钥直接投影给消费者。

## Hook、变更与回滚

Hook 只实现 `calculate`：派生 `INCUS_NETWORK_NAME`，选择显式远端或固定宿主 bundle 连接路径，并在
四项凭据不完整、自动 bundle 不安全或绑定漂移时拒绝。拒绝发生在 apply 早期，而不是供给中途——
半配置的 Provider 比一个根本没启动的 Provider 更难排查。

`endpoint` 与 `server_certificate_b64` 的变更是 `reconcile`；两项管理凭据是 `credential_rotate`，
且轮换不影响运行中实例。

## 测试与实现位置

| 位置 | 内容 |
| --- | --- |
| `provisioner/client.go` | REST 信封解析、证书固定、not-found 归一 |
| `provisioner/ops.go` | `ensure`/`inspect`/`revoke` 与配额映射 |
| `provisioner/main.go` | 参数与环境校验、隔离档分派 |
| `provisioner/provisioner_test.go` | 假 daemon 覆盖幂等、fail-closed、越权证书拒绝、固定失配、输入校验与敏感值不回显 |
| `hook/main_test.go` | 凭据完整性、宿主 bundle 自动投影、绑定幂等、安全文件拒绝与不回显 |

假 daemon 是 `httptest.NewTLSServer`，因此固定逻辑走的是真实 TLS 握手，不是打桩。

## 当前限制

已在隔离的发行版 Incus 6.0.5 daemon 探查项目、bridge、profile、镜像 metadata 与证书供给，
并观察到 dir 磁盘配额可能被跳过；这不等于 7.3.0 完整验收。guest 启动探查尚未通过，实际配额、
受限证书越权、双消费者及 janitor 仍待 E2E。状态保持 `developing`。

`ANAS_RESOURCE_IMAGE_ALLOWLIST` 由 Provider 核对本 project 镜像 metadata 后交给消费者，镜像固定的最终执行
点在消费者的共享客户端，而不在 daemon。**这是本设计中唯一一条不由 daemon 兜底的约束**——即消费者
被攻破后不再成立的那一条。

「由 daemon 兜底」的含义是：约束写在 project 或证书上，由 daemon 执行，消费者代码完全失守也依然
成立。本 Module 的记分表：

| 约束 | 执行点 | 消费者被攻破后 |
| --- | --- | --- |
| 只能访问自己的 project | daemon（受限证书） | 成立 |
| 配额 | daemon（project limits） | 成立 |
| 禁 device / 挂载 / raw config / 低层配置 | daemon（`restricted.*`） | 成立 |
| 容器非特权 | daemon（`restricted.containers.privilege`） | 成立 |
| 镜像 fingerprint allowlist | 消费者共享库 | **不成立** |

即便如此，被攻破的消费者也只能在**自己那个受限、有配额、无设备、无挂载**的 project 里启动计划外
镜像，爆炸半径被其余每一条约束框住。

> [!NOTE]
> 2026-09-10 已核查上游 main 配置参考：镜像服务器域名限制与 project 镜像隔离不等于逐 fingerprint allowlist。
> 固定 daemon 版本的导入/启动强制效果仍待实测，证据边界见宿主供给架构。
> 若上游存在等价键，应把这条约束下沉到 project，本表随之更新。

### 源码 checkout 与 staging 构建上下文

共享构建使用 `additional_contexts.shared`，默认 `../..` 只适用于保留 `modules/incus` 布局的源码
checkout；它不是对部署 staging 树的源码根探测。CI 的 `check-shared-build` 检查 Dockerfile
COPY、路径存在与 revision 触发配置，不能替代实际镜像构建。

在 staging 树进行本地构建时，必须显式把 `ANAS_SHARED_BUILD_CONTEXT` 指向与该 Module 版本匹配的
**完整仓库源码根的绝对路径**，而不是 staging 根、`modules` 目录或 `internal/computeclient`。
该路径须能被构建端读取，并包含 Dockerfile 引用的共享源码。以 `/srv/src/ANAS` 为例：

```sh
export ANAS_SHARED_BUILD_CONTEXT=/srv/src/ANAS
# 替换为已经渲染且具有对应 .env 的 Incus Compose 文件。
export INCUS_COMPOSE_FILE=/srv/anas/staging/modules/incus/docker-compose.yml
docker compose -f "$INCUS_COMPOSE_FILE" config --quiet
docker compose -f "$INCUS_COMPOSE_FILE" build anas_incus_provision
```

这两个路径均为示例，不代表 Runner 固定使用这些目录。使用源码 checkout 构建也可以显式设置同一
覆盖值，以避免依赖工作目录或复制后的相对布局。运行环境与敏感参数仍由正常部署渲染准备，不要
为了构建把管理私钥填进公开命令或提交源码。

只有发布 Module 工件、没有完整匹配源码时，不能用任意目录或空目录代替 shared context；应使用
该版本发布镜像，或先准备匹配源码再构建。覆盖变量不会下载源码，也不会绕过共享包的离线构建边界。
以上说明已按 Compose 源文件核对，但源码/staging 两条路径的真实构建在本轮均未执行，R-084 仍待验收。

2026-09-18 增加 staging 静态检查入口（代码及用例未运行）：在匹配源码 checkout 中执行
`go run ./cmd/check-shared-build --source-root "$ANAS_SHARED_BUILD_CONTEXT" --staging-root "$STAGING_ROOT"`。
`STAGING_ROOT` 是包含 `modules/` 的实际渲染部署根。检查要求显式的绝对 shared 覆盖值，核对已选择
Module 的 Compose build、Dockerfile、构建目录和共享输入字节；不加载部署环境，不调用
Compose/Docker，不下载源码。缺少模块、源码漂移、被枚举到的符号链接、缺失/新增共享文件不会被当成有效构建。
这是静态预检，不是两条路径的 Docker 构建通过记录。

## compute 镜像配置冻结

镜像配置改为结构化对象：Forgejo 为单对象，AI Agent 为 runtime→对象映射。Core 通过显式
`spec_from` 投影，在 Hook calculate 前解析。运行时容器只接收冻结摘要：Forgejo 读取租约
allowlist，AI Agent 读取 JSON 镜像绑定。Agent Hook 使用与 manifest 参数一致的
`AI_AGENT_AGENT_RUNTIMES`，Compose 向 orchestrator 映射为 `AI_AGENT_RUNTIMES`。本轮未引入新依赖。

Incus 的 `image_architecture` 必须显式描述目标 daemon。受信 bundle 目录当前为空；ensure 在
登记信任前检查租约 project 中现有镜像的 fingerprint、架构与类型，缺失直接失败，不查询 alias
或重建。自动导入/烘焙仍待实现。快照及回滚语义见 [compute 契约](../../../contracts/compute/docs/technical.md)。

HTTP 网络原型 `cmd/incus-network-prototype` 只生成实验产物：指定源地址的 guest /32 路由、
绑定 veth 的入站过滤、限时地址/端口集合，以及既有 Traefik 路由环境字段。它不安装规则，也不开启
生产 ingress。独立 Linux namespace 的 HTTP、来源冒用拒绝、已有流撤销与过期检查已于
2026-09-11 通过；Docker/Incus 规则顺序、真实 guest、IP 复用及完整撤销仍待验收。
TCP/UDP 发布未实施。

## Split 镜像工件离线核验（本机回归通过，实机待验收）

`internal/computeimage/artifact.go`、`release_verify.go` 与 `cmd/compute-image-artifact` 复用统一
`ArtifactRelease` 描述，提供已完成烘焙产物的只读检查，
不运行 distrobuilder、不导入 Incus、不下载、不签名，也不写入受信目录。受信目录仍为空，
`guest_image` 契约、首次烘焙/分发和缺失恢复接线仍未完成。

该 CLI 当前只接受 split 工件；共享库另外支持 unified 描述。按 [Incus 镜像格式](https://linuxcontainers.org/incus/docs/main/reference/image_format/)，
总 fingerprint 是按顺序拼接 **metadata 原始文件字节 + rootfs 原始文件字节** 的 SHA-256，不是
两段十六进制摘要的 hash。描述文件另存每段的 SHA-256 和长度，并绑定 catalog/name/revision、
架构、隔离 interface 和 recipe digest。具名核验检查完整版本键、目标及冻结配方摘要，要求目录
摘要存在；调用方仍负责提供已获信任的冻结快照，校验器不鉴别目录签名。这些比较只证明字节身份，
不证明压缩格式有效、镜像能启动或已通过配额/安全验收。

在匹配源码 checkout 中，对已完成的烘焙产物生成**候选描述**（以下路径均需替换；命令未运行）：

```sh
go run ./cmd/compute-image-artifact \
  --metadata /srv/images/example/incus.tar.xz \
  --rootfs /srv/images/example/disk.qcow2 \
  --name example-guest --revision r1 \
  --architecture amd64 --interface incus_vm \
  --recipe-digest "$RECIPE_INPUTS_SHA256"
```

标准输出是带唯一结尾 LF 的规范 JSON，可由发布流程保存为 `artifact.json`。recipe digest 必须来自
全部已固定配方输入的可信摘要；该命令不会把镜像摘要、任意 YAML 文件或下载来源自动当成配方信任。
已有版本再次检查时应传入 `--expected-fingerprint`，不同字节直接失败，不能覆盖旧版本映射。
候选描述发布仍须经过既有目录历史不可变检查与发布信任流程，单次 inspect 成功没有发布权限。

核验已有描述必须另给来自受信目录/冻结计划的 fingerprint，不能从待核验描述中取值代替信任来源：

```sh
go run ./cmd/compute-image-artifact \
  --verify /srv/images/example/artifact.json \
  --metadata /srv/images/example/incus.tar.xz \
  --rootfs /srv/images/example/disk.qcow2 \
  --architecture amd64 --interface incus_vm \
  --expected-fingerprint "$FROZEN_IMAGE_FINGERPRINT"
```

CLI 支持 Linux/macOS 的普通本地文件，拒绝末端符号链接、特殊文件和读取期间可见的文件变化。
描述文件最多 16 KiB，metadata 最多 16 MiB、rootfs 最多 64 GiB；128 KiB 分块读取并检查取消，
不把大块数据或原始读取错误塞进输出。规范描述拒绝重复/未知字段、大小写别名、null、非规范编码
及尾随数据。单元/命令用例已在本机通过；该工具不构成 M12 或 M13 的实机验收。

## 本地镜像产物归档（本机回归通过，实机待验收）

`cmd/incus-image-artifacts` 在上述只读核验之外提供显式的本地归档写入，复用相同的
`ArtifactRelease` 和 fingerprint 算法，没有第二套镜像协议。它面向发布准备工具，不是安装器、
Provider 动作、浏览器接口或消费者 API；当前支持 Linux/macOS 上由当前执行用户独占的本地目录。
归档和 CLI 的本机回归已通过；以下文件路径是示例，不代表已发布或可启动的真实 guest 镜像。

归档包含 0700 根目录和 `objects/`、`releases/` 子目录，0600 的 `.lock`，以及 0400 的
`.format`、按 SHA-256 命名的对象和规范 revision 记录。会话持有排他文件锁；读写前后核对目录与
锁身份。对象先写私有临时文件并同步，再无覆盖发布，最后提交 revision 元数据。相同 revision
重复记录不改变映射；字节、格式或 recipe digest 变化报冲突，需显式使用新 revision。

在匹配源码 checkout 中，为已完成、来源可信的 distrobuilder 输出建立**新的本地归档**：

```sh
go run ./cmd/incus-image-artifacts init --archive "$ARCHIVE_DIR"
go run ./cmd/incus-image-artifacts record \
  --archive "$ARCHIVE_DIR" --name example-guest --revision r1 \
  --architecture amd64 --interface incus_vm \
  --recipe "$PINNED_RECIPE_FILE" --format split \
  --metadata "$METADATA_FILE" --rootfs "$ROOTFS_FILE"
go run ./cmd/incus-image-artifacts inspect \
  --archive "$ARCHIVE_DIR" --name example-guest --revision r1 \
  --architecture amd64 --interface incus_vm
```

`init` 只接受不存在的目录，不接管空目录或修复损坏归档。`record` 不运行 builder；`--recipe`
读取自包含、已审阅的配方原始字节，限制 4 MiB，配方引用的所有外部输入必须在配方中明确固定。
记录配方文件的 hash 不证明构建实际使用了它；构建来源、签名与发布授权仍由可信发布流程提供。
如需覆盖多文件配方或额外构建输入，应先交付统一的 provenance 清单，不得仅哈希其中一个文件。
Unified 产物使用 `--format unified --image FILE`，不能与 split 的参数混用。

`inspect` 会重新核验完整工件。对象缺失时只能再次 `record` 相同的原始产物恢复，不能现场重建
同名的新字节；已存在但损坏的对象、符号链接和无法确认归属的文件均保留并失败。失败可能留下
无 revision 引用的完整对象或崩溃临时文件，工具不自动 prune，也不据此改变已发布的映射。

生成候选目录必须显式声明历史来源：

```sh
go run ./cmd/incus-image-artifacts catalog \
  --archive "$ARCHIVE_DIR" --previous-catalog "$TRUSTED_PREVIOUS_CATALOG"
```

确实没有历史发布时才使用 `--first-release`，不能与 `--previous-catalog` 同时给出。目录生成
重新检查全部产物，并拒绝丢失或改变旧版本键；上一次目录缺失或损坏不能解释为空历史。stdout 只
包含 JSON 元数据，不返回镜像字节、源路径或配方正文，也不会自动改写 `modules/incus/images/catalog.json`。
受信归档与历史目录需要独立备份；本地产物供给、Provider 导入和受限 prune 已有代码，尚未完成
发布签名、正式分发、真实镜像启动或破坏性 prune 验收。生产目录仍为空，不据此验收 M12/M13。

## 发布目录打包与可取消供给（2026-09-21）

将已验证的完整归档导出为 Provider 的 `images/` 目录，必须明确历史和**尚不存在**的目的目录：

```sh
go run ./cmd/incus-image-artifacts bundle \
  --archive "$ARCHIVE_DIR" \
  --previous-catalog "$TRUSTED_PREVIOUS_CATALOG" \
  --output-dir "$NEW_RELEASE_DIR/images"
```

`NEW_RELEASE_DIR` 须事先存在；确实首次发布时才用 `--first-release` 替换历史参数，两者互斥。
同一归档锁覆盖历史校验和全部已提交 revision/架构/interface 的导出，产物写入
`artifacts/<catalog>/<name>/<revision>/<architecture>/<interface>/`，最后写 `catalog.json`。
空归档、缺失历史、损坏对象或 unified 工件在输出前拒绝，split 文件按原始字节恢复，不重建。
写入固定已打开的目录句柄，实际复制的长度与 SHA-256 再次核验；目的目录被替换则不报告成功。
失败可能留下私有候选目录，须显式检查并选择新的输出目录，不允许重试直接接管。

`scripts/ci/incus-image-release-build.sh` 先核对显式历史和新输出目录，再执行构建，最后调用
同一 bundle 入口，不只导出当前两份目标而遗漏历史产物。工具仅向 stdout 返回数量和目录摘要，
不签名、不连接 Incus、不提供下载 URL 或内联 base64，正式发布信任仍由发布流程负责。

Core 在供给前核对每个 frozen reference/target/fingerprint/recipe，重复 runtime/named revision
只在验证全部绑定后合并相同物理字节，不改写 deployment。split-only 入口拒绝 unified 描述，
供给 JSON 统一限制 1 MiB。哈希和复制复用 apply 取消 context，取消时清理部分复制及临时 staging。
归档到 Core staging、历史保留、同字节多引用、目录替换及中途取消均有合成字节回归，不证明 guest 可启动。

## 发布侧构建一次并归档（未执行真实烘焙）

`cmd/incus-image-artifacts build` 接入 `ArtifactArchive.BuildOnce`，在部署准备之外显式运行
distrobuilder。它不是 `anas apply`、Provider `ensure`、Module Command 或宿主动作，亦不注册
浏览器端点。`record`、`inspect`、`catalog` 仍不启动 builder。

只允许在**独立、可销毁的原生 Linux 发布构建机**上，以 root 执行经审阅的配方和独立核实摘要的
distrobuilder ELF。目标架构必须与构建机一致；VM 构建所需的额外设备和软件须在该构建机准备。
配方可执行 root 命令，因此本工具不是配方沙箱，不能在生产 NAS 上运行不可信配方。
配方须自包含并固定全部外部输入；目前不会自动生成 Forgejo/AI Agent 配方或补齐其镜像目录。

先在可信源码构建此发布工具，再在构建机上显式初始化新归档并执行下列命令（真实烘焙尚未验证）：

```sh
incus-image-artifacts init --archive "$ARCHIVE_DIR"
incus-image-artifacts build \
  --archive "$ARCHIVE_DIR" --name example-guest --revision r1 \
  --architecture amd64 --interface incus_vm \
  --recipe "$PINNED_RECIPE_FILE" \
  --distrobuilder "$TRUSTED_DISTROBUILDER_BINARY" \
  --distrobuilder-sha256 "$TRUSTED_DISTROBUILDER_SHA256" \
  --timeout 2h
```

平台、权限和二进制预检先于 revision 预留。二进制必须是当前用户所有、单链接、不可被组/其他
用户写入的普通原生 ELF；测量后复制到 sealed memfd，通过固定文件描述符执行，拒绝脚本和
符号链接。执行环境显式构造，不继承调用者变量，也不把 builder 的 stdout/stderr 写入结果或日志。
参数固定为 [distrobuilder 的 split 构建模式](https://linuxcontainers.org/distrobuilder/docs/latest/howto/build/)，
容器读取 `incus.tar.xz` + `rootfs.squashfs`，VM 读取 `incus.tar.xz` + `disk.qcow2`。
不启用 `--import-into-incus`，不接受自由附加参数、alias 或目标 daemon。

会话锁覆盖预留、构建和提交。已有 revision 先重新验证原始对象及配方摘要，直接复用、不调用
builder；缺失或损坏时失败，不重新烘焙同一 revision。CLI 的 `build` 仍先做构建机和程序预检，
仅查看已有产物应使用跨平台的 `inspect`。首次构建前，将 0400 的冻结配方、recipe/builder
摘要和版本键写入私有 `build-<版本键摘要>/` 尝试目录并同步；构建后复核配方，再复用归档的
流式哈希及不可变提交。JSON stdout 只有元数据，不包含镜像字节。

失败、中断或进程消失均保留尝试目录；重开归档后也拒绝静默重试该 revision。确认有完整、可信的
原始输出时，可通过 `record` 显式恢复；无法确认时采用新 revision。归档与历史备份不可丢弃。
取消会尝试终止构建进程组，但不证明挂载、子进程或外部资源已收敛；工具不递归删除构建目录。
管理员须先核对并清理构建机残留，不能将失败称为已安全取消。

回归覆盖构建一次、复用、配方冲突、丢失工件拒绝重建、跨会话中断保护、输出恢复、取消及私密错误
脱敏。夹具使用不透明测试字节，只证明编排与字节身份；Linux sealed-ELF 测试、真实镜像格式、
启动、安全围栏、签名/分发及 Provider 导入仍须分别验证。

## 租约命名密钥生命周期

Core 已接入独立的 32 字节 compute `LEASE_SECRET`，与客户端证书分开生成和复用。Deployment 与
resource state 只保存引用；消费者接收敏感 base64 投影，备份恢复保留同一密钥，不参与凭据轮换。
详见 [compute 生命周期契约](../../../contracts/compute/docs/technical.md#独立租约命名密钥)。
专属轮换命令和生产 HTTP 发布仍待实现。


## HTTP 请求文件提交与恢复（代码未验收）

`internal/computeingress.RequestWriter` 为消费者提供原子请求提交与精确撤回。它只打开安装已提供
的私有 0700 租约目录，不创建授权、注册表或挂载。Linux amd64/arm64 以目录锁协调写入，复用
中介的有界严格读取器；完整请求经私有临时文件、同步和 rename 发布。目录最多 256 项（包括
忽略的临时文件），本地保留回执也最多 256 项；未知文件不批量删除。

相同请求重试复用文件，不同任务不能覆盖同一实例端口。回执保留 inode 描述符，撤回时同时匹配
身份与内容，不误删复用同名槽位的新请求。`Resume` 只接管与持久化预期相符的已有请求，缺失时
不重新发布。`Withdraw` 删除意图文件，`Close` 仅释放本地句柄；二者均不证明路由、许可、连接或
地址保留已清理。同步/核验失败按结果不确定返回，不能把文件回执当作发布成功。

共享客户端通过 `Client.OpenHTTPPublisher` 显式配置 `HTTPPublisher.PublishPort/UnpublishPort`。
配置只投影租约范围、公开策略、基础域名、私有请求目录及 random 模式独立命名密钥；不交付完整
冻结授权、middleware、entrypoint 或全局 Store 引用，命名密钥也不进入默认格式化/JSON 输出。
`Policy.Host` 和中介的 `Authorization.Host` 共用域名算法，只有后者核验完整冻结授权。
发布前精确匹配受管 Running 实例和 `user.anas.workload`；`Inspect` 不再取 Incus 名称过滤结果的
第一项，并拒绝重复的精确身份。收到的文件回执及 `RequestedURL()` 不代表网络 ready。
撤销必须使用原 `HTTPPublication` 回执，停止/删除后的实例不必重新启动；旧回执不撤销新请求。

这些客户端检查不是授权边界。生产装配、最小配置投影的自动交付、应用侧恢复、UID/挂载、
只读观测身份和宿主动作仍待交付；自动 ingress 保持关闭。三处共享客户端镜像及 CI 目录已纳入
`internal/computeingress` 构建依赖。除 `request_writer_integrity_test.go` 外，新增
`http_publication_contract_test.go`、`http_publication_projection_test.go` 与
`request_writer_receipt_regression_linux_test.go`，覆盖命名投影/完整授权区分、精确实例、名称冲突、
旧回执、取消、跨 writer 替换与文件边界；均未运行，也未执行运行时构建、镜像构建或宿主验收。

## HTTP 原型的观测与生命周期计划

2026-09-12 新增 `--capture`：在独立 Linux 实验环境经显式 Incus/Docker Unix socket 只读 GET，
结合宿主与 Traefik namespace 的 `ip` 查询，核对租约网络、guest 配置/运行态 MAC 和地址租约、
Docker endpoint 与双向 veth，再比较两轮观测。仅输出选择后的观测与版本/时间记录，不保存完整
Docker 环境或管理凭据。此入口需要实验宿主权限，不是生产中介的受限只读身份，也不改变 Module
或消费者容器的 socket 挂载。

`--previous`/`--withdraw` 增加发布、替换、撤销与失败回收计划。撤销保留默认拒绝表，同拓扑更新
以单次 nft 事务替换；拓扑变化仅生成撤销，需先结束旧实验。输入拒绝重复/未知字段、过大文件及
最终文件符号链接；产物独占写入新目录。`publication.json` 只记录拟发布事实，不是已应用回执。
域名预占、HTTP 探测、地址保留和清理确认仍是计划中的操作者步骤，没有自动执行或重启对账。

当前仍是单条 `.example.test` HTTP 实验路由，未开放生产 ingress 或 TCP/UDP。本轮按要求未执行
编译门禁、测试或服务器验证；文档生成不计作验收，此前 namespace 记录不覆盖这些新增路径。用法与待测清单位于仓库
`test-env/fixtures/incus-network-prototype/README.md` 与 `e2e-plan.md`。

## HTTP 授权与中介代码进展

Core 现已解析可选 `spec.ingress`，在 deployment 的 `compute_ingress` 冻结端口、认证、域名、
租约身份与密钥引用；启动/激活仍拦截带 ingress 的消费者。域名支持 fixed/named/random，random
取 HMAC-SHA256 的前 128 位；跨租约及已知服务域名冲突在准备期拒绝。`auth: none`（默认）
不提供访问控制；SNI、Referer 与日志可能泄露 URL，不得发布敏感或可写服务。

实验命令的 `--mediation` 已接入 Linux 受约束请求读取、授权核对及进程内域名预占，发布计划
使用冻结的 ForwardAuth middleware；撤销计划仍先移除路由，再撤许可及连接。它不运行 renderer，
不安装规则，也没有生产只读身份、请求目录挂载、IP 保留或 watcher。详细字段、限制和实现边界见
[compute HTTP 授权说明](../../../contracts/compute/docs/technical.md#http-授权冻结与实验中介尚未开放运行时)。
本轮新代码尚未测试；双语文档生成不代表编译门禁、单元或真实宿主验收通过。

Core 活动授权适配器继续补齐：读取器在共享运行锁下验证活动 deployment、绑定与镜像冻结；
实验 `--register-requests` 可新建独立租约请求目录，workspace 模式从 Core 核对授权并按需取得
单条命名密钥，采集后重新检查代次。登记不含密钥、不执行挂载或网络变更，也不能替代生产中介
的只读身份、窄范围密钥交付与持续对账。该代码尚未测试，详见上面的 compute 契约链接。

`internal/computeingressruntime.Executor` 已编码可恢复的发布顺序：地址保留、guest `/32`、窄 HTTP
许可、后端探测、Traefik 路由；撤销依次删除路由、许可、存量连接、`/32`，最后才释放地址。每一步
完成后经 `StateStore` 持久化，重启先清理不属于当前 Core epoch 与新鲜观测交集的记录。文件 renderer
只以 no-overwrite 方式发布按 reservation 派生的 YAML，并拒绝覆盖或删除不同内容。

上述代码是受信执行内核，不是已经运行的 daemon。类型化宿主动作、受限 Incus observer、探测、
事件源和消费者目录挂载仍待接入；本地互斥与周期循环见下段。生产 ingress 继续禁用，本轮未编译或测试。

2026-09-13 补充撤销意图持久化：执行任何清理前记录 `retiring`，重启后即使原请求恢复有效也先
完成撤销。地址保留、路由或许可动作失败，以及探测前观测失效，都进入同一清理路径。状态文件
严格校验 schema 与小写 64 位十六进制 epoch；写盘失败仍需恢复处理，不能据此认定网络已关闭。

## 受限宿主观察（2026-09-21，代码接线，未自动安装）

`incus.ingress.observe_http` 已进入 `anas-hostd` 的编译清单：只读、30 秒、reject 并发，无自由
endpoint/路径/命令输入。它复用既有共享 job、安装对端身份、审计和退出监督，不新增服务或 root 入口。
`HostObservationInvoker` 每次新建 job；取消等待不取消已归属宿主执行，历史结果不能成为本次观察。

本机 root 后端只读取 `/etc/anas/incus-ingress/observers/<workspace-id>.json` 中的已安装 scope，
并从受保护的服务配置取得工作区路径，核对当前 Core 快照和受管连接 bundle。scope 与 job 工作区
必须一致；文件/祖先、现有共享锁和内容在观察前后复核。Incus 必须已运行；管理凭据留在 root
进程内，通过固定 `127.0.0.1:8443` mTLS 使用，不交给中介，也不称为 daemon 的只读证书。

后端复用 Incus 双采样，再核验实际受管 bridge 上的 veth、数值 ifindex、peer index 和 MAC，重复
两组 API/内核样本且完全一致才返回选定租约。当前只支持 container，VM/TAP 明确拒绝。
投影 v3 增加 workload/interface 绑定，不兼容旧 v1/v2；`server_uuid` 是既有 ANAS 随机安装 ID
的 UUID 形表示，同时受 bundle 摘要固定，不是 Incus API 提供的字段。

`HostProjectionReader` 可作为 `FactReader` 和 `Observer`，自身不持有 Incus 凭据或 socket，
必须匹配固定安装 ID、Core epoch、完整租约策略及每次请求的 observation ID。scope 的受确认
自动创建/刷新与撤销见下一节；生产中介启动仍未接入，也未证明真实 root、VM、health、IP/ifindex 连续生命周期或长连接。
发布仍关闭。完整安装 schema 与边界见[宿主供给设计](../../../docs/architecture/incus-host-provisioning.md) §7.8。

## 中介生命周期与 Host 读取器装配（2026-09-21，内部接口）

`ReaderInstallation.Host` 选择不携带 Incus 凭据的独立交付格式
`anas.compute-http-host-reader-credentials/v1`。文件保留活动快照、租约命名密钥与 Traefik
读取配置，只加入宿主 scope/安装 UUID pin；与直接 Incus 配置互斥，不从失败的宿主调用回退
直连。`OpenHostWorkspaceReaders` 由可信启动方另行提供共享宿主动作客户端，文件不能选择
invoker。现有直接读取器的 v1 格式保持不变。两种模式都校验交付文件身份，关闭后拒绝继续使用。

`WorkspaceReaders.NewControllerService` 连接同一 Source、Observer、renderer 和读取器生命周期。
宿主动作、probe 和 StateStore 必须显式提供；构造不启动服务，`Run` 由可信所有者调用。
启动恢复及第一次完整对账后才发出 Ready；这不是持续健康证明。Stop 使用独立超时排空，
不能先关闭清理需要的宿主服务、Traefik 凭据或挂载。

失败返回 `ErrControllerDrain`，原 journal/flock 与旧读取器继续保留，Run 等待显式 RetryDrain；
重试只清理原目标，不重新读取请求或发布。取消等待不取消排空，并发重试共用同一轮操作。
清理和库存确认后才关闭读取器并退出。尚未接入真实 daemon 启停、UID/挂载或 observer 配置
变更协调；生产 ingress 仍关闭。完整范围见[宿主供给设计](../../../docs/architecture/incus-host-provisioning.md) §7.10。

## 观察配置的自动生成与确认交付（2026-09-21）

无需手写 observer scope。已安装且启用 `host_actions` 的宿主，通过已有控制台会话提交配置
计划，工作区必须已登记；refresh 还要求活动运行部署、受管本机 Incus 和 container ingress。
以下入口已经接线，尚未在真实宿主验收，也不会启动生产入站：

```sh
anas host incus-plan -w main --phase observer \
  --request-json '{"operation":"refresh"}' --session-json - --json < "$SESSION_FILE"
```

等待共享 job 成功并审阅其版本、deployment、epoch、旧/新摘要、租约数和 recovery 标志后，
使用原计划生成一次性批准（会话文件须私密，不能提交到仓库）：

```sh
anas host incus-confirm -w main --plan-job "$PLAN_JOB" \
  --action incus.ingress.observer --session-json - --json < "$SESSION_FILE"
anas host incus-apply -w main --phase observer \
  --request-json - --json < "$APPLY_ENVELOPE_FILE"
```

Apply envelope 沿用既有格式，含 `session`、`plan_job_id`、`confirmation_token`、计划结果中的
原始 `parameters`。不要把 token 放在参数或日志中。过期或状态变化须重新计划，不重复使用旧批准。
HTTP 对应原 `/api/v1/workspaces/{ws}/host/actions/incus/observer/plan` 与 `/apply`，plan 的
`request` 仅有 operation；工作区从受授权 URL 提取，不能从正文选择另一个工作区。

Root 根据固定安装状态和活动部署生成 `0600` scope；凭据不复制进去。既有宿主状态的
`observer_scopes` 记录先进入 pending，文件通过已打开目录原子交付并读回后才 enabled。
重复相同配置不重写；中断后必须重新计划，且只能收敛到已登记的原/待提交内容。未知文件不接管，
旧手写 scope 没有归属回执也不能当作新授权。旧二进制可能拒绝新增状态字段，禁止删除记录强行降级。

撤销使用同样流程，仅把计划请求改为 `{"operation":"disable"}`。它不需要 daemon 在线或旧
deployment 可读，保留 disabled 墓碑，旧文件回放不能复活。卸载 Incus 前必须先撤销这些 scope。
配置授权的撤销不等于网络许可已经排空；中介启动/旧路由清理、UID/挂载、health、VM/TAP 和
实际网络生命周期仍需后续实现及验收。生产 publication gate 继续关闭。

## HTTP 周期对账与渲染接入（本机流程回归，生产未验收）

`Controller.Run` 持有 `FileStateStore` 的独占 flock，串行执行初始清理、周期对账和退出清理。
同一 ingress 必须统一使用同一个私有本地状态根，不能各建一个目录规避互斥；不支持跨宿主选主。
锁文件不删除，目录/锁 inode、owner 或权限变化会阻止进一步操作。退出清理使用独立的超时上下文，
在清理超时前继续持锁；失败记录留盘。已退役 token 持久化且永不按 TTL 自动清除，状态上限 4 MiB。

`WorkspaceSource` 从 Core 和登记目录重读当前请求。每租约最多 256 个目录项、每轮最多 1024 个
JSON 请求，同一实例端口不能被两份请求占用；省略/撤销的请求不进入期望集合。新 token 只用于变化
的内容或目标，同内容不在每次轮询重新发号。全部清理确认后，循环才允许清空 token 缓存并通过新一轮
完整授权/观测恢复仍有效的请求，用于停止后启动或暂时故障恢复；旧 token 仍不可复用。
执行步骤前重读请求与 auth/Host，另核验 UUID/IP/MAC；
完整读取失败则撤销原型中所有记录，不能用上一次快照续期。事件只是唤醒信号，周期读取仍会执行。

文件 renderer 现在构造 `ANAS_TRAEFIK_ROUTE__*`，调用受信 entrypoint 的仅渲染模式，复用已有
模板。候选文件先生成在私有临时目录，子进程使用干净环境，最后独占写入动态目录；共享模板变化
造成旧内容不匹配时拒绝覆盖或删除。文件落盘不证明 Traefik 已消费配置；renderer 强制要求
`RouteConfirmation` 实际消费/撤销确认，缺少适配器即拒绝执行，确认失败不允许后续释放地址。

2026-09-21 增加路由消费后的独立实例与授权复查；失败不能保存 ready，按原顺序撤销。
实际本机联合回归使用 Controller、Planner、固定证书 mTLS 与文件 journal，覆盖暂停/停止、
相同身份恢复后换新 reservation、取消后的独立清理，以及清理失败保留地址/retiring 回执、重开后
继续撤销。测试的请求源、daemon 元数据、HostActions、renderer 和 probe 是明确的适配器；
没有执行实际 Core 请求目录交付、Traefik 文件消费或内核流量，不能把复查当作消除所有可见性窗口。

服务端受限只读身份、完整宿主/探测适配器、孤立工件恢复和服务安装仍是必需前置。当前没有启动
生产 daemon、改变全局授权策略或开放 ingress，没有增加 Go 依赖。

## HTTP 实例事实读取（本机 mTLS 回归，实机未验收）

`IncusFactReader` 已提供可注入 `WorkspaceSource.Facts` 的 GET 实现。固定 HTTPS endpoint、证书
和租约映射来自管理员安装配置，消费者请求不能指定它们。最低 TLS 1.3、服务端精确证书 pin、双方
证书有效期检查；拒绝重定向、环境代理及非成功 JSON。每个 GET 限 8 秒/2 MiB，完整双采样限 30 秒，
错误不包含 endpoint、私钥或响应正文。连接失败不回退到 Unix socket。

安装授权必须完整匹配，调用方不能在保留 project/前缀的同时扩大端口、替换 deployment、auth
或域名策略。请求的 workload 必须与实例自身 `config` 中的 `user.anas.workload` 相同，且
`user.anas.managed=true`；不从 profile 或请求文件补造该事实。这些标签不是跨项目安全边界。
JSON 保留上游扩展字段与大小写敏感的 map 名称，但拒绝选定字段的大小写别名；读取响应后再次
核对取消与证书期限。`IncusFactReader.ValidateTarget` 接入执行器 Observer，每次都重新观察，
不缓存成功结果；活动 Core epoch 与 auth/Host 仍由独立授权源校验。

读取范围是服务器身份、选定 project、该租约的 default-project bridge、选定实例/状态及 bridge
分配表；验证版本、restricted 围栏、bridge 归属/NAT、唯一受管 NIC、MAC 和唯一私有 IPv4 分配。
两次选定事实必须一致，不能只看地址落在子网内。UUID、generation 和最后启动时间派生的 incarnation
写入执行目标并在每步重查，覆盖 UUID/IP/MAC 不变的快速重启；缺失字段直接拒绝。此摘要不是地址
保留，宿主仍需独立验证当前映射。旧实验回执缺少 incarnation 时拒绝读取，不自动清理外部工件。

普通 Incus restricted TLS 证书仍有本项目写权限。GET 代码不构成服务端只读身份，专用授权供给仍待
实现；未更改 daemon 全局授权或启用中介。字段依据 Incus v7.3.0 官方 API，版本 pin 不代表兼容性
验收。本机真实 mTLS 测试使用合成 daemon 响应，覆盖六类 GET 的双采样、错误身份/授权、暂停、
重启、地址变化、传输/JSON 反例和错误脱敏；没有证明实际 Incus 版本可用或身份只读。
本机及实机范围分别列于 `test-env/fixtures/incus-network-prototype/e2e-plan.md`。

### 内部宿主观测的逐次绑定

尚未生产接线的 `ProjectionClient` 使用 `anas.incus-http-host-projection/v2`，每次调用自行生成
32 字节随机 `observation_id`，要求响应精确匹配并限制为 30 秒上下文；不接受调用方指定标识、
旧版或缺失标识响应、先前调用的响应，以及取消后到达的成功结果。标识不是持久凭据或幂等 key。
真正宿主处理器仍须独立授权，并在本次调用后读取真实状态，不能给缓存数据换新标识冒充新鲜。
本轮没有注册该 root 动作、改变现有 journal/宿主回执格式或提供新特权入口；完整装配继续待办。

## Traefik 库存与消费确认（代码未验收）

`TraefikReader` 现可同时注入 `WorkspaceSource.Inventory` 和文件 renderer 的 `Confirmation`。
Controller 传入当前持锁 Journal，库存候选来自有效执行回执；只有完整受信模板产物、owner 摘要和
实际 router/service/auth 全部匹配才排除自有路由，不能只认文件名。动态目录、入口脚本及文件还要求
root 或当前执行用户拥有、无共享写权限。孤立工件仍需后续恢复流程。

读取器只使用既有受 BasicAuth 保护的 HTTPS API，固定证书及 3.7.10 版本；不创建 API 或监听端口。
GET 限 8 秒、完整 rawdata 限 4 MiB；在 12 秒内要求两次间隔 250 ms 的完整匹配快照，并核对实例
启动时间与 API router/auth。HTTPS Host 库存支持 Host/Path/PathPrefix、括号和布尔组合；无界或未知
规则、多层路由和 HTTPS TCP 抢占均拒绝，warning/disabled 路由仍保留其 Host。

动态文件发布前验证 Host/槽位及 ForwardAuth，加载后再核对唯一 HTTPS/TLS router、精确后端、
service 与认证。ForwardAuth 必须是直接 middleware，且完整动态定义匹配可信安装输入的规范 JSON
摘要（含版本默认值，排除运行状态）；不能通过现场 API 自行建立信任。缺失摘要、定义漂移或 chain
直接拒绝。撤销需要文件及 API router/service/直接引用均消失，失败继续保留清理回执和地址占用。

Traefik 默认把构建的后端标为 UP，这不是 HTTP 探测或真实权限验收。自动凭据供给、完整服务安装、
宿主动作和真实 BackendProbe 验收仍待完成；未运行上述路径，未开放生产 ingress。

## HTTP 私有配置与 fixture 探测（代码未验收）

`runner.DeliverComputeHTTPReaders` 已提供 Core 侧私有文件交付，`OpenWorkspaceReaders` 用它连接
Incus 事实、Traefik 库存/确认和命名密钥。文件只含当前活动 random 租约需要的 key、安装方提供的
专用观测证书/版本、Traefik API 凭据和冻结 ForwardAuth 摘要；拒绝多余/缺失的 key 或摘要。中介
不读取完整 Secret Store；固定和具名模式同样必须匹配完整活动快照。安装方仍需供给服务端只读
授权、可信 pin 和正确 UID/挂载，代码没有自动签发证书、启动进程或开放 API。

文件限 1 MiB 规范 JSON，父目录私有且属于执行用户，文件 0400、单硬链接；不覆盖既有目标。
交付前后重查 Core 与命名密钥来源，每个 API GET 前后重读文件并比较目录/文件身份、权限和摘要。
新 epoch 需要新交付；旧路由清理期间旧配置必须保持有效。配置缺失/替换使确认失败并保留地址占用，
不能先删凭据再要求撤销。此原语尚未接入安装和轮换流程，也未实际运行。

`FixtureHTTPProbe` 只支持 `.example.test` 的预登记 fixture。每个完整目标（含 token、epoch、
UUID/incarnation、MAC/IP/端口）绑定独有响应长度/SHA-256 和安全路径；最多 1024 项、路径 256 字节、
响应 16 字节至 64 KiB。拒绝首次响应自建信任、不同目标复用摘要、仅凭 HTTP 200 或 TCP 连通成功。
发送前后重查请求/授权和独立 Incus 事实；响应可复制，因此仍依赖宿主地址保留与映射，不能当成
恶意 guest 的密码学身份证明。

可信启动方需预先把探测进程放入 Traefik netns，并提供 PID、启动 tick、boot ID、netns device/inode、
独立取得的 socket namespace cookie 和入站 IPv4。Linux 检查真实 procfs/nsfs 及实际 socket 的
`SO_NETNS_COOKIE`；PID 复用、进程停止/重启、namespace 漂移、不支持的内核或非 Linux 均拒绝。
代码绑定来源 IPv4，直连精确 guest 端口，只发送无凭据 GET，禁用代理、重定向、压缩和连接复用。
连接/响应头各 3 秒、HTTP 交换 5 秒、完整探测 25 秒，不延长宿主许可，不执行 setns 或宿主命令。
它不证明 Traefik 自己的路由选源或公网 TLS/认证；这些仍需真实请求验证。

复用已锁定的 `golang.org/x/sys v0.47.0`，仅改为直接依赖。生产启动身份/cookie 供给、guest 内
服务准备、宿主动作、生产应用探测、孤立工件恢复与服务安装仍待接入。以上仅完成代码、格式化和
文档生成，测试/门禁/服务器操作暂缓；用例见 `test-env/fixtures/incus-network-prototype/e2e-plan.md`。

实验准备新增两个入口（仅代码，未运行）：`--capture-probe` 用完整实验 Docker 容器/网络 ID 读取
一致的 PID、源地址和分配，在已处于目标 netns 的进程内锁定线程采集内核 cookie，前后重查进程/
namespace。输出来源 ID 与时间，不执行 setns，不启动服务；管理员 socket 不交给中介，生产启动
身份供给仍待接入。

`--prepare-fixtures` 在同一状态锁下读取真实 WorkspaceReaders 的 example.test 目标，存在未撤销
publication 时拒绝。它预生成独有响应、安装指引和私有 0400 登记（1 MiB/1024 项），前后检查当前
授权、请求及独立实例事实；不探测或发布路由。文件仍需安装进对应 guest 的 HTTP 服务。登记失败
保留私有准备文件供检查，不能当成网络就绪。

新增 `NewRegisteredFixtureHTTPProbe`，持久登记保存除 reservation 外的完整目标，探测时再绑定
当前有效 token。文件/Core 前后重查，只有 epoch、请求、实例 incarnation/MAC/IP、端口、Host 和
认证都匹配才使用期待值；中介重启可用新 token，guest 重启或身份变化须重新登记。旧 token 的
退役禁令保持不变。静态 `NewFixtureHTTPProbe` 仍只用于单会话完整目标。宿主动作、guest 内服务
安装和完整 E2E 继续待办，新命令说明见实验目录双语 README。

## 共用动作调用服务（代码未验收）

`internal/jobexecutor/module_action_dispatcher.go` 为后续 CLI/HTTP 适配提供统一调用、查询、
订阅和取消服务，执行与持久取消复用现有 ModuleActionWorker，不新增第二条执行队列。
订阅断连不会取消任务，历史 job 按当前权限读取；Module 服务不消费或暴露宿主命名空间动作。
内部代码与回归源尚未运行，主 daemon、现有客户端与 root 宿主通道未接线，因此不改变 Incus 的
developing 状态，也不开放生产 ingress。确认 token、产品入口迁移与实机验收仍是独立待办。

注册表与 dispatcher 已接入同一 store 的动作级幂等及 `coalesce/reject/queue`。key 不按操作者或
入口分区，workspace 与完整冻结参数不同则冲突；每次重试仍再授权，返回既有 job/冲突前校验
读取权限。pending/running key 不超时，终态后保留 1 小时；绑定时间和归属链随 job 压缩/恢复，
时钟回拨不能复活已被替代的旧归属。新别名经实际加入者审计后原子提交，每 job 最多 64 个，
超额拒绝而非淘汰旧键；无键合流不生成别名。逻辑到期不代表删除 job 或完成全局存储配额。
相关测试源码已补写但未执行，这些非特权 Module 能力不替代 Incus 的宿主网络适配器。

## HTTP 外部工件恢复（代码未验收）

`Executor.Recover` 在同一状态锁内撤销未完成回执并确认完整外部作用域清空。Controller 启动、
故障与退出共用该路径；开通/续租前后及地址释放前强制执行 `HTTPArtifactInventory`。未知工件
阻止新发布与地址复用，已知路由/许可/连接可先关闭；失败保留 retiring 回执。tombstone 只防重放，
不作为清理候选；缺失状态不是外部干净证明，损坏状态不覆盖，也不自动导入未知归属。

文件盘点使用专属受信目录（不能混放 auth 等配置），最多 4096 项、30 秒；文件全字节和目录身份/
项集在实际 API 读取前后核对。只有完整匹配回执、文件和已加载 HTTP router/service/auth 的对象
才被认可。其他 provider、协议和服务间引用使用 `anas-compute-` 保留前缀均拒绝；仅允许已核验
ForwardAuth 的对应 `usedBy`。保守扫描也可能拒绝使用该前缀的无关字符串，不输出原始 API 配置。

撤销补充规范临时文件清理：名称、完整渲染字节与当前未完成回执一致才删除，删除前重查身份、
之后确认不存在并同步目录。部分写入、模板变更和未知工件不按前缀清除。API 撤销确认还覆盖服务间
和其他动态 section 的引用；文件成功删除不足以释放地址。

宿主盘点仍是必需接口，尚无真实适配器。独立网络/分配证据、默认拒绝基线、IP 保留及受限管理员
恢复动作仍依赖宿主通道；没有空实现或第三个提权入口。文件/API/内核不能原子采样，竞态、故障、
两档 guest 与公网双栈需真实验收。代码未运行，相关用例已登记，生产 ingress 保持关闭。

宿主动作前置已开始实现 `internal/actionabi` 的有界请求/事件协议、job/invocation 绑定、重放序列
与实际进程终态校验；强杀和不完整输出不能报告 cancelled 或成功。共用 `consolejobs` 已补内部
动作 binding/独立序号、原子事件/截断/终态、重放和重启 unknown 恢复；`jobexecutor.ActionRecorder`
要求动作公有投影，终态在实际 EOF/退出核验后才提交。没有第二份 job store 或新增依赖。动作
生产 dispatcher、Module Command/CLI/HTTP 迁移、授权/审计策略及宿主通道安装仍待接线与验收；
Module 注册表和 Linux 受监督进程处于内部编码阶段，不能作为宿主执行通道；
普通 worker 暂不领取动作 job，HTTP 宿主适配器仍不可启用。新代码均未编译/测试，继续仅编码与
文档生成，不运行门禁或服务器操作，生产 ingress 保持关闭。

2026-09-18 增加共享 job store 的执行失联阻断：动作 unknown 的原因是
`execution_containment_lost` 或 `daemon_restarted` 时，普通及动作启动入口均拒绝新执行，包含
同一 store 中的其他 workspace 和只读任务。读取、重放和取消排队任务仍可进行。阻断依赖持久化
回执，不因压缩、重开 store、重建 registry 或普通业务补偿确认消失；它不代替残留进程/writer
清理证明，受限恢复与服务接线仍待实现。回归测试源已补，尚未运行。

## 入站宿主内核身份与回执（2026-09-20）

宿主后端已补完整 Target 与安装拓扑摘要、v2 回执、dirfd-relative 安全写入及可取消 guard。
namespace 执行器独立核对 Docker ID/启动时间、PID/start tick/boot ID、nsfs device/inode 和 socket
cookie；固定 ip 命令在打开的 namespace 内执行，不接收外部路径或命令。容器 source IP 与宿主
bridge gateway 分开观察，夹具扩展 JSON 不作为生产 ip 输出。多端口只共享同一完整分配身份，
旧 hold 未释放时拒绝重启或其他实例复用同一 IP。

回执使用 `anas.incus-http-host-receipt/v2`，旧 v1 不静默迁移或清理。真实 allocator 生命周期、
health 身份、生产装配和原生验收仍未完成，生产 ingress 保持关闭。前轮专项、全仓 Go 回归与 Linux
双架构编译通过，不代表新增 native CI 门禁或实机运行通过。当前核对见
[实现恢复记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-20-incus-ingress-recovery.md)。

## 受管 nft 防火墙与安装归属（2026-09-20）

入站后端只拒绝受管 bridge 路径，不安装全局 `policy drop`；许可先进入 regular `http_permits`
链，未命中才拒绝。读回核对完整 ordered AST、表/链/handle、所有动态对象及 native JSON 中的
顶层 comment、numeric timeout、concat 与 expiry，不凭名称过滤掉未知规则。空/过期集合能撤销，不能报 ready。

独立 `.nft-baseline.json` 记录 installing/installed/removing/removed 和真实 table handles。
安装前确认不存在，变更前保存意图，读回后才完成；卸载要求 publication、route、permit 和 connection
均已清空。没有独立回执的既存表不接管，失败意图不自动重试。复用私有 dirfd 文件原语，无新增 Go 依赖。

本机专项、全仓 Go 和竞态回归已通过；原生 namespace/nft CI 用例已编写且拒绝 skip，本轮未运行。
该原生用例仅测试隔离 namespace 中的 nft 语法、JSON 和生命周期，IP/allocator/conntrack 使用夹具，
不是实际 HTTP 流量、Docker/Incus 共存、双栈或地址复用验收。生产 ingress 仍关闭。

界面只使用实际 public job DTO 的 `kind`、`mutating`、workspace/id 和 result，不依赖内部 `job.action`。
显示的计划还需与批准 binding 的 schema、workspace、计划/状态摘要和待删集合一致。合法的空库存
显示“无变更”且不能执行；输入变更、计划过期或组件卸载均不能沿用旧确认。确认 token 不进入公开状态，
执行结果不确定时不自动重试。公开响应缺字段或绑定漂移会拒绝，不通过类型断言兜底。


## 设备绑定的宿主地址路由（候选，生产关闭）

`address_routing` 是 root 安装投影，不是消费者配置。独立 host routing table 与终止 unreachable
rule 只处理指定 Traefik 源、guest 子网和 Docker 入接口。永久邻居与 `/32` 绑定已核验的 container
host veth；设备删除后不重建旧 reservation 的路由，防止沿普通 bridge 路由把前向请求误送到复用地址。
这不是 DHCP reservation，也没有修改 Incus pool 或 guest 设备。

`.address-routing.json` 记录完整作用域、ifindex、分配意图、共享端口和退休 token；回执与内核读回
共同决定就绪。`address_intent` 在外部效果前保存，正常及中途失败都经同一撤销路径。创建、续租、
盘点与释放已接入 Backend；没有独立 kernel hold 的生产调用不能只凭 journal 报 ready。
已有 table/priority/neighbor 不接管；未知更早 policy、本地目标、替换设备和未解释的工件均拒绝。

上限为 32 分配、每分配 64 使用者、退休加活跃 token 共 256；准入预留清理容量，不在 apply 或重装时
清空历史。准入以编码后的 60 KiB JSON 预算在 64 KiB 文件上限内预留清理空间。
地址层本身仅提供 container veth 的前向候选；回复侧补强见下节。VM/TAP、完整旧 TCP 会话、
真实 Incus 观察、health 和生产服务装配仍待完成。原生 FIB 测试已加入门禁但本轮未执行；生产 ingress 保持关闭。
详见[地址路由核对](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-20-incus-address-routing.md)。


## 回复来源与双向连接清理（候选，生产关闭）

配置了 `address_routing` 时，bridge `http_reply_origins` 先核验原始数值 ifindex、veth 名、guest MAC
与获批 IP/端口，再允许回复到 Traefik 后端源地址；未命中明确拒绝。原始 index 来自独立地址 hold，
清理时不会学习同名替代设备身份。inet 请求/回复另限定 conntrack original/reply 方向。

两族定时许可同事务创建、续租、撤销，两侧完整读回后才就绪。缺失、过期或外来对象不能报 ready。
删除连接前必须确认两侧许可撤销，逐条绑定原/回复地址、端口和默认 zone，单次最多 256 条，之后读回
无残留。翻译元组、非零 zone、offload 或异常库存阻断；目前只针对无后端 NAT 的直接 IPv4/TCP。
旧策略摘要不兼容新 `bidirectional-origin-v1`，不静默迁移回执或接管遗留对象。

原生包与 conntrack 测试源已进入门禁但本轮未运行；它们分别测试来源规则和内核记录，不等于完整
TCP 会话、Incus guest 或 Docker 共存验收。ifindex 强制/回绕复用、接口仍存在的停止/暂停、VM/TAP、
独立 Incus 身份供给、health 及生产装配仍待完成。生产 ingress 保持关闭。见
[回复来源核对](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-20-incus-reply-origin.md)。

## 配置变更屏障与共享服务停机（2026-09-21，生产未开启）

共享宿主队列已连接 `ControllerCoordinator`。observer 配置任务先封闭目标工作区的新中介启动，
再排空旧实例；宿主供给、卸载和镜像 prune 涉及共用 daemon，排空全部登记工作区。等待期间配置
任务保持 queued，不占用 root 执行位置，旧清理依赖的只读任务仍可执行。

排空成功后重新核验权限，broker 还会校验屏障绑定的 job/调用/参数。原计划、一次性确认和退出
监督不变。失败拒绝配置执行并保留原锁/读取器；未知执行不释放屏障，新实例不会自动启动。
正常服务取消先拒绝新配置，保持队列和执行租约到旧中介清理完成；失败需可信所有者显式调用
`RetryIngressShutdown`，该方法不是新的 Web API。取消等待不取消已经开始的清理。

这些约束已由真实本地 Controller、文件日志、锁、确认账本和共享队列测试覆盖，网络/root
执行仍为夹具。launcher 必须共享同一个协调器；空的内存登记不证明外部工件为空。生产启动、
UID/挂载、root 网络动作、health、VM/TAP 和异常跨进程恢复仍未交付，不能由此启用 ingress。
完整范围见[宿主供给设计](../../../docs/architecture/incus-host-provisioning.md) §7.11。

## 独立进程的工作区写入围栏（2026-09-21，生产关闭）

工作区 Controller 装配复用 `.anas/state/lock` 和原 HTTP 日志。运行前持久化未清理标记，运行中
持共享锁；完整排空与旧读取器关闭后才清除标记，不删除锁。独立 CLI 的凭据/本地管理员轮换和
其他经过 Runner 写锁的操作检查同一描述符，不能因进程已退出或 `--force` 而跳过待恢复状态。
纯共享读仍能用于诊断，需要迁移/恢复写入的读入口则仍须等待安全状态。

标记固定原日志目录路径摘要与设备/inode；缺失、损坏日志或同路径重建的目录不能伪装成空安装。
真实子进程强杀用例只验证文件锁与持久记录，宿主网络仍为测试适配器。不新增 CLI 自动排空 RPC，
不启动服务或开放 ingress。必须由可信所有者完成原 Controller 的停止或恢复，不能删除标记、
替换锁文件、降级到不识别围栏的旧 writer 来恢复写入。现有操作的 `runtime_lock_failed` /
`runtime_lock_unavailable` 错误保留，说明中会提示 HTTP ingress 活跃或需要恢复。

这不是最终非特权 UID/挂载装配，也不约束主动忽略锁协议的程序或受信管理员。通用裸
FileStateStore 仍是实验原语；工作区读取器装配不能选内存日志绕过围栏。细节见
[宿主供给设计](../../../docs/architecture/incus-host-provisioning.md) §7.13。

## 部署与维护任务的排空互锁（2026-09-21）

`anasd` 的普通部署/维护 worker 与宿主任务共享协调器。部署切换、启动/停止/重启/回滚、
本地管理员轮换、Module 变更及快照任务先排空本工作区；等待仍为 queued，不占运行槽或
工作区写锁。失败返回 `ingress_drain_failed` 且没有 started_at；权限失效返回
`job_authorization_revoked`。任务取消不取消已开始的清理，不让后续任务抢占旧屏障。

应用返回或 success 事件不等于终态已落盘；确认终态并完成所需补偿后才释放屏障。启动临时
身份只保留原事务 apply 权限，不能因排队自动获得管理员权限。独立本地 `anas credential rotate`
与其他进程仍未接入本屏障，不能据此开放生产 ingress。新未执行失败记录可能被旧二进制拒绝，
不应通过删队列日志降级。详细范围见[宿主供给设计](../../../docs/architecture/incus-host-provisioning.md) §7.12。

## 工作区中介启动准入（2026-09-21，生产仍关闭）

内部 `StartHostWorkspace` 将 host-only 读取器、原协调器和跨进程围栏连成一次启动操作。
共享宿主服务的 `StartIngressWorkspace` 绑定自身 owner context、已授权 actor 和同一队列的
观察调用器；它不是新的 CLI/Web 命令，不能通过调用方配置指定其他 invoker。

请求根、凭据父目录、HTTP 日志与 Traefik 输出必须是独立的已安装目录。相互包含、路径别名、
暴露整个工作区/Core 状态、共享写权限或私有文件硬链接都会拒绝启动。renderer 的脚本内容和
文件身份、输出目录身份在装配时固定，进入原围栏前复核，之后不静默采用替代脚本或目录。
Start 返回仅表示认领，首次对账完成以 Ready 为准；停机仍须等待原清理与库存核对。

这些是目录与生命周期约束，不是 UID 或实际挂载的安装器。消费者目录丢失会拒绝发布，但
旧 Traefik 的独立撤销仍可读取原凭据；原凭据或输出身份自身失效则保留恢复证据。完整本地
启动测试为空请求场景，API 数据/网络/探测是夹具。生产配置源、独立运行身份、挂载、health、
VM/TAP 和真实网络验收仍未交付。见[宿主供给设计](../../../docs/architecture/incus-host-provisioning.md) §7.14。

## 原生读回修复（2026-09-21，生产关闭）

nft 库存改用符号协议读回，拒绝可能混淆不同 EtherType 的数字值。回包规则使用宿主内部只读
GETRULE 提取原始接口索引，由 GETGEN 前后与完整 JSON、表/链/handle 绑定，不从设备名学习。
完整有序表达式只消除紧邻且等价的协议依赖；其他谓词、counter、verdict 和全部归属检查不变。
策略路由支持严格的 IPv4 地址与前缀长度分列格式，不能由空/未知长度扩大授权。

指定 Ubuntu 主机八项强制内核门禁及乱序重复回归通过，包含错误 EtherType、规则 generation
变化、接口删除/复用和真实回包来源反例。namespace 夹具恢复原线程，guest veth 对端独立。
测试不安装服务或验证真实 guest/Traefik/Docker 共存，不解除生产 gate。完整范围见
[宿主供给设计](../../../docs/architecture/incus-host-provisioning.md) §7.15。

## 独立 daemon 存储验证（2026-09-21，生产关闭）

宿主供给客户端修复同步创建 HTTP 201 被误判为失败的问题，仅 POST 可在完整成功 envelope
下接受 201，不放宽 GET、异步等待或资源读回。实际独立 Incus 6.0.5 已验证旧客户端失败、
修复后创建/读回/删除成功；Provider 重复拒绝 dir/缺失池，缺失 project inspect 保持只读，
错误 server pin 被拒绝，project/network/profile/trust 库存不变。

原生入口在私有 namespace/tmpfs 中运行发行版解包程序，不安装默认服务，不把 dir 当作
有效配额池，也不创建 guest 或访问现有 Docker。尚未验证 btrfs/zfs 实际磁盘限制、完整
容器/VM/one-job、7.3.0 或默认自动安装。见
[宿主供给设计](../../../docs/architecture/incus-host-provisioning.md) §7.16。

## 网桥依赖与真实容器生命周期（2026-09-22）

宿主声明式安装表显式包含 `dnsmasq-base`，避免 `--no-install-recommends` 跳过 Incus 的
网桥运行依赖。真实 Ubuntu 26.04 / Incus 6.0.5 首轮因此未能创建网桥，补齐依赖后实际
Provider 的双租约重复 ensure/inspect 和共享客户端容器生命周期通过。已有包不自动归
ANAS 所有，卸载仍保留安装前存在的 helper。

新增 `test-env/scripts/server-incus-lifecycle-e2e.py` 只允许具备精确 cloud-init 身份的可销毁
QEMU VM，要求无 Docker、初始 Incus 库存为空。使用真实 btrfs、受限证书和同名前缀的两份
租约，覆盖实际 stdin、写满、取消后的独立删除和幂等回收。测试镜像是有真实摘要的最小
原生夹具，不是正式 Runner 或发布目录条目，不证明 distrobuilder、ZFS、VM 档、one-job
或生产 ingress 已验收。流程及边界见[宿主供给设计](../../../docs/architecture/incus-host-provisioning.md) §7.18。

## 默认 Runner 配方的真实 copy 验证（2026-09-22）

冻结的 Runner 输入位于配方目录的 `sources/forgejo-runner`，默认 copy generator 已改为
使用该路径。旧裸文件名在真实 distrobuilder 3.2 中失败，修正后 squashfs 内字节与输入相符。
这改变配方摘要，不能覆盖旧 revision。真实构建的取消与同版本重试拒绝也已核验。

完整 Debian/Podman 烘焙尚未完成；局部 pack 是合成 rootfs 反例，不是正式 Runner 镜像。
新增 `server-incus-runner-image-e2e.py` 和 `TestNativeBakedForgejoRunnerImage`，分别要求真实
导入/完整租约、guest 启动、one-job 参数及 runner-agent 对 rootless Podman 的访问。完整
入口尚未执行，不据此完成 M12、one-job 或签名发布。详见宿主供给设计 §7.19。

## Runner 构建恢复与诊断（2026-09-22）

发布侧增加只保留固定阶段的有界诊断，不返回原始 distrobuilder 输出，失败 revision 仍不可
隐式重烘焙。默认 Runner 的 resolver 链接由镜像内的 tmpfiles 规则在启动时建立，不在构建
chroot 内替换；显式声明 systemd init 包。完整说明见
[镜像供给设计](../../../docs/architecture/incus-image-supply.md)。
