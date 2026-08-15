# 应用目录（Application Catalog）设计

## 1. 目标与硬约束

部署里的服务应当以**面向用户的应用列表**呈现：用户登录门户后看到自己有权访问
的应用，按分类排列，每个条目有可配置的名称、描述、图标和顺序。LLNG 的
`applicationList` 和 Authentik 的 *My applications* 都提供这种视图，但它们的数据
模型完全不同，因此这不能是某个 IAM Module 的内部细节。

本设计把现有的 `APPS_LIST` 私有约定提升为 **Runner 拥有的应用目录契约**，与
[iam-capability-design.md](iam-capability-design.md) 的做法一致：Runner 解析并
发布事实，Provider 只负责把契约翻译成自己的对象模型。

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

## 2. 当前实现的问题

现状：`APPS_LIST` 是一组 Hook 之间的口头约定，Runner 完全不参与。

1. **只有两个 Module 参与。** 只有 `nextcloud`
   （[main.go:207](https://github.com/anas-project/ANAS/blob/master/modules/nextcloud/hook/main.go#L207)）和 `netbird`
   （[main.go:191](https://github.com/anas-project/ANAS/blob/master/modules/netbird/hook/main.go#L191)）发布条目。
   `lam`、`meshcentral`、`collabora`、各 Adminer、Traefik dashboard、LLNG
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
   后要重跑，路径依赖渲染产物位置——[design-review-2026-07-19.md](../research/design-review-2026-07-19.md)
   记录的就是 promote 后路径失效导致的启动破坏。Authentik 侧则完全没有图标。
5. **展示元数据重复。** `module.yml` 已经有 `title`、`description`、`category`，
   Hook 里又硬编码了一份 `NAME`/`DESC`，两者可以漂移。
6. **无法加入外部条目。** 用户没法把路由器管理页、机柜 PDU、外部 SaaS 这类
   非 ANAS 应用放进同一个门户。
7. **无用户覆盖。** 改一个应用的显示名或图标要改 Module 源码。

因此不建议继续在 Hook 里扩展 `APPS_LIST`。

## 3. 用户配置

新增顶层 `launcher` 段，以及 `modules.<app>.launcher` 覆盖块：

```yaml
launcher:
  # 用户可见分类。未声明时 Runner 使用内置默认分类集。
  categories:
    - id: office
      name: 办公协作
      order: 10
    - id: infra
      name: 基础设施
      order: 90
      # 整个分类的可见性；与条目自身的规则取交集。
      allow_groups: Admins

  # 非 ANAS 管理的外部条目。
  entries:
    - id: router
      name: 主路由
      description: OpenWrt 管理界面
      uri: https://192.168.1.1
      category: infra
      icon: ./branding/router.png
      allow_groups: Admins

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
- `modules.<app>.launcher.allow_groups` **只能是执行点组集合的子集**（约束 2）。
  给出超集时 Runner 报错，而不是悄悄放宽。
- `visibility` 取 `allowed`（默认）、`always`、`hidden`。
- 外部条目的 `id` 不得与任何 Module 名冲突。外部条目没有执行点，因此它的
  `allow_groups` 就是它自己的定义，Runner 不做子集校验，但必须在文档和 `plan`
  输出里标记为 `source: config`——它只影响是否显示，不影响那个 URL 能不能打开。

## 4. 清单能力模型

新增可选的 `launcher` 段。现有 Module 不带该段仍然合法，因此这是纯增量变更，
**ABI 保持 `anas.module-hook/v1`**，不需要 v3。

单条目形式（绝大多数应用 Module）：

```yaml
launcher:
  publish: true
  category: office
  # 缺省取清单的 title / description。
  name: Nextcloud
  description: Self hosted file sharing and communication
  icon: assets/nextcloud.png
  # 关键字段：URL 是动态的，但"URL 在哪个变量里"是静态的。
  uri_from: NEXTCLOUD_DOMAIN_FULL
  order: 50
  visibility: allowed
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
      category: infra
      access:
        via: forward_auth
      # 该条目只在这个变量非空时进入目录，用于可选服务。
      enabled_if: LLNG_MANAGER_DOMAIN_PREFIX
    - id: llng_adminer
      name: LLNG Adminer
      uri_from: LLNG_ADMINER_DOMAIN_FULL
      category: infra
      enabled_if: LLNG_ADMINER_ENABLED
      access:
        via: forward_auth
```

字段约束：

- `publish` 缺省 `false`。不显式声明的 Module 不进目录，避免把 `postgres`、
  `lego` 这类没有界面的基础设施塞进用户门户。
- `uri_from` 必须是该 Module 自己前缀下的变量，或它 `config.consumes` 覆盖的变量。
  跨界读取沿用现有作用域规则，不为目录开后门。
- `access.via` 取 `iam`、`forward_auth`、`none`，决定 §5 里 `allow_groups` 的
  继承来源。它描述的是**事实**（这个界面实际由谁把门），不是愿望；填错会被 §9
  的一致性校验抓出来。
- `icon` 是相对 Module 目录的路径。
- `enabled_if` 引用一个布尔或非空判定的变量，解决 Adminer 这类可选服务。

`identity.application_group: true`（已存在）与 `launcher.publish` 是两件事：前者
决定 Samba AD 里是否创建 `APP_<module>` 组，后者决定是否进门户。一个应用可以有组
但不进门户（纯 API 客户端），也可以进门户但不限制组（`visibility: always`）。

## 5. 权限模型

每个条目的最终组集合 `ALLOW_GROUPS` 由 Runner 按以下顺序确定，第一个命中的生效：

1. 用户配置 `modules.<app>.launcher.allow_groups`（必须是下面继承结果的子集）；
2. `access.via: iam` → 该应用的 `ANAS_IAM_CLIENT__<APP>__ALLOW_GROUPS`，即
   Authentik 策略绑定和 LLNG 规则**已经在用**的那一份；
3. `access.via: forward_auth` → 保护它的网关的 `allow_groups`
   （`oauth2_proxy` 当前默认 `Admins`）；
4. `access.via: none` → 空集合，表示不限制；
5. 外部条目 → `launcher.entries[].allow_groups` 原样。

然后 Runner 统一追加管理员组 `SAMBA_DC_ADMIN_GROUP_NAME`。这条兜底今天写死在
LLNG 的 shell 里（`groups_filter="$groups_filter | inGroup('$SAMBA_DC_ADMIN_GROUP_NAME')"`），
正式契约中所有组固定按 **OR / any** 解释：命中任意一组即可访问。当前不提供
`access.match` 配置项；如果将来增加，它只能显式取 `any` 或 `all`，而本方案生成的
`APP_<应用名>,APP_all,Admins` 必须保持 `any`。固定语义可避免两个 IAM adapter 对同一份
目录声明得出不同授权结果。
搬到 Runner 后 Authentik 侧自动获得同样语义，不必在两份脚本里各写一次。

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
ANAS_APP_CATEGORIES=office,infra
ANAS_APP_CATEGORY__OFFICE__NAME=办公协作
ANAS_APP_CATEGORY__OFFICE__ORDER=10
ANAS_APP_CATEGORY__INFRA__NAME=基础设施
ANAS_APP_CATEGORY__INFRA__ORDER=90
ANAS_APP_CATEGORY__INFRA__ALLOW_GROUPS=Admins

ANAS_APP_ENTRY__NEXTCLOUD__NAME=我的云盘
ANAS_APP_ENTRY__NEXTCLOUD__DESCRIPTION=文件与协作
ANAS_APP_ENTRY__NEXTCLOUD__URI=https://cloud.nas.example.com
ANAS_APP_ENTRY__NEXTCLOUD__CATEGORY=office
ANAS_APP_ENTRY__NEXTCLOUD__ORDER=10
ANAS_APP_ENTRY__NEXTCLOUD__ICON_NAME=nextcloud.png
ANAS_APP_ENTRY__NEXTCLOUD__ALLOW_GROUPS=APP_nextcloud,Admins
ANAS_APP_ENTRY__NEXTCLOUD__VISIBILITY=allowed
ANAS_APP_ENTRY__NEXTCLOUD__SOURCE=module:nextcloud

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
   `SOURCE`。这些只依赖清单和用户配置。
2. 所有 `calculate` 之后、任何 `render_env` 之前：Runner 按 `uri_from` 取值填入
   `URI`，按 §5 解析填入 `ALLOW_GROUPS`（此时 `ANAS_IAM_CLIENT__*__ALLOW_GROUPS`
   已由各应用的 `calculate` 发布）。

Provider 在 `render_env` 里读到的永远是完整目录。这与 IAM 注册请求的时序完全
同构：Runner 先发名单，应用在 `calculate` 里补自己的字段，Provider 在
`render_env` 里读全量——单向依赖，不成环。

第 2 步意味着 `nextcloud`/`netbird` Hook 里那几行 `APPS_LIST__*` 赋值可以整段
删掉：它们做的事就是把 `NEXTCLOUD_DOMAIN_FULL` 抄到另一个变量名下。

## 7. 图标契约

`docker cp` 换成声明式挂载：

- Runner 在渲染时把每个条目的图标收敛到产物内的 `apps/icons/<id>.<ext>`，并发布
  `ANAS_APP_ICONS_DIR` 指向 promote 之后的稳定路径。这正是
  [design-review-2026-07-19.md](../research/design-review-2026-07-19.md) 里那个
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

| 契约字段 | LLNG | Authentik |
| --- | --- | --- |
| `CATEGORY` | `applicationList/<order><id> catname` | `application.group`（单层字符串） |
| `ORDER` | 分类与条目的 key 排序前缀 | **不支持**，忽略 |
| `NAME` / `DESCRIPTION` | `options name` / `description` / `tooltip` | `attrs.name` / `meta_description` |
| `URI` | `options uri` | `attrs.meta_launch_url` |
| `ICON_NAME` | `options logo` | `attrs.meta_icon` |
| `ALLOW_GROUPS` | `options display` 的 `inGroup()` 表达式 | 表达式策略 + `policybinding`（已实现） |
| `VISIBILITY: always` | `display: on` | 不创建策略绑定 |
| `VISIBILITY: hidden` | 不创建条目 | 不设 `meta_launch_url`（Authentik 的应用库不展示无启动 URL 的应用，**待验证**） |

Authentik 缺少条目排序、缺少多级分类，这是能力差异，按约束 5 如实忽略并在
`plan` 输出里提示一次，而不是伪造实现。

## 9. 校验

全部在 `plan` 阶段完成，与现有能力解析同批次：

- 条目引用了未定义的分类；
- `uri_from` 指向的变量在解析后为空，而条目 `visibility` 不是 `hidden`；
- `icon` 文件不存在、扩展名或大小越界；
- 条目 id 与 Module 名冲突，或两个条目 id 相同；
- 用户配置的 `allow_groups` 不是执行点集合的子集；
- `access.via: iam` 但该 Module 没有 IAM 绑定，或 `access.via: forward_auth` 但
  部署里没有 `forward_auth` 提供方——这条是把 §5 的继承链从"填什么都行"变成可
  验证的原因；
- **组名字符集** `^[A-Za-z0-9 _-]+$`。这是安全校验，不是洁癖：组名会被拼进
  LLNG 的 Perl `display` 表达式和 Authentik 的 Python 策略表达式，两处都是代码
  上下文，`yamlString` 只挡得住 YAML 层。

错误信息带上条目、来源和修复动作：

```text
launcher entry "nextcloud" references category "media", which is not defined;
define it under launcher.categories or use one of: office, infra
```

```text
modules.nextcloud.launcher.allow_groups adds "Staff", which is not enforced
anywhere; the portal would show nextcloud to users who cannot open it.
allow_groups may only narrow the enforced set: APP_nextcloud, Admins
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
| A | Runner 契约：清单 `launcher` 段、两段式发布、§9 校验；同时双写旧 `APPS_LIST*` 保持现有 Module 可用 |
| B | LLNG 改读新契约；图标改挂载，删除 `after_start: copy_portal_logos` 与 `LOGO_PATH`/`LOGO_NAME` |
| C | Authentik 补分类与图标；策略绑定与门户可见性统一读 §5 的解析结果 |
| D | 给 `lam`、`meshcentral`、`collabora`、各 Adminer、Traefik dashboard、LLNG Manager、Authentik 自身补 `launcher` 声明；删除 `APPS_LIST*` 与 `nextcloud`/`netbird` Hook 里的对应代码 |
| E | 用户配置覆盖与外部条目 |

A 到 D 之间旧契约保持可用，因此每一阶段都能单独渲染验证；`APPS_LIST` 的删除放在
最后一步，避免中途出现两个门户数据源。

## 12. 已知限制与未验证点

- Authentik 的 `meta_icon` 路径形状、以及"无 `meta_launch_url` 的应用不出现在
  应用库"这两条行为未经真实实例验证，首次部署时须复核。
- Authentik 只有单层分组且无排序，`ORDER` 与多级分类在该 Provider 上退化。
- 目录**不表示服务可用**。列表里有条目不代表容器在跑；健康状态是另一个能力，不
  在本设计范围，也不应该偷偷塞进 `VISIBILITY`。
- 匿名（未登录）门户不在范围。
- 一个应用的多个界面共享同一套执行点组集合。若将来出现"同一应用不同界面不同
  授权"的需求，`access` 需要按条目而不是按 Module 解析；当前多条目形式已经把
  `access` 放在条目上，因此这是扩展而非重构。
