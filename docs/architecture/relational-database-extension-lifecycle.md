---
status: current
created: 2026-10-03
updated: 2026-10-08
---

# relational_database 扩展生命周期设计

状态：当前代码模型；9fc699c 基线的 finance 隔离主机升级与一致恢复已通过，
最新 master aa1944a 的复验在 Immich 配套计划继续记录。ANAS 尚未发版，直接完善当前
`relational_database 1.0.0` 定义，不规划兼容过渡。名称字段、传参、PG 固定组合、真实资格检查和
受控维护接线已实施；Module 完整支持状态以测试证据为准。
验收见[需求矩阵](https://github.com/anas-project/ANAS/blob/master/dev-docs/requirements/relational-database-extensions.md)，
顺序见[实施计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/archived/relational-database-extensions.md)。

## 1. 收缩设计的结论

Contract 只表达 Consumer 需要哪些数据库扩展。扩展版本、依赖、安装和升级属于 PostgreSQL Module，
调度与恢复复用 ANAS 现有机制。当前不需要先造一个通用扩展包管理器。

| 保留 | 不进入本次设计 |
| --- | --- |
| 扩展名称列表、严格输入校验、MariaDB 拒绝 PG 专属字段 | 引擎/扩展 SemVer 范围、原始版本映射、多消费者版本求解器 |
| Provider 固定镜像、扩展版本和 preload 配置 | 独立扩展目录协议、目录摘要和资格摘要协议 |
| ensure/inspect 的完整传参及实际就绪检查 | 新请求文件 ABI、专用 Go helper、通用结果协议改造 |
| 固定组合的升级脚本及实际状态复查 | 独立维护状态机、跨阶段自动续跑框架、独立调度器 |
| 现有 plan/apply、锁、ANAS 备份与失败处理 | 新旧 Contract/Core/Provider 的兼容矩阵、旧 lock 迁移层 |
| 当前普通数据库消费者的回归 | 为尚未发布的接口承诺历史版本兼容 |

删去兼容层不等于可以破坏工作区数据；升级失败的数据恢复继续保留。当前 Provider/注册表版本一致校验
照常使用，无需为本次变更另建版本机制。

## 2. 最小 Contract 增量

在现有 Resource spec 中增加可选 `postgres.extensions`，列表元素直接是 SQL 扩展名称：

```yaml
spec:
  name: immich
  principal: immich
  credential:
    policy: generated
  deletion_policy: retain
  postgres:
    extensions: [vector, earthdistance]
```

以上展示字段形态；当前 Provider 只支持 `vector`、`cube`、`earthdistance`，会在任何写入前拒绝
`vchord`。Immich 实际声明 `[vector, earthdistance]`，固定 `DB_VECTOR_EXTENSION=pgvector`。
本轮构建组合为 PostgreSQL 18.4/Alpine、pgvector 0.8.2、cube 1.5、earthdistance 1.2，不需要 preload。
原生 arm64 与 finance 原生 amd64 的已验证范围见配套计划；不据此声明其他架构合格。SQL 名称 `vector` 与应用配置枚举 `pgvector` 不同。

字段规则：

- `postgres` 仅能用于 postgres interface；MariaDB 收到该字段立即拒绝。
- `extensions` 是必需且非空的名称列表；无 PG 块代表普通数据库。不接受未知字段、重复项、路径、SQL 或 URL。
- 名称必须是合法 SQL 扩展标识，并由所选 Provider 确认支持；不支持的名称在任何建库/扩展写入前拒绝。
- 所列扩展都是必需项。依赖由 Provider 固定处理，例如 `earthdistance` 的 `cube`，Consumer 不声明依赖图。
- 不增加 `server_version`、扩展 `version`、`optional`、`upgrade_policy` 或 maintenance operation。

扩展请求进入既有 Resource spec 和指纹即可，不另加版本/目录摘要。普通 PostgreSQL 和 MariaDB 请求
仍需正常工作，这是当前功能回归，不是历史制品兼容工程。

## 3. PostgreSQL Module 管理的内容

这里的“扩展”是 **PostgreSQL 扩展**，不是 Immich 插件。Provider 发布资产固定一组经过验证的 PG、
扩展二进制和 SQL 版本；原始版本串仅作精确值匹配，不实现通用 SemVer 转换或范围求解。

| 层次 | 本次责任 |
| --- | --- |
| 镜像 | 固定 PG 大版本、平台及扩展包版本，携带控制文件、升级 SQL 和共享库；运行时不下载或编译 |
| 实例 | 固定必要的 `shared_preload_libraries`，随 PostgreSQL Module 变更管理重启 |
| 数据库 | 在 Consumer 独立库中以 Provider 权限 CREATE/ALTER EXTENSION；应用继续使用普通角色 |
| 发行验证 | 验证已支持 Consumer 与该固定组合；冲突时阻止发布，不在 Core 中自动求解 |

固定版本和依赖放在构建输入、Provider 脚本及文档中，无需暴露新的目录 Contract。PG 服务和 provision
容器使用同一组合。应用特有索引维护归 Consumer 生命周期；Core 和通用 Provider 不按 Immich 名称写 SQL。

预安装仍然不够：Immich `v3.2.4` 启动可能尝试把扩展升级至镜像默认可用版本。ANAS 必须在启动应用前
完成需要特权的升级并检查版本，不能靠给应用超级用户权限绕过。
[上游数据库启动代码](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/database.service.ts)

## 4. 复用现有请求与就绪链路

`internal/runner/resources.go` 通过统一投影函数传入库名、账号、口令与
`ANAS_RESOURCE_POSTGRES_EXTENSIONS`，使用经校验的逗号分隔名称，Compose 明确传入 provision。
它不含凭据，也不能成为任意 SQL 入口。沿用 psql、标识符引用和一次性 `compose_run`，不引入解析依赖或文件协议。

Provider 共用一份检查逻辑：

1. `ensure` 写入前校验全部名称、镜像支持及当前状态；建立库/账号、启用缺失扩展后调用检查。
2. `inspect` 只观察，不创建对象、改密码、更新扩展或重启实例。
3. 以实际应用凭据连接目标库，检查非超级用户角色、扩展及依赖符合固定版本、必要 preload 已生效。
   任一失败不能报告 ready。
4. 扩展已安装但版本不符时，普通 ensure 失败并提示需要 PostgreSQL Module 维护，不在应用启动中偷偷升级。
5. 沿用 Runner 的退出状态处理：ensure 只有检查通过才能退出 0，失败不能保存 ready。
   本次不重做所有 Provider 的结果收集协议；既有 schema 输出应准确，但输出本身不能替代检查。

重试和再次检查都查询数据库实际状态，不沿用上次 ready。日志不输出口令，继续使用现有 Secret 注入。
PG entrypoint 已在 TCP 开放前迁移现有 HBA 和管理员 SCRAM，Consumer ensure 迁移资源角色；
此认证子项仍归属
[凭据轮换计划](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/credential-rotation.md)
中的认证子项验收；不能把初始化变量变更视为真实认证检查通过。本主题不重复实现凭据轮换，也不重做资源认领或删除机制。

## 5. 扩展升级与失败处理

升级随 **PostgreSQL Module 发布变更**管理，不由 Consumer 字段触发任意版本安装。
每次允许的升级在 Provider 固定脚本中写明来源版本、目标版本和必要维护；未知来源版本失败。
当前固定脚本只接受 pgvector 0.8.1 → 0.8.2，先检查所有受管库，再更新并复查；未知来源失败。
真实服务器上的完整路径尚待验收，不建立升级图求解器或自动降级功能。

沿用现有 plan/apply、workspace 锁、生命周期执行与失败记录，按以下顺序接线：

```text
检查当前组合和全部受影响消费者 → 计划说明停机与恢复范围
→ 停止写入并用 ANAS 取得一致恢复点
→ 更新 PostgreSQL 镜像/preload，必要时重启
→ 对受管数据库执行固定扩展升级及 Consumer 必要索引维护
→ 复查数据库资格 → 启动 Consumer → 业务健康验证
```

共享镜像变更影响同一实例的其他应用，包括 IAM。Runner 在现有 plan 中列出普通和扩展消费者；
有 PostgreSQL Resource 绑定的 PG Provider 制品变化强制停全 workspace 并取得 ANAS Btrfs 恢复点，不接受
`--no-snapshot` 绕过。Provider 每模块 `after_start` 屏障先完成维护，才执行 Consumer ensure/start。
没有可验证恢复点时拒绝维护；PG 大版本迁移和制品降级要求匹配数据恢复。

跨库维护可能部分成功。错误或中断后保留受影响服务停止状态，记录现有作业错误与恢复点；重试先查询
实际扩展版本，仅对已验证且可重试的步骤继续。无需先做通用自动续跑框架。
启动新 Provider 前在现有 deployment 失败记录保存冻结候选。错误或中断后禁止 start/restart
旧制品，保留停机状态；只能重试同一候选（先验证恢复点和实际扩展版本）或使用 ANAS 一致恢复。
普通 rollback 不能直接换回带扩展的旧 PG 制品。
移除扩展声明或卸载应用不会自动 DROP EXTENSION，也不会删共享镜像二进制。
因此保护覆盖普通 PG 请求及所有消费者移除后仍保留的 Provider：当前声明无法证明库内没有保留的扩展。
失败候选可用 `anas apply --deployment <原候选ID> -w <workspace> -y` 重试，仍须验证原恢复点与匹配镜像，
再次确认候选已停写。失败备份的自动重启补偿也不能越过数据不确定的保护。

## 6. ANAS 备份与交付边界

备份统一使用 ANAS。首版是全 workspace 灾备和升级恢复，覆盖共享 PG、耦合媒体、配置、Secret 和可取得的
匹配镜像。数据归属的规范来源是[Module 开发规范](/developer/module-development#持久数据归属)；
本设计据此将 Immich 受管媒体放在 `data/immich/media`，共享数据库保持在 `data/postgres`。

路径名不证明备份覆盖，仍需验证外部挂载、嵌套子卷与停写。恢复时检查 Immich 的媒体、相册和账号，
以及同一 PG 中至少一个其他应用的数据。单应用选择恢复、无停机升级、PG 大版本迁移、通用资源档位
不在本版范围，也不另建 Immich 备份调度器。

交付分三步：字段和传参；固定镜像及真实就绪；受控升级和 ANAS 恢复验证。同步调整 Contract 源文档和
当前 Provider/Consumer 后生成文档，不维护旧 schema 或旧 lock 转换器。
