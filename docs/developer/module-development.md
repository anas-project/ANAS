# Module 开发

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

## 依赖和能力

- 硬依赖必须显式声明并参与选择与排序；
- 可替代实现通过能力 Provider 绑定；
- 仅排序关系不能隐式选择另一个 Module；
- 持久资源通过 Resource/Provider operation 管理，而不是共享数据库密码约定。

## 配置与 Secret

只声明 Module 实际消费和导出的配置。不要依赖全量环境注入；生成的 `.env` 应只包含当前 Module、依赖闭包和显式声明的键。敏感值不得写入日志或非必要容器。

开始实现前阅读[Module、Contract 与 Resource 设计](/architecture/module-contract-resource-design)和[环境变量契约](/reference/module-environment-variables)。

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
   purpose、用户名来源、直登/恢复入口、apply/rotate 实现及限制。没有本地账号也必须写明
   原因，不能靠 Manifest 缺字段让操作者猜测。
7. 测试至少覆盖：Manifest 非法声明、明文不进入 deployment env/lock/manifest、CLI 查询
   显式敏感、apply 真实更新、rotate 成功、验证失败回滚、直登入口；声明为稳定支持前必须
   在真实容器上验证旧密码失效和新密码成功。

当前状态必须显式记录：Authentik 声明固定 `akadmin` 的 `break_glass`，Traefik 声明模板
用户名的 `primary`，两者都有真实 apply/rotate handler；MeshCentral 的上游在未设置
domain `auth` 时支持本地账号，但当前 Module 使用 LDAP，故没有同 domain 本地绕过。
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
