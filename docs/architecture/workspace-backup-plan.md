# workspace 与备份体系：实施计划

> [!NOTE]
> 这是已实施功能的设计与迁移记录，正文中的“当前”指制定计划时的历史基线。现行目录、命令和恢复语义以[备份与恢复指南](/guide/backup-and-restore)及[备份契约](/reference/contracts/backup)为准。

> 状态：一（workspace 语义）、二（`anas init`）、三（snapshot 体系）、
> 四（breaking 升级自动快照）、五（backup 体系）已落地。本文是总纲，JSON 契约见
> [CLI 契约](../reference/contracts/)，module 分发见
> [module-distribution-draft.md](module-distribution-draft.md)（草案，不在本计划内）。

## 目标

用户备份时**只需备份一个目录**，且恢复具备幂等性——不会因为配置文件、密钥或运行时
状态没被覆盖到而导致恢复出一个跑不起来的系统。

## 一、workspace 语义

### 目录布局

```text
<workspace>/
  config.yml              # 用户期望状态，唯一手工维护。放明面，不藏进 .anas
  config.lock.yml         # projectLockPath 按 config 同目录推导，无需改
  data/                   # 固定位置；btrfs 上建为 subvolume（快照源的硬性要求）
  snapshots/              # 普通目录，0700；快照作为 subvolume 建在它下面
  .anas/                  # 即今日的 base，0700
    state/  deployments/  staging/  hook-bin/  go-build-cache/
    secrets.yml
```

workspace 自身与 `.anas/` **保持普通目录，不做 subvolume**。理由是实测数据：
`.anas/` 共 404M，其中真正不可重建的只有 `state/`(44K) + `secrets.yml`(8K)
约 52K，其余是 `deployments/`(249M)、`go-build-cache/`(68M)、`hook-bin/`(45M)、
`staging/`(42M)。btrfs 快照不可过滤，做成 subvolume 会把 400M 缓存一并固化；52K 的
文件级复制在已持有的 `state/lock` 排他锁下即可保证一致。

### 解析顺序

```
-w / --workspace  →  ANAS_WORKSPACE  →  当前目录（仅当 ./.anas/ 存在）  →  报错
```

- 不向上逐级查找。
- 所有改状态的命令（`apply`/`start`/`stop`/`restart`/`rollback`/`snapshot`/`backup`）
  动手前打印一行 `workspace: <绝对路径>`。
- **`rollback` 与 `backup restore` 必须显式 `-w`**，不接受环境变量，不接受 cwd。
  这是唯一会破坏性替换数据的操作，而环境变量恰恰是最容易过期、指错地方的来源。

### 移除 `data_path`

`global.data_path` 从配置中删除，data 恒等于 `<workspace>/data`。

- 需要把数据放大盘的用户，**把整个 workspace 放到大盘上**——`.anas/` 只有几十 K 的
  权威状态，没有理由必须待在系统盘。
- 从 [config_cli.go:197](https://github.com/anas-project/ANAS/blob/master/internal/runner/config_cli.go) 的可 `config set` 列表移除。
- **明确禁止 `<w>/data` 是符号链接或挂载点**，`anas init` 主动检测并拒绝：symlink 会
  让 tar/rsync 默认不跟随、备份静默变空；挂载点会让 `restoreDeploymentSnapshot` 的
  `os.Rename(source, recovery)`（[deployment.go:934](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go)）
  返回 EBUSY。

### 移除 `-b / --base`

不保留逃生舱。它是唯一能让 workspace 不自包含的口子，既然还没发版就不留后门。

`deploymentAPIVersion` **不升级**——尚未发版，没有需要区分的旧格式。

### 改动点

| 位置 | 改动 |
| --- | --- |
| [deployment.go:117](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go)、[runner.go:127](https://github.com/anas-project/ANAS/blob/master/internal/runner/runner.go)、[runner.go:166](https://github.com/anas-project/ANAS/blob/master/internal/runner/runner.go) | 三处重复的 `~/.anas` 默认值收敛成一个 `resolveWorkspace()` |
| [runner.go:107](https://github.com/anas-project/ANAS/blob/master/internal/runner/runner.go)、[runner.go:150](https://github.com/anas-project/ANAS/blob/master/internal/runner/runner.go)、[deployment.go:128](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go)、[deployment.go:541](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go)、[deployment.go:1040](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go) | 五个 flag 解析点：加 `-w`，去 `-b` |
| runner 内 env 加载后 | data 路径由 workspace 推导，`config` 包不需要知道 workspace |
| [deployment.go:423](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go) | `DataRoot` 不再需要存储，由 workspace 推导 |
| [runner.go:86-98](https://github.com/anas-project/ANAS/blob/master/internal/runner/runner.go) | `usage()` 十三行帮助文本全部写着 `[-b ~/.anas]`，逐行改为 `[-w <workspace>]` |
| [deployment.go:1111](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go) | `ensureRuntimeLayout` 现在把 `snapshots` 建在 `base` 内。**目录布局属于一期**：`snapshots/` 移到 `<workspace>/snapshots`，与 `.anas` 平级。留到二期做等于让用户迁移两次目录 |
| [runtime-release-state-design.md:134](runtime-release-state-design.md)、§13 | §4「推荐运行目录」画的仍是 `~/.anas/` 树，§13 备份单元仍是五项清单，两处均与本计划冲突，随代码同步更新 |

## 二、`anas init`

```
anas init [路径] [--shell-init] [-y]
```

职责：建**最终形态**的目录树（含 `snapshots/`，尽管二期之前无人写入）、写 `config.yml`
骨架、btrfs 上把 `data/` 建为 subvolume、检测并拒绝挂载点/符号链接。`snapshots/` 是
普通目录——只有快照的**源**必须是 subvolume。

一期就确立完整布局是为了**只迁移一次**：`snapshots/` 今天在 `.anas` 内
（`ensureRuntimeLayout`），新设计里在 workspace 根下。若留到二期再移，用户要迁两回。

### btrfs 检测

用 `unix.Statfs` 读 magic（btrfs = `0x9123683E`），不 shell out。非 btrfs 时提示要
说清损失并要求确认（`-y` 跳过）：

```
警告：<workspace> 位于 ext4，不是 btrfs。
      快照与回滚数据恢复将不可用。
      升级 module 时不会自动创建可回退的数据快照。
      备份仍然可用，但只能是整目录复制模式。
继续初始化？[y/N]
```

仅"在 btrfs 上"不够，还要检查 `data/` 是 subvolume——复用已有的 `btrfsSubvolumeShow`
（[deployment.go:865](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go)）。否则用户会以为有快照能力，
到升级那天才发现。

### shell 环境变量：默认只打印，不写文件

默认在结尾打印可复制的 export 语句（按检测到的 shell 给对语法），**不修改任何文件**。

不默认写入的理由：`ANAS_WORKSPACE` 是机器级全局的，一旦写进 rc，无论 `cd` 到哪个
workspace 目录，解析结果都固定——正好重新引入 cwd 解析要防的"在错地方跑对命令"。
其次 rc 不对 cron / systemd 生效。

`--shell-init` 才写，且必须满足：

| 要求 | 做法 |
| --- | --- |
| shell 检测 | `$SHELL` basename；bash 在 macOS 写 `~/.bash_profile`、Linux 写 `~/.bashrc`；zsh → `~/.zshrc`；fish → `~/.config/fish/config.fish` 且语法为 `set -gx`；识别不出则只打印并非零退出 |
| 幂等 | `# >>> anas workspace >>>` / `# <<< anas workspace <<<` 标记块，重复执行替换而非追加 |
| 不静默覆盖 | 已存在指向别的 workspace 的块时，打印新旧值并要求 `-y` 或交互确认 |
| 可见 | 写完打印文件路径与生效方式 |
| 可撤销 | `--shell-init=remove` 删除标记块 |

## 三、snapshot 体系（已落地）

完整契约见 [snapshot 契约](../reference/contracts/snapshot.md)，实现见
[snapshot.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/snapshot.go)、
[snapshot_restore.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/snapshot_restore.go)、
[snapshot_cli.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/snapshot_cli.go)。要点：

- **概念分界**：snapshot = 本机、瞬时、为回滚服务；backup = 异地、完整、为灾难恢复
  服务。因此 `snapshots/` 默认不进 backup。
- **快照自足**：内含 config、lock、secrets、必要 state、`deployment/` 完整副本、data
  只读快照。仅凭快照即可恢复系统（上游基础镜像除外）。
- **`deployment/` 复制**按 reflink → 硬链接 → 完整复制降级。`snapshots/` 保持普通
  目录（非 subvolume），无跨 subvolume 的 EXDEV 限制。但**硬链接一档依赖制品封印，
  见下**。
- **制品封印**（前置任务，已落地）：`sealDeployment` 在 rename 进
  `deployments/<id>/` 之前按位清除写权限，因此三档降级链全部可用。按位清除而非赋固定
  模式，是为了让可执行文件仍可执行、`.env` 仍是 0600 → 0400 而不是被"只读"顺手放宽成
  全局可读。目录保持 0700 不封——只读目录会连 unlink 一起挡住，而 unlink-and-replace
  分配新 inode，本就是硬链接安全的改动。
- **`apply` 把原始配置写入 `deployments/<id>/config.source.yml`（0600）**（前置任务，
  已落地）。此前原始 config 原文在系统里没有任何副本：`saveAppliedConfig` 只存 sha256
  指纹，release 只有脱敏的 `resolved.redacted.yml`，manifest 只有 Settings 指纹。指纹
  能**检测**磁盘 config 与 applied 不符，但产不出那份旧配置。没有它，快照拿不到与自己
  匹配的配置，"仅凭快照恢复系统"不成立。
- **换来的简化**：deployment GC 与快照完全解耦，不再需要"pinned 快照连带 pin 住
  deployment"这条跨子系统不变量。
- **`state/` 只拷不可重建的部分**，`active.yml` 恢复时由快照的 `deployment_id` 重建。
- **保留策略** `snapshot.keep_auto` 默认 5；`manual` 与 `pinned` 不计入、不回收。
- **`deploymentState.SnapshotID` 删除**——单数字段撑不住多快照，且制造双写不一致窗口；
  该查询用 `from_deployment` / `to_deployment` 即可完成。
- **创建原子**：`.tmp-<id>` → rename，`complete: true` 最后写。
- **恢复全有或全无**：secret store 分代追加，不允许"只恢复 meta 保留当前 data"。

- **`rollback` 永不动数据**，没有 `--restore-data`。回退数据一律走
  `anas snapshot restore`。由此连带删除 `restoreDeploymentSnapshot` 的跃迁配对校验
  （[deployment.go:925](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go)）——快照自足之后它就是一个
  时间点，恢复它无需参照 `deployments/`。

命令：`list` / `show` / `create` / `restore` / `pin` / `unpin` / `delete` / `prune` /
`verify` / `path`。`diff` 列入二期。

## 四、breaking 升级自动快照（已落地）

`upgrade` 节点**已存在**（[manifest.go:104](https://github.com/anas-project/ANAS/blob/master/internal/runner/manifest.go)），本期在
其下新增 `data_breaking`，不另建顶层字段：

```yaml
upgrade:
  from: ">=30.0.0"           # 已有：允许的来源版本，validateUpgrade 直接阻断
  data_breaking: ["31.0.0"]  # 新增：跨过即改写数据格式
```

两者语义不同、不可互推：`from` 说"能不能升"，`data_breaking` 说"升了能不能回来"。
截至目前没有任何 module 声明 `upgrade:`，两个字段都是纯新增。

设旧版 `A`、新版 `B`，存在 `V ∈ data_breaking` 使 `A < V ≤ B` 即为 breaking。

- **升级方向**：`apply` 前自动建 `kind: auto` 快照。`--no-snapshot` 需 `-y`（这是在
  放弃唯一退路）。
- **回滚方向**：`deploymentRollbackVersionBlockers`
  （[deployment.go:766](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go)）改为只在跨过 breaking 版本
  时阻断。该函数现在把**任何**版本变化都判为 "data compatibility unknown"，其上方
  注释明说这是等待契约的保守占位——本期正好补上。
- **配置变更触发**复用代码中已有的"不可自动逆转" effect 集合（[config_state.go:51](https://github.com/anas-project/ANAS/blob/master/internal/runner/config_state.go)、
  [deployment.go:754](https://github.com/anas-project/ANAS/blob/master/internal/runner/deployment.go) 三处一致使用）：
  `data_migrate`（7 处）与 `credential_rotate`（10 处）触发快照，`reconcile` 及各类
  重启型 effect 不触发。分工：这两者由**配置变更**触发，`data_breaking` 由**版本升级**
  触发。
- **不对"每次 apply 都建快照"**：`keep_auto` 槽位会被例行 apply 填满，真正要命的
  pre-breaking 快照反而被挤掉。强制建用显式 `--snapshot`。
- **回滚判定**：版本相同 → 放行；有版本变化且未声明 `data_breaking` → 阻断（现状）；
  已声明且不跨断代点 → 放行；**跨断代点 → 直接报错，不提供 `--allow-risky` 绕过**，
  错误信息指向 `anas snapshot restore`。跨断代点意味着旧代码确定读不了新格式数据，
  放行只会让服务起不来。
- **`data_breaking` 列表不需永久累积**：`A` 的下界由 `upgrade.from` 保证，`V ≤ 该下界`
  的条目是死条目。提高 `upgrade.from` 时同步修剪，已有条目只增不改。
- **发版前动作**：给每个 module 显式写 `data_breaking: []`，否则"未知"会让 rollback 永远
  被阻断。
- **`data_breaking` 未声明 ≠ 声明为空。** 若把未声明当空列表，判定恒为"不 breaking"
  → 所有回滚放行，而现状是默认全阻断、且 16 个 module 一个都没声明 `upgrade:`——那是
  一个会静默生效的安全回归。实现必须用 `*[]string` 区分 `nil` 与 `[]`。
- **不取消"不带数据的制品回滚"。** 它与快照恢复解决不同问题：配置改错但数据完好是最
  常见的回滚场景，强制走快照恢复会丢掉自上次 apply 以来的全部用户数据。
- 非 btrfs 无法建快照：breaking 升级需打印警告并要求 `-y`。

## 五、backup 体系（已落地）

完整契约见 [backup 契约](../reference/contracts/backup.md)，实现见
[backup.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup.go) 及同前缀的各文件，端到端测试见
[test-backup.sh](https://github.com/anas-project/ANAS/blob/master/test-env/scripts/test-backup.sh)。要点：

- **备份单元就是快照**。`backup create` 不带 `--snapshot` 时先建一个
  `reason: pre_backup` 的快照再发送，因此不存在第二套 include/exclude 规则。
- **四种模式**由「源是否 btrfs」×「目标是否 btrfs / 是否同一 fs」决定：`snapshot` /
  `send` / `send-file` / `copy`。判定"同一文件系统"用 **btrfs fsid**，不是 `st_dev`。
- **send 类模式需要两条传输通道**：`btrfs send` 只能发送 subvolume，而快照里只有
  `data/` 是 subvolume；`snapshot.yml` / `meta/` / `deployment/` 必须另走 tar/rsync。
  两条都完成才写目标端完成标记。
- **`backup capabilities`** 探测并返回各模式可用性与原因，**交互式模式内部就调它**，
  只把可用选项列给用户；web 层用同一份 JSON 自行渲染。一套逻辑，不写两份。
- **`backup plan`** 输出动作清单不执行，是 web 端确认页的数据源；`create` 内部第一步
  就跑它。
- **停机顺序**：`start_containers` 排在 `send_stream` **之前**。容器只需在建快照期间
  停机，send 从只读快照读取。这把停机时间从"数据体积决定"压到"快照耗时决定"。
- **失败必须恢复服务**。用 `.anas/state/transactions/` 记事务，崩溃后下次任意 anas
  命令启动时检测并补偿。备份失败把服务留在停机状态是本功能唯一不可接受的失效方式。
- **明文密钥警告**：往非本机目标写时强制打印 `plaintext_secrets_leaving_host`。
  `--encrypt` 二期。
- **落地时修正的四处**（详见契约中的「与初稿的偏差」各节）：`statfs` 的 `f_fsid`
  混入了 subvolume objectid，不能用作文件系统标识，改从
  `/sys/fs/btrfs/<uuid>/devices/` 读 UUID；`copy` 模式除"目标可写"外还需要能读全
  `data/`，而容器以 root 写下的数据普通用户读不到，故 `capabilities` 增加
  `source.data_fully_readable` 并据此报 `insufficient_privilege`；目标端布局改为
  每备份一个目录，中断产物带 `.tmp-` 前缀、靠 rename 发布；`recommended` 把
  `snapshot` 排在最后，因为它把副本放在要防的那块盘上。

## 六、非交互契约收口

见 [CLI 契约](../reference/contracts/)：结构化结果走 stdout 且只有一个 JSON
文档；进度与日志走 stderr（JSON Lines）；`-y` 是唯一确认绕过方式，**未给 `-y` 且
stdin 非 tty 时以退出码 3 立即失败，绝不阻塞等待**；退出码 0/1/2/3/4 分级；所有
`code` / `reason` 是固定枚举而非文案；时间 RFC3339 UTC、大小一律字节整数。

---

## 七、空间回收需要特权，以及为什么现在不做守护进程

### 实测（loopback 镜像，内核 6.8，非特权用户，快照内含容器以 root 写入的 0600 文件）

| 操作 | 默认挂载 | `user_subvol_rm_allowed` |
| --- | --- | --- |
| `rm -rf` 快照内容 | 拒绝 | 拒绝 |
| 清除 ro 标志 | 允许 | 允许 |
| `btrfs subvolume delete` | 拒绝 | **允许** |

三条结论：

1. **只读快照删除失败的原因是 "Read-only file system"，不是权限。** 清 ro 标志非特权即可，
   所以存在两道独立闸门。**即使加了挂载选项，不先清 ro 也仍然失败**——目前代码没有清。
2. **`rm -rf` 在两种挂载下都失败**，因为容器以 root 写入了文件。这与 btrfs 无关。
3. `btrfs subvolume delete` **绕过逐文件权限检查**，这既是它有效的原因，也是该挂载选项属于
   安全开关的原因。

因此真正的约束不是 btrfs，而是**容器数据归 root**：回收任何一份容器数据的副本都需要特权，
用不用快照都一样。以下替代方案均不成立——reflink 文件级复制（里面仍是 root 文件）、
把快照建成读写（默认挂载下读写子卷同样删不掉）、清 ro 后 `rm -rf`（死在第一个 root 文件上，
留下半删状态）。

该挂载选项还**无法按子卷收窄**：实测把某个子卷单独挂到第二个挂载点并带上该选项，第一个
挂载点也会随之带上——它是 superblock 级的。

### 结论：回收是显式的特权操作

anas 保持应用级，不要求任何宿主挂载改动。回收与备份本就是定时任务而非交互操作，交给
systemd 以 root 执行即可，同时解决"要特权"和"要定时"：

```
anas-prune.timer   → 以 root 执行 anas snapshot prune
anas-backup.timer  → 以 root 执行 anas backup create
```

提权应只包住 `btrfs subvolume delete` 那一个调用，而不是让用户 `sudo anas ...` —— 整个
进程以 root 运行会往 `.anas/state/` 写入 root 拥有的文件。

挂载选项降级为可选便利，写进文档而非要求，并注明必须同时清 ro 标志。

### 守护进程：推迟到 web 服务之后

考虑过 Docker 式的常驻 root 守护进程 + anas 作为控制客户端。**推迟**，理由：

- **它并不引入新的权限等级。** anas 今天已经等价于 root：运行它的用户在 `docker` 组里，
  而 docker 组成员可以挂载整个宿主文件系统进容器。反对"守护进程要 root"的最大一条理由
  本就不成立。
- **但它独有的价值只剩一条。** 定时用 systemd timer；容器拉起用 Docker 的 restart policy；
  证书续期 lego 容器里的 cron 已在做。剩下无可替代的只有"给 web 服务提供实时状态与 API"。
- **代价是状态权威分裂。** 本设计建立在"磁盘文件是唯一权威"之上（见
  [runtime-release-state-design.md](runtime-release-state-design.md) §3），常驻进程天然
  倾向于在内存里攒状态并与磁盘漂移。
- **业界在往反方向走**：Docker 的 root 守护进程是长期被批评的设计，podman 正是为去掉它
  而生。"docker 组等于 root"是公认的坑，本项目刚刚验证过。

**方向**：等 web 服务落地时，**让 web 服务本身充当那个常驻进程**，而不是在它下面再垫一层
守护进程。它以 root 运行、持有同一把 `state/lock`、读写同一批文件；[CLI 契约](../reference/contracts/)
的 JSON 契约就是它的调用接口。少一层，少一个需要保持同步的真相源。

若日后仍决定单独做守护进程，两条约束不可放弃：它不得成为第二个真相源（决策必须重读磁盘、
走同一把锁，且 CLI 在守护进程未运行时仍能独立工作）；API 必须区分只读查询与变更操作，
否则就是复制了 docker 组那个坑。

## 分期

| 期 | 内容 | 交付后达成 |
| --- | --- | --- |
| 一 | 第一、二节：workspace 语义、`anas init`、移除 `data_path`、砍掉 `-b`、`usage()` 与 `ensureRuntimeLayout` 同步、老设计文档同步。**含 R1、R10 两个回退测试** | **备份契约成立**（一个目录搞定，配置不会漏）。此时可用 tar/rsync 手工备份，snapshot/backup 命令只是让它更省事 |
| 二 | 第三节：snapshot 目录重构、元数据、全套子命令、保留策略。**含两个前置任务**：制品封印（§13 已规定未实现）、`apply` 写出 `config.source.yml` | 可回滚到任意历史数据点 |
| 三 | 第四节：`data_breaking_versions` 声明、自动触发、放宽回滚阻断 | 升级有自动退路，且不再过度保守 |
| 四 | 第五节：backup 四种模式、停机事务、交互式 | 异地灾难恢复 |
| 五 | 第六节：`--json` / 退出码 / 进度输出统一收口 | web 服务可直接调用 |

第五期可与第四期合并。[module 分发](module-distribution-draft.md)不在本计划内。

## 回退与快照测试

### 测试机

**btrfs 测试在 `ssh whl@ln.hlong.wang -p 2200` 上做**，`/data` 是真实 btrfs（3.7T、
双设备），不需要 loopback。实测能力：

| 操作 | 结果 |
| --- | --- |
| `btrfs subvolume create` / `snapshot` / `delete` | ✅ 无需 sudo |
| `cp --reflink=always`（`/data` 内） | ✅ |
| `btrfs send` | ❌ **需要 root**（`Operation not permitted`） |
| `go` | ❌ **未安装，需补** |

两条前置：

1. **ln 必须装 Go。** `freezeHookBinary` 让 deployment 不依赖工具链，但 render 阶段仍
   要 `go build`；回退测试需要两次真实 apply，绕不过去。
2. **四期的 send 模式测试需要免密 sudo 规则。** 一到三期（快照本身）不需要。

`/data` 下已有 `anas-refactor-test`、`anas-docker-test` 等旧目录，新测试用独立路径。

macOS（APFS）与 finance（ext4）都跳过 btrfs 相关用例。**跳过项必须在 `test-all.sh`
汇总中明确列出**，不得静默通过——否则本机全绿、服务器上才炸。

不为 APFS 实现第二套快照后端：APFS 快照是**卷级**而非目录级，没有 `data/` 那种目录
subvolume 概念；挂载快照只能只读，做不到 `snapshot + rename` 的原地替换；且没有
`send`/`receive` 等价物，四种备份模式有两种无法实现。加之 Docker Desktop 在 macOS 上
经 VirtioFS 做 bind mount，文件并不在快照逻辑假设的原生路径语义上。anas 的部署目标是
Linux NAS，macOS 只是开发机。

### 不沿用"替换 lock 文件"的伪造手法

现有 `test-upgrade.sh` 通过覆盖 `config.lock.yml` 伪造旧版本。回退测试**不采用**这种
做法：回退需要真实存在两个 deployment 目录、真实的 active 指针、以及真实快照，必须走
完整的 `apply → apply → rollback` 序列。

因此需要两个真实 module 版本的用例（R4、R5）**推迟到发版后**再写。

### 用例矩阵

判定逻辑是纯函数，**R2~R5 走表驱动单元测试**放进 `deployment_test.go`——组合多、跑得
快、不依赖 Docker。端到端只保留真正需要真实环境的。

| # | 场景 | 期望 | 形式 | 需 btrfs | 时机 |
| --- | --- | --- | --- | --- | --- |
| R1 | 纯配置变更后回滚（版本不变） | 放行，制品退回，**数据 marker 仍在** | 端到端 | ❌ | **一期** |
| R2 | 版本变化，未声明 `data_breaking` | 阻断，退出码 4 | 单元 | ❌ | 三期 |
| R3 | 同 R2 + `--allow-risky` | 放行 | 单元 | ❌ | 三期 |
| R4 | 版本变化，已声明且不跨断代点 | 放行，数据不动 | 单元 + 端到端 | ❌ | 单元三期／端到端发版后 |
| R5 | 版本变化，反向跨断代点 | 直接报错并指向 snapshot restore | 单元 + 端到端 | ❌ | 同上 |
| R6 | 配置含 `data_migrate` 变更 | apply 前自动建快照；回滚被阻断 | 端到端 | ✅ | 三期 |
| R7 | 配置含 `credential_rotate` 变更 | 同 R6 | 端到端 | ✅ | 三期 |
| R8 | `snapshot restore` 恢复到 R6 的快照 | 数据、制品、config 三者一致回退 | 端到端 | ✅ | 二期 |
| R9 | 手工删除快照后 `snapshot verify` | 报 `subvolume_missing` | 端到端 | ✅ | 二期 |
| R10 | rollback 中途失败 | 服务恢复原状态，不留半停机 | 端到端 | ❌ | 一期 |

**R1 是一期的交付项，不是可选项。** 前面关于"不能取消制品回滚"的全部论证都依赖"制品
回滚不丢数据"这一条保证，而它目前在代码里没有任何测试锁着。用
`test-upgrade-probes.sh` 已有的 marker 机制即可断言。

R10 同样在一期：备份和回滚失败后把服务留在停机状态，是这两个功能唯一不可接受的失效
方式。

### 脚本

| 脚本 | 覆盖 |
| --- | --- |
| `test-env/scripts/test-rollback.sh` | R1、R10（无需 btrfs） |
| `test-env/scripts/test-snapshot.sh` | R6~R9（需 btrfs，仅 ln） |
| `test-env/scripts/test-backup.sh` | B1~B8（需 btrfs，仅 ln；B8 需 root） |

`test-backup.sh` **要跑两遍**，普通用户一遍、root 一遍：`btrfs send` 需要
`CAP_SYS_ADMIN`，普通用户那遍无法覆盖 send 与 send-file，跳过信息同时打到 stdout
和 stderr——静默跳过会让套件在最需要它的那台机器上变绿。

判定逻辑的单元测试进 `internal/runner/deployment_test.go`，随 `test-static.sh` 跑。

## 验证环境的缺口

**finance 上 `/` 是 ext4（`/dev/sda2`），reflink 不支持。** 二到四期的 btrfs 路径在这台
机器上跑不起来。分工：

- **finance** —— 一期（workspace 语义、`anas init` 的非 btrfs 分支）、R1、R10
- **ln.hlong.wang:2200** —— 二期起的全部 btrfs 用例（`/data` 是真实 btrfs），前提是
  先装 Go
- **本机 macOS** —— 仅单元测试与静态检查，btrfs 用例全部跳过并在汇总中列出

## 迁移

finance 现状 `-b /home/whl/anas-deploy/runtime` + `data_path:
/home/whl/anas-deploy/data`，父目录已经是正确形状：

```bash
mv /home/whl/anas-deploy/runtime /home/whl/anas-deploy/.anas
```

`config.yml` 与 `config.lock.yml` 本就在根上，`data/` 也在。之后 workspace 即
`/home/whl/anas-deploy`，所有脚本里的 `-b` 参数删除。
