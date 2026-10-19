---
doc_type: plan
status: done
created: 2026-10-11
updated: 2026-10-20
---

# 独立用户门户实施计划

**状态：2026-10-20 finance 当前制品的账号管理107项、十场景联动退出148项、实际菜单退出30项及匿名回调竞态7项全部通过；清理、证据封存和最终门禁结果见§21.11—21.13。历史失败与旧通过基线分别保留。§18 的 R-074 已按新要求退役；邮件后端 PoC、完整 Module/Provider 矩阵不属于本轮。**
规范来源为 [门户要求](../../requirements/user-portal.md) PORTAL-R-001—R-082（十项旧要求已退役），
实现机制见 [独立用户门户设计](../../../../../docs/architecture/user-portal-design.md)。
原应用目录 APPCAT-R-001—R-039 仍归 [应用目录计划](../../../../../dev-docs/plans/app-catalog.md)；
共享交付时两份矩阵分别记录结果，不重复归属。

## 1. 确认范围与基线

2026-10-11 用户“确定 继续”确认上一轮推荐首版：独立应用入口、本人资料/原密码改密、
首次/过期受限改密、范围内用户查找、重置/强改/解锁、组成员管理、审计和近期认证。
账号创建/启停、无原密码恢复、MFA、跨应用会话管理与便利功能继续为后续候选。
实际生产委派人员/组由部署配置给出；未配置就拒绝管理，不影响隔离普通人员夹具的开发。
后续用户确认优先原生 OIDC/API/配置，尽量不增加 Casdoor 源码补丁；
退出门户同时退出当前浏览器 IAM 会话，新增 R-041。其他设备会话撤销仍属后续范围。

设计保留单个服务实例；推荐内存会话与私有持久审计文件，不添加数据库/Redis、
用户/组副本或门户同步守护进程。整体高复杂度来自必要的 LDAP/认证和跨系统失败边界；
用户已确认这个方向，不再重复请求同范围确认。新持久服务或扩大目录管理另行评审。

首次设计检查只写需求/设计/计划，历史结果保留在 §5。
后续已在用户授权的 SSH 主机运行隔离 Casdoor/Samba 能力实验，结果见 §6—7；
2026-10-14 已新增 user_portal Module、页面/API、原生客户端配置和私有审计；原应用目录及原生协议实验仍不自动计入 PORTAL。

## 2. 里程碑与需求归属

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：登录、授权目录与本人资料 | R-001—R-005、R-007、R-008、R-010、R-041 | 已完成；最终复验见 §11 |
| M2：本人改密与写入基础 | R-016、R-018—R-020、R-025—R-027、R-032—R-039 | 已完成；最终复验见 §11 |
| M3：委派用户与分组管理 | R-006、R-011、R-012、R-021—R-024、R-028—R-031 | 已完成；最终复验见 §11 |
| M4：门户界面与结果反馈 | R-042、R-044、R-047、R-060 | 已完成；3937镜像实机与下拉专项通过，见 §17；后续并行改动另验 |
| M5：资料和平台管理员管理 | R-045、R-046、R-048—R-054、R-058 | 已完成；R7最终复验见 §15.4 |
| M6：本人权限的域级操作 | R-055—R-057、R-059 | 已完成；最终复验见 §13 |
| M7：限定人员 OU 的系统邮箱与建号 | R-062—R-068 | 已完成；R7最终复验见 §15.4 |
| M8：页内确认与高级分组 | R-069、R-071、R-072 | 已完成；最终镜像89项实机、清理与保全通过，见 §16 |
| M9：移除操作理由 | R-073 | 已完成；3937镜像页面/API/审计通过，见 §17 |
| M11：退出主页与分级验证 | R-075—R-076 | 已完成；当前制品完整账号107项通过，见 §21.11 |
| M12：两大组分类显示 | R-077 | 已完成；双语 UI 与 finance 真实页面通过，见 §20 |
| M13：当前浏览器旧应用会话清理 | R-078—R-082 | 已完成；完整148项、真实菜单30项、匿名竞态7项及清理/保全通过，见 §21.10—21.13 |

### M1：能力实验、接线和只读入口

阅读要求 §1—2、设计 §2—3。按现有 Module 注册 OIDC 和只读目录共享；
复用 internal/appcatalog 的语言/BuildView，认证后的允许集合与排版分开。
补名称/简介搜索，联动分类/空组，但不重复实现协议规则。

先对固定 Casdoor 3.143.0、当前 r12 源码验证 PKCE、sub/anchor、nonce/state、
签名验证、近期认证所需 auth_time/max_age 和最小受限授权读取。
优先实验原生用户身份 API 凭据、API 权限配置及 enforce/batch-enforce，
读取当前人员有效性及受管条目判定，不持有通用应用管理员凭据；
不能因为普通应用 secret 过宽就推断所有原生凭据都不可用。
原生默认允许/管理员绕过/公开目录资产旁路保持原 APPCAT 判据。
登录/权限失效反例用当前 AD 事实，不能仅按旧 JWT 分组。
近期认证先验证关闭自动登录等应用配置与可信 auth_time 校验；
联合登出验证 discovery 的 end_session_endpoint、不向浏览器交付门户 token、原生 CORS、
当前 IAM 会话实际失效及 IAM 已退出/不可达时的结果。
仅在原生路径不足时重新评估最小改动，不预先实现新的受限 API 或再认证补丁。
LLNG 的现有适用目录验收仍在 app-catalog，不把 Casdoor 通过复制给 LLNG。

服务会话先用内存，重启要求重新登录，Cookie 中不存 token。
接口名称、具体超时/限制值和 Module 参数在实施时按现有 conventions 定稿；
当前可执行配置及限制以 modules/user_portal/module.yml、README 和技术文档为准。

### M2：LDAPS 本人改密与持久审计

阅读要求 §2—3、设计 §4、§6。在固定 Samba 4.23.6 上验证 LDAPS CA/服务器名、
普通人员的原密码 change、错误原密码、实际密码历史/最小年龄/复杂度拒绝。
先查清密码过期/首次强改时 bind 与 unicodePwd Modify 的行为，确认原密码证明路径；
不预先用通用 reset 代替 change，不宣称 RFC 3062 对所有目录都有效。

审计采用一个私有追加文件写入者及 fsync，操作意图先持久化后发 LDAP。
提供本人/授权范围的分页查询；日志损坏、满盘、写结果失败、重启残缺记录都关闭写入口或
显示真实未知状态，不自动修复目录。跟随现有 Module backup/restore，恢复不保留旧会话。
审计文件容量/保留阈值在固定夹具容量验证后定稿，不先引入第二种存储。
同时验证旧/新密码泄露检查、可信代理 IP 限流和受限入口的统一错误响应。

### M3：委派重置、解锁与成员写入

阅读要求 §2—3、设计 §2、§4—6。在隔离 OU 中建立普通人员、跨范围人员、
相同名称复用人员、受保护/嵌套组与门户委派角色等夹具。
密码管理和成员管理使用门户专属受限绑定，查询使用现有只读绑定；
当前 svc_password 不具有组写权，也不扩权来绕过实验。

分别测试 Reset Password、pwdLastSet、lockoutTime 和允许组 member 的最小 ACL；
目录 ACL 不天然限制 member 值，动态目标范围和间接提权由门户强制核验。
实验核对 assertion control 支持、critical 不支持失败、anchor/DN 复用、
并发添加他人成员及门户外保护关系变化；不能用进程锁冒充 AD 多对象事务。

近期认证不能把 token iat/签发成功当成新认证。强改仅在 M2 受限入口闭环通过后启用；
reset/强改部分失败、未知结果及一次密码响应分别验证。
单组/单人员 member add/delete；不加入批量任务、组嵌套编辑或自动回滚框架。

## 3. 实现前的能力验证清单（历史状态）

以下保留实现前的实验任务及当时状态；最终结果见 §9，能力实验与产品验收分开记录：

| 实验 | 已知事实与需验证部分 | 当时状态 |
| --- | --- | --- |
| Casdoor 受限授权读取 | 本人 token 的 UserInfo/get-account 提供当前身份、组、权限及状态；固定组模板拒绝反例通过；enforce 忽略停用/待审批状态 | 原生路径通过，无需新接口；门户目录/资产保护与完整动态授权仍待实现 |
| 近期认证 | max_age/prompt 仍复用旧会话；独立客户端关闭自动登录后显示密码表单，新密码认证更新 auth_time，旧时间/不同身份被检查拒绝 | 原生配置路径通过，无新增补丁；门户回调/敏感操作反例未执行 |
| 门户与 IAM 联合登出 | discovery /api/logout 无 hint 的原生 POST 清除当前 Cookie，原生 CORS/已退出响应通过；内置组织旧 token 保留，不能冒充全部撤销 | 原生 Cookie 退出路径通过；R-004/R-041 的门户本地会话、实际 ANAS 接线与错误路径未实现 |
| 普通/首次/过期密码 change | 原密码 delete/add、错误原密码、实际策略拒绝及只读绑定证明首次/真实到期原密码已验证 | 原生能力通过；受限页面、限流、会话与审计尚未实现 |
| 受限写绑定 ACL | 专属 reset + pwdLastSet/lockoutTime ACL 和单个允许组 member ACL 已验证；跨 OU/属性/组拒绝 | 原生对象/属性边界通过；member 可指向跨范围人员，动态授权仍须门户核验 |
| 原子条件/稳定成员寻址 | 正确 critical assertion 返回 12、不支持；GUID/SID 单值增删、并发不同成员和 DN 复用拒绝已验证 | 采用稳定引用与单值操作；跨对象保护关系竞态未验证 |
| 审计持久性/容量/恢复 | 单进程私有文件方案；尚无实现、满盘/残缺/备份恢复结果 | 未执行 |

2026-10-11 查阅 [AD unicodePwd](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-adts/6e803168-f140-4d23-b2d3-c3a8ab5917d2)、
[OIDC Core](https://openid.net/specs/openid-connect-core-1_0.html) 和
[LDAP Assertion Control](https://datatracker.ietf.org/doc/html/rfc4528)。
RFC Editor 的 RFC 4528 地址首次请求返回 429，改从 IETF Datatracker 阅读。
上述是规范事实，不证明固定产品已支持。
首次设计阶段本机只阅读源码；§6 才执行隔离账号写入实验。
目录结构未知或实验否定某路径时先继续调查替代方案；涉及新增服务或不同产品范围再请求确认。

## 4. 实施检查表

### 4.1 CI 与文档同步

首次文档检查记录在 §5，能力验证记录在 §6；尚无本主题产品 CI 全绿提交。
新增 Module/API 后运行相关单元/契约与既有 Module revision、生成文档/镜像/配置门禁，
采用完整测试用例 catalog 并生成双语引用。
用例与运行脚本从实际接线产生，当前不建立空脚本或无实现的假覆盖。

- [x] 新增独立矩阵与配套计划，保留 APPCAT 的原有判据/归属。
- [x] 更新中文架构、英文索引摘要、导航与关联文档。
- [x] 模块、接口、配置和运维/备份源文档已补齐；行为随真实验收更新。
- [x] 固定源码/镜像/制品基线下逐项验收；最终 95 项运行检查和相关单元/契约通过，见 §9。
- [x] 原生能力实验共享证据归档；源码/制品摘要及位置见 §6，不代替产品验收证据。

### 4.2 e2e 执行记录

入口见 test-env/cases/user-portal/cases.yml；具体断言和最终源码/镜像基线见 §11。
运行基线分开记录，不能把中途版本的通过自动复制给后来修改的制品。

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-001 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-002 | T-001 unit/contract, T-002 browser, T-004 boundary | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-003 | T-001 unit/contract, T-002 browser, T-004 boundary | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-006 | T-001 unit/contract, T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-007 | T-001 unit/contract, T-002 browser, T-003 faults | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-008 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-010 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-011 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-014 | T-001 unit/contract, T-002 browser, T-004 boundary | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-015 | T-001 unit/contract, T-002 browser, T-004 boundary | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-016 | T-001 unit/contract, T-005 native LDAP | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-017 | T-002 browser, T-005 native LDAP | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-018 | T-001 unit/contract, T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-019 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-020 | T-002 browser, T-003 faults | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-021 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-022 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-023 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-024 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-026 | T-003 faults | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-027 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-028 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-029 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-030 | T-002 browser, T-004 boundary | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-031 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-033 | T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-035 | T-001 unit/contract, T-003 audit contract, T-003 faults | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-036 | T-003 audit contract, T-003 faults | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-037 | T-001 unit/contract, T-003 faults | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-038 | T-001 unit/contract, T-003 faults | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-041 | T-001 unit/contract, T-002 browser | ln 正式镜像（权限修复），§11 | 2026-10-15 | 通过 |
| R-077 | T-006 双语 UI、T-010 categories browser | finance 79f87dbc；§20.1 | 2026-10-19 | 通过，真实页面 4/4；联动退出场景另记 |
| R-078 | T-010 browser | finance 新制品，§21 | 2026-10-19 | 待执行 |
| R-079 | T-010 browser | finance 新制品，§21 | 2026-10-19 | 待执行 |
| R-081 | T-010 browser | finance 新制品，§21 | 2026-10-19 | 待执行 |



### 4.3 单元与契约验收记录

| 需求 ID | 验证入口 | 证据基线 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-004 | T-001 unit/contract, T-002 browser | 本机单元与 ln 契约，§11 | 2026-10-15 | 通过 |
| R-005 | T-001 unit/contract, T-002 browser | 本机单元与 ln 契约，§11 | 2026-10-15 | 通过 |
| R-009 | T-002 browser | 本机单元与 ln 契约，§11 | 2026-10-15 | 通过 |
| R-012 | T-001 unit/contract | 本机 race 单元；最终源码，§11 | 2026-10-15 | 通过 |
| R-013 | T-001 unit/contract, T-002 browser | 本机单元与 ln 契约，§11 | 2026-10-15 | 通过 |
| R-025 | T-001 unit/contract, T-002 browser, T-003 audit contract | 本机单元与 ln 契约，§11 | 2026-10-15 | 通过 |
| R-032 | T-002 browser, T-003 audit contract | 本机单元与 ln 契约，§11 | 2026-10-15 | 通过 |
| R-034 | T-002 browser, T-003 audit contract | 本机单元与 ln 契约，§11 | 2026-10-15 | 通过 |
| R-039 | T-002 browser, T-003 faults | 本机单元与 ln 契约，§11 | 2026-10-15 | 通过 |
| R-040 | T-001 unit/contract, T-002 browser | 本机单元与 ln 契约，§11 | 2026-10-15 | 通过 |


## 5. 首次设计检查（2026-10-11；保留历史基线）

### 5.1 逐项结论

PORTAL-R-001—R-040 已抽成可验证首版判据并全部安排里程碑。
全部产品判据当前为 **未执行**，没有单元/契约/e2e 通过基线；
上表按 ID 列出 e2e 待执行，其余契约/单元随实现验证。
本轮文档门禁只验证归属、索引和站点构建，不构成产品验收。

### 5.2 文档与证据基线

本轮文档复核基线为 HEAD `0dbdb3103c32bd23e75e83fba64e813798ef1f86` 加当前工作树，
无关变更保留。十个需求/架构/索引/关联计划文件的逐文件清单保存在本机
`/tmp/anas-user-portal-design-baseline-20261011.json`，
SHA-256 `427062b4bad1059b6ce5aaaa4e5d0893a0d442b2630bdef33c501afe605c35e9`。
本计划不纳入该清单，避免记录清单摘要造成自引用。
门户架构 SHA-256 为 `2a7b5c4986ec426dce3cdaf894852de494ee29c572667d7221d11bceeb33af70`，
门户矩阵为 `2803d4d0d62ea2b290e0aa699a4c19335a91e06cdef93c6900a68101d9647c31`。
没有门户二进制、镜像摘要、部署制品或运行账户验收基线。

| 检查 | 本轮结果 |
| --- | --- |
| docs:requirement-status / docs:plan-status | 已生成；新主题 0/40，计划 proposed，不将文档完成计为产品完成 |
| docs:check-requirements | 通过：26 份活动矩阵、764 项归属；本主题全部 40 项与 30 条 e2e 待执行记录一致 |
| 既有测试用例 catalog | 5 个主题、54 个 active 用例检查通过；没有把未实现门户加入假用例或继承已有运行结果 |
| docs:check-requirement-status / docs:check-plan-status | 通过，生成状态与索引一致 |
| docs:check-status | 108 份文档通过，待实现/已确认范围与运行状态明确区分 |
| docs:build | 最终通过；保留既有 500 kB bundle 提示，没有构建门户产品 |
| diff / 新文档格式 | tracked diff 与三个新文档格式通过；初次 --no-index 包装因正常差异退出 1 提前结束，后改为 tracked 检查及新文档逐项检查全部通过 |

主要日志前缀 `/tmp/anas-user-portal-`，后缀 `-20261011.log`。
最终名称为 `requirements-final`、`requirement-status-check-final`、
`plan-status-check-final`、`status-final`、`build-final`、
`diff-check-retry`；首次日志和复测日志保留，不覆盖早前 2026-10-10 证据。
本机临时清单/日志不构成共享持久归档。本轮没有产品测试或 SSH/LDAP 实验，
PORTAL 的运行验收仍全部未执行。


### 5.3 下一步

先完成 §3 的固定版本能力实验，再按 M1—M3 交付。
首个 LDAP 写入具备审计与失败保护，管理员强改等待受限本人入口实测通过。
应用目录完整验收仍未完成，账号矩阵也未完成，两个计划均不归档。

## 6. 固定版本原生能力实验（2026-10-11）

### 6.1 范围、基线与证据

本次仅验证已确认设计所需的 Casdoor/Samba 能力；没有实现门户。
SSH 主机为 `whl@finance.hlong.wang`，使用全新 network namespace、专属 Docker/containerd
socket、私有数据根、虚拟 `portal.test` 域和普通测试账号；不修改业务目录或默认 Docker 栈。
不运行 Authentik，也不把本次 Casdoor 结果复制给 LLNG 或其他应用。

源码基线为 HEAD `46669e59dcea1503021c0628a6a348df3f33f167` 加工作树，逐文件摘要见
`evidence/source-baseline.json`（SHA-256 `28bbafe16de385b9f043e99d8dca8ad87331dd4ad5343f41b053cccf5f5112e7`）。
Casdoor 上游固定 `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e`，归档 SHA-256
`365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460`，应用当前 0001—0008 补丁。
使用 Go 1.26.6、Linux amd64；关键制品 SHA-256：

| 制品 | SHA-256 |
| --- | --- |
| ANAS CLI | `52ea23a444012ac74a5def4112a6beb39f843bf4548e46af47c59b35a8d98939` |
| Casdoor server | `95c90c397a944e3688df559b69ef6c691d2872925b642df1d91563142313ca6b` |
| 定稿 LDAP 探针 | `f7c73d4f4e5a7fc4bf24863c11780277fd3aed4ecc2404c638c5e423a715a1e1` |
| 最小源码/Hook 上传包 | `252f065bfac83a503fb8f21064d64426e8b2fe065623f86202df4bc1b9e7d417` |
| 定稿探针源码包 | `846e66cf3124f810cca882e82ddb1e79ed6450b6d5d295ed8b1de82648ad3f94` |

部署 ID `20261011T052047Z-3ff4ad95`，Samba 实际版本
`4.23.6-Ubuntu-4.23.6+dfsg-1ubuntu2.2`，Casdoor 3.143.0-r12 候选。
候选镜像在私有 daemon 中由既有基础镜像覆盖当前源码/二进制构建；
基础/最终 image ID 见 `images-input.json`、`images-built.jsonl`、`images-final.jsonl`。
这不是完整发布 Dockerfile、多架构构建或全部依赖模块验收；PG 扩展引导也未单独验收。

共享证据保留在主机 `/home/whl/anas-user-portal-test-20261011-296d0a61/evidence/`。
原始日志、所有重试、定稿脚本/摘要和逐项 JSON 均在该私有目录；不进入 tracked Git。
归档与清理摘要在本节结尾记录；本机 `/tmp/anas-user-portal-test-20261011-296d0a61/`
只是工作副本，不作为共享持久归档。

### 6.2 原生结果及限制

| 实验 | 定稿结果 | 关键证据 |
| --- | --- | --- |
| LDAP normal / TLS / 单成员条件 / 并发 / 解锁 / 最小年龄 / 真到期 / DN 复用 | **56/56 通过**，同一定稿探针完整复跑 | `frozen-ldap-summary.json`、八份 `frozen-ldap-*.json`、`frozen-run-final.log` |
| LDAP critical assertion 与未知 control | 两者返回 **12**，不写入；固定 Samba 不支持 assertion | `ldap-controls-wire.json`、`frozen-controls-wire.log` |
| Casdoor HTTPS 原生协议与权限边界 | 15 项观察成立；其中包含权限过宽和复用旧认证的缺陷观察，不能称为 15 项产品通过 | `casdoor-capability.json`、`casdoor-capability-final.log` |
| Casdoor Chrome 154.0.8037.99 原生登录 | 3 项通过，**2 项失败**：max_age=0、prompt=login 自动返回旧认证 | `casdoor-browser.json`、`casdoor-browser-complete.log` |

LDAPS 正确 CA/服务器名通过，错误 CA 与证书 SAN 之外的名称被拒绝。
普通原密码 change、错误原密码、长度/复杂度/历史/一天最小年龄拒绝通过。
首次强改和真实到期均先确认普通 bind 返回 49，再由现有只读绑定执行原密码 delete/new add：
错误原密码返回 19，正确原密码成功，新密码正常 bind。真到期用隔离域 10 秒 maxPwdAge
并核验当前时间已越过到期点；测试后恢复夹具策略，没有修改生产密码策略。
reset → pwdLastSet=0 → 受限原密码 change → 新密码登录的原生闭环通过。
真实锁定后单独清零 lockoutTime，原密码重新登录成功，UAC/pwdLastSet 不变。

专属 reset 服务只获隔离 PortalA OU 的 Reset Password、pwdLastSet、lockoutTime 权；
跨 OU/服务 OU 的 reset、受保护人员的 reset，以及启用账号属性写入被拒绝。
保护人员的其他属性拒绝仍须随门户动态保护检查验证，不能由 reset 拒绝外推。
专属成员服务只获 PortalAllowed 的 member 权；
其他组/属性、reset 服务及原 svc_password 的组写入均被拒绝。
但成员服务可把 PortalB 人员加入允许组：**目录 member ACL 不限制人员值范围**，
必须由门户实时核验同一完整委派规则、目标范围和直接/间接保护关系。

组 <GUID=…> 寻址、人员 GUID/SID 成员值、两个连接并发添加不同成员均成功且保留其他成员。
单值重复 add 返回 68，缺失 delete 返回 53；旧人员/组 GUID 在同 DN 删除重建后返回 32，
新组保持不变。不得回退旧 DN，不得将所有 53 判为可忽略冲突。
assertion 未支持，首版采用已验证的稳定引用/单值操作；门户外管理员同时修改保护关系
仍是未执行反例，不能宣称这些操作形成跨对象事务。

Samba 改密后旧密码仍能 bind，符合原生旧密码宽限期行为；旧密码不能再次更改当前密码。
PORTAL-R-027 的门户会话注销仍需产品实现，不由这个原生现象代替或否定。

Casdoor 实际 RS256/JWKS、issuer/audience、nonce/auth_time、错误 PKCE verifier、code 防重用
和无 client_secret 的 PKCE/S256 code 交换通过；refresh 保留 auth_time，重新输入密码认证更新它。
浏览器回调校验 state、签名和同一 sub，token/JWKS 通道校验 CA/SNI；浏览器页面为私有 CA
夹具使用 ignoreHTTPSErrors，不据此计作门户 TLS 产品验收。
max_age/prompt 的 HTTP 与浏览器结果一致，**不能提供所需强制近期认证路径**。
普通应用 secret 可创建应用、修改测试用户；新普通 consumer secret 可读取另一组织用户。
门户不得保存这种通用 API 凭据；最小受限授权端点尚未实现。

DN 复用反例之后 watcher 报 `directory identity conflict; admission removed for poperator`：
新 AD anchor 与先前导入的同名 Casdoor 身份不同，源码的冲突过滤拒绝入场。
保存 `dirwatch-final.log`、`dirwatch-health-final.json`；本次不宣称目录同步/撤权链完整通过。

### 6.3 按 PORTAL 需求记录的结论

以下均是实现所需证据，不勾选产品判据；§4.2 的 30 项门户 e2e 和全部 40 项产品验收仍未执行。

| 需求 ID | 本次能力证据 | 产品剩余/阻塞 |
| --- | --- | --- |
| R-002、R-003 | 原生密码登录的签名/请求绑定正例、稳定对象 DN 复用反例 | 门户 callback 全部拒绝反例与会话 anchor 绑定未实现 |
| R-006 | auth_time 真实来源通过，max_age/prompt 浏览器失败 | 修复强制再认证；门户缺失/过期/伪造时间及换身份拒绝未执行 |
| R-007、R-013 | 原生普通应用凭据过宽已实证 | 受限授权端点与当前 AD 状态/委派配置逐请求判定未实现 |
| R-014、R-015、R-017 | 属性/对象最小 ACL 与跨范围拒绝；成员值可跨人员范围 | 门户保护集合、嵌套关系与完整规则核验未实现 |
| R-016 | LDAPS CA/主机名正反例通过 | 门户连接实现与不降级反例未执行 |
| R-018—R-020 | 原密码/策略/首次及真实到期原生 change 通过 | 本人会话绑定、受限入口、普通访问隔离和错误响应未实现 |
| R-021、R-023、R-024 | scoped reset/强改恢复/独立解锁原生路径通过 | 操作理由、近期认证、部分完成、逐动作授权未实现 |
| R-028、R-030 | 单成员操作、并发保留及 GUID/SID/DN 复用通过 | 门户接口限制、anchor 映射与保护关系竞态未执行 |
| R-001、R-004、R-005、R-008—R-012、R-022、R-025—R-027、R-029、R-031—R-040 | 没有完整产品结果；探针脱敏不等于门户 Secret/审计通过 | 页面/API、审计持久性/容量/恢复、未知结果、限流和备份均待实现/验收 |

### 6.4 重试、清理与文档门禁

保留首次失败及修正后的定稿结果，不把探针错误归为产品缺陷：
错误 hostname 首次落在通配 SAN 内、Casdoor JWKS/cert 名称与回调等待、Samba 夹具初始化/anchor
和到期策略时间单位均经核查修正。LDAP BER Boolean 的 true 初次编码成 01，不能证明 critical；
另一次编码成两字节 00ff，连接失败。最后用单字节 ff 和独立原始 TLS/BER 探针证实返回 12。
Go LDAP 对该 ExtendedResponse 显示 unexpected response，协议结论以原始报文解析为准。
最初完整源码归档上传被自动审批拒绝，改用已核查白名单的最小公开源码/Hook 包，未上传完整归档。

专属容器、Docker/containerd unit、socket、namespace/veth、两个夹具 Btrfs 子卷和运行数据已删除；
本机 SSH tunnel 及临时密码文件也已清理。`cleanup.json` 确认默认 Docker 的 10 个运行容器
ID/名称/镜像/端口与测试前一致，另两个既有测试 namespace 保留；不据此宣称宿主全部状态不变。
清理首次因 Btrfs 元数据空白匹配失败，后因 Docker 留下专属 nsfs 挂载停止；
核对本轮精确路径后卸载，最终完成，三个清理日志均保留。

文档门禁通过：26 份活动矩阵/772 项要求、5 个用例 catalog/54 个 active 用例，
30 份需求状态与 30 份计划状态一致；108 份文档状态检查及站点构建通过。
本主题仍为 0/40，不建立未实现门户的假用例。首次状态生成因里程碑状态前缀不符合规范失败，
修正后通过；站点保留既有 500 kB bundle 提示。tracked diff 与新文档空白检查通过。
日志 `docs-*.log`，定稿文件摘要 `document-checks-final.json`
（SHA-256 `75f66b1c1737528e2e1a3e5550a987396edf3351295669329f5ec737c7e6f132`；
不包含本计划，避免校验和自引用），初次文件摘要也保留。

共享封存归档：主机
`/home/whl/anas-user-portal-test-20261011-296d0a61/identity-capabilities-final.tar.gz`，
54,114,136 bytes，SHA-256 `27f0f58dc4df38be99517ceea40f359da0bafc771d02cea90e276e8c1f6a9373`。
`evidence/manifest.json` SHA-256 为
`b313503d970763cc0db213441d01d46cd2a33a47d88f81fa1f248fe53b8fe884`，109 个文件逐项校验通过。
归档包含最小上传源码/Hook 包、实际 CLI/Casdoor/LDAP 探针制品和原始证据；
不含运行 workspace/临时密码。主机目录 0700、证据与归档文件 0600；初次封存版本仍保留。

### 6.5 当时建议与后续修正

当时提出先修复 max_age/prompt 并新增最小受限授权接口；这个结论把已验证的普通应用凭据过宽
推断成所有原生 API 都不可用，尚未完成原生替代路径实验。后续用户要求原生优先，
§7 已验证本人 token 与再认证应用配置，当前路线取代上述补丁/接口建议。

## 7. 原生优先补充验证（2026-10-12）

### 7.1 范围、基线与环境

按用户指示优先 OIDC、Casdoor 原生 API/配置，不新增源码补丁；退出门户同时退出 IAM，
新增 PORTAL-R-041 并归属 M1。本节是原生能力研究，不是门户或 APPCAT 第二阶段验收。
PORTAL-R-001—R-041 仍为 **0/41**，所有产品入口、审计和失败保护均未实现/验收。

新隔离目录为主机 `/home/whl/anas-user-portal-native-test-20261011-9a9c3fa4/`，
namespace 同名，socket `/run/anas-user-portal-native-test-20261011-9a9c3fa4.sock`；
子网 10.253.85.0/24，入口 10.253.85.2:29544，容器/网络前缀 `anas_pn9a9c3fa4_`。
复用 §6 固定源码、CLI/Casdoor 制品和基础镜像，另建私有 Docker/containerd、AD 域和数据库；
不访问业务目录，不测试 Authentik。三组 OIDC 客户端由原生 add-application 配置。

复用的 189 个输入文件逐项清单 `source-manifest.json`，SHA-256
`b513be7c3c934e2df3c2bdfbd651526b1b06d592f1a26577297584fd88149373`；
CLI/Casdoor server 摘要与 §6 完全一致，source parent 为 `46669e59dcea1503021c0628a6a348df3f33f167` 加当时工作树。
本轮文档基线为 HEAD `f3555bbaae463a8f9df6994345bcf0a151a5b015` 加当前工作树。
`native-baseline.json` 记录实际二进制、探针，镜像输入另见 images-input/images-final；现有 0001—0008 补丁保留，新增补丁数 0。
实验镜像仍为 §6 的覆盖制品候选，不代表完整发布镜像验收。

### 7.2 结果与接线决策

| 实验 | 固定组合的观察 | 产品适用范围/仍待执行 |
| --- | --- | --- |
| 原生 API 与固定组过滤 | `native-access.json` 46 条断言成立，包含夹具与缺陷观察，不是 46 项产品通过 | 本人 token + UserInfo/get-account 作为首选，不新增专用读取接口 |
| 组 OR 与 platform_admin | 两侧组分别允许，无组/其他组拒绝；使用 Provider 的 Admins 同源映射；撤销管理员组后拒绝 | 两套实际 Adminer/ForwardAuth 历史验收不由本轮替代；动态 AD 与目录页面仍待产品实现 |
| 管理员/状态/策略反例 | IAM IsAdmin 已确认但无显式组仍拒绝；hidden、禁用、空/不匹配组、缺 binding、停用/Pending 策略、资源/动作/model 改动、不同身份/无效 token 拒绝 | 未把任意 Casbin 模型、未知组发布或静态图标保护判作已完成 |
| 当前 IAM 事实 | 同一旧 token 撤组后 UserInfo/get-account 读取新事实；恢复组后允许，停用账号拒绝 | 不承诺 AD 尚未被 IAM 观察的变更已同步；门户还需逐请求核验 AD 状态 |
| 原生本人字段差异 | get-user 受 AccountItems 隐藏影响，把 isForbidden 置零；get-account 保留实际 true | 采用 get-account 状态，后端丢弃多余 token/字段；UserInfo 成功不等于账号有效 |
| enforce/batch-enforce | 当前 hook 的 `built-in/user-model-built-in` 正反例可判定；单独 enforce 对停用/Pending 策略仍 true | 对固定模板先检验完整绑定与状态；不给普通用户追加 enforce 权限，不直接复用默认允许列表 |
| User 类型 API key | 非管理员用户仅授予 GET get-permission；同组织策略读取成功，另一组织/用户列表/应用与权限写入拒绝；停用 key 后拒绝 | 原生备用路径可用，首版采用本人 token，无额外 key |
| 真实 Chrome 再认证/登出 | `native-browser.json` 16 条断言成立，含同会话观察 token 状态；Chrome 154.0.8037.99 | 原生配置与登出能力通过；门户产品判据全部未执行 |
| 不向浏览器交付 token 的退出 | `logout-browser.json` 23 条断言成立，含 4 条 token 状态观察；同站原生 POST 清除 Cookie、拒绝继续 SSO、原生 CORS 放行/拒绝和已退出响应通过 | 同制品 + SQLite/内置组织的能力补测，未连接 LDAP；内置组织旧 token 保留，不替代 ANAS 完整集成或 R-041 产品验收 |

应用过滤实验使用与当前 hook 相同的 permission/resource 命名和固定 model，
对启用且 Approved 的 Allow Application 权限检查明确 binding、Read 动作、资源及完整允许组集合，
再取本次 IAM 组事实判定。仅支持已确认的正向组 OR，不建立任意权限解释框架。
get-account 会返回额外 accessToken，实验不使用该值；门户只能在后端消费必要身份/状态/权限字段。

正常登录客户端保留 enableAutoSignin/enableSigninSession。单独再认证客户端两项关闭：
旧 IAM Cookie 下仍显示密码表单，实际重新输入密码得到同 anchor 的更新 auth_time。
绕过表单直调旧会话仍可以取 code，但 auth_time 保持旧值，5 秒实验窗口拒绝；
另一人员重新输入有效密码的结果被同一身份检查拒绝。max_age/prompt 的历史缺口仍存在，
采用原生表单配置加门户的签名时间/身份校验，无需新增强制认证补丁。
缺失/伪造时间、请求串用、nonce/state 和敏感操作服务器实际接线仍随 PORTAL-R-002/R-006 验收。

客户端 discovery 的 end_session_endpoint 为 `/api/logout`。真实浏览器携带 id_token_hint、
登记返回地址和随机 state 回跳成功；旧 IAM Cookie 不再自动登录，门户与同会话观察 token
不能读取 UserInfo，另一浏览器会话仍可 SSO，未登记返回地址拒绝。
上述 hint 实验是协议能力证据；将门户 ID Token 送入浏览器违反 R-004，不能作为产品接线。
不将 token 无效推断为全部外部应用的本地会话失效。

补测复用同一个 Casdoor server，另建 `anas-portal-logout-test-20261012` namespace，
入口 10.253.86.2:29545，仅用 SQLite/内置组织合成账号，不复用业务目录。
本机 SSH tunnel 与临时 HTTPS 代理供真实 Chrome 访问 29547；实际页面从代理提供，
没有取消浏览器 Cookie、CORS 或本地网络限制。
两应用使用不同 redirectUris；无 hint 的已登记返回成功，当前 Cookie 已失效，
但 IAM 已退出时原生 GET 返回成功 JSON 而不回跳。因此首版采用固定门户结果页中的原生
`POST /api/logout`、`credentials: include`，省去登出返回地址与 hint，所有门户 token 留在后端。
同站 HTTPS 的门户 origin 由原生 redirectUris 登记；真实浏览器 POST 成功，
当前 Cookie 不能再取得 SSO code；同站未登记 origin 的浏览器请求失败，原生直接请求返回空 403，
原会话保留；已退出时重试仍返回成功，另一浏览器保持有效。
内置组织 4 次 token 状态观察均仍有效；现有 `ExpireAnasParentTokens` 限定 ANAS 组织，
本补测不能证明 ANAS 无 hint 时的 token 撤销，也不能把该差异当成新增补丁的理由。
门户本地会话撤销和丢弃 token 必须先完成，结果页只报告当前浏览器 IAM 退出。
R-004/R-041 的实际 ANAS 接线、CSRF/Origin、固定结果页、浏览器限制及 IAM 失败提示仍待产品验收。

### 7.3 重试、清理与证据

保留初始化与失败重试：首个 socket 名不符合隔离守卫要求，改名并重建；
Chrome 沙箱启动失败后获准真实浏览器运行；合成人员 native LDAP 导入没有 ExternalId 时按当前 LDAP anchor 对齐；
夹具字段 uid、API 登录设置、重复对象与 immutable ID、浏览器 APIRequestContext 的 DNS 行为修正后继续。
get-user 状态被隐藏的失败保留，改用 get-account 后对应反例通过；UserInfo 空组的 null 与 [] 按空集合观察。
无 hint 补测另保留启动目录权限、SQLite 驱动名/锁等待、重复应用及 Affected 返回语义重试。
HTTP/拦截页面的浏览器 fetch 失败后改为真实 HTTPS 页面，未关闭浏览器安全限制。
原生未登录响应的 data 为非空错误字符串，改用 status/明确未登录结果；空 403 按 HTTP 状态记录。
内置组织 token 未撤销的失败观察保留，并改记实际边界，未计入 token 撤销通过。
这些失败没有被算作产品通过，也没有修改 Casdoor 源码来绕过。

封存前扫描已知测试密码、client secret 与用户 key，证据中没有命中。
本次容器、两个 Btrfs 子卷（1499/1500）、namespace/veth、Docker/containerd unit、socket、运行数据和密码已删除；
旧错误命名的空隔离实例也已清理。本地 tunnel 与临时密码文件关闭/删除。
cleanup.json 确认默认 Docker 10 个容器的 ID/名称/镜像/端口一致，两个既有测试 namespace 保留。
清理脚本末尾缺 heredoc 终止符产生警告，但 Python 执行/所有清理断言成功，原日志保留。
无 hint 补测的独立 Casdoor unit、namespace/veth、SQLite 数据库与文件会话也已清理；
本机 tunnel、HTTPS 代理、临时证书/私钥和浏览器上下文已关闭/删除。
`logout-cleanup.json`/`logout-local-cleanup.json` 记录结果，默认 Docker 的 10 个容器仍一致。
`logout-baseline.json` 核对 server 摘要与 §6 一致，测试 client secret 扫描无命中。

共享证据已封存于 `whl@finance.hlong.wang`：
`/home/whl/anas-user-portal-native-test-20261011-9a9c3fa4/native-capabilities-final.tar.gz`，
**51,223,644 bytes**，SHA-256
`34fe2e0527bcea82f7a6b0a21be658133edacaece08bae8f37dad129469b7c48`。
归档包含 189 项公共源码输入、固定 CLI/server、公共探针、全部重试、清理结果和门禁日志；
`public-archive-manifest.json` 核对封存前 300 个文件，测试数据库、密码、会话与 TLS 私钥不入归档。
`documentation-tested-manifest.json` 记录上述 HEAD 加工作树的文档门禁基线，
该基线在本段封存元数据加入前；最终文档逐文件摘要另存同目录 `documentation-final-manifest.json`。
原始日志不进入 tracked Git，也不把本机 `/tmp` 副本作为共享持久证据。

本轮 docs:requirement-status / docs:plan-status 已重新生成；
docs:check-requirements 通过（26 份矩阵、774 项要求；5 个 catalog、54 个 active 用例），
docs:check-requirement-status / docs:check-plan-status 均通过（各 30 份文档），
docs:check-status 通过（108 份文档），docs:build 通过，保留既有 500 kB bundle 提示。
tracked diff 检查及四份新文档尾随空白检查通过；本轮只修改文档，没有门户产品测试结果。

### 7.4 下一步

按已确认 M1/M2 交付单个门户：PKCE 登录、原生本人当前事实、固定模板过滤、联合登出、
只读账号与本人改密；首个 LDAP 写入同时交付持久审计与失败保护。
委派写入口按 M3 的动态目标范围、保护对象、近期认证与审计反例验收后启用。
本轮不新增 Casdoor 源码补丁；不撤销 §6 的失败观察，也不把原生能力通过当作门户已实现。
§6 的 56 条 LDAP 断言与本轮原生实验只解除对应能力疑问，两个产品矩阵均不归档。


## 8. 门户候选实现与产品验收（2026-10-14；进行中）

已实现 `modules/user_portal` 单进程模块，版本 `0.1.0-r1`，状态 developing。
使用 Authorization Code/PKCE、用户自己的 UserInfo/get-account、固定正向受管策略、当前 AD 人员/组与受限 LDAP 操作；审计为 fsync 的私有追加文件，没有新增 Casdoor 上游补丁。
新增维护中的 go-oidc 与 go-ldap 协议库，分别负责标准 OIDC/JWK 验证和原生 LDAPS。

隔离环境为 `ssh whl@finance.hlong.wang` 上的
`/home/whl/anas-user-portal-e2e-test-20261014-3486429e`，独立 namespace/daemon/socket/prefix；
固定 Casdoor/Samba/Traefik 候选沿用 §7 的上游与既有补丁，门户使用当前静态 Linux 二进制。
当前门户运行镜像 ID `sha256:52e821ffe252180e27b3bc621c96c6f479cbbe3f8a704048a87f59bfbf9e3c50`；
这是固定旧 PostgreSQL 基础镜像上的运行覆盖层，不能冒充发布 Dockerfile 已构建验收。
最终源码/二进制摘要及清理/证据归档将在本节补齐。

`test-env/playwright/user-portal-e2e.mjs` 已执行 50 项检查，全部通过；Chrome 为 `155.0.8059.40`。
真实路径包含 OIDC 登录与 Cookie、权限后的搜索/语言/七档图标、范围/分页/嵌套来源、
过期近期认证拒绝、原生再认证、GUID 单值成员增删与冲突、专用 ACL 拒绝、原子 reset+强改、
受限强改闭环、独立解锁、目录密码策略拒绝、本人改密后的门户会话失效、新密码登录、
改名/停用/撤组的实时拒绝、范围内审计，以及门户与当前 IAM Cookie 联合退出和故障重试。
原始报告和仅应用页截图暂位于本机同名 `/tmp/.../evidence/`，最终需转存共享主机归档。
这些通过结论仅覆盖报告中的具体断言；其余判据及故障反例尚待执行，里程碑不标为完成。

已保留失败重试：原生 UserInfo 未选择 Groups 导致回调拒绝（用 tokenFields 配置解决）；
no-referrer 导致真实表单 Origin=null（改 same-origin 策略，继续严格拒绝 null）；
测试窗口 5 秒在完整页面加载后不足（测试配置改 15 秒，生产默认仍 300 秒）。
同版本镜像标签更新后必须核对容器 Image ID，必要时在隔离 Compose 中强制重建，
不得把旧容器检查算作新代码通过。

后续原生适配器 11 项、实际过期/限流/回复丢失/审计故障 19 项、真实回调/保护/身份复用 6 项均通过，累计 86 项。
最终运行覆盖镜像 ID 为 `sha256:0b7790664b297922c6e733a0dc6d641a544b04f5bd38a4ff9b5983a411045744`，
包含未知/锁定状态失败关闭、当前 IAM 必需状态字段校验、JWK 端点校验和审计一次快照；19+6 项在此基线上执行。
50 项浏览器主流程保留前一运行基线，不以版本字符串相同冒充最终制品的完整重新验收。

当时阻塞：正式 Dockerfile 的 Docker Hub 拉取出现 TLS handshake timeout；公共镜像站重试亦达到 120 秒超时。后续构建结果见 §9。
构建上下文静态门禁通过，但不代替 Dockerfile 构建。尚需正式镜像构建及最终制品的全套复验，
因此本计划、M1—M3 和 Module 保留 implementing/developing，不归档、不宣称 41/41 完成。
全量目录跨 Module 验收仍由 APPCAT 计划负责。


### 8.1 本轮门禁与验证限制

关联门户/Casdoor Hook/appcatalog 并发测试、Runner 参数 schema/inventory 和目录相关测试、
modulepackage/gen-module-docs、shared build 静态上下文、测试用例与文档状态/需求/计划索引门禁均通过。
广泛 Runner 测试在 Darwin 上仍出现现有 Linux managed temporary storage 限制；没有据此修改无关存储代码，
也不把选定测试通过宣称为整个 Runner/Linux 验收通过。静态发布构建路径已修正为仓库标准共享上下文参数。

需求/计划已归入 user_portal Module 私有 dev-docs，APPCAT 的编号和归属不变。
源码文档与研究的当前实现/实验状态已分开维护；原始日志和重试不进入 Git。

## 9. 正式 Dockerfile 构建与最终复验（2026-10-14）

Docker Hub 在 finance 宿主及隔离 namespace 均连接超时；仓库已有的
`m.daocloud.io/docker.io` 可达，修正第一次镜像站名称后基础镜像拉取成功。
复用 `GOPROXY_URL` 和 `CHINESE_BUILD_SPEEDUP`/`APK_MIRROR_URL`；没有关闭下载 TLS。
首次官方 APK 下载耗时 156 秒并成功；镜像站一次 502 重试保留，最终加速构建成功。

正式 Linux/amd64 镜像由当前 Dockerfile 编译，摘要为
`sha256:16e8af16cfd106eb84589d91914fffee53f44756fb61de09d424f808d953030c`；
实际容器 Image 一致，rootfs 只读；二进制 SHA-256
`2f7b919cc4335fc9c51b9b1b7f75dfb7edff37356c0e8dc8a4d7e25562234477`。
构建 Go 基础镜像为 `sha256:3082400e369fa24d5fc60bca20edab3f6d604e0c5a690ec66b295eff4dd87ade`，
Alpine 基础镜像为 `sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1`。
仍复用固定 Casdoor 3.143.0-r12 server 及既有 0001—0008 补丁，本轮新增上游补丁数为 0。

测试夹具重置的重复 Admins 成员导致初始化中断及旧输入残留，修正后完整重建并核对 8 人/6 组；
身份复用反例留下的原生软删除记录仅在隔离组织中清除，再立即恢复原生软删除配置。
外部 SSH 隧道反复超时/中断，不判作产品通过；页面已回调而 networkidle 等待未结束的记录保留。
改为 DOM + 实际 API 就绪；测试窗口 60 秒并等待真实过期，生产默认仍为 300 秒。
复用仓库固定 Playwright 1.62.1 官方工具镜像
`sha256:dcc5531e97840b9b5e794f2814476b21571c5124a3fca2267d73041f56e7580e`，
在 finance 私有 daemon/namespace 内直接运行真实浏览器，不添加产品服务。

最终结果：Chromium 151.0.7922.34 的主要浏览器 50 项、故障 19 项、边界 7 项，
实际 LDAP 适配器 13 项、实际审计契约 6 项，**95/95 通过**；
均以本节正式镜像/同一运行源码基线重新执行，不复制 §8 的覆盖镜像结论。
28 个运行输入（go.mod/go.sum、门户非测试 Go/UI、appcatalog 和 securefs）逐文件摘要与当前源码一致。
R-001—R-041 的逐项来源见 §4.2；相关 race 单元/契约、参数/inventory、modulepackage/
gen-module-docs、共享构建与文档门禁通过。M1—M3 完成，首版矩阵 **41/41**。
本轮仅验收 Linux/amd64 的门户首版；全 Module 的 APPCAT、其他 IAM/所有登出方向、多架构发布
仍由对应计划负责，不据本主题宣称已通过。

正式容器实际进程 UID/GID 为 65532/65532，rootfs 只读；
清理前默认 Docker 的 10 个生产容器 ID/名称/镜像/端口与测试前一致。

最终脱敏证据已在 finance 封存：
`/home/whl/anas-user-portal-e2e-test-20261014-3486429e/verified-final-evidence.tar.gz`，
3006 bytes，SHA-256 `dc42b76c18abad792ca6759304085a0ad26c85ac47531bb285396f20c9f721a3`。
仅含白名单逐项结论和制品元数据；随机身份已替换，已扫描实际测试密码及 token。
不含原始日志、截图、数据库、会话、私钥或运行凭据。清理与公开源码封存见 §9.1。

### 9.1 清理与交付边界

仅本轮 8 个产品容器及工具容器、私有 Docker/containerd unit/socket、namespace/veth、
Btrfs data/userdata 子卷 1501/1502、运行 workspace/cache、绑定凭据和浏览器依赖已清理。
默认 Docker 的 10 个生产容器 ID/名称/镜像/端口仍一致，既有两个测试 namespace 保留。
清理原始记录位于同一共享主机目录的 evidence/cleanup.json，脱敏清理结果加入最终封存归档。
代码尚未提交或发布到生产；本轮不测试已移除的 Authentik。

最终证据（含清理）位于同目录 `verified-final-evidence-with-cleanup.tar.gz`，
3664 bytes，8 项文件，SHA-256
`bfa90af0c3304d417c63a05a651da2077200a03469cebc781a45015c602d4384`。
公开源码白名单 70 项，`verified-final-public-source.tar.gz` 为 146858 bytes，
SHA-256 `6690afacc3cd2eb2d8834634b13bad93329da4ae25fce6b36f25ef103c3c0b7d`；
源父提交 `ad1e0b982b9eb8bedf369740558f7e2c77c45799` 加工作树，逐文件清单同目录
`verified-final-public-source-manifest.json`，不包含编译输入以外的运行私有数据。
该源码快照在本段封存元数据加入前；最终文档摘要另行保存，避免校验和自引用。

相关门禁通过：门户/Casdoor Hook/appcatalog race，Runner 选定参数/库存/目录测试，
modulepackage/gen-module-docs，共享构建上下文、用例/需求/状态/计划索引、文档站构建及空白检查。
广泛 Darwin Runner 的既有 Linux 临时存储失败仍见 §8.1，不宣称全仓库或 Linux Runner 全绿。

本机本轮私有密码输入、临时依赖及含会话字段的诊断已删除；专用 SSH 转发均已关闭。
共享 `local-cleanup.json` 仅保存三项布尔结论，`documentation-final-manifest.json` 保存最终文档摘要。

## 10. 提交前审查与修复（2026-10-14）

审查基线为 `ad1e0b982b9eb8bedf369740558f7e2c77c45799` 加本任务工作树。
沿当前调用链发现两项既有判据缺口，修复不增加产品范围或 Casdoor 补丁：

| 判据 | 观察到的问题 | 修复与当前验证 | 剩余动作 |
| --- | --- | --- | --- |
| PORTAL-R-038、R-039 | 稳定身份限流触发后，未耗尽提交标识限流的别名返回 429；不存在的标识返回 409，泄露存在性 | 保留写入阻断，身份桶命中使用统一 409；所有提交标识/IP/全局限流仍在查询前执行。新增 HTTP 契约反例，已通过 race 回归 | finance 新正式制品运行复验 |
| PORTAL-R-032、R-034 | 成员请求在解析失败时先于 adminGuard 拒绝，审计 Actor 为空 | 从已绑定会话记录操作者，失败仍不写 LDAP；新增持久审计契约反例，已通过 race 回归 | finance 新正式制品运行复验 |

整仓门禁发现并修复 Hook Provider 名分支和升级清单遗漏。沿现有绑定增加账户 API 格式声明，未知/缺失时拒绝；升级登记为 no_prior_release，不增加新服务/API，也不宣称升级实测通过。

其他修正：publicsuffix 为直接 Go 依赖；校正中英文说明/侧边栏中的过期状态。
历史正式镜像 `sha256:16e8af16cfd106eb84589d91914fffee53f44756fb61de09d424f808d953030c`
和 §9 的 95 项运行检查属于修复前源码，不转移到本次提交。旧测试环境已清理，
本次审查没有重建该环境，不能报告修复后镜像已通过 finance 全套验收。
完成正式构建和 finance 全套复验后才重新归档。

本次隔离提交快照验证：门户/Casdoor Hook/appcatalog race 回归，各 Module Go 测试、internal/config、modulepackage、gen-module-docs 均通过；全仓 go vet、Runner IAM 解耦/目录/库存选定测试、共享构建静态检查、升级清单、用例摘要与需求/状态/索引检查通过。文档站构建通过（快照没有发布标签，标识 development；不代表发布）。

完整 Runner 在 macOS 上仍有 Linux 受管临时存储限制失败：TestCLIApplyRetriesFailedFrozenMaintenanceCandidateAfterHookFailure、TestCLIApplyCannotReaddHistoricalPostgresOverRetainedData（5 分支）、TestCLIApplyAllowsFirstPostgresWithNoPriorPostgresRecords（2 分支）。本轮以父提交 ad1e0b98 单独复核这三个入口及全部分支，确认同样因 local Linux filesystem 要求失败；不将选定测试通过宣称为完整 Runner 通过。提交快照保存在 /tmp/anas-portal-review-20261014，原始测试输出只在会话/临时环境，不作为共享耐久验收证据。

源码空白检查通过；保留的 0008 补丁通过 git apply --numstat 的统一差异语法解析。补丁上下文的首空格属于差异格式，未把它计为 Go 源码缩进错误。

## 11. 指定提交的正式镜像复验（2026-10-14—15）

以下按执行顺序保留构建、问题发现和重试记录；最终通过基线见 §11.3。

用户指定源码提交 `84f377cca2c468080d0b58efd12c42c07f2282f3`。运行输入来自该提交的干净导出，
保留无关工作树改动；仅验收脚本补充真实 UPN 别名和 §10 两项回归，不修改门户运行源码。
本轮独立环境为 `anas-user-portal-e2e-test-20261014-cbc88ccc`，
路径 `/home/whl/anas-user-portal-e2e-test-20261014-cbc88ccc`。
finance 的原 SSH 入口传输间歇中断；用户提供 `ln.hlong.wang:2200`，已确认它是另一台主机，
只用作 SSH 跳板，没有修改其现有容器。测试继续在 finance（hostname `fengoffice`）的私有 daemon/namespace 中。

214 项公开构建/测试输入已逐文件校验。正式 Dockerfile 的第一次 Go 镜像拉取遇到镜像站 502，
重试构建成功；Linux/amd64 镜像 ID 为
`sha256:052ad457f0c44588b222a2165104277888c81c345a051f8a560ed1831b7d6503`。
该结果目前只证明正式镜像构建成功。实际容器部署、全部 PORTAL-R-001—R-041 的运行复验、
§10 回归、审计/目录边界、最终制品核对、证据封存和环境清理尚待完成。
不把 §9 历史通过结果转移到本次制品。

### 11.1 验收主机调整与准备

用户随后明确允许把全套验收改到 ln 的独立环境，并要求不修改既有容器。
ln（hostname `liannong`）根盘仅约 341 MiB 可用，容量分析另见[只读评估](../../../../../dev-docs/reviews/2026-10-14-ln-root-disk.md)；
没有清理既有数据、镜像或容器。本轮新目录位于
`/data/anas-user-portal-e2e-test-20261014-f2ac813b`，独立绑定到同名 `/home/whl/` 路径，
用于兼容当前测试夹具的路径校验；Docker/containerd、工作区和镜像缓存都在该目录内。
namespace、socket 和容器前缀分别为同名 run、`/run/anas-user-portal-e2e-test-20261014-f2ac813b.sock`、`anas_puf2ac813b_`。

指定提交的干净源码、CLI/7 个 Hook、Casdoor server/helper 已成功传输并核对摘要。
Casdoor server/helper 与 finance 使用的既有二进制逐字节相同，SHA-256 分别为
`95c90c397a944e3688df559b69ef6c691d2872925b642df1d91563142313ca6b`、
`4a6d3a3b6990a797326003fe6d632dcfe27ce037ee7edffb852502c9d4ba90ff`；仍为既有 0001—0008 补丁，没有新增 Casdoor 补丁。
正式门户 Dockerfile 正在 ln 重新构建，不能据 finance 的构建摘要宣称 ln 制品相同。

浏览器依赖的初次抽取遗漏 `playwright/node_modules/fsevents@2.3.2`，npm ci 明确失败；
已补齐仓库锁文件中的该条目，不修改产品依赖版本或忽略完整性校验。后续安装和实际验收尚待通过。

### 11.2 复验中发现的问题（2026-10-15）

ln 的正式 Dockerfile 首次拉取 Alpine 遇到镜像站 502，重试构建成功，镜像 ID
`sha256:05e006b956198a91818778ca250c955978b74fa3ac463660c5d59adbc1e9cbdd`。
锁文件补齐后 npm ci 通过。准备脚本的 PostgreSQL 覆盖镜像漏了维护工具，且设置 ENTRYPOINT 时
没有保留 CMD，容器退出 127/后续检查退出 137；改为当前 PostgreSQL Dockerfile 的完整构建，
使用校验为 `69f4019389af05dc1c9548deb8628e62878e6e207c03907f2b8af2016472cdaa` 的固定 pgvector 0.8.2 源码，
仅在隔离构建上下文将下载输入替换为已校验本地 COPY、APK 下载站改为 HTTPS 镜像。
完整依赖镜像 `sha256:b9a36115b71be2469f610e6204bad242fd349bb5b7043b97c4380aa70c91d0c0`。
配置导入脚本误用了 apply 的 `--module-root` 参数，已按 config import 接口改为 `--root`。
以上为准备脚本问题，不作为产品源码缺陷或通过判据。

实际启动发现现有 PORTAL-R-001/R-004 的缺口：CLI 运行在 umask 0077 时，应用目录生成权限被裁窄，
封存后的 catalog.json 为 0400、目录 0700，降权到 UID 65532 的门户无法读取，启动明确失败。
修复 `internal/appcatalog.WriteBundle`：为只读挂载显式设置目录 0755、JSON/图标 0644，再由既有封存移除写位；
私有 deployment 父目录与认证 API 边界不变，不新增 Casdoor 补丁或服务。
新增 Unix 权限回归在干净 84f377cc 源码上失败，在修复源码上通过；关联 appcatalog/门户/Casdoor Hook race 通过。
制品基线因此改为 **84f377cc + 本次共享目录权限修复**，原 84f377cc 构建成功不等于该缺口已通过。
修复后的正式重建与全套验收仍待执行。

finance 的本轮独立 Docker/containerd、namespace 及镜像缓存已移除；两个既有测试 namespace 和
10 个业务容器 ID/名称/镜像/端口保持一致。正式镜像已保留在
`/home/whl/anas-user-portal-e2e-test-20261014-cbc88ccc/evidence/formal-user-portal-image.tar.gz`，
7571962 bytes，SHA-256 `24fd61fa9660b2a4b83ab07b6c1c385bc808ac5af672f6d183fcefb87e8cd2a1`，
二进制 SHA-256 `eb0ed299467590d2768c0018fd864b1ac798d8e06187df9e02a37e5dd057d1c3`。
该镜像仍属修复前，仅记录构建与清理，不计作运行验收。
214 项公开输入封存在同目录 `verified-public-source.tar.gz`，SHA-256
`31da92c21cb3831c7f5f725990b270c6fd7b50987db1b54e15e23df5d6f705d8`。

### 11.3 最终验收与清理（2026-10-15）

用户确认将验收主机改到 ln 后，最终源码为 `84f377cca2c468080d0b58efd12c42c07f2282f3`
加 `internal/appcatalog/icons.go` 的显式目录制品权限修复；该文件 SHA-256
`8da435a1f2990a004d4bfa65a1f130df3aefae93c84960ecb16ecf8f1cc76066`。
未带入 AI Agent、Incus 或其他无关工作树改动。四项夹具脚本变更分别补实际 UPN 别名、
限流/审计回归和按 run.json 选择端口；没有以修改运行源码或跳过断言来处理夹具错误。
浏览器初次误用 finance 端口，连接拒绝且执行 0 项；纠正后从未变更的初始夹具开始全套重跑。

最终正式 Dockerfile 的 Linux/amd64 镜像为
`sha256:5a8b10a483026de35f0d8d5db44cb7df14f36f1a3bc8b88e9b8f95072c871119`，
实际容器 Image 一致；二进制 SHA-256
`bdf40136077908094d7b75011fa933f0dee8bfeeeff726128aa1a43c9249b333`。
Go/Alpine 基础摘要分别为
`sha256:3082400e369fa24d5fc60bca20edab3f6d604e0c5a690ec66b295eff4dd87ade`、
`sha256:4bcff63911fcb4448bd4fdacec207030997caf25e9bea4045fa6c8c44de311d1`。
214 项公开构建/测试输入在最终运行环境逐文件校验。
服务健康，PID 1 的 UID/GID 均为 65532，rootfs 与目录挂载只读，目录 0755、catalog.json 0444。
委派规则为 1 项，测试近期认证窗口 60 秒；生产默认 300 秒。
仍使用同一既有 Casdoor server/helper 及 0001—0008 补丁，本次新增 Casdoor 补丁为 0。

| 验证 | 结果 |
| --- | --- |
| Chromium 151.0.7922.34 主要浏览器流程 | 50/50 |
| 故障回归（含稳定身份别名限流隐私、无效成员请求 Actor） | 28/28 |
| 实际 LDAP 适配器/最小权限与 TLS 边界 | 13/13 |
| 身份/DN/组名称复用与保护边界 | 7/7 |
| 最终持久审计契约 | 6/6 |
| 最终编译 Hook 的账户 API 格式探测 | 3/3 |

运行检查 **104/104**，另有配置探测 **3/3**，合计 **107/107**。
执行顺序为 browser、faults、native、boundary、最终 audit；boundary 会重建对象，不能在其后用旧 anchor 验证 native。
PORTAL-R-001—R-041 全部按 §4.2/§4.3 规定方式通过，M1—M3 完成，首版 **41/41**。
R-012 的规则不拼接判据按需求规定使用单元；R-016 的原生报告提供严格 CA/服务器名反例，
不因原生报告把 ID 放在报告顶层就遗漏其覆盖。appcatalog/门户/门户 Hook/Casdoor Hook race、
Runner 目录相关选定测试、相关 go vet、用例及文档门禁通过。
这里不宣称完整 Runner、全部 APPCAT/所有 Module/IAM 组合或多架构发布已经通过。

专用容器、daemon/socket、namespace/veth、工作区、私有绑定输入、浏览器依赖和 owned Btrfs data/userdata 子卷均已移除。
临时绑定目录也已卸载，最终共享位置为 `/data/anas-user-portal-e2e-test-20261014-f2ac813b/`。
清理脚本的“默认 Docker 整体集合完全相等”检查返回 1：期间新增
`mesh-protocol-test-ln-client`、`mesh-protocol-test-ln-v2ray` 两个非本轮容器。
随后逐项核对，原有 **36** 个容器的 ID/名称/镜像/端口全部一致，无删除或变更；新增两项未处理。
保留原始失败结论和新增项清单，不把集合检查改写成已通过，也不恢复或删除其他任务容器。
用户要求的“现有容器不改动”以及本轮专用资源清理均通过。
根盘历史数据与旧 Docker 镜像没有清理，相关建议仍在根盘评估中。

脱敏证据与制品共享封存如下；不含原始日志、截图、会话、数据库、私钥或测试密码：

| 文件（上文共享目录内） | 大小 | SHA-256 |
| --- | --- | --- |
| `verified-final-evidence.tar.gz` | 4615 bytes，11 个安全 JSON | `42a6ea4d7d6cecbaf25b46f053d5453af31f28f9c99ed4cb47ea0283d6d4098c` |
| `verified-public-source.tar.gz` | 321621 bytes，214 项白名单输入及新增权限回归和清单 | `ed629695b37ea162adedbf6de79a5a3a7e6c134b7a02585d95c40990ec106299` |
| `verified-formal-user-portal-image.tar.gz` | 8020155 bytes | `842434c6563d4a8b953e73a326a44fca0b2ee875b335f955ea6cdc16093ca60d` |

归档中的随机身份统一替换为 `[fixture-id]`，已扫描实际测试密码及 JWT。
本机 `/tmp/anas-user-portal-e2e-test-20261014-f2ac813b/final-evidence/` 只是安全证据副本；共享持久位置仍为 ln 的 `/data`。
权限修复及夹具/文档更新单独提交；本轮未发布到生产，已有 84f377cc 提交保持不变。

最后保留检查：本轮原始日志、截图及传输副本已删除，正式镜像/公开源码/安全证据三份归档保留，
私有运行输入与临时宿主绑定路径均不存在；共享 `final-retention.json` 保存四项布尔结论。
文档站最终构建通过（按仓库当前标签生成 v0.1.1 文档，不代表本次修复已发布）；
归档后的需求、用例摘要、计划/需求索引和文档状态门禁通过，源码空白检查通过。

## 12. finance 既有测试栈部署（2026-10-15）

用户指定替换 `finance.hlong.wang:10000`，确认把当前配置服务全部部署。
目标工作区为 `/home/whl/anas-finance`，容器/网络前缀 `anas_finance_`。
保留原有 lego、traefik、samba_dc、postgres、casdoor、eturnal、nextcloud、collabora、immich
九个 Module 的配置，新增 user_portal；沿用原域名、端口、数据目录和可选服务开关。
本节是保留数据的实际部署记录，不将 §11 隔离夹具的 107 项检查冒充既有目录验收。

Module/门户部署源码为干净提交 `f8056361`，包含 `850b7094` 的共享目录权限修复；
仅提取 805 项运行输入，压缩包 SHA-256
`e0d5438d7d2239f194050ed1de4ef66f4e8a45de2ac53dce9757b6c23c094fd6`。
服务器使用现有 Go 1.26.6 编译 CLI 和十个 Hook；无关工作树改动未进入部署。
门户复用 §11 最终正式镜像归档，完整传输校验为
`842434c6563d4a8b953e73a326a44fca0b2ee875b335f955ea6cdc16093ca60d`，
二进制仍为 `bdf40136077908094d7b75011fa933f0dee8bfeeeff726128aa1a43c9249b333`。
该正式二进制的构建信息为 Go 1.26.9；服务器备用 Go 1.26.6 门户构建未用于部署。
Docker 导入采用归档原标签，再校验实际二进制，避免把归档配置摘要直接当作当前引擎的镜像索引引用。

其他依赖复用本机固定版本基础镜像：无变化的运行输入先核对文件摘要；
Traefik、Lego、Samba 和 Nextcloud 使用当前运行文件重新打包。
Casdoor 使用 §11 相同摘要的 server/helper 和当前入口脚本，入口用户保持正式 Dockerfile 的 root，
随后由既有入口降权；继续保留既有 0001—0008 补丁，无新增 Casdoor 源码补丁。
这些依赖是本次本机测试制品，不宣称全部重新执行了发布 Dockerfile；
逐镜像来源、摘要和打包日志保留在下列专用目录的 `image-preparation.json` 与 `image-builds/`。

原始诊断和私有输入保留在 finance 的
`/home/whl/anas-portal-deploy-20261015-f8056361`，目录 0700；不提交 Git。
切换前 `data`、`userdata` 均保存只读 Btrfs 快照，配置/凭据/状态与旧部署保存为私有归档：
`rollback/control-state.private.tar.gz`，24031041 bytes，SHA-256
`ec8537eef20de5b7de1b7981874d9bbe3f84260bbe4594f59ba3571a034a0db5`。
正式 CLI 再创建 ANAS 恢复点 `20261015T051240Z-119a4d6e`，包含旧运行镜像与数据；
新冻结部署为 `20261015T050100Z-42a75a01`。

首轮启动在恢复归档完成后被临时目录空间门槛拒绝：可用约 7.5 GiB，Collabora 要求 8 GiB。
仅清理本轮可再生成的 Go 编译缓存，并让两份旧恢复归档中经内核验证相同的数据块共享 Btrfs 存储；
两个文件及路径均保留。受影响归档完整 SHA-256 复核仍为
`fbde68270c824d6308b551b56b5691ca9109a4f1464b37a31adf2716348b38b2`，
核验后可用 9139027968 bytes。证据为 `storage-final-verification.json` 和 `dedupe.log`。
保留首轮失败日志，按数据库维护保护要求重试同一冻结部署，不跳过恢复点、不换用其他候选。

重试另发现 Core 恢复缺陷：首次失败把 2026-10-07 的历史临时切换终态改为 `recovery_failed`，
而当前活动部署为 2026-10-08，因此历史转换与当前活动标识不匹配。
新增终态保护：后续失败补偿保留 `complete`、`restored`、`cleanup_deferred`。
新回归在原源码的三个分支均失败，修复后全部 `^TestTemporary` 单元通过。
按历史转换两端、已应用根和无目标容器核验，仅恢复历史记录为 `cleanup_deferred`，
原注册表另存 `historical-temp-before.private.yml`，全部租约和目录保留。
部署 CLI 改用 `f8056361 + temp-recovery`，二进制 SHA-256
`43681d841ad33c412e9f26bbb6c79356de2138ca3d71b65258368773fe424a18`；
修复源码及对应 TEMP-R-016 / TEMP-T-008 验证见[临时存储计划](../../../../../dev-docs/plans/archived/workspace-temp-storage.md#2026-10-15历史终态被后续失败补偿重新打开)。
门户镜像、Module Hook 与冻结配置均保持原制品，不新增 Casdoor 补丁。

随后实际启动到 Samba，发现本轮本机覆盖镜像打包错误：把 Git 中为 0755 的
`/etc/cont-init.d/01-anas-ca.sh` 统一设为 0644，入口明确报 `Permission denied`。
修正打包流程为保留源码可执行位，再按正式 Dockerfile 补齐显式权限；Module 源码无需修改。
用相同冻结 Compose 单独启动 Samba 验证为 healthy、0 次重启，再恢复完整 CLI 启动。
该次真实失败补偿后历史转换仍为 `cleanup_deferred`，验证了 Core 终态修复不会重新打开旧转换。
第二段重复块共享后归档完整校验仍通过，额外共享 1090519040 bytes；
Samba 就绪时根盘可用 10083123200 bytes，见 `storage-tail-repair.json`、`samba-probe-health.json`。
后续部署期间将目标容器诊断采集至 `container-diagnostics.private/`，原始内容只在私有目录保存。

独立 Samba 诊断曾遗漏正式 Compose 项目名，导致诊断容器的项目为 `samba_dc`，
与正式 `anas_finance_samba_dc` 争用同一容器名称；已核实完整容器 ID 与工作目录后正常停止并重命名，
保留诊断数据，再由正式 CLI 管理启动。此为本轮操作问题，非 Module 源码缺陷。

完整启动随后在 Nextcloud 的 `user_oidc 8.11.0` 下载遇到持续 HTTP 404/超时；
确认新旧 Nextcloud 数据挂载相同，原数据路径未迁移。复用 finance 原独立 Casdoor 测试栈缓存的
同版本公共扩展，仅读取来源代码，不修改来源容器；只提取签名清单列出的文件并核对 382 个文件哈希。
包缓存 SHA-256 为 `4a35a22cc4dadd4ffd083ef76d11302827e6accb55166719105e311b4f01a7c4`，
记录在 `nextcloud-cache-repair.json`。正常启动继续执行原生完整性检查，未绕过就绪条件。
官方版本来源见 [user_oidc v8.11.0](https://github.com/nextcloud/user_oidc/releases/tag/v8.11.0)。

本轮建立唯一普通验收账号，仅加入 `APP_nextcloud`，用于核对目录过滤、近期认证与联合登出；
凭据保留在 finance 的私有文件内，已有人员密码及组成员关系不修改，验收结束后按 GUID 核对删除该账号。
本机浏览器的凭据导出被自动审批拒绝（缺少跨主机传递凭据的明确授权），已向用户请求选择；
同时准备仅在 finance 内执行的原生 OIDC/PKCE/API 检查，避免阻塞其他部署验证。

### 12.1 最终结果与验证边界

2026-10-15 07:15 UTC，正式 CLI 对同一冻结部署返回成功，活动部署为
`20261015T050100Z-42a75a01`、`runtime_status=running`。
十个已配置 Module 全部部署，17 个常驻容器运行，15 个带健康检查的容器均为 healthy；
Lego/Nextcloud cron 无健康检查，一次性目录事件初始化容器正常退出 0。
所有目标容器重启次数为 0、无 OOM 标记。

| 检查 | 本轮结果与适用范围 |
| --- | --- |
| HTTPS 入口 | 8/8：门户首页/健康、匿名目录拒绝、Casdoor discovery、Nextcloud installed 且无维护/数据库升级待办、Immich ping、Collabora discovery、Traefik 匿名拒绝；均校验证书 |
| finance 本机原生 OIDC/PKCE/API | 37/37；PORTAL-R-002/003/008 的真实回调与本人资料正向路径、R-006 的独立密码再认证回跳和近期认证标志、R-007/011/013 的无委派动作/空用户列表/详情拒绝、R-041 的门户与原生 IAM 联合登出路径通过 |
| 应用过滤与资产 | 上述原生检查中，普通账号只见 Nextcloud，管理大组隐藏；七档 PNG 可读，匿名图标 401、未获准 Immich 图标 404；中文、英文和不支持语言回退英文通过 |
| 真实浏览器公开页面 | Chrome 155.0.8059.41，4/4：公网 HTTPS 英文登录页、匿名 API 拒绝、中文切换、法语回退英文；中英文截图已检查 |
| Nextcloud 扩展 | `occ integrity:check-app user_oidc` 退出 0，原生签名文件存在，版本 8.11.0；未跳过完整性检查 |
| 配置与数据 | 除新增 user_portal 外，原始配置整体相等；原九个 Module 配置逐项相等，24 处旧持久挂载源路径全部相同 |
| 非目标容器 | 清理后再次核对原 14 个非目标容器，ID/镜像/完整配置/挂载/状态/进程/启动时间/重启次数全部一致，含原已停止容器 |
| 临时资源 | 按 GUID/owned description 删除唯一普通验收账号，删除其私有凭据文件；精确核对已停止诊断容器 ID/项目标签后正常删除，无强制删除或卷删除 |

原生流程还验证了 IAM Cookie 在登出前可免密登录、原生登出后同一 Cookie 不再可免密登录，
以及门户 Origin 的原生登出 CORS 响应；密码与会话只在 finance 进程内处理。
这不是 finance 上的已登录真实浏览器验收：跨主机凭据导出未获授权，因此本轮未执行该项。
§11 的 107 项完整隔离验收及 41/41 判据历史结论保留；本节不宣称重跑其负向/故障/目录写入矩阵。

现有配置未声明门户委派规则及管理员写绑定，因此用户管理不会因 platform_admin 身份自动启用。
委派重置、解锁与分组管理需另行明确操作者、目标范围和动作，并配置最小目录权限后才能在该部署使用；
本轮未给现有账号增加权限，也未测试写入现有人员。

最终门户运行镜像 ID 为
`sha256:574956be6258cc89aaaca3da3ac0f55bb749d4cd61cda8e63bfdc862dc34f83f`，
容器内二进制 SHA-256 与 §11 正式制品一致。
最终检查时根盘可用约 9.4 GiB，恢复点、只读数据快照和控制状态备份均保留。
本轮未修改根盘容量保护门槛、未删除历史恢复归档，未修改 ln 上的容器。

入口如下，统一沿用 HTTPS 10000：

- 门户：<https://portal.finance.hlong.wang:10000>
- IAM：<https://auth.finance.hlong.wang:10000>
- Nextcloud：<https://nc.finance.hlong.wang:10000>
- Immich：<https://photos.finance.hlong.wang:10000>
- Collabora：<https://collabora.finance.hlong.wang:10000>

finance 上述专用证据目录内保留原始私有日志；安全摘要和公开截图另封装为
`deployment-verification.tar.gz`，12 项、55586 bytes，SHA-256
`a18e2267b4b875b9d225290912d49ce7ee349e01fb6a5e886fdb4b2875055c8e`。
关键摘要为 `verification-summary.json`、`final-deployment.json`、`native-flow.json`、
`http-checks.json`、`public-ui.json`、`nextcloud-integrity.json`、
`config-preservation.json`、`preservation-check.json`、`cleanup-summary.json`。
这些证据均不进入 Git；归档中的公开截图不含登录身份。

本轮 Core 回归已通过全部 `^TestTemporary` 选定单元；需求覆盖 735 项、59 个用例 catalog、
29 份需求/计划索引及 109 份文档状态门禁通过。本轮相关源码和文档空白检查通过；
未运行完整 Runner 测试或将既有数据目录用于破坏性的隔离验收矩阵。
部署仍基于 `f8056361 + temp-recovery`，本轮新增修复尚未 Git 提交；
同一证据目录另保留 `core-temp-recovery.patch`（SHA-256
`7f4d9a9c5ef4f6545c0e52a1a4ba654e92b1f253f4e7c7b8d82ef49130ce4df2`）及三层测试差异审阅文本，
可在干净基线上复现本轮 Core 源码、回归、用例摘要与双语指南改动。


## 13. 2026-10-16—17 扩展实施与 finance 验收

本轮执行[扩展方案](../../../../../docs/architecture/portal-account-mail-administration.md)。用户确认本人域权限、逐次验证，并明确授权三个最小目录绑定和专用验收人员。邮件后端和个人别名写入仍留待邮件 Module PoC；本轮邮箱只读。

- [x] 保存目录 DACL/锚点、配置、容器和旧制品私有基线；无关工作树改动不进入构建。
- [x] 界面、分类开关、姓名白名单/版本检查、平台角色和自身保护实现及回归。
- [x] 本人域权限、一次性确认、原生 LDAP 权限和 Secret 边界验证。
- [x] 固定镜像部署到 finance；69 项真实浏览器、22 项原生目录和 8 项 HTTPS 检查通过。
- [x] 清理六个人员、两个组、两个 OU 和一个密码策略；确认五个已导入 IAM 的测试身份停用。
- [x] 固定证据及源码封存清单；最终文档门禁记录见本节末。

### 13.1 源码、运行制品与部署范围

基线为 `f8056361` 加本轮门户差异；CLI 沿用 §12 的 `f8056361 + temp-recovery`，不混入其他未提交 Core/AI/Incus 工作。Module revision 未手动增加，不新增 Casdoor 源码补丁、数据库或同步服务。工作区仍为 `/home/whl/anas-finance`，门户入口仍为 <https://portal.finance.hlong.wang:10000>。

| 项目 | 本轮固定结果 |
| --- | --- |
| 最终活动部署 | `20261016T011103Z-0820edf3`，`runtime_status=running` |
| 最终门户镜像 | `sha256:df726754b1ff7b2fa6f541ebb048900d7c0b8004fd9f1480ae9d378c3828a6a5` |
| 容器内二进制 SHA-256 | `0b1bd579e5a3fd0a6479f15c37e389ea626355885f885e3901627dfa21fe6360` |
| 镜像构建输入归档 SHA-256 | `d5b30e8dbe42a193ac2a91917eb0e0fb0891ca519fecc1481e24c9a26c74b95d`（`source-ui.tar.gz`） |
| 先前管理员功能候选 | `20261016T004848Z-de9c068e`，镜像 `855ebcb25e8c…`；相对 §12 只改变 Samba 组注册和门户 |
| 本轮最后一次切换 | 相对上述候选只改变 `user_portal`，补齐保存姓名后当前摘要/标题的即时刷新 |
| 回退制品 | `anas-portal-rollback:20261016-before-admin` 与 `:20261016-before-ui-fix` 均保留 |

Docker Hub `golang:1.26-alpine` 拉取没有进展，精确停止本任务拉取进程后，使用现有 Go 1.26.6 以 `CGO_ENABLED=0`、`-trimpath -ldflags='-s -w'` 编译，再装入 §12 已验收的正式运行时基底。镜像与容器二进制摘要一致。这不表示本轮正式 Dockerfile 从所有基础镜像重新拉取构建成功，也未发布新 registry 制品。后续主组配置工具和验收代码修复不属于运行二进制输入；最终源码快照另存，工具 SHA-256 为 `47c8c1584d3b52d466c1eeb75e251378a12c152222f6d5decff20ea161fbb482`。

旧锁先出现 `lock_stale`；通过原生 import/lock/render 和受支持的预编译 Hook 布局解决。对八个未改 Module 核对本地依赖源码与旧基线相同后，复用其原已验证 Hook；拒绝首个意外改变十个 RenderDigest 的候选。最终首轮仅 Samba/门户改变，末轮仅门户改变；未编辑冻结部署或活动状态文件。

### 13.2 真实失败、修复和根盘恢复

- AD 姓名旧值比较忽略大小写。首个原生并发反例失败后，加入写前逐字读取复核并串行门户资料写入；完整原生用例随后通过。LDAP 单对象 Modify 继续检查旧属性值，不新增 schema。外部 RSAT/任务不共享门户锁，不能承诺全域事务。
- 本人域绑定后必须校验实际用于 Bind 的 DN 对应 GUID/SID/anchor；修复了原先读取“预期 GUID”不能证明绑定主体的问题。另一人有效密码及过期/复用 DN 反例通过。Samba 实验拒绝 GUID 绑定名，未据此扩充 Casdoor 补丁。
- 最小权限配置脚本原先漏掉 AD 主组授予的域权限。将专用 privileged 人员设为仅通过主组属于 Domain Admins：旧函数判定为 false，新函数为 true。补齐主组 SID 查找，无法解析时停止配置。再次运行配置工具没有创建账号、没有新增域角色、ACL 增量为 0；已有四名域级人员仍受保护。随后 22 项原生测试全部通过。
- 首次 `apply --snapshot` 已保存一致恢复点，但新归档占用空间后，Collabora 原有 8 GiB 门槛同时拒绝启动和自动恢复。恢复点 `20261016T001401Z-0bba0038` 的镜像归档为 2953673728 bytes，SHA-256 `3d97fdbbccb2fee2091bd8a793b14084c144d4a5799d42aa39a8538d0e9daa0d`，保留全部内容。
- 先用原生 CLI 恢复其余服务；Btrfs 内核重复块共享后完整校验不变，但实际回收空间有限。只清理可重建 Go/Docker 缓存，以及逐项核对无容器、无恢复点引用的六个公开下载镜像缓存；所有本地独有制品、容器、卷和恢复点保留。
- systemd journal 占约 4 GiB。先将本轮日志压缩保存在私有证据，SHA-256 `083be19f6c639417981856ec73fa6590c5ec57396814a0391f42cf7d04c6f9b7`，再原生轮转并缩减至 512 MiB，恢复约 9.4 GiB 可用。恢复 Collabora 后原生重新渲染 ready 部署，默认 apply 保留自动保护，未再次强制生成同一基线归档。没有降低空间门槛、删除旧恢复点或修改历史失败记录。
- 独立浏览器首次因 UID/缓存权限无法导入工具，尚未执行测试；改用缓存所属 UID 1000。后续两次分别在 60/63 个已通过检查后因精确组名/按钮选择器超时，保留全部结果并修正选择器。最终相同修复镜像完整 69/69 通过。

原始失败与重试位于 finance 的 `/home/whl/anas-portal-admin-20261016`；私有材料在 `private/`，不进入 Git 或公开归档。首次主机核验器误用了 `/healthz`，并按数组顺序比较 Docker Mounts；改用真实 `/health` 并按挂载目标逐属性比较，8/8 与 14/14 复核通过。该修正没有改变服务或放宽实际配置比较。

### 13.3 验收结果与边界

| 检查 | 结果 |
| --- | --- |
| 本地回归 | 门户/Hook、Samba Hook、appcatalog race 通过；摘要刷新后门户 race 再次通过；原生测试编译、Node/Python 语法、Module 文档生成检查通过 |
| finance 原生 LDAP | 初始只读 TLS/角色边界通过；修复后 22/22，包括大小写并发、跨 OU、主组域权限、普通绑定越权拒绝、实际本人域 ACL、另一人密码及复用 DN 拒绝 |
| finance 浏览器 | Chromium `151.0.7922.34`，69/69；真实 OIDC、资料及管理员表单、组操作、认证真实过期/重认证、域级确认改参/过期/重放、原生 reset/强改、自身保护、旧会话降权、审计无密码、页面联合登出及同 Cookie 不能免密登录 |
| 界面和语言 | 中英文 1440/390、未支持语言回退英文、分类偏好、表单错误、键盘登录通过；人工查看英文实机截图。Linux 测试镜像缺 CJK 字体，另用本机现有 Noto 字体和独立 Chrome 做 4/4 显示复核：真实公开登录页、同一生产静态资源的模拟资料管理页；明确不把后者称为已登录目录写入验收 |
| HTTPS/容器 | 严格校验证书的 8/8 入口检查；17 个常驻目标容器运行，15 个健康检查均 healthy，无重启/OOM；14 个非目标容器的 ID、镜像、配置、挂载、进程和启动时间保持一致 |
| 配置范围 | 除已批准的八个门户管理/绑定参数，其余受管配置完全相同 |
| 最小绑定 | 三个服务账号保留；没有增加域角色，没有扩大既有共享只读绑定；初始 66 项 ACL/对象修改、补齐 2 名既有人员锚点，不修改既有人员密码和成员关系 |
| 清理 | 按 GUID/专属 description 删除 11 个测试对象；剩余专属对象为 0；五个 IAM 测试身份均停用。SamDB 本地 SamDB 删除后按原有 300 秒对账兜底观察到停用，未添加同步代码。测试密码文件删除，独立 Docker 环境无容器 |

性能实测：finance 同机浏览器文档 DOMContentLoaded 185 ms，单次 `/api/me` 457 ms、目录 659 ms；匿名严格 HTTPS bootstrap 约 62—261 ms。本机公网 bootstrap 约 1.5 秒，默认解析与显式公网 IP 结果相近。已消除启动重复本人请求，并为授权后的图标使用内容版本私有缓存；没有据此认定用户当时 Chrome 的全部慢因已经复现。主机仅约 3.2 GiB 内存，启动期间有交换与健康探针超时，最终健康检查均正常。

以下每行指向最终镜像 `df726754b1ff…`；原生适配器业务源码相同，配置工具按上节单独固定。历史有效的 35 项判据继续保留 §11/12 的通过基线，本轮不宣称重跑所有旧故障注入矩阵。R-053 的最后管理员反例在单元隔离图中验证；实机验证自我拒绝及有其他管理员时的成员移除，不破坏现有最后生产管理员。

| 需求 ID | 脚本/入口 | 环境与基线 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-042 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-043 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-044 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-045 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-046 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-047 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-048 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-049 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-050 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-051 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-052 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-053 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-054 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-055 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-056 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-057 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-058 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-059 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-060 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |
| R-061 | PORTAL-T-006/007 | finance / df726754b1ff | 2026-10-17 | 通过 |

原始日志、失败重试、截图及执行 JSON 均在上述主机专用目录。恢复点包含验收前的临时测试身份；如从该恢复点恢复，开放服务前必须按保留的 GUID/description 清理这些身份，不能把测试人员带入恢复后的正常使用。

### 13.4 最终封存与门禁

2026-10-17 最终封存位置为 finance 的 `/home/whl/anas-portal-admin-20261016`：

| 文件 | SHA-256 / 范围 |
| --- | --- |
| `portal-account-source-20261017.tar.gz` | `e29ddabbb26c89feffd825a61f6cba67d53a7a0918df47fa68dd8683a56a21a2`，177883 bytes；37 个运行输入逐文件与镜像构建输入一致，另含更新后的工具、测试和文档；配套计划单独保存以避免归档摘要自引用 |
| `portal-account-acceptance-20261017.tar.gz` | `6aaec9e2ca6b64e792f8b5b268a84137ef62ffbe74a92b0572c7c23fe908e810`，590909 bytes、49 项脱敏结果/截图/清单，包含失败重试；不含私有日志、配置或凭据 |
| `final-acceptance-summary.json` | 最终 69/22/8、14 个非目标容器保持、11 个测试对象已删除、5 个 IAM 身份停用和目录权限结果的汇总 |
| `implementation-plan.md` | 本文件的最终副本；不放入其自身摘要所引用的源码/证据归档 |

最终根盘可用 9786839040 bytes，约 9.1 GiB。所有恢复点与本地独有运行制品仍保留；未修改 ln 上的现有容器。无关工作树改动保留，本轮没有 git 提交或发布 registry 镜像。

最终门禁全部通过：需求归属与 61 个活动用例摘要、29 份需求索引、29 份计划索引、110 份文档状态、Module 文档生成检查及 VitePress 构建。构建仅有既有 chunk 大小提示。更新后的原生测试编译通过；行为代码的相关 race 回归与真实主机检查见上节。M4—M6 及新增 R-042—R-061 均完成，计划再次归档；没有把未执行的邮件后端 PoC 计入本轮。


## 14. 2026-10-17 提交前审查与修复

本次按用户要求审查、修复并准备仅含门户任务的 Git 提交。基线为 `f8056361` 加本轮门户代码、Samba 注册表、相关设计/研究和验收文档；不包含 Core 临时存储恢复、AI Agent、Incus 或其他用户工作树改动。当前分支为 master，直接提交到 master，不另引入无关分支历史。

### 14.1 发现与处理

| 问题 | 处理及需求关联 |
| --- | --- |
| 服务账号的 memberOf 不包含 AD 主组，配置工具可能接受主组具有域权限的旧账号 | 写入 ACL 前要求普通 user、无额外组、主组为 Domain Users，并检查该主组及服务账号的有效域权限；R-054/055。五项离线配置守卫测试通过，finance 三个已有账号只读检查通过，未修改目录 |
| 资料已保存，但后续读取失败会覆盖成功结果与请求编号 | 保留已确认的保存结果、请求编号，并在表单内提示刷新失败；R-044。本人及管理员两条界面路径反例通过 |
| 退出期间迟到的资料读取能重新设置前端旧身份 | 表单断开后不再更新全局本人资料；R-041/044。退出、延迟响应、再切语言的浏览器反例通过；没有声称旧服务器会话被重新激活 |
| 架构摘要仍宣称资料只读、扩展未实施，或未区分平台角色与受限委派 | 同步中英文当前模型、邮件未接入边界、配套计划链接和生成文档 |

修复前镜像构建归档内的静态资源运行同一浏览器反例为 0/3；修复后的生产静态资源配模拟 API 为 3/3。模拟 API 无真实账号、OIDC 或 LDAP 写入，不能替代 finance 新制品验收。首次浏览器执行被本机沙箱阻止启动；独立临时 Chrome 获准启动后，一次管理员表单选择器过宽失败，修正选择器后全部通过，保留失败日志。

### 14.2 验证与后续

- [x] 仅含本任务的临时源码树执行门户、门户 Hook、Samba Hook、appcatalog race 回归通过；BuiltinInventory 契约通过；portal_native 编译通过（未提供写入夹具，不称为实机执行）。
- [x] 五项 Python 配置守卫及三项生产 UI/模拟 API 浏览器回归通过；Node/Python 语法检查通过。没有增加依赖。
- [x] finance 只读检查三个服务账号，Domain Users 主组、额外组为零、有效域权限为零；无 LDAP Modify、无容器变更。
- [x] 候选源码树的用例摘要、需求归属、需求/计划索引、文档状态和 Module 文档生成检查通过。
- [x] 候选源码树 VitePress 构建及最终提交范围核对。31.93 秒完成，仅有既有 chunk 大小提示；54 个变更文件/重命名，只保留门户任务的共享索引段落。
- [x] R7已在finance复验R-041/044/054相关路径，结果见§15.4；原有目录对象与非目标容器对照通过。

§13 的 `df726754b1ff…` 及 69/22/8 结果为本次审查前的固定通过基线，继续保留。本轮新修复尚未替换该镜像，不套用旧验收结果；M4/M5 重新打开，待新制品实机复验后再归档。配置守卫没有重新发放任何 ACL。邮件后端和别名写入继续留待已约定 PoC。

审查原始结果和失败重试保存在 finance 的 `/home/whl/anas-portal-admin-20261016/review-20261017`，本机 `/tmp/anas-portal-review-20261017` 为工作副本。

| 归档 | SHA-256 / 范围 |
| --- | --- |
| `portal-review-source-20261017.tar.gz` | `5e43b889dbf10865bd7efa35ee2f238d1f69f0e162ccbf21b93b852a883916c1`，300335 bytes；叠加 `f8056361` 的门户任务覆盖包及逐文件清单，不包含本计划以免摘要自引用；运行制品仍为 §13 旧基线 |
| `portal-review-evidence-20261017.tar.gz` | `a599e939a4233d2b371d6ceb21313cedd4c8b0e210886b0f1edc1f87b0deaab9`，6829 bytes；旧/新 UI 反例、选择器失败、Go 结果、finance 只读结果、最终文档构建和检查摘要；不含真实凭据 |

任务源码树的用例与需求门禁为 61 个活动用例、26 个活动需求文档/772 项需求，另有 10 个归档主题；109 份文档状态通过。根级状态生成器只处理仓库作用域，门户两个私有索引另外调用相同状态库生成并核对：M4/M5 实施中，39/55 已完成。此前 55/55 属于 §13 的固定旧镜像，本次没有抹掉或重新声明该历史结果。

暂存区逐文件与隔离候选树相同；共享中英文索引逐段剔除 AI Agent 等无关变更。原始无关文件内容校验保持一致；Core 恢复补丁、Incus 和 AI Agent 实现都不进入本提交。本次不发布 registry 镜像、不推送远端，也不替换 finance 容器。


## 15. 2026-10-17 系统主邮箱、用户 OU 与门户建号

用户新增确认：门户管理员可创建人员，后台使用指定授权账号，管理和创建范围限定到 Samba 指定用户 OU 及子 OU；邮箱字段完全由系统生成，默认“用户名@邮箱后缀”，后缀可以独立变更。此前跨 OU 的 R-061 退役，新范围为 R-062。R-047 明确为禁止手工主邮箱输入；自动建号和邮箱维护由 MAIL 矩阵约束。所有历史制品结果保留。

邮箱跨 Module 的规则和实机结果归[目录主邮箱计划](../../../../../dev-docs/plans/archived/directory-primary-email.md)，M7 只登记门户自身授权、字段和创建结果。当前候选基线为 `0889af2e` 加本轮差异，尚未部署。

| 需求 ID | 脚本/入口 | 环境与基线 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-062 | 门户 OU 范围反例与目录适配器 | 本轮候选 | 2026-10-17 | 单元及适用原生检查通过；新制品完整浏览器待激活 |
| R-063 | 固定建号 API/LDAP 权限反例 | 本轮候选 | 2026-10-17 | 单元及适用原生检查通过；新制品完整浏览器待激活 |
| R-064 | 创建字段与地址规则 | 本轮候选 | 2026-10-17 | 单元及适用原生检查通过；新制品完整浏览器待激活 |
| R-065 | 近期认证/CSRF/角色/审计/未知结果 | 本轮候选 | 2026-10-17 | 单元及适用原生检查通过；新制品完整浏览器待激活 |
| R-066 | 临时密码/强改及脱敏检查 | 本轮候选 | 2026-10-17 | 单元及适用原生检查通过；新制品完整浏览器待激活 |
| R-067 | 重名/邮箱冲突与不覆盖 | 本轮候选 | 2026-10-17 | 单元及适用原生检查通过；新制品完整浏览器待激活 |

本轮 finance 尚未激活；完整浏览器验收被新快照空间/明确回退授权阻塞，见目录主邮箱计划。已完成人员管理原生23项、建号适配器最终14项、邮箱维护19项、Samba创建5项、LAM配置5项及本地Go/界面回归；这些结果不替代新部署的完整浏览器验收。旧活动部署与容器未替换。默认邮箱维护和创建绑定的最小授权已经用户明确确认，目录基线已保存。

另发现创建绑定拥有其新建用户的 DACL 控制权，原生 Owner Rights 实验未限制住该权限。真实 finance 提权测试被自动审批拦截，后用隔离假域安全验证；后续处理设计尚待确认，具体观察和边界见目录主邮箱计划 §6。该结果不与既有普通对象创建正例混为安全隔离通过。

完全断网的 owner.test 原生实验已确认：创建者在用户加入假域 Domain Admins 后仍能改 DACL；容器和数据卷已移除，真实 finance 提权未执行。新增“本人移交 owner 后再加域级组”方案因多阶段结果处理已依 AGENTS.md 请求确认，尚未实现；在快照及所有者方案确认前不激活建号候选或归档 M7。

本轮候选与证据最终封存于 finance 的 `/home/whl/anas-portal-admin-mail-20261017/`。`task-source-final.tar.gz` SHA-256 `1cff1c9609dcb5a07f944174e3b158e2ac18cb7766044b2363ffc529deceff86`，本地证据包 `local-evidence-final.tar.gz` SHA-256 `c571c338ef73aeb4fc3c65120e3ba02b42ee3ac859a5fe2f814174257275b2c8`，原生结果包 `finance-native-evidence-final.tar.gz` SHA-256 `d72c1e0a1058a406b4b1427d5600289a354592ef740ecb3fa0f919794aeb2092`；逐项范围、R4运行输入/镜像摘要与限制见配套邮箱计划 §7。最终计划副本单独保存，不把最新计划与先前归档中的历史副本混淆。32个既有容器完全未变；未激活、未提交。


### 15.1 已确认所有权处理与快照方案

2026-10-17 用户确认先腾出空间并创建完整快照；域级实验仅在完全隔离假域验证；采用本人域绑定先移交所有者到 Domain Admins、回读后再加域风险组的两步方案。新增 R-068 归 M7，实施和验证中；不再要求同范围重复确认。第一步失败或未知不执行第二步，第二步失败保留已移交的 owner；分别显示并审计结果，不自动重试。完整快照和新制品实机验收仍未完成。


### 15.2 2026-10-18 最终候选和原生验证

已实现所有者移交、仅 owner 的原生 SD Flags control、回读后加组、两步 API/审计/中英文页面结果和部分成功提示；失败或未知不继续，不回退新 owner。确认凭据额外绑定是否执行所有权步骤，预览后的组风险变化不能静默增加该步骤。完整门户 race 与6项生产静态资源/模拟API浏览器回归通过，配置守卫5项通过；反例夹具和查询参数选择器失败保留。没有新增 Go 依赖、IAM 源码补丁或长期域级服务凭据。移交消除默认 owner 的隐式控制权，不会清除其他显式 ACL 或已被授予的组权限，不能宣传成任意目录状态下的凭据隔离。

隔离 `owner.test` 已以8项生产适配器检查及7项目录回读通过：正常移交/升权、加组失败保留 owner、另一人密码拒绝、普通创建者无法升权、本人域重置和资料更新、重置后同一账号能绑定、移交后创建绑定不能改 DACL/重置；容器和匿名卷已删除。前一版测试二进制 `619c5001ca7bb9f6852489b5fc5db158019d8d33857fb39166e06b2d29c57c86` 与前一版 Samba 镜像固定在 `repository-proof-final/owner-isolated-results.json`。最终 R7 使用新二进制/镜像再次复验，另记结果，不套用此历史组合。

最终 R7 运行包 `candidate-runtime-owner-r7.tar.gz` SHA-256 `ed44c1c27b8d2897de6603ceb35a2fd6ab9f6f80abe147e9ac853c976de3c5f7`（283814 bytes、104项白名单输入），在 finance `/home/whl/anas-portal-owner-20261017/`。门户镜像 `e7ce89dc1f54e952e2c3edb25d04314b8771c34a07d10495de3b97ad2add8674`，二进制 `ada96b6268c3a183d1e7fc02bb92516fc41a0bb16b1bcf9c7a292ed2d5145ee8`；Samba `1d110032df763dd0ba7d1dbe233f4b4f40468322eb49f779677bbfa89a62333e`；Worker `97cc088b169c5e368be219fbb360cf3ca3172c8f706e4a2d4073c2e7edf2094d`。Go1.26.6原生构建加既有正式运行镜像；本轮未完整执行仓库正式 Dockerfile，也未发布 registry。BuildKit 在本地基础标签元数据查询阻塞，停止该任务构建后通过仅本次使用的 `DOCKER_BUILDKIT=0` 本地构建器成功；未改 Docker 全局配置。前轮镜像和归档继续保留。

finance 的所有新人员夹具均不再授予域角色；高级域操作的成功写入只在隔离假域执行，finance 页面验证本人密码/确认/ACL拒绝与普通管理正例。此分布依据用户“仅在隔离假域验证”的确认；没有删去域级成功判据，原生成功归 PORTAL-T-008，页面流程和真实 OIDC 归 PORTAL-T-007。待完整快照后再创建普通测试夹具，避免新恢复点包含测试人员。


R7 最终原生复验：`repository-proof-r7/owner-isolated-results.json` 15/15通过，测试二进制 SHA-256 `147e77c68f78b8bbf184d66458e2eb66c0d9dc36100c02bfce26c3543212cb19`，Samba 镜像为上述最终 `1d110032df76…`。首次同组合在归档 I/O 同时运行时，“own_domain_rights_reset_promoted_test_user”未通过，但新密码绑定检查通过；未记录当时归一化返回值，不能断言根因为超时或写入失败。失败原始 Go 日志保留于首个随机 `repository-proof-r7/owner-private-*/go.log`。旧假域已删除，归档扫描和构建结束后新建无信任假域重跑全部通过；没有重试旧域的未知密码操作。最终容器和匿名卷已移除。R-068 的正反原生路径及中英文流程/审计模拟回归已通过，新 finance 页面和部署仍待完整快照后执行。

R7 最终冻结部署 `20261017T161419Z-060068f7`（本地2026-10-18），受影响 Module 仍仅 Samba、门户；活动部署尚未切换。


### 15.3 新完整快照、R7激活与追加实机验证

新完整快照 `20261017T164752Z-8a725dd0` 包含 data/userdata及完整恢复镜像，经原生verify通过，5个历史恢复点均保留。冷启动时宿主3.3GiB内存出现换页和高I/O负载，原栈所有服务恢复健康后才激活；没有放宽空间/健康门槛。R7在2026-10-18 01:22（本地）通过原生apply，活动部署为 `20261017T161419Z-060068f7`，全部健康。目录原有12个对象受检字段激活前后完全相同，admin邮箱为admin@finance.hlong.wang。

最终隔离假域补充结果 `repository-proof-bootstrap/owner-isolated-results.json` 为21/21通过，其中Go适配器8项、目录回读和生产管理员初始化区块13项。使用同一最终R7镜像/二进制，补充驱动SHA256 `6aacb86c3f200a1090e35d55ddfa3192a2b9e9e673a39904e9ac0e353e5942c0`；无网络/端口/生产信任，容器及匿名卷已删除。R-068正反原生路径复验通过。SYSVOL/SMB不属于该LDAP实验范围，未运行完整文件服务初始化。

finance新夹具前缀`puac54f6fa2`，7人均无域角色，仅6人在指定人员OU；4个既有最小服务绑定复用，未再发放ACL。R7只读Go探针通过，严格TLS/错CA/错主机名拒绝，人员和系统组边界正确、LDAP写入0。独立HTTPS探针200/59ms。为减少finance内存负担，完整浏览器改在本机独立Chrome运行，通过仅监听127.0.0.1的SSH转发连接finance的真实10000端口。原生证书探针仍校验CA和名称；临时本机浏览器夹具仅含本轮随机普通人员密码，0700/0600保存，结束删除。浏览器脚本仅把专用夹具目录守卫适配为本机固定/tmp路径，业务步骤不变；原始/本机脚本SHA256分别为`48e70297061891abbf7b89e30d9938917d2cc8d31ccdd1f201ab4be68b4f94f9`/`eb9e9d4bebb0d752d490f9d761ce7329f6d9b4a090a1062a210281e283e1898a`。本轮未在finance创建域高权测试身份。浏览器完整结果及清理继续登记。


### 15.4 2026-10-18 最终验收与清理

R7真实浏览器80/80、只读原生TLS/范围探针、隔离原生21/21、本地race、界面模拟6项、配置守卫5项全部通过，M4/M5/M7完成。高级域成功写入依用户要求仅在断网假域验证，finance验证真实OIDC、个人凭据与原生ACL拒绝；没有给finance临时用户域权限。API、界面、确认和审计的所有权/成员分步反例通过；移交不消除自定义绑定已有的额外权限。

| 需求 ID | 脚本/入口 | 环境与基线 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-041 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-042 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-043 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-044 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-045 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-046 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-047 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-048 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-049 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-050 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-051 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-052 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-053 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-054 | PORTAL-T-006/007；配置守卫与既有服务绑定/目录对照 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-055 | PORTAL-T-006/007/008；域级成功仅隔离假域 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-056 | PORTAL-T-006/007/008；域级成功仅隔离假域 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-057 | PORTAL-T-006/007/008；域级成功仅隔离假域 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-058 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-059 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-060 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-062 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-063 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-064 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-065 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-066 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |
| R-067 | PORTAL-T-006/007 | R7／finance与断网owner.test | 2026-10-18 | 通过 |

浏览器Chrome155.0.8059.80，真实近期认证等待140秒，联合退出后同一IAM cookie不能静默登录。中英文桌面/390px布局通过；本机Chrome支持中文，实际查看中文登录和用户管理窄屏截图正常，不套用此前缺少CJK字体的Linux截图结论。导航DOMContentLoaded1555ms、响应532ms；这些是SSH隧道下本机浏览器数据，不能当作用户公网线路性能承诺。finance原生HTTPS200/59ms；全栈冷启动的换页/高负载另见邮箱计划，不修改宿主容量或其他服务。

本轮13个目录测试对象全部删除，8个IAM查询结果在周期同步后均停用或不存在，无手工IAM缓存写入；4个授权最小服务账号保留。人类测试密码文件已删除，临时本机SSH转发关闭。原有12个目录对象受检字段完全不变，28个非目标容器ID/镜像/配置/挂载/状态不变，finance18个容器最终正常。根盘可用10118901760 bytes，完整新快照及5个旧恢复点保留。

浏览器归档 `/home/whl/anas-portal-admin-owner-20261018/browser-acceptance-r7.tar.gz` SHA256 `ee86dc81b284087a8199370ea3c43526d1770fee127b96d4f21f87c7dc2ba7c9`（411891 bytes）。R7输入和制品摘要见§15.2；最终原生/健康/清理证据在finance两个本轮专用目录。门禁：需求覆盖27文档/804项、7主题/67活动用例、112份文档状态、Module生成检查及VitePress14.09秒构建通过，只有既有chunk大小提示。最终索引/归档移动后另核对。未完整执行正式Dockerfile或发布registry，未部署邮件后端；本轮未Git提交。

R-068按矩阵的“单元 + 原生隔离域 + 界面”记录：所有权单元、最终原生21/21及界面6项均通过，原生基线/正反例见§15.2—15.4；不把该原生记录列作矩阵未声明的e2e。首轮完成态门禁因把R-068放入e2e表而拒绝，已只修正记录位置，不改变判据或结果；失败日志保留。


### 15.5 最终封存与归档

本主题61/61与目录邮箱7/7完成后归档。归档前完成态门禁27文档/804项通过；归档后25份活动文档/736项，其余68项转为归档归属检查，67个活动用例继续通过。需求/计划状态索引、Module生成和112份文档状态检查通过；归档链接后的VitePress构建14.24秒通过。没有用归档跳过完成态e2e记录检查。

最终源码、门禁和finance50个脱敏结果/截图包保存在 `/home/whl/anas-portal-owner-20261017`，完整文件名/摘要及范围见[目录主邮箱计划](../../../../../dev-docs/plans/archived/directory-primary-email.md)§10。任务源码SHA256 `f4ab21703f595b496a4efb9b8fe3d189108d1bf485b74332d2f9d94f790db792`；本地证据 `da31bfd5a7c7a0411e7266768efcd2c109397eb5ef93e2ed6138406a0d07d9e1`；finance验收 `2f0310a0d9a57e3a4923195ac06ef4b5c06802bd1e1db4dba73d8cf00a19e31c`。原始私有日志和失败重试保留，最终计划副本另存，无自引用归档。本轮未Git提交，邮件后端/个人别名PoC仍未实施，不属于本轮验收。


## 16. 2026-10-18 页内操作与分组清单

用户确认普通分组添加直接执行；系统分组添加须页内确认及本人域验证。普通组移除、建号、管理员资料修改、重置/解锁显示操作摘要，备注选填；域风险组即使属于 ANAS 仍保留本人域权限流程。所有高级组实际置于页底的默认折叠区域。元数据编译与 Samba 同源建组归根计划 [分组共享清单](../../../../../dev-docs/plans/archived/identity-group-catalog.md)，不重复归属。

复杂度中等：复用 Module 声明、现有计算/渲染流程和一个只读 JSON 文件；不增加服务、数据库或权限缓存。用户已确认该方案。既有目录 ACL、GUID/SID/anchor、近期认证、一次性域确认和审计继续生效。`reason` 是内部协议字段名，语义为可选备注，最大 512 字节；不将密码自动填入备注。

最终基线：部署 `20261018T081820Z-80a8a6b3`，门户镜像 `bfaf33c667b9…`，完整摘要见下文。R-069—072及适用回归已在最终镜像分别通过；finance不进行域管理员测试主体提权，原生高权正反例按确认范围在无生产信任的隔离假域验证。历史R7结果仍在 §15。

| 需求 ID | 脚本/入口 | 环境与基线 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| PORTAL-R-069 | PORTAL-T-006/007 | 最终部署 / finance与断网owner.test | 2026-10-18 | 通过：全部确认/凭据页内展示，摘要、取消/Esc均验证且无浏览器原生弹框 |
| PORTAL-R-070 | PORTAL-T-006/007；备注单元 | 最终部署 / finance与断网owner.test | 2026-10-18 | 通过：空备注可执行，有备注随对应动作持久审计，密码未进入审计 |
| PORTAL-R-071 | PORTAL-T-006/007/008 | 最终部署 / finance与断网owner.test | 2026-10-18 | 通过：普通组直接添加，系统组页内确认及本人域凭据/ACL拒绝，域级成功见断网假域原生记录 |
| PORTAL-R-072 | PORTAL-T-006/007 | 最终部署 / finance与断网owner.test | 2026-10-18 | 通过：高级实际组行在页底默认折叠，英/中/不支持语言回退及390px单行标签通过 |


M8 追加进度：本机 Chrome 9/9 界面模拟通过；页内摘要使用动作/对象，备注为空可确认，编辑备注后重新绑定目标 GUID 版本，目标被替换时拒绝继续。断网假域26/26含原生分组清单验证通过。候选源码/二进制/镜像和清理证据见根[分组共享清单计划](../../../../../dev-docs/plans/archived/identity-group-catalog.md)。新完整快照进行中；IPv6前缀变化导致全栈环境摘要变化，等待用户确认更新范围，未激活候选、不声称 finance e2e 通过。


本轮清理/恢复收口：新完整快照 `20261018T040309Z-5edbad65` 包含data/userdata及恢复镜像，独立verify通过，6旧恢复点保留；finance18容器最终正常，原32容器ID/镜像/配置/挂载不变。当前仍为R7，M8的新制品finance验收未执行，等待用户确认全栈更新范围。根盘末次可用9402564608 bytes，证据位于 `/home/whl/anas-portal-groups-20261018/evidence`。详细基线、hash、冷启动超时和下一步见根分组清单计划；没有将本轮隔离验证或历史R7结果替代M8 e2e。


2026-10-18后续继续：全栈范围已获用户确认，操作CLI含既有恢复终态保护且两地构建hash一致；候选改用不可变镜像ID，冻结部署为 `20261018T051557Z-e3bf7b8f`。Core因PostgreSQL摘要变化要求新的原生过渡恢复点，不能跳过快照；空间尚不足，两个旧传输缓存归档被自动审批拒绝后已询问专项授权，未传输、未删除，finance仍为R7。六份恢复镜像归档复核不变，M8 e2e继续待候选激活，不能将历史R7或本机9项测试代替本轮实机结果。详细进度归根分组清单计划。

用户随后明确授权上述两份具体缓存跨主机归档、校验和本地清理，专项授权等待已解除；正在按根计划执行。仅本次传输指定 IPv4 以避开 finance 到 ln 不可达的 IPv6 路径，不修改主机网络配置。部署和 M8 验收结果仍须单独记录。

本轮候选 `c7d08ab8c9f8…` 真实浏览器88/88通过。人工查看首次截图发现取图早于分组加载，已补充等待并只读补拍3/3；列表完整出现后确认390px下成员状态及操作按钮被挤成竖排，属于现有R-042的实际回归，重开M4并保留R7历史通过基线。现改为手机分组介绍占一行、状态/操作在下一行，补充按实际文字行数检查；待只更新门户及最终完整复验，不把88项旧候选结果当成修复版已验收。

修复版已原生构建并激活：部署`20261018T081820Z-80a8a6b3`，镜像`bfaf33c667b96fc0d7eaeb60100a64da088fefc0a40a6bfa93f1e46f3ab3faf0`，二进制`0fe19cac9f5916f5df7dc5305763826af801b25e2d53cb9e0b4f60b8d73b74df`，原生/交叉一致；仅门户变化，8个固定恢复点保留。旧手机排版被新增行数断言实际拒绝，修复后现有界面模拟9/9通过；全新普通夹具`puaf750baaa`的最终完整89项仍运行中。严格TLS、范围及主邮箱/IAM前置通过；旧13对象及8个IAM身份清理完成。源包、继承清单、磁盘和实机保全详情及摘要见根分组清单计划，不据此前结果提前归档。

最终镜像真实浏览器89/89通过（Chrome155.0.8059.80，2026-10-18 16:34—16:46 Asia/Singapore），含实际认证过期、重新登录后旧确认拒绝、个人密码与原生ACL拒绝、普通组无确认、系统组页内确认/取消/Esc、空/有备注审计、即时主邮箱、资料、保护规则和IAM联合退出。390px分组状态及操作文字实际行数均为1，中英文1440px及中文390px无页面水平溢出；人工查看最终桌面/手机截图，7个分组介绍完整显示，高级区域默认折叠且在页底，R-042回归已修复，M4恢复完成。最终原版/本机路径守卫适配脚本SHA256分别为`ea6386c7c855968b13c628cd3f30e4d4d5c121cb42006b69ddada3a4644ba8c7`/`2c447e16a8fc84ac4a77a406ca7e1832f060d48e3f3b9a6f7c24133cb8ae1cbb`。未授予finance测试人员域权限，域级成功仍按确认范围由断网假域原生26项证明；手机增量未改25个生产Go文件。

脱敏浏览器包`final-browser-acceptance.tar.gz`为740169 bytes，SHA256 `49b34cc3b1aa98d84e7fc280f799bd585b0a24d70a675c31e347cbd09391943f`，正在回存finance；最终JSON和稳定清理引用已逐项SHA保存。Root执行目录仍为`/home/whl/anas-portal-groups-20261018`，新人员验收目录`/home/whl/anas-portal-admin-groups-mobile-20261018`。导航DOMContentLoaded3426ms、响应732ms是本机Chrome经SSH转发的测量，不当作公网线路承诺。最终测试夹具清理、全栈/原有数据及无关容器保全尚在收口，M8不提前归档。


### 16.1 最终收口

最终89项JSON、6张截图和实际本机脚本已归档finance并独立核对：`/home/whl/anas-portal-groups-20261018/final-browser-acceptance.tar.gz`，740169 bytes，SHA256 `49b34cc3b1aa98d84e7fc280f799bd585b0a24d70a675c31e347cbd09391943f`。本轮原生构建、激活、清理和保全的21个脱敏结果在同目录 `final-operations-evidence.tar.gz`，7508 bytes，SHA256 `415065fdce03a263c91346d1750505250407d4d3a9ce74f048061984532feb39`，逐文件及tar回读校验通过。最终计划副本和本机门禁另存，原始失败/重试不覆盖。

最终夹具13个目录对象均已删除，8个IAM身份经原生同步全部停用或不存在，测试密码和临时SSH转发已移除；4个授权最小写绑定保留。再次回读45个原有组的成员/身份/安全描述符、12个人员受检字段完全一致；受管介绍7项与共享清单一致，内置组属性不变。finance18容器健康；其他引擎及主引擎共29个非目标容器ID、镜像、Config/HostConfig、挂载、状态、启动时间及重启次数不变；最后门户增量之外17个finance容器也未重建或重启。挂载列表按Destination排序比较，仅消除Docker返回顺序差异，失败诊断保留。

原7点和本轮新过渡点均完整且固定，合计8恢复点；含data/userdata的完整点继续保留。根盘末次可用9411588096 bytes。M4回归及M8所有适用标准通过，本计划65/65完成。稳定行为已沉淀至门户需求、设计及中英文技术文档，无新增Casdoor源码补丁、服务或依赖；构建使用已缓存正式运行时，未完整执行正式Dockerfile或发布registry。未实施邮件后端/个人别名PoC，本轮未Git提交；文档门禁及完成态归档随后收口。


最终文档收口：完成态归档前需求覆盖27份/807项及e2e一致性通过；两个计划移入archived后25份活动文档/736项，其余71项按归档归属校验，8主题/70活动用例继续通过。114份文档状态、31份需求/31份计划状态索引、Module源文档生成检查和diff空白检查通过；归档链接后的VitePress v0.1.1构建14.30秒通过，仅既有chunk大小提示。门户README已与技术文档一致说明实际所有权移交行为，属验收后的文档收口，未再改变部署运行输入。本机门禁包保存在finance `/home/whl/anas-portal-groups-20261018/final-local-validation.tar.gz`（4140 bytes，SHA256 `a68e7100568a093e8940e7629c68139e050adf97179ff3d80ca95894735a009f`），含12项日志/摘要，finance回读和成员数检查通过；最终计划副本另存同目录 `final-plans/`，不自引用归档。本轮实现、部署、适用验收和清理均已完成，无剩余阻塞，未Git提交。


## 17. 2026-10-18 移除理由与下拉框对齐

用户明确要求删除finance备份到ln的文件、去掉所有操作理由、修复下拉框对齐。沿用现有服务及授权模型，复杂度低：删除页面和写请求中的reason字段，旧审计记录仍可读取，保留对象/动作/规则/结果审计、页内摘要和本人域凭据确认；统一原生select的内边距和箭头，保留原生键盘操作。R-070按用户新选择退役，R-073代替其当前行为；R-042重新验收下拉框，不扩大目录授权或邮件范围。

ln已删除两个专用目录 `/data/anas-portal-image-archive-20261018` 与 `/data/anas-portal-groups-20261018`，含旧镜像归档、两份传输缓存和中继残片，共14文件，删除前分配4899041280 bytes（约4.56GiB）。删除前核对无容器挂载、进程fd/cwd/root/exe或挂载命名空间引用；43个ln容器的ID/镜像/配置/挂载/状态/启动时间/重启次数前后一致。finance的8个本机恢复点及验收证据不属于删除范围，未删除。ln原有Incus迁移数据/日志和独立门户测试目录由ln自身产生，并非finance备份；不因目录名包含ANAS就删除。上述ln归档历史位置现在已失效，不能再作为可用备份引用；既有finance证据保留。

| 需求 ID | 脚本/入口 | 环境与基线 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| PORTAL-R-042 | PORTAL-T-007；Chrome截图与原生下拉专项 | 3937镜像 / finance / 部署20261018T115342Z-1d5c0f36 | 2026-10-18 | 通过：最终镜像英/中/390px截图及下拉专项4/4，无CSP错误，键盘可操作 |
| PORTAL-R-073 | PORTAL-T-007/009；Go/race与界面9项 | 3937镜像 / finance / 部署20261018T115342Z-1d5c0f36 | 2026-10-18 | 通过：91项实机断言含页面无理由、新建拒绝旧字段及审计无新理由；原确认与授权保持 |

原始清理记录在本机 `/tmp/anas-portal-polish-20261018/ln-cleanup.json`，仅此本机副本尚不算共享证据；构建和实机结果后续同节补记，不套用 §16旧镜像结论。本轮未请求Git提交。


本轮运行源码沿用 §16 的已接受基线，仅覆盖门户账号请求、页面与同源箭头静态资源。增量包 `portal-polish-source.tar.gz` 为42279 bytes，SHA256 `040acf83ccd3c381da5d1dfca67708febe4d18b52f5b7343f1621dda8d3d31a3`；静态资源及验证源码包 `portal-polish-static.tar.gz` 为23835 bytes，SHA256 `4e1a7f1d23f15a8206cee2fd893eb89dd3856680b06839eac431a171f78d57e9`。finance Go1.26.6原生与独立Linux交叉构建一致，最终门户二进制 SHA256 `a85950a23a7867ddcd8dead94e79ac6f73f6df8bc0885eb2ace9ee7779fc4c7c`，镜像 `3937fcb4c2fa8693a3d6c5408177ee424238c000696b7efe73b6363245ea5269`。部署 `20261018T115342Z-1d5c0f36` 已以原生默认策略apply成功，只有门户变化，PostgreSQL摘要和8个固定恢复点不变。初版内嵌图片会受现有img-src限制，未激活该候选；改为同源SVG，不放宽CSP。仍复用缓存正式运行时，未完整执行正式Dockerfile或发布registry，无新增Casdoor补丁。

本机门户完整Go/race及界面9/9通过。最终制品的finance只读原生探针通过：严格LDAPS、错误CA及错误主机名拒绝、人员/服务主体和系统组边界，写入0；独立HTTPS证书及名称验证200/111ms。实际Chrome下拉框专项4/4，英/中/390px及原生键盘切换通过，CSP错误0，最终中文下拉截图已人工查看。上述测量不作为公网速度承诺。新产生的可再生Go缓存已在工具链退出后清理，SDK、模块缓存及证据保留。

首次完整浏览器在第65项改参反例停止：原脚本仍期待旧reason改参的403，移除该字段后改用expected_direct=true，却会先触发当前operationTarget的成员冲突检查；首轮报告未记录实际HTTP status，不能把调用路径推断当作实测409。前64项通过，失败JSON及实际脚本保留在finance `/home/whl/anas-portal-admin-polish-20261018/`，生产逻辑未为此修改。反例现改为另一名处于同一人员OU的普通测试用户，并要求403且error为confirmation_required，直接证明一次确认不能绑定到另一对象；Go单元继续覆盖其他参数变化。首轮13个对象已按GUID删除，IAM停用仍待观察；全新前缀 `pua0cf5842d` 无域权限、无新增ACL，准备以同一最终镜像重新完整验收，M4/M9保持实施中。

### 17.1 最终本轮交付与边界

同一最终镜像完成91项真实Chrome通过断言（包括本轮R-042/R-073、改参403 confirmation_required、真实认证过期、系统组确认取消/关闭、权限拒绝、资料/建号/密码、审计及窄屏）。完整脚本最后一次转到IAM登录页时触及30秒导航上限，未执行最后一项，原始report.status仍为failed，不能表述成完整92/92通过。另以同一镜像、新独立上下文补验R-041四项全部通过：原生get-account先证实Cookie已认证，经门户页面退出后本地401、相同Cookie的原生API变为error/未认证，最后再访问登录入口显示密码表单且不能静默登录（16877ms）。正式脚本仅将最后导航上限调至120秒，未重试密码或改动目录。适用功能判据由这些同一制品的结果合并覆盖，两个失败过程均保留，不改写原始通过数。

最终完整流程原始源码SHA256 `b7ac3954f2cc8f9f8ae57723d2d6bc7fde37d9a0b791828a332dc2ef012b27d3`，仅本机夹具守卫适配后为 `6105cc09da2daf36da2f61f930f51fe162b667ff14251c90ac6f430737383d2c`；Chrome155.0.8059.80。根部署和Samba未再变更。严格公网HTTPS入口200/5.586秒、finance本机200/210ms是不同路径测量，不能由此确定最后导航超时原因或承诺公网速度；SSH转发仍在、18个服务健康，未因超时调整服务/CSP或新增IAM补丁。

两轮各13个目录测试对象已删除，各8个IAM身份经原生周期同步均停用或不存在；随机测试密码、临时回环转发已清理，4个已授权最小服务绑定保留。18个finance容器正常；最后门户之外17个finance容器及主引擎14个、其他引擎15个无关容器ID/镜像/配置/挂载/状态/启动时间/重启次数不变。8个恢复点完整且固定，末次根盘可用9306222592 bytes。ln两处归档14文件已经删除，43个ln容器不变，没有再向ln备份。

目录补查：45个旧组的身份、成员、类型及完整安全描述符保持，7项介绍与清单一致；本轮真正操作前后67个对象的GUID/安全描述符/锚点基线语义相同。沿用更早任务的12个人员属性基线比较时，唯一差异是admin的lockoutTime从旧非零值变为0，来源尚未确认；该旧字段不在67对象的四属性基线中，不能据后者声称已证明锁定值本轮未变，也没有写回它来迁就旧基线。其余受检人员字段一致。原失败及诊断保留，不能声称12个人员所有受检字段完全不变。

完整浏览器、截图、实际脚本、首轮反例失败及原生登出补验证据已保存在finance `/home/whl/anas-portal-polish-20261018/browser-and-ui-evidence.tar.gz`（824841 bytes，SHA256 `3c13a414a9620b7d039e12d974c3baf168df955056fcf172f4b5be16a149472e`），finance回读验证完整归档及91+4结果通过。构建、目录及清理原始回执在同目录evidence，最终计划和门禁另存；临时本机副本不作为唯一共享证据。

工作区并行边界：本轮验收期间另一个已授权任务新增§18的R-074管理员角色确认，并修改administration.go、directory_admin.go、ui/app.js及相关测试/需求。本轮保留其代码，不将当前共享工作树或新角色确认功能标记为已验收，也不部署该未经本轮实机验证的版本。共享目录一度因该并行改动出现测试摘要失效；本轮隔离源码由已封存输入重建，与部署二进制逐字节一致，用于本轮相关门禁。M4/M9完成仅绑定3937基线，M10仍实施中，整体计划不归档。本轮未Git提交。


最终门禁：当前工作区的需求覆盖26文档/802项、8主题/70活动用例、114份文档状态、31份需求/31份计划索引、Module生成及diff空白检查通过；VitePress v0.1.1构建15.18秒通过，仅既有chunk提示。该结构检查不代表R-074已通过实机验收。另将封存输入重建为临时冻结副本，排除后来新增的权限代码，重建二进制SHA256与已部署a85950a2…完全一致，门户Go/race再次通过（10.341秒），冻结用例目录校验通过。冻结副本初检因复制未保留脚本执行位及混入并行R-074元数据失败，随后只在临时副本恢复执行位/本轮输入，未回滚共享工作区。

finance同目录 `operations-and-cleanup-evidence.tar.gz`（4942 bytes，SHA256 `899fb66dbceb29559b335b07dc49f6be02e03308e2087b70316af1d04d0b1b90`）含19个结果及逐文件回读清单；`local-validation-evidence.tar.gz`（18895 bytes，SHA256 `84145aaf9a40aedbbfd4fc8684fee832001b5bc0c2730d713e06a2ca33053dbf`）含17项本机门禁/冻结源码清单和适用范围，finance回读校验通过。最终计划副本另存 `final-plans/`，不把暂存副本当作唯一证据。M4/M9本轮交付已收口；M10的原生/实机验收由并行任务继续，整体计划保持实施中，不归档、不Git提交。

## 18. 2026-10-18 管理员角色组成员变更收紧

来源：[门户与控制台联动评审](../../../../../dev-docs/reviews/2026-10-18-user-portal-ux-and-console-linkage.md) P1。原实现把 `Admins`（`platform_admin`，启用 anasd `trusted_proxy` 时即控制台 owner）当作普通受管组：近期认证窗口内一次点击即可加人，没有页内确认和本人密码。用户 2026-10-18 选择“先收紧，再拆组”，本节只做第一步；独立宿主管理员组属于后续设计，迁移策略待另行确认。

实现：成员动作的目标组带 `AdminRole`（含嵌套继承该角色的组）时，沿用域级的 prepare/execute 一次性确认（绑定会话、操作者、目标、组和完整参数，120 秒、单次），执行时用操作者本人 LDAPS 绑定验证密码并回读身份，然后仍由 `svc_portal_group` 写成员；不要求 `domain_administration_enabled`，不使用操作者 AD 权限。审计规则为 `platform_admin_confirmed`，不含密码。分组列表新增 `requires_password`；页面弹出“确认管理员角色变更”对话框并说明宿主级影响。复杂度低：复用既有确认存储、限流和绑定校验，未增服务、依赖或配置。

| 需求 ID | 脚本/入口 | 环境与基线 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| PORTAL-R-074 | `TestAdminRoleMembershipNeedsConfirmationAndPersonalPassword`；PORTAL-T-009 双语界面；PORTAL-T-007 | 本机；finance 部署 `20261018T162135Z-785c20e7` | 2026-10-19 | 通过：单元、界面模拟 11/11；实机 10 项（无本人密码要求标记、页内确认与取消不写入、无确认拒绝、他人密码拒绝、本人密码加入与移出、三次一次性确认） |
| PORTAL-R-071 | PORTAL-T-007/009 | 同上 | 2026-10-19 | 通过：普通受管组仍直接加入，系统组页内确认与本人域凭据/ACL 拒绝 |
| T-007 覆盖的其余 41 个门户需求 | PORTAL-T-007 | 同上 | 2026-10-19 | 通过：同一完整运行 105/105，含建号、改密、认证过期、退出联动及 1440/390 页面检查 |

### 18.1 finance 实机验收（2026-10-19）

制品：在已部署源码（`0889af2e` 加 §17 任务文件）上只覆盖本节 7 个文件，overlay 包 SHA256 `2ac727f41d200262b65947901e4ac1c636476d9699e104a3f2bbb3323483dd36`。finance Go1.26.6 原生构建与本机无 VCS 交叉构建逐字节一致，门户二进制 `9606cf61a4a1ebf0f7c76b650b6cae272fc95386cc0e010543f6d3e8f9317440`；同法复现了旧镜像二进制 `a85950a2…`，证明差异只来自本次改动。镜像 `sha256:def7274908a8a80d4ba7c65e607a7243c6d7b8bf69301e16393480974e831d84`，以 `3937fcb4…` 为底只替换二进制。原生 lock/render/apply 激活部署 `20261018T162135Z-785c20e7`（上一部署 `20261018T115342Z-1d5c0f36`），只重建门户；其余 31 个容器的 ID、镜像、启动时间和重启次数不变，8 个恢复点不变，未发布 registry。

结果：只读原生探针通过（严格 LDAPS、错 CA/错主机名拒绝、角色与系统组边界、服务主体排除、写入 0）；独立 HTTPS 严格证书探针 200。最终完整浏览器 PORTAL-T-007 **105/105 通过**（Chrome 155.0.8059.80，2026-10-19 02:05—02:14 Asia/Singapore，夹具 `pua9457b93b`），R-074 的 10 项全部通过，退出联动通过；人工查看 390px 用户管理截图，管理员角色组位于“管理角色”，状态和按钮单行显示。

过程与重试（失败均已保留，不覆盖）：

1. 原浏览器镜像 `anas-immich-browser:c8e14a69` 已从测试引擎消失；测试引擎处在隔离网络，无法直接 apt。改为在生产引擎上以同版本 `anas-immich:3.2.4`（`d72f129694df`）重建，同一 apt 包列表，流式导入测试引擎，生成 `anas-portal-r074-browser:20261018`（`sha256:912a1ba1fd12e3c7c942e1b647096a78266e54e2e4f95d74ddeb498407ef153f`）；生产引擎的临时镜像已删除，容器不变。该镜像保留在测试引擎供后续使用。
2. 第一轮在第 35 项后停止：运行脚本的私有目录对浏览器容器只读，写不了建号清理引用。新增单文件可写挂载，未改业务步骤。
3. 第二、三轮在 finance 内浏览器分别遇到登录页 45 秒超时、普通用户枚举请求出错。当时主机 swap 3.7 GiB、iowait 约 44%（含浏览器镜像导入的 I/O）；该项前两轮均通过，且普通夹具不在 Admins、部署没有委派规则，代码上只能返回 0 条或报错，归为负载抖动。此后按 §15.3 先例改为本机 Chrome 经 SSH 转发连接 finance 真实 10000 端口，仅把脚本目录守卫改为本机临时路径。
4. 第三、四轮 R-074 的“移出”被拒：审计显示 `platform_admin_confirmed` 拒绝。原因是建号（`create.go`）与 R-074 都消耗同一操作者的密码限流桶（5 分钟 5 次），第 6 次被正确限流。验收脚本改为由未用过限流桶的 `other` 管理员执行 R-074 密码操作；后续“移出/恢复另一位管理员”的步骤改为一次性确认加本人密码（R-074 实施后的必然调整）。
5. 第五、六轮“新建账号临时密码完成改密”失败；加入状态码后定位为 `403 csrf_rejected`：页面自身的 `/api/bootstrap` 与测试辅助的 `/api/bootstrap` 并发，各建一个匿名会话，令牌与 Cookie 错配。属于验收脚本竞态，改为先等页面初始化完成再调用；门户代码未改。
6. 第七轮全量通过。

清理：七组夹具（每组 12 个目录对象）及四个建号用户均按 GUID/标记删除，剩余 0；全部 IAM 身份停用或不存在；4 个最小写绑定保留；未授予任何域权限。第三、四轮失败时 normal 夹具曾停留在 Admins 组，均随夹具删除立即撤销。67 个目录对象的 GUID、安全描述符和身份锚点与第一轮前完全一致。夹具文件中的测试密码已清除，本机临时副本已删除，SSH 转发已关闭。

证据：finance `/home/whl/anas-portal-r074-20261018/`（构建、激活、容器与恢复点基线、只读探针），各轮夹具目录 `/home/whl/anas-portal-admin-r074{,b..g}-20261018/`；本机浏览器四轮报告、截图、实际脚本和日志归档为 `r074-browser-evidence.tar.gz`（1014142 bytes，SHA256 `d97a52c934d699204a604846d78350f73b0ad45e3caf2fd4bb9889f39c6c3122`），已回存同一 finance 目录。原始日志不入库。

## 19. 2026-10-19 退出主页、分级验证与跨应用退出检查

用户确认：普通用户操作和 ANAS 管理员角色组使用 5 分钟内真实认证，系统组、Unix Admins 等操作系统权限组逐次输入本人域密码。受保护账号的资料修改、重置和解锁也继续逐次验证，保持原生 AD 权限边界；不缓存密码，不扩大专用服务绑定权限。管理员角色组仍有一次性操作摘要确认；本人修改密码仍提供原密码。

本轮仅修改门户验证与页面，不新增 Casdoor 补丁。退出页增加中英文返回主页按钮，IAM 失败保留重试。跨应用检查针对 finance 当前启用的门户、Nextcloud、Immich，分别记录从门户和 Nextcloud 退出后，各应用及 IAM 的真实会话状态；不能用 IAM Cookie 已清除推断所有应用、全部设备或旧 token 已撤销。根双向登出矩阵的其他 Provider/Consumer 不冒充完成。

- [x] 门户 Go/race（11.996 秒）；299/301 秒边界、同一近期会话连续普通操作、一次性角色确认及系统组逐次凭据约束通过。
- [x] Chrome 独立模拟 15/15；普通组直接加入、管理员角色确认无密码、系统组仍输入密码、中英文及 IAM 成功/失败后的主页入口通过。
- [x] finance 最终制品部署与本轮功能定向复验；完整账号回归两轮失败保留，不声称全绿。
- [x] 从门户和 Nextcloud 原生 SLS 退出后检查其余应用及 IAM 原会话，见 §19.2—19.3。
- [x] 清理两轮专用夹具并保全容器、目录、恢复点；持久证据见本节末尾。

过程：沙箱禁止本机回环监听/Chrome 启动，放行后测试运行。首次 UI 修改把缺失 admin_role 的布尔表达式变成 undefined，触发默认确认，回归测试发现后修复为 Boolean；随后两项系统组测试的密码输入误改，恢复后 13/13 通过。原始失败日志保留。

| 需求 ID | 脚本 | 环境与基线 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| PORTAL-R-075 | PORTAL-T-006/009；T-007 部分与定向原生脚本 | finance 4b5bc591，二进制 7f56f63f；本机 race/UI | 2026-10-19 | 分级操作定向通过；完整 T-007 未全绿，M11 暂不收口 |
| PORTAL-R-076 | PORTAL-T-009；定向原生脚本 | 同上 | 2026-10-19 | 通过：双语成功/失败模拟，实机退出及点击返回匿名主页 |

配置复核：`recent_window_seconds` 曾允许最大 900 秒，与本轮 5 分钟要求不一致。Module 参数与进程配置均将上限收紧为 300 秒（可缩短至 5 秒）；finance 仍使用 300 秒。新制品重建后记录最终验收，不把旧 e4d154 的测试自动归给新制品。

### 19.1 首次实机过程（失败历史保留）

已激活 `20261018T190226Z-02319cc9`，镜像 `e4d1544092da…`、二进制 `848d90e2…`（原生/交叉逐字节一致）。其余 31 个同引擎容器 ID/镜像/配置/启动时间/重启次数不变，挂载按 Destination 排序后相同；初次直接比较 Mounts 数组因 Docker 返回顺序变化失败，原始记录保留。8 个恢复点保留。

本轮第一份完整账号回归已执行 75 项，前 74 项通过，第 75 项“无确认的域写入应返回 confirmation_required”不符合预期；该断言未记录实际状态码，原因尚未证实，不能把它宣布为负载问题或通过。后续脚本补充实际 HTTP 状态和 error 字段。新用例涉及 5 分钟内复用、真实认证过期、一次性管理员角色确认、普通组直接加入等；尚不以此完成 R-075/076。

同时出现主机负载 29.85—35.43、约 4 GiB swap，Samba、anchor、directory watch 健康失败。严格 LDAPS 握手独立复现超时；Samba 健康两次超过 10 秒，Casdoor directory watch 报 OIDC logout delivery failed。Casdoor 到门户的原证书 HTTPS 只读探针 404/455ms（误用了不存在的 /healthz，只能证明 HTTPS 连通，不能作为健康通过）。TCP 观察为 636 已建立 4 条，未见连接堆积；没有证据归因于单一进程。未擅自更改 DNS、目录 ACL、任何真实用户或其他容器。

跨应用准备的首轮漏处理 Casdoor 已有账号选择页，第二轮漏处理 Immich 自动跳 IAM；第三轮 Nextcloud 已跳 Dashboard 但等待 DOMContentLoaded 超时。所有原始失败与进度保留，未声称完成退出测试。已改为单个浏览器、原生 Immich PKCE/Cookie、Nextcloud 原生退出端点与实际受保护 API，第四轮执行中。该方式不向报告写 token，不凭密码表单存在推断 IAM 已退出（有效 IAM Cookie 时也可能显示密码表单和可继续账号）。

只读 Go 探针首次误用容器内 DNS 地址，随后 loopback 握手失败；从 Samba 配置的宿主 IP 保留原 CA/主机名进行独立 TLS 握手仍超时，记录为目录不可用，未通过。原始错误和修正脚本均保留。

最终额外收紧 300 秒上限的二进制 `7f56f63f…` 已本机构建，Go/race 11.031 秒通过；300/301/900 秒配置边界与两项 Runner schema/inventory 门禁通过。finance 原生二进制一致，镜像暂存中；尚未将旧镜像结果算给该制品。待确认是否仅重启测试 Samba DC 以恢复目录后继续验收。

2026-10-19 用户明确授权仅重启 `anas_finance_samba_dc`。重启保留容器 ID、镜像、配置和挂载，另 31 个容器及 8 份恢复点元数据 SHA256 不变。Samba 随后恢复 healthy，anchor/directory watch 也恢复；不能据此证明此前故障的根因。第一轮 12 个标记对象及 1 个门户建号已删除，IAM 8 个身份均停用或不存在，4 个正式受限绑定保留，测试密码已从主机夹具清除。新夹具使用独立前缀，不复用已修改状态。最终本机 UI 补充中文退出成功/失败分支，15/15 通过。

目录范围复核：实际启用的独立 OIDC 应用是 Nextcloud、Immich，门户另有自身会话，Casdoor 原生账号页用于验证中央 Cookie。目录还启用了 Collabora 管理和 Traefik 管理入口，二者声明 `access.mode=local`，不能纳入 OIDC 全局退出成功声明；应按实际本地认证机制说明限制。Adminer `enabled=false`，不作为已部署接受过退出测试的应用。不得把门户管理员能看到卡片等同于本地后台已登录。


### 19.2 最终制品与定向复验（2026-10-19）

最终制品已激活部署 `20261018T194224Z-f4c09715`，镜像 `sha256:4b5bc59177b8ba3281cc327c5891a6be349a2b7de76684bb241da7954eafc06e`；原生及交叉构建二进制 SHA256 均 `7f56f63fec4ed9163edbb50f3bdeb72e1e7bcd36401143292d051e3acbf5af57`。只在已缓存门户运行时内替换二进制，未发布 registry、未重建其余模块；激活时另 31 个容器及 8 份恢复点未变。此前经明确授权重启的 Samba 是唯一额外容器启动时间变化。

第二份完整账号浏览器回归通过前 37 项后在建号确认等待超时，并非完整通过。原脚本在打开/填写确认前就开始 30 秒等待响应，已将等待移到提交前并改为 120 秒；未知建号结果不自动重试同名操作，由清理按专用用户名核查。此修正只属于测试。本轮定向验证复用尚未执行分组步骤的第二夹具，并明确检查初始成员关系，未把它假定为全新账号回归。

浏览器登录过程中曾等待门户 URL 超时，但捕获的真实 OIDC 回调是 303；不据此认定用户准入失败。后续通过原生 OIDC/API 建立同一独立浏览器上下文的真实 Cookie，再使用页面和受保护 API 验证；所有请求经受限本机 CONNECT 转发到现有 SSH 入口，避免 API 请求绕过 Chromium DNS 规则。未手工注入 Cookie、token 或认证时间。

跨应用结果：`cross-logout-final.json` 的 portal 场景退出前，门户、IAM、Nextcloud、Immich 均是同一测试身份；门户点击退出后前三者中的 Nextcloud 首次仍为 200，第二轮检查为 401，其余立即失效，再访问门户也未静默恢复。`native-cross-sls.json` 则按 Nextcloud 原生 `/apps/user_oidc/sls` 退出，四处原会话全部失效，重新访问门户没有恢复。通用 `/logout` 仅本地退出的早期观察不当成用户 OIDC 菜单行为；固定版本源码已核对原生 Backend 使用 `/sls`。这些结果证明当前浏览器的应用联动，不扩展成全部设备、旧 token 或 exact sid 安全矩阵通过。

宿主故障仍独立记录：重启恢复后 Samba、门户及 anchor 健康再次超时，负载与交换/I/O 压力明显；没有再次重启或更改其他容器。后续功能测试的真实通过与宿主健康问题分开呈现，完整账号回归不冒充全绿。最终定向 R-075/R-076、清理和持久证据结果在下节续记。


### 19.3 定向最终结果与限制

R-075：`native-policy-final.json` 前 18 项真实结果通过，涵盖普通组连续增删且不提交密码、管理员角色组无确认拒绝及带确认免密码增删、系统组空密码/他人密码拒绝和本人密码无域 ACL 仍拒绝。该脚本整体仍标失败：其过期循环使用 `!data.recent_authentication`，未要求 `/api/me` 成功且字段明确为 false，出现一次“脚本认为已过期但写入 200”；当时未记录该次身份读取状态，不推断它的实际返回。无效的过期判定不计入通过。

修正后 `native-expiry-home-final.json` **9/9 通过**：只接受 HTTP 200 且明确 false 的过期状态；305.627 秒后确认过期，普通解锁写入返回 `403 recent_authentication_required`；再次原生凭据登录得到新的近期认证。全程身份读取失败 0。独立回读部署配置 `USER_PORTAL_RECENT_WINDOW_SECONDS=300`。相同防误判已补入 T-007，不放宽过期拒绝断言。

R-076：同一最终脚本实际点击 Sign out，确认 IAM 登出成功及返回主页链接，再点击链接到 `/`，显示 Sign in，`/api/me` 为 401。`logout-home-en.png`、`home-return-final.png` 已人工查看；本机 15/15 UI 包含中英、IAM 成功/失败与重试分支。受保护账号逐次本人域凭据的原生写路径未修改，相关本机验证保持通过；未在 finance 给予任何夹具域级权限。

跨应用：门户场景四处原会话均已核实，Nextcloud 存在异步失效间隔；Nextcloud 原生 SLS 场景在实际再次访问后门户、IAM、Nextcloud、Immich 均保持未认证。原生回调与 Cookie 没有手工注入。`native-cross-sls.json` 的 3 个回调断言及四处身份/退出状态验证通过。只有这两个当前浏览器场景计为本轮结果；根双向登出全矩阵、所有设备及独立 local 管理入口不继承此结论。

最新只读观测中 Samba、门户、Nextcloud、Immich、Casdoor、dirwatch 均恢复 healthy，但主机 I/O pressure some avg10 79.89%、memory pressure some avg10 58.83%，此前有重复健康失败。Btrfs 设备错误计数均为 0，未运行 balance。不能把短时健康恢复宣布为资源故障已修复。M11 暂保持实施中：完整账号 T-007 没有本轮全绿基线，先保留定向通过，后续在资源稳定后补全回归；§18 的 105/105 历史基线不覆盖新代码。


清理最终结果：第二轮 12 个标记对象及未知结果的 1 个门户建号均已按专用引用删除，剩余 0；第一轮此前也已清理。独立只读 SQL 严格检查两轮 16 个 IAM 名称，14 个已停用/删除、2 个不存在、活动身份 0；命令错误不算停用。原 67 个目录对象的 GUID、安全描述符和锚点与基线相同；除门户重建及已授权的 Samba 重启外，30 个容器的 ID、镜像、配置、挂载、启动时间和重启次数不变，8 个原恢复点及元数据摘要保留。最终 Samba/门户/anchor/dirwatch 均 healthy，4 个正式受限写绑定保留。主机和本机夹具中的测试密码字段已移除，临时只读探针凭据副本已删除。制品内二进制摘要和 5 个关键源码文件摘要均重新核对，与最终部署及当前工作树一致。原始回执：`second-final-cleanup.json`、`strict-final-iam.json`。

本轮本机门禁：Go/race 11.031 秒、UI 15/15、Runner 两项配置/清单测试、26 文档/803 需求与 8 主题/70 用例、114 份文档状态、31/31 根索引及 Module 生成检查通过；docs 构建 14.01 秒通过，仅既有 chunk 提示。Module 检查曾被沙箱禁止读取 Go 缓存，放行相同只读检查后通过。测试脚本语法与 diff 空白检查通过。未 Git 提交，未发布镜像，未向 ln 写入新备份。


持久证据：finance `/home/whl/anas-portal-session-20261019/` 保存构建、激活、重启、健康、原生 TLS 和清理结果；最终本机报告/截图/实际执行脚本为 `final-browser-evidence.tgz`（480157 bytes，106 个成员，SHA256 `5d66b14b029e5cd2807ac6f0c486a05fdf1bb2368e58ca87f9c4c58c8a814ffb`），上传后已回读核对完整文件摘要。第一阶段 `browser-local-evidence.tgz`（SHA256 `62fc04de3d919d66b9d0fe34b18ae667ea839c5058f017241ba59a34719c677d`）继续保留，不覆盖失败历史。最终计划与补写后的门禁另存 `final-closure/`，不把本机 `/tmp` 副本当作唯一共享证据。本轮专用 SSH 转发已关闭。

下一步：先处理 finance 的内存/交换与 I/O 压力，再以全新夹具运行完整 T-007；本轮确认的功能无需新设计审批，未完成的全量回归不得沿用定向结果冒充完成。


## 20. 2026-10-19 E2E 覆盖核查、分类范围调整与旧会话反例

用户要求：核对上述问题的 E2E 覆盖和当前主题计划，分类开关也控制后台管理；补“任一应用退出后其他应用是否退出”的真实浏览器测试。R-043 的旧“仅用户大组”约束按本次明确要求退役为 R-077。联动退出范围和涉及的新增流程另行确认，不能用按用户名撤销全部设备代替当前浏览器。

### 当前计划问题及处理顺序

1. **P1：跨应用证据覆盖不足。** §19 的脚本通过原生 API 建立 Cookie 并检查 API 状态，没有正常 Immich 网页回调、较早应用会话和当前 IAM 会话变化的完整场景。它只能保留为当时那组授权的局部结果，不能解释用户的旧网页会话。2026-10-19 在用户当前 Chrome 刷新 Immich，实际 `/api/users/me`、偏好、相册等仍为 200，确认会话有效，撤回“所有场景的 Immich 已退出”结论。
2. **P1：关键定向脚本未固定在仓库。** 上轮五分钟/退出定向脚本和跨应用脚本仅在证据归档，不是仓库的稳定执行入口。新增 `test-env/playwright/user-portal-logout.mjs` 和 T-010，验证真实页面 OIDC、旧凭据、刷新、另一浏览器隔离及 IAM Cookie 丢失后重新认证的旧应用会话；不注入认证 Cookie，不把 5xx 算退出。
3. **P2：完整 T-007 过于串行。** 建号前序失败会让后段分组、过期和退出未执行。105/105 是 §18 的历史镜像，不可覆盖最终新制品；当前 M11 保持实施中。新退出场景独立运行，不以“有测试代码”代替“最新制品全部通过”。
4. **P2：计划分散且快照陈旧。** 门户负责账号和 UI，app-catalog 负责目录协议/全模块覆盖，双向登出计划负责 Provider×Consumer 矩阵，Immich 负责自身网页/移动端/撤权。根 app-catalog 的 0/39 表示尚未按其完整矩阵验收，不能解释为功能全未实现；各计划的历史通过也不能相互继承。本节作为当前门户任务入口，保留原归属而不擅自改成已完成。

### 覆盖情况（测试代码与实机结果分别核对）

| 用户问题 | 已有仓库入口 | 覆盖与本轮处理 |
| --- | --- | --- |
| 首页按钮、恢复入口与窄屏 | T-007、T-009 | 已有真实页面几何/溢出及错误可见性断言；像素级对齐仍包含截图人工检查，不能称全部自动 E2E |
| 下拉框对齐 | T-007、§17 实机专项 | 有同源箭头/CSP、视口检查与历史双语截图；箭头与文字的像素位置仍含人工确认，没有全自动截图比对 |
| Chrome 访问慢 | T-007、R-060 | 有首页账号读取次数、图标缓存/访问控制及导航/资源耗时采集；没有已确认的延迟阈值，主机资源与具体慢请求仍需结合实测诊断 |
| 分类开关 | T-007 原仅用户组；T-006 模拟接口 UI | 原行为符合旧 R-043，但不满足本次后台分类要求；新增双语两大组、卡片顺序、偏好记忆回归，更新为 R-077 |
| 个人资料、密码失败提示、管理员建号/分组/重置 | T-002、T-006—T-009 | 有测试；不同历史制品的通过基线保留。当前完整 T-007 两轮没有全绿，不宣称最新全量验收完成 |
| 五分钟认证及逐次系统验密 | T-006/007/009 | Go/模拟 UI、上轮定向原生结果存在；完整 T-007 当前待补，过期循环已要求 HTTP 200 且明确 false |
| 退出后返回主页 | T-007/009 | UI 双语成功/失败分支有自动检查；上轮定向原生 9/9 含实际点击，固定入口后仍需按新制品记录 |
| 一个应用退出，其他应用一起退出 | T-010（本轮新增） | 过去临时脚本覆盖过窄。本轮必须使用正常网页登录和旧会话反例，并验证旧 Immich Bearer、Cookie、刷新及另一浏览器不受影响；尚未通过，不沿用 §19 局部结论 |

只读目录/会话诊断：当前 Immich admin 会话创建于 2026-10-17，应用 `expiresAt` 为 null，原 OIDC Token 的有效期为一小时；对应较早 Casdoor 父会话与最近门户授权的父会话不同。只记录时间和 SID 哈希，不输出 Token。此事实说明仅核对同批新授权不足，不能仅根据当前 IAM Cookie 推断旧 RP 会话失效。后续实现优先原生 OIDC/API/配置，不新增 Casdoor 补丁或全域凭据。

证据暂存本机 `/tmp/anas-portal-e2e-20261019/`、finance `/home/whl/anas-portal-e2e-20261019/`，专用目录夹具 `pua83137117` 不具备域权限；完整归档和清理结果在本节后续补充。

### 20.1 分类修复的制品与实机结果

部署 `20261018T212006Z-47b41724`，镜像 `sha256:79f87dbcd8081f8e1d610628f64a2ed3ef245117fa5f1b7e8a77fc1c8d680ad2`，二进制 `5c37abb5f7e065653c48cdf863690dfd0792f3ed3a6212d5614b4392747bc8fc`（finance 原生与本机交叉构建一致）。生产源码只在上一冻结门户基线上修改分类开关条件；UI 源 `app.js` SHA256 为 `33dc1e1a73c46e108222409d291988813a0b8f9b50522a73bb306b5df1c1ed73`，不把其他并行工作树改动混入镜像。原生 lock/render/apply 只改变 user_portal；其余 31 个容器 ID/配置/启动时间/重启次数/挂载保持一致，8 个恢复点元数据校验相同，门户 healthy。

| 需求 ID | 固定入口 | 制品/环境 | 结果 |
| --- | --- | --- | --- |
| PORTAL-R-077 | T-006 双语模拟 UI；T-010 categories 场景 | 本机 Chrome；finance 79f87dbc | 通过：17/17 UI；真实网页 4/4，用户与后台大组保留、小分类同时隐藏、卡片顺序不变、刷新记忆与恢复通过 |
| 变更回归 | `go test -race ./modules/user_portal/portal` | 当前门户源码 | 通过，10.597 秒；不是全账号 E2E 的替代 |

首次新增 UI 测试因模拟 bootstrap 固定英语，中文刷新断言失败；修正模拟输入后通过，失败日志保留。当前分类场景的真实运行使用专用测试管理员，不操作用户现有浏览器会话。

### 20.2 剩余顺序与边界

1. 完成 T-010 各退出来源、旧 RP 会话、旧 Bearer 有效性对照、刷新及另一浏览器隔离；前置认证失败或服务不可用分别记录，不能计入已退出。
2. 根据用户确认的退出范围确定补齐方式。建议当前浏览器的原生逐应用退出保留其他设备；这需要应用间导航、超时和部分成功处理，复杂度中等，没有新增服务或密码保存。若选择所有设备，必须另行采用用户级撤销语义，不能冒充当前 sid 联动。该设计尚未实现。
3. finance 资源压力恢复后补完 M11 的完整 T-007，保留过去失败；通过全部现行判据后才收口。全 Provider/Consumer 矩阵继续归双向登出计划；邮件后端 PoC 仍独立。

本轮只读观测：load 22.93，I/O 等待 `some avg10=96.64%`、内存等待 `some avg10=77.83%`，可用内存约 1.4 GiB、swap 使用约 3.9 GiB，Samba unhealthy，其他六项所查服务 healthy。它与本轮 API 超时同时出现，但不足以归因于单一进程；未重复重启 Samba、调整其他现有容器或认证配置。这是实机完整验收的实际阻塞，不能用本地单元通过掩盖。

### 20.3 真实网页联动首轮（保留失败）

同一 79f87dbc 制品，首轮驱动及逐项记录保存为 `real-browser-driver.mjs`、`real-ui-full-first-pass.json`，驱动摘要另在 `first-pass-driver.json`，不能与后续改进版本混用。

| T-010 场景 | 已执行检查 | 首轮结论 |
| --- | --- | --- |
| categories | 4/4 | 通过，R-077 |
| portal | 前 8 项通过，含四服务原会话失效、旧 Immich 凭据 401、主页按钮 | 整组失败：随后读取另一浏览器状态超时；隔离及刷新检查未完成 |
| nextcloud | 9/9 | 同批新会话联动通过：四服务退出、旧凭据拒绝、另一浏览器保留、刷新不恢复；不扩展为任意旧会话或完整矩阵 |
| immich | 登出前的身份检查超时 | 未执行退出，不能判定 |
| portal-after-central-cookie-loss | 首次门户 OIDC 回调 401 | 未执行退出；回调统一返回 authentication_rejected，不能仅凭该状态确认具体原因 |

更早几轮的 Nextcloud 原生回调曾 403，页面显示 “The received state has expired.”；后续未修改认证配置即可完成回调。驱动改为仅选择已有 IAM 账号，避免同时提交第二份密码；重复状态消费仍是未证实解释，不把它宣布为服务器根因。Immich 网页回调的异步导航等待也已修正。失败报告、驱动和日志全部保留。

最终驱动增加退出前 Bearer 有效性对照、IAM 明确未登录响应匹配、逐应用不可用结果及阶段/耗时，并在旧会话场景核验“仅 IAM 已未登录、原 RP 会话仍有效”。不可用一律失败，不计入退出。该场景针对 IAM 会话变化；没有修改 Token 时间，也没有等待真实一小时再验收过期授权，不能代替真实长时间存活会话的全部覆盖。最终定向运行结果及清理在后续补充。

### 20.4 最终定向复现：旧会话仍有效

执行 T-010 的 `portal-after-central-cookie-loss`，真实驱动 SHA256 `f69bc1388fe95258ff93f3ec71cb898527c9b0088eceffdab3722493c3bbdc05`，镜像仍为 79f87dbc。所有前置身份/凭据对照通过：四服务身份一致、旧 Immich Bearer 退出前 200、另一浏览器独立登录成功、仅 IAM 站点数据丢失后原应用仍有效、门户原生重新认证后原应用 Cookie 仍有效。

| 观察点 | 门户 | IAM | Nextcloud | Immich |
| --- | --- | --- | --- | --- |
| 退出前 | 200，同一身份 | 已登录，同一身份 | 200，同一身份 | 200，同一身份 |
| 门户退出并等待 90 秒 | 401 | 明确 Please login first | **200，仍为原身份** | **200，仍为原身份** |
| 刷新/重新进入 | 401 | 明确未登录 | **200** | **200** |
| 另一浏览器 | 200，同一身份 | 已登录 | 200 | 200 |

原 Immich Bearer 退出后依然 200，故“所有当前浏览器会话退出”“旧凭据拒绝”“刷新不恢复”三项核心断言失败。该失败是有效会话保留的实测反例，不是超时或仅旧 DOM。R-041 当前门户/IAM 的退出与 R-076 主页按钮通过，但不能宣布跨应用退出完整通过；全套 T-010 与 M11 保持未完成，不新增 Casdoor 补丁。

末项 UI 取样在页面渲染前未看到账号按钮，却有 API 200，不能独立作为退出成功。审查后将该断言同时绑定 Immich API 必须未登录；用本次真实观测验证收紧后结果为 false。此单行收紧未再重跑整套浏览器；实际执行驱动与收紧后代码、SHA 分开保存，核心失败结论不变。T-007 另纠正残留的退役 R-043 报告 ID 到 R-077，未改变请求行为。

本轮使用正常网页 OIDC 登录；门户实际点击退出和返回主页，Nextcloud/Immich 退出来源使用各自原生路由，不声称已验证两者的菜单接线。本轮四服务之外的 Module、本地独立凭据后台、真实移动端及全部 Provider 矩阵未计入通过。下一步先确认当前浏览器/所有设备的产品范围，再补齐原生逐应用退出及部分失败处理；完整账号和跨模块回归继续待执行。

### 20.5 清理、门禁与证据保全

专用 12 个目录对象已按 GUID/标记删除，剩余 0；原生只读 SQL 确认 7 个 IAM 名称中 6 个已停用/删除、1 个不存在，活动 0。原 67 个目录对象的 GUID/安全描述符/身份锚点相同；其余 31 个容器 ID/配置/启动时间/重启次数/挂载不变，8 个恢复点元数据 SHA 相同。四个正式受限绑定保留，未授予域权限。finance 私有夹具及本轮执行目录内的测试密码副本核验为 0。最后 Samba、门户、anchor、directory watch 均 healthy；此前资源压力和失败记录仍保留。

相关 Go/race、Hook/appcatalog、17/17 模拟 UI、模块文档生成检查、803 项需求覆盖、8 个主题/71 个用例 catalog、需求/计划状态索引、114 份文档状态、语法和 diff 检查通过；文档最终构建 14.12 秒通过。未执行本轮全仓库 Go 或完整 T-007 重跑，不将门禁通过计为跨应用 E2E 通过。原始 JSON、截图、实际执行和审查后的驱动、失败日志及源码/制品摘要保存在 finance `/home/whl/anas-portal-e2e-20261019/`；浏览器证据以 `execution-evidence.tgz` 回存此目录，校验摘要见后续记录。没有向 ln 创建备份。

归档已完成：`execution-evidence.tgz` 为 117976 bytes、53 个成员，SHA256 `764c9f60a56f1abbf3ac75eae70becc371e6528a1f231b55bd5a77cdee05b9e6`；本机扫描未发现夹具密码、JWT 或私钥，finance 回读校验压缩包及清单中 52 个文件全部相符。当前计划/要求/用例与最后门禁副本另存同目录 `final-documents/`。本机测试密码已去除，本任务 SSH 转发已关闭。M12 完成，M11 与跨应用退出继续未完成。


## 21. 2026-10-19 当前浏览器联动退出修复

用户已确认：退出当前浏览器全部应用，保留其他设备；允许最小登出协议元数据（域名及固定路径）覆盖当前不可见的应用。§20 的等待范围确认已解除，历史失败和证据保留。该多阶段流程复杂度中等，用户已确认；复用门户进程、私有部署清单和模块原生接口，不新增服务、密码保存或 Casdoor 源码补丁。

首版实现（已由 §21.2 的简化版本替代）：门户按固定适配器识别已部署的 Nextcloud/Immich 源应用，业务目录仍严格过滤。Traefik 只将两个应用域名上的一个固定协议路径交给现有门户进程，短期签名请求校验目标 origin，父子页面校验 origin、窗口、随机 nonce 后调用原生 API。Nextcloud 使用本地 core logout 与原生 CSRF Token，避免 SLS 在本地退出前请求 OIDC discovery；Immich 使用 native logout 删除当前会话。应用 Cookie、身份和含 ID Token 的原生返回值不经 postMessage 返回门户页面。退出结果逐项核验；部分失败提供十分钟内仅可退出的重试凭证，门户会话不会恢复。

本轮当前基线：本机 Go/race 三包通过（门户 10.874 秒）；模拟真实 Chrome UI 首轮 20/20 通过，包含两个原生适配器、接口不可用、部分失败和重试。默认 Playwright 缓存浏览器不存在造成的启动失败保留，随后使用系统 Chrome 重跑；不把模拟接口当作 finance 原生验收。

剩余：核对 Hook 清单输入和 Traefik 路由，构建冻结门户源码并部署 finance；运行真实旧会话/另一浏览器/旧 Bearer 回归及失败分支。Nextcloud/Immich 的原生退出来源如何在 IAM 返回后继续浏览器清理还需接线，不以门户来源通过代替；M13 与根双向登出矩阵保持实施中。原始证据本机 `/tmp/anas-portal-browser-logout-20261019/`，finance `/home/whl/anas-portal-browser-logout-20261019/`，最终补制品摘要、逐项结果和清理。


### 21.1 冻结构建与激活

部署 `20261019T003300Z-463ea940`，镜像 `sha256:001c980aff6d10cc93c8f9d1b98cd72805ad60e548e0002b2bf8cf40cc869c5c`，二进制 SHA256 `5bfe5c5e209af997beb61a666e848eaa461ae58d1a6967dc7ed0841b49a70c64`，finance 原生与本机交叉构建一致。以上一冻结门户基线覆盖九个本次生产文件，不混入其他模块/工作树改动。原生 lock/render/apply 仅改变 user_portal，31 个其他容器与八个快照元数据完全一致，门户 healthy；原始构建和激活回执见本轮证据目录。未发布镜像。

追加伪造父窗口/错误 nonce 和静默超时反例后，当前本机模拟 Chrome UI **21/21** 通过。需求新增四项后覆盖归属 26 文档/807 项、8 主题/71 用例通过；中间未更新表格和摘要造成的门禁失败保留，不能用失败前状态宣称通过。当前仍未完成 finance 新制品 E2E。


### 21.2 匿名原生回调确认与第二轮修复

用户进一步确认：原生 Nextcloud/Immich 退出后，固定回调页即使没有门户登录态，也允许读取相同的最小协议地址。新增 R-082；业务目录 API 和图标权限不变。原生 Module 可配置 `browser_logout_return_url`，登记回调并交给其原生退出处理；Immich 使用 Provider 发布的可选 `OIDC_END_SESSION_URL`，不硬编码特定 IAM 路径，不新增 Casdoor 上游补丁。门户回调页沿用匿名 CSRF 会话；跨站点或缺少 Fetch Metadata 的导航必须点击继续，同站点原生返回可自动继续。

首轮真实检查：分类 4/4；旧会话场景在 Immich 首次使用向导的旧 CSS 选择器处超时，未执行退出；下一场景门户导航失败。检查发现本任务 SSH 转发已退出；当时仅本机转发的连接拒绝是实测，不能把所有原生页面错误都归因于目录。恢复为有限次数重连的专用本机转发，未重启 Samba。首次向导通过独立测试账号只读检查，已取到当前真实按钮和页面，继续修复选择器。

从真实 Nextcloud 回读确认 OCS v2 成功 `statuscode` 为 200，原先模拟只用 100，已将固定适配器改为兼容两个原生成功值并保留身份字段检查。匿名首页 CSP 的协议域名泄漏已增加反例；第二版曾以短期退出凭证 Cookie 修正，最终版本按下段简化；匿名固定回调是用户明确授权的独立例外。第二版镜像 b6a8c4c6 仅构建未激活，后续最终制品还需纳入匿名回调接线后重新构建验收。


匿名协议读取获准后，最终实现进一步简化：重试复用 `/api/bootstrap` 的匿名 CSRF 与同一个 `/api/logout/complete`，删除首版另存的十分钟退出凭证、额外 Cookie 和重试 API。仅固定退出页发布协议 frame origins，普通匿名首页不发布；应用帧仍使用两分钟签名请求并验证父窗口/origin/nonce。前端在请求前锁定按钮和清理迟到响应，部分失败可重载后重新核验；不保存 IAM Token 或目录凭据。首版/第二版构建与失败记录不作为最终制品验收。


### 21.3 最终候选构建与部署前空间门槛

最终候选已在 finance 构建完成但尚未激活：门户镜像 `sha256:b93639cfb312a8b254c454bf527318059807e7e84a100c5960cef88d1d1e7cd6`，二进制 `4c77e2c2fed846d88e13f266a89345d1dcacbae98aa00e848f59ba1049a973de`；Nextcloud 原生配置脚本覆盖镜像 `sha256:e9bb49d4defe7dabb41d72a886b1ec039f844fce5da028f5ebddc9a88878b5e0`。门户和四个 Hook 的原生/交叉构建摘要相同，清单及逐文件摘要在 finance `evidence/final-build.json`。Nextcloud 首次 Docker 标签提交超时，复用缓存后构建完成；本轮遗留的单个中间构建容器已删除，未删除用户容器。两个 Nextcloud 服务引用已在候选冻结 Compose 中固定为新镜像，其他服务镜像不变。

最新本机校验：门户与三个原生 Module Hook、固定协议目标包的 Go/race 通过；23/23 Chrome 模拟接口 UI 通过，覆盖匿名回调、两种适配器、错误来源/nonce、部分失败重试和静默超时。808 项需求/71 个活动用例目录、114 份文档状态、需求/计划索引、模块文档与语法检查通过；文档构建 23.21 秒。Nextcloud 原生回调配置改动同时改变 NCT-T-001 所引用的测试文件和 TEMP-T-021 所引用的 task.sh，已复核该行差异并更新溯源摘要，不将此动作记作两者新的实机验收。

实机门槛未解除：8 个既有恢复点保留，上次完整快照实际占用约 2.8 GB，当前根盘约余 8.1 GiB，不能同时写新完整快照并保证 Collabora 的 8 GiB 启动条件。旧独立 Docker 中无容器/无标签且不被冻结配置和主引擎引用的构建镜像已原生清理，实际释放约 170 MiB，不能按镜像逻辑大小虚报清理量。首份归档尝试原生 Btrfs 透明压缩，前后 SHA256 相同但未释放空间；已停止后续在线压缩。归档包含 gzip OCI 镜像层，不能假定再次压缩有收益。没有删除恢复点，没有向 ln 创建备份。

主机只读采样内存等待 some avg10=98.73%、I/O 等待 some avg10=99.51%，压缩推进缓慢。曾请求将测试栈的暂停提前到空间整理前；确认主要镜像层已压缩后撤回该建议，不因这个尚未选择的问题暂停服务。现改为使用主机已有 xfs_io 原生块去重：两对归档的同名、同尺寸、同块对齐 OCI blob 约有 5.1 GB 可共享，内核逐块比较一致才执行，不同内容拒绝，不需要新依赖或自定义 ioctl 实现。私有临时文件正反例已验证：该工具“内容不同”时退出码仍为 0，故必须检查明确字节数或不匹配消息，不能仅看退出码。实际去重及摘要复核仍进行中，不按估计量宣称已释放。当前线上仍是 `20261019T003955Z-793f145e`/001c980a。M13、完整 T-010 和 M11 均未完成，不能用本机门禁代替实机结论。

首轮浏览器错误 JSON 曾被临时调试脚本复用文件名覆盖；原始逐项 JSON 已不可用，不能补造。首轮控制台摘要、当时工具输出及独立向导调试材料保留；这里只依据这些材料记录分类通过和前置失败。最终驱动改为每轮唯一目录并记录自身 SHA256，后续验收必须保存实际执行版本。


候选配置已通过原生 `config set --defer`、lock/render：`20261019T102453Z-c70218fc`，相对活动部署仅改变 Casdoor、Immich、Nextcloud、user_portal 四个预期 Module，未激活。两项回调为 `https://portal.finance.hlong.wang:10000/signed-out?native=1`。准备过程中直接编辑受管配置被 CLI 拒绝；确认只加两项后恢复原始字节，再改用原生命令。`config` 使用 `--root`，与 lock/render 的 `--module-root` 不同；先显式 lock 消除模块摘要差异后暂存成功。失败日志均保留，未绕过受管配置校验。


### 21.4 实际空间回收与退出缓存审查

归档去重全部使用已安装工具和内核比较，不删除文件或恢复点。第一对 Oct 18 归档的约 2.955 GB 相同块已共享，双方前后 SHA256 与原生清单一致；Oct 8 一对虽报告约 2.170 GB 逻辑去重，实际几乎无回收，`btrfs filesystem du` 确认其独占空间原本为 0。不能将工具逻辑处理字节当作回收量。继续在 Oct 17 归档的独占块上与 Oct 16 只读来源去重约 1.584 GB，修改目标前后 SHA256 均为 `835ccee5b6d5a151221bc674b5a2af145ea002a6d7bc4a6d74b41c7bccb6e141`。最终根盘可用 `12504403968` bytes，8 个恢复点元数据及 pin 保持一致；APT/Go 可重建缓存和旧 journal 已清理，所有镜像制品和验收证据保留。另一个只读来源未做重复全文件后哈希，原生接口不写入其逻辑内容，不能扩张为全部恢复点的数据恢复演练。

原生完整快照以有 PID/日志的单次任务启动，脱离 SSH 会话防止连接中断；严格等待上述归档复核及既定空间门槛，再执行 include-userdata 的创建和 verify，并按原生流程恢复。此处没有提前暂停测试栈，没有删除固定恢复点，未向 ln 备份。完整快照的最终 ID/结果及激活尚待回执。

审查发现：重新登录后再打开旧 `/signed-out`，原实现会在检查 bootstrap.authenticated 之前使用 sessionStorage 的旧成功结果，可能把新的有效登录显示成已退出。按既有 R-081 修复：只有匿名 bootstrap 可恢复旧结果；新认证会清除旧状态并回应用页。明确的新认证/匿名对照在修复前 1/2（新认证失败），修复后完整 Chrome UI 25/25，14.610 秒；门户 Go/race 9.844 秒通过。伪造消息测试首轮发现仅等待 iframe 元素会产生取 frame 的竞态，现等待真实 frame URL，保留原来源/nonce/超时断言。交叉构建首次误设 GOTOOLCHAIN=local 落到宿主 Go 1.24.2 而失败；使用项目实际 Go 1.26.6 后得到修订二进制 `3ef4c1a6ecd9faea3d6631e208de5fe6697ebbaafec51f85eac37bbd944791ca`。原生匹配、修订镜像及激活仍待结果，不把候选 b93639cf 计作最终通过。

T-010 补齐中英各四项真实分类检查、匿名退出后的业务目录 401、明确重新登录后旧结果页不误报；真实代码继续使用原生网页登录，无注入认证。上述新增驱动尚未实机运行，冻结版本随最终执行结果记录。


### 21.5 最终制品激活与原生浏览器前置阻塞

完整新快照 `20261019T110522Z-7115429c` 已完成包含 userdata 的捕获及原生 verify；保留此前八个恢复点，共九个。创建包装进程在恢复服务阶段达到 1200 秒超时，不能记作该命令直接成功；原生 `start immich user_portal` 恢复依赖闭包后，另行 verify 通过。原始超时、恢复和校验回执分别保留在本轮 finance 证据目录，不重复创建快照。

最终活动部署 `20261019T114808Z-b392c642`：门户镜像 `sha256:3c137cac7cb0bb607348fdc93837547efa019a47b3ac37a3f98e94c4fe302c06`，二进制 `3ef4c1a6ecd9faea3d6631e208de5fe6697ebbaafec51f85eac37bbd944791ca`，app.js `c3f3d9ae2202e02cb06fedc1239fa5f796b4346508a7b58ff8922954ed75ba1f`。Nextcloud 镜像为 `sha256:e9bb49d4defe7dabb41d72a886b1ec039f844fce5da028f5ebddc9a88878b5e0`；Immich 与 Casdoor 镜像不变，没有新增 Casdoor 上游补丁。原生 apply 只改变 Casdoor、Immich、Nextcloud、user_portal 四个 Module，其余 22 个快照后容器的 ID、镜像、配置、挂载、启动时间及重启数一致。回执 `evidence/native-final-activation.json`；未发布镜像或 Git 提交。

真实 Chrome 155.0.8059.80 首轮 SSH 入口及第二轮正常域名入口均取得中英分类 8/8；第二轮冻结驱动 `7724d12574cef30993e7b40ccd62b95f7edd169c79594d47a7e649af5c4265ce` 的三个退出来源卡在 Immich 原生登录前置，退出断言未执行。原始 JSON 和截图按运行 ID 保留；中止仅针对任务自建 Node 进程，另存 operator-interruption.json，不改写已经测得的断言。不能将分类通过扩张为联动退出通过。

传输诊断观察到 SSH 转发默认 HTTP/2 下两个 Immich 脚本请求长期未完成；同一路径禁用 HTTP/2及正常域名的独立匿名检查曾成功显示原生登录按钮。但正常域名全套中仍出现加载动画或已登录后的点击超时，故这些观测不足以归因全部失败。补细登录阶段、原生请求和完整脱敏点击错误；增加显式 HTTP/1 诊断选项并记录在报告，不改生产协议配置。M13 与 M11 继续实施中，待真实退出断言、夹具清理与证据回存。


定向 HTTP/1 原生门户退出（驱动 `96e374b1049e9a81791d3a757acd64ae7bda9ff1874c187ff97683fb741f194e`，运行 `2026-10-19T12-55-15-903Z-7561`）取得 13 项已测断言通过：四方同身份、旧 Bearer 有效、另一浏览器独立、退出后四方未认证、旧 Bearer 401、业务目录 401、另一浏览器全保留、刷新不恢复等。页面诚实显示部分未确认；当时 Nextcloud 只读探针用时 14.856 秒，不能据此断言该项失败原因。整体仍失败于附加缓存回归的测试前提：脚本错误地要求缓存的结果必须为全部成功，尚未执行新登录回归。修正为比较本次实际结果，并增加逐域 UI 结果保存，未放松任何会话或失败断言。

全套修订驱动 `734c55ad9e0c3b28d131c4989b68ca8044bf5640cf3073560c35a3f9419e9a33` 已启动十场景，显式记录 `http1_only=true`；对照浏览器跨场景保持原生会话，每次执行前后核验，禁止重新登录掩盖误撤销。中英分类再次 8/8。尚未出最终结果，不能将定向 13 项或分类重复次数累加为全套通过。

阶段证据已回存 finance `browser-progress.tar.gz`，719720 bytes，112 个文件逐项摘要校验，SHA256 `d9be87612acdb2b39f2bcc73a41b38161d32f93cc2992b61f5ddf10ffbc67937`；夹具已知密码、JWT 与私钥模式扫描通过。该包是运行中阶段副本，最终结果和清理另补；没有向 ln 写入备份。


修订全套首轮 `2026-10-19T13-06-06-094Z-11709` 的分类 8/8，但对照浏览器门户 `/oidc/callback` 原生返回 401，退出断言尚未执行。保留页面/请求错误和操作员中止记录；不把它归为退出缺陷，也不宣称该原生登录错误已修复。驱动随后修正部分准备的对照上下文不能误当作已验证上下文的处理：准备失败即关闭候选；已验证的对照会话仍绝不重登。并对门户原生回调的 HTTP 拒绝及时报错，不等待主页标题超时。实际执行版本 `805f29d9c2fac608db9be03ea891acd814f1d32ffebe4912d31eaac00a3bc412` 从 Nextcloud 来源开始，其余场景继续待结果。

只读资源复核 `runtime-memory-limits.json`：门户、Casdoor、Nextcloud、Immich、Samba、Postgres 的 Memory/MemoryReservation/MemorySwap 均为 0，cgroup memory.high/max 均为 max，无这些容器的 OOM 事件；不能推测为容器额外内存上限。该时段 system.slice 使用约 3.8 GB swap，根 cgroup some avg10 23.55%、full avg10 14.33%；反映主机等待仍存在，不证明特定 401 的原因。采样不调整系统设置或暂停容器。


Nextcloud 原生来源定向复验通过：运行 `2026-10-19T13-32-33-088Z-18352`，驱动 `bd7fe9f45d07226c859d34b4b5870362b1d4532c38eed1f0e91cc9a58a0d3951`，实际 Chrome 155.0.8059.80、正常域名入口、HTTP/1 诊断选项，14/14 断言。原生 `/apps/user_oidc/sls` 自动回到匿名门户回调并继续清理，UI 逐域三项 Signed out；四方接口未认证、旧 Immich Bearer 401、另一浏览器四方身份保持、刷新不恢复及匿名目录 401 均通过。没有将原生路由导航扩张为菜单按钮接线验证。

此前 `805f29d9…` 的两个对照 Immich 登录前置超时及普通门户回调前置失败保留；新增驱动在关闭失败候选前保存其页面/请求，未修改生产鉴权。其余八个场景用相同 `bd7fe9f4…` 继续，从 IAM Cookie 丢失后的旧会话来源开始。M13 尚待全部适用场景和清理，不以 Nextcloud 单场景通过归档。


剩余八场景首轮的旧会话来源仍未到退出：四方前置检查中，门户/IAM/Immich 已匹配，Nextcloud 探针 15.199 秒超时；同时页面还有多项资源/API 在等待。该单次读不能当作登录丢失或退出成功。测试准备增加最多 120 秒的真实 API 身份就绪观测，保留每次原始状态和耗时；不重登、不注入凭据、不放宽身份/退出判据。已执行版本 `bd7fe9f4…` 仍按原结果保留，后续重跑才使用新版本。

Nextcloud 通过及中间失败证据增量回存 `browser-after-nextcloud.tar.gz`，879243 bytes、127 个逐项校验文件，SHA256 `3e41478b1b362b97909d2ba05d89b545eccff59ec691cc6a0e38a48e865a1807`；已知测试密码、JWT/私钥扫描通过。该增量包仍不代表全套或最终清理。


同轮 `2026-10-19T13-40-39-604Z-20208` 的 portal-partial 已通过 20/20：先确认 IAM Cookie/本地缓存丢失而旧 RP 会话仍有效，重新原生认证不替换旧 RP Cookie；只阻断 Immich 固定退出帧后，页面报告部分失败，旧 Immich API 仍有效而门户已退出。刷新保留部分结果/重试，恢复目标后原生重试清理完成；最终四方未认证、旧 Bearer 401、对照浏览器四方保留、刷新不恢复和业务目录 401 均通过。这是实际旧会话与故障恢复证据，不等同于原先前置失败的单独 portal-after-central-cookie-loss 场景已重跑。其余来源继续执行。


`bd7fe9f4…` 八场景运行已自然结束，未人工改写结果：portal-partial 20/20，通过；另外七场景在真实登录/身份准备阶段失败，未执行退出断言。主要原始观测为 Nextcloud 15 秒请求超时、门户回调/身份未就绪及 Immich 原生回调等待超时；不是七项退出反例，也绝不是全套通过。新增真实身份就绪观测版 `b8539654ffde73a55c97727334e38780a01d1785f0a2865c6c7ac991b8ab81a3` 单独重跑 Immich 来源，不隐藏旧结果或重登对照浏览器。

浏览器运行环境尝试：finance 无现成 Node/Chromium，ln 有约 51.3 GB 根盘可用、约 8 GB 内存，但无浏览器运行时。只准备一次性 Playwright 1.62.1 官方固定镜像 `mcr.microsoft.com/playwright@sha256:c091b21d9fae78c76e85cd4356431e9b018402f172a214fc7d7a5e9a7e29d8ac` 的工具环境，保留 ln 原 43 个容器基线；不迁移 finance 服务。具体临时 ANAS 管理员凭据的跨主机传输被自动审批以未明确授权为由拒绝，已通过异步问题请求用户确认；在回复前不转移密码。公开脚本/包的传输与匿名加载诊断可独立继续，不包含任何真实身份或凭据。不得将工具准备记成 finance 已完成新验收。

本机只读内存采样：16 GiB 物理内存，系统 memory_pressure 报告可用比例 35%；尽管 swap 总使用约 16.9 GB，两秒观测 Swapins/Swapouts 未增加，不能据总 swap 量认定当时有活动换页瓶颈。仅本任务一个 Chrome 和一个驱动在运行，没有发现本任务残留的并行头部浏览器。默认 HTTP/2 固定公网入口匿名检查仍有六个字体/脚本 45 秒未完成，不能仅归因为 SSH。


用户随后明确批准一次性转移临时测试账号 `pua05799dfcmanager`，并要求继续验收、排查服务器等待原因。仅该账号的用户名/anchor/密码及制品基线已传至 ln 的 0700 临时目录，文件 0600；不含另六个测试密码或生产绑定凭据。先前自动审批拒绝为历史记录，不再作为当前阻塞。ln 使用一次性官方固定 Playwright 工具容器；只挂载公开依赖、驱动、该单一只读输入和自己的证据目录，不挂载 Docker socket 或 ln 原 43 个容器的私有基线。

ln 原 Docker 默认 docker0 不存在，首个匿名容器在网络建立前失败 125，回执保留；没有修复/重启既有 Docker 或容器。改为创建本轮具名、带归属标签的独立桥接网络后，匿名默认协议浏览器检查 10.65 秒完成，Chromium **151.0.7922.34**、Immich 登录按钮可见，无页面错误。与本机 Chrome 155 的 45 秒停滞存在客户端/路径差别；两个变量尚未分离，不能断言仅由网络或仅由浏览器版本造成。

登记驱动还修复一个测试出口问题：旧版会在 JSON 标记 failed 后仍返回 0，历史结论一直以原始 JSON 判定，没有据退出码宣布通过；新版失败返回非零。完整十场景已在 ln 启动，目标仍是 finance:10000，默认协议不禁用 HTTP/2，实际驱动 `d99346c989627411e69fa39e84a7fb8564d3e549927d2d5f04c794bb56eac4b5`。先前本机身份就绪版 Immich 定向仍卡在原生回调，未到新增身份轮询，保留失败而不记作修复成功。

恢复后 2026-10-19T14:19:56Z 的 6.05 秒采样：finance uptime 约 21.2 天，MemTotal 3386268 KiB 未变，swap 使用约 3.85 GiB；换入 182048 B/s、换出 3310706 B/s，每秒 57 次需要磁盘读取的缺页，内存 some avg10 21.65%、I/O some avg10 37.35%，sda busy 50.1%。这确认仍有活跃换页与 I/O 等待，较此前接近 100% 的峰值下降；不等同于服务器已完全无压力。Btrfs 设备五项错误计数均 0，无活动 balance；近十小时相关 kernel journal 查询无匹配 OOM/块设备错误/长阻塞记录。继续区分已测资源问题、维护负载放大与尚未证实的根因，不随意修改系统参数或既有服务。


### 21.6 LN 默认协议完整测试与性能诊断

LN 一次性工具容器完整运行 `2026-10-19T14-25-52-748Z-7` 已自然结束：Chromium 151.0.7922.34、默认协议（未禁用 HTTP/2）、驱动 `d99346c989627411e69fa39e84a7fb8564d3e549927d2d5f04c794bb56eac4b5`，十场景 **3 通过、7 失败**，CLI 返回 1。制品仍为 §21.5 的 `20261019T114808Z-b392c642`，没有重新构建、发布或修改 Casdoor 源码。

| 场景 | 实际结果 | 结论边界 |
| --- | --- | --- |
| categories | 8/8，通过 | 中英文开关控制用户和管理小分类 |
| portal | 退出及隔离等前 13 项通过；新增显式登录检查失败 | 四方退出、旧 Bearer 401、另一浏览器保留、刷新不恢复均已测得；后续新认证 `/api/me` 503，整体失败 |
| nextcloud | Immich 登录前置失败 | `/api/oauth/authorize` 持续等待约 120 秒；未到退出断言 |
| immich | Immich 登录前置失败 | 原生 `/api/oauth/callback` 返回 502；未到退出断言 |
| portal-after-central-cookie-loss | 门户登录前置失败 | `/oidc/callback` 返回 404，代理记录无匹配 Router/Service；不能据此断言具体健康事件 |
| portal-partial | 门户登录前置超时 | 原先 Mac 20/20 基线保留，本次未到故障注入 |
| nextcloud-after-central-cookie-loss | 16 项中隔离检查未确认，整体失败 | 当前浏览器退出/旧 Bearer/刷新均通过；另一浏览器门户 API 15 秒超时，其他三方身份仍匹配；不能把超时说成被登出 |
| immich-after-central-cookie-loss | 16/16，通过 | 清除 IAM Cookie 后的旧会话，四方退出、另一浏览器保留、旧 Bearer 与刷新均通过 |
| nextcloud-without-portal | 15 项中刷新检查未确认，整体失败 | 退出后四方明确未认证，另一浏览器保留；刷新后 Nextcloud 探针 15 秒超时，不能算成功或会话恢复 |
| immich-without-portal | 15/15，通过 | 从未打开门户的原生来源自动回调，四方未认证、旧 Bearer、刷新与另一浏览器隔离均通过 |

原始报告、截图、日志和实际驱动已从 LN 回收，逐文件校验与测试密码/JWT/私钥模式扫描通过；阶段包 `ln-browser-native.tar.gz` SHA256 `b2c61becdffebaafee03a4c0390d3437d7762336509f9b1377d6f05c9fd42510`，21 个文件。该包在本机暂存，最终必须连同重跑和清理证据回存 finance，不能算共享持久归档已完成。

性能诊断的实测与推断分开：

- 实测物理内存仅 3386268 KiB（约 3.23 GiB），未观察到重启或扩容。14:19Z 仍有约 3.31 MB/s 换出和 182 KB/s 换入；内存/I/O 等待从此前接近 100% 峰值降到 21.65%/37.35%，14:54Z 十秒平均进一步降到 5.95%/18.33%，不能用单次 MemAvailable 较高或总 swap 量代替活动换页判断。
- 主引擎 32 个现有容器之外，`/run/anas-casdoor-test-20261003.sock` 独立引擎还运行 14 个旧测试容器，含另一套 Samba、Nextcloud、Casdoor、LLNG；入口端口 9000，与本次 10000 栈独立。其容器及专用 Docker/containerd 占用额外内存/swap，是实测的容量负担；未进行停机对照，不能宣称唯一根因。未把仍运行的旧测试栈当垃圾删除，已请求用户单独确认停机范围。
- Samba 最近五次健康检查曾连续两次约十秒超时，随后三次约四至五秒通过。门户当前健康检查调用原生 LDAP Probe，目录慢可能影响代理可用性；14:36Z 门户回调确有无路由 404，但历史 health_status 事件查询为空，尚不能证明该次路由丢失必由这个调用链造成。没有关闭健康检查或绕过目录授权。
- 代理记录确认门户 `/api/me` 503 来自应用，约 13.25 秒；Immich authorize 约 120.25 秒后 499，callback 原生 502。当前六个关键容器均无代理环境变量；容器内严格 TLS 的 OIDC discovery 三次 200（约 3028/276/36 ms），DNS 指向预期内网入口。不把该短时正常检查扩张为间歇网络或应用错误已排除。
- 未发现活动归档/构建进程、容器额外内存上限或所查时段的 OOM/块设备错误；维护阶段的压缩/哈希和构建会增加已紧张主机的 I/O，但尚无足够连续观测给各项因素分配因果占比。此轮未调整 swappiness、未重启 Samba/宿主/既有 Docker、未暂停其他服务。

诊断原件保存在 finance 本轮 `evidence/`：`recovered-host-diagnostic.json`、`runtime-cause-diagnostic.json`、`resource-user-map.json`、`extra-stack-readonly.json`、`proxy-health-diagnostic.json`、`oidc-upstream-diagnostic.json`、`runtime-error-details.json`。不含原密码、Cookie 或令牌；副本逐项摘要另存 `public-diagnostics-receipt.json`。

本轮 R-077 的真实分类通过；R-078/R-079/R-081/R-082 有上述定向真实通过证据，但 T-010 完整验收仍失败，M13 和 M11 继续实施中。七个失败场景使用相同不可变驱动在 LN 定向重跑，不变更生产配置或测试判据，保留十场景原失败结论。夹具、一次性工具环境及最终证据回存尚待后续回执；不得提前标为清理完成或归档完成。


首轮完整 LN 结果及诊断已作为 `browser-after-ln-full.tar.gz` 回存 finance，1713927 bytes、202 个逐文件校验条目，SHA256 `3878ee18e47d8142256dfc7abf3db20ef99db33ca8a464edf8ecff01593bf922`；密码/JWT/私钥模式扫描通过。此包包含首轮独立 JSON、截图和当时顶层状态/日志副本；定向重跑使用新的运行目录，包装器复用了顶层日志名，首轮原始字节已在阶段包保留，不能用后来的顶层文件替代首轮记录。

七场景重跑 `2026-10-19T15-00-43-153Z-7` 已取得 portal **15/15**：包括明确重新登录后旧退出页回到工作空间而不误报已退出。该结果不改写首轮新登录 503。Nextcloud 来源本轮再次卡在 Immich 登录前置；其余仍进行中。两套目录的隔离已只读核实：主栈为宿主网络命名空间、FINANCE.HLONG.WANG 和 `/home/whl/anas-finance/data/samba_dc/var`；旧栈为另一网络命名空间、NAS.TEST 和独立旧 workspace 数据目录。`extra-stack-stop-plan.json` 仅准备停止十四个原运行容器及两个专用守护进程的步骤，尚未获批或执行，不将其当成已清理。

本轮驱动语法、808 项需求/活动用例溯源、需求/计划/文档状态及 diff 检查通过；包含上述首轮记录的文档构建 17.07 秒通过。这些门禁不替代失败的实机验收。


### 21.7 原生退出 CSRF 竞态修复与独立部署

七场景重跑 `2026-10-19T15-00-43-153Z-7` 自然结束，3 通过、4 失败，CLI 返回 1。通过为 portal 15/15、portal-after-central-cookie-loss 15/15、portal-partial 20/20。Nextcloud 正常来源和无门户来源卡在 Immich 原生登录回调；Nextcloud 旧会话来源在退出前，门户 503（12.215 秒），Nextcloud 与 Immich 各 15 秒超时，IAM 虽为正确身份但也耗时 11.98 秒。不能把这些未就绪状态记作会话被撤销或退出成功。

本轮新确认的功能缺陷发生在 Immich 正常退出之后：原生自动回调已确认，随后 UI 收到 `csrf_rejected`，未进入联动完成。调用链可复现：bootstrap 取到已有会话的 CSRF，原生 IAM 后台通知随后撤销该会话，紧接的 complete 请求必须拒绝旧 CSRF。修复仅在退出 API **明确返回 403 csrf_rejected**、尚未执行该请求时，刷新匿名 CSRF 并重试一次；第二次拒绝、5xx 或网络未知均不重放，资料、密码、建号和分组操作不变。没有新接口、Cookie、持久凭据、服务或 Casdoor 补丁。

修复前四项界面反例 2/4 通过（恢复及重复拒绝边界失败）；修复后完整 Chrome 界面 **29/29**、15.00 秒；门户 Go/race **11.116 秒**通过。新增服务器测试以真实 Store 撤销到 HTTP bootstrap/complete 的调用链确认：旧 CSRF 403、新匿名 CSRF 可完成，业务目录仍 401，另一独立 SID 保留。测试代码及用例溯源均纳入既有 T-001/T-006/T-010，没有新增产品验收范围。

生产源码逐文件对比 42 项，仅 `modules/user_portal/portal/ui/app.js` 改变；候选 JS `fd88b4594c598821e28279d5cee82e9b09eafff99819ce9f7efd733070968619`，Go 1.26.6 构建 Linux/amd64 二进制 `17985ea3587f4fe95517f4f8eb96adaf83ec4eadc5c2a068d018ec285c3c2b06`。finance 候选镜像 **`sha256:b0cd860e749f7997da94bd4f81b20fa81c7da056078a856d0fbcb309da53e574`** 已核对镜像内二进制和继承运行设置。首次 BuildKit 将裸 `sha256:…` 解释为 Docker Hub 仓库名而失败，改用校验当前 image ID 的本地标签后成功；失败日志与修订 Dockerfile 摘要均保留，不当作基础镜像发布问题或源码失败。

原生 lock/render 得到 **`20261019T160108Z-826b9557`**，只改变 user_portal。初次部署前置检查误用 `config_digest` 字段而在 apply 前失败；核对固定 CLI 的实际 `config_fingerprint` 后修正。配置指纹、受保护 settings、全部模块版本/revision/app_version 均相同，故依据原生 `deploymentSnapshotTrigger` 和 `snapshotBeforeApply` 规则，本次只是可回退的容器镜像替换，使用默认 apply，未传 --snapshot 或 --no-snapshot 覆盖参数。此前用户要求的新完整快照已经由 `20261019T110522Z-7115429c` 完成并 verify，data 与 userdata 全覆盖；保留全部九份恢复点和原门户 image ID。本段纯前端修复没有额外的整栈数据迁移快照要求；前文新增全栈快照的空间预检不作为其发布阻塞。

新部署已激活且健康：原镜像 3c137cac 仍可用于回退；其余 **31 个容器**的 ID、镜像、配置、挂载、启动时间及重启数均一致，九个恢复点元数据完全一致。回执 `evidence/csrf-candidate-activation.json`；未发布 registry 或 Git 提交。

针对新制品增加了无账号的真实后端回归：`native-csrf-2026-10-19T16-21-02-132Z`，Chrome 155.0.8059.80，默认协议（未禁用 HTTP/2），**7/7**。从同站点真实 JSON 文档导航到原生匿名回调，确认实际服务的 JS 摘要为 fd88b459；先用原生 complete 请求令待处理匿名会话失效，再继续原浏览器请求，实际收到 **403 → 200**，只刷新重试一次。没有伪造响应、Cookie 或认证状态。三目标均 Signed out、匿名业务目录 401、返回主页成功。该回归验证真实会话失效恢复，**不能替代认证用户、旧应用会话和另一设备的完整 T-010**；先前 b392c642 的正例和失败均保持原基线，M13/M11 不归档。

### 21.8 临时环境清理与根盘净回收

LN 两轮公开证据先回存 finance `browser-before-client-cleanup.tar.gz`：2496487 bytes、241 个逐文件校验条目，SHA256 `725cfd608d60c20fcf67686b41f0ef62fff23d76c1eaa7325aebeaebce3a068a`；已知测试密码、JWT 和私钥模式检查通过。首轮及重跑的原始报告、截图、日志与实际驱动全部保留。LN 新镜像引用、专用网络和 0700 临时根目录（含唯一转移的 manager 密码、私有基线）已删除，临时容器已自然退出/自动删除；原 **43 个容器**配置及启动状态完全一致。没有向 LN 留存 finance 备份。

finance 前缀 pua05799dfc 的 **12 个目录对象**已删除，剩余 owned 对象 0；七个用户名均无有效 IAM 记录（六个停用/删除记录、一个不存在）。原有 **67 个身份/权限/锚点对象**与基线完全相同，E2E 期间 32 个容器不变、九个恢复点元数据不变。正式四个受限绑定保留，未授予任何测试域权限。finance 上该批测试密码副本已移除；Nextcloud/Immich 派生用户资料按原生保留规则处理，没有直接删业务数据库行。回执 `final-cleanup.json` 与 `ln-client-cleanup.json`。

后台原生去重复用此前通过的 xfs_io/kernel 等字节校验，未删除恢复点、归档、镜像或容器。Oct15 归档逻辑处理 975368192 bytes，Oct19 归档 2350342144 bytes；两目标完成后分别与原生清单全文件 SHA256 一致：`a17730bef9c55393267226126e3a326e5b4850075c570b971c11ccaa7a1944f6`、`257bf365ce32e092d75c0107225e93b290560f45f55c440dfe86e934f1eb8a96`。使用已有清单作预期摘要、内核逐块比较，之后全文件重读验证；没有声称额外执行了独立前置全文件重读或只读来源的重复后哈希。进程采用 idle I/O、低 CPU 优先级，并及时丢弃归档读缓存以减少维护负载。

操作期间根盘可用空间从 **9356562432** 到 **10428088320 bytes**，净增加约 1.0 GiB；不能把 3.325 GB 逻辑处理量当成物理回收量。额外全栈快照的 12193914368-byte 预检门槛仍未满足，没有降低该门槛去创建新全栈快照。旧 SDK 重复文件与额外内容 blob 预检均没有找到可共享量；未据 Docker 逻辑回收估计删除镜像或卷。镜像只读探针仅检查了当前制品目录的直接引用，未完整解析继承制品引用，**不能作镜像删除清单**。

本段收尾时，旧 NAS.TEST 栈的停机范围尚待确认；后续用户授权、端口归属更正与实际清理见 §21.9。确认的性能因素是小内存主机上的活跃换页、I/O 等待及两套栈的额外容量负担，维护负载会放大等待。当前 b0cd860e 制品仍须重新完成认证会话全套，不能用匿名回归或本机门禁关闭原整套失败。


### 21.9 旧 NAS.TEST 栈停机、数据清理与端口归属更正

用户明确授权“停止9000，删除数据”。进一步核对发现：旧 `anas-casdoor-test-20261003` 栈的 9000 发布在独立网络命名空间内；**宿主机 9000 属于另一套 `feng` 项目的 Traefik**（`/home/whl/docker/feng/docker-compose.yml`）。前文将两者统称旧端口9000栈不准确。已向用户说明，用户另行明确选择保留 feng、仅清理旧 ANAS 栈；不停止或删除 feng、其他项目或当前10000栈。

旧引擎十四个运行容器全部停止；十五个容器定义的原生删除命令超过60秒，原始超时保留。随后原生停止两个专用 transient systemd 单元，确认旧命名空间进程与旧目录挂载均为0；未强制重启宿主或主Docker。比较脚本首次因 Mounts 数组顺序变化失败，按挂载目标排序复核后，主引擎三十二个容器的 ID、镜像、配置、挂载、启动时间和重启数完全一致。

整份旧workspace删除被自动审批拒绝，理由是未证明其中没有需保留的配置或验收证据。采用更窄方案：先只读核实无主引擎、其他专用引擎、进程或挂载引用，再仅删除旧根下 `docker`、`containerd`、`workspace/data` 和空 `workspace/userdata`。保留旧workspace配置/快照、LLNG配置、源代码与历史验收记录；当前九份恢复点全部保留。后续不据已停止状态宣称数据已删完，最终以 `evidence/old-9000-delete.json` complete 回执和目录缺失检查为准。

停机前16:33:29Z五秒采样：换入约9.70MB/s、换出1.08MB/s，内存/I/O some avg10为25.22%/58.92%。16:39:24Z停机后、数据删除前同样五秒采样：换入约17.2KB/s、换出0，等待降至1.60%/5.09%，swap空闲增加约1.29GiB。显示旧栈停机后资源压力明显下降，但前置目录扫描与停机本身也会影响短窗样本，不能据两点观测断言唯一根因。删除进程已确认仅打开旧containerd目录并降至nice15、idle I/O；最终稳定性和全套登录验收仍待结果。


本轮重新准备前缀 `puaace36ee5` 的七个临时人员，无域级权限；生成默认邮箱及五个可登录IAM身份已就绪，严格TLS bootstrap 200、76ms。仅一个 manager 需要进入浏览器，其余密码和生产绑定均不需离开finance。向当前工作站导出新一批凭据被自动审批拒绝，旧的一次性 pua05799dfc→LN 授权不覆盖此操作；已请求用户仅确认 `puaace36ee5manager`→本机0700/0600私有目录的一次性转移，答复前不执行导出或依赖该密码的测试。旧数据清理与无凭据的文档/溯源验证继续。


用户随后明确授权本轮测试密码转移；仅导出 `puaace36ee5manager` 一个普通ANAS管理员的测试输入到当前工作站，不包含其他六个密码、生产绑定或LN目的地。先前拒绝记录保留，授权阻塞解除；验收结束须移除这一个本机凭据及全部本轮目录对象。


清理最终回执 `old-9000-delete.json` 为 complete：四个窄范围目录已删除，旧命名空间已移除，主引擎32个容器按排序后的挂载比较全部一致，九份当前恢复点元数据一致；feng保留。长文件清理改为具名后台任务（`old-9000-delete-resume.json`）继续同一范围，原SSH退出255为有记录的主动交接，不当作宿主失联。根盘可用从停机前10412609536增至19178225664 bytes，**净增8765616128 bytes（约8.16GiB）**；不能把去重逻辑字节或镜像逻辑大小算入此净值。

17:03:13Z、开始浏览器验收期间五秒采样：memory/io some avg10为1.87%/7.91%，磁盘busy24.3%；换入293218、换出560564B/s，仍有活跃换页，不能宣布完全无压力。相较停机前25.22%/58.92%明显下降；不同短窗和正在进行的Web请求限制了因果结论。`post-maintenance-host-diagnostic.json`保留进程、磁盘及压力原始值。

新完整T-010运行 `2026-10-19T17-02-46-826Z-85869` 已启动，当前b0cd860e镜像、同一d99346c9驱动、Chrome155.0.8059.80、正常域名、默认协议。中英分类8/8已通过，认证退出场景仍执行中；不提前记录整套通过。

另修正T-007过时的退出等待语句：该套件原本仅放行门户和IAM请求，因此其他应用帧可能诚实显示未确认；等待明确的IAM Signed out、门户401、返回主页和原生重新登录，不再等待已被新UI替代的旧提示语。完整跨应用成败仍由T-010检查，未放松它的全部退出/旧Bearer/其他浏览器断言。测试代码与用例说明同步，源码及制品不变；该修订T-007尚未实机执行，不将语法或溯源生成通过当成账号回归通过。


主题现状与建议分开记录：当前实现为浅色背景、白色面板和蓝色主操作，已有统一控件高度、同源下拉箭头和窄屏布局；没有主题切换、系统深色跟随或继承IAM主题。颜色大多硬编码，后续维护会有样式分散问题；Casdoor原生登录页仍使用自身品牌配置。这些属于设计债务，不能把未承诺的深色主题列为本轮失败。建议后续先集中颜色和间距变量，再另行确认深色范围；本轮不增加主题依赖或修改Casdoor源码。


### 21.10 当前镜像完整联动退出通过

正常域名/默认协议 Chrome155.0.8059.80，运行 `2026-10-19T17-02-46-826Z-85869`，17:02:46—17:22:57Z；当前活动部署 `20261019T160108Z-826b9557`、门户镜像b0cd860e、JS fd88b459，实际驱动d99346c9。完整十场景 **10/10、148/148**，CLI退出0；原始报告SHA256 `b51f69fd3f1acf2c09da135dd0763a26c03196a8061bcb13f0f6039b1cb3007f`。没有通过HTTP/1诊断选项绕过本轮默认协议验证，没有修改Casdoor源码。旧基线首轮及重跑失败仍保留。

| 场景 | 断言 | 结果 |
| --- | --- | --- |
| 双语分类 | 8 | 通过 |
| 门户来源 | 15 | 通过；含重新登录后旧退出页不误报 |
| Nextcloud来源 | 14 | 通过 |
| Immich来源 | 14 | 通过；不再出现先前CSRF竞态中断 |
| 门户旧IAM会话 | 15 | 通过 |
| 门户部分失败、刷新与重试 | 20 | 通过；故障时旧Immich仍有效，恢复后旧Bearer被拒绝 |
| Nextcloud旧IAM会话 | 16 | 通过 |
| Immich旧IAM会话 | 16 | 通过 |
| Nextcloud从未打开门户 | 15 | 通过 |
| Immich从未打开门户 | 15 | 通过 |

三种来源均以真实四方受保护API、旧Immich Bearer401、刷新不恢复和另一浏览器四方保留验证。Nextcloud/Immich来源为从应用页面导航原生退出路由，不扩张为菜单按钮接线验证。另一浏览器跨场景不重新登录；超时或5xx不当作退出成功。该套件代理只允许四个测试域，IAM外部CDN装饰图标被阻断，不将这些预期拦截说成真实用户网络故障。保存的API探针最大5.594秒，本轮没有先前15秒身份超时；这不是长期性能基准或唯一根因证明。

R-078/079/081已取得本轮完整真实会话证据；R-080/082契约及匿名CSRF7/7、Go/race和29项UI证据另见§21.7。先完成T-007完整账号回归，再统一清理七个目录人员/额外建号、移除本机与finance临时凭据、回存逐项校验的最终证据；在清理/保全回执前不归档计划。全Module/Provider矩阵仍归其原计划，不将四方通过扩张为该矩阵全通过。


### 21.11 账号107项与原生菜单30项最终通过

T-007 在 finance 同机一次性工具容器执行，固定 Playwright1.62.1、Chromium151.0.7922.34，报告时间17:40:42—17:48:31Z，外层执行17:40:35—17:48:40Z。**107/107、CLI退出0**。实际驱动SHA256 `44565f98ef7c1c45a371fda7b36791b4dee999fec11a834b499d6c284e5ab676`，报告 `77371956e67353993eba3d2b27b57a818b0d4120dad0cc95f2f54e906d26a964`。未修改被测制品；七个测试人员密码均留在finance，之前单独授权到Mac的manager仅用于T-010。

逐项覆盖资料姓名、限定OU建号/系统主邮箱、改密/受限恢复/错误与弱密码拒绝、普通组即时操作、管理员角色完整参数确认、过时及并发成员冲突、自保、真实300秒认证过期403及重新认证、保护账号逐次验证与原生ACL拒绝、审计不含秘密。中英1440及中文390截图留存，中文390管理页已人工核对；高级组在底部默认折叠，组介绍、按钮和下拉布局正常。T-007仅放行门户/IAM，其他RP退出受阻会如实显示未确认；它不替代T-010跨应用断言。域级成功/创建者所有权转移继续引用§13/§15的隔离假域原生证据，没有在finance授予临时账号域权限，也未取消原判据。

补充真实菜单接线：在Nextcloud点击Settings menu→Log out（先关闭新用户欢迎层），Immich点击头像→Sign Out链接；不是直接导航退出URL。最终组合运行 `2026-10-19T17-46-14-081Z-324`，17:46:14—17:50:07Z，默认协议Chrome155，**2/2、30/30、CLI退出0**。驱动 `739a8856bf15a087fdf1d3fe8c5ba67808920b42b08ff8d29099945c1c60640d`，报告 `5fbf76adcd7abe2b3637249fec63923c917691a691a17111efa7224c5ba7b556`。二者各15项均核对四方API未认证、旧Immich Bearer401、刷新不恢复、另一浏览器四方保留和返回主页。

此前17:28菜单运行因按钮名称/链接文本定位错误失败；17:36运行Immich通过15项、Nextcloud被欢迎层挡住；17:41 Nextcloud单独通过15项。这些测试定位失败和实际驱动副本保留，不伪装成产品已通过或从最终结果删除。默认十场景148项实际运行驱动为d99346c9，最终菜单30项为739a8856；后者只补可选菜单入口、准确DOM选择器及参数检查，不声称十场景已在新驱动重跑。

| 需求 ID | 最终结果及依据 |
| --- | --- |
| PORTAL-R-075 | 通过：T-007真实5分钟窗口与过期拒绝、角色绑定确认/自保、系统/保护账号逐次本人验证；域级写成功沿用未变的隔离原生基线 |
| PORTAL-R-076 | 通过：T-007和T-010成功/未确认/部分失败均可回主页，失败可重试 |
| PORTAL-R-077 | 通过：T-010中英分类8项、两大组同时平铺/恢复、顺序和浏览器偏好保持；29项UI已通过 |
| PORTAL-R-078 | 通过：三种来源、旧IAM父会话及从未打开门户，四方API和旧Immich Bearer均明确未认证；含真实菜单 |
| PORTAL-R-079 | 通过：同一账号另一独立浏览器跨场景不重新登录，四方会话保留 |
| PORTAL-R-080 | 通过：最小固定退出协议清单契约；业务目录/资产继续认证过滤，详见§21.7 |
| PORTAL-R-081 | 通过：故意阻断应用退出得到部分失败，旧会话仍有效；刷新保留重试、恢复后清除，不把超时/5xx当成功 |
| PORTAL-R-082 | 通过：匿名固定回调契约与真实服务7/7，403 csrf_rejected仅重取匿名CSRF并重试一次；其他错误和业务写不自动重放 |

M1—M9的既有逐项通过基线保留；T-007对未变的账号能力作整套复验。未新增Casdoor上游补丁、身份数据库、密码缓存或长期测试服务。邮件后端PoC、全Module/Provider登出矩阵、系统深色主题仍各自另议，不扩张本轮通过范围。

### 21.12 清理、证据与不变性核对

finance `evidence/post9000-fixture-cleanup.json`：删除12个带本轮标签的目录对象及门户新建人员，残留所属对象0；核对8个IAM测试名，活动数0（7个已禁用/删除、1个不存在）。原67个人员/安全/anchor对象精确不变；32个现有容器ID、镜像、Config、HostConfig、排序后挂载、启动时间及重启数不变；九份恢复点元数据不变。门户二进制仍为 `17985ea3587f4fe95517f4f8eb96adaf83ec4eadc5c2a068d018ec285c3c2b06`，四个受检目录/门户/同步服务healthy。finance本轮已知明文测试凭据副本0，四个生产受限绑定保留。Nextcloud/Immich业务侧测试资料遵循原生保留规则，未直接删业务库行，不声称所有应用数据已物理清空。

finance `evidence/finance-account-tool-cleanup.json`：一次性容器、此次新拉取的官方Playwright镜像引用、展开的依赖和传输缓存、空私有输入目录均移除；公共源码包和证据保留。镜像manifest `c091b21d9fae78c76e85cd4356431e9b018402f172a214fc7d7a5e9a7e29d8ac` 和linux/amd64 config/layers已核对；Docker29将manifest作Image.Id，与旧引擎config ID不同的首次预检失败保留，未误报镜像内容不一致。工具清理后32容器仍不变，17:54:36Z根盘可用19003592704 bytes。旧NAS.TEST清理净增加约8.16GiB的结论见§21.9；feng保留。

持久证据根为 `finance:/home/whl/anas-portal-browser-logout-20261019/`，不是仅本机忽略目录：

| 制品 | SHA256 | 保全核对 |
| --- | --- | --- |
| `browser-final-acceptance.tar.gz` | `e1d460704ad46c21271dcc092c304df6faf55927a2c1c4013d51dcf6a7a7550a` | 4969725 bytes、318文件逐项SHA校验；完整148、菜单30、匿名7、账号107、截图、历史重试和实际驱动副本；上传前已扫描本机持有的旧/新测试密码及令牌模式 |
| `account-browser-public.tar.gz` | `98dc7602af17ed301048aa39d64a4a79f08dbe08cdbf730f23bd15a580612184` | 748947 bytes、9文件逐项SHA；在finance以全部8个已知测试密码及令牌模式扫描后才导出公共报告 |
| `account-tool-public/public.tar.gz` | `793c6603586aace41306137fc1e93e16019360d205600083d78a2c92578bc1a2` | 4025197 bytes；固定Playwright依赖和实际账号驱动，保留复现输入，不含私有配置 |

内部容器/进程/配置诊断留在finance。最终批量导出诊断被自动审批拒绝后没有重试导出，改为仅返回清理计数、通过状态、归档和回执摘要；不导出内部详细记录。此前各阶段独立公共归档见§21历史记录，未覆盖旧失败或声称LN是本轮新增归档地。最终本机凭据清理、精简核对回执及门禁日志另存同一finance证据根下，见§21.13。


### 21.13 最终收口与门禁

本轮最终独立核对只传回计数及摘要：所属测试对象0、活动IAM测试身份0、finance测试秘密副本0，原67目录对象/32容器/9恢复点不变，一次性浏览器工具已清理；根盘可用 **19120443392 bytes（约17.81GiB）**。原始回执仍在finance，SHA256：

- `post9000-fixture-cleanup.json`：`3aebbd36135f5b1b4428b095148743408eb8c3f722722fe84aaa96dede949797`。
- `finance-account-tool-cleanup.json`：`9f42b4c11708fdc8e9411a5545d7a30931919bf30cff2d41b0af213a70a296f8`。
- `old-9000-delete.json`：`90b732f9185fca132cbdf97e3ef88f79266bb54bce453bbf5b219f4a1a3279ef`。
- `post-account-cleanup-host-diagnostic.json`：`79a4480888042e01e7c9042a7fa833aa6810a0958396582647167c82cc32c816`；详细内部采样未另导出。

确认最终318文件归档在finance逐项验证后，才移除Mac两批私有浏览器输入中的密码字段；8个已知旧/新测试密码在本任务三个私有目录扫描残留0。没有修改生产绑定；本任务浏览器、驱动和SSH隧道残留0，不结束用户Chrome或其他隧道。精简回执为 `final-minimal-verification.json`、`local-test-credential-cleanup.json` 和 `local-test-process-cleanup.json`。

稳定行为已归入模块中英文技术文档和门户设计；本计划完成后移入archived，需求保留为回归判据。历史按时点记录的失败、未执行和待授权状态保留，由本节最终结论收口；不把这些旧时点文字当成当前阻塞。所有适用PORTAL条目已通过；原APPCAT、全Provider/Module双向登出和邮件后端PoC保持各自计划状态，未被本次归档关闭。本轮未执行Git提交或推送。

最终门禁日志、精简回执、实际三份浏览器驱动和归档计划副本封存于同一finance证据根的 `final-closeout/`；逐文件摘要在 `final-closeout/manifest.json`。最终九个检查均退出0：`docs:check-requirements`（含71个活动用例溯源）、`docs:check-requirement-status`、`docs:check-plan-status`、`docs:check-status`、`docs:build`、三份浏览器驱动的`node --check`及`git diff --check`。根索引已通过两条生成命令再生；门户私有需求索引用同一`documentStatus/renderIndex`生成库计算为72/72，门户计划索引为已归档。没有因本轮收口扩张其他在研主题的完成状态。此前六包Go测试、门户race及29项UI通过后未再修改生产代码，不重复以本机测试冒充实机验收。
