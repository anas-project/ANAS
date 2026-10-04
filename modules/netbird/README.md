# NetBird

尚未完成的 WireGuard overlay 网络 Module。

> [!WARNING]
> 当前生命周期为 `developing`，仅用于开发和验证，不属于推荐生产部署。

Casdoor r10 暂不接受 Netbird 注册：固定 0.76.1 把 OIDC sub 原值用于普通用户个人访问令牌 API 的 URL，违反 DIRKEY-R-013。须先修正 Consumer 投影；源码证据见技术文档。

## 快速信息

<!-- generated:module-facts:start -->
| 项目 | 值 |
| --- | --- |
| Module | `netbird` |
| 版本 / revision | `0.76.1-r5` |
| 状态 | `developing` |
| 类别 | `network` |
| 运行时 | `compose` |
<!-- generated:module-facts:end -->

## 依赖的 Module、Capability 与 Contract

| 依赖 | 类型 | 接口/版本 |
| --- | --- | --- |
| `traefik` | Module | — |
| `eturnal` | Module | — |
| `iam` | Capability | `oidc` |

## 最简配置

```yaml
modules:
  netbird: {}
```

本 Module 还要求在部署级选择 IAM Provider，例如：

```yaml
identity:
  iam:
    provider: llng
```

## 身份、用户与 Group

声明为 OIDC Consumer，并需要应用 Group，但管理员角色映射仍是发布阻塞项。

固定 Dashboard `2.90.9` 从 discovery 读取 logout endpoint 并发起 RP logout，但没有标准 IAM→Dashboard 通知 endpoint。ANAS 只在浏览器矩阵验证 `state`、本地 session 先失效和中央 session 结束后升级为“应用发起登出”；当前为“上游支持、待接入”，不声明后台双向登出。

| 能力 | 当前声明 |
| --- | --- |
| Directory / LDAPS | 不支持/不适用 |
| IAM | oidc |
| Group | `APP_netbird` / `APP_all` |
| 目录密码回写 | 不支持/不适用 |

当前没有通用的 `anas user/group/password` 子命令。目录型 Module 会按自身机制自动同步；用户、Group 和目录密码应在 Samba AD/LAM 或具备受限 LDAPS password-writeback 的应用中管理，不能用 `anas config set` 或 `env.<KEY>` 冒充目录操作。

### 目录属性变更说明

**匹配键**：OIDC `sub`。management 配置里 `AuthUserIDClaim` 显式设为 `sub`，NetBird 用它作为自己的
用户 id。**这个键在目录改名后是否稳定，取决于部署选了哪个 IAM Provider**——`authentik`（内部用户
UUID）与 `casdoor`（不可变 User ID）的 `sub` 改名后不变；`llng` 的 `sub` 默认取自登录名，改名后会变，
NetBird 会把同一个人当成新用户，其 peer、setup key 与访问策略成员资格全部留在旧用户下。

**本 Module 当前不请求 `anasIdentityAnchor` claim**：注册的 claim 是
`name:displayName`、`cn:cn`、`sAMAccountName:sAMAccountName`、`email:email`。`sAMAccountName` 只用于
显示，不进入任何持久键；持久键只有 `sub` 一个。

`AuthUserIDClaim` 可配置这一点是本 Module 在 M2 的机会：主体标识符切成 anchor 之后，这个字段可以
不动（`sub` 本身就是 anchor），也可以改指向 anchor claim；两条路都不需要上游新增能力。

| 目录侧变更 | NetBird 的行为 | 证据 |
| --- | --- | --- |
| `sAMAccountName` 改变 | Provider 的 `sub` 稳定时是同一用户，NetBird 侧不新建；界面显示名来自 `name` claim。Provider 的 `sub` 取自登录名时（`llng`）会产生第二个用户 | `推断`（依据 `NETBIRD_AUTH_USER_ID_CLAIM = "sub"` 的渲染值与各 Provider 的主体标识符形态；NetBird 没有身份类 E2E） |
| `mail` 改变 | 从 `email` claim 读取，刷新时机未经复核；不参与账号绑定 | `推断` |
| `displayName` 与其他 profile 属性 | 从 `name`/`cn` claim 读取，刷新时机未经复核 | `推断` |
| 直接或递归组成员变更 | **只影响能否登录**，在 IAM 侧按 `APP_netbird`/`APP_all`/管理员组判定，下次登录生效。**NetBird 的 group、访问策略与管理员角色由 NetBird 自己的数据库拥有，目录组不投影进去**；管理员角色映射本身仍是发布阻塞项 | 目录组不投影：`已验证`（Hook 与 management 配置中不存在 group→role 映射）；准入与收敛时刻：`推断` |
| 账号停用 | 下次登录被 IAM 拒绝。**已有 Dashboard 会话不失效**（固定 Dashboard `2.90.9` 没有 IAM→Module receiver）；更要紧的是**已注册 peer 与 setup key 根本不经过交互登录**，VPN 隧道会继续工作 | 无 receiver：`已验证`（见[Module IAM / OIDC 支持清单](/reference/module-iam-support)的登出矩阵）；peer/setup key 的存活：`推断` |
| 账号删除 | 同上，NetBird 用户连同其 peer 与 setup key 原样保留；**peer 归属不会自动转移或注销** | `推断` |
| 标识符回收再分配 | 取决于 Provider：`sub` 是内部不可变 id 时新人得到新用户（fail-closed）；`sub` 取自被回收的登录名时（`llng`）新人直接接上旧用户的全部 peer 与策略成员资格（**fail-open**，等于把一条已建立的 VPN 访问交给新人） | `推断` |

**兜底路径**——上表每一行"无自动路径"对应的运维动作：

1. 目录里停用或删除一个人时，必须在 NetBird 管理界面**删除该用户，并逐个撤销其名下的 peer 与
   setup key**。只停用目录账号不会断开已建立的隧道——这是本 Module 撤权风险最高的一条；
2. 删除用户前，先把仍需保留的 peer 转移给接手人或改为服务账号所有；
3. 组撤权要立即生效时，NetBird 侧的访问策略必须另行修改；目录组变更不会传播到策略。

## 管理员登录与 IAM 故障恢复

没有受支持的私有恢复管理员。IAM 故障时没有文档化的绕过入口。

本 Module 没有声明由 `anas admin local` 管理的账号；`credential` 和 `rotate` 对它不可用。

## 数据库支持

本 Module 不消费或提供关系数据库 Contract。

## 所有可用配置参数

以下清单来自当前 `module.yml` 和 `anas config list`。`环境变量` 是渲染后的 Module 私有键；不要把它当成首选配置接口。

| 路径 | 类型 | 约束 | 默认值 | 默认来源 | 环境变量 | 输入必填 | 必须解析 | 敏感 | 可编辑性 | 影响 | 作用 |
| --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- | --- |
| `netbird.domain_prefix` | string | — | `netbird` | `static` | `NETBIRD_DOMAIN_PREFIX` | 否 | 否 | 否 | 是 | `container_recreate` | 服务域名前缀 |
| `netbird.iam_protocol` | enum (`auto`, `oidc`, `saml`) | — | `auto` | `static` | `NETBIRD_IAM_PROTOCOL` | 否 | 否 | 否 | 是 | `container_recreate` | IAM 登录协议 |

### 查询和修改

```bash
anas config list netbird -w /srv/anas
anas config explain netbird.iam_protocol
anas config set netbird.iam_protocol oidc -w /srv/anas
anas config plan -w /srv/anas
```

`editable=false` 的参数不能用普通 `config set` 完成；表中的专用流程名称是生命周期声明，不保证存在同名通用子命令。原始 `env.<KEY>` 仅是兼容逃生口，不能用来轮换应用内部密码。

## 存储、备份与验证

持久数据应随 workspace 的 snapshot/backup 一起保护。数据库 Consumer 还必须备份所绑定的数据库 Resource；生成 Secret 和本地管理员状态也必须与数据保持同一恢复点。

`TURN_SECRET` 通过 `credentials.consumes` 显式绑定到 `eturnal.secret`。Runner 只有在 Eturnal 的
credential ready barrier 验证成功后才启动 NetBird；本 Module 不拥有或自行轮换该值。

```bash
anas plan -c /srv/anas/config.yml
anas config list netbird -w /srv/anas
anas status -w /srv/anas
```

## 当前限制

状态为 `developing`，不属于推荐部署。

## 技术文档

密码存储、环境作用域、Hook、网络、Resource 和测试细节见[技术文档](docs/technical.md)。

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`0.76.1-r5`（reviewed 2026-08-13）
- Timezone / 时区：`partial` — Dashboard, signal, and management receive the module environment; the relay service does not currently receive TZ.
- Language scope / 语言范围：NetBird Dashboard v2.90.9
- Selection / 选择方式：`fixed`
- ANAS global defaults / 全局默认：`default_language=not_consumed`; `default_locale=not_consumed`
- Upstream format / 上游格式：none
- Fallback / 回退：English is the only Dashboard language in the fixed source version.
- Supported languages / 支持语言（1）：`en`
- Notes / 说明：NetBird desktop client's i18n package is a different component and must not be used to claim Dashboard languages.

Evidence / 证据：

- [v2.90.9 — source tree contains no locale, i18n, or translation resources](https://github.com/netbirdio/dashboard/tree/v2.90.9)
<!-- generated:localization:end -->
