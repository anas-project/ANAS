---
title: LLNG Passkey/WebAuthn 与 Samba 共享边界
created: 2026-08-21
updated: 2026-08-21
evidence_as_of: 2026-08-21
---

# LLNG Passkey/WebAuthn 与 Samba 共享边界

## 结论

1. 当前 ANAS 使用 LLNG `2.23.2`，满足 Passkey 第一因素所需的 LLNG `2.20+` 版本门槛。
   推荐先上线“AD 密码 + WebAuthn 第二因素”，稳定后再增加 Passkey 免密码登录入口；不要一步
   删除密码登录和主机侧恢复路径。
2. 推荐由 LLNG 成为唯一 WebAuthn Relying Party（RP）。凭据的服务端部分保存在 LLNG 现有
   `persistentStorage`，也就是本 Module 的关系数据库 `psessions` 表；其他应用继续通过
   LLNG 的 OIDC/SAML 登录，不需要读取 Passkey 数据。
3. Samba AD **可以保存 WebAuthn 的服务端公钥凭据元数据**，但不能保存用户认证器中的
   Passkey 私钥。LLNG 也支持从映射到 `_2fDevices` 的 LDAP 属性读取外部设备数据。
4. 不建议把 Samba 当作“多个独立 IAM 共用同一 Passkey”的通用仓库。WebAuthn 凭据绑定
   RP ID，各 IAM 的序列化格式、origin 校验、计数器更新、登记与撤销流程也不是通用协议。
   仅仅共享 LDAP 属性，不能使另一个域名、另一个 RP ID 的 IAM 使用同一凭据。
5. `msDS-KeyCredentialLink` 是 AD/Windows Hello key-trust 的
   `KEYCREDENTIALLINK_BLOB`，不是 LLNG `_2fDevices` 或通用 WebAuthn 仓库；不要复用这个属性
   保存 LLNG JSON。

## 当前 ANAS 状态

- `modules/llng/module.yml` 固定上游版本为 `2.23.2`。
- `modules/llng/llng/root/root/lmConf.json` 当前使用 Samba AD 作为 `authentication`、
  `userDB` 和 `passwordDB`。
- 同一文件把普通会话和持久会话放入绑定的 Postgres/MariaDB；持久会话表为 `psessions`。
- LLNG 官方说明，persistent sessions 用于登录历史、二次认证设备和 OIDC consent。
  因此采用 LLNG 自助登记时，不需要先修改 Samba schema。
- 当前 ANAS 尚未声明 Passkey 配置参数，也没有 Passkey E2E；它应先作为显式 PoC，而不是
  直接成为生产唯一登录方式。

依据：[LLNG Sessions](https://lemonldap-ng.org/documentation/2.0/sessions.html)、
[LLNG WebAuthn second factor](https://lemonldap-ng.org/documentation/2.0/webauthn2f.html)、
[LLNG Passkeys / WebAuthn](https://lemonldap-ng.org/documentation/latest/authwebauthn.html)。

## 推荐架构

```text
用户认证器（平台 Passkey / 安全密钥）
        │ 私钥不离开认证器
        │ WebAuthn，RP ID = auth.<稳定域名>
        ▼
LLNG Portal（唯一 WebAuthn RP）
        ├── Samba AD：用户、启停状态、稳定身份锚点、Group、密码
        ├── LLNG psessions：credential ID、公钥、设备元数据、撤销状态
        └── OIDC / SAML
                 ▼
        Nextcloud / NetBird / 其他 Consumer 或下游 IAM
```

这条路线把“身份是否存在、是否停用、拥有哪些 Group”继续交给 Samba，把“如何完成
WebAuthn ceremony”交给 LLNG。用户被 Samba 停用、删除或移出应用组后，即使认证器里仍有
Passkey，也不能继续获得有效应用授权。

WebAuthn 规范要求凭据密钥对绑定到特定 RP；注册时只有公钥交给 RP，私钥保留在认证器。
凭据只能在登记时的 RP ID 范围内使用。依据：
[W3C Web Authentication Level 3](https://www.w3.org/TR/webauthn-3/)。

## 分阶段开启方案

### 阶段 0：上线门槛

在任何配置切换前完成以下检查：

- 固定长期 Portal 主机名，例如 `auth.example.com`。RP ID 一旦变化，旧凭据不能在新 RP ID
  下继续使用。
- 保持 HTTPS 且浏览器无证书错误；WebAuthn Web 页面必须运行在 secure context。
- 在实际镜像中验证 `Authen::WebAuthn` 可加载。LLNG 官方把它列为 WebAuthn 依赖；若上游
  镜像未包含，应在 `modules/llng/llng/Dockerfile` 安装发行版包并提升 Module revision。
- 明确恢复流程：每位用户至少登记两个凭据，推荐一个平台 Passkey 加一个独立硬件安全密钥；
  管理员能够删除丢失设备；保留 AD 密码和主机侧恢复。
- 把 LLNG 配置、关系数据库及 workspace secrets 放在同一备份恢复点。

镜像内依赖验证示例：

```bash
docker exec anas_llng perl -MAuthen::WebAuthn -e 'print "$Authen::WebAuthn::VERSION\n"'
```

### 阶段 1：先启用 WebAuthn 第二因素

先通过 LLNG Manager 的 `General Parameters > Second factors > WebAuthn` 配置，PoC 通过后再
把相同设置固化到 ANAS Module。建议值：

| 设置 | 建议 | 原因 |
| --- | --- | --- |
| Activation (`webauthn2fActivation`) | `on` | 已登记用户登录时执行 WebAuthn |
| Self registration (`webauthn2fSelfRegistration`) | `on` | 用户先用 AD 密码登录并登记 |
| User verification (`webauthn2fUserVerification`) | `required` | 要求 PIN/生物识别等本地用户验证 |
| Discoverable credential (`webauthn2fResidentKey`) | `required` | 为第二阶段 Passkey 第一因素做准备 |
| RP ID (`webauthnRpId`) | Portal 的精确稳定主机名 | 缩小凭据作用域，避免信任整个父域 |
| RP display name (`webauthnRpName`) | 组织可识别名称 | 降低用户误登记风险 |
| Display-name attribute (`webauthnDisplayNameAttr`) | `displayName` | 与当前 Samba exported variable 对齐 |
| Authentication level (`webauthn2fAuthnLevel`) | 高于密码认证级别 | 允许应用按认证强度授权 |
| Attestation | 先不要求 | 避免初期设备兼容性和证书信任运维成本 |
| User can remove key | 试点期关闭或仅在恢复 SOP 完成后开启 | 避免用户误删唯一可用凭据 |

LLNG 官方明确要求 Passkey 使用 discoverable credential 的 `preferred` 或 `required`；这里选择
`required`，以便 PoC 直接验证目标能力。官方也说明当前 WebAuthn 默认可做无 attestation
验证的登记；只有确有“限定企业硬件型号”的需求时才启用 attestation trust。

试点建议新增 Samba 组 `PasskeyPilot`：

- `sfManagerRule` 仅向试点组展示二次认证管理入口；
- `sfRequired` 仅对试点组强制首次登记；
- 不要一开始对 `Admins` 全员强制，先准备两名以上可恢复管理员并分别登记两个设备；
- 试点稳定后再逐组扩大，最后决定是否对全员强制。

LLNG 支持按规则强制首次 2FA 登记，也支持 Portal 自助管理。依据：
[LLNG Second Factors](https://lemonldap-ng.org/documentation/2.0/secondfactor.html)、
[LLNG parameter list](https://lemonldap-ng.org/documentation/2.0/parameterlist.html)。

### 阶段 2：增加 Passkey 免密码入口

LLNG `2.20+` 的推荐方式是使用 Authentication Choice：

- 保留现有 `AD` 认证选项，供首次登记和恢复使用；
- 新增 `WebAuthn` authentication 选项；
- WebAuthn authentication 仍使用现有 AD user database，以便登录后加载用户状态、属性和组；
- 若 WebAuthn 2F activation 使用了自定义规则，增加 `$_auth ne "WebAuthn"`，避免 Passkey
  第一因素成功后再次要求同一 WebAuthn 第二因素；
- OIDC/SAML Consumer 不需要变化，它们仍只信任 LLNG 发出的 token/assertion。

这一阶段仍不要删除 AD 密码选项。至少完成丢失设备、停用用户、改名、组撤权、数据库恢复、
RP 主机名恢复和管理员恢复测试后，才能决定是否把 Passkey 设为默认选项。

### 阶段 3：固化为 ANAS 能力

PoC 通过后，再把下列能力加入 `modules/llng/module.yml` 和启动配置，而不是依赖 Manager
手工漂移：

- `passkey_enabled`、`passkey_passwordless_enabled`；
- `passkey_rp_id`、`passkey_rp_name`；
- `passkey_required_group` 或明确的 rollout rule；
- user verification、resident key、attestation 策略；
- 是否允许用户自助删除及每用户最少凭据数；
- 对应中英文 README、technical 文档和服务器 E2E。

建议 E2E 至少覆盖：

1. 未登记普通用户仍能用 AD 密码登录；
2. 试点用户可登记平台 Passkey 和 roaming security key；
3. 登记后第二因素成功，authentication level 提升；
4. Passkey 第一因素不重复触发 WebAuthn 2F；
5. 删除或停用 Samba 用户后，Passkey 不能产生新会话；
6. 移出 `APP_*` 组后，认证成功但应用授权被拒绝；
7. 删除 credential 后立即失效；
8. 备份恢复后 credential、身份锚点和撤销状态一致；
9. 丢失单个设备后可用第二设备或受控管理员流程恢复。

## Passkey 数据能否放在 Samba

### 能放什么

可以保存的是 RP 侧数据，例如 credential ID、公钥、算法、sign counter、transports、用户
handle、设备名称和登记时间。认证器私钥、Touch ID/Face ID/Windows Hello 的生物特征不会
也不应写入 Samba。

Samba AD 支持与 Microsoft AD 同类的 schema extension，但 schema 变更默认关闭、不可逆且
需要先在恢复副本和 staging 验证。依据：
[Samba AD schema extensions](https://wiki.samba.org/index.php/Samba_AD_schema_extensions)。

LLNG 官方允许把 LDAP 属性映射成 `_2fDevices` JSON，以读取外部提供的 WebAuthn/TOTP 等
设备。由此可以设计一个专用多值或 JSON 属性，例如组织自有 OID 下的
`anasWebAuthnDevices`，并给 LLNG 受限读写 ACL。

### 为什么当前不推荐

- LLNG 自助登记默认写 persistent session；要让 Samba 成为权威存储，还需实现登记、更新
  counter、重命名、撤销和并发冲突的 LDAP 写回，不是只加一个 exported variable。
- JSON 整体单值写回容易发生并发覆盖；多值属性又需要定义稳定的逐凭据编码和删除语义。
- schema extension 是域级、近乎不可逆的变更，而当前 LLNG 数据库已经满足单一 RP 的高可用
  和备份需求。
- 把 credential metadata 暴露给多个服务账号会扩大敏感认证元数据的读取和篡改面。

因此，只有在“多实例 LLNG 必须跨独立数据库共享同一设备”或“已有统一凭据管理服务”这类
明确需求下，才值得做 Samba 存储 PoC。单纯为了让其他 IAM 使用，不足以支持 schema 变更。

## 能否共享给其他 IAM

### 不能直接共享的原因

W3C WebAuthn 规定 credential key pair 被限定在特定 RP ID；RP 还必须验证注册和认证响应中的
origin。比如为 `auth.example.com` 登记、RP ID 也是 `auth.example.com` 的凭据，不能直接在
`authentik.example.com` 或 `keycloak.example.com` 作为另一个 RP 使用。

即使多个 IAM 人为使用共同父域 `example.com` 作为 RP ID，也还必须共同解决：

- 精确允许哪些 origins；
- 相同 credential ID、公钥和 COSE 算法编码；
- sign counter 的原子更新与克隆检测；
- user handle 与 Samba 稳定身份锚点映射；
- attestation、备份、撤销、审计和账号停用语义；
- 各 IAM 是否原生支持外部 credential store。

这时它们实际上必须成为“同一个逻辑 WebAuthn RP 的多个前端”，而不是互不相关的 IAM。
直接把 RP ID 放宽到父域还会扩大可使用凭据的子域范围；如果某个允许 origin 或子域可运行
不受信任代码，会提高凭据滥用风险。

### 推荐共享方式：共享认证结果，不共享凭据

让 LLNG 完成 WebAuthn，然后通过 OIDC/SAML 把认证结果提供给其他 IAM/应用。需要表达强认证
时，应在协议映射中传递并校验认证上下文（例如 OIDC `acr`/`amr` 或 SAML AuthnContext），
而不是让每个 IAM 读取 LLNG credential 表。

这样只需一个系统实现登记、counter、撤销和恢复；其他 IAM 保持标准协议边界。若未来确实
需要多个上游 IAM 统一 Passkey，应引入一个独立、标准化的中央认证服务，并让各 IAM 作为
OIDC/SAML relying party，而不是把 Samba LDAP 当成 WebAuthn API。

## `msDS-KeyCredentialLink` 的边界

Microsoft 规范把 `msDS-KeyCredentialLink` 定义为 DN-Binary 属性，承载
`KEYCREDENTIALLINK_BLOB`；Windows Hello for Business key-trust 会把用户公钥写到该属性。
这是 Windows/AD key credential 协议的数据结构和约束，不等价于 LLNG WebAuthn device JSON。

依据：

- [Microsoft `msDS-KeyCredentialLink` attribute](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-ada2/45916e5b-d66f-444e-b1e5-5b0666ed4d66)
- [Microsoft `KEYCREDENTIALLINK_BLOB`](https://learn.microsoft.com/en-us/openspecs/windows_protocols/ms-adts/f3f01e95-6d0c-4fe6-8b43-d585167658fa)
- [Windows Hello for Business key storage](https://learn.microsoft.com/en-us/windows/security/identity-protection/hello-for-business/how-it-works)

结论是：若 Windows Hello/AD key trust 需要该属性，应按 Microsoft/Samba 的协议用途使用；
LLNG WebAuthn 若必须进入 Samba，应申请组织自有 OID 和专用 schema，不要混写
`msDS-KeyCredentialLink`。

## 最终建议

采用“LLNG 单一 RP + LLNG 数据库存凭据元数据 + Samba 存身份/组 + OIDC/SAML 联邦”的方案。
它与当前 ANAS 架构变化最小，也最符合 WebAuthn 的 RP 边界。Samba 自定义属性方案保留为后续
专项 PoC，不作为开启 Passkey 的前置条件；`msDS-KeyCredentialLink` 不进入 LLNG 方案。
