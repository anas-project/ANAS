---
doc_type: research
created: 2026-10-03
updated: 2026-10-03
evidence_as_of: 2026-10-03
---

# Casdoor 应用目录与权限过滤

结论：固定 Casdoor `3.143.0` 有应用卡片和 Application Permission，可提供名称、图标、入口、
描述、顺序及组过滤。完整的多尺寸图标、简介/详情、文档地址、展示分类列表仍需扩展。
配置原生字段不能独立完成目标；本页记录调研与实验，不表示新目录协议已实现。

## 1. 固定基线与资料

源码输入取当前 Module Dockerfile：提交
[`1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e`](https://github.com/casdoor/casdoor/tree/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e)，
归档 SHA-256 `365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460`。
探针按现有构建顺序应用仓库 0001—0006 补丁，然后实际执行该树中的对象查询与权限函数。
网页文档是 2026-10-03 的说明，不能替代固定版本源码。

官方文档给出的能力：

- [SSO 门户](https://casdoor.ai/docs/session/single-sign-on/)描述组织应用卡片及点击后追加
  `silentSignin=1`；普通组织用户与 built-in 全局管理员的首页行为不同。
- [权限概览](https://casdoor.ai/docs/permission/overview/)说明默认组织访问与 Permission；
  [权限配置](https://casdoor.ai/docs/permission/permission-configuration/)说明用户、角色、组可组合。
- [应用分类](https://casdoor.ai/docs/application/categories/)的 Default/Agent 用于应用及协议类型，
  不是办公、媒体等门户分类。
- [应用 Tags](https://casdoor.ai/docs/application/tags/)还用于登录准入，管理员存在豁免；
  不适合承载与权限独立的展示分类。

官方权限配置页同时有“Resource type 不用于外部应用认证”的概括；固定源码的
`CheckLoginPermission` 明确要求 `ResourceType == "Application"`，本报告以已运行的固定源码为准。
不得据通用文档把字段删除，也不能把 Casbin 的全部表达能力等同于 ANAS 已验证支持。

## 2. 实验方法与结果

复现入口：

```bash
bash test-env/scripts/test-casdoor-catalog-source.sh /tmp/anas-casdoor-fixed-source-20261003.tar.gz
# 不传归档时下载 Dockerfile 固定版本，并校验同一摘要。
```

脚本在全新临时源码目录运行，用内存 SQLite、Casdoor 的 User/Role/Permission 模型、
XORM policy adapter 和真实 Casbin enforcer；不修改部署数据库，不创建生产账号。
测试模板位于仓库 `test-env/fixtures/casdoor-catalog-probe/catalog_test.go.in`。
同时断言 `CheckLoginPermission` 与 `GetAllowedApplications` 的结果，并单独验证原生 enforcer，
不自行实现一个假的过滤器。

2026-10-03 在本机完成 **12/12 子场景**，`counts_as_e2e=false`：

| 场景 | 观察结果 | 对设计的影响 |
| --- | --- | --- |
| 无 Permission + 无 clientId + DisableSignin 的链接条目 | 正常被组织查询选出，普通用户可见 | 目录查询不要求真实 OAuth 客户端；未配置权限默认开放 |
| 仅允许指定用户 | 命中用户可见，另一用户不可见 | 原生可按用户过滤，但第一版不采用可变用户名做持久规则 |
| 仅允许指定组 | 命中组可见，无关组不可见 | 可作为目录的基础过滤 |
| 角色直接包含用户 | 命中角色可见 | 原生角色可用，仍不等于任意角色配置均已验收 |
| 角色包含组 | 组成员通过角色可见 | 语义角色可映射到目录组 |
| 两个允许组，用户仅命中其一 | 可见 | 原生允许组为 OR |
| 同时命中 Allow 与 Deny | 普通用户拒绝，组织管理员仍允许 | 原生管理员绕过不能冒充严格目录规则 |
| 原允许策略停用或变为 Pending | 原被拒用户重新允许 | 不能把策略缺失/停用解释为默认拒绝 |
| 删除用户组及其 enforcer 关系 | 下一次权限/列表查询不再返回 | 支持撤组后重查；不证明目录事件传播延迟或 Cookie 撤销 |
| 未匹配 Tags 且 DisableSignin=true | 目录函数仍返回条目 | 标签登录限制与隐藏卡片不同，需独立 hidden 字段 |
| 未登录 userId 为空 | 列表函数返回错误 | 目录 API 必须继续要求有效登录 |
| 直接调用原生 Permission enforcer 的组规则 | 组成员允许，非成员及没有该组的 IsAdmin 用户均拒绝 | 可复用现有引擎实现严格过滤，无需另建权限解释器 |

首次执行因沙箱不能写 Go 缓存而失败，授权后重跑；初始 fixture 缺少 ThirdPartyLink 表，
补齐真实依赖表后上述场景全绿。最终日志为本机 `/tmp/anas-casdoor-catalog-probe-20261003.log`，
脚本会打印每轮保留的源码目录，日志不包含部署凭据。

**验证限制**：没有启动 HTTP server、执行浏览器或接入真实 LDAP；没有验证管理员目录 API、
分页、未知/已删除用户、角色停用、跨组织行为、图标 HTTP 服务、真实 OIDC token 拒发或外部网站访问。
尤其是无 clientId 的条目“能被列出”不代表所有 OAuth/SAML 入口都已经禁止签发。
新协议安全语义和真实部署必须另行验收，不能把本实验记成产品 E2E。

## 3. 固定源码字段与调用链

主要证据是
[Application 模型](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/object/application.go)、
[目录过滤](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/object/application_util.go)、
[登录权限检查](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/object/check.go)、
[目录 controller](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/controllers/application.go)。
组织列表 controller 调用 GetAllowedApplications，后者调用 CheckLoginPermission；
缺少有效 Allow/Deny 会落到允许，IsAdmin 会提前返回。

| 需求信息 | 固定模型/页面现状 |
| --- | --- |
| 稳定名、显示名 | Name / DisplayName；均存在 |
| 图标 | 单个 Logo URL；Favicon 是登录页面图标，不能当成应用多尺寸列表 |
| 链接 | HomepageUrl；当前数据库声明 varchar(100)，长链接需要扩展或由目录制品提供 |
| 简介和描述 | 只有 Description，卡片直接显示，没有独立短/长文案 |
| 文档地址 | 没有本协议对应的专用字段/卡片入口 |
| 顺序权重 | Order，前端按升序排序；需要补稳定的同权重次序 |
| 分类列表 | 原生页面用 Tags 做筛选；Category 用于协议分类，两者均不可直接复用 |
| 隐藏 | DisableSignin 不隐藏目录；必须另外表达 |

前端
[AppListPage](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/web/src/basic/AppListPage.js)
使用接口返回条目、Order、Tags、DisplayName、Description、Logo；
[SingleCard](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/web/src/basic/SingleCard.js)
直接拼接 silentSignin 参数，包含 fragment 时还可能把参数追加到片段之后。
这些是源码审阅结果，没有把前端构建或浏览器行为记为已实测。

## 4. 选项与建议

1. 只填原生 Application 字段：成本最低，但无法完整提供用户要求的全部信息，且原生列表
   默认权限和 URL 改写不符合新协议。只能作为底层能力参考。
2. 扩展现有 Casdoor Module：复用会话与权限引擎，读取只读目录制品，补 API 和页面。
   无需另建服务或元数据库；代价是前后端受控补丁、前端构建和随上游升级复验。推荐，待确认。
3. 新建独立门户：可以独立控制 UI，但增加服务、身份接入和接口运维；当前需求不要求独立部署，
   不作为默认方案。

共享图片可挂到 Casdoor 的现有 `/files` 静态目录子路径，来源为
[main.go](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/main.go)。
目录 JSON 必须留在非静态路径并经登录 API 过滤。该挂载和 URL 方案仍需真实容器验证。

第一版采用正向组过滤和映射到组的 platform_admin，避免把可变用户名、用户标签、
任意原生角色、Deny 混合和自定义 Casbin 脚本同时纳入跨 Provider 协议。
LLNG 新协议适配尚未试验；Authentik 按用户决定逐步弃用，不为其限制缩减新协议。
具体字段和共享机制见[设计](/architecture/app-catalog-design)。
