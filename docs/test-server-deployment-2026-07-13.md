# 测试服务器部署与验证记录（2026-07-13）

## 当前结论

本轮目标是把 `refactor/` 部署到 `whl@ln.hlong.wang:2200`，执行完整运行时验证并修复
发现的问题。

截至当前：本地非运行时回归全部通过，并修复了会让 smoke 测试假性通过的容器状态检查；
但指定服务器的 2200 端口只接受 TCP 连接，不返回 SSH banner，约 9–11 秒后由远端关闭。
因此尚未在指定服务器执行代码同步、镜像构建、容器启动或业务探针，不能声称部署完成或
功能完整可用。

项目历史使用过的 `finance.hlong.wang:22` 备用测试环境仍可登录，部署目录、隔离 Docker
daemon 和历史镜像均存在。由于这不是用户本轮指定的主机，本轮只做了只读盘点，没有向其
同步代码或启动容器。

## SSH 入口排查

本机代理将 `ln.hlong.wang` 返回为 `198.18.1.64`，属于 Fake-IP 网段。通过加密 DNS
绕过本机解析后得到真实 A 记录 `218.8.172.22`，并使用本机 `known_hosts` 中已有的
`[ln.hlong.wang]:2200` Ed25519 主机密钥进行严格校验连接。

两条独立路径的结果一致：

1. 本机直连 `218.8.172.22:2200`：TCP 成功，SSH 在密钥交换前被远端关闭；
2. 从 `finance.hlong.wang` 连接 `ln.hlong.wang:2200`：TCP 成功，但同样收不到 SSH
   banner。

这说明阻断点在指定服务器的 SSH 服务或 2200 端口转发链路，而不是本机 DNS、SSH 密钥
或单一路径网络。入口恢复后应先执行：

```sh
ssh -o StrictHostKeyChecking=yes -o HostKeyAlias=ln.hlong.wang \
  -p 2200 whl@218.8.172.22
```

## 已完成的本地验证

本地使用 Go 1.24.2、隔离的项目内 Go build cache 和 Docker Compose v2.33.1 执行：

```sh
go test ./...
./test-env/scripts/test-static.sh
./test-env/scripts/test-render.sh
./test-env/scripts/test-upgrade-render.sh
./test-env/scripts/test-compose-config.sh
sh -n test-env/scripts/test-smoke.sh
```

结果：

- 所有 Go package 测试与 cask manifest 静态校验通过；
- `full`、`matrix-apps`、`matrix-auth`、`matrix-db`、`matrix-network`、
  `matrix-storage`、`min` 共 7 组 plan/render 通过；
- `mixed-old`、`previous-patch` 升级渲染通过，`future-downgrade` 按预期拒绝；
- 17 个渲染模块的 `docker compose config` 全部通过；
- `test-smoke.sh` 通过 POSIX shell 语法检查。

Compose 仅有两类非阻断警告：旧 `version` 字段已过时；未配置 DDNS 时
`DDNS_CONFIG` 回退为空字符串。

## 本轮修复：smoke 假绿

### 问题

启动器用固定项目名 `anas_<module>` 启动每个 Compose 项目，但原
`test-env/scripts/test-smoke.sh` 查询状态时没有传 `--project-name`。因此查询可能返回空
结果，后续只搜索 `exited|dead|unhealthy|restarting` 又不会匹配空输出，最终把“没有检查
到任何容器”误判为成功。

此外，时间点 `ps` 可能恰好在崩溃容器的短暂 running 窗口采样，无法识别持续重启。

### 修复

`test-env/scripts/test-smoke.sh` 现在：

- 使用与启动器一致的 `--project-name anas_<module>`；
- 用 `ps --all` 查询运行和已退出的容器；
- 任一模块查不到容器即失败；
- 对每个真实容器读取 `.State.Status`、`.State.Health.Status` 和 `.RestartCount`；
- 非 running、health 为 starting/unhealthy、或重启次数非零均判为失败；
- 默认稳定等待时间由 30 秒提高到 180 秒，避免慢启动服务尚未完成初始化就采样。

这会让 smoke 真实暴露 crash loop、退出、健康检查未完成和项目名不匹配，不再以空结果
通过。

## 待指定服务器入口恢复后执行

1. 只读盘点系统、磁盘、宿主业务容器和隔离 Docker daemon；
2. 保留 `.tools/`、`.anas-test/`、`.gocache/` 和历史报告，同步当前 `refactor/`；
3. 在远端重新执行构建、static、render、upgrade-render、compose-config；
4. 构建或复用隔离 daemon 中的项目镜像；
5. 用修复后的 smoke 启动完整 server-buildable 配置；
6. 逐容器检查运行状态、健康状态、重启次数和日志；
7. 执行 PostgreSQL/MariaDB、Keycloak OIDC、Nextcloud HTTP/occ、NetBird OIDC、Samba、
   TURN、RADIUS 等服务级探针；
8. 执行运行时升级与数据持久化探针；
9. 修复发现的问题后重复上述验证，最后用 `anas stop` 确认干净拆除。

在以上远端步骤完成前，本记录保持“未完成”结论。
