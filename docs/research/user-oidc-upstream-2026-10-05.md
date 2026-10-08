# user_oidc 上游查重与 issue 建议

核查日期：2026-10-05。最初核查上游并准备 issue；同日后续在隔离环境完成统一 UID 与官方 8.11.0 原样制品验收，未发布正式镜像。

## 结论

**同日实机补充：**[统一 anchor UID 验收](https://github.com/anas-project/ANAS/blob/master/dev-docs/plans/directory-identity-key.md) 已通过。对于本项目无需迁移的新部署，将 LDAP 内部 UID 来源与 OIDC mapping 统一，并补充 LDAP 搜索属性和登录过滤器，可直接关联同一账号，不需要 LDAP 身份查询补丁。下面拟提交的 issue 仅适用于必须保留旧内部 UID 的场景，当前项目不再需要为此发布功能请求。

- 当前固定版本为 8.10.1。原始控制器与上游 v8.10.1 的 SHA256 相同：`8c1d99d3e7ae766f1b67ff641b881b09cfa391b37eb82a5783d1a988c18606fc`。
- 重复 back-channel logout 返回 400 已有 [issue #1430](https://github.com/nextcloud/user_oidc/issues/1430)，由 [PR #1431](https://github.com/nextcloud/user_oidc/pull/1431) 和 [PR #1432](https://github.com/nextcloud/user_oidc/pull/1432) 修复。[v8.11.0](https://github.com/nextcloud/user_oidc/releases/tag/v8.11.0) 已包含修复，源码也校验退出令牌 issuer。无需另开此问题。
- [PR #1498](https://github.com/nextcloud/user_oidc/pull/1498) 新增 provider_id/sub 查询，但作用于 user_oidc 自身账号表。在 `auto_provision=false` 的 LDAP 登录路径中，v8.11.0 仍然使用 UID mapping claim 调用 `userManager->get($userId)`，没有通过不可变目录身份解析既有 LDAP UID。
- LDAP 关联问题已有 [#507](https://github.com/nextcloud/user_oidc/issues/507)；用户名变更问题已有 [#547](https://github.com/nextcloud/user_oidc/issues/547)。本次检索未找到专门覆盖“LDAP 用户改名后，以不可变目录身份找回冻结的内部 UID”的 issue。可以提出具体功能请求，并明确引用这些相关讨论，由维护者决定合并范围。

## 验证范围

8.10.1 的原始失败与本地补丁恢复登录，已有隔离环境实机记录，见 [验收报告](https://github.com/anas-project/ANAS/blob/master/dev-docs/reviews/2026-10-04-casdoor-directory-identity-acceptance.md)。初次查重时 8.11.0 仅核查源码；同日后续已验收原样制品的新 anchor UID 配置，但没有实机复现“旧用户名 UID 必须保留”的 8.11.0 缺陷，不能混淆这两个场景。

官方配置要求 OIDC UID mapping 的值匹配 LDAP 内部 UID。因此此请求属于扩展现有账号关联方式，而不是声称所有 LDAP 配置都受影响。把 UID mapping 改为不可变 claim 并不能自动将该值转换为既有的 LDAP UID；如果 IdP 能直接提供那个既有 UID，则可能无需此扩展。

## 拟提交 issue

标题：`Support resolving existing LDAP users by an immutable directory claim after username changes`

英文正文保存在 [user-oidc-ldap-identity-issue.md](user-oidc-ldap-identity-issue.md)，不包含测试服务器地址、令牌或真实账号信息。尚未向 GitHub 发布。

同日后续：官方 8.11.0 原样制品的真实登录、改名、文件保留、组撤权和完整性检查已通过，模块移除了两项本地补丁。正式 Dockerfile 的 amd64 构建、Web/cron 重建、显示名称和身份回归也已通过；其他 Provider、桌面/移动客户端、ARM64 和完整版本升级矩阵仍待验收。旧 UID 必须保留场景的功能请求草稿不代表当前项目仍需要这项扩展。
