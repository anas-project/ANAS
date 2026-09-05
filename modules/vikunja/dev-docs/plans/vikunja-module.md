---
doc_type: plan
status: implementing
created: 2026-08-23
updated: 2026-08-25
---

# Vikunja Module 实施计划

验收依据是[Vikunja Module 集成要求](../requirements/vikunja-module.md)的需求矩阵。M1—M4 已完成。
服务器地址与连接信息只登记在 Git 忽略的 `docs/private/test-servers.md`；测试使用
独立 network namespace、containerd、Docker socket、data-root 和 workspace，不接触宿主现有容器。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：Module、Contract/IAM/Secret、文档与静态门禁 | R-001—R-016、R-028 | 已完成 |
| M2：多架构构建、PostgreSQL、Authentik 与登出 | R-017—R-018、R-020、R-022 | 已完成 |
| M3：MariaDB 与 LLNG | R-019、R-021 | 已完成 |
| M4：恢复、API/Webhook/CalDAV、轮换与负载 | R-023—R-027 | 已完成 |

## 2. M2 检查表

- [x] 建立 `anas-vikunja-test` network namespace、专用 containerd 与独立 Docker daemon，并验证
  data-root/socket guard 及 host-network 容器实际位于该 namespace。
- [x] 新增最小 PostgreSQL + Authentik + Vikunja fixture，并完成冷启动与健康/非 root 检查。
- [x] r4 amd64 源码构建与真实运行通过；r4 arm64 从固定源码交叉构建通过，镜像创建时间
  `2026-08-24T15:02:17+08:00`，未复用 r3 的旧标签。
- [x] Authentik curl 与真实 Chromium 均完成五账号矩阵：直接组、`APP_all`、管理员允许，无组与
  禁用账号拒绝；三个允许账号在浏览器流程完成 JIT，本地登录与开放注册保持关闭。
- [x] Chromium 正常 RP logout 已验证本地 session、`id_token_hint`/post-logout URI；暂停
  Authentik 后本地 session 仍在 5 秒内清除并发出一次 `POST /api/v1/user/logout`。r4 构建内
  Vitest 2/2 与真实 Chromium 2/2 均通过。
- [x] 验证应用/数据库重启与数据持久性，记录固定版本升级和回滚边界；r3→r4 升级后对象
  计数不变，旧凭据代际 r3 回退被安全拒绝，兼容 revision 3 artifact 往返保持
  `data_touched=false` 并恢复到 r4。

## 3. M3 检查表

- [x] 新增 MariaDB + LLNG + Vikunja fixture，断言上游 database type 为 `mysql`。
- [x] 完成空库、应用/数据库重启和数据持久性测试；最终 deployment
  `20260824T114212Z-cdea4011` 健康且零重启。
- [x] LLNG curl 授权码与真实 Chromium 均完成五账号矩阵、JIT 和本地注册关闭测试。
- [x] MariaDB 部署完成真实 r4→r3→r4 往返：运行时 image ref/ID 分别断言为 r4、r3、r4，
  8 类对象计数 `6|6|0|0|0|0|0|0` 不变，两次 rollback 均 `data_touched=false`，最终 r4 healthy、零重启。

## 4. M4 检查表

- [x] 用 API token 创建 project、task、comment、attachment，并完成 CalDAV 发现/读取。
- [x] 启动隔离 webhook receiver，验证 HMAC-SHA256、错误签名拒绝和日志无 Secret。
- [x] 从空 workspace 恢复数据库、附件、Secret Store 和 deployment metadata；复核 project、
  task、comment、attachment、OIDC 关联/重新登录、API token 与 webhook，源栈和临时子卷清理通过。
- [x] 两项单凭据、`--module vikunja` 与 `--all` 的真实 candidate 通过；Vikunja 专属一次性
  candidate `compose up` 失败返回 `previous_restored`，active/Store/live secret digest 不变且 previous
  runtime 恢复 healthy、零重启。
- [x] 已生成分布在 8 个 project 的 1k/10k task 样本并记录资源、写入吞吐与 API 延迟；真实
  Chromium 在 10k 样本下确认任务总数并记录冷首屏 `1,153 ms`、API 探针 `383.1 ms`，报告 mode
  `0600`，测试数据和 token 清理通过。

## 5. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-017 | `test-env/scripts/server-vikunja-e2e.sh build-runtime` | amd64 运行 + arm64 交叉构建 | 2026-08-24 | **通过**：r4 amd64 真实运行；r4 arm64 固定源码交叉构建，image `sha256:8e8bf7e2…` |
| R-018 | `server-vikunja-e2e.sh postgres` + `upgrade` | PostgreSQL + Authentik | 2026-08-24 | **通过**：r3→r4 后 8 类对象计数不变；真实 r3 凭据代际不兼容回退安全拒绝，兼容 revision 3 artifact 往返 `data_touched=false`，最终恢复 r4 |
| R-019 | `server-vikunja-e2e.sh mariadb` + `upgrade` | MariaDB + LLNG | 2026-08-25 | **通过**：空库、`mysql` 映射、应用/数据库重启和持久性通过；真实 r4→r3→r4 image 往返保持 8 类对象计数且 `data_touched=false`，最终 r4 healthy、零重启 |
| R-020 | `server-vikunja-oidc-e2e.sh authentik` + `vikunja-authentik-matrix-browser.spec.mjs` | Authentik + Samba AD + Chromium | 2026-08-25 | **通过**：curl 5/5、Chromium 5/5，允许/拒绝/JIT/注册关闭全部验证 |
| R-021 | `server-vikunja-oidc-e2e.sh llng` + `vikunja-llng-browser.spec.mjs` | LLNG + Samba AD + Chromium | 2026-08-24 | **通过**：curl 5/5、Chromium 5/5；三类允许账号 JIT，无组/禁用账号未建号 |
| R-022 | `test-env/playwright/vikunja-browser.spec.mjs` | Authentik 正常与不可用 | 2026-08-24 | **通过**：r4 正常 RP logout 与 IAM-down 真实 Chromium 2/2 通过；脱敏报告 mode 0600 |
| R-023 | `test-env/scripts/server-vikunja-e2e.sh restore` | Btrfs snapshot backup → 空 workspace | 2026-08-24 | **通过**：结构 verify 7/7，deployment/Secret digest/8 类对象计数一致；全部业务对象、OIDC 重新登录、API token、webhook 通过，源栈及测试资产清理通过 |
| R-024 | `test-env/scripts/server-vikunja-e2e.sh api` | API token + attachment + CalDAV | 2026-08-24 | r4 通过；project/task/comment/attachment 清理，API token 残留为 0 |
| R-025 | `test-env/scripts/server-vikunja-e2e.sh webhook` | 隔离签名 receiver | 2026-08-24 | r4 通过；HMAC、错误签名 401、Secret 日志检查和清理通过 |
| R-026 | `anas credential rotate` + `server-vikunja-rotation-failure-e2e.sh` | 单项/Module/deployment/失败恢复 | 2026-08-25 | **通过**：四种成功事务、会话影响/OIDC 复验、失败 candidate previous 恢复与 Store 不提交通过 |
| R-027 | `server-vikunja-e2e.sh load` + `vikunja-load-browser.spec.mjs` | 4 vCPU/3.23 GiB，8 projects，1k/10k tasks + Chromium | 2026-08-25 | **通过**：1k 12.66 task/s；第二轮 10k 为 11.04 task/s、API p50/p95 0.287/0.379 s、64.91 MiB；Chromium 冷首屏 1,153 ms、API 383.1 ms 并确认 10,000 tasks |

## 6. 验证命令

```bash
go test ./...
go run ./cmd/gen-module-docs --check
go run ./cmd/gen-contract-docs --check
npm run docs:check-requirements
npm run docs:requirement-status
npm run docs:check-requirement-status
npm run docs:build
bash -n test-env/scripts/server-vikunja-e2e.sh test-env/scripts/server-vikunja-oidc-e2e.sh
python3 test-env/scripts/server-vikunja-webhook-receiver_test.py
npm run e2e:vikunja-browser
npm run e2e:vikunja-authentik-matrix-browser
npm run e2e:vikunja-llng-browser
npm run e2e:vikunja-load-browser
```

服务器执行必须显式设置 `DOCKER_HOST=unix:///run/anas-vikunja-test-docker.sock`，且每个脚本先
source `server-require-isolated-docker.sh`。原始报告写入 Git 忽略的 `test-env/reports/`。

## 7. 当前阻塞

- 当前服务器只能完成 arm64 交叉构建，不能提供 arm64 原生启动证据；R-017 规定的多架构构建和
  amd64 真实运行均已完成。
- r3 的 IAM-down 登出阻断已由 r4 关闭：固定源码补丁单测 2/2、正常与暂停 Authentik 的真实
  Chromium 2/2 均通过。E2E 同时修复了两个测试误判源：用户名包含 `logout` 时的模糊控件匹配，
  以及 IAM 不可达时 Playwright 等待导航完成导致的 click 超时。
- r4 arm64 后台重跑已取回完整 PASS 与新镜像元数据；构建日志保持 mode `0600`。五个一次性目录
  账号、会话文件和误同步脚本副本均已清理，数据库一次性 API token 残留为 `0`。
- PostgreSQL r3→r4 固定版本升级与回退边界已完成。真实 r3 deployment 因凭据 Store 已从 generation
  3 轮换到 4 而被 `credential_store_mismatch` 安全拒绝；同代际兼容 revision 3 artifact 的回退与
  返回 r4 均未触碰数据。该兼容 artifact 的 Compose 曾在历史测试中被改为 r4 image，因此只证明
  deployment/revision 回退语义，不作为 r3 binary 成功回退运行的证据。
- 测试树传输一度把 Samba build context 目录 mode 从本地 `0755` 降为远端 `0750`，导致候选和
  同标签 previous image 都不可启动；修正测试副本权限并验证干净构建内 `/etc`、`/usr`、`/var`
  可遍历后完成正式升级。仓库内 Samba 源目录始终为 `0755`，未因此修改 Module 源码。
- MariaDB/LLNG 空库、重启、映射、LLNG 浏览器矩阵与真实 r4→r3→r4 固定版本往返均已有证据；
  回滚时运行的 r3 image ID 为 `sha256:67b754e9…`，返回 r4 为 `sha256:2f4b0991…`。
- R-027 的 idle/1k/10k 资源、写入吞吐、API 延迟和真实 Chromium 首屏指标均已有可复现证据。
