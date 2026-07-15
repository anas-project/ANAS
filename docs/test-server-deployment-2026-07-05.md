# 测试服务器部署与验证记录（2026-07-05）

> 说明：本文件由 2026-07-05 的两轮工作合并而成。前一轮（下称"上半场"）修复了两处
> cask hook bug 并一度将结论表述为"功能完整可用"，其依据是 smoke 与时间点 `docker ps`
> 快照。后一轮（下称"下半场"）对完整栈做了**逐容器的真实状态核查**（`RestartCount` /
> 容器日志 / 数据库探针 / 健康状态随时间变化），发现上半场的 smoke 属**假性通过**，栈中
> 仍有三个服务在**崩溃重启**。下半场定位并修复了这三处，复验通过，并把结论修正为下方
> "结论"。所有测试均在远程服务器执行，本地仅编辑代码、同步文件与编写本记录。

## 结论（修正后）

重构后的启动器能在服务器上**部署并运行**基础设施层、数据层与大部分应用服务。经本轮
修复，全部 20 个容器启动并稳定运行：Nextcloud 达到 `healthy` 并完成安装，Keycloak
干净启动（迁移 117 个 changeset、初始化 master realm、创建 admin 用户、监听 8080），
NetBird dashboard 停止崩溃、转为正确重试。非运行时套件（Go 单测、static、render、
compose-config、upgrade-render）全部通过，`anas stop` 干净拆除、无残留容器。

但**尚不能表述为"全栈端到端功能完整可用"**：SSO 深度集成仍有缺口（Keycloak realm/OIDC
客户端下发与 hostname/proxy 配置未完善，导致 NetBird OIDC 引导拿到 404；Nextcloud LDAP
后台任务、MeshCentral LDAP 模块受隔离环境限制未完成）。详见文末"未完成项"。

## 服务器与隔离环境

| 项目 | 值 |
| --- | --- |
| SSH | `whl@finance.hlong.wang:22` |
| 主机名 | `fengoffice` |
| 系统 | Ubuntu，Linux 6.8，x86-64 |
| Docker（隔离 daemon） | 27.3.0，data-root `/data/anas-docker-test` |
| 隔离 socket | `/run/anas-docker-test.sock` |
| 隔离 network namespace | `anas-test`（systemd 单元 `anas-test-docker-netns.service` active） |
| macvlan parent | netns 内 dummy `enp3s0` |
| Go | 1.24.2，部署目录内隔离工具链（`.tools/go`） |
| 部署目录 | `/home/whl/anas-refactor-test` |
| 磁盘 | 根分区 218 GB，测试前约 166 GB 可用 |

隔离 daemon 与宿主主 Docker（EVA、Dify、Nginx、Traefik、FRP 等业务容器）完全隔离：
独立 socket、data-root、containerd namespace、bridge 地址池与 network namespace。测试
未停止或修改宿主任何业务容器。

## 部署流程

### 1. 同步代码

```sh
rsync -az --delete \
  -e 'ssh -o StrictHostKeyChecking=no' \
  --exclude '.tools/' --exclude '.anas-test/' --exclude '.gocache/' \
  --exclude 'test-env/reports/*' --exclude '.git/' --exclude 'bin/anas' \
  refactor/ whl@finance.hlong.wang:/home/whl/anas-refactor-test/
```

### 2. 构建与非运行时验证

```sh
cd /home/whl/anas-refactor-test
export PATH="$PWD/.tools/go/bin:$PATH"
export GOTOOLCHAIN=local
export GOPROXY=https://goproxy.cn,direct
export GOCACHE="$PWD/.gocache"
export DOCKER_HOST=unix:///run/anas-docker-test.sock

go build -o bin/anas ./cmd/anas
go test ./...
./test-env/scripts/test-static.sh
./test-env/scripts/test-render.sh
./test-env/scripts/test-compose-config.sh
./test-env/scripts/test-upgrade-render.sh
```

以上全部返回 0。Compose 仅报告 `version` 字段过时及少量可选变量为空的告警。

### 3. 隔离 daemon 与镜像

隔离 daemon 常驻运行，`docker info` 显示 28 个镜像，全部 server-buildable 镜像此前已
构建就绪（`anas_bind`、`anas_samba_dc/fs`、`anas_traefik`、`anas_lego`、`anas_mariadb`、
`anas_keycloak`、`anas_nextcloud`(+talk/imaginary)、`anas_meshcentral`、`anas_netbird_*`、
`anas_lam`、`anas_eturnal`、`anas_freeradius`）。

### 4. 真实运行时验证

用 `server-buildable-runtime.yml`（samba_dc、samba_fs、postgres、mariadb、keycloak、
nextcloud、meshcentral、netbird、lam、bind、ddns、eturnal、freeradius，外加自动带入的
core/lego/traefik 依赖）执行：

```sh
./bin/anas start -c test-env/server-buildable-runtime.yml -b .anas-test/runtime/server-0705
# 用真实探针核查，而非 smoke 的 release-scoped ps：
docker ps -a
docker inspect <container> --format 'Restarts={{.RestartCount}} Health={{.State.Health.Status}} ...'
docker logs <container>
docker exec anas_test_postgres psql -U postgres -tAc "select datname from pg_database"
```

## 上半场：两处 cask hook 修复（已在代码中，经本轮确认生效）

### Hook 修复 A：NetBird management OIDC endpoint 为空

**文件**：`casks/mods/netbird/hook/main.go`。`NETBIRD_AUTH_OIDC_CONFIGURATION_ENDPOINT`
原硬连到 `LLNG_OIDC_CONFIGURATION_ENDPOINT`，而 server-buildable 配置用 Keycloak、不加载
LLNG，导致该变量为空、NetBird management 反复重启。修复为优先取
`KEYCLOAK_OIDC_CONFIGURATION_ENDPOINT`，回退 LLNG。本轮核查确认 `anas_test_netbird_management`
稳定运行，该修复有效。

### Hook 修复 B：Nextcloud `DB_HOST` 含端口

**文件**：`casks/mods/nextcloud/hook/main.go`。原将 `DB_HOST` 设为 `POSTGRES_HOST_PORT`
（`host:port`），occ 内部 psql 把整串当主机名解析失败。修复为分开设置
`DB_HOST=POSTGRES_HOST`、`DB_PORT=POSTGRES_PORT`（MariaDB 分支同理）。本轮读码确认修复
在位。**但该修复仅覆盖 occ 路径**，未覆盖初始化脚本的 DB 等待路径（见下半场缺陷 3）。

> 更正：上半场依据 smoke + 时间点 `docker ps` 快照得出"功能完整可用"。该 smoke 的 ps
> 检查存在盲点（见"未完成项"第 4 条），崩溃重启的容器在时间点快照里常恰好显示 "Up"。
> 下半场以更严格的核查发现结论不成立。

## 下半场：三处运行时崩溃缺陷（本轮定位并修复、复验）

### 缺陷 1：postgres cask 不为依赖服务建库（阻断 Keycloak + Nextcloud）

**现象**：全新启动时 Keycloak 反复 `FATAL: database "keycloak" does not exist` →
`Failed to start server`（`RestartCount=4`）；Nextcloud 卡在
`database "nextcloud" does not exist` → 60s 超时后退出重启。`docker exec ... psql` 确认
库中只有 `postgres/template0/template1`。网络层正常（可解析、共享网络、TCP 5432 可达、
超级用户认证成功），失败纯在"目标库不存在"——没有任何组件负责为依赖模块建库。

**修复**：
- `casks/mods/postgres/hook/main.go`：`render_env` 阶段扫描全局 env 中所有
  `<PREFIX>_DB_NAME`，凡其 `<PREFIX>_DB_HOST` 或 `<PREFIX>_NETWORK_DB` 指向本 postgres
  主机者收集为待建库；生成幂等脚本 `initdb/10-anas-create-databases.sh`（`SELECT 1 FROM
  pg_database` 不存在则 `CREATE DATABASE`）。MariaDB 背书模块因主机不匹配而不会误建。
- `casks/mods/postgres/docker-compose.yml`：挂载
  `./initdb:/docker-entrypoint-initdb.d:ro`。
- `internal/runner/hook.go`：hook 渲染文件权限 `0600` → `0644`。原权限下文件属主为宿主
  用户（uid 1000），postgres 容器以 postgres 用户运行，`source` 挂载脚本报
  `Permission denied`。hook 渲染的是需被容器内服务读取的配置文件（生成的密钥走独立
  secret store，不经此路径），`0644` 与 ERB 渲染路径一致。

**复验**：清空 postgres 数据重启，日志出现 `anas: creating database keycloak/nextcloud`，
`pg_database` 新增 `keycloak`、`nextcloud`。Keycloak：`started in 144.641s. Listening on:
http://0.0.0.0:8080`、`Added user 'admin' to realm 'master'`、`RestartCount=0`。

### 缺陷 2：NetBird dashboard 启动脚本 `set -e` 崩溃（exit 6）

**现象**：`anas_test_netbird_dashboard` 反复 `Restarting (6)`，日志只有重复的
`Set hosts` / `Set 172.31.1.2 netbird.nas.test`。

**定位**：`casks/mods/netbird/dashboard/start.sh` 顶部 `set -e`，而 `waiting_url` 里
`response=$(curl -s ... $url)` 在 OIDC 端点无法解析时 curl 返回 6（couldn't resolve
host），命令替换失败直接触发 `set -e` 退出，重试循环没机会执行。OIDC 端点主机
`auth.nas.test` 从未被写入容器 `/etc/hosts`（脚本只写了 `NETBIRD_DOMAIN`）。这与 hook
修复 A 无关：A 修好了 management 的空变量，dashboard 脚本自身另有此缺陷。

**修复**（`start.sh`）：`waiting_url` 内 curl 追加 `|| true` 使重试循环生效；新增
`url_host()` 解析 OIDC 主机并把它也指向 traefik IP 写入 `/etc/hosts`（单机架构下所有
HTTPS 都经 traefik）；curl 加 `-k` 容忍测试自签证书。需重建 `anas_netbird_dashboard`
镜像（`start.sh` 经 `COPY` 打入镜像）。

**复验**：dashboard 转为稳定 `Up`，日志由崩溃退出变为经 traefik 到达 keycloak 的
`Status code: 404 ... Retrying`（404 见未完成项 1）。

### 缺陷 3：Nextcloud 初始化脚本误拆 `DB_HOST`

**现象**：修复缺陷 1 后，Nextcloud 变为
`Waiting anas_test_postgres:anas_test_postgres online...`——端口位置竟是主机名。

**定位**：`casks/mods/nextcloud/nextcloud/root/etc/cont-init.d/10-tasks.sh` 用
`cut -d ":" -f 2` 从 `$DB_HOST` 拆端口，假设其为 `host:port`。但 hook 修复 B 已把
`DB_HOST` 设为纯主机；无冒号时 `cut -f 2` 又返回整串主机名，于是 `nc -zv host host`
永不成功。这正是 hook 修复 B 未覆盖到的第二条路径。

**修复**（`10-tasks.sh`）：改用 `cut -d ":" -s -f 2` 仅在存在分隔符时取端口，否则回退
`${DB_PORT:-5432}`，同时兼容旧的 `host:port`。需重建 `anas_nextcloud` 镜像（脚本经
`COPY root/` 打入镜像，`--build-arg CHINESE_SPEEDUP=true`）。

**复验**：Nextcloud 通过 DB 就绪探测、执行 `occ maintenance:install` 完成安装，
`Health=healthy`、`RestartCount=0`。

## 最终运行时状态

`anas start` 全 13 模块（+3 依赖）后，20 个容器全部启动并持续运行 10 分钟以上：

```text
anas_test_bind                Up (running)
anas_test_ddns                Up (healthy)
anas_test_eturnal             Up (healthy)
anas_test_freeradius          Up (healthy)
anas_test_keycloak            Up (running, Restarts=0, Listening :8080)
anas_test_lam                 Up (healthy)
anas_test_lego                Up (running)
anas_test_mariadb             Up (running)
anas_test_meshcentral         Up (周期性重启，见未完成项 3)
anas_test_netbird_dashboard   Up (running, 已停止崩溃)
anas_test_netbird_management  Up (running)
anas_test_netbird_signal      Up (running)
anas_test_nextcloud           Up (healthy, 已安装)
anas_test_nextcloud_imaginary Up (healthy)
anas_test_nextcloud_redis     Up (running)
anas_test_nextcloud_talk      Up (healthy)
anas_test_postgres            Up (running, 含 keycloak/nextcloud 库)
anas_test_samba_dc            Up (running)
anas_test_samba_fs            Up (running)
anas_test_traefik             Up (running)
```

`anas stop` 返回 0，容器清零，拆除干净。

## 未完成项（尚不能表述为"功能完整可用"）

1. **Keycloak 作为 OIDC Provider 的集成未完善**。Keycloak 服务本身完全正常（启动、DB
   迁移、master realm、admin 用户、监听 8080），但直连
   `/realms/master/.well-known/openid-configuration` 返回 500、经 traefik 返回 404，
   NetBird dashboard 因此持续拿到 404。根因是 Keycloak 的 hostname/proxy 配置与 realm/
   OIDC 客户端下发尚未补齐（README 也将 Keycloak 定位为 scaffold）。属集成完善度问题，
   本轮未修复。
2. **Nextcloud LDAP 后台任务**等待 `nas.test:636`（LDAPS）。Nextcloud 主服务已 healthy，
   该后台任务需 samba DC 经 `nas.test` DNS 可达 + macvlan 打通，属隔离环境限制。
3. **MeshCentral** 首启在运行时 `npm install ldapauth-fork`（LDAP 模块未预置到镜像 /
   npm 出网），导致周期性重启；容器保持 running 但 LDAP 认证模块未就绪，属隔离环境限制。
4. **smoke 脚本盲点**（建议修复）。`test-env/scripts/test-smoke.sh` 的健康检查读取
   `release/*/docker-compose.yml` 执行 `docker compose ps`，但容器实际由 `tmp/*` 项目名
   启动，两者 compose 项目名不同，`ps` 返回空表头、`grep exited|unhealthy` 无匹配，于是
   smoke **假性通过**而未真正核查容器健康——这正是上半场误判"全部 Up / 功能完整可用"的
   直接原因。本轮改用真实 `docker ps -a` / `inspect` / 日志 / 数据库探针绕过此盲点。建议
   让 smoke 用真实运行容器状态判定，并在 stop 之前采样。

## 远端报告

目录：`/home/whl/anas-refactor-test/test-env/reports`。本轮关键文件：

- `server-2026-07-05-gotest.log`
- `server-2026-07-05-test-static.log` / `-test-render.log` / `-test-compose-config.log` /
  `-test-upgrade-render.log`
- `server-2026-07-05-start.log`（首次真实启动，暴露三缺陷）
- `server-2026-07-05-start-fixed.log` / `-fixed2.log` / `-fixed3.log`（逐步修复后的启动）
- `server-2026-07-05-final-ps.log`（最终 20 容器快照）
- `server-2026-07-05-postgres-databases.log`
- `server-2026-07-05-stop.log`

## 续跑步骤

要迈向"全栈端到端可用"，建议按以下顺序补齐并复验（均在服务器）：

```sh
rsync -az --delete --exclude '.tools/' --exclude '.anas-test/' \
  --exclude '.gocache/' --exclude 'test-env/reports/*' --exclude '.git/' \
  --exclude 'bin/anas' refactor/ whl@finance.hlong.wang:/home/whl/anas-refactor-test/

ssh whl@finance.hlong.wang
cd /home/whl/anas-refactor-test
export PATH="$PWD/.tools/go/bin:$PATH" GOTOOLCHAIN=local \
  GOPROXY=https://goproxy.cn,direct GOCACHE="$PWD/.gocache" \
  DOCKER_HOST=unix:///run/anas-docker-test.sock
go build -o bin/anas ./cmd/anas && ./test-env/scripts/test-static.sh
```

待办：
1. 补齐 Keycloak cask：realm 与各服务 OIDC/SAML 客户端下发、正确的 hostname/proxy 配置，
   使 `/realms/<realm>/.well-known/openid-configuration` 经 traefik 返回 200；复验 NetBird
   dashboard OIDC 引导完成。
2. 修复 `test-smoke.sh` 使其对真实运行容器判定健康，纳入常规回归。
3. 打通 `nas.test`→samba DC 的容器内 DNS 与 macvlan，复验 Nextcloud LDAP 后台任务。
4. 将 `ldapauth-fork` 预置进 MeshCentral 镜像或提供离线安装，消除周期性重启。
5. 全部通过后再执行服务级探针与 `test-upgrade.sh previous-patch` 的运行时升级测试。

在以上步骤通过前，不应将结果表述为"功能完整可用"。
