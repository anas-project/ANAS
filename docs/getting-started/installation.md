# 安装与要求

## 主机要求

ANAS 面向 Linux 主机，运行服务需要 Docker Engine 和 Docker Compose v2。推荐使用 Btrfs 承载 workspace；其他文件系统也能运行，但无法提供基于 Btrfs 的本地快照。

还需要：

- 足够创建 workspace、数据目录和容器网络的主机权限；
- 可以访问所选 Module 镜像和软件源的网络；
- 为所有服务准备的持久存储；
- 如果使用域名和 HTTPS，准备 DNS 控制权以及所需的 DNS API 凭据。

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
