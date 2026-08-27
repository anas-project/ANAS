# Test case catalogs

`test-env/cases/<topic>/cases.yml` 是需求到测试实现之间的机器可读验证契约；同目录 `README.md` 由
`cases.yml` 生成，只供审阅，不得手改。`topic` 必须与成对的
`docs/requirements/<topic>.md`、`docs/plans/<topic>.md` 以及目录名一致。

## 工作流

1. 在需求矩阵中写稳定、原子且可证伪的需求，并在计划中分配唯一里程碑。
2. 把本轮采用的需求 ID 放入 `requirement_scope`，为每个自动验证需求创建至少一个 active 用例。
3. 用例使用需求前缀的 `<PREFIX>-T-<三位序号>`；ID 永久不复用。废弃用例保留原 ID、标为
   `retired` 并列出 active `replaced_by`。
4. 测试实现文件用语言对应的注释声明 `TEST_CASES: <ID>[, <ID>...]`；catalog 必须反向列出该文件。
5. 运行生成器写入用例文档，再运行 `--check`。需求正文或验证方式改变后，旧摘要会失败；审阅变更后
   从 `--print-digests` 取新摘要并显式更新 `cases.yml`。

```bash
npm run test-cases:digests
npm run test-cases:generate
npm run test-cases:check
```

`requirement_scope` 使迁移可以逐主题、逐里程碑进行，但范围是显式的：进入 scope 的自动验证需求不能
被跳过。一个需求可以由多个用例验证，一个业务用例也可以覆盖多个需求；标注 `e2e` 的需求至少要有
一个 `level: e2e` 用例。

## Schema

顶层固定使用 `api_version: anas.test-cases/v1`，包含主题、标题、需求/计划路径、采用范围和用例列表。
active 用例必须声明：

- `id`、`title`、`level`、`requirements` 和逐用例 `requirement_digest`；
- `fixture`、`capabilities`、`preconditions`、`steps` 和 `timeout`；
- `implementation.files` 与可发现的 `implementation.commands`；
- 外部可观察的 `assertions`、`negative_cases`、`cleanup` 和 `sensitive_data`。

解析使用严格字段模式；未知字段、重复 ID、越界路径、不存在的脚本、未知 npm script、错误 Go package、
缺失双向标记、陈旧需求摘要或生成 README 漂移都会使门禁失败。当前可发现命令入口为
`npm run <script>`、`go test <repo-package>`、仓库相对可执行脚本，以及显式 `bash` / `sh` /
`python3` / `node` 脚本。

详细约束见[文档驱动测试生成与远程执行要求](../../docs/requirements/document-driven-test-automation.md)，
实施状态见[配套计划](../../docs/plans/document-driven-test-automation.md)。
