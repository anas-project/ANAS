---
doc_type: plan
status: implementing
created: 2026-09-04
updated: 2026-10-03
---

# 凭据轮换覆盖面实施计划

验收依据是[凭据轮换覆盖面与语义要求](../requirements/credential-rotation.md)的需求矩阵；设计
依据是[凭据库存与 deployment 驱动轮换设计](../../docs/architecture/credential-rotation.md)；
发现来源是[凭据轮换机制审查](../reviews/2026-09-04-credential-rotation-review.md)。

**当前状态：M4 的 PostgreSQL 认证前置子项已实施，整体仍实施中；其余里程碑未开始。**
已实现的事务型轮换器不在本计划范围内——本计划只补它覆盖不到的部分。

排序原则是**先修表述、再补声明位、最后动认证基线**：前两者风险低且能立即减少误解，最后一项要
动运行中部署的认证方式，必须有 e2e 兜底。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：表述修正与差异记录 | R-008、R-009、R-013 | 未开始 |
| M1：资源凭据声明位与两侧契约 | R-001—R-005、R-016—R-018 | 未开始 |
| M2：compute 客户端证书接入 + `lease_secret` 专属命令 | R-006、R-007、R-015 | 未开始 |
| M3：跨类清单与轮换时效 | R-010、R-011 | 未开始 |
| M4：PostgreSQL 认证基线修正 | R-012、R-014 | 实施中；认证子项已通过隔离 Docker，通用轮换库存与真实主机升级待验收 |

覆盖统计：18 项需求全部有且只有一个里程碑归属。

## 2. M0：表述修正（低风险，先做）

- 把 `internal/runner/credential_plan.go` 的阻断原因从 `"... is not reconcile"` 改为同时承认
  `overlap`；
- 修正[命令契约](../../docs/reference/contracts/commands.md)中 `--all` 只选 `reconcile` 的表述；
- 补写 `--module` 与 `--all` 对 manual 目标处置相反的说明；
- 在 PostgreSQL 相关文档区分本次 SCRAM 认证前置修复与尚未交付的统一口令轮换。

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

2026-10-03 协同说明：[relational_database 扩展计划](archived/relational-database-extensions.md)依赖本节的安全认证
基线。新装/既有 HBA 修复可提前作为独立子项交付，不必等待 compute 或跨类库存工作全部完成；资源口令
轮换仍由本主题验收。`CRED-R-012`、`CRED-R-014` 的归属不变，认证子项不代表完整轮换已交付。

已定走 (a)：`postgres.password` 由 ANAS 生成而非用户指定，因此必须与其他生成凭据同标准——可单独
轮换、也可随 `--all` 轮换。这使修正 `trust` 基线成为**前置条件**而不是备选项。必须验证改基线后
既有部署仍能启动，且 provider 的资源账号创建路径不受影响。

2026-10-03 落地子项：`modules/postgres/postgres/entrypoint.sh` 在正式 TCP 监听前完成新装/既有目录的
SCRAM HBA 和管理员口令投影；Provider ensure 为普通 Resource 角色写 SCRAM 并使用实际口令连接验证。
普通应用不得具有数据库特权或任何已授予角色成员关系。管理员凭据仍为 Provider 私有，Secret 键不变。
`postgres.password` 接入跨类库存与完整轮换事务没有在本任务扩张实现，仍待本主题后续验收。

隔离 Linux arm64 Docker 已验证新装、旧 trust 数据目录替换镜像后启动、正常 Resource 建库、错误/空口令、
冒用管理员、跨库拒绝、只读 inspect 不改口令及重启。复用脚本
`modules/postgres/tests/container-e2e.sh --local-task-fixture`；真实服务器入口为
`test-env/scripts/server-relational-database-extensions-e2e.sh`，强制现有隔离 Docker daemon guard。
工作区未找到可定位的专用测试服务器 target，真实 ANAS apply 和一致恢复仍待验收，不记录为服务器全绿。

## 7. CI 门禁

本轮 PG Hook 定向单测、脚本语法检查及隔离 Docker 认证/扩展测试通过；未 git 提交，未跑真实主机门禁。
已实现的事务型轮换器不在本计划范围内，它的门禁记录也不算本计划的证据。

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
| R-014 | `modules/postgres/tests/container-e2e.sh --local-task-fixture`；服务器包装 `test-env/scripts/server-relational-database-extensions-e2e.sh` | 隔离 Docker Linux arm64，PG18.4/Alpine，旧 trust→SCRAM及Resource账号创建 | 2026-10-03 | 本地及finance原生amd64真实trust/MD5→SCRAM通过；ANAS新装/重复apply/重启通过 |

## 9. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [凭据轮换覆盖面与语义要求](../requirements/credential-rotation.md)、[凭据库存与 deployment 驱动轮换设计](../../docs/architecture/credential-rotation.md) | `lease_secret` 不走凭据轮换通道、改用专属命令（`CRED-R-007`、`CRED-R-015`），去掉旧的 `migrate` 答案 | 已完成（`6823232`） |
| [Incus 宿主供给与镜像烘焙](../../docs/architecture/incus-host-provisioning.md) §5.1.3.2 | 表中 `lease_secret` 仍写 `migrate`，与 `CRED-R-007` 及同文后面「专属命令，不走凭据轮换通道」的结论矛盾 | 未开始 |
| [部署与配置命令 JSON 契约](../../docs/reference/contracts/commands.md)与英文镜像的 `credential` 一节 | M0：改正「`--all` 只选 `reconcile` 目标」，写明 `--module` 与 `--all` 对 manual 目标的处置相反；M3：跨类清单落地后改写 `list` 的接入范围说明 | 未开始 |
| `postgres` 的 README 与技术文档（中英文） | M4：说明 SCRAM 新装/既有认证迁移、普通角色真实检查和完整轮换未交付边界 | 已完成（本轮工作树，未提交） |
| `docs/developer/module-development.md` 的 `resources.requires` 示例与 Contract 技术文档（`contracts/*/docs/technical*.md`） | M1：`spec.credential` 增加 `rotation_mode` 与两侧生命周期 | 未开始 |
| `contracts/compute` 的 README 与技术文档（中英文） | M2：客户端证书 `overlap` 与 `lease_secret` 专属命令的轮换语义 | 未开始 |
| [英文架构索引](../../docs/en/architecture/index.md) | 按文档标准 §2 至少补凭据轮换设计的英文摘要；目前没有条目 | 未开始 |

## 10. 待决

- 两侧生命周期契约中 consumer 侧 verify 的调用时机（激活屏障之前还是之后）。

2026-10-03 补充原生主机证据：finance专用daemon native-pg-r4完成真实trust HBA、三角色旧MD5
到全SCRAM迁移和普通Resource登录；ANAS新装、重复apply、重启与Immich/Authentik实际共享
消费者也通过。证据见[实施核对](../../modules/immich/dev-docs/plans/immich-module.md#执行记录归并)。
仅交付本任务所需PG认证前置子项；完整跨类凭据轮换、消费者接线与本计划其他里程碑不因此完成。
