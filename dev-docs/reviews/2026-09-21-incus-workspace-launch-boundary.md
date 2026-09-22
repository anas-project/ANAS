---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# Incus 中介启动准入与目录身份核对

基线：`claude/forgejo-docs-audit-20260920` / `3f5242e` 加前轮工作树；直接修改原 checkout，
保留已有暂存/未暂存/未跟踪变更，未切换分支、提交或推送。承接
[跨进程围栏](2026-09-21-incus-workspace-process-fence.md)，对应 M11 的 R-063/R-087/R-088。
本轮依据仓库源码与本机测试，没有外部检索或新依赖。

## 先复现再修复

新增两个回归组，在旧代码上实际失败：八种请求/凭据/日志/路由目录重叠或别名均被工作区
构造器接受；renderer 原地改变、同内容替换 inode 以及输出目录被新建同名目录替换也被接受。
这些是启动边界缺陷，不表示已有生产中介被攻破；生产路径原本没有开启。

补上目录角色隔离及实际安装检查；路径规范化与祖先 device/inode 一起用于别名/包含关系。
对凭据、注册文件及所有租约目录核对权限、归属和登记内容；renderer 脚本固定字节摘要及
文件身份，后续编译仍复用既有 ANAS_TRAEFIK_ROUTE 渲染机制。Script pin 是受信安装输入的
固定值，不是新的上游签名验证。工作区装配不能被公开 Source/Renderer 路径字段另行定向。

构造后捕获的目录/文件在进入实际 WorkspaceStateStore、初始化日志/写入围栏之前重新核对。
这一步不在独占工作区锁内再次取得 Core 共享锁。并发构造只允许认领同一读取器集合一次。
对从未取得执行会话的失败，关闭该次新开的私有读取器；通用 Controller 的可能共享资源
不据此擅自关闭。已取得会话的失败仍保留原生命周期语义。

## 启动调用链

`HostActionService.StartIngressWorkspace` 仅在实际服务运行、owner 未取消且 actor 获授权时
接受内部调用；使用服务自身 context 和 coordinator，并自行构造 HostObservationInvoker。
调用者不能传入另一观察 invoker。`ControllerCoordinator.StartHostWorkspace` 打开 host-only
私有配置，要求其 scope 匹配目标工作区，连接经验证的 WorkspaceReaders 与原生命周期所有者。
未增加监听器、命令、新的持久状态格式或无认证的排空入口。

返回的 owner 必须通过 Ready/Done/Stop 判读，不将成功认领解释为恢复完成。完整本地联合
用例使用真实 Core 冻结文件、注册目录、私有文件、flock/journal、Controller 和固定证书
HTTPS/BasicAuth 读取器；API 内容是合成的，Host/Probe 为明确适配器，请求目录为空。
移走请求根会使发布授权校验失败，但不废掉原 Traefik 读取器，独立库存清理仍可结束并解除围栏。
这不等于实际 guest、活动路由或旧 TCP 已通过联合验收。

## 验证记录

新增 8 个顶层 Go 测试入口，含目录角色、文件替换、启动前复查、scope/队列绑定、并发认领、
取消、读取器释放及本地 HTTPS 生命周期等子用例。原读取器测试曾只用占位路径及未私有化的
临时状态目录；加强准入后它失败，改为创建真实 renderer 文件和 0700 日志目录，未放宽校验。

| 检查 | 结果 |
| --- | --- |
| 目录重叠和 renderer 替换反例 | 旧实现实际失败；修复后通过 |
| computeingressruntime / jobexecutor `-count=1` | 通过 |
| 两包 `-race -count=1` | 通过 |
| 全仓 `go test ./...` / `go vet ./...` | 通过；GOPROXY=off，未变更包允许 Go 缓存 |
| Linux amd64 / arm64 | computeingressruntime、jobexecutor、runner、cmd/anasd 共八份测试二进制交叉编译通过；没有在 Linux 上执行 |
| Module/Contract 文档、需求/计划索引、需求覆盖与状态检查 | 生成与检查通过；Incus 统计仍为 30/75 |
| 共享构建与升级目录 | 静态检查通过；没有执行 Docker build 或升级 E2E |
| `npm run docs:build` | 最终双语源构建通过，v0.1.1，仅非阻断 chunk 大小警告 |
| `git diff --check HEAD` | 通过，包含前轮保留的工作树变更 |
| 实际 UID/挂载、Incus/Traefik/guest/活动 TCP | 未执行，不从本地 HTTPS 夹具或编译推导通过 |

一次组合门禁请求被工具拦截，没有计作执行；随后使用明确的检查命令分别执行并取得上述
成功终态。文档批量补丁曾因标题不匹配被拒绝，读回确认未写入后按实际标题重新应用。

## 未完成范围

本轮增加内部受信 owner 的统一启动入口及目录隔离，不安装/启动生产服务、不创建 UID、不执行
挂载、不提供默认允许的 Host/Probe。仍要求当前受信文件所有者；非特权独立进程如何访问
受保护的 Core 元数据与工作区锁，需要实际权限/句柄交付设计及实测，不能简单 chmod 放开。
目录别名检查不是 mount flags、user namespace 或能力集合的完整验证。

生产安装配置源/启动调用、root 网络动作、应用 health、VM/TAP、地址/ifindex 连续性和真实
网络撤销仍待实施/验收；镜像签名及原宿主矩阵不变。Incus 继续 `7.3.0-r2 / developing`，
完成统计仍为 30/75，publication gate 不变。
