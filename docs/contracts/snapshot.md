# snapshot 命令 JSON 契约

> 状态：设计中，尚未实现。通用约定见 [README.md](README.md)，本文不再重复。
> 与 backup 的分界见 [backup.md](backup.md)。

## 存储布局

```text
<workspace>/
  config.yml
  config.lock.yml
  data/                   ← btrfs subvolume（快照的源必须是 subvolume）
  snapshots/              ← 普通目录，0700
    .tmp-<snapshot-id>/   # 创建中的快照，完成后 rename 到位
    <snapshot-id>/
      snapshot.yml        # 元数据，0600，complete 字段最后写
      meta/
        config.yml            # 取自制品的 config.source.yml，非磁盘当前值，0600
        config.lock.yml
        secrets.generated.yml # 该时刻的 secret store，0600
        deployment-state.yml  # 对应那一个 .anas/state/deployments/<id>.yml
      deployment/         # .anas/deployments/<id>/ 的完整副本
      data/               # data 的只读 btrfs 快照
  .anas/                  ← 0700
    state/
      snapshots.yml       # 派生索引，可由扫描重建
```

**快照必须自足到仅凭自身即可恢复系统。** 送到外部盘之后那边没有 `.anas`，所以
config、lock、secrets、可运行制品、数据全部是实体副本而非引用。唯一无法覆盖的是
上游基础镜像（需要 registry），见"恢复语义"。

`config.yml` 与 `secrets.generated.yml` 含明文密钥，故 `snapshots/` 整体 0700、
`meta/` 下文件 0600。

**`snapshots/` 是普通目录，不是 subvolume。** 只有快照的**源**必须是 subvolume
（`data/`）；目标只要求父目录存在且位于同一个 btrfs 上，`snapshots/<id>/data` 会被
创建**为** subvolume。把容器目录也做成 subvolume 没有任何收益（qgroup 已按各快照
计量、send 只送单个快照、`--one-file-system` 会连 `data/` 一起排除），反而会让含嵌套
subvolume 的目录无法直接删除，并切断下面的硬链接降级路径。

### `deployment/` 的复制方式

按以下顺序降级复制：

1. `cp --reflink=auto` —— btrfs 可用
2. 硬链接 —— **有前置条件，见下**。因 `snapshots/` 是普通目录而非 subvolume，btrfs 上
   同样不跨边界，无 `EXDEV` 限制
3. 完整复制 —— 兜底，约 42M/快照

### 硬链接的前置条件：制品封印

硬链接与源共享 inode，**任何对 deployment 文件的原地写入都会同时污染所有引用它的
快照**。设计文档 §13 规定"release 普通资产在封印后改为只读"，但**该封印尚未实现**
——全仓仅两处 `os.Chmod`（[deployment.go:1121](../../internal/runner/deployment.go)、
[runner.go:286](../../internal/runner/runner.go)），都是给 base 设 0700，制品一个
都没封。

当前代码路径上确实没有 render 之后的写入（`applyHookFiles`、`renderERBFiles`、
`freezeHookBinary`、`writeEnv` 全在 render 阶段；`runAfterStart` 只处理
`DockerCopies`），但这是约定而非保证。

**因此：封印实现之前，降级链跳过第 2 档，只走 reflink → 完整复制。** 封印落地
（制品 0444/0555、`.env` 0400）后再启用硬链接。

复制它换来一条跨子系统不变量的消失：deployment GC **不需要**读快照索引来规避被引用
的制品，pin 一个快照也不再需要连带 pin 住 deployment。

### `state/` 只拷不可重建的部分

| 内容 | 是否拷入 | 原因 |
| --- | --- | --- |
| `state/deployments/<id>.yml` | ✅ 仅匹配的那一个 | 不可重建 |
| `state/active.yml` | ❌ 恢复时重建 | 其 `previous_deployments` 引用快照中不存在的 deployment，拷入即悬空。本快照的 active 由 `deployment_id` 隐含 |
| `state/index.yml` | ❌ | 定义上可重建 |
| `state/transactions/` | ❌ | 诊断性、瞬态 |
| `state/lock` | ❌ | 运行时锁 |

### 创建的原子性

先写入 `snapshots/.tmp-<id>/`，全部内容落盘并 `fsync` 后整体 `rename` 到
`snapshots/<id>/`，`snapshot.yml` 的 `complete: true` 最后写入。中途失败留下的
`.tmp-*` 目录由下次任意 anas 命令启动时清理。

### 恢复语义

**全有或全无。** secret store 是追加式分代的，恢复旧快照会丢弃其后生成的代次；这与
data 一同回退是自洽的，但"只恢复 meta 保留当前 data"会造成密钥与数据错配，CLI 必须
拒绝该组合。

恢复后仍需从 registry 拉取上游基础镜像。完全离线恢复需要 `docker save` 级别的镜像
归档，属二期 `--include-images` 范围。

## 真相源

**`snapshot.yml` 是唯一权威。** `.anas/state/snapshots.yml`（deployment → snapshot
列表）是**派生索引**，可由扫描 `snapshots/*/snapshot.yml` 完全重建，
`anas snapshot verify --rebuild-index` 负责重建。

不设第二个权威方向。`deploymentState.SnapshotID` 应删除——它只服务"Y 回滚到 X 该用
哪个快照"这一个查询，而该查询用 `from_deployment` / `to_deployment` 即可完成，保留它
只会制造双写不一致窗口。

## 保留策略

- `snapshot.keep_auto`（默认 **5**）：`kind: auto` 且 `pinned: false` 的按创建时间
  倒序保留 N 个，其余回收。
- `kind: manual` 与 `pinned: true` 一律不参与自动回收，**且不计入 N**。
- 回收时机：每次 `apply` 成功提交 active 之后，以及显式 `anas snapshot prune`。
- deployment GC 与快照**无耦合**：快照持有自己的制品副本（见上），`.anas/deployments/`
  的回收不会影响任何快照。

## 自动触发

`upgrade` 节点**已经存在**（[manifest.go:104](../../internal/runner/manifest.go)），
本设计在其下新增 `data_breaking`，不另建顶层字段：

```yaml
upgrade:
  from: ">=30.0.0"           # 已有：允许的来源版本，不满足由 validateUpgrade 直接阻断
  data_breaking: ["31.0.0"]  # 新增：跨过即改写数据格式
```

两者语义不同，不能互相推导：`from` 说的是"能不能升"，`data_breaking` 说的是"升了能不
能回来"。一个 cask 可以允许从任意版本升上来，而某次升级仍然改写了数据格式。

截至目前**没有任何 cask 声明 `upgrade:`**，两个字段都是纯新增。

`data_breaking` 列出**磁盘数据格式发生断代的版本**：一旦部署了 ≥ 该版本，磁盘数据就
不再能被低于它的版本读取。

它是列表而非布尔，因为 breaking 是**跃迁的属性，不是版本的属性**：30.0.1 → 30.0.2 不
breaking，30.0.1 → 31.0.0 breaking，30.0.1 → 33.0.0 一次跨过两个断代点。只有列出断
代点才能判断任意两版本之间的跳跃。

### 判定

设当前已部署 `A`、目标 `B`：

```
breaking  ⟺  ∃ V ∈ data_breaking,  A < V ≤ B
```

`≤ B` 而非 `< B`：升到断代版本本身就已改写格式。多个 cask 同时升级时任一 breaking 即
整体 breaking——快照是 workspace 级的，一个就够。

### 未声明 ≠ 声明为空（必须区分，否则默认值反转）

若把"未声明 `data_breaking`"当作空列表，上式恒为假 → 判定"从不 breaking" → **所有
回滚放行**。而现状是**默认全部阻断**，且目前 16 个 cask 一个都没声明 `upgrade:`。
按空列表处理会把默认行为从最保守翻转成最宽松，是一个会静默生效的安全回归。

因此：

| 声明 | 含义 | 回滚判定 |
| --- | --- | --- |
| 不写 `data_breaking` | **未知** | 沿用现状：任何版本差异一律阻断 |
| `data_breaking: []` | 显式声明任何版本跃迁都不改写数据格式 | 放行 |
| `data_breaking: ["31.0.0"]` | 列出断代点 | 按上式判定 |

实现上必须用 `*[]string` 才能区分 `nil` 与空切片，不能用 `[]string`。

**发版前动作**：给每个 cask 显式写 `data_breaking: []`。含义是"到当前版本为止没有断代
点"——一个可核实的事实陈述，不是对未来的承诺。若发版时全部留空不写，rollback 会因
"未知"而永远被阻断。

### 列表不需要永久累积

`A` 的下界由 `upgrade.from` 保证（`validateUpgrade` 拒绝任何不满足约束的来源版本），
因此**任何 `V ≤ upgrade.from 的下界` 的条目永远无法满足 `A < V`，是死条目**：

```yaml
upgrade:
  from: ">=30.0.0"             # 低于 30 根本升不上来
  data_breaking: ["31.0.0"]    # 历史上的 21.0.0/25.0.0/28.0.0 已无法命中，删除
```

三条配套规则：

- **提高 `upgrade.from` 时同步修剪 `data_breaking`**——这是唯一安全的删除时机
- **已有条目只增不改**：修改历史条目会改变已部署系统的判定结果
- `upgrade.from` 必须写成有明确下界的形式（`>=X` 系）。`"!=31.0.0"` 这类没有下界，
  无法修剪

（cask 独立分发落地后可改为每个版本只声明一个布尔、由 runner 遍历 `A..B` 之间所有
版本判定。现在做不到是因为 runner 手中只有 `B` 版本的 cask.yml。见
[cask-distribution-draft.md](../cask-distribution-draft.md)。）

版本粒度用 cask `version` 而非 `app_version`，与 `validateUpgrade` 保持一致；且
samba_dc、lego、ddns、core 等 cask 本就没有 `app_version`。

### 升级方向

breaking → `apply` 前自动创建 `kind: auto` 快照。`--no-snapshot` 显式禁用，因为这是
在放弃唯一退路，需要 `-y`。

### 回滚方向

`deploymentRollbackVersionBlockers`（[deployment.go:766](../../internal/runner/deployment.go)）
现在把**任何**版本差异都判为 "data compatibility unknown" 一律阻断——连 patch 号变化
都不让回滚。该函数上方注释明说这是等待本契约的保守占位。改为三档：

**`rollback` 永不动数据。** 它只切换制品，没有 `--restore-data` 这个开关。需要回退
数据一律走 `anas snapshot restore`。

| 情形 | 结果 |
| --- | --- |
| 目标 deployment 版本**完全相同**（纯配置回滚） | ✅ 放行，数据不动（现状即如此） |
| 有版本变化，cask 未声明 `data_breaking` | ❌ 阻断（`--allow-risky` 可绕）——现状 |
| 有版本变化，已声明且反向不跨断代点 | ✅ 放行 |
| 有版本变化，**反向跨断代点** | ❌ **直接报错，不提供绕过**，指向 snapshot restore |

第三档是本期唯一的行为放宽——把"任何版本差异都要 `--allow-risky`"缩小到"只有跨断代
点才要"。这是**体验改进而非安全性改进**，且以 cask 作者正确声明为前提，故默认值必须
保持保守（见上）。

第四档不给 `--allow-risky` 逃生舱：跨断代点意味着旧代码**确定**读不了新格式的数据，
放行只会让服务起不来。错误信息必须给出下一步：

```
cannot roll back nextcloud 31.0.0 -> 30.0.1: crosses data-breaking version 31.0.0
data written by 31.0.0 cannot be read by 30.0.1

to return to that state, restore a snapshot instead:
  anas snapshot list
  anas snapshot restore <id>
```

### 由此简化掉的三处

1. **`rollback --restore-data` 标志删除**，`rollback` 语义变为纯粹的制品切换
2. **`restoreDeploymentSnapshot` 的跃迁配对校验删除**
   （[deployment.go:925](../../internal/runner/deployment.go) 的
   `snapshot.ToDeployment != currentID || snapshot.FromDeployment != targetID`）——
   该检查存在的唯一理由是快照绑定于特定跃迁；快照自足之后它就是一个**时间点**，恢复
   它无需参照 `deployments/` 中的任何东西
3. **`deploymentState.SnapshotID` 删除**（本就计划删除，此处失去最后的存在理由）

### 为什么不取消"不带数据的回滚"

制品回滚与快照恢复**解决的不是同一个问题**，前者也不是后者的弱化版：

| 场景 | 正确做法 | 若强制用快照恢复 |
| --- | --- | --- |
| 改错域名/端口/资源限制，服务起不来，**数据是好的** | 制品回滚 | **丢掉自上次 apply 以来的全部数据** |
| 30.0.1 → 30.0.2 有回归，数据格式未变 | 制品回滚 | 同上 |
| 升级把数据库 schema 改坏了 | 快照恢复 | 正确 |

前两种是最常见的回滚场景。在 NAS 场景（Nextcloud 文件、邮件、数据库写入）下，"回滚
即丢失这期间所有数据"是灾难性的——用户回滚是为了修一个配置错误，不是为了放弃一周的
工作。取消制品回滚会让最常见的情况没有正确答案可选。

### 配置变更触发

代码中已有一个"不可自动逆转"的 effect 集合，三处一致使用
（[config_state.go:51](../../internal/runner/config_state.go)、
[deployment.go:754](../../internal/runner/deployment.go)）。**直接复用，不另立标准**：

| effect | 触发快照 | 理由 |
| --- | --- | --- |
| `data_migrate`（7 处） | ✅ | 已有数据需要迁移 |
| `credential_rotate`（10 处） | ✅ | 改的是服务**内部**状态（LDAP/DB 中的口令），改回 config.yml 不会改回它 |
| `immutable` | — | 根本改不了，不会发生 |
| `reconcile` | ❌ | hook 要求幂等，改的是可重建的外部状态 |
| `container_recreate` / `hot_reload` / `container_restart` / `process_restart` | ❌ | 不触及数据 |

分工：`data_migrate` 与 `credential_rotate` 由**配置变更**触发，`data_breaking` 由
**版本升级**触发。

**不对"每次 apply 都建快照"**：`keep_auto` 槽位会被例行 apply 填满，真正要命的
pre-breaking 快照反而被挤掉。要强制建用显式 `--snapshot`。

### 边界

- **降级**：`validateUpgrade` 已全面禁止（`cmp > 0` 直接报错），`A > B` 不会发生
- **cask 新增**：无 `A`，不判 breaking（没有旧数据）
- **cask 移除**：数据留在磁盘上，保守起见维持现有阻断
- `upgrade.from` 与 `data_breaking` **独立**：前者决定"能不能升"，后者决定"升了能不
  能回来"

### 标错的后果不对称

- **漏标**：升级前不建快照，出事无法回退；回滚被放行但数据格式对不上，服务起不来。
  **危险方向**
- **多标**：多建几个快照、回滚被过度阻断（`--allow-risky` 可绕）。**安全方向**

因此 cask 作者指引应明确：**宁可多标**。

### 非 btrfs

无法建快照，breaking 升级需打印明确警告并要求 `-y`，不得静默继续。

---

## `snapshot.yml` 字段

```yaml
api_version: anas.dev/snapshot/v1
id: 20260729T081504Z-4a1b2c3d
backend: btrfs
kind: auto              # auto | manual
pinned: false
created_at: 2026-07-29T08:15:04Z
reason: cask_upgrade_breaking    # 枚举，见下
label: "升级前"                   # 用户自由文本，可空
source: /home/whl/anas-deploy/data
path: /home/whl/anas-deploy/snapshots/20260729T081504Z-4a1b2c3d/data
from_deployment: 20260728T041632Z-a9f9519d
to_deployment: 20260728T131040Z-cd6fc061
deployment_id: 20260728T131040Z-cd6fc061
config_digest: sha256:…
lock_digest: sha256:…
secret_generation: 7
casks: { nextcloud: "30.0.1", authentik: "2024.10.5" }
recovery_path: ""       # 回滚时被挪开的原数据位置，仅回滚后有值
```

### `reason` 枚举

| 值 | 触发场景 |
| --- | --- |
| `cask_upgrade_breaking` | 跨过 `data_breaking_versions` |
| `setting_data_migrate` | 变更了 `effect: data_migrate` 的设置 |
| `manual` | 用户执行 `anas snapshot create` |
| `pre_backup` | `anas backup create` 内部建的快照 |

---

## `anas snapshot list`

```
anas snapshot list [--json]
```

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/home/whl/anas-deploy",
  "keep_auto": 5,
  "snapshots": [
    {
      "id": "20260729T081504Z-4a1b2c3d",
      "kind": "auto",
      "pinned": false,
      "created_at": "2026-07-29T08:15:04Z",
      "reason": "cask_upgrade_breaking",
      "label": "",
      "deployment_id": "20260728T131040Z-cd6fc061",
      "complete": true,
      "config_matches_current": false,
      "size_bytes": null,
      "casks": { "nextcloud": "30.0.1" },
      "healthy": true
    }
  ]
}
```

- `size_bytes` 为 `null` 表示 btrfs qgroup 未启用，无法统计——**不要输出 `0`**。
- `config_matches_current: false` 表示当前 `config.yml` 与该快照记录的不一致。
- `complete: false` 表示这是一个中断产物，不可用于恢复。

## `anas snapshot show <id>`

输出 `snapshot.yml` 的全部字段，外加运行时校验结果。

`"config_matches_current": false` 表示磁盘上当前的 `config.yml` 与快照记录的不同。
这**不是异常**——pre-upgrade 自动快照几乎总是如此：它捕获的是旧数据与旧 deployment，
而用户此刻磁盘上的 config 已经是他们准备 apply 的新版本。如实呈现，不要隐藏。

### 前置依赖：制品必须携带原始配置

快照的 `meta/config.yml` 必须是**与该快照的 `deployment_id` 匹配**的那一份配置，而
不是"快照时刻磁盘上的 config.yml"。对 `kind: manual` 的稳态快照二者相同，但对
`kind: auto` 的 pre-upgrade 快照必然不同：

```
t0  磁盘 config = 30.0.1，active = deployment-A，data = 30 格式
t1  用户编辑 config.yml → 31.0.0        ← 磁盘上的 config 此刻已是新的
t2  apply 检测到 breaking
t3  建 auto 快照：data = 30 格式，deployment/ = deployment-A
t4  渲染 deployment-B 并激活
```

`t3` 若拷磁盘上的 config，得到 31.0.0 的配置配 30 格式的数据和 deployment-A——恢复
它正好得到快照本该救你脱离的坏状态。

而 deployment-A 对应的原始配置在系统里没有任何地方保存原文：

- `saveAppliedConfig`（[config_state.go:70](../../internal/runner/config_state.go)）
  写入 `state/config-applied.yml`，其中只有每个 setting 的 **sha256 哈希**
- 设计文档 §9.1 明确 release 不保存原始配置，只有脱敏的 `resolved.redacted.yml`
- `deployment.yml` 的 `Settings` 同样只是指纹

**因此本设计要求 `apply` 时把原始配置原文写入
`deployments/<id>/config.source.yml`（0600）。** 这不与 §9.1 冲突——那条限制的是不
拿它作**启动输入**（避免 config.yml 兼具期望状态与启动输入的双重身份），此处是一份
只读的**恢复用**副本，用途与命名都区分开。

这也不是新机制：legacy 的 `release/` 路径已经在做
（`copyFile(cfgPath, work/config.yml)`，[runner.go:369](../../internal/runner/runner.go)），
只是 deployment 路径没有承接——服务器实测 `deployments/<id>/` 下只有 `casks`、
`deployment.yml`、`lock.yml`。

用 `state/config-applied.yml` 的 sha256 指纹可以**检测**磁盘 config 与 applied 不符，
但检测不等于能**产出**那份旧配置。

该文件含明文 secret（用户 secret 可能直接写在 config.yml 中），故 0600，与 §3
「deployment 目录整体按敏感数据保护」一致。

**没有它，"仅凭快照恢复系统"不成立。**

## `anas snapshot create`

```
anas snapshot create [--label "…"] [--reason manual] [--json]
```

创建 `kind: manual` 快照。输出同 `show`。

## `anas snapshot pin` / `unpin`

```
anas snapshot pin <id> [--label "…"] [--json]
anas snapshot unpin <id> [--json]
```

## `anas snapshot delete`

```
anas snapshot delete <id> [--force] [-y] [--json]
```

`pinned: true` 的快照需要 `--force`。

## `anas snapshot prune`

```
anas snapshot prune [--dry-run] [--keep N] [-y] [--json]
```

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "dry_run": true,
  "keep_auto": 5,
  "would_delete": [
    { "id": "20260720T…", "kind": "auto", "created_at": "…", "size_bytes": null }
  ],
  "retained": 5,
  "pinned_excluded": 2
}
```

非 `--dry-run` 时字段名为 `deleted`。**第一次运行保留策略之前，用户必须能先看它要删
什么**，所以 `--dry-run` 不是可选功能。

## `anas snapshot verify`

```
anas snapshot verify [<id>] [--rebuild-index] [--json]
```

校验元数据与实际 subvolume 是否对得上。设计为可被 cron 调用：元数据在、subvolume 被
人手工 `btrfs subvolume delete` 掉的情况，只有回滚那一刻才会暴露。

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": false,
  "checked": 7,
  "index_rebuilt": false,
  "problems": [
    { "id": "20260720T…", "code": "subvolume_missing", "message": "…" },
    { "id": "20260722T…", "code": "deployment_missing", "message": "…" }
  ]
}
```

### `problems[].code` 枚举

| 值 | 含义 |
| --- | --- |
| `subvolume_missing` | `snapshot.yml` 在但数据 subvolume 不见了 |
| `metadata_unreadable` | `snapshot.yml` 损坏或版本不支持 |
| `meta_incomplete` | `meta/` 下缺少 config / lock / secrets / deployment-state 之一 |
| `deployment_incomplete` | 快照内 `deployment/` 副本缺失或不完整 |
| `snapshot_incomplete` | `complete: false`，中断产物 |
| `index_stale` | 派生索引与实际扫描结果不一致（`--rebuild-index` 可修） |

## `anas snapshot restore <id>`

```
anas snapshot restore <id> -w <workspace> [--dry-run] [-y] [--json]
```

把 workspace 恢复到该快照代表的时间点：data、制品、config、lock、secrets、必要 state
一同还原。这是**唯一**会回退数据的命令。

- **必须显式 `-w`**，不接受 `ANAS_WORKSPACE`，不接受 cwd 推导。
- `--dry-run` 输出将被覆盖的路径清单，不落盘；非 dry-run 需 `-y`。
- **全有或全无**：不允许"只恢复 meta 保留当前 data"，那会造成密钥与数据错配。
- 快照 `complete: false` 时拒绝执行。
- 恢复前先把当前状态另建一个 `kind: auto`、`reason: pre_restore` 的快照——恢复本身
  也要可撤销。
- `state/active.yml` 由快照的 `deployment_id` 重新生成，不来自快照副本。
- 恢复**不自动启动服务**，输出 `next_steps`。

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/home/whl/anas-deploy",
  "restored_from": "20260729T081504Z-4a1b2c3d",
  "pre_restore_snapshot": "20260730T093012Z-7f8e9a0b",
  "restored": ["config", "lock", "secrets", "state", "deployment", "data"],
  "deployment_id": "20260728T131040Z-cd6fc061",
  "next_steps": ["anas start -w /home/whl/anas-deploy"]
}
```

## `anas snapshot path <id>`

打印快照的只读数据路径并退出。btrfs 快照本身就是可读目录，用户想捞一个误删的文件
不该被迫整体回滚。

```json
{ "api_version": "anas.dev/cli/v1", "ok": true, "id": "…", "path": "/home/whl/anas-deploy/snapshots/…/data" }
```

## 二期

`anas snapshot diff <id>` —— 对比快照与当前的 cask 版本、配置差异，回滚前知道会丢
什么。有价值但不阻塞一期。
