# 邮件 Module 技术说明

邮件入口和 Roundcube 使用 `anas.client-ip.mode=edge`：客户端地址以 Traefik 访问日志为准，
后端不从任意 `X-Forwarded-For` 恢复人员身份或授权信息。Roundcube 的 IP 会话绑定和登录 IP 日志保持关闭。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `0.1.0-r1` / `anas.module/v1`.
<!-- generated:module-identity:end -->

当前已实现三组件部署接线、统一身份与单箱协议基础、AD 只读投影和公司通讯录接口；完整一期仍在实施，正式邮件要求仍在根 `dev-docs/requirements/mail-integration.md`，进度和证据在配套计划。没有把隔离原型的 Docker 命令、测试 Token、硬编码 finance 路径或 `doveadm save` 复制进正式服务。

## 边界与数据模型

`store/schema.sql` 定义人员/组锚点、邮箱和统一地址表。邮箱、协议登录、存储引用及 Roundcube 用户键分别随机生成，不从 anchor、用户名或邮件地址派生。主箱唯一约束及按 anchor 的事务锁保证重复协调返回原资源；全局地址主键覆盖大小写规范化后的占用、保留和既有归属。地址改名未实现；人员认证通过 Go 的 OIDC/实时目录路径。正式协调器消费 AD 的稳定 mailbox_id，不通过 `EnsurePrimary` 创建第二份目录权威。

新资源为 `pending`，人员目录状态为 `unknown`；没有“已保存即可以登录”的路径。普通移除保留地址占用；只有后述匹配持久证明的内部事务允许受限移交，没有通用地址删除接口。组准入锚点和只读公司通讯录已接线，真实事件到各端的收敛仍待验收；不创建二期共享联系人。

初始化以事务级 advisory lock 串行执行，校验内嵌 SQL 的 SHA-256，确认版本后才提交。并发冷初始化不竞争 DDL，失败不会留下部分迁移；不认识的版本或源码漂移拒绝启动。每个已应用版本都核对校验和并要求连续编号，未知后续版本、历史版本漂移或中间记录缺失均拒绝；`sessions.sql` 为 v2，`login_flows.sql` 为 v3，`iam_logout.sql` 为 v4，`webmail_handoffs.sql` 为 v5，`mailbox_projection.sql` 为 v6，`protocol_credentials.sql` 为 v7，`submissions.sql` 为 v8，`revocation_tasks.sql` 为 v9；已有迁移文本和校验和保持不变。不能修改已发布迁移后自动覆盖数据库。

`store/session.go` 是 M2 HTTP 调用的内部持久层：消费已验证的人员 anchor、HTTPS issuer、IAM sid、认证时间和到期时间；调用方必须先验证 OIDC、实时目录准入并限制会话到期时间。256 位随机 Cookie 只返回一次，数据库保存 SHA-256 和独立会话 UUID。查询总是读取 PostgreSQL，检查到期、永久撤销和人员 enabled 状态；没有内存授权回退。同人多会话可分别撤销，重复撤销幂等。存储方法不自行验 OIDC、不直接接收浏览器输入、不持有人员密码。`AdmitSession` 仅由可信回调在实时目录核验后调用，事务中登记本地准入和会话，不创建邮箱；重新准入先终结非 enabled 状态下遗留的旧会话。下游踢线与事件撤权沿用持久任务；恢复旧会话失效候选见后述 v15，完整实际协议及恢复仍须 M4/M6 验收。

## AD 邮箱配置投影

`store/projection.go` 接收邮件只读目录端已经核验的单箱事实，直接使用 AD 的 mailbox_id；协议登录名、存储引用和 Roundcube 键另行随机分配，并在后续地址/状态变更中保持。v6 保存规范化快照、摘要、本地修订号、AD 配置版本、授权版本、应用确认和有限错误码，不保存人员密码。主箱已被另一内部 ID 占用、异主箱/地址、未知对象或不完整状态都拒绝自动接管。

暂存以人员事务锁和本地修订号比较串行更新，保留旧地址占用、关闭登录/发送就绪并增加授权版本。别名移除不会转让地址或移动历史邮件。附加箱分别分配协议、存储和 Webmail 键；人员禁用或单箱停用的接收配置与访问许可分别处理。配置投影不将人员设为 enabled；只有 IAM/目录准入路径可以登记人员登录。

`ConfirmMailbox` 要求同一待应用修订号、与新鲜 AD 读取相同的配置摘要，以及未被其他撤权动作推进的授权版本。pending 地址/邮箱不能确认；停用箱可确认接收配置但保持不能登录。调用方仍必须在真实 DMS 回读后调用确认，不能把该库方法当作下游就绪证明。固定 DMS 适配及正式协调循环已接线，实际 AD→PG→DMS 配置回读结果见根邮件计划 §4.22—4.24；这不代表公网收发已就绪。

## 身份校验与短期登录流程

`identity/oidc.go` 复用已有 go-oidc，使用 HTTPS discovery、同源认证/token/JWKS 端点及 RS256 签名验证；HTTP 请求拒绝重定向。授权码使用 S256 PKCE、随机 state/nonce，兑换校验 issuer、audience/azp、nonce、人员 anchor、认证时间及可选 access-token hash，不把 email 或用户名当身份。此库只确认 IAM 身份，不自动授予邮箱或创建会话；Casdoor/LLNG 真实浏览器组合验收尚未执行。

`store/login_flow.go` 创建 5 分钟流程，数据库保存 state 与浏览器 secret 的 SHA-256、nonce、PKCE verifier 和到期时间。HTTP 调用方将浏览器 secret 放在 Secure/HttpOnly/SameSite=Lax Cookie；回调须先按 state 和浏览器绑定原子删除，再兑换授权码。错误浏览器不消费流程，8 路并发只有一个成功，重启不丢绑定，过期记录在新建流程时回收。内部 HTTP 登录调用该流程，正式 Hook/Compose 通过私有 runtime 文件交付身份配置；已完成隔离三组件启动，真实双 Provider 登录仍须分别验收。该流程不替代 Roundcube 交接码。

`identity/directory.go` 只有服务绑定和 Search，没有人员密码绑定或 AD 写方法。LDAPS 使用 TLS 1.2 以上并校验证书；默认校验 URL 主机名，受信调用方可通过标准 TLS ServerName 指定已知证书名以连接同一服务的内网地址。按固定 `anasIdentityAnchor` 在受管人员边界查唯一对象，核验账号状态、有效期、密码状态和域人员 SID；未知密码有效状态不能仅凭 lockoutTime=0 放行，已提供的密码到期计算值也须有效；按稳定组 anchor 查询 AD 嵌套链及主组。目录超时、referral 或不完整读取拒绝准入，不将故障当作人员删除。

`proxyAddresses` 仅作为内部 SMTP 地址事实读取，不推断所有地址归主箱，也不公开为公司通讯录。两类非登录 AD 对象及离线安装器已接入正式引导，门户建号、系统主址、别名和附加箱共用占用；邮件端已接入只读绑定及后台投影，管理员移交仍待完成。身份读取器不写目录数据；`identity/addresses.go` 使用独立受限身份维护地址，不改写系统 `mail`。

`identity/mailbox.go` 新增按人员 anchor 和独立 mailbox_id 读取单箱配置的库方法。固定 OU 的单层、限量查询核对类、唯一 ID、owner、确定 DN、默认地址、配置版本、操作及状态；地址只取该箱记录，额外核查默认地址是否被其他箱占用。有效地址必须同时出现在当前人员 mail/SMTP proxy 中；主箱默认地址必须等于人员 mail。未关联 proxy 不会自动归箱；有效地址另按最多 64 个一批核对全域既有 mail/SMTP proxy，包括无锚点和非人员对象，发现其他归属即拒绝，查询不完整不视为无冲突。此处使用 critical `LDAP_SERVER_DOMAIN_SCOPE_OID`，把查询限定在已配置域的单一命名上下文，避免 DNS 等其他分区的 continuation reference；不支持控制或仍有引用时拒绝，不跟随外部地址。控制语义见 [Microsoft 文档](https://learn.microsoft.com/en-us/previous-versions/windows/desktop/ldap/ldap-server-domain-scope-oid)。pending、disabled、retained 保留为配置事实，停用箱的有效接收地址不会被改为停用。受管占用沿用 `directorymail.Address` 的现有 ASCII 地址格式；其他合法 SMTP 字符串只作为目录事实，不自动成为受管地址。

资源读取后再次检查人员/组准入，最后回读箱版本及字段；不一致返回固定配置错误，目录读取故障/referral 返回不可用，不缓存上次结果。协议 API 已用此方法核对当前 AD 与已确认投影；方法本身不签发读信/发信权限，也不把 AD active 当成 DMS/云端已就绪；它不是跨对象 LDAP 事务或完整撤权实现。

## 内部 HTTP 登录与退出

`service/login.go` 接线 `GET /login`、`GET /oidc/callback` 和 `GET /api/me`。回调先原子消费流程，再验证 IAM 和实时 AD 准入，持久会话最长为 2 小时且不超过已验证 ID token 的有效期；不保存 IAM token 或人员密码。两种 Cookie 均为 `__Host-`、Secure、HttpOnly、Path=/、SameSite=Lax，不设置 Domain。重复 query/Cookie、错误浏览器、回调重放及 issuer 混用被拒绝。Host 必须等于配置的固定 HTTPS 地址，不从转发头构造跳转。

`/api/me` 仅返回本人 anchor、用户名、显示名和主地址，每次先读 PG，再读实时目录，最后复查会话；不返回个人别名、其他箱、组 DN 或服务凭据。权威准入拒绝调用 `DenyPerson` 持久撤销本地旧会话，保持箱和地址不变；目录故障返回 503，不记录删除。

`POST /api/session/revoke` 要求固定 Origin 并只声明本地退出，数据库失败不得返回成功。`POST /oidc/backchannel-logout` 校验 RS256、issuer/audience/azp、jti、目标、事件、时间及禁止 nonce；通知在 PG 中登记后再确认，重放幂等，sid/subject 同时提供时须同时匹配，不依赖 LDAP。v4 退出记录与会话创建共享 issuer 事务锁，阻止“通知先到、回调后到”及并发复活。subject-only 通知拒绝该时间及以前的认证，新的 IAM 认证仍可使用。退出记录尚无自动清理，维护策略须在正式上线前完成。此处不代表应用发起 IAM 退出或 DMS/Roundcube 踢线已实现。

仅供隔离开发的 `MAIL_IDENTITY_CONFIG` 指向非公开普通 JSON 文件（0400/0600、无组/其他权限，最大 64 KiB）。包含固定 `URL`、`OIDC`（Issuer/ClientID/Callback）、`Directory`（只读 LDAPS 绑定、人员边界、邮件域、允许组的 DN/anchor）及 `CAFile`；回调必须为 URL + `/oidc/callback`。秘密不回显，未知字段、尾随 JSON、缺失/不可信 CA 或无效绑定拒绝启动，不回退匿名访问。运行容器 UID 须能读取该只读文件。当前 Hook/Compose 不生成、挂载或公开该开发绑定，不能把它当作已完成的正式 Secret/最小权限契约。

## 单次邮箱交接的内部持久层

v5 在邮箱本地投影增加 `authorization_epoch`，追加 `webmail_handoff`，不安装 AD 字段或改变人员登录。`IssueHandoff` 从已认证人员 Cookie 查实际会话，验证 enabled 人员、同人归属、active 邮箱及默认地址，签发最长 30 秒且不超过人员会话到期的随机码。数据库只存交接码和 Roundcube 浏览器状态的 SHA-256；持有内部码不等于 IMAP/SMTP 凭据。

`RedeemHandoff` 核验码、人员会话 ID、指定 mailbox_id 和目标浏览器状态，一次 DELETE 消费，再检查当前会话、人员、地址、邮箱及授权版本。错误绑定不消费正确流程；正确绑定但已失效的码消费后拒绝，不能等待重新准入而恢复。并发兑换仅一个成功，独立 Store 可续接，无内存授权回退。返回的是内部标识，后续凭据签发仍须事务中重查，不得把返回结构当可复用的协议授权。

`WithholdMailbox` 只关闭本地登录/发送就绪并递增授权版本，重复调用幂等；保留地址和邮件，不写 AD，也不关闭另一箱。未来确认目录和下游后重新启用时保留这个版本，旧交接码不能复活。正式协调器仅在新鲜 AD 与 DMS 实际回读均通过后确认投影。

交接方法只供可信服务端调用；内部 HTTP 接线如下，Roundcube 插件及正式服务 Secret 交付已接线；短期协议凭据见下节。码通过 POST 正文交付，不进入 URL、日志或浏览器持久存储。此处的数据库/HTTP 验证不代替真实 Roundcube/DMS 无感进入验收。测试为 `TestPostgreSQLHandoffBindings`、`TestPostgreSQLConcurrentHandoffRedemption`、`TestPostgreSQLHandoffRevocation`、`TestPostgreSQLMailboxHandoffIsolation`；复用显式授权的 0600 PG 测试配置，缺失时跳过，不冒充实机通过。

## 内部 HTTP 邮箱交接

`MAIL_IDENTITY_CONFIG` 可选 `Webmail` 对象含固定 HTTPS `URL`（无路径/query、与 Go 不同 host）及 `ServiceToken`（随机 32 字节的小写 hex，存于同一私有文件）。未配置时两条交接路径返回 404；Hook/Compose 仍不交付这一开发绑定。没有新增库、常驻服务或 AD 写权限。

`POST /api/webmail/handoff` 只接受 Go 固定 Origin、唯一人员 Cookie 及 form 正文的 mailbox_id/browser_state/issued_at/signature，不接受 Authorization、query、重复或额外字段。Roundcube 插件在其服务端浏览器会话中生成并保存 32 字节随机状态（43 字符无 padding base64url），以解码后的 ServiceToken 作 HMAC-SHA256 key，签名 UTF-8 `anas-mail-webmail-handoff-v1\n<mailbox_id>\n<browser_state>\n<issued_at>`，没有结尾换行；issued_at 为十进制 Unix 秒，最多 60 秒前、允许最多 5 秒未来偏差，signature 为小写 hex。签名只证明挑战来源，不授予人员身份。Go 检查当前会话/issuer、实时 AD 后由 store 重新检查箱归属与就绪；返回码、真实 session_id/mailbox_id、expires_at 及固定 Roundcube POST 目的地 `/?_task=login&_action=plugin.anas_login`，不使用浏览器提供的回跳地址。

`POST /internal/webmail/redeem` 在解析正文/访问数据库前要求唯一 `Authorization: Bearer <ServiceToken>`，拒绝浏览器 Cookie/Origin；只接受 form 正文的 code/session_id/mailbox_id/browser_state。正确绑定的码消费后重新读目录，最后 `CheckMailboxGrant` 检查当前人员会话、issuer、归属、箱/默认地址及授权版本，阻止 LDAP 读取期间发生的登出或停用。目录故障返回 503，已消费码不复活，需要重新交接；目录准入拒绝持久撤销人员会话。未启用 Protocol 时仅返回内部标识。启用后要求内部 TLS，核对当前 AD 与已确认投影，再在事务中重新检查授权并签发单箱短期凭据；不返回 AD 密码或服务 Secret。

所有错误为固定机器码；正文最大 2 KiB，响应 no-store/no-referrer，固定 Host 检查继续适用。Go 浏览器半端和 Roundcube 插件已加入正式 Compose，实际浏览器验收仍须通过，不能仅以接口存在宣称 R-008/R-009 已完成。

`POST /api/webmail/start` 接受当前 Go Origin、人员 Cookie 和单个 mailbox_id，实时核对归属/目录/投影后返回固定 Roundcube begin 目的地及签名。开始签名的原文为 `anas-mail-webmail-start-v1\n<mailbox_id>\n<start_nonce>\n<issued_at>`，字段格式和时间窗口与挑战相同，nonce 为 32 字节随机值。Roundcube 验证后销毁旧浏览器会话并更换 ID，保存自己生成的 browser_state，自动 POST Go `/webmail/continue`；Go 验证固定 Roundcube Origin、签名及当前人员 Cookie，签发一次码后自动 POST 回插件。所有授权码只在正文出现，不进入 URL 或浏览器存储。Go 与 Webmail 正式地址须位于同一站点，使 Lax Cookie 可用于该 HTTPS POST 往返。

`roundcube/plugins/anas_mail` 是固定 1.7.5 的薄适配。SQL 用户名使用随机 roundcube_key，IMAP `storage_connect` 钩子使用独立 protocol_login，保证偏好/私人通讯录和实际邮件存储各按邮箱隔离。插件逐次请求 Go session 接口检查当前目录/会话，剩余少于 30 秒时轮换；只将加密的 SMTP/刷新值及加密 IMAP 密码写入服务端会话。固定 256 个文件锁桶串行化同一会话，取得锁后从 SQL 重读完整快照，缺失的已认证会话不得复活；提交会话后释放锁。新挑战清除旧会话，旧窗口不能借陈旧草稿切换到新箱。发件身份仅同步当前箱有效地址，邮箱地址不能在 Roundcube 自行增删/修改。依赖故障不回退为人员密码。原生组合验证正在补齐，实际 IAM/AD 与正式发布仍为独立验收。

`roundcube/config.php` 读取固定私有 0600 JSON，复用现有 CA，强制 PostgreSQL、TLS、服务秘密分离和 Host Cookie。Roundcube 不持有 AD 或上游代发服务凭据，默认皮肤为 Elastic。原生 Docker entrypoint 加载该配置；只使用固定基础镜像与内置插件，无额外 Composer 下载。

## 单箱协议凭据与逐封发件授权

v7 仅保存 IMAP、SMTP、刷新凭据的 SHA-256，分别使用不同的 256 位随机值。IMAP/SMTP 有效期最多 90 秒，刷新最多 15 分钟，均不超过人员会话期限。签发、查询和刷新要求人员 enabled、有效会话、单箱 active、当前授权版本，以及已确认的同版本配置投影。刷新只允许一个并发请求成功，事务提交即使旧代 IMAP/SMTP 凭据失效；重放旧刷新值不撤销已成功的新代。人员或邮箱重新启用不会恢复旧代许可。凭据只交给已认证的 Roundcube 服务端，浏览器不接触它们。

开发绑定可显式增加 Protocol（InternalURL、CertFile、KeyFile、DovecotToken、SenderToken、SendPerMinute）。内部 URL 必须是同一固定主机的 HTTPS 8443，三个服务秘密各不相同，限速必须显式给出。服务使用现有证书/信任设施，不创建第二套 CA。公开 8080 监听始终拒绝 `/internal`；内部监听要求 TLS 和固定 Host，拒绝 Cookie/Origin，认证后才读取正文。正式 Hook/Compose 生成并交付这些私有绑定；协议许可仍要求实时人员、单箱和已确认投影。

Dovecot 使用独立 Basic 服务身份调用 `POST /internal/protocol/introspect`，通过唯一 X-Dovecot-Auth-Protocol 区分 imap/smtp。Go 先检查凭据，再读取 AD 单箱事实、核对已确认投影摘要和授权版本，最后重查本地会话与凭据。SMTP 返回该代专用提交身份；不能把 IMAP、刷新或其他箱凭据作为 SMTP 凭据。权威 AD 准入拒绝持久撤销人员会话，AD 故障返回 503，不误登记删除。Roundcube 的 `/internal/webmail/refresh` 同样先核对新鲜目录和投影，再原子轮换。

`POST /internal/protocol/sender` 供 DMS 每次提交调用，要求独立 sender 服务身份、一个信封发件地址、恰好一个可见 From、DMS 队列 ID，以及 Rspamd 为本次扫描产生的随机请求标识。两个地址都须属于该凭据当前箱的有效地址。目录核验后在同一事务重新检查授权、记录授权并执行每箱滚动 60 秒限速；同一请求标识只允许完全相同的凭据、队列、箱、会话及发件地址重试，其他情况拒绝。新扫描即使使用相同队列 ID 也重新计数。限速返回 429，依赖故障返回 503，DMS 适配必须临时拒绝而不能放行。SMTP 重新提交会产生新扫描，可能再次计数；授权成功不证明 SMTP 最终接受。

v25 保留 v24 历史授权记录以继续限速，但不为历史记录伪造请求标识。队列 ID 可被复用，参见 [Postfix postqueue](https://www.postfix.org/postqueue.1.html)；随机标识只用于本次扫描的授权请求去重，不绑定收件人或内容，也不能作为逐封状态或投递证明。新旧 DMS 适配器与 Go 协议必须配套更新；旧适配器缺少标识时拒绝提交。真实固定镜像兼容性、完整 SMTP 及迁移仍须实际验收。

## 自动目录配置协调

可选 DMS 绑定启用后台协调器，定期读取受限邮箱 OU、核对已知人员准入并同步 AD→PG→DMS。读取收件配置的 `MailboxConfiguration` 仍验证人员 anchor、正常人员对象、所有权/版本/占用/默认地址，但不以停用、过期或退组删除收件路由；认证始终使用严格 `Mailbox`。准入组先按不可变 anchor 在本域唯一定位，再于当前 DN 验证嵌套或主组成员，改组名不改变身份，重复 anchor 或异域结果拒绝。

每次应用先保存 PG 意图，DMS 实际回读与新鲜 AD 摘要一致才确认。已经确认的配置漂移会撤回本地许可并提升授权版本；回读失败可返回已知旧修订，但必须 Applied=false，使更新的 AD 配置能以精确 CAS 恢复。损坏到无法识别版本时不能强制覆盖。目录读取失败不造资源、不永久登记删除；明确不存在/无效的旧资源只关闭使用，不删除历史邮件或地址保留。全局就绪仍不开放。

`primaryGroupID` 单独变更也触发准入和公司组关系刷新。Samba Hook 在保留自定义过滤的同时补齐该字段，生产者只发布属性名；消费者在刷新与快照保存成功后才提交游标。此补正已有生产者/消费者回归，真实事件到多端收敛仍需单独实测。

可选 DirectoryEvents 绑定启用固定只读 `/run/anas/directory-events/events.jsonl` 消费，v12 保存本 Module 的独立序列游标。每秒观察文件变化，合并突发事件后进行同一权威全量协调，正常兜底间隔 15 秒；失败保留游标、至少间隔 5 秒重试，撤权工作循环独立运行。轮换文件 `.1` 与当前文件一起读取；重复、缺口、生产者序列重置和消费者重启均处理。不完整尾行不提交；损坏/未知结构不推进，并继续定期权威核对。只在刷新成功或明确无关后比较保存游标，陈旧消费者不能覆盖进度。已接入邮箱/准入及公司目录刷新，正式 Compose 只读挂载事件日志；真实轮换/改名及 60 秒最终状态仍待验收。

Roundcube CLI 初始化/升级数据库时不注册浏览器钩子；HTTP 请求仍全部校验。不能因启动了 Apache 就声称数据库可用，原生夹具另核对 session/users/identities/contacts 表已建立。

## 公司通讯录候选

可选 CompanyDirectory 与固定业务 GroupsDN 启用 company 只读来源。读取人员 OU 与业务组 OU，每页 250 个、每类最多 5000 个对象，按 anchor 验证唯一性；公开人员仅包含启用且未过期的正常人员、主地址及指定联系方式。服务/系统身份不公开。直接 member 和主组 SID 构成直接关系，递归组关系在有界图中展开并处理循环；不把组自动生成为可投递地址。源返回 LDAP 错误、引用、缺失分页确认或分段 member 范围时拒绝发布部分结果；当前尚不支持超过该成员范围的组，不能据此宣称任意目录规模通过。

v13 在 PG 中原子保存一个完整、仅含公开字段的快照与指纹。AD 读取或写入失败保留旧快照；事件游标须等邮箱与公司目录两侧刷新成功后才能提交。原始 DN、SID、proxyAddresses、附加箱地址及服务秘密不进入快照。来源 API 使用固定 TLS 服务身份及当前单箱刷新许可，重新核验 AD/会话；只支持 sources/search/groups/contact，source 必须是 company，没有共享来源或写操作。搜索分页、组筛选与 ID 查询只查公开快照，不把浏览器自报的 owner/组名用于授权；超过 60 秒未成功刷新的数据拒绝查询，不无限使用旧目录。

Roundcube 的 anas_company 后端只读查询该 API，并保留原有 SQL 私人联系人；发件人补全同时查询本人当前箱私人联系人及公司来源。界面只列人员主地址，组仅作筛选。可选 language 绑定映射八种 ANAS 语言到 Roundcube locale；company_directory 开关只由部署绑定交付。当前有单元与隔离 PG/固定 Roundcube 测试接线，真实 Samba 来源、完整公司目录和正式引导仍待验收。

## DMS 固定操作适配

`dms/` 是 DMS 容器内的受限控制进程，基于固定 DMS 16.0.1 镜像，不新增独立服务或 Docker socket。TLS 9443 仅接受固定 Host、独立 Bearer 服务秘密及无 Cookie/Origin 的内部请求；接口只有应用单箱投影、回读配置和按真实人员会话 ID 踢线，不接受命令或路径。该镜像已加入正式 Compose，公网收发和完整恢复仍须单独验收。

应用前核对目录快照摘要、修订前提和不可更改的 owner/协议/存储/Roundcube 键；先 fsync 保存完整意图，再原子替换 DMS 账号、别名和容量输入。崩溃或加载失败保留 unapplied，原请求可恢复。默认地址和有效别名指向随机内部账号，目录停用箱仍有收件路由，retained 地址不再生成路由。账号保存不可恢复的随机密码摘要，Dovecot 只开放 OAuth 机制，没有向人员签发另一份密码。容量写入账号 userdb 字段并显式设置零宽限，清空 DMS quota 文件以避免其自动追加 10% 宽限；实际回读同时核对容量与零宽限。

首次启动必须先提交一个真实邮箱，DMS 才能完成初始化。适配器在原生校验点有效且 changedetector 为 RUNNING 前，只允许保存首个邮箱意图及重试同一修订；不添加其他邮箱或替换修订，也不返回已应用。这样避免 DMS 加载早期账号配置却记录后续配置的校验值，永久漏载其余账号。同修订重试仍能修复中断的文件写入；prepare 还会在 supervisord 启动前从已保存意图恢复完整配置，避免既有多箱配置写入中断后再次发生初始化漏载。监测器运行后继续同步并执行原有实际回读。普通容器重启保留原生校验点，遵循 DMS 跳过初始化的行为，不删除该文件。

changedetector 状态命令单独允许最多 10 秒，并服从调用方更短的截止时间；其他 DMS 固定命令仍保持原有 3 秒上限。私有 DMS 接口在该状态命令超时时返回 `native-initialization-timeout` 检查原因；仍报告未确认，不推断监测器已停止或启动。其他启动检查失败继续使用 `native-initialization`，不返回命令输出。

适配通过固定 `doveconf`、`doveadm user`、`postmap` 命令和不执行 DATA 的本地 SMTP 收件人检查核对实际配置，未确认时返回 503，并给出固定检查环节；磁盘中的成功标记本身不算当前就绪。输入仍按完整状态检查，实际协议探测只针对所选箱。SMTP 探测允许 Postscreen 的首次延迟欢迎语，期限 12 秒，固定客户端上限 20 秒；各调用方仍可施加更短的撤权任务期限。命令不经过 shell，路径和过滤字段固定。人员会话踢线使用 `user_anas_sid`；单箱踢线使用 `user_anas_context`（会话 UUID + 点号 + 邮箱 UUID），均只执行固定 `doveadm kick -f`，不以邮箱名或全员踢线代替。当前单进程串行控制，原生 DMS 配置检测器消费生成输入；持久撤权工作循环已接线，正式目录协调器已接线，完整撤权和恢复验收仍待完成。

启动前固定 prepare 动作生成 Dovecot OAuth/零认证缓存、双向会话字段和 Rspamd 提交适配，复用现有 CA 信任。Rspamd 每封已认证提交请求 Go；失败或限速临时拒绝，403 拒绝发件，204 继续正常垃圾邮件检查，未添加测试用的 accept 覆盖。API 行为依据 [DMS 16.0.1 账号处理](https://github.com/docker-mailserver/docker-mailserver/blob/v16.0.1/target/scripts/helpers/accounts.sh)、[配置检测](https://github.com/docker-mailserver/docker-mailserver/blob/v16.0.1/target/scripts/helpers/change-detection.sh)和 [Rspamd HTTP API](https://docs.rspamd.com/lua/rspamd_http/)，兼容性必须由固定镜像实测确认。

## 持久撤权任务

v9 用同一数据库事务内的触发器，将会话撤销和邮箱授权版本变化登记为撤权任务；过期会话也由独立清理步骤撤销。会话任务停止该会话全部箱，单箱任务只停止该会话对应的箱。任务保存目标、修订、尝试次数、下次时间和 20 秒租约；多个工作者以 SKIP LOCKED 竞争领取，完成必须匹配当前修订和租约。后来的目录变更会重新排队，旧工作结果不能清掉新任务。失败以有限退避重试，最大间隔 60 秒，既不伪报完成，也不依赖内存队列。

`service/revocation.go` 的工作循环只依赖 PG 和固定 DMS 客户端，不调用 LDAP。开发绑定可增加 DMS（固定 HTTPS 9443 URL 和独立 ServiceToken），沿用 CAFile 的 TLS 信任并拒绝跳转；服务秘密不能与 Dovecot、sender 或 Roundcube 共用。数据库失败不能发送未登记任务；DMS 成功但完成记录失败仍保留重试，固定踢线幂等。该循环与新鲜 AD 读取/目录配置循环分别执行，目录故障不阻塞已有任务。新登录、协议认证和 SMTP 提交仍遵循已有失败关闭规则。

触发器、租约并发/崩溃、精确单箱和后续修订由显式独立的 `MAIL_REVOCATION_TEST_CONFIG` 数据库验证；没有此配置时跳过，不标实机通过。恢复后的全局会话失效、实际已有连接踢出以及 60 秒传播目标仍须完整 Module 验收。

## 运行、身份与 Secret

可选身份/协议绑定下增加 Go 管理首页与 `/api/mailboxes`。服务端检查当前人员会话、实时目录准入，并仅读取本人资源；页面显示主箱/附加箱、当前有效地址和已确认/处理中/停用状态。未确认投影不能打开，停用显示继续收件；返回数据不含随机协议、存储或 Roundcube 内部键。页面提供邮件内地址管理和 Webmail 交接，认证与运行投影继续用只读 AD 身份，地址写入另用受限身份。界面包含八种语言及系统暗色外观，默认语言由可选 Language 绑定指定；PortalURL 是固定 HTTPS Origin。正式 binding 已接线，Tutorial、主题与完整原生浏览器验收尚待完成。

统一退出候选使用 `/api/session/logout`，先提交本地撤销并清除人员 Cookie，再创建独立 purpose=logout 的有界持久状态（v11），跳转经过同源 discovery 验证的 end_session_endpoint。回跳必须绑定独立 HttpOnly Cookie、一次状态及固定注册地址；不能把登录状态用于退出或反过来。IAM 不可用不撤回本地退出，也不报告统一退出成功。Roundcube 核对 Core CSRF 后先销毁其会话，再通过服务端刷新值终止对应 Go 人员会话；该终止接口不查 LDAP，允许过期/已轮换代值，仅对原会话幂等撤销。返回的短期签名与当前 Go Cookie 对应的同 issuer/session 匹配后，浏览器才能继续 IAM 退出；不能退出同人其他独立登录。后台 IAM 签名通知继续沿用原有持久撤销路径。此接线尚需实际两家 IAM 和固定 Roundcube 的退出验收，不能据单元测试判 R-024 通过。

当前 Hook 声明 calculate/validate/render_env/runtime_restore，要求已准备的 Samba 邮件资源/应用组、通用 OIDC 和 Go/Webmail 两份 PostgreSQL Resource。calculate 登记 public PKCE 客户端，在既有 Secret Store 保存五个稳定且不同的服务/加密键；render_env/runtime_restore 生成三个私有 runtime JSON，不回显到普通 Env。Go 的认证与运行投影获得 AD reader，地址管理另获独立受限 address_writer，DMS 获得固定协议校验凭据，Roundcube 获得交接凭据和自己的数据库连接，不交付 creator、域管理员或人员密码；address_writer 仅在 Go 私有 identity binding 中交付。Root 负责冻结、私有文件和 Resource 生命周期；Hook 不执行 Docker、SQL 或远程命令。Go 读取配置/TLS 后降到 UID/GID 65532，根文件系统只读，没有宿主发布端口。隔离环境已通过正式 apply、三个服务健康和 Go 降权；能力列表、双 Provider、云接入及完整恢复仍待实测。

`/healthz` 仅检查基础服务和数据库；`/readyz` 始终返回 503。未配置内部身份绑定时，其他路径均为 404。容器基础健康不等于完整邮件就绪。数据库初始化失败不会打开 HTTP 监听，错误日志不输出连接串或密码。没有本地管理员、静态凭据回退或开放的邮箱操作 API。

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_mail` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mail:0.1.0-r1` | `proxy, database, mail` | 6 |
| `anas_mail_dms` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mail-dms:0.1.0-r1` | `mail` | 6 |
| `anas_mail_webmail` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mail-webmail:0.1.0-r1` | `proxy, mail, webmail_database` | 3 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `mail.db_type` | enum (`postgres`) | — | `postgres` | `static` | `MAIL_DB_TYPE` | 否 | 否 | 否 | 是 | `container_recreate` | 使用受管 PostgreSQL Resource；接口固定。 |
| `mail.domain_prefix` | string | — | `mail` | `static` | `MAIL_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | Go 邮件管理页的 HTTPS 子域前缀 |
| `mail.iam_protocol` | enum (`oidc`) | — | `oidc` | `static` | `MAIL_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | 使用通用 OIDC 绑定，支持的两个 Provider 仍须分别实测 |
| `mail.ingress_capacity_bytes` | int | `0..9007199254740991` | `0` | `static` | `MAIL_INGRESS_CAPACITY_BYTES` | 否 | 否 | 否 | 是 | `container_recreate` | 全部 active/draining 来源的显式云暂存总预算；0 只允许未绑定来源，不自动购额或删除邮件。 |
| `mail.ingress_credentials` | string | — | `""` | `static` | `MAIL_INGRESS_CREDENTIALS` | 否 | 否 | 是 | 是 | `container_recreate` | 按来源 UUID 索引的 Reader/Control/通知/路由与代理秘密，仅进入 Go 私有绑定。 |
| `mail.ingress_sources` | string | — | `""` | `static` | `MAIL_INGRESS_SOURCES` | 否 | 否 | 否 | 是 | `container_recreate` | 公开来源 JSON：UUID/代次、域、容量、留存及可选 Cloudflare 规则绑定；不含密钥。 |
| `mail.language` | string | — | — | `inherited` | `MAIL_LANGUAGE` | 否 | 是 | 否 | 是 | `container_recreate` | 邮件界面八语言默认值，继承全局设置后映射至 Go 与 Roundcube |
| `mail.outbound_passwords` | string | — | `""` | `static` | `MAIL_OUTBOUND_PASSWORDS` | 否 | 否 | 是 | 是 | `container_recreate` | 按服务 ID 关联的 SMTP 密码 JSON，仅交付 DMS 私有运行绑定。 |
| `mail.outbound_routing` | string | — | `""` | `static` | `MAIL_OUTBOUND_ROUTING` | 否 | 否 | 否 | 是 | `container_recreate` | 默认 SMTP 服务及按发件域覆盖的 JSON；密码单独输入。 |
| `mail.service_api_credentials_dir` | string | — | `""` | `static` | `MAIL_SERVICE_API_CREDENTIALS_DIR` | 否 | 否 | 否 | 是 | `container_recreate` | anasd 交付令牌的专用只读挂载目录。 |
| `mail.service_api_socket_dir` | string | — | `""` | `static` | `MAIL_SERVICE_API_SOCKET_DIR` | 否 | 否 | 否 | 是 | `container_recreate` | 可选的 anasd 邮件安装套接字专用目录，只读挂载。 |
| `mail.service_api_url` | string | — | `""` | `static` | `MAIL_SERVICE_API_URL` | 否 | 否 | 否 | 是 | `container_recreate` | 邮件安装器访问 anasd 的可选直连 HTTPS 地址。 |
| `mail.service_api_workspace` | string | — | `""` | `static` | `MAIL_SERVICE_API_WORKSPACE` | 否 | 否 | 否 | 是 | `container_recreate` | 邮件安装受限授权对应的工作区 ID。 |
| `mail.webmail_domain_prefix` | string | — | `webmail` | `static` | `MAIL_WEBMAIL_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | Roundcube Webmail 的独立 HTTPS 子域前缀，与管理页共用站点但隔离浏览器会话 |

## 验证与保存

云导入专用 SMTP 服务固定使用展开后的原生过滤器列表，并设置 `milter_default_action=tempfail`、清空 `receive_override_options`。提交及观察前回读专用服务的过滤器、故障策略和 cleanup 绑定；列表为空、过滤器自行覆盖故障策略、禁用过滤、配置漂移或读取失败均拒绝确认。过滤器连接失败产生临时 SMTP 拒绝，不能绕过扫描继续投递。此保护不等于完整内容绑定，也不解决现有 Sieve 丢弃回执缺口。

真实 PostgreSQL 重启用例分两个进程执行：第一阶段保留合成账号与许可，第二阶段验证旧许可失效后按该账号的 `owner_anchor` 清理自身记录；清理在测试数据库连接关闭前执行。宿主串行入口随后运行 HTTP/ingress 用例，不应依赖阶段间删除整个 schema。

`MAIL_DMS_MILTER_PROBE=1` 只供固定 DMS 的独立隔离夹具使用。标准库实验代理原有 Rspamd 的 Milter 帧并观察最终 LMTP 字节，覆盖合成普通邮件、折叠头、附件、Sieve 丢弃和 GTUBE 拒绝。它不生成可信回执或云确认；原型完成只表示观察执行完成，完整内容绑定的阻塞及未执行安全项必须另记在计划。此模式不能与公网、其他来源或 Roundcube 组合模式混用，且结束时恢复测试容器配置。

实机运行器按固定仓库摘要核验镜像，兼容经典 Docker 与 containerd 镜像存储的 ID 差异；异常必须记录类型并以非空状态判定失败。 对丢失 RepoDigests 的离线 PostgreSQL 包，只额外接受已经核验的固定 OCI 索引下的 amd64 配置摘要；不放宽为任意镜像 ID。`MAIL_DMS_NATIVE_OUTBOUND_ONLY=1` 须同时启用原生外发，只执行冷启动回读和外发协议组，并要求具体通过标记；结果明确记录未执行 intake 或 Roundcube，不替代完整收件验收。真实 Cloudflare 规则/SMTP 测试仅允许已授权的旧测试域与 `hlong.wang`，使用随机精确地址，核对其他规则不变并清理自身资源。SMTP 来源夹具的 `MAIL_CLOUDFLARE_SSH_TARGET=whl@ln.hlong.wang` 使用 2200 端口，在该主机运行正式取信客户端；发信沿用 finance 上原有测试凭据，只交接无秘密的合成信封。ln 访问 Worker 可经仅绑定回环、限定本轮 Worker 和 API 的 SSH CONNECT 测试代理，TLS 仍由客户端校验。此网络路径及测试结果不自动成为产品配置或完整交付验收。

叠加 `MAIL_DMS_BOUND_COPY_PROBE=1` 可执行已确认的投递副本隔离原型：协商保留头值前导空白，严格比较私有原文、见证 Rspamd 允许的修改、保存过滤副本，并在转交 Dovecot 前替换唯一额外的本机 `Received`。原有头顺序、折叠和正文必须完全匹配；不支持的操作拒绝。它仍是独立实验，不签发产品回执；并发、进程恢复、容量与全部攻击矩阵须分别验收，不能由正常路径推定通过。

原型的 `CopyStore` 使用同一私有目录下的文件锁串行处理投递，默认最多 8 项、每项预留两份邮件及原子写暂存空间；消息上限为 1 MiB。写盘使用独占暂存、文件与目录同步，发送前持久记 `started`，原生成功后记 `completed`。新进程可以读取既有事实；已开始但未完成时保持未知，重复完成不重投，内容或目标不匹配仍拒绝。它不自动清理记录，未做整机断电验证，也不构成产品状态迁移或吞吐保证。受控投递端的多进程及 SIGKILL 测试与真实 Dovecot 恢复验收分开记录。

`go test ./modules/mail/...` 覆盖 ID/主址校验、未实现入口拒绝及错误信息。`MAIL_STORE_TEST_CONFIG` 指向 0600 JSON 文件 `{"dsn":"..."}` 时启用真实模型测试；仅允许数据库名 `anas_mail_model_1025` 、`anas_mail_lifecycle_1025` 或 `anas_mail_cold_1025`，并在已授权的专用 PostgreSQL 上运行。模型检查包含并发迁移/地址竞争、资源稳定性、地址保留与历史迁移漂移；会话检查包含独立令牌、哈希保存、并发撤销、到期、人员状态、重新实例化及数据库故障拒绝。设置 `MAIL_STORE_TEST_UPGRADE=1` 仅执行升级测试时，要求保留的非空模型，`MAIL_STORE_TEST_FROM_VERSION` 显式选择历史版本（默认 1），升级至当前版本后核对标识/地址逐项不变。地址竞争/保留模型用例需要空的专用库，不能在保留验收数据上无条件重复整套测试；故障反例可显式按子用例续测。运行结果由根计划记录，未配置时为跳过。

身份单元测试使用 TLS 模拟 IAM，覆盖签名、issuer/audience/azp、nonce、PKCE、过期/未来时间、重定向、目录边界、账号状态、组归属及读取故障。`TestPostgreSQLLoginFlows` 检查浏览器绑定、一次消费、并发及过期回收。新增 `TestPostgreSQLAdmissionAndLogout`、`TestPostgreSQLLoginLogoutRace` 验证持久撤权和竞争；`service/TestPostgreSQLLoginHTTPProtocol` 在同一显式测试库中使用真实 TLS/OIDC 模拟签发端及 HTTP 处理器，验证回调一次消费、资料授权及 LDAP 故障下退出，不冒充真实 IAM/AD 端到端通过。`MAIL_IDENTITY_TEST_CONFIG` 只用于显式授权的 finance 真实只读 LDAPS 夹具，包含服务绑定、公开 CA、测试人员/组和可选证书名，须 0600 并在测试后删除；不配置时跳过。使用既有服务账号读通 AD 不等于已验证其拒绝写入，也不等于正式邮件绑定已交付。

当前 Compose 候选将 DMS 内容/状态/配置与 Webmail 临时附件保存到 DATA_PATH/mail；Go 与 Roundcube 的数据库由 PostgreSQL Provider 保存，不创建匿名卷。Module 仍为 developing；正式 render/冻结 Hook 和多平台 Module 包已有检查入口；隔离三组件 apply、AD 角色和投影回读已实测；IAM Provider 组合、完整备份/恢复和公网邮件管道仍待验收。

正式绑定预留私有输入路径：Go 可在启动时读取 0600 配置与 TLS 后降为 UID/GID 65532，再开放监听和协调任务；Roundcube 自有 pre-setup 任务将私有输入复制为 www-data 可读的 0600 运行绑定，post-setup 任务必须确认 SQL 表可用。通讯录保留固定镜像的原生 addressbook 任务名称及模板映射，不修改上游文件。这些启动接线已在正式隔离容器执行；finance 已部署候选并验证三个主邮箱、旧址别名及真实登录交接，公网收发和完整一期仍未验收。

正式引导候选支持 `AdmissionGroups` 中 application/all/administrator 的初始固定 DN；v14 在 PG 保存逻辑角色对应的 AD anchor，首次只读查询必须确认全域唯一，事务冲突不得重绑定。已有角色启动时直接使用已存 anchor，不因组改名或同 DN 重建而重新采用新对象。它与显式 `Directory.AllowGroups` 互斥，缺组、歧义、数据库或目录故障均不开放权限。v14 原生迁移已在第 21 轮通过；正式隔离环境已确认三个角色锚点持久保存；组变更与恢复仍须验收。

`TestLiveIsolatedSambaCompanyDirectory` 仅接受显式私有配置与 mailschema.invalid 全新测试域，使用真实受限 reader 经 LDAPS 验证公司来源、主组/嵌套关系、资料/禁用/组改名及隐私字段隔离。`server-mail-bootstrap-runtime-e2e.py` 在强制隔离 daemon 内先对停机目录取快照，再执行候选离线引导与两阶段只读 Go 检查。脚本存在不代表已运行或完整正式部署通过；结果归入根计划。


## 正式部署接线候选

Manifest 现在声明 Go/DMS/Roundcube 三个服务、通用 OIDC、Samba/门户依赖、主库与 Webmail 独立数据库，以及实际管理应用入口。Go 和 Webmail 在同站点的不同 HTTPS 名称；`features.additional_domains: [MAIL_WEBMAIL_DOMAIN]` 由 Root 追加至既有 DNS 发布协议，并参与计算服务域名冲突检查。DMS 仅在 Module 私网通过 mailstore.<BASE_DOMAIN> 互联，没有 NAS 公网 SMTP 端口。内容、DMS 队列/控制数据、配置及 Webmail 临时附件位于 DATA_PATH/mail 的固定目录。

当前候选已通过 Casdoor/LLNG 两套正式 CLI lock/render 与私有配置分离检查；OIDC 注册包含 APP_mail、APP_all 及 provider 管理员组。固定镜像运行层组装、正式 apply 和空账户 DMS 冷启动已实测；完整多阶段镜像构建、受限能力验收与两种真实 IAM 登录组合验证仍待完成；/readyz 仍保持未完成。外部收发、Tutorial、管理员移交及完整恢复仍待实施。上文早期基础阶段的隔离验证不能作为这些新部署绑定的通过结论。

浏览器交接页面和 Go 管理首页使用 `Referrer-Policy: origin`，只发送固定来源而不发送路径或查询；HTML meta 与响应头一致。`no-referrer` 会使跨来源导航 POST 产生 `Origin: null`，导致严格来源校验失败，见 [Fetch 标准](https://fetch.spec.whatwg.org/#append-a-request-origin-header)。接口和错误响应继续使用 no-referrer；接收端不放宽为接受 null。交接码、签名及服务凭据不进入 URL。

Roundcube 发起统一退出时，Go 在同一个已验证 POST 中先持久撤销本地会话、登记退出状态并设置独立 Cookie，然后返回 Go HTML 页面，由带 nonce 的脚本发起已验证 IAM 地址的 GET。该页面禁止表单、使用 no-store/no-referrer，并保留普通链接作为无脚本回退。POST 不直接 303 到 IAM，避免 Chrome 将后续跳转继续按 Roundcube 的 form-action 限制处理。Roundcube 只需信任 Go 来源，不增加 IAM 地址绑定；一次状态和回跳校验保持。浏览器失败证据及重验见根邮件计划，导航规则见 [CSP 标准](https://www.w3.org/TR/CSP/#directive-form-action)。

### 授权初始化与已有撤权任务

启动先读取私有配置、信任链及内部 TLS 身份，并校验固定 DMS 服务绑定。进程降为 UID/GID 65532 后，已有持久撤权任务立即进入独立工作循环；该循环不等待 LDAP 准入组解析或 IAM discovery。在线授权初始化仅对依赖故障每 5 秒重试，每次最多 15 秒；本地配置错误保持失败，不重试为可用。

IAM/目录依赖尚不可用时，不启动登录及协议监听器，不签发凭据，也不报告邮箱就绪；撤权仍可经已验证的内部 TLS DMS 接口执行。恢复后重新完成原有身份验证流程再开放入口，不使用过期人员快照或降低 TLS 校验。配置及私钥只在降权前读取，重试不重新读取私有挂载。

### 停机恢复后的会话隔离（v15）

v15 保存上次邮件启动核对的 PostgreSQL `pg_postmaster_start_time()`。正式 ANAS snapshot/backup restore 先安全停止整个工作区，包括 Go、PostgreSQL、DMS、Roundcube 与共享身份服务，再替换一致恢复集合；Go 在开放任何监听及签发凭据前，于既有迁移事务和排他锁内比较当前数据库进程启动时间。首次升级或启动时间变化会永久撤销全部已有邮件会话，删除未用交接码及登录/登出流程，并通过原触发器持久登记 DMS 撤权任务。旧 Roundcube 会话后续访问、access/refresh 许可仍经过同一已撤销会话核验。仅重启 Go、数据库进程不变时不撤销当前会话；数据库重启后重新启动 Go 会要求重新进入邮箱。邮箱归属、协议/存储标识和历史邮件不变，当前 IAM 与 AD 核验成功后可重新进入。

该机制只针对已确认的停机一致恢复，不承诺在线 SQL 回灌或单应用恢复。事务失败导致启动失败，不能继续开放监听。事务级测试中的旧启动值注入不等于真实恢复通过；正式停机恢复、旧 Cookie/交接码/协议凭据及新登录仍须按 R-037 实机验收。

### 外部 SMTP 配置候选

`smtpconfig` 已实现默认发送服务和按完整发件域覆盖的校验及 Postfix 配置生成。Resend 有固定服务器预设，兼容 SMTP 可使用 STARTTLS 或隐式 TLS；两种模式使用固定的独立 Postfix transport。证书验证强制开启，凭据只写入 DMS 内部的 0600 SASL 文件，不进入公开配置副本、命令或 Roundcube。没有配置外部 SMTP 时，远程投递进入原生 retry transport，不自动直投公网 25。

邮件首页区分本地邮箱配置与公网收发验证：本地投影就绪只显示“本地邮箱可用”。当前完整公网验收尚未接入，登录用户均看到“公网收发尚未通过验证，请联系管理员”。现有 DMS 内部认证接口只读 `postconf` 的默认路由、覆盖映射和 relayhost；确认全部为空且为未配置 retry 时提示未配置，读取失败为未知，其余仅表示路由已读取、不代表可投递。管理员草稿页单独展示这个运行观察并可只读刷新，不覆盖未保存的草稿；普通用户只看粗粒度结果。Roundcube 收件箱提示候选仅在打开主页面时通过已认证会话获取同样的粗粒度状态；获取后重新核对完整邮箱授权、当前目录及会话；目录不可用或授权变化时不返回旧状态，状态未知不能显示正常。此候选尚需真实 Roundcube 浏览器验收；完整配置检查和逐封结果仍待实现。

Roundcube 发信确认候选把原生“发送成功”改为“本地邮件服务器已接收邮件，最终投递结果尚未确认”。它仅替换 SMTP 成功后的确认文案，不更改发送失败或草稿保存提示；不能将本地接收解释为对方服务器接受或收件箱到达，逐封逐收件人状态仍未实现。

邮件首页的地址管理入口指向本服务 `/addresses`；门户链接仅打开账号资料，由邮件服务执行地址权限和目录写入检查。配置草稿新增无需 Cloudflare 的 VPS 直接收件选项；它仅保存计划，不安装或启用。管理员可对输入域名执行有界只读 MX 查询，并临时填写预期主机比较观察结果；服务区分已观察到、未观察到及无法核验，查询后重新检查会话与管理员资格。普通 MX 的每个主机分别查询 A 和 AAAA，逐项报告地址、未查到或未知；部分解析失败不影响其他查询结果，重复主机复用本次观察；超时或临时故障即使同时带有未找到标记也显示未知。唯一的 `MX 0 .` 按 [RFC 7505](https://www.rfc-editor.org/rfc/rfc7505.html) 显示域名明确不接收邮件；空 MX 与其他记录混用或优先级不为 0 时显示配置无效，不将混用记录中的预期主机标为通过。预期主机仅存在于当前页面，不保存到草稿；观察到某条记录不排除其他 MX，也不能证明 25 端口或邮件交付。其他逐步检查及 VPS 正式启用仍未完成。

收件 MX 查询拒绝重复、大小写变体或显式空值造成的歧义请求。收件和发件 DNS 查询返回的域名或预期目标与本次输入不一致时，页面显示 DNS 结果不可用，不保留空白或展示不匹配的观察；输入改变后已完成或正在执行的旧结果仍会丢弃。这些检查不会启用邮件服务。

管理员还可临时输入独立发件域与可选 DKIM 选择器，只读查询该域的 SPF、`_dmarc` 及所选 `_domainkey` TXT，分别显示观察到、未找到、多条或未知；输入和观察不写草稿。查询后重新核对会话与管理员身份，结果不回传 TXT 原文。它只检查精确输入的名称，不验证 SPF 语法、DKIM 公钥、DMARC 策略或签名，SPF 信封域可能不同且 DMARC 可能继承上级域；因此记录存在不能证明认证或投递成功。

管理员还可检查当前页面填写的 SMTP 主机、端口与 STARTTLS 或直接 TLS：从邮件服务所在网络分别解析 IPv4/IPv6，只连接受限数量的公网地址，观察 SMTP 问候并验证 TLS 证书和主机名。探测不发送认证信息、信封或邮件；主机、端口或 TLS 模式变化时页面丢弃旧结果。其只证明当次输入与连接的观察，不验证已部署配置、SMTP 账号、发件域或最终投递；无法连接或验证时显示失败/未知，不能据此启用服务。

Cloudflare 安装候选在发布 Worker 前将随机来源 Reader/Control 凭据同步至进程锁保护的 0600 私有文件，重启时复用；损坏、危险路径或发布意图记录后文件缺失时停止，不自动重建。私有来源文件尚未交接给 ANAS Secret 或运行配置，安装入口、调度、路由和端到端收件仍未完成。真实隔离版本测试和模拟回执丢失的范围见根计划，不代表正式安装可用。

可选通知配置以 NAS 的 HTTPS origin 和独立随机凭据加入 Worker 发布候选，实际通知地址为 origin 加 `/api/ingress/notify`；无通知配置时保持原来的轮询路径。通知凭据和地址随来源配置进行持久恢复，禁止与 Reader/Control 共用，Worker 非秘密绑定与 Secret 类型可独立回读，但提供商元数据不能证明 Secret 值或通知实际到达。VPS 候选来源使用同一签名协议，并将每个目标的通知任务写入来源账本；发送前校验原文并同步意图，最多按首次、1 分钟和 5 分钟尝试，结果未知时保留同一通知 ID 供 NAS 去重。通知结果与 DMS 交付分别保存，延迟通知回执不会覆盖交付结果，通知也不删除原文。候选调度循环可扫描持久任务并在失败后等待，但尚未由正式 VPS 服务启动；未完成任务的目标或密钥变化需要恢复处理，当前不静默切换。真实 SMTP、公网通知和安装仍未接线。

配置草稿的云源通知区域明确标识 HTTPS origin、固定接收路径和 Secret 引用，Worker/R2 与 VPS 均可保存并恢复这些非秘密输入。此步骤不创建 Secret、不发布服务，也不证明通知端能访问或接收协议。

VPS 路由账本候选将来源、允许域、版本、地址绑定及历史关闭序号写入私有目录，并通过文件锁、原子替换和同步持久化；丢失或损坏的状态不会自动重建。重复提交同一操作可恢复，旧版本、重复占用和复用已关闭绑定会失败。候选 `/v1/routes` 处理器区分读写令牌，严格校验请求字段，受控 TLS 测试使用现有 NAS 来源客户端读写；实际 TLS 监听和安装尚未接线。

信封归属预留候选在同一锁事务中为可信收件地址分配序号，记录当时的邮箱、人员、地址占用版本、信封发件人和原文摘要。相同事务重试保留原归属；更改发件人、摘要、大小或目标的重试被拒绝，多个目标中有未知地址时全部拒绝。当前最多 64 笔待完成预留、每笔 100 个地址；活动记录还受 5000 笔和 4 MiB 控制账本上限约束，达到上限拒绝新预留但允许既有事务重试。

原文存储候选在同一来源锁内先同步完整临时原文及目录，再同步待完成信封及其归属，发布原文后把账本状态改为就绪；待完成状态不能读取为已保存邮件。持有服务独占锁的启动恢复会核对已绑定原文的字节数和摘要，并继续原来的归属；缺少原文仍保留待完成记录，损坏、重复文件或权限异常阻止恢复。没有可信账本的孤立原文和临时文件不会自动认领或覆盖，未完成的已绑定同事务临时文件可以重写。孤立文件计入容量，需后续受控核查处理。容量采用原文字节数加 4 MiB 控制区预留，并计入孤立原文、其他临时文件及待完成预留的完整大小；这不是文件系统实际剩余空间保证。原文保存或恢复不代表 DMS 已入箱或通知已送达，尚不能启用为 VPS 收件。

保留清理候选仅处理所有目标均已有最终回执的原文：达到配置保留期后清理；容量不足时先清理最早完全交付的副本，仍不足则拒收。先同步清理意图和逐目标回执证明，再删除原文并完成账本；中断可续行，已清理事务的旧回执仍可幂等重放，证明缺失时不猜测成功。未交付原文不会自动删除。候选清理循环尚未由正式服务启动，回执证明及其索引持续占用容量且有上限；容量报警、真实断电恢复与完整上线配置尚未完成。

VPS 来源协议候选提供受限 `/v1/messages` 列表、按消息及目标获取元数据/原文、逐目标最终回执和通知状态，并沿用读写分离的 `/v1/routes`。列表按序号分页；早于后续项目的待完成信封形成屏障，避免游标越过未完成项目。回执必须匹配来源、目标、DMS 接收键、原文摘要和完成状态，重复回执保留首次结果；回执不会立即删除原文，也不代表 Webhook 已送达。受控 TLS 测试使用现有 NAS 来源客户端，不伪造 DMS 运行结果。该服务尚未正式部署，不能启用或声称真实收发通过。

来源状态、列表、读取及回执遇到短暂账本锁竞争时有限重试锁获取；操作一旦开始执行就不在此层重放。持续竞争仍返回暂时不可用，NAS 保留任务并按既有机制重试。

VPS 来源服务候选 `cloudrelay` 读取当前用户拥有的私有配置文件，检查来源身份、目录及容量配置、独立凭据、证书有效期与证书主机名；只打开已初始化的来源，使用进程锁防止同一来源重复运行。它通过 TLS 提供来源接口，并在同一进程启动留存循环和可选通知循环；通知连接验证系统或显式配置的 CA。私有配置增加可选 `smtp_listen` 和 `smtp_hostname`，两者同时填写时启用 SMTP 监听并检查证书覆盖该主机名；两者均为空时不启动 SMTP。当前测试候选配置必须显式包含这两个字段，与此前仅用于隔离测试的候选配置不兼容。停止时先关闭监听及客户端连接，等待正在处理的 SMTP 正文退出后才释放来源锁。通知已收到响应但账本暂时被其他请求占用时，有限重试写入结果，不重发通知；持续无法提交仍记录为结果未知。该程序尚未由自动安装入口生成配置，也没有真实公网及 DMS 验收；启动成功只证明本地来源进程启动。

候选 `cloudrelay init PRIVATE_CONFIG_PATH` 仅初始化来源，不启动服务或修改 DNS。父目录必须是当前进程持有的私有目录；先在同目录建立私有暂存，初始化空路由与原文存储，写入绑定完整配置的认证摘要，再以不覆盖已有目录的方式发布。重试会核验同一配置和已有状态，保留已有路由及邮件；已完成但未发布的暂存可继续发布，不完整暂存、未知目标或配置漂移拒绝自动覆盖，保留现场供受控恢复。认证摘要不含原始凭据；改变证书或凭据不能通过重复 init 自动迁移，轮换和异常暂存运维尚待实现。此命令不生成凭据、服务单元或配置，不能替代受限自动安装、公开连通及完整收发验收。

VPS 来源交接候选复用现有多来源配置与 Secret 事务：在准备交接时通过证书验证的 HTTPS 分别核验读取令牌、来源身份、存储容量和保留期，再以控制令牌读取来源允许域；可选通知还用一次性随机挑战验证通知目标和密钥一致。挑战证明不能证明通知真正到达 NAS，也不等于 SMTP 或最终入箱通过。控制检查不改路由，读取令牌不能调用控制接口；交接对象不向公开配置输出凭据。候选操作记录用不含明文凭据的认证摘要绑定安装输入，提交前重新核验来源及调用方授权，并在工作区配置锁内审计、固定提交前版本；回执丢失后可核验同一结果，已移除来源不能由旧操作恢复。具体服务端管理员与服务授权、工作区级安装互斥、记录备份恢复和安装接线尚未完成，不能将此候选直接开放为正式安装入口。

SMTP 接收候选使用 `github.com/emersion/go-smtp` 解析协议，间接引入 `go-sasl`；无需自行实现 SMTP 协议。按 SMTP 信封收件人检查路由，完整读取正文后在来源锁内保存当前归属和原文，仅保存成功才允许响应 250；邮件头不决定分箱，未知收件人拒收，路由或存储故障返回临时失败。候选最多同时处理 8 条连接、1 个正文，其他已连接发送方遇到正文忙返回临时失败；支持 STARTTLS，未要求远方服务器必须使用 TLS。原文上限为 25 MiB，协议库 SIZE 通告多 1 字节以处理其边界实现，声明超限或实际超过上限均拒收。上游在收不到最终响应后可能重新投递，不能据此宣称上游重投恰好一次。已测试完整暂存的进程中断恢复；孤立或损坏文件的运维处置、生产安装和全链验收尚未完成，不能用于生产收件。

候选 `/v1/status` 使用读取凭据返回来源身份、逻辑容量和使用量、保留期、待完成数及通知配置状态；账号整体额度仍为未知。容量或待完成上限拒收时同步记录告警，重启后保留；清理或恢复后只有实际重查仍能容纳此前被拒绝的最大原文、并且任务和账本数量未达上限时才清除。未完成通知的凭据绑定变化显示配置错误。该状态不检查物理剩余磁盘、公网 SMTP、通知是否可达或最终交付，认证失败、文件异常或容量无法可靠核对时返回不可用；管理员页面的正式运行和真实告警验收仍待完成。

定时维护协调候选仅在 Worker 身份、当前版本及空计划回读成功时设置每分钟计划，先同步意图；回执丢失保持未知，已确认计划重读而不重写。现有计划、漂移及不完整回读阻止写入；提供商接口按名称替换计划，不能保证阻止其他管理员在检查与写入之间并发更改。隔离云测试已验证设置与回读，以及真实写入后模拟丢回执不重写；尚未接入产品或验证真实触发和清理。

Worker 取信入口协调候选仅对已确认的独立 Worker 构造 `workers.dev` origin：先记录启用意图，回执丢失时保持未知；有明确启用回执但探测失败时只重读而不重写。它检查账号子域、版本、R2、定时计划、禁用预览，验证无凭据请求遭拒且真实来源客户端可使用 Reader 凭据读取匹配的容量、保管和通知配置，之后再次回读云状态。此检查不验证 Control 凭据的实际写操作、外部 SMTP 收件或通知到达；API 按名称操作不能原子排除其他管理员并发更改。隔离云测试使用模拟定时步骤，正式安装入口和完整启用仍待接线。

安装内部协调候选在创建云资源前检查收件域、容量、保留期、通知地址和取信代理参数，再将资源预留、来源凭据、Worker 版本、定时计划和取信入口按顺序串联；恢复时逐项回读，不重新执行已确认步骤，结果未知的写入不重发。对外结果不包含凭据，也不代表邮件已启用。候选配置页已接入准备和 Secret 事务，但尚未完成地址规则发布验收；测试的真实云串联仍使用模拟定时，首次冲突原因尚未定位，不能计完整验收通过。

来源准备前使用独立路由 Token 只读查询一页 Cloudflare 规则；明确的认证拒绝、其他不可确认结果都阻止创建资源。该检查不证明规则写权限、域名归属或实际收信，代理和提供商错误不会回显凭据或响应正文。

管理员的已绑定来源状态另显示 Cloudflare 规则读取结果：有界读取成功、认证失败、不可用或未配置。它不返回规则正文，也不证明写权限、地址路由或投递；显示该观察不改变启用条件。

安装库提供受控的运行配置交接候选：先检查允许域、Cloudflare 区域和独立路由凭据，再准备来源，并生成与现有 `ingress_sources`、`ingress_credentials` 格式一致的公开描述与私有凭据。凭据只能由调用者明确写入私有目标，格式化结果默认隐藏凭据；合并时保留既有及排空中来源，检查域冲突、全部来源的容量和重复身份，完全相同的重试可复用。准备接口本身不写 Secret Store、不证明路由写权限或发布规则；配置页通过后续事务提交，实际部署仍待验收。

`ingress_credentials` 与 `outbound_passwords` 已声明 `store_input`，配置事务可将其作为受管输入保存在 Secret Store；公开配置不含明文。安装库的事务候选可新增来源并保留已提供的活动和排空中来源，在同一事务内核对配置版本和原有私有凭据，再提交来源、总容量和凭据；不同来源或凭据拒绝覆盖，相同重试通过只读验证恢复。原有凭据须与 Secret Store 保存的字节完全一致，无法核对时保守拒绝。替换时可显式指定旧来源进入排空状态，但新来源须覆盖该旧来源的全部域，事务本身不撤销云路由；实际切换仍由运行时检查。配置页已连接候选事务，实际部署和真实收件尚未完成；配置提交不等于服务启用。 发信事务候选由受限宿主读取同一锁内的完整公开路由与受管密码快照，校验归属和配置一致性，再以配置版本和原私有值为条件原子写入完整显式路由及密码；直发的密码映射为空对象，不含 SMTP 凭据。旧式明文、环境覆盖、并发改动、审计失败或结果未知均不自动覆盖或报告成功。默认发信路径的候选合并会保留其他服务与按域覆盖；若原默认服务被域覆盖引用，会为新默认服务分配独立 ID，达到服务数量上限时拒绝变更，不自动删除其他服务。受限宿主处理器候选在读取和提交前分别检查服务授权并写审计，不接受调用方提供完整路由或私有工作区路径。服务 API 使用单独的 `mail.outbound.configure` 授权，不能以收件安装权限代替；当前用户授权由调用方负责。管理员页面候选可在核对当前会话、管理员组和已保存草稿后提交默认服务；密码只随请求交付、提交后清空，未知结果提示先核对且阻止页面直接重试。宿主返回后再次读取草稿及复核权限；若草稿已变化或无法读取，可能已经写入旧配置，因此返回结果未知而非可直接重试的冲突。请求前后复核不构成跨系统原子撤权或草稿与宿主提交的原子事务：提交期间人员撤权及最后一次读取后的并发变更仍可能发生，须继续验证并处理结果。候选尚未部署；提交不检查或处置队列，实际部署仍须执行 DMS 队列保护。

安装协调候选将受控 Worker 准备与上述配置事务串联：先检查容量和排空计划、要求调用方提供云操作审计，再在私有日志目录持久绑定请求，随后准备来源并提交配置。绑定只保存以路由凭据计算的校验值，恢复必须使用相同请求；云操作回执未知时不重发，配置回执丢失后以相同输入只读确认。此接口要求调用方另外完成服务身份和管理员授权，提交事务不意味着邮件路由或服务已启用；候选管理员界面已连接，真实环境仍待验收。

协调器也支持分两次调用：准备云端后，提交前重新检查管理员及服务授权，写入事务时再次检查授权和审计；只读重试同样要求授权。首次提交的基础配置版本在持锁事务写入前持久记录，旧操作不得覆盖后来独立修改的配置。宿主内部可在配置锁下读取邮件模块专用的来源描述和所属 Secret 快照，不提供通用 Secret 读取接口；当新来源已提交时，可从当前快照反推原有来源和显式排空状态，并通过原请求绑定验证，避免在浏览器保存旧凭据。调用方必须实现实时管理员验证及受限服务授权。

anasd 新增受限服务接口候选 `mail.ingress.install`：只接受明确登记的 `mail` 服务身份、工作区及固定 Worker 或 VPS 安装输入，直接 TLS 调用。宿主读取受管 Samba 邮件域策略，不接受调用方扩大域范围；读取现有来源前审计，准备和提交前再次鉴权，云 API 每次请求及配置持锁提交时重验服务 grant。逐工作区安装操作互斥，私有日志不返回凭据。接口的人员锚点仅是服务声明；邮件服务在两次调用前实际检查管理员身份及已保存草稿。日志备份恢复、实际云端操作和端到端收发仍待验收。 管理员安装请求和宿主接口仅接受精确字段名；重复字段及大小写变体在调用安装器前拒绝。

邮件服务的 Worker、已有 VPS 接入和受限 VPS 安装请求必须显式提供非负草稿版本；零是合法版本，但缺失或 null 不等同于零。`ipv4_only` 可以省略，但显式 null 不等同于 false；多个 Content-Type 也在调用宿主安装器前拒绝。已发送的安装操作仍须按原操作记录核对，输入校验不改变结果未知的处理。

管理员配置页可在工作区显式配置 `service_api_url`、`service_api_workspace` 和 `service_api_credentials_dir` 后调用该接口。地址必须是 anasd 直连 TLS origin，而不是 trusted-proxy listener；宿主须给独立邮件服务身份授予该工作区的 `mail.ingress.install` 和 `mail` 资源，并将其专用交付目录只读挂载给 UID 65532。先保存 Worker 收件方案与显式限额，再填写账号、区域、资源名称和操作 ID；准备与提交是两次独立操作，每次重新验证管理员，Secret 不进入草稿、URL、日志或浏览器存储，请求结束后清空输入。保留操作 ID 和非秘密设置以便恢复；结果未知时不能盲目重建。提交仅保存期望配置，不表示已部署、路由已生效或邮件已送达。 如同时设置 `service_api_socket_dir`，邮件容器只读挂载该专用目录，并在该套接字上仍使用配置的 HTTPS 地址验证证书和 Host；套接字异常不会回退到网络。宿主需单独启用 `mail_install_socket` 并准备权限正确的目录。

显式选择 `direct` 时，独立原生 SMTP transport 按收件域 DNS 路由经 25 端口投递，不使用上游凭据。直发使用机会式 TLS（`may`）：对方支持 STARTTLS 时加密，不支持时允许明文，不强制验证对方证书；第三方服务仍要求验证证书。直发可作为默认路径或完整发件域的覆盖；服务商失败时不自动切换。公网可用性须另行检查 IP、PTR、SPF、DKIM、DMARC 和实际投递，草稿状态不代表就绪。

接收配置草稿现在可选直接 SMTP、Worker/R2 或自管云。直接收件草稿不要求云来源 Secret 和云容量，并提示 NAS 离线期间依赖发件方有限重试、不能保证邮件保管或送达；页面不发布 25 端口、修改 MX 或启用直接收件。直接发件说明 IP 信誉可能导致拒收或垃圾邮件，第三方发件也不能保证进入收件箱。直接收件的正式监听、地址校验及真实外网投递仍待实现和验收。

DMS 私有启动绑定新增 `Outbound`。固定启动准备器生成原生 sender-dependent relay、transport 和认证表，沿用 DMS 官方 `postfix-main.cf` 与启动前 `user-patches.sh` 入口，不修改上游源码。路由按 SMTP 信封发件域选择。已持久化的路由公开副本位于 `/var/mail-state/anas-outbound/public.json`；若路由改变且原生队列仍有消息，启动拒绝改路由，须先用旧配置完成队列再切换。凭据轮换不改变路由时允许重启。检查同时覆盖持久 `/var/mail-state/spool-postfix` 与当前 `/var/spool/postfix` 的真实队列目录，未知状态拒绝，不删除或重新提交消息。

路由变更时，两个队列根目录均缺失、已有根目录的关键队列子目录缺失、损坏或指向未声明位置的根符号链接、关键子目录重定向均视为未知，拒绝写入新路由。固定上游镜像首次启动前尚未创建队列目录；仅在无旧路由状态、无持久队列且镜像 spool 存在而各队列子目录均不存在时允许首次初始化。相同路由可重试；初始化不完整时不得更改路由。这种判定不能证明宿主挂载或数据卷身份，恢复验收仍须独立核验。管理员可显式请求 DMS 通过 [Postfix 队列列表](https://www.postfix.org/postqueue.1.html) 做有界只读观察，只返回“观察到邮件 / 未观察到邮件 / 未知”和检查时间，不暴露队列 ID、地址或邮件。列表包含入站邮件，队列变化时可能漏项或重复；未观察到邮件并不保证队列为空，也不授权切换。查看后再次核验管理员与会话；该检查不触发重试、删除或投递。

当前已接通正式参数与私有绑定，原生配置生成和单元检查通过；原生 DMS 两种 TLS 与既有 RC 组合的隔离子集已通过；管理员 Tutorial、服务切换界面及公网完整收发仍待接线与验收。直接 SMTP 探针成功不能替代 Roundcube/DMS 实际发信；服务商 accepted、接收服务器 delivered 与用户可见到信分别记录。

### 管理员配置向导候选（v16）

Cloudflare 安装器新增只读预检候选 `modules/mail/cloudflare/install-preflight.mjs`：使用仅当前用户可读的本机凭据文件检查 Token 状态、Worker 名称及逐页 R2 bucket 冲突。检查只发 GET，写权限、配额与计费仍为未知，结果不代表接入就绪。创建时的并发重名、安装失败恢复和真实云资源验收尚未实现，预检通过不能授权覆盖同名资源。

资源预留候选 `modules/mail/cloudflare/installer` 使用私有追加日志、文件锁和每步持久意图；创建响应丢失时停止而不重试，已确认的 Worker ID 和 bucket 元数据指纹在再次运行时回读。固定 Cloudflare HTTPS 目标的创建/回读客户端与日志引擎已完成真实随机资源预留及清理子集，尚未接入配置页。bucket 元数据只是漂移检查，不是不可变身份；真实云请求中断、生产发布协调和完整收信仍需验证，不能当作自动安装已完成。

固定 Worker 源码的版本协调候选使用不可变 Worker ID 和不同的 Reader/Control Secret，发布前记录意图并检查没有既有部署；隔离真实测试覆盖源码及非秘密绑定、当前生效版本、重启回读和模拟丢回执不重发。该过程尚未接入产品入口与 Secret 持久交接，元数据回读不能证明 Secret 值或实际接收；定时维护和路由启用仍待实现与验收，预检也不等于其他管理员无法并发修改。

`/setup` 与 `GET/PUT /api/setup` 已接入，管理员身份来自持久钉住的 `administrator` 组 anchor 及每次新鲜 AD 成员查询，不按用户名或组标签授予权限。写入要求同源 Origin、严格 JSON、当前有效会话；解析后及响应前再次核对人员/组。v16 单表保存全站非秘密草稿、版本、编辑人员及时间，沿用现有 PostgreSQL 和备份集，不增加服务或 Secret 存储。存储事务锁定人员、会话与管理员组记录，以版本比较拒绝并发覆盖；撤销会话不能继续读写。

草稿写入必须明确提供 revision 和 draft；顶层及草稿、发信设置和限额中的字段名只能精确出现一次，拒绝重复、大小写变体和显式 null。最终复核必须仍是同一人员及钉住的管理员组；如果保存后授权已变化，保存可能已经完成，不以错误响应推断未写入。

草稿接受直接收件或两种云来源、域名、无用户信息/查询/路径的 HTTPS Origin、SMTP 公开端点、`secrets.MAIL_*` 引用和显式限额。选择直接收件时云存储、云容量和通知检查显示不适用，而非通过。真实密码、Token、部署与核验状态没有可写字段；未知字段或非法输入统一拒绝，不回显原值。草稿不会向 DNS、云端、Core 配置或 SMTP 写入；实际凭据继续通过既有 ANAS Secret/参数和 Hook 私有绑定交付。

八语言界面列出账号/资源准备、官方获取指引、2026-10-26 核查的公开套餐、共享额度与计费边界。实际账号额度无法读取时为未知；通知、存取、分箱及 DMS 交付分项显示。当前云验证执行器未完成，因此保存只更新草稿，启用按钮保持禁用；不报告接入就绪，也不冒充完整 R-063—066 通过。浏览器不保存草稿/秘密，陈旧版本冲突保留未保存编辑并要求显式重新读取。

草稿读写请求在浏览器端设十五秒期限。保存后响应丢失或其他未知结果可能已经提交；页面标明未确认，禁止继续保存及基于旧草稿安装，并要求重新读取。明确版本冲突同样要求重新读取；读取本身失败不会解除限制。此约束不自动重发或证明下游配置生效。

若管理员在保存请求进行时继续编辑，成功响应不会覆盖当前输入；页面提示提交的版本已保存、当前修改尚未保存，要求先复制修改并重新读取，再继续保存或安装。

### 云邮件持久交接候选

固定内部 `/v1/intake/{id}` 接收原始 RFC 5322 正文，可信元数据在受控请求头中提交；沿用 9443 TLS 和 Go 服务凭据，不公开 NAS SMTP。来源 UUID、来源消息编号与目标箱生成幂等键，人员/箱必须匹配已确认且实际可用的 DMS 配置。原文最大 25 MiB，与元数据以私有目录原子发布到 `/var/mail-state/anas-control/intake/`，纳入原有 DATA_PATH 恢复集。原文不被改写；发送给本机原生 SMTP 的副本替换保留追踪头，不按 To/Cc/Received 决定归属。

直接收件隔离反例证实，普通 SMTP 可伪造云导入追踪头并误导本机回执。修复候选为普通 SMTP 清除该保留头，云导入使用容器回环上的独立 Postfix SMTP 和 cleanup 服务，并保留其他原生头检查；归档原文不变。由于上游容器可能在启动脚本失败后继续运行，提交及未决回执观察前均核对规则文件和 Postfix 服务配置，无法确认时保持待重试或未知，不报告成功。修复仍需真实 DMS 伪造、原有过滤与恢复回归验收，不能据此开放公网 25；旧版本和测试结果见根计划。

提交前持久记录 submitting。重放必须匹配同一元数据与摘要；prepared/已明确未接受的 retryable 可继续，提交结果未知不再盲目提交。`stored` 需要实际 Dovecot 邮箱中查到私有追踪头；Postfix 队列需回读该头才能报告 `queued`。队列 ID 可复用，日志只用来寻找队列候选，不能单凭同 ID 的日志推断处理或拒收。旧版保存的 `processed` 回执保留原记录，但当前不再用它确认云交付或释放本机原文；对旧版依据日志判断的拒收只重新核对，不重新投递；已经完成的历史云确认和删除不能自动撤销。由于尚无安全关联，实际 Sieve 丢弃或原生拒收且没有邮箱副本的结果暂保持未知，原文保留并待恢复，完整交付验收未通过。API 不提供邮箱删除、任意命令/路径或把未知改成功的接口。

DMS 官方 pre-daemon Hook 追加固定 header_checks 追踪表，保留既有检查；常规原生投递仍执行过滤及硬配额。当前仅候选，云消费账本、源回执和管理核验尚未接线，不能把内部 HTTP 成功当成完整 R-052/R-058 通过。

交接回执以固定失败步骤及 SMTP 状态码区分临时拒绝、永久拒绝和 DATA 确认丢失，不回显服务器原始回复。Postfix 原文上限固定为 26 MiB，配合 25 MiB 输入上限留出传输头空间；仍执行每箱硬配额。

### 云取信客户端与账本候选

`ingress` 使用两种来源共用的 `anas.mail-source/v1` 固定 HTTPS 协议；来源 UUID/代次、受管域、TLS 和私有读信凭据均钉住，不接受来源返回的任意下载 URL。列举、原文及回执都有严格大小/字段限制；原文必须匹配 ETag、长度及已知摘要。禁止跳转及环境隐式代理，可在私有绑定显式设置现有出站代理。仅启用 HTTP/1，验证系统 CA 或明确附加 CA，不关闭证书校验。Cloudflare 管理 Token 不作为读信凭据。

v17 追加接收许可、逐封账本和分页进度三表，复用 PostgreSQL。许可绑定完整地址、AD 配置版本及不可变人员/箱；新许可要求已生效投影。关闭许可保存云确认的最后接受序号，旧积压继续进入原箱，超过边界拒绝，不重开旧许可。消费阶段为 download → native → ack → cleanup → complete，使用有期限的数据库租约；原文先以私有临时文件校验、fsync 和不覆盖发布，再进入 DMS。结果未知只回读；已在 DMS 最终处理但云回执丢失只补回执。原文/任务不依赖人员会话或新的 AD 查询。

云端确认后才持久进入 cleanup。固定 DMS `/v1/intake/{id}/release` 只移除该接收键的冗余原文，要求相同箱/摘要/最终状态及云确认声明，先保存清理意图；回执和真实邮箱内容保留。NAS 再删除自身冗余原文并结束任务。中断可重复清理，queued/unknown 及符号链接不能释放。恢复后的 ack 阶段再次核对当前 DMS；分页到末尾保留尾页起点，后续只重读尾页及新增项目；一致旧备份保留较早游标及逐项任务，云已交付标记本身不能跳过本地恢复。

上述为库及固定接口候选，尚未从 Hook/服务调度器或 Tutorial 启用；正式 Worker/R2、自管云协议、通知、保留/总配额和云路由许可同步仍待实现。受控 HTTPS/数据库与原生 DMS 测试分别记录，不能合称实际云完整验收。

### Worker/R2 候选实现

`cloudflare/worker.mjs` 新增与 Go 共用的协议实现和独立 Reader/Control 凭据。路由使用预期版本及操作 UUID 比较更新，关闭的许可不复活；容量预留与接受序号共用 R2 条件写，上传中的邮件持有接受时的原箱/人员。原文和控制记录持久后才列举；回执只接受匹配接收键、箱、摘要的 stored/processed。

显式总量计入 4 MiB 根记录及每封 4 KiB 元数据预留。已交付原文按 7 天留存或容量压力清理，最早接受的已交付副本优先，足够后停止；不清理未交付完整原文。清理以永久微小占位阻止迟到写入，压缩后的回执/占位继续计 2 KiB；跨对象释放容量有持久日志及 CAS 复核，不能重复减计。上传不完整也先占位后释放原文预算。具体输入和当前限制见 `modules/mail/cloudflare/README.md`。

R2 同一 key 每秒写入限制需要控制文档有界退避；邮件流已消费时不重新发送。单元已涵盖该协议/故障子集，真实 R2 性能、实际 SMTP、通知、Go 调度/部署与完整恢复仍待验，不能标启用。当前通知明确 unconfigured。

### 通知与定点取信候选

Worker 的独立通知组件使用可选 `NOTIFY_ORIGIN`/独立 `NOTIFY_TOKEN`，仅向固定 `/api/ingress/notify` 发送来源、代次、通知/消息/箱 UUID，不含正文、邮件头或人员凭据。HMAC-SHA256 覆盖秒级时间及完整 JSON，HTTP 验证 TLS、禁止重定向、5 秒超时；仅匹配的 200/202 持久接收回执可记 notified。初次、60 秒、300 秒最多三次，发送前先占用尝试；通知未配置、错误或耗尽不影响原文保管和拉取。配置变更按新绑定重置尝试。通知成功与原生交付分开，迟到失败不覆盖后来成功，通知不能回退 delivery。

Go 增加来源/代次/独立密钥校验、±300 秒窗口、严格 4 KiB 请求及常量时间签名比较。v18 追加通知表：先持久幂等提示再返回 202/唤醒；重复同一通知不重开已处理提示，改箱或改正文的同 ID 拒绝。解析提示后通过认证来源的定点元数据接口读取，再按既有许可入逐封账本；提示本身不授予分箱或登录权限。来源已过留存窗口记 expired 告警，不把未找到邮件当完成。源发送端/本机鉴权测试已通过；真实 PG、实际两源通知、服务监听和调度仍待接线或验收。

### 来源配置与持续执行候选

新增 `ingress_sources`（最多八项公开 JSON）与敏感 `ingress_credentials`（按来源 UUID 索引的 Reader/Control/Notification/代理），凭据仅随原有私有身份文件进入 Go。来源须明确 active/draining、UUID/代次、Origin、域、容量及留存；同域不能配置两个 active，密钥不能跨角色/来源或与既有服务凭据共用。来源公开描述、嵌套 Cloudflare 绑定及按来源索引的私有凭据只接受精确字段名，不接受重复键、大小写变体或普通字段的显式 null；可选 Cloudflare 绑定对象仍可为 null。旧的含糊格式必须先核对并规范化，才能重新部署。旧源继续取信不等于云端切换完成，当前路由操作与回读协调仍待实现。

Go 准备固定 `/var/lib/anas-mail-ingress`，绑定 DATA_PATH/mail/ingress，并在 root 引导时将该目录设为 65532:65532/0700，随后沿现有流程降权。只为这个固定目录增加启动 CHOWN；运行进程无 Docker socket。Scratch 镜像复制 Go 构建镜像已有的系统 CA。配置来源后在在线 AD/IAM 初始化之前启动一分钟列举及独立逐项/通知任务恢复；公开通知仍随正常 HTTP 启动，失联不阻止拉取。通知与轮询共享接收键和账本。

源列举先快照接受序号和未完成上传边界，不能让分页跳过更早尚未发布的消息；固定占位或发布完成后再前进。NAS 保留尾页起点作增量读取；旧一致备份保留较早位置和待办，不按云已交付标记跳过。上述接线为候选，实际一分钟/新镜像完整启动、两云源、路由协调及 Tutorial 启用仍待验，readyz 保持未完成。


### 云路由操作与恢复候选（v19）

复用 PostgreSQL 保存来源已确认版本及 prepared/issued/confirmed 路由操作，历史请求改为 superseded 时仍保留识别迟到提交的依据。网络写入前持久化操作与旧版本；响应未知时只回读已登记操作。未知未来版本、倒退或缺失历史均停止，不把云当前版本当作自动覆盖许可。返回已知旧配置时如有未决请求，仍以新 CAS 操作封住该版本，避免旧请求晚到改变路由。

DMS 临时不可用、登录撤权或其他地址修改中，保留当前 AD 快照仍有效的已发布地址；新增地址等待精确 DMS 回读和当前目录配置。云关闭后保存原接收序号边界，旧积压仍投原箱，已关闭许可不能重开或拓宽。重新添加地址使用 AD 新 claim version；切回旧供应商用新的来源实例，旧实例继续排空。首次配置必须对应空来源，不自动认领已有未知云路由。

服务在 DMS 回读、AD 复核之后协调来源，云确认后再次核对 AD 才确认本地投影；相同路由最多一分钟复查一次，配置变化立即协调。旧源 draining 仍需 Control 凭据以撤下接受路由，保留 Reader 处理积压。候选尚待实际 PG/云全链路和恢复验收，Tutorial 尚不允许启用，不能视为 finance 已部署。


若云端清理尚未确认，新的本地接收任务还会核对当前目录投影中的完整地址和 claim version；撤下或 pending 的地址拒绝新增任务。已有任务继续执行，不受临时 AD 故障影响。云关闭已确认时，以永久关闭序号核验旧积压，保持原人员/箱。此保护不依赖人员登录状态。


### 收件运行状态候选

管理员 `GET /api/ingress` 使用持久钉住的管理员组与新鲜 AD 身份，在实际观测前后均检查授权。最多八个来源各在四秒上下文内读取 PG 汇总及认证云 `/v1/status`；只返回本地最近成功取信、待办/错误数量、下一重试时间、过期提示、已确认路由版本，以及云已用/上限、保留天数、压力告警及通知配置。响应不包含凭据、任意原文 URL、邮件/人员标识或内部游标；数据库错误不直接回显。配置量与实际云容量/保留/通知不符显示 configuration-changed，认证与来源身份变更分别显示。未知云路由版本留下 source-state-unrecognized，不把成功读取容量当作路由已确认。

配置页读取上述状态时另设十秒浏览器期限；超时或网络错误清除旧状态卡片、显示读取失败并允许管理员重试。该期限不改变后台来源任务，也不将超时判作来源已停用或邮件丢失。

Tutorial 增加八语言运行状态卡片及手动刷新；未绑定、不可用、已观测和未知账号额度分开显示。通知 configured 仅说明配置存在，不能当 NAS 接收或 DMS 交付成功。当前 `public_ready` 仍为 false，状态读取与保存草稿不启用服务。云路由写入后的独立回读必须仍匹配精确操作/版本/指纹，陈旧回读保持未确认。真实 PG/两云与公开完整收发验收按配对计划继续执行。


### Cloudflare 逐地址规则客户端候选

Email Routing 子域需 literal 逐地址规则，根域 catch-all 不能代替 `mail-poc.bengbu.cc.cd` 的公开规则。新增规则客户端固定 Cloudflare HTTPS API、zone 与 Worker，复用来源的证书验证和明确代理，使用独立 API Token；接口限于读取/建立逐地址 Worker 规则及核对后删除己方规则，不提供 DNS、Worker 部署或计费动作。规则名绑定来源 UUID、代次和完整地址摘要；删除前按 ID 独立读取并比对完整己方配置，外部或漂移规则拒绝。失去提交回执保持未知，不自动盲目重试。

列表分页最大 50 条/页，完整计数与唯一 ID 必须吻合；提供商当前 matcher 地址最长 90 字符。限制与独立权限不替代每地址持久操作和回读协调，该客户端尚未从服务或 Tutorial 启用，也未将当前管理 Token 交给 NAS。Cloudflare API 未承诺跨多个规则的事务；不能宣称对任意外部管理员并发改写具有原子排他。按 R-040/R-048 继续实现受管操作和漂移恢复。


v20 候选为 Cloudflare 每地址保存提供商规则 ID/配置摘要和持久操作。登记要求已知源版本/指纹一致，请求前保存 issued 与时间；两分钟租约排他。撤下地址会取消未发请求并保留已发请求历史，迟到结果仅用于核对配置及后续清理，不能重新授予 Worker 接收许可。该账本尚待实际 PG 和公开规则协调验收，未启用。


公开规则接线候选新增 `ingress_sources` 项的可选 `cloudflare: {zone_id, worker}`，对应敏感 `ingress_credentials` 的独立 `routing` Token，限 Worker/R2 来源；只进入 Go，与 Reader/Control/Notification 及内部服务凭据均不能共用。Worker 缺少公开规则绑定时不确认新的邮箱投影，已有取信任务仍运行。VPS 直接收件不依赖 Cloudflare，在来源地址路由核验后可继续协调；公网 MX、TCP 25 和真实投递仍需另行验证。涉及缺少受管公开规则的来源切换仍失败关闭，不自动撤销旧来源。仅提供规则权限不等于已经允许 DNS、Worker/R2 部署或新订阅。

协调按已确认源许可登记每地址意图，创建与删除均先保存 issued，再调用固定 API 和独立回读；不因丢失回执重复赋予邮箱身份。公开 API 当前以 409/2014 拒绝重复同地址规则，未知创建先回读精确己方对象，缺失后按同地址重试；外部规则或漂移阻断，不覆盖。原源许可已永久关闭时可取消尚未观察到的旧创建，迟到配置仍凭 issued 历史识别并清理。相同配置成功缓存至多一分钟、失败五秒退避，命中不顺延起点；重启必做独立回读。当前为候选，真实 PG/公开链路、来源切换预检及 Tutorial 启用仍待验收。


真实 Cloudflare 规则 API 的分页以必需的 total_count、per_page 和 page/count 核验，不能要求响应存在 total_pages；若提供该可选字段，仍校验其一致性。固定每页 50、最多 5,000 条，跨页总数变化、遗漏、重复 ID 或计数漂移均拒绝，不据不完整列表推断可用地址。真实合同测试 mail-cloudflare-rules-e2e.mjs 使用随机拒收 Worker/地址，管理凭据留本机；独立核对其他规则并清理，不能代替邮件投递验收。


云来源绑定新增 `ingress_capacity_bytes`：显式的全部云暂存容量预算，默认 0 只允许未绑定来源。Hook 的 validate/render/restore 以及 Go 启动均核验 active 与 draining 来源的 capacity_bytes 总和不超过预算，不按离线来源的未知已用量释放预留；finance 初始化仍为总计 2 GiB。此检查不自动购买、删除邮件或调整提供商容量。配置缩减/删除旧来源的持久生命周期门禁、来源切换及云端容量变更回读仍待实现，不能把静态预算检查当完整启用/切换通过。Tutorial 草稿的 cloud_bytes 不直接改此运行绑定，仍等待核验启用流程。


消费者将本地暂存目录权限、创建/同步及本地副本冲突记为 local-staging-unavailable，与认证、云端不可达和云原文变化分开；仍要求 0700 目录、0600 原文和摘要核验，不自动覆盖损坏的已发布副本。本地故障保留任务及云原文，按原账本重试。

已应用投影的快速核验也区分来源故障：未知云版本、来源身份改变或公开规则漂移会撤下当前邮箱的本地登录/发件授权，推进授权代次并持久登记下游撤权；保留地址及历史邮件归属。普通来源连接超时不撤销既有本地邮箱访问。来源精确核验恢复后只能签发新代次授权，旧授权不复活。这项候选修复不等于完整 ANAS 备份恢复已经验收。

v21 在既有 ingress_route_state 保存公开来源身份摘要与容量预留，Go 在消费/授权初始化前事务核对整个来源集合。所有持久来源（含旧绑定、poll 和通知）都须明确配置；遗漏、缩小已预留容量、改变原点/提供商/Cloudflare 目标或复用代次均拒绝启动。active 与 draining 共同占用总预算，并发启动串行登记，失败回滚，不改云修订或邮件。旧 v20 行初始未绑定，由显式匹配来源配置补登记；不是自动采纳云路由。当前尚无独立排空后退休/缩容接口，因而拒绝这些变化；实际云容量匹配、切换前置核验和 Tutorial 启用仍须另外完成。

地址移交准备/历史候选：共用目录库可只准备目标 pending 非登录主箱，保留原地址占用并保持重试 ID；未占用地址不能开始建号。目录只读器按既有多值 anasMailOwnershipHistory 读取 anas.mail-address-transfer/v1 记录，严格拒绝未知/重复字段及归属歧义；最多 64 条/总 64 KiB、单条 4 KiB。投影仅保存与当前地址/owner/箱一致的规范记录，空记录省略，保留旧 v1 摘要；公开邮箱列表不输出归属历史。记录本身不授予地址，未知证明仍拒绝跨箱占用；已知证明的持久登记/内部消费见下节。旧许可及供应商事实的生产协调、管理员确认 API 和完整恢复仍待完成，不能用这些准备接口表示 R-068 完成。


## 地址移交撤下证明候选

v22 的 `address_transfer.sql` 和 `RecordAddressWithdrawal` 仅登记可信协调方核验的撤下事实：旧 claim/箱版本、授权代次、全部预留来源与关闭边界、剩余默认公开规则及已完成撤权。相同意图重试返回原证明；缺失、漂移或未完成操作拒绝。Cloudflare→VPS 转发规则只要仍在准备、创建或删除过程中，也会阻止生成移交证明；已取消或确认删除的规则不阻止。剩余默认地址可使用已确认的 Cloudflare→VPS 规则作为证据，规则 ID、指纹和仍开放的原邮箱绑定必须一致。记录不移动地址、不创建新人员；地址证明外键只有在证明已由确定目标消费后才可关联。`StageMailbox` 已增加内部消费路径，只允许精确已知证明把 retained、非默认旧址分配给确定的准备主箱；证明消费、地址外键及 pending 投影同一事务提交，并按稳定顺序锁两个人员。未知记录仍拒绝，原箱稳定键和来源已接受目标保持。当前没有生产证明协调调用者或管理员移交入口；真实 AD、下游及完整恢复继续按根计划验收。


云端已接受邮件的本机 SMTP 导入使用空反向路径 `MAIL FROM:<>`。原始 EnvelopeFrom 仍保存为不可变元数据，原始 RFC 5322 字节及可见 From/Reply-To 不改写；本机垃圾过滤、Sieve 和配额仍执行。该本地传输不代表原发件人再次发送，原生拒收不得把含历史内容的 DSN/MDN 自动发到可能已经移交的地址；遵循 [RFC 5429](https://www.rfc-editor.org/rfc/rfc5429.html#section-2.2.1) 的空反向路径规则。原生 Sieve 的 envelope-from 因此是本地空值；原始信封供受信来源审计使用。已认证用户的对外发信路径保持当前有效发件人。170 隔离实机复现修复前误投；171—172 已通过修复后的真实拒收隔离及 MIME/附件、Sieve、硬配额、Consumer/PG/DMS/Roundcube 回归。目录/IAM/云来源仍是受控依赖，不代表完整管理员移交、真实云 SMTP 或一致恢复通过；固定基线及证据见根计划。

真实 SMTP 来源验证另用 `test-env/scripts/mail-cloudflare-smtp-e2e.mjs`：部署未修改的正式 Worker，由已授权的 Resend/eusend 一次 SMTP 信封投向主地址、别名和附加箱；finance 的正式 Go 来源客户端延迟取信，核对可信目标及 MIME/附件。它不伪造 EmailEvent 或 DMS 回执；未接 PG/原生投递时，读取保持未交付。运行前显式提供私有凭据与编译制品，管理 Token 留本机；结束仅清理本轮隔离资源与临时绑定，记录源码/制品摘要及无关规则比较。实际结果归根邮件计划，不从测试代码存在推断通过。

来源 HTTP 客户端的空闲连接保留 30 秒，短于正常 60 秒轮询间隔，避免下一轮复用可能被 NAS NAT 静默回收的连接；不会中断活动请求或放宽 TLS、响应头/正文及来源身份校验。真实 SMTP 测试的只读列举允许在总期限内对 unavailable 重试并记录实际延迟，身份或认证错误仍立即失败。foreign 可见地址与 main/Bcc 两种测试结果分别保存，后者不能替代伪造外域头的完整验收。


## 来源切换前检查候选

Worker 到 Cloudflare 转发 VPS 的切换候选会分批建立并重新核对每个转发目标及当前地址归属，并检查完整公开规则与持久记录。目标待验证时保留旧来源中仍开放且有效的地址，核对全部来源的本地许可及其他公开规则后，允许本人使用本地邮箱读取验证信；已关闭的旧地址不会重新打开。目标核验缓存不能作为公开规则存在的证明；规则禁用或漂移、创建结果未知和已登记规则缺失均不得作为撤下旧入口的依据。目标待验证及公开规则冲突分别记录状态，预检成功不会清除尚未完成的公开规则错误。目标全部就绪后才按持久意图撤下旧规则、发布并回读新规则。转发 VPS 切回新 Worker 或切换到另一个 Cloudflare 转发 VPS 的候选同样核对旧转发规则的精确身份及持久状态；删除结果未知时回读旧规则并恢复，不盲目重复删除。其他缺少受管公开规则的来源切换仍失败关闭。该候选仍须完成真实服务、中断恢复与邮件投递验收，尚不保证无中断切换。

同域 active 替换 draining 时，Go 在首次协调及重启后先检查新源实际容量、留存和通知设置，压力/满额/不可达阻止切换。旧源仍有效的已发布目标必须在新源具备同一人员、邮箱和 claim 版本；新源内部许可独立回读，不复用一分钟缓存。随后只读核验公开规则与持久账本：仅允许精确已知 draining 规则暂时占用待切换地址，拒绝未纳管或漂移对象，不覆盖未知云状态。检查成功后才撤下旧入口并按原逐地址持久意图发布新规则，旧来源继续取信。部分失败后重新核验；全部成功后的进程内标记只减少重复预检，不能跨重启作为凭据。

这仍是切换前协议检查，未包含实际 SMTP 写入 canary、正式 Tutorial 切换执行器、安全退休/缩容或两云完整故障验收；预检后失联仍可能导致中断，不承诺原子切换。旧来源的持久容量预留继续保留，不因预检或空列表释放。

管理员配置页的 VPS 直收草稿可接入**已运行**的云中转：保存域、来源 HTTPS 地址、通知地址和明确限额后，使用操作 ID、来源 ID、代次和相互独立的读取/控制/通知令牌，分别检查来源并保存配置。服务在每次请求前后验证管理员，宿主按工作区授权、审计并锁定事务，再核对域、容量和通知挑战；令牌不进入草稿或浏览器存储，输入发送后立即清空。操作 ID 应保留以恢复未知结果。该入口不安装 VPS、不配置公网路由，成功仅表示配置已保存；Cloudflare→VPS、真实通知和收发仍需独立实现及验收。

VPS 候选程序提供 `cloudrelay preflight PRIVATE_CONFIG_PATH`：使用同一私有配置读取与证书检查，输出无秘密的 JSON 检查项，包括当前本机 HTTPS/SMTP 端口是否可绑定、数据目录是否被占用、当前可用磁盘与计划容量的比较、证书域名/有效期及系统信任链。检查会短暂绑定端口但不写文件或访问外网；存在的数据目录一律显示占用，须由初始化恢复流程另行核验。磁盘、端口检查只是瞬时观察，不预留资源；无 SMTP 配置、系统权限不足和端口被占用不得视作公网可达，外部防火墙、MX、通知、实际邮件投递始终显示未知或需另测。预检命令以结构化状态表达失败，进程正常退出不表示检查通过或服务已安装。

`cloudrelay prepare-install PRIVATE_CONFIG_PATH` 将本机预检与既有封存初始化连接起来：新来源要求证书通过系统信任、明确配置 SMTP、当前磁盘余量足够且两端口可绑定；随后按同一配置快照初始化。已有目录只通过封存身份和存储核验才能作为重试，运行中占用端口不阻止读取既有状态。末尾重新读取配置；初始化后的配置漂移或操作失败返回结果未知，需要保留配置与目录检查恢复，不得盲目重建。此命令尚不安装或启动服务，也不证明域名、公网连通性、Webhook 或实际投递。

VPS systemd 单元候选使用 `DynamicUser`、私有 `StateDirectory` 和 systemd `LoadCredential`，仅授予绑定低端口所需的 `CAP_NET_BIND_SERVICE`。`run-systemd` 在同一进程中读取受限凭据、验证来源目录必须为状态目录下 `data/source`，检查后初始化并运行，避免准备与运行使用不同配置；数据目录不接受符号链接。配置由 systemd 从 `/etc/anas-mail-relay/runtime.json` 提供，服务启动状态仍不能证明公网收信或投递。受限自动安装候选可发布该单元；证书续期、凭据轮换、真实主机安装与完整恢复仍需实现和验收。

`cloudrelay publish-systemd PRIVATE_CONFIG_PATH` 是受限文件发布候选，要求 root 持有的私有输入，固定发布当前程序、内嵌 systemd 单元和私有配置；输入的来源目录必须是 `/var/lib/anas-mail-relay/data/source`。首次发布前检查同名状态目录、常见 systemd 单元及覆盖目录；以控制令牌计算的绑定摘要固定配置、程序及单元，私有记录不含凭据。使用独占写入、同步和不覆盖重命名；相同输入可重复核验，完整暂存可续行，损坏或未知暂存停止并保留供检查，不自动覆盖。此命令不执行 daemon-reload、启用或启动服务，不代表完成安装；systemd 管理器内存态、所有覆盖来源、证书信任、端口和公网收发仍须在后续安装步骤重新核验。程序升级、配置或证书轮换也尚不受支持。

`cloudrelay inspect-systemd PRIVATE_CONFIG_PATH` 只读检查已发布材料和当前 systemd 单元：持发布锁核对私有绑定、配置、程序、固定单元和覆盖目录，再检查管理器的单元路径、动态用户及运行状态。运行中的服务还需在 Linux `/proc` 核验非 root 进程及程序摘要，并以证书验证的回环 HTTPS 和读取令牌核对来源身份、容量及保留期，前后确认进程未变化；不支持的监听地址或任何不确定结果返回未知。`active-local-verified` 仅表示本机来源响应经核对，不证明公网 SMTP、路由、通知、空间充足或实际入箱。该命令不会重载、启用或启动服务。

`cloudrelay activate-systemd PRIVATE_CONFIG_PATH` 是受限启用候选：以发布锁核验绑定和材料、重载并核对 systemd 单元归属，检查新来源目录不存在、证书信任、磁盘和端口后记录持久意图，再分别启用并启动固定单元。启用或启动回执丢失时只按实际状态确认；无法确认或来源目录已出现时停止，不能盲目重发。重复调用在服务已运行时检查动态用户、程序和受认证本机来源；已发启动意图但服务已停止时返回未知，不自动覆盖管理员的停止操作。`active-local-verified` 不证明公网路由、Webhook 或投递。此入口仅供 root 本机执行，受限 SSH 助手可代为调用；部分故障仍需人工恢复，证书/凭据轮换与升级尚未实现。

VPS 远程安装候选增加固定 `cloudrelay bootstrap` 入口：仅 root 可执行，从标准输入接收受限 JSON，只允许发布、启用或检查固定服务，不接受可执行文件、命令或文件路径。配置规范化后暂存于 `/run` 的 root 私有目录，正常结束即删除；进程异常中断可能留下该私有临时目录，需按运维流程核验清理。SSH 客户端仅使用专用私钥与预先核对的主机公钥，命令固定为 `sudo -n /usr/local/libexec/anas-mail-relay-bootstrap/cloudrelay bootstrap`，不使用 SSH agent 或交互式认证；配置通过 SSH 标准输入发送，错误或连接中断不自动重试。VPS 须先由管理员安装固定助手，并为该部署账号配置强制命令与仅允许此精确 sudo 命令的规则；受限账号安装入口见下文；实际远程主机及完整安装仍待验收。程序使用项目已有 `golang.org/x/crypto/ssh`，没有新增第三方依赖。

VPS 发布的配置、程序和单元分别记录持久意图和完成标记：完成后文件或程序目录被删除时，旧操作拒绝重建；只有未完成且暂存文件完整匹配时才能续行。意图已写但目标和暂存均缺失、部分暂存或标记损坏时保留未知，需运维核验；不从已存在的文件推断整个安装完成。启动检查也要求三项完成标记齐全。

VPS 自动配置候选在独占的私有操作目录中一次生成来源 ID、代次以及相互独立的读取、控制和可选通知令牌，并将运行配置（含 TLS 私钥）保存在 0600 文件；SSH 私钥不写入该记录，只绑定客户端公钥指纹及 SSH 主机公钥指纹。相同输入可重开并复用，配置、主机或公钥变化拒绝，记录缺失或损坏不再生成另一组密钥。SSH 发布与启用调用分别要求授权、远程动作前审计，以及握手后命令执行前再次授权；响应后撤权不得记录完成。只在确认发布后允许启用，网络失败不会自动重发；明确重试沿用相同凭据和远端不覆盖规则。调用方仍须持工作区锁、核对受管域与来源容量，并完成实际管理员身份验证、Secret 事务与页面接线；该候选不等于远程服务安装或公网收发已通过。

VPS 自动来源交接候选将确认发布、启用后的生成凭据接到既有来源准备和工作区配置／Secret 事务。准备和提交均要求重新授权，并检查发布、启用意图与完成标记；来源仍须通过受限 HTTPS 读取、控制及通知挑战核验。配置提交沿用现有来源快照、排空及防止旧操作恢复已删除来源的保护。测试中的 SSH 结果为受控替身；此调用不启用公网路由。


管理员配置页候选为 VPS 直收提供 `publish`、`activate`、`prepare`、`commit` 四步；先保存来源地址与限额，预先安装受限助手并独立核验 SSH 主机公钥，提供来源域的受信任证书。请求使用服务身份及当前管理员会话，宿主重读受管域和来源快照，并在远程动作前持久绑定配置；提交意图出现后禁止旧操作再次启动远端。SSH 私钥仅用于请求，页面提交后清空；TLS 私钥会保存在宿主 0600 私有操作记录中以恢复相同操作，不写入配置草稿或响应。SSH 和 TLS 私钥每步需重新输入。成功仅确认当前步骤，不代表公网 MX、Webhook、路由或真实收发已通过；受限助手的安装、权限、凭据恢复及完整验收仍待完成。


`cloudrelay publish-bootstrap` 仅供 root 调用；可信 `/usr/local` 下缺少标准 `libexec` 目录时会创建并校验它，拒绝符号链接、不安全权限及不可遍历目录，将当前可执行文件发布到 `/usr/local/libexec/anas-mail-relay-bootstrap/cloudrelay`；应先独立核对制品摘要。`/etc/anas-mail-relay-bootstrap` 中的私有持久绑定固定程序摘要，并记录目录与文件的意图/完成状态。相同程序可重试核验；已有目标、内容变化、已完成文件或目录缺失、未完成的目录创建及损坏暂存均停止，不接管或重建。文件使用独占暂存及同步，完整暂存可续行。该命令不建立 SSH 用户、公钥和 sudo 规则、不启动邮件服务，也不代表自动安装完成；操作记录备份、升级及凭据轮换仍需补齐。


`cloudrelay bootstrap-access-plan PUBLIC_KEY_FILE` 只读生成固定账户、公钥强制命令和 sudoers 材料；公钥必须为 root 持有、不可被组或其他用户写入的普通文件，只接受一个无自带选项的 Ed25519 公钥，去除注释并拒绝额外密钥。`bootstrap-sshd-check REMOTE_IP REMOTE_HOST LOCAL_IP LOCAL_PORT` 以受限环境执行 `sshd -T -C`，检查该具体连接条件的认证方式、密钥来源、强制命令和 Ed25519 支持。检查通过仅描述该上下文，不证明其他来源地址的 Match 规则、sudo 授权、账号、后续配置变化或服务安装安全；两个命令均不创建账号或修改 SSH/sudo。


通知接收端要求签名正文恰好包含协议规定的六个字段，拒绝重复字段、大小写变体、缺失字段和尾随对象；格式错误不会登记通知或唤醒取信。签名通过仍只代表通知已登记，不表示原文已投递。


`cloudrelay bootstrap-sudo-check` 只读检查固定 `anasmaildeploy` 账号当前的 sudo 生效列表，要求恰好只有一条以 root 运行固定助手命令的免密授权；额外条目、命令、运行用户或组及其他选项均拒绝。该检查依赖 sudo 的 C 语言长列表格式，支持条目不显示来源或显示固定规则文件 `/etc/sudoers.d/anas-mail-relay-bootstrap`，其他未知格式失败关闭。它不创建账号或安装规则，不固定后续策略，不验证其他 SSH 连接条件，该检查已被受限账号安装入口调用。


`cloudrelay bootstrap-install-access PUBLIC_KEY_FILE REMOTE_IP REMOTE_HOST LOCAL_IP LOCAL_PORT` 为 root 显式执行的受限账号安装候选。先独立核对程序摘要和 SSH 主机公钥，公钥文件及其每级父目录必须由 root 持有、不得被组或其他用户写入且不能经符号链接；连接参数必须对应 SSH 服务端看到的具体连接。程序发布固定助手，创建锁定密码的 `anasmaildeploy` 专用系统账号和 root 持有的主目录、公钥及固定 sudo 规则；核验指定 SSH 上下文启用 PAM 与 StrictModes，并在开放公钥前后检查 sudo 实际权限。公钥携带强制命令与 `restrict`，账号不得有附加组。若已安装后检测到 sudo 或 SSH 策略漂移，命令报告失败但不会自动撤销已发布公钥，须由管理员停止使用并处理；重复执行只核对原绑定；已有外来资源、不同公钥或已完成文件缺失会停止。创建账号回执丢失后，只可核验匹配该操作绑定的账号；目录创建意图存在但结果不明确时保留未知，由管理员核验恢复，不自动接管。`.hushlogin` 由 root 持有，但某些 PAM 配置仍可能输出额外内容；必须通过实际受限 SSH 连接检查协议，成功信息只表示账号材料安装，不代表远程邮件服务或收发已就绪。该命令不修改 sshd 配置、启动邮件服务或变更 DNS；当前账户安装、实际 PAM、跨机操作和恢复尚未完成一期验收。


VPS 服务发布同样会在验证输入后准备可信 `/usr/local/libexec`；不会修改已有目录权限，也不会覆盖同名程序。

通用 `cloudrelay preflight` 按给定来源目录的现有父目录检查；systemd 首次安装前，该父目录由服务启动创建，通用检查可能显示目录失败。`activate-systemd` 使用独立的固定状态目录冲突、磁盘、端口和证书检查；不能把通用预检目录结果当成 systemd 已就绪，配置引导仍需将两者区分。

VPS 中转的私有运行配置候选可选 `forward_domain`，用于 Cloudflare 转发至独立投递子域。它为每个公开地址的来源、代次、邮箱和地址版本确定一个独立 SMTP 投递目标；目标是路由标识，不是认证凭据。SMTP 只接收当前目标，保存时在同一锁内核验当前绑定并记录公开地址与原归属；目标在 DATA 完成前撤销或移交则临时拒绝，不转给新归属。已接受邮件重启及重试仍保持原绑定；历史目标碰撞、投递域变化或损坏账本失败关闭。旧配置不含该字段时仍为直接收件。此为中转接收候选，目标和规则协调现已接入服务调用，但管理员引导和真实验证信访问尚未验收，不能启用或视为实际公网收件成功。

Cloudflare 转发规则的底层客户端候选使用与区域规则令牌分离的账号目标令牌，只按已记录的目标 ID 查询，并核对精确投递地址及提供商验证时间后才允许创建规则；外来目标、未经验证或含糊响应均拒绝。删除前重新读取并比较完整规则，写入回执丢失视为结果未知。候选支持向账号创建精确投递目标，调用此创建接口需要目标编辑权限；创建返回 ID 不代表验证完成，调用方须先记录已发起意图并检查目标是否已存在，回执未知时不能盲目重试或认领已有目标。此客户端没有接入持久操作账本、管理员引导或自动启用；调用方仍须先持久记录意图、检查既有规则并独立回读。提供商不保证跨管理员操作的原子互斥，读取后仍可能发生外部修改；实际云端验证和完整同步尚未完成。

规则预检候选逐页读取整个区域，要求分页总数与每页数量一致、ID 唯一且字段无歧义；已有规则占用所需地址或该地址的受管规则名称指向其他归属时停止。最多读取 5,000 条，遇到无法识别的规则结构也停止；同来源的其他地址不因此被当成冲突。返回的匹配项只是提供商观察，不能代替操作账本证明归属或确认对外收件，也不能据此自动认领外来规则。

创建投递目标前，客户端也逐页只读检查账号目标列表，发现同地址、分页不完整或字段不明确就拒绝创建；不会向调用方返回其他人的地址或 ID。检查仅是当时的观察，不排除其他管理员在检查后并发创建。账号目标编辑权限覆盖整个账号，实际安装须明确告知并限制用途；调用仍未接入产品流程。

v23 增加转发目标持久意图候选。记录绑定精确来源代次、账号、区域、投递域、地址版本及目标，不保存令牌；创建前须有已回读的开放来源许可和当前目录投影，且一个目标只有一个进程可标记发起。发起后丢失响应或进程中断保持未知，不盲目重发或认领已有目标，需要管理员核查；这也可能在请求实际未发出时暂停等待。迟到响应可记入原归属的历史，但验证完成后还要重新检查该归属仍开放，不能复活已撤销地址。记录的提供商 ID 不是验证结果，必须再次读取提供商状态。此协调器已与 v24 规则候选及服务来源协调调用组合；账号目标未知结果的人工恢复、验证邮件访问、真实云验证及完整收发尚未实现或验收。

v24 增加公开转发规则持久意图候选，按原始来源许可记录逐地址规则及精确提供商 ID/配置摘要。创建前核对已验证目标、完整规则列表和当前来源归属；发送请求前持久登记发起，收到响应先保存 ID/摘要，再独立回读并确认。响应已保存但回读失败可继续回读；创建结果未知不自动重复或认领外部规则，仍需人工核查。来源已关闭时，未发起规则可取消；已知规则先核对原 ID 和完整配置再持久登记删除，响应丢失后仅在独立读回确认规则不存在时完成，不盲目重发。旧规则未确认移除前，同源的新地址版本不能建立新规则；已完成的旧规则事实保留历史。候选已接入服务来源协调调用，但尚未接入管理员安装引导，不代表公网已就绪。

转发来源批量协调候选按已确认来源快照处理各地址并清理旧规则；单个目标待验证、超时或规则失败不停止其他任务，但整轮仍报告未完成。每轮最多执行 20 个任务，单任务 5 秒、整轮 45 秒，按顺序轮转，失败至少退避 5 秒，成功观察最多复用 1 分钟；内存观察不跨进程，重启须重新回读。若批次尚未全部观察，返回处理中而不报告成功。该协调器已接入邮件服务的来源协调调用，但真实验证信访问、旧未知创建结果的人工恢复和真实云收发验收仍未完成。

`ingress_sources` 的自管云条目可选 `cloudflare_forward`（账号 ID、区域 ID、投递子域），对应 `ingress_credentials` 必须分别提供 `routing` 和 `destination` 令牌；不能与 Worker 模式或其他来源/服务凭据共用。该来源身份包含账号、区域及投递域，旧直收或 Worker 配置的身份摘要不变，不能将既有来源静默切换为转发模式。自管云经认证状态接口返回实际投递域；服务同步前和来源就绪检查核对预期模式，转发域错误或把转发源当直收时拒绝路由写入。状态接口只证明配置，不证明公网端口、目标验证或邮件送达。已部署的旧版直收源不返回该可选字段，仍按直收识别。若 DMS 回读、所有来源的本地地址路由以及最新目录归属均已确认，且唯一未完成项是转发目标待验证或受限批次尚在处理中，则允许本人登录本地邮箱；公网地址仍显示未验证并持续重试。任何来源故障、身份不符、规则冲突、目录变化或混合错误都不能使用此例外。该机制旨在解除验证信的先后顺序问题，但真实验证信访问、管理员安装页、账号权限交付、切换恢复及完整收发验收均未完成，不得启用正式服务。

管理员 VPS 自动安装候选现同时接受“Cloudflare + VPS”草稿：每一步输入独立投递子域、Cloudflare 账号和区域 ID、区域路由令牌及账号目标令牌，并沿用固定操作 ID、来源限额和受限 SSH 发布/启动/检查/配置事务。账号目标编辑令牌影响整个 Cloudflare 账号，管理员须独立核对权限；投递子域的 DNS/MX 和每个目标验证仍须单独完成。令牌每次提交后从页面清空，不写草稿、审计、VPS 配置或普通安装日志；安装器的受保护绑定检查重试输入不变，并经现有 Secret 事务交接给邮件服务。投递域与来源身份及 VPS 本地配置绑定，不允许当作直收来源。页面步骤完成不代表公开路由、验证信、Webhook 或实际收发已验收；真实管理员安装及云端端到端验证仍待完成。

转发规则删除候选接受 Cloudflare 的完整规则回执或仅 ID 的简短回执。完整回执必须严格匹配删除前已核对的规则；未知或不一致回执保持结果未知。调用方仍须持久记录删除意图并独立读回，不能仅凭响应认定删除已生效。

Cloudflare 转发目标的列表、创建响应与详情可包含 `status` 字段；兼容缺省以及 `verified`、`unverified`，未知或格式错误的状态保守拒绝。仅创建返回成功不代表目标已验证；启用规则前仍须独立回读精确目标及有效验证时间，若状态与验证时间相矛盾则拒绝，不根据状态猜测已验证。

配置页可由管理员显式检查 NAS 或 VPS 的 SMTP 主机名：从邮件服务器所在网络分别解析 IPv4/IPv6，逐个尝试每类不超过八个地址的 TCP 25，读取有限长度的 SMTP 问候，并仅发送 EHLO 和 STARTTLS，不发送邮件。每个成功问候的地址分别报告 STARTTLS 是否提供、证书是否与输入主机名匹配且受信任，或结果未知。非公网及保留地址不连接；地址解析后直接连接该地址，避免再次解析导致目标变化。结果区分收到 220 问候、拒绝、异常问候、非公网与未知，并显示时间；DNS 不可用或结果过多为未知。检查前后复核会话与管理员身份，并发检查有上限。此观察不验证 MX 指向、Cloudflare 或其他发件方的访问、邮箱路由或实际投递；邮件服务器与目标可能处于同一网络，不能仅凭本观察认定公网就绪。

重新读取的草稿改变收件方案时，页面清除原有 SMTP 观察并丢弃尚未返回的旧检查；需要重新检查新方案。

Worker 安装、接入现有 VPS 和受限 VPS 安装的管理服务在宿主操作返回后再次读取原草稿，并再次核对当前管理员；草稿已变化或无法读取时返回结果未知，不将可能已经执行的操作说成当前草稿已生效。三个入口的页面还会比较提交时与返回时的非敏感安装参数，包括操作 ID、来源 ID 或 VPS 主机等；参数变化时显示结果未知。接入已有 VPS 时，宿主返回的来源 ID 必须与所选来源一致；安装回执只接受精确字段名，重复或大小写变体不能当作成功。敏感输入不参与页面快照，结果未知时显示本次原操作 ID 供检查，即使页面上输入的 ID 已变更。此核对不构成跨系统原子事务，仍须按操作记录核对实际状态。

宿主入站安装和发信配置服务接口在返回成功前再次检查同一服务身份及工作区授权。调用期间授权撤销或身份变化时返回结果未知；操作可能已经执行，不应盲目重试。此复核不替代管理员目录检查，也不使跨系统操作和撤权原子化。

Worker 和 VPS 通知只接受包含各协议字段一次的明确 JSON 回执；重复字段、大小写变体、额外字段或字段缺失均不会标记通知成功。通知是否成功与邮件投递及原文保留独立；异常回执不能证明 NAS 收信。

从 Cloudflare 来源切换到不经 Cloudflare 的 VPS 时，协调器先检查新 VPS 并为当前主地址和别名准备、回读独立来源路由；同时保留旧来源上仍有效且已开放的路由和公开规则。此阶段返回直接 VPS 的 MX 切换未确认，不关闭旧来源，也不表示公网可用。实际 MX 切换、确认与旧来源退出尚未接通。

云邮件每次明确可重试的本地提交在 SMTP 前先生成并持久保存独立随机投递意图，私有文件绑定原始来源、邮箱、地址版本及原文摘要；结果未知或已经排队时不重新生成或提交。该文件不改变既有 intake 状态格式，旧记录可以没有此文件；内容不进入 API 回执或日志。它目前只是可信原生处理证明的准备，监听器、消息绑定和 LMTP 持久结果尚未接通，不能据此确认 processed 或删除原文。


### 邮件内的地址管理（2026-10-30 候选）

`/addresses` 和 `GET/POST /api/addresses` 管理本人独立附加箱及同箱别名；管理员可按姓名/用户名搜索人员并代办，目标人员在页面中明确展示。普通用户不能通过 owner 参数管理他人；服务端复查当前会话、目录准入、稳定管理员组及目标 objectGUID，受保护人员限制沿用目录检查。主地址仍为只读，删除别名仍保留占用，完整地址移交 R-068 继续未完成。

写操作复用 `internal/directorymail` 的全局占用、版本、幂等 ID、配额和中断恢复，每个写边界重新检查会话与目标。只接受同源 JSON 请求及精确字段；重复字段、null、未知字段和其他 owner 注入拒绝。最近真实认证超过 5 分钟时通过独立 `anas-mail-reauth` 客户端重新验证，同一人员与真实 `auth_time` 校验后方可重试；不会自动重放之前的写入。正常登录和再次认证都可返回原地址管理目标。

AD 的受限写身份从 Samba 提供，只进入私有 `identity/config.json` 的 `Addresses`；原 reader 继续用于认证、目录与运行投影。PostgreSQL v26 的 `mail.address_audit` 在目录写入前持久记录操作者、目标及操作意图，再写结果；中断保持 unknown，审计失败不报告完整成功。原生目录占用仍是权威，不把审计表变成地址副本。目录保存不表示 DMS 或公网收发就绪。

本轮为本地代码、Go 与浏览器候选验证。真实 AD 写身份 ACL、v25→v26 数据库升级、Casdoor/LLNG 再认证及正式部署仍需按根计划验收。
