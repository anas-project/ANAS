# Module 升级检查表

> 状态：**当前基线**。更新：2026-08-21。

本页是 [Module 上游升级 SOP](/developer/module-upgrade-sop) 的可签署执行清单。
上游应用、基础镜像、附带 Web UI/helper 或其他运行资产变化时必须使用；
纯文档修正可记录 `N/A`。若与 SOP 冲突，以 SOP 为准。

## 评审元数据

- [ ] Module、来源版本/revision、目标版本/revision、变更类型（上游/打包/附带组件）已记录。
- [ ] 每个运行镜像的来源/目标 tag 或 digest、目标平台、评审人和证据日期已记录。
- [ ] 最低受支持来源版本、上一发布版本和可回滚目标已确定。

## 1. 上游变更调查

- [ ] 已阅读源到目标之间所有 release notes、迁移指南、弃用和安全公告。
- [ ] 已检查数据格式、数据库 schema、配置键/默认值、端口、权限、API 和认证协议变化。
- [ ] 已检查镜像 user/entrypoint/healthcheck/VOLUME、基础发行版、附带 UI/helper 和平台支持变化。
- [ ] 已检查 locale、language、timezone、TZ、日期格式和翻译清单。
- [ ] 需要经过的中间版本和不可跳跃路径已记录；不能确认时已阻断升级。

## 2. Manifest、打包与契约

- [ ] 上游版本变化时 `version` 更新且 `revision` 重置为 `1`；只改 ANAS 打包时由发布流程计算 revision。
- [ ] `module.yml`、Compose image tag、`localization.yml`、生成文档和附带组件版本一致。
- [ ] ABI、Module/Capability/Contract 依赖、Contract 版本范围、Resource spec 和 Provider interface 已复核。
- [ ] `upgrade.from` 接受已验证的来源范围并拒绝其他版本。
- [ ] 目标版本会写出旧制品无法读取的磁盘数据时，断代点已加入 `upgrade.data_breaking`；无断代时显式声明 `[]`。
- [ ] `.github/modules.json`、构建上下文、发布平台和 Module/Contract 全局清单已同步。

## 3. 配置、Hook 与运行资产

- [ ] 新增/删除/重命名配置键、类型、默认值、constraints、敏感性和 change effect 已更新并测试。
- [ ] 旧配置到新配置的转换是显式的；不支持的旧值会在启动前返回结构化错误，不会被 Core 静默改写。
- [ ] Hook phase allowlist、ABI、派生值、Secret 所有权和上游环境映射已复核。
- [ ] Dockerfile、Compose、入口脚本、模板、健康检查、network、volume 和运行用户已与目标镜像核对。
- [ ] 新增的自有转换/升级脚本有实际状态前置检查、幂等依据、中断重试、适用版本和删除条件。

## 4. 数据迁移、快照与回滚

- [ ] 数据库 schema、文件格式、索引、缓存、队列和外部 Resource 的迁移所有者已明确。
- [ ] 升级前快照/备份覆盖数据库、用户文件、Secret Store、Resource state 和 deployment 元数据。
- [ ] 从最低受支持版本与上一版本升级已使用真实持久数据验证。
- [ ] 重复 apply、重启、迁移重复执行、迁移前/中/后中断的重试已验证。
- [ ] 无数据断代时已验证制品回滚；有断代时已验证从快照恢复，没有声明不可能的原地回滚。

## 5. 密码/密钥与轮换兼容

- [ ] 升级前后 ANAS-managed 密码、shared/client secret、签名/加密密钥和 Resource 凭据库存已比较。
- [ ] 升级不会因键名、默认值或 provenance 变化而静默重生成稳定 Secret。
- [ ] 需要转换凭据格式/算法时，使用 candidate→应用更新→verify→Store commit 事务，失败恢复旧值和旧应用状态。
- [ ] 升级后重新验证单目标、`anas credential rotate --module MODULE` 和 deployment `--all` 轮换范围；Resource、本地管理员等未纳入范围明确标为 manual/unsupported。
- [ ] IAM client secret、数据库凭据或共享 Secret 变化时，所有 Provider/consumer 在同一事务中对账，没有只更新一端。

## 6. IAM、API 与管理面（条件项）

- [ ] OIDC/SAML/LDAP/Kerberos endpoint、redirect/SLS/logout URI、scope、claim 和 Group 映射与目标版本一致。
- [ ] 本地账号、break-glass 入口、apply/rotate/rollback handler 在升级后仍可用。
- [ ] 应用发起、IAM 发起和管理员无浏览器撤销的支持结论已按目标版本重新验证，旧 endpoint 未残留。
- [ ] 对外 API/webhook/CalDAV 等契约、token 权限和轮换/撤销行为已复核。

## 7. 本地化与文档

- [ ] `localization.yml` 版本/revision/reviewed_at、语言清单、映射、fallback 和固定版本证据已更新。
- [ ] 一个非英文语言、一个不支持语言、非 UTC 时区、DST 和用户偏好优先级已验证。
- [ ] 中英文 README/技术文档的版本、参数、迁移、恢复、轮换、限制和测试证据一致。
- [ ] Module 目录、Contract consumer、本地化矩阵、配置统计、全局参考和升级评审记录已同步。

## 8. 静态与真实环境验证

- [ ] 已运行并记录：

```bash
go run ./cmd/gen-module-docs
go run ./cmd/gen-module-docs --check
go run ./cmd/gen-contract-docs --check
go test ./...
npm run docs:build
git diff --check
```

- [ ] Compose 静态解析、Module/helper 单元测试和所有声明平台的镜像构建已通过。
- [ ] 全新安装、最低版本升级、上一版本升级、重复 apply、重启、中断重试和回滚/恢复有真实数据证据。
- [ ] 健康检查、主要业务流、依赖 Module、IAM/凭据轮换和备份恢复在隔离的非生产 Docker 环境通过。

## 9. 签署结论

| 项目 | 结果 | 证据/阻断项 |
| --- | --- | --- |
| 上游差异审查 | 通过 / 不通过 |  |
| Manifest/契约/打包 | 通过 / 不通过 |  |
| 配置与数据迁移 | 通过 / 不通过 |  |
| Secret/凭据轮换 | 通过 / 不通过 |  |
| 升级、中断重试与回滚 | 通过 / 不通过 |  |
| 本地化与文档 | 通过 / 不通过 |  |
| 真实环境发布门禁 | 通过 / 不通过 |  |

只有所有适用项通过，才能发布升级。无法验证的项必须阻断发布或保持 Module
`developing`，不得用“上游应该支持”代替证据。
