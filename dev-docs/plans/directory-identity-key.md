---
doc_type: plan
status: implementing
created: 2026-09-20
updated: 2026-09-20
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
| M1：逐 Module 盘点与文档补全 | R-006 | 实施中；已派生独立任务 |
| M2：主体标识符切换为 anchor | R-007—R-009、R-012、R-013 | 未开始；阻塞于 Provider 能力核实 |
| M3：改名复用身份的真实 E2E | R-003 | 未开始 |

## 2. M0 检查表

- [x] 新增[目录身份键要求](../requirements/directory-identity-key.md),把"anchor 是唯一身份键、
      标签只用于登录/搜索/显示"从 `samba-ad-user-planning` §5 的枚举升级为可验收的规范条目。
- [x] [Module 文档生成标准](../../docs/developer/module-documentation.md) §3 增加《目录属性变更说明》
      必需章节(中英文),要求逐行回答七种目录侧变更,并单独写明匹配键与兜底路径、标注证据等级。
- [x] 同标准 §4 要求技术文档写清实现侧:身份存在哪张表/哪个字段、每次登录刷新什么、撤权经哪个
      接口、没有自动路径时技术阻碍是什么。
- [ ] [Samba AD 用户与权限规划](../../docs/architecture/samba-ad-user-planning.md) §5 的枚举改写为
      指向本要求的规范陈述。

## 3. M1 盘点范围

逐个 Module 回答三件事,结论写进它自己的 README 与技术文档:

1. 它实际用什么键认人——OIDC `sub`、SAML `NameID`、LDAP UUID/anchor、用户名还是邮箱;
2. 这个键在目录改名后是否稳定,证据等级是 `已验证` 还是 `推断`;
3. 不稳定时的后果与兜底动作。

范围是所有用户来自目录的 Module。已知起点:

| Module | 已知情况 | 待确认 |
| --- | --- | --- |
| `authentik` | LDAP source `object_uniqueness_field` 取 anchor;OIDC `sub_mode: user_uuid`,注释明确"用户名是登录名,绝不能成为 subject" | 符合 R-007;R-008 待切换,见 M2 |
| `casdoor` | 目录成员解析按 anchor 建索引(`groupsByAnchor`) | 用户身份与 `sub` 是否同样锚定,尚未核对 |
| `forgejo` | 按 OIDC `sub` 认人,`sub` 存入 `login_name`;用户名取 `preferred_username` 且只在建号时写一次 | `sub` 存进 `login_name` 与管理端不支持改名两条是推断,待探针 |
| `vikunja` | 按 `(issuer, sub)` JIT 建号 | 同上,待探针 |
| `nextcloud`、`meshcentral` | 规划文档记为使用 anchor 作为 LDAP UUID / SAML UID / 文本用户键 | 实现侧复核与证据入口 |
| `netbird`、`oauth2_proxy` | claim 映射含 `sAMAccountName`;NetBird 已请求 `anasIdentityAnchor` claim | `sAMAccountName` 是否进入任何持久键 |

盘点只读不改实现。发现不符合 `DIRKEY-R-002` 的 Module,按 `DIRKEY-R-004` 先把缺口写进文档,
整改归 M2。

## 4. M2 切换主体标识符

- [ ] **核实 Provider 能力(阻塞项)**:authentik 的 `sub_mode` 能否取到 anchor 所在的属性
      (当前 anchor 落在 LDAP source 写入的 `attributes.ldap_uniq`,而 `sub_mode` 是固定枚举);
      Casdoor 的主体标识符是否可配置。必须在真实固定版本上验证,不得凭上游文档定稿。
- [ ] 两家都可配时,按 `R-008` 把 `sub`/`NameID` 切成 anchor;任一家不可配时,按 `R-012` 在该
      Provider 的 Module 文档声明缺口,该部署下所有 Consumer 走 `R-004` 缺口路径。
- [ ] 切换前按 `R-013` 逐个 OIDC/SAML Consumer 验证主体标识符没有进入用户名、URL 标识符或
      文件路径;`forgejo` 存入 `login_name`(内部字段)、`vikunja` 存入 `subject` 列,均需实证。
- [ ] provider-neutral 契约声明主体标识符取自 anchor(`R-012`),Consumer 不读 Provider 私有配置。

**不做**:`sub ↔ anchor` 映射服务。它需要一个中立契约扩展、一条撤权时在线的查询路径和每个
Provider 各自的适配器,用更多零件换同一个结果;只有在 M2 第一项证明主体标识符确实不可配置时
才重新评估。

## 5. M3 边界

M3 只验收一件事——**目录里改个名,应用里还是同一个人**,覆盖至少一个 OIDC Consumer 与一个
LDAP Consumer。

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
| 环境 | Samba AD + authentik、Samba AD + Casdoor 各一轮 |
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
| 断言 | anchor 值**不出现**在上述任何位置 |
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
| 入口 | 待新增,与 Provider 的 Hook 单元测试共用 fixture |
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
| 各 Module 的 README 与技术文档（中英文） | 按新章节补全匹配键与目录变更行为 | 实施中（M1） |
| [Module IAM / OIDC 支持清单](../../docs/reference/module-iam-support.md)与英文镜像 | 盘点结论出来后补一列"匹配键" | 未开始（M1） |

## 8. 当前阻塞

- M2 阻塞于 authentik 与 Casdoor 的主体标识符是否可配置,需要真实部署跑探针;
- M1 的多数结论同样依赖对固定版本跑探针,`forgejo` 的探针脚本已存在,其余 Module 没有同类入口;
- M3 需要可用的真实部署与目录,改名 E2E 还要求能安全地改回来。
