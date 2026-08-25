# Vikunja Module 设计规范符合性审查

> 状态：**2026-08-21 工作树历史快照**。
> 基线：`vikunja 2.4.0-r1`，Manifest 状态 `developing`。

本审查使用 [Module 设计与发布检查表](/developer/module-design-checklist)，检查
`modules/vikunja/`、相关需求、全局清单和当前自动门禁。本页记录审查时状态，
不替代持续维护的 Module README 或架构规范。

## 修复跟踪（2026-08-21）

| 偏差 | 当前状态 | 修复证据 |
| --- | --- | --- |
| D1 Hook phases | 已修复 | Manifest 精确声明 `calculate`、`render_env` 与三个 credential lifecycle phases |
| D2 `iam_protocol` | 已修复 | 公开 enum 已收窄为 `[auto, oidc]` |
| D3 `domain_prefix` | 已修复 | 声明 1..63 长度与 DNS label pattern，并进入统一 constraints inventory |
| D4 全局库存 | 已修复 | 架构清单加入 Vikunja；中英文统计、参数表、effect 与约束数同步为 20/151/15 |
| D5 Secret 轮换 | 已修复并完成 E2E | 两项单目标、`--module vikunja`、deployment `--all` 的真实 candidate，以及 candidate 失败后 previous 恢复均已通过 |

因此本页下方“待修复偏差”保留为审查发现的历史记录，不再表示当前代码仍缺少 D1–D5 实现。
Module 仍为 `developing`，等待显式发布决定；需求矩阵与真实发布验收已经完成。

## 真实环境补充（2026-08-23）

- D1—D4 修复在 `2.4.0-r3` 上继续通过生成器、schema/Hook 测试和真实容器验证：Hook phase
  allowlist 显式存在，`iam_protocol` 只有 `auto/oidc`，`domain_prefix` 有 DNS label 约束，全局
  Module/配置库存包含 Vikunja 并同步为 20/151。
- amd64 非 root/read-only runtime、arm64 交叉构建、PostgreSQL 冷启动与重启持久性通过。
- Authentik 五账号授权矩阵、Chromium 直接组登录、API v2、附件、CalDAV 与 HMAC webhook 通过。
- 两项单目标、Module 范围和 deployment `--all` 的真实轮换成功；service secret 轮换后旧 JWT
  返回 401，OIDC secret/Module/`--all` 后全新授权码登录成功。
- Chromium 验证正常 RP logout 携带 `id_token_hint`/post-logout URI；IAM-down 时本地 token 也会
  立即清除，R-022 已关闭。
- PostgreSQL 与 MariaDB 均完成真实 r4→r3→r4 往返；恢复、API/Webhook/CalDAV、轮换失败补偿、
  1k/10k 负载与真实 Chromium 首屏均已通过。需求状态为 `28/28`。

## 结论

**对 `developing` 状态有条件通过；不符合提升为 `release` 的条件。**

Manifest、数据库 Resource、Provider-neutral OIDC、Secret 作用域、持久化、非 root 运行、
本地化和双语 Module 文档的主体设计符合当前规范。所有已运行的机械门禁通过。
审查快照当时在 Manifest 精确性、单字段约束、凭据轮换和全局文档清单中发现五项偏差；
实现修复状态见上表。真实容器、双数据库、双 IAM、恢复、升级/回滚、凭据轮换和负载验收均已完成。

## 通过项

| 检查面 | 结果 | 证据 |
| --- | --- | --- |
| Module 身份与打包 | 通过 | `anas.module/v1`、固定 `2.4.0-r1`、Compose runtime、`.github/modules.json` 含 amd64/arm64 |
| 依赖与数据库 | 通过 | Traefik 显式依赖；`relational_database` 支持 postgres/mariadb；`primary_database` 专属凭据且 `retain` |
| IAM 边界 | 通过 | 只消费 Vikunja binding；发布通用 client registration；本地认证/注册关闭；不伪造 logout receiver |
| Secret 与运行时 | 通过 | OIDC/service/Resource Secret 分离；read-only rootfs；入口准备附件树后永久降权到 `1000:1000` |
| 存储与备份边界 | 通过（设计） | 附件显式绑定 `${DATA_PATH}/vikunja/files`，其他业务状态在专属数据库 |
| 本地化 | 通过 | 固定 2.4.0 语言清单、BCP 47 映射、fallback warning、TZ 投影与版本证据 |
| Module 双语文档 | 通过 | README 和 technical 中英文齐全，参数表与生成标记无漂移 |

## 待修复偏差

### D1. Hook phase 未在 Manifest 显式列出

`modules/vikunja/module.yml` 声明了 Hook command，但没有 `logic.hook.phases`。当前 Runner
会使用旧 Hook 兼容集合，所以运行和测试没有失败；但新 Module 应把实际实现的
`calculate, render_env` 写成精确 allowlist，使 Manifest 与技术文档一致。

### D2. `iam_protocol` 公开了不支持的 `saml`

`config.types.iam_protocol` 允许 `[auto, oidc, saml]`，但 Capability/identity interface 和 Hook
只接受 OIDC。这使 CLI/schema 先接受一个必定在拓扑解析或 Hook 阶段失败的值。
应将公开 enum 收窄为 `[auto, oidc]`，或在通用 schema 若有强制原因时记录该原因。

### D3. `domain_prefix` 缺少 DNS label 单字段约束

`domain_prefix` 目前是无 constraints 的普通 string。空值、过长值或非法 label 可以进入
calculate，直到域名/Compose/路由后续阶段才失败。应在 Manifest 声明非空、最大 63 字符
和 DNS label pattern，并增加拒绝用例。

### D4. 全局文档库存与实现漂移

与 Vikunja 相关的生成 Module/Contract/IAM/localization 页已更新，但仍有人工清单过期：

- `docs/architecture/module-contract-resource-design.md` 的内置 Module 清单没有 `vikunja`；
- `docs/developer/module-documentation.md` 仍记录 19 个 Module/146 个参数，而当前测试基线为 20/151；
- `docs/reference/configuration.md` 及对应英文页的标题、说明和测试参数计数仍为 146。

这些页在 Module 新增时应同步；生成器当前没有对所有人工统计建立漂移检查。

### D5. ANAS-managed service/OIDC Secret 没有轮换 lifecycle

Hook 使用 Secret Store 稳定生成 `VIKUNJA_SERVICE_SECRET` 和
`VIKUNJA_OIDC_CLIENT_SECRET`，但 Manifest 没有为两者声明 `credentials.provides`、
probe/reconcile/verify handler 或其他可执行轮换 lifecycle。因此当前既不能单独轮换，
也不会被 Module/deployment 批量轮换覆盖。

service secret 变更的会话/token 影响，以及 OIDC client secret 在 IAM Provider 和 Vikunja
两端的原子对账、验证与回滚尚未实现。在这些值由 ANAS 管理的前提下，该项应作为
`release` 门禁，不能只依赖删除 Secret 后重新生成。

## `release` 阻断项

以下项是 `developing` 的已知验收缺口，不是已通过的能力：

- amd64/arm64 真实镜像构建、启动和健康检查；
- PostgreSQL/MariaDB 空库安装、重启、重复 apply 和升级；
- Authentik/LLNG 真实 OIDC 登录、JIT、Group 允许/拒绝、本地注册关闭；
- 应用发起登出、原 Cookie 失效和 IAM-down 降级；
- 数据库+附件+Secret+元数据的同点备份恢复；
- API token/webhook smoke test 与 NAS 资源基线。
- `VIKUNJA_SERVICE_SECRET` 与 `VIKUNJA_OIDC_CLIENT_SECRET` 的单目标、Module 范围和 deployment 范围真实 IAM/容器 E2E 轮换证据。

## 已运行验证

2026-08-21 审查时通过：

```bash
go test ./internal/modulepackage ./internal/runner ./modules/vikunja/hook ./modules/vikunja/vikunja
go run ./cmd/gen-module-docs --check
go run ./cmd/gen-contract-docs --check
docker compose -f modules/vikunja/docker-compose.yml config --no-interpolate --no-path-resolution -q
```

先前实现轮次还已通过 `go test ./...`、Linux amd64/arm64 编译、VitePress 构建和
`git diff --check`。当前主机 Docker daemon 未运行，因此不存在可声明的真实容器 E2E 证据。
