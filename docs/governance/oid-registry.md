# ANAS OID 注册表

本文件是 ANAS OID 分配的项目内注册表。OID 一经分配，其数字、名称和语义即为永久
记录；退役表示停止用于新对象，不表示可以重新分配。

## 命名空间

| OID | 名称 | 用途 | 状态 |
| --- | --- | --- | --- |
| `1.3.6.1.4.1.66678` | ANAS PEN | IANA 分配的企业根 | active |
| `1.3.6.1.4.1.66678.1` | `directorySchema` | LDAP / Active Directory schema 分支 | namespace |
| `1.3.6.1.4.1.66678.1.1` | `schemaClasses` | schema class 分配分支 | namespace |
| `1.3.6.1.4.1.66678.1.2` | `schemaAttributes` | schema attribute 分配分支 | namespace |
| `1.3.6.1.4.1.66678.1.2.1` | `anasIdentityAnchor` | 文本身份锚点 AD schema attribute | active |
| `1.3.6.1.4.1.66678.1.1.1` | `anasMailbox` | Non-login mailbox resource owned by an existing personnel anchor | allocated |
| `1.3.6.1.4.1.66678.1.1.2` | `anasMailAddressClaim` | Unique managed SMTP address claim referring to one mailbox | allocated |
| `1.3.6.1.4.1.66678.1.2.2` | `anasMailboxID` | Stable mailbox UUID, separate from personnel identity | allocated |
| `1.3.6.1.4.1.66678.1.2.3` | `anasMailOwnerAnchor` | Reference to the owning personnel anasIdentityAnchor | allocated |
| `1.3.6.1.4.1.66678.1.2.4` | `anasMailboxKind` | Mailbox resource kind: primary or additional | allocated |
| `1.3.6.1.4.1.66678.1.2.5` | `anasMailDefaultAddress` | Normalized default SMTP address of one mailbox | allocated |
| `1.3.6.1.4.1.66678.1.2.6` | `anasMailAddress` | Normalized complete SMTP address held by one claim | allocated |
| `1.3.6.1.4.1.66678.1.2.7` | `anasMailAddressKind` | Claim kind: primary, additional default or alias | allocated |
| `1.3.6.1.4.1.66678.1.2.8` | `anasMailState` | Mail resource or address lifecycle state | allocated |
| `1.3.6.1.4.1.66678.1.2.9` | `anasMailConfigVersion` | Opaque UUID changed by each conditional configuration update | allocated |
| `1.3.6.1.4.1.66678.1.2.10` | `anasMailOperationID` | Stable UUID of the current resumable mail operation | allocated |
| `1.3.6.1.4.1.66678.1.2.11` | `anasMailOperationState` | Durable progress of the current mail operation | allocated |
| `1.3.6.1.4.1.66678.1.2.12` | `anasMailOperationData` | Non-secret JSON intent for the current mail operation | allocated |
| `1.3.6.1.4.1.66678.1.2.13` | `anasMailOwnershipHistory` | Non-secret ownership transition records keyed by operation UUID | allocated |
| `1.3.6.1.4.1.66678.1.2.14` | `anasMailQuotaState` | Primary mailbox JSON quota configuration and pending reservations | allocated |
| `1.3.6.1.4.1.66678.2` | `managementAndTelemetry` | 管理与遥测对象分配分支 | reserved namespace |
| `1.3.6.1.4.1.66678.3` | `protocolIdentifiers` | 协议标识分配分支 | reserved namespace |

邮件目录分配对应 `modules/samba_dc/samba_dc/root/usr/local/share/anas/mail-schema.json`。`allocated` 表示已永久分配并进入实现，不表示现有业务域已安装；既有人员 `anasIdentityAnchor` 不复制为邮箱资源身份。固定 schema GUID 同时记录在该定义文件中。

## 历史分配

| OID | 名称 | 用途 | 状态 |
| --- | --- | --- | --- |
| `1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401` | `legacyDirectorySchemaRoot` | PEN 批准前的 AD schema 根 | retired namespace; never allocate descendants |
| `1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1` | `anasIdentityAnchor` legacy schema object | PEN 批准前部署的文本身份锚点属性 | retired; never reuse |

历史 OID 位于 ANAS PEN 之外，但仍保留在本表中，以阻止遗漏、误认和重新使用。
旧根及其所有后代均已退役；不再在该根下分配新 OID。

## 分配规则

1. 在实现或发布新对象之前，先在本表中分配具体 OID，并记录稳定名称、唯一用途与状态。
2. 只能在所属的 namespace 下顺序分配；不要根据代码版本、主机或环境动态生成 OID。
3. 已发布分配不得改号或改义。替代对象取得新 OID，旧分配改为 `retired` 并永久保留。
4. 内部子分配不提交给 IANA；只有 PEN 登记资料变更才使用 IANA 修改流程。

IANA 登记事实与规则依据见
[IANA Private Enterprise Number 与 OID 管理](iana-pen-application.md)。
