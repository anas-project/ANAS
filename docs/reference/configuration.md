# `config.yml` 结构化配置与环境变量清单

本文回答两个问题：哪些设置已经有 `config.yml` 的结构化入口，哪些设置目前只能写入
顶层 `env:`。本页的 inventory 统计由当前工作树生成，不是固定 ABI。

## 结论

<!-- generated:configuration-summary:start -->
- 内置 Module：`22`
- 已声明参数：共 `173` 个（全局 `17` 个、Module 所有 `156` 个；结构化 Module 参数 `152` 个、裸 `env.*` 参数 `4` 个）
- 解析阶段：`input_required` `2` 个、`must_resolve` `26` 个、未知类型 `0` 个
- 类型分布：`bool` `25`、`enum` `22`、`int` `27`、`string` `99`
- 默认值来源分布：`generated` `10`、`host` `3`、`inherited` `7`、`none` `8`、`runtime` `4`、`static` `141`
<!-- generated:configuration-summary:end -->
- `modules`、`administration`、`identity`、`dynamic_dns`、`rollback` 的控制字段
  和 `secrets` 也有结构化 schema，但它们不是“参数到环境变量”的映射，因此不计入
  上述参数 inventory。
- 顶层 `env:` 是开放的 raw-env 逃生口，任意合法环境变量键都能写入（输入会规范为大写，
  并须匹配 `[A-Z_][A-Z0-9_]*`），所以“只能用环境变量”的总数
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
| `global.*` | 部署级参数，详见下一节 | 已使用 |
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
| `env.<KEY>` | 原始环境变量逃生口 | 开放 map；不限制键集合，但键必须匹配 `[A-Z_][A-Z0-9_]*` |

`config.Load` 使用 `KnownFields(true)`：除 `secrets`、`modules` 和 `env` 这些有意开放的
map 外，拼错结构化字段会直接报错，不会静默忽略。`modules.<module>.config` 虽在 YAML
解码层是 map，但 `config import` 和部署解析会再按 manifest 校验每个键及类型；手写 YAML
不能绕过 `config set` 的声明检查。

`config import` 在验证和写入受管配置前统一 YAML 地址：`env` 和 `secrets` 键转换为规范的
运行时大写拼写，Module 名、global 参数名和 Module 参数名转换为小写。若两个源键规范化后
落到同一地址（例如 `env.custom_key` 与 `env.CUSTOM_KEY`、`secrets.demo_token` 与
`secrets.DEMO_TOKEN`，或 `modules.TRAEFIK` 与 `modules.traefik`），导入会拒绝整个文件，
而不是按顺序覆盖。映射到同一运行时键的 `secrets`、`env` 和结构化 Module 参数也不能同时
出现。声明为 bare export 的结构化 Module 参数会迁移到其唯一受管地址；例如源文件中的
`modules.samba_fs.config.share_dir_name` 会写成 `env.SHARE_DIR_NAME`。若源文件同时提供这
两个地址，同样按规范化碰撞拒绝。源文件自身不被修改。

`secrets.<KEY>` 是运行时输入的敏感拼写，不是绕过 schema 的无类型通道。键映射到已声明
参数时，导入仍应用该参数的类型、constraints、敏感性和变更 effect，且错误不回显候选值。
用于选择 provider、interface、backend 或 DNS platform 的结构 selector 不能放在 `secrets:` 或
lifecycle Secret Store；因为其规范标识必须写入 plan/resolution lock，导入与 plan 会安全拒绝并
要求迁到普通配置。secret 通道只保存实际凭据。
普通部署 Secret 保留在权限为 0600 的受管 `config.yml`；`credential_rotate` 输入和本地管理
员 bootstrap 密码则抽取到 `.anas/secrets.yml`。重新导入已规范化配置时，只有 Secret Store
中 kind 为 `lifecycle_managed` 的现有非空值会在私有校验视图中满足调用方输入要求；
`generated` 和 `local_admin` 记录不能冒充这类输入。相同值重复导入是幂等的，不同值替换会
被拒绝并要求走对应轮换流程。`config plan` 按当前 schema 重新校验同一私有视图，`config
list` 只显示这类值已设置；两者都不投影明文。

统一 schema 边界不只用于 `config set`。当前 set、import/reimport、`config plan`、deployment
lock/plan/materialize 和 remote lock 都先构造同一个 registry-aware runtime view，再执行地址
唯一性、环境键语法、声明、类型、constraints 和 caller-input 校验。失败发生在受管文件、
完整性摘要、Secret Store 或 lock 替换之前；`--update-lock` 也不能先写入一个随后会被 schema
拒绝的 lock。所有 Secret Store kind 的值都只在内存中为等值 alias 传播敏感来源，因此即使
Module 升级时遗漏了 `sensitive` 标记，错误和普通投影仍不会回显旧明文；只有
`lifecycle_managed` 被合入有效输入视图。

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

`global.container_prefix` 的默认值为 `anas_`。配置中省略该字段，或导出配置时因其等于
默认值而不写出，都不会改变最终值。Runner 使用 `<container_prefix><module>` 作为 Docker
Compose project name，同时以该前缀生成容器名；例如默认配置下 Nextcloud 的 project 为
`anas_nextcloud`。同一 Docker daemon 上的不同 workspace 必须使用不同前缀。修改已有部署
的前缀会生成另一组 project、容器名和跨容器地址，属于静态部署变更，不是现有资源的原地
重命名；修改前必须先完成显式迁移或清理旧部署。

## 已声明参数

表中参数都能被 `anas config list` 列出。普通可编辑参数可通过 `anas config set` 设置；
`credential_rotate`、`data_migrate` 和 `immutable` 只用于 inventory/explain，必须执行专用流程。除特别说明
外，全局参数写为 `global.<parameter>`，module 参数写入
`modules.<module>.config.<parameter>`。

JSON 清单中的 `type` 取 `string`、`bool`、`int` 或 `enum`；`enum` 同时提供
`allowed_values`。`unknown` 仅为旧 Module 或开发中声明不完整时保留的兼容值，内置 Module
的 release 校验不允许它出现；当前数量见生成摘要。

配置元数据把“操作者是否必须输入”和“解析后的值是否必须存在”分开：

- `required` 是兼容字段，值始终等于 `input_required`。只有无法由默认值、宿主探测或其他
  无条件来源补足，且在所有适用场景都要求操作者提供非空值时，`input_required` 才为
  `true`；Module 参数的适用范围从该 Module 启用时开始；
- `must_resolve` 表示规范化并应用默认值、宿主探测和运行时来源后，最终值仍必须为非空。
  因而它可以在 `input_required: false` 时为 `true`；
- `has_default` 区分“没有静态默认值”和“默认值明确为空字符串”，`default_source` 取
  `none`、`static`、`host`、`runtime`、`generated` 或 `inherited`，说明省略输入时可用的
  无条件来源；输入必填项不得同时声明默认值或非 `none` 来源。`none` 只表示没有无条件
  来源，不排除 deployment resolver 在满足条件时注入值；
- `constraints` 是可选的单字段约束对象，只投影已声明的 `minimum`、`maximum`、
  `min_length`、`max_length`、`pattern` 和 `format`。条件必填、字段间关系以及需要读取其他
  运行态的规则仍由 resolver、应用层、plan 或 Hook 校验，不能压成单字段 `required` 或约束。

这份 inventory **不是 JSON Schema**。这里的 `required` 描述当前参数是否需要显式输入，
不是对象属性名数组；这里的默认值会由 ANAS 解析流程应用，而 JSON Schema 的 `default`
只是注解；`constraints` 也只是统一配置 schema 的稳定投影，不承诺接受任意 JSON Schema
关键字。CLI、未来的 `anasd` 配置 API 和 Web 表单必须消费同一份应用层 schema。

`ddns_go.dns_provider` 与 `ddns_updater.dns_provider` 可由 deployment
`dynamic_dns.dns_provider` 条件注入，因此表现为 `input_required: false`、
`must_resolve: true`、`default_source: none`；只有 resolver 未能注入时才需要 Module 侧输入。

<!-- generated:configuration-constraints:start -->
当前显式可移植约束：`23` 项。

| 参数路径 | 可移植约束 |
| --- | --- |
| `casdoor.ldap_auto_sync_minutes` | <code>minimum=1</code> |
| `eturnal.port` | <code>minimum=1; maximum=65535</code> |
| `forgejo.actions_incus_profile` | <code>min_length=1; max_length=63; pattern=&#34;^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$&#34;</code> |
| `forgejo.actions_runner_image` | <code>pattern=&#34;^(?:[0-9a-f]{64})?$&#34;</code> |
| `forgejo.domain_prefix` | <code>min_length=1; max_length=63; pattern=&#34;^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$&#34;</code> |
| `forgejo.ssh_port` | <code>minimum=1; maximum=65535</code> |
| `global.base_domain` | <code>format=&#34;dns_name&#34;</code> |
| `global.default_language` | <code>format=&#34;language_tag&#34;</code> |
| `global.default_locale` | <code>format=&#34;locale&#34;</code> |
| `global.host_ip` | <code>format=&#34;ipv4&#34;</code> |
| `global.host_lan_bridge_ip` | <code>format=&#34;ipv4&#34;</code> |
| `global.host_lan_ip` | <code>format=&#34;ipv4&#34;</code> |
| `global.timezone` | <code>format=&#34;iana_timezone&#34;</code> |
| `meshcentral.mps_port` | <code>minimum=1; maximum=65535</code> |
| `oauth2_proxy.console_proxy_port` | <code>minimum=1; maximum=65535</code> |
| `samba_dc.domain` | <code>format=&#34;dns_name&#34;</code> |
| `samba_dc.max_log_size` | <code>minimum=1</code> |
| `traefik.base_port` | <code>minimum=1; maximum=65535</code> |
| `versitygw.domain_prefix` | <code>min_length=1; max_length=63; pattern=&#34;^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$&#34;</code> |
| `versitygw.region` | <code>min_length=1; max_length=64; pattern=&#34;^[A-Za-z0-9][A-Za-z0-9._-]*$&#34;</code> |
| `versitygw.root_access_key` | <code>min_length=3; max_length=64; pattern=&#34;^[A-Za-z0-9._-]+$&#34;</code> |
| `versitygw.root_secret_key` | <code>min_length=16; max_length=128</code> |
| `vikunja.domain_prefix` | <code>min_length=1; max_length=63; pattern=&#34;^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$&#34;</code> |
<!-- generated:configuration-constraints:end -->

<!-- generated:configuration-owners:start -->
| Owner | 参数数 | 参数路径 |
| --- | ---: | --- |
| `global` | 17 | `global.base_domain`<br>`global.chinese_build_speedup`<br>`global.chinese_speedup`<br>`global.container_prefix`<br>`global.default_language`<br>`global.default_locale`<br>`global.dns_server`<br>`global.email`<br>`global.host_ip`<br>`global.host_lan_arp_check`<br>`global.host_lan_bridge_ip`<br>`global.host_lan_ip`<br>`global.ipv4`<br>`global.ipv6`<br>`global.network_prefix`<br>`global.timezone`<br>`global.virtual_domain` |
| `authentik` | 6 | `authentik.db_name`<br>`authentik.db_type`<br>`authentik.domain_prefix`<br>`authentik.ldap_enabled`<br>`authentik.ldap_password_writeback`<br>`authentik.log_level` |
| `casdoor` | 4 | `casdoor.db_name`<br>`casdoor.db_type`<br>`casdoor.domain_prefix`<br>`casdoor.ldap_auto_sync_minutes` |
| `collabora` | 5 | `collabora.admin_password`<br>`collabora.admin_username`<br>`collabora.auto_save`<br>`collabora.domain_prefix`<br>`collabora.log_level` |
| `ddns_go` | 10 | `ddns_go.dns_provider`<br>`ddns_go.domain_prefix`<br>`ddns_go.interval`<br>`ddns_go.ipv4_gettype`<br>`ddns_go.ipv4_interface`<br>`ddns_go.ipv4_urls`<br>`ddns_go.ipv6_gettype`<br>`ddns_go.ipv6_interface`<br>`ddns_go.ipv6_urls`<br>`ddns_go.web_enabled` |
| `ddns_updater` | 10 | `ddns_updater.dns_provider`<br>`ddns_updater.domain_prefix`<br>`ddns_updater.forward_auth_interface`<br>`ddns_updater.publicip_dns_providers`<br>`ddns_updater.publicip_fetchers`<br>`ddns_updater.publicip_ipv4_providers`<br>`ddns_updater.publicip_ipv6_providers`<br>`ddns_updater.publicip_providers`<br>`ddns_updater.ttl`<br>`ddns_updater.zone_identifier` |
| `eturnal` | 2 | `eturnal.domain_prefix`<br>`eturnal.port` |
| `forgejo` | 16 | `forgejo.actions_allowed_scopes`<br>`forgejo.actions_enabled`<br>`forgejo.actions_incus_client_cert_b64`<br>`forgejo.actions_incus_client_key_b64`<br>`forgejo.actions_incus_endpoint`<br>`forgejo.actions_incus_profile`<br>`forgejo.actions_incus_server_cert_b64`<br>`forgejo.actions_runner_image`<br>`forgejo.custom_git_hooks_enabled`<br>`forgejo.db_name`<br>`forgejo.db_type`<br>`forgejo.domain_prefix`<br>`forgejo.iam_protocol`<br>`forgejo.language`<br>`forgejo.local_path_import_enabled`<br>`forgejo.ssh_port` |
| `lam` | 3 | `lam.admin_password`<br>`lam.domain_prefix`<br>`lam.language` |
| `lego` | 2 | `lego.dns_provider`<br>`lego.dns_server` |
| `llng` | 7 | `llng.db_name`<br>`llng.db_type`<br>`llng.domain_prefix`<br>`llng.enable_test`<br>`llng.log_level`<br>`llng.manager_domain_prefix`<br>`llng.test_domain_prefix` |
| `mariadb` | 3 | `mariadb.adminer_enabled`<br>`mariadb.forward_auth_interface`<br>`mariadb.root_password` |
| `meshcentral` | 5 | `meshcentral.db_name`<br>`meshcentral.db_type`<br>`meshcentral.domain_prefix`<br>`meshcentral.iam_protocol`<br>`meshcentral.mps_port` |
| `netbird` | 2 | `netbird.domain_prefix`<br>`netbird.iam_protocol` |
| `nextcloud` | 13 | `nextcloud.db_name`<br>`nextcloud.db_type`<br>`nextcloud.domain_prefix`<br>`nextcloud.iam_protocol`<br>`nextcloud.language`<br>`nextcloud.locale`<br>`nextcloud.log_level`<br>`nextcloud.memories_enabled`<br>`nextcloud.memory_limit`<br>`nextcloud.phone_region`<br>`nextcloud.rm_skeleton_files`<br>`nextcloud.talk_enabled`<br>`nextcloud.upload_max_size` |
| `oauth2_proxy` | 4 | `oauth2_proxy.console_proxy_enabled`<br>`oauth2_proxy.console_proxy_port`<br>`oauth2_proxy.domain_prefix`<br>`oauth2_proxy.iam_protocol` |
| `postgres` | 4 | `postgres.adminer_enabled`<br>`postgres.forward_auth_interface`<br>`postgres.password`<br>`postgres.username` |
| `samba_dc` | 40 | `samba_dc.admin_complex_pass`<br>`samba_dc.admin_lockout_duration`<br>`samba_dc.admin_lockout_reset_after`<br>`samba_dc.admin_lockout_threshold`<br>`samba_dc.admin_max_pass_age`<br>`samba_dc.admin_min_pass_age`<br>`samba_dc.admin_min_pass_length`<br>`samba_dc.admin_name`<br>`samba_dc.admin_password`<br>`samba_dc.admin_password_history`<br>`samba_dc.administrator_password`<br>`samba_dc.anchor_bind_name`<br>`samba_dc.anchor_bind_password`<br>`samba_dc.anchor_scan_interval`<br>`samba_dc.app_filter`<br>`samba_dc.application_dns_mode`<br>`samba_dc.create_structure`<br>`samba_dc.dns_allowed_networks`<br>`samba_dc.dns_cache_size`<br>`samba_dc.dns_debug`<br>`samba_dc.dns_forwarders`<br>`samba_dc.domain`<br>`samba_dc.ldap_bind_name`<br>`samba_dc.ldap_bind_password`<br>`samba_dc.log_level`<br>`samba_dc.max_log_size`<br>`samba_dc.netbios_name`<br>`samba_dc.password_bind_name`<br>`samba_dc.password_bind_password`<br>`samba_dc.realm`<br>`samba_dc.template_homedir`<br>`samba_dc.template_shell`<br>`samba_dc.user_complex_pass`<br>`samba_dc.user_lockout_duration`<br>`samba_dc.user_lockout_reset_after`<br>`samba_dc.user_lockout_threshold`<br>`samba_dc.user_max_pass_age`<br>`samba_dc.user_min_pass_age`<br>`samba_dc.user_min_pass_length`<br>`samba_dc.user_password_history` |
| `samba_fs` | 7 | `env.SHARE_ACCESS_MODE`<br>`env.SHARE_DIR_NAME`<br>`env.SHARE_GUEST_READ_ONLY`<br>`env.USE_DEFAULT_DOMAIN`<br>`samba_fs.hostname`<br>`samba_fs.log_level`<br>`samba_fs.wsdd_log_level` |
| `traefik` | 3 | `traefik.base_port`<br>`traefik.domain_prefix`<br>`traefik.forwarded_headers_trusted_ips` |
| `versitygw` | 5 | `versitygw.domain_prefix`<br>`versitygw.read_only`<br>`versitygw.region`<br>`versitygw.root_access_key`<br>`versitygw.root_secret_key` |
| `vikunja` | 5 | `vikunja.db_name`<br>`vikunja.db_type`<br>`vikunja.domain_prefix`<br>`vikunja.iam_protocol`<br>`vikunja.language` |
<!-- generated:configuration-owners:end -->

Nextcloud 管理员密码不属于配置参数，必须通过托管 `break_glass` Secret 和应用 handler
管理，不能写入 YAML。

`global.default_service_root_password` 已删除。LAM、Collabora、Samba DC 等 Module 各自
拥有密码参数；省略时分别生成独立 Secret，不再存在跨应用共享管理员密码。

`samba_fs` 的以下声明项是特殊情况。它们有 manifest 元数据，不能算“未声明 raw env”，
但因为 `config.exports` 要求发布裸键，受管配置中的 canonical YAML 地址只能是顶层
`env:`；`config import` 可接受结构化源地址，但会在持久化前迁移到这里：

<!-- generated:configuration-bare-parameters:start -->
| YAML 地址 | 所有者 | 环境变量 |
| --- | --- | --- |
| `env.SHARE_ACCESS_MODE` | `samba_fs` | `SHARE_ACCESS_MODE` |
| `env.SHARE_DIR_NAME` | `samba_fs` | `SHARE_DIR_NAME` |
| `env.SHARE_GUEST_READ_ONLY` | `samba_fs` | `SHARE_GUEST_READ_ONLY` |
| `env.USE_DEFAULT_DOMAIN` | `samba_fs` | `USE_DEFAULT_DOMAIN` |
<!-- generated:configuration-bare-parameters:end -->

例如 `anas config set samba_fs.share_guest_read_only Yes` 接受逻辑参数路径，但写回文件时
会落到 `env.SHARE_GUEST_READ_ONLY`。

## 参数会产生什么结果

`anas config list --json` 是参数名、环境键、默认值和变更结果的权威机器可读清单。

<!-- generated:configuration-effects:start -->
| Module 参数 effect | 参数数 | 修改结果 |
| --- | ---: | --- |
| `container_recreate` | 103 | 重新渲染，并重建受影响容器或 Compose project |
| `credential_rotate` | 7 | 通过凭据轮换事务同步应用状态与 Secret Store |
| `data_migrate` | 15 | 激活前迁移持久数据、数据库或成员身份 |
| `hot_reload` | 16 | 通过声明的管理命令应用；当前执行器可能保守地重建容器 |
| `immutable` | 3 | 使用替换或专用迁移流程 |
| `reconcile` | 12 | 通过 Module 生命周期调和应用、API 或文件状态 |
<!-- generated:configuration-effects:end -->

### 当前版本的实际执行边界

effect 是期望的生命周期语义，`executor` 才是当前版本实际会走的执行路径。当前尚未实现
通用的 module `config_apply` Hook，因此 `hot_reload` 和 `reconcile` 都使用
`deployment_apply_fallback`；测试不会把它们误报成已经完成原地热加载。

`deployment_apply_fallback` 不是新的 effect，而是 Runner 返回的“实际执行器”名称：当
workspace 已有运行中的 active deployment 时，`config set` 会先保存参数、重新渲染一个
不可变 deployment，再按 render digest 选择发生变化的 Module，对旧制品执行 Compose `down`
并从目标制品执行 `up`。这条保守路径会重建容器，所以不等同于 `hot_reload`。相同值仍会生成
deployment 记录，但不会选择容器执行 `down/up`；若 activation 失败，Runner 会先删除 candidate
容器，再启动上一 deployment，并恢复
`config.yml` 及 managed digest。若 workspace 尚未首次 apply、已 stop，或显式要求 defer，
参数只会分别进入 pending/deferred 状态，不会擅自启动容器。

| effect | 当前可观察执行结果 |
| --- | --- |
| `container_recreate` | 写入配置，生成并激活新部署，对 render digest 变化的 Module 执行 Compose `down → up`；真实 Docker 生命周期测试还要求容器 ID 改变 |
| `hot_reload`, `reconcile` | 写入并渲染目标值，生成新部署，只对受影响 Module 执行 Compose `down → up`，不调用 Compose `build` |
| `image_rebuild` | 生成新部署，先对 Module 执行 Compose `build`，再对变化 Module 执行 `down → up`；deployment manifest 记录 `images_built: true` |
| `credential_rotate` | `config set` 和用不同值重新 import 都被拒绝；Secret Store 与运行时保持不变 |
| `data_migrate` | 可以生成候选配置，但普通 activation 在执行任何 Compose/宿主网络变更前阻断，并报告参数和迁移操作 |
| `immutable` | 与 `data_migrate` 一样在运行时边界前阻断，并报告替换/域迁移操作 |

### 原地更新能力审计

以下结论回答“上游服务是否具备原地更新能力”，不表示当前 Runner 已经调用这些接口。
2026-08-15 在独立的非生产隔离 Docker daemon 上执行
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
| `authentik.domain_prefix`, `casdoor.domain_prefix`, `llng.domain_prefix`, `nextcloud.domain_prefix` | 不可以 | 完整效果包含 Traefik Docker label；Docker 24.0.7 的 `docker update` 不支持修改容器 label，必须至少重建带路由的容器，并同步 IAM client/metadata |
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
| `global` | `base_domain`, `virtual_domain`, `email` | 派生应用 URL、SSO issuer、ACME/内部 CA 模式和服务联系邮箱；AD 域由 `samba_dc.domain` 独立定义 |
| `global` | `container_prefix`, `network_prefix` | 改变 Compose project/容器名、网络名和跨容器地址；project 为 `<container_prefix><module>`，不同 workspace 不得复用同一前缀 |
| `global` | `host_ip`, `dns_server`, `ipv4`, `ipv6` | 改变宿主路由目标、容器 DNS 和 DDNS A/AAAA 意图 |
| `global` | `host_lan_ip`, `host_lan_bridge_ip`, `host_lan_arp_check` | 固定 host-LAN 容器与宿主桥地址，并控制地址占用探测；三项均为可选，未关闭探测时默认执行 ARP 检查 |
| `global` | `timezone`, `default_language`, `default_locale` | 生成最终 `TZ`/BCP 47 默认值；只下发给声明支持的应用 |
| `global` | `chinese_speedup`, `chinese_build_speedup` | 分别切换运行时镜像/下载源和构建期镜像/包源；后者要求重建镜像 |
| `authentik` | `db_name`, `db_type`, `domain_prefix`, `ldap_enabled`, `ldap_password_writeback`, `log_level` | 选择数据库资源，生成 IAM URL/Blueprint、LDAP source/writeback 和日志级别 |
| `casdoor` | `db_name`, `db_type`, `domain_prefix`, `ldap_auto_sync_minutes` | 选择 PostgreSQL Resource，生成 IAM URL、OIDC/SAML Application、LDAPS 导入配置和同步周期 |
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
| `oauth2_proxy` | `domain_prefix`, `iam_protocol` | 生成 OIDC client、回调 URL 和 Traefik ForwardAuth middleware；放行的组由 `platform_admin` 角色派生，不是参数 |
| `postgres` | `adminer_enabled`, `password`, `username` | 改变可选 service；账号转换为上游初始化变量，并分别标记迁移/轮换语义 |
| `samba_dc` | 表中 40 项 | 由 `domain` 生成 Realm/Base DN/Kerberos 身份，由 `application_dns_mode` 选择应用记录所在权威 zone，并生成 AD/BIND 配置、目录结构与三类 service account；普通用户域策略与管理员 PSO 均由参数生成，并声明为 `samba-tool` 热更新，但当前版本通过 deployment fallback 应用，身份/密码项走迁移或轮换保护 |
| `samba_fs` | `hostname`, `log_level`, `share_access_mode`, `share_dir_name`, `share_guest_read_only`, `use_default_domain`, `wsdd_log_level` | 生成 member join、smb.conf、共享目录/ACL、guest 状态和 WSDD 广播 |
| `traefik` | `base_port`, `domain_prefix`, `forwarded_headers_trusted_ips` | 改变入口监听/发布端口、应用派生 URL、dashboard router 和受信转发代理边界 |
| `vikunja` | `db_name`, `db_type`, `domain_prefix`, `iam_protocol`, `language` | 选择数据库 Resource 与 OIDC 绑定，生成任务服务路由和新用户默认语言 |

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
4. 每个内置参数都必须显式声明类型；`unknown` 只用于读取旧 Module，不能进入 release；
5. CLI/JSON 的 `required` 始终与 manifest `input_required` 相等；输入必填项不得同时有默认值
   或其他无条件来源。manifest 旧字段 `required` 保留 Hook 前校验语义，`must_resolve` 描述
   Hook patch 后的最终非空不变量；
6. 单字段限制声明在 `constraints`；条件必填和跨字段/运行态约束留给 resolver、应用层、
   plan 或 Hook；
7. 不把 runner/module 推导变量列为用户配置；
8. 凭据走 `secrets` 或 manifest 中标记 `sensitive` 的参数。

验证命令：

```sh
go test ./internal/config ./internal/runner
test-env/scripts/test-parameters.sh
test-env/scripts/test-parameter-effects.sh
test-env/scripts/test-render.sh
```

前者拒绝没有运行时消费者的声明参数；若消费者只存在于上游镜像，例外必须同时记录固定
版本的上游源码证据。其余测试分别验证完整 inventory、类型完整性和废弃路径，所有已声明
effect 的真实 CLI→Hook→render→deployment→Compose/阻断边界，以及每个参数键在本轮新生成的
Module 部署产物中至少出现一次。`test-lifecycle.sh` 在真实 Docker 上进一步验证
`container_recreate` 会更换容器 ID。
