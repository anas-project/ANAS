# PostgreSQL

提供 `relational_database/postgres`，并可选启用 Adminer。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `postgres` |
| 版本 / revision | `18.4.0-r4` |
| 状态 | `developing` |
| 类别 | `database` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `relational_database` | 提供 Contract | `1.0.0` / `postgres` |

## 最简配置

```yaml
modules:
  postgres: {}
```

## 身份、用户与 Group

数据库服务不使用目录或 IAM。每个 Consumer 获得独立数据库、角色和生成凭据。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | 不支持/不适用 |
| Group | 未声明 |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 管理员登录与 IAM 故障恢复

超级用户密码是 Provider 凭据，不是本地管理员。Adminer 启用后使用数据库账号登录。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 提供 `relational_database/postgres` Contract，版本 `1.0.0`。

可选 `spec.postgres.extensions` 声明扩展名称。本版固定 PostgreSQL `18.4` / Alpine、pgvector（SQL 名 `vector`）
`0.8.2`、`cube 1.5`、`earthdistance 1.2`；`earthdistance` 的 `cube` 依赖由 Provider 安装。
不支持 VectorChord，不提供运行时版本选择或联网安装。Immich 使用 `[vector, earthdistance]` 和
`DB_VECTOR_EXTENSION=pgvector`。服务与 provision 使用同一 ANAS 镜像。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `postgres.adminer_enabled` | bool | — | `false` | `static` | `POSTGRES_ADMINER_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否启用 Adminer |
| `postgres.forward_auth_interface` | enum (`auto`, `http`) | — | `auto` | `static` | `POSTGRES_FORWARD_AUTH_INTERFACE` | 否 | 否 | 否 | 是 | `container_recreate` | Adminer 所用认证网关的接口，`auto` 由 Runner 选择 |
| `postgres.password` | string | — | — | `generated` | `POSTGRES_PASSWORD` | 否 | 是 | 是 | 否：`rotate-postgres-password` | `credential_rotate` | 管理员或服务密码 |
| `postgres.username` | string | — | `postgres` | `static` | `POSTGRES_USERNAME` | 否 | 否 | 否 | 否：`migrate-postgres-owner` | `data_migrate` | 数据库管理员用户名 |

### 查询和修改

```bash
anas config list postgres -w /srv/anas
anas config explain postgres.adminer_enabled
anas config set postgres.adminer_enabled false -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

### 敏感参数与生成 Secret

- `postgres.password` → `POSTGRES_PASSWORD`

```bash
anas config secret list -w /srv/anas
anas config secret get POSTGRES_PASSWORD -w /srv/anas
```

`secret get` 只在该值由 Module 生成并保存到 Secret Store 时可用。用户显式写入配置的值不会由安全库存命令回显。对于 `credential_rotate`，不能用 `config set` 或 `env.<KEY>` 代替应用内部轮换；对于仍标为普通重建的敏感参数，当前 CLI 虽接受 `config set`，但值会进入 argv/shell history，建议省略并使用生成 Secret，或在受保护的配置编辑流程中设置。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

新装和既有数据目录在开放 TCP 前统一写入 SCRAM HBA；管理员口令由既有 ANAS Secret 投影恢复为 SCRAM，
普通应用角色由 Resource ensure 写入自己的 SCRAM 口令。错误/空口令、冒用管理员及跨库访问必须失败。
ensure 和 inspect 都以应用角色真实连接并执行扩展检查；inspect 不创建对象或改口令。

PostgreSQL 制品更新涉及共享实例：ANAS 停止 workspace 写入、取得一致恢复点，启动 Provider 并完成固定
扩展维护后再启动消费者。普通 ensure/start/restart 不升级扩展，版本不符时阻断。当前固定升级路径为
pgvector `0.8.1 → 0.8.2`；未知来源拒绝，部分完成后按实际版本重试。不能用旧镜像直接打开未知的新数据，
应恢复 ANAS 的数据库、耦合媒体、配置/Secret 及匹配镜像集合。

```bash
anas plan -c /srv/anas/config.yml
anas config list postgres -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

普通 `config set` 不能安全轮换数据库超级用户密码。

本版认证与扩展已通过隔离 Docker 容器测试；这不替代真实服务器的 ANAS apply、全 workspace 一致恢复及
Immich 业务验收。`postgres.password` 仍未接入通用凭据轮换库存，本次只交付认证前置子项。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`18.4.0-r4`（reviewed 2026-10-03）
- Timezone / 时区：`container` — PostgreSQL and optional Adminer receive TZ; database timezone remains an independent SQL setting.
- Language scope / 语言范围：optional Adminer 5.5.0 Web UI; PostgreSQL itself has no UI language
- Selection / 选择方式：`browser`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：Adminer language code
- Fallback / 回退：Adminer negotiates browser language and falls back to English.
- Supported languages / 支持语言（47）：`ar`, `bg`, `bn`, `bs`, `ca`, `cs`, `da`, `de`, `el`, `en`, `es`, `et`, `fa`, `fi`, `fr`, `gl`, `he`, `hi`, `hr`, `hu`, `id`, `it`, `ja`, `ka`, `ko`, `lt`, `lv`, `ms`, `nl`, `no`, `pl`, `pt-BR`, `pt`, `ro`, `ru`, `sk`, `sl`, `sr`, `sv`, `ta`, `th`, `tr`, `uk`, `uz`, `vi`, `zh-TW`, `zh`

Evidence / 证据：

- [v5.5.0 — Adminer language files; xx test locale excluded](https://github.com/vrana/adminer/tree/v5.5.0/adminer/lang)
<!-- generated:localization:end -->
