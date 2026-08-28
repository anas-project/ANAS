---
doc_type: requirement
status: current
created: 2026-08-28
updated: 2026-08-28
---

# MeshCentral OIDC-only 验收要求

本文规定 MeshCentral 关闭密码认证后的安全边界和双 IAM 真实运行验收。实现细节见
[MeshCentral 技术文档](../../modules/meshcentral/docs/technical.md)，执行进度见
[MeshCentral OIDC-only 实施计划](/plans/meshcentral-oidc-only)。

## 1. 认证边界

浏览器登录必须只使用通用 IAM OIDC binding。隐藏密码表单不能代替服务端保护：中心密码认证器和
HTTP 密码登录入口都必须拒绝本地及 LDAP 密码，LDAPS 只用于目录 provisioning。固定上游源码补丁
必须校验精确锚点；上游结构变化时构建失败，不得静默生成失去保护的镜像。

## 2. 真实运行验收

Authentik 和 LLNG 必须分别在独立测试部署中验证匿名入口、登录页公开状态、密码 POST 拒绝、真实
Authorization Code 回调、持久化账号的目录 identity anchor、显示名和管理员映射。E2E fixture 必须
只使用公开配置字段；网络 namespace、网关、接口、Docker socket 等运行事实由 Runner 从专用隔离
环境解析，不能再通过原始 `env` 绕过所有权边界。

## 3. revision 与恢复账号

日常修改不提升 Module revision。正式 revision 由 `image-release` 计算并写回；只有 E2E 必须编译
尚未发布镜像时，测试副本才临时提升全部 revision 投影，且不得把临时值带入功能提交。

MeshCentral 没有应用内应急账号或密码登录地址。Module README 和技术文档必须明确这一点，并说明
IAM 故障时恢复 IAM/目录链路。若未来新增应急账号，文档必须同时给出登录地址、实际用户名和安全取密
方法，不得记录生成后的密码值。

## 4. 需求矩阵

| ID | 要求 | 验证方式 |
| --- | --- | --- |
| `MCO-R-001` | 固定版本补丁必须同时阻断中心密码认证器和 HTTP 密码登录处理器，并在上游锚点变化或重复应用时失败关闭 | 单元 |
| `MCO-R-002` | 真实 MeshCentral 匿名首页必须跳转 `/auth-oidc`，登录页必须只声明 OIDC 策略且关闭密码表单，密码 POST 必须返回 404 | e2e |
| `MCO-R-003` | Authentik 部署必须完成真实 OIDC 回调，并验证 MeshCentral 持久化账号的目录 identity anchor、显示名和管理员映射 | e2e |
| `MCO-R-004` | LLNG 部署必须完成真实 OIDC 回调，并验证 MeshCentral 持久化账号的目录 identity anchor、显示名和管理员映射 | e2e |
| `MCO-R-005` | Authentik 与 LLNG E2E fixture 必须使用公开 `global` 配置字段，并在专用 network namespace、Docker socket、data root 和 workspace 中运行 | 单元 + e2e |
| `MCO-R-006` | 正式 revision 必须由 `image-release` 计算写回；仅构建未发布 E2E 镜像时允许在测试副本临时提升并保持全部投影一致 | 审阅 |
| `MCO-R-007` | MeshCentral 文档必须明确没有应用内应急账号和密码入口；未来若新增，必须记录登录地址、实际用户名和安全取密方法 | 审阅 |
