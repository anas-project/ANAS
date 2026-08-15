# 容器镜像发布

运行时镜像由 [`.github/workflows/container-images.yml`](https://github.com/anas-project/ANAS/blob/master/.github/workflows/container-images.yml) 从专用的 `image-release` 分支同时发布到 GHCR 和 CNB。普通 `master` push 不发布镜像；只有合并到 `image-release` 才进入 revision 计算和发布链路。`.github/images.json` 登记 ANAS 派生构建，`.github/mirrors.json` 登记按 digest 锁定、未经修改的上游镜像。

## 整体同步流程

GitHub 是代码主仓库，GHCR 是容器首发仓库，CNB 同时承担国内代码镜像和容器分发。日常开发先进入 `master`；需要发布镜像时，把准备发布的 `master` 提交合并到 `image-release`。发布工作流自动生成 revision commit，从该 commit 构建镜像，成功后再把同一 commit 快进回 `master`。

```text
代码：功能分支 ──> master ──> image-release
                                  │
                                  ├──> 自动 revision commit
                                  ├──> 构建并验证镜像
                                  └──> 成功后快进 master

派生镜像：Dockerfile ──> GHCR ──> CNB Registry
上游镜像：固定 digest ──> GHCR ──> CNB Registry
异常恢复：             CNB Registry ──> GHCR
```

### 代码仓库

`.github/workflows/cnb-sync.yml` 在 GitHub 任意分支或 tag 推送、删除以及手工触发时运行。它获取全部 GitHub 分支和 tag，并使用 `--prune` 推送到 `https://cnb.cool/anas.dev/ANAS.git`，因此 GitHub 上的删除也会同步到 CNB。同步方向固定为 GitHub 到 CNB；CNB 不是代码真相源。

根据 [GitHub 的 `GITHUB_TOKEN` 触发规则](https://docs.github.com/en/actions/concepts/security/github_token#when-github_token-triggers-workflow-runs)，工作流自身 token 生成的普通 push 不会递归创建另一个 Actions run。成功发布的 finalize Job 因此使用 `CNB_TOKEN` 直接把对应的 `image-release`、`master` 和成功 tag 推送到 CNB。

代码到达 CNB 的 `master` 后，`.cnb.yml` 运行镜像元数据和 Compose 引用校验，不重复构建或发布容器。

### 容器镜像

`.github/workflows/container-images.yml` 负责容器发布：

- 目标为 `image-release` 的 PR 计算候选 revision 并构建验证，但不提交、不推送镜像；
- 合并到 `image-release` 后，工作流按 Module 自动计算并提交 revision；
- ANAS 派生镜像从登记的 Dockerfile 构建，首先推送 GHCR，再将运行平台 manifest 复制到 CNB；
- 未经修改的上游镜像先校验 `.github/mirrors.json` 中锁定的 digest，再复制到 GHCR，最后复制到 CNB；
- 两边均无固定 tag 时执行上述首发流程；只有一边存在时从已有一侧补齐缺失侧；两边均存在时只验证，不覆盖；
- 正常发布主方向是 GHCR 到 CNB。CNB 到 GHCR 仅用于 GHCR 缺失而 CNB 已有固定 tag 时的恢复。

因此，GHCR 和 CNB 本身都不会主动向对方推送；跨 Registry 复制由 GitHub Actions 中的 `scripts/ci/runtime-image.sh` 使用 Crane 执行。

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

正常发布不需要人工运行 revision 命令。工作流找到当前 `image-release` 历史中最新的 `image-release/*` 成功发布 tag，以其 commit 为基准执行：

```bash
bash scripts/ci/module-revisions.sh --base "$LAST_SUCCESSFUL_RELEASE_SHA" --write
```

脚本只管理 `.github/images.json` 中登记的 ANAS 派生镜像。它逐个 Module 比较登记的 build context；同一 Module 的任一 context 相对上次成功发布有变化，就从基准 manifest 的 revision 加一。多个 context 同时变化仍只增加一次，不影响其他 Module。上游 `version` 变化或新增 Module 时使用 `1`。`--write` 同时更新 `module.yml`、存在时的 `localization.yml`，以及该 Module 在 `docker-compose.yml` 中的全部派生镜像标签。未经修改的上游 mirror 不使用 `-r<revision>` 镜像标签，因此不由此脚本计算。

生成内容由 `github-actions[bot]` 提交到 `image-release`，后续 Job 显式 checkout 该 commit；OCI `org.opencontainers.image.revision` 也记录这个 SHA。全部镜像在 GHCR 和 CNB 验证成功后，工作流创建 `image-release/<run>-<attempt>` tag。该 tag 只代表成功发布，是下次 revision 计算的稳定基准。构建失败不会创建 tag，也不会同步 `master`；重新运行或下一次合并会继续从上一个成功 tag 累计变化。

本地排查时仍可只读预览或显式写入，但这不是正常发版步骤：

```bash
bash scripts/ci/module-revisions.sh --base <成功发布SHA> --print
bash scripts/ci/module-revisions.sh --base <成功发布SHA> --write
```

## Registry

```text
ghcr.io/anas-project/anas-<软件>:<version>-r<revision>
docker.cnb.cool/anas.dev/anas/anas-<软件>:<version>-r<revision>
```

未经修改的上游镜像使用 `anas-mirror-<软件>:<固定版本>`。清单同时保存上游引用、上游 manifest digest 和运行平台；即使来源使用 `latest`，发布输入也必须带 digest，ANAS 目标 tag 不得使用 `latest`。

`ANAS_IMAGE_REGISTRY` 选择全部运行时镜像来源。`global.chinese_speedup: true` 默认切换到 CNB，因此部署服务器只需访问 `docker.cnb.cool`；`global.chinese_build_speedup: true` 才会为源码构建设置 `DOCKER_HUB_REGISTRY`、`GHCR_REGISTRY` 和包管理器镜像。

## CI 行为

- 普通 `master` push：不运行容器发布工作流。
- 目标为 `image-release` 的 Pull Request：自动计算候选 revision，只构建受影响镜像，不提交、不推送。
- `image-release` push：自动提交生成的 revision，只处理从上次成功发布以来 context 发生变化的 Module。
- 首次发布还没有成功 tag 时：处理全部派生镜像和 mirror；失败重试仍处理全部目标。
- 同一 Module 的任一登记 context 变化：该 Module revision 增加一次，并构建它登记的全部镜像。
- Mirror：校验上游 digest，筛选 `linux/amd64`、`linux/arm64` 运行 manifest，原样发布到 `anas-mirror-*`，不重新构建上游软件。
- Registry 间使用固定版本、校验过下载包的 Crane 逐平台复制；对 CNB 禁用 HTTP/2，并按平台重试，已存在的 blob 不重复上传。
- 两个 Registry 都没有固定标签时：构建一次并发布 GHCR，再复制运行平台 manifest 到 CNB。
- 只有一侧存在时：补齐另一侧。
- 两侧都存在时：验证后结束，不覆盖已有标签。
- 构建目标为 `linux/amd64` 和 `linux/arm64`，GHCR 保留 provenance 与 SBOM。
- 构建期间 `image-release` 又有新提交时：当前成功 commit 仍创建发布 tag，但不回退分支；排队的下一轮从该 tag 继续计算并负责同步 `master`。
- `master` 独立前进而不能快进时：停止同步，必须先把最新 `master` 合并进 `image-release`，不会强推或覆盖历史。

修改 Dockerfile 时同时检查：

1. Dockerfile 在 `.github/images.json` 中恰好登记一次；
2. Compose 镜像名和标签与 `version`、`revision` 一致；
3. 同版本的 `revision` 正确递增，或新版本重置为 `1`；
4. 所有 Compose 上游镜像都在 `.github/mirrors.json` 中锁定 digest，并使用 `anas-mirror-*`；
5. `go test ./...` 和相关 Module 测试通过。

## 首次发布与恢复

从当前 `master` 创建一次 `image-release` 分支并推送：

```bash
git fetch origin
git switch -c image-release origin/master
git push origin image-release
```

第一次可在 GitHub Actions 中从 `image-release` ref 手工运行 `Container images`，选择 `module=all`；也可以把后续 `master` 通过 PR 合并进该分支触发。首次成功会发布全部派生镜像和上游 mirror，并创建第一个成功 tag。以后只需把待发布的 `master` 合并到 `image-release`，revision、提交、构建、tag 和成功后的 `master` 同步均自动完成。

手工 `workflow_dispatch` 必须选择 `image-release` ref。`module=all` 可完成失败发布并创建成功 tag、同步 `master`；选择单个 Module 只用于补发或 Registry 恢复，不会推进全局成功 tag，也不会同步 `master`。

CNB 的 `master` 页面还提供“从 GHCR 同步全部 Cask 镜像”按钮。该入口由 `.cnb/web_trigger.yml` 和 `.cnb.yml` 定义，调用 `scripts/ci/cnb-container-images.sh mirror-all`，只把 GHCR 中已有、CNB 中缺失的固定版本镜像补到 CNB；它不会构建镜像，也不会覆盖 CNB 已有 tag。

工作流需要：

- Repository Actions 的 workflow permission 允许读写仓库；
- `image-release` 禁止 force push；[ruleset bypass](https://docs.github.com/en/repositories/configuring-branches-and-merges-in-your-repository/managing-rulesets/creating-rulesets-for-a-repository#granting-bypass-permissions-for-your-branch-or-tag-ruleset) 允许 GitHub Actions App 提交自动 revision；
- `master` 分支规则允许 GitHub Actions App 执行安全的 fast-forward；如果仓库规则无法授予该 App bypass，需要把 checkout/push token 换成具备同等权限的专用 GitHub App installation token；工作流从不 force push；
- GitHub 自动提供的 `GITHUB_TOKEN` 写入 GHCR；
- `CNB_REGISTRY_TOKEN` 写入 CNB Registry；
- `CNB_TOKEN` 供成功发布后把 `image-release`、`master` 和成功 tag 同步到 CNB Git 仓库。
