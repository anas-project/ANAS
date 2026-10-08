---
doc_type: review
status: current
created: 2026-10-02
updated: 2026-10-03
---

# Immich 接入方案评审

状态：方案评审，未修改运行实现，未做部署验收。ANAS 基线：`fd734b83f1faff8637b72bf406308a9e74d1f7bb`，评审开始时工作树干净。上游源码基线：Immich `v3.2.4`；在线资料核验日期：2026-10-02。

## 1. 结论与审查范围

方向可保留，但不能把当前方案视为已经具备发布条件。共享 PostgreSQL、专属 Redis/Valkey、仅 OIDC 常规登录、完整照片视频业务、复用 ANAS 备份，以及下一版本再做通用资源运行方式，这些决定没有根本冲突。需要修正的是共享数据库的安全前提、扩展升级责任、媒体与数据库的回滚边界，以及一处过时的上游事实。

本次通过 `read_thread` 读取了引用聊天“调研Immich接入方案”的三轮完整答复，并核对当前 ANAS 源码。引用聊天最后称已在另一台机器的 `/Users/whl/Documents/anas` 写入以下文件，但当前仓库没有这些文件，也未找到 Immich Module：

- `docs/architecture/immich-module-design.md`
- `docs/research/immich-module-integration.md`
- `docs/architecture/module-resource-profiles.md`
- `dev-docs/requirements/module-resource-profiles.md`
- `dev-docs/plans/module-resource-profiles.md`

因此本报告评价的是**聊天中可见的方案与当前仓库的适配关系**，不是对那五份正文、28 条矩阵或原下载附件的逐行验收。不能据此断言另一份正文必然遗漏了本报告的建议，也不能确认先前声称的文件落盘与构建结果。

| 编号 | 级别 | 结论 | 证据性质 |
| --- | --- | --- | --- |
| F1 | P1 | 共享 PG 接入前必须修正现有 `trust` 认证基线 | 已观察到的本地实现，尚未检查实机 HBA |
| F2 | P1 | 自动升级快照默认不含媒体所在的 `userdata`，不能直接承诺一致回滚 | 已观察到的本地行为与方案的组合风险 |
| F3 | P1 | 扩展“已预装、版本在范围内”不足以保证非超级用户启动与升级 | 固定版本上游启动路径已确认 |
| F4 | P1 | OIDC-only 仍未完成严格 anchor 绑定与撤权闭环 | 原方案已识别的发布阻塞，本次复核并补充唯一性证据 |
| F5 | P2 | “v3.2.4 自动备份使用 pg_dumpall，故需超级用户”不正确 | 固定版本源码与官方页面存在矛盾，以源码为准 |
| F6 | P2 | Contract 升级还涉及 Runner 参数传递与就绪判定，不能仅改 schema/provision | 已观察到的本地调用链 |

P1 表示正式支持前应完成的条件，不表示本次已经在生产环境复现了故障或越权。F4 不是新发现的遗漏，不能把原方案诚实声明的待验收项说成“已实现但有缺陷”。

## 2. 需要修正的内容

### F1：共享数据库隔离依赖于尚未修正的认证基线

[`modules/postgres/hook/main.go`](../../modules/postgres/hook/main.go) 的 `renderEnv`（第 128 行）固定设置 `POSTGRES_HOST_AUTH_METHOD=trust`；[Compose](../../modules/postgres/docker-compose.yml) 将 `.env` 注入 PostgreSQL，消费者接入同一数据库网络。

按此配置初始化且使用相应 `trust` HBA 规则的实例，不会校验连接者声明的数据库身份所对应的密码。即使应用没有拿到管理员密码，也可能通过声明管理员用户名连接；分库、独立角色及 `REVOKE ... FROM PUBLIC` 无法替代身份认证。[PostgreSQL 18 的 trust 语义](https://www.postgresql.org/docs/18/auth-trust.html)明确包括超级用户名。

这是已有债务，[凭据轮换要求 §6](../requirements/credential-rotation.md)已明确要求修正。共享 PG 方案必须将其列为接入前置条件，而不是只要求“不把超级用户密码交给 Immich”。

建议：使用口令认证基线，迁移并验证既有 `pg_hba.conf`，分别测试无密码、错误密码、冒用管理员及跨库访问被拒绝，正常资源账号仍可用。只改容器环境变量不等于既有数据目录里的 HBA 已经迁移。本次没有连接运行实例，不判断其当前实际 HBA。

### F2：完整灾备与升级回滚不能共用一个模糊承诺

方案建议将媒体放入受管用户数据，并复用现有快照与回滚。但本地有明确的不同语义：

- [`backup_create.go`](../../internal/runner/backup_create.go) 默认包含 `userdata`；[`backup_restore.go`](../../internal/runner/backup_restore.go) 会恢复备份携带的用户数据。
- [`snapshot.go`](../../internal/runner/snapshot.go) 的 `snapshotOptions.includeUserData` 默认关闭（第 276 行），[`deployment.go`](../../internal/runner/deployment.go) 的 `snapshotBeforeApply` 未开启它（第 1282 行）。
- [`snapshot_restore.go`](../../internal/runner/snapshot_restore.go) 仅在显式要求时恢复 `userdata`（第 175 行）。

因此，若共享 PG 在 `data`、照片在 `userdata`，升级前自动快照通常只保存数据库侧。升级或运行期间一旦迁移、移动或删除媒体，恢复旧数据库可能指向已经不存在的文件；不能将普通部署回滚宣传为 Immich 完整恢复。即使显式捕获 `userdata`，恢复整个共享 PG 也会回退其他应用。

建议首版分别规定：

1. **全 workspace 灾备**：停写并验证数据库、完整媒体、配置/Secret 与所需镜像、扩展版本均可恢复；明确其他应用也一起恢复。
2. **Immich 升级回退**：升级前取得匹配的数据库和媒体恢复集合；没有这个集合时，不声称自动快照足以回退。沿用现有工具可以，但必须检查实际覆盖记录。
3. **单独恢复 Immich**：可以暂不提供；若提供，需单库导入与对应媒体一致点，不恢复整个共享 PG。

此外，Btrfs 快照可缩短停机；普通文件系统 copy 路径会保持服务停止直到复制完成。TB 级图库的整套停机窗口必须在计划中可见，不能普遍承诺“备份只停几秒”。这不是要求另建备份系统，而是补齐已有机制的适用边界。

### F3：扩展资格还要覆盖自动选择、自动更新与恢复

[`database.repository.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/repositories/database.repository.ts) 的自动选择读取的是 `pg_available_extensions`；同时提供多个候选时，优先次序包含 VectorChord 在 pgvector 之前。因此在共享镜像里新增扩展二进制，即使没在 Immich 库中启用，也可能改变自动选择结果。

[`database.service.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/database.service.ts) 启动时比较 installed/default available version；后者较新时会尝试更新，失败会终止启动。仅验证“当前启用版本符合 Consumer 的范围”不够：共享镜像更新后，普通应用账号可能因无权更新 Provider 所有的扩展而启动失败。

建议固定 `DB_VECTOR_EXTENSION`，并冻结“PG 大版本＋扩展二进制/default version＋数据库已启用版本＋Immich 版本”的合格组合。由 Provider 在应用启动前完成需要特权的启用、升级及重建索引，并验证重试、失败恢复与其他消费者的影响；不让应用自行安装任意扩展，也不在卸载 Immich 时删除共享镜像的二进制。

扩展清单还应覆盖所选路线需要的其他扩展及依赖，而非只列向量扩展；官方非超级用户准备步骤还包含 `earthdistance CASCADE`。版本范围和 preload 的实例级影响应依据选定组合验证。[官方现存 PG 接入说明](https://docs.immich.app/administration/postgres-standalone/)

### F4：OIDC-only 是正确收敛，但不能作为身份验收结果

本节为原评审建议；2026-10-03 明确空库纯 OIDC 前提后，适配范围已按 §6 收缩，不再默认要求整套身份补丁。

保留 `anasIdentityAnchor = OIDC sub = user.oauthId`、保留 Immich 自己的内部 `user.id` 是合理的，不必改造全部主外键。

原方案关于邮箱回退、自助解绑重绑和会话撤销的担忧有源码依据：[`auth.service.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/auth.service.ts) 仍在按 sub 查询失败后尝试关联同邮箱的未绑定账号；`autoRegister` 在后面才判断。只关闭密码登录或自动注册不能消除这条路径。Back-channel 注销 OAuth 会话也不等于撤销 API key、共享链接等独立访问方式。

补充证据：[`user.table.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/schema/tables/user.table.ts) 将 email 声明为 unique，却没有为 `oauthId` 声明同等约束；[`user.repository.ts`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/repositories/user.repository.ts) 按 `oauthId` 查询取第一行，创建走普通 insert。不能仅因为字段保存了 anchor 就声称它是数据库强制唯一键。本次未实测并发竞争，也未检查部署数据库的实际索引。

建议将最小适配范围定为：禁用隐式邮箱归并；限制托管身份解绑重绑；为有效 anchor 提供数据库唯一性或等效并发保障；明确首个管理员建立流程；以实际旧会话和其他有效凭据验证准入撤销。补唯一索引时还要考虑上游用空字符串表示未绑定，不能直接给整列加普通唯一约束。

不必因此强行补 LDAP 登录。若首版采用仅 OIDC，就按各 IAM Provider 的已实现能力验收，并对未覆盖的资料同步、角色变更、撤权时间与恢复路径准确声明。已有[目录事件要求](../requirements/directory-event-subscription.md)也区分纯 IAM Consumer 与直接 LDAP 消费者，不能随意将全部目录适配工作推给 Immich。

### F5：自动数据库备份的权限理由需要勘误

原答复多次以 `pg_dumpall` 为理由建议关闭 Immich 自动数据库备份。但 v3.2.4 的 [`DatabaseBackupService.createDatabaseBackup`](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/database-backup.service.ts)（第 213–217 行）明确请求 `pg_dump`。同文件仍出现 `pg_dumpall` 字样，不代表当前自动备份调用它。[官方备份页](https://docs.immich.app/administration/backup-and-restore/)也已使用单库 `pg_dump` 示例，而现存 PG 接入页仍保留旧警告。

应改为：由 ANAS 负责统一灾备仍合理；是否关闭 Immich 内置数据库备份是调度、空间及恢复策略的选择，不能以该过时结论证明必须关闭，也不能因此授予超级用户权限。普通账号的导出、扩展所有权及导入恢复仍需分别验证，确认使用 `pg_dump` 不等于恢复全部权限条件已经满足。

### F6：Contract 的 minor 升级可以做，但实施面需要补全

本节原有版本过渡假设已由 §6 的“尚未发版、直接完善当前定义”取代；下述传参缺口仍成立。

现有 [`resource.yml`](../../contracts/relational_database/schemas/resource.yml) 禁止未知字段；[`resources.go`](../../internal/runner/resources.go) 在第 90–93 行只向 provision 投影库名、用户名及口令，并在进程成功退出后保存 ready（第 127 行）。[`provision.sh`](../../modules/postgres/providers/relational_database/provision.sh) 的 inspect 只查数据库和角色是否存在。

因此需要同时设计 schema、Consumer/Provider 版本协商、Runner 参数传递、Provider 验证和状态证据。只新增 schema 字段与 SQL 逻辑，扩展需求并不会自动到达 provision。可以让 ensure 只有在完整资格检查通过后才成功；不必为了此事新建第二套资源框架。

此外，当前 [Provider manifest](../../modules/postgres/providers/relational_database/provider.yml) 仅声明 ensure/inspect；delete、rotate_credential 是 Contract 的可选操作，不能把契约存在写成当前 Provider 已有完整生命周期实现。沿用卸载保留数据的首版边界是合适的。

## 3. 可保留的决定与尚需验收的边界

- **专属 Redis/Valkey**：与现有 Module 管理方式相容。首版可由 Immich Module 自有服务管理，不必另起一个共享 Redis Provider 项目。仍需定义队列持久化、容量满时行为、停写顺序与灾备恢复时旧任务的处理。
- **全功能照片与视频业务**：定位合理；手机上传备份与服务器灾备应分别验收。手机客户端登录、回调、后台上传与服务端实际数据恢复都应覆盖，不能只测浏览器。
- **4GB 与通用资源运行方式分开**：将通用机制延后合理。官方允许 4GB 关闭 ML 运行，但不证明“ANAS＋IAM＋共享 PG＋其他 Module”整机已合格。首版可提供显式关闭 ML、并发配置；是否宣称支持 4GB，由整机负载测试决定，不必等待通用框架完成。[官方硬件要求](https://docs.immich.app/install/requirements/)
- **只读外部图库**：不能承诺继承 Samba/AD ACL；图库归属、源挂载失联和缓存/分享访问的撤权仍需验证。这个风险原方案已识别，无需推翻方向。[官方外部图库说明](https://docs.immich.app/features/libraries/)

## 4. 建议的实施顺序

1. 同步并复核另一台机器的五份正文，再将本报告发现合入实际方案；不要直接把聊天摘要复制成“已完成设计”。
2. 先解决共享 PG 认证，冻结一个非超级用户可完成新装、升级、恢复的扩展组合；补齐 Contract 到 Provider 的参数与验证链路。
3. 验证严格 anchor 绑定及准入撤销，落实必要的最小上游适配；明确配置与首个管理员的管理责任。
4. 定义媒体存储与一致恢复集合，验证全 workspace 灾备和升级回退；单应用恢复若延后，应明示不支持。
5. 再完成 Module、移动端和整机资源验收。通用资源运行方式继续留在下一版本。

以上是建议顺序，不是新增实施计划或验收矩阵。本次不修改需求、里程碑或运行代码。

## 5. 本次验证记录

- 已完成：引用聊天完整答复读取；当前仓库文件存在性、PG Hook/Compose/Provider、Contract/Runner、备份/快照调用链静态核对；固定版本上游认证、数据库启动、表结构及备份源码核对。
- 已尝试定向 Go 测试，但当前环境无 `go` 可执行文件，命令以 `command not found: go` 退出。未安装工具链，未将已有测试源码视为本次通过证据。
- 已通过：`docs:check-status`、`docs:check-requirement-status`、`docs:check-plan-status`，本报告 13 个仓库相对链接存在性检查及 `git diff --check`。
- `docs:check-requirements` 的 Node 需求归属阶段通过（21 份活动文档、632 项需求）；后续 Go 测试用例目录校验因缺少 `go` 未执行，完整门禁不记为通过。
- `docs:build` 在 Go 文档生成阶段以 `spawnSync go ENOENT` 失败，未完成站点构建。
- 本次仅新增评审正文并更新评审索引；没有改变需求、计划成员或里程碑状态，因此未重新生成需求/计划索引，已用对应 `--check` 命令确认其一致性。
- 未执行：Immich 部署、真实 PG 认证/扩展升级、移动端 OIDC、API key/分享撤权、真实媒体恢复、并发建号、4GB 整机负载；未确认另一台机器的正文与原附件。

## 6. 2026-10-03 后续决策

用户已确定：数据库与文件耦合的内容放入 `data/`，仅可独立使用或数据库为辅助索引的内容进入 `userdata/`；
备份统一使用 ANAS。已写入 [Module 规范](../../docs/developer/module-development.md#持久数据归属)，
并建立 [Immich 接入设计](../../docs/architecture/immich-module-design.md)与
[Contract 完整升级规划](../plans/archived/relational-database-extensions.md)。

F2 所基于的媒体放置建议已在设计层修正为 `data/immich/media`；这不代表真实一致恢复已经验收。
F1/F3/F6 仍待实现；F4 的适用条件见下方补充；F5 的过时事实已在新设计中勘误。原评审基线与观察保持不变。

### 同日补充：纯 OIDC 前提与设计收缩

用户进一步明确：技术规则只放开发规范；当前未发版，不承担历史接口兼容；新装不创建本地账号。
已从 AGENTS.md 移除目录细则，检查表引用规范，快照文档只说明相关覆盖行为。

F4 不能表述为“纯 OIDC 必然误关联”。固定版本回调只在 email 对应记录的 oauthId 为空时关联；所有用户
始终绑定 anchor 时，不同 sub 复用邮箱会被拒绝。需要验证的是运行中是否仍能建本地账号或解绑/重绑。
AuthService 与 BaseService 还支持通过受信 roleClaim 创建首个 OIDC 管理员；因此移除专用初始化器、
历史账号迁移与默认 schema 补丁方案，改为原生路径验证和必要的最小入口限制。
详见[修订后的身份方案](../../docs/architecture/immich-module-design.md)。

上一版 Contract 规划确实过度设计：为固定 Provider 增加了版本求解、目录与资格摘要、请求文件 ABI、
独立维护阶段记录及未发布版本兼容矩阵。现收缩为名称列表、固定版本 Provider、实际检查和现有执行流程
内的升级恢复；36 项收缩为 19 项有效要求，七个里程碑收缩为三个，其余 ID 保留为已废弃。
PG 认证、特权扩展更新、共享实例停写和一致恢复仍是实际必要工作，不随抽象层删减而取消。

本轮修订检查：需求/计划索引已重新生成，Node 需求归属检查通过（本主题 19 项有效、17 项废弃），
两个索引一致性、文档状态、27 个本地链接和 git diff --check 通过。完整需求门禁仍在 Go 用例目录阶段
因本机缺少 go 失败；本轮 docs:build 同样以 spawnSync go ENOENT 失败，未完成站点构建。
没有运行 Immich/PG 部署与新装、升级或恢复测试，功能完成率仍为 0/19。

下一步：按收缩后的三阶段计划实现并验证；纯 OIDC 初始化及运行入口限制先做真实新装验证。
