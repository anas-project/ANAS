---
doc_type: plan
status: done
created: 2026-10-23
updated: 2026-10-23
---

# anasd 服务授权与门户后台凭据实施计划

依据：[需求矩阵](../../requirements/service-api.md)、[架构和配置](../../../docs/architecture/service-api.md)。
用户已确认高复杂度方向及生命周期，当前实施无需再次确认同一设计。

## 里程碑与需求归属

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：服务身份、宿主授权与自动交付 | R-001—R-009 | 已完成；单元与 HTTP 契约通过 |
| M2：当前后台凭据与服务 API | R-010、R-011、R-015、R-016 | 已完成；实际服务授权/HTTP/Runner 集成通过 |
| M3：门户近期认证与复制 | R-012—R-014、R-017、R-018 | 已完成；Go 与浏览器契约通过 |
| M4：后续 Module 接入标准 | R-019 | 已完成；中英文开发标准及升级检查表明确强制接入与不适用边界 |
| M5：finance 10000 部署验证 | R-020 | 已完成；finance 正式部署、13 项服务 API 与 28 项真实浏览器检查通过，临时账号已清理 |

## 执行记录

2026-10-23 用户追加要求：部署到 finance.hlong.wang:10000，并将此能力作为后续 Module
的接入义务。对应新增 R-019/R-020；原有本机验证历史保持，真实部署结果在本计划追加。

基线：`058d5c2ce872` 加当前工作树；本任务开始前存在其他任务修改，保持原样。
2026-10-23 首次本机验证完成三个里程碑的代码及矩阵内自动验证。当时尚未通过完整 Linux
回归或真实主机交付，计划标为 `partial`；后续用户授权 finance 部署后改为 `implementing`。
以下保留本机验证历史，后续执行结果另列；不归档，不将单元结果表述为真实后台登录验收。

本轮修复独立审查发现的三处问题：严格 `UMask=0077` 下显式设置客户端目录遍历权限；
Compose 使用 `create_host_path: false` 避免 Docker 创建错误权限的交付目录；凭据查询改用
不触发恢复的共享锁，配置或凭据事务未完成时拒绝读取，不运行 Hook 或迁移配置。
旧门户镜像的共享源码声明还漏列 Dockerfile 已复制的依赖，已按实际 COPY 清单补齐
`.github/images.json` 和 `.github/modules.json`，共享构建静态检查通过；没有引入新依赖。

### 逐需求结果

R-001—R-019 的下表结论按矩阵约定的单元、契约及源码审阅判断；R-020 单列真实主机验收，详细结果见下方部署记录。

| 需求 ID | 自动验证结论与入口 |
| --- | --- |
| `SVCAPI-R-001` | 通过；`serviceauth` 独立客户端身份及 anasd 启动签发测试 |
| `SVCAPI-R-002` | 通过；`TestGrantDimensionsCannotBeCombined`，HTTP 跨资源/工作区拒绝 |
| `SVCAPI-R-003` | 通过；`TestServiceTokenDoesNotAuthorizeOwnerRoutes` |
| `SVCAPI-R-004` | 通过；配置位置/符号链接隔离测试；`TestApplicationCredentialAPIIntegratesCurrentValuesAndHostRevocation` 验证恢复工作区不能恢复宿主授权 |
| `SVCAPI-R-005` | 通过；生命周期测试不启动门户，维护仍续期 |
| `SVCAPI-R-006` | 通过；重启、自然过期及文件丢失重新签发；旧值不可复活 |
| `SVCAPI-R-007` | 通过；未维护前缩权/停用立即拒绝，维护删除文件，重启不恢复 |
| `SVCAPI-R-008` | 通过；文件/目录权限、符号链接拒绝、严格 umask 回归；Compose 固定只读目录挂载审阅 |
| `SVCAPI-R-009` | 通过；HTTP `TestServiceCredentialRouteHiddenOutsideFullDirectTLS` |
| `SVCAPI-R-010` | 通过；Runner 拒绝禁用、隐藏、IAM、用户、外部、未声明及跨 Module 来源；声明校验和显式冻结 |
| `SVCAPI-R-011` | 通过；Secret Store 变更及 active deployment 切换测试，未 apply 的 desired 值不生效；未完成事务拒绝且无恢复副作用 |
| `SVCAPI-R-012` | 通过；门户获取前/返回前再次检查当前身份、GUID、可登录性、管理员角色与 IAM 可见性 |
| `SVCAPI-R-013` | 通过；5 分钟认证复用与过期测试，浏览器验证本人密码后重试 |
| `SVCAPI-R-014` | 通过；生产 UI 的复制、关闭/导航/退出及晚响应清理浏览器测试 |
| `SVCAPI-R-015` | 通过；目录只含可获取标志；服务令牌不出门户 server；响应、审计、错误无令牌/密码泄漏测试与源码审阅 |
| `SVCAPI-R-016` | 通过；HTTP 审计成功后返回凭据，审计失败拒绝；actor 仅为审计上下文 |
| `SVCAPI-R-017` | 通过；门户 CSRF、Origin、JSON 请求边界测试 |
| `SVCAPI-R-018` | 通过；令牌延迟发布、原子替换/401 一次新值重试、后端失败及浏览器不可用提示测试 |
| `SVCAPI-R-019` | 通过；中英文 Module 开发规范及升级检查表要求适用后台接入，说明来源、宿主授权、本人认证、当前值、真实登录验收与不适用边界 |
| `SVCAPI-R-020` | 通过；finance 正式部署、13 项服务 API/令牌恢复及 28 项真实浏览器检查全部通过，详见后续实机记录 |

### 被测基线和证据

- 源码清单：172 个相关实现、测试、UI、Manifest、OpenAPI、构建声明文件，按相对路径排序，
  每行 `SHA256  path`；清单 SHA-256 为
  `6da04dcdfb8aa4d068fc25c766800ad6e8bf44475655b8beef30b8a8b60994f0`。
- `linux/amd64`、`CGO_ENABLED=0` 构建通过；anasd SHA-256 为
  `5cd4010edc3141c0837c59664494c56d2138cedda0be8a9744a8375f2a73278f`，门户为
  `661822f98966a40d4de35966a5f4a2c5926aa6645fb300a0cdfb3232dc3542ad`。
  二进制只在 `/private/tmp/anas-service-api-build/`，未部署。
- Root 运行日志和源码清单位于 Git 忽略的 `test-env/reports/service-api/`；门户 Go/57 项浏览器
  回归结果另保留在本轮工具执行记录中。没有使用真实密码，没有生成真实主机截图。
  这些均非共享持久证据归档；上面的基线、入口与结论保存在本计划。

## CI 门禁

通过：

- `go test -race ./internal/serviceauth ./internal/consoleconfig ./internal/api/httpapi ./cmd/anasd ./internal/appcatalog ./internal/deployment`。
- `go test -race ./internal/runner -run 'Test(ApplicationCredential|Catalog|BundledApplications|BuiltinInventoryMatchesApprovedSurface)' -count=1`，以及后补的未完成轮换拒绝测试。
- `go test ./modules/user_portal/portal ./modules/user_portal/hook`；门户凭据 focused 测试复核通过。
- `node --test test-env/playwright/user-portal-credentials.test.mjs test-env/playwright/user-portal-ui.test.mjs test-env/playwright/user-portal-experience.test.mjs`：57/57，通过本机 Chrome；默认 Playwright 浏览器未安装，使用 `PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH` 指向已安装 Chrome。
- 涉及包的 `go vet`、Web `check:api` / `typecheck`、`src/api/problems.test.ts` 4/4。
- Module 文档生成及 `--check`、`check-shared-build`、`docs:check-requirements`（含测试用例摘要）、`docs:check-status`。

完整 `go test ./internal/appcatalog ./internal/deployment ./internal/runner` 未通过：初次有
inventory golden 未更新（生成后已单独复验通过），另外三个 Runner 顶层测试在 macOS 返回
`managed temporary storage requires a local Linux filesystem`。已单独重跑确认同一限制：
`TestCLIApplyRetriesFailedFrozenMaintenanceCandidateAfterHookFailure`、
`TestCLIApplyCannotReaddHistoricalPostgresOverRetainedData`、
`TestCLIApplyAllowsFirstPostgresWithNoPriorPostgresRecords`。未修改或跳过这些测试来宣称通过。
本机 Docker daemon 未运行，未用容器冒充已完成的 Linux 验证。

## 文档同步

- 服务配置、授权、令牌启动恢复和门户接线写入架构页及英文摘要。
- Module 行为说明写入各 Module 源技术文档的中英文页面；生成镜像使用生成器更新。
- 此前[方案调查](../../reviews/2026-10-23-portal-backend-credentials.md)保留源调查边界，实施结果只在本计划记录。

## finance 部署记录

### finance 部署准备（2026-10-23）

- 用户指定入口 `finance.hlong.wang:10000`；SSH 使用默认 22 端口。目标工作区
  `/home/whl/anas-finance`，活动部署 `20261021T212121Z-57b67b84`。
- 原门户镜像 `sha256:3a94b649760ced7af3f448ea99622942ac8c73c00858799af72429e2b8f82745`。
  正式切换前必须再次检查活动部署与镜像未发生变化，保留旧镜像及全部既有恢复点。
- 主机既有 `anasd.service` 属于其他工作区测试，保持原状。待部署配置使用独立
  `anasd-finance.service`、18446 端口和独立 console store；复用 finance 现有证书，
  仅允许本机及门户所在 Docker 网段连接。文件仍在暂存目录，未安装或启动。
- 门户待接线参数：`https://portal.finance.hlong.wang:18446`、工作区 `finance`、
  `/var/lib/anas/finance-service-clients/user-portal`。UID/GID 已从运行进程核实为 65532。
  宿主授权只包含 `traefik-admin` 与 `collabora-admin`；主机上的外部 DDNS-Go 不属于该工作区。
- 暂存根目录 `/home/whl/anas-service-api-20261023`；源码为 `058d5c2ce872` 对应完整提交基线
  加本任务 182 个文件的显式覆盖清单，排除本地其他未提交任务。源码归档、清单、待部署
  systemd/YAML、验证脚本均已上传，独立源码执行原生 Linux 验证。
- 基线源码归档 SHA-256：`5cce31b31ae9d87e939d8822a7d5b213d19c5d12e32c4776cf3f3e5c78747684`；
  本任务覆盖归档 SHA-256：`0d9cf014f90f55541fa58bcd4ec8a96e62f0a6b11d0ae6868c89fa8c97f815d6`。
  `source-manifest.json` 列出逐文件摘要；生成 inventory 后为暂存目录补建独立 Git 元数据，
  只供依赖 `git ls-files` 的打包测试使用，不是对用户仓库的提交。
- Linux 原生九个相关包完整测试已通过：`serviceauth`、`consoleconfig`、`httpapi`、
  `cmd/anasd`、`appcatalog`、`deployment`、`runner`、门户 server 与 Hook。
  初次执行仅 `TestRemoteModuleCLIEndToEndUpdateAndDigestOnlySync` 因归档缺少 Git 索引
  返回 exit 128；补齐测试元数据后整组重跑通过，没有修改或跳过测试。
  上述三个 macOS 文件系统限制用例在该轮 Linux 回归中通过。
  日志分别保留于暂存根目录 `private/native-go-tests.log` 和 `private/native-go-tests-retry.log`。
- 本轮中英文开发标准、升级检查表、需求/计划索引校验及完整文档站点构建均通过。
  本机日志位于 `test-env/reports/service-api/finance/`；原始日志保持 Git 忽略，实机业务验收仍未执行。
- Linux 部署制品均已生成，SHA-256：
  - CLI：`a38bebb87d533bd4d140077499cad774217ddd0066ce744045c59f53b0202e1c`。
  - anasd：`a6ffaf50c245148a0e86accb305ff83b862d4b6a6bd84541d01651c86f879493`。
  - 门户：`7b700b6dfa29da5ea34bb0ed9128f5092cf3b1b9e1d7ab8e76ab4ade80f2ae86`。
- 待部署门户镜像：`sha256:6300c6ad1b0fafa7d4c907bc7454790d0f693559d55534a45de85f8224a5512b`；
  在原门户镜像上仅替换已验证二进制构建，并非完整正式 Dockerfile 重建。
  暂存来源保留 finance 已固定的 Samba/Nextcloud 镜像；除共享目录消费者 Casdoor 与门户外，
  对照源码相同的 Module 复用当前活动部署的冻结 Hook，避免无关制品变更。
  11 个部署覆盖项及摘要记录在 `evidence/deployment-source-overrides.json`。
- `anas console status` 验证暂存的 root 所有配置通过；`systemd-analyze verify` 对指向暂存
  二进制/配置的单元语法校验通过。尚未安装服务或 owner enrollment，不能据此宣称运行验收。
  准备结束再次确认活动部署及门户容器 ID 未变化，根盘可用 7,374,635,008 字节，约 6.9 GiB。
- 准备阶段的测试日志与制品摘要已取回
  `test-env/reports/service-api/finance/native-preparation-evidence.tar.gz`，SHA-256 为
  `c9eab982f3153f9fbf2de5b46ac62e64adbbddb4138ff4010edc32322fdeca53`。
  原始证据仍分别保存在本机忽略目录和目标主机独立暂存目录，尚非共享持久归档。
- 根盘 Btrfs 预检可用约 7.0 GiB，低于 Collabora `min_free_bytes=8589934592`；
  本轮完整恢复快照还需额外余量。用户选择自行扩容或腾出至少 5 GiB，要求继续准备并等待。
  未删除数据、快照、镜像或共享缓存；未修改 finance 配置、活动部署和运行容器。
- 预检时一条配置摘要误包含既有门户绑定凭据；已向用户说明并改用字段白名单。
  文档和验收报告不得保存这些值；后续应安排绑定凭据轮换，当前未擅自修改绑定账号。

恢复路径：配置修改前保存 root 私有副本；正常 CLI 创建并验证包含用户数据的完整恢复点，
再检查剩余空间高于 Collabora 门槛后 apply。若部署验证失败，按 CLI 的部署回退/快照恢复
流程恢复旧部署，不直接修改 active state 或 Secret Store。独立服务接线可随门户旧配置
回退撤销；不能用跳过快照或降低空间门槛完成本次部署。

准备阶段待办（由下方恢复执行记录接续）：等待用户空间确认，再完成 anasd 首次 owner enrollment、
令牌交付、门户接线、快照、正式 apply 和 `SVCAPI-R-020` 实机验收。真实 UID 65532 文件读取、
Docker 只读挂载、服务重启恢复、本人密码验证、分别复制以及目标后台登录均仍待实测。
准备阶段未提交 Git，尚未满足归档条件。


### 恢复执行（2026-10-23）

- 用户要求继续；再次实测根盘可用 67,724,386,304 字节，约 63 GiB，空间阻塞解除。
- 已安装并启用独立 `anasd-finance.service`，完成 owner enrollment，状态为 `full`；
  原 `anasd.service` 保持不变。专用令牌交付为 UID/GID 65532、0400，门户暂未切换。
  owner 初始密码仅保存在宿主 root 可读的 `/etc/anas/finance-owner-password`，不写入证据。
- 实际证书链包含证书块之间的空行，旧 TLS 解析器错误拒绝合法 PEM。已修复为仅允许
  块外 ASCII 空白，仍拒绝其他杂项数据、非法块和原有链校验错误；补充回归测试与中英文
  配置文档。本机及 Linux 的 `internal/consoletls`、`cmd/anasd` 测试通过。
  修复后的 anasd SHA-256：`80a2e2f0a9ff024e353b64f8a976699218ea78e86ffe17c493e3f8360265dd1d`。
- 首次生成候选 `20261022T194252Z-755dc3c1` 后严格检查发现暂存文件含另一任务的 LLNG
  适配改动；未切换。独立暂存目录改回基线 Casdoor 路径，仅叠加本任务 service API
  字段与调用；本地其他任务修改保持原样。重新运行门户 server/Hook 测试通过，再构建镜像。
  其余模块的额外差异为文档、冻结部署路径与删除 `.DS_Store`，运行配置逐项核对。
- 修正范围后的门户二进制 SHA-256：
  `04482c20203ea17a8c2e3b44204905a14edbc901b540102ce9f407f7826a5b7e`；镜像：
  `sha256:7112512fe82da42362e1cbd207e12b1442f40fe724dc7526351d169dce8d9125`。
  初始制品摘要保留作历史，后续实机以修正制品为准；范围修正摘要在远程
  `evidence/scope-correction.json`，测试日志在 `private/scoped-portal-tests.log`。

- 正式切换前进一步收窄部署：仅在独立暂存源保留 Immich、Nextcloud、Postgres 的原冻结
  技术文档及 Samba 的原冻结元数据，避免文档/元数据差异触发无关模块重启。
  本地仓库文件未回退；7 个覆盖项及摘要记录在 `evidence/runtime-scope-overrides.json`。
  恢复快照完成后重新生成候选并检查最终运行变更范围。

- 完整恢复快照 `20261022T195239Z-4a2beba2` 已创建并通过 `snapshot verify`，包含用户数据，
  原有 12 个恢复点全部保留。快照后可用 63,061,790,720 字节；原服务已恢复运行。
- 最终候选 `20261022T201800Z-ee65453f` 的严格文件/字段检查通过，实际变更模块仅为
  `casdoor`、`collabora`、`traefik`、`user_portal`；其他模块沿用原冻结运行制品。
  门户镜像仍为上面的 `7112512f…`，开始按此候选执行正式 apply。

- 正式 apply 返回 0，活动部署已切换为 `20261022T201800Z-ee65453f`，runtime 为 `running`；
  切换前记录的其他项目容器 ID/状态全部保持一致，剩余空间约 58.7 GiB。
  接下来执行真实账号、复制、后台认证及令牌恢复验收；尚不将 R-020 标为通过。

- 服务端实机 13 项检查全部通过：真实 CA/主机名 TLS 校验、两个后台当前凭据读取、
  无令牌/跨资源/跨工作区/控制台接口拒绝、令牌 UID/GID 65532 与 0400 权限、只读挂载、
  超过 15 分钟仍有效、anasd 重启使旧值失效、交付文件丢失自动补发、普通日志无令牌/密码。
  门户容器在令牌恢复测试中未重启；结果在 `evidence/service-acceptance.json`。
- 浏览器首轮停在临时账号登录，后续原账号重试通过；首次失败原因未单独确认，保留记录。
  第二轮通过普通用户拒绝、管理员识别、应用范围、CSRF、分别复制、清理与浏览器存储检查，
  后台认证请求因测试工具未采用 Chrome 的本机域名映射未完成。调整验收请求的本机解析、
  使用实际 CA 和主机名校验后，Traefik 匿名 401、凭据认证 200 已通过。未修改产品认证逻辑。
  前两轮失败报告均保留，当前第三轮继续验证真实五分钟窗口和 Collabora。

- 第三轮真实浏览器 28 项检查全部通过：普通用户看不到复制入口且直接请求被拒绝；
  管理员只看到两个已授权后台；CSRF/IAM 边界正确；用户名和密码分别写入真实剪贴板；
  关闭后清理 DOM，浏览器存储没有凭据。实际等待超过 305 秒后返回近期认证失效，
  错误本人密码被拒绝，正确本人密码通过目录校验，5 分钟内再次获取无需重复验证。
- Traefik `/api/overview` 与 Collabora 管理页面均验证匿名 401、复制出的凭据认证 200；
  后台请求使用实际 CA 与主机名校验，保留正确 Host/SNI 并连接本机入口。
  anasd 重启及文件补发后，同一门户容器继续成功获取和复制，未重新部署门户。
- 浏览器页面驱动使用 `ignoreHTTPSErrors`；服务 API 和两个后台的独立请求均启用真实 TLS
  证书链与主机名校验，因此不把浏览器驱动本身表述为浏览器信任链验收。
  浏览器结果在 `evidence/browser.json`，失败重试在相邻 `browser.attempt-*.json`。


## 验收结论与后续事项

- `SVCAPI-R-001`—`SVCAPI-R-020` 在确认范围内全部通过，五个里程碑完成。功能已部署到
  `https://portal.finance.hlong.wang:10000`，当前生效 `20261022T201800Z-ee65453f`。
- 最终健康核查覆盖 18 个 finance 容器，运行服务健康，初始化容器按预期正常退出；
  其他项目容器 ID/状态仍与切换前一致，剩余空间约 58.7 GiB。
- 已按 GUID 删除本任务的 2 个目录账号与专属 OU；IAM 的 2 条记录已禁用并软删除，
  活跃测试账号为 0。临时密码文件已删除；结果在 `evidence/cleanup.json`。
- 最终部署源码清单覆盖 202 个文件（含冻结 Hook/范围覆盖），SHA-256：
  `afef5b1e3d58f2fc14323b4ce61665b53deb121087127c035b5b9cddfb3ab2cf`。
  门户、anasd 的最终二进制和镜像摘要见上文，详细记录在 `evidence/final-health.json`。
- 无凭据的实机证据归档保存在主机
  `/home/whl/anas-service-api-20261023/final-evidence.tar.gz`，本地副本为
  `test-env/reports/service-api/finance/final-evidence.tar.gz`；SHA-256：
  `5bd5f172f20648a290d68dbd6e456a8b774cb641785bc3180b6f1cbcb2425e4c`，40,116 字节。
  包含最终结果、失败重试、源码/验收脚本摘要与脚本；不含真实密码、服务令牌或私有配置。
  本机目录受 Git 忽略，主机目录仍是独立部署暂存目录，尚未另存共享持久归档。
- 中英文 Module 接入规范和升级检查表已要求适用本地后台接入共同机制；不适用的条目必须
  明确说明。完整文档站点构建通过，收尾重新生成并检查需求、计划索引。
- 没有功能验收阻塞；计划按文档门禁要求移入归档目录，Git 收录范围及提交前验证见下文。前期误输出的既有门户绑定凭据
  仍需另行安排轮换，当前未擅自修改这些绑定账号。


### 收尾门禁限制

归档后的完整文档站点构建、`docs:check-requirement-status`、`docs:check-plan-status`、
`docs:check-status` 和需求归属覆盖均通过；需求及计划索引已重新生成。
最后两次 `npm run docs:check-requirements` 的测试用例目录子检查未通过：
`test-env/cases/llng-production-readiness/cases.yml` 的 `LLNG-READY-T-001`
实现摘要过期（现登记 `1d824ee4…`，当前计算 `9ed9bedd…`）。本轮前期该全仓门禁通过，
收尾时工作树已有另一任务新增的 LLNG theme 等修改；这些修改未进入 finance 部署源。
保留其他任务修改，没有替其更新摘要来宣称全仓通过。当前全仓门禁因此仍有这一项失败；
本功能的独立源码、约定需求和真实主机验收结果如上。完整输出保存在
`test-env/reports/service-api/finance/requirements-final-current.log`。


### Git 提交范围与验证（2026-10-23）

用户要求提交后，以 `058d5c2ce87253897e1619b762f42cef9c3e71ee` 为基线建立隔离副本，
仅收录本任务服务授权、后台凭据复制、TLS PEM 空白兼容修复、Module 接入规范及本计划。
LLNG 账号/Passkey、AI Agent、Incus、邮件调研等共享工作树修改不在本次提交内；
门户混合文件按本任务范围提取，生成文档按隔离源码重新生成，没有更新其他任务的用例摘要。

提交前该隔离副本的验证结果：

- 九个相关 Go 包完整测试及 Runner 凭据/目录/inventory 定向测试通过，相关包 `go vet` 通过。
- 三组门户浏览器回归 57/57 通过；前端 API 生成一致性、类型检查及错误映射测试 4/4 通过。
- 需求覆盖、用例目录、需求/计划索引、文档状态、Module/Contract 文档和共享构建静态检查全部通过。
- 完整文档站点构建通过，仅有打包体积提示；没有将本机测试替代上文的 finance 实机验收。

上述结果对应本次提交的独立范围，未宣称包含其他任务修改的共享工作树全仓门禁已通过。
浏览器与站点构建原始日志保存在 Git 忽略的
`test-env/reports/service-api/finance/git-commit-browser.log` 和 `git-commit-docs.log`。
