# Forgejo

自托管 Git 协作服务，提供 HTTP/SSH Git、代码审查、Issue、Wiki、Git LFS 和 Package Registry，
并接入 ANAS OIDC 身份认证。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `forgejo` |
| 版本 / revision | `15.0.7-r1` |
| 状态 | `developing` |
| 类别 | `app` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖与最简配置

依赖 `traefik`、OIDC `iam` capability，以及 `relational_database >=1.0.0 <2.0.0` Contract；
数据库支持 PostgreSQL 和 MariaDB，默认 PostgreSQL。

```yaml
modules:
  forgejo: {}

identity:
  iam:
    provider: llng
```

默认 Web 地址为 `https://git.<BASE_DOMAIN>:<TRAEFIK_BASE_PORT>`，SSH 使用宿主机 `2222/tcp`。
若 2222 已被占用，请在部署前设置 `forgejo.ssh_port` 并同步防火墙规则。

## 身份、Group 与登录

Module 登记 confidential OIDC client `forgejo`，回调地址为
`<FORGEJO_DOMAIN_FULL>/user/oauth2/anas/callback`，scope 为 `openid profile email groups`。
首次 OIDC 登录由 Forgejo JIT 建号。启用 Samba 应用过滤时，IAM 只允许 `APP_forgejo`、
`APP_all` 或管理员组成员进入；管理员组 claim 会映射为 Forgejo site administrator。
组织、team、仓库权限及部署密钥仍由 Forgejo 管理，Module 不做 LDAP 同步或目录密码回写。

Forgejo `/user/logout` 只清除应用 session。固定版本没有可由本 Module 稳定登记的 RP-Initiated
Logout 或 IAM 主动 front/back-channel receiver，因此不声明单点登出与后台会话撤销。

## 本地恢复管理员

OIDC、目录或内部 CA 故障时，可从 ANAS Secret Store 取回专用 `break_glass` 账号：

```bash
anas admin local credential forgejo break_glass -w /srv/anas
```

固定用户名默认为 `admin_forgejo`，登录地址为 `<FORGEJO_DOMAIN_FULL>/user/login`。普通登录仍优先
使用 OIDC。本版只声明幂等 apply；Forgejo 15 CLI 无法完成带验证与回滚的事务式密码轮换，因此
`anas admin local rotate forgejo break_glass` 暂不可用，不能直接在应用内修改该托管密码。

## 所有可用配置参数

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `forgejo.actions_allowed_scopes` | string | — | `""` | `static` | `FORGEJO_ACTIONS_ALLOWED_SCOPES` | 否 | 否 | 否 | 是 | `container_recreate` | 可使用 ANAS Runner 的组织或仓库 scope，逗号分隔 |
| `forgejo.actions_enabled` | bool | — | `false` | `static` | `FORGEJO_ACTIONS_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | Actions 服务端与 one-job Runner controller 的唯一共同开关 |
| `forgejo.actions_incus_client_cert_b64` | string | — | `""` | `static` | `FORGEJO_ACTIONS_INCUS_CLIENT_CERT_B64` | 否 | 否 | 是 | 是 | `container_recreate` | restricted project Incus TLS client certificate 的 base64 值 |
| `forgejo.actions_incus_client_key_b64` | string | — | `""` | `static` | `FORGEJO_ACTIONS_INCUS_CLIENT_KEY_B64` | 否 | 否 | 是 | 是 | `container_recreate` | restricted project Incus TLS client key 的 base64 值 |
| `forgejo.actions_incus_endpoint` | string | — | `""` | `static` | `FORGEJO_ACTIONS_INCUS_ENDPOINT` | 否 | 否 | 否 | 是 | `container_recreate` | 独立 Incus/KVM 宿主的 HTTPS endpoint |
| `forgejo.actions_incus_profile` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `anas-forgejo-runner` | `static` | `FORGEJO_ACTIONS_INCUS_PROFILE` | 否 | 否 | 否 | 是 | `container_recreate` | 受限 one-job Runner VM profile 名称 |
| `forgejo.actions_incus_server_cert_b64` | string | — | `""` | `static` | `FORGEJO_ACTIONS_INCUS_SERVER_CERT_B64` | 否 | 否 | 是 | 是 | `container_recreate` | 固定 Incus server certificate 的 base64 值 |
| `forgejo.actions_runner_image` | string | `pattern: ^(?:[0-9a-f]{64})?$` | `""` | `static` | `FORGEJO_ACTIONS_RUNNER_IMAGE` | 否 | 否 | 否 | 是 | `container_recreate` | 已批准 Runner VM image 的固定 SHA-256 fingerprint |
| `forgejo.custom_git_hooks_enabled` | bool | — | `false` | `static` | `FORGEJO_CUSTOM_GIT_HOOKS_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否允许仓库自定义 Git Hooks；Hook 会以 Forgejo 用户身份执行服务端代码 |
| `forgejo.db_name` | string | — | `forgejo` | `static` | `FORGEJO_DB_NAME` | 否 | 否 | 否 | 否：`migrate-forgejo-database` | `data_migrate` | 应用数据库名 |
| `forgejo.db_type` | enum (`auto`, `postgres`, `mariadb`) | — | `auto` | `static` | `FORGEJO_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-forgejo-database` | `data_migrate` | 关系数据库类型或自动选择 |
| `forgejo.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `git` | `static` | `FORGEJO_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `forgejo.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `FORGEJO_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议；仅支持 OIDC |
| `forgejo.language` | string | — | — | `inherited` | `FORGEJO_LANGUAGE` | 否 | 是 | 否 | 是 | `reconcile` | 默认 UI 语言；浏览器和用户偏好优先 |
| `forgejo.local_path_import_enabled` | bool | — | `false` | `static` | `FORGEJO_LOCAL_PATH_IMPORT_ENABLED` | 否 | 否 | 否 | 是 | `container_recreate` | 是否允许从 Forgejo 容器内已可见的本地路径导入；不会增加宿主路径挂载 |
| `forgejo.ssh_port` | int | `1..65535` | `2222` | `static` | `FORGEJO_SSH_PORT` | 否 | 否 | 否 | 是 | `container_recreate` | 对外 SSH Git 端口 |

### 查询和修改

```bash
anas config list forgejo -w /srv/anas
anas config set forgejo.domain_prefix git -w /srv/anas
anas config set forgejo.ssh_port 2222 -w /srv/anas
anas config set forgejo.language zh-CN -w /srv/anas
anas config set forgejo.custom_git_hooks_enabled true -w /srv/anas
anas config set forgejo.local_path_import_enabled true -w /srv/anas
anas config plan -w /srv/anas
```

数据库类型或名称变更不会搬迁既有数据，必须先完成一致性备份并执行显式迁移流程。

## Actions、高风险开关、存储与备份

Actions 默认关闭，只提供 `forgejo.actions_enabled` 一个功能开关；同一值同时控制 Forgejo 服务端和
one-job Runner controller，不存在 `runner.enabled`。`actions_allowed_scopes` 是逗号分隔的组织或
`owner/repo` 授权集合，不是第二个开关，也不会注册 global Runner。开启前还必须配置独立 Incus/KVM
宿主 endpoint、restricted project TLS credential、受限 profile 和固定 Runner image fingerprint；
缺少任一前置条件时 Hook 会拒绝开启，不能只启动 Actions 服务端。

controller 默认每 15 秒查询已批准 scope；空队列不注册 Runner、不创建 VM。每个 waiting job 对应
一个 ephemeral registration 和一个 Incus VM，并用 job handle 启动 `forgejo-runner one-job`。token
只经 Incus agent stdin 写入 guest tmpfs；VM 内使用独立 `runner-agent` 与 `runner-engine` 用户及
rootless Podman。关闭唯一开关时 controller 进入清理模式后退出。Module 不会把 Docker/Podman socket
挂入 Forgejo 容器，Runner VM 也不挂载 ANAS/Forgejo 数据。

自定义 Git Hooks 与 local-path import 默认关闭，必须分别显式开启。前者允许仓库 Hook 以 Forgejo
用户身份执行服务端代码；后者只允许读取容器内本来已可见的路径，Module 不会因此挂载任意宿主目录。
两项变更都会重建 Forgejo 容器，可以独立启停。

`${DATA_PATH}/forgejo` 对应完整 `/var/lib/gitea`，包含 Git repository、LFS、Package、附件、
SSH key/config 和应用配置；用户、权限、Issue 等关系状态位于数据库 Resource。备份/恢复必须在
同一一致性点覆盖数据目录、数据库、`.anas/secrets.yml` 与部署元数据。恢复后至少验证 HTTP/SSH
clone/push、LFS、Package、OIDC 登录和本地恢复登录。

## 当前限制

- 状态为 `developing`；PostgreSQL/MariaDB、amd64/arm64、真实浏览器 OIDC、备份恢复和前一版本
  升级/回滚 E2E 尚是提升 `release` 的门槛。
- SMTP、S3/object storage 和外部搜索尚未自动配置。Actions controller/Runner image 资产已接入，但
  独立 Incus/KVM、网络 egress 和真实 one-job E2E 尚未完成，因此 Actions 仍是开发中能力。
- `forgejo.oidc_client_secret` 使用 `migrate` 模式，不能参与统一 `credential rotate` 事务；变更需
  在维护窗口内同时更新 IAM client 与 Forgejo auth source 并验证登录。

选型与范围依据见[自托管开源 Git 服务研究](../../docs/research/self-hosted-open-source-git-services-research.md)，
当前实现见[技术文档](docs/technical.md)，设计决策与剩余工作分别见
[Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md)和
[实施计划](../../dev-docs/plans/forgejo-module.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`15.0.7-r1`（reviewed 2026-08-22）
- Timezone / 时区：`configured` — Forgejo time.DEFAULT_UI_LOCATION inherits ANAS TZ; signed-in users may retain their own UI preference.
- Language scope / 语言范围：Forgejo Web UI
- Selection / 选择方式：`application`
- ANAS global defaults / 全局默认：`default_language=fallback`; `default_locale=not_consumed`
- Upstream format / 上游格式：Forgejo locale key from the pinned i18n LANGS list
- Fallback / 回退：The matched ANAS language is moved to the first position; unmatched values warn and fall back to en-US, while browser and saved user choices take precedence.
- Supported languages / 支持语言（31）：`en-US`, `zh-CN`, `zh-HK`, `zh-TW`, `da`, `de-DE`, `nds`, `fr-FR`, `nl-NL`, `lv-LV`, `ru-RU`, `uk-UA`, `ja-JP`, `es-ES`, `pt-BR`, `pt-PT`, `pl-PL`, `bg`, `it-IT`, `fi-FI`, `fil`, `eo`, `tr-TR`, `cs-CZ`, `sl`, `sv-SE`, `ko-KR`, `el-GR`, `fa-IR`, `hu-HU`, `id-ID`
- Notes / 说明：Forgejo uses the first configured locale only when the browser does not match a listed locale. The hook keeps the complete pinned list and reorders the selected default instead of removing choices.

Evidence / 证据：

- [v15.0 — i18n LANGS and NAMES](https://forgejo.org/docs/v15.0/admin/config-cheat-sheet/)
<!-- generated:localization:end -->
