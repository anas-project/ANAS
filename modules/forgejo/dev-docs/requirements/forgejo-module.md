---
doc_type: requirement
status: current
created: 2026-08-22
updated: 2026-09-20
---

# Forgejo Module 集成要求

本文规定 Forgejo 代码托管、OIDC 身份、高风险配置和 Actions 隔离执行面的交付边界。设计理由见
[Forgejo Module 设计](../../../../docs/architecture/forgejo-module-design.md)，实施进度见
[Forgejo Module 实施计划](../plans/forgejo-module.md)。本文的需求矩阵是验收规范来源，不记录施工进度。

## 1. 当前应用边界

Forgejo 应用 Module 提供 HTTP/SSH Git、LFS、Package、OIDC JIT 和托管本地恢复账号。身份只走
IAM/OIDC，不实现 LDAP/SAML 双链路。Actions 服务端和 Runner 属于同一个产品功能；Module 默认关闭
Actions，只能在执行面前置条件全部满足时通过唯一开关同时启用服务端和 controller，不能暴露一个
只能开启服务端的半功能开关。

Git Hooks 与 local-path import 是彼此独立的高风险能力，不属于 Actions 开关。两者默认关闭，开启
任一项都不得扩大 Compose 宿主挂载范围。

执行面有两档隔离，见 §3；Actions 开启后还会存在一个专用的控制面账号，见 §4。

## 2. Actions 单开关模型

隔离执行面完成后，管理员只看到 `forgejo.actions_enabled` 一个功能开关。开启它必须调和 Forgejo
Actions 服务端、Runner controller 和执行面；关闭它必须阻止新任务并回收 Runner。单独交付的
Runner 包可以保留独立版本和安全边界，但不得提供 `runner.enabled`，也不得要求管理员再手工启用
`forgejo_runner` Module。

仓库或组织 scope 是计算资源授权策略，不是第二个启用开关。仓库自身的 Actions Unit 是 Forgejo
应用权限的一部分，同样不能替代 ANAS 的 repo/org Runner 授权。不得注册 global Runner。

## 3. 执行实例隔离档

执行面由 compute contract 提供两档隔离，管理员用 `forgejo.actions_isolation` 选择：
`incus_container` 是非特权 Incus 系统容器，**与宿主共享内核**；`incus_vm` 是 QEMU/KVM 虚拟机，有
独立 guest kernel。默认档是 `incus_container`——ANAS 的目标硬件（NAS 与小型主机）不保证具备 KVM，
把需要 KVM 的档位设为默认会让 Actions 在这些机器上根本装不上。`incus_vm` 是管理员显式选择的升级，
宿主缺少 KVM 时不得自动降级，也不得自动升级。

两档共享全部与内核边界无关的约束：restricted project、配额、一次性注册与一次性实例、固定镜像
fingerprint、无宿主挂载与 socket、出站白名单。差别只在内核边界，因此跨信任域的写入者、或会执行
不受信输入（例如公开仓库的 PR）的 scope 必须选 `incus_vm`；`incus_container` 的隔离承诺以宿主内核
与非特权 idmap 为前提。这条差别必须对管理员可见，不能只写在设计文档里。

## 4. Actions 控制面账号

controller 以 Forgejo 账号身份调用 Actions Runner API，因此 Actions 开启后存在第二个托管本地账号，
与 `break_glass` 分属不同用途：`break_glass` 是给人用的恢复入口，控制面账号是给程序用的、不用于
交互登录的凭据。它的存在、权限范围，以及关闭 Actions 之后的处置都必须成文。

controller 实际只调用获批 scope 下的 `actions/runners` 列表、创建与删除端点。当前实现把该账号建成
站点管理员，原因是没有按 scope 授予组织 owner / 仓库 admin 的调和路径，而不是调用集合需要全站权限。
这是一处已登记的权限偏差，不是设计意图。

## 5. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `FORGEJO-R-001` | Module 名为 `forgejo`、类别为 `app`、状态为 `developing`，固定 Forgejo `15.0.7` rootless 镜像与 ANAS revision，不得使用 `latest` | 静态 |
| `FORGEJO-R-002` | Module 消费 `relational_database >=1.0.0 <2.0.0`，支持 PostgreSQL/MariaDB 并默认 PostgreSQL；不得读取数据库 Provider 管理员凭据 | 单元 |
| `FORGEJO-R-003` | 数据目录、数据库 Resource、Secret Store 与部署元数据必须形成一致备份恢复点，且文档必须写明哪些运行时状态（如 controller state volume）不在这个一致点内；修改数据库类型或名称不得宣称自动迁移 | 文档 |
| `FORGEJO-R-004` | Forgejo 只通过通用 IAM OIDC binding 登录，首次登录 JIT 建号，不得按 LLNG、Authentik 或其他 Provider 名称分支 | 单元 |
| `FORGEJO-R-005` | 应用过滤开启时只允许 `APP_forgejo`、`APP_all` 或管理员组进入；管理员组映射 site administrator | 单元 |
| `FORGEJO-R-006` | 当前固定版本不得配置 LDAP/SAML source、目录用户或组同步，也不得启用 LDAP/OIDC 自动账号合并（`ACCOUNT_LINKING` 固定 `disabled`）；不得写回密码，不得实现 `anasIdentityAnchor` reconciler，也不得发布 Forgejo 不消费的 anchor claim。账号只由 OIDC JIT 产生 | 静态 + 单元 |
| `FORGEJO-R-007` | `break_glass` 本地账号密码由 Secret Store 管理，apply 不得把明文放入宿主 Docker argv；不能满足事务轮换时不得声明 rotate | 单元 |
| `FORGEJO-R-008` | **已废弃**（2026-09-20，M3 的单开关已接入）：原要求"M3 接入前配置 inventory 不得暴露 `forgejo.actions_enabled`，Hook 必须固定输出 `false`"。过渡已经发生，剩余的"不得只开启服务端"约束由 `FORGEJO-R-030`—`R-032` 承担 | —— |
| `FORGEJO-R-010` | `custom_git_hooks_enabled` 是默认 `false` 的 bool，取反映射 `DISABLE_GIT_HOOKS`，变更触发 `container_recreate` | 单元 |
| `FORGEJO-R-011` | `local_path_import_enabled` 是默认 `false` 的 bool，直接映射 `IMPORT_LOCAL_PATHS`，变更触发 `container_recreate` | 单元 |
| `FORGEJO-R-012` | 开启 Git Hooks 或 local-path import 不得新增任意宿主挂载；Forgejo 上游环境键必须符合 ANAS Hook ABI 的大写键规则 | 单元 |
| `FORGEJO-R-020` | Core 使用中立 compute contract 管理 VM 生命周期，不得按 Forgejo 或 Incus Module 名称增加特判 | 审阅 + 单元 |
| `FORGEJO-R-021` | Runner 执行实例只由独立宿主上的 Incus 隔离实例提供，档位限于 `incus_container`（非特权系统容器）与 `incus_vm`（QEMU/KVM）；不得把 privileged DinD、宿主 Docker socket 或 Forgejo 容器内的 hypervisor 权限当作任何一档的等价实现 | 静态 |
| `FORGEJO-R-022` | Incus Provider 只能操作 restricted project `anas-forgejo-runners`，使用 project-scoped restricted credential 和 CPU/memory/disk/实例数量配额 | 单元 |
| `FORGEJO-R-023` | Incus credential 只进入 compute Provider/controller，不得进入 Forgejo 应用容器、Runner job、cloud-init、镜像或普通日志 | 单元 |
| `FORGEJO-R-024` | `forgejo.actions_isolation` 是 `auto`、`incus_container`、`incus_vm` 三值枚举，`auto` 必须解析为 `incus_container`；不得因宿主缺少 KVM 自动降级，也不得自动升级为 `incus_vm` | 单元 |
| `FORGEJO-R-025` | 中英 README 与技术文档必须写明默认档是与宿主共享内核的系统容器、两档的隔离边界差异，以及跨信任域或执行不受信输入的 scope 必须选 `incus_vm` | 审阅 |
| `FORGEJO-R-026` | 固定 Runner image fingerprint 必须与所选隔离档和 compute Provider 的目标架构一致，并在开启 Actions 时校验；不一致必须在创建实例前失败 | 单元 |
| `FORGEJO-R-030` | 执行面交付后，`forgejo.actions_enabled` 必须是 ANAS 唯一的 Actions 功能开关，同时控制服务端和 Runner desired state | 契约 + 单元 |
| `FORGEJO-R-031` | 不得增加用户可设置的 `runner.enabled`、`forgejo_runner.enabled` 或第二个 Module 启用步骤；Runner 包若独立交付，只能由 `forgejo.actions_enabled` 派生调和 | 静态 + 单元 |
| `FORGEJO-R-032` | `actions_enabled=false` 时不得注册 Runner 或创建 Runner 执行实例；`true` 时不得只开启服务端而遗漏 controller/执行面调和 | 单元 |
| `FORGEJO-R-033` | Runner 授权只接受 `{owner}` 或 `{owner}/{repo}` scope，拒绝 global scope；scope 是授权集合，不得实现为第二个 bool 开关 | 单元 |
| `FORGEJO-R-034` | 仓库 Actions Unit 只控制 Forgejo 仓库功能可见性；没有获批 repo/org scope 时不得获得 ANAS Runner 计算资源 | 单元 |
| `FORGEJO-R-035` | 每个 Job 使用一个 ephemeral Forgejo Runner 和一个 Incus one-job 执行实例；作业完成后必须销毁执行实例、root disk 与 Runner registration | 单元 |
| `FORGEJO-R-036` | Runner token 必须是一次性凭据，只经 Incus agent/stdin 写入 guest tmpfs，不得进入 cloud-init、argv、镜像、磁盘快照或日志 | 单元 |
| `FORGEJO-R-037` | Runner 执行实例不得挂载 ANAS workspace、Forgejo data、NAS、Secret Store 或宿主 Docker/Podman socket；不得提供 `host` label、privileged job 或任意 volume | 单元 |
| `FORGEJO-R-038` | 每个 scope 和 Incus project 必须限制 CPU、memory、PID、disk、并发数、job timeout 与 egress；默认无入站端口 | 单元 |
| `FORGEJO-R-039` | 关闭唯一 Actions 开关必须先阻止新任务，再注销 Runner 并回收空闲/排队执行面；调和失败必须保留可诊断状态并由 janitor 收敛，不能留下第二个手工开关补偿 | 单元 |
| `FORGEJO-R-040` | 每个获批 scope 最多保留一个等待任务的预热 one-job 执行实例；预热实例执行首个任务后必须销毁 | 单元 |
| `FORGEJO-R-041` | 扩容只能增加相互隔离的 one-job 执行实例，并同时受 scope quota 与 Incus project quota 限制，不得提高持久 Runner capacity | 单元 |
| `FORGEJO-R-042` | janitor 必须回收正常结束、失败、取消、启动超时和 controller crash 路径上的执行实例、磁盘、registration 与 token | 单元 |
| `FORGEJO-R-043` | 默认 `prewarm=0`；Actions 已开启但授权 scope 没有 waiting job 时，连续两个调和周期后必须为零 Runner registration、零 Runner 执行实例、零临时 root disk，不能用常驻 daemon Runner 冒充空闲状态 | 单元 + e2e |
| `FORGEJO-R-044` | 空队列基准持续 30 分钟时，controller RSS 必须不高于 128 MiB、平均 CPU 不高于单核 1%，每个 scope 的 job-queue 请求频率不得高于每 10 秒一次；测试必须同时记录 Actions 关闭时的 Forgejo 基线 | e2e |
| `FORGEJO-R-045` | 已按 job handle 启动但因并发组等原因尚未领取任务的 one-job Runner 必须受 10 分钟 waiting TTL 约束；超时后注销 registration、销毁执行实例/root disk 并退避重试，不能无限轮询占用 VM | 单元 + e2e |
| `FORGEJO-R-046` | controller state volume 丢失后仍必须能回收孤立执行实例与孤立 Runner registration；回收不得只依赖 state 中记录的 identity，也不得只发生在关闭开关时 | 单元 |
| `FORGEJO-R-050` | PostgreSQL/MariaDB 与 amd64/arm64 必须分别完成真实启动、Git/LFS/Package smoke、备份恢复和升级回滚验收 | e2e |
| `FORGEJO-R-051` | LLNG/Authentik 必须分别完成 OIDC 登录、Group 准入/拒绝、管理员映射、降权和 IAM-down 恢复验收 | e2e |
| `FORGEJO-R-052` | Actions E2E 必须证明一个 `actions_enabled` 操作同时改变服务端和 Runner desired state，且系统不存在第二个用户可见 Runner 开关 | e2e |
| `FORGEJO-R-053` | Actions E2E 必须证明获批 repo/org 能运行无 Secret 的容器构建，未获批仓库无 Runner，正常/失败/取消/controller crash 后不残留执行实例、磁盘或 token | e2e |
| `FORGEJO-R-054` | 纯代码托管 Module 可以先于 Runner 执行面达到 `release`，但在 `FORGEJO-R-052` 与 `FORGEJO-R-053` 完成前不得把 Actions 功能标为可用或 release | 审阅 |
| `FORGEJO-R-060` | OIDC auth source 必须支持把 IAM group claim 声明式映射到 Forgejo 组织 team（含登录时的自动移除），映射内容由消费方提供；Forgejo Module 不得硬编码具体组名 | 单元 + e2e |
| `FORGEJO-R-061` | Forgejo Module 的 reconcile 不得删除或改写由管理端 API 创建的自动化账号、token 与 SSH key；这些对象的生命周期归属其创建方 | 单元 + 审阅 |
| `FORGEJO-R-062` | Forgejo Module 不注册业务用系统 webhook，也不得在 reconcile 中清理不属于自己的 hook；管理凭据的发放与审计边界必须在文档中说明 | 单元 + 审阅 |
| `FORGEJO-R-063` | **已废弃**（2026-09-20，双源形态撤回，身份收敛为 OIDC-only）：原要求"开启目录同步时必须配置只读 LDAP source 同步用户"。LDAP source 的禁令回归 `FORGEJO-R-006` | —— |
| `FORGEJO-R-064` | **已废弃**（2026-09-20，双源形态撤回，身份收敛为 OIDC-only）：原要求"开启目录同步时 OIDC 登录必须绑定到 LDAP 同步出的既有账号"。`account_linking` 配置已移除，`ACCOUNT_LINKING` 固定 `disabled`，见 `FORGEJO-R-006` | —— |
| `FORGEJO-R-065` | **已废弃**（2026-09-20，双源形态撤回，身份收敛为 OIDC-only）：原要求"开启目录同步时必须订阅 Samba 目录事件日志"。Module 不再持有目录副本，不在 `DIRSYNC-R-002` 适用范围；撤权兜底路径见 `FORGEJO-R-006` 与设计 §2.2 | —— |
| `FORGEJO-R-066` | 每次变更 Forgejo 固定版本（含 patch）必须按[设计](../../../../docs/architecture/forgejo-module-design.md) §2.3 复核四点：LDAP source 是否有可配置的不可变 ID 字段、OIDC source 能否按 claim 绑定既有账号、是否出现认证源 REST API 或 LDAP CLI 组同步选项、是否出现 IAM 主动 logout receiver 或按用户撤销会话/token 的管理端接口。复核必须先跑互操作探针再改结论，结果写回设计 §2.2 与互操作基线；在复核结论推翻 `FORGEJO-R-006` 之前不得恢复双链路 | 审阅 |
| `FORGEJO-R-067` | Actions controller 必须使用与 `break_glass` 分离的专用 Forgejo 账号，口令由 Secret Store 管理；apply 不得把明文放入宿主 Docker argv | 单元 |
| `FORGEJO-R-068` | controller 只能调用获批 scope 下的 `actions/runners` 端点，不得调用 `/api/v1/admin/*` 或其他不受 scope 限定的端点 | 单元 |
| `FORGEJO-R-069` | controller 账号当前持有站点管理员权限是已登记偏差：中英 README 与技术文档必须写明该账号的用户名、创建时机、权限范围与撤销方式，并在收敛到按 scope 授权之前保持登记 | 审阅 |
| `FORGEJO-R-070` | 关闭 `forgejo.actions_enabled` 之后不得留下可用的 controller 账号凭据；调和必须停用该账号或使其口令失效 | 单元 |

## 6. 明确排除

本要求不包含 LDAP/SAML 双链路、global Runner、Forgejo 容器内 Incus 权限、ANAS 核心宿主上的
privileged DinD、跨信任域持久 workspace，或让仓库管理员绕过 ANAS repo/org scope 授权。
