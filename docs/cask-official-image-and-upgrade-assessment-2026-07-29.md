# Cask 官方镜像切换与版本升级评估

评估日期：2026-07-29
实施更新：2026-08-01

## 1. 实施前提

当前项目尚未发布，因此本轮不兼容旧数据目录和旧镜像的原地升级，只保证全新部署后的功能契约不变。数据库、证书账户和容器内部目录均按新版本初始化，不提供旧版本回滚或数据迁移路径。

本轮先处理基础服务：

- 证书与入口：`lego`、`traefik`。
- 数据库：`postgres`、`mariadb`，以及附属 `adminer`。
- 网络与通信：`ddns`、`eturnal`、`freeradius`。
- 目录和文件服务基础：`samba_dc`、`samba_fs`。

应用层 cask（Authentik、Nextcloud、Collabora、MeshCentral、NetBird、LAM、LLNG）留到下一批处理。

## 2. 升级结果

| Cask | 原版本或镜像 | 新版本或镜像 | 镜像策略 | 功能保持措施 |
| --- | --- | --- | --- | --- |
| `lego` | `goacme/lego:v4.11.0` | `goacme/lego:v5.3.1` | 保留最小派生镜像 | 保留内部 CA、ACME DNS-01、周期续期和证书发布目录；CLI 改为 v5 的 `run --renew-days` |
| `traefik` | `traefik:v2.5.1` 派生镜像 | `traefik:v3.7.10` | 直接使用官方镜像 | 将生成的 `cert.yml` 只读挂载，保留 Docker provider、实例约束、Dashboard BasicAuth 和 HTTPS 入口；增加 ping 健康检查 |
| `postgres` | `postgres:15.3-alpine` | `postgres:18.4-alpine` | Docker Official Image | 两个 PostgreSQL 服务同步升级；按 PostgreSQL 18 目录模型将持久化挂载点改为 `/var/lib/postgresql`；保留初始化和数据库 reconcile 脚本 |
| `mariadb` | `linuxserver/mariadb:10.11.4` | `mariadb:12.3.2` | 切换 Docker Official Image | 数据目录改为 `/var/lib/mysql`，配置只读挂载到 `/etc/mysql/conf.d`；删除新版本已移除的 InnoDB 参数；保留 root 凭据和 MySQL 兼容导出 |
| `adminer` | 浮动 `adminer` | `adminer:5.5.0` | Docker Official Image | PostgreSQL 和 MariaDB 的可选 UI 同步固定版本，保留原 Traefik 路由和主题环境变量 |
| `ddns` | 无 tag 的 `qmcgaw/ddns-updater` | `ghcr.io/qdm12/ddns-updater:2.10.0` | 直接使用上游镜像 | 保留 DNSPod 配置、IPv4/IPv6 判断、Web UI 和 Traefik 路由；使用新版 `LISTENING_ADDRESS` 和健康服务变量 |
| `eturnal` | `eturnal/eturnal:1.12.0` 派生镜像 | `ghcr.io/processone/eturnal:1.12.2` | 直接使用上游镜像 | 将动态配置改为只读模板挂载；保留 TCP/UDP 监听、共享 secret 和 relay 范围，并补齐 relay UDP 端口发布 |
| `freeradius` | 镜像 `3.2.7`，元数据误写 `4.4.0` | `freeradius/freeradius-server:3.2.10` | 直接使用上游镜像 | 保留 1812/1813 UDP 和 `freeradius -XC` 健康检查；修正 cask/app 版本 |
| `samba_dc` | LinuxServer Ubuntu 24.04 base，Samba 4.19 系列 | Docker Official `ubuntu:resolute`，Samba 4.23.6 包 | 官方基础镜像加项目服务层 | 用 `tini`/`runit` 替代 LinuxServer s6，保留 BIND9-DLZ、AD 初始化、TLS、DNS、密码策略和 `/var/lib/samba` 布局 |
| `samba_fs` | LinuxServer Ubuntu 22.04 base，Samba 4.15 系列 | Docker Official `ubuntu:resolute`，Samba 4.23.6 包 | 官方基础镜像加项目服务层 | 用 `tini`/`runit` 运行现有服务脚本，保留域加入、winbind、Avahi、wsdd2、ACL 和共享配置 |

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

1. 所有 `cask.yml` 可解析，`version` 和 `app_version` 与镜像一致。
2. 所有 Compose 文件可完成变量替换和 `docker compose config`。
3. 所有 ERB 模板可渲染，生成的 Traefik 和 Eturnal YAML 可解析。
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

## 6. 后续批次

基础服务验证通过后，再处理应用层：

1. 先升级数据库消费者并确认 PostgreSQL 18、MariaDB 12 的支持范围。
2. 将 Collabora、Nextcloud、LAM、LLNG 等第三方镜像迁移到官方镜像。
3. 按官方升级序列处理 Authentik 和 Nextcloud 等不允许跨版本跳级的应用。
4. 按当前官方自托管拓扑重写已过时的 NetBird experimental cask。

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
