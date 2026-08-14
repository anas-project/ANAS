# Contract 文档生成规范

本规范对齐 [Module 文档规范](/developer/module-documentation)，定义 Contract 根目录文档、机器事实、人工语义和 VitePress 页面如何共同维护。

## 必需文件与单一来源

每个 `contracts/<name>/` 必须包含：

- `contract.yml`：版本、接口、资源标识、operation 与 schema 路径的机器契约；
- `schemas/*.yml`：资源、请求和结果字段；
- `documentation.yml`：实现状态、复核日期和中英文摘要；
- `README.md`：中文语义与运维说明；
- `README.en.md`：英文语义与运维说明。

README 必须位于 Contract 根目录，因为 Contract 应能离开 ANAS 文档站单独阅读。VitePress 页面 `docs/reference/contracts/<name>.md` 与 `docs/en/reference/contracts/<name>.md` 是生成镜像，不是新的事实源。

运行：

```bash
go run ./cmd/gen-contract-docs
go run ./cmd/gen-contract-docs --check
```

第二条命令不修改文件，适合 CI。生成器把根目录中英文 README 和 VitePress 中英文页面视为一个不可拆分输出集，缺失或过期都会失败。

## `documentation.yml`

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

- `implemented`：Runner 已调度、至少一个 provider 实现、真实 E2E 已验证；
- `partial`：只有部分 operation/interface 或 provider/consumer 链路实现；
- `pending`：契约已定义，但当前 Runner 不调度；
- `proposal`：仍需 ADR 或必要性评审；
- `deprecated`：保留兼容但不得新增使用方。

存在 `contract.yml`、schema 或 Module 名称并不能自动推断 `implemented`。

## 生成内容与人工内容

以下内容可从代码可靠生成，位于 `generated:contract-reference` 标记之间：

- Contract 版本、status、复核日期、interfaces；
- resource identity 和 resource schema；
- operations 的 required、request schema、result schema；
- 每个 schema 的类型、required 字段、全部字段；
- Module manifests 当前声明的 provider、consumer、版本约束、接口和实现入口。

以下内容必须在标记外人工或由 AI 分析后审核：

- 业务目的、资源身份语义和幂等边界；
- 生命周期顺序、事务、验证与失败补偿；
- Secret、权限、日志和信任边界；
- 兼容性、版本升级和删除策略；
- 当前限制、未实现项、运行示例和故障恢复。

代码和既有文档可以生成初稿，但无法仅靠静态分析证明应用内状态已更新、登录真实可用、回滚可靠或安全意图正确；因此人工复核的意义是把这些运行时/语义断言绑定到测试和设计决定，而不是重复抄字段。

## 双语、证据与 CI

中英文 README 章节结构必须一致，技术 ID、schema path、operation、CLI 命令和状态值保持原文不翻译。修改 Contract 时必须同时运行生成器、Contract 测试和文档构建：

```bash
go run ./cmd/gen-contract-docs --check
test-env/scripts/test-contract.sh
npm run docs:build
```

实现状态提升到 `implemented` 时，提交必须同时包含 provider/consumer manifests、Runner 调度、operation 单测和真实服务 E2E 的证据。
