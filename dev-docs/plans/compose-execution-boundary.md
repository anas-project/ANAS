---
doc_type: plan
status: proposed
created: 2026-08-28
updated: 2026-08-28
---

# Compose 执行边界实施计划

验收依据是[Compose 执行边界要求](../requirements/compose-execution-boundary.md)的需求矩阵。本主题没有
独立架构文档；endpoint 选择、命令执行边界和错误分层已经在要求文档中定案。

已有 stop-before-start 与 workspace owner guard 作为 M0 基线保留。当前入口是 M1；M2 依赖 M1 提供
统一调用上下文，否则只能继续让每个 Docker 调用自行继承环境。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：容器切换与 workspace owner 基线 | R-001—R-002 | 已完成；由现有 Runner 单元与事务测试覆盖 |
| M1：统一 Docker endpoint context | R-003—R-009 | 未开始 |
| M2：结构化 Compose 子进程错误 | R-010—R-011、R-016 | 未开始 |
| M3：primary/recovery 分层与持久化 | R-012—R-015 | 未开始 |
| M4：契约、运维文档与完整验收 | R-017 | 未开始 |

## 2. M0 落地快照

- [x] `changedOrRemovedModules` 计算旧 deployment 侧需要停止的 Module，并在候选启动前调用
      `oldApp.stopModules`。
- [x] `ensureComposeProjectOwner` 从容器 `working_dir` 反解 workspace，拒绝无 owner 或 owner 不同的
      同名 project。
- [x] unchanged Module 继承原 artifact deployment，避免只因新 deployment 目录改变 Compose
      `working_dir`。
- [x] `test-credential-rotation-e2e.sh` 与 Runner 单元测试验证 down 发生在候选 up 之前。

## 3. M1：统一 Docker endpoint context

- [ ] 定义 `DockerEndpoint` 值对象，至少记录规范化 identity、scheme、Unix socket path（若适用）和
      面向日志的脱敏显示值。
- [ ] 在 Runner 顶层命令入口解析一次 endpoint，并通过 `app`/执行 context 传给
      `ensureComposeProjectOwner`、`runCompose`、`outputCompose` 及直接 Docker 查询。
- [ ] 重构 `internal/compose.CLI`，由调用方显式传入 endpoint 环境；同一 `exec.Cmd.Env` 中只保留一个
      生效的 `DOCKER_HOST`。
- [ ] 未设置 `DOCKER_HOST` 时保留默认 context 行为，不通过硬编码 `/var/run/docker.sock` 改变 Docker
      Desktop、rootless Docker 或自定义 context。
- [ ] 把 `DOCKER_SOCKET_PATH` 派生限制为 Unix endpoint；为非 Unix endpoint 与需要 socket mount 的
      Module 定义稳定前置条件错误码。
- [ ] 在 workspace 公告之后、首个变更动作之前输出 endpoint identity；JSON 模式使用 progress/event，
      不向 stdout 混入日志。
- [ ] 单元测试覆盖 unset、Unix、SSH、TCP、重复环境值、ownership/Compose endpoint 一致性及
      socket-mount 拒绝路径。

## 4. M2：结构化 Compose 子进程错误

- [ ] 在命令执行边界增加类型化 `CommandFailure`，包含阶段、project、退出状态、是否截断及安全 stderr
      摘要；不要把环境变量或完整命令行默认放入错误。
- [ ] 普通 `RunFile` 使用有界 ring/tail writer 同时写真实 stderr 与摘要；规范化 ANSI/CR 但保持行序。
- [ ] `RunFileQuiet`/`OutputFileQuiet` 保持输出丢弃，只构造不含子进程文本的安全失败对象。
- [ ] 给摘要上限、UTF-8 截断、超长单行、ANSI、空 stderr、signal exit 和 quiet 路径补单元测试。
- [ ] 保留 `OutputFile` 的机器可读 stdout 语义，stderr 摘要不得混入返回的 stdout。

## 5. M3：primary/recovery 分层与持久化

- [ ] 将 activation 的失败处理拆成 `PrimaryFailure` 与 `RecoveryOutcome`；补偿函数不再拼接一个无结构的
      `error.Error()` 字符串。
- [ ] `saveDeploymentFailure` 在停止候选或恢复旧 deployment 前写入 primary；补偿结束后只追加 recovery
      字段，不改写 primary。
- [ ] 非 JSON stderr 先输出 primary，再输出带明确 recovery phase 的开始、成功或失败事件。
- [ ] 扩展 CLI JSON error detail，分别暴露 `primary` 与 `recovery`；保持顶层稳定错误码仍代表触发补偿的
      primary failure。
- [ ] 用 fake Docker/Compose 注入“候选启动失败 + 候选停止失败 + 旧 deployment 恢复失败”，验证三个
      结果均可见且 primary 始终不变。
- [ ] 注入 primary 持久化完成后的进程中断，重新读取 deployment state 时仍能得到原始失败。

## 6. M4：契约与运维文档

- [ ] 更新 `docs/reference/contracts/commands.md` 中 `apply`、`start`、`restart`、`stop` 的 endpoint 与错误
      detail 契约。
- [ ] 同步中英文运维文档，说明 endpoint identity、非 Unix endpoint 的 socket-mount 限制，以及如何
      区分 primary 与 recovery。
- [ ] 在开发测试文档登记 endpoint 一致性与双重失败注入入口。
- [ ] 运行全量 Go、契约、需求、文档状态与 VitePress 构建门禁。

## 7. 当前阻塞

没有外部阻塞。M1 必须先确定默认 Docker context 在不设置 `DOCKER_HOST` 时如何以“不改变 Docker CLI
语义”的方式传递给 ownership 检查与 Compose；该决策在 M1 内通过适配层单元测试固定，不需要新增用户
配置字段。

## 8. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-004 | 待扩展 `test-env/scripts/test-compose-project-isolation.sh` | fake Docker，ownership 与 Compose 记录 endpoint | — | 待实现 |
| R-012 | 待新增 `test-env/scripts/test-compose-failure-recovery.sh` | fake Compose，候选启动与恢复均失败 | — | 待实现 |
| R-015 | `test-compose-failure-recovery.sh interrupted-after-primary` | primary 持久化后注入进程中断 | — | 待实现 |

## 9. CI 门禁记录

尚无实施提交。M0 的既有测试在本计划创建时不重新声明为本主题的新 CI 证据；M1 开始后记录首次全绿
提交及对应命令。

## 10. 文档同步

| 文档 | 当前状态 | 完成条件 |
| --- | --- | --- |
| `/requirements/compose-execution-boundary` | 已建立 | 需求变化时原地更新稳定 ID，不复用编号 |
| `/plans/compose-execution-boundary` | 提案 | 每个里程碑随实现逐 PR 更新 |
| `/reference/contracts/commands` | 待 M4 | 稳定错误码和 JSON detail 落地后同步 |
| 中英文运维与开发测试文档 | 待 M4 | endpoint 与故障归因可被管理员和贡献者查到 |
