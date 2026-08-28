---
doc_type: plan
status: implementing
created: 2026-08-28
updated: 2026-08-28
---

# Module IAM 双向登出实施计划

验收依据是[使用 OIDC/SAML 的 Module 双向登出要求](../requirements/module-iam-bidirectional-logout.md)
的 `LOGOUT-R-###` 矩阵。固定版本能力结论见
[Module IAM / OIDC 支持清单](../../docs/reference/module-iam-support.md#固定版本登出矩阵)。实现已经落地；当前里程碑
只在真实 Provider、固定 Consumer 和真实会话证据齐全时升级状态，不以 Hook、render 或空 Playwright
报告替代 E2E。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：已知不支持方向与降级边界 | R-006—R-008、R-010、R-012、R-014、R-020—R-022、R-024、R-026、R-028、R-031—R-032、R-034—R-036、R-038、R-040、R-042 | 已完成 |
| M1：Nextcloud OIDC/SAML 双向登出复验 | R-001—R-004、R-015—R-018、R-029—R-030 | 实施中；历史路径已有证据，统一固定版本矩阵待重跑 |
| M2：只有应用发起或本地登出的 Consumer | R-005、R-009、R-011、R-013、R-019、R-023、R-025、R-027、R-033、R-037、R-039、R-041 | 实施中；Vikunja Authentik 已通过，其余统一矩阵待跑 |

## 2. 实施检查表

- [x] Runner 校验 HTTPS URL、成对字段、枚举、布尔值和当前协议字段，拒绝 OIDC/SAML 遗留字段。
- [x] Authentik、LLNG、Casdoor adapter 在声明消失、协议切换、域名变化和重复 apply 后收敛旧配置。
- [x] Nextcloud OIDC/SAML、MeshCentral、NetBird 和 oauth2-proxy 固定版本 endpoint 与降级语义落地。
- [x] 后续接入的 Forgejo、Vikunja 纳入完整组合空间；不支持方向逐格明示。
- [x] 中英文 Module 文档与支持清单区分本地、应用发起、浏览器双向和后台双向登出。
- [x] 统一 Playwright 入口、脱敏 reporter、`0600` 报告权限及非空/fixture/结果/泄密报告门禁已实现。
- [ ] 在专用隔离 Docker fixture 重跑 Authentik × OIDC/SAML 矩阵并归档非空报告。
- [ ] 在专用隔离 Docker fixture 重跑 LLNG × OIDC/SAML 矩阵并归档非空报告。
- [ ] 在专用隔离 Docker fixture 重跑 Casdoor × OIDC 矩阵及 SAML no-SLO 本地降级并归档非空报告。
- [ ] 对每个声称支持的组合核对原应用会话、IAM 中央会话、静默恢复、会话隔离和安全负例。

## 3. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-001 | `server-iam-logout-matrix-e2e.sh` | Authentik + Nextcloud OIDC + Chromium | — | 待重跑：Module→IAM、原 Cookie 与静默恢复 |
| R-002 | `server-authentik-oidc-login-e2e.sh`、统一 logout matrix | Authentik + Nextcloud OIDC | 历史 2026-08-21；本轮待重跑 | 历史浏览器/管理员撤销已有结论；当前统一报告缺少非空 fixture 证据 |
| R-003 | `server-authentik-nextcloud-login-e2e.sh`、统一 logout matrix | Authentik + Nextcloud SAML Redirect | 历史已通过；本轮待重跑 | 历史 SP/IdP Redirect 路径已有会话断言；本轮报告待归档 |
| R-004 | `server-authentik-nextcloud-login-e2e.sh`、统一 logout matrix | Authentik + Nextcloud SAML Redirect + Chromium | 历史已通过；本轮待重跑 | 浏览器 SLO；不覆盖管理员无浏览器撤销 |
| R-005 | `server-iam-logout-matrix-e2e.sh` | Authentik + MeshCentral `1.2.4` | — | 待执行：通过前保持“上游支持、待接入” |
| R-009 | `server-vikunja-e2e.sh`、`vikunja-browser.spec.mjs` | Authentik + Vikunja `2.4.0` | 2026-08-24 | 通过：正常 RP logout 与 IAM-down 本地优先失效 2/2 |
| R-011 | `server-iam-logout-matrix-e2e.sh` | Authentik + NetBird Dashboard `2.90.9` | — | 待执行：通过前保持“上游支持、待接入” |
| R-013 | `server-iam-logout-matrix-e2e.sh` | Authentik + oauth2-proxy `7.15.3` | — | 待执行：暂停 IAM 后验证网关 Cookie 清理 |
| R-015 | `server-iam-logout-matrix-e2e.sh` | LLNG + Nextcloud OIDC + Chromium | — | 待重跑：Module→IAM、原 Cookie 与静默恢复 |
| R-016 | `server-llng-oidc-login-e2e.sh`、统一 logout matrix | LLNG + Nextcloud OIDC | 历史已通过；本轮待重跑 | 历史 Portal 浏览器登出已有会话断言；管理员无浏览器结果待明确 |
| R-017 | `server-iam-logout-matrix-e2e.sh` | LLNG + Nextcloud SAML Redirect | — | 待执行：SP-Initiated SLO |
| R-018 | `server-iam-logout-matrix-e2e.sh` | LLNG + Nextcloud SAML Redirect + Chromium | — | 待执行：IdP-Initiated SLO；不覆盖管理员无浏览器撤销 |
| R-019 | `server-iam-logout-matrix-e2e.sh` | LLNG + MeshCentral `1.2.4` | — | 待执行：通过前保持“上游支持、待接入” |
| R-023 | `server-iam-logout-matrix-e2e.sh` | LLNG + Vikunja `2.4.0` | — | 待执行：RP logout 与 IAM-down 本地优先失效 |
| R-025 | `server-iam-logout-matrix-e2e.sh` | LLNG + NetBird Dashboard `2.90.9` | — | 待执行：通过前保持“上游支持、待接入” |
| R-027 | `server-iam-logout-matrix-e2e.sh` | LLNG + oauth2-proxy `7.15.3` | — | 待执行：暂停 IAM 后验证网关 Cookie 清理 |
| R-029 | `server-iam-logout-matrix-e2e.sh` | Casdoor + Nextcloud OIDC + Chromium | — | 待执行：Provider Consumer fixture 不能替代本组合 |
| R-030 | `server-casdoor-oidc-logout-e2e.sh`、统一 logout matrix | Casdoor + 标准 Consumer / Nextcloud receiver | 2026-08-27；Nextcloud 待重跑 | Provider exact-`sid`、多会话隔离和重放已通过；固定 Nextcloud receiver 待复验 |
| R-033 | `server-iam-logout-matrix-e2e.sh` | Casdoor + MeshCentral `1.2.4` | — | 待执行：通过前保持“上游支持、待接入” |
| R-037 | `server-iam-logout-matrix-e2e.sh` | Casdoor + Vikunja `2.4.0` | — | 待执行：RP logout 与 IAM-down 本地优先失效 |
| R-039 | `server-iam-logout-matrix-e2e.sh` | Casdoor + NetBird Dashboard `2.90.9` | — | 待执行：通过前保持“上游支持、待接入” |
| R-041 | `server-iam-logout-matrix-e2e.sh` | Casdoor + oauth2-proxy `7.15.3` | — | 待执行：暂停 IAM 后验证网关 Cookie 清理 |

## 4. 本轮执行状态

- 2026-08-28 本地 `go test ./...` 通过，覆盖 Runner 与各 Provider/Consumer Hook 的登出契约。
- `test:iam-logout-report` 2/2 通过；Playwright `--list` 产生的零执行结果已由新报告门禁拒绝。
- Authentik/LLNG OIDC/SAML 与 Casdoor OIDC 相关 render 均通过；完整 render 随后被既有 Forgejo
  Actions 陈旧断言阻塞，该断言不属于本主题。
- npm 依赖已同步为仓库锁定的 Playwright `1.62.1`；对应 Chromium 下载因 CDN 长时间无进展中止，
  不记录为浏览器执行证据。
- 已登记专用测试主机 `finance.hlong.wang` 和 `192.168.0.222` 在本轮均于 SSH 建连阶段超时；没有执行
  远端 Docker 重启或容器变更。
- `test-env/reports/iam-logout-playwright.json` 的 fixture 为 `unset` 且结果为空，继续视为无效证据；
  只有带固定 Provider/Consumer、非空结果和 `0600` 权限的报告才能更新上表状态。

## 5. 验证命令

```bash
go test ./...
go run ./cmd/gen-module-docs --check
npm run docs:check-requirements
npm run docs:requirement-status
npm run docs:check-requirement-status
npm run docs:build
bash -n test-env/scripts/server-iam-logout-matrix-e2e.sh
```

远端恢复可达后，必须显式使用专用 test Docker socket，并从 `0600` session artifact 读取一次性测试
账号；不得把凭据写入命令、计划或报告。完整矩阵通过前，M1/M2 保持“实施中”。
