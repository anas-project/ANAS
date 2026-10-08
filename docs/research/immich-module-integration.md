---
doc_type: research
status: proposed
created: 2026-10-02
updated: 2026-10-02
evidence_as_of: 2026-10-01
---

# Immich 接入可行性与证据边界

本文整理此前 Immich 调研中仍适用的技术结论，供接入设计复核使用，不是安装说明。
本次按 2026-10-02 的用户决策将文档写入本地仓库；未部署 Immich，也未进行实机验收。
确定的方案见 [Immich Module 接入设计](/architecture/immich-module-design)，
按资源选择运行方式的后续设计见 [Module 资源运行方式](/architecture/module-resource-profiles)。

## 1. 来源与适用范围

上游结论沿用会话提供的 `immich-module-integration-2026-09-30.md` 和
`immich-module-integration-revised-2026-10-01.md` 两份调研附件，源码引用基线为 `v3.2.4`。
本次没有重新联网调研；此版本只表示前稿引用的基线，不表示当前最新版本，也不是本次已通过测试的发布组合。
`evidence_as_of` 保留上游材料的原核查日期，不能用本次写入日期冒充新的上游核验日期。

ANAS 部分重新读取了实际工作区。读取时 HEAD 为 `c789116295e14322b0c698825bf89404caef3c12`，
工作区另有未提交修改，尤其涉及临时存储、生命周期和备份；因此旧稿远端 `9a6921a…` 的实现状态不能直接沿用。
下面的“本地已读”只表示代码或文档存在，不代表相关业务已完成真实环境验收。

## 2. 仍适用的上游结论

| 主题 | 前稿核查结论 | 接入前需要复核的证据 |
| --- | --- | --- |
| PostgreSQL | 官方提供连接既有实例和非超级用户运行的方法；向量扩展需要额外准备 | [既有 PostgreSQL](https://docs.immich.app/administration/postgres-standalone/)；实际选择的 PG/扩展/平台组合 |
| 向量实现 | 前稿引用的版本支持选择 pgvector 或 VectorChord，不能把关闭 ML 理解为不再需要数据库迁移所需扩展 | [环境变量](https://docs.immich.app/install/environment-variables/)；固定版本的环境 schema 与迁移代码 |
| Redis/Valkey | 服务可外接，但 BullMQ 队列与随意丢弃的缓存不是同一种用途 | [BullMQ 生产要求](https://docs.bullmq.io/guide/going-to-production)；重启、满内存和恢复测试 |
| OIDC | 前稿源码显示按 `sub` 查找 `oauthId`，未找到时还有未绑定本地账号的邮箱关联路径 | [固定版本认证服务](https://github.com/immich-app/immich/blob/v3.2.4/server/src/services/auth.service.ts)；严格身份模式与客户端登录测试 |
| LDAP 预配 | 未找到直接配置 LDAP 同步即可完成双接入的路径；管理员创建/更新 DTO 未暴露 `oauthId` 写入参数 | [固定版本用户 DTO](https://github.com/immich-app/immich/blob/v3.2.4/server/src/dtos/user.dto.ts)；不把响应字段当作可写接口 |
| 手机备份 | 原生提供照片/视频备份；iOS 后台执行受系统调度约束 | [手机备份](https://docs.immich.app/features/mobile-backup/)；真实 iOS/Android 大批量和增量上传 |
| 灾难恢复 | 数据库备份本身不包含媒体原件，数据库与媒体需要一致恢复 | [备份恢复](https://docs.immich.app/administration/backup-and-restore/) |
| 内存 | 前稿要求页同时给出常规内存建议和 4GB 关闭 ML 的条件，不能只摘取一个数字当作全栈保证 | [运行要求](https://docs.immich.app/install/requirements/)；整机、运行环境限额和实际负载分别记录 |
| 远程 ML | 属于可选部署路径，但图片预览会离开本机，且仍有本地工作 | [远程 ML](https://docs.immich.app/guides/remote-machine-learning/)；网络、版本与数据流向验证 |

表中来源是复核入口。实现时若固定版本不同，应重新确认配置字段、认证回调、队列和迁移行为，
不能通过更换文档里的版本号宣称兼容性已经成立。

## 3. 本地已读的复用基础

| 本地来源 | 本次观察 | 设计含义 |
| --- | --- | --- |
| `contracts/relational_database/schemas/resource.yml` | Resource 声明库名、principal、凭据策略与删除策略，未包含扩展要求 | 需扩展现有 Contract，不是另造数据库生命周期 |
| `modules/postgres/docker-compose.yml` | 使用 `18.4-alpine`，包含 `anas_postgres_provision` 一次性服务 | 扩展构建和管理操作可以复用 PostgreSQL Module |
| `modules/postgres/providers/relational_database/provision.sh` | 已有创建库/角色及只读检查路径；当前检查没有向量扩展版本判定 | 不能将现有 `ready` 当成已经满足 Immich 的扩展要求 |
| `contracts/identity/README.md` | identity Contract 仍标 `pending`，现行 IAM 走 capability、绑定环境与 Hook | 本版沿用已实现的 IAM 接入，不声明依赖未就绪 Contract |
| `internal/runner/backup_txn.go` | 已有停启事务、逆依赖停止及原运行集合恢复，工作树另含临时存储协调 | 复用现有备份和生命周期路径，不复制一套补偿框架 |
| `docs/guide/backup-and-restore.md` | 备份默认覆盖 `userdata`，区分发布回滚、快照恢复和备份恢复 | 媒体必须进入受管用户数据边界 |
| `internal/runner/manifest.go` | 模块加载遍历 `modules/` 直接子目录并读取各自 `module.yml` | 文档阶段不能仅创建空的 `modules/immich/` 目录，否则会改变模块加载结果 |

## 4. 选型决定及被替代的方案

2026-10-02 确定：Immich 使用专属 Redis/Valkey；常规用户只使用 OIDC，不为 LDAP 与 OIDC
双登录增加适配；继续共享 ANAS PostgreSQL、使用 anchor 绑定并复用 ANAS 备份。
不同内存选择不同运行方式移至下一版本，采用面向所有 Module 的方案，而非 Immich 私有开关体系。

这替代了前稿的“优先共享 Redis”“为了 LDAP 预建号补写管理接口”以及“首版提供多种内存运行档”。
专用 PostgreSQL 仍不是默认方案；不能因为 Redis 专属，就把全部基础设施一起复制。
本次没有为比较中的候选新增通用 Redis Provider、LDAP 同步服务或运行时资源调度服务。

## 5. 尚不能宣称已经解决的问题

严格按 anchor 绑定仍需检查邮箱回退和用户解绑；仅使用 OIDC 不会自动消除这些问题。
管理员停用用户后，既有会话、API key 与共享链接的失效需要分别验收。
数据库扩展构建、非超级用户升级、共享实例重启影响、媒体一致恢复和大批量导入仍需要实际测试。
前稿对登出 token 重放等细节的观察作为待复核项保留，不写成已复现漏洞或本次已完成的修复。

完整职责、故障边界及验证项目集中在 [接入设计](/architecture/immich-module-design)，不在研究页维护第二份实施进度。
