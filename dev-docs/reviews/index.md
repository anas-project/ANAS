# 评审与历史快照

本目录保存针对明确日期、版本或提交基线的审查、库存和实施评估。文件名前缀日期表示评审基线，不是文档最后修改时间；后续只能补充解决状态或勘误，新的独立评审应创建新文件。

只保留仍有用的记录：所述实现已删除、发现已全部处理，或已被后续验收、需求矩阵、架构文档取代的评审直接删除，不另设归档目录。已提交的删除记录可在 git 历史中查阅；2026-10-04 清理前的全部记录见提交 `9a6921a1`，其他文档对它们的引用已改为指向该提交的固定链接。

| 文档 | 基线 | 类型 |
| --- | --- | --- |
| [Immich 初始接入评审与修正](2026-10-02-immich-integration-plan-review.md) | 固定 v3.2.4 与用户确认边界 | 设计评审；末尾修正早期方案，执行结果见私有计划 |
| [Immich master 整合评估](2026-10-07-immich-master-integration.md) | 9fc699c 与未提交工作树 | 基线差异与验证限制；最新 aa1944a 回归见私有计划 |
| [Casdoor 可信角色与撤权声明核对](2026-10-04-casdoor-claims-verification.md) | 2026-10-04，`7d612be5` 加当前未提交工作树 | 目录身份、准入和 back-channel 已实现；目录撤权已由另一任务接入；未找到 `anasRole` / CAEP 声明，本地测试通过，声明来源待确认 |
| [Incus 租约网络、HTTP 发布与端口绑定实施记录](2026-10-03-incus-lease-network-implementation.md) | 2026-10-03，`c7891162` 加当前未提交工作树 | M10a/M10b/M11/M11b/M11c、HOSTACT M5 与运行问题记录 CLI 的实现与本机门禁；撤销停止实例等五项自行决定；实机 e2e 全部未运行 |
| [finance 保留工作区部署](2026-10-02-finance-workspace-deployment.md) | 2026-10-02，审核Source25与CLI2c；finance新工作区 | 10000入口、Casdoor八Module、真实SSO编辑及验收后恢复；10月3日使用ln DNS key签发公网证书，主机TLS通过，当前外网TLS提前关闭 |
| [入站旧实现删除与宿主通道身份链简化](2026-09-30-incus-old-code-removal-and-hostd-simplification.md) | 2026-09-30，`7ad1876a` 加累积工作树 | 删除逐实例转发许可与逐发布 HTTP 入站运行时（225 个文件）；hostd 对端只认 root，调用记录取代回连 broker 与 systemd 退出观察（`HOSTACT-R-016`）；删除控制转发服务；服务安装必须带 `anas-helper`；宿主审批门禁实机结果见 §5 |
| [租约网络实机探测](2026-09-30-incus-lease-network-probe.md) | 2026-09-30，`7ad1876a` 加累积工作树；一次性 Debian 13 VM、Incus 7.0.1、docker.io 26 与 Docker CE 29 | M10a/M11b/M11c 待验证项与控制连接启动顺序：ACL 与 address set、默认拒绝入站、端口隔离、槽位固定地址、Docker 式端口表与占位；静态规则改窄规则、UDP 占位改常驻持有；据此 Incus 改为直接监听控制网关 |
| [Collabora 文档编辑失败诊断](2026-09-30-collabora-disk-pressure.md) | 2026-09-30 19:38–19:43，ln 主机现行部署 | 根分区低空间导致会话失败，健康探针及 discovery 仍成功；只读诊断，未重启或清理，提出临时存储与真实编辑验收要求 |
| [Incus proxy NAT 通配监听实机探测](2026-09-29-incus-proxy-nat-wildcard-probe.md) | 2026-09-29，`7ad1876a` 加累积工作树；一次性 Debian 13 VM、Incus 7.0.1 | 通配监听的 DNAT 不匹配目的地址，宿主外连与转发流量同端口被劫持；具体地址与 Docker 式 `fib daddr type local` 对照；诊断轮，未改实现 |
| [Incus 设计简化评审](2026-09-28-incus-design-simplification-review.md) | 2026-09-28，`dddae0c2` 加累积工作树 | 出站逐实例许可、无消费者的入站、部署热路径协调、hostd 身份链与控制转发的简化路径；未改实现 |
| [compute 租约入站简化草案](2026-09-28-compute-ingress-simplification-draft.md) | 2026-09-28，`7ad1876a` 加累积工作树 | 30 秒逐发布许可的由来、租约 ACL 入站规则与默认拒绝、无特权中介、删除范围；已采纳，2026-09-29 定稿：`none`/`published` 两档、HTTP 发布与 Docker 式端口绑定分开、多租约规则，写入 `INCUS-R-130`—`R-164`、`HOSTACT-R-014`/`R-015` 与运行问题记录要求 |
| [compute 租约出站分级草案](2026-09-28-compute-egress-tiers-draft.md) | 2026-09-28，`dddae0c2` 加累积工作树 | 四档出站与 Module 访问开关、Forgejo 官方建议映射、Incus ACL 执行与撤销；已采纳，写入 `INCUS-R-112`—`R-129` |
| [未适配发行版上的宿主供给审批链路](2026-09-27-incus-unadapted-distribution.md) | 2026-09-27，快照 `c0119e85`（`0.0.0-native.15`） | `INCUS-R-094`、`R-057`、`R-048` 实机验收 |
| [Incus 租约来源围栏 network ACL](2026-09-27-incus-source-fence-acl.md) | 2026-09-27，`fe85e510` 加累积工作树 | `INCUS-R-044`（M9a）实现与实机验收：伪造源由 Provider 网桥 ACL 丢弃、漂移可检出可修复；影响卸载盘点 |
| [Incus 镜像 prune 与 Core 租约全链路实机验收](2026-09-27-incus-image-prune.md) | 2026-09-27，快照 `c0119e85`（`0.0.0-native.15`） | `INCUS-R-072` 显式 prune、`R-055` 回滚与产物丢失、`R-111`、Core 路径来源围栏与卸载盘点 |
| [Incus 国内软件源实机核验](2026-09-27-incus-chinese-speedup-native.md) | 2026-09-26—28，逐轮冻结工作树与二进制摘要 | 三档宿主国内源后端、guest 烘焙与不可变配方检查、脚本断言修正及物理 Docker 基线；逐轮终态与边界见正文 |
| [Incus 7.0.1 两档生命周期、双栈出网与三处回归缺陷](2026-09-26-incus-vm-tier-lifecycle.md) | 2026-09-26，`fe85e510` 加累积工作树；嵌套 KVM | 两档实例全流程、同名双租约隔离、daemon 配额、双栈出网与时延基线（M2a/M6/M9a 证据） |
| [租约结束路径的真实 Core 验收](2026-09-26-incus-lease-revocation-core.md) | 2026-09-26，快照 `c0119e85`（`0.0.0-native.15`） | `INCUS-R-111`：移除消费者后证书撤销、project 保留并记 `revocation: confirmed` |
| [Incus 7.0.1 宿主供给审批实机验收](2026-09-26-incus-host-action-7.0.1-acceptance.md) | 2026-09-26，默认源改为 Zabbly `lts-7.0`（`7f0e8620`） | 三个一级发行版上的实际审批链路；`INCUS-R-047`/`R-048`/`R-057`/`R-107`/`R-108` |
| [Incus 租约围栏、归属与 7.x 限制键实机核验](2026-09-26-incus-fence-native-validation.md) | 2026-09-26，`0b61448` 加累积工作树 | 6.0.5 / 7.0.1 / 7.5.1 三档一次性 VM 16/18/19 项全部通过；物理网卡定时断链与 Debian IPv4 下载慢的处理；VM 启动、ZFS 与多工作区部署未覆盖 |
| [批量数据路径安全审阅](2026-09-26-incus-bulk-data-path-review.md) | 2026-09-26，`fe85e510` 加累积工作树 | `INCUS-R-083`（M13）：无制品下载端点与控制流内联，新增 OpenAPI 媒体类型门禁 |
| [Forgejo Runner 固定 AppArmor 加载范围与联合验收](2026-09-25-forgejo-fixed-policy-loader.md) | 2026-09-25，累积工作树；ln 实验环境 | 新不可变 Runner 镜像与真实工作流完整验收；联合停止链路另记 |
| [系统容器 Runner cgroup 与作业闭环](2026-09-22-incus-onejob-closeout.md) | 2026-09-22，`3f5242e` 加累积工作树 | 不可变镜像 OCI exec/资源限制、真实作业中断矩阵与物理 Docker 后置基线；终态见正文 |
| [Incus 网络与 proxy 权限核验](2026-09-10-incus-network-proxy-validation.md) | 2026-09-10，ANAS `f7642c5` / Incus `v7.3.0` | 固定版本源码调用链、兼容性缺陷与实机验证边界 |
| [凭据轮换机制审查：全面轮换与单独轮换](2026-09-04-credential-rotation-review.md) | 2026-09-04 工作树，HEAD `5306b63` | 轮换覆盖面、作用域语义与 resource 凭据空白 |
| [AI Agent 编排设计审查](../../modules/ai_agent/dev-docs/reviews/2026-09-05-orchestration-design-review.md) | 2026-09-05 工作树，HEAD `455770b` | 编排设计、凭据边界与事件状态审查（随组件迁至 `modules/ai_agent/`） |


本轮临时存储、目录身份键及 Casdoor 的测试验收结论已统一维护在[临时存储计划](../plans/archived/workspace-temp-storage.md)、[目录身份键计划](../plans/directory-identity-key.md)及[Casdoor 计划](../../modules/casdoor/dev-docs/plans/archived/casdoor-iam.md)，不再另存重复验收报告。原始证据在 Git 忽略的 `test-env/reports/2026-10-07-review-consolidation/` 保留本机副本；未建立共享归档。
