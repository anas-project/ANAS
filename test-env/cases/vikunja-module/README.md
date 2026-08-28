<!-- Generated from cases.yml by cmd/gen-test-case-docs. DO NOT EDIT. -->

# Vikunja Module 发布验收用例

> 需求来源：[`vikunja-module.md`](../../../dev-docs/requirements/vikunja-module.md)
>
> 实施计划：[`vikunja-module.md`](../../../dev-docs/plans/vikunja-module.md)
> 本文由同目录 `cases.yml` 生成；修改用例后运行 `go run ./cmd/gen-test-case-docs`。

## 覆盖总览

| 用例 ID | 级别 | 需求 ID | 实现 |
| --- | --- | --- | --- |
| `VIK-T-001` | unit | `VIK-R-001`、`VIK-R-002`、`VIK-R-003`、`VIK-R-004`、`VIK-R-005`、`VIK-R-006`、`VIK-R-007`、`VIK-R-008`、`VIK-R-009`、`VIK-R-010`、`VIK-R-011`、`VIK-R-012`、`VIK-R-013`、`VIK-R-014`、`VIK-R-015`、`VIK-R-016`、`VIK-R-028` | modules/vikunja/hook/main_test.go<br>modules/vikunja/vikunja/entrypoint_test.go<br>test-env/scripts/test-static.sh |
| `VIK-T-002` | e2e | `VIK-R-017` | test-env/scripts/server-vikunja-e2e.sh |
| `VIK-T-003` | e2e | `VIK-R-018` | test-env/scripts/server-vikunja-e2e.sh |
| `VIK-T-004` | e2e | `VIK-R-019` | test-env/scripts/server-vikunja-e2e.sh |
| `VIK-T-005` | e2e | `VIK-R-020` | test-env/scripts/server-vikunja-oidc-e2e.sh<br>test-env/playwright/vikunja-authentik-matrix-browser.spec.mjs |
| `VIK-T-006` | e2e | `VIK-R-021` | test-env/scripts/server-vikunja-oidc-e2e.sh<br>test-env/playwright/vikunja-llng-browser.spec.mjs |
| `VIK-T-007` | e2e | `VIK-R-022` | test-env/playwright/vikunja-browser.spec.mjs |
| `VIK-T-008` | e2e | `VIK-R-023` | test-env/scripts/server-vikunja-e2e.sh |
| `VIK-T-009` | e2e | `VIK-R-024` | test-env/scripts/server-vikunja-e2e.sh |
| `VIK-T-010` | e2e | `VIK-R-025` | test-env/scripts/server-vikunja-e2e.sh |
| `VIK-T-011` | e2e | `VIK-R-026` | test-env/scripts/server-vikunja-e2e.sh<br>test-env/scripts/server-vikunja-rotation-failure-e2e.sh |
| `VIK-T-012` | e2e | `VIK-R-027` | test-env/scripts/server-vikunja-e2e.sh<br>test-env/playwright/vikunja-load-browser.spec.mjs |

## `VIK-T-001` Module 静态、Hook、入口与生成文档契约

- 级别：`unit`
- 覆盖需求：`VIK-R-001`、`VIK-R-002`、`VIK-R-003`、`VIK-R-004`、`VIK-R-005`、`VIK-R-006`、`VIK-R-007`、`VIK-R-008`、`VIK-R-009`、`VIK-R-010`、`VIK-R-011`、`VIK-R-012`、`VIK-R-013`、`VIK-R-014`、`VIK-R-015`、`VIK-R-016`、`VIK-R-028`
- 需求复核摘要：`sha256:4e6ab7db4302d264c32e0ecc479254154a12cc6e58c306a9bb5a9ea8af71a809`
- 实现复核摘要：`sha256:ab566e8458f72ac3c203300c599dbec13fc99b16aa50728f03cecae6e253e43e`
- Fixture：Vikunja Module manifest、Hook 输入和入口临时目录
- 目标能力：`go`、`shell`、`module-manifest`
- Oracle 来源：`return-value`、`filesystem`、`error-contract`
- 有效性证明：`counterexample`
- 有效性证据：单元 fixture 注入错误 binding、过期 candidate、未知语言和符号链接并要求契约拒绝
- 超时：`10m`
- 敏感数据：仅使用确定性合成 Secret，断言中不输出值

前置条件：

- 无。

执行步骤：

- 执行 Vikunja Hook 与入口 Go 测试
- 执行仓库静态测试并检查 manifest、Compose 和生成文档

可观察断言：

- 固定版本、网络、数据库、IAM、Secret、本地化和文档边界通过静态与 Hook 测试
- 入口健康探针有界重试并安全创建受管目录
- MariaDB 映射、应用组、登出配置和凭据 candidate 遵守通用契约

反例与故障路径：

- 注入非 OIDC binding、其他 Consumer binding、过期 candidate、不支持语言和目录符号链接

清理：

- Go 测试删除临时目录；静态脚本只使用仓库测试工作区

执行入口：

```bash
go test ./modules/vikunja/...
./test-env/scripts/test-static.sh
```

有效性验证入口：

```bash
go test ./modules/vikunja/...
./test-env/scripts/test-static.sh
```

## `VIK-T-002` 多架构构建与 amd64 真实运行

- 级别：`e2e`
- 覆盖需求：`VIK-R-017`
- 需求复核摘要：`sha256:29abaea4db944e5d396c1d9e92e01067e34b67cfe3dc2cb7d3829120697a8c60`
- 实现复核摘要：`sha256:8e3ecb7ad3d297b4e75079800d862e4c09814a142b8d896be5fc5ba8a14ce7c3`
- Fixture：专用 Vikunja Docker daemon 中的 amd64 部署与 arm64 交叉构建
- 目标能力：`docker`、`buildx`、`amd64`、`arm64-cross-build`
- Oracle 来源：`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：隔离门禁使用错误 Docker socket 时在构建和运行前失败
- 超时：`2h`
- 敏感数据：构建日志 mode 0600，不记录 registry 凭据

前置条件：

- 专用非生产 Docker socket和Vikunja workspace 已准备

执行步骤：

- 从固定源码交叉构建 arm64 镜像并检查架构
- 检查当前 amd64 部署的健康、重启次数和进程身份

可观察断言：

- arm64 镜像架构正确
- amd64 容器健康、无异常重启且业务进程非 root

反例与故障路径：

- 默认或生产 Docker socket 被隔离门禁拒绝

清理：

- 删除本次临时容器和 workspace；构建镜像允许保留复用

执行入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh build-runtime
```

有效性验证入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh build-runtime
```

## `VIK-T-003` PostgreSQL 安装、重启、升级与回滚边界

- 级别：`e2e`
- 覆盖需求：`VIK-R-018`
- 需求复核摘要：`sha256:ec9a6186b215569a656b8fa74b6e74b91eca70df8297afaead173b02ef408cfb`
- 实现复核摘要：`sha256:6b295076e700690a0bb7b0f349b089a33a3efc65bc8f14365f0fedce40e6fdc7`
- Fixture：PostgreSQL、Authentik 与 Vikunja 隔离部署
- 目标能力：`docker`、`postgres`、`btrfs`
- Oracle 来源：`database`、`runtime`
- 有效性证明：`counterexample`
- 有效性证据：测试构造旧凭据代际并要求部署拒绝，同时从数据库核对重启和往返后的对象
- 超时：`1h`
- 敏感数据：deployment 报告 mode 0600，不记录数据库或 OIDC Secret

前置条件：

- 当前与兼容历史 deployment ID 均属于同一测试 workspace

执行步骤：

- 验证 PostgreSQL 空库和应用/数据库重启
- 执行固定版本升级、兼容回退和不兼容代际拒绝

可观察断言：

- 空库启动、应用和数据库重启后对象保持
- 固定版本升级及兼容回退保持数据且不触碰不兼容代际

反例与故障路径：

- 旧凭据代际 deployment 必须以 credential_store_mismatch 安全拒绝

清理：

- 恢复目标 deployment 并清理测试对象

执行入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh postgres
bash test-env/scripts/server-vikunja-e2e.sh upgrade
```

有效性验证入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh postgres
bash test-env/scripts/server-vikunja-e2e.sh upgrade
```

## `VIK-T-004` MariaDB 安装、映射、重启与版本往返

- 级别：`e2e`
- 覆盖需求：`VIK-R-019`
- 需求复核摘要：`sha256:aede0e7385b4c2d35cc0670370934cbe7506824a016e6f83c88350f78bdea8d9`
- 实现复核摘要：`sha256:f759abdf8523595a1874b827a1721cd5c58d221e878fd44f6f54f88d17e815f0`
- Fixture：MariaDB、LLNG 与 Vikunja 隔离部署
- 目标能力：`docker`、`mariadb`、`btrfs`
- Oracle 来源：`database`、`runtime`
- 有效性证明：`counterexample`
- 有效性证据：测试提供越界 workspace 和不兼容代际并要求回退拒绝
- 超时：`1h`
- 敏感数据：报告只记录对象计数和摘要

前置条件：

- r3 与 r4 deployment ID 均属于同一测试 workspace

执行步骤：

- 验证 MariaDB 空库、mysql 映射和应用/数据库重启
- 执行 r4 到 r3 到 r4 的固定镜像往返

可观察断言：

- relational_database 的 mariadb binding 映射为上游 mysql
- 应用和数据库重启以及 r4 到 r3 到 r4 往返保持对象计数

反例与故障路径：

- 回退目标不属于当前 workspace 或凭据代际不兼容时拒绝

清理：

- 恢复 r4 deployment 并清理测试对象

执行入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh mariadb
bash test-env/scripts/server-vikunja-e2e.sh upgrade
```

有效性验证入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh mariadb
bash test-env/scripts/server-vikunja-e2e.sh upgrade
```

## `VIK-T-005` Authentik OIDC 五账号浏览器矩阵

- 级别：`e2e`
- 覆盖需求：`VIK-R-020`
- 需求复核摘要：`sha256:deea37b29ff56047ad3576787db389708738067935fc721792000e3d07957d05`
- 实现复核摘要：`sha256:6536fd7349c46d4fab59d4f2341989dbaf5c2653e22d6097eb7587ef51707574`
- Fixture：Samba AD、Authentik、PostgreSQL、Vikunja 与 Chromium
- 目标能力：`docker`、`authentik`、`playwright`、`chromium`
- Oracle 来源：`ui`、`api`、`database`
- 有效性证明：`counterexample`
- 有效性证据：两类无授权账号真实完成浏览器流程并证明拒绝且数据库没有 JIT 用户
- 超时：`30m`
- 敏感数据：使用一次性账号；截图、trace、video 关闭；报告 mode 0600

前置条件：

- 一次性目录账号可创建且浏览器通过测试服务器隧道访问

执行步骤：

- 创建五类一次性目录账号并执行 Authentik 授权码矩阵
- 用五个隔离 Chromium context 验证允许、拒绝和 JIT

可观察断言：

- 直接组、APP_all 和管理员账号允许并完成 JIT
- 无组与禁用账号拒绝且不创建本地用户
- 本地登录和开放注册保持关闭

反例与故障路径：

- 无应用组和禁用目录账号分别验证授权拒绝

清理：

- 删除五个一次性目录账号、浏览器 session 和脱敏报告之外的临时文件

执行入口：

```bash
bash test-env/scripts/server-vikunja-oidc-e2e.sh authentik
npm run e2e:vikunja-authentik-matrix-browser
```

有效性验证入口：

```bash
bash test-env/scripts/server-vikunja-oidc-e2e.sh authentik
npm run e2e:vikunja-authentik-matrix-browser
```

## `VIK-T-006` LLNG OIDC 五账号浏览器矩阵

- 级别：`e2e`
- 覆盖需求：`VIK-R-021`
- 需求复核摘要：`sha256:1d00bb4f40bb6d3f384cfb4431526261f53a4366ff600f4e6426927477c61309`
- 实现复核摘要：`sha256:36be9e4bdaca703ec7e4fe0ef4f341cb1a2e1ba1f26e6483cfdfd8e33b039ede`
- Fixture：Samba AD、LLNG、MariaDB、Vikunja 与 Chromium
- 目标能力：`docker`、`llng`、`playwright`、`chromium`
- Oracle 来源：`ui`、`api`、`database`
- 有效性证明：`counterexample`
- 有效性证据：两类无授权账号真实完成浏览器流程并证明拒绝且数据库没有 JIT 用户
- 超时：`30m`
- 敏感数据：使用一次性账号；浏览器报告脱敏并设为 mode 0600

前置条件：

- 一次性目录账号可创建且 LLNG fixture 独立运行

执行步骤：

- 创建五类一次性目录账号并执行 LLNG 授权码矩阵
- 用五个隔离 Chromium context 验证允许、拒绝和 JIT

可观察断言：

- 直接组、APP_all 和管理员账号允许并完成 JIT
- 无组与禁用账号拒绝且本地注册关闭

反例与故障路径：

- 无应用组和禁用目录账号分别验证授权拒绝

清理：

- 删除一次性目录账号和浏览器临时 session

执行入口：

```bash
bash test-env/scripts/server-vikunja-oidc-e2e.sh llng
npm run e2e:vikunja-llng-browser
```

有效性验证入口：

```bash
bash test-env/scripts/server-vikunja-oidc-e2e.sh llng
npm run e2e:vikunja-llng-browser
```

## `VIK-T-007` RP-Initiated Logout 与 IAM 故障降级

- 级别：`e2e`
- 覆盖需求：`VIK-R-022`
- 需求复核摘要：`sha256:4f63d65543e6c0d7fe4603c90337135df424db79c384f1a4fd356f4cdf2fe785`
- 实现复核摘要：`sha256:cfc148b4d615c936554afc80e8a32161b166c23ca3924e42251207d29689ade6`
- Fixture：Authentik、Vikunja 与两个隔离 Chromium context
- 目标能力：`docker`、`authentik`、`playwright`、`chromium`
- Oracle 来源：`ui`、`network`、`runtime`
- 有效性证明：`fault-injection`
- 有效性证据：测试主动暂停 IAM，再从浏览器存储与网络请求独立确认本地会话仍被清除
- 超时：`15m`
- 敏感数据：不记录授权 URL、cookie、ID token、截图或 trace

前置条件：

- 一次性账号已能通过 OIDC 登录

执行步骤：

- 正常登录后执行应用发起登出并检查 RP logout 参数
- 暂停 Authentik 后再次登出并独立检查本地 session 清理

可观察断言：

- 正常登出先清除本地 session 并携带 id_token_hint 和 post-logout URI
- IAM 不可用时本地 session 仍在时限内清除

反例与故障路径：

- 暂停 Authentik 后执行应用登出并独立观察本地存储和 logout 请求

清理：

- finally 恢复 Authentik 并删除一次性账号/session

执行入口：

```bash
npm run e2e:vikunja-browser
```

有效性验证入口：

```bash
npm run e2e:vikunja-browser
```

## `VIK-T-008` 空 workspace 灾难恢复

- 级别：`e2e`
- 覆盖需求：`VIK-R-023`
- 需求复核摘要：`sha256:af9567532cc04bcc77717e0e2c0caeccb2f064eedce79e258f6aa4b9b77bd70b`
- 实现复核摘要：`sha256:19b7cef82db4748db05ce930738f23914892762f6f92c028682f912bb0449489`
- Fixture：Btrfs snapshot backup、源 workspace 与自动生成的空恢复 workspace
- 目标能力：`docker`、`btrfs`、`postgres`、`oidc`
- Oracle 来源：`filesystem`、`database`、`api`、`runtime`
- 有效性证明：`counterexample`
- 有效性证据：恢复 fixture 缺少结构或摘要不符时，恢复在接触源数据前失败
- 超时：`1h`
- 敏感数据：session 和恢复日志 mode 0600，报告只记录摘要

前置条件：

- 一次性 OIDC session 文件为 mode 0600

执行步骤：

- 创建业务对象并生成、验证 Btrfs snapshot backup
- 恢复到空 workspace、启动并逐项核对数据和 OIDC
- 返回源 deployment 并严格删除恢复 fixture

可观察断言：

- project、task、comment、attachment、OIDC 关联、API token 和 webhook 在同一恢复点保持
- 恢复 workspace 可启动并完成新的 OIDC 登录

反例与故障路径：

- 恢复缺少结构或摘要不一致时立即失败且不删除源数据

清理：

- 恢复源 deployment 并删除恢复 workspace、备份、snapshot、测试对象和 token

执行入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh restore
```

有效性验证入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh restore
```

## `VIK-T-009` API、附件与 CalDAV smoke

- 级别：`e2e`
- 覆盖需求：`VIK-R-024`
- 需求复核摘要：`sha256:71dd022c63fd2715999d0edd7e150f592aed3d46c2dd9af45e2d9851c3586f2c`
- 实现复核摘要：`sha256:09859bd09c598fa955e0b68bcd7ccd37d4fb09d69923f7a912564874d8c53a22`
- Fixture：健康 Vikunja deployment 和一次性最小权限 API token
- 目标能力：`docker`、`curl`、`caldav`
- Oracle 来源：`api`、`filesystem`
- 有效性证明：`counterexample`
- 有效性证据：无效或过期 token 真实调用 API 时必须拒绝，成功路径则回读业务对象和附件内容
- 超时：`15m`
- 敏感数据：token 不进入日志；调用方提供的 token 不由脚本删除

前置条件：

- 调用方提供一次性 JWT 或自有 API token

执行步骤：

- 创建或使用最小权限 API token 执行业务对象 CRUD
- 执行附件上传下载以及 CalDAV discovery/read

可观察断言：

- project、task、comment 和 attachment 完成创建、读取、更新与删除
- CalDAV discovery 和任务读取成功

反例与故障路径：

- 无效或过期 token 不能被当作成功

清理：

- 删除生成的业务对象和由脚本创建的 API token

执行入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh api
```

有效性验证入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh api
```

## `VIK-T-010` Webhook HMAC 与错误签名拒绝

- 级别：`e2e`
- 覆盖需求：`VIK-R-025`
- 需求复核摘要：`sha256:c619dbd6ac88cec61b9faaa35e2a25e22aae04490512284c9c7931c356cdc98c`
- 实现复核摘要：`sha256:f30e8c5c1fba0934fe12ca6deb92bfe42aab0d6380789c52d93a3b5c4a70c528`
- Fixture：隔离 network namespace 中的 Vikunja 和无日志 webhook receiver
- 目标能力：`docker`、`webhook`、`hmac`
- Oracle 来源：`network`、`api`、`report`
- 有效性证明：`counterexample`
- 有效性证据：receiver 对同一原始 body 的错误 HMAC 返回 401，正确 HMAC 才允许投递
- 超时：`15m`
- 敏感数据：webhook Secret 只存在于 mode 0600 临时目录且不写日志

前置条件：

- 测试网络允许为 receiver 添加本次临时 alias

执行步骤：

- 启动隔离无日志 receiver 并创建 webhook/task
- 验证正确 HMAC 投递和错误签名拒绝

可观察断言：

- 原始 body 的 HMAC-SHA256 签名验证通过
- 错误签名返回 401 且普通日志和报告不含 Secret

反例与故障路径：

- 使用同一 body 发送错误签名

清理：

- 删除 webhook、任务、project、token、receiver 和网络 alias

执行入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh webhook
```

有效性验证入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh webhook
```

## `VIK-T-011` 凭据轮换成功事务与 candidate 失败恢复

- 级别：`e2e`
- 覆盖需求：`VIK-R-026`
- 需求复核摘要：`sha256:22c58293b5531ae291b2b7edee90e38bec595a36204d8a2ea5922d3ed0dd1961`
- 实现复核摘要：`sha256:5b280f0865e3b0e3fb93b7dcc14e5c84914f6ee153df7f26249a60d40736dc1e`
- Fixture：可轮换 Vikunja deployment、Secret Store 和一次性失败 Docker wrapper
- 目标能力：`docker`、`credential-rotation`
- Oracle 来源：`runtime`、`filesystem`、`report`
- 有效性证明：`fault-injection`
- 有效性证据：fail-once Docker wrapper 破坏 candidate 启动并要求 previous、Store 和 live 摘要全部恢复
- 超时：`30m`
- 敏感数据：只比较 Secret 摘要，stderr 与 JSON 报告 mode 0600

前置条件：

- 当前与 previous deployment 均属于测试 workspace

执行步骤：

- 依次执行单凭据、Module 和 deployment 轮换
- 注入一次 candidate compose 失败并核对 previous 恢复

可观察断言：

- 两项单凭据、Module 和 deployment 轮换保持 live/Store 同步并各提交一次
- candidate 启动失败时 previous 恢复且 active、Store 和 live secret 摘要不变

反例与故障路径：

- 用 fail-once Docker wrapper 注入 candidate compose up 失败

清理：

- 删除 wrapper 和临时目录并保证 previous deployment 健康

执行入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh rotation
bash test-env/scripts/server-vikunja-rotation-failure-e2e.sh
```

有效性验证入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh rotation
bash test-env/scripts/server-vikunja-rotation-failure-e2e.sh
```

## `VIK-T-012` 1k/10k task 负载与真实浏览器首屏

- 级别：`e2e`
- 覆盖需求：`VIK-R-027`
- 需求复核摘要：`sha256:c40163be83690445ceda370835b10d70b27f306677a5cccf1079aac04f502430`
- 实现复核摘要：`sha256:fc4ab3f993c0a484a94bb754fead4ca5989c46635527ece516e7fb6b8877d80b`
- Fixture：记录规格的测试服务器、八个临时 project 和 Chromium
- 目标能力：`docker`、`playwright`、`chromium`、`load`
- Oracle 来源：`ui`、`api`、`runtime`、`report`
- 有效性证明：`counterexample`
- 有效性证据：浏览器和 API 必须回读精确 10000 个任务；过期 JWT 路径还要求刷新后清理临时 token
- 超时：`2h`
- 敏感数据：浏览器报告脱敏且 mode 0600，不记录 JWT、密码、cookie 或截图

前置条件：

- 提供一次性 OIDC session、JWT 和 load hold file

执行步骤：

- 在八个 project 生成 1k、10k task 并采集资源和 API 指标
- 保持 10k fixture，执行真实 Chromium 冷首屏和任务总数检查
- 释放 hold file 并等待服务端删除全部测试数据

可观察断言：

- 记录 idle、1k 和 10k task 的 CPU、内存、写入吞吐和 API 延迟
- Chromium 冷首屏登录后确认 API 精确返回 10000 个任务并记录时延

反例与故障路径：

- JWT 过期时刷新一次性 OIDC session 后仍必须完成 token 清理

清理：

- 释放 hold file，删除八个 project、10000 个任务和生成的 API token

执行入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh load
npm run e2e:vikunja-load-browser
```

有效性验证入口：

```bash
bash test-env/scripts/server-vikunja-e2e.sh load
npm run e2e:vikunja-load-browser
```
