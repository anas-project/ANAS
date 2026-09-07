---
doc_type: plan
status: proposed
created: 2026-09-04
updated: 2026-09-04
---

# 凭据轮换覆盖面实施计划

验收依据是[凭据轮换覆盖面与语义要求](../requirements/credential-rotation.md)的需求矩阵；设计
依据是[凭据库存与 deployment 驱动轮换设计](../../docs/architecture/credential-rotation.md)；
发现来源是[凭据轮换机制审查](../reviews/2026-09-04-credential-rotation-review.md)。

**当前状态：全部未开始。** 已实现的事务型轮换器不在本计划范围内——本计划只补它覆盖不到的部分。

排序原则是**先修表述、再补声明位、最后动认证基线**：前两者风险低且能立即减少误解，最后一项要
动运行中部署的认证方式，必须有 e2e 兜底。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：表述修正与差异记录 | R-008、R-009、R-013 | 未开始 |
| M1：资源凭据声明位与两侧契约 | R-001—R-005 | 未开始 |
| M2：compute 两条凭据按各自模式接入 | R-006、R-007 | 未开始 |
| M3：跨类清单与轮换时效 | R-010、R-011 | 未开始 |
| M4：PostgreSQL 认证基线处置 | R-012、R-014 | 未开始；需先定 (a)/(b) |

覆盖统计：14 项需求全部有且只有一个里程碑归属。

## 2. M0：表述修正（低风险，先做）

- 把 `internal/runner/credential_plan.go` 的阻断原因从 `"... is not reconcile"` 改为同时承认
  `overlap`；
- 修正[命令契约](../../docs/reference/contracts/commands.md)中 `--all` 只选 `reconcile` 的表述；
- 补写 `--module` 与 `--all` 对 manual 目标处置相反的说明；
- 在 PostgreSQL 相关文档写明其口令轮换当前不产生安全效果。

这一组不改行为，只消除误解，因此不需要 e2e。

## 3. M1：资源凭据的声明位与两侧契约

- `spec.credential` 增加 `rotation_mode`，复用既有四档的校验；
- 定义**两侧**生命周期：provider 侧 reconcile、consumer 侧 verify。这是与 `credentials.provides`
  单侧 owner 契约的实质差别，不能复用；
- 补齐“生成新值”的触发——今天 `secrets.Ensure` 见到已有值即返回；
- 在两侧契约可用之前，库存继续把资源凭据报告为 `unsupported`。

## 4. M2：compute 的两条凭据

同一资源内模式不同，是 M1 那条“不得一刀切”的第一个真实用例：

| 凭据 | 模式 | 验收要点 |
| --- | --- | --- |
| 客户端证书 | `overlap` | 轮换期间运行中实例不被杀掉 |
| `lease_secret` | `migrate` | 不进入 `--all` 批量；轮换需显式确认 |

## 5. M3：跨类清单

只读视图，合并五类展示，标注类别、轮换入口与上次轮换时间。**不合并执行路径**——本地管理员轮换
要改活动系统账号、配置参数轮换要走 plan/apply 守卫，塞进同一事务会让失败恢复无法推理。

## 6. M4：PostgreSQL 认证基线

先在 (a) 改口令认证基线 / (b) 标为 `unsupported` 之间定案并记录理由，再实施。选 (a) 时必须验证
既有部署仍能启动，且 provider 的资源账号创建路径不受影响。

## 7. CI 门禁

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go test ./...` | 待记录 |
| `npm run docs:check-requirements` | 待记录 |

## 8. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-006 | 待新增 `test-env/scripts/server-resource-credential-rotation-e2e.sh` | compute 租约 + 运行中实例 | — | 待执行 |
| R-010 | 待新增 `test-env/scripts/server-credential-inventory-e2e.sh` | 五类凭据齐备的部署 | — | 待执行 |
| R-012 | 待新增 `test-env/scripts/server-postgres-auth-baseline-e2e.sh` | 既有部署升级 | — | 待执行 |
| R-014 | 待新增 `test-env/scripts/server-postgres-auth-baseline-e2e.sh` | 改基线后的启动与资源账号创建 | — | 待执行 |

## 9. 待决

- PostgreSQL 认证基线选 (a) 还是 (b)；
- 两侧生命周期契约中 consumer 侧 verify 的调用时机（激活屏障之前还是之后）。
