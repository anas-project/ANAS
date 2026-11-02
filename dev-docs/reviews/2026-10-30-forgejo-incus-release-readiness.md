# Forgejo 与 Incus 代码及发布状态复核

> 本文保留修复前的时点评审。后续实现与验证以 [Forgejo 计划 §14](../../modules/forgejo/dev-docs/plans/forgejo-module.md) 和 [Incus 计划 §17](../plans/incus-module.md) 为准；不把历史失败描述当作修复后代码结论。

状态：2026-10-30 的代码评审；两者继续保持 `developing`，不构成实机发布验收。

基线：`master` 的 `a9242c2acef71e14a1a3097c3fb59bf5fc1a9d59` 加当前工作树。只修改评审和计划，没有修复产品代码、提交或发布。相关源码摘要和本机测试结果分别记录在 [Forgejo 计划 §13](../../modules/forgejo/dev-docs/plans/forgejo-module.md#13-2026-10-30-代码与本机回归复核) 与 [Incus 计划 §16](../plans/incus-module.md#16-2026-10-30-代码与本机回归复核)；这里不重复建立验收矩阵。

## 发布判断

| 范围 | 当前事实 | 判断 |
| --- | --- | --- |
| Forgejo 代码托管 | `15.0.7-r3`，数据库、OIDC、恢复管理员、HTTP/SSH 和安全开关已接线；M0/M1 完成 | 尚不可发布：M5 的数据库/架构、Git/LFS/Package、备份恢复、升级回滚和身份实机矩阵未闭合 |
| Forgejo Actions | compute 消费、单开关、one-job、token stdin、正常/失败/中断及保留状态重启已有实现和部分实机证据 | 尚不可发布：状态丢失清理有确定缺陷，完整业务部署和双档/双架构的容器构建、网页取消、空闲基准仍缺证据 |
| Incus Provider | `7.3.0-r4`；租约、配额/证书隔离、共享客户端、宿主供给、网络、HTTP/端口发布、镜像及持久卷已实现；部分 amd64 实机矩阵通过 | 接近候选版，但不能宣布当前完整发布范围通过：新网络/卷能力的 VM 档、ARM64、正式 guest 镜像及交付证据仍不全，当前共享 Core/Linux 门禁也未全绿 |

Incus 的索引统计为 **136/141**。余下五项归 M8 长驻实例预留，不应把它们直接当成本版五个缺陷，也不能把该统计当作发布通过率。M14 其他发行版同属后续范围。Forgejo 的 `FORGEJO-R-054` 允许代码托管先于 Actions 发布；当前纯代码托管自己的发布门禁也尚未通过。

## 确认的代码缺口

### FJINC-001 / P1：Actions 状态丢失后不能回收孤立资源

`FORGEJO-R-046` 明确要求状态卷丢失后，在 Actions 保持开启时回收孤立实例及注册。

[`Reconcile`](../../modules/forgejo/actions-controller/controller.go#L27) 只遍历持久状态中的 `Workloads`，没有按 Provider 实际实例盘点；`ListManaged` 只在 [`CleanupAll`](../../modules/forgejo/actions-controller/controller.go#L264) 调用。[`ForgejoAPI`](../../modules/forgejo/actions-controller/forgejo.go#L17) 也只有队列查询、注册创建和按 ID 删除，没有注册列表对账能力。状态文件不存在时 [`FileStateStore.Load`](../../modules/forgejo/actions-controller/state.go#L44) 返回空状态，不会阻止正常调和。

本轮用 Go overlay 临时追加反例，不改仓库测试：空状态、空队列、Provider 留有一个受管实例，连续执行两次真实 `Reconcile`，期望回收实例，实际 `lists=0 deleted=[]`，反例失败。孤立实例残留已在单元层复现；注册丢失路径由现行接口/调用链确认，未重新运行真实 Forgejo 注册服务。

修复应让常规调和按可验证归属盘点并清理实例和注册，覆盖 `CreateRunner` 已成功但状态尚未持久化的窗口，不能只增加关闭时清理或依赖旧状态备份。

### FJINC-002 / P2：消费方声明的 IAM group → Forgejo team 映射未实现

`FORGEJO-R-060` 要求消费方提供映射并在下次登录移除已失去的 team 关系。当前 [`oidcInput`](../../modules/forgejo/forgejo/entrypoint.go#L37) 没有映射字段，[`ensureOIDC`](../../modules/forgejo/forgejo/entrypoint.go#L310) 只配置 `groups` claim 和管理员组，没有 `--group-team-map` / `--group-team-map-removal`。固定版本 CLI 支持这两个选项的历史探测不等于 Module 已接入。

这阻止完整外部自动化消费者集成完成；不将它扩张为 `FORGEJO-R-054` 允许单独结算的纯代码托管发布条件。

## 不应继续照抄的旧结论

1. **镜像类型/架构校验已存在。** Forgejo Hook 自身虽只检查 fingerprint 格式，但 compute 租约在消费者启动前经 Provider [`verifyImages`](../../modules/incus/provisioner/images.go#L27) 校验 fingerprint、architecture 和 image type；[`ensure`](../../modules/incus/provisioner/ops.go#L787) 在校验通过后才登记证书。`TestEnsureRefusesWrongImageBeforeRegisteringTrust` 覆盖三类不一致并通过。本轮更新 Forgejo 的 R-026 实现检查项，不再报告“只能等创建实例失败”。这不补足四种真实 Runner 镜像交付证据。
2. **Incus 不是“尚无实机证据”。** 2026-09-26 已有 amd64 的容器/VM 生命周期、隔离与配额证据；2026-10-10 native.54 在 Debian 13 和 Ubuntu 26.04 的容器租约网络测试各 221 项通过；2026-10-11 有容器持久卷与卸载重装记录。计划较早段落中的“未执行”须结合 §1 和后续执行记录理解，不能覆盖较新的通过基线，也不能将历史快照直接视为本轮工作树验收。
3. **IAM 发布矩阵漂移。** Forgejo 的 R-051/计划仍写 LLNG/Authentik，而 `APPCAT-R-027` 及现行应用目录计划已移除 Authentik、保留 Casdoor/LLNG。需要整理配套验收范围；不能继续测试已移除模块，也不能把 Casdoor 自动计为已验收。本轮未改需求。

## 已披露的设计债务与待证实风险

- **Actions 管理账号权限偏宽（设计债务）。** [`reconcileManagedActionsAccount`](../../modules/forgejo/forgejo/actions_account.go#L327) 调用 `ensureLocalAdmin`，且身份验证要求 `IsAdmin`。客户端确实只调用批准 scope 的 runners API，但密码泄露的权限边界仍是站点管理员。当前计划已明确披露；API 路径测试通过不等于服务端已实现最小权限。本轮不擅自新增验收条款。
- **同步请求被合并后可能漏掉新部署（尚未实机复现）。** [`portBindingWatch.Run`](../../cmd/anasd/port_bindings.go#L107) 在排队前更新已见部署身份，`InvokeSync` 使用无部署代次的固定参数，运行中的同名动作按 `ActionCoalesce` 返回旧作业；周期检查只记录问题、不补同步。若旧作业已读完旧部署、新激活又被并入旧作业，可能直到下次激活/服务重启才应用新端口表。Traefik 地址同步有相似触发模式。该疑点在 Incus 计划 §14.2 已登记，当前代码未见后续补跑机制；应以“旧作业读取后激活新部署”的并发反例验证，不能记为已复现实机故障，也不能忽略后直接批准发布。

## 收尾顺序

1. 修复状态丢失回收；补齐既定 scope 内的身份 team 映射，并整理 IAM 矩阵与过时计划表述。
2. 将当前候选源码固定成可识别制品，在 Linux 上跑共享 Core 回归；处理全仓测试目录、共享构建和升级目录阻塞。初查邮件反例漏登记，收尾复查已改为邮件用例的实现/需求摘要过期；共享构建及升级目录仍失败。当前错误来自邮件/LLNG，不能归因于 Forgejo/Incus，但整个工作树仍不能直接作为发布候选。
3. 沿配套计划补真实业务部署、双架构/相应隔离档及正式镜像交付的剩余验收；验证同步竞争。只有已确认发布范围内每个条件通过后才调整 `status`。若希望先发布受限范围，应单独明确支持范围，不能由本轮评审静默缩小。
