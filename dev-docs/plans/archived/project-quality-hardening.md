---
doc_type: plan
status: done
created: 2026-09-08
updated: 2026-09-09
---

# 项目审查整改实施计划

验收见[需求矩阵](../../requirements/project-quality-hardening.md)，范围来自用户确认的
[审查建议](../../reviews/2026-09-05-agent-configuration-and-project-quality-review.md)。
当前里程碑：M0—M2 已完成；本计划 6 项要求已落地。Compose 的剩余子阶段和 ALM 迁移分别沿用原计划。

| 里程碑 | 需求 ID | 状态 |
| --- | --- | --- |
| M0：规则与文档一致性 | R-001、R-002、R-005 | 已完成 |
| M1：补偿失败闭环 | R-003 | 已完成 |
| M2：前端 CI 与行为覆盖 | R-004、R-006 | 已完成 |

- [x] 复核 2026-09-08 工作树；升级测试数字已修正，AI 提案已移入 Module 私有目录。
- [x] 应用共享 AGENTS 改稿，明确 reviews 归属。
- [x] 修正文档状态和索引语义校验。
- [x] 返回恢复失败，保留补偿标记并保存安全事件。
- [x] 前端 PR/master CI、生成一致性检查。
- [x] 共享镜像构建校验，回写 Incus R-038。
- [x] lego / deploymentaudit 行为测试和构建产物忽略。
- [x] 抽出 Compose 选择环境与恢复结果边界，回写原计划的部分完成状态；未宣称 ALM 服务迁移完成。

## 验证命令

```bash
go test ./... && go vet ./...
npm --prefix web run check:api && npm --prefix web run typecheck && npm --prefix web test
npm run docs:test-status && npm run docs:check-status
npm run docs:check-requirements && npm run docs:check-requirement-status && npm run docs:check-plan-status
npm run docs:build
```

## 阻塞与后续

真实 Incus/Forgejo 宿主验收沿用原计划的外部资源条件；模型对比运行需要可用的两个运行配置。
静态检查和本地行为测试不能代替这些验收。尚未请求 git 提交，不提交或合并。

## 本批验收记录（2026-09-09）

共享构建与故障注入测试、Go 包测试、go vet、前端类型/API 检查及 72 项测试、两套界面构建通过。
全量 Go 首次因沙箱禁止本地监听导致两个包失败；同一代码在允许本地监听后两包重跑通过。
文档门禁和构建记录见[整改报告](../../reviews/2026-09-09-project-quality-hardening-implementation.md)。
