---
doc_type: plan
status: implementing
created: 2026-08-26
updated: 2026-08-27
---

# Casdoor IAM Provider 实施计划

验收依据是[Casdoor IAM Provider 集成要求](/requirements/casdoor-iam)的需求矩阵。通用架构依据为
[IAM Capability 设计](/architecture/iam-capability-design)；没有另建 Casdoor 专属架构文档。
M1—M3 已完成；真实 Samba/Casdoor 目录收敛、OIDC Authorization Code 和 SAML SP-initiated
登录均已通过隔离 E2E。当前里程碑为 M4：OIDC 会话撤销与固定版本 SAML SLO 能力决策。

服务器地址与连接信息只登记在 Git 忽略的 `docs/private/test-servers.md`。远程测试只能在用户明确
指定的服务器执行，并必须使用独立 network namespace、Docker socket、data-root 和 workspace，
不得接触服务器现有容器或数据。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：Module、数据库、协议注册、恢复账号与静态边界 | R-001—R-005、R-017—R-018、R-020—R-021、R-026、R-030、R-032—R-033、R-040 | 已完成 |
| M2：Samba 目录事件订阅与运行时 E2E | R-006—R-014 | 已完成 |
| M3：删除/停用、Group 门禁、永久锚点与 OIDC/SAML 登录 | R-015—R-016、R-019、R-022—R-025 | 已完成 |
| M4：OIDC 会话撤销与 SAML SLO | R-027—R-029、R-031 | 实施中 |
| M5：恢复、备份、升级、密钥轮换与发布 | R-034—R-039 | 未开始 |

## 2. 已完成落地快照

- M1 已固定 Casdoor `3.143.0-r6`，接入 PostgreSQL Resource、Traefik、OIDC/SAML registration、
  稳定 Secret 和 `admin_casdoor` 本地恢复账号；Hook/Helper 单元测试覆盖已声明边界。
- M2 已接入 Samba 持久目录事件日志，完成事件过滤、防抖、最小间隔、日志轮换、不完整尾记录、
  失败重试、原子游标和受限 profile 刷新。
- 2026-08-26 在用户明确指定服务器的隔离 Docker daemon 完成新增用户、profile 更新、突发合并和
  游标重启恢复 E2E；测试脚本清理一次性目录账号。
- M3 已落地并通过目录权威 E2E 的实现：目录订阅器按永久锚点收敛改名/停用/删除，把 Samba 锚点写入
  Casdoor `ExternalId` 而不修改其不可变 User ID，并直接查询受信任
  LDAPS 计算递归受管组，并以 Casdoor Group/Role/Application Permission 执行 `ALLOW_GROUPS`；
  OIDC custom claim 与 SAML 属性均从同一永久锚点和受管 Role 发出。
- 2026-08-26 在同一隔离环境复验事件订阅，并完成目录权威 E2E：直接/递归组加入与移除、账号
  停用/恢复、改名和删除均收敛；恢复与改名保持 Casdoor 不可变 User ID 和 Samba `ExternalId`。
- r6 从 Casdoor `3.143.0` 固定提交和校验过的源码包构建，只补充 SAML `$user.displayName` 与
  `$user.externalId` 模板；OIDC access/refresh token 有效期固定为 1 小时/30 天。
- 2026-08-27 在同一隔离环境完成真实 OIDC 与 SAML E2E：RS256/JWKS 和 assertion 签名、协议字段、
  目录属性、六类授权矩阵、停用/撤权/改名/删除、永久锚点应用建号以及 `Admins` 应用权限与降权均通过。
- 当前仍不发布未经验证的 SAML SLO；M3 通过不等于 M4 会话撤销和 M5 发布生命周期验收通过。

## 3. M3 实施检查表

- [x] 扩展目录同步，使删除、禁用和组移除在 Casdoor 本地影子记录及新凭据签发上确定性收敛。
- [x] 为通用 `ALLOW_GROUPS` 建立 Casdoor 可证明等价的 Application/Permission/Role 策略；若固定版本
  无法表达，则停止发布级评估并回到需求范围决策。
- [x] 让 OIDC 与 SAML 使用可由 Samba `anasIdentityAnchor` 证明的稳定身份映射，覆盖改名不重复建号。
- [x] 建立真实 OIDC Authorization Code 和 SAML HTTP fixture，检查签名、claim、允许/拒绝、
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
| R-014 | `test-env/scripts/server-casdoor-directory-events-e2e.sh` | 用户明确指定服务器；专用 netns、workspace、Docker socket 与 data-root | 2026-08-26 | **通过**：停止订阅时保持陈旧，恢复后新增导入、profile 刷新、三事件合并为一次同步、游标重启不重放与清理通过 |
| R-015 | `server-casdoor-directory-authority-e2e.sh` + OIDC/SAML 协议 E2E | 同一隔离 Samba AD + Casdoor + Consumer | 2026-08-27 | **通过**：停用/删除均清组并禁止新登录与 token/assertion；恢复后复用原永久身份 |
| R-016 | `server-casdoor-directory-authority-e2e.sh` + OIDC/SAML 协议 E2E | 同一隔离 Samba AD + Casdoor + Consumer | 2026-08-27 | **通过**：直接/递归组加入与移除传播，撤权后 OIDC/SAML 均停止签发应用凭据 |
| R-019 | `test-env/scripts/server-casdoor-oidc-e2e.sh` | 真实 confidential Consumer HTTP fixture + RS256/JWKS verifier | 2026-08-27 | **通过**：Authorization Code 交换、签名/claim/有效期、允许/拒绝、永久锚点建号及最终应用权限通过 |
| R-022 | `test-env/scripts/server-casdoor-saml-e2e.sh` | 固定 Go SAML SP fixture + metadata trust/assertion verifier | 2026-08-27 | **通过**：签名、request ID、Destination、Issuer、Audience、NameID、时效、属性和应用权限通过 |
| R-023 | OIDC/SAML 协议 E2E | Samba 六类账号矩阵 + 两类 Consumer | 2026-08-27 | **通过**：直接 `APP_nextcloud`、`APP_all`、`Admins`、递归组允许；无组和禁用账号拒绝 |
| R-024 | OIDC/SAML 协议 E2E | Samba 改名 + 永久锚点键控的 Consumer fixture | 2026-08-27 | **通过**：改名保持 Samba 锚点和 Casdoor 身份，Consumer 更新原应用账号且账号总数不增加 |
| R-025 | OIDC/SAML 协议 E2E | 管理员授权、普通组不提权、`Admins` 移组降权 | 2026-08-27 | **通过**：`Admins` 映射 `app-admin`；保留 `APP_nextcloud` 并移出 `Admins` 后同一应用账号降为 `user` |
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
bash -n test-env/scripts/server-casdoor-oidc-e2e.sh
bash -n test-env/scripts/server-casdoor-saml-e2e.sh
(cd test-env/fixtures/casdoor-saml-consumer && go test ./...)
```

远程 E2E 必须由用户明确指定服务器，并显式设置该测试专用的 `DOCKER_HOST`；脚本首先 source
`server-require-isolated-docker.sh`。报告写入 Git 忽略且权限为 `0600` 的 `test-env/reports/`。

## 8. 当前阻塞与风险

- OIDC back-channel 目前只有注册和清理单元证据；真实通知、管理员删 session 和应用 Cookie 失效未验收。
- 固定版本 SAML SLO 能力尚未验证，因此当前正确行为仍是不发布 SLO，而不是把该方向标记为支持。
- r6 固定源码镜像已在 amd64 隔离环境构建并通过 OIDC/SAML 冷启动；arm64 构建、升级和安全回滚属于
  M5，尚不能据此声明多架构发布验收完成。
- 恢复管理员、备份恢复、签名密钥/client credential 轮换和运维迁移文档仍属 M5 发布阻塞项。
