---
doc_type: requirement
status: current
created: 2026-09-04
updated: 2026-09-04
---

# 凭据轮换覆盖面与语义要求

本文规定 ANAS 凭据轮换在**覆盖面**与**语义一致性**上必须交付的结果。轮换事务本身的设计见
[凭据库存与 deployment 驱动轮换设计](../../docs/architecture/credential-rotation.md)；本轮审查的
发现见[凭据轮换机制审查](../reviews/2026-09-04-credential-rotation-review.md)。

本文**不重新设计**已实现的那套事务型轮换器——它的 candidate projection、ready barrier、
probe/reconcile/verify 与原子 Store 提交都已经工作。本文只处理它**覆盖不到的部分**，以及覆盖面
边界没有说清楚的部分。

关键词“必须”“不得”“应该”具有规范性。ID 一经分配即固定，废弃的需求保留行并标 `已废弃`。

## 1. 问题

凭据分五类，轮换机制有三套，其中一类没有任何入口：

| 类别 | 数量 | 轮换入口 |
| --- | --- | --- |
| `credentials.provides` 部署凭据 | 7 | `anas credential rotate`（含 `--module` / `--all`） |
| 本地管理员账号 | 按 Module | `anas admin local rotate` |
| `effect: credential_rotate` 配置参数 | 9 | `anas config set` + apply 守卫 |
| **Resource 凭据** | 7 | **无** |
| DNS 厂商凭据 | 按 provider | 无（属 external，合理） |

因此 `anas credential rotate --all` 这个名字是名不副实的：它只覆盖 7/23。

## 2. 范围

必须包含：

- Resource 凭据的轮换声明位与两侧生命周期契约；
- `overlap` 在文档与错误信息中被正确表述；
- 跨类的只读凭据清单与轮换时效信息；
- PostgreSQL `trust` 基线导致的“轮换无效果”问题的处置。

不要求：

- 把四套机制合并成一条执行路径。它们的事务边界确实不同，强行统一会稀释第 1 类已有的事务语义；
- 为第 5 类（DNS 厂商凭据）提供轮换——ANAS 不拥有它们。

## 3. Resource 凭据

1. `resources.requires[].spec.credential` 必须支持声明 `rotation_mode`，沿用既有四档
   （`reconcile` / `overlap` / `migrate` / `external`），**不得对整类资源凭据一刀切**。
2. Resource 凭据的生命周期是**两侧**的：provider 侧 reconcile（推新值），consumer 侧 verify
   （确认换完之后自己仍能连上）。这与 `credentials.provides` 的单侧 owner 契约不同，必须显式
   建模，不得复用单侧契约冒充。
3. 在两侧契约落地之前，统一库存必须把资源凭据报告为 `unsupported`，**不得仅凭 provider 的
   `ensure` 能执行 `ALTER` 就宣称可轮换**。
4. 缺的不是推送半边：`provision.sh` 的 `ensure` 每次 apply 都无条件执行 `ALTER ROLE`。缺的是
   **生成新值的触发**与**声明位**，两者都必须补。

## 4. 语义一致性

1. `overlap` 与 `reconcile` 同为可执行模式。文档与阻断原因**不得**表述为只有 `reconcile` 可执行
   ——`overlap` 恰是证书类凭据唯一正确的模式，误述会让人以为它没实现。
2. `--module` 与 `--all` 对 manual/unsupported 目标的处置方向相反（前者阻断整批，后者跳过）。
   两者都有道理，但差异必须写进文档。

## 5. 可见性

1. 必须提供**跨类的只读凭据清单**：五类合并展示，每条标注所属类别与轮换入口。它是**视图**统一，
   不是执行路径统一。
2. 清单必须包含**上次轮换时间**。仅有 `generation` 无法回答“多久没换过”，定期轮换策略因此无法
   制定。

## 6. PostgreSQL `trust` 基线

`modules/postgres/hook/main.go` 将 `POSTGRES_HOST_AUTH_METHOD` 设为 `trust`，该基线下连接不校验
口令。因此 `postgres.password` 的轮换今天**能跑通但不产生安全效果**。

1. 必须消除“能跑通但无效果”这一状态。可接受的处置有两种，必须择一并记录理由：
   **(a)** 改为口令认证基线，使轮换真正生效；**(b)** 明确把该口令标为 `unsupported`
   并说明当前认证模型。
2. 在处置之前，文档必须写明该轮换不产生安全效果。**一个能跑通但无效果的轮换比明确不支持更
   危险**——它会让人以为已经换过了。
3. 处置为 (a) 时必须验证：改基线后既有部署仍能启动，且 provider 的资源账号创建路径不受影响。

## 7. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `CRED-R-001` | `resources.requires[].spec.credential` 必须支持 `rotation_mode` 声明，沿用 `reconcile`/`overlap`/`migrate`/`external` 四档 | 契约 + 单元 |
| `CRED-R-002` | 不得对整类资源凭据一刀切：同一资源内不同凭据可以有不同模式（compute 客户端证书 `overlap`、`lease_secret` `migrate`） | 契约 + 单元 |
| `CRED-R-003` | 资源凭据必须建模为两侧生命周期：provider 侧 reconcile、consumer 侧 verify；不得复用单侧 owner 契约冒充 | 契约 + 审阅 |
| `CRED-R-004` | 两侧契约落地前，统一库存必须把资源凭据报告为 `unsupported`，不得仅凭 `ensure` 可执行 `ALTER` 宣称可轮换 | 单元 |
| `CRED-R-005` | 必须补齐“生成新值”的触发：`secrets.Ensure` 见到已有值即返回，现状等于铸一次永不更换 | 单元 |
| `CRED-R-006` | compute 客户端证书必须以 `overlap` 轮换：新旧同时受信再撤旧，轮换不得杀掉运行中实例 | 单元 + e2e |
| `CRED-R-007` | compute `lease_secret` 必须以 `migrate` 轮换：它会改变全部已发布 URL，不得进入例行批量轮换 | 契约 + 审阅 |
| `CRED-R-008` | 文档与阻断原因必须把 `overlap` 表述为可执行模式，不得写成只有 `reconcile` 可执行 | 文档 + 单元 |
| `CRED-R-009` | `--module` 与 `--all` 对 manual/unsupported 目标处置相反这一事实必须写入文档 | 文档 |
| `CRED-R-010` | 必须提供跨五类的只读凭据清单，每条标注类别与轮换入口；它是视图统一，不得借此合并执行路径 | 契约 + e2e |
| `CRED-R-011` | 凭据清单必须包含上次轮换时间，仅有 `generation` 不足以回答“多久没换过” | 契约 + 单元 |
| `CRED-R-012` | 必须消除 PostgreSQL 超级用户口令“能跑通但无安全效果”的状态：或改为口令认证基线使其生效，或明确标为 `unsupported`，二择一并记录理由 | 审阅 + e2e |
| `CRED-R-013` | 处置前文档必须写明该轮换不产生安全效果——能跑通但无效果比明确不支持更危险 | 文档 |
| `CRED-R-014` | 若改为口令认证基线，必须验证既有部署仍能启动且 provider 的资源账号创建路径不受影响 | e2e |
