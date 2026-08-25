# ANAS Web API 与管理前端实施计划

> 状态：**部分实施**。M0（只读骨架）、M0.5（配置元数据）、M0.6（约束语义）已落地；
> 认证、前端、任务系统、配置写入与安装发布集成尚未实现。
> 日期：2026-08-16，更新：2026-08-21

需求：[Web API 与管理前端要求](/requirements/web-api-admin-console)。该特性不单独建架构文档，
设计写在要求文档的 §3—§5 与 §9 决策记录里。

本文只记录**落地顺序、里程碑与剩余工作**。每个阶段“做对了”的判定标准不在这里：里程碑正文
用章节指针给出阅读入口，§5.1 用需求 ID 给出精确范围，两者都不复述要求原文，避免同一条约束
在两处各写一遍而后失步。ID 归属与 e2e 记录的一致性由 `npm run docs:check-requirements` 门禁。

估算按一名熟悉当前 Go 代码的开发者计算，重点是依赖顺序而非承诺日期，且不含要求文档
§7.4 所列特权模型决策的时间。

## 1. 给实施者

**当前入口：M1。** M0、M0.5、M0.6 已落地（见 §2 落地快照）；M4 在要求文档 §7.4 的特权能力集决策做出前不要开工，其余阻塞项见 §6。

**规范来源是要求文档的 §10 需求矩阵**，不是本文。本文只回答「先做什么」。每个里程碑的精确范围以 §5.1 的需求 ID 归属为准；里程碑正文里的章节指针只是阅读入口。

**每次改动后跑这一组**，它们是 CI 的门禁，本地先过再提：

```bash
go test ./... && go vet ./... && go run ./cmd/gen-module-docs --check
```

```bash
npm run docs:test-requirements && npm run docs:check-requirements && npm run docs:build
```

**改动纪律：**

- 新增或废弃需求时，同步更新要求文档的矩阵与本文 §5.1 的归属表——`docs:check-requirements` 会拦住不一致。
- 完成一条需求后在 §5.1 更新状态；跑过 e2e 后在 §5.3 填脚本、环境、日期、结果四列，只写「跑过了」等于没写。
- 改 `anas` 或 Module 的功能时，同一次改动更新受影响的文档（仓库 `AGENTS.md` 的既有要求）。
- 本文的进度描述必须与代码一致。宁可写「未开始」，不要写一个没验证过的「已完成」。


## 2. 当前落地快照（2026-08-19）

| 范围 | 已完成 | 尚未完成 |
| --- | --- | --- |
| M0 只读骨架 | `internal/deployment`、`internal/application`、`internal/api/httpapi`、`cmd/anasd`、workspace registry、OpenAPI；health/system/status/deployment list/detail 与 Module Command list/detail 共用类型化服务，不调用 CLI 子进程 | 认证、前端、SSE 任务、写操作、安装与 systemd 集成 |
| M0.5 元数据 | 17 个 global + 155 个 Module 参数全部显式声明类型；`unknown=0`；生成器、四份 Module 参数表和 release gate 共用 inventory | M3 配置 HTTP schema/表单投影 |
| M0.6 约束语义 | `input_required`、legacy `required`、`must_resolve` 三阶段语义；默认值存在性/来源；范围、长度、pattern、format；所有配置入口的统一规范化与校验 | 条件/跨字段规则继续由 resolver、plan 或 Hook 执行，不伪装成单字段 schema |

当前只读 `anasd` 只接受 registry 中的 workspace ID，HTTP DTO 不返回 workspace、deployment
或 Secret 的本机路径；daemon 启动和请求 Host 都限制为数值 loopback。生产目录
`cmd/anasd`、`internal/api/httpapi`、`internal/application`、`internal/deployment` 与
`internal/configschema` 不包含任何内置 Module 名称或 Module 分支。未来配置 HTTP API 必须继续消费
统一 schema，不能破坏这个边界。

配置实现的当前精确基线：

- 172 项参数的类型分布为 `string: 100`、`bool: 26`、`int: 26`、`enum: 20`；
- `input_required`/CLI `required` 只有 `global.base_domain`、`global.email` 2 项，最终
  `must_resolve` 共 26 项；有静态默认值或无条件来源的参数不是 caller-input required；
- 23 项已声明且有运行证据的单字段约束包括 2 个 DNS name、IANA timezone、language/locale、
  3 个 IPv4、4 个 `1..65535` 端口、`samba_dc.max_log_size >= 1`、非空白 group pattern、
  四个 DNS-label pattern、Runner image fingerprint 和 VersityGW credential/region 约束；schema 本身还
  支持 `min_length`/`max_length`；
- `config set`、import/reimport、`config plan`、deployment lock/plan/materialize 和 remote lock
  都使用同一声明、地址、类型和 constraints 校验；失败发生在持久化前并保持配置、摘要、
  Secret Store 与 lock 原子不变；
- 只有 Secret Store 的 `lifecycle_managed` 记录可以在私有视图中满足 caller input；所有
  kind 都只作为等值来源的脱敏 taint，不能经错误、list 或 plan 投影明文；
- calculate/render Hook 的 Env 与 Secret patch 先整包校验键、ownership、exports、碰撞和
  schema 再应用；Hook 只能刷新本 Module 已拥有的 `generated/module-hook` Secret。

该基线已经通过 `go test ./...`、关键包 `go test -race`、`go vet ./...`、参数 inventory/effect
脚本、`gen-module-docs --check`、VitePress 构建与版本测试、Module revision 检查，以及
Linux amd64/arm64 的静态 `anasd` 构建。后续里程碑不得把这些结果解释为 M1—M3 已完成。

## 3. 里程碑

### M0：服务层与契约骨架（3—5 天）— 部分实施

已完成共享应用层、独立 HTTP 适配器、OpenAPI 契约与只读 daemon 入口：`version`/`status`/`deployments list`/`deployments inspect` 以及 Module Command list/detail 只读用例已抽出，CLI 输出不变；`api/openapi.yaml`、`cmd/anasd` 与 health、system、workspace status、deployment list/detail、Module Command list/detail GET 路由就绪；workspace 只由 registry ID 选择；daemon 仅监听数值 loopback 且 handler 拒绝非 loopback/域名 Host。认证、前端、任务与任何写操作（包括 Module Command invoke）不在此状态内。

验收：要求文档 §3（架构与代码边界）、§4.1（通用约定）、§7.2（输入边界）；另加本阶段特有的两条——现有 Go 测试与 CLI contract 全绿，且这四条路径不接触全局 `os.Stdout`/`os.Stderr`（全仓 34 处引用的清理是后续持续工作，M0 不承诺）。

### M0.5：配置元数据回填（3—4 天）— 已实施

见 §2 落地快照。验收：要求文档 §2.2。

### M0.6：配置解析与约束语义（1—2 天）— 已实施

见 §2 落地快照。验收：要求文档 §2.2；另加本阶段固定的基线数字——`input_required` 固定 2 项、`must_resolve` 固定 26 项、`default_source` 分布固定为 static 131 / generated 10 / none 8 / runtime 4 / inherited 7 / host 3。

### M1：认证、前端壳与只读总览（5—7 天）

- 两级通道：LAN 明文 HTTP 引导、检测到证书后单向升级、bootstrap token、引导级端点白名单、单向棘轮；可选的 `anas console tls --self-signed`。
- 本地管理员初始化、登录、退出、会话与 CSRF；监听器的“是否受信代理”标志与 handler 权限声明。
- Vue/TypeScript/Vite 前端、OpenAPI 客户端、布局、错误处理、zh/en 映射表、独立应急 UI 包。
- 引导页、总览、Module 只读列表、部署历史、能力探测；前端嵌入 `anasd`，提供 systemd unit 与默认 localhost 配置。

验收：要求文档 §5.2（能力分级与棘轮）、§5.5（认证与角色）、§5.6（应急 UI 包）、§6.3（双语）、§7.1（网络与认证）；测试项见 §8 的「分级」「破玻璃」「代理身份头」「权限声明」四组。

### M2：任务系统与生命周期操作（7—12 天）

- 任务/事件/审计存储、每 workspace 串行队列、SSE 重放。
- `start`、`stop`、`restart`、`plan`、`apply`、`rollback` 抽到服务层。
- 运行时锁改为非阻塞取锁 + 退避重试 + 持有者可见。
- 外部命令改用 `exec.CommandContext` 并使用显式子进程环境；覆盖 31 处调用点、compose 封装与 hook 执行。这是“取消”能成立的唯一前置，也是本阶段区间偏大的原因。
- 前端任务中心、依赖 chain 预览、风险/失败展示。

验收：要求文档 §2.1（非阻塞锁）、§4.3（异步任务）、§4.4（任务持久化）、§7.3（子进程环境隔离）；测试项见 §8 的「并发」「崩溃恢复」「环境隔离」三组。

### M3：配置与 Module 管理（7—10 天）

- schema 驱动的配置 GET/validate/PUT，以 `managedConfigState.Digest` 为 ETag，原子写入、敏感值只写。
- Module 启用/停用与多字段配置的核心用例，不再生成临时 YAML 再调 CLI。
- Module catalog、版本、sync/update 进入任务系统。
- 前端完成草稿、预检、变更计划与 apply 串联。

验收：要求文档 §2.2（统一 schema 复用）、§4.1（`If-Match` 与 ETag）、§6.2（守卫字段与关键交互）。

### M4：快照、备份与账户（7—10 天）

- 迁移 snapshot、backup、local admin 用例，范围按要求文档 §7.4 的能力集裁剪。
- 高危操作二次认证、备份目标 allowlist、凭据 reveal 防缓存。
- 快照、备份、管理员、证书与访问页面。

验收：要求文档 §4.2（凭据轮换只随机）、§6.1（页面与内部 CA 下载）、§7.4（特权能力集）、§7.5（审计）。

### M5：加固与发布（7—10 天）

- 端到端测试覆盖登录、配置、apply、任务进度、失败、补偿、快照恢复；API 兼容性测试、前端可访问性检查、安全 headers、限流与模糊输入测试。
- **发布链改造**：`.github/workflows/anas-release.yml` 目前只有 `setup-go`，需新增 Node 构建阶段产出前端；现有归档是确定性的（`tar --sort=name --mtime=@0 --owner=0 --group=0 --numeric-owner`），Vite 产物必须可复现，否则同一 commit 两次构建产生不同 hash 文件名；`scripts/ci/build-anas-release.sh` 增加 `anasd` 目标（`anas-helper` 已在其中）；`install.sh` 新增 `anasd` 二进制、systemd unit、服务用户与管理端口的安装/升级/卸载路径——注意 helper 的 `setcap` 已因“升级替换文件会丢失 capability”单独处理过，`anasd` 会遇到同类问题。
- 使用真实 Docker/Btrfs test-env 做破坏性测试；普通单元测试不假装覆盖恢复正确性。

验收：要求文档 §8 全部测试项通过。

整体约 6—9 个开发周，不含特权模型决策与可能的 Traefik entrypoint 扩展。若首版只交付 M0—M2（含 M0.5/M0.6），约 3—4 周可得到“可登录、可看状态、可部署和启停、可追踪任务”的管理面。

## 4. 第一条实施切片

1. 抽取 `Version`、`Status`、`ListDeployments`、`InspectDeployment` 四个类型化用例。
2. 现有 CLI 通过这些用例输出原有 JSON，契约零变化。
3. 新增 `anasd`，实现 `/healthz`、`/api/v1/system`、`/api/v1/workspaces/{ws}/status`、部署列表/详情与 Module Command 列表/详情。
4. workspace 只从服务启动配置注册，API 不接受路径。
5. 暂不做前端、认证写操作与异步任务；开发环境仅监听 loopback。
6. 同一 PR 修改 [CLI 契约索引](/reference/contracts/)中“面向……将来的 web 服务”那句话（要求文档 §1.3）。

第二条 PR 再加入认证、Vue 壳与只读总览，先验证“共享服务层 + 两个适配器”，避免一开始同时调试部署逻辑、任务系统、认证与 UI。

## 5. 实现检查表

本节是施工过程中**逐 PR 更新**的记录，不是完工后一次性补的总结。只记录 CI 无法自动判定的内容——CI 能门禁的事项写「哪个提交上绿」，不重复打勾。

需求条目见[要求文档](/requirements/web-api-admin-console) §10 需求矩阵；下表只记归属与进度，不复述要求原文。

### 5.1 需求归属与覆盖

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0 | R-001、R-002、R-003、R-007、R-009、R-046、R-143 | 已完成 |
| M0.5 / M0.6 | R-027、R-028、R-029、R-030 | 已完成 |
| M1 | R-004、R-008、R-010、R-060—R-062、R-064—R-068、R-070、R-080—R-090、R-100—R-113、R-128、R-129、R-130、R-140—R-142 | 未开始 |
| M2 | R-020—R-022、R-024—R-026、R-040、R-041、R-048—R-052、R-124、R-125、R-145—R-148 | 未开始 |
| M3 | R-023、R-042、R-043、R-044、R-045、R-120、R-121、R-122、R-126 | 未开始 |
| M4 | R-047、R-063、R-069、R-123、R-127、R-144、R-149、R-150、R-152、R-153、R-155 | 未开始 |
| M5 | R-005、R-006、R-151、R-154、R-160、R-161、R-162 | 未开始 |

覆盖统计：100 项需求全部有归属，其中已完成 11 项。**每个 ID 必须恰好归属一个里程碑**；新增需求时同步补进本表，否则它不会被任何阶段验收。

### 5.2 CI 门禁

不逐条打勾，只记录最近一次全绿的提交。

| 门禁 | 命令 | 最近全绿提交 |
| --- | --- | --- |
| 单元测试 | `go test ./...` | 待填 |
| 竞态 | 关键包 `go test -race` | 待填 |
| 静态检查 | `go vet ./...` | 待填 |
| 参数 inventory / effect | `gen-module-docs --check` 与 inventory 脚本 | 待填 |
| 文档构建 | `npm run docs:build` | 待填 |
| 静态交叉编译 | `linux/amd64`、`linux/arm64` 的 `anasd` | 待填 |

### 5.3 e2e 执行记录

CI 查不了这些——它们需要真实 Docker、Btrfs、域名或主机。**只写「跑过了」无法复核**，四列都要填。

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-044 | 待补 | | | |
| R-049 | 待补 | | | |
| R-063 | 待补 | | | |
| R-069 | 待补 | | | |
| R-101 | 待补 | | | |
| R-102 | 待补 | | | |
| R-103 | 待补 | | | |
| R-104 | 待补 | | | |
| R-113 | 待补 | | | |
| R-120 | 待补 | | | |
| R-122 | 待补 | | | |
| R-124 | 待补 | | | |
| R-125 | 待补 | | | |
| R-155 | 待补 | | | |
| R-162 | 待补 | | | |

脚本放在 `test-env/scripts/`，命名沿用既有的 `server-<主题>-e2e.sh`。

### 5.4 文档同步

AGENTS.md 要求功能变更与文档在同一次改动中保持一致。以下是本特性已知需要同步的点。

| 项 | 状态 |
| --- | --- |
| [管理员账户体系](/architecture/admin-account-system)补充 `Admins` 扩大范围说明 | 已完成（2026-08-21） |
| [CLI 契约索引](/reference/contracts/)措辞修正（R-006） | 未开始，随第一条切片一起做 |
| 安装文档写明「引导窗口不具备机密性」与两个访问地址（R-105） | 未开始 |
| 安装文档写明完整级需要能解析 `anas.<base_domain>`（R-064） | 未开始 |
| `anasd` 与 systemd unit 的运维文档 | 未开始 |

## 6. 阻塞项

| 项 | 阻塞什么 |
| --- | --- |
| 要求文档 §7.4 的特权能力集决策（是否授予 ambient capability） | M4 |
| Traefik entrypoint 的 `__SERVERS_TRANSPORT` 扩展 | 经 Traefik 的访问路径（P2） |
