# Module 文档生成标准

本标准规定 Module 文档的源文件、必需内容、自动生成边界、VitePress 映射、双语要求和 CI 验证。关键词“必须”“不得”“应该”具有规范性；新建 Module、升级 Module 或修改生成器时都必须遵守。

> [!IMPORTANT]
> `cmd/gen-module-docs` 维护 README 内的时区/语言标记块和 localization 汇总；`cmd/materialize-module-docs` 在 VitePress 构建使用的临时源目录中生成逐 Module 页面、目录、导航数据和有限的版本历史。生成页面不写回或提交到正式 `docs/`。

## 1. 源文件结构

每个包含 `module.yml` 的 `modules/<name>/` 目录必须包含：

```text
modules/<name>/
├── module.yml
├── localization.yml
├── README.md
├── README.en.md
└── docs/
    ├── technical.md
    └── technical.en.md
```

各文件职责如下：

| 文件 | 读者 | 职责 |
| --- | --- | --- |
| `module.yml` | Runner、生成器 | 版本、状态、依赖、配置、数据库、IAM、管理员入口和其他机器契约 |
| `localization.yml` | 生成器 | 与当前版本绑定的时区、语言、fallback 和证据 |
| `README.md` | 中文用户和管理员 | 安装依赖、配置、登录、用户管理、故障恢复和日常命令 |
| `README.en.md` | 英文用户和管理员 | 与中文 README 等价的英文说明 |
| `docs/technical.md` | 中文维护者 | 实现、安全边界、Secret、数据流、Hook、Compose 和测试 |
| `docs/technical.en.md` | 英文维护者 | 与中文技术文档等价的英文说明 |

Module 目录是唯一事实来源。逐 Module VitePress 页面只存在于临时构建目录和最终静态产物中，不得反向作为 Module 实现或源文档的依据。

## 2. 事实来源与优先级

文档事实按以下顺序确认：

1. Runner、配置解析、CLI 实现和测试；
2. `module.yml`、Compose、Hook、脚本和 Dockerfile；
3. Contract manifest、schema、Provider/Consumer 实现和测试；
4. 固定版本的上游源码、版本化官方文档和真实容器验证；
5. 既有文档，仅用于线索，不能覆盖当前实现。

不得根据名称推断能力。例如，出现 LDAP 环境变量不能自动证明支持用户同步、Group 同步或密码回写；必须由 manifest、Hook、测试或运行证据支持。不存在的 CLI 命令必须明确写“当前不支持”，不得用提案语法冒充可执行命令。

## 3. README 用户文档必需内容

中英文 README 必须具有等价章节，并至少包含：

1. 快速信息：Module 名、版本/revision、状态、类别和运行时；
2. 依赖：Module、Capability、Contract、接口和版本约束；
3. 最简配置：符合当前 `config.yml` schema 的可复制示例；
4. 身份与用户管理：支持的 LDAPS/OIDC/SAML/Kerberos 等协议，用户和 Group 的事实来源、同步方向、过滤规则和密码回写能力；
5. 管理员登录与 IAM 故障恢复：日常登录、直接入口、私有/本地管理员、IAM 故障时的恢复路径；
6. 管理命令：查询账号、获取凭据、轮换密码、查看配置、修改配置和计划变更的当前真实命令；
7. 数据库支持：Provider/Consumer/无数据库、支持接口、默认接口、Resource、凭据和删除策略；
8. 全部配置参数；
9. 存储、备份、验证、当前限制和技术文档链接；
10. 版本化的时区与语言生成块。

如果 Module 没有 IAM、数据库、本地管理员或密码回写能力，也必须保留相应章节并明确写“不支持/不适用”，不能省略后让读者猜测。

### 配置参数表

README 必须列出 `anas config list <module> --json` 返回的全部参数。每一项至少记录：

| 字段 | 要求 |
| --- | --- |
| 路径 | 完整配置路径，例如 `nextcloud.db_type` |
| 类型 | CLI 实际接受的类型 |
| 默认值 | 当前默认值；无默认值写 `—` |
| 环境变量 | 渲染后的 Module 私有环境变量名 |
| 必填 | 是否 required |
| 敏感 | 是否属于 Secret |
| 可编辑性 | 是否可由普通 `config set` 修改；不可编辑时记录专用生命周期操作 |
| 影响 | `hot_reload`、`container_recreate`、`credential_rotate`、`data_migrate`、`immutable` 等 |
| 作用 | 对应用、数据或网络的实际影响 |

参数不得只出现在技术文档中。新增、删除、改名或改变默认值时，中英文 README 和技术文档必须在同一变更中更新。

### 管理员与密码命令

只有 `module.yml` 已声明且实现 handler 的本地管理员账号，才能记录：

```bash
anas admin local list -w /srv/anas
anas admin local credential <module> <account-id> -w /srv/anas
anas admin local rotate <module> <account-id> -w /srv/anas
anas admin local rotate <module> <account-id> --prompt -w /srv/anas
```

文档必须说明 `credential` 输出明文、`rotate` 是否事务化、`--prompt` 如何读取密码，以及失败后的旧凭据是否仍有效。未声明本地账号的 Module 必须写明这些命令不可用。

不得在示例中把密码放进 argv、普通环境变量或 shell history。`anas config set` 不能被写成应用内部密码轮换或目录用户密码修改命令。

## 4. 技术文档必需内容

中英文 `docs/technical*.md` 必须至少包含：

1. 实现版本、状态和适用范围；
2. Module、Capability、Contract 依赖；
3. Compose service、镜像/build、网络和 volume 拓扑；
4. 与 README 一致的完整配置契约；
5. 用户、Group、LDAPS、OIDC/SAML、身份锚点和密码回写数据流；
6. 管理入口、本地管理员和 IAM 故障恢复的实现；
7. Secret 生命周期、存储格式、权限、投影路径、hash/明文边界和日志边界；
8. 数据库 Contract、Resource identity、Provider/Consumer、凭据和删除策略；
9. 导出与显式消费的环境变量，禁止把依赖闭包误写成无限环境变量授权；
10. Hook、变更执行器、事务、回滚和失败补偿；
11. 实现文件、单元测试、集成/E2E 入口和当前限制。

技术文档必须放在 Module 自己的 `docs/` 中维护。ANAS 核心文档站只发布镜像，不接管 Module 的实现语义。

## 5. 自动生成与人工审核边界

下列内容可以从机器事实生成或校验：

- Module 名、版本、revision、status、category 和 runtime；
- Module/Capability/Contract 依赖；
- 配置路径、类型、默认值、环境变量、required、sensitive、editable、effect 和 apply；
- 数据库和管理员账号的 manifest 声明；
- localization 清单与证据链接；
- Compose service、network、volume 和 Hook 文件索引；
- VitePress 页面、Module 目录和导航链接。

下列内容必须人工或由 AI 分析后审核：

- 用户同步方向、Group 授权语义和撤权行为；
- 密码回写 ACL、管理员恢复安全边界和登录可用性；
- 数据迁移、凭据轮换、事务、回滚和备份恢复语义；
- 上游功能是否真的被当前镜像、配置和 Hook 启用；
- 未实现项、风险和运行时验证结论。

静态分析不能证明真实登录、同步、轮换或恢复已经成功。此类结论必须绑定测试或运行证据，不能仅由生成器推断。

## 6. 生成标记

生成器只能修改明确的生成标记块。现有 localization 标记为：

```markdown
<!-- generated:localization:start -->
...
<!-- generated:localization:end -->
```

标记外内容属于人工审核区。新增生成块必须使用唯一名称 `generated:<section>`，同时支持缺失、重复、反向和不平衡标记检查。不得用生成器覆盖整份人工 README。

## 7. VitePress 输出规则

文档构建必须在临时 VitePress 源目录中执行以下映射：

| Module 源文件 | 中文站点输出 | 英文站点输出 |
| --- | --- | --- |
| `README.md` / `README.en.md` | `/reference/modules/<name>/` | `/en/reference/modules/<name>/` |
| `docs/technical.md` / `docs/technical.en.md` | `/reference/modules/<name>/technical` | `/en/reference/modules/<name>/technical` |

同时必须生成或更新：

- `/reference/modules` 与 `/en/reference/modules`：从所有 `module.yml` 生成的目录；
- `docs/reference/module-localization.md` 与英文镜像；
- 临时的 `.vitepress/generated/module-docs.json`，供中英文侧边栏和版本链接使用。

VitePress 输出必须带“由 Module 源文档生成，请勿直接编辑”提示。生成器必须改写 README 与 technical 之间的相对链接，保证源目录和站点镜像都无死链。技术页面可以通过用户页面进入，不要求在侧边栏平铺全部技术页。

Module 目录和侧边栏中的名称、状态、类别、版本和链接必须来自同一排序后的 manifest 清单，不得再人工维护 README 路径映射。正式 Module 集合与 `.github/modules.json` 必须一致。

历史页面来自不可变 tag `module/<name>/<version>-r<revision>`。每个 Module 先对同一 version 只取最大 revision，再按 SemVer 倒序取最近五个 version。五个候选版本的中英文用户与技术正文经过换行、尾空格和纯发布标识规范化后计算组合 SHA-256；正文完全一致时只保留较新版本，并为较旧版本记录别名。缺少四份完整源文档的早期 tag 不得用当前文件补齐，也不生成历史正文。

## 8. 生成器行为

两个 Module 文档命令分工如下：

1. 枚举所有包含 `module.yml` 的目录，而不是维护硬编码名单；
2. 校验目录名、manifest name、localization module 和文档归属一致；
3. `materialize-module-docs` 在缺少四份双语源文档或 `localization.yml` 时失败；
4. 更新允许的生成标记块；
5. `materialize-module-docs` 在临时目录中一次性生成全部中英文 VitePress 页面、目录和导航数据；
6. 使用确定性排序和稳定格式；
7. `gen-module-docs --check` 不写文件，并在源生成块或 localization 汇总过期时失败；
8. 不删除或覆盖标记外的人工内容。

生成与检查命令：

```bash
go run ./cmd/gen-module-docs
go run ./cmd/gen-module-docs --check
npm run docs:build
```

`npm run docs:build` 复制 `docs/` 到临时目录，运行 `materialize-module-docs`，再由 VitePress 构建；它不会在工作树留下逐 Module 镜像。`npm run docs:dev` 使用相同的临时物化流程。

## 9. localization 清单规则

`localization.yml` 使用 `anas.module-localization/v1`。`module_version` 和 `module_revision` 必须与 `module.yml` 完全一致，`reviewed_at` 是实际复核日期而不是生成日期。

`language.status` 只能是 `supported`、`fixed` 或 `not_applicable`；`language.selection` 使用 `browser`、`integration`、`application`、`deployment_default`、`fixed`、`client` 或 `none`。`global_default` 和 `global_locale` 使用 `applied`、`fallback`、`not_consumed` 或 `not_applicable`。

支持语言使用规范 BCP 47。上游的 `zh_CN`、`pt-br` 或 POSIX locale 写入 `upstream_format`。证据必须绑定当前固定版本，优先级为：版本化源码、版本化官方文档、当前镜像运行时检查、官方宣传页。

`selection: browser` 表示应用继续读取用户或浏览器偏好。未知语言应遵循声明的 fallback；脚本变体不得交叉回退，例如 `zh-Hant` 不得静默匹配 `zh-Hans`。详细升级复核流程见 [Module 上游升级 SOP](/developer/module-upgrade-sop)。

## 10. CI 与提交验收

CI 必须在 VitePress 构建前执行：

```bash
go run ./cmd/gen-module-docs --check
npm run docs:build
```

涉及运行行为的变更还必须执行相应 Module 单测和集成/E2E。提交只包含 Module 源文档及允许提交的 localization 汇总，不提交逐 Module VitePress 镜像。

验收清单：

- [ ] 所有 `module.yml` 目录都有四份双语文档和 `localization.yml`；
- [ ] 中英文结构、命令、默认值、支持状态和风险一致；
- [ ] 配置表覆盖 `anas config list <module> --json` 的全部参数；
- [ ] IAM、LDAPS、Group、管理员、数据库和不支持项都有明确结论；
- [ ] 敏感值没有进入示例 argv、日志或普通环境变量；
- [ ] 生成标记、临时页面、目录、链接和导航没有漂移；
- [ ] 生成器 `--check`、相关测试和 `npm run docs:build` 通过。
