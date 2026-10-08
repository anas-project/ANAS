---
doc_type: requirement
status: current
created: 2026-10-03
updated: 2026-10-07
---

# Immich Module 集成要求

只支持空库安装及固定 Immich `v3.2.4`。照片、视频、相册与媒体路径需要数据库和文件共同恢复，
受管媒体放在 `data/immich/media`。共享 PG 的扩展、认证、受控维护和恢复不在本矩阵重复定义，
分别遵守 [RDBEXT](../../../../dev-docs/requirements/relational-database-extensions.md)、
[CRED](../../../../dev-docs/requirements/credential-rotation.md) 与既有备份主题；应用使用普通角色。

原生 OAuth callback 与受信 roleClaim 完成 JIT 建号。关闭密码登录和 setup 不足以保护运行中的身份：
固定源码仍有管理建号、密码写入、个人和全局解绑入口，因此只为已确认入口提供精确拒绝补丁。
不建立本地初始管理员、历史迁移器、独立身份框架或照片删除式撤权。目录 anchor 只作身份键，
用户的内部 id、媒体路径和公开 URL 继续归上游。账号删除后重登和并发建号必须实测，不能仅靠
无空 oauthId 的 guard 宣称唯一绑定。

普通登出、IAM 登出与目录撤权分别验收，并遵守已有
[双向登出要求](../../../../dev-docs/requirements/module-iam-bidirectional-logout.md)的签名、目标范围与重放边界。
上游 backchannel 只撤销 session，不证明 API key/分享
或目录撤权已交付。服务器灾备使用 ANAS；移动端上传备份仍需真实客户端验收。显式 ML 和并发设置
不构成 4GB 整机支持声明。计划见[配套计划](../plans/immich-module.md)。
登出 token 校验、jti 防重放和通用注册继承 [ALOG 双向登出矩阵](../../../../dev-docs/requirements/module-iam-bidirectional-logout.md)，
不在本矩阵重复定义安全标准。

本矩阵是验收规范来源；验证证据和完成状态在配套计划维护。

当前 finance 接入目标保留 Casdoor。固定 Casdoor `3.143.0-r11` 的可选受信角色映射与策略撤权
须独立验证 IMMI-R-001—R-005、R-008—R-010，不能复用 Authentik 通过结果推断 Casdoor 通过。
这两个能力使用现有 IAM 声明，普通 OIDC Consumer 不必请求；不增加 Immich 专属 Casdoor 配置。
真实手机客户端验收仍是 release 的强制条件，缺少设备不能以浏览器/API 证据替代。

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `IMMI-R-001` | 空库首次普通 OIDC 访客不得建号；受信 IAM admin roleClaim 必须能原生建立首管理员 | e2e |
| `IMMI-R-002` | JIT 用户必须满足 anchor = sub = oauthId；同 sub 改邮箱仍命中原内部 user.id | e2e |
| `IMMI-R-003` | 不同 sub 使用已绑定用户的邮箱必须拒绝且不改变原身份 | e2e |
| `IMMI-R-004` | 服务端必须拒绝本地/无绑定建号、密码写入、OAuth link/unlink 及管理员全局 unlink；补丁锚点变化时构建失败 | 单元 + e2e |
| `IMMI-R-005` | 同 sub 并发首次登录与软删除后重登不得产生多个有效绑定或接管旧资产 | e2e |
| `IMMI-R-006` | 关闭 ML 必须同时禁用 ML 容器与应用 ML 配置；并发参数必须拒绝非法值并投影到对应队列 | 单元 |
| `IMMI-R-007` | Web/API 与真实移动客户端必须可上传照片和视频、读取原件并管理相册 | e2e |
| `IMMI-R-008` | 普通退出必须撤销对应 Immich 会话，再按原生流程退出 IAM，旧凭据必须无法恢复该会话 | e2e |
| `IMMI-R-009` | IAM 发出的有效 backchannel logout 必须撤销对应移动/Web 会话；错误 issuer/audience/签名不得撤销 | e2e |
| `IMMI-R-010` | 目录停用、删除、移出准入组或失去受信管理员角色必须使变更前授权的既有会话、API key 和相关分享访问失效，且不删除媒体或改变 anchor 绑定 | e2e |
| `IMMI-R-011` | 重复 apply 和重启必须保留媒体、相册、身份及模块专属 Valkey 队列；队列存储满时不得报告健康上传成功 | e2e |
| `IMMI-R-012` | ANAS 全 workspace 恢复后必须验证照片/视频 hash、相册、身份、配置/Secret、匹配镜像及其他共享 PG Consumer 数据 | e2e |
| `IMMI-R-013` | 只读外部图库必须说明源文件独立备份范围，不得声称受管媒体备份自动包含源文件或继承 AD ACL | 审阅 |
| `IMMI-R-014` | Module 不得将 anchor 用作 storageLabel、公开路径或媒体目录；受管媒体必须挂载 data/immich/media | 单元 + 审阅 |
