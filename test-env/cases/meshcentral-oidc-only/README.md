<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# MeshCentral OIDC-only 验收用例

> 需求来源：[`meshcentral-oidc-only.md`](../../../dev-docs/requirements/meshcentral-oidc-only.md)
>
> 实施计划：[`meshcentral-oidc-only.md`](../../../dev-docs/plans/archived/meshcentral-oidc-only.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `MCO-T-001` | unit | `MCO-R-001`、`MCO-R-005` | modules/meshcentral/meshcentral/enforce-oidc-only.test.js<br>internal/runner/server_identity_fixture_test.go |
| `MCO-T-002` | e2e | `MCO-R-002`、`MCO-R-003`、`MCO-R-005` | test-env/scripts/server-authentik-oidc-login-e2e.sh<br>test-env/scripts/server-meshcentral-oidc-only-e2e-lib.sh |
| `MCO-T-003` | e2e | `MCO-R-002`、`MCO-R-004`、`MCO-R-005` | test-env/scripts/server-llng-oidc-login-e2e.sh<br>test-env/scripts/server-meshcentral-oidc-only-e2e-lib.sh |

## `MCO-T-001` 服务端补丁失败关闭与 E2E fixture 配置契约

- 级别：`unit`
- 覆盖需求：`MCO-R-001`、`MCO-R-005`
- 需求复核摘要：`sha256:c8ccbc50102245fdb2d2fd903b6a3cc0b620eabf9f87c8df49f47f7078a34f06`
- 实现复核摘要：`sha256:6563a89a3134c9520a28762cd7b5bc70a5e1da86d7b6bc0378472b4d881b38d1`
- Fixture：固定 MeshCentral 上游登录处理器片段与仓库双 Provider YAML fixture
- 目标能力：`node`、`go`、`module-manifest`
- Oracle 来源：`return-value`、`error-contract`、`filesystem`
- 有效性证明：`counterexample`
- 有效性证据：固定上游片段的锚点和 fixture 所有权被主动破坏时，补丁或导入必须失败
- 超时：`10m`
- 敏感数据：只使用合成源码片段和无 Secret 的 fixture

前置条件：

- 无。

执行步骤：

- 对固定上游片段应用 OIDC-only 补丁并注入锚点变化和重复应用
- 加载 Authentik 与 LLNG fixture 并执行配置导入所有权校验

可观察断言：

- 密码认证器和 HTTP handler 同时被阻断，未知锚点与重复补丁失败
- 双 Provider fixture 不包含 Runner-owned 原始网络输入并可被当前 Module registry 导入

反例与故障路径：

- 缺少固定上游锚点时补丁拒绝构建
- fixture 注入 Runner-owned key 时导入拒绝

清理：

- 单元测试只使用临时目录

执行入口：

```bash
node modules/meshcentral/meshcentral/enforce-oidc-only.test.js
go test ./internal/runner -run TestServerIdentityFixturesUsePublicConfiguration
```

有效性验证入口：

```bash
node modules/meshcentral/meshcentral/enforce-oidc-only.test.js
go test ./internal/runner -run TestServerIdentityFixturesUsePublicConfiguration
```

## `MCO-T-002` Authentik OIDC-only 与账号映射

- 级别：`e2e`
- 覆盖需求：`MCO-R-002`、`MCO-R-003`、`MCO-R-005`
- 需求复核摘要：`sha256:6c24c448188d47a57f8163acd4ad26e352a748e1a2e5c00fc6907b3c88355ab7`
- 实现复核摘要：`sha256:103f898ec20476189bc7e3715b18a8b2973408aa7904038f39238f48a313a691`
- Fixture：finance 专用 Docker daemon 中的 Samba AD、PostgreSQL、Authentik、MeshCentral 与 Traefik
- 目标能力：`docker`、`authentik`、`oidc`、`postgres`
- Oracle 来源：`api`、`database`、`runtime`
- 有效性证明：`counterexample`
- 有效性证据：测试真实提交本地密码 POST 并要求 HTTP 拒绝，同时从数据库读取 OIDC 账号映射
- 超时：`2h`
- 敏感数据：管理员密码只从容器环境传入子进程，不写报告

前置条件：

- 专用 network namespace、Docker socket、data root 和 workspace 已准备

执行步骤：

- 验证匿名首页、登录页 OIDC 策略和密码 POST 拒绝
- 使用目录管理员完成 Authentik Authorization Code 登录
- 查询 MeshCentral 持久化账号并比对 identity anchor、显示名和 site-admin

可观察断言：

- 密码登录返回 404 且只公开 OIDC
- 真实回调建立会话，账号 identity anchor、显示名与目录一致
- 管理员组映射为 MeshCentral site-admin

反例与故障路径：

- 构造的本地密码 POST 必须拒绝

清理：

- 停止 Authentik deployment；保留 0600 脱敏报告

执行入口：

```bash
bash test-env/scripts/server-authentik-oidc-login-e2e.sh
```

有效性验证入口：

```bash
bash test-env/scripts/server-authentik-oidc-login-e2e.sh
```

## `MCO-T-003` LLNG OIDC-only 与账号映射

- 级别：`e2e`
- 覆盖需求：`MCO-R-002`、`MCO-R-004`、`MCO-R-005`
- 需求复核摘要：`sha256:be4c2735a5f3b086fb7f248491063fad8e02057cc38e9717118271946b7bd5b2`
- 实现复核摘要：`sha256:6e5542dcd0fbf05b0fff2502a860a27b1546267d419372c8922eb7ba27b4c336`
- Fixture：finance 专用 Docker daemon 中的 Samba AD、PostgreSQL、LLNG、MeshCentral 与 Traefik
- 目标能力：`docker`、`llng`、`oidc`、`postgres`
- Oracle 来源：`api`、`database`、`runtime`
- 有效性证明：`counterexample`
- 有效性证据：测试真实提交本地密码 POST 并要求 HTTP 拒绝，同时从数据库读取 OIDC 账号映射
- 超时：`2h`
- 敏感数据：管理员密码只从容器环境传入子进程，不写报告

前置条件：

- Authentik deployment 已停止且 LLNG 使用独立 workspace 和容器前缀

执行步骤：

- 验证匿名首页、登录页 OIDC 策略和密码 POST 拒绝
- 使用目录管理员完成 LLNG Authorization Code 登录
- 查询 MeshCentral 持久化账号并比对 identity anchor、显示名和 site-admin

可观察断言：

- 密码登录返回 404 且只公开 OIDC
- 真实回调建立会话，账号 identity anchor、显示名与目录一致
- 管理员组映射为 MeshCentral site-admin

反例与故障路径：

- 构造的本地密码 POST 必须拒绝

清理：

- 停止 LLNG deployment；恢复测试副本 revision；保留 0600 脱敏报告

执行入口：

```bash
bash test-env/scripts/server-llng-oidc-login-e2e.sh
```

有效性验证入口：

```bash
bash test-env/scripts/server-llng-oidc-login-e2e.sh
```
