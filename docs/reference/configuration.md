# `config.yml` 结构化配置与环境变量清单

本文回答两个问题：哪些设置已经有 `config.yml` 的结构化入口，哪些设置目前只能写入
顶层 `env:`。清单按 2026-08-13 的当前工作树统计；module 增删参数后应重新运行文末命令，
不要把本文中的数字当成固定 ABI。

## 结论

- `anas config list --json` 当前登记 **131** 个可设置参数：17 个 `global` 参数、114 个
  module 参数。
- 114 个 module 参数中，110 个保存到 `modules.<module>.config.<parameter>`；4 个
  `samba_fs` 参数虽然已经在 module manifest 中声明并拥有默认值、类型和变更策略，
  但为了导出裸环境变量，YAML 地址仍是 `env.<KEY>`。
- `modules`、`administration`、`identity`、`dynamic_dns`、`rollback` 的控制字段
  和 `secrets` 也有结构化 schema，但它们不是“参数到环境变量”的映射，因此不计入
  上述 131 项。
- 顶层 `env:` 是开放的 raw-env 逃生口，任意键都能写入，所以“只能用环境变量”的总数
  理论上不可穷举。下文只列仓库当前明确使用、且没有结构化参数的用户覆盖项。

这里的“已结构化”分两层：

1. **原生结构化字段**：Go YAML schema 直接解析、校验的部署语义；
2. **manifest 声明参数**：值仍会变成环境变量，但参数名、默认值、类型、敏感性和变更
   策略由 module 声明，`config list/set/explain/plan` 能识别它。它不等同于任意 raw env。

## 原生结构化字段

| YAML 路径 | 说明 | 状态 |
| --- | --- | --- |
| `modules.<module>` | 请求启用的 module 及其配置 | 已使用 |
| `global.*` | 17 个部署级参数，详见下一节 | 已使用 |
| `administration.bootstrap.username` | 引导管理员用户名 | 已使用 |
| `administration.bootstrap.display_name` | 引导管理员显示名 | schema 已接收，当前尚未下发 |
| `administration.bootstrap.email` | 引导管理员邮箱 | schema 已接收，当前尚未下发 |
| `administration.bootstrap.roles[]` | 引导管理员角色 | schema 已接收，当前尚未下发 |
| `administration.local_accounts.username_template` | 本地管理员用户名模板，必须包含 `{module}` | 已使用 |
| `administration.local_accounts.password_policy` | 当前只允许 `generated_per_module` | 已使用 |
| `administration.local_accounts.password_length` | 生成密码长度，最小 16 | 已使用 |
| `identity.directory.provider` | 目录 Provider；当前只接受 `samba_dc` | 已使用 |
| `identity.iam.provider` | IAM Provider | 已使用 |
| `identity.iam.default_protocol` | IAM 默认协议 | 已使用 |
| `dynamic_dns.provider` | 管理部署自身 DNS 记录的 module 或 `auto` | 已使用 |
| `dynamic_dns.dns_provider` | DNS 厂商 | 已使用 |
| `rollback.snapshot.backend` | 快照后端 | 已使用 |
| `rollback.snapshot.source` | 快照源 | 已使用 |
| `rollback.snapshot.root` | 快照根路径 | 已使用 |
| `rollback.snapshot.keep_auto` | 自动快照保留数 | 已使用 |
| `secrets.<name>` | 用户提供的敏感值；按消费者声明分发 | 已使用，键集合动态 |
| `modules.<module>.enabled` | 启用或禁用服务 | 已使用 |
| `modules.<module>.depends_on[]` | 用户追加的依赖 | 已使用 |
| `modules.<module>.identity.login_protocol` | `auto`、`oidc` 或 `saml` | 已使用 |
| `modules.<module>.administration.local_accounts.<id>.username` | 覆盖 module 本地账户用户名 | 已使用，账户 ID 动态 |
| `modules.<module>.config.<parameter>` | manifest 声明的 module 参数 | 已使用，见下一节 |
| `env.<KEY>` | 原始环境变量逃生口 | 开放 map，不做键集合校验 |

`config.Load` 使用 `KnownFields(true)`：除 `secrets`、`modules` 和 `env` 这些有意开放的
map 外，拼错结构化字段会直接报错，不会静默忽略。

`global.timezone`、`global.default_language` 和 `global.default_locale` 都是可选字段。显式值
会在加载时校验和规范化。省略 `default_locale` 时，Runner 依次使用：包含明确地区的显式
`default_language`、宿主 locale、CLDR likely-subtag 推断、`en-US`。只有 `language.Region()`
返回 `Exact` 时语言才直接成为 locale，因此 `en-GB` 可以推导，`en` 和 `zh-Hans` 不会
覆盖可用的宿主 locale。`config list` 与渲染环境显示最终解析的 BCP 47 值。

语言只控制 UI 文本 fallback，locale 控制区域格式；推导只是默认值来源，不表示两个概念
相同。各 Module 的实际消费边界见 [Module 时区与语言支持矩阵](/reference/module-localization)。

## 已声明的 131 个参数

表中参数都能被 `anas config list` 列出，也能通过 `anas config set` 地址设置。除特别说明
外，全局参数写为 `global.<parameter>`，module 参数写入
`modules.<module>.config.<parameter>`。

| 所有者 | 数量 | 参数 |
| --- | ---: | --- |
| `global` | 17 | `base_domain`, `basicauth_user`, `chinese_build_speedup`, `chinese_speedup`, `container_prefix`, `default_language`, `default_locale`, `default_service_root_password`, `dns_server`, `email`, `host_ip`, `image_prefix`, `ipv4`, `ipv6`, `network_prefix`, `timezone`, `virtual_domain` |
| `authentik` | 6 | `db_name`, `db_type`, `domain_prefix`, `ldap_enabled`, `ldap_password_writeback`, `log_level` |
| `collabora` | 4 | `auto_save`, `domain_prefix`, `interface`, `log_level` |
| `ddns_go` | 10 | `dns_provider`, `domain_prefix`, `interval`, `ipv4_gettype`, `ipv4_interface`, `ipv4_urls`, `ipv6_gettype`, `ipv6_interface`, `ipv6_urls`, `web_enabled` |
| `ddns_updater` | 9 | `dns_provider`, `domain_prefix`, `forward_auth_interface`, `publicip_dns_providers`, `publicip_fetchers`, `publicip_ipv4_providers`, `publicip_ipv6_providers`, `publicip_providers`, `ttl` |
| `eturnal` | 2 | `domain_prefix`, `port` |
| `lam` | 3 | `admin_password`, `domain_prefix`, `language` |
| `lego` | 2 | `dns_server`, `virtual_domain` |
| `llng` | 9 | `adminer_enabled`, `db_name`, `db_type`, `domain_prefix`, `enable_test`, `log_level`, `manager_domain_prefix`, `password`, `test_domain_prefix` |
| `mariadb` | 2 | `adminer_enabled`, `root_password` |
| `meshcentral` | 4 | `db_name`, `db_type`, `domain_prefix`, `mps_port` |
| `netbird` | 3 | `adminer_enabled`, `domain_prefix`, `iam_protocol` |
| `nextcloud` | 15 | `admin_password`, `db_name`, `db_type`, `debug`, `domain_prefix`, `iam_protocol`, `language`, `locale`, `log_level`, `memories_enabled`, `memory_limit`, `phone_region`, `rm_skeleton_files`, `talk_enabled`, `upload_max_size` |
| `oauth2_proxy` | 3 | `allow_groups`, `domain_prefix`, `iam_protocol` |
| `postgres` | 3 | `adminer_enabled`, `password`, `username` |
| `samba_dc` | 30 | `admin_name`, `admin_password`, `administrator_password`, `anchor_bind_name`, `anchor_bind_password`, `anchor_scan_interval`, `app_filter`, `create_structure`, `dns_allowed_networks`, `dns_cache_size`, `dns_debug`, `dns_forwarders`, `ldap_bind_name`, `ldap_bind_password`, `log_level`, `max_log_size`, `netbios_name`, `password_bind_name`, `password_bind_password`, `realm`, `template_homedir`, `template_shell`, `user_complex_pass`, `user_lockout_duration`, `user_lockout_reset_after`, `user_lockout_threshold`, `user_max_pass_age`, `user_min_pass_age`, `user_min_pass_length`, `user_password_history` |
| `samba_fs` | 7 | `hostname`, `log_level`, `share_access_mode`, `share_dir_name`, `share_guest_read_only`, `use_default_domain`, `wsdd_log_level` |
| `traefik` | 2 | `base_port`, `domain_prefix` |

`samba_fs` 的以下 4 项是特殊情况。它们有 manifest 元数据，不能算“未声明 raw env”，
但因为 `config.exports` 要求发布裸键，实际 YAML 地址只能是顶层 `env:`：

| YAML 地址 | 所有者 | 环境变量 |
| --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | `samba_fs` | `SHARE_ACCESS_MODE` |
| `env.SHARE_DIR_NAME` | `samba_fs` | `SHARE_DIR_NAME` |
| `env.SHARE_GUEST_READ_ONLY` | `samba_fs` | `SHARE_GUEST_READ_ONLY` |
| `env.USE_DEFAULT_DOMAIN` | `samba_fs` | `USE_DEFAULT_DOMAIN` |

例如 `anas config set samba_fs.share_guest_read_only Yes` 接受逻辑参数路径，但写回文件时
会落到 `env.SHARE_GUEST_READ_ONLY`。

## 当前只能使用顶层 `env:` 的用户覆盖项

以下键由仓库明确消费，但没有 global 字段或 module manifest 参数，因此当前只能写成
`env.<KEY>`。这些项没有 manifest 级类型校验、敏感标记或变更策略，`config list` 也不会
主动列出它们。

### 下载、包管理器与镜像源

| 键 | 用途 |
| --- | --- |
| `APT_MIRROR_URL` | Debian/Ubuntu APT 镜像 |
| `APK_MIRROR_URL` | Alpine APK 镜像 |
| `NPM_REGISTRY_URL` | npm registry |
| `GOPROXY_URL` | Go module proxy |
| `GITHUB_DOWNLOAD_PROXY_PREFIX` | GitHub 下载代理前缀 |
| `BUILD_GITHUB_DOWNLOAD_PROXY_PREFIX` | 构建阶段的 GitHub 下载代理前缀 |
| `NEXTCLOUD_APPSTORE_URL` | Nextcloud 应用商店 API |
| `DOCKER_HUB_REGISTRY` | 构建阶段的 Docker Hub 基础镜像前缀 |
| `LLNG_DOCKER_HUB_REGISTRY` | LemonLDAP::NG 专用 Docker Hub 镜像前缀 |
| `ANAS_IMAGE_REGISTRY` | ANAS 派生镜像与上游 mirror 的统一运行时仓库 |
| `GHCR_REGISTRY` | 构建阶段的第三方 GHCR 基础镜像前缀 |
| `NEXTCLOUD_APT_MIRROR_URL` | 只覆盖 Nextcloud 构建的 APT 镜像 |
| `LAM_APT_MIRROR_URL` | 只覆盖 LAM 构建的 APT 镜像 |
| `LAM_DOWNLOAD_URL` | 直接覆盖 LAM 安装包下载地址 |

`global.chinese_speedup: true` 只生成 `ANAS_IMAGE_REGISTRY`、`NEXTCLOUD_APPSTORE_URL`
和 `GITHUB_DOWNLOAD_PROXY_PREFIX`，供正式发布镜像及 Nextcloud 运行时下载使用。
`global.chinese_build_speedup: true` 生成 APT、APK、npm、Go、Docker Hub、GHCR、LLNG
和构建期 GitHub 下载默认值；修改它或上表中的构建变量后必须执行 `anas apply --build`。
顶层 `env:` 中的显式值始终优先。

### 宿主构建与 Compose 集成

| 键 | 用途 |
| --- | --- |
| `DOCKER_BUILD_NETWORK` | module 镜像构建阶段使用的 Docker 网络，默认 `default` |
| `DOCKER_SOCKET_PATH` | Traefik 挂载的宿主 Docker socket，默认 `/var/run/docker.sock` |

这些键属于高级宿主集成项。若它们需要稳定的校验和生命周期语义，应提升为 global 或
module manifest 参数，而不是继续扩充 raw env 清单。

## 不应当手工配置的环境变量

Hook 输出、生成 Secret、工作区路径、宿主网络探测结果和 `ANAS_*` 跨 module 契约也会
出现在渲染后的 `.env`，但它们不是“只能用 env 配置”的用户参数。例如 `DATA_PATH`、
`USER_DATA_PATH`、`LOCAL_DNS_SERVER`、`ANAS_TLS_*`、`ANAS_IAM_*`、`POSTGRES_HOST` 都由
runner 或 module 计算。把这类键写进顶层 `env:` 可能覆盖内部结果，但不构成受支持的配置
接口，也不应纳入 raw-only 用户清单。

用户提供的凭据应优先写入 `secrets.<name>`；不要因为最终载体是环境变量，就把 Token、
密码或私钥放进顶层 `env:`。

## 维护与复核

获取权威的声明参数清单：

```sh
go run ./cmd/anas config list --json
```

统计总数和各所有者数量：

```sh
go run ./cmd/anas config list --json \
  | sed -n '/^{/,$p' \
  | jq '.parameters | {total: length, by_module: group_by(.module) | map({module: .[0].module, count: length})}'
```

查找已经声明、但由于裸名导出而落入顶层 `env:` 的参数：

```sh
go run ./cmd/anas config list --json \
  | sed -n '/^{/,$p' \
  | jq '.parameters[] | select(.path | startswith("env.")) | {path, module, parameter, env_key}'
```

维护规则：

1. 新增普通用户配置时，优先加入 `global` schema 或对应 module 的 `config` 声明；
2. 新增 raw-only 键时同步更新本文，并说明为什么暂不结构化；
3. 新增、删除或重命名 manifest 参数后，重新生成统计并更新参数表；
4. 不把 runner/module 推导变量列为用户配置；
5. 凭据走 `secrets` 或 manifest 中标记 `sensitive` 的参数。
