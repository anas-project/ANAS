# Module 目录

本页是 `modules/*/module.yml` 的人类可读索引。版本和实验状态以各 Module manifest 为准。

## 稳定 Module

| Module | 类别 | 作用 | 主要关系 |
| --- | --- | --- | --- |
| `lego` | 证书 | 申请和保存通配符证书，并维护内部 CA 资料 | DNS 凭据按 Provider 配置 |
| `traefik` | 网络 | HTTPS 反向代理、Dashboard 和声明式路由 | 依赖 `lego` |
| `samba_dc` | 身份/DNS | AD 域控制器、LDAP、Kerberos 和 BIND9-DLZ DNS | 依赖 `lego` |
| `samba_fs` | 存储 | 加入域的 SMB 文件共享 | 依赖 `samba_dc`，需要 host LAN |
| `postgres` | 数据库 | PostgreSQL 和可选 Adminer | 提供 `relational_database` Contract |
| `mariadb` | 数据库 | MariaDB 和可选 Adminer | 提供 `relational_database` Contract |
| `eturnal` | 通信 | TURN 服务 | 由实时通信应用使用 |
| `nextcloud` | 应用 | 文件同步、分享、Talk、Memories、Imaginary 等 | 需要 Traefik、TURN、目录和关系数据库能力 |
| `collabora` | 应用 | Nextcloud 在线文档编辑后端 | 依赖 `nextcloud` |
| `llng` | IAM | LemonLDAP::NG 门户、OIDC/SAML Provider 和应用启动器 | 需要 Traefik、目录和关系数据库能力 |
| `lam` | 管理 | LDAP Account Manager Web 管理界面 | 需要 Traefik 和 Samba 目录 |
| `meshcentral` | 应用 | 使用 LDAP 的远程设备管理 | 需要 Traefik、目录和关系数据库能力 |
| `ddns_go` | 网络 | 支持 IPv4/IPv6 和多家中国 DNS 厂商的 DDNS | 可由 `dynamic_dns.provider` 选择 |
| `ddns_updater` | 网络 | 基础域名和通配符记录的 DDNS | 可由 `dynamic_dns.provider` 选择 |
| `oauth2_proxy` | IAM 网关 | 为没有登录能力的服务增加 OIDC 认证入口 | 需要 IAM Provider 和 Traefik |

## 实验性 Module

以下 Module 的 manifest 明确标记为 `experimental`，不应默认用于生产部署：

| Module | 当前边界 |
| --- | --- |
| `authentik` | IAM Provider 实现仍处于实验状态 |
| `netbird` | WireGuard overlay 拓扑尚不完整，不属于推荐部署 |
| `freeradius` | 只有服务骨架，未生成生产客户端和用户策略 |

## 选择与配置

Module 在 `config.yml` 中以映射选择：

```yaml
modules:
  traefik: {}
  nextcloud:
    config:
      domain_prefix: cloud
      db_type: auto
```

不要手工复制依赖清单。Runner 会根据 `requires`、Contract Provider 和配置选择解析完整顺序。查看最终结果：

```bash
anas plan -c /srv/anas/config.yml
```

查看可设置参数：

```bash
anas config list <module>
anas config explain <module>.<parameter>
```
