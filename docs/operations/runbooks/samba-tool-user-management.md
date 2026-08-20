---
doc_type: runbook
status: current
created: 2026-08-17
updated: 2026-08-20
---

# 使用 `samba-tool` 管理用户、组和管理员

适用范围：ANAS `samba_dc`（Samba 4.23.6）以及其他以 Samba 作为 Active Directory Domain Controller（AD DC）的环境。本文不适用于仅作为工作组文件服务器的 Samba。

English version: [Manage users, groups, and administrators with `samba-tool`](/en/operations/runbooks/samba-tool-user-management)

快速导航：[最短操作路径](#_1-结论与最短操作路径) · [Docker 与通用参数](#_2-1-命令结构和-docker-参数) · [用户管理](#_3-创建和维护用户) · [组管理](#_4-创建和管理组) · [管理员范围](#_5-设置管理员-必须先确定管理范围) · [授权模板](#_6-新用户的典型授权模板) · [命令速查](#_9-常用命令速查) · [ANAS 组命名规范](/architecture/samba-ad-user-planning#anas-group-naming)

## 1. 结论与最短操作路径

在 ANAS 中，新建人员账号通常分为三步：

1. 在 `OU=People` 中创建用户；
2. 按需加入应用、文件访问或业务角色组；
3. 只有确有管理职责时，才把专用管理账号加入对应管理员组。

以下是最常用的一组命令。第 1 节只保留操作路径；Docker、Samba 用户和组参数分别在后面的唯一章节解释，避免同一参数维护多份说明。默认 Samba DC 容器名为 `anas_samba_dc`；它受 [`global.container_prefix`](/reference/configuration#已声明的-146-个参数) 影响。自定义部署先按 [§2 操作前检查](#_2-操作前检查) 找到实际容器名。

### 1.1 创建王海龙的普通账号

```bash
# 创建普通用户；不在命令行中写密码，按交互提示输入
docker exec -it anas_samba_dc samba-tool user add wanghailong \
  --userou='OU=People' \
  --given-name='海龙' \
  --surname='王' \
  --mail-address='wanghailong@example.com' \
  --must-change-at-next-login
```

参数跳转：外层 `docker exec -it` 和续行符见 [§2.1 命令结构和 Docker 参数](#_2-1-命令结构和-docker-参数)；通用认证选项见 [§2.2 `samba-tool` 通用参数](#_2-2-samba-tool-通用参数)；`user add` 的位置参数、常用选项和 AD 属性映射见 [§3.1 创建普通用户](#_3-1-创建普通用户)。`OU=People` 是 [ANAS 默认目录结构](/architecture/samba-ad-user-planning#anas-directory-layout)的一部分。

### 1.2 设置显示名称

```bash
docker exec anas_samba_dc samba-tool user rename wanghailong \
  --display-name='王海龙'
```

`user rename` 的参数、属性影响和风险统一见 [§3.2 查看和修改属性](#_3-2-查看和修改属性)。

### 1.3 添加应用组和文件访问组

```bash
# 允许登录 Nextcloud，并允许写公共共享目录
docker exec anas_samba_dc samba-tool group addmembers 'APP_nextcloud' wanghailong
docker exec anas_samba_dc samba-tool group addmembers 'FS Share RW' wanghailong
```

`group addmembers` 的两个位置参数以及添加、移除、列表选项统一见 [§4.2 添加、移除和核验成员](#_4-2-添加、移除和核验成员)。[`APP_nextcloud`](/architecture/samba-ad-user-planning#anas-app-groups) 是 ANAS 按启用 Module 生成的应用登录组；[`FS Share RW`](/architecture/samba-ad-user-planning#anas-access-groups) 是 ANAS 固定的公共共享读写组。命令中的单引号只是 Shell 引号，不属于组名。

### 1.4 核验用户和组关系

```bash
# 核验用户和直接组成员关系
docker exec anas_samba_dc samba-tool user show wanghailong
docker exec anas_samba_dc samba-tool user getgroups wanghailong
docker exec anas_samba_dc samba-tool group listmembers 'APP_nextcloud'
```

查看用户属性见 [§3.2](#_3-2-查看和修改属性)；`user getgroups` 和 `group listmembers` 的直接成员语义及列表参数见 [§4.2](#_4-2-添加、移除和核验成员)。嵌套组不会被这两条命令完整展开，按 [§4.3](#_4-3-用嵌套组分配权限) 继续核验父子组。

普通用户创建后会自动以 `Domain Users` 为主组，不需要手动加入。

Samba 4.23 的首选命令是 `samba-tool user add`；`samba-tool user create` 仍可使用，但只是兼容别名。不同 Samba 版本的帮助文字可能相反，编写自动化前应以目标 DC 上的 `samba-tool user add --help` 为准。

## 2. 操作前检查

先确认容器、版本、域和目录结构正确：

```bash
docker ps --format '{{.Names}}' | grep samba_dc
docker exec anas_samba_dc samba-tool --version
docker exec anas_samba_dc samba-tool domain level show
docker exec anas_samba_dc samba-tool ou list --full-dn
docker exec anas_samba_dc samba-tool domain passwordsettings show
```

| 命令或参数 | 英文含义 | 检查内容 |
| --- | --- | --- |
| `docker ps` | list containers | 列出正在运行的容器 |
| <code v-pre>--format '{{.Names}}'</code> | output format | 只输出容器名称；`Names` 是 Docker 模板字段 |
| `\| grep samba_dc` | filter matching text | 只保留名称中含 `samba_dc` 的行；竖线把前一命令的输出传给 `grep` |
| `samba-tool --version` | show version | 显示容器内安装的 Samba 版本 |
| `domain level show` | show domain and forest levels | 查看域、林和最低 DC 功能级别 |
| `ou list --full-dn` | list OUs with full DNs | 列出组织单位并显示完整 DN，便于确认 `OU=People` 等路径 |
| `domain passwordsettings show` | show password settings | 查看域密码复杂度、长度、历史、有效期和锁定策略 |

在 ANAS 默认部署中，从宿主机通过 `docker exec` 进入 DC 容器后，`samba-tool` 直接操作本机 AD 数据库，不需要在命令中附带管理员密码。远程管理时应使用专用管理账号、Kerberos 或安全保存的凭据，不要写成 `-U '用户%明文密码'`。

建议在每次高权限变更前记录当前成员：

```bash
docker exec anas_samba_dc samba-tool group listmembers 'Domain Admins'
docker exec anas_samba_dc samba-tool group listmembers 'Administrators'
docker exec anas_samba_dc samba-tool group listmembers 'Admins'
docker exec anas_samba_dc samba-tool group listmembers 'FS Admins'
```

这里复用 [§4.2 的 `group listmembers` 参数说明](#_4-2-添加、移除和核验成员)。做高权限基线审计时不要使用 `--hide-expired` 或 `--hide-disabled`，否则记录不完整。组名中的空格需要用引号保护为一个 Shell 参数。

### 2.1 命令结构和 Docker 参数

本文命令由外层 Docker 命令和内层 Samba 命令组成：

```text
docker exec [Docker 选项] <容器名> samba-tool <对象> <动作> [位置参数] [选项]
```

例如：

```bash
docker exec -it anas_samba_dc samba-tool user add wanghailong --userou='OU=People'
```

| 片段 | 英文含义 | 作用 |
| --- | --- | --- |
| `docker exec` | execute a command in a running container | 在已经运行的容器中执行一条命令，不会新建容器 |
| `-i` | interactive / keep STDIN open | 保持标准输入打开，让密码提示可以读取键盘输入 |
| `-t` | allocate a pseudo-TTY | 分配伪终端，使交互提示和输入正常显示；通常与 `-i` 合写为 `-it` |
| `anas_samba_dc` | container name | 目标 Samba DC 容器名；自定义前缀部署应替换它 |
| `samba-tool` | Samba administration tool | Samba 主管理工具 |
| `user` / `group` / `ou` | object category | 要管理的对象类别：用户、组或组织单位 |
| `add` / `show` / `move` | subcommand / action | 对对象执行的动作：新增、查看、移动等 |
| `wanghailong` | positional argument | 位置参数；这里是用户的登录名，位置不能任意改变 |
| `--userou=...` | named option | 带名称的可选参数；`=` 两边的内容分别是参数名和值 |
| `\` | shell line continuation | Shell 续行符，只为排版方便；反斜杠后不能再有空格 |

本文不在 `docker exec` 后指定 `--user`，因此使用容器默认用户 root。root 在本机 DC 容器中可以通过本地数据库完成受信任管理操作；这不代表远程普通用户也自动具有 AD 权限。

### 2.2 `samba-tool` 通用参数

以下参数可用于多个子命令。参数是否能放在子命令前后可能随 Samba 版本和命令解析器变化，自动化中应遵循目标版本 `--help` 给出的顺序。

| 参数 | 英文全称或含义 | 详细说明 |
| --- | --- | --- |
| `-h`, `--help` | help | 显示当前层级的帮助。`samba-tool --help`、`samba-tool user --help` 和 `samba-tool user add --help` 的详细程度不同 |
| `-V`, `--version` | version | 输出 Samba 版本后退出 |
| `-H`, `--URL` | LDB URL / target URL | 指定本地数据库或远程目录目标。常见值为 `ldap://dc.example.com`、`ldaps://dc.example.com` 或本地 LDB 路径；`ldaps://` 表示 LDAP over TLS |
| `-U`, `--user` | authentication user | 指定执行操作的身份，格式通常为 `DOMAIN\username`。不要附加 `%password`，否则明文可能进入 shell 历史和进程参数 |
| `-W`, `--workgroup` | workgroup / NetBIOS domain | 覆盖 `smb.conf` 中的短域名，例如 `NAS`；它不是 DNS realm |
| `-r`, `--realm` | Kerberos realm | 覆盖域的 Kerberos realm，通常是大写 DNS 域名，例如 `NAS.EXAMPLE.COM` |
| `--use-kerberos=desired|required|off` | Kerberos authentication mode | `desired` 尝试使用 Kerberos，`required` 强制使用，`off` 禁用；使用 Kerberos 时应按 DNS 名连接而不是 IP |
| `--use-krb5-ccache=<路径>` | Kerberos credential cache | 指定 Kerberos 凭据缓存，同时等价于要求 Kerberos |
| `-A`, `--authentication-file` | authentication file | 从权限受控的文件读取 `username`、`password` 和 `domain`。文件包含明文秘密，必须限制权限 |
| `--password=<值>` | authentication password | 在命令行提供认证密码；存在进程列表和历史泄漏窗口，不推荐 |
| `-N`, `--no-pass` | no password prompt | 禁止密码提示。只有目标操作确实不需要密码时才使用；它不是“自动认证” |
| `-s`, `--configfile` | Samba configuration file | 指定替代的 `smb.conf` |
| `-d`, `--debuglevel=0..10` | debug level | 调试详细度。日常默认为低级别；大于 1 会显著增加日志，大于 3 主要面向开发排障 |

在 ANAS 的本机容器操作中通常不需要 `-H`、`-U`、`-W` 或 `-r`，因为 `samba-tool` 会读取容器内的 `smb.conf` 和本地 AD 数据库。

### 2.3 英文缩写和目录术语

| 缩写或术语 | 英文全称 | 中文含义与示例 |
| --- | --- | --- |
| AD | Active Directory | 活动目录；保存用户、组、计算机和策略的目录服务 |
| AD DS | Active Directory Domain Services | Active Directory 域服务 |
| DC | Domain Controller | 域控制器；ANAS 中由 `samba_dc` 提供 |
| LDAP | Lightweight Directory Access Protocol | 轻量级目录访问协议；用于查询和修改目录对象 |
| OU | Organizational Unit | 组织单位，例如 `OU=People`；用于组织对象和委派管理，不直接授予权限 |
| CN | Common Name | 通用名称，也是常见的相对 DN，例如 `CN=wanghailong`、`CN=Users` |
| DC（在 DN 中） | Domain Component | 域组成部分；在 `DC=nas,DC=example,DC=com` 中不是“域控制器”的意思 |
| RDN | Relative Distinguished Name | 相对可分辨名称；DN 最左侧的当前节点，例如 `CN=wanghailong` |
| DN | Distinguished Name | 可分辨名称；对象完整目录路径，例如 `CN=wanghailong,OU=People,DC=nas,DC=example,DC=com` |
| realm | Kerberos realm | Kerberos 域，通常对应大写 DNS 域名，例如 `NAS.EXAMPLE.COM` |
| workgroup | NetBIOS domain name | 短域名，例如 `NAS`，与完整 DNS realm 不同 |
| `sAMAccountName` | Security Account Manager account name | 兼容登录名，例如 `wanghailong`；`samba-tool` 多数用户命令中的 `username` 指它 |
| UPN | User Principal Name | 用户主体名称，通常为 `wanghailong@nas.example.com` |
| SID | Security Identifier | 安全标识符；ACL 真正引用的不可变对象标识。同名删除重建会得到新 SID |
| RID | Relative Identifier | SID 中在本域内唯一的相对编号 |
| ACL / DACL | Access Control List / Discretionary ACL | 访问控制列表；定义谁可以对目录对象或文件执行什么操作 |
| GPO | Group Policy Object | 组策略对象；向域内计算机和用户下发策略 |
| PSO | Password Settings Object | 密码设置对象；为特定用户或组应用细粒度密码策略 |
| UID / GID | User ID / Group ID | Unix 数字用户/组标识；不要与 AD 的 SID 混淆 |
| RFC2307 | Unix attributes in LDAP | LDAP 中的 Unix 账号属性规范，例如 `uidNumber`、`gidNumber` |
| Kerberos ticket | Kerberos authentication ticket | Kerberos 认证票据；组变更后旧票据可能仍携带旧权限 |

### 2.4 常用子命令的英文含义

| 子命令 | 英文原意 | 实际动作 |
| --- | --- | --- |
| `list` | list | 列出对象摘要，例如全部用户或组 |
| `add` / `create` | add / create | 新建对象；Samba 4.23 中 `create` 通常是 `add` 的兼容别名 |
| `show` | show | 显示一个对象的目录属性 |
| `edit` | edit | 在文本编辑器中编辑对象属性；自动化不宜依赖交互编辑 |
| `rename` | rename | 修改名称相关属性；可能同时改变 `sAMAccountName`、UPN、显示名或 CN，取决于参数 |
| `move` | move | 把对象移动到另一个 OU 或容器，因而改变 DN |
| `delete` | delete | 删除目录对象；SID 和 ACL 影响不可由同名重建恢复 |
| `enable` / `disable` | enable / disable | 启用或禁用账号认证，不删除对象 |
| `unlock` | unlock | 解除账号锁定 |
| `setpassword` | set password | 由管理员设置或重置指定用户密码 |
| `setexpiry` | set expiry | 设置账号对象到期时间 |
| `getgroups` | get groups | 获取用户的直接组成员关系 |
| `addmembers` | add members | 把用户、计算机或组加入目标组 |
| `removemembers` | remove members | 从目标组移除成员 |
| `listmembers` | list members | 列出目标组的直接成员 |

## 3. 创建和维护用户

### 3.1 创建普通用户

完整示例：

```bash
docker exec -it anas_samba_dc samba-tool user add wanghailong \
  --userou='OU=People' \
  --given-name='海龙' \
  --surname='王' \
  --department='IT' \
  --company='Example' \
  --mail-address='wanghailong@example.com' \
  --description='员工账号；负责人：IT' \
  --must-change-at-next-login

docker exec anas_samba_dc samba-tool user rename wanghailong \
  --display-name='王海龙' \
  --upn='wanghailong@nas.example.com'
```

> 执行前把 `example.com` 和 `nas.example.com` 替换为实际邮箱域和 AD DNS realm。UPN
> 必须显式写成 `<sAMAccountName>@<AD DNS realm>`，不能只写短用户名。

本例把“人的姓名”“登录名”和“显示名称”明确分开：

| 信息 | 示例值 | 写入位置 | 为什么这样填写 |
| --- | --- | --- | --- |
| 中文姓名 | 王海龙 | 不是单一 AD 字段 | 完整姓名由姓、名和显示名共同表达 |
| 登录名 | `wanghailong` | `sAMAccountName` | 使用稳定的 ASCII 登录名，避免不同客户端处理中文账号名不一致 |
| 姓 | 王 | `sn`，由 `--surname='王'` 设置 | AD/LDAP 将 family name 单独保存 |
| 名 | 海龙 | `givenName`，由 `--given-name='海龙'` 设置 | AD/LDAP 将 given name 单独保存 |
| 显示名 | 王海龙 | `displayName`，由 `--display-name` 设置 | 用户界面按中文姓名顺序展示，不影响登录名 |
| 邮箱 | `wanghailong@example.com` | `mail` | 真实、唯一且可投递的主邮箱；没有实际邮箱时不要设置 |
| UPN | `wanghailong@nas.example.com` | `userPrincipalName` | 现代 AD 登录格式；本地部分必须等于 `sAMAccountName`，后缀采用目录接受的 AD DNS realm |

添加过程按以下顺序执行：

1. 运行 `user add`。`-it` 让终端提示输入并确认临时密码，密码不会出现在命令历史中。
2. Samba 在 `OU=People` 创建 `sAMAccountName=wanghailong` 的启用账号，并按参数写入姓、名、部门、公司、邮箱和说明。
3. 运行 `user rename` 设置中文显示名和明确的 UPN。这里的 `rename` 是修改现有对象属性，不是删除重建。
4. 因为使用了 `--must-change-at-next-login`，王海龙第一次登录时必须更换临时密码。
5. 运行以下命令核验 DN、登录名、UPN、姓名和邮箱，再按第 6 节加入所需权限组：

```bash
docker exec anas_samba_dc samba-tool user show wanghailong \
  --attributes=sAMAccountName,userPrincipalName,givenName,sn,displayName,mail,distinguishedName
docker exec anas_samba_dc samba-tool user getgroups wanghailong
```

核验命令不再重复列参数：`user show --attributes` 见 [§3.2](#_3-2-查看和修改属性)，`user getgroups` 的直接成员语义见 [§4.2](#_4-2-添加、移除和核验成员)。

创建命令参数详解：

| 参数 | 英文含义 | 对应 AD 属性或行为 | 说明 |
| --- | --- | --- | --- |
| `wanghailong` | username / logon name | `sAMAccountName` | 必填位置参数；域内唯一的兼容登录名 |
| `[password]` | initial password | 密码凭据 | 可选的第二个位置参数。本文故意省略，让工具交互读取，避免明文泄漏 |
| `--userou='OU=People'` | user organizational unit | 对象 DN 的父路径 | 在指定 OU 中创建对象。参数值可不含域 DN；默认位置通常是 `CN=Users` |
| `--given-name='海龙'` | given name | `givenName` | 名；对中文姓名可按组织规范填写拼音或中文 |
| `--surname='王'` | surname / family name | `sn` | 姓；某些 LDAP 应用把 `sn` 视为必需属性 |
| `--initials` | initials | `initials` | 姓名首字母，中间名场景较常用 |
| `--department='IT'` | department | `department` | 部门名称；只是资料属性，不自动维护 [`ROLE_DEPT_*` 部门组](/architecture/samba-ad-user-planning#anas-department-groups)或授予权限 |
| `--company='Example'` | company | `company` | 公司或组织名称 |
| `--job-title` | job title | `title` | 职位名称；职位变化不应导致账号名变化 |
| `--description='...'` | description | `description` | 管理备注。可记录用途或负责人，禁止记录密码 |
| `--mail-address='...'` | mail address | `mail` | 真实可投递的邮箱地址；若允许邮箱登录，必须保证唯一；没有实际邮箱时省略 |
| `--telephone-number` | telephone number | `telephoneNumber` | 办公电话 |
| `--physical-delivery-office` | office location | `physicalDeliveryOfficeName` | 办公地点 |
| `--profile-path` | roaming profile path | `profilePath` | Windows 漫游配置文件路径；ANAS 默认不需要 |
| `--script-path` | logon script path | `scriptPath` | Windows 登录脚本路径，通常相对 `NETLOGON` 共享 |
| `--home-drive` | home drive letter | `homeDrive` | Windows Home 盘符，例如 `H:`；不会自行创建目录 |
| `--home-directory` | home directory UNC path | `homeDirectory` | Windows Home 的 UNC 路径；不同于 ANAS Samba FS 首次访问时创建的 Home 目录 |
| `--must-change-at-next-login` | force password change at next logon | 通常设置 `pwdLastSet=0` | 用户下次成功登录时必须修改初始密码 |
| `--random-password` | generate a random password | 随机设置密码 | 适合不由人登录的技术账号；必须另行安全交付或配合 keytab，不能假定能再次读取明文 |
| `--use-username-as-cn` | use username as Common Name | 对象 CN | 强制 CN 使用登录名，而不是姓名组合；有利于稳定路径，但显示不够友好 |
| `--smartcard-required` | smart card required | `userAccountControl` 标志 | 要求交互登录使用智能卡；启用前必须完成证书和客户端基础设施 |

`--given-name`、`--surname`、`--department` 等参数写入的是身份资料，不会自动把用户加入任何角色组。部门成员关系按 [ANAS 公司部门组约定](/architecture/samba-ad-user-planning#anas-department-groups)单独维护；其他权限仍由第 4～6 节的组成员关系决定。

注意事项：

- `wanghailong` 是 `sAMAccountName`，必须在域内唯一。建议只使用稳定的英文、数字、点、连字符或下划线命名。
- UPN 必须显式设置为 `<sAMAccountName>@<AD DNS realm>`。例如 realm 为 `LNNJ.COM.CN` 时，应使用 `wanghailong@lnnj.com.cn`；`wanghailong` 不是合规 UPN。
- `--userou='OU=People'` 不需要附加域 DN；Samba 会自动补全当前域的 `DC=...`。
- 省略位置参数中的密码后，`-it` 让命令在终端中安全提示输入密码，避免密码进入 shell 历史和进程参数。
- 密码必须满足 `samba-tool domain passwordsettings show` 显示的域策略。
- `--must-change-at-next-login` 适合人工初始密码；应用或服务账号不应照搬这一选项。
- 当前 ANAS 的 LDAP 应用只搜索 `OU=People`。把需要登录 ANAS 应用的账号放进 `OU=Admins` 会使其不在应用用户搜索范围内。

如果用户已经建在默认的 `CN=Users` 中，可将其移入人员 OU：

```bash
docker exec anas_samba_dc samba-tool user move wanghailong 'OU=People'
```

| 参数 | 英文含义 | 说明 |
| --- | --- | --- |
| `user move` | move a user | 把现有用户移动到新的父 OU 或容器，用户 SID 不变但 DN 改变 |
| `wanghailong` | username | 第一个必填位置参数；要移动的用户 |
| `'OU=People'` | new parent DN | 第二个必填位置参数；目标父路径，可省略当前域的 `DC=...` 后缀 |

### 3.2 查看和修改属性

```bash
# 查看常用属性
docker exec anas_samba_dc samba-tool user show wanghailong \
  --attributes=sAMAccountName,userPrincipalName,displayName,mail,distinguishedName,userAccountControl

# 修改显示名称、邮箱或 UPN
docker exec anas_samba_dc samba-tool user rename wanghailong \
  --display-name='王海龙' \
  --mail-address='wanghailong@example.com' \
  --upn='wanghailong@nas.example.com'
```

查看和重命名参数：

| 参数 | 英文含义 | 说明 |
| --- | --- | --- |
| `--attributes=a,b,c` | attributes to display | 只输出逗号分隔的 LDAP 属性；属性名区分拼写，不要在列表中插入 shell 空格 |
| `--display-name` | display name | 修改 `displayName`，通常只影响界面显示，不改变登录名 |
| `--mail-address` | mail address | 修改 `mail` |
| `--upn` | User Principal Name | 修改 `userPrincipalName`；UPN 后缀必须是目录接受的后缀 |
| `--samaccountname` | SAM account name | 修改兼容登录名；风险高，会影响登录和不使用永久锚点的集成 |
| `--force-new-cn` | force a new Common Name | 明确指定新的 CN/RDN，从而改变对象 DN |
| `--reset-cn` | reset Common Name | 让 Samba 根据 given name、initials 和 surname 重新计算 CN |

`user rename` 可能同时修改显示属性、登录属性和 CN，但它不是删除重建：对象 SID 保持不变。仍应在修改登录名或 DN 前检查 LDAP 客户端是否错误地把旧 DN 当永久主键。

将示例 UPN 后缀 `nas.example.com` 替换为实际 AD DNS realm。UPN 的本地部分必须与
`sAMAccountName` 相同，后缀必须是目录接受的 UPN suffix。修改 `sAMAccountName`、UPN
或邮箱可能影响登录名和第三方应用匹配，变更前应检查所有依赖方。ANAS 的永久身份锚点可
让支持该锚点的应用保持同一身份，但这不能保证所有外部应用都能无感处理登录名变化。

#### 审计和修复 UPN

先限定业务用户 OU，只读列出短登录名和 UPN：

```bash
docker exec anas_samba_dc ldbsearch \
  -H /var/lib/samba/private/sam.ldb \
  -b 'OU=People,DC=nas,DC=example,DC=com' \
  '(objectClass=user)' sAMAccountName userPrincipalName
```

逐个核对 `userPrincipalName` 是否严格等于
`<sAMAccountName>@<AD DNS realm>`。缺失、没有 `@`、后缀不被目录接受或本地部分不一致
都应列为异常。例如 `sAMAccountName=wangdanyi`、realm 为 `LNNJ.COM.CN` 时，正确值为
`wangdanyi@lnnj.com.cn`，只有 `wangdanyi` 是错误值。

确认应用依赖后，用现有对象的短登录名修复，不要删除重建用户：

```bash
docker exec anas_samba_dc samba-tool user rename wangdanyi \
  --upn='wangdanyi@lnnj.com.cn'

docker exec anas_samba_dc samba-tool user show wangdanyi \
  --attributes=sAMAccountName,userPrincipalName,mail,displayName,distinguishedName
```

审计和批量修复不得覆盖内置系统账号，也不得假定 UPN 与 `mail` 必须相等。UPN、邮箱和
`sAMAccountName` 是独立属性；OIDC `preferred_username` 仍应取 `sAMAccountName`，修复
UPN 不应改变应用内部用户 ID。

不要为了让 UPN 和邮箱看起来一致而批量补写 `mail`。未安装邮件 Module 或对应邮箱尚未
实际创建时，`mail` 应保持为空。邮件 Module 创建可投递邮箱或受监控别名后，再用
`--mail-address` 回写实际地址；邮箱改址、迁移或停用时同步更新目录。IAM 不得用 UPN
臆造 email claim；要求邮箱的应用应明确拒绝缺少 `mail` 的账号。

### 3.3 密码、锁定、启用和到期

```bash
# 管理员重置密码；交互输入新密码
docker exec -it anas_samba_dc samba-tool user setpassword wanghailong

# 重置后要求下次登录修改
docker exec -it anas_samba_dc samba-tool user setpassword wanghailong \
  --must-change-at-next-login

# 解锁、禁用、重新启用
docker exec anas_samba_dc samba-tool user unlock wanghailong
docker exec anas_samba_dc samba-tool user disable wanghailong
docker exec anas_samba_dc samba-tool user enable wanghailong

# 设置账号在指定天数后到期，或取消账号到期
docker exec anas_samba_dc samba-tool user setexpiry wanghailong --days=90
docker exec anas_samba_dc samba-tool user setexpiry wanghailong --noexpiry
```

`setexpiry --noexpiry` 表示账号本身不因 `accountExpires` 到期，并不等于密码永不过期；密码有效期仍由域密码策略或 Password Settings Object（PSO）决定。

状态和密码参数：

| 参数 | 英文含义 | 说明 |
| --- | --- | --- |
| `user setpassword <username>` | set or reset password | 管理员重置指定用户密码；省略 `--newpassword` 时交互提示 |
| `--newpassword=<值>` | new password | 非交互提供新密码，但会暴露到进程参数和 shell 历史，不推荐 |
| `--must-change-at-next-login` | must change at next logon | 与密码重置一起使用，使临时密码只用于首次登录 |
| `user unlock` | unlock account | 清除因失败登录次数达到阈值产生的锁定状态 |
| `user disable` / `enable` | disable / enable account | 禁止或恢复认证，不删除对象、SID 或文件 ACL |
| `--remove-supplemental-groups` | remove supplemental groups | `user disable` 的高影响选项：同时移除除主组外的所有组。会丢失授权记录，除非已导出成员关系，否则不要使用 |
| `setexpiry --days=N` | expire after N days | 设置账号对象从当前时间起 N 天后到期 |
| `setexpiry --noexpiry` | account never expires | 清除账号到期时间，不改变密码到期策略 |

### 3.4 停用和删除

离职或不再使用时，优先禁用和撤权，不要立即删除：

```bash
docker exec anas_samba_dc samba-tool user disable wanghailong
docker exec anas_samba_dc samba-tool group removemembers 'APP_nextcloud' wanghailong
docker exec anas_samba_dc samba-tool group removemembers 'FS Share RW' wanghailong
docker exec anas_samba_dc samba-tool user move wanghailong 'OU=Graveyard'
```

`user disable` 和高影响选项 `--remove-supplemental-groups` 见 [§3.3](#_3-3-密码、锁定、启用和到期)；`group removemembers` 见 [§4.2](#_4-2-添加、移除和核验成员)。`user move` 的参数顺序与 [§3.1 的移动示例](#_3-1-创建普通用户) 相同，这里只是把目标改为 [ANAS 停用目录 `OU=Graveyard`](/architecture/samba-ad-user-planning#anas-directory-layout)。

完成数据交接、审计和保留期后再删除：

```bash
docker exec anas_samba_dc samba-tool user delete wanghailong
```

| 参数 | 英文含义 | 说明 |
| --- | --- | --- |
| `user delete` | delete a user | 永久删除目录对象；不是“禁用” |
| `wanghailong` | username | 必填位置参数；要删除的用户 |
| `--help` | help | 删除前可运行 `samba-tool user delete --help` 核对当前版本语法；该子命令通常没有业务属性选项 |

删除再重建同名用户会产生新的 SID。旧文件 ACL 中仍可能保留原 SID，因此“同名重建”不能恢复原有文件权限；删除前必须确认 Home、Share 和其他域资源的 ACL 已完成交接。

## 4. 创建和管理组

创建前先查 [ANAS 组命名规范](/architecture/samba-ad-user-planning#anas-group-naming)：管理员自建的部门、职责、项目和资源组使用稳定代码；ANAS 固定组、动态 `APP_*` 组以及 AD 内置组保留产品或目录定义的准确名称，不重复创建也不擅自改名。公司部门统一使用 [`ROLE_DEPT_<公司代码>_<部门代码>`](/architecture/samba-ad-user-planning#anas-department-groups)，例如 `ROLE_DEPT_ANAS_IT`。

### 4.1 创建业务角色组

ANAS 建议把组按职责放入 [ANAS 默认组目录](/architecture/samba-ad-user-planning#anas-directory-layout)：

- `OU=Role,OU=Groups`：人员职责或业务角色；
- `OU=Access,OU=Groups`：文件等资源权限；
- `OU=Apps,OU=Groups`：应用登录范围。

下面创建项目角色组。`ROLE_PROJECT_A` 是文档示例，不是 ANAS 预创建组：

```bash
docker exec anas_samba_dc samba-tool group add 'ROLE_PROJECT_A' \
  --groupou='OU=Role,OU=Groups' \
  --group-scope=Global \
  --group-type=Security \
  --description='Project A 成员'
```

组创建参数：

| 参数 | 英文含义 | 对应属性或行为 | 说明 |
| --- | --- | --- | --- |
| `ROLE_PROJECT_A` | group name | `sAMAccountName`，默认也作为 CN | 必填位置参数；组名包含空格时必须加引号 |
| `--groupou` | group organizational unit | 对象 DN 的父路径 | 指定组创建位置；默认通常是 `CN=Users` |
| `--group-scope` | group scope | `groupType` 的 scope 位 | 可选 `Global`、`Universal`、`Domain`；Samba 的 `Domain` 对应 AD 术语 Domain Local |
| `--group-type` | group type | `groupType` 的 security 位 | `Security` 用于授权；`Distribution` 仅用于分发 |
| `--description` | description | `description` | 记录组代表的职责、资源、负责人或审批范围 |
| `--mail-address` | mail address | `mail` | 组邮箱地址；只在邮件系统需要时设置 |

组作用域决定“可以包含谁”和“可以在哪里授予权限”：

| Samba 值 | AD 英文术语 | 可包含的典型对象 | 可授予权限的范围 | ANAS 建议 |
| --- | --- | --- | --- | --- |
| `Global` | Global group | 同一域的账号和 Global 组 | 本林任意域及信任域 | 单域人员角色和业务职责的默认选择 |
| `Universal` | Universal group | 同一林任意域的账号、Global/Universal 组 | 本林任意域及信任林 | 只有多域聚合确有需要时使用，会进入全局编录复制 |
| `Domain` | Domain Local group | 任意受信任域的账号及多种组 | 仅本域 | 适合表达“本域某资源的访问权”；命令值不是 `DomainLocal` |

常用选择：

- `Security` 组可用于权限控制；`Distribution` 组主要用于邮件分发，不能作为资源 ACL 的安全主体。
- 单域中的人员角色通常使用 `Global`。
- 跨域聚合可使用 `Universal`；给本域资源分配权限可使用 `Domain`（Domain Local）。没有多域需求时不要增加不必要的复杂度。

### 4.2 添加、移除和核验成员

```bash
# 一次添加一个成员，便于审计和定位错误
docker exec anas_samba_dc samba-tool group addmembers 'ROLE_PROJECT_A' wanghailong
docker exec anas_samba_dc samba-tool group addmembers 'ROLE_PROJECT_A' bob

# 查看成员和组属性
docker exec anas_samba_dc samba-tool group listmembers 'ROLE_PROJECT_A'
docker exec anas_samba_dc samba-tool group show 'ROLE_PROJECT_A'

# 移除成员
docker exec anas_samba_dc samba-tool group removemembers 'ROLE_PROJECT_A' wanghailong
```

`user getgroups` 和 `group listmembers` 显示的是直接关系，不会把所有嵌套组展开成最终有效权限。组嵌套后，还要同时检查父组与子组，并在实际应用或客户端上验证授权结果。

成员管理和列表参数：

| 参数 | 英文含义 | 说明 |
| --- | --- | --- |
| `groupname` | target group name | 第一个位置参数，目标组的 `sAMAccountName` |
| `members` | members to add or remove | 第二个位置参数，可为用户、计算机或组；本文一次处理一个成员，便于审计 |
| `listmembers --full-dn` | show full Distinguished Names | 输出完整 DN，而不是优先输出 `sAMAccountName` |
| `listmembers --hide-expired` | hide expired members | 列表中隐藏已过期账号；做审计时通常不要隐藏 |
| `listmembers --hide-disabled` | hide disabled members | 列表中隐藏已禁用账号；做权限清理时通常不要隐藏 |

### 4.3 用嵌套组分配权限

可以先把用户加入业务角色组，再把角色组加入资源组：

```bash
docker exec anas_samba_dc samba-tool group addmembers 'ROLE_PROJECT_A' wanghailong
docker exec anas_samba_dc samba-tool group addmembers 'APP_nextcloud' 'ROLE_PROJECT_A'
```

参数顺序仍是 [§4.2](#_4-2-添加、移除和核验成员) 定义的“目标组、成员”。第二条命令把组 `ROLE_PROJECT_A` 作为成员，因此形成嵌套；[`APP_nextcloud`](/architecture/samba-ad-user-planning#anas-app-groups) 的 ANAS 定义见应用组规范。

ANAS 的应用过滤器支持这种递归成员关系。撤权时移除父子组关系即可：

```bash
docker exec anas_samba_dc samba-tool group removemembers 'APP_nextcloud' 'ROLE_PROJECT_A'
```

这里从父组 `APP_nextcloud` 移除子组 `ROLE_PROJECT_A`；子组内用户本身仍保留。`removemembers` 参数见 [§4.2](#_4-2-添加、移除和核验成员)。

不要形成循环嵌套，也不要把普通业务组嵌套进高权限管理员组。

### 4.4 删除组

```bash
docker exec anas_samba_dc samba-tool group delete 'ROLE_PROJECT_A'
```

| 参数 | 英文含义 | 说明 |
| --- | --- | --- |
| `group delete` | delete a group | 删除组目录对象和该组 SID 的可解析主体 |
| `'ROLE_PROJECT_A'` | group name | 必填位置参数；要删除的组。引号可保护空格和 shell 特殊字符 |
| `--help` | help | 删除前运行 `samba-tool group delete --help` 核对本机版本语法 |

组也有自己的 SID。删除组会使引用该 SID 的文件或应用 ACL 失去可解析主体；删除前应先导出成员、移除资源 ACL，并确认没有应用过滤器仍引用该组。

## 5. “设置管理员”必须先确定管理范围

“管理员”不是单一权限。ANAS 中至少有以下几类：

| 组 | 英文展开或直译 | 获得的权限 | 是否等同域管理员 | 典型用途 |
| --- | --- | --- | --- | --- |
| [`Domain Admins`](/architecture/samba-ad-user-planning#ad-built-in-groups) | Domain Administrators，域管理员 | 整个 AD 域的高权限管理 | 是 | 管理域、域控、用户、组和域级配置 |
| [`Administrators`](/architecture/samba-ad-user-planning#ad-built-in-groups) | Administrators，管理员 | AD 内置管理员组的高权限 | 是，高风险 | 底层目录或域控制器管理；通常不需单独直加用户 |
| [`Unix Admins`](/architecture/samba-ad-user-planning#anas-role-groups) | Unix Administrators，Unix 管理员 | ANAS 域基础设施管理；该组已嵌套进 `Administrators` | 是，高风险 | Samba/Unix 域基础设施管理 |
| [`Group Policy Creator Owners`](/architecture/samba-ad-user-planning#ad-built-in-groups) | Group Policy Creator Owners，组策略创建者所有者 | 创建和管理其负责的 GPO | 否，不是完整域管理员 | 组策略维护 |
| [`Admins`](/architecture/samba-ad-user-planning#anas-role-groups) | Administrators 的缩写，管理员 | ANAS 应用管理员角色 | 否 | 登录并管理 Authentik、Nextcloud、LAM 等应用；目录操作仍受 AD ACL 限制 |
| [`FS Admins`](/architecture/samba-ad-user-planning#anas-access-groups) | File System Administrators，文件系统管理员 | Samba 文件服务器上的 root 等效文件操作能力 | 否，但风险极高 | 修复 ACL、恢复文件、文件服务运维 |
| [`FS Share RW`](/architecture/samba-ad-user-planning#anas-access-groups) | File System Share Read/Write，共享读写 | 公共 `Share` 的写权限 | 否 | 普通文件协作 |
| [`APP_all` / `APP_<应用>`](/architecture/samba-ad-user-planning#anas-app-groups) | Application all / Application name，全部或指定应用 | 所有或指定应用的登录权限 | 否 | 普通应用访问 |

### 5.1 创建专用域管理员

不要把日常账号 `wanghailong` 直接升级为域管理员。先创建独立的管理账号：

```bash
docker exec -it anas_samba_dc samba-tool user add adm_wanghailong \
  --userou='OU=People' \
  --given-name='海龙' \
  --surname='王' \
  --description='王海龙的专用域管理账号'

docker exec anas_samba_dc samba-tool user rename adm_wanghailong \
  --display-name='王海龙（域管理员）'

docker exec anas_samba_dc samba-tool group addmembers 'Domain Admins' adm_wanghailong
docker exec anas_samba_dc samba-tool group listmembers 'Domain Admins'
```

本节复用前文语法：创建参数见 [§3.1](#_3-1-创建普通用户)，显示名参数见 [§3.2](#_3-2-查看和修改属性)，加组和核验参数见 [§4.2](#_4-2-添加、移除和核验成员)。`adm_wanghailong` 遵循 [ANAS 管理账号命名规范](/architecture/samba-ad-user-planning#anas-account-naming)；`Domain Admins` 是 [AD 内置高权限组](/architecture/samba-ad-user-planning#ad-built-in-groups)，不是 ANAS 自定义组。

`Domain Admins` 已是极高权限，不要为了“看起来更完整”再自动加入 `Administrators`、`Enterprise Admins`、`Schema Admins`、`FS Admins` 或 `Admins`。只有职责明确需要时才分别授权。

当前 ANAS 把需要被 LDAP 应用发现的人工账号放在 `OU=People`；`OU=Admins` 目前是预留的特权账号 OU，不在各应用的用户搜索基准中。如果专用域管理员永远不需要登录 ANAS 应用，也可以经评估后放入 `OU=Admins`，但要接受其无法通过当前应用 LDAP 搜索登录的结果。

### 5.2 设置应用管理员

```bash
docker exec anas_samba_dc samba-tool group addmembers 'Admins' adm_wanghailong
docker exec anas_samba_dc samba-tool group listmembers 'Admins'
```

参数见 [§4.2](#_4-2-添加、移除和核验成员)。[`Admins`](/architecture/samba-ad-user-planning#anas-role-groups) 是 ANAS 应用管理员角色，不是 AD 内置 `Administrators`，也不会自动赋予 `Domain Admins`、宿主机 root、数据库超级用户或 `FS Admins` 权限。

### 5.3 设置文件服务器管理员

```bash
docker exec anas_samba_dc samba-tool group addmembers 'FS Admins' fsadm_wanghailong
docker exec anas_samba_dc samba-tool group listmembers 'FS Admins'
```

参数见 [§4.2](#_4-2-添加、移除和核验成员)。[`FS Admins`](/architecture/samba-ad-user-planning#anas-access-groups) 在 Samba 文件服务器上具有 root 等效文件能力；`fsadm_wanghailong` 遵循 [ANAS 管理账号命名规范](/architecture/samba-ad-user-planning#anas-account-naming)。应避免把普通日常账号、`Domain Admins` 或整个业务组加入其中。

### 5.4 设置 GPO 管理员或工作站本地管理员

只负责创建 GPO 时，可按职责加入：

```bash
docker exec anas_samba_dc samba-tool group addmembers \
  'Group Policy Creator Owners' adm_wanghailong
```

参数见 [§4.2](#_4-2-添加、移除和核验成员)。`Group Policy Creator Owners` 是 [AD 内置组](/architecture/samba-ad-user-planning#ad-built-in-groups)，允许创建和管理其负责的 GPO，但不等于完整域管理员。

如果目标只是让某些人管理 Windows 工作站，不应把他们加入 `Domain Admins`。更安全的做法是创建例如 `ROLE_WORKSTATION_ADMINS` 的普通安全组，再通过 GPO 将该组加入目标工作站的本地 `Administrators` 组。`samba-tool group addmembers` 只管理 AD 组成员关系，本身不会自动修改每台 Windows 计算机的本地管理员组。

### 5.5 撤销管理员权限

```bash
docker exec anas_samba_dc samba-tool group removemembers 'Domain Admins' adm_wanghailong
docker exec anas_samba_dc samba-tool group removemembers 'Admins' adm_wanghailong
docker exec anas_samba_dc samba-tool group removemembers 'FS Admins' fsadm_wanghailong
docker exec anas_samba_dc samba-tool group removemembers \
  'Group Policy Creator Owners' adm_wanghailong
```

四条命令都使用 [§4.2](#_4-2-添加、移除和核验成员) 的“目标组、待移除成员”参数顺序。

撤权后重新运行 `group listmembers`。已登录的 Windows、Kerberos 或应用会话可能仍持有旧的授权令牌，应让用户注销并重新登录，必要时清理 Kerberos ticket 和应用会话，再进行实际权限测试。

## 6. 新用户的典型授权模板

本节只给可复制模板，不重复 `group addmembers` 参数；语法统一见 [§4.2](#_4-2-添加、移除和核验成员)。组名的来源和权限边界见 [ANAS 组命名规范](/architecture/samba-ad-user-planning#anas-group-naming)。

### 6.1 普通用户，只登录一个应用

```bash
docker exec anas_samba_dc samba-tool group addmembers 'APP_nextcloud' wanghailong
```

### 6.2 普通用户，可登录所有启用的应用

```bash
docker exec anas_samba_dc samba-tool group addmembers 'APP_all' wanghailong
```

### 6.3 普通用户，可写公共共享

```bash
docker exec anas_samba_dc samba-tool group addmembers 'FS Share RW' wanghailong
```

### 6.4 ANAS 应用管理员，但不是域管理员和文件管理员

```bash
docker exec anas_samba_dc samba-tool group addmembers 'Admins' adm_wanghailong
```

### 6.5 完整的专用域管理员

```bash
docker exec anas_samba_dc samba-tool group addmembers 'Domain Admins' adm_wanghailong
```

不要用一串固定命令把每个“管理员”同时加入 `Domain Admins`、`Admins` 和 `FS Admins`。这三个组分别控制目录、应用和文件服务，应由不同职责独立审批。

## 7. 核验和故障排查

### 7.1 创建失败

先查看子命令帮助和密码策略：

```bash
docker exec anas_samba_dc samba-tool user add --help
docker exec anas_samba_dc samba-tool domain passwordsettings show
```

| 命令或参数 | 英文含义 | 说明 |
| --- | --- | --- |
| `user add --help` | show help for user creation | 显示本机 Samba 版本支持的语法、位置参数和全部可选参数；不会创建用户 |
| `domain passwordsettings show` | show domain password settings | 只读显示域密码和锁定策略 |
| `domain passwordsettings set` | set domain password settings | 常用但高影响的修改子命令；本文排障流程不执行它 |
| `set --complexity=on\|off` | password complexity | 设置是否要求复杂密码 |
| `set --min-pwd-length=N` | minimum password length | 设置最短密码长度 |
| `set --history-length=N` | password history length | 设置禁止重复使用的历史密码数量 |
| `set --min-pwd-age=N` / `--max-pwd-age=N` | minimum / maximum password age | 设置密码最短和最长使用天数 |
| `set --account-lockout-threshold=N` | account lockout threshold | 设置触发锁定的失败登录次数 |
| `set --account-lockout-duration=N` | account lockout duration | 设置锁定持续分钟数 |
| `set --reset-account-lockout-after=N` | reset lockout counter after | 设置多少分钟后重置失败计数 |

修改域密码策略会影响全部未被更具体 PSO 覆盖的用户，应先记录原值并经过变更审批。

常见原因包括用户名已存在、OU 路径错误、密码不符合长度/复杂度/历史策略，以及当前操作者没有创建对象的 ACL。

### 7.2 已加组但权限没有立即生效

依次检查：

```bash
docker exec anas_samba_dc samba-tool user getgroups wanghailong
docker exec anas_samba_dc samba-tool group listmembers '目标组'
docker exec anas_samba_dc samba-tool user show wanghailong
```

命令参数分别见 [§3.2 用户属性查询](#_3-2-查看和修改属性)和 [§4.2 组成员核验](#_4-2-添加、移除和核验成员)。排查缺少权限时不要使用 `--hide-expired` 或 `--hide-disabled`，以免漏掉恰好需要清理的账号。

然后确认：

1. 用户是否被禁用或过期；
2. 是否只检查了直接成员而漏掉嵌套组；
3. 应用是否配置了正确的 `APP_*` 过滤组；
4. 客户端或应用是否仍使用旧的登录会话、Kerberos ticket 或组缓存；
5. 文件权限是否还需要在目标 Share 的 ACL 上授予对应安全组。

### 7.3 用户能登录应用，但不能管理 AD

这是可能的正常结果。加入 `Admins` 只授予 ANAS 应用管理入口，不会绕过 AD ACL。确需域级管理时使用专用账号加入 `Domain Admins`；如果只需管理某个 OU，更推荐对专用管理组委派该 OU 的最小 ACL，而不是授予整个域管理员权限。

### 7.4 不要给 ANAS 用户手动添加 Unix UID/GID

ANAS 的 Samba 文件服务器当前使用确定性的 `idmap_rid`，不读取用户对象的 `uidNumber` 和 `gidNumber`。因此不要照搬通用 Samba RFC2307 教程执行 `user addunixattrs` 或 `group addunixattrs`。已有文件数据后切换 ID 映射方式可能改变 Unix 所有者映射，必须作为迁移项目单独设计和验证。

## 8. 安全和运维要求

1. 每个自然人使用独立账号；日常账号和 `adm_*`、`fsadm_*` 管理账号分离。
2. 密码不要作为位置参数或 `-U user%password` 写进 shell 历史。Samba 官方也建议使用交互提示、Kerberos，或权限受控的 `PASSWD_FD` / `PASSWD_FILE`。
3. `Domain Admins`、`Administrators`、`Unix Admins`、`FS Admins` 至少每月复核；`Admins`、`APP_all`、`APP_*`、`FS Share RW` 至少每季度复核。
4. 高权限组变更应记录申请人、批准人、执行人、原因、时间和撤销时间。
5. 用户离开时先禁用、撤组和转移到 `OU=Graveyard`，完成数据与 ACL 交接后再删除。
6. `Enterprise Admins` 和 `Schema Admins` 不用于日常管理，只在明确的林级或架构变更窗口临时授权并立即撤销。
7. 批量执行前先在测试账号验证，并备份 AD、组成员关系和关键文件 ACL。

## 9. 常用命令速查

| 目的 | 命令 |
| --- | --- |
| 列出用户 | `samba-tool user list` |
| [创建用户](#_3-1-创建普通用户) | `samba-tool user add <用户名> --userou='OU=People'` |
| [查看用户](#_3-2-查看和修改属性) | `samba-tool user show <用户名>` |
| [查看用户直接所属组](#_4-2-添加、移除和核验成员) | `samba-tool user getgroups <用户名>` |
| [重置密码](#_3-3-密码、锁定、启用和到期) | `samba-tool user setpassword <用户名>` |
| [解锁用户](#_3-3-密码、锁定、启用和到期) | `samba-tool user unlock <用户名>` |
| [禁用/启用用户](#_3-3-密码、锁定、启用和到期) | `samba-tool user disable <用户名>` / `user enable` |
| [移动用户](#_3-1-创建普通用户) | `samba-tool user move <用户名> 'OU=People'` |
| [删除用户](#_3-4-停用和删除) | `samba-tool user delete <用户名>` |
| [列出组](#_4-2-添加、移除和核验成员) | `samba-tool group list` |
| [创建组](#_4-1-创建业务角色组) | `samba-tool group add <组名> --groupou='<OU>'` |
| [查看组成员](#_4-2-添加、移除和核验成员) | `samba-tool group listmembers '<组名>'` |
| [加入组](#_4-2-添加、移除和核验成员) | `samba-tool group addmembers '<组名>' <成员>` |
| [移出组](#_4-2-添加、移除和核验成员) | `samba-tool group removemembers '<组名>' <成员>` |
| [查看密码策略](#_7-1-创建失败) | `samba-tool domain passwordsettings show` |
| 查看特权 PSO | `samba-tool domain passwordsettings pso show pso_privileged` |

在宿主机执行时，在上述命令前加 `docker exec anas_samba_dc`；需要交互输入密码的命令使用 `docker exec -it`。

用户列表还有几个适合审计的参数：

| 参数 | 英文含义 | 用途 |
| --- | --- | --- |
| `user list --full-dn` | show full Distinguished Names | 同时核对用户所在 OU |
| `user list --base-dn='<DN>'` | search base DN | 只列出指定目录基准下的用户，例如 `OU=People` |
| `user list --hide-expired` | hide expired accounts | 排除已到期账号；全面审计时不要使用 |
| `user list --hide-disabled` | hide disabled accounts | 排除已禁用账号；全面审计时不要使用 |
| `user list --locked-only` | show locked accounts only | 只列出当前锁定的账号，适合登录故障排查 |

## 10. 参考资料

- [Samba 官方 `samba-tool` 4.23 手册](https://www.samba.org/samba/docs/current/man-html/samba-tool.8.html)：用户、组、OU、密码策略和通用凭据选项。
- [SambaWiki：Adding users with samba tool](https://wiki.samba.org/index.php/Adding_users_with_samba_tool)：用户属性、OU 和首次登录修改密码选项。
- [SambaWiki：Group Policy](https://wiki.samba.org/index.php/Group_Policy)：通过 GPO 管理 Windows 工作站本地管理员组。
- [Microsoft Learn：Active Directory security groups](https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/manage/understand-security-groups)：安全组、分发组、组作用域和内置组定义。
- [Microsoft Learn：Privileged accounts and groups](https://learn.microsoft.com/en-us/windows-server/identity/ad-ds/plan/security-best-practices/appendix-b--privileged-accounts-and-groups-in-active-directory)：`Domain Admins`、`Administrators`、`Enterprise Admins` 和 `Schema Admins` 的高权限风险。
- [ANAS Samba AD 用户、组命名与权限规划](/architecture/samba-ad-user-planning#anas-group-naming)：本仓库的目录结构、组命名、账号分类和权限矩阵。
- [ANAS Samba 与 Active Directory 运维说明](../samba.md)：文件服务组、应用组和 ID 映射约束。
