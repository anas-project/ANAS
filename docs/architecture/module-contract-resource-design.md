# Module、Contract 与 Resource 架构

状态：实施基线
日期：2026-08-21

## 1. 目标

ANAS 将部署单元、跨模块协议和外部持久资源分成三个不同概念：

- **Module** 是用户选择、安装、升级、启停和发布的部署单元。
- **Contract** 是 Module 之间可独立版本化的互操作协议，不是容器或守护进程。
- **Resource** 是 Consumer Module 按 Contract 申请、由 Provider Module 管理的持久对象。

Runner 负责解析绑定、保存密钥与资源状态、调用 Provider 操作以及执行 Module
生命周期。Runner 不实现 PostgreSQL、MariaDB、ACME 或 IAM 的具体协议。

本设计首先落地 `relational_database`，并为 `certificate`、`identity` 等后续
Contract 保留相同扩展模型。

## 2. 不变量

1. 新增数据库 Consumer 不重启数据库 Provider，也不重启已有 Consumer。
2. 应用的长期容器只获得自己的最小权限凭据。
3. 数据库管理员凭据只进入一次性 Provider operation。
4. Provider operation 必须幂等；重复 `ensure` 不得破坏已有数据。
5. 删除 Module 默认保留持久 Resource；销毁 Resource 必须显式执行。
6. Contract 不包含 Provider 方言；具体实现随 Provider Module 发布。
7. Consumer 不依赖具体 Provider 的文件、镜像、SQL 或环境变量。
8. Module 可以单独发布，且其 Provider 实现必须包含在同一个 Module 包中。
9. Contract 版本和摘要、Provider 绑定及 Resource 身份都写入 lock/deployment 状态。
10. 未变化 Module 在一次 apply 中不调用 `compose up`，也不更换其运行制品。

## 3. 源码和发布布局

ANAS 核心源码：

```text
modules/
  <module>/
    module.yml
    compose.yml
    hook/
    providers/
      <contract>/
        provider.yml
        <operation assets>

contracts/
  <contract>/
    contract.yml
    schemas/

internal/runner/
  module loading and lifecycle
  contract loading and validation
  binding resolution
  resource lifecycle and state
  operation execution
```

一个独立发布的 Module 是自描述目录或归档：

```text
postgres-18.4.0-r1/
  module.yml
  compose.yml
  providers/
    relational_database/
      provider.yml
      ensure.sh
      inspect.sh
      rotate.sh
      delete.sh
```

Module 包不复制官方 Contract。它只声明 Contract 名称、兼容版本和实现入口。
Provider 实现随 Module 发布，避免 PostgreSQL 模块版本与外置 SQL 脚本漂移。

官方 Contract 随 ANAS 核心发布。以后可增加独立 Contract 包和 registry，但初始
实现不要求网络 registry。

## 4. Module

### 4.1 定义

Module 是唯一进入普通部署依赖图和 `start/stop/restart` 生命周期的对象。例如：

- `postgres`
- `mariadb`
- `nextcloud`
- `authentik`
- `llng`
- `lego`
- `traefik`

一次性数据库 provisioner 不是 Module，因为它没有独立的用户期望状态，也不应成为
启停依赖图中的共享单例。

### 4.2 Module manifest

```yaml
api_version: anas.module/v1
kind: Module
name: nextcloud
version: 34.0.2
revision: 1

runtime:
  type: compose
  compose_file: compose.yml

dependencies:
  requires:
    - traefik
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
        principal: nextcloud
        credential:
          policy: generated
        deletion_policy: retain
```

`resources.requires[].id` 在一个 Consumer Module 内唯一。部署级 Resource identity 是：

```text
<consumer-module>.<resource-id>
```

例如 `nextcloud.primary_database`。

`spec_from` 把 module 参数解析进最终 Resource request。上例允许
`modules.nextcloud.config.db_name` 改变数据库名；provider 只接收解析后的
`spec.name`，不会读取 Nextcloud 私有配置。

### 4.3 Provider Module

```yaml
api_version: anas.module/v1
kind: Module
name: postgres
version: 18.4.0
revision: 1

contracts:
  provides:
    - name: relational_database
      version: 1.0.0
      interface: postgres
      implementation: providers/relational_database/provider.yml
```

Provider 的普通 Compose 服务仍属于 Module 生命周期；Provider operation 则按需
执行，不进入普通服务列表。

### 4.4 用户配置

用户配置只使用 `modules`，不再使用 `services.<module>.env`：

```yaml
modules:
  postgres:
    enabled: true
    config:
      adminer_enabled: false

  nextcloud:
    enabled: true
    config:
      db_type: postgres
      domain_prefix: nc
```

`services.nextcloud` 过去实际配置的是 Module。删除该命名可以避免与 Compose
`services` 以及能力服务概念混淆。

### 4.5 内置 Module 清单

每个目录都是可单独打包的 Module；“协议角色”只描述当前实现，不为未迁移的占位
Contract 声明虚假兼容性。

| Module | 类别 / 状态 | 职责与当前协议角色 |
| --- | --- | --- |
| `authentik` | identity / experimental | IAM Provider；申请一个 `relational_database` Resource |
| `collabora` | app / stable | Nextcloud 在线文档后端 |
| `ddns_go` | network / stable | 动态 DNS Provider 实现 |
| `ddns_updater` | network / stable | 受 forward-auth 保护的动态 DNS 更新器 |
| `eturnal` | communication / stable | TURN 服务 |
| `freeradius` | network / experimental | RADIUS 服务脚手架 |
| `lam` | identity / stable | LDAP 账号管理 UI |
| `lego` | certificate / stable | 当前证书实现；尚未迁移到 `certificate` Contract |
| `llng` | identity / stable | IAM Provider；申请一个 `relational_database` Resource |
| `mariadb` | database / stable | `relational_database/mariadb` Provider |
| `meshcentral` | app / stable | 设备管理 Consumer；申请一个 `relational_database` Resource |
| `netbird` | network / experimental | WireGuard overlay 与 OIDC Consumer 脚手架 |
| `nextcloud` | app / stable | 文件协作 Consumer；申请一个 `relational_database` Resource |
| `oauth2_proxy` | identity / stable | OIDC Consumer，提供 forward-auth capability |
| `postgres` | database / stable | `relational_database/postgres` Provider |
| `samba_dc` | identity / stable | AD、LDAP 与 DNS Provider；目录 Contract 尚未迁移 |
| `samba_fs` | storage / stable | 加入 AD 的文件服务 |
| `traefik` | network / stable | 反向代理和路由入口 |

### 4.6 IAM Consumer 的登出边界

OIDC/SAML Consumer 的登录和登出都属于 Module 与 IAM capability 的互操作边界。Core 只解析
effective interface、校验通用注册字段并把声明交给所选 Provider；应用会话如何创建、关联和
撤销仍由 Consumer Module 所有。

Module 必须用 Provider-neutral 的 `ANAS_IAM_BINDING__<APP>__*` 与
`ANAS_IAM_CLIENT__<APP>__*` 表达：

- Module -> IAM：OIDC RP-Initiated Logout 或 SAML SP-Initiated SLO；
- IAM -> Module：OIDC front-/back-channel logout 或 SAML IdP-Initiated SLO；
- 导航回调与应用会话通知 endpoint 是两类不同字段；
- 支持方法为空表示固定上游版本不支持 IAM 主动即时登出，Provider 与 Core 都不得猜测路径；
- 双向登出是否成立必须按 `Provider × 协议 × Module` 用真实应用会话验证。

Core 不认识 Nextcloud、MeshCentral 或某个插件的 logout URL，也不验证 Logout Token/SAML
消息；这些是 Module/上游应用的协议实现。Core 可以机械校验 URI 与 method/binding 成对、枚举
合法、字段属于当前 binding，并保证只有选中的 Provider 获得注册请求。详细字段、安全和发布
门禁见[使用 OIDC/SAML 的 Module 双向登出要求](/requirements/module-iam-bidirectional-logout)
与[Module 开发规范](/developer/module-development)。

## 5. Contract

### 5.1 必要性

Contract 解决的是独立发布 Module 之间的互操作，而不是代码复用。没有 Contract 时：

- Consumer 必须认识每个具体 Provider；
- Runner 会累积 PostgreSQL、MariaDB、ACME、IAM 等方言分支；
- 第三方 Provider 无法声明兼容性；
- Request、Result、密钥边界和生命周期无法统一验证。

只有同时满足以下条件的能力才建立 Contract：

1. 存在独立的 Provider 与 Consumer；
2. 可能有多个 Provider；
3. Consumer 不应依赖具体实现；
4. Runner 需要编排资源或协议生命周期。

内部辅助脚本、日志目录和单模块实现细节不建立 Contract。

### 5.2 Contract manifest

```yaml
api_version: anas.contract/v1
kind: Contract
name: relational_database
version: 1.0.0

interfaces: [postgres, mariadb]

resource:
  schema: schemas/resource.yml
  identity: [consumer, resource_id]

operations:
  ensure:
    request_schema: schemas/ensure-request.yml
    result_schema: schemas/connection-result.yml
    required: true
  inspect:
    request_schema: schemas/inspect-request.yml
    result_schema: schemas/inspect-result.yml
    required: true
  rotate_credential:
    request_schema: schemas/rotate-request.yml
    result_schema: schemas/connection-result.yml
    required: false
  delete:
    request_schema: schemas/delete-request.yml
    result_schema: schemas/delete-result.yml
    required: false
```

### 5.3 Contract 版本

- Patch：文档和诊断修正，不改变机器协议。
- Minor：增加可选字段、可选结果或可选 operation。
- Major：删除/重命名字段、改变语义、增加必填字段或改变幂等保证。

Consumer 声明接受范围，Provider 声明一个具体实现版本，lock 固定版本及内容摘要。
Contract schema 版本变化只决定协议兼容性；真实资源迁移仍由 Provider operation 完成。

### 5.4 Contract 集合

- `relational_database`：本次完整实现。
- `certificate`：本次只提供占位 schema；后续定义证书申请、检查、续期和撤销，
  由 `lego` 等实现。
- `identity`：本次只提供占位 schema；后续定义认证接口、客户端注册和元数据，
  收敛现有 IAM capability。

`directory`（目录查询、同步和身份锚点）与 `dns_record`（record
ensure/inspect/delete）是后续候选；在请求、结果和生命周期语义确定前不发布空
Contract manifest。

本次不能仅为了目录完整而给尚未迁移的能力伪造运行实现。未迁移 Contract 可以先有
稳定 schema，但现有 capability 在迁移完成前仍按其原生命周期执行。

## 6. Provider

Provider manifest 描述 Contract operation 怎样在 Provider Module 中执行：

```yaml
api_version: anas.provider/v1
kind: ContractProvider
contract: relational_database
contract_version: 1.0.0
interface: postgres

operations:
  ensure:
    runtime: compose_run
    service: anas_postgres_provision
    command: [ensure]
  inspect:
    runtime: compose_run
    service: anas_postgres_provision
    command: [inspect]
```

`compose_run` 的语义是：

```text
docker compose run --rm --no-deps <service> <operation>
```

Runner同步等待退出码。operation 服务不得由普通 `compose up` 启动；Provider
manifest 中引用的 service 自动从 Module 的普通服务集合中排除。

Provider operation 通过受控环境获得：

- Contract request；
- Resource 专用密钥；
- Provider 管理通道；
- Provider 网络。

管理员密钥不得进入 Consumer 的渲染环境。

## 7. relational_database Resource

### 7.1 Request

```yaml
consumer: nextcloud
resource_id: primary_database
provider: postgres
interface: postgres
spec:
  name: nextcloud
  principal: nextcloud
  credential:
    policy: generated
  deletion_policy: retain
```

数据库名和 principal 使用保守 identifier：

```text
^[a-z][a-z0-9_]{0,62}$
```

### 7.2 Result

```yaml
host: anas_postgres
port: 5432
database: nextcloud
username: nextcloud
password_secret: RESOURCE_NEXTCLOUD_PRIMARY_DATABASE_PASSWORD
network: anas_postgres
```

Result 中不持久化明文密码，只保存 secret reference。

### 7.3 Ensure

PostgreSQL Provider 必须幂等完成：

1. 创建或更新 LOGIN role；
2. 创建数据库（若不存在）；
3. 对账 owner、connect 和 schema create 权限；
4. 不删除表、不重建数据库。

MariaDB Provider 必须幂等完成：

1. 创建数据库（若不存在）；
2. 创建或更新用户；
3. 对账 `<database>.*` 权限；
4. 不删除表、不重建数据库。

### 7.4 删除

移除 Consumer Module 时：

- 停止 Consumer；
- Resource state 进入 `retained`；
- 数据库和账号保持不变。

只有显式 Resource 删除操作才调用 Provider `delete`。删除前必须显示目标数据库、
Provider、Consumer 和不可恢复影响，并要求确认。

## 8. Runner 职责

Runner 包含通用类型和状态机，不包含方言：

```go
type ResourceRequest struct {
    Consumer  string
    ID        string
    Contract  string
    Provider  string
    Interface string
    Spec      map[string]any
}

type ProviderOperation struct {
    Runtime string
    Service string
    Command []string
}
```

Runner 的资源阶段：

```text
resolve modules
resolve contract/provider bindings
validate contract versions and schemas
materialize resource secrets
render immutable module artifacts
diff active and target modules/resources
ensure provider module is healthy
run provider ensure operation
persist resource state
start added/changed consumer modules
verify
commit active state
```

Runner 不得通过 `if provider == postgres` 选择 SQL。`relational_database` 的字段验证与
连接环境发布由内置 Contract adapter 完成；PostgreSQL/MariaDB 的 SQL 仍只存在于各自
Provider 包中。通用 executor 只按 operation runtime（例如 `compose_run`）分派。

## 9. Resource 状态与密钥

状态位置：

```text
<workspace>/.anas/state/resources/
  <consumer>.<resource-id>.yml
```

示例：

```yaml
api_version: anas.resource-state/v1
consumer: nextcloud
resource_id: primary_database
contract: relational_database
contract_version: 1.0.0
provider: postgres
interface: postgres
spec_fingerprint: sha256:...
actual:
  host: anas_postgres
  port: 5432
  database: nextcloud
  username: nextcloud
  password_secret: RESOURCE_NEXTCLOUD_PRIMARY_DATABASE_PASSWORD
  network: anas_postgres
status: ready
deletion_policy: retain
provisioned_at: 2026-08-12T00:00:00Z
last_reconciled_at: 2026-08-12T00:00:00Z
```

Secret store 使用稳定键：

```text
RESOURCE_NEXTCLOUD_PRIMARY_DATABASE_PASSWORD
```

Resource state、deployment manifest 和日志中均不得写入明文。

## 10. Module 级 activation diff

仅有一次性 Provider operation 还不能保证热添加。当前每次 apply 都从新 deployment
目录对所有 Module 执行 `compose up`，路径变化可能导致未变化容器重建。

Activation 必须先分类：

- `unchanged`：Module artifact fingerprint、运行配置和绑定都相同；不调用 Compose。
- `added`：先 provision Resource，再启动 Module。
- `changed`：按 change policy reload/recreate/migrate。
- `removed`：按反向依赖顺序停止 Module。

新增 Nextcloud 的计划必须是：

```text
postgres                              unchanged
existing postgres consumers          unchanged
nextcloud.primary_database           ensure
nextcloud                             added
```

运行结果：

```text
PostgreSQL 和已有 Consumer 保持运行
→ 短命 provision operation
→ 只启动 Nextcloud
```

活动状态必须记录每个 Module 当前使用的 artifact deployment。新 deployment 可以复用
旧 deployment 中未变化 Module 的冻结目录，直到没有活动状态引用时再回收。

`state/active.yml` 另外记录 `runtime_status: running|stopped`。运行态 apply 使用上述
activation diff；完整 `anas stop` 后的 apply 必须恢复目标 deployment 的全部 Module，
不能因为 artifact 未变化而漏掉已被删除的容器和网络。

## 11. 发布与解析

Module 包必须独立验证：

1. `module.yml` schema；
2. 文件摘要；
3. Runner ABI；
4. 所声明 Provider manifest 和 operation assets；
5. Contract 名称和版本范围；
6. Compose 配置。

Contract 随 Runner 安装：

```text
<anas-install>/contracts/<name>/<version>/...
```

Module resolver 从安装目录或显式 module root 读取独立 Module 包。Lock 必须固定：

```yaml
modules:
  postgres:
    version: 18.4.0
    revision: 1
    digest: sha256:...

contracts:
  relational_database:
    version: 1.0.0
    digest: sha256:...

bindings:
  nextcloud:
    relational_database: postgres
    relational_database.interface: postgres
```

## 12. 首次迁移范围

本仓库尚未发布，不保留旧格式兼容层。本次直接完成：

1. `casks/mods` 改为顶层 `modules`；
2. `module.yml` 改为 `module.yml`，Kind 改为 `Module`；
3. 用户配置合并为 `modules.<name>.config`；
4. 新增 `contracts/relational_database`；
5. PostgreSQL、MariaDB 实现 Provider operation；
6. 所有关系数据库 Consumer 声明 Resource 并改用专用账号；
7. 删除 PostgreSQL 的全局 `*_DB_NAME` 扫描和 reconcile Compose service；
8. 增加 Resource 状态、密钥和同步 operation executor；
9. 增加 Module 级 activation diff；
10. 更新静态、渲染、Compose、生命周期、升级和服务器 E2E 测试。

验收条件是：在已有 PostgreSQL 和至少一个已有 Consumer 运行时新增 Nextcloud，
PostgreSQL container ID、started-at 和已有 Consumer container ID 均不变化；Nextcloud
使用专用数据库账号完成安装并通过功能探针。
