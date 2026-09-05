---
doc_type: requirement
status: current
created: 2026-09-05
updated: 2026-09-05
---

# 共享应用层迁移要求

本文是把三个共享服务实现从 `internal/runner` 迁入 `internal/application` 的目标、边界与验收标准。
[§4 需求矩阵](#4-需求矩阵规范来源)是规范来源，其余章节是解释。

配套计划见[共享应用层迁移实施计划](../plans/application-layer-migration.md)。

## 1. 缘由

[Web API 与管理前端要求](web-api-admin-console.md) §3.2 声明的代码边界是：共享用例在
`internal/application`，`internal/runner` 只承担 CLI flag 解析与文本输出。`CONSOLE-R-001` 据此要求
CLI 与 HTTP 共享同一服务层。

首版按这个方向交付了**抽象**，但没有完成**迁移**。2026-09-05 的实施复核确认了以下事实：

| 事实 | 数据 |
| --- | --- |
| `internal/application` 的规模 | 约 2,300 行，主要是接口、DTO 与 `module_command_*` 少数实现 |
| `internal/runner` 的规模 | 约 32,600 行（不含测试） |
| 仍在 `internal/runner` 的共享服务实现 | `deployment_application.go`（894 行）、`maintenance_application.go`（663 行）、`module_management_application.go`（547 行） |
| 这三个文件引用的 runner 内部未导出标识符 | 149 个 |
| 依赖方向 | 正确：`runner → application`，反向零导入 |

方向正确、抽象成立，因此这不是架构缺陷，而是**未完成的迁移**。它有三个具体代价：

1. **`anasd` 链接整个 CLI 包。** [Web API 与管理前端要求](web-api-admin-console.md) §4.4 拒绝 SQLite 的
   理由是「一次性引入数十个包并扩大一个近 root 服务的攻击面」。同一标准没有用在这里：守护进程
   当前链接 `internal/runner` 及其传递依赖 `internal/modulepackage`，后者内部执行 `go build` 与 `git`。
   链接不等于可达，但这是同一条推理的不一致适用。
2. **一个包服务两种运行时，靠运行时开关区分。** 子进程环境隔离（`CONSOLE-R-146`、`CONSOLE-R-147`）
   目前由 13 个文件、42 处 `restrictedProcessEnvironment` 分支实现。`CONSOLE-R-021` 的 inventory 门禁
   已经能拦住新增的裸调用点，但门禁是补丁，不是设计——正确做法是让守护进程根本无法走到 CLI 分支。
3. **新用例缺少明确落点。** `internal/application/module_command_*.go` 已经证明用例可以完全落在共享层
   （invoke 路径不触及 `internal/runner`），但三个既有实现留在旧位置，形成相互矛盾的先例。

**这条要求不改变任何外部可观察行为。** 它是纯粹的位置迁移与依赖收敛。

## 2. 范围

### 2.1 目标

把 `WorkspaceDeploymentService`、`WorkspaceMaintenanceService`、`WorkspaceModuleManagementService`
三个实现及其所需的 workspace 读写、锁、快照、备份、compose 与补偿逻辑迁入 `internal/application`
（或其下的子包），使 `cmd/anasd` 不再链接 `internal/runner`。

### 2.2 非目标

- **不**改变 CLI 的 `anas.dev/cli/v1` 输出，**不**改变 HTTP 的 `anas.dev/api/v1` 契约；
- **不**借迁移之机重构业务逻辑、改变锁语义或调整补偿路径；
- **不**要求一次性完成。分阶段迁移期间依赖方向必须始终成立，`anasd` 可以在最后一个阶段才断开
  对 `internal/runner` 的链接；
- **不**迁移纯 CLI 关注点：flag 解析、TTY 交互、文本渲染、`anas init` 的宿主机路径写入。

## 3. 硬约束

迁移每一步都必须保持既有验收不回归。特别是：

- 子进程环境隔离在迁移后**必须**由类型区分而不是运行时布尔开关表达：守护进程持有的服务类型不得
  存在通向 `os.Environ()` 继承或全局 `os.Stdout`/`os.Stderr` 的分支；
- 运行时锁的非阻塞语义（`CONSOLE-R-024`）与既有补偿逻辑复用（`CONSOLE-R-025`）不得被重写；
- `internal/application` 迁移后仍**不得**导入 `internal/runner`（`CONSOLE-R-184`）；
- 迁移不得引入新的外部依赖。

## 4. 需求矩阵（规范来源）

| ID | 要求 | 验证 |
| --- | --- | --- |
| `ALM-R-001` | `WorkspaceDeploymentService`、`WorkspaceMaintenanceService`、`WorkspaceModuleManagementService` 三个实现及其私有依赖迁入 `internal/application` 或其子包 | 审阅 |
| `ALM-R-002` | 迁移后 `cmd/anasd` 的导入图不再包含 `internal/runner`；该断言由门禁测试保证，不靠人工检查 | 单元 |
| `ALM-R-003` | 迁移全程 `internal/application` 及守护进程适配器不导入 `internal/runner`（沿用 `CONSOLE-R-184` 的门禁） | 单元 |
| `ALM-R-004` | CLI 的 `anas.dev/cli/v1` 输出在迁移前后逐字节不变，由既有 CLI contract 测试证明 | 契约 |
| `ALM-R-005` | HTTP 的 `anas.dev/api/v1` 契约与 OpenAPI 双向覆盖在迁移前后不变 | 契约 |
| `ALM-R-006` | 守护进程使用的服务类型不存在继承 `os.Environ()` 或写全局 `os.Stdout`/`os.Stderr` 的分支；子进程环境由构造时注入而不是运行时布尔选择 | 单元 |
| `ALM-R-007` | 迁移后 `CONSOLE-R-021` 的 daemon-reachable inventory 中不再出现由运行时开关保护的 CLI 变体条目 | 单元 |
| `ALM-R-008` | 运行时锁的 `LOCK_NB` 语义与既有 `cleanStaleSnapshotTemp`/`compensateContainerTransactions` 复用不变 | 单元 |
| `ALM-R-009` | 迁移不新增 `go.mod` 直接依赖 | CI |
| `ALM-R-010` | 迁移完成后 [Web API 与管理前端要求](web-api-admin-console.md) §3.2 的「已记录的债」段落随之删除，代码边界描述与实际布局一致 | 审阅 |

## 5. 参考资料

- [Web API 与管理前端要求](web-api-admin-console.md) §3.2（代码边界）、§4.4（攻击面取舍）、§10.1（`CONSOLE-R-001`）
- `internal/application/layering_test.go`（`CONSOLE-R-184` 的方向门禁）
- `cmd/anasd/subprocess_inventory_test.go`（`CONSOLE-R-021` 的调用点门禁）
- `internal/application/module_command_invoke.go`（完全落在共享层的用例样板）
