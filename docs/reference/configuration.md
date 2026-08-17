# `config.yml` 结构化配置与环境变量清单

本文回答两个问题：哪些设置已经有 `config.yml` 的结构化入口，哪些设置目前只能写入
顶层 `env:`。清单按 2026-08-15 的当前工作树统计；module 增删参数后应重新运行文末命令，
不要把本文中的数字当成固定 ABI。

## 结论

- `anas config list --json` 当前登记 **128** 个可设置参数：14 个 `global` 参数、114 个
  module 参数。
- 114 个 module 参数中，110 个保存到 `modules.<module>.config.<parameter>`；4 个
  `samba_fs` 参数虽然已经在 module manifest 中声明并拥有默认值、类型和变更策略，
  但为了导出裸环境变量，YAML 地址仍是 `env.<KEY>`。
- `modules`、`administration`、`identity`、`dynamic_dns`、`rollback` 的控制字段
  和 `secrets` 也有结构化 schema，但它们不是“参数到环境变量”的映射，因此不计入
  上述 128 项。
- 顶层 `env:` 是开放的 raw-env 逃生口，任意键都能写入，所以“只能用环境变量”的总数
  理论上不可穷举。下文只列仓库当前明确使用、且没有结构化参数的用户覆盖项。

这里的“已结构化”分两层：

1. **原生结构化字段**：Go YAML schema 直接解析、校验的部署语义；
2. **manifest 声明参数**：值仍会变成环境变量，但参数名、默认值、类型、敏感性和变更
   策略由 module 声明，`config list/set/explain/plan` 能识别它。它不等同于任意 raw env。

## 原生结构化字段

| YAML 路径 | 说明 | 状态 |
| --- | --- | --- |
| `module_source` | Module 分发 profile：`official`、`official-cn` 或简写 `cn` | 已用于 catalog、下载、缓存、lock 与 CN 默认 |
| `modules.<module>` | 请求启用的 module 及其配置 | 已使用 |
| `global.*` | 14 个部署级参数，详见下一节 | 已使用 |
| `administration.bootstrap.username` | 引导管理员用户名 | 已使用 |
| `administration.local_accounts.username_template` | 本地管理员全局用户名模板 | 非法；用户名由 ANAS 管理 |
| `administration.local_accounts.password_length` | 生成密码长度，最小 16 | 已使用 |
| `identity.directory.provider` | 目录 Provider；当前只接受 `samba_dc` | 已使用 |
| `identity.iam.provider` | IAM Provider | 已使用 |
| `identity.iam.default_protocol` | IAM 默认协议；省略时为 `oidc`，不支持的 Module 按 manifest 回退 | 已使用 |
| `dynamic_dns.provider` | 管理部署自身 DNS 记录的 module 或 `auto` | 已使用 |
| `dynamic_dns.dns_provider` | DNS 厂商 | 已使用 |
| `rollback.snapshot.backend` | 快照后端 | 已使用 |
| `rollback.snapshot.source` | 快照源 | 已使用 |
| `rollback.snapshot.root` | 快照根路径 | 已使用 |
| `rollback.snapshot.keep_auto` | 自动快照保留数 | 已使用 |
| `secrets.<name>` | 用户提供的敏感值；按消费者声明分发 | 已使用，键集合动态 |
| `modules.<module>.enabled` | 启用或禁用服务 | 已使用 |
| `modules.<module>.version` | 精确 Module release，例如 `34.0.2-r4` | `module update` 解析为不可变 OCI digest |
| `modules.<module>.depends_on[]` | 用户追加的依赖 | 已使用 |
| `modules.<module>.identity.login_protocol` | `auto`、`oidc` 或 `saml` | 已使用 |
| `modules.<module>.administration.local_accounts.<id>.username` | 覆盖 Module 本地账户用户名 | 非法；`fixed_username` 优先，否则使用固定 `admin_{module}` |
| `modules.<module>.config.<parameter>` | manifest 声明的 module 参数 | 已使用，见下一节 |
| `env.<KEY>` | 原始环境变量逃生口 | 开放 map，不做键集合校验 |

`config.Load` 使用 `KnownFields(true)`：除 `secrets`、`modules` 和 `env` 这些有意开放的
map 外，拼错结构化字段会直接报错，不会静默忽略。`modules.<module>.config` 虽在 YAML
解码层是 map，但 `config import` 和部署解析会再按 manifest 校验每个键及类型；手写 YAML
不能绕过 `config set` 的声明检查。

未通过安装器选择源时，`module_source` 省略值为 `official`。一行安装脚本会把选择保存到
`${XDG_CONFIG_HOME:-$HOME/.config}/anas/source`；新建 workspace 或导入未声明该字段的外部
配置时会先把此选择固化到受管配置。`cn` 会在托管配置中规范化为 `official-cn`；选择
`official-cn`/`cn` 且未显式设置 `global.chinese_speedup` 时，导入过程会持久化
`global.chinese_speedup: true`。显式 `false` 不会被默认覆盖。

`global.timezone`、`global.default_language` 和 `global.default_locale` 都是可选字段。显式值
会在加载时校验和规范化。省略 `default_locale` 时，Runner 依次使用：包含明确地区的显式
`default_language`、宿主 locale、CLDR likely-subtag 推断、`en-US`。只有 `language.Region()`
返回 `Exact` 时语言才直接成为 locale，因此 `en-GB` 可以推导，`en` 和 `zh-Hans` 不会
覆盖可用的宿主 locale。`config list` 与渲染环境显示最终解析的 BCP 47 值。

语言只控制 UI 文本 fallback，locale 控制区域格式；推导只是默认值来源，不表示两个概念
相同。各 Module 的实际消费边界见 [Module 时区与语言支持矩阵](/reference/module-localization)。

## 已声明的 128 个参数

表中参数都能被 `anas config list` 列出。普通可编辑参数可通过 `anas config set` 设置；
`credential_rotate`、`data_migrate` 和 `immutable` 只用于 inventory/explain，必须执行专用流程。除特别说明
外，全局参数写为 `global.<parameter>`，module 参数写入
`modules.<module>.config.<parameter>`。

| 所有者 | 数量 | 参数 |
| --- | ---: | --- |
| `global` | 14 | `base_domain`, `chinese_build_speedup`, `chinese_speedup`, `container_prefix`, `default_language`, `default_locale`, `dns_server`, `email`, `host_ip`, `ipv4`, `ipv6`, `network_prefix`, `timezone`, `virtual_domain` |
| `authentik` | 6 | `db_name`, `db_type`, `domain_prefix`, `ldap_enabled`, `ldap_password_writeback`, `log_level` |
| `collabora` | 5 | `admin_password`, `admin_username`, `auto_save`, `domain_prefix`, `log_level` |
| `ddns_go` | 10 | `dns_provider`, `domain_prefix`, `interval`, `ipv4_gettype`, `ipv4_interface`, `ipv4_urls`, `ipv6_gettype`, `ipv6_interface`, `ipv6_urls`, `web_enabled` |
| `ddns_updater` | 10 | `dns_provider`, `domain_prefix`, `forward_auth_interface`, `publicip_dns_providers`, `publicip_fetchers`, `publicip_ipv4_providers`, `publicip_ipv6_providers`, `publicip_providers`, `ttl`, `zone_identifier` |
| `eturnal` | 2 | `domain_prefix`, `port` |
| `lam` | 3 | `admin_password`, `domain_prefix`, `language` |
| `lego` | 2 | `dns_provider`, `dns_server` |
| `llng` | 8 | `adminer_enabled`, `db_name`, `db_type`, `domain_prefix`, `enable_test`, `log_level`, `manager_domain_prefix`, `test_domain_prefix` |
| `mariadb` | 2 | `adminer_enabled`, `root_password` |
| `meshcentral` | 5 | `db_name`, `db_type`, `domain_prefix`, `iam_protocol`, `mps_port` |
| `netbird` | 3 | `adminer_enabled`, `domain_prefix`, `iam_protocol` |
| `nextcloud` | 13 | `db_name`, `db_type`, `domain_prefix`, `iam_protocol`, `language`, `locale`, `log_level`, `memories_enabled`, `memory_limit`, `phone_region`, `rm_skeleton_files`, `talk_enabled`, `upload_max_size` |
| `oauth2_proxy` | 3 | `allow_groups`, `domain_prefix`, `iam_protocol` |
| `postgres` | 3 | `adminer_enabled`, `password`, `username` |
| `samba_dc` | 38 | `admin_complex_pass`, `admin_lockout_duration`, `admin_lockout_reset_after`, `admin_lockout_threshold`, `admin_max_pass_age`, `admin_min_pass_age`, `admin_min_pass_length`, `admin_name`, `admin_password`, `admin_password_history`, `administrator_password`, `anchor_bind_name`, `anchor_bind_password`, `anchor_scan_interval`, `app_filter`, `create_structure`, `dns_allowed_networks`, `dns_cache_size`, `dns_debug`, `dns_forwarders`, `ldap_bind_name`, `ldap_bind_password`, `log_level`, `max_log_size`, `netbios_name`, `password_bind_name`, `password_bind_password`, `realm`, `template_homedir`, `template_shell`, `user_complex_pass`, `user_lockout_duration`, `user_lockout_reset_after`, `user_lockout_threshold`, `user_max_pass_age`, `user_min_pass_age`, `user_min_pass_length`, `user_password_history` |
| `samba_fs` | 7 | `hostname`, `log_level`, `share_access_mode`, `share_dir_name`, `share_guest_read_only`, `use_default_domain`, `wsdd_log_level` |
| `traefik` | 2 | `base_port`, `domain_prefix` |

Nextcloud 管理员密码不属于配置参数，必须通过托管 `break_glass` Secret 和应用 handler
管理，不能写入 YAML。

`global.default_service_root_password` 已删除。LAM、Collabora、Samba DC 等 Module 各自
拥有密码参数；省略时分别生成独立 Secret，不再存在跨应用共享管理员密码。

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

## 参数会产生什么结果

`anas config list --json` 是参数名、环境键、默认值和变更结果的权威机器可读清单。
当前 128 项按 effect 统计如下；effect 表示**修改已有部署后必须完成的动作**，不是参数
传输到 `.env` 就算应用成功。

| effect | 数量 | 修改结果 |
| --- | ---: | --- |
| `container_recreate` | 88 | 重新渲染，并重建受影响容器或 Compose project |
| `credential_rotate` | 7 | 普通设置和替换导入会被拒绝，必须通过凭据轮换事务同步应用状态与 Secret Store |
| `data_migrate` | 9 | 普通设置和部署激活会被阻断，必须先迁移持久数据、数据库或成员身份 |
| `hot_reload` | 8 | 声明目标是 Samba 管理命令；当前执行器保守地生成新部署并重新 `up` 受影响容器 |
| `image_rebuild` | 1 | 使用 `anas apply --build` 重建镜像后再部署 |
| `immutable` | 3 | 通用 `config set` 不允许修改，必须走替换或域迁移流程 |
| `reconcile` | 12 | 声明目标是应用/API/文件调和；当前执行器通过新部署和容器启动流程完成 |

### 当前版本的实际执行边界

effect 是期望的生命周期语义，`executor` 才是当前版本实际会走的执行路径。当前尚未实现
通用的 module `config_apply` Hook，因此 `hot_reload` 和 `reconcile` 都使用
`deployment_apply_fallback`；测试不会把它们误报成已经完成原地热加载。

`deployment_apply_fallback` 不是新的 effect，而是 Runner 返回的“实际执行器”名称：当
workspace 已有运行中的 active deployment 时，`config set` 会先保存参数、重新渲染一个
不可变 deployment，再按 render digest 选择发生变化的 Module 执行 Compose `up`。这条
保守路径可能重建容器，所以不等同于 `hot_reload`。相同值仍会生成 deployment 记录，但
不会选择容器执行 `up`；若 activation 失败，Runner 会重启上一 deployment，并恢复
`config.yml` 及 managed digest。若 workspace 尚未首次 apply、已 stop，或显式要求 defer，
参数只会分别进入 pending/deferred 状态，不会擅自启动容器。

| effect | 当前可观察执行结果 |
| --- | --- |
| `container_recreate` | 写入配置，生成并激活新部署，对 render digest 变化的 Module 执行 Compose `up`；真实 Docker 生命周期测试还要求容器 ID 改变 |
| `hot_reload`, `reconcile` | 写入并渲染目标值，生成新部署，只对受影响 Module 执行 Compose `up`，不调用 Compose `build` |
| `image_rebuild` | 生成新部署，先对 Module 执行 Compose `build`，再 `up`；deployment manifest 记录 `images_built: true` |
| `credential_rotate` | `config set` 和用不同值重新 import 都被拒绝；Secret Store 与运行时保持不变 |
| `data_migrate` | 可以生成候选配置，但普通 activation 在执行任何 Compose/宿主网络变更前阻断，并报告参数和迁移操作 |
| `immutable` | 与 `data_migrate` 一样在运行时边界前阻断，并报告替换/域迁移操作 |

### 原地更新能力审计

以下结论回答“上游服务是否具备原地更新能力”，不表示当前 Runner 已经调用这些接口。
2026-08-15 在 ln 的隔离 Docker daemon 上执行
`test-env/scripts/server-parameter-inplace-e2e.sh`：测试在修改前后比较容器 ID、读取应用
实际状态，并在结束时恢复原值。当前 `config set` 仍走上一节所述 deployment fallback。

| 参数 | 原地完成完整效果 | 检测结果 |
| --- | --- | --- |
| `samba_dc.user_*` 8 项 `hot_reload` | 可以 | `samba-tool domain passwordsettings set/show` 实际修改并恢复 8 项策略，Samba DC 容器 ID 不变 |
| `global.default_language` | 可以 | Nextcloud 通过 `occ` 更新；LAM 在 Apache 运行时重写 profile，两个容器均不需要重建；handler 必须与启动期配置写入串行化 |
| `global.default_locale` | 可以 | 当前消费者 Nextcloud 可通过 `occ config:system:set` 在线更新，容器 ID 不变 |
| `nextcloud.language`, `nextcloud.locale` | 可以 | `occ` 写入的实际系统配置可立即读回，容器 ID 不变 |
| `samba_fs.share_access_mode`, `samba_fs.share_guest_read_only` | 可以，但需新 handler | `smbcontrol all reload-config` 使运行中 `smbd` 发布新配置，ACL 同时在线修改；现有 `fix_perm.sh` 还不能接收新 deployment 的显式输入 |
| `nextcloud.memories_enabled` | 有条件可以 | Nextcloud 支持在线 app enable/disable；启用时还可能下载固定版本、修改数据库并启动 places 初始化，必须实现事务、进度与 verify，不能只执行一个 `occ app:enable` |
| `authentik.domain_prefix`, `llng.domain_prefix`, `nextcloud.domain_prefix` | 不可以 | 完整效果包含 Traefik Docker label；Docker 24.0.7 的 `docker update` 不支持修改容器 label，必须至少重建带路由的容器，并同步 IAM client/metadata |
| `lego.dns_provider` | 不可以 | 可以为一次 `docker exec` 临时传入新 provider，但 PID 1 的 cron 环境仍保留旧值，后续续期会退回旧 provider |
| `global.virtual_domain` | 不可以 | Lego 长期进程仍持有旧模式，而且证书替换后各 TLS 消费者还需各自 reload/recreate；不能靠一次证书脚本完成全局效果 |

因此可以为前六组实现专用 `config_apply` handler；后三组域名、DNS provider 与证书模式
不能标成“容器完全不重建”。`reconcile` 只表示需要收敛外部状态，不天然等于热更新。

下面按所有者整理“输入最终改变什么”。同一行中列出的每个参数都已由
`internal/runner` 的消费者审计覆盖；动态拼接的键必须列入带理由的显式例外。仅由上游
镜像读取的参数也必须给出精确上游证据。目前没有参数仅凭 `env_file` 透传就被认定有效：
Collabora、Nextcloud 和数据库镜像的保留设置都在 Hook、容器脚本或 Compose 中有显式
转换/映射。

| 所有者 | 参数 | 可观察结果 / 消费边界 |
| --- | --- | --- |
| `global` | `base_domain`, `virtual_domain`, `email` | 派生应用 URL、AD realm、ACME/内部 CA 模式和服务联系邮箱 |
| `global` | `container_prefix`, `network_prefix` | 改变 Compose 容器名、网络名和跨容器地址 |
| `global` | `host_ip`, `dns_server`, `ipv4`, `ipv6` | 改变宿主路由目标、容器 DNS 和 DDNS A/AAAA 意图 |
| `global` | `timezone`, `default_language`, `default_locale` | 生成最终 `TZ`/BCP 47 默认值；只下发给声明支持的应用 |
| `global` | `chinese_speedup`, `chinese_build_speedup` | 分别切换运行时镜像/下载源和构建期镜像/包源；后者要求重建镜像 |
| `authentik` | `db_name`, `db_type`, `domain_prefix`, `ldap_enabled`, `ldap_password_writeback`, `log_level` | 选择数据库资源，生成 IAM URL/Blueprint、LDAP source/writeback 和日志级别 |
| `collabora` | `admin_username`, `admin_password`, `auto_save`, `domain_prefix`, `log_level` | 显式映射到上游 `username`/`password`，并生成 `extra_params`、server name 和 Traefik route |
| `ddns_go` | `dns_provider`, `domain_prefix`, `interval`, `ipv4_gettype`, `ipv4_interface`, `ipv4_urls`, `ipv6_gettype`, `ipv6_interface`, `ipv6_urls`, `web_enabled` | 生成 ddns-go desired-state 文件、轮询参数、地址发现方式、本地登录和 host-network 路由 |
| `ddns_updater` | `dns_provider`, `domain_prefix`, `forward_auth_interface`, `publicip_fetchers`, `publicip_providers`, `publicip_ipv4_providers`, `publicip_ipv6_providers`, `publicip_dns_providers`, `ttl`, `zone_identifier` | 生成 updater JSON、公共地址探测器、DNS provider/Cloudflare zone 配置和受 IAM gate 保护的路由 |
| `eturnal` | `domain_prefix`, `port` | 生成 TURN 域名、监听/发布端口及 Nextcloud Talk 连接值 |
| `lam` | `admin_password`, `domain_prefix`, `language` | 生成 LAM profile、管理登录、路由和受支持的 POSIX locale |
| `lego` | `dns_provider`, `dns_server` | 设置 ACME DNS-01 provider 和 resolver；证书模式由 `global.virtual_domain` 控制 |
| `llng` | `adminer_enabled`, `db_name`, `db_type`, `domain_prefix`, `enable_test`, `log_level`, `manager_domain_prefix`, `test_domain_prefix` | 选择数据库/可选 Adminer，生成 Portal、Manager、Test 路由和 LLNG 配置 |
| `mariadb` | `adminer_enabled`, `root_password` | 改变可选 service；密码转换为上游初始化变量并受轮换策略保护 |
| `meshcentral` | `db_name`, `db_type`, `domain_prefix`, `iam_protocol`, `mps_port` | 生成数据库、OIDC/LDAP 配置、Web route 和 MPS 发布端口 |
| `netbird` | `adminer_enabled`, `domain_prefix`, `iam_protocol` | 改变可选 service、NetBird URL 和 Runner 选择的 IAM interface |
| `nextcloud` | `db_name`, `db_type`, `domain_prefix`, `iam_protocol`, `language`, `locale`, `log_level`, `memories_enabled`, `memory_limit`, `phone_region`, `rm_skeleton_files`, `talk_enabled`, `upload_max_size` | 生成安装/数据库环境，并由 `task.sh` 调和认证、locale、应用开关、PHP 限额和 skeleton 状态 |
| `oauth2_proxy` | `allow_groups`, `domain_prefix`, `iam_protocol` | 生成 OIDC client、允许组、回调 URL 和 Traefik ForwardAuth middleware |
| `postgres` | `adminer_enabled`, `password`, `username` | 改变可选 service；账号转换为上游初始化变量，并分别标记迁移/轮换语义 |
| `samba_dc` | 表中 38 项 | 生成 AD/BIND 配置、目录结构与三类 service account；普通用户域策略与管理员 PSO 均由参数生成，并声明为 `samba-tool` 热更新，但当前版本通过 deployment fallback 应用，身份/密码项走迁移或轮换保护 |
| `samba_fs` | `hostname`, `log_level`, `share_access_mode`, `share_dir_name`, `share_guest_read_only`, `use_default_domain`, `wsdd_log_level` | 生成 member join、smb.conf、共享目录/ACL、guest 状态和 WSDD 广播 |
| `traefik` | `base_port`, `domain_prefix` | 改变入口监听/发布端口、应用派生 URL 和 dashboard router |

## 本轮删除的无效参数

静态消费者审计、渲染结果和上游镜像入口共同确认以下字段不会产生声明中的效果，因此
直接删除，而不是继续把它们留作“兼容占位”。旧配置出现这些结构化字段时会明确报错；
顶层 `env:` 仍是开放逃生口，但写入同名废弃环境键不属于受支持接口。

上游核对使用 [Collabora CODE 官方 SDK 手册](https://sdk.collaboraonline.com/CO-SDK-manual.pdf)
列出的容器环境变量，以及 Nextcloud 官方镜像的
[环境变量文档](https://github.com/nextcloud/docker/blob/master/README.md#auto-configuration-via-environment-variables)
和 [entrypoint 源码](https://github.com/nextcloud/docker/blob/master/docker-entrypoint.sh)。

| 已删除字段 | 原因 | 替代方式 |
| --- | --- | --- |
| `global.basicauth_user` | 只生成无人读取的 `BASICAUTH_USER`；Traefik 已使用托管本地账户 | `anas admin ...` 管理 Traefik 本地账户 |
| `global.image_prefix` | 只生成无人读取的 `IMAGE_PREFIX`；Compose 镜像均使用固定名称和 `ANAS_IMAGE_REGISTRY` | 需要 registry mirror 时设置 `ANAS_IMAGE_REGISTRY` |
| `global.default_service_root_password` | schema 早已删除，但一个远端 E2E fixture 仍携带该字段并会在 import 时失败 | 使用各 Module 自己的密码参数或托管 Secret |
| `collabora.interface` | 只生成 `COLLABORA_INTERFACE`；Hook、Compose 和上游公开环境变量契约都不读取它 | 无；网络接口由容器网络管理 |
| `lego.virtual_domain` | 生成错误命名空间 `LEGO_VIRTUAL_DOMAIN`，证书逻辑只读取全局键 | `global.virtual_domain` |
| `nextcloud.debug` | 只生成 `NEXTCLOUD_DEBUG`；自有入口和上游公开环境变量契约都不读取它 | 使用 `nextcloud.log_level`；若以后实现 debug mode，需新增真实调和逻辑 |
| `traefik.subnet`, `traefik.gateway_ip` | 未声明且 Compose 已改为由 Docker 分配子网；6 个远端 fixture 中的值不会参与渲染 | 无；避免固定 subnet 冲突 |
| `administration.bootstrap.display_name` | schema 接收后从未进入任何账户创建流程 | 当前不支持 |
| `administration.bootstrap.email` | schema 接收后从未进入任何账户创建流程 | 服务联系邮箱使用 `global.email`；管理员邮箱当前不支持 |
| `administration.bootstrap.roles[]` | schema 接收后从未映射为 AD/application group | 通过目录组管理权限 |
| `administration.local_accounts.password_policy` | 只允许唯一常量且运行时不分支，设置与省略结果完全相同 | 固定采用 per-module 托管密码；长度由 `password_length` 控制 |

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

验证命令：

```sh
go test ./internal/config ./internal/runner
test-env/scripts/test-parameters.sh
test-env/scripts/test-parameter-effects.sh
test-env/scripts/test-render.sh
```

前者拒绝没有运行时消费者的声明参数；若消费者只存在于上游镜像，例外必须同时记录固定
版本的上游源码证据。其余测试分别验证 128 项 inventory/废弃路径、七类 effect 的真实
CLI→Hook→render→deployment→Compose/阻断边界，以及全部 128 个参数键在本轮新生成的
Module 部署产物中至少出现一次。`test-lifecycle.sh` 在真实 Docker 上进一步验证
`container_recreate` 会更换容器 ID。
