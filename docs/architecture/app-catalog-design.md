---
doc_type: architecture
status: partial
updated: 2026-10-11
---

# 应用目录协议

**状态：部分实施；第一阶段使用原生列表，第二阶段门户待实现。** 2026-10-03 按未发版前提重写，替代原 `APPS_LIST` 扩展方案，
不保留双写、旧配置迁移或旧字段别名。本文满足
[应用目录要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/app-catalog.md)
`APPCAT-R-001`—`APPCAT-R-039`；进度见
[实施计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/app-catalog.md)。
Casdoor 是首要交付目标；2026-10-10 用户要求直接移除 Authentik，当前保留 Casdoor 与 LLNG。
未发版不增加旧 Provider 兼容别名、自动改绑或业务数据迁移。
2026-10-04 补充多语言：首期支持简体中文 `zh-CN` 和英文 `en`，应用与分类的展示字段使用语言映射。
2026-10-08 明确两级展示：先分用户应用和后台管理两个大组，再在大组内按主分类分小组。

## 1. 应用条目与登录客户端分别声明

一个条目代表一个供人打开的入口。是否接入 OIDC/SAML 不决定是否进入目录；
本地登录、LDAP 登录、ForwardAuth 后台、外部网站均可声明。
一个 Module 可发布多个入口，例如 PostgreSQL Adminer、应用主界面和管理员后台。
没有人员入口的数据库、TURN、ACME 等不自动生成卡片。

目录只表示入口及当前用户可见性。服务健康、业务权限和数据库权限继续由相应系统负责。
`kind: user/admin` 决定入口用途与大组，`categories` 决定大组内的小组及分类筛选，管理员授权必须明确绑定
`platform_admin`，不能从“系统管理”文字推导。

三份事实各有来源：

- Module `applications`：默认展示资料、真实入口、执行点声明、资源文件；
- workspace `application_catalog`：分类定义、展示覆盖和外部条目；
- 现有 identity/IAM 注册：OIDC/SAML 协议、client secret、claim、允许组和回调。

目录通过 `access.client` 引用 IAM 注册。一个 IAM client 可以被多个入口引用；
仅有目录条目不注册可用于登录的 OAuth client 或 SAML SP。
Casdoor 的原生 Application 初始化会自动补客户端字段；目录适配禁用登录，不授予授权类型或回调地址，
这些字段不能被当作目录条目的可用登录凭据。

## 2. 条目字段

名称统一用 `snake_case`；所有未知字段拒绝，避免错拼后静默使用默认值。
下表描述规范化后的 `anas.app-catalog/v1`，并注明声明层允许的简写。

| 字段 | 定义与校验 |
| --- | --- |
| `name` | 稳定应用名/程序标识，全目录唯一，`^[a-z][a-z0-9_-]{0,62}$`；改名等价于删旧建新 |
| `display_name` | 按语言映射的显示名，各值 1—100 字符；不参与身份或权限匹配 |
| `url` | 点击入口，必须完整保留 query 和 fragment；默认仅 HTTPS，外部 HTTP 必须显式允许并在 plan 提示 |
| `summary` | 按语言映射的卡片简介，各值为纯文本，1—160 字符 |
| `description` | 按语言映射的详情描述，各值为纯文本，1—4000 字符；支持换行，不解析 HTML |
| `documentation_url` | 按语言映射的 HTTPS 文档地址，可指官方或 ANAS 文档；允许两种语言使用相同地址 |
| `weight` | 0—10000 的整数，默认 100；同一小组内数值越小越靠前，同权重按 `name` 字典序 |
| `categories` | 非空、去重且保留声明顺序的分类 ID 列表；第一项决定卡片所属小组，所有项参与分类筛选，同一条目只显示一张卡片 |
| `icons` | 16/32/48/64/128/256/512 像素的方形 PNG 文件记录，详见 §4 |
| `kind` | `user` 对应“用户应用”大组，`admin` 对应“后台管理”大组；每个条目只属于一个大组，管理入口不能借改分类改变角色要求 |
| `enabled` | 可选功能实际启用后才为 true；不是健康状态 |
| `hidden` | 默认 false；true 时任何用户的日常目录均不返回该项，包括 IAM 管理员 |
| `access` | 实际访问方式及其已有授权事实，见 §3 |
| `visibility` | 明确的门户显示过滤，见 §3；省略与空允许列表都不是允许所有用户 |
| `source` | Runner 填写 `module/config`、Module 名及配置来源；用户不能伪造 |

Module 清单的 `url_from` 与 `url` 二选一。`url_from` 引用自身导出或已声明消费的变量，
在全部 `calculate` 完成后取值；不能为空或引用未获准的变量。
`enabled_by` 只引用本 Module 已声明的布尔配置，在归一化后变为 `enabled`。
应用展示字段显式提供语言映射，不从单语 Module `title/description` 猜测翻译；
简介、描述分别声明，不能继续把一段文案当成两种字段。
Module 的 `category` 保持其 Module inventory 用途，不自动冒充条目的分类列表。

示意声明（尚不可执行）：

```yaml
applications:
  - name: nextcloud
    display_name:
      zh-CN: Nextcloud
      en: Nextcloud
    url_from: NEXTCLOUD_DOMAIN_FULL
    summary:
      zh-CN: 文件、照片与团队协作
      en: Files, photos and team collaboration
    description:
      zh-CN: 存储和共享文件，并使用已启用的日历、联系人与在线办公功能。
      en: Store and share files, with enabled calendar, contacts and online office features.
    documentation_url:
      zh-CN: https://docs.nextcloud.com/server/latest/user_manual/en/
      en: https://docs.nextcloud.com/server/latest/user_manual/en/
    weight: 20
    categories: [files, collaboration]
    kind: user
    icons:
      source: assets/nextcloud-512.png
      sizes:
        32: assets/nextcloud-32.png
    access:
      mode: iam
      client: nextcloud
    visibility:
      mode: access
```

### 2.1 中英文与回退

首期目录语言键仅接受 `zh-CN`（简体中文）与 `en`（英文）；结构允许未来增加语言，
当前输入其他键报错，避免把拼错的语言当成有效翻译。字段统一使用映射，不另加单语字符串简写。
`display_name/summary/description/documentation_url` 与分类的 `display_name/description` 可本地化。
分类描述可整体省略；一旦提供，每个语言值必须非空。每个文本值单独校验长度与纯文本约束，
每个文档地址分别校验 URL；不能只校验部署默认语言。

内置 Module 和内置分类必须提供中英文文案，CI 检查完整性。产品品牌名可在两种语言中相同，
文档没有中文版时可在 `zh-CN` 下明确填写英文文档地址，不制造不存在的译文链接。
外部条目与自定义分类每个必需展示字段至少提供一种语言即可，缺失翻译按以下规则回退。
不在运行时调用自动翻译服务。

语言来源优先级：**用户在门户明确选择的语言 → 浏览器首选语言 → `global.default_language` → `en`**。
仅当前来源未提供语言时才取下一来源；选出的语言不受支持时，**直接使用 `en`**，
不继续尝试浏览器次选语言或部署默认语言。浏览器首选指有效偏好中权重最高的具体语言，
同权重保留原顺序；权重为零的项和通配符不作为具体语言。
复用现有语言匹配设施：`en-US/en-GB` 等匹配 `en`，`zh/zh-Hans/zh-CN/zh-SG` 匹配 `zh-CN`；
繁体中文本期未提供，不将 `zh-TW/zh-HK/zh-Hant` 当作已有繁体翻译。
例如用户选择 `fr`，即使浏览器和部署默认均为中文，也显示英文；没有用户选择且浏览器首选
`ja` 时同样显示英文。完全没有用户/浏览器语言时才采用部署默认，默认语言不支持则为英文。
`global.default_locale` 的日期/数字格式设置不替代显示语言。Casdoor 内部 `zh` 由 adapter
映射到目录 `zh-CN`，不改变协议的规范语言键。

选定语言后，每个字段按“**选定语言 → en → zh-CN**”依次取首个存在的值，
重复候选跳过；空字符串不是合法译文。这样中文单语的外部条目在英文门户也不会消失或空白。
这里的字段缺译回退与“不支持的语言默认英文”分别处理：门户语言仍是英文，
仅外部条目未提供英文的字段显示其已有中文；内置文案必须有英文。
`name`、分类 `id`、入口 `url`、权重、图标与权限不随语言变化；本期各语言共用七档图标。
入口不自动拼接语言参数，打开应用后由应用自己的语言机制处理。

展示覆盖按“字段 + 语言键”合并：只覆盖 `display_name.zh-CN` 时保留 Module 的英文值。
分类覆盖也采用相同规则；不支持用空字符串/null 删除必需译文。配置来源和回退结果应能在 plan 中解释。
完整语言映射随 `catalog.json` 冻结并参与摘要，修改任一译文都走原有部署更新流程。
不在构建时按部署默认语言丢弃另一种语言，也不复制两套条目或图标。

认证 API 先做权限过滤，再按选定语言返回解析后的字符串字段和响应级 `language`；
不因某语言缺译而改变条目可见性。门户切换语言重新取得已过滤结果，无需重新登录或部署。
API 与页面使用同一回退算法；如缓存结果，必须同时隔离用户与语言。
目录自身的“用户应用”“后台管理”“打开应用”“文档”“分类”“无可用应用”等界面文字也需中英文资源，
不能只翻译应用文案而把操作按钮留成单语。

## 3. 权限过滤与执行点

### 3.1 第一版只接受已能解释和验证的过滤

固定 Casdoor 源码实验已验证组/角色/用户允许规则、多个组 OR、撤组后的下一次查询，
同时暴露默认允许和管理员绕过。证据与复现见
[Casdoor 应用目录研究](/research/casdoor-app-catalog)。
协议第一版采用**正向组允许列表**和 `platform_admin` 语义角色，不暴露 Casbin 自定义模型、
脚本表达式、任意 claim、用户标签、正则、跨组织规则或 allow/deny 混合。
角色在目录发布前解析成 IAM 已同步的组；具体 IAM 原生角色仍留在 adapter 内，不能由配置任意引用。
不提供按用户名长期授权，避免目录改名破坏匹配；未来按永久 anchor 的个人例外需另行验证。

`visibility` 必须是以下三种之一：

- `mode: access`：继承已有执行点规则。`iam` 取对应 client 的准入组，
  `forward_auth` 取网关角色；原生目录组授权由 Module 声明其同源组事实。
- `mode: subjects`：`any_of` 中列出 `{group: APP_design}` 或 `{role: platform_admin}`，
  任一命中即显示；至少一项，未知组/角色拒绝。用于本地登录或外部入口，标记为“仅过滤目录”。
- `mode: authenticated`：明确表示所有有效登录用户；仅用于没有 ANAS 组门禁的用户入口，
  不能覆盖已有 IAM/ForwardAuth 限制，不能用于 `kind: admin`。

所有模式均先检查用户有效、组织正确、条目启用及未隐藏。
组取 IAM 当前维护的用户组事实，不从浏览器传入值或自助可改资料获得。
管理入口必须最终只允许 `platform_admin`；不能混入 `APP_all` 或普通应用组。
`platform_admin` 由 identity Provider 发布的组名事实解析，不能在 Core 硬编码产品名、组名或 DN。

### 3.2 `access` 的表达

| `mode` | 必需关联 | 目录与目标之间的关系 |
| --- | --- | --- |
| `iam` | `client` | 登录资格取已有 client；同一 client 的普通主入口共用其准入规则 |
| `forward_auth` | 已解析的 forward_auth binding 与 `role: platform_admin` | 网关实际保护路由，目录继承同一角色 |
| `native` | Module 声明的目录组执行点和组事实引用 | 例如 LDAP 管理后台；必须有真实访问反例证明映射 |
| `local` | 无 IAM client | 目标继续要求本地凭据；目录过滤不代表目标认证成功 |
| `external` | workspace 外部条目 | ANAS 只维护链接和目录过滤，不声明控制外部网站授权 |

若应用后台需要比其主界面更窄的角色，则单独声明管理条目及原生管理执行点，
不能以普通 IAM client 的准入权限冒充应用内部管理员权限。
恢复账号登录页、IAM 自己的门户首页、只供机器使用的 client 默认不发布。

第一版展示覆盖只能改名称、文案、图标、文档地址、权重、分类或隐藏状态；
不能改变 Module 的 `kind/access/visibility`。需要调整应用准入时修改该应用既有授权配置，
目录随后重新派生，避免出现两份允许组。外部条目由配置完整声明自己的显示规则。

### 3.3 第二阶段门户的安全边界

Casdoor `GetAllowedApplications` / `CheckLoginPermission` 不能直接作为完整目录契约：
没有有效 Application Permission 时默认允许，组织管理员还会绕过原生检查。
`DisableSignin` 也不隐藏卡片；应用 `Tags` 的登录检查没有被这个列表函数覆盖。

第二阶段门户通过 Casdoor OIDC 建立门户会话，并读取当前用户/组和权限判定，先做显式的
`hidden/enabled/组织/有效用户/预期策略存在且已启用批准` 检查，再直接调用其原生 Permission enforcer 求值生成的组允许规则；该引擎不经过列表函数的管理员提前放行。
不得把原生 `IsAdmin` 当作目录超级通行证；需要管理员可见时规则明确包含解析后的管理员组。
关联 IAM client 的条目还要通过原生 client 准入检查，防止目录放宽登录资格。
筛选在服务端完成，分页和计数在筛选后执行；未授权响应不携带被过滤条目的描述、URL 或文档地址。
异常、缺失策略、无匹配组和不支持的条件全部不返回条目，并记录可定位的配置问题。

Casdoor 需要管理和同步的组是 IAM client 准入组与目录条目引用组的并集。
不能仅遍历 OIDC/SAML client，否则只用于 LAM、本地后台或外部条目的组不会进入同步/撤权链路。
目录角色和组沿用已有目录同步事实；受管 Permission 使用可识别的目录名称空间，
在现有 apply/启动配置协调入口创建、更新和移除，只处理本部署拥有的对象。
写入失败或期望策略不一致时目录条目不开放；不通过新守护进程或定时全量复制维护元数据。

上述是第二阶段目标行为，第一阶段原生列表无法保证全部约束。源码实验验证了基础与反例，
尚未证明自己的门户已实现这些约束。
策略故障及管理员不绕过等反例必须纳入后续 HTTP 和浏览器 E2E。

### 3.4 第一阶段的声明与计划边界

目录 Provider 在 manifest 中声明 application_catalog.consumes: true 和 limitations 列表，
Compose 必须把整个目录只读挂到 /run/anas/application-catalog；公网目录只能挂资产子目录。
Runner 为 render Hook 临时注入 ANAS_APP_CATALOG_FILE，.env 只保留最终 ANAS_APP_CATALOG_DIR
与 ANAS_APP_CATALOG_DIGEST。Hook 不可改写这些值或把暂存文件路径写入输出。
共享文件属于不可变 deployment，图标内容变更同时改变消费者摘要；历史 deployment 保留原图。
plan 的 application_catalog_digest 包含配置相对图标源的内容摘要，apply 重新检查计划摘要。

选中的目录 Provider 以 identity.roles.platform_admin 指向自身组导出，以 identity.groups_from
发布其可识别的目录组集合。当前 Samba 发布 Admins、文件共享组、启用的 APP_* 与 CAP_* 组；
未知引用和空的准入集合拒绝渲染。IAM access.client 引用发布 Module 的真实注册；
ForwardAuth role 必须匹配实际绑定网关与目录角色；native.groups_from 必须声明为可读环境键。
本地/外部入口用 visibility.subjects 仅过滤目录，目标 URL 继续由应用认证。

当前文件共享只支持本地 Unix Docker endpoint。选定 SSH/TCP endpoint 或无法确认当前 context
会在 plan 拒绝，不把本机路径交给远程 daemon。还没有远程制品投影实现。

## 4. 多尺寸图标和共享文件

### 4.1 输入、尺寸与质量

统一交付 `16,32,48,64,128,256,512` 七档 PNG；每档包含 `path/media_type/width/height/sha256`。
Module 提供至少 512×512、最多 2048×2048 的方形 PNG 源图，可逐档提供专门设计的图片，
尤其是小尺寸细节。Runner 对缺少的档位按固定算法缩小，不放大；UI 按显示尺寸与像素密度选择。
未提供任何图标时，由 Core 固定图案生成完整七档占位图，并在 plan 提示。Nextcloud/NetBird 使用随 Module 打包的 512px PNG。

Module 作者可从 SVG 制作 PNG，但第一版不把 SVG、HTML、远程 URL 或动画文件直接交给浏览器。
这样无需引入 SVG 执行/字体/外部资源清洗链路。自定义图标随配置工作区保存，不在 apply 时联网下载。
PNG 输入限单文件 2 MiB、解码像素 2048²；七档输出每应用合计最多 4 MiB，部署共享图标总量最多
64 MiB；在解析图片前检查头部/尺寸，扩展名不能代替真实内容校验。
缩放采用 `golang.org/x/image/draw`：新增的 Go 编译依赖仅用于确定性重采样，
不新增运行服务或宿主命令；如实施时发现已有等价依赖，应复用并记录版本。

### 4.2 一个目录制品，不创建文件共享服务

本期只定义**应用目录的只读文件共享**，不扩张成运行时通用读写文件总线。
图标不是业务数据、Secret、TLS 私钥或 Module 持久卷，不挂载整个 Module 目录。

```text
<deployment>/shared/application-catalog/
  catalog.json                 # 完整规范化条目、分类与 provenance，不能公网静态发布
  assets.json                  # 源/输出摘要、尺寸、媒体类型、来源记录
  assets/<sha256>.png           # 实际内容寻址文件；相同内容去重
```

路径边界：Module 来源相对 Module 根；配置来源相对 workspace 配置根。
禁止绝对路径、`..`、符号链接、越界解析、特殊文件及从 Secret/data 目录复制。
源文件只读，生成文件进入新的 deployment 制品；将源字节摘要、缩放器版本、结果和元数据纳入
输入摘要。不能只在 YAML 改动时才触发更新。
资产清单保留来源、版权/许可说明和生成信息；不得把应用商标当成 ANAS 自有版权。

Module→Runner→Consumer 的共享过程：

1. Runner 从已启用声明收集公开资产；所有 Hook `calculate` 完成后一次性解析 URL、角色和分类，
   生成目录制品。消费目录不添加启动顺序依赖，避免 IAM 与数据库 Adminer 成环。
2. 只有声明消费 `application_catalog` 的 Module 的 render Hook 得到
   `ANAS_APP_CATALOG_FILE`（本次暂存文件，只用于渲染读取）与
   `ANAS_APP_CATALOG_DIR`（最终 deployment 上的宿主目录，用于生成挂载）。
   临时渲染路径不能写进持久配置。其他 Module 不获得跨模块文件访问权。
3. compose/runtime 把制品只读挂到 `/run/anas/application-catalog`。
   Casdoor 可把 **assets 子目录**只读挂到自身现有 `/files/anas-catalog/assets` 静态目录；
   `catalog.json` 和 `assets.json` 不挂到公网目录，API 读取后按当前用户过滤。
4. 浏览器只收到 provider 的 HTTPS 图标 URL，例如
   `/files/anas-catalog/assets/<sha256>.png`。它不认识宿主路径；不存在的资源返回 404，
   Content-Type 固定 `image/png`，禁止目录浏览，声明 `nosniff` 和按摘要缓存。

目录制品在生成时显式设置目录 `0755`、JSON/图标文件 `0644`，不依赖生成进程的 umask；
deployment 封存后继续移除文件写位。这样不同 UID 的消费进程可以读取只读挂载，
宿主私有 deployment 父目录和认证 API 仍控制完整目录访问，不将其发布成公网静态目录。

图片是公开品牌资源，公开图片 URL 不授予应用访问权；不得包含账号、内网信息或敏感截图。
有保密需求的条目使用通用占位图。未经筛选的目录和真实 URL 则必须留在认证 API 内。
浏览器/API 响应不缓存跨用户的目录结果；目录资产可长缓存，二者生命周期分开。

消费 runtime 所在宿主必须能读取本次制品。当前复用 deployment 的上传/投影流程；
不能把操作端本机路径直接传给远程 Docker daemon。不支持共享制品投影的 runtime 在 plan 阶段拒绝，
不静默降级到 `docker cp`、NFS 或新同步服务。

图标或元数据变更按现有 apply 更新受影响的目录消费者；首次实现采用容器重建，
不新增热更新守护进程。旧 deployment 持有自己的完整目录制品，回滚读取旧制品；
清理沿用 deployment 引用与保留规则，不删除仍被历史部署使用的资源。

## 5. 两级分组与外部配置

### 5.1 大组与小组

门户固定两级结构，复用 `kind` 与 `categories`，不新增 group/parent 字段或任意深度分类树。
这部分复杂度低：只对已授权条目做确定性分组，不增加组件、依赖、持久副本或同步流程。

| 大组与固定顺序 | `kind` | 英文标题 | 小组示例 |
| --- | --- | --- | --- |
| 1. 用户应用 | `user` | User applications | 文件、协作、影音、开发 |
| 2. 后台管理 | `admin` | Administration | 数据库、网络、身份与权限、监控 |

一个条目只归属一个大组。一个 Module 同时提供用户界面和独立管理后台时，使用不同 `name`
分别声明两个入口，各自指定 `kind`、分类和权限。只有数据库进程、TURN、纯 API 等后端服务，
没有可打开的人员界面时不生成条目；有管理界面的服务发布其管理入口。
大组标题使用门户内置中英文资源，小组显示名来自分类语言映射，均遵循 §2.1 的语言选择规则；
未支持语言直接使用英文，切换语言不改变分组、权限或顺序。

分类注册表全局共用，同一分类可出现在两个大组下，例如用户网络工具与网络管理后台均使用
`network`；分组位置由 `(kind, categories[0])` 确定，不能把两个大组的同名小组合并。
类别本身不带授权规则，后台管理仍由条目明确绑定 `platform_admin`。

展示顺序如下：

1. 服务端按 §3 过滤身份、权限、hidden 和 enabled，再向门户返回可见条目。
2. 有分类筛选时，对可见条目的完整 `categories` 列表匹配；选择多个分类按任一匹配保留。
3. 按 `kind` 分大组，再按第一项主分类分小组。附加分类只参与筛选，不复制卡片或改变卡片所属小组。
4. 大组按上表顺序；各大组内的小组按分类 `weight` 升序、同权重按分类 ID 排序；
   小组内卡片按条目 `weight` 升序、同权重按 `name` 排序。条目权重不跨小组比较。
5. 权限过滤及当前筛选后没有卡片的小组、大组隐藏；全部为空时显示“无可用应用”。

例如 Nextcloud 的 `kind: user`、`categories: [files, collaboration]` 只在“用户应用 → 文件”
出现一张卡片。筛选“协作”仍能找到它，卡片继续归属“文件”，不会复制到“协作”小组。
因此规范化及摘要排序必须保留 `categories` 的声明顺序，不能按 ID 重排主分类。

### 5.2 分类配置与外部条目

workspace 顶层 `application_catalog` 是完整配置入口。内置小组分类提供
`files/collaboration/media/development/database/network/identity/monitoring/security/other`，
不再用 `applications/admin` 充当内置小组，避免与大组用途混淆。允许补充自定义分类；
分类字段为 `id/display_name/description/weight`，显示名和描述按 §2.1 提供语言映射。
`other`（其他 / Other）仅供显式选择，不能用它掩盖空分类或未知分类 ID。

以下示例包含分类配置、展示覆盖和不使用 IAM 登录的外部后台；路由器位于“后台管理 → 网络”：

```yaml
application_catalog:
  categories:
    - id: files
      display_name: {zh-CN: 文件, en: Files}
      description: {zh-CN: 文件存储与共享, en: File storage and sharing}
      weight: 10
    - id: collaboration
      display_name: {zh-CN: 协作, en: Collaboration}
      weight: 20
  overrides:
    nextcloud:
      display_name:
        zh-CN: 家庭云盘  # 仅覆盖中文；英文继续使用 Module 声明
      weight: 10
      icons:
        source: branding/cloud-512.png
  entries:
    - name: external_router
      display_name: {zh-CN: 主路由管理, en: Main router}
      url: https://router.example.net/
      summary: {zh-CN: 家庭网络与 Wi-Fi 管理, en: Home network and Wi-Fi management}
      description:
        zh-CN: 使用路由器自己的管理员账号登录。
        en: Sign in with the router's administrator account.
      documentation_url:
        zh-CN: https://openwrt.org/docs/guide-user/start
        en: https://openwrt.org/docs/guide-user/start
      weight: 80
      categories: [network]
      kind: admin
      icons:
        source: branding/router-512.png
      access:
        mode: external
      visibility:
        mode: subjects
        any_of:
          - role: platform_admin
```

外部条目与 Module 条目共用全部显示、图标、分类和过滤校验；名称冲突拒绝，不能覆盖 Module 身份。
管理员直接编辑配置后走 `plan/apply`；删除 entry 就撤销目录条目及其受管权限，
不删除外部应用或账号。外部 HTTP 通过条目 `allow_http: true` 显式放行，
禁止 `javascript/data/file`、URL 内用户名密码；不探测或抓取目标页面。
内置条目的 URL、执行点不允许通过展示 override 偷换；确需自定义目的地时新增外部条目。

## 6. Casdoor 与 LLNG

### Casdoor（首要目标，第一阶段原生投射）

现有模型可对应 `Name/DisplayName/Logo/Order/HomepageUrl/Description`，但一个 `Logo` 和一段
`Description` 不能完整承载本协议。`Category` 是 Default/Agent 协议分类，`Tags` 带登录限制，
两者禁止映射展示分类。原生卡片还会无条件给 HTTP URL 追加 `silentSignin=1`，
新入口必须原样打开配置 URL，具体 SSO 起始 URL 由应用自己发布。
目录 API 与页面必须保留中英文切换能力；写入原生单字符串字段的值只能作为部署默认语言的
显示投影，完整翻译继续来自目录制品，不能用每次用户切换语言就更新数据库的方式实现。

2026-10-08 用户确认分阶段交付。第一阶段不修改 Casdoor API 或页面，只在现有 Hook 中把目录
投射到原生 Application 和 Permission。显示名、简介、URL、单个 128px 图标和顺序写入原生字段；
本版本原生字段最大 100 字符，标题/简介/URL 超长会拒绝渲染，不截断链接。
不把展示分类映射到 Category/Tags，不伪造纯目录条目的 OAuth/SAML 凭据。
Casdoor 自动补齐的 clientId/clientSecret 保留为原生对象内部字段，grantTypes/redirectUris 为空，
disableSignin 为 true，enablePassword 为 false。原生列表中的 clientSecret 是掩码；
验收须检查实际协议拒绝签发和密钥掩码，不能只断言数据库中的客户端字段为空。
只有目录用途的原生 Application 使用独立受管命名空间，启动时清理配置已删除的这类对象和权限，
保留已有 IAM client 与非 ANAS 对象。

原生限制沿用上游行为：点击追加 silentSignin、无有效 Permission 默认允许、IAM 管理员绕过、
hidden/禁用的 IAM client 对象可能仍被枚举。配置解析拒绝空或未知组不能消除这些运行时缺口。
因此第一阶段不是完整过滤/保密保证，直接访问仍由目标 IAM/ForwardAuth/应用本地认证保护。

第二阶段实现自己的门户，完成全部文案、两级分组、附加分类筛选、七档图标选择、原样链接和严格
认证后过滤；推荐组件部署位置与复杂度见 §10；仍是提案，本次不实施新的 API 或页面。

### LLNG（保留，能力须另验）

按新协议实现适配，不保留 `APPS_LIST` 兼容层。
LLNG 2.23.2 的菜单数据支持递归，但默认 Portal 模板只渲染一层 category；真实运行曾遗漏
嵌套分类中的卡片。当前 Hook 将用途与主分类合成“用户应用 / 分类”“后台管理 / 分类”的原生组名，
条目直接放在组内，完整两级布局由第二阶段门户实现，
以明确的 inGroup OR 作为 display 规则，不自动附加管理员。图标通过资产子目录只读挂载。
同一配置的旧受管分类会被替换，不复制源 Module 文件、不做 after_start 图标复制。
原生界面的语言选择不切换应用文案；完整分类筛选、文档链接及多尺寸展示仍属第二阶段。
这些显示缺口写入 Module 的 application_catalog.limitations 并进入 plan。

### Authentik（已移除）

2026-10-10 用户决定替代先前逐步弃用路线。模块源码、镜像/Module 发布清单、专属测试脚本与
当前配置均移除；选择旧 Module/provider 时明确报错，不自动改成 Casdoor。Git 历史与旧计划中的
版本、失败和通过记录保留，不能把它们重新标为 Casdoor 验收。本次仓库移除不操作既有外部部署、
身份绑定、账号或业务数据。

## 7. Adminer 与 Module 覆盖

PostgreSQL/MariaDB Adminer 的目标声明均为 `kind: admin`、`access.mode: forward_auth`、
`role: platform_admin`、`visibility.mode: access`，且只在 `adminer_enabled` 时发布。
两套入口均使用 `categories: [database]`，展示于“后台管理 → 数据库”。
网关负责平台管理员身份，Adminer 继续用数据库凭据登录；不注入数据库超级用户密码。
两套 compose 目前都已有 `ANAS_FORWARD_AUTH_MIDDLEWARE` 路由，oauth2_proxy 固定派生管理员组；
新版目录声明和实际授权事实校验已实现；Casdoor 实际会话下的双数据库门禁/回调已在隔离主机完成（范围与证据见 plan），门户严格过滤仍待实现；
不重复增加第二层代理。

首轮覆盖由 Module inventory 派生，不能仅遍历 IAM client：

| 入口 | 纳入方式 |
| --- | --- |
| Nextcloud、Forgejo、Vikunja、MeshCentral、NetBird 人员 UI | 用户条目；按各自实际协议与准入声明；平台存在不代表所有 Casdoor 组合已验收 |
| LAM、DDNS、Traefik Dashboard、Collabora 管理台、数据库 Adminer | 分别核实 native/local/ForwardAuth，作为管理入口；本地登录不冒充 SSO |
| Casdoor 管理 UI、LLNG Manager/Test | 单独管理入口；日常门户不为自己生成循环卡片，恢复入口隐藏 |
| Collabora 文档编辑、SMB、TURN、纯 API 服务 | 没有独立可打开的 HTTP 人员入口时显式标“不发布”，不伪造地址 |
| Immich 等后续 Module | 按同一 schema 声明，其独立接入状态不被本目录设计提前标记完成 |

管理后台中哪些是日常管理、哪些仅是 break-glass 恢复入口，应逐个核实，不能按产品名一律发布。
所有没有发布的 Module 都要有原因；新增 Module 的文档/CI 检查应用声明或“不适用”说明。

## 8. 与当前协议和前稿的差异

| 当前代码 / 原提案 | 本次修改 |
| --- | --- |
| Hook 追加 `APPS_LIST`；同一 Module 基本一项 | Manifest `applications` 统一列表，可多个入口，Runner 聚合 |
| `NAME` 混用标识与文案 | `name` 稳定标识 + `display_name` 展示 |
| 单 `DESC` | `summary` 与 `description` 分开 |
| 单 `LOGO_PATH/LOGO_NAME`；LLNG 启动后复制 | 七尺寸 PNG、摘要清单、deployment 制品、只读挂载、浏览器 HTTPS URL |
| 没有文档字段 | 必需 `documentation_url`；卡片/详情有独立文档入口 |
| 单语展示字符串 | 应用/分类展示字段采用 `zh-CN/en` 映射；文档按语言选择，逐字段回退、逐语言覆盖 |
| 无统一权重；前稿单分类 | 固定 `kind` 大组 → 主分类小组；组内确定性 `weight` 排序，附加分类筛选去重 |
| 门户与 IAM 允许组分别写 | 引用执行点规则；本地/外部规则明确仅管目录 |
| 主要跟随 OIDC/SAML client 生成 | 发布目录与注册登录客户端独立 |
| 前稿 `visibility: always` 含糊 | 显式 authenticated；缺失/空权限失败关闭；hidden 包括管理员 |
| 前稿允许展示覆盖收窄组 | 首版不增加第二份内部应用权限覆盖，统一从执行点派生 |
| 前稿 Runner→LLNG→Authentik 双写迁移 | 未发版直接替换旧目录协议，Casdoor 优先，Authentik 已移除 |
| 前稿列 Adminer ForwardAuth 为未来工作 | 代码已存在，补目录与实机验收 |
| 前稿仅 `ANAS_APP_ICONS_DIR` | 完整目录制品，区分 render 暂存路径、宿主挂载路径、容器路径和公开 URL |

## 9. 实施边界

Core 只理解通用 schema、归一化、文件安全边界和制品；不包含 Casdoor/LLNG 产品字段。
第一阶段 Provider adapter 负责原生对象和原生权限规则，沿用原生界面，无新增服务。
第二阶段交付独立门户，负责完整展示与严格过滤；不建立应用/人员数据库副本或后台文件同步器。
实现步骤、阻塞与验收记录仅维护在配套计划中。

Module 的消费声明固定为 `application_catalog: { consumes: true }`，由 Runner 识别；
不把目录消费伪装成另一个 IAM client，也不建立对每个发布应用的 capability 依赖。
制品 JSON 顶层为 `schema: anas.app-catalog/v1`、`languages: [zh-CN, en]`、
`default_language`（从全局默认匹配，无法匹配时为 `en`）、`categories`、`applications`；
资产清单为 `schema: anas.app-catalog-assets/v1`、`files`，两者都不带构建时间等不稳定字段。
对象/无序集合规范排序后计算摘要，每个条目的 `categories` 列表保留声明顺序；
同输入产生相同字节，不支持的 schema 版本必须拒绝读取。

## 10. 第二阶段：独立用户门户（首版已确认，尚未实现）

用户新增决定：第二阶段交付独立门户网站，并增加 LDAP 本人改密及获权管理员的分组、
密码重置功能。此前 Casdoor 同进程页面提案不再作为推荐部署方案；第一阶段原生功能与历史验收不变。

独立门户使用 Casdoor OIDC 登录，消费同一只读目录与七档图标，提供自己的受保护 API。
完整应用目录仍遵守本协议：当前账号与权限过滤、策略失效拒绝、原样 URL、中英文切换、
未知语言直接英文、两级分组与空组隐藏；原生 API/旧图标旁路仍须处理，独立页面不会自动解决泄漏。

Casdoor 继续负责 IAM 认证与应用授权事实；Samba AD 继续负责真实用户、组与密码。
门户不维护另一套用户/组数据库，也不使用原生管理员绕过或旧 JWT groups 代替当前授权。
原生用户 token 的 UserInfo/get-account 当前本人读取与固定组过滤已完成能力实验，
按此路径接线，当前不新增 Casdoor 受限接口或源码补丁；门户与资产保护仍未实现。

2026-10-11 用户确认首版包括本人资料/原密码及首次/过期改密、范围受限用户查询、
重置/强改/解锁和成员增删，配套近期认证与审计。动作、人员、组按同一委派规则限定，
Admins 不自动取得账号写权，生产名单由配置显式给出。
整体高复杂度分步方向已确认；存储推荐单实例内存会话与私有持久审计，能力实验在实现前完成。
账号创建/启停、找回、MFA、跨应用会话管理与便利功能仍是后续候选。
详细机制见 [门户设计](/architecture/user-portal-design)，新增判据的规范来源为
[PORTAL-R-001—R-041](https://github.com/anas-project/ANAS/blob/master/modules/user_portal/dev-docs/requirements/user-portal.md)，
不替代原 APPCAT 矩阵。


### 退出协议的独立最小元数据

2026-10-19 用户确认：为清理当前浏览器的旧会话，固定退出回调可以匿名读取已部署应用的域名与固定退出协议路径；已有登录用户退出也可读取。该元数据独立于业务目录，没有名称、图标、简介、权限、文档及业务 URL。APPCAT-R-014 的完整目录、用户目录 API 和资产过滤继续生效；具体机制及当前验收范围见[门户设计](/architecture/user-portal-design)。
