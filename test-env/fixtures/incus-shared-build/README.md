# Incus 共享源码与 staging 镜像构建验收

`server-incus-shared-build-e2e.py` 对 Incus Provider、Forgejo Actions controller 与 AI Agent
orchestrator 各执行源码 checkout 和 staging 两种布局的真实 Compose/BuildKit 构建。
入口不启动 ANAS 部署，不访问运行时 `.env`，不发布镜像，也不连接已有业务 Docker。

## 前置条件与隔离

操作者必须明确授权测试服务器。只在**全新、可销毁的 QEMU VM** 中安装测试依赖，使用新的
cloud-init 身份（例如 `anas-incus-build-abc123`）、独立可写系统盘和临时 SSH 身份。
不得把物理宿主 Docker socket、业务目录、块设备或网络桥映射进 VM。

VM 内需要官方发行版提供的 Docker、Compose v2 和 Buildx。测试 daemon 必须已经运行，
且使用单独的 root-owned Unix socket（例如 `/run/anas-shared-build-test.sock`）与实际独立
数据根（例如 `/var/lib/anas-shared-build-test`）。入口检查精确 VM 身份、root、socket 类型、
专用数据根、固定 daemon ID 和空容器库存；仍执行仓库原有的独立 Docker guard。
默认 `/var/run/docker.sock`、指向业务 socket 的符号链接和已有容器均不准入。

从受信、匹配版本的源码编译检查器，并把检查器、源码快照和文件摘要交付到 VM：

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 GOPROXY=off \
  go build -o /tmp/check-shared-build ./cmd/check-shared-build
```

源码必须包含 `.github` 构建清单、Module 构建上下文、`go.mod`、`go.sum`、声明的共享包以及
本入口和 `server-require-isolated-docker.sh`。只复制受信源码，不复制 `.git`、工作区状态、
运行凭据、开发者 Docker 登录配置或生产 `.env`。报告目录必须全新，且不在源码目录中。

## 执行

下面路径是 VM 内的示例，须替换为本次交付的实际路径。报告父目录须事先存在：

```sh
sudo python3 /home/anas-test/source/test-env/scripts/server-incus-shared-build-e2e.py \
  --vm-id anas-incus-build-abc123 \
  --source-root /home/anas-test/source \
  --checker /home/anas-test/bin/check-shared-build \
  --report-root /home/anas-test/reports/run-a \
  --docker-socket /run/anas-shared-build-test.sock
```

默认使用 Docker Hub 与 Go 官方模块代理。可以显式指定 `--registry`（主机名及可选仓库前缀）
和 `--go-module-proxy`（HTTPS 地址及可选 `,direct`）。参数不得含登录信息、查询 token 或
关闭校验的配置。入口通过仓库既有 `DOCKER_HUB_REGISTRY`、`GOPROXY_URL` 设置传入，实测
Compose 的嵌套默认值；不把这些值投影到业务容器，不关闭 Go checksum database。
Incus Provider 和 controller 的共享仓库代码仍使用 `GOPROXY=off`，只有单独固定的外部
Incus CLI 和 AI Agent 第三方依赖允许按显式代理解析。

## 验收内容与边界

检查器的 `--json` 输出是静态构建输入报告（`anas.shared-build-inputs/v1`），不是执行许可或
构建成功证明。它绑定共享源码、Module 上下文、可执行权限和原始 build 声明的摘要，拒绝
源码漂移、缺失覆盖路径、未知构建字段、额外 SSH/secret 上下文和凭据 build args。
运行时环境、命令、网络和挂载不进入该报告。

原生入口按真实 Module 的 build 声明生成**仅用于构建的 Compose 投影**。相对路径、命名
`shared` 上下文、Dockerfile、参数和构建网络保留，只添加独有测试镜像名与清理标签。
Forgejo 的两个服务必须声明同一个构建，实际镜像只构建一次。每种布局覆盖三个镜像，
六次构建均使用 `--no-cache`，不能用一次成功、Go 交叉编译或空测试结果替代。

源码布局保留默认 `../..`；staging 复制真实 Module 构建文件，并显式设置
`ANAS_SHARED_BUILD_CONTEXT` 为匹配源码根。两者均经真正的 `docker compose config` 核对。
缺少 staging 覆盖变量时，检查器与真实构建均必须失败；构建失败须明确由缺失共享输入造成，
网络超时、镜像缺失或取消不算该反例通过。

生成镜像通过不联网、只读、无额外 capabilities 的短期容器检查 `65532:65532` 身份、入口
文件和二进制摘要，并核对 Forgejo 镜像包含固定版本的 Incus CLI。源码与 staging 的最终
业务二进制和输入摘要须一致，完成后重新读取源码与 staging 输入，漂移仍判失败。

**staging 是构建布局夹具，不是 Core 完整渲染部署。** 本入口证明共享上下文与镜像构建路径，
不证明 `anas build/apply`、Hook、Provider ensure、容器业务启动、guest 生命周期、镜像发布
或入站功能可用。这些目标保持各自的真实验收门禁。

## 失败、清理和证据

每个命令都有期限及有界私有 stdout/stderr；超时终止其测试客户端进程组，不停止其他服务。
每次失败保留独立报告，不覆盖失败记录。总结要求六个唯一成功结果、缺失覆盖反例、输入复核
和空容器库存同时成立。网络不可达、任何阶段未完成或清理未确认均不能报告通过。

清理前复核 daemon ID。容器按本次唯一标签和名称核对，镜像按本次标签与精确 tag 删除；
不执行全局 prune、不删除业务镜像或其他实验。不把 inspect 错误当作对象已不存在。
只收集本次报告、输入摘要与源码记录；不收集临时 SSH 或 runtime 私钥。

报告收集后，由实验所有者正常关闭并核对该 VM，再按固定清单删除本次系统盘和临时身份。
物理宿主的 Docker 容器、网络、卷、服务、配置以及 nft/路由应与实验前的只读快照独立对照。

离线安全回归：

```sh
python3 -m unittest discover -s test-env/scripts -p 'test_incus_shared_build_e2e.py'
go test ./cmd/check-shared-build
```
