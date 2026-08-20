# 研究与选型资料

本目录集中存放有明确外部调研、竞品比较、技术评估或阶段性代码审查性质的文档。稳定的架构设计、用户手册和命令契约进入各自的持续维护分类；原始测试日志不进入公开文档树。

## 目录

| 文档 | 类型 | 状态 |
| --- | --- | --- |
| [Nextcloud 搜索方案调研（2026-08-20）](./nextcloud-search-solution-research-2026-08-20.md) | 当前搜索基线、全文/SQL/语义方案与 Elasticsearch 实施门槛 | 当前 |
| [ANAS Module 分类与访问边界分析（2026-08-19）](./module-classification-and-access-boundary-analysis-2026-08-19.md) | 三类主分类、入口级 audience、现有 Module 归类与迁移顺序 | 提案 |
| [`BASE_DOMAIN` 与 `SAMBA_DC_DOMAIN` 分离实施计划（2026-08-19）](./base-domain-samba-domain-separation-plan-2026-08-19.md) | AD/应用域分离、DNS 模式、校验 ABI 与迁移计划 | 实施中（WP1/WP2 首批） |
| [BIND 9 开源 Web 管理工具调研（2026-08-19）](./bind9-open-source-web-management-research-2026-08-19.md) | 标准 BIND、Samba BIND9-DLZ 与 Web 管理/监控选型 | 当前 |
| [ANAS 凭据库存与全量轮换范围审计及首版实现（2026-08-19）](./anas-credential-inventory-and-rotation-scope-2026-08-19.md) | Secret 存储面、轮换能力与安全实施边界 | 安全首版已实施 |
| [开源自部署邮件服务调研（2026-08-19）](./self-hosted-open-source-mail-services-research-2026-08-19.md) | 完整邮件服务与外部转发能力选型 | 当前 |
| [开源自部署邮件转发服务调研（2026-08-19）](./self-hosted-open-source-email-forwarding-research-2026-08-19.md) | Cloudflare Email Routing 类转发与隐私别名选型 | 当前 |
| [ANAS Core 与 Module Changelog 方案（2026-08-18）](./changelog-plan-2026-08-18.md) | 变更记录信息模型与发布流水线规划 | 提案 |
| [使用 `samba-tool` 管理用户、组和管理员（2026-08-17）](./samba-tool-user-group-admin-guide-2026-08-17.md) | Samba AD 操作手册 | 当前 |
| [ANAS Web API 与管理前端实施规划（2026-08-16）](./web-api-admin-console-plan-2026-08-16.md) | Web 管理面架构与实施路线 | M0 部分实施（开发骨架） |
| [开源自部署应用研究 Module 规范](./application-research-module-spec.md) | 可复用研究方法与字段规范 | 当前 v1.1 |
| [开源自部署 S3 兼容文件与对象服务调研（2026-08-15）](./self-hosted-open-source-s3-compatible-storage-research-2026-08-15.md) | 竞品与 module/外部资源选型 | 当前 |
| [开源自部署 Git 服务全景调研（2026-08-15）](./self-hosted-open-source-git-services-research-2026-08-15.md) | 竞品与 module 选型 | 当前 |
| [Super Productivity 与同类开源自部署项目调研（2026-08-15）](./super-productivity-alternatives-research-2026-08-15.md) | 竞品与 module 选型 | 当前 |
| [开源自部署笔记应用全景调研（2026-08-13）](./self-hosted-open-source-notes-research-2026-08-13.md) | 竞品与 module 选型 | 当前 |
| [中国大陆镜像与 CNB 发行方案（2026-08-11）](./china-mainland-mirrors-and-cnb-distribution-2026-08-11.md) | 构建、镜像和国内发行 | 当前 |
| [开源自部署 Kanban 应用全景调研（2026-08-10）](./self-hosted-open-source-kanban-research-2026-08-10.md) | 竞品与 module 选型 | 当前 |
| [Module 官方镜像切换与版本升级评估](./cask-official-image-and-upgrade-assessment-2026-07-29.md) | 上游镜像和版本评估 | 已实施，历史快照 |
| [ANAS 设计问题审查报告](./design-review-2026-07-19.md) | 代码与架构审查 | 历史快照 |

## 维护约定

- 文件名带调研快照日期；动态数据必须标明采集日期。
- Star、最新提交和版本不是质量分数，只作为社区规模和活跃度信号。
- 优先引用项目官网、官方文档、许可证和仓库；聚合站只用于发现候选。
- “开源”默认采用 OSI/FSF 通常语义。Fair-code、Business Source License 等源码可见项目单列，不混入严格开源推荐。
- 新选型文档必须说明与 ANAS 的 module、IAM、数据库、Traefik、备份和升级契约如何衔接。
- 新主题按[应用研究 Module 规范](./application-research-module-spec.md)定义范围、证据和交付物。
