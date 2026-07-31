# CLI 契约

本目录定义 `anas` 命令行的**机器可读输出契约**。这些契约面向非交互式调用方——
将来的 web 服务、定时任务、以及 CLI 自己的交互式模式（交互式模式不另写一套逻辑，
它只是"探测 + 提问 + 调用非交互式路径"）。

> **状态：`snapshot` 已实现，`backup` 仍是设计。** 通用约定（流分离、退出码、
> 枚举、时间与大小、路径、版本）由 `snapshot` 落地，实现见
> [cli.go](../../internal/runner/cli.go)。
> 实现落地时若与此处不符，以本目录为准修改代码，或先修改本目录。

## 索引

| 文档 | 覆盖命令 |
| --- | --- |
| [backup.md](backup.md) | `anas backup capabilities` / `plan` / `create` / `list` / `restore` / `verify` |
| [snapshot.md](snapshot.md) | `anas snapshot list` / `show` / `create` / `restore` / `pin` / `unpin` / `delete` / `prune` / `verify` / `path` |

## 通用约定

以下规则适用于所有支持 `--json` 的命令，各文档不再重复。

### 流分离

- **结构化结果走 stdout**，且 stdout 上**只有**一个 JSON 文档，调用方可以直接
  `JSON.parse(stdout)`，不需要按行切分或过滤。
- **进度、日志、警告走 stderr**。
- 未加 `--json` 时 stdout 是人类可读文本，格式不构成契约，不要解析。

### 进度输出

长操作（`backup create`、`backup restore`、`snapshot create`）在 `--json` 下向
**stderr** 按行输出 JSON 对象（JSON Lines），每行一个：

```json
{"type":"progress","phase":"send-data","current":734003200,"total":1395864371,"unit":"bytes"}
{"type":"progress","phase":"stop-containers","current":8,"total":13,"unit":"containers"}
{"type":"warning","code":"plaintext_secrets_leaving_host","message":"..."}
```

`phase` 取值由各命令文档定义。`total` 未知时省略该字段，不要写 `0` 或 `-1`。

### 确认

- 需要确认的操作，`-y` / `--yes` 是唯一的绕过方式。
- **未给 `-y` 且 stdin 不是 tty 时直接以退出码 3 失败**，绝不阻塞等待输入。
  这是非交互式调用方最需要的保证：命令要么完成，要么立刻返回。

### 退出码

| 码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 一般错误（执行中失败） |
| 2 | 用法错误（参数缺失、互斥、无法识别） |
| 3 | 需要确认但未提供 `-y`，或 stdin 非 tty |
| 4 | 前置条件不满足（非 btrfs、快照缺失、目标不可写、权限不足） |

退出码非 0 时，stdout 仍输出一个 JSON 文档：

```json
{
  "ok": false,
  "error": { "code": "dest_not_writable", "message": "…", "detail": {"path": "/mnt/backup"} }
}
```

成功时顶层为 `{"ok": true, ...}`。

### 错误码与原因码是枚举，不是文案

所有 `code`、`reason` 字段取**固定的机器可读枚举值**（`snake_case`），不是自由文本。
`message` 是给人看的英文说明，**调用方不应解析它**。CLI 自己持有一张枚举 → 中文的
映射表用于人类可读输出；web 层持有自己的映射表。新增语言不需要改动任何输出逻辑。

枚举值只增不改。要淘汰一个值时保留它并在文档中标注 deprecated。

### 时间与大小

- 时间一律 RFC 3339 UTC 字符串（`2026-07-29T08:15:04Z`）。
- 大小一律**字节整数**，字段名以 `_bytes` 结尾。不做单位换算，不输出 `"1.3G"`。

### 路径

一律绝对路径。相对路径不出现在任何 JSON 输出中。

### 版本

每个 JSON 文档顶层带 `"api_version"`，格式 `anas.dev/cli/v1`。字段新增不升版本，
字段删除或语义变更升版本。
