<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# 独立用户门户验收用例

> 需求来源：[`user-portal.md`](../../../modules/user_portal/dev-docs/requirements/user-portal.md)
>
> 实施计划：[`user-portal.md`](../../../modules/user_portal/dev-docs/plans/archived/user-portal.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `PORTAL-T-001` | unit | `PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-012`、`PORTAL-R-016`、`PORTAL-R-018`、`PORTAL-R-025`、`PORTAL-R-035`、`PORTAL-R-037`、`PORTAL-R-038`、`PORTAL-R-039`、`PORTAL-R-032`、`PORTAL-R-034`、`PORTAL-R-041`、`PORTAL-R-080`、`PORTAL-R-082` | modules/user_portal/portal/server_test.go<br>modules/user_portal/hook/main_test.go<br>internal/appcatalog/bundle_umask_test.go<br>modules/user_portal/portal/browser_logout_test.go<br>modules/user_portal/browserlogout/targets_test.go<br>modules/immich/hook/main_test.go<br>modules/nextcloud/hook/iam_test.go<br>modules/casdoor/hook/iam_test.go |
| `PORTAL-T-002` | e2e | `PORTAL-R-001`、`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-008`、`PORTAL-R-010`、`PORTAL-R-011`、`PORTAL-R-018`、`PORTAL-R-019`、`PORTAL-R-020`、`PORTAL-R-021`、`PORTAL-R-022`、`PORTAL-R-023`、`PORTAL-R-024`、`PORTAL-R-025`、`PORTAL-R-027`、`PORTAL-R-028`、`PORTAL-R-029`、`PORTAL-R-030`、`PORTAL-R-031`、`PORTAL-R-032`、`PORTAL-R-033`、`PORTAL-R-034`、`PORTAL-R-039`、`PORTAL-R-041` | test-env/scripts/user-portal-server-browser.py<br>test-env/scripts/user-portal-prepare-directory.py<br>test-env/playwright/user-portal-e2e.mjs<br>test-env/scripts/user-portal-fixture-control.py |
| `PORTAL-T-003` | e2e | `PORTAL-R-007`、`PORTAL-R-020`、`PORTAL-R-025`、`PORTAL-R-026`、`PORTAL-R-032`、`PORTAL-R-034`、`PORTAL-R-035`、`PORTAL-R-036`、`PORTAL-R-037`、`PORTAL-R-038`、`PORTAL-R-039` | test-env/scripts/user-portal-server-browser.py<br>test-env/scripts/user-portal-prepare-directory.py<br>test-env/playwright/user-portal-e2e.mjs<br>test-env/scripts/user-portal-fixture-control.py<br>test-env/scripts/user-portal-runtime-fault.py<br>test-env/scripts/user-portal-ldap-reply-proxy.py<br>test-env/scripts/user-portal-audit-fault.py |
| `PORTAL-T-004` | e2e | `PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-030` | test-env/scripts/user-portal-server-browser.py<br>test-env/scripts/user-portal-prepare-directory.py<br>test-env/playwright/user-portal-e2e.mjs<br>test-env/scripts/user-portal-fixture-control.py |
| `PORTAL-T-005` | e2e | `PORTAL-R-016` | test-env/scripts/user-portal-prepare-directory.py<br>modules/user_portal/portal/native_test.go |
| `PORTAL-T-006` | unit | `PORTAL-R-045`、`PORTAL-R-046`、`PORTAL-R-048`、`PORTAL-R-049`、`PORTAL-R-050`、`PORTAL-R-051`、`PORTAL-R-052`、`PORTAL-R-053`、`PORTAL-R-055`、`PORTAL-R-056`、`PORTAL-R-057`、`PORTAL-R-058`、`PORTAL-R-059`、`PORTAL-R-062`、`PORTAL-R-063`、`PORTAL-R-064`、`PORTAL-R-065`、`PORTAL-R-066`、`PORTAL-R-067`、`PORTAL-R-069`、`PORTAL-R-073`、`PORTAL-R-071`、`PORTAL-R-072`、`PORTAL-R-075`、`PORTAL-R-076`、`PORTAL-R-077`、`PORTAL-R-081` | modules/user_portal/portal/administration_test.go<br>modules/user_portal/portal/create_test.go<br>modules/user_portal/tools/test_provision_directory.py<br>test-env/playwright/user-portal-ui.test.mjs |
| `PORTAL-T-007` | e2e | `PORTAL-R-042`、`PORTAL-R-077`、`PORTAL-R-044`、`PORTAL-R-045`、`PORTAL-R-046`、`PORTAL-R-047`、`PORTAL-R-048`、`PORTAL-R-049`、`PORTAL-R-050`、`PORTAL-R-051`、`PORTAL-R-052`、`PORTAL-R-053`、`PORTAL-R-054`、`PORTAL-R-055`、`PORTAL-R-056`、`PORTAL-R-057`、`PORTAL-R-058`、`PORTAL-R-059`、`PORTAL-R-060`、`PORTAL-R-062`、`PORTAL-R-063`、`PORTAL-R-064`、`PORTAL-R-065`、`PORTAL-R-066`、`PORTAL-R-067`、`PORTAL-R-069`、`PORTAL-R-073`、`PORTAL-R-071`、`PORTAL-R-072`、`PORTAL-R-075`、`PORTAL-R-076` | test-env/playwright/user-portal-account-management.mjs<br>test-env/scripts/user-portal-account-fixture.py<br>modules/user_portal/portal/account_native_test.go<br>modules/user_portal/tools/provision-directory.py |
| `PORTAL-T-008` | e2e | `PORTAL-R-055`、`PORTAL-R-057`、`PORTAL-R-068` | modules/user_portal/portal/directory_owner_test.go<br>modules/user_portal/portal/owner_native_test.go<br>test-env/scripts/user-portal-owner-isolated.py<br>test-env/playwright/user-portal-ui.test.mjs |
| `PORTAL-T-009` | contract | `PORTAL-R-069`、`PORTAL-R-073`、`PORTAL-R-071`、`PORTAL-R-072`、`PORTAL-R-075`、`PORTAL-R-076` | test-env/playwright/user-portal-ui.test.mjs<br>modules/user_portal/portal/administration_test.go |
| `PORTAL-T-010` | e2e | `PORTAL-R-041`、`PORTAL-R-076`、`PORTAL-R-077`、`PORTAL-R-078`、`PORTAL-R-079`、`PORTAL-R-081`、`PORTAL-R-082` | test-env/playwright/user-portal-logout.mjs<br>test-env/playwright/user-portal-native-logout.mjs |

## `PORTAL-T-001` 协议、授权和失败关闭单元/契约

- 级别：`unit`
- 覆盖需求：`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-012`、`PORTAL-R-016`、`PORTAL-R-018`、`PORTAL-R-025`、`PORTAL-R-035`、`PORTAL-R-037`、`PORTAL-R-038`、`PORTAL-R-039`、`PORTAL-R-032`、`PORTAL-R-034`、`PORTAL-R-041`、`PORTAL-R-080`、`PORTAL-R-082`
- 需求复核摘要：`sha256:23340dee7cad104e1dce4d618193987f96a0d5d822ad45c3f5e32bf5fe6c29b2`
- 实现复核摘要：`sha256:44d9a4f54ae2e8b5bd1f7e303fd213b512b5b80ef34259f00c977644fbe495aa`
- Fixture：固定候选；e2e 使用用户确认的测试主机独立 namespace、Docker endpoint、Samba/Casdoor/Traefik 和随机测试账号
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
go test ./modules/user_portal/portal ./modules/user_portal/hook ./modules/user_portal/browserlogout ./internal/appcatalog
```

有效性验证入口：

```bash
go test ./modules/user_portal/portal ./modules/user_portal/hook ./modules/user_portal/browserlogout ./internal/appcatalog
```

## `PORTAL-T-002` 真实页面、原生 OIDC 与目录操作

- 级别：`e2e`
- 覆盖需求：`PORTAL-R-001`、`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-008`、`PORTAL-R-010`、`PORTAL-R-011`、`PORTAL-R-018`、`PORTAL-R-019`、`PORTAL-R-020`、`PORTAL-R-021`、`PORTAL-R-022`、`PORTAL-R-023`、`PORTAL-R-024`、`PORTAL-R-025`、`PORTAL-R-027`、`PORTAL-R-028`、`PORTAL-R-029`、`PORTAL-R-030`、`PORTAL-R-031`、`PORTAL-R-032`、`PORTAL-R-033`、`PORTAL-R-034`、`PORTAL-R-039`、`PORTAL-R-041`
- 需求复核摘要：`sha256:3ee2efcaee7b757199d830f3404a93554a36338409a1d77dc699bb3834e857bc`
- 实现复核摘要：`sha256:2e0c5d05db721e363b60897add96119e8c063646c8e90a9140eca5f0b426ef1c`
- Fixture：固定候选；e2e 使用用户确认的测试主机独立 namespace、Docker endpoint、Samba/Casdoor/Traefik 和随机测试账号
- 目标能力：`go`、`oidc`、`ldaps`、`casdoor`、`private-docker`
- Oracle 来源：`ui`、`api`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：普通用户、范围外目标、过期认证、错误原密码、弱密码、成员冲突和 IAM 不可达反例
- 超时：`15m`
- 敏感数据：随机测试密码、token 和绑定凭据不进入报告、截图、源码或归档；仅记录稳定引用/结果

前置条件：

- PORTAL_PROBE_ROOT/PORTAL_TEST_REMOTE_ROOT 指向独立夹具；原生 Go 验收指定 PORTAL_NATIVE_FIXTURE
- T-002 默认 browser；T-003 设置 PORTAL_TEST_MODE=faults；T-004 设置 boundary
- 测试数据需预置；依次运行 browser、faults、原生目录、boundary，再核对最终审计；boundary 会重建身份，不得在它之后运行原生目录；凭据仅在 0700/0600 私有输入文件
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
- 实现复核摘要：`sha256:3fc7fecda3c5e565e766b80204b6db17addb5bd2c1fa43a00ceb707d2798e4cb`
- Fixture：固定候选；e2e 使用用户确认的测试主机独立 namespace、Docker endpoint、Samba/Casdoor/Traefik 和随机测试账号
- 目标能力：`go`、`oidc`、`ldaps`、`casdoor`、`private-docker`
- Oracle 来源：`api`、`network`、`filesystem`、`runtime`、`report`
- 有效性证明：`fault-injection`
- 有效性证据：修改已经保存后丢失回复、审计文件链接/路径替换、重启未完成意图不得重放或误报；实际审计上下文、差异和失败记录完整且无密码/token
- 超时：`15m`
- 敏感数据：随机测试密码、token 和绑定凭据不进入报告、截图、源码或归档；仅记录稳定引用/结果

前置条件：

- PORTAL_PROBE_ROOT/PORTAL_TEST_REMOTE_ROOT 指向独立夹具；原生 Go 验收指定 PORTAL_NATIVE_FIXTURE
- T-002 默认 browser；T-003 设置 PORTAL_TEST_MODE=faults；T-004 设置 boundary
- 测试数据需预置；依次运行 browser、faults、原生目录、boundary，再核对最终审计；boundary 会重建身份，不得在它之后运行原生目录；凭据仅在 0700/0600 私有输入文件
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
- 覆盖需求：`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-030`
- 需求复核摘要：`sha256:8b486f828f28db2c032c93545e4585a737c6f714c2487dd485886cf1312097b7`
- 实现复核摘要：`sha256:2e0c5d05db721e363b60897add96119e8c063646c8e90a9140eca5f0b426ef1c`
- Fixture：固定候选；e2e 使用用户确认的测试主机独立 namespace、Docker endpoint、Samba/Casdoor/Traefik 和随机测试账号
- 目标能力：`go`、`oidc`、`ldaps`、`casdoor`、`private-docker`
- Oracle 来源：`api`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：伪造 callback、受保护人员/组、同名同 DN 新对象不得取得旧会话
- 超时：`15m`
- 敏感数据：随机测试密码、token 和绑定凭据不进入报告、截图、源码或归档；仅记录稳定引用/结果

前置条件：

- PORTAL_PROBE_ROOT/PORTAL_TEST_REMOTE_ROOT 指向独立夹具；原生 Go 验收指定 PORTAL_NATIVE_FIXTURE
- T-002 默认 browser；T-003 设置 PORTAL_TEST_MODE=faults；T-004 设置 boundary
- 测试数据需预置；依次运行 browser、faults、原生目录、boundary，再核对最终审计；boundary 会重建身份，不得在它之后运行原生目录；凭据仅在 0700/0600 私有输入文件
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
- 覆盖需求：`PORTAL-R-016`
- 需求复核摘要：`sha256:2c9bfd8039bcca34a0e35da476086c926fec464e741a32b6a4478093ffe3434b`
- 实现复核摘要：`sha256:825cc5c34bea39474e8bbf87c6f4e588e9e1b7b1205b5a6138f44422354e7b22`
- Fixture：固定候选；e2e 使用用户确认的测试主机独立 namespace、Docker endpoint、Samba/Casdoor/Traefik 和随机测试账号
- 目标能力：`go`、`oidc`、`ldaps`、`casdoor`、`private-docker`
- Oracle 来源：`network`、`return-value`、`error-contract`、`report`
- 有效性证明：`counterexample`
- 有效性证据：错误 CA/服务器名、明文、范围外对象/属性和共享改密绑定组写入必须拒绝
- 超时：`15m`
- 敏感数据：随机测试密码、token 和绑定凭据不进入报告、截图、源码或归档；仅记录稳定引用/结果

前置条件：

- PORTAL_PROBE_ROOT/PORTAL_TEST_REMOTE_ROOT 指向独立夹具；原生 Go 验收指定 PORTAL_NATIVE_FIXTURE
- T-002 默认 browser；T-003 设置 PORTAL_TEST_MODE=faults；T-004 设置 boundary
- 测试数据需预置；依次运行 browser、faults、原生目录、boundary，再核对最终审计；boundary 会重建身份，不得在它之后运行原生目录；凭据仅在 0700/0600 私有输入文件
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

## `PORTAL-T-006` 资料、平台管理员与域级确认反例

- 级别：`unit`
- 覆盖需求：`PORTAL-R-045`、`PORTAL-R-046`、`PORTAL-R-048`、`PORTAL-R-049`、`PORTAL-R-050`、`PORTAL-R-051`、`PORTAL-R-052`、`PORTAL-R-053`、`PORTAL-R-055`、`PORTAL-R-056`、`PORTAL-R-057`、`PORTAL-R-058`、`PORTAL-R-059`、`PORTAL-R-062`、`PORTAL-R-063`、`PORTAL-R-064`、`PORTAL-R-065`、`PORTAL-R-066`、`PORTAL-R-067`、`PORTAL-R-069`、`PORTAL-R-073`、`PORTAL-R-071`、`PORTAL-R-072`、`PORTAL-R-075`、`PORTAL-R-076`、`PORTAL-R-077`、`PORTAL-R-081`
- 需求复核摘要：`sha256:92dfd8f76d9f7e9fdfb575194893bf68123072c7001d13e1b23b999c8cc8919a`
- 实现复核摘要：`sha256:39afe91a9bba349ac79799cdadf17060c6b5af26b339673c1b4cb56d1dbb7597`
- Fixture：内存目录与 HTTPS 服务、无 Samba 写入的配置守卫和生产静态资源配模拟 API；不使用真实账号
- 目标能力：`go`、`https-test-server`、`python3`、`playwright-chromium`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`counterexample`
- 有效性证据：改字段、旧版本、同名角色、本人降权、最后管理员、改参/过期/重放、另一人密码与审计失败
- 超时：`20m`
- 敏感数据：域密码、临时密码、令牌和服务绑定只在主机内处理，不进入报告/截图/命令行

前置条件：

- 安装锁定的 Playwright；浏览器使用默认 Chromium 或 PLAYWRIGHT_CHROMIUM_EXECUTABLE_PATH 指定的独立测试进程

执行步骤：

- 执行完整入口
- 核对真实 HTTP/目录结果及 UI；反例不得产生越界写入

可观察断言：

- 改字段、旧版本、同名角色、本人降权、最后管理员、改参/过期/重放、另一人密码与审计失败
- 原生退出遭后台撤销后仅对明确 403 csrf_rejected 刷新 CSRF 并重试一次；重复拒绝、5xx 和网络错误不重放

反例与故障路径：

- 改字段、旧版本、同名角色、本人降权、最后管理员、改参/过期/重放、另一人密码与审计失败

清理：

- 按对象 GUID 和 owned description 清理测试主体，保留无凭据验收摘要；现有服务只替换门户

执行入口：

```bash
go test -race ./modules/user_portal/portal
python3 modules/user_portal/tools/test_provision_directory.py
node test-env/playwright/user-portal-ui.test.mjs
```

有效性验证入口：

```bash
go test -race ./modules/user_portal/portal
python3 modules/user_portal/tools/test_provision_directory.py
node test-env/playwright/user-portal-ui.test.mjs
```

## `PORTAL-T-007` finance 真实目录、界面与平台管理验收

- 级别：`e2e`
- 覆盖需求：`PORTAL-R-042`、`PORTAL-R-077`、`PORTAL-R-044`、`PORTAL-R-045`、`PORTAL-R-046`、`PORTAL-R-047`、`PORTAL-R-048`、`PORTAL-R-049`、`PORTAL-R-050`、`PORTAL-R-051`、`PORTAL-R-052`、`PORTAL-R-053`、`PORTAL-R-054`、`PORTAL-R-055`、`PORTAL-R-056`、`PORTAL-R-057`、`PORTAL-R-058`、`PORTAL-R-059`、`PORTAL-R-060`、`PORTAL-R-062`、`PORTAL-R-063`、`PORTAL-R-064`、`PORTAL-R-065`、`PORTAL-R-066`、`PORTAL-R-067`、`PORTAL-R-069`、`PORTAL-R-073`、`PORTAL-R-071`、`PORTAL-R-072`、`PORTAL-R-075`、`PORTAL-R-076`
- 需求复核摘要：`sha256:34f48dfb6867e20c12750f4d1f7f86756462509462843d61038b06c02e1fa301`
- 实现复核摘要：`sha256:491cad85de2e7080d2cb9828989016924cf719e5549ee7f4ac14e578a3518ee0`
- Fixture：本轮固定候选；finance 仅使用带专属标记和稳定标识的测试人员/组，不修改已有人员密码或成员关系
- 目标能力：`oidc`、`ldaps`、`casdoor`、`private-browser`
- Oracle 来源：`ui`、`api`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：普通用户枚举、越界字段、并发覆盖、系统组混入、本人降权、域级凭据及确认旁路
- 超时：`20m`
- 敏感数据：域密码、临时密码、令牌和服务绑定只在主机内处理，不进入报告/截图/命令行

前置条件：

- 候选源码、镜像和私有夹具路径已核对；原始凭据仅保留在测试主机；真实写入前须有目录专用账号/ACL 的明确授权；只读探针可独立运行
- finance 所有新夹具均无域角色；域级成功写入和所有权验证只在 PORTAL-T-008 的隔离假域执行
- 账号套件仅放行门户/IAM请求，其他应用帧可能显示未确认；退出要求明确IAM结果、门户401、主页按钮及原生重新登录表单，完整跨应用退出与其他设备隔离由独立T-010判定

执行步骤：

- 执行完整入口
- 核对真实 HTTP/目录结果及 UI；反例不得产生越界写入

可观察断言：

- 普通用户枚举、越界字段、并发覆盖、系统组混入、本人降权、域级凭据及确认旁路

反例与故障路径：

- 普通用户枚举、越界字段、并发覆盖、系统组混入、本人降权、域级凭据及确认旁路

清理：

- 按对象 GUID 和 owned description 清理测试主体，保留无凭据验收摘要；部署前按计划核对实际受影响服务，不修改无关容器

执行入口：

```bash
go test -tags portal_native ./modules/user_portal/portal -run TestNativeAdministrationReadOnly
go test -tags portal_native ./modules/user_portal/portal -run TestNativeAccountManagementCreation$
node test-env/playwright/user-portal-account-management.mjs
```

有效性验证入口：

```bash
go test -tags portal_native ./modules/user_portal/portal -run TestNativeAdministrationReadOnly
go test -tags portal_native ./modules/user_portal/portal -run TestNativeAccountManagementCreation$
node test-env/playwright/user-portal-account-management.mjs
```

## `PORTAL-T-008` 域风险加组前的所有权移交与部分成功

- 级别：`e2e`
- 覆盖需求：`PORTAL-R-055`、`PORTAL-R-057`、`PORTAL-R-068`
- 需求复核摘要：`sha256:343b85b4a48ed846aa64633ed4c545a54ac6fedae2b5b8d1cd50742d5d518b03`
- 实现复核摘要：`sha256:512f5da97f845ad00a4076674c5fd62f14eca5889dd73f7a5e6a81235470cf89`
- Fixture：完全断网的 owner.test 假域，无生产信任或现有目录凭据；使用本轮生产 Go 适配器
- 目标能力：`go`、`docker`、`openssl`、`python3`、`ldaps`
- Oracle 来源：`return-value`、`api`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：原生隔离域所有权、域组成员及创建者权限回读；不以单元成功代替原生验收
- 超时：`10m`
- 敏感数据：仅一次性假域凭据，无真实目录密码；私有日志不进入 Git

前置条件：

- 本地已有候选 Samba 镜像；PORTAL_OWNER_IMAGE、PORTAL_OWNER_BINARY、PORTAL_OWNER_EVIDENCE 指向专用输入/结果目录；测试二进制使用 portal_native 构建

执行步骤：

- 执行单元反例
- 启动隔离假域并执行生产 Go 适配器
- 用原创建者回试 DACL 写入
- 删除专用容器和匿名卷

可观察断言：

- 所有权失败或未知不加组
- 移交后原创建者不能改 DACL
- 成功升权仍受保护
- 加组失败不回退 owner，API/审计分别记录两步
- 仅断网假域，不修改真实域

反例与故障路径：

- 另一人密码
- 普通创建者无域授权
- 所有权写入丢失
- 回读失败
- 第二步拒绝或未知

清理：

- 只按本次唯一标签删除测试容器及其匿名卷，移除临时域密码和私钥，保留脱敏结果及失败日志

执行入口：

```bash
go test ./modules/user_portal/portal -run 'TestDomainOwnershipSteps|TestOwnerDescriptorBounds|TestOwnershipPartialOutcomeAndAudit'
python3 test-env/scripts/user-portal-owner-isolated.py --image "$PORTAL_OWNER_IMAGE" --native-binary "$PORTAL_OWNER_BINARY" --evidence-dir "$PORTAL_OWNER_EVIDENCE"
```

有效性验证入口：

```bash
go test ./modules/user_portal/portal -run 'TestDomainOwnershipSteps|TestOwnerDescriptorBounds|TestOwnershipPartialOutcomeAndAudit'
```

## `PORTAL-T-009` 页内确认、无操作理由与高级区域

- 级别：`contract`
- 覆盖需求：`PORTAL-R-069`、`PORTAL-R-073`、`PORTAL-R-071`、`PORTAL-R-072`、`PORTAL-R-075`、`PORTAL-R-076`
- 需求复核摘要：`sha256:7ef1fe16a6ee2f6c67f4aeef704c6b6f1f2d2d2fba2ebacd870111a909717de2`
- 实现复核摘要：`sha256:6525c5c36b5c43bfc2944cb005633acb3c5ad148d428c9923f5aa4fe97efe16d`
- Fixture：生产前端加隔离模拟 API 和 Go 目录替身
- 目标能力：`go`、`playwright`
- Oracle 来源：`ui`、`api`、`error-contract`
- 有效性证明：`counterexample`
- 有效性证据：对原生弹框、取消和已移除reason字段设反例断言
- 超时：`5m`
- 敏感数据：仅模拟密码，无真实凭据

前置条件：

- 本机 Chrome 或 Playwright Chromium

执行步骤：

- 执行前端双语用例与移除字段API反例

可观察断言：

- 普通组添加无确认、系统组取消不写入、页面和写请求不含理由字段、双语高级组位于底部且默认收起

反例与故障路径：

- 浏览器原生弹框一旦出现即失败；取消不得写入；旧reason字段被拒绝

清理：

- 关闭隔离浏览器和本机测试服务

执行入口：

```bash
node test-env/playwright/user-portal-ui.test.mjs
go test ./modules/user_portal/portal -run TestAdministrationWithoutReasons
```

有效性验证入口：

```bash
node test-env/playwright/user-portal-ui.test.mjs
```

## `PORTAL-T-010` 真实网页跨应用退出、旧会话与分类开关

- 级别：`e2e`
- 覆盖需求：`PORTAL-R-041`、`PORTAL-R-076`、`PORTAL-R-077`、`PORTAL-R-078`、`PORTAL-R-079`、`PORTAL-R-081`、`PORTAL-R-082`
- 需求复核摘要：`sha256:053624282c344e97349ee104d7e8fa3497eacb04e6243bda42adda146c31d029`
- 实现复核摘要：`sha256:1cace75951256f5177cbc1188bba7f0513163939234e8d75e0b1fbe89eaec1db`
- Fixture：finance 已授权测试栈的新前缀人员；独立 Chromium/Chrome 上下文和显式记录的正常域名/SSH 入口；浏览器可为本机或已授权 LN 临时工具容器，服务目标始终是 finance，不使用真实账号或注入认证状态
- 目标能力：`playwright-chromium`、`casdoor`、`nextcloud`、`immich`、`private-input`
- Oracle 来源：`ui`、`api`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：真实网页回调、浏览器 API 状态、旧凭据、页面刷新和并行浏览器；保留失败与未执行场景；登录前 120 秒内仅轮询原生身份就绪，记录每次观测，不重登或放宽退出判据
- 超时：`60m`
- 敏感数据：密码、Cookie、JWT 仅在 0700/0600 私有输入或内存处理；报告不含原始 URL 查询、令牌或私钥

前置条件：

- 匿名竞态入口仅需 PORTAL_NATIVE_LOGOUT_ROOT 指向本轮 /tmp/anas-portal-browser-logout-* 证据目录和 PORTAL_NATIVE_LOGOUT_JS_SHA256 指定已部署静态资源摘要；不读取账号密码；它不能替代下面的认证会话全套
- PORTAL_LOGOUT_ROOT 指向专用夹具私有输入；候选镜像与 baseline 一致；近期目录同步已就绪；entry_mode=direct 使用正常域名路由，默认 forward 使用专用 SSH 入口；PORTAL_LOGOUT_HTTP1_ONLY=1 仅为传输诊断显式禁用浏览器 HTTP/2；报告记录模式、选项和实际 Chrome 版本
- 使用正常网页 OIDC 登录；浏览器自身请求检查 Cookie，原 Cookie 值只在内存用于旧 Bearer 反例；不输出令牌
- --native-menu 默认只运行 Nextcloud/Immich 两个菜单点击场景；PORTAL_LOGOUT_NATIVE_MENU=1 也可对指定原生来源实际打开用户菜单并点击退出，保存菜单截图与失败控件；默认仍使用原生退出路由，报告记录 native_menu；PORTAL_LOGOUT_SCENARIOS 可选定向运行；独立对照浏览器跨场景保留且每次回读身份，不重新登录掩盖会话撤销；默认包含分类、三种退出来源、三种 IAM Cookie 丢失后的旧会话来源、门户部分失败重试、两种从未打开门户的原生来源，共十个场景；每轮保存独立证据目录，部分运行不代表全套通过

执行步骤：

- 检查两大组分类、排序和偏好记忆
- 在同一浏览器真实登录全部应用，并建立另一浏览器的独立会话
- 门户实际点击退出；Nextcloud 和 Immich 从已登录页面导航各自原生退出路由，并以 PORTAL_LOGOUT_NATIVE_MENU=1 另行验证用户菜单的实际退出点击
- 清除仅 IAM 的测试 Cookie 和缓存登录数据后原生重新认证，保留原应用 Cookie，再测试旧会话联动
- 模拟应用退出帧不可用并保留部分结果，刷新后重试；验证无门户会话的原生退出回调
- 检查原受保护 API、旧 Bearer、刷新、返回主页及另一浏览器隔离

可观察断言：

- 匿名原生回调使用真实退出请求使待处理会话失效，浏览器实际收到 403 后仅一次新 CSRF 请求成功；三目标明确退出、业务目录 401 和返回主页均须通过；不伪造认证响应或注入 Cookie
- 前置身份必须相同且真实认证；5xx、超时或无身份字段不能算退出
- 后台与用户小分类同时开关，保留大组及卡片顺序
- 一个入口退出后逐应用核验旧会话，保留另一浏览器会话
- 刷新不得静默恢复，旧 Bearer 必须拒绝，门户按钮返回匿名主页

反例与故障路径：

- IAM Cookie 丢失后旧 RP 会话不因新 IAM 登录而自动刷新
- 另一浏览器会话不得被误撤销
- 缺少前置认证、同名身份不匹配或服务不可用一律不算退出通过

清理：

- 关闭独立浏览器与本轮转发；按 GUID/标记删除专用目录对象并核验 IAM 停用；保留四个正式受限绑定和现有容器/恢复点；原始证据回存 finance

执行入口：

```bash
node test-env/playwright/user-portal-logout.mjs
node test-env/playwright/user-portal-logout.mjs --native-menu
node test-env/playwright/user-portal-native-logout.mjs
```

有效性验证入口：

```bash
node test-env/playwright/user-portal-logout.mjs
node test-env/playwright/user-portal-logout.mjs --native-menu
node test-env/playwright/user-portal-native-logout.mjs
```
