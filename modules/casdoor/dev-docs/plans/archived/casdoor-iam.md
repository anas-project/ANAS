---
doc_type: plan
status: done
created: 2026-08-26
updated: 2026-10-20
---

# Casdoor IAM Provider 实施计划

当前 r10 的正式双架构镜像、OIDC/Nextcloud、目录撤权、受管凭据轮换、空工作区恢复与
生命周期验收已通过，Module 已标为 `release`，见
[2026-10-06 发布验收](casdoor-iam.md)。
ARM64 为 QEMU 目标 helper 执行，SAML 应用会话终止与 SLO 待实现；首次建号延迟保留观察。
以下 M1—M5 的历史快照仍记录 2026-08-26—27 的实现，不用历史 r8 结果代替当前 r10 验收。

验收依据是[Casdoor IAM Provider 集成要求](../../requirements/casdoor-iam.md)的需求矩阵。通用架构依据为
[IAM Capability 设计](../../../../../docs/architecture/iam-capability-design.md)；没有另建 Casdoor 专属架构文档。
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
| 2026-10-20 缺陷：并发 LDAP 导入串行化 | R-041 | 单元通过；finance 新镜像复验待执行，见文末 |

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


## 当前发布范围验收结论（2026-10-06，2026-10-07 整理）

验收依据为当前 Casdoor 需求矩阵。已确认的发布范围以 OIDC 为主，SAML SLO/应用会话终止列为后续待实现，首发不要求旧账号迁移；不把该范围扩大为全部 SAML 功能通过。

正式 Dockerfile amd64/arm64 构建、OIDC 身份/refresh/UserInfo、目录撤权/持久重试与双 client 隔离、真实 Nextcloud 改名原 UID/文件与 Cookie 撤权、恢复管理员/签名密钥/Portal secret 轮换及注入失败恢复、Btrfs 空工作区恢复、冷启动/直接重启/临时 revision 切换和回滚全部通过。可选 SAML 签名协议回归通过，SLO 未执行且未声明。ARM64 仅完成 QEMU 目标 helper 探针，不代表完整 ARM 服务器部署。Module 已标 release，没有据此推送镜像或发布外部发行版。

amd64 镜像：`sha256:9c843a8e6300beb659b05872f8a646715ad50f2bf2c318939e585e4f46559b04`；ARM64 导出索引：`sha256:89432941730c0ea5d351803d16d98ad7dfa9e6a65e51f36163e207303a0cd7a8`。20 个生产文件和 4 个生命周期/轮换脚本摘要与当轮本地一致。首次建号延迟和一次 45 秒回调超时原因仍待定位；后续复验通过不能解释为根因已修复，不自动新增性能门槛，若需要新门槛须先确认需求。

远端证据：finance `/home/whl/anas-casdoor-test-20261003/casdoor-release-20261006/`，包含正式构建、各阶段失败与最终复验。原始凭据和恢复包不进入 Git。本地脱敏 evidence、source-hashes、test-hashes 及原报告已迁入 Git 忽略的 `test-env/reports/2026-10-07-review-consolidation/`，逐文件 SHA-256 见该目录 manifest；本机副本尚未成为共享持久归档。

下一步：当前已确认 OIDC 发布验收范围无阻塞；SAML SLO/应用会话退出仍按已有待办推进，首次建号延迟继续观察。

## 2026-10-20：并发 LDAP 导入产生 `<uid>_` 重复账号

**现象。** finance 门户验收（夹具前缀 `pua7597e3c2`）在 05:56:32—33（+08）新建 7 个 AD 测试用户后，
其中 6 个在 Casdoor 各有两条记录：`<uid>` 与 `<uid>_`，`ldap` 相同，均由 `app-anas-directory`
在同一秒创建。目录订阅器此后每轮报 `Casdoor users X and X_ share identity anchor` 并整批失败，
`anas_finance_casdoor_dirwatch` 持续 unhealthy，目录到 IAM 的同步全部停滞。

**根因。** 上游 `object.SyncLdapUsers` 先按 LDAP UUID 查已有用户，再逐个插入缺失用户，中间无锁。
5 分钟自动同步 goroutine 与订阅器调用的 `sync-ldap-users` API 在同一 Casdoor 进程内并发执行时，
两边都判定新用户缺失；后插入者经 `buildLdapUserName` 重名回退得到 `<uid>_<uidNumber>`，
AD 用户 `uidNumber` 为空，所以是 `<uid>_`。

**方案取舍（复杂度：低）。** 选用 `0009-ldap-user-sync-serialization.patch`：在 `SyncLdapUsers`
入口加进程内互斥锁，自动同步、API 和管理界面三条路径共用该入口，后一次导入必然看到前一次的结果。
新增约 10 行代码和 1 个源码测试，不新增组件、配置或运维步骤；Casdoor 为单副本容器，进程内锁覆盖全部
导入方。未选方案：关闭上游自动同步会违反 R-013 的兜底要求；在订阅器侧加锁覆盖不到上游 goroutine；
由订阅器按锚点预建用户或自动修复 `_` 重复，需要复制上游建号字段或删除账号，逻辑多且有误删风险。
若将来 Casdoor 多副本运行，进程内锁不再充分，须重新评估（例如数据库唯一约束）。

**验证。** 源码测试 `TestAnasLdapUserSyncSerializesImports` 用上游“先查后插”模型并发导入同一用户：
不经锁时复现 `alice`、`alice_`，经 `SyncLdapUsers` 时只得到 `alice`；临时去掉锁后该测试失败。
Dockerfile 构建阶段运行此测试。2026-10-20 在固定源码（0001—0009 依次应用）上本地通过 `go test`
与 `go vet ./object`。R-041：单元通过。finance 尚未部署含 0009 的镜像，真实并发导入复验待执行。

**finance 存量清理。** `anas` 组织开启了软删除，`delete-user` API 只会再次置 `isDeleted`，
6 条 `_` 记录已是 `isDeleted=true`，API 删除无效，只能在 Casdoor 数据库硬删除。清理前只读核对：
每条 `_` 记录的 `name = ldap || '_'`、`isDeleted`、`signupApplication = app-anas-directory`、
创建于 05:56:3x、存在同 `ldap` 的未删除原记录；`casbin_user_rule`、`permission_rule`、`session`、
`token` 中无引用。清理脚本在单个事务内按上述全部条件选择记录，数量不等于 6 即回滚；删除前把整行
导出为 JSONL，证据目录为 finance `/home/whl/anas-casdoor-dup-cleanup-20261020/`（`0700`，
文件 `0600`，含 `SHA256SUMS`）。执行结果见下方记录。

**清理记录（2026-10-20 用户批准后执行）。** 事务筛中 6 条，`DELETE 6` 后提交：`pua7597e3c2{domain,manager,normal,other,privileged,target}_`。
`deleted-rows.jsonl` SHA-256 `7adec7178058f0d2dc53118ce580b922958c109bb059e099f0399686c83b1042`，
`result.txt` SHA-256 `a8131761d496f766de21acfe5fd6065d76a202c1c13a20d0f24c67a066ca5da7`。锚点冲突随之消失。
订阅器仍未恢复：故障期间每轮同步都在锚点检查失败前写入待撤权记录，`pending-logouts.json` 累积到 6547 条，
全部属于测试用户；其中 3260 条指向已删除的 `_` 用户（无 token、无 session，只有 Immich 策略通知）。
撤权接口对这些用户返回 `directory user no longer exists`，`revokePreparedLogouts` 遇到首个错误即停，
健康状态报 `directory logout is overdue`。暴露独立缺陷：失败轮次因策略通知 `eventTimestamp` 每次不同而
无法去重，待撤权记录无限增长；用户被删除后，引用它的待撤权记录无法完成，会一直阻塞后续记录。

经用户批准，停止订阅器、备份后只移除指向 6 个已删除 `_` 用户 ID 的 3260 条记录（6547→3287，全部无 token/session），再启动。`pending-logouts.before.json` SHA-256 `321fd9d7fa9dba2c399680148e876937a12fbd29bae905ade6956e872312b838`，`pending-logouts.removed.json` SHA-256 `c002ec60a5648da3b5aeaea70ecefae7f6f8c6bdd9e1722644d0a5a2ea09d941`，同在上述证据目录。此后 LDAP 同步恢复（游标 286→310，触发无错误），但仍 unhealthy：其余 3260 条属于 5 个原记录测试用户，快照 `subject` 为空，只有 Immich 策略通知目标；`0006` 的 deliver 对空 subject 返回 `captured directory subject is invalid`，这些记录永远无法完成，同属上述“无法完成的记录永久挂起”缺陷。

经用户再次批准，停止订阅器、备份后只移除 `name` 以 `pua7597e3c2` 开头、`subject` 为空且无 token/session 的 3260 条记录（3268→8，保留另一会话夹具 `pua96083604` 的 8 条），再启动。`pending-logouts.before2.json` SHA-256 `7e8f8efb57547e972629808b8b19ba66106061e0d564370f5e71b26e191a4338`，`pending-logouts.removed2.json` SHA-256 `00db7cb098d2e51d152c42e7a471ebaba3518659e1c3909561f4e21759099408`。10:24:30（+08）订阅器恢复 healthy，`ready=true`、`pending_logouts=0`（保留的 8 条已正常投递），Casdoor `anas` 组织 157 个用户中无 `ldap` 重复、无 `_` 结尾账号。

**后续。** 上述两个撤权缺陷（失败轮次待撤权记录无限增长；引用已删除用户或空 subject 的记录永久挂起并使健康检查失败）未在本次修复，另行处理。finance 尚未部署含 0009 的镜像，R-041 的实机并发复验待新镜像部署后执行。
