---
doc_type: plan
status: implementing
created: 2026-08-22
updated: 2026-08-28
---

# 需求 ID 矩阵采用实施计划

验收依据是[需求 ID 矩阵采用范围与门禁要求](../requirements/requirement-id-adoption.md)的需求矩阵。
本主题没有独立架构文档——它改的是文档门禁脚本和文档存放位置，不引入运行时机制。

M1（双向登出需求矩阵）、M2（迁移后的扫描边界）与 M3（索引状态列生成）已落地。M0 仍可独立推进；
`module-iam-bidirectional-logout` 已直接采用矩阵，因此无需等待豁免清单即可接受门禁检查。

## 1. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：门禁可见性与豁免清单 | R-001—R-003、R-008—R-009、R-014 | 未开始 |
| M1：双向登出需求矩阵 | R-004—R-005 | 已完成 |
| M2：迁移后的扫描边界 | R-006—R-007 | 已完成 |
| M3：索引状态列生成 | R-010—R-013 | 已完成 |

## 2. M0 检查表

- [ ] 在 `scripts/ci/requirement-coverage.mjs` 中把「无矩阵即 `continue`」改为查豁免清单。
- [ ] 新增豁免清单文件（建议 `scripts/ci/requirement-exemptions.json`），字段为 `path`、`reason`、
      `reviewed`。
- [ ] 登记 `iam-provider.md` 与 `vikunja-module.md` 两条豁免，理由按要求文档 §3 填写。
- [ ] 门禁输出末尾追加「已豁免 N 份」清单，逐行打印路径与理由。
- [ ] 跨文档前缀唯一性校验：收集所有矩阵的前缀集合，出现交集即失败。
- [ ] 豁免清单自身校验：条目指向的文件不存在时失败（R-009），防止改名后豁免悬空。
- [ ] 退出标记改为专用信号（R-014）：`parseRequirementMatrix` 现在扫描整行文本，导致一条描述
      退出规则的需求会把自己判成已退出。改成独立列或行首标记，并补一个「正文提到关键词但未标记
      的行不算退出」的测试。
- [ ] 为 `requirement-coverage-lib.mjs` 新增单元测试覆盖 R-001—R-003、R-008—R-009 的失败分支。
- [ ] 通过 `npm run docs:check-requirements`。

## 3. M1 检查表

- [x] 按[需求编写规范](../../docs/developer/requirement-authoring.md) §1 的顺序处理
      `docs/requirements/module-iam-bidirectional-logout.md`：先补散文中缺失的组合结论，再抽断言。
- [x] 枚举当前 Provider（`llng`、`authentik`、`casdoor`）× 已接入 Module × 登出方向（IAM→应用、
      应用→IAM）的完整组合空间，逐格判定支持与否。
- [x] 分配 `LOGOUT-R-###` 前缀的稳定 ID，受支持与不支持的组合各自成行。
- [x] 新建 `docs/plans/module-iam-bidirectional-logout.md`，包含归属表与 e2e 执行记录表。
- [x] 标注 `e2e` 的条目在执行记录表中给出脚本名；待执行项明确记录为待跑。
- [x] 该文档已直接采用矩阵，M0 豁免清单无需登记或移除它。
- [x] 通过 `npm run docs:check-requirements`（2026-08-28：双向登出 42 项均有唯一归属及 e2e 记录）。

## 4. M2 检查表

- [x] 迁移完成后，把 `requirementsDir` / `plansDir` 常量一次性改到新位置，不保留旧路径回退。
      新增 `scripts/ci/requirement-docs-lib.mjs` 承载目录解析，`requirement-coverage.mjs`、
      `requirement-status.mjs` 与 `document-status.mjs` 都从它取扫描范围。
- [x] 扩展扫描逻辑覆盖 Module 私有需求目录，配对规则与仓库级一致
      （`requirementScopes()` 依次收 `dev-docs/` 与每个 `modules/*/dev-docs/`）。
- [x] 新增测试：同一主题在新旧两个位置同时存在时门禁必须失败
      （`scripts/ci/requirement-docs-test.mjs`，已串进 `npm run docs:test-requirements`）。
- [x] 通过 `npm run docs:check-requirements`。

## 5. M3 检查表

- [x] 里程碑状态词汇规范化为 `已完成` / `实施中` / `未开始` / `阻塞` 开头，细节写在分号之后；
      `docs/plans/module-command-capability.md` 的 M3/M4 两行按此改写。
- [x] 新增 `scripts/ci/requirement-status-lib.mjs`：从需求矩阵与计划里程碑表算出每份文档的完成度，
      并渲染索引状态列。复用 `requirement-coverage-lib.mjs` 的解析函数，不重复实现表格解析。
- [x] 新增 `scripts/ci/requirement-status.mjs`：默认写入 `docs/requirements/index.md`，
      `--check` 模式在过期时失败，与 `gen-module-docs --check` 同一契约。
- [x] 新增 `scripts/ci/requirement-status-test.mjs`，覆盖词汇解析、废弃项退出分母、里程碑状态非法、
      需求未归属、无矩阵、无计划、渲染只改有算出值的行、渲染幂等。
- [x] `package.json` 增加 `docs:requirement-status` 与 `docs:check-requirement-status`；
      `docs:test-requirements` 串联新测试。
- [x] `.github/workflows/docs.yml` 的「Validate documentation sources」加入
      `npm run docs:check-requirement-status`。
- [x] 通过 `npm run docs:test-requirements`、`npm run docs:check-requirement-status`、
      `npm run docs:check-requirements`、`npm run docs:build`。

M2 已把本生成器的 `requirementsDir` / `plansDir` 常量与门禁脚本一并切到 `dev-docs/`。索引只渲染
仓库级作用域：Module 私有需求属于该 Module 自己的索引，但仍受覆盖门禁校验。

## 6. e2e 执行记录

本主题没有标注 `e2e` 的需求：门禁行为在 Node 单元测试内即可判定，无需真实 Docker 或域名。

## 7. 当前阻塞

- M1 的组合空间需要先确认「已接入 Module」的准确清单。`de2e8ea` 落地了双向登出矩阵的实现，
  但实现覆盖范围与文档结论是否一致尚未逐格核对——这是 M1 第一步要做的事，不是可以跳过的前提。
