# Incus compute provider 技术实现

## 安装软件时的国内源

宿主安装计划读取工作区受管配置的有效 `CHINESE_SPEEDUP`（包括顶层 `env:` 的覆盖），
并在确认前冻结为 `request.chinese_speedup`。开启时，发行版依赖使用
`https://mirrors.aliyun.com/debian`、`debian-security`、`ubuntu` 或 `ubuntu-ports`；
Incus 包仍只取固定的 Zabbly `lts-7.0`。发行版签名密钥、套件与架构不变，APT origin pin
同步换源。实际安装仅接受同一配方的两套编译配置，可在新安装计划中反切；未知配置漂移
仍拒绝覆盖。卸载接受任一完整编译策略，不因当前开关改变而重新下载或换源。已经安装齐全
时不为了换源重装软件。工作区配置改变后，既有确认继续使用原计划，新的计划采用新值。

guest 烘焙使用独立的 `CHINESE_BUILD_SPEEDUP`：发布脚本经 recipe CLI 的
`--chinese-build-speedup` 固定 Debian bootstrap URL，并在 `post-unpack`、APT 安装前
转换 `.list`/`.sources` 中的 Debian 主源和安全源，保留 suites、components 与签名配置。
手工 `runner-image/provision.sh` 使用同一脚本。开关进入配方字节及摘要；同 revision
修改源会被拒绝，运行期 `CHINESE_SPEEDUP` 不重烘焙已发布 guest。上述两条路径均不接受
任意 `APT_MIRROR_URL`。本轮未执行真实 Linux 下载、安装或 guest 烘焙验收。

## 已删除的实现（2026-09-30）

以下实现不再属于需求范围，已从代码中删除；设计过程与历次核对记录保留在 `dev-docs/reviews/` 与宿主供给设计的
历史章节中，删除清单见[入站与宿主通道简化记录](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-09-30-incus-old-code-removal-and-hostd-simplification.md)：

- 逐实例转发许可（出站）：`internal/incusingresshost/forwarding_*`、`internal/incusprovision/forwarding_*`、宿主动作
  `incus.forwarding.permission(.plan)` 与 `incus.forwarding.withdraw`、部署前的转发撤回，以及宿主 `state.json` 的
  `forwarding_scopes`。出站改为租约级分级（`INCUS-R-112`—`R-127`，M10a）。
- 逐发布许可的 HTTP 入站运行时：`internal/computeingressruntime`、`internal/incusingresshost` 的其余部分、
  `internal/incusprovision` 的 `ingress_observation*` 与 `observer_configuration*`、宿主动作
  `incus.ingress.observe_http` 与 `incus.ingress.observer(.plan)`、控制台与 CLI 的 `observer` 阶段、job 执行器的
  入站排空与工作区围栏、`cmd/incus-network-prototype`，以及宿主 `state.json` 的 `observer_scopes`。入站改为租约
  ACL、anasd 内的 HTTP 发布中介与端口绑定（`INCUS-R-130`—`R-164`，M11/M11b/M11c）。
- 保留的部分：Core 对 HTTP 授权的解析与冻结、`internal/computeingress` 的请求文件与授权类型、租约命名密钥。
  2026-10-03 起声明改为 `publish.http`，启动时对带 HTTP 发布消费者的拦截已删除，请求改为
  `{instance, address, port, label?}`，见下文「HTTP 发布与端口绑定的宿主侧」。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `7.3.0-r2` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `compute` | 提供的 Contract | `1.0.0` / `incus_vm` |
| `compute` | 提供的 Contract | `1.0.0` / `incus_container` |

两个 interface 共用 executor 与校验路径，按隔离档选择相应 project/profile 围栏，不允许调用方修改低层设备或特权设置。

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
`go.sum`、`internal/computeclient`、`internal/computeimage`、`internal/computeingress` 与
`internal/securefs`；不能指向 `modules/incus`、`provisioner`
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
或先准备完整受信源码。2026-09-23 已在独立 Ubuntu 26.04 / Docker 29.1.3 / Compose 2.40.3
VM 中完成三个计算镜像的六次 source/staging 无缓存实际构建。输入与业务二进制摘要一致，
固定 Incus CLI 的二进制和 `7.3` 版本输出一致；缺失覆盖路径反例及所有测试容器/镜像清理
均通过。该结果验证 build-only staging 路径，不代表完整 Core 部署或消费者实际作业。

三个计算镜像的构建层和运行层均使用仓库既有 `DOCKER_HUB_REGISTRY` 策略，允许带仓库
前缀的镜像源；显式 `GO_BUILDER_REGISTRY` 只覆盖 Go 构建层。Forgejo 的固定上游 Incus CLI
和 AI Agent 第三方模块使用 `GO_MODULE_PROXY`，缺省回退到 `GOPROXY_URL`，再回退官方源。
这些都是构建设置，不进入服务 runtime environment，不关闭 Go checksum database，
也不把 Provider/controller 的共享仓库代码变为网络解析。

`go run ./cmd/check-shared-build --json` 输出经过核对的静态输入报告，明确包含
`docker_executed: false`。对实际 staging 另传 `--staging-root`、`--source-root` 及上述
绝对 shared override；报告绑定文件字节、执行位和 build 声明，拒绝混版、未知 build 字段
和凭据输入，不读取运行时 `.env`。它不能代替真正构建。

仓库 `test-env/fixtures/incus-shared-build/README.md` 定义独立 QEMU VM 的六次真实构建
门禁，检查源码/staging 的实际 Compose 解析、缺失 override 反例及最终非 root 二进制。
其中 staging 是保留真实构建输入的布局夹具，不是 Core 完整部署，不证明 `anas build/apply`、
Provider/guest 生命周期或正式发布成功。

## 控制连接（2026-09-30 起 Incus 直接监听控制网关）

消费者与 Provider 经 Docker 控制网桥访问宿主上的 Incus。`incus.configure` 把 Incus 的
`core.https_address` 设为控制网桥网关的 `8443`，这是它唯一的 HTTPS 监听，并写入 drop-in 让
`incus.service` 排在 `docker.service` 之后（网关地址要等 Docker 建好网桥才存在，Incus 7.0 绑定失败只在
30 秒后重试一次）。宿主防火墙只放行控制网桥网段到这个端口，mTLS 与 pinned server certificate 不变。
原来的非 root 转发组件 `modules/incus/control-relay` 及其单元、账号与配置已删除。设计与实测见宿主供给
架构 §3.9。

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
设计、包来源与上游版本差异见 [宿主供给架构](../../../docs/architecture/incus-host-provisioning.md) §2.1、§2.2。

### 宿主动作的执行（2026-09-30 简化）

以下 Module 配置不包含宿主动作通道的私有参数。anasd 在共享 job store 中开始任务后，直接连接 root:root 0600 的
`/run/anas/hostd.sock`，写一条 `anas.action/v1` 请求并读事件流；hostd 只接受 root/root 对端，在
`/var/lib/anas-hostd/invocations` 为每次调用留下记录（同一调用 id 只能执行一次，终态先落盘后发帧）。事件流在终态前
中断时，anasd 用只读动作 `host.invocation.status` 查询这份记录，不再有私有 broker、PID 1 私有总线核验或 systemd
退出观察。服务配置 `host_actions` 默认关闭，不是 compute ready。Linux 原生入口为
`bash test-env/scripts/test-host-action-native.sh`，要求调用记录、激活与对端核验的用例实际执行；它不是 systemd 或
Incus 验收。当前边界见[宿主通道架构](../../../docs/architecture/host-action-channel.md) §14。

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
| `incus.lan_extra_subnets` | string | `pattern: ^[0-9A-Fa-f:./, ]*$` | `""` | `static` | `INCUS_LAN_EXTRA_SUBNETS` | 否 | 否 | 否 | 是 | `reconcile` | 附加局域网网段，逗号分隔的 CIDR；与 Core 在 apply 时算出的默认路由网卡直连网段合并，供 `internet_lan`、`internet_lan_host` 两档放行；Hook 拒绝默认路由、回环、链路本地与组播网段 |
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
超限、未知字段、重复 JSON 字段和旧 schema 均拒绝。bundle schema 为
`anas.incus-connection-bundle/v2`（2026-09-30 去掉 `relay_service`），自动接入要求 `architecture`
（`amd64`/`arm64`）和 `storage_pool`（`anas-btrfs`）字段；旧 bundle 不会被猜测补齐。

自动 bundle 必须固定 `endpoint=https://<control_gateway>:8443`、`control_network=anas-incus-control`，
并验证管理证书、私钥 pair 和
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
   合并而不是覆盖，是因为 project 里可能有运行中的实例和运维手工加的 `user.*` 键；合并前先
   校验归属（见下文「租约归属」），已有 `features.networks=true` 的 project 直接拒绝，要求显式迁移，不自动切换网络归属；
2. **读回**并断言全部受管 project 配置与申请值一致，包括 `restricted=true`、四项 limits 的精确总量、隔离档实例类型上限、完整 `restricted.*` 集合（见下文）、再次读取存储池准入条件，且 network feature 关闭、NIC 为 managed、
   `restricted.networks.access` 恰好等于本租约 bridge。任一不满足立刻返回错误，且**不继续**
   登记证书——这一步是整个契约唯一的信任来源，写入成功不算数，daemon 自己的副本才算；
3. 在 default project 建立受管 bridge 并读回校验，再在租约 project 建立 profile 并读回校验。这一步在证书之前：一个还没有根磁盘和
   网卡的租约，把证书发出去也没用；
4. 读取目标 project 的每个冻结镜像，校验 fingerprint、架构与类型；缺失时只允许从匹配的冻结 supply 导入原始字节并读回，无法供给或失配即失败，不查询 alias；
5. 登记消费者证书。若该 fingerprint 已在信任库中，校验它是 restricted 且 `projects` 恰好只有本
   sandbox；发现它无限制或绑着别的 project 就报错退出，不做任何修改。最后只读核对完整依赖链，通过后才返回 ready。

## 租约归属

sandbox 名写在消费者 manifest 里，同一消费者在每个工作区都用同一个名字，因此名字本身不能证明
归属。Provider 在 project 与受管 bridge 上写入归属标记：`user.anas.consumer`、`user.anas.sandbox`
和 `user.anas.lease_credential`（本租约受限客户端证书的 SHA-256 指纹）。Core 按工作区、消费者、
Resource 各生成一张证书，所以同一 daemon 上第二个工作区声明相同 sandbox 时指纹不同。

`ensure` 在写入前拒绝两种 project：标记属于别的租约（即使那份租约的证书已被 `revoke`，project
仍属于它），或者除本租约外还有其他受限 `client` 证书可以操作它。没有标记的 project（标记出现
之前由 Provider 建立、由旧 controller 建立或手工建立）只在第二个条件不成立时被采纳并写入标记。
无限制证书属于 daemon 管理员，metrics 证书不能写入，二者不计入。`inspect` 的 `ready` 同样要求
project 与 bridge 标记为本租约、且没有其他受限证书；它只读，不修复。`default` project 永远不能
作为 sandbox，Core 与 Provider 都拒绝。

指纹随证书变化：将来的证书重叠轮换（CRED-R-006）必须在同一事务里改写标记。工作区连同 Secret
Store 被整份复制时两份副本持有同一证书，这属于身份复制，本机制不区分。

## network 与 profile

network 名是 `lease` 加 sandbox 名 SHA-256 的前 10 位十六进制（共 15 字符）。不能直接用 sandbox 名：Linux bridge
接口名上限 15 字符，而 `anas-forgejo-runners` 有 20。前缀 `lease` 让宿主的静态转发规则能精确匹配全部租约网桥，
又不落进 `anas-helper` 可以操作的 `anas*` 接口（`INCUS-R-127`）。2026-10 之前的租约用 `anas` 加同一摘要；
`ensure` 建好新网桥并把 profile 指过去后，删除不再被任何实例使用的旧网桥及其 ACL，仍被使用时保留并报告。

bridge 的所有者是 Provider，API 请求显式使用 `project=default`；租约设置
`features.networks=false`、`restricted.devices.nic=managed` 和精确的 `restricted.networks.access`。
这避免 Incus 7.3 不支持非 default project 内 bridge 的问题，同时限制消费者可引用的网络。
实例、profile、证书作用域和配额仍属于独立租约 project。

网络记录 `user.anas.consumer` 与 `user.anas.sandbox`。已有同名网络若类型或归属不符、缺少归属
标记或挂接外部接口，ensure 拒绝接管且不登记证书。不得仅凭派生名字推断所有权。
重复 apply 保留 daemon 已分配的子网及无关配置；关闭 IPv6 时写 `none` 并移除旧 NAT 开关。
网络创建/更新后读回类型、归属、地址及 NAT，再建立 profile。

**地址规划。** IPv4 的 DHCP 动态范围是 `.2` 到广播地址前第 33 个地址，网段顶部 32 个地址留给槽位，按槽位名
排序分配并跨 apply 保持不变；槽位不再声明时释放。租约启用 IPv6 且声明了槽位时，网桥开启有状态 DHCPv6，动态
范围是 `<前缀>::1:0`—`<前缀>::1:ffff`，槽位地址从 `<前缀>::ff00` 起。槽位分配写在网桥的
`user.anas.slot.<名>.{instance,ipv4,ipv6}` 键上，profile 带同样的副本供共享客户端读取。`ensure` 的最后一行 JSON
交回网桥名、IPv4/IPv6 网段与网关和各槽位地址，Core 记入 resource state（`INCUS-R-159`）。

**ACL。** 每张租约网桥挂一份与网桥同名的 Provider 自有 network ACL（default project，带 consumer/sandbox 标记
及租约证书摘要），网桥设 `security.acls=<ACL>` 且两个方向的默认动作都是 `drop`（`INCUS-R-130`）。Incus 先放行
DNS、DHCP 与核心 ICMPv6，回包由 conntrack 放行。规则由冻结的档位生成：

| 档位 | 出站规则（来源都限定为租约自己的网段，伪造的子网外来源因此被丢弃） |
| --- | --- |
| `internet` | 放行公网地址；丢弃局域网、宿主地址与 `$anas-leases` |
| `internet_lan` | 放行公网地址与局域网；丢弃宿主地址与 `$anas-leases` |
| `internet_lan_host` | 放行公网地址、局域网、宿主地址与私有地址段（Docker 已发布端口在 DNAT 后落在这里）；丢弃 `$anas-leases` |
| `modules_only` | 只放行到 `$anas-traefik` 的 Traefik 入口端口 |

`module_access` 给 `internet`、`internet_lan` 加一条到 `$anas-traefik` 入口端口的放行。入站只有 `published` 档有
规则：先丢弃来自 `$anas-leases` 的连接，再放行 `$anas-traefik` 到 HTTP 发布端口、任意来源到各槽位已绑定的
guest 端口。Incus 的 drop 先于 allow，所以同租约实例经宿主端口访问自己的槽位也会被挡住；租约内直连由
`intra_lease` 决定——关闭时 profile 的网卡设 `security.port_isolation=true`，同一网桥的实例之间二层不通
（`INCUS-R-123`）。局域网是 apply 时 Core 算出的宿主默认路由网卡直连网段加 `lan_extra_subnets`，宿主地址是宿主
各接口地址，二者经 `ANAS_RESOURCE_NETWORK`、`ANAS_RESOURCE_LAN_SUBNETS`、`ANAS_RESOURCE_HOST_ADDRESSES` 交给
Provider。

`$anas-leases` 是全局 address set，由每次 `ensure` 改写为全部租约网桥的网段之并；`$anas-traefik` 只由 hostd 的
同步动作写入，Provider 只按名字引用，不存在时 `ensure` 失败并提示先运行 `incus.configure`。两者都需要 daemon
提供 `network_address_set` API 扩展（Incus 7.0 起），缺少时 `ensure` 在写入前失败。`inspect.ready` 要求 ACL 规则
与当前档位、网段、槽位完全一致，`$anas-leases` 覆盖本租约网段；多余规则、禁用或放宽都算漂移，`ensure` 以整体
PUT 修复。宿主包卸载盘点把任何 network ACL 与 address set 视为 daemon 仍在使用。

profile 固定名为 `anas-lease`，只有两个设备：

| 设备 | 内容 |
| --- | --- |
| `root` | `type=disk`、`path=/`、`pool=<storage_pool>`，无 `source` |
| `eth0` | `type=nic`、`network=<租约 bridge>`、`security.mac_filtering=true`、`security.ipv4_filtering=true`，`intra_lease` 关闭时加 `security.port_isolation=true`；无 `parent`/`nictype` |

profile 的配置是 `user.anas.managed=true`、隔离档的 nesting/privileged 键与槽位副本。共享客户端创建槽位实例时用
`--device=eth0,ipv4.address=…`（及 `ipv6.address`）在实例上覆盖这两个键，其余网卡设置沿用 profile。

network 的 IPv6 跟随宿主：Hook 只在 IPv6 开关未关闭**且** `HOST_HAS_IPV6=true` 时置
`INCUS_NETWORK_IPV6=true`，此时 bridge 得到 `ipv6.address=auto` + `ipv6.nat=true`；否则显式写
`ipv6.address=none`。写 `none` 而不是留空是有意的——留空会让 daemon 按自己的默认发地址，而给
guest 一个宿主路由不到的 v6 地址，表现是每次出网先等一次超时再回落 v4，看起来像作业卡住而不像
配置错误。

`ensureProfile` 走的是 **PUT 整体替换**而不是合并，`verifyProfile` 随后读回并要求设备数恰好为 2，
配置恰好是受管模板的键，每个设备的完整属性也必须与受管模板一致。附加 raw config 或设备属性均拒绝。
两者合起来是这条约束的唯一执行点——daemon 不会阻止别人往 profile 上挂东西，所以「profile 上没有
多余设备」只能由这里保证。

第 5 步的两条拒绝是 Provider 侧的越权防线：一张已被以全局权限信任的证书，如果这里默默接受，
消费者拿到的就是整台 daemon。

`inspect` 的 `ready` 要求全部 project 围栏、存储池准入、网络归属/NAT、来源围栏 ACL、profile、受限证书和冻结镜像
当前仍然有效；不是只看 project 存在或网络作用域。撤销证书后 project 保留，但不再 ready。
检查不读取供给文件、不导入镜像、不修复配置或重新授权，仍分别保留 restricted 与 quota 标志。
`inspect` 只读，分别报告 `exists`、`ready`、`restricted`、`quota_enforced`。project 不存在时返回
零值而不是错误，因为「不存在」是一个正常的可观测状态。

`revoke` 删除消费者证书，清空租约 ACL 的规则（默认动作仍是丢弃，从此进出都不通），并停止 project 内运行的
实例：ACL 放行已建立连接的回包，只有停止实例才能结束这些连接（`INCUS-R-124`）。project、实例磁盘与网桥保留；
删 project 会连带销毁里面的实例，而那些实例从来不属于本 Contract。对不存在的 fingerprint 删除是幂等成功，
不属于本租约的 ACL 与 project 不动。

消费者被移除或其能力被关闭、目标部署不再声明租约时，Core 用上一个部署冻结的本 Module 产物调用
`revoke`；失败默认中止激活，只有 `--allow-risky` 才记录未确认的撤销。resource state 保持
`retained` 并记录 `revocation`，细节见 compute Contract 技术文档「租约的结束」。

## 配额映射

Contract 说的是每实例上限，Incus project 说的是项目总量，`projectConfig` 用 `max_instances` 相乘
把两者对上：

| Contract | Incus project 键 | 值 |
| --- | --- | --- |
| `quota.max_instances` | `limits.instances` | 原值 |
| `quota.cpu` | `limits.cpu` | `max_instances × cpu` |
| `quota.memory_mib` | `limits.memory` | `max_instances × memory_mib` MiB |
| `quota.disk_gib` | `limits.disk` | 容器档 `max_instances × disk_gib` GiB；VM 档 `max_instances × (disk_gib GiB + 500 MiB)` |

VM 档多出的 500 MiB 是 Incus 给每个 VM 根块卷附带的状态文件系统卷（`size.state`）：daemon 统计
`limits.disk` 时把它加在根盘大小上。只按根盘预算时，用满单实例磁盘配额的 VM 永远建不出来
（2026-09-26 Incus 7.0.1 实测 `Reached maximum aggregate value`，6.0.5 源码同样计入）。VM 档 profile
的根设备因此显式写 `size.state=500MiB`，让预算不随 daemon 默认值漂移。

项目 `limits.disk` 限制的是声明总量，不能证明根磁盘真的限额。实机探查发现 `dir` 在底层未启用
project quota 时可仅警告并继续创建卷，因此本版仅准入状态 `Created` 的 `btrfs`/`zfs` 池；
`dir`（包括可能已配置底层 quota 的 dir）及其他驱动均拒绝。Provider 不探测宿主文件系统、不自动
转换池、不迁移既有实例。其他驱动须补充能力证明后再准入，不表示上游不支持它们。
`inspect` 重新读取池状态；缺失、未就绪或不支持时 `quota_enforced=false`、`ready=false`，保留
project 的 `exists`/`restricted`。读取故障返回错误，不伪装成缺失。该检查不替代 guest 写满验收，
也不持续监控管理员在租约发出后的存储变更。既有证书不会因失败而自动撤销。
[Incus dir 配额前提](https://linuxcontainers.org/incus/docs/main/reference/storage_dir/#quotas)说明底层条件。

Provider 拥有目标 daemon 认识的全部 `restricted.*` 键：Incus 6.0 LTS（6.0.0—6.0.5，发行版官方
仓库的版本）的完整集合，加上 daemon 通过 API extension 声明支持的 7.x 新键。宿主供给默认安装
Zabbly `lts-7.0`（见[宿主供给设计](../../../docs/architecture/incus-host-provisioning.md) §2.2），
Provider 仍兼容已有的 6.0 daemon。每个键要么显式写入严格值，要么必须不存在：

| 处理 | 键 |
| --- | --- |
| 写入 `block` | `restricted.backups`、`restricted.snapshots`、`restricted.cluster.target`、`restricted.containers.interception`、`restricted.{containers,virtual-machines}.lowlevel`、`restricted.devices.disk\|gpu\|infiniband\|pci\|proxy\|usb\|unix-block\|unix-char\|unix-hotplug` |
| 写入其他固定值 | `restricted=true`、`restricted.containers.privilege=unprivileged`（两档都写）、`restricted.devices.nic=managed`、`restricted.networks.access=<本租约 bridge>`、`restricted.containers.nesting`（VM 档 `block`，容器档 `allow`） |
| 必须不存在 | `restricted.idmap.uid\|gid`、`restricted.networks.integrations\|subnets\|uplinks\|zones`、`restricted.devices.disk.paths`（仅在 disk=allow 时生效）、`restricted.cluster.groups`（仅在允许选择集群目标时生效）、`restricted.images.servers`（7.0+，原因见下） |
| daemon 支持时写入 | `restricted.storage-pools.access=<storage_pool>`（`projects_restricted_storage_pool_access`，7.0+）、`restricted.virtual-machines.nesting=block`（`projects_restricted_virtual_machines_nesting`，7.x，已见于 7.5.1），两档都写 |

`ensure` 与 `inspect` 先读 `GET /1.0` 的 `api_extensions`，不按版本号推断：daemon 拒绝不认识的
project 键，所以 7.x 键只在 daemon 声明支持时写入；而 daemon 支持却不写时，
`restricted.virtual-machines.nesting` 默认 `allow`、`restricted.storage-pools.access` 默认允许所有池，
都是宽松值。daemon 不返回 extension 列表时失败关闭，不当作 6.0 处理。

没有该扩展的 daemon（包括默认安装的 Incus 7.0 LTS）无法从项目层阻止 VM 嵌套虚拟化：`security.nesting`
在那里是容器专用键，VM 接受但不生效，2026-09-26 在嵌套 KVM 宿主上实测 VM 租约 guest 可见 `vmx`。
这是版本限制，不是 Provider 可以补上的配置；需要该保证的部署应使用带 `projects_restricted_virtual_machines_nesting`
的 daemon，或在宿主层关闭嵌套 KVM。

支持 VM nesting 限制的 daemon 默认给 VM 打开嵌套虚拟化，限制设为 `block` 后会拒绝任何没有显式写
`security.nesting=false` 的 VM（7.5.1 `checkRestrictions`）。因此这类 daemon 上 VM 档 profile 同时写入
`security.nesting=false`；更早的版本里该键只对容器有效，VM 会拒绝它，所以不写。升级到这类 daemon 时，
若 VM 档 project 里已有在 profile 更新前创建的 VM，收紧项目限制会与它们冲突而使 `ensure` 失败；
一次性实例回收后即可收敛。收紧存储池后，若已有实例
仍在其他池上（例如改了 `storage_pool` 配置），daemon 拒绝更新，`ensure` 失败，不自动迁移实例。

`restricted.images.servers` 不能用作「禁止远端镜像」：按 Incus 7.0.1 与 7.5.1 源码
（`internal/server/project/permissions.go`），它非空时，从 project 内已有镜像创建实例的请求不带
镜像服务器、主机名为空，同样被拒；它只检查 URL 方式的下载，不检查 simplestreams 复制。设置它会让
租约无法启动自己的镜像，却挡不住镜像导入，所以 Provider 删除它，逐 fingerprint allowlist 仍在消费者
侧执行（R-085）。2026-09-26 已在 Incus 7.0.1 与 7.5.1 实机复现：该键非空时，从 project 内本地镜像创建实例被拒
（`Image server "" isn't allowed in this project`）；simplestreams 复制不受约束仍只来自源码阅读。

`restricted.devices.disk=block` 仍允许根盘，只禁止附加其他磁盘。`incus_container` 为固定的 guest 内
OCI namespace 需求写入 `restricted.containers.nesting=allow`，profile 固定 `security.nesting=true`。
消费者不能逐任务改变这些选项；系统容器档只是比 VM 更弱的**隔离**边界，绝不是更弱的**特权**边界。

隔离档同样写在 project 上，而不只靠共享客户端的 `--vm` 参数：VM 档写 `limits.containers=0`、
`limits.virtual-machines=<max_instances>`，容器档相反。daemon 因此拒绝在 VM 租约里创建与宿主共享
内核的系统容器；允许的那一类也显式写入，避免换档后旧的 `0` 在合并中残留。

这些键必须显式处理，不能只依赖 `restricted=true` 的默认值：`ensure` 合并既有 project 配置，
未受管的键会保留历史 `allow` 或范围。`ensure` 收紧写入键、删除必须不存在的键并读回；`inspect` 对
缺失、放宽或多余的键返回未就绪且不修复。既有 project 带有本版不管理的 `restricted.*` 键（比本版
Provider 更新的 Incus 新增的键，或 daemon 未声明支持的 7.x 键）时，语义未知，`ensure` 在写入前拒绝
并只列出键名，需运维删除或显式迁移。`user.*` 等无关设置仍然保留。该变更不主动删除已有实例或设备；已有实例违反收紧后的限制
（例如 VM 档 project 里已有容器）时 daemon 拒绝更新，Provider 失败关闭，不以清理业务实例强行通过。

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

`endpoint` 与 `server_certificate_b64` 的变更是 `reconcile`；两项管理凭据是 `credential_rotate`。
管理凭据轮换不得重建运行中实例或更换消费者证书。可销毁 VM 生命周期夹具已加入旧/新管理
证书重叠、撤销旧证书及实例身份不变的必需检查；2026-09-23 的新增用例尚无完整原生执行结果，
不能将 helper 单测或交叉编译记作该要求已经验收。

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

2026-09-22 已在独立实验环境验证默认容器档的双租约生命周期、btrfs 根盘实际写满及部分
直接越权拒绝；真实 distrobuilder `lab-r11` 与 Forgejo 正常、失败、controller SIGTERM、
SIGKILL 后保留 state 的恢复/回收也已通过。这些结果不覆盖 VM、ARM64、ZFS、完整设备与
双栈矩阵、state volume 丢失、正式签名发布或生产 ingress。2026-09-23 的显式 proxy 围栏修复
通过本机及指定 Linux 主机 Provider 回归，新版 proxy/管理证书轮换原生用例仍待执行。
发行版 daemon 的结果不等于 7.3.0 全平台验收，状态保持 `developing`。

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
| 隔离档（VM 租约不能创建容器，反之亦然） | daemon（`limits.containers` / `limits.virtual-machines`） | 成立 |
| 镜像 fingerprint allowlist | 消费者共享库 | **不成立** |

即便如此，被攻破的消费者也只能在**自己那个受限、有配额、无设备、无挂载**的 project 里启动计划外
镜像，爆炸半径被其余每一条约束框住。

> [!NOTE]
> 2026-09-10 已核查上游 main 配置参考：镜像服务器域名限制与 project 镜像隔离不等于逐 fingerprint allowlist。
> 2026-09-26 阅读 7.0.1、7.5.1 源码：`restricted.images.servers` 非空时连 project 内本地镜像的实例创建也被拒，
> 且不检查 simplestreams 复制，不能作为镜像约束下沉，Provider 删除该键（见上文「network 与 profile」）。
> 2026-09-27 在 Incus 7.0.1 实测（容器档生命周期 r11）：共享客户端拒绝白名单外的 fingerprint，而租约证书
> 本身可以向自己的 project 导入该镜像并以租约 profile 创建实例。即上表“不成立”一行是实际行为，不是推测。
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
专属轮换命令仍待实现。


## HTTP 发布与端口绑定的宿主侧

HTTP 发布不经过本 Module 的 Provider operation：Provider 只按冻结的发布端口写 ACL 入站规则。消费者把请求文件写进
`HTTP_REQUEST_DIR`，anasd 内的中介校验后写 Traefik 动态目录的 `compute-http/` 子目录，完整语义见
[compute Contract 技术文档](../../../contracts/compute/docs/technical.md#http-发布)。

宿主侧由 hostd 承担，全部随 `incus.configure` 的二段确认安装、随 `incus.uninstall` 删除：

| 产物 | 内容 | 谁改写 |
| --- | --- | --- |
| FORWARD 静态规则（`anas-lease-forward-1`—`6`） | 追加在 Docker 规则之后：租约网桥的转发交给 ACL；从租约网桥到 Docker 网桥只放行 Docker 已 DNAT 的连接（`INCUS-R-126`） | 只在 configure 与开机恢复时 |
| address set `anas-traefik` | Traefik 容器的当前地址，hostd 自己从 Docker 读取 | 同步动作 `incus.traefik.sync`：anasd 在启动时与 Traefik 容器启动时触发（`INCUS-R-118`） |
| `/var/lib/anas/incus-host/network.json` | 操作者批准的端口绑定范围；incus Hook 读取后交给 Core（`INCUS_PORT_BINDING_RANGE`） | 只在 configure |
| nft 表 `inet anas_incus_ports` | Docker 式规则链：目的地址是本机（`fib daddr type local`，回环除外）时按四张端口表做 DNAT | 链随 configure；端口表只由同步动作 `incus.ports.sync` 替换 |
| systemd 单元 `anas-port-{tcp,udp}@<端口>.{socket,service}` | 每个生效的绑定一对：`Accept=no` 的套接字占住端口，常驻的 `sleep infinity`（`DynamicUser=yes`）持有它 | 同步动作先建后删 |
| `anas-incus-network.service` | 开机在 Docker 之后、Incus 之前运行 `anas-hostd --restore-network` | 只在 configure |

`incus.ports.sync` 不带参数：hostd 读取 `/etc/anas/anasd.yml` 登记的各工作区的活动部署、冻结的
`compute_network` 与 resource state 记录的槽位地址，逐条校验协议与端口、批准范围、目标在 `lease*` 网桥自己的
网段内且不是网段/网关/广播地址、端口没有被其他绑定、宿主进程或 Docker 发布占用（`INCUS-R-158`），先为新绑定
启用占位单元，再以单个 nft 事务替换四张端口表并读回，最后停用不再需要的占位。不合格的条目不生效，记成宿主的
运行问题（`/var/lib/anas-hostd/runtime-issues.json`）。生效的条目写入 `/var/lib/anas/incus-host/ports.json`，开机时
占位单元随 `sockets.target` 先于 Docker 启动，`--restore-network` 再按它恢复端口表，占位没能起来的绑定不生效并
记录运行问题（`INCUS-R-161`）。anasd 在启动时与每次部署激活后触发同步；它另外每分钟及在容器启动时只读检查
已生效的绑定，发现占位丢失、Docker 发布冲突或端口表漂移时写进对应工作区的运行问题，不做修复（`INCUS-R-162`）。

没有 hostd 的宿主上，incus Hook 发布 `INCUS_PORT_BINDING_BLOCKER`（`hostd_missing`、`host_not_configured` 或
`remote_daemon`），声明端口绑定的 apply 在 Core 准备阶段失败并说明原因（`INCUS-R-164`）。

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

## 镜像 prune 的控制台确认

界面只使用实际 public job DTO 的 `kind`、`mutating`、workspace/id 和 result，不依赖内部 `job.action`。
显示的计划还需与批准 binding 的 schema、workspace、计划/状态摘要和待删集合一致。合法的空库存
显示“无变更”且不能执行；输入变更、计划过期或组件卸载均不能沿用旧确认。确认 token 不进入公开状态，
执行结果不确定时不自动重试。公开响应缺字段或绑定漂移会拒绝，不通过类型断言兜底。

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

2026-09-26 起同一脚本接受 `--interface container|vm`：VM 档要求实验 VM 内有嵌套 KVM，并以上游
Debian VM 镜像为底、经 agent 推入同一夹具程序后发布、导出成有摘要的 VM 夹具（4 GiB 根盘，
仍不是产品镜像）。两档共用同一测试矩阵，另加设备/低层配置拒绝（容器 unix-char/unix-block/
privileged/`raw.lxc`，VM pci/`raw.qemu`）、`typical-job-wall-time` 时延与 VM 嵌套虚拟化的版本相关检查。
`server-incus-network-e2e.py` 以独立 netns 作上游，验证 IPv6 启用/关闭两种租约的出网均被
masquerade；随后 guest 以静态邻居直送网桥、伪造子网外两族源地址，上游 nft 计数必须为 0，IPv6 租约
还要求伪造 IPv6 报文确实到达实验 VM 的路由层（证明是围栏挡下），并做一次故意漂移（加 allow-all
规则）：泄漏可被计数看见、Provider `inspect` 报未就绪、`ensure` 修复后不再泄漏。宿主侧所有者脚本 `server-incus-lifecycle-lab.py` 在一台一次性 VM 里依次运行三者，
`hold-*` 代号可保留 VM 供诊断，但不计为验收。结果与发现的缺陷见
[两档生命周期](../../../dev-docs/reviews/2026-09-26-incus-vm-tier-lifecycle.md)与
[来源围栏](../../../dev-docs/reviews/2026-09-27-incus-source-fence-acl.md)。

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
