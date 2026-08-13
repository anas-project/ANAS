# 管理员账号系统设计

## 1. 目标

ANAS 的管理员不能再由一个全局 `admin` 字符串和一份共享密码隐式代表。系统必须明确
区分：身份从哪里来、通过什么协议登录、凭什么获得管理权限，以及 IAM/目录不可用时
如何进入只支持本地登录的 Module。

设计目标：

1. 日常管理员使用目录中的实名身份，权限由角色组决定，不按用户名判断。
2. LDAP provisioning 与 OIDC/SAML authentication 是两条可同时启用的独立链路。
3. 不能使用外部身份的 Module 获得独立随机本地密码和统一的查询、轮换体验。
4. 本地账号的物理用户名可以遵循统一模板，但上游固定账号允许声明例外。
5. 密码不进入普通清单、日志或长期容器环境；查询明文必须是显式敏感操作。
6. 已创建账号的名称进入部署状态；改模板不会偷偷创建第二个管理员。

非目标：把 Samba `Administrator`、数据库 `root`、`svc_*` 服务账号强制改成同一个
用户名；让所有应用共享一份本地密码；用门户可见性代替授权。

## 2. 四种对象

| 对象 | 例子 | 用途 | 日常使用 |
| --- | --- | --- | --- |
| 目录管理员身份 | Samba AD 中的 `admin`、实名 `alice` | 统一人员身份 | 是 |
| 管理角色 | `platform_admin`、`directory_admin`、`nextcloud.application_admin` | 决定权限 | 是 |
| Module 本地管理员 | `admin_ddns_go` | Module 只支持本地密码时的主要登录 | 是 |
| 本地恢复管理员 | `admin_nextcloud`、`akadmin` | IAM/LDAP 故障恢复 | 否 |

服务账号是第五类非人员身份，继续由各 provider 管理，不进入本设计的管理员 CLI。

## 3. 身份拓扑

### 3.1 仅本地登录

ddns-go 不接入 IAM：

```text
browser -> Traefik -> ddns-go native login
browser -> host:9876 -> ddns-go native login
```

两条入口使用同一份 Module 私有凭据。Traefik 路由不附加 ForwardAuth。

### 3.2 无本地登录、由外部门禁保护

ddns-updater 没有用户数据库：

```text
browser -> Traefik -> oauth2-proxy -> IAM/OIDC -> directory role -> ddns-updater
```

它不消费某个管理员用户名，只声明管理界面要求 `platform_admin` 角色。

### 3.3 LDAP provisioning 与 SSO 同时存在

Nextcloud 的账号进入和登录必须分别建模：

```text
Samba AD --LDAPS provisioning--> Nextcloud user/group
Samba AD -> IAM --SAML/OIDC authentication--> existing Nextcloud user
```

稳定身份锚点把 SSO subject 关联到 LDAP 已创建的用户。本地
`admin_nextcloud` 只用于恢复，不参与正常 SSO。

## 4. 用户配置

普通部署只配置 provider、首个目录管理员和本地账号默认策略：

```yaml
identity:
  directory:
    provider: samba_dc
  iam:
    provider: authentik
    default_protocol: oidc

administration:
  bootstrap:
    username: admin
    display_name: Administrator
    email: admin@example.com
    roles:
      - platform_admin
      - directory_admin
      - storage_admin
      - all_application_admins

  local_accounts:
    username_template: "admin_{module}"
    password_policy: generated_per_module
    password_length: 24

modules:
  nextcloud:
    identity:
      login_protocol: saml
```

IAM 只使用 `identity.iam`；顶层 `iam:` 不再接受。Module 必需
的 provisioning 不要求用户重复写 `directory_sync: true`，因为那是 Module 能力事实，
不是部署偏好。

`bootstrap` 只表示首次创建并授予角色的目录账号，不表示系统永远只有一个管理员。
后续人员通过角色成员关系管理。

本地账号允许逐 Module 覆盖用户名，但密码不接受 YAML 明文：

```yaml
modules:
  ddns_go:
    administration:
      local_accounts:
        primary:
          username: ops_ddns
```

## 5. Module Manifest 契约

### 5.1 Provisioning 与 authentication

```yaml
identity:
  provisioning:
    capability: directory
    interfaces:
      any_of: [ldaps]
    objects: [users, groups]
    identity_key: anasIdentityAnchor
    required: true

  authentication:
    capability: iam
    selected_by: login_protocol
    interfaces:
      any_of: [saml]
      prefer: [saml]
```

如果应用以后真正支持 OIDC，才把 `oidc` 加入 `any_of`。Manifest 不能声明尚未实现的
协议。

### 5.2 管理界面和本地账号

```yaml
management:
  surfaces:
    - id: web
      uri_from: DDNS_GO_DOMAIN_FULL
      authentication:
        primary: local

  local_accounts:
    - id: primary
      purpose: primary
      username:
        template: global
      credential:
        policy: generated_per_module
        container_format: bcrypt
      lifecycle:
        apply: reconcile
        rotate: rotate-ddns-go-local-admin
```

`purpose` 取：

- `primary`：正常登录只能使用该账号；
- `break_glass`：外部身份失效时使用；
- `embedded_guard`：应用无法关闭的第二层本地认证。

用户名固定的上游账号声明 `fixed_username: akadmin`。Runner 使用固定值而不是全局模板。

## 6. Runner 解析和 Secret

对每个启用的本地账号，Runner：

1. 读取锁定名称；没有锁时按 Module 固定值、服务覆盖、全局模板的顺序解析。
2. 校验字符集和长度并把结果锁定在 `.anas/local-admins.yml`。
3. 在 Secret Store 中以账号为粒度生成密码。
4. 仅在 Hook 需要的阶段把明文交给所属 Module。
5. Hook 尽可能只向容器发布 hash；bootstrap-only 应用只在初始化时获得明文。

Secret 逻辑键：

```text
ANAS_LOCAL_ADMIN__DDNS_GO__PRIMARY__PASSWORD
ANAS_LOCAL_ADMIN__NEXTCLOUD__BREAK_GLASS__PASSWORD
```

Module Hook 可获得账号私有别名：

```text
DDNS_GO_LOCAL_ADMIN_USERNAME
DDNS_GO_LOCAL_ADMIN_PASSWORD
```

这些键归 Module 所有并标记 sensitive，不能因依赖闭包传播给其他 Module。

`ACCOUNT` 在所有 Runner API 和 CLI 中都指 `management.local_accounts[].id`，不是
`username`。省略时按 `primary` → Module 唯一账号 → 歧义错误解析，因此单账号
`break_glass` Module 也可省略，而多账号 Module 不会猜测恢复账号。

`local-admins.yml` 不含密码，只保存 Module、账号 id、用途、已锁定用户名和 Secret 逻辑
键。它与 `secrets.generated.yml` 一起进入 snapshot/backup，并在恢复时一起回滚。

状态边界如下：

- `config.yml`：用户期望状态和用户名策略；不接受托管密码。遗留显式密码会阻断 render，
  删除后由 Module apply handler 把生成的托管密码写入应用；
- `config.lock.yml` 与 deployment manifest：版本、绑定、账号 ID 和 handler 契约；不含明文；
- `.anas/local-admins.yml`：账号 ID 到锁定物理用户名和 Secret 逻辑键的映射；不含明文；
- `.anas/secrets.generated.yml`：唯一持久 Secret Store（0600）；
- `.anas/runtime-secrets/`：bootstrap-only 容器的 0600 可再生运行时投影，不进入制品；
- 应用内部状态：只保存应用需要的 hash/凭据。修改成功并验证后，Secret Store 才提交。

初始化后改变普通 username override 会明确报错并指出锁定值，不能静默无效。通用
rename/migrate 命令尚不存在：只有应用提供“改名、验证、失败回滚”的专用 handler 后才能
声明该能力。

## 7. CLI

安全库存命令不显示密码：

```console
anas admin local list -w /srv/anas
```

显式凭据读取同时给出入口、用户名和密码：

```console
anas admin local credential ddns_go -w /srv/anas
anas admin local credential ddns_go --password-only
anas admin local credential ddns_go --json
```

轮换默认生成新随机密码；人工密码必须从 TTY 读取，不能放进 argv：

```console
anas admin local rotate ddns_go
anas admin local rotate ddns_go --prompt
```

轮换事务顺序是：生成候选值、调用 Module handler、验证登录、提交 Secret。失败时继续
保留旧值。对 bootstrap-only 应用不能只改环境变量，必须执行应用 API/CLI。

当前实际声明轮换能力的本地账号包括 ddns-go `primary`、Traefik `primary`、Nextcloud
`break_glass` 与 Authentik `break_glass`。
ddns-go handler 原子更新其持久配置 hash、重启并验证状态，失败恢复原文件；Nextcloud
handler 通过 `occ user:resetpassword --password-from-env` 更新，再用 Nextcloud 用户管理器
验证，失败时用旧 Secret 执行同一路径回滚；库存返回 `/login?direct=1` 作为 IAM 故障时
绕过 SAML 门禁的本地恢复入口。Authentik 使用固定上游账号 `akadmin`，首次启动从运行时
Secret 文件引导，后续通过 `ak shell` 更新并验证；Traefik 将 bcrypt 校验值原子写入活动
file-provider 配置，并用候选 BasicAuth 对真实 Dashboard 发起请求验证。其他 Module 不声明
handler，CLI 会报不支持。

首次 apply 只输出查询提示，不输出密码：

```text
ddns_go local administrator ready: anas admin local credential ddns_go
```

## 8. 配置边界

本系统不保留旧管理员字段或历史用户名迁移分支：

1. `SAMBA_DC_ADMIN_NAME` 属于目录 provider 身份，不得作为其他 Module 的本地管理员名；
2. Traefik 不再消费 `BASICAUTH_USER`、`BASICAUTH_PASSWD` 或
   `TRAEFIK_BASICAUTH_HTPASSWD`，Dashboard 账号只来自托管 `primary`；
3. `DEFAULT_SERVICE_ROOT_PASSWORD` 已删除；每个账号拥有独立 Secret；
4. 新部署用户名直接按固定值、Module override、全局模板解析并锁定；
5. 改用户名属于 `identity_migrate`，必须有应用专用事务；没有 handler 就明确不支持。

## 9. 验证要求

- Manifest 解码和非法声明测试；
- 用户名模板、固定用户名、锁定名称和 Secret 隔离单元测试；
- CLI 列表不泄密、credential 显式输出和 JSON 合同测试；
- ddns-go E2E：计划不包含 oauth2-proxy、域名登录和直连登录均验证本地密码；
- Nextcloud E2E：LDAP 用户已同步、SAML 登录关联同一用户、本地恢复账号可用；
- Authentik 真实容器测试：`akadmin` bootstrap、`ak shell` 轮换及旧密码失效；
- Traefik 真实容器测试：Dashboard BasicAuth 新密码生效、旧密码失效；
- 轮换成功和失败回滚测试；
- ln 主机执行 `test-all.sh` 与服务器身份/DDNS E2E。

## 10. 实施阶段

1. `management.local_accounts`、随机 Secret、只读 CLI，迁移 ddns-go；
2. 轮换 handler 与事务；
3. provisioning/authentication Manifest 归一化并迁移 Nextcloud；
4. provider-neutral 管理角色和授权 CLI；
5. 移除旧的共享管理员字段。

当前已完成阶段 1、阶段 2（对有真实 handler 的 ddns-go、Traefik、Nextcloud 与
Authentik）、阶段 3 的
Manifest/配置表达，以及本地管理员状态的 snapshot/backup 一致性。Nextcloud 已不再复用
Samba 管理员密码：首次安装使用运行时 Secret 文件，已有安装由真实 occ handler 更新；
默认用户名由全局模板直接解析为 `admin_nextcloud` 并锁定。

仓库已有 `server-authentik-nextcloud-login-e2e.sh` 覆盖 LDAP provisioning、SAML 浏览器
登录、目录 anchor 与同一 Nextcloud 用户映射；`server-nextcloud-local-admin-e2e.sh` 覆盖
真实 occ apply/rotate、旧密码失效、新密码验证和恢复入口。它们需要完整服务器环境，本地
单元测试不会冒充运行过这些链路。LAM 主登录使用已启用的 Samba `Admins` 组成员各自的
目录凭据；`lam` 是服务器 profile 名，LAM 私有密码仅保护配置编辑器。`Admins` 只授权
进入 LAM，目录读写权限仍由 Samba AD ACL 和高权限组决定。Collabora 使用
`admin_collabora` 规则名与 Module 私有密码；其密码省略时独立随机生成，但尚无可验证
回滚的 rotate handler，因此不加入托管账号。LLNG 的旧密码变量没有上游消费者，当前
Manager 仅接受 AD 和目录管理员组，不声明虚假的 `break_glass`。MeshCentral 上游在未设置 domain `auth` 时支持本地账号，但本 Module
使用 `auth: ldap`，同一 domain 没有可声称的本地绕过入口。仍未完成的是 Authentik/Traefik
新增 handler 的真实服务器 E2E、阶段 4 的 provider-neutral 角色授权 CLI，以及阶段 5 中
不属于本地账号的其余共享字段移除。这些能力继续如实标为未实现；尤其不会给没有
应用级 handler 的 Module 伪造 rotate 或 rename 能力。
