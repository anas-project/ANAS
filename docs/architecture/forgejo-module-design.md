# Forgejo Module 设计

> 状态：**当前模型与明确标注的未实现部分**。Forgejo 应用、controller、Incus adapter 和 guest image
> 资产已实现；独立 Incus 宿主上的真实隔离与 one-job E2E 尚未完成，不能视为 release 能力。
> 更新：2026-09-20。

本文记录 Forgejo Module 的身份、Actions 授权、Runner 隔离和高风险功能开关设计。当前实现事实以
[`modules/forgejo/module.yml`](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/module.yml)和
[技术实现](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/docs/technical.md)为准。

## 1. 边界与目标

Forgejo Module 负责代码托管服务端、数据库、持久数据、HTTP/SSH 入口、OIDC 登录和本地恢复管理员。
Actions Runner 是远程代码执行面，必须作为独立服务组件、在独立宿主上的一次性隔离实例里运行，不进入
Forgejo 应用容器；controller 可以作为同一 Module 的隔离 Compose service 由唯一开关调和，但不能获得
ANAS 宿主 Docker socket、数据库或目录管理凭据。controller 需要一个 Forgejo 账号凭据来调用 Actions
Runner API，这个账号的边界见 §3.1。

设计目标是：

- Forgejo 服务端可以开启 Actions，但 ANAS 计算资源只分配给明确批准的仓库或组织；
- 作业攻陷只影响一个执行实例和一个信任域；隔离强度取决于管理员选择的档位，见 §4；
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
选项，产品也没有认证源 REST API**（2026-09-20 在固定镜像上复跑 `admin auth add-ldap --help` 实测确认：
匹配 `group` 的行数为 0，只有 `--synchronize-users`；报告在 `test-env/reports/forgejo-cli-probe-20260920.md`（报告目录不入库）），组只能继续经 OIDC 声明在登录时
映射 team。也就是说双源形态换不到
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
| 组成员变更 | 该用户下次 OIDC 登录时随 groups 声明到达 team（`FORGEJO-R-060`）。依赖的 `--group-team-map` / `--group-team-map-removal` 已于 2026-09-20 在固定镜像上实测存在 |
| 改名、邮箱变更 | **同一个账号**：Forgejo 按 OIDC `sub` 认人（`sub` 存进 `login_name`），用户名取 `preferred_username` 且只在建号时写一次，因此改名后用户名冻结在旧值，仓库路径仍是 `/old/...`；邮箱同样不刷新。`sub` 若不稳定才会建出第二个账号。**`推断`**，待 `FJPROBE-T-003`/`T-004` 复核 |

因此有两条约束必须写进目录管理流程，Module 无法代为强制：**用户名与邮箱别名不得回收再分配、改名走
正式流程**；**离职与紧急撤权必须包含"在 Forgejo 停用账号并吊销其 token 与 SSH key"这一步**。需要组
撤权即时生效的消费者（`ai_agent`）必须自己订阅目录事件日志并保留即时否决表，这一点在双源形态下同样
成立，不因本次撤回而改变。

**上面的"无自动路径"是可以解开的，入口不在 Forgejo。** 以下这条路建立在一个尚未复核的前提上：
Forgejo 把 OIDC `sub` 原样存进 `login_name`，而管理端用户列表对管理员会返回 `login_name`。这个前提
由 [`FJPROBE-T-003`](https://github.com/anas-project/ANAS/blob/master/modules/forgejo/dev-docs/plans/forgejo-module.md)
验证；在它给出结论之前，下面这段是**设计意图，不是已证实的能力**。因此只要 IAM 把主体标识符本身取成 `anasIdentityAnchor`
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

**当前固定版本 `15.0.7+gitea-1.22.0` 的复核状态**（2026-09-20 CLI 实测，报告在 `test-env/reports/forgejo-cli-probe-20260920.md`（报告目录不入库））：

| 点 | 结论 | 证据 |
| --- | --- | --- |
| 1. LDAP source 的不可变 ID 字段 | **不成立** | `add-ldap` 的 attribute 选项只有 username/firstname/surname/email/ssh-key/avatar |
| 2. OIDC 按 claim 绑定既有账号 | **不成立** | `add-oauth` 无此入口；`--required-claim-*` 只是准入过滤 |
| 3a. LDAP CLI 组同步选项 | **不成立** | `add-ldap --help` 里匹配 `group` 的行数为 0 |
| 3b. 认证源 REST API | **未复核** | 需要管理员 token，本次只跑了 CLI |
| 4. IAM logout receiver / 按用户撤销会话 token 的管理端接口 | **未复核** | 同上；已知 `admin user` 没有 `prohibit_login` 子命令 |

第 1、2 点仍不成立，因此**不恢复双链路**。3b 与 4 是下一次复核要补的两项。

## 3. Actions 授权模型

Actions server 与 Runner 是同一个产品功能。管理员只应看到一个 ANAS 功能开关：
`forgejo.actions_enabled`。它已接入配置 inventory，同一个值同时投影到 Forgejo 服务端、
Runner controller 与 Runner desired state。默认值为 `false`。

开启的前置条件分两处把关，不要把它们混为一谈：**Hook 在渲染时**校验 Forgejo 自己拥有的东西——
获批 scope 的形状、控制面账号口令、固定 image fingerprint；**一次性 preflight service 在 Forgejo
启动前**真正连接 Incus，验证 project、quota 与 profile。Incus endpoint 与客户端证书已经不是 Forgejo
的配置项，它们由 compute contract 供给，因此不可能在 Hook 阶段校验。任一处失败都不会启动 Forgejo，
所以形不成"只开启了服务端"的半功能状态。

Runner 可以保持独立镜像、版本、发布节奏和权限边界，但它是由上述 desired state 自动派生的内部
执行组件，不得再提供 `runner.enabled`、`forgejo_runner.enabled` 或要求管理员手工启用第二个 Module。

唯一开关之外还有两层**授权**，它们不是功能开关：

1. 仓库管理员在 Forgejo 仓库 Units 中决定仓库是否使用 Actions；
2. ANAS 实例管理员批准 `{owner}/{repo}` 或 `{owner}` Runner scope，controller 只为批准 scope 注册
   Runner。

不部署 global runner。仓库即使打开 Actions，没有匹配的 repo/org Runner 也不能使用 ANAS 计算资源。
能修改 `.forgejo/workflows` 的写入者等价于能在对应 Runner 上执行代码，因此一个 repo/org scope
只能覆盖写入者属于同一信任域的仓库；每个执行实例仍只执行其中一个作业。

状态调和遵循一个入口：开启时先验证 compute Provider、scope policy 和 credential prerequisites，再
启用服务端与执行面；关闭时先阻止新任务，再注销 Runner 并回收空闲/排队的执行实例。失败状态由同一
controller 和 janitor 收敛，不能要求操作者切换第二个开关补偿。

当前实现采用常驻轻量 controller + 按需执行实例：controller 开启时默认每 15 秒查询获批 scope，
空队列不会注册 Runner 或创建实例；关闭时执行一次 cleanup 后退出。controller 本身的空闲 RSS/CPU
指标仍须按 `FORGEJO-R-044` 在真实环境测量，不能用“没有 Runner 实例”替代 controller 资源基准。

### 3.1 控制面账号

controller 用 Forgejo 账号的 basic auth 调用三个端点，全部限定在获批 scope 内：列出 waiting job、
创建 ephemeral runner registration、删除 registration。Actions 开启时 `after_start` 会调和一个固定
账号 `anas_actions_controller`，口令来自 Secret Store 并经 stdin 传入，不进入宿主 `docker` argv。
它与 `break_glass` 是两件东西：`break_glass` 是给人用的恢复入口，这个账号是给程序用的凭据。

**当前它被建成站点管理员，这是一处已登记的偏差，不是设计意图。** 上面三个端点要求的是组织 owner
或仓库 admin 权限，不是全站权限；把账号建成站点管理员，只是因为现在没有"按获批 scope 授予组织
owner / 仓库 admin"的调和路径。后果要写明：controller 被攻陷等于 Forgejo 全站管理员，这超出了
§1 "作业攻陷只影响一个执行实例和一个信任域"的目标范围——那条目标约束的是**作业**，不是控制面。
收敛方向是按 scope 授权（`FORGEJO-R-068`）；在收敛之前，这条偏差必须留在中英文文档里
（`FORGEJO-R-069`）。

关闭唯一开关时，除了阻止新任务与回收执行面，还必须让这个账号的凭据失效（`FORGEJO-R-070`）：
否则关掉 Actions 之后，一个站点管理员账号和它在 Secret Store 里的有效口令会继续留在部署里。

## 4. 执行实例隔离：Incus 的两档

Runner 执行实例统一选择 **Incus**，不并列支持 libvirt、Proxmox 或 Firecracker。选择 Incus 的原因是
它用同一套 REST API、模板、项目隔离、资源上限、restricted client certificate、cloud-init 和 instance
agent 同时覆盖系统容器与虚拟机两种实例——两档共用一套控制面，消费者只换一个 interface。

官方依据：Incus [实例类型](https://linuxcontainers.org/incus/docs/main/explanation/instances/)、
[Project 限制与配额](https://linuxcontainers.org/incus/docs/main/reference/projects/)和
[受限 TLS client](https://linuxcontainers.org/incus/docs/main/howto/projects_confine/)。

### 4.1 两档的选择与默认值

| 档位 | 实例 | 内核边界 | 宿主要求 |
| --- | --- | --- | --- |
| `incus_container`（默认） | 非特权 Incus 系统容器（LXC） | **与宿主共享内核**，project 强制 `unprivileged` | 不需要 KVM |
| `incus_vm` | QEMU/KVM 虚拟机 | 独立 guest kernel | 需要 KVM |

管理员用 `forgejo.actions_isolation` 选择，`auto` 解析为 `incus_container`。

**默认档为什么是共享内核的那一档。** ANAS 面向 NAS 与小型主机，这类硬件不保证提供 KVM。把需要
KVM 的档位设成默认，等于让 Actions 在目标硬件的相当一部分上根本装不上——这不是一个更安全的默认，
而是一个装不上的默认。因此 `incus_vm` 是管理员**显式**选择的升级：宿主缺少 KVM 时不自动降级
（应当报错），宿主具备 KVM 时也不自动升级（`INCUS-R-052`、`FORGEJO-R-024`）。隔离档是一次看得见
的决定，不是一次静默的猜测。

**两档共享的约束与内核无关，因此一条都不放松**：restricted project 与配额、一次性 registration 与
一次性实例、固定 image fingerprint、无宿主挂载与 socket、无 `host` label、无 privileged、
`valid_volumes` 为空、出站白名单、作业后销毁。§4.2 与 §4.3 的全部内容对两档同时成立。

**差别只在内核边界，而这个差别是真实的。** `incus_container` 的隔离承诺以宿主内核和非特权 idmap
为前提：一个内核提权漏洞在容器档里会打到宿主，在 VM 档里先要打穿 guest kernel 与 hypervisor。
由此推出一条硬规则：**跨信任域的写入者，或会执行不受信输入的 scope（典型是接受外部贡献者 PR 的
公开仓库），必须选 `incus_vm`**。§3 里"一个 scope 只能覆盖写入者属于同一信任域的仓库"在容器档下
不再是建议，而是前提。

这条差别必须对管理员可见（`FORGEJO-R-025`）：只写在本文里不算数，中英 README 与技术文档都要说明
默认档与宿主共享内核。

### 4.2 Incus 控制面

- Incus daemon 安装在独立宿主，不运行在 Forgejo 容器中；选 `incus_vm` 时该宿主必须具备 KVM；
- 创建 restricted project `anas-forgejo-runners`，隔离 image、profile、network 和 storage volume；
- 设置实例数量、CPU、memory 和 disk 总上限；VM 档禁止 low-level QEMU options、设备直通和嵌套
  虚拟化，容器档由 project 强制非特权，两档都不接受调用方传入的 raw config、device 或 mount；
- Runner controller 使用只绑定该 project 的 restricted TLS certificate，不获得 Incus 全局管理权限；
- cloud-init 只创建用户、安装固定包和写入非敏感配置；Forgejo runner token 不进入 image 或
  cloud-init metadata，而是在实例启动后经 Incus agent/stdin 写入 tmpfs。

Forgejo 应用容器只提供 Actions API，不持有 Incus endpoint、client certificate 或实例创建权限。

固定 image fingerprint 是按档位和架构分别签收的：`runner-image/` 要求按
amd64/arm64 × `incus_container`/`incus_vm` 各出一份，因此所选档位、compute Provider 的目标架构与
配置里的 fingerprint 三者必须一致，并在开启 Actions 时校验（`FORGEJO-R-026`），不能等到创建实例
失败才发现。

### 4.3 单作业执行实例

执行实例从第一版起就是单作业的，不交付跨作业复用的持久 Runner：

1. Controller 为批准的 repo/org scope 创建 Forgejo ephemeral runner；
2. 从只读模板创建 Incus 实例，把一次性 runner token 经 Incus agent/stdin 写入 tmpfs；
3. `runner-agent` 用户执行 `forgejo-runner one-job`，`runner-engine` 用户提供 rootless Podman；
4. Runner 不提供 `host` label，保持 `privileged=false`、`valid_volumes` 为空；
5. 作业结束后验证 Forgejo 已注销 runner token，然后销毁实例、root disk 和临时 Secret；
6. janitor 按 runner UUID 与 Incus instance ID 回收启动失败、取消、超时和 controller crash 残留。

controller 的持久状态只记 handle、scope、registration/实例 identity 和时间，不含 token，因此它不在
备份一致点内。但它**不是可丢弃的**：当前 `ListManaged` 只在关闭开关时兜底，孤立的 Forgejo runner
registration 更是没有列举路径。把"state 丢失后仍能收敛"做成真的，是 `FORGEJO-R-046`；在那之前，
重建部署时要把这个 volume 当作需要保留的东西对待。

默认不预热。Controller 轮询获批 scope 的 Forgejo v15 `actions/runners/jobs` API，只对 `waiting`
job 创建 ephemeral registration 和实例，并把 API 返回的 job `handle` 交给
`forgejo-runner one-job --handle ... --wait`，避免多个 Runner 竞争错领任务。`waiting` 仍可能表示作业
被 concurrency group 暂时阻塞，因此已启动但尚未领取任务的实例由 Controller 施加 10 分钟 TTL；
Forgejo Runner 本身没有等待超时，不能依赖它自行退出。

未来如管理员显式启用预热，每个已启用 scope 最多保留一个**尚未启动 Runner 进程**的预热实例；默认
`prewarm=0`。预热实例收到具体 job handle 后才创建 registration、注入 token 并启动 Runner，执行
一次后销毁。扩容只能增加独立 one-job 实例数量，不能提高同一 Runner 的 capacity。

执行实例不挂载 NAS、ANAS workspace、Forgejo data、宿主 Docker/Podman socket 或 Secret Store；默认
无入站端口，出站只允许 Forgejo HTTPS、DNS/NTP 和明确批准的 registry/package mirror。需要严格
Docker Engine 兼容时，可以在同一实例边界内把 rootless Podman 换成 rootless Docker。privileged DinD
两档都不接受，也不得运行在 ANAS 核心服务宿主。

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
- 按宿主能力自动选择隔离档（既不自动降级也不自动升级，§4.1）；
- 把 `incus_container` 档当作 `incus_vm` 的等价隔离向管理员呈现（§4.1）；
- 不受约束的宿主目录导入或默认开启 custom Git Hooks。
