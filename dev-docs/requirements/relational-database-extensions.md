---
doc_type: requirement
status: current
created: 2026-10-03
updated: 2026-10-03
---

# relational_database 扩展生命周期要求

本文规定最小的 PostgreSQL 扩展接入能力。当前未发版，直接完善工作中的 Contract `1.0.0`，
不建立历史版本兼容层。字段、固定镜像、认证和维护接线已实施；验收证据与服务器恢复缺口见配套计划。
设计见[扩展生命周期](../../docs/architecture/relational-database-extension-lifecycle.md)，
实施见[配套计划](../plans/archived/relational-database-extensions.md)。

## 1. 范围

Contract 只增加可选的 PostgreSQL 扩展名称列表。Provider 负责固定扩展版本、依赖、preload、安装和升级；
Runner 沿用现有传参、计划与执行机制。没有 PG 需求的当前消费者仍应正常工作，MariaDB 不接受 PG 字段。

不新增版本范围求解器、独立目录协议、请求文件 ABI、通用结果协议改造、维护状态机或备份系统。
共享 PG 的认证基线由[凭据轮换主题](credential-rotation.md)交付；本主题只验证接入门槛，
不重复认领 CRED 要求。未发版允许调整接口，不免除持久数据安全与升级恢复要求。

## 2. 必须保留的运行边界

扩展指 PostgreSQL 内的 vector、vchord、earthdistance 等，不是 Immich 插件。Consumer 使用普通数据库角色，
Provider 完成特权操作。ensure 必须执行实际检查；数据库存在或一次命令退出成功本身不证明扩展可用。

升级随 PostgreSQL Module 的固定发布组合进行，普通 Consumer 启动不能偷偷升级共享实例。
复用 plan/apply、锁、生命周期和失败记录；若现有执行顺序不能保证维护先于 Consumer 启动，应在原路径补齐。
错误或中断后先核实实际状态；数据不兼容时使用 ANAS 恢复，而不是直接换回旧镜像启动。

备份统一复用 ANAS，首版验证全 workspace 恢复，不新增应用专属调度或单应用恢复能力。
目录归属遵守[Module 开发规范](../../docs/developer/module-development.md#持久数据归属)。

## 3. 需求矩阵

本矩阵是验收规范来源。收缩后保留 19 项有效要求；完成状态由配套计划维护。已有 ID 不重排、不复用。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `RDBEXT-R-002` | MariaDB interface 收到 spec.postgres 时必须拒绝，不得静默忽略 | 契约 |
| `RDBEXT-R-005` | 扩展名称列表必须拒绝未知字段、空列表、重复项及非法名称 | 单元 |
| `RDBEXT-R-007` | Provider 只能启用当前固定发布组合支持的扩展及必要依赖，不支持的请求在写入前失败 | 契约 + e2e |
| `RDBEXT-R-008` | 运行时不得联网下载或构建扩展二进制 | 审阅 |
| `RDBEXT-R-012` | Consumer 不得获得 Provider 管理凭据或数据库超级用户权限 | e2e |
| `RDBEXT-R-013` | 共享扩展接入必须通过真实 PG 认证基线检查，不得仅检查初始化环境变量 | e2e |
| `RDBEXT-R-014` | Runner 和 Compose 必须完整传递扩展名称请求，不能丢弃扩展字段 | 契约 |
| `RDBEXT-R-017` | inspect 必须只读，不创建或修改数据库、角色、扩展、凭据或实例配置 | e2e |
| `RDBEXT-R-018` | ensure 只有实际应用身份连接、角色权限、扩展及依赖版本和必要 preload 检查通过才能成功并保存 ready | e2e |
| `RDBEXT-R-022` | 共享实例升级的计划必须列出全部受影响消费者与停写、重启范围 | 契约 |
| `RDBEXT-R-023` | 新装在目标数据库按 Provider 固定版本启用扩展及依赖，重复 ensure 结果一致 | e2e |
| `RDBEXT-R-024` | 扩展更新只能在 PostgreSQL Module 的受控维护中按固定升级路径执行，普通 Consumer ensure 遇到版本不符应失败 | e2e |
| `RDBEXT-R-026` | 扩展或 preload 不再满足要求时，再次检查必须报告非 ready | e2e |
| `RDBEXT-R-028` | 中断后重试必须先检查数据库实际状态，不能盲目重放破坏性维护动作 | e2e |
| `RDBEXT-R-029` | 数据格式已改变或结果不确定时，不得仅换回旧镜像并自动启动 Consumer | e2e |
| `RDBEXT-R-030` | 移除扩展需求不得自动 DROP EXTENSION 或删除共享二进制 | e2e |
| `RDBEXT-R-032` | 扩展升级和恢复复用 ANAS 备份执行机制，不建立应用专属备份调度器 | 审阅 |
| `RDBEXT-R-033` | 允许改变数据格式的维护前，必须确认恢复集合覆盖相关数据库、耦合文件及匹配的扩展/镜像版本 | e2e |
| `RDBEXT-R-034` | 共享实例恢复必须验证目标应用及其他 Consumer 的数据和身份 | e2e |

## 4. 收缩后废弃的条目

以下只保留 ID 历史，不属于本次实施或完成率分母；废弃不等于相关通用安全原则被取消。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `RDBEXT-R-001` | 旧请求兼容承诺；当前未发版，改为当前普通请求回归（已废弃） | 审阅 |
| `RDBEXT-R-003` | 最低 Contract 1.1.0 限制；直接完善当前未发布定义（已废弃） | 审阅 |
| `RDBEXT-R-004` | Provider/注册表版本规则；沿用现有校验，不新增该项工作（已废弃） | 审阅 |
| `RDBEXT-R-006` | 通用资源认领机制；超出扩展接入范围，不借本次重做（已废弃） | 审阅 |
| `RDBEXT-R-009` | 扩展 SemVer 映射；删除范围求解，使用 Provider 固定原始版本（已废弃） | 审阅 |
| `RDBEXT-R-010` | 额外目录、依赖和资格摘要冻结；复用现有 Module 制品与 Resource spec（已废弃） | 审阅 |
| `RDBEXT-R-011` | 多消费者版本求解器；改由固定发布组合的发行测试检查（已废弃） | 审阅 |
| `RDBEXT-R-015` | 通用凭据输出边界；沿用现有安全规范和凭据主题，不重复建设（已废弃） | 审阅 |
| `RDBEXT-R-016` | 所有 Provider 结果协议重构；本次确保 ensure 实际检查后才成功（已废弃） | 审阅 |
| `RDBEXT-R-019` | 新增资格证据摘要协议；沿用现有状态和本次实际检查（已废弃） | 审阅 |
| `RDBEXT-R-020` | 扩展专用过期计划检测协议；不引入新目录或消费者摘要机制（已废弃） | 审阅 |
| `RDBEXT-R-021` | 计划阶段只读原则；沿用现有 plan/apply 规范，不新增独立机制（已废弃） | 审阅 |
| `RDBEXT-R-025` | Core 不按应用名硬编码；已有 Core/Module 职责规范，不重复列需求（已废弃） | 审阅 |
| `RDBEXT-R-027` | 独立维护阶段持久化与自动续跑框架；沿用现有失败处理，重试检查实际状态（已废弃） | 审阅 |
| `RDBEXT-R-031` | 删除与凭据轮换交付；留在原主题，现有 retain 语义继续有效（已废弃） | 审阅 |
| `RDBEXT-R-035` | 历史 Consumer/Contract 迁移矩阵；未发版，不设兼容工程（已废弃） | 审阅 |
| `RDBEXT-R-036` | 新建发布资格体系；复用已有 Module 发布与真实升级/恢复门禁（已废弃） | 审阅 |
