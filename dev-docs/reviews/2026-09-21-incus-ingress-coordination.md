---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# Incus 配置任务与中介排空协调核对

基线为 `claude/forgejo-docs-audit-20260920` / `3f5242e` 加前轮工作树，直接修改原 checkout。
保留全部已暂存、未暂存和未跟踪变更，没有切换分支、提交或推送。承接
[中介所有者记录](2026-09-21-incus-controller-owner.md)，对应 M11 的 INCUS-R-063、R-087/R-088。
本轮只依据仓库源码、现有设计及实际编译测试，没有外部资料检索或新增依赖。

## 补齐的调用路径

前轮 ControllerService 已能持有原 journal/flock 直到排空，但配置动作与共享服务停机并不
等待它。本轮新增 ControllerCoordinator，显式维护同一进程的工作区所有者与配置屏障。
启动时同步认领新 owner，避免注册后尚未 Run 就被 Stop 判成未启动；与配置占用使用同一互斥。
多个 scope 原子取得屏障，全部先接收停止信号，之后才等待各自清理。

HostActionService 对 observer 配置封闭目标 scope；对共用 daemon 的 install/configure/enroll/
uninstall/image-prune 封闭所有登记工作区。排空异步执行，但仍属于受跟踪的 owner，配置 job
保持 queued。轮询不重新发起清理，也不堵住共享队列。一次明确批准的新变更可重试此前保留的
失败 owner；清理失败则拒绝未开始的配置任务，原中介、锁和读取器继续保留。

成功屏障绑定 job、invocation、action、workspace 和完整冻结 request 摘要。执行前重查 actor，
真实 broker 授权回调在执行前后也检查该绑定。正常终态或未开始拒绝才释放；未知执行或身份
漂移保留屏障。原 root Claim、五分钟批准、计划/状态比对、审计和独立退出监督没有绕过。
释放不自动创建替代中介；过期计划不通过排空等待获得延期。

正常 owner 取消立即阻止新的配置准入，不直接取消 broker context。已开始的受监督宿主动作
先走原执行流程；后续仅继续只读观察/预检，等待中介排空后才关闭 runtime 和执行租约。
停机清理失败不自动重试或退出放锁，RetryIngressShutdown 只允许可信生命周期所有者显式
继续旧清理。它不是新 HTTP 路由，不变更 observer scope，也不恢复发布。

## 验证方法

新增 11 个顶层测试：6 个 coordinator 用例、5 个共享队列用例。它们验证配置完成前不能重启、
失败排空保留原锁、显式恢复、取消等待、多 scope 独立停止、永久停机屏障、清理依赖经同队列
完成、清理失败时配置不执行、正常停机依赖顺序、排空后权限撤销以及未知执行保留屏障。

联合测试使用真实 ControllerService/Executor、FileStateStore/flock、共享 Store、执行租约、
审计和一次性确认账本。宿主网络、Traefik、应用探测和 root 执行器使用显式测试适配器。
清理依赖测试让 RemoveHTTP 经原共享队列完成一个只读 job，用于复现并防止单队列自等待，
不是声称已经注册或实测生产网络清理动作。

开发中一个测试误用了不存在的导出 CloneJob，编译检查失败；改为仅复制被修改的 Action 值，
再完成专项与全仓检查。未放宽生产校验，也不把编译失败当作故障场景测试通过。

| 检查 | 本轮结果 |
| --- | --- |
| Controller/协调器/共享队列专项 | 通过 |
| `go test -race -count=1 ./internal/computeingressruntime ./internal/jobexecutor` | 两包通过 |
| `go vet ./...`、`go test ./...` | 通过，未变化包允许 Go 缓存 |
| Linux amd64/arm64 测试二进制 | computeingressruntime、jobexecutor、hostaction、cmd/anasd 共八份交叉编译通过；未在 Linux 上执行 |
| Module/Contract 文档、索引、需求覆盖及状态 | 生成与检查通过，Incus 仍为 30/75 |
| 共享构建与升级目录 | 静态检查通过；不是实际 Docker build 或升级 E2E |
| `npm run docs:build` | 双语构建通过，v0.1.1；仅非阻断 chunk 大小警告 |
| `git diff --check HEAD` | 通过，包含前轮保留变更 |
| 实际 Incus/Docker/Traefik/systemd/UID/挂载 | 未执行 |

## 未完成与不能推导的结论

默认协调器的空登记仅表示本进程没有已注册的中介，并不证明宿主没有历史路由。生产 launcher
必须与队列共享此实例，并让每个新中介按原 journal 做真实库存恢复。本轮没有安装/启动这个
生产 launcher，也没有绕过 publication gate。现有固定 scope 配置机制不是启动器。

异常 broker 丢失、损坏队列、强杀或跨进程重启仍须单独监督与持久恢复；本轮正常停机路径
不能当作这些异常场景的网络关闭证明。部署 apply/凭据轮换的所有入口也尚未统一加入该协调器。
生产 UID/挂载、root 网络动作、health、VM/TAP、持续 IP/ifindex 身份及旧 TCP 会话仍未完成。
镜像签名分发、真实烘焙/guest 启动/配额/双栈/one-job/回滚/prune 矩阵继续按原计划追踪。

Incus 仍为 `7.3.0-r2 / developing`、30/75，正式镜像 catalog 仍不得填入未经验证的产物。
