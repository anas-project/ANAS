---
doc_type: requirement
status: current
created: 2026-09-04
updated: 2026-09-13
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
2. Resource 凭据的生命周期是**两侧**的，两侧回答的是**不同的问题**：

   | 侧 | 问题 | 怎么答 |
   | --- | --- | --- |
   | provider `verify` | 我拥有的系统上，凭据真的换成新值了吗 | **用新值实际登录一次**，而不是「`ALTER ROLE` 没报错」 |
   | consumer 侧 | 消费者拿到新值了吗、用得了吗 | 以新投影重新激活后的健康证据 |

   provider 侧必须做**真实登录测试**：`ALTER ROLE` 返回成功只证明 SQL 执行了，不证明这个角色
   能用这个口令连上。provision 容器用新的用户名/口令实连一次数据库，代价极小，却把「以为换好了」
   这一类失败挡在提交之前。

3. **但 provider 的登录测试不能替代 consumer 侧。** 它证明不了最可能发生的那种失败：
   **消费者手里还是旧值**。provider 测的是它刚写下去的值，消费者读的是自己环境里的值，两者
   是否一致不在 provider 的视野内。`pg_hba` 按客户端地址匹配规则，因此 provider 能连上也不等于
   消费者能连上——取消 `trust` 基线之后这一条会从理论问题变成真实问题。
4. **consumer 侧默认不需要专门的 verify 处理器。** 消费者以新投影重新激活后的
   **healthcheck 通过**即为有效证据；重新激活发生在候选部署内、提交之前，因此这份证据来得及用于
   决定提交还是回滚。只有 healthcheck 不足以覆盖凭据路径的模块（容器起得来但首次查询才会失败）
   才需要声明显式 verify。

   这条同时解决了"消费者能否在激活屏障之前自测"那个两难：它不要求消费者在启动前自测，而是把
   启动本身当作测试。

5. 在两侧契约落地之前，统一库存必须把资源凭据报告为 `unsupported`，**不得仅凭 provider 的
   `ensure` 能执行 `ALTER` 就宣称可轮换**。
6. 缺的不是推送半边：`provision.sh` 的 `ensure` 每次 apply 都无条件执行 `ALTER ROLE`。缺的是
   **生成新值的触发**与**声明位**，两者都必须补。
7. **并非所有 resource "凭据" 都属于本机制。** `lease_secret` 是域名派生密钥，不认证任何东西；
   轮换它改变的是已发布的 URL，不是认证材料。它必须有**专属命令**，不得进入凭据轮换通道，也不得
   被 `--all` 选中——把两种含义不同的东西放进同一个批量操作，是这类系统里最容易造成误操作的一种
   归类错误。

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

1. **已定案：走 (a)。** 判据是「值由谁产生」——`postgres.password` 的 `default_source` 是
   `generated`，不是用户指定的，因此它必须**既能单独轮换、也能随 `--all` 一起轮换**，与其他
   ANAS 自己产生的凭据同一标准。
2. 由此，**修正 `trust` 基线成为前置条件而不是备选项**：声明一条凭据可轮换，却让它的轮换不产生
   安全效果，是比不支持更糟的状态。改为口令认证基线的工作必须排在把它接入统一库存之前。
3. 用户显式指定的凭据不适用本条——那属于 `external` authority，由用户在源头轮换。
4. 在基线修正落地之前，文档必须写明该轮换不产生安全效果。**一个能跑通但无效果的轮换比明确
   不支持更危险**——它会让人以为已经换过了。
5. 必须验证：改基线后既有部署仍能启动，且 provider 的资源账号创建路径不受影响。

## 7. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `CRED-R-001` | `resources.requires[].spec.credential` 必须支持 `rotation_mode` 声明，沿用 `reconcile`/`overlap`/`migrate`/`external` 四档 | 契约 + 单元 |
| `CRED-R-002` | 不得对整类资源凭据一刀切：同一资源内不同凭据可以有不同模式（compute 客户端证书为 `overlap`），且同一资源里可能存在根本不属于本机制的值（`lease_secret`，见 `CRED-R-007`） | 契约 + 单元 |
| `CRED-R-003` | 资源凭据必须建模为两侧生命周期：provider 侧 reconcile + verify、consumer 侧证据；不得复用单侧 owner 契约冒充 | 契约 + 审阅 |
| `CRED-R-016` | provider 侧 `verify` 必须用新凭据**实际登录一次**，不得以 `ALTER ROLE` 未报错代替——后者只证明 SQL 执行了，不证明该角色能用该口令连上 | 单元 + e2e |
| `CRED-R-017` | provider 的登录测试**不得**被当作 consumer 侧证据：它看不见「消费者手里还是旧值」，也看不见按客户端地址匹配的 `pg_hba` 差异 | 审阅 |
| `CRED-R-018` | consumer 侧默认以「新投影重新激活后 healthcheck 通过」为证据，不强制每个消费者实现 verify 处理器；仅 healthcheck 覆盖不到凭据路径的模块需声明显式 verify | 契约 + e2e |
| `CRED-R-004` | 两侧契约落地前，统一库存必须把资源凭据报告为 `unsupported`，不得仅凭 `ensure` 可执行 `ALTER` 宣称可轮换 | 单元 |
| `CRED-R-005` | 必须补齐“生成新值”的触发：`secrets.Ensure` 见到已有值即返回，现状等于铸一次永不更换 | 单元 |
| `CRED-R-006` | compute 客户端证书必须以 `overlap` 轮换：新旧同时受信再撤旧，轮换不得杀掉运行中实例 | 单元 + e2e |
| `CRED-R-007` | compute `lease_secret` **不得**走凭据轮换通道：它不是凭据，轮换它改变的是已发布的域名而非认证材料。必须提供专属命令，且不得被 `--all` 选中 | 契约 + 审阅 |
| `CRED-R-008` | 文档与阻断原因必须把 `overlap` 表述为可执行模式，不得写成只有 `reconcile` 可执行 | 文档 + 单元 |
| `CRED-R-009` | `--module` 与 `--all` 对 manual/unsupported 目标处置相反这一事实必须写入文档 | 文档 |
| `CRED-R-010` | 必须提供跨五类的只读凭据清单，每条标注类别与轮换入口；它是视图统一，不得借此合并执行路径 | 契约 + e2e |
| `CRED-R-011` | 凭据清单必须包含上次轮换时间，仅有 `generation` 不足以回答“多久没换过” | 契约 + 单元 |
| `CRED-R-012` | `postgres.password` 的 `default_source` 是 `generated`，因此必须既能单独轮换也能随 `--all` 轮换；修正 `trust` 基线是其前置条件，不是备选项 | 审阅 + e2e |
| `CRED-R-013` | 处置前文档必须写明该轮换不产生安全效果——能跑通但无效果比明确不支持更危险 | 文档 |
| `CRED-R-014` | 若改为口令认证基线，必须验证既有部署仍能启动且 provider 的资源账号创建路径不受影响 | e2e |
| `CRED-R-015` | `lease_secret` 的专属轮换命令必须说明其后果是「全部已发布 URL 改变」，并要求显式确认；不得复用密码轮换的措辞与流程 | 契约 + 文档 |
