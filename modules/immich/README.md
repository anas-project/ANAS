# Immich

照片和视频上传、手机备份、相册、分享与搜索。当前状态为 developing：已经实现部署与固定版本入口保护，
真实 IAM/目录同步、移动端和全 workspace 恢复尚未验收。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `immich` |
| 版本 / revision | `3.2.4-r1` |
| 状态 | `developing` |
| 类别 | `app` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖

使用 Traefik、OIDC IAM Capability、共享 PostgreSQL relational_database Contract。仅支持空库安装，
不支持接管既有本地用户。Module 自带专属 Valkey 和可关闭的机器学习服务。

## 最简配置

```yaml
identity:
  iam:
    provider: authentik
modules:
  immich:
    config:
      machine_learning: false
      job_concurrency: 2
      video_concurrency: 1
```

默认入口 `https://photos.<BASE_DOMAIN>:<TRAEFIK_BASE_PORT>`。本版选定 Authentik `2026.5.6-r15`：
已有受信管理员 roleClaim 和目录撤权事件接线，真实登录/目录同步验收仍待执行。LLNG 仅有角色映射，
未发布所需撤权事件能力；Casdoor 尚不能表达该角色映射，未支持的组合在配置阶段拒绝。
不开启本地密码登录或本地管理员 setup。首个登录者必须属于受信管理员组并取得 `anas_role=admin`；
普通首访客不会变成管理员。没有应用内应急密码账号，IAM 故障时应恢复 IAM/目录/DNS/CA。

## 身份与撤权

OIDC sub 请求 Samba 永久 anchor，并保存为 Immich oauthId。内部 user.id 保持上游生成值，anchor
不进入 storageLabel 或媒体目录。相同 sub 再次登录仍使用原账号，邮箱只作资料和冲突检测。
固定镜像服务端拒绝无绑定建号、密码写入、OAuth 绑定替换/解绑和管理员全局解绑。

| 目录变更 | 当前行为与验收状态 |
| --- | --- |
| 登录名、邮箱、显示名改变 | sub 应保持 anchor；同 sub 命中原 id。资料不保证自动刷新，真实 IAM 改名待验收 |
| 组改变 | IAM 在新登录时判断 APP_immich/APP_all/管理员组；admin/user role 在 OIDC 回调更新 |
| 账号停用、删除或撤销准入 | 固定 Authentik 完成 LDAP 同步/删除时发送已协商的签名策略事件；接收端撤销旧 session/API key/分享，真实目录链路仍待验收 |
| anchor 回收或替换 | 不支持换绑；真实 PG fixture 已验证并发唯一和软删除后拒绝新绑定，真实 IAM/主机仍待验收 |

上游支持普通 RP 退出和 `/api/oauth/backchannel-logout`；Module 已登记 backchannel URI。
普通退出只删除当前会话，backchannel 删除匹配 sid/sub 会话，不撤销 API key 或分享。
固定镜像补丁持久记录已验证 token 的 jti；重放不能撤销随后创建的会话。签名测试 IdP 的合法/非法
token、旧 Cookie、并发及跨重启重放已通过；真实 IAM/浏览器/手机仍待验收，不声明双向登出完成。
目录撤权使用独立的已协商事件成员，不把普通退出改成长期凭据撤销；保留用户与媒体。
延迟的旧登录及其派生凭据需要按已验证 ID Token 的签发时间拒绝，重试保留原事件时间。

## 数据、队列与灾备

受管媒体：`data/immich/media` → `/data`；队列 AOF：`data/immich/valkey`；可重建模型缓存：
`data/immich/model-cache`。共享数据库由 PostgreSQL Provider 保存，Immich 只获得普通应用角色。
Provider 在启动应用前提供固定 `vector`、`earthdistance` 及依赖；应用显式选择 `pgvector`。

关闭 Immich 内置数据库定时备份，统一使用 ANAS 全 workspace 备份。恢复必须同时包含数据库、媒体、
配置/Secret 和匹配镜像，且会回退其他共享 PG 应用。恢复点后的上传可能丢失。真实一致恢复尚未验收。
只读外部图库尚无自动挂载配置；源文件须独立核对备份范围，不继承 AD/Samba ACL，不因受管媒体备份
而自动获备份。不得修改已生成 deployment 来增加不可冻结的挂载。

本机真实 AOF 待处理任务恢复、队列满盘探针与上传反例已通过。队列无法写入时不会报告上传成功，
新增资产记录先回滚，恢复队列后可重试；未入队清理的临时文件可能需要随后核对。

## 所有可用配置参数

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `immich.db_name` | string | — | `immich` | `static` | `IMMICH_DB_NAME` | 否 | 否 | 否 | 否：`migrate-immich-database` | `data_migrate` | 应用独立数据库名；修改需匹配媒体迁移 |
| `immich.db_type` | enum (`auto`, `postgres`) | — | `auto` | `static` | `IMMICH_DB_TYPE` | 否 | 否 | 否 | 否：`migrate-immich-database` | `data_migrate` | 仅共享 PostgreSQL |
| `immich.domain_prefix` | string | `length: 1..63`; `pattern: ^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$` | `photos` | `static` | `IMMICH_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 公开照片服务子域名前缀 |
| `immich.iam_protocol` | enum (`auto`, `oidc`) | — | `auto` | `static` | `IMMICH_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | 仅 OIDC；自动选择仍只能解析到 OIDC |
| `immich.job_concurrency` | int | `1..16` | `2` | `static` | `IMMICH_JOB_CONCURRENCY` | 否 | 否 | 否 | 是 | `container_recreate` | 非视频后台队列的并发数 |
| `immich.machine_learning` | bool | — | `true` | `static` | `IMMICH_MACHINE_LEARNING` | 否 | 否 | 否 | 是 | `container_recreate` | 同时开启应用 ML 设置和专属 ML 服务 |
| `immich.video_concurrency` | int | `1..8` | `1` | `static` | `IMMICH_VIDEO_CONCURRENCY` | 否 | 否 | 否 | 是 | `container_recreate` | 视频转换队列并发数 |

ML 关闭后照片/视频上传仍保留，依赖 ML 的搜索/识别功能关闭。未完成整机压力测试，不声明支持 4GB。

## 语言与时区

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`3.2.4-r1`（reviewed 2026-10-03）
- Timezone / 时区：`container` — The server and ML container receive ANAS TZ; media timestamps remain owned by Immich metadata.
- Language scope / 语言范围：Immich Web UI
- Selection / 选择方式：`browser`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：Translation filenames use underscores; Chinese aliases select zh_Hans or zh_Hant; upstream tl is canonicalized to fil.
- Fallback / 回退：Saved user preference or browser language selects the closest available locale; unmatched languages fall back to English. Script variants remain separate.
- Supported languages / 支持语言（88）：`af`, `ar`, `az`, `be`, `bg`, `bi`, `bn`, `br`, `bs`, `ca`, `cs`, `cv`, `da`, `de`, `de-CH`, `el`, `en`, `en-GB`, `eo`, `es`, `et`, `eu`, `fa`, `fi`, `fil`, `fr`, `ga`, `gl`, `gsw`, `gu`, `he`, `hi`, `hr`, `hu`, `hy`, `id`, `is`, `it`, `ja`, `ka`, `kab`, `kk`, `km`, `kmr`, `kn`, `ko`, `kxm`, `lb`, `lmo`, `lt`, `lv`, `mfa`, `mi`, `mk`, `ml`, `mn`, `mr`, `ms`, `nb-NO`, `ne`, `nl`, `nn`, `pa`, `pl`, `pt`, `pt-BR`, `ro`, `ru`, `si`, `sk`, `sl`, `sq`, `sr-Cyrl`, `sr-Latn`, `sv`, `sw`, `swg`, `ta`, `te`, `th`, `tr`, `uk`, `ur`, `uz`, `vi`, `yue-Hant`, `zh-Hans`, `zh-Hant`
- Notes / 说明：No module language override is exposed; application preferences and browser negotiation remain native.

Evidence / 证据：

- [3.2.4 — translation JSON filenames](https://github.com/immich-app/immich/tree/v3.2.4/i18n)
- [3.2.4 — langCodes, convertBCP47, aliases, getPreferredLocale](https://github.com/immich-app/immich/blob/v3.2.4/web/src/lib/utils/i18n.ts)
<!-- generated:localization:end -->

[技术实现](docs/technical.md)记录固定镜像、入口补丁和测试边界。
