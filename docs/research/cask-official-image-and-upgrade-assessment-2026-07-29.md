# Cask 官方镜像切换与版本升级评估

评估日期：2026-07-29
实施更新：2026-08-01、2026-08-02

## 1. 实施前提

当前项目尚未发布，因此本轮不兼容旧数据目录和旧镜像的原地升级，只保证全新部署后的功能契约不变。数据库、证书账户和容器内部目录均按新版本初始化，不提供旧版本回滚或数据迁移路径。

本轮先处理基础服务：

- 证书与入口：`lego`、`traefik`。
- 数据库：`postgres`、`mariadb`，以及附属 `adminer`。
- 网络与通信：`ddns`、`eturnal`、`freeradius`。
- 目录和文件服务基础：`samba_dc`、`samba_fs`。

2026-08-02 已继续处理应用层 cask：Authentik、Nextcloud、Collabora、MeshCentral、NetBird、LAM、LLNG。

## 2. 升级结果

| Cask | 原版本或镜像 | 新版本或镜像 | 镜像策略 | 功能保持措施 |
| --- | --- | --- | --- | --- |
| `lego` | `goacme/lego:v4.11.0` | `goacme/lego:v5.3.1` | 保留最小派生镜像 | 保留内部 CA、ACME DNS-01、周期续期和证书发布目录；CLI 改为 v5 的 `run --renew-days` |
| `traefik` | `traefik:v2.5.1` 派生镜像 | `traefik:v3.7.10` | 官方镜像的最小入口包装 | 容器启动时在 `/run/anas` 生成 `cert.yml`，保留 Docker provider、实例约束、Dashboard BasicAuth 和 HTTPS 入口；增加 ping 健康检查 |
| `postgres` | `postgres:15.3-alpine` | `postgres:18.4-alpine` | Docker Official Image | 两个 PostgreSQL 服务同步升级；按 PostgreSQL 18 目录模型将持久化挂载点改为 `/var/lib/postgresql`；保留初始化和数据库 reconcile 脚本 |
| `mariadb` | `linuxserver/mariadb:10.11.4` | `mariadb:12.3.2` | 切换 Docker Official Image | 数据目录改为 `/var/lib/mysql`，配置只读挂载到 `/etc/mysql/conf.d`；删除新版本已移除的 InnoDB 参数；保留 root 凭据和 MySQL 兼容导出 |
| `adminer` | 浮动 `adminer` | `adminer:5.5.0` | Docker Official Image | PostgreSQL 和 MariaDB 的可选 UI 同步固定版本，保留原 Traefik 路由和主题环境变量 |
| `ddns` | 无 tag 的 `qmcgaw/ddns-updater` | `ghcr.io/qdm12/ddns-updater:2.10.0` | 直接使用上游镜像 | 保留 DNSPod 配置、IPv4/IPv6 判断、Web UI 和 Traefik 路由；使用新版 `LISTENING_ADDRESS` 和健康服务变量 |
| `eturnal` | `eturnal/eturnal:1.12.0` 派生镜像 | `ghcr.io/processone/eturnal:1.12.2` | 上游镜像的最小入口包装 | 容器启动时在 `/run/anas` 生成配置；保留 TCP/UDP 监听、共享 secret 和 relay 范围，并补齐 relay UDP 端口发布 |
| `freeradius` | 镜像 `3.2.7`，元数据误写 `4.4.0` | `freeradius/freeradius-server:3.2.10` | 直接使用上游镜像 | 保留 1812/1813 UDP 和 `freeradius -XC` 健康检查；修正 cask/app 版本 |
| `samba_dc` | LinuxServer Ubuntu 24.04 base，Samba 4.19 系列 | Docker Official `ubuntu:resolute`，Samba 4.23.6 包 | 官方基础镜像加项目服务层 | 用 `tini`/`runit` 替代 LinuxServer s6，保留 BIND9-DLZ、AD 初始化、TLS、DNS、密码策略和 `/var/lib/samba` 布局 |
| `samba_fs` | LinuxServer Ubuntu 22.04 base，Samba 4.15 系列 | Docker Official `ubuntu:resolute`，Samba 4.23.6 包 | 官方基础镜像加项目服务层 | 用 `tini`/`runit` 运行现有服务脚本，保留域加入、winbind、Avahi、wsdd2、ACL 和共享配置 |

### 应用服务

| Cask | 原版本或镜像 | 新版本或镜像 | 镜像策略 | 功能保持措施 |
| --- | --- | --- | --- | --- |
| `authentik` | 2024.10.5，外置 Redis | 2026.5.6 | Authentik 官方 server/worker 镜像 | 按新版拓扑移除 Redis，保留 PostgreSQL、bootstrap 管理员、OIDC provider、SAML provider、Traefik 路由和持久化 `/data` |
| `collabora` | 23.05.0-2 自定义派生镜像 | `collabora/code:26.04.2.4.1` | 直接使用官方镜像 | 使用官方 `--use-env-vars` 配置入口，保留 WOPI allowlist、Nextcloud 集成、TLS 终止和健康探针 |
| `lam` | 7.8.1，LinuxServer 派生镜像 | 9.6.0 | Docker Official `debian:trixie-slim` 加 LAM 官方 `.deb` | 保留中文界面、LDAPS、Samba AD profile、密码修改和 Traefik 路由；下载包固定 SHA-256 |
| `llng` | 2.0.64，旧 LinuxServer 服务层 | 2.23.2 | LemonLDAP::NG 官方镜像的最小配置层 | 保留 PostgreSQL 配置存储、LDAP 登录、Nextcloud SAML、NetBird OIDC、应用门户和内部 CA 信任 |
| `meshcentral` | 1.1.6 自定义 npm 镜像 | 1.2.4 | MeshCentral 官方 GHCR 镜像的最小依赖层 | 仅补充 LDAP/MySQL 运行依赖和动态配置，保留 LDAP 登录、MariaDB、Traefik 路由和持久化目录 |
| `netbird` | 0.25.6 experimental 拓扑 | management/signal/relay 0.76.1，dashboard 2.90.9 | 官方多容器拓扑 | 保留外部 LLNG OIDC，新增 relay，管理存储切到新版支持的 SQLite，保留 TURN、WebSocket/gRPC 路由和内部 CA 信任 |
| `nextcloud` | 30.0.1，自制 push/Imaginary/Talk 镜像，Redis 7.4 | 34.0.2，Redis 8.10.0 | Nextcloud 官方 Apache 基础镜像和官方 AIO 配套镜像 | 保留 LDAP、SAML、Collabora、Talk、notify_push、Imaginary、Memories、WebDAV 和 cron；应用版本固定到 NC 34 兼容版本 |

## 3. 官方镜像判断

以下服务已消除无必要的本地 Dockerfile：

- `traefik`：原派生层只复制一个配置文件，改为官方镜像加只读挂载。
- `mariadb`：从 LinuxServer 第三方镜像切换到 MariaDB 官方镜像。
- `eturnal`：原派生层只生成配置并改启动命令，改为官方镜像加渲染模板。
- `freeradius`：原 Dockerfile 只包含 `FROM`，直接使用上游镜像。

`lego` 继续使用上游官方镜像作为基础，因为内部 CA、OpenSSL、cron 和证书发布逻辑是该 cask 的现有功能。

Samba 没有 Docker Official Image。社区应用镜像不能直接覆盖当前 BIND9-DLZ、域结构创建、域成员加入、Avahi/wsdd2 和数据目录约定，因此保留项目服务层，但基础镜像已切换为 Docker Official `ubuntu:resolute`。LinuxServer s6 依赖已移除，现有初始化脚本由发行版 `tini`/`runit` 托管；Samba 使用 Ubuntu 26.04 的 4.23.6 安全更新包，不引入源码编译。

## 4. 无兼容性约束的影响

- PostgreSQL 18 使用新的版本化 `PGDATA`，旧的 PostgreSQL 15 数据目录不能直接复用。
- MariaDB 官方镜像使用 `/var/lib/mysql`，旧 LinuxServer `/config` 目录不能直接复用。
- Lego 5 更改账户目录并移除 `renew` 命令；本轮只支持新建 v5 账户，不自动迁移 v4 状态。
- 删除的本地镜像 tag 不再作为运行时接口；功能由固定上游镜像和挂载配置提供。

## 5. 功能验证清单

### 静态和编排

1. 所有 `cask.yml` 可解析，`version`、`revision` 和 `app_version` 与镜像发布身份一致。
2. 所有 Compose 文件可完成变量替换和 `docker compose config`。
3. Traefik 和 Eturnal 容器入口可从作用域化环境生成合法 YAML，仓库中不存在 ERB 模板。
4. 不再存在本轮基础服务的无 tag 上游镜像。

### 服务功能

1. Lego：内部 CA 启动、ACME 首次签发、ACME 续期、证书权限和共享路径。
2. Traefik：自定义证书、Dashboard BasicAuth、Docker 标签发现、HTTP/WebSocket/gRPC 路由。
3. PostgreSQL：空目录初始化、消费者数据库创建、密码连接、Adminer。
4. MariaDB：空目录初始化、配置加载、root 密码连接、MySQL 兼容变量、Adminer。
5. DDNS：DNSPod A/AAAA 更新、Web UI、健康检查和无 IPv6 主机降级。
6. Eturnal：STUN、TURN REST 凭据、TCP/UDP 3478 和 UDP relay 端口。
7. FreeRADIUS：配置检查和 1812/1813 UDP 监听。
8. Samba DC/FS：AD provision、Kerberos、LDAP/LDAPS、BIND9-DLZ、域加入、共享访问和发现服务。

2026-08-01 已在远端隔离 Docker daemon 完成上述基础服务回归，结果见 `docs/test-server-base-services-2026-08-01.md`。

## 6. 应用层实施说明

1. 本轮按“未发布、无旧数据兼容”前提使用全新 workspace 验证，因此 Authentik 和 Nextcloud 直接初始化最新版，不提供跨大版本原地迁移脚本。
2. NetBird 保留多容器模式，因为 combined 模式内置身份提供方，无法保持现有 LLNG 外部 OIDC 功能。
3. Nextcloud 首次初始化会下载 98 MB Memories 地理库；现改为持久缓存、断点续传、固定 SHA-256 校验，再从本地缓存导入，避免网络中断后从零开始。
4. 应用服务远端回归结果记录在 `docs/test-server-application-services-2026-08-02.md`。

## 7. 上游依据

- Traefik releases: https://github.com/traefik/traefik/releases/latest
- Lego releases and v5 migration: https://github.com/go-acme/lego/releases/latest, https://go-acme.github.io/lego/migration/cli/
- PostgreSQL versioning and official image: https://www.postgresql.org/support/versioning/, https://hub.docker.com/_/postgres
- MariaDB releases and official image: https://mariadb.org/mariadb/all-releases/, https://hub.docker.com/_/mariadb
- Adminer official image: https://hub.docker.com/_/adminer
- DDNS Updater releases: https://github.com/qdm12/ddns-updater/releases/latest
- Eturnal container and changelog: https://eturnal.net/doc/container.html, https://eturnal.net/doc/changelog.html
- FreeRADIUS 3.2.10: https://github.com/FreeRADIUS/freeradius-server/releases/tag/release_3_2_10
- Samba stable release and Ubuntu package: https://devel.samba.org/samba/, https://packages.ubuntu.com/resolute/samba
- Authentik releases and Docker Compose installation: https://docs.goauthentik.io/releases/, https://docs.goauthentik.io/install-config/install/docker-compose/
- Nextcloud releases and official image: https://nextcloud.com/changelog/, https://hub.docker.com/_/nextcloud
- Collabora CODE image: https://hub.docker.com/r/collabora/code
- LemonLDAP::NG image: https://hub.docker.com/r/lemonldapng/lemonldap-ng
- MeshCentral releases: https://github.com/Ylianst/MeshCentral/releases
- NetBird self-hosting: https://docs.netbird.io/selfhosted/selfhosted-guide
- LDAP Account Manager releases: https://github.com/LDAPAccountManager/lam/releases
