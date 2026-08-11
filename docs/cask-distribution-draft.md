# cask 独立分发（草案）

> **状态：草案，未排期。** 本文只记录问题、约束和候选方案，不构成实施计划。
> 它与 [workspace-backup-plan.md](workspace-backup-plan.md) 的一到四期没有依赖关系，
> 可以在其之后任意时点推进。

> **已实施子集（2026-08-11）：** cask 容器镜像的版本规则、GHCR 发布清单和 GitHub
> Actions 已落地；cask bundle 本身的 OCI 分发仍是草案。

## 现状

cask **没有** embed 进二进制。运行时按以下顺序在磁盘上查找 `casks/mods`
（[manifest.go:184](../internal/runner/manifest.go)）：

```
--cask-root  →  ANAS_CASK_ROOT  →  $CWD/casks/mods  →  可执行文件旁的 casks/mods
             →  <可执行文件目录>/casks/mods  →  其父目录
```

lock 中每个 cask 记录 `source: bundle:<name>` 与 `digest`，digest 由
`caskBundleDigest(mod.SourceDir)`（[versions.go:234](../internal/runner/versions.go)）
对源目录树计算。

hook 的处理是这里最关键的一环：

- 声明形式是 `command: [go, run, ./hook]`，16 个 cask 中有 hook 目录
- `ensureHookBinary`（[hook.go:122](../internal/runner/hook.go)）**优先查找**
  `<cask>/hook/bin/<GOOS>-<GOARCH>/anas-hook`，存在即直接使用；不存在才
  `go build`。**这条预编译加载路径已经实现，但今天没有任何 cask 附带它**
- `freezeHookBinary`（[hook.go:167](../internal/runner/hook.go)）把编译产物拷进
  `deployments/<id>/casks/<name>/.hook.bin`，**并删除该 cask 的 `hook/` 源码目录**，
  目的是让 release 无需 Go 工具链即可运行

## 问题

1. **cask 源树在 workspace 之外，且无版本身份。** finance 上是
   `/home/whl/anas-src/`，一份 rsync 副本、非 git 检出——"服务器上跑的是哪个 commit"
   现在答不出来。lock 里有 digest 可校验，但没有任何东西能告诉你这个 digest 对应
   哪个上游版本、去哪里再取一份。
2. **从 config + lock 重建 deployment 需要匹配 digest 的 cask 源，而它不可寻址。**
   digest 只能验证手上这份对不对，不能用来获取。
3. **制品体积几乎全是 hook 二进制。** finance 实测单个 deployment 42M，其中 41M 是
   13 个 `.hook.bin`。真正的配置和 build context 约 1M。

## 目标形态

cask 成为**独立发布、digest 寻址、可获取**的制品：

```yaml
# config.lock.yml
casks:
  nextcloud:
    version: 30.0.1
    source: <scheme>://…/nextcloud@sha256:117617…
    digest: sha256:117617…
```

`anas` 在本地缓存中按 digest 查找，缺失时按 `source` 获取并校验 digest 后使用。

## 关键约束

**cask 本体是架构无关的。** 它是 YAML + Dockerfile + Docker build context + hook
的 Go 源码，没有任何部分需要按 x64 / arm64 / macOS-arm64 分版本。

需要分平台的只有两样：

| 产物 | 是否分平台 | 说明 |
| --- | --- | --- |
| `anas` 二进制 | ✅ | 常规交叉编译发布 |
| hook 二进制 | ✅ **可选** | 附带则填进 `hook/bin/<GOOS>-<GOARCH>/anas-hook`，加载路径已就绪 |
| cask 其余内容 | ❌ | 架构无关 |

hook 预编译是**可选优化**，不是分发的前提。两种选择：

- **保持源码分发、本地编译**（现状）：cask 制品小，但恢复目标机需要 Go 工具链。
  注意 `freezeHookBinary` 已经让 *deployment* 不依赖工具链，只有首次 render 需要
- **附带预编译二进制**：每个有 hook 的 cask 多出 N 个平台 × 约 3M 的产物，并引入
  交叉编译流水线；换来 render 阶段也不依赖 Go

倾向前者，理由是后者的收益仅覆盖"首次 render"这一个场景，而代价是每个 cask 的产物
数量乘以平台数。

## 仓库布局：monorepo

**已决定：所有 first-party cask 留在本仓库，每个 cask 独立版本、独立发布制品。**
（本节是全文唯一已定的部分——它不依赖分发是否排期，即便本草案永不推进，仓库布局
也按此维持。）

前提是把两件事解耦：**仓库布局 ≠ 分发粒度**。制品按 digest 寻址，lock 里记的是
`source: <scheme>://…/nextcloud@sha256:…`，消费者看不到它从哪个 git 仓库构建。
"独立版本、独立更新"和"同一个仓库"没有冲突——Homebrew tap、nixpkgs、helm charts
都是这个形态。

选 monorepo 而不是每 cask 一仓的理由：

1. **跨 cask 的契约变更需要原子提交。** IAM capability binding 这次改动同时动了
   authentik / llng / nextcloud 的 hook、runner 和文档。分仓后这是四个 PR 加一套
   合并顺序约定。这些 cask 之间耦合度很高（`dependencies.requires`、IAM
   provider/consumer、traefik 路由），它们不是互相独立的插件。
2. **横切测试没有地方放。** `cask_manifest_test.go` 已经在遍历所有 cask 校验 ABI
   一致性，分仓后这个测试无家可归。
3. **单人维护 17 个仓库的 issue / CI / release 是纯开销。**

第三方 cask 不构成分仓理由——第三方本来就在自己的仓库里，跟 first-party 怎么放无关。

### 版本身份与发布触发

容器发布采用“规范化上游版本 + ANAS 修订号”。同一上游版本下修改 Dockerfile、入口
脚本或其他 build context 文件时，只增加 `revision`；上游升级时更新 `version` 和
`app_version`，并把 `revision` 重置为 `1`。

```yaml
version: 34.0.2
app_version: 34.0.2
revision: 2
```

- **发布身份与镜像 tag**：`34.0.2-r2`。`-r2` 不作为 SemVer 预发布后缀参与比较；
  runner 先比较 `version`，相同时再比较整数 `revision`
- **git tag**：`nextcloud/v34.0.2-r2`
- **首次发布**：手工执行 GitHub Actions 的 `workflow_dispatch`，参数使用 `all`
- **后续发布触发**：只在 `.github/images.json` 登记的 build context 发生变化时选择
  对应 cask；PR 构建验证，合并主分支后发布
- **版本校验**：上游版本不变时 revision 必须恰好加一；上游版本变化时必须重置为 1；
  已存在的 GHCR tag 不允许覆盖
- **多镜像 cask**：`samba_dc` 任一 context 变化时，两个镜像按同一版本成组发布
- **`data_breaking` 布尔化遍历的是修订序列**，不是应用版本序列

容器清单位于 `.github/images.json`。CI 同时扫描仓库中的全部 Dockerfile，未登记或重复
登记都会失败。镜像发布到 `ghcr.io/anas-project/anas-<image>:<version>-r<revision>`，不发布
`latest`。默认平台为 `linux/amd64` 和 `linux/arm64`。第一次成功推送后，需要在 GitHub
Packages 中把这些 container package 设为 public，部署端才能匿名拉取。

⚠️ **hook 共享库会打破路径过滤。** 若把 16 份重复的 hook 协议样板抽成
`casks/lib/hook` 并留在本仓库，该目录一变就要重建全部 cask 制品。这是 monorepo 加
同仓 SDK 的真实成本，也是"cask 分发落地时 SDK 应拆成独立 module"的主要论据——
cask bundle 源码分发时，`import` 一个独立小 module 才能让编译闭包保持最小，否则
编译一个 3MB 的 bundle 要拉整个 anas 仓库。

### 索引

**不单独造索引文件，registry 的 tag list 就是索引。**

- `GET /v2/<name>/tags/list` 是标准 API；tag 先拆出 `-rN`，再用已有的
  `Masterminds/semver` 排序版本并按整数排序 revision
- 单独的 `index.yaml` 会引入新的失败模式：索引里有的制品不在、制品在的索引里没有。
  让 registry 当唯一真相源就没有这个不一致
- 本地缓存（见「开放问题」2）只是缓存，可随时按 registry 重建

## 候选分发形式

| 形式 | 优点 | 缺点 |
| --- | --- | --- |
| OCI artifact（registry） | digest 寻址天然契合；用户已经在跑 docker，无新依赖；可用现有 registry | 需要 registry 可达；离线场景要预先 pull |
| tar.gz + 索引文件（HTTP） | 实现最简单；可放任意静态托管 | 需自己做索引、签名、缓存目录管理 |
| git tag / submodule | 版本身份最清晰 | 获取粒度粗（整仓），digest 与 git 对象模型对不齐 |

倾向 OCI artifact，但需要确认 `caskBundleDigest` 的计算方式能否与 OCI descriptor
digest 统一，否则 lock 里会出现两个含义不同的 digest。

## 影响面

- `locateCaskRoot` 从"在磁盘上猜路径"变成"在缓存中按 digest 取"，`--cask-root` /
  `ANAS_CASK_ROOT` 保留为本地开发的覆盖手段
- `source: bundle:<name>` 的写法要变，`caskLockRecord` 结构随之调整
- 快照内 `deployment/` 副本会从 42M 降到约 1M（若 hook 不预编译，则冻结的 `.hook.bin`
  仍在制品里，体积不变——**这一点要想清楚再动**）

## 明确不改变的

**deployment 制品仍然进备份和快照。** cask 可获取之后，理论上可以只备份 config +
lock 再重建，但那让恢复依赖"网络通 + 制品仓库仍然存在"。对自托管工具这个假设太重，
而制品只占数据体积的 3%。见 [contracts/backup.md](contracts/backup.md) 的
「备份单元就是快照」。

## 顺带解锁的一件事：`data_breaking` 可简化为布尔

[contracts/snapshot.md](contracts/snapshot.md) 要求 cask 用**列表**声明数据格式断代
版本，因为 runner 手中只有目标版本 `B` 的 `cask.yml`，拿不到 `A..B` 之间任何中间版本。

cask 可按 digest 寻址之后，runner 能取到区间内每个版本的 `cask.yml`，于是每个版本只
需声明一个布尔"我相对上一版是否改写了数据格式"，由 runner 遍历判定。列表连同它的修剪
规则（随 `upgrade.from` 同步删除死条目）都可以取消。

这不是推进 cask 分发的理由，只是落地后可以顺手做的简化。

## 开放问题

1. `caskBundleDigest` 与所选分发形式的 digest 能否统一为一个值？
   **倾向不统一。** 两者语义本就不同：`caskBundleDigest` 是"解包后源目录树的内容
   指纹"，OCI descriptor digest 是"压缩制品的指纹"。lock 保留两个字段——`source`
   内的 digest 用于**获取**，`digest` 用于**校验解包后的内容**。强行统一要求 tar
   完全可复现（时间戳、条目顺序、权限全部规范化），能做但很脆，收益只是少一个字段。
2. 本地缓存放哪里——workspace 内（每个 workspace 一份，浪费但自足）还是用户级
   （`~/.cache/anas/casks`，共享但破坏 workspace 自足性）？
   **倾向用户级。** 它是纯缓存、可按 registry 重建，不破坏 workspace 自足性——
   自足性由 deployment 制品保证（见「明确不改变的」）。缓存丢失只影响首次 render，
   不影响恢复。
3. hook 预编译若不做，`freezeHookBinary` 冻结的 `.hook.bin` 仍占制品 97% 体积，
   "cask 分离能让制品变小"这个预期不成立。需要先确认目标到底是体积还是可寻址性。
4. 制品签名与信任模型——digest 能防篡改，但谁来背书 digest 本身？
