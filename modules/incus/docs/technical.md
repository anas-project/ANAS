# Incus compute provider 技术实现

本文记录 `incus` Module 的 Provider 实现与安全边界。配置与操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `7.3.0-r1` / `anas.module/v1`.
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
| `anas_incus_provision` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-incus-provisioner:7.3.0-r1` | `incus` | 0 |
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

## 固定控制转发组件（未安装、未验收）

`modules/incus/control-relay` 是为宿主回环连接候选方案编写的 Linux 非 root 传输组件，
**不是当前 Compose 服务，尚未打包、自动安装或启用**。它只把安装配置指定的控制 bridge
IPv4/高位端口原样转发到编译期固定的 `127.0.0.1:8443`，不接受 upstream、HTTP CONNECT、
SOCKS、TLS 密钥或调用方命令；Incus mTLS 与原服务端 pin 仍由原两端核验。

组件要求 root 所有且不可被组/其他用户写入的配置及父目录、专用非 root UID/GID、无额外附加组
和无 capabilities。配置绑定接口名称、index、网段与网关；接口漂移时停止服务，不改绑通配地址。
连接数、拨号期限和双向空闲期限受限，保留半关闭，停止时关闭现存连接。配置字段属于未来的宿主
安装流程，不是本节下面的 `incus.*` Module 参数，也不改变当前远端 daemon 的接入方式。

宿主安装动作、官方二进制发布、服务单元、INPUT/FORWARD 入接口规则、接口重建协调、endpoint
投影以及 mTLS/pin/project/跨网络与卸载实机验收仍未完成。源 CIDR 检查不能代替防火墙或认证。
本地配置和传输测试源码已补但未运行，生产 ingress 保持关闭；详细边界见宿主供给架构 §3.8。

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `incus.admin_certificate_b64` | string | — | `""` | `static` | `INCUS_ADMIN_CERTIFICATE_B64` | 否 | 是 | 是 | 否：`rotate-incus-admin-credential` | `credential_rotate` | 供给专用的管理客户端证书，不交给任何消费者 |
| `incus.admin_key_b64` | string | — | `""` | `static` | `INCUS_ADMIN_KEY_B64` | 否 | 是 | 是 | 否：`rotate-incus-admin-credential` | `credential_rotate` | 管理证书的私钥 |
| `incus.endpoint` | string | `pattern: ^(?:https://[A-Za-z0-9.:_-]+)?$` | `""` | `static` | `INCUS_ENDPOINT` | 否 | 是 | 否 | 是 | `reconcile` | 远端 Incus daemon 的 HTTPS 地址 |
| `incus.image_architecture` | enum (`amd64`, `arm64`) | — | — | — | `INCUS_IMAGE_ARCHITECTURE` | 否 | 是 | 否 | 是 | `container_recreate` | 目标 daemon 的 guest 镜像架构；必须显式提供，不从 CLI 宿主推断 |
| `incus.server_certificate_b64` | string | — | `""` | `static` | `INCUS_SERVER_CERTIFICATE_B64` | 否 | 是 | 是 | 是 | `reconcile` | 被固定的 daemon 服务端证书；失配时直接失败，不回退 |
| `incus.storage_pool` | string | `pattern: ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$` | `default` | `static` | `INCUS_STORAGE_POOL` | 否 | 否 | 否 | 是 | `reconcile` | 每个租约根磁盘所在的存储池 |

四项全部经 `.env` 进入 run-only 容器，三项凭据以 base64 PEM 传递，Hook 在 apply 早期校验类型。

## Contract Resource 生命周期

`ensure` 的顺序是刻意的：

1. 先读取 default project 的 `GET /1.0/storage-pools/{pool}`，要求名称匹配、状态为 `Created`，
   且驱动为本版准入的 `btrfs` 或 `zfs`；不支持时在任何租约写入前失败。随后
   `GET /1.0/projects/{sandbox}`。存在则把期望配置合并进现有配置后 `PUT`，不存在则 `POST` 新建。
   合并而不是覆盖，是因为 project 里可能有运行中的实例和运维手工加的 `user.*` 键；已有
   `features.networks=true` 的 project 直接拒绝，要求显式迁移，不自动切换网络归属；
2. **读回**并断言 `restricted=true`、四项 limits 都非空、再次读取存储池准入条件，且 network feature 关闭、NIC 为 managed、
   `restricted.networks.access` 恰好等于本租约 bridge。任一不满足立刻返回错误，且**不继续**
   登记证书——这一步是整个契约唯一的信任来源，写入成功不算数，daemon 自己的副本才算；
3. 在 default project 建立受管 bridge 并读回校验，再在租约 project 建立 profile 并读回校验。这一步在证书之前：一个还没有根磁盘和
   网卡的租约，把证书发出去也没用；
4. 读取目标 project 的每个冻结镜像，校验 fingerprint、架构与类型；缺失或失配即失败，不查询 alias；
5. 登记消费者证书。若该 fingerprint 已在信任库中，校验它是 restricted 且 `projects` 恰好只有本
   sandbox；发现它无限制或绑着别的 project 就报错退出，不做任何修改。

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

`ensureProfile` 走的是 **PUT 整体替换**而不是合并，`verifyProfile` 随后读回并要求设备数恰好为 2。
两者合起来是这条约束的唯一执行点——daemon 不会阻止别人往 profile 上挂东西，所以「profile 上没有
多余设备」只能由这里保证。

第 5 步的两条拒绝是 Provider 侧的越权防线：一张已被以全局权限信任的证书，如果这里默默接受，
消费者拿到的就是整台 daemon。

`inspect` 的 `ready` 还要求上述精确网络作用域成立，仍分别保留 restricted 与 quota 标志。
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

Hook 只实现 `calculate`：派生 `INCUS_NETWORK_NAME`，并在四项凭据不完整时拒绝。拒绝发生在 apply
早期，而不是供给中途——半配置的 Provider 比一个根本没启动的 Provider 更难排查。

`endpoint` 与 `server_certificate_b64` 的变更是 `reconcile`；两项管理凭据是 `credential_rotate`，
且轮换不影响运行中实例。

## 测试与实现位置

| 位置 | 内容 |
| --- | --- |
| `provisioner/client.go` | REST 信封解析、证书固定、not-found 归一 |
| `provisioner/ops.go` | `ensure`/`inspect`/`revoke` 与配额映射 |
| `provisioner/main.go` | 参数与环境校验、隔离档分派 |
| `provisioner/provisioner_test.go` | 假 daemon 覆盖幂等、fail-closed、越权证书拒绝、固定失配、输入校验与敏感值不回显 |
| `hook/main_test.go` | 凭据完整性与不回显 |

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

## Split 镜像工件离线核验（代码未验收）

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
及尾随数据。新增单元/命令用例尚未运行；该工具不构成 M12 或 M13 的实机验收。

## 本地镜像产物归档（代码未验收）

`cmd/incus-image-artifacts` 在上述只读核验之外提供显式的本地归档写入，复用相同的
`ArtifactRelease` 和 fingerprint 算法，没有第二套镜像协议。它面向发布准备工具，不是安装器、
Provider 动作、浏览器接口或消费者 API；当前支持 Linux/macOS 上由当前执行用户独占的本地目录。
源码与新增测试尚未编译或执行，以下命令只是待验证的用法示例。

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
受信归档与历史目录需要独立备份；当前只冻结本地记录，尚未完成发布签名、产物分发、Provider 导入、
真实镜像启动或保留当前/上一个 deployment 的 prune。生产目录仍为空，M12/M13 不据此验收。

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

## HTTP 周期对账与渲染接入（代码未验收）

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

服务端受限只读身份、外部路由库存、宿主动作/IP 保留、探测、孤立工件恢复和窄范围命名密钥交付
仍是必需前置，当前没有启动 daemon 或开放生产 ingress。没有增加 Go 依赖；上述代码未编译或测试。

## HTTP 实例事实读取（代码未验收）

`IncusFactReader` 已提供可注入 `WorkspaceSource.Facts` 的 GET 实现。固定 HTTPS endpoint、证书
和租约映射来自管理员安装配置，消费者请求不能指定它们。最低 TLS 1.3、服务端精确证书 pin、双方
证书有效期检查；拒绝重定向、环境代理及非成功 JSON。每个 GET 限 8 秒/2 MiB，完整双采样限 30 秒，
错误不包含 endpoint、私钥或响应正文。连接失败不回退到 Unix socket。

读取范围是服务器身份、选定 project、该租约的 default-project bridge、选定实例/状态及 bridge
分配表；验证版本、restricted 围栏、bridge 归属/NAT、唯一受管 NIC、MAC 和唯一私有 IPv4 分配。
两次选定事实必须一致，不能只看地址落在子网内。UUID、generation 和最后启动时间派生的 incarnation
写入执行目标并在每步重查，覆盖 UUID/IP/MAC 不变的快速重启；缺失字段直接拒绝。此摘要不是地址
保留，宿主仍需独立验证当前映射。旧实验回执缺少 incarnation 时拒绝读取，不自动清理外部工件。

普通 Incus restricted TLS 证书仍有本项目写权限。GET 代码不构成服务端只读身份，专用授权供给仍待
实现；未更改 daemon 全局授权或启用中介。字段依据 Incus v7.3.0 官方 API，版本 pin 不代表兼容性
验收。读取器尚未实例化、编译或测试，新增用例已列入 `test-env/fixtures/incus-network-prototype/e2e-plan.md`。

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
