---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# 指定 Linux 主机的原生读回修复核对

承接[首次 finance 验证](2026-09-21-incus-finance-linux-validation.md)，继续使用操作者明确指定
的 `whl@finance.hlong.wang`。基线仍为 `claude/forgejo-docs-audit-20260920` / `3f5242e`
加保留的未提交工作树；没有提交、推送或丢弃前轮修改。仅使用源码、既有日志和指定主机实测。

## 复现、修复与被否定的候选

旧实现重新执行后仍出现原四项失败。实际 `ip -j -d` 在同 namespace 对端输出 `link: peer0`，
不是 `link_index`。没有放宽生产数值索引检查，而是让测试 peer 真正在第二个 netns 运行。
随后暴露真实策略路由 JSON 的 `dst`/`dstlen` 分列形式；增加严格前缀解析后完整 FIB、永久邻居、
接口删除/同名重建与原路由保留用例通过。

namespace 夹具原来依赖锁线程退出；当选中进程 leader 时，其他线程与 `/proc/PID/ns/net`
可能不再一致。所有创建器改为先恢复并核对原 namespace，再返回持有的新 namespace 描述符。
新增十六次连续创建/恢复反例，原 cookie 用例在批量和乱序中均通过。

nft 读回问题包括数字 ct-state、被编译器省略的冗余协议依赖，以及 EtherType 数字歧义。
曾尝试只对指定版本做字节序转换；虽然单元和正例通过，新增真实错误 EtherType 规则反例
证明该候选会误认协议，因此已删除这份候选 codec。最终使用符号协议输出，拒绝数字 EtherType，
真实反例确认错误规则被拒绝、恢复原规则后正例仍通过。没有通过删除负例取得绿色门禁。

原生 `meta iif` 在 `-n`、重复 `-n` 甚至数值文本中仍会返回设备名。最终使用只读内核 GETRULE
的原始 meta/cmp 数值证据，而非名字反查；GETGEN 必须覆盖原始 dump 和完整 JSON 的同一版本。
规则 handle、表/链、寄存器、运算、字节长度、内核发送者、序号、中断和大小/时间上限均检查。
真实 generation 变更反例与原始索引/名称复用报文反例通过。新增 socket 只是后端内部只读
NETLINK_NETFILTER 取证，不监听端口、不订阅事件、不增加 root 动作，也不赋予中介 root 权限。

## 执行范围与证据

主机仍为 Ubuntu 26.04 / x86_64，kernel 7.0.0-30-generic、nft 1.1.6、iproute2 6.19.0。
用本机 Go 1.26.6、GOPROXY=off 编译 Linux 二进制后在指定主机实际执行。root 原生测试始终
位于新 mount/network/PID namespace，挂载传播 private、只读依赖 overlay、私有 /run，
外层 timeout 与 kill-child 约束测试进程。没有启动业务容器、安装系统软件包或默认 Incus 服务。

最终 Linux amd64 测试二进制 SHA-256：
`ba8944a6c71bd7829905e61b110c629f28b3abc0157f87be74b26a25687456c4`。
远端证据位于原测试根 `/home/whl/anas-incus-verify-20260921.EY5yZQ/reports/repair-20260921/`。
诊断与 v1—v5 的失败日志保留；v6 首次八项通过，最终版本另加 generation 反例并重测。
本轮十三份源码/测试/门禁文件另有稀疏归档 `native-repair-source.tgz`，它补充而不替代前轮
完整源码快照。归档 SHA-256 为 `984a48cba8b5ab5311aee40bf37ba6d04219ded5bc3e466e28043fa8d536a31d`，
逐文件摘要清单 `source-files.sha256` 的 SHA-256 为
`86d2458937c6426618d36ced33b4b841ddc1aad8fa0f942790091b4f2547b329`。

仓库原生 gate 新增可选的绝对路径 test2json 参数，无 Go 主机也能运行同一检查器；它仍逐项
要求八个父用例及四个 namespace 子用例通过，任何跳过或缺失证据都失败。没有使用管道末尾
成功掩盖测试二进制的失败。生产网络、guest 身份等测试适配器边界未改变。

| 检查 | 实际结果 |
| --- | --- |
| 原失败用例重新复现 | 四项失败保持，可从诊断记录复查 |
| 错误 EtherType 实际规则反例 | 中间候选失败；最终符号读回通过 |
| 内核原始索引解析与反例 | Linux 运行通过，拒绝截断/重复属性、错误 scope/寄存器/运算/句柄及替代索引 |
| 强制原生门禁 | 八项通过，含规则版本变化、原生 nft 生命周期、FIB 与报文反例 |
| 三轮乱序运行，seed=9212026 | 八个父用例各通过三次，共 24 次；0 fail、0 skip；包终态 pass |
| 宿主网络包完整 Linux 回归 | 77 个父用例通过；0 fail、0 skip；包终态 pass |
| Linux arm64 测试二进制 | 交叉编译通过，没有在 arm64 执行 |
| 本机 `go vet ./...` / `go test ./...` | 全仓通过，未修改包允许 Go 缓存 |
| 本机两包 `go test -race -count=1` | incusingresshost、computeingressruntime 全部通过；不是 Linux-only netlink 的竞态运行证据 |
| Module/Contract 文档、需求/计划索引、需求覆盖与文档状态 | 生成和全部检查通过，Incus 完成统计仍 30/75 |
| 共享构建与升级目录 | 静态检查通过；未执行实际 Docker build 或升级 E2E |
| 双语 `docs:build` / `git diff --check HEAD` | 通过，构建仅原有非阻断 chunk 大小警告 |
| 宿主基线与进程/挂载清理 | nft stateless、Docker 容器 ID、命名 namespace 与本轮前置逐字节一致；35 条 IPv4 和 49 条 IPv6 路由仅 expires 差异；没有本轮测试进程或测试根挂载 |

原始重复/完整包日志由 root 写入 0600，直接 scp 读取失败；随后只用 sudo 读取这两份本轮
生成的报告，没有放宽目录或文件权限。逐项解析确认上述通过次数、无跳过与包终态。旧失败日志
和原型/daemon 的旧记录均未覆盖。Docker 仍 active，默认 Incus service/socket 仍 inactive。

新增八个顶层测试入口，并扩展原生门禁及原有生命周期用例。源码/测试的普通 Go 回归在 macOS
执行；新增 Linux netlink 解析反例和真实 kernel 取证在上述 Linux 完整包及原生门禁执行。

## 未完成范围

本轮没有重跑独立 Incus 6.0.5 daemon 的建池超时，也未补齐上轮 Runner 全包所需的 Git/Go/
部分源码夹具。该机仍没有 KVM；真实 btrfs/zfs guest 配额、one-job、镜像烘焙/签名、轮换与
回滚、生产中介/health/UID/挂载、VM/TAP、完整 Docker/Incus 防火墙共存仍需继续实施和验收。
内核报文/FIB 通过不等于这些退出条件满足，Incus 保持 `7.3.0-r2 / developing`、30/75，
production publication gate 未解除。
