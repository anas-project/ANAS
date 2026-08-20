# snapshot 命令 JSON 契约

> 状态：**已实现**（`list` / `show` / `create` / `restore` / `pin` / `unpin` /
> `delete` / `prune` / `verify` / `path`），"自动触发"一节（`data_breaking`、
> 自动快照触发条件与回滚判定）**亦已落地**。`diff` 列入二期。
> 通用约定见 [契约索引](index.md)，本文不再重复。
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
        config-managed.yml    # 与上述 config.yml 匹配的 CLI 完整性摘要，0600
        config.lock.yml
        secrets.yml # 该时刻的 secret store，0600
        local-admins.yml       # 本地管理员锁定名称与 Secret 逻辑键，0600
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

`config.yml` 与 `secrets.yml` 含明文密钥，`local-admins.yml` 虽不含密码但属于
安全库存，故 `snapshots/` 整体 0700、`meta/` 下文件 0600。恢复时三者必须一起回滚，
否则用户名锁与密码逻辑键可能指向不同账号。

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

### 硬链接的前置条件：制品封印（**已落地**）

硬链接与源共享 inode，**任何对 deployment 文件的原地写入都会同时污染所有引用它的
快照**。设计文档 §13 规定"release 普通资产在封印后改为只读"。该封印已由
`sealDeployment`（[artifact.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/artifact.go)）实现，在 staging
rename 进 `deployments/<id>/` 之前执行，因此三档降级链**全部可用**。

封印**按位清除写权限**而非赋固定模式，保留 render 已经做出的两个区分：可执行文件仍
可执行（0755 → 0555），owner-only 的敏感文件仍是 owner-only（0600 → 0400），其余落在
0444。若统一赋 0444，`.env` 会从 0600 变成全局可读——为了只读而放宽了访问面。

**目录保持 0700，不封。** 只读目录会连 unlink 一起挡住，而 unlink-and-replace 分配的
是新 inode，恰恰是硬链接本来就安全的那种改动；封目录换不来额外保证，却会让
deployment 无法回收。

`snapshot.yml` 的 `artifact_copy` 字段记录实际用到的档位（`reflink` / `hardlink` /
`copy`）。第 1 档用 `cp -a --reflink=always` 而非 `=auto`：`auto` 会静默降级为完整
复制，记录下来的档位就成了假话。

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

### 删除子卷需要权限（实测结论，与"create/snapshot/delete 都无需 root"的通行说法不符）

`btrfs subvolume create` 与 `snapshot` 普通用户可用，`delete` **不可用**：
`BTRFS_IOC_SNAP_DESTROY` 要求 CAP_SYS_ADMIN，除非文件系统挂载时带
`user_subvol_rm_allowed`。以下为独立非生产环境的历史实测（内核 5.15，`/data` 未带
该选项）：

| 操作 | 普通用户 |
| --- | --- |
| `btrfs subvolume create` | ✅ |
| `btrfs subvolume snapshot -r` | ✅ |
| `btrfs subvolume delete`（非空子卷） | ❌ EPERM |
| `btrfs property set -ts <p> ro false` | ✅ |
| `rmdir` 空子卷 | ✅（内核 ≥ 4.18） |
| `rm -rf` 只读快照内容 | ❌ Read-only file system |

因此**所有回收空间的命令**（`delete`、`prune`、apply 之后的保留策略、清理中断的
`.tmp-*`）在缺少该挂载选项时都会失败。实现的处置是**报错并给出补救方式**
（`subvolume_delete_denied`，退出码 4），而不是回退到"清空 ro 标志 + 递归删除"：

- 那条路径对**容器以 root 写入的数据目录**（NAS 的常态）依然会中途 EACCES；
- 中途失败会留下一个被删掉一半的快照——正是 `verify` 存在的意义所在的那种损坏状态，
  自己制造它比拒绝更糟。

补救：`mount -o remount,user_subvol_rm_allowed <fs>` 并写进 fstab，或以 root 执行回收
类命令。

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
  **实现位置与本文命名不一致**：配置里的 snapshot 段目前挂在 `rollback.snapshot`
  下（`backend` / `source` / `root` 已在那里），故实际写作
  `rollback.snapshot.keep_auto`。JSON 输出的字段名仍是 `keep_auto`，与本文一致。
  把整段提升为顶层 `snapshot:` 属于独立的配置改名，不在本期内。
  用 `*int` 而非 `int`：显式写 `keep_auto: 0`（一个都不留）必须与"没写"区分开，
  后者取默认值 5。
- `kind: manual` 与 `pinned: true` 一律不参与自动回收，**且不计入 N**。
- 回收时机：每次 `apply` 成功提交 active 之后，以及显式 `anas snapshot prune`。
- deployment GC 与快照**无耦合**：快照持有自己的制品副本（见上），`.anas/deployments/`
  的回收不会影响任何快照。

## 自动触发

`upgrade` 节点**已经存在**（[manifest.go:104](https://github.com/anas-project/ANAS/blob/master/internal/runner/manifest.go)），
本设计在其下新增 `data_breaking`，不另建顶层字段：

```yaml
upgrade:
  from: ">=30.0.0"           # 已有：允许的来源版本，不满足由 validateUpgrade 直接阻断
  data_breaking: ["31.0.0"]  # 新增：跨过即改写数据格式
```

两者语义不同，不能互相推导：`from` 说的是"能不能升"，`data_breaking` 说的是"升了能不
能回来"。一个 module 可以允许从任意版本升上来，而某次升级仍然改写了数据格式。

本期落地前**没有任何 module 声明 `upgrade:`**，两个字段都是纯新增；落地时 17 个 module
全部补上了 `data_breaking: []`，`from` 仍然一个都没有用到。

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

`≤ B` 而非 `< B`：升到断代版本本身就已改写格式。多个 module 同时升级时任一 breaking 即
整体 breaking——快照是 workspace 级的，一个就够。

判定对方向对称：区间 `(min, max]` 与谁是起点无关，升级与回滚走同一个比较。区别只在
调用方拿到答案之后做什么。

### 用哪一份声明（本文初稿未指明，实现按下述规则）

一次跃迁涉及两个版本，各自的 module.yml 都带着一份 `data_breaking`。**取版本较高的
那一份。**

只有"造成断代的那个 release"才可能知道自己断了代；较低版本的 module.yml 写于断代之前，
不可能提到它。若取低版本的声明，恰好在真正危险的跃迁上得到"兼容"。

因此升级方向取**目标**的声明，回滚方向取**当前已部署**的声明——两者都是"版本较高的
那一份"。实现上 `data_breaking` 被冻结进 `deployments/<id>/deployment.yml` 的
`modules.<name>.data_breaking`，判定读冻结值而非磁盘上当前的 module 包，这样已部署系统的
判定结果不会因为有人更新了 bundle 而改变。

### 无法解析一律降级为"未知"

版本号或声明条目解析失败时判为**未知**（阻断），不判为"兼容"。声明里的错误是 bug，
而安全声明里的 bug 的安全读法是"这道闸门可能本该生效"。`module.yml` 加载时即校验每个
条目是合法 semver（`loadModuleManifest`），所以这条只在冻结制品被手工改坏时才会走到。

### 未声明 ≠ 声明为空（必须区分，否则默认值反转）

若把"未声明 `data_breaking`"当作空列表，上式恒为假 → 判定"从不 breaking" → **所有
回滚放行**。而此前的行为是**默认全部阻断**，且当时 17 个 module 一个都没声明 `upgrade:`。
按空列表处理会把默认行为从最保守翻转成最宽松，是一个会静默生效的安全回归。

因此：

| 声明 | 含义 | 回滚判定 |
| --- | --- | --- |
| 不写 `data_breaking` | **未知** | 沿用现状：任何版本差异一律阻断 |
| `data_breaking: []` | 显式声明任何版本跃迁都不改写数据格式 | 放行 |
| `data_breaking: ["31.0.0"]` | 列出断代点 | 按上式判定 |

实现上必须用 `*[]string` 才能区分 `nil` 与空切片，不能用 `[]string`。

**发版前动作**：给每个 module 显式写 `data_breaking: []`。含义是"到当前版本为止没有断代
点"——一个可核实的事实陈述，不是对未来的承诺。若发版时全部留空不写，rollback 会因
"未知"而永远被阻断。

列表的维护规则（只增不改、何时可以修剪）见下面的"给 module 作者的规则"。

发布粒度使用 module 的 `(version, revision)`，不使用仅供展示的 `app_version`，与
`validateUpgrade` 保持一致。`version` 不同时按 SemVer 比较；`version` 相同时按整数
`revision` 比较。同一 version 的 revision 跃迁也属于发布变化：未声明
`data_breaking` 时仍按未知处理，显式 `[]` 才表示兼容。

### 升级方向

breaking → `apply` 前自动创建 `kind: auto` 快照。`--no-snapshot` 显式禁用，因为这是
在放弃唯一退路，需要 `-y`。

`apply` 为此新增三个标志：

| 标志 | 作用 |
| --- | --- |
| `--snapshot` | 无论是否触发都建一个，记 `reason: pre_apply` |
| `--no-snapshot` | 触发了也不建，需 `-y`；非 tty 无 `-y` 直接退出码 3 |
| `-y` / `--yes` | 确认上述两类需要确认的场景 |

`--snapshot` 与 `--no-snapshot` 同时给出是用法错误（退出码 2）。

### 回滚方向

`deploymentRollbackVersionBlockers`（[deployment.go:766](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go)）
现在把**任何**版本差异都判为 "data compatibility unknown" 一律阻断——连 patch 号变化
都不让回滚。该函数上方注释明说这是等待本契约的保守占位。改为三档：

**`rollback` 永不动数据。** 它只切换制品，没有 `--restore-data` 这个开关。需要回退
数据一律走 `anas snapshot restore`。

| 情形 | 结果 |
| --- | --- |
| 目标 deployment 版本**完全相同**（纯配置回滚） | ✅ 放行，数据不动（现状即如此） |
| 有版本变化，module 未声明 `data_breaking` | ❌ 阻断（`--allow-risky` 可绕）——现状 |
| 有版本变化，已声明且反向不跨断代点 | ✅ 放行 |
| 有版本变化，**反向跨断代点** | ❌ **直接报错，不提供绕过**，指向 snapshot restore |

第三档是本期唯一的行为放宽——把"任何版本差异都要 `--allow-risky`"缩小到"只有跨断代
点才要"。这是**体验改进而非安全性改进**，且以 module 作者正确声明为前提，故默认值必须
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

该错误的 `code` 为 `data_breaking_crossed`，退出码 4（precondition），与
`--allow-risky` 可绕的那一类（普通错误、退出码 1）在机器可读层面也区分得开。判定在
`--allow-risky` 之前执行，所以加了这个标志也绕不过去。

**module 新增/移除维持原样阻断**（`--allow-risky` 可绕）：新增的 module 在目标里没有对应
版本，移除的 module 数据留在磁盘上无人接管，两者都不是版本区间问题，`data_breaking`
无从判定。

### 由此简化掉的三处（**均已落地**）

1. **`rollback --restore-data` 标志删除**，`rollback` 语义变为纯粹的制品切换
2. **`restoreDeploymentSnapshot` 的跃迁配对校验删除**（连同 `restoreDeploymentSnapshot`
   与 `createDeploymentSnapshot`、`dataSnapshot` 一并删除）——该检查存在的唯一理由是
   快照绑定于特定跃迁；快照自足之后它就是一个**时间点**，恢复它无需参照
   `deployments/` 中的任何东西。`from_deployment` / `to_deployment` 保留为字段，但
   降级为供人阅读的上下文，不再参与任何判定
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

代码中已有一个"不可自动逆转"的 effect 集合。**直接复用，不另立标准**：deployment 侧
的那份已收进 `guardedSettingChanges`（[deployment.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go)），
apply 阻断与自动快照触发共读同一个函数，两者无从各自漂移；
`ensureNoGuardedChanges`（[config_state.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/config_state.go)）是同
一集合在 start 路径上的另一次使用。

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
- **module 新增**：无 `A`，不判 breaking（没有旧数据）
- **module 移除**：数据留在磁盘上，保守起见维持现有阻断
- `upgrade.from` 与 `data_breaking` **独立**：前者决定"能不能升"，后者决定"升了能不
  能回来"

### 给 module 作者的规则

**一句话：拿不准就标上。**

后果是不对称的：

| | 后果 | 方向 |
| --- | --- | --- |
| **漏标**（该标没标） | 升级前不建快照；回滚被放行，旧代码读不了新格式，**服务起不来** | 危险 |
| **多标**（不该标标了） | 多建一个快照；回滚被过度阻断，`--allow-risky` 可绕 | 安全 |

漏标的代价是一次无法挽回的数据事故，多标的代价是一次多余的确认。两者不在一个量级，
所以判断不清时按"标上"处理。

五条操作规则：

1. **发版前每个 module 都要显式写 `data_breaking: []`。** 不写等于"未知"，会让该 module
   的任何版本变化都无法回滚。`TestBundledModulesDeclareDataBreaking` 守着这一条。
   截至当前 17 个 module 全部声明为 `[]`——这是一个可核实的事实陈述（还没发过版，没有
   任何 release 改写过数据格式），不是对未来的承诺。
2. **断代点必须与造成它的版本号同一次提交写下。** 声明在渲染时被冻结进
   `deployment.yml`，判定读的是冻结值，所以**给一个已经部署出去的版本补标断代点，不会
   回头去保护那个已有的 deployment**——它冻结的是补标之前的空列表。补标只对此后重新
   渲染的 deployment 生效。

   历史实测（独立非生产环境，2026-07-31）：先以 `data_breaking: []` 部署 9.0.0，
   事后把 9.0.0 加进列表，
   从该 deployment 回滚到 2.5.1 **仍然放行**。改为"升到 9.0.1 的同时声明 9.0.1 断代"，
   回滚立即被拒。

   这是冻结语义的必然结果，而冻结是对的：另一种做法是判定时去读磁盘上当前的 module 包，
   那会让已部署系统的判定结果随着有人更新 bundle 而改变。代价是补标无效——所以规则是
   **同批提交**，不是"想起来再补"。

3. **已有条目只增不改。** 修改一条历史条目会改变**此后渲染的** deployment 的判定结果：
   昨天判定为可回滚的跃迁今天变成阻断，或者更糟，反过来。条目一旦发出去就是历史。
4. **不需要永久累积：提高 `upgrade.from` 时同步修剪。** `A` 的下界由 `upgrade.from`
   保证（`validateUpgrade` 拒绝任何不满足约束的来源版本），因此**任何
   `V ≤ upgrade.from 的下界` 的条目永远无法满足 `A < V`，是死条目**：

   ```yaml
   upgrade:
     from: ">=30.0.0"             # 低于 30 根本升不上来
     data_breaking: ["31.0.0"]    # 历史上的 21.0.0/25.0.0/28.0.0 已无法命中，删除
   ```

   **提高 `upgrade.from` 是唯一安全的删除时机**，因为约束本身已经把那些来源版本挡在
   门外了。任何其他时候的删除都是在放宽一条已经发出去的安全声明。
5. **`upgrade.from` 必须写成有明确下界的形式**（`>=X` 系）。`"!=31.0.0"` 这类没有下界，
   第 4 条无从执行。

（module 独立分发落地后可改为每个版本只声明一个布尔、由 runner 遍历 `A..B` 之间所有
版本判定。现在做不到是因为 runner 手中只有涉及的那两个版本的 module.yml。见
[module-distribution-draft.md](../../architecture/module-distribution-draft.md)。）

### 非 btrfs

无法建快照，breaking 升级需打印明确警告并要求 `-y`，不得静默继续。

`rollback.snapshot.backend` 未配置时，`anas lock` 探测工作区并把结果冻结到
`config.lock.yml`：可用时写 `btrfs` 与 `keep_auto: 5`，否则写 `none`。因此运行时
"建不了快照"有两种：lock 中 backend 为 `none`，或
`<workspace>/data` 后来不再是 btrfs subvolume。两者走同一条路径：把触发原因和无法建快照的
原因各打印一行到 stderr，然后要求确认（`-y`，或 tty 上的交互确认）。非 tty 且无 `-y`
时退出码 3。**没有任何一条路径会在触发条件成立时静默地不建快照。**

---

## `snapshot.yml` 字段

```yaml
api_version: anas.dev/snapshot/v1
id: 20260729T081504Z-4a1b2c3d
backend: btrfs
kind: auto              # auto | manual
pinned: false
created_at: 2026-07-29T08:15:04Z
reason: module_upgrade_breaking    # 枚举，见下
label: "升级前"                   # 用户自由文本，可空
source: /srv/anas/data
path: /srv/anas/snapshots/20260729T081504Z-4a1b2c3d/data
from_deployment: 20260728T041632Z-a9f9519d   # 可选，仅供人读，恢复时不参照
to_deployment: 20260728T131040Z-cd6fc061     # 同上
deployment_id: 20260728T131040Z-cd6fc061
config_digest: sha256:…
lock_digest: sha256:…
modules: { nextcloud: "30.0.1", authentik: "2024.10.5" }
artifact_copy: hardlink  # reflink | hardlink | copy，实际用到的降级档位
complete: true           # 最后写入；缺失即中断产物，不可恢复
```

两处与初稿的偏差，均已按现实修正：

- **不使用单值 `secret_generation`。** `secrets.yml` record 现在可带
  owner/kind/provenance/generation/rotation_id，但代次属于每个逻辑凭据，不存在能代表整个快照的
  一个标量。快照已复制该时刻完整 Secret Store；另写 `0` 或最大代次都会是假测量值，因此
  snapshot metadata 继续不出现该字段。若以后需要索引，应增加按逻辑 ID 的 generation map。
- **删掉 `recovery_path`。** 它描述的是"回滚时被挪开的原数据位置"。恢复前必建
  `reason: pre_restore` 快照之后，那份被挪开的数据已经在一个具名快照里，恢复成功后
  就地删除；再留一个无人知道含义的目录只会占盘。撤销一次恢复现在走
  `snapshot restore <pre_restore-id>`，返回的 JSON 里有它的 id。

### `reason` 枚举

| 值 | 触发场景 | 状态 |
| --- | --- | --- |
| `manual` | 用户执行 `anas snapshot create` | 已实现 |
| `pre_apply` | `apply` 切换 deployment 前的自动快照 | 已实现 |
| `pre_restore` | `snapshot restore` 执行前，为使恢复本身可撤销 | 已实现 |
| `pre_backup` | `anas backup create` 内部建的快照 | 预留 |
| `module_upgrade_breaking` | 跨过 `data_breaking` | 已实现 |
| `setting_data_migrate` | 变更了 `effect: data_migrate` **或 `credential_rotate`** 的设置 | 已实现 |

**`setting_data_migrate` 的名字比它的范围窄。** 它同时覆盖 `credential_rotate`：改回
config.yml 里的口令不会把服务内部（LDAP/DB）的口令改回去，这与数据迁移一样不可自动
逆转，一样需要退路。名字保留原样是因为枚举值是对外契约，为措辞改名要升
`api_version`，代价与收益不相称。

两者同时成立时记 `module_upgrade_breaking`：只建一个快照，取更严重的那个理由。

`pre_restore` 与 `pre_apply` 是本次补上的：初稿的枚举里没有它们，但正文既要求恢复前
建快照、`apply` 又早已在切换前建快照，两者都没有可写的 `reason`。

---

## `anas snapshot list`

```
anas snapshot list [--json]
```

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/srv/anas",
  "keep_auto": 5,
  "snapshots": [
    {
      "id": "20260729T081504Z-4a1b2c3d",
      "kind": "auto",
      "pinned": false,
      "created_at": "2026-07-29T08:15:04Z",
      "reason": "module_upgrade_breaking",
      "label": "",
      "deployment_id": "20260728T131040Z-cd6fc061",
      "complete": true,
      "config_matches_current": false,
      "size_bytes": null,
      "modules": { "nextcloud": "30.0.1" },
      "healthy": true
    }
  ]
}
```

- `size_bytes` 为 `null` 表示 btrfs qgroup 未启用，无法统计——**不要输出 `0`**。
  **一期恒为 `null`**：读 qgroup 要走 `btrfs qgroup show`，与
  `btrfs subvolume show` / `list` 同样需要 CAP_SYS_ADMIN 的 tree-search ioctl，
  而整个快照子系统刻意做到无 root 可用（见 `checkBtrfsSubvolume` 用 inode 256 判定
  subvolume 的理由）。为了一个体积数字把全部命令推到 root 之后，代价不对等。
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

- `saveAppliedConfig`（[config_state.go:70](https://github.com/anas-project/ANAS/blob/master/internal/runner/config_state.go)）
  写入 `state/config-applied.yml`，其中只有每个 setting 的 **sha256 哈希**
- 设计文档 §9.1 明确 release 不保存原始配置，只有脱敏的 `resolved.redacted.yml`
- `deployment.yml` 的 `Settings` 同样只是指纹

**因此本设计要求 `apply` 时把原始配置原文写入
`deployments/<id>/config.source.yml`（0600）。** 这不与 §9.1 冲突——那条限制的是不
拿它作**启动输入**（避免 config.yml 兼具期望状态与启动输入的双重身份），此处是一份
只读的**恢复用**副本，用途与命名都区分开。

这也不是新机制：legacy 的 `release/` 路径已经在做
（`copyFile(cfgPath, work/config.yml)`，[runner.go:369](https://github.com/anas-project/ANAS/blob/master/internal/runner/runner.go)），
只是 deployment 路径没有承接——服务器实测 `deployments/<id>/` 下只有 `modules`、
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
  "workspace": "/srv/anas",
  "restored_from": "20260729T081504Z-4a1b2c3d",
  "pre_restore_snapshot": "20260730T093012Z-7f8e9a0b",
  "restored": ["config", "lock", "secrets", "state", "deployment", "data"],
  "deployment_id": "20260728T131040Z-cd6fc061",
  "next_steps": ["anas start -w /srv/anas"]
}
```

## `anas snapshot path <id>`

打印快照的只读数据路径并退出。btrfs 快照本身就是可读目录，用户想捞一个误删的文件
不该被迫整体回滚。

```json
{ "api_version": "anas.dev/cli/v1", "ok": true, "id": "…", "path": "/srv/anas/snapshots/…/data" }
```

## 二期

`anas snapshot diff <id>` —— 对比快照与当前的 module 版本、配置差异，回滚前知道会丢
什么。有价值但不阻塞一期。

## 覆盖范围（coverage）

工作区有两棵独立的 btrfs subvolume，快照分别对待：

| 树 | 内容 | 快照默认 | restore 默认 |
|---|---|---|---|
| `<workspace>/data` | 应用状态（数据库、AD 库、证书） | **总是包含** | **总是还原** |
| `<workspace>/userdata` | 用户自己存的文件 | **不包含** | **不还原** |

分开的理由是正确性而非整洁：restore 会整体替换 `data/`，用户文件若在里面，**每次部署回滚都会删掉快照之后保存的文件**——那些文件和被回滚的部署毫无关系。

`snapshot.yml` 用 `coverage` 记录每棵树捕获与否，未捕获时给出原因：

```yaml
coverage:
    - tree: data
      path: /data/ws/data
      captured: true
    - tree: userdata
      path: /data/ws/userdata
      captured: false
      reason: excluded
```

`reason` 取值：`excluded`（自动快照，或手动快照未加 `--include-userdata`）、`not_a_subvolume`（userdata 不在 btrfs 上，无法快照）、`missing`（工作区没有这棵树）。

没有这条记录，快照会照常标记 `complete`、restore 会照常报告成功，而工作区里最大的一棵树原封未动，盘上没有任何东西说明这件事。

命令层面：

- `anas snapshot create [--include-userdata]` —— 默认不含
- `anas snapshot restore <id> [--restore-userdata]` —— 默认不还原；交互式终端会额外问一次；`-y` 走默认值（不还原），因为它的意思是"别问我"而不是"做更狠的那个"
- 自动的 pre-apply 快照**永不包含** userdata
- `anas backup create [--skip-userdata]` —— **默认包含**，方向与快照相反：备份是为了盘挂了还能回来，用户文件是唯一 redeploy 补不回来的部分
