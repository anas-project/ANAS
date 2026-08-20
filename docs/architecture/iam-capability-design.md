# IAM 多实现与协议能力设计

## 1. 目标与硬约束

ANAS 应允许应用 Module 对接不同 IAM 实现，例如 LemonLDAP::NG（LLNG）和 Authentik，
而不在应用 Hook 中按 IAM 名称写分支。

> **实现状态**：阶段 A–D 已全部落地。仓库中的 Keycloak scaffold 已删除，
> `iam.provider` 在 `llng` 与 `authentik` 之间切换不需要改动任何消费方 Module。
> 与本文档正文的偏差集中记录在 §12。

本设计遵循四条硬约束，它们决定了后面所有取舍：

1. **只接受同时提供 OIDC 和 SAML 的 IAM。** 只提供其中一种协议的实现不具备
   IAM provider 准入资格。
2. **一个部署只启动一个 IAM。** 不支持多个 IAM 同时活动。
3. **provider 只能由用户显式指定。** 没有默认值、没有优先级列表、没有自动
   推断。
4. **Module 不能选择 provider。** 应用 Module 只能选择协议，不能选择由哪个 IAM
   服务自己。

在此之上，方案还需要满足：

- IAM Module 在清单中声明自己提供的协议；
- 应用 Module 声明自己可以使用的协议以及优先顺序；
- Runner 在启动前完成协议解析，配置错误在 `plan`、`render`、`build`、`start`
  阶段立即失败；
- Module 通过统一环境变量读取解析结果，不依赖 `LLNG_*`、`KEYCLOAK_*` 等实现私有
  变量；
- 选择结果进入锁文件，重启时保持稳定，切换 IAM 时给出明确的变更提示。

约束 1 和 2 共同保证了单点登录语义：所有应用登录到同一个 IAM 的同一个会话域，
用户只登录一次。这也是不允许多 IAM 的根本原因——两个 IAM 各自维护独立会话，
用户会被要求登录两次，除非引入 IAM 联邦，而联邦不在本设计范围内。

约束 1 还消除了"应用需要 SAML 但 provider 只有 OIDC"这一整类失败：既然任何
合格 provider 都同时提供两种协议，协议交集永远非空。

### 1.1 准入条件的直接后果

Authelia 当前只提供 OIDC，没有 SAML IdP，因此在本设计下**不具备 IAM provider
资格**，不进入迁移路径。若其未来提供 SAML IdP，可按 §4.1 的准入条件重新评估，
届时无需修改任何应用 Module。

已实现的双协议提供方是 LLNG 和 Authentik。Keycloak 虽然也满足准入条件，但仓库
中原有的 scaffold 已删除，不在支持范围内。

两者的端点模型正好相反，这不是巧合而是选型标准：Authentik 的 Application 与
Provider 一对一绑定，SAML 端点挂在应用 slug 下
（`/application/saml/<slug>/metadata/`、
`/application/saml/<slug>/` 等），每个 Provider 有独立的
EntityID；OIDC 默认也按应用 slug 生成不同 issuer 与 discovery URL。而 LLNG 的
IdP 端点是部署级单例，所有 SP 共用。同时支持这两种形状，契约才算真的通用。

因此**不能假设 IdP 端点是部署级单例**。§6.2 的端点契约按消费方发布，以覆盖这
两种形状；这是准入条件之外，第二个必须由契约而非实现来吸收的差异。上述
Authentik 路径形状未经运行实例验证，首次真实部署时应对照其当前版本文档复核。

## 2. 历史实现的问题

本节记录引入现行 IAM Contract 之前的耦合，名称和 lock 路径是历史基线，不是当前配置接口。

当时的 `requires_one` 只能从静态列表选择一个 Module，并将绑定写入
`module.lock.yml`。它适合 PostgreSQL/MariaDB 这种"实现名即能力"的依赖，但 IAM
还存在以下耦合：

1. 消费方必须列出所有实现名称，例如 `providers: [authentik, llng]`；新增 IAM
   实现时必须修改所有应用清单。
2. `netbird` Hook 直接判断具体 Provider 名，并读取 Provider 专用的 OIDC
   configuration endpoint。
3. `nextcloud` 默认写入 LLNG 使用的 `SMAL_SP_*` 变量，协议和 IAM 实现绑定。
4. `features.sso_provider: true` 只能表达布尔能力，不能表达实际支持 OIDC、SAML
   中的哪些协议。
5. 当前自动选择带默认实现，会在用户没有明确意图时自动加入并启动 IAM。这与
   约束 3 冲突。

因此不建议继续扩展 IAM 名称分支。需要把"实现选择"和"协议选择"提升为 Runner
可验证的能力绑定，其中实现选择完全交给用户，协议选择交给应用。

## 3. 用户配置

新增顶层 `iam` 配置：

```yaml
modules:
  nextcloud:
    identity:
      login_protocol: saml
  netbird:
    identity:
      login_protocol: auto

identity:
  iam:
    provider: llng
    default_protocol: oidc
```

规则：

- `identity.iam.provider` 是部署级选择，值为 IAM Module 名称；只要存在 IAM 消费方就必填，
  **没有默认值**。Runner 不会因为只有一个候选就自动选它。
- 被选择的 IAM 由 Runner 自动加入依赖闭包，用户无需同时写进 `modules`。
- `identity.iam.default_protocol` 可选，是应用未显式指定协议时的部署级默认值。
- `modules.<app>.config.iam_protocol` 是可选的应用级覆盖，取值为 `oidc`、`saml`
  或 `auto`。
- **不存在应用级 provider 覆盖。** 应用 Module 与用户配置都不能让某个应用使用
  `identity.iam.provider` 以外的 IAM。
- 如果 `modules` 同时显式列出另一个 IAM Module，Runner 报错。
- 没有 IAM 消费方时不自动启动 `identity.iam.provider`。如果用户确实只想启动 IAM，可把
  该 IAM 同时列入 `modules`。

不允许宿主进程环境变量覆盖 `identity.iam.provider` 或 `identity.iam.default_protocol`，否则相同
配置文件可能产生不同部署。临时试算可后续增加 `anas plan --iam llng`，但持久
配置仍是唯一事实来源。

### 3.1 协议解析优先级

对每个消费方，最终协议按以下顺序确定，第一个命中的规则生效：

1. `modules.<app>.config.iam_protocol` 的显式值（非 `auto`）；
2. `iam.default_protocol`，当它出现在该应用清单的 `any_of` 中；
3. 该应用清单 `prefer` 列表中的第一项。

无论走哪条规则，结果都必须落在该应用的 `any_of` 内，否则报错。

## 4. Module 清单能力模型

该变更引入新的清单字段，ABI 升级为 `anas.module-hook/v1`。由于尚未正式发版，没有做
v1/v2 双读：所有 Module 一次性切到 v2，Runner 只认识 v2。

### 4.1 IAM 提供方与准入条件

LLNG：

```yaml
abi:
  supports:
    - anas.module-hook/v1

capabilities:
  provides:
    - name: iam
      interfaces:
        - oidc
        - saml
```

准入条件：

- `interfaces` 必须使用 Runner 已知的小写协议标识；第一版只认识 `oidc` 和
  `saml`，未知标识在加载清单时失败，不能静默忽略。
- `interfaces` **必须同时包含 `oidc` 和 `saml`**。只声明其中一个的 Module 在清单
  加载阶段就被拒绝，不能作为 IAM provider 注册，也不能被 `iam.provider` 选中。

把准入检查放在清单加载阶段而不是解析阶段，是为了让"某个 IAM 不合格"这件事与
用户的具体配置无关，错误信息也更直接。

### 4.2 IAM 消费方

消费方只声明自己能用哪些协议，以及 `auto` 时的偏好顺序。清单中**没有任何字段
可以指定 provider**。

NetBird 只接受 OIDC：

```yaml
dependencies:
  requires_capabilities:
    - name: iam
      interface_selected_by: iam_protocol
      interfaces:
        any_of:
          - oidc
        prefer:
          - oidc
```

一个可接受两种协议、优先 OIDC 的消费方长这样；Nextcloud 当前使用这个声明：

```yaml
dependencies:
  requires_capabilities:
    - name: iam
      interface_selected_by: iam_protocol
      interfaces:
        any_of:
          - oidc
          - saml
        prefer:
          - oidc
          - saml
```

约束：

- `any_of` 非空，表示至少匹配一个，不表示必须同时使用所有协议；
- `prefer` 必须是 `any_of` 的子集，并决定 `auto` 的选择顺序；
- `interface_selected_by` 使用应用参数名，映射成例如
  `NEXTCLOUD_IAM_PROTOCOL`；
- 原设计中的 `selected_by: global.iam.provider` 字段被删除：provider 恒为
  `iam.provider`，不需要在清单中重复表达一个不可变的事实；
- 如果未来确有"必须同时支持多个接口"的场景，再增加 `all_of`，第一版不预留
  含糊语义。注意这不是一个局部改动：`all_of` 会同时打破
  `ANAS_IAM_BINDING__<APP>__INTERFACE` 的单值假设和 §6.1 的名单划分不变量。

IAM 不再使用带静态 `providers` 列表的 `requires_one`。数据库等现有
`requires_one` 保持不变。

## 5. Runner 解析算法

解析发生在 Hook 执行之前：

1. 读取所有 Module 清单，建立 `capability -> provider -> interfaces` 索引，并在
   此阶段执行 §4.1 的双协议准入检查。
2. 收集已启用应用的 `requires_capabilities`。
3. 如果存在 `iam` 消费方，读取 `iam.provider`；为空则报错，不做任何自动选择。
4. 验证指定 Module 存在、未被禁用，并声明 `provides: iam`。
5. 对每个消费方按 §3.1 的优先级确定协议。
6. 验证结果协议同时位于 `consumer.any_of` 和 `provider.interfaces` 中。由于
   准入条件保证了 provider 两种协议都有，这一步实际只会因应用侧的显式值越界
   而失败，但仍然保留为不变量检查。
7. 校验失败立即报错，不运行 Hook、不生成密钥、不写运行目录。
8. 把 IAM Module 加入每个消费方的依赖边，保证 IAM 的 `calculate` Hook 先运行。
9. **在任何 Hook 运行之前**注入完整绑定集合：`ANAS_IAM_PROVIDER`、
   `ANAS_IDENTITY_CLIENTS`、按协议拆分的 `ANAS_IDENTITY_OIDC_CLIENTS` 与
   `ANAS_IDENTITY_SAML_CLIENTS`，以及每个消费方的
   `ANAS_IAM_BINDING__<APP>__INTERFACE`。
10. 按现有顺序执行 Hook。
11. 成功 `start` 后把提供方和每个应用的协议绑定写入锁文件。

第 9 步是端点契约的前置条件。对 Authentik 这类 per-app 端点的 provider，它的
`calculate` 必须先知道消费方名单和各自协议，才能推导出每个应用的 IdP 端点
（slug 取 Module 名）。这些信息在第 5–7 步就已经全部解析完毕，因此 Runner 可以
在第一个 Hook 之前发布，不需要改动 `calculate` → `render_env` 的生命周期顺序。

`plan` 仍保持只读，但需要执行清单级能力解析，因此能提前报告配置错误。

建议错误信息包含消费方、提供方、双方协议和修复动作，例如：

```text
netbird requires IAM capability, but iam.provider is not set;
set iam.provider to one of: llng
```

```text
iam.provider "foo" does not provide capability "iam";
available providers: llng[oidc,saml]
```

```text
module "authelia" declares capability iam with interfaces [oidc];
an IAM provider must declare both oidc and saml
```

```text
netbird.iam_protocol is "saml", but netbird supports [oidc];
set netbird.iam_protocol to one of: oidc, auto
```

## 6. 统一环境变量契约

Runner 在任何 Hook 之前发布部署级只读变量：

```dotenv
ANAS_IAM_PROVIDER=llng
ANAS_IAM_INTERFACES=oidc,saml

ANAS_IDENTITY_CLIENTS=nextcloud,netbird
ANAS_IDENTITY_LDAPS_CLIENTS=nextcloud
ANAS_IDENTITY_OIDC_CLIENTS=netbird
ANAS_IDENTITY_SAML_CLIENTS=nextcloud
ANAS_IDENTITY_CLIENT__NEXTCLOUD__INTERFACES=ldaps,saml
ANAS_IDENTITY_CLIENT__NETBIRD__INTERFACES=oidc

ANAS_IAM_BINDING__NETBIRD__INTERFACE=oidc
ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE=saml
```

这一组只包含 Runner 自己解析得出的事实。`ANAS_IAM_PORTAL_URL` 等由 IAM 域名推导
的值不在其中，它们属于 §6.2 的 Provider `calculate` 产物——Runner 在 Hook 运行前
并不知道 provider 的域名。

绑定中不包含 `__PROVIDER`：单 IAM 部署下它恒等于 `ANAS_IAM_PROVIDER`，重复
发布只会制造两个可能不一致的事实来源。

### 6.1 消费方名单按协议拆分

`ANAS_IDENTITY_<PROTOCOL>_CLIENTS` 是 `ANAS_IDENTITY_CLIENTS` 按协议的投影。变量
都由 Runner 从直接协议声明和 §5 的 IAM 解析结果一次写出，Module 只读不写，因此不存在
互相偏离的可能。

拆分的理由不是"否则拿不到协议"——协议本来就可以逐个查
`ANAS_IAM_BINDING__<APP>__INTERFACE`。真正的收益是让
**"本次部署没有 SAML 消费方"成为可以直接判断的一等条件**（`ANAS_IDENTITY_SAML_CLIENTS`
为空），而这正是 §6.3 端点校验所依据的条件。两处使用同一事实，就该有同一种
表达，否则每个 Provider Module 都要自己扫一遍名单才能决定是否生成 SAML 配置段。
对 Authentik 这类按协议逐个创建 Application/Provider 对象的实现，拆分后的列表
是直接的 1:1 映射。

保留扁平的 `ANAS_IDENTITY_CLIENTS`，是因为存在与协议无关的消费场景，例如 LLNG 门户
的应用启动器需要列出全部应用。

**不变量：OIDC 与 SAML 列表构成 IAM 消费方集合的一个划分**——每个 IAM 消费方恰好出现
在一个列表中，因为 §4.2 规定一个应用只绑定一个 IAM 协议。身份协议的全体列表允许
重叠，例如 Nextcloud 同时出现在 LDAPS 和 SAML 列表。若将来引入 `all_of` 允许
一个应用同时使用两种协议，划分会退化为覆盖，`__INTERFACE` 也不再是单值，届时
本节和 §6.3 必须一并重新设计，不能靠"顺手多加一个列表"糊过去。

### 6.2 端点按消费方发布

IdP 端点由 Provider 的 `calculate` Hook 产生，并且**必须按消费方发布**，而不是
作为部署级单例：

```dotenv
ANAS_IAM_BINDING__NETBIRD__OIDC_ISSUER_URL=https://auth.nas.example.com/application/o/netbird/
ANAS_IAM_BINDING__NETBIRD__OIDC_DISCOVERY_URL=https://auth.nas.example.com/application/o/netbird/.well-known/openid-configuration

ANAS_IAM_BINDING__NEXTCLOUD__SAML_METADATA_URL=https://auth.nas.example.com/application/saml/nextcloud/metadata/
ANAS_IAM_BINDING__NEXTCLOUD__SAML_ENTITY_ID=https://auth.nas.example.com/application/saml/nextcloud/metadata/
ANAS_IAM_BINDING__NEXTCLOUD__SAML_SSO_URL=https://auth.nas.example.com/application/saml/nextcloud/
ANAS_IAM_BINDING__NEXTCLOUD__SAML_SLO_URL=https://auth.nas.example.com/application/saml/nextcloud/
```

这是覆盖 §1.1 两种端点形状的唯一契约：对 LLNG 和 Keycloak，各消费方拿到的值
就是同一个单例值重复若干遍；对 Authentik，各消费方的值真正不同。反过来把端点
定义成部署级单例，则会把 LLNG/Keycloak 的形状写死进"通用"契约，第一个
Authentik Module 就会迫使消费方改代码，违背决策 6。

Provider 若确有部署级单例端点，**可以额外**发布 `ANAS_IAM_OIDC_ISSUER_URL`
等全局变量作为便利值，但消费方 Hook 不得读取它们。应用只读取自己的
`ANAS_IAM_BINDING__<APP>__*`。例如 NetBird 不再出现 `switch keycloak/llng`，
而是验证 `ANAS_IAM_BINDING__NETBIRD__INTERFACE` 为 `oidc` 后读取
`ANAS_IAM_BINDING__NETBIRD__OIDC_DISCOVERY_URL`。

### 6.3 端点校验

Runner 在 Provider Hook 返回后，**按实际绑定逐个消费方**校验必需变量，遍历范围
就是 §6.1 的两个协议列表：

- `ANAS_IDENTITY_OIDC_CLIENTS` 中的每个消费方：`..._OIDC_ISSUER_URL`、
  `..._OIDC_DISCOVERY_URL`；
- `ANAS_IDENTITY_SAML_CLIENTS` 中的每个消费方：`..._SAML_METADATA_URL`、
  `..._SAML_ENTITY_ID`、`..._SAML_SSO_URL`；`..._SAML_SLO_URL`、
  `..._SAML_SLO_RESPONSE_URL`、`..._SAML_SIGNING_CERT` 可选，但 SP 若要校验
  断言签名就需要 `SAML_SIGNING_CERT`。

`SAML_SIGNING_CERT` 只传公钥证书。**签名私钥不进入契约**——要求 Provider 交出
私钥的契约对 Authentik 这类自行管理密钥的实现不可实现，那正是把单一实现的形状
写进"通用"契约的错误。SP 自己的密钥对由 SP 生成，Provider 从 SP metadata 取回
其证书。

列表为空时该协议不参与校验。只校验已绑定的协议，不校验未使用的协议，原因是 per-app 端点的 provider 上
根本不存在"与具体应用无关的 SAML 端点"——SAML 端点要等某个应用的 Provider
对象建好之后才存在，要求无条件发布是无法满足的。

清单级的双协议准入（§4.1）与运行时的按绑定校验各司其职：前者保证这个 IAM
**有能力**服务将来任何协议的应用，后者保证本次部署**实际**可用。缺少必需变量时
在 Provider Hook 后立即失败。

### 6.4 客户端注册请求

消费方继续负责生成自己的客户端 secret 和回调地址，但改用通用命名空间。名单类
变量（`ANAS_IDENTITY_CLIENTS`、`ANAS_IDENTITY_OIDC_CLIENTS`、`ANAS_IDENTITY_SAML_CLIENTS`）
已由 Runner 在 §6 发布，消费方只补充自己的字段，不得追加或改写名单：

```dotenv
ANAS_IAM_CLIENT__NETBIRD__INTERFACE=oidc
ANAS_IAM_CLIENT__NETBIRD__CLIENT_ID=netbird
ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET=...
ANAS_IAM_CLIENT__NETBIRD__REDIRECT_URIS=https://netbird.example/auth,...
ANAS_IAM_CLIENT__NETBIRD__POST_LOGOUT_REDIRECT_URIS=https://netbird.example
ANAS_IAM_CLIENT__NETBIRD__OIDC_LOGOUT_URI=https://netbird.example/oidc/backchannel-logout
ANAS_IAM_CLIENT__NETBIRD__OIDC_LOGOUT_METHODS=backchannel,frontchannel
ANAS_IAM_CLIENT__NETBIRD__OIDC_LOGOUT_SESSION_REQUIRED=true
ANAS_IAM_CLIENT__NETBIRD__SCOPES=openid,profile,email,groups
ANAS_IAM_CLIENT__NETBIRD__ALLOW_GROUPS=APP_netbird,APP_all,Admins
```

SAML 客户端使用同一前缀，发布 `SP_METADATA_URL`、`SP_ENTITY_ID`、`ACS_URL`、
`NAME_ID_FORMAT`；支持 Single Logout 时另外发布 `SAML_SLS_URL` 和
`SAML_SLS_BINDINGS=redirect,post`。`POST_LOGOUT_REDIRECT_URIS` 只表示 IAM 登出后允许
跳回的地址，不能替代 OIDC front/back-channel endpoint。两种协议共用 `ALLOW_GROUPS`、`DOMAIN` 和 `ATTRIBUTES`；
`ATTRIBUTES` 的格式是 `name:source:required` 的逗号列表，由 Provider 翻译成自己
的属性映射（LLNG 展开成 `ATTR01`/`ATTR02`…）。

Provider 的 `render_env` Hook 读取所有通用注册请求，翻译成 LLNG 或 Authentik 的
私有配置：LLNG 写 `OIDC_RP_*` / `SAML_SP_*` 供其配置脚本消费，Authentik 生成
声明式 blueprint 创建 Application 与 Provider 对象。私有变量不得再由应用 Module
生成。

由于当前生命周期先完成所有 `calculate`，再执行所有 `render_env`，这个方向与现有
Hook 顺序兼容，且不会与 §6.2 的 per-app 端点循环依赖：

1. Runner 先发布消费方名单与协议绑定（§5 第 9 步）；
2. Provider 在 `calculate` 中据此推导每个消费方的 IdP 端点；
3. 应用在自己的 `calculate` 中读取端点并发布注册请求；
4. Provider 在 `render_env` 中读取完整注册请求，生成私有配置。

端点只依赖消费方名单和协议（Runner 已知），注册请求才依赖端点，因此顺序单向，
不构成环。

Runner 在所有 `calculate` 完成后、Provider `render_env` 前校验登出声明：OIDC method 与
`OIDC_LOGOUT_URI` 必须成对，method 只能是 `backchannel`/`frontchannel`，session-required
只能是布尔值；SAML SLS URL 与 binding 必须成对，binding 只能是 `redirect`/`post`。
声明为空仍表示应用不支持 IAM 发起的即时登出，Provider 不得把普通 `/logout` 页面猜成
标准通知 endpoint。环境所有权继续由消费方持有，只有显式消费 `ANAS_IAM_CLIENT_*` 的所选
Provider 在 render 阶段收到这些字段。

## 7. 锁文件与切换语义

provider 是部署级且唯一，因此锁文件把它记录一次，只按应用记录协议：

```yaml
iam:
  provider: llng
bindings:
  nextcloud:
    iam.interface: saml
  netbird:
    iam.interface: oidc
```

如果保持当前 `map[string]map[string]string` 结构，过渡期可继续使用复合键，但
长期建议使用结构化记录。

当配置从 LLNG 切到 Keycloak，或应用从 SAML 切到 OIDC 时，不应当作普通容器重启。
`iam.provider` 与清单参数 `iam_protocol` 应标记为 `reconcile`：先生成新客户端
配置，校验回调地址和 secret，再切应用，最后停止旧 IAM。第一版若尚未实现自动
协调，普通 `start` 必须沿用现有配置变更保护并提示显式执行 reconcile，而不能
静默切换。

## 8. 迁移步骤

### 阶段 A：Runner 与 ABI

- 增加 v2 清单结构、能力索引、双协议准入检查和协议解析；
- 增加顶层 `iam.provider` 与 `iam.default_protocol`；
- 注入绑定环境变量并扩展锁文件；
- `plan` 输出 `app -> interface`，而不只输出启动顺序；
- 保留 v1 数据库 `requires_one` 行为。

### 阶段 B：LLNG 与现有应用

- LLNG 升级到 v2，声明 `iam[oidc,saml]`，并按消费方发布端点（LLNG 的端点是
  部署级单例，各消费方拿到相同值）；
- NetBird 改成只消费 OIDC 通用变量，删除 `NETBIRD_SSO_PROVIDER` 分支；
- Nextcloud 把 `SMAL_SP_*`（现有拼写也应一并纠正为内部 SAML 映射）迁移为通用
  客户端注册请求，并停止读取 `LLNG_SAML_*`；
- 对已有 LLNG 部署提供一次锁文件迁移，默认保留原协议，不改变 secret。

### 阶段 C：第二个双协议 IAM

选择 Authentik 而不是 Keycloak，是因为它是 per-app 端点形状，能真正验证 §6.2 的
按消费方端点契约。若第二个 IAM 也是单例端点形状（Keycloak 与 LLNG 同类），契约中
"端点可以随消费方不同"这一维度不会被任何测试覆盖，缺陷会留到之后才暴露。

- 新增 Authentik Module，声明 `iam[oidc,saml]`；
- 在 `calculate` 中按 Runner 发布的消费方名单推导每个应用的 SAML/OIDC 端点；
- 自行生成 SAML 签名密钥对，因此 `SAML_SIGNING_CERT` 在 `calculate` 阶段就可发布，
  而不必等 Authentik 首次启动时自行生成；
- 读取通用客户端注册请求，生成 Authentik 的 Application 与 Provider blueprint。

### 阶段 D：清理私有耦合

- 禁止应用 Hook 读取 `LLNG_*`、`KEYCLOAK_*`、`AUTHENTIK_*` 等 IAM 私有端点；
- 禁止应用 Hook 读取 §6.2 的部署级便利端点变量。这条检查必须按变量名精确列举
  （`ANAS_IAM_OIDC_ISSUER_URL`、`ANAS_IAM_OIDC_DISCOVERY_URL`、
  `ANAS_IAM_SAML_METADATA_URL`、`ANAS_IAM_SAML_ENTITY_ID`、
  `ANAS_IAM_SAML_SSO_URL`、`ANAS_IAM_SAML_SLO_URL`），**不能按
  `ANAS_IAM_OIDC_*` / `ANAS_IAM_SAML_*` 前缀匹配**，否则会误伤 §6.1 中允许读取
  的 `ANAS_IDENTITY_OIDC_CLIENTS` 和 `ANAS_IDENTITY_SAML_CLIENTS`；
- 增加清单/源码静态测试防止重新引入实现名分支；
- 移除 `features.sso_provider` / `features.sso_client` 布尔字段，能力信息统一由
  `capabilities` 表达。

## 9. 必要测试

Runner 单元测试：

- `llng + nextcloud(auto)` 选择 OIDC；
- `llng + nextcloud(saml)` 选择 SAML；
- `llng + netbird(auto)` 选择 OIDC；
- 设置 `iam.default_protocol: saml` 时，`nextcloud(auto)` 选 SAML，而只支持
  OIDC 的 `netbird(auto)` 仍选 OIDC；
- `netbird(saml)` 因越出自身 `any_of` 在 `plan` 阶段失败；
- 未设置 `iam.provider` 且存在消费者时失败，且不因候选唯一而自动选择；
- 指定不存在、被禁用或不提供 IAM 能力的 Module 时失败；
- 只声明 `oidc` 的 IAM Module 在清单加载阶段被拒绝；
- Provider Hook 未为某个 SAML 绑定的消费方发布
  `ANAS_IAM_BINDING__<APP>__SAML_METADATA_URL` 时失败；
- 反向用例：本次部署没有 SAML 消费方时，`ANAS_IDENTITY_SAML_CLIENTS` 为空，Provider
  不发布任何 SAML 端点也应成功，校验只覆盖已绑定协议；
- 名单划分不变量：`ANAS_IDENTITY_OIDC_CLIENTS` 与 `ANAS_IDENTITY_SAML_CLIENTS` 无交集，
  且并集等于 IAM 消费方集合；
- 协议列表随解析结果变化：`nextcloud` 从 `saml` 改为 `auto` 且
  `iam.default_protocol: oidc` 时，它从 SAML 列表移动到 OIDC 列表；
- per-app 端点用例：伪造一个为每个消费方发布不同端点的 provider，验证各应用读到
  的是自己的端点而非彼此的；
- 消费方 Hook 读取部署级 `ANAS_IAM_OIDC_*` 而非自己的绑定端点时，被静态测试
  拦截；
- 两个 IAM 被显式列入启动模块时失败；
- 清单中出现 provider 选择字段时失败，防止绕过约束 4；
- 锁文件稳定保留 provider 与各应用 interface；
- 显式修改 `iam.provider` 或 `iam_protocol` 被配置生命周期保护拦截。

集成测试矩阵：

| IAM | 声明协议 | 端点形状 | NetBird | Nextcloud OIDC | Nextcloud SAML |
| --- | --- | --- | --- | --- | --- |
| LLNG | OIDC, SAML | 部署级单例 | 成功 | 不适用 | 成功 |
| Authentik | OIDC, SAML | per-app | 成功 | 不适用 | 成功 |

矩阵必须同时覆盖两种端点形状，否则 §6.2 的按消费方端点契约实际上未被验证。
"Nextcloud OIDC"列不适用，原因见 §12。

测试不仅检查容器为 running，还应请求 OIDC discovery/SAML metadata，完成一次重定向
流程，并确认 IAM 中已生成对应 client/SP 配置。

## 10. 关键决策

1. **选择由 Runner 完成。** Module 环境变量用于消费解析结果，而不是让每个 Module
   自己扫描 `LLNG_*`、`KEYCLOAK_*` 后猜测提供方。
2. **provider 是部署级且唯一，协议是应用级。** 一个 IAM 同时服务 OIDC 和 SAML
   应用，用户只登录一次。
3. **provider 必须显式指定。** 不提供默认值，不因候选唯一而自动选择。
4. **IAM 必须双协议。** 准入条件在清单加载阶段执行，从根本上消除协议不匹配。
5. **端点按消费方发布，不是部署级单例。** IdP 端点是否随应用变化属于实现差异
   （LLNG/Keycloak 单例、Authentik per-app），必须由契约吸收。单例形状是
   per-app 的特例，反之不成立，所以契约取更一般的那个。
6. **新增 IAM 不修改消费方。** 只要新 Module 满足准入条件并实现统一环境契约，就能
   被现有应用选择。
7. **不静默切换。** IAM 和协议绑定写入锁文件，变更进入 reconcile 流程。

## 11. 明确不做

以下能力被有意排除，记录原因以免后续被当作遗漏重新引入：

- **多 IAM 同时活动。** 跨 IAM 没有共享会话，用户需要登录两次，违背 SSO 目的。
  若将来确有需求，正确方向是 IAM 联邦（一个 IAM 作为另一个的上游），而不是并列
  多个活动 provider。
- **Module 或应用级指定 provider。** 一旦允许，等价于多 IAM，同样导致多次登录，
  并让 `iam.provider` 不再是可信的部署级事实。
- **provider 优先级列表 / 按协议的默认 provider。** 这些机制只在多 IAM 下才有
  意义。
- **单协议 IAM。** 接受它就必须重新引入协议交集为空的整套失败路径和用户可见的
  组合矩阵。
- **通过宿主环境变量覆盖 IAM 选择。** 相同配置文件必须产生相同部署。

## 12. 实现与本文档的偏差

实现过程中发现三处正文与现实不符，这里记录结论和原因。

### 12.1 Nextcloud 已增加 OIDC，SAML 保留为 fallback

Nextcloud 现已接入官方 `user_oidc`，实际声明为 `any_of: [oidc, saml]`，并优先
OIDC。OIDC 使用授权码流，`preferred_username` 对齐 LDAP 的 `sAMAccountName`
Internal Username；`auto_provision=false`，因此 IAM 只认证，用户和组仍由 LDAPS
backend 管理。原有 `user_saml` 路径保留，只有显式选择 `iam_protocol: saml` 时启用。
Runner 的 provider-neutral capability 解析不需要为这项新增支持做特例。

MeshCentral 同时加入 OIDC-only IAM capability：OIDC 负责认证和管理员组 claim，
LDAPS 继续负责目录同步。两个 consumer 的完整授权码链路由服务器 E2E 验证。

### 12.2 SP 密钥归属改变

迁移前 Nextcloud 直接读 `LLNG_SAML_SERVICE_PRIVATE_KEY`，把 IdP 的私钥当作
自己的 SP 私钥使用。通用契约不能这样做：它会要求每个 IAM 都交出签名私钥，而
Authentik 这类实现无法满足。

现在 Nextcloud 生成自己的 SP 密钥对，IdP 只发布公钥证书
（`SAML_SIGNING_CERT`）。LLNG 本来就通过 `curl` 抓取 SP metadata，会自动取到新的
SP 证书。**这是本次改动中唯一改变了运行时密钥材料的地方，需要在真实部署上验证
一次 SAML 登录。**

### 12.3 `ANAS_IAM_PORTAL_URL` 不是 Runner 发布的

§6 原先把它列在 Runner 的部署级变量里，但 Runner 在 Hook 运行前并不知道
provider 的域名。它由 Provider 的 `calculate` 产出，已在正文更正。

## 13. 部署环境注意事项

以下两点来自一次真实服务器部署，都与设计无关，但会让部署失败或误判。

### 13.1 samba_dc 的 :53 不与 systemd-resolved 冲突

`samba_dc` 使用 `network_mode: host`，容易让人以为它会占用主机 `0.0.0.0:53`。实际
配置是：

```
listen-on port 53 { 127.0.0.1; ${SAMBA_DC_DNS_SERVER}; };
```

它只绑 `127.0.0.1` 和 `SAMBA_DC_DNS_SERVER` 指定的地址，而 systemd-resolved 的存根
绑的是 `127.0.0.53` 和 `127.0.0.54`。两者不冲突，**不需要关闭 DNSStubListener**。

反过来，关闭存根监听在使用透明代理（v2raya 一类）的主机上会直接切断出站：这类
代理依赖劫持 `127.0.0.53` 做分流，绕过它之后对被墙域名的 TLS 握手会被重置，表现为
所有 `curl` 返回 000，很容易被误判成网络故障。

### 13.2 ghcr.io 镜像需要单独处理

`daemon.json` 的 `registry-mirrors` **只对 docker.io 生效**。ghcr.io 的 manifest 虽然
能通过镜像源解析，blob 却会重定向到 `pkg-containers.githubusercontent.com`，该域名
不在镜像源代理范围内。

在无法直连 ghcr.io 的网络里，按镜像源域名拉取再重打回原名即可，不需要修改任何
Dockerfile：

```sh
docker pull <mirror>/goauthentik/server:2024.10.5
docker tag  <mirror>/goauthentik/server:2024.10.5 ghcr.io/goauthentik/server:2024.10.5
```

当前需要这样处理的镜像：`linuxserver/baseimage-ubuntu`（noble 与 jammy）、
`ylianst/meshcentral`、`goauthentik/server`。
