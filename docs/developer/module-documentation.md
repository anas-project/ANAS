# Module 文档规范

本规范定义每个 Module 必须维护的版本、时区、语言和上游证据，以及如何生成 README 和汇总参考页。

## 必需文件

每个包含 `module.yml` 的 `modules/<name>/` 目录必须同时包含：

- `README.md`：Module 的人工说明；时区与语言小节由工具生成；
- `localization.yml`：当前 Module 版本的机器可读时区与语言清单。

`localization.yml` 是语言事实的单一来源。Module README 中以下标记之间的内容不得手工修改：

```markdown
<!-- generated:localization:start -->
...
<!-- generated:localization:end -->
```

`module.yml` 还必须显式声明顶层 `status`，Runner 只接受：

- `release`：达到发布质量，允许作为推荐部署的默认候选；
- `developing`：功能或 E2E 尚不完整，只能用于开发和显式测试部署；
- `deprecated`：仍保留用于迁移或读取现有部署，但不得用于新部署，必须给出替代方案。

状态不得由缺省值推断，也不再接受含义含混的 `experimental`。从 `developing` 提升到
`release` 前必须完成该 Module 的功能、升级/恢复和集成 E2E；标记 `deprecated` 前必须
先提供迁移文档。

运行以下命令生成所有 Module README 和中英文汇总页：

```bash
go run ./cmd/gen-module-docs
go run ./cmd/gen-module-docs --check
```

第二条命令不修改文件，适合 CI 和提交前检查。

生成器把 `docs/reference/module-localization.md` 与 `docs/en/reference/module-localization.md` 视为不可拆分的输出对：一次运行同步生成，任一缺失或过期都会使 `--check` 失败。AI Agent 修改生成器或清单时必须检查两种语言的结果和导航，不得只提交中文页。

## `localization.yml` 结构

```yaml
api_version: anas.module-localization/v1
module: example
module_version: 1.2.3
module_revision: 1
reviewed_at: 2026-08-13
timezone:
  status: container
  mechanism: All long-running services receive TZ.
language:
  status: supported
  scope: Example Web UI
  selection: browser
  global_default: not_consumed
  global_locale: not_consumed
  upstream_format: BCP 47
  fallback: Browser preference, then deployment default, then English.
  supported: [de, en, zh-Hans, zh-Hant]
  evidence:
    - version: 1.2.3
      url: https://github.com/example/example/blob/v1.2.3/locales.json
      path: locales.json keys
  notes: The API service itself has no UI language.
```

字段规则：

- `module_version` 和 `module_revision` 必须与 `module.yml` 完全相同；主应用或附带组件升级但未复核会使检查失败。
- `reviewed_at` 是实际复核日期，不是生成日期。
- `timezone.status` 描述 `container`、`application`、`system`、`partial` 等真实边界，`mechanism` 必须指出哪些进程接收 `TZ` 或使用哪个应用设置。
- `language.status` 只能是 `supported`、`fixed` 或 `not_applicable`。
- `language.selection` 使用 `browser`、`integration`、`application`、`deployment_default`、`fixed`、`client` 或 `none`。
- `global_default` 和 `global_locale` 使用 `applied`、`fallback`、`not_consumed` 或 `not_applicable`，记录全局值是否被应用真正消费，不能仅凭变量出现在 `.env` 就写成生效。
- `supported` 使用规范化 BCP 47；上游的 `zh_CN`、`pt-br`、POSIX locale 等原始格式写在 `upstream_format`，运行时通过转换库适配。
- `evidence` 必须绑定当前固定版本，优先使用 tag/版本分支下的源码文件或官方版本文档，不能只链接滚动的宣传首页。

## 语言选择与不支持语言

`selection: browser` 表示应用继续读取用户设置或浏览器 `Accept-Language`。ANAS 不强制覆盖它。只有 `global_default: fallback` 的应用才把 `global.default_language` 用作部署回退；`not_consumed` 表示上游只使用自己的 fallback。Nextcloud 这类同时区分 UI 语言和区域格式的应用，还使用 `global.default_locale`，不得用一个字段混合两个概念。

省略 `global.default_locale` 时可以从语言推导，但仅限显式地区：`en-GB` 可直接成为 locale，`en` 或 `zh-Hans` 必须先尝试宿主 locale，最后才由 `x/text` 的 CLDR likely-subtag 补全。Module 不应自行重复这套推导。

Module 需要显式语言参数时，输入边界统一使用 BCP 47，并通过 `internal/localization` 转为上游格式。处理顺序为：

1. 显式 Module 值能够匹配当前清单：转换并使用；
2. 显式 Module 值不能匹配：输出 `module_localization_fallback` 告警、使用 `fallback`，但不阻断进程；
3. 继承的全局值不能匹配：同样使用 `fallback`，不阻断进程；
4. 浏览器协商应用：把未知浏览器语言交给上游协商和回退；
5. `fixed` 或 `not_applicable`：不得制造一个实际上无效的语言参数。

脚本变体不能交叉回退，例如 `zh-Hant` 不能匹配到仅有的 `zh-Hans`。语言标签、可能的 locale 拼写和时区校验不得用字符串替换表散落在各 Hook 中。

## 证据优先级

按以下顺序查找支持语言：

1. 当前版本源码中的 locale 目录、翻译 manifest 或资源键；
2. 当前版本官方管理/开发文档；
3. 当前版本镜像内的语言资源、`locale -a` 和应用运行时接口；
4. 官方宣传页，仅用于补充，不用于替代版本化清单。

浏览器中出现语言选择器并不能证明镜像包含所有上游语言包；必须以当前 Module 使用的版本和构建产物为准。升级复核流程见 [Module 上游升级 SOP](/developer/module-upgrade-sop)。
