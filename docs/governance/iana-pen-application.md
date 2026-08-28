# IANA Private Enterprise Number 申请草案

使用官方申请表
<https://www.iana.org/assignments/enterprise-numbers/assignment/apply/>。注册免费。
IANA 会发一封确认邮件；确认之后，如果不需要补充材料，通常七天内完成号码分配。

## 需要由项目所有者填写的信息

仓库不得猜测或公开任何个人联系资料。提交前由项目所有者填写下列字段（字段名是表单原文，
不翻译）：

```text
Assignee name / organization: <法定组织或负责人>
Assignee postal address:      <必填>
Assignee country:             <必填>
Assignee phone:               <可选>

Contact name:                 <负责的维护者>
Contact postal address:       <必填>
Contact country:              <必填>
Contact email:                <必填，且需有人查收>
Contact phone/fax:            <可选>
```

IANA 要求说明用途时，建议使用下面这段（提交时保持英文原文）：

```text
ANAS requires a Private Enterprise Number to allocate globally unique object
identifiers for LDAP/Active Directory schema extensions and related identity,
management and telemetry protocol objects maintained by the project.
```

## 分配到号码之后的规划

若 IANA 分配的 PEN 为 `<PEN>`，在本文件中记录 assignee 与注册表 URL，并预留：

```text
1.3.6.1.4.1.<PEN>.1     LDAP/Active Directory schema
1.3.6.1.4.1.<PEN>.1.1   schema classes
1.3.6.1.4.1.<PEN>.1.2   schema attributes
1.3.6.1.4.1.<PEN>.2     management and telemetry
1.3.6.1.4.1.<PEN>.3     protocol identifiers
```

每一项分配都必须先进入仓库的 OID 表，代码才能使用。已废弃的 OID 不得改作他用。

## 与当前 AD schema OID 的关系

`anasIdentityAnchor` 目前使用 Microsoft 的 GUID 派生 schema OID 根，见
[samba-identity-anchor.md](/architecture/samba-identity-anchor)。那个根本身就是全局唯一的，
对私有 AD 林合法。拿到 PEN **不构成**修改一个已经装进已发布林的 OID 的授权。

由于 ANAS 尚未发布该 schema，项目所有者可以在首次生产发布之前二选一：

1. 永久保留 GUID 派生的 OID，PEN 只用于今后的分配；或
2. 等 PEN 到手，替换常量，并在发布前清空全部测试林。

**首次生产环境安装 schema 之后，只能走第 1 条。**
