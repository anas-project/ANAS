# finance 测试服务器部署与验证记录（2026-07-13）

## 结论

本轮将 Go 重构版 `refactor/` 部署到 `whl@finance.hlong.wang`，在独立 Docker daemon、
独立 network namespace 和独立数据目录中完成冷启动、容器稳定性、AD/DNS、数据库、Web
入口及持久化重启验证。测试使用专用复杂凭据，本文和报告均不记录明文。

服务器上原有业务 Docker daemon、容器、网络和数据没有被停止、重建或修改。验证结束后，
测试容器、测试网络、macvlan shim 均由 `anas stop` 拆除；隔离 daemon 和测试数据保留，
便于后续复测。

“当前配置中已实现的运行时”可以正常构建和启动，但不能把尚未实现的产品脚手架等同于
端到端功能完整：Keycloak 尚未自动创建业务 realm/client/LDAP federation，FreeRADIUS
仍是实验性脚手架，手动 DDNS 没有外部提供商可调用。这些边界详见“未覆盖和剩余工作”。

## 环境与隔离

- 远端目录：`/home/whl/anas-refactor-test`
- 测试数据：`/data/anas-refactor-test/validation-2026-07-13-v14/data`
- 隔离 Docker socket：`/run/anas-docker-test.sock`
- 隔离 Docker data root：`/data/anas-docker-test`
- network namespace：`anas-test`
- 测试 veth：宿主端 `10.254.0.1/24`，namespace 端 `10.254.0.2/24`
- macvlan 地址池：`10.254.0.240/28`
- 测试域：`nas.test`；Traefik 测试入口端口：`9000`
- 远端版本：Ubuntu 24.04.1、Docker 27.3.0、Compose 2.29.6、Go 1.24.2

由于 Docker 的 `network_mode: host` 使用宿主初始 network namespace，即使 dockerd 本身
运行在另一个 namespace 中，Samba DC 和 BIND 仍位于宿主网络。测试拓扑因此让域控和
BIND 监听宿主侧 `10.254.0.1`，隔离容器与 macvlan 文件服务器通过该地址访问 DNS、KDC
和 LDAP；Traefik 的测试入口则发布在 namespace 侧 `10.254.0.2:9000`。

Traefik 只挂载 `/run/anas-docker-test.sock`，不会发现或路由宿主业务 daemon 中的容器。

## 配置与执行过程

测试配置为 `test-env/server-validation-2026-07-13.yml`，覆盖下列服务器可构建模块：

`samba_dc`、`samba_fs`、`postgres`、`mariadb`、`keycloak`、`nextcloud`、
`meshcentral`、`netbird`、`lam`、`bind`、`ddns`、`eturnal`、`freeradius`。

代码同步保留远端工具链、运行时、缓存和历史报告：

```sh
rsync -azc \
  --exclude .git --exclude .tools --exclude .anas-test \
  --exclude .gocache --exclude test-env/reports --exclude bin/anas \
  refactor/ whl@finance.hlong.wang:/home/whl/anas-refactor-test/
```

远端执行 Go 回归和 shell 语法检查；本地也执行相同 shell 语法检查：

```sh
go test ./...
bash -n casks/mods/samba_dc/samba_dc/root/usr/local/bin/anas_zone.sh
bash -n casks/mods/nextcloud/nextcloud/root/usr/local/bin/task.sh
./bin/anas build -c test-env/server-validation-2026-07-13.yml \
  -b .anas-test/runtime/server-validation-2026-07-13-v8
```

在本轮服务器部署前，7 组 render matrix、升级渲染 fixture、17 个 Compose 配置和所有
服务器可构建镜像也已通过。最终远端 `go test ./...` 包含新增的 Keycloak hostname-port
和 Traefik 网络选择回归测试并通过。Compose 只报告旧 `version` 字段和未配置 DDNS 时的
空 `DDNS_CONFIG` 警告。

完整 smoke 使用严格检查脚本：

```sh
DOCKER_HOST=unix:///run/anas-docker-test.sock \
ANAS_TEST_CONFIG="$PWD/test-env/server-validation-2026-07-13.yml" \
ANAS_TEST_RUNTIME="$PWD/.anas-test/runtime/server-validation-2026-07-13-v8" \
ANAS_SMOKE_WAIT_SECONDS=3600 \
./test-env/scripts/test-smoke.sh
```

脚本逐模块使用与启动器相同的 Compose project name，要求每个服务确实存在容器，并检查
`.State.Status`、health 和 `.RestartCount`；任一容器非 running、仍在 starting、
unhealthy 或发生重启即失败，最后始终执行 `anas stop`。等待过程改为每 10 秒轮询状态，
而不是固定等待后直接判断；首次导入 Memories 地理数据库约需 40 分钟，所以冷启动验证
允许 1 小时，完成过一次后持久化重启通常不需要这个时长。

全栈稳定后使用 `test-env/scripts/server-functional-probes.sh` 执行服务探针。该脚本不读取或
输出明文凭据，复用容器内环境完成数据库认证；它统一检查容器状态、AD/LDAP/SMB、DNS、
Nextcloud 配置与应用完整性、Memories 数据、Keycloak discovery、Traefik 路由、数据库
读写和 LDAPS 证书。第一次执行写入专用探针记录，重启后设置
`ANAS_PROBE_WRITE_MARKERS=false` 只读验证记录仍存在。

## 发现并修复的问题

### 测试与运行器

1. 原 smoke 查询使用了错误的 Compose project name，查不到容器也会返回成功。现已查询
   全部真实容器并检查状态、健康和重启次数。
2. 原 smoke 直接执行 `start`，不会把刚修改的镜像内容重建进去。现统一执行
   `start --build`，保证被验证的是本轮代码。
3. 原 smoke 固定等待 30 秒，不能区分正常的长初始化和卡死。现使用有上限的自适应轮询，
   超时后仍保留逐容器诊断结果并执行清理。
4. runner 创建的 macvlan shim 原来总在宿主 namespace，无法与隔离 dockerd 的网络互通。
   新增绝对路径 `NETWORK_NAMESPACE_PATH` 支持，网络脚本通过 `nsenter` 在目标 namespace
   执行；停止时同时删除 Docker macvlan 网络和 shim。
5. macvlan gateway 曾错误使用宿主地址；现改用 `DEFAULT_GATEWAY_IP`。显式提供
   `HOST_IP`、`INTERFACE`、掩码时不再覆盖用户网络配置，`LOCAL_DNS_SERVER` 也可配置。
6. Traefik 的 Docker socket 改为 `${DOCKER_SOCKET_PATH:-/var/run/docker.sock}`，使测试和
   生产可选择不同 daemon。
7. Traefik provider 原来固定选择名为 `traefik` 的网络，但实际 Compose 网络会带
   `NETWORK_PREFIX`；多网络容器的网络枚举顺序变化时，Traefik 会偶发选择不可达的数据库
   地址，Keycloak 因而返回 504。provider 默认网络和现有显式标签现统一使用渲染后的
   `${NETWORK_PREFIX}traefik`，并增加静态回归检查。

### Samba、BIND 与 DNS

1. Samba DC 初始化脚本改为严格错误处理；测试改用满足域策略的专用复杂凭据。
2. `/certs` 没有完整 ACME 文件时，域控现在生成并持久化 3072 位自签名证书，SAN 包含
   `nas.test` 和域控 FQDN；健康检查要求域、管理员、88/389/636 端口和 DNS 均可用。
3. 域控 Dockerfile 调整层次，将大体积 apt 层放到动态 root 文件之前，重复构建可复用缓存。
4. Samba FS 镜像补齐 init/service 脚本执行权限，使用可配置 DNS，并以 `wbinfo -t` 作为
   健康检查；服务启动脚本中的错误 shell 引号也已修复。
5. 初版动态 DNS 使用 GSS `nsupdate` 写 BIND DLZ。BIND 通过 SSHFS 挂载域控数据库时，
   写操作会使 `named` 内部进程反复退出，而 Docker 容器仍显示 running。现改为：
   域控使用 `samba-tool dns` 在本地 AD 数据库中幂等写入全部 A 记录，写完发布 marker；
   BIND 等待 marker 后再只读加载数据库。验证中 `named` 仅启动一次，全部内部记录可见。
6. 域控、BIND、文件服务器和应用容器分别使用正确的测试地址：域控 FQDN 指向
   `10.254.0.1`，应用内部域名指向 Traefik 的 `10.254.0.2`。

### 应用服务

1. MeshCentral 原来在每次启动时安装 LDAP/MySQL Node 模块，造成重启循环；依赖现固定为
   `ldapauth-fork@6.1.0` 和 `mysql@2.18.1` 并在镜像构建时安装，使用独立 npm prefix。
2. Keycloak 默认 realm 与 OIDC discovery 路径统一为 `master`，修复 NetBird discovery URL。
   非标准 HTTPS 端口需要 Keycloak 23 的独立 `KC_HOSTNAME_PORT` 参数；现与
   `TRAEFIK_BASE_PORT` 保持一致，discovery issuer 包含正确外部端口。服务探针还会在
   Quarkus 启动期间有上限地重试，避免把 running 状态误当成应用已就绪。
3. Imaginary 镜像虽然会在中国网络模式切换 Alpine 软件源，但 Go 模块下载仍默认访问
   `proxy.golang.org`，完整重建时会因 TLS 超时失败。构建现允许显式覆盖 `GOPROXY`；未
   覆盖时，中国网络按国内代理、阿里云代理、直连依次回退，其他网络按官方代理、直连
   回退，网络错误也会尝试下一项。
4. Nextcloud 的 Samba hosts 映射改用可配置域控地址。应用安装增加有限重试；中国网络模式
   从官方 Nextcloud app-store API 选择与当前平台兼容的最新稳定版本，GitHub Release
   不可直连时通过可配置镜像下载，并在启用后执行应用完整性检查。
5. LDAP 搜索属性列表原来会多出一个空项，最终生成 `(=admin)`，使 LDAP 测试报
   `Bad search filter`。现按非空属性构造过滤器，`occ ldap:test-config s01` 和管理员查询
   均通过。
6. Preview Generator 自动选择到 5.12.1，但该版本使用的 Symfony 接口与 Nextcloud 30
   不兼容并触发 PHP fatal。当前为 Nextcloud 30 固定使用已验证的 5.7.0，并保留版本覆盖
   入口。
7. Memories 首次地理数据初始化还暴露了下载、重复执行和中断恢复问题。现通过可配置
   GitHub 镜像下载，执行时使用 `--force` 与 10000 条事务批次；对临时修改的应用源文件
   持久化备份并在中断后自动恢复。成功后写入数据卷中的完成 marker，后续容器重建会跳过
   约 72.6 万条记录的重复导入。
8. Nextcloud 覆盖镜像自带 healthcheck：必须同时满足 notify-push HTTP 可用和
   `/run/nextcloud-tasks.ready` 存在。只有 Talk、preview generator、notify push、LDAP、
   SAML 等初始化任务全部完成才会健康，避免“Web 已启动但后台配置仍失败”的假绿。
9. 镜像内 Nginx 对根路径 `/` 明确返回 500，旧 healthcheck 因此把实际可用的 Nextcloud
   判为 unhealthy；健康探针现改用镜像提供的 `/ping` 端点，并继续要求后台任务 marker。
10. `.test` 域的自签名证书会使 notify-push 自检拒绝 Nextcloud；push 启动脚本新增显式的
   `NEXTCLOUD_NOTIFY_PUSH_ALLOW_SELF_SIGNED` 测试开关。生产默认仍校验证书，本测试配置
   才启用该开关。
11. 同一自签名 CA 也不在 Nextcloud 镜像的系统信任库中；测试配置显式设置
   `LDAPTLS_REQCERT=never` 后验证 LDAPS。生产部署不应沿用此项，而应把真实内部 CA 导入
   Nextcloud 信任库并恢复证书校验。

## 功能验证结果

最终验证使用 v14 数据目录；该目录先完成完整冷启动和 Memories 一次性地理数据导入，
再用于容器重建与持久化复测。最新代码的最终 smoke 在 150 秒后达到全栈稳定。验证项目如下：

- 全部 20 个测试容器 running，health 无 starting/unhealthy，`RestartCount=0`；
- Samba 域级别可读取，自定义管理员存在，KDC/LDAP/LDAPS 端口可连接；
- Samba FS 成功入域，`wbinfo -t` 信任检查通过；
- `fengoffice.nas.test` 解析为 `10.254.0.1`，应用域名解析为 `10.254.0.2`；
- BIND `named` 启动次数为 1，没有 DLZ 写入导致的内部重启；
- Nextcloud 已安装、非维护模式、无需 DB 升级，必需应用启用且 LDAP 配置测试通过；
  Memories 原始地理数据导入约 72.6 万行，当前数据库中的有效行数为 497,024；容器重建
  日志明确显示跳过已完成的地理数据初始化；
- Keycloak master realm OIDC discovery、Nextcloud、LAM、MeshCentral、NetBird 的 Traefik
  HTTPS 路由返回成功或预期重定向；
- PostgreSQL 和 MariaDB 均能创建、写入并读取专用探针表；
- 停止后再用同一数据目录启动，两个数据库探针记录仍存在，域、DNS、Nextcloud 和
  Samba FS 信任检查仍通过；
- 最终停止后隔离 daemon 中无测试容器，测试 macvlan 网络和 shim 均已清理。

服务探针输出保存在：

- `/home/whl/anas-refactor-test/test-env/reports/server-2026-07-13-smoke-v23.log`
- `/home/whl/anas-refactor-test/test-env/reports/server-2026-07-13-service-probes-v20.log`
- `/home/whl/anas-refactor-test/test-env/reports/server-2026-07-13-persistence-restart-v22.log`
- `/home/whl/anas-refactor-test/test-env/reports/server-2026-07-13-go-test-v22.log`

## 未覆盖和剩余工作

以下不是本轮运行故障，而是当前 cask 的实现边界，因此不能宣称相应业务已经端到端完整：

1. Keycloak 目前能启动并提供 master realm discovery，但尚未自动创建 NetBird 等业务
   client、redirect URI、LDAP federation 和角色映射；NetBird 的真实登录流程未完成。
2. FreeRADIUS 当前是可启动的实验性镜像和端口脚手架，未配置测试 NAS/client、用户和
   EAP/RADIUS 认证用例。
3. DDNS 使用 `manual` provider，未向任何真实 DNS 提供商发送更新。
4. `.test` 域不能申请公共 ACME 证书；本轮验证的是自签名 TLS fallback，浏览器正式信任链
   仍需真实域名和证书。
5. 未从真实 Windows、macOS、移动端执行 SMB、Nextcloud Talk 音视频、TURN 穿透和
   NetBird peer 组网；这些需要客户端、外部网络和 IdP 配置。

## 复测与清理

复测前确认隔离 daemon 与 namespace 已存在，然后：

```sh
cd /home/whl/anas-refactor-test
export PATH="$PWD/.tools/go/bin:$PATH"
export DOCKER_HOST=unix:///run/anas-docker-test.sock

go run ./cmd/anas start --build \
  -c test-env/server-validation-2026-07-13.yml \
  -b .anas-test/runtime/server-validation-2026-07-13-v8

ANAS_PROBE_WRITE_MARKERS=false \
  ./test-env/scripts/server-functional-probes.sh

go run ./cmd/anas stop -b .anas-test/runtime/server-validation-2026-07-13-v8
```

清理后检查：

```sh
docker -H unix:///run/anas-docker-test.sock ps -aq
docker -H unix:///run/anas-docker-test.sock network ls
sudo ip netns exec anas-test ip link show anas_bridge
```

第一条应无测试容器，网络列表不应包含本轮 `anas_test_*` 网络，最后一条应返回设备不存在。
