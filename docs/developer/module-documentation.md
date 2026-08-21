# Module 文档生成标准

本标准规定 Module 文档的源文件、必需内容、自动生成边界、VitePress 映射、双语要求和 CI 验证。关键词“必须”“不得”“应该”具有规范性；新建 Module、升级 Module 或修改生成器时都必须遵守。

> [!IMPORTANT]
> `cmd/gen-module-docs` 维护 README 的快速信息与时区/语言标记块、技术文档的实现身份与 Compose 拓扑标记块，以及 localization 汇总；`cmd/materialize-module-docs` 在 VitePress 构建使用的临时源目录中生成逐 Module 页面、目录、导航数据和有限的版本历史。生成页面不写回或提交到正式 `docs/`。

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
4. 身份与用户管理：支持的 LDAPS/OIDC/SAML/Kerberos 等协议，用户和 Group 的事实来源、同步方向、过滤规则、密码回写能力，以及应用发起/IAM 发起两个方向的登出能力；
5. 管理员登录与 IAM 故障恢复：日常登录、直接入口、私有/本地管理员、IAM 故障时的恢复路径；有应急账号时必须给出登录地址、实际用户名和密码获取命令；
6. 管理命令：查询账号、获取凭据、轮换密码、查看配置、修改配置和计划变更的当前真实命令；
7. 数据库支持：Provider/Consumer/无数据库、支持接口、默认接口、Resource、凭据和删除策略；
8. 全部配置参数；
9. 存储、备份、验证、当前限制和技术文档链接；
10. 版本化的时区与语言生成块。

如果 Module 没有 IAM、数据库、本地管理员或密码回写能力，也必须保留相应章节并明确写“不支持/不适用”，不能省略后让读者猜测。

### IAM 登出说明

使用 OIDC 或 SAML 的 Module，其“身份与用户管理”章节必须另外记录：

1. 用户从 Module 点击退出时，是只清本地会话，还是继续执行 OIDC RP-Initiated Logout / SAML SP-Initiated SLO；
2. 用户从 IAM Portal 退出时，Module 是否接受 OIDC front-/back-channel 或 SAML IdP-Initiated SLO；
3. 管理员无用户浏览器参与地撤销 IAM session 时，应用会话是否失效；
4. 实际登记的 method/binding、endpoint 类型和固定插件/应用版本，不能记录 Secret 或 token；
5. 按 `sid`、`sub`、`NameID/SessionIndex` 的撤销范围及浏览器、SameSite/CSP、TTL、重试限制；
6. 真实会话 E2E 的脚本/测试入口和未覆盖项。

`post_logout_redirect_uri` 只能写成退出后导航，不得描述为 IAM 通知应用的 endpoint。只有
满足[使用 OIDC/SAML 的 Module 双向登出要求](/requirements/module-iam-bidirectional-logout)
并具备真实会话证据时，README 才能使用“支持双向登出”或“支持后台撤销”。经
ForwardAuth 接入的 Module 必须分别说明 IAM、认证网关和后端应用会话，不能把网关退出能力
投影为后端能力。

### 配置参数表

README 必须列出 `anas config list <module> --json` 返回的全部参数。每一项至少记录：

| 字段 | 要求 |
| --- | --- |
| 路径 | 完整配置路径，例如 `nextcloud.db_type` |
| 类型 | `config.types` 显式声明且 CLI 实际接受的类型；不得从默认值推断 |
| 默认值 | 当前静态默认值；无默认值写 `—`，明确的空字符串写 `""`，不得把两者混为一谈 |
| 默认来源 | `default_source`；取 `none`、`static`、`host`、`runtime`、`generated` 或 `inherited` |
| 环境变量 | 渲染后的 Module 私有环境变量名 |
| 输入必填 | `input_required`；`required` 是与它完全相等的兼容别名 |
| 解析必有 | `must_resolve`；应用默认值及其他来源后，最终值是否必须非空 |
| 单字段约束 | `constraints` 中适用的范围、长度、pattern 或 format；没有时写 `—` |
| 敏感 | 是否属于 Secret |
| 可编辑性 | 是否可由普通 `config set` 修改；不可编辑时记录专用生命周期操作 |
| 影响 | `hot_reload`、`container_recreate`、`credential_rotate`、`data_migrate`、`immutable` 等 |
| 作用 | 对应用、数据或网络的实际影响 |

内置 Module 的每个可配置参数都必须显式声明 `config.types`。`unknown` 只用于兼容旧 Module
或暴露开发中的不完整声明，release gate 必须拒绝内置清单中的任何 `unknown`。

`input_required` 只回答“操作者是否必须输入”：有静态默认值或 host/runtime/generated/
inherited 无条件来源时必须为 `false`，并由 `has_default` 区分无默认值与空字符串默认值。
`must_resolve` 回答“解析后是否必须有非空值”，可以与 `input_required: false` 同时成立。
存在条件 resolver 时，`default_source: none` 也可以与这组值同时出现；它不表示操作者在所有
场景都必须直接填写 Module 参数。
manifest 的 `input_required` 声明“进入解析流程前必须由调用者显式提供”；旧字段
`required` 为兼容已有 Module 保留，仍在 defaults/resolver 完成后、calculate Hook 前检查，
可以由默认值或 resolver 满足；`must_resolve` 在 Hook patch 后检查最终非空。CLI/JSON 的
`required` 不是 manifest 旧字段的直接投影，而始终是 `input_required` 的兼容别名。

`constraints` 只声明单字段规则：整数的 `minimum`/`maximum`，字符串的 `min_length`/
`max_length`/`pattern`/`format`。条件性要求、跨字段关系和依赖运行态的规则必须在“作用”中
写清，并由 resolver、统一应用层、plan 或 Hook 校验，不能为了驱动表单而把参数笼统标成 required。
这些元数据是 ANAS schema，不是 JSON Schema：两者的 `required` 和 `default` 语义不同。
Module 只维护声明和通用校验规则；M3 的 `anasd` 配置 API 复用统一 schema，绝不增加逐
Module 的 HTTP 适配。

参数不得只出现在技术文档中。新增、删除、改名、改变类型/required 或改变默认值时，中英文
README 和技术文档必须在同一变更中更新。

配置表位于人工审核区。`gen-module-docs` 会用统一参数库存校验四张表的机器派生列是否一致，
但不得在没有生成标记时改写表格；“作用”列及跨字段、迁移和安全语义始终由维护者审核。

### 管理员与密码命令

只有 `module.yml` 已声明且实现 handler 的本地管理员账号，才能记录：

```bash
anas admin local list -w /srv/anas
anas admin local credential <module> <account-id> -w /srv/anas
anas admin local rotate <module> <account-id> -w /srv/anas
anas admin local rotate <module> <account-id> --prompt -w /srv/anas
```

文档必须说明 `credential` 输出明文、`rotate` 是否事务化、`--prompt` 如何读取密码，以及失败后的旧凭据是否仍有效。未声明本地账号的 Module 必须写明这些命令不可用。

应急账号说明必须能让操作者不再查源码即可登录，至少包含：完整登录 URL，或
`<MODULE_DOMAIN_FULL>` 与固定 path 的明确组合；Manifest 账号 ID 和 purpose；实际物理用户名
及其是上游固定值还是 `admin_{module}` 模板；精确的 `anas admin local credential` 取密命令。
文档只能记录取密方法，不能记录生成后的密码值。若没有应急账号，必须明确写出不存在应用内
恢复入口，以及应恢复 IAM、目录还是通过宿主机执行其他真实流程。

README 和技术文档展示的 revision 是发布状态，不是工作树变化计数。普通功能、修复或文档
修改不得手工提升 revision；`image-release` 发布流程负责计算并写回正式 revision。只有本地
E2E 为构建独立测试镜像确实需要新标签时，才临时同步修改 `module.yml`、`localization.yml`、
Compose 和生成块中的 revision；不需要新镜像的测试不得修改。

不得在示例中把密码放进 argv、普通环境变量或 shell history。`anas config set` 不能被写成应用内部密码轮换或目录用户密码修改命令。

## 4. 技术文档必需内容

中英文 `docs/technical*.md` 必须至少包含：

1. 实现版本、状态和适用范围；
2. Module、Capability、Contract 依赖；
3. Compose service、镜像/build、网络和 volume 拓扑；
4. 与 README 一致的完整配置契约；
5. 用户、Group、LDAPS、OIDC/SAML、身份锚点、密码回写，以及 RP/SP 发起和 IAM 发起登出的会话数据流；
6. 管理入口、本地管理员和 IAM 故障恢复的实现；
7. Secret 生命周期、存储格式、权限、投影路径、hash/明文边界和日志边界；
8. 数据库 Contract、Resource identity、Provider/Consumer、凭据和删除策略；
9. 导出与显式消费的环境变量，禁止把依赖闭包误写成无限环境变量授权；
10. Hook、变更执行器、事务、回滚和失败补偿；
11. 实现文件、单元测试、集成/E2E 入口和当前限制。

技术文档的登出数据流必须画清或写清应用会话、IAM 中央会话、`sid`/`sub` 或
`NameID`/`SessionIndex`、通知 endpoint、签名信任、重放存储和失败降级边界。OIDC 必须说明
Logout Token 的签名/claim 校验；SAML 必须说明 LogoutRequest/LogoutResponse 校验和 binding
限制。只支持 front-channel/Redirect 时必须明确依赖浏览器，不得暗示管理员后台撤销已覆盖。

`config.env_prefix`、`exports` 和 `consumes` 必须使用环境变量安全的 upper-snake 命名；通配
只允许一个前置或末尾 `*`，且禁止裸 `*`。不同 Module 的默认/自定义 prefix 不得相等或
互相嵌套，也不得覆盖 global、runner-owned 或 `ANAS_*` 命名空间。calculate Hook 的
env/Secret patch 必须整体通过 ownership、键规范化和碰撞校验后才应用，不能覆盖其他来源
已拥有的键。`calculate` 与 `render_env` 合并后的已声明参数都重新通过统一 type/constraints
schema；render 私有键仍可不声明。Hook Secret 只能刷新既有 `generated/module-hook` 记录，不能
覆盖 `lifecycle_managed`、`local_admin` 或其他来源记录，整包拒绝也不得回显值或自由 provenance。

### 静态 credential 声明与 lifecycle ABI

Module 必须用显式声明建立机器凭据关系，Core 不按 `PASSWORD`、`TOKEN` 或 `KEY` 名称猜测：

```yaml
credentials:
  provides:
    - id: eturnal.secret
      secret_key: TURN_SECRET
      type: shared_secret
      rotation_mode: reconcile
      generation: {kind: hex, length: 16}
      lifecycle:
        probe: probe-eturnal-secret
        reconcile: reconcile-eturnal-secret
        verify: verify-eturnal-secret
  consumes:
    - credential: eturnal.secret
      projection: TURN_SECRET
```

Provider ID 必须位于自身 Module 的 lower-case dotted namespace；Secret key 与 projection 必须是
upper-snake 环境键，consumer projection 还必须出现在 `config.consumes`。`reconcile` provider
必须声明至少 16 长度的 `password`/`hex` 生成策略、三个 handler，并在 `hook.phases` 中显式列出
`credential_probe`、`credential_reconcile`、`credential_verify`。Provider ID、Secret key、consumer
projection 和 control edge 在 registry admission 时检查唯一性与引用完整性。

Materialization 在 calculate 后从 Secret Store provenance 冻结 `authority` 与 `generation`；
`generated/module-hook` 且 owner 匹配的记录是 `anas`，其他来源是 `external`。deployment manifest
只保存 ID、owner/consumer、authority、generation、projection、handler 和策略，不保存值、hash
或 verifier。Credential consumer 会生成 provider→consumer 激活 edge，即使两者没有普通依赖。

Credential Hook 请求在 `credential` 对象中携带 handler、逻辑 ID、Secret key、authority 和
generation；期望值只放在 stdin JSON 的 `secrets.ANAS_CREDENTIAL_DESIRED`，不放在 metadata、
宿主 argv、错误或 Hook stderr。响应只能返回：

```json
{"credential":{"credential_id":"eturnal.secret","status":"match","changed":false}}
```

Probe/verify 的有效状态为 `match`、`missing`、`mismatch`、`unavailable` 或 `unsupported`；
reconcile 成功返回 `reconciled`（有 mutation）或 `match`（无需 mutation）。`external` authority
只能 probe/verify；不匹配必须要求人工处理。任意普通 Hook mutation 字段、未知 JSON 字段、额外
JSON 值或无效状态都会使 ready barrier 失败。

Module 文档若声明可执行 `reconcile` provider，必须给出当前真实命令：
`anas credential list`、`anas credential rotate <id> --dry-run` 和实际轮换命令，并说明受影响
consumer、停机范围、verify 证明的内容与限制。Core 使用 candidate deployment、无明文 journal、
一次性 Store commit 和自动 crash recovery；文档不得把“存在 Secret Store 值”或只有 probe 的
记录描述为可轮换。未迁入统一执行器的 resource、本地管理员、overlap/migrate 或 external provider
必须明确写为 manual/unsupported，并指向其专用流程。

### Hook phase 与 `validate` ABI

技术文档必须逐项列出 Module 实际实现的 Hook phase，并与 `module.yml` 的声明一致：

```yaml
logic:
  hook:
    command: [go, run, ./hook]
    phases: [validate, calculate, render_env, services, after_start]
```

`logic.hook.phases` 的非空列表是精确 allowlist；可用值为 `validate`、`calculate`、
`render_env`、`runtime_restore`、`services`、`after_start`、`local_account_apply`、
`local_account_rotate`、`local_account_rollback`、`credential_probe`、`credential_reconcile` 和
`credential_verify`。省略该字段只保留旧 Hook 的非 `validate`、非 credential
兼容生命周期，绝不会推断旧 Hook 已实现配置校验或凭据协调；显式空列表因语义含混而被 manifest
admission 拒绝。要接收 `validate` 请求，Module 必须在非空列表中显式声明它。

部署激活按依赖顺序逐个 Module 执行；某个 Module 的容器启动、owned credential
probe/reconcile/verify、`after_start` 和本地管理员协调全部成功后，Runner 才会启动下游 Module。
技术文档不得把 `after_start` 描述成“全部容器启动后异步执行”的通知。

`validate` 在有效拓扑的依赖顺序中运行，只检查期望状态。请求的 `phase` 为 `validate`，
`env` 是 Module scoped 视图且已删除敏感键及其已知等值 alias，`secrets` 永远是空对象，
`workdir` 为空；Hook 进程也只继承运行命令所需的最小环境。这是 ABI 的数据和响应边界，
不是操作系统 sandbox，Module Hook 仍按受信任代码审核。

`validate` response 只有 `plan` 与 `warnings` 可以非空。任何 `env`、`secrets`、`files`、
`runtime_files`、`disable_services`、`docker_copies` 或 `internal_env` mutation 都拒绝整个校验；
未知 JSON 字段、多个 JSON 值和无效 JSON 同样拒绝。`warnings` 表示可恢复问题，不使校验失败，
Runner 以 `module_validation_warning` 输出；内容必须保持非敏感。

`plan` 是只读、非敏感的 `string -> string` 元数据，不会回写配置或环境。每个 Module 最多
64 项；key 不做 trim 或大小写规范化，必须直接匹配 `^[a-z][a-z0-9_]*$`，且不得指向敏感
参数。value 会 trim，必须非空、不超过 1024 bytes、不得含控制字符，也不得等于
Core 已知的 Secret 明文。通过校验的值出现在 deployment/config plan 的
`module_plans.<module>` 中；物化部署时同一份值冻结到 deployment manifest 的 Module
`validation_plan`，因此技术文档不得把 `plan` 描述成临时日志或 mutation 指令。

技术文档必须放在 Module 自己的 `docs/` 中维护。ANAS 核心文档站只发布镜像，不接管 Module 的实现语义。

## 5. 自动生成与人工审核边界

下列内容可以从机器事实生成或校验：

- Module 名、版本、revision、status、category 和 runtime；
- Module/Capability/Contract 依赖；
- 配置路径、类型、默认值及来源、`required`/`input_required`、`must_resolve`、
  `has_default`、单字段 constraints、环境变量、sensitive、editable、effect 和 apply；
- 内置 Module 参数的类型声明完整性；完整 release 验收还要求 `config list --json` 中
  `type: "unknown"` 的数量为 0；
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

生成器只能修改明确的生成标记块。当前标记为：

```markdown
<!-- generated:module-facts:start -->
...
<!-- generated:module-facts:end -->

<!-- generated:localization:start -->
...
<!-- generated:localization:end -->

<!-- generated:module-identity:start -->
...
<!-- generated:module-identity:end -->

<!-- generated:compose-topology:start -->
...
<!-- generated:compose-topology:end -->
```

`module-facts` 与 `module-identity` 来自 `module.yml`，`compose-topology` 来自 manifest 指定的 Compose 文件，`localization` 来自 `localization.yml`。标记外内容属于人工审核区。新增生成块必须使用唯一名称 `generated:<section>`，同时支持缺失、重复、反向和不平衡标记检查。不得用生成器覆盖整份人工 README。

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
4. 在内存中预检四份源文档的全部标记，再更新允许的生成标记块；缺失、重复、反向或不平衡标记不得造成部分写入；
5. `materialize-module-docs` 在临时目录中一次性生成全部中英文 VitePress 页面、目录和导航数据；
6. 使用确定性排序和稳定格式；
7. `gen-module-docs --check` 不写文件，并在源生成块、localization 汇总、四份参数表的
   机器派生列过期，或任一内置 Module 参数缺少显式类型时失败；
8. Module 类型审计覆盖 `required`、`defaults`、`types`、`changes` 的并集，不能只检查有
   默认值的参数；global 参数由 runner 的完整 inventory 验收覆盖；
9. 不删除或覆盖标记外的人工内容。

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

截至 2026-08-21，内置发布门已经固定 19 个 Module、146 个参数、`unknown=0`、2 个
`input_required` 和 23 个最终 must-resolve 参数，并精确比对 14 项已声明 constraints。
测试还必须证明通用 set/import/plan/lock/apply 路径与 calculate/render Hook 使用同一 schema，
Secret Store 各 kind 不会泄露或冒充 caller input，Hook Secret 不能跨 Module 改写。新增 Module
只应改变 manifest/inventory/生成表，不应要求修改 `anasd` 或 HTTP handler。

涉及运行行为的变更还必须执行相应 Module 单测和集成/E2E。提交只包含 Module 源文档及允许提交的 localization 汇总，不提交逐 Module VitePress 镜像。

验收清单：

- [ ] 所有 `module.yml` 目录都有四份双语文档和 `localization.yml`；
- [ ] 中英文结构、命令、默认值、支持状态和风险一致；
- [ ] 配置表覆盖 `anas config list <module> --json` 的全部参数，区分输入必填、解析必有和
      默认来源，且内置清单没有 `type: "unknown"`；
- [ ] 单字段 constraints 与统一 schema 一致；条件/跨字段规则写入作用说明并有 resolver、
      plan/Hook 或应用层验证，未增加逐 Module 的 API 适配；
- [ ] IAM、LDAPS、Group、管理员、数据库和不支持项都有明确结论；
- [ ] 敏感值没有进入示例 argv、日志或普通环境变量；
- [ ] 生成标记、临时页面、目录、链接和导航没有漂移；
- [ ] 生成器 `--check`、相关测试和 `npm run docs:build` 通过。
