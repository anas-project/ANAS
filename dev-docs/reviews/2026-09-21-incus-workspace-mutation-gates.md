---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# Incus 工作区写任务与中介排空互锁核对

基线为 `claude/forgejo-docs-audit-20260920` / `3f5242e` 加前轮未提交工作树。直接修改原
checkout，保留之前已暂存、未暂存和未跟踪文件，未切换分支、提交或推送。仅使用仓库源码、
现有文档与本机编译测试，没有外部资料检索或新增依赖。承接
[宿主配置协调记录](2026-09-21-incus-ingress-coordination.md)，对应 M11 的 R-063/R-087/R-088。

## 实际复现与实现

先新增 `TestWorkspaceMutationWaitsQueuedForIngressDrain`，在旧调用路径运行，实际失败：
普通部署 worker 直接执行应用，没有开始中介排空。仅增加安装参数、未使用它时反例仍失败；
修复领取入口后同一行为断言通过。

`anasd` 创建一个协调器，显式交给普通 Executor 与 HostActionService；不再让普通部署/维护
操作绕开宿主配置使用的工作区屏障。范围是该普通 worker 领取的所有 mutating job，包括部署
切换/生命周期/回滚、本地管理员轮换、Module 命令和快照。服务拿写锁可能触发事务恢复，所以
不凭操作名缩小范围。独立本地 CLI 和其他进程不是这条队列的调用者，不计作完成。

普通领取仍使用原 Store 的 FIFO、容量、并发与补偿规则。提交前观察器只启动非阻塞排空，
不等待、不重入 Store、不获取工作区锁，也不在 jobs.lock 中执行网络操作。排空期间没有
running job，旧中介清理所需的宿主只读任务可通过同一 Store/队列完成。成功屏障绑定 job ID、
kind、工作区、actor、创建时间和规范请求摘要；应用工厂调用前再核对真实 running claim。

开始排空前的审计是授权尝试，不是假称 job 已开始。实际领取前及应用执行前重新读取 actor
权限。完整模式复用 CheckJobOwner；bootstrap/enrollment 只认当前状态和原事务的已确认 apply，
不能转成 local-admin 轮换或后来 full-owner 权限。这些检查不延长批准、不续登录会话、不替代
应用层原 plan/config/deployment 漂移校验。

排空失败与领取前权限撤销用 `RejectQueuedObserved` 原子提交 rejected 事件和 failed 终态，
分别投影固定错误码 `ingress_drain_failed`、`job_authorization_revoked`。未开始的任务没有
started_at、执行结果或待补偿标记，不伪装为用户取消。该接口拒绝 action-ABI 与已经 running
的任务；回放仅新增受约束的 queued → failed，没有第二份日志或新字段。旧二进制可能拒绝
这种记录，因此没有宣称任意降级兼容；不能删去审计/队列记录来绕过它。

排空中的任务被取消后，下一任务仍等待原排空结束。失败 Controller 保留原锁与旧读取器；
轮询不自动重试。原任务确认终态且所需补偿已被现有流程确认后，才能释放屏障。应用成功返回、
已追加 success 事件、缺失或改变的任务都不能代替终态落盘。释放不会自动启动替代中介。

## 验证

新增 15 个顶层 Go 测试入口及多组子用例。联合测试使用真实 Store、确认账本、队列、
ControllerService、FileStateStore/flock 与 journal；应用操作、宿主网络、Traefik 和 root
执行器为明确夹具。版本状态测试重开真实队列文件验证未执行失败回放；这不是 Linux 实机验收。

| 检查 | 本轮结果 |
| --- | --- |
| 领取前排空反例 | 旧路径实际失败；修复后通过 |
| 部署/轮换/取消/身份/终态故障专项 | 通过；涵盖同队列清理依赖、不自动重试、未知终态保留与补偿确认 |
| 未执行失败的审计原子性、重开及状态边界 | 通过 |
| 完整 owner、bootstrap/enrollment 事务与权限漂移 | 通过；独立授权服务为明确夹具 |
| `go test -race -count=1 ./internal/consolejobs ./internal/jobexecutor ./cmd/anasd` | 三包通过 |
| `go vet ./...`、`go test ./...` | 通过，未变更包允许 Go 缓存 |
| Linux amd64/arm64 三包测试二进制 | 上轮工具终态已确认六份交叉编译通过；本轮恢复连接后补记，没有在 Linux 上执行 |
| 文档生成、索引、需求覆盖、静态目录与文档构建 | 上轮最终门禁通过；本轮恢复连接后重新生成/检查 Module 文档、检查状态、构建双语站点及 diff 均通过，补齐最后英文小节排序后的验证 |

开发中新增批量测试夹具漏填 Store 要求的请求摘要，专项失败；补齐合法测试数据后全部通过，
没有放宽 Store 校验。一次不匹配格式化上下文的补丁整体被拒绝，重新检查后应用，未把失败
补丁当作已落盘。代码和文档最终需按实际文件读回核对。

## 未完成范围

独立 `anas credential rotate`、直接应用调用与独立 Module action worker 没有因为这份
内存协调器自动受约束；跨进程恢复、生产启动器/UID/挂载、root 网络动作、health、VM/TAP、
持续 IP/ifindex 身份及旧 TCP 会话仍待实现或实机验收。当前默认协调器的空 owner 集合不能
证明宿主没有旧网络工件。镜像签名分发、真实烘焙/启动/配额/双栈/one-job/回滚/prune 矩阵不变。

Incus 保持 `7.3.0-r2 / developing`，完成统计不因上述本机测试提升，生产 publication gate
保持关闭。本轮不启动实际 daemon、不选取生产 SSH 目标、不修改宿主网络。

接续记录：上轮最终回填期间连接失败，本轮已恢复并读回本文件；上述历史编译结果不计作本轮
新执行的测试。独立 CLI 与进程退出后的进一步互锁见
[跨进程工作区围栏记录](2026-09-21-incus-workspace-process-fence.md)。
