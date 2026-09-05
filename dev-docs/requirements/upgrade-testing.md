---
doc_type: requirement
status: current
created: 2026-09-03
updated: 2026-09-05
---

# 版本升级 E2E 测试要求

本文规定 ANAS Core、管理 Web 和每个内置 Module 的发布升级测试契约。实施顺序见
[版本升级 E2E 测试实施计划](../plans/upgrade-testing.md)。机器可读事实来源是
`test-env/upgrades/catalog.yml`；本文定义它必须证明什么，不复制具体版本清单。

## 1. 版本边界

Core 与 Web 以不可变 Git 发布 tag 为旧端、待发布工作树为新端。Module 以独立的
`version-rN` 发布身份为两端，并以最近一次成功 `image-release/*` 所含 manifest 为发布基线。
发布基线存在时，不能用“首次发布”跳过升级；没有历史发布的全新组件只能登记
`no_prior_release`，它进入第一次成功发布后，下一次变更就必须提供升级用例。

每个升级用例必须实际构建或取得旧端产物。只运行当前实现再替换旧 lock、旧版本号或 fixture
不能算升级 E2E；这类测试仍可作为渲染兼容单元测试，但报告和名称必须与真实升级测试区分。

## 2. 执行与可观察结果

Core 测试由旧 CLI 创建 workspace、lock 和不可变 deployment，再由新 CLI 读取并推进同一个
workspace。Module 测试必须在隔离 Docker daemon 上用旧二进制和旧 Module 源启动旧服务、写入
持久状态，再由新二进制和新 Module 源升级同一部署。只检查进程退出码或镜像 tag 不足以通过：
至少要观察配置、持久数据、服务身份或认证集成、健康状态与旧 deployment 可读性。

当 manifest 的 `upgrade.data_breaking` 声明允许回退时，Module 用例还必须完成“旧版 → 新版 →
旧 deployment → 新 deployment”的往返，并在每一阶段验证已写入状态。测试必须复用服务器隔离
门禁，且只清理本次明确的 workspace 和资源。

## 3. 发布门禁

普通 CI 校验 catalog 的完整性并运行无 Docker 的 Core 兼容 E2E。ANAS 发布根据最新 Core tag
要求精确的 Core/Web 边界；Module 镜像发布在自动计算 revision 后，对相对上次成功镜像发布发生
变化的每个 Module 要求精确的旧版到新版 transition。目录遗漏、版本漂移、用例端点不精确、脚本
缺失或不可执行都必须在发布前失败。

## 4. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `UPGRADE-R-001` | 必须有一份严格、机器可读且版本化的升级测试 catalog，统一登记 Core、Web、全部内置 Module、升级边界和执行 suite | 单元 + CI |
| `UPGRADE-R-002` | Core/Web transition 必须引用可解析的不可变 Git 基线；Module transition 必须使用精确 `version-rN` 端点，to 必须严格晚于 from | 单元 + CI |
| `UPGRADE-R-003` | `.github/modules.json` 中每个 Module 必须恰好出现一次，catalog 中也不得登记未注册 Module | 单元 + CI |
| `UPGRADE-R-004` | 无历史发布的组件只能以当前版本登记 `no_prior_release`；基线一旦已包含该组件，后续版本变化必须有精确 transition，不能继续按首次发布绕过 | 单元 + CI |
| `UPGRADE-R-005` | suite、transition 和 baseline ID 必须全局唯一；引用的 runner、config、seed、verify、report 必须位于仓库内且存在，脚本必须可执行 | 单元 + CI |
| `UPGRADE-R-006` | catalog 的 Module 当前版本必须与各 `module.yml` 顶层 `version` 和 `revision` 完全一致；嵌套 compatibility/version 字段不得误判为发布版本 | 单元 + CI |
| `UPGRADE-R-007` | Core E2E 必须从 Git 基线提取并构建真实旧 CLI，不能用当前 CLI 伪装旧版本 | e2e |
| `UPGRADE-R-008` | Core E2E 必须让旧 CLI 创建真实 workspace、lock 和不可变 deployment，再让当前 CLI 读取旧 deployment、更新 lock 并 render 新 deployment | e2e |
| `UPGRADE-R-009` | Core E2E 必须断言升级不改写用户管理配置，且旧 deployment 在新 Core 推进前后都仍可读取 | e2e |
| `UPGRADE-R-010` | 普通 CI 必须覆盖最低受支持版本和最新已发布 Core 到当前工作树；Core 发布门禁必须要求从当时最新 tag 出发的精确 transition | CI |
| `UPGRADE-R-011` | Web 有历史发布后，每次 Web 发布必须以旧发布的持久 console store 和浏览器状态启动当前 Web/anasd，验证状态迁移而非只做当前版本 smoke test | e2e |
| `UPGRADE-R-012` | Web 升级 E2E 必须在真实浏览器验证新静态资源加载、旧管理员重新登录、核心页面/API 可用及旧 Secret 不进入浏览器持久存储 | e2e |
| `UPGRADE-R-013` | Web 尚无历史发布时必须登记并执行首次发布浏览器基线；首次发布后 release gate 必须拒绝继续用 baseline 代替 transition | CI + e2e |
| `UPGRADE-R-014` | Module 升级 E2E 必须使用真实旧 ANAS 二进制、同源 `anas-helper`、旧 Module 目录和发布 registry 中的旧 `version-rN` 镜像启动旧服务，不得用已漂移的 Dockerfile 输入重建并冒充旧发布；再以当前二进制、同源 helper 和当前 Module 目录升级同一 workspace，缺少任一 helper 必须在当前镜像构建前失败 | e2e |
| `UPGRADE-R-015` | 相对最近成功 Module 发布发生 `version` 或 `revision` 变化的每个 Module，都必须存在端点完全匹配的 transition，缺一个就禁止构建或发布 | 单元 + CI |
| `UPGRADE-R-016` | 每个 Module transition 的 suite 必须实际包含该 Module；有持久数据的服务必须在旧端写入唯一标记或业务对象，并在升级后核对原值 | e2e |
| `UPGRADE-R-017` | Module suite 除数据连续性外，还必须验证容器/服务健康和适用的数据库、目录身份、OIDC/SAML、应用 API 或文件访问集成；只核对镜像 tag 不足以通过 | e2e |
| `UPGRADE-R-018` | `upgrade.data_breaking` 允许回退的 Module 必须完成旧版、新版、旧 deployment、新 deployment 往返，并在每阶段验证状态；拒绝回退的边界必须有负向用例 | e2e |
| `UPGRADE-R-019` | 服务器 Module E2E 必须先通过独立 Docker socket 与 data-root 双重门禁；runner 与 daemon 还必须位于同一个显式 ANAS 测试网络 namespace。workspace 必须在升级测试专属路径；清理必须先通过当前 CLI 删除该 workspace 内的只读 Btrfs 快照，再删除普通文件，且不得使用全局 prune 或通用生产前缀 | 单元 + e2e |
| `UPGRADE-R-020` | Module 发布流程必须在 revision 计算完成、任何镜像或包构建开始前执行 catalog 基线校验 | CI |
| `UPGRADE-R-021` | 真实升级报告必须记录 from/to 身份、suite、deployment 身份、配置/数据断言、往返结果、环境和清理状态；脚本存在不等于执行证据 | 契约 + e2e |
| `UPGRADE-R-022` | 旧的“当前版本启动后替换历史 lock”测试只能标记为 render/lock 兼容检查，不得被发布门禁或文档计为真实旧版升级 E2E | 静态 + 审阅 |
| `UPGRADE-R-023` | 每个 Module suite 的 config 必须显式选择 suite 声明覆盖的全部 Module，并同时通过精确旧版与当前版 CLI 的 `init`；只被单端接受的 fixture 不得进入发布测试 | 单元 + CI |
| `UPGRADE-R-024` | Module suite 不得声明未分配给该 suite 的 transition Module，避免依赖项或重复配置被误报为该 suite 的升级覆盖 | 单元 + CI |
| `UPGRADE-R-025` | 使用已发布旧镜像的 Module suite config 不得启用 `global.chinese_build_speedup` 或设置顶层 `env` 构建输入；否则旧 CLI 会要求重建并破坏旧发布产物身份，catalog 必须静态拒绝 | 单元 + CI |
| `UPGRADE-R-026` | Module suite 写入旧端种子前及每次升级、回退、再升级验证时，必须等待至少覆盖仓库内最大 healthcheck `start_period` 与 retry 窗口，并在截止时拒绝重启中、未健康、仍在启动或异常退出的长期服务；只有名称明确以 `_init` 结尾、退出码为 0 且 restart policy 为 `no` 的一次性容器可视为成功完成 | 单元 + e2e |
| `UPGRADE-R-027` | 历史发布因已知环境拓扑或资源时序缺陷不能启动时，只允许按精确 Module `version-rN` 启用幂等且可撤销的测试环境兼容准备或有界重试；重试只接受结构化的预期失败码并复用同一旧镜像与持久数据。不得改写或重建旧产物，进入当前版本前必须撤销，回退阶段可再次启用，最终清理必须恢复原网络状态并留下脱敏证据 | 单元 + e2e |
| `UPGRADE-R-028` | 每个 Module suite 的新端 Module 根必须以精确旧发布树为基底，只用当前树覆盖 catalog 分配给该 suite 的 Module；targets inventory 必须与 catalog 完全一致，未分配的依赖 Module 必须保持旧发布版本，避免跨 suite 升级和错误归因 | 单元 + CI + e2e |
| `UPGRADE-R-029` | 测试网络不能稳定访问当前 Dockerfile 的上游 registry 时，只允许通过专用、非 Secret 且格式受限的测试环境变量为“当前目标 Module 构建”覆盖 registry host；该值不得进入 suite config、旧端启动、运行时 deployment 或其他 Module 构建，runner 必须记录覆盖范围和值以便审计 | 静态 + e2e |
| `UPGRADE-R-030` | Module suite 可以使用 Docker client 构建代理，但在创建容器前必须验证其 `noProxy` 覆盖 localhost、loopback 与全部 RFC1918 CIDR；否则代理设置会被注入旧/新运行容器并把 Docker backend 流量错误送往出站代理，runner 必须拒绝执行且不得靠接受代理错误响应绕过深度探针 | 单元 + e2e |

## 5. 排除项

本机制不要求不存在历史产物的新组件凭空制造升级；它要求明确登记首次发布，并保证下一次变更自动
转为升级义务。无 Docker 的 Core 用例不替代真实服务数据测试，Module suite 也不替代 Web 的浏览器
迁移测试。远程执行、报告收集与服务器权限仍遵守文档驱动测试自动化的隔离与脱敏要求。
