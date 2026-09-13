---
doc_type: plan
status: proposed
created: 2026-09-04
updated: 2026-09-13
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
| M1：资源凭据声明位与两侧契约 | R-001—R-005、R-016—R-018 | 未开始 |
| M2：compute 客户端证书接入 + `lease_secret` 专属命令 | R-006、R-007、R-015 | 未开始 |
| M3：跨类清单与轮换时效 | R-010、R-011 | 未开始 |
| M4：PostgreSQL 认证基线修正 | R-012、R-014 | 未开始；已定走 (a) |

覆盖统计：18 项需求全部有且只有一个里程碑归属。

## 2. M0：表述修正（低风险，先做）

- 把 `internal/runner/credential_plan.go` 的阻断原因从 `"... is not reconcile"` 改为同时承认
  `overlap`；
- 修正[命令契约](../../docs/reference/contracts/commands.md)中 `--all` 只选 `reconcile` 的表述；
- 补写 `--module` 与 `--all` 对 manual 目标处置相反的说明；
- 在 PostgreSQL 相关文档写明其口令轮换当前不产生安全效果。

这一组不改行为，只消除误解，因此不需要 e2e。

## 3. M1：资源凭据的声明位与两侧契约

- `spec.credential` 增加 `rotation_mode`，复用既有四档的校验；
- 定义**两侧**生命周期。provider 侧 reconcile 之后必须用新凭据**实际登录一次**——`ALTER ROLE`
  没报错只证明 SQL 执行了。consumer 侧默认取「以新投影重新激活后 healthcheck 通过」，不强制每个
  消费者写 verify 处理器；重新激活在提交之前发生，证据来得及用；
- 补齐“生成新值”的触发——今天 `secrets.Ensure` 见到已有值即返回；
- 在两侧契约可用之前，库存继续把资源凭据报告为 `unsupported`。

## 4. M2：compute 的两条凭据

同一资源内模式不同，是 M1 那条“不得一刀切”的第一个真实用例：

| 凭据 | 模式 | 验收要点 |
| --- | --- | --- |
| 客户端证书 | `overlap` | 轮换期间运行中实例不被杀掉 |
| `lease_secret` | **专属命令** | 它不是凭据——轮换改变的是已发布 URL 而非认证材料。不进凭据通道、不被 `--all` 选中 |

## 5. M3：跨类清单

只读视图，合并五类展示，标注类别、轮换入口与上次轮换时间。**不合并执行路径**——本地管理员轮换
要改活动系统账号、配置参数轮换要走 plan/apply 守卫，塞进同一事务会让失败恢复无法推理。

## 6. M4：PostgreSQL 认证基线

已定走 (a)：`postgres.password` 由 ANAS 生成而非用户指定，因此必须与其他生成凭据同标准——可单独
轮换、也可随 `--all` 轮换。这使修正 `trust` 基线成为**前置条件**而不是备选项。必须验证改基线后
既有部署仍能启动，且 provider 的资源账号创建路径不受影响。

## 7. CI 门禁

全部里程碑未开始，没有实施提交，`go test ./...` 等代码门禁无从记录。已实现的事务型轮换器不在本计划
范围内，它的门禁记录也不算本计划的证据。

文档门禁也**没有 CI 记录**：本计划与要求文档随 `a4c4d0c`（2026-09-07）加入，而 GitHub CI 在 master
上最近一次运行是 `5306b63`（2026-09-05），其后的提交均未推送。只有本地记录：`6823232` 的提交说明记载
`docs:check-requirements`、`docs:check-requirement-status`、`docs:check-plan-status` 与
`docs:check-status` 通过，2026-09-13 在该提交上复核一致。

## 8. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-006 | 待新增 `test-env/scripts/server-resource-credential-rotation-e2e.sh` | compute 租约 + 运行中实例 | — | 待执行 |
| R-010 | 待新增 `test-env/scripts/server-credential-inventory-e2e.sh` | 五类凭据齐备的部署 | — | 待执行 |
| R-012 | 待新增 `test-env/scripts/server-postgres-auth-baseline-e2e.sh` | 既有部署升级 | — | 待执行 |
| R-016 | 待新增 `test-env/scripts/server-resource-credential-rotation-e2e.sh` | 登录测试挡住「ALTER 成功但连不上」 | — | 待执行 |
| R-018 | 待新增 `test-env/scripts/server-resource-credential-rotation-e2e.sh` | 消费者未拿到新值时 healthcheck 失败并回滚 | — | 待执行 |
| R-014 | 待新增 `test-env/scripts/server-postgres-auth-baseline-e2e.sh` | 改基线后的启动与资源账号创建 | — | 待执行 |

## 9. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [凭据轮换覆盖面与语义要求](../requirements/credential-rotation.md)、[凭据库存与 deployment 驱动轮换设计](../../docs/architecture/credential-rotation.md) | `lease_secret` 不走凭据轮换通道、改用专属命令（`CRED-R-007`、`CRED-R-015`），去掉旧的 `migrate` 答案 | 已完成（`6823232`） |
| [Incus 宿主供给与镜像烘焙](../../docs/architecture/incus-host-provisioning.md) §5.1.3.2 | 表中 `lease_secret` 仍写 `migrate`，与 `CRED-R-007` 及同文后面「专属命令，不走凭据轮换通道」的结论矛盾 | 未开始 |
| [部署与配置命令 JSON 契约](../../docs/reference/contracts/commands.md)与英文镜像的 `credential` 一节 | M0：改正「`--all` 只选 `reconcile` 目标」，写明 `--module` 与 `--all` 对 manual 目标的处置相反；M3：跨类清单落地后改写 `list` 的接入范围说明 | 未开始 |
| `postgres` 的 README 与技术文档（中英文） | M0：写明其口令轮换当前不产生安全效果；M4 修正认证基线后改写 | 未开始 |
| `docs/developer/module-development.md` 的 `resources.requires` 示例与 Contract 技术文档（`contracts/*/docs/technical*.md`） | M1：`spec.credential` 增加 `rotation_mode` 与两侧生命周期 | 未开始 |
| `contracts/compute` 的 README 与技术文档（中英文） | M2：客户端证书 `overlap` 与 `lease_secret` 专属命令的轮换语义 | 未开始 |
| [英文架构索引](../../docs/en/architecture/index.md) | 按文档标准 §2 至少补凭据轮换设计的英文摘要；目前没有条目 | 未开始 |

## 10. 待决

- 两侧生命周期契约中 consumer 侧 verify 的调用时机（激活屏障之前还是之后）。
