<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# LLNG Samba 强制改密与 OIDC 回跳

> 需求来源：[`llng-password-change.md`](../../../modules/llng/dev-docs/requirements/llng-password-change.md)
>
> 实施计划：[`llng-password-change.md`](../../../modules/llng/dev-docs/plans/llng-password-change.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `LLNG-PWD-T-001` | e2e | `LLNG-PWD-R-001`、`LLNG-PWD-R-002` | test-env/scripts/server-llng-password-config-e2e.sh |
| `LLNG-PWD-T-002` | e2e | `LLNG-PWD-R-003`、`LLNG-PWD-R-005` | test-env/playwright/llng-password-oidc.spec.mjs<br>test-env/playwright/llng-password-oidc.config.mjs |
| `LLNG-PWD-T-003` | e2e | `LLNG-PWD-R-004`、`LLNG-PWD-R-005` | test-env/playwright/llng-password-oidc.spec.mjs<br>test-env/playwright/llng-password-oidc.config.mjs |

## `LLNG-PWD-T-001` 新安装与已有持久配置收敛

- 级别：`e2e`
- 覆盖需求：`LLNG-PWD-R-001`、`LLNG-PWD-R-002`
- 需求复核摘要：`sha256:4aea6b35581c69ddcb2cc7a4d6a1274a196a6d72a4e37c01ca93a0da0eff9346`
- 实现复核摘要：`sha256:3ca6cd12f47624f14ffd518c3f4091ea3b55e551093d5ac621821dcd5b61b24a`
- Fixture：独立 Docker 中从当前 Module 源码构建的 LLNG，使用真实 PostgreSQL 和 Samba LDAPS
- 目标能力：`docker`、`ssh`、`samba`、`llng`
- Oracle 来源：`return-value`、`filesystem`
- 有效性证明：`counterexample`
- 有效性证据：对真实持久配置注入禁用值，保留配置卷重启
- 超时：`5m`
- 敏感数据：不输出完整 LLNG 配置、目录密码或 OIDC secret

前置条件：

- 显式 test socket 和独立 data-root 已通过 guard
- 新安装的测试 LLNG 已完成启动

执行步骤：

- 读取两个运行时开关
- 将持久值设为 0
- 重启同一个 LLNG 容器
- 等待 Module 完成启动后再次读取

可观察断言：

- 新安装的两个开关都是 1
- 不清空持久配置也能将两个旧值收敛回 1

反例与故障路径：

- 向持久配置写入两个禁用值，要求重启纠正

清理：

- 测试结束删除本次专用 Docker 容器与域数据，保留脱敏日志

执行入口：

```bash
bash test-env/scripts/server-llng-password-config-e2e.sh
```

有效性验证入口：

```bash
bash test-env/scripts/server-llng-password-config-e2e.sh
```

## `LLNG-PWD-T-002` 强制改密后的原始 OIDC 回跳

- 级别：`e2e`
- 覆盖需求：`LLNG-PWD-R-003`、`LLNG-PWD-R-005`
- 需求复核摘要：`sha256:8cfdbe57a381d74713e3fe3d243ab5861db25cd9a2f074c5fb8b6e2383700132`
- 实现复核摘要：`sha256:e0cb72c777fe93897c81553eb253b8b1727c72ba44f53e728bca62018f1512fa`
- Fixture：独立 Samba pwdLastSet=0 用户，真实 LLNG Module 与 Vikunja 2.4.0、TLS 代理
- 目标能力：`docker`、`ssh`、`samba`、`browser`
- Oracle 来源：`api`、`ui`
- 有效性证明：`counterexample`
- 有效性证据：同一浏览器事务先提交错误密码，再完成改密与应用用户 API 验证
- 超时：`3m`
- 敏感数据：关闭 trace、截图、视频；报告不保存密码、token、授权码或 state 值

前置条件：

- 只使用一次性测试域账号
- 浏览器凭据 JSON 文件权限为 0600
- ANAS_TEST_PASSWORD_FLOW=reset

执行步骤：

- 从 Vikunja 发起 OIDC
- 提交错误旧密码
- 提交正确旧密码并修改密码
- 从成功后的登录表单用新密码登录

可观察断言：

- 错误密码不产生改密表单或会话
- 正确旧密码显示 PE25
- 改密成功显示 PE35 且尚无 SSO
- 回跳 state 与原请求一致且只有一次回调
- Vikunja 用户 API 返回 200 和正确用户名

反例与故障路径：

- 错误旧密码
- 不允许重新发起应用 OIDC 请求掩盖 state 丢失

清理：

- 关闭浏览器上下文
- 删除临时凭据
- 清理专用测试栈

执行入口：

```bash
npm run e2e:llng-password-oidc
```

有效性验证入口：

```bash
npm run e2e:llng-password-oidc
```

## `LLNG-PWD-T-003` 自然到期后的原始 OIDC 回跳

- 级别：`e2e`
- 覆盖需求：`LLNG-PWD-R-004`、`LLNG-PWD-R-005`
- 需求复核摘要：`sha256:d70aad83ad51ee5cf11ecf6e5a3fccf6bd7cc89571c81fba7a7637ef33290f20`
- 实现复核摘要：`sha256:e0cb72c777fe93897c81553eb253b8b1727c72ba44f53e728bca62018f1512fa`
- Fixture：独立 Samba 域缩短 maxPwdAge 后真正到期的用户及同一 LLNG/Vikunja 栈
- 目标能力：`docker`、`ssh`、`samba`、`browser`
- Oracle 来源：`api`、`ui`、`return-value`
- 有效性证明：`counterexample`
- 有效性证据：目录计算属性确认真正到期，错误密码反例与实际应用会话正例在同一事务执行
- 超时：`3m`
- 敏感数据：不输出凭据或协议 token；原始执行 JSON 进入 Git 忽略目录

前置条件：

- pwdLastSet 非零且计算属性带 0x800000
- 查询账号密码不参与到期
- ANAS_TEST_PASSWORD_FLOW=expired

执行步骤：

- 确认目录到期标志
- 从 Vikunja 发起 OIDC 并先尝试错误密码
- 使用原密码进入 PE22 表单
- 改密后重新登录并核对原请求回跳

可观察断言：

- 错误密码不产生改密表单或会话
- 正确旧密码显示 PE22
- 改密显示 PE35 后再次认证
- 回跳 state 保持且 Vikunja 用户 API 返回 200

反例与故障路径：

- 错误旧密码
- 不得以 pwdLastSet=0 替代自然过期

清理：

- 恢复测试域 maxPwdAge
- 关闭浏览器和删除临时凭据
- 清理专用测试栈

执行入口：

```bash
npm run e2e:llng-password-oidc
```

有效性验证入口：

```bash
npm run e2e:llng-password-oidc
```
