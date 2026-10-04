---
doc_type: research
created: 2026-10-03
updated: 2026-10-03
evidence_as_of: 2026-10-03
---

# Casdoor 目录主体标识符可行性

结论：固定 `3.143.0` 不能仅靠配置，使 OIDC token、UserInfo、Logout Token 与 SAML `NameID`
全部取 `ExternalId`。需要一个共用取值函数和签发路径补丁。产品尚未发布，直接采用目录永久锚点，
无需旧账号迁移、历史兼容或 `sub ↔ anchor` 映射。

本页记录上游能力核实和源码验证；r10 已将验证后的补丁接入构建，**不是已部署行为或发布验收**。目录事件会话撤销的方案见
[Directory event journal](/architecture/directory-event-journal#casdoor-session-revocation-proposal)。

## 固定输入与复现

输入来自 Module Dockerfile：提交 `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e`，归档 SHA-256 为
`365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460`。先应用当前镜像的四个
受控补丁，再调用该源码自身的函数；使用隔离内存 SQLite 和临时 RSA 密钥，不连接部署。

在仓库根执行：

```bash
bash test-env/scripts/test-casdoor-identity-source.sh
# 也可提供已下载的同一归档，脚本仍校验 SHA-256：
bash test-env/scripts/test-casdoor-identity-source.sh /tmp/casdoor-source.tar.gz
```

需要 Bash、Go、curl、patch、tar 和 SHA-256 工具。Go 按固定上游 `go.mod` 下载依赖；SQLite 是
上游已有的测试依赖，没有增加 Module 运行时依赖。脚本保留临时源码便于复核，并输出
`counts_as_e2e=false`。测试模板在 `test-env/` 下；生产补丁 0005/0006 已接入 Dockerfile。

## 配置能力的实际边界

| 路径 | 源码探针结果 | 实施含义 |
| --- | --- | --- |
| JWT-Custom `tokenAttributes` | `sub → Existing Field/ExternalId` 可以覆盖已注册的 `sub` | 仅能改该 token 路径 |
| JWT-Custom 缺锚点 | 得到空 `sub`，没有报错 | 需要在签发前显式拒绝 |
| UserInfo | `Sub` 固定取 `user.Id` | token 配置不能统一它 |
| Logout Token | 当前受控补丁的 claims 仍取 `user.Id` | 须与登录主体标识符一起改 |
| SAML 2.0 `UseEmailAsSamlNameId=false` | `NameID` 为 `user.Name` | 改名会变化 |
| SAML 2.0 `UseEmailAsSamlNameId=true` | `NameID` 为 `user.Email` | 邮箱变更会变化 |

对应固定上游源码为 [JWT 签发](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/object/token_jwt.go)、
[UserInfo](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/object/user.go) 与
[SAML 模板](https://github.com/casdoor/casdoor/blob/1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e/object/saml_idp.go)。
Logout Token 结论以本仓 `0002-oidc-session-logout.patch` 应用后的源码为准。

## 已接入的受控补丁

主体补丁位于 `modules/casdoor/casdoor/patches/0005-directory-subject.patch`，撤权及刷新补丁位于
`0006-directory-revocation.patch`。两者由 Dockerfile 和源码探针按同一顺序应用。


- `anas` 组织用户统一取 `ExternalId`；缺失或带首尾空白时拒绝，不回退到内部 ID、用户名或邮箱。
- JWT 签发、UserInfo、Logout Token 与 SAML 使用同一函数。JWT-Custom 的字段配置不能再次覆盖
  目录主体标识符；原有显式锚点属性、用户名和邮箱仍是各自的字段。
- SAML 2.0 使用 persistent NameID 格式。SAML 1.1 两处 NameIdentifier 也采用同一取值，并拒绝
  缺锚点；ANAS 当前部署和正向探针使用 SAML 2.0。
- 恢复管理员属于 `built-in`，OIDC 保留其 Casdoor 内部 ID，不要求不存在的目录锚点。

正向测试实际签名并验证四种 JWT 格式的 access/refresh token 和 Logout Token，覆盖自定义 `sub`
配置冲突、同一 `sid`、改名/邮箱变化保持主体，以及恢复管理员的 OIDC 签发。负向测试覆盖 JWT、
UserInfo、Logout Token、SAML 2.0/1.1 缺锚点拒绝。SAML 正向测试检查模板 XML；未验证签名断言、
实际 HTTP token/refresh grant、浏览器登录或 Consumer 账号归属。

## 实机验收剩余条件

1. Netbird 普通用户 URL 投影已确认，Casdoor 已阻止该组合；其余 Consumer 的资产归属及展示层
   按计划逐项验收，未发布不免除身份绑定要求。
2. 同一 r10 部署执行真实 OIDC/SAML 登录、UserInfo、Logout Token、refresh grant、改名复用、
   标签回收和缺锚点拒绝，源码测试不计作这些 E2E 的通过证据。
3. 目录撤权已经接入，仍须验证变更前 Cookie 失效、接收失败的持久重试及新会话隔离；SAML SLO
   仍是显式缺口。

规范来源是
[目录身份键要求](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/directory-identity-key.md)，
实施进度见
[配套计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md)。

## Consumer 投影复核（2026-10-03）

Netbird 0.76.1 的 extractor 原样绑定 sub，个人访问令牌接口 `/api/users/{userId}/tokens` 对
普通用户开放，故有禁止的 URL 投影。Casdoor Hook 在发布 binding 和渲染应用前均拒绝这组组合。
证据入口为 Netbird 技术文档列出的三个固定源码文件；源码归档在测试报告中记录，未计作 UI E2E。
其他 Consumer 的既有匹配键证据仍按身份键计划分别保留，不能把 Provider 源码测试算作其资产归属验收。
