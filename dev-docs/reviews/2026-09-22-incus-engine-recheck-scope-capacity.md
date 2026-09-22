---
doc_type: review
status: current
created: 2026-09-22
updated: 2026-09-22
---

# 原样 Runner 引擎复测与失败补偿的 scope 配额

基线为 `claude/forgejo-docs-audit-20260920` / `3f5242e` 加已有工作树。继续指定的
`whl@ln.hlong.wang:2200`，不使用已有 Docker 执行测试、不做网页检索、不切换分支或提交。
关联 INCUS-R-025/R-027、FORGEJO-R-038/R-041/R-042；本文只记录验证到的子范围。

## 恢复与原样镜像复测

本轮 MCP 和 SSH 均恢复。新物理测试根为
`/home/whl/anas-engine-debug-20260922.K9kUYU`，实验身份为 `anas-runner-bake-k9kuyu`。
独立 QEMU 使用 1 vCPU、2048 MiB、24 GiB 稀疏磁盘及用户态网络，SSH 仅绑定回环 22130；
没有 TAP 或宿主桥接，也未修改物理宿主的软件、Docker 配置和服务。VM 使用保留的
Ubuntu 26.04 镜像，重新核对其摘要及旧 `recovered-build.tar` 的完整 SHA-256。

旧归档恢复后，通过已有 export 命令取得原样 `lab-r4`；fingerprint 仍为
`037153fd3525c1ba7d84d582cb1ffffe0bfc1f2e15db0f44bf38860a33c09507`。
没有重烘焙、覆盖版本或把旧镜像标作带新 starter 的版本。VM 内实际 Incus 为 6.0.5。

run01 的 Provider 供给、重复 ensure、inspect 和受限容器启动完成，真实 Runner 和 one-job
CLI 子项通过。35 秒内 19 次引擎探测仍未成功，整个镜像测试失败。终点服务观察是
`ActiveState=inactive`、`SubState=dead`、`ExecMainCode=0`、`ExecMainStatus=0`、
`NRestarts=0`、`Result=success`。这只证明当时没有已启动的 engine 进程，不能证明网络等待、
权限、socket 或具体启动依赖已经被确认为根因。journal 的封闭分类为 `unclassified`。

## 测试自身的临时空间问题

为保留两轮运行现场而移动旧 runtime 后再次复制镜像，填满了 VM 原有约 393 MiB 的 `/run`。
实测旧完整副本约 234 MiB，新部分副本约 159 MiB；磁盘仍有 19 GiB 空闲。run02 因文件错误
提前失败，不能计为原生引擎或新 boot-jobs 诊断已运行。嵌套 SSH 吞掉外层脚本输入的准备问题
也已更正；没有将此前空输出的退出 0 算作依赖已安装。

选择性删除这两份临时副本的请求被工具拦截，未执行。随后采用保留全部文件的容量调整：仅把
该可销毁 VM 的 `/run` 上限改成 768 MiB，外层 QEMU 2048 MiB 上限及物理宿主保持不变。
另一个使用磁盘原始 export 的独立诊断 project 由实际 Provider 收敛；init 返回错误但留下
Stopped 实例。直接启动该诊断实例的请求被工具拦截，未执行，也没有改用其他方式启动它。
后续只按本轮精确名称清理这些 Incus 对象并读回，不重复被拦截的文件删除或实例启动请求。

测试脚本现新增无副作用空间预检：在创建任何报告/runtime 目录或操作 daemon 前，要求
`/run` 能放下两份实际镜像片段及 16 MiB 余量。观察失败、非正整数大小或容量不足返回固定
错误。它不预留容量，仍不能防止其他进程随后占用空间。最终清理事件改名，明确只声明
daemon 资源已回收，不再可能被理解为 staging、凭据文件或 VM 也已删除。

原生失败诊断新增受限的 boot-jobs 字段，仅保留固定的 Podman/network-online 相关 unit
名称与合法任务状态，未知名称不输出、重复或越界输入拒绝。该解析回归通过不等于实机上已
观察到这些任务；本轮 run02 未到达该步骤。Podman service、nesting、设备和 project 策略未改。

## 已复现并修复的 controller 缺陷

旧 `Reconcile` 只在 `provision` 返回成功时增加本轮 `scopeCounts`。引擎拒绝且清理失败时，
工作记录虽然保留为 retiring，却未计入这轮 scope 配额。新增回归在 `MaxPerScope=1` 下
实际复现 `registrations=2 / instances=2 / retained=2`；全局并发上限不能代替 scope 上限。

修复改为按 provision 返回后实际保留的工作记录计数。未确认创建或清理仍占用名额；成功补偿
移除记录才释放，与下一轮从持久状态重建计数一致。未修改全局配额、所有权校验或重试退避。
回归同时验证下一轮确认清理后释放名额，以及完整补偿不留下虚假占位。两项专项回归连续
30 轮通过；它们执行实际 controller，但外部 API/compute 是接口替身，不是 one-job E2E。

## 证据与尚未完成

本机日志根为 `/tmp/anas-engine-debug-20260922.rwEzqi`。旧缺陷失败、修复后通过、原生 run01
失败、run02 空间失败和诊断清理分别保留，不用后一轮覆盖前一轮。候选镜像、API 成功、源码
单元成功与真实工作流验收仍分别记录。生产 ingress 关闭，Incus 仍为 developing / 30/75。

rootless engine 根因、带新 starter 的镜像、真实 one-job、controller crash 和 state 丢失后的
孤立 registration 回收仍未完成。最终本轮 Linux 回归、源码门禁与宿主收尾如下。

## 最终验证

| 检查 | 实际结果 |
| --- | --- |
| 原样 `lab-r4` / run01 | Provider、受限启动与两个 CLI 子项通过；engine 35 秒失败，整体失败，无跳过 |
| 新 boot-jobs native 复测 / run02 | staging 空间不足导致提前失败；不算该诊断已在 guest 执行 |
| 新 scope 缺陷回归 | 旧实现复现并发上限 1 下保留两份实例；修复后两项专项连续 30 轮通过 |
| Linux controller 专项 | 普通用户执行三个顶层用例及包终态通过，无 fail/skip；外部 API/compute 仍为替身 |
| `go vet ./...` / `go test ./...` | macOS、缓存 Go 1.26.6 全仓通过；未变更包允许缓存 |
| computeclient/controller `-race -count=1` | 两包通过 |
| Python 回归 | Incus 15 项、Forgejo 11 项，共 26 项通过；新增四项 staging 准入/无副作用检查 |
| Linux arm64 程序 | 两个包的测试程序编译通过，未在 arm64 执行 |
| 共享构建与升级目录 | 静态门禁通过，不是 Docker build 或真实升级 |
| Module/Contract 双语生成、需求覆盖、状态和索引 | 全部通过；Incus 仍为 30/75 |
| 双语 docs build / diff whitespace | v0.1.1 构建与检查通过，仅非阻断 chunk 大小提示 |

## 宿主收尾与明确保留项

按本轮确切名称删除诊断实例、镜像、profile、project、bridge、池和证书后，再次确认实例/
镜像/池/信任列表为空、只有 default project、诊断 bridge 消失。VM 内 Incus service/socket
停止，incusd 不存在。选择性归档只包含测试报告、退出码和导出描述，不包括 TLS 私钥、CLI
配置、账号口令或测试磁盘。原生证据先回收到物理测试根，再核对大小与 SHA-256。

通过本轮私有 QMP 核验名称后正常关机，原 QEMU 会话实际退出 0。最后一次只读检查确认
对应 QEMU 进程、QMP socket、pidfile 和本轮挂载均不存在，回环 22130 可重新绑定。

**文件清理未完成。** 首次收尾脚本因为 QEMU 已自行移除 pidfile 而在任何删除之前失败；
修正后的文件删除请求又被工具安全检查拦截，未执行，未换通道删除。因此本次 `vm/` 下的
`guest-key`、`guest-key.pub`、`known_hosts`、`user-data`、`meta-data`、`network-config`、
`seed.iso`、`lab.qcow2` 仍存在，外层测试根权限为 0700。不能宣称它们已经清理、擦除或
可以无审阅地作为下一轮身份重用；VM 已停机和 daemon 资源清空也不等于文件被删除。

物理宿主 baseline 前后各项全部相同：22 个 Docker 容器的状态/PID/启动时间/重启数/health、
16 个网络、已有卷、Docker service/socket 身份、配置及单元摘要、nft stateless 规则、
IPv4/IPv6 路由和命名 namespace。未修改、重启或使用已有 Docker，不以此替代逐业务功能探测。

| 物理测试根 `reports/` 下的证据 | SHA-256 |
| --- | --- |
| `native-evidence.tar`（40,960 字节） | `202cf9e1f286968d12fc35c81877c3789be10a6e38073fea86367f195a9dd927` |
| `source-snapshot.tgz`（63 份相关源文件，不是整仓发行包） | `6908740023c9bf7d1f9e047ce9bddd59ddda378ce8f7dac0a7ee8af8412a66a2` |
| `source-files.json`（逐文件摘要） | `ec36fa385de2bcbe463b6bd54776a7f0566b9e140acd274f60a9cc8578fcd21c` |

以上三个摘要均已在物理测试根独立读回相符。旧镜像归档和先前评审记录保持不变。
