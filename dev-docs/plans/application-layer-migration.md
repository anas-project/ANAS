---
doc_type: plan
status: proposed
created: 2026-09-05
updated: 2026-09-13
---

# 共享应用层迁移实施计划

验收依据是[共享应用层迁移要求](../requirements/application-layer-migration.md)的需求矩阵。
本文只回答「先做什么」，不复述验收标准。

## 1. 给实施者

**当前状态：未开始。** 这是 2026-09-05 对[Web API 与管理前端](../requirements/web-api-admin-console.md)
首版实施复核后立项的技术债偿还，不改变任何外部可观察行为。

**先读这个再动手：** 迁移的难点不是搬文件，而是那三个文件引用的 **149 个 runner 内部未导出标识符**。
盲目按文件迁移会把半个 `internal/runner` 拖进来。正确顺序是先切依赖，再搬实现。

**每个阶段结束都要过这一组**，任何一条红就地停下，不要继续下一阶段：

```bash
go build ./... && go vet ./... && go test ./...
```

```bash
go test ./internal/application -run 'TestSharedLayer|TestCLIAdapter' && go test ./cmd/anasd -run TestDaemonReachableSubprocessInventory
```

```bash
npm run docs:check-requirements && npm run docs:check-plan-status
```

**改动纪律：** CLI contract 测试（`ALM-R-004`）与 OpenAPI 双向覆盖（`ALM-R-005`）是这次迁移唯一的
安全网。任何一个阶段如果需要修改这两类测试的期望值，说明迁移改变了行为——回退，不要改测试。

## 2. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：依赖测绘与门禁 | R-002、R-003、R-009 | 未开始 |
| M1：抽出无状态私有依赖 | R-008 | 未开始 |
| M2：子进程边界改为构造时注入 | R-006、R-007 | 未开始 |
| M3：迁移三个服务实现 | R-001、R-004、R-005 | 未开始 |
| M4：断开 `anasd` 对 runner 的链接并收尾文档 | R-010 | 未开始 |

### M0：依赖测绘与门禁

先让「还差多少」可测量，再动代码。

- 生成三个实现对 `internal/runner` 内部标识符的完整引用清单，按「纯函数 / 需要 `*app` 状态 /
  需要文件系统布局」分类；分类结果决定 M1 与 M3 的切分。
- 加一个当前**预期失败**的门禁：断言 `cmd/anasd` 的导入图不含 `internal/runner`（`ALM-R-002`），
  用 skip 标记并注明本计划，M4 解除。这样迁移完成的判定是自动的，不靠人读代码。
- `internal/application/layering_test.go` 与 `cmd/anasd/subprocess_inventory_test.go` 已存在，
  M0 只需确认它们覆盖新增的包路径。

### M1：抽出无状态私有依赖

把 M0 归类为「纯函数」的部分（路径构造、argv 构造、快照与备份元数据的解析与投影、ID 校验）
迁入 `internal/application` 的内部子包。这批没有 `*app` 状态，迁移风险最低，且能显著缩小 M3 的面。

`internal/runner` 改为从新位置导入，CLI 行为不变。

### M2：子进程边界改为构造时注入

这是整个计划里唯一改变**代码形态**而非位置的阶段，也是最有价值的一步。

当前 42 处 `restrictedProcessEnvironment` 分支要收敛为一个在构造时注入的命令执行器：CLI 构造继承
环境、写 stderr 的执行器，守护进程构造显式环境、丢弃输出、带 context 的执行器。调用点不再分支，
因而守护进程**在类型上**无法走到 CLI 路径（`ALM-R-006`）。

完成后 `CONSOLE-R-021` 的 inventory 中，由运行时开关保护的条目应当消失（`ALM-R-007`），
inventory 只剩真正 CLI 专属的调用点。

### M3：迁移三个服务实现

按 `module_management` → `maintenance` → `deployment` 的顺序，由小到大、由弱耦合到强耦合。
每迁一个就跑全量验证并提交，不要三个一起搬。

`deployment_application.go` 放最后：它牵扯运行时锁、补偿逻辑与 materialize 路径，是唯一可能需要
调整包边界的一个。

### M4：断开链接并收尾

- 解除 M0 门禁的 skip，`ALM-R-002` 转为常态断言；
- 删除 [Web API 与管理前端要求](../requirements/web-api-admin-console.md) §3.2 的「已记录的债」段落，
  代码边界描述改为与实际布局一致（`ALM-R-010`）；
- 复核 `internal/runner` 剩余内容是否确实只剩 CLI flag、TTY 交互与文本渲染。

## 3. 风险与退出条件

| 风险 | 处理 |
| --- | --- |
| M3 迁移 `deployment_application.go` 时发现锁与补偿逻辑无法与 CLI 路径解耦 | 停在 M3，保留前三个阶段成果。M1/M2 单独就有价值，`anasd` 仍链接 runner 但子进程边界已由类型保证 |
| 迁移过程中 CLI contract 测试变红 | 回退该步。测试期望值不得修改（见 §1 改动纪律） |
| 包循环 | `internal/application` 不得导入 `internal/runner`（`ALM-R-003`），出现循环说明切分点选错，回到 M0 重新分类 |

**允许的部分完成状态：** M0—M2 完成、M3—M4 未完成是一个合理的停靠点，此时 `ALM-R-006`、`ALM-R-007`
已达成而 `ALM-R-001`、`ALM-R-002` 未达成。计划状态相应记为 `partial`，不算失败。

## 4. CI 门禁

全部里程碑未开始，没有实施提交可记录。下表是 §1 每阶段必过的门禁在**开工前**的基线，M0 起逐阶段更新：

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go vet ./...` | `5306b63`（GitHub CI，2026-09-05） |
| `go test ./...` | `25433bd`（GitHub CI，2026-08-29），当时下一行的两个门禁测试尚未加入；`5306b63` 上因 `internal/runner` 的 `TestMaintenanceBackupCreateDescriptorIsBoundToPublicPlan` 失败，其后提交均未经 CI |
| §1 第二组定向测试（`TestSharedLayer*`、`TestCLIAdapter*`、`TestDaemonReachableSubprocessInventory`） | 没有独立 CI 步骤；`internal/application` 与 `cmd/anasd` 两个包在 `5306b63` 的 `go test ./...` 中通过（该次整体失败） |
| `go build ./...` | 从未单独记录：CI 没有这一步 |
| `npm run docs:check-requirements`、`docs:check-plan-status` | CI 无记录：本计划随 `9888ae1`（2026-09-06）加入，未推送；本地 `6823232` 通过 |

`ALM-R-009`（不新增 `go.mod` 直接依赖）的验证方式是 CI，但 `.github/workflows/ci.yml` 目前没有对应
步骤——这是 M0 要补的门禁，不是已有记录。

## 5. e2e 执行记录

本计划无 `e2e` 验证方式的需求：迁移不改变外部行为，验收由 CLI contract、OpenAPI 覆盖与门禁测试承担。
若 M3 期间发现行为差异，说明迁移出错，应回退而不是补 e2e。

## 6. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [Web API 与管理前端要求](../requirements/web-api-admin-console.md) §3.2 | 删除「已记录的债」段落，目录说明里 `internal/application` 与 `internal/runner` 的分工改为与实际布局一致（`ALM-R-010`） | 未开始（M4） |
| [仓库结构](../../docs/developer/repository-layout.md)与英文镜像 | `internal/runner/` 的「迁移期实现」随三个服务迁出后改写 | 未开始（M3—M4） |
