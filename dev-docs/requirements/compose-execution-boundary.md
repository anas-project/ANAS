---
doc_type: requirement
status: current
created: 2026-08-28
updated: 2026-08-28
---

# Compose 执行边界要求

本文规定 Runner 调用 Docker 与 Docker Compose 时的端点绑定、子进程环境、失败归因和补偿输出边界。
实施顺序见[Compose 执行边界实施计划](../plans/compose-execution-boundary.md)。本主题不另设架构文档：范围较小，
机制决策与被否决方案在本文中已经足够明确。

本文的需求矩阵是规范来源，正文是解释。两者冲突以矩阵为准。

## 1. 已有基线与剩余问题

当前 Runner 已经具备两项必须保留的安全性质：

- Compose project 名称稳定，并在变更或移除 Module 时先停止旧容器，再启动候选容器；固定宿主端口
  的旧容器不会与候选容器争用同一端口。
- 任何会修改 Compose project 的命令都先读取容器的
  `com.docker.compose.project.working_dir`，拒绝操作属于其他 workspace 的 project。

这两项已经解决最初的“新 deployment 目录导致固定端口重建冲突”和“同名 project 跨 workspace
误操作”问题。本主题不重新设计容器切换顺序，也不增加独立的部署预检脚本。

剩余边界有两处：

1. ownership 检查、Compose 调用及辅助 Docker 查询都各自继承进程环境；若调用期间环境来源不一致，
   安全检查与实际变更可能落在不同 daemon。
2. `exec.Cmd.Run` 只把 Compose 输出流向终端，返回值通常只有 `exit status 1`；候选启动失败后又会执行
   停止候选和恢复旧 deployment，后续输出容易遮住第一次失败。

## 2. Docker endpoint 绑定

Runner 的一次顶层命令必须先解析一个 endpoint identity，随后把同一 identity 显式传给该命令触发的
ownership 检查、Compose 调用、Docker 查询和补偿动作。解析发生在任何会修改容器状态的动作之前；
执行中不得重新读取 `DOCKER_HOST` 得到另一个答案。

CLI 保持当前选择方式：显式 `DOCKER_HOST` 优先；未设置时继续使用 Docker CLI 的默认 context。这里不
增加 `global.docker_socket` 配置项，因为 `DOCKER_SOCKET_PATH` 是提供给 Module 的宿主 socket 挂载源，
不是支持 `unix://`、`ssh://`、`tcp://` 等形式的控制端 endpoint，两者不能混为一个字段。

当 endpoint 是 Unix socket 时，可以从中派生 `DOCKER_SOCKET_PATH`；非 Unix endpoint 不得伪装成宿主
路径。需要挂载 Docker socket 的 Module 遇到非 Unix endpoint 时必须得到明确的前置条件错误，而不是
把 `ssh://` 或 `tcp://` 字符串交给 Compose 当 bind source。

未来 `anasd` 的 endpoint 来源仍由
[Web API 与管理前端要求](web-api-admin-console.md)中的 `CONSOLE-R-146`—`R-148` 约束：
服务端从 workspace 注册项取得 endpoint，并用白名单环境启动子进程。本主题不为 `anasd` 另造第二套规则。

## 3. Compose 失败归因

失败必须按执行阶段结构化保存，而不是从一段混合终端文本里猜“第一行”或“最后一行”。第一次失败的
子进程结果是 primary failure；停止候选、恢复旧 deployment 或清理资源产生的结果是 recovery outcome。
补偿成功或失败都不能覆盖 primary failure 的错误码、退出状态和有界 stderr 摘要。

普通 Compose 调用可以在继续实时写 stderr 的同时保留一个有字节上限的尾部摘要。摘要必须移除终端控制
序列、标明是否截断，并保留原始行序；不得依赖 Docker 当前版本的英文进度词列表来推测哪一句才是原因。

涉及候选凭据或其他敏感环境的 quiet 调用必须继续丢弃子进程输出。为了“更好报错”而捕获、持久化或
回显 quiet 输出是不允许的；这类失败只返回阶段、退出状态和经过调用方提供的安全错误说明。

CLI 的非 JSON 输出先报告 primary failure，再用明确的 recovery 标题报告补偿结果。`--json` 的最终错误
文档把两者放在不同字段；stderr 上的进度与 warning 仍保持 JSON Lines 契约。deployment failure state
也必须先写 primary failure，再开始可能失败的恢复动作，使进程中断后仍能查到触发补偿的原始原因。

## 4. 非目标与否决方案

- **不恢复旧 worktree 的补丁。** 它基于 196 个提交以前的 `casks/` 布局，不能移植到当前 Module
  deployment 模型。
- **不新增 `global.docker_socket`。** 配置字段会把 Module bind source 和 Docker 控制 endpoint 混合，
  也会与未来 `anasd` 的 workspace 注册模型冲突。
- **不靠日志关键字选择首因。** Compose 输出格式、语言和进度行会变化；首因由调用顺序决定，而不是
  由字符串启发式决定。
- **不在敏感路径保存 stderr。** 可诊断性不能突破已有凭据输出边界。
- **不重复修复端口竞态。** `de2e8ea` 的 stop-before-start 顺序是当前实现，新增预检不能替代它。

## 5. 需求矩阵

本矩阵是规范来源，正文是解释。两者冲突以矩阵为准。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `COMPOSE-R-001` | 变更或移除 Module 时，Runner 必须先停止旧 deployment 的对应 Compose project，再启动候选 project | 单元 |
| `COMPOSE-R-002` | Runner 在修改 Compose project 前必须验证现有容器的 workspace owner，拒绝跨 workspace 操作 | 单元 |
| `COMPOSE-R-003` | 一次顶层 Runner 命令必须在首次容器操作前解析唯一 Docker endpoint identity，并在该命令结束前保持不变 | 单元 |
| `COMPOSE-R-004` | ownership 检查、Compose 调用、Docker 查询与补偿动作必须显式使用同一个 endpoint identity | e2e |
| `COMPOSE-R-005` | CLI 显式设置 `DOCKER_HOST` 时必须使用该 endpoint；未设置时必须保持 Docker 默认 context 语义 | 单元 |
| `COMPOSE-R-006` | 子进程环境中不得出现会让同一次调用解析出不同 Docker endpoint 的重复或冲突值 | 单元 |
| `COMPOSE-R-007` | 只有 Unix endpoint 可以派生 `DOCKER_SOCKET_PATH`；非 Unix endpoint 不得作为 bind source 路径 | 单元 |
| `COMPOSE-R-008` | 需要宿主 Docker socket 挂载的 Module 遇到非 Unix endpoint 时必须在修改容器前返回稳定前置条件错误 | 契约 |
| `COMPOSE-R-009` | 变更容器状态前，CLI stderr 必须输出不含凭据的 endpoint identity；`--json` 模式必须输出对应的结构化进度事件 | 契约 |
| `COMPOSE-R-010` | 普通 Compose 子进程失败时必须保留退出状态与有字节上限的 stderr 摘要，同时继续把原始进度实时写到 stderr | 单元 |
| `COMPOSE-R-011` | stderr 摘要必须移除终端控制序列、保留行序并明确标注截断，不得靠首行、末行或英文关键字启发式选择原因 | 单元 |
| `COMPOSE-R-012` | 候选 deployment 的第一次失败必须作为 primary failure 保存，后续补偿结果不得覆盖其错误码、退出状态或摘要 | e2e |
| `COMPOSE-R-013` | CLI 非 JSON 输出必须先报告 primary failure，并把补偿开始、成功与失败标为独立 recovery 输出 | 契约 |
| `COMPOSE-R-014` | `--json` 最终错误文档必须把 primary failure 与 recovery outcome 放入不同字段，stderr 继续满足 JSON Lines 契约 | 契约 |
| `COMPOSE-R-015` | deployment failure state 必须在补偿开始前持久化 primary failure，使补偿期间进程退出后仍可查询原始原因 | e2e |
| `COMPOSE-R-016` | 敏感 quiet 调用不得捕获、持久化或回显子进程 stdout/stderr，只能返回安全的阶段与退出状态 | 单元 |
| `COMPOSE-R-017` | endpoint 与失败归因的稳定 CLI 错误码、JSON 字段和运维含义必须同步写入命令契约及中英文运维文档 | 审阅 |

## 6. 相关文档

- [Compose 执行边界实施计划](../plans/compose-execution-boundary.md)
- [运行时 Release 与状态设计](../../docs/architecture/runtime-release-state-design.md)
- [Web API 与管理前端要求](web-api-admin-console.md)
- [CLI 命令契约](../../docs/reference/contracts/commands.md)
