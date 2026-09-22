---
doc_type: review
status: current
created: 2026-09-21
updated: 2026-09-21
---

# Incus 观察配置的计划交付与撤销

直接继续 `claude/forgejo-docs-audit-20260920` / `3f5242e` 的原工作树，保留前轮暂存、未暂存和
未跟踪文件。不提交、推送、安装服务或修改实际宿主网络。本轮目标是消除观察处理器依赖手写
scope 的缺口，不把 scope 自动生成等同于默认生产中介已启动。

## 新动作与五问

`incus.ingress.observer.plan` 与 `incus.ingress.observer` 的配置动作对应 INCUS-R-063、R-087、
R-088；共用通道约束对应 HOSTACT-R-001、R-003、R-009、R-010、R-012、R-013。refresh 和 disable
是同一有界配置动作的互逆操作，不是任意文件管理。无需新增服务、socket 或依赖库。

| 审阅问题 | 本轮选择 |
| --- | --- |
| 能否不用 root | 固定 root 配置和归属状态不能交给消费者/中介写；继续使用已有 hostd，不新增入口 |
| 权限是否最小 | 输入仅 workspace ID 与 refresh/disable；实际配置、版本、路径和证书由宿主推导；无脚本、命令、下载或网络写入 |
| 留下什么产物 | 固定目录下单个 0600 scope；原宿主状态新增摘要/generation/状态；无新 job 数据库 |
| 有无对称撤销 | disable 使用同一计划确认，删除已登记文件并保留 disabled 墓碑；不依赖 daemon 或旧部署在线 |
| 半途失败和重跑 | pending 先持久化，观察拒绝；新计划只接受已登记旧/待提交字节；未知内容保留，目标再变化需先 disable |

## 实现范围

计划是只读的：使用已存在的 host/workspace 锁，不创建目录或配置。refresh 从受保护 anasd
登记、受管 bundle、完整活动 HTTPAuthorizationSnapshot 和 Provider 本机绑定推导 scope。
仅支持 container，不降级 VM。daemon 版本来自已经 pin 证书和客户端身份的 mTLS 双读，进入
批准摘要；不从响应学习新证书，也不宣称特定版本兼容。apply 在锁内重算计划，状态漂移拒绝。

文件写入使用 os.Root 限定目录、NOFOLLOW/NONBLOCK、普通单链接文件、精确原字节比对、临时
文件同步、原子替换/删除和最终读回。目录替换或当前文件不符会失败，清理只触及本次临时名。
existing host state 保存 pending/enabled/disabled；替换已成功但状态仍为 pending 时，不能
继续观察。失败响应没有可用的成功结果，重试先展示新的计划。若 enabled 持久提交已完成、仅
最终确认丢失，则磁盘仍可能是已批准的新配置；新增反例明确验证该未知结果不能解释为没有
副作用，必须重新计划读回，旧批准不可重放。完全相同的已提交刷新不重写。

旧手写文件没有归属记录时不采纳；disabled 后回放旧 scope 不能复活。记录最多 64 个，墓碑不
按 TTL 淘汰。旧二进制严格解码可能拒绝新增字段，不能剥除记录以规避；不宣称无条件降级兼容。
卸载动作现在先拒绝 enabled、pending 或无效观察记录，防止先移除其依赖再要求撤销。

CLI 与 HTTP 的 observer phase 复用原登录、权限、共享队列和一次性确认，不增加公开凭据端点。
HTTP plan 只接受 operation，工作区从已授权 URL 注入；apply/入队/队列恢复/执行绑定均核对
scope 和工作区。OpenAPI 的新分支与 TypeScript 生成类型同步，没有新增前端交互页面。

## 开发中实际发现

首次共享确认集成测试失败：动作采用 `.observer.apply`、计划采用 `.observer.plan`，不满足
已有确认协议的 `<action>.plan` 约束。修正执行动作名为 `incus.ingress.observer`，保留共享
确认语义，没有给 ABI 添加例外或放宽校验。修正后真实 Store/确认账本/执行绑定专项通过。
两次批量工具请求因无法判定安全状态被拦截；后续独立格式化和正常测试请求成功，没有执行被拦截的命令。

## 验证记录

| 检查 | 本轮结果 |
| --- | --- |
| 配置生成/重复/撤销、失效计划、未知文件、失败写入及重开恢复 | 本机专项通过；真实文件目录，宿主状态/API 为明确夹具 |
| mTLS 版本双读、身份改变和取消 | 本机专项通过；不是实际 daemon |
| 共享 Store/一次性确认/跨工作区/执行绑定 | 命名缺陷先失败，修复后专项通过 |
| CLI/HTTP 严格输入与 scope 推导、OpenAPI 分支 | 专项与全仓检查通过 |
| `go test ./...` / `go vet ./...` | 通过；未变化包允许缓存，后续仅增加最终确认丢失的测试源并单独补测 |
| 五包 `go test -race -count=1` | incusprovision、computeingressruntime、hostaction、jobexecutor、httpapi 全部通过 |
| 最终确认丢失及 Observer 专项 `-race -count=1` | 增加反例后重新执行通过；失败返回不等于无持久副作用 |
| Linux amd64/arm64 四包测试二进制 | 八份交叉编译通过，新增最终反例后重编 incusprovision 两份；没有在 Linux 上执行 |
| 前端 API 生成/一致性、typecheck、Vitest | 通过；20 个测试文件、92 项测试，未新增交互页面 |
| Module/Contract 文档生成检查、需求覆盖、需求与计划索引、文档状态 | 通过；Incus 仍为 30/75 |
| 共享构建与升级目录 | 静态检查通过，不代表实际 Docker 构建或升级 E2E |
| `npm run docs:build` | 最终双语源构建通过，v0.1.1，仅有非阻断 chunk 大小警告 |
| `git diff --check HEAD` | 通过，包含前轮保留的暂存与未暂存修改 |
| 真实 root/systemd/Incus/网络联合验收 | 未执行 |

本轮新增 13 个顶层 Go 测试入口，并扩展已有 HTTP/读观察 fixture。没有新增 Go 依赖。
最终核对为 Darwin arm64 / Go 1.26.6，未发现 `test-env/targets.local.yml`；未自行选择生产
SSH 目标或执行实际安装。HEAD 仍为 `3f5242e`、原分支不变，未提交或推送。

## 剩余边界

本轮只完成受确认的配置交付机制，不会在每次 apply 或启动时未经确认刷新授权。生产中介启动、
UID/挂载与旧路由排空、health、VM/TAP、完整内核生命周期、真实长连接和事件丢失/崩溃仍待实现
或验收。disable 是观察授权的撤销，不是网络已经关闭的证明。镜像签名发布和真实烘焙/guest
启动/配额/双栈/one-job/回滚/prune 矩阵不在本轮冒充完成。

生产 publication gate 保持关闭，Incus 仍为 developing；本机夹具或 Linux 编译不提升实机
验收状态。文件系统接口核对依据为 [Go 官方 os.Root 文档](https://pkg.go.dev/os#Root)，本机编译器接口是实际编译依据。
