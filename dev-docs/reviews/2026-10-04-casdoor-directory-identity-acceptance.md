---
doc_type: review
status: current
created: 2026-10-04
updated: 2026-10-04
---

# Casdoor r10 目录身份与撤权实机验收

Casdoor 保持 `developing`。本轮完成已授权的统一目录主体与 OIDC 目录撤权实施，并在隔离服务器
验证真实 Consumer；这不表示 SAML 会话终止、完整镜像发布或全部 IAM Provider 的通用要求完成。
产品没有发布，不实施旧账号迁移。

## 输入与隔离

- SSH 目标为用户指定的 `whl@finance.hlong.wang`；工作区 `/home/whl/anas-casdoor-test-20261003`。
- 独立网络命名空间、`unix:///run/anas-casdoor-test-20261003.sock`、Docker data-root 和
  `anas_cd261003_` 容器前缀。所有测试脚本通过隔离守卫；没有切换业务 deployment。
- Casdoor 上游提交 `1ee6deb8d8f1c64ffb54847fc0e4780b91c34c6e`，归档 SHA-256
  `365d61c7e8cae30a6b1a135204c74145c9ce6c692068d3fc044404703c0f9460`，应用生产补丁 0001—0006。
- 运行中的 Linux server SHA-256：`20700e9a1315dd08f251854000e8a3d647c45681b0b0debb0299d55a9a0fd181`；
  helper：`8eefa1043e92c06e243a46a6ec90010c9633a856437c71e6e2bf460157a47ebe`。
- Casdoor 测试镜像 ID：`sha256:d1a6e82d52881342bd0437c1412c270ce2c031664f44d577a8c70e86bd9ad540`。
  它以缓存 r9 的 UI/系统为载体，叠加固定源码编译的 server/helper；不能替代完整 Dockerfile 双架构构建。
- Nextcloud `34.0.2-r11` 测试镜像以缓存 r10 叠加本轮启动脚本与插件补丁，ID
  `sha256:79a9338e68823d0bb582dc1b1d333dc0da42919b46f3cfd1534bee68d0582c7f`。
  临时 Compose override 仅重建隔离 Web/cron，没有修改冻结 deployment 的 `.env` 或 Compose。

脱敏日志、运行摘要、清理状态与健康回读保存于
[evidence.json](../../test-env/reports/2026-10-07-review-consolidation/assets/2026-10-04-casdoor-directory-identity/evidence.json)。最终 watcher 为
`ready=true`、`pending_logouts=0`、`last_error=""`，测试 Consumer 容器已清理，Casdoor/Nextcloud
候选均 healthy。只读回查的业务基线 24 个容器仍存在并运行，但部分 StartedAt 晚于 10 月 3 日的
基线文件时间，不能据此断言整个期间业务容器完全未重启；本任务的变更命令均指向隔离 socket 与前缀。

## 验收矩阵

| 项目 | 结果与准确范围 | 入口 / 服务器日志 |
| --- | --- | --- |
| 固定源码身份/撤权 | 通过；六项历史配置边界、八项主体检查、定向撤权及既有回归。未兑换授权码不能在改名后兑换，不通知未建立的 RP 会话 | `test-casdoor-identity-source.sh`；本地 `anas-casdoor-identity-final-source-20261003.log` |
| OIDC 两 client 隔离 | 通过；同一用户失去 A 准入后 A 的保存 Cookie 和真实 refresh 被拒，B 的 Cookie/refresh 及其他用户保持有效 | `server-casdoor-oidc-client-scope-e2e.sh`；`two-client-scope-e2e.log` |
| SAML 2.0 签名协议 | 通过；独立 SP 验签、persistent NameID 等于 anchor，直接/全局/管理员组、拒绝、停用、改名与删除 | `server-casdoor-saml-protocol-e2e.sh`；`saml-protocol-e2e.log` |
| Nextcloud 原账号与文件 | 通过；真实 OIDC 回调建立应用 Cookie，读取 LDAP 映射，创建 DAV 文件，改名后同 uid 与 anchor、原文件内容不变 | `server-casdoor-nextcloud-identity-e2e.py`；`nextcloud-r11-identity-e2e.log` |
| Nextcloud 真实会话撤权 | 通过；改名与组撤权使保存的原 Cookie 无法访问 OCS，未受影响用户仍认证 | 同上；未用 fixture 数据库代替应用状态 |
| Nextcloud r11 启动补丁 | 通过；原版控制器启动时自动修正，重复执行幂等，版本漂移与未知源码被拒绝 | `nextcloud-r11-startup.log` |
| 最终 r10 完整 OIDC 故障矩阵 | 通过，退出 0；普通/管理员登出后真实刷新拒绝，直接/递归组撤权、停用、删除、改名与同名新 anchor 隔离；503 接收失败持久待办、watcher 重启、重新授权与旧通知重试不撤新会话 | `server-casdoor-oidc-logout-e2e.sh`，`CASDOOR_DIRECTORY_LOGOUT_E2E=1`；`directory-logout-r10-final-e2e.log` |

SAML 验收使用临时注册的签名协议 SP，结束后恢复原 OIDC Application。
`nextcloud_saml_acceptance=false` 是明确边界：本轮没有建立 Nextcloud `user_saml` 真实应用会话，
也没有实现或验收 SAML SLO。其他 Provider、LDAP 密码登录及设备 token 不包含在这些通过结论中。

最终矩阵第一次在持久队列断言处失败：影子 profile 已更新时，撤权确认尚未落盘，原脚本只读取
一次。按实际调用顺序修正为最多等待 120 秒，并精确匹配该 grant 的 sid；健康诊断也等待异步发布。
没有修改 server/helper 产品代码或放宽断言。重跑完整矩阵退出 0，且确认待办跨 watcher 重启后
完成、后来授权的 Cookie 与 refresh 保持有效。

## 实机发现与修正

原始 Nextcloud `user_oidc 8.10.1` 回调按 `preferred_username` 查找用户。LDAP 的
`owncloud_name` 在改名后保持旧值，因此原账号映射正确但 OIDC 回调返回 HTTP 400。本轮在
既有控制器中增加显式 anchor 查询，用 `UserMapping::getNameByUUID` 找回原 uid；缺少 anchor
或映射拒绝登录，没有新增映射表或按标签回退。

原始 back-channel 对已不存在的 sid 返回 HTTP 400，导致 watcher 永久保留目标。本轮在原有
签名/audience/事件/nonce 校验后增加 issuer 核验，合法通知的目标已不存在时返回 200；无效 Token
仍拒绝。修正通过严格固定版本/源码摘要守卫、PHP 语法检查及原子替换接入启动任务。

原始控制器 SHA-256 为 `8c1d99d3e7ae766f1b67ff641b881b09cfa391b37eb82a5783d1a988c18606fc`，
产物为 `7c7e8e8a20869e1874b3b246c247416dc93512c26a87d74bc6dc48fe93f91893`。镜像新增系统
`patch` 工具；没有新增服务、PHP 依赖或身份存储。

**已观察到的发布缺口**：上游插件签名清单仍对应原控制器，`occ integrity:check-app user_oidc`
退出 1，仅报告该文件的 `INVALID_HASH`。没有关闭完整性检查，Nextcloud r11 也保持
`developing`；发布前需提供包含修正的可信插件制品。其他 Provider 的 r11 回归尚未运行。

## 验证与未完成项

Casdoor/Nextcloud Hook 的 Go 测试与 vet、相关 Shell/Python 语法检查通过。当前共享工作树的
原生 Module 文档生成与 `--check` 已通过；此前阻断生成器编译的无关字段问题不再阻断本轮命令。
需求覆盖/测试用例、需求状态、计划状态、文档状态与文档站构建通过；构建只有非阻断的 bundle
大小提示。不是全仓库测试或发布流水线的通过记录。
升级 catalog 已同步 Casdoor r10 与 Nextcloud r11 当前目标并通过静态校验，不代表升级/回退实机运行。

首发无需旧账号迁移；本轮没有运行历史版本升级/回退、备份恢复、受管凭据轮换或完整双架构发布
流水线。历史 r8 的通过记录不能升级成当前 r10 的通过记录。直接 LDAP Consumer 设备凭据终止、
Nextcloud SAML 会话、其他 Provider 的 anchor 签发与完整目录事件覆盖仍按各自计划推进。

## 后续范围调整

2026-10-04 用户确认：OIDC 为主要支持协议；SAML 会话退出/SLO 列为待实现，不作为当前 OIDC
发布阻塞项。已通过的签名 SAML 协议结果保留，应用本地退出的限制继续明确，不声明完整 SAML
会话撤权支持。待实现检查项见目录事件订阅计划，未来能力的安全验收要求不变。

下一步：处理 user_oidc 修正的正式制品与完整性告警，完成其他 Provider 回归及完整镜像/生命周期验收。
