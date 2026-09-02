---
doc_type: plan
status: partial
created: 2026-08-16
updated: 2026-09-03
---

# ANAS Web API 与管理前端实施计划

> 状态：**部分实施（129/149）**。M0（只读骨架）、M0.5（配置元数据）、M0.6（约束语义）、M1A（管理通道与本地认证底座）、M1B（首次引导后端闭环）、M1C（前端壳与完整引导体验）、M1.5（Traefik/OIDC 受信入口）、M2（生命周期与任务扩展）和 M3（Module 管理）已落地；当前入口为 M4。安装发布集成仍属 M5。
> 日期：2026-08-16，更新：2026-09-03

需求：[Web API 与管理前端要求](../requirements/web-api-admin-console.md)。该特性不单独建架构文档，
设计写在要求文档的 §3—§5 与 §9 决策记录里。

本文只记录**落地顺序、里程碑与剩余工作**。每个阶段“做对了”的判定标准不在这里：里程碑正文
用章节指针给出阅读入口，§5.1 用需求 ID 给出精确范围，两者都不复述要求原文，避免同一条约束
在两处各写一遍而后失步。ID 归属与 e2e 记录的一致性由 `npm run docs:check-requirements` 门禁。

本计划只承诺依赖顺序，不再在跨安全、前端、任务与发布的里程碑标题里给出天数估算；
每个切片进入实施前再按当时的调用点 inventory 与测试环境估算。

## 1. 给实施者

**当前入口：M4。** M0、M0.5、M0.6、M1A、M1B、M1C、M1.5、M2、M3 已落地（见 §2 落地快照）。首版特权边界已经确定为“不需要 `CAP_SYS_ADMIN` 的子集”，不再阻塞 M4；Traefik/OIDC 支线已达到 M5 汇合所需退出条件。

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


## 2. 当前落地快照（2026-09-03，工作树基于 `c9e9a12`）

| 范围 | 已完成 | 尚未完成 |
| --- | --- | --- |
| M0 只读骨架 | `internal/deployment`、`internal/application`、`internal/api/httpapi`、`cmd/anasd`、workspace registry、OpenAPI；health/system/status/deployment list/detail 与 Module Command list/detail 共用类型化服务，不调用 CLI 子进程 | 前端、任务执行、plan/apply 等其他写操作、安装与 systemd 集成 |
| M0.5 元数据 | 所有内置 global 与 Module 参数均显式声明类型；`unknown=0`；生成器、Module 参数表、release gate 与 M1B 配置 HTTP schema 共用 inventory；配置响应投影 registry `available_modules` 与字段规范 `document_path`，M1C 表单既不遗漏无字段 Module，也无需复制 runner 寻址规则 | 后续新增参数继续由同一 release gate 约束 |
| M0.6 约束语义 | `input_required`、legacy `required`、`must_resolve` 三阶段语义；默认值存在性/来源；范围、长度、pattern、format；所有配置入口的统一规范化与校验 | 条件/跨字段规则继续由 resolver、plan 或 Hook 执行，不伪装成单字段 schema |
| M1A 管理通道与本地认证底座 | root-owned 严格服务配置；默认 `lan` 的 IPv4/IPv6 wildcard；固定端口 TLS/明文识别；Host/Origin/CSRF、显式路由策略与直连代理头剥离；bootstrap/local Argon2id 会话；fail-closed JSONL 审计；lego 热重载/LKG；显式 token/临时自签 CLI；OpenAPI 双向覆盖 | M1C 嵌入式前端；M1.5 受信代理身份入口；M2 进程组取消 |
| M1B 首次引导后端闭环（已完成） | 持久 `bootstrap → enrollment → full` 状态机；可恢复的 enrollment 推进与首个 owner 原子提交及浏览器 `303` handoff；durable job/event JSONL core、有界同进程 append receipt、未知增长全量重验、sealed checkpoint 物理 compaction、跨 generation Store 换代、进程生命周期执行租约与显式崩溃恢复；audit 独立 MaxEvents/Retention storage primitive、sealed generation compaction、跨进程 Writer 换代及 full-owner 分页查询，生产策略固定为 `MaxEvents=0`、`Retention=0`；只读 job 查询、SSE 重放/缺口与存续重认证；配置 GET/validate/PUT 的 schema 投影、随机 opaque 强 ETag、敏感值三态与 crash-safe 原子提交，R-044 外部手改 `412` 已通过真实 daemon HTTP e2e；运行时 `LOCK_NB` 非阻塞取锁；CLI 共用的类型化 Plan/Apply core、workspace 固定 Module view、基于 opaque validator 的稳定 plan digest、Apply 锁内漂移复核与 daemon 子进程受限环境/可取消 context；daemon 生命周期持久 job executor、每 workspace 串行 Claim、job-owned context、终态前事件；audit-before-commit 覆盖 confirmation 签发/消费、job 创建/running/terminal、失败与启动恢复；Plan、bootstrap 首次 Apply 及 full 直连本地 step-up/Apply HTTP/OpenAPI；step-up 与 confirmation/幂等原子消费；内部 CA 下载、证书 issuer 状态与规范 HTTPS redirect；R-049、R-059、R-063/R-069、R-093、R-102 均已通过真实 Linux/浏览器 e2e | M1C 嵌入式双语前端与完整引导体验 |
| M1C 前端壳与完整引导体验（已完成） | 独立 `web/` Vue 3/TypeScript/Vite 工程与锁文件；由 OpenAPI 生成类型并使用 `openapi-fetch`；确定文件名的主包和不依赖 Vue 的独立应急包由 `go:embed` 进入 `anasd`；根页面/固定资产路由有显式 state/transport/listener policy 与 OpenAPI 双向覆盖；公开 `console_state` 驱动 M0/bootstrap/enrollment/full 入口；明文界面持续显示不可关闭的中英双语 LAN 风险横幅；bootstrap token 只在内存中交换，enrollment handoff 只经已验证的顶层 form POST，HTTPS owner 创建、本地登录、双语错误码映射与未知码回退、DNS 凭据轮换提醒、内部 CA 下载入口及无 workspace `anas init` 指引已接入；workspace 选择、schema 分组字段、Module 增删、敏感值显式 set/unset、受保护字段只读、纯内存草稿、validate 变更预览和强 ETag/首次创建条件保存已接入；plan preview、bootstrap/full step-up、guarded retry与幂等 apply 已接入；认证会话可在整页刷新后恢复并轮换 CSRF，部署与任务抽屉通过 SSE 实时更新且以任务 GET 终态对账；独立应急 UI、真实 Casdoor 运行/停止时的本地登录及 bootstrap/full 风险确认均已通过真实 daemon + Google Chrome Beta e2e | 无（M1C 已完成） |
| M1.5 Traefik/OIDC 支线（已完成） | 独立 TLS-only 受信 listener；客户端 CA + SPKI pin + 精确源 IP；固定身份头拒绝重复/歧义且直连剥离；proxy session、`platform_admin → owner` 与目录组审计；不超过 5 分钟且动作/主体/plan 绑定的一次性 step-up；本地登录/应急路由 proxy `404`；`anas console status` 与访问页恢复地址；oauth2-proxy 双 bridge；Traefik 命名 mTLS `serversTransport` 与稳定 Secret-Store 客户端身份；R-072/R-101/R-114/R-122 真实 Linux e2e | 无（M1.5 已完成；安装自动接线仍属 M5） |
| M2 生命周期与任务扩展（已完成） | workspace status 改为 Compose 派生的实时聚合与逐 Module runtime/health；start/stop/restart/rollback 由 CLI 与 daemon 共用类型化应用服务，HTTP 先返回 deployment/digest 绑定的服务端 preview，再以精确有序 chain 入队幂等持久任务并在运行时锁内复核漂移；cancel 只在 executor 注册的安全阶段接受，Unix 子进程组按 TERM→2 秒宽限→KILL，终态后重新取得运行时锁执行 snapshot/container/credential 补偿检查；完整级 SPA 展示 runner 展开的真实 chain 后才允许确认；R-031/R-034/R-057/R-124 已通过 Linux 静态二进制测试，R-124 另经实际 Chrome Beta UI 验证 | 无（M2 已完成） |
| M3 Module 管理（已完成） | CLI、daemon HTTP 与 job executor 共用类型化 Module 管理服务；完整级开放 Module 状态、catalog、sync/update 与强配置 ETag 绑定的 enable/disable；配置事务发布前 fail-closed 审计并复用既有 CAS/WAL；deployment 冻结公开 HTTP(S) 管理入口；SPA 展示配置/安装/期望/部署/目录版本、实时运行/健康/容器数、依赖和入口地址；OpenAPI 双向覆盖与路径泄漏负向测试已通过 | 无（M3 已完成） |

当前生产 `anasd` 只接受 root-owned 服务配置与 registry 中的 workspace ID，HTTP DTO 不返回 workspace、deployment
或 Secret 的本机路径；默认 `lan` 静态绑定 `0.0.0.0` 与可用时的 `[::]`，数值 Host 必须匹配连接实际命中的本机地址，配置允许的 DNS Host 才可按名访问。服务从 `console_store` 读取持久单向 capability state：首次为 `bootstrap`，验证通过的 lego 证书推进到 `enrollment`，首个本地 owner 的可恢复提交再推进到 `full`；既有 M0 workspace 查询只在 `full` 状态经 HTTPS 与 owner 会话开放。配置 GET/validate/PUT 在 bootstrap 的直连明文或 TLS、以及 full 的直连 TLS 上开放；三者统一要求 bootstrap/local 会话、workspace scope 与权限，validate/PUT 另外要求 Origin/CSRF。M0 和 enrollment 保持默认 `404`。配置保存只提交 desired config、Secret Store 与 managed state；Plan 不写运行态，bootstrap Apply 只入队持久任务。生产目录
  `cmd/anasd`、`internal/api/httpapi`、`internal/application`、`internal/deployment` 与
`internal/configschema` 不包含任何内置 Module 名称或 Module 分支。Plan 在 bootstrap/full 直连策略下开放；Apply 在 bootstrap 直连明文/TLS 以及 full 直连 TLS 下开放，full 必须先由当前本地 owner 会话重新验密取得动作/对象/状态绑定的单次 step-up。完整级受信代理入口提供相同 API 能力，但使用受信近期 OIDC assertion 签发 step-up，且不接受本地或 IdP 密码。配置 HTTP API 必须继续消费
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

当前可复核验证记录见 §5.2；参数数量与调用点数量不得再充当跨提交验收标准。配置 HTTP API 的完成依据是
M1B 应用服务、HTTP/OpenAPI 契约、事务恢复与负向测试，不是 M0.5/M0.6 的 schema 门禁；M1C 前端与真实故障 e2e 已完成。

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

验收：要求文档 §5.1、§5.3—§5.5 与 §7.1 中属于直连通道和本地认证基础的约束。M1A 本身不开放配置写入或 apply；M1B 在审计与原子提交边界就绪后开放 desired config 保存，plan/apply 还要等待任务执行边界就绪。

### M1B：首次引导后端闭环

- 先落任务/事件/审计 JSONL、SSE 重放与运行时锁，再开放首次 desired config 保存；每 workspace 串行队列、幂等键和 job-owned context 就绪后再开放 plan/apply。
- 配置 GET/validate/PUT 直接投影统一 schema，以随机 opaque 强 ETag 和敏感值三态完成配置、Secret Store 与 managed state 的原子提交。
- 首次 `plan`/`apply` 使用类型化服务、job-owned context、显式子进程环境和既有补偿路径；请求断开不得取消已入队任务，`NETWORK_NAMESPACE_PATH` 下需要 sudo 的路径明确拒绝，bootstrap 的风险确认绑定当前 transaction 而不要求尚不存在的 owner 密码。
- 实现并持久化 `bootstrap → enrollment → full` 单向状态机、逐状态路由 allowlist、HTTPS handoff 和首个 owner 原子提交。
- 状态提交前使用 M1A 的封闭审计适配器持久记录 from/to、actor 与固定 reason；审计失败则不提交转换。
- 配置 PUT 以持久、脱敏的 `attempt` 和锁内 `authorized` intent 作为提交前否决门，唯一 `operation_id` 贯穿它们、配置 WAL 和 terminal；独立 audit journal 的 terminal 只是有界补充，只有 durable/存疑的 WAL publish 后才如实标记 `indeterminate`。plan/apply 还必须先覆盖 confirmation、job 创建/状态和失败审计；首次 apply 的 confirmation 绑定候选配置与 plan 摘要。LAN 明文窗口的主动劫持风险按要求文档 §5.2 明确接受。

验收：要求文档 §2.1—§2.3、§4.1、§4.3—§4.4、§5.2、§7.3 与 §7.5 中首次引导可达的范围。M1B 完成后，一个全新 NAS 能从 LAN 明文入口提交首次配置/apply，经 lego 证书 handoff 到 HTTPS 创建 owner，且状态不可倒退。

### M1C：前端壳与完整引导体验 — 已实施

- 建立独立 Vue/TypeScript/Vite 工程和 OpenAPI 客户端，产物嵌入 `anasd`；主 SPA 与独立应急 UI 包分别验证。
- 完成引导、登录、总览、配置草稿/validate/计划/保存、直连 `allow_risky` 确认、任务恢复、证书与访问、无 workspace 指引及 zh/en 错误映射。
- 明文页面持续显示不可关闭的风险横幅，列出 `ssh -L` 与临时自签 TLS 替代路径；完整级提示轮换引导期 DNS 凭据。
- 浏览器不持久化 secret，任务日志只作为不可信文本渲染，并完成 CSP、frame、MIME、来源与缓存策略。
- 用真实 IAM 运行/停止场景证明直连本地登录始终可用；这属于直连主线，不等待代理入口集成。

验收：要求文档 §3.1、§5.2、§5.6、§6 与 §7.1 的浏览器侧约束；真实浏览器刷新后仍可恢复任务观察，不依赖内存状态。

### M1.5：Traefik/OIDC 支线 — 已实施

- 为代理身份监听器建立 Unix socket、mTLS 或等价不可旁路的传输边界；来源 IP allowlist 本身不作为身份信任。
- 经 Traefik 的入口只接受覆盖歧义身份头且带稳定 issuer/subject 的受信代理；`platform_admin` 映射 `owner`。
- 完整级下代理入口与直连入口能力相同；本地登录不出现在代理入口，也不随 IAM 健康状态开关。
- OIDC 高危操作使用近期认证证明生成动作绑定的 StepUpProof，不在 `anasd` 接收 IdP 密码。

验收：要求文档 §5.4—§5.5 的代理入口、恢复路径、角色映射和 step-up 约束。当前实现已满足不可旁路传输边界并开放独立代理身份监听器；本支线不阻塞 M2—M4 的直连管理路径。M5 仍是直连主线与本支线的最终汇合门禁。

### M2：生命周期与任务扩展 — 已实施

- 把 start/stop/restart/rollback 全部迁到类型化应用服务和任务 API，与 M1B 的 plan/apply 一起关闭生命周期服务化总要求，补齐容器实时状态与健康探测。
- 任务取消只在声明安全的阶段生效；外部命令按进程组 TERM→宽限期→KILL 并进入补偿检查。
- 前端在执行前展示 runner 展开的真实依赖 chain。

验收：要求文档 §2、§4.3 与 §6.2 的完整生命周期范围；取消、崩溃恢复、CLI/Web 并发与环境隔离均有验证证据。

### M3：Module 管理 — 已实施

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

验收：直连主线与 M1.5 在此汇合；要求文档 §8 与 §10.9、全部 149 项有效需求归属、CI 门禁和 e2e 证据完整，才可把首版标为完成。

## 4. 已完成与下一步

本轮已完成：

1. 生产 daemon 使用持久 capability state，验证通过的 lego 证书以可恢复事务推进 `bootstrap → enrollment`；handoff 绑定 transaction、origin、目标 HTTPS origin 与实际连接 SPKI，以两个目标源 Cookie 加 `303` 完成浏览器闭环；直连 TLS 禁用 session resumption，首个 owner 以可恢复原子提交推进 `enrollment → full`。两种认证事务在 WAL 持久化后都脱离请求取消，并以独立有界 context 完成 publish 或回滚仲裁。
2. `console_store` 已有 durable job/event JSONL core、有界同进程 append receipt 证明下的增量 tail 刷新、未知增长全量重验、读写 record/批次限长、进程生命周期执行租约与显式崩溃恢复，以及只读 job list/detail 与 SSE 重放/缺口；job journal 已用分块 checkpoint、generation、counts 与 SHA-256 seal 做同目录原子替换，目录 fsync 后截断旧 inode，并让其他 Store 在 `jobs.lock` 下全量验证、换代 FD/receipt 后再读写；阈值触发先计算 prospective state 的实际回收收益，无 obsolete history 时不做无收益重写。audit storage primitive 初始化时先持久化含 StoreID/策略的 pristine lock slot，再 fsync 匹配 header；空 lock 可配对既存空 journal（及可明确识别的旧 Event 无完整 record 残尾），首槽 torn 只可配对精确空 journal重写 revision 1，或在完整验证为旧 Event-only journal 后按其水位/commit time 重试迁移；有效 pristine 首槽只用原 StoreID/策略补完空或可证明同 StoreID 的规范 partial header。完整 header 无有效 metadata 拒绝，非空 torn 首槽配 partial journal 也拒绝且不截断。锁内双 512-byte slot 以递增 revision、SHA-256 和交替 `WriteAt`（不 truncate）抗 torn write，恢复使用最高有效槽并可从 torn 最新槽回退，旧单行 metadata 迁移 torn 时也保留旧完整前缀，初始化后无有效槽 fail closed。metadata 记录 generation、sequence/prune/commit-time 水位；每个已确认 append/compaction 在成功返回前更新，journal 回退或 policy mismatch 均 fail closed，而完整验证为同 lineage 前进的 journal-ahead crash window 会写新槽追平；旧 Writer 仍只接受同 lineage、更高 generation 且 retained event/水位无回退的替代。audit 按 Writer-owned commit time 做独立 prefix prune；旧 Event-only journal 以 inode mtime 补足 legacy `recorded_at` 并在首次 append 强制迁移，其他自动换代受 obsolete history 与收益门槛约束。checkpoint 保留 retained `recorded_at` 并严格验证时间，clean pre-rename cancellation 成功清理 temp 后不毒化 Writer，其他歧义或持久化故障 fail closed；reserved temp 的安全残留由下一次锁内 Open/Compact 清理，危险类型不删除。full-owner audit 查询通过同一 Writer 持锁刷新 verified state，按 sequence 倒序分页并在分页前过滤未知 workspace，保留 attempt/authorized/terminal/indeterminate 原始证据，不从 terminal 缺失推断结果。SSE 在 batch/poll/heartbeat 边界做不续期 idle TTL 的存续重认证，terminal 前排空尾事件且已追平的 `EventSource` 重连返回 `204`。audit 的生产保留策略已固定为 `MaxEvents=0`、`Retention=0`，即当前不删除历史且不调度周期清理；daemon-lifetime executor 已持有 execution lease，领取 deployment Apply 后以 job-owned context 执行并写入进度、终态和补偿标记。
3. 部署运行时锁已改为 `LOCK_NB` 非阻塞尝试与 context-aware 重试，避免锁等待永久占住请求执行路径。
4. 配置 GET/validate/PUT 已接到统一 schema 与 workspace registry：GET 只返回脱敏投影，validate/PUT 使用严格 sensitive tagged union、封闭且规范拼写的候选 surface、随机 opaque 强 ETag 与同锁 CAS；256-bit validator 只在实际 generation 提交时生成并持久化，零变更 PUT 保持稳定，validate 不预生成候选值，旧 state 由首个受支持读写者在排他运行时锁内校验内部 content digest 后迁移，内容 SHA 不再出现在 HTTP 或审计。CLI 写入以及 snapshot/backup restore 同样轮换 validator。Module JSON object 往返保留既有 YAML 顺序，新 Module 确定性追加，零语义变化保留原受管字节，Module 集合增删进入响应与审计 change summary。PUT 以 redo-only WAL 将配置、Secret Store 与 managed state 作为一个可恢复提交发布，在 authorized 后再次核对准备时的完整旧 tuple，拒绝非协作手工改动；manifest/stage/目标读取均有显式尺寸上限。每次已认证 PUT 在解析条件头/请求体或解析 workspace service 前先从 CSPRNG 生成 128-bit `operation_id` 并持久化 value-free `attempt`；同一 ID 贯穿锁内 `authorized` intent、terminal 与 WAL manifest。attempt/authorized 是提交前否决门，terminal 使用脱离请求取消的独立有界 context，失败写 daemon 日志而不改写业务结果；WAL 前错误记 `failure`，只有 manifest 已发布或其目录持久化存疑才记 `indeterminate`。受支持 reader 通过同一运行时锁恢复或 fail closed，且恢复会修复目标文件为 `0600`。bootstrap 直连明文/TLS 与 full 直连 TLS 路由均执行会话、权限和 workspace scope 校验，validate/PUT 另外执行 Origin/CSRF 校验；M0/enrollment 默认 `404`，响应与审计不包含 Secret 值。`credential_rotate`、`data_migrate` 与 `immutable` 字段由服务端投影为只读并拒绝写入。R-044 已补 service 层回归用例与真实 daemon HTTP e2e：客户端先取得强 ETag，外部手工修改 `config.yml` 后，GET 和携带旧 ETag 的 PUT 均返回 `412 config_precondition_failed`，且失败请求不覆盖外部内容、不轮换 managed state。
5. deployment `plan` 与 `apply` 已从 CLI 函数抽到同一类型化 application service；CLI 只保留参数解析和既有 JSON/文本渲染。daemon 构造器只读取 workspace 持久化的绝对 Module view，不继承 cwd、`ANAS_MODULE_ROOT` 或调用方路径；Plan/Apply Hook 使用可取消 context，warning/progress 走 `EventSink`。结果对完整类型化计划与当前 opaque config validator 计算稳定 SHA-256 plan digest，内部 content digest 和 config/module 本机路径不进入绑定材料。Apply 在同一 workspace 排他锁内重新计算计划并拒绝 validator/digest 漂移，daemon 明确拒绝需交互 sudo 的 `NETWORK_NAMESPACE_PATH`，materialize/activate、Compose、网络与自动快照的 daemon 子进程已切到显式环境和 job context。Plan HTTP 返回不含本机路径的类型化 DTO 与 CSPRNG 256-bit 单次 confirmation；bootstrap Apply 以规范请求摘要和 `Idempotency-Key` 原子消费 confirmation/创建任务。full 直连会话先在 `/api/v1/auth/step-up` 重新验证当前本地 owner 密码，签发只存摘要、最长 5 分钟且绑定 session/action/workspace/可选 deployment ID/当前 validator+plan digest 的 proof；Plan 校验 proof 后签发 confirmation，Apply 在同一 `jobs.lock` 事务中单次消费两种 proof 并创建幂等任务。完整级受信代理会话用同一 plan/apply surface，不接收密码；issuer、subject、`auth_time`、assertion 与 plan 绑定的一次性 proof 只有在最近认证不超过 5 分钟时签发。
6. `jobexecutor` 已由 daemon execution lease 生命周期持有：每个注册 workspace 独立串行领取持久 mutation，HTTP 请求 context 不参与运行期，warning/progress/terminal 事件进入 `consolejobs.Store`，最终事件先于 terminal 状态，daemon 关机中的任务标为需补偿检查的 `interrupted`。Apply 取锁后的 snapshot/container/credential 中断补偿也已接入同一 context、受限环境和 `EventSink`。confirmation proof 原文只返回调用方、不写 journal；Store 以 plan job 中的服务端绑定和 proof digest 校验 actor/source/transaction/action/workspace/config validator/plan digest/expiry，并在同一 `jobs.lock` 事务中完成单次消费、apply job 与幂等索引写入；同 key 重试先返回原 job，异 key 二次消费被拒绝。job 创建、领取、terminal 与启动恢复均在 `jobs.lock` 提交前调用持久部署审计 observer；审计失败不提交状态，executor 也不会领取未审计任务。confirmation issue 与 plan success、confirmation consume 与 apply create 都以明确的 authorized-intent 语义关联，不声称独立 journal 与 job journal 跨域原子。
7. lego 的独立 `anas-internal-ca.crt` 现作为显式 root-owned 服务配置制品，与 serving pair、issuer、trust bundle 和 marker 一起校验；它必须是当前有效的单个自签 CA、包含于 trust bundle，且 serving issuer 变为 ACME 后仍保留在 LKG snapshot。`GET /api/v1/system` 返回闭集 issuer 与 `m0`/`bootstrap`/`enrollment`/`full` 运行状态，`GET /api/v1/system/ca` 在 enrollment 明文/TLS 下载公开 CA、在 full 仅经 HTTPS owner 下载。enrollment/full 的明文根路径仅以 `308` 跳到服务配置推导的规范 HTTPS origin，不传播 Host/query/credential/body；full 明文请求一旦携带 Cookie、Authorization 或 body 即在路由授权前统一隐藏为 `404`。
8. M1B 的关键并发边界已有跨 Store/`-race` 证明：capability state 与 bootstrap→enrollment 竞争只提交一次，handoff 只有一个消费者，两个 store 视图并发创建首个 owner 只允许一个调用 state publish 且败者不得保留 enrollment credential；job store 在同 workspace 拒绝第二个 mutation 并允许 read-only 并发，daemon executor 对同 workspace 串行、对不同 workspace 可同时进入 job-owned Apply，不退化为全局串行队列。
9. R-049 已在真实 Linux daemon 上证明 execution lease 持有者运行期间，普通 Store reader 与竞争 daemon 都不会改写活动 `running` job；持有者退出后新 daemon 才以 `daemon_restarted` 恢复为 `interrupted` 并要求补偿检查。R-063/R-069 已完成真实内部 CA enrollment、handoff、owner/full、内部 leaf 热换与 internal→ACME 热切换，三次握手 SPKI 均按预期变化且 daemon PID 不变；ACME 切换后内部 CA 下载仍保留。该 e2e 同时发现并修复 `cloneTLSCertificate` 把默认 `nil` 签名算法错误克隆为空限制、导致所有真实 TLS 握手失败的问题，并补了真实 client/server 握手回归测试。
10. R-059 已在真实 Linux/Btrfs loop workspace 上证明 job 与 audit 使用独立策略并在 compaction 后实际缩小文件；过期 `Last-Event-ID` 返回可机读 `410 event_gap`。console store 位于 workspace 外，快照创建、快照恢复、备份创建和备份恢复都未包含或覆盖其 marker；测试后的 daemon、挂载、loop 设备、工作目录和上传目录均已清理。
11. R-102 已在真实 Linux daemon 与由当前 `modules/samba_dc/samba_dc` 构建的隔离 Samba 容器上双向验证凭据独立性：Samba 管理密码轮换后 `pwdLastSet` 变化且新密码可认证，本地 owner 会话不受影响；本地 owner 经生产凭据存储轮换后旧密码立即失效、新密码可登录，Samba 新密码继续有效；两组交叉凭据均被拒绝，明文未出现在 console store、容器配置或 daemon 日志中。测试使用独立 bridge、无宿主机端口，仅为 Samba SYSVOL ACL 初始化向容器授予临时 `CAP_SYS_ADMIN`，结束后 daemon、容器、测试镜像标签、工作目录和上传目录均已清理。
12. R-093 已由真实 Chromium 98 完成顶层 HTTPS form handoff：`r093.frp.hlong.wang:7795` 向 `anas.frp.hlong.wang:7795` 提交后，服务端消费 handoff、只创建一个绑定目标 origin 的 enrollment session，并以 `303` 回到无 query/fragment 的当前嵌入式控制台根路径；成功同时证明请求未携带 exchange 明确拒绝的 Cookie/Authorization/普通 CSRF。动作确认前的 fixture 到期后没有复用，而是在确认后重建全新 run 并成功。真实浏览器还发现 `Referrer-Policy: no-referrer` 会把跨源 POST 的 `Origin` 降为 `null`；生产与测试源均改为 `strict-origin` 后得到精确源 origin，且只暴露 origin、不暴露路径或查询。验证器同时确认 TLS resumption 已禁用、连接 SPKI 与绑定一致、handoff 明文未进入 console store/daemon log；服务器、本地代理与一次性文件均已清理。
13. M1C 配置页已接入完整 registry Module 选择、服务端规范 `document_path` 表单、敏感字段显式 set/unset、受保护 effect 只读、内存草稿、validate 变更预览和 ETag/首次创建条件保存。一次性本地 API fixture 的真实浏览器检查覆盖英文/中文、桌面/390px 移动布局、无横向溢出、编辑后旧预览与保存入口立即失效、保存后敏感输入清空以及 local/session storage 为空；检查发现 Vue reactive Proxy 不能直接 `structuredClone`，草稿复制已改为与 API transport 一致的 JSON round trip并补回归测试。R-120 随后在真实 Linux daemon 与 Google Chrome Beta 上完成：经 SOCKS5 代理访问 LAN wildcard 明文入口，兑换一次性 bootstrap token，选择无字段 `freeradius`、把 `global.timezone` 改为 `Asia/Singapore`，确认 validate 的两项变更预览、编辑后旧预览/保存入口立即失效、重新 validate 后保存；服务器验证 desired config 已更新而 deployments/data/userdata 摘要未变。真实 e2e 发现未声明但允许原样往返的 `env` 字符串因 YAML 引号风格变化被误判为篡改；比较已改为语义值等价，并保留“原值可往返、真实改值仍拒绝”的 bundled-registry 回归测试。
14. R-125 的持久任务抽屉已接入 jobs list/detail API：页面挂载即读取服务端 newest-first 分页，优先恢复未终态任务，否则展示最新终态；plan/apply 返回 job 后触发抽屉重读，详情、警告与补偿标记均由服务端响应重建，不使用 local/session storage。run13 先证明仅凭 HttpOnly 会话 Cookie 可在刷新后恢复任务；随后新增 `GET /api/v1/auth/session`，bootstrap/enrollment/full 都能在整页刷新后恢复认证能力并轮换 CSRF，因此配置写入口不再回退到 token/password 输入。部署面板与任务抽屉现统一使用 SSE 接收运行状态和事件，并用 job GET 做终态对账；断流可重连，服务端存续重认证不续期 idle TTL。run17 在真实 Linux daemon + Google Chrome Beta 中刷新后恢复 bootstrap 配置写会话，成功保存 `global.timezone=Asia/Singapore`，同时恢复既有任务并实时观察 apply 从 0%/99% 到 100%。
15. M1C 真实故障 e2e 使用同一脚本覆盖明文 bootstrap 与 HTTPS full：主 SPA JavaScript 被故障代理固定返回 503 时，独立 `/emergency` 仍渲染并通过健康检查；bootstrap/full 普通 apply 均在 99% 原样展示 `modules.m1c_fixture.config.marker (data_migrate; migrate-m1c-fixture-marker)`，输入 `APPLY` 并重新签发 confirmation/本地 step-up 后成功，SSE 完整显示 started、progress、warning、succeeded。固定为 `casbin/casdoor-all-in-one:3.164.0` 的真实 Casdoor 在运行和停止两种状态下，本地 owner 都能从无既有会话的 Chrome Beta 登录。run17 验证器对 R-103、R-104、R-113、R-131 全部返回 PASS；服务器 run/upload、端口、容器、本次新拉取镜像、本地构建制品与 SSH SOCKS 代理均已清理。

16. M1.5 已完成：`anasd` 的第二个 TLS-only listener 同时要求精确源 IP、客户端 CA 和叶 SPKI pin；Traefik 命名 `serversTransport` 使用稳定 Secret-Store 客户端身份，oauth2-proxy identity bridge 覆盖七个固定身份字段。完整级 proxy 与直连共用能力面，但本地登录、引导、注册和应急 UI 在 proxy 源返回 `404`。proxy session 把 issuer+subject 映射为 `owner`，审计保留语义角色与物理目录组；高危动作只用最近 5 分钟 OIDC `auth_time` 签发 assertion/subject/plan 绑定的一次性 proof，不接收 IdP 密码。run18 在真实 Linux 上验证无证书、错误 SPKI 与明文均无法进入 listener；陈旧认证返回 `428`，proof 跨 assertion 或消费后复用失败，普通 Apply 原样保留 guarded blocker，新的 proxy step-up + confirmation 成功完成 `allow_risky`。服务、容器、端口和上传目录均已清理。

17. M2 已完成：status 不再把 deployment 文件中的状态伪装成实时值，而是从当前 Compose 运行态投影聚合和逐 Module 健康；start/stop/restart/rollback 的 CLI 与 daemon 共用类型化 preview/execute 服务，HTTP、OpenAPI、幂等任务和锁内漂移复核完整接线。生命周期取消仅在安全阶段生效，Unix 外部命令以独立进程组 TERM→宽限期→KILL，随后在运行时锁内执行补偿检查并清除标记。Linux run19 用四个静态测试制品验证实际子进程组、executor cancel/compensation、冻结依赖展开与 HTTP 精确 chain/rollback 契约；Chrome Beta 再从生产构建 SPA 选择 `db`，看到服务端返回的 `db → app`，并成功提交精确有序确认链。远端一次性上传目录、本地二进制、浏览器夹具和结果文件均已清理。

下一步按依赖顺序实施：

1. 进入 M3，把 Module enable/disable、catalog、sync/update 接到类型化应用服务和任务 API，并补 UI 的版本、配置态、实时运行态、健康与入口地址。
2. 当前 audit 零值生产策略继续保留全部历史且不运行周期删除；将来若启用非零保留策略，须先把相同 Options 传给 daemon 与 CLI 的全部协作 Writer，并接入周期维护。

M2 已完成，当前进入 M3；已接受的 LAN 明文风险不改变写入原子性、审计和任务生命周期要求。

## 5. 实现检查表

本节是施工过程中**逐 PR 更新**的记录，不是完工后一次性补的总结。只记录 CI 无法自动判定的内容——CI 能门禁的事项写「哪个提交上绿」，不重复打勾。

需求条目见[要求文档](../requirements/web-api-admin-console.md) §10 需求矩阵；下表只记归属与进度，不复述要求原文。

### 5.1 需求归属与覆盖

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0 | R-001、R-002、R-003、R-006、R-007、R-009、R-046、R-143 | 已完成 |
| M0.5 / M0.6 | R-011—R-016、R-030 | 已完成 |
| M1A | R-060—R-062、R-064—R-068、R-071、R-080、R-081、R-086、R-090、R-108、R-119、R-140、R-157、R-163—R-166 | 已完成 |
| M1B | R-020—R-022、R-024—R-029、R-033、R-040—R-044、R-048—R-051、R-054—R-056、R-058、R-059、R-063、R-069、R-082—R-085、R-089、R-091—R-093、R-102、R-112、R-117—R-118、R-121、R-142、R-145—R-148、R-151、R-153、R-167—R-180 | 已完成 |
| M1C / R-120 | R-120 | 已完成 |
| M1C / R-125 | R-125 | 已完成 |
| M1C（其余） | R-004、R-010、R-070、R-087、R-088、R-103、R-104、R-113、R-126、R-128—R-131、R-156 | 已完成 |
| M1.5 | R-072、R-100、R-101、R-105—R-107、R-109—R-111、R-114、R-122 | 已完成 |
| M2 | R-023、R-031、R-034、R-057、R-124 | 已完成 |
| M3 | R-032 | 已完成 |
| M4 | R-047、R-115—R-116、R-123、R-127、R-132、R-133、R-144、R-149、R-150、R-152、R-155 | 未开始 |
| M5 | R-005、R-008、R-045、R-053、R-154、R-160—R-162 | 未开始 |

覆盖统计：149 项有效需求全部有归属，其中已完成 129 项；R-052、R-141、R-158、R-159 四项复合要求已废弃并由原子要求取代。**每个有效 ID 必须恰好归属一个里程碑**；新增或废弃需求时同步更新本表，否则门禁会拒绝。

### 5.2 CI 门禁

不逐条打勾，只记录最近一次验证过的提交或工作树基线。

| 门禁 | 命令 | 最近验证基线 |
| --- | --- | --- |
| 单元测试 | `go test ./...` | M3 工作树（基于 HEAD `c9e9a12`，2026-09-03）全仓通过 |
| 竞态 | `go test -race ./internal/application ./internal/api/httpapi ./internal/consolejobs ./internal/jobexecutor ./internal/processgroup ./internal/runner ./cmd/anasd` | M3 工作树（基于 HEAD `c9e9a12`，2026-09-03）通过；M1.5 的认证/Hook 竞态基线仍为 `2c03c6d` |
| 静态检查 | `go vet ./...` | M3 工作树（基于 HEAD `c9e9a12`，2026-09-03）通过 |
| 参数 inventory / effect | `go run ./cmd/gen-module-docs --check` | M3 工作树（基于 HEAD `c9e9a12`，2026-09-03）通过 |
| 前端构建 | `npm --prefix web run build` | M3 工作树（基于 HEAD `c9e9a12`，2026-09-03）通过：OpenAPI 生成、类型检查、13 个测试文件 43 个用例、主 SPA 与独立应急包构建全部通过 |
| 前端生产依赖审计 | `cd web && npm audit --omit=dev --audit-level=high --registry=https://registry.npmjs.org` | 工作树（HEAD `2c03c6d`，2026-09-01）通过：0 个已知漏洞；项目镜像不提供 npm audit API，因此审计显式使用官方 registry |
| 需求/计划一致性 | `npm run docs:test-requirements && npm run docs:check-requirements && npm run docs:test-status && npm run docs:check-requirement-status && npm run docs:check-plan-status` | M3 工作树（基于 HEAD `c9e9a12`，2026-09-03）通过：149 项有效要求全部有归属，M3 完成后 129 项已完成，另有 4 项已废弃 |
| 文档构建 | `npm run docs:build` | M3 工作树（基于 HEAD `c9e9a12`，2026-09-03）通过 |
| 静态交叉编译 | `CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o /private/tmp/anasd-m3-verify-linux-amd64 ./cmd/anasd`；arm64 同命令改 `GOARCH` 与输出名 | M3 工作树（基于 HEAD `c9e9a12`，2026-09-03）两架构静态 ELF 通过，临时二进制已清理 |

### 5.3 e2e 执行记录

CI 查不了这些——它们需要真实 Docker、Btrfs、域名或主机。**只写「跑过了」无法复核**，四列都要填。

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-044 | `test-env/scripts/server-console-config-manual-edit-e2e.sh` | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64、tmpfs；root；当前工作树 Linux/amd64 静态二进制 | 2026-08-31 | 通过：bootstrap session 先取得强 ETag，外部追加修改 `config.yml` 后 GET 与携带旧 ETag 的 PUT 均返回 `412 config_precondition_failed`；外部字节与 managed state 摘要保持不变；上传制品 SHA-256 校验通过并已清理远端临时目录 |
| R-049 | `test-env/scripts/server-console-execution-lease-e2e.sh` | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64、tmpfs；root；当前工作树 Linux/amd64 静态二进制 | 2026-08-31 | 通过：普通 Store reader 与竞争 daemon 均未改写租约持有者的活动 `running` job；竞争 daemon 退出码为 1；持有者退出后新 daemon 恢复为 `interrupted`，`needs_compensation_check=true`，审计原因为 `daemon_restarted`；上传制品 SHA-256 校验通过并已清理远端临时目录 |
| R-057 | `test-env/scripts/server-console-m2-lifecycle-e2e.sh` | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64；sudo；当前工作树交叉编译的 processgroup、jobexecutor、runner、httpapi Linux/amd64 测试二进制 | 2026-09-03 | 通过：真实 shell 父子进程组忽略 TERM 后进入宽限并被组 KILL；持久 `deployment.restart` 任务在已注册安全阶段接受 cancel，终态为 `canceled`，后代进程全部消失；executor 随后重新取得 workspace 运行时锁执行 snapshot/container/credential 补偿检查并清除 `needs_compensation_check`；上传 SHA-256 一致，一次性远端目录与本地制品已清理 |
| R-059 | `test-env/scripts/server-console-storage-isolation-e2e.sh` | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64、tmpfs backing + 512 MiB Btrfs loop workspace；root；当前工作树 Linux/amd64 静态二进制与专用 fixture | 2026-08-31 | 通过：job 容量 1、audit 容量 2 的独立策略分别保留 1/2 条，物理 compaction 将 journal 从 527021/525029 bytes 缩至 2219/920 bytes；过期游标返回 `410 event_gap`（`pruned_through=1`、`latest_id=2`）；snapshot create/restore 与 backup create/restore 均未包含或覆盖 workspace 外 console store；上传制品 SHA-256 校验通过，测试后无残留 daemon、挂载、loop 设备、工作目录或上传目录 |
| R-063 | `test-env/scripts/server-console-internal-ca-e2e.sh` | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64、tmpfs；root；OpenSSL 3.5.5；当前工作树 Linux/amd64 静态二进制 | 2026-08-31 | 通过：初始 internal leaf、热换 internal leaf、切换 ACME leaf 的下一次握手 SPKI 依次变化；`certificate_issuer=acme`，稳定内部 CA 仍可由 owner 下载；daemon PID 不变、无重启；e2e 发现并修复 cloned `tls.Certificate` 的 nil 签名算法语义缺陷，修复后二进制 SHA-256 为 `8c4d3ebf196ea0c10c5e8c45b16e7356a39a37a97fdbfaa03ce1ddacd030657f`，测试目录已清理 |
| R-069 | `test-env/scripts/server-console-internal-ca-e2e.sh` | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64、tmpfs；root；OpenSSL 3.5.5；当前工作树 Linux/amd64 静态二进制 | 2026-08-31 | 通过：`virtual_domain=true` 经真实内部 CA 进入 enrollment，CA 下载与配置 root 一致；handoff issuance/exchange 为 `201/303`，首个 owner 为 `201` 并进入 `full`，full 明文 system 为 `404`；handoff、CSRF 与 owner 密码原文均未进入 console store；随后切换 ACME 仍保持 full，上传制品已校验并清理 |
| R-072 | `test-env/scripts/server-console-trusted-proxy-e2e.sh` | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64、root；OpenSSL 3.5.5、Docker 29.7.2；当前工作树 Linux/amd64 静态二进制与专用 guarded-change Module fixture；loopback 直连 7796、TLS-only proxy 7797 | 2026-09-03 | 通过：无客户端证书、同 CA 签发但 SPKI 未 pin 的客户端证书、明文 HTTP 均不能进入 proxy listener；只有 CA 验证、叶 SPKI pin 与精确来源 `127.0.0.1` 同时满足才返回 `listener=trusted_proxy`；原 assertion 未进入 console store/daemon log，测试后 daemon、容器、端口、工作目录和上传目录均已清理 |
| R-093 | `test-env/scripts/server-console-browser-handoff-e2e.sh`；`test-env/helpers/console-browser-source` | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64、tmpfs；root；Chromium 98；当前工作树 Linux/amd64 静态二进制；SSH 转发与 Chromium 的 1080 代理，公开测试端口 7795、服务器 loopback 源后端 7796 | 2026-09-01 | 通过：动作确认前的 fixture 到期后未复用，确认后重建全新 run；真实顶层 HTTPS form POST 从 `r093.frp.hlong.wang:7795` 发送到 `anas.frp.hlong.wang:7795`，携精确非 `null` Origin 成功消费，只创建 1 个绑定目标 origin 的 enrollment session并 `303` 到无 query/fragment 的当前嵌入式控制台根路径；成功证明未携带 exchange 拒绝的 Cookie/Authorization/普通 CSRF。`no-referrer` 导致 Chromium 发 `Origin: null` 的问题已修为 `strict-origin`；连接 SPKI 匹配且 TLS resumption 禁用，handoff 明文未进入 console store/daemon log，远端 fixture、run/upload 目录、本地凭据、SSH/SOCKS 代理及 7795/7796/1080/17795/17796 监听均已清理 |
| R-101 | `test-env/scripts/server-console-trusted-proxy-e2e.sh` | 与 R-072 相同；同一 run18 同时保留直连恢复 listener 与受信 proxy listener | 2026-09-03 | 通过：携完整受信 proxy identity、session、Origin/CSRF 向 `/api/v1/auth/login` POST 仍返回 `404`；测试末尾直连 TLS `/api/v1/system` 保持 `200`，证明本地恢复入口未被代理能力取代 |
| R-102 | `test-env/scripts/server-console-credential-independence-e2e.sh` | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64、tmpfs；root；Docker 29.7.2；当前工作树 Linux/amd64 静态二进制与当前 Samba Docker context（项目 GHCR Ubuntu Resolute 基础镜像）；独立 bridge、无宿主机端口，容器仅为 SYSVOL ACL 初始化临时增加 `CAP_SYS_ADMIN` | 2026-08-31 | 通过：Samba 密码轮换使 `pwdLastSet` 变化且新密码认证成功，本地 owner 仍可登录；本地 owner 轮换后旧密码返回 `401`、新密码返回 `200`，Samba 新密码继续有效；两组交叉凭据均被拒绝，明文未进入 console store、Samba 容器配置或 daemon 日志；上传制品 SHA-256 校验通过，测试后无残留 daemon、容器、测试镜像标签、工作目录或上传目录 |
| R-103 | `test-env/scripts/server-console-m1c-browser-e2e.sh`；Google Chrome Beta | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64、root；Docker 29.7.2；当前工作树 Linux/amd64 静态二进制；`casbin/casdoor-all-in-one:3.164.0`（digest `sha256:6b3d1a3c1d812e53af4af620b5e9222a8496b0c11ca8b47d561e2922e7654de9`）；macOS 26.1、Google Chrome Beta 153.0.8010.12、ZeroOmega SOCKS5 `127.0.0.1:1080`；LAN wildcard `192.168.0.222:7793` HTTPS | 2026-09-02 | 通过：真实 Casdoor 运行时，从无 full 会话的 Chrome Beta 以本地 owner 直连登录成功并进入完整配置/部署/任务界面；本地登录入口未按 IAM 健康状态开关 |
| R-104 | `test-env/scripts/server-console-m1c-browser-e2e.sh`；Google Chrome Beta | 与 R-103 相同；先停止真实 Casdoor，再用全新隐身窗口访问 `192.168.0.222:7793` | 2026-09-02 | 通过：Casdoor 容器停止后，本地 owner 仍在无既有 Cookie 的 Chrome Beta 隐身窗口登录成功，完整配置、部署与任务 API 保持可用 |
| R-113 | `test-env/scripts/server-console-m1c-browser-e2e.sh`；`test-env/helpers/console-web-fault-proxy`；Google Chrome Beta | 与 R-103 相同；故障代理 `192.168.0.222:7792`，主 daemon `192.168.0.222:7793` | 2026-09-02 | 通过：故障代理对主 SPA `/assets/main.js` 返回 503，根页面无法渲染时独立 `/emergency` 仍正常显示，点击健康检查得到 `OK` |
| R-114 | `test-env/scripts/server-console-trusted-proxy-e2e.sh` | 与 R-072 相同；固定 issuer/subject/`platform_admin`/`Admins` identity，使用不同一次性 assertion 模拟 oauth2-proxy 已验证转发 | 2026-09-03 | 通过：`auth_time` 超过 5 分钟返回 `428 recent_auth_required`；新 proof 跨 assertion 用于 plan 返回 `409 step_up_invalid`，绑定 assertion 的 proof 可签发 confirmation，但第一次 Apply 消费后复用返回 `409 step_up_consumed`；全部 proxy step-up 请求不含密码，审计记录 `semantic_role=platform_admin` 与 `directory_group=Admins` |
| R-120 | `test-env/scripts/server-console-config-ui-e2e.sh`；Google Chrome Beta | `finance.hlong.wang`；Ubuntu 26.04、Linux 7.0.0-30-generic、x86_64、root；当前工作树 Linux/amd64 静态二进制；macOS 26.1、Google Chrome Beta 153.0.8010.12、ZeroOmega SOCKS5 `127.0.0.1:1080`；LAN wildcard `192.168.0.222:7794` 明文 bootstrap | 2026-09-01 | 通过：一次性 token 兑换成功；`global.timezone=Asia/Singapore` 与无字段 `freeradius` validate 预览精确显示两项变更；后续编辑立即移除旧预览和保存入口，重新 validate 后保存 2 项；服务端结构化验证 desired config 已更新，deployments/data/userdata 摘要未变且 daemon 保持运行；真实 e2e 发现并修复未声明 `env` 原值因 YAML 风格变化被误判为篡改的问题，远端 fixture/upload、本地临时 token 与 SSH SOCKS 代理均已清理 |
| R-122 | `test-env/scripts/server-console-trusted-proxy-e2e.sh` | 与 R-072 相同；专用 `data_migrate` guarded-change fixture，daemon job executor 执行真实 plan/apply | 2026-09-03 | 通过：普通 proxy Apply 失败且 job detail 的 `error.detail.blocked` 精确保留唯一原始项 `modules.proxy_fixture.config.marker (data_migrate; migrate-proxy-fixture-marker)`；重新取得来源感知 step-up 与服务端 confirmation 后，`allow_risky=true` job `job_72fa256ef5e5675d9d4381b30c002711` 成功，普通失败 job 为 `job_b2e3581bd3264191c9ad82372f637243` |
| R-124 | `test-env/scripts/server-console-m2-lifecycle-e2e.sh`；`test-env/helpers/console-m2-browser-fixture`；Google Chrome Beta | Linux 契约测试与 R-057 同一 run19；macOS 26.1、Google Chrome Beta 153.0.8010.12，生产构建 SPA 由 loopback `127.0.0.1:7794` 夹具提供，未改变既有 ZeroOmega 配置 | 2026-09-03 | 通过：Linux runner/HTTP 测试证明请求 `db` 由冻结 deployment 展开为有序 `db,app`，客户端交换顺序或部署/digest 漂移均拒绝；实际 Chrome Beta 页面显示 `db → app` 后才出现确认入口，提交体保留 requested `db` 并精确回送 confirmed `db,app`，创建 `deployment.restart` 作业；夹具、结果文件和监听已清理 |
| R-125 | `test-env/scripts/server-console-job-refresh-e2e.sh`；`test-env/scripts/server-console-config-ui-e2e.sh`；`test-env/scripts/server-console-m1c-browser-e2e.sh`；`test-env/helpers/console-jobs-fixture`；Google Chrome Beta | run13 明文 bootstrap 基线及 R-103 的 run17 明文/HTTPS 环境 | 2026-09-02 | 通过：run13 先证明整页刷新后任务由服务端恢复；run17 再证明 `GET /api/v1/auth/session` 恢复 bootstrap/full 认证并轮换 CSRF，刷新后配置写入口、任务列表与详情均恢复。bootstrap/full apply 通过 SSE 实时显示 0%/99%/100% 和 started/progress/warning/succeeded，并以 job GET 对账；刷新后成功保存 `global.timezone=Asia/Singapore`，不再回退到 token/password 输入 |
| R-131 | `test-env/scripts/server-console-m1c-browser-e2e.sh`；`test-env/helpers/console-jobs-fixture`；Google Chrome Beta | 与 R-103 相同；同一 run17 覆盖明文 bootstrap 与 HTTPS full | 2026-09-02 | 通过：bootstrap 普通 apply `job_cb55b784718b815664198038fe6bf933`、full 普通 apply `job_a2a94aac1e273b8089900beef1c8ec8d` 均在 99% 原样显示 `modules.m1c_fixture.config.marker (data_migrate; migrate-m1c-fixture-marker)`；输入 `APPLY` 并重新生成 confirmation/本地 step-up 后，bootstrap `job_d3899c7c246c1a93f9f6b2166595c524` 与 full `job_01e418223946c09a00ee78e0e8e58e17` 均成功到 100% |
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
| 安装文档写明「引导窗口不具备机密性」与两个访问地址（R-105） | 已完成（2026-09-03） |
| [服务配置参考](../../docs/reference/anasd-service-configuration.md)写明完整级 DNS、lego 不增加管理面 IP SAN（R-064） | 已完成（2026-08-29） |
| `anasd` root-owned 服务配置与 workspace 外 console store 运维文档（R-157） | 已完成（2026-08-29） |
| systemd root 身份/可写路径与安装升级卸载文档（R-154、R-162） | 未开始 |

## 6. 支线依赖与退出条件

| 项 | 影响范围 | 退出条件 |
| --- | --- | --- |
| Traefik 到代理身份监听器的不可旁路传输 | 仅 M1.5 | Unix socket、mTLS 或等价机制已有集成测试；仅有来源 IP allowlist 不算完成 |
| OIDC 近期认证证明的标准化转发字段 | 仅 M1.5 高危操作 | 能验证 issuer、subject、认证时间并绑定单次 StepUpProof；否则代理入口不开放高危操作 |

M4 首版范围已固定为不需要 `CAP_SYS_ADMIN` 的子集，不再等待 ambient capability 决策。直连 M1A—M4 不等待 Traefik/OIDC 支线；M5 与首版完成状态必须等待 M1.5 达到上述退出条件。
