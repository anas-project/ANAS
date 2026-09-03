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
Release 下载对应静态归档，使用同一 Release 的 `SHA256SUMS` 校验后安装
`/usr/local/bin/anas` 与 `/usr/local/bin/anasd`。覆盖现有程序或写入源偏好之前，安装器还会
实际运行下载的 CLI，确认其自报版本与 Release tag 一致。默认安装还会以 root 创建
`/etc/anas/anasd.yml`（`0600`）、安装并启动 `anasd.service`；因此即使二进制目录可写，
服务安装仍会明确调用 `sudo`。这是为了满足服务配置、systemd unit 与 daemon root 身份的权限边界。

从 GitHub 安装并把 GitHub/GHCR 设为后续默认源：

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/anas-project/ANAS/master/install.sh | sh
```

从 CN 源安装，并把 CNB 设为后续默认分发源：

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://cnb.cool/anas.dev/ANAS/-/git/raw/master/install.sh | sh -s -- --source cn
```

CNB Release 附件由 GitHub Release 原样同步并再次校验 SHA-256，不在 CNB 重新编译；安装器
通过公开的 `/-/releases/latest` 跳转解析当前稳定 tag。

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

默认服务配置使用 `lan`、管理端口 `8080`、`/var/lib/anas/console`，且初始 workspace 清单为空。
首次安装时可用 `ANAS_MANAGEMENT_PORT` 选择其他端口；随后通过
`/etc/anas/anasd.yml` 注册 `anas init` 创建的 workspace 并重启服务。同一安装命令可升级
CLI、daemon 和 unit，但不会覆盖已有服务配置。

无权使用 `/usr/local/bin` 或不希望调用 `sudo` 时，可以安装到用户目录（并确保该目录在
`PATH` 中）。自定义安装目录默认只安装二进制，不创建 systemd 服务：

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/anas-project/ANAS/master/install.sh | sh -s -- --install-dir "$HOME/.local/bin"
```

安装脚本依赖 `curl`、`tar`、`install`，以及 `sha256sum` 或 `shasum`；默认服务安装还需要
`systemctl`。可用 `ANAS_INSTALL_SOURCE=github|cn`、`ANAS_INSTALL_DIR`、
`ANAS_MANAGEMENT_PORT` 做非交互覆盖；程序侧可用
`ANAS_DEFAULT_SOURCE=official|official-cn` 临时覆盖安装器默认值。

卸载会停止/禁用服务并删除 Core 二进制与 unit，但保留 workspace、console state 和服务配置：

```bash
curl --proto '=https' --tlsv1.2 -fsSL https://raw.githubusercontent.com/anas-project/ANAS/master/install.sh | sh -s -- --uninstall
```

追加 `--purge` 会删除 `/etc/anas/anasd.yml` 与当前用户的源偏好；即使 purge 也不会删除
workspace 或 `/var/lib/anas` 中的控制面状态。

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

ANAS Core release 包含 CLI、嵌入管理前端的 daemon、最小权限 helper、服务配置样例和
systemd unit，不包含 Module bundle。首次初始化会在找不到本地 Module bundle 时，根据外部
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

## 记录管理控制台入口

`anasd` 的 `lan` 模式会绑定全部 IPv4/IPv6 接口。首次引导允许同网设备立即通过明文 HTTP 配置 NAS；这个引导窗口**不具备机密性或抗主动劫持能力**，主机防火墙与接口隔离由管理员负责。若不接受该边界，改用 `loopback` 配合 `ssh -L`，或先在 NAS 上生成临时 TLS：`sudo anas console tls --self-signed`。

服务配置完成并启动后立即记录两个地址：

```bash
sudo anas console status
```

- `Direct recovery (local owner)`：不依赖 IAM 的直连恢复入口，应保存到运维记录。
- `Traefik / OIDC`：启用受信代理后的日常管理入口；未配置时不会显示。

代理入口不提供本地账号登录链接。IAM 故障时，从已记录的直连地址使用本地管理员恢复；完整字段与 mTLS 配置见 [`anasd` 服务配置](../reference/anasd-service-configuration.md)。

接下来执行[首次部署](quick-start.md)。
