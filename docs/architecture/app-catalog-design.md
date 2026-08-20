# 应用目录（Application Catalog）设计

## 1. 目标与硬约束

部署里的服务应当以**面向用户的应用列表**呈现：用户登录门户后看到自己有权访问
的应用，按分类排列，每个条目有可配置的名称、描述、图标和顺序。LLNG 的
`applicationList` 和 Authentik 的 *My applications* 都提供这种视图，但它们的数据
模型完全不同，因此这不能是某个 IAM Module 的内部细节。

本设计把现有的 `APPS_LIST` 私有约定提升为 **Runner 拥有的应用目录契约**，与
[iam-capability-design.md](iam-capability-design.md) 的做法一致：Runner 解析并
发布事实，Provider 只负责把契约翻译成自己的对象模型。

本文是应用目录 schema、角色解析和 LLNG/Authentik Provider 映射的规范来源。Module 为什么
归入基础支持、平台管理服务或用户应用，由
[Module 分类与访问边界分析](../reviews/2026-08-19-module-classification.md)
说明；分类研究不重复定义本文字段。

硬约束：

1. **列表是显示过滤，不是访问控制。** 门户少显示一项不等于拒绝访问，多显示一项
   也不等于放行。授权始终由 IdP 的策略、`forward_auth` 网关或应用自身执行。
2. **权限只有一个事实来源。** 门户可见性是执行点授权规则的投影，不是与之并列的
   第二份规则。用户配置只能**收紧**可见性，不能放宽。
3. **静态展示元数据属于清单，动态值属于 Hook。** 名称、描述、图标、分类在清单里
   就能确定；URL 依赖域名计算，只能由 Hook 产生。清单声明"URL 在哪个变量里"，
   Runner 负责取值。
4. **目录不是 IAM 专属。** 契约按消费方发布，任何 Module（IAM 门户、独立仪表盘）
   都可以消费它。这是把它做成 Runner 契约而不是 LLNG 私有逻辑的全部理由。
5. **Provider 可以声明不支持某个字段，但不得自行发明语义。** Authentik 没有条目
   排序，就忽略 `ORDER`，而不是把 `ORDER` 塞进名称前缀。
6. **Module 分类、目录分类和授权角色互不替代。** `classification.class` 表示 Module
   是基础支持、平台管理服务还是用户应用；`launcher.category` 只决定卡片显示在哪个
   分区；`access` 才描述执行点和允许角色。尤其是 `category: admin` 不等于
   `SAMBA_DC_ADMIN_GROUP_DN`，也不能单独产生任何访问权限。

### 1.1 与 Module 三类模型的关系

应用目录消费 [Module 分类与访问边界分析](../reviews/2026-08-19-module-classification.md)
定义的主分类，但不把主分类直接翻译成授权：

| Module 主分类 | 默认目录行为 | 例外 |
| --- | --- | --- |
| `foundation` | Module 主体不发布 | Adminer、Dashboard 等独立管理面可发布到 `admin` |
| `admin_service` | 只有人员管理面发布到 `admin` | IAM Portal 是目录宿主，不给自己生成循环入口 |
| `user_app` | 可用的人员主入口发布到 `applications` | SMB 等无 HTTPS URL 的入口不生成假卡片 |

Runner 内置两个语义稳定的目录分类：

- `applications`：普通用户应用；
- `admin`：系统管理入口。

分类只包含名称、图标、顺序等展示元数据。`admin` 的安全约束是 Runner **校验其中的每个
条目都声明了管理员 audience 和真实执行点**，而不是把分类 ID 当作组。用户可以修改分类
显示名和顺序，也可以把条目移动到自定义显示分类，但不能借此放宽该条目的 `access`。

管理员入口统一声明 `access.role: platform_admin`，不直接写物理组名或 DN。输入、Runner
解析结果和 Provider 消费值的完整边界见 §4.1。

## 2. 当前实现的问题

现状：`APPS_LIST` 的条目和元数据仍是一组 Hook 之间的过渡约定，尚未实现本文设计的
完整 Runner catalog。Runner 只守住聚合键的所有权边界：声明 `APPS_LIST*` export 的
Module 只能保留已有列表并追加自己的 Module 名，聚合后的 `APPS_LIST` 归 Runner
所有；单个 Module 不能覆盖、重排或删除其他条目。这是阶段 A 前的兼容保护，不替代
后文的清单 schema、校验和两段式发布。

1. **只有三个 Module 参与。** 只有 `nextcloud`
   （[main.go:207](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/hook/main.go#L207)）和 `netbird`
   （[main.go:191](https://github.com/anas-project/ANAS/blob/master/modules/netbird/hook/main.go#L191)），以及 `meshcentral`
   （[main.go:191](https://github.com/anas-project/ANAS/blob/master/modules/meshcentral/hook/main.go#L191)）发布条目。
   `lam`、`collabora`、各 Adminer、Traefik dashboard、LLNG
   Manager、Authentik 自身都不在门户里，用户必须记域名。
2. **权限写了两份且互不校验。** LLNG 的 `display` 表达式读
   `APPS_LIST__<APP>__ALLOW_GROUPS`
   （[llng-config.sh:117](https://github.com/anas-project/ANAS/blob/master/modules/llng/llng/root/root/llng-config.sh#L117)），
   Authentik 的策略绑定读 `ANAS_IAM_CLIENT__<APP>__ALLOW_GROUPS`
   （[iam.go:287](https://github.com/anas-project/ANAS/blob/master/modules/authentik/hook/iam.go#L287)）。两者由不同代码路径
   产生，可以静默不一致——门户显示一个点进去被拒的应用，或者藏起一个用户其实
   有权访问的应用。
3. **没有分类契约。** LLNG 把所有应用硬编码进单一分类 `1apps` "Applications"
   （[llng-config.sh:103](https://github.com/anas-project/ANAS/blob/master/modules/llng/llng/root/root/llng-config.sh#L103)）；
   Authentik 的 `application.group` 根本没有设置。
4. **图标机制脆弱。** LLNG 靠 `after_start` 的 `docker cp` 把
   `LOGO_PATH` 拷进容器 htdocs
   （[main.go:165](https://github.com/anas-project/ANAS/blob/master/modules/llng/hook/main.go#L165)）。这是命令式的：容器重建
   后要重跑，路径依赖渲染产物位置——[2026-07-19-design-review.md](../reviews/2026-07-19-design-review.md)
   记录的就是 promote 后路径失效导致的启动破坏。Authentik 侧则完全没有图标。
5. **展示元数据重复。** `module.yml` 已经有 `title`、`description`、`category`，
   Hook 里又硬编码了一份 `NAME`/`DESC`，两者可以漂移。
6. **无法加入外部条目。** 用户没法把路由器管理页、机柜 PDU、外部 SaaS 这类
   非 ANAS 应用放进同一个门户。
7. **无用户覆盖。** 改一个应用的显示名或图标要改 Module 源码。
8. **管理员分类和管理员授权尚未建模。** 当前没有 `admin` 分区契约，也没有把
   `SAMBA_DC_ADMIN_GROUP_NAME`/`SAMBA_DC_ADMIN_GROUP_DN` 的使用位置区分清楚。把管理
   条目放进某个分类不会自动保护其 URL。
9. **LLNG Test 的当前执行规则过宽。** `LLNG_ENABLE_TEST=true` 时，Test 域当前配置为
   `accept`，应用菜单条目使用 `display: auto`；普通已登录用户可以直接访问。目标规则应与
   Manager 一样要求 `SAMBA_DC_ADMIN_GROUP_NAME`，目录显示也使用同一组事实。
10. **Adminer 只有数据库登录，没有 ANAS 人员用户系统。** PostgreSQL/MariaDB Adminer
    当前直接暴露 Traefik 路由，登录表单使用数据库服务器的 username/password；它没有
    可与 Samba `Admins` 自动对应的独立用户库。官方也建议通过 Web 服务器密码、IP allowlist
    或插件增加外围保护。ANAS 的目标应是 `platform_admin` ForwardAuth 作为第一层、数据库
    登录作为第二层，而不是二选一（[Adminer 安全建议](https://www.adminer.org/en/)）。

因此不建议继续在 Hook 里扩展 `APPS_LIST`。

## 3. 用户配置

新增顶层 `launcher` 段，以及 `modules.<app>.launcher` 覆盖块：

```yaml
launcher:
  # 仅覆盖显示名和排序。applications/admin 是 Runner 内置语义分类。
  categories:
    - id: office
      name: 办公协作
      order: 10
    - id: admin
      name: 系统管理
      order: 90

  # 非 ANAS 管理的外部条目。
  entries:
    - id: router
      name: 主路由
      description: OpenWrt 管理界面
      uri: https://192.168.1.1
      category: admin
      icon: ./branding/router.png
      audience: administrators
      access:
        # 外部 URL 没有 ANAS 可校验的执行点；role 只收紧目录可见性。
        via: external
        role: platform_admin

modules:
  nextcloud:
    launcher:
      name: 我的云盘
      description: 文件与协作
      category: office
      icon: ./branding/cloud.svg
      order: 10
      visibility: allowed
```

规则：

- `launcher` 是**显示层配置**，不参与 `modules.<app>.config` 的前缀转换。它不会
  变成 `NEXTCLOUD_LAUNCHER_NAME` 这类变量，而是并入 Runner 的目录解析结果。
  理由：这些值的消费方是门户 Module，不是应用自己，走应用前缀会让它们进错
  `.env`。
- `modules.<app>.launcher.allow_groups` 仅作为 `assigned_users` 用户应用的可选收窄项，且
  **只能是执行点组集合的子集**（约束 2）。给出超集时 Runner 报错，而不是悄悄放宽。
  管理员入口必须声明 `access.role: platform_admin`，不接受用户用物理组名覆盖角色。
- 分类不接受 `allow_groups`。`category: admin` 只是把条目放进“系统管理”分区；
  `audience: administrators` 和条目 `access` 才要求 Runner 解析、校验管理员角色。
- 内置 Module 的 `audience` 是 Manifest 事实，用户配置只能隐藏条目或移动显示分类，不能把
  `administrators` 改成 `authenticated_users`。
- `visibility` 取 `allowed`（默认）、`always`、`hidden`。
- 外部条目的 `id` 不得与任何 Module 名冲突。管理员外部条目使用
  `audience: administrators` + `access.via: external` + `access.role: platform_admin`，由
  Runner 解析目录可见组，不允许硬编码 `Admins` 或完整 DN。外部 URL 没有 ANAS 可校验的
  执行点，因此 `plan` 必须标记为 `source: config` 和 `catalog_visibility_only`——它只影响
  是否显示，不保证那个 URL 自身的授权。

## 4. 清单能力模型

新增可选的 `launcher` 段。现有 Module 不带该段仍然合法，因此这是纯增量变更，
**ABI 保持 `anas.module-hook/v1`**，不需要 v3。

单条目形式（绝大多数应用 Module）：

```yaml
launcher:
  publish: true
  category: applications
  # 缺省取清单的 title / description。
  name: Nextcloud
  description: Self hosted file sharing and communication
  icon: assets/nextcloud.png
  # 关键字段：URL 是动态的，但"URL 在哪个变量里"是静态的。
  uri_from: NEXTCLOUD_DOMAIN_FULL
  order: 50
  visibility: allowed
  audience: assigned_users
  access:
    via: iam
```

多条目形式（一个 Module 暴露多个界面）：

```yaml
launcher:
  entries:
    - id: llng_manager
      name: WebSSO Manager
      description: Configure LemonLDAP::NG
      icon: assets/configure.png
      uri_from: LLNG_MANAGER_DOMAIN_FULL
      category: admin
      audience: administrators
      access:
        via: native_group
        role: platform_admin
    - id: llng_test
      name: LLNG authentication test
      uri_from: LLNG_TEST_DOMAIN_FULL
      category: admin
      audience: administrators
      enabled_if: LLNG_ENABLE_TEST
      access:
        via: native_group
        role: platform_admin
```

Adminer 属于基础 Module 的可选管理面。它的数据库登录保留为第二层，外层 ForwardAuth
负责把人员入口限制到平台管理员：

```yaml
launcher:
  entries:
    - id: postgres_adminer
      name: PostgreSQL Adminer
      uri_from: POSTGRES_ADMINER_DOMAIN_FULL
      category: admin
      audience: administrators
      enabled_if: POSTGRES_ADMINER_ENABLED
      access:
        via: forward_auth
        role: platform_admin
        second_factor: database
```

#### Adminer 的两层认证边界

Adminer 只是数据库客户端登录界面，不提供可由 ANAS 同步的人员账号库
（[Adminer 请求与登录流程](https://github.com/vrana/adminer/blob/main/docs/developing.md)）。
`platform_admin` ForwardAuth 证明访问者是平台管理员，Adminer 随后的数据库 username/password
决定其数据库权限；两层都必须保留。

可以预选 driver、内部 server、port、database，最多预填非敏感 username；不得自动注入
PostgreSQL 超级用户、MariaDB root 或 Consumer resource password，也不得用共享密码静默
登录。真正的一键登录需要未来的 credential broker 为人员签发短期、可撤销、最小权限的
逐人数据库角色，不属于本目录契约。

字段约束：

- `publish` 缺省 `false`。不显式声明的 Module 不进目录，避免把 `postgres`、
  `lego` 这类没有界面的基础设施塞进用户门户。
- `uri_from` 必须是该 Module 自己前缀下的变量，或它 `config.consumes` 覆盖的变量。
  跨界读取沿用现有作用域规则，不为目录开后门。
- `audience` 取 `assigned_users`、`administrators`、`authenticated_users`。匿名入口不进
  已登录后的应用目录；本地恢复入口固定 `visibility: hidden`。
- `access.via` 取 `iam`、`forward_auth`、`native_group`、`local`、`external`、`none`，决定 §5 里
  `allow_groups` 的继承来源。`native_group` 表示应用自身用目录组执行授权，例如 LLNG
  location rule；`local` 表示目标仍有独立本地登录；`external` 表示 ANAS 只管理链接。
  后两者的目录角色都只控制卡片可见性。它描述的是
  **事实**（这个界面实际由谁把门），不是愿望；填错会被 §9 的一致性校验抓出来。
- `access.role: platform_admin` 由 Runner 按消费位置解析为管理员组名或 DN；Manifest 不应
  把 `category: admin`、`Admins` 字符串和完整 LDAP DN 混写。
- `icon` 是相对 Module 目录的路径。
- `enabled_if` 引用一个布尔或非空判定的变量，解决 Adminer 这类可选服务。

### 4.1 `platform_admin` 的输入、解析和输出

管理员入口只在设计输入层使用语义角色，物理组名和 DN 由 Runner 从身份拓扑解析：

| 层 | 规范表示 | 示例 | 约束 |
| --- | --- | --- | --- |
| 内置 Module 输入 | `audience: administrators` + `access.role: platform_admin` + 实际 `access.via` | Adminer 使用 `forward_auth`，LLNG Test 使用 `native_group` | Module 作者声明；用户只能隐藏，不能放宽或改执行点 |
| 外部链接输入 | 同一 audience/role，`access.via: external` | 路由器管理页 | 只控制目录可见性，`plan` 标记 `catalog_visibility_only` |
| Runner 规范化结果 | 保留 `ACCESS_ROLE=platform_admin`，另生成具体 `ALLOW_GROUPS` | 当前通常为 `Admins` | 物理值是输出，不回写 Manifest/config |
| Provider/执行点 | 按接口使用组名或 DN | 见下表 | 目录显示和真实执行点必须使用同一角色事实 |

| 消费位置 | Runner 应提供的事实 |
| --- | --- |
| LLNG `inGroup()`、Authentik policy、OIDC/SAML group claim、ForwardAuth | `SAMBA_DC_ADMIN_GROUP_NAME` |
| LAM 等直接 LDAP `memberOf` 过滤 | `SAMBA_DC_ADMIN_GROUP_DN` |
| `launcher.category` | 两者都不用；分类只负责展示 |

因此管理员入口不得在 Manifest 或顶层外部链接中写 `allow_groups: Admins`，更不能写完整 DN。
`ANAS_APP_ENTRY__*__ALLOW_GROUPS=Admins` 可以出现在 Runner 解析后的环境契约中，这是预期输出。
当前 `oauth2_proxy.allow_groups` 仍是物理配置且默认 `Admins`；迁移期间 Runner 必须验证它与
`platform_admin` 的解析结果一致，最终应由角色绑定派生而不是由用户重复维护。

截至本文版本，这些 launcher 字段仍是目标 schema，当前 Manifest parser、Adminer
ForwardAuth 路由和角色派生尚未实现，不能把设计示例误读成已生效配置。

`identity.application_group: true`（已存在）与 `launcher.publish` 是两件事：前者
决定 Samba AD 里是否创建 `APP_<module>` 组，后者决定是否进门户。一个应用可以有组
但不进门户（纯 API 客户端），也可以进门户但不限制组（`visibility: always`）。

## 5. 权限模型

每个条目的最终组集合 `ALLOW_GROUPS` 先按 `audience` 确定，再核对执行点：

1. `audience: administrators` → `access.role` 必须是 `platform_admin` 或未来更窄的已定义
   管理角色；`platform_admin` 的 claim 组名解析为 `SAMBA_DC_ADMIN_GROUP_NAME`。它**不包含**
   `APP_all` 或任何 `APP_<module>`；
2. `audience: assigned_users` + `access.via: iam` → 继承该应用的
   `ANAS_IAM_CLIENT__<APP>__ALLOW_GROUPS`，通常是
   `APP_<应用名>,APP_all,Admins`；
3. `audience: authenticated_users` → 空集合，表示所有已登录用户；只能由 Manifest 明确
   声明，不能由用户覆盖放宽；
4. `access.via: forward_auth` 或 `native_group` → 上述解析集合必须与实际网关或应用原生
   location/policy 规则一致；
5. `access.via: local` → 目标仍以本地凭据授权，目录组只能收紧卡片可见性，Runner 必须在
   `plan` 标记 `catalog_visibility_only`，不能声称目录组就是执行点权限；
6. `access.via: external` → `access.role` 只用于目录可见性；管理员条目的
   `platform_admin` 解析为 `SAMBA_DC_ADMIN_GROUP_NAME`，并标记 `catalog_visibility_only`。

用户配置 `modules.<app>.launcher.allow_groups` 只能是上述解析集合的子集，不能放宽。
正式契约中多个组固定按 **OR / any** 解释：用户应用命中
`APP_<应用名>`、`APP_all`、`Admins` 任意一个即可；管理员条目只命中管理员角色。当前 LLNG
Hook 的“给所有条目统一追加管理员组”应移到 Runner 的 `assigned_users` 解析中，绝不能把
`APP_all` 反向追加到管理员条目。固定语义可避免两个 IAM adapter 对同一份目录声明得出
不同授权结果。

Adminer 的人员管理员门禁与数据库登录是串联关系，具体边界和禁止共享超级用户自动登录的
规则见 §4“Adminer 的两层认证边界”。

LLNG adapter 还必须把每个应用声明的 claim 源属性加入 `ldapExportedVars`，再写入对应
RP 的 exported vars；否则配置中虽然出现 `preferred_username: sAMAccountName`，LLNG
会话里却没有 `sAMAccountName`。生成的 OIDC RP 统一启用
`oidcRPMetaDataOptionsIDTokenForceClaims`，因为 Nextcloud 等客户端直接从 ID token 读取
用户名，不保证再调用 UserInfo。Runner/E2E 同时校验“目录属性已加载、RP claim 已导出、
应用 mapping 指向该 claim”这三个层次。

`visibility` 的语义：

| 值 | 含义 | 典型场景 |
| --- | --- | --- |
| `allowed` | 只对满足 `ALLOW_GROUPS` 的用户显示 | 默认 |
| `always` | 对所有已登录用户显示，忽略组 | 无授权限制的公共应用 |
| `hidden` | 注册客户端但不进目录 | `oauth2_proxy` 自身、纯 API 客户端 |

门户列表本身要求已登录，因此不存在匿名可见性档位。

**为什么可见性必须是执行点的投影而不是独立规则**：两种偏离都有害且都不会报错。
显示集大于执行集，用户看到点进去被拒的应用；显示集小于执行集，用户发现不了自己
有权用的应用。把它定义成投影，"一致"就成了构造上的性质，而不是需要人去维护的
巧合；用户配置只允许收紧，是唯一不会引入新执行语义的放松方式。

## 6. 环境变量契约

Runner 发布，owner 为合成的 `runner`（与 §6 身份拓扑同样处理），只有显式
`config.consumes` 的 Module 才收到：

```dotenv
ANAS_APP_CATALOG=lam,netbird,nextcloud,router
ANAS_APP_CATEGORIES=applications,admin
ANAS_APP_CATEGORY__APPLICATIONS__NAME=应用
ANAS_APP_CATEGORY__APPLICATIONS__ORDER=10
ANAS_APP_CATEGORY__ADMIN__NAME=系统管理
ANAS_APP_CATEGORY__ADMIN__ORDER=90

ANAS_APP_ENTRY__NEXTCLOUD__NAME=我的云盘
ANAS_APP_ENTRY__NEXTCLOUD__DESCRIPTION=文件与协作
ANAS_APP_ENTRY__NEXTCLOUD__URI=https://cloud.nas.example.com
ANAS_APP_ENTRY__NEXTCLOUD__CATEGORY=applications
ANAS_APP_ENTRY__NEXTCLOUD__ORDER=10
ANAS_APP_ENTRY__NEXTCLOUD__ICON_NAME=nextcloud.png
ANAS_APP_ENTRY__NEXTCLOUD__ALLOW_GROUPS=APP_nextcloud,APP_all,Admins
ANAS_APP_ENTRY__NEXTCLOUD__AUDIENCE=assigned_users
ANAS_APP_ENTRY__NEXTCLOUD__ACCESS_VIA=iam
ANAS_APP_ENTRY__NEXTCLOUD__VISIBILITY=allowed
ANAS_APP_ENTRY__NEXTCLOUD__SOURCE=module:nextcloud

# 以下是 Runner 把 platform_admin 解析后的组名输出，不是 Manifest 输入，也不是 LDAP DN。
ANAS_APP_ENTRY__LLNG_TEST__CATEGORY=admin
ANAS_APP_ENTRY__LLNG_TEST__ALLOW_GROUPS=Admins
ANAS_APP_ENTRY__LLNG_TEST__AUDIENCE=administrators
ANAS_APP_ENTRY__LLNG_TEST__ACCESS_VIA=native_group
ANAS_APP_ENTRY__LLNG_TEST__ACCESS_ROLE=platform_admin

ANAS_APP_ICONS_DIR=<release>/apps/icons
```

`SOURCE` 区分 `module:<name>` 与 `config`，让 Provider 和 `plan` 输出都能说明一个
条目是从哪来的；这在排查"门户里为什么有这一项"时是最先要回答的问题。

条目 id 到变量名的转换与现有契约一致：大写，`-` 转 `_`。

### 6.1 两段式发布

URL 依赖各 Module 的 `calculate`，分类和图标不依赖。因此契约分两次写出，正好落在
现有生命周期的缝隙里，**不需要改动 Hook 阶段顺序**：

1. 所有 `calculate` 之前：Runner 发布 `ANAS_APP_CATALOG`、全部分类变量，以及每个
   条目的 `NAME`、`DESCRIPTION`、`CATEGORY`、`ORDER`、`ICON_NAME`、`VISIBILITY`、
   `AUDIENCE`、`ACCESS_VIA`、`ACCESS_ROLE`、`SOURCE`。这些只依赖清单和用户配置。
2. 所有 `calculate` 之后、任何 `render_env` 之前：Runner 按 `uri_from` 取值填入
   `URI`，按 §5 解析填入 `ALLOW_GROUPS`（此时 `ANAS_IAM_CLIENT__*__ALLOW_GROUPS`
   已由各应用的 `calculate` 发布）。

Provider 在 `render_env` 里读到的永远是完整目录。这与 IAM 注册请求的时序完全
同构：Runner 先发名单，应用在 `calculate` 里补自己的字段，Provider 在
`render_env` 里读全量——单向依赖，不成环。

第 2 步意味着 `nextcloud`/`netbird`/`meshcentral` Hook 里那几行 `APPS_LIST__*` 赋值可以整段
删掉：它们做的事就是把 `NEXTCLOUD_DOMAIN_FULL` 抄到另一个变量名下。

## 7. 图标契约

`docker cp` 换成声明式挂载：

- Runner 在渲染时把每个条目的图标收敛到产物内的 `apps/icons/<id>.<ext>`，并发布
  `ANAS_APP_ICONS_DIR` 指向 promote 之后的稳定路径。这正是
  [2026-07-19-design-review.md](../reviews/2026-07-19-design-review.md) 里那个
  "calculate 阶段用临时渲染路径构造持久值" 缺陷的正解。
- Provider 的 compose 以只读 bind mount 挂载该目录，容器重建幂等，
  `after_start` 的 `copy_portal_logos` 整个删除。
- 校验：扩展名限 `.png`/`.svg`/`.webp`，单文件 ≤ 256 KiB，文件必须存在。在
  `plan` 阶段失败，不等到 `start`。
- 缺省图标由 Runner 提供一个内置占位，不允许每个 Provider 各自 fallback——否则
  同一部署换个门户，图标就变了。

Provider 侧落点：

- LLNG：挂到 `/usr/share/lemonldap-ng/portal/htdocs/static/common/apps/`，
  `applicationList/.../options logo` 填 `ICON_NAME`。
- Authentik：挂到 media 目录（例如 `/media/public/anas/`），blueprint 的
  `meta_icon` 填相对 media 的路径。**这一点未经真实实例验证**，首次部署时需要
  对照当时版本的 Authentik 文档复核，与 §12 的其他未验证项同样处理。

## 8. Provider 映射

Provider 在清单里声明：

```yaml
capabilities:
  provides:
    - name: app_launcher
      interfaces:
        - portal
```

与 `iam` 不同，`app_launcher` **不设"一个部署只能有一个"的约束，也没有
`launcher.provider` 配置项**。目录是只读显示数据，两个门户同时渲染它不产生冲突，
也不产生第二个会话域。谁在部署里，谁就渲染。这正是契约化之后才可能出现的收益：
将来加一个独立仪表盘 Module，不需要改动任何应用 Module。

### 8.1 LLNG：Portal 承载双分区目录

LLNG 的 Portal 是登录入口和目录宿主，不给 Portal 自己创建卡片。Runner 发布的条目映射为
两类 `applicationList` category：

| Runner 分类 | LLNG category | 普通用户 | `platform_admin` |
| --- | --- | --- | --- |
| `applications` | `1anas_applications` / “应用” | 只显示其 `ALLOW_GROUPS` 允许的条目 | 显示其获授权应用 |
| `admin` | `9anas_admin` / “系统管理” | 分类为空，因此不显示 | 显示 Manager、Test、LAM、DDNS、Adminer、Dashboard 等获授权管理条目 |

每个条目的 `options display` 直接由 Runner 的最终 `ALLOW_GROUPS` 生成 `inGroup()` OR 表达式。
LLNG 官方的 Application list 支持按规则显示条目，但菜单显示仍不是 URL 授权，因此 Manager、
Test 和其他 Handler/ForwardAuth 路由必须使用同一组事实执行限制
（[LLNG Portal menu](https://lemonldap-ng.org/documentation/2.0/portalmenu.html)）。

LLNG 自身入口的目标声明为：

| 入口 | 目录 | audience | 执行点 | 规则 |
| --- | --- | --- | --- | --- |
| Portal | 不生成卡片 | `authenticated_users` | LLNG Portal | 登录成功后承载目录 |
| Manager | `admin` | `administrators` | LLNG location rule | `inGroup(SAMBA_DC_ADMIN_GROUP_NAME)` |
| Test | `admin`，仅 `enable_test=true` | `administrators` | LLNG location rule | `inGroup(SAMBA_DC_ADMIN_GROUP_NAME)` |
| 本 Module 的可选 Adminer | `admin`，仅组件真实存在且启用 | `administrators` | ForwardAuth + database | 管理员门禁后再数据库登录 |

当前 Test 的 `locationRules/$LLNG_TEST_DOMAIN default=accept` 必须改成与 Manager 相同的
管理员规则；`applicationList/.../test_auth/options display` 也必须使用同一个
`inGroup(SAMBA_DC_ADMIN_GROUP_NAME)` 表达式，不能继续用 `auto` 或 `on`。普通用户无论从目录
点击还是直接输入 Test URL 都必须被拒绝。`enable_test` 控制入口是否存在，不改变 audience。

LLNG adapter 只独占并重建 `1anas_applications`、`9anas_admin` 两个 category；其他 category
原样保留。所有 ANAS Module 和用户 `launcher.entries` 都进入这两个 Runner-owned category，
管理员不得在 LLNG Manager 里手工修改它们；需要持久化的外部链接必须写回 ANAS 配置。这样
既不会静默删除非 ANAS 分类，也不会让同一条目同时受 LLNG 手工配置和 Runner 配置控制。

### 8.2 Authentik：Application Dashboard 承载同一目录

Authentik 使用 `authentik_core.application` 渲染 Application Dashboard：

- `group` 映射 Runner 分类显示名（“应用”“系统管理”）；
- `meta_launch_url`、`meta_description`、`meta_icon` 映射目录字段；
- `meta_hide: true` 精确实现 `visibility: hidden`。项目固定版本为 2026.5.6，2026.5 已新增
  “Hide from Application Dashboard”，不再使用旧的 `blank://blank` 兼容技巧
  （[Authentik 2026.5 release](https://docs.goauthentik.io/releases/2026.5/)）；
- `policy_engine_mode: any` 与 ANAS 的 OR 组语义一致；
- 每个 `visibility: allowed` 条目都生成显式 policy binding。Authentik 默认在无 binding 时
  允许所有用户，因此 adapter 不能把“没有生成 policy”解释为“拒绝”
  （[Authentik application bindings](https://docs.goauthentik.io/add-secure-apps/applications/manage_apps/)）。

ANAS 管理的 Authentik 实例还应把 `core_default_app_access` 设为 `false` 作为 fail-closed
兜底；即使某个新条目漏绑 policy，也不能默认向所有用户开放。显式
`visibility: always` 的条目则生成一条“已认证用户”允许策略，而不是依赖全局默认。

目录条目与 IAM client 必须解耦：Authentik Application 的 Provider 可为空，因此 LAM、
Traefik Dashboard、Adminer 这类由本地登录或 ForwardAuth 保护的 URL 仍可创建“仅启动链接”
Application；Nextcloud、MeshCentral 等 IAM Consumer 则同时关联对应 Provider。不能再像当前
`writeApplicationEntry` 一样假设每张卡片都有 `provider-<slug>`。

Authentik 自身入口的目标声明为：

| 入口 | 目录 | audience | 执行点 | 规则 |
| --- | --- | --- | --- | --- |
| Application Dashboard | 不生成卡片 | `authenticated_users` | Authentik | 登录用户的目录宿主 |
| Admin interface | `admin` | `administrators` | Authentik superuser/role | 仅 Samba `Admins` 映射 superuser |
| `akadmin` recovery | `hidden` | 本地恢复 | Authentik inbuilt backend | 永不进入日常目录 |

普通用户只能看到通过 `APP_<module>`/`APP_all` 等 policy 的普通应用；管理员可以同时看到
“系统管理”组。Authentik 的 policy binding 同时控制卡片可见性和 Authentik application
launch，但目标 URL 仍须保留自身的 ForwardAuth、原生管理员角色、本地认证或网络隔离。

| 契约字段 | LLNG | Authentik |
| --- | --- | --- |
| `CATEGORY` | `applicationList/<order><id> catname` | `application.group`（单层字符串） |
| `ORDER` | 分类与条目的 key 排序前缀 | **不支持**，忽略 |
| `NAME` / `DESCRIPTION` | `options name` / `description` / `tooltip` | `attrs.name` / `meta_description` |
| `URI` | `options uri` | `attrs.meta_launch_url` |
| `ICON_NAME` | `options logo` | `attrs.meta_icon` |
| `ALLOW_GROUPS` | `options display` 的 `inGroup()` 表达式 | 表达式策略 + `policybinding`（已实现） |
| `AUDIENCE` / `ACCESS_ROLE` | 校验 location rule/Handler 与目录表达式一致 | 校验 application policy 与目标角色一致 |
| `VISIBILITY: always` | `display: on` | 显式“已认证用户”允许策略 |
| `VISIBILITY: hidden` | 不创建条目 | `meta_hide: true` |

Authentik 缺少条目排序、缺少多级分类，这是能力差异，按约束 5 如实忽略并在
`plan` 输出里提示一次，而不是伪造实现。

## 9. 校验

全部在 `plan` 阶段完成，与现有能力解析同批次：

- 条目引用了未定义的分类；
- `uri_from` 指向的变量在解析后为空，而条目 `visibility` 不是 `hidden`；
- `icon` 文件不存在、扩展名或大小越界；
- 条目 id 与 Module 名冲突，或两个条目 id 相同；
- 分类声明 `allow_groups`，或代码试图从 `category: admin` 推导组——分类只能有展示元数据；
- `category: admin` 条目没有 `audience: administrators`，或管理员条目包含 `APP_all`/
  `APP_<module>`；
- 管理员外部条目直接写 `allow_groups: Admins`、管理员组 DN 或其他物理组标识，而没有声明
  `access.via: external` + `access.role: platform_admin`；
- `access.role: platform_admin` 在 claim/policy 上没有解析为 `SAMBA_DC_ADMIN_GROUP_NAME`，
  或在 LDAP filter 上错误地没有使用 `SAMBA_DC_ADMIN_GROUP_DN`；
- 用户配置的 `allow_groups` 不是执行点集合的子集；
- `access.via: iam` 但该 Module 没有 IAM 绑定，或 `access.via: forward_auth` 但
  部署里没有 `forward_auth` 提供方——这条是把 §5 的继承链从"填什么都行"变成可
  验证的原因；
- Adminer 条目标记 `audience: administrators`，但路由既没有与 `platform_admin` 一致的
  ForwardAuth，也没有声明为管理网络隔离；数据库登录不能替代这项人员入口检查；
- LLNG Test 启用后，目录表达式或 `locationRules/$LLNG_TEST_DOMAIN` 不是
  `platform_admin`；这应作为安全错误而不是 warning；
- **组名字符集** `^[A-Za-z0-9 _-]+$`。这是安全校验，不是洁癖：组名会被拼进
  LLNG 的 Perl `display` 表达式和 Authentik 的 Python 策略表达式，两处都是代码
  上下文，`yamlString` 只挡得住 YAML 层。

错误信息带上条目、来源和修复动作：

```text
launcher entry "nextcloud" references category "media", which is not defined;
define it under launcher.categories or use one of: applications, admin, office
```

```text
modules.nextcloud.launcher.allow_groups adds "Staff", which is not enforced
anywhere; the portal would show nextcloud to users who cannot open it.
allow_groups may only narrow the enforced set: APP_nextcloud, APP_all, Admins
```

## 10. 生命周期与状态

- 所有 `launcher.*` 变更的 effect 是 `container_recreate`：LLNG 的配置脚本和
  Authentik 的 blueprint 都在容器启动时执行，重建即生效，不涉及数据迁移，也不需
  要 `reconcile` 的多步协调。
- 图标内容变更同样是 `container_recreate`；Runner 应把图标内容摘要纳入渲染产物
  的输入，否则只改图片不改配置时不会触发重建。
- **目录不进锁文件。** 它不产生需要跨部署稳定的绑定（不像 `iam.provider`），重算
  是幂等的，写进锁只会多一份可能过期的副本。

## 11. 实施阶段

| 阶段 | 内容 |
| --- | --- |
| A | Runner 契约：清单 `launcher`、`audience`、`access.role`、两段式发布和 §9 校验；同时双写旧 `APPS_LIST*` 保持现有 Module 可用 |
| B | LLNG 改读新契约，生成 `applications`/`admin` 双分区；Manager 与 Test 统一限制 `platform_admin`；图标改挂载，删除 `after_start: copy_portal_logos` 与旧图标变量 |
| C | Authentik 用 `application.group`、`meta_hide` 和显式 fail-closed policy binding 渲染同一目录；支持无 Provider 的启动链接；补分类与图标 |
| D | 给 `lam`、DDNS、各 Adminer、Traefik Dashboard、LLNG Manager/Test、Authentik Admin interface 补 `launcher` 声明；Adminer 增加 `platform_admin` ForwardAuth；删除三个用户应用 Hook 中的 `APPS_LIST*` 代码 |
| E | 用户配置覆盖与外部条目 |

A 到 D 之间旧契约保持可用，因此每一阶段都能单独渲染验证；`APPS_LIST` 的删除放在
最后一步，避免中途出现两个门户数据源。

## 12. 已知限制与未验证点

- Authentik 的 `meta_icon` 路径形状、`meta_hide` 的 blueprint 写法、Admin interface 的稳定
  launch URL 和 `core_default_app_access` 自动配置方式仍须在固定版本 2026.5.6 的真实实例验证。
- Authentik 只有单层分组且无排序，`ORDER` 与多级分类在该 Provider 上退化。
- LLNG Manager 对 `1anas_applications`、`9anas_admin` 的手工修改会在下一次 reconcile 被
  Runner 覆盖；管理 UI 和运维文档必须明确标识这两个分类为 ANAS-owned。
- 目录**不表示服务可用**。列表里有条目不代表容器在跑；健康状态是另一个能力，不
  在本设计范围，也不应该偷偷塞进 `VISIBILITY`。
- 匿名（未登录）门户不在范围。
- 同一 Module 的不同界面按条目解析 `audience` 和 `access`；Manager/Test、Portal/Admin、
  primary/recovery 不共享一套组集合。
