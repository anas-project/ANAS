# 测试服务器部署与验证记录（2026-06-30）

## 结论

Go 重构版已部署到测试服务器并保持 PostgreSQL 测试实例运行。最终代码通过：

- 全部 Go 单元测试与 module manifest 静态校验；
- 7 组配置矩阵的 plan/render；
- 支持升级与拒绝降级的 lock fixture 校验；
- 所有渲染模块的 `docker compose config` 校验；
- PostgreSQL 启动、就绪、SQL 写入、`anas stop`、再次启动及数据持久化验证；
- 既有 5 个容器名称和 ID 不变，未占用新的宿主机端口。

不能据此宣称全栈运行功能全部通过。服务器部署前根分区已使用 96%，最终清理测试缓存后仍使用 97%，仅余约 3.4 GB；同时 80、9000 等端口已被既有服务占用。因此未执行 `test-build.sh`、全栈 `test-smoke.sh` 和运行时 `test-upgrade.sh`。执行这些测试前需要扩容，或在确认不会影响既有环境后清理 Docker 的未使用镜像、卷和构建缓存。

## 环境与隔离方案

| 项目 | 值 |
| --- | --- |
| SSH | `whl@ln.hlong.wang:2200` |
| 系统 | Ubuntu 22.04.1 LTS, Linux 5.15, x86-64 |
| Docker | 24.0.7 |
| Docker Compose | v2.21.0 |
| 部署目录 | `/home/whl/anas-refactor-test` |
| Go 工具链 | 部署目录内隔离安装 Go 1.24.2 |
| 测试容器 | `anas_test_postgres` |
| 测试网络 | `anas_test_postgres`、`anas_test_traefik` |
| 持久化目录 | `/home/whl/anas-refactor-test/.anas-test/server-data/postgres` |
| 远端报告 | `/home/whl/anas-refactor-test/test-env/reports` |

测试配置为 `test-env/server-runtime.yml`。它使用 `anas_test_` 容器、镜像和网络前缀，数据目录使用绝对路径，避免 Compose 相对路径随临时 release 移动。PostgreSQL 没有发布宿主机端口。

## 部署过程

### 1. 服务器盘点

部署前执行只读检查：

```sh
ssh -p 2200 whl@ln.hlong.wang
docker --version
docker compose version
docker system df
docker ps
df -h "$HOME"
ss -lntup
```

发现根分区 98 GB 中已使用约 89 GB，Docker 有约 25.45 GB 未使用镜像、2.381 GB 未使用卷和 14.79 GB 构建缓存。这些资源可能属于服务器上的其他项目，本次没有执行全局 prune。

### 2. 安装隔离 Go 工具链

Go 1.24.2 安装在部署目录的 `.tools/go`，没有修改系统包。压缩包 SHA-256 使用 Go 官方下载 JSON 的 `sha256` 字段校验后解压。

服务器默认 `proxy.golang.org` 解析到不可达的 IPv6 地址，依赖下载超时。后续命令使用：

```sh
export PATH="$HOME/anas-refactor-test/.tools/go/bin:$PATH"
export GOTOOLCHAIN=local
export GOPROXY="$TEST_GOPROXY"
```

### 3. 同步与构建

从本地 `refactor/` 同步项目，排除 `.git`、运行数据、Go 缓存和旧报告，并保留远端 `.tools/`：

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

远端构建：

```sh
cd /home/whl/anas-refactor-test
go build -o bin/anas ./cmd/anas
file bin/anas
```

产物为静态链接的 Linux x86-64 ELF 可执行文件。

### 4. 全矩阵非运行验证

```sh
./test-env/scripts/test-static.sh
./test-env/scripts/test-render.sh
./test-env/scripts/test-upgrade-render.sh
./test-env/scripts/test-compose-config.sh
```

最终代码四项全部返回 0。对应报告为：

- `server-final-static.log`
- `server-final-render.log`
- `server-final-upgrade-render.log`
- `server-final-compose-config.log`

### 5. PostgreSQL 运行时和持久化验证

Docker Hub 在该服务器连接超时。PostgreSQL 官方镜像通过可达的 AWS Public ECR Docker Library 缓存拉取，并保留项目期望的原始标签：

```sh
docker pull public.ecr.aws/docker/library/postgres:15.3-alpine
docker tag public.ecr.aws/docker/library/postgres:15.3-alpine postgres:15.3-alpine
```

镜像 digest 为 `sha256:4ca65a9209f164bdb30f715ac2ed9182cc0737d0f0a549031b3c1b0b7e652f3a`。

PostgreSQL Compose 文件声明了外部 Traefik 网络，即使 Adminer 被禁用，Compose 仍会校验该网络。测试环境创建隔离占位网络：

```sh
docker network create anas_test_traefik
```

运行验证：

```sh
./bin/anas start -c test-env/server-runtime.yml -b .anas-test/server-runtime
docker exec anas_test_postgres pg_isready -U postgres

docker exec anas_test_postgres psql -U postgres -d postgres \
  -c 'CREATE TABLE IF NOT EXISTS anas_deploy_probe (id integer PRIMARY KEY, value text NOT NULL);' \
  -c "INSERT INTO anas_deploy_probe VALUES (1, 'server-runtime-ok') ON CONFLICT (id) DO UPDATE SET value = EXCLUDED.value;"

./bin/anas stop -b .anas-test/server-runtime
./bin/anas start -c test-env/server-runtime.yml -b .anas-test/server-runtime

docker exec anas_test_postgres psql -U postgres -d postgres -Atc \
  'SELECT value FROM anas_deploy_probe WHERE id=1;'
```

最终查询返回 `server-runtime-ok`。容器状态为 `running`，重启计数为 0；第一次 `stop` 确认容器和模块网络均被实际删除，第二次启动后数据仍存在。

## 测试中发现并修复的问题

1. PostgreSQL Adminer 的 Compose service key 使用连字符，但 hook 和 manifest 使用下划线，导致 `adminer_enabled: false` 时仍尝试启动 Adminer。已统一为 `anas_postgres_adminer`。
2. `anas stop` 在停止现有 release 前重新执行 `renderAll`，会遍历运行数据并可能因容器数据权限失败。已使 `stop` 在渲染前直接处理现有 release。
3. `stopRelease` 原先忽略所有 `docker compose down` 错误，可能输出 `done` 但容器仍运行。已改为聚合并返回模块级错误。
4. 测试配置原先使用相对 `data_path`，实际绑定到临时 release 目录，不适合验证跨发布持久化。服务器配置改用绝对路径。

修复后本地 `go test ./...` 通过，远端四组最终矩阵验证及 PostgreSQL 生命周期验证均通过。

## 当前状态与操作

测试实例当前保持运行：

```sh
cd /home/whl/anas-refactor-test
export PATH="$PWD/.tools/go/bin:$PATH"

# 查看
docker ps --filter name=anas_test_postgres
docker logs --tail 100 anas_test_postgres

# 停止
./bin/anas stop -b .anas-test/server-runtime

# 再次启动
GOPROXY="$TEST_GOPROXY" ./bin/anas start \
  -c test-env/server-runtime.yml \
  -b .anas-test/server-runtime
```

`test-env/server-runtime.yml` 使用测试口令和 PostgreSQL trust 模式，仅适合当前不发布端口的隔离验证。扩大到可访问环境或生产部署前必须更换凭据和认证策略。

## 尚未完成的全栈验证

以下项目因服务器磁盘和端口条件未执行：

- 全部 Dockerfile 的镜像构建；
- full 配置的所有容器同时启动和健康检查；
- Samba DC、Nextcloud、LLNG/Keycloak、NetBird、TURN 等服务级业务探针；
- `previous-patch` 的运行时升级和持久化迁移探针。

建议准备至少 15–20 GB 额外可用空间和不与现有服务冲突的测试主机/虚拟机后执行：

```sh
./test-env/scripts/test-build.sh
./test-env/scripts/test-smoke.sh
./test-env/scripts/test-upgrade.sh previous-patch
```
