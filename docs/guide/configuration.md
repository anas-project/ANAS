# 配置

## 配置文件的职责

`<workspace>/config.yml` 是 ANAS CLI 管理的规范化期望状态；它不是用户手工维护的输入文件。首次创建时用 `anas init WORKSPACE --config SOURCE` 导入外部 YAML；已有 workspace 用 `anas config import SOURCE -w WORKSPACE`。两种方式都不会修改源文件。`config.lock.yml` 是 ANAS 解析并固化的 Module 版本、能力提供方和宿主机策略。不要手工编辑 workspace 配置或 `.anas/` 运行状态；plan、lock、render 和 apply 会校验配置摘要并拒绝越权修改。

配置只支持结构化 YAML。主要区域为：

- `modules`：选择参与部署的 Module；
- `global`：域名、邮箱、时区等共享设置；
- `administration`：引导管理员和 Module 本地管理员默认策略；
- `identity`：目录与 IAM Provider 选择；
- `dynamic_dns`：负责 ANAS 声明记录的 DDNS Module 和 DNS 厂商；
- `rollback`：本地快照后端与保留策略；
- `modules.<name>`：单个 Module 的精确版本、启用状态、身份协议和 `config` 参数；
- `secrets`：需要显式提供的敏感值；
- `env`：无法用结构化字段表示的原始环境变量。

完整字段清单见[配置结构参考](/reference/configuration)。

## 修改和预览

初始化时导入外部配置；已有 workspace 可再次导入。之后只通过 CLI 修改：

```bash
anas init /srv/anas --config ./my-config.yml
# 已初始化时：
anas config import ./my-config.yml -w /srv/anas
anas module update -w /srv/anas
anas config explain nextcloud.domain_prefix
anas config set global.timezone Asia/Singapore -w /srv/anas
anas config plan -w /srv/anas
anas apply -w /srv/anas
```

若外部配置选择 `module_source: cn` 且省略 `global.chinese_speedup`，导入后的受管配置会
规范化为 `module_source: official-cn` 并补入 `global.chinese_speedup: true`；渲染结果包含
`CHINESE_SPEEDUP=true`。显式 `false` 保持不变。

需要固定 Module release 时，在外部配置中写入 `modules.<name>.version`，格式为
`<semver>-r<N>`，例如 `34.0.2-r2`。`anas module update` 根据该约束解析并记录不可变 OCI
digest；普通 `plan`、`render` 和 `apply` 不会自动升级。新主机可用 `anas module sync` 按
已有 lock 恢复同一批包。

已有活跃部署时，`config set` 默认立即生成并激活新 deployment；失败会恢复旧受管配置与旧运行态。使用 `--defer` 可只保存 desired state。尚未部署或已显式停止时，命令分别返回 pending 状态，不会擅自启动服务。`config plan` 用于查看 deferred/initial 待应用变更。

`credential_rotate`、`data_migrate` 和 `immutable` 参数会在 `config set` 写入前拒绝，必须走声明的轮换、迁移或替换流程。`apply --allow-risky` 只用于已在外部完成并验证迁移后的显式接管，不会代替数据库迁移或凭据轮换。

OIDC 是 `identity.iam.default_protocol` 的默认值，只对声明支持 OIDC 的 IAM consumer 生效。Nextcloud 与 MeshCentral 当前默认使用 OIDC；Nextcloud 仍可通过 Module 参数显式选择 SAML。完整清单见 [Module IAM / OIDC 支持](/reference/module-iam-support)。

## 应用域与 Samba AD 域

`global.base_domain`（渲染为 `BASE_DOMAIN`）只定义应用和 Web 入口命名空间：应用 URL、
SSO Cookie、回调地址、Web 证书和公网 DDNS 都从它派生。Samba 的 AD DNS 域、Kerberos
Realm、Base DN、DC canonical FQDN 和机器信任则由
`modules.samba_dc.config.domain`（渲染为 `SAMBA_DC_DOMAIN`）定义。新 workspace 可以在首次
provision 前显式分开两个域：

```yaml
global:
  base_domain: nas.example.net

modules:
  samba_dc:
    config:
      domain: corp.example.com
      application_dns_mode: auto
```

为兼容旧配置，省略 `modules.samba_dc.config.domain` 时，Samba Hook 仍把
`global.base_domain` 作为有效 AD 域。该 fallback 只是让旧配置保持原有含义，不表示以后修改
`BASE_DOMAIN` 会重命名已 provision 的 AD。

`application_dns_mode` 决定 Samba 内部 DNS 如何承载应用记录：

| 请求值 | 解析规则 | Samba 权威 zone |
| --- | --- | --- |
| `auto` | `BASE_DOMAIN` 等于 `SAMBA_DC_DOMAIN`，或是它的 DNS label 子域时解析为 `ad_zone`；其他关系解析为 `separate_zone` | 按解析结果 |
| `ad_zone` | 只允许上述相等/子域关系；无关域会在 plan 阶段报错 | `SAMBA_DC_DOMAIN` |
| `separate_zone` | 除两个域完全相同外，不要求父子关系；等域必须使用 `ad_zone` | `BASE_DOMAIN` |

`separate_zone` 是内部 split-horizon 权威 zone，只能用于该 workspace 可完整维护的专用
`BASE_DOMAIN`。Samba 不会把该 zone 中未受管的名字继续转发到公网；缺失记录会返回不存在。
公网 DNS/DDNS 仍只管理应用域，不会因此接管 AD 域。

运行 plan 可在修改运行态之前确认 requested、resolved 和 zone：

```bash
anas plan -w /srv/anas
anas plan -w /srv/anas --json | jq '.module_plans.samba_dc'
```

文本输出形如
`module plan: samba_dc requested_mode=auto resolved_mode=separate_zone zone=nas.example.net`；
生成 deployment 后，同样的三项会固化在
`modules.samba_dc.validation_plan`。ANAS 内部 LDAPS 目前继续使用受 Web 证书覆盖的服务别名：
`SAMBA_DC_HOST=BASE_DOMAIN`，其内部 A 记录指向 `SAMBA_DC_HOST_IP`；它不是 AD Realm 或
canonical DC 名称。

> [!WARNING]
> 已有 workspace 的 `migrate-service-domain` 与 `migrate-application-dns-zone` 迁移器尚未
> 交付。当前只支持在新 workspace 首次 provision 前选择分离域；不要用普通 apply、原始
> `env` 或手工状态编辑绕过门禁。已 provision 的 `SAMBA_DC_DOMAIN` 不支持原地换域；如需
> 新 AD 域，必须新建目录、迁移身份并让成员机重新加入。

## 时区、语言与区域格式

三个全局字段相互独立：

```yaml
global:
  timezone: Asia/Singapore
  default_language: zh-Hans
  default_locale: zh-SG
```

- `timezone` 使用 IANA 时区名，控制时间解释和夏令时规则；
- `default_language` 使用 BCP 47，表示 UI 文本的部署默认语言；
- `default_locale` 使用 BCP 47，表示日期、数字、货币等区域格式。

未填写时按以下规则解析：

1. `timezone` 从 `TZ` 或系统 zoneinfo 继承；
2. `default_language` 从 `LC_ALL`、`LC_MESSAGES`、`LANG` 继承，macOS 还读取 `AppleLocale`；
3. `default_locale` 优先采用带明确地区的显式 `default_language`，例如 `en-GB`、`pt-BR`、`zh-Hant-TW`；
4. 语言只有 `en`、`pt`、`zh-Hans` 等无地区信息时，`default_locale` 使用宿主 locale；
5. 宿主 locale 也不可用时，通过 CLDR likely-subtag 推断地区，最后才回退 `en-US`。

解析后的时区规范为 IANA 名称，语言与 locale 规范为 BCP 47。为了让多台主机和灾难恢复后的结果完全一致，生产部署仍建议显式填写 `default_locale`。

例如，只填写 `default_language: en-GB` 会得到 `default_locale: en-GB`；填写 `default_language: zh-Hans` 时不会直接假定国家，而是优先保留宿主的 `zh-SG`、`zh-CN` 等 locale。

“随浏览器自动切换”不是把 `default_language` 写成 `auto`。标记为 browser 的应用继续使用已保存的用户偏好和浏览器 `Accept-Language`；ANAS 不写 `force_language` 一类强制项。通常直接修改浏览器首选语言即可，应用内保存的用户语言会优先于浏览器。只有支持部署 fallback 的应用才消费全局默认；其他应用使用自己的 fallback。Collabora 的语言由 WOPI 集成按会话传入，固定英文或没有 UI 的 Module 不会伪造一个无效语言开关。

每个 Module 当前支持的语言、全局值消费状态、选择机制和版本证据见 [Module 时区与语言支持矩阵](/reference/module-localization)。提供显式语言参数的 Module 遇到不支持值时会输出 `module_localization_fallback` 告警并继续运行，使用该 Module 声明的 fallback；继承的全局默认无法匹配时也采用同一 fallback，但不会阻断部署。

Module 继承全局语言不需要重复填写语言字段。例如：

```yaml
global:
  default_language: zh-Hant
  default_locale: zh-SG
modules:
  lam: {}       # LAM_LANGUAGE 未设置，因此继承 DEFAULT_LANGUAGE
  nextcloud: {} # 继承为 default_language/default_locale fallback
```

Runner 先读取显式 `global.default_language`；若省略，则从宿主机 `LC_ALL`、`LC_MESSAGES`、`LANG`（macOS 还包括 `AppleLocale`）解析默认值。该值规范化为 BCP 47 后发布为全局 `DEFAULT_LANGUAGE`。Module 自己的 `modules.<name>.config.language` 若存在则优先，否则 LAM、Nextcloud 等声明消费全局值的 Hook 使用 `DEFAULT_LANGUAGE`。浏览器型或固定语言 Module 即使能在 `.env` 中看到该全局变量，也不会因此自动改变 UI；以支持矩阵的 `Global language` 列为准。

`DEFAULT_LOCALE` 同样是最终解析值：显式 `global.default_locale` 的优先级最高；省略时才执行“带地区的显式语言 → 宿主 locale → CLDR 推断”的链路。Module 自己的 locale 参数仍优先于 `DEFAULT_LOCALE`。

没有新增更多全局“通用格式”字段：字符集统一使用 UTF-8；日期、数字、货币、星期首日和度量衡从 locale 或应用内用户偏好派生；电话默认国家等业务语义仍是 Module 参数（例如 Nextcloud 的 `phone_region`）。自动化需要稳定解析命令输出时，应在该脚本边界设置 `LC_ALL=C`，不能用用户 UI 语言控制机器接口。

## PostgreSQL 与 MariaDB

数据库不是全局开关，而是每个 Consumer Module 通过 `relational_database` Contract 独立绑定。支持双数据库的 Module 使用 `modules.<module>.config.db_type` 选择 `postgres`、`mariadb` 或 `auto`。例如让 LLNG 使用 MariaDB、Nextcloud 使用 PostgreSQL：

```yaml
modules:
  postgres: {}
  mariadb: {}
  llng:
    config:
      db_type: mariadb
  nextcloud:
    config:
      db_type: postgres
```

`auto` 在已有部署中优先保留 `config.lock.yml` 固化的绑定；首次解析时，若只显式选择了一个兼容数据库 Provider 就使用它，否则使用 Module 声明的默认值，当前双数据库 Module 的默认值为 `postgres`。因此同时启用两个数据库并不会自动把应用迁移到 MariaDB。用以下命令确认解析结果：

```bash
anas plan -c /srv/anas/config.yml
anas config explain llng.db_type
```

首次部署应在第一次 `apply` 前选定数据库。已有应用切换引擎时，修改 `db_type` 会被标记为 `data_migrate`；必须先备份并按应用要求迁移数据，再执行带有明确风险确认的应用。`--allow-risky` 只允许部署新配置，不复制表、转换 SQL 或验证迁移结果。不要通过手工编辑 `config.lock.yml` 切换数据库。

Runner 会为每个 Consumer 创建独立数据库、用户和稳定生成的凭据，并只向该 Module 发布 `*_DB_HOST`、`*_DB_PORT`、`*_DB_NAME`、`*_DB_USERNAME`、`*_DB_PASSWORD` 和 `*_NETWORK_DB`。应用不应使用 PostgreSQL 超级用户或 MariaDB root 凭据。

当前 Manifest 声明的兼容范围如下；只有列出的 interface 会被 Runner 接受：

| Consumer Module | PostgreSQL | MariaDB |
| --- | --- | --- |
| `llng` | 支持 | 支持 |
| `nextcloud` | 支持 | 支持 |
| `meshcentral` | 支持 | 支持 |
| `authentik` | 支持 | 不支持（Manifest 未声明） |

## Secret 边界

DNS API token 等普通部署输入保留在系统管理的 `config.yml` 中；该文件权限为 `0600`，CLI 库存和 plan 对 sensitive 字段脱敏，值通过 `config set` 一类配置命令修改。不要提交含真实 Secret 的外部源文件。

只有 `lifecycle_managed` 凭据会在导入时从 workspace 配置原子剥离：包括 Module 本地管理员密码，以及 Manifest 将变化声明为 `credential_rotate`、必须调用应用 API/CLI 才能正确改变的凭据。它们与系统生成的密码统一保存在版本化 `.anas/secrets.yml`（`0600`），记录稳定逻辑 key、owner、kind 和 provenance；deployment 驱动轮换使用的记录还可带 `generation` 与无明文 `rotation_id`。旧 `.anas/secrets.generated.yml` 不受支持，也不会自动迁移。

`config secret list` 只列存储键和类型；只有明确的 `config secret get` 操作会输出明文。导入失败不会修改 `config.yml`、`secrets.yml` 或配置摘要。备份和快照必须把 `config.yml` 与 `secrets.yml` 都按明文敏感数据保护。

### `config.yml`、`config-managed.yml` 与 `secrets.yml`

假设操作者准备了以下外部配置：

```yaml
# /tmp/my-anas.yml
global:
  base_domain: example.com
  timezone: Asia/Singapore

modules:
  nextcloud:
    administration:
      local_accounts:
        break_glass:
          password: Initial-Nextcloud-Password

secrets:
  cloudflare_dns_api_token: cloudflare-token-123
```

执行受控导入：

```bash
anas config import /tmp/my-anas.yml -w /srv/anas
```

外部源文件保持不变；workspace 中产生三类相互关联的状态。

`/srv/anas/config.yml` 保存普通期望状态以及普通部署 Secret：

```yaml
global:
  base_domain: example.com
  timezone: Asia/Singapore

modules:
  nextcloud:
    administration:
      local_accounts:
        break_glass: {}

secrets:
  cloudflare_dns_api_token: cloudflare-token-123
```

Nextcloud 本地管理员密码已经移除；其用户名由 ANAS 固定规则决定，不进入配置。Cloudflare token 仍留在 `config.yml`，因为普通 apply
可以正确重新渲染该部署输入。文件权限为 `0600`，CLI 库存与 plan 对声明为 sensitive 的值
脱敏。

`/srv/anas/.anas/secrets.yml` 保存生命周期托管凭据及系统生成 Secret：

```yaml
api_version: anas.secrets/v2
secrets:
  ANAS_LOCAL_ADMIN__NEXTCLOUD__BREAK_GLASS__PASSWORD:
    value: Initial-Nextcloud-Password
    owner: nextcloud
    kind: local_admin
    provenance: config-import:modules.nextcloud.administration.local_accounts.break_glass.password
  NEXTCLOUD_DB_PASSWORD:
    value: 8QLRhDzA4ScpJp6h...
    owner: runner
    kind: generated
    provenance: runtime
```

稳定逻辑 key 标识凭据；`owner` 表示归属，`kind` 区分本地管理员、其他生命周期凭据和
生成值，`provenance` 记录来源。以后运行 `anas admin local rotate nextcloud break_glass`
只会在应用更新并验证成功后原子更新这里，不会把密码写回 `config.yml`。

`/srv/anas/.anas/config-managed.yml` 只保存 `config.yml` 的完整性元数据：

```yaml
api_version: anas.config/v1
digest: sha256:643136ee18baf6e3...
updated_by: config-import
```

摘要只覆盖 `config.yml`，不覆盖 `secrets.yml`；该文件不含配置副本或 Secret。plan、lock、
render、build 和 apply 会比较摘要，拒绝绕过 CLI 的手工修改。快照和备份必须把它与对应的
`config.yml` 一起恢复。

| 操作 | `config.yml` | `config-managed.yml` | `secrets.yml` |
| --- | --- | --- | --- |
| `config import` | 规范化写入，剥离生命周期密码 | 写入新摘要 | 导入生命周期凭据 |
| `config set global.timezone UTC` | 修改并执行/明确 pending | 更新摘要 | 不变 |
| 修改普通 DNS/API token | 修改 | 更新摘要 | 不变 |
| `admin local rotate nextcloud` | 不变 | 不变 | 应用验证成功后更新 |
| `credential rotate eturnal.secret` | 不变 | 不变 | candidate 验证后更新值、代次与 rotation ID |
| 手工编辑 `config.yml` | 内容改变 | 摘要未同步 | 不变；plan/apply 拒绝 |
| snapshot/backup restore | 恢复 | 与配置一起恢复 | 一起恢复 |

简言之：`config.yml` 表示“希望部署成什么样”，`config-managed.yml` 证明“这份配置由
ANAS CLI 合法写入”，`secrets.yml` 保存“不能通过普通 apply 安全改变的凭据和 Runner
生成的 Secret”。

## Deployment 机器凭据

声明了 `credentials.provides/consumes` 的机器凭据使用无明文库存和 deployment 事务轮换：

```bash
anas credential list -w /srv/anas
anas credential rotate eturnal.secret --dry-run -w /srv/anas
anas credential rotate eturnal.secret -y -w /srv/anas
anas credential rotate --all --dry-run -w /srv/anas
```

`list` 不返回值、hash 或 verifier。`--dry-run` 检查 active runtime、Store presence/generation、
authority、生成策略、Hook ABI 以及 owner/consumer 图，但不生成候选、不写文件，也不调用 Hook 或
Docker。实际执行在独立 candidate deployment 中协调并验证凭据，成功后一次性提交 Secret Store
并 promotion；Store 提交前失败会恢复 previous deployment，提交后的中断由下一次排他运行时操作
自动完成 promotion。

轮换后的 previous deployment 与新 Store 代次不同，普通 `rollback` 会以
`credential_store_mismatch` 拒绝，`--allow-risky` 不能绕过；当前应恢复包含匹配 Store、deployment
和数据的 snapshot，而不是只切换制品指针。

当前统一库存只覆盖 active deployment 冻结的机器凭据，首个可执行 provider 是
`eturnal.secret`。数据库 resource credential、本地管理员和外部 API token 仍分别使用既有边界；
不能用 `--force` 绕过缺失的 generator、handler 或验证能力。

## Module 本地管理员

`management.local_accounts` 是 Module Manifest 的能力声明。用户不能配置本地管理员用户名：
Module `fixed_username` 优先，否则使用 ANAS 固定模板 `admin_{module}`。这里的账号 ID（如
`primary`、`break_glass`）不是用户名。
生命周期托管密码不能长期写进 `config.yml`；外部导入文件可把它作为一次性 bootstrap 输入，成功导入后 workspace 副本会移除该值。查询与轮换分别使用：

```bash
anas admin local credential nextcloud break_glass -w /srv/anas
anas admin local rotate nextcloud break_glass -w /srv/anas
anas admin local rotate ddns_go --prompt -w /srv/anas
```

省略账号 ID 时按 `primary`、唯一账号、歧义报错的顺序解析。用户名首次物化后进入
`.anas/local-admins.yml` 并锁定。全局 `username_template` 与 Module 级 `username` override
都是非法字段；CLI 不提供 rename 命令。

Nextcloud 不声明 `modules.nextcloud.config.admin_password`；该路径是非法配置。Nextcloud
handler 通过真实的 `occ user:resetpassword --password-from-env` 路径更新和验证恢复账号。
