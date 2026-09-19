# 宿主 job 跨进程绑定接续核对

> 状态：历史记录；2026-09-19，`49bbf45` 的 master 加前轮暂存与未暂存修改。
> 本轮直接修改原 checkout，保留已有工作；本记录不是宿主安装批准或全部需求完成声明。

## 范围与来源

先核对需求/计划索引、宿主动作要求与计划、Incus 计划和激活/job 绑定源码。继续实现
HOSTACT-R-001/R-002/R-004 的内部跨进程交接，保持 Incus 的生产入口保护。
本轮不注册新的特权动作、不改现有服务 UID/GID、不安装服务或接触真实 daemon。

## 交付代码

| 文件 | 作用 |
| --- | --- |
| `internal/hostaction/broker_protocol.go` | 复用 action/v1 请求的有界规范握手；阶段与 nonce 绑定、截止、末尾 EOF、一次性 root 侧适配 |
| `internal/hostaction/broker.go` | 非 root 所有者侧会话、拒绝审计与 grant 后进程句柄保留 |
| `internal/hostaction/broker_linux.go` / `broker_other.go` | 固定私有端点、双向 SO_PEERCRED/SO_PEERPIDFD、CLOEXEC、实际进程终止观测；不支持即拒绝 |
| `internal/hostaction/activation_broker.go` | 将现有 Activation 接到固定 broker，不提供调用方路径或命令 |
| `internal/jobexecutor/host_action_broker.go` | 把已运行的 HostJobBinding 接到跨进程会话，保留原 Store/lease |
| `internal/jobexecutor/host_action_binding.go` | 执行后的角色重新授权、再次读取控制状态；关闭时保留仍存活执行者的租约，避免反向锁序 |
| `test-env/scripts/test-host-job-broker-native.sh` | 非 root Linux 的隔离 socket/子进程测试门禁，缺少或跳过关键用例不算通过 |

没有新增依赖。进程句柄选项来自已有 `golang.org/x/sys v0.47.0`；没有用环境变量接受新路径、
处理器、程序、传入 PID 或 UID，也没有创建第二份 job/事件日志。

## 实现与测试边界

握手只接受当前 `incus.status` / `{}` 预检，绑定真实 running job 与冻结 release。owner
不能仅凭 root 发来的 job id 授权：它还核对原请求进程就是自己，复核原 actor 的现时权限，
并在处理完成后再次检查。规范帧拒绝重复、大小写别名、null、未知字段、错误 nonce/阶段和尾随数据。

grant 在发送前即标为可能生效，失败或断连不自动重试、不丢弃进程句柄。握手完成但进程仍在时
`Close` 拒绝释放原执行租约。`WaitExecutor` 只证明该句柄指向的进程已终止，没有返回零退出码、
确认成功或清理所有后代。现有预检不启动子进程，任何后续写动作都不能直接继承这份清理结论。

本机协议 fixture 使用 Unix sockets 和私有进程观测 fake；它们不冒充内核身份测试。
Linux fixture 单独使用真实测试子进程，在握手完成后保持子进程存活，再由父进程收割，
检查同一个 pidfd 的结束观测。原生正例仍须另行运行；测试私有 UID seam 不进入生产 API。
脚本要求指定原生用例真实通过，避免 `go test` 的 skip 或无匹配用例被报告为验收。

最终自检又补了原请求连接的进程句柄：仅从第二条 broker 连接取得 pidfd 并比较 PID/UID/GID，
仍不足以排除原请求者已退出、PID 随后被复用的窗口。激活侧现在保留**第一条连接**的句柄，
在拨号/授权后的回调前后及返回时确认原进程存活。新增回归模拟相同数字身份但原句柄在不同阶段
结束，阻止尚未执行的回调或拒绝最终成功；模拟不声称真实制造了内核 PID 复用。

## 本轮验证

| 检查 | 结果 |
| --- | --- |
| `go test ./...` | macOS arm64 全仓通过；最后的原连接进程句柄补强后另重跑 hostaction/jobexecutor 均通过 |
| `go vet ./...` | macOS arm64 全仓通过；原连接补强后相关两包再次通过 |
| `go test -race ./internal/hostaction ./internal/jobexecutor -count=1` | 两包通过；最后原连接补强后 hostaction 单独重跑通过 |
| Linux amd64/arm64 全仓源码构建 | 两种架构通过；最后原连接补强后两架构再次全仓构建通过 |
| hostaction/jobexecutor 双架构测试交叉编译 | 四次通过；原连接补强后 hostaction 的两架构测试再次编译通过，均非原生运行 |
| 原生脚本语法及不适用平台拒绝 | `bash -n` 通过；本机实际运行按预期 exit 2，未执行 Linux 测试，不计作原生通过 |
| `go run ./cmd/check-shared-build` | 通过，仅静态检查；未构建 Docker 镜像 |
| Module/Contract 文档生成与 `--check` | 通过，修改源文档后生成，未手改镜像 |
| 需求/计划索引生成、需求覆盖、文档状态及两项索引门禁 | 通过；最后文档变更后再检查通过，验收统计不变 |
| `npm run docs:build` | 最后文档变更后构建通过，v0.1.1；存在非阻断的 500 kB chunk 警告 |
| `git diff --check` / Go 格式检查 | 通过 |
| Linux 原生、真正 root 对端、systemd/Incus/KVM | 未运行；本机交叉编译不替代这些验收 |
| 升级目录门禁 | 先前 ai_agent 缺条目的已知阻塞未修改，不声称完整 CI 绿色 |

## 上游依据及剩余目标

已阅读 [Linux unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html) 的 SO_PEERCRED：凭据
对应 connect/listen/socketpair 时刻。因此 owner 不能直接把 PID 1 创建的监听 fd 当成自己的
认证端点。另核对 [pidfd_open(2)](https://man7.org/linux/man-pages/man2/pidfd_open.2.html) 中
进程退出通知与 wait 子进程的区别，不以 EOF/可读事件推导正常退出。内核源码网页未成功读取，
没有把常量存在或本地交叉编译写成固定内核实现审核或实机支持证明。

仍缺：私有 listener 与非 root execution owner 装配/迁移、正式 root 程序及同版本发布安装、
独立退出状态与完整后代清理、共用 recorder 的生产终态接线、CLI/Web 执行入口、两段确认及
安装/配置/对称卸载。镜像供给和生产入站仍按 Incus 计划推进。本轮无真实宿主状态修改，
Incus 30/75、HOSTACT 0/13、Module developing 和生产 ingress 保护保持不变。
