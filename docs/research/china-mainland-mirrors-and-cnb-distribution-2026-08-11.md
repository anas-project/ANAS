# 中国大陆镜像与 CNB 发行方案（2026-08-11）

## 结论

ANAS 采用两层方案：源码构建时由 `CHINESE_SPEEDUP` 统一切换国内代理；正式发布时，
再由 CNB 构建并托管完整的国内 OCI 镜像集。前者保证开发者能在中国大陆完成构建，
后者让普通用户只访问 `docker.cnb.cool`，无需分别连接 Docker Hub、GHCR、Quay 和
GitHub Release。

`CHINESE_SPEEDUP` 是总开关，不是“仅替换 apt”的提示位。值为 `true` 时，ANAS 在
用户没有显式覆盖的情况下生成以下全局环境：

| 变量 | 默认值 | 覆盖范围 |
| --- | --- | --- |
| `APT_MIRROR_URL` | `https://mirrors.aliyun.com` | Debian、Ubuntu 构建依赖 |
| `APK_MIRROR_URL` | `https://mirrors.aliyun.com` | Alpine 构建依赖 |
| `NPM_REGISTRY_URL` | `https://registry.npmmirror.com` | MeshCentral npm 依赖 |
| `GOPROXY_URL` | `https://goproxy.cn,direct` | module hook 与 ddns-go Go modules |
| `GITHUB_DOWNLOAD_PROXY_PREFIX` | `https://files.m.daocloud.io/` | LAM、Nextcloud GitHub Release |
| `NEXTCLOUD_APPSTORE_URL` | `https://files.m.daocloud.io/apps.nextcloud.com/api/v1` | Nextcloud 应用元数据 |
| `DOCKER_HUB_REGISTRY` | `m.daocloud.io/docker.io` | Dockerfile 基础镜像 |
| `LLNG_DOCKER_HUB_REGISTRY` | `docker.1ms.run` | LemonLDAP::NG 基础镜像；该镜像不在 DaoCloud 白名单中 |
| `ANAS_IMAGE_REGISTRY` | `docker.cnb.cool/anas.dev/anas` | 全部正式运行时镜像（派生构建与上游 mirror） |
| `GHCR_REGISTRY` | `ghcr.nju.edu.cn` | 第三方 GHCR 基础镜像 |
| `QUAY_REGISTRY` | `quay.nju.edu.cn` | Quay 基础镜像 |

例如只替换企业内部 GHCR 镜像库，而保留其他中国默认值：

```yaml
env:
  CHINESE_SPEEDUP: "true"
  GHCR_REGISTRY: registry.example.cn/ghcr
```

GitHub 下载前缀采用“镜像根地址 + 去掉协议的源 URL”语义。例如
`https://github.com/owner/repo/releases/download/v1/file.tgz` 会变为
`https://files.m.daocloud.io/github.com/owner/repo/releases/download/v1/file.tgz`。

唯一无法由配置倒推影响的是首次 `go run ./cmd/anas`：Go 必须先编译 ANAS，程序才能
读取 `config.yml`。源码方式首次启动需先执行
`go env -w GOPROXY=https://goproxy.cn,direct`；预编译二进制没有这个先后关系。

## 为什么不能只配置 Docker daemon

Docker 的 `registry-mirrors` 只透明代理 Docker Hub，不能用一个 daemon mirror 替换
GHCR 或 Quay。ANAS 因而将三类 registry 显式写入 Compose 和 Dockerfile，并分别提供
变量。Docker 官方也说明其 pull-through mirror 只能镜像中央 Docker Hub：
<https://docs.docker.com/docker-hub/image-library/mirror/>。

DaoCloud 公共镜像支持 `docker.io`、`ghcr.io`、`quay.io` 的前缀映射，并保持源
manifest/blob 的 SHA-256；但公共服务存在白名单、限流、缓存保留和 tag 同步延迟，
适合源码构建与临时部署，不应作为生产发行的唯一来源：
<https://github.com/DaoCloud/public-image-mirror>。二进制下载镜像说明见
<https://github.com/DaoCloud/public-binary-files-mirror>。

软件包镜像参考：

- 阿里云 Debian：<https://developer.aliyun.com/mirror/debian>
- 阿里云 Ubuntu：<https://developer.aliyun.com/mirror/ubuntu>
- 清华 Alpine：<https://mirrors.tuna.tsinghua.edu.cn/help/alpine/>
- goproxy.cn：<https://github.com/goproxy/goproxy.cn>

## CNB 国内发行

CNB 同时提供国内 Git 托管、云原生构建和 `docker.cnb.cool` Docker 制品库。Docker
制品跟随代码仓库公开性；公开仓库的制品允许任何人匿名拉取。社区版当前每个顶级组织
包含 100 GiB Git 存储、100 GiB 对象/制品存储和每月 160 核时构建 CPU 免费额度：

- 制品和匿名访问：<https://docs.cnb.cool/zh/artifact/intro.html>
- 免费额度：<https://docs.cnb.cool/zh/pricing.html>
- 构建并推送 Docker 镜像：
  <https://docs.cnb.cool/zh/build/showcache/docker-build-and-push-to-cnb-artifact.html>
- GitHub 导入与跨平台同步：<https://docs.cnb.cool/zh/guide/migration-tools.html>

保留 GitHub 为上游主仓库，并自动同步到公开 CNB 仓库。当前发布流程完成：

1. 构建 `.github/images.json` 登记的 ANAS 派生镜像；
2. 按 `.github/mirrors.json` 中的 digest 读取 Docker Hub、GHCR、Quay 上游镜像，不重新构建；
3. 只组合 `linux/amd64`、`linux/arm64` 运行平台 manifest，排除 provenance 等 `unknown` 平台描述符以满足 CNB 元数据限制；
4. 以 `anas-*` 和 `anas-mirror-*` 两类名称同时发布到 GHCR 与 CNB；
5. 所有 Compose 运行镜像只使用 `ANAS_IMAGE_REGISTRY`，不生成另一份国内版 Compose；
6. 上游 `latest` 必须锁定 digest，ANAS 目标 tag 永远不可变且不得使用 `latest`。

实际采用的 CNB 地址为 `https://cnb.cool/anas.dev/ANAS`，运行镜像使用
`docker.cnb.cool/anas.dev/anas/<image>:<固定-tag>`。源码通过 GitHub Actions
镜像全部分支和 tag。镜像在 GitHub Actions 中只构建一次，同时推送到 GHCR 与 CNB；
CNB 不重复编译。灾备按钮可把 CNB 中缺失的固定 tag 从公开 GHCR 直接复制回来，并保留
多架构 manifest。

上游 mirror 示例：

```sh
docker.cnb.cool/anas.dev/anas/anas-mirror-postgres:18.4-alpine
docker.cnb.cool/anas.dev/anas/anas-mirror-nextcloud-talk:2026.07.30-2b9a7d12d3e6
```

最终用户只需：

```sh
git clone https://cnb.cool/<org>/anas
cd anas
docker compose pull
docker compose up -d
```

## 发布边界

仅把 Dockerfile 构建结果放入国内 registry 还不完整。Nextcloud 首次启动会访问应用商店
并下载固定应用和 Memories 地理数据；CNB 正式发行应把这些固定版本制品预置到镜像或
持久缓存。所有镜像复制和下载都应保留 digest/SHA-256 校验。CNB 的 100 GiB 免费对象
存储对当前单版本镜像集通常足够，但多版本、多架构长期保留必须在流水线中执行保留策略，
并监控实际去重后的用量。

CNB 是“免费托管开源项目及其公开容器制品”的国内平台，并不意味着平台软件本身是开源
项目。若发行需要服务 SLA、私有网络或不可变长期归档，应把相同镜像再同步到付费
ACR/TCR 或自建 Harbor；Harbor Proxy Cache 支持 Docker Hub、GHCR 和 Quay：
<https://goharbor.io/docs/main/administration/configure-proxy-cache/>。
