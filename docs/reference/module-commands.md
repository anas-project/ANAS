# Module 专属命令

> 状态：M1/M2 已实现 manifest 校验、deployment 冻结、只读发现、共享执行服务、严格 ABI、锁和
> `anas module commands|invoke`，且 anasd M0 已提供只读 list/detail；anasd invoke/job 与 Forgejo/Incus 命令按
> [实施计划](/plans/module-command-capability)后续里程碑交付。

Module Command 允许少数 Module 发布自身特有的管理员操作，例如 Forgejo Runner project 诊断。
它不替代 Core 的 start/stop/restart、自动 lifecycle hook 或 Module 间 Contract operation，也不提供
任意 shell、argv、Docker、systemd 或 SSH 透传。

## Manifest

声明命令的 Module 必须支持独立 ABI：

```yaml
abi:
  supports:
    - anas.module-hook/v1
    - anas.module-command/v1

management:
  command_executor:
    command: [go, run, ./command]
  commands:
    - id: doctor
      title: Inspect service
      description: Inspect the configured service without changing it.
      handler: doctor
      mode: query
      risk: normal
      runtime_state: any
      lock: module_read
      timeout_seconds: 20
      cancellable: true
      parameters:
        - name: verbose
          title: Verbose diagnostics
          description: Include additional non-sensitive checks.
          type: bool
          default: false
      env: [EXAMPLE_ENDPOINT]
      secrets: [EXAMPLE_MAINTENANCE_KEY]
```

源码 Module 可以按与 Hook 相同的方式声明 `go run ./command`；render 和 Registry 打包会编译固定
平台二进制，活动 deployment 不依赖 Go 工具链。也可以声明一个包内固定、已有 executable bit 的
相对文件。一个 Module v1 只能有一个 executor，调用时由固定 `handler` 在进程内分派。

命令 ID 使用 lower kebab-case，参数名使用 lower snake_case。参数复用配置 schema 的
`string`、`int`、`bool`、`enum` 和对应单字段 constraints；参数必须是非敏感扁平标量。Secret 只能
从 `secrets` 白名单投影，调用方不能提供值或键名。

`query` 必须使用 `module_read`；`change` 使用 `module_write` 或 `workspace_write`。`destructive` 只能
用于 change，invoke 必须确认。timeout 范围为 1—3600 秒；取消策略为 `false`、`true` 或
`safe_points`。

## 发现

```text
anas module commands [MODULE] [-w WORKSPACE] [--json]
```

发现只读取活动 deployment 中冻结的 descriptor 和 executor，不读取当前源码或 Module cache，也不
连接远程服务。结果包含 Module/release、公开说明、参数、command digest、`available` 和稳定不可用
原因；不包含 handler、executor 路径、env/Secret key 或值。

没有活动 deployment 或所有 Module 都没有命令时成功返回空列表。指定的 Module 不在活动 deployment
中时返回 `module_not_active`。executor 缺失、不可执行、摘要不匹配或运行态不满足时保留命令说明，
并将 `available` 设为 `false`。

稳定不可用原因包括 `descriptor_invalid`、`descriptor_digest_mismatch`、`runtime_state_mismatch`、
`executor_missing`、`executor_digest_mismatch`、`missing_env` 和 `missing_secret`。调用方应按枚举分支，
不要解析人类说明。

## 调用

```text
anas module invoke MODULE COMMAND [-w WORKSPACE] [--param NAME=VALUE]... [-y] [--json]
```

CLI 先从活动 deployment 准备命令，并用同一个 application service 按 descriptor 规范化 bool、int、
string 或 enum 参数。重复、未知、缺失和非法参数在 executor 启动前失败。确认后服务会获取声明的
`module_read`、`module_write` 或 `workspace_write` 锁，再次核对活动 deployment 与 command digest，
然后读取白名单 env/Secret 并执行被冻结的二进制。

`destructive` 命令在 TTY 显示 title、description、deployment、Module release 和规范化参数；非 TTY
必须提供 `-y`。成功结果包含 `deployment_id`、Module、command、`changed` 和扁平公开 `result`。
executor 可以用 `changed:false` 表达重复调用已经收敛。

## Executor ABI

Core 以空进程环境和固定 argv 启动 executor，不继承代理、Docker 或 anasd session 环境。stdin 是一条
`anas.module-command/v1` JSON request，包含随机 invocation ID、冻结的 Module/release、deployment、
command/handler、规范化参数，以及 Core 按声明选择的 env/Secret 映射；不包含 workspace 路径或调用方
选择的键名。

stdout 限制为 1 MiB 严格 JSON Lines：零到多个 `progress`/`warning` 后必须恰好一个 `result`，且不能有
尾随记录。未知字段/record、非法进度、嵌套或小数结果、缺失终态与敏感值回显都会失败。原始 stderr
始终丢弃；CLI 只把验证后的 progress/warning 写到 stderr，在 `--json` 下 stdout 仍只有一个
`anas.dev/cli/v1` 最终信封。timeout 或 context 取消会终止整个 executor 进程组，不能把未知外部状态
报告为成功。

## anasd

当前只读 M0 可以通过注册的 workspace ID 发现同一批活动命令：

```text
GET /api/v1/workspaces/{ws}/modules/{module}/commands
GET /api/v1/workspaces/{ws}/modules/{module}/commands/{command}
```

HTTP 使用独立的 `anas.dev/api/v1` DTO，而不是复用 CLI 信封；DTO 同样不含 handler、executor、输入键、
注入值或宿主路径。当前未认证监听器不提供 POST invoke。后续写入口必须把同一个
`application.ModuleCommandService` 接到认证、角色、job、审计、digest/幂等键和 destructive 重认证，
不能调用 `anas module invoke --json` 子进程。

## 安全边界

- command descriptor、executor 路径和摘要冻结进 `deployment.yml`，二进制进入 Module render digest；
- 命令声明本身不授予 root、sudo、Linux capability、Docker socket 或 systemd 权限；
- 本机高权限动作必须经固定目标/动词的 Core named helper；远程维护使用独立最小权限凭据；
- `anasd` 当前 M0 未认证且只读，只开放 list/detail、不开放 invoke；后续 HTTP adapter 必须复用 application service、job、
  认证、角色和审计，不能执行 `anas --json` 子进程。

完整规范来源是[Module 专属命令能力要求](/requirements/module-command-capability)。
