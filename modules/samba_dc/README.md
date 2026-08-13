# Samba domain controller and DNS

Active Directory compatible domain controller, LDAP source, and BIND9-DLZ DNS server.

## Administrator identities / 管理员身份

日常目录管理员默认为 `admin`（由 `admin_name` 设置）。它加入 `Domain Admins`、
`Administrators`、`Group Policy Creator Owners` 和 ANAS 应用管理员组，足够日常用户、
组、组策略、文件服务和应用管理；同时有意移出 `Schema Admins` 与 `Enterprise Admins`。

内置 `Administrator` 保留给域初始化、底层恢复、架构/林级操作以及需要内置 RID 500
身份的工具，不作为日常登录账号。`admin_password` 与 `administrator_password` 均可直接
设置；省略时分别生成两个不同的随机 Secret，绝不共享默认密码。LDAP、password-write
和 anchor bind 账号也各自使用独立 Secret。

The routine `admin` account is a Domain Admin and is sufficient for ordinary
directory, policy, file-service, and application administration. Built-in RID
500 `Administrator` remains for provisioning and exceptional recovery. Their
module parameters are independent and omitted values generate distinct Secrets.

<!-- generated:localization:start -->
## 时区与语言 / Timezone and language

> 本节由 `localization.yml` 生成；请勿手工编辑。 / Generated from `localization.yml`; do not edit manually.

- Module version / 版本：`4.23.6-r5`（reviewed 2026-08-13）
- Timezone / 时区：`system` — Startup validates TZ against /usr/share/zoneinfo and installs /etc/localtime and /etc/timezone.
- Language scope / 语言范围：directory, Kerberos, and DNS protocol services
- Selection / 选择方式：`none`
- ANAS global defaults / 全局默认：`default_language=not_applicable`; `default_locale=not_applicable`
- Upstream format / 上游格式：none
- Fallback / 回退：No user-facing Web UI exists; automation should keep LC_ALL=C where stable machine-readable output is required.
- Supported languages / 支持语言：not applicable / 不适用

Evidence / 证据：

- [4.23.6 — protocol and command-line services without a Module UI](https://www.samba.org/samba/docs/current/man-html/)
<!-- generated:localization:end -->
