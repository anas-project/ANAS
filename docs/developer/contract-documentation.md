# Contract 文档生成标准

本标准规定 Contract 文档的源文件、机器契约、人工语义、自动生成范围、VitePress 映射、双语输出和 CI 验证。它与 [Module 文档生成标准](/developer/module-documentation) 共同构成 Module/Contract 文档维护基线。

> [!NOTE]
> 当前 `cmd/gen-contract-docs` 已实现 README 生成块以及中英文用户/技术页面的 VitePress 镜像。自动生成 Contract 目录、侧边栏数据和 CI `--check` 仍是本标准要求但尚未完成的部分。

## 1. 源文件结构

每个 `contracts/<name>/` 必须包含：

```text
contracts/<name>/
├── contract.yml
├── documentation.yml
├── schemas/
│   └── *.yml
├── README.md
├── README.en.md
└── docs/
    ├── technical.md
    └── technical.en.md
```

| 文件 | 职责 |
| --- | --- |
| `contract.yml` | Contract 名、版本、接口、Resource identity、operation 和 schema 路径 |
| `schemas/*.yml` | Resource、request、result 的类型、required、字段和约束 |
| `documentation.yml` | 文档 API、实现状态、复核日期和中英文摘要 |
| `README.md` / `README.en.md` | 面向使用方的语义、能力、示例、错误和运维边界 |
| `docs/technical.md` / `docs/technical.en.md` | Provider/Consumer、幂等、Secret、生命周期、兼容性和测试实现 |

`contracts/<name>/` 是唯一事实来源。`docs/reference/module-contracts/` 下的文件是生成镜像，不得直接编辑，也不得在生成页面中维护源目录没有的独立事实。

## 2. 命名、身份与版本

以下值必须一致：

- 目录名 `contracts/<name>`；
- `contract.yml` 的 `name`；
- `documentation.yml` 的 `contract`；
- Module manifest 引用的 Contract name。

Contract 使用语义化版本。兼容字段扩展可以保留当前 major；删除、改名、收紧字段、改变 Resource identity 或 operation 语义必须提升 major。README 和技术文档必须说明兼容边界、弃用期和迁移方式。

Contract 不建立独立发布 tag 或独立站点版本轴。正式站点从所选 ANAS `vMAJOR.MINOR.PATCH` tag 读取 Contract 源文档、schema 和同一快照下的 Module manifests，因此 Contract 页面和 Provider/Consumer 矩阵随 ANAS Core 发布与归档。Module release 不得单独推进 Contract 页面内容。

Resource identity 必须稳定、可序列化，并足以区分同一 Consumer 的多个 Resource。文档不得把显示名或易变地址误写成稳定身份。

## 3. `documentation.yml`

```yaml
api_version: anas.contract-documentation/v1
contract: example
status: implemented
reviewed_at: 2026-08-14
summary:
  zh: 中文摘要。
  en: English summary.
```

`status` 只能是：

- `implemented`：Runner 已调度、至少一个 Provider 实现且真实 E2E 已验证；
- `partial`：只有部分 operation、interface 或 Provider/Consumer 链路实现；
- `pending`：契约已定义，但 Runner 当前不调度；
- `proposal`：仍需 ADR、必要性或接口评审；
- `deprecated`：仅保留兼容，不得新增 Consumer。

存在 `contract.yml`、schema、Provider 名称或测试桩不能自动证明 `implemented`。`reviewed_at` 记录语义实际复核日期，不是生成日期。

## 4. README 用户文档必需内容

中英文 README 必须结构等价，并至少包含：

1. Contract 的业务目的、适用范围和不适用范围；
2. 当前 version、status、reviewed date 和 interfaces；
3. Resource identity、所有权和生命周期语义；
4. 每个 operation 的用途、required、输入、输出、幂等性和错误边界；
5. 当前 Provider、Consumer、版本约束、接口和实现状态；
6. 凭据、Secret、权限和数据删除的用户可见边界；
7. 可复制但不包含真实 Secret 的调用或 manifest 示例；
8. 验证、故障恢复、未实现项和已知限制；
9. 技术文档链接。

如果 Contract 仍为 `pending` 或 `proposal`，示例必须标明“当前不可执行”。可选 operation 未实现时必须返回或记录“不支持”，不得伪装成成功。

## 5. 技术文档必需内容

中英文 `docs/technical*.md` 必须至少包含：

1. 完整 manifest 与 schema 索引；
2. Resource identity、所有权、幂等键和持久化位置；
3. operation 的请求/结果、前置条件、事务顺序和状态转换；
4. Provider dispatch、Consumer binding、版本选择和 lock/deployment 状态；
5. Secret 生成、保存、投影、轮换、撤销和日志边界；
6. 权限、网络、进程和信任边界；
7. delete/retain、失败补偿、重试、回滚和灾难恢复语义；
8. 兼容性、版本升级、弃用和迁移策略；
9. Provider/Consumer 实现位置、Runner 调度入口、单测和真实服务 E2E；
10. 当前限制、未实现 operation 和验证缺口；
11. 文档生成来源和复核方式。

技术文档解释“为什么”和“如何实现”，不能只是重复 schema 字段表。schema 是字段契约，技术文档负责跨 operation 的安全与生命周期语义。

## 6. 机器生成与人工审核边界

以下内容由生成器从 `contract.yml`、schema、Module manifests 和 `documentation.yml` 生成，位于 `generated:contract-reference` 标记之间：

- Contract version、status、review date 和 interfaces；
- Resource identity 和 Resource schema；
- operation 的 required、request schema 和 result schema；
- 每个 schema 的类型、required 字段和全部字段；
- 当前 Provider、Consumer、版本约束、接口和实现入口。

以下内容必须在标记外人工或由 AI 分析后审核：

- 业务目的和 Resource identity 的真实语义；
- 幂等、事务、锁、重试、补偿和删除语义；
- Secret、权限、日志和信任边界；
- 兼容性、迁移、故障恢复和未实现项；
- 运行时可用性和 E2E 结论。

静态分析不能证明应用内状态已改变、回滚可靠或权限边界生效。生成器不得把静态声明提升为未经验证的运行结论。

## 7. 生成标记与编辑规则

README 中的生成块使用：

```markdown
<!-- generated:contract-reference:start -->
...
<!-- generated:contract-reference:end -->
```

生成器只能替换该块。缺失生成块时可以追加；重复、反向或不平衡标记必须失败。标记外 README 和 `docs/technical*.md` 是人工审核源文件，生成器不得覆盖其语义内容。

生成后的 `docs/reference/module-contracts/*.md` 必须带有“请勿直接编辑”提示。修复站点页面时必须修改 Contract 源文档或生成器后重新生成，不能直接修生成镜像。

## 8. VitePress 输出规则

每个 Contract 必须生成四个页面：

| Contract 源文件 | 中文站点输出 | 英文站点输出 |
| --- | --- | --- |
| `README.md` / `README.en.md` | `docs/reference/module-contracts/<name>.md` | `docs/en/reference/module-contracts/<name>.md` |
| `docs/technical.md` / `docs/technical.en.md` | `docs/reference/module-contracts/<name>-technical.md` | `docs/en/reference/module-contracts/<name>-technical.md` |

生成器必须把源 README 的 `docs/technical*.md` 链接改写为对应站点技术页，并把技术文档返回 `../README*.md` 的链接改写为站点用户页。源目录和 VitePress 镜像必须同时无死链。

Contract 目录和侧边栏中的名称、状态、版本和链接应该从同一排序后的 Contract 清单生成。用户页必须可由侧边栏或 Contract 目录发现；技术页可以从用户页进入，不要求全部平铺到侧边栏。

## 9. 生成器行为

`cmd/gen-contract-docs` 必须：

1. 枚举所有包含 `contract.yml` 的目录；
2. 严格校验必需源文件、API version、名称、status、version、interfaces、operations 和 schema 路径；
3. 校验 operation 引用的 schema 存在，并报告未知或无法解析的引用；
4. 扫描所有 Module manifests 生成 Provider/Consumer 矩阵；
5. 只修改 README 生成标记，保留人工内容；
6. 原子生成全部中英文用户页、技术页、目录和导航数据；
7. 使用确定性排序和稳定 Markdown；
8. 在 `--check` 模式下不写文件，并对缺失源文件、缺失镜像或陈旧输出失败；
9. 不根据名称或文件存在自动提升实现状态。

```bash
go run ./cmd/gen-contract-docs
go run ./cmd/gen-contract-docs --check
npm run docs:build
```

`npm run docs:build` 只构建已经位于 `docs/` 的页面，不会自动运行 Contract 生成器。

## 10. 双语与技术标识

中英文 README 和技术文档必须具有相同章节、operation 集合、支持状态、默认值、风险和限制。以下标识保持原文，不翻译：

- Contract、Module、Provider、Consumer 和 Resource 名称；
- interface、operation、字段和 schema path；
- CLI 命令、环境变量、状态值、错误码和版本约束。

只翻译解释文本。修改一种语言时必须在同一变更中更新另一种语言和所有生成镜像。

## 11. CI 与提交验收

CI 必须在 VitePress 构建前执行：

```bash
go run ./cmd/gen-contract-docs --check
test-env/scripts/test-contract.sh
npm run docs:build
```

如果某 Contract 没有统一测试入口，必须运行相关 Provider/Consumer 单测和真实服务 E2E。提升为 `implemented` 的同一变更必须包含 Provider/Consumer manifests、Runner 调度、operation 测试和 E2E 证据。

验收清单：

- [ ] 所有 Contract 均有 manifest、documentation metadata、schemas 和四份双语文档；
- [ ] 名称、版本、interfaces、identity、operations 和 schema 引用一致；
- [ ] 中英文语义、支持状态、风险和限制一致；
- [ ] Provider/Consumer 矩阵来自当前 Module manifests；
- [ ] Secret、权限、幂等、删除、补偿和恢复边界明确；
- [ ] pending/proposal/未实现 operation 没有被写成当前可用；
- [ ] 生成块、VitePress 镜像、链接、目录和导航没有漂移；
- [ ] 生成器 `--check`、Contract 测试和 `npm run docs:build` 通过。
