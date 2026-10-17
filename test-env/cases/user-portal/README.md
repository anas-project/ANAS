<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# 独立用户门户验收用例

> 需求来源：[`user-portal.md`](../../../modules/user_portal/dev-docs/requirements/user-portal.md)
>
> 实施计划：[`user-portal.md`](../../../modules/user_portal/dev-docs/plans/user-portal.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `PORTAL-T-001` | unit | `PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-012`、`PORTAL-R-016`、`PORTAL-R-018`、`PORTAL-R-025`、`PORTAL-R-035`、`PORTAL-R-037`、`PORTAL-R-038`、`PORTAL-R-039`、`PORTAL-R-032`、`PORTAL-R-034`、`PORTAL-R-041` | modules/user_portal/portal/server_test.go<br>modules/user_portal/hook/main_test.go<br>internal/appcatalog/bundle_umask_test.go |
| `PORTAL-T-002` | e2e | `PORTAL-R-001`、`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-008`、`PORTAL-R-010`、`PORTAL-R-011`、`PORTAL-R-018`、`PORTAL-R-019`、`PORTAL-R-020`、`PORTAL-R-021`、`PORTAL-R-022`、`PORTAL-R-023`、`PORTAL-R-024`、`PORTAL-R-025`、`PORTAL-R-027`、`PORTAL-R-028`、`PORTAL-R-029`、`PORTAL-R-030`、`PORTAL-R-031`、`PORTAL-R-032`、`PORTAL-R-033`、`PORTAL-R-034`、`PORTAL-R-039`、`PORTAL-R-041` | test-env/scripts/user-portal-server-browser.py<br>test-env/scripts/user-portal-prepare-directory.py<br>test-env/playwright/user-portal-e2e.mjs<br>test-env/scripts/user-portal-fixture-control.py |
| `PORTAL-T-003` | e2e | `PORTAL-R-007`、`PORTAL-R-020`、`PORTAL-R-025`、`PORTAL-R-026`、`PORTAL-R-032`、`PORTAL-R-034`、`PORTAL-R-035`、`PORTAL-R-036`、`PORTAL-R-037`、`PORTAL-R-038`、`PORTAL-R-039` | test-env/scripts/user-portal-server-browser.py<br>test-env/scripts/user-portal-prepare-directory.py<br>test-env/playwright/user-portal-e2e.mjs<br>test-env/scripts/user-portal-fixture-control.py<br>test-env/scripts/user-portal-runtime-fault.py<br>test-env/scripts/user-portal-ldap-reply-proxy.py<br>test-env/scripts/user-portal-audit-fault.py |
| `PORTAL-T-004` | e2e | `PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-030` | test-env/scripts/user-portal-server-browser.py<br>test-env/scripts/user-portal-prepare-directory.py<br>test-env/playwright/user-portal-e2e.mjs<br>test-env/scripts/user-portal-fixture-control.py |
| `PORTAL-T-005` | e2e | `PORTAL-R-016` | test-env/scripts/user-portal-prepare-directory.py<br>modules/user_portal/portal/native_test.go |
| `PORTAL-T-006` | unit | `PORTAL-R-045`、`PORTAL-R-046`、`PORTAL-R-048`、`PORTAL-R-049`、`PORTAL-R-050`、`PORTAL-R-051`、`PORTAL-R-052`、`PORTAL-R-053`、`PORTAL-R-055`、`PORTAL-R-056`、`PORTAL-R-057`、`PORTAL-R-058`、`PORTAL-R-059` | modules/user_portal/portal/administration_test.go<br>modules/user_portal/tools/test_provision_directory.py<br>test-env/playwright/user-portal-ui.test.mjs |
| `PORTAL-T-007` | e2e | `PORTAL-R-042`、`PORTAL-R-043`、`PORTAL-R-044`、`PORTAL-R-045`、`PORTAL-R-046`、`PORTAL-R-047`、`PORTAL-R-048`、`PORTAL-R-049`、`PORTAL-R-050`、`PORTAL-R-051`、`PORTAL-R-052`、`PORTAL-R-053`、`PORTAL-R-054`、`PORTAL-R-055`、`PORTAL-R-056`、`PORTAL-R-057`、`PORTAL-R-058`、`PORTAL-R-059`、`PORTAL-R-060`、`PORTAL-R-061` | test-env/playwright/user-portal-account-management.mjs<br>test-env/scripts/user-portal-account-fixture.py<br>modules/user_portal/portal/account_native_test.go<br>modules/user_portal/tools/provision-directory.py |

## `PORTAL-T-001` 协议、授权和失败关闭单元/契约

- 级别：`unit`
- 覆盖需求：`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-012`、`PORTAL-R-016`、`PORTAL-R-018`、`PORTAL-R-025`、`PORTAL-R-035`、`PORTAL-R-037`、`PORTAL-R-038`、`PORTAL-R-039`、`PORTAL-R-032`、`PORTAL-R-034`、`PORTAL-R-041`
- 需求复核摘要：`sha256:10bd5771ce666b5e688bbfc201e3bb5299ac95925967849afcc6f87b40d17430`
- 实现复核摘要：`sha256:eb3ab84b1897097106f2d5cedb80e78cdc12024a2d9b027d7991a065fc223cc4`
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
go test ./modules/user_portal/portal ./modules/user_portal/hook ./internal/appcatalog
```

有效性验证入口：

```bash
go test ./modules/user_portal/portal ./modules/user_portal/hook ./internal/appcatalog
```

## `PORTAL-T-002` 真实页面、原生 OIDC 与目录操作

- 级别：`e2e`
- 覆盖需求：`PORTAL-R-001`、`PORTAL-R-002`、`PORTAL-R-003`、`PORTAL-R-004`、`PORTAL-R-005`、`PORTAL-R-006`、`PORTAL-R-007`、`PORTAL-R-008`、`PORTAL-R-010`、`PORTAL-R-011`、`PORTAL-R-018`、`PORTAL-R-019`、`PORTAL-R-020`、`PORTAL-R-021`、`PORTAL-R-022`、`PORTAL-R-023`、`PORTAL-R-024`、`PORTAL-R-025`、`PORTAL-R-027`、`PORTAL-R-028`、`PORTAL-R-029`、`PORTAL-R-030`、`PORTAL-R-031`、`PORTAL-R-032`、`PORTAL-R-033`、`PORTAL-R-034`、`PORTAL-R-039`、`PORTAL-R-041`
- 需求复核摘要：`sha256:3ee2efcaee7b757199d830f3404a93554a36338409a1d77dc699bb3834e857bc`
- 实现复核摘要：`sha256:1bde314437568f682801ed5822206fc5bd74bc26e8df064595ecfeff28a3fc30`
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
- 实现复核摘要：`sha256:64d996f4f941ee7299d2e5bbeba569839741f2c88fba47a6d5bd7e434b38937a`
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
- 实现复核摘要：`sha256:1bde314437568f682801ed5822206fc5bd74bc26e8df064595ecfeff28a3fc30`
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
- 覆盖需求：`PORTAL-R-045`、`PORTAL-R-046`、`PORTAL-R-048`、`PORTAL-R-049`、`PORTAL-R-050`、`PORTAL-R-051`、`PORTAL-R-052`、`PORTAL-R-053`、`PORTAL-R-055`、`PORTAL-R-056`、`PORTAL-R-057`、`PORTAL-R-058`、`PORTAL-R-059`
- 需求复核摘要：`sha256:768e062e220dd33d9d9a3c4f51409e33dcb57c7eda3c42f3eaea69f964e42ec7`
- 实现复核摘要：`sha256:a9fac528caee9cb406946f8ebfe9a7156480e7ecaf28f264811421e160ab1e70`
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
- 覆盖需求：`PORTAL-R-042`、`PORTAL-R-043`、`PORTAL-R-044`、`PORTAL-R-045`、`PORTAL-R-046`、`PORTAL-R-047`、`PORTAL-R-048`、`PORTAL-R-049`、`PORTAL-R-050`、`PORTAL-R-051`、`PORTAL-R-052`、`PORTAL-R-053`、`PORTAL-R-054`、`PORTAL-R-055`、`PORTAL-R-056`、`PORTAL-R-057`、`PORTAL-R-058`、`PORTAL-R-059`、`PORTAL-R-060`、`PORTAL-R-061`
- 需求复核摘要：`sha256:0f38c44cc96317603ff7a198752f82fdf2400c37788b93a76176e87778c9ffba`
- 实现复核摘要：`sha256:588944aadc45ac8b9d0d2a6082bc745d83426967274e79c4fb5e36c3e4c2657d`
- Fixture：本轮固定候选；finance 仅使用带专属标记和稳定标识的测试人员/组，不修改已有人员密码或成员关系
- 目标能力：`oidc`、`ldaps`、`casdoor`、`private-browser`
- Oracle 来源：`ui`、`api`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：普通用户枚举、越界字段、并发覆盖、系统组混入、本人降权、域级凭据及确认旁路
- 超时：`20m`
- 敏感数据：域密码、临时密码、令牌和服务绑定只在主机内处理，不进入报告/截图/命令行

前置条件：

- 候选源码、镜像和私有夹具路径已核对；原始凭据仅保留在测试主机；真实写入前须有目录专用账号/ACL 的明确授权；只读探针可独立运行
- privileged 夹具仅通过 AD 主组具有 Domain Admins 权限，写绑定使用修复后的 provision-directory.py 配置，原生测试必须验证该路径被保护

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
go test -tags portal_native ./modules/user_portal/portal -run TestNativeAccountManagement
node test-env/playwright/user-portal-account-management.mjs
```

有效性验证入口：

```bash
go test -tags portal_native ./modules/user_portal/portal -run TestNativeAdministrationReadOnly
go test -tags portal_native ./modules/user_portal/portal -run TestNativeAccountManagement
node test-env/playwright/user-portal-account-management.mjs
```
