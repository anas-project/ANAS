# 评审与历史快照

本目录保存针对明确日期、版本或提交基线的审查、库存和实施评估。文件名前缀日期表示评审基线，不是文档最后修改时间；后续只能补充解决状态或勘误，新的独立评审应创建新文件。

| 文档 | 基线 | 类型 |
| --- | --- | --- |
| [Incus 回复来源与双向连接清理接续核对](2026-09-20-incus-reply-origin.md) | 2026-09-20，`49bbf45` 加累积工作树 | 原始设备入口、双族原子许可、双向 conntrack 精确清理与原生测试源；生产与 native 验收未完成 |
| [Incus 设备绑定地址路由接续核对](2026-09-20-incus-address-routing.md) | 2026-09-20，`49bbf45` 加累积工作树 | 宿主独立路由表、永久邻居、分配回执、正常与失败撤销；前向候选，未运行原生或生产验收 |
| [Incus 入站实现恢复与继续核对](2026-09-20-incus-ingress-recovery.md) | 2026-09-20，`49bbf45` 加累积工作树 | 恢复未落盘文档、入站持久化与内核身份、后续实现和实际验证边界 |
| [Incus 中断恢复与集成回归](2026-09-19-incus-integration-recovery.md) | 2026-09-19，`49bbf45` 加累积工作树 | 参数/Store/确认交接、私有连接与网络、升级保护和界面回归；真实宿主及生产 ingress 未验收 |
| [宿主共享队列与可选 HTTP 接续核对](2026-09-19-host-action-queue-http.md) | 2026-09-19，`49bbf45` 加前轮暂存/未暂存修改 | 中断恢复、同 daemon 队列、HTTP 授权/合流/取消和非 root 配置读取；TLS/权限与安装迁移未完成 |
| [宿主动作退出证据与共享终态核对](2026-09-19-host-action-exit-completion.md) | 2026-09-19，`49bbf45` 加前轮暂存/未暂存修改 | 独立 systemd 退出观察、共享 recorder、同版本 root 程序与候选单元；未安装或通过原生验收 |
| [宿主 job 私有监听与分派核对](2026-09-19-host-job-broker-listener.md) | 2026-09-19，`49bbf45` 加前轮暂存/未暂存修改 | 固定私有 listener、有界共享任务分派、停机/退休租约与原生 CI 门禁；未启用生产服务 |
| [宿主 job 跨进程绑定接续核对](2026-09-19-host-job-broker-implementation.md) | 2026-09-19，`49bbf45` 加前轮工作树 | 固定 broker、双向内核身份、job/权限复核和进程租约保留；生产安装与原生验收未完成 |
| [宿主动作激活与共享 job 绑定核对](2026-09-19-host-action-activation-job-binding.md) | 2026-09-19，`49bbf45` 加此前已暂存修改 | 固定激活/安装身份、拒绝审计、共享执行租约及本机清单；生产 broker 未接入 |
| [Incus 宿主预检与动作边界接续核对](2026-09-19-incus-host-preflight-implementation.md) | 2026-09-19，`49bbf45` 加上一轮工作区修改 | 声明式发行版表、只读预检、peer/ABI 与审计原语；未安装生产通道 |
| [Incus 实现接续与测试核对](2026-09-18-incus-implementation-verification.md) | 2026-09-18，干净 `master` / `49bbf45` 后直接修改 | 配置/错误边界修复、发布侧 build-once、HTTP 事务回归与真实未完成范围 |
| [Incus 固定控制转发接续核对](2026-09-18-incus-control-relay-implementation.md) | 2026-09-18，`f7642c5` 加并行修改的未提交工作树 | 非 root 控制转发、执行租约保护与未验收边界；不是评审批准 |
| [Incus 本地镜像归档接续核对](2026-09-18-incus-artifact-archive-implementation.md) | 2026-09-18，未提交工作树 | 本地镜像归档、不可变 revision、恢复边界与未验收项；不是发布批准 |
| [Incus 发行版 daemon 供给探查](2026-09-11-incus-daemon-probe.md) | 2026-09-11 未提交工作树 / Incus 6.0.5 | 存储配额误报、修复边界与未通过项 |
| [Incus HTTP namespace 实验](2026-09-11-incus-http-netns-validation.md) | 2026-09-11 未提交工作树 / Ubuntu 26.04 | 真实 nft/HTTP 数据面检查与 Incus 验收边界 |
| [Incus 网络与 proxy 权限核验](2026-09-10-incus-network-proxy-validation.md) | 2026-09-10，ANAS `f7642c5` / Incus `v7.3.0` | 固定版本源码调用链、兼容性缺陷与实机验证边界 |
| [凭据轮换机制审查：全面轮换与单独轮换](2026-09-04-credential-rotation-review.md) | 2026-09-04 工作树，HEAD `5306b63` | 轮换覆盖面、作用域语义与 resource 凭据空白 |
| [GPT‑6 / GPT‑5.6 混用配置与项目质量评估](2026-09-05-agent-configuration-and-project-quality-review.md) | 2026-09-05 工作树，HEAD `455770b` | Agent 配置、文档、架构与代码质量审查 |
| [AI Agent 编排设计审查](../../modules/ai_agent/dev-docs/reviews/2026-09-05-orchestration-design-review.md) | 2026-09-05 工作树，HEAD `455770b` | 编排设计、凭据边界与事件状态审查（随组件迁至 `modules/ai_agent/`） |
| [ANAS 综合项目审计与整改状态（2026-09-03）](2026-09-03-comprehensive-project-audit.md) | 2026-09-03 工作树复核 | 综合审计、整改状态与未解决发现 |
| [ANAS 综合项目审计与整改状态（2026-08-23）](2026-08-23-comprehensive-project-audit.md) | 2026-08-23 工作树复核 | 综合审计、整改状态与未解决发现 |
| [Vikunja Module 设计规范符合性审查](2026-08-21-vikunja-module-design-compliance.md) | 2026-08-21 工作树，Vikunja `2.4.0-r1` | Module 规范与发布门禁审查 |
| [Module 分类与访问边界分析](2026-08-19-module-classification.md) | 2026-08-19 工作树 | 分类和实现差距审查 |
| [Module 官方镜像切换与版本升级评估](2026-07-29-module-image-upgrade.md) | 2026-07-29，后续实施更新至 2026-08-02 | 升级评估与实施记录 |
| [ANAS 设计问题审查报告](2026-07-19-design-review.md) | 2026-07-19 代码基线 | 代码与架构审查 |
