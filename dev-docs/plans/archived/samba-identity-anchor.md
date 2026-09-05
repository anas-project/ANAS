---
doc_type: plan
status: done
created: 2026-09-03
updated: 2026-09-03
---

# Samba 身份锚点 OID 与既有目录迁移实施计划

验收依据是
[Samba 身份锚点 OID 与既有目录迁移要求](../../requirements/samba-identity-anchor.md)的需求矩阵；
schema、worker 与 Consumer 设计见
[Samba AD identity anchor](../../../docs/architecture/samba-identity-anchor.md)。M0—M2 已完成：正式 PEN
namespace、新安装 schema、受保护的单 DC 迁移路径和用户明确指定的既有部署迁移均已落地。公开计划
不记录真实服务器地址、私人联系信息、DN 清单或 Secret。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：PEN 注册表与新安装 schema | ANCHOR-R-001—R-005 | 已完成；正式 PEN、内部注册表、代码、架构与 fresh-install E2E 已同步 |
| M1：单 DC 迁移、备份与 ACL | ANCHOR-R-006—R-009、R-011 | 已完成；离线迁移、冷恢复点、证据、完成态/部分态和真实最小权限探针已通过 |
| M2：Consumer 连续性、Runbook 与既有部署迁移 | ANCHOR-R-010、R-012 | 已完成；中英文 Runbook、worker、实际 LDAP Consumer 与改名连续性已验证 |

## 2. M0：PEN 注册表与新安装 schema

验收入口：要求文档 §1、§2。

- [x] 把 PEN `66678`、内部 arc、正式 attributeID、legacy OID/GUID 与状态写入中英文治理和架构文档。
- [x] 为正式 schema 对象生成一次性稳定 `schemaIDGUID`，登记后同步安装脚本、`structure.sh` ACL 与测试。
- [x] 切换 fresh-install 常量，保留名称、syntax、索引、class `mayContain` 和 joined-forest 检查。
- [x] 增加 fresh、重复运行、名称/OID/GUID/shape 冲突和 joined forest 缺失 schema 的自动验证。

## 3. M1：单 DC 迁移、备份与 ACL

验收入口：要求文档 §3。

- [x] 实现默认只读检查和显式执行模式，核对单 DC、schema master、精确 legacy 标识与当前迁移状态。
- [x] 在不可逆写入前验证恢复点，并以权限受限文件导出 schema 与所有受影响 DN/anchor 映射。
- [x] 在隔离 legacy 林验证值导出、legacy defunct、正式 schema 创建、class link、值恢复及逐项核对。
- [x] 把 `svc_anchor` 委派切换到新 `schemaIDGUID` 并移除旧 ACE，验证 worker 仍只能写两种锚点属性。
- [x] 覆盖完成态重跑、中断 marker 和无 marker 部分状态拒绝；失败保留恢复材料并停止自动推进。

## 4. M2：Consumer 连续性、Runbook 与既有部署迁移

验收入口：要求文档 §4。

- [x] 新增中英文 Runbook，给出 `--check`、备份验证、显式执行、复验、恢复和重跑步骤。
- [x] 复验 anchor worker 事件写入与补漏扫描，并通过实际 Consumer 验证改名前后复用同一应用身份。
- [x] 在用户明确指定的既有单 DC 部署执行只读检查；备份、源状态和恢复路径通过后才执行迁移。
- [x] 比较迁移前后锚点集合，验证 schema、class、ACL、worker、目录健康和实际 Consumer，并记录匿名化结果。
- [x] 同步 Samba Module 中英文 README/技术文档、运维索引和架构索引；真实目标与敏感输出不进入仓库。

## 5. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-003—R-005 | `server-anchor-schema-migration-e2e.sh` + hook 单元测试 | Samba 4.23.6 fresh/join fixture | 2026-09-03 | **通过**：正式 OID/GUID/shape、无 legacy、新装幂等、join 缺 schema 拒绝及冲突守卫通过 |
| R-006—R-008 | `server-anchor-schema-migration-e2e.sh` + 既有部署验收 | 精确 legacy 单 DC；用户/组及既有锚点 | 2026-09-03 | **通过**：默认只读、显式执行、冷恢复点、0700 外部证据、defunct replacement、class link 和逐值恢复通过 |
| R-009 | `structure.sh` 单元断言 + 真实 `svc_anchor` 正反写入探针 | 用户明确指定的既有单 DC 部署 | 2026-09-03 | **通过**：新 GUID ACE 存在、legacy ACE 移除；两种锚点可写且无关属性不可写 |
| R-010 | worker 事件/补漏探针 + 实际 Nextcloud LDAP Consumer 改名复验 | 用户明确指定的既有单 DC 部署 | 2026-09-03 | **通过**：事件和补漏均写入；改名前后 anchor 与应用身份不变、未重复建号 |
| R-011 | `server-anchor-schema-migration-e2e.sh` | 完成态、中断 marker 和无 marker 部分状态 | 2026-09-03 | **通过**：完成态 check/execute 无副作用；两类 marker 与缺 class link 状态均 fail closed |
| R-012 | 中英文治理、架构、Runbook、README 与技术文档审阅 | 仓库文档门禁 | 2026-09-03 | **通过**：操作、恢复、IANA 边界及敏感信息约束均已同步 |

远端执行证据只记录源码身份、匿名化环境、日期、检查项和结果。SSH endpoint、凭据、完整私有 DN
清单和原始导出保存在权限受限的外部位置，不写入本表。

既有部署执行时，在全 workspace 停写后创建并验证、固定冷快照，再执行离线迁移。迁移前后共 15 个
锚点按 DN/值逐项及整体摘要完全一致；正式 schema、defunct legacy 历史、User/Group class link、
目录健康、ACL、worker 与 Nextcloud 连续性均通过。快照与 0700 外部证据继续按观察期保留。

## 6. 当前阻塞

无。后续只需按发布流程构建并发布 r11；既有部署已完成现场修复，不依赖公开镜像发布才能继续运行。

## 7. 验证命令

```bash
bash -n modules/samba_dc/samba_dc/root/usr/local/bin/install-identity-schema.sh
bash -n modules/samba_dc/samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh
bash -n test-env/scripts/server-anchor-e2e.sh
bash -n test-env/scripts/server-anchor-schema-migration-e2e.sh
go test ./modules/samba_dc/hook
python3 -m unittest discover -s modules/samba_dc/anchor_worker -p 'test_*.py'
go run ./cmd/gen-module-docs --check
npm run docs:requirement-status
npm run docs:plan-status
npm run docs:check-requirements
npm run docs:check-requirement-status
npm run docs:check-plan-status
npm run docs:check-status
npm run docs:build
git diff --check
```

真实迁移 E2E 只在隔离 legacy fixture 或用户明确指定、通过 M1 全部前置条件的单 DC 部署执行。
稳定结论已经同步到治理、架构和运维文档，本计划作为已完成交付记录归档。
