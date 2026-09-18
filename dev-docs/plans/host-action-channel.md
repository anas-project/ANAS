---
doc_type: plan
status: proposed
created: 2026-09-04
updated: 2026-09-18
---

# 宿主特权动作通道实施计划

验收依据是[宿主特权动作通道要求](../requirements/host-action-channel.md)；设计见
[同名架构文档](../../docs/architecture/host-action-channel.md)。

**当前状态：通道与动作均未开始，设计已定。** 它依赖[统一动作 ABI](action-abi.md) 先落地——通道复用那套
线格式与 job 语义，不另起一套。截至 2026-09-18，ABI 前置已有共用 journal、执行 recorder、
非特权 Module 注册表/Linux 进程适配、ModuleActionWorker 及调用/查询/订阅服务的内部代码；
动作级幂等、终态后 1 小时保留和 coalesce/reject/queue 已接入同一 journal，仍无 root 注册表。
这些代码与回归源尚未编译/运行，主 daemon 与旧 CLI/HTTP 尚未迁移。Module worker 不领取宿主
命名空间的动作，不能据此注册 Incus HTTP 宿主动作或宣称 root 通道完成。没有新增 root RPC、
脚本入口或 CAP_SYS_ADMIN helper。

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

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go test ./...` | 待记录 |
| `npm run docs:check-requirements` | 待记录 |

## 4. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-003 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | 安装与卸载对称性 | — | 待执行 |
| R-004 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | CLI 与 Web 同一通道同一审计 | — | 待执行 |
| R-011 | 待新增 `test-env/scripts/server-host-action-e2e.sh` | token 过期后重新展示而非沿用旧摘要 | — | 待执行 |

## 5. 待决

- 非 systemd 发行版的 accept 启动器形态（OpenRC 服务脚本细节）。
