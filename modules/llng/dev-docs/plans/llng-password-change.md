---
doc_type: plan
status: done
created: 2026-10-23
updated: 2026-10-23
---

# LLNG Samba 强制改密实施计划

验收依据：[需求矩阵](../requirements/llng-password-change.md)。本轮只实现两个原生开关，
验证实际 Vikunja OIDC 回跳；Casdoor 对比、界面和免密码能力作为调研交付，不扩展实现范围。
设计复杂度低：沿用 Module 初始 JSON、启动 CLI 和原生 AD 改密流程，无新产品依赖或服务。

## 需求归属

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：配置接入与持久配置收敛 | LLNG-PWD-R-001、LLNG-PWD-R-002 | 已完成：最终 2.23.4 五项回归通过，历史基线保留 |
| M2：真实 OIDC 回跳及错误密码反例 | LLNG-PWD-R-003、LLNG-PWD-R-004、LLNG-PWD-R-005 | 已完成 |

## 检查表

- [x] 初始配置和每次启动均启用两个开关；未修改上游源码或发布 revision。
- [x] 独立 Docker / network namespace 中验证新配置与已有配置重启。
- [x] 浏览器验证强制改密、自然到期、原 state 回跳及实际应用用户接口。
- [x] 更新中英文 Module 文档、研究报告，完成相关门禁。
- [x] 保存脱敏证据与校验和并清理测试凭据、容器和数据。

## 执行记录

源代码基线 `058d5c2ce87253897e1619b762f42cef9c3e71ee` 加本轮未提交变更。
在用户授权的 finance 隔离环境执行，专用 socket `/run/anas-llng-test-1022.sock`，
专用数据根 `/home/whl/anas-llng-oidc-test-20261022/docker`。生产 Docker 仅只读导出缓存镜像。
原始日志和执行 JSON 保留在隔离目录及 Git 忽略的 `test-env/reports/`，不提交到 Git。

## e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| LLNG-PWD-R-001 | `server-llng-password-config-e2e.sh` | finance 独立 Docker，候选 LLNG Module | 2026-10-23 | **通过**：新安装运行时两个开关均为 1 |
| LLNG-PWD-R-002 | `server-llng-password-config-e2e.sh` | 同上，保留配置卷重启 | 2026-10-23 | **通过**：先写两个 0，重启后均为 1 |
| LLNG-PWD-R-003 | `llng-password-oidc.spec.mjs`，mode=reset | Chromium 151、真实 Vikunja 2.4.0 | 2026-10-23 | **通过**：PE25→PE35→新密码登录，原 state 单次回跳，应用用户 API 200 |
| LLNG-PWD-R-004 | `llng-password-oidc.spec.mjs`，mode=expired | 同上，独立 Samba maxPwdAge=60 秒 | 2026-10-23 | **通过**：pwdLastSet 非零且 computed=8388608；PE22→PE35→新登录，原 state 单次回跳及用户 API 200；computed 清零 |
| LLNG-PWD-R-005 | 同一浏览器脚本两种 mode | 同上 | 2026-10-23 | **通过**：错误旧密码 PE5，无改密表单、SSO Cookie 或应用 token |

两条最终浏览器执行分别 17.9 秒和 11.0 秒。未重启应用授权请求，也未在改密后注入 Cookie。
改密成功不自动创建 SSO 是已明确的原生行为，验收通过不代表自动登录能力。

## 测试基线与证据

| 制品 | 基线 |
| --- | --- |
| LLNG 候选镜像 | `sha256:363e253fc1feefb4dec18237a4327e8c53139620c5d9d4558b8807b66b019951`；当前 Module Dockerfile 与原始入口，包 `2.23.2-1` |
| Samba | `4.23.6-r13`；镜像 `sha256:1d110032df763dd0ba7d1dbe233f4b4f40468322eb49f779677bbfa89a62333e` |
| Vikunja | 上游 `2.4.0`；镜像 `sha256:1a5dd2d410b5359f689c77bbd1c66742cde1993f82d4adfe9aefa400ff1dd833` |
| 数据库与代理 | LLNG 使用 PostgreSQL 16，Vikunja 使用隔离 SQLite；Traefik 3.6，浏览器 Chromium 151 |
| 29 文件证据 manifest | SHA-256 `6dbdaf32d80d8827c7005d13a5e18c4c1adf131fa1ee89c701876c09260070df` |

本地证据：Git 忽略的 `test-env/reports/llng-password-oidc-20261023/`；远端证据副本：
`/home/whl/anas-llng-oidc-test-20261022/evidence/`。`baseline.json` 保存全部镜像 ID 和 LLNG
构建输入摘要，已逐文件核对与最终本地源码一致。`summary.json`、两个 browser JSON、三个
目录状态 JSONL 和 `config-results.log` 给出验收结果。证据未包含测试密码、token 或私钥。
这些是复核副本，不是已发布的共享持久归档。

`cleanup.json` 七项检查全部通过：专用 Docker/containerd 停止、socket 与 namespace 删除、
测试域及镜像存储删除、密码/私钥删除；本地临时凭据删除、SSH 隧道关闭。

## 复现与限制

1. 在专用测试 Docker 中构建 `modules/llng/llng`，按证据目录脚本建立独立 Samba、LLNG、
   PostgreSQL、TLS 代理及 Vikunja。成功构建参数为
   `APT_MIRROR_URL=https://mirrors.tuna.tsinghua.edu.cn`（不追加 `/debian`）。
   `setup-apps.py` 只准备 Vikunja 环境文件；启动应用需把独立 `data/vikunja-files` 目录归属
   UID/GID 1000 并挂载 `/app/vikunja/files`，同时挂载 SQLite `/db` 和测试 CA。
   测试绑定手工提供，未执行完整 ANAS runner apply。
2. `server-llng-password-config-e2e.sh` 需要显式测试 socket 和 `ANAS_TEST_LLNG_CONTAINER`。
3. 浏览器入口 `npm run e2e:llng-password-oidc` 需要 `ANAS_TEST_PASSWORD_FLOW=reset/expired`，
   `ANAS_TEST_CREDENTIAL_FILE` 指向仅含一次性用户凭据的 0600 JSON，形如
   `{ "reset": { "username": "…", "old": "…", "new": "…" }, "expired": { … } }`。
   自然到期必须先确认非零 pwdLastSet 和计算属性；测试后恢复测试域策略。
4. Vikunja 是真实上游应用，但不是完整 ANAS Vikunja Module 的 PostgreSQL/登出补丁基线；
   本次只证明该真实客户端的授权码回跳和用户会话，不覆盖其登出、恢复和全部应用矩阵。
5. 历史密码、最小年龄完整矩阵、其他 OIDC Consumer、SAML、Passkey 与生产升级未执行。
   既有 `server-llng-password-policy-e2e.sh` 增加运行时开关检查，本轮只做语法检查；其完整旧
   策略用例未重跑，不能写为本轮全矩阵通过。

过程修正：测试构建镜像源参数多带了一层 `/debian`，修正参数后构建通过；Vikunja 夹具
补上非 root 可写附件目录；本机下载临时 Playwright Chromium；浏览器脚本处理单 Provider
自动重定向后通过。失败的自动跳转假设报告保留为 `reset-browser-fixture-retry.json`。
没有改动 LLNG 原生认证或改密源码来获得通过。

## 验收结论

本轮五项需求全部通过，M1/M2 完成并归档。已通过 `go test ./modules/llng/...`、Shell/JSON
语法检查、Module 生成文档检查、用例双向溯源、需求覆盖/索引/文档状态检查及 `npm run docs:build`。
文档构建首次发现研究页指向未发布的私有计划路径，改为仓库源链接后通过；未关闭死链检查。
未运行全仓库 Go 测试，未修改其他 Module 的产品行为。

下一步是按发布流程构建、发布并在目标部署应用此改动；不在本次验收内。Casdoor 替代、UI
主题和 Passkey 的补充建议保留为研究，不作为已完成的产品能力。

## 2.23.4 升级回归（已完成）

2026-10-23 按用户要求升级至官方最新稳定版 2.23.4。候选镜像已构建，包版本为
`2.23.4-1`；此前 2.23.2 的五项通过结果仍为历史基线，不外推到新版本。

新版本首次配置通过，但同卷重启后未出现 `/run/llng-configured`，RP registry 尚未重建；
`server-llng-password-config-e2e.sh` 超时失败，LLNG-PWD-R-002 重开。容器没有 OOM 事件。
已定位为 `ping | sed` 提前结束管道触发 SIGPIPE，修复后新配置和持久配置重启均通过。
2.23.4 密码候选的强制改密、自然到期、错误旧密码及实际 Vikunja 回跳均通过，两条浏览器
执行分别为 13.7 秒和 11.6 秒。后续加入 Passkey 等上线功能后须在最终镜像重跑这五项，
因此计划暂不重新归档。新证据目录为
`/home/whl/anas-llng-ready-test-20261023/evidence/`。

上线完整范围另见[上线方案](../../../../docs/research/llng-production-readiness.md)，
整体高复杂度实现已获用户确认，执行结果归上线计划；此处保留改密要求及其独立回归结果。


最终完整镜像 `ba40c3dadf0df099495c9e22404d23991dc354cb1cffab031364bc8019ad6834`
（config `0fc4dd6e16c9d30502aee90f88ef01ae45ab25473fafaff326677f7edc935e9c`）在
2026-10-23 完成全部五项回归：`final-password-config.log` 验证两个开关及同卷重启；
`password-reset-fresh-secret-browser.json`（8.9 秒）和 `password-expired-confirmed-browser.json`
（8.3 秒）验证正确改密、错误旧密码拒绝、改密前无会话、原 state 单次回跳及真实应用 API 200。
自然到期先轮询确认 `pwdLastSet=134371853985672031`、computed=8388608，成功后恢复 42 天策略。
M1/M2 均重新完成；完整上线计划仍单独实施，不能据此标记 LLNG 整体 release。

过程失败保留：`password-reset-final-browser.json` 复用了先前的新密码，测试域密码历史为 3，
改用全新随机目标密码后通过。原生 AD 在这类改密失败后仍显示强制改密状态，本轮未改动上游
错误处理。`password-expired-final-browser.json` 启动过早，目录尚未标记过期；后续明确等待
计算属性再执行。失败均未计为通过，最终脚本和产品代码未为测试放宽密码策略。
证据位于 Git 忽略的 `test-env/reports/llng-ready-20261023/`，完整环境暂保留供上线验收。
