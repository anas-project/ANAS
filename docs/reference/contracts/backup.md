# backup 命令 JSON 契约

> 状态：**已实现**（`capabilities` / `plan` / `create` / `list` / `restore` /
> `verify`，以及无子命令时的交互式表单）。实现见
> [backup.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup.go)、
> [backup_create.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup_create.go)、
> [backup_transfer.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup_transfer.go)、
> [backup_txn.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup_txn.go)、
> [backup_restore.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup_restore.go)、
> [backup_cli.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/backup_cli.go)。
> 通用约定（流分离、退出码、枚举、时间与大小格式）见
> [通用约定](index.md)，本文不再重复。
>
> 落地时发现初稿四处与现实不符，均已按现实修正，见文中「与初稿的偏差」各节。
> 其中「copy 模式也需要权限」与「f_fsid 不是文件系统标识」两条，按初稿实现会分别
> 产出**静默残缺的备份**和**永远选不中 snapshot 模式**。

## 概念分界

- **snapshot** = 本机、瞬时、为回滚服务。见 [snapshot.md](snapshot.md)。
- **backup** = 异地、完整、为灾难恢复服务。

因此 `snapshots/` 目录**默认不进 backup**——它是同一块盘上的 CoW 引用，复制到别处
既巨大又无意义。备份要带走某个快照时是显式地 `send` 它。

## 备份单元就是快照

**backup 不自己定义备份内容——它备份的是一个快照。** 快照按
[snapshot.md](snapshot.md) 的定义已经自足（config、lock、secrets、必要 state、
可运行制品、数据），所以 backup 的职责只剩"把它安全地送到别处"。

`backup create` 不带 `--snapshot` 时，内部先建一个 `reason: pre_backup` 的快照，
再发送它。因此备份内容永远等于快照内容，不存在第二套 include/exclude 规则。

在 btrfs 之外没有快照能力，此时 `copy` 模式直接复制 workspace，按下表取舍：

| 类别 | 内容 | copy 模式是否包含 |
| --- | --- | --- |
| 权威状态 | `config.yml`、`config.lock.yml`、`.anas/state/`、`.anas/secrets.yml` | ✅ |
| 业务数据 | `data/` | ✅ |
| active 制品 | `.anas/deployments/<active-id>/` | ✅ |
| 历史制品 | `.anas/deployments/` 下其余目录 | ❌ |
| 缓存 | `.anas/go-build-cache/`、`.anas/hook-bin/`、`.anas/staging/` | ❌ 永不备份 |

active 制品必须包含，理由有三条，每一条单独都足够：

1. 它是**镜像构建上下文**（compose 里写的是 `build: context: ./${MODULE_NAME}`），
   没有它连 `docker compose build` 都跑不了；
2. 它是 workspace 内**唯一一份可运行的 module 副本**——module 源树在 workspace 之外
   （`locateModuleRoot` 按 `ANAS_MODULE_ROOT`/cwd/可执行文件寻找 module bundle）；
3. 它携带**冻结的 hook 二进制**（`<module>/.hook.bin`），`freezeHookBinary` 在写入它的
   同时删除了 `hook/` 源码目录，目的正是"无需 Go 工具链即可运行"。`.anas/hook-bin/`
   只是构建缓存，权威副本在这里。

体量上 active 制品在一次非生产环境测量中约占 data 的 3%（42M vs 1.3G），其中 41M 是 13 个
冻结 hook 二进制。

**恢复仍需从 registry 拉取上游基础镜像**，任何模式都不例外。完全离线恢复属二期
`--include-images` 范围。

---

## `anas backup capabilities`

探测源与目标，返回每种备份模式是否可用。**交互式模式内部调用它，只把
`available: true` 的选项列给用户**；web 层用同一份输出自行渲染。

```
anas backup capabilities [--to <dest>] [--json]
```

`--to` 省略时只探测源，所有依赖目标的模式返回 `available: false` 且
`reason: "dest_not_specified"`。

### 输出

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/srv/anas",
  "source": {
    "fstype": "btrfs",
    "fsid": "3f2a1c8e-...",
    "data_is_subvolume": true,
    "data_is_mountpoint": false,
    "data_fully_readable": false
  },
  "dest": {
    "path": "/mnt/backup",
    "exists": true,
    "writable": true,
    "fstype": "ext4",
    "fsid": "9b41e0aa-...",
    "free_bytes": 900000000000
  },
  "tools": { "btrfs": true, "rsync": true },
  "privileged": true,
  "estimate": {
    "data_bytes": 1395864371,
    "state_bytes": 53248,
    "active_deployment_bytes": 44040192,
    "total_bytes": 1439957811
  },
  "modes": [
    { "id": "snapshot",  "available": false, "reason": "dest_not_same_filesystem" },
    { "id": "send",      "available": false, "reason": "dest_not_btrfs" },
    { "id": "send-file", "available": true,  "incremental": true,
      "parents": ["20260728T131040Z-cd6fc061"],
      "notes": ["restore_requires_btrfs_target"] },
    { "id": "copy",      "available": true,  "incremental": false,
      "notes": ["snapshots_excluded_by_default"] }
  ],
  "recommended": "send-file"
}
```

### `recommended` 的排序（初稿未指明）

`send` > `send-file` > `copy` > **`snapshot`（最后）**。

刻意不按模式表的顺序。备份存在的理由是**源盘整块坏掉之后还能恢复**，而 `snapshot`
把第二份副本放在同一块盘上——推荐它等于推荐一个不是备份的东西。它保留为可选项
（同盘秒级副本在升级前仍然有用），但永远不会被推荐。

### 模式

| `id` | 条件 | 说明 |
| --- | --- | --- |
| `snapshot` | 源 btrfs + `data/` 是 subvolume + 目标与源同一 fs | 等价于 `anas snapshot create`，最快 |
| `send` | 源 btrfs + 目标 btrfs + 有 `btrfs` 工具 + 有权限 | `btrfs send \| btrfs receive`，支持增量 |
| `send-file` | 源 btrfs + 目标可写 + 有 `btrfs` 工具 + 有权限 | `btrfs send > <file>`，只能还原到 btrfs |
| `copy` | 目标可写 **+ data 全部可读** | rsync，可还原到任意 fs。**源非 btrfs 时唯一可用模式** |

### 与初稿的偏差二：`copy` 模式也需要权限（初稿写作「目标可写」）

初稿的模式表把 `copy` 的条件写成"目标可写"。历史非生产环境实测（普通用户，workspace 里的
module 真跑过）：**不成立**。lego 以 root 写入 `data/lego/certs/accounts`（0700
root）和 `ca.key`、`*.key`（0600 root），普通用户读不到，rsync 退出码 23。

`copy` 是四种模式里**唯一逐文件读取数据**的，因此也是唯一会被权限拦住的：

| 模式 | 读取方式 | 受目录权限影响 |
| --- | --- | --- |
| `snapshot` | btrfs 元数据操作，不读文件 | ❌ |
| `send` / `send-file` | 经文件系统读取 subvolume | ❌ |
| `copy` | 逐文件 read | ✅ |

于是出现一个反直觉但真实的不对称：**需要最高权限才能启动的模式，反而不是被权限
最早拦住的那个。**

处置与二期"回收空间需要特权"一致：**报错并给出补救方式，不半途而废**。跳过读不到的
文件会产出一个"标记为 complete 但缺少全部私钥"的备份——正是 `verify` 存在的意义
所在的那种损坏状态，而且是被故意制造出来的。

检测放在 `capabilities`：估算体积时本来就要走一遍 `data/`，顺带记录是否遇到
`EPERM`，输出 `source.data_fully_readable`。**必须在这里检测**，否则拒绝会发生在
容器已经停机之后。

根因不是 btrfs 而是**容器数据归 root**，即
[workspace-backup.md](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/archived/workspace-backup.md) §7 已经论证过的那一条，此处
只是它在读取方向上的另一次体现。

### send 类模式需要两条传输通道

**`btrfs send` 只能发送 subvolume。** 快照目录 `snapshots/<id>/` 中只有 `data/` 是
subvolume，`snapshot.yml`、`meta/`、`deployment/` 都在普通目录里，**send 不会带上
它们**。因此 `send` 与 `send-file` 模式必须成对传输：

1. `btrfs send` 传 `data` subvolume（可带 `-p` 增量）
2. tar/rsync 传 `snapshot.yml` + `meta/` + `deployment/`

两条通道全部完成后才写入目标端的完成标记；任一失败该备份记为 `complete: false`。

### 与初稿的偏差三：目标端布局是每备份一个目录（初稿画的是平铺文件）

初稿的动作清单写 `/mnt/backup/<id>.data.stream` 与 `/mnt/backup/<id>.meta.tar`，
即平铺在目标根下。实现改为**每个备份一个目录**：

```text
<dest>/
  .tmp-<backup-id>/     # 传输中，完成后整体 rename
  <backup-id>/
    backup.yml          # 清单，0600，complete 最后写
    data.stream         # send-file
    meta.tar            # send / send-file 的第二通道
    data/               # snapshot（ro 子卷）/ send（received 子卷）/ copy（普通目录）
    meta/  deployment/  snapshot.yml    # snapshot / copy 模式下的第二通道
```

三条理由：`copy` 模式本来就要写一棵目录树，平铺放不下；**rename 一个目录**给了和
snapshot 创建同样的原子性，中断产物带 `.tmp-` 前缀因而不出现在任何列表里；以及
四种模式因此有同一个形状，**恢复只需一条路径**。

**目标端不设索引文件。** 目标常是可移动盘或多台主机共写的共享目录，索引会成为
第二个没人能保证其时效的真相源。每备份一份 `backup.yml` 是唯一权威，与源侧
`snapshot.yml` 的处理一致。`chain_broken` 是列举时算出来的，不是存下来的——祖先
还在不在是目标此刻的性质，不是写入那一刻的性质。

### 每种模式都产出同一个"快照形状"

`copy` 模式在非 btrfs 主机上没有快照可拷，实现按上表逐项点名地从活的 workspace
组装出同一个形状（`config.source.yml` → `meta/config.yml`，活动制品 →
`deployment/`，`data/` → `data/`）。**点名就是排除**：历史制品与缓存因为没被点到
而不进备份，不存在一条会写错的过滤规则——尤其不存在一条会漏掉 `data/` 的。

`copy` 模式不受此限制，但注意 **rsync 默认不保留硬链接关系**（需 `-H`）。当快照的
`deployment/` 是硬链接实现时，不加 `-H` 会把每个链接当独立文件完整复制——对备份而言
这是正确取舍，完整性优先于去重。

判定"同一文件系统"用 **btrfs fsid**，不是 `st_dev`——同一个 btrfs 上不同 subvolume
的 `st_dev` 是不同的（实测：两个兄弟 subvolume 为 124 与 125，父目录为 43）。

### 与初稿的偏差一：`statfs` 的 `f_fsid` 也不能用（初稿未指明）

初稿说"用 btrfs fsid"，但没说从哪里读。最自然的读法——`statfs(2)` 的 `f_fsid`——
**同样是错的**，而且错得比 `st_dev` 更隐蔽，因为它的名字看起来正是"文件系统标识"。

btrfs 把 subvolume 的 root objectid 异或进了 `f_fsid`：

```c
buf->f_fsid.val[0] ^= objectid >> 32;
buf->f_fsid.val[1] ^= objectid;
```

非生产环境历史实测（内核 5.15）：

| 路径 | `f_fsid` | `st_dev` |
| --- | --- | --- |
| `/data`（挂载点） | `38df694b8bbdc98e` | 43 |
| `/data/…/ws/data`（subvolume） | `38df680d`​`8bbdc98e` | 129 |

高 32 位不同、低 32 位相同。用它比较会把**同一块盘上的目标判成不同文件系统**，
snapshot 模式于是永远不可用——一个只表现为"少了一个模式"的静默失败。

真正稳定的标识是**文件系统 UUID**，且无需特权即可读到：把挂载表
（`/proc/self/mountinfo`）给出的块设备，与 `/sys/fs/btrfs/<uuid>/devices/` 下的
设备名对上即可。`btrfs filesystem show` 能直接回答，但和其余 tree-search ioctl
一样需要 root。sysfs 读不到时退回 `cp --reflink=always` 探测——直接问文件系统能不能
做这些模式真正依赖的操作，比两个方向的猜测都强。

`data_is_mountpoint` 同理不能用"与父目录比 `st_dev`"判定：在 btrfs 上那会把每个
subvolume 都判成挂载点。用挂载表判定，因为只有真正的挂载点才会让恢复路径的
`rename(2)` 返回 EBUSY。

同一原因导致 **`rsync --one-file-system` / `find -xdev` 会在 subvolume 边界停下**
（实测：`find -xdev` 对 subvolume 内的文件命中 0 个）。因此 `copy` 模式不能靠
one-file-system 来排除 `snapshots/`——那样会连必须包含的 `data/` 一起漏掉，必须写
显式 `--exclude`。

### `reason` 枚举（模式不可用的原因）

| 值 | 含义 |
| --- | --- |
| `dest_not_specified` | 未提供 `--to` |
| `dest_not_exist` | 目标路径不存在 |
| `dest_not_writable` | 目标不可写 |
| `dest_not_btrfs` | 目标不是 btrfs |
| `dest_not_same_filesystem` | 目标与源不在同一 btrfs |
| `source_not_btrfs` | 源不是 btrfs |
| `data_not_subvolume` | `data/` 不是 btrfs subvolume |
| `data_is_mountpoint` | `data/` 是挂载点（`rename(2)` 会 EBUSY，恢复流程无法工作） |
| `btrfs_tool_missing` | 找不到 `btrfs` 命令 |
| `insufficient_privilege` | send 类：缺少 `CAP_SYS_ADMIN`；`copy`：读不全 `data/` |
| `insufficient_space` | 目标剩余空间小于预估体积 |

`insufficient_privilege` 覆盖两种不同的缺失权限——send 的 ioctl 要
`CAP_SYS_ADMIN`，`copy` 要能读容器以 root 写下的数据。共用一个码，是因为调用方对
两者的反应相同（以 root 重跑）；但 `message` 按模式区分，因为人需要知道的不是同一
件事。

### `notes` 枚举（可用但需提醒）

| 值 | 含义 |
| --- | --- |
| `restore_requires_btrfs_target` | 该模式产出的备份只能还原到 btrfs |
| `snapshots_excluded_by_default` | `snapshots/` 默认不含在内 |
| `no_incremental_support` | 该模式不支持增量，每次全量 |
| `crash_consistent_only` | 配合 `--no-stop` 时仅崩溃一致性 |
| `plaintext_secrets_leaving_host` | 备份含明文密钥（`config.yml`、`secrets.yml`）将离开本机 |

---

## `anas backup plan`

完整前置校验 + 输出将要执行的动作清单，**不执行**。`backup create` 内部第一步就是
跑它，plan 不通过直接失败。web 端的"确认页"数据源。

```
anas backup plan --to <dest> --mode <mode> [--snapshot <id>] [--parent <id>]
                 [--no-stop] [--json]
```

### 输出

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/srv/anas",
  "mode": "send-file",
  "dest": "/mnt/backup",
  "incremental": true,
  "parent": "20260728T131040Z-cd6fc061",
  "estimate": { "transfer_bytes": 204010496, "dest_free_after_bytes": 899795989504 },
  "includes": ["config", "lock", "secrets", "state", "deployment", "data"],
  "excludes": ["history_deployments", "caches"],
  "stop_containers": true,
  "containers_to_stop": ["anas_traefik", "anas_authentik", "anas_nextcloud"],
  "estimated_downtime_seconds": 240,
  "warnings": [
    { "code": "plaintext_secrets_leaving_host", "message": "…" }
  ],
  "actions": [
    { "step": 1, "op": "acquire_lock",     "target": ".anas/state/lock" },
    { "step": 2, "op": "stop_containers",  "count": 13 },
    { "step": 3, "op": "snapshot_data",     "target": "snapshots/<new-id>/data" },
    { "step": 4, "op": "copy_state",        "target": "snapshots/<new-id>/meta" },
    { "step": 5, "op": "copy_deployment",   "target": "snapshots/<new-id>/deployment", "method": "reflink" },
    { "step": 6, "op": "seal_snapshot",     "target": "snapshots/<new-id>" },
    { "step": 7, "op": "start_containers",  "count": 13 },
    { "step": 8, "op": "send_stream",       "target": "/mnt/backup/<new-id>.data.stream" },
    { "step": 9, "op": "send_metadata",     "target": "/mnt/backup/<new-id>.meta.tar" }
  ]
}
```

`actions` 是给人看的执行预览，**顺序与 op 名称构成契约**，但调用方不应依赖 `step`
编号连续。`copy_deployment` 的 `method` 取 `reflink` / `hardlink` / `copy`，对应
[snapshot.md](snapshot.md) 的降级顺序。

`--snapshot <id>` 备份已有快照时，动作清单里没有 `stop_containers` /
`start_containers`，取而代之的是 `use_snapshot`：那个快照已经把数据冻住了，再停一次
机什么也换不来。`copy` 模式的传输动作是 `copy_files`，`snapshot` 模式是
`snapshot_data` + `copy_state`。

注意 `start_containers` 排在 `send_stream` **之前**：容器只需在建快照期间停机，send
是从只读快照读取的，可以在服务恢复后进行。这把停机时间从"数据体积决定"压到"快照耗时
决定"——btrfs 建快照是秒级，而 1.3G 的 send 可能要几分钟。

---

## `anas backup create`

```
anas backup create --to <dest> --mode <mode> [--snapshot <id>] [--parent <id>]
                   [--no-stop] [-y] [--json]
```

`--snapshot <id>` 备份一个已存在的快照；省略时先建一个 `reason: pre_backup` 的快照
再发送。备份内容永远等于快照内容。

停机行为：默认在建快照期间停止全部容器，结束后**恢复到原有的运行状态**（只启动
原本在运行的）。整个过程记录在 `.anas/state/transactions/`；**备份失败绝不能把服务
留在停机状态**——这是本命令唯一不可接受的失效方式，崩溃后下次任何 anas 命令启动时
必须检测并补偿。

两套机制，缺一不可：进程内用 defer 恢复，使所有出错路径都经过它；进程被 `SIGKILL`
时 defer 不生效，所以**事务记录在第一个容器停机之前就写入磁盘**。

"原本在运行的"从 compose 读（`ps -q`），不从运行时状态文件读：后者记的是 anas 上次
的意图，不是重启或人手 `docker stop` 之后 Docker 实际在做什么。用 `stop`/`start`
而非 `down`/`up`——容器只需暂停，`up` 会按**当前**的 compose 文件重建它们，而恢复
期间那不一定是它们原本的样子。

### 补偿的触发点（初稿写作「下次任何 anas 命令」）

实现把补偿挂在**取得排他锁**时（`acquireRuntimeLock`），而不是字面意义上的任何命令。
排他锁正是保证安全的东西：备份全程持有同一把锁，所以这里看到的事务记录不可能属于
一个仍在运行的备份。只读命令（`snapshot list`、`backup capabilities`）不取排他锁，
也不应该取——启动容器是一次变更，从 `snapshot list` 里做出来会是个意外。

于是补偿发生在 `apply` / `rollback` / `snapshot create|delete|prune|pin` /
`backup create` 等**会改状态的命令**上，这也正是操作者接下来真会运行的那一批。

`--no-stop` 需配合 `-y`，且输出中带 `crash_consistent_only` 警告。`snapshot` 模式下
btrfs 快照是原子的，风险显著低于 `copy` 模式，警告文案需区分二者。

### 进度 `phase` 枚举

`acquire_lock` → `stop_containers` → `snapshot_data` → `copy_state` →
`copy_deployment` → `seal_snapshot` → `start_containers` →
`send_stream` + `send_metadata` / `copy_files` → `finalize`

### 输出

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "backup_id": "20260729T081504Z-4a1b2c3d",
  "mode": "send-file",
  "dest": "/mnt/backup",
  "incremental": true,
  "parent": "20260728T131040Z-cd6fc061",
  "transferred_bytes": 204010496,
  "started_at": "2026-07-29T08:15:04Z",
  "finished_at": "2026-07-29T08:23:41Z",
  "downtime_seconds": 217,
  "snapshot_id": "20260729T081504Z-4a1b2c3d",
  "warnings": []
}
```

---

## `anas backup list`

```
anas backup list --to <dest> [--json]
```

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "dest": "/mnt/backup",
  "backups": [
    {
      "backup_id": "20260729T081504Z-4a1b2c3d",
      "mode": "send-file",
      "created_at": "2026-07-29T08:15:04Z",
      "incremental": true,
      "parent": "20260728T131040Z-cd6fc061",
      "size_bytes": 204010496,
      "deployment_id": "20260728T131040Z-cd6fc061",
      "config_digest": "sha256:…",
      "modules": { "nextcloud": "30.0.1", "authentik": "2024.10.5" },
      "complete": true
    }
  ]
}
```

`complete: false` 表示该备份是中断产物。增量链断裂（parent 缺失）时该条目附带
`"chain_broken": true`。

---

## `anas backup restore`

```
anas backup restore --from <src> -w <workspace> [--backup-id <id>] [--dry-run] [-y] [--json]
```

- **必须显式 `-w`**，不接受 `ANAS_WORKSPACE`，不接受 cwd 推导。
- `--dry-run` 输出将要写入/覆盖的路径清单，不落盘。
- 目标 workspace 非空时需要 `-y`。
- 完成后自动执行一次结构校验与 `snapshot verify`，结果并入输出。
- **恢复是全有或全无**：secret store 分代追加，恢复旧快照会丢弃其后的代次；这与 data
  一同回退是自洽的，但"只恢复 meta 保留当前 data"会造成密钥与数据错配，必须拒绝。
- `state/active.yml` 不来自备份，由快照的 `deployment_id` 重新生成。

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/srv/anas",
  "backup_id": "20260729T081504Z-4a1b2c3d",
  "restored": ["config", "state", "secrets", "data", "active_deployment"],
  "verify": { "ok": true, "checked": 6, "problems": [] },
  "next_steps": ["anas start -w /srv/anas"]
}
```

`next_steps` 是建议执行的命令字符串数组，供 web 端直接呈现。恢复**不自动启动服务**。

### 与初稿的偏差四：恢复 `send` 备份走复制而非再 send 一次（初稿未指明）

`send-file` 备份必须 `btrfs receive`，因而**恢复也需要 `CAP_SYS_ADMIN`**，且目标必须
是 btrfs；增量备份还要按 parent 链**从全量起依次 receive**，链在动手之前先解析完，
因为顺序错了 btrfs 会拒绝，而那时 workspace 已经被动过了。

`send` 备份的数据在目标端已经是一个真实 subvolume，本可以再 `btrfs send` 回去以
保住 CoW 共享。实现**改为直接复制**：再 send 一次需要两端都有 `CAP_SYS_ADMIN`，那
意味着"创建时要 root 的模式，恢复时也要 root"——而真到了要恢复的时候，那台机器很
可能是刚装好的。恢复能不能做，不该比备份能不能做更苛刻。

四种模式在恢复侧先被归一成同一个"快照形状"的目录（`materializeBackup`），**校验通过
之后才动 workspace**。缺了元数据通道的备份必须在数据被替换之前发现，而不是之后。

---

## `anas backup verify`

```
anas backup verify --to <dest> [--backup-id <id>] [--json]
```

校验备份是否仍然可用：文件/流存在、大小与元数据一致、增量链完整。设计为可被 cron
调用——备份系统最常见的失效是"以为有其实没有"。

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": false,
  "dest": "/mnt/backup",
  "checked": 4,
  "problems": [
    { "backup_id": "20260726T…", "code": "parent_missing", "message": "…" },
    { "backup_id": "20260727T…", "code": "size_mismatch",  "message": "…" }
  ]
}
```

### `problems[].code` 枚举

`stream_missing`、`metadata_stream_missing`（两条通道之一缺失）、`parent_missing`、
`size_mismatch`、`metadata_unreadable`、`incomplete_backup`
