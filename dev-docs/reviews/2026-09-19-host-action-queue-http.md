# 宿主动作共享队列与 HTTP 接续核对

> 状态：历史记录；2026-09-19，master / 49bbf45 加此前暂存及未暂存变更。
> 直接修改 `/Users/whl/Documents/anas`，未提交、未改变宿主服务或文件属主。不是生产安装验收。

## 来源与恢复核对

先读[需求索引](../requirements/index.md)和[计划索引](../plans/index.md)，接续
[Incus 计划](../plans/incus-module.md)、[宿主要求](../requirements/host-action-channel.md)与
[宿主计划](../plans/host-action-channel.md)。稳定机制见[架构 §12](../../docs/architecture/host-action-channel.md)。

MCP 恢复后实际检查原文件和 Git 状态，确认上一轮只有 `host_action_service.go` 与
`job_owner.go` 落盘，HTTP/config 补丁未落盘。没有根据错误回执猜测成功或直接重复覆盖原工作树。

## 本轮实现

`HostActionService` 连接原 Store/lease/broker，入队只接受固定预检，采用 action 级重试键和合流；
当前 actor 及冻结版本在入队、执行、终态检查，失权的 queued job 不启动，未知 running 任务
不重试。HTTP 的空对象 POST、Full/TLS/owner、Origin/CSRF、注册 workspace 和错误投影复用
既有边界；直连与代理使用同一服务，不建立新的 root HTTP/RPC 或任务数据库。

`anasd` 显式 opt-in 后在监听前等待 HostActionService 就绪并在退出时收尾，默认不启动通道。
取消分流避免 Action ABI 误走旧 executor；关闭开关时仍可为此前 running 宿主 job 写恢复审计
并保留既有 daemon_restarted 阻断。API 返回原 job 查询位置，非完成回执；专用 CLI invoke 与
页面按钮没有新增。OpenAPI、生成的前端类型、51 条路由清单和中英文错误提示同时维护。

非 root Linux 配置读取是独立的 root-owned 0640/primary group/单链接/可信目录链路径。
现有 root 0600 配置验证没有放宽。`CheckJobOwner` 不存原始 Cookie、不刷新凭据；代理权限只
以本地未过期记录和当前配置为准，不声称即时远端 IdP 查询。本地 owner 任务与浏览器寿命分离。

测试发现并修复正常停机时完成通知与取消同时就绪的选择竞态、queued cancel 与 worker 扫描竞争后
错误停止服务的问题。另一个首次失败来自新 runtime 测试夹具遗漏 ABI success 必需的 Value，
修正的是夹具，不是放宽正式协议。新增 API 错误码的双语门禁发现缺项，补齐后全仓回归通过。

## 验证记录

| 检查 | 结果 |
| --- | --- |
| `go test ./...` / `go vet ./...` | 本轮最终业务代码在 macOS arm64 全仓通过 |
| 实际 Store 队列、HTTP/CSRF、身份复核、daemon 生命周期专项 | 通过；runtime 使用私有测试夹具，不作为 socket/systemd 证据 |
| API 类型生成、一致性、前端类型检查与测试 | 通过，18 个测试文件、72 项前端测试 |
| `go test -race` | hostaction、jobexecutor、consoleauth、consoleconfig、httpapi、cmd/anasd 六包使用 `-count=1` 通过 |
| Linux amd64/arm64 全仓源码编译 | 两种架构均通过，CGO_ENABLED=0 |
| 六包 Linux 双架构测试交叉编译 | 十二次通过，含新增 Linux 配置读取测试；未运行这些 Linux 测试二进制 |
| 主界面与应急前端构建 | 均通过；重建后 internal/webui、cmd/anasd 测试再次通过 |
| Module 源生成/检查、Contract 源检查 | 通过；修改源文档后生成，不手改镜像 |
| 需求/计划索引生成及覆盖、状态、一致性检查 | 全部通过，仍为 Incus 30/75、HOSTACT 0/13 |
| 共享构建检查 | 通过，仅静态校验，未实际构建 Docker 镜像 |
| 文档站构建 | 通过，v0.1.1；保留非阻断的 500 kB chunk 大小警告 |
| `git diff --check` / Go 格式检查 | 通过；原 60 个暂存文件保留 |
| Linux 原生配置读取、新 HTTP→root/systemd/Incus/KVM 链路 | 未执行；不能由交叉编译或本机夹具替代 |
| 已有升级测试目录门禁 | 本轮重跑仍失败：registered module ai_agent has no upgrade test entry；该目录未修改，不称完整 CI 通过 |

## 尚未解决的生产迁移

实际调用链 `newTLSManager → consoletls.RootOwnedFileSecurityCheck` 加默认私钥检查仍要求
root 所有且不允许组/其他用户读取。服务配置的 0640 读取不适用于 TLS key；只把 anasd 的
User/Group 改为非 root 将不能读取现有 root:root 0600 私钥。须同时设计并验证受管密钥读取及
热更新、原 console_store/workspace 的权限、Docker 访问、安装策略及同版本单元安装/升级/撤销。
没有通过放宽 TLS、加 Docker 组、扩大 capability、改默认 unit 或批量 chown 绕过约束。

Incus 包安装/配置/登记/所有权/对称卸载、二段确认、镜像供给及生产 ingress 仍按原计划继续。
Incus 30/75、HOSTACT 0/13 和 developing 不因这条代码接线改变；无真实宿主变更。
