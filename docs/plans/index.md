# 实施计划

本目录记录已明确目标的落地顺序、里程碑、迁移和剩余工作。计划不是当前操作指南；完成后应把稳定结论沉淀到需求、架构、开发或运维文档，并从活跃计划中移除。

| 文档 | 范围 | 状态 |
| --- | --- | --- |
| [应用域与 Samba AD 域分离](domain-separation.md) | 参数契约、DNS 模式、迁移与验收 | 实施中 |
| [Core 与 Module Changelog](changelog-rollout.md) | change fragment、发布产物和流水线 | 提案 |
| [Web API 与管理前端](web-api-admin-console.md) | 管理面里程碑与剩余工作；验收依据见[要求](/requirements/web-api-admin-console) | 部分实施 |
| [VersityGW S3 兼容 Module](versitygw-module.md) | S3 Module、Capability/Resource、独立 bucket/凭据、客户端与恢复验收 | 实施中 |
| [workspace 与备份体系](workspace-backup.md) | workspace、snapshot、backup 与恢复 | 实施中 |
| [Forgejo Module](forgejo-module.md) | 安全开关、Incus compute、单作业 Runner 与发布门禁 | 部分实施 |
| [Vikunja Module](vikunja-module.md) | 多架构、双数据库、双 IAM、恢复、凭据轮换与负载发布验收 | 实施中 |

计划使用稳定主题文件名。创建日期、更新时间、状态和目标里程碑写在文档内，不因日常更新重命名。
