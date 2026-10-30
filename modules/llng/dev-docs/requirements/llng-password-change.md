---
doc_type: requirement
status: current
created: 2026-10-23
updated: 2026-10-23
---

# LLNG Samba 强制改密要求

用户要求将已验证的两个 LLNG 原生开关接入 Module，并验证实际 OIDC 应用回跳。
本次使用 LLNG 2.23.2 自带 AD/LDAP 改密实现，不修改上游源码。研究见
[原生能力验证](../../../../docs/research/llng-samba-password-change.md)，执行结果见
[实施计划](../plans/llng-password-change.md)。

`ldapChangePasswordAsUser=1` 使 LLNG 提交旧密码和新密码，由 Samba 验证用户改密；
`ldapAllowResetExpiredPassword=1` 允许已过期密码进入改密流程。两项是本 Module 的固定行为，
新安装和已有持久配置启动时均须生效。LDAPS 证书验证继续启用。

LLNG 原生改密成功后要求用新密码重新登录。验收允许这个明确的步骤，但必须从改密后的
登录表单继续原 OIDC 请求，不能靠重新打开应用发起第二次授权来掩盖回跳丢失。实际应用选用
Vikunja；验收结果只覆盖记录的应用版本、数据库和运行基线，不替代全部 Consumer 验收。
错误旧密码不得获得改密表单或会话。

## 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| LLNG-PWD-R-001 | LLNG Module 新安装时启用上述两个原生开关 | e2e |
| LLNG-PWD-R-002 | LLNG Module 启动时将已有持久配置中的上述两个开关收敛为启用 | e2e |
| LLNG-PWD-R-003 | Samba `pwdLastSet=0` 用户改密并重新登录后，完成原始 OIDC 请求并建立实际应用会话 | e2e |
| LLNG-PWD-R-004 | Samba 密码真正到期的用户改密并重新登录后，完成原始 OIDC 请求并建立实际应用会话 | e2e |
| LLNG-PWD-R-005 | 强制改密或密码到期用户提交错误旧密码时，不得取得改密表单、SSO 会话或应用会话 | e2e |
