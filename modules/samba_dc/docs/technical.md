# Samba domain controller and DNS 技术实现

本文面向 Module 维护者，记录 `samba_dc` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `4.23.6-r13` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

持久目录事件默认还发布 `lockoutTime` 与 `accountExpires` 的属性名，使 IAM 订阅者可以
及时复查锁定和账户到期状态；日志不包含属性值。消费者仍需定期核对时间自然推进造成的到期。
成功追加事件后立即更新健康文件的 `last_published_seq`，不等下一次全量扫描，避免监控序号
落后于真实持久日志。

邮件候选补充 `proxyAddresses`、密码状态、公开联系方式及 `anasMail*` 资源版本/操作字段的属性名；Hook 升级会保留已有自定义列表并补齐这些字段，不包含属性值。配置邮件域时，固定 Mailboxes/Addresses OU 的对象也进入规范事件日志；该事件范围与人员/组锚点写入范围分开，非登录资源的 Add 不触发人员盖章或其重试等待。当前生产者单元验证已覆盖，真实升级及邮件消费者收敛仍待联调。

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `lego` | Module | — |


启动时若 `/etc/localtime` 已指向所选 `TZ` 文件，保留该文件并写入 `/etc/timezone`，避免 Ubuntu UTC 默认软链接导致 `cp` 同文件错误中断目录初始化。

未启用声明能力码的 Module 时，能力组列表可以缺省；结构初始化将其视为空列表，继续建立基础组和服务账号。

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_samba_dc` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc:4.23.6-r13` | `` | 5 |
| `anas_samba_dc_anchor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc-anchor:4.23.6-r13` | `` | 3 |
| `anas_samba_dc_events_init` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-mirror-ubuntu:resolute-678c6550cc43` | `` | 1 |
<!-- generated:compose-topology:end -->

## 配置契约

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `samba_dc.admin_complex_pass` | bool | — | `true` | `static` | `SAMBA_DC_ADMIN_COMPLEX_PASS` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员密码复杂度策略 |
| `samba_dc.admin_lockout_duration` | int | — | `30` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_DURATION` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员锁定时长 |
| `samba_dc.admin_lockout_reset_after` | int | — | `30` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_RESET_AFTER` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员失败计数重置时长 |
| `samba_dc.admin_lockout_threshold` | int | — | `10` | `static` | `SAMBA_DC_ADMIN_LOCKOUT_THRESHOLD` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员锁定阈值 |
| `samba_dc.admin_max_pass_age` | int | — | `0` | `static` | `SAMBA_DC_ADMIN_MAX_PASS_AGE` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员密码最长有效期，0 表示永不过期 |
| `samba_dc.admin_min_pass_age` | int | — | `1` | `static` | `SAMBA_DC_ADMIN_MIN_PASS_AGE` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员密码最短使用期 |
| `samba_dc.admin_min_pass_length` | int | — | `8` | `static` | `SAMBA_DC_ADMIN_MIN_PASS_LENGTH` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员密码最短长度 |
| `samba_dc.admin_name` | string | — | `admin` | `static` | `SAMBA_DC_ADMIN_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 日常目录管理员用户名 |
| `samba_dc.admin_password` | string | — | — | `generated` | `SAMBA_DC_ADMIN_PASSWORD` | 否 | 是 | 是 | 否：`rotate-samba-admin-password` | `credential_rotate` | 管理界面或服务管理员密码 |
| `samba_dc.admin_password_history` | int | — | `2` | `static` | `SAMBA_DC_ADMIN_PASSWORD_HISTORY` | 否 | 否 | 否 | 是 | `hot_reload` | 管理员密码历史数量 |
| `samba_dc.administrator_password` | string | — | — | `generated` | `SAMBA_DC_ADMINISTRATOR_PASSWORD` | 否 | 是 | 是 | 否：`rotate-samba-administrator-password` | `credential_rotate` | 内置 Administrator 密码 |
| `samba_dc.anchor_bind_name` | string | — | `svc_anchor` | `static` | `SAMBA_DC_ANCHOR_BIND_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 身份锚点服务账号名 |
| `samba_dc.anchor_bind_password` | string | — | — | `generated` | `SAMBA_DC_ANCHOR_BIND_PASSWORD` | 否 | 是 | 是 | 否：`rotate-anchor-bind-password` | `credential_rotate` | 身份锚点服务账号密码 |
| `samba_dc.anchor_scan_interval` | int | — | `300` | `static` | `SAMBA_DC_ANCHOR_SCAN_INTERVAL` | 否 | 否 | 否 | 是 | `container_recreate` | 身份锚点补漏扫描间隔 |
| `samba_dc.app_filter` | bool | — | `true` | `static` | `SAMBA_DC_APP_FILTER` | 否 | 否 | 否 | 是 | `container_recreate` | 是否按 APP_* Group 过滤应用用户 |
| `samba_dc.application_dns_mode` | enum (`auto`, `ad_zone`, `separate_zone`) | — | `auto` | `static` | `SAMBA_DC_APPLICATION_DNS_MODE` | 否 | 否 | 否 | 否：`migrate-application-dns-zone` | `data_migrate` | 应用 DNS 权威区模式 |
| `samba_dc.create_structure` | bool | — | `true` | `static` | `SAMBA_DC_CREATE_STRUCTURE` | 否 | 否 | 否 | 是 | `container_recreate` | 是否创建目录 OU/Group 结构 |
| `samba_dc.dns_allowed_networks` | string | — | `""` | `static` | `SAMBA_DC_DNS_ALLOWED_NETWORKS` | 否 | 否 | 否 | 是 | `container_recreate` | 允许递归查询的网络 |
| `samba_dc.dns_cache_size` | string | — | `128M` | `static` | `SAMBA_DC_DNS_CACHE_SIZE` | 否 | 否 | 否 | 是 | `container_recreate` | DNS 缓存上限 |
| `samba_dc.dns_debug` | bool | — | `false` | `static` | `SAMBA_DC_DNS_DEBUG` | 否 | 否 | 否 | 是 | `container_recreate` | DNS 调试开关 |
| `samba_dc.dns_forwarders` | string | — | `""` | `static` | `SAMBA_DC_DNS_FORWARDERS` | 否 | 否 | 否 | 是 | `container_recreate` | 上游 DNS 转发器 |
| `samba_dc.domain` | string | `format: dns_name` | — | `inherited` | `SAMBA_DC_DOMAIN` | 否 | 是 | 否 | 否：`replace-directory-domain` | `immutable` | AD DNS 域（目录身份） |
| `samba_dc.ldap_bind_name` | string | — | `svc_ldap` | `static` | `SAMBA_DC_LDAP_BIND_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 只读 LDAP 服务账号名 |
| `samba_dc.ldap_bind_password` | string | — | — | `generated` | `SAMBA_DC_LDAP_BIND_PASSWORD` | 否 | 是 | 是 | 否：`rotate-ldap-bind-password` | `credential_rotate` | 只读 LDAP 服务账号密码 |
| `samba_dc.log_level` | string | — | `1` | `static` | `SAMBA_DC_LOG_LEVEL` | 否 | 否 | 否 | 是 | `container_recreate` | 日志级别 |
| `samba_dc.mail_address_bind_password` | string | — | — | `generated` | `SAMBA_DC_MAIL_ADDRESS_BIND_PASSWORD` | 否 | 否 | 是 | 否：`rotate-mail-address-bind-password` | `credential_rotate` | 门户受限地址维护服务身份独立生成密码，不授予邮件只读服务 |
| `samba_dc.mail_creator_bind_password` | string | — | — | `generated` | `SAMBA_DC_MAIL_CREATOR_BIND_PASSWORD` | 否 | 否 | 是 | 否：`rotate-mail-creator-bind-password` | `credential_rotate` | 门户受限建号服务身份独立生成密码，不复用地址维护或人员密码 |
| `samba_dc.mail_directory_enabled` | bool | — | `false` | `static` | `SAMBA_DC_MAIL_DIRECTORY_ENABLED` | 否 | 否 | 否 | 否：`bootstrap-mail-directory` | `data_migrate` | 显式启用已确认的非登录邮箱资源引导；必须先验证全量快照并停止外部写入 |
| `samba_dc.mail_domain` | string | `format: dns_name` | — | `inherited` | `SAMBA_DC_MAIL_DOMAIN` | 否 | 是 | 否 | 否：`migrate-default-mail-addresses` | `data_migrate` | 主邮箱后缀；缺省继承 AD 域，变更后维护指定人员 OU 的主地址，不重命名目录身份 |
| `samba_dc.mail_initial_snapshot_id` | string | — | `""` | `static` | `SAMBA_DC_MAIL_INITIAL_SNAPSHOT_ID` | 否 | 否 | 否 | 否：`bootstrap-mail-directory` | `data_migrate` | 首次邮箱资源导入的已验证完整快照引用；容器不能自行证明全主机停写 |
| `samba_dc.mail_initial_source_domain` | string | — | `""` | `static` | `SAMBA_DC_MAIL_INITIAL_SOURCE_DOMAIN` | 否 | 否 | 否 | 否：`bootstrap-mail-directory` | `data_migrate` | 首次导入时已收敛的旧主邮件域；完成后固定，不作为后续目标域 |
| `samba_dc.mail_management_policy` | string | — | `""` | `static` | `SAMBA_DC_MAIL_MANAGEMENT_POLICY` | 否 | 否 | 否 | 是 | `container_recreate` | 完整受管域、保护地址与管理员显式邮箱/别名/容量/限速政策 JSON |
| `samba_dc.max_log_size` | int | `>= 1` | `2048` | `static` | `SAMBA_DC_MAX_LOG_SIZE` | 否 | 否 | 否 | 是 | `container_recreate` | 单个日志文件上限 |
| `samba_dc.netbios_name` | string | — | — | `runtime` | `SAMBA_DC_NETBIOS_NAME` | 否 | 是 | 否 | 否：`replace-domain-controller` | `immutable` | 域控制器 NetBIOS 名称 |
| `samba_dc.password_bind_name` | string | — | `svc_password` | `static` | `SAMBA_DC_PASSWORD_BIND_NAME` | 否 | 否 | 否 | 是 | `container_recreate` | 密码回写服务账号名 |
| `samba_dc.password_bind_password` | string | — | — | `generated` | `SAMBA_DC_PASSWORD_BIND_PASSWORD` | 否 | 是 | 是 | 否：`rotate-password-bind-password` | `credential_rotate` | 密码回写服务账号密码 |
| `samba_dc.realm` | string | — | — | `inherited` | `SAMBA_DC_REALM` | 否 | 是 | 否 | 否：`replace-directory-domain` | `immutable` | 从 AD DNS 域派生的 Realm |
| `samba_dc.template_homedir` | string | — | `/home/%D/%U` | `static` | `SAMBA_DC_TEMPLATE_HOMEDIR` | 否 | 否 | 否 | 是 | `container_recreate` | 目录用户 home 模板 |
| `samba_dc.template_shell` | string | — | `/bin/false` | `static` | `SAMBA_DC_TEMPLATE_SHELL` | 否 | 否 | 否 | 是 | `container_recreate` | 目录用户 shell 模板 |
| `samba_dc.user_complex_pass` | bool | — | `false` | `static` | `SAMBA_DC_USER_COMPLEX_PASS` | 否 | 否 | 否 | 是 | `hot_reload` | 密码复杂度策略 |
| `samba_dc.user_lockout_duration` | int | — | `30` | `static` | `SAMBA_DC_USER_LOCKOUT_DURATION` | 否 | 否 | 否 | 是 | `hot_reload` | 锁定时长 |
| `samba_dc.user_lockout_reset_after` | int | — | `30` | `static` | `SAMBA_DC_USER_LOCKOUT_RESET_AFTER` | 否 | 否 | 否 | 是 | `hot_reload` | 失败计数重置时长 |
| `samba_dc.user_lockout_threshold` | int | — | `10` | `static` | `SAMBA_DC_USER_LOCKOUT_THRESHOLD` | 否 | 否 | 否 | 是 | `hot_reload` | 锁定阈值 |
| `samba_dc.user_max_pass_age` | int | — | `90` | `static` | `SAMBA_DC_USER_MAX_PASS_AGE` | 否 | 否 | 否 | 是 | `hot_reload` | 密码最长有效期 |
| `samba_dc.user_min_pass_age` | int | — | `1` | `static` | `SAMBA_DC_USER_MIN_PASS_AGE` | 否 | 否 | 否 | 是 | `hot_reload` | 密码最短使用期 |
| `samba_dc.user_min_pass_length` | int | — | `8` | `static` | `SAMBA_DC_USER_MIN_PASS_LENGTH` | 否 | 否 | 否 | 是 | `hot_reload` | 密码最短长度 |
| `samba_dc.user_password_history` | int | — | `2` | `static` | `SAMBA_DC_USER_PASSWORD_HISTORY` | 否 | 否 | 否 | 是 | `hot_reload` | 密码历史数量 |

参数库存的权威来源是 `module.yml`；CLI 负责合并默认值、类型、required、环境变量映射、敏感性和变更执行器。技术文档不得另造可设置参数。

## 双域与应用 DNS 计划

`BASE_DOMAIN` 只属于应用/Web 命名空间。`SAMBA_DC_DOMAIN` 属于已 provision 的目录身份，
并派生 `SAMBA_DC_REALM`、`SAMBA_DC_BASE_DN`、`SAMBA_DC_DNS_SEARCH`、
`SAMBA_DC_DC_DOMAIN` 和默认 UPN suffix。`modules.samba_dc.config.domain` 未设置时，
`validateDomainDNSConfig` 为旧配置把有效 AD 域回退到 `BASE_DOMAIN`；这个兼容分支不允许对
已有目录执行重命名。显式 `realm` 必须与 `upper(SAMBA_DC_DOMAIN)` 大小写无关地一致，
calculate 最终发布大写 Realm。

`application_dns_mode` 的 requested 值和解析结果是两层状态：

| Requested | 校验与解析 | Resolved zone |
| --- | --- | --- |
| `auto` | `BASE_DOMAIN == SAMBA_DC_DOMAIN` 或前者是后者的 label 子域时为 `ad_zone`，否则为 `separate_zone` | AD 域或应用域 |
| `ad_zone` | 只接受相等/label 子域关系 | `SAMBA_DC_DOMAIN` |
| `separate_zone` | 除等域外不限制域关系；等域必须使用现有 AD zone，应用域还必须是 ANAS 可完整维护的专用内部命名空间 | `BASE_DOMAIN` |

validate Hook 把 `requested_mode`、`resolved_mode` 和 `zone` 写入非敏感 module plan。文本
`anas plan` 输出 `module plan: samba_dc ...`，JSON 位于
`module_plans.samba_dc`；物化 deployment 后同一份数据保存在
`modules.samba_dc.validation_plan`。calculate 同时发布
`SAMBA_DC_APPLICATION_DNS_MODE`、`SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED` 和
`SAMBA_DC_APPLICATION_DNS_ZONE`，DNS reconciler 不会在运行期重新猜测域关系。

Runner 到 reconciler 的 `DOMAINS` 是内部协议，只收集声明 `features.domain: true` 的 Web
Module，并使用 `inner/<完整 FQDN>/<module>`，例如
`inner/cloud.nas.example.net/nextcloud`。它不包含 `SAMBA_DC_DOMAIN`，也不把 FQDN 截成第一
段。`ad_zone` 会从完整 FQDN 得到多 label owner（例如 `cloud.nas`），`separate_zone` 则在
应用 zone 内得到短 owner（例如 `cloud`）。这些 Web A 记录指向 `HOST_IP`。

LDAPS 服务别名走独立记录：兼容期保持 `SAMBA_DC_HOST=BASE_DOMAIN`，并解析到
`SAMBA_DC_HOST_IP`。它由 Web 证书覆盖，供 ANAS LDAP 消费者使用，但不参与 Realm、Base
DN、SRV 或 canonical DC FQDN 的计算。`SAMBA_DC_HOST_IP` 可以与 Web 记录使用的
`HOST_IP` 不同，因此该别名不能塞回 `DOMAINS`。

服务别名在两种情况下会与 Samba 原生 A 记录重合：等域时它是 AD zone apex；或者
`SAMBA_DC_HOST` 恰好等于 `SAMBA_DC_DC_DOMAIN`（例如 server name 为 `nas`、AD 域为
`test.example`、应用域为 `nas.test.example`）时，它是 canonical DC 名。ANAS 对这两类
directory-native 记录都只验证其包含精确的 `SAMBA_DC_HOST_IP`，不 claim、add、replace 或
delete。升级时，旧版 applied 清单曾错误取得的所有权会被释放而不删除原生记录；其他无法
证明创建来源的同目标记录仍只写为不可删除的 legacy observation。

BIND 只会在 application zone reconciliation 完成后启动；如果 Samba 的首次自动 DNS update
发生在 BIND 尚未监听 53 端口时，后台重试可能晚于 Compose health window。reconciler 因此在
BIND ready 后显式通过 Samba RPC 执行一次 `samba_dnsupdate --all-names --use-samba-tool`，并在发布 ready marker 前验证
`SAMBA_DC_DC_DOMAIN` 精确解析到 `SAMBA_DC_HOST_IP`。

首次 provision 显式传入 `SAMBA_DC_DC_NAME` 和 `SAMBA_DC_HOST_IP`，使目录中的 DC 身份及原生 DNS
记录与 Hook 派生值一致，不依赖容器继承的宿主机名。已有目录不在启动时自动改名；首次 provision
已使用其他 DC 名的失败工作区需要保留诊断数据，再创建正确配置的新目录。

zone inventory 的 RPC/解析失败会 fail closed；reconciler 在任何 DNS mutation 前逐个检查
`SAMBA_DC_HOST` 与 `DOMAINS` FQDN 是否被更近的 child zone 截获。A 记录只有在 committed
manifest 或 durable pending journal 中有来源证明时才可替换/删除，显式写入失败会撤销
pending，避免把管理员并发创建的同目标记录提升为 ANAS 所有。Samba RPC 可能为同一地址返回
不同内部 flags 的重复 A 项；比较前会按地址去重，但出现任意其他地址仍会 fail closed。

`separate_zone` 由 Samba 内部权威管理；未进入受管记录清单的名字不会继续向公网转发。
zone 选择和记录清单存入 Samba 持久数据，用于拒绝无迁移的 zone 漂移。当前尚未交付已有
workspace 所需的 `migrate-service-domain` 和 `migrate-application-dns-zone` 迁移器；只承诺
新 workspace 在首次 provision 前选择分离域。`SAMBA_DC_DOMAIN` 本身不支持原地换域，
变更必须新建目录并迁移身份和成员机。

## 密码策略实现

`structure.sh` 使用 `SAMBA_DC_USER_*` 调用 `samba-tool domain passwordsettings set`，生成普通用户的域策略。管理员策略由 `SAMBA_DC_ADMIN_*` 生成或更新 `pso_privileged`，并应用于内置 `Administrator` 和 `Admins` 组。管理员 PSO 的复杂度、长度、历史、最短/最长有效期以及三项锁定规则均来自 Module 参数，脚本中不保留策略常量。

启动结构维护前，最多检查本机 LDAP 监听 60 次，间隔 1 秒；不再固定等待 20 秒。普通用户的八项策略通过一次原生命令更新，减少重启时重复加载 Samba/Python 的耗时。监听失败或策略写入失败仍终止初始化，只有全部结构维护完成才写入本次启动的就绪标记；Compose 的完整 AD/DNS 健康检查保持不变。

Compose 给首次目录创建和 ACL/密码策略初始化 180 秒启动宽限期，单次完整检查最多 30 秒，仍以 30 秒间隔、6 次失败判定不健康；检查成功可在宽限期内立即就绪，不增加固定等待。资源受限主机上，90 秒宽限期曾在初始化仍正常推进时触发失败补偿；整栈启动的负载峰值也曾多次触发原 10 秒检查超时，完整检查在单独运行时返回成功。保留全部 AD/DNS 检查，按新预算另行验证整栈。

所有 16 个策略参数声明为 `hot_reload`/`samba-password-policy`。当前执行器仍使用 deployment apply fallback；重新运行容器初始化时会幂等更新域策略与现有 PSO。

## 身份与授权数据流

它是人员用户、Group、服务账号和身份锚点的事实来源。使用 LAM 或 Samba 管理工具创建、禁用用户及管理 Group。`svc_ldap` 只读，`svc_password` 仅能在限定范围修改普通用户密码，`svc_anchor` 管理身份锚点。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | provider: AD / ldaps / kerberos (`users, groups, service accounts`) |
| IAM | 不支持/不适用 |
| Group | Group 事实来源 |
| 目录密码回写 | 目录事实来源；由 ACL 与密码策略控制 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

## 开发中的邮件目录 schema

用户已确认主箱/附加箱资源及地址占用记录共用人员登录。`samba_dc/root/usr/local/share/anas/mail-schema.json` 固定定义 `anasMailbox`、`anasMailAddressClaim` 两个非登录结构类和 13 个属性；OID/GUID 与项目注册表对应。邮箱自己的 anasMailboxID 和引用人员的 anasMailOwnerAnchor 分开，资源不设置人员 anasIdentityAnchor、sAMAccountName、UPN 或密码。人员有效别名仍保存在 proxyAddresses，地址记录补充分箱映射。

`install-mail-schema.py` 是开发安装器，默认及 `--check` 只读；`--install` 明确增加缺失定义。先检查全部名称/OID/GUID、类型和字段，碰撞/defunct/漂移即拒绝，不覆盖已有 schema。安装中断后只有定义完全匹配才能续行；join 模式仅检查已有定义。它不创建 OU、不配置 ACL、不改人员或现有主地址策略，也没有接入自动启动。当前发布镜像不推定已经包含新工具；既有业务域尚未安装，正式迁移须有验证过的全卷快照及明确部署范围。

类定义指定域管理员为对象所有者，默认 DACL 仅授予域管理员/SYSTEM，另用 OWNER RIGHTS 限制隐含的 WRITE_DAC；须以实机 ACL 验证为准，不能仅按 SDDL 推断通过。开发工具 `install-mail-directory.py` 已提供固定 OU 和分角色授权；尚未接入正式部署，隔离验证不能代替生产绑定。安全描述符的 owner 只决定目录 ACL 管理，不改变 anasMailOwnerAnchor 表达的人员归属或授予邮件登录。schema 不强制 owner 外键、每人主箱唯一、地址规范化或状态机，这些由受控写入、回读校验和失败关闭共同保证。

共享 Go 目录定位在 `internal/directorymail/resources.go`：固定 Mailboxes/Addresses OU，按人员锚点确定主箱定位、按邮箱 UUID 确定附加箱定位、按规范化完整地址确定唯一占用 DN；地址类型不参与 DN。定位库本身不写 LDAP、不提供授权；候选门户建号已使用该约定，系统主址维护已增加同规则的 Python LDAP 协调入口；别名/附加箱写入及邮件投影仍待接入。

验证入口为 `python3 -m unittest discover -s modules/samba_dc/tests -p 'test_mail_*.py'` 及 `test-env/scripts/server-mail-directory-schema-e2e.sh`（强制专用 Docker daemon、新建私有目录及全新测试域，并显式指定 `ANAS_ANCHOR_IMAGE` 为已缓存的固定 Anchor Worker 镜像）。实际结果按根邮件计划记录；该子集不代替已有域迁移、全部写入口并发、地址移交、事件或 DMS/Roundcube 验收。

### 显式准备邮件 OU 与权限

`install-mail-directory.py` 复用 Samba 自带的 Python/ldb/SamDB，无新增依赖或服务。先只读核对全部邮件 schema，再在一个本地事务内检查目标域、单一可写且持有 Schema Master 的 DC、人员/服务 OU 和四类既有账号。角色必须是服务 OU 的直接普通启用用户，使用 Domain Users 主组，无额外组、特权主组祖先、adminCount 或 SID history；四者 SID 不能相同。工具不创建账号或修改密码、成员、人员及 schema。

非敏感 JSON 从标准输入读取（最大 64 KiB），示例：

```json
{
  "base_dn": "DC=example,DC=com",
  "people_dn": "OU=People,DC=example,DC=com",
  "roles": {
    "reader": "CN=svc_ldap,OU=Service Accounts,DC=example,DC=com",
    "creator": "CN=svc_portal_create,OU=Service Accounts,DC=example,DC=com",
    "address_writer": "CN=svc_portal_mail,OU=Service Accounts,DC=example,DC=com",
    "primary_writer": "CN=svc_anchor,OU=Service Accounts,DC=example,DC=com"
  }
}
```

`SAMBA_DC_BASE_DN` 必须匹配当前域。默认/`--check` 仅检查，发现缺失不写；显式 `--prepare` 新建 `OU=Mail` 下的 Mailboxes/Addresses 三个受保护 OU，目录所有者为域管理员。join 模式拒绝 prepare。既有同名对象须具备精确所有权标记、owner/group 和权限；漂移即停止，不接管或覆盖。创建、最小权限追加及回读在同一事务内执行，任一步失败全部回滚，重复完整准备零写入。

邮件 reader 只有资源读权限；creator 可创建指定资源类、推进状态/版本/操作字段，并在人员 OU 创建 user，不能改既有人员主地址/别名、箱 ID/人员归属。address_writer 可创建资源、维护默认地址/配额及地址映射/历史，只能在人员 OU 写 proxyAddresses，不能写系统 mail、人员登录名或转移邮箱本身。primary_writer 复用既有 Anchor Worker：可创建两类资源、推进操作/版本、改箱默认地址及地址类型；人员 OU 保留其原有锚点/mail 写权并增加 proxyAddresses 写权，不能改登录名、箱 ID/人员归属或删除资源。全部角色可读取当前域 mail/proxyAddresses 并列举目录，以检查旧地址冲突；这些权限不代表运行时有管理员授权，也不能代替应用层校验。域/人员 OU 的无关 ACE 原样保留，发现这些服务的额外显式授权即停止；不授予删除、改 ACL、接管所有者或域角色。AD 允许创建者在初次 Add 提交字段，字段级后续限制不能代替受控 API 的归属校验。

此工具尚未注册到启动、Hook 或现有门户 provision；示例中的地址维护账号不会自动创建/发放。正式迁移还需验证全卷快照、已有地址导入、全部写入口和实际绑定的全域可见性。受保护对象的 ACL、外部授权及后续目录变动须另行验证，不能凭基础继承 ACE 认定完整命名空间可见。当前不得单独更新门户镜像后宣称完成升级。

### 既有人员主箱迁移候选

`migrate-mail-primary.py` 复用 anchor worker 的 `mail_policy.py`，不给人员、别名、密码、组、schema 或既有资源写入。DC 的 Compose 与 CI 镜像目录均改用 Module 根构建上下文，指定 `samba_dc/Dockerfile`，以复制同一份地址规则；`.dockerignore` 白名单只纳入 DC root、Dockerfile 和该规则文件，排除 `.env`、测试及其他 Module 文件。anchor 镜像仍使用原构建上下文。隔离 scratch COPY 探针验证实际上下文，不代表完整 Ubuntu/Samba 发布镜像已经重建。

标准输入使用上一节的 JSON，另加 `mail_domain`。默认/`--check` 只读返回待建主箱、已存在及待映射 SMTP 地址数量，不表示邮件已就绪。输出 `planned` 记录本次预检拟新增量，`created` 记录本次确认新增量，`existing` 记录本次复用量。目标人员须已有成对的稳定锚点、原生 objectGUID/SID、普通账号类型，且 `mail` 已按当前系统规则收敛；姓名、后缀或缺失主地址仍待维护时拒绝，不替代主地址 Worker。检查全域 mail/SMTP proxy、全部人员锚点以及受管箱/占用；冲突、缺失的已完成占用、未知/待执行操作、错误 DN、另一箱的默认地址或删除重建的人员均拒绝。已完成的门户建号主箱可按同一 anchor/objectGUID 复用，不以资料标签重新分配身份。

显式 `--execute --snapshot-id ID --evidence-dir PATH` 需要 root、停止的 DC、不同于 Samba 数据卷的私有证据父目录，以及新的证据子目录。快照 ID 只是调用者已验证完整快照的证据标签；脚本不会执行或验证 ANAS snapshot，也无法证明宿主所有 LDAP writer 已停止。业务迁移前仍须按维护流程完成这些步骤，不能把传入任意标签当成快照验收。

全部预检、新主箱与主址占用 Add、回读确认在一个本地 SamDB 事务中执行，失败全部回滚。每箱新建独立随机 ID，操作数据绑定原人员 objectGUID；重复执行只核对既有资源，不重建或改写对象。证据目录在提交前保存意图，提交后另存结果；提交后结果写盘失败按未知处理，下一次先检查目录并使用新的证据目录，不凭上次响应判断资源不存在。

此入口只迁移已收敛的人员主地址。既有 SMTP proxy 及非 SMTP 值保持原样，未明确分箱的别名只统计为待映射，不合并进主箱、不创建别名占用；既有占用也不转让。AD active/complete 只表示目录配置提交，DMS、云路由、Webmail 和本地投影仍须独立确认。当前未注册到自动启动、Hook 或 ANAS 升级，系统主址/后缀 Worker 的候选协调入口见下节，业务域未应用。

### 系统主地址与统一占用

Anchor Worker 复用现有 LDAPS 连接与 python3-ldap，不增加服务或数据库。发现邮件 schema 后，必须读取带固定标记的 Mail OU 并使用 `mail_resources.py` / `mail_resource_ldap.py`；OU 缺失、不可见、权限不足或操作未知会报错，不回退成只写 mail。未安装该 schema 的既有部署继续使用原目录规则。正式启用仍需先完成 schema、四角色 ACL 和既有主箱迁移，不能只替换 Worker 镜像。

已有人员主箱按 anchor 定位，并绑定原生 objectGUID。系统先持久保留同箱操作和目标地址占用，再原子更新人员 mail/proxyAddresses，最后确认占用与箱配置；未完成时箱保持 pending。目录写入失败或响应丢失由下次完整核对按同一操作继续，不能分配第二个邮箱、删除占用或报告邮件已就绪。已完成但丢失占用、人员被删除重建、重复 anchor、外部 mail/proxy 冲突或他人占用均拒绝。

后缀更新只接受原 local-part，保持随机邮箱 ID，把旧主址保留为同箱有效别名，并保留全部既有 SMTP/非 SMTP proxy。重新切回自己的有效旧后缀可复用原占用；停用/保留/他人待执行占用不自动认领。sAMAccountName 改名仍未开放。资源版本采用同一 Modify 严格删除旧值/增加新值；人员写入使用 GUID 定位，并在同一 Modify 比较双锚点及原 mail/proxy 值，防止覆盖并发变更。并发增加的其他 proxy 保留，回读不一致则保持 pending；用户名和人员范围在前后重新核对。sAMAccountName 没有原子断言写锁，当前不提供改名流程。RootDSE 未声明 RFC 4528 控制，因此复用门户已验证的原子 Modify，不增加该控制依赖。下游撤权、投影、DNS 和真实收发仍需单独确认。

门户地址候选复用主箱 `anasMailQuotaState` 保存 `anas.mail-management/v1` 的单项待执行操作及最后回执。系统后缀变更在读取该状态后拒绝越过 pending，再以相同主箱版本争夺写入权；个人操作期间当前主地址未变化的检查可保持无操作。旧主地址降为别名时写入 `system.primary.previous` 标记，避免把曾经的个人别名误当成可删除的旧主地址。该协调不授予门户修改人员 mail 的权限，也不让 Worker 代写个人操作。

本节为开发候选行为；实际 Samba/Anchor Worker 固定镜像及故障验证结果记录于根邮件计划 §4.20—4.21，不能推断业务 10000 端口部署已经升级。旧三角色候选 OU 不会自动扩权：原地升级尚未实现，漂移仍拒绝。

## PEN 66678 schema 迁移契约

r11 的当前 `anasIdentityAnchor` 使用 `attributeID=1.3.6.1.4.1.66678.1.2.1` 和 `schemaIDGUID=db3786ae-3261-4d44-a2a1-588bfe3e41c5`。旧 OID `1.2.840.113556.1.8000.2554.17237.23501.51519.17672.44223.1228429.7407401.2.1` 及其 GUID `7108c5a7-2290-45e0-9eba-eef087be58e3` 只保留在改名后的 defunct schema 对象中，永不复用。迁移器位于 [`migrate-identity-anchor-oid.sh`](../samba_dc/root/usr/local/bin/migrate-identity-anchor-oid.sh)，只支持一个可写且持有 Schema Master FSMO 的 DC。

这是离线、整卷回滚的维护事务：

1. 在升级前渲染精确的 `4.23.6-r11` 候选部署，但不启动；停止整个 workspace，并在宿主机排除 DC、worker、Consumer、备份、计划任务以及 LDAP/LDB 人工会话等所有 writer。容器内检查不能证明宿主已经静止。
2. 停写后创建真实冷快照，保存工具实际返回的 snapshot ID，并要求 `anas snapshot verify` 成功。`--snapshot-id` 只写入审计证据，不会验证快照存在或可恢复。
3. `--check` 只读确认精确旧状态后，`--execute` 才导出旧值、拆除 class link、独立设为 defunct、分别修改 `lDAPDisplayName` 与 RDN、创建新 schema 对象、恢复值并重建 class link。
4. `--execute` 必须显式传入位于 Samba 数据卷之外的 `--backup-dir /mnt/anas-migration-evidence/<new-dir>`。目录必须是新建的执行子目录；其中包含 DN 和稳定标识符，应按敏感目录数据保护。
5. 完成态 `--check` 验证 final/legacy schema、User/Group class link、文本 anchor 与 `mS-DS-ConsistencyGuid` 的 Windows `bytes_le` 一致性及全局唯一性。启动 r11 后还必须验证 OU ACL 已换成新 GUID、旧 GUID 已移除、`svc_anchor` 真实写入成功、worker 健康，并逐个检查 Consumer 未产生重复账户或授权偏移。

任一步骤在 `--execute` 开始后失败，都停止重试并恢复已校验的**整个 Samba 数据卷快照**。禁止删除迁移标记、只恢复 `sam.ldb` 或按检查点手工续跑；外部证据不随数据卷回滚，应保留用于调查。完整命令和验收顺序见[迁移 Runbook](../../../docs/guide/migrate-identity-anchor-oid.md)。

## 管理面与 Secret 生命周期

日常目录管理员由 `admin_name` 定义；内置 RID 500 `Administrator` 只用于初始化和底层恢复。两者密码独立，不属于 `anas admin local` 管理面。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

### Secret 边界

- `SAMBA_DC_ADMINISTRATOR_PASSWORD`
- `SAMBA_DC_ADMIN_PASSWORD`
- `SAMBA_DC_ANCHOR_BIND_PASSWORD`
- `SAMBA_DC_LDAP_BIND_PASSWORD`
- `SAMBA_DC_PASSWORD_BIND_PASSWORD`

生成值和 lifecycle-managed 凭据以稳定逻辑键保存在 workspace 的 `.anas/secrets.yml`（`0600`）；它是受权限保护的明文，不是加密保险库。明文不得写入 README、lock、日志或普通 `config list`。本地管理员名称和 Secret 引用保存在不含密码的 `.anas/local-admins.yml`；Hook 只在所需生命周期阶段取得明文。`bcrypt` 类型只向运行配置持久化 hash，`plaintext_on_bootstrap` 类型通过 `.anas/runtime-secrets/local-admins/<module>/<id>.password` 的 `0600` 临时投影交给应用。snapshot/backup 必须把 Secret Store、账号库存和应用数据保持在同一恢复点。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 环境变量所有权

### 导出

- `ANAS_DIRECTORY_EVENTS_DIR`
- `ANAS_DIRECTORY_EVENTS_FILE_NAME`

### 显式消费

- `DOMAINS`
- `ANAS_TLS_CERTS_DIR`
- `ANAS_TLS_INTERNAL_CA_NAME`
- `ANAS_TLS_TRUST_BUNDLE_NAME`
- `LEGO_CA_CERT_NAME`
- `LEGO_CERTS_PATH`
- `LEGO_CERT_NAME`
- `LEGO_KEY_NAME`
- `ANAS_IDENTITY_APP_CLIENTS`
- `ANAS_IDENTITY_CAPABILITY_GROUPS`

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

`ANAS_IDENTITY_CAPABILITY_GROUPS` 是由启用的 Module Hook 发布的可选逗号分隔能力码名单；没有发布者时该键可以缺失。结构脚本将缺失或空名单视为空，不创建能力 OU/Group，继续后续目录和管理员初始化。非空名单仅在 `create_structure=true` 时创建对应 `CAP_*` 组；其他必需环境仍由 `set -u` 拒绝缺失。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)
- [`structure_script_test.go`](../hook/structure_script_test.go)：实际 Bash 执行生产能力组段，覆盖缺失、空名单、非空名单、关闭结构创建及缺少必需环境；仅替换目录写入，不操作 Samba 数据或 `/run`。
- [`zone_script_test.go`](../hook/zone_script_test.go)
- [`domain_dns.go`](../hook/domain_dns.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 当前限制

`domain`、`realm` 和 `netbios_name` 是不可变身份；密码参数需要专用凭据轮换，不能靠重建
容器修改。已有 workspace 的服务域/应用 DNS zone 迁移器尚未交付，不支持用普通 apply
切换 zone，也不支持原地重命名 AD 域。

## 应用目录

不发布独立人员卡片；原因声明在 module.yml 的 applications_not_published。该 Module 的服务协议或管理入口由现有集成方使用。

ANAS 显式组注册表也包含 Unix Admins。注册只表示所有权，不表示低风险：该组具有基础设施管理员嵌套权限，门户必须按域级操作处理。

### 系统主邮箱

`mail_domain` 缺省继承 AD DNS 域。初始化新建日常管理员时直接传入 `--mail-address`。现有目录 Worker 为 Provider 用户 OU 及子 OU 内的人员维护小写 `用户名@后缀` 的 AD `mail`；先检查已有 mail/proxyAddresses 冲突，写后回读，失败反映在就绪检查。该范围既有手工主地址也由系统规则替换。范围外、服务 OU、内置主体、信任和计算机不参与。Worker 仅增加人员 OU 的 mail 属性写权限，组锚点权限不附带组邮箱权限。不创建邮箱、别名、新身份 schema 或额外同步服务。后缀变更仅改邮箱；运维须确认 Worker 就绪且无剩余规则错误，不能凭容器启动认定迁移成功。

修改后缀当前使用原生 config import → lock → render → apply 流程：在完整配置的 `modules.samba_dc.config.mail_domain` 设置后缀，核对受影响模块与恢复点，激活后检查 Worker 就绪和剩余错误。`migrate-default-mail-addresses` 是策略标识，不是新增的已注册 CLI 命令；普通 config set 会被 data_migrate 策略拦截。实际邮件收发和旧址别名迁移另待邮件后端处理。


## 分组声明的单一来源

`module.yml` 的 `identity.groups` 声明通用组的名称、目录父节点、类别、顺序、风险及中英文介绍，并按 SID/RID 声明 AD 内置组说明。应用组复用各应用已有身份声明，能力名称从所属 Module 的已声明列表生成。Core 编译为本部署共享的 `shared/identity-groups/catalog.json`；Samba 的 `identity_groups.py` 只从该只读清单创建受管组和维护英文 AD `description`，不修改内置组，不移动同名对象，不由元数据修改成员/ACL。原生初始化的成员关系及权限策略仍单独执行。

`SAMBA_DC_APPLICATION_CATALOG_GROUPS` 是共享清单中受管名称的 CSV 投影，不再在 Hook 中手工列出。清单缺失、定位冲突或内容非法时初始化失败，不退回另一套硬编码默认清单。人员邮箱与身份锚点维护流程保持原有授权边界。

审计跟读器只提交以换行结尾的完整记录；写入尚未结束时保留该行起点，下次重新读取，
包括 UTF-8 字符跨写入边界的情况。轮转文件的残缺尾行不会与新文件拼接。

邮件正式引导候选由 `mail_directory_enabled` 显式启用，要求管理员提供完整 `mail_management_policy`、已收敛旧主地址的 `mail_initial_source_domain` 与已验证的全量快照引用 `mail_initial_snapshot_id`。新增两个独立生成的受限服务身份供门户建号/地址维护，reader 复用既有 LDAP reader，primary_writer 复用 Anchor Worker 身份。固定启动脚本在 Samba daemon 停止时检查单一可写 schema-master，按已确认的两类非登录对象安装 schema、建立/核对角色与 OU/ACL，并调用既有主箱导入器。任一步失败不写完成标记；重启可幂等继续。完成标记固定初始源域、快照与角色 SID，之后仅核查，禁止重导旧域或采用替换角色。导入前仍须通过 ANAS 停止外部写入者并验证完整快照；单元不能证明该前提。私有迁移证据保存在 DATA_PATH/samba_dc/mail-bootstrap，须纳入恢复集合；无业务域实测前不宣称可部署完成。Anchor 容器仅接收所需 ANCHOR_* 绑定及 TZ，不再通过整份 .env 接收目录管理员或其他写角色秘密。

正式引导遇到尚未映射的旧 SMTP 别名时，在主箱导入写入前停止，不能凭旧 proxy 值猜测分箱。`server-mail-bootstrap-runtime-e2e.py` 为真实隔离引导及公司目录两阶段脚本；完成前仅作为待执行用例，不外推业务域或完整生命周期验收。


`primaryGroupID` 属于准入和公司通讯录消费的字段，默认事件过滤及既有过滤的 Hook 升级均补齐它。单独调整 AD 主组也发布属性名，避免只能等待全量兜底；不发布属性值。生产者和邮件消费者分别有回归，实机最终收敛按根计划验收。

## DNS 监听范围

BIND 仅监听 IPv4/IPv6 回环地址和 `SAMBA_DC_DNS_SERVER` 指定的服务地址。启动脚本按服务地址所属协议生成 `listen-on` 与 `listen-on-v6`，不再使用 IPv6 的 `any`。这避免 host 网络模式下 BIND 抢占新建 Incus 网桥的 IPv6 DNS 地址，导致该网桥的 dnsmasq 无法启动；不关闭 Incus IPv6，也不增加用户配置项。DNS 查询来源限制仍由既有 trusted-networks ACL 控制。

本次运行资产修复不手工提升仓库的正式 revision；正式编号由 image-release 流程计算。隔离 E2E 使用的临时 revision 与实验镜像须在验收计划中另记基线，不能当作已发布制品。
