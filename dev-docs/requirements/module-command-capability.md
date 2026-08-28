---
doc_type: requirement
status: current
created: 2026-08-23
updated: 2026-08-23
---

# Module 专属命令能力要求

本文规定 Module 发布管理员可调用命令时的声明、发现、执行、安全和适配器边界。现状分析与设计理由见
[Module 专属命令能力设计](../../docs/architecture/module-command-capability-design.md)，实施顺序见
[Module 专属命令能力实施计划](../plans/module-command-capability.md)。本文的需求矩阵是验收规范来源。

## 1. 产品边界

Module Command 是可选的管理面能力。普通 Module 无需声明任何命令；Core 的通用 deployment/Module
生命周期、Module 间 Capability/Contract 和自动 lifecycle hook 保持原有职责。管理员只能按 Module
和稳定命令 ID 调用已声明能力，不能通过该入口提交 shell、可执行文件、argv、环境变量名或宿主路径。

CLI 与 HTTP 必须调用同一个 application use case。CLI 可以同步等待；`anasd` 中的变更和长任务必须
进入既有 job、认证、角色与审计边界，不能通过执行 `anas --json` 子进程实现。

## 2. 声明与执行模型

命令定义、executor 路径和摘要随 deployment 冻结。发现和执行只读取活动 deployment，不读取当前
源码树或可变 Module cache。参数是扁平、非敏感、类型化对象；Secret 只能由 Core 按 manifest 白名单
从活动 deployment 的 Secret Store 投影。

executor 通过独立 `anas.module-command/v1` JSON Lines ABI 接收请求、报告进度并返回一个终态。命令
声明不授予 root、sudo、Docker socket、Linux capability 或 systemd 权限；高权限宿主操作只能经 Core
管理的 named helper，远程宿主权限必须使用独立的最小权限维护凭据。

## 3. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `MCMD-R-001` | Module 未声明命令时，加载、打包、render、start/stop 和 CLI 行为必须保持不变，发现结果为空 | 单元 |
| `MCMD-R-002` | 声明命令的 Module 必须同时声明 `anas.module-command/v1`；未声明该 ABI、未知 ABI 或旧 Core 无法理解新字段时必须 fail closed | 单元 |
| `MCMD-R-003` | 每个命令必须有 Module 内唯一且符合 `^[a-z][a-z0-9-]{0,62}$` 的稳定 ID，以及非空、可公开的 title 和 description | 单元 |
| `MCMD-R-004` | 命令必须声明固定 handler、`query|change` mode、`normal|destructive` risk、运行态、锁、1—3600 秒 timeout 和取消策略；未知枚举必须拒绝 | 单元 |
| `MCMD-R-005` | 一个 Module v1 只能声明一个包内固定 executor；路径必须是安全相对路径且正式包按目标平台预编译，不能依赖 shell、`go run` 或宿主工具链 | 单元 + CI |
| `MCMD-R-006` | 命令参数名必须唯一并符合 `^[a-z][a-z0-9_]{0,62}$`；类型与约束复用 `configschema.Parameter` 的 string/int/bool/enum 语义 | 单元 |
| `MCMD-R-007` | 参数默认值必须在 manifest 加载时规范化并验证；必填参数不得同时声明默认值，调用时必须拒绝重复、未知、缺失和非法参数 | 单元 |
| `MCMD-R-008` | v1 调用参数必须是非敏感扁平标量，不能接受自由 JSON、文件上传、宿主路径、环境变量展开或 argv/shell 片段 | 单元 |
| `MCMD-R-009` | 命令 env/secret 输入必须是 manifest 静态白名单并通过 Module 作用域/所有权校验；调用方不能选择键名，值不能进入发现 DTO | 单元 |
| `MCMD-R-010` | 完整 command descriptor、executor 相对路径和摘要必须冻结进 deployment manifest，并受 artifact 完整性校验；发现和执行不得读取当前 Module 源码或 cache | 单元 |
| `MCMD-R-011` | 命令发现必须返回活动 deployment、Module release、公开 descriptor、command digest、available 和稳定 unavailable reason | 单元 + 契约 |
| `MCMD-R-012` | 发现只做本地只读前置检查，不连接远程服务；远程健康必须由显式 query 命令报告 | 单元 |
| `MCMD-R-013` | HTTP/普通 CLI 发现不得公开 handler、executor 路径、env/secret key、workspace 路径或任何注入值 | 单元 + 契约 |
| `MCMD-R-014` | CLI 必须提供 `anas module commands [MODULE]`，按稳定顺序列出活动 deployment 的有效命令，并保持 `anas.dev/cli/v1` JSON 信封 | 契约 |
| `MCMD-R-015` | CLI 必须提供 `anas module invoke MODULE COMMAND --param NAME=VALUE`，并在启动 executor 前使用共享 schema 完成规范化和校验 | 契约 + 单元 |
| `MCMD-R-016` | destructive CLI 命令在 TTY 必须展示命令说明、deployment/release 和参数并确认；非 TTY 必须 `-y`，否则退出 confirmation code | 契约 |
| `MCMD-R-017` | CLI JSON stdout 必须只有一个最终信封；进度走 stderr JSONL，executor 内部协议和原始 stderr不得直接透传 | 契约 + 单元 |
| `MCMD-R-018` | CLI 与 HTTP 必须调用同一个 `application.ModuleCommandService`，不得各自实现发现、参数校验、锁或 executor 调用 | 审阅 + 单元 |
| `MCMD-R-019` | Core 启动 executor 时 argv 只能来自冻结 descriptor，请求只能经 stdin；进程环境必须从最小白名单构造，不能继承 anasd session、代理或 Docker 环境 | 单元 |
| `MCMD-R-020` | executor stdout 必须是有界严格 JSON Lines：零到多个 progress/warning 后恰好一个 result；未知 record、尾随数据、超限或缺失终态必须失败 | 单元 |
| `MCMD-R-021` | executor 必须使用 `exec.CommandContext` 并受 timeout、context 和进程组终止控制；取消不得把未知外部状态伪装成成功 | 单元 |
| `MCMD-R-022` | query 只能声明只读锁；change 必须使用 module_write 或 workspace_write；workspace_write 与 apply/rollback/start/stop/config write 互斥 | 单元 |
| `MCMD-R-023` | 重复执行已收敛命令必须能返回 `changed:false`；anasd idempotency key 相同且请求相同必须返回同一 job，请求不同必须冲突 | 单元 + 契约 |
| `MCMD-R-024` | `anasd` 必须提供 Module Command list/detail/invoke API；DTO 使用独立 HTTP API schema，不能原样复用 CLI 信封 | 契约 |
| `MCMD-R-025` | anasd change 或可能超过同步期限的命令必须返回 `202`、Location 和 job；job 保存脱敏参数、actor、目标 release/digest、事件和终态，不保存注入值 | 契约 + 单元 |
| `MCMD-R-026` | HTTP invoke 必须校验 command digest；descriptor 已变化时返回 `412`，destructive 命令还必须经过授权、重新认证和显式确认 | 契约 |
| `MCMD-R-027` | 当前未认证只读 anasd M0 不得开放 Module Command invoke；HTTP 写入口只能在认证、角色、job 和审计边界完成后启用 | 审阅 + 契约 |
| `MCMD-R-028` | Module Command 声明不得自动授予 root、sudo、Linux capability、Docker socket 或 systemd 权限；本机高权限动作只能调用固定动词和固定目标的 Core named helper | 单元 + 审阅 |
| `MCMD-R-029` | Secret、原始 stdin、原始 stderr、配置摘要和敏感结果不得进入普通结果、错误、进度、job、审计或日志 | 单元 |
| `MCMD-R-030` | Forgejo 的 restricted project TLS credential 只能用于 runner project API，不得复用为 incusd 宿主 service-manager credential | 单元 + 审阅 |
| `MCMD-R-031` | Forgejo 首批命令必须分离 project 诊断/对账与 daemon status/start/stop；不得发布 `incus exec`、`systemctl`、`ssh` 或任意配置透传 | 静态 + 单元 |
| `MCMD-R-032` | `incus-daemon-stop` 必须先阻止新 job、drain 或拒绝仍运行的 managed VM，再停止 daemon 并验证终态；force 必须是受约束参数和二次确认，不得只是透传 service stop | 单元 + e2e |
| `MCMD-R-033` | Forgejo Module Command 必须在独立 Incus/KVM 宿主验证 daemon start/stop、project reconcile、凭据隔离、幂等重放、中断和无 secret 泄漏 | e2e |
| `MCMD-R-034` | Module Command 的 CLI、HTTP 和 manifest/deployment 契约必须进入参考文档，并说明普通生命周期、Contract operation 与 Module Command 的边界 | 文档 |

## 4. 明确排除

本要求不包含交互式 shell、通用远程终端、任意 Docker/Compose/systemd/SSH 透传、未部署 Module 的
bootstrap 命令、由命令修改 config/lock/active deployment、浏览器提交 secret 参数，或让 Forgejo
应用容器持有 Incus/宿主维护权限。
