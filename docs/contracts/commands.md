# 部署与配置命令 JSON 契约

> 状态：**已实现**（`init` / `plan` / `lock` / `render` / `build` / `apply` /
> `start` / `restart` / `stop` / `rollback` / `status` / `deployments` /
> `config`）。
> 通用约定（流分离、退出码、枚举、时间与大小、路径、版本、最小信封）见
> [README.md](README.md)，本文不再重复。
> `snapshot` 见 [snapshot.md](snapshot.md)，`backup` 见 [backup.md](backup.md)。

本文覆盖的是**早于契约存在**的那批命令。它们此前统一"返回一个 error、退出 1"，
对一个要按码分支的调用方等于什么都没说。

## 目录

- [init](#init)
- [plan](#plan) / [lock](#lock)
- [render / build](#render--build)
- [apply](#apply)
- [start / restart / stop](#start--restart--stop)
- [rollback](#rollback)
- [status](#status)
- [deployments](#deployments)
- [config](#config)
- [help](#help)

---

## init

```
anas init [PATH] [--shell-init write|remove] [-y] [--json]
```

创建 workspace。是唯一会创建 workspace 的命令——别的命令一律拒绝凭空造一个，
因为一次打错的 `cd` 不该悄悄长出第二套平行部署。

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/data/ws",
  "config_path": "/data/ws/config.yml",
  "data_path": "/data/ws/data",
  "snapshots_path": "/data/ws/snapshots",
  "state_path": "/data/ws/.anas",
  "btrfs": true,
  "data_is_subvolume": true,
  "snapshots_usable": true,
  "shell_init": { "action": "none", "profile": null, "changed": false }
}
```

`data_is_subvolume` 决定这个 workspace **有没有快照和数据回滚能力**，所以它是
结果的一部分而不是实现细节。非 btrfs 上它是 `false`，此后 `snapshot` 全线不可用、
`apply` 不会自动打前置快照、`backup` 只剩整目录复制一种模式。

`shell_init.action` 取 `none` / `write` / `remove`；`changed` 区分"已经就是这样"
和"改写了"。

`--shell-init` 只接受 `write` 与 `remove`。此前任何非空取值都当 `write`，
`--shell-init=yes` 静默生效与静默不生效无从分辨。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `workspace_exists` | 4 | 目标已经是 workspace |
| `data_is_symlink` | 4 | `data/` 是符号链接；tar 与 rsync 会跳过它，备份会悄悄是空的 |
| `data_is_mount_point` | 4 | `data/` 是挂载点；数据恢复要 rename 这个目录，跨挂载点做不到 |
| `shell_unrecognised` | 4 | `$SHELL` 不认识，写不了 profile |
| `confirmation_required` | 3 | 非 btrfs 需要确认，或 profile 里已有指向别处的 anas 块 |
| `subvolume_create_failed` / `mkdir_failed` / `write_failed` | 1 | 建到一半失败 |

## plan

```
anas plan -c config.yml [--cask-root DIR] [--json]
```

只算不写。**不创建也不读取任何运行时状态**，`-w` 只为命令行对称而接受。

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "config": "/data/ws/config.yml",
  "cask_root": "/srv/anas/casks/mods",
  "modules": ["postgres", "authentik", "nextcloud"],
  "iam": {
    "provider": "authentik",
    "consumers": [{ "cask": "nextcloud", "interface": "oidc" }]
  },
  "capability_bindings": { "nextcloud": { "relational_database": "postgres" } }
}
```

`modules` 是启动顺序，不是集合，顺序有意义。

`iam.provider` 在无人消费该能力时为 `null` 而不是缺席——调用方总能拿到这个键，
不必去分辨"没有 IAM"和"这个版本不报告 IAM"。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `config_missing` / `config_invalid` | 4 | 配置文件不在，或读不出来 |
| `cask_root_missing` / `cask_root_invalid` | 4 | 找不到 cask 目录，或它读不出来 |
| `resolution_failed` | 4 | 依赖成环、模块未知、被禁用的模块又被依赖 |
| `version_conflict` | 4 | 版本约束互相打架 |

## lock

```
anas lock [-w WORKSPACE] [-c config.yml] [--json]
```

把解析结果钉住，写 `<config>.lock.yml`。

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "config": "/data/ws/config.yml",
  "lock_path": "/data/ws/config.lock.yml",
  "modules": ["postgres", "traefik"],
  "casks": [{ "name": "postgres", "version": "16.4.0", "revision": 1, "app_version": "16.4", "digest": "sha256:…" }],
  "iam": { "provider": null, "consumers": [] },
  "capability_bindings": {}
}
```

`casks` 是契约对锁的视图，不是锁文件的磁盘格式；磁盘格式可以变，这里不跟着变。

错误码同 `plan`，另有 `lock_invalid`（4，锁文件读不出来）与 `write_failed`（1）。

## render / build

```
anas render [-w WORKSPACE] [-c config.yml] [--update-lock] [--json]
anas build  [CASK...] [-w WORKSPACE] [-c config.yml] [--update-lock] [--json]
```

产出一个不可变部署制品并封印它，但**不激活**。`build` 比 `render` 多一步构建镜像。

`build` 接受 cask 名，只构建这些 cask 的镜像；渲染始终是整个部署的，因为渲染一半不构成部署。`render` 不接受 cask 名。

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "config": "/data/ws/config.yml",
  "deployment_id": "20260731T101500Z-a1b2c3d4",
  "deployment_path": "/data/ws/.anas/deployments/20260731T101500Z-a1b2c3d4",
  "built": false
}
```

进度 `phase`：`calculate` → `render` → `build-images`（仅 `build`）→ `seal`，
`unit` 为 `casks`（`seal` 为 `deployments`）。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `lock_missing` | 4 | 没有 config lock，且没给 `--update-lock` |
| `lock_stale` | 4 | 锁与源码里的 cask 对不上；要显式 `anas lock` 更新 |
| `secrets_unreadable` | 4 | secret store 读不出来 |
| `compose_missing` | 4 | 要 build 但没有 docker compose |
| `calculate_failed` / `render_failed` / `build_failed` / `seal_failed` | 1 | 动手之后失败 |

## apply

```
anas apply [-w WORKSPACE] [-c config.yml [--build] | --deployment ID]
           [--update-lock] [--allow-risky] [--snapshot | --no-snapshot] [-y] [--json]
```

渲染（或取一个已就绪的制品）并激活它。

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws",
  "deployment_id": "20260731T101500Z-a1b2c3d4",
  "previous_deployment": "20260730T090000Z-9f8e7d6c",
  "activated_at": "2026-07-31T10:15:07Z",
  "deployment_path": "/data/ws/.anas/deployments/20260731T101500Z-a1b2c3d4"
}
```

`previous_deployment` 在这是第一次部署时为 `null`。

**守卫**。跨越不可逆变更（`credential_rotate`、`data_migrate`、`immutable`）时
拒绝执行，退出 **4**，`error.detail.blocked` 列出具体项：

```json
{
  "api_version": "anas.dev/cli/v1", "ok": false,
  "error": {
    "code": "guarded_changes",
    "message": "apply crosses guarded state changes:\n  …",
    "detail": { "blocked": ["global.default_service_root_password (credential_rotate; rotate-secret)"] }
  }
}
```

这是 4 而不是 1：机器处在一个不能就这么往下走的状态，调用方要么跑迁移，要么带
`--allow-risky` 重来。

**自动快照**。触发条件见 [snapshot.md](snapshot.md)。放弃快照要确认，因此
`--no-snapshot` 在非 tty 下必须配 `-y`，否则退出 **3**。快照打不成时以
warning 记录到 stderr，`code` 取 `no_snapshot_backend` 或 `data_not_subvolume`，
随后仍然要确认。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `deployment_not_ready` | 4 | `--deployment` 指的制品状态不是 ready |
| `guarded_changes` | 4 | 见上 |
| `confirmation_required` | 3 | `--no-snapshot`、或无法快照仍要继续，而无从确认 |
| `start_failed` | 1 | 启动失败；已尽力把旧部署重新拉起 |

## start / restart / stop

```
anas start|restart|stop [CASK...] [-w WORKSPACE] [--json]
```

不给 cask 名就是整个部署。给了名字只作用于这些 cask，顺序取自部署的依赖顺序而非命令行词序；名字不在本部署中是用法错误，并列出本部署实际有哪些 cask。

部分停止**不拆 macvlan 网桥**：整体停止会拆（没人再用），停一个 cask 不代表其他 cask 不用它。

**没有天然结果的命令**，按 README 的"最小信封"办：

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "action": "stop",
  "deployment_id": "20260731T101500Z-a1b2c3d4",
  "casks": ["postgres", "authentik", "traefik"]
}
```

`casks` 是操作涉及的 cask 顺序，不是"停/起了几个"的统计。调用方**不应**期待此外
的结果字段。未加 `--json` 时 stdout 为空。

进度 `phase`：`stop-containers`、`start-containers`、`after-start-hooks`，
`unit` 为 `casks`。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `no_active_deployment` | 4 | 还没 apply 过 |
| `compose_missing` | 4 | 没有 docker compose |
| `deployment_unreadable` | 4 | 活跃部署的制品坏了或不在 |
| `start_failed` / `stop_failed` | 1 | 动手之后失败 |

## rollback

```
anas rollback [DEPLOYMENT_ID] -w WORKSPACE [--allow-risky] [--json]
```

换制品，**绝不动数据**。倒回数据只有 `anas snapshot restore` 一条路。

只接受 `-w`，不从 `ANAS_WORKSPACE` 或当前目录推断——一个写进 shell profile 的
环境变量是最容易过期并指向别处的东西。

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws",
  "deployment_id": "20260730T090000Z-9f8e7d6c",
  "previous_deployment": "20260731T101500Z-a1b2c3d4",
  "activated_at": "2026-07-31T11:02:31Z",
  "data_touched": false
}
```

`data_touched` 恒为 `false`，且**有意保留**。它是这条命令与 `snapshot restore` 的
分界写进文档里的样子；把它省掉，调用方就得靠命令名来推断有没有动数据。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `no_previous_deployment` | 4 | 没有可回滚的目标 |
| `already_active` | 4 | 指定的部署已经是活跃的 |
| `rollback_data_breaking` | 4 | 目标 cask 声明了这次降级会重写磁盘数据；`--allow-risky` **不能**绕过 |
| `rollback_guarded_changes` | 4 | 跨越守卫变更；`--allow-risky` 可以绕过 |

`rollback_data_breaking` 与 `rollback_guarded_changes` 分成两个码是有意的：
后者描述的是运行时不知道而操作者可能知道的事，前者描述的是运行时**知道**的事。

## status

```
anas status [-w WORKSPACE] [--json]
```

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws",
  "active_deployment": "20260731T101500Z-a1b2c3d4",
  "activated_at": "2026-07-31T10:15:07Z",
  "verified_at": "2026-07-31T10:15:07Z",
  "previous_deployments": ["20260730T090000Z-9f8e7d6c"]
}
```

**没有活跃部署是一个成功的回答，退出 0**，`active_deployment` 为 `null`。把它
报成失败，调用方就分不开"崭新的 workspace"和"状态读不出来的 workspace"。

## deployments

```
anas deployments list [-w WORKSPACE] [--json]
anas deployments inspect ID [-w WORKSPACE] [--json]
```

`list`：

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws",
  "deployments": [{
    "id": "20260731T101500Z-a1b2c3d4", "status": "active",
    "created_at": "2026-07-31T10:15:00Z", "activated_at": "2026-07-31T10:15:07Z",
    "deactivated_at": null, "verified_at": "2026-07-31T10:15:07Z",
    "predecessor": "20260730T090000Z-9f8e7d6c", "failure": null
  }]
}
```

`status` 取 `ready` / `active` / `previous` / `failed`。

`inspect` 输出 `deployment`（制品清单）与 `state`。**清单以 JSON 输出，不是磁盘上
那份 YAML。** 此前它把 `deployment.yml` 原样打到 stdout，加不加 `--json` 都一样，
而 YAML 不是 JSON 文档，任何 `JSON.parse` 都过不去。清单类型因此加了 json tag，
键名与磁盘上的 snake_case 一致——同一份清单两种拼法正是调用方会写死绕过去的东西。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `deployment_missing` | 4 | 没有这个 id |
| `state_unreadable` | 4 | 状态目录读不出来 |

## config

```
anas config list    [global|<cask>]     [-w WORKSPACE] [-c config.yml] [--json]
anas config set     <module.parameter> <value> [-w WORKSPACE] [-c config.yml] [--json]
anas config explain <module.parameter> [--json]
anas config plan    [-w WORKSPACE] [-c config.yml] [--json]
anas config secret  list | get <KEY>   [-w WORKSPACE] [--json]
```

`config list` enumerates every settable parameter with the path `set` accepts,
the environment key it becomes, its default, its current value and its change
effect. Values of parameters marked sensitive are reported as `<set>`/`<unset>`
and never printed; `config secret get` remains the way to read a credential. It
needs no workspace, because what can be set is a property of the casks; inside
one it additionally fills in the current values.

`set` and `explain` reject a parameter no manifest declares, naming the closest
declared one, and exit with the usage code. The raw `env.<KEY>` path is not
checked: it is the escape hatch for values nothing declares.

```text
```

`set` 与 `explain` 共用一个 `setting` 形状，能读一个就能读另一个：

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "config": "/data/ws/config.yml",
  "setting": {
    "path": "global.default_service_root_password", "module": "global",
    "parameter": "default_service_root_password",
    "effect": "credential_rotate", "apply": "rotate-secret",
    "sensitive": true, "description": "…"
  }
}
```

`setting.path` 是**配置项的点分路径**，不是文件系统路径——README 的"路径一律绝对"
不适用于它。

`explain` 不需要 workspace，只读 cask 注册表。

`plan`：

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "config": "/data/ws/config.yml",
  "applied_at": "2026-07-30T09:00:00Z",
  "matches_last_start": false,
  "changes": [{
    "key": "global.timezone", "change": "change",
    "path": "global.timezone", "module": "global", "parameter": "timezone",
    "effect": "container_recreate", "apply": "render-and-recreate",
    "sensitive": false, "description": "…"
  }]
}
```

`change` 取 `add` / `remove` / `change`，是枚举而非为句子挑的动词。
`applied_at` 在从未成功启动过时为 `null`。

`secret list` **只返回键名**，`secret get` 返回值。理由见 README 的"敏感值"。

`secret get KEY -w WORKSPACE` 与 `secret get -w WORKSPACE KEY` 必须等价。此前
前者用标准 flag 解析、在 `KEY` 处停下，`-w` 被静默丢弃，命令转而去读当前目录或
`ANAS_WORKSPACE` 指的那个 workspace 的 secret——在操作者以为问的是另一个部署的
时候读出这一个部署的密钥，不是可以交给参数顺序去决定的事。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `usage` | 2 | 路径格式不对、模块未知、参数个数不对 |
| `config_missing` / `config_invalid` | 4 | 配置文件不在或读不出来 |
| `secret_missing` | 4 | 没有这个生成的 secret |
| `secrets_unreadable` | 4 | secret store 读不出来 |
| `write_failed` | 1 | 写配置失败 |

## help

```
anas help [--json]
```

人类可读的帮助是散文，**不是契约**。`--json` 下改为列出可调用的命令：

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "commands": ["init", "plan", "lock", "render", "…"],
  "cask_abi": "anas.cask/v2"
}
```

存在的理由只有一个：`anas help --json` 不能是 stdout 上唯一一处不是 JSON 文档的
地方。
