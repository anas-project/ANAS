---
doc_type: plan
status: done
created: 2026-08-29
updated: 2026-08-29
---

# 内置 Module 与配置 Inventory 实施计划

验收依据是[内置 Module 与配置 Inventory 要求](../../requirements/builtin-inventory.md)的需求矩阵。
M1—M4 已完成；本计划没有改变配置运行语义，只统一声明后的读取、生成、回归和发布回写边界。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：统一类型化聚合与 surface golden | R-001—R-003、R-008 | 已完成 |
| M2：Module 目录与配置参考生成 | R-004—R-007、R-012 | 已完成 |
| M3：测试迁移与只读检查 | R-009—R-011 | 已完成 |
| M4：参数文档与发布回写 | R-013—R-018 | 已完成 |

## 2. M1：统一类型化聚合与 golden

- [x] 在 Runner 中增加 `BuiltinInventory`、Module 投影和确定性 summary，复用现有参数 inventory。
- [x] 让 `LoadConfigParameterInventory` 委托统一聚合，保持 CLI/HTTP 调用方行为不变。
- [x] 校验 manifest 集合与 `.github/modules.json` 集合严格相等。
- [x] 增加一份排序后的 Module 名/参数路径 golden，并比较完整集合而非总数。

## 3. M2：生成现行文档

- [x] 抽出工作树与构建期共用的双语 Module catalog renderer。
- [x] 让 `gen-module-docs` 写入中英文 Module 目录与配置 inventory 生成块。
- [x] 让中英文架构文档的内置 Module 表由同一 inventory 生成，并引用生成目录。
- [x] 将参数章节改为稳定标题并迁移数字锚点链接。
- [x] 移除现行开发/需求/计划文档中的可变总数；绑定提交的历史快照保留明确基线。

## 4. M3：测试与门禁

- [x] Go 测试从单一 golden 读取完整 surface，删除散落数量断言。
- [x] `test-parameters.sh` 保留字段语义与代表性约束检查，删除总数和分类分布副本。
- [x] `test-render.sh` 直接使用本轮 CLI inventory 集合验证 transport 覆盖。
- [x] `gen-module-docs --check` 覆盖新增输出且不写工作树。
- [x] 运行 Go、Shell、文档构建、需求覆盖及双索引状态门禁。

## 5. M4：参数文档与发布回写

- [x] 普通生成模式保留人工 Purpose，更新已有参数行的机器列；新增、删除和重命名缺少人工语义时
  关闭式失败。
- [x] `--print-managed-files` 确定性输出全部持久生成文件的仓库相对路径。
- [x] `image-release` 覆盖 inventory 原始来源及生成实现的触发路径，并按生成器输出闭集暂存。
- [x] Bot 提交后要求工作树为空，再执行只读检查并安全推送；发布成功后才快进 `master`、同步 CNB。
- [x] 中英文 Module 开发、文档标准和发布说明明确参数修改及人工/自动边界。

## 6. 当前阻塞

没有外部阻塞。实施保留了工作树原有 Web API 需求/计划与架构修改的语义，只把其中的可变统计
迁入生成块或统一 inventory 引用。

## 7. 验证命令与 CI 门禁记录

本主题没有新增 e2e 场景；使用以下单元、集成、Shell 与文档门禁验收：

```bash
go test ./internal/runner ./internal/moduledocs ./cmd/gen-module-docs ./cmd/materialize-module-docs
go test ./...
go vet ./...
go run ./cmd/gen-module-docs --check
sh test-env/scripts/test-parameters.sh
sh test-env/scripts/test-render.sh
npm run docs:check-requirements
npm run docs:requirement-status
npm run docs:plan-status
npm run docs:check-requirement-status
npm run docs:check-plan-status
npm run docs:check-status
npm run docs:build
```

2026-08-29 验收结果：

- `go test ./...`、`go vet ./...` 通过；
- `go run ./cmd/gen-module-docs --check` 通过；
- `test-parameters.sh` 通过；
- `test-render.sh` 通过，并从本轮 CLI inventory 动态观察全部参数 transport；其中同步修正了已完成
  Forgejo M3 后遗留的旧断言，现验证 `actions_enabled` 是服务端/controller 的共享关闭开关；
- `docs:check-requirements`、`docs:check-status`、双索引 check 与 `docs:build` 通过；VitePress 只有既有
  大 chunk 提示，没有构建错误。

M4 增量验收：

- `go test ./...`、`go vet ./...` 与 `scripts/ci/module-revisions-test.sh` 通过；生成器单测覆盖普通模式
  参数机器列写回、空 Purpose/缺失路径/残留路径拒绝、97 项持久输出闭集和发布步骤顺序；
- `gen-module-docs --check` 与 `--print-managed-files` 通过，后者按排序输出 22 个 Module 的 88 份
  源文档和 9 份全局汇总/架构/golden；
- `test-parameters.sh` 与 `test-render.sh` 通过，渲染矩阵动态观察全部 171 个参数 transport；
- 需求/状态/索引门禁和 `docs:build` 再次通过，发布工作流 YAML 可解析。

## 8. 文档同步

| 文档 | 当前状态 | 完成条件 |
| --- | --- | --- |
| `/requirements/builtin-inventory` | 已完成 | 稳定 ID 与实现一致 |
| `/plans/archived/builtin-inventory` | 已归档 | 三个里程碑全部完成 |
| `/reference/modules`、`/en/reference/modules` | 已生成化 | 普通生成与 `--check` 一致 |
| `/reference/configuration`、`/en/reference/configuration` | 已生成化 | 可变统计全部来自 inventory |
| `/architecture/module-contract-resource-design` | 已生成化 | 当前 Module 表与目录使用同一 inventory |
