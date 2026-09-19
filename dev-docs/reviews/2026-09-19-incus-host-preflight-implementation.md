# Incus 宿主供给预检与动作边界接续核对

> 状态：历史记录；2026-09-19。基线为 `49bbf45` 上保留上一轮 38 个文件修改的 master 工作区。
> 本轮直接在同一 checkout 续作，不提交、不合并，不把只读原语称为自动供给或完整 root 通道。

## 范围

先核对需求与计划索引，再阅读宿主通道的要求、计划、架构和现有 action ABI/审计实现。
Incus 目标仍按[原计划](../plans/incus-module.md)验收；通道实现归[宿主计划](../plans/host-action-channel.md)。
上轮全部修改保留，没有启动本机或其他宿主的服务、安装软件或改变防火墙。

## 实现

`internal/incushost/recipes.json` 是编译期声明表，只含发行版、精确版本、架构、包名及有日期的
上游证据；没有可执行脚本/命令/路径。执行安装前仍要验证实际源、签名、依赖和服务单元。
`osrelease.go` 不执行 shell；Linux reader 使用固定位置、root 所有父目录和描述符读回，拒绝
可写祖先、特殊文件、硬链接、非规范符号链接和元数据漂移。未知版本、衍生发行版及未适配架构不自动安装。

`Preflight` 默认 `incus_container`；VM 明确选择，没有自动升降档。跳过不读取系统标识。
包目录匹配、设备存在和 systemd 路径存在都只是观察；`compute_ready` 和 `runtime_verified`
始终 false。`cmd/incus-host-preflight` 可打印本机预检或编译配方表，没有任意根目录、endpoint、
包管理器或安装参数。它不是正式 `anas host` API，不接入主部署的失败降级。

`internal/hostaction` 使用与 Module 分离的编译期清单，只有 `incus.status` 的 installation-preflight
子集。请求复用 `anas.action/v1`，参数只有空对象；所有写动作、命令、argv、路径和自报身份拒绝。
Linux Receive 使用 SO_PEERCRED 和可信 UID/GID 策略，读取有期限，等待 LF/EOF，非 Linux 拒绝。
Peer/Invocation 字段对包外不可写，数据与请求内存脱离，单次调用不能在内存中重复执行。

执行复用 `audit.Writer.AppendContext`；开始审计持久化后才读取主机，结束审计后才返回结果。
两条记录保留 action/job/invocation、规范化参数和内核 peer；原始错误不回显。尾部审计失败返回
unknown，取消后仍使用独立五秒期限写尾部审计。输出帧仍需共享 recorder 核验进程 EOF/退出，
没有自选事件 seq、另建 job store 或让 root 打开用户可写 workspace 的日志。

该只读操作不需要 root、无特权产物、无要撤销的变更；因此没有为它装 root 服务。
没有动态注册 API、解释器、Module 可执行文件或新依赖。真正写动作仍须满足逐动作需求评审和对称撤销。

## 上游核验

| 来源（本轮读取） | 观察与使用边界 |
| --- | --- |
| [Debian 13 incus](https://packages.debian.org/trixie/incus) | `6.0.4-2+deb13u10`，列出 amd64/arm64；不是本机已安装版本 |
| [Ubuntu 24.04 noble-updates](https://packages.ubuntu.com/noble-updates/incus) | `6.0.0-1ubuntu0.3`，universe，列出 amd64/arm64 |
| [Ubuntu 26.04 resolute](https://packages.ubuntu.com/resolute/incus) | `6.0.5-8`，universe，列出 amd64/arm64 |
| [Incus 官方安装说明](https://linuxcontainers.org/incus/docs/main/installing/) | 官方包与第三方来源不同，发行版包可能跟随 LTS；不自动引入第三方源 |
| [os-release 手册](https://www.man7.org/linux/man-pages/man5/os-release.5.html) | etc 优先、缺失才回退、数据不支持变量扩展；本实现对重复键和链接采用更保守的拒绝边界 |
| [Unix socket 手册](https://www.man7.org/linux/man-pages/man7/unix.7.html) | 从内核取得连接 peer 身份；不信任 JSON 中的 uid/gid |

包目录是日期快照，不钉死安全更新版本。它不证明包已签名验证、服务单元已核验或安装/卸载可用。
三个一级发行版的 6.0.x 包不能继承当前按 7.3.0 编写的入站观察器兼容性结论；未放宽其版本检查。

## 验证

| 检查 | 结果 |
| --- | --- |
| 新增两个包及诊断 CLI 单元测试 | macOS arm64 通过 |
| 同三包 race | 通过 |
| 诊断 CLI 本机运行 / `--skip` | 分别返回 disabled/linux_required 与 skipped；compute_ready 均 false |
| Linux amd64/arm64 全仓源码构建 | 两次 `GOOS=linux GOARCH=<arch> go build ./...` 均通过 |
| Linux amd64/arm64 新增三包测试交叉编译 | 六次 `go test -c` 均通过；不是原生执行 |
| `go test ./...` / `go vet ./...` | macOS arm64 全仓通过 |
| `go run ./cmd/check-shared-build` | 通过；不代表 Docker 镜像实际构建 |
| Module/Contract 文档生成与 `--check` | 通过；只修改源文档，再生成对应页面 |
| 需求覆盖、文档状态与两项状态索引检查 | 通过；两项索引均已重新生成 |
| `npm run docs:build` | 通过，生成 v0.1.1 文档；存在非阻断的 500 kB chunk 大小警告 |
| `git diff --check` | 通过 |
| 升级测试目录门禁 | 本轮未重跑；上轮已记录 ai_agent 缺少条目，本轮未修复，不声称完整 CI 绿色 |
| Linux 原生 SO_PEERCRED / 文件完整性 | 未执行；不把交叉编译作为权限验收 |
| 真正安装、软件源签名、daemon 状态、卸载、CLI/Web job、Incus/KVM | 未执行且生产实现不完整 |

## 剩余边界

激活 socket/策略文件、拒绝连接审计、共享 job 身份和执行租约交接、独立于订阅的真实执行所有者、
同版本 root 二进制发布/升级、安装/撤销、二段确认、完整 daemon 状态仍未交付。
当前 peer 策略只识别 primary GID，不将 supplementary group 资格推断为允许；未来正式 CLI
准入必须有独立设计和测试。控制 relay、存储/NAT/防火墙、服务端只读身份与 HTTP 发布仍保持旧阻塞。

本轮不改变 `developing`、Incus 30/75 或宿主通道 0/13 的验收统计；只将通道 M0/M4 从未开始
更新为实施中。完成统计没有用代码数量或本机测试替代实机目标。
