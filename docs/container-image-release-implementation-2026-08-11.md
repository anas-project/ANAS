# Cask 容器镜像发布实施记录（2026-08-11）

## 目标与结论

本次改动把仓库内所有 Dockerfile 纳入 GitHub Actions 和 GHCR 的统一发布流程，并解决
“上游镜像版本没有变化，但 ANAS 仍需修复 Dockerfile 或 build context”时无法产生新版本
的问题。

发布版本由两部分组成：

- `version`：规范化后的上游版本，使用 `MAJOR.MINOR.PATCH`。
- `revision`：ANAS 对该上游版本的打包修订号，使用从 `1` 开始的正整数。

完整发布身份和容器 tag 为 `<version>-r<revision>`。例如，上游 Nextcloud 仍是
`34.0.2`，ANAS 第一次打包为 `34.0.2-r1`；只修复 ANAS 的镜像内容时发布
`34.0.2-r2`。上游升级时更新 `version` 和 `app_version`，并把 `revision` 重置为 `1`。

项目尚未发布，因此这次直接修改 manifest、lock、deployment 和 snapshot 契约，不提供
旧格式兼容层。

## GitHub 归属与命名空间

- GitHub 组织：`anas-project`
- 代码仓库：`https://github.com/anas-project/ANAS`
- Git clone：`git@github.com:anas-project/ANAS.git`
- Go module：`github.com/anas-project/ANAS`
- 容器命名空间：`ghcr.io/anas-project/`

国内镜像与代码镜像使用：

- CNB 组织：`anas.dev`
- CNB 仓库：`https://cnb.cool/anas.dev/ANAS`
- CNB 容器命名空间：`docker.cnb.cool/anas.dev/anas/`

其他工作副本可执行以下命令切换远端：

```bash
git remote set-url origin git@github.com:anas-project/ANAS.git
```

## 仓库与运行时契约

所有 `casks/mods/*/cask.yml` 都必须声明正整数 `revision`。该字段已经进入：

- cask manifest 解析和校验；
- cask lock 文件与 `anas lock --json` 输出；
- frozen deployment manifest；
- snapshot 中的 cask 发布身份；
- 升级、降级和数据兼容性判定。

runner 比较发布版本时先按 SemVer 比较 `version`，相同后再比较数字 `revision`。同一
上游版本的 revision 降级会被拒绝。同版本不同 revision 也属于一次发布跃迁，并继续受
`upgrade.data_breaking` 和快照保护规则约束。

`app_version` 只保存上游版本的原始展示形式。例如上游版本无法直接表示为三段 SemVer
时，`version` 保存规范化值，`app_version` 保存原始拼写。

## 镜像清单

`.github/images.json` 是 Dockerfile 与发布镜像的唯一登记表。本次登记 11 个 cask、12 个
镜像，其中 `samba_dc` 的主镜像和 anchor worker 镜像作为同一 cask 成组发布。

CI 会扫描 `casks/mods` 下的全部 Dockerfile；任何 Dockerfile 未登记、重复登记，或者
Compose 中的镜像 tag 与 cask 的 `version`/`revision` 不一致，都会使校验失败。

所有 ANAS 自建镜像统一使用：

```text
ghcr.io/anas-project/anas-<image>:<version>-r<revision>
```

不发布可变的 `latest` tag。默认构建平台为 `linux/amd64` 和 `linux/arm64`。

Compose 使用 `ANAS_IMAGE_REGISTRY` 单独选择 ANAS 自建镜像来源，默认值为
`ghcr.io/anas-project`。启用 `CHINESE_SPEEDUP=true` 时，默认切换为
`docker.cnb.cool/anas.dev/anas`；`GHCR_REGISTRY` 继续只负责第三方 GHCR 镜像，避免把
第三方命名空间与 ANAS 的 CNB 仓库路径混在一起。

## GitHub Actions 发布规则

工作流位于 `.github/workflows/container-images.yml`：

1. Pull Request 只构建受影响的镜像，不推送，用于验证 Dockerfile 和多架构构建。
2. 合并到 `master` 后，只发布 build context 发生变化的 cask 镜像。
3. 同一上游版本发生镜像内容修改时，`revision` 必须恰好增加 `1`。
4. `version` 变化时，`revision` 必须重置为 `1`。
5. 发布前分别检查 GHCR 与 CNB；两个 registry 都不存在时才执行构建，已存在的固定 tag
   永不覆盖。
6. 生成 provenance 和 SBOM，并使用 GitHub Actions cache。
7. 首次发布使用 `workflow_dispatch`，参数 `cask=all`，一次构建当前清单中的全部镜像。
8. 非 PR 构建只执行一次，同时把相同多架构结果推送到 GHCR 与 CNB，不在 CNB 重复
   编译。如果其中一个 registry 已有该 tag、另一个缺失，GitHub Actions 直接复制现有
   manifest 和镜像层到缺失的一侧，用于首次迁移或单侧发布故障恢复。

首次成功推送后，需要在 GitHub Packages 中确认每个 container package 为 public，才能
让未登录 GHCR 的部署端直接拉取。

## CNB 同步与发布规则

GitHub 仍是上游主仓库。`.github/workflows/cnb-sync.yml` 在任意分支或 tag 变化时，把全部
远端分支和 tag 镜像到公开仓库 `anas.dev/ANAS`。GitHub 仓库需要配置一个名为
`CNB_TOKEN` 的 secret，令牌仅需对该 CNB 仓库拥有 Git 写权限。

CNB 收到 `master` push 后读取 `.cnb.yml`，由
`scripts/ci/cnb-container-images.sh` 校验镜像登记和 Compose tag。正式镜像由 GitHub
Actions 的同一次 BuildKit 构建同时推送到两个 registry，因此不重复消耗 CNB 构建额度。
灾备恢复时可以在 `master` 分支页面点击“从 GHCR 同步全部 Cask 镜像”，用
`docker buildx imagetools create` 把 CNB 中缺失的多架构固定 tag 从 GHCR 直接复制过来。

CNB 镜像使用：

```text
docker.cnb.cool/anas.dev/anas/<image>:<version>-r<revision>
```

GitHub 构建启用双架构 manifest、cache、provenance 和 SBOM，并在发布前同时检查两个
registry。两边都没有时构建并双推；仅一边存在时从现有 registry 直接补齐另一边；两边
都存在时校验后结束。所有路径都不会覆盖已存在的固定 tag，CNB 灾备同步也会跳过已有 tag。

## 文档整理

外部调研、竞品比较和历史技术评估统一移动到 `docs/research/`，并增加目录索引。稳定的
架构文档、命令契约和运行时契约仍保留在原有位置。容器镜像发布属于已经实施的子集；
cask bundle 的 OCI 分发仍保留为后续设计。

## 验证记录

提交前执行：

```bash
go test ./...
git diff --check
```

两项均通过，同时确认仓库中不再残留旧的 Go module 路径或
`ghcr.io/whlsxl` 镜像命名空间。

## 首次发布操作

GitHub 首次发布需要：

1. 查看 GitHub Actions 的首次校验结果；
2. 手工运行 `Container images` 工作流，选择 `all`；
3. 确认全部镜像成功推送到 `ghcr.io/anas-project/`；
4. 将新建的 container packages 设置为 public；
5. 用一台干净机器按 Compose 中的固定 tag 做匿名拉取验证。

CNB 首次同步需要：

1. 在 CNB 创建公开组织 `anas.dev` 和公开仓库 `ANAS`；
2. 创建 CNB Git 访问令牌，并保存为 GitHub repository secret `CNB_TOKEN`；
3. 创建具备当前仓库 Docker 制品写权限的 CNB 令牌，并保存为
   `CNB_REGISTRY_TOKEN`；
4. 手工运行一次 GitHub 的 `Sync repository to CNB` 工作流；
5. 手工运行 GitHub 的 `Container images` 工作流，参数 `cask=all`，同时发布两个
   registry 的 12 个镜像；
6. 匿名拉取 `docker.cnb.cool/anas.dev/anas/<image>:<version>-r<revision>` 验证。
