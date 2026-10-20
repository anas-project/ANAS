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
| `PORTALUX-T-002` | unit | `PORTALUX-R-006`、`PORTALUX-R-007`、`PORTALUX-R-008`、`PORTALUX-R-009`、`PORTALUX-R-010`、`PORTALUX-R-013` | modules/user_portal/portal/prefs_test.go |
| `PORTALUX-T-003` | unit | `PORTALUX-R-003`、`PORTALUX-R-004`、`PORTALUX-R-005`、`PORTALUX-R-011`、`PORTALUX-R-012`、`PORTALUX-R-014`、`PORTALUX-R-015`、`PORTALUX-R-016`、`PORTALUX-R-017`、`PORTALUX-R-018`、`PORTALUX-R-019`、`PORTALUX-R-020`、`PORTALUX-R-021` | test-env/playwright/user-portal-experience.test.mjs |
| `PORTALUX-T-004` | e2e | `PORTALUX-R-006`、`PORTALUX-R-021`、`PORTALUX-R-022` | test-env/playwright/user-portal-account-management.mjs |

## `PORTALUX-T-001` 八种界面语言与语言匹配

- 级别：`unit`
- 覆盖需求：`PORTALUX-R-001`、`PORTALUX-R-002`
- 需求复核摘要：`sha256:0ed6fe37bb039a35de2b3bdad27943d183c6e731cab623580aa185c61756cc93`
- 实现复核摘要：`sha256:f7e915dccb2da87cbddd664a1146848ef84dc4441ae101fdc7b39c92f7b5d62b`
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
- 覆盖需求：`PORTALUX-R-006`、`PORTALUX-R-007`、`PORTALUX-R-008`、`PORTALUX-R-009`、`PORTALUX-R-010`、`PORTALUX-R-013`
- 需求复核摘要：`sha256:2e57b633fbec26a470f4674e1ef0ae1505daabe2fcad88b4ec42467e7fb3540c`
- 实现复核摘要：`sha256:6b28d39c1108a82a16081b2fd91b5ae74cf559849c00ce87a08f68d01d8fb56e`
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
- 覆盖需求：`PORTALUX-R-003`、`PORTALUX-R-004`、`PORTALUX-R-005`、`PORTALUX-R-011`、`PORTALUX-R-012`、`PORTALUX-R-014`、`PORTALUX-R-015`、`PORTALUX-R-016`、`PORTALUX-R-017`、`PORTALUX-R-018`、`PORTALUX-R-019`、`PORTALUX-R-020`、`PORTALUX-R-021`
- 需求复核摘要：`sha256:85dfdadac5b429d47205ccb11c293f4247952bd76ffb9ff73f4114f6d21a2104`
- 实现复核摘要：`sha256:fa6a96b8ff329bd13f58a3bfb1bdef419ffc7ab4a39d9caeda81b84cab93e73a`
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

- 系统主题实时跟随、固定主题不跟随、首屏主题；对比度不低于 4.5:1；常用加入/排序/移出；快速搜索键盘操作与人员权限；地址刷新与前进后退；管理模式可见性；卡片入口与文档分离；三段结果；危险区确认；390px 无横向滚动

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
- 覆盖需求：`PORTALUX-R-006`、`PORTALUX-R-021`、`PORTALUX-R-022`
- 需求复核摘要：`sha256:584302f12ef69d7d4e0fc4507e7185cbaff26d850f66d6fe23e445cbf7df3d34`
- 实现复核摘要：`sha256:9a9072bc8081c7a1382314c2bd0a5d62cd4670ba9c1651b3cf77e980ad9053df`
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
