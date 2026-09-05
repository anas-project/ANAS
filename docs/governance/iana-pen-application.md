# IANA Private Enterprise Number 与 OID 管理

> 状态：**已分配**。IANA 已批准 PEN `66678`，ANAS 的企业 OID 根为
> `1.3.6.1.4.1.66678`。

## IANA 登记记录

| 项目 | 值 |
| --- | --- |
| PEN（十进制） | `66678` |
| 企业 OID 根 | `1.3.6.1.4.1.66678` |
| 登记持有人（assignee） | Wang Hailong |
| 官方注册表 | [IANA Private Enterprise Numbers：66678 所在页](https://www.iana.org/assignments/enterprise-numbers/?page=667) |

仓库只记录管理 OID 所需的公开事实，不复制登记联系人的邮箱、邮寄地址或电话。
若 IANA 联系资料需要变更，由登记持有人使用官方修改流程处理；不要通过提交仓库文件
来变更 IANA 记录。

## 项目内部分配

ANAS 管理 `1.3.6.1.4.1.66678` 以下的命名空间：

```text
1.3.6.1.4.1.66678.1     LDAP/Active Directory schema
1.3.6.1.4.1.66678.1.1   schema classes
1.3.6.1.4.1.66678.1.2   schema attributes
1.3.6.1.4.1.66678.2     management and telemetry
1.3.6.1.4.1.66678.3     protocol identifiers
```

`anasIdentityAnchor` 的正式 `attributeID` 已分配为
`1.3.6.1.4.1.66678.1.2.1`。全部具体分配和历史 OID 均记录在
[ANAS OID 注册表](oid-registry.md)中。代码使用一个新 OID 之前必须先登记；已分配或
已退役的 OID 永远不得改变含义或重新使用。

## 是否需要继续向 IANA 登记

不需要为 `1.3.6.1.4.1.66678.1.2.1` 或其他内部子分配再次申请。
[RFC 9371 §2.1](https://www.rfc-editor.org/rfc/rfc9371.html#section-2.1)
说明取得一个 PEN 后进行内部分配通常更合适，而且子分配不应报告给 IANA。

仅在登记持有人、公开联系人或其他 PEN 登记资料需要变更时，才使用
[IANA PEN 修改表](https://www.iana.org/assignments/enterprise-numbers/assignment/modify/)；
项目内新增、保留或退役子 OID 只更新本仓库的注册表。

## AD schema 决定与旧部署

早期 ANAS 部署中的 `anasIdentityAnchor` 使用 GUID 派生 OID：

```text
1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1
```

该 OID 现为 **legacy / retired**，只用于识别和迁移旧 schema 对象，绝不复用。
AD schema 对象的 `attributeID` 不在原对象上就地改写；旧部署通过受控的
**defunct replacement** 迁移：保留并停用旧对象，为其改用 legacy 名称，再以正式
OID 创建同名 `anasIdentityAnchor`，恢复预先导出的值并验证消费者。完整约束见
[Samba AD identity anchor](../architecture/samba-identity-anchor.md#legacy-oid-migration)。

仓库脚本位于
`modules/samba_dc/samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh`，并安装到
Samba DC 镜像的 `/usr/local/bin/`。它只支持单 DC 林，必须由 root 在 Samba 及该
数据卷的其他写入者全部停止后运行。
先执行只读检查；创建并验证 ANAS 快照后，才把真实快照 ID 交给写入模式：

```sh
/usr/local/bin/migrate-identity-anchor-oid.sh --check
/usr/local/bin/migrate-identity-anchor-oid.sh \
  --execute \
  --snapshot-id <verified-snapshot-id> \
  --backup-dir /mnt/anas-migration-evidence/<new-dir>
```

`--backup-dir` 是必填项，必须是挂载在 Samba 数据卷之外的受保护持久存储上
尚不存在的绝对路径。脚本拒绝默认到 `/var/lib/samba` 或覆盖既有目录，因为整卷
快照恢复会同时擦除放在 Samba 卷内的迁移证据。

完成后，在数据卷仍离线时再执行一次 `--check`。它会验证 final/legacy schema、
User 与 Group class link，以及全部文本锚点与 `mS-DS-ConsistencyGuid` 的一致性和
唯一性。然后先单独启动升级后的 DC，让目录结构对账器替换新 schema GUID
的写入 ACE；核验 `svc_anchor` 实际写权限后，再启动 Anchor Worker 和其他消费者。
完整的停机、快照、外部证据、分阶段启动和整卷恢复步骤见
[PEN 66678 identity-anchor 迁移 Runbook](../guide/migrate-identity-anchor-oid.md)。
