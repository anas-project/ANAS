# Debian 宿主供给触发器与逐包卸载接续

状态：实施与独立原生验收记录。日期：2026-09-23。

后续终态：本记录中的失败轮次原样保留。第三十轮全新 Debian 13 amd64 在补齐空池清单
兼容核验和明确自有 daemon 停止后，完整 25 项门禁、18 个作业/退出均通过；同工件的
Ubuntu 26.04 与 24.04 对照也分别通过全部 25 项。三个环境的归档、正常关机及物理
宿主对照已核实，详见[空池盘点与显式卸载接续](2026-09-23-incus-storage-inventory-compatibility.md)。
这不是对原失败状态的重试或改写，也不表示 ARM64/VM、完整业务部署及失败恢复全部完成。

接续 `/Users/whl/Documents/anas` 的已有暂存和未暂存工作，HEAD 保持
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`；本轮不提交、不推送、不 reset 或重新暂存。
依据是 Incus M10 与宿主动作通道。指定测试入口仍为
`ssh whl@ln.hlong.wang -p 2200`，没有联网搜索；真实变更仅发生在独立 QEMU VM。

## 先前证据的独立复核

前轮最终答复仍停在第七轮，但实际工作树已包含后续源码和原生记录。接续先读取需求/计划
索引、两份 incus-module 与宿主供给架构，再核对实际报告，不按旧答复重做已修复的代码。
第二十二轮浏览器外层结果再次读回确认：test_exit=0、qemu_exit=0、正常关机、无强制退出、
cleanup_passed=true，公开归档摘要与浏览器恢复记录一致。

Debian 第十六、十七轮停在环境准备是历史结果，不能覆盖新增证据。第二十四轮已完成环境
准备、实际 HTTPS owner、共享 job/hostd 预检、跨工作区反例、确认 skip 与重放拒绝；在
真实 `confirmed_install` 失败，监督器仍正常归档、关机并确认宿主基线相同。
其公开归档摘要为 `af33dffa7903417a76baa342db722891a1be0b1355ea578fb85f32057d4785d9`。

对第二十四轮单独 overlay 的受限诊断也已读取。诊断没有重新执行失败的安装、清理 intent
或修改原盘，原盘摘要保持
`e2f4f0eb235389796a8941269822b05f0aab753c5c903940964bc72e6ec7981e`；诊断本身退出 0、
QEMU 正常退出 0、所有宿主前后检查相同。实际包日志表明官方 Incus 及依赖已配置，随后
initramfs-tools 无法写入 `/boot/initrd.img-6.12.107+deb13-cloud-amd64.dpkg-bak`，报只读
文件系统并使 dpkg 退出 1。这不是继续下载失败，也不是证书登记失败。

同一份真实 dpkg 数据确认 Debian 13 的 `incus 6.0.4-2+deb13u10` 是元包，daemon 位于
`incus-base`。Ubuntu 26.04 已记录该拆分，Debian 配方此前未记录；不能靠元包消失就认定
实际 daemon 的卸载及归属处理完成。

## 代码与回归

固定 `anas-hostd@.service` 的 ReadWritePaths 增加 `-/boot`，供官方包触发器更新 initrd；
路径不存在时不影响服务启动。保留 ProtectSystem=strict、ProtectHome、NoNewPrivileges、
有限动作清单、确认、审计和既有能力边界；不授予整个根文件系统写权限，不跳过触发器，
不向 anasd 或 relay 增加 `/boot` 写入。

Debian 的声明配方显式包含 `incus-base`，复用现有逐包归属实现，没有按发行版新增执行
脚本分支。测试先复现缺失写路径和实际 daemon 包归属；补齐后保留已有 daemon、补装
辅助包不得接管、已拥有 daemon 的精确卸载与未托管包保留均通过。旧测试的 Debian
模拟库存同步包含实际 daemon 包，不放宽生产所有权判断。

真实审批入口由 23 增为 **25 项**，增加已确认的逐包删除和重复卸载。完整 dpkg 库存读取
拒绝半配置、待处理触发器、reinstreq、重复或架构别名歧义、缺尾换行等异常。删除前后
严格要求只有根状态逐项记录且原本不存在的包消失；原始包和未托管依赖必须全部保留。
还独立确认 daemon inactive，重复卸载不改变库存；不运行 autoremove/force/remove-all。
新解析/集合回归先失败后通过，局部 Python 共 17 项通过，相关五个 Go 包通过。

## 新独立执行的来源

新 VM 为 `anas-incus-host-e19d64`，远端目录为
`/home/whl/anas-incus-followup-20260923.4p9ob_nk/host-v25`，SSH 仅使用回环 22160。
Debian 官方基础镜像再次按完整 SHA-256 和 qcow2/无 backing 校验；全新独立写盘，不复用
第二十四轮状态。QEMU 单核 KVM/2048 MiB、UID 1000/GID 108、空附加组/能力和 NNP。
实验 IPv6 仅由 QEMU 用户态网络提供，不修改物理宿主桥、路由或已有 Docker。

`/tmp/anas-incus-debian-20260923.z9Bcwl/product-v1/source-manifest.json` 绑定编译前后
未变化的 452 个源码/嵌入/回归输入，明确记录 dirty checkout；实验版本为
`0.0.0-native.20260923.5`，不是正式签名发布。实际工件摘要：

| 工件 | SHA-256 |
| --- | --- |
| anas | `117f2c086612220cd17e16ec34802dd9d7148c902cffaca06530f36ca46e9288` |
| anasd | `3ac11c9464d544791adba3ce15ea49637b6c13c9b4b043dd37421675879b32f4` |
| hostd | `74a478fcd0450399fa0efb7bb1d0ebd5ea0d7cebfe2b6a2954381917b398c564` |
| hostd 单元 | `3593a0b2750d0ee36fca035d63d5a73827f606d4b31fde80e9a4665adbf0052a` |
| 审批入口 | `5bfdef3f789e44c610e4330dc131c0d3370ec452d49d0844f9479cc3318f6b64` |

完整新原生结果、公开归档和外层正常收尾在取得终态后单独追加；上述本机回归与工件
校验不是这份新门禁已经通过的替代证据。

## 第二十五轮终态与时序修正

真实安装、配置、登记、四项消费者控制桥验证与默认卸载均已通过，说明 Debian 的
`/boot` 包触发器修复已经获得真实执行证据；不是仅靠修改单元或模拟测试判定。该轮总计
18 个通过阶段，但重启后的旧 token 重放断言失败，所以完整 25 项仍不计通过。

源码复核确认，确认消费在读取 ledger 之前先拒绝过期的 plan binding。该轮官方包获取
已超过五分钟，因此不能再用最初 skip 的旧 token 检验“尚未过期但已被消费”的错误码；
不能把任意 409 或认证失败当成该门禁通过，也不为测试改动生产拒绝优先级。

测试改为在长时包获取前立即执行同一 consumed token 的真实重启/重放检查，检查前后
都确认 plan 仍在真实五分钟窗口内。保持精确 `confirmation_consumed` 断言，另一个未使用
token 的自然过期仍单独验证 `confirmation_expired`。新增边界/执行顺序回归先失败后通过，
局部 Python 用例增为 18 项。产品八个二进制/单元摘要完全未变，只更新测试入口及测试
manifest；`product-v2` 记录父 manifest 和新的 runner 摘要
`a3708fe018a2326df0fbfe089b6a8caee3116dcfbd04ec30bec4b149ae71fbb3`。

第二十五轮公开归档摘要：
`622b1e11d0ca7ae26a1ebf2aff518eebcd3f5e68fbda31c1b834eb4c8d987763`。
外层记录 test_exit=1、qemu_exit=0、normal_shutdown_requested=true、无 forced quit、
cleanup_errors=[]、cleanup_passed=true；物理宿主所有独立对照项一致。旧报告及盘保留，
新第二十六轮 `anas-incus-host-dd55ba` / 22161 使用独立干净磁盘重跑。

## 未适配系统的独立只读证据

在指定物理主机以 UID 1000 运行当前编译的 `incus-host-preflight`，只读固定系统文件，
不连接 Docker/Incus、不安装或配置宿主。真实 Ubuntu 22.04/jammy 返回
distribution_not_adapted、disabled、compute_ready=false、runtime_verified=false，
无配方；`--skip` 返回 skipped 且不读取宿主发行版/KVM/systemd 信息。两次均退出 0，
固定二进制摘要为 `1383ce48d4de45e364bef7c1c98b71b5f2f4508e673d8ecf941f8fad5874ef17`。

公开 JSON 与独立前后对照位于远端 `unsupported-readonly-v2/reports`，24 个已有容器、
17 个网络、卷、Docker 服务/配置/单元、nft、IPv4/IPv6 路由和 named netns 全部相同。
这证明实际未适配系统的只读判定及显式跳过，不冒充在该物理主机执行了安装、审批或卸载。

## 本轮本机门禁

产品改动已通过完整 Go 测试、incushost/incusprovision/hostaction/jobexecutor/hostd 五包
竞态、全仓 go vet、65 项 Incus Python 与 4 项浏览器离线 guard；前端完整生成/类型检查/
96 项测试/嵌入构建通过，构建后重新比对 452 个输入摘要均与本轮产品 manifest 一致。
Module/Contract 文档生成和检查、需求/计划状态生成与检查、需求覆盖、共享构建和升级
目录门禁均通过。以上数字对应重启时序修正前的完整门禁，新增时序用例后单独补测。
日志位于本机 `/tmp/anas-incus-debian-20260923.z9Bcwl`，不把本机通过替代原生终态。
