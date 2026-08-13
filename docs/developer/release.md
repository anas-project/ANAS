# 容器镜像发布

运行时镜像由 [`.github/workflows/container-images.yml`](https://github.com/anas-project/ANAS/blob/master/.github/workflows/container-images.yml) 同时发布到 GHCR 和 CNB。`.github/images.json` 登记 ANAS 派生构建，`.github/mirrors.json` 登记按 digest 锁定、未经修改的上游镜像。

## 发布身份

每个 Module 在 `module.yml` 中声明：

- `version`：规范化的上游 SemVer；
- `revision`：ANAS 对该上游版本的打包修订号，从 `1` 开始；
- `app_version`：上游版本的原始展示形式。

固定镜像标签为：

```text
<version>-r<revision>
```

修改构建上下文但不升级上游时，`revision` 必须恰好加一。升级 `version` 时，`revision` 必须重置为 `1`。固定标签不可覆盖，不发布 `latest`。

## Registry

```text
ghcr.io/anas-project/anas-<软件>:<version>-r<revision>
docker.cnb.cool/anas.dev/anas/anas-<软件>:<version>-r<revision>
```

未经修改的上游镜像使用 `anas-mirror-<软件>:<固定版本>`。清单同时保存上游引用、上游 manifest digest 和运行平台；即使来源使用 `latest`，发布输入也必须带 digest，ANAS 目标 tag 不得使用 `latest`。

`ANAS_IMAGE_REGISTRY` 选择全部运行时镜像来源。`global.chinese_speedup: true` 默认切换到 CNB，因此部署服务器只需访问 `docker.cnb.cool`；`DOCKER_HUB_REGISTRY`、`GHCR_REGISTRY` 和 `QUAY_REGISTRY` 只用于构建阶段解析基础镜像。

## CI 行为

- Pull Request：只构建受影响的镜像，不推送。
- `master`：只处理登记的构建上下文发生变化的 Module。
- Mirror：校验上游 digest，筛选 `linux/amd64`、`linux/arm64` 运行 manifest，原样发布到 `anas-mirror-*`，不重新构建上游软件。
- 两个 Registry 都没有固定标签时：构建一次并发布 GHCR，再复制运行平台 manifest 到 CNB。
- 只有一侧存在时：补齐另一侧。
- 两侧都存在时：验证后结束，不覆盖已有标签。
- 构建目标为 `linux/amd64` 和 `linux/arm64`，GHCR 保留 provenance 与 SBOM。

修改 Dockerfile 时同时检查：

1. Dockerfile 在 `.github/images.json` 中恰好登记一次；
2. Compose 镜像名和标签与 `version`、`revision` 一致；
3. 同版本的 `revision` 正确递增，或新版本重置为 `1`；
4. 所有 Compose 上游镜像都在 `.github/mirrors.json` 中锁定 digest，并使用 `anas-mirror-*`；
5. `go test ./...` 和相关 Module 测试通过。

## 首次发布与恢复

首次发布可手工运行 `Container images` 工作流并选择 `module=all`。这会同时生成派生镜像和全部上游 mirror。发布后确认 GHCR packages 与 CNB 制品均可匿名拉取，并验证两个 Registry 的运行平台 manifest。

工作流需要：

- GitHub 自动提供的 `GITHUB_TOKEN` 写入 GHCR；
- `CNB_REGISTRY_TOKEN` 写入 CNB Registry；
- `CNB_TOKEN` 供代码仓库同步工作流写入 CNB Git 仓库。
