# Module 设计与发布检查表

> 状态：**当前基线**。更新：2026-08-21。

本检查表供新建、升级或评审 ANAS Module 时使用。它汇总现有的
[Module 开发规范](/developer/module-development)、
[Module、Contract 与 Resource 设计](/architecture/module-contract-resource-design)、
[Core 实现标准](/architecture/core-implementation-standard)、
[Module 文档规范](/developer/module-documentation)和
[Module 升级 SOP](/developer/module-upgrade-sop)。若本页与权威规范冲突，以权威规范为准。

## 使用方法

- `[A]` 表示可由现有测试或生成器机械验证；`[M]` 表示必须人工审查；`[E]` 表示需要真实环境证据。
- 不适用的项必须写 `N/A` 及原因，不得直接删除。
- `developing` Module 可以保留明确列出的 `[E]` 待验收项；提升为 `release` 前，所有适用的发布门禁必须有可复现证据。
- 评审记录至少填写：Module、上游版本、revision、目标状态、平台、数据库/IAM 组合、评审基线、评审人和日期。

## 1. 边界与所有权

- [ ] `[M]` 功能是独立的发布/部署单元，不是一次性 operation 或只有内部意义的辅助脚本。
- [ ] `[M]` Module 私有参数、派生值、跨参数不变量和上游适配位于 Module，没有向 Core 添加按 Module 名称或环境变量前缀的分支。
- [ ] `[M]` 跨 Module 能力通过显式 Module 依赖、Capability、Contract、Resource 或其他中立 ABI 表达，没有隐式产品耦合。
- [ ] `[A/M]` 冻结的 deployment/Module 包包含 Compose、Hook 和所有运行资产，运行时不依赖仓库检出路径。

## 2. Manifest、版本与打包

- [ ] `[A]` 目录名、`module.yml` 的 `name`、`api_version`、`kind`、`runtime`、Compose 路径和 Hook ABI 合法且一致。
- [ ] `[A/M]` `version` 是规范 SemVer 上游版本，`revision` 符合发布流程；镜像、`localization.yml` 和生成文档投影一致，不使用 `latest`。
- [ ] `[M]` `status` 与已有证据一致；未完成真实安装、升级、恢复和安全 E2E 时不声明稳定发布。
- [ ] `[A]` Module 已登记到 `.github/modules.json`，平台和 `shared_contexts` 覆盖真实构建需求，正式 Module 集合与机器清单一致。
- [ ] `[M]` `upgrade.from` 与 `upgrade.data_breaking` 分别表达来源版本兼容性和磁盘数据断代；确认无断代时显式声明 `data_breaking: []`。

## 3. 依赖、Contract 与 Resource

- [ ] `[M]` 硬依赖仅用于真正必须的具体 Module；可替换 Provider 使用 Capability 或 Contract 解析。
- [ ] `[A/M]` Contract 名称、版本范围、interface、`selected_by` 和默认 Provider 准确，Consumer 不读取 Provider 私有文件或管理凭据。
- [ ] `[A/M]` 每个持久对象都有稳定 Resource ID、最小权限 principal、独立凭据和明确 `deletion_policy`；默认不随 Consumer 删除数据。
- [ ] `[E]` Provider `ensure` 可重复执行，新增 Consumer 不重启 Provider 或已有 Consumer。

## 4. 配置 Schema 与变更语义

- [ ] `[A]` `defaults`、`types`、`required/input_required`、`must_resolve` 和 `changes` 的并集中，每个内置参数都有显式类型，没有 `unknown`。
- [ ] `[M]` 默认值、`default_source`、输入必填和最终必有的语义符合真实解析路径。
- [ ] `[A/M]` 可用单字段表达的长度、范围、pattern 和 format 写入 `constraints`；条件或跨字段规则由 resolver/plan/`validate` Hook 拒绝，不由 Core 猜测修正。
- [ ] `[M]` 每个参数的 `effect`、可编辑性、显式 operation 和数据/凭据/容器影响准确，未实现的 operation 不被写成可执行命令。
- [ ] `[A/M]` `config.exports`、`config.consumes` 仅列出真实边界；命名符合 upper-snake，通配符不是裸 `*`，不依赖全量环境注入。

## 5. Hook 与 lifecycle

- [ ] `[A/M]` 新 Module 在 `logic.hook.phases` 显式列出精确 phase allowlist，技术文档与 Manifest 一致；不借用旧 Hook 的隐式兼容集合。
- [ ] `[A]` Hook 拒绝不支持的 ABI，输出只有单个合法 JSON 值，错误不回显 Secret。
- [ ] `[A/M]` `validate` 无副作用、不读 Secret；`calculate` 只生成所有权范围内的派生值/Secret；`render_env` 不修改外部持久状态。
- [ ] `[A/E]` 初始化、reconcile、重复 `apply`、重启和中断重试收敛到同一状态；有 mutation 的 lifecycle 说明验证、失败补偿和回滚。

## 6. Secret、凭据与管理面

- [ ] `[A/M]` Secret 使用稳定 key 保存于 Secret Store/Resource store，不进入 workspace YAML、argv、日志、deployment manifest 或非必要容器。
- [ ] `[M]` 服务身份、Resource 凭据、API token 与人机本地账号分类正确，没有把应用 Secret 伪装成 `management.local_accounts`。
- [ ] `[M/E]` 每个管理界面都声明真实认证拓扑。只在有真实直登入口、更新机制和验证路径时才声明本地账号及 apply/rotate。
- [ ] `[E]` 声明凭据轮换时，已验证新值生效、旧值失效、失败回滚和明文不泄露。

### ANAS 管理的密码/密钥轮换

- [ ] `[A/M]` 盘点 ANAS 生成、保存或有权更新的每个密码、shared secret、client secret 和签名/加密密钥；每项都标注 owner、consumer、authority、轮换方式与不可轮换理由。
- [ ] `[A/E]` 每个可轮换值都有单目标命令，并完成 candidate、应用内更新、probe/verify、Secret Store 提交与失败回滚；只生成一个新值或重建容器不算轮换。
- [ ] `[M/E]` 同一 Module 拥有多个统一 lifecycle 凭据时，评审必须验证 `anas credential rotate --module MODULE` 的完整集合、顺序、共享 consumer/冻结别名投影、停机范围和原子性；Resource、本地管理员等未纳入类别必须显式标为未实现，不得用多次手工命令冒充跨类别原子操作。
- [ ] `[M/E]` 部署级“全部轮换”明确列出包含和排除的凭据类型，共用同一 planner/ready barrier；manual/unsupported 项必须成为显式 blocker 或 `manual` 排除项，执行器不得改写排除项或静默部分失败。
- [ ] `[M]` 文档不得把 `anas credential rotate --all` 称为“所有 ANAS Secret”：当前它只覆盖 active deployment 中可执行的 `credentials.provides` reconcile 凭据；Resource credential、本地管理员和外部 API token 仍有独立边界。

## 7. Compose、镜像与运行时安全

- [ ] `[A]` Compose 可解析，service、image/build、network、volume、healthcheck 与 Manifest/文档一致。
- [ ] `[M]` 只暴露业务所需端口；Web 界面经受管 Traefik/TLS 路由，没有多余未认证宿主端口。
- [ ] `[M/E]` 业务进程以非 root 运行。如启动时需 root，权限准备范围最小并在 `exec` 前不可逆降权；按上游能力使用 read-only rootfs、tmpfs 和最小可写 volume。
- [ ] `[M]` 持久目录都显式绑定到 workspace 受管路径，不依赖匿名 volume；入口脚本不跟随不受信路径中的 symlink。
- [ ] `[M/E]` 健康检查覆盖真实应用及必要依赖，不只检查 PID；失败语义与 restart policy 一致。
- [ ] `[M/E]` 内部 DNS/CA 和上游 TLS 验证正确，不使用 skip-verify 作为正式路径。
- [ ] `[E]` 声明的 `linux/amd64`、`linux/arm64` 等平台都能构建、启动并通过健康检查。

## 8. 数据、备份与升级

修改上游版本或运行资产时，还必须完成
[Module 升级检查表](/developer/module-upgrade-checklist)。

- [ ] `[M]` 数据库、用户文件、配置、Secret 和部署元数据的所有权与宿主路径明确。
- [ ] `[E]` 备份将所有相关持久面保持在同一恢复点，恢复验证业务数据、附件、身份关联和应用 Secret/token。
- [ ] `[E]` 验证全新安装、最低受支持版本和上一版本升级、重复 apply、重启、中断重试与必要回滚。
- [ ] `[M/E]` 数据迁移参数不被描述成自动完成；升级脚本有适用版本、幂等依据和删除条件。

## 9. IAM 与管理员恢复（条件项）

- [ ] `[A/M]` IAM Consumer 只读取自己的 `ANAS_IAM_BINDING__<APP>__*`，只发布自己的 `ANAS_IAM_CLIENT__<APP>__*`，不按 Provider 名称分支。
- [ ] `[M/E]` client type、redirect URI、scope、claim、Group 门禁、JIT/同步方向和本地认证状态与固定上游版本一致。
- [ ] `[M]` 分别记录 Module→IAM、IAM→Module 和管理员无浏览器撤销。`post_logout_redirect_uri` 没有被写成通知 endpoint。
- [ ] `[A/M]` 固定版本无标准 logout receiver 时省略 `OIDC_LOGOUT_*`/SAML SLS，没有猜测普通 `/logout` 路径。
- [ ] `[E]` 真实浏览器保留原应用 Cookie，验证应用会话、IAM 中央会话、Group 允许/拒绝、IAM-down 降级和声明支持的登出方向。
- [ ] `[M/E]` 有管理界面但无可绕过 IAM 的本地入口时，Manifest 不伪造本地账号，README 说明 IAM 故障的真实恢复路径。

## 10. 关系数据库 Consumer（条件项）

- [ ] `[A/M]` 同时支持 PostgreSQL/MariaDB 时使用 `relational_database` Contract，对外 interface 是 `postgres`/`mariadb`/`auto`，`mysql` 只在 Module 内部映射。
- [ ] `[A/M]` Hook 只消费 `<PREFIX>_DB_*` 和 `<PREFIX>_NETWORK_DB`，Compose 只连接选中 Provider network，不读取 Provider root/admin 凭据。
- [ ] `[A/E]` 单元测试覆盖两个 interface、专属凭据和 network 映射；发布前两个引擎都验证空库安装、重启和重复 apply。

## 11. 时区与本地化

- [ ] `[A]` `localization.yml` 的 Module 名、版本、revision、status 值和 BCP 47 标签通过生成器检查。
- [ ] `[M]` 支持清单来自固定版本源码、版本化官方文档或精确镜像，不使用滚动宣传页代替版本证据。
- [ ] `[A/E]` 不支持语言的 warning/fallback、脚本变体边界、一个非英文语言、非 UTC 时区和 DST 场景已验证。

## 12. 文档与全局清单

- [ ] `[A]` 存在 `README.md`、`README.en.md`、`docs/technical.md`、`docs/technical.en.md` 和 `localization.yml`，且中英文支持状态一致。
- [ ] `[A/M]` README/技术文档覆盖全部参数、依赖、身份、本地账号、数据库、Secret、存储、备份、Hook、测试和当前限制。
- [ ] `[A]` Module 目录、本地化矩阵、Contract consumer 清单、配置参数统计、IAM/环境变量参考和内置 Module 架构清单已同步。
- [ ] `[A]` 生成标记外的人工语义没有被生成器覆盖，站内链接和中英文导航可构建。

## 13. 建议的静态验证

在仓库根目录执行：

```bash
go test ./internal/modulepackage ./internal/runner
go test ./modules/<name>/hook
go run ./cmd/gen-module-docs --check
go run ./cmd/gen-contract-docs --check
docker compose -f modules/<name>/docker-compose.yml config --no-interpolate --no-path-resolution -q
go test ./...
npm run docs:build
git diff --check
```

如 Module 含独立 helper/entrypoint 包，再运行对应单元测试和 Linux
amd64/arm64 编译。命令通过只能证明机械契约，不能替代真实登录、恢复、
升级或数据安全证据。Docker/E2E 必须使用隔离的非生产 daemon、workspace、
Compose project 前缀和端口范围。

## 14. 评审结论模板

| 类别 | 结果 | 证据/未完成项 |
| --- | --- | --- |
| Manifest 与打包 | 通过 / 不通过 / N/A |  |
| 所有权与依赖 | 通过 / 不通过 / N/A |  |
| 配置与 Hook | 通过 / 不通过 / N/A |  |
| Secret 与运行安全 | 通过 / 不通过 / N/A |  |
| 数据、升级与恢复 | 通过 / 不通过 / N/A |  |
| IAM/数据库/本地化 | 通过 / 不通过 / N/A |  |
| 文档与自动门禁 | 通过 / 不通过 / N/A |  |
| 真实环境发布门禁 | 通过 / 不通过 / N/A |  |

最终结论只使用：**通过**、**有条件通过（列出条件）**或**不通过（列出阻断项）**。
