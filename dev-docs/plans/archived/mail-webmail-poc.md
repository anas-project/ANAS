---
doc_type: plan
status: done
created: 2026-10-23
updated: 2026-10-24
---

# DMS 与 Roundcube 组合验证计划

依据：[需求矩阵](../../requirements/mail-webmail-poc.md)、[集成研究](../../../docs/research/mail-routing-alias-and-module-access.md)、[测试用例](../../../test-env/cases/mail-webmail-poc/README.md)。本轮是用户确认的隔离 PoC，不是生产邮件 Module 实施。

**历史基线：原生组合的真实统一登录、合法别名收发和停机后空目录恢复通过；主地址变化后的 Webmail 身份连续性、可见 From 的授权未通过。** 2026-10-24 按用户指定的 Go 中间件方案追加真实主机验证，执行结果见文末；不把历史通过项自动视为新架构通过。

实验复杂度为中等：DMS、Roundcube、独立 Samba/LLNG 和测试 TLS 入口，使用原生配置及手工测试预配。正式加入目录同步、Portal 自助管理、稳定身份及云端联动会涉及多处状态一致性，复杂度高；本次没有实施这些生产机制或上游补丁。

## 里程碑与需求归属

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：隔离与固定基线 | MAILPOC-R-001 | 已完成；隔离基线与限定范围清理均已核对 |
| M2a：统一登录与认证拒绝 | MAILPOC-R-002、MAILPOC-R-003 | 已完成；真实浏览器、IMAP 与 SMTP 通过 |
| M2b：改址后的身份连续性 | MAILPOC-R-004 | 已完成；10-23 原生失败保留，10-24 固定登录标识原型通过测试预配改址 |
| M3a：别名收件与界面发信 | MAILPOC-R-005 | 已完成；新版 SMTP 会话适配下真实界面发信及独立收件核验通过 |
| M3b：未授权地址拒绝 | MAILPOC-R-006 | 已完成；10-23 原生失败保留，10-24 Go + Rspamd 信封及 From 拒绝通过 |
| M4a：data 保存集合 | MAILPOC-R-007 | 已完成；两轮挂载检查通过，新 Go/PostgreSQL 也无匿名持久卷 |
| M4b：新架构空目录及队列恢复 | MAILPOC-R-008、MAILPOC-R-009 | 已完成；Go/PostgreSQL/目录订阅完整停机备份、空目录恢复和真实队列续投通过 |
| M5：组合比较 | MAILPOC-R-010 | 已完成；见研究报告第 9 节，未实测产品明确标注 |
| M6：Go 统一认证及独立邮箱 | MAILPOC-R-011、MAILPOC-R-012 | 已完成；真实 Go 交接和两个独立邮箱验证通过 |
| M7：Go 别名管理、凭据与故障拒绝 | MAILPOC-R-013、MAILPOC-R-014、MAILPOC-R-015 | 已完成；限定的别名/新认证/故障检查通过；旧连接撤权缺口已记录 |
| M8：AD 只读通讯录与二期接口 | MAILPOC-R-016、MAILPOC-R-017 | 已完成；真实用户/组查询、资料更新与补全通过；共享联系人本体延期二期 |
| M9：事件与登录会话撤权 | MAILPOC-R-018、MAILPOC-R-019 | 已完成；禁用/删除/直接及嵌套退组、本地及 IAM 退出，旧 Cookie/IMAP/SMTP 与不误伤通过 |
| M10：目录订阅恢复 | MAILPOC-R-020 | 已完成；消费者重启、日志缺口和 LDAP 读取失败恢复通过 |

## 环境与实际基线

- 用户在本次明确指定 `finance`，SSH 为 `whl@finance.hlong.wang`。本机 Docker 应用启动失败 `kLSNoExecutableErr` 后使用此已授权目标；没有维修或重装本机 Docker。
- 远端根目录 `/home/whl/anas-mail-webmail-test-20261023`；专用 socket `/run/anas-mail-test-1023.sock`，Docker 数据根为其 `docker/`，containerd 也有独立 root/state/socket。
- 网络命名空间 `anas-mail-test-1023`，宿主连接 `10.253.23.0/24`，容器地址池 `10.246.0.0/16`。专用 `DOCKER-USER` 规则拒绝容器向池外发起 NEW 连接。端口 18883/19993/15887 仅由该 namespace 发布，通过专用 SSH 隧道访问；没有公网 DNS/MX 或第三方投递。
- 原项目 `anas_mail_test_1023`，原目录 `mail-test-workspace`；恢复项目 `anas_mail_test_1023_restored`，恢复目录 `mail-test-restored`。恢复前目标不存在，原目录保留。
- Docker 29.7.2、Compose v5.5.0、x86_64。预检约 59 GB 可用磁盘、1.5 GB 可用内存；禁用了病毒、垃圾邮件等非本轮功能，不把此最小配置或内存限制视为生产安全/容量基线。
- DMS 文件预配两个测试邮箱，别名投递与发信许可在 DMS 原生文件中配置。Samba 中有真实 `mail`、`proxyAddresses`、`anasIdentityAnchor`；本轮没有实现 AD 自动同步。稳定邮箱目录为 `/var/mail/box-alice`，不是直接以 anchor 或可变地址建目录。
- LLNG 使用真实 AD/LDAPS、原生授权码与 PKCE；邮件端使用真实 UserInfo 校验。Webmail→IAM、IMAP、SMTP 校验证书；浏览器仅对本次短期自签 CA 使用忽略证书错误。

| 组件 | 实际镜像 ID / digest |
| --- | --- |
| DMS 16.0.1 | `sha256:d0fe7668defe157aad57ea31b1707ad1e2fb57d7a91bdf17cbdf876549946c86` |
| Roundcube 1.7.5 Apache | `sha256:ebf5271ff6f15fd5939c66b31525ba1bfaefdeae8bc2b69f631e10890b1ad15f` |
| Samba 4.23.6-r13 | `sha256:1d110032df763dd0ba7d1dbe233f4b4f40468322eb49f779677bbfa89a62333e` |
| LLNG 2.23.4 测试候选 | `sha256:0e50fcf6503a38858aca6a2e52b29f481b3a4cf3306747c1e74c65738d4ce1aa` |
| nginx 缓存镜像 | `sha256:a5127daff3d6f4606be3100a252419bfa84fd6ee5cd74d0feaca1a5068f97dcf` |

DMS 内实际为 Dovecot 2.4.1-4、Postfix 3.10.13；Roundcube 运行文件声明 1.7.5。DMS 固定源码 tree 为 `72c034618d46a05106dd3c75164fa242dfbb607e`。Roundcube Docker Hub 两次 TLS 超时后，从 finance 已配置使用的镜像源 `sk-docker.bengbu.cc.cd` 拉取该版本，记录实际摘要与运行版本；未宣称完成独立镜像签名验证。Samba/nginx 与 LLNG 镜像仅从既有 daemon 只读导出，测试运行均在本次专用 daemon。

LLNG 候选镜像含仓库自定义文件，但本夹具使用其原生入口和原生 AD/OIDC 配置；不能据此认定 LLNG Module 的正式启动、配置升级或全部 Provider 矩阵验收通过。

仓库起始 HEAD 为 `058d5c2ce87253897e1619b762f42cef9c3e71ee` 加既有未提交工作；执行期间其他任务将 HEAD 推进至 `a9242c2acef71e14a1a3097c3fb59bf5fc1a9d59`。本次测试以固定镜像和下列文件内容为准，不认定整个工作区通过验收：

| 测试源文件 | SHA-256 |
| --- | --- |
| `test-env/scripts/mail-webmail-poc-prepare.py` | `0535e3f809dbc4c3c357ca5a386868916f71ce1b11615c7a136bc8eedb6e2526` |
| `test-env/scripts/mail-webmail-poc-protocol.py` | `d809227344768d8844b263c7ac80cb3812dc50b29b24db61caaa4fe239018b95` |
| `test-env/scripts/mail-webmail-poc-server.py` | `15f846d9dad3e22b90cb9f784d55909cad6a531a56c0b759148d7b10e99a345d` |
| `test-env/playwright/mail-webmail-poc.mjs` | `29e49e17dea39797586bdf39efef4e0e0c88b40c6fa64829e0fb04bddb77258b` |

## e2e 执行记录

均于 2026-10-23 在 finance 专用环境执行；浏览器为本机已有 Google Chrome 的独立临时配置。

| 需求 ID | 脚本/入口 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| MAILPOC-R-001 | `mail-webmail-poc-server.py inventory`、专用 daemon 脚本 | finance 专用 namespace/socket | 2026-10-23 | 通过；五个容器标签、专用数据根、镜像/挂载已核对；本次容器、daemon、containerd、namespace 清理完成，默认 Docker 可达 |
| MAILPOC-R-002 | `mail-webmail-poc.mjs`：`login` | 真实 AD/LLNG/DMS/Roundcube | 2026-10-23 | 通过；Alice/Bob 都仅提交一次 IAM 密码后进入本人邮箱，恢复后再次通过 |
| MAILPOC-R-003 | `mail-webmail-poc-protocol.py auth` | TLS IMAP 993 / SMTP 587 | 2026-10-23 | 通过；两个有效认证及四个无效令牌/跨邮箱拒绝断言全部成立 |
| MAILPOC-R-004 | `mail-webmail-poc-server.py rename/verify-rename`、浏览器 `token`、协议 `verify-history` | 恢复副本 | 2026-10-23 | 失败；AD anchor 与真实 OIDC sub 不变、原邮件完整，但 Roundcube 新建 user_id=3，原 user_id=1 的偏好及别名身份未跟随 |
| MAILPOC-R-005 | 协议 `seed/verify-ui`；浏览器 `identities/send-aliases` | 原测试项目 | 2026-10-23 | 通过；shopping/work 共箱；真实界面各发一封，Bob 收到的 From 与 Return-Path 分别正确 |
| MAILPOC-R-006 | 协议 `alias-negative/removed-alias/verify-history` | 原测试项目 | 2026-10-23 | 失败；他人/已移除别名的信封均被拒绝，合法信封搭配这些可见 From 仍被接受；移除后既有邮件保留 |
| MAILPOC-R-007 | `mail-webmail-poc-server.py inventory` | 原项目与恢复项目 | 2026-10-23 | 通过本夹具范围；两次均无匿名持久卷，邮件及恢复状态均在明确 data 挂载集合中 |
| MAILPOC-R-008 | 协议 `snapshot/verify`；服务端 `backup-restore/verify-webmail` | 全新 `mail-test-restored` | 2026-10-23 | 通过；正文原文哈希、附件哈希、文件夹、持久标志及 SQLite 用户/身份/签名/非默认偏好一致 |
| MAILPOC-R-009 | 服务端 `queue-filter/backup-restore/queue-release`；协议 `queued/verify-queued` | 原队列→新目录恢复队列 | 2026-10-23 | 通过；精确 Message-ID 的真实 hold 邮件恢复后只投递一次，没有通过删除队列绕过检查 |

MAILPOC-R-010 的审阅结论：完成 DMS + Roundcube、mailcow + SOGo、Mailu 的组件/认证/别名/恢复与维护成本比较；成套产品本轮未部署，不能写成其验收已通过。见研究报告第 9 节。

## 保存集合与恢复证据

本夹具的 `data` 包括 DMS `/var/mail`、`/var/mail-state`（含 Postfix 队列）、日志与配置；Roundcube SQLite、附加配置与临时附件目录；测试 AD 的数据库/配置、IAM 配置/会话以及测试密钥材料。生产配置和凭据归属仍须遵循仓库规范，本夹具不是正式 Secret Store 设计。

- 停止全部五个服务后归档 `data/`、`fixture/` 和 `compose.json`，在不存在的新目录解包；用新项目/新网络运行，原数据未被覆盖。
- 归档：`/home/whl/anas-mail-webmail-test-20261023/evidence/stopped-workspace.tar`。
- SHA-256：`c214a1edbcfaaa379c3f28ccb8d51ecc1b89ddd63c42d794d99fe576a713692a`。
- 入站与界面收发基线：Alice 有 2 封 INBOX、2 封 Sent、1 封带附件及 Seen/Flagged 的 PoC 文件夹邮件；Bob 含 2 封真实界面别名邮件及拒绝反例中被错误接受的合成邮件。
- 待恢复邮件 Message-ID 为 `<mail-poc-queued-restore@mailtest.invalid>`，备份前队列 ID `2C79D4C6737`。恢复时 Postfix 根据 inode 重命名队列文件，因此验收依据是实际原文/Message-ID 与投递次数，不要求队列文件名不变。
- 原始结果、状态快照与日志在远端 `evidence/`；本地原始证据在 Git 忽略的 `test-env/reports/mail-webmail-poc-20261023/`。密码、令牌、Cookie、合成邮件和截图不进入 Git。两处保留副本不等于正式共享证据仓库；本计划未归档。

## 重试与验证边界

初始化候选修正过 Samba sysvol 的容器文件系统适配、TLS 私钥所有权、LLNG YAML 加载/签名密钥，以及 Docker internal 网络不发布端口的问题；失败初始目录保留在 `failed-bootstrap-1`。最终运行使用原生配置，没有修改 DMS/Roundcube 上游代码。

浏览器测试修正了回调捕获方式、带图标按钮的可访问名称与弹窗克隆控件定位；最终用原生界面保存/发送，并从 SQLite/IMAP 独立核验结果。没有把按钮点击或 HTTP 200 单独当成功依据。

第一次队列夹具被 submission 的 `sender-cleanup` 覆盖，探测邮件已投递；最终改用该服务的原生 `header_checks` 配置和不同的精确 Message-ID，确认 hold 后才备份。初始探测邮件保留，不作为队列恢复样本；普通内容基线与专门队列断言分开。

DMS 重启或全栈恢复初期曾连接重置、IAM UserInfo 返回 502；就绪后重新执行，最终通过记录如上。正式实现应增加依赖就绪门禁。本次只验证停机、同版本、同架构恢复，不覆盖在线备份、升级恢复、容量、公网投递、垃圾邮件策略、AD 自动预配、Portal CRUD、云端路由、外部 Module 只读/文件夹授权、完整受众/scope/撤权和登出矩阵。

## 检查与下一步

- [x] 真实统一登录、拒绝反例、界面别名收发与删除后历史保留。
- [x] 明确 data 挂载、空目录恢复、Webmail 状态与待投递队列复核。
- [x] 真实主地址变化测试，记录身份连续性失败而不豁免。
- [x] Python/Node 语法检查及本主题 3 个测试用例独立生成/校验。
- [x] 本次容器、daemon、containerd 和 namespace 已清理；原目录、恢复目录、备份保留；默认 Docker 仍可访问。
- [x] 本地完整备份副本解压流的 SHA-256 与远端原归档一致；本次 SSH 隧道已关闭。
- [x] DMS 阶段本主题 3 个用例独立校验、`docs:check-requirement-status`、`docs:check-plan-status`、`docs:check-status` 与 `docs:build` 通过；后续比较阶段门禁见下文。
- [ ] DMS 阶段最后一轮全量 `docs:check-requirements` 的需求归属检查通过，但 catalog 检查受其他任务的摘要漂移阻塞：`LLNG-READY-T-001`、`PORTAL-T-006`、`PORTAL-T-008`、`PORTAL-T-009`；本轮未修改这些用例。
- [x] MAILPOC-R-004：2026-10-24 的固定内部邮箱标识实验通过，详见追加记录；历史原生改址失败保留。
- [x] MAILPOC-R-006：2026-10-24 的 Go + Rspamd 发件授权通过，详见追加记录；10-23 禁用 Rspamd 的最小配置失败保留。

门禁历史：修正本计划记录格式后，曾有一轮需求覆盖及全部 13 个主题/96 个 active 用例的 catalog 静态校验通过；随后并行 LLNG/Portal 源码继续变化，DMS 阶段最后一轮出现上列 4 个其他主题的 implementation_digest 漂移。本主题在隔离目录重新校验仍通过，不能把它替代全仓门禁。其他主题的运行验收没有由本轮执行。DMS 阶段文档构建通过，只有既有的大 bundle 提示。原始门禁输出在该阶段本地证据目录的 `docs-*.log`。

10-23 当时的下一步是用同一组失败用例评估成套后端，或形成 DMS 所需适配的具体设计后确认；后续选择和实测见追加记录。mailcow 默认要求至少 6 GiB RAM，而 finance 总内存约 3.3 GiB，本轮未在该服务宿主上追加成套部署。涉及新服务、跨库同步或上游补丁时，按 AGENTS.md 在具体方案可评审后取得确认；本次测试授权不自动扩大到生产实现。

## 继续比较 mailcow + SOGo（2026-10-23）

用户要求继续后，完成 MAILPOC-R-010 的固定源码补充审阅；DMS 原有 8/10 结论及 MAILPOC-R-004、MAILPOC-R-006 失败保持不变。比较结果见研究报告第 10 节。本阶段没有修改生产 Module、需求矩阵或 DMS 测试实现，也没有新增部署、主机资源或上游补丁。

基线为 mailcow `2026-10` / `55982ac74bfd35581cc44b122ab5fc659e3ae90a`。Compose 声明 18 个服务，其中 SOGo 镜像为 `ghcr.io/mailcow/sogo:5.12.12-1`；这是声明值，尚未拉取镜像、记录运行 digest 或确认实际版本。取回 24 份相关固定源码，保存在 Git 忽略的 `test-env/reports/mailcow-poc-20261023/source/`；`source-sha256.json` 的 SHA-256 为 `66e439bfc7895d4fdd60be0e046ccbacab387c17c78a0531b2a18a8d1c3e1c32`。本地缓存不是共享证据归档，复核可使用研究文中的固定提交链接。

| 关键源文件 | SHA-256 |
| --- | --- |
| `docker-compose.yml` | `de6731c40e174fafd70f576b35a43c56a0121014b7dc2f4b55edecaa3dd981ee` |
| `data/web/inc/functions.inc.php` | `eb8832d4fe56440efa6af04085ea14c197187455db0cf3ac83ebe99bce7f9614` |
| `data/web/inc/functions.mailbox.inc.php` | `861b3b408818e5ba575022c3102dc42eab5bb82996906e96093aa9293d81917a` |
| `data/Dockerfiles/dockerapi/modules/DockerApi.py` | `2e9f2fc8f44902e40fce5842fe230bec539f320039e9c05c3dbc7e9d9b412ca2` |
| `data/conf/rspamd/local.d/composites.conf` | `feb378cd12288027381e431d477dbc22415194061b22c84a97379b5a51f27d23` |
| `helper-scripts/backup_and_restore.sh` | `623f3bdf2acfb5dbcda0f52d8e2c8fdb1dd62079a67053edf9facbfdba8a12c7` |

实测准备按已有比较问题安排，不把下表当成 DMS 需求通过记录，也不静默新增或豁免验收条件：

| 对照问题 | 执行及独立观察方式 | 当前状态 |
| --- | --- | --- |
| 统一登录与本人归属（参考 MAILPOC-R-002/003） | 真实测试 AD/IAM→mailcow→SOGo；Alice/Bob 各观察本人收件箱，检验无二次密码和跨用户拒绝；协议应用密码与浏览器令牌分别记录 | 未执行；不能沿用 DMS OAuth 协议通过结果 |
| 主地址变化（参考 MAILPOC-R-004） | 先保留 anchor/sub，仅改变 AD mail，记录原生 OIDC 行为；再从独立基线调用原生同域改名入口，对比邮件原文及 SOGo 偏好、签名、发件身份 | 源码已核对；浏览器及迁移结果未执行 |
| 别名共箱/界面发送（参考 MAILPOC-R-005） | 两个别名投递到 Alice；实际 SOGo 发信，Bob 用独立 IMAP 核验 From/Return-Path | 未执行 |
| 发信拒绝（参考 MAILPOC-R-006） | 保留原生 Rspamd；分别提交合法信封配他人 From、已移除别名 From，另测非法信封；观察 SMTP 响应及 Bob 是否实际收到；覆盖外部 587 和 SOGo 的内部 588 链路 | 默认规则不能单独证明授权；实测未执行 |
| data 与恢复（参考 MAILPOC-R-007/008/009） | 将明确状态集合放 data，检查容器挂载；停机归档后恢复至不存在的目录，比较正文/附件/文件夹/标志、SOGo 数据及真实待投递队列 | 已核对默认命名卷与备份脚本依赖；运行适配及恢复未执行 |

原生改名额外发现 SQL 提交后未检查邮件目录移动和 SOGo 命令结果。这里只记录已确认的调用缺口和待复现风险；没有把新故障恢复场景追加为验收标准，没有实现跨系统恢复框架。若选型必须依赖此适配，先形成具体设计并确认。

- [x] 完成固定版本的 OIDC 查找/建号、原生改名、发信策略、SOGo 内部登录与保存集合审阅。
- [x] 更新研究报告及中英文研究索引，保留原 DMS 失败结论。
- [ ] 获得资源足够的 Linux 测试目标并完成只读容量/隔离预检。finance 上次总内存约 3.3 GiB，低于默认 mailcow 6 GiB + 1 GiB swap 要求；已询问可用 SSH 目标，建议至少 8 GiB 以容纳测试 AD/IAM，尚未收到新目标。
- [ ] 在合适目标完成上述实际浏览器、SMTP/IMAP 和空目录恢复，再更新后端选择。

目标选择遵循 `test-env/README.md` 的逐次明确主机规则；没有将已有 finance 测试目录复用成新的成套环境。本次只完成可独立进行的研究，mailcow 的运行验收继续保持未执行。

本阶段文档检查：两个状态索引已重新生成，`docs:check-requirement-status`、`docs:check-plan-status`、`docs:check-status` 及本次文件空白检查通过，24 份来源文件的 SHA-256 与清单一致。全量 `docs:check-requirements` 的 28 份文档/764 项需求归属通过，但 `PORTAL-T-008`、`PORTAL-T-009` 实现摘要仍过期；未修改其他主题的摘要。`docs:build` 在生成 Module 文档时被当前工作区 `ai_agent` 配置拒绝，报 `module ai_agent capability family must select its own names`。另直接运行 VitePress 检查渲染，因跳过 Module 文档生成而出现 101 个死链并失败，不能替代标准构建，也没有关闭死链检查。日志位于 `test-env/reports/mailcow-poc-20261023/docs-*.log`。本次不宣称全仓门禁通过。


## Go 中间层实机验证（2026-10-24）

用户明确指定 `ssh whl@finance.hlong.wang`，并已选择 Go 统一邮件认证、AD 保存别名、PostgreSQL 复用和混合邮箱模型。本轮实施仅限 `test-env/mail-go-poc/` 隔离原型，没有创建 `modules/mail/`，没有改动生产 AD/IAM、默认 Docker 容器或公网路由。整体生产方案复杂度仍为高，实验只验证关键接法与边界。

新增环境为 `/home/whl/anas-mail-go-test-20261024/mail-test-go`，项目 `anas_mail_test_go_1024`，独立 socket `/run/anas-mail-test-1024.sock`、Docker/containerd root、网络命名空间 `anas-mail-test-1024`。命名空间入口 `10.253.24.2`，容器池 `10.244.0.0/16`；容器池外 NEW 出站被拒绝。18884/19994/15888/15254 只通过本次 SSH 隧道访问，没有修改公网 DNS。旧测试 daemon 仅短时恢复用于只读导出缓存镜像，随后再次停止。

Go 复用根 go.mod 的 go-oidc、oauth2、go-ldap、pgx；新原型没有增加 Go 依赖。PostgreSQL 使用现有 ANAS 18.4.0-r5 镜像在本次隔离目录启动，不能解释成已经接入生产数据库 Provider。固定邮箱登录名为合成的 `box-01/02/03@boxes.mailtest.invalid`，分别对应稳定目录和 Roundcube 本地用户；它们不是 AD anchor。公开地址在 Go 页面和 Roundcube 用户名区域显示，技术地址禁止发信。

三个人员邮箱资源只对应两个测试 AD 用户：Alice 的主箱带 shopping/work 别名，projects 为独立箱；Bob 为另一个人。Go 在真实 OIDC 授权码 + PKCE 后按已验证的 sub 匹配 anchor，从 PostgreSQL 取得本人箱列表。Roundcube 不配置 IAM OAuth，通过一次性交接和 PHP 插件取得 Go 的单箱凭据，Dovecot 通过 HTTPS POST 内省验证。Rspamd Lua 仅提取 MTA 已认证的邮箱名、信封与 MIME From，交给 Go 做地址授权。

### 已观察到的适配与限制

- Go 管理页添加/移除 webui 别名，AD 的 proxyAddresses、Go 授权和 DMS 路由分别从独立通道核对。Go 管理页还不是完整人员/邮箱开通产品；独立箱使用测试预配。
- Roundcube 真实界面分别发送 shopping/work/projects，独立 IMAP 核验 From、Return-Path 和两个箱的 Sent。直接 SMTP 伪造他人地址、同人另一个邮箱、已删除别名或技术地址均被拒绝。
- DMS 实际为 Dovecot 2.4.1-4、Postfix 3.10.13、Rspamd 4.1.5。Dovecot 的命名配置块由最终 doveconf 核对，不沿用先前 2.3 文档猜测运行行为。
- Roundcube XOAUTH2 连接 API 要求完整的 `Bearer <token>`；只传裸 token 的初次试验被拒绝。修正插件后真实协议连接通过。
- `no-referrer` 使浏览器表单来源与严格 CSRF 检查不兼容，改为仅发送来源的 `strict-origin`；未移除 CSRF 检查。
- 地址表更新会触发 DMS 重载，曾在重载窗口出现 `Internal login failure`。最终 Go 仅在内容变化时替换文件，测试等待原生配置应用完成。生产必须补配置就绪状态和失败处理，不能宣称零中断热更新。
- 离线域的入站测试最初先被发件域 DNS 检查暂拒；最终用标准空反向路径，要求已删除地址被拒且有效地址被接受，避免把无关拒绝当成功。
- 原型是同步直线的 AD→PostgreSQL→配置写入，没有跨存储失败恢复、目录事件同步、自动开通全部邮箱或生产权限委派。新建测试 AD 的管理员绑定仅用于本次隔离夹具。
- 不覆盖完整 IAM 禁用/全局登出、外部客户端接入策略、并行多标签页草稿隔离、真实 sAMAccountName 修改和新架构的全量恢复。原有停机恢复通过记录保留，但不能转移为 Go/PostgreSQL 新架构通过。

### 本轮 e2e 执行记录

| 需求 ID | 脚本/入口 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| MAILPOC-R-001 | server.py inventory、专用 daemon/namespace 守卫 | finance 本次独立项目 | 2026-10-24 | 通过；七个容器标签、工作区、数据根与挂载核对；清理结论见下文 |
| MAILPOC-R-002 | browser.mjs initial/baseline/continuity | 真实 AD/LLNG→Go→Roundcube→DMS | 2026-10-24 | 通过；Alice/Bob 各仅一次 IAM 密码；Roundcube 通过 Go 交接，未向 IAM 注册 OAuth 客户端 |
| MAILPOC-R-003 | browser.mjs、protocol.py | TLS IMAP/SMTP、Go、PostgreSQL | 2026-10-24 | 通过；同人跨箱、异人、无效 token 拒绝；INBOX/Sent 和当前发件身份隔离 |
| MAILPOC-R-004 | server.py rename-fixture/rc-state、browser continuity、protocol continuity-before/after | 显式测试预配改址 | 2026-10-24 | 通过兼容性实验；AD anchor 不变，两个箱 INBOX/Sent/Drafts 原邮件哈希相等，全部 Roundcube user_id 和偏好相等，新主址为默认身份、旧址保留同箱；没有实现自动 sAMAccountName 修改 |
| MAILPOC-R-005 | browser initial、独立 IMAP 读取 | 真实 Roundcube 界面→Bob 收件箱 | 2026-10-24 | 通过；shopping/work 共箱，projects 独立；From/Return-Path 均符合选定身份 |
| MAILPOC-R-006 | protocol.py 直接 SMTP 反例、删除后旧邮件查询 | DMS/Rspamd→Go 授权 | 2026-10-24 | 通过；他人、同人另一箱、已删除别名的信封/可见 From 被拒绝，技术地址不能发送，旧邮件保留 |
| MAILPOC-R-007 | server.py inventory | 七个容器显式 data 挂载 | 2026-10-24 | 通过夹具范围；新增 PostgreSQL、邮件和 Webmail 状态在明确保存集合，零匿名卷 |
| MAILPOC-R-008、MAILPOC-R-009 | 新架构停机备份/空目录恢复 | Go/PostgreSQL 新基线 | 2026-10-24 | 未执行；10-23 原生通过保留但不转移，本计划继续 partial |
| MAILPOC-R-011 | browser.mjs initial/baseline/continuity | 真实 Go 服务端交接与 DMS 内省 | 2026-10-24 | 通过；Go 作为邮件认证入口，Roundcube 无 IAM 客户端，令牌存 PostgreSQL 哈希表并按单箱签发 |
| MAILPOC-R-012 | browser.mjs、protocol.py | 同人两个箱与另一个人的箱 | 2026-10-24 | 通过；INBOX/Sent 独立，当前箱发件身份隔离，同人跨箱 token 也拒绝 |
| MAILPOC-R-013 | browser alias 表单、protocol.py、server.py ad | Go/测试 AD/DMS | 2026-10-24 | 通过；新增/删除均在 AD 独立观察，删除后收件地址拒绝、有效别名仍接受，发件拒绝及历史保留通过 |
| MAILPOC-R-014 | protocol.py lifecycle、browser refresh | 90 秒 access token、一次性交接和真实续期 | 2026-10-24 | 通过本条新认证/记录边界要求；错误绑定/交接重放/刷新重放、过期令牌、注销后的新 IMAP/SMTP 均拒绝，浏览器过期后自动续期；不等于全部旧连接已撤销 |
| MAILPOC-R-015 | protocol.py lifecycle 停止/恢复 Go | 既有 SMTP 与新的 IMAP 连接 | 2026-10-24 | 通过“不绕过”要求；既有 SMTP 新提交得到 4.7.1，新的 IMAP 在客户端 20 秒超时内未完成认证；不是立即返回拒绝，服务端重试可持续到 Go 恢复 |

协议测试分段记录：`attempt-4-protocol-result.json` 保留前半矩阵的成功断言和随后未分类的客户端超时失败；最终 `lifecycle-result.json` 单独复验禁用、故障、过期、轮换和注销全部路径，将超时与明确拒绝分开记录，没有删除历史失败。最终生命周期结果包含 11 个成功断言和 2 个实际边界观察。浏览器 initial、refresh、baseline、continuity 分别通过；continuity-after 四项比较全部通过。没有把 SQL 测试禁用说成完整 AD 人员禁用功能。

### 已确认的未解决边界

- **旧 IMAP 连接：** 邮箱 disabled 后同一连接 NOOP 仍为 OK，新登录已被拒绝。
- **旧 SMTP 连接：** Go 邮件会话注销后，事先认证的 SMTP 连接仍接受新的合成邮件，新登录已被拒绝。当前 milter 只拿到稳定邮箱 SASL 用户名，无法判断对应哪一个已注销 Go 会话。生产需要会话关联或连接终止设计；不能以“Go 统一认证”宣称已经完整撤权。
- **故障延迟：** Dovecot HTTP 客户端默认请求超时为 1 分钟，本轮客户端 20 秒先超时，恢复 Go 后服务端仍可能完成该已关闭连接的验证。生产应设置明确的服务器超时和重试预算。
- **配置与恢复：** AD/PostgreSQL/DMS 的部分失败恢复、目录事件同步、配置就绪确认和新基线空目录恢复未完成。Roundcube 只验证单活动邮箱的顺序切换，不承诺多标签页同时编辑互不干扰。

### 制品与证据

仓库 HEAD `a9242c2acef71e14a1a3097c3fb59bf5fc1a9d59` 加既有未提交工作。本轮没有认定整个工作区通过验收。固定源码清单 `source-sha256.json` 的 SHA-256 为 `9d330cb469d2b72556d97ac526c87985f937a9dcbb6235bae7305660ca356f67`；Linux amd64 Go 二进制为 `841a40a6af2025141ca0b2b6971c321fe03397cec9da508a9f7b49e3f801b852`。Go 为 1.26.6，PostgreSQL 镜像为 `sha256:339c4dd98a38d35ef9ad4c9164231c9905c4cbd046287813fb8170f5f3ddd362`；其余镜像 ID 同历史固定表，运行清单另保存在本轮 inventory.json。

私有原始记录：本机 `test-env/reports/mail-go-poc-20261024/`，远端 `/home/whl/anas-mail-go-test-20261024/evidence/`。测试源副本和摘要两处保留；生成的凭据、邮件、数据库在远端专用 data。它们不是正式共享归档，计划不归档。原型 Go/Python/Node 语法与静态检查、本主题四个用例独立 catalog 校验通过，完整文档门禁及清理结果见最后记录。


### 本轮收尾与下一步

- [x] 本轮 Go、PHP、Python、Node 检查通过；最终源码/二进制 SHA-256 与保留清单一致，远端运行二进制和 main.go 摘要独立核对一致。
- [x] 本主题四个用例独立 catalog 校验通过；全量 `docs:check-requirements` 通过：28 份文档、769 项需求，13 个主题/99 个 active 用例。本次没有刷新其他主题的摘要来掩盖问题。
- [x] 需求/计划状态索引已重新生成，`docs:check-requirement-status`、`docs:check-plan-status`、`docs:check-status` 通过。文档构建通过（v0.1.1，14.72 秒），只有既有大 bundle 提示。
- [x] 七个容器和当前 Compose 网络已移除，专用 Docker/containerd 与 namespace 已停止/删除，1023 缓存 daemon 也没有遗留运行 socket；本次 SSH 隧道已关闭。默认 Docker 29.7.2 仍能正常响应；没有据此声称对全部生产服务做过业务验收。
- [x] 专用 data、源码、运行日志、前后状态、失败重试和成功结果保留。没有删除邮件来通过测试，没有对默认 Docker 做 prune。远端 `cleaned-up-at.txt` 记录清理时间，本地 `remote-evidence/` 有清理日志副本。
- [ ] 正式实现前补会话与连接关联、Roundcube 撤销和 Dovecot 踢线方案，明确 SMTP 长连接如何及时撤权。
- [ ] 将地址操作产品化：最小权限目录 Provider、冲突/并发和部分失败、配置应用确认、独立邮箱开通以及目录外部变化订阅。
- [ ] 新架构完成停机空目录恢复和队列恢复，验证不复活旧会话；完整 IAM 禁用/全局登出及多标签页场景另行验收。

当前结论为“Go 统一邮件认证与管理的核心接法可实现”，不是“生产邮件系统或完整撤权已验收”。本轮可实现性实验完成，配套计划继续 partial（新架构恢复尚未执行），不归档。下一步以这些实际缺口完善正式 Module 设计，不需要重新选 Webmail 才能继续。


## 2026-10-24 公司通讯录与会话撤权扩展验证

以下保留该轮结束时的 17/20 历史结论；同日晚的补测结果见文末。

用户确认共享通讯录延期二期，本轮只验证 AD 用户/组、公司只读目录、来源/授权扩展接口，以及既有 Go、Roundcube、IMAP、SMTP 会话撤权。未实施生产邮件 Module，未改变 sAMAccountName 的管理行为，未实现共享簿本体、共享簿编辑或组邮件列表。

本轮新建 `/home/whl/anas-mail-go-test-20261024/mail-test-directory`，旧 `mail-test-go` 数据、源码和证据保留。复用已停止的专用 Docker 数据根、socket `/run/anas-mail-test-1024.sock`、namespace `anas-mail-test-1024` 和 SSH 测试端口，不接入宿主生产 AD、IAM 或邮件。七个容器的工作区标签、持久挂载与实际镜像 ID 已核对；DMS、Roundcube、Samba、IAM、nginx 固定版本同上一轮，PostgreSQL 镜像固定 `sha256:339c4dd98a38d35ef9ad4c9164231c9905c4cbd046287813fb8170f5f3ddd362`。

实现仍是隔离原型。Go/PostgreSQL 复用根 go.mod 依赖；Roundcube 使用薄插件和只读通讯录后端；Samba 用现有事件规范化器的测试副本，监听资料属性的扩展仅在夹具中。DMS 内新增一个受 Supervisor 管理的受限 HTTP 适配器，只接受 Go 会话引用并执行固定 `doveadm kick`；没有赋予 Go Docker socket 或通用执行权限。复杂度高，集中于七服务、事件与数据库状态、三类会话撤销及失败重试，不能按单个网页功能估算维护成本。

### 扩展 e2e 执行记录

| 需求 ID | 脚本/入口 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| MAILPOC-R-016 | `directory_browser.mjs directory`、`directory_protocol.py directory` | finance 新测试域/真实 Roundcube | 2026-10-24 | 通过；真实用户、组成员筛选、资料修改和人员新增删除均收敛；Roundcube 列表、显示更新与写信收件人补全通过 |
| MAILPOC-R-017 | 通讯录 sources/query/write 与错误服务/会话反例 | 同上 | 2026-10-24 | 通过；只返回 company 只读来源，只公开主地址；未启用共享来源、写入、无服务认证和无效会话均拒绝 |
| MAILPOC-R-018 | `disable/direct/nested/delete`，旧 Cookie 与已有 IMAP/SMTP | 同上 | 2026-10-24 | 通过；禁用、删除、直接及嵌套退组均在 60 秒内使旧 Cookie、IMAP 与 SMTP 新提交失效；恢复准入不复活旧会话，Bob 保持有效 |
| MAILPOC-R-019 | `logout/iam`，真实 Go 退出及 LLNG 后台登出 | 同上 | 2026-10-24 | 通过；Go 本地退出与真实 LLNG 后台退出均使目标旧 Cookie/凭据/IMAP/SMTP 失效，同人另一个登录及 Bob 均保持 |
| MAILPOC-R-020 | `recovery`，消费者重启/journal 缺口/LDAP 故障 | 同上 | 2026-10-24 | 通过；9 项断言成立，重启继续消费，缺口被明确识别并核对，LDAP 失败不推进游标/误删人员，恢复后收敛且联系人 ID 不变 |

### 修正、证据与边界

首轮失败记录保留：Samba `-i` 把审计送到 stdout，改 `-F` 后产生实际文件事件；Dovecot 配置块格式、只传 passdb 标记和未受管理的后台踢线服务导致初次登录或断开失败，最终使用同时传入 passdb/userdb 的会话标记与 Supervisor；Roundcube 列表初始化需等待就绪，联系人列表展示姓名，补全来源须在请求初始化注册；负向认证原 6 秒客户端超时不足，改 20 秒等待真实拒绝。测试脚本的域名解析错误和旧任务路由探测失败单独保留，未算成通过。

本地原始证据在私有 Git 忽略目录 `test-env/reports/mail-go-poc-directory-20261024/`；失败与重跑归档在其 `attempt-*` 子目录，远端证据在 `evidence-directory/`。本计划仍未归档：原生组合的历史恢复通过不能覆盖新增 Go/PostgreSQL/事件状态，MAILPOC-R-008、MAILPOC-R-009 仍未执行。


最终 Go 构建环境为 Go 1.26.6（darwin/arm64 交叉编译 linux/amd64，CGO=0）。早期验证二进制 SHA-256 `923958ed71a37faa7d021053c962da31c287892b703636022536beb20893ca51`；最终制品为 `7287d2f4194c08b80fb73c07107d1df353c8907a4663084a64bd1dfdacd99e0b`，两次 Go 源码仅 TEST_CASES 追踪注释变化，逻辑一致。IAM 和恢复场景使用前者；最终公司目录、本地退出、AD 撤权时延及删除场景使用后者。完整源码与逐文件摘要在 `evidence-directory/final-source.tar.gz`、`artifact-manifest.json`；源码包 SHA-256 `860fab6aa7652d43b90e1b865df0818599fb1489eee7a576751085567cb350de`。

测试边界：60 秒目标仅覆盖本次健康依赖、单 DC、小规模合成域。没有验证密码变化、锁定、自动到期、多 DC、生产者崩溃窗口、长期失联、冷启动时 AD 不可达或高并发撤权竞态。LDAP 刷新异常当前还会推迟同一轮待踢线队列处理，生产应将两者失败隔离并独立验收；不得把本轮健康链路通过外推为所有故障下立即退出。既有页面已下载的邮件不可能由会话撤销追回。共享簿权限本体属于二期。


### 最终结果与时延

8 组场景最终全部通过，共 148 个浏览器/协议断言（含每组重复的真实登录断言）。历史失败记录保留，不计入最终通过数。MAILPOC-R-016～020 的本轮确认范围通过；按当前变更范围复核后，整体计划为 **17/20**：新版 SMTP 会话标识后的 Roundcube 实际发送尚需回归（R-005），新增 Go 架构的空目录与队列恢复（R-008、R-009）仍未执行，不标记全部完成或归档。上一轮 R-005 的真实界面发信通过记录及固定基线完整保留；本轮直接 SMTP 协议通过与浏览器通讯录补全不替代该界面发送验收。

| 触发 | Go 会话失效 | 旧 Roundcube Cookie 拒绝 | IMAP 断开及旧 SMTP 新提交拒绝均确认 |
| --- | --- | --- | --- |
| Go 本地退出 | 0.484 秒 | 0.725 秒 | 1.145 秒 |
| IAM 后台退出 | 1.693 秒 | 2.003 秒 | 2.995 秒 |
| AD 禁用 | 8.440 秒 | 8.698 秒 | 9.134 秒 |
| 直接退组 | 8.216 秒 | 8.528 秒 | 8.926 秒 |
| 嵌套退组 | 13.967 秒 | 14.216 秒 | 14.624 秒 |
| AD 删除 | 5.542 秒 | 5.817 秒 | 6.243 秒 |

时延是本次单次测量，从触发前开始累计，含测试 SSH/API 往返；嵌套场景还包含设置嵌套准入与 3 秒保持检查，因此是保守上界，不是吞吐或分位数指标。资料修改可见耗时 6.681 秒。SMTP 记录的是拒绝后续提交，不代表 TCP 连接被关闭。

汇总证据 `final-results.json` SHA-256：`c6ec212a48fd8c6a407d03c517661087e2af987aaf7cba449ba18a365ee0c4be`；实际运行配置及事件生产者三个源文件摘要在远端 `runtime-file-hashes.json`。


### 收尾与仓库检查

- 七个测试容器及其 Compose 网络已移除；专用 Docker、containerd、namespace 与 SSH 隧道已关闭。旧、新测试数据和固定镜像保留，默认 Docker 只读检查仍可达。清理证明为远端 `evidence-directory/cleanup.json` 与 `compose-cleanup.json`，不是删除测试资料后重新生成结果。
- `go vet ./test-env/mail-go-poc`、Linux amd64 构建、Python AST、Node 语法、实际容器内 PHP lint 通过。
- `docs:check-requirements` 通过（28 份需求文档、774 项需求；13 个测试主题、100 个 active cases）；`docs:check-requirement-status`、`docs:check-plan-status` 通过。本主题独立生成目录后再做全仓校验，未改其他主题摘要。
- `docs:build` 通过；仅有现有打包体积大于 500 kB 的提示，无构建错误。
- 没有提交 Git，没有部署生产邮件 Module。下一步为新版 Roundcube 发送回归、新架构备份/恢复、生产目录新鲜度与故障撤权隔离、地址应用确认等正式设计/验收；共享通讯录仍为二期。

私有执行证据归档：`/home/whl/anas-mail-go-test-20261024/evidence-directory/execution-evidence.tar.gz`，SHA-256 `e5b4fd44fcb0b132436747b3ddfb5a23942c849a7cca80774963299f963ac961`。该包含合成测试凭据与会话，权限 0600，仅在本机 Git 忽略目录及已授权 finance 私有目录保留；尚未登记正式共享证据库。

## 2026-10-24 新版界面发信与完整恢复补测

用户明确要求“继续补齐测试”。本轮只补 MAILPOC-R-005、R-008、R-009，复核 R-001/R-007 的隔离和保存集合；不部署生产 Module、不扩大共享通讯录范围。确认矩阵现为 **20/20 已完成**，此前失败、17/20 状态和各轮固定基线完整保留。mailcow 的历史候选验证仍未执行，不把本次结果外推到其他产品。

### 制品与隔离基线

- 仓库 HEAD `a9242c2acef71e14a1a3097c3fb59bf5fc1a9d59` 加既有未提交工作；只修改邮件测试脚本、目录和文档，没有修改生产 Go/Module 行为，也没有提交 Git。
- 源夹具 `/home/whl/anas-mail-go-test-20261024/mail-test-recovery`，恢复目录 `mail-test-recovery-restored` 在恢复前不存在。项目分别为 `anas_mail_test_go_recovery_1024` 和其 `_restored` 项目。复用专用 Docker root、socket、namespace 和本机 SSH 测试端口，容器新增外连被阻断；前两轮数据保持原样。
- 七个镜像固定为上一轮实际 image ID；Go 二进制仍为 `7287d2f4194c08b80fb73c07107d1df353c8907a4663084a64bd1dfdacd99e0b`，应用认证、目录和插件逻辑未修改。测试入口为 `browser.mjs initial/restored`、`recovery_protocol.py`、`recovery_server.py`，用例为 MAILPOC-T-006。

### 逐项结果

| 需求 ID | 检查 | 结果 |
| --- | --- | --- |
| MAILPOC-R-005 | 真实 IAM 登录、Roundcube 使用 shopping/work 两个别名发信，独立 IMAP 核验；同时回归 projects 独立箱 | 通过；三封真实界面邮件 From/Return-Path 正确；shopping/work 共箱，Sent 为主箱 2 封、独立箱 1 封 |
| MAILPOC-R-007 | 源项目与恢复项目各七容器的实际挂载检查 | 通过；无匿名持久卷，全部 bind 路径位于各自工作区 |
| MAILPOC-R-008 | 全部服务停止后归档完整 data/fixture/compose，解包至新目录；重新启动并登录 | 通过；1,560 个 data 文件/链接内容、属主和权限逐项一致；三个邮箱正文/附件哈希、全部文件夹及持久标志一致；Roundcube 用户 ID、身份与非默认排序偏好一致 |
| MAILPOC-R-008 | 新架构应用状态复核，登录前后各比较一次 | 通过；PostgreSQL 人员归属、三个邮箱、别名、公司目录和组保持一致；订阅游标 1 保留且原事件日志仍在；公司目录可查询，跨箱凭据仍拒绝 |
| MAILPOC-R-009 | 保留真实 Postfix hold 邮件，恢复后导出原文并与源队列文件独立比较，再释放 | 通过；原文 SHA-256 一致，正文/附件/别名发件地址正确，恢复后只投递一次，队列最终为空 |
| MAILPOC-R-001 | 独立项目、固定镜像、出站阻断、拒绝已有恢复目标 | 通过；已有目标反例在写入前拒绝，源目录保留；收尾状态见下文 |

本轮浏览器/协议结果合计 **50 项断言通过**：初始真实 UI 18、恢复后真实 UI 13、别名收件及数据准备 7、邮件基线 1、hold 提交 2、恢复后协议 5、队列投递 4。另有服务端应用状态、挂载、逐文件解包和目标覆盖拒绝检查；不混入上述 50 项计数。

### 停机、队列与失败记录

第一次同时停止全部服务的尝试被测试拦截，没有生成备份：目录退出 127、Go 退出 2，IAM 等待 45 秒后退出 137；无 OOM。确认 IAM 候选镜像 PID 1 为 shell，未转发 TERM，改为 nginx 原生平滑退出；停机顺序改为入口、Webmail、DMS、Go、IAM、目录、PostgreSQL。Samba 使用 `smbcontrol all shutdown`。第二次 DMS/IAM/Samba/PostgreSQL/入口/Webmail 均退出 0，Go 仍退出 2；Go 未实现优雅停机，原因未进一步定位，不能声称所有服务均优雅退出。备份时全部进程已停止，无 OOM/强杀，数据库与邮件存储正常停止；最终数据恢复通过。生产应单独实现并验收停机生命周期。

两个测试比较方式错误也已保留并纠正：Postfix 队列中的实际收件人已从 `work@mailtest.invalid` 展开为 `box-01@boxes.mailtest.invalid`，因此同时核验 original_recipient 与展开目标；恢复启动后短队列 ID 从 `CEE984F38BC` 变为 `CEE984F3B6D`，原生历史实验已记录 inode 更名行为，不能把队列文件名不变新增为 R-009 判据。最终使用保留的源队列文件和恢复队列分别执行 `postcat -bh` 比较完整邮件哈希，并核验 Message-ID、信封和单次实际投递，没有重新提交或删除队列来通过测试。

待投递邮件 Message-ID 为 `<mailgo-queued-recovery@mailtest.invalid>`；两个队列导出的原文 SHA-256 均为 `cfb367db9a232fcf4d34a9f552f21279e6296517d524df308c5e2486bdead61d`。停机归档 `evidence-recovery/stopped-workspace.tar` 为 **140,810,240 字节**，SHA-256 `a6c3ab49d5928471c78992d39c7af49598624a9f40da9c67fc71bfb9388e2dc8`。记录中的恢复过程是同版本、单 DC、小规模合成域的停机恢复，不是在线备份、跨版本升级或生产 ANAS 备份命令验收。

### 证据与后续边界

原始证据位于本机私有 Git 忽略目录 `test-env/reports/mail-go-poc-recovery-20261024/` 与 finance 私有目录 `/home/whl/anas-mail-go-test-20261024/evidence-recovery/`。源目录、恢复目录、停机归档和前两轮证据均保留。测试矩阵已通过，按仓库门禁将计划移入 archived/ 并保留全部历史记录；原始证据继续由上述私有目录保存，尚未登记正式共享证据库。

本次不补生产目录同步与撤权队列的故障隔离、不实现 AD/PostgreSQL/DMS 地址操作的跨系统失败恢复、不实现 sAMAccountName 修改或多标签页并行写信。共享通讯录仍是二期。下一阶段是基于这些固定制品与已知限制细化正式邮件 Module 设计及其独立验收要求。

### 补测收尾与检查

- 七个恢复测试容器与网络已移除，专用 Docker/containerd 均为 inactive，socket 与 namespace 均不存在，SSH 隧道已关闭；四轮源/恢复目录保持，默认 Docker 只读检查仍可达。证明为远端 evidence-recovery/cleanup.json 与 compose-cleanup.json。
- Python AST、Node 语法、测试目录生成和全仓目录校验通过；docs:check-requirements（28 份需求文档、803 项需求，13 主题、101 个 active cases）、docs:check-requirement-status、docs:check-plan-status 均通过。docs:build 通过，只有既有分包体积提示。未运行无关生产代码全量测试。
- 最终测试源码包 final-source.tar.gz SHA-256 `6706a0af1a50693ee7c9f0c7890100bd627873850c482a36ab3e08adf99fd65e`；50 项结果 final-results.json SHA-256 `fd44d7fa9de6f821fd0c28efee1cbb71dd0d69d32d3a318d7c1f397c0c5788ae`。完整运行配置、固定二进制和事件生产者位于停机归档；源码包包含逐文件 manifest。
- 完成状态触发仓库门禁，计划已迁移到 archived/，研究报告、测试目录和索引引用已同步。此前生成器缺少支持文件追踪标记、完成计划仍在活动目录的问题已修正；没有修改或豁免验收要求。

补测执行证据包 `evidence-recovery/execution-evidence.tar.gz` SHA-256 `a3142878d645d9c365fae43ee7bb650c6491d81087ef2dbe9c177a867c20803c`，包含 65 个私有执行/检查文件及 manifest，单独保存于本机与 finance。约 141 MB 的停机备份经压缩复制到本机后解压，SHA-256 与远端原件一致（`backup-copy-check.json`）；传输中断的未完成副本已被完整校验副本替换。证据含合成凭据/会话，目录 0700、归档 0600，不公开、不提交 Git。
