---
doc_type: review
status: current
created: 2026-09-20
updated: 2026-09-20
---

# Incus 设备绑定地址路由接续核对

基线：`master` / `49bbf45` 加此前累积工作树。直接修改原 checkout，保留已有暂存、未暂存与未跟踪改动。
本次接续不提交、不推送、不安装服务或修改实际宿主网络；生产 ingress 继续关闭。
此前 nft/界面恢复结果见[恢复记录](2026-09-20-incus-ingress-recovery.md)，不重复计作本轮实现。

## 已实现的候选保护

`address_routing.go`、`address_observation.go` 与 `address_integration.go` 提供独立的宿主地址路由层，
它不是 DHCP reservation，也没有改写 Incus 的地址池或实例设备。选定地址只经精确源 IP、guest 子网与
Docker 入接口的策略路由进入专用表；该表与 Traefik namespace 中的 `/32` 表分离。

安装按终止 `unreachable` policy → 表内 `unreachable default` → lookup policy 顺序进行。
新的容器分配先核验当前 host veth、MAC、peer index 和受管 bridge，再建立永久邻居与直接指向该设备
的 `/32` 路由。设备注销使路由丢失时，原 reservation 不会重建路由，普通 bridge 路由不得作为 fallback。
主机 local table 优先命中的目的地址、未知更早 policy、已有 table/priority/neighbor 和未知对象均拒绝，
不自动移动规则、接管资源或覆盖外部配置。

`.address-routing.json` 绑定完整安装作用域，保存基线 installing/installed/removing/removed、
分配 holding/held/releasing 与退休 token。同一完整分配的多个端口共享内核对象；不同 UUID、incarnation、
lease 或 epoch 不共享。最后一个端口退出前保留内核路由，设备已删除时不操作同名替代设备。
初期上限为 32 个分配、每分配 64 个使用者、退休加活跃 token 共 256 个；准入先预留退休容量，
不能因为历史记录已满而阻止已获准分配的清理。新增准入还限制编码后的 JSON 不超过 60 KiB，
在 64 KiB 持久文件边界内给清理状态迁移预留空间。历史不按 TTL 或重新安装清空，达到上限需显式维护。

`HoldAddress`、路由/许可创建及续租、完整工件盘点和 `ReleaseAddress` 已连接这条可选根侧接口。
启用该候选投影时，缺少独立 kernel hold 不再接受 journal-only ready。nft 请求 dispatch/deny 改按受管
目标子网匹配，具体 permit 的出口为获准 veth，而非可转发到其他 guest 的 bridge。
安装先验证 nft 拒绝基线；卸载先撤销所有发布和地址规则，再允许移除 nft 基线。

## 故障回归发现与修复

新增测试实际复现：邻居或地址路由创建失败后，普通 executor 的撤销流程因 publication 回执未保存
而卡在 `RemoveHTTPPermit`。修复在第一项地址外部动作前保存 `address_intent`，成功读回后才写
`address_held`。从未创建的 permit/Traefik route 通过独立读取确认不存在，不伪造非法的执行顺序；
未开始内核 hold 的已验证尝试只退休 token，不接管或删除冲突对象。conntrack 清理也要求精确回执。
现在该失败路径使用与正常 executor 相同的 permit → connection → route → inventory → address 清理顺序。

本轮没有新增第三方依赖、通用 root RPC、consumer 指定地址/argv 或生产启用开关。

## 原生验证入口与限制

`TestNativeAddressRoutingCannotFollowDeviceReuse` 在全新 Linux netns 创建测试 bridge/veth，使用真实
iproute2 FIB lookup 验证原路由对照、未批准拒绝、批准选择设备、删除与同名重建后不回落、清理及原路由保留。
已加入 `test-incus-ingress-native.sh` 的必跑清单，缺失、skip 或失败均不能得到 native 通过。
本轮仅交叉编译该用例，没有执行 namespace 或路由命令；独立 Incus observer 仍为测试夹具。
即使用例通过，也只证明前向 FIB/邻居行为，不证明真实 HTTP 包、反向 conntrack 或 Docker/Incus 共存。

**未完成项没有被重命名为完成：** 当前实现只识别 container veth，VM/TAP 另行实现，不能自动降级；
真实 Incus 分配和启动身份供给、反向流量与旧 TCP 连接撤销、生产 health identity、宿主动作/服务装配、
Docker/Incus 防火墙顺序、双栈和真实 guest 矩阵仍待完成。生产构造器仍拒绝启用 publication，
`developing` 和 30/75 统计不变。正式镜像签名发布与真实烘焙/启动仍按原计划单独跟踪。

## 本轮验证

| 检查 | 本轮实际结果 |
| --- | --- |
| 地址专项、正常发布/撤销、故障与未开始清理 | 通过；创建失败缺回执的问题先复现再修复 |
| `go vet ./...`、`go test ./...` | 最终工作树通过；未变化的包允许使用 Go 缓存 |
| `go test -race ./internal/incusingresshost ./internal/computeingressruntime -count=1` | 最终版本通过 |
| Linux amd64 / arm64 ingress 测试二进制交叉编译 | 通过；不是执行证明 |
| `bash -n test-env/scripts/test-incus-ingress-native.sh` | 通过 |
| Module / Contract 文档生成检查 | 通过；修改双语源文件后重新生成 |
| 需求覆盖、需求/计划索引、文档状态门禁 | 通过；Incus 状态仍为 30/75 |
| `npm run docs:build` | 通过，v0.1.1；非阻断 chunk 大小警告 |
| `git diff --check` | 通过 |
| Linux 原生 FIB/neighbor、实际双向流量、Incus/Docker/KVM/Traefik E2E | 未执行 |

最后新增的回归还确认：已有同 IP/MAC 的外部永久邻居不能被接管；该准入失败经普通撤销仅退休本次
尝试，不删除外部邻居。编码后的 JSON 预算为清理状态留有独立空间，不能只按字符数或 token 数推定安全。

实际连接宿主为 macOS arm64，Docker daemon 不可用；没有选择生产 SSH 目标，也没有触发或取得 native CI
结果。原生测试仅在代码中登记，不能把交叉编译或夹具当作真实内核/Incus 验收。

## 接口与语义依据

[Linux RPDB / ip-rule](https://man7.org/linux/man-pages/man8/ip-rule.8.html) 与
[ip-route](https://man7.org/linux/man-pages/man8/ip-route.8.html) 区分终止的 unreachable 与允许继续查表的 throw；
[ip-neighbour](https://man7.org/linux/man-pages/man8/ip-neighbour.8.html) 定义永久邻居与 add/del 语义。
[内核 IPv4 FIB 设备事件](https://codebrowser.dev/linux/linux/net/ipv4/fib_frontend.c.html) 的设备注销清理
用于设计候选保护，不作为目标内核版本已经执行的证据。
[Incus bridge 配置](https://linuxcontainers.org/incus/docs/main/reference/network_bridge/) 的 DHCP/MAC
映射与这里的设备生命周期不是同一保证；没有将普通静态租约当作被攻陷消费者下的完整隔离证明。

原生夹具在已新建并核验的 netns 内设置 `ip_forward`，再跑原路由正例；不以默认关闭转发造成的拒绝
当作保护成功。该设置没有在本轮执行。作用域与默认值依据
[network namespaces](https://man7.org/linux/man-pages/man7/network_namespaces.7.html) 和
[内核 IP sysctl](https://docs.kernel.org/networking/ip-sysctl.html)。
