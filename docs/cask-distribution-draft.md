# cask 独立分发（草案）

> **状态：草案，未排期。** 本文只记录问题、约束和候选方案，不构成实施计划。
> 它与 [workspace-backup-plan.md](workspace-backup-plan.md) 的一到四期没有依赖关系，
> 可以在其之后任意时点推进。

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
2. 本地缓存放哪里——workspace 内（每个 workspace 一份，浪费但自足）还是用户级
   （`~/.cache/anas/casks`，共享但破坏 workspace 自足性）？
3. hook 预编译若不做，`freezeHookBinary` 冻结的 `.hook.bin` 仍占制品 97% 体积，
   "cask 分离能让制品变小"这个预期不成立。需要先确认目标到底是体积还是可寻址性。
4. 制品签名与信任模型——digest 能防篡改，但谁来背书 digest 本身？
