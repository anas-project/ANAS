---
doc_type: plan
status: proposed
created: 2026-09-04
updated: 2026-09-04
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
