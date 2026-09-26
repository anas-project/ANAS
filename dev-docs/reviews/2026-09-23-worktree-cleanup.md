---
doc_type: review
status: current
created: 2026-09-23
updated: 2026-09-23
---

# Worktree 清理核对

本次核对基于本地 `master`（`0b6144887d6c1ecc89847be6d2773f139e8b7f23`）。
4 个附属 worktree 的 HEAD 均是 master 的祖先；没有独有提交、暂存改动、
未暂存改动或未忽略的新文件。无需再合并代码，均可移除工作目录。

| Worktree | HEAD | 相对 master 落后 / 独有提交 | 处理依据 |
| --- | --- | --- | --- |
| `~/.codex/worktrees/5135/anas` | `3abe464` | 36 / 0 | Incus 集成已由 `49bbf45` 合并 |
| `.claude/worktrees/kind-cerf-a6b5eb` | `49bbf45`（detached） | 8 / 0 | 已位于 master 历史中 |
| `.claude/worktrees/optimistic-fermat-03e91f` | `54c3a90`（detached） | 19 / 0 | 计划文档提交已由 `658fe40` 合并 |
| `.claude/worktrees/quizzical-khorana-840a68` | `259aa5d` | 5 / 0 | 目录身份键盘点已位于 master 历史中 |

## 清理边界

- 已移除以上 4 个 worktree；保留所有本地分支引用和提交历史。
- 清理前 `du -sh` 分别为 286M、188M、146M、146M，合计约 766 MiB。
- 忽略内容为依赖目录、文档构建产物、Python 缓存，以及三个 Claude 本地设置文件。
  本地设置已逐字节备份到被 Git 忽略的
  `docs/private/worktree-cleanup/2026-09-23/<worktree>/settings.local.json`。
- 主工作目录已有 42 个修改或未跟踪文件，其内容 SHA-256 已记录在同一备份目录的
  `master-files-before.json`；清理不提交、暂存或改写这些文件。
- 本地 master 比本地记录的 `origin/master` 领先 7 个提交；本次未 fetch 或 push，
  不把本地已合并等同于远端已发布。

## 验证

- `git worktree list --porcelain`、各 worktree 的状态、差异与独有提交已核对。
- 对全部 4 个 HEAD 执行 `git merge-base --is-ancestor <HEAD> master`，均成功。
- `git worktree prune --dry-run --verbose` 未发现可清除的失效注册。
- 当前可见进程的 cwd 扫描未发现位于这些附属目录的进程；不代表没有其他编辑器会话。
- 清理后 `git worktree list --porcelain` 仅列出主 worktree，4 个附属目录均已不存在；
  prune dry-run 无输出。
- 清理前后全部本地分支名与完整提交 SHA 一致，master HEAD 未变；主目录原有
  42 个文件的 SHA-256 全部一致，Git 状态仅新增本报告。`git diff --check` 通过。
- 本次只涉及 Git 工作目录管理及本报告，不涉及产品行为；未运行应用测试或实机验收。
