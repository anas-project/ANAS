# ANAS 凭据库存与全量轮换范围审计（2026-08-19）

本文基于当前工作树（基线提交 `edcc8b6`）对 ANAS Core、Module manifest、Module Hook、
配置导入、Secret Store、本地管理员与关系数据库 Contract 做静态审计。全文只记录逻辑键、
存储位置和生命周期，不记录、复制或散列任何真实凭据值。

## 结论

ANAS 已经把多类敏感值集中到受管边界，但当前不能安全地把 `.anas/secrets.yml` 中的所有值
直接替换：只有四个本地管理员账号具备真实的应用侧 apply、验证与回滚 handler。其他值有的
只有“必须走轮换流程”的策略声明，有的只是 Hook 首次生成并永久复用，还有的由外部云厂商
签发，ANAS 根本无权生成下一把凭据。

因此“轮换所有”必须先区分三件事：

1. **ANAS 保存**：值是否进入受管配置或 Secret Store；
2. **ANAS 签发**：ANAS 是否能够生成合法候选值；
3. **ANAS 轮换**：ANAS 是否能够更新全部消费者、验证新值并在失败时恢复旧值。

只有三项同时成立的活动凭据才能进入自动轮换事务。仅改存储文件不构成轮换。

## 当前存储面

| 存储面 | 内容 | 权限/传播 | 当前边界 |
| --- | --- | --- | --- |
| `<workspace>/config.yml` | 普通部署 Secret、外部 API/DNS 凭据、普通敏感参数 | 受管文件 `0600`；可能被投影到所属 Module 的环境 | 值由用户或外部系统提供，不能据此推断 ANAS 可轮换 |
| `<workspace>/.anas/secrets.yml` | `generated`、`lifecycle_managed`、`local_admin` | Secret Store v2，原子写入，`0600` | 只有 owner/kind/provenance，没有通用 rotator、消费者、代次或轮换时间 |
| `<workspace>/.anas/local-admins.yml` | Module、账号 ID、用途、用户名、Secret 逻辑键 | `0600`，不含密码 | 是账号索引，不是 Secret Store |
| `<workspace>/.anas/runtime-secrets/` | bootstrap-only 明文运行时投影 | 目录 `0700`、文件 `0600`，可由 Store 重建 | 非权威副本，不能单独轮换 |
| deployment `.env` 与渲染文件 | 活动制品所需的敏感投影或派生值 | 按 Module 作用域生成 | 更新 Store 后旧制品不会自动采用新值 |
| 应用内部状态 | 密码 hash、数据库角色密码、AD 账号、OIDC client、签名/加密状态 | 由各应用管理 | 真正的轮换必须先更新并验证这里 |
| Module 数据目录 | ACME/证书私钥、上游应用自行生成的 token/密钥等 | 随应用数据备份 | 当前没有统一结构化库存，不能由 Secret Store 清单代表 |

`anas config secret list` 当前只列 `.anas/secrets.yml` 的逻辑键与 kind，并不是全量凭据
库存；它不会列出 `config.yml` 中的普通 Secret、Module 数据目录中的密钥或应用内部状态。
`config secret get` 和 `admin local credential` 是显式明文读取接口，不能用于例行盘点。

## 当前可执行轮换

以下四个 `management.local_accounts` 账号声明了实际 rotate handler：

| 账号 | Secret 逻辑键 | 应用侧动作 | 当前状态 |
| --- | --- | --- | --- |
| `ddns_go.primary` | `ANAS_LOCAL_ADMIN__DDNS_GO__PRIMARY__PASSWORD` | 更新持久 bcrypt 配置并验证 | 可逐账号事务轮换 |
| `traefik.primary` | `ANAS_LOCAL_ADMIN__TRAEFIK__PRIMARY__PASSWORD` | 更新 Dashboard BasicAuth 并验证 | 可逐账号事务轮换 |
| `nextcloud.break_glass` | `ANAS_LOCAL_ADMIN__NEXTCLOUD__BREAK_GLASS__PASSWORD` | 通过 `occ` 更新、验证并可恢复 | 可逐账号事务轮换 |
| `authentik.break_glass` | `ANAS_LOCAL_ADMIN__AUTHENTIK__BREAK_GLASS__PASSWORD` | 更新固定 `akadmin`、验证并可恢复 | 可逐账号事务轮换 |

现有命令是 `anas admin local rotate MODULE [ACCOUNT]`。每个账号内部遵循“候选值 → Module
handler → 验证 → 提交 Secret Store；失败保留或恢复旧值”，但还没有统一库存、批量预检、
跨账号补偿日志或崩溃恢复事务。

## 已声明但尚未实现的凭据轮换

以下七个参数标记为 `credential_rotate`，所以普通 `config set` 和不同值的重新导入会被拒绝；
但 manifest 的 `apply` 目前只是说明性名称，不是 Runner 可执行 handler：

| 参数 | Secret 逻辑键 | 缺失能力 |
| --- | --- | --- |
| `mariadb.root_password` | `MARIADB_ROOT_PASSWORD` | 数据库 root 更新、候选认证、消费者/容器切换与回滚 |
| `postgres.password` | `POSTGRES_PASSWORD` | superuser 更新、候选认证、消费者/容器切换与回滚 |
| `samba_dc.admin_password` | `SAMBA_DC_ADMIN_PASSWORD` | AD 账号更新、全部消费者切换、验证与回滚 |
| `samba_dc.administrator_password` | `SAMBA_DC_ADMINISTRATOR_PASSWORD` | 域 Administrator 更新、验证与恢复 |
| `samba_dc.ldap_bind_password` | `SAMBA_DC_LDAP_BIND_PASSWORD` | bind 账号与 LDAP 消费者原子切换 |
| `samba_dc.password_bind_password` | `SAMBA_DC_PASSWORD_BIND_PASSWORD` | password-bind 账号与认证消费者原子切换 |
| `samba_dc.anchor_bind_password` | `SAMBA_DC_ANCHOR_BIND_PASSWORD` | anchor 账号与 Worker 原子切换 |

这些项不能通过删除 Store 记录、重新运行 Hook 或重建容器来安全轮换。数据库和 Samba 都会在
持久数据中保留旧密码，环境变量通常只在首次初始化时生效。

## 其他 ANAS 生成或保存的敏感材料

下表按用途归类当前代码能够生成的逻辑键。是否出现取决于启用的 Module、IAM 协议和资源
绑定；逻辑键存在并不表示当前已有安全轮换器。

| 类别 | 逻辑键或模式 | 当前轮换结论 |
| --- | --- | --- |
| 关系数据库资源账号 | `RESOURCE_<CONSUMER>_<RESOURCE_ID>_PASSWORD` | Contract 定义可选 `rotate_credential`，Postgres/MariaDB Provider 尚未实现该 operation |
| Module 私有管理密码 | `COLLABORA_ADMIN_PASSWORD`、`LAM_ADMIN_PASSWORD` | 可首次生成；没有可验证、可回滚 rotate handler |
| Samba 服务账号 | 上节五个 `SAMBA_DC_*_PASSWORD` | 首次生成/导入；只有策略守卫，没有执行器 |
| OIDC client secret | `NEXTCLOUD_OIDC_CLIENT_SECRET`、`MESHCENTRAL_OIDC_CLIENT_SECRET`、`OAUTH2_PROXY_CLIENT_SECRET`、`ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET` | 必须同时更新 IAM provider 与 client，当前没有双端事务 |
| 应用/会话 secret | `AUTHENTIK_SECRET_KEY`、`OAUTH2_PROXY_COOKIE_SECRET` | 更换可能使 session 失效；未声明影响、验证和回滚 |
| TURN/Talk/内部服务 secret | `TURN_SECRET`、`NEXTCLOUD_TALK_INTERNAL_SECRET`、`TALK_SIGNALING_SECRET`、`NEXTCLOUD_IMAGINARY_SECRET`、`NETBIRD_RELAY_AUTH_SECRET` | 必须协调全部生产者/消费者并重启验证，当前没有事务 |
| 签名私钥与证书 | `AUTHENTIK_SIGNING_KEY`/`AUTHENTIK_SIGNING_CERT`、`NEXTCLOUD_SAML_SP_PRIVATE_KEY`/`NEXTCLOUD_SAML_SP_CERT`、`<CLIENT>_SERVICE_PRIVATE_KEY`/`<CLIENT>_SERVICE_PUBLIC_KEY`/`<CLIENT>_OIDC_SERVICE_KEY_ID` | 需要双钥匙重叠、元数据传播和旧钥匙退役窗口，不能瞬时替换 |
| 数据加密主钥 | `NETBIRD_DATASTORE_ENC_KEY` | 属于数据迁移/重加密，不是普通 Secret rotate |
| 本地认证派生值 | `DDNS_GO_WEB_PASSWORD_HASH` | 是本地管理员密码的派生投影，应随账号 handler 更新，不应单独生成 |

此外，`config.yml` 的 `secrets:` 可保存 DNS/API access key、secret key、token 等外部凭据。
这些值由云厂商或其他外部系统签发。ANAS 可以发现、投影和提示到期，但只有在接入对应厂商
的创建、并行生效、验证、撤销 API 后才能自动轮换；当前不能单方面生成替代值。

## 统一库存需要的模型

建议新增独立的逻辑凭据声明，而不是按 `PASSWORD`、`TOKEN`、`KEY` 名称猜测：

```yaml
credentials:
  - id: nextcloud.break_glass
    secret_keys: [ANAS_LOCAL_ADMIN__NEXTCLOUD__BREAK_GLASS__PASSWORD]
    authority: anas
    purpose: local_admin
    consumers: [nextcloud]
    disruption: session_only
    lifecycle:
      generate: random_password
      apply: apply-nextcloud-break-glass
      verify: verify-nextcloud-break-glass
      rollback: rollback-nextcloud-break-glass
```

统一只读命令应为：

```text
anas credential list [-w WORKSPACE] [--json]
```

每项至少返回 `id/key/owner/kind/purpose/source/active/consumers/authority/rotation_status/handler/reason`，
且任何模式都不返回值、值摘要或可用于离线猜测的派生信息。库存还应标记：

- `rotatable`：具备生成、apply、verify、rollback；
- `manual`：外部签发，需要人工或 provider 集成；
- `unsupported`：ANAS 保存/生成但没有完整生命周期；
- `orphaned`：Store 中存在但活动 manifest 已无声明或消费者。

## 全量轮换的安全首版

推荐首版命令：

```text
anas credential rotate --all -w WORKSPACE --dry-run
anas credential rotate --all -w WORKSPACE -y
```

安全语义：

1. 获取 workspace 排他锁并读取与 `list` 相同的库存；
2. `--dry-run` 不生成随机数、不写文件、不调用 Hook/Docker，只返回顺序、影响与 blockers；
3. 真正执行前完成全量 preflight；活动且由 ANAS 掌管的任何凭据缺少完整 handler 时，零副作用
   返回 `credential_rotation_blocked`；
4. 为所有目标生成候选值，逐项 apply 与 verify；
5. 失败时按逆序 rollback 并验证旧值；
6. 全部成功后只原子提交一次 Secret Store；
7. 事务日志和审计只记录逻辑 ID、阶段、代次、时间与结果，不记录值或 hash。

跨 Docker、数据库、AD 和外部 API 的操作不是 ACID 事务，合同应称为 all-or-compensate saga。
回滚失败时必须保留不含明文的恢复日志、阻断后续写操作并返回
`credential_rotation_recovery_required`，不能把部分成功报告成整体成功。

## 需要确认的实施范围

存在两个明显不同的交付范围：

1. **安全首版（推荐）**：先交付统一脱敏库存、dry-run、全量预检和事务框架；接入已经具备
   真实 handler 的四个本地管理员账号。其他活动 ANAS-owned 凭据作为 blocker，绝不静默跳过，
   也不提供 `--allow-partial`。
2. **一次性全覆盖**：同时补齐 Samba、数据库 root、数据库 resource、OIDC/SAML 双端切换、
   签名双钥匙窗口、内部共享 secret 和数据加密主钥迁移。该范围需要逐类确定停机预算、双凭据
   共存窗口、真实环境验证与恢复策略；外部 DNS/API token 仍需逐厂商 API 或人工流程。

两者的风险和工作量不在同一量级。在确认范围之前，不应把“重写所有 Store 值”实现成一个
看似完成、实际会让应用与 Store 失步的命令。
