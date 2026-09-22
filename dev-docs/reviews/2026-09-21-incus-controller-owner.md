---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# Incus 中介所有者、排空与 Host 读取器装配核对

基线为 `claude/forgejo-docs-audit-20260920` / `3f5242e` 加前四轮未提交工作树。
本轮直接修改原 checkout，保留所有暂存、未暂存及未跟踪变更。未切换分支、提交、推送或
安装实际服务。接续目标为 M11 的启动/停止与旧路由清理，以及无凭据观察的运行时装配。

## 核对与实现

此前 `HostProjectionReader` 已存在，但统一 `OpenWorkspaceReaders` 和私有交付仍固定要求
Incus keypair；不能把“无凭据读取类存在”说成完整装配已经具备。本轮增加独立的 Host 私有
文件 schema 与 `OpenHostWorkspaceReaders`，复用 Core 交付入口、完整快照校验、命名密钥和
Traefik 读取器。Host/direct 互斥，非法混用及缺客户端拒绝；不提供自动直连回退或默认授权。
旧 direct v1 的字段、顺序与语义保留。Host 只有安装 pin，不带 Incus endpoint、证书或私钥。

宿主观察调用前后同时检查配置文件内容与目录/文件身份。同内容换 inode 也失效。读取器关闭后
配置、密钥、直接/宿主观察与 Traefik 调用拒绝，不能重建连接继续工作。`WorkspaceReaders`
可装配单个 `ControllerService`，不允许两份生命周期对象分享同一组读取器。

原 Controller 的取消和清理错误合并返回，单凭 errors.Is(context.Canceled) 不能判断是否排空。
本轮将循环抽到同一会话内的私有方法，旧 Run 入口保持原行为，新所有者显式区分运行停止与
清理完成。没有第二个对账算法、租约锁、job 或持久数据库。

新所有者只在 Run 被调用后工作，Ready 在启动恢复和首次完整对账后发出；不是长期健康状态。
Stop 取消观察并有界排空。失败返回稳定 `ErrControllerDrain`，同一 journal/flock 和旧读取器
继续保留，Run 等待显式 RetryDrain，即使原上下文已取消也不退出。重试不重新读取 desired
或打开许可；并发等待者共用一轮清理。等待取消不取消实际排空。仅在全部旧目标清理和完整
库存检查成功后关闭读取器、释放会话。锁获取失败、资源 Close 失败都不能被报告为成功停止。

这是进程内持有边界；强杀仍会释放内核锁，下一进程必须使用旧 journal 做恢复。没有实现
强杀情况下网络已被关闭的证明，也没有把读取器 Close 宣称为内存秘密的可靠擦除。

## 验证

新增 14 个顶层测试入口。生命周期测试运行真实 FileStateStore、flock、journal、Controller
与 Executor；网络/renderer/probe 为显式适配器。配置测试生成实际冻结 Core 元数据、镜像
引用、请求注册与 0400 私有文件，并打开真实 WorkspaceReaders；宿主投影结果是夹具。
这些测试不能拼接成 Linux/systemd/Incus/Traefik 联合验收通过。

| 检查 | 本轮结果 |
| --- | --- |
| Controller/Host/direct 专项 | 通过 |
| `go test -race ./internal/computeingressruntime -count=1` | 通过，包含并发重试与取消等待 |
| `go vet ./...`、`go test ./...` | 通过；未变化的包允许 Go 测试缓存 |
| Linux amd64/arm64 四包测试二进制 | computeingressruntime、incusprovision、hostaction、jobexecutor 共八份交叉编译通过；未在 Linux 执行 |
| Module/Contract 生成检查、需求覆盖、需求/计划索引、文档状态 | 生成与检查均通过；Incus 完成统计仍为 30/75 |
| 共享构建与升级目录检查 | 静态检查通过，不是 Docker build 或升级 E2E |
| `npm run docs:build` | 最终双语源构建通过，v0.1.1；非阻断的 chunk 大小警告 |
| `git diff --check HEAD` | 通过，包含前轮保留的暂存与未暂存变更 |
| 真实 Linux/Incus/Docker/Traefik/UID/挂载 | 未执行 |

没有增加 Go 依赖。上下文清理使用现有
[Go context](https://pkg.go.dev/context#WithoutCancel) 接口并另加 timeout；文件身份仍使用
现有 [os.Root](https://pkg.go.dev/os#Root) 路径约束。本机实际编译/测试是本仓库接口可用性的证据，
不是生产宿主能力的证据。

最终读回仍为 Darwin arm64 / Go 1.26.6，未发现 `test-env/targets.local.yml`。HEAD 仍是
`3f5242e`，分支未变；正式镜像 catalog 仍为空，production gate 未解除。没有选择生产 SSH
目标、启动守护进程或修改宿主网络。前端本轮没有行为改动，未将前轮 92 项测试计为新执行结果。

## 尚未交付

新生命周期装配未接入实际 `anasd` 的生产启动器。外层必须在关闭宿主动作服务、旧 Traefik
凭据或挂载之前排空中介；observer refresh/disable 与该屏障尚需实际协调器连接，不能以新增
类和方法声称这些操作已经自动互锁。真实 root 网络动作、health、UID/挂载、VM/TAP、持续
生命周期/ifindex 复用、旧 TCP 与进程崩溃仍需实现或实测。镜像签名分发与其余宿主矩阵不变。

生产 publication gate 保持关闭，Incus 保持 `7.3.0-r2 / developing`；完成统计仍应为 30/75，
本轮内部接线与本机回归不提升实机验收里程碑。
