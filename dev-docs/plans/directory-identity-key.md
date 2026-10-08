---
doc_type: plan
status: implementing
created: 2026-09-20
updated: 2026-10-07
---

# 目录身份键实施计划

验收依据是[目录身份键要求](../requirements/directory-identity-key.md)的需求矩阵。规则本身在
2026-09-20 成文,触发它的是 Forgejo 身份收敛为 OIDC-only 时发现的一件事:改名之后按用户名对账
会误杀在职者,而这个坑在每一个"按标签认人"的 Module 里都存在,只是还没有人逐个看过。

产品尚未上线,**没有历史账号需要兼容**,因此直接取最优形态:主体标识符本身就是 anchor,不建
`sub ↔ anchor` 映射层。

## 1. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：把规则写进规范与文档标准 | R-001、R-002、R-004、R-005、R-010、R-011 | 已完成；2026-09-20 成文 |
| M1：逐 Module 盘点与文档补全 | R-006 | 已完成；2026-09-20，12 个在范围内的 Module 的中英文 README 与技术文档补齐，`module-iam-support` 增加匹配键列 |
| M2：主体标识符切换为 anchor | R-007—R-009、R-012、R-013 | 实施中；Casdoor r10 OIDC 与签名 SAML 协议、两 client 隔离已通过；LLNG r12 OIDC anchor 通过；Authentik 已过时，不纳入当前发布整改；LLNG SAML 待完成 |
| M3：改名复用身份的真实 E2E | R-003 | 实施中；Casdoor/LLNG + Nextcloud r11 同账号与原文件通过，其他 Module 和协议仍待完成 |

## 2. M0 检查表

- [x] 新增[目录身份键要求](../requirements/directory-identity-key.md),把"anchor 是唯一身份键、
      标签只用于登录/搜索/显示"从 `samba-ad-user-planning` §5 的枚举升级为可验收的规范条目。
- [x] [Module 文档生成标准](../../docs/developer/module-documentation.md) §3 增加《目录属性变更说明》
      必需章节(中英文),要求逐行回答七种目录侧变更,并单独写明匹配键与兜底路径、标注证据等级。
- [x] 同标准 §4 要求技术文档写清实现侧:身份存在哪张表/哪个字段、每次登录刷新什么、撤权经哪个
      接口、没有自动路径时技术阻碍是什么。
- [ ] [Samba AD 用户与权限规划](../../docs/architecture/samba-ad-user-planning.md) §5 的枚举改写为
      指向本要求的规范陈述。

## 3. M1 盘点范围与结论

逐个 Module 回答三件事,结论写进它自己的 README 与技术文档:

1. 它实际用什么键认人——OIDC `sub`、SAML `NameID`、LDAP UUID/anchor、用户名还是邮箱;
2. 这个键在目录改名后是否稳定,证据等级是 `已验证` 还是 `推断`;
3. 不稳定时的后果与兜底动作。

范围判据是 `module.yml` 声明 `iam` capability 或消费 `SAMBA_DC_*`。按此复核,**成文时的起点表漏了
四个 Module**:`llng`(IAM Provider)、`lam`、`samba_fs`、`ai_agent`。实际范围是下表的 12 个。
`samba_dc` 不在范围内——它是目录本身,不是消费侧。

### M1 检查表

- [x] 以 `module.yml` 为准复核范围,补齐起点表遗漏的 `llng`、`lam`、`samba_fs`、`ai_agent`。
- [x] 12 个 Module 的中英文 README 补《目录属性变更说明》:七行表 + 匹配键 + 兜底路径,逐行标注
      `已验证` / `推断` 并给出证据入口。
- [x] 12 个 Module 的中英文技术文档补实现侧小节:身份存在哪张表/哪个字段、每次登录刷新什么、
      撤权经哪个接口、有无对账/事件订阅路径、没有自动路径时技术阻碍是什么。
- [x] 每个 OIDC/SAML Consumer 给出 `R-013` 投影结论(见 §4 的对应检查项)。
- [x] [Module IAM / OIDC 支持清单](../../docs/reference/module-iam-support.md)与英文镜像补"匹配键"列。

### 盘点结论

下表与四条发现记录 2026-09-20 的起点快照；Casdoor r10 和 Nextcloud r11 的当前变化见 M2/M3，
不能把历史 NameID 来源或用户名回调当作当前实现。

| Module | 实际匹配键 | 改名是否稳定 | 证据等级 |
| --- | --- | --- | --- |
| `meshcentral` | **anchor 直接作为用户 id**(`user//~oidc:<anchor>`);LDAP 侧 `ldapUserKey` 同取 anchor | 是 | `已验证`(`server-authentik-oidc-login-e2e.sh`、`server-llng-oidc-login-e2e.sh` 各断言一次) |
| `nextcloud` | anchor 同时作为 `directory_uuid` 和新账号内部 UID | 是；anchor UID 保持不变 | `已验证`(同上两个脚本断言 `directory_uuid == anchor`) |
| `casdoor` | 消费侧 anchor(`externalId`);签发侧 OIDC `sub` = 不可变 User ID,**SAML `NameID` = 用户名** | 消费侧是;OIDC `sub` 是;**SAML NameID 否** | `已验证`(`server-casdoor-directory-authority-e2e.sh` 改名复用、`server-casdoor-oidc-e2e.sh` 一 anchor 一 `sub`、`server-casdoor-saml-e2e.sh` NameID 随改名变化) |
| `authentik` | 消费侧 anchor(`object_uniqueness_field` → `UserSourceConnection.identifier`);签发侧内部用户 UUID | 是,但签发的不是 anchor | `已验证`(`server-authentik-oidc-login-e2e.sh` 断言 `identifier == anchor`) |
| `samba_fs` | 对象 SID,经 `idmap backend = rid` 定成 UID/GID;NT ACL 存 SID | 是 | `已验证`(`smb.conf.envsubst` 的 idmap 与 `acl_xattr` 配置) |
| `lam` | **无持久键**,每次登录按 `sAMAccountName` 检索后 bind DN | 不适用 | `已验证`(`server-lam-admins-e2e.sh` 覆盖组、停用、撤销) |
| `oauth2_proxy` | **无持久键**,透传 `sub`;控制台取 `sha256(issuer‖sub)` 作 principal id | 随 Provider | `已验证`(`internal/consoleauth/job_owner_test.go`) |
| `vikunja` | OIDC `(issuer, sub)`,**上游不可配置** | 随 Provider | 用户名取自 `preferred_username`:`已验证`(`server-vikunja-oidc-e2e.sh` 查库断言);`sub` 稳定性:`推断` |
| `forgejo` | OIDC `sub`,存入 `login_name` | 随 Provider | `推断`(无探针,见 `forgejo-interop` §1 的纪律) |
| `netbird` | OIDC `sub`(`AuthUserIDClaim` **可配置**) | 随 Provider | `推断`(无身份类 E2E) |
| `ai_agent` | **Forgejo 用户名**(`agent_grant`/`agent_grant_deny`/`policy_override`/`audit_record` 四张表) | 依赖 Forgejo 冻结用户名这一 `推断` 性质 | `推断` |
| `llng` | **`whatToTrace` = 小写 `sAMAccountName`**;`sub` 与 SAML `NameID` 都由它派生 | **否** | `推断`(依据本仓的 `lmConf.json`/`llng-config.sh` 与上游回退行为,**未跑探针**) |

### 四条值得单独记住的发现

1. **`llng` 发出的主体标识符是标签。** 起点表没有列 `llng`,而它的 `sub` 与 `NameID` 都等于
   小写化的 `sAMAccountName`。这意味着在 LLNG 部署下,目录改名会让 `forgejo`、`vikunja`、
   `netbird` 各建出第二个账号,回收用户名则直接让新人继承旧人全部资产(fail-open)。M2 的
   Provider 能力核实必须包含 LLNG,不能只看 authentik 与 Casdoor。
2. **`casdoor` 的 SAML `NameID` 是用户名**,且已由 E2E 证实随改名变化。SAML Consumer 的稳定关联
   必须使用显式锚点属性——`nextcloud` 的 SAML 模式正是这样配的。
3. **起点表关于 `netbird` 的记载有误**:它**没有**请求 `anasIdentityAnchor` claim,注册的是
   `name`/`cn`/`sAMAccountName`/`email`。好消息是它的 `AuthUserIDClaim` 可配置,M2 反而好办。
4. **`meshcentral` 已经在跑 M2 的目标形态**:anchor 直接作为应用内用户 id,且 anchor 只出现在
   账号 id 与管理员视图里。它是 `DIRKEY-R-010` 的现成实证,也是 M2 的参照实现。

盘点只读不改实现。发现不符合 `DIRKEY-R-002` 的 Module(`llng`、`ai_agent`),按 `DIRKEY-R-004`
已把缺口、技术阻碍与后果写进文档,整改归 M2。

## 4. M2 切换主体标识符

### Casdoor 进展（2026-10-04）

- [x] 校验 Dockerfile 固定归档，在实际上游函数上验证六项配置边界：JWT-Custom 可覆盖 `sub`，
      但 UserInfo 与 Logout Token 仍取内部 ID，SAML 只取用户名/邮箱，缺锚点不会拒绝自定义签发。
- [x] 在测试夹具内验证统一主体标识符候选补丁：四种格式的签名 access/refresh token、UserInfo、
      签名 Logout Token、SAML 2.0 模板一致；缺锚点拒绝，恢复管理员 OIDC 不受影响。
- [x] Netbird 固定 0.76.1 已确认 `sub` 进入普通用户 PAT API URL；Casdoor 在发布 binding 和渲染应用前拒绝组合。
- [x] 将统一主体补丁接入 Dockerfile（0005），刷新/sid/定向撤权补丁同时接入（0006）；复验完整固定归档通过。
- [x] 同一 r10 候选通过签名 SAML 2.0 SP 协议、OIDC 真实刷新与两 client 隔离；协议 fixture 不计作 Nextcloud SAML 会话通过。
- [x] Nextcloud r11 统一 LDAP 内部 UID 与 OIDC `sub` 为 anchor，补齐 LDAP 搜索与登录过滤器，移除身份查询补丁；真实改名后同 UID、同 anchor 与原 DAV 文件通过。
- [x] 官方原样 user_oidc 8.11.0、正式 Nextcloud Dockerfile 的 amd64 构建及 Web/cron 启动通过；HTML/OCS 显示名称与 anchor UID 分离，改名/文件/定向撤权回归通过。其他 Provider、客户端与 ARM64 发布验收不计入本项。
- [x] 在同一候选部署完成真实 token/refresh grant、签名 assertion、Nextcloud 改名账号归属和 Casdoor 标签回收隔离 E2E；不计作 Nextcloud SAML 应用会话通过。

入口为 `test-env/scripts/test-casdoor-identity-source.sh`，结论见
[固定源码可行性核实](../../docs/research/casdoor-directory-subject.md)。两份生产补丁已接入 r10 Dockerfile；源码测试仍**不记为 `R-008` 的实机通过**。独立实机结果见
[2026-10-04 验收记录](../reviews/2026-10-04-casdoor-directory-identity-acceptance.md)。
2026-10-06：Casdoor r10 正式双架构构建、OIDC/真实 Nextcloud、轮换、恢复和生命周期验收通过，
已标为 `release`，见 [发布验收](../../modules/casdoor/dev-docs/plans/archived/casdoor-iam.md)。
SAML 应用会话/SLO 仍待实现；其他 Provider、Consumer 和客户端的待办不因此变为完成。
产品尚未发版，无需迁移或兼容映射。

### Provider 范围调整（2026-10-05）

用户确认 Authentik 标记为过时。保留其历史核实记录，不要求继续实施 anchor 主体整改；现有实例不自动迁移或删除。当前发布整改聚焦 Casdoor，LLNG 已有验收结果继续保留。

### LLNG 进展（2026-10-05）

- [x] 在固定 `2.23.2-1` 源码核对 per-RP `oidcRPMetaDataOptionsUserIDAttr`；ID Token、UserInfo、refresh 与 Logout Token 使用同一主体来源，无需修改上游代码。
- [x] r12 的所有 OIDC RP 取 `anasIdentityAnchor`，准入规则拒绝缺失或空 anchor；登录、日志标签与显示名称保留原有目录属性。
- [x] 补齐仅监听 loopback 的配置 reload 入口；正式 Dockerfile amd64 构建和启动、跨容器访问拒绝通过。
- [x] 真实 Nextcloud r11 / 官方 user_oidc 8.11.0 登录、同 UID 与原文件改名复用、Portal 原 Cookie 撤销与另一用户隔离通过；签名主体与 refresh 链路通过。
- [x] 回收标签的新对象取得不同 anchor/UID/sub，原文件访问被拒；有效的缺 anchor SSO 反例及最终六项整体复验通过，退出码 0；原 Nextcloud Provider 已恢复。
- [x] Netbird 未批准的 anchor URL 投影在 calculate/render_env 拒绝；同步移除 LLNG 正向测试 fixture 中的 Netbird。
- [ ] SAML NameID 与真实应用会话验收列为待实现，不作为本轮主要协议。
- [ ] Samba AD 事件触发的既有会话撤销仍未实现；不能把 Portal 登出当作目录事件撤权。

证据见[LLNG r12 隔离实机验收](directory-identity-key.md)。
产品尚未发版，不实现旧账号迁移。M2/M3 仍是跨 Provider、跨 Consumer 的实施中里程碑。

### 跨 Provider 检查表

- [ ] **核实 Provider 能力**：当前范围为 Casdoor 与 LLNG；Authentik 已过时，以下其配置问题仅作历史记录。authentik 的 `sub_mode` 能否取到
      anchor 所在的属性(当前 anchor 落在 LDAP source 写入的 `attributes.ldap_uniq`,而 `sub_mode`
      是固定枚举);Casdoor 的 OIDC `sub` 与 **SAML `NameID`** 能否配成 `ExternalId`(M1 已证实
      NameID 当前是用户名,且该 Module 已有一个改 SAML 模板的受控补丁,可能说明这里也要补丁);
      LLNG 的 per-RP `oidcRPMetaDataOptionsUserIDAttr` 能否取到 `anasIdentityAnchor` 这个 exported
      var,以及 SAML NameID 来源能否独立于 `whatToTrace` 配置。必须在真实固定版本上验证,不得凭
      上游文档定稿。Casdoor 的源码能力边界已按上节核实，配置无法覆盖完整链路，受控候选补丁
      已通过源码与实机测试；LLNG OIDC 本轮也已核实，Authentik 不纳入当前整改，LLNG SAML 待实现。
- [ ] 当前支持的 Provider 可配时,按 `R-008` 把 `sub`/`NameID` 切成 anchor;任一家不可配时,按 `R-012` 在该
      Provider 的 Module 文档声明缺口,该部署下所有 Consumer 走 `R-004` 缺口路径。
- [ ] 切换前按 `R-013` 逐个 OIDC/SAML Consumer 验证主体标识符没有进入用户名、URL 标识符或
      文件路径，应用 R-010 的明确例外。**M1 已逐个给出结论并写进各 Module 技术文档,汇总如下**；
      `netbird` 仍需整改，Nextcloud 新 UID 配置的显示名与客户端体验仍需真实验证：

      | Consumer | 主体标识符是否被投影 | 结论证据 |
      | --- | --- | --- |
      | `oauth2_proxy`(→ ANAS 控制台) | 否。principal id 取 `sha256(issuer‖sub)`,原值只进内部绑定字段与审计 | `已验证`,`internal/consoleauth/job_owner_test.go` |
      | `nextcloud` | 是；用户已确认新部署内部 UID 和技术路径使用 anchor 的 R-010 例外。OIDC mapping 取 `sub`，LDAP 内部 UID 来源取 anchor | Casdoor / LLNG 新配置 `已验证`；Authentik 已过时；客户端待回归 |
      | `vikunja` | 否。用户名取 `preferred_username`,`sub` 只进内部 `subject` 列 | `已验证`,`server-vikunja-oidc-e2e.sh` 查库断言 `users.username` 等于目录用户名 |
      | `meshcentral` | 不适用。读显式 anchor claim,根本不读 `sub`;且已经是目标形态 | `已验证`,两个 Provider 的 OIDC E2E |
      | `forgejo` | 否。`sub` 进 `login_name`(内部字段),用户名取 `preferred_username` | `推断`,复核入口待加进 `forgejo-agent-api-probe.sh` |
      | `netbird` | **是,需先改或先确认**。`AuthUserIDClaim = "sub"` 使 `sub` 成为用户 id,出现在 `/api/users/{userId}` 与 Dashboard 用户管理视图 | `推断`,**M2 的唯一 `R-013` 待办** |

      验证**不得只在 LLNG 部署上跑**:M1 中 LLNG 的 `sub` 曾是用户名,任何投影会被掩盖成"看起来
      正常",必须至少覆盖 Casdoor。
- [ ] provider-neutral 契约声明主体标识符取自 anchor(`R-012`),Consumer 不读 Provider 私有配置。

**不做**:`sub ↔ anchor` 映射服务。它需要一个中立契约扩展、一条撤权时在线的查询路径和每个
Provider 各自的适配器,用更多零件换同一个结果;只有在 M2 第一项证明主体标识符确实不可配置时
才重新评估。

## 5. M3 边界

M3 只验收一件事——**目录里改个名,应用里还是同一个人**,覆盖至少一个 OIDC Consumer 与一个
LDAP Consumer。

2026-10-05：全新部署统一 anchor UID 的 Casdoor + Nextcloud 隔离实机验收通过，
不再需要身份查询补丁；官方 `user_oidc 8.11.0` 替代退出补丁的实机验收独立记录。
用户确认允许 Nextcloud anchor UID 出现在技术路径，登录名与显示名继续使用人类可读属性。
LLNG r12 的 OIDC/Nextcloud 组合也已通过改名复用与标签回收隔离；
Authentik 已过时，不纳入当前发布整改；客户端体验、独立 LDAP 登录和全部 Module 的改名验收未完成，不把局部通过
汇总为 `DIRKEY-R-003` 全局完成。

## 6. E2E 验收用例

本节是待实现用例的规范来源。脚本写出来之后,每条搬进
`test-env/cases/directory-identity-key/cases.yml` 并登记 `requirement_scope`——正式用例目录不接受
"待实现"状态(`status` 只有 `active`/`retired`,且 active 用例的实现文件必须已存在),所以在那之前
用例留在这里。

每条用例的断言都必须落在**最终结果**上:同一个应用账号 id、资产归属未变、请求被拒。只断言
"同步 API 已调用"、"claim 里有 anchor"或 HTTP 302 都不算。

### DIRKEY-T-001 改名后复用身份(`R-003`)

| 项 | 内容 |
| --- | --- |
| 级别 | e2e |
| 脚本 | 待新增 `test-env/scripts/server-directory-rename-identity-e2e.sh` |
| 环境 | Samba AD + 一个 IAM Provider + 一个 OIDC Consumer 与一个 LDAP Consumer |
| 前置 | 目标用户已在两个 Consumer 中各登录一次并留下可识别资产(仓库/任务/文件) |
| 步骤 | 记录两侧的应用账号 id 与资产归属 → 在 AD 改 `sAMAccountName` → 等待或触发同步 → 再次登录两侧 |
| 断言 | 两侧的应用账号 id **不变**;资产仍属于该账号;没有新建第二个账号;应用内用户名的变化(跟随/冻结)与该 Module 文档《目录属性变更说明》里写的一致 |
| 反例 | 改名后若出现第二个账号,或原账号资产变为孤儿,必须失败 |
| 清理 | 改回原 `sAMAccountName`,删除测试用户与资产 |

**"与文档一致"是这条用例的重点**:用户名是跟着改还是冻结,两种都可能合规,但文档写的那个必须是
真的。这条同时验收 `DIRKEY-R-011` 的证据等级从 `推断` 升为 `已验证`。

### DIRKEY-T-002 LDAP source 的唯一性字段是 anchor(`R-007`)

| 项 | 内容 |
| --- | --- |
| 级别 | e2e |
| 脚本 | 待新增 `test-env/scripts/server-directory-identity-key-e2e.sh` |
| 环境 | Samba AD + LLNG、Samba AD + Casdoor 各一轮 |
| 步骤 | 读取 Provider 侧实际生效的 LDAP source 配置 → 比对 `SAMBA_DC_IDENTITY_ANCHOR_ATTRIBUTE` |
| 断言 | 唯一性字段等于 anchor 属性名;**不是** `sAMAccountName`、DN 或 `mail` |
| 反例 | 把唯一性字段改成 `sAMAccountName` 后重新调和,断言调和拒绝或告警,不得静默接受 |

### DIRKEY-T-003 主体标识符的值就是 anchor(`R-008`)

| 项 | 内容 |
| --- | --- |
| 级别 | e2e |
| 脚本 | 同 `DIRKEY-T-002` |
| 步骤 | 完成一次真实 OIDC 登录,取出 ID Token 的 `sub`(SAML 取 `NameID`)→ 从目录读同一对象的 `anasIdentityAnchor` |
| 断言 | 两个值**逐字节相等**;不是 Provider 内部 id、用户名或邮箱;改名后再取一次仍相等 |
| 反例 | Provider 配置为内部 id 时断言失败——这条用例存在的意义就是挡住"看起来也很稳定"的替代实现 |

这条用例是 M2 的验收核心。它无法在 Provider 侧能力核实(§4 第一项)完成前通过。

### DIRKEY-T-004 主体标识符不进入展示层(`R-013`)

| 项 | 内容 |
| --- | --- |
| 级别 | e2e |
| 脚本 | 同 `DIRKEY-T-002`,逐 Consumer 循环 |
| 步骤 | 每个 OIDC/SAML Consumer 各登录一次 → 抓取应用内用户名、个人主页 URL、API 路径、以及该应用为用户创建的文件/目录路径 |
| 断言 | 除 R-010 明确允许的 Nextcloud 内部 UID、技术路径和内部存储目录外，anchor 不得出现在登录名、显示名或其他未获例外允许的位置；Nextcloud 另验证 UID 等于 anchor、改名后文件保留 |
| 反例 | 已知高风险形态:把主体标识符直接当成应用内用户 id 的应用。命中任何一个,该 Consumer 必须先整改,不得以"多数 Consumer 没问题"放行 |

### DIRKEY-T-005 标识符回收再分配必须 fail closed(`R-001`,同时覆盖 `R-002`)

| 项 | 内容 |
| --- | --- |
| 级别 | e2e |
| 脚本 | 待新增 `test-env/scripts/server-directory-identifier-reuse-e2e.sh` |
| 环境 | 同 `DIRKEY-T-001` |
| 前置 | 用户 A 已在各 Consumer 建号并留下资产 |
| 步骤 | 删除 A(或停用并移出准入组)→ 新建用户 B,把 A 原来的 `sAMAccountName` 与 `mail` 赋给 B(anchor 必然不同)→ B 登录每个 Consumer |
| 断言 | B **不得**进入 A 的应用账号,不得看到 A 的任何资产;要么建出全新账号,要么因唯一性冲突拒绝建号 |
| 反例 | 这条用例本身就是故障注入:任何一个 Consumer 让 B 接管 A 的账号即为严重失败 |
| 清理 | 删除 B 与两侧账号、资产 |

这是整份要求里唯一会造成**权限被他人继承**的失败模式,因此必须独立成例,不能并进 `DIRKEY-T-001`。

### DIRKEY-T-006 anchor 不可得时 fail closed(`R-009`,单元级)

| 项 | 内容 |
| --- | --- |
| 级别 | 单元 |
| 入口 | Casdoor 候选：`test-env/scripts/test-casdoor-identity-source.sh`；其他 Provider 待新增 |
| 步骤 | 构造一个明确请求 anchor claim 的 Consumer,让 Provider 侧取不到 anchor(目录对象缺该属性) |
| 断言 | 调和或登录**拒绝**,并给出可诊断错误;不得静默降级成 `sAMAccountName` 或邮箱 |

### 执行记录

| 需求 ID | 脚本 | 用例 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-003 | 待新增 `test-env/scripts/server-directory-rename-identity-e2e.sh` | DIRKEY-T-001 | — | 待执行 |
| R-007 | 待新增 `test-env/scripts/server-directory-identity-key-e2e.sh` | DIRKEY-T-002 | — | 待执行 |
| R-008 | 同上 | DIRKEY-T-003 | — | 待执行 |
| R-013 | 同上 | DIRKEY-T-004 | — | 待执行 |
| R-001 | 待新增 `test-env/scripts/server-directory-identifier-reuse-e2e.sh` | DIRKEY-T-005；同时覆盖 R-002 | — | 待执行 |

## 7. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [Module 文档生成标准](../../docs/developer/module-documentation.md)与英文镜像 | 《目录属性变更说明》必需章节与技术文档实现侧要求 | 已完成（2026-09-20） |
| [Samba AD 用户与权限规划](../../docs/architecture/samba-ad-user-planning.md) §5 | 把"哪些系统用 anchor"的枚举改写为指向本要求的规范陈述 | 未开始 |
| 各 Module 的 README 与技术文档（中英文） | 按新章节补全匹配键与目录变更行为 | 已完成（2026-09-20）；12 个在范围内的 Module 各四份文档 |
| [Module IAM / OIDC 支持清单](../../docs/reference/module-iam-support.md)与英文镜像 | 盘点结论出来后补一列"匹配键" | 已完成（2026-09-20）；Provider 分消费侧/签发侧两项 |

## 8. 当前阻塞

- Authentik 已标记为过时，不再作为当前发布整改的阻塞项；Casdoor r10 与 LLNG r12 的 OIDC anchor 已完成本轮真实协议及 Nextcloud 账号归属验证。LLNG SAML NameID 待实现。
- `netbird` 固定源码已验证主体标识符进入普通用户 PAT API URL，违反 `R-013`；Casdoor 与 LLNG 均已拒绝该组合。恢复支持需要先改变 Netbird 的用户 URL 设计。
- `forgejo`、`vikunja` 等其他 Consumer 的主体稳定性及账号归属仍需实机验证；LLNG 已有独立协议与 Nextcloud 探针，不再属于未核实链路。
- M3 的 Casdoor / LLNG + Nextcloud 改名及标签回收已通过；其他 Provider、Consumer、桌面/移动客户端仍待完成。
- LLNG 的 Samba AD 事件监听与实时会话撤销待实现；现有会话可能保留旧目录属性。


## 9. 已执行的局部验收与未完成范围

| 日期与候选 | 对应需求 | 验收结果与限制 |
| --- | --- | --- |
| 2026-10-05，LLNG 2.23.2-r12 正式 Linux/amd64 镜像 | DIRKEY-R-001/002/003/007/008/009/012/013 的 LLNG OIDC 与 Nextcloud 子集 | 六项真实断言通过：首次登录、签名主体/UserInfo/refresh、Portal 注销、改名原账号和文件、标签回收新身份隔离、缺 anchor 拒绝；Provider 配置恢复、测试账号清理 |
| 2026-10-05，Nextcloud 34.0.2-r11 + 官方原样 user_oidc 8.11.0 | DIRKEY-R-003/010/013 的 Nextcloud 子集 | 正式 amd64 构建/启动、插件完整性、anchor 内部 UID、可读姓名、改名同 UID/原文件、组撤权后 Cookie 失效和无关用户保留通过 |

以上仅是明确子集验收成功，M2/M3 的全 Provider × Consumer × 协议矩阵仍未完成。LLNG 保持 developing，SAML NameID、目录事件实时撤权、全新 workspace apply、ARM64、历史升级与全 Consumer 未验收。Nextcloud 保持 developing，桌面/移动客户端、ARM64 和完整升级/编辑矩阵未验收；不能把身份子集通过当作整个 Module 发布通过。

LLNG 镜像摘要：`2df0357521b47b31a9df85bfb218c17fe71225e1deee05fea73cd93643b9d950`。Nextcloud 镜像摘要：`89bb0c1f8f896444cedd386998971ed4ba952504e3ea3b568263583b9d3e4593`；官方 user_oidc 归档 SHA-256：`887920504c2f0e0d055abd66225ee7b66d756f50fe5a3739779d6b2ce108633f`。

执行入口为 `test-env/scripts/server-llng-nextcloud-identity-e2e.py` 与 `server-casdoor-nextcloud-identity-e2e.py`。远端材料保留于 finance `/home/whl/anas-casdoor-test-20261003/`（LLNG 位于 `llng-anchor/`）。本地原报告与脱敏 evidence、源码/夹具摘要已迁入 Git 忽略的 `test-env/reports/2026-10-07-review-consolidation/`；这是本机副本，未发布共享归档。完整材料可按本地 manifest 逐文件校验。

下一步：完成现有矩阵的其他 Consumer 和协议；新增验收条件先确认再更新需求，既有未通过项继续修复。
