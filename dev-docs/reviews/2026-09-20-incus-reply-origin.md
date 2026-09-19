---
doc_type: review
status: current
created: 2026-09-20
updated: 2026-09-20
---

# Incus 回复来源与双向连接清理接续核对

基线：`master` / `49bbf45` 加此前累积工作树。直接修改原 checkout，保留已有暂存、未暂存及未跟踪修改。
未提交、推送、安装生产服务或修改实际宿主网络。前轮的地址路由候选及其历史测试见
[地址路由核对](2026-09-20-incus-address-routing.md)，不重复计为本轮新实现。

## 原问题与回归

初始 `TestConntrack*` 回归实际失败：旧解析器只取第一组 src/dst/dport，接受缺失回复元组、缺源端口、
错误回复地址/端口、第三组元组、重复字段、错误 family/protocol 编号、非零 zone 与 offload 标记。
另外复现许可仍开放时可以删除连接、原方向单组伪造库存可以授权删除。修复后专项通过。

回复许可此前只在 inet 层匹配 guest bridge 与 IP/端口，没有独立的物理入口绑定。本轮没有把新鲜 IP/MAC
观测当作连续分配证明，也没有把消费者传入的接口名当作原实例身份。

## 新增回复来源候选

配置了既有 `address_routing` 的 scope 增加 bridge regular chain `http_reply_origins`。
受管 guest bridge 上发往 Traefik 后端源 IPv4 的包先检查该链，未命中时明确拒绝；不增加全局默认拒绝。
每个许可同时匹配原数值 ifindex、veth 名、guest MAC、IPv4/TCP、Traefik 地址以及获批 guest IP/源端口。

数值 ifindex 从 `.address-routing.json` 的独立原始 hold 读取，租约、实例、incarnation 和 reservation
必须对应。清理时仍使用原记录，不能查询同名替代设备并学习其新身份。前向 inet 和回复 bridge 的定时
集合在同一 nft 事务里创建、续租和撤销；只在两侧精确读回后登记 ready。任何一侧缺失、过期、重复、
出现未知对象或身份变化都不能报 ready。inet 请求/回复规则另显式匹配 original/reply conntrack direction。

代码更新了既有发布、撤销、完整盘点和重复安装路径，没有新增通用 root 命令接口。策略摘要增加
`bidirectional-origin-v1` 域，因此旧策略回执不能静默解释为新策略授权；既有证据保留、需显式恢复裁决。

## 双向 conntrack 边界

解析完整原/回复四元组及协议，要求回复为原元组的精确反向；端口、地址、计数、zone 和记录数均受界限约束。
本候选是无后端 NAT 的直接 IPv4/TCP 路由，只处理默认 zone 0。翻译元组、非零 zone、offload 或未知格式
均拒绝，不宣称支持所有 Docker/Incus conntrack 布局。

连接删除前必须有精确 publication 回执，许可不能 ready 或处于未完成 intent，且两侧 nft 许可已读回缺失。
只对已经观察到的每条原/回复地址和端口、默认 zone 执行精确删除，单次最多 256 条；随后读回为空才返回成功。
不使用全表 flush，不把命令失败当作空表。scope 列表请求限定 Traefik 源和 guest 子网，越界响应拒绝。

多端口回归检查删除事务不触碰其他端口的 rule handles/set；失败回归确认保留 intent，在两侧许可未撤销前
禁止删除连接。原地址层正常/失败撤销流程继续沿用。

## 新增原生用例（本轮未运行）

- `TestNativeReplyOriginRejectsSpoofAndDeviceReuse` 在全新 netns 中创建 veth/bridge，以真实 Ethernet/IP/TCP
  帧测试获批来源、伪造入口/MAC/端口、撤销、定时过期与同名新 ifindex。装规则前必须验证两条路径都可达；
  替换负例还要求旧许可尚未过期。它不是完整 TCP 会话、Incus guest 或 Docker 共存验收。
- `TestNativeConntrackBidirectionalCleanup` 在独立 netns 插入两条真实内核记录，校验原生 CLI 双向输出，
  精确清理目标并保留相邻端口对照。这是记录生命周期测试，不是已存在 TCP 会话或排队包的流量证明。

二者已加入 `test-incus-ingress-native.sh` 的必跑清单。CI 对可销毁 runner 显式准备 nftables、iproute2、
conntrack，普通身份预编译后运行限定 native 测试；缺失/跳过不得算通过。本轮未触发或取得 CI 的结果。
没有引入新的 Go 依赖。

## 验证记录

| 检查 | 本轮实际结果 |
| --- | --- |
| 初始 conntrack 缺陷回归 | 实际先失败，修复后通过 |
| 两包专项 `go test -count=1` | 通过 |
| 多端口不越界、失败 intent 保留和删除 argv 绑定 | 通过 |
| `go vet ./...`、`go test ./...` | 最终源码通过；未变化的包允许 Go 缓存 |
| `go test -race ./internal/incusingresshost ./internal/computeingressruntime -count=1` | 通过 |
| Linux amd64 / arm64 ingress 测试二进制 | 交叉编译通过，不是执行证明 |
| native 门禁脚本 `bash -n` | 通过 |
| Module / Contract 生成检查、需求覆盖、需求与计划索引、文档状态 | 通过 |
| `npm run docs:build` | 通过，v0.1.1；非阻断 chunk 大小警告 |
| 共享构建静态检查、升级测试目录检查 | 通过；不代表 Docker build 或升级 E2E |
| `git diff --check` | 通过 |
| 新增原生包/conntrack 用例、完整 TCP、真实 Incus/Docker/KVM/Traefik/双栈 | 未执行 |

开发中曾因通用 nft 库存循环局部变量遮蔽表名而导致许可回归失败，已修复并重新执行两包、竞态和全仓
检查，没有放宽归属核验。当前实际连接为 macOS arm64，Docker daemon 不可用，未发现已授权的
`targets.local.yml`；没有自行选择生产 SSH 目标或运行实际宿主网络命令。前端本轮没有行为改动，
没有把此前的 92 项前端测试计为本轮新运行结果。

## 尚未完成

这仍是受管 container veth 路径的候选，不是完整生命周期安全证明。数值 ifindex 的强制/回绕复用、
接口仍存活的停止/暂停/恢复、真实 Incus 独立身份供给、完整 TCP 会话和排队包、VLAN/快速路径/卸载、
VM/TAP、生产 health identity、宿主动作与服务装配以及 Docker/Incus 规则共存仍需独立实现或验收。
正式镜像签名发布、真实烘焙、启动、配额、双栈和 one-job 仍按原计划跟踪，不因本轮代码而登记完成。
生产构造器继续拒绝开启 publication；Incus 保持 developing，需求统计仍为 30/75。

## 上游接口依据

[nft 上游手册](https://www.netfilter.org/projects/nftables/manpage.html) 定义数值 `iif` 与可重新匹配的
`iifname`、bridge 元数据、numeric 输出和 conntrack direction；
[conntrack 上游作者手册的发行版镜像](https://manpages.debian.org/testing/conntrack/conntrack.8.en.html)
定义 orig/reply 地址、端口、zone、extended 输出及 CIDR 过滤。它们是实现的接口依据，不是本仓库原生执行证据。
