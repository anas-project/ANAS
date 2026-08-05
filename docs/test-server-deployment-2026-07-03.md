# 测试服务器部署与验证记录（2026-07-03）

## 结论

当前 `refactor/` 已同步并部署到 `whl@ln.hlong.wang:2200` 的
`/home/whl/anas-refactor-test`。

本次验证通过：

- Go 1.24.2 Linux/amd64 构建；
- 全部 Go 单元测试和 cask manifest 静态校验；
- 7 组配置矩阵的 plan/render；
- 支持升级与拒绝降级的 lock fixture 校验；
- 全部渲染模块的 `docker compose config` 校验；
- PostgreSQL 首次启动、就绪、SQL 写入、停止、再次启动和数据持久化验证。

不能判定全栈功能完整可用。全量镜像构建在拉取 GHCR 基础层时因网络超时中断；
隔离 Docker daemon 重启后出现镜像元数据存在但 overlay 根文件系统不可执行的问题；
包含 Samba/macvlan 的全栈配置也不能直接运行在当前 Docker 网络命名空间隔离方案中。
因此 Samba DC、Nextcloud、Keycloak、NetBird、TURN、RADIUS 等服务的运行时业务探针
和完整运行时升级测试本次未通过验证。

## 服务器与隔离环境

| 项目 | 值 |
| --- | --- |
| SSH | `whl@ln.hlong.wang:2200` |
| 系统 | Ubuntu 22.04，Linux 5.15，x86-64 |
| Docker | 24.0.7 |
| Docker Compose | v2.21.0 |
| Go | 1.24.2，部署目录内隔离工具链 |
| 部署目录 | `/home/whl/anas-refactor-test` |
| 隔离 Docker socket | `/run/anas-docker-test.sock` |
| 隔离 Docker data-root | `/data/anas-docker-test` |
| 测试数据根目录 | `/data/anas-refactor-test/data` |
| 远端报告 | `/home/whl/anas-refactor-test/test-env/reports` |

服务器根分区仅剩约 4.0 GB，但 `/data` 有约 3.7 TB 可用空间。隔离 daemon 使用
独立 network namespace、socket 和 data-root，不占用宿主已有的 80、9000 等端口。
主 Docker 只用于最终 PostgreSQL 专用测试容器验证，没有修改其他业务容器。

## 部署过程

### 1. 远端盘点

```sh
ssh -p 2200 whl@ln.hlong.wang
df -hT
docker --version
docker compose version
docker system df
docker ps
ss -lntup
sudo /home/whl/anas-refactor-test/test-env/scripts/server-isolated-docker.sh status
```

确认隔离 daemon 处于 active 状态，data-root 为 `/data/anas-docker-test`。

### 2. 同步当前代码

本地只执行代码读取和同步，没有执行构建或测试：

```sh
rsync -az --delete \
  -e 'ssh -p 2200' \
  --exclude '.tools/' \
  --exclude '.anas-test/' \
  --exclude '.gocache/' \
  --exclude 'test-env/reports/*' \
  --exclude '.git/' \
  refactor/ whl@ln.hlong.wang:/home/whl/anas-refactor-test/
```

保留远端 Go 工具链、运行数据和历史报告。

### 3. 构建和非运行验证

远端环境：

```sh
cd /home/whl/anas-refactor-test
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

以上命令全部返回 0。Compose 校验仅有 `DDNS_CONFIG`、`LLNG_NETWORK_DB` 未设置时
回退为空字符串的警告，没有语法或引用错误。

对应报告：

- `server-2026-07-03-static.log`
- `server-2026-07-03-render.log`
- `server-2026-07-03-upgrade-render.log`
- `server-2026-07-03-compose-config.log`

### 4. 全量镜像构建

```sh
export ANAS_SERVER_CONFIG="$PWD/test-env/server-buildable-runtime.yml"
./test-env/scripts/test-server-build.sh
```

第一次尝试因隔离 daemon 的 DNS 瞬时失败，无法解析配置的两个代理域名。重启隔离
daemon 后，`goacme/lego:v4.11.0` 拉取成功，构建继续并成功生成 `anas_lego`、
`anas_bind`、`anas_samba_dc` 等镜像。

后续 `ghcr.io/linuxserver/baseimage-ubuntu:jammy` 的 blob 下载指向
`pkg-containers.githubusercontent.com`，连接反复超时。重试约 20 分钟后，两个
32–38 MB 层仍只完成约 4–5 MB，构建进程被停止。该结果记为失败，不记为部分通过。

报告：`server-isolated-build.log`。

### 5. 全栈 smoke 尝试

使用当前代码、`server-buildable-runtime.yml` 和隔离 daemon 执行 smoke。渲染完成，
但没有创建任何容器。配置中的 Samba/macvlan 模块要求在宿主网络创建 macvlan；
隔离 daemon 位于单独 network namespace，当前 `ensureMacvlan` 路径与该隔离方式不兼容。

此外，隔离 daemon 重启后，缓存镜像出现层存储异常：`docker image inspect` 能看到
镜像及层元数据，但 Alpine 和 PostgreSQL 容器均找不到 `/bin/sh` 或入口脚本。
因此没有继续把该 daemon 的运行结果作为应用功能结论。

### 6. PostgreSQL 运行时与持久化验证

使用主 Docker 中项目专用的 `anas_test_postgres` 容器和网络执行，不发布宿主端口，
其他业务容器未停止或修改。

```sh
unset DOCKER_HOST
base="$PWD/.anas-test/runtime/server-main-postgres-20260703"

./bin/anas start -c test-env/server-runtime.yml -b "$base"
docker exec anas_test_postgres pg_isready -U postgres

docker exec anas_test_postgres psql -U postgres -d postgres \
  -c 'CREATE TABLE IF NOT EXISTS anas_deploy_probe (id integer PRIMARY KEY, value text NOT NULL);' \
  -c "INSERT INTO anas_deploy_probe VALUES (1, 'server-2026-07-03-ok') ON CONFLICT (id) DO UPDATE SET value=EXCLUDED.value;"

./bin/anas stop -b "$base"
./bin/anas start -c test-env/server-runtime.yml -b "$base"

docker exec anas_test_postgres psql -U postgres -d postgres -Atc \
  'SELECT value FROM anas_deploy_probe WHERE id=1;'
```

最终查询返回 `server-2026-07-03-ok`；容器状态为 `running`，重启计数为 0。说明当前
启动器的 render/start/stop、Compose 调用、数据库容器生命周期和绑定目录持久化链路可用。

对应报告：

- `server-2026-07-03-main-postgres-start1.log`
- `server-2026-07-03-main-postgres-sql.log`
- `server-2026-07-03-main-postgres-stop1.log`
- `server-2026-07-03-main-postgres-start2.log`

## 最终远端状态

- 当前代码位于 `/home/whl/anas-refactor-test`；
- `anas_test_postgres` 保持运行，不发布宿主端口；
- SQL 探针数据保留在 `.anas-test/server-data/postgres`；
- 其他宿主业务容器未改变；
- 隔离 Docker daemon 保持运行，但在清理并重建 data-root 前不应继续用于运行时结论。

停止 PostgreSQL 测试实例：

```sh
cd /home/whl/anas-refactor-test
./bin/anas stop -b .anas-test/runtime/server-main-postgres-20260703
```

## 后续完成标准

要宣称全栈完整可用，需要至少完成以下工作：

1. 修复或重建 `/data/anas-docker-test` 的 overlay 层存储，并验证基础 Alpine 容器可执行；
2. 为 GHCR 配置稳定、不会回源到不可达 blob CDN 的镜像代理；
3. 调整隔离方案，使 macvlan 创建发生在与测试 daemon 一致的 network namespace，
   或使用独立虚拟机而不是 daemon network namespace；
4. 重新执行 `test-server-build.sh`、全栈 smoke 和 `test-upgrade.sh previous-patch`；
5. 增加 Samba LDAP/DNS、Nextcloud HTTP/数据库、Keycloak OIDC、NetBird、TURN 和
   RADIUS 的服务级探针，不能只检查容器未退出。
