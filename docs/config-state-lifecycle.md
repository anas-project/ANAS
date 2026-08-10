# ANAS 配置、初始化与持久状态通用方案

本文基于当前 `casks/mods`、Go runner、Compose 挂载和容器初始化脚本的实际行为，回答四个问题：哪些状态必须保存、哪些配置只在首次初始化生效、哪些默认值一旦物化就不能靠修改环境变量更新，以及后续如何分层管理配置。

## 结论

runner 现在已经能持久保存随机生成的密钥、声明部分配置的变更生命周期，并在成功启动后保存不含明文的配置哈希快照。应用内部 observed state 和各模块的实际迁移器仍需继续补齐。

建议采用三个核心机制：

1. 每个 cask 声明自己的持久数据、配置生命周期和对账动作。
2. runner 保存一份不含明文密钥的 applied state，记录每个模块已经初始化和实际应用的配置版本。
3. 启动拆成 `resolve -> inspect -> plan -> reconcile -> start -> verify -> commit state`，禁止把首次初始化配置的变化静默当成普通重启。

## 当前状态盘点

状态分为四类：

- **关键数据**：丢失会造成业务数据、身份、证书或信任关系丢失，必须备份。
- **可重建状态**：应持久化以保持身份或避免昂贵重建，但可以通过明确操作重建。
- **应用标记**：用于避免重复执行大范围操作，必须与它所描述的数据放在一起。
- **临时产物**：可由配置重新渲染，不应作为权威数据备份。

| 模块 | 当前持久状态 | 判断与处理建议 |
| --- | --- | --- |
| runner 自身 | `${base}/secrets.generated.yml`、`cask.lock.yml`、`release/` | generated secrets 是关键状态；lock 是升级元数据；release 可重建。新增 applied state。 |
| `collabora` | 无 | 无关键本地状态。 |
| `ddns` | `/updater/data` 未挂载 | 当前主要损失是历史/缓存；若启用备份或依赖更新历史，应挂载为可选运行状态。 |
| `eturnal` | `TURN_SECRET` 在 generated secrets | 已满足稳定密钥要求，无业务卷。 |
| `freeradius` | 无 | 当前只是 experimental scaffold；正式启用前必须声明证书、客户端密钥和计费数据的存储。 |
| `keycloak` | 业务状态在外部数据库；本地无卷 | 数据库是关键状态；管理员 bootstrap 密码仅首次建库有效。当前还会生成身份签名材料，应纳入明确轮换流程。 |
| `lam` | 配置每次启动生成 | 本地无关键状态；LDAP/AD 是权威数据源。 |
| `lego` | `${LEGO_DATA_PATH}:/certs` | ACME 账户、证书和私钥是关键状态，必须备份且权限应受限。 |
| `llng` | `${DATA_PATH}/llng/conf` 加外部数据库 | conf 和数据库都是关键状态；`lmConf-1.json` 是首次初始化标记。签名密钥在 generated secrets。 |
| `mariadb` | `${DATA_PATH}/mariadb:/config` | 关键数据库状态；root 密码在空数据目录初始化后不会因 env 变化自动更新。 |
| `meshcentral` | data、files、web、backups 四个目录，加外部数据库 | data、files、backups 和数据库为关键状态；web 通常可重建，但当前也被持久化。证书软链接每次启动重建。 |
| `netbird` | 当前没有任何持久卷 | 功能尚未完成，已标记为 experimental 并从完整示例移除。本阶段不补持久化；正式恢复前必须先决定保留还是删除。 |
| `nextcloud` | `${NEXTCLOUD_BASE_PATH}/nextcloud`、Redis 数据、外部数据库、`.anas-state` 标记 | Nextcloud 数据目录和数据库必须一致备份；Redis 可重建；Memories 标记正确地跟随数据卷。Talk 的 signaling `hashkey/blockkey` 已改为 generated secrets。 |
| `postgres` | `${DATA_PATH}/postgres` | 关键数据库状态；首次初始化脚本保留，同时增加一次性在线 reconciler，后续新增模块也能幂等创建数据库。 |
| `samba_dc` | `${DATA_PATH}/samba_dc/var`、generated secrets | AD 数据库、BIND9-DLZ DNS、域 SID、机器账户、GPO、用户/组、内部 TLS 是关键状态；BIND 配置和缓存可由 cask 重建。 |
| `samba_fs` | `${SAMBA_FS_USERDATA_PATH}`、`${DATA_PATH}/samba_fs/var`、guest ACL 状态文件 | 用户文件是关键数据；member join 状态可重建但应持久化；guest ACL 标记与 userdata 同卷，可避免每次启动递归扫描，当前方向正确。 |
| `traefik` | 无；只读使用 lego 证书 | 路由配置可重建，无独立关键状态。 |

### runner 自身状态

当前 base 目录中的文件应明确分级：

| 路径 | 性质 | 备份 |
| --- | --- | --- |
| `secrets.generated.yml` | 稳定生成密钥，当前为 `0600` | 必须，且应加密备份 |
| `cask.lock.yml` | 已安装 cask 版本和来源 | 建议 |
| `release/config.yml` | 当前 desired config 副本 | 建议，但不得包含明文生产密钥 |
| `release/*/.env` | 含派生凭据的渲染产物 | 不作为权威备份；保持 `0600` |
| `tmp/`、`go-build-cache/` | 临时/缓存 | 不需要 |
| `state/config-applied.yml` | 最近一次成功启动时的配置路径及 SHA-256 | 建议；当前不保存配置值或密钥明文 |

## 只在首次初始化生效或不能靠 env 直接修改的设置

这里的“不能修改”是指修改配置后普通 `restart/start` 不会安全地修改已有实例；并非永远不能改，而是必须有模块专用的迁移、重命名、重新加入或轮换动作。

| 范围 | 设置 | 当前实际行为 | 正确动作 |
| --- | --- | --- | --- |
| 存储根目录 | `DATA_PATH`、`SAMBA_FS_USERDATA_PATH`、`NEXTCLOUD_BASE_PATH`、`LEGO_DATA_PATH` | 修改后指向另一套目录，常表现为“全新实例”，不会搬迁旧数据 | `migrate-storage`，校验容量、停止写入、复制、校验、切换、回滚 |
| PostgreSQL | `POSTGRES_USERNAME`、`POSTGRES_PASSWORD` | 官方 entrypoint 只对空 PGDATA 初始化；后改 env 不会改数据库角色密码 | `rotate-credential` 或 SQL reconciler |
| PostgreSQL | 依赖模块的 `*_DB_NAME` | 生成的 initdb 脚本只在空 PGDATA 执行；数据库已运行后新增模块不会得到新库 | 在线、幂等的 `ensure-databases` reconciler |
| MariaDB | `MARIADB_ROOT_PASSWORD` | `/config` 首次初始化后，改 env 不会自动改现存 root 密码 | 数据库内轮换后，再原子更新消费者凭据 |
| Samba DC | `BASE_DOMAIN`、`SAMBA_DC_REALM`、`WORKGROUP`、`NETBIOS_NAME`、provision/join 模式 | `/var/lib/samba/registry.tdb` 存在后不再 provision；这些值构成域身份 | 默认 immutable；只能执行受控域迁移或重建恢复 |
| Samba DC | `SAMBA_DC_ADMINISTRATOR_PASSWORD` | 只传给首次 `samba-tool domain provision/join` | `samba-tool user setpassword` 轮换 |
| Samba DC | admin、LDAP bind、password bind 的名称和密码 | 用户不存在时才创建；改密码 env 不会更新 AD；改名称会新建账户并留下旧账户 | 显式 `rename-account`/`rotate-credential`，更新消费者后禁用旧账户 |
| Samba FS | `SAMBA_FS_HOSTNAME`、域/realm、机器身份 | 已有 `net ads testjoin` 成功时不会重新 join；仅改 env 可能造成配置与机器账户不一致 | `leave/join` reconciler，并处理旧 machine account |
| Nextcloud | `NEXTCLOUD_DB_TYPE/DB_NAME/DB_HOST`、admin username/password、data dir | `maintenance:install` 只在未安装时执行；后改 env 不会迁移数据库或重置 admin | Nextcloud 专用 DB migration、`occ user:resetpassword`、storage migration |
| Keycloak | `KEYCLOAK_ADMIN_PASSWORD` | bootstrap admin 只在首次创建时有效 | Keycloak 管理 API/CLI 轮换 |
| Keycloak/LLNG/Nextcloud | SSO entity、issuer、redirect URI 与域名 | 部分配置能重渲染，但已有客户端/元数据/外部信任不会自动整体迁移 | 跨模块 reconcile，保留过渡地址和签名验证窗口 |
| LLNG | 首份 `lmConf-1.json` | 文件不存在时才从模板初始化；后续只能通过配置合并/管理接口修改 | 用版本化 reconciler 更新现有配置，不覆盖整份文件 |
| NetBird | `/var/lib/netbird` 数据和 DataStoreEncryptionKey | 当前功能未完成且默认不部署 | 恢复开发时先决定持久模型，否则删除此 cask |
| MeshCentral | 数据库选择和持久数据身份 | 改 env 不会搬迁数据库或文件状态 | 专用备份/迁移流程 |

密码策略、Samba OU/组、应用访问组、Samba share 模式、Traefik 路由和多数资源限制适合做幂等 reconcile，可在每次启动检查并更新；它们不应混入“首次初始化”脚本。

## 默认值物化后的规则

当前有两种“默认值”，行为并不相同：

1. `secrets.Ensure` 生成的稳定默认值会写入 `secrets.generated.yml`，以后修改代码里的生成规则不会替换旧值。
2. `defaultValue(x, parent)` 每次渲染重新计算，但目标应用可能只在第一次读取。于是 `.env` 已改变而应用内部仍保留旧值，形成无提示漂移。

第一类包括：

- SSH 私钥/公钥；
- TURN secret；
- LLNG/Keycloak 的 SAML/OIDC 私钥、证书和 key ID；
- NetBird OIDC client secret；
- Nextcloud Talk 的 internal/signaling secret；
- PostgreSQL/MariaDB 密码；
- Samba LDAP bind、password bind 密码。

第二类中风险最高的是：

- Keycloak/LLNG 密码继承 `DEFAULT_SERVICE_ROOT_PASSWORD`；
- Samba admin、Administrator、Nextcloud、LAM、Collabora、Keycloak/LLNG 等人工登录管理员继承 `DEFAULT_SERVICE_ROOT_PASSWORD`；
- DB 类型根据当前已启用的数据库自动选择；
- 各服务域名根据 `BASE_DOMAIN` 和 prefix 自动生成。

`DEFAULT_SERVICE_ROOT_PASSWORD` 现在是唯一的人工管理员默认密码，配置校验要求至少 8 位；旧的 `DEFAULT_ROOT_PASSWORD` 已移除。数据库 root、LDAP bind、密码修改 bind 等非交互账户不继承它。

通用规则应改为：

- `computed`：每次计算且不进入业务持久状态，例如容器名、网络名、URL 拼接。
- `generated_once`：首次生成后稳定保存，例如签名密钥。
- `materialized_default`：首次把父级默认值写入应用时，记录当时的最终值或秘密版本；以后父级变化只报告 drift，不自动覆盖。
- `reconciled`：允许 runner 幂等更新应用，例如 Samba 密码策略。
- `rotatable`：必须执行轮换事务，例如数据库和 bind account 密码。
- `immutable`：初始化后禁止普通修改，例如 AD realm。
- `migratable`：有明确迁移器才能修改，例如数据目录和数据库类型。

用户显式填写一个新值也不应绕过这些生命周期；显式值只表达 desired state，是否可直接应用仍由 lifecycle 决定。

## 建议的配置分层

不要继续把所有内容压平成一张来源不明的 env map。建议分为五个平面，最终只有 render 阶段才生成环境变量。

### 1. Schema / cask defaults

由 cask 提供类型、默认值、是否敏感、生命周期和校验规则。它是产品默认，不是已应用状态。

### 2. Desired config

用户期望状态，内部优先级为：

`产品默认 < profile/site < host/global < service/module`

推荐继续使用结构化 YAML，不鼓励顶层 raw `env`。模块不能随意覆盖其他模块的私有参数；共享参数必须声明为 global output/input。

### 3. Secret providers

配置只保存引用，例如 `secret://anas/samba/password-bind`。后端可以先支持本地 `secrets.generated.yml`，以后再接 Docker secrets、文件或外部 secret manager。日志、plan、applied state 和 config diff 中只显示引用、版本和 hash，不显示明文。

### 4. Applied/observed state

记录应用实际采用的值、初始化标记、迁移版本和最近一次成功对账。普通配置不能覆盖这一层；只有成功的 reconciler/migration/rotation 可以更新它。

### 5. Runtime render and operation overrides

容器名、URL、网络、探测到的 HOST_IP 等是只读派生输出。临时 `--set` 只允许用于一次操作，必须显示在 plan 中，不写回 desired config，也不能用来绕过 immutable/rotation 规则。

最终 env 的来源必须可追踪。`anas config explain NEXTCLOUD_DB_TYPE` 应能显示：值、来源、生命周期、是否已应用、改变它会触发什么动作。

## cask 状态契约

建议把现有 `cask.yml` 扩展为类似下面的声明：

```yaml
config:
  parameters:
    db_type:
      type: enum
      values: [postgres, mariadb]
      default_from: available_database
      lifecycle: migratable
      apply: migrate-database
    admin_password:
      type: secret
      default_from: secret://anas/samba/admin
      lifecycle: rotatable
      apply: rotate-admin-password

state:
  volumes:
    - name: data
      host: ${NEXTCLOUD_BASE_PATH}/nextcloud
      container: /data
      class: critical
      backup_group: nextcloud
  markers:
    - name: installed
      probe: nextcloud-installed
  reconcilers:
    - name: configure-ldap
      version: 2
      inputs: [ldap.*, domain]
```

每个可写容器路径必须声明为以下之一：`critical`、`rebuildable`、`cache`、`scratch`。未声明的可写状态在 lint 阶段报错。这样可以自动发现 NetBird 这类漏挂载问题。

## applied state 格式

第一阶段已经新增 `${base}/state/config-applied.yml`，仅保存显式配置路径的 SHA-256 和成功启动时间。完整 observed state 后续可扩展为 `${base}/state/applied.yml`，示意如下：

```yaml
schema: anas.state/v1
modules:
  samba_dc:
    instance_id: 8f9d...
    initialized: true
    data_fingerprint: sha256:...
    applied:
      realm: {value: EXAMPLE.COM, lifecycle: immutable}
      administrator_password: {secret_ref: samba/administrator, version: 1}
    reconcilers:
      directory-structure: 3
      password-policy: 2
    migrations: []
```

此文件不替代应用本身。启动时仍应通过只读 probe 检查数据库、AD、文件标记等真实状态；applied state 用于比较、审计和选择动作。若文件与真实状态冲突，必须报 drift，不能直接相信任一方。

## 启动与修改流程

统一流程如下：

1. **Resolve**：合并 schema、desired config 和 secret refs，保留每个值的来源。
2. **Inspect**：只读探测挂载、数据库、AD、应用安装状态和版本。
3. **Plan**：把差异分类为 `render`、`restart`、`reconcile`、`rotate`、`migrate`、`replace` 或 `blocked`。
4. **Preflight**：检查备份、磁盘空间、依赖、旧凭据、回滚条件。
5. **Apply**：按依赖顺序执行幂等 reconciler 或显式事务。
6. **Start/Restart**：只重启真正受影响的服务。
7. **Verify**：健康检查之外，还验证登录、DB 连接、域 join、证书和共享权限。
8. **Commit**：全部成功后原子写入 applied state；失败保留旧状态和恢复指引。

`anas start` 默认只能自动执行 `render/restart/reconcile`。`rotate/migrate/replace` 必须通过显式命令并展示 plan，例如：

```text
anas config plan
anas reconcile postgres --action ensure-databases
anas secret rotate samba/password-bind
anas migrate nextcloud --target-db postgres
```

## 建议的实施顺序

### P0：先消除数据丢失和静默漂移

1. 对 DB 密码、Nextcloud 初始管理员密码、Samba 域身份变更做启动前阻断提示（第一阶段已完成）。
2. 将 Nextcloud Talk `hashkey/blockkey` 纳入 generated secrets（已完成）。
3. 把 PostgreSQL 创建依赖数据库从 initdb-only 改为在线幂等 reconciler（已完成）。
4. NetBird 保持 experimental 且不进入推荐部署，恢复开发时再决定持久化或删除。
5. Samba DC SSH host key 按产品决定忽略，不纳入此方案。

### P1：建立状态模型

1. 扩展 cask schema：parameter lifecycle、state volumes、probe、reconciler。
2. 增加 `state/applied.yml`、原子写入和敏感值脱敏。
3. 增加 `config plan/explain`，显示来源和变更分类。
4. 首次运行升级版 runner 时从真实服务导入 observed state，不根据当前 env 猜测已应用值。

### P2：补齐显式运维动作

1. 数据库、Samba service account、Nextcloud/Keycloak admin 的凭据轮换事务。
2. storage path、数据库类型、域名/SSO endpoint 的迁移器。
3. 基于 state contract 自动生成备份清单和恢复顺序。
4. 为每个 cask 增加“首次初始化后修改”的集成测试。

这套方案的关键不是增加更多环境变量，而是让每个配置值都有来源、生命周期、实际状态和合法变更动作。这样默认值可以方便首装，又不会在系统运行后造成无提示的配置漂移或意外新建实例。
