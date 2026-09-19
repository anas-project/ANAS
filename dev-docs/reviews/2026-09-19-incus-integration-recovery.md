---
doc_type: review
status: current
created: 2026-09-19
updated: 2026-09-19
---

# Incus 中断恢复与集成回归

基线：`master` / `49bbf45` 加此前已暂存和未暂存工作树。直接修改原仓库，未提交或发布，
未修改真实宿主服务、软件包、防火墙或账户。本文区分当前代码、可重复的本机证据和未完成目标。

## 本轮修复

1. **共享 job 参数往返。** 新测试复现了合法计划参数经过 map 持久化后因 JSON 键排序被严格
   字节比较拒绝的问题。现按精确字段和值比较，保留 ABI 大小、深度、UTF-8、重复键、未知字段
   与大小写别名检查。增加完整的计划公开投影、签发、消费、Store、执行绑定、wire 解码和独立
   Claim 回归；重复 Claim 拒绝。这里的宿主观察是夹具，不声称执行了 root 操作。
2. **私有连接与控制网络。** 宿主 bundle 携带真实观察架构及受管存储池；Hook 自动接入原 Secret
   Store，重放已恢复 Env 也重新核对自动绑定。Provider 使用 external 控制网桥，Forgejo 两个
   compute 服务与 AI Agent 经每资源投影连接相同网络，业务网络保留默认出口。完整远端配置
   不读取本地 bundle，部分显式与旧自动绑定不得混用。
3. **供给操作。** APT 配置由受审计安装 effect 准备并逐字节检查，只接受完整匹配的编译 recipe；
   未知源码或漂移文件不自动覆盖。root 目录创建使用逐级目录描述符，状态读取使用 nofollow/
   nonblock、前后身份复核和关闭错误传播。控制网络还需 internal/no-IPv6/no-ICC/no-masquerade
   读回，不能仅凭 owner 标签认领。
4. **防火墙和输出边界。** 移除会影响无关宿主 IPv6 TCP 的拒绝规则；规则只针对受管桥与端点。
   读回验证 AST、链内顺序、priority、额外规则、大小写/重复字段和 dormant flags。动作公开
   投影拒绝未知数据和伪造 compute readiness；进度只允许当前编译动作的固定阶段名。
   另以失败测试复现了本机登记探测会被默认拒绝的问题，补入仅限 `lo`、同一受管源/目的地址和
   固定端口的规则及客户端源地址绑定。配方显式安装 nftables；APT 的 partial 目录保留给 APT
   自身管理。最终静态检查发现的超时上下文泄漏已修复，卸载实际操作使用执行者预算，只有
   持久化收尾使用独立、正确释放的限时上下文。
5. **CLI、界面与安装。** 会话及确认只读单一 stdin 信封；UI 改为类型化选项、服务端计划、
   明确同意和自动重新展示过期计划，不再让用户粘贴令牌或参数 JSON。切换输入/工作区和卸载
   组件会结束本地等待，不触发 apply 或服务端取消；未知 apply 不重试。升级/卸载先检查活动
   宿主操作、停止准入并复查，失败不覆盖二进制；同一开机内确认消费记录保留。

## 身份与权限结论

保留控制台既有 root/root 服务，撤回早期“必须迁移非 root anasd”的前置判断；不放宽 TLS key
权限、不加入 Docker 组、不递归改状态属主。UID 0 不是唯一准入条件：固定安装策略、实际内核
peer 和 PID 1 独立单元身份都必须一致。`anas-hostd` 是完整 root 的具名操作执行器；安装官方
软件包和系统账户需要 `/etc`、`/usr`、`/var`、`/run` 的权限，不能把它宣称为 DAC-only 沙箱。

`host_actions` 默认关闭。安装器打包并安装程序和固定单元，但不默认启用控制转发；真实系统
的 unit 权限、包维护脚本、退出/清理和网络行为仍须 native 验收。

## 验证

| 检查 | 结果 |
| --- | --- |
| JSON map 往返失败复现及修复后完整确认交接 | 通过；真实 Store/lease/ledger，宿主 plan 为夹具 |
| 六包专项测试 | 通过；后续改动还需以最终全仓结果为准 |
| 前端类型检查与 Vitest | 通过，19 个文件、82 项，含 9 项确认状态机用例 |
| 隔离安装器 fixture | 通过，活动宿主动作和停止失败不覆盖现有版本 |
| 最终全仓 Go test/vet | 收尾验证后记录 |
| 竞态、Linux 双架构源码/测试编译 | 收尾验证后记录 |
| 前端发行构建、源码生成及文档门禁 | 收尾验证后记录 |
| 真实 Linux/systemd/Incus/KVM/apt/nft/双栈/one-job | 未执行，不以交叉编译或夹具替代 |

最初全仓测试在 `incusingresshost` 测试夹具的 5 秒外部命令边界超时；把夹具的多次 `echo | grep`
改为 shell 内建匹配，保留正式时限和原故障注入语义，专项重跑通过。未通过加长生产期限、跳过
用例或关闭权限校验来制造成功。

## 未完成范围

生产 ingress 仍缺真正的地址生命周期证明、打开的 netns FD 执行、应用健康身份及宿主/消费者
装配；破坏性镜像 prune 的原子确认/删除、正式签名镜像分发与真实烘焙也未完成。宿主部分失败
遗留意图仍会阻断，需要受限恢复裁决，不能把重新运行 apply 当成孤立资源归属证明。非 systemd
与额外发行版仍在后续范围；当前首批发行版的真实安装、重试、卸载和隔离验证也未运行。

当前本机为 macOS，Docker daemon 不可用，仓库未提供已授权的 `targets.local.yml`。本轮没有
自行选择生产 SSH 目标，没有伪造镜像 fingerprint、发布证明或成功的 native 结果。
Incus 30/75、宿主通道 0/13、`developing` 和生产 ingress 关闭状态不因代码数量而改变。

## 上游接口参考

- [Compose 网络的 external 生命周期](https://docs.docker.com/reference/compose-file/networks/)
- [Compose 服务的 gw_priority](https://docs.docker.com/reference/compose-file/services/#gw_priority)：本次使用需要 Compose 2.33.1+。
- [Docker 内部网络](https://docs.docker.com/reference/cli/docker/network/create/#internal)：是否可达宿主和是否拥有业务默认出口分别验证。
- [ip-route 的 local 路由](https://man7.org/linux/man-pages/man8/ip-route.8.html)：本机地址的包会回环交付；据此审查宿主探测规则，不当作已执行原生 nft 验收。

这些接口说明不构成本仓库的真实网络验收证据。
