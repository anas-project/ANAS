---
doc_type: requirement
status: current
created: 2026-08-29
updated: 2026-08-29
---

# 内置 Module 与配置 Inventory 要求

本文规定内置 Module 集合、配置参数清单、派生统计、文档目录和发布门禁的单一逻辑事实边界。
实施记录见[已归档的内置 Inventory 实施计划](../plans/archived/builtin-inventory.md)。需求矩阵是规范来源，正文是解释；
两者冲突以矩阵为准。

## 1. 当前问题

Module 的运行声明属于各自 `modules/*/module.yml`，部署级参数由 Go `Global` 类型与嵌入的
`globals.yml` 共同声明；这些分布式声明符合 Module 自治边界，不应搬入一个中央大文件。

问题发生在声明之后：CLI 已经拥有类型化参数 inventory，但中英文 Module 目录、架构清单、配置参考、
Go 测试和 Shell 测试仍分别保存模块数、参数数及各类分布。新增、删除或重新分类参数时，这些副本会在
不同提交中变成互相矛盾的快照，数字标题还会让跨文档链接随数量变化而失效。

重新审计后的原始事实分为四个逻辑来源家族：

| 来源家族 | 文件 | 所有事实 |
| --- | --- | --- |
| Module manifest | `modules/*/module.yml` | Module 身份、版本、状态、类别、运行声明与 Module 参数 |
| 全局结构 | `internal/config/config.go` | `global.*` 的 Go/YAML 地址结构 |
| 全局参数元数据 | `internal/runner/globals.yml` | 全局参数类型、默认来源、约束、敏感性与 effect |
| 发布元数据 | `.github/modules.json` | repository、platforms 与 shared contexts；Module 集合只做一致性校验 |

它们承担不同所有权，不能安全压成一个中央声明文件；可以且必须合并的是声明之后的读取边界。
生成目录、统计表、测试 golden 和历史审计都不是新的原始事实来源。

## 2. 目标边界

仓库必须提供一个类型化的 built-in inventory 聚合入口。它读取现有 Module manifest、全局 schema 与
发布清单，返回排序稳定的 Module 元数据、完整参数投影和派生汇总。CLI、文档生成器与发布门禁消费
同一投影，不再独立解析一组较小的 manifest 字段或手工重算数量。

`modules/*/module.yml` 仍是 Module 身份、版本、状态、类别和运行声明的事实来源；全局参数仍由现有
Go/YAML schema 声明。`.github/modules.json` 可以继续保存 repository、platforms 和 shared context 等
发布元数据，但其中的 Module 集合必须与 manifest 集合严格相等，不能成为另一个可漂移的产品目录。

精确 surface 回归若需要人工确认，应保存完整、排序后的 Module 名与参数路径 golden，而不是只比较
总数。完整列表能发现“删一项又加一项”这种总数不变的替换；总数和分类分布则始终由列表派生。

## 3. 文档与测试边界

当前 Module 目录与架构清单必须由 inventory 生成中英文版本。配置参考可以保留人工维护的语义说明，
但所有会随 inventory 变化的
总数、owner 表、类型/default/effect 分布和约束统计必须位于生成块中。

生成器的普通模式更新生成块与 golden，`--check` 模式只读比较并报告漂移。历史 review、绑定提交的
审计快照和已归档计划不应被改写为“当前值”；现行指南、开发标准、需求和测试不得把可变总数当成稳定
契约。标题和跨文档链接必须使用不含数量的稳定锚点。

## 4. 非目标

- 不把全部 Module 参数复制到一个中央 schema 文件。
- 不改变任何参数的运行语义、默认值、effect、约束或地址。
- 不把发布平台与 shared context 伪装成运行时配置。
- 不重写历史 review 中明确绑定日期或提交的旧统计。

## 5. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `INVENTORY-R-001` | Runner 必须提供一个排序稳定的 built-in inventory，一次返回全部内置 Module 元数据、配置参数投影和派生汇总 | 单元 |
| `INVENTORY-R-002` | built-in inventory 的参数条目必须与 `anas config list --json` 使用的类型、默认来源、要求阶段、约束、敏感性和 effect 投影完全一致 | 单元 |
| `INVENTORY-R-003` | Module manifest 集合与 `.github/modules.json` 的 Module 集合必须完全相等，空项、重复项、缺项和多余项均被拒绝 | 单元 |
| `INVENTORY-R-004` | 中英文当前 Module 目录必须由同一排序后的 inventory 生成，每个 Module 恰好出现一次，并同步版本、状态、类别和描述 | CI |
| `INVENTORY-R-005` | 中英文配置参考中的总数、owner、类型、default source、effect、input-required、must-resolve、constraint 与 unknown 统计必须由 built-in inventory 生成 | CI |
| `INVENTORY-R-006` | 中英文架构文档中的当前内置 Module 表必须由同一 inventory 生成，不得继续手工维护另一套 Module 集合、状态和类别 | CI |
| `INVENTORY-R-007` | 配置参考的参数章节及所有现行跨文档链接必须使用不含参数数量的稳定锚点 | CI |
| `INVENTORY-R-008` | 精确 built-in surface 回归必须只使用一份包含完整 Module 名和参数路径的排序 golden；不得以散落总数代替完整集合 | 单元 |
| `INVENTORY-R-009` | Go 与 Shell inventory 测试不得各自维护 Module 总数、参数总数或类型/default/effect 分布副本 | 单元 |
| `INVENTORY-R-010` | 渲染覆盖测试必须比较实际 CLI inventory 的完整参数集合，不得固定 inventory 长度 | 单元 |
| `INVENTORY-R-011` | `gen-module-docs --check` 必须只读检测 Module 目录、配置统计、localization、Module 生成块和 golden 的任一漂移 | CI |
| `INVENTORY-R-012` | 工作树 Module 目录与文档构建期 Module 目录必须使用同一 catalog renderer，避免两套输出格式或字段集合 | 单元 |
| `INVENTORY-R-013` | `gen-module-docs` 普通模式必须保留四张参数表的人工作用文本并刷新已有行的全部机器派生列；`--check` 必须保持只读并报告同一漂移 | 单元 |
| `INVENTORY-R-014` | 新增参数缺少任一中英文 README/technical 作用文本，或删除/重命名后仍保留未声明路径时，生成与发布必须失败，不能生成占位语义 | 单元 |
| `INVENTORY-R-015` | 生成器必须只读输出排序、仓库相对且完整的持久输出文件闭集，供发布暂存使用；工作流不得另维护生成文件子集 | 单元 |
| `INVENTORY-R-016` | Module manifest、四个 inventory 原始来源家族及生成器读取实现变化进入 `image-release` 时，必须触发 Module/inventory 发布准备 | CI |
| `INVENTORY-R-017` | 发布准备必须在最终 revision 计算后生成文档，按生成器输出闭集暂存，与 revision 投影一起提交；提交后工作树必须为空并再次执行只读检查 | CI |
| `INVENTORY-R-018` | Bot 准备提交必须先安全推到 `image-release`，制品全部成功后才允许 fast-forward 到 `master` 并同步 CNB；失败或分叉不得 force push | CI |

## 6. 相关文档

- [已归档的内置 Inventory 实施计划](../plans/archived/builtin-inventory.md)
- [Module 文档生成标准](../../docs/developer/module-documentation.md)
- [结构化配置参考](../../docs/reference/configuration.md)
- [Module、Contract 与 Resource 设计](../../docs/architecture/module-contract-resource-design.md)
