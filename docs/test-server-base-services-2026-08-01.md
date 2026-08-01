# 基础服务升级远端回归报告

测试日期：2026-08-01
测试服务器：`whl@ln.hlong.wang:2200`

## 1. 结论

本轮基础服务在全新 workspace 中完成构建、部署、协议探针和整栈重启测试。最终所有运行容器均为 healthy、重启计数为 0，协议级报告为 `FAILURES=0`。

测试结束后已删除全部隔离容器和自定义网络，停止专用 Docker daemon 并删除网络命名空间。宿主原有 6 个业务容器的 ID 和运行状态与测试前完全一致。

## 2. 隔离环境

- 代码目录：`/home/whl/anas-base-upgrade-test`
- workspace：`/home/whl/anas-base-upgrade-test/.anas-test/runtime/base-upgrade`
- Docker socket：`/run/anas-base-docker.sock`
- Docker data-root：`/data/anas-base-docker`
- network namespace：`anas-base`
- 测试地址：`10.253.0.2/24`
- 容器、镜像、网络前缀：`anas_base_`

专用 daemon 与宿主 Docker 使用不同的 socket、data-root、bridge 地址池和网络命名空间。Samba DC 的 `network_mode: host` 实际进入 daemon 所在的 `anas-base` 命名空间，因此测试配置绑定 `anas-b-peer` 和 `10.253.0.2`。

## 3. 实测版本

| 服务 | 实测版本或镜像 |
| --- | --- |
| Traefik | 3.7.10 |
| Lego | 5.3.1 |
| PostgreSQL | 18.4 |
| MariaDB | 12.3.2 |
| Adminer | 5.5.0，PHP 8.4.24 |
| DDNS Updater | `ghcr.io/qdm12/ddns-updater:2.10.0` |
| Eturnal | `ghcr.io/processone/eturnal:1.12.2` |
| FreeRADIUS | 3.2.10 |
| Samba DC/FS | 4.23.6，Ubuntu 26.04 包 |

## 4. 功能结果

| 范围 | 结果 | 验证内容 |
| --- | --- | --- |
| 编排 | 通过 | 远端 `go test ./...`、构建、render、逐 cask `docker compose config`、`anas apply --build` |
| 容器状态 | 通过 | 8 个带健康检查的容器全部 healthy；Lego 运行正常；全部重启计数为 0 |
| Traefik | 通过 | HTTPS Dashboard 未认证返回 401，BasicAuth 返回 200；自定义证书生效 |
| Lego | 通过 | 内部 CA 签发 `nas.test` 和 `*.nas.test`；私钥权限 0600；证书有效期检查通过 |
| PostgreSQL | 通过 | 建表、写入和读取成功；版本 18.4；数据目录为 `/var/lib/postgresql/18/docker` |
| MariaDB | 通过 | 建库、建表、写入和读取成功；`skip_name_resolve=1`、`utf8mb4` 配置生效 |
| Adminer | 通过 | 独立启动 `adminer:5.5.0`，Web 页面返回 200，随后删除探针容器 |
| DDNS | 通过（受限） | 空 manual-provider 配置可稳定运行；健康检查和 Traefik Web UI 返回 200 |
| Samba DC | 通过 | 全新 AD provision、Samba/BIND 进程、域功能级别、用户、A/SRV DNS、受信 LDAPS |
| Samba FS | 通过 | 域加入、`wbinfo -t`、共享认证、SMB 上传/下载和内容比对 |
| Eturnal | 通过 | `eturnalctl status`、REST credentials 命令和真实 UDP STUN Binding Response |
| FreeRADIUS | 通过（受限） | `freeradius -XC`、UDP Access-Request/Access-Reject 往返；未生成生产用户策略 |

## 5. 重启和持久化

整栈执行 `anas restart` 后再次等待健康检查：

- 所有服务恢复 healthy，重启计数仍为 0。
- PostgreSQL 标记 `postgres-18.4-ok` 保留。
- MariaDB 标记 `mariadb-12.3.2-ok` 保留。
- Samba DC 域状态和 Samba FS trust 保留。
- SMB 共享中的探针文件可重新下载并通过内容比对。

## 6. 实测发现并修正的问题

1. LinuxServer `resolute` 基础镜像的 GHCR 大层在服务器直连和现有代理上均极慢；Samba 改为 Docker Official `ubuntu:resolute`。
2. Ubuntu 极简镜像切到 HTTPS 镜像源前需要先安装 `ca-certificates`。
3. Ubuntu 26.04 将 AD 组件拆分到 `samba-ad-provision`、`samba-dsdb-modules` 和 `samba-ad-dc`，Dockerfile 已显式安装。
4. 官方 Ubuntu 不含 LinuxServer s6；改由 `tini` 加 `runit` 执行现有 cont-init 和 services 脚本。
5. MariaDB 官方健康脚本会读取全局 `MYSQL_HOST` 并误走 TCP；健康检查调用前已清除 `MYSQL_HOST`、`MARIADB_HOST`。
6. Samba 4.23 在 `winbind use default domain = yes` 时需要无域前缀组名；hook 现按该开关生成组标识，并覆盖默认域和显式域两种测试。

## 7. 验证边界

- DDNS 使用 `manual` provider 和空 settings，仅验证容器、配置解析、健康和 Web UI；没有真实 DNSPod 凭据，因此未更新公网记录。
- FreeRADIUS cask 不生成生产 clients/users；验证了配置和 UDP 请求响应，真实 Access-Accept 需要部署方策略。
- Eturnal 验证了控制面和 UDP STUN；未使用外部 NAT 客户端执行完整 TURN relay 媒体会话。
- 本轮按“未发布、无需旧数据兼容”的前提测试全新数据目录，不代表 PostgreSQL 15、MariaDB LinuxServer 目录或旧 Samba 数据可直接升级。

## 8. 远端报告

- 最终协议报告：`/home/whl/anas-base-upgrade-test/test-env/reports/2026-08-01-base-upgrade/final-functional-report.log`
- 重启持久化：`/home/whl/anas-base-upgrade-test/test-env/reports/2026-08-01-base-upgrade/restart-persistence.log`
- Adminer 探针：`/home/whl/anas-base-upgrade-test/test-env/reports/2026-08-01-base-upgrade/adminer-probe.log`
- 最终部署日志：`/home/whl/anas-base-upgrade-test/test-env/reports/2026-08-01-base-upgrade/apply-samba-groups.log`
- 清理记录：`/home/whl/anas-base-upgrade-test/test-env/reports/2026-08-01-base-upgrade/cleanup.log`
