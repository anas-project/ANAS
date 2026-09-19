# Forgejo Module 设计

> 状态：**当前模型与明确标注的未实现部分**。Forgejo 应用、controller、Incus adapter 和 guest image
> 资产已实现；独立 Incus/KVM 隔离与真实 one-job E2E 尚未完成，不能视为 release 能力。更新：2026-09-20。

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

### 2.2 Forgejo 结论：只用 OIDC 登录

> 状态：**2026-09-20 决定并已实现**。此前 2026-09-13 的"LDAP 同步 + OIDC 登录双源形态"结论连同其
> 实现一并撤回，`FORGEJO-R-063`—`R-065` 作废，`FORGEJO-R-006` 恢复为本节的规范来源，新增
> `FORGEJO-R-066` 约束升级复核。

固定 Forgejo v15 分别会做 LDAP 用户同步、LDAP Group 成员校验和 OIDC 登录，但**不能把两条链路安全地
接起来**：LDAP source 没有可配置的不可变用户 UUID 字段，OIDC source 也没有按 anchor claim 绑定既有
LDAP 用户的接口——绑定只能落回用户名或邮箱。§2.1 的四条门禁里第 1、2、4 条都不成立，因此 Forgejo
不进入双链路形态，只保留 OIDC 一条链路：用户由 OIDC JIT 创建，Organization/Team 仍由 Forgejo 管理。

**为什么 2026-09-13 的双源结论被撤回。** 当时改变结论的不是上游新增了 anchor 绑定，而是
[AI Agent 编排](https://github.com/anas-project/ANAS/blob/master/modules/ai_agent/docs/architecture/orchestration-design.md) §6.2
需要组变更秒级生效：让 Forgejo 经 LDAP 保有目录副本，它就能订阅[目录事件日志](https://github.com/anas-project/ANAS/blob/master/docs/architecture/directory-event-journal.md)
并按游标增量刷新。实现时核对固定版本源码 `cmd/admin_auth_ldap.go` 后确认，**LDAP CLI 没有任何组同步
选项，产品也没有认证源 REST API**，组只能继续经 OIDC 声明在登录时映射 team。也就是说双源形态换不到
§6.2 真正要的 team 成员实时性，只能换到"账号能否登录"的加速，而代价是：

- 引入一份目录副本，从此落入[目录事件订阅要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-event-subscription.md)
  的全部订阅、收敛与 E2E 义务；
- `ACCOUNT_LINKING=auto` 的三条前提，其中"部署只能有一个 OAuth2 source"是一条会限制未来外部协作
  （例如给外部贡献者接第三方登录）的硬约束；
- 一个常驻 watcher 进程、一个专用站点管理员账号和它的 Secret。

用一条硬约束加一份目录副本换半个收益，不成立。**安全边界没有变差**：撤权本来就不能只靠 LDAP 同步，
因为 access token 与 SSH key 根本不经过登录。

**OIDC-only 的代价必须写明，并由运维承担。** 按 `DIRSYNC-R-014`，这里声明缺失的是哪一侧、兜底是什么：

| 目录侧变更 | Forgejo 侧的收敛路径 |
| --- | --- |
| 账号停用、删除、移出 `APP_forgejo` | **无自动路径**：Module 不持有目录副本，固定版本也没有 IAM 主动 logout receiver（`LOGOUT-R-008`）。管理员必须在 Forgejo 停用或删除该账号 |
| 组成员变更 | 该用户下次 OIDC 登录时随 groups 声明到达 team（`FORGEJO-R-060`） |
| 改名、邮箱变更 | **同一个账号**：Forgejo 按 OIDC `sub` 认人（`sub` 存进 `login_name`），用户名取 `preferred_username` 且只在建号时写一次，因此改名后用户名冻结在旧值，仓库路径仍是 `/old/...`；邮箱同样不刷新。`sub` 若不稳定才会建出第二个账号 |

因此有两条约束必须写进目录管理流程，Module 无法代为强制：**用户名与邮箱别名不得回收再分配、改名走
正式流程**；**离职与紧急撤权必须包含"在 Forgejo 停用账号并吊销其 token 与 SSH key"这一步**。需要组
撤权即时生效的消费者（`ai_agent`）必须自己订阅目录事件日志并保留即时否决表，这一点在双源形态下同样
成立，不因本次撤回而改变。

**上面的"无自动路径"是可以解开的，入口不在 Forgejo。** Forgejo 把 OIDC `sub` 原样存进 `login_name`，
而管理端用户列表对管理员会返回 `login_name`。因此只要 IAM 把主体标识符本身取成 `anasIdentityAnchor`
（[目录身份键要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-identity-key.md)
`DIRKEY-R-008`），Forgejo 侧不需要任何新能力就持有了一个可以直接与目录对账的稳定键：列出 Forgejo 的
外部账号、与目录准入集合取差、对差集撤权，匹配是精确的，不会误伤改过名的在职者。这条路不需要 LDAP
source，也不改变账号的产生方式，因此与本节的 OIDC-only 结论并不冲突；它的落地由
[目录身份键实施计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md)
M2 跟踪，不在本 Module 的里程碑内。

仍然不做的：LDAP source、SAML source、密码回写、`anasIdentityAnchor` 的自动 reconciler。发布一个
Forgejo 不消费的 anchor claim 同样不做——上面那条路用的是 `sub` 本身，不是额外的 claim。

### 2.3 升级复核：固定版本每次变更都要重新判定

本节结论绑定在固定版本 `15.0.7` 的能力上，因此**每次变更 Forgejo 固定版本（含 patch）都必须复核以下
四点**，任何一点成立就重新评估双链路（`FORGEJO-R-066`）：

1. LDAP source 是否新增了可配置的不可变 ID 字段——例如把目录 UUID/anchor 作为外部 ID 持久化；
2. OIDC/OAuth2 source 是否能按 claim（而不是用户名或邮箱）绑定到既有账号；
3. 是否出现认证源的 REST API，或 LDAP CLI 的组同步选项——这决定 team 成员关系能否离开"登录时刻"；
4. 是否出现 IAM 主动 logout receiver（front/back-channel），或能按用户撤销会话、token 与 SSH key 的
   管理端接口。

第 1、2 点同时成立才可能恢复双链路；第 3、4 点即使双链路仍不成立也有独立价值，它们直接决定撤权能否
不依赖人工。四点都只问 **Forgejo 上游**；IAM 侧的主体标识符取 anchor 是另一条独立的路，由
`DIRKEY-R-008` 规定，不随本节复核。复核必须按[互操作基线](https://github.com/anas-project/ANAS/blob/master/docs/developer/forgejo-interop.md) §4
先跑探针再改文档——这一页里过半条目与上游文档描述不符，不能凭 changelog 下结论。结论写回本节、
`FORGEJO-R-066` 的执行记录和互操作基线 §1。

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

- Forgejo LDAP source、SAML source 与 `anasIdentityAnchor` 自动关联（§2.2、§2.3）；
- global Runner 或共享 ANAS 宿主 Docker socket；
- 在 Forgejo 容器内调用 Incus/libvirt/hypervisor API；
- 在 ANAS 核心服务宿主运行 privileged DinD；
- 不受约束的宿主目录导入或默认开启 custom Git Hooks。
