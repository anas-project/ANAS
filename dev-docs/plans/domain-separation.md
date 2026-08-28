---
doc_type: plan
status: implementing
created: 2026-08-19
updated: 2026-08-19
---

# `BASE_DOMAIN` 与 `SAMBA_DC_DOMAIN` 分离实施计划

> 状态：实施中（WP1-WP3 代码与 WP4 验收入口已落地；WP4 LLNG 真实服务器执行已发现 fresh apply 阻断问题，WP5 迁移闭环及 WP6 尚未完成）
> 日期：2026-08-19
> 目标示例：应用域 `nas.lnnj.com.cn`，AD DNS 域 `lnnj.com.cn`

## 1. 结论与推荐方案

把当前一个域承担的两类职责拆开：

- `global.base_domain` / `BASE_DOMAIN`：应用和 Web 入口命名空间。
- `modules.samba_dc.config.domain` / `SAMBA_DC_DOMAIN`：AD DNS 域和目录身份。

推荐配置形态：

```yaml
modules:
  samba_dc:
    config:
      domain: lnnj.com.cn
      application_dns_mode: auto

global:
  base_domain: nas.lnnj.com.cn
```

DNS 只提供两种运行模式，并用 `auto` 作为选择策略：

```text
ad_zone        在现有 AD zone 中维护应用记录
separate_zone  在 Samba DNS 中维护独立的 BASE_DOMAIN zone
auto           根据规范化后的域关系选择上述一种，不是第三种运行模式
```

示例配置自动解析为 `ad_zone`：

```text
AD DNS zone:       lnnj.com.cn
应用基础域:        nas.lnnj.com.cn
Nextcloud:         nc.nas.lnnj.com.cn
IAM:               auth.nas.lnnj.com.cn 或 manager.nas.lnnj.com.cn
DC canonical FQDN: <dc-name>.lnnj.com.cn
LDAP Base DN:      DC=lnnj,DC=com,DC=cn
用户默认 UPN:      user@lnnj.com.cn
```

无父子关系的组合也受支持，并自动解析为 `separate_zone`：

```text
BASE_DOMAIN=apps.example.net
SAMBA_DC_DOMAIN=corp.example.com
application_dns_mode=auto -> separate_zone
```

`separate_zone` 是 Samba DNS 中的内部 split-horizon 权威 zone；公网 DNS/DDNS 仍由现有动态 DNS provider 管理。其前提是 `BASE_DOMAIN` 是 ANAS 专用命名空间，所有内部需要访问的记录都能由 ANAS 完整维护。Samba 对独立 zone 具有权威性，未镜像的公网记录不会继续转发，而会返回不存在。

### 1.1 已确认决策清单

| 议题 | 已确认结论 |
| --- | --- |
| 两个参数的职责 | `BASE_DOMAIN` 只表示应用/Web 命名空间；`SAMBA_DC_DOMAIN` 表示 AD DNS 域、Realm、Base DN 和目录身份 |
| 是否要求父子域 | 不要求；相同域、父子域、互不相关域都允许，DNS mode 决定内部权威 zone |
| DNS 运行模式数量 | 只实现 `ad_zone`、`separate_zone` 两种；`auto` 是选择器，不是第三种运行模式 |
| `auto` 规则 | 等域或 `BASE_DOMAIN` 是 AD 子域时选 `ad_zone`；其余关系选 `separate_zone` |
| 无关域显式指定 `ad_zone` | 配置无效，Module `validate` 必须报错；不得静默改成 `separate_zone`，也不得等容器启动后失败 |
| 无关域显式指定 `separate_zone` | 允许，但 `BASE_DOMAIN` 必须是 ANAS 可完整维护的专用内部命名空间 |
| LDAPS 是否可使用 `BASE_DOMAIN` | 可以把它作为证书覆盖的 LDAP 服务别名；无需检查它与 AD 域的父子关系，但必须解析到 DC 且证书 SAN 匹配 |
| 是否只需修改 DNS | 否；DNS 是无关域支持的关键，但 Realm/Base DN、Kerberos、LDAPS、证书、SSO redirect URI 和消费者派生值也必须解耦 |
| Samba DNS 是否无状态 | 否；配置文件可重建，但 zone/记录位于持久化 `/var/lib/samba`，必须 inspect/reconcile |
| `SAMBA_DC_DOMAIN` 能否修改 | 只允许首次 provision 前设置；已有 AD 不支持原地修改，换域只能新建目录并迁移身份/机器 |
| IAM protocol 值域 | Core topology enum 限制通用词汇；Module manifest 声明自身子集；Runner capability resolver 校验组合兼容性 |
| Module 专用组合校验 | Core 不写 Module 名称或专用规则；Core 调度通用 `validate` Hook，规则由各 Module 实现 |

显式无关域 `ad_zone` 的期望错误应包含三个值和可执行修复建议，例如：

```text
samba_dc config validation: application_dns_mode=ad_zone requires
BASE_DOMAIN to equal or be a DNS-label subdomain of SAMBA_DC_DOMAIN;
got BASE_DOMAIN=apps.example.net, SAMBA_DC_DOMAIN=corp.example.com;
use application_dns_mode=separate_zone or auto
```

### 1.2 当前事实与实施进度

截至 2026-08-19，本轮已经完成：

- `dns_name` schema、IAM topology enum 单一词汇源、Samba `domain` 与 `application_dns_mode` 参数；
- v1 显式 opt-in 的通用 Module `validate` phase、Secret/mutation 隔离和非敏感 plan metadata；
- Samba `validate`/`calculate` 共用 `domain_dns.go`，requested/resolved mode 与 zone 进入 plan 和 deployment manifest；
- `DOMAINS` 完整 FQDN 协议，以及 `ad_zone`/`separate_zone` 的受管记录、zone 状态、durable mutation journal 与 directory-native record 边界；
- Samba FS 的 AD-only resolver/Kerberos/join/DNS registration 接线，LAM、Nextcloud、Authentik、LLNG 消费者回归，以及 Nextcloud cron 的内部 CA；
- 两套新 workspace fixture、真实 import/lock/plan/render 断言和隔离 Docker runtime probe 入口；
- Samba DC/Samba FS/Nextcloud 及 IAM 消费者 revision、生成文档和中英文操作文档同步。

仍未完成、因此不能视为已有生产 workspace 的发布闭环：

- observed AD 域物化、旧配置基线对齐，以及 `migrate-service-domain`；
- `migrate-application-dns-zone` 的完整 plan/preflight/rollback；
- Authentik 隔离 daemon 的 WP4 apply 与 Samba/BIND/LDAPS/IAM runtime E2E；
- LLNG `separate_zone` fresh apply 中发现的 DC NetBIOS/容器 hostname 不一致问题及后续完整 contracts 复验；
- canonical DC FQDN 的独立证书资源（WP6）。

Core 中没有 `validateSambaDomainDNSConfig` 或 Samba 专用域关系分支；组合规则仍由 Module Hook
拥有。首版 DNS reconciler 已有静态/单元覆盖，但在真实 Samba/BIND 数据库完成 runtime E2E
前，不宣称旧生产 workspace 可直接启用域分离。

## 2. 设计原则

1. 分离是命名空间拆分，不是 AD 域改名。
2. 新安装可以直接使用相同、父子或互不相关的两个域；DNS 模式决定应用记录位于哪个 zone。
3. `SAMBA_DC_DOMAIN` 只允许在首次 provision 前设置；一旦 `/var/lib/samba` 中存在 AD，便永久不可变。变更 `BASE_DOMAIN` 不得修改 Realm、Base DN、域 SID、对象 GUID 或机器信任。
4. 未显式配置 `samba_dc.domain` 的旧 workspace 在兼容期仍按旧行为运行，但迁移前必须把实际 AD 域物化到配置中。
5. 普通 `config set` 和 `anas apply --allow-risky` 不能修改已有 AD 域；产品不承诺 Samba AD 原地改名。
6. Samba DNS 的配置文件可重建，但 AD zone 和记录存储在持久化 `/var/lib/samba` 中，必须按持久资源 reconcile。
7. DNS、证书、LDAP、Kerberos、SSO 和应用回调地址必须作为一个整体验证。

## 3. 参数与派生值契约

### 3.1 参数定义

在 `modules/samba_dc/module.yml` 中新增：

```yaml
config:
  must_resolve:
    - domain
  defaults:
    application_dns_mode: auto
  types:
    domain:
      kind: string
      constraints:
        format: dns_name
      default_source: inherited
    application_dns_mode:
      enum: [auto, ad_zone, separate_zone]
  changes:
    domain:
      effect: immutable
      apply: replace-directory-domain
      description: The AD DNS domain is part of the provisioned directory identity.
    application_dns_mode:
      effect: data_migrate
      apply: migrate-application-dns-zone
      description: Switching zone ownership moves persistent DNS records between Samba zones.
```

兼容期按以下规则解析：

```text
effective SAMBA_DC_DOMAIN = configured samba_dc.domain, otherwise BASE_DOMAIN
```

但用户界面和新配置生成器必须主动写入 `samba_dc.domain`；fallback 只用于读取旧配置，不能作为长期 desired state。

`application_dns_mode=auto` 的解析规则：

```go
if baseDomain == sambaDomain || strings.HasSuffix(baseDomain, "."+sambaDomain) {
	resolvedMode = "ad_zone"
} else {
	resolvedMode = "separate_zone"
}
```

比较前必须先完成小写和末尾点规范化，并使用带 `.` 的 label 边界；`evillnnj.com.cn` 不能被误判为 `lnnj.com.cn` 的子域。显式 `ad_zone` 若不满足等域/子域关系必须在 render 前拒绝；显式 `separate_zone` 除等域外不限制域关系，等域必须复用现有 AD zone。

### 3.2 域名规范化

在 `internal/configschema` 增加统一的 `dns_name` format：

- 转为小写；
- 去除末尾的点；
- 拒绝 scheme、端口、路径、通配符和 IP 地址；
- 每个 label 只允许 DNS 主机名安全字符；
- 校验单 label 和完整名称长度；
- 拒绝空 label，例如 `a..example.com`。

`global.base_domain` 和 `samba_dc.domain` 都使用同一格式，避免在 hook 或 shell 中重复实现不一致的校验。

### 3.3 派生归属

| 派生值 | 新来源 | 说明 |
| --- | --- | --- |
| `SAMBA_DC_DOMAIN` | `samba_dc.domain` | AD DNS 域 |
| `SAMBA_DC_REALM` | 默认 `upper(SAMBA_DC_DOMAIN)` | Kerberos Realm；显式值必须与 AD 域大小写无关地一致 |
| `SAMBA_DC_WORKGROUP` | 默认 AD 域第一段的大写 | 可保留显式 NetBIOS 域名覆盖 |
| `SAMBA_DC_DNS_SEARCH` | `SAMBA_DC_DOMAIN` | Samba FS/Kerberos 搜索域 |
| `SAMBA_DC_DC_DOMAIN` | `<dc-name>.<SAMBA_DC_DOMAIN>` | DC canonical FQDN |
| `SAMBA_DC_BASE_DN` 及全部 OU/DN | `SAMBA_DC_DOMAIN` | 不再受应用域影响 |
| `SAMBA_DC_USER_PRINCIPAL_NAME_BASE_DOMAIN` | 默认 `SAMBA_DC_DOMAIN` | 默认 UPN suffix |
| `SAMBA_DC_HOST` | 兼容期保持 `BASE_DOMAIN` | ANAS 内部 LDAP/TLS 服务别名，避免破坏现有证书与消费者 |
| `SAMBA_DC_LDAPS_SERVER_URL` | 默认 `ldaps://SAMBA_DC_HOST` | ANAS 应用通过受证书覆盖的别名访问 |
| `SAMBA_DC_APPLICATION_DNS_MODE` | 配置值 `auto/ad_zone/separate_zone` | 保留用户意图 |
| `SAMBA_DC_APPLICATION_DNS_MODE_RESOLVED` | 域关系或显式模式 | zone reconciler 实际执行模式 |
| 各 `*_DOMAIN` | `prefix + BASE_DOMAIN` | Web 应用域名 |
| SSO Cookie domain | `BASE_DOMAIN` | 仅覆盖应用子域 |
| DDNS base/wildcard | `BASE_DOMAIN` | 公网应用入口 |
| Web 通配证书 | `BASE_DOMAIN`、`*.BASE_DOMAIN` | Traefik 和当前 ANAS LDAPS 别名 |

必须删除 `calcSambaDC` 中无条件执行的：

```go
domain := e["BASE_DOMAIN"]
e["SAMBA_DC_DOMAIN"] = domain
```

改为显式优先、旧配置 fallback，并以 `SAMBA_DC_DOMAIN` 计算 Realm、Base DN、DC FQDN、DNS search 和 UPN。

## 4. 跨参数不变量

以下不变量全部由 Samba DC Module 拥有；Core Runner 只提供通用 Hook/lifecycle 调度和错误传播，不包含 Samba 专用判断：

1. `SAMBA_DC_REALM` 必须等于 `strings.ToUpper(SAMBA_DC_DOMAIN)`；若为了兼容旧外部域必须放宽，应做成明确的 legacy 例外，不能静默接受第三套命名空间。
2. `SAMBA_DC_DC_DOMAIN` 必须位于 AD 域内。
3. `ad_zone` 模式下，`BASE_DOMAIN` 必须等于 `SAMBA_DC_DOMAIN`，或以 `.` + `SAMBA_DC_DOMAIN` 结尾。
4. `separate_zone` 模式除等域外不限制两个域的关系；等域必须使用 `ad_zone`，且 `BASE_DOMAIN` 必须被声明为 ANAS 独占管理的内部命名空间。
5. `SAMBA_DC_HOST` 必须解析到 Samba DC，并被当前 Samba TLS 证书 SAN 覆盖。
6. 已有 AD 的 observed Realm/Base DN 必须与 requested `samba_dc.domain` 一致，否则停止部署；不提供原地改名旁路。
7. `auto` 的 resolved mode 必须写入 deployment plan/manifest，实际启动脚本不能自行做第二次、可能不同的判断。

其中 1-4、7 是只依赖 desired config/派生值的规则，由 Samba DC Module 的无副作用 `validate` Hook 在生成 deployment 之前检查；5-6 需要 DNS、证书或 Samba 持久状态，放在同一 Module 的 lifecycle preflight。不要把任一规则只留给容器启动脚本或无限重试。

### 4.1 实施前基线与当前剩余缺口

当前仓库已经有多层无效配置处理，不是完全依赖 Module 启动后报错：

| 阶段 | 当前机制 | 能处理的内容 |
| --- | --- | --- |
| YAML 加载 | `internal/config/config.go` 的严格字段解析和 typed struct | 未知字段、错误结构、部分 top-level 固定值 |
| 通用参数 Schema | `internal/configschema` + `globals.yml`/Module manifest | string/bool/int/enum、format、constraints、required/default |
| 配置修改/导入 | 临时文件加载和 whole-config validation | `config set/import` 提交前拒绝无效单参数、缺失输入和运行时 key 冲突 |
| 拓扑解析 | `resolveOrderWithInputValidation`、contract/capability resolver | 依赖闭包、Provider 选择、接口兼容性、单 IAM 等组合 |
| Module calculate | Module Hook 返回错误；Core 重新校验 patch | 派生失败、Module 当前自行检查的部分组合、非法导出和 ownership 冲突 |
| 生命周期门禁 | `immutable`、`data_migrate` 等 change policy | 阻止普通配置修改绕过专用迁移/替换流程 |

计划形成时的缺口及本轮状态如下：

- **已落地**：通用、只读、在 plan 前执行的 Module `validate` phase；
- **已落地**：Module 跨参数检查不再只能塞进 `calculate`，Samba 校验与派生共用同一 helper；
- **已落地**：IAM protocol 删除 `config.Load()` 手写值域，统一使用 topology enum；
- **已落地**：`BASE_DOMAIN`/`SAMBA_DC_DOMAIN` 关系、requested/resolved DNS mode；
- **部分落地**：Samba DNS zone/记录已有首版 ANAS 受管清单，但完整模式迁移器尚未实现；
- **未完成**：已有 AD 的 observed Realm/Base DN 与 requested domain 对比仍需要 Samba lifecycle preflight；
- **持续约束**：容器 readiness/启动脚本报错只能作为最后防线，不能代替配置校验和 lifecycle preflight。

目标校验链统一为：

```text
严格 YAML
  -> 通用 Schema/enum/format
  -> 通用 dependency/contract/capability resolution
  -> 活动 Module validate（只读、无 Secret、无状态探测）
  -> Module calculate + Core patch Schema/ownership 复核
  -> Module lifecycle preflight（observed state）
  -> render/apply/reconcile
```

### 4.2 IAM 协议校验的 Core/Module 分层改造

IAM 协议校验作为同一套“单参数 Schema + 跨模块拓扑校验”模型的基线一并整理，但它不属于 Samba Module。本节的目标是消除 `internal/config/config.go` 中对协议值的手写 `switch`，同时保留 Runner 已有的能力兼容性校验。

当前状态：

- `identity.iam.default_protocol` 在 `config.Load()` 中手写限制为 `oidc/saml`；
- `modules.<name>.identity.login_protocol` 在 `config.Load()` 中手写限制为 `auto/oidc/saml`；
- 各 Module manifest 通过 `requires_capabilities.iam.interfaces.any_of` 声明自己实际支持的协议子集；
- Runner 的 capability resolver 负责选择 Provider、解析 `auto`、求 Provider/Consumer 可用协议并验证 IAM 端点。

目标状态按以下三层实现：

| 校验 | 所有者 | 实现方式 |
| --- | --- | --- |
| Core 认识的 IAM 协议词汇 | Core 的共享 capability schema | `oidc/saml` 单一来源，配置 Schema 与 Runner 共同引用 |
| `identity.iam.default_protocol` 的值域 | Core topology config schema | enum `[oidc, saml]`，缺省值 `oidc` |
| `modules.*.identity.login_protocol` 的通用值域 | Core topology config schema | enum `[auto, oidc, saml]`，未配置时 effective value 为 `auto` |
| 某个 Consumer 实际支持的协议子集 | Consumer Module manifest | 例如 NetBird 只声明 `oidc`，Nextcloud 声明 `oidc/saml` |
| IAM Provider 实际提供的协议 | Provider Module manifest | 例如 Authentik/LLNG 声明 `oidc/saml` |
| Provider 选择、协议交集、单 IAM 和端点完整性 | Core Runner capability resolver | 跨模块校验，不能由 enum 替代 |

建议把协议词汇从 `internal/runner/capability.go` 的私有常量移动到不依赖 Runner 的 Core 共享包，再新增 Core topology 参数声明，例如：

```go
var topologyParameterTypes = map[string]configschema.Parameter{
	"identity.iam.default_protocol": {
		Kind: "enum",
		Enum: iamProtocolValues(), // oidc, saml
	},
	"modules.*.identity.login_protocol": {
		Kind: "enum",
		Enum: append([]string{"auto"}, iamProtocolValues()...),
	},
}
```

`config.Load()` 仍负责默认值和字段路径解析，但值规范化应调用 `configschema.Parameter.Normalize()`，不再维护另一份 `switch`。建议的迁移文件边界是：

- 新增 `internal/config/topology_schema.go`：声明和应用 top-level/topology 参数类型；
- 把 IAM capability 名称和协议词汇放入中立的 Core schema 包，供 `internal/config` 与 `internal/runner` 共同使用，避免循环依赖；
- 删除 `internal/config/config.go` 中 `default_protocol` 和 `login_protocol` 的手写值域分支；
- `internal/runner/capability.go` 继续处理模块声明子集、Provider/Consumer 匹配、`auto` fallback、单 IAM 和 endpoint contract。

enum 只解决“这个字符串是不是系统认识的协议”，不能让 `saml` 自动变成某个只支持 OIDC 的 Module 的合法选择。后者必须继续由 capability resolver 给出带 Module 名称、Provider 和允许协议列表的错误。

需要新增/调整测试：

- topology enum 对大小写和空白使用 `configschema` 的统一规范化行为；
- `default_protocol` 非 `oidc/saml` 在 `config.Load()` 阶段失败；
- `login_protocol` 非 `auto/oidc/saml` 在 `config.Load()` 阶段失败；
- NetBird 显式选择 `saml` 通过 Core enum 后，仍在 capability resolution 阶段以“不受该 Module 支持”失败；
- Nextcloud 的 `auto` 优先采用部署默认协议，不支持默认协议时按 manifest preference fallback；
- Provider 缺少所选协议或必需 endpoint 时仍在 Runner 阶段失败；
- 同时启用多个 IAM Provider 时仍由 `checkSingleIAM()` 拒绝。

### 4.3 通用 Module `validate` 接口与 Samba 实现

Core 中不新增 `validateSambaDomainDNSConfig`，也不出现 `samba_dc` 名称、域关系或 DNS mode
分支；这是 [Core 实现标准](../../docs/architecture/core-implementation-standard.md) 的通用强制边界，而非
本方案的临时选择。Core 通过 Module Hook ABI 提供统一调度接口，各 Module 实现自己的参数
组合校验。

项目现有 Module Hook 是进程间 JSON ABI，不是由 Core import Module Go package 后调用的 Go interface。因此接口扩展采用新的 phase：

```json
{
  "abi": "anas.module-hook/v1",
  "phase": "validate",
  "module": "samba_dc",
  "env": {
    "BASE_DOMAIN": "nas.lnnj.com.cn",
    "SAMBA_DC_DOMAIN": "lnnj.com.cn",
    "SAMBA_DC_APPLICATION_DNS_MODE": "auto"
  }
}
```

Core Runner 只增加通用函数，例如：

```go
func (a *app) validateModules(order []string) error {
	for _, name := range order {
		mod := a.reg[name]
		if err := a.runModuleValidation(mod, a.scopedEnv(name)); err != nil {
			return fmt.Errorf("%s config validation: %w", name, err)
		}
	}
	return nil
}
```

Core 的职责严格限制为：

- 按 effective dependency order 调用所有活动 Module；
- 只传递该 Module 可见的 scoped env，不能像 `calculate` 一样暴露完整部署环境；
- `validate` 不传 Secret Store 明文；参数组合校验如果依赖 secret 是否存在，只传递布尔/元数据，不传值；
- 将 Module 非零退出和结构化错误转换为统一的 `config_invalid`/`validation_failed`；
- 拒绝 `validate` response 中的 Env、Secrets、Files、RuntimeFiles、DockerCopies 和服务变更，保证该 phase 只读；
- 不识别任何 Module 名称，也不解释 Module 专用字段。

`validate` 可以作为 v1 ABI 的显式 opt-in phase 加入 `logic.hook` 声明；Runner 只调用声明支持它的 Module，避免假设旧的第三方 Hook 会忽略未知 phase。建议把 Hook 声明扩展为：

```yaml
logic:
  hook:
    command: [go, run, ./hook]
    phases: [validate, calculate, render_env, services, after_start]
```

`phases` 缺失时按现有 v1 legacy 行为处理；新发布的 Module 应声明自己真正支持的完整 phase 集合，manifest admission 拒绝未知 phase。没有跨参数规则的 Module 依赖通用 Schema 即可，不必声明 `validate` 或复制空实现。若产品决定所有 Hook 必须实现该方法，则升级 Module ABI 并提供共享默认 no-op handler，不能让每个 Module 手写重复样板。

Samba DC 在 `modules/samba_dc/hook/domain_dns.go` 中实现纯函数：

```go
type domainDNSPlan struct {
	BaseDomain    string
	SambaDomain   string
	RequestedMode string
	ResolvedMode  string
	Zone          string
}

func validateDomainDNSConfig(env map[string]string) (domainDNSPlan, error)
```

`modules/samba_dc/hook/main.go` 的通用 phase dispatch 增加：

```go
case "validate":
	_, err := validateDomainDNSConfig(env)
	return hookResponse{}, err
```

Samba 实现负责：

1. 读取 Core 已按 Schema 规范化并按 scope 交付的 `BASE_DOMAIN`、`SAMBA_DC_DOMAIN` 和 requested mode；兼容期空 Samba domain 继承 base domain，空 mode 视为 `auto`。
2. 以 DNS label 边界判断等域/子域关系，将 `auto` 解析成 `ad_zone` 或 `separate_zone`。
3. 显式 `ad_zone` 配合无关域时报错；显式 `separate_zone` 不限制父子关系，但拒绝与 AD zone 完全同名。
4. 返回 resolved mode 和 zone。`calculate` Hook 必须复用同一个纯函数生成导出环境，不能复制第二套判断；这样 validate 与 render 不会漂移。
5. 不读取 Samba 数据库、不探测 DNS zone，也不执行迁移。

推荐执行顺序：

```text
config.Load / 通用字段与 enum Schema
  -> dependency、contract、capability resolution
  -> 通用 defaults/bindings 物化
  -> validateModules(active order)
  -> plan 输出，或进入 calculate
  -> calculate Hook 复用 Module 自己的校验/解析 helper
  -> Core 对 calculate patch 再执行通用 Schema 与 ownership 校验
  -> render
```

`validateModules` 应接入 whole-config validation 公共入口，因此覆盖：

- `anas config set` 和 `anas config import` 提交临时配置前，对 lock 已 pin 的活动 Module 执行；
- 显式 `anas lock` 的代码信任转换：先在内存中形成候选 lock，再对包含新 Module 的完整有效
  拓扑执行，成功后才提交 lock；
- `anas config plan` 和 `anas plan`；
- 带配置文件的 `render/build/apply/start`；
- anasd 创建 deployment。

空 lock 的新 workspace 以及 set/import 暂存的尚未 pin 新 Module，都不得在信任转换前执行其
Hook；其静态 schema、
dependency 与 capability 仍立即校验。这样既避免未锁代码在配置写入路径中执行，也避免
“必须先改 config 才能生成新 lock、又必须先有 lock 才能改 config”的死锁。新 Module 的组合
错误最迟在显式 `anas lock` 阶段拒绝，且失败不能提交候选 lock。

未启用 Module 的参数仍做类型、enum、format 等静态 Schema 校验，但不执行 Module `validate`，因为其跨参数运行不变量不属于当前 effective topology。纯 artifact 的 `start/restart` 也不重新执行，因其启动的是已经校验并冻结的 deployment。

Observed AD/DNS 状态检查属于 Module lifecycle preflight/reconciler，而不是纯配置 `validate`：Samba Module 自己读取 `/var/lib/samba` 对应的 Realm、Base DN、zone 与受管资源清单，并拒绝已有 AD 域漂移、未认领 zone 和危险迁移；Core 仍只提供通用 lifecycle 调度和事务边界。

## 5. Samba DNS 持久资源与双模式重构

### 5.1 当前持久化事实

当前 DNS 不是完全由配置文件重建的无状态服务：

- BIND 配置、监听地址、forwarder 等可以从环境变量重新渲染；
- AD zone、独立 zone 和 DNS 记录由 `samba-tool dns` 写入 Samba AD 数据库；
- Samba DC 将 `${DATA_PATH}/samba_dc/var` 挂载为 `/var/lib/samba`；
- `/var/lib/samba/registry.tdb` 存在后不会再次 provision；
- 当前 `anas_zone.sh` 只更新配置中仍存在的名称及其 IP，不会删除已移除 Module 的遗留名称，也没有独立 zone 的所有权清单。

因此 zone 与记录必须建模为“由配置驱动、但具有 observed/applied state 的持久资源”。容器重建不能视为 DNS 状态重建。

### 5.2 当前记录协议问题

Runner 当前把服务域名截成第一段：

```text
nextcloud.nas.lnnj.com.cn -> nextcloud
```

`anas_zone.sh` 又无条件把 `BASE_DOMAIN` 当成 zone。这在域分离后既无法表达 `ad_zone` 中的 `nextcloud.nas`，也无法判断是否需要创建一个独立应用 zone。

Runner 的 `DOMAINS` 必须保留完整 FQDN：

```text
inner/nc.nas.lnnj.com.cn/nextcloud,
inner/auth.nas.lnnj.com.cn/authentik
```

`DOMAINS` 是 Runner 到 Samba zone reconciler 的内部协议；更新生产者、唯一消费者和契约测试即可，不需要保持错误的第一段语义。

### 5.3 `ad_zone` 算法

适用条件：`BASE_DOMAIN` 等于或属于 `SAMBA_DC_DOMAIN`。

```text
zone = SAMBA_DC_DOMAIN
relative_name = fqdn 去掉末尾 "." + zone
```

示例：

| FQDN | zone | relative name |
| --- | --- | --- |
| `nc.nas.lnnj.com.cn` | `lnnj.com.cn` | `nc.nas` |
| `nas.lnnj.com.cn` | `lnnj.com.cn` | `nas` |
| `lnnj.com.cn` | `lnnj.com.cn` | zone apex，使用经集成测试确认的 Samba apex 表达法 |

应用子域不创建 child zone，而是在 AD zone 中维护带点的相对 owner。Samba provision 产生的 `_ldap._tcp`、`_kerberos._tcp` 和 DC 主机记录继续留在同一个 AD zone。

### 5.4 `separate_zone` 算法

适用于除等域外的任意域关系，特别是：

```text
BASE_DOMAIN=nas.example.net
SAMBA_DC_DOMAIN=lnnj.com.cn
```

解析结果：

```text
zone = BASE_DOMAIN
relative_name = fqdn 去掉末尾 "." + BASE_DOMAIN
```

示例：

| FQDN | zone | relative name |
| --- | --- | --- |
| `nc.nas.example.net` | `nas.example.net` | `nc` |
| `auth.nas.example.net` | `nas.example.net` | `auth` |
| `nas.example.net` | `nas.example.net` | zone apex |

Zone reconciler 应执行：

1. `samba-tool dns zonelist` 检查 observed zone。
2. Zone 不存在时幂等执行 `zonecreate`。
3. Zone 已存在时校验其类型、名称和 ANAS 所有权状态，不重复创建。
4. 只 reconcile ANAS 声明的记录，不覆盖未受管记录。
5. 未镜像的公网记录会因 Samba 对该 zone 权威而返回不存在；因此此模式只允许用于 ANAS 专用 `BASE_DOMAIN`。

这里不新增第三种 DNS mode 或额外的 ownership 布尔参数。产品契约直接规定：用于 `separate_zone` 的 `BASE_DOMAIN` 必须是本 workspace 的专用应用命名空间；选择显式 `separate_zone`，或无关域下让 `auto` 解析到它，即表示接受这一约束。Preflight 仍必须检查 observed zone 和受管清单；如果发现同一内部 zone 还承载无法由 ANAS 镜像的共享记录，则拒绝部署并要求更换 `BASE_DOMAIN`，不能通过一个“我已确认”开关绕过。

### 5.5 自动识别与冲突处理

`auto` 只在 render/plan 阶段解析一次：

| 域关系 | resolved mode |
| --- | --- |
| 等域 | `ad_zone` |
| `BASE_DOMAIN` 是 AD 子域 | `ad_zone` |
| 互不相关 | `separate_zone` |

字符串关系只能选择默认模式，不能替代 observed-state 检查：

- 目标为 `ad_zone`，但 observed 中已经存在同名 child zone：停止并报告冲突，不能静默绕过 child zone。
- 目标为 `separate_zone`，zone 已存在但不在 ANAS 资源清单中：停止并要求认领或换域。
- `ad_zone -> separate_zone` 或反向切换：属于持久 DNS 迁移，普通 apply 必须阻止。
- 显式模式可以覆盖 `auto`，但仍必须满足各自不变量。

显式 `ad_zone` 与无关域是 desired-config 错误，应在 Module `validate` 阶段失败；它与“observed 中已有冲突 child zone”是两类错误。前者不访问 DNS，后者由 Samba lifecycle preflight 探测。两者都不得自动降级为 `separate_zone`。

生命周期比较必须基于 applied/target 的 resolved mode 和目标 zone，而不只比较请求文本：

- `auto -> ad_zone` 且两者都解析到同一 zone：没有持久 DNS 迁移，可作为配置意图固化处理。
- `auto` 因 `BASE_DOMAIN` 变化从 `ad_zone` 解析为 `separate_zone`：即使请求文本仍为 `auto`，也必须触发 `data_migrate` 门禁。
- resolved mode 相同但目标 zone 改变：仍属于记录迁移，必须进入 `migrate-service-domain`。

Module manifest 对 `application_dns_mode` 声明静态 `data_migrate` 是保守下限。若要允许第一种“requested 文本变化但 resolved mode/zone 不变”的安全固化，lifecycle planner 必须先支持比较 applied/target 派生结果并证明无 DNS 资源差异；在此能力落地前，`config set` 仍按 `data_migrate` 拒绝普通写入，操作员通过专用迁移/固化命令完成，不能仅凭字符串判断绕过门禁。

### 5.6 受管记录清单

为安全清理遗留记录，需要保存独立于 Samba 原始数据库的 ANAS 资源状态，至少包含：

```text
zone
fqdn
record type
target
owner module
last applied deployment
```

Reconciler 用 desired 与 applied 清单求差异：

- 新记录：ensure；
- 同名目标变化：replace；
- Module 移除产生的旧记录：仅在清单证明其由 ANAS 创建时 delete；
- 人工记录：永不因未出现在 `DOMAINS` 中而删除；
- 旧 reconciler 没有逐条 creation provenance；升级时同目标既有记录只能记为不可删除的
  legacy observation，直到显式迁移确认所有权，不能因健康 marker 自动取得 delete 权；
- Zone：只有清单证明由 ANAS 创建、已无受管记录且完成迁移确认时才允许删除。

### 5.7 通用实现要求

- query/add/delete 使用 resolved mode 选择的 zone；
- 对完整后缀进行 label-aware 判断；
- 日志同时打印 mode、FQDN、zone、relative name；
- 增加最大重试或明确 health error，避免永久无信息循环；
- readiness 逐个验证完整 FQDN；
- 公网 DDNS 继续只管理 `BASE_DOMAIN` 和 `*.BASE_DOMAIN`，不因内部 zone 模式改变；
- resolved mode、zone 和受管记录摘要进入 deployment plan，便于迁移和审计。

## 6. LDAP、Kerberos 与消费者调整

### 6.1 Samba FS

保持现有职责，但确认所有值改从 AD 域派生：

- `default_realm = SAMBA_DC_REALM`；
- `default_domain = SAMBA_DC_DOMAIN`；
- `[domain_realm]` 映射 AD 域，而不是应用域；
- KDC 使用 `SAMBA_DC_DC_DOMAIN`；
- `dns_search` 使用 AD 域；
- `net ads join` 后验证 `wbinfo -t` 和机器账户所在域。

已有 Samba FS 机器信任不得因为只修改 `BASE_DOMAIN` 而 leave/join。

### 6.2 LAM、Nextcloud、Authentik、LLNG

这些消费者继续只订阅 Samba 导出的能力变量，不自行拼接 DN：

- Base DN、Users DN、Groups DN 来自 AD 域；
- LDAPS URL 使用受证书覆盖的服务别名；
- bind DN 来自 AD 域；
- Web URL、OIDC/SAML redirect URI 来自 `BASE_DOMAIN`。

重点回归：目录同步成功不代表 SSO 成功，必须分别验证 LDAP 与浏览器登录链路。

### 6.3 LLNG Cookie 与组织域

LLNG 当前同时把 `BASE_DOMAIN` 用作 SSO Cookie 域和展示/配置字段。拆分后：

- Cookie、门户 URL、应用回调地址继续使用 `BASE_DOMAIN`；
- LDAP Base DN 和 bind URL 使用 Samba 导出；
- 用户 UPN suffix 使用 AD 域；
- 不得为了让 Cookie 覆盖父域而把它改回 `SAMBA_DC_DOMAIN`。

## 7. TLS 证书计划

### 7.1 第一阶段兼容方案

保持 `SAMBA_DC_HOST=BASE_DOMAIN`，ANAS 内部 LDAP 消费者连接：

```text
ldaps://nas.lnnj.com.cn:636
```

现有证书包含 `nas.lnnj.com.cn` 和 `*.nas.lnnj.com.cn`，因此主机名验证成立。Kerberos 仍使用 canonical DC FQDN，不与这个 LDAP 服务别名混用。

该服务别名不要求 `BASE_DOMAIN` 与 `SAMBA_DC_DOMAIN` 存在父子关系。验证条件只有：内部 DNS 将别名解析到 Samba DC、连接使用同一个名称、证书 SAN 覆盖该名称并且客户端信任签发链。它只是 LDAPS endpoint alias，不参与 AD Realm、Base DN、SRV record 或 Kerberos canonical name 的计算。

必须新增验证：

```text
openssl s_client -connect <host>:636 -servername nas.lnnj.com.cn
```

并检查 SAN 和完整信任链。

### 7.2 Canonical DC LDAPS 完整支持

如果产品要承诺外部 Windows/LDAP 客户端通过 `<dc-name>.lnnj.com.cn` 验证 LDAPS，则共享应用通配证书不够。后续必须完成以下之一：

1. 落地 `certificate` Contract，为 Samba DC 请求独立资源，SAN 至少包含 `SAMBA_DC_DC_DOMAIN`；或
2. 让 lego 支持多个独立证书资源，而不是扩大全局共享证书的权限范围。

推荐方案 1。独立证书应：

- 使用稳定 resource id，例如 `samba_dc.directory_server`；
- 私钥只投影给 Samba DC；
- 支持 ACME DNS-01 或内部 CA；
- 把信任根分发给 Samba FS 和 LDAP 消费者；
- 续期后触发 Samba 安全 reload/restart；
- 在切换前验证 canonical DC FQDN 的 SAN。

不要简单申请 `*.lnnj.com.cn` 并把同一私钥共享给所有服务；那会扩大父域证书和私钥的影响面。

## 8. 生命周期与兼容发布策略

### 8.1 兼容发布 N

1. 新增 `samba_dc.domain`，hook 对旧配置 fallback 到 `BASE_DOMAIN`。
2. 新增 `application_dns_mode=auto`，把 resolved mode 固化进 deployment plan。
3. `global.base_domain` 继续保持 immutable 门禁并把动作标识改为 `migrate-service-domain`，避免旧配置在 domain 未物化时漂移。
4. `anas init`、示例配置和 UI 对启用 Samba 的新安装显式写入 `samba_dc.domain`。
5. `anas config migrate` 增加 schema migration：
   - 优先从运行中 AD 的 RootDSE、Realm 和 AD DNS zone 读取实际域；active deployment `.env` 只能作为交叉证据，不能取代 observed AD；
   - 与旧 `BASE_DOMAIN`、Realm、Base DN 和 DC FQDN 交叉校验；
   - 将确认后的值写入 `modules.samba_dc.config.domain`；
   - 不修改 Samba 数据或重启容器。
6. `config list/explain/plan` 展示 domain 的 inherited/immutable 语义以及 requested/resolved DNS mode。

### 8.2 后续严格发布 N+1

当迁移工具和升级探针稳定后：

- 新安装把 `samba_dc.domain` 设为 required input；
- 对尚未物化的旧 workspace 给出精确迁移提示；
- fallback 只保留在读取冻结旧 deployment 的兼容路径，不再用于新 desired config。

`SAMBA_DC_DOMAIN` 的 immutable 动作是 `replace-directory-domain`，不是原地 `migrate-domain`。若未来确需换域，必须新建目录、迁移身份并重新加入机器；该工程不属于本参数分离计划。

### 8.3 为什么不能立即把 `BASE_DOMAIN` 降为普通变更

它仍影响：

- 所有应用 URL；
- Traefik 路由；
- SSO issuer/entity ID/redirect URI；
- Cookie 域和现有会话；
- DDNS；
- ACME 证书；
- Nextcloud、Authentik、LLNG 等应用内持久配置。

因此它应改名为或声明为“应用域迁移”语义，例如：

```yaml
effect: data_migrate
apply: migrate-service-domain
```

但只能在 `migrate-service-domain` 执行器真正落地后修改现有门禁；在此之前继续保持 immutable 更安全。

## 9. 新安装实施流程

1. 解析并规范化两个域和 requested DNS mode。
2. 解析 `auto`，在创建 deployment 前验证 resolved mode、Realm 和 observed zone 不冲突。
3. 申请/生成 `BASE_DOMAIN` Web 证书。
4. 按 `SAMBA_DC_DOMAIN` provision AD；首次成功后该值永久不可变。
5. `ad_zone` 在 AD zone 中写入应用完整相对记录；`separate_zone` 先创建/认领独立 zone，再写入短相对记录。
6. 写入受管 DNS 资源清单和 applied deployment。
7. 启动 Samba FS，完成 Kerberos 和域加入。
8. 启动 LDAP/IAM 消费者。
9. 验证应用 URL、SSO、LDAP、SMB、DNS、zone 权威边界和证书。

新安装失败可以删除未投入使用的 workspace 后重建；一旦写入生产用户和机器对象，就按已有部署迁移规则处理。

## 10. 已有部署迁移流程

目标场景：现有 AD 和应用都使用 `lnnj.com.cn`，只把应用迁移到 `nas.lnnj.com.cn`，AD 保持不变。

### 10.1 Preflight

1. 读取 observed AD：Realm、DNS domain、Base DN、domain SID、DC FQDN。
2. 读取 active deployment 和实际应用 URL。
3. 运行 `config migrate`，物化 `samba_dc.domain=lnnj.com.cn`。
4. 确认最近可恢复快照，并做实际 restore 演练或至少完整性检查。
5. 确认新域 DNS API 权限、证书签发能力和 TTL。
6. 枚举所有 OIDC/SAML issuer、entity ID、redirect URI 和外部 webhook。
7. 解析目标 DNS mode，读取 `samba-tool dns zonelist` 和现有记录，检查 child/独立 zone 冲突与所有权。
8. 生成包含 requested/resolved mode、目标 zone、记录增删和回滚动作的迁移计划，不修改运行状态。

任何 observed AD 值与 requested `samba_dc.domain` 不一致时立即停止。

### 10.2 Prepare

1. `ad_zone` 提前准备父 zone 中的相对记录；`separate_zone` 先创建或显式认领独立 zone，再准备短相对记录。
2. 提前签发新 Web 证书，旧证书继续保留。
3. IAM provider 和 relying parties 在可行时同时接受旧、新 redirect URI。
4. Traefik 暂时保留旧域别名或重定向；登录端点不要在验证前强制跳转。
5. 降低 DNS TTL，记录旧值和恢复步骤。

### 10.3 Apply

1. 进入维护窗口并停止会改变身份/应用配置的后台任务。
2. 生成目标 deployment：
   - `samba_dc.domain` 保持旧 AD 域；
   - `global.base_domain` 改为新应用域。
   - `application_dns_mode` 固化 requested 值和 resolved mode。
3. Reconcile 目标 zone 和 ANAS 受管记录清单；不删除清单之外的记录。
4. Reconcile IAM issuer/client、应用 redirect URI、trusted domains 和 Web URL。
5. 切换 Traefik、DDNS 和应用路由。
6. 重建受影响的应用容器；Samba AD 数据卷不得 reprovision，Realm/Base DN/domain SID 必须保持原值。

### 10.4 Verify

必须全部通过：

- AD Realm、Base DN、domain SID 与迁移前一致；
- 抽样用户 object GUID/identity anchor 不变；
- `samba-tool domain level show` 成功；
- `_ldap._tcp`、`_kerberos._tcp` 和 DC FQDN 在 AD zone 正确；
- `nc.nas.lnnj.com.cn` 等内部、外部解析正确；
- resolved DNS mode、权威 zone 和记录 owner 与 plan 一致；
- `separate_zone` 中所有 ANAS 所需记录完整，且对未声明记录返回权威结果的行为符合专用命名空间约定；
- `kinit`、`net ads testjoin`、`wbinfo -t` 成功；
- LDAPS bind 和证书主机名验证成功；
- LAM、Nextcloud、Authentik/LLNG 目录同步成功；
- OIDC/SAML 浏览器登录和退出成功；
- 旧用户仍映射到同一个应用账号；
- Nextcloud/SMB 数据可读写；
- 新证书续期 dry-run 或 provider inspect 成功。

### 10.5 Commit 与清理

1. 记录新 applied deployment 和验证结果。
2. 保留旧路由一个明确的兼容窗口。
3. Cookie 域变窄会使旧会话失效，应提前通知用户重新登录。
4. 兼容窗口结束后删除旧 redirect URI、旧路由，以及受管清单证明由 ANAS 创建的旧 DNS 记录。
5. 证书确认不再被使用后再撤销或归档。

### 10.6 Rollback

Rollback 不只是切回旧 Compose：

- 恢复旧 Traefik 路由和 DNS；
- 恢复旧 requested/resolved DNS mode 和受管记录清单；新建独立 zone 只有在确认无人工记录后才能删除；
- 恢复 IAM/应用内 issuer 与 redirect URI；
- 切回旧 deployment；
- 必要时恢复同一恢复点的应用数据库；
- 再次确认 AD SID、对象和 Samba FS 信任没有变化。

迁移工具应按 `inspect -> prepare -> apply -> verify -> rollback/commit` 实现，失败不得提交 applied state。

## 11. 实施工作包

### WP1：配置 schema 与兼容层

实施状态：schema、IAM enum、参数与新配置 fallback 已完成；已有 workspace 的 observed-domain
物化和基线对齐归 WP5，尚未完成。

主要文件：

- `internal/configschema/schema.go`、测试；
- `internal/config/topology_schema.go`（新增）和 `internal/config/config.go`；
- 中立的 Core capability schema 包，以及 `internal/runner/capability.go`；
- `modules/samba_dc/module.yml`；
- `modules/samba_dc/hook/main.go`、`main_test.go`；
- `internal/runner/config_schema_inventory_test.go`；
- `internal/runner/config_schema_cli_test.go`；
- config init/import/migrate 相关测试。

实现范围：

- `dns_name` format 与 Samba 参数 enum/format；
- IAM `default_protocol`/`login_protocol` 接入 Core topology enum，删除手写值域 `switch`；
- IAM 协议词汇单一来源，Module 支持子集仍由 manifest 声明；
- 兼容期 domain fallback、init/config migrate 物化和 immutable change policy。

交付：可显式配置 AD 域和 requested DNS mode；IAM 单值校验统一进入 Schema；配置层具备 immutable guard，observed AD 双重保护在 lifecycle preflight 工作完成后闭环；相同域配置渲染结果完全兼容。

### WP2：跨域验证与内部 DNS

实施状态：通用 validate ABI、plan metadata、完整 FQDN 协议和安全 DNS reconciler 已完成；
zone 切换迁移器与 observed lifecycle preflight 归 WP5，尚未完成。

主要文件：

- `internal/deployment/model.go`、`internal/runner/manifest.go`：Hook phase 声明；
- `internal/runner/hook.go`、`config_validate.go`、`runner.go`：通用只读 `validateModules` 调度；
- `internal/runner/envscope_test.go`；
- `modules/samba_dc/hook/domain_dns.go`（新增）及测试；
- `modules/samba_dc/samba_dc/root/usr/local/bin/anas_zone.sh`；
- Samba DC hook/DNS 测试；
- render、compose 和 smoke 测试。

实现范围：

- Module Hook `validate` phase、scoped env、无 Secret/无 mutation 响应约束；
- whole-config validation、config set/import、plan 和 deployment pipeline 的统一触发；
- `auto/ad_zone/separate_zone` 解析与跨参数验证；
- `DOMAINS` 完整 FQDN 协议；
- `ad_zone` 相对 owner 计算；
- `separate_zone` zone ensure/inspect；
- 受管 DNS 记录状态与安全清理；
- zone 模式切换的 `data_migrate` 门禁。

交付：父子域使用 AD zone，互不相关的域使用 Samba 独立 zone；DNS 持久状态可 inspect、reconcile 和审计。

### WP3：Kerberos、LDAP 与证书回归

实施状态：生产接线、消费者 contract tests、render 回归、Samba FS post-join/DNS 门禁及
Nextcloud cron CA 已完成；真实服务器行为由 WP4 runtime probe 最终确认。

主要范围：

- Samba FS krb5 和 join；
- Samba DC LDAPS 服务别名；
- LAM、Nextcloud、Authentik、LLNG 消费端；
- lego/证书检查；
- server functional probes。

交付：AD 身份与 Web/SSO 域完全解耦，LDAP 服务别名、Kerberos canonical FQDN 和证书边界明确，所有现有消费者可工作。

### WP4：新安装 E2E

实施状态：两套 fixture、静态 import/lock/plan、fresh render 和隔离 runtime probe 已交付。
2026-08-28 在 `finance.hlong.wang` 的专用 LLNG 隔离 daemon 执行了当前版 fresh apply：

- 当前 Samba DC provision 使用容器 hostname `fengoffice` 创建目录原生 DC 记录，而生成配置要求
  NetBIOS name `LLNGDC`；因此 canonical `llngdc.ad.example.test` 没有生成，fresh apply 在 Samba DC
  readiness 阶段失败。
- 手工补全 DC/FS A 记录并仅在当次 Samba FS 容器可写层跳过重复 DNS register 后，
  `net ads testjoin` 和 `wbinfo -t` 均通过；这是诊断性绕过，不代表 fresh apply 验收通过。
- LLNG 容器环境与最新 `lmConf-*.json` 中的 SAML/OIDC private-key projection 断言通过；
  Eturnal live config 中的 credential 与当前 `TURN_SECRET` 断言通过，测试未输出 Secret 明文。
- 后续 apply 又因隔离网络无法解析 Nextcloud app-store 镜像域名而卡在应用安装，
  因此 `contracts/full` 尚未得到完整通过结果。

下一步必须先让 `samba-tool domain provision` 使用生成的 NetBIOS/DC 名称，然后在全新持久目录上
重跑 LLNG `core/contracts/full`；Authentik 路径仍未在真实服务器执行。

新增专用配置：

```yaml
global:
  base_domain: nas.test.example
modules:
  samba_dc:
    config:
      domain: test.example
```

覆盖 Authentik 和 LLNG 两条 IAM 路径，以及 Samba FS、LAM、Nextcloud。

再增加一组无父子关系配置以覆盖 `separate_zone`：

```yaml
global:
  base_domain: apps.example.test
modules:
  samba_dc:
    config:
      domain: ad.example.test
      application_dns_mode: auto
```

### WP5：已有 workspace 迁移器

实施状态：未开始；因此当前成果只支持全新 workspace，不支持已有生产目录直接切换。

实现：

- domain 物化 migration；
- `migrate-service-domain` plan/preflight/apply/verify；
- `migrate-application-dns-zone` 的记录迁移、所有权和回滚；
- 快照与回滚集成；
- 迁移中双 URL/双 redirect 的过渡处理；
- machine-readable JSON 输出与错误码。

### WP6：独立目录证书资源

实施状态：未开始。

如果 canonical DC LDAPS 属于发布验收范围，落地 certificate Contract 或多证书 provider，再把 Samba DC 切到最小权限独立证书。

### WP7：文档、版本与发布

实施状态：与已落地代码对应的文档、revision、镜像标签和测试说明已同步；正式发布说明需等
WP4 真实运行和 WP5/WP6 范围决策后完成。

- 更新中英文配置指南、Samba DC/Samba FS 技术文档和环境变量参考；
- 更新 `config.example.yml`、`config.full.example.yml`；
- 更新部署样例，但不得把生产 Secret 写入文档或测试 fixture；
- 运行 `go run ./cmd/gen-module-docs`；
- 对发生运行时变更的 Module bump revision，并同步 image、localization 元数据；
- 添加升级说明和明确的不支持拓扑。

## 12. 测试矩阵

### 12.1 Unit

- topology enum 规范化 IAM `default_protocol`/`login_protocol`，非法词汇在 load 阶段失败；
- Module manifest 的 Hook phases 拒绝未知/重复值，legacy 未声明状态保持兼容；
- 通用 `validateModules` 只调用活动且声明 `validate` 的 Module，并按 dependency order 执行；
- `validate` 只收到 scoped env、不收到 Secret 明文，任何 Env/Secret/file/service mutation response 都被拒绝；
- 对 lock 已 pin 的活动 Module，`config set/import/plan` 与 render/apply 对同一 Module validation
  error 给出一致结果；新增未 pin Module 在显式 `anas lock` 信任转换时拒绝且不写候选 lock；
- domain 未配置时兼容继承；
- 显式 domain 优先于 base domain；
- provision 后任何 domain 差异均被 immutable guard 阻止；
- Realm、Base DN、DC FQDN、UPN、DNS search 从 AD 域派生；
- Web 域名仍从 base domain 派生；
- 大小写、末尾点和非法 DNS 名称规范化；
- `auto` 对等域/子域解析为 `ad_zone`，对无关域解析为 `separate_zone`；
- 显式 `ad_zone` 拒绝无关域和伪后缀域；显式 `separate_zone` 接受除等域外的任意合法域关系，等域必须使用现有 AD zone；
- `DOMAINS` 保留完整 FQDN；
- 两种模式的 zone/relative owner 计算覆盖 apex 和多级子域；
- observed child zone、未认领独立 zone 和模式切换冲突能够被检测。
- Samba `validate` 与 `calculate` 复用同一个 domain/DNS helper，requested/resolved mode 不会漂移；
- NetBird 选择 `saml` 通过 Core 通用 enum 后，仍由 capability resolver 以 Module 不支持该协议拒绝；

### 12.2 Render/contract

- 两域相同时生成物与当前版本等价；
- 两域分离时各 Module `.env` 只获得声明消费的 Samba 输出；
- 变更 `samba_dc.domain` 被 immutable guard 阻止；
- requested/resolved DNS mode、目标 zone 和受管记录摘要进入 plan；
- `ad_zone <-> separate_zone` 被 data-migration guard 阻止；
- 旧配置迁移后 plan 不出现虚假的 AD 域变更；
- 敏感值不进入 plan、报告和测试输出。

### 12.3 Runtime E2E

- Samba provision 的 realm/domain 正确；
- AD DNS SRV、DC、应用 FQDN 正确；
- `ad_zone` 和 `separate_zone` 各自的权威回答、幂等重启及遗留记录清理正确；
- Samba FS `kinit`、join、`wbinfo -t`；
- LDAPS TLS hostname/chain 和 bind；
- LAM 管理；
- Nextcloud LDAP 与用户映射；
- Authentik、LLNG 各自的 OIDC/SAML 登录矩阵；
- 目录事件和 identity anchor 不受域分离影响；
- DDNS 和证书续期仍只针对应用域。

### 12.4 Upgrade/migration

- 从未配置 `samba_dc.domain` 的旧 deployment 升级；
- 物化 domain 是无运行时变化操作；
- 旧 base -> 新子域或无关专用域迁移后 AD SID、用户、组、机器信任不变；
- 模式迁移只删除受管记录，不删除人工记录；
- 迁移中途故障可恢复旧 URL；
- 目标 deployment 失败不会提交 applied state；
- 迁移后的 snapshot restore 和 rollback 可验证。

## 13. 验收标准

功能完成必须同时满足：

1. 新安装能运行示例拓扑 `BASE_DOMAIN=nas.lnnj.com.cn`、`SAMBA_DC_DOMAIN=lnnj.com.cn`。
2. 新安装能运行无父子关系拓扑，并自动选择 `separate_zone`。
3. `samba-tool domain info`/LDAP RootDSE 显示 AD 域始终为首次 provision 的值。
4. 所有 Web 服务位于 `*.BASE_DOMAIN`，不反向影响 AD Realm/Base DN。
5. `ad_zone` 不创建 child zone；`separate_zone` 幂等维护独立内部 zone。
6. DNS zone/记录具有受管资源清单，Module 移除不会留下受管垃圾，也不会删除人工记录。
7. Samba FS 信任、LDAP bind、SSO 和应用用户映射全部通过。
8. TLS hostname verification 开启且通过。
9. 两域相同的旧配置没有行为回归。
10. 普通配置修改和 `--allow-risky` 不能改变已有 AD 域。
11. 已有部署迁移前后 AD SID、identity anchor 和用户数据不变。
12. 文档、示例、schema、generated docs 和 Module revision 同步。
13. Core 源码不包含 `samba_dc` 专用配置判断；Samba 域/DNS 组合错误由 Module `validate`
    返回。已 pin Module 在 config set/import/plan/render/apply 中一致失败；新增未 pin Module
    必须在显式 `anas lock` 信任转换中、写 lock 前失败。
14. IAM protocol 单值由 Core topology enum 规范化，Module 协议子集和 Provider/Consumer 组合仍由 manifest/capability resolver 校验。
15. `validate` Hook 不能读取 Secret 明文、改变环境或写入 deployment/runtime 状态。

## 14. 明确不做的事情

- 不通过修改 env 给已有 AD 自动改名。
- 不自动改变 domain SID、Base DN 或用户 DN。
- 不提供 Samba AD 原地 domain rename；换 AD 域只能新建目录并迁移身份/机器。
- 不提供第三种 `external` DNS 运行模式；`auto` 只选择 `ad_zone` 或 `separate_zone`。
- 不允许 `separate_zone` 接管包含未镜像公网记录的共享命名空间。
- 不把 `SAMBA_DC_REALM` 当成第三个可自由分离的 DNS 名称。
- 不用关闭 TLS 校验解决证书 SAN 不匹配。
- 不用无限 DNS 重试掩盖 zone 配置错误。
- 不在没有专用迁移器时把 `BASE_DOMAIN` 降级成普通 container recreate。
- 不在 Core 中添加 `validateSambaDomainDNSConfig`、Module 名称分支或 Samba 专用错误规则。
- 不让 Module `validate` 执行 DNS/网络探测、写文件、生成 Secret 或修改 lifecycle state。

## 15. 推荐实施顺序

```text
WP1 schema/IAM enum/派生
  -> WP2a 通用 Module validate ABI
  -> WP2b Samba 域校验与 DNS
  -> WP3 消费者与 TLS
  -> WP4 新安装 E2E
  -> WP5 已有部署迁移
  -> WP6 canonical DC 独立证书（若属于 GA 范围）
  -> WP7 文档与发布
```

WP1-WP4 完成后可以支持全新分离部署，包括父子域的 `ad_zone` 和无关专用域的 `separate_zone`；已有生产部署必须等 WP5 完成。`SAMBA_DC_DOMAIN` 的原地修改不在任何工作包内。若要向外部客户端承诺 canonical DC FQDN 的 LDAPS，发布完成条件还必须包括 WP6。

## 16. 实施前仍需落定的工程细节

以下不改变本文已经确认的产品语义，但编码前需要在对应工作包内作出并记录选择：

1. Module Hook `validate` 是以 v1 显式 `phases` opt-in 发布，还是升级到强制实现的 ABI v2；推荐先用 v1 opt-in，稳定后再决定是否强制。
2. IAM 协议共享词汇所在的中立 Core package 名称，以及 topology schema 使用 Go 声明还是嵌入 YAML；无论形式如何，必须保持单一来源并复用 `configschema.Parameter`。
3. DNS 受管资源清单的具体存储文件、schema version、锁和原子提交格式；它必须与 deployment/applied state 关联，但不能伪装成 Samba 自身数据库。
4. `migrate-service-domain` 是否与本功能同一版本交付；若不交付，已有 workspace 的 `BASE_DOMAIN` 继续 immutable，只承诺新安装分离域。
5. Canonical DC LDAPS 独立证书是否属于首个 GA 验收范围；若不属于，首期只承诺受证书覆盖的 `BASE_DOMAIN` 服务别名。
6. topology-aware lifecycle diff 是否首期支持“requested mode 变化但 resolved mode/zone 不变”的安全固化；若不支持，继续使用保守 `data_migrate` 门禁。

这些工程选择必须在实现 PR/设计记录中明确，不能由某个 Hook、shell 脚本或隐式 fallback 自行决定。
