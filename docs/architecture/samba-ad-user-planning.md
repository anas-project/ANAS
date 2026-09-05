# Samba AD 用户与权限规划

> 状态：**当前模型**。本文以当前项目实现为准；示例域 `nas.example.com` 需替换为部署实际域名。

## 1. 文档目的

本文用于规划 ANAS 中 Samba Active Directory（AD）的用户、管理员、服务账号、用户组和权限分配。目标是让身份、应用访问和文件权限分别管理，减少共享管理员账号和权限叠加带来的风险。

本文以当前项目实现为准。示例域使用 `nas.example.com`；实际部署时应替换为 `global.domain` 中配置的域名。

## 2. 规划原则

1. 每个自然人使用独立账号，不共用账号，便于审计和离职回收。
2. 日常账号与管理账号分离。管理员平时用普通账号，只有管理操作才使用专用管理账号。
3. 权限只通过组授予，不直接逐个配置用户 ACL。
4. 角色、应用登录和资源访问使用不同组，避免一个组同时表达多种含义。
5. 服务账号禁止交互登录，只授予应用完成任务所需的最小权限。
6. `Domain Admins`、`Administrators`、`FS Admins` 等高权限组成员必须少量、明确并定期复核。
7. 用户停用优先于立即删除；完成审计、数据交接和保留期后再删除。

<span id="anas-directory-layout"></span>

## 3. 当前目录结构

Samba DC 初始化后，目录结构如下：

```text
DC=nas,DC=example,DC=com
├── CN=Users                         # AD 内置用户和内置组所在容器
│   └── CN=Administrator             # 内置域管理员
├── CN=Computers                     # 默认入域计算机容器
├── OU=People                        # 普通自然人账号；当前自定义 admin 也在此处
├── OU=Admins                        # 预留的特权账号 OU，当前脚本不自动放入账号
├── OU=Service Accounts              # 非交互服务账号
│   ├── CN=svc_ldap
│   └── CN=svc_password
├── OU=Servers                       # 服务器对象规划位置
├── OU=Graveyard                     # 停用或待删除对象的隔离位置
└── OU=Groups
    ├── OU=Role                      # 业务或管理角色
    │   ├── CN=Admins
    │   └── CN=Unix Admins
    ├── OU=Access                    # 文件等资源的访问权限
    │   ├── CN=FS Admins
    │   └── CN=FS Share RW
    ├── OU=Apps                      # 应用登录范围
    │   ├── CN=APP_all
    │   └── CN=APP_<应用名>
    └── OU=Cap                       # 提案，尚未实现：应用内能力授权（§5.4.1）
        └── CN=CAP_<module-id>_<capability>
```

`OU=Groups`、`Role`、`Access` 等结构由 `samba_dc.create_structure` 控制；应用组由 `samba_dc.app_filter` 和实际启用的 LDAP 应用共同决定。两项当前默认均为 `true`。

## 4. 账号分类与当前状态

| 类别 | 当前账号/命名 | 位置 | 当前权限或用途 | 规划要求 |
| --- | --- | --- | --- | --- |
| 内置应急管理员 | `Administrator` | `CN=Users` | AD 内置最高权限账号，用于建域和应急恢复 | 不用于日常操作；凭据单独保管 |
| 自动创建的管理员 | 默认 `admin`，可配置 | `OU=People` | 属于 `Domain Admins`、`Administrators`、`Group Policy Creator Owners`、`Admins`、`FS Admins`；不加入 `APP_all` 或具体 `APP_*` | 作为部署/管理账号，不作为日常个人账号；后续宜拆分职责 |
| 普通用户 | 建议 `姓名拼音` 或员工编号 | `OU=People` | 默认属于 `Domain Users`；按需加入角色、应用和资源组 | 每人一个账号，不加入内置高权限组 |
| LDAP 查询服务账号 | `svc_ldap` | `OU=Service Accounts` | 供应用只读查询目录；不属于管理组；密码不过期 | 禁止交互使用，随机长密码，定期轮换 |
| 密码修改服务账号 | `svc_password` | `OU=Service Accounts` | 仅继承 `OU=People` 的“重置密码”权限；明确禁止重置 `admin`；不能创建或删除用户 | 仅给确需修改 AD 密码的应用使用 |
| 计算机账号 | `<主机名>$` | `CN=Computers` 或规划 OU | 域成员身份 | 使用统一主机命名并记录负责人 |

说明：当前 `admin` 虽位于 `OU=People`，但其权限由组成员关系决定，仍然是高权限域管理员。`OU=Admins` 当前只是预留结构，不能据 OU 位置判断账号是否有管理员权限。

## 5. 用户组规划

<span id="ad-built-in-groups"></span>

### 5.1 AD 内置高权限组

| 组 | 权限 | 建议 |
| --- | --- | --- |
| `Domain Admins` | 域级管理权限 | 仅专用域管理员账号；普通用户禁止加入 |
| `Administrators` | 域控制器和目录管理权限 | 通过专用管理组间接授权；严格限制成员 |
| `Group Policy Creator Owners` | 创建和管理组策略 | 仅确需维护 GPO 的管理员 |
| `Enterprise Admins` | 林级管理权限 | 当前自动从 `admin` 移除；日常保持空闲 |
| `Schema Admins` | 修改 AD 架构 | 当前自动从 `admin` 移除；仅在架构变更窗口临时授予 |

<span id="anas-role-groups"></span>

### 5.2 Role：职责角色组

| 组 | 当前含义 | 是否等同域管理员 |
| --- | --- | --- |
| `Admins` | 全局应用管理员角色：允许进入所有应用并映射为最高应用级角色 | 否 |
| `Unix Admins` | 域基础设施管理员；嵌套进内置 `Administrators`，并获得 `SeDiskOperatorPrivilege` | 是，高权限 |

建议后续为实际组织增加 `ROLE_<职责代码>`，例如 `ROLE_FINANCE`、`ROLE_HR`、`ROLE_IT_SUPPORT`。角色组描述“用户是谁/承担什么职责”，不直接代替应用或文件资源权限组。

<span id="anas-access-groups"></span>

### 5.3 Access：资源权限组

| 组 | 权限 | 风险级别 |
| --- | --- | --- |
| `FS Admins` | 在 Samba 文件服务器上具有等同 root 的文件操作能力 | 极高 |
| `FS Share RW` | 对公共 `Share` 目录具有读写权限 | 中 |
| `Domain Users` | 默认模式下可读取公共 `Share`；若 `SHARE_ACCESS_MODE=all_rw` 则可读写 | 普通 |

文件共享默认采用 `all_read_group_write`：所有已认证域用户可读，只有 `FS Share RW` 和 `FS Admins` 可写。建议保持该模式，不要把 `Domain Admins` 或日常账号直接加入 `FS Admins`。

<span id="anas-app-groups"></span>

### 5.4 Apps：应用登录组

| 组 | 用途 |
| --- | --- |
| `APP_all` | 允许访问所有启用组过滤的应用 |
| `APP_nextcloud` | 允许登录 Nextcloud |
| `APP_meshcentral` | 允许登录 MeshCentral |
| `APP_<应用名>` | 允许登录对应 LDAP 应用；实际组随启用模块生成 |

初始化会把 `APP_all` 嵌套到各 `APP_<应用名>`，但自动创建的 `admin` 只加入 `Admins`，
不加入 `APP_all` 或具体 `APP_*`。IAM 客户端的允许集合统一为
`APP_<应用名>,APP_all,Admins`（三者为 OR），因此 `Admins` 已足以让该账号进入全部应用；
保持组职责互斥能让审计清楚，`APP_all` 只表达“非管理员的全应用访问”。

在 Nextcloud 中，Samba `Admins` 被配置为 LDAP administrative group，所有成员动态获得
Nextcloud `admin` 权限，移组后也随 LDAP 组映射撤销。初始化脚本不再把引导账号单独写入
本地 `admin` 组，避免形成无法随目录收敛的第二份权限事实。

同一规则适用于其他 Module：`Admins` 必须显式映射为应用的最高应用级角色；`APP_all`
和 `APP_*` 仅授予登录权。这里的“应用级”不包含 Samba `Domain Admins`、宿主机 root、
数据库超级用户或 `FS Admins`。LAM 是典型边界：`Admins` 获得完整 LAM 入口，但目录写入
仍服从该用户的 AD ACL。**逐 Module 的当前实现状态**（哪些 Module 已接 OIDC、哪些还在用自身认证、
各自的组映射与密码写回口径）见[Module IAM / OIDC 支持清单](/reference/module-iam-support)——那张表
随实现更新，是这件事的可查询事实来源。新 Provider 的准入条件另有规范来源：[新 IAM Provider 准入与
实施要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/iam-provider.md)
（仓库 `dev-docs/`，不在本站发布）。

嵌套组是目录授权契约的一部分。`用户 -> ROLE_X -> APP_nextcloud` 必须与用户直接加入
`APP_nextcloud` 等价；实现必须递归解析成员关系、处理循环且不能仅查看用户对象上的直接
`memberOf`。当前 Authentik Source 与 Nextcloud/MeshCentral 的 LDAP 用户过滤器均
使用 AD matching-rule-in-chain（OID `1.2.840.113556.1.4.1941`），LLNG 开启
`ldapGroupRecursive`。这保证 IAM 放行后，应用侧目录开户也不会再次按直接成员拒绝。
E2E 会创建临时 `ROLE_*` 嵌套组，同时验证两种 IAM 的结果。

#### 5.4.1 Caps：应用能力组（提案）

> 状态：**提案，当前未实现**。`OU=Cap` 与 `CAP_*` 组尚未由任何代码创建；本节记录待决策的通用规则，
> 首个消费者是 [AI Agent 编排设计](ai-agent-orchestration-design.md)。

`APP_<module-id>` 回答“能不能进这个应用”。有些 Module 还需要回答“进去之后能用哪个功能”——例如
谁可以让 AI Agent 执行代码、谁可以附着运行中的终端。这类授权：

- 不是 `ROLE_*`：那是管理员按组织结构维护的职责组，由人创建、随组织调整；
- 不是 `FS_*`：它不描述文件或存储资源；
- 也不宜塞进 `APP_*`：那会让同一前缀既表示登录范围又表示应用内权限，违反“一个组只表达一种含义”。

因此提案新增一类由 **ANAS 按启用的 Module 生成**的能力组：

| 项 | 约定 |
| --- | --- |
| 命名 | `CAP_<module-id>_<capability>`，`<module-id>` 与 Module ID 一致（小写），`<capability>` 用小写下划线代码 |
| 位置 | `OU=Cap,OU=Groups` |
| 创建主体 | ANAS，随 Module 启停增删；管理员不手工创建 |
| 语义 | 应用内的功能授权；不隐含登录权，成员仍需通过 `APP_<module-id>`、`APP_all` 或 `Admins` 进入应用 |
| 与 `Admins` 的关系 | 由各 Module 决定是否让 `Admins` 默认获得某项能力；能力组用于把该能力开放给**非管理员** |
| 消费方式 | 经 IAM 的 group claim 投影到应用侧（例如 Forgejo 的 `--group-team-map`），应用或其编排器读取投影结果 |

独立 OU 的理由是边界清楚：`OU=Cap` 里的对象全部由 ANAS 生成、可整体审计与备份筛选、可在 Module
停用时安全回收，不与管理员手建的组混在一起。

示例：`CAP_ai_agent_execute`（可批准 AI Agent 执行）、`CAP_ai_agent_terminal`（非管理员可附着 Agent
终端）、`CAP_nextcloud_admin`（应用内管理员，若该 Module 选择用能力组表达）。

采纳前的待决项：`samba_dc` 是否新增 `create_structure` 分支创建 `OU=Cap`；能力组是否也需要类似
`APP_all` 的聚合组；Module 停用时是保留还是删除其能力组。

<span id="anas-group-naming"></span>

### 5.5 ANAS 组命名规范

先判断组表达的是“人员身份/职责”“资源权限”还是“应用登录范围”，再选择前缀和 OU。一个组只表达一种含义；不要让同一个组同时承担部门成员、应用登录和文件 ACL 三种职责。

| 类别 | 创建主体 | 命名格式 | 创建位置 | 推荐作用域/类型 | 示例 |
| --- | --- | --- | --- | --- | --- |
| 公司部门 | 管理员创建 | `ROLE_DEPT_<公司代码>_<部门代码>` | `OU=Role,OU=Groups` | `Global` / `Security` | `ROLE_DEPT_ANAS_IT`、`ROLE_DEPT_ANAS_FINANCE` |
| 一般职责 | 管理员创建 | `ROLE_<职责代码>` | `OU=Role,OU=Groups` | `Global` / `Security` | `ROLE_HR_REVIEWER`、`ROLE_IT_SUPPORT` |
| 项目或临时团队 | 管理员创建 | `ROLE_PROJECT_<项目代码>` | `OU=Role,OU=Groups` | `Global` / `Security` | `ROLE_PROJECT_ATLAS` |
| 新增文件或资源权限 | 管理员创建 | `FS_<资源代码>_<RO\|RW\|ADMIN>` | `OU=Access,OU=Groups` | 单域资源通常用 `Domain` / `Security` | `FS_FINANCE_RW`、`FS_ARCHIVE_RO` |
| 单应用登录 | ANAS 按启用的 LDAP Module 生成 | `APP_<module-id>` | `OU=Apps,OU=Groups` | 由 ANAS 管理 | `APP_nextcloud`、`APP_meshcentral` |
| 全应用登录 | ANAS 固定创建 | `APP_all` | `OU=Apps,OU=Groups` | 由 ANAS 管理 | `APP_all` |
| 应用内能力（**提案**，见 §5.4.1） | ANAS 按启用的 Module 生成 | `CAP_<module-id>_<capability>` | `OU=Cap,OU=Groups` | 由 ANAS 管理 | `CAP_ai_agent_execute` |
| ANAS 固定管理/资源组 | ANAS 固定创建 | 保留产品定义的准确名称 | `OU=Role` 或 `OU=Access` | 由 ANAS 管理 | `Admins`、`Unix Admins`、`FS Admins`、`FS Share RW` |
| AD 内置组 | AD 建域时创建 | 保留 AD 的准确名称 | AD 内置容器 | 由 AD 管理 | `Domain Admins`、`Administrators`、`Domain Users` |

命名细则：

- 管理员新建的组名使用稳定的 ASCII 代码；代码由大写 `A-Z`、数字 `0-9` 和下划线组成，不使用空格、中文全称或会随组织调整频繁变化的显示名称。
- `<公司代码>`、`<部门代码>`、`<职责代码>`、`<项目代码>` 和 `<资源代码>` 必须来自组织维护的代码表；重命名显示名称时尽量保持代码和组 `sAMAccountName` 不变。
- `RO` 是 read-only，`RW` 是 read/write，`ADMIN` 是 resource administrator。`ADMIN` 只能用于边界清楚的资源管理组，不等同 `Domain Admins`。
- `APP_<module-id>` 的 `<module-id>` 与 ANAS Module ID 保持一致，通常为小写。不要手工创建一个看似存在但对应 Module 未启用的 `APP_*` 组。
- `CAP_<module-id>_<capability>`（提案）同样由 ANAS 生成，`<capability>` 使用小写下划线代码；它只表达应用内功能授权，不表达登录权，也不要手工创建。
- `Admins` 与 AD 内置 `Administrators` 不是同一个组；`FS Admins`、`FS Share RW` 是现有产品契约。不要为了统一外观擅自改成下划线名称。
- 每个自建组必须填写 `description`，至少记录中文名称、用途、负责人或审批方；临时组还要记录到期或复核条件。

<span id="anas-department-groups"></span>

#### 5.5.1 公司部门组

部门组表示“哪些自然人属于该部门”，不直接表示某个应用或文件资源权限。部门组使用 `ROLE_DEPT_<公司代码>_<部门代码>`，例如 `ROLE_DEPT_ANAS_IT`，并创建在 `OU=Role,OU=Groups` 中。即使当前只有一家公司，也保留公司代码，避免以后增加法人或组织时出现两套命名格式。

```bash
samba-tool group add 'ROLE_DEPT_ANAS_IT' \
  --groupou='OU=Role,OU=Groups' \
  --group-scope=Global \
  --group-type=Security \
  --description='ANAS 公司信息技术部；负责人：IT Manager；成员来源：HR'
```

用户对象的 `department` 和 `company` 是资料属性，不会自动维护部门组成员关系。创建或转岗用户时，仍需显式运行 `group addmembers` / `group removemembers`，或者由经过审计的 HR 同步流程完成。

授权时优先嵌套而不是给每位部门成员重复授权：

```text
用户 -> ROLE_DEPT_ANAS_IT -> APP_nextcloud
用户 -> ROLE_DEPT_ANAS_FINANCE -> FS_FINANCE_RW
```

部门组不得直接嵌套进 `Domain Admins`、`Administrators`、`Unix Admins` 或 `FS Admins`。需要管理权限的人员应使用独立管理账号，并按职责单独审批。

## 6. 建议的目标账号模型

每名承担管理职责的人员至少使用两个账号：

| 账号类型 | 命名示例 | 组成员关系 | 用途 |
| --- | --- | --- | --- |
| 日常账号 | `zhangsan` | `Domain Users`、所需 `APP_*`、`FS Share RW`、业务角色组 | 登录工作站、应用和文件共享 |
| 域管理账号 | `adm_zhangsan` | 仅按职责加入 `Domain Admins` 或 `Unix Admins` | AD、域控、组策略等管理操作 |
| 文件管理账号 | `fsadm_zhangsan`，可选 | `FS Admins` | 文件权限修复、数据恢复；不用于日常访问 |

小型家庭环境可保留一个专用 `admin` 管理账号，但仍应另外建立个人日常账号。组织环境不应多人共用 `admin`；应逐步建立实名 `adm_*` 账号，验证可用后将共享 `admin` 降为受控应急账号。

建议组授权链为：

```text
自然人账号
├── ROLE_<职责>       表示组织职责
├── APP_<应用名>      控制能否登录应用
└── FS Share RW       控制共享目录写权限

专用管理账号
├── Domain Admins 或 Unix Admins
└── FS Admins         仅确有文件服务器管理职责时加入
```

## 7. 命名与属性规范

<span id="anas-account-naming"></span>

### 7.1 用户名

- 普通用户：`firstname.lastname`、姓名拼音或不可变员工编号，域内唯一。
- 管理账号：`adm_<普通用户名>`。
- 文件管理账号：`fsadm_<普通用户名>`。
- 服务账号：`svc_<系统或用途>`。
- 组：统一遵循[§5.5 ANAS 组命名规范](#anas-group-naming)，此处不再重复列举。
- 计算机：建议 `PC-<部门>-<编号>`、`SRV-<用途>-<编号>`。

账号重名时使用员工编号或明确的数字后缀，不建议使用职位作为个人账号名。职位会变化，账号主体不应随职位改变。

### 7.2 必填属性

普通用户至少维护：

- `sAMAccountName`：兼容登录名，必须唯一；
- `userPrincipalName`：必须为 `<sAMAccountName>@<AD DNS realm>`；
- `displayName`：人员显示名称；
- `mail`：用户真实、唯一且可投递的主邮箱；没有邮箱时保持为空；
- `description`：部门、用途或工单号，禁止记录密码；
- 负责人和到期日：外包、访客、临时账号必须填写。

`sAMAccountName`、`userPrincipalName` 和 `mail` 都可能被当前应用作为登录属性，因此三者必须保持唯一，避免账号误匹配。

### 7.3 UPN 格式

普通用户、管理账号和人工使用的服务账号必须显式设置 UPN，规范形式为：

```text
<sAMAccountName>@<AD DNS realm>
```

其中本地部分必须与同一对象的 `sAMAccountName` 完全对应；后缀必须是该 AD 林已接受的
UPN suffix，默认使用当前 AD DNS realm 的小写形式。例如 AD realm 为
`EXAMPLE.COM` 时，`sAMAccountName=alice` 对应
`userPrincipalName=alice@example.com`。

以下值不符合规范：

- `alice`：缺少 `@` 和 UPN suffix；
- `alice@other.example`：若 `other.example` 不是目录接受的 UPN suffix；
- `another-name@example.com`：本地部分与 `sAMAccountName=alice` 不一致。

UPN 不是邮箱字段。即使组织当前让 UPN 与 `mail` 取相同字符串，也必须分别维护
`userPrincipalName` 和 `mail`；邮件域变更不应自动改变 UPN。UPN 也不是应用的永久身份
主键。ANAS 应用使用永久身份锚点保持对象身份，OIDC `preferred_username` 使用
`sAMAccountName`，邮件 claim 使用 `mail`。

`mail` 不得由 UPN 或基础域名自动拼出。未安装邮件 Module、尚未创建邮箱，或地址不能
实际收信时，`mail` 保持为空。邮件 Module 成功创建用户邮箱或受监控别名后，才把实际
地址写入目录；改址、迁移和停用邮箱时也必须按邮件生命周期同步更新该属性。应用与 IAM
不得为了满足必填字段而伪造 `<sAMAccountName>@<域名>`。确实要求 email claim 的应用，
应在准入或同步阶段明确拒绝缺少 `mail` 的账号并给出可操作错误，而不是发布虚假地址。

内置系统对象或由 AD/Samba 自身管理的特殊主体不适用上述人工账号命名规则。批量审计和
修复应限定在 ANAS 管理的业务 OU，不能据此重写 `krbtgt`、计算机、信任或系统账号。

## 8. 权限分配矩阵

| 人员类型 | Domain Admins | Admins | Unix Admins | FS Admins | FS Share RW | APP_all | 指定 APP_* |
| --- | ---: | ---: | ---: | ---: | ---: | ---: | ---: |
| 普通用户 | 否 | 否 | 否 | 否 | 按需 | 否 | 按需 |
| 部门负责人 | 否 | 按应用管理职责 | 否 | 否 | 按需 | 通常否 | 按需 |
| 应用管理员 | 否 | 是或使用更细的应用角色 | 否 | 否 | 按需 | 通常否 | 是 |
| 文件服务器管理员 | 否 | 否 | 否 | 是 | 自动具备 | 否 | 按需 |
| 域管理员 | 是 | 按需 | 可二选一 | 默认否 | 按需 | 默认否 | 按需 |
| LDAP 服务账号 | 否 | 否 | 否 | 否 | 否 | 否 | 否 |

当前自动创建的 `admin` 权限高于此目标矩阵：它同时拥有域管理、应用管理、文件管理和全部应用访问权限。新部署完成后应按实际运维模式评估并拆分该账号职责。

## 9. 密码与账号安全基线

当前默认域密码策略为：

| 项目 | 当前值 |
| --- | --- |
| 复杂度 | 关闭 |
| 最小长度 | 8 位 |
| 密码历史 | 2 次 |
| 最大有效期 | 90 天 |
| 最短修改间隔 | 1 天 |
| 锁定阈值 | 10 次失败 |
| 锁定时间 | 30 分钟 |
| 失败计数重置 | 30 分钟 |

特权策略 `pso_privileged` 当前设置最小 8 位、历史 2 次、复杂度启用且密码永不过期，并应用于内置 `Administrator` 和 `Admins` 组。

规划建议：

1. 人工账号使用至少 12 位口令；管理账号和应急账号建议至少 16 位随机口令。
2. 服务账号使用随机生成的长密码，通过 ANAS secrets 管理，不复制到文档、工单或聊天记录。
3. 轮换 `svc_ldap` 或 `svc_password` 时，必须同步更新全部使用方并验证 LDAP 登录/改密流程。
4. 内置 `Administrator` 与自定义管理账号不得使用相同密码。
5. 对支持 MFA 的上层 SSO 和应用启用 MFA；Samba AD 本身的口令仍须独立保护。
6. `secrets.yml`、模块 `.env`、备份和 Docker 管理权限均按秘密数据保护。

## 10. 用户生命周期流程

### 10.1 入职或新增成员

1. 根据实名和命名规则创建普通账号到 `OU=People`。
2. 按 `<sAMAccountName>@<AD DNS realm>` 填写 UPN，并填写显示名、邮箱、部门/用途和负责人。
3. 设置临时初始密码，并要求首次登录后修改。
4. 仅加入必要的 `ROLE_*`、`APP_*` 和 `FS Share RW`。
5. 如需管理权限，另建 `adm_*` 或 `fsadm_*` 专用账号。
6. 由申请人和资源负责人验证应用登录与文件权限。

### 10.2 转岗

1. 先添加新岗位所需权限并验证。
2. 在约定的切换时间移除旧角色、旧应用和旧资源组。
3. 复核是否仍持有 `Admins`、`Unix Admins`、`FS Admins` 或内置高权限组。
4. 更新账号描述、部门和负责人信息，保留审批记录。

### 10.3 离职或成员退出

1. 立即禁用普通账号及其所有管理账号。
2. 终止活动会话；必要时重置口令并撤销上层 SSO 会话。
3. 从所有应用、资源和管理员组中移除。
4. 将对象移入 `OU=Graveyard`，记录停用日期、工单和数据接收人。
5. 按保留策略完成 Home 目录和业务数据交接。
6. 保留期结束、审计确认后再删除账号；不要立即删除 SID 仍被文件 ACL 使用的账号。

### 10.4 定期复核

- 每月：复核 `Domain Admins`、`Administrators`、`Unix Admins`、`FS Admins`。
- 每季度：复核 `Admins`、`APP_all`、全部 `APP_*` 和 `FS Share RW`。
- 每半年：复核服务账号使用方、密码轮换记录、长期未登录账号和 `OU=Graveyard`。
- 每年：恢复演练内置 `Administrator` 凭据和 AD/文件 ACL 备份。

## 11. 常用管理命令

以下命令在 Samba DC 容器内执行。密码不应直接写进 shell 历史；创建用户时可省略密码参数并按提示输入。

```bash
# 列出用户、组和指定组成员
samba-tool user list
samba-tool group list
samba-tool group listmembers "Domain Admins"
samba-tool group listmembers "FS Admins"

# 创建普通用户并移动到 OU=People
samba-tool user create zhangsan
samba-tool user move zhangsan "OU=People,DC=nas,DC=example,DC=com"

# 授予应用和文件写权限
samba-tool group addmembers "APP_nextcloud" zhangsan
samba-tool group addmembers "FS Share RW" zhangsan

# 撤销权限
samba-tool group removemembers "APP_nextcloud" zhangsan
samba-tool group removemembers "FS Share RW" zhangsan

# 禁用、启用和查看账号
samba-tool user disable zhangsan
samba-tool user enable zhangsan
samba-tool user show zhangsan

# 查看域密码策略和特权 PSO
samba-tool domain passwordsettings show
samba-tool domain passwordsettings pso show pso_privileged
```

移动账号前应使用实际域 DN；不要直接照抄示例。批量变更前先导出用户、组成员关系和文件 ACL，并在测试账号上验证。

## 12. 落地清单

- [ ] 确认正式 AD 域名、NetBIOS 名称和主机命名规范。
- [ ] 确认普通用户、域管理员、文件管理员的账号命名规范。
- [ ] 建立人员—普通账号—管理账号对应表。
- [ ] 确定各应用的 `APP_*` 授权负责人。
- [ ] 确定 `FS Share RW` 和 `FS Admins` 的审批负责人。
- [ ] 导出并复核全部高权限组成员。
- [ ] 为共用 `admin` 制定实名管理账号替换计划。
- [ ] 将人工账号最小密码长度提升到组织要求，并验证旧客户端兼容性。
- [ ] 建立入职、转岗、离职和临时账号到期流程。
- [ ] 建立季度权限复核和年度恢复演练记录。

## 13. Authentik 与 Samba AD

### 13.1 身份边界

Samba AD 是业务用户、组、密码和启用状态的唯一权威来源。Authentik 只同步目录镜像、验证 AD 密码、管理 MFA，并向应用提供 SAML/OIDC。禁止在 Authentik 创建业务本地用户。

唯一例外是 Authentik 首次启动创建的本地超级管理员 `akadmin`。它是目录故障时使用的 break-glass（应急恢复）账号，不是 Samba AD 用户，不得用于 Nextcloud、NetBird 等业务登录。

`akadmin` 是 Authentik Manifest 中固定用户名的 `break_glass` 托管账号。密码由 ANAS
按账号生成并保存在统一 Secret Store。显式查看命令为：

```bash
anas admin local credential authentik break_glass -w <workspace>
```

正式环境示例：

```bash
anas admin local credential authentik break_glass -w /srv/anas
```

该命令会向终端输出明文，只能在受控终端执行，不得进入 shell trace、工单或聊天记录。日常不查看、不使用。当前 Secret 是首次引导凭据；首次接入已有 Authentik 时，Bootstrap 环境变量不会修改数据库中已经存在的 `akadmin` 密码，必须在迁移窗口单独完成一次校准。项目尚未提供原子化的 `akadmin` 密码轮换命令，因此不要只在 Authentik UI 中修改后仍把上述命令当作当前密码；正式发布前应补充“同时更新 Authentik 与 ANAS Secret 状态”的轮换事务。

### 13.2 密码修改

Authentik 允许用户修改密码，但新密码必须通过 LDAPS 写回 Samba AD，不在 Authentik 保留可独立认证的密码。LDAP Source 使用 `svc_password`，设置 `sync_users_password=true` 和 `password_login_update_internal_password=false`。

Authentik worker 在处理目录 Blueprint 前，从 Lego 的共享证书目录导入 ANAS 内部 CA；LDAP Source 显式设置 `peer_certificate`，但不启用 Authentik 的显式 SNI 开关，因为 Authentik 2026.5 会错误地把完整 `ldaps://host:port` URI 作为 SNI 名称。TLS 库仍会从 URI 主机名完成正常证书校验。不得把证书字段留空，否则 Authentik 会跳过 LDAPS 服务端证书校验。

`svc_password` 只有 `OU=People` 的 Reset Password 权限，不能创建、删除用户或修改服务账号；对高权限 `admin` 的显式拒绝 ACE 保持有效。密码找回流程必须结合可信邮箱、MFA 和事件审计。

### 13.3 唯一身份标识

登录名和永久身份键必须分开：

- `sAMAccountName`、UPN、mail 用于登录和查找，可以展示给用户；
- `objectSid` 适合当前 AD 对象和 Windows ACL，但删除重建用户、重建域或跨域迁移会变化；
- `mS-DS-ConsistencyGuid` 是本部署的二进制永久身份锚点，由 Anchor Worker 在对象首次进入业务 OU 时以 `objectGUID` 原始 16 字节初始化；之后禁止自动覆盖；`anasIdentityAnchor` 是从该二进制值按 AD GUID 字节序生成的标准 UUID 文本投影。

Nextcloud 的用户和组 LDAP UUID、SAML UID、Authentik LDAP Source 的对象唯一字段以及 MeshCentral 的文本 LDAP 用户键都使用 `anasIdentityAnchor`；Entra 等理解 AD 二进制 GUID 的系统继续使用 `mS-DS-ConsistencyGuid`。`sAMAccountName`、UPN 和 mail 只用于登录、搜索和显示。跨域重建必须先恢复旧二进制锚点，再把对象移入 Worker 与下游可见的业务 OU；仅创建同名账号不能恢复原身份。

### 13.4 小型 NAS 管理组

小型 NAS 中可以只保留一个 `Admins` 管理角色，不额外建立 `ROLE_IAM_ADMINS`，前提是成员很少且所有成员都被授权管理整个身份入口。加入 `Admins` 等价于获得 Authentik、应用和部分基础设施的高权限，必须使用专用管理账号、启用 MFA 并定期复核。

Authentik 的 Samba AD Source 通过组属性映射，仅将名称精确等于 `Admins` 的同步组设置为 superuser；其他同步组不会因此获得 Authentik 管理权限。不要建立名为 `Admins` 的普通业务组，也不要把普通用户或 `APP_*` 组嵌套到它下面。

`Admins` 不能替代 `APP_<应用>`：普通用户仍通过 `APP_nextcloud`、`APP_netbird` 等组获得最小应用访问权限；只有管理员才加入 `Admins`。

## 14. 身份协议环境契约

Runner 统一发布 `ANAS_IDENTITY_CLIENTS`、`ANAS_IDENTITY_APP_CLIENTS`、`ANAS_IDENTITY_LDAPS_CLIENTS`、`ANAS_IDENTITY_OIDC_CLIENTS`、`ANAS_IDENTITY_SAML_CLIENTS` 以及 `ANAS_IDENTITY_CLIENT__<MODULE>__INTERFACES`。Samba DC 根据 `ANAS_IDENTITY_APP_CLIENTS` 创建 `APP_<应用>`，不再依赖 LDAP 专用名单。

完整变量清单见 [Module 环境变量契约](../reference/module-environment-variables.md)。

## 15. 实现依据

本规划依据当前仓库中的 Samba DC 初始化和配置实现：

- `modules/samba_dc/hook/main.go`：OU、DN、账号和组名称的计算；
- `modules/samba_dc/samba_dc/root/usr/local/bin/structure.sh`：用户、服务账号、组成员关系、委派权限及密码策略；
- `modules/samba_dc/module.yml`：默认配置和密码策略；
- `docs/samba_dc.md`：文件服务权限和 AD 使用说明。

若上述实现发生变化，应同步更新本文的“当前状态”，再评估目标规划是否需要调整。
