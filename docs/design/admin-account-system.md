# 管理员账号系统设计

## 1. 目标

ANAS 的管理员不能再由一个全局 `admin` 字符串和一份共享密码隐式代表。系统必须明确
区分：身份从哪里来、通过什么协议登录、凭什么获得管理权限，以及 IAM/目录不可用时
如何进入只支持本地登录的 Cask。

设计目标：

1. 日常管理员使用目录中的实名身份，权限由角色组决定，不按用户名判断。
2. LDAP provisioning 与 OIDC/SAML authentication 是两条可同时启用的独立链路。
3. 不能使用外部身份的 Cask 获得独立随机本地密码和统一的查询、轮换体验。
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
| Cask 本地管理员 | `admin_ddns_go` | Cask 只支持本地密码时的主要登录 | 是 |
| 本地恢复管理员 | `admin_nextcloud`、`akadmin` | IAM/LDAP 故障恢复 | 否 |

服务账号是第五类非人员身份，继续由各 provider 管理，不进入本设计的管理员 CLI。

## 3. 身份拓扑

### 3.1 仅本地登录

ddns-go 不接入 IAM：

```text
browser -> Traefik -> ddns-go native login
browser -> host:9876 -> ddns-go native login
```

两条入口使用同一份 Cask 私有凭据。Traefik 路由不附加 ForwardAuth。

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
    username_template: "admin_{cask}"
    password_policy: generated_per_cask
    password_length: 24

services:
  nextcloud:
    identity:
      login_protocol: saml
```

首期实现保留现有顶层 `iam:` 作为兼容入口；`identity.iam` 是最终归一形态。Cask 必需
的 provisioning 不要求用户重复写 `directory_sync: true`，因为那是 Cask 能力事实，
不是部署偏好。

`bootstrap` 只表示首次创建并授予角色的目录账号，不表示系统永远只有一个管理员。
后续人员通过角色成员关系管理。

本地账号允许逐 Cask 覆盖用户名，但密码不接受 YAML 明文：

```yaml
services:
  ddns_go:
    administration:
      local_accounts:
        primary:
          username: ops_ddns
```

## 5. Cask Manifest 契约

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
        policy: generated_per_cask
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

1. 读取锁定名称；没有锁时按 Cask 固定值、服务覆盖、全局模板的顺序解析。
2. 校验字符集和长度并把结果锁定在 `.anas/local-admins.yml`。
3. 在 Secret Store 中以账号为粒度生成密码。
4. 仅在 Hook 需要的阶段把明文交给所属 Cask。
5. Hook 尽可能只向容器发布 hash；bootstrap-only 应用只在初始化时获得明文。

Secret 逻辑键：

```text
ANAS_LOCAL_ADMIN__DDNS_GO__PRIMARY__PASSWORD
ANAS_LOCAL_ADMIN__NEXTCLOUD__RECOVERY__PASSWORD
```

兼容 Cask 可获得私有别名：

```text
DDNS_GO_LOCAL_ADMIN_USERNAME
DDNS_GO_LOCAL_ADMIN_PASSWORD
```

这些键归 Cask 所有并标记 sensitive，不能因依赖闭包传播给其他 Cask。

`local-admins.yml` 不含密码，只保存 Cask、账号 id、用途、已锁定用户名和 Secret 逻辑
键。它与 `secrets.generated.yml` 一起进入 snapshot/backup，并在恢复时一起回滚。

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

轮换事务顺序是：生成候选值、调用 Cask handler、验证登录、提交 Secret。失败时继续
保留旧值。对 bootstrap-only 应用不能只改环境变量，必须执行应用 API/CLI。

首次 apply 只输出查询提示，不输出密码：

```text
ddns_go local administrator ready: anas admin local credential ddns_go
```

## 8. 兼容与迁移

1. 现有 `SAMBA_DC_ADMIN_NAME` 先由 bootstrap username 派生，随后迁移为目录 provider
   私有输出；其他 Cask 改为消费角色或身份能力。
2. `BASICAUTH_USER` 不再作为通用管理员名；Traefik BasicAuth 若保留，应改成 Traefik
   私有参数。
3. `DEFAULT_SERVICE_ROOT_PASSWORD` 不再给本地管理员扇出；旧部署可读取原值作为首次
   迁移输入，但迁移后每 Cask 拥有独立 Secret。
4. 现有 `admin_nc`、`akadmin` 等名称进入状态锁，不因新模板自动重命名。
5. 改用户名属于 `identity_migrate`，必须通过显式命令执行。

## 9. 验证要求

- Manifest 解码和非法声明测试；
- 用户名模板、固定用户名、锁定名称和 Secret 隔离单元测试；
- CLI 列表不泄密、credential 显式输出和 JSON 合同测试；
- ddns-go E2E：计划不包含 oauth2-proxy、域名登录和直连登录均验证本地密码；
- Nextcloud E2E：LDAP 用户已同步、SAML 登录关联同一用户、本地恢复账号可用；
- 轮换成功和失败回滚测试；
- ln 主机执行 `test-all.sh` 与服务器身份/DDNS E2E。

## 10. 实施阶段

1. `management.local_accounts`、随机 Secret、只读 CLI，迁移 ddns-go；
2. 轮换 handler 与事务；
3. provisioning/authentication Manifest 归一化并迁移 Nextcloud；
4. provider-neutral 管理角色和授权 CLI；
5. 移除旧的共享管理员字段。

当前已完成阶段 1、阶段 3 的 Manifest/配置表达，以及本地管理员状态的快照和备份
一致性。阶段 2 的轮换命令、Nextcloud 实际 LDAP/SAML 登录 E2E、阶段 4 的角色授权和
阶段 5 的兼容字段移除仍是后续工作；因此目前 CLI 只有 `list` 与 `credential`，不会
假装提供尚未具备事务回滚能力的 `rotate`。
