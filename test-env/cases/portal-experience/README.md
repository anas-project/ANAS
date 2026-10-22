<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# 门户多语言、主题与界面重做验收用例

> 需求来源：[`portal-experience.md`](../../../modules/user_portal/dev-docs/requirements/portal-experience.md)
>
> 实施计划：[`portal-experience.md`](../../../modules/user_portal/dev-docs/plans/archived/portal-experience.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `PORTALUX-T-001` | unit | `PORTALUX-R-001`、`PORTALUX-R-002` | internal/appcatalog/catalog_test.go<br>test-env/playwright/user-portal-experience.test.mjs |
| `PORTALUX-T-002` | unit | `PORTALUX-R-006`、`PORTALUX-R-007`、`PORTALUX-R-008`、`PORTALUX-R-009`、`PORTALUX-R-010`、`PORTALUX-R-013`、`PORTALUX-R-026`、`PORTALUX-R-027` | modules/user_portal/portal/prefs_test.go<br>modules/user_portal/portal/reauth_password_test.go |
| `PORTALUX-T-003` | unit | `PORTALUX-R-003`、`PORTALUX-R-004`、`PORTALUX-R-005`、`PORTALUX-R-011`、`PORTALUX-R-012`、`PORTALUX-R-014`、`PORTALUX-R-015`、`PORTALUX-R-016`、`PORTALUX-R-018`、`PORTALUX-R-020`、`PORTALUX-R-021`、`PORTALUX-R-023`、`PORTALUX-R-024`、`PORTALUX-R-025`、`PORTALUX-R-028`、`PORTALUX-R-029`、`PORTALUX-R-030`、`PORTALUX-R-032` | test-env/playwright/user-portal-experience.test.mjs |
| `PORTALUX-T-004` | e2e | `PORTALUX-R-006`、`PORTALUX-R-021`、`PORTALUX-R-022`、`PORTALUX-R-026`、`PORTALUX-R-027`、`PORTALUX-R-028` | test-env/playwright/user-portal-account-management.mjs |
| `PORTALUX-T-006` | e2e | `PORTALUX-R-032`、`PORTALUX-R-033` | test-env/playwright/iam-appearance-finance.mjs<br>test-env/scripts/application-themes-directory-fixture.py |

## `PORTALUX-T-001` 八种界面语言与语言匹配

- 级别：`unit`
- 覆盖需求：`PORTALUX-R-001`、`PORTALUX-R-002`
- 需求复核摘要：`sha256:0ed6fe37bb039a35de2b3bdad27943d183c6e731cab623580aa185c61756cc93`
- 实现复核摘要：`sha256:11c0fb9afaeedebfe977c78c80b00a63b74f7f57a67327d577b842ad037357a2`
- Fixture：Go 语言匹配表与生产语言包、前端源码；无网络
- 目标能力：`go`、`node`
- Oracle 来源：`return-value`
- 有效性证明：`counterexample`
- 有效性证据：删除任一语言的一个键或改动匹配规则时测试失败
- 超时：`5m`
- 敏感数据：无

前置条件：

- 安装锁定的 Node 依赖

执行步骤：

- 执行语言匹配单元测试
- 加载生产语言包并核对全部键与源码引用

可观察断言：

- 地区变体归入基础语言，繁体地区归 zh-TW，未支持语言回退英文；八种语言键集合一致且非空

反例与故障路径：

- ru、pt-BR、it 等未支持语言；缺键或多键的语言包

清理：

- 无

执行入口：

```bash
go test ./internal/appcatalog
node test-env/playwright/user-portal-experience.test.mjs
```

有效性验证入口：

```bash
go test ./internal/appcatalog
node test-env/playwright/user-portal-experience.test.mjs
```

## `PORTALUX-T-002` 账号偏好存储、白名单与授权过滤

- 级别：`unit`
- 覆盖需求：`PORTALUX-R-006`、`PORTALUX-R-007`、`PORTALUX-R-008`、`PORTALUX-R-009`、`PORTALUX-R-010`、`PORTALUX-R-013`、`PORTALUX-R-026`、`PORTALUX-R-027`
- 需求复核摘要：`sha256:b04ba211ceee2062c903896843dc6c9c25f1dc950088ea0de1a17b48abc5dd58`
- 实现复核摘要：`sha256:09d68fa6e46baaee5e85c8cbf1442cfcd5b94add433a2e6c06e9ed654a14fccd`
- Fixture：内存目录、模拟 IAM 与临时偏好目录
- 目标能力：`go`、`https-test-server`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`counterexample`
- 有效性证据：去掉白名单、授权过滤或损坏保护时对应断言失败
- 超时：`5m`
- 敏感数据：无

前置条件：

- 无

执行步骤：

- 执行偏好接口与存储单元测试

可观察断言：

- 按锚点保存并在新进程读取；越界字段、取值、重复和超过 24 个常用拒绝；CSRF 与未登录拒绝；撤权应用不再返回；损坏文件不被覆盖

反例与故障路径：

- 伪造 CSRF、匿名写入、未授权常用、损坏偏好文件

清理：

- 临时目录随测试删除

执行入口：

```bash
go test -race ./modules/user_portal/portal -run Preference
```

有效性验证入口：

```bash
go test -race ./modules/user_portal/portal -run Preference
```

## `PORTALUX-T-003` 主题、常用、快速搜索与界面结构

- 级别：`unit`
- 覆盖需求：`PORTALUX-R-003`、`PORTALUX-R-004`、`PORTALUX-R-005`、`PORTALUX-R-011`、`PORTALUX-R-012`、`PORTALUX-R-014`、`PORTALUX-R-015`、`PORTALUX-R-016`、`PORTALUX-R-018`、`PORTALUX-R-020`、`PORTALUX-R-021`、`PORTALUX-R-023`、`PORTALUX-R-024`、`PORTALUX-R-025`、`PORTALUX-R-028`、`PORTALUX-R-029`、`PORTALUX-R-030`、`PORTALUX-R-032`
- 需求复核摘要：`sha256:edace9fb42fe6e194c9f5359318c36c774a5ead75e751d9fa87605322208c405`
- 实现复核摘要：`sha256:788738407834ef588a173508a223696ee37b1ce33d4d1852e81a60462a831661`
- Fixture：生产静态资源配模拟 API；不使用真实账号
- 目标能力：`playwright-chromium`
- Oracle 来源：`ui`
- 有效性证明：`counterexample`
- 有效性证据：去掉主题预加载、人员权限判断或路由回退时对应用例失败
- 超时：`10m`
- 敏感数据：无

前置条件：

- 安装锁定的 Playwright；浏览器使用默认 Chromium 或 PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH 指定的独立测试进程

执行步骤：

- 执行门户界面用例

可观察断言：

- 系统主题实时跟随、固定主题不跟随、首屏主题；对比度不低于 4.5:1；常用加入/排序/移出；快速搜索键盘操作与人员权限；地址刷新与前进后退；管理模式可见性；卡片入口与文档分离；仅可确认的操作结果；危险区确认；390px 无横向滚动

反例与故障路径：

- 无人员权限的快速搜索、普通用户访问管理地址、未知地址

清理：

- 浏览器上下文随用例关闭

执行入口：

```bash
node test-env/playwright/user-portal-experience.test.mjs
```

有效性验证入口：

```bash
node test-env/playwright/user-portal-experience.test.mjs
```

## `PORTALUX-T-004` finance 新界面账号偏好、手机排版与完整回归

- 级别：`e2e`
- 覆盖需求：`PORTALUX-R-006`、`PORTALUX-R-021`、`PORTALUX-R-022`、`PORTALUX-R-026`、`PORTALUX-R-027`、`PORTALUX-R-028`
- 需求复核摘要：`sha256:6052ef019b9961b4776f53059292a3cad1c6aec1a3148a33995ad76b67573c65`
- 实现复核摘要：`sha256:0de66dfd4f7062458b169967ab9bf65fe7a814b35535b5a52a78d86e7b7b47ad`
- Fixture：finance 新制品与带专属标记的普通测试人员；不授予域权限
- 目标能力：`oidc`、`ldaps`、`casdoor`、`private-browser`
- Oracle 来源：`ui`、`api`、`report`
- 有效性证明：`counterexample`
- 有效性证据：偏好未保存或页面横向溢出时对应检查失败
- 超时：`30m`
- 敏感数据：测试密码只在主机内处理，不进入报告

前置条件：

- 新门户制品已部署；夹具准备与清理沿用 PORTAL-T-007

执行步骤：

- 执行 PORTAL-T-007 完整入口，其中包含偏好跨浏览器与 390px 全页面检查

可观察断言：

- 同一账号在另一浏览器应用语言、主题与常用；390px 页面无横向滚动且底部导航在底部；原有账号验收全部通过

反例与故障路径：

- 沿用 PORTAL-T-007 的全部反例

清理：

- 按 PORTAL-T-007 清理夹具；测试结束前把偏好恢复为默认

执行入口：

```bash
node test-env/playwright/user-portal-account-management.mjs
```

有效性验证入口：

```bash
node test-env/playwright/user-portal-account-management.mjs
```

## `PORTALUX-T-006` finance原生改密入口及清晰结果

- 级别：`e2e`
- 覆盖需求：`PORTALUX-R-032`、`PORTALUX-R-033`
- 需求复核摘要：`sha256:6ea34060cc9f4f8ba9fe83b7ed52fd8043e6cdfe967f527acc13755da00ab3eb`
- 实现复核摘要：`sha256:b7b9c2465d1a408d1b4902d6e12d88fd02bc0b70687b5f7d3e89fbb3036f9356`
- Fixture：finance原生IAM与门户、同主机专用临时账号
- 目标能力：`ssh`、`node`、`browser`、`docker`、`native-oidc`、`samba-ad`
- Oracle 来源：`ui`、`filesystem`、`return-value`
- 有效性证明：`counterexample`
- 有效性证据：移除恢复入口或仍展示未跟踪的状态时断言失败
- 超时：`15m`
- 敏感数据：临时密码仅留finance的0700/0600私有目录；不转移到工作站或其他服务器

前置条件：

- 本轮候选已原生部署，同主机临时normal用户标记pwdLastSet=0

执行步骤：

- 创建专属普通账号与ANAS管理员，标记普通账号需改密
- 执行原生IAM外观与密码流程
- 保留无密证据并清理专属账号

可观察断言：

- ANAS原生IAM登录恢复入口可用、本地恢复管理员保持原生流程
- 普通账号pwdLastSet=0，错误临时密码可见失败，正确密码恢复成功，随后原生OIDC新密码登录通过
- 资料和密码结果没有未跟踪的IAM或会话状态

反例与故障路径：

- 匿名、CSRF缺失、普通用户导入、隐藏应用图片和损坏主题

清理：

- 按标记和GUID删除两临时账号及OU，清理IAM软删除用户和私有凭据；保留无密报告和截图

执行入口：

```bash
node test-env/playwright/iam-appearance-finance.mjs
```

有效性验证入口：

```bash
node test-env/playwright/iam-appearance-finance.mjs
```
