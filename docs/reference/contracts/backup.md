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
| 临时内容与租约 | `<workspace>/tmp`、外部受管根、`.anas/temp/` | ❌ 每次恢复重新分配 |

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

有 PostgreSQL Resource 的 workspace 自动捕获全部 Compose service 的不可变镜像 ID 与 Docker
archive；运行容器的 Image 是依据，不以可能移动的 tag 代替。snapshot/copy/send/send-file 均沿现有
metadata 通道携带归档和完整性校验。恢复加载 archive，仅固定恢复制品的镜像，不改其他部署的 tag。
捕获不到镜像时备份失败，归档会增加所需空间和停机时间。PG 的保留扩展不因删除声明消失，因此普通
PG Resource 或仍保留的 PG Provider 也遵循此规则。新建 PG workspace 备份拒绝 `--no-stop`；
该选项仅能用于传输已有快照，以避免把运行中数据库和媒体的文件复制称为一致备份。
其他 workspace 尚需 registry 或构建环境，通用 `--include-images` 选项未交付。

本机隔离 Docker 已验证旧镜像缓存删除后的 ID 恢复、tag 保持原值；整台服务器的数据库、媒体及其他
Consumer 一致恢复仍待验收，不能从镜像 round-trip 推断数据恢复通过。

PG 的 Btrfs 恢复点在停机前拒绝 `data/` 内的外部挂载、嵌套子卷以及逃逸或失效的符号链接，
避免父子卷快照遗漏真实数据。`copy` 按文件读取并包含可读的挂载/子卷内容，也拒绝逃逸或失效链接；
它不重建原来的挂载或子卷拓扑。恢复到相应布局前仍须核对挂载配置与完整数据覆盖。

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
    owner.yml           # 所有权记录：谁在写、心跳；发布后删除
  .abandoned-<backup-id>-<随机>/   # 已判定放弃、正在删除
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

### 中断产物只在证明已被放弃后清理

同一个目标常被多方同时写入：一台主机上的多个 workspace，或挂着同一共享目录的多台
主机（anasd 由 root 管理的备份目标对它管理的每个 workspace 都开放）。workspace
运行时锁（`.anas/state/lock`）只串行化一个 workspace 内部的操作，管不到目标。早先
`backup create` 开始时会删除目标下**所有** `.tmp-` 目录，于是后开始的备份会删掉先开始
那个正在写的树。先开始的备份通常随之失败；但若删除恰好落在它的两个传输步骤之间，后续
步骤（包括写清单）会重建缺失的目录，最终可能发布一个缺了通道、却标着
`complete: true` 的备份。

现在每个临时目录在排他的 `mkdir` 之后、写入任何数据之前，先写一份所有权记录
`.tmp-<backup-id>/owner.yml`：

| 字段 | 用途 |
| --- | --- |
| `backup_id`、`token` | 这次认领；`token` 每次随机，发布前用它确认目录仍是自己写的那棵 |
| `host` | 主机名，只供人读警告，不参与判定 |
| `boot_id`、`pid_namespace` | 写入进程所在的 PID 空间（Linux 取自 `/proc/sys/kernel/random/boot_id` 与 `/proc/self/ns/pid`） |
| `pid`、`process_start` | 写入进程及其启动时间，后者用于识别 PID 复用 |
| `started_at`、`heartbeat_at` | 认领时间与心跳；心跳每分钟原地改写一次 |

`create` 开始时的清理只删除能**证明**已被放弃的目录，其余一律保留：

| 情形 | 判定 |
| --- | --- |
| 与写入者处于同一 PID 空间（`boot_id` 与 `pid_namespace` 都相同） | 直接查进程：已退出、是僵尸进程、或该 PID 已属于启动时间不同的进程 → 放弃；仍在运行或查不清 → 保留 |
| 其他（另一台主机、另一个 PID 命名空间、`boot_id` 未知） | `heartbeat_at` 超过 30 分钟未更新 → 放弃；否则保留 |
| 没有 `owner.yml` | 30 分钟内的目录静默保留（可能正处于 `mkdir` 与写记录之间）；更旧的若为空目录则删除，否则保留并警告 |
| `owner.yml` 无法解读（损坏，或来自更新的版本） | 保留并警告 |

没有 `owner.yml` 的非空目录永远不会被自动删除：没有任何东西能证明它已被放弃，而早于
所有权记录的版本留下的正是这种目录。确认没有备份正在写这个目标之后手工删除；其中的
`data/`、`userdata/` 若是 received 子卷，要先用 `btrfs subvolume delete` 删除。

`machine-id` 刻意不参与判定：克隆出来的机器共享它，据此推断"同一台主机重启过"会删掉
另一台机器上正在进行的备份。同理，只有 `boot_id` 相同还不够——同一主机上的容器共享
`boot_id`，却各有自己的 PID 命名空间，跨命名空间查 PID 会把活着的写入者判成已退出。

判定为放弃的目录先 `rename` 成 `.abandoned-<backup-id>-<随机>`，再删除。rename 是原子的：
写入者发布时的 rename 与清理的 rename 只可能有一个成功，不会发布出一棵删了一半的树。
删除中途被打断时留下的是 `.abandoned-` 名字——这个名字下从不存放需要保留的东西，任何
一次清理都可以把它删完——而不是一棵可能已经丢了 `owner.yml`、再也无法证明被放弃的
`.tmp-` 树。写入者自己失败时也走同一条路。`.abandoned-` 与 `.tmp-` 一样以 `.` 开头，
不出现在任何列表里；每个 `owner.yml` 只描述它所在的那棵树，目标端依旧没有索引文件。

写入者在写完 `backup.yml` 之后、rename 之前再核对一次：`owner.yml` 仍在且 `token` 是
自己的。否则以 `backup_temp_lost`（退出码 1）失败，**不发布任何东西**。核对放在写清单
之后，是因为写清单会重建缺失的目录，只有记录能说明清单落进的是自己写满的那棵树，还是
一个空的替身。心跳只打开已有的 `owner.yml` 原地改写、从不创建，所以被拿走又被重建的
目录里不会凭空长出一份记录。

**不设目标端锁，是有意的。** 目标正是 flock 最不可靠的地方——NFS、SMB 与 FUSE 挂载
各有各的模拟方式，或者根本不支持——而锁目录又需要自己的"持有者是否已死"判定，等于把
同一个问题再做一遍。可能互相竞争的两步都由这些文件系统都原子执行的操作裁决：认领是
排他的 `mkdir`，拿走是 `rename`。锁唯一能关掉的，是 `mkdir` 与写 `owner.yml` 之间的
几微秒窗口，而这个窗口里的目录按上表只会被静默保留。

代价与前提：

- 共用一个目标的主机，时钟要大致同步（误差远小于 30 分钟）。时钟落后超过这个量的主机，
  其正在进行的备份会被其他主机判为放弃；时钟超前只会让它的目录多保留一阵。
- 写入者被挂起（例如笔记本休眠）超过 30 分钟，另一台主机上的 `create` 会判它放弃；它
  恢复后以 `backup_temp_lost` 失败，不会发布残缺的备份。
- 只有另一台主机写过的死树，要等心跳过期后的下一次 `create` 才会回收。

清理产生的警告（`--json` 下是 stderr 上的 JSON Lines）：

| `code` | 含义 |
| --- | --- |
| `backup_temp_kept` | 某个临时目录缺少可用的所有权证明而被保留；`message` 给出路径与原因 |
| `backup_temp_removal_failed` | 判定放弃的目录删除失败（常见于没有 `CAP_SYS_ADMIN` 时删不掉 received 子卷），下次 `create` 会重试 |

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

公共 plan ID 绑定目的地与操作选择；磁盘可用空间是实时观测，不参与 ID。执行前仍重新校验空间与其他前置条件。

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
- 目标必须是已有 workspace；新目标先执行 `anas init <workspace> -y`，空初始化不启动服务。
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

上例为冻结制品仍绑定本 workspace 的恢复。恢复到另一 workspace 时，`next_steps` 为 `anas apply -w <workspace>`：导入的 active 记录不授予源 project 或源临时租约的运行权限。首次配置 apply 保留诊断记录、解除外来 active 授权并生成目标新 deployment；相应 plan 不停止源 Module。受管临时制品缺少有效冻结 `DATA_PATH` 绑定时拒绝运行。直接生命周期操作、`apply --deployment` 和历史回滚不能绕过此检查；同 workspace 快照恢复及有效本地租约保持原授权。

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
