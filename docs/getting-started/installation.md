# 安装与要求

## 主机要求

ANAS 面向 Linux 主机，运行服务需要 Docker Engine 和 Docker Compose v2。推荐使用 Btrfs 承载 workspace；其他文件系统也能运行，但无法提供基于 Btrfs 的本地快照。

还需要：

- 足够创建 workspace、数据目录和容器网络的主机权限；
- 可以访问所选 Module 镜像和软件源的网络；
- 为所有服务准备的持久存储；
- 如果使用域名和 HTTPS，准备 DNS 控制权以及所需的 DNS API 凭据。

## 安装布局

发布包应保持二进制和 Module bundle 的相对布局：

```text
/opt/anas/
  bin/anas
  modules/
```

这样 `anas` 可以自动找到 Module。若两者不在同一安装根目录，显式设置：

```bash
export ANAS_MODULE_ROOT=/opt/anas/modules
```

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
```

接下来执行[首次部署](quick-start.md)。
