> 本页由 Contract 技术文档生成，请勿直接编辑。

# compute Contract 技术说明

## 交付的是围栏，不是实例

`compute` 1.x 在 `anas apply` 时交付一份**隔离沙箱租约**：一个受限 project、一组由 Provider 侧
强制的配额、一份固定镜像 allowlist，以及一张只绑定该 project 的受限客户端证书。实例的
`create`/`start`/`exec`/`delete` **不是** Contract operation，而是消费者在租约边界内自行驱动的
运行时行为。

这条边界不是省事，是模型决定的。Contract operation 的唯一 runtime 是 `compose_run`，由 Runner
在 apply 时以 `docker compose run --rm` 执行一次；ANAS 在运行时没有「模块容器 → Core」的调用
通道。而一次性实例是 per-job 热路径，发起方是常驻消费者容器。把 per-job 的实例生命周期塞进
per-apply 的冷路径，只会得到每个操作一次容器冷启动、且没有 stdin 流可用于注入 Secret。

因此职责这样切分：

| 层 | 机制 | 时机 | 发起方 |
| --- | --- | --- | --- |
| 围栏供给 | 本 Contract 的 `ensure` | apply，一次 | Runner |
| 围栏内使用 | 消费者直连 Provider daemon | 每个 job | 消费者容器 |
| 围栏运维 | Module Command | 按需 | 管理员 |

一个受限 project 之于 `compute`，等于一个 database + role 之于 `relational_database`：Provider 在
apply 时把它建好并交出凭据，之后不再位于数据路径上。

## 声明模型

本机宿主供给还会按消费者的私有 Resource 前缀投影 `CONTROL_NETWORK_NAME` 与
`CONTROL_NETWORK_EXTERNAL`，让 Provider 与 compute 消费者连接同一个受管控制桥。网络名来自
Provider 已验证的宿主连接，不由消费者选择；无本机控制桥时只投影 `false`，不制造空的必填
网络名。该内部环境投影不改变 Contract 请求/结果 schema，也不把 daemon 管理私钥交给消费者。
业务网络保留默认出口，external 网络的生命周期仍归宿主供给而非消费者 Compose。

`compute` 1.x 提供 `incus_vm` 与 `incus_container` 两个 interface。二者的 schema 与操作语义完全
一致，区别只有隔离强度：系统容器与宿主共享内核，VM 提供独立 guest kernel。选哪一档是部署
决策，由消费者的 `binding` 参数选择，Provider 不自动降级。

```yaml
dependencies:
  contracts:
    - name: compute
      version: ">=1.0.0 <2.0.0"
      selected_by: actions_isolation
      interfaces: [incus_container, incus_vm]
      default: incus_container
resources:
  requires:
    - id: runners
      contract: compute
      binding: actions_isolation
      spec:
        sandbox: anas-forgejo-runners
        instance_prefix: anas-fj-
        quota: {max_instances: 8, cpu: 4, memory_mib: 8192, disk_gib: 40}
        image_allowlist: [{fingerprint: "<64位小写SHA-256>"}]
        credential: {policy: generated}
        deletion_policy: retain
        # 可省略；默认只能出公网、不接受主动连入。见下文「租约网络」。
        network: {egress: internet, module_access: true}
```

删除这两段声明的 Module 不参与 Resource 解析，也不会收到任何沙箱凭据。

## 生命周期

Runner 为每个 Resource 生成一对稳定的客户端证书与私钥，作为单条 Secret 保存，然后在 Consumer
启动前调用 Provider 的幂等 `ensure`。Provider 必须：

1. 校验 endpoint 可达，且 server certificate 与固定 fingerprint 一致，失败即 fail closed；
2. 确保 `sandbox` 指定的 project 存在且 `restricted=true`，并且只属于本租约：属于其他租约、或能被
   其他受限凭据操作的 project 必须拒绝，不得接管（sandbox 名在每个工作区都相同，不能证明归属）；
3. 把 `quota` 写到 project 自身的限制上，而不是依赖调用方自觉；
4. 在 default project 建立每租约独立的受管 bridge 和它的 network ACL（按冻结的出入站档位与发布写规则），在消费者
   project 建立租约 profile，并把 profile **读回**校验：它必须恰好只有一块根磁盘（在受管存储池上、无 host source）和
   一块接到该受管 network 的 NIC。`ensure` 的结果交回 bridge 名、IPv4 网段与网关、启用时的 IPv6 网段与网关，以及
   各槽位的固定地址，Core 记入 resource state 的 `lease_network`（`INCUS-R-159`）；
5. 把 Runner 传入的客户端证书登记为**只绑该 project** 的受限证书，不使用全局管理凭据；
6. 重复调用收敛到同一结果，不产生第二个 project、第二个 network 或第二条 trust 条目。

## Provider 拥有 profile 与 network

实例的数值上限来自消费者，**其余一切来自 profile**：根磁盘落在哪个存储池、插在哪张网卡上。
profile 名字由 Contract 固定为 `anas-lease`，消费者只能引用、不能编写——能命名一个 profile 的
调用方，也就能指向别人写的 profile，而那正是「Provider 拥有它」要防的事。

每份租约有自己的受管 bridge，均由 Provider 在 default project 拥有。消费者 project 关闭
`features.networks`，通过 `restricted.networks.access` 精确限制为该 bridge，并保持 managed NIC
限制；实例、profile、配额和证书仍隔离于各自 project。不同 bridge 不等于实际流量自动隔离，隔离由下文
「租约网络」的 ACL 承担。网络名不是 sandbox 名：Linux bridge 接口名上限 15 字符，而 `anas-forgejo-runners`
已经 20，所以它是 `lease` 加 sandbox 名 SHA-256 的前 10 位十六进制——短、跨 apply 稳定、租约之间不碰撞。
前缀 `lease` 不是 `anas`：宿主的静态转发规则只匹配这个前缀，而 `anas-helper` 可以操作所有 `anas*` 接口
（`INCUS-R-127`）。2026-10 之前的租约用 `anas` 加同一摘要；Provider 在租约迁到新网桥后删除不再使用的旧网桥。

guest 只能以租约网络自己的地址出网。出口 NAT 只改写租约子网内的源地址，guest 伪造子网外源地址
的报文不会被改写，若被转发就以伪造身份到达外部。Provider 因此必须让这类报文在宿主上被丢弃，
并在 `ready` 中检查该约束仍然成立；Incus Provider 用自有的网桥 network ACL 实现（见 Incus Module
技术文档）。

`ensure` 每次都**整体替换** profile 的设备而不是合并。一块被人手工挂上去的 host 路径，正是
最不该在一次 ensure 之后还留在那里的东西。

`inspect` 是只读的，必须能分别报告 `exists`、`ready`、`restricted` 与 `quota_enforced`——一个存在
但未受限或未设配额的 project 是没有围栏的围栏，必须能被单独看见。`revoke` 是 1.x 可选 operation。

### 租约的结束

消费者被移除、或申请租约的能力被关闭（例如 `enabled_by` 的开关关掉）时，目标部署不再声明该
Resource。数据库或桶的「保留」保留的是数据；compute 租约若同样只改状态，保留下来的是一条仍受信的
访问授权。因此 Core 在新部署启动后，用**上一个部署冻结的 Provider 产物**执行 `revoke`：撤销该租约
的受限证书，清空租约 ACL 的规则（两个方向的默认动作都是丢弃，从此进出都不通），并停止租约内运行的实例——ACL
放行已建立连接的回包，只有停止实例才能结束这些连接（`INCUS-R-124`）。project、实例磁盘与网络保留（`INCUS-R-014`）。同一次 apply 连 Provider 一起移除时，
旧产物仍可执行这次撤销。Provider 没有声明 `revoke` 时记录 `unsupported` 并告警，证书保持受信。

撤销失败不会被记作完成：默认中止本次激活并恢复上一个部署；只有显式 `--allow-risky` 才接受
未确认的撤销（例如 daemon 已永久不存在），此时 resource state 记 `revocation: unconfirmed` 并告警。
Resource state 的 `status` 仍为 `retained`，`revocation` 取 `confirmed`、`unconfirmed` 或
`unsupported`。重新声明同一 Resource 时，稳定证书由 `ensure` 重新登记，归属标记证明它仍是同一份租约。

`deletion_policy` 只接受 `retain`：没有 Provider 实现删除租约 project，接受 `delete` 等于承诺一次
不会发生的清理。

### 结果 schema 描述的是 Core 记录的租约

与其他资源 Contract 相同，operation 的 `result_schema` 描述 Core 在操作成功后记录并投影的结果，
不是 Provider stdout 的格式：Runner 只以退出码判断 Provider operation 成功与否。`ensure` 的
`sandbox-result.yml` 对应 resource state 的租约事实与上文的私有投影；`endpoint` 与 pinned server
证书来自 Provider Module 自身配置导出的 `<PROVIDER>_ENDPOINT`、`<PROVIDER>_SERVER_CERT_B64`，
可选的 `<PROVIDER>_CONTROL_NETWORK_NAME` 决定消费者连接的控制桥（`<PROVIDER>` 是 Provider Module 名
的大写形式）。Provider 缺少其中必需的键时，Core 拒绝发布租约。Provider 自行输出到 stdout 的
`inspect`/`revoke` 结果只供人工诊断，不进入 Core 状态。

就绪租约通过 Consumer 私有命名空间发布：

```text
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__INTERFACE
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__ENDPOINT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__SANDBOX
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__INSTANCE_PREFIX
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__PROFILE
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__SERVER_CERT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__SERVER_CERT_FINGERPRINT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CLIENT_CERT
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CLIENT_KEY
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__LEASE_SECRET
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__IMAGE_ALLOWLIST
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__MAX_INSTANCES
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__CPU
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__MEMORY_MIB
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__DISK_GIB
# 只在声明了 publish.http 时：
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__HTTP_REQUEST_DIR
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__HTTP_POLICY
ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__HTTP_BASE_DOMAIN
```

`ENDPOINT`、`SERVER_CERT`、`CLIENT_CERT` 和 `CLIENT_KEY` 均标为敏感值，只属于目标 Consumer。
凭据在部署清单与 Resource state 中只保存 Secret Store 引用，不保存明文；证书可公开验证不代表
可以将该部署的连接配置回显到日志或公开清单。

## 多消费者隔离

一份租约恰好拥有一个 project 与一张证书；两个消费者不共用 project，也不共用证书。**跨消费者
隔离完全由 project 承担**：受限证书越不出自己的 project，两份租约即使落在同一 Provider 上也互相
看不见对方的实例。

实例名前缀解决的是另一个问题，不要与上面混为一谈：它区分的是**同一个 project 内部**哪些实例由
ANAS 托管。运维可能在这个 project 里手工建过实例，janitor 必须能认出哪些不该由它回收。前缀是
消费者侧的过滤，不是安全边界。

真正重要的是：跨 project 这道边界由 **Provider daemon 自己**执行，不是由消费者代码自觉遵守。
受限证书的作用域写在 daemon 的信任库里，配额写在 project 上。消费者容器被完全攻破，越界请求
依然会被 daemon 拒绝。

## 两种 fingerprint

Incus 把「SHA-256 十六进制摘要」统称 fingerprint，本 Contract 里它出现在两个**互不相关**的位置，
读文档时不要混淆：

| 字段 | 是什么的 SHA-256 | 作用 |
| --- | --- | --- |
| `image_allowlist[]` | **镜像内容**（`incus image list` 的 FINGERPRINT 列） | 钉死这份租约能启动哪些镜像 |
| `server_certificate_fingerprint` | **daemon 证书的 DER** | 让消费者钉死它连的是同一台 daemon |

下文提到 fingerprint 时一律写明是哪一种。

## 安全边界

镜像 fingerprint 是镜像**内容**的标识，而 alias（例如 `images:debian/13`）只是一个指针，远端明天
可以把它指向新内容。因此固定镜像 fingerprint 的 allowlist 意味着一份租约能启动的东西在 apply 时
就已封闭：tag、alias 与远程 URL 一律不接受，否则一份审过的租约会随远端更新而漂移。Provider 拥有 profile、network 与 storage pool，不接受调用方传入 device、raw config、
挂载或宿主 socket。

Contract 不负责安装、配置或托管 Provider daemon 本身，也不要求 ANAS 宿主具备虚拟化能力——它
只是这台 daemon 的客户端控制面。`deletion_policy` 只接受 `retain`，移除声明撤销证书、不删除 project，
见「租约的结束」。

一次性 Secret（例如 runner token）如何进入 guest 不属于本 Contract：那发生在租约交付之后，由
消费者经共享 `computeclient.ExecStdin` 直连 Incus 注入，不经过 Provider operation。Secret 不得出现在命令参数、环境变量、cloud-init、
镜像、磁盘状态或日志中。

### 共享客户端的凭据准备与子进程边界

`New` 保持原有调用接口；`NewWithContext` 让凭据准备、文件锁等待和初始 CLI 连接共同受调用方
取消信号与最多 30 秒初始化预算约束。初始化复制镜像 allowlist，调用方修改原切片不能改变已
构造客户端的镜像许可。直接构造的租约与环境输入使用相同的 endpoint、project、profile、配额、
隔离档及摘要校验；guest 入口必须是不重复的规范绝对路径，非法输入在文件写入前拒绝。
此方法只准备连接，不启动或清理 guest。

凭据准备先核验有界 base64、单份 X.509 证书、服务端证书摘要及匹配的客户端证书/私钥，全部
通过后才创建目录。Linux/macOS 上，配置根和 `servercerts` 必须属于执行用户且为 0700；三个
凭据文件、冻结的 `config.yml` 和初始化锁必须是同一用户的 0600、单硬链接普通文件。使用目录句柄、不跟随最终符号
链接、独占创建、fsync 和读回；任一已有文件内容不同、权限不符、特殊文件或身份漂移均拒绝。
没有匹配字节就不会截断或覆盖，也不会自动 chmod 既有文件。不同身份应交付不同的私有目录。
初始化锁不删除，等待可取消；相同内容只读回而不重写，失败后的私有部分文件保留供显式处理，
不在失败返回时删除其他进程可能使用的路径。其他平台没有不受保护的写入回退。

`config.yml` 使用标准 JSON 编码表达 CLI 可读取的 YAML 配置，固定 `protocol: incus`、remote、endpoint、TLS 类型和
project，并与证书一起校验、提交和读回。旧实验的 `protocol: lxd` 配置不被覆盖或静默迁移，
需使用新私有配置目录。不再执行 `remote add`；重复初始化复用相同字节，
然后只读列举受限 project 中的实例来检查连接。不同 project 或其他既有配置不能静默覆盖。
初始化锁先独占创建，已存在时以不带创建标志的方式打开；后者消失时拒绝，不另建锁 inode。
错误只附带固定阶段和系统错误类别，不回显路径或凭据。

这只是初始化的序列化与文件保护，不是整个客户端运行期的凭据锁或轮换协调。只读列举成功
不替代 Provider 的完整围栏/profile/配额就绪检查，也不证明 guest 生命周期通过验收。

Incus 子进程只收到固定系统 PATH/locale、本租约的 HOME、INCUS_CONF 和 INCUS_PROJECT，不继承
其他租约 Secret、默认 Incus socket、代理或动态加载器环境。程序仍从受信消费者自身 PATH 解析。
stdout 上限为 4 MiB，stderr 为 64 KiB，超限主动取消子进程并返回固定错误，不返回部分输出；
错误不回显 stderr/stdin。`WaitDelay` 限制退出后管道等待，保留调用方取消错误身份；未提供 stdin
时关闭输入。这些限制不能代替对取消后真实 guest 的独立回收与生命周期验收。
实例列表拒绝 `null` 和重复托管身份；托管过滤与创建使用相同的实例名规则。删除 CLI 成功后仍须重新读取并确认实例不存在。
单个实例的读取列出整个租约 project，再在客户端精确匹配名称：Incus 7.x 的 CLI 把 `<remote>:<name>` 解析为
只有 remote、没有过滤条件，返回空列表（6.0 则是名称前缀过滤）。旧实现因此在 7.x 上把任何实例读成
“不存在”，删除确认会被误判为成功；2026-09-26 的 Incus 7.0.1 原生生命周期发现并修复。

## 结构化声明与 deployment 镜像冻结

镜像声明只接受互斥的 `{fingerprint: <64hex>}` 或
`{catalog: anas, name: <name>, revision: <revision>}` 对象。拒绝全部字符串旧格式、混用/未知字段、
alias 与 URL；运行时 ABI 仍只传裸摘要。name/revision 的字符范围以 schema 为准。

Core 在 deployment 准备阶段、Hook 渲染与 Provider ensure 之前解析。Provider 的
`image_architecture` 显式选择 `amd64` 或 `arm64`，不从 CLI 宿主推断。目录仅来自已通过 bundle
信任校验的 Provider 源目录 `images/catalog.json`，精确匹配架构和 interface。随仓库分发的目录
当前为空：命名引用需要包含真实目录记录的发布包，不把示例摘要当作可用镜像。

`spec_from` 的字符串简写继续用于普通标量参数。结构化来源显式声明
`{parameter: actions_runner_image, projection: singleton}`（单对象）或
`{parameter: agent_runtime_images, projection: values}`（映射）；`values` 按键排序并保留
runtime→摘要关联，`value` 原样传递解码后的 JSON。不按 Module 名分支。配置对象以规范 JSON
跨越现有字符串 ABI，声明为 `format: json_object`。

`deployment.yml` 的 Resource 冻结 `compute_images`：目录、逐条引用、目标、镜像摘要、目录/配方
摘要及可选绑定。Core state 独立保留只增不改的 `compute-image-history.yml`，deployment 留存清理
不会抹去已经发布的版本键。后续 apply 拒绝旧键换内容；回滚只校验冻结快照，不查询新目录。
没有快照的旧 deployment 在启动/重放前明确失败；仍可读取元数据并由使用对象配置的新 apply 替换，
不提供字符串执行兼容分支。
Resource state 同时记录快照，凭据仍只保存 Secret Store 引用。

消费者收到 `IMAGE_ALLOWLIST`（CSV 摘要）；映射额外收到 `IMAGE_BINDINGS`（JSON runtime→摘要）。
Forgejo 使用单条冻结 allowlist，AI Agent 使用冻结映射。Provider ensure 在登记新消费者证书前，
读回本租约 project 的镜像 metadata，核对精确 fingerprint、架构和镜像类型；缺失即失败。
相同产物导入、distrobuilder 烘焙、发布分发和 prune 仍未实现。重新 apply 失败不会自动撤销已经登记
的消费者信任。单元测试不能证明真实 daemon 围栏或宿主验收通过。

## 独立租约命名密钥

Core 在新 apply 为每个 compute Resource 生成独立的 32 字节随机 `lease_secret`，以规范 base64
保存为 `.anas/secrets.yml` 的单独条目 `ANAS_COMPUTE_RESOURCE__<MODULE>__<RESOURCE_ID>__LEASE_SECRET`。
客户端证书 bundle 保持独立；补齐旧租约时不重签证书，重复 apply 和证书变更不会重铸命名密钥。
已存在但为空、格式损坏或归属不符的条目会报错；历史 deployment 或 resource state 已引用而条目
丢失时也报错，必须恢复原条目，不能以新密钥悄悄改变派生 URL。

同名运行时投影标为敏感，只交给所属 Consumer；Forgejo 的控制/执行服务和 AI Agent orchestrator
通过 Compose 接收，Provider ensure 与 guest 均不接收。`config list`、只读验证 Hook 和跨消费者
环境过滤复用 Secret Store 的敏感来源规则。Deployment 与 resource state 的 `lease_secret` 字段
仅保存引用。旧冻结部署没有引用时，读取/重放不会生成密钥；需要新 apply 才补齐，镜像旧字符串的
启动拒绝规则保持不变。备份的 Secret Store 与 deployment 一起恢复，密钥及 HMAC 派生结果保持一致。

它是域名派生的命名密钥，不认证请求、不授予发布权限；不进入凭据轮换或 `--all`，模块也不能
把它声明成可轮换凭据。专属密钥轮换命令尚未实现；HTTP 发布见下文。
文件备份恢复回归通过不代表真实 Incus 宿主或 HTTP 入站验收通过。

## 租约网络

租约声明里的 `network` 决定实例能连到哪里、谁能连进来，随部署冻结进 `deployment.yml` 的
`compute_network`，运行时消费者改不了（需求见 Incus 要求 §7sexies、§7septies）。

| 字段 | 取值 | 默认 | 含义 |
| --- | --- | --- | --- |
| `egress` | `internet`、`internet_lan`、`internet_lan_host`、`modules_only` | `internet` | 实例主动连出的范围；任何一档都到不了其他租约 |
| `module_access` | 布尔 | `false` | 让 `internet`、`internet_lan` 也能经 Traefik 访问 ANAS Module |
| `intra_lease` | 布尔 | `false` | 同一租约的实例之间能否互访 |
| `ingress` | `none`、`published` | `none` | 谁能主动连入；只有 `published` 能声明发布 |
| `slots` | 槽位名 → `{instance}` | 无 | 每个槽位写死一个实例名，Provider 为它保留固定地址 |

各出站档位：`internet` 只到公网地址；`internet_lan` 加上局域网（宿主默认路由所在网卡的直连网段加操作者配置的
附加网段，每次 apply 重新计算）；`internet_lan_host` 再加上宿主本机地址和 Docker 已发布的端口；`modules_only` 只能
经 Traefik 访问 ANAS Module。Provider 把档位、开关与发布写进租约网桥的 network ACL，两个方向的默认动作都是丢弃。
「全部租约网段」是 Provider 维护的全局 address set `anas-leases`，出入站都用它排除其他租约；Traefik 容器的当前地址
在全局 address set `anas-traefik` 里，只由 hostd 写入。两者都要求 Incus 7.0 以上（`network_address_set` API 扩展）。

## HTTP 发布

`publish.http` 让 Traefik 终止 HTTPS，以 HTTP 转给租约实例的端口。它要求 `network.ingress: published`，
并要求消费者在 manifest 的 `resources.requires` 里写 `http_request_owner: "<uid>:<gid>"`：写请求文件的那个
进程的身份，Core 用它创建请求目录。

```yaml
publish:
  http:
    allowed_ports: [7000]
    # 默认 none。域名不可预测不是访问控制：SNI、Referer 与日志可泄露 URL；
    # 不得用于发布敏感数据或带写入能力的服务。
    auth: none
    domain: {mode: random, prefix: ci}
```

端口为 1–65535 的去重整数，最多 64 个，冻结时排序。未知字段、null、字符串端口、重复端口及
非 HTTP 协议声明均拒绝。`fixed` 使用 `<prefix>.<base_domain>`；`named` 使用
`<prefix>-<label>.<base_domain>`；`random` 使用独立租约密钥对原始 `workload_id` 做
HMAC-SHA256，取前 32 个十六进制字符（128 位）。最终 label 不超过 63 字符，prefix 上限分别为
63/61/30；named 的 label 必须是小写 DNS label，其他模式不得传 label。任务标识为最多 256 字符
的 ASCII `[A-Za-z0-9][A-Za-z0-9._:-]*`，不做大小写或空白归一化。

Core 在 calculate 后、render 前冻结 `compute_ingress`：部署与租约身份、project/实例前缀、
端口、认证、域名模式和 `BASE_DOMAIN`、命名密钥引用，以及需要时的 ForwardAuth provider/middleware。
`forward_auth` 要求消费者声明并解析到 `forward_auth/http` capability，且 middleware 与 provider
输出属于该绑定的 Provider；运行时请求不能覆盖认证。加载冻结部署核对 spec、身份、认证绑定和密钥引用，
不从当前配置重算。apply 检查租约间重叠命名空间、Module 声明域名及现有环境中的字面 `Host(…)` 路由；
named/random 保守预留整个 `prefix-*` 空间。

**请求。** 消费者把宿主目录 `HTTP_REQUEST_DIR` 挂进自己的容器，每个要发布的实例端口写一个请求文件
（`schemas/http-publication-request.yml`）：

```json
{"instance": "anas-fj-job1", "address": "10.101.0.17", "port": 7000, "label": "…"}
```

`address` 是实例在租约网桥上的 IPv4；`label` 在 named 模式是消费者选的名字，在 random 模式是共享客户端用命名
密钥算出的 32 位十六进制标签，fixed 模式省略。文件存在即请求，删除即撤销。目录归 `http_request_owner`、
权限 0700，跨 apply 不变。

**中介。** anasd 内的 HTTP 发布中介每 3 秒对每个登记工作区做一次全量重算：读取活动部署的冻结授权、各租约
记录的网段与请求文件，逐条校验——实例名在租约前缀内、端口在 `allowed_ports` 内、地址在租约网段内且不是网段
地址、网关或广播地址、域名在租约命名空间内——然后把 Traefik 动态目录下专属子目录 `compute-http/` 改写成恰好
这些路由，再改写顶层的 `compute-http.reload` 让 Traefik 重读（Traefik 只监视顶层目录）。不合格的请求跳过并
记日志，不影响其他请求；同一域名已有路由时，后来的请求不能接管它（`INCUS-R-149`）。中介不持有凭据、不调用宿主
动作，也不经过 Core 的单个作业；中介停机期间已发布的路由保持可用，重启后的第一次重算删掉没有有效请求的路由、
补齐缺失的（`INCUS-R-145`）。部署激活时，Core 删除授权被移除或改变的租约的路由文件；授权按内容摘要比较，
不含部署 ID，未变的授权保留路由（`INCUS-R-147`）。

请求文件的读取仅接受单层 JSON 文件名，以 `openat(O_PATH|O_NOFOLLOW)` 固定 inode 并拒绝符号链接、设备/FIFO 及
硬链接，再从受信宿主 `/proc/self/fd` 以非阻塞只读模式打开同一普通文件，限 4 KiB；拒绝重复键、大小写字段别名、
null、未知字段和尾随内容。每个目录最多 256 项。

**共享客户端。** `computeclient.HTTPPublicationFromLookup` 从投影的 `HTTP_POLICY`、`HTTP_BASE_DOMAIN` 与（random
模式的）`LEASE_SECRET` 构造配置，消费者传入请求目录在自己容器里的挂载路径；`Client.OpenHTTPPublisher` 打开它，
`HTTPPublisher.PublishPort` 校验实例正在运行、端口已声明，再提交带实例地址的请求。返回的 `RequestedURL()` 只是
预测的名字，不代表路由已生效。`Client.Stop` 与 `Client.Delete` 撤销该实例的全部请求，`HTTPPublisher.PruneOrphans`
删除实例已不存在的请求（`INCUS-R-146`）。这些客户端检查不是授权边界，中介独立校验每条请求。

## 端口绑定

`publish.ports` 把宿主上的一个端口原样转到槽位实例（TCP 或 UDP，报文不改，guest 看到真实客户端地址）。

```yaml
network:
  ingress: published
  slots: {dev: {instance: anas-fj-dev}}
publish:
  ports:
    - {protocol: tcp, host_port: auto, slot: dev, guest_port: 22}
```

- **宿主端口**：显式端口，或 `auto`——Core 从操作者在 `incus.configure` 时批准的范围（默认 30000–32767）里随机选
  一个空闲端口，记入部署，此后的 apply 保持不变，直到这条绑定被删除。显式端口与 Traefik 入口、其他租约的绑定、
  宿主进程或 Docker 发布的端口冲突时 apply 失败，并记录运行问题（`anas issues` 可见）；`auto` 跳过被占用的端口。
- **槽位**：Provider 在 DHCP 动态范围外为每个槽位保留固定 IPv4，租约启用 IPv6 时另保留固定 IPv6，经 `ensure`
  交回；共享客户端创建该名称的实例时把地址加到 profile 的网卡上，其余网卡设置不变。名称随机的一次性实例不占槽位。
- **生效**：hostd 的边界内同步动作 `incus.ports.sync` 自己重读各工作区的冻结部署，逐条校验后以单个 nft 事务替换
  端口表；每个生效的绑定由一个 systemd 套接字单元占住宿主端口，之后在同一端口绑定的宿主进程或 Docker 发布直接失败。
  anasd 在启动时和每次部署激活后触发同步，并定期及在容器启动时检查已生效的绑定。没有 hostd 的宿主（`--no-service`
  安装、非 systemd、开发构建）上，声明了端口绑定的 apply 失败并说明原因（`INCUS-R-164`）。

**两种发布的差异（`INCUS-R-163`）**：HTTP 发布下 guest 看到的来源是租约网桥网关，真实客户端在 `X-Forwarded-For`；
认证可由 `forward_auth` 提供。端口绑定没有 ANAS 层的认证，guest 服务必须自己认证；实例未运行时连接会超时；guest
内部的端口被占用时 ANAS 看不到。

## 活动授权读取

`deployment.Reader.HTTPAuthorizations` 只读 Core 的 `.anas/state/lock`、active/state/deployment
清单，在既有共享运行锁下核对 `runtime_status: running`、active 状态、激活时间、资源身份、
compute/ForwardAuth 绑定和镜像冻结。它不创建工作区、不执行恢复或 Hook，也不读取 Secret Store。
活动状态不一致、未激活、停用、缺失或过大的元数据均拒绝。授权代次由实际 workspace 路径摘要、
deployment ID、激活时间和清单字节摘要组成；清单改变、切换部署或恢复到不同路径使旧登记失效。
密钥和派生 URL 的备份稳定性不依赖这个目录代次。

