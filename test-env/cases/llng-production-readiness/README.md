<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# LLNG 上线验收

> 需求来源：[`llng-production-readiness.md`](../../../modules/llng/dev-docs/requirements/llng-production-readiness.md)
>
> 实施计划：[`llng-production-readiness.md`](../../../modules/llng/dev-docs/plans/llng-production-readiness.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `LLNG-READY-T-001` | e2e | `LLNG-READY-R-002`、`LLNG-READY-R-003`、`LLNG-READY-R-005` | test-env/playwright/llng-portal.spec.mjs<br>test-env/playwright/llng-portal.config.mjs<br>test-env/scripts/llng-directory-fixture.py<br>test-env/scripts/llng-event-fixture.py<br>test-env/scripts/llng-event-status.py<br>modules/user_portal/portal/llng_logout_test.go<br>test-env/playwright/llng-logout-ui.test.mjs |
| `LLNG-READY-T-002` | e2e | `LLNG-READY-R-004`、`LLNG-READY-R-005` | test-env/playwright/llng-passkey.spec.mjs<br>test-env/playwright/llng-passkey.config.mjs<br>test-env/playwright/llng-portal-passkey.spec.mjs<br>test-env/playwright/llng-portal-passkey.config.mjs<br>test-env/scripts/llng-passkey-fixture.py<br>test-env/scripts/llng-directory-fixture.py<br>modules/user_portal/portal/llng_passkey_test.go<br>test-env/playwright/llng-passkey-lifecycle.spec.mjs<br>test-env/playwright/llng-passkey-lifecycle.config.mjs |
| `LLNG-READY-T-003` | e2e | `LLNG-READY-R-005` | test-env/playwright/llng-public-oidc.spec.mjs<br>test-env/playwright/llng-public-oidc.config.mjs |
| `LLNG-READY-T-004` | e2e | `LLNG-READY-R-007` | test-env/playwright/llng-theme.spec.mjs<br>test-env/playwright/llng-theme.config.mjs |
| `LLNG-READY-T-005` | e2e | `LLNG-READY-R-008` | test-env/scripts/llng-recovery-fixture.py<br>test-env/playwright/llng-recovery.spec.mjs<br>test-env/playwright/llng-recovery.config.mjs<br>test-env/scripts/llng-rotation-fixture.py<br>test-env/playwright/llng-key-rotation.spec.mjs<br>test-env/playwright/llng-key-rotation.config.mjs<br>test-env/scripts/llng-client-rotation-fixture.py<br>test-env/playwright/llng-client-rotation.spec.mjs<br>test-env/playwright/llng-client-rotation.config.mjs<br>test-env/scripts/llng-service-rotation-fixture.py<br>test-env/playwright/llng-service-rotation.spec.mjs<br>test-env/playwright/llng-service-rotation.config.mjs<br>test-env/playwright/llng-recovery-failure.spec.mjs<br>test-env/playwright/llng-recovery-failure.config.mjs |
| `LLNG-READY-T-006` | e2e | `LLNG-READY-R-003`、`LLNG-READY-R-010` | test-env/playwright/llng-nextcloud.spec.mjs<br>test-env/playwright/llng-nextcloud.config.mjs<br>test-env/scripts/llng-directory-fixture.py<br>test-env/scripts/llng-event-status.py |
| `LLNG-READY-T-007` | e2e | `LLNG-READY-R-009` | test-env/scripts/llng-resource-benchmark.py<br>test-env/scripts/llng-resource-comparison.py |
| `LLNG-READY-T-008` | e2e | `LLNG-READY-R-001` | test-env/scripts/llng-upgrade-fixture.py<br>test-env/playwright/llng-upgrade.spec.mjs<br>test-env/playwright/llng-upgrade.config.mjs |

## `LLNG-READY-T-001` 目录事件与实际门户会话

- 级别：`e2e`
- 覆盖需求：`LLNG-READY-R-002`、`LLNG-READY-R-003`、`LLNG-READY-R-005`
- 需求复核摘要：`sha256:260280b30597eb0bfe09c4de8bf328439c51cd293ce6d6a86474882904acad0f`
- 实现复核摘要：`sha256:473a5043644591a37eab26cbca5a51e3a064b97fbffcb571081a34484ad11cb0`
- Fixture：finance 独立 Docker、独立 LLNGTEST.INVALID Samba 域、实际 LLNG 2.23.4 与 user_portal/Vikunja，Chromium 虚拟认证器
- 目标能力：`docker`、`ssh`、`browser`、`samba`、`llng`
- Oracle 来源：`api`、`ui`、`filesystem`、`return-value`
- 有效性证明：`counterexample`
- 有效性证据：在同一事务执行拒绝反例和实际会话正例，不以登录页面跳转代替应用用户 API
- 超时：`20m`
- 敏感数据：不得记录密码、token、Cookie、授权码或 Passkey 私钥；禁用 trace/视频，截图只允许匿名主题页面

前置条件：

- 明确指定隔离 socket 与 finance 测试主机
- 测试凭据只在私有 0600 文件中
- 实际服务已完成配置且事件消费者健康

执行步骤：

- 从实际门户建立会话并保存旧 Cookie
- 通过 LDAPS 改变隔离账户和直接/嵌套组
- 阻断回调，重新启用目录账户并重启消费者，再恢复回调
- 保留真实审计记录，依次制造未完整尾行、重复轮转、保留缺口及游标损坏，再恢复事件流
- 执行本人资料、改密及委派写操作
- 执行门户标准 OIDC 双向退出及 IAM 不可用时的本地退出
- 启用已确认 Vikunja 例外后禁用实际账户，重启消费者并验证应用旧会话的实际差异
- 通过真实 LDAPS 锁定和撤销 Admins 成员，事件确认后恢复权限，再重放受影响与另一用户的旧 Cookie

可观察断言：

- 受影响旧 Cookie 在 AD 恢复后仍失效
- 未受影响用户和同用户无关应用保持认证
- 失败回调期间游标保持，重启后继续重试
- 本人和委派写操作保持 CSRF、范围、版本及近期认证边界
- 未完整记录不提交游标，轮转/缺口/游标损坏恢复后真实旧 Cookie 被精确撤销
- 退出回调校验一次性 state 与浏览器绑定；原门户和 IAM Cookie 不再认证，其他用户保持会话
- Vikunja 显式例外持久记录且不阻塞游标；其旧应用会话仍有效，LLNG 与门户旧会话失效
- 锁定及管理员角色撤销清除受影响旧会话，恢复权限不会复活旧 Cookie，另一用户保持登录

反例与故障路径：

- 回调不可达
- 过期成员关系
- 越权目标
- 错误旧密码
- 缺失 CSRF
- 无换行尾记录
- 重复序号
- 日志保留缺口
- 损坏游标
- 退出回调重放/错浏览器/过期
- IAM 不可达时本地退出

清理：

- 恢复临时账户密码策略与组关系
- 关闭浏览器上下文，私钥保留在私有恢复验收文件
- 全部验收后清理独立测试环境

执行入口：

```bash
npm run e2e:llng-portal
go test ./modules/user_portal/portal ./modules/user_portal/hook
node test-env/playwright/llng-logout-ui.test.mjs
```

有效性验证入口：

```bash
npm run e2e:llng-portal
```

## `LLNG-READY-T-002` 原生与实际门户 Passkey

- 级别：`e2e`
- 覆盖需求：`LLNG-READY-R-004`、`LLNG-READY-R-005`
- 需求复核摘要：`sha256:5ba1a1538fd71d31e01ad4dbcd895412d4c27e5cf0fa919440d30358c78ff9d4`
- 实现复核摘要：`sha256:8278b927c847d4823e31a858d676fc120c7893e82b7a8bd97085e2efa96531cb`
- Fixture：finance 独立 Docker、独立 LLNGTEST.INVALID Samba 域、实际 LLNG 2.23.4 与 user_portal/Vikunja，Chromium 虚拟认证器
- 目标能力：`docker`、`ssh`、`browser`、`samba`、`llng`
- Oracle 来源：`api`、`ui`、`filesystem`、`return-value`
- 有效性证明：`counterexample`
- 有效性证据：在同一事务执行拒绝反例和实际会话正例，不以登录页面跳转代替应用用户 API
- 超时：`5m`
- 敏感数据：不得记录密码、token、Cookie、授权码或 Passkey 私钥；禁用 trace/视频，截图只允许匿名主题页面

前置条件：

- 明确指定隔离 socket 与 finance 测试主机
- 测试凭据只在私有 0600 文件中
- 实际服务已完成配置且事件消费者健康

执行步骤：

- 登记两台认证器并使用原生 Passkey 登录实际应用
- 设置强制改密和真正自然到期，核对门户签名登录方式与稳定身份
- 再次使用 Passkey 完成近期认证并验证本人资料写入
- 禁用、锁定、账户到期和设备撤销后再尝试登录
- 删除临时目录账号，复用用户名并重新登记；在恢复基线验收后执行

可观察断言：

- 强制改密和自然到期仍可使用有效 Passkey
- 禁用、锁定、账户到期和已撤销设备拒绝
- 改名保持真实应用用户 ID
- 门户只接受已验证 LLNG ID Token 中的明确 Passkey 声明
- 备用认证器与密码仍可用
- 真实 LDAPS 删除旧身份后旧 SSO 和 Passkey 拒绝；复用用户名不能继承旧凭据，新登记产生不同应用身份

反例与故障路径：

- 错误 nonce
- 非 LLNG 声明
- 缺失/冲突 AMR
- 目录状态未知
- 已删除设备

清理：

- 恢复临时账户密码策略与组关系
- 关闭浏览器上下文，私钥保留在私有恢复验收文件
- 全部验收后清理独立测试环境

执行入口：

```bash
npm run e2e:llng-portal-passkey
npm run e2e:llng-passkey
go test ./modules/user_portal/portal ./modules/llng/hook
npm run e2e:llng-passkey-lifecycle
```

有效性验证入口：

```bash
npm run e2e:llng-portal-passkey
npm run e2e:llng-passkey
go test ./modules/user_portal/portal ./modules/llng/hook
```

## `LLNG-READY-T-003` 公共 OIDC 协议与近期认证

- 级别：`e2e`
- 覆盖需求：`LLNG-READY-R-005`
- 需求复核摘要：`sha256:b6703e69a7e1e0f4d6cbacc7d3f4dcd491fc9f87e245b5693411f24ba6bce4ac`
- 实现复核摘要：`sha256:fe894a31d2088d47388dce2ccca498734694aaf3393566a650bb46afe253c177`
- Fixture：finance 独立 Docker、独立 LLNGTEST.INVALID Samba 域、实际 LLNG 2.23.4 与 user_portal/Vikunja，Chromium 虚拟认证器
- 目标能力：`docker`、`ssh`、`browser`、`samba`、`llng`
- Oracle 来源：`api`、`ui`、`filesystem`、`return-value`
- 有效性证明：`counterexample`
- 有效性证据：在同一事务执行拒绝反例和实际会话正例，不以登录页面跳转代替应用用户 API
- 超时：`5m`
- 敏感数据：不得记录密码、token、Cookie、授权码或 Passkey 私钥；禁用 trace/视频，截图只允许匿名主题页面

前置条件：

- 明确指定隔离 socket 与 finance 测试主机
- 测试凭据只在私有 0600 文件中
- 实际服务已完成配置且事件消费者健康

执行步骤：

- 执行授权码与 S256 PKCE 交换
- 验证签名 issuer、audience、nonce 和 auth_time
- 保留 SSO 再发起近期认证并完成真实交互
- 禁用目录账户后尝试旧 token 和新授权

可观察断言：

- 公开客户端正确回跳
- 再次认证产生真实近期 auth_time
- 本人 API 绑定原主体及当前目录状态
- PKCE 降级、错误 verifier 和匿名 API 拒绝

反例与故障路径：

- plain PKCE
- 缺失 challenge
- 错误 verifier
- 禁用后的旧 token

清理：

- 恢复临时账户密码策略与组关系
- 关闭浏览器上下文，私钥保留在私有恢复验收文件
- 全部验收后清理独立测试环境

执行入口：

```bash
npm run e2e:llng-public-oidc
```

有效性验证入口：

```bash
npm run e2e:llng-public-oidc
```

## `LLNG-READY-T-004` 原生主题与语言

- 级别：`e2e`
- 覆盖需求：`LLNG-READY-R-007`
- 需求复核摘要：`sha256:fca6382b3d9d928b34fcbde0ec7c902ef67294e107c37b9e502251c1ecd62dc6`
- 实现复核摘要：`sha256:0810ee640f4a45ced5cdb3fac2d06e99ccb887c1b5a3a7ef9b56edd8fa46d967`
- Fixture：finance 独立 Docker、独立 LLNGTEST.INVALID Samba 域、实际 LLNG 2.23.4 与 user_portal/Vikunja，Chromium 虚拟认证器
- 目标能力：`docker`、`ssh`、`browser`、`samba`、`llng`
- Oracle 来源：`api`、`ui`、`filesystem`、`return-value`
- 有效性证明：`counterexample`
- 有效性证据：在同一事务执行拒绝反例和实际会话正例，不以登录页面跳转代替应用用户 API
- 超时：`5m`
- 敏感数据：不得记录密码、token、Cookie、授权码或 Passkey 私钥；禁用 trace/视频，截图只允许匿名主题页面

前置条件：

- 明确指定隔离 socket 与 finance 测试主机
- 测试凭据只在私有 0600 文件中
- 实际服务已完成配置且事件消费者健康

执行步骤：

- 分别以中英文桌面和 390px 视口打开原生登录页
- 切换固定明暗与系统主题
- 检查密码和 Passkey 原生控件及匿名截图

可观察断言：

- 主题持久化并跟随系统变化
- 本地品牌图标加载
- 窄屏无横向溢出
- 中英文文案和原生认证控件可见
- 无浏览器脚本错误

反例与故障路径：

- 深色模式
- 窄屏
- 异步原生语言载入

清理：

- 恢复临时账户密码策略与组关系
- 关闭浏览器上下文，私钥保留在私有恢复验收文件
- 全部验收后清理独立测试环境

执行入口：

```bash
npm run e2e:llng-theme
```

有效性验证入口：

```bash
npm run e2e:llng-theme
```

## `LLNG-READY-T-005` 空库恢复既有 Passkey、签名密钥与服务凭据轮换

- 级别：`e2e`
- 覆盖需求：`LLNG-READY-R-008`
- 需求复核摘要：`sha256:95e448a52c61edad49771cd6230d151bd012f18ed4a4ccbf49736b26808d3016`
- 实现复核摘要：`sha256:c9e1b6a5d14079a953b736a45f4b9cdcb1f8b5169de28e4a7711543f4be4bf63`
- Fixture：finance 独立 Docker、独立 LLNGTEST.INVALID Samba 域、实际 LLNG 2.23.4 与 user_portal/Vikunja，Chromium 虚拟认证器
- 目标能力：`docker`、`ssh`、`browser`、`samba`、`llng`
- Oracle 来源：`api`、`ui`、`filesystem`、`return-value`
- 有效性证明：`counterexample`
- 有效性证据：空目标和备份完整性前置检查；恢复后用原有认证器和实际应用验证，不用新登记代替恢复
- 超时：`20m`
- 敏感数据：不得记录密码、token、Cookie、授权码或 Passkey 私钥；禁用 trace/视频，截图只允许匿名主题页面

前置条件：

- 明确指定隔离 socket 与 finance 测试主机
- 测试凭据只在私有 0600 文件中
- 实际服务已完成配置且事件消费者健康

执行步骤：

- 用既有门户及备用 Passkey 登录，记录稳定应用身份和公开签名密钥摘要
- 停止写入后备份配置、会话数据库、签名密钥及消费者状态
- 恢复至新空数据库与新配置目录，核对表内容摘要
- 再次使用原有凭据登录并核对应用身份和签名公钥
- 在隔离环境演练原生 OIDC 密钥过渡、提前撤旧反例与回滚
- 通过原生接口分两端轮换 Nextcloud OIDC 客户端凭据，验证不一致失败、恢复与回滚
- 使用专用 LDAP 绑定账户、克隆数据库与强制 SCRAM 的专用角色，分别轮换目录/数据库和 LLNG 配置
- 截断私有备份，分别验证恢复校验和守卫与空库原生导入失败，然后用既有门户 Passkey 重新登录
- 只在隔离域将旧密码宽限窗口缩短到 1 分钟，验证窗口内旧/新密码均可用，到期后仅新密码可用，最后恢复原策略

可观察断言：

- 恢复前后数据库表内容一致
- 两名用户既有 Passkey 仍有效
- 真实应用内部用户 ID 与目录 anchor 保持
- 原签名公钥保持
- 原数据库与配置保留供回退
- 原生预发布/提升签名密钥期间旧令牌继续可验，实际门户和 Nextcloud 接受新签名；提前撤旧会破坏旧令牌，回滚恢复原签名
- 签名切换前预发布公钥并确认 Nextcloud 缓存已更新；客户端旧秘密失效、两端不一致拒绝登录，更新与回滚后身份不变
- 新建 LDAPS/SCRAM 连接拒绝旧凭据、接受新凭据；实际应用新登录保持身份并写入新库，回滚原配置后可用
- 损坏备份与部分导入失败不改变原库/原配置；原签名公钥、真实门户身份和已登记 Passkey 仍可用
- Samba 旧密码宽限按实测期限退役，不把改密成功误判为立即撤销；原域策略在 finally 恢复

反例与故障路径：

- 恢复目标数据库已经存在时拒绝
- 备份摘要不符时拒绝
- 未完成通知必须原样保留，不能作为备份清理项
- 退役目录/数据库密码被拒绝；不得以 trust 认证或既有连接证明轮换成功
- 备份校验和不符、原生归档导入失败

清理：

- 恢复临时账户密码策略与组关系
- 关闭浏览器上下文，私钥保留在私有恢复验收文件
- 全部验收后清理独立测试环境
- 恢复 LLNG 原共享服务配置；专用账户、库和 SCRAM 规则仅保留于隔离环境供复核

执行入口：

```bash
npm run e2e:llng-recovery
npm run e2e:llng-key-rotation
npm run e2e:llng-client-rotation
npm run e2e:llng-service-rotation
npm run e2e:llng-recovery-failure
```

有效性验证入口：

```bash
npm run e2e:llng-recovery
```

## `LLNG-READY-T-006` 实际 Nextcloud OIDC 双向退出与目录通知

- 级别：`e2e`
- 覆盖需求：`LLNG-READY-R-003`、`LLNG-READY-R-010`
- 需求复核摘要：`sha256:5e7ff4b5d31c8a34ece16635a9377574a4314763ad45636d7973ea15cd16ff28`
- 实现复核摘要：`sha256:1dab06dc4f7f6bac287da7ff9a7750a2f85ab6a774369655fa656632095202f8`
- Fixture：finance 独立 Docker 与 Samba 域；LLNG 2.23.4；实际 Nextcloud 34.0.2-r12 / user_oidc 8.11.0 / user_ldap
- 目标能力：`docker`、`ssh`、`browser`、`samba`、`llng`
- Oracle 来源：`api`、`ui`、`filesystem`、`return-value`
- 有效性证明：`counterexample`
- 有效性证据：真实旧 Cookie API 拒绝与同用户另一 session/另一用户成功对照；停用期间不向受影响应用发送请求
- 超时：`15m`
- 敏感数据：不得记录密码、token、Cookie、授权码或 Passkey 私钥；禁用 trace/视频，截图只允许匿名主题页面

前置条件：

- 明确指定隔离 socket 与 finance 测试主机
- 测试凭据只在私有 0600 文件中
- 实际服务已完成配置且事件消费者健康

执行步骤：

- 建立同用户两个独立会话及另一用户会话
- 分别从应用和 LLNG 发起 OIDC 退出
- 经 LDAPS 停用用户，等待原生授权撤销及 Nextcloud 回调确认，再启用 AD
- 使用实际旧 Cookie 调用 Nextcloud OCS 用户 API

可观察断言：

- 稳定目录 anchor 成为实际应用 UID
- 用户退出只撤销对应 session，其他 session/用户保留
- 停用事件撤销该用户旧应用会话，重新启用 AD 后旧 Cookie 仍拒绝
- 其他应用不支持通知时，Nextcloud 的通知仍执行且未删除原失败队列

反例与故障路径：

- 已退出旧 Cookie 重用
- 账号重新启用后旧 Cookie 重用
- 其他客户端无后台接收端

清理：

- 恢复临时账户密码策略与组关系
- 关闭浏览器上下文，私钥保留在私有恢复验收文件
- 全部验收后清理独立测试环境

执行入口：

```bash
npm run e2e:llng-nextcloud
```

有效性验证入口：

```bash
npm run e2e:llng-nextcloud
```

## `LLNG-READY-T-007` 同主机 Casdoor 与 LLNG 资源比较

- 级别：`e2e`
- 覆盖需求：`LLNG-READY-R-009`
- 需求复核摘要：`sha256:203d3c1b304bdc0bbd206f5b245c1ccc3d69338178540034e447dd8016450aff`
- 实现复核摘要：`sha256:fe4f6d32baaaaa4278fe7edc8f6a45ff86183cfa02f5bcf9757e8d9dc5d8f4e2`
- Fixture：finance 独立 Docker、相同 Samba/PostgreSQL 与单个临时用户；依次启动 Provider 和必需事件消费者
- 目标能力：`docker`、`ssh`、`browser`、`samba`、`llng`
- Oracle 来源：`api`、`ui`、`filesystem`、`return-value`
- 有效性证明：`counterexample`
- 有效性证据：每次请求校验真实授权码、签名令牌存在和 UserInfo subject；错误及缺失会话不能计作成功
- 超时：`20m`
- 敏感数据：不得记录密码、token、Cookie、授权码或 Passkey 私钥；禁用 trace/视频，截图只允许匿名主题页面

前置条件：

- 仅在 finance 指定独立 socket 的夹具运行，生产服务不参与
- 私有 secrets.json 提供单个临时用户凭据；两者使用同一目录及数据库服务
- 先完成浏览器故障验收，避免测试之间互相中断

执行步骤：

- 核对容器、共享依赖与资源限制
- 分别预热四次，采样空闲 30 秒
- 并发二执行 60 次全新 OIDC 授权、换码及 UserInfo
- 恢复原有隔离服务并记录共享主机负载

可观察断言：

- 两个 Provider 均完成 60 次请求并保持同一 subject
- 计入必需目录消费者及共享依赖
- 记录 working set、swap、CPU、延迟和版本基线；不将微基准当作容量承诺

反例与故障路径：

- 认证、换码或 UserInfo 失败即停止并记录失败
- 缺少必需进程或就绪超时不得记为完成

清理：

- 恢复原先运行的隔离服务
- 只保存聚合报告，不保存令牌、Cookie 或密码

执行入口：

```bash
python3 test-env/scripts/llng-resource-comparison.py
```

有效性验证入口：

```bash
python3 test-env/scripts/llng-resource-comparison.py
```

## `LLNG-READY-T-008` 历史 2.23.2 到 2.23.4 的真实持久状态升级

- 级别：`e2e`
- 覆盖需求：`LLNG-READY-R-001`
- 需求复核摘要：`sha256:8b92ff0ab15074374feb82440b67810f1bf6de60502b24fd4b8d65629bdbfb96`
- 实现复核摘要：`sha256:73a475ec8b69d6fccce53a09024438e5a86b9342187e7d5eca62319881907c87`
- Fixture：finance 独立 Docker、历史提交 058d5c2 的 2.23.2 Module、当前候选 2.23.4、单独配置目录和数据库
- 目标能力：`docker`、`ssh`、`browser`、`samba`、`llng`
- Oracle 来源：`api`、`ui`、`filesystem`、`return-value`
- 有效性证明：`counterexample`
- 有效性证据：使用固定历史源码镜像和实际包版本，先在旧版登录，再原地升级持久配置和数据库；不以新版改标签冒充旧基线
- 超时：`20m`
- 敏感数据：不得记录密码、token、Cookie、授权码或 Passkey 私钥；禁用 trace/视频，截图只允许匿名主题页面

前置条件：

- 明确指定 finance 独立 Docker socket，其他验收暂停
- 原候选容器和数据库保留，旧版本使用全新配置和数据库
- 私密凭据仅通过 0600 文件加载

执行步骤：

- 启动历史 Module，核对实际原生包版本
- 通过真实 Vikunja 登录记录应用身份、中央会话与公开签名密钥
- 以当前候选镜像升级同一份原生配置与数据库
- 核对数据库内容、旧中央会话和实际应用身份，再恢复原候选服务

可观察断言：

- 实际旧版本为 2.23.2，新版本为 2.23.4
- 升级启动不丢弃原生会话表内容
- 旧中央会话、公钥和实际应用身份保持
- 原候选测试环境能够恢复，旧基线证据保留

反例与故障路径：

- 拒绝复用已存在的升级目标数据库
- 旧版启动或升级失败时保留基线并恢复原候选服务

清理：

- 恢复原候选容器及目录消费者；旧版独立数据和私密日志保留在测试目录

执行入口：

```bash
npm run e2e:llng-upgrade
```

有效性验证入口：

```bash
npm run e2e:llng-upgrade
```
