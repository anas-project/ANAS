# Forgejo

自托管 Git 协作服务，提供 HTTP/SSH Git、代码审查、Issue、Wiki、Git LFS 和 Package Registry，
并接入 ANAS OIDC 身份认证。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `forgejo` |
| 版本 / revision | `15.0.7-r2` |
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
组织、team、仓库权限及部署密钥仍由 Forgejo 管理；Module 不配置 SAML 源，也不向目录回写密码。

**只有 OIDC 一条登录链路。** Module 不配置 LDAP 源，不同步目录用户与组，也不发布或消费
`anasIdentityAnchor`：固定版本没有"按不可变 ID 把 OIDC 身份绑定到预配 LDAP 账号"的接口，按用户名或
邮箱回退绑定会让回收的标识符接上别人的旧账号。代价是**撤权不会自动到达 Forgejo**：目录里停用一个人
之后，他的 Forgejo 账号、已建立的 session、access token 与 SSH key 仍然有效，而后两者根本不经过登录，
不存在"下次登录时收敛"。离职与紧急撤权必须由管理员在 Forgejo 停用该账号，这一步不能省。

**改名不会建出第二个账号。** Forgejo 按 OIDC `sub` 认人（`sub` 存进内部的 `login_name` 字段），用户名
取 `preferred_username` 且只在建号时写一次，因此目录改名之后仍是同一个账号，只是 Forgejo 里的用户名
冻结在旧值，仓库路径仍然是 `/old/...`；邮箱同样不刷新。邮箱别名与用户名仍然不得回收再分配——Forgejo
的邮箱全局唯一，旧地址还挂在原账号上时，新人首次登录会因冲突建号失败。（以上按上游实现推断，尚未
探针复核。）

Forgejo `/user/logout` 只清除应用 session。固定版本没有可由本 Module 稳定登记的 RP-Initiated
Logout 或 IAM 主动 front/back-channel receiver，因此不声明单点登出与后台会话撤销。

### 目录属性变更说明

**匹配键**：OIDC `sub`，Forgejo 把它原样存进内部的 `login_name` 字段。用户名另取
`preferred_username`，只在建号时写一次，不参与认人。**这个键在目录改名后是否稳定，取决于部署
选了哪个 IAM Provider**——`authentik` 与 `casdoor` 发出的主体标识符都不是目录标签，改名后 `sub`
不变；`llng` 的主体标识符默认取自登录名，改名后 `sub` 会变，Forgejo 会建出第二个账号、原有仓库
留在旧账号下（见[Module IAM / OIDC 支持清单](/reference/module-iam-support)的匹配键列）。

| 目录侧变更 | Forgejo 的行为 | 证据 |
| --- | --- | --- |
| `sAMAccountName` 改变 | 同一账号、不新建；Forgejo 里的用户名冻结在旧值，仓库路径仍是 `/old/...`，URL 标识符不跟着变。Provider 的 `sub` 取自登录名时（`llng`）此结论不成立，会建出第二个账号 | `推断` |
| `mail` 改变 | 不刷新，账号里仍是建号时的旧地址；邮箱不参与账号绑定，但 Forgejo 的邮箱全局唯一，旧地址还挂在原账号上时，用同一地址的新账号会建号失败 | `推断` |
| `displayName` 与其他 profile 属性 | 只在建号时写一次，之后从不刷新 | `推断` |
| 直接或递归组成员变更 | 该用户**下次 OIDC 登录**时随 groups 声明到达 team 与站点管理员映射；不登录就不收敛，没有同步或实时路径 | `推断` |
| 账号停用 | **无自动路径**。已有 Forgejo session、access token 与 SSH/部署密钥全部继续有效；后两者根本不经过登录，不存在"下次登录时收敛" | `推断` |
| 账号删除 | 同上，且 Forgejo 账号与其仓库、issue、包全部原样保留；资产归属不会自动转移 | `推断` |
| 标识符回收再分配 | 用户名回收：新人首次登录时 `preferred_username` 与原账号冲突，Forgejo 建号失败（fail-closed）。邮箱回收同样冲突失败。但若 Provider 的 `sub` 也取自被回收的标签（`llng`），新人会直接接上旧账号（**fail-open**） | `推断` |

**兜底路径**——上表每一行"无自动路径"对应的运维动作：

1. 目录里停用或删除一个人时，必须在 Forgejo 以站点管理员身份**停用该账号，并显式吊销它的全部
   access token 与 SSH/部署密钥**。固定版本的 `prohibit_login` 是否同时关闭 token 与 Git over SSH
   尚未复核，因此两步都要做，不能只停用账号；
2. 删除目录账号前，先在 Forgejo 把该账号拥有的仓库转移给接手人或组织，再删除 Forgejo 账号；
3. 组撤权要立即生效时，除上面第 1 步外还要结束该用户已有的 Forgejo session；等下次登录不够；
4. **目录侧流程约束**：用户名与邮箱别名不得回收再分配，改名走正式流程。Module 无法强制这两条。

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
| `forgejo.actions_isolation` | enum (`auto`, `incus_vm`, `incus_container`) | — | `auto` | `static` | `FORGEJO_ACTIONS_ISOLATION` | 否 | 否 | 否 | 是 | `container_recreate` | 向 compute Provider 申请的隔离档：VM 有独立 guest kernel，系统容器与宿主共享内核 |
| `forgejo.actions_runner_image` | string | `format: json_object` | `""` | `static` | `FORGEJO_ACTIONS_RUNNER_IMAGE` | 否 | 否 | 否 | 是 | `container_recreate` | 结构化镜像引用；Core 在渲染前冻结摘要 |
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
`owner/repo` 授权集合，不是第二个开关，也不会注册 global Runner。开启还需要一台独立的 Incus 宿主
（由 `incus` Module 经 compute contract 供给 endpoint、受限证书与 profile），以及一个固定的 Runner
image fingerprint。前置校验分两处：Hook 在渲染时校验 scope、控制面账号口令与 fingerprint，一次性
preflight 在 Forgejo 启动前连接 Incus 验证 project、配额与 profile。任一处失败 Forgejo 都不会启动，
因此不存在"只开了服务端"的状态。

### 执行实例的两档隔离

`forgejo.actions_isolation` 决定作业跑在什么里面，默认 `auto`：

| 档位 | 实例 | 内核 | 对宿主的要求 |
| --- | --- | --- | --- |
| `incus_container`（`auto` 解析到这一档） | 非特权 Incus 系统容器 | **与宿主共享内核** | 不需要 KVM |
| `incus_vm` | QEMU/KVM 虚拟机 | 独立 guest kernel | 宿主必须具备 KVM |

默认是容器档，因为 NAS 与小型主机不保证提供 KVM，把需要 KVM 的档位设成默认会让 Actions 在这类机器
上装不上。两档的配额、一次性实例、无宿主挂载、无 socket、租约网络（出公网、经 Traefik 访问 Forgejo，到不了局域网、宿主、其他租约与其他作业实例，不接受连入）完全相同，**差别只在内核边界**：
容器档的隔离以宿主内核为前提，一个内核提权漏洞会打到宿主。因此**跨信任域的写入者、或会执行不受信
输入的 scope（典型是接受外部 PR 的公开仓库）请显式设为 `incus_vm`**，并为它准备具备 KVM 的宿主。
选档不会自动发生：宿主没有 KVM 时不会自动降级，有 KVM 时也不会自动升级。

### controller 使用的 Forgejo 账号

开启 Actions 会在 Forgejo 里调和一个固定账号 `anas_actions_controller`，口令由 ANAS Secret Store
管理，供 controller 调用 Actions Runner API。它不是给人用的登录入口——人用的恢复入口是下文的
`break_glass`。

**当前该账号被建成站点管理员，这是一处已登记的权限偏差。** controller 实际只调用获批 scope 下的三个
`actions/runners` 端点，站点管理员只是因为目前没有"按 scope 授予组织 owner / 仓库 admin"的调和路径。
controller 被攻陷仍等同于 Forgejo 全站管理员，按 scope 收敛权限仍待完成。
关闭 `actions_enabled` 的调和现在先等待同一个禁用 controller 完成清理并成功退出，再由
独立 `break_glass` 管理员使 **ANAS 管理的 controller 口令失效**。清理失败、账号身份变化或
凭据不匹配时拒绝继续，不冒充撤销成功。重新启用只恢复具有已验证归属记录的同一账号；
不会重置未知的同名账号。账号本身、站点管理员角色及人工另建的 token/SSH key 不因此删除。
发生泄露或调和失败时，管理员仍须在 Forgejo 停用账号并撤销其全部其他凭据。实际账号 API
与完整 Core/Compose 开关链路的验收分开记录，见实施计划 M3。

controller 默认每 15 秒查询已批准 scope；空队列不注册 Runner、不创建实例。每个 waiting job 对应
一个 ephemeral registration 和一个 Incus 实例，并用 job handle 启动 `forgejo-runner one-job`。token
只经 Incus agent stdin 写入 guest tmpfs；实例内使用独立 `runner-agent` 与 `runner-engine` 用户及
rootless Podman。关闭唯一开关时 controller 进入清理模式后退出。Module 不会把 Docker/Podman socket
挂入 Forgejo 容器，Runner 实例也不挂载 ANAS/Forgejo 数据。

自定义 Git Hooks 与 local-path import 默认关闭，必须分别显式开启。前者允许仓库 Hook 以 Forgejo
用户身份执行服务端代码；后者只允许读取容器内本来已可见的路径，Module 不会因此挂载任意宿主目录。
两项变更都会重建 Forgejo 容器，可以独立启停。

`${DATA_PATH}/forgejo` 对应完整 `/var/lib/gitea`，包含 Git repository、LFS、Package、附件、
SSH key/config 和应用配置；用户、权限、Issue 等关系状态位于数据库 Resource。备份/恢复必须在
同一一致性点覆盖数据目录、数据库、`.anas/secrets.yml` 与部署元数据。恢复后至少验证 HTTP/SSH
clone/push、LFS、Package、OIDC 登录和本地恢复登录。

Actions controller 的状态在 Docker named volume `forgejo_actions_state` 里，不属于上面的一致点，但
目前**也不能随手丢弃**：丢了它，Actions 还开着时孤立的 Runner 实例要等到关闭开关才回收，个别孤立
的 Runner registration 则会永久留在 Forgejo 里。重建部署时请一并保留它。

## 当前限制

- 状态为 `developing`；PostgreSQL/MariaDB、amd64/arm64、真实浏览器 OIDC、备份恢复和前一版本
  升级/回滚 E2E 尚是提升 `release` 的门槛。
- SMTP、S3/object storage 和外部搜索尚未自动配置。Actions controller/Runner image 资产已接入，但
  独立 Incus 宿主、网络 egress 和真实 one-job E2E（两档各一遍）尚未完成，因此 Actions 仍是开发中
  能力。Runner 镜像需要按 amd64/arm64 × 容器/VM 各签收一份 fingerprint，配置里只有一个值，填错
  档位或架构要到创建实例失败时才会暴露。
- `forgejo.oidc_client_secret` 使用 `migrate` 模式，不能参与统一 `credential rotate` 事务；变更需
  在维护窗口内同时更新 IAM client 与 Forgejo auth source 并验证登录。

选型与范围依据见[自托管开源 Git 服务研究](../../docs/research/self-hosted-open-source-git-services-research.md)，
当前实现见[技术文档](docs/technical.md)，设计决策与剩余工作分别见
[Forgejo Module 设计](../../docs/architecture/forgejo-module-design.md)和
[实施计划](dev-docs/plans/forgejo-module.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`15.0.7-r2`（reviewed 2026-08-22）
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

## 结构化 compute 镜像

```yaml
modules:
  incus:
    config:
      image_architecture: amd64
  forgejo:
    config:
      actions_runner_image: {fingerprint: "<64hex>"}
```

填写已导入租约 project 的实际镜像摘要。拒绝裸字符串旧格式；命名引用需要受信发布目录，当前随包目录为空。
