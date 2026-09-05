# Samba domain controller and DNS 技术实现

本文面向 Module 维护者，记录 `samba_dc` 当前实现、安全边界和验证入口。用户操作见[中文 README](../README.md)。

<!-- generated:module-identity:start -->
> 状态：当前实现；对应 `4.23.6-r11` / `anas.module/v1`.
<!-- generated:module-identity:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `lego` | Module | — |

## Compose 拓扑

<!-- generated:compose-topology:start -->
| Service | Image/build | Networks | Volumes |
| --- | --- | --- | --- |
| `anas_samba_dc` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc:4.23.6-r11` | `` | 3 |
| `anas_samba_dc_anchor` | `${ANAS_IMAGE_REGISTRY:-ghcr.io/anas-project}/anas-samba-dc-anchor:4.23.6-r11` | `` | 3 |
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

依赖闭包不会自动授予全部环境变量。敏感值只有在所有权或 `config.consumes` 明确允许时才进入该 Module 的 Hook/容器作用域。

## Hook、变更与回滚

- Hook command: `go run ./hook`
- `credential_rotate`、`data_migrate` 和 `immutable` 禁止普通编辑；声明的生命周期操作必须更新应用持久状态。
- 本地管理员轮换只在 Module handler 成功后提交生成 Secret；失败会保留或恢复旧应用凭据。

## 测试与实现位置

- [`main_test.go`](../hook/main_test.go)
- [`zone_script_test.go`](../hook/zone_script_test.go)
- [`domain_dns.go`](../hook/domain_dns.go)
- [`module.yml`](../module.yml)
- [`docker-compose.yml`](../docker-compose.yml)

## 当前限制

`domain`、`realm` 和 `netbios_name` 是不可变身份；密码参数需要专用凭据轮换，不能靠重建
容器修改。已有 workspace 的服务域/应用 DNS zone 迁移器尚未交付，不支持用普通 apply
切换 zone，也不支持原地重命名 AD 域。
