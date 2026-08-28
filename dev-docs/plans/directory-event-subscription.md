---
doc_type: plan
status: implementing
created: 2026-08-27
updated: 2026-08-27
---

# Samba 目录事件订阅与实时同步实施计划

验收依据是[Samba 目录事件订阅与实时同步要求](../requirements/directory-event-subscription.md)的需求矩阵，
既有事件格式、生产者和游标设计见[Directory event journal](../../docs/architecture/directory-event-journal.md)。
当前里程碑是 M1：盘点并接入尚未订阅事件的 IAM 与 LDAP Module。

## 1. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：通用订阅契约与安全边界 | DIRSYNC-R-001—R-004、R-011 | 实施中；Authentik 与 Casdoor 已有实现，其余消费者待盘点 |
| M1：可靠消费与全量兜底 | DIRSYNC-R-006—R-010 | 实施中；既有 watcher 覆盖部分场景，统一缺口恢复尚未验收 |
| M2：全消费者实时同步 E2E | DIRSYNC-R-005、R-012 | 未开始 |

## 2. M0 检查表

- [ ] 从 Manifest/渲染结果生成 IAM Provider 与直接 LDAP/LDAPS 消费者清单，门禁漏接事件目录的 Module。
- [ ] 为每个消费者只读投影 `ANAS_DIRECTORY_EVENTS_DIR` 与事件文件名，并使用消费者独立游标。
- [ ] 为尚未接入的 IAM Provider 和 LDAP Module 实现事件过滤与刷新/缓存失效入口。
- [ ] 验证 Samba 生产者不持有任何消费者 API Secret，事件中没有属性值或 Secret。

## 3. M1 检查表

- [ ] 为各消费者声明防抖、最小调用间隔、最大传播时间和周期全量 reconcile 周期。
- [ ] 统一验证成功后提交游标、失败重试、轮换、重启、不完整尾记录和重复事件处理。
- [ ] 增加游标落后于日志保留窗口的检测，并以全量 reconcile 修复缺口。
- [ ] 对不保存影子目录的 LDAP Module 明确缓存失效或受影响对象重读行为。

## 4. M2 检查表

- [ ] 为每个 IAM Provider 执行新增、修改、删除、停用、直接/递归组、突发、重启和缺口恢复 E2E。
- [ ] 为每个直接 LDAP/LDAPS Module 执行与其功能相关的同类 E2E，并断言最终认证、授权或目录视图。
- [ ] 把最大传播时间、全量兜底周期、测试入口和结果同步到各 Module 技术文档。
- [ ] 运行 `npm run docs:check-requirements` 与 `npm run docs:check-requirement-status`。

## 5. 当前阻塞

没有外部阻塞。下一步先以 Manifest 和最终 render 环境建立完整消费者清单，再按清单补实现与 E2E。

## 6. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-001 | 待新增 `test-env/scripts/server-directory-event-iam-matrix-e2e.sh` | Samba AD + 每个 IAM Provider | — | 待实现 |
| R-005 | 待新增 `test-env/scripts/server-directory-event-consumer-matrix-e2e.sh` | Samba AD + 每个 IAM/LDAP 消费者 | — | 待实现 |
| R-006 | `server-directory-event-consumer-matrix-e2e.sh burst` | 事件突发与消费者防抖配置 | — | 待实现 |
| R-009 | `server-directory-event-consumer-matrix-e2e.sh restart-rotation` | 日志轮换、不完整尾记录与消费者重启 | — | 待实现 |
| R-010 | `server-directory-event-consumer-matrix-e2e.sh retention-gap` | 游标落后保留窗口 + 全量 reconcile | — | 待实现 |
| R-012 | `server-directory-event-consumer-matrix-e2e.sh full` | 全部 IAM Provider 与直接 LDAP/LDAPS Module | — | 待实现 |
