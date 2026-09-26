# Incus compute provider

把一台独立的 Incus 宿主变成 `compute` Contract 的 Provider：在 apply 时为每个消费者建好受限
project、配额与专属证书，之后不再参与实例的创建与销毁。

系统容器档为运行内层 OCI workload，在 Provider 固定 profile 中允许 namespace nesting；
外层仍强制非特权，宿主设备、宿主路径挂载和 raw 配置仍拒绝。它与嵌套虚拟机不同，也不
等同于验证内层引擎 rootless。共享内核的攻击面需纳入评估；跨信任域或不可信任务选择显式
VM 档。没有新增调用方 nesting 开关，也不会自动改写或重启旧实例。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `incus` |
| 版本 / revision | `7.3.0-r2` |
| 状态 | `developing` |
| 类别 | `compute` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 这个 Module 做什么、不做什么

它交付的是**围栏**，不是机器。`ensure` 在 apply 时把一个 restricted project 建好、把配额写到
project 上、把消费者的客户端证书登记为只绑该 project 的受限证书；之后消费者拿着这张证书自己
连 Incus daemon，自己创建和销毁一次性实例。ANAS 不在这条运行时路径上。

因此它**不**做这些事：

- 不安装、不配置、不托管 Incus daemon 本身；daemon 运行在另一台具备 KVM 的宿主上；
- 不要求 ANAS 宿主具备虚拟化能力，不挂载宿主虚拟化设备或 Docker socket；
- 不暴露 HTTP 服务、Traefik 路由或宿主端口；
- 不代理实例的 `create`/`start`/`exec`/`delete`，也不保管消费者的一次性 Secret。

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `compute` | 提供的 Contract | `1.0.0` / `incus_vm` |
| `compute` | 提供的 Contract | `1.0.0` / `incus_container` |

没有 Module 依赖，也不提供 Capability：本 Module 只经 Contract Provider operation 被 Runner
调用。

## 前置条件：先准备好 Incus 宿主

> [!NOTE]
> 本节描述的是**高级路径**：连接你自建的、ANAS 之外的 Incus 宿主。
>
> 默认路径应当是 ANAS 在本机自动装好 daemon、自动签发证书，用户不需要执行本节任何一步。该自动
> 化尚未实现，设计见 [Incus 宿主供给与镜像烘焙](../../docs/architecture/incus-host-provisioning.md)；
> 在它落地之前，本节是唯一可用的路径，这也是本 Module 状态仍为 `developing` 的原因之一。

自建宿主时这一步在 ANAS 之外完成，本 Module 只消费其结果。
根磁盘存储池须为已创建的 `btrfs` 或 `zfs`；当前 Provider 拒绝 `dir` 和未准入的其他驱动，
避免把已设置 `limits.disk` 误报为实际磁盘限额。不会自动转换池或迁移数据。

```bash
incus config set core.https_address :8443
```

取回 daemon 的服务端证书，它将被固定（pin）：

```bash
openssl s_client -connect INCUS_HOST:8443 </dev/null 2>/dev/null | openssl x509 > server.crt
```

为 ANAS 的**供给操作**签发一张管理证书，并加入信任库。这张证书只有本 Module 持有，用来创建
project 和登记消费者证书；它不会交给任何消费者：

```bash
openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 -nodes -days 3650 -subj "/CN=anas-provisioner" -keyout admin.key -out admin.crt
```

```bash
incus config trust add-certificate admin.crt
```

## 最简配置

四项都必须提供，缺任一项 Hook 会在 apply 阶段直接拒绝，而不是在供给中途失败：

```yaml
modules:
  incus:
    endpoint: https://incus.example:8443
    server_certificate_b64: "<base64 of server.crt>"
    admin_certificate_b64: "<base64 of admin.crt>"
    admin_key_b64: "<base64 of admin.key>"
```

生成 base64 值：

```bash
base64 -w0 server.crt
```

## 供其他 Module 使用

消费者不依赖 `incus`，只在自己的 manifest 里声明 Contract 与 Resource：

```yaml
dependencies:
  contracts:
    - name: compute
      version: ">=1.0.0 <2.0.0"
      selected_by: isolation
      interfaces: [incus_container, incus_vm]
      default: incus_container
resources:
  requires:
    - id: runners
      contract: compute
      binding: isolation
      spec:
        sandbox: anas-forgejo-runners
        instance_prefix: anas-fj-
        quota: {max_instances: 8, cpu: 4, memory_mib: 8192, disk_gib: 40}
        image_allowlist: [{fingerprint: "<64hex>"}]
        credential: {policy: generated}
        deletion_policy: retain
```

Runner 会为该消费者生成证书、调用本 Module 的 `ensure`，并向消费者容器发布 endpoint、project、
实例前缀、镜像 allowlist、配额与证书。字段清单见
[compute Contract 技术文档](/reference/module-contracts/compute-technical)。

## 两个隔离档

| Interface | 实例形态 | 边界 |
| --- | --- | --- |
| `incus_vm` | QEMU/KVM 虚拟机 | 独立 guest kernel；project 以 `limits.containers=0` 拒绝创建容器 |
| `incus_container` | 非特权系统容器（LXC） | 与宿主共享内核，project 强制 `unprivileged` 并以 `limits.virtual-machines=0` 拒绝创建 VM |

schema 与操作语义完全一致，选哪一档是部署决策。**默认是 `incus_container`**——NAS 宿主不保证
有 KVM，把需要 KVM 的档位设为默认会让服务在目标硬件上根本装不上。VM 是显式升级，不是静默降级：
Provider 不会因为宿主内存不足或缺少 KVM 就把 VM 悄悄换成容器，而是失败并说明原因。

## 身份、用户与 Group

不适用。本 Module 没有用户、没有 Web UI、不接入 IAM，也不提供登录入口。它的唯一身份是两张 TLS
证书：一张供给用的管理证书（本 Module 持有），以及每个消费者一张只绑自己 project 的受限证书
（Runner 生成、消费者持有）。

## 管理员登录与 IAM 故障恢复

不适用，本 Module 无登录入口。IAM 故障不影响它：供给走 mTLS，与 IAM 无关。

若管理证书泄漏或需要轮换，在 Incus 宿主上撤销旧证书并重新签发，然后更新 `admin_certificate_b64`
与 `admin_key_b64`：

```bash
incus config trust remove <fingerprint>
```

轮换管理证书**不影响运行中的实例**：实例由消费者证书驱动，与管理证书无关。

## 数据库支持

不适用。本 Module 无数据库、无持久卷，所有状态都在远端 Incus daemon 里。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；
不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `incus.admin_certificate_b64` | string | — | — | `host` | `INCUS_ADMIN_CERTIFICATE_B64` | 否 | 是 | 是 | 否：`rotate-incus-admin-credential` | `credential_rotate` | 供给专用的管理客户端证书，不交给任何消费者 |
| `incus.admin_key_b64` | string | — | — | `host` | `INCUS_ADMIN_KEY_B64` | 否 | 是 | 是 | 否：`rotate-incus-admin-credential` | `credential_rotate` | 管理证书的私钥 |
| `incus.endpoint` | string | `pattern: ^https://[A-Za-z0-9.:_-]+$` | — | `host` | `INCUS_ENDPOINT` | 否 | 是 | 是 | 是 | `reconcile` | 远端 Incus daemon 的 HTTPS 地址 |
| `incus.image_architecture` | enum (`amd64`, `arm64`) | — | — | `host` | `INCUS_IMAGE_ARCHITECTURE` | 否 | 是 | 否 | 是 | `container_recreate` | 目标 daemon 的 guest 镜像架构；必须显式提供，不从 CLI 宿主推断 |
| `incus.server_certificate_b64` | string | — | — | `host` | `INCUS_SERVER_CERTIFICATE_B64` | 否 | 是 | 是 | 是 | `reconcile` | 被固定的 daemon 服务端证书；失配时直接失败，不回退 |
| `incus.storage_pool` | string | `pattern: ^[a-zA-Z0-9][a-zA-Z0-9._-]{0,62}$` | — | `runtime` | `INCUS_STORAGE_POOL` | 否 | 是 | 否 | 是 | `reconcile` | 每个租约根磁盘所在的 Incus 存储池；改它不会迁移已有实例 |

### 查询和修改

```bash
anas config list incus -w /srv/anas
```

```bash
anas config set incus.image_architecture amd64 -w /srv/anas
anas config set incus.endpoint https://incus.example:8443 -w /srv/anas
```

不要在 shell argv 中直接写证书与私钥；用受保护的配置导入流程设置它们。

## 故障排查

供给失败时错误来自 apply 输出。常见原因与含义：

| 错误 | 含义 |
| --- | --- |
| `does not match the pinned certificate` | daemon 换过证书，或 endpoint 指向了别的主机。确认后更新 `server_certificate_b64`，不要绕过校验 |
| `is not restricted after ensure` | daemon 接受了写入但没应用 `restricted`，供给已 fail closed，不会继续登记证书 |
| `must be a Created btrfs or zfs pool` | 存储池不存在、未就绪或驱动未准入；先准备受支持的池，已有数据需单独规划迁移 |
| `has no enforced quota after ensure` | project 存在但配额未生效，同样 fail closed |
| `is already trusted without a project restriction` | 该证书此前被以全局权限加入过信任库；先移除旧条目再重新 apply |
| `is scoped to ... not to ...` | 同一张证书已绑定别的 project，拒绝跨 project 复用 |
| `belongs to another lease` | 同名 project 已属于另一份租约（常见于第二个工作区连到同一 daemon 且声明了相同 sandbox）；Provider 不接管，也不因原租约证书已撤销而放行。确认后删除该 project 或改用其他 daemon |
| `is also trusted by other restricted certificates` | 除本租约外还有其他受限证书能操作该 project（错误只列指纹前缀）；确认它们已不再使用后从信任库移除，再重新 apply |
| `carries restriction keys this provider does not manage` | 既有 project 带有本版不管理的 `restricted.*` 键（错误只列键名，常见于比本版 Provider 更新的 Incus 新增的键）；确认后手工删除这些键或显式迁移 project，再重新 apply |
| `is too low: there currently are ... instances of type ...` | 隔离档与 project 里已有实例类型冲突（例如 VM 档 project 里已有容器），daemon 拒绝收紧；先处理这些实例，Provider 不会替你删除 |

## 当前限制

状态为 `developing`：独立 Incus 6.0.5 的供给探查发现了 dir 磁盘配额缺口，尚未通过 guest
启动与完整配额、证书和双消费者 E2E。7.3.0/KVM 仍待单独验收。在那之前不要把它当作 `release` 能力使用。

`revoke` 只撤销消费者证书，不删除 project——project 里的实例从来不属于本 Contract。

## 技术文档

REST 边界、证书固定、幂等与测试见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`7.3.0-r2`（reviewed 2026-09-21）
- Timezone / 时区：`not_applicable` — The provisioner is a one-shot process with no scheduling, retention or timestamp output; the remote Incus daemon keeps its own clock.
- Language scope / 语言范围：no user interface; the provisioner emits machine-readable JSON only
- Selection / 选择方式：`none`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：none
- Fallback / 回退：Diagnostics are English-only operator output on stderr.
- Supported languages / 支持语言：not applicable / 不适用

Evidence / 证据：

- [7.3.0 — Incus REST API; this module ships no upstream user interface](https://github.com/lxc/incus/tree/v7.3.0)
<!-- generated:localization:end -->
