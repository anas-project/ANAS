# Incus 实现接续与测试核对

> 状态：历史记录；2026-09-18，基线 `49bbf45` 的干净 `master`，随后直接修改该 checkout。
> 本记录区分已执行测试、交叉编译与未实现/未实测项，不是生产放行或全部需求完成声明。

## 范围与来源

本轮先阅读[需求索引](../requirements/index.md)、[计划索引](../plans/index.md)、
[Incus 需求](../requirements/incus-module.md)、[Incus 计划](../plans/incus-module.md)和
[宿主供给架构](../../docs/architecture/incus-host-provisioning.md)，再对照源码执行测试与接续修改。
实现进度仍由 Incus 计划维护；未更改需求矩阵、Module `developing` 或生产 ingress 禁用边界。

## 确认并修复的问题

| 已证实问题 | 修改 | 验证范围 |
| --- | --- | --- |
| 两份 action ABI 测试定义同名 `regressionTerminal`，阻止编译 | 边界测试使用独立 helper 名 | `internal/actionabi` 本机测试 |
| dispatcher fixture 依赖 TempDir 模式，未满足正式 Store 的 0700 要求 | fixture 显式 chmod，正式权限检查不变 | `internal/jobexecutor` 本机测试 |
| native Module 进程入口使用 `exec.Command`，不满足 daemon 子进程清单门禁 | daemon-owned deadline 的 `CommandContext`；关闭其默认 Cancel，继续由原 pidfd/group 监督收敛 | `cmd/anasd` 门禁；Linux 原生监督待验收 |
| `incus.endpoint` 未标敏感，与已完成的需求和文档不符 | Module policy 补敏感标记；用真实注册表测试列表输出 | canonical config 和 `config list` 回归 |
| 配置产生 `*_CERTIFICATE_B64`，Hook 却读 `*_CERT_B64`，既有 fixture 掩盖断层 | Hook 校验规范字段后派生 wire 别名；拒绝以旧别名替代规范输入 | Hook 配置投影、旧别名和不完整配置回归 |
| Consumer 仅对私钥作显式敏感标记 | endpoint、server/client certificate 一并标敏感 | compute consumer 投影回归 |
| Provider transport 可能回显 endpoint、daemon 原始错误或 metadata 类型错误 | 固定错误类别、取消身份保留、4 MiB 限制、严格 origin、禁止重定向、同步成功证明 | TLS fixture、重定向、超限、解析及错误脱敏回归 |
| X.509 解析非法 SAN URI 时原始错误直接包含证书中的 URI，已通过失败测试复现 | 共享证书解码器仅返回固定类别，覆盖管理 pin 与消费者 trust 输入 | 保留先验证标准库确实回显的恶意 SAN fixture，再断言 Provider 不回显 |

未使用新依赖，没有改写正式权限检查、忽略失败测试或给 subprocess inventory 加例外。

## 接续实现

### 发布侧镜像构建

`internal/computeimage/artifact_build.go` 接入现有归档，`Record` 与 `BuildOnce` 复用锁内提交函数。
`cmd/incus-image-artifacts` 增加显式 `build`，只在独立原生 Linux root 构建机执行固定摘要的
distrobuilder ELF。预检在版本预留之前完成；ELF 经 memfd 封存后执行，固定环境/参数，日志不
暴露 builder 原文。split 文件按固定名称流式进入原不可变归档，不另定义镜像结果协议。

同 revision 验证并复用既有工件；缺失/损坏不触发重建。首次构建前持久化冻结配方和 builder
摘要，失败/取消/失联留下尝试目录，跨会话也拒绝隐式重试。可信原始输出可由 `record` 显式恢复。
不删除未知构建目录或挂载；杀进程组不被当作外部资源清理证明。

参数与输出名对照 [distrobuilder 官方构建说明](https://linuxcontainers.org/distrobuilder/docs/latest/howto/build/)。
此命令不是部署动作，不调用 Provider、不自动生成默认配方、不发布目录、不签名分发、不导入
Incus。opaque fixture 字节只验证编排与摘要，不证明镜像格式、可启动性或实际构建来源。

### HTTP 策略与撤销事务

新增 `internal/computeingress/policy_planner_test.go`：HMAC 稳定向量、fixed/named/random、
严格请求模式、名称冲突、实际 project/接口/IP/MAC、冻结 ForwardAuth 与并发/旧 reservation。
新增 `internal/computeingressruntime/executor_test.go`：发布顺序、每个开通/关闭步骤故障、
持久化失败后的无回执副作用清理、probe 后身份失效、坏 journal 和外部孤立工件。

撤销始终先关闭路由/许可和既有连接，再撤 guest route，最后释放地址。中途失败保留退休意图
和地址；仍存在的旧请求不能取消退休。未知工件不被导入成权限，也不允许地址复用。
测试适配器在内存中，不安装真实防火墙、不证明 Traefik 已消费文件。

## 实际验证

环境：macOS arm64，Go 1.26.6。Docker CLI 可用，但其本地 daemon socket 不可连接；没有启动
Docker、Linux VM、Incus、root 服务或对外网络设施，也未访问历史记录中的其他宿主。

| 检查 | 结果 |
| --- | --- |
| `go test ./modules/incus/... ./cmd/incus-image-artifacts ./internal/compute... ./cmd/anasd` | 通过 |
| `go test ./internal/actionabi ./internal/jobexecutor` | 通过 |
| `go test ./internal/runner -run 'TestIncusControlInputs\|TestComputeLeaseIsPublished\|TestSensitiveEnvKeySet' -count=1` | 通过 |
| `go test ./...` | 最终完整执行通过；最初暴露的编译冲突、fixture 权限、subprocess 门禁和源参数表过期均已修复 |
| `go vet ./...` | 最终执行通过；当前平台为 macOS arm64 |
| `go test -race`：actionabi、consolejobs、computeimage、computeingress、computeingressruntime、Incus provisioner/control-relay | 七个包全部通过；新增 X.509 脱敏修复后 provisioner 单独重跑也通过 |
| Linux amd64/arm64 全仓源码构建 | 最后代码变更后两种架构均通过 `GOOS=linux GOARCH=<arch> go build ./...` |
| Linux amd64/arm64 测试交叉编译 | computeingress、computeingressruntime、control-relay、incus-image-artifacts、jobexecutor 共十次 `go test -c` 通过；不是原生运行 |
| `go run ./cmd/incus-image-artifacts --help` | 通过；示例命令形态与 flag 定义一致，真实 bake 未执行 |
| `go run ./cmd/check-shared-build` | 通过；仅静态源码/staging 输入约束，不证明 Docker 构建 |
| `go run ./cmd/check-upgrade-tests` | 失败：已登记 Module `ai_agent` 缺升级测试条目；该目录与 Module 未在本轮修改 |
| `gen-module-docs` / `gen-contract-docs` 生成及 `--check` | 通过；修改源文档并生成镜像/参数表，未手改生成输出 |
| `docs:check-requirements` / `docs:check-status` / 两项状态索引检查 | 通过；已运行两项状态生成器，Incus 仍为 30/75、实施中 |
| `git diff --check` | 通过 |
| Linux 原生、Docker 构建、真实 distrobuilder、Incus/KVM、双栈/配额/入站/one-job | 未执行；当前环境与生产接线均不满足 |

本机 `go test` 会按 build tags 跳过 Linux 专属实现，必须与交叉编译记录分开理解。没有将
真实 daemon、内核权限、镜像启动或现场防火墙行为记为通过。

## 尚未交付的目标

宿主动作通道和产品入口、安装/盘点/卸载、控制 relay 的可信发布与服务安装、受管网络的防火墙
基线与 endpoint 自动投影仍未交付。生产 HTTP 还缺宿主特权适配器、只读身份的实际供给和完整
观察/授权/路由链路。镜像还缺默认可用配方、真实产物、签名分发、Provider 导入及回滚/受限 prune。
发行版包与观察器 API 的版本准入也需在实际一级发行版逐项核对。

因而本轮不是“全部文档目标完成”，也不只是欠一遍 E2E。计划保持实施中；保留范围的长驻实例、
TCP/UDP 与其他发行版未被提前启用。升级测试目录的既有阻塞单独报告，不伪装为完整 CI 绿色。
