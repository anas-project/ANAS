# Module 开发

新建、升级或评审 Module 时，使用 [Module 设计与发布检查表](/developer/module-design-checklist)
统一记录机械门禁、人工评审和真实环境证据。

## 基本职责

Module 是独立的发布和部署单元。它拥有：

- `module.yml` 中的身份、版本、ABI、依赖、能力和配置声明；
- Docker Compose 定义；
- 需要时使用的 Hook；
- 模板、构建上下文和运行资产。

Module 不应依赖仓库检出的相对路径才能启动。冻结后的 deployment 必须携带运行所需的 Compose、环境、文件和 Hook 二进制。

Module 参数的业务语义属于 Module，不属于 Core。跨参数校验放入 `validate` Hook，派生值放入
`calculate` Hook，持久状态协调使用 lifecycle operation/reconciler；不要要求 Core 添加
Module 名称分支或直接改写私有参数。完整边界见 [Core 实现标准](/architecture/core-implementation-standard)。

## 版本与 revision 所有权

`version` 跟随规范化的上游应用版本；`revision` 表示同一上游版本下已经发布的 ANAS 镜像
修订。日常功能、修复和文档提交不得为了“有修改”而手工提升 `revision`。正式值由变更合并到
`image-release` 后的发布流程计算，并同步写回 `module.yml`、`localization.yml`、Compose 镜像
标签和生成文档，再随发布结果回到 Git。

本地唯一需要临时修改 `revision` 的场景，是 E2E 必须编译一个不同于已发布镜像的测试版本。
此时所有 revision 投影必须保持一致；普通功能分支在提交前恢复已发布 revision，由
`image-release` 决定下一个正式编号。只测试 Hook、配置生成或不需要新镜像的用例，不应修改
revision。

## 依赖和能力

- 硬依赖必须显式声明并参与选择与排序；
- 可替代实现通过能力 Provider 绑定；
- 仅排序关系不能隐式选择另一个 Module；
- 持久资源通过 Resource/Provider operation 管理，而不是共享数据库密码约定。

新增或修改 `capabilities.provides`、`dependencies.requires_capabilities`、Runner capability
registry 或 capability binding 时，必须遵守 [Capability 开发标准](/developer/capability-development)。

## 配置与 Secret

只声明 Module 实际消费和导出的配置。不要依赖全量环境注入；生成的 `.env` 应只包含当前 Module、依赖闭包和显式声明的键。敏感值不得写入日志或非必要容器。

开始实现前阅读[Module、Contract 与 Resource 设计](/architecture/module-contract-resource-design)和[环境变量契约](/reference/module-environment-variables)。

## ANAS 管理凭据的轮换范围

ANAS 生成、保存或有权写入应用内部状态的密码、shared secret、client
secret 和签名/加密密钥，都必须记录 owner、consumer、authority 和轮换状态。
声明为可轮换时，发布验收必须覆盖三个作用域：

1. **单目标**：轮换一个逻辑凭据或一个 Module 本地账号；
2. **Module 全部**：按依赖顺序原子轮换某 Module 拥有的全部 ANAS-managed 凭据；
3. **deployment 全部**：用同一 planner/ready barrier 轮换活动部署中所有纳入统一 lifecycle 的凭据。

每个范围都必须说明 candidate 生成、应用内更新、probe/verify、Secret Store 提交、
停机面和失败回滚。多次手工执行单凭据命令不能冒充 Module/deployment 级原子轮换。

当前 CLI 只部分覆盖这个目标：`anas credential rotate <id>`、
`anas credential rotate --module MODULE` 与 `anas credential rotate --all` 分别覆盖
`credentials.provides` 统一 lifecycle 的单目标、Module owner 批次和 deployment 批次；
`anas admin local rotate MODULE [ACCOUNT]` 覆盖单个本地账号。尚无通用的跨凭据类别事务，
`credential rotate --module/--all` 也不包含 Resource credential、本地管理员或
外部 API token。在这些范围实现前，Module 文档和发布检查必须标为 manual/unsupported，
不得宣称“全部 ANAS Secret 可轮换”。

## 使用 OIDC/SAML 的 Module：双向登出设计规范

任何直接消费 `iam` capability 并使用 OIDC 或 SAML 建立应用会话的 Module，都必须在设计
阶段分别回答以下问题：

1. 用户从应用点击退出时，应用会话是否失效，IAM 中央会话是否也失效？
2. 用户从 IAM 退出时，IAM 能否通知应用并使原应用会话失效？
3. 管理员在无用户浏览器参与时撤销 IAM session，应用会话是否立即失效？

详细安全和验收规则以[使用 OIDC/SAML 的 Module 双向登出要求](/requirements/module-iam-bidirectional-logout)
为准。Module 设计必须遵守以下边界。

### Provider-neutral 注册

Module 只读取自己的 `ANAS_IAM_BINDING__<APP>__*`，并在 `calculate` Hook 发布自己的
`ANAS_IAM_CLIENT__<APP>__*`。不得按 Provider 名称分支，也不得生成 LLNG、Authentik、
Casdoor 或其他实现的私有字段。

OIDC Module 支持 IAM 主动登出时发布：

```dotenv
ANAS_IAM_CLIENT__<APP>__POST_LOGOUT_REDIRECT_URIS=https://app.example/logged-out
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_URI=https://app.example/oidc/backchannel-logout
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_METHODS=backchannel,frontchannel
ANAS_IAM_CLIENT__<APP>__OIDC_LOGOUT_SESSION_REQUIRED=true
```

SAML Module 支持 SLO 时发布：

```dotenv
ANAS_IAM_CLIENT__<APP>__SAML_SLS_URL=https://app.example/saml/sls
ANAS_IAM_CLIENT__<APP>__SAML_SLS_BINDINGS=redirect,post
```

URI 与 method/binding 必须成对。`POST_LOGOUT_REDIRECT_URIS` 只是导航允许列表，不能替代
OIDC 通知 endpoint；普通 `/logout` 页面不能冒充 back-channel。协议切换、域名变化和重复
apply 必须清除另一协议或旧域名的残留字段。

### OIDC 实现边界

- Module 发起登出必须通过 discovery 的 `end_session_endpoint`，使用标准
  `id_token_hint`/`client_id`、已登记的 `post_logout_redirect_uri` 和随机 `state`；应用会话
  必须在离开应用前失效。
- IAM 发起登出优先声明 back-channel。Endpoint 必须验证 Logout Token 的签名、算法、
  `iss`、`aud`、`events`、时间、`jti` 防重放和 `sid`/`sub`，并只撤销目标应用会话。
- 只支持 front-channel 时必须明确浏览器、iframe、SameSite/CSP 限制，并标注不支持管理员
  无浏览器后台撤销。
- 上游固定版本没有标准通知 endpoint 时，Module 必须省略 `OIDC_LOGOUT_*`，明确记录“仅
  本地登出”或“只支持应用发起登出”，不得自建一个不符合标准的猜测路径。

### SAML 实现边界

- SP 发起登出必须使用 IAM binding/metadata 发布的 SLO URL，保存并使用登录时的 `NameID`、
  format 和 `SessionIndex`，验证签名、Destination、Issuer、`InResponseTo`、状态和时效。
- IdP 发起登出只有在固定应用版本正式暴露 SLS 时才能声明。Redirect SLS 只保证浏览器参与
  的双向登出；管理员无浏览器撤销必须有正式 POST/back-channel 支持和独立真实 E2E。
- Provider 不发布可选 `SAML_SLO_URL` 时，Module 必须清除旧值并执行本地登出，不能继续
  调用历史 endpoint。

### 发布门禁

双向登出是 `Provider × 协议 × Module` 的组合能力，不是看到 discovery/metadata 或配置字段
就算完成。Module 单元测试必须覆盖字段发布、协议切换、缺项和非法值；真实容器 E2E 必须
保存登出前应用 Cookie，并分别验证应用发起、IAM 发起和声称支持时的管理员无浏览器撤销。
只检查 302、退出页或 token TTL 不算通过。

经 `oauth2-proxy`/ForwardAuth 间接接入的 Module 必须分别说明 IAM 会话、网关 Cookie 和后端
应用会话的失效范围；网关支持退出不自动证明后端应用支持双向登出。

## 管理界面与本地管理员账号

每个有管理界面的 Module 都必须先判断实际登录拓扑，再决定是否声明
`management.local_accounts`。判断的是上游应用真实能力，不是希望它具备的能力：

| 应用能力 | Module 要求 |
| --- | --- |
| 已接入 IAM；可以设置本地管理员；存在跳过 IAM、直接用本地账号登录的入口 | 必须实现托管本地管理员，通常声明为 `break_glass` |
| 已接入 IAM；不能创建/设置本地管理员，或所有入口都无法绕过 IAM | 不声明本地管理员；在 Module README 明确说明缺失的是哪项能力，以及 IAM 故障时的真实恢复方式或“无应用内恢复入口” |
| 未接入 IAM；管理界面使用应用自身的用户名密码 | 必须实现托管本地管理员，通常声明为 `primary` |
| 未接入 IAM；应用没有用户数据库或没有人机管理登录 | 不声明本地管理员；在 README 说明管理界面如何受保护，或说明该 Module 没有管理登录 |

不能因为应用带有 `admin` 字段就声明本地管理员。数据库 root、LDAP bind、API token、
`svc_*` 和容器内部服务凭据都属于服务身份或 Resource Secret，不进入本契约。也不能为没有
真实直登入口、密码更新 API/CLI 或验证路径的应用伪造 `break_glass`、`apply` 或 `rotate`
能力。

实现本地管理员的 Manifest 示例：

```yaml
management:
  surfaces:
    - id: web
      uri_from: EXAMPLE_DOMAIN_FULL
      authentication:
        primary: iam
    - id: local_recovery
      uri_from: EXAMPLE_BREAK_GLASS_URL
      authentication:
        primary: local
  local_accounts:
    - id: break_glass
      purpose: break_glass
      credential:
        policy: generated_per_module
        container_format: plaintext_on_bootstrap
      lifecycle:
        apply: apply-example-break-glass
        rotate: rotate-example-break-glass
```

契约规则：

1. `id` 是 CLI 中的 `ACCOUNT`，是稳定的逻辑账号 ID，不是用户名。允许的用途为
   `primary`、`break_glass`、`embedded_guard`。CLI 省略 `ACCOUNT` 时按 `primary`、唯一账号、
   歧义错误的顺序解析。
2. 用户名不可由用户配置。上游固定账号由 Module 声明 `fixed_username`；否则 Runner 使用
   固定的 ANAS 默认模板 `admin_{module}`。用户名首次物化后锁定，不提供 rename 命令。
3. 密码只能由版本化 `.anas/secrets.yml`（0600）持久化，以稳定 key 和
   owner/kind/provenance 区分来源。外部导入 YAML 可提供一次性 bootstrap 值，成功导入后
   workspace config 必须移除；Module 不得从 argv、workspace YAML、`.env` 或长期容器环境
   接受托管明文。bootstrap-only 上游使用 0600 runtime Secret 文件；能使用 hash 的上游只
   发布 hash。DNS API token 等普通部署 Secret 不属于 lifecycle-managed，继续留在受管配置。
4. `apply` 必须把当前托管 Secret 写入或核对应用内部状态，并验证账号确实可登录；仅设置
   容器环境变量不算完成。`rotate` 接收候选 Secret，更新应用、验证候选值，成功后才允许
   Runner 提交 Secret。失败必须恢复旧应用凭据；无法可靠回滚时不得声明 rotate。
5. IAM Module 的 `break_glass` 必须给出不经过 IAM 的真实入口，例如上游 direct-login URL；
   普通 IAM 登录页不能冒充恢复入口。
6. Module README 必须有“管理员访问”小节，列出 IAM 状态、本地账号支持状态、账号 ID、
   purpose、物理用户名及其来源、直登/恢复入口、apply/rotate 实现及限制。只要存在应急账号，
   必须写出可操作的登录地址（完整 URL，或明确的 `<DOMAIN_FULL>` 加固定 path）、实际用户名，
   以及通过 `anas admin local credential <module> <account-id> -w <workspace>` 获取密码的命令；
   不得把密码值写进文档。没有本地账号也必须写明原因和 IAM 故障时的真实恢复路径，不能靠
   Manifest 缺字段让操作者猜测。
7. 测试至少覆盖：Manifest 非法声明、明文不进入 deployment env/lock/manifest、CLI 查询
   显式敏感、apply 真实更新、rotate 成功、验证失败回滚、直登入口；声明为稳定支持前必须
   在真实容器上验证旧密码失效和新密码成功。

当前状态必须显式记录：Authentik 声明固定 `akadmin` 的 `break_glass`，Traefik 声明模板
用户名的 `primary`，两者都有真实 apply/rotate handler；MeshCentral 当前强制 OIDC-only，
并在中心认证器拒绝本地和 LDAP 密码认证，因此没有同 domain 的 `break_glass` 绕过入口。
LAM 主登录使用目录账号并限制为已启用的 Samba `Admins` 组成员；每位用户使用自己的
`sAMAccountName` 和目录密码。其 Module 私有密码只保护无用户名的配置/profile 编辑器。
`Admins` 只控制能否进入 LAM，实际目录读写能力仍由 Samba AD ACL 和高权限组决定。
Collabora 使用自己的 Module 参数并在省略密码时生成独立 Secret，但尚无可验证回滚的
热轮换 handler，因此不声明托管账号；LLNG 当前只使用 AD/目录管理员组，删除
无人消费的 `LLNG_PASSWORD` 后也不伪造 `break_glass`。CLI 必须明确报不支持。

共享的 `global.default_service_root_password` 已删除。每个账号或服务身份必须使用所属
Module 的参数或独立生成 Secret。若以后提供批量轮换，应由 Runner 对每个账号分别执行其
handler 并报告逐账号结果，不能重新引入共享密码。

## 同时支持 PostgreSQL 与 MariaDB 的规范

同时支持两个数据库的 Consumer Module 必须通过 `relational_database` Contract 集成，不能写死对 `postgres`、`mariadb` Module 的硬依赖，也不能读取 Provider 的管理员凭据。Manifest 至少包含以下结构：

```yaml
dependencies:
  contracts:
    - name: relational_database
      version: ">=1.0.0 <2.0.0"
      selected_by: db_type
      interfaces: [postgres, mariadb]
      default: postgres
resources:
  requires:
    - id: primary_database
      contract: relational_database
      binding: db_type
      spec_from:
        name: db_name
      spec:
        principal: example_app
        credential:
          policy: generated
        deletion_policy: retain
config:
  defaults:
    db_type: auto
    db_name: example_app
  changes:
    db_type:
      effect: data_migrate
      apply: migrate-example-app-database
    db_name:
      effect: data_migrate
      apply: migrate-example-app-database
```

实现必须遵守以下边界：

1. `db_type` 的公开值是 `postgres`、`mariadb`、`auto`；不要把 Contract interface 命名为 `mysql`。MariaDB 对应用的 MySQL 协议兼容只在 Module 内部转换。
2. Runner 解析完成后发布 `<PREFIX>_DB_TYPE`、`_DB_HOST`、`_DB_PORT`、`_DB_NAME`、`_DB_USERNAME`、`_DB_PASSWORD`、`_NETWORK_DB`。Hook 只消费这些 Consumer 私有值，不读取 `POSTGRES_PASSWORD`、`MARIADB_PASSWORD` 或 `MYSQL_ROOT_PASSWORD`。
3. Compose 通过 `*_NETWORK_DB` 加入所选 Provider 的 external network。不得同时连接两个数据库网络，也不得用 Compose `depends_on` 跨 Project 模拟资源依赖。
4. 容器必须包含两个引擎所需的客户端/驱动，并在入口点显式映射上游应用配置。例如 PostgreSQL 分支可生成 `POSTGRES_*`，MariaDB 分支可生成应用所需的 `MYSQL_*`；这些是 Module 内部适配，不是跨 Module 契约。
5. 初始化 SQL 必须可重复执行。数据库和专属用户由 Provider `ensure` 创建；应用入口点只负责自身 schema，不应创建全局用户或授予管理员权限。
6. Hook 单元测试必须覆盖两个 `db_type` 分支、专属凭据和网络映射；render/Compose 测试必须各覆盖至少一个引擎；声明为稳定支持前还必须有真实容器测试验证 schema 初始化、重启和幂等重应用。

如果上游应用只支持一个引擎，只在 `interfaces` 中声明实际验证过的 interface。不要为了保持 Manifest 对称而声明未实现的兼容性。

## 升级与兼容

新增 Module 时应让初始化、迁移和 reconcile 幂等：全新安装、重复 `apply`、重启和中断重试应收敛到同一状态。上游入口或声明式配置能完成适配时，不增加自有升级脚本；必须使用脚本时，脚本应检查实际状态、可重复执行，并明确适用版本和删除条件。

后续 release 可以停止兼容适配前的来源版本并删除旧脚本，优先与上游 major 版本升级合并。删除时必须在同一 release 提高 `upgrade.from` 下界；若新版本还改写了旧版本不能读取的磁盘数据，同时在 `upgrade.data_breaking` 标出断代版本。两者的判断和测试按 [Module 上游升级 SOP](/developer/module-upgrade-sop)执行。

## 文档、时区与语言

每个 Module 必须维护 `README.md` 和与当前 `module.yml` 版本一致的 `localization.yml`。支持语言必须从当前固定版本的源码、官方文档或精确镜像中取证，并以规范 BCP 47 记录；浏览器协商、部署默认、固定语言和无 UI 必须明确区分。

字段、fallback 策略和生成命令见 [Module 文档规范](/developer/module-documentation)。每次升级上游版本必须执行 [Module 上游升级 SOP](/developer/module-upgrade-sop)。
