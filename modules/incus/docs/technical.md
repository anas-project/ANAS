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

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `incus.admin_certificate_b64` | string | — | `""` | `static` | `INCUS_ADMIN_CERTIFICATE_B64` | 否 | 是 | 是 | 否：`rotate-incus-admin-credential` | `credential_rotate` | 供给专用的管理客户端证书，不交给任何消费者 |
| `incus.admin_key_b64` | string | — | `""` | `static` | `INCUS_ADMIN_KEY_B64` | 否 | 是 | 是 | 否：`rotate-incus-admin-credential` | `credential_rotate` | 管理证书的私钥 |
| `incus.endpoint` | string | `pattern: ^(?:https://[A-Za-z0-9.:_-]+)?$` | `""` | `static` | `INCUS_ENDPOINT` | 否 | 是 | 否 | 是 | `reconcile` | 远端 Incus daemon 的 HTTPS 地址 |
| `incus.server_certificate_b64` | string | — | `""` | `static` | `INCUS_SERVER_CERTIFICATE_B64` | 否 | 是 | 是 | 是 | `reconcile` | 被固定的 daemon 服务端证书；失配时直接失败，不回退 |
| `incus.storage_pool` | string | `pattern: ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$` | `default` | `static` | `INCUS_STORAGE_POOL` | 否 | 否 | 否 | 是 | `reconcile` | 每个租约根磁盘所在的存储池 |

四项全部经 `.env` 进入 run-only 容器，三项凭据以 base64 PEM 传递，Hook 在 apply 早期校验类型。

## Contract Resource 生命周期

`ensure` 的顺序是刻意的：

1. `GET /1.0/projects/{sandbox}`。存在则把期望配置合并进现有配置后 `PUT`，不存在则 `POST` 新建。
   合并而不是覆盖，是因为 project 里可能有运行中的实例和运维手工加的 `user.*` 键；
2. **读回**并断言 `restricted=true` 且四项 limits 都非空。任一不满足立刻返回错误，且**不继续**
   登记证书——这一步是整个契约唯一的信任来源，写入成功不算数，daemon 自己的副本才算；
3. 建立受管 network 与租约 profile，并把 profile 读回校验。这一步在证书之前：一个还没有根磁盘和
   网卡的租约，把证书发出去也没用；
4. 登记消费者证书。若该 fingerprint 已在信任库中，校验它是 restricted 且 `projects` 恰好只有本
   sandbox；发现它无限制或绑着别的 project 就报错退出，不做任何修改。

## network 与 profile

network 名由 sandbox 名 SHA-256 前 10 位派生（`anas` + 10 位十六进制 = 14 字符）。不能直接用
sandbox 名：Linux bridge 接口名上限 15 字符，而 `anas-forgejo-runners` 有 20。派生保证短、稳定、
租约间不碰撞。

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

第 3 步的两条拒绝是 Provider 侧的越权防线：一张已被以全局权限信任的证书，如果这里默默接受，
消费者拿到的就是整台 daemon。

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

单元级边界齐备，但没有任何一条在真实 Incus/KVM 宿主上验证过：project 与配额是否被 daemon 真正
执行、受限证书能否越出 project、双消费者并行是否互不可见、Provider 中断后残留实例如何回收——
这些都只有 e2e 能回答。状态 `developing` 记录的就是这个缺口。

`ANAS_RESOURCE_IMAGE_ALLOWLIST` 目前只在本 Provider 侧校验格式并交给消费者，镜像固定的最终执行
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
> 「Incus project 没有镜像 allowlist 原生开关」这一判断尚未对照 Incus 7.3 的 project 配置参考核实。
> 若上游存在等价键，应把这条约束下沉到 project，本表随之更新。
