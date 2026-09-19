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

## 6. E2E 执行记录

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-003 | 待新增 `test-env/scripts/server-directory-rename-identity-e2e.sh` | 改名前后各登录一次,断言同一应用账号、资产未孤立 | — | 待执行 |
| R-007 | 待新增 `test-env/scripts/server-directory-identity-key-e2e.sh` | authentik/Casdoor 的 LDAP source 唯一性字段 | — | 待执行 |
| R-008 | 同上 | `sub`/`NameID` 的值等于目录 anchor | — | 待执行 |
| R-013 | 同上 | 逐 Consumer 断言主体标识符未出现在用户名、URL 或文件路径 | — | 待执行 |

断言必须落在最终结果上:同一应用账号 id、资产归属未变。只断言"同步 API 已调用"或"claim 里有
anchor"不算。

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
