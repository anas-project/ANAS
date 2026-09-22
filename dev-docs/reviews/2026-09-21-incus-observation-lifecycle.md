---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# Incus 独立观测校验与生命周期接续核对

基线：`claude/forgejo-docs-audit-20260920` / `3f5242e`，加前轮镜像供给与 Provider 就绪性工作树。
直接修改原 checkout，保留前轮已暂存、未暂存与未跟踪文件；没有切换分支、提交或推送。
前轮结果见[镜像供给记录](2026-09-21-incus-image-supply-readiness.md)，不计为本轮新增验证。
本次对应 Incus M11，尤其 R-063、R-087/R-088 的实例生命周期、消费者之外的授权验证边界。

## 实际复现的缺陷

新增反例先在旧实现运行，以下行为均实际失败，再修改正式调用路径：

1. `IncusFactReader` 接受没有自身 managed 标记、标记 false、缺少或具有不同 workload 的实例，
   只凭 Running、前缀、NIC 和分配就返回事实。
2. 读取器只比较安装授权中的部分字段。调用方沿用同一 project/前缀，改变端口、deployment、
   域名或 auth 仍可成功调用 daemon。
3. 观测 JSON 只拒绝重复精确 key；`auth` 与 `Auth` 同时出现时，Go 大小写兼容解码可改变选定身份。
4. 宿主投影对完全相同请求可重复接受第一次响应，不能区分本次观察与历史响应重放。
5. 投影适配器取消 context 后仍返回正常响应，客户端接受了该结果。
6. renderer 消费路由期间实例/授权失效，执行器没有加载后复查，仍把路由记为成功。

首组反例包括 4 种实例标签、4 种授权替换及 JSON 别名；第二组包括投影重放、取消和加载后竞态。
日志留在本机临时目录，不把主机信息或原始服务响应写入仓库。

## 实现变更

读取器匹配构造时深复制的完整授权，后续采样只使用已安装的副本。托管/workload 检查来自实例
自身 config，不从请求或继承 profile 补造；这些可修改标签不是跨项目的安全边界。
新增 `IncusFactReader.ValidateTarget` 实现执行器 `Observer`，每次重新观察并与完整目标比较，
与 `WorkspaceSource` 共用事实比较函数。当前 Core epoch、请求和 auth/Host 仍由独立授权源核对。

API JSON 按选定 Go 类型递归拒绝字段大小写别名，同时保留未知扩展字段与大小写敏感的 map 名称。
重复 key、非法 UTF-8、尾随内容与深度限制继续有效。HTTP 响应读完后再次检查 context 和证书有效期；
失败只给固定错误类别，不回显 endpoint、证书或 daemon 正文。

内部宿主投影升级为 `anas.incus-http-host-projection/v2`，客户端自行生成每调用独立的
32 字节随机 observation ID，响应必须回传完全相同的值；拒绝 caller 预置 ID、旧/缺字段协议、
旧响应、字段别名，以及取消后的成功。上下文最长 30 秒；适配器必须遵守上下文，包装层不能强行
中断一个不返回的任意实现。该 ID 不用于持久认证或幂等。尚未接线的宿主处理器必须在本次调用后
真正读取新状态，不能给缓存数据换 ID；没有新增 root 动作或特权入口，也不修改持久回执 schema。

执行器在 renderer 确认路由消费后，保存 ready 之前，再次检查授权、实例和 journal。失败走既有
路由/许可/连接/guest 路由/地址释放顺序。已经短暂可见的路由不能由事后核验追溯为从未发布；
持续分配、许可期限、双向 TCP 与内核身份仍须独立保障。

## 验证范围与结果

`incus_reader_test.go` 使用实际 TLS 1.3、固定服务端证书及精确客户端证书校验，合成 Incus 六类
GET 元数据；容器/VM 类型分支、两次采样、停止/暂停/重启、地址/分配变化、错误身份和传输反例均覆盖。
这不是实际 Incus daemon，也不证明证书在 daemon 上只读。

`controller_lifecycle_test.go` 连接真实 Controller、Planner、上述 mTLS 读取器与真实
FileStateStore/flock/journal。明确的请求源和 Host/renderer/probe 适配器验证暂停/停止→完整
退役→相同身份恢复且换新 token；取消时独立清理；清理失败保留地址与 retiring；重开日志后继续
撤销。它不运行真实 Core 目录投影、Traefik、内核网络或进程 kill，不将日志重开当作完整 crash 验收。

| 检查 | 本轮结果 |
| --- | --- |
| 新增反例在修复前运行 | 实际失败；修复后通过 |
| `go test -count=1 ./internal/computeingressruntime ./internal/incusingresshost` | 两包通过 |
| 同两包 `go test -race -count=1` | 通过，不使用结果缓存 |
| `go vet ./...` / `go test ./...` | 通过；未变化包允许 Go 缓存 |
| Linux amd64/arm64 两包测试二进制 | 四份交叉编译通过；没有在 Linux 上执行 |
| 生成文档/需求计划索引/状态/共享构建/升级目录 | 生成及检查均通过；共享构建和升级目录检查是静态检查，不是 Docker build 或升级 E2E |
| 双语文档构建与 `git diff --check HEAD` | 通过；文档为 v0.1.1，仅有非阻断 chunk 大小警告 |
| 真正 Incus/Docker/Traefik/Linux/KVM/双栈 | 未运行 |

本轮新增 14 个顶层测试入口，包括已存在 `executor_test.go` 中的加载后竞态回归；含多组子用例。
没有新增 Go 依赖，没有修改前轮 `7.3.0-r2` 的 Provider 镜像身份或声称已发布产物。

## 独立身份仍待交付

2026-09-21 查阅 [Incus main 授权文档](https://linuxcontainers.org/incus/docs/main/authorization/)：
TLS 的 restricted 约束不是只读授权；改用其他授权后端时，原证书的 project 列表不再由 TLS 后端
执行，替代策略必须保持原有隔离。本次没有修改 daemon 全局授权、引入 OpenFGA 服务或签发所谓
“只读 restricted 证书”。该资料是 main 文档，不是固定 7.3.0 或发行版 Incus 6.x 的兼容证明。

仍缺：服务端真正只读的独立身份供给（或完成评审的受限宿主观察方案）、实际 root 投影处理器与
活动 Core 授权绑定、VM/TAP、health、目录/UID/挂载与生产服务装配，以及暂停发生在采样间的
窗口、IP/ifindex 复用、已有双向 TCP、真实事件丢失和崩溃恢复验收。
最终核对仍为 macOS arm64、Go 1.26.6，未发现 `test-env/targets.local.yml`，HEAD 仍为 `3f5242e`。
没有选择生产 SSH 目标或执行 nft/ip/conntrack 宿主变更。开发环境回归不能解除这些边界，
Incus 保持 developing、30/75，生产 ingress 关闭。
