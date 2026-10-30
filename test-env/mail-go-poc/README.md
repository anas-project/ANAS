# Go 邮件中间层可实现性夹具

仅用于 `dev-docs/requirements/mail-webmail-poc.md` 的隔离实验。它不是邮件 Module，也不是可直接部署的认证服务。执行记录、真实基线和未通过项保存在配套计划。

- `main.go`：真实 OIDC + PKCE 登录、PostgreSQL 邮箱/会话/令牌映射、测试 AD 别名管理、一次性交接、Dovecot 内省和发件授权。依赖复用根 go.mod 的 go-oidc、oauth2、go-ldap 与 pgx。
- `prepare.py`：复用上一轮原生夹具，生成 DMS/Roundcube/PostgreSQL 配置；内嵌 PHP 插件与 Rspamd Lua 薄适配器，业务授权仍由 Go 决定。
- `browser.mjs`：真实浏览器管理别名、切换邮箱和界面发信；`MAIL_GO_MODE=refresh` 另测跨令牌过期的自动续期，`continuity` 用于测试改址后的重登录。
- `protocol.py`：独立 IMAP 收件核验、SMTP 伪造/跨箱反例、别名撤销、禁用、Go 停机及会话注销。
- `server.py`：严格限定本次目录、Docker root、项目及工作区标签的诊断和故障注入。公开地址变更只是测试预配，没有实现 sAMAccountName 修改功能。

所有远端命令都要遵守 test-env/README.md 的独立 Docker、containerd、网络命名空间和出站阻断边界。当前脚本固定 20261024 实验路径与 18884/19994/15888/15254 端口，不能直接指向生产。先编译 Linux amd64 的 Go 二进制，再生成空目录并启动目录/IAM、数据库、Go、DMS/Roundcube；入口代理在所有 DNS 名称可解析后启动。自动化总入口尚未实现。

合成凭据、会话、原始协议记录、图片、数据库和构建产物只存放在私有 test-env/reports 或远端 evidence，不能提交。只读复制已有镜像，禁止启动或修改默认 Docker 的服务。

明确边界：AD 使用新建测试域的管理员绑定；生产必须改为目录 Provider 的最小权限账号。地址修改采用同步直线流程，尚无跨 AD/PostgreSQL/DMS 失败恢复；目录订阅及撤权仅由新增隔离实验实现，完整生产 IAM 生命周期尚未验收。Go 的 /test/credential 是协议验证入口，不能作为生产 API。Roundcube 每个浏览器会话只激活一个邮箱，不承诺多标签页并行写信。Rspamd 在这个夹具中仅验证发件授权，合法测试邮件直接 accept，不能沿用为生产反垃圾配置。新架构的停机恢复由下述独立实验核验，以配套计划结果为准。

地址路由文件有变化才原子替换，避免无变化重启 Go 也触发 DMS 重载。测试为原生 DMS 异步应用保留等待时间；这不表示已实现配置版本、应用确认、失败恢复或无中断登录。

首轮历史边界：禁用后已认证 IMAP 仍能 NOOP，Go 邮件会话注销后已认证 SMTP 仍可发信。新增实验针对这两项补充定向断开及逐封发信授权；是否通过以配套计划的真实连接结果为准，不覆盖历史失败记录。


## 公司通讯录与撤权实验

用户确认一期只做 AD 用户、组和只读公司通讯录；组内/全局共享联系人延期二期。`directory.go` 提供来源列表、查询和会话检查接口；当前只返回 `company`，未知共享来源与写入口均拒绝。Roundcube 由薄地址簿后端读取 Go，不直接取得 AD 凭据。来源 ID、只读属性与会话授权作为二期扩展边界，不预建共享数据副本或编辑界面。

- `directory_prepare.py`：Samba 文件审计→现有事件规范化器→只读 journal 挂载；扩展监听资料属性仅存在本测试夹具。DMS 内定向断开适配器由 Supervisor 管理，不挂载 Docker socket。
- `directory.go`：持久游标、缺口识别、完整 LDAP 读取后的原子更新、待断开任务重试、真实 OIDC 后台退出。每次事件触发权威目录刷新；300 秒兜底核对。
- `directory_server.py`：固定新工作区的真实 LDAPS 修改与故障注入；新目录为 `mail-test-directory`，旧 `mail-test-go` 保留。
- `directory_browser.mjs` / `directory_protocol.py`：`directory`、`logout`、`disable`、`direct`、`nested`、`iam`、`recovery`、`delete`。每轮使用真实 IAM 登录，`delete` 必须最后执行。浏览器重跑会归档同名旧结果。

IMAP 仍以固定 mailbox login 登录，OAuth 内省另给 `user_mailgo_sid`；当前 Dovecot 2.4.1 用 `oauth2_fields` 同时设置 `user_mailgo_sid` 和 `userdb_user_mailgo_sid`：后者将标记传到实际 IMAP 进程。仅设置 passdb 标记无法匹配本地邮箱连接。撤权用原生 `doveadm kick -f user_mailgo_sid`。SMTP 使用受 Go 会话绑定的独立 SASL 标识及短期令牌，每封信经 Go 复核；撤权后拒绝新邮件，不承诺物理关闭 SMTP TCP 连接。

本夹具不是完整增量目录引擎：小规模域每次全量核对，未测多 DC、容量、长期断网、审计生产者崩溃窗口、冷启动时 AD 不可达、并发授权竞态和所有账户锁定策略。生产须补充最小权限、可观测性、目录新鲜度与故障关闭策略。二期共享通讯录本体尚未实施或验收。

## 新架构发信与恢复实验

`prepare.py` 可创建全新的 `mail-test-recovery`，项目为 `anas_mail_test_go_recovery_1024`；不会覆盖 `mail-test-go` 或已删除 Alice 的 `mail-test-directory`。Go 二进制与七个镜像固定为上一轮已验证制品，原型业务逻辑不变。

- `browser.mjs` 的 `initial` 模式实际发送两个别名及独立邮箱邮件；`restored` 模式在恢复环境重新登录 Alice/Bob 并打开三个邮箱，不重复发送基线邮件。
- `recovery_protocol.py` 复用 `protocol.py` 的真实认证连接。`seed` 核对实际 From/Return-Path、别名共箱和 Sent 隔离，创建非空正文/附件/文件夹/持久标志；`snapshot`、`verify` 比较三个邮箱全量内容；`queued`、`verify-queued` 核对 hold 邮件及恢复后单次投递。
- `recovery_server.py` 仅允许上述源目录和 `mail-test-recovery-restored`，逐次核对专用 daemon、项目、工作区标签和挂载。`backup-restore` 停止全部服务，检查无 OOM/强制杀死，再归档 data、fixture、compose 到私有 evidence；目标必须不存在，解包后比较每个文件内容、属主和权限，再启动独立恢复项目。
- `snapshot`/`verify-state` 比较 PostgreSQL 人员归属、邮箱、别名、公司目录、订阅游标及 Roundcube 身份/非默认偏好。队列核验同时检查原始别名和 Postfix 展开后的内部收件地址；直接从保留的源队列文件导出邮件原文，与恢复队列原文哈希比较，不能重发替代恢复。Postfix 短队列 ID 随 inode 变化，不要求文件名相同。

完整保存集合包括邮件、DMS 状态/队列与配置、Roundcube SQLite、Go PostgreSQL、Samba、IAM、目录事件/审计及测试密钥和固定夹具。实验验证的是停机备份与相同版本恢复，不验证在线一致备份、跨版本迁移或生产 ANAS 备份命令。证据、tar 归档和所有测试凭据不得提交 Git。
