# 需求与验收标准

本目录记录系统必须满足的目标、范围、约束和验收标准，只回答“必须做到什么”，不混入候选调研、具体实现步骤或阶段进度。

| 文档 | 范围 | 状态 |
| --- | --- | --- |
| [新 IAM Provider 准入与实施要求](iam-provider.md) | 目录、OIDC/SAML、身份锚点、安全和 E2E 验收 | 当前 |
| [使用 OIDC/SAML 的 Module 双向登出要求](module-iam-bidirectional-logout.md) | RP/SP 发起登出、IAM 发起登出、通用注册、安全和真实会话 E2E | 当前 |
| [Web API 与管理前端要求](web-api-admin-console.md) | 管理控制台的范围、访问与证书、认证与角色、安全和验收 | 当前 |
| [Forgejo Module 集成要求](forgejo-module.md) | 代码托管、OIDC、Actions 单开关、Incus one-job VM Runner 与安全验收 | 当前 |
| [Vikunja Module 集成要求](vikunja-module.md) | 独立任务/看板服务、数据库、OIDC、持久化、登出和验收 | 当前 |
| [VersityGW S3 兼容 Module 集成要求](versitygw-module.md) | `object_storage/s3` Capability、per-Resource bucket/凭据、POSIX backend、安全和验收 | 当前 |

新增或修改需求文档前先读[需求编写规范](/developer/requirement-authoring)：它规定需求矩阵怎么产出、
一条需求的判据、验证方式如何选择，以及需求变化时 ID 怎么处理。

需求发生变化时原地更新稳定文件名，并同步 `updated`；实现方案进入 `architecture/`，落地顺序进入 `plans/`。
