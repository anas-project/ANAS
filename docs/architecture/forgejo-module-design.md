# Forgejo Module 设计

> 状态：**当前模型与明确标注的未实现部分**。Forgejo 应用、controller、Incus adapter 和 guest image
> 资产已实现；独立 Incus/KVM 隔离与真实 one-job E2E 尚未完成，不能视为 release 能力。更新：2026-08-22。

本文记录 Forgejo Module 的身份、Actions 授权、Runner 隔离和高风险功能开关设计。当前实现事实以
[`modules/forgejo/module.yml`](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/module.yml)和
[技术实现](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/docs/technical.md)为准。

## 1. 边界与目标

Forgejo Module 负责代码托管服务端、数据库、持久数据、HTTP/SSH 入口、OIDC 登录和本地恢复管理员。
Actions Runner 是远程代码执行面，必须作为独立服务组件和独立 VM 管理，不进入 Forgejo 应用容器；
controller 可以作为同一 Module 的隔离 Compose service 由唯一开关调和，但不能获得 ANAS 宿主 Docker
socket、数据库或目录管理凭据。

设计目标是：

- Forgejo 服务端可以开启 Actions，但 ANAS 计算资源只分配给明确批准的仓库或组织；
- 作业攻陷只影响一个 Runner VM 和一个信任域；
- 身份集成只声明固定 Forgejo 版本能够安全证明的能力；
- 高风险服务器功能保持默认关闭，管理员可以显式选择承担风险。

## 2. 身份设计决策

### 2.1 双链路策略的通用门禁

ANAS 对具备原生能力的应用优先采用“LDAP 预配用户/Group + OIDC/SAML 登录”：目录负责生命周期和
Group，登录协议负责交互认证，两条链路以不可变 `anasIdentityAnchor` 关联。应用必须同时具备：

1. 把 `anasIdentityAnchor` 配置为 LDAP 用户和 Group 的持久 UUID；
2. 用 OIDC/SAML 中的同一 anchor 安全定位既有 LDAP 用户；
3. 对缺失、重复和冲突 anchor fail closed；
4. 通过受支持 API/配置完成关联，不按用户名/邮箱回退，也不直接修改私有数据库表；
5. 用户停用、改名、Group 撤权和 session 行为有真实 E2E 证据。

这是一项能力门禁，不是要求所有应用都实现两条链路。

### 2.2 Forgejo 结论：LDAP 同步 + OIDC 登录的双源形态

> 状态：**2026-09-13 决定，尚未实现。** 本节取代此前"不实现双链路"的结论；实现前 Module 保持
> OIDC-only 的现状。验收条目见 `FORGEJO-R-063`—`FORGEJO-R-065`，原 `FORGEJO-R-006` 已废弃。

固定 Forgejo v15 能做 LDAP 用户同步、LDAP Group 成员校验和 OIDC 登录。原结论拒绝同时使用两者，
理由是 LDAP source 没有可配置的不可变用户 UUID 字段，OIDC source 也没有按 anchor claim 绑定既有
LDAP 用户的接口——绑定只能落回用户名或邮箱。

改变结论的不是上游新增了 anchor 绑定，而是**需求变了**：[AI Agent 编排](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/docs/architecture/orchestration-design.md) §6.2
需要组变更秒级生效。只有 OIDC 一条链路时，组声明随 claim 在**登录时**才到达 Forgejo，撤权要等
用户下次登录。让 Forgejo 通过 LDAP 保有目录副本，它就落入[目录事件订阅要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-event-subscription.md)
的范围，可以像 authentik 与 Casdoor 那样订阅目录事件日志并按游标增量刷新。

因此确定设计是：

- **LDAP source（只读）同步用户与组**，目录仍是唯一事实来源；Forgejo 侧不写回；
- **OIDC source 负责交互登录**；
- **`ACCOUNT_LINKING=auto`**：OIDC 登录按用户名或邮箱自动绑定到 LDAP 同步出的既有账号；
- Forgejo 订阅目录事件日志，在声明的最大传播时间内完成增量刷新；
- 管理员 Group 仍映射为 Forgejo site administrator，仓库与 Organization 授权仍由 Forgejo 管理。

**`auto` 的风险与它成立的条件。** 上游对 `auto` 的警告是"同用户名或同邮箱就授予既有账号访问权"。
在本部署里两条链路指向同一个 Samba AD，同一个人两边的 `sAMAccountName` 与 `mail` 本就一致，因此
危险的不是两个人撞同一个标识符，而是**同一个标识符先后属于不同的人**。三条前提必须同时成立，
`auto` 才是安全的：

1. **IAM 不得允许用户自助修改 `mail` 与 `sAMAccountName`**。这是硬前提：两家 Provider 都保存目录
   影子记录，写回 AD 的 `svc_ldap` 是只读，所以自助修改只落在 IAM 本地，却会直接进入 OIDC 的 email
   claim，直到下一次全量同步覆盖为止——足够完成一次冒名绑定。该禁令已写进
   [IAM Provider 要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/iam-provider.md) §1.6。
2. **不得再启用第二个 OIDC/OAuth source**（例如给外部贡献者接 GitHub 登录）。一旦存在不受本部署
   控制的身份源，`auto` 就变成"外部身份按邮箱认领内部账号"。需要外部协作时必须改用其他机制，
   而不是加一个登录源。
3. **邮箱别名与用户名不得回收再分配**，改名走正式流程。这条对 `auto` 与 `login` 一样成立：
   Forgejo 的 LDAP source 没有不可变 UUID，回收的地址会让新人接上旧账号。

前两条是配置约束，可以在验收里检查；第三条是运维约束，必须写进目录管理流程。任何一条不成立时，
退回 `ACCOUNT_LINKING=login`（要求用户先登录既有账号自证所有权）是正确的降级，代价是每人一次
交互。

仍然不做的：SAML source、密码回写、`anasIdentityAnchor` 的自动 reconciler，以及发布一个 Forgejo
不消费的 anchor claim。anchor 仍是目录侧的永久身份键，Forgejo 只是还消费不了它。

## 3. Actions 授权模型

Actions server 与 Runner 是同一个产品功能。管理员只应看到一个 ANAS 功能开关：
`forgejo.actions_enabled`。它已接入配置 inventory，同一个值同时投影到 Forgejo 服务端、
Runner controller 与 Runner desired state。默认值为 `false`；Incus credential、scope、profile 或
固定 image fingerprint 缺失时 Hook 拒绝开启，不能形成只能开启服务端的半功能状态。

Runner 可以保持独立镜像、版本、发布节奏和权限边界，但它是由上述 desired state 自动派生的内部
执行组件，不得再提供 `runner.enabled`、`forgejo_runner.enabled` 或要求管理员手工启用第二个 Module。

唯一开关之外还有两层**授权**，它们不是功能开关：

1. 仓库管理员在 Forgejo 仓库 Units 中决定仓库是否使用 Actions；
2. ANAS 实例管理员批准 `{owner}/{repo}` 或 `{owner}` Runner scope，controller 只为批准 scope 注册
   Runner。

不部署 global runner。仓库即使打开 Actions，没有匹配的 repo/org Runner 也不能使用 ANAS 计算资源。
能修改 `.forgejo/workflows` 的写入者等价于能在对应 Runner 上执行代码，因此一个 repo/org scope
只能覆盖写入者属于同一信任域的仓库；每个 VM 仍只执行其中一个作业。

状态调和遵循一个入口：开启时先验证 compute Provider、scope policy 和 credential prerequisites，再
启用服务端与执行面；关闭时先阻止新任务，再注销 Runner 并回收空闲/排队 VM。失败状态由同一
controller 和 janitor 收敛，不能要求操作者切换第二个开关补偿。

当前实现采用常驻轻量 controller + 按需 VM：controller 开启时默认每 15 秒查询获批 scope，空队列
不会注册 Runner 或创建 VM；关闭时执行一次 cleanup 后退出。controller 本身的空闲 RSS/CPU 指标仍须
按 R-044 在真实环境测量，不能用“没有 Runner VM”替代 controller 资源基准。

## 4. VM 技术选型：Incus VM

Runner VM 统一选择 **Incus VM（QEMU/KVM）**，不是 Incus system container，也不并列支持
libvirt、Proxmox 或 Firecracker。选择 Incus 的原因是它提供统一 REST API、VM 模板、项目隔离、
资源上限、restricted client certificate、cloud-init 和 instance agent；Incus VM 使用独立 guest
kernel，隔离边界强于同宿主 DinD 或 system container。

官方依据：Incus [VM 实现](https://linuxcontainers.org/incus/docs/main/explanation/instances/)、
[Project 限制与配额](https://linuxcontainers.org/incus/docs/main/reference/projects/)和
[受限 TLS client](https://linuxcontainers.org/incus/docs/main/howto/projects_confine/)。

### 4.1 Incus 控制面

- Incus daemon 安装在具备 KVM 的独立虚拟化宿主，不运行在 Forgejo 容器中；
- 创建 restricted project `anas-forgejo-runners`，隔离 image、profile、network 和 storage volume；
- 设置 VM 数量、CPU、memory 和 disk 总上限，禁止 low-level QEMU options、设备直通和嵌套虚拟化；
- Runner controller 使用只绑定该 project 的 restricted TLS certificate，不获得 Incus 全局管理权限；
- cloud-init 只创建用户、安装固定包和写入非敏感配置；Forgejo runner token 不进入 image 或
  cloud-init metadata，而是在 VM 启动后经 Incus agent/stdin 写入 tmpfs。

Forgejo 应用容器只提供 Actions API，不持有 Incus endpoint、client certificate 或 VM 创建权限。

### 4.2 单作业 Runner VM

Runner 从第一版起就是单作业 VM，不交付跨作业复用的持久 Runner：

1. Controller 为批准的 repo/org scope 创建 Forgejo ephemeral runner；
2. 从只读模板创建 Incus VM，把一次性 runner token 经 Incus agent/stdin 写入 tmpfs；
3. `runner-agent` 用户执行 `forgejo-runner one-job`，`runner-engine` 用户提供 rootless Podman；
4. Runner 不提供 `host` label，保持 `privileged=false`、`valid_volumes` 为空；
5. 作业结束后验证 Forgejo 已注销 runner token，然后销毁 VM、root disk 和临时 Secret；
6. janitor 按 runner UUID 与 Incus instance ID 回收启动失败、取消、超时和 controller crash 残留。

默认不预热 VM。Controller 轮询获批 scope 的 Forgejo v15 `actions/runners/jobs` API，只对 `waiting`
job 创建 ephemeral registration 和 VM，并把 API 返回的 job `handle` 交给
`forgejo-runner one-job --handle ... --wait`，避免多个 Runner 竞争错领任务。`waiting` 仍可能表示作业
被 concurrency group 暂时阻塞，因此已启动但尚未领取任务的 VM 由 Controller 施加 10 分钟 TTL；
Forgejo Runner 本身没有等待超时，不能依赖它自行退出。

未来如管理员显式启用预热，每个已启用 scope 最多保留一个**尚未启动 Runner 进程**的预热 VM；默认
`prewarm=0`。预热 VM 收到具体 job handle 后才创建 registration、注入 token 并启动 Runner，执行
一次后销毁。扩容只能增加独立 one-job VM 数量，不能提高同一 Runner 的 capacity。

VM 不挂载 NAS、ANAS workspace、Forgejo data、宿主 Docker/Podman socket 或 Secret Store；默认无入站
端口，出站只允许 Forgejo HTTPS、DNS/NTP 和明确批准的 registry/package mirror。需要严格 Docker
Engine 兼容时，可以在同一 Incus VM 边界内把 rootless Podman 换成 rootless Docker。privileged DinD
不作为默认方案，也不得运行在 ANAS 核心服务宿主。

## 5. 高风险功能开关

Module 增加两个正向布尔配置，默认均为 `false`：

| 配置 | 环境变量 | 上游映射 |
| --- | --- | --- |
| `forgejo.custom_git_hooks_enabled` | `FORGEJO_CUSTOM_GIT_HOOKS_ENABLED` | 取反写入 `security.DISABLE_GIT_HOOKS` |
| `forgejo.local_path_import_enabled` | `FORGEJO_LOCAL_PATH_IMPORT_ENABLED` | 直接写入 `security.IMPORT_LOCAL_PATHS` |

两个变更都触发 `container_recreate`。开启 custom Git Hooks 表示允许服务器端任意代码执行；开启
local-path import 只放开 Forgejo 功能开关，不自动增加宿主挂载。若后续确需本地导入，只允许固定
只读 staging directory，不接受任意宿主路径。

## 6. 非目标

- Forgejo LDAP/SAML 与 `anasIdentityAnchor` 自动关联；
- global Runner 或共享 ANAS 宿主 Docker socket；
- 在 Forgejo 容器内调用 Incus/libvirt/hypervisor API；
- 在 ANAS 核心服务宿主运行 privileged DinD；
- 不受约束的宿主目录导入或默认开启 custom Git Hooks。
