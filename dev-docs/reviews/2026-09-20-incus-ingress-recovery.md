---
doc_type: review
status: current
created: 2026-09-20
updated: 2026-09-20
---

# Incus 入站实现恢复与继续核对

基线：`master` / `49bbf45` 加前轮累积工作树，直接修改原 checkout，保留已有暂存、未暂存和未跟踪修改。
本轮恢复 MCP 后确认：前轮代码、测试和 native CI 门禁已落盘，收尾文档补丁未落盘。
未提交、推送、安装生产服务或开放生产 ingress。

## 已恢复记录

前轮新增的 `internal/incusingresshost` 实现包括完整 Target 与安装拓扑摘要、v2 回执、安全临时文件、
可取消 guard、Docker/procfs/nsfs 独立核验和 opened-netns 执行器。真实源地址位于容器 NIC，网关位于
宿主 bridge，两者分开观察；夹具扩展 JSON 不作为生产 `ip` 输出。多端口只共享同一完整实例分配身份，
未释放的旧 hold 不允许重启或不同实例复用。生产关闭状态不能写入“地址已保留”回执。

回执为 `anas.incus-http-host-receipt/v2`。旧 v1 不静默迁移、不从 comment 接管，也不自动删除。
新版同时绑定实例 UUID、租约 Resource、容器进程和 namespace，防止旧回执授权替换后的拓扑。
新增 CI 脚本 `test-env/scripts/test-incus-ingress-native.sh` 检查实际测试事件，拒绝跳过或漏跑。

前轮可见工具回执证明：修复后的专项、全仓 Go 测试和 Linux amd64/arm64 测试编译通过。
该历史结果不是本轮新执行的结果，交叉编译不是原生执行；真实 Linux/Incus/KVM 等验收未执行。

## 本轮新实现：受管防火墙与独立归属

先添加测试并实际复现五类失败：全局 `policy drop`、原生 numeric timeout/inline concat 不兼容、
顶层 comment 被拒绝、空集合误报 ready、无 ANAS 注释的外来规则被盘点忽略。相关初始失败未作为门禁通过。

- `nft.go` 的基线改为 `policy accept`，仅在受管 bridge 路径施加明确拒绝。inet forward 先跳入独立
  `http_permits` regular chain，未命中才拒绝；旧版将许可追加在终止 drop 后，实际无法放行。
  反向 HTTP 流量同样先验许可，guest 自己发起 NAT 出站的 conntrack reply 单独处理。
- bridge prerouting 按真实 `ibrname`、Traefik veth、源 MAC/IP 拒绝冒用，在这条仅 IPv4 的后端网络
  拒绝 IPv6。它不修改公网 Traefik 的 IPv6 入口，也不宣称覆盖其他 Docker/Incus base chain。
- 读回使用有界的原生 nft JSON，读取顶层 rule comment、numeric timeout、inline concatenation 与
  expiry；核对 table/chain/handle/priority/policy、完整有序 AST、所有规则和集合。重复、额外 verdict、
  条件、对象、dormant、重排及跨表身份均拒绝，不再凭注释或名称前缀排除未知对象。
- 单 publication 集合最多一个精确 IP/端口，活跃状态还要求有效剩余期限。空/过期集合可按原归属撤销，
  但不得作为 ready；已登记 ready publication 丢失规则对、集合或 live tuple 时盘点失败。
  CT state 的等价表示按上游互斥状态位语义归一，不能推广到 TCP flags；未知字段与更宽状态集合仍拒绝。
- `nft_lifecycle.go` 增加独立 `.nft-baseline.json`，绑定安装作用域与两个真实 table handle。
  安装前完整 list-tables 证明目标不存在，先持久化 installing intent，再 `nft -c`、事务应用、读回并保存
  installed；无独立回执的现有表不接管。重复安装校验而不重复写入。
- 卸载前证明 publication、route、permit、connection 均清空，持久化 removing，再删除并通过实际表库存
  确認缺失后记录 removed。失败保留 intent，不把下一次 apply 或相同表名当作恢复裁决。
  复用已有 securefs/dirfd 私有文件路径，没有新增 Go 依赖。

`nft_boundary_test.go` 与 `nft_lifecycle_test.go` 覆盖上述反例、幂等、失败保留、table 替换、连接残留、
临时文件、规则顺序和过期清理。新增 `nft_native_linux_test.go` 在全新独立 netns 中运行真实 nft 的
语法、JSON、安装/许可/续租/撤销/卸载；IP/allocator/conntrack 仍为夹具，不能把该测试解释为网络流量验收。
CI 在可销毁 runner 安装 nftables，普通身份预编译后执行指定 native 测试；脚本要求每项确实 pass，
拒绝 skip 或漏跑。本轮只编译它，没有运行原生用例、触发 CI 或变更实际宿主规则。

## 收尾发现并修复：镜像清理前端契约

`npm --prefix web run typecheck` 首次失败，定位到前轮新增的 `incus-prune-flow` 使用了实际 HTTP DTO 与
OpenAPI 都不存在的 `job.action`、可选属性赋 undefined 与未类型化 Promise。不是通过放宽类型设置或
双重断言掩盖：改为读取真实 public job 的 `kind`/`mutating`/workspace/id，以及 result 中的 schema、action、
plan 和批准 binding。界面同时检查计划摘要、状态摘要与待删集合与 binding 一致；不把内部 ActionState
泄露为新增 API 字段。

测试夹具现在完整满足生成的 HostActionJob 类型。空 Go slice 的 null 可显示为无变更，不触发确认；
异步期间冻结 input，输入改变或组件卸载时不能沿用旧确认；未知 apply 结果仍不自动重试。
新增公开 DTO、空计划、绑定漂移、输入变更和待确认卸载回归。最后 typecheck、20 文件/92 测试、主界面
与应急界面 build 均通过，嵌入发行产物已生成；没有修改实际服务或运行破坏性清理。

## 当前边界

显式镜像 prune 的 plan/apply、一次性确认、workspace/历史引用保护、删除和读回此前已存在。
本轮修正文档“只有 dry-run”的过期表述，不把已有代码计为本轮新实现。真实破坏性删除仍未验收。

生产 ingress 仍缺真实地址分配生命周期保证、health 身份和生产动作/服务装配。回执内的 IP 占用不是
Incus DHCP reservation；新增 namespace 原语也不等于真实网络验收。正式镜像签名发布、真实烘焙/
启动、首批发行版安装/重试/卸载、配额、取消/崩溃回收、双栈及 Forgejo one-job 仍需独立验收。

## 本轮验证

| 检查 | 本轮实际结果 |
| --- | --- |
| 初始五项 NFT 回归 | 先失败；修改后通过 |
| `go vet ./...`、`go test ./...` | 通过；未变化的包允许使用 Go 缓存 |
| `go test -race ./internal/incusingresshost ./internal/computeingressruntime` | 通过 |
| 最后生产代码更新后的上述专项 `-race -count=1` 与专项 vet | 通过 |
| Linux amd64 / arm64 测试二进制交叉编译 | 通过；不是 native 执行 |
| `bash -n test-env/scripts/test-incus-ingress-native.sh` | 通过 |
| Module/Contract 生成检查、需求覆盖、需求/计划状态和文档状态门禁 | 通过 |
| `go run ./cmd/check-shared-build` | 通过；静态校验，不是 Docker 构建 |
| `go run ./cmd/check-upgrade-tests` | 通过，2 products / 24 modules / 6 suites / 20 transitions；此前缺口未在当前工作树重现 |
| `npm run docs:build` | 通过，v0.1.1；存在非阻断 chunk 大小警告 |
| 前端 `check:api`、`typecheck`、Vitest、主/应急界面构建 | 通过；初次类型失败已经修复，20 文件/92 测试 |
| `git diff --check` | 通过 |
| 真实 Linux/netns/nft/Incus/Docker/Traefik/KVM/双栈/one-job | 未执行 |

当前 Mac 宿主 Docker daemon 不可用，仓库没有本轮授权的远程 targets 配置。未自行选择生产 SSH 目标。
Incus 保持 developing 和 30/75，完整生产 ingress 仍关闭。

## 上游语义依据

[nft 官方手册](https://www.netfilter.org/projects/nftables/manpage.html) 说明 base-chain policy、hook 顺序、
accept/drop 的作用范围，以及互斥 conntrack state 的集合匹配；[libnftables JSON 手册](https://manpages.debian.org/testing/libnftables1/libnftables-json.5.en.html)
定义顶层 comment、numeric timeout/expiry、concat 与 match 表示。这些文档用于纠正实现和夹具，
不构成本仓库实际内核规则、Docker/Incus 共存或双栈验收证据。
