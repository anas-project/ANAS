---
doc_type: plan
status: in_progress
created: 2026-10-11
updated: 2026-10-14
---

# 独立用户门户实施计划

**状态：审查修复已实现；计划重新打开，修复后正式镜像的 finance 复验待执行。** 历史 41/41、95/95 通过基线保留在 §9。
规范来源为 [门户要求](../requirements/user-portal.md) PORTAL-R-001—R-041，
实现机制见 [独立用户门户设计](../../../../docs/architecture/user-portal-design.md)。
原应用目录 APPCAT-R-001—R-039 仍归 [应用目录计划](../../../../dev-docs/plans/app-catalog.md)；
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
| M1：登录、授权目录与本人资料 | R-001—R-005、R-007—R-010、R-013、R-041 | 历史完成；审查后复验见 §10 |
| M2：本人改密与写入基础 | R-016、R-018—R-020、R-025—R-027、R-032—R-039 | 历史完成；审查后复验见 §10 |
| M3：委派用户与分组管理 | R-006、R-011、R-012、R-014、R-015、R-017、R-021—R-024、R-028—R-031、R-040 | 历史完成；审查后复验见 §10 |

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

入口见 test-env/cases/user-portal/cases.yml；具体断言和源码/镜像基线见 §8。
运行基线分开记录，不能把中途版本的通过自动复制给后来修改的制品。

| 需求 ID | 脚本 | 环境 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-001 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-002 | T-001 unit/contract, T-002 browser, T-004 boundary | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-003 | T-001 unit/contract, T-002 browser, T-004 boundary | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-006 | T-001 unit/contract, T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-007 | T-001 unit/contract, T-002 browser, T-003 faults | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-008 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-010 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-011 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-014 | T-001 unit/contract, T-002 browser, T-004 boundary | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-015 | T-001 unit/contract, T-002 browser, T-004 boundary | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-016 | T-001 unit/contract, T-005 native LDAP | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-017 | T-002 browser, T-005 native LDAP | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-018 | T-001 unit/contract, T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-019 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-020 | T-002 browser, T-003 faults | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-021 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-022 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-023 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-024 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-026 | T-003 faults | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-027 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-028 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-029 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-030 | T-002 browser, T-004 boundary | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-031 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-033 | T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-035 | T-001 unit/contract, T-003 audit contract, T-003 faults | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-036 | T-003 audit contract, T-003 faults | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-037 | T-001 unit/contract, T-003 faults | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-038 | T-001 unit/contract, T-003 faults | finance 正式镜像，§9 | 2026-10-14 | 通过 |
| R-041 | T-001 unit/contract, T-002 browser | finance 正式镜像，§9 | 2026-10-14 | 通过 |


### 4.3 单元与契约验收记录

| 需求 ID | 验证入口 | 证据基线 | 日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-004 | T-001 unit/contract, T-002 browser | 本机单元与 finance 契约，§9 | 2026-10-14 | 通过 |
| R-005 | T-001 unit/contract, T-002 browser | 本机单元与 finance 契约，§9 | 2026-10-14 | 通过 |
| R-009 | T-002 browser | 本机单元与 finance 契约，§9 | 2026-10-14 | 通过 |
| R-012 | T-001 unit/contract | 本机 race 单元；运行源码一致，§9 | 2026-10-14 | 通过 |
| R-013 | T-001 unit/contract, T-002 browser | 本机单元与 finance 契约，§9 | 2026-10-14 | 通过 |
| R-025 | T-001 unit/contract, T-002 browser, T-003 audit contract | 本机单元与 finance 契约，§9 | 2026-10-14 | 通过 |
| R-032 | T-002 browser, T-003 audit contract | 本机单元与 finance 契约，§9 | 2026-10-14 | 通过 |
| R-034 | T-002 browser, T-003 audit contract | 本机单元与 finance 契约，§9 | 2026-10-14 | 通过 |
| R-039 | T-002 browser, T-003 faults | 本机单元与 finance 契约，§9 | 2026-10-14 | 通过 |
| R-040 | T-001 unit/contract, T-002 browser | 本机单元与 finance 契约，§9 | 2026-10-14 | 通过 |

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
