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
```

删除这两段声明的 Module 不参与 Resource 解析，也不会收到任何沙箱凭据。

## 生命周期

Runner 为每个 Resource 生成一对稳定的客户端证书与私钥，作为单条 Secret 保存，然后在 Consumer
启动前调用 Provider 的幂等 `ensure`。Provider 必须：

1. 校验 endpoint 可达，且 server certificate 与固定 fingerprint 一致，失败即 fail closed；
2. 确保 `sandbox` 指定的 project 存在且 `restricted=true`，并且只属于本租约：属于其他租约、或能被
   其他受限凭据操作的 project 必须拒绝，不得接管（sandbox 名在每个工作区都相同，不能证明归属）；
3. 把 `quota` 写到 project 自身的限制上，而不是依赖调用方自觉；
4. 在 default project 建立每租约独立的受管 bridge，在消费者 project 建立租约 profile，并把 profile **读回**校验：它必须恰好只有一块
   根磁盘（在受管存储池上、无 host source）和一块接到该受管 network 的 NIC；
5. 把 Runner 传入的客户端证书登记为**只绑该 project** 的受限证书，不使用全局管理凭据；
6. 重复调用收敛到同一结果，不产生第二个 project、第二个 network 或第二条 trust 条目。

## Provider 拥有 profile 与 network

实例的数值上限来自消费者，**其余一切来自 profile**：根磁盘落在哪个存储池、插在哪张网卡上。
profile 名字由 Contract 固定为 `anas-lease`，消费者只能引用、不能编写——能命名一个 profile 的
调用方，也就能指向别人写的 profile，而那正是「Provider 拥有它」要防的事。

每份租约有自己的受管 bridge，均由 Provider 在 default project 拥有。消费者 project 关闭
`features.networks`，通过 `restricted.networks.access` 精确限制为该 bridge，并保持 managed NIC
限制；实例、profile、配额和证书仍隔离于各自 project。不同 bridge 不等于实际流量自动隔离。网络名不是
sandbox 名：Linux bridge 接口名上限 15 字符，而 `anas-forgejo-runners` 已经 20，所以它由 sandbox
名哈希派生——短、跨 apply 稳定、租约之间不碰撞。

`ensure` 每次都**整体替换** profile 的设备而不是合并。一块被人手工挂上去的 host 路径，正是
最不该在一次 ensure 之后还留在那里的东西。

`inspect` 是只读的，必须能分别报告 `exists`、`ready`、`restricted` 与 `quota_enforced`——一个存在
但未受限或未设配额的 project 是没有围栏的围栏，必须能被单独看见。`revoke` 是 1.x 可选 operation。

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
只是这台 daemon 的客户端控制面。`deletion_policy: delete` 仅表达显式删除意图；在 Core 提供
破坏性 Resource delete 入口前仍按 retain 处理。

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
把它声明成可轮换凭据。专属密钥轮换命令与生产发布尚未实现；授权冻结及实验中介见下节。
文件备份恢复回归通过不代表真实 Incus 宿主或 HTTP 入站验收通过。

## HTTP 授权冻结与实验中介（尚未开放运行时）

2026-09-12 已接入声明解析和 deployment 准备流程，尚未运行新代码的测试。省略 `spec.ingress`
即无发布授权；声明存在时可准备冻结产物，但启动/激活会明确拒绝该 compute 消费者。内置消费者
暂未新增 ingress 配置或请求目录挂载。不得把接受 schema 当成可用的生产发布功能。

```yaml
# 加在 compute 的 spec 内；当前仅支持准备授权，不能启动带此声明的消费者。
ingress:
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
不从当前配置重算。Resource state 的模型同样只保存授权与密钥引用；未通过启动拦截就不会产生
带 ingress 的 ready state。

apply 检查租约间重叠命名空间、Module 声明域名及现有环境中的字面 `Host(…)` 路由；named/random
保守预留整个 `prefix-*` 空间。尚不能解释的 file-provider 匹配规则会阻止准备，而不是忽略。
生产运行时还需枚举实际路由所有者并持久对账；目前没有这项保证。

`internal/computeingress` 提供严格 JSON 请求解析、Linux amd64/arm64 目录句柄相对读取和进程内域名预占。
请求 schema 见 `schemas/http-publication-request.yml`，只接受 `action`（publish/revoke）、
`instance_id`、`workload_id`、`guest_port` 与可选 `label`；不新增 Provider operation。
目录由受信调用方绑定到活动 deployment 的授权。读取仅接受单层 JSON 文件名，以
`openat(O_PATH|O_NOFOLLOW)` 固定 inode 并拒绝符号链接、设备/FIFO及硬链接，
再从受信宿主 `/proc/self/fd` 以非阻塞只读模式打开同一普通文件，限 4 KiB；拒绝重复键、
大小写字段别名、null、未知字段和尾随内容。读取前后的 inode/内容元数据变化会拒绝该请求。

中介规划核对独立观测的 Running 实例、UUID、project、隔离档和 IP/MAC 分配。名称碰撞及同一
实例端口改名失败；完全相同的请求幂等。每次预占带会话/序号，旧清理回调不能释放新预占。停止
实例的撤销依赖已记录身份，不要求 guest 再次 Running；清理确认前保留预占。

实验命令已接入 `--mediation`，仅针对 `.example.test`、单条 `INCUS_LAB` 路由生成计划及既有
Traefik 环境字段，不执行 renderer 或网络变更。离线实验输入的活动部署与观测由管理员断言；workspace 模式的 Core 状态读取见下节。
只读受限 daemon 身份、自动挂载/登记接入、全局路由所有权、宿主动作/IP 保留、
探测执行和事件/周期对账仍未形成生产闭环；TCP/UDP 与 LAN 不实施。

2026-09-18 已新增消费者文件请求 API（源码及回归用例未编译/执行）：
`Client.OpenHTTPPublisher(HTTPPublicationConfig)` 显式打开本租约已安装的私有请求目录；
`HTTPPublisher.PublishPort` 校验精确 Running 实例和 `user.anas.workload`、端口及 label，
只提交既有小型请求 schema。配置是租约范围、公开策略与基础域名的最小投影，不含完整冻结授权、
middleware、entrypoint 或全局 Store 引用；random 模式命名密钥独立交付，不进入请求或默认
JSON/格式化输出。公开策略的 `Policy.Host` 只预测名称；中介的 `Authorization.Host` 仍验证完整授权。

返回的 `HTTPPublication` 仅代表请求已提交；`RequestedURL()` 不代表路由、TLS、认证或后端已就绪。
`UnpublishPort(ctx, publication)` 使用原文件回执，实例停止或删除后仍可撤销意图；不把文件移除当作
网络撤销完成。关闭 publisher 仅释放本地句柄，不自动删除持久请求。底层 `RequestWriter` 使用
私有目录、协作锁、原子 rename 和文件/目录 fsync；持有 inode 的旧回执不能撤销合作 writer
后建的文件。符号链接、硬链接、FIFO、目录换位、冲突和未支持平台均拒绝。
自动配置投影与挂载、应用生命周期/崩溃恢复、受信中介装配和真实发布状态确认仍待接入；
上述 API 不移除生产 ingress 启动拦截。

## 活动授权读取与请求目录登记（实验适配器）

`deployment.Reader.HTTPAuthorizations` 只读 Core 的 `.anas/state/lock`、active/state/deployment
清单，在既有共享运行锁下核对 `runtime_status: running`、active 状态、激活时间、资源身份、
compute/ForwardAuth 绑定和镜像冻结。它不创建工作区、不执行恢复或 Hook，也不读取 Secret Store。
活动状态不一致、未激活、停用、缺失或过大的元数据均拒绝。授权代次由实际 workspace 路径摘要、
deployment ID、激活时间和清单字节摘要组成；清单改变、切换部署或恢复到不同路径使旧登记失效。
密钥和派生 URL 的备份稳定性不依赖这个目录代次。

`internal/computeingressruntime` 为当前授权创建新 0700 根目录、每租约独立 0700 请求目录和
0400 `registry.json`。登记只含代次、清单摘要、租约、相对目录名及设备/inode 身份，不含密钥，也不把授权 JSON
交给消费者。目录名由代次/租约摘要派生，不接受消费者给出的路径。旧目标目录不覆盖；失败时只
尝试移除本次创建的文件和空目录，不递归删除后续写入。登记前后会重新核对活动状态。

读取请求时将登记的规范字节与当前 Core 授权重新计算结果比较，拒绝过期、篡改或不匹配的登记，
核对创建时的设备/inode，拒绝同名替换或目录换位，再固定该租约目录句柄。workspace 与登记根必须在消费者写权限之外，后续仅挂载单租约请求目录。
同一活动授权的登记副本仍必须通过 Core 核对；登记文件本身不授予权限，不提供全局发布锁。

实验命令新增互斥的 `--register-requests <workspace> --out <新绝对路径>`，只创建上述登记与
空目录。workspace 版 `--mediation` 只给 workspace、request_registry、consumer、resource 和
request_file，不接受手填授权、活动 ID、目录或密钥文件覆盖。random 命名调用 Core 侧读取适配器，
复用现有 Secret Store 解析和租约 metadata 检查，仅返回这一租约的独立命名密钥；缺失不重铸，
密钥不进入登记或输出。该调用仍在具有工作区读取权限的管理员实验进程内，不给消费者或生产
中介挂载整个 Store；生产窄范围密钥传递接口仍待实现。

采集结束再读取 Core 状态：授权变化或不可读时，无 previous 则失败，有同租约 previous 则只
生成撤销计划。拟发布记录保存 `authorization_epoch` 便于核对；它不是授权续期或已执行回执。
共享锁保护本次元数据读取，释放锁后的状态仍可能改变；真实执行前必须再次校验。

这些入口仍只支持 `.example.test` 实验授权。正常 apply 的 ingress 启动拦截保留，所以不能用
普通 active 部署产生生产 ingress 登记；新分支的正例留给独立元数据夹具，不修改实际部署状态
绕过拦截。全局 runtime_status 也不证明每个消费者容器仍运行，部分停止/crash、事件丢失和中介
重启仍需后续实时观测与对账。目录/挂载自动化、受限 daemon 观测、宿主动作、探测和 renderer
执行尚未接入；本轮没有执行登记命令、测试或服务器操作。
