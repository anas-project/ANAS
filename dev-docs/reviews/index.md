# 评审与历史快照

本目录保存针对明确日期、版本或提交基线的审查、库存和实施评估。文件名前缀日期表示评审基线，不是文档最后修改时间；后续只能补充解决状态或勘误，新的独立评审应创建新文件。

| 文档 | 基线 | 类型 |
| --- | --- | --- |
| [Casdoor r10 发布验收](2026-10-06-casdoor-release-acceptance.md) | 2026-10-06，finance 独立 Docker/Btrfs | 正式双架构构建、OIDC/Nextcloud、撤权、轮换、恢复与生命周期通过，已标 release；首次建号延迟保留观察，SAML 应用退出/SLO 待实现 |
| [Nextcloud 官方 anchor UID 实机验收](2026-10-05-nextcloud-anchor-uid-acceptance.md) | 2026-10-05，官方 user_oidc 8.11.0、正式 amd64 镜像 | 官方原样插件、改名同 UID/原文件及会话撤权通过；不扩大为完整编辑验收 |
| [LLNG anchor sub 与 Nextcloud 隔离实机验收](2026-10-05-llng-anchor-sub-acceptance.md) | 2026-10-05，LLNG 2.23.2-r12 正式 amd64 镜像、finance 独立 Docker | 签名主体、refresh、注销、改名与标签回收通过；缺 anchor 反例与最终六项整体复验通过，原 Nextcloud Provider 已恢复；SAML、事件撤权待实现 |
| [Casdoor 发布就绪评估](2026-10-03-casdoor-release-readiness.md) | 2026-10-03—04，Casdoor `3.143.0-r10` | 初次评估；发布阻塞已由 2026-10-06 正式验收闭环，当前已标 release |
| [Incus 租约网络、HTTP 发布与端口绑定实施记录](2026-10-03-incus-lease-network-implementation.md) | 2026-10-03，`c7891162` 加当前未提交工作树 | M10a/M10b/M11/M11b/M11c、HOSTACT M5 与运行问题记录 CLI 的实现与本机门禁；撤销停止实例等五项自行决定；实机 e2e 全部未运行 |
| [租约网络实机探测](2026-09-30-incus-lease-network-probe.md) | 2026-09-30，`7ad1876a` 加累积工作树；一次性 Debian 13 VM、Incus 7.0.1、docker.io 26 与 Docker CE 29 | M10a/M11b/M11c 待验证项与控制连接启动顺序：ACL 与 address set、默认拒绝入站、端口隔离、槽位固定地址、Docker 式端口表与占位；静态规则改窄规则、UDP 占位改常驻持有；据此 Incus 改为直接监听控制网关 |
| [入站旧实现删除与宿主通道身份链简化](2026-09-30-incus-old-code-removal-and-hostd-simplification.md) | 2026-09-30，`7ad1876a` 加累积工作树 | 删除逐实例转发许可与逐发布 HTTP 入站运行时（225 个文件）；hostd 对端只认 root，调用记录取代回连 broker 与 systemd 退出观察（`HOSTACT-R-016`）；删除控制转发服务；服务安装必须带 `anas-helper`；宿主审批门禁实机结果见 §5 |
| [Incus 国内软件源实机核验](2026-09-27-incus-chinese-speedup-native.md) | 2026-09-26—28，逐轮冻结工作树与二进制摘要 | 三档宿主国内源后端、guest 烘焙与不可变配方检查、脚本断言修正及物理 Docker 基线；逐轮终态与边界见正文 |
| [Incus 设计简化评审](2026-09-28-incus-design-simplification-review.md) | 2026-09-28，`dddae0c2` 加累积工作树 | 出站逐实例许可、无消费者的入站、部署热路径协调、hostd 身份链与控制转发的简化路径；未改实现 |
| [Incus proxy NAT 通配监听实机探测](2026-09-29-incus-proxy-nat-wildcard-probe.md) | 2026-09-29，`7ad1876a` 加累积工作树；一次性 Debian 13 VM、Incus 7.0.1 | 通配监听的 DNAT 不匹配目的地址，宿主外连与转发流量同端口被劫持；具体地址与 Docker 式 `fib daddr type local` 对照；诊断轮，未改实现 |
| [compute 租约入站简化草案](2026-09-28-compute-ingress-simplification-draft.md) | 2026-09-28，`7ad1876a` 加累积工作树 | 30 秒逐发布许可的由来、租约 ACL 入站规则与默认拒绝、无特权中介、删除范围；已采纳，2026-09-29 定稿：`none`/`published` 两档、HTTP 发布与 Docker 式端口绑定分开、多租约规则，写入 `INCUS-R-130`—`R-164`、`HOSTACT-R-014`/`R-015` 与运行问题记录要求 |
| [compute 租约出站分级草案](2026-09-28-compute-egress-tiers-draft.md) | 2026-09-28，`dddae0c2` 加累积工作树 | 四档出站与 Module 访问开关、Forgejo 官方建议映射、Incus ACL 执行与撤销；草案，未进矩阵 |
| [Incus 转发许可：重新授权与增量续期需求草案](2026-09-28-incus-forwarding-regrant-renewal-draft.md) | 2026-09-28，`dddae0c2` 加累积工作树 | 授权要素与绑定事实分离、部署/重启后自动重新打开的条件、逐实例增量续期；草案，未进矩阵 |
| [Incus 转发续期与入站中介的运行 owner 候选对比](2026-09-27-incus-runtime-owner-candidates.md) | 2026-09-27，`dddae0c2` 加累积工作树 | 独立服务、anasd（调度/进程内两种）与租约绑定许可的利弊，对照既有守护进程结论与 HOSTACT 约束；决策输入，未定案 |
| [Incus 租约围栏、归属与 7.x 限制键实机核验](2026-09-26-incus-fence-native-validation.md) | 2026-09-26，`0b61448` 加累积工作树 | 6.0.5 / 7.0.1 / 7.5.1 三档一次性 VM 16/18/19 项全部通过；物理网卡定时断链与 Debian IPv4 下载慢的处理；VM 启动、ZFS 与多工作区部署未覆盖 |
| [compute Contract 与 Incus 实现审查](2026-09-25-compute-contract-incus-review.md) | 2026-09-25，`0b61448` 加累积工作树 | project 围栏不完整、档位未由 daemon 强制、跨工作区共用 project、租约无撤销路径与 Contract 声明失真；未改实现 |
| [系统容器 Runner cgroup 与作业闭环](2026-09-22-incus-onejob-closeout.md) | 2026-09-22，`3f5242e` 加累积工作树 | 不可变镜像 OCI exec/资源限制、真实作业中断矩阵与物理 Docker 后置基线；终态见正文 |
| [lab-r6 镜像准入与真实工作流接续](2026-09-22-incus-onejob-acceptance.md) | 2026-09-22，`3f5242e` 加累积工作树 | 不可变新镜像 rootless API 已通过；真实工作流、夹具修复及本轮收尾以正文实际终态为准 |
| [Runner 根目录、主组与真实 one-job 接续](2026-09-22-incus-onejob-runtime-completion.md) | 2026-09-22，`3f5242e` 加累积工作树 | 原样候选双平台复测、rootfs 0700 与 newuidmap 主组不一致的实证修复；实际终态见正文 |
| [原样 Runner 引擎复测与失败补偿配额](2026-09-22-incus-engine-recheck-scope-capacity.md) | 2026-09-22，`3f5242e` 加累积工作树 | 原样 lab-r4 实机复测仍失败、空间预检与 boot 诊断；修复失败补偿遗漏 scope 占位，非 one-job 验收 |
| [Runner 引擎准入与 token 输入顺序](2026-09-22-incus-runner-engine-admission.md) | 2026-09-22，`3f5242e` 加累积工作树 | token 前有界 rootless 检查、controller 补偿与原生诊断；本轮 SSH 握手失败，非原 engine 故障已修复或 one-job 验收 |
| [Runner 构建恢复、resolver 与真实 Forgejo API](2026-09-22-incus-runner-build-recovery.md) | 2026-09-22，`3f5242e` 加累积工作树 | 中断构建取证、封闭阶段诊断、原生 resolver 正反控制及实际 scope API；各项终态见正文 |
| [Runner 真实构建尝试与 copy 路径修复](2026-09-22-incus-runner-bake-validation.md) | 2026-09-22，`3f5242e` 加累积工作树 | distrobuilder 原生 copy 反例/正例、真实取消后 revision 保护；完整镜像与 one-job 尚未验收 |
| [真实 btrfs 容器生命周期与取消回收](2026-09-22-incus-container-lifecycle.md) | 2026-09-22，`3f5242e` 加累积工作树 | 独立 QEMU 中真实 Provider/双租约/btrfs 写满/越权拒绝/取消回收；最小安装缺 dnsmasq 修复，非正式镜像与生产 ingress 验收 |
| [ln 主机隔离 Linux、KVM 与真实 Incus 客户端验证](2026-09-22-incus-ln-linux-validation.md) | 2026-09-22，`3f5242e` 加累积工作树 | 既有 Docker 只读基线；物理 namespace 原生测试、独立 KVM 实验机、真实 CLI 协议修复与取消回归；非完整产品 guest 验收 |
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

临时存储设计与停止审阅的有效结论已合并到[归档验收计划](../plans/archived/workspace-temp-storage.md#历史设计与停止审阅的结论)，原始测试文件不进入 Git。
