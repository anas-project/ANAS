# 测试服务器部署与验证记录（2026-07-04）

## 结论

当前 `refactor/` 已同步到 `whl@finance.hlong.wang` 的
`/home/whl/anas-refactor-test`，测试均在远程服务器执行。本地仅读取代码、同步文件和
编写本记录。

已通过：

- Go 1.24.2 Linux/amd64 构建；
- 全部 Go 单元测试和 cask manifest 静态校验；
- 7 组配置矩阵的 plan/render；
- 支持升级与拒绝降级的 lock fixture 校验；
- 全部渲染模块的 `docker compose config` 校验；
- 独立 Docker daemon 的镜像拉取、容器执行和容器 DNS 验证；
- GHCR 代理基础镜像预拉取；
- Lego、Bind、Samba DC、Samba FS、Traefik、Keycloak、MariaDB、Eturnal、
  Nextcloud、Nextcloud Talk、Nextcloud Imaginary、MeshCentral、NetBird、LAM、
  FreeRADIUS 等全部 server-buildable 镜像构建。

尚不能判定全栈功能完整可用。smoke 已实际启动 Lego、Bind、Samba DC/FS、Traefik、
MariaDB、PostgreSQL 和 Keycloak，并依次修复 macvlan parent、数据库启动顺序、Keycloak
数据库网络变量等问题。当前最新失败是 Keycloak 沿用了 LLNG 的门户图标复制逻辑；对应
Keycloak 运行配置已在本地重构，但因 Codex 外部操作额度用尽，尚未同步到服务器复测。
服务级探针和运行时升级测试尚未执行。

## 服务器与隔离环境

| 项目 | 值 |
| --- | --- |
| SSH | `whl@finance.hlong.wang:22` |
| 主机名 | `fengoffice` |
| 系统 | Ubuntu 24.04.1，Linux 6.8，x86-64 |
| Docker | 27.3.0 |
| Docker Compose | v2.29.6 |
| Go | 1.24.2，部署目录内隔离工具链 |
| 部署目录 | `/home/whl/anas-refactor-test` |
| 独立 Docker socket | `/run/anas-docker-test.sock` |
| 独立 Docker data-root | `/data/anas-docker-test` |
| 独立 network namespace | `anas-test` |
| 磁盘 | 根分区 218 GB，测试前约 175 GB 可用 |

服务器主 Docker 已运行 EVA、Dify、Nginx、Traefik、FRP 等业务容器。测试未停止或修改
这些容器。测试 daemon 使用独立 socket、data-root、bridge 地址池和 network namespace，
避免占用宿主已有端口。

## 服务器配置

### sudo 免密

创建 `/etc/sudoers.d/90-whl-nopasswd`：

```text
whl ALL=(ALL) NOPASSWD: ALL
```

权限设为 `0440`，并通过 `visudo -cf` 校验。`sudo -n true` 验证通过。

### Docker 镜像代理

专用 daemon 使用 `test-env/server-docker-daemon.json` 中的测试专用镜像代理。代理地址不进入
生产配置或公开部署说明。两个测试端点的 `/v2/` 均返回 HTTP 200。Docker 的
`registry-mirrors` 只原生代理 Docker Hub，所以 GHCR 镜像通过测试代理拉取后回标为
`ghcr.io/<path>`。

## 独立 Docker daemon 修复

首次复测 Alpine 时，镜像层存在但容器报告 `/bin/sh` 不存在。定位到两个隔离问题：

1. 测试 daemon 与主 Docker 共用系统 containerd 的默认 `moby` namespace；已增加独立的
   `anas-test` 和 `anas-test-plugins` namespace。
2. `systemd-run` 的 `BindReadOnlyPaths` 创建私有 mount namespace，containerd-shim 看不到
   dockerd 创建的 overlay 挂载；已移除该属性。

移除绑定后，dockerd 在 network namespace 内无法访问宿主 `127.0.0.53` DNS stub。脚本已
增加仅作用于测试 namespace 的 DNS DNAT/SNAT 规则，将 stub 请求转发到实际上游 DNS。

最终验证：

```text
isolated-runtime-ok
```

容器内解析测试镜像代理主机也成功。

Samba 运行时要求 daemon 所在 namespace 内存在 `enp3s0` 作为 macvlan parent。测试脚本会
创建同名 dummy 接口，仅供隔离 smoke 使用，不移动或修改宿主真实网卡。macvlan network
创建验证通过。

## 部署与测试命令

代码同步：

```sh
rsync -az --delete \
  -e 'ssh -o StrictHostKeyChecking=no -o UserKnownHostsFile=/dev/null' \
  --exclude '.tools/' \
  --exclude '.anas-test/' \
  --exclude '.gocache/' \
  --exclude 'test-env/reports/*' \
  --exclude '.git/' \
  refactor/ whl@finance.hlong.wang:/home/whl/anas-refactor-test/
```

Go 1.24.2 从测试环境配置的下载代理获取；`go.dev` 和 `dl.google.com` 在该服务器连接超时：

```sh
curl -fL -o /tmp/go1.24.2.linux-amd64.tar.gz \
  "$TEST_GO_DOWNLOAD_URL"
tar -C .tools -xzf /tmp/go1.24.2.linux-amd64.tar.gz
```

非运行时验证：

```sh
export PATH="$PWD/.tools/go/bin:$PATH"
export GOTOOLCHAIN=local
export GOPROXY="$TEST_GOPROXY"
export DOCKER_HOST=unix:///run/anas-docker-test.sock

go build -o bin/anas ./cmd/anas
./test-env/scripts/test-static.sh
./test-env/scripts/test-render.sh
./test-env/scripts/test-upgrade-render.sh
./test-env/scripts/test-compose-config.sh
```

以上全部返回 0。Compose 仅报告 `version` 字段过时，以及 `LLNG_NETWORK_DB` 等可选变量为空。

## 构建中发现并修复的问题

### Nextcloud Talk / Janus

直接从 GitHub clone Janus 连续两次 TLS 失败。`CHINESE_SPEEDUP=true` 时已改用：

```text
https://ghfast.top/https://github.com/meetecho/janus-gateway.git
```

修复后 Janus 编译完成并产出 `anas_nextcloud_talk:latest`。

### LAM / Debian Buster

`ldapaccountmanager/lam:7.8` 内的 Buster 仓库已从活动镜像移除。Dockerfile 已改为
`archive.debian.org` 并设置 `Acquire::Check-Valid-Until=false`；同时移除无必要的全系统
`apt-get upgrade`。远程构建已通过，产出 `anas_lam:latest`。

### FreeRADIUS scaffold

原 `freeradius/docker-compose.yml` 和 Dockerfile 实际是 Lego 的复制，构建目录不存在。
已替换为 `freeradius/freeradius-server:3.2.7`，提供 1812/1813 UDP 端口和 `freeradius -XC`
健康检查。全量构建已通过，产出 `anas_freeradius:latest`。

### smoke 启动链路

- 隔离 namespace 增加 dummy `enp3s0` 后，Samba macvlan 创建和启动通过；
- Keycloak 增加 PostgreSQL/MariaDB 的运行顺序约束；
- Keycloak Compose 的错误 `$LLNG_NETWORK_DB` 已改为 `$KEYCLOAK_NETWORK_DB`；
- smoke 在发布目录生成前失败时，现会从 `tmp/` Compose 文件反向清理已启动容器；
- Keycloak cask 原先大量复制 LLNG 行为。最新本地修改已去除 LLNG 文件、门户图标复制，
  并增加 Keycloak 原生数据库和管理员环境变量。该项尚待同步验证。

## 远端报告

目录：`/home/whl/anas-refactor-test/test-env/reports`。

关键文件：

- `server-2026-07-04-static.log`
- `server-2026-07-04-render.log`
- `server-2026-07-04-upgrade-render.log`
- `server-2026-07-04-compose-config.log`
- `server-2026-07-04-build.log`
- `server-2026-07-04-build-retry.log`
- `server-2026-07-04-build-proxy-fix.log`
- `server-2026-07-04-build-freeradius-fix.log`
- `server-2026-07-04-smoke-clean.log`
- `server-2026-07-04-smoke-keycloak-fix.log`

## 续跑步骤

外部操作额度恢复后执行：

```sh
rsync -az \
  refactor/casks/mods/keycloak/ \
  whl@finance.hlong.wang:/home/whl/anas-refactor-test/casks/mods/keycloak/

ssh whl@finance.hlong.wang
cd /home/whl/anas-refactor-test
export PATH="$PWD/.tools/go/bin:$PATH"
export GOTOOLCHAIN=local
export GOPROXY="$TEST_GOPROXY"
export DOCKER_HOST=unix:///run/anas-docker-test.sock
./test-env/scripts/test-static.sh
ANAS_SERVER_CONFIG="$PWD/test-env/server-buildable-runtime.yml" \
  ./test-env/scripts/test-server-build.sh

ANAS_TEST_CONFIG="$PWD/test-env/server-buildable-runtime.yml" \
ANAS_TEST_RUNTIME="$PWD/.anas-test/runtime/server-smoke-keycloak-native" \
ANAS_SMOKE_WAIT_SECONDS=60 \
  ./test-env/scripts/test-smoke.sh
```

构建通过后，再执行全栈 smoke、服务级探针和 `test-upgrade.sh previous-patch`。在这些步骤
通过前，不应将当前结果表述为“功能完整可用”。
