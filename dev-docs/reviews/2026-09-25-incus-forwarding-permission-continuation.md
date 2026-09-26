# 租约转发确认事务与原生读回接续

状态：实施中；生产启用保持关闭，默认 Docker 下的真实 guest/Forgejo 联合验收尚未通过。日期：2026-09-25。

实际 checkout `/Users/whl/Documents/anas`，HEAD
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。全部已有 staged、unstaged、untracked
改动保留，未暂存、提交或推送。暂存 diff SHA-256 在本轮开始核验为
`fc6a330643646f357c5c7e95f3a17cb63b663f7fa47313e8a1e0c534c209d0fb`。
仅经 `ssh whl@ln.hlong.wang -p 2200` 使用独立实验；无联网搜索。

## 上轮遗留环境的实际收尾

接续时 `kernel-r1` / `anas-incus-host-0384f1` / 22270 确实仍在运行。先保存其 r3
两张实验 nft 表的真实读回，再通知原监督器正常关机。精确 QMP 身份、QEMU 退出0、
无强制退出、无清理错误、22270 无监听及物理宿主十项基线一致均已重新核验。
原失败磁盘、intent、receipt 与实验表没有被删除。报告目录：
`/data/anas-incus-forwarding-20260925.bwWwTX/kernel-r1/reports`。

## 产品接线与边界

Provider profile 强制 MAC/IPv4/IPv6 来源过滤；实际实例读取器另检查 expanded NIC，
不能只相信默认 profile。租约观察复用既有 pinned mTLS/真实实例读取，不制造 HTTP
ingress 授权。当前 Core 资源、真实投递凭据、独立受限证书、实例 UUID/运行代际、
分配地址/MAC、物理 veth 及目的路由分别检查。

新增编译动作 `incus.forwarding.permission.plan` / `incus.forwarding.permission`，
复用现有共享 job、一次性确认、release/工作区绑定、hostd 和审计。只接收资源与明确
IPv4/TCP 目的地，操作为 enable/disable；调用者不能提供 IP 来源、接口、命令或路径。
事务回执位于既有宿主 `state.json` 的 `forwarding_scopes`，不是第二套 job 数据库。

每次外部效果前持久化 pending；失败保留原回执。disable 不重新授权已撤销租约，先
关许可，再精确清理已观察的双向 NAT 连接；保留拒绝表，不冒充完整释放。残留许可或
无效归属阻止宿主供给、凭据依赖变更及卸载。新动作仍有编译侧生命周期门禁：enable
不可用于生产开放流量，不能靠确认 token、请求字段或环境变量绕过。没有自动续期、
完整停止协调、重启恢复或公开完整业务部署的完成声明。

## 原生 r4 与读回修复

旧 r3 证据表明 nft 1.1.6 会把 meta iif/oif 编号输出成可复用名称，即使使用数值选项；
同时省略后续类型化 payload 已蕴含的协议条件。协议回归先失败，再实现限定的无副作用
合取归一化；不跨 counter/verdict/jump。接口编号复用既有 netlink GETGEN/GETRULE
读取器，规则 handle、寄存器和实际数字独立核对；不按当前名称重新学习接口编号。

全新 `kernel-r2` / `anas-incus-host-19d724` / 22272 使用 76 个冻结输入，二进制为
`7ce38cb031ded9cca2cbe8581da81872b180a0c3ea8079c9aad21f68957fd85f`。
默认 Docker DROP 保持，空拒绝表/兼容链/ipset 安装通过，首次 nft 许可刷新后的完整
读回失败，原生退出1。六项便携/数字解析测试通过不能覆盖实际 native 的失败。
失败回执保留 `nft.refresh` 与 pending instance，不清除表来重复认领。

归档：`/data/anas-incus-forwarding-20260925.bwWwTX/kernel-r2/reports/public-evidence.tar.gz`。
SHA-256：`fe21eb1bb832888d4376dd477c1442c72b568db954b218a4e5271fdb4f0cbc2a`。
该 VM 正常退出0，无强制退出/清理错误；物理宿主原24容器、17网络及其余基线全部相同。

该失败另复现一项事务缺陷：刷新可能已生效但读回失败时，旧回执仍声称连接已关闭。
新增回归先失败，再改为刷新前持久化“关闭/完全撤销均未确认”，不把旧状态当新证明。
新增失败当刻的本实验表读回保存，避免下次重启或 TTL 消失后推测原状态。

后续 r5 使用新冻结二进制 `21c5ea627e506c4ab82de3d25ac146e15466d17cfdca8786033df11940254ba3`，
全新 `kernel-r3` / `anas-incus-host-3c428a` / 22274；终态、独立证据检查和最终门禁在
取得后追加。它仍是内核执行器测试，不是实际 guest 或 Forgejo 工作流。
