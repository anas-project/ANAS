# Test case catalogs

`test-env/cases/<topic>/cases.yml` 是需求到测试实现之间的机器可读验证契约；同目录 `README.md` 由
`cases.yml` 生成，只供审阅，不得手改。`topic` 必须与成对的
`dev-docs/requirements/<topic>.md`、`dev-docs/plans/<topic>.md` 以及目录名一致。

## 工作流

1. 在需求矩阵中写稳定、原子且可证伪的需求，并在计划中分配唯一里程碑。
2. 把本轮采用的需求 ID 放入 `requirement_scope`，为每个自动验证需求创建至少一个 active 用例。
3. 用例使用需求前缀的 `<PREFIX>-T-<三位序号>`；ID 永久不复用。废弃用例保留原 ID、标为
   `retired` 并列出 active `replaced_by`。
4. Agent 根据明确需求和用例直接生成或修改完整 Go、Shell、Python、Playwright 测试；不得只留下
   `TODO` 脚手架。实现文件用语言对应的注释声明 `TEST_CASES: <ID>[, <ID>...]`，catalog 反向列出文件。
5. 运行 `test-cases:review`，按“需求差异 -> 用例差异 -> 测试代码差异”审阅普通 Git 补丁；不得静默
   覆盖人工断言。确认 diff 后才从 `--print-digests` 取新的需求与实现摘要，显式更新 `cases.yml`。
6. 运行生成器写入用例文档，再运行 `--check` 和实现命令。Agent 生成代码与人工代码使用完全相同的
   编译、静态、溯源、执行和审阅门禁。

```bash
npm run test-cases:digests
npm run test-cases:review
npm run test-cases:generate
npm run test-cases:check
```

`requirement_scope` 使迁移可以逐主题、逐里程碑进行，但范围是显式的：进入 scope 的自动验证需求不能
被跳过。一个需求可以由多个用例验证，一个业务用例也可以覆盖多个需求；标注 `e2e` 的需求至少要有
一个 `level: e2e` 用例。

## Schema

顶层固定使用 `api_version: anas.test-cases/v2`，包含主题、标题、需求/计划路径、采用范围和用例列表。
active 用例必须声明：

- `id`、`title`、`level`、`requirements`、`requirement_digest` 和 `implementation_digest`；
- `fixture`、`capabilities`、`preconditions`、`steps` 和 `timeout`；
- `implementation.files` 与可发现的 `implementation.commands`；
- 外部可观察的 `assertions`、结构化 `oracle.sources`、`negative_cases`、`cleanup` 和 `sensitive_data`；
- `validity.method`，以及自动变异/反例/故障注入的命令与证据，或不可自动化时的人工复核理由。

`oracle.sources` 可以包含 `exit-status`、`logs`、`generation` 作为辅助证据，但至少还要包含 API、数据库、
文件、网络、UI、运行时、报告、返回值或错误契约之一。涉及拒绝、安全、回滚、故障、降级或恢复的需求，
必须由至少一个带 `negative_cases` 的 active 用例覆盖。`validity.method` 只能是 `mutation`、
`counterexample`、`fault-injection` 或 `manual`；前三者必须提供可发现命令和行为破坏证据，`manual`
必须说明无法自动化的具体原因。

解析使用严格字段模式；未知字段、重复 ID、越界路径、不存在的脚本、未知 npm script、错误 Go package、
缺失双向标记、陈旧需求/实现摘要、弱 oracle、风险需求缺少反例、有效性证据不完整或生成 README 漂移
都会使门禁失败。当前可发现命令入口为
`npm run <script>`、`go test <repo-package>`、仓库相对可执行脚本，以及显式 `bash` / `sh` /
`python3` / `node` 脚本。

审阅时还必须确认 mock/fixture 没有复制被测实现的决策逻辑。该项不能由通用静态规则可靠判断，
`test-cases:review` 因此在三层 diff 后固定输出人工审阅项；不能自动做变异或反例验证时，理由必须进入
catalog，不能只留在对话里。

详细约束见[文档驱动测试生成与远程执行要求](../../dev-docs/requirements/document-driven-test-automation.md)，
实施状态见[配套计划](../../dev-docs/plans/document-driven-test-automation.md)。
