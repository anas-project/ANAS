# CLI 契约

本目录定义 `anas` 命令行的**机器可读输出契约**。这些契约面向外部非交互式调用方、
定时任务，以及 CLI 自己的交互式模式（交互式模式不另写一套逻辑，它只是“探测 +
提问 + 调用非交互式路径”）。ANAS 自己的 Web API 不通过子进程消费这些 JSON 文档；
它与 CLI 共享带类型的 Go 应用层。CLI 契约仍是必须保持的外部兼容边界，也是验证两个
适配器行为一致的黑盒回归基线。

> **状态：全部命令均已实现。** 通用约定（流分离、退出码、枚举、时间与大小、
> 路径、版本）由 `snapshot` 落地，实现见
> [cli.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/cli.go)；分发与统一错误封装见
> [runner.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/runner.go) 的 `Main`。
> 实现落地时若与此处不符，以本目录为准修改代码，或先修改本目录。

## 索引

| 文档 | 覆盖命令 |
| --- | --- |
| [backup.md](backup.md) | `anas backup capabilities` / `plan` / `create` / `list` / `restore` / `verify` |
| [snapshot.md](snapshot.md) | `anas snapshot list` / `show` / `create` / `restore` / `pin` / `unpin` / `delete` / `prune` / `verify` / `path` |
| [commands.md](commands.md) | `anas init` / `plan` / `lock` / `render` / `build` / `apply` / `start` / `restart` / `stop` / `rollback` / `status` / `deployments` / `config` |

## 通用约定

以下规则适用于所有支持 `--json` 的命令，各文档不再重复。

### 流分离

- **结构化结果走 stdout**，且 stdout 上**只有**一个 JSON 文档，调用方可以直接
  `JSON.parse(stdout)`，不需要按行切分或过滤。
- **进度、日志、警告走 stderr**。这条对**子进程**同样成立：`docker compose` 的
  输出是日志而不是结果，一律接到 stderr。曾经把它接到本进程 stdout，任何启动
  容器的命令都会在 JSON 文档前面堆上一串拉镜像的行，"stdout 只有一个文档"
  当场失效。
- 未加 `--json` 时 stdout 是人类可读文本或为空（见"没有结果的命令"），格式不构成
  契约，不要解析。

### 进度输出

耗时较久的命令在 `--json` 下向 **stderr** 按行输出 JSON 对象（JSON Lines），
每行一个，每行自成一个完整 JSON 值——调用方在操作**进行中**逐行读取，不能等一个
只在结束时才出现的右括号：

```json
{"type":"progress","phase":"send-data","current":734003200,"total":1395864371,"unit":"bytes"}
{"type":"progress","phase":"stop-containers","current":8,"total":13,"unit":"modules"}
{"type":"warning","code":"plaintext_secrets_leaving_host","message":"..."}
```

`phase` 取值由各命令文档定义；哪些命令输出进度也由各命令文档定义，此处不再维护
一份会过时的清单。`total` 未知时**省略**该字段，不要写 `0` 或 `-1`——两者调用方
都得当成真实数量特判一次。

### 确认

- 需要确认的操作，`-y` / `--yes` 是唯一的绕过方式。
- **需要确认、未给 `-y`、且 stdin 不是 tty 时，立即以退出码 3 失败**，绝不阻塞
  等待输入。这是非交互式调用方最需要的保证：命令要么完成，要么立刻返回。
- **stdin 不是 tty 本身不是错误。** 退出码 3 只在"确实需要一次确认而无从取得"时
  出现。`anas status --json > out.json` 的 stdin 同样不是 tty，它必须正常退出 0。
  （早先的措辞是"未给 `-y` 且 stdin 非 tty"，字面照做会让全部非交互式调用无法
  使用——绝大多数命令根本不需要确认。）

### 退出码

| 码 | 含义 |
| --- | --- |
| 0 | 成功 |
| 1 | 一般错误（**已经开始干活**之后失败） |
| 2 | 用法错误（参数缺失、互斥、无法识别、取值非法） |
| 3 | 需要一次确认，而 `-y` 未给且 stdin 不是 tty |
| 4 | 前置条件不满足（**还没开始干活**：非 btrfs、快照缺失、目标不可写、权限不足） |

**1 与 4 的分界是"活儿有没有开始"**，不是错误有多严重。缺少配置文件、没有活跃
部署、锁文件过期、部署未就绪都是 4——环境不具备条件，调用方可以修好之后重试；
渲染失败、hook 失败、compose 启动失败是 1——已经动手了，中途出的事。

**2 与 4 的分界是"错在命令行还是错在机器上"**。`-w /nowhere` 是 2：这个参数本身
就不对。`start` 时没有活跃部署是 4：参数没问题，机器还没准备好。

退出码非 0 时，stdout 仍输出一个 JSON 文档，且**同样带 `api_version`**：

```json
{
  "api_version": "anas.dev/cli/v1",
  "ok": false,
  "error": { "code": "dest_not_writable", "message": "…", "detail": {"path": "/mnt/backup"} }
}
```

成功时顶层为 `{"api_version": …, "ok": true, ...}`。

`detail` 可选，取值由各命令文档定义；没有结构化补充信息时整个字段省略，不要写
`{}` 或 `null`。

### `ok` 回答的是问题，不是"命令跑没跑完"

大多数命令里 `ok: true` 就等于"做成了"。查验类命令（`snapshot verify`、
`backup verify`）不同：它们**成功地**查出了问题，此时 `ok: false` 且退出码为 1。
这是有意的——这两个命令是给 cron 用的，只看退出码是常态，"报告成功、正文说有问题"
正是一个缺失的 subvolume 一直没人发现、直到需要它那天的经过。

反过来，`snapshot show` 一个损坏的快照是 `ok: true`：命令回答了"这个快照是什么"
这个问题，健康与否在 `problems` 里，判定健康是 `verify` 的职责。把两者混在一起，
"show 一个损坏的快照"和"show 一个不存在的快照"就再也分不开了。

### 错误码与原因码是枚举，不是文案

所有 `code`、`reason` 字段取**固定的机器可读枚举值**（`snake_case`，正则
`[a-z][a-z0-9_]*`），不是自由文本。`message` 是给人看的英文说明，**调用方不应
解析它**。CLI 自己持有一张枚举 → 中文的映射表用于人类可读输出；web 层持有自己的
映射表。新增语言不需要改动任何输出逻辑。

枚举值只增不改。要淘汰一个值时保留它并在文档中标注 deprecated。

以下几个码由分发层统一产生，任何命令都可能返回，各命令文档不再重复：

| code | 退出码 | 何时 |
| --- | --- | --- |
| `usage` | 2 | 参数缺失、互斥、无法识别、取值非法；未知命令或未知子命令 |
| `confirmation_required` | 3 | 需要确认而无从取得，或交互式下用户拒绝 |
| `internal` | 1 | 未归类的错误逃到了顶层。**出现即是缺陷**，应补上具体的码 |

### 没有结果的命令

有些命令没有天然的结果：`stop` 要么把服务停了，要么没停。它们输出**最小信封**
——`api_version`、`ok`，加上足以说明操作对象的标识（workspace、deployment id），
仅此而已。

不要为了让文档"看起来有分量"而编造载荷。编出来的字段调用方一定会依赖，之后就
再也删不掉。同样地，调用方**不应**等待这些命令给出结果字段。

未加 `--json` 时这些命令的 stdout 为空，这是正确的行为，不是缺陷。

### 时间与大小

- 时间一律 RFC 3339 UTC 字符串（`2026-07-29T08:15:04Z`）。没有这个时刻时写
  `null`，不要写空串——空串和"未发生"调用方分不开。
- 大小一律**字节整数**，字段名以 `_bytes` 结尾。不做单位换算，不输出 `"1.3G"`。
- **量不出来时写 `null`，不是 `0`。** 没有 btrfs qgroup 就没有测量，`0` 是一个
  假的测量值。字段仍然出现（`"size_bytes": null`），调用方总能拿到这个键。

### 路径

**文件系统路径**一律绝对路径，相对路径不出现在任何 JSON 输出中。

这条只管文件系统路径。`config explain` 的 `path` 字段是配置项的点分路径
（`global.domain`），不是文件系统路径，不受此约束——按字段名字面套规则会把它
当成 bug。

### 敏感值

`config secret list` 只返回键名。它的用途是"看看生成了哪些 secret"，把值一起
返回会让一条例行的清点命令把全部密钥泄进任何抓走了它输出的地方。

`config secret get` 是唯一在文档里返回明文 secret 的命令，这是它的全部用途。
调用方按此对待：它的 stdout 与任何别的命令的 stdout 敏感级别不同。

### 版本

每个 JSON 文档顶层带 `"api_version"`，格式 `anas.dev/cli/v1`——**成功和失败的
文档都带**。字段新增不升版本，字段删除或语义变更升版本。

### 一致性由测试守着

- [internal/runner/contract_test.go](https://github.com/anas-project/ANAS/blob/master/internal/runner/contract_test.go)
  表驱动走一遍所有不需要真实部署的命令：stdout 恰好一个文档、信封字段齐全、
  退出码逐个断言到具体数字。
- [test-env/scripts/test-contract.sh](https://github.com/anas-project/ANAS/blob/master/test-env/scripts/test-contract.sh)
  用**编译出来的二进制**重跑退出码部分。`go run` 会把所有非零状态压成 1，
  Go 测试也只能看到返回的 error——进程真正退出的那个数字只有这里能看见。
