---
doc_type: plan
status: implementing
created: 2026-08-26
updated: 2026-08-26
---

# Casdoor IAM Provider 实施计划

验收依据是[Casdoor IAM Provider 集成要求](/requirements/casdoor-iam)的需求矩阵。通用架构依据为
[IAM Capability 设计](/architecture/iam-capability-design)；没有另建 Casdoor 专属架构文档。
M1、M2 已完成，当前里程碑为 M3：目录权威收敛、真实登录、Group 门禁与永久锚点。

服务器地址与连接信息只登记在 Git 忽略的 `docs/private/test-servers.md`。远程测试只能在用户明确
指定的服务器执行，并必须使用独立 network namespace、Docker socket、data-root 和 workspace，
不得接触服务器现有容器或数据。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：Module、数据库、协议注册、恢复账号与静态边界 | R-001—R-005、R-017—R-018、R-020—R-021、R-026、R-030、R-032—R-033、R-040 | 已完成 |
| M2：Samba 目录事件订阅与运行时 E2E | R-006—R-014 | 已完成 |
| M3：删除/停用、Group 门禁、永久锚点与 OIDC/SAML 登录 | R-015—R-016、R-019、R-022—R-025 | 实施中 |
| M4：OIDC 会话撤销与 SAML SLO | R-027—R-029、R-031 | 未开始 |
| M5：恢复、备份、升级、密钥轮换与发布 | R-034—R-039 | 未开始 |

## 2. 已完成落地快照

- M1 已固定 Casdoor `3.143.0-r5`，接入 PostgreSQL Resource、Traefik、OIDC/SAML registration、
  稳定 Secret 和 `admin_casdoor` 本地恢复账号；Hook/Helper 单元测试覆盖已声明边界。
- M2 已接入 Samba 持久目录事件日志，完成事件过滤、防抖、最小间隔、日志轮换、不完整尾记录、
  失败重试、原子游标和受限 profile 刷新。
- 2026-08-26 在用户明确指定服务器的隔离 Docker daemon 完成新增用户、profile 更新、突发合并和
  游标重启恢复 E2E；测试脚本清理一次性目录账号。
- M3 已落地待真实 E2E 验收的实现：目录订阅器按永久锚点收敛改名/停用/删除，把 Samba 锚点写入
  Casdoor `ExternalId` 而不修改其不可变 User ID，并直接查询受信任
  LDAPS 计算递归受管组，并以 Casdoor Group/Role/Application Permission 执行 `ALLOW_GROUPS`；
  OIDC custom claim 与 SAML 属性均从同一永久锚点和受管 Role 发出。
- 当前仍不发布未经验证的 SAML SLO；M3 代码通过单元测试不等于真实登录和应用权限验收通过。

## 3. M3 实施检查表

- [x] 扩展目录同步，使删除、禁用和组移除在 Casdoor 本地影子记录及新凭据签发上确定性收敛。
- [x] 为通用 `ALLOW_GROUPS` 建立 Casdoor 可证明等价的 Application/Permission/Role 策略；若固定版本
  无法表达，则停止发布级评估并回到需求范围决策。
- [x] 让 OIDC 与 SAML 使用可由 Samba `anasIdentityAnchor` 证明的稳定身份映射，覆盖改名不重复建号。
- [ ] 建立真实 OIDC Authorization Code 和 SAML 浏览器/HTTP fixture，检查签名、claim、允许/拒绝、
  应用建号、管理员映射与撤权结果。

## 4. M4 实施检查表

- [ ] 用声明 back-channel endpoint 的真实 OIDC Consumer 验证用户退出和管理员删除 IAM session。
- [ ] 保存登出前应用 Cookie，断言目标会话失效、其他会话不受影响，并验证 Logout Token 字段与重放拒绝。
- [ ] 评估固定 Casdoor 版本的 SAML SLO；只有完整签名浏览器流程通过后才发布 metadata/binding。
- [ ] 对不支持的登出方向保持字段为空，并同步 README、技术文档和 IAM 支持清单。

## 5. M5 实施检查表

- [ ] 运行真实 `admin_casdoor` 登录、成功轮换与失败回滚 E2E。
- [ ] 从空 workspace 恢复 PostgreSQL、签名材料、Consumer Secret、订阅游标、账号 inventory 和
  deployment metadata，复验原用户登录及锚点映射。
- [ ] 完成 amd64/arm64 固定源码构建、冷启动、重启、升级与安全回滚证据。
- [ ] 定义签名密钥和 client credential 的轮换/信任重叠/失败恢复语义，并复验登录及旧凭据失效。
- [ ] 补齐运维与迁移文档；全部需求完成后生成索引状态并单独审阅生命周期提升。

## 6. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-014 | `test-env/scripts/server-casdoor-directory-events-e2e.sh` | 用户明确指定服务器，专用 network namespace 与隔离 Docker daemon | 2026-08-26 | **通过**：新增导入、profile 刷新、三事件合并、游标重启恢复与清理通过 |
| R-015 | `test-env/scripts/server-casdoor-directory-authority-e2e.sh` | Samba AD + Casdoor，隔离 Docker daemon | — | 脚本已编写，待执行：删除、停用状态；凭据拒绝仍由登录 E2E 覆盖 |
| R-016 | `test-env/scripts/server-casdoor-directory-authority-e2e.sh` | Samba AD + Casdoor，隔离 Docker daemon | — | 脚本已编写，待执行：直接组、递归组、加入与撤权传播 |
| R-019 | 待新增 `test-env/scripts/server-casdoor-oidc-e2e.sh` | Casdoor + 真实 OIDC Consumer + 浏览器 | — | 待实现 |
| R-022 | 待新增 `test-env/scripts/server-casdoor-saml-e2e.sh` | Casdoor + 真实 SAML SP + 浏览器 | — | 待实现 |
| R-023 | `server-casdoor-oidc-e2e.sh` + `server-casdoor-saml-e2e.sh` | Samba 六类账号矩阵 + OIDC/SAML Consumer | — | 待实现：`APP_*`、`APP_all`、`Admins`、递归组、无组、禁用 |
| R-024 | 待新增 `test-env/scripts/server-casdoor-anchor-e2e.sh` | Samba 改名 + OIDC/SAML Consumer | — | 待实现 |
| R-025 | `server-casdoor-oidc-e2e.sh` + `server-casdoor-saml-e2e.sh` | 管理员授权、普通组不提权、移组撤权 | — | 待实现 |
| R-027 | 待新增 `test-env/scripts/server-casdoor-oidc-logout-e2e.sh` | Casdoor + back-channel OIDC Consumer | — | 待实现：用户正常退出 |
| R-028 | `test-env/scripts/server-casdoor-oidc-logout-e2e.sh` | Casdoor + back-channel OIDC Consumer | — | 待实现：管理员无浏览器删 session |
| R-029 | `test-env/scripts/server-casdoor-oidc-logout-e2e.sh` | Logout Token 负例与多会话隔离 | — | 待实现 |
| R-031 | 待新增 `test-env/scripts/server-casdoor-saml-slo-e2e.sh` | Casdoor + SAML SP + 浏览器 | — | 待评估固定版本能力；通过前不发布 SLO |
| R-034 | 待新增 `test-env/scripts/server-casdoor-local-admin-e2e.sh` | 真实 Casdoor + PostgreSQL | — | 待实现 |
| R-035 | 待新增 `test-env/scripts/server-casdoor-restore-e2e.sh` | Btrfs snapshot → 空 workspace | — | 待实现 |
| R-036 | 待新增 `test-env/scripts/server-casdoor-lifecycle-e2e.sh` | amd64 运行 + arm64 构建/运行 | — | 待实现 |
| R-037 | 待新增 `test-env/scripts/server-casdoor-key-rotation-e2e.sh` | 签名密钥、client credential 与真实 Consumer | — | 待设计和实现 |

## 7. 验证命令

```bash
go test ./modules/casdoor/hook
(cd modules/casdoor/casdoor/helper && go test ./...)
go run ./cmd/gen-module-docs --check
npm run docs:check-requirements
npm run docs:requirement-status
npm run docs:check-requirement-status
npm run docs:check-status
npm run docs:build
bash -n test-env/scripts/server-casdoor-directory-events-e2e.sh
bash -n test-env/scripts/server-casdoor-directory-authority-e2e.sh
```

远程 E2E 必须由用户明确指定服务器，并显式设置该测试专用的 `DOCKER_HOST`；脚本首先 source
`server-require-isolated-docker.sh`。报告写入 Git 忽略且权限为 `0600` 的 `test-env/reports/`。

## 8. 当前阻塞与风险

- Casdoor 的 Group/Role/Application Permission 映射已经单元验证，但直接组、递归组、拒绝、撤权和
  `Admins` 应用权限仍缺真实 Consumer E2E，M3 不能据此标记完成。
- 删除、停用、Group 撤权和永久锚点改名已有确定性收敛实现，最终状态及旧凭据拒绝仍缺真实 E2E 证据。
- OIDC back-channel 目前只有注册和清理单元证据；真实通知、管理员删 session 和应用 Cookie 失效未验收。
- 固定版本 SAML SLO 能力尚未验证，因此当前正确行为仍是不发布 SLO，而不是把该方向标记为支持。
