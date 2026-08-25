---
doc_type: plan
status: proposed
created: 2026-08-25
updated: 2026-08-25
---

# 文档驱动测试自动化实施计划

验收依据是[文档驱动测试生成与远程执行要求](/requirements/document-driven-test-automation)的需求矩阵。
本主题没有独立架构文档；生成链、SSH 信任边界、隔离生命周期和报告契约已经由要求文档规定。
当前下一里程碑是 M0。

## 1. 需求归属与状态

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：用例模型、生成文档与双向溯源门禁 | R-001—R-008 | 未开始 |
| M1：Agent 完整测试生成与独立有效性门禁 | R-009—R-014 | 未开始 |
| M2：SSH 目标、源包和服务器隔离预检 | R-015—R-022 | 未开始 |
| M3：一键远程运行、恢复、清理与报告 | R-023—R-031 | 未开始 |
| M4：CI 手动触发与受控证据保留 | R-032 | 未开始 |

## 2. M0 检查表

- [ ] 定义 `test-env/cases/` 下的机器可读用例 schema、稳定用例 ID 和废弃规则。
- [ ] 从用例清单生成可阅读的 Markdown catalog，生成文件标明来源且禁止手改。
- [ ] 扩展需求门禁，校验需求、用例、实现文件和可发现执行命令的双向关系。
- [ ] 为需求文本摘要增加待复核状态，区分无语义措辞变化与需要重新验收的变化。
- [ ] 先迁移一个纯单元主题和一个服务器 e2e 主题，验证 schema 不只适配单一测试框架。
- [ ] 将生成器单元测试、catalog `--check` 和覆盖门禁接入文档 CI。

## 3. M1 检查表

- [ ] 把“需求差异 -> 用例差异 -> 完整测试代码差异”定义为 Agent 默认工作流。
- [ ] Agent 允许生成完整 Go、Shell、Python 和 Playwright 测试，不设置只生成脚手架的限制。
- [ ] 生成或更新已有测试时保留人工修改，以补丁形式展示需求、用例和断言变化。
- [ ] 对拒绝、安全、回滚、故障降级和恢复需求生成反例或故障注入用例。
- [ ] 在可行的包中引入变异/行为破坏验证；不可行时要求记录人工复核理由。
- [ ] 增加反模式门禁或审阅清单：仅检查退出码、仅匹配日志、mock 复制实现逻辑等不能独立证明行为。

## 4. M2 检查表

- [ ] 定义 Git 忽略的本地 target profile 和可提交的无 Secret 示例文件。
- [ ] profile 只引用 SSH config alias；验证 known-host 绑定，不在参数中拼接密码、私钥路径或关闭校验。
- [ ] 在专用测试服务器安装非生产标记和固定动词的特权 helper，审计测试账号的 sudo/组权限。
- [ ] 支持 committed source 与“基准 commit + worktree patch”两种源包，并在两端校验内容摘要。
- [ ] 为每个 `run-id` 分配独立 workspace、端口、网络、containerd、Docker 和报告目录。
- [ ] 复用 `server-require-isolated-docker.sh` 与 Compose workspace owner guard，不另造较弱检查。
- [ ] 合并架构、容量、端口、路由/DNS、目标能力与并发配额 preflight；任何失败都停在部署前。

## 5. M3 检查表

- [ ] 提供统一运行器的 `plan`、`run`、`status`、`collect` 和 `cleanup` 生命周期。
- [ ] `run` 用一个非交互命令完成传输、部署、suite 执行、报告回收和按 `run-id` 清理。
- [ ] 支持按 suite/case 选择以及无副作用 dry-run，先覆盖现有 Vikunja 发布门禁。
- [ ] 服务器端保存阶段状态；本地 SSH 中断后能重新查询、收集或清理。
- [ ] 失败保留必须显式设置期限，并实现只按 `run-id` 的到期清理。
- [ ] 统一 Shell、Go、Python 和 Playwright 结果为 JSON/JUnit/Markdown，区分产品失败与基础设施失败。
- [ ] 复用现有脱敏 reporter，并增加源身份、需求/用例覆盖、环境指纹、部署摘要和清理状态。
- [ ] 提供从成功报告生成 e2e 执行记录候选项的命令，不把脚本登记自动视为已经执行。

目标用户入口暂定为下列形状，具体命令名在 M3 实现时以仓库 CLI 复用情况为准：

```bash
./test-env/bin/remote-test run \
  --target dedicated-test \
  --suite vikunja-release \
  --source worktree
```

该示例当前尚未实现，不能作为现有操作命令使用。

## 6. M4 检查表

- [ ] 增加手动触发的 CI workflow，复用 M3 运行器而不是在 YAML 复制远程流程。
- [ ] 对目标、suite、审批人、并发、超时和 artifact 保留设置显式白名单。
- [ ] CI 只从受控 Secret/SSH agent 获取认证，日志和 artifact 继续执行同一脱敏策略。
- [ ] PR 默认不自动占用稀缺远程服务器；发布门禁和人工 dispatch 按风险选择 suite。

## 7. e2e 执行记录

| 需求 ID | 脚本 | 环境 | 执行日期 | 结果 |
| --- | --- | --- | --- | --- |
| R-017 | 待新增远程运行器拒绝测试 | 未登记、缺标记与生产标记三类目标 | — | 待执行 |
| R-019 | 待新增源包传输 E2E | commit 与 worktree patch 两种来源 | — | 待执行 |
| R-020 | 待新增并发隔离 E2E | 同一专用服务器两个 `run-id` | — | 待执行 |
| R-021 | 复用并扩展 `test-server-docker-isolation.sh` | 错误 socket/data-root/workspace owner | — | 待执行 |
| R-023 | 待新增 `remote-test` 全流程 E2E | 专用非生产服务器 + Vikunja suite | — | 待执行 |
| R-025 | 待新增 SSH 中断恢复 E2E | 传输、部署、测试、收集各阶段断连 | — | 待执行 |
| R-026 | 待新增清理范围 E2E | 成功、失败保留、到期清理与相邻运行 | — | 待执行 |
| R-029 | 待新增远程 Secret 泄漏 E2E | Shell、Playwright、报告与源包扫描 | — | 待执行 |
| R-030 | 待新增总状态 E2E | 用例失败、跳过、报告损坏、远端未知 | — | 待执行 |

## 8. 验证命令

文档阶段使用：

```bash
npm run docs:test-requirements
npm run docs:check-requirements
npm run docs:requirement-status
npm run docs:check-requirement-status
npm run docs:build
```

M0 以后必须把新增用例 schema、生成器和溯源门禁的确定性测试加入这里；M3 以后再加入远程运行器的
本地契约测试和专用服务器 E2E 命令。

## 9. 当前阻塞

- 当前没有统一用例 schema、生成 catalog 或需求到测试实现的双向门禁。
- 已有服务器脚本的环境变量、部署准备和报告格式不完全一致，尚不能由一个入口可靠编排。
- 私有测试服务器已有 SSH 和隔离 Docker 资料，但还没有机器可读 target profile、非生产标记协议和
  受约束特权 helper；在完成权限审计前不能把现有 root 脚本直接暴露给自动运行器。
