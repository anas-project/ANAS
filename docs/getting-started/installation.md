# 安装与要求

## 主机要求

ANAS 面向 Linux 主机，运行服务需要 Docker Engine 和 Docker Compose v2。推荐使用 Btrfs 承载 workspace；其他文件系统也能运行，但无法提供基于 Btrfs 的本地快照。

还需要：

- 足够创建 workspace、数据目录和容器网络的主机权限；
- 可以访问所选 Module 镜像和软件源的网络；
- 为所有服务准备的持久存储；
- 如果使用域名和 HTTPS，准备 DNS 控制权以及所需的 DNS API 凭据。

## 一行命令安装主程序

安装脚本目前只支持 Linux，并自动识别 `x86_64`/`amd64` 与 `aarch64`/`arm64`。脚本从
Release 下载对应静态二进制，使用同一 Release 的 `SHA256SUMS` 校验后安装到
`/usr/local/bin/anas`；仅当目标目录不可写时调用 `sudo`。

从 GitHub 安装并把 GitHub/GHCR 设为后续默认源：

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/anas-project/ANAS/master/install.sh | sh
```

从 CN 源安装，并把 CNB 设为后续默认分发源：

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://cnb.cool/anas.dev/ANAS/-/git/raw/master/install.sh | sh -s -- --source cn
```

安装器把选择写入 `${XDG_CONFIG_HOME:-$HOME/.config}/anas/source`。选择 CNB 时保存
`official-cn`；之后 `anas init` 创建的新配置，以及未显式声明 `module_source` 的
`anas config import`，都会持久化：

```yaml
module_source: official-cn
global:
  chinese_speedup: true
```

因此 Module 从 CNB 获取，Compose 的 `ANAS_IMAGE_REGISTRY` 为
`docker.cnb.cool/anas.dev/anas`，运行期下载也使用国内镜像。workspace 一旦初始化，源已写入
自身的 `config.yml`，换机或修改安装器默认值不会静默改变现有部署。外部配置中显式写出的
`module_source` 始终优先。

无权使用 `/usr/local/bin` 或不希望调用 `sudo` 时，可以安装到用户目录（并确保该目录在
`PATH` 中）：

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/anas-project/ANAS/master/install.sh | sh -s -- --install-dir "$HOME/.local/bin"
```

安装脚本依赖 `curl`、`tar`、`install`，以及 `sha256sum` 或 `shasum`。可用
`ANAS_INSTALL_SOURCE=github|cn`、`ANAS_INSTALL_DIR` 做非交互覆盖；程序侧可用
`ANAS_DEFAULT_SOURCE=official|official-cn` 临时覆盖安装器默认值。

## 选择 Module 源

顶层 `module_source` 选择 Module artifact 的首选 Registry：

```yaml
module_source: official       # GHCR 主源，CNB 回退
# module_source: cn           # CNB 主源，GHCR 回退
```

`cn` 会规范化为 `official-cn`。使用中国大陆源时，如果没有显式设置，ANAS 自动加入
`global.chinese_speedup: true`，使 Module、正式运行镜像和运行期下载源保持在同一国内
分发链路。若只想从 CNB 取 Module，可以覆盖默认：

```yaml
module_source: cn
global:
  chinese_speedup: false
```

首次初始化时直接导入外部配置：

```bash
anas init /srv/anas --config ./anas.yml
```

默认值写入初始化后生成的 `/srv/anas/config.yml`，并在渲染时导出
`CHINESE_SPEEDUP=true`；外部 `anas.yml` 保持原样。已有 workspace 使用
`anas config import ./anas.yml -w /srv/anas` 获得相同的规范化行为。

CLI 会从所选源读取 OCI catalog 和标准 tag list，并把固定版本安装到用户级内容寻址缓存。
Registry tag 只用于首次解析；`config.lock.yml` 同时记录 OCI manifest digest、解包内容
digest 和安装树 digest，普通 `sync` / `apply` 不会静默追踪移动后的 tag。

## Module 安装与缓存

ANAS Core release 只包含二进制。首次初始化会在找不到本地 Module bundle 时，根据外部
配置自动引导下载；随后显式解析并写 lock：

```bash
anas init /srv/anas --config ./anas.yml
anas module update -w /srv/anas
```

列出来源和历史版本、单独预取固定版本：

```bash
anas module list --source cn
anas module versions nextcloud --source cn
anas module install nextcloud@34.0.2-r4 --source cn
```

恢复或换机时，`anas module sync -w /srv/anas` 严格按现有 lock 获取缺失包，不升级版本。
默认缓存位于 `~/.cache/anas/modules/`，可用 `ANAS_MODULE_CACHE` 覆盖。Registry 私有凭据
读取 Docker `config.json` 中的基本认证记录；公开 GHCR/CNB 使用标准 Bearer challenge。

`--module-root` / `ANAS_MODULE_ROOT` 仍是优先级最高的本地开发覆盖入口。源码检出中的
`modules/` 和相邻 `contracts/` 可继续直接使用，不经过 Registry。

从源码运行时，需要仓库声明的 Go 工具链：

```bash
go run ./cmd/anas --help
```

## 验证依赖

在开始部署前确认：

```bash
docker version
docker compose version
anas --help
anas version
```

接下来执行[首次部署](quick-start.md)。
