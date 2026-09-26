# 默认 Docker 转发的可观测性与受控报文验收

状态：只读诊断与完整受控报文实验已验收；默认转发的生产自动授权适配尚未完成。日期：2026-09-25。

继续实际 `/Users/whl/Documents/anas`，HEAD 为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`，保留全部原有暂存与未暂存改动，不提交、
不推送。重新读取需求/计划索引、两份 Incus 主题文档、宿主供给架构与最新停止/镜像记录。
指定服务器仍是 `ssh whl@ln.hlong.wang -p 2200`；不联网搜索，不操作其原有业务容器。

## 接续事实与范围

仓库已有后续 `policy-loader-r1` 和 `forgejo-stop-r10` 的真实终态：镜像与十阶段联合停止
已经分别通过。当前重新读取第十轮 supervisor 与宿主对照，确认正常退出0且全部对照项
相同；开始本轮时没有运行中的 QEMU。未重复修复已经解决的旧构建错误。

仍未关闭的是默认 Docker 转发兼容性。此前停止夹具明确在 Docker 首次启动前启用转发，
第八轮只证明外部工作流镜像下载超时；不能把这两轮当成单变量因果实验，直接断言生产
根因或插入全局 ACCEPT。详见[此前分层记录](2026-09-25-forgejo-stop-forwarding-continuation.md)。

## 产品侧只读观察

`internal/incusprovision/forwarding.go` 读取固定 `/proc/sys/net/ipv4/ip_forward` 及
`nft -j list ruleset`，仅公开 IPv4 路由开关、相关 filter base chain 数量、是否观察到
DROP policy 和 Docker 用户链存在。不会公开原防火墙规则、地址、注释或计数器。
额外读取限五秒和1MiB；父取消立即保留，缺工具/读取失败/未知schema均记为未观察到，
不借观察失败修改规则，也不阻断无关控制面维护。

JSON 拒绝重复字段、大小写别名、null/缺失关键事实、未知策略、矛盾或重复链、超大与
尾随内容。只总结完整观察；部分观察不能变为“未发现drop”。没有drop policy也不等于
任意规则允许、iptables legacy已覆盖或实际流可达；IPv6没有因此被宣称验证。

计划增加固定 warnings：`guest_egress_unverified`、`ipv4_forwarding_disabled`、
`ipv4_forwarding_unobserved`、`ipv4_forward_filter_drop_observed` 或
`ipv4_forward_filter_unobserved`。`compute_ready` 始终不由此变true。过滤器policy和
路由开关变化进入观察摘要，业务流量计数不进入，避免正常流量导致确认不断漂移。
测试先复现新增行为缺失，再完成实现；相关供给、hostaction、jobexecutor回归已通过。

## 独立受控实验

新入口 `server-incus-forwarding-e2e.py` 使用真实 Docker/Incus bridge、内核路由/nft，
但两端是明确的网络namespace进程，不是booted guest或Forgejo工作流。固定本地 HTTP
端点先由VM本身验证，排除公网镜像仓库、DNS和证书波动。准备时要求原始ip_forward=0，
由新实验Docker默认启动变为1并设置DROP；不使用此前“预先可路由”夹具。

它对同一源/目的/端口验证：早期nft ACCEPT计数增加但随后Docker默认DROP计数也增加；
两条精确临时DOCKER-USER许可后成功，其他源与端口仍拒绝；早于许可的显式拒绝仍获
执行；撤销许可后再次拒绝。后者仅是实验干预，不是生产授权适配，也不证明源地址防伪、
动态租约身份、TTL、重启、已有连接撤销或IPv6。产品没有新增写Docker链的执行入口。

新环境 `incus-forwarding-r1` 位于 `/data/anas-incus-20260924.Hd6AM0`，VM为
`anas-incus-host-b92a86`、22250、Ubuntu26.04 amd64、2CPU/2048MiB。只读基础镜像
摘要为 `4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc`。
QEMU以临时UID1000/GID108、无附加组/capability和no-new-privileges运行，没有业务
socket/目录映射。220个源码输入与本轮实际二进制/脚本摘要绑定，属于dirty实验工件。

本轮实际终态、报文计数、公开归档和正常关机/宿主对照将在取得后追加。仅存在脚本、
本地五项离线guard或通过Go回归不代表这个原生实验已经成功，更不关闭M10/M11。

## 首轮终态与夹具前置条件修复

第一轮准备成功：实际Docker 29.1.3把原始ip_forward=0变为1，FORWARD确为DROP。
但完整入口在`controlled_endpoint_responds`阶段以`TimeoutExpired`失败，没有取得
后续报文因果证据。已记录的实验规则/namespace/Docker库存恢复相同；外层正常关机、
QEMU0、无强制退出或清理错误，物理宿主全部对照项相同。公开归档摘要为
`59827a4d1c7c5183e682cb74454e86bdafcb47e9dba8af9c81369c83c8e2a488`。

独立`inspect-forwarding-r1`只在新写层读取原轮journal、官方包状态和接口列表，没有
运行旧测试或修复失败状态。包清单实际显示dnsmasq-base未安装；新的实验准备改为显式
安装并读回它。生产发行版配方此前已包含该依赖，因此这不是再次修复生产配方。旧日志
没有精确到失败子命令，不能把缺包直接写为这次超时已被完全证明的原因。

另有本地行为回归确认Python HTTPServer在绑定数字地址时仍会反向DNS查询。实验端点
现在使用明确绕过该查询的TCP bind，只保留真实HTTP服务；探测总期限覆盖连接及读取。
增加有界命令阶段/退出/耗时记录，原始错误仍只写私有目录，不加入公开归档。这是排除
额外测试依赖，不把超时当作预期DROP或改变生产网络结论。最新七项离线guard通过。

诊断层正常关机、QEMU0，原首轮磁盘SHA-256仍为
`5b2d4315e532e5f8f1dc9f3012b2dcdd9d8b26a406a5ca2db8f766063896db1e`；物理宿主
全部前后对照相同。新第二轮为`incus-forwarding-r2` / `anas-incus-host-cab706` /
22254，使用同一只读Ubuntu基础镜像的新写盘，2CPU/2048MiB。`v2`保留相同生产测试
二进制，单独绑定更新的夹具和准备脚本摘要，不把它们归因于第一轮的冻结工件。

## 第二轮命令输入证据

第二轮仍退出1，但新增的有界命令记录已明确区分阶段：两个本地HTTP端口分别在77ms和
46ms返回正确标记，实际超时的是`incus --force-local network create an-fw-br`（60060ms）。
不能继续把HTTP端点、反向DNS或缺少dnsmasq当作这一轮未完成的原因；后续报文阶段尚未
开始，亦无防火墙因果结论。该轮规则/namespace与实验Docker库存恢复，VM正常退出0，
无强制退出或清理错误，全部物理宿主对照相同。公开归档SHA-256为
`b2966dc980c800cdbf4b1efbd43312cdbf561d670c6ae58e76b3c156459eaf27`。

进一步用真实子进程和始终保持打开的父stdin pipe复现了通用命令包装器继承输入的缺陷。
`capture`在没有显式输入时现在给`DEVNULL`，有nft输入时才建立专用pipe；不是用更长
超时掩盖等待EOF，也不改变生产Incus命令或防火墙权限。8项离线回归通过，包括真实
继承管道反例；生产Go测试二进制保持原样，夹具`v3`另有来源链与摘要。

第三轮`incus-forwarding-r3`为全新`anas-incus-host-a54332`、22256、同一校验过的
Ubuntu26.04基础镜像、2CPU/2048MiB。准备脚本与第二轮相同，唯一执行修复是命令输入
流关闭；是否完成创建与完整报文门禁须以本轮实际终态为准。

## 第三轮到第四轮：完整原生结果

第三轮已通过Incus bridge创建和本地端点准备，命令输入修复取得实际执行证据；之后在
实验nft规则加载阶段失败，未把规则加载失败当作DROP反例。原归档SHA-256为
`c4d4157030ea6620131e6587a5b5b45f3988d2fe44c23c0731303c8e4aa5ff82`。该轮仍正常
关机、QEMU0、无清理错误，原始宿主全部对照相同。独立宿主namespace语法检查请求被
工具拦截，没有执行；不把它记录为已验证结果。

实验nft事务随后采用显式分行/块边界，并在新VM内实际加载之前增加`nft --check`。
错误只输出固定诊断类别，原始stderr继续保留在私有目录；这不改变授权范围或实验
默认DROP。第四轮`incus-forwarding-r4` / `anas-incus-host-95bad3` / 22258 使用全新
Ubuntu26.04 amd64、2CPU/2048MiB、同一校验过的基础镜像和相同生产测试二进制。

第四轮完整 **9项门禁全部通过**，原生入口与独立VM owner均退出0：

| 实际检查 | 结果 |
| --- | --- |
| Docker 29.1.3默认启用IPv4转发并留下FORWARD DROP | 通过；没有预先设置ip_forward或改ACCEPT |
| 固定本地HTTP双端口、实际Incus bridge与namespace客户端 | 通过；网络创建1288ms，不依赖外网服务 |
| 早期nft ACCEPT与后续Docker默认DROP | 早期counter命中2包，后续DROP计数增加，客户端请求失败 |
| 精确临时DOCKER-USER双向TCP许可 | 同一客户端/目的/端口成功，默认policy仍DROP |
| 另一来源和另一端口 | 继续拒绝；本机访问另一端口成功，排除服务未监听 |
| 早于许可的显式DROP | 仍获执行；移除该实验DROP后精确流恢复 |
| 撤回两条许可 | 同一流再次拒绝，原默认DROP计数增加 |
| 实际生产只读观察器及计划投影 | 精确原生测试run/pass与package终态完整，无skip/fail |
| 实验清理 | 精确原iptables规则、namespace与Docker库存恢复 |

实际生产观察值为IPv4 routing=`enabled`、filter=`drop_observed`、3条相关base chain，
Docker用户链存在。没有把控制连接成功或早期ACCEPT标成guest可用。此次因果结果只对应
这些真实内核/软件版本与明确的namespace端点；不冒充booted guest、Forgejo工作流、
IPv6、legacy firewall或全部默认部署的验收，也不倒推较早外部下载超时的唯一原因。

公开归档：
`/data/anas-incus-20260924.Hd6AM0/incus-forwarding-r4/reports/public-evidence.tar.gz`

SHA-256：`5c5c177bf02ba07f1dd727aadec6f4833eb6df518ccedf0e8752d853c5b4efa7`。

独立复核重新读取归档，不只依赖summary：确认只含五个公开报告文件，9项无重复/漏项，
原生事件的包名、实际test run/pass与最终退出一致，固定nft语法检查退出0，源码与交付
工件摘要一致。`v4/source-manifest.json` 的SHA-256为
`545c9dc49eb690c273e3ba41076b266506b66a7684cb858209a786045452ecab`，绑定220个
未变化的源码输入。原生测试二进制SHA-256为
`354b117f8b4eba8963ee2ce7ad2395316af079380609c3b854ae4e161ddf70c7`。
独立记录位于同一reports目录的`independent-verification.json`。

四轮测试VM和独立诊断写层均已正常关机，第四轮QEMU退出0，无强制退出/清理错误。
复核时不存在QEMU进程，22250/22252/22254/22256/22258均无监听。物理宿主原24个容器、
17个网络、卷、Docker服务/配置/unit、nft、双栈路由与named netns前后全部相同；没有
修改既有业务Docker。旧失败盘和报告保持，不删除失败记录来制造完成。

本轮全仓Go测试、相关三包竞态、全仓go vet通过；最新 **81项Incus Python与44项Forgejo
Python** 回归通过，ARM64仅为交叉编译。Module/Contract文档源与状态/覆盖、共享构建
静态和升级目录检查独立执行，不用来替代上述原生证据。

## 最终解析边界补强

第四轮之后进一步补充异常元信息回归，实际复现大小写别名覆盖schema值、重复metainfo、
未知元字段和未知地址族可能被忽略为“no_drop_observed”的问题。虽然此值仍不授予
compute就绪或规则写权限，也不应把不完整结构作为可信诊断。现在元信息使用同一闭合
字段解码器、只接受一个schema记录，并将未知地址族整体记为未观察到。五组新增负例
与三个相关包回归通过；既有合法规则与维护动作的较短调用方期限不变。

为避免把旧二进制的通过归因于新代码，`v5`重新编译并绑定220个当前输入，仍使用完全
相同的报文夹具和准备脚本。新测试二进制SHA-256为
`d433d09dc2eac187791916fc8c098bb6bd17fe829b28ad2a721a2f642cb7d966`。第五轮是全新
`incus-forwarding-r5` / `anas-incus-host-8671ce` / 22260；第四轮归档、原生结果及
独立复核不被覆盖。第五轮的实际终态与最终全部门禁单列记录。

第五轮随后以最终代码再次通过完整9项门禁和原生读取器测试，两个执行器均退出0。
网络创建1210ms、nft语法检查退出0，实际读回仍为3条相关base chain、DROP与Docker
用户链存在；精确许可、其他流拒绝、更早DROP和撤回的原始事件均完整。新增解析拒绝
规则没有破坏实际标准nft输出。原生读取调用真实生产collector；计划warning投影调用
生产buildPlan，但其他preflight字段来自测试夹具，不把它描述为完整已安装HTTP审批验收。

最终归档：
`/data/anas-incus-20260924.Hd6AM0/incus-forwarding-r5/reports/public-evidence.tar.gz`

SHA-256：`6503fde100294c9ce4412108b4a427e63ce68dae40c3e392073c22c75e3e55b8`。

最终输入清单SHA-256为
`d0ae6c14ebc38a2c65e28194473d3026917946f0508ee2a8b4665f86a116bb5b`。再次独立读取
五个公开文件、测试run/pass与最终package结果，检查220个当前输入及实际交付工件
摘要一致；结果保存在该轮`reports/independent-verification.json`。没有归档私有错误
输出、SSH密钥或其他私有目录。

第五轮正常关机、QEMU0，无强制退出或清理错误，原物理宿主各项对照全部相同。最终
复核时没有QEMU进程，22250/22252/22254/22256/22258/22260均无监听。所有本轮失败、
成功及独立诊断环境都已结束，不遗留后台测试等待下一次对话。

最终代码重新通过全仓Go测试、三个相关包竞态、全仓go vet和Linux ARM64测试程序
编译；ARM64仍不是原生运行。81项Incus与44项Forgejo Python回归保持通过。当前
Module/Contract生成、545项需求覆盖、状态索引、共享构建静态、升级目录与暂存/未暂存
差异检查通过，后续仅追加本终态文档记录。

## 尚未关闭的生产目标

当前产品改动仅为只读诊断，不包含自动修改DOCKER-USER的生产入口。实验使用的固定
/32与端口规则不是可直接发布的租约授权：生产仍需绑定真实实例/接口身份、防源冒用、
明确出站许可、并发与重启收敛、退出及过期撤回，以及原管理员拒绝规则的保留。不能
据本实验改成全局ACCEPT、按bridge前缀放行或重写既有Docker配置。M10/M11、完整业务
部署、ARM64/VM、双栈与正式发布仍沿原计划分别关闭。
