# 宿主 job 私有监听与分派核对

> 状态：历史记录；2026-09-19，基线 `49bbf45` 的 master 加前轮已暂存、未暂存及未跟踪修改。
> 本轮直接修改原 checkout，保留此前工作；不代表 root 服务、Linux 原生或 Incus 全部目标验收。

## 来源与实施范围

先重读需求/计划索引，再核对 Incus 要求矩阵、宿主通道计划和现有激活、broker、Store/lease 代码。
接续[上轮跨进程绑定](2026-09-19-host-job-broker-implementation.md)，补的是非 root 所有者
私有 listener 和请求分派，不增加可由客户端注册的 root 动作，不创建第二份任务存储。
对应 HOSTACT-R-001/R-002/R-004/R-007 的内部前置；未更改需求定义或验收统计。

## 本轮实现

| 位置 | 内容 |
| --- | --- |
| `internal/hostaction/broker_listener*.go` | 在安装已提供的 0700 私有目录创建固定 0600 socket；排他目录锁，空闲/接收/关闭身份复核，关闭仅删除本次所持节点；不接管旧文件、不递归删除 |
| `internal/hostaction/broker_linux.go` | 原有端点验证复用为目录/完整端点两个内部阶段，避免另建一套路径信任规则 |
| `internal/jobexecutor/host_job_broker.go` | 共享 Store/lease 的注册、按 job/invocation 分派、32 绑定/8 连接上限、停机、终态/清理约束的退休；不自动创建/恢复任务 |
| `internal/jobexecutor/host_action_broker.go` | 原单任务 ServeBroker 与服务分派共用一次性 session 绑定，保留原进程监督边界 |
| `internal/hostaction/broker.go` | 只读 `GrantPossible` 观测供所有者在授权可能送达后的失败中停止新准入；不是执行或成功回执 |
| `test-env/scripts/test-host-job-broker-native.sh`、`.github/workflows/ci.yml` | 原生门禁扩展到 listener 和所有者分派，按包/用例核对通过记录，接入 Go CI，拒绝 skip/无匹配用例 |

成功握手后由监督者保留 session，不在 accept worker 中提前 Close；否则进程恰好先退出时，
句柄可能在 `WaitBrokerExecutor` 读取退出证据前被丢弃。服务退出与每个 job 的远端结束分别处理。
开始就绪只反映实际 listener 成功打开；停止准入后等待全部连接处理，远端未退出时仍保留任务租约。

新测试使用真实 Store 与私有 session seam 核对已注册任务分派、拒绝未知 job/错 invocation/
参数/权限、并发同任务只执行一次、容量上限、注册请求取消不取消执行、授权后失败停止准入、
审计/清理失败、启动失败不就绪和退休不得重建 running job。session seam 不提供内核身份或
root 证据。Linux 测试源另覆盖真实 socket 权限/owner PID、双 listener 冲突、残留拒绝、替换
不删除、目录漂移及空闲取消；必须与 macOS 测试分开理解。

## 验证

| 检查 | 结果 |
| --- | --- |
| 所有者分派专项与 hostaction 本机测试 | macOS arm64 通过 |
| `go test ./...` / `go vet ./...` | macOS arm64 全仓通过 |
| `go test -race ./internal/hostaction ./internal/jobexecutor -count=1` | 两包通过 |
| Linux amd64/arm64 全仓 `go build ./...` | 两种架构均通过 |
| hostaction/jobexecutor 双架构 `go test -c` | 四次通过；不是原生执行 |
| 原生脚本语法与本机拒绝 | `bash -n` 通过；macOS 执行 exit 2，不计作原生通过 |
| `go run ./cmd/check-shared-build` | 通过；仅静态检查，不代表 Docker 镜像构建 |
| Module/Contract 源生成及 `--check` | 通过；只修改源文档后生成 |
| 需求/计划索引生成、需求覆盖及文档/状态索引检查 | 通过；Incus 30/75、HOSTACT 0/13 不变 |
| `npm run docs:build` | 通过，v0.1.1；非阻断的 500 kB chunk 大小警告仍存在 |
| `git diff --check` 与 Go 格式 | 通过 |
| GitHub Actions 实际运行、Linux 原生、root 对端、systemd、Incus/KVM | 未执行；工作流接线和交叉编译不代替这些证据 |
| 升级目录门禁 | 继承 ai_agent 缺升级条目的已知失败，本轮未修改该目录 |

## 边界与剩余目标

复核的公开接口来源：[Linux unix(7)](https://man7.org/linux/man-pages/man7/unix.7.html)
与 [Go UnixListener.SetUnlinkOnClose](https://pkg.go.dev/net#UnixListener.SetUnlinkOnClose)。
代码关闭自动路径 unlink，复用私有目录权限和持有身份来限制清理；不把普通路径检查描述成可
防御恶意 root/同 UID 写入的沙箱，也不在服务内改变全局 umask。

本轮未修改原 root/root 的 anasd 服务身份、安装器或机器属主，未安装任何 listener/root 单元。
仍须补非 root 服务迁移/装配、root 二进制及同版本发布安装、实际退出状态与完整后代清理、
共用 recorder 的生产接线、CLI/Web 执行入口、二段确认和 Incus 安装/配置/对称卸载。
Incus 镜像供给、真实 ingress 与宿主矩阵继续按原计划；30/75、HOSTACT 0/13 与 developing 不变。
