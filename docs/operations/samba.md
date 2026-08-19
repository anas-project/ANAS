
# Samba 与 Active Directory

## 简介

`Samba`是一套Linux/Unix上开源的和Windows互操作的套件，主要有以下几个功能，
1. 基于[SMB协议](https://zh.wikipedia.org/wiki/%E4%BC%BA%E6%9C%8D%E5%99%A8%E8%A8%8A%E6%81%AF%E5%8D%80%E5%A1%8A)文件和打印机共享的服务器端和客户端。
2. 作为`Domain member`接入微软的`Active Directory（AD）`，让`AD`管理Linux上的用户和群组。
3. 全功能的`Active Directory` 的域控制器（`Domain Controller`），兼容`windows server 2008 r2`协议，完全可以替代windows server，管理域内计算机。

使用过windows server的朋友，对于`Active Directory（AD）`一定不陌生。AD作为`Windows Server`的目录管理服务，掌握着域（`Domain`）内的所有资源，包括用户，计算机，组，软件，权限等。想象这样一个场景，我们每次重装系统都要填写用户名密码，还要分配一个默认的管理员权限，之后要装软件，调整系统设置等等。如果我们要为家里爸爸、妈妈、姐姐和哥哥安装系统，那每次都要重复这些步骤。AD的作用就是让我们在一台服务器上统一管理这些权限和配置，每次有新的电脑加入，我们可以通过AD的管理员账户，把计算机加入域，然后设置域内的哪个用户可以访问这台电脑，有什么权限，用户在AD上的配置信息（组策略）也会自动部署到这台电脑上。我们也可以允许爸爸妈妈作为普通用户访问这台电脑，哥哥有这台电脑的管理员权限，他们登录之后都会自动部署各自的组策略。

当然，我们也可以把AD集成在一些软件里，如`nextcloud`，这样家人就可以以相同的用户名密码登录相应的服务。

域（`Domain`）也称网域。域是一个范围，可以是一个家庭，也可以是个小型的公司，如果公司组织结构庞大复杂，也可以针对不同分公司，或者部门划分子域。域通常作为一个管理单位，我们可以在域内指定不同权限的管理员。

域控制器`Domain Controller（DC）`，顾名思义就是用来提供域服务的服务器。域控制器可以在域内提供用户的身份认证，存储用户账户信息，执行域组策略等服务。`Windows Server`可以提供DC的集群功能，也就是在网内部署多个DC，实现故障转移。`Samba`也可以提供相同的功能，即可以加入`Windows Server`的集群，也可以组建自己的集群。

在`AD`中，我们需要给每个域起个名字，称为域名。这个域名和我们平时访问网站的域名类似，也是通过DNS解析的。域名最好命名为我们公网上拥有的域名。如果我拥有`example.com`这个域名，我AD的域名可以是`example.com`这样的顶级域名，或者`corp.example.com`这样的子域名。

`AD`是要依赖内网的DNS工作的。`Samba`支持两种DNS解析方式，Samba内置的DNS，或者使用开源的`BIND`DNS服务器。正常情况下Samba内置DNS已经提供了足够可以让`Samba DC`运行起来的功能。`BIND`则可以提供更多的额外功能。

## ANAS 中的应用域与 AD 域

ANAS 将两个命名空间分开：

- `global.base_domain` / `BASE_DOMAIN` 只表示应用和 Web 入口域，用于应用 URL、SSO
  Cookie、回调地址、Web 证书和公网 DDNS；
- `modules.samba_dc.config.domain` / `SAMBA_DC_DOMAIN` 表示 AD DNS 域，并决定 Kerberos
  Realm、Base DN、DC canonical FQDN、UPN 默认 suffix 和成员机信任。

新 workspace 应在首次 provision 前确定两者：

```yaml
global:
  base_domain: nas.example.net

modules:
  samba_dc:
    config:
      domain: corp.example.com
      application_dns_mode: auto
```

旧配置没有 `modules.samba_dc.config.domain` 时，有效 AD 域继续回退到 `BASE_DOMAIN`。这是
兼容旧 workspace 原有身份的规则，不是 AD 域重命名功能。

### 应用 DNS mode

Samba DC validate Hook 在 plan 阶段解析 `application_dns_mode`：

| 请求值 | 规则 | 权威 zone |
| --- | --- | --- |
| `auto` | 应用域等于 AD 域或是 AD 域的 label 子域时使用 `ad_zone`；其他关系使用 `separate_zone` | 由解析结果决定 |
| `ad_zone` | 只允许相等或 label 子域关系 | `SAMBA_DC_DOMAIN` |
| `separate_zone` | 两个域可以无关，但不能完全相同；等域必须使用现有 AD zone | `BASE_DOMAIN` |

`ad_zone` 示例：应用 `cloud.nas.corp.example.com` 在 AD zone `corp.example.com` 内使用
owner `cloud.nas`，不能只取第一段。`separate_zone` 示例：应用
`cloud.nas.example.net` 在 zone `nas.example.net` 内使用 owner `cloud`。独立应用 zone 是
Samba 内部的 split-horizon 权威 zone；它必须是当前 workspace 可完整维护的专用命名空间。
缺失的公网记录不会被 Samba 继续转发，而会返回不存在。

操作前先看解析计划：

```bash
anas plan -w /srv/anas
anas plan -w /srv/anas --json | jq '.module_plans.samba_dc'
```

计划同时显示 `requested_mode`、`resolved_mode` 和 `zone`，生成 deployment 后还会固化到
manifest 的 `modules.samba_dc.validation_plan`，可用于审计实际选择。Runner 发送给 Samba
zone reconciler 的 `DOMAINS` 使用 `inner/<完整 FQDN>/<module>`，只包含声明发布 Web 域的
Module，不包含 `SAMBA_DC_DOMAIN`。Web A 记录指向 `HOST_IP`。

ANAS 内部 LDAPS 暂时继续使用 `SAMBA_DC_HOST=BASE_DOMAIN` 作为证书覆盖的服务别名，
reconciler 为它单独维护指向 `SAMBA_DC_HOST_IP` 的 A 记录。该别名不参与 Realm、Base DN、
SRV 或 Kerberos canonical name；`SAMBA_DC_HOST_IP` 也可能与 Web 记录使用的 `HOST_IP`
不同。等域时这个名字就是 AD zone apex；当 `SAMBA_DC_HOST == SAMBA_DC_DC_DOMAIN` 时，
它则与 canonical DC A 重名。这两类记录都由 Samba 自身注册，reconciler 只验证其中存在
精确的 `SAMBA_DC_HOST_IP`，不 claim、add、replace 或 delete。升级时，旧 reconciler 对这类
原生记录取得的所有权会被释放而不删除记录；其他无法证明来源、但恰好命中目标的记录仍只
记录为 legacy observation。

任何 zone 枚举 RPC 失败或无法解析都按“状态未知”停止，而不是按 zone 不存在处理；在写入
zone 或 A 记录前，reconciler 还会逐个检查 `SAMBA_DC_HOST` 和 `DOMAINS` 的完整 FQDN，拒绝
会截获这些名称的更近 child zone。只有已提交清单或 durable pending journal 能证明记录
所有权，显式 DNS 写入失败会撤销 pending，不会把并发出现的人工记录升级为可删除资源。

> [!WARNING]
> 已有 workspace 所需的 `migrate-service-domain` 与
> `migrate-application-dns-zone` 迁移器尚未交付。当前不要对已有部署用普通 apply、原始
> `env` 或手工编辑切换应用域/内部 zone。已 provision 的 `SAMBA_DC_DOMAIN` 永远不支持
> 原地换域；需要新建目录、迁移身份并重新加入 Samba FS 和其他成员机。

### AD的最佳实践

`AD`作为一种`LDAP`服务的实现，同样也提供了非常灵活的信息组织形式。参考Dan Holme的最佳实践[<sup>1</sup>](#refer-anchor-1)[<sup>2</sup>](#refer-anchor-2)[<sup>3</sup>](#refer-anchor-3)，我总结了一套适合小型企业和家庭的`AD`使用方法。这里只讨论在一台`DC`上的用户，计算机，群组的管理方法，有关组策略和集群方面，请看`Dan Holme`的视频。

在`AD`中管理的资源有，用户（User），计算机（Computer），组（Group）

#### 管理员

1. 根据最佳实践，我们应该按照不同权限划分不同管理员，但是因为我们的信息相对来说没有那么复杂，所以我建议只用一个管理员账户即可。
2. 在我们日常使用中，非必要的情况下，尽量避免使用管理员账户，而是应该单独建立一个日常使用的普通账户。
3. 修改管理员的默认名字（Administrator）。



#### 用户

##### 登录名

登录名必须在域内唯一，`Windows 2000`以前使用`sAMAccountName`作为登录用户名，


##### 密码



### User ID

在Linux上集成`AD`，需要把`AD`上的用户和组对应到Linux的`User ID`/`Group ID`上。ANAS默认使用`tdb` 

https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html#idm410


在使用`ad ID mapping`的时候，如果要将`Administrator`用户映射为`root`用户，不要设置`Administrator`用户的`uidNumber`，否则会覆盖`root`用户为`0`的`UID`

### 默认用户、组与文件权限

ANAS 将身份目录和文件服务分开：

- Samba DC 数据、SYSVOL 和域数据库存放在`${DATA_PATH}/samba_dc/var`。
- Samba FS 的域成员状态存放在`${DATA_PATH}/samba_fs/var`。
- 用户文件存放在`${SAMBA_FS_USERDATA_PATH}`，固定为`${DATA_PATH}/userdata`，在容器内挂载为`/userdata`。
- 用户私有目录为`Home/<用户名>`，首次访问时创建，权限为`0700`。
- 公共共享目录为`Share`，默认不允许匿名访问。设置`SHARE_GUEST_READ_ONLY=Yes`后，guest映射为本地`nobody`，并递归获得`r-X` ACL，所以可以浏览目录和读取文件，但不能写入。guest ACL状态保存在`${SAMBA_FS_USERDATA_PATH}/.anas-share-guest-acl-state`；只有开关变化时才递归扫描Share，普通容器重启不会遍历全部文件。首次切换大型目录时仍会产生一次较高的元数据I/O。

目录结构中的组分为三类；新建部门、职责、项目或资源组前，先按 [ANAS 组命名规范](/architecture/samba-ad-user-planning#anas-group-naming)选择前缀和 OU：

- `Groups/Role`：业务或管理角色，例如`Admins`。`Admins`只表示应用管理员，不自动获得域管理员权限。
- `Groups/Access`：资源权限，包括`FS Admins`和`FS Share RW`。
- `Groups/Apps`：应用登录权限，包括`APP_all`和每个应用自己的`APP_<应用名>`。

公共共享由`SHARE_ACCESS_MODE`控制：

- `all_rw`：所有已认证的`Domain Users`都可以读写。
- `all_read_group_write`：所有已认证的`Domain Users`都可以读取；只有`FS Share RW`和`FS Admins`可以写入。这是默认模式。

应用访问仍通过组成员关系授权，例如：

```bash
samba-tool group addmembers "FS Share RW" alice
samba-tool group addmembers "APP_nextcloud" alice
```

`FS Admins`在文件服务器上具有等同root的文件操作能力，只能授予专用文件服务器管理员。不要把`Domain Admins`、`Enterprise Admins`或日常使用账户加入该组。

只读LDAP集成应用使用`svc_ldap`查询目录，不再使用内置`Administrator`。需要修改密码的应用使用通用的`svc_password`账户；该账户只在`OU=People`中继承“重置密码”权限，因此Nextcloud、LLNG等应用可以修改普通用户的AD密码，但不能创建、删除用户或管理管理员和服务账户。AD用户的创建、删除和组管理仍应通过LAM或Samba管理工具完成。

LAM 主登录允许已启用的 `Admins` 组成员（包括嵌套组成员）使用各自的
`sAMAccountName` 和目录密码登录。`svc_ldap` 只负责在登录前查找用户 DN；LAM 随后使用
该用户自己的绑定执行目录操作。因此 `Admins` 负责登录授权，但不会越过 AD ACL：需要
创建、删除或管理域对象的账号仍须按职责加入相应高权限组或获得专门委派。

两个服务账户的随机密码保存在运行目录的`secrets.yml`中，并写入各模块的`.env`供容器使用；两个文件权限均为`0600`。密码是权限保护的明文，不是加密密文，因此运行目录、Docker管理权限和备份必须按秘密数据保护。

普通用户和管理员密码的最小长度均为8位，密码历史为2次。普通用户复杂度关闭；管理员通过`pso_privileged`应用独立策略，复杂度启用，密码最长有效期为0（永不过期）。管理员策略的8项规则由`samba_dc.admin_*`参数传入，不再硬编码在初始化脚本中。

日常 `admin` 账号属于 `Domain Admins`、`Administrators` 和
`Group Policy Creator Owners`，足够日常目录管理。内置 RID 500 `Administrator` 只保留给
域 provision、底层恢复以及架构/林级操作。`admin_password` 与
`administrator_password` 省略时分别生成不同随机 Secret；不要日常使用或复用
`Administrator` 密码。

文件服务器使用确定性的`idmap_rid`映射，因此不读取AD对象上的`uidNumber`和`gidNumber`。已存放数据后不要直接切换到`idmap_ad`或修改映射范围，否则文件的Unix所有者可能发生变化。


### 参考
<div id="refer-anchor-1">

1. [Active Directory Best Practices - Ten Years Later](https://www.youtube.com/watch?v=_Q-rLcBKJaw)
</div>
<div id="refer-anchor-2">

2. [Role-Based Management Extreme Makeover](https://www.youtube.com/watch?v=IKzokBgCp60&t=1161s)
</div>
<div id="refer-anchor-3">

3. [Active Directory Best Practices - Ten Years Later - PDF](http://download.microsoft.com/download/e/a/7/ea75457b-65d0-481c-b53b-d7ca2ae7ee08/s2b%20-%209.pdf)
</div>
