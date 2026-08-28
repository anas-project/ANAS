---
doc_type: plan
status: done
created: 2026-08-28
updated: 2026-08-28
---

# MeshCentral OIDC-only 实施计划

验收依据是[MeshCentral OIDC-only 验收要求](../../requirements/meshcentral-oidc-only.md)。当前目标是在
`finance.hlong.wang` 的专用隔离 daemon 上完成 Authentik 与 LLNG 两套真实 OIDC 验收。

## 1. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：服务端强制、fixture 与文档边界 | R-001、R-005—R-007 | 已完成 |
| M1：Authentik 真实 OIDC | R-002—R-003 | 已完成 |
| M2：LLNG 真实 OIDC | R-004 | 已完成 |

## 2. 检查表

- [x] 固定版本补丁单元测试覆盖密码认证器、HTTP handler、上游锚点变化和重复应用。
- [x] 双 Provider 登录脚本共用 OIDC-only 浏览器可观察断言。
- [x] E2E fixture 改用公开 `global` 字段，不再注入 Runner-owned 网络变量。
- [x] Module 文档记录 revision 所有权和不存在应用内应急账号的恢复边界。
- [x] Authentik 部署完成 OIDC-only、回调、identity anchor、显示名和 site-admin 验收。
- [x] LLNG 独立部署完成相同验收。
- [x] 原始报告以 `0600` 保存，临时 r9 revision 在测试副本恢复为 r8。

## 3. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-002 | `server-authentik-oidc-login-e2e.sh`、`server-llng-oidc-login-e2e.sh` | finance / 独立双 Provider deployment | 2026-08-28 | 通过：两套环境均验证匿名入口、仅 OIDC 策略和密码 POST 404 |
| R-003 | `server-authentik-oidc-login-e2e.sh` | finance / 独立 Authentik daemon 与 workspace | 2026-08-28 | 通过：回调、identity anchor、Administrator 显示名、siteadmin 全部匹配 |
| R-004 | `server-llng-oidc-login-e2e.sh` | finance / 独立 LLNG deployment | 2026-08-28 | 通过：回调、identity anchor、Administrator 显示名、siteadmin 全部匹配 |
| R-005 | `server-authentik-oidc-login-e2e.sh`、`server-llng-oidc-login-e2e.sh` | finance / 专用 namespace、socket、data root 与 workspace | 2026-08-28 | 通过：未复用生产或既有测试 daemon，报告 mode 均为 `0600` |

Authentik 冷启动时，默认双 worker 被蓝图与 LDAP 父任务占满，`anas apply` 在蓝图 readiness 阶段回滚。
保留数据后使用 Authentik 官方 `Importer` 和 LDAP synchronizer 顺序完成相同蓝图与目录同步，随后正式
E2E 通过。该容量相关并发竞态不影响本计划对 MeshCentral OIDC-only 行为的验收，但应作为 Authentik
启动可靠性的独立后续事项处理。LLNG 场景的 `anas apply` 未使用恢复步骤，直接成功。

## 4. 验证命令

```bash
node modules/meshcentral/meshcentral/enforce-oidc-only.test.js
go test ./internal/runner -run TestServerIdentityFixturesUsePublicConfiguration
npm run docs:check-requirements
npm run test-cases:check
```

远端脚本必须显式使用 `/run/anas-meshcentral-oidc-e2e-docker.sock`，报告进入测试目录且 mode 为
`0600`。不得复用生产 daemon 或其他任务的测试 socket、data root、namespace 和 workspace。

## 5. 当前阻塞

- 无。GHCR 尚无 `anas-meshcentral:1.2.4-r8` manifest，本次按规则仅在远端测试副本临时使用 r9
  构建，测试完成后已恢复为 r8；正式 revision 仍由 `image-release` 写回。
