# 应用服务升级远端回归报告

测试日期：2026-08-02 至 2026-08-03
测试服务器：`whl@ln.hlong.wang:2200`

## 1. 结论

通过。LLNG 与 Authentik 两种 IAM 场景均在专用 Docker daemon 中完成全新部署、完整功能探针、workspace 重启和重启后复测。应用场景 18 个长期运行容器、Authentik 场景 10 个长期运行容器均保持运行，健康检查无失败，重启计数均为 0。

Nextcloud 34.0.2 的 LDAP、SAML、WebDAV、Talk、Memories、Imaginary、notify_push 和 Collabora 集成功能均通过；其余 LAM、MeshCentral、LLNG、Authentik、NetBird、Eturnal、PostgreSQL 和 MariaDB 服务也通过对应功能检查。测试完成后已停止并清理专用 daemon 和 network namespace，宿主原有业务容器保持运行且重启计数为 0。

## 2. 隔离环境

- 代码目录：`/home/whl/anas-app-upgrade-test`
- workspace：`/home/whl/anas-app-upgrade-test/.anas-test/runtime/app-upgrade-rebuilt`
- Docker socket：`/run/anas-app-docker.sock`
- Docker data-root：`/data/anas-app-docker`
- network namespace：`anas-app`
- 测试地址：`10.251.0.2/24`
- 容器、镜像、网络前缀：`anas_app_`

专用 daemon 与宿主 Docker 使用不同的 socket、data-root 和网络命名空间。测试覆盖 LLNG 与 Authentik 两种 IAM 场景；Samba DC 的 host network 实际绑定专用命名空间中的 `anas-app-peer` 和 `10.251.0.2`。

## 3. 实测版本

| 服务 | 实测版本或镜像 |
| --- | --- |
| Authentik | 2026.5.6 |
| Nextcloud | 34.0.2 |
| Nextcloud Redis | 8.10.0-alpine |
| Collabora | 26.04.2.4.1 |
| LemonLDAP::NG | 2.23.2 |
| LDAP Account Manager | 9.6 |
| MeshCentral | 1.2.4 |
| NetBird | management/signal/relay 0.76.1，dashboard 2.90.9 |

## 4. 功能结果

| 范围 | 结果 |
| --- | --- |
| 容器稳定性 | LLNG 场景 18 个容器、Authentik 场景 10 个容器均运行；有健康检查的容器全部 healthy；全部重启计数为 0 |
| Nextcloud 核心 | `occ status` 为 34.0.2、已安装、非维护模式、无需数据库升级 |
| Nextcloud 应用 | richdocuments 11.1.0、spreed 24.0.3、previewgenerator 5.14.0、notify_push 1.3.5、memories 8.1.0、user_saml 8.2.0 全部启用且完整性检查通过 |
| Nextcloud LDAP | `ldap:test-config` 建连成功，管理员可查询且同时属于 `admin` 与 AD `Admins` 组 |
| Nextcloud 文件 | WebDAV 上传、下载、内容比对和 PROPFIND 均通过 |
| Nextcloud 推送 | Redis、数据库挂载信息、Nextcloud 回连、受信代理和应用/服务版本五项自检全部通过 |
| Nextcloud Talk | 信令、TURN、STUN 配置可读，`/talk/api/v1/welcome` 返回 200；Eturnal 健康 |
| Nextcloud Memories | 地理库导入标记保留，`oc_memories_planet` 实测 497024 行 |
| Nextcloud Office | Collabora discovery、capabilities、mimetype 和 `richdocuments:activate-config` 全部通过，实测 Collabora 26.04.2.4 |
| LAM / MeshCentral | LAM 页面、JSON profile 和 LDAPS bind 通过；MeshCentral 页面、LDAP/MySQL 依赖和运行时配置通过 |
| LLNG | OIDC discovery、SAML metadata、Manager 鉴权跳转、带防重放 token 的 AD 管理员登录及动态客户端配置通过 |
| Authentik | 2026.5.6 server/worker 健康，init 正常退出，NetBird Application 蓝图落库，OIDC discovery 通过 |
| NetBird | Dashboard 返回 200；API 和 WebSocket/relay 协议入口响应符合预期；SQLite store、32 字节数据密钥、relay、TURN 和 OIDC 配置通过 |

## 5. 重启和持久化

- LLNG 应用场景执行 `anas restart` 后，18 个容器重新创建并再次通过完整探针。
- Nextcloud 的数据库、LDAP 配置、六个扩展应用、Memories 497024 行地理数据、WebDAV 文件、notify_push 受信代理和 Office 配置均保留。
- Authentik 场景执行 `anas restart` 后，server/worker、NetBird Application 蓝图、OIDC discovery 和 NetBird SQLite/密钥配置再次通过。
- 两次重启后的容器重启计数仍为 0，没有观察到 crash-loop 或健康检查退化。

## 6. 实测发现并修正的问题

1. Collabora 官方镜像是无 shell 的 Nix 镜像，不能沿用自定义 shell entrypoint；改为官方 `--use-env-vars` 和原生 `coolwsd --probe`。
2. Nextcloud 34 的 LDAP CLI 需要先创建空配置，再逐项写入；现已验证受信 LDAPS 和管理员同步。
3. Nextcloud Talk 需要无端口的 `NC_DOMAIN`，并要求 `turn.nas.test` 存在于 Samba DNS；Eturnal 已发布 TURN 域名别名。
4. Memories 98 MB 地理库在服务器访问 GitHub 时速度很低且会产生截断 ZIP；现使用持久缓存、断点续传、SHA-256 校验和本地缓存导入。
5. LLNG 官方镜像要求原生 YAML 动态客户端配置，配置目录还需要 `www-data` 写权限；入口已完成转换和权限初始化。
6. NetBird 0.76.1 不再支持 `jsonfile` store，现改为 SQLite；数据加密密钥要求 Base64 解码后严格为 32 字节，hook 已按该格式生成和校验。
7. AIO notify_push 官方启动脚本固定等待 Nextcloud 9001 端口，而本项目使用官方 Apache 镜像的 80 端口；现保留官方二进制，改用内部 Apache URL，并在 Nextcloud 初始化时动态登记 push 容器 IP 为受信代理。
8. Nextcloud 容器通过专用 namespace 的宿主地址回连 Collabora 会发生 hairpin 超时；入口现把 Collabora 公网域名映射到同一 Traefik 网络内地址。
9. NetBird signal 同时发布 gRPC 和 WebSocket 服务时，Traefik 需要在基础 router 上显式指定 service；否则会报告 multiple Services 歧义。
10. Authentik init 的 shell command 在 Compose 中必须使用单元素数组，否则会被拆成 `/bin/sh -c mkdir -p /data` 并导致 `mkdir: missing operand`。
11. 测试服务器直连 GHCR 很慢。专用 daemon 使用测试专用镜像代理；Authentik 镜像经代理拉取后回标为官方地址，manifest-list digest 为 `sha256:ed120caf710ccf82ef0026f0bc74e51615bc95ebff228a7a2d6fc60c441c3868`，linux/amd64 digest 与官方一致，为 `sha256:5a77d345a63452cda3762c573070e50a9e4be7f500fb1d82de18d95f705a898c`。

## 7. 验证边界

- 本轮按项目未发布前提使用全新 workspace，不验证旧 Nextcloud 30、Authentik 2024 或 NetBird JSON 数据的原地迁移。
- Talk 验证容器健康、信令配置、TURN/STUN 配置和 HTTP 入口，不包含两个公网客户端之间的真实媒体通话。
- NetBird 验证 Dashboard、management、signal、relay 和 LLNG OIDC 配置，不包含异地 peer 的真实隧道吞吐测试。

## 8. 远端报告

应用/LLNG 场景：

- 最终部署：`test-env/reports/2026-08-02-app-upgrade/apply-collabora-url-route.log`
- 首次完整回归：`test-env/reports/2026-08-02-app-upgrade/functional-rebuilt.log`
- workspace 重启：`test-env/reports/2026-08-02-app-upgrade/restart-rebuilt.log`
- 重启后完整回归：`test-env/reports/2026-08-02-app-upgrade/functional-after-restart.log`

Authentik 场景：

- 最终部署：`test-env/reports/2026-08-02-authentik-upgrade/apply-init-command.log`
- 首次完整回归：`test-env/reports/2026-08-02-authentik-upgrade/functional.log`
- workspace 重启：`test-env/reports/2026-08-02-authentik-upgrade/restart.log`
- 重启后完整回归：`test-env/reports/2026-08-02-authentik-upgrade/functional-after-restart.log`

清理后 `anas-test-docker-netns.service` 为 inactive，`anas-app` namespace 和专用 socket 已移除。宿主 `anas_test_postgres`、`ddns-go`、`traefik`、`v2raya`、`vpn-service`、`deng-ss` 均保持 running，重启计数为 0。
