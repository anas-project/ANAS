---
doc_type: plan
status: partial
created: 2026-09-30
updated: 2026-09-30
---

# Nextcloud 回收站配置测试实施计划

验收依据：[回收站配置要求](../requirements/nextcloud-trashbin.md)。本主题没有独立架构文档；
实现入口与边界见[技术文档](../../docs/technical.md)。

## 里程碑与归属

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：默认值、occ 类型及失败路径 | R-002 | 已完成 |
| M1：真实运行配置、WebDAV 行为与测试清理 | R-001、R-003—R-009 | 实施中 |

## 实施检查表

- [x] Hook 测试验证默认值、合法覆盖及非法布尔值拒绝。
- [x] 执行真实启动脚本配置块，捕获 occ 参数并注入两条写入失败。
- [x] 新增专用服务端 E2E 脚本，接入 domain-separation 的 full 测试级别。
- [x] 编写 E2E 判据反例：认证失败、跨用户路径、清理故障和密码参数泄漏。
- [x] 新增机器用例、双向 TEST_CASES 标记和生成用例目录。
- [ ] 在固定 Nextcloud 34.0.2 的专用测试部署执行并保存真实报告。

## e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-001 | `server-nextcloud-trashbin-e2e.sh` | 待指定专用隔离部署 | — | 未执行：尚无可用的本轮测试部署 |
| R-003 | `server-nextcloud-trashbin-e2e.sh` | 同上 | — | 未执行 |
| R-004 | `server-nextcloud-trashbin-e2e.sh` | 同上 | — | 未执行 |
| R-005 | `server-nextcloud-trashbin-e2e.sh` | 同上 | — | 未执行 |
| R-006 | `server-nextcloud-trashbin-e2e.sh` | 同上 | — | 未执行；合成时间戳只验证运行时服务，不代表 cron 长期验收 |
| R-007 | `server-nextcloud-trashbin-e2e.sh` | 同上 | — | 未执行 |
| R-008 | `server-nextcloud-trashbin-e2e.sh` | 同上 | — | 未执行 |
| R-009 | `server-nextcloud-trashbin-e2e.sh` | 同上 | — | 未执行 |

## 验证入口与文档同步

`test-env/cases/nextcloud-trashbin/cases.yml` 为用例来源；README 由生成器生成。
模块 README、双语技术文档和 `test-env/README.md` 同步记录执行入口。
本地执行结果只证明 Hook、测试判据和文档契约，不替代上述真实运行验收。

## 当前阻塞

未指定本轮可用的专用非生产服务器，当前本地 Docker daemon 不可用；不复用历史测试服务器。
