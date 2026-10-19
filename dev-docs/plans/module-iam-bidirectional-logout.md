---
doc_type: plan
status: implementing
created: 2026-08-28
updated: 2026-10-19
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
| M0：已知不支持方向与降级边界 | R-020—R-022、R-024、R-026、R-028、R-031—R-032、R-034—R-036、R-038、R-040、R-042 | 已完成 |
| M1：Nextcloud OIDC/SAML 双向登出复验 | R-015—R-018、R-029—R-030 | 实施中；历史路径已有证据，统一固定版本矩阵待重跑 |
| M2：只有应用发起或本地登出的 Consumer | R-019、R-023、R-025、R-027、R-033、R-037、R-039、R-041 | 实施中；保留的 Provider 统一矩阵待跑；旧 Authentik 证据只保留历史边界 |

## 2. 实施检查表

- [x] Runner 校验 HTTPS URL、成对字段、枚举、布尔值和当前协议字段，拒绝 OIDC/SAML 遗留字段。
- [x] LLNG、Casdoor adapter 在声明消失、协议切换、域名变化和重复 apply 后收敛旧配置。
- [x] Nextcloud OIDC/SAML、MeshCentral、NetBird 和 oauth2-proxy 固定版本 endpoint 与降级语义落地。
- [x] 后续接入的 Forgejo、Vikunja 纳入完整组合空间；不支持方向逐格明示。
- [x] 中英文 Module 文档与支持清单区分本地、应用发起、浏览器双向和后台双向登出。
- [x] 统一 Playwright 入口、脱敏 reporter、`0600` 报告权限及非空/fixture/结果/泄密报告门禁已实现。
- [x] 2026-10-10 按用户要求移除 Authentik，并废弃专属 R-001—R-014；不重新分配旧 ID。
- [ ] 在专用隔离 Docker fixture 重跑 LLNG × OIDC/SAML 矩阵并归档非空报告。
- [ ] 在专用隔离 Docker fixture 重跑 Casdoor × OIDC 矩阵及 SAML no-SLO 本地降级并归档非空报告。
- [ ] 对每个声称支持的组合核对原应用会话、IAM 中央会话、静默恢复、会话隔离和安全负例。

## 3. CI 门禁

| 门禁 | 最近全绿提交 |
| --- | --- |
| `go test ./...`（Runner 字段校验与各 Provider/Consumer Hook 的登出契约） | `25433bd`（GitHub CI，2026-08-29），已包含实现 `836564c` 与证据加固 `918bae6`。`5306b63` 上全部 20 个 `modules/*/hook` 包通过，Runner 侧校验所在的 `internal/runner` 包因无关用例失败；其后提交均未经 CI |
| `go run ./cmd/gen-module-docs --check` | `5306b63`（GitHub CI，2026-09-05） |
| `npm run docs:check-requirements`、`docs:check-requirement-status` | `5306b63`（GitHub CI）；本计划与要求文档在那之后直到本次补写检查表都没有改动，该次运行校验的就是现行归属表与 e2e 记录；最近本地记录 `6823232` |
| `npm run test:iam-logout-report` | 不在 CI 中；本地记录见 §6（2/2） |
| `bash -n test-env/scripts/server-iam-logout-matrix-e2e.sh`、`npm run docs:build` | 不在 `ci.yml` 中，没有带提交号的记录 |

## 4. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
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

## 5. 文档同步

| 文档 | 需要的变更 | 状态 |
| --- | --- | --- |
| [Module IAM / OIDC 支持清单](../../docs/reference/module-iam-support.md#固定版本登出矩阵)与英文镜像的「固定版本登出矩阵」 | 区分本地、应用发起、浏览器双向与后台双向登出，不支持方向逐格明示 | 已完成 |
| `nextcloud`、`meshcentral`、`netbird`、`oauth2_proxy`、`vikunja`、`forgejo` 的技术文档（中英文） | 各自的登出能力与降级语义 | 已完成 |
| 同上两处 | M1、M2 的统一矩阵重跑通过后，把「上游支持、待接入」等条目改为实测结论 | 未开始 |

## 6. 本轮执行状态

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

## 7. 验证命令

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

## 8. 已移除 Provider 的历史记录（2026-10-10）

Authentik 的专属需求与用例已经废弃，原脚本已移除、不可执行。以下仅保全此前执行边界，
不进入保留 Provider 的验收覆盖；Casdoor 不继承旧通过结果。

- 原记录： /  R-001  /  `server-iam-logout-matrix-e2e.sh`  /  Authentik + Nextcloud OIDC + Chromium  /  —  /  待重跑：Module→IAM、原 Cookie 与静默恢复  /
- 原记录： /  R-002  /  `server-authentik-oidc-login-e2e.sh`、统一 logout matrix  /  Authentik + Nextcloud OIDC  /  历史 2026-08-21；本轮待重跑  /  历史浏览器/管理员撤销已有结论；当前统一报告缺少非空 fixture 证据  /
- 原记录： /  R-003  /  `server-authentik-nextcloud-login-e2e.sh`、统一 logout matrix  /  Authentik + Nextcloud SAML Redirect  /  历史已通过；本轮待重跑  /  历史 SP/IdP Redirect 路径已有会话断言；本轮报告待归档  /
- 原记录： /  R-004  /  `server-authentik-nextcloud-login-e2e.sh`、统一 logout matrix  /  Authentik + Nextcloud SAML Redirect + Chromium  /  历史已通过；本轮待重跑  /  浏览器 SLO；不覆盖管理员无浏览器撤销  /
- 原记录： /  R-005  /  `server-iam-logout-matrix-e2e.sh`  /  Authentik + MeshCentral `1.2.4`  /  —  /  待执行：通过前保持“上游支持、待接入”  /
- 原记录： /  R-009  /  `server-vikunja-e2e.sh`、`vikunja-browser.spec.mjs`  /  Authentik + Vikunja `2.4.0`  /  2026-08-24  /  通过：正常 RP logout 与 IAM-down 本地优先失效 2/2  /
- 原记录： /  R-011  /  `server-iam-logout-matrix-e2e.sh`  /  Authentik + NetBird Dashboard `2.90.9`  /  —  /  待执行：通过前保持“上游支持、待接入”  /
- 原记录： /  R-013  /  `server-iam-logout-matrix-e2e.sh`  /  Authentik + oauth2-proxy `7.15.3`  /  —  /  待执行：暂停 IAM 后验证网关 Cookie 清理  /


## 9. 2026-10-19 finance 当前浏览器联动复验

本轮按用户要求在 finance 的 10000 入口验证现有门户、Casdoor、Nextcloud OIDC 与 Immich；未新增 Casdoor/Nextcloud 补丁。门户基线是部署 `20261018T194224Z-f4c09715`，镜像 `sha256:4b5bc59177b8ba3281cc327c5891a6be349a2b7de76684bb241da7954eafc06e`。其余制品标识及宿主健康记录在 finance `/home/whl/anas-portal-session-20261019/evidence/`。只使用标记的随机测试用户；原生 OIDC/API 回调签发真实 Cookie，未注入会话、伪造 auth_time 或用登录页外观推断退出。

| 需求/检查范围 | 入口与原始结果 | 2026-10-19 结论 |
| --- | --- | --- |
| LOGOUT-R-029 当前会话功能路径 | `native-cross-sls.json`；Nextcloud `user_oidc` 原生 `/apps/user_oidc/sls` | 四个服务退出前均为同一身份的有效会话；退出后门户/Nextcloud/Immich 的受保护 API 均 401，IAM 无账号；重新访问门户进入 IAM，未静默恢复。功能路径通过，不代表统一矩阵全绿 |
| LOGOUT-R-030 当前会话联动观察 | `cross-logout-final.json` 的 portal 场景 | 门户退出后 IAM/门户/Immich 先失效，首次 Nextcloud 仍有效；等待后第二次 Nextcloud 为 401，再访问也未恢复。证明异步联动，未单独验证 exact sid、多浏览器隔离、token 重放或错误签名 |
| 独立本地认证边界 | 当前应用目录：Collabora 管理、Traefik 管理为 local；Adminer 未启用 | 不声称本地管理凭据会随 IAM 登出，不把可见卡片当作已认证会话 |

测试更正：早期脚本在缺少退出菜单链接时退回 Nextcloud 通用 `/logout`，观察到它只结束 Nextcloud 本地会话；此结果不能代替用户菜单的 OIDC 单点退出结论。固定 `user_oidc` 的 Backend 为 OIDC 会话提供 `/sls`，该入口先清本地会话再使用 discovery 的 end_session_endpoint。统一测试的 OIDC fallback 因此改为 `OC.generateUrl('/apps/user_oidc/sls')`，SAML fallback 保留原路径。原先断言未放宽；统一固定版本矩阵本轮没有完整重跑，M1/M2 保持实施中。

宿主限制：经用户授权重启现有测试 Samba DC 后 LDAP 曾恢复，随后再次健康超时；观测到主机 load 48.81、swap 约 4 GiB、I/O 等待 34–37%。本轮功能正例有独立原始 API 结果支撑，不据此宣布宿主健康或此前全部回归通过。早期账号回归失败、浏览器导航等待与 SSH 转发失效记录均保留。门户功能及清理结果继续记在[门户计划 §19](../../modules/user_portal/dev-docs/plans/archived/user-portal.md#19-2026-10-19-退出主页分级验证与跨应用退出检查)，完整矩阵仍按本计划原标准验收。


最终证据与清理：同一 finance 目录的 `final-browser-evidence.tgz`，SHA256 `5d66b14b029e5cd2807ac6f0c486a05fdf1bb2368e58ca87f9c4c58c8a814ffb`，含 106 个成员及摘要清单；计划及最后门禁在 `final-closure/`。两轮 16 个 IAM 名称均已停用/删除或不存在，活动身份 0；原 67 个目录身份/安全/锚点不变，其他 30 个容器及 8 恢复点保留，4 个正式受限绑定保留；测试密码及 SSH 转发已清理。最后 Samba/门户/anchor/dirwatch healthy，但资源压力未根治，完整矩阵仍未完成。


## 10. 2026-10-19 旧网页会话反例与覆盖补齐

用户报告门户退出后 Immich 仍可使用。本轮通过当前 Chrome 的真实刷新确认 `/api/users/me` 等受保护请求仍为 200，不能归为纯页面缓存。§9 的同批 API 授权/Cookie 检查保留为当时局部证据，**不得扩展为任意既有网页会话都能退出**。只读查询显示该 Immich 会话使用较早的授权，父会话与近期门户授权不同，原 OIDC Token 为一小时而 RP 会话未设 expiresAt；未根据 Token 已到期直接推断 RP 会话失效。

新增稳定入口 [PORTAL-T-010](../../test-env/cases/user-portal/README.md)，实际执行 `test-env/playwright/user-portal-logout.mjs`。默认检查分类及门户/Nextcloud/Immich 三个退出来源、旧 Bearer、刷新和另一浏览器隔离，并增加仅丢失 IAM Cookie 与缓存登录数据后正常重新登录、保留旧 RP Cookie 的场景。没有完成原生网页前置认证的运行不得记作登出结果，5xx/无身份字段不能算退出。旧会话场景的产品范围/新增流程需用户确认，不擅自改成撤销该用户所有设备，也不放宽 exact sid 与重放要求。

当前结果与失败明细统一记录在[门户计划 §20](../../modules/user_portal/dev-docs/plans/archived/user-portal.md#20-2026-10-19-e2e-覆盖核查分类范围调整与旧会话反例)。M1/M2 继续实施中，完整固定 Provider 矩阵和真实移动端未因此完成。本轮未新增 Casdoor 源码补丁。

最终定向驱动已通过原凭据 200、仅 IAM 站点数据丢失、旧 RP Cookie 保留及 IAM 正常重新认证的前置对照；门户退出后观测到门户 401、IAM 明确未登录，Nextcloud/Immich 仍 200。这是可重复的 IAM 会话变化后的联动缺口；有效 Logout Token 的签名及接收端处理仍按既有矩阵独立验收。还需按确认后的范围补齐流程，不能用撤销其他设备来规避隔离要求。最终结果、刷新和旧凭据状态继续由门户计划收口。
