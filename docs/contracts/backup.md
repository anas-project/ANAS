# backup 命令 JSON 契约

> 状态：设计中，尚未实现。通用约定（流分离、退出码、枚举、时间与大小格式）见
> [README.md](README.md)，本文不再重复。

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
| 权威状态 | `config.yml`、`config.lock.yml`、`.anas/state/`、`.anas/secrets.generated.yml` | ✅ |
| 业务数据 | `data/` | ✅ |
| active 制品 | `.anas/deployments/<active-id>/` | ✅ |
| 历史制品 | `.anas/deployments/` 下其余目录 | ❌ |
| 缓存 | `.anas/go-build-cache/`、`.anas/hook-bin/`、`.anas/staging/` | ❌ 永不备份 |

active 制品必须包含，理由有三条，每一条单独都足够：

1. 它是**镜像构建上下文**（compose 里写的是 `build: context: ./${MODULE_NAME}`），
   没有它连 `docker compose build` 都跑不了；
2. 它是 workspace 内**唯一一份可运行的 cask 副本**——cask 源树在 workspace 之外
   （`locateCaskRoot` 在 `ANAS_ROOT`/cwd/可执行文件旁找 `casks/mods`）；
3. 它携带**冻结的 hook 二进制**（`<cask>/.hook.bin`），`freezeHookBinary` 在写入它的
   同时删除了 `hook/` 源码目录，目的正是"无需 Go 工具链即可运行"。`.anas/hook-bin/`
   只是构建缓存，权威副本在这里。

体量上 active 制品约占 data 的 3%（finance 实测 42M vs 1.3G），其中 41M 是 13 个
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
  "workspace": "/home/whl/anas-deploy",
  "source": {
    "fstype": "btrfs",
    "fsid": "3f2a1c8e-...",
    "data_is_subvolume": true,
    "data_is_mountpoint": false
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

### 模式

| `id` | 条件 | 说明 |
| --- | --- | --- |
| `snapshot` | 源 btrfs + `data/` 是 subvolume + 目标与源同一 fs | 等价于 `anas snapshot create`，最快 |
| `send` | 源 btrfs + 目标 btrfs + 有 `btrfs` 工具 + 有权限 | `btrfs send \| btrfs receive`，支持增量 |
| `send-file` | 源 btrfs + 目标可写 + 有 `btrfs` 工具 + 有权限 | `btrfs send > <file>`，只能还原到 btrfs |
| `copy` | 目标可写 | rsync 整个 workspace，可还原到任意 fs。**源非 btrfs 时唯一可用模式** |

### send 类模式需要两条传输通道

**`btrfs send` 只能发送 subvolume。** 快照目录 `snapshots/<id>/` 中只有 `data/` 是
subvolume，`snapshot.yml`、`meta/`、`deployment/` 都在普通目录里，**send 不会带上
它们**。因此 `send` 与 `send-file` 模式必须成对传输：

1. `btrfs send` 传 `data` subvolume（可带 `-p` 增量）
2. tar/rsync 传 `snapshot.yml` + `meta/` + `deployment/`

两条通道全部完成后才写入目标端的完成标记；任一失败该备份记为 `complete: false`。

`copy` 模式不受此限制，但注意 **rsync 默认不保留硬链接关系**（需 `-H`）。当快照的
`deployment/` 是硬链接实现时，不加 `-H` 会把每个链接当独立文件完整复制——对备份而言
这是正确取舍，完整性优先于去重。

判定"同一文件系统"用 **btrfs fsid**，不是 `st_dev`——同一个 btrfs 上不同 subvolume
的 `st_dev` 是不同的（实测：两个兄弟 subvolume 为 124 与 125，父目录为 43）。

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
| `insufficient_privilege` | 缺少 root / `CAP_SYS_ADMIN`，无法 send/receive |
| `insufficient_space` | 目标剩余空间小于预估体积 |

### `notes` 枚举（可用但需提醒）

| 值 | 含义 |
| --- | --- |
| `restore_requires_btrfs_target` | 该模式产出的备份只能还原到 btrfs |
| `snapshots_excluded_by_default` | `snapshots/` 默认不含在内 |
| `no_incremental_support` | 该模式不支持增量，每次全量 |
| `crash_consistent_only` | 配合 `--no-stop` 时仅崩溃一致性 |
| `plaintext_secrets_leaving_host` | 备份含明文密钥（`config.yml`、`secrets.generated.yml`）将离开本机 |

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
  "workspace": "/home/whl/anas-deploy",
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
      "casks": { "nextcloud": "30.0.1", "authentik": "2024.10.5" },
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
  "workspace": "/home/whl/anas-deploy",
  "backup_id": "20260729T081504Z-4a1b2c3d",
  "restored": ["config", "state", "secrets", "data", "active_deployment"],
  "verify": { "ok": true, "checked": 6, "problems": [] },
  "next_steps": ["anas start -w /home/whl/anas-deploy"]
}
```

`next_steps` 是建议执行的命令字符串数组，供 web 端直接呈现。恢复**不自动启动服务**。

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
