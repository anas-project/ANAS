# 部署与配置命令 JSON 契约

> 状态：**已实现**（`init` / `plan` / `lock` / `render` / `build` / `apply` /
> `start` / `restart` / `stop` / `rollback` / `status` / `deployments` /
> `config` / `admin` / `credential` / `module`）。
> 通用约定（流分离、退出码、枚举、时间与大小、路径、版本、最小信封）见
> [通用约定](index.md)，本文不再重复。
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
- [credential](#credential)
- [admin local](#admin-local)
- [module](#module)
- [help](#help)

---

## init

```
anas init [PATH] [-c CONFIG] [--module-root DIR] [--shell-init write|remove] [-y] [--json]
```

创建 workspace。是唯一会创建 workspace 的命令——别的命令一律拒绝凭空造一个，
因为一次打错的 `cd` 不该悄悄长出第二套平行部署。

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "workspace": "/data/ws",
  "config_path": "/data/ws/config.yml",
  "config_source": "/data/anas.yml",
  "secrets_imported": 0,
  "data_path": "/data/ws/data",
  "snapshots_path": "/data/ws/snapshots",
  "state_path": "/data/ws/.anas",
  "btrfs": true,
  "data_is_subvolume": true,
  "snapshots_usable": true,
  "shell_init": { "action": "none", "profile": null, "changed": false }
}
```

`-c` / `--config` 在创建 workspace 时导入外部 YAML。ANAS 会先验证和规范化源文件，验证
失败时不会创建 workspace；源文件自身始终保持不变。`module_source: cn` 会规范化为
`official-cn`，若没有显式声明 `global.chinese_speedup`，受管 `config.yml` 会补入 `true`，
渲染环境相应包含 `CHINESE_SPEEDUP=true`。显式 `false` 保持不变。`--module-root` 仅用于
定位导入验证需要的 Module 定义。

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
| `config_import_failed` | 4 | 外部配置无法读取、规范化或通过 Module 参数验证 |
| `module_root_missing` / `module_root_invalid` | 4 | 指定配置时无法找到或载入 Module 定义 |
| `data_is_symlink` | 4 | `data/` 是符号链接；tar 与 rsync 会跳过它，备份会悄悄是空的 |
| `data_is_mount_point` | 4 | `data/` 是挂载点；数据恢复要 rename 这个目录，跨挂载点做不到 |
| `shell_unrecognised` | 4 | `$SHELL` 不认识，写不了 profile |
| `confirmation_required` | 3 | 非 btrfs 需要确认，或 profile 里已有指向别处的 anas 块 |
| `subvolume_create_failed` / `mkdir_failed` / `write_failed` | 1 | 建到一半失败 |

## plan

```
anas plan [-w WORKSPACE] [-c config.yml] [--module-root DIR] [--json]
```

只算不写：Core 不创建或修改 workspace/deployment 状态。`-w` 选择受管 workspace，并非只为
命令行对称；plan 会校验配置完整性，并在私有校验视图中读取已有 Secret Store 以满足 lifecycle
input 与敏感 taint。Secret 明文不会进入输出，也不会进入 Module `validate` 请求。

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "config": "/data/ws/config.yml",
  "module_root": "/srv/anas/modules",
  "modules": ["samba_dc", "postgres", "authentik", "nextcloud"],
  "iam": {
    "provider": "authentik",
    "consumers": [{ "module": "nextcloud", "interface": "oidc" }]
  },
  "capability_bindings": { "nextcloud": { "relational_database": "postgres" } },
  "module_plans": {
    "samba_dc": {
      "requested_mode": "auto",
      "resolved_mode": "ad_zone",
      "zone": "example.net"
    }
  }
}
```

`modules` 是启动顺序，不是集合，顺序有意义。

`iam.provider` 在无人消费该能力时为 `null` 而不是缺席——调用方总能拿到这个键，
不必去分辨"没有 IAM"和"这个版本不报告 IAM"。

`module_plans` 也始终存在且为对象；没有 Module 返回 plan metadata 时为 `{}`。其第一层 key
是 Module 名，第二层是该 Module 的 `validate` Hook 返回并经 Core 接纳的只读、非敏感
`string -> string` 元数据。它不修改配置，也不表示执行了变更。人类输出使用按 Module 和 key
排序的 `module plan: <module> key=value ...` 行。

Module `validate` Hook 的非零退出、非法/未知 JSON 字段、mutation response 或不合规 plan
metadata 都属于配置无法通过当前期望状态校验：`anas plan` 返回 `config_invalid`（退出 4）。
下文的 `anas config plan` 使用同一边界。当前 CLI **没有** `validation_failed` error code，调用方
不得按尚未实现的代码分支。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `config_missing` | 4 | 配置文件不存在 |
| `config_invalid` | 4 | 配置无法解析、违反通用 schema，或 Module `validate` Hook 拒绝期望状态/返回非法 response |
| `module_root_missing` / `module_root_invalid` | 4 | 找不到 module 目录，或它读不出来 |
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
  "modules": [{ "name": "postgres", "version": "16.4.0", "revision": 1, "app_version": "16.4", "digest": "sha256:…" }],
  "iam": { "provider": null, "consumers": [] },
  "capability_bindings": {},
  "snapshot": { "backend": "btrfs", "keep_auto": 5 }
}
```

`modules` 是契约对锁的视图，不是锁文件的磁盘格式；磁盘格式可以变，这里不跟着变。

错误码同 `plan`，另有 `lock_invalid`（4，锁文件读不出来）与 `write_failed`（1）。

## render / build

```
anas render [-w WORKSPACE] [-c config.yml] [--update-lock] [--json]
anas build  [MODULE...] [-w WORKSPACE] [-c config.yml] [--update-lock] [--json]
```

产出一个不可变部署制品并封印它，但**不激活**。`build` 比 `render` 多一步构建镜像。

`build` 接受 module 名，只构建这些 module 的镜像；渲染始终是整个部署的，因为渲染一半不构成部署。`render` 不接受 module 名。

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
`unit` 为 `modules`（`seal` 为 `deployments`）。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `lock_missing` | 4 | 没有 config lock，且没给 `--update-lock` |
| `lock_stale` | 4 | 锁与源码里的 module 对不上；要显式 `anas lock` 更新 |
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
    "detail": { "blocked": ["samba_dc.admin_password (credential_rotate; rotate-samba-admin-password)"] }
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
| `credential_store_mismatch` | 4 | 目标 deployment 的凭据代次/authority 与 Store 不一致；不能用 `--allow-risky` 绕过 |
| `confirmation_required` | 3 | `--no-snapshot`、或无法快照仍要继续，而无从确认 |
| `start_failed` | 1 | 启动失败；已尽力把旧部署重新拉起 |

## start / restart / stop

```
anas start|restart|stop [MODULE...] [-w WORKSPACE] [--json]
```

不给 module 名就是整个部署。给出名字时，命令必须把目标展开为依赖安全的 chain，不能只操作点名的 module：

- `start MODULE...` 向前展开目标的全部直接、间接依赖，再按依赖正序启动。这样目标启动前，它需要的 provider、数据库等都已经运行。
- `stop MODULE...` 向后展开全部直接、间接依赖目标的 module，再按依赖逆序停止。这样不会在依赖已停止后留下仍在运行但已经出错的应用。
- `restart MODULE...` 使用与 `stop` 相同的依赖者 chain，先按依赖逆序全部停止，再按依赖正序全部启动。

多个目标分别展开后取并集、去重。最终顺序取自部署的冻结依赖顺序，而非命令行词序。`dependencies.after` 只在 chain 中两个 module 都已被选中时约束顺序，不会扩大 chain。名字不在本部署中是用法错误，并列出本部署实际有哪些 module。CLI 不提供绕过 chain、只操作单个依赖节点的选项。

例如依赖关系为 `postgres -> nextcloud -> collabora` 时，`anas restart postgres` 依次停止 `collabora、nextcloud、postgres`，再依次启动 `postgres、nextcloud、collabora`。`anas restart nextcloud` 不重启仍正常运行的 PostgreSQL，只重启 Nextcloud 及其依赖者 Collabora。

指定 chain 的停止**不拆 macvlan 网桥**：整体停止会拆（没人再用），停止一个 chain 不代表 chain 外的 module 不用它。

**没有天然结果的命令**，按 README 的"最小信封"办：

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "action": "stop",
  "deployment_id": "20260731T101500Z-a1b2c3d4",
  "modules": ["postgres", "authentik", "traefik"]
}
```

`modules` 是 chain 展开后实际操作涉及的 module，按依赖正序列出，不是"停/起了几个"的统计。调用方**不应**期待此外
的结果字段。未加 `--json` 时 stdout 为空。

进度 `phase`：停止为 `stop-containers`，启动为 `activate-modules`，`unit` 为
`modules`。一次 `activate-modules` 包含当前 Module 的容器启动、owned credential
probe/reconcile/verify 和 after-start/local-admin ready barrier；该屏障成功后才处理下游 Module，
不再把所有容器启动与 Hook 分成两个全局阶段。

三条生命周期命令取排他运行时锁。若存在未完成 credential transaction，它们先按 Store
`rotation_id/generation` 自动恢复 previous 或完成 candidate promotion，再执行用户请求。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `no_active_deployment` | 4 | 还没 apply 过 |
| `compose_missing` | 4 | 没有 docker compose |
| `deployment_unreadable` | 4 | 活跃部署的制品坏了或不在 |
| `credential_store_mismatch` | 4 | 活跃 deployment 的凭据代次/authority 与 Store 不一致；先恢复匹配快照 |
| `lock_failed` | 1 | 无法取得运行时锁，或 credential transaction 自动恢复失败 |
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
| `rollback_data_breaking` | 4 | 目标 module 声明了这次降级会重写磁盘数据；`--allow-risky` **不能**绕过 |
| `rollback_guarded_changes` | 4 | 跨越守卫变更；`--allow-risky` 可以绕过 |
| `credential_store_mismatch` | 4 | 目标 deployment 的凭据代次/authority 与 Store 不一致；`--allow-risky` 不能绕过，需恢复匹配快照 |

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
anas config list    [global|<module>]     [-w WORKSPACE] [-c config.yml] [--json]
anas config import  SOURCE [-w WORKSPACE] [--json]
anas config migrate        [-w WORKSPACE] [--json]
anas config set     <module.parameter> <value> [-w WORKSPACE] [-c config.yml] [--json]
anas config explain <module.parameter> [--json]
anas config plan    [-w WORKSPACE] [-c config.yml] [--json]
anas config secret  list | get <KEY>   [-w WORKSPACE] [--json]
```

`config list` 列出每个参数的 `set` 路径、环境变量、类型、输入/解析要求、默认来源、单字段
约束、当前值状态和变更影响。敏感参数的人类输出只报告 `<set>`/`<unset>`；JSON 只返回
`set: true`/`false` 并省略 `value`，绝不打印明文。Secret Store 中 kind 为
`lifecycle_managed` 的记录也按此方式报告 presence。读取凭据仍使用 `config secret get`。参数
声明属于 Module，不需要 workspace；在 workspace 中调用时还会填充当前值。

`parameters[].type` 取 `string`、`bool`、`int`、`enum` 或 `unknown`。`enum` 项同时
带非空 `allowed_values`；其他类型省略该字段。`unknown` 是读取旧 Module 和开发中不完整
声明的兼容值，内置 Module 的 release gate 禁止它进入发布，当前内置清单中数量必须为 0。
调用方仍应处理 `unknown`，不能把它当成已明确声明的自由文本 `string`。

每项都返回以下解析语义字段：

| 字段 | 契约 |
| --- | --- |
| `required` | `input_required` 的兼容别名；两者必须始终相等 |
| `input_required` | 操作者是否必须在该参数的所有适用场景显式提供非空输入；有默认值或其他无条件来源时必须为 `false` |
| `must_resolve` | 规范化并应用所有来源后，最终值是否必须非空；它可以在 `input_required: false` 时为 `true` |
| `has_default` | 是否声明了静态默认值；用来区分“无默认值”和“默认值明确为空字符串” |
| `default_source` | 省略输入时的无条件来源：`none`、`static`、`host`、`runtime`、`generated` 或 `inherited` |

`input_required: true` 的参数必须同时满足 `has_default: false` 和
`default_source: "none"`。`required: false` 不表示任意值都有效，也不表示解析后允许缺值。
兼容字段 `default` 仍是字符串，并可能显示当前宿主上可计算的 host 值；判断是否有静态
默认值必须读取 `has_default`，不能用 `default` 是否为空来猜。

`default_source: "none"` 只表示没有**无条件**来源，不推出 `input_required: true`。
例如 `ddns_go.dns_provider` 与 `ddns_updater.dns_provider` 可由 deployment
`dynamic_dns.dns_provider` 条件注入，所以两者都是 `input_required: false`、
`must_resolve: true`、`default_source: "none"`；resolver 未注入时再由校验要求 Module
侧提供值。

声明了单字段限制时，参数还返回 `constraints` 对象。对象只包含非空成员：整数使用
`minimum`、`maximum`，字符串使用 `min_length`、`max_length`、`pattern`、`format`；当前
`format` 值为 `iana_timezone`、`language_tag`、`locale` 或 `ipv4`。`type`、枚举值和这些
constraints 可在单个候选值上完成校验。条件必填、两个或更多字段之间的关系，以及依赖
workspace/运行态的规则仍由 resolver、应用服务、plan 或 Hook 校验，不能把其中一个字段笼统标为
`input_required`。

import 与 `config plan` 会运行同一个通用依赖/Capability/Contract resolver。只有调用方尚未
选择且 `auto` 暂时无法唯一解析的 binding 可以作为草稿延后；显式非法、unknown、disabled
或不受支持的 provider/interface 必须立即失败，不能留到 apply。provider/interface/backend/DNS
platform 这类结构 selector 不能来自 `secrets:` 或 lifecycle Secret Store：其规范标识必须进入
plan 与 resolution lock，无法同时承诺明文保密；调用方必须把 selector 移到普通配置，只把实际
凭据留在 secret 通道。拒绝诊断只显示 `<redacted>`。

这些字段是 ANAS 配置 schema 的 CLI 投影，**不是 JSON Schema 文档**：JSON Schema 的
`required` 是对象属性名数组，而这里是当前参数的输入要求；JSON Schema 的 `default` 只是
注解，而 ANAS 会在解析时真正应用默认值；`constraints` 也只支持上列稳定字段，不接受任意
JSON Schema 关键字。新增参数不能从默认值的 YAML scalar 类型反推类型，必须在 global
schema 或 Module manifest 中显式声明。

类型化值在持久化和送入运行时前会规范化：`bool` 写成小写 `true`/`false`，`enum` 写回
manifest 中的标准拼写，`int` 去除首尾空白，`string` 保留原文。这样旧配置中的大小写差异
不会让 selector 突然失效，也不会出现“校验通过但 Hook 对同一值作相反解释”。

`config import` 是已有 workspace 导入外部 YAML 的入口；首次创建也可用
`anas init WORKSPACE --config SOURCE` 完成同样的导入。两者都不修改源文件。导入先把
`env` 和 `secrets` 键写成运行时大写拼写，把 Module 名、global 参数名和 Module 参数名写成
小写；任何规范化后重复的地址都会让导入失败，不以 YAML 顺序决定胜负。`secrets` 内部，
以及映射到同一运行时键的 `secrets`、`env` 和结构化 Module 参数之间都执行该碰撞检查，
错误不回显敏感值。声明为 bare export 的结构化 Module 参数迁移到 canonical `env.<KEY>`：例如
`modules.samba_fs.config.share_dir_name` 持久化为 `env.SHARE_DIR_NAME`；若两种地址同时出现，
按碰撞拒绝。`secrets.<KEY>` 映射到已声明参数时仍使用同一类型、constraints、敏感性和变更
effect，不是无类型的校验逃生口。验证规范化配置后，`credential_rotate` 与本地管理员
bootstrap 密码移入
`.anas/secrets.yml`，普通 DNS/API token 等部署 Secret 则保留在 0600 的 workspace
`config.yml` 中。配置、
Secret Store 和完整性摘要先全部暂存，再一起替换；任一步失败都保留原状态。`migrate` 仅用于
把尚未建立 CLI 完整性摘要的当前 workspace 配置纳入该模型，不提供旧 Secret Store 兼容。
plan/lock/render/apply 拒绝外部 `-c` 和摘要不匹配的手工修改。

抽取后的 `lifecycle_managed` 输入不需要在重新导入受管 `config.yml` 时回填明文：已有的非空
Secret Store 值只在私有校验视图中满足 `input_required` 并按当前 schema 重新校验。显式重复
同一值是幂等操作；用不同值重新 import 会在写文件前拒绝，并要求使用声明的凭据轮换命令或
`anas admin local rotate`。kind 为 `generated` 或 `local_admin` 的记录不能满足无关的调用方输入
要求；缺失或不符合当前 constraints 的 `lifecycle_managed` 值同样使 import/plan 失败且不泄露
值。`config plan` 只输出变更路径、effect、sensitive 等元数据；来自 `secrets:` 的普通部署
Secret 保留其所有者声明的风险策略，任何 Secret Store 明文都不会进入 plan 输出。

所有 Secret Store kind 都是来源敏感 taint：若普通配置值与任一 store 明文相同，set/import/
plan/lock/apply 的错误及 list/plan 投影仍按敏感值处理；只有 `lifecycle_managed` 会实际合入
caller-input 视图。set、import/reimport、`config plan`、deployment lock/plan/materialize 和
remote lock 使用同一套 registry-aware schema；失败不得先改变 config、完整性摘要、Secret
Store 或 lock。

空 lock 的新 workspace，以及已有 lock 的 workspace 在 set/import 暂存新增 Module 时，都不
会执行尚未 pin 的 Module Hook；已 pin Module 仍按有效依赖顺序校验。`config plan`/`anas plan`
在首次 lock 前也只做静态 schema 与拓扑投影，不产生 Module validation metadata。后续显式
`anas lock` 是新 Module 的代码信任转换：它在
内存中计算候选 lock，执行扩展拓扑的 `validate`，只有校验成功才写 lock。这样既不会在摘要
校验前执行被篡改的 Hook，也不会让“先改 config / 先改 lock”形成新增 Module 的死锁。

`set` 和 `explain` 会拒绝 manifest 未声明的参数，指出最接近的已声明项并使用 usage 退出码。
映射到已声明参数的 raw `env.<KEY>` 接受相同的类型校验和规范化；只有 schema 完全不认识的
合法环境键保留宽容兼容入口。旧 Module 若只在 legacy `required` 中声明裸环境键，该键仍只
能通过 `env.<KEY>` 寻址，不会伪造成无效的 `<module>.<parameter>`。

`set` 与 `explain` 共用同一个完整 `setting` 形状，包括 `type`、枚举时的
`allowed_values`、`env_key`、输入/解析要求、默认来源和单字段 constraints。类型和元数据
含义与 `config list` 相同：

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "config": "/data/ws/config.yml",
  "setting": {
    "path": "samba_dc.admin_password", "module": "samba_dc",
    "parameter": "admin_password", "type": "string",
    "env_key": "SAMBA_DC_ADMIN_PASSWORD",
    "required": false, "input_required": false, "must_resolve": true,
    "has_default": false, "default_source": "generated", "default": "",
    "effect": "credential_rotate", "apply": "rotate-samba-admin-password",
    "sensitive": true, "description": "…"
  }
}
```

`setting.path` 是**配置项的点分路径**，不是文件系统路径——README 的"路径一律绝对"
不适用于它。

`explain` 不需要 workspace，只读 module 注册表。

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
  }],
  "module_plans": {
    "samba_dc": {
      "requested_mode": "auto",
      "resolved_mode": "ad_zone",
      "zone": "example.net"
    }
  }
}
```

`change` 取 `add` / `remove` / `change`，是枚举而非为句子挑的动词。
`applied_at` 在从未成功启动过时为 `null`。
`module_plans` 与 deployment `plan` 的字段语义相同，且独立于 `changes`；即使
`matches_last_start: true`，已选择 Module 的校验 metadata 仍可出现。人类输出同样追加排序后的
`module plan: <module> key=value ...` 行。Module 校验失败统一返回 `config_invalid`（退出 4），
不返回 `validation_failed`。

`secret list` **只返回键名**，`secret get` 返回值。理由见 README 的"敏感值"。

`secret get KEY -w WORKSPACE` 与 `secret get -w WORKSPACE KEY` 必须等价。此前
前者用标准 flag 解析、在 `KEY` 处停下，`-w` 被静默丢弃，命令转而去读当前目录或
`ANAS_WORKSPACE` 指的那个 workspace 的 secret——在操作者以为问的是另一个部署的
时候读出这一个部署的密钥，不是可以交给参数顺序去决定的事。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `usage` | 2 | 路径格式不对、模块未知、参数个数不对 |
| `config_missing` | 4 | 配置文件不存在 |
| `config_invalid` | 4 | 配置无法解析、违反通用 schema，或 Module `validate` Hook 拒绝期望状态/返回非法 response |
| `lock_invalid` | 4 | 已有 lock 文件无法解析 |
| `lock_stale` | 4 | 已 pin Module 的 version/revision/bundle digest 与当前注册表不一致 |
| `secret_missing` | 4 | 没有这个生成的 secret |
| `secrets_unreadable` | 4 | secret store 读不出来 |
| `state_unreadable` | 4 | 已应用设置指纹等 workspace 状态无法读取 |
| `write_failed` | 1 | 写配置失败 |

## credential

```text
anas credential list [-w WORKSPACE] [--json]
anas credential rotate CREDENTIAL_ID [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]
anas credential rotate --module MODULE [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]
anas credential rotate --all [-w WORKSPACE] [--force] [--dry-run] [-y] [--json]
```

`list` 读取 active deployment 冻结的机器凭据库存。它只返回逻辑身份和状态，不返回值、hash、
verifier 或其他可用于离线猜测的摘要。当前接入范围是声明了
`credentials.provides/consumes` 的 deployment 凭据；resource credential、本地管理员和外部
API token 仍分别由原有库存/配置边界管理，不能因为 Secret Store 中存在一个值就被宣称为可轮换。

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "deployment_id": "20260820T010203Z-deadbeef",
  "credentials": [{
    "id": "eturnal.secret", "owner": "eturnal",
    "consumers": ["netbird", "nextcloud"], "kind": "shared_secret",
    "authority": "anas", "generation": 2,
    "rotation_mode": "reconcile", "status": "rotatable"
  }]
}
```

`status` 取 `rotatable` / `manual` / `unsupported` / `orphaned` /
`recovery_required`。有未完成事务时，`list` 仍可读，并额外返回不含明文的
`recovery: {transaction_id, phase, credentials[]}`。

`--dry-run` 与实际执行共用 planner，并读取 active state 与 Secret Store presence/generation；
它不生成随机数、不创建 candidate、不写 journal/Store、不调用 Hook 或 Docker。结果中的
`executable` 表示 blockers 是否为空：

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "workspace": "/data/ws", "dry_run": true, "executable": true,
  "plan": {
    "previous_deployment": "20260820T010203Z-deadbeef", "scope": "single",
    "credential_order": ["eturnal.secret"],
    "affected_modules": ["eturnal", "netbird", "nextcloud"],
    "stop_order": ["nextcloud", "netbird", "eturnal"],
    "activation_order": ["eturnal", "netbird", "nextcloud"],
    "blockers": [], "manual": [], "force": false, "all": false
  }
}
```

实际执行要求 active runtime 为 `running`，并在非交互调用中要求 `-y`。Runner 先生成独立
candidate projection，停止 previous 的全部或受影响闭包，按 ready barrier 顺序执行
probe/reconcile/verify；只有全部验证通过后才用一次原子 Store Save 提交值、generation 与
`rotation_id`，随后 promotion candidate。`--module MODULE` 选择该 Module owner 在 active
deployment 中声明的完整统一 lifecycle 集合；集合中的 manual/unsupported 项会阻断整个批次。
`--all` 选择 active deployment 中所有可执行
`reconcile` 目标；manual 目标不会被悄悄改写，也没有 `--allow-partial`。

`--force` 仅接管同时具备 ANAS generator、完整 probe/reconcile/verify 和有效 owner/consumer
图的 external authority 记录。它不是跳过 blocker 的通用开关。

成功 JSON 同时返回 `plan` 与 `rotation`；`rotation.status` 为 `complete`。失败使用
`error.detail.rotation.status` 区分 `not_started`、`candidate_failed`、`previous_restored`、
`previous_restore_failed` 与 `recovery_required`。Store 提交前的失败先停止 candidate 并恢复
previous；Store 提交后不再回写旧 Store，而由下一次排他运行时操作依据 journal 与
`rotation_id/generation` 自动完成 candidate promotion。任何 journal、JSON、进度或 Hook stderr
都不得包含凭据值。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `usage` | 2 | 子命令、目标数量或 `ID`/`--module`/`--all` 组合不合法 |
| `confirmation_required` | 3 | 实际轮换在非 TTY 中未给 `-y`，或交互确认被拒绝 |
| `no_active_deployment` / `deployment_unreadable` | 4 | 没有可读取的 active deployment |
| `credential_recovery_required` | 4 | dry-run 发现未完成事务；只读 list 仍可报告恢复状态 |
| `credential_rotation_blocked` | 4 | planner、运行状态或 Store generation/presence preflight 有 blocker |
| `runtime_lock_failed` | 4 | 无法取得 workspace 运行时锁或读取待恢复 journal |
| `compose_missing` | 4 | 实际执行需要 Compose，但宿主不可用 |
| `credential_rotation_failed` | 1 | candidate、补偿、Store commit 或 promotion 失败；具体状态见 detail |

## admin local

```text
anas admin local list [-w WORKSPACE] [--json]
anas admin local credential MODULE [ACCOUNT] [-w WORKSPACE] [--password-only | --json]
anas admin local rotate MODULE [ACCOUNT] [-w WORKSPACE] [--prompt] [--json]
```

`list` 是不泄密的安全库存，只返回 Module、账号 id、用途、用户名和当前入口；不存在本地
账号时返回空列表。`credential` 是显式敏感读取，同时返回入口、用户名、密码和用途。
`--password-only` 供交互式管道使用，输出一行裸密码；它不能与 `--json` 组合。

`ACCOUNT` 始终是 Module Manifest 的 `management.local_accounts[].id`（例如 `primary`、
`break_glass`），不是物理用户名。省略时依次选择 id 为 `primary` 的账号、该 Module 的唯一
账号；其余多账号情形报歧义错误。`rotate` 默认生成随机密码；`--prompt` 只能从真实 TTY
无回显读取并要求二次确认。命令不接受密码参数、环境变量或 YAML 明文输入。

轮换先把候选 Secret 仅交给活动部署冻结的 Module handler。handler 更新应用内部状态并
验证新凭据；验证失败必须恢复旧应用状态。只有成功后 Runner 才原子提交
`secrets.yml`（bootstrap-only 应用同时更新受限的 runtime Secret 投影）。未声明
`lifecycle.rotate` 的 Module 明确报不支持，不能由 CLI 猜测一个更新方式。

账号名称锁保存在 `.anas/local-admins.yml`，密码只由 `secrets.yml` 持久保存，
两者都不进入 `config.lock.yml`、deployment manifest 或部署 `.env`。Runner 仅在 Module
Hook 执行期间注入明文；bootstrap-only 应用通过 `.anas/runtime-secrets/` 下的 0600 临时
投影读取，支持 hash 的应用制品只持久化 hash。

| code | 退出码 | 何时 |
| --- | --- | --- |
| `usage` | 2 | 子命令、参数数量或 flag 组合错误 |
| `confirmation_required` | 3 | `--prompt` 在非 TTY 中使用、两次输入不一致或密码策略不满足 |
| `local_admin_missing` | 4 | Module 无本地账号，或有多个账号但未指定 id |
| `local_admin_state_unreadable` | 4 | 本地管理员库存无法读取 |
| `secret_missing` | 4 | 库存存在，但对应随机密码缺失 |
| `secrets_unreadable` | 4 | Secret Store 无法读取 |
| `local_admin_rotate_failed` | 1 | handler、验证、回滚或 Secret 提交失败 |

## module

```text
anas module list [--source NAME] [-w WORKSPACE] [--json]
anas module versions NAME [--source NAME] [-w WORKSPACE] [--json]
anas module install NAME@VERSION-rN [--source NAME] [--digest sha256:...] [--json]
anas module sync [-w WORKSPACE] [--source NAME] [--json]
anas module update [MODULE...] [-w WORKSPACE] [--source NAME] [--json]
```

`list` 读取 `anas.module-catalog/v1`；`versions` 读取 Module OCI repository 的标准 tag
list，过滤 `<semver>-r<N>` 并按 SemVer、revision 降序排列。catalog 只给发现入口和当前
release，历史版本的唯一真相源仍是 Registry tag list。

`install` 下载一个明确 release。可选 `--digest` 要求 tag 当前解析到给定 OCI manifest
digest。安装依次校验 artifact/layer media type、manifest/layer digest、`package.yml`
身份和解包 `content_digest`；绝对路径、`..`、重复路径、symlink/hardlink 和设备节点一律
拒绝。结果进入用户级内容寻址缓存，不直接修改 workspace 或 lock。

`update` 是改变 lock 中 Module 版本的唯一普通入口。它根据 `modules.<name>.version` 或
catalog 当前 release 解析完整依赖闭包，写入 OCI manifest、content 和安装树三个 digest，
在内存候选 lock 上执行 defaults 与 Module `validate` 后，再生成 workspace Module 视图。
指定 Module 名时，未指定且已有远程 lock 的 Module 保持原
release；不指定时更新配置直接选择的 Module。`sync` 只按现有 lock 恢复缺失缓存和视图，
不会升级；lock 中的本地 `bundle:<name>` 不会被它偷偷替换为 Registry 包。

当前 lock 与 `module-view.json` 仍是两个独立路径：命令返回写入错误时会恢复两者的旧内容，
但进程在两次 rename 之间被 `SIGKILL` 或掉电，仍可能留下跨代组合；后续 digest/trust gate 会
fail closed。彻底消除该窗口需要后续改为单一 generation pointer，并同时串行化 remote writer
与 reader；在该存储升级完成前，不应并发运行 `module sync/update` 与其他 workspace 写命令。

`--source` 的优先级高于 workspace `module_source`。无二者时使用 `official`。缓存默认在
用户 cache 目录的 `anas/modules/` 下，`ANAS_MODULE_CACHE` 可覆盖。`--module-root` 与
`ANAS_MODULE_ROOT` 仍优先于 workspace 远程视图，供源码开发使用。

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": true,
  "source": "official-cn",
  "module": "nextcloud",
  "release": "34.0.2-r4",
  "oci_digest": "sha256:…",
  "content_digest": "sha256:…",
  "path": "/home/user/.cache/anas/modules/unpacked/sha256/…"
}
```

| code | 退出码 | 何时 |
| --- | --- | --- |
| `usage` | 2 | 子命令、source、release 或参数格式错误 |
| `lock_missing` / `lock_invalid` | 4 | `sync` 没有可用 lock |
| `config_not_managed` / `config_invalid` | 4 | `update` 的 workspace 配置未受 CLI 管理或内容非法 |
| `module_lock_local` / `module_lock_mismatch` | 4 | lock 来源不是 OCI，或缓存内容与 lock 不一致 |
| `module_not_found` | 4 | 配置选择的 Module 不在 catalog |
| `module_root_invalid` / `contract_root_invalid` / `contract_invalid` | 4 | 已安装包的 Module/Contract 定义无法载入或校验 |
| `resolution_failed` / `version_conflict` / `snapshot_policy_invalid` | 4 | 依赖、精确版本、能力绑定或宿主策略无法解析 |
| `module_source_unavailable` / `module_versions_unavailable` | 1 | catalog、tag list 或所有回退源不可用 |
| `module_install_failed` / `module_sync_failed` / `module_update_failed` | 1 | 下载、认证或多层 digest/包校验失败 |
| `module_cache_unavailable` / `module_cache_corrupt` | 1 | 缓存目录不可用，或内容寻址记录/内容损坏 |
| `module_view_failed` / `write_failed` / `lock_update_failed` | 1 | workspace 视图或 lock 解析/事务写入失败 |

## help

```
anas help [--json]
```

人类可读的帮助是散文，**不是契约**。`--json` 下改为列出可调用的命令：

```json
{
  "api_version": "anas.dev/cli/v1", "ok": true,
  "commands": ["init", "plan", "lock", "render", "…"],
  "module_abi": "anas.module-hook/v1"
}
```

存在的理由只有一个：`anas help --json` 不能是 stdout 上唯一一处不是 JSON 文档的
地方。
