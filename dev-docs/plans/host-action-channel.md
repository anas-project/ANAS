---
doc_type: plan
status: proposed
created: 2026-09-04
updated: 2026-09-13
---

# 宿主特权动作通道实施计划

验收依据是[宿主特权动作通道要求](../requirements/host-action-channel.md)；设计见
[同名架构文档](../../docs/architecture/host-action-channel.md)。

**当前状态：全部未开始，设计已定。** 它依赖[统一动作 ABI](action-abi.md) 先落地——通道复用那套
线格式与 job 语义，不另起一套。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：通道形态、授权与审计 | R-001—R-004 | 未开始 |
| M1：入口划分与升级绑定 | R-005、R-006 | 未开始 |
| M2：长时动作与非 systemd 可移植性 | R-007、R-008 | 未开始 |
| M3：二段确认 | R-009—R-011 | 未开始 |
| M4：动作清单治理 | R-012、R-013 | 未开始 |

覆盖统计：13 项需求全部有且只有一个里程碑归属。

## 2. 顺序理由

M0 先于其余，因为「只接受动作 id 与类型化参数」这条不变量一旦在实现里被破坏，后面每个里程碑都
建立在一个可以被塞进脚本的通道上。M3 依赖 ABI 的 job 记录（`plan` 与 `apply` 是两个互相引用的
job），因此排在 M2 之后。

## 3. CI 门禁

全部里程碑未开始，没有实施提交，`go test ./...` 等代码门禁无从记录；它依赖的[统一动作 ABI](action-abi.md)
也尚未开工。

文档门禁也**没有 CI 记录**：本计划与要求文档随 `40a8b2e`（2026-09-09）加入，而 GitHub CI 在 master
上最近一次运行是 `5306b63`（2026-09-05），其后的提交均未推送。只有本地记录：`6823232` 的提交说明记载
`docs:check-requirements`、`docs:check-requirement-status`、`docs:check-plan-status` 与
`docs:check-status` 通过，2026-09-13 在该提交上复核一致。

## 4. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-003 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | 安装与卸载对称性 | — | 待执行 |
| R-004 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | CLI 与 Web 同一通道同一审计 | — | 待执行 |
| R-011 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | token 过期后重新展示而非沿用旧摘要 | — | 待执行 |

## 5. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [Incus compute Provider Module 集成要求](../requirements/incus-module.md) | 迁出的 `INCUS-R-058` 等标为由 `HOSTACT-R-*` 取代 | 已完成 |
| [特权操作与 helper（草案）](../../docs/architecture/privilege-helper-draft.md) | §3 按本通道重新划分：btrfs 与备份的特权操作改归 `anas-hostd` | 已完成 |
| [宿主特权动作通道设计](../../docs/architecture/host-action-channel.md)与[架构索引](../../docs/architecture/index.md) | 状态「设计，未实现」随里程碑落地改写 | 未开始 |
| [英文架构索引](../../docs/en/architecture/index.md) | 按文档标准 §2 至少补本设计的英文摘要；目前没有条目 | 未开始 |
| [日志与可观测性](../../docs/architecture/observability-and-logs.md) | 「特权动作审计」一行标注未实现，M0 落地后更新 | 未开始 |
| [安装](../../docs/getting-started/installation.md)与英文镜像 | M0/M1：安装期那一次授权，以及 `anas-hostd` 与 socket 的安装、卸载（R-003） | 未开始 |
| [部署与配置命令 JSON 契约](../../docs/reference/contracts/commands.md)与英文镜像 | M1、M3：特权动作的 CLI 入口，二段确认的 plan/apply 与 token 过期语义 | 未开始 |

## 6. 待决

- 非 systemd 发行版的 accept 启动器形态（OpenRC 服务脚本细节）。
