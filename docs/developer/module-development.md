# Module 开发

## 基本职责

Module 是独立的发布和部署单元。它拥有：

- `module.yml` 中的身份、版本、ABI、依赖、能力和配置声明；
- Docker Compose 定义；
- 需要时使用的 Hook；
- 模板、构建上下文和运行资产。

Module 不应依赖仓库检出的相对路径才能启动。冻结后的 deployment 必须携带运行所需的 Compose、环境、文件和 Hook 二进制。

## 依赖和能力

- 硬依赖必须显式声明并参与选择与排序；
- 可替代实现通过能力 Provider 绑定；
- 仅排序关系不能隐式选择另一个 Module；
- 持久资源通过 Resource/Provider operation 管理，而不是共享数据库密码约定。

## 配置与 Secret

只声明 Module 实际消费和导出的配置。不要依赖全量环境注入；生成的 `.env` 应只包含当前 Module、依赖闭包和显式声明的键。敏感值不得写入日志或非必要容器。

开始实现前阅读[Module、Contract 与 Resource 设计](/architecture/module-contract-resource-design)和[环境变量契约](/reference/module-environment-variables)。

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

## 文档、时区与语言

每个 Module 必须维护 `README.md` 和与当前 `module.yml` 版本一致的 `localization.yml`。支持语言必须从当前固定版本的源码、官方文档或精确镜像中取证，并以规范 BCP 47 记录；浏览器协商、部署默认、固定语言和无 UI 必须明确区分。

字段、fallback 策略和生成命令见 [Module 文档规范](/developer/module-documentation)。每次升级上游版本必须执行 [Module 上游升级 SOP](/developer/module-upgrade-sop)。
