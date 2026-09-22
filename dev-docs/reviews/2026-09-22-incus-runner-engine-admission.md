---
doc_type: review
status: current
created: 2026-09-22
updated: 2026-09-22
---

# Runner 引擎准入、token 读取顺序与原生诊断

接续 [Runner 构建恢复](2026-09-22-incus-runner-build-recovery.md)，基线仍为
`claude/forgejo-docs-audit-20260920` / `3f5242e` 加累积工作树。目标仍是操作者指定的
`whl@ln.hlong.wang:2200`，不修改已有 Docker、不网页检索、不切换分支、提交或推送。
关联 INCUS-R-025/R-027、FORGEJO-R-035/R-036/R-042。本文记录实现与验证子范围，不把
局部测试当作完整需求完成。

## 远端状态与证据边界

本轮四次 SSH 尝试均在认证和远端命令执行之前关闭，最后一次明确退出 255。一次有界
协议诊断显示 TCP 已连接，发送本地 SSH identification 后在 `kex_exchange_identification`
阶段被关闭。ANAS MCP 本身可读写仓库；这不是上轮 MCP 502，也没有据此推断服务器上的
Incus、Podman 或业务 Docker 发生了什么故障。

没有启动新 VM、导入镜像、修改服务器配置、用户组、网络或已有 Docker，也没有采集成功的
本轮前后基线。旧 `lab-r4`、归档和已关闭 VM 的清理证据仍以先前评审为准，不能冒充本轮
新观察。增强后的原生镜像测试尚未实际执行，旧 rootless engine 退出 125 的原因仍未确认。

## 已确认的启动顺序缺口

当前调用链是 `leasedCompute.Start` → 共享 `WaitForGuest` → `ExecStdin` → guest starter。
`WaitForGuest` 仅检查固定入口文件可执行；旧 starter 在已有 one-job 不 active 时直接
创建 token 目录和读取 stdin，没有实际 engine API 检查。因此文件可执行、Incus agent
可用不能证明 rootless Podman 就绪。

新增行为测试执行源文件本身，以测试 PATH 命令拦截首次 install，不改变源脚本文本，
不创建真实 /run、用户、服务或容器。旧实现的套件实际失败，证明不可用/rootful/错误输出
等场景仍到达 token 文件流程。测试用固定无效占位 token，不涉及真实 Runner 凭据。

## 本轮实现

`anas-forgejo-runner-start` 在任何 token 目录/输入操作之前，以 `runner-agent` 调用固定
socket 的 Podman 只读 info，子进程环境清空，仅保留固定 PATH/HOME。只有命令成功且
rootless 字段明确为 true 才能继续；失败命令提前写出的 true 也不能作为成功回执。
每次真实 coreutils timeout 为两秒，终止宽限一秒，最多八次与七次一秒间隔；名义等待
上限 31 秒，不把系统调度延迟算成可严格保证的时限。失败退出 69，返回固定消息，不回显
engine 输出或接收 token，不启动 one-job、不重启服务、不调整 Incus 权限。

既有 active one-job 的重复调用行为保留，不接收第二份 token；没有把这个行为扩展声明
为跨 handle 的身份校验。host/CLI 可能已经缓冲 stdin，本文的“不读取”指 guest starter
未消费该流，而不是网络中从未出现过输入。

Podman 的 Go 格式表达式由相邻 shell 字面量拼接，避免 distrobuilder 把 guest 内容误当
自己的模板。配方测试对 amd64/arm64 × container/VM 四种目标解析 YAML，确认嵌入的
starter 与行为测试的实际文件相同，且没有构建模板分隔符。本次源变更需要新 revision，
不能覆盖、修改或重新烘焙旧 `lab-r4`。本轮尚未烘焙或启动带该检查的新镜像。

## 测试与诊断

八个 Python 顶层用例覆盖：不可用、真实 rootless 回执的模拟正对照、延迟就绪、rootful/
输出不符、错误状态附带 true、实际 coreutils 超时、已有 active job、非法参数。
探测替身读到 EOF，之后首次文件操作的替身仍能读出完整占位输入，验证 stdin 顺序。
执行到首次文件操作即停止，不把这个套件当作真实 token 文件保存或 systemd-run 验收。
CI 已加入该无特权、无网络测试，缺少 timeout 时失败而不是 skip。

controller 新增一个顶层回归及两个子场景，验证准入拒绝后实际调用补偿、注销 registration、
删除失败保留 retiring，并在队列 API 不可用时优先重试清理。测试使用接口替身，不当作
真实 controller crash、guest 删除或 Forgejo workflow 证据。

原生镜像测试不再用一次立即调用判断 engine 失败，而是在 35 秒内反复验证实际 JSON
rootless=true，每次命令上限三秒；错误、超时或 rootful 不能通过。失败时对固定 guest
service 读取选定 systemctl 属性和最多 40 行 journal。报告只保留闭集枚举/有界整数及
固定观察类别，不输出原始日志。缺失、重复或未知 unit 字段无法混入诊断；阶段词只是
观察，不是根因结论。检查失败时父用例和包仍失败，缺失/skip 的 native gate 不放行。

这些增强可用于原样恢复的 `lab-r4`，不必先改变 image、profile、nesting 或设备限制。
本机的 readiness、取消、字段过滤和配方定向 Go 回归已通过；完整收尾门禁结果另在下方
记录，不以交叉编译替代 Linux 执行。

## 源码与后续范围

本机日志根为 `/tmp/anas-engine-admission-20260922.7YBXdC`，保留旧 starter 套件失败、
修复后通过、controller 补偿和 SSH 失败记录。没有把本机临时文件映射为服务器上的文件。
M5a/M12 仍实施中，Incus 保持 `7.3.0-r2 / developing`、30/75 与关闭的生产 ingress。
后续首先在可用的指定 SSH 通道恢复原归档，执行增强诊断，再验证新 revision 的引擎准入、
真实 one-job 与中断回收；本轮不宣称原 Podman 故障已修复。

## 最终门禁与实际结果

| 检查 | 本轮结果 |
| --- | --- |
| `go vet ./...` / `go test ./...` | macOS、已缓存 Go 1.26.6 全仓通过；未改变包允许缓存 |
| computeclient / computeimage / Forgejo controller `-race -count=1` | 三包通过 |
| Python 回归 | Forgejo 11 项（含新增八项），Incus 11 项，共 22 个顶层测试通过 |
| Shell 语法、四目标配方嵌入、就绪观察和固定诊断分类 | 通过；不以模拟 API 当作真实 Podman |
| 新 starter CI 步骤 | 已写入 CI；相同命令本机执行通过，未提交或触发远端 CI |
| Linux amd64/arm64 computeclient 测试程序 | 编译通过；本轮未执行 Linux 原生镜像门禁 |
| 共享构建 / 升级目录 | 静态校验通过，不是 Docker build 或实际升级 |
| 双语 Module/Contract 生成检查、需求覆盖、需求/计划索引与文档状态 | 通过，Incus 保持 30/75 |
| 双语 `docs:build` / `git diff --check HEAD` | 通过，v0.1.1；仅非阻断 chunk 大小提示 |
| 目标 SSH / 本轮服务器基线 | 握手前失败，没有新的服务器执行或基线证据 |

增强后的原生测试程序仅保存在上述本机日志目录，尚未上传。其 SHA-256 为：

| 程序 | SHA-256 |
| --- | --- |
| `computeclient-amd64.test` | `e9a9679b7afca76b3879ee4e30df668deea48f3cb0b41dfd2554beac08df572b` |
| `computeclient-arm64.test` | `d72b750764c321a2214a669854dfea60fa418b43bc18e348627901ad0b102188` |

本轮还按实际 `verifyImages` → `importSuppliedImage` → `verifyImage` 调用链修正文档中
“导入/烘焙仍待实现”的过期说法，区分已有实验候选能力与尚未完成的正式签名和 one-job。
一次多文件文档 patch 因末尾上下文不匹配整体拒绝，读回确认未写入后改用精确上下文成功。
