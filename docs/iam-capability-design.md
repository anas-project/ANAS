# IAM 多实现与协议能力设计

## 1. 目标

ANAS 应允许应用 Cask 对接不同 IAM 实现，例如 LemonLDAP::NG（LLNG）、
Authelia 和后续的 Keycloak，而不在应用 Hook 中按 IAM 名称写分支。

方案需要满足：

- 用户明确选择本次部署要启动的 IAM；
- IAM Cask 声明自己提供的协议，例如 `oidc`、`saml`；
- 应用 Cask 声明自己可以使用的协议以及优先顺序；
- Runner 在启动前计算 IAM 与应用协议的交集；
- 没有共同协议时，`plan`、`render`、`build`、`start` 都立即失败；
- Cask 通过统一环境变量读取解析结果，不依赖 `LLNG_*`、
  `AUTHELIA_*` 等实现私有变量；
- 选择结果进入锁文件，重启时保持稳定，切换 IAM 时给出明确的变更提示。

本设计只支持一个部署选择一个活动 IAM，但允许不同应用在同一 IAM 上分别选择
OIDC 或 SAML。多活动 IAM 可以以后在相同绑定模型上扩展，不应在第一版引入。

## 2. 当前实现的问题

当前 `requires_one` 已能从静态列表选择一个 Cask，并将绑定写入
`cask.lock.yml`。它适合 PostgreSQL/MariaDB 这种“实现名即能力”的依赖，但 IAM
还存在以下耦合：

1. 消费方必须列出所有实现名称，例如 `providers: [keycloak, llng]`；新增
   Authelia 时必须修改所有应用清单。
2. `netbird` Hook 直接判断 `NETBIRD_SSO_PROVIDER=keycloak|llng`，并读取
   `KEYCLOAK_OIDC_CONFIGURATION_ENDPOINT` 或
   `LLNG_OIDC_CONFIGURATION_ENDPOINT`。
3. `nextcloud` 默认写入 LLNG 使用的 `SMAL_SP_*` 变量，协议和 IAM 实现绑定。
4. `features.sso_provider: true` 只能表达布尔能力，不能表达实际支持 OIDC、SAML
   中的哪些协议。
5. 当前自动选择带默认实现，会在用户没有明确意图时自动加入并启动 IAM。

因此不建议继续扩展 IAM 名称分支，也不建议只增加
`AUTHELIA_OIDC_CONFIGURATION_ENDPOINT`。需要把“实现选择”和“协议选择”提升为
Runner 可验证的能力绑定。

## 3. 用户配置

新增顶层 `iam` 配置：

```yaml
modules:
  - nextcloud
  - netbird

iam:
  provider: authelia

services:
  nextcloud:
    env:
      iam_protocol: oidc
  netbird:
    env:
      iam_protocol: auto
```

规则：

- `iam.provider` 是部署级选择，值为 IAM Cask 名称；只要存在 IAM 消费方就必填。
- 被选择的 IAM 由 Runner 自动加入依赖闭包，用户无需同时写进 `modules`。
- `services.<app>.env.iam_protocol` 是可选的应用级覆盖；`auto` 使用该应用清单中
  的协议优先顺序。
- 如果 `modules` 同时显式列出另一个 IAM，Runner 报错，避免启动两个占用相同
  域名的 IAM。
- 没有 IAM 消费方时不自动启动 `iam.provider`。如果用户确实只想启动 IAM，可把
  该 IAM 同时列入 `modules`。

第一版不建议让宿主进程环境变量覆盖 `iam.provider`，否则相同配置文件可能产生
不同部署。临时试算可后续增加 `anas plan --iam authelia`，但持久配置仍是事实来源。

## 4. Cask 清单能力模型

该变更引入新的清单字段，建议升级为 `anas.cask/v2`，Runner 在迁移期同时读取
v1 和 v2；只有 v2 Cask 可以参与通用 IAM 绑定。

### 4.1 IAM 提供方

LLNG：

```yaml
abi:
  supports:
    - anas.cask/v2

capabilities:
  provides:
    - name: iam
      interfaces:
        - oidc
        - saml
```

Authelia：

```yaml
capabilities:
  provides:
    - name: iam
      interfaces:
        - oidc
```

`interfaces` 必须使用 Runner 已知的小写协议标识。第一版支持 `oidc` 和
`saml`；未知标识在加载清单时失败，不能静默忽略。

### 4.2 IAM 消费方

NetBird 只接受 OIDC：

```yaml
dependencies:
  requires_capabilities:
    - name: iam
      selected_by: global.iam.provider
      interface_selected_by: iam_protocol
      interfaces:
        any_of:
          - oidc
        prefer:
          - oidc
```

Nextcloud 可接受两种协议，但优先 OIDC：

```yaml
dependencies:
  requires_capabilities:
    - name: iam
      selected_by: global.iam.provider
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

- `any_of` 非空，表示至少匹配一个，不表示必须同时提供所有协议；
- `prefer` 必须是 `any_of` 的子集，并决定 `auto` 的选择顺序；
- `interface_selected_by` 使用应用参数名，映射成例如
  `NEXTCLOUD_IAM_PROTOCOL`；
- 如果未来确有“必须同时支持多个接口”的场景，再增加 `all_of`，第一版不预留
  含糊语义。

IAM 不再使用带静态 `providers` 列表的 `requires_one`。数据库等现有
`requires_one` 保持不变。

## 5. Runner 解析算法

解析发生在 Hook 执行之前：

1. 读取所有 Cask 清单，建立 `capability -> provider -> interfaces` 索引。
2. 收集已启用应用的 `requires_capabilities`。
3. 如果存在 `iam` 消费方，读取 `iam.provider`；为空则报错。
4. 验证指定 Cask 存在、未被禁用，并声明 `provides: iam`。
5. 对每个消费方计算：
   `consumer.any_of ∩ provider.interfaces`。
6. `iam_protocol` 为显式值时验证它位于交集内；为 `auto` 时按 `prefer` 选择
   第一个交集协议。
7. 交集为空立即报错，不运行 Hook、不生成密钥、不写运行目录。
8. 把 IAM Cask 加入每个消费方的依赖边，保证 IAM 的 `calculate` Hook 先运行。
9. 注入统一环境变量，再按现有顺序执行 Hook。
10. 成功 `start` 后把提供方和每个应用的协议绑定写入锁文件。

`plan` 仍保持只读，但需要执行清单级能力解析，因此能提前报告配置错误。

建议错误信息包含消费方、提供方、双方协议和修复动作，例如：

```text
nextcloud requires IAM protocol one of [saml], but provider authelia offers [oidc];
choose an IAM with SAML support or set nextcloud.iam_protocol to a supported protocol
```

```text
netbird requires IAM capability, but iam.provider is not set;
set iam.provider to one of: authelia, llng
```

```text
iam.provider "foo" does not provide capability "iam";
available providers: authelia[oidc], llng[oidc,saml]
```

## 6. 统一环境变量契约

Runner 和 IAM Provider Hook 发布以下只读变量：

```dotenv
ANAS_IAM_PROVIDER=authelia
ANAS_IAM_INTERFACES=oidc
ANAS_IAM_ISSUER_URL=https://auth.nas.example.com
ANAS_IAM_PORTAL_URL=https://auth.nas.example.com

ANAS_IAM_OIDC_DISCOVERY_URL=https://auth.nas.example.com/.well-known/openid-configuration
ANAS_IAM_OIDC_ISSUER_URL=https://auth.nas.example.com

ANAS_IAM_SAML_METADATA_URL=
ANAS_IAM_SAML_ENTITY_ID=
ANAS_IAM_SAML_SSO_URL=
ANAS_IAM_SAML_SLO_URL=
```

Runner 为每个消费方发布绑定：

```dotenv
ANAS_IAM_BINDING__NETBIRD__PROVIDER=authelia
ANAS_IAM_BINDING__NETBIRD__INTERFACE=oidc
ANAS_IAM_BINDING__NEXTCLOUD__PROVIDER=authelia
ANAS_IAM_BINDING__NEXTCLOUD__INTERFACE=oidc
```

应用 Hook 只读取自己的 `ANAS_IAM_BINDING__<APP>__INTERFACE` 和对应的通用端点。
例如 NetBird 不再出现 `switch keycloak/llng`，而是验证绑定为 `oidc` 后直接读取
`ANAS_IAM_OIDC_DISCOVERY_URL`。

端点变量由 Provider 的 `calculate` Hook 产生。Runner 在每个 Provider Hook 返回后
校验它为声明的每个协议发布了必需变量：

- OIDC：`ANAS_IAM_OIDC_ISSUER_URL`、`ANAS_IAM_OIDC_DISCOVERY_URL`；
- SAML：`ANAS_IAM_SAML_METADATA_URL`、`ANAS_IAM_SAML_ENTITY_ID`、
  `ANAS_IAM_SAML_SSO_URL`，SLO 可选。

这样可以区分“清单声称支持”和“运行配置实际可用”。缺少必需变量时在 Provider
Hook 后立即失败。

### 6.1 客户端注册请求

消费方继续负责生成自己的客户端 secret 和回调地址，但改用通用命名空间：

```dotenv
ANAS_IAM_CLIENTS=nextcloud,netbird

ANAS_IAM_CLIENT__NETBIRD__INTERFACE=oidc
ANAS_IAM_CLIENT__NETBIRD__CLIENT_ID=netbird
ANAS_IAM_CLIENT__NETBIRD__CLIENT_SECRET=...
ANAS_IAM_CLIENT__NETBIRD__REDIRECT_URIS=https://netbird.example/auth,...
ANAS_IAM_CLIENT__NETBIRD__POST_LOGOUT_REDIRECT_URIS=https://netbird.example
ANAS_IAM_CLIENT__NETBIRD__SCOPES=openid,profile,email,groups
ANAS_IAM_CLIENT__NETBIRD__ALLOW_GROUPS=APP_netbird,APP_all,Admins
```

SAML 客户端使用同一前缀，发布 `SP_METADATA_URL`、`ACS_URL`、
`NAME_ID_FORMAT` 等 SAML 字段。Provider 的 `render_env` Hook 读取所有通用注册请求，
翻译成 LLNG、Authelia 或 Keycloak 的私有配置。私有变量不得再由应用 Cask 生成。

由于当前生命周期先完成所有 `calculate`，再执行所有 `render_env`，这个方向与现有
Hook 顺序兼容：Provider 先发布端点，应用随后发布注册请求，Provider 在
`render_env` 时能读取完整的最终环境。

## 7. 锁文件与切换语义

锁文件建议扩展为：

```yaml
bindings:
  nextcloud:
    iam:
      provider: llng
      interface: oidc
  netbird:
    iam:
      provider: llng
      interface: oidc
```

如果保持当前 `map[string]map[string]string` 结构，可在过渡期保存两个键：

```yaml
bindings:
  nextcloud:
    iam: llng
    iam.interface: oidc
```

长期建议使用结构化记录，避免继续编码复合键。

当配置从 LLNG 切到 Authelia，或应用从 SAML 切到 OIDC 时，不应当作普通容器重启。
清单参数 `iam_provider`、`iam_protocol` 应标记为 `reconcile`：先生成新客户端配置，
校验回调地址和 secret，再切应用，最后停止旧 IAM。第一版若尚未实现自动协调，
普通 `start` 必须沿用现有配置变更保护并提示显式执行 reconcile，而不能静默切换。

## 8. 迁移步骤

### 阶段 A：Runner 与 ABI

- 增加 v2 清单结构、能力索引和协议解析；
- 增加顶层 `iam.provider`；
- 注入绑定环境变量并扩展锁文件；
- `plan` 输出 `app -> provider/interface`，而不只输出启动顺序；
- 保留 v1 数据库 `requires_one` 行为。

### 阶段 B：LLNG 与现有应用

- LLNG 声明 `iam[oidc,saml]` 并发布统一端点；
- NetBird 改成只消费 OIDC 通用变量；
- Nextcloud 把 `SMAL_SP_*`（现有拼写也应一并纠正为内部 SAML 映射）迁移为通用
  客户端注册请求；
- 对已有 LLNG 部署提供一次锁文件迁移，默认保留原协议，不改变 secret。

### 阶段 C：Authelia

- 新增 Authelia Cask，声明 `iam[oidc]`；
- 读取通用 OIDC 客户端注册请求生成 Authelia 配置；
- 复用现有 Samba/LDAP 能力时单独声明 LDAP 依赖，不把 LDAP 隐含进 IAM 能力；
- 用 NetBird 和 Nextcloud OIDC 完成端到端验证。

### 阶段 D：清理私有耦合

- 禁止应用 Hook 读取 `LLNG_*`、`KEYCLOAK_*`、`AUTHELIA_*` IAM 端点；
- 增加清单/源码静态测试防止重新引入实现名分支；
- Keycloak Cask 在成为真实 Keycloak 实现后再声明 v2 IAM 能力。当前仓库中的
  Keycloak 仍是基于 LLNG 集成资产的 scaffold，不应作为新接口的正确性基准。

## 9. 必要测试

Runner 单元测试：

- `llng + nextcloud(auto)` 选择 OIDC；
- `llng + nextcloud(saml)` 选择 SAML；
- `authelia + netbird(auto)` 选择 OIDC；
- `authelia + saml-only-app` 在 `plan` 阶段失败；
- 未设置 `iam.provider` 且存在消费者时失败；
- 指定不存在、被禁用或不提供 IAM 能力的 Cask 时失败；
- Provider 清单声明 OIDC 但 Hook 未发布 discovery URL 时失败；
- 两个 IAM 被显式列入启动模块时失败；
- 锁文件稳定保留 provider/interface；
- 显式修改 provider/interface 被配置生命周期保护拦截。

集成测试矩阵：

| IAM | 声明协议 | NetBird | Nextcloud OIDC | Nextcloud SAML |
| --- | --- | --- | --- | --- |
| LLNG | OIDC, SAML | 成功 | 成功 | 成功 |
| Authelia | OIDC | 成功 | 成功 | 预期在 plan 失败 |

测试不仅检查容器为 running，还应请求 OIDC discovery/SAML metadata，完成一次重定向
流程，并确认 IAM 中已生成对应 client/SP 配置。

## 10. 关键决策

1. **选择由 Runner 完成。** Cask 环境变量用于消费解析结果，而不是让每个 Cask
   自己扫描 `LLNG_*`、`AUTHELIA_*` 后猜测提供方。
2. **提供方是部署级，协议是应用级。** 一个 IAM 可以同时服务 OIDC 和 SAML 应用。
3. **没有协议交集必须提前失败。** 不允许降级为无 SSO，也不允许等容器启动后失败。
4. **新增 IAM 不修改消费方。** 只要新 Cask 实现清单能力和统一环境契约，就能被
   现有应用选择。
5. **不静默切换。** IAM 和协议绑定写入锁文件，变更进入 reconcile 流程。

