# Incus 转发失败撤回与显式退役原生验收

状态：失败撤回已接入原确认事务，显式退役专项原生通过；生产启用、自动续期与真实 guest/Forgejo 默认路径仍未完成。
记录日期：2026-09-26。原生事件保留服务器实际 UTC 时间（2026-09-25），不重写历史时间戳。

## 接续基线

实际 checkout 为 `/Users/whl/Documents/anas`，HEAD 保持
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。重新发现工具并复用实际工作区，读取根指令、
需求/计划索引、Incus 要求/计划、宿主架构及最新接续记录。全部原 staged/unstaged/untracked
改动保留，未暂存、提交或推送。开工暂存 diff SHA-256：
`fc6a330643646f357c5c7e95f3a17cb63b663f7fa47313e8a1e0c534c209d0fb`。

最近可见回复不是最后的执行状态：当前仓库已有确认动作、原宿主状态接线和显式退役，
不能重新当作只有孤立内核执行器。旧 `kernel-r1` 已正常关机，随后内核第六轮已有独立
通过记录；最新 `retirement-r1` 内核部分通过，但退役计划拒绝，完整入口退出1。
本轮读取其真实 supervisor 和十项宿主对照，确认正常关机、QEMU0、无强制退出/清理错误。
原失败盘、intent、receipt 及归档未修改。前置记录见
[显式退役接续](2026-09-26-incus-forwarding-retirement.md)。

## 许可可能生效后的产品失败出口

新回归先执行模拟内核的刷新及回执保存，再注入后续错误，不把所有错误都模拟为没有
外部效果。红灯实际复现三个出口没有撤回：刷新读回失败、最终保存失败、会话关闭失败。
最后一种还留下 enabled/live 状态。已有刷新后取消分支能撤回，但不能替代其他出口。

`internal/incusprovision/forwarding_permissions.go` 现使用共同失败收尾器，在原宿主
状态锁下处理后置错误，包括会话关闭和取消。有原内核句柄时，以独立30秒上下文先持久化
撤回意图，再调用原 `kernel.Close`；不创建新授权、重新安装或刷新重试，不认领没有
回执的未知对象。原内核执行器仍逐步保留 pending 与失败历史，先关闭许可，再核对
精确连接撤销。

成功补偿不把原启用改成成功：原授权、实例身份与失败状态保留，依赖拆除仍被阻止。
另一个回归在调用方取消后使撤回失败，验证只尝试一次、独立期限有效、原 pending
`connections.close` 和失败记录保留、既有连接不被误报撤销。不新增动作、权限、宿主
脚本入口或第二套状态库；生产 enable 的编译门禁保持不变。

## 第二轮的真实退役阻止原因

本轮新建 `/data/anas-incus-retirement-20260926.5p9Wxh/retirement-r2`，VM 为
`anas-incus-host-e30a36` / 22282。252个源码输入重新冻结，包含开工前已有的新原生
正向检查和有界诊断，但不包含随后编写的上述失败收尾器。

内核通过；撤销证书后的空项目可计划正例通过。随后创建/删除实验空实例，再次退役
计划失败。实际诊断显示 host/config/record/bridge 检查均成立，被定位的 API 阻止为
`operation_counts={"success":2}`：Incus 暂留两个已完成异步操作记录。这是正确的
非空拒绝，不能放宽产品或删除记录，也不能让这个原因冒充物理端口隔离已证明。

完整入口退出1，正常关机、QEMU0、无强制退出/清理错误。归档 SHA-256：
`005c5bc5fb4285c52b38ad8fbc1fef787b510ecec0b93b90fa0f5f566b5e5523`。
首轮没有同样完整的诊断，不能把第二轮观察倒填为首轮当时的全部事实。

原生夹具现在在删除实例后读取原操作清单，最多等待30秒自然为空；不删除或改写操作，
遇到未完成/失败组或不完整响应仍拒绝。实际为空后先验证计划成功，再进行独立物理端口
反例。便携 API 回归明确 success/failure 非空组均拒绝；循环不再次断言计划必失败，
避免两次请求之间的自然过期造成假失败。产品的空清单判据没有改变。

## 第三轮冻结身份与完整终态

| 项目 | 实际值 |
| --- | --- |
| 实验目录 | `/data/anas-incus-retirement-20260926.5p9Wxh/retirement-r3` |
| VM / SSH | `anas-incus-host-9d4ca5` / `127.0.0.1:22284` |
| 环境 | 全新 Ubuntu26.04 amd64，KVM，2CPU/2048MiB，独立32GiB写盘 |
| 基础镜像 SHA-256 | `4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc` |
| 冻结源码输入 | 253项，构建前后及最终复核一致 |
| 输入清单 SHA-256 | `a9fde13dd4abf3da92ca337f01f844073b5032f5a605063f2c83ec89312660ce` |
| 内核测试 SHA-256 | `1743ebdaa986825ad24986f0d696d2d15f46e69a2d4b3648fc850bfb29dcecdc` |
| 退役/失败撤回测试 SHA-256 | `cc565a2e4fd9da815f56cebaf790700333befff03740fa57f0e46ba62fa08bac` |
| 准备脚本 SHA-256 | `ccc85ea837894f7bb4a9f5262390ef78592e888bc3eb5478054096f72246663f` |

准备、内核入口、退役入口和整体入口均退出0。Docker 自行把原 ip_forward=0 变为1，
保留其默认 FORWARD DROP；没有使用提前启用转发的旧夹具。QEMU 以 UID1000/GID108、
无附加组/capability、no-new-privileges 运行，无业务目录/socket/设备映射。

原生内核事务再次验证精确连通、其他来源/端口拒绝、更早管理员拒绝、不同物理 veth
冒用 IP/MAC 仍拒绝、关闭后新连接拒绝、同一原已建立 socket 无法继续访问，最后移除
原对象并恢复原 Docker/管理员规则。

`TestNativeForwardingRetirement` 的主测试与七个子项各有唯一 run/pass：活动凭据
拒绝、撤权后空清单正例、停止但非托管实例拒绝、成功操作记录自然消失、物理端口
拒绝、绑定确认退役及重复退役/证据保留。实际 installed 后端操作真实 Incus trust、
空实例和网桥，持久化原宿主状态，并移除原回执所属内核对象；released 墓碑和原授权
保留，依赖阻止仅在核验成功后解除。

这不是完整产品部署：Core stopped 元数据及原 grant 是准备的夹具，没有运行公开
审批、Core stop、真实 booted guest 或 Forgejo 工作流。同程序额外执行的失败出口
与补偿失败仍是故障注入单元测试，不因在 Linux VM 内运行而升级为实机故障验收。
内核包总25对 run/pass 包含便携/解析回归；退役包总14对中8对属于原生主/子测试，
另6对属于上述单元回归，不能混作39项产品验收。

公开归档：
`/data/anas-incus-retirement-20260926.5p9Wxh/retirement-r3/reports/public-evidence.tar.gz`

SHA-256：`9053694b781fc67bfa94301b0fa7acc524ca8fc7f3e52e153589eb767aecbb6b`。

独立复核逐项检查原 JSONL 的包名、唯一且有序 run/pass、最终 package pass、无 skip/fail
及实际退出码；同时解析归档中的内核终态和退役回执，不只依赖 summary。归档不含
私钥、宿主 connection/state 或 Core 私有环境。独立结果位于同一 reports 目录的
`independent-verification.json`。

正常关机已确认，QEMU退出0、无强制退出、无清理错误。物理宿主原24个容器、17个网络、
卷、Docker服务身份及配置/unit、nft、双栈路由、named netns的十项前后对照全部相同。
失败和成功轮次均保留原始状态，不通过清理失败实验盘制造通过。

最终再次核对没有 QEMU 进程，22270/22272/22274/22276/22280/22282/22284 均拒绝
TCP 连接，第三轮 QMP socket 不存在。远端全部交付二进制及监督器输入符合冻结清单；
本地253个源码输入仍一致。原暂存 diff 摘要保持不变，开工快照中的原文件没有缺失。

## 本地回归与未关闭目标

最终 Go 源码通过全仓 `go test ./...`、五个相关包的 `-race`、全仓 `go vet ./...`，
Incus Python 81项与 Forgejo Python 44项。Module 双语源及生成、Contract 文档、需求
覆盖/状态、计划状态、文档状态、共享构建及升级目录门禁执行通过；不替代前述原生
范围外的验收。当前只构建 Linux amd64 原生工件，未新增 ARM64 或 VM 隔离档业务运行结论。

M10a/M10/M11/M12保持未完成。下一步是把已批准租约接入现有运行 owner 的周期刷新、
停止屏障及崩溃/重启撤回，再在默认 Docker DROP 独立环境完成真实 guest/Forgejo 和
跨租约、身份替换等完整反向隔离。不能仅去掉 `forwarding_lifecycle_integration_unavailable`
或给消费者增加 NET_ADMIN 来绕过这些缺口。
