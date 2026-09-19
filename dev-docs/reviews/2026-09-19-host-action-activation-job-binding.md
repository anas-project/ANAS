# 宿主动作激活与共享 job 绑定核对

> 状态：历史记录；2026-09-19，基线 `master` / `49bbf45` 加此前已暂存的 60 项文件修改。
> 本轮保留既有 index 与工作区修改，未提交、未安装服务。本记录不是生产放行或全部目标完成声明。

## 范围与实际变更

先复核需求/计划索引及[宿主要求](../requirements/host-action-channel.md)、
[宿主计划](../plans/host-action-channel.md)、[Incus 计划](../plans/incus-module.md)，
继续实现[宿主架构](../../docs/architecture/host-action-channel.md)的激活和共享存储边界。

| 位置 | 交付内容 | 仍未提供 |
| --- | --- | --- |
| `internal/hostaction/installation.go`、`activation_linux.go` | 固定 root 安装配置及 version/commit；目录/文件/socket pin；已接受 AF_UNIX fd 3 校验 | 安装配置生成器、服务单元安装与 root 二进制发布 |
| `activation.go`、`admission.go`、`receive.go` | 单次请求、强制 job 绑定、拒绝请求审计、单次同步回调及关闭连接；只读执行函数收为私有 | 可信跨进程 broker 与真正监督执行者 |
| `internal/consolejobs/action_execution_binding.go` | 使用原 execution lease 读取并保留同 store 的 running action | 新 job 存储、排队领取或恢复均不新增 |
| `internal/jobexecutor/host_action_binding.go` | 冻结 job/version/commit/参数匹配，当前 actor 再授权，取消后重查，保留执行所有权 | 生产 dispatcher、root 进程退出证据与安装 |
| `internal/runner/host_cli.go` | 实际 `anas host actions [--json]` 只读清单 | 任何 host invoke/plan/apply、sudo、socket 或密码覆盖 |

安装 JSON 限 4 KiB；只含 schema、release、service_uid/service_gid/group_gid。策略为 root 所有的
0600 单链接普通文件，所有祖先不可共享写入；socket 为 root 所有、配置组、0660。开发版本、
版本漂移、无效 UID/GID、未知/重复/null/大小写字段、普通文件、监听或匿名 socket 都拒绝。
全路径与身份校验仍只代表代码边界，不证明安装单元或 Linux 运行已经通过。

没有引入新依赖或特权 helper。`Execute` 原语改为私有，仅 `Activation.Serve` 提供公开的执行
入口；接口强制绑定，但实际 broker 尚未实现，不能拿测试回调装配生产。非 root job 所有者复用
同一个 store，root 不读取用户可写日志。清单只列已有只读预检，未登记任何写动作。

## 明确发现的部署阻塞

`packaging/systemd/anasd.service` 当前为 root/root，而 peer 政策不接纳零值/root 身份。
未采用允许任意 UID、读取请求自报组列表、改变服务用户或批量 chown 的捷径；需要单独处理
非 root job 所有者、现有状态与 Docker 权限迁移，再连接真正 broker 和执行监督。

本机清单的 `source=compiled-client` 与 `installation_verified=false` 始终明确出现；
false 表示没检查，不是安装不存在的证据。job 绑定没有终态写权限；受控回归验证缺少实际
退出证据时共用 recorder 仍写 unknown，不因 socket 流结束而宣告成功。

## 验证记录

| 检查 | 结果 |
| --- | --- |
| 新增策略、job 绑定、CLI 专项 | macOS arm64 通过；CLI 初次断言误把缩进 JSON 当紧凑 JSON，修正字段存在性断言后通过 |
| `go run ./cmd/anas host actions --json` | 实际运行通过；仅 incus.status / installation-preflight，安装状态未核验 |
| Linux amd64/arm64 全仓 `go build ./...` | 两种架构均通过 |
| hostaction/jobexecutor/consolejobs 双架构 `go test -c` | 六次通过；不是 Linux 原生执行 |
| `go test ./...` / `go vet ./...` | macOS arm64 全仓通过；最后的关闭错误传播修复后，另重跑 hostaction 单元/race、相关包 vet 与 Linux 构建通过 |
| `go test -race ./internal/hostaction ./internal/jobexecutor ./internal/consolejobs` | 三包通过；最后补充关闭错误回归后 hostaction 用 `-count=1` 再次通过 |
| `go run ./cmd/check-shared-build` | 通过；仅静态检查，未做 Docker 镜像构建 |
| Module/Contract 文档源 `--check` | 通过；没有手改生成页面 |
| 需求/计划索引生成及需求覆盖、状态与索引检查 | 均通过；仍为 Incus 30/75、HOSTACT 0/13 |
| `npm run docs:build` | 通过，生成 v0.1.1 文档；存在非阻断的 500 kB chunk 大小警告 |
| `git diff --check` / Go 格式检查 | 通过 |
| Linux 原生 fd/SO_PEERCRED/路径竞态、systemd 激活 | 未执行；已补原生回归源，不把交叉编译当通过 |
| 真实 Incus、安装/卸载、root 动作、HTTP 入站 | 未执行；相关生产实现仍未交付 |
| 升级目录门禁 | 继承上一轮 ai_agent 缺条目的已知阻塞，本轮未修改该目录 |

策略测试覆盖重复/转义字段、null、版本漂移；job 测试使用真实 `consolejobs.Store`，覆盖跨 store
租约、伪造 ID/动作/参数、排队/变更型任务、权限撤销、授权期间取消、八并发一次执行与保留租约。
单次绑定返回后调用保存的执行回调仍被拒绝。Linux 源覆盖正确 fd、错误类型、配置/祖先/socket
替换、链接/模式、断连后审计完成及拒绝输入脱敏，必须另行原生运行。

收尾发现 `Serve` 原先忽略 deferred close 的错误，已改为清理未确认时返回固定失败，避免未来
启动器把成功候选帧加正常退出误当作完整完成。跨平台回归验证早返回也传播清理错误，Linux 回归源
另覆盖已执行/写帧后的失败。hostaction 在此修复后重跑本机单元及 race 通过，两种 Linux 架构的
构建和最新测试交叉编译也通过；Linux 正例仍未原生运行。

## 上游核验与剩余工作

已通过受限只读抓取核对 [systemd v257 sd_listen_fds 原文](https://github.com/systemd/systemd/blob/v257/man/sd_listen_fds.xml)：
Accept=yes 的连接 fd 名称为 connection，环境标记清除与连接/监听 fd 有别。网页读取失败后从
同一官方仓库的 raw 文档读取，没有从第三方教程推导 fd 约定，也未下载或执行上游代码。

仍缺非 root 执行者接入、认证 broker、可信 root 进程监督与同版本发布安装、对称卸载和二段确认；
Incus 存储/网络供给、镜像分发/导入及实机矩阵继续按原计划。Incus 30/75、HOSTACT 0/13 与
developing 状态不因内部代码或清单命令改变。
