---
doc_type: requirement
status: current
created: 2026-09-03
updated: 2026-09-03
---

# Samba 身份锚点 OID 与既有目录迁移要求

本文规定 ANAS 如何使用已获分配的 IANA Private Enterprise Number（PEN）管理 OID，以及
`anasIdentityAnchor` 在新 Samba AD 林和已经安装旧 schema 的目录中的兼容迁移边界。身份锚点的
值语义不因 OID 迁移改变：`mS-DS-ConsistencyGuid` 仍是二进制权威值，`anasIdentityAnchor` 仍是
按 AD GUID 字节序派生的 lowercase canonical UUID 文本投影。

架构与具体 schema 形状见
[Samba AD identity anchor](../../docs/architecture/samba-identity-anchor.md)。本文只规定可判定结果；
实施顺序与一次性迁移证据见
[已归档的 Samba 身份锚点 OID 与既有目录迁移实施计划](../plans/archived/samba-identity-anchor.md)。

## 1. PEN 与内部 OID 注册

IANA 已分配 PEN `66678`，ANAS 的企业 OID 根固定为：

```text
1.3.6.1.4.1.66678
```

仓库内部按以下 arc 分配对象：

```text
1.3.6.1.4.1.66678.1       LDAP/Active Directory schema
1.3.6.1.4.1.66678.1.1     schema classes
1.3.6.1.4.1.66678.1.2     schema attributes
1.3.6.1.4.1.66678.1.2.1   anasIdentityAnchor attributeID
1.3.6.1.4.1.66678.2       management and telemetry
1.3.6.1.4.1.66678.3       protocol identifiers
```

IANA 只管理 PEN 根及其公开登记信息，不逐项登记企业根下的子 OID。任何代码、schema、协议或遥测
对象使用新子 OID 前，必须先在仓库的 OID 注册表中记录用途、状态和所有者；同一 OID 只能有一种
含义。已经发布或安装过的 OID 即使废弃也只改为 retired，不删除、不改义、不复用。IANA assignee
或联系信息变化时使用 IANA 修改流程，不把内部子 OID 当成新的 PEN 申请。

公开治理文档可以记录 PEN、assignee 和公开注册表 URL，但不得从申请邮件或运维现场复制私人联系
邮箱、地址、电话、SSH 目标或认证材料。

## 2. 新 schema 契约

新安装以及完成迁移后的 `anasIdentityAnchor` 必须使用
`1.3.6.1.4.1.66678.1.2.1`。旧 GUID 派生 attributeID：

```text
1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1
```

只作为既有目录的 legacy 标识保留，不得再用于新林。旧 schema 对象使用的 `schemaIDGUID`
`7108c5a7-2290-45e0-9eba-eef087be58e3` 同样属于 legacy 对象；替代对象必须使用另一个一次生成、
登记后固定且跨林一致的 `schemaIDGUID`。安装脚本、委派 ACL、测试和文档必须引用同一个新值。

OID 变化不改变 LDAP 消费契约。新对象仍使用 `lDAPDisplayName=anasIdentityAnchor`，Unicode string
syntax、single-valued、长度 `36`、indexed，并进入 `user` 与 `group` class 的 `mayContain`。新林重复
执行安装必须无副作用；相同名称对应其他 OID、相同 OID 或 `schemaIDGUID` 已被其他对象占用、或
syntax/shape 不兼容时必须停止。加入既有林的 DC 只能验证已经复制的 schema，不得自行创建另一份。

## 3. 既有单 DC 迁移

首个迁移工具只支持经预检确认的单 DC Samba 林，且源 schema 必须精确匹配上节登记的 legacy
attributeID、legacy `schemaIDGUID`、名称和 shape。多 DC、状态未知、标识冲突或已经出现未识别的
部分迁移时必须停止并给出诊断，不能猜测修复。

工具默认执行只读检查；任何 schema 写入都要求显式执行开关。在第一次不可逆写入前，工具必须确认
存在本次可恢复且已经验证的目录备份，并导出 schema 状态以及所有受影响用户、组的 DN 与锚点值。
导出和临时文件必须限制访问，命令与日志不得打印管理员密码、Bind Secret 或完整私有连接信息。

迁移完成后必须同时满足：

- legacy schema 对象作为 defunct 历史保留，不被删除或改作他用；
- 新对象使用正式 PEN OID 和新 `schemaIDGUID`，并成为 `user`、`group` class 当前允许的
  `anasIdentityAnchor`；
- 每个迁移前存在的文本锚点逐项保持相同，数量、DN 和值均可由受保护导出核对，且不存在 malformed
  或 duplicate anchor；
- `svc_anchor` 对新 printable attribute 保留最小写权限，旧 attribute-specific 写入 ACE 被移除，
  其他目录写权限不扩大；
- anchor worker 能继续由 `mS-DS-ConsistencyGuid` 写入同名文本属性，IAM 与 LDAP Consumer 继续读取
  相同值，账号改名后仍复用原应用身份。

工具必须识别“尚未迁移”“已完整迁移”和“部分迁移”三种状态。对已完整迁移的再次执行为无副作用
成功；部分迁移必须 fail closed，保留备份和诊断，并指向恢复步骤，不能继续套用 fresh-install 路径。

## 4. 运维与验收证据

中英文 Runbook 必须给出前置条件、只读检查、备份验证、显式执行、schema/值/ACL/worker/Consumer
复验、失败恢复及重复执行方法。生产执行记录只保存日期、版本或源码身份、匿名化环境说明、检查项和
结果；真实服务器地址、个人联系信息与 Secret 只存在于受控的本地或外部系统，不进入公开仓库。

真实迁移验收不能只检查脚本退出码或 schema 搜索结果。它必须比较迁移前后锚点集合，验证新对象与
class/ACL 状态、worker 新写入，并至少通过一个实际 IAM 或 LDAP Consumer 证明同一目录身份仍映射到
原应用账号。

## 5. 需求矩阵

本矩阵是规范来源，正文是解释。两者冲突以矩阵为准。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `ANCHOR-R-001` | ANAS 企业 OID 根必须固定为 IANA PEN `1.3.6.1.4.1.66678`，治理文档必须记录其 assignee 与 IANA 公开注册表 URL | 审阅 |
| `ANCHOR-R-002` | 每个 PEN 子 OID 必须先进入仓库 OID 注册表再被代码或 schema 使用；同一 OID 不得多义，退役 OID 不得删除、改义或复用 | 审阅 |
| `ANCHOR-R-003` | 新林以及迁移完成后的 `anasIdentityAnchor` attributeID 必须精确等于 `1.3.6.1.4.1.66678.1.2.1`，新林不得安装 legacy attributeID | 单元 + e2e |
| `ANCHOR-R-004` | 正式 `anasIdentityAnchor` schema 对象必须使用仓库登记且跨林一致的新 `schemaIDGUID`，该值必须不同于 legacy `7108c5a7-2290-45e0-9eba-eef087be58e3`，安装、ACL、测试与文档引用必须一致 | 单元 + e2e |
| `ANCHOR-R-005` | schema 安装必须保持既有名称、syntax、single-value、长度、索引与 `user`/`group` class 契约；重复安装必须无副作用，名称/OID/GUID/shape 冲突或 joined forest 缺少复制 schema 时必须停止 | 单元 + e2e |
| `ANCHOR-R-006` | 迁移工具默认必须只读，只有显式执行开关才能写入；首版只接受精确 legacy 状态的单 DC 林，多 DC、未知或冲突状态必须在写入前拒绝 | 契约 + e2e |
| `ANCHOR-R-007` | 第一次不可逆 schema 写入前必须确认可恢复且已验证的目录备份，并生成权限受限、足以逐项核对 schema 与全部既有锚点的导出 | e2e |
| `ANCHOR-R-008` | 迁移必须逐项保留全部既有用户和组的 `anasIdentityAnchor` 值，把正式属性接入 `user`/`group` class，并把 legacy schema 对象仅保留为不可复用的 defunct 历史 | e2e |
| `ANCHOR-R-009` | 迁移必须把 `svc_anchor` 的 printable-attribute 写入委派切换到新 `schemaIDGUID`、移除 legacy attribute-specific ACE，且不得扩大它对其他目录属性或对象的权限 | e2e |
| `ANCHOR-R-010` | 迁移后 anchor worker 必须能继续写入正式属性，IAM/LDAP Consumer 必须读到迁移前相同的身份值，并在目录账号改名后继续复用原应用身份而不重复建号 | e2e |
| `ANCHOR-R-011` | 迁移工具必须区分迁移前、已完成和部分迁移状态；完成态重跑必须无副作用，部分迁移必须 fail closed、保留诊断与恢复材料 | 单元 + e2e |
| `ANCHOR-R-012` | 中英文运维文档必须覆盖检查、备份、执行、复验、失败恢复、重跑、支持拓扑及 IANA 更新边界；公开文档、日志和执行证据不得包含真实 SSH 目标、私人联系信息或 Secret | 审阅 |
