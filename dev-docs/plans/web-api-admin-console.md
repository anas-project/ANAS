---
doc_type: plan
status: partial
created: 2026-08-16
updated: 2026-08-29
---

# ANAS Web API 与管理前端实施计划

> 状态：**部分实施（36/137）**。M0（只读骨架）、M0.5（配置元数据）、M0.6（约束语义）与
> M1A（管理通道与本地认证底座）已落地；当前入口为 M1B。首次引导写入闭环、前端、任务执行、配置写入与安装发布集成尚未实现。
> 日期：2026-08-16，更新：2026-08-29

需求：[Web API 与管理前端要求](../requirements/web-api-admin-console.md)。该特性不单独建架构文档，
设计写在要求文档的 §3—§5 与 §9 决策记录里。

本文只记录**落地顺序、里程碑与剩余工作**。每个阶段“做对了”的判定标准不在这里：里程碑正文
用章节指针给出阅读入口，§5.1 用需求 ID 给出精确范围，两者都不复述要求原文，避免同一条约束
在两处各写一遍而后失步。ID 归属与 e2e 记录的一致性由 `npm run docs:check-requirements` 门禁。

本计划只承诺依赖顺序，不再在跨安全、前端、任务与发布的里程碑标题里给出天数估算；
每个切片进入实施前再按当时的调用点 inventory 与测试环境估算。

## 1. 给实施者

**当前入口：M1B。** M0、M0.5、M0.6、M1A 已落地（见 §2 落地快照）。首版特权边界已经确定为“不需要 `CAP_SYS_ADMIN` 的子集”，不再阻塞 M4；Traefik/OIDC 是独立 M1.5 支线，不阻塞直连 M1B—M4，但必须在 M5 汇合后才能宣告首版完成。

**规范来源是要求文档的 §10 需求矩阵**，不是本文。本文只回答「先做什么」。每个里程碑的精确范围以 §5.1 的需求 ID 归属为准；里程碑正文里的章节指针只是阅读入口。

**每次改动后跑这一组**，它们是 CI 的门禁，本地先过再提：

```bash
go test ./... && go vet ./... && go run ./cmd/gen-module-docs --check
```

```bash
npm run docs:test-requirements && npm run docs:check-requirements && npm run docs:build
```

```bash
npm run docs:check-requirement-status && npm run docs:check-plan-status
```

**改动纪律：**

- 新增或废弃需求时，同步更新要求文档的矩阵与本文 §5.1 的归属表——`docs:check-requirements` 会拦住不一致。
- 完成一条需求后在 §5.1 更新状态；跑过 e2e 后在 §5.3 填脚本、环境、日期、结果四列，只写「跑过了」等于没写。
- 改 `anas` 或 Module 的功能时，同一次改动更新受影响的文档（仓库 `AGENTS.md` 的既有要求）。
- 本文的进度描述必须与代码一致。宁可写「未开始」，不要写一个没验证过的「已完成」。


## 2. 当前落地快照（2026-08-29，工作树基于 `4793f70`）

| 范围 | 已完成 | 尚未完成 |
| --- | --- | --- |
| M0 只读骨架 | `internal/deployment`、`internal/application`、`internal/api/httpapi`、`cmd/anasd`、workspace registry、OpenAPI；health/system/status/deployment list/detail 与 Module Command list/detail 共用类型化服务，不调用 CLI 子进程 | 前端、任务执行、写操作、安装与 systemd 集成 |
| M0.5 元数据 | 所有内置 global 与 Module 参数均显式声明类型；`unknown=0`；生成器、Module 参数表和 release gate 共用 inventory | M1B 配置 HTTP schema/表单投影 |
| M0.6 约束语义 | `input_required`、legacy `required`、`must_resolve` 三阶段语义；默认值存在性/来源；范围、长度、pattern、format；所有配置入口的统一规范化与校验 | 条件/跨字段规则继续由 resolver、plan 或 Hook 执行，不伪装成单字段 schema |
| M1A 管理通道与本地认证底座 | root-owned 严格服务配置；默认 `lan` 的 IPv4/IPv6 wildcard；固定端口 TLS/明文识别；Host/Origin/CSRF、显式路由策略与直连代理头剥离；bootstrap/local Argon2id 会话；fail-closed JSONL 审计；lego 热重载/LKG；显式 token/临时自签 CLI；OpenAPI 双向覆盖 | M1B config/plan/apply、job execution、CA 与 redirect；M1.5 受信代理身份入口 |
| M1B 首次引导后端闭环（部分实施） | 持久 `bootstrap → enrollment → full` 状态机；可恢复的 enrollment 推进与首个 owner 原子提交及浏览器 `303` handoff；durable job/event JSONL core、有界同进程 append receipt 与未知增长全量重验、进程生命周期执行租约与显式崩溃恢复；只读 job 查询、SSE 重放/缺口与存续重认证；运行时 `LOCK_NB` 非阻塞取锁 | physical journal compaction；config GET/validate/PUT；plan/apply；每 workspace 串行队列与 job-owned execution；内部 CA 路由；HTTP→HTTPS redirect |

当前生产 `anasd` 只接受 root-owned 服务配置与 registry 中的 workspace ID，HTTP DTO 不返回 workspace、deployment
或 Secret 的本机路径；默认 `lan` 静态绑定 `0.0.0.0` 与可用时的 `[::]`，数值 Host 必须匹配连接实际命中的本机地址，配置允许的 DNS Host 才可按名访问。服务从 `console_store` 读取持久单向 capability state：首次为 `bootstrap`，验证通过的 lego 证书推进到 `enrollment`，首个本地 owner 的可恢复提交再推进到 `full`；既有 M0 workspace 查询只在 `full` 状态经 HTTPS 与 owner 会话开放。配置写入和 apply 仍未开放。生产目录
`cmd/anasd`、`internal/api/httpapi`、`internal/application`、`internal/deployment` 与
`internal/configschema` 不包含任何内置 Module 名称或 Module 分支。未来配置 HTTP API 必须继续消费
统一 schema，不能破坏这个边界。

配置实现的当前精确基线：

- bundled inventory 的总数和类型分布由统一 inventory 生成；稳定要求是 release gate 的 `unknown=0`；
- `input_required`/CLI `required` 只包含 `global.base_domain`、`global.email`，最终
  `must_resolve` 集合由同一 inventory 投影；有静态默认值或无条件来源的参数不是 caller-input required；
- 已声明且有运行证据的单字段约束包括 DNS name、IANA timezone、language/locale、
  IPv4、`1..65535` 端口范围、`samba_dc.max_log_size >= 1`、Casdoor 同步周期、
  domain prefix 与 Forgejo Incus profile 的 DNS-label pattern、Runner image fingerprint 和 VersityGW credential/region 约束；schema 本身还
  支持 `min_length`/`max_length`；
- `config set`、import/reimport、`config plan`、deployment lock/plan/materialize 和 remote lock
  都使用同一声明、地址、类型和 constraints 校验；失败发生在持久化前并保持配置、摘要、
  Secret Store 与 lock 原子不变；
- 只有 Secret Store 的 `lifecycle_managed` 记录可以在私有视图中满足 caller input；所有
  kind 都只作为等值来源的脱敏 taint，不能经错误、list 或 plan 投影明文；
- calculate/render Hook 的 Env 与 Secret patch 先整包校验键、ownership、exports、碰撞和
  schema 再应用；Hook 只能刷新本 Module 已拥有的 `generated/module-hook` Secret。

当前可复核验证记录见 §5.2；参数数量与调用点数量不得再充当跨提交验收标准。后续里程碑不得把
M0.5/M0.6 的 schema 门禁解释为配置 HTTP API 或前端已经完成。

## 3. 里程碑

### M0：服务层与契约骨架 — 已实施

已完成共享应用层、独立 HTTP 适配器、OpenAPI 契约与只读 daemon 入口：`version`/`status`/`deployments list`/`deployments inspect` 以及 Module Command list/detail 只读用例已抽出，CLI 输出不变；`api/openapi.yaml`、`cmd/anasd` 与 health、system、workspace status、deployment list/detail、Module Command list/detail GET 路由就绪；workspace 只由 registry ID 选择。当前生产监听边界随后由 M1A 扩展为静态 `lan`/`loopback` 配置：`lan` 绑定 IPv4/IPv6 wildcard，Host 按连接实际命中的本机地址或配置允许的 DNS 名校验。M0 本身不包含认证、前端、任务与任何写操作（包括 Module Command invoke）。

验收：要求文档 §3（架构与代码边界）、§4.1（通用约定）、§7.2（输入边界）。现有 Go 测试与 CLI contract 全绿；只读服务路径不接触全局 `os.Stdout`/`os.Stderr`。全仓剩余调用点由后续服务化切片与 CI inventory 约束，不在文档中固定数量。

### M0.5：配置元数据回填 — 已实施

见 §2 落地快照。验收：要求文档 §2.2。

### M0.6：配置解析与约束语义 — 已实施

见 §2 落地快照。验收：要求文档 §2.2。参数总数、来源分布与调用点数量只作为绑定提交的复核快照；稳定门禁是 bundled inventory 的 `unknown=0`、统一 schema 与原子校验语义。

### M1A：管理通道与本地认证底座 — 已实施

- 增加 root-owned 服务配置、认证密钥与 console store 边界；管理端口与 `lan`/`loopback` 是静态配置。
- `lan` 明确监听 IPv4 wildcard，并在主机启用 IPv6 时监听 IPv6 wildcard；这是 NAS 首次配置的产品决策，不做“自动挑一个常用网卡地址”。
- 建立 bootstrap token、Argon2id、本地会话、CSRF、Host/Origin 校验、监听器身份边界，以及非 full 状态的显式路由 allowlist/默认 404。
- 在 wildcard 开放前先建立 append-only 最小审计与统一脱敏，覆盖 token/session 的签发、兑换、认证失败、撤销与本地登录尝试；真实状态转换及其原子审计随 M1B 的持久状态机启用。
- 消费 lego 证书并实现原子 TLS 热重载、last-known-good 与同一固定端口的 TLS/受限明文协议识别；无证书时可由 CLI 显式生成临时自签证书。

验收：要求文档 §5.1、§5.3—§5.5 与 §7.1 中属于直连通道和本地认证基础的约束。M1A 不开放配置写入或 apply；这些写操作只有 M1B 的任务、审计和原子提交边界就绪后才启用。

### M1B：首次引导后端闭环

- 先落任务/事件/审计 JSONL、每 workspace 串行队列、SSE 重放、幂等键和运行时锁可观测性，再开放首次配置与 apply。
- 配置 GET/validate/PUT 直接投影统一 schema，以强 ETag 和敏感值三态完成配置、Secret Store 与 digest 的原子提交。
- 首次 `plan`/`apply` 使用类型化服务、job-owned context、显式子进程环境和既有补偿路径；请求断开不得取消已入队任务，`NETWORK_NAMESPACE_PATH` 下需要 sudo 的路径明确拒绝，bootstrap 的风险确认绑定当前 transaction 而不要求尚不存在的 owner 密码。
- 实现并持久化 `bootstrap → enrollment → full` 单向状态机、逐状态路由 allowlist、HTTPS handoff 和首个 owner 原子提交。
- 状态提交前使用 M1A 的封闭审计适配器持久记录 from/to、actor 与固定 reason；审计失败则不提交转换。
- 审计、脱敏、confirmation 与资源限制必须先于任何引导写操作；LAN 明文窗口的主动劫持风险按要求文档 §5.2 明确接受。

验收：要求文档 §2.1—§2.3、§4.1、§4.3—§4.4、§5.2、§7.3 与 §7.5 中首次引导可达的范围。M1B 完成后，一个全新 NAS 能从 LAN 明文入口提交首次配置/apply，经 lego 证书 handoff 到 HTTPS 创建 owner，且状态不可倒退。

### M1C：前端壳与完整引导体验

- 建立独立 Vue/TypeScript/Vite 工程和 OpenAPI 客户端，产物嵌入 `anasd`；主 SPA 与独立应急 UI 包分别验证。
- 完成引导、登录、总览、配置草稿/validate/计划/保存、直连 `allow_risky` 确认、任务恢复、证书与访问、无 workspace 指引及 zh/en 错误映射。
- 明文页面持续显示不可关闭的风险横幅，列出 `ssh -L` 与临时自签 TLS 替代路径；完整级提示轮换引导期 DNS 凭据。
- 浏览器不持久化 secret，任务日志只作为不可信文本渲染，并完成 CSP、frame、MIME、来源与缓存策略。
- 用真实 IAM 运行/停止场景证明直连本地登录始终可用；这属于直连主线，不等待代理入口集成。

验收：要求文档 §3.1、§5.2、§5.6、§6 与 §7.1 的浏览器侧约束；真实浏览器刷新后仍可恢复任务观察，不依赖内存状态。

### M1.5：Traefik/OIDC 支线

- 为代理身份监听器建立 Unix socket、mTLS 或等价不可旁路的传输边界；来源 IP allowlist 本身不作为身份信任。
- 经 Traefik 的入口只接受覆盖歧义身份头且带稳定 issuer/subject 的受信代理；`platform_admin` 映射 `owner`。
- 完整级下代理入口与直连入口能力相同；本地登录不出现在代理入口，也不随 IAM 健康状态开关。
- OIDC 高危操作使用近期认证证明生成动作绑定的 StepUpProof，不在 `anasd` 接收 IdP 密码。

验收：要求文档 §5.4—§5.5 的代理入口、恢复路径、角色映射和 step-up 约束。本支线不阻塞 M2—M4 的直连管理路径；未满足不可旁路传输边界时，不开放代理身份监听器。M5 是直连主线与本支线的汇合门禁，M1.5 未完成时 M5 不得标为完成。

### M2：生命周期与任务扩展

- 把 start/stop/restart/rollback 全部迁到类型化应用服务和任务 API，与 M1B 的 plan/apply 一起关闭生命周期服务化总要求，补齐容器实时状态与健康探测。
- 任务取消只在声明安全的阶段生效；外部命令按进程组 TERM→宽限期→KILL 并进入补偿检查。
- 前端在执行前展示 runner 展开的真实依赖 chain。

验收：要求文档 §2、§4.3 与 §6.2 的完整生命周期范围；取消、崩溃恢复、CLI/Web 并发与环境隔离均有验证证据。

### M3：Module 管理

- Module enable/disable、catalog、sync/update 使用类型化服务与任务 API。
- 在 UI 中呈现版本、配置态、运行态、健康和入口地址。

验收：要求文档 §4.1—§4.2 中 Module/catalog 与列表范围。

### M4：快照、备份与账户

- 迁移 snapshot、backup、local admin 用例；首版明确只交付不需要 `CAP_SYS_ADMIN` 的子集。
- 对暂不支持的 restore/delete 等特权操作，由服务端类型化 preview API 返回影响摘要、argv 与安全转义的确切终端命令；前端不拼接命令，也不保留必然失败的 Web 按钮或执行写路由。
- 高危 preview、step-up、confirmation、备份目标 allowlist、凭据 reveal 防缓存与全量审计同时落地。
- 使用真实 Btrfs/Docker 环境验证既有 `userdata/` 默认不恢复语义。

验收：要求文档 §4.2、§6.1—§6.2、§7.4—§7.5。本阶段没有 ambient capability 前置决策；将来若扩展特权子集，另立需求和威胁模型。

### M5：全局加固与发布

- 用 OpenAPI 双向覆盖验证 §4.2 的最终 API surface 与所有列表分页，并完成登录、配置、任务、补偿、快照/备份等端到端回归。
- 发布流水线新增独立 Node 构建和可复现性检查；构建脚本加入 `anasd` 的静态 amd64/arm64 目标。
- `install.sh` 安装/升级/卸载 `anasd` 二进制、root-owned 服务配置、root 身份 systemd unit 与管理端口；文档明确其权限边界。
- 使用真实 Docker/Btrfs test-env 做破坏性测试；普通单元测试不替代恢复正确性验证。

验收：直连主线与 M1.5 在此汇合；要求文档 §8 与 §10.9、全部 137 项有效需求归属、CI 门禁和 e2e 证据完整，才可把首版标为完成。

## 4. M1B 已完成与下一步

本轮已完成：

1. 生产 daemon 使用持久 capability state，验证通过的 lego 证书以可恢复事务推进 `bootstrap → enrollment`；handoff 绑定 transaction、origin、目标 HTTPS origin 与实际连接 SPKI，以两个目标源 Cookie 加 `303` 完成浏览器闭环；直连 TLS 禁用 session resumption，首个 owner 以可恢复原子提交推进 `enrollment → full`。两种认证事务在 WAL 持久化后都脱离请求取消，并以独立有界 context 完成 publish 或回滚仲裁。
2. `console_store` 已有 durable job/event JSONL core、有界同进程 append receipt 证明下的增量 tail 刷新、未知/跨进程增长全量重验、读写 record/批次限长、进程生命周期执行租约与显式崩溃恢复，以及只读 job list/detail 与 SSE 重放/缺口；SSE 在 batch/poll/heartbeat 边界做不续期 idle TTL 的存续重认证，terminal 前排空尾事件且已追平的 `EventSource` 重连返回 `204`。当前尚未实现物理 journal compaction、执行队列或把 plan/apply 交给 job-owned context。
3. 部署运行时锁已改为 `LOCK_NB` 非阻塞尝试与 context-aware 重试，避免锁等待永久占住请求执行路径。

下一步按依赖顺序实施：

1. 在开放任何引导写路由前，把审计、confirmation、幂等键、容量限制与业务提交组成可恢复的一致边界，并用 crash-safe compaction/segment rotation 让事件保留策略实际回收 journal 磁盘。
2. 把统一配置 schema 投影为 GET/validate/PUT，以强 ETag 和 sensitive 三态原子提交配置、Secret Store 与 digest。
3. 将首次 plan/apply 接到类型化应用服务与持久 job，实现每 workspace 串行执行和 job-owned context；保留既有锁与补偿语义，并拒绝无 TTY sudo 的 `NETWORK_NAMESPACE_PATH` 路径。
4. 补齐内部 CA 下载路由、明文入口到目标 HTTPS origin 的 redirect，以及 config/job/state/handoff 的契约、竞态和真实证书 e2e。

M1B 完成后进入 M1C，构建嵌入式双语前端与完整引导体验；已接受的 LAN 明文风险不改变写入原子性、审计和任务生命周期要求。

## 5. 实现检查表

本节是施工过程中**逐 PR 更新**的记录，不是完工后一次性补的总结。只记录 CI 无法自动判定的内容——CI 能门禁的事项写「哪个提交上绿」，不重复打勾。

需求条目见[要求文档](../requirements/web-api-admin-console.md) §10 需求矩阵；下表只记归属与进度，不复述要求原文。

### 5.1 需求归属与覆盖

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0 | R-001、R-002、R-003、R-006、R-007、R-009、R-046、R-143 | 已完成 |
| M0.5 / M0.6 | R-011—R-016、R-030 | 已完成 |
| M1A | R-060—R-062、R-064—R-068、R-071、R-080、R-081、R-086、R-090、R-108、R-119、R-140、R-157、R-163—R-166 | 已完成 |
| M1B | R-020—R-022、R-024—R-029、R-033、R-040—R-044、R-048—R-051、R-054—R-056、R-058、R-059、R-063、R-069、R-082—R-085、R-089、R-091—R-093、R-102、R-112、R-118、R-142、R-145—R-148、R-151、R-153、R-159、R-167 | 实施中；状态机、认证事务、任务持久化与只读 SSE 已完成，配置写入和执行链待实现 |
| M1C | R-004、R-010、R-070、R-087、R-088、R-103、R-104、R-113、R-120、R-121、R-125、R-126、R-128—R-131、R-156 | 未开始 |
| M1.5 | R-072、R-100、R-101、R-105—R-107、R-109—R-111、R-114、R-122 | 未开始 |
| M2 | R-023、R-031、R-034、R-057、R-124 | 未开始 |
| M3 | R-032 | 未开始 |
| M4 | R-047、R-115—R-117、R-123、R-127、R-132、R-133、R-144、R-149、R-150、R-152、R-155 | 未开始 |
| M5 | R-005、R-008、R-045、R-053、R-154、R-160—R-162 | 未开始 |

覆盖统计：137 项有效需求全部有归属，其中已完成 36 项；R-052、R-141、R-158 三项复合要求已废弃并由原子要求取代。**每个有效 ID 必须恰好归属一个里程碑**；新增或废弃需求时同步更新本表，否则门禁会拒绝。

### 5.2 CI 门禁

不逐条打勾，只记录最近一次验证过的提交或工作树基线。

| 门禁 | 命令 | 最近验证基线 |
| --- | --- | --- |
| 单元测试 | `go test ./...` | 工作树（HEAD `4793f70`，2026-08-29）通过 |
| 竞态 | `go test -race ./internal/audit ./internal/consoleaudit ./internal/consoleauth ./internal/consoleconfig ./internal/consolejobs ./internal/consolelistener ./internal/consolestate ./internal/consoletls ./internal/tempconsolecert ./internal/protocolmux ./internal/api/httpapi ./internal/runner ./cmd/anasd` | 工作树（HEAD `4793f70`，2026-08-29）通过 |
| 静态检查 | `go vet ./...` | 工作树（HEAD `4793f70`，2026-08-29）通过 |
| 参数 inventory / effect | `go run ./cmd/gen-module-docs --check` | 工作树（HEAD `4793f70`，2026-08-29）通过 |
| 需求/计划一致性 | `npm run docs:test-requirements && npm run docs:check-requirements && npm run docs:test-status && npm run docs:check-requirement-status && npm run docs:check-plan-status` | 工作树（HEAD `4793f70`，2026-08-29）通过：137 项有效要求全部有归属，另有 3 项已废弃 |
| 文档构建 | `npm run docs:build` | 工作树（HEAD `4793f70`，2026-08-29）通过 |
| 静态交叉编译 | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /private/tmp/anasd-m1b-linux-amd64 ./cmd/anasd`；arm64 同命令改 `GOARCH` | 工作树（HEAD `4793f70`，2026-08-29）两架构静态 ELF 通过 |

### 5.3 e2e 执行记录

CI 查不了这些——它们需要真实 Docker、Btrfs、域名或主机。**只写「跑过了」无法复核**，四列都要填。

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-044 | 待补 | | | |
| R-049 | 待补 | | | |
| R-057 | 待补 | | | |
| R-059 | 待补 | | | |
| R-063 | 待补 | | | |
| R-069 | 待补 | | | |
| R-072 | 待补 | | | |
| R-093 | 待补 | | | |
| R-101 | 待补 | | | |
| R-102 | 待补 | | | |
| R-103 | 待补 | | | |
| R-104 | 待补 | | | |
| R-113 | 待补 | | | |
| R-114 | 待补 | | | |
| R-120 | 待补 | | | |
| R-122 | 待补 | | | |
| R-124 | 待补 | | | |
| R-125 | 待补 | | | |
| R-131 | 待补 | | | |
| R-133 | 待补 | | | |
| R-155 | 待补 | | | |
| R-162 | 待补 | | | |

脚本放在 `test-env/scripts/`，命名沿用既有的 `server-<主题>-e2e.sh`。

### 5.4 文档同步

AGENTS.md 要求功能变更与文档在同一次改动中保持一致。以下是本特性已知需要同步的点。

| 项 | 状态 |
| --- | --- |
| [管理员账户体系](../../docs/architecture/admin-account-system.md)同步 `platform_admin → owner` 与当前 Samba `Admins` 解析 | 已完成（2026-08-29） |
| [CLI 契约索引](../../docs/reference/contracts/index.md)措辞修正（R-006） | 已完成（`6841c0e`） |
| 安装文档写明「引导窗口不具备机密性」与两个访问地址（R-105） | 未开始 |
| [服务配置参考](../../docs/reference/anasd-service-configuration.md)写明完整级 DNS、lego 不增加管理面 IP SAN（R-064） | 已完成（2026-08-29） |
| `anasd` root-owned 服务配置与 workspace 外 console store 运维文档（R-157） | 已完成（2026-08-29） |
| systemd root 身份/可写路径与安装升级卸载文档（R-154、R-162） | 未开始 |

## 6. 支线依赖与退出条件

| 项 | 影响范围 | 退出条件 |
| --- | --- | --- |
| Traefik 到代理身份监听器的不可旁路传输 | 仅 M1.5 | Unix socket、mTLS 或等价机制已有集成测试；仅有来源 IP allowlist 不算完成 |
| OIDC 近期认证证明的标准化转发字段 | 仅 M1.5 高危操作 | 能验证 issuer、subject、认证时间并绑定单次 StepUpProof；否则代理入口不开放高危操作 |

M4 首版范围已固定为不需要 `CAP_SYS_ADMIN` 的子集，不再等待 ambient capability 决策。直连 M1A—M4 不等待 Traefik/OIDC 支线；M5 与首版完成状态必须等待 M1.5 达到上述退出条件。
