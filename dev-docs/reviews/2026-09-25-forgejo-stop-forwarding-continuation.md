# Forgejo 停止事务与出站前置条件的分层验收

状态：显式可路由夹具的联合停止十项已通过；默认 Docker 转发共存与完整业务栈尚未关闭。
日期：2026-09-25。

接续实际 `/Users/whl/Documents/anas`，HEAD 仍为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`。保留原有暂存、未暂存与失败实验；
未提交或推送。全部服务器操作经 `ssh whl@ln.hlong.wang -p 2200`，未联网搜索，
没有操作物理宿主已有 Docker 容器。前置记录见
[固定策略加载器](2026-09-25-forgejo-fixed-policy-loader.md)和
[Core 停止前清理](2026-09-24-forgejo-core-stop-barrier.md)。

## 第八轮实际失败原因与证据边界

重新核对第八轮 784 个源输入，开始接续时与仓库一致。其已知终态是
`actual_workflow_running/payload_not_running`，尚未执行 Core 停止或口令停用。
原始失败盘与全部已有报告没有改写。

此前只读任务日志检查没有解压 `.log.zst`，因此“没有错误行”不能解释任务失败。
本轮独立 `inspect-forgejo-stop-r8-zstd` 使用有界 libzstd 解压，仅读取这份隔离工作流
的状态和日志，不访问账号口令、token、会话列。实际日志显示 Runner 13.2.0 已领取
task 1 并准备工作流；拉取固定 BusyBox 镜像后约123秒返回 `i/o timeout`，payload
从未启动。该结果区别于旧镜像的 userns/engine 准入失败，不把包级镜像验收推导为
完整 Docker/Incus 运行路径已经可用。

本次压缩日志诊断退出0、正常关机、QEMU0，物理宿主各项对照相同；原失败盘摘要仍为
`0f7d10bc5c279d8f480937af0346a2e98c0609301b7245a33e45b3a28218fbd2`。
只保留经过脱敏的固定实验日志片段，不将原数据库或私有状态加入公开归档。

后续受控端点诊断中，重启写层的 `FORWARD` 基础策略被观察为 `accept`，但这**不代表**
第八轮原内核当时的策略。冷启动顺序会改变 Docker/Incus 的运行环境，不能拿重启后的
状态补写原始网络事实。一次诊断未通过 SSH 准备，另一次触发了 Incus 要求实例显式
memory quota 的拒绝，第三次在有界诊断期限内未完成；它们都不是连通性或修复成功证据。
这些写层均已独立正常关机并验证原盘不变，未把局部观察标为完整因果证明。

目前可确认的是拉取网络超时；默认 Docker 的转发兼容性仍需完整独立网络实验。
没有将外部镜像仓库故障、DNS、转发链默认策略或原生启动顺序中的某一个猜测直接写成
已证明的产品根因，也没有在产品中加入全局 ACCEPT、名称前缀放行或修改 Docker 配置。

## 验收程序修复

新增回归实际复现 `core_stop` 的事件判断仅需一个测试 pass，就会接受缺少 run、缺少
包终态、其他包的同名事件及额外测试结果。现单独验证正确 package、唯一且有序的
run/pass、唯一最终 package pass、真实整数退出0；截断、重复、skip/fail、含糊或畸形
数据均拒绝。它是监督证据完整性修复，不改变 Core、Hook、controller 的退出策略。

原生入口另外只读记录当轮 `ip filter FORWARD` 的基础策略与 `ip_forward`，作为新的
`forwarding-before.json`。记录本身不是网络准入、连接成功或安全授权证明；只有实际
工作流、正在运行的payload与后续清理阶段可以满足相应门禁。查询结果不完整时不编造策略。
包括上述新负例的10项停止夹具离线回归通过。

## 第九轮显式可路由环境

为独立验证停止事务，新增全新 `forgejo-stop-r9`，VM 为
`anas-incus-host-495cc7`，SSH回环端口22246，Ubuntu26.04 amd64，KVM双核/4096MiB。
只读基础镜像摘要仍为
`4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc`。

该夹具在**空的新VM**首次启动实验 Docker 之前显式启用 IPv4 转发。它模拟已经具备
路由前置条件的NAS；不改物理宿主sysctl、Docker配置或容器，也不把这一步称为产品对
默认 Docker DROP 的修复。既有宿主控制桥、mTLS、guest隔离和工作流准入不变。
准备脚本摘要为
`71fabf02648acb05ee3180c8ae6fb5e9813df5b95621eb8828a94a4524bf8ab7`，已记录到lab身份。

本轮 `0.0.0-native.20260925.502` 工件绑定784个构建前后不变的源码输入。
沿用独立完整验收的 `policy-loader-r1` 镜像，fingerprint为
`16e440ee8ad764f7faac9bcdaed33388466cfcefd102ffbd0ed1d67982c1d5b1`，导出归档摘要为
`628ef2b669477823a05bffdc8c8c19e6f963b4a9c94a6328b91afc6f3e34a8e4`。
没有烘焙新revision、手工修改guest、预填任务状态或替换生产controller。

该轮要求实际payload运行，再经生产Core stop、旧冻结Hook、controller清理、默认Compose
移除、账号口令停用与重新启用；错误controller状态仍须阻止移除和撤权。完整终态和
独立归档/正常关机/宿主对照以随后记录为准，不能由本轮的可路由前置条件推导。

即使本轮通过，范围仍是明确的生命周期夹具：SQLite、准备的workspace与TLS传输镜像，
不是公开CLI完整IAM/PostgreSQL业务部署，也不关闭M10/M11的默认转发、生产入站和
其他平台验收。

## 第九轮实际停止与注册记录计数

第九轮已取得真实payload正在执行的证据，随后生产Core停止子进程完成；归档中的
`TestNativeForgejoCoreStop` 唯一run/pass和最终package pass已独立复核。完整父入口
仍退出1，报告 `job_cleanup_missing`，不能据此把后续停用和重新启用标为通过。
公开归档SHA-256为
`7fe7260fd78d0bc531963820afed1baa0ee76a0da6a8382874ce16a9b98e5bf0`。
VM正常关机、QEMU0，无强制退出、无清理错误；物理宿主全部前后对照相同。

接续核对发现清理检查使用 `SELECT count(*) FROM action_runner`，把软删除保留行也
当作有效registration。此前第八轮实际只读数据库已经记录id=1且deleted为正时间戳，
不能删除历史行来让计数归零。新增SQLite回归先复现，再改为只读id/deleted并校验完整
形状、正ID、唯一性、非负整数删除时间与有界清单；仅deleted=0计为有效，缺字段/null/
负值/未知类型均阻止判定。没有更新数据库状态，也不读取token/口令字段。

重新启用、第二次工作流成功且应用在线时，再以真实scope Runner API空清单独立核对。
Core已移除Forgejo服务后不要求调用不存在的API，Actions关闭时也不假设相关端点可用。
原生入口的这些新核验不回填到已冻结运行的第九轮；当前12项离线停止回归通过。

第十轮 `forgejo-stop-r10` 使用全新 `anas-incus-host-d9b971`、22248、同版本Ubuntu
基础镜像和显式可路由夹具，2CPU/4096MiB。实验版本为 `.503`，784个构建前后输入
一致；Core/Hook/controller/helper/Provider与第九轮字节相同，监督器和独立观测增强
分别记录新摘要。准备脚本摘要为
`e83f1cfdcb6cf73c43b5701e06bdf9e8a6f08e92b362e76efc57d4ee2812a40d`。
该轮结果、资源回收与完整宿主收尾仍以实际终态为准。

## 第十轮完整终态与独立复核

第十轮完整入口实际退出 **0**，十个必需阶段全部通过，没有跳过、缺项或重复。先确认
真实工作流payload正在运行，再经生产Core停止、旧冻结Hook、controller退出与资源
回读，验证实例、根盘、有效registration均空。重复stop成功无副作用；关闭后的原管理
口令被真实Forgejo拒绝，独立恢复owner不变。

重新启用保留数值账号id=2，第二个真实workflow成功，并以在线scope Runner API空清单
独立核对清理结果。随后再次Core stop通过。刻意注入的未完成controller状态使Core
停止与账号停用均拒绝，原容器ID、失败状态字节和恢复仍需的管理口令保持；之后仅由
实验owner显式退场，不将该清理称为产品恢复或把失败状态改成成功。

独立解析公开归档验证全部十阶段、五个Core用例（active/repeated/disabled/reenabled/
failed）的唯一run/pass与package终态，以及六个宿主计划/执行job记录。实际准备快照
为IPv4 `FORWARD=accept`、`ip_forward=1`，与显式可路由夹具一致，不宣称默认DROP路径
已验收。两轮并不是单变量网络因果实验，镜像仓库传输本身也可能变化。

公开归档：
`/data/anas-incus-20260924.Hd6AM0/forgejo-stop-r10/reports/public-evidence.tar.gz`

SHA-256：`d49f443c142e4241575d951da6fc5f43e3d57d4c0447f4cb87759411c0ecbd12`。
归档只包含公开JSON/JSONL，没有原环境、账号凭据、SQLite数据库或私钥。
外层精确QMP身份正常关机，QEMU退出0，无强制退出及清理错误；原有物理宿主24个容器、
17个网络，以及卷、Docker服务身份/配置/unit、nft、IPv4/IPv6路由与named netns全部
前后相同。第八/九轮失败盘及独立诊断层未删除或覆盖。

本轮最终全仓Go测试、相关Runner/Forgejo包的非缓存回归与go vet、**44项Forgejo和
71项Incus Python回归**通过。其中停止夹具12项包含本轮新增的证据与软删除负例。
Module/Contract文档检查、需求覆盖、状态索引及暂存/未暂存差异检查分别执行。
本轮未新增ARM64或VM隔离档原生结果，也不把之前交叉编译当作本轮实机证明。

本次关闭的是明确的生命周期联合夹具，不是完整公开CLI的IAM/PostgreSQL业务部署、
默认Docker转发兼容、生产入站、权限最小化、全部故障恢复或正式签名发布。相关大
里程碑继续保持未完成；后续默认转发实验必须保留原策略并独立证明访问与隔离。
