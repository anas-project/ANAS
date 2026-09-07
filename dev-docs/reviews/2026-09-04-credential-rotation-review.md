---
doc_type: review
status: current
created: 2026-09-04
updated: 2026-09-04
review_baseline: 2026-09-04 工作树，HEAD `5306b63`
---

# 凭据轮换机制审查：全面轮换与单独轮换

审查对象是 `anas credential rotate` 及其周边——它覆盖了什么、没覆盖什么、全面轮换与单独轮换的
语义是否一致。结论先行：**`credentials.provides` 这一类做得相当完整，但它只是五类凭据中的一类；
"全面轮换"这个说法在今天的实现里名不副实。**

## 1. 已实现的部分：一个完整的事务型轮换器

`anas credential rotate` 有三种作用域，共用同一个 planner：

```text
anas credential rotate CREDENTIAL_ID      单独
anas credential rotate --module MODULE    按 Module
anas credential rotate --all              全部
```

执行路径是真正的事务，不是"改个值再重启"：

1. 生成独立的 candidate projection；
2. 停止 previous 的全部或受影响闭包；
3. 按 ready barrier 顺序执行 **probe / reconcile / verify**；
4. **全部验证通过后**才用一次原子 Store Save 提交值、generation 与 `rotation_id`；
5. 随后 promotion candidate。

失败有分级状态（`not_started` / `candidate_failed` / `previous_restored` /
`previous_restore_failed` / `recovery_required`）；Store 提交前失败会恢复 previous，提交后由下一次
排他运行时操作依据 journal 自动完成 promotion。`--dry-run` 与实际执行共用 planner，且明确不生成
随机数、不写 Store、不调 Hook。

这一部分的质量没有问题。下面全是边界与覆盖面的问题。

## 2. 核心发现：五类凭据，三套机制，一处空白

| # | 凭据类别 | 数量 | 轮换入口 | 全面轮换能否覆盖 |
| --- | --- | --- | --- | --- |
| 1 | `credentials.provides` 部署凭据 | 7 | `anas credential rotate` | ✅ `--all` |
| 2 | 本地管理员账号 | 按 Module | `anas admin local rotate MODULE [ACCOUNT]` | ❌ 独立命令，无 `--all` |
| 3 | `effect: credential_rotate` 配置参数 | 9 | `anas config set` + apply 守卫 | ❌ 逐个改配置 |
| 4 | **Resource 凭据** | 7 | **无** | ❌ **完全没有入口** |
| 5 | DNS 厂商凭据 | 按 provider | 无（本就属 external） | — 合理 |

`anas credential rotate --all` 只覆盖第 1 类的 7 条。**其余 16 条各有各的路径，或者根本没有路径。**

一个想"把所有凭据都换一遍"的管理员，今天必须：跑一次 `credential rotate --all`、对每个有本地
管理员的 Module 各跑一次 `admin local rotate`、再对 9 个配置参数各做一次 `config set` 加 apply——
而第 4 类无论怎么做都换不掉。

### 2.1 第 4 类：数据库账号是有理由的延期，compute 的两条才是真空白

先纠正一个容易下错的判断。[凭据轮换设计](../../docs/architecture/credential-rotation.md) §3.3
已经点名了关系数据库资源账号，并说明了为什么现在不接：

> PostgreSQL 与 MariaDB provider 的现有 `ensure` 已能创建或 `ALTER` 普通资源账号，但没有独立的
> 候选认证、authority 检查和回滚合同；统一库存接入后必须把这些资源凭据报告为 unsupported，
> 不能仅凭现有 `ensure` 宣称可轮换。

所以这不是"没人想过"，是**想过并明确拒绝了用 `ensure` 冒充轮换**——立场是对的。

值得强调的是缺的到底是哪一半。`provision.sh` 里那句 `ALTER ROLE ... PASSWORD` **每次 apply 都
无条件执行**，所以"把新口令推下去"这半早就是幂等的。真正缺的是：

1. **没有任何东西会生成新值**——`secrets.Ensure` 见到已有值就原样返回，实际行为是"铸一次，
   永不更换"；
2. **`spec.credential` 没有轮换声明位**，今天只有 `policy: generated`。

**compute 新增的两条凭据则是真正的空白**：它们晚于 §3.3 那份清单，从未被评估过，而且它们的正确
模式互不相同（证书 `overlap`、`lease_secret` `migrate`）。补齐时不能对整类资源凭据一刀切。

### 2.2 compute 让这个空白变得具体

新增的 `compute` 契约带来两条 resource 凭据，而它们的正确轮换语义**互不相同**：

| 凭据 | 应有模式 | 理由 |
| --- | --- | --- |
| 客户端证书 | `overlap` | 先登记新证书、两张同时受信、再撤旧的。这是让轮换不杀掉运行中实例的唯一办法（`INCUS-R-029`） |
| `lease_secret` | `migrate` | 换掉它会让**所有已发布的 URL 集体改变**，必须有人知情 |

这一条把问题从"缺个功能"变成了"缺个声明位"：即使将来给 resource 凭据加上轮换，也**不能一刀切**
——两条凭据在同一个资源里，模式却必须不同。

## 3. 单独轮换与全面轮换的语义差异

三种作用域并不是同一件事的三种粒度，差异要写清楚：

| | 单独 `ID` | `--module` | `--all` |
| --- | --- | --- | --- |
| 选择 | 一条 | 该 Module owner 声明的**完整统一 lifecycle 集合** | 所有可执行目标 |
| 遇到 manual/unsupported | 该条被阻断 | **阻断整个批次** | 不选中，静默跳过 |
| 部分成功 | — | 无 `--allow-partial` | 无 `--allow-partial` |

`--module` 与 `--all` 对 manual 目标的处置**方向相反**：前者阻断整批，后者跳过。两者都有道理
（`--module` 是"把这个 Module 弄干净"，`--all` 是"能换的都换"），但差异没有写在文档里，是一个
容易踩的坑。

## 4. 具体缺陷

### 4.1 `overlap` 可执行，但文档与错误信息都说只有 `reconcile`

`internal/runner/credential_plan.go:62` 与 `:84` 的判定是
`RotationMode != "reconcile" && RotationMode != "overlap"`，即**两种模式都可执行**。但：

- 阻断原因写成 `"rotation mode " + mode + " is not reconcile"`——对 `migrate` 而言这句话虽然不假，
  却暗示 `reconcile` 是唯一可执行模式；
- [命令契约](../../docs/reference/contracts/commands.md)写"`--all` 选择 active deployment 中所有
  可执行 `reconcile` 目标"，同样漏掉了 `overlap`。

两处都应改为"`reconcile` 或 `overlap`"。这是措辞问题，不是行为问题，但它会让人误以为 `overlap`
没实现——而 `overlap` 恰恰是证书类凭据唯一正确的模式。

### 4.2 PostgreSQL 超级用户口令的轮换目前是形式上的

`postgres.password` 属于第 3 类，可以经 `anas config set` + apply 走
`rotate-postgres-password`。但 `modules/postgres/hook/main.go:128` 把
`POSTGRES_HOST_AUTH_METHOD` 设为 `trust`——该基线下连接**不校验口令**。

因此换掉这个值只改变了 Secret Store 里的内容，并不改变任何人能不能连上。
[凭据轮换设计](../../docs/architecture/credential-rotation.md) §3.3 已经点出这一点
（"不足以证明密码认证，不能冒充完成"），此处只是把它记进本审查的发现清单：**一个能跑通、
但不产生安全效果的轮换，比明确不支持更危险**，因为它会让人以为已经换过了。

### 4.3 没有跨类的"全部凭据"视图

`anas credential list` 只列第 1 类。管理员没有任何一条命令能回答"这个部署里一共有多少凭据、
各自多久没换过、哪些换不了"。审计与合规场景下这是第一个会被问到的问题。

### 4.4 没有轮换时效性信息

`list` 返回 `generation`，但不返回**上次轮换时间**。`generation: 2` 不能告诉管理员那是上周换的
还是两年前换的。定期轮换策略无法基于当前输出制定。

## 5. 建议

按投入产出排序：

1. **给 resource 凭据加 `rotation_mode` 声明**（`spec.credential.rotation_mode`），沿用既有四档。
   这是唯一一处"空白"，而且 compute 已经给出了两条模式不同的具体用例；
2. **修正 §4.1 的两处措辞**，把 `overlap` 写进可执行模式。成本近乎为零；
3. **`list` 增加上次轮换时间**，让"多久没换过"可回答；
4. **文档写明 `--module` 与 `--all` 对 manual 目标处置相反**；
5. **考虑一个跨类的只读清单**（第 1–4 类合并展示，标注各自的轮换入口）。注意这只是**视图**统一，
   不是把四套机制合并——它们的事务边界确实不同，强行统一会把第 1 类那套完整的事务语义稀释掉。

第 5 条要克制：本审查不建议做"一条命令换掉所有东西"。本地管理员轮换要改活动系统的账号、配置
参数轮换要走 plan/apply 守卫，把它们塞进同一个事务会让失败恢复变得无法推理。**统一的应该是
可见性，不是执行路径。**
