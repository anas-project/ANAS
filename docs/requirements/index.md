# 需求与验收标准

本目录记录系统必须满足的目标、范围、约束和验收标准，只回答“必须做到什么”，不混入候选调研、具体实现步骤或阶段进度。

| 文档 | 范围 | 状态 |
| --- | --- | --- |
| [新 IAM Provider 准入与实施要求](iam-provider.md) | 目录、OIDC/SAML、身份锚点、安全和 E2E 验收 | 当前 |
| [使用 OIDC/SAML 的 Module 双向登出要求](module-iam-bidirectional-logout.md) | RP/SP 发起登出、IAM 发起登出、通用注册、安全和真实会话 E2E | 当前 |
| [Vikunja Module 集成要求](vikunja-module.md) | 独立任务/看板服务、数据库、OIDC、持久化、登出和验收 | 当前 |
| [VersityGW S3 兼容 Module 集成要求](versitygw-module.md) | `object_storage/s3` Capability、per-Resource bucket/凭据、POSIX backend、安全和验收 | 当前 |

需求发生变化时原地更新稳定文件名，并同步 `updated`；实现方案进入 `architecture/`，落地顺序进入 `plans/`。
