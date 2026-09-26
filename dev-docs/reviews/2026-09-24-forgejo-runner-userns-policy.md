# Forgejo Runner 的 guest 用户命名空间策略接续

状态：原因已核实，新固定加载器镜像的原生闭环已通过；完整停止链路另行验收。日期：2026-09-24；接续：2026-09-25。

接续实际 checkout `/Users/whl/Documents/anas`，HEAD 为
`0b6144887d6c1ecc89847be6d2773f139e8b7f23`；保留已有暂存、未暂存和失败实验。
对应 Incus M6/M10/M12 与 Forgejo 停止前清理的接续，不增加用户可选的宿主安全开关。
测试主机仍是 `ssh whl@ln.hlong.wang -p 2200`，只在新建隔离 VM/写层内测试。

## 实际失败而非启动期限猜测

`forgejo-stop-r6` 的 controller 多次记录 guest 入口退出69。独立只读磁盘观察及随后
独立诊断写层确认：已入队的真实工作流未执行；保留 guest 使用原 `trust-r2` fingerprint，
外层 profile 的 `security.nesting=true`、`security.privileged=false` 均正确。
root subuid/subgid 为 `1000000:1000000000`，guest 映射范围相符；不是映射不足。

guest 的 `user@1002.service` 与 logind 都已 active/running，启动作业清单为空。
实际 Podman 用户服务却不断退出125，日志为 `cannot clone: Permission denied` 和
`cannot re-exec process`。因此没有把31秒引擎准入简单延长，也没有删除准入或提前消费token。

在相同失败盘的独立写层中，Ubuntu26.04内核审计给出了确切拒绝：`userns_create` 无法在
Incus 的子 AppArmor namespace 中找到 `unprivileged_userns`。宿主限制开关为1，AppArmor
启用；Incus 生成的外层策略已经有 `userns,`，没有必要为此放宽外层 profile。
两个 guest 普通用户的独立 `unshare --user --map-root-user` 同样得到 EACCES。
宿主和guest系统服务没有额外的 RestrictNamespaces 或 NoNewPrivileges 开关造成此拒绝。

## 最小策略对照

诊断仅向这个测试 guest 的独立 AppArmor namespace 加载一个固定策略，匹配
`/usr/bin/podman`，显式允许 `userns`。加载后首次真实 rootless API 查询成功；普通
`unshare` 仍得到同样的权限拒绝。全局 userns 限制仍为1，原 Incus profile 未改变。
没有禁用 AppArmor、修改sysctl、使用privileged容器、raw.lxc或外层unconfined。

策略的 `flags=(unconfined)` 只保留 **guest 内层原先未施加MAC规则的基线**，给这个固定
程序补足namespace创建许可；它不解除宿主叠加的Incus约束。它不是通用guest命令入口，
也不是对其他程序或全局用户命名空间的放开。

上述对照在 `diagnose-forgejo-stop-r6-policy-proof` 下进行，不计作产品验收。VM正常关机、
QEMU退出0、无强制退出/清理错误，物理宿主全部前后对照相同。原失败盘SHA-256仍为
`6c8323a2fdd715114f8c295a4f09dc93fb316e94a76db77d0844c498f256942f`。
详细的前置只读结果在 `diagnose-forgejo-stop-r6-engine-3` 和 `-4` 的独立报告中；更早两个
诊断未通过空库存前置检查，没有创建额外guest，也不作为成功诊断记录。

## 已编码的镜像交付路径

新固定源为 `anas-forgejo-podman.apparmor` 与 `anas-forgejo-podman-policy.service`。
四个架构/隔离档的 distrobuilder 配方都冻结原样文件，安装官方 `apparmor` parser 包，
并保持公开目录0755、源文件0644/root所有。单独的 `provision.sh` 同步采用同一来源。

策略位于 `/usr/share/anas/forgejo-runner/podman.apparmor`，不进入通用AppArmor自动加载
目录。固定guest oneshot在AppArmor启用且内核提供该userns调节入口时加载它；原engine
user manager显式Requires/After此服务。加载失败不能忽略，15秒上限不变成后台成功。
没有该内核机制时不引入该内层策略，但原宿主/guest安全约束及引擎实际准入仍照常执行。

新版镜像原生验收增加一个必需阶段：核对固定文件身份与实际启动单元；在受限宿主上，
单元必须已自动成功执行，普通guest程序仍无法创建userns，宿主开关保持。原有镜像/设备
围栏、实际rootless OCI create/exec限额和真实工作流矩阵仍需完整通过，不能只用API成功
或本轮诊断覆盖它们。原生执行不手工加载策略、不改旧镜像、不改已有revision。

## 新实验身份

`runner-policy-r1` / `anas-runner-bake-7b64af` / 22210 是全新Ubuntu26.04 amd64 VM，
4GiB/2CPU，独立可写盘，基础镜像摘要仍为
`4908fb59ccd4e87ae4e8e973b7ef56f535448eacb24a87fd787270c0048987bc`。
原Runner13.2.0与Forgejo15.0.7测量输入不变；新构建输入清单包含127个仓库文件。
配方的下载镜像覆盖仅为此前记录的Debian官方到TUNA的传输选择，官方验签保留；仓库
默认源不变。新修订名为 `policy-r1`，不把实验构建冒充正式签名发布。

实际新镜像、十项镜像必需事件、工作流和物理宿主收尾以本记录后续终态为准。此前Debian
上`trust-r2`的九项与工作流通过，不能替代Ubuntu内核这次新增的策略验收。

该VM安装的官方distrobuilder为3.2，执行文件SHA-256为
`c095ea24c87d4a95dce7c0515534583eed25dcd7585aa6027c2fa2aff3856027`。
当前新配方SHA-256为
`a4ab2ee5581d0f584a3bc3f96eac3449f02d16c21d62aa1f7c7af6b2c5f93bfa`；默认未覆盖源的配方
SHA-256为 `9586bd796ed183beb56437df5d7046909ba8d6d90dfd44f438be3c53dc0bc7a6`。
完整输入/实验脚本/官方Runner与Forgejo工件的测量记录均在该轮src清单中。

## 本机门禁与范围

全仓Go测试、computeimage/computeclient/controller/Hook竞态、全仓go vet、39项Forgejo
Python与68项Incus Python回归均通过。Linux ARM64的镜像工具、computeclient测试程序和
controller测试程序编译通过，不计作ARM64原生验收。Module/Contract文档生成检查、需求
覆盖、状态索引、共享构建静态门禁、升级目录及暂存/未暂存差异格式检查通过。

源码再核对：冻结的127个输入中，运行代码和测试入口未改变；构建后只更新Runner README。
这些本机/交叉编译结果不替代新镜像自动加载、OCI及联合停止的原生结果。

2026-09-25 后续终态：在处理构建keyring、chroot TMPDIR及发行版完整profile加载器
带入通用userns许可的问题后，新 `policy-loader-r1` 已通过全新Ubuntu26.04 amd64的
实际build/export/reuse、十项镜像/内层策略/OCI门禁及五种真实工作流。普通程序仍被
拒绝，原始事件与归档、正常关机和物理宿主基线已独立核对。该结果仅归属于新的明确
fingerprint，不追认本文早先失败revision或诊断写层；详见
[固定策略加载器终态](2026-09-25-forgejo-fixed-policy-loader.md)。

## 2026-09-25：已产出镜像与发行版自动加载器边界

后续已完成引导验签和chroot环境修复，`policy-tmpdir-r1` 真实构建、导出及重复复用
成功，fingerprint为 `76adc973d1a6d3bc690dd794bdbed014ae19ea593a337b87a8fd7f1fcc7a799c`。
但启动验收在普通userns拒绝探针失败；固定Podman单元虽已active/exited/success，不能
据此证明没有其他权限授予。前五个镜像子项通过，后续rootless/OCI/工作流未运行；该
候选仍未准入。完整导出与公开归档分别有摘要、正常VM退出和全部物理宿主对照，见
[chroot环境接续](2026-09-25-incus-build-chroot-environment.md)。

对精确导出rootfs的只读解包确认官方AppArmor包还带入 `unprivileged_userns` profile，
内容为complain模式并显式允许userns，包的 `apparmor.service` 被启用并加载整个目录。
这不同于原诊断只向guest namespace加载单个Podman profile。没有把拒绝门禁删除或
改成接受任意非零退出，也不把测试的错误消息当作已取得精确errno的证据。

新增四个目标均冻结的 `anas-apparmor-loader.conf`，把guest AppArmor服务的启动与
reload都限定为同一个固定parser调用，只加载公开Podman策略。服务不禁用、不mask，
原有不卸载profile的停止行为保留；包文件原样保留，宿主内核限制与外层Incus策略不变。
两个loader的实际退出、固定命令与普通userns拒绝仍必须在新镜像原生门禁中通过。
先补回归复现配方缺少此约束，再完成代码，相关包回归已通过；新镜像结果尚不由此推导。

两个独立loader诊断均使用原候选磁盘的新写层，原盘SHA-256保持
`facabecc967a2e60dbc039ae379ad1f2f06fe5c27c2f5d00939daf1f3970d143`，正常关机、QEMU0且
宿主全部对照相同。第一轮在Incus测试命令失败，未到策略比较；补齐与生产客户端相同
的显式资源限额后，第二轮实际观测普通 `unshare --user --map-root-user` 返回1，但
错误为写 `/proc/self/uid_map` 的Operation not permitted，而不是创建userns的EACCES。
发行版fallback与固定Podman策略均确实加载。因此原测试的拒绝未得到确认，不能把任意
非零退出当作正确的userns隔离，更不能声称该命令已成功完成uid映射。

第二轮诊断自身错误地预期该命令完全成功，故未执行对照修改，保持失败记录。第三轮
在新的写层增加不带uid映射的 `unshare --user` 观测，将命名空间创建与映射明确分开，
再比较另一个实例首次启动前仅安装固定loader的效果；这仍不是新不可变镜像验收。

## 2026-09-25 接续：第一轮构建失败的证据

重新读取实际终态：`runner-policy-r1` 的安装准备、Incus 启动与归档初始化成功，但
`image-build` 退出1，公开错误只确认最后观察阶段为 `hooks`。没有有效新镜像，因此
没有运行新增的自动策略加载门禁，也未运行这一修订的工作流或停止链路。公开归档
SHA-256 为 `9ac1283b9d4d036596067551c3785596238ad16884726a1a3f621d81891d16e1`。
远端同目录中20字节的 `runner-export.tar.gz` 是失败导出的空流，不能作为镜像交付物。
原监督器已经正常关机、QEMU退出0，物理宿主各项对照一致。

保留失败盘经独立诊断写层和只读raw副本核对，未重开原归档里的失败revision执行构建。
原磁盘SHA-256保持 `09b796a8661a0479414e07f589f6352eed6569351c1b5484d99097bf069c6b9f`；
成功读取的诊断VM正常关闭、监听消失、宿主对照一致。离线副本只通过只读loop读取，
没有挂载、重放或修复原文件系统，loop已解除。

实际attempt保留的recipe摘要为
`8533f64e021ce11db8d4d59363da9da9a5f52831d7d94917e81b868eb915380c`，包含既有builder与
Runner的provenance注释。Ubuntu构建环境安装了官方debootstrap与Ubuntu keyring，但没有
`debian-archive-keyring`包或对应keyring文件；这是一项单独待核实的验签前置风险，不能仅据
此断定本轮Hook失败的原因。后续诊断在全新的output/cache下运行同一测量builder与recipe，
只为取得具体错误，不记录发行版本、不修复旧attempt、不把诊断构建当作产品验收。
