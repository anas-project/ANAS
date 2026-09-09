# 项目审查整改记录

日期：2026-09-09。依据：[9 月 5 日审查建议](2026-09-05-agent-configuration-and-project-quality-review.md)。
本次在已有未提交修改的工作树上实施，保留其他任务的文档、需求和 AI Module 改动；未提交或合并 Git。
本记录列明已完成子集，不代表原审查四批工作或 Compose/ALM 全部完成。

## 已完成修改

| 原审查项 | 本次落实与证据 |
| --- | --- |
| Agent 规则 | 采用精简共享 `AGENTS.md`，GPT-6 与 5.6 共用；明确直接实施、评审不自动改代码、保留无关修改、reviews 归属及验证停止条件。没有删除项目事实，也没有建立两套模型规则 |
| D1/D2/D3/D5 | 升级测试数字在实施前已修正；补计划索引章节/路径/归档一致性检查及负例，修正应用层迁移的索引位置、Incus 过时摘要、目录职责描述 |
| D4/D6 | Vikunja 按既有 M1—M4 验收记录归档，保留历史证据限制；条件/无序依赖的真实宿主验收仍未完成，修正里程碑；9 月 3 日审计追加勘误，不覆盖历史结论 |
| C1 | 容器恢复函数返回错误；daemon 加锁恢复失败时返回，取消任务仅在恢复成功后确认补偿。失败保存安全 `compensation_checked` 事件、保留阻塞标记；恢复与终态保存分别使用超时预算 |
| C2 的具体不一致风险 | Compose 探测时保存进程选择环境，拒绝部署变量覆盖 Docker selectors；owner guard 与 Compose 使用同一份选择。CLI 与 daemon fake Docker 测试记录探测/检查/变更并比较 endpoint；默认 context 不硬编码为系统 socket |
| C3 | 常规 PR/master CI 新增 web job，执行安装、临时生成 API 类型一致性比较、类型检查、Vitest、主/应急界面构建。生成检查不修改真实文件，不依赖整个工作树干净 |
| C4 / INCUS-R-038 | 新增共享构建检查：路径存在、Dockerfile COPY、Compose additional_contexts、Module revision 触发路径。实际补齐 Incus 的共享上下文以及 AI Agent/Forgejo/Incus 的 computeclient revision 路径；缺路径/漏 COPY/漏 revision/错 context 负例均拒绝 |
| C5 的错误边界 | `CommandFailure` 提供 phase/project/exit code/截断标志；普通 stderr 留 4096 字节尾部并过滤终端控制符，quiet 和 daemon 变更不捕获文本。激活失败先持久化 primary，再分别记录候选停止与旧部署恢复结果；恢复回调测试确认首因已落盘且不被双重恢复失败覆盖 |
| C6 | 新增 lego Provider/TLS 输出/敏感值边界测试；deploymentaudit 测试覆盖值隔离、拒绝不提交任务/不消耗幂等键；确认根目录 actions-controller 已有精确忽略规则 |

实现与需求对应：[QUALITY 需求](../requirements/project-quality-hardening.md)、[本批完成计划](../plans/archived/project-quality-hardening.md)。
命令契约、排障、测试入口和 Incus 技术文档已同步中英文。

## 验证结果

- Go 全量包测试首次有两个包因沙箱拒绝 httptest 监听本地端口失败，其余包通过；允许本地监听后，
  `modules/ai_agent/orchestrator` 和 `modules/incus/provisioner` 重跑通过。新增后续回归测试也通过。
  未将沙箱失败当作代码通过，亦未改测试绕过监听要求。
- `go vet ./...`、共享构建检查和负例测试通过。
- 前端 API 一致性、类型检查通过；18 个测试文件、72 项测试通过；主界面和应急界面构建通过。
- 文档状态测试、文档状态校验、需求归属与用例目录校验、需求/计划状态生成一致性检查通过；
  Module/Contract 文档生成器 `--check` 通过。
- VitePress 构建通过，存在超过 500 kB 的 chunk 提示；这是现有构建警告，不作为失败。
- 未运行远端 GitHub Actions、真实 Docker/Incus/IAM/Forgejo 宿主验收、模型对比运行或漏洞扫描。

## 尚未完成的原建议

1. [Compose 执行边界](../plans/compose-execution-boundary.md)仍为部分实施：顶层解析一次并传到所有辅助
   Docker 查询、context 配置文件冻结、端点公告、非 Unix socket mount 前置错误尚未实现；信号、真实
   进程中断和多重失败端到端脚本、逐阶段恢复 progress 仍待补齐。环境选择快照不等于完整 endpoint 生命周期。
2. [应用层迁移](../plans/application-layer-migration.md)尚未开始。本次只抽出端点选择和恢复结果边界，
   没有宣称已消除 restrictedProcessEnvironment 分支或切断 anasd→runner 依赖。
3. 真实 Incus/Forgejo、Adminer/IAM 验收继续按原计划执行；需要相应宿主和服务。Vikunja 归档沿用既有
   记录，不代表本次重新完成真机验收，也不自动更改 Module 发布状态。
4. 两代模型的代表任务对比尚未执行；当前只完成规则审阅。前端页面/会话结构提取仍按实际重复和浏览器
   用例证据渐进推进；没有引入新的状态库或扩大到 AI 编排提案全量实现。

下一步：继续 Compose 剩余子阶段，再按 ALM 测绘、依赖抽取、构造器注入的顺序迁移；外部验收单独登记真实证据。
