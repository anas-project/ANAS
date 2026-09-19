# 宿主动作退出证据与共享终态核对

> 状态：历史记录；2026-09-19，基线 `49bbf45` 的 master 加此前暂存/未暂存工作树。
> 本轮直接修改 `/Users/whl/Documents/anas`，保留原有变更，未提交。不是正式发布或实机验收记录。

## 来源和范围

接续[需求索引](../requirements/index.md)、[计划索引](../plans/index.md)、
[Incus 要求](../requirements/incus-module.md)及[计划](../plans/incus-module.md)，并按
[宿主通道要求](../requirements/host-action-channel.md)与[计划](../plans/host-action-channel.md)
补齐上一轮缺少的独立退出状态、共享终态和可执行程序。长期机制见
[宿主通道架构 §11](../../docs/architecture/host-action-channel.md)。

## 已写入的实现

`internal/hostaction/systemd_exit*` 增加 PID 1 的实际 D-Bus 读取适配器与独立验证器。
生产 broker 在授权前读取同一服务管理器的单元身份/启动时间并持有 Ref；执行后同时要求
socket pidfd 结束、同 invocation 的真实退出字段和空进程集。管理器重连不重新绑定身份，
缺字段/错误/默认零值不解释为退出成功。不让执行器 JSON 自报退出状态。

`internal/jobexecutor/host_action_completion.go` 连接已运行任务、原 broker 注册、固定 socket、
严格 preflight 输出、实际 EOF、broker 复核、独立退出证据和原 `ActionRecorder`。正常失败帧
只有在真实非零退出时才归 failed；信号/超时/矛盾输出归 unknown。进程清理不确定时复用
`execution_containment_lost`，保留原绑定及 lease，不能重新注册或自动复位。
成功提交前再次授权，Store pre-commit 还会拒绝等待期间已持久化的取消；审计失败不能写成功。

`cmd/anas-hostd` 已有一次性激活入口。它没有路径/命令/脚本参数，也不开自己的监听器。
root 审计固定到 `/var/lib/anas-hostd/audit`，复用已有 writer、root 祖先核对和原 ABI。
新增 `--actions` / `--version` 只打印本二进制，安装状态明确未核验。服务失败帧及清理错误
导致真实进程非零退出，不再返回会被当作 exit 0 的 nil。

`scripts/ci/build-anas-release.sh` 用同一 version/commit/date 编译并打包 `anas-hostd` 及两份
候选 systemd 单元。`install.sh` 未变；没有安装这些单元、启动 root 服务、改变现有 anasd 身份、
chmod/chown 机器数据目录或修改外部宿主。打包测试使用 `0.1.1`、当前 HEAD 和固定日期作为
本地测试输入，不代表当前工作树已经提交或形成新的正式 release。

新增依赖只有 `github.com/godbus/dbus/v5 v5.2.2`，版本和校验和进入 go.mod/go.sum。
用途是正式 D-Bus 类型化调用；不自己实现总线协议，不通过 shell/systemctl 解析退出码。

## 自检修正

一项新增测试起初预期“后续 job 可以创建但不能开始”，实际共享 Store 在创建阶段已经因
containment barrier 拒绝。测试修正为接受这个更早的阻断，未修改或放宽 Store。

候选 root 单元最初使用空 capability 集合，但这也会阻止进入非 root 所有的 0700 目录及
连接 0600 socket。已改为仅保留 `CAP_DAC_OVERRIDE` 并在打包回归中固定这一选择；没有放宽
原 socket 文件权限。它仍是广义文件权限能力，不应误称零特权沙箱；固定路径和编译动作校验
继续执行，包安装、信号控制、mount 与网络管理未开放。

## 验证记录

| 检查 | 结果 |
| --- | --- |
| hostaction/jobexecutor/anas-hostd 专项单元测试 | macOS arm64 通过 |
| 同三包 `go test -race -count=1` | 通过 |
| `go test ./...` / `go vet ./...` | macOS arm64 全仓通过；含最后的单元权限/打包约束测试 |
| Linux amd64/arm64 全仓源码构建 | 两种架构均通过 |
| hostaction/jobexecutor/anas-hostd 双架构测试交叉编译 | 六次通过，未执行 Linux 测试二进制 |
| 两种架构实际 release 归档构建 | 均通过；tar 内容包含 hostd 及两个单元，ELF build info 核对架构与 CGO_ENABLED=0 |
| `go run ./cmd/anas-hostd --actions` | 实际运行通过，只列 incus.status / installation-preflight，安装状态未核验 |
| 发布脚本 `bash -n`、共享构建静态检查 | 通过；没有 Docker 镜像实际构建 |
| Module/Contract 生成及 `--check` | 通过；修改源文档后重新生成，未手改生成页面 |
| 需求/计划索引生成、需求覆盖、文档状态和两项状态索引门禁 | 全部通过；仍为 Incus 30/75、HOSTACT 0/13 |
| `npm run docs:build` | 通过，生成 v0.1.1 站点；仍有非阻断的 500 kB chunk 大小警告 |
| `git diff --check` / Go 格式 | 文档构建后通过；没有 Go 格式偏差 |
| systemd、D-Bus 原生路径、真实 root/非 root 对端、Incus/KVM | 未执行；合成 manager 快照测试不作为实机证据 |
| `go run ./cmd/check-upgrade-tests` | 本轮重跑仍失败：registered module ai_agent has no upgrade test entry；该目录未改，不称完整 CI 绿色 |

单元测试覆盖活进程/单元绑定、重启/漂移、退出 0/非零/信号/超时、默认零值、残留进程、管理器
及盘点失败、关闭失败、原 Store 中的终态原子提交/重放、伪造 readiness、尾随输出、权限撤销、
取消、重复 finalization 和持久 containment 阻断。测试没有模拟成真实 PID 1，也未宣称生成
了内核级 PID 复用。新增原生服务模式仍需单独的 Linux 配置和集成验收。

## 上游依据与尚未完成

核对了 [systemd D-Bus 接口](https://man7.org/linux/man-pages/man5/org.freedesktop.systemd1.5.html)
的 Ref/Unref、GetUnitByPID、GetUnitProcesses 与 ExecMainCode/Status；
[godbus v5.2.2 文档](https://pkg.go.dev/github.com/godbus/dbus/v5@v5.2.2)和
[capabilities(7)](https://man7.org/linux/man-pages/man7/capabilities.7.html)。固定版本 systemd
源码网页未成功获取，未把接口文档核对记为固定发行版实现审计。

当前观察器要求系统总线 socket 的对端 root 身份、PID 1 的唯一总线身份和无 drop-in 的固定
模板。每个发行版的真实总线启动方式、权限、单元回收与终态字段保留都仍需实测；不匹配时
保持关闭，不能以常量可编译或本文的合成快照当成已支持证明。

仍缺非 root 执行者的生产迁移与装配、公共 CLI/Web 请求入队、安装器/升级/卸载、破坏性确认，
以及 Incus 包安装、配置、登记、所有权和对称撤销。当前代码只执行无子进程预检，不证明未来
包管理器后代树或恶意 root 的完整清理。镜像供给和生产 ingress 按 Incus 计划继续跟踪。
Incus 30/75、HOSTACT 0/13 和 developing 不变。
