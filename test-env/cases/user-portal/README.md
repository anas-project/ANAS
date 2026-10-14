<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# 独立用户门户验收用例

> 需求来源：[`user-portal.md`](../../../modules/user_portal/dev-docs/requirements/user-portal.md)
>
> 实施计划：[`user-portal.md`](../../../modules/user_portal/dev-docs/plans/user-portal.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `PORTAL-T-001` | unit | `PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-012`、`PORTAL-R-013`、`PORTAL-R-014`、`PORTAL-R-015`、`PORTAL-R-016`、`PORTAL-R-018`、`PORTAL-R-025`、`PORTAL-R-035`、`PORTAL-R-037`、`PORTAL-R-038`、`PORTAL-R-039`、`PORTAL-R-032`、`PORTAL-R-034`、`PORTAL-R-040`、`PORTAL-R-041` | modules/user_portal/portal/server_test.go<br>modules/user_portal/hook/main_test.go |
| `PORTAL-T-002` | e2e | `PORTAL-R-001`、`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-008`、`PORTAL-R-009`、`PORTAL-R-010`、`PORTAL-R-011`、`PORTAL-R-013`、`PORTAL-R-014`、`PORTAL-R-015`、`PORTAL-R-017`、`PORTAL-R-018`、`PORTAL-R-019`、`PORTAL-R-020`、`PORTAL-R-021`、`PORTAL-R-022`、`PORTAL-R-023`、`PORTAL-R-024`、`PORTAL-R-025`、`PORTAL-R-027`、`PORTAL-R-028`、`PORTAL-R-029`、`PORTAL-R-030`、`PORTAL-R-031`、`PORTAL-R-032`、`PORTAL-R-033`、`PORTAL-R-034`、`PORTAL-R-039`、`PORTAL-R-040`、`PORTAL-R-041` | test-env/scripts/user-portal-server-browser.py<br>test-env/scripts/user-portal-prepare-directory.py<br>test-env/playwright/user-portal-e2e.mjs<br>test-env/scripts/user-portal-fixture-control.py |
| `PORTAL-T-003` | e2e | `PORTAL-R-007`、`PORTAL-R-020`、`PORTAL-R-025`、`PORTAL-R-026`、`PORTAL-R-032`、`PORTAL-R-034`、`PORTAL-R-035`、`PORTAL-R-036`、`PORTAL-R-037`、`PORTAL-R-038`、`PORTAL-R-039` | test-env/scripts/user-portal-server-browser.py<br>test-env/scripts/user-portal-prepare-directory.py<br>test-env/playwright/user-portal-e2e.mjs<br>test-env/scripts/user-portal-fixture-control.py<br>test-env/scripts/user-portal-runtime-fault.py<br>test-env/scripts/user-portal-ldap-reply-proxy.py<br>test-env/scripts/user-portal-audit-fault.py |
| `PORTAL-T-004` | e2e | `PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-014`、`PORTAL-R-015`、`PORTAL-R-030` | test-env/scripts/user-portal-server-browser.py<br>test-env/scripts/user-portal-prepare-directory.py<br>test-env/playwright/user-portal-e2e.mjs<br>test-env/scripts/user-portal-fixture-control.py |
| `PORTAL-T-005` | e2e | `PORTAL-R-016`、`PORTAL-R-017` | test-env/scripts/user-portal-prepare-directory.py<br>modules/user_portal/portal/native_test.go |

## `PORTAL-T-001` 协议、授权和失败关闭单元/契约

- 级别：`unit`
- 覆盖需求：`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-012`、`PORTAL-R-013`、`PORTAL-R-014`、`PORTAL-R-015`、`PORTAL-R-016`、`PORTAL-R-018`、`PORTAL-R-025`、`PORTAL-R-035`、`PORTAL-R-037`、`PORTAL-R-038`、`PORTAL-R-039`、`PORTAL-R-032`、`PORTAL-R-034`、`PORTAL-R-040`、`PORTAL-R-041`
- 需求复核摘要：`sha256:4060754a43956653ebd72aacedbafc2744ca88ed38288eb1a764c66abf1094a8`
- 实现复核摘要：`sha256:eb0bccd655c03373efb4ddc0dc5d0613a1ee9c2ccf7dd3f7592c9b593671cf25`
- Fixture：固定候选；e2e 使用 finance 独立 namespace、Docker endpoint、Samba/Casdoor/Traefik 和随机测试账号
- 目标能力：`go`、`https-test-server`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`counterexample`
- 有效性证据：伪造/过期 token、规则拼接、CSRF、身份复用、审计故障和 TLS 反例必须拒绝
- 超时：`5m`
- 敏感数据：随机测试密码、token 和绑定凭据不进入报告、截图、源码或归档；仅记录稳定引用/结果

前置条件：

- 无。

执行步骤：

- 执行列出的固定入口
- 分别核对授权正例和反例，读取退出状态及脱敏结果

可观察断言：

- 伪造/过期 token、规则拼接、CSRF、身份复用、审计故障和 TLS 反例必须拒绝

反例与故障路径：

- 伪造/过期 token、规则拼接、CSRF、身份复用、审计故障和 TLS 反例必须拒绝

清理：

- 临时测试目录自动清理

执行入口：

```bash
go test ./modules/user_portal/portal ./modules/user_portal/hook
```

有效性验证入口：

```bash
go test ./modules/user_portal/portal ./modules/user_portal/hook
```

## `PORTAL-T-002` 真实页面、原生 OIDC 与目录操作

- 级别：`e2e`
- 覆盖需求：`PORTAL-R-001`、`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-008`、`PORTAL-R-009`、`PORTAL-R-010`、`PORTAL-R-011`、`PORTAL-R-013`、`PORTAL-R-014`、`PORTAL-R-015`、`PORTAL-R-017`、`PORTAL-R-018`、`PORTAL-R-019`、`PORTAL-R-020`、`PORTAL-R-021`、`PORTAL-R-022`、`PORTAL-R-023`、`PORTAL-R-024`、`PORTAL-R-025`、`PORTAL-R-027`、`PORTAL-R-028`、`PORTAL-R-029`、`PORTAL-R-030`、`PORTAL-R-031`、`PORTAL-R-032`、`PORTAL-R-033`、`PORTAL-R-034`、`PORTAL-R-039`、`PORTAL-R-040`、`PORTAL-R-041`
- 需求复核摘要：`sha256:1f5553893a779e73821d6a5c796c5ffd20020a50746de731a5757980ab61621b`
- 实现复核摘要：`sha256:2394844ad395210de7857eb97cabf0a6ac4c05770d9d4c807fa09bb1093f44c6`
- Fixture：固定候选；e2e 使用 finance 独立 namespace、Docker endpoint、Samba/Casdoor/Traefik 和随机测试账号
- 目标能力：`go`、`oidc`、`ldaps`、`casdoor`、`private-docker`
- Oracle 来源：`ui`、`api`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：普通用户、范围外目标、过期认证、错误原密码、弱密码、成员冲突和 IAM 不可达反例
- 超时：`15m`
- 敏感数据：随机测试密码、token 和绑定凭据不进入报告、截图、源码或归档；仅记录稳定引用/结果

前置条件：

- PORTAL_PROBE_ROOT/PORTAL_TEST_REMOTE_ROOT 指向独立夹具；原生 Go 验收指定 PORTAL_NATIVE_FIXTURE
- T-002 默认 browser；T-003 设置 PORTAL_TEST_MODE=faults；T-004 设置 boundary
- 测试数据需预置并按顺序运行；凭据仅在 0700/0600 私有输入文件
- 服务器内部模式设 PORTAL_TEST_LOCAL_FIXTURE=1、DOCKER_HOST 为该 run 的私有 socket，PORTAL_TEST_RESOLVED_IP 为 run.json 的 ip；使用锁定 Playwright 1.62.1 工具容器
- 近期认证测试窗口默认 60 秒；PORTAL_TEST_RECENT_WINDOW_SECONDS 必须与隔离服务配置相同，生产默认 300 秒
- 页面以 DOM 和实际 API 结果判定就绪，不能使用网络空闲代替认证结果

执行步骤：

- 执行列出的固定入口
- 分别核对授权正例和反例，读取退出状态及脱敏结果

可观察断言：

- 普通用户、范围外目标、过期认证、错误原密码、弱密码、成员冲突和 IAM 不可达反例

反例与故障路径：

- 普通用户、范围外目标、过期认证、错误原密码、弱密码、成员冲突和 IAM 不可达反例

清理：

- 仅移除该 run 的 daemon、容器、namespace、数据和测试凭据；先归档公共源码与脱敏证据

执行入口：

```bash
node test-env/playwright/user-portal-e2e.mjs
```

有效性验证入口：

```bash
node test-env/playwright/user-portal-e2e.mjs
```

## `PORTAL-T-003` 真实目录和审计故障

- 级别：`e2e`
- 覆盖需求：`PORTAL-R-007`、`PORTAL-R-020`、`PORTAL-R-025`、`PORTAL-R-026`、`PORTAL-R-032`、`PORTAL-R-034`、`PORTAL-R-035`、`PORTAL-R-036`、`PORTAL-R-037`、`PORTAL-R-038`、`PORTAL-R-039`
- 需求复核摘要：`sha256:a34c0af1019dc8e6a77336f03b3e8ad18e3953b8af227a01d6a1e97c1069b5c1`
- 实现复核摘要：`sha256:093430dcda30d3330eccf7a280339a5dccb0b81a03ce9c31beb2bd12d553aec1`
- Fixture：固定候选；e2e 使用 finance 独立 namespace、Docker endpoint、Samba/Casdoor/Traefik 和随机测试账号
- 目标能力：`go`、`oidc`、`ldaps`、`casdoor`、`private-docker`
- Oracle 来源：`api`、`network`、`filesystem`、`runtime`、`report`
- 有效性证明：`fault-injection`
- 有效性证据：修改已经保存后丢失回复、审计文件链接/路径替换、重启未完成意图不得重放或误报；实际审计上下文、差异和失败记录完整且无密码/token
- 超时：`15m`
- 敏感数据：随机测试密码、token 和绑定凭据不进入报告、截图、源码或归档；仅记录稳定引用/结果

前置条件：

- PORTAL_PROBE_ROOT/PORTAL_TEST_REMOTE_ROOT 指向独立夹具；原生 Go 验收指定 PORTAL_NATIVE_FIXTURE
- T-002 默认 browser；T-003 设置 PORTAL_TEST_MODE=faults；T-004 设置 boundary
- 测试数据需预置并按顺序运行；凭据仅在 0700/0600 私有输入文件
- 服务器内部模式设 PORTAL_TEST_LOCAL_FIXTURE=1、DOCKER_HOST 为该 run 的私有 socket，PORTAL_TEST_RESOLVED_IP 为 run.json 的 ip；使用锁定 Playwright 1.62.1 工具容器
- 近期认证测试窗口默认 60 秒；PORTAL_TEST_RECENT_WINDOW_SECONDS 必须与隔离服务配置相同，生产默认 300 秒
- 页面以 DOM 和实际 API 结果判定就绪，不能使用网络空闲代替认证结果

执行步骤：

- 执行列出的固定入口
- 分别核对授权正例和反例，读取退出状态及脱敏结果

可观察断言：

- 修改已经保存后丢失回复、审计文件链接/路径替换、重启未完成意图不得重放或误报；实际审计上下文、差异和失败记录完整且无密码/token

反例与故障路径：

- 修改已经保存后丢失回复、审计文件链接/路径替换、重启未完成意图不得重放或误报；实际审计上下文、差异和失败记录完整且无密码/token

清理：

- 仅移除该 run 的 daemon、容器、namespace、数据和测试凭据；先归档公共源码与脱敏证据

执行入口：

```bash
node test-env/playwright/user-portal-e2e.mjs
python3 test-env/scripts/user-portal-runtime-fault.py $PORTAL_TEST_REMOTE_ROOT audit-contract
```

有效性验证入口：

```bash
node test-env/playwright/user-portal-e2e.mjs
python3 test-env/scripts/user-portal-runtime-fault.py $PORTAL_TEST_REMOTE_ROOT audit-contract
```

## `PORTAL-T-004` 实际回调、保护和身份复用边界

- 级别：`e2e`
- 覆盖需求：`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-014`、`PORTAL-R-015`、`PORTAL-R-030`
- 需求复核摘要：`sha256:88812280037ae7845286f47b9d646e7ff07ae5906447ec412c00c705c95a679d`
- 实现复核摘要：`sha256:2394844ad395210de7857eb97cabf0a6ac4c05770d9d4c807fa09bb1093f44c6`
- Fixture：固定候选；e2e 使用 finance 独立 namespace、Docker endpoint、Samba/Casdoor/Traefik 和随机测试账号
- 目标能力：`go`、`oidc`、`ldaps`、`casdoor`、`private-docker`
- Oracle 来源：`api`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：伪造 callback、受保护人员/组、同名同 DN 新对象不得取得旧会话
- 超时：`15m`
- 敏感数据：随机测试密码、token 和绑定凭据不进入报告、截图、源码或归档；仅记录稳定引用/结果

前置条件：

- PORTAL_PROBE_ROOT/PORTAL_TEST_REMOTE_ROOT 指向独立夹具；原生 Go 验收指定 PORTAL_NATIVE_FIXTURE
- T-002 默认 browser；T-003 设置 PORTAL_TEST_MODE=faults；T-004 设置 boundary
- 测试数据需预置并按顺序运行；凭据仅在 0700/0600 私有输入文件
- 服务器内部模式设 PORTAL_TEST_LOCAL_FIXTURE=1、DOCKER_HOST 为该 run 的私有 socket，PORTAL_TEST_RESOLVED_IP 为 run.json 的 ip；使用锁定 Playwright 1.62.1 工具容器
- 近期认证测试窗口默认 60 秒；PORTAL_TEST_RECENT_WINDOW_SECONDS 必须与隔离服务配置相同，生产默认 300 秒
- 页面以 DOM 和实际 API 结果判定就绪，不能使用网络空闲代替认证结果

执行步骤：

- 执行列出的固定入口
- 分别核对授权正例和反例，读取退出状态及脱敏结果

可观察断言：

- 伪造 callback、受保护人员/组、同名同 DN 新对象不得取得旧会话

反例与故障路径：

- 伪造 callback、受保护人员/组、同名同 DN 新对象不得取得旧会话

清理：

- 仅移除该 run 的 daemon、容器、namespace、数据和测试凭据；先归档公共源码与脱敏证据

执行入口：

```bash
node test-env/playwright/user-portal-e2e.mjs
```

有效性验证入口：

```bash
node test-env/playwright/user-portal-e2e.mjs
```

## `PORTAL-T-005` 门户实际 LDAP 适配器 TLS 与 ACL

- 级别：`e2e`
- 覆盖需求：`PORTAL-R-016`、`PORTAL-R-017`
- 需求复核摘要：`sha256:ba79ae443d4b97ee964387c63c06ac3ad40aa1e7beb0b7375f554b79fc19efce`
- 实现复核摘要：`sha256:0103969026ae7288fcf3c78ebedb2e09bcd91b2b0ed8ae4edf7adf0a0b032084`
- Fixture：固定候选；e2e 使用 finance 独立 namespace、Docker endpoint、Samba/Casdoor/Traefik 和随机测试账号
- 目标能力：`go`、`oidc`、`ldaps`、`casdoor`、`private-docker`
- Oracle 来源：`network`、`return-value`、`error-contract`、`report`
- 有效性证明：`counterexample`
- 有效性证据：错误 CA/服务器名、明文、范围外对象/属性和共享改密绑定组写入必须拒绝
- 超时：`15m`
- 敏感数据：随机测试密码、token 和绑定凭据不进入报告、截图、源码或归档；仅记录稳定引用/结果

前置条件：

- PORTAL_PROBE_ROOT/PORTAL_TEST_REMOTE_ROOT 指向独立夹具；原生 Go 验收指定 PORTAL_NATIVE_FIXTURE
- T-002 默认 browser；T-003 设置 PORTAL_TEST_MODE=faults；T-004 设置 boundary
- 测试数据需预置并按顺序运行；凭据仅在 0700/0600 私有输入文件
- 服务器内部模式设 PORTAL_TEST_LOCAL_FIXTURE=1、DOCKER_HOST 为该 run 的私有 socket，PORTAL_TEST_RESOLVED_IP 为 run.json 的 ip；使用锁定 Playwright 1.62.1 工具容器
- 近期认证测试窗口默认 60 秒；PORTAL_TEST_RECENT_WINDOW_SECONDS 必须与隔离服务配置相同，生产默认 300 秒
- 页面以 DOM 和实际 API 结果判定就绪，不能使用网络空闲代替认证结果

执行步骤：

- 执行列出的固定入口
- 分别核对授权正例和反例，读取退出状态及脱敏结果

可观察断言：

- 错误 CA/服务器名、明文、范围外对象/属性和共享改密绑定组写入必须拒绝

反例与故障路径：

- 错误 CA/服务器名、明文、范围外对象/属性和共享改密绑定组写入必须拒绝

清理：

- 仅移除该 run 的 daemon、容器、namespace、数据和测试凭据；先归档公共源码与脱敏证据

执行入口：

```bash
go test ./modules/user_portal/portal -tags portal_native -run TestNativeDirectoryBoundary
```

有效性验证入口：

```bash
go test ./modules/user_portal/portal -tags portal_native -run TestNativeDirectoryBoundary
```
