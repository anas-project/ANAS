---
doc_type: requirement
status: current
created: 2026-08-22
updated: 2026-08-22
---

# Forgejo Module 集成要求

本文规定 Forgejo 代码托管、OIDC 身份、高风险配置和 Actions 隔离执行面的交付边界。设计理由见
[Forgejo Module 设计](/architecture/forgejo-module-design)，实施进度见
[Forgejo Module 实施计划](/plans/forgejo-module)。本文的需求矩阵是验收规范来源，不记录施工进度。

## 1. 当前应用边界

Forgejo 应用 Module 提供 HTTP/SSH Git、LFS、Package、OIDC JIT 和托管本地恢复账号。身份只走
IAM/OIDC，不实现 LDAP/SAML 双链路。Actions 服务端和 Runner 属于同一个产品功能；Module 默认关闭
Actions，只能在执行面前置条件全部满足时通过唯一开关同时启用服务端和 controller，不能暴露一个
只能开启服务端的半功能开关。

Git Hooks 与 local-path import 是彼此独立的高风险能力，不属于 Actions 开关。两者默认关闭，开启
任一项都不得扩大 Compose 宿主挂载范围。

## 2. Actions 单开关模型

隔离执行面完成后，管理员只看到 `forgejo.actions_enabled` 一个功能开关。开启它必须调和 Forgejo
Actions 服务端、Runner controller 和执行面；关闭它必须阻止新任务并回收 Runner。单独交付的
Runner 包可以保留独立版本和安全边界，但不得提供 `runner.enabled`，也不得要求管理员再手工启用
`forgejo_runner` Module。

仓库或组织 scope 是计算资源授权策略，不是第二个启用开关。仓库自身的 Actions Unit 是 Forgejo
应用权限的一部分，同样不能替代 ANAS 的 repo/org Runner 授权。不得注册 global Runner。

## 3. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `FORGEJO-R-001` | Module 名为 `forgejo`、类别为 `app`、状态为 `developing`，固定 Forgejo `15.0.7` rootless 镜像与 ANAS revision，不得使用 `latest` | 静态 |
| `FORGEJO-R-002` | Module 消费 `relational_database >=1.0.0 <2.0.0`，支持 PostgreSQL/MariaDB 并默认 PostgreSQL；不得读取数据库 Provider 管理员凭据 | 单元 |
| `FORGEJO-R-003` | 数据目录、数据库 Resource、Secret Store 与部署元数据必须形成一致备份恢复点；修改数据库类型或名称不得宣称自动迁移 | 文档 |
| `FORGEJO-R-004` | Forgejo 只通过通用 IAM OIDC binding 登录，首次登录 JIT 建号，不得按 LLNG、Authentik 或其他 Provider 名称分支 | 单元 |
| `FORGEJO-R-005` | 应用过滤开启时只允许 `APP_forgejo`、`APP_all` 或管理员组进入；管理员组映射 site administrator | 单元 |
| `FORGEJO-R-006` | 当前版本不得配置 LDAP/SAML source、目录用户/组同步、密码回写、`anasIdentityAnchor` reconciler 或 LDAP/OIDC 自动账号合并 | 静态 + 文档 |
| `FORGEJO-R-007` | `break_glass` 本地账号密码由 Secret Store 管理，apply 不得把明文放入宿主 Docker argv；不能满足事务轮换时不得声明 rotate | 单元 |
| `FORGEJO-R-008` | M3 接入前配置 inventory 不得暴露 `forgejo.actions_enabled`，Hook 必须固定输出 `false`；M3 接入时必须由 R-030—R-039 的完整单开关调和替代，不能只移除固定关闭 | 单元 |
| `FORGEJO-R-010` | `custom_git_hooks_enabled` 是默认 `false` 的 bool，取反映射 `DISABLE_GIT_HOOKS`，变更触发 `container_recreate` | 单元 |
| `FORGEJO-R-011` | `local_path_import_enabled` 是默认 `false` 的 bool，直接映射 `IMPORT_LOCAL_PATHS`，变更触发 `container_recreate` | 单元 |
| `FORGEJO-R-012` | 开启 Git Hooks 或 local-path import 不得新增任意宿主挂载；Forgejo 上游环境键必须符合 ANAS Hook ABI 的大写键规则 | 单元 |
| `FORGEJO-R-020` | Core 使用中立 compute contract 管理 VM 生命周期，不得按 Forgejo 或 Incus Module 名称增加特判 | 审阅 + 单元 |
| `FORGEJO-R-021` | 首个 compute Provider 只实现独立宿主上的 Incus VM（QEMU/KVM），不得把 Incus system container、privileged DinD 或宿主 Docker socket当作等价实现 | 静态 |
| `FORGEJO-R-022` | Incus Provider 只能操作 restricted project `anas-forgejo-runners`，使用 project-scoped restricted credential 和 CPU/memory/disk/VM 数量配额 | 单元 |
| `FORGEJO-R-023` | Incus credential 只进入 compute Provider/controller，不得进入 Forgejo 应用容器、Runner job、cloud-init、镜像或普通日志 | 单元 |
| `FORGEJO-R-030` | 执行面交付后，`forgejo.actions_enabled` 必须是 ANAS 唯一的 Actions 功能开关，同时控制服务端和 Runner desired state | 契约 + 单元 |
| `FORGEJO-R-031` | 不得增加用户可设置的 `runner.enabled`、`forgejo_runner.enabled` 或第二个 Module 启用步骤；Runner 包若独立交付，只能由 `forgejo.actions_enabled` 派生调和 | 静态 + 单元 |
| `FORGEJO-R-032` | `actions_enabled=false` 时不得注册 Runner 或创建 Runner VM；`true` 时不得只开启服务端而遗漏 controller/执行面调和 | 单元 |
| `FORGEJO-R-033` | Runner 授权只接受 `{owner}` 或 `{owner}/{repo}` scope，拒绝 global scope；scope 是授权集合，不得实现为第二个 bool 开关 | 单元 |
| `FORGEJO-R-034` | 仓库 Actions Unit 只控制 Forgejo 仓库功能可见性；没有获批 repo/org scope 时不得获得 ANAS Runner 计算资源 | 单元 |
| `FORGEJO-R-035` | 每个 Job 使用一个 ephemeral Forgejo Runner 和一个 Incus one-job VM；作业完成后必须销毁 VM、root disk 与 Runner registration | 单元 |
| `FORGEJO-R-036` | Runner token 必须是一次性凭据，只经 Incus agent/stdin 写入 guest tmpfs，不得进入 cloud-init、argv、镜像、磁盘快照或日志 | 单元 |
| `FORGEJO-R-037` | Runner VM 不得挂载 ANAS workspace、Forgejo data、NAS、Secret Store 或宿主 Docker/Podman socket；不得提供 `host` label、privileged job 或任意 volume | 单元 |
| `FORGEJO-R-038` | 每个 scope 和 Incus project 必须限制 CPU、memory、PID、disk、并发数、job timeout 与 egress；默认无入站端口 | 单元 |
| `FORGEJO-R-039` | 关闭唯一 Actions 开关必须先阻止新任务，再注销 Runner 并回收空闲/排队执行面；调和失败必须保留可诊断状态并由 janitor 收敛，不能留下第二个手工开关补偿 | 单元 |
| `FORGEJO-R-040` | 每个获批 scope 最多保留一个等待任务的预热 one-job VM；预热 VM 执行首个任务后必须销毁 | 单元 |
| `FORGEJO-R-041` | 扩容只能增加相互隔离的 one-job VM，并同时受 scope quota 与 Incus project quota 限制，不得提高持久 Runner capacity | 单元 |
| `FORGEJO-R-042` | janitor 必须回收正常结束、失败、取消、启动超时和 controller crash 路径上的 VM、磁盘、registration 与 token | 单元 |
| `FORGEJO-R-043` | 默认 `prewarm=0`；Actions 已开启但授权 scope 没有 waiting job 时，连续两个调和周期后必须为零 Runner registration、零 Runner VM、零临时 root disk，不能用常驻 daemon Runner 冒充空闲状态 | 单元 + e2e |
| `FORGEJO-R-044` | 空队列基准持续 30 分钟时，controller RSS 必须不高于 128 MiB、平均 CPU 不高于单核 1%，每个 scope 的 job-queue 请求频率不得高于每 10 秒一次；测试必须同时记录 Actions 关闭时的 Forgejo 基线 | e2e |
| `FORGEJO-R-045` | 已按 job handle 启动但因并发组等原因尚未领取任务的 one-job Runner 必须受 10 分钟 waiting TTL 约束；超时后注销 registration、销毁 VM/root disk 并退避重试，不能无限轮询占用 VM | 单元 + e2e |
| `FORGEJO-R-050` | PostgreSQL/MariaDB 与 amd64/arm64 必须分别完成真实启动、Git/LFS/Package smoke、备份恢复和升级回滚验收 | e2e |
| `FORGEJO-R-051` | LLNG/Authentik 必须分别完成 OIDC 登录、Group 准入/拒绝、管理员映射、降权和 IAM-down 恢复验收 | e2e |
| `FORGEJO-R-052` | Actions E2E 必须证明一个 `actions_enabled` 操作同时改变服务端和 Runner desired state，且系统不存在第二个用户可见 Runner 开关 | e2e |
| `FORGEJO-R-053` | Actions E2E 必须证明获批 repo/org 能运行无 Secret 的容器构建，未获批仓库无 Runner，正常/失败/取消/controller crash 后不残留 VM、磁盘或 token | e2e |
| `FORGEJO-R-054` | 纯代码托管 Module 可以先于 Runner 执行面达到 `release`，但在 `FORGEJO-R-052` 与 `FORGEJO-R-053` 完成前不得把 Actions 功能标为可用或 release | 审阅 |

## 4. 明确排除

本要求不包含 LDAP/SAML 双链路、global Runner、Forgejo 容器内 Incus 权限、ANAS 核心宿主上的
privileged DinD、跨信任域持久 workspace，或让仓库管理员绕过 ANAS repo/org scope 授权。
