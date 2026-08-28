---
doc_type: plan
status: done
created: 2026-08-26
updated: 2026-08-27
---

# Casdoor IAM Provider 实施计划

验收依据是[Casdoor IAM Provider 集成要求](../../requirements/casdoor-iam.md)的需求矩阵。通用架构依据为
[IAM Capability 设计](../../../docs/architecture/iam-capability-design.md)；没有另建 Casdoor 专属架构文档。
M1—M5 已完成；真实 Samba/Casdoor 目录收敛、OIDC/SAML 登录、OIDC 用户/管理员会话撤销、
恢复管理员、空 workspace 备份恢复、多架构生命周期与受管凭据轮换均已通过隔离 E2E。固定版本
SAML SLO 的不发布决策已有源码审计，Module 生命周期已提升为 `release`。

服务器地址与连接信息只登记在 Git 忽略的 `docs/private/test-servers.md`。远程测试只能在用户明确
指定的服务器执行，并必须使用独立 network namespace、Docker socket、data-root 和 workspace，
不得接触服务器现有容器或数据。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M1：Module、数据库、协议注册、恢复账号与静态边界 | R-001—R-005、R-017—R-018、R-020—R-021、R-026、R-030、R-032—R-033、R-040 | 已完成 |
| M2：Samba 目录事件订阅与运行时 E2E | R-006—R-014 | 已完成 |
| M3：删除/停用、Group 门禁、永久锚点与 OIDC/SAML 登录 | R-015—R-016、R-019、R-022—R-025 | 已完成 |
| M4：OIDC 会话撤销与 SAML SLO | R-027—R-029、R-031 | 已完成 |
| M5：恢复、备份、升级、密钥轮换与发布 | R-034—R-039 | 已完成 |

## 2. 已完成落地快照

- M1 已固定 Casdoor `3.143.0`，接入 PostgreSQL Resource、Traefik、OIDC/SAML registration、
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
- r8 从 Casdoor `3.143.0` 固定提交和校验过的源码包构建，受控补丁只扩展 SAML
  `$user.displayName`/`$user.externalId` 模板、OIDC exact `sid` 用户/管理员 back-channel、两分钟
  Logout Token、失败可观测性和 PostgreSQL 保留列安全查询；OIDC access/refresh token 有效期固定为
  1 小时/30 天。
- 2026-08-27 在同一隔离环境完成真实 OIDC 与 SAML E2E：RS256/JWKS 和 assertion 签名、协议字段、
  目录属性、六类授权矩阵、停用/撤权/改名/删除、永久锚点应用建号以及 `Admins` 应用权限与降权均通过。
- 2026-08-27 真实 OIDC logout Consumer E2E 已覆盖用户正常退出、管理员无浏览器精确删除 session、
  同用户多 session/其他用户隔离、原 Cookie 撤销、Logout Token 签名与字段、重放拒绝以及测试后
  Consumer 配置恢复。
- 固定版本源码只有 SAML assertion `SessionIndex` 生成，没有 `LogoutRequest`、`LogoutResponse` 或
  Single Logout 消费路由，因此继续不发布 SLO endpoint/binding。
- M5 已完成 `admin_casdoor` 真实登录、成功轮换和注入失败回滚；候选 bcrypt 应用后失败时恢复旧值，
  Secret Store 和 active deployment 保持不变。
- Btrfs snapshot 备份已恢复到空 workspace；PostgreSQL、签名材料、Consumer Secret、订阅游标、
  本地管理员 inventory 和 deployment metadata 均恢复，原 Samba 锚点、Casdoor `sub` 与 JWKS 签名保持。
- amd64 最新 r8 已完成真实冷启动、直接容器重启、r9 测试 revision 升级及 `data_touched=false` 制品
  回滚；arm64 使用 build-platform 原生交叉编译和固定摘要静态 QEMU 非特权运行探针，未修改宿主 binfmt。
- 签名密钥轮换验证新 `kid`、旧 token 一小时重叠信任；Portal client secret 轮换验证新值可用、旧值
  立即拒绝；注入失败时上一 deployment、应用值与 Store generation 全部恢复。

## 3. M3 实施检查表

- [x] 扩展目录同步，使删除、禁用和组移除在 Casdoor 本地影子记录及新凭据签发上确定性收敛。
- [x] 为通用 `ALLOW_GROUPS` 建立 Casdoor 可证明等价的 Application/Permission/Role 策略；若固定版本
  无法表达，则停止发布级评估并回到需求范围决策。
- [x] 让 OIDC 与 SAML 使用可由 Samba `anasIdentityAnchor` 证明的稳定身份映射，覆盖改名不重复建号。
- [x] 建立真实 OIDC Authorization Code 和 SAML HTTP fixture，检查签名、claim、允许/拒绝、
  应用建号、管理员映射与撤权结果。

## 4. M4 实施检查表

- [x] 用声明 back-channel endpoint 的真实 OIDC Consumer 验证用户退出和管理员删除 IAM session。
- [x] 保存登出前应用 Cookie，断言目标会话失效、其他会话不受影响，并验证 Logout Token 字段与重放拒绝。
- [x] 审计固定 Casdoor 版本的 SAML SLO；因没有 LogoutRequest/LogoutResponse 消费路由，不发布
  metadata/binding，也不伪造无法执行的浏览器 E2E。
- [x] 对不支持的登出方向保持字段为空，并同步 README、技术文档和 IAM 支持清单。

## 5. M5 实施检查表

- [x] 运行真实 `admin_casdoor` 登录、成功轮换与失败回滚 E2E。
- [x] 从空 workspace 恢复 PostgreSQL、签名材料、Consumer Secret、订阅游标、账号 inventory 和
  deployment metadata，复验原用户登录及锚点映射。
- [x] 完成 amd64/arm64 固定源码构建、冷启动、重启、升级与安全回滚证据。
- [x] 定义签名密钥和 client credential 的轮换/信任重叠/失败恢复语义，并复验登录及旧凭据失效。
- [x] 补齐运维与迁移文档；全部需求完成后生成索引状态并单独审阅生命周期提升。

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
| R-027 | `test-env/scripts/server-casdoor-oidc-logout-e2e.sh` | 真实 Authorization Code Consumer + back-channel receiver | 2026-08-27 | **通过**：用户正常退出后原应用 Cookie 被拒绝，同用户和其他用户会话不受影响 |
| R-028 | `test-env/scripts/server-casdoor-oidc-logout-e2e.sh` | Casdoor 管理 API + 同用户双 session | 2026-08-27 | **通过**：无用户浏览器参与，按 exact `sid` 删除目标 session 并只撤销目标 Cookie |
| R-029 | `test-env/scripts/server-casdoor-oidc-logout-e2e.sh` | RS256/JWKS verifier + Logout Token 重放负例 | 2026-08-27 | **通过**：`iss/aud/sub/iat/exp/jti/events/sid`、两分钟时效、签名、多会话隔离与重放拒绝通过 |
| R-031 | 固定提交源码审计 + Module 不发布断言 | Casdoor `1ee6deb8…` 路由与 SAML 实现 | 2026-08-27 | **满足条件**：固定版本没有 SLO 消费路径，故不声明 SAML SLO；若未来声明，必须先新增完整签名浏览器 E2E |
| R-034 | `test-env/scripts/server-casdoor-local-admin-e2e.sh` | 真实 Casdoor + PostgreSQL | 2026-08-27 | **通过**：`admin_casdoor` 登录、新密码生效、旧密码失效；应用接收候选后的注入失败恢复旧 bcrypt、Store 与 active deployment |
| R-035 | `test-env/scripts/server-casdoor-restore-e2e.sh` | Btrfs snapshot → 空 workspace | 2026-08-27 | **通过**：数据库、签名、Consumer Secret、游标、inventory 与 deployment metadata 恢复；原锚点、`sub`、JWKS 签名和真实登录保持 |
| R-036 | `test-env/scripts/server-casdoor-lifecycle-e2e.sh` | amd64 真实运行 + arm64 固定源码构建/非特权运行 | 2026-08-27 | **通过**：最新 r8 冷启动/直接重启、r9 测试 revision 升级、`data_touched=false` 回滚和状态保持；arm64 返回 `aarch64` |
| R-037 | `test-env/scripts/server-casdoor-key-rotation-e2e.sh` | 签名密钥、client credential 与真实 Consumer | 2026-08-27 | **通过**：新 `kid` 签发、旧 token 重叠信任；Portal 新 Secret 可用且旧值拒绝；注入失败恢复 deployment、应用值与 Store generation |

## 7. 发布审阅记录

| 需求 ID | 审阅材料 | 日期 | 结果 |
| --- | --- | --- | --- |
| R-038 | `docs/operations/casdoor-iam.md` + 英文镜像 + Module README/技术文档 | 2026-08-27 | **通过**：轮换、恢复、升级/回滚、故障恢复、Provider 切换、弃用和未支持能力均已记录 |
| R-039 | Module/需求/计划/支持清单同步 + 发布门禁 | 2026-08-27 | **通过**：R-001—R-040 强制范围均有证据或明确不支持决策，生命周期提升为 `release` |

## 8. 验证命令

```bash
go test ./modules/casdoor/hook
(cd modules/casdoor/casdoor/helper && go test ./...)
(cd modules/casdoor/casdoor/helper && go vet ./...)
go run ./cmd/gen-module-docs --check
npm run docs:check-requirements
npm run docs:requirement-status
npm run docs:check-requirement-status
npm run docs:check-status
npm run docs:build
bash -n test-env/scripts/server-casdoor-directory-events-e2e.sh
bash -n test-env/scripts/server-casdoor-directory-authority-e2e.sh
bash -n test-env/scripts/server-casdoor-oidc-e2e.sh
bash -n test-env/scripts/server-casdoor-oidc-logout-e2e.sh
bash -n test-env/scripts/server-casdoor-saml-e2e.sh
bash -n test-env/scripts/server-casdoor-local-admin-e2e.sh
bash -n test-env/scripts/server-casdoor-restore-e2e.sh
bash -n test-env/scripts/server-casdoor-lifecycle-e2e.sh
bash -n test-env/scripts/server-casdoor-key-rotation-e2e.sh
(cd test-env/fixtures/casdoor-oidc-logout-consumer && go test ./... && go vet ./...)
(cd test-env/fixtures/casdoor-saml-consumer && go test ./...)
```

远程 E2E 必须由用户明确指定服务器，并显式设置该测试专用的 `DOCKER_HOST`；脚本首先 source
`server-require-isolated-docker.sh`。报告写入 Git 忽略且权限为 `0600` 的 `test-env/reports/`。

## 9. 已知限制与发布观察

- 固定版本 SAML SLO 已完成不支持决策并保持不发布；未来上游新增消费路径时必须重新进入需求与 E2E，
  不能沿用本次条件满足结论声明支持。
- arm64 在指定服务器以固定源码交叉构建并用固定摘要 QEMU 做非特权目标运行探针；真实 IAM 协议、
  数据恢复和升级/回滚运行矩阵在 amd64 隔离部署完成，未声明 arm64 性能或宿主特定结论。
- 制品回滚不会回退数据；R036 只验证 `data_touched=false`。需要回退数据时必须使用独立、显式的
  snapshot restore 流程，不能把两者混为同一操作。
