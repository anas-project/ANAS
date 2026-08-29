---
doc_type: plan
status: proposed
created: 2026-08-28
updated: 2026-08-28
---

# Changelog 实施计划

验收依据是[Changelog 要求](../requirements/changelog.md)的需求矩阵；写作规则与机制细节见[Changelog 规范](../../docs/developer/changelog-standard.md)。

本文只记录**落地顺序、里程碑与剩余工作**，不复述要求原文。当前入口是 M0：不做完 M0 的两处排除，后面每个里程碑都会自锁。

## 1. 里程碑

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：前置排除与文件骨架 | CHLOG-R-001—R-007、R-021—R-022 | 未开始 |
| M1：写入时机与覆盖检查 | CHLOG-R-008—R-013、R-033 | 未开始 |
| M2：发布改名与 master 回推 | CHLOG-R-014—R-020、R-023 | 未开始 |
| M3：文档站与 Release 正文 | CHLOG-R-024—R-032 | 未开始 |

## 2. M0 检查表：前置排除与文件骨架

先做排除，再建文件。顺序反了会在建骨架的那一刻就触发全部 catalog Module 的 revision 提升。

- [ ] `scripts/ci/module-revisions.sh` 的 `runtime_path()` 排除 `modules/*/changelog/`，并在 `module-revisions-test.sh` 中补用例。
- [ ] `internal/modulepackage` 的 `excludedRuntimeDirectory()` 同时排除 `docs` 与 `changelog` 两个目录名，补 `package_test.go` 用例断言变更记录不进制品、不进 digest。
- [ ] 用上述两项验证：只修改某 Module 的 `changelog/master.md` 时，`module-revisions.sh --print` 对该 Module 输出 `unchanged`。
- [ ] 建立 `docs/changelog/master.md` 与 `master.en.md`，内容只有 `# Unreleased`。
- [ ] 为 `.github/modules.json` 中全部 Module 建立 `modules/<name>/changelog/master.md` 与 `master.en.md`。
- [ ] 在[Changelog 规范](../../docs/developer/changelog-standard.md)中固化分节顺序与条目写法。

## 3. M1 检查表：写入时机与覆盖检查

- [ ] 实现覆盖检查脚本：对区间内每个 merge 提交，用 `git diff <first-parent>...<second-parent>` 取该分支引入的路径，判定是否需要条目。
- [ ] 受影响集合直接取 `scripts/ci/module-revisions.sh --print` 的输出，不另写路径规则。
- [ ] 实现三条豁免：只命中 `shared_contexts`/打包器/catalog 的分支归类为重新打包；只改测试、格式、CI、生成产物的分支沿用 `runtime_path()` 忽略规则；第二父已是 `master` 祖先的同步性 merge 跳过。
- [ ] 把覆盖检查挂进 `docs.yml` 已有的 “Validate documentation sources” 步骤，不新建 workflow。
- [ ] 用一次真实 dependabot 分支验证：只改 `go.mod` 的合并不要求任何条目。
- [ ] 把写入时机、落点与合并时的责任写入 `AGENTS.md`、`CONTRIBUTING.md` 与 `docs/developer/release.md`（中英文）。

## 4. M2 检查表：发布改名与 master 回推

- [ ] 在 `container-images.yml` 的 `prepare` 阶段，于 revision 计算之后、既有 `git add` 之前，对本批将获得新 tag 的 Module 执行改名并重建空 `master.md`。
- [ ] 在 `anas-release.yml` 的 `validate` 之后、`publish` 之前，对 Core 执行同样的改名与重建。
- [ ] 为 `anas-release.yml` 新增推回 `master` 的步骤，照抄 `container-images.yml` `sync_master` 的两级祖先校验。
- [ ] 实现幂等自愈：发布运行开始时比对 `master` 上的 `master.md` 与已发布版本文件，发现重复即先补上次丢失的改名。
- [ ] 实现重复发布检测与完整性检查，任一失败中止发布且不打 tag。
- [ ] 验证改名提交包含在 tag 指向的提交里：`git show <tag>:docs/changelog/<version>.md` 可取到正文。

## 5. M3 检查表：文档站与 Release 正文

- [ ] Core 变更记录页面由发布模式下 `git archive <coreTag> -- docs` 抽取的 `docs/` 提供；确认工作树中未发布内容不出现在正式站点。
- [ ] Module 变更记录页面通过 `git show <module-tag>:modules/<name>/changelog/...` 逐 tag 读取，与 Core tag 解耦。
- [ ] 同一上游版本的不同 revision 各自保留段落，不复用 `materialize-module-docs` 的文档去重结果。
- [ ] 为全部注册 Module 无条件生成页面，无条目时写占位；`renderVersionNavigation` 的“变更记录”链接指向 Module 根而不是 `pageBase`。
- [ ] 转义条目正文中的 Vue 插值；文档 CI 增加构建日志检查，不只看退出码。
- [ ] Core GitHub Release 改用 `--notes-file` 读取英文版本文件；`.cnb.yml` 的 `description` 改读中文版本文件。
- [ ] 中英文侧边栏加入 Core 变更记录入口。

## 6. 当前阻塞

没有外部阻塞。M0 的两处排除是其余全部工作的前提，先做这一项。

## 7. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-015 | 待新增 `test-env/scripts/changelog-release-rename-e2e.sh` | 模拟 image-release 与 anas-release 各一次发布 | — | 待实现 |
| R-016 | `changelog-release-rename-e2e.sh tag-contains-rename` | 打 tag 后校验 `git show <tag>:<path>` | — | 待实现 |
| R-017 | `changelog-release-rename-e2e.sh core-sync-master` | Core 发布后校验改名到达 master | — | 待实现 |
| R-018 | `changelog-release-rename-e2e.sh self-heal` | 人为丢弃一次 master 同步后重跑发布 | — | 待实现 |
| R-020 | `changelog-release-rename-e2e.sh missing-entry` | 缺条目的组件必须中止发布 | — | 待实现 |
| R-029 | `changelog-release-rename-e2e.sh github-notes` | GitHub Release 正文来自英文版本文件 | — | 待实现 |
| R-030 | `changelog-release-rename-e2e.sh cnb-notes` | CNB Release 正文来自中文版本文件 | — | 待实现 |
