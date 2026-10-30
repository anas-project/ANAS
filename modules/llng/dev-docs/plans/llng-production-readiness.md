---
doc_type: plan
status: implementing
created: 2026-10-23
updated: 2026-10-26
---

# LLNG 上线实施计划

依据：[需求矩阵](../requirements/llng-production-readiness.md)。用户已确认完整方案。
复杂度高：跨 AD、LLNG、门户、应用会话和持久凭据；复用现有事件生产者、数据库及原生认证
扩展点，不另建业务用户目录或消息服务。独立恢复管理员明确排除；密码到期不阻断 Passkey。

## 需求归属

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1a：版本与启动基线 | LLNG-READY-R-001 | 已完成；2026-10-26 驱动冷启动/重启实际复验通过，历史升级/改密基线保留 |
| M1b：资源比较 | LLNG-READY-R-009 | 已完成；保留原同负载实测基线 |
| M2：目录事件与会话撤权 | LLNG-READY-R-002、LLNG-READY-R-003 | 实施中 |
| M3：Passkey | LLNG-READY-R-004 | 已完成 |
| M4：门户与主题 | LLNG-READY-R-005、LLNG-READY-R-007 | 已完成 |
| M5：全部适用 Consumer，OIDC 优先、SAML 后置 | LLNG-READY-R-006、LLNG-READY-R-010 | 实施中 |
| M6：恢复与轮换 | LLNG-READY-R-008 | 实施中 |

## 检查表

- [x] 完整高复杂度方案获用户确认，明确两项策略。
- [x] 上游版本改为 2.23.4，实际包版本 2.23.4-1；未发布正式 revision。
- [x] 修复启动 ping 管道提前退出导致的 SIGPIPE；新增延迟回复正例与解析失败反例。
- [x] 完成版本回归、旧版本升级与资源比较，记录最终镜像。
- [x] 修复邮件接入复测发现的 PostgreSQL 驱动首次加载超时及常驻工作进程重载失败；固定入口预加载，实际冷启动与持久配置重启后门户/Go/Roundcube OIDC 均通过，见邮件计划 §4.26。
- [ ] 实现并验收事件消费与真实会话撤权。
- [x] 实现并验收 Passkey 与目录状态边界。
- [x] 实现并验收现有门户、近期认证与主题。
- [ ] 完成 SAML 与全部适用应用矩阵。
- [ ] 完成空环境恢复、凭据轮换和发布门禁；全部通过后才能标记 release。

## e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| LLNG-READY-R-001 | 历史升级/改密；正式邮件接入的实际 OIDC 冷启动 | finance 独立 Docker | 2026-10-26 | 2026-10-23 的 2.23.2→2.23.4/五项改密通过基线保留；2026-10-26 重开后以入口预加载修复 Pg 首次 TIMEOUT，固定运行镜像的冷启动及持久配置重启后实际门户/邮件 OIDC 通过；完整发布和其他消费者范围仍未完成 |
| LLNG-READY-R-002 | `journal.t`、`watch.t`、`llng-portal.spec.mjs` | finance 独立域 | 2026-10-23 | 真实 LDAPS→审计→事件→后台登出、回调失败/重启、日志半行/重复轮转/缺口/损坏游标恢复通过 |
| LLNG-READY-R-003 | `DirectoryRevoke.pm`、`llng-portal.spec.mjs` | finance 独立域/实际 user_portal | 2026-10-23 | 禁用、直接/嵌套组撤权、另一用户及无关客户端保持通过；Vikunja 例外真实差异通过，删除（中央会话）、锁定及管理员角色撤权通过；其他消费者仍待验 |
| LLNG-READY-R-004 | `llng-passkey.spec.mjs` | Chromium 虚拟认证器/独立域 | 2026-10-23 | 原生及门户矩阵、重启/空库恢复、真实删除与同名重建拒绝旧凭据、重新登记全部通过 |
| LLNG-READY-R-005 | `llng-public-oidc.spec.mjs`、`llng-portal.spec.mjs` | finance 独立域 | 2026-10-23 | 公共协议、真实门户登录/应用交集/密码与 Passkey 近期认证/普通用户边界通过；本人资料/密码写入与委派管理通过；门户双向退出、IAM 故障时本地退出及一次性 state 边界通过 |
| LLNG-READY-R-006 | 既有 SAML 矩阵待适配 | finance 独立域/实际 SP | — | 未执行 |
| LLNG-READY-R-007 | `llng-theme.spec.mjs`，原生 CSS/JS 与应用目录配色 | 中英文桌面与窄屏浏览器 | 2026-10-23 | 最终候选四组浏览器检查与窄屏中文截图复查通过 |
| LLNG-READY-R-008 | `llng-recovery-fixture.py`、`llng-recovery.spec.mjs` | finance 新建空数据库/配置目录 | 2026-10-23 | 空库恢复、既有 Passkey/身份、损坏归档/既有目标拒绝、OIDC 签名与客户端秘密、LDAP/SCRAM 凭据轮换和回滚通过；SAML 签名过渡随 SP 后续验收 |
| LLNG-READY-R-009 | `llng-resource-comparison.py`、`llng-resource-benchmark.py` | finance 同主机/目录/PG | 2026-10-23 | 双方各 60/60，已计必需消费者；共享主机压力与 swap 限制已记录 |
| LLNG-READY-R-010 | 实际门户、Nextcloud、Vikunja 浏览器与目录事件套件 | finance 隔离环境 | 2026-10-23 | 三个 OIDC 应用部分矩阵通过，Vikunja 后台退出例外已确认；Nextcloud 退出补强及其余消费者范围待决定，SAML 未执行 |

## 基线与当前结果

初始源码 HEAD `058d5c2ce87253897e1619b762f42cef9c3e71ee`；最终工作区 HEAD
`a9242c2acef71e14a1a3097c3fb59bf5fc1a9d59` 加本任务变更。历史升级仍固定前者原始源码。
测试仅使用 `whl@finance.hlong.wang` 的独立 Docker socket
`/run/anas-llng-test-1023.sock`，data-root
`/home/whl/anas-llng-ready-test-20261023/docker`，独立域 `LLNGTEST.INVALID`。
生产 Docker 仅只读导出缓存镜像。其他任务的隔离 Docker 不参与本测试。

原始脚本、日志和后续证据位于远端测试目录及 Git 忽略的
`test-env/reports/llng-ready-20261023/`；最终摘要、校验和及清理结果待补。
历史 2.23.2 五项改密通过基线保留在[改密计划](./llng-password-change.md)，
本轮 2.23.4 结果单列。该计划因重启回归失败已重开，必须在最终镜像回归后关闭。

初步代码核查：原生 Manager `userLogout` 可向 Portal `/admintokenrevoke` 发起 SSO/OIDC
撤销。后续验证应走这一原生流程，不能把直接删除数据库会话视为下游退出成功。


Passkey 已接入原生 Choice 和设备注册配置，增加 WebAuthn 校验依赖及持久查询索引；
不可变 anchor 查询与目录状态规则已落地，尚待实际浏览器/目录状态矩阵验收。
密码是备用首因素；登记 Passkey 不自动开启强制双因素。

2026-10-23 Passkey 实测发现第一版目录状态规则使用 `time()`，被 LLNG Safe 表达式沙箱拒绝；
上游 GrantSession 跳过无法编译的规则，导致锁定用户仍被授予会话。该候选失败，不能发布。
已改用原生 `_lastAuthnUTime`，并处理实际 LDAP 返回的重复 computed 属性值；单元测试改为
在 Safe 中编译和执行表达式，实际锁定/到期矩阵待修复后重跑。此前登记、首因素、强制改密、
改名保留实际 Vikunja 用户 ID 和禁用拒绝已分别观察通过，尚不构成整项验收。

后续候选已通过锁定及账户到期拒绝。设备管理发现原生激活值 `0` 会隐藏删除入口，改用
假表达式 `0 == 1`，维持两种独立首因素。双 worker 连续登记又观察到旧 SSO 缓存覆盖
前一台新设备；关闭进程内及本地会话缓存后，本轮 2.0 分钟完整浏览器用例通过：双设备登记、
首因素与真实 Vikunja 回跳、强制改密/自然到期允许、改名不换号、禁用/锁定/账户到期拒绝、
删除主设备后主设备拒绝且备用设备与密码有效。证据 `passkey-browser.json`；两个失败候选
分别保存为 `passkey-browser-remove-selector.json` 和 `passkey-browser-stale-registration.json`。
此结果尚未覆盖目录删除、重启、恢复与全部 Consumer，M3 保持实施中。

目录订阅第一版复用 LLNG 原生 Perl 会话与 OIDC 签名 API；同镜像独立非 root 进程，
只读配置/日志，私有持久游标及待通知文件。74 项原生运行时单元测试通过（包括日志完整尾行、
轮转、重复/冲突、保留缺口、游标损坏、突发防抖、周期核对、回调失败重试和重启）。
Samba 补充锁定/账户到期属性名，成功发布事件后立即刷新健康文件序号，36 项生产者测试通过。
当前 Vikunja 2.4.0 没有标准后台登出接收接口，SAML 浏览器单点登出也不能冒充后台撤权；
对应通知将保留为失败，不允许据此关闭 R-003/R-010。后续须在完整消费者矩阵中记录具体限制。

门户源码适配正在进行：`llng-account/v1` 本人 API、S256/近期认证、原生规则与目录现值的
交集；新增 OIDC/SAML 请求时目录复查。完整门户验收及事件消费者矩阵仍未完成。

真实门户已通过登录、本人资料、原生准入与应用目录交集、再次输入密码的近期认证以及普通
用户不可枚举其他账户。隔离夹具最初将近期认证窗口设为 5 秒，在共享主机慢响应时曾出现
认证完成后已过期的测试失败，改为 30 秒并仍等待其真正过期；最终候选须重新跑完整矩阵。

`portal-events.json`：真实 LDAPS 修改临时账户，Samba 原生审计生成持久事件，LLNG 消费者
完成原生后台登出，传播用时 16849 ms。禁用期间浏览器停在空白页，未请求受影响用户 API；
待回调确认后重新启用 AD，受影响旧 Cookie 仍为 401，另一用户旧 Cookie 为 200。
本次候选镜像 config digest `sha256:db6bd8f89313a44294e051a62b51d9e3d6aa7b59b7f6ef0c9f91dcb8320c479b`。
初期失败分别来自夹具直写 sam.ldb 绕过审计、交互模式将审计写向 stdout，以及生产者健康序号
到全量扫描时才刷新；实际审计发布改用 LDAPS、文件日志，健康序号已修复。失败证据保留在
`portal-browser-local-db-write.json`、`portal-browser-audit-not-initialized.json` 和
`portal-browser-first-live-events.json`。这些失败记录不能视作撤权通过证据。

`portal-event-retry.json`（2.4 分钟通过）：测试代理仅将门户后台登出指向不可达端口；禁用后
出现持久待通知且游标不推进。在回调仍失败时重新启用账号并重启消费者，通知及旧游标保持；
恢复原回调后重试成功，旧受影响 Cookie 为 401，另一用户为 200。测试期间未重启实际门户，
避免内存会话清空造成假通过。最初代理重启瞬间连接重置的夹具失败保存为
`portal-event-retry-proxy-start.json`。组声明带 `;string;always` 时的管理员角色撤销也已补回归。

公共 OIDC 浏览器协议测试 37.0 秒通过：S256、签名/issuer/aud/nonce/auth_time、真实再次
输入密码、本人 API、缺少令牌拒绝、PKCE 降级/错误 verifier 拒绝，以及禁用账户后旧令牌
访问本人 API 和旧 SSO 新授权拒绝。回调为协议夹具，不能代替完整 user_portal 验收。
实测修复了原生 renewsession 重定向丢失近期认证状态的问题：使用 LLNG 一次性令牌，绑定
原请求、目录 anchor 和新会话，并通过 keepPdata 保留到重定向后消费；重放与跨请求拒绝。
该轮运行插件通过容器内更新加载，最终镜像仍须重建并回归；证据 `public-oidc-browser.json`。

资源初测：Casdoor 3.143.0-r12 + 目录消费者空闲工作集约 33.94 MiB，60/60 登录成功，
中位耗时 579.29 ms；LLNG 7 worker 初测使用 swap，待两 worker 最终功能候选复测后比较。
脚本与汇总在 `test-env/reports/llng-ready-20261023/`，不外推为容量保证。

两 worker LLNG 中间候选：空闲工作集中位 184.62 MiB；60/60 登录通过，中位 1616.39 ms。
负载期发生约 61.78 MiB 中位 swap，目录与数据库的 swap 状态也与 Casdoor 测试不同。
此结果说明需要重新统一内存/预热基线，不能据此断言两者性能差距；必需事件消费者也尚未
计入 LLNG。证据 `resources-llng-2workers.json`，最终比较保持待办。

2026-10-23 门户完整回跳审查发现：原门户 `canLogin()` 会在 AD 强制改密/到期时拒绝
已经成功使用 Passkey 的会话。已使用原生 AMR 规则按首因素发布 `webauthn`/`pwd`，门户仅从
已验证 ID Token 接受 Passkey 证据；当前目录状态与写操作复查保持。密码会话及域级密码操作
维持原有边界。Passkey 的近期认证改走原生 OIDC，返回后重新提交操作。Go 门户/LLNG hook
回归和 21 项门户 UI 回归通过，实际 Passkey→门户强制改密/到期/近期认证用例待执行。

主题浏览器四组（en-US/zh-CN × 1280/390）检查通过，证据 `theme-browser-final.json`。
截图审查随后补正选中认证方式在深色模式下的对比度，最后候选仍需复验。截图仅包含未登录页面，
保存在本地私有测试目录。启动探测此前未看到 503，保留为失败；移除公开 vhost 的 loopback
例外，配置初始化改用已有独立 reload 端点，最终候选正在重测。

`portal-passkey-browser.json`（3.3 分钟通过）：真实门户识别签名 AMR，强制改密/自然到期
均继续允许 Passkey；本人资料写入、同主体的真实 Passkey 近期认证通过。初次再认证按钮
在原生异步 `portalLoaded` 绑定处理器前被测试点击，未发起认证请求；夹具等待原生处理器
后重测通过，保留失败 `portal-passkey-button-readiness.json`。本轮 LLNG config digest
`sha256:a47d8e42927f50053c715f42cb4ca63e050481cf451ba84c39fc18764a198a79`，
Portal 为当前 Go 源码交叉编译二进制。

启动保护已确认被上游 http 层 `error_page 503` 改为 302。加入 vhost 内独立 503 响应
后，实际移除就绪标记得到 Portal 503、内部 reload 200，标记随后恢复。该修复目前在
运行容器中加载，最终候选镜像须包含该文件并重跑完整启动探测。

完整门户七项首轮保留为 `portal-full-browser.json`：登录/近期认证、失败回调重启重试、
真实停用撤权三项通过，本轮传播 14073 ms。另四项尚未通过：两个浏览器 Fetch Response
误用 Playwright 的 `status()` 方法，委派夹具漏配 `audit.read`，均已定位并修正夹具，等待
重跑；不把已执行的写操作等同于完整用例通过。新增四种真实事件日志故障用例正在执行准备。
Samba 审计读取器补正未写完尾行处理，等待完整换行后再解析，38 项 Python 单元测试通过；
实际部署与故障链路仍需复验。

最终候选镜像 config digest `sha256:d2ba3b42cd56af8e3e0c132daa84713029ee6eafdea79949cd22fe5f155ff0c6`，
manifest `sha256:4b99c5e58cf59c9c5fbf63cc517386bd48330674ae67291e7d6dd1d50c8fa4bc`。
启动探测实际观察到连接未监听时 000、初始化时 503，配置完成后恢复；消费者正常启动。
`theme-candidate-browser.json` 四项通过（34.6 秒），深色选中标签已修复，窄屏中文截图复查通过。

门户最终候选首轮因隔离夹具遗漏模块已有 restart 策略，discovery 暂不可用时退出。
保留 `portal-candidate-startup.json`；夹具补齐 `unless-stopped`，服务健康恢复后重跑。
重跑发现两个历史 Vikunja 会话的通知无法完成：固定版本无原生后台接收端，失败队列保留且
游标不提交，后续要求全队列清空的事件用例受阻。未删除队列或把这些会话记作已撤销。
已请用户明确无后台协议应用的上线验收边界；支持后台通知的独立功能继续执行。

空库恢复夹具及既有认证器复验用例已实现，尚未执行；Nextcloud 34.0.2-r12 镜像已只读导入
独立 Docker。认证插件从官方商店取得：user_saml 8.2.0 SHA-256
`1ec7978f781a55ad6b8c9fc2d4deb1db6df2534bacc89eebb7c46a85530a8836`，user_oidc 8.11.0
`887920504c2f0e0d055abd66225ee7b66d756f50fe5a3739779d6b2ce108633f`；真实 Nextcloud 验收仍待执行。

用户随后明确“目前 OIDC 优先，LLNG 优先接入 OIDC”。执行顺序调整为先完成 OIDC
登录/退出/后台通知，再处理 SAML；这不等于已同意豁免没有后台接口的应用，也没有把未执行
SAML 项标为通过。已说明应用→LLNG 与 LLNG→应用两种退出方向及 Consumer 接口前提。

候选完整门户重跑保留 `portal-candidate-browser.json`：本人资料/密码写入、CSRF、冲突拒绝
和重新登录通过；通知失败、停用撤权因既有 Vikunja 无接收端待通知项阻塞，未获通过。
委派复验在重置时得到 403；待核对近期认证窗口边界并重跑，不能仅以前一轮写入成功放行。
`recovery-baseline-browser.json` 在 20.5 秒通过，记录两名用户既有 Passkey、真实应用内部
ID/目录 anchor 及公开签名密钥摘要；一致备份和空库恢复正在执行。
测试目录生成暂被并行工作新增 MAILPOC-T-001/002/003 引用、但尚无对应用例定义阻塞；
这些非本任务文件保持不变，后续待其完成后再运行仓库级门禁。

`portal-delegation-fresh-browser.json` 最终委派用例通过（2.9 分钟）：重置后新密码实际登录、
解锁、范围内改组、旧成员状态冲突、越权拒绝及审计均通过。前次 403 发生在 30 秒近期认证
窗口下；测试在最终重置前执行真实再次认证后未复现，没有放宽产品校验。

空库恢复已执行至新数据库 `llng_restore_20261023`，原库与原配置目录保留；所有表的行数
与内容摘要一致，两个未完成 Vikunja 通知及游标原样恢复。`recovery-restored-final-browser.json`
在 31.8 秒通过：原门户 Passkey、备用 Passkey、门户 anchor 和真实 Vikunja 用户 ID 均不变。
首次公钥摘要比较受 JSON 字段顺序影响而失败，修正为固定 `kty/kid/n/e` 顺序，并从恢复前
备份的公钥 PEM 独立导出预期 JWK；与恢复后的公钥材料一致，SHA-256
`1e9bd14fb64303708da7c7dab43c60d594348454bb5f5dfb87b24d40f4a6a93d`。
原失败证据和原始基线保留；该结果尚不覆盖凭据轮换及故障恢复矩阵。

OIDC 优先复核发现门户前端退出仍调用 Casdoor 私有 API，LLNG 登录适配未覆盖此路径。
已接入标准浏览器 RP 退出和一次性 state/Cookie 校验，保留 Casdoor 既有路径；门户、Hook
和 LLNG Hook 的 Go 回归通过，前端和真实会话验证正在执行。Nextcloud 严格 LDAPS、
anchor 身份和原生 user_oidc 配置完成，待重载注册后执行三项真实退出/事件用例。

门户标准退出候选二进制 SHA-256：
`58929449b993bd31d5aea5c3ba868cc9689d6fa5461af7c1e72d1b33083f0a21`。
Go 门户/门户 Hook/LLNG Hook 回归通过；原有 50 项门户 UI 回归通过，新增三个标准退出
前端用例（确认、回调失败、IAM 不可达）通过。前次两个新用例失败属于 Playwright 模拟
302 回跳绕过路由拦截，真实服务用例仍单独执行，不以模拟 UI 结果代替实际退出。


2026-10-23 用户明确接受 Vikunja 接入但无法后台强制退出。R-003/R-010 的范围仅增加该
应用的明确例外，其他应用、中央授权和新准入撤销要求不变。实现显式 client ID 列表，
把限制与失败通知分开持久保存；既有历史失败基线保留，不能改写成实际退出成功。
当前代码和真实迁移/重启验收进行中。

Nextcloud OIDC 实际浏览器测试已分别观察应用发起退出、目录禁用后台撤权通过；
Provider 发起退出曾出现旧 Cookie 有效，加入只输出已登记 client ID 的诊断后复测通过
（44.9 秒，`nextcloud-provider-registration-browser.json`），尚不能把重试通过当作根因修复。
代码审查确认目录更新可能使用计划阶段的旧 RP 列表覆盖随后登记的客户端，正在改为
原生事务内只删除被撤销的客户端，并补并发登记回归。Nextcloud 原生 SLS 未携带 state，
且 discovery 请求早于本地退出，仍不满足全部 LOGOUT-R-015 安全/故障判据。

门户 OIDC 最终二进制 SHA256 `3505234df1f5b4fe72901ab6d85143ca04bb9f76c2bb00c4c2c7dee84ae1a7e4`
已部署隔离环境，Go 门户与 hook 回归以及 53 项 UI 测试通过；三个真实退出方向用例待执行。
本轮测试目录生成已通过（13 主题、96 用例），此前并行 MAILPOC 引用缺失不再阻塞。


### 待确认：Nextcloud 原生 OIDC 退出缺口

官方 user_oidc 8.11.0 的 `LoginController::singleLogoutService` 在本地退出前读取 discovery，
且请求及回调均无 state。实际应用→LLNG 退出成功仅证明健康路径，不足以满足根双向登出
§3/§4.1 和 LOGOUT-R-015 的完整判据。Vikunja 例外不适用于这里。

建议使用 Nextcloud 原生 AppFramework 中间件和路由扩展，限定接管 ANAS 已登记 OIDC 的
退出入口：先读取必要退出参数并销毁本地 session/token，再使用已验证 discovery 跳转；
随机一次性 state 通过短期 HttpOnly Cookie 绑定浏览器，在同源专用回调验证后显示结果；
IAM 不可达时仍保留本地退出成功并允许重试。原 user_oidc 登录及后台接收端不改。

复杂度中：增加一个随 Module 交付的 Nextcloud 小扩展，不增加服务、数据库或用户副本；
依赖 Nextcloud 和 user_oidc 的服务接口，每次两者升级须复验中间件、会话撤销和回调。
相比直接改签名发布包，避免破坏上游文件完整性校验；相比保留原生行为，增加了插件维护。
更简单方案是明确接受 Nextcloud 原生退出的这两项限制并收窄相关判据，不能默认为已获授权。
此部分仅为设计建议，尚未实现；其他已批准工作继续。


2026-10-23 Vikunja 例外候选镜像 manifest
`ba40c3dadf0df099495c9e22404d23991dc354cb1cffab031364bc8019ad6834`，config
`0fc4dd6e16c9d30502aee90f88ef01ae45ab25473fafaff326677f7edc935e9c`；两 worker，
恢复后的独立数据库。启用 `LLNG_BACKGROUND_LOGOUT_EXCEPTIONS=vikunja-test` 前将历史队列
保存在远端私有 `exception-before/`。部署后积压三项转为明确限制，pending=0，
游标从 47 推进至 79，健康摘要显示 `limited_clients=[vikunja-test]`。原生 Perl 84 项通过，
Go LLNG hook 通过；实际旧会话差异和事件故障矩阵继续执行。

门户三个真实 OIDC 退出用例全部通过（35.3 秒，`portal-oidc-logout-browser.json`）：
应用发起、Provider 发起、Provider 不可用时本地先退出，原 Cookie 401、其他用户 200。
新镜像的 Nginx 合成查询/Referer 标记未进入日志，正常及 404 路径/状态仍保留。
需求覆盖、用例目录及需求/计划状态三项门禁通过。随后 inventory 测试被并行工作中的
`module ai_agent capability family must select its own names` 阻塞；不修改该模块来掩盖失败。


`exception-directory-matrix-browser.json`：9 项全部通过，8.4 分钟。覆盖通知失败重启重试、
实际 LDAPS 禁用（11.873 秒完成该次传播）、Vikunja 明确例外、直接和嵌套组撤权、未完成
尾行、重复轮转、保留缺口、损坏游标。每项都使用事件到达后重新启用 AD 的旧 Cookie
断言，防止请求时目录检查替代后台通知。Vikunja 例外测试确认其旧 API token 仍为 200，
而门户旧 Cookie 为 401、LLNG 旧 Cookie 回到登录，消费者重启后限制记录存在。


资源比较已完成：`resources-casdoor-final.json` / `resources-llng-final.json`，两者各 60/60；
IAM + 必需消费者空闲工作内存分别 92.08 / 269.14 MiB，负载 CPU 10.15 / 21.49 秒，
流程中位数 1098.85 / 1578.12 ms。方法、共享依赖、swap、各组件限额和不能外推的
[解释已沉淀到调研](../../../../docs/research/llng-production-readiness.md#2026-10-23-同主机资源实测)。
Casdoor 镜像 `50456c6d25c7350fc72cbf7de4955ec5ef29acc7381802630b9003f8f1759fc8`，LLNG
继续使用上述 `ba40c3…` 候选；Postgres/Samba/生产者完整镜像 ID 在聚合 JSON 中。
本次补齐消费者后不能继续沿用此前省略 LLNG 消费者的 184.62 MiB 数字比较。


2026-10-23 原生签名密钥轮换：`key-rotation-prepublished-browser.json` 通过（1.0 分钟），
在相同候选镜像上验证旧签名与新公钥预发布、新签名与旧公钥重叠、真实门户及 Nextcloud
登录 anchor 不变、提前撤旧反例及原密钥回滚。新 `kid=llng-test-rotated-20261023`；
私钥只保存在远端私有 `rotation-backup/`，测试结束恢复原签名配置。

失败记录 `key-rotation-isolated-callback-browser.json` 与 `key-rotation-diagnostic-browser.json`
保留。后者定位到 Nextcloud user_oidc 8.11.0 的 3600 秒缓存：`obtainJWK` 在 `JWT::decode`
的重试块之前调用，未知 kid 在 `fixJwksAlg` 抛错，实际 OCS 用户接口为 401；门户为 200。
按照原生缓存时间清除命令，在旧密钥仍签名时执行真实 Nextcloud 登录以获取新旧公钥，
之后切换签名通过。未修改上游源码；缓存预发布步骤已写入中英文技术文档。最初两份
`key-rotation-browser.json` / `key-rotation-state-browser.json` 是协议回调测试页被真实
Vikunja 页面干扰，隔离回调路由和禁用该测试上下文 service worker 后 state 一致；
不记作产品 state 漏洞。

补齐四份人工维护配置表的 `background_logout_exceptions` 用途后，文档生成及
`TestBuiltinInventory` 通过（`inventory-after-purpose.log`）；前述并行模块错误已消失。
R008 仍待其他适用凭据轮换及完整失败矩阵，不能据此标记 M6 完成。


`passkey-lifecycle-scoped-browser.json` 通过（47.2 秒）：在生产者订阅的 `OU=People` 内
登记新测试凭据，通过真实 LDAPS 删除账号，等实际事件确认后验证旧中央 Cookie 不能登录；
删除时及相同用户名重建后旧 Passkey 都被拒绝；新账号以密码登记新 Passkey 后登录成功，
实际 Vikunja 用户 ID 改变。没有新增恢复管理员。结合之前双设备/撤销/改名/禁用/锁定/
到期矩阵及空数据库恢复后的既有 Passkey 登录，R004/M3 完成。

首轮 `passkey-lifecycle-browser.json` 未通过：旧密码测试账号在 `CN=Users`，生产者仅
订阅 `OU=People`，因此未发布删除事件。修正夹具范围后通过，未扩大生产者订阅配置，
不能把首轮失败解释为 LLNG 消费故障。重建后的临时账号具有新的 anchor，历史恢复测试
使用的原 Passkey 文件仍仅代表历史基线，不得拿它对新账号重跑并期待成功。


`portal-directory-boundaries-browser.json` 两项通过（2.0 分钟）：真实 LDAPS 锁定与
Admins 成员撤销发布事件，消费者确认后恢复目录状态，受影响门户旧 Cookie 仍为 401，
另一用户为 200。使用实际事件与旧 Cookie，未以请求时目录拒绝替代后台撤权。
结合删除与既有禁用/直接组/嵌套组结果，门户边界补齐；其他 Consumer 矩阵继续保留未执行。


`client-rotation-native-browser.json` 通过（57.3 秒）：原生 LLNG 配置与 Nextcloud
`user_oidc:provider` 分两端更新临时客户端秘密。旧秘密从 `invalid_grant`（通过客户端认证，
拒绝故意无效的授权码）变为 `invalid_client`，新秘密相反；两端不一致时实际 Nextcloud
回调 403、用户 API 401，更新 Consumer 后恢复并保持 anchor，回滚后新秘密失效、原身份有效。
操作结束恢复原秘密，原始/替换秘密仅留在远端私有 `client-rotation-backup/`。

失败证据 `client-rotation-browser.json`：容器内 OCC 无法通过 `/dev/stdin` 读取秘密；
改为 UID 33 可读的随机 0600 临时文件并在 finally 删除。`client-rotation-file-browser.json`
定位到官方 user_oidc 8.11.0 `UpsertProvider` 的更新选项白名单遗漏 `clientsecret-file`，
单独传此选项只查询却返回 0；同时传不变的 `--clientid=nextcloud` 后进入原生更新分支，
最终真实回跳通过。未修改上游文件。此结果验证原生两端轮换与失败边界，不等于实现 ANAS
统一自动轮换命令，也不覆盖共享 LDAP bind/数据库凭据轮换。


历史升级镜像由固定提交 `058d5c2ce87253897e1619b762f42cef9c3e71ee` 的原始 Module 源码
构建，基础镜像 `lemonldap-ng:2.23.2` manifest
`a43d6110e6754a10d5f19986d8125cbdc4e4247a7cd1da1c62d8bffa4f7997b9`，生成镜像
`a49853b5bfca9150d485a45249225abd07161ba625c9248d68be7056d07e3608`；原生包版本确认 2.23.2。
首轮单独数据库 `llng_upgrade_20261023` 登录未通过应用准入：历史 Module 将夹具组名
`LLNG App A` 拆成 `inGroup('LLNG')|inGroup('App')|inGroup('A')`，而测试用户不属于这些组。
原源码未修改；后续升级夹具使用符合历史 APP 命名的 `APP_llng_upgrade` 和独立新数据库。
带空格组名的当前候选行为已由前述独立矩阵验证，不能据此宣称历史版本也支持。
首轮等待已中止，私有基线配置/数据库/日志保留；升级验收尚未成功，不记为通过。


`upgrade-compatible-group-browser.json` 通过（4.5 分钟）：历史源码镜像原生版本 2.23.2
实际登录 Vikunja 后，使用同一独立配置目录和 `llng_upgrade_v2_20261023` 数据库升级到
`ba40c3…` 候选，原生版本确认为 2.23.4。启动前后四张原生会话表数量/内容摘要一致，
原公钥及旧中央 Cookie 保持有效，重新登录仍是相同 Vikunja 用户 ID。随后恢复原候选
容器及目录消费者，保留旧版配置快照和测试数据库。原始证据在私有 `upgrade-baseline/`。
这仅证明该实际 PostgreSQL/Vikunja 升级基线；不外推 MariaDB、未测试 SP 或生产切换。

更新对比研究使其区分初始缺口与已验收候选能力；完整文档构建通过（14.83 秒，
`docs-build-latest.log`），保留原有 bundle 大小警告。当前仍 developing，M5 和 M6 未完成。


### 首批应用范围待确认（不是扩大 Vikunja 例外）

按当前调用路径复核，`meshcentral/hook/iam.go` 与 `oauth2_proxy/hook/main.go` 不发布
`OIDC_LOGOUT_URI`，各自测试明确要求不发布；`forgejo/hook/main.go::calculate` 也只登记
登录回调，不登记后台退出。它们不能借用 Vikunja 的已确认例外。Immich 要求
`OIDC_CAEP_EVENTS=session-revoked`，当前 LLNG 不发布该能力，Runner 的协商守卫拒绝绑定；
NetBird 还有已记录的 anchor 投影拒绝，不能计作可用组合。这是现有 Module 契约核查，
不是这些应用所有新版本能力的断言。

建议首批明确为 user_portal、Nextcloud 和已接受限制的 Vikunja。其他缺少后台接收能力的
OIDC 绑定在配置阶段明确拒绝，随后按应用补齐；SAML 保留后续里程碑，不记为已完成。
复杂度中：利用现有注册与例外字段做校验，无新服务，但会收窄首批可接入应用集合。
替代方案是坚持全部现有消费者同时上线，先逐项补齐客户端接收端、真实本地会话终止、
长效 token/SSH key 等相应撤权边界及回归；复杂度高，涉及多个上游应用和长期维护补丁。
两项都尚未作为新的范围决定实施。AGENTS.md 要求收窄验收范围或新增显著维护复杂度前确认。


最终完整镜像的五项密码回归已通过，详见配套改密计划及 `final-password-config.log`、
`password-reset-fresh-secret-browser.json`（8.9 秒）、`password-expired-confirmed-browser.json`
（8.3 秒）。历史密码复用和过早启动到期测试的失败报告保留，最终使用全新目标密码且先确认
目录过期属性；测试域已恢复 42 天策略。结合真实旧版本升级及同机资源实测，M1 完成。
门户联合退出 `portal-oidc-logout-browser.json` 的三项已通过，结合本人/委派、公开协议、
Passkey 近期认证和主题验收，R005/R007 对应 M4 完成；Nextcloud 的新增退出扩展不计入门户
结果，也尚未获确认实施。


`nextcloud-final-browser.json` 在最终 `ba40c3…` 镜像上三项全部通过（2.2 分钟）：
实际应用发起退出、Provider 发起退出，以及真实 Samba 禁用事件触发后台撤销。旧应用 Cookie
与中央会话失效，同一用户的另一独立 SSO 和其他用户会话保留。原生 RP 退出请求仍未携带
state；该结果不覆盖尚待确认的 state 校验和 IAM 故障时本地退出扩展。


服务凭据轮换过程发现真实目录边界：`service-rotation-resumed-browser.json` 中新建 LDAPS
连接同时接受原/新绑定密码，不能计为旧秘密已退役。Samba AD 默认 `old password allowed
period=60` 分钟，官方说明见 [参数文档](https://www.samba.org/samba/docs/current/man-html/smb.conf.5.html#OLDPASSWORDALLOWEDPERIOD)。
试验拟临时缩短隔离 DC 窗口到 1 分钟并观察真实到期，最后恢复原设置；生产策略未改变。
此前 `service-rotation-browser.json` 在标签守卫前失败（底层 PG/Samba 使用较早的
`llng-oidc-20261023` 标签）；`service-rotation-isolated-browser.json` 已准备专用库/角色后遇到
SSH 连接关闭，尚未切换 LLNG 配置。`service-rotation-grace-browser.json` 的首次重载使用
默认配置路径，未达到预期旧密码拒绝；改为显式传 DC 活动配置 `/var/lib/samba/etc/smb.conf`。
以上失败均保留，测试通过前 M6 不完成。


`recovery-failure-browser.json` 通过（2.5 分钟）：与真实恢复共用的校验和及目标库存在守卫
拒绝损坏或覆盖；另故意绕过校验，在新的 `llng_failed_restore_20261023` 测试库执行截断
归档原生导入并确认失败。原运行库四张会话表数量/内容摘要、配置文件摘要和环境配置不变。
重新启动原 LLNG 后，用同一已登记门户 Passkey 登录，稳定 anchor 和公开签名密钥保持。
部分导入库及私有日志保留在远端 `recovery-failure/`，未污染原库。

`service-rotation-active-config-browser.json` 仍未通过：显式活动配置重载后，1 分钟窗口
超过 90 秒仍接受旧密码。原配置已回滚；下一轮改为重启隔离 DC 后验证，不能将文件中参数
改变当成运行认证已生效。官方 4.23.6 `source4/auth/ntlm/auth_sam.c` 同时将 NTLM 和 LDAP
简单绑定纳入旧密码宽限，并按 `auth_context->lp_ctx` 的参数与 pwdLastSet 计算期限。


最终制品复核：`llng-exception-build.tgz` SHA-256
`a14887f22f3419f65d2430d1a2de72f545a79743aa91c37e11afd9bd0586ef6e`，内含 24 个运行时
源文件逐个与当前 `modules/llng/llng` 比较完全一致。使用当前工作区重新构建 Linux/amd64、
CGO=0 门户，输出 SHA-256 仍为
`3505234df1f5b4fe72901ab6d85143ca04bb9f76c2bb00c4c2c7dee84ae1a7e4`，与实测二进制逐位一致。
本轮没有为了复测部署新的产品源码。


`service-rotation-native-grace-browser.json` 最终通过（3.0 分钟）：专用 LDAP 绑定身份和
克隆数据库/专用角色能登录真实 Vikunja；Samba 测试 DC 重启应用 1 分钟窗口后，先验证新旧
密码均可用，再观察旧密码在期限结束后拒绝，新密码继续有效。PG 仅对专用库/角色的回环连接
强制 SCRAM；新建连接分别验证旧密码拒绝、新密码接受，真实登录在新库产生额外 OIDC 会话。
最终回到原共享服务配置，应用用户 ID 保持。原共享账号密码未改动，测试专用秘密只存放于
远端私有 `service-rotation-backup/`。原 DC 配置及默认 60 分钟宽限期恢复。

`service-rotation-restarted-dc-browser.json` 在开始变更前等待浏览器页面 load 超时；未发生
轮换。最终改用回跳 origin 加真实用户 API 判定，保留该失败，不推断它是服务端认证失败。
本轮没有实施 ANAS 统一凭据轮换命令或生产域策略变更。R008 的 OIDC 范围已验收；SAML
签名/metadata 过渡仍随实际 SP 矩阵后续执行，所以 M6 和整体计划继续保持实施中。


### 当前收口状态与证据

`post-acceptance-health.json` 确认原服务配置和 Samba 配置逐字恢复，旧密码宽限回到 60 分钟；
七个相关容器运行且无 OOM，原生就绪检查通过。目录消费者 pending=0、原生健康检查通过，
持久限制仅 `vikunja-test`。最初额外使用了 30 秒健康更新时间阈值，和消费者空闲时 300 秒
核对/360 秒原生健康边界不符；核对当前调用路径后按既有原生检查验收，未修改产品阈值。

本轮最终门禁通过：需求覆盖 28 文档/764 需求、13 主题/98 用例目录、需求状态、计划状态、
BuiltinInventory、Module 文档生成、Python/JS 语法与 `git diff --check`；完整文档构建 15.10 秒。
未重复全仓库 Go 测试。本轮新增的是验收脚本和运维说明，产品运行源码与已测制品已复核一致。

独立脱敏证据包共 116 文件，包含浏览器成功与失败记录、最终资源比较、恢复后健康摘要、
验收脚本源码、运行时构建输入和门禁日志。SHA-256：
`e3e03fd68ae0286041bc824f61feb59ed3959779acaca2bc7d01a9aa794074cd`。
归档位置：`finance:/home/whl/anas-acceptance-evidence/llng-oidc-20261023/e3e03fd68ae0.tgz`，
独立于测试 Docker/data-root，另有本地私有复核副本。密码、Passkey 私钥、运行配置和数据库
备份未放入该包；其私有原件暂留隔离目录，整体验收完成后再清理。

M1、M3、M4 完成。OIDC 范围的恢复、签名/客户端/LDAP/数据库轮换及失败边界通过；M2/M5/M6
仍保留未覆盖应用及 SAML 判据，整体保持 developing。下一步等待已有两项选择：Nextcloud
退出扩展或接受其原生限制；首批限定门户/Nextcloud/Vikunja或继续补齐所有消费者。未将
Vikunja 例外推广给其他应用，未擅自收窄范围，也未切换生产 IAM。

## 2026-10-26 邮件接入的启动回归

邮件计划第 33 轮的正式 LLNG 部署首次加载 DBD::Pg 时发生 TIMEOUT，之后同工作进程反复报重载失败并拒绝表单 token；root 与 Web UID 的独立驱动加载均通过，不能解释为缺包。重启并等待实际健康后，真实 LLNG→user_portal→Go 邮件→Roundcube 登录通过，但不覆盖冷启动失败，因此重新打开 R-001 的启动验收。R-009 的原资源比较保持历史结论，其余已通过范围没有据此重新定性。

现有入口的 PostgreSQL 分支补标准 PERL5OPT 预加载，并在镜像中明确 libdbd-pg-perl 依赖；不修改上游或请求超时，不回退文件会话。冻结制品、原始私有证据、后续冷启动与重启结果统一记录在[邮件计划 §4.25](../../../../dev-docs/plans/mail-integration.md)，未通过前保持 developing。

2026-10-26 邮件接入回归曾重开 M1a：首次请求加载 DBD::Pg 被 TIMEOUT 中断后，同一工作进程不能重新加载。自有入口的 PostgreSQL 分支通过标准 PERL5OPT 预加载，Dockerfile 显式声明 libdbd-pg-perl，未修改上游。固定源码 `73f4512f672a738026b507721779a36642c79be6`，运行镜像 `8f1b35d5b3bae149b4588191bb8f7ecfeff875526d70962a6fb2bc9be4da84f5`；它使用已固定 2.23.4 基础的自有运行层装配，未声称重跑完整发布构建。第 35 轮冷启动和第 37 轮显式重启后的真实门户 → Go → Roundcube 通过；重启后认证前实际父进程和两个工作进程均映射 Pg.so。原环境变量可见性检查不足及暂时路由 404 的未就绪尝试保留，完整证据/校验和见[邮件计划 §4.26](../../../../dev-docs/plans/mail-integration.md#426-完整退出补正与建号冲突查询2026-10-26实施中)。仅关闭原 R-001 启动回归，不扩大至事件、SAML、恢复或发布验收。
