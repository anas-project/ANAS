# 容器镜像发布

ANAS 自建镜像由 [`.github/workflows/container-images.yml`](https://github.com/anas-project/ANAS/blob/master/.github/workflows/container-images.yml) 构建并发布到 GHCR 和 CNB。`.github/images.json` 是 Dockerfile 与发布镜像的登记表。

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
ghcr.io/anas-project/anas-<image>:<version>-r<revision>
docker.cnb.cool/anas.dev/anas/<image>:<version>-r<revision>
```

`ANAS_IMAGE_REGISTRY` 选择 ANAS 自建镜像来源。`global.chinese_speedup: true` 默认切换到 CNB；`GHCR_REGISTRY` 只控制第三方 GHCR 镜像。

## CI 行为

- Pull Request：只构建受影响的镜像，不推送。
- `master`：只处理登记的构建上下文发生变化的 Module。
- 两个 Registry 都没有固定标签时：构建一次并发布 GHCR，再复制运行平台 manifest 到 CNB。
- 只有一侧存在时：补齐另一侧。
- 两侧都存在时：验证后结束，不覆盖已有标签。
- 构建目标为 `linux/amd64` 和 `linux/arm64`，GHCR 保留 provenance 与 SBOM。

修改 Dockerfile 时同时检查：

1. Dockerfile 在 `.github/images.json` 中恰好登记一次；
2. Compose 镜像名和标签与 `version`、`revision` 一致；
3. 同版本的 `revision` 正确递增，或新版本重置为 `1`；
4. `go test ./...` 和相关 Module 测试通过。

## 首次发布与恢复

首次发布可手工运行 `Container images` 工作流并选择 `module=all`。发布后确认 GHCR packages 可匿名拉取，并验证两个 Registry 的多架构 manifest。

工作流需要：

- GitHub 自动提供的 `GITHUB_TOKEN` 写入 GHCR；
- `CNB_REGISTRY_TOKEN` 写入 CNB Registry；
- `CNB_TOKEN` 供代码仓库同步工作流写入 CNB Git 仓库。
