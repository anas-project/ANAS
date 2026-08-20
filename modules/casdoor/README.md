# Casdoor

提供 OIDC 与 SAML 的 IAM Provider，并通过 LDAPS 从 Samba AD 导入目录用户。

> [!WARNING]
> 当前生命周期为 `developing`，只用于开发与验证。目录永久锚点、按应用 Group 授权、账号停用传播和真实客户端会话撤销尚未满足生产验收。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `casdoor` |
| 版本 / revision | `3.143.0-r1` |
| 状态 | `developing` |
| 类别 | `identity` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `samba_dc` | Module | — |
| `relational_database` | Contract | `>=1.0.0 <2.0.0`; `postgres` |
| `iam` | 提供 Capability | `oidc, saml` |

## 最简配置

```yaml
identity:
  iam:
    provider: casdoor
modules:
  nextcloud: {}
```

只有存在 IAM Consumer 时 Runner 才自动加入所选 Provider；也可在 `modules` 中显式加入 `casdoor` 做独立验证。

## 身份与协议行为

Samba AD 仍是人员和目录账号的事实来源。Casdoor 使用受限只读 Bind 经 LDAPS 导入用户并远程校验密码。独立 `casdoor_dirwatch` 订阅 Samba 的持久目录事件日志，经过防抖后立即触发一次 LDAP 导入；默认每 5 分钟的周期同步继续保留为兜底。本实现不启用 Casdoor 的 LDAP/AD 密码写回，也不把 Casdoor 本地用户记录当作目录权威。

OIDC 和 SAML Consumer 会按通用 `ANAS_IAM_CLIENT__<APP>__*` 注册。Casdoor 当前不发布 SAML SLO；Consumer 只能执行应用本地登出。OIDC back-channel URI 会在 Consumer 明确声明时登记，但真实会话撤销仍待 E2E 验证。

通用 `ALLOW_GROUPS` 尚未找到可证明等价的 Casdoor Application 策略，因此本模块不会伪装成已执行按应用 Group 门禁。未知 SAML 属性只能映射到 Casdoor 本地用户 ID，而非已验证的 `anasIdentityAnchor`；需要永久身份锚点的部署不得将此状态视为生产支持。

## 管理员登录与 IAM 故障恢复

`break_glass` 本地恢复账号遵循 ANAS 的不可配置默认模板 `admin_{module}`，实际用户名为 `admin_casdoor`；密码独立生成并可事务轮换。Casdoor 没有要求保留的上游内置用户名，因此不声明 `fixed_username`。

| 入口 ID | 地址来源 | 主要认证 |
| --- | --- | --- |
| `web` | `CASDOOR_DOMAIN_FULL` | `iam` |
| `local_recovery` | `CASDOOR_LOCAL_RECOVERY_URL` | `local` |

| ID | 用途 | 用户名 | 容器格式 | 可轮换 |
| --- | --- | --- | --- | --- |
| `break_glass` | `break_glass` | `admin_casdoor` | `plaintext_on_bootstrap` | 是 |

```bash
anas admin local credential casdoor break_glass -w /srv/anas
anas admin local rotate casdoor break_glass -w /srv/anas
```

## 数据库支持

| 项目 | 值 |
| --- | --- |
| 角色 | Consumer |
| 支持接口 | `postgres` |
| 默认接口 | `postgres` |
| Resource | `primary_database` |
| 凭据策略 | `generated` |
| 删除策略 | `retain` |

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `casdoor.db_name` | string | — | `casdoor` | `static` | `CASDOOR_DB_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 应用数据库名 |
| `casdoor.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `CASDOOR_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-casdoor-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `casdoor.domain_prefix` | string | — | `auth` | `static` | `CASDOOR_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `reconcile` | 服务域名前缀及所有 IAM 端点 |
| `casdoor.ldap_auto_sync_minutes` | int | `>= 1` | `5` | `static` | `CASDOOR_LDAP_AUTO_SYNC_MINUTES` | 否 | 否 | 否 | 是 | `container_recreate` | LDAP 自动同步周期（分钟） |

### 查询和修改

```bash
anas config list casdoor -w /srv/anas
anas config explain casdoor.ldap_auto_sync_minutes
anas config plan -w /srv/anas
```

## 存储、备份与验证

Casdoor 持久状态位于绑定的 PostgreSQL Resource；目录订阅游标位于 `${DATA_PATH}/casdoor/dirwatch`。备份必须让数据库、订阅游标、workspace Secret Store 和本地管理员库存保持同一恢复点。

```bash
anas plan -c /srv/anas/config.yml
anas status -w /srv/anas
```

## 技术文档

实现、安全边界与测试入口见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`3.143.0-r1`（reviewed 2026-08-21）
- Timezone / 时区：`container` — Casdoor receives TZ through the module environment; no separate application timezone is forced.
- Language scope / 语言范围：Casdoor Web UI default
- Selection / 选择方式：`application`
- ANAS global defaults / 全局默认：`default_language=applied`; `default_locale=not_consumed`
- Upstream format / 上游格式：Casdoor language code
- Fallback / 回退：ANAS maps zh-prefixed defaults to zh and all other values to en; users may change the UI language in Casdoor.
- Supported languages / 支持语言（2）：`en`, `zh`
- Notes / 说明：This inventory records the two ANAS-selected defaults, not every translation shipped by upstream.

Evidence / 证据：

- [v3.143.0 — web/src/locales English and Chinese resources](https://github.com/casdoor/casdoor/tree/v3.143.0/web/src/locales)
<!-- generated:localization:end -->
