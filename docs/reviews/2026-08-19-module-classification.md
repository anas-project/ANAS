# ANAS Module 分类与访问边界分析（2026-08-19）

本文结合当前工作树的 18 个 `modules/*/module.yml`、Module 文档、Hook、Compose 路由、
管理员账号设计和应用目录设计，回答两个问题：ANAS 的 Module 应分成几类，以及每类 Module
的界面应该允许谁访问。

本文是分类与现状审计报告，记录“为什么这样分”和“当前实现差在哪里”。应用目录字段、
`platform_admin` 解析、LLNG/Authentik 映射与校验规则，以
[`app-catalog-design.md`](../architecture/app-catalog-design.md) 为唯一规范来源，本文不另建一套
平行 schema。

## 结论

建议把 Module 的**主用途分为三类**：

1. **基础支持（`foundation`）**：主要被其他 Module 或 Runner 作为技术能力、Contract、
   协议后端或网关消费，不为普通用户提供独立业务工作区；
2. **平台管理服务（`admin_service`）**：提供目录、IAM、DNS、认证、网络等平台能力，
   日常配置和管理入口面向管理员或受委派运维人员，普通用户最多间接消费其能力；
3. **用户应用（`user_app`）**：普通用户直接使用其 Web、客户端或网络协议完成文件、协作、
   设备管理、远程访问等任务，访问权由 `APP_<module>`、`APP_all` 等应用组分配。

这三类应该是新的**产品/使用层分类**，不能替代当前 `category: database|network|identity|...`
这种**技术领域标签**。`status: release|developing` 也应继续作为独立的成熟度维度。

同时，不能仅凭 Module 主分类决定权限。一个 Module 可以包含多个访问面：PostgreSQL 是基础
支持，但可选 Adminer 是管理员界面；LLNG 是平台服务，但 Portal 面向所有需要登录的用户，
Manager 只面向管理员；Nextcloud 是用户应用，但 `break_glass` 入口只能用于恢复。因此最终
模型应是：

```text
Module 主分类（3 类）
  + 技术领域（database / network / identity / ...）
  + 成熟度（release / developing）
  + 每个入口的 audience、认证方式和授权组
  + 是否发布到普通应用目录或管理员目录
```

分类用于组织、默认展示和策略校验；真正的访问控制必须在 IAM、ForwardAuth、应用自身认证
或网络边界执行，不能用“门户里不显示”代替授权。

## 三类的判定标准

| 主分类 | 判断问题 | 人员访问默认值 | 目录默认值 | 典型 Module |
| --- | --- | --- | --- | --- |
| `foundation` | 它的主要消费者是否是其他 Module/机器，而不是人员？ | 无人员入口；若带 Dashboard/Adminer，只允许管理员 | 主体不发布；管理入口可进入管理员目录 | `postgres`、`mariadb`、`lego`、`traefik` |
| `admin_service` | 它是否提供需要管理员运营的平台能力，而不是终端用户工作区？ | 管理面使用 `platform_admin` 或更窄的委派运维角色 | 只进入管理员目录；共享登录 Portal 可作为系统入口单独处理 | `lam`、`ddns_go`、`samba_dc`、IAM Provider |
| `user_app` | 非管理员是否会直接使用它完成自己的任务？ | `APP_<module>`、`APP_all` 或 `Admins` 可访问；管理员角色另行映射 | 进入普通应用目录 | `nextcloud`、`meshcentral`、`samba_fs` |

判定时看的是 Module 的**主要产品用途**，不是它有没有 Web UI：

- `samba_fs` 没有 Web UI，但普通用户直接通过 SMB 使用，所以属于 `user_app`；
- `collabora` 有用户可感知的编辑能力，但当前只作为 Nextcloud/WOPI 后端被嵌入，没有独立
  用户入口，所以更适合作为 `foundation`；
- `postgres` 启用 Adminer 后仍是 `foundation`，Adminer 只是该 Module 的一个
  `administrators` 管理面；
- `meshcentral` 虽然包含设备管理职能，但当前实现允许 `APP_meshcentral`、`APP_all` 和
  `Admins` 登录，且只把 `Admins` 映射为 site-admin，因此它可以作为面向获授权普通用户的
  `user_app`，不应因名称中有“管理”二字归入平台管理服务。

## 当前 18 个 Module 的建议归类

### 基础支持：7 个

| Module | 当前技术类别 | 状态 | 人员入口与建议边界 | 归类理由 |
| --- | --- | --- | --- | --- |
| `lego` | `certificate` | `release` | 无 Web/人员登录；不进目录 | 证书和内部 CA 材料由其他 Module 消费 |
| `traefik` | `network` | `release` | Dashboard 使用本地管理员；只进管理员目录 | 主体是所有 Web Module 的反向代理和路由基础设施 |
| `postgres` | `database` | `release` | 数据库主体为机器凭据；可选 Adminer 只允许管理员 | 提供 `relational_database/postgres` Contract |
| `mariadb` | `database` | `release` | 数据库主体为机器凭据；可选 Adminer 只允许管理员 | 提供 `relational_database/mariadb` Contract |
| `eturnal` | `communication` | `release` | 无人员管理入口；Consumer 使用 TURN shared secret | 为 Nextcloud Talk、NetBird 等实时通信提供协议后端 |
| `oauth2_proxy` | `identity` | `release` | 自身不作为应用发布；登录回调不是业务入口 | 为无登录能力的服务提供 `forward_auth/http` 能力 |
| `collabora` | `app` | `release` | 普通用户经 Nextcloud/WOPI 间接使用；管理面只允许管理员 | 当前是 Nextcloud 的嵌入式在线编辑后端，不是独立应用入口 |

### 平台管理服务：7 个

| Module | 当前技术类别 | 状态 | 人员入口与建议边界 | 归类理由 |
| --- | --- | --- | --- | --- |
| `samba_dc` | `identity` | `release` | 无 Web；目录管理只允许目录管理员/受委派管理员 | 是人员、组、服务账号、AD DNS 和身份锚点的事实来源；普通用户只是使用其认证结果 |
| `lam` | `identity` | `release` | LAM 管理界面只允许 `Admins` 或未来显式委派的目录管理员组 | 用于创建、禁用和管理目录对象，不是普通用户自助应用 |
| `ddns_go` | `network` | `release` | Web 使用 Module 本地 `primary` 管理员；只进管理员目录 | 变更域名解析和 DNS Provider 凭据属于平台运维操作 |
| `ddns_updater` | `network` | `release` | 当前由 ForwardAuth 默认限制为 `Admins`；只进管理员目录 | Web UI 修改动态 DNS 行为，没有普通用户业务场景 |
| `llng` | `identity` | `release` | Portal 是共享登录入口；Manager 和管理工具只允许 `Admins` | 提供 IAM、SSO 和应用启动器，是平台身份服务而不是终端业务应用 |
| `authentik` | `identity` | `developing` | 普通目录用户可使用登录/自助面；只有 `Admins` 映射 superuser，`akadmin` 仅恢复 | 主体是 IAM Provider；多访问面不能用单一 audience 表达 |
| `freeradius` | `network` | `developing` | 当前无受支持的管理员 UI；未来管理面默认管理员 | 提供网络认证服务；终端设备消费认证结果，不是人员工作区 |

### 用户应用：4 个

| Module | 当前技术类别 | 状态 | 普通访问与管理边界 | 归类理由 |
| --- | --- | --- | --- | --- |
| `nextcloud` | `app` | `release` | `APP_nextcloud`、`APP_all`、`Admins` 可登录；`Admins` 映射应用管理员；本地 `break_glass` 不进目录 | 普通用户直接使用文件、分享、Talk、Memories 等功能 |
| `meshcentral` | `app` | `release` | `APP_meshcentral`、`APP_all`、`Admins` 可登录；仅 `Admins` 映射 site-admin | 获授权用户直接进行远程设备访问和管理，不要求成为平台管理员 |
| `samba_fs` | `storage` | `release` | 普通目录用户通过 SMB 访问；共享读写由 `FS Share RW` 等组控制 | 虽无 Web UI，但它是普通用户直接使用的文件服务 |
| `netbird` | `network` | `developing` | 目标是 `APP_netbird`、`APP_all`、`Admins`；管理员角色映射尚是发布阻塞项 | 客户端和 Dashboard 面向获授权用户，但当前仍是实验性骨架 |

这里的数量是对**当前仓库**的归类，不是固定配额。以后新增 Module 仍按判定标准归入三类，
不应为了维持数量而改变语义。

## 为什么不能继续只用现有 `category`

当前 `category` 表示技术领域，不能推导用户范围：

- 同为 `identity`，`samba_dc` 是目录服务，`lam` 是目录管理界面，`oauth2_proxy` 是机器网关，
  `llng`/`authentik` 又同时包含普通用户入口和管理员入口；
- 同为 `network`，`traefik` 是基础支持，`ddns_go` 是管理员服务，`netbird` 则计划成为用户应用；
- `collabora` 当前标为 `app`，实际没有独立用户身份和入口；
- `samba_fs` 当前标为 `storage`，却是普通用户直接使用的服务。

因此不建议把 `category` 的现有值替换为三类，否则会丢掉数据库、身份、网络等技术检索能力。
更安全的增量做法是保留 `category`，新增主分类，例如：

```yaml
name: postgres
category: database
classification:
  class: foundation
```

```yaml
name: nextcloud
category: app
classification:
  class: user_app
```

`classification.class` 应为必填枚举，但可以先由 Runner 为未迁移清单生成兼容默认值并输出
警告，再在所有内置 Module 补齐后收紧为必填。不要根据依赖数量、容器名或有没有
`application_group` 自动猜测主分类。

## 入口级访问模型（分析结论）

主分类只提供默认策略；真实入口仍需分别表达以下互不替代的维度：

| 维度 | 回答的问题 | 典型值 |
| --- | --- | --- |
| `classification.class` | Module 在产品中的主要用途是什么？ | `foundation`、`admin_service`、`user_app` |
| 技术 `category` / `status` | 属于什么技术领域，成熟度如何？ | `identity`、`database`；`release`、`developing` |
| `audience` | 谁应该使用这个具体入口？ | `administrators`、`assigned_users`、`authenticated_users` |
| `access.via` | 哪个执行点真正认证或授权？ | `iam`、`forward_auth`、`native_group`、`local`、`external` |
| `launcher.category` | 卡片显示在哪个分区？ | `applications`、`admin`；仅展示，不产生授权 |
| 入口用途与状态 | 是正常、管理、恢复还是嵌入入口，是否启用？ | `primary`、`management`、`recovery`、`embedded` |

管理员入口在设计输入中使用语义角色 `access.role: platform_admin`，不写
`allow_groups: Admins` 或 LDAP DN。Runner 按消费位置解析：LLNG、Authentik、IAM claim 和
ForwardAuth 使用 `SAMBA_DC_ADMIN_GROUP_NAME`，直接 LDAP `memberOf` 过滤使用
`SAMBA_DC_ADMIN_GROUP_DN`。用户应用则从现有 IAM client 事实继承
`APP_<module>`、`APP_all`、`Admins` 的 OR 集合。

因此 `category: admin` 不对应 `SAMBA_DC_ADMIN_GROUP_DN`；它只能改变目录分区。完整 YAML、
Runner 输出契约、Provider 映射和 fail-closed 校验统一见
[`app-catalog-design.md`](../architecture/app-catalog-design.md)。

## 当前实现与目标模型的差距

### 1. 应用目录尚不能承载三类和多入口

当前 Runner 仍使用过渡性的 `APPS_LIST*` 协议，实际只有 `nextcloud`、`meshcentral`、
`netbird` 发布条目。完整 `ANAS_APP_CATALOG`/`launcher` 契约仍停留在
[`app-catalog-design.md`](../architecture/app-catalog-design.md) 的设计阶段。

三类主分类可以先进入 Manifest 和 `plan`，但在完整 catalog 上线前，不能声称门户已按
“普通应用/系统管理”正确分组。管理员目录也不应继续由各 Hook 私自追加变量。

### 2. 管理面清单不完整

当前只有 `authentik`、`ddns_go`、`meshcentral`、`nextcloud`、`traefik` 五个 Module 声明了
`management.surfaces`。`lam`、`ddns_updater`、`llng` Manager、数据库 Adminer、Collabora
管理面等已经存在或可选，却没有进入统一入口库存。结果是 Runner 无法完整校验“这个入口
属于哪类、由谁认证、是否应该进目录”。

### 3. LAM 的声明与实际登录过滤不一致

`lam/module.yml` 当前声明 `identity.application_group: true`，文档表格也写有
`APP_lam`/`APP_all`；但 `lam/configure.php` 的 `loginSearchFilter` 实际要求用户属于
`SAMBA_DC_ADMIN_GROUP_DN`。以“LAM 是管理员服务”为目标，当前执行点的 `Admins` 限制是合理
的，`application_group: true` 和相关文档则会误导应用目录与账号分配。

建议删除 LAM 的普通应用组声明，或在将来确实需要委派账号管理员时新增独立角色（例如
`directory_admin`）并配置 AD ACL；不要把 `APP_lam` 当作目录写权限。

### 4. Adminer 目前不是统一的管理员授权入口

PostgreSQL 和 MariaDB 的 Adminer 路由当前没有 ForwardAuth middleware，只依赖数据库账号
登录。数据库凭据本身能阻止无凭据访问，但它不能表达“只有 ANAS 管理员可见和可尝试登录”，
也无法与管理员目录、统一撤权和审计保持一致。

Adminer 没有独立的 ANAS 人员用户系统。它的登录表单收集数据库 server、username、password，
再用这些凭据连接 PostgreSQL/MariaDB；因此目标策略确定为两层：

1. Traefik 路由先用 `platform_admin` ForwardAuth 做人员管理员门禁；
2. 放行后，Adminer 仍要求数据库账号密码作为第二层认证。

ForwardAuth 后可以预选非敏感的连接元数据，但不能自动注入 PostgreSQL 超级用户、MariaDB
root 或 Consumer 密码；真正的一键登录需要未来的逐人短期凭据机制。角色如何解析为组名或
DN，以及完整的禁止共享超级用户规则，统一见应用目录设计。仅把 Adminer 从普通门户隐藏不
构成修复。

### 5. LLNG/Authentik 等混合入口需要拆面声明

LLNG 当前用同一 Traefik router 承载 Portal、Manager 和 Test 域名，再由 LLNG location rule
区分权限：Portal 默认接受已登录用户，Manager 要求 `Admins`。这说明 Module 级单一
`audience` 不可行。当前 Test 在启用时仍是 `default=accept`，这不符合管理服务边界。应分别
登记 `portal`、`manager`、`test` 和可选管理工具；Test 的直接 URL 和目录条目都必须只允许
`SAMBA_DC_ADMIN_GROUP_NAME`，普通已登录用户不得访问。

Authentik 同样要区分普通登录/自助入口、管理员 UI 和 `akadmin` 本地恢复入口。恢复入口必须
`catalog: hidden`，不应因 Module 被归为平台服务而出现在日常管理员目录。

### 6. 成熟度不能由分类掩盖

`authentik`、`netbird`、`freeradius` 当前仍为 `developing`。分类只说明用途，不代表生产可用；
目录和 UI 必须同时展示 `status`，默认部署选择也继续排除实验性 Module。

## 与应用目录和权限组的关系

建议目录至少有两个展示分区，但不另造授权规则：

| 目录分区 | 条目来源 | 默认可见者 | 示例 |
| --- | --- | --- | --- |
| 普通应用 | `user_app` 的可发布入口 | 对执行点已授权的用户 | Nextcloud、MeshCentral、NetBird |
| 系统管理 | `admin_service` 的管理入口，以及 `foundation` 的可选管理入口 | `platform_admin` 或显式委派管理员角色 | LAM、DDNS-GO、Traefik Dashboard、Adminer |

以下内容默认隐藏：机器协议端点、OIDC callback、TURN、数据库端口、Collabora WOPI 后端、
OAuth2 Proxy 自身、任何 `break_glass` 入口。`samba_fs` 虽属于 `user_app`，但没有合适的 HTTPS
URL 时不必为了“凑目录”创建假条目；主分类与 Web launcher 是否存在是两个维度。

现有应用组在执行点的物理语义可以保留；这些是 Runner 的解析结果，不是管理员入口的
Manifest/config 写法：

- `APP_<module>`：允许访问某一个用户应用；
- `APP_all`：允许访问所有用户应用；
- `Admins`：允许访问并获得应用管理员映射；
- 管理服务默认不创建 `APP_<module>`，而是声明 `platform_admin` 或未来的专用委派角色；
  当前 `platform_admin` 解析为 `Admins`。

需要特别避免把 `APP_all` 解释为“所有管理后台”。它只能扩展用户应用访问，不能使普通用户
进入 LAM、DDNS、Traefik、Adminer 或 IAM Manager。

### LLNG 与 Authentik 的目录目标

两个 IAM Provider 应消费同一份 Runner-owned catalog，呈现相同的普通应用与系统管理集合，
并在各自执行点 fail closed。Portal/Dashboard 是目录宿主，不创建指向自身的循环卡片；LLNG
Manager/Test 和 Authentik Admin interface 是管理员入口；`akadmin`、`break_glass` 等恢复入口
始终隐藏。LLNG Test 即使不显示卡片，其直接 URL 也必须只允许 `platform_admin`。

LLNG category、Authentik group/policy、隐藏规则及无 Provider 启动链接的具体映射，以
[`app-catalog-design.md`](../architecture/app-catalog-design.md) 为准。

## 建议实施顺序

### P0：先修访问事实和库存

1. 为所有现存 Web/协议入口补齐 `management.surfaces` 或等价入口声明；
2. 统一核对路由、IAM/ForwardAuth、应用自身登录和文档，解决 LAM 的
   `application_group` 不一致；
3. 将 Adminer 明确为管理员管理面，增加 `platform_admin` ForwardAuth，并保留数据库登录作为
   第二层认证；
4. 拆分 LLNG、Authentik、Nextcloud 等 Module 的正常、管理和恢复入口，修正 LLNG Test
   为仅管理员可访问。

### P1：引入三类主分类

1. Manifest 新增 `classification.class` 枚举；
2. 为当前 18 个 Module 按本文表格补齐值；
3. Runner 校验默认策略：`foundation` 不得无声明发布普通应用，`admin_service` 的管理面不得
   默认为普通应用组，`user_app` 的人员入口必须有可执行的授权来源；
4. `anas plan`、模块索引和未来管理 UI 同时展示主分类、技术领域、状态和入口 audience。

### P2：接入正式应用目录

1. 按现有应用目录设计实现 Runner-owned `launcher`/`ANAS_APP_CATALOG`；
2. 普通应用和系统管理分区都从入口事实生成，不在 IAM adapter 中维护第二份权限；
3. 迁移 `APPS_LIST*`，补齐管理员条目、图标、排序和可选入口；
4. 用 E2E 验证“可见集合是实际允许集合的投影”，并验证隐藏条目仍不能绕过执行点访问。

## 验收标准

完成分类和访问边界改造后，至少应满足：

- 18 个内置 Module 均有且只有一个主分类，同时保留技术 `category` 和 `status`；
- 每个可由人员访问的入口都有唯一 owner、URI 来源、用途、audience 和认证执行点；
- 普通用户目录不出现基础组件、管理后台、恢复入口和机器端点；
- `APP_all` 不能打开任何管理员入口；
- `platform_admin` 可以访问管理员入口，但高风险组件仍可保留第二层本地/数据库认证；
- 普通用户对 Nextcloud、MeshCentral、Samba FS、NetBird 的访问不依赖成为平台管理员；
- 门户隐藏或显示变化不会改变实际授权，直接访问 URL 仍得到相同的允许/拒绝结果；
- `developing` Module 不因被归入某一类而变成默认生产组件。

## 最终建议

ANAS 当前最合适的是**三类主分类，不是三套权限系统**。`foundation`、`admin_service`、
`user_app` 解决“它在产品里扮演什么角色”；现有 `category` 解决“它属于什么技术领域”；
`status` 解决“它是否成熟”；入口级声明解决“谁能通过哪种认证访问什么”。

这套分法能覆盖用户提出的 PostgreSQL/MariaDB、LAM/DDNS-GO/Samba AD、
Nextcloud/MeshCentral 三组例子，也能正确处理 Adminer、LLNG、Authentik、Collabora、
Samba FS 等不能靠单一 UI 标签判断的边界场景。
